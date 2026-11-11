//! Mac VMM confinement (architecture §3.2): a deny-by-default Seatbelt
//! profile the worker applies to itself before it opens any VM file.
//!
//! Allowed: read the system frameworks/libraries the framework loads; read
//! the assigned template objects; read/write only the sandbox's private
//! directory and its assigned disks/state files; reach the framework's VM
//! service. Denied: the owner's home and keychains, every other file,
//! ambient network (TCP/UDP/raw) and unrelated Mach/XPC services.
use std::path::{Path, PathBuf};

/// Sandbox extensions VZ issues to its VM service for a macOS guest
/// (observed on macOS 26.5 with a reporting profile). Only these classes.
pub const MACOS_GUEST_EXTENSIONS: &[&str] = &[
    "com.apple.virtualization.extension.usb-hci",
    "com.apple.virtualization.extension.strong-identity",
    "com.apple.virtualization.extension.paravirtualized-graphics",
    "com.apple.virtualization.extension.io-surface",
    "com.apple.virtualization.extension.videotoolbox",
    "com.apple.virtualization.extension.fp",
    "com.apple.virtualization.extension.avp.rtc",
];

/// Mach services the client side of Virtualization.framework needs. Any
/// other global name is refused.
pub const MACH_SERVICES: &[&str] = &[
    "com.apple.Virtualization.VirtualMachine",
    "com.apple.Virtualization.LinuxRosetta",
    "com.apple.xpc.launchd.domain.system",
    "com.apple.logd",
    "com.apple.diagnosticd",
    "com.apple.system.notification_center",
    "com.apple.system.opendirectoryd.libinfo",
    "com.apple.trustd",
    "com.apple.trustd.agent",
    "com.apple.SecurityServer",
    "com.apple.CoreServices.coreservicesd",
    "com.apple.lsd.mapdb",
    "com.apple.cfprefsd.daemon",
    "com.apple.cfprefsd.agent",
    "com.apple.mobileasset.autoasset",
];

fn quote(p: &str) -> String {
    let mut s = String::from("\"");
    for c in p.chars() {
        if c == '"' || c == '\\' {
            s.push('\\');
        }
        s.push(c);
    }
    s.push('"');
    s
}

/// Resolves symlinked prefixes (`/var`, `/tmp`) the kernel reports as
/// `/private/...`; an absent file is resolved through its parent.
pub fn real(p: &Path) -> PathBuf {
    if let Ok(r) = std::fs::canonicalize(p) {
        return r;
    }
    match (p.parent(), p.file_name()) {
        (Some(parent), Some(name)) => real(parent).join(name),
        _ => p.to_path_buf(),
    }
}

pub struct Grants {
    /// A macOS guest: VZ hands its VM service sandbox extensions for the
    /// paravirtualized devices, and needs IOSurface for the guest display.
    pub macos_guest: bool,
    pub graphics: bool,
    pub read_subpaths: Vec<PathBuf>,
    pub read_literals: Vec<PathBuf>,
    pub write_subpaths: Vec<PathBuf>,
    pub write_literals: Vec<PathBuf>,
}

/// The profile text. Machine-readable inventory of the same grants is
/// `inventory`.
pub fn profile(g: &Grants) -> String {
    let mut p = String::from("(version 1)\n(deny default)\n");
    // The dynamic loader, system frameworks and their data.
    p.push_str(
        "(allow file-read* file-map-executable\n  (subpath \"/System\") (subpath \"/usr/lib\") (subpath \"/usr/share\")\n  (subpath \"/Library/Apple\") (subpath \"/private/var/db/dyld\") (subpath \"/System/Volumes/Preboot/Cryptexes\")\n  (subpath \"/private/var/db/timezone\") (literal \"/private/etc/localtime\")\n  (literal \"/dev/null\") (literal \"/dev/urandom\") (literal \"/dev/random\") (literal \"/dev/dtracehelper\"))\n",
    );
    p.push_str("(allow file-read-metadata)\n");
    p.push_str("(allow file-write-data (literal \"/dev/null\") (literal \"/dev/dtracehelper\"))\n");
    p.push_str("(allow file-ioctl (literal \"/dev/dtracehelper\"))\n");
    // Own executable image.
    if let Ok(exe) = std::env::current_exe() {
        p.push_str(&format!(
            "(allow file-read* file-map-executable (literal {}))\n",
            quote(&real(&exe).to_string_lossy())
        ));
    }
    for r in &g.read_subpaths {
        p.push_str(&format!(
            "(allow file-read* file-issue-extension (subpath {}))\n",
            quote(&real(r).to_string_lossy())
        ));
    }
    for r in &g.read_literals {
        p.push_str(&format!(
            "(allow file-read* file-issue-extension (literal {}))\n",
            quote(&real(r).to_string_lossy())
        ));
    }
    for w in &g.write_subpaths {
        let w = quote(&real(w).to_string_lossy());
        p.push_str(&format!(
            "(allow file-read* file-write* file-issue-extension (subpath {w}))\n"
        ));
        // Only Unix sockets inside the private directory.
        p.push_str(&format!(
            "(allow network-bind network-inbound network-outbound (local unix-socket (subpath {w})))\n"
        ));
    }
    for w in &g.write_literals {
        p.push_str(&format!(
            "(allow file-read* file-write* file-issue-extension (literal {}))\n",
            quote(&real(w).to_string_lossy())
        ));
    }
    p.push_str("(allow mach-lookup\n");
    for m in MACH_SERVICES {
        p.push_str(&format!("  (global-name {})\n", quote(m)));
    }
    p.push_str(")\n");
    p.push_str("(allow mach-register (local-name-prefix \"\"))\n");
    p.push_str("(allow ipc-posix-shm-read-data ipc-posix-shm-read-metadata (ipc-posix-name \"apple.shm.notification_center\"))\n");
    p.push_str("(allow process-info* (target self))\n");
    p.push_str("(allow process-info-pidinfo process-info-setcontrol)\n");
    p.push_str("(allow signal (target self))\n");
    p.push_str("(allow sysctl-read)\n");
    p.push_str("(allow iokit-open (iokit-user-client-class \"RootDomainUserClient\"))\n");
    p.push_str("(allow system-fsctl)\n");
    if g.macos_guest || g.graphics {
        p.push_str("(allow generic-issue-extension\n");
        for e in MACOS_GUEST_EXTENSIONS
            .iter()
            .filter(|e| g.macos_guest || e.ends_with(".io-surface"))
        {
            p.push_str(&format!("  (extension-class {})\n", quote(e)));
        }
        p.push_str(")\n");
    }
    if g.graphics {
        p.push_str("(allow iokit-open (iokit-user-client-class \"IOSurfaceRootUserClient\"))\n");
    }
    // No process-fork / process-exec: the worker spawns nothing.
    p
}

/// Machine-readable inventory of everything the profile grants.
pub fn inventory(g: &Grants) -> serde_json::Value {
    let s = |v: &Vec<PathBuf>| -> Vec<String> {
        v.iter()
            .map(|p| real(p).to_string_lossy().into_owned())
            .collect()
    };
    serde_json::json!({
        "format": "lamp.confinement-inventory/v0",
        "profile": "seatbelt deny-default",
        "files": {
            "system_read": ["/System", "/usr/lib", "/usr/share", "/Library/Apple", "/private/var/db/dyld",
                            "/System/Volumes/Preboot/Cryptexes", "/private/var/db/timezone", "/private/etc/localtime",
                            "/dev/null", "/dev/urandom", "/dev/random"],
            "metadata_read": "all paths (stat only, no content)",
            "read_subpaths": s(&g.read_subpaths),
            "read_files": s(&g.read_literals),
            "read_write_subpaths": s(&g.write_subpaths),
            "read_write_files": s(&g.write_literals),
            "macos_guest_extra": "the worker account's own confstr(_CS_DARWIN_USER_CACHE_DIR), read-write, macOS guests only",
            "issue_extension": "file extensions only for the paths above",
        },
        "network": {"inet": "denied", "unix_sockets": "bind/connect only inside read_write_subpaths"},
        "mach_services": MACH_SERVICES,
        "macos_guest": g.macos_guest,
        "generic_extensions": MACOS_GUEST_EXTENSIONS.iter().filter(|e| g.macos_guest || g.graphics && e.ends_with(".io-surface")).collect::<Vec<_>>(),
        "iokit_user_clients": if g.graphics { &["RootDomainUserClient", "IOSurfaceRootUserClient"][..] } else { &["RootDomainUserClient"][..] },
        "process": {"fork": "denied", "exec": "denied", "signal": "self only"},
        "entitlements": ["com.apple.security.virtualization"],
        "handles": ["assigned disk images", "state files", "private API socket", "private vsock socket"],
    })
}

#[cfg(target_os = "macos")]
unsafe extern "C" {
    fn sandbox_init_with_parameters(
        profile: *const libc::c_char,
        flags: u64,
        parameters: *const *const libc::c_char,
        errorbuf: *mut *mut libc::c_char,
    ) -> libc::c_int;
    fn sandbox_free_error(errorbuf: *mut libc::c_char);
}

/// Applies the profile to this process, irreversibly.
#[cfg(target_os = "macos")]
pub fn apply(profile: &str) -> Result<(), String> {
    let c = std::ffi::CString::new(profile).map_err(|e| e.to_string())?;
    let params: [*const libc::c_char; 1] = [std::ptr::null()];
    let mut err: *mut libc::c_char = std::ptr::null_mut();
    // SAFETY: valid NUL-terminated profile, NULL-terminated parameter list
    // and a valid error out-pointer.
    let rc = unsafe { sandbox_init_with_parameters(c.as_ptr(), 0, params.as_ptr(), &mut err) };
    if rc != 0 {
        let msg = if err.is_null() {
            "sandbox_init failed".to_string()
        } else {
            // SAFETY: the library returned a NUL-terminated message.
            let m = unsafe { std::ffi::CStr::from_ptr(err) }
                .to_string_lossy()
                .into_owned();
            unsafe { sandbox_free_error(err) };
            m
        };
        return Err(msg);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn profile_denies_by_default_and_quotes_paths() {
        let g = Grants {
            macos_guest: false,
            graphics: false,
            read_subpaths: vec![],
            read_literals: vec![PathBuf::from("/nonexistent/a\"b")],
            write_subpaths: vec![PathBuf::from("/nonexistent/dir")],
            write_literals: vec![],
        };
        let p = profile(&g);
        assert!(p.starts_with("(version 1)\n(deny default)\n"));
        assert!(p.contains("(literal \"/nonexistent/a\\\"b\")"));
        assert!(!p.contains("process-exec"));
        assert!(!p.contains("network-outbound (remote"));
    }

    #[test]
    fn macos_guest_extensions_are_exact_and_only_for_macos() {
        let mut g = Grants {
            macos_guest: false,
            graphics: false,
            read_subpaths: vec![],
            read_literals: vec![],
            write_subpaths: vec![PathBuf::from("/nonexistent/dir")],
            write_literals: vec![],
        };
        let linux = profile(&g);
        assert!(!linux.contains("generic-issue-extension"));
        assert!(!linux.contains("IOSurfaceRootUserClient"));
        g.macos_guest = true;
        let mac = profile(&g);
        for e in MACOS_GUEST_EXTENSIONS {
            assert!(mac.contains(&format!("(extension-class \"{e}\")")));
        }
        // No unrestricted extension issue anywhere.
        assert!(!mac.contains("(allow generic-issue-extension)"));
        assert!(!mac.contains("(allow file-issue-extension)"));
        assert!(mac.contains("file-issue-extension (subpath \"/nonexistent/dir\")"));
    }
}
