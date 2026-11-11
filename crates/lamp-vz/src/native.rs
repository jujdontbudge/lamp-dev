use serde::{Deserialize, Serialize};
use std::io::{Read, Write};
use std::os::unix::fs::{DirBuilderExt, MetadataExt, OpenOptionsExt};
use std::os::unix::process::CommandExt;
use std::process::{Command, Stdio};
use std::sync::mpsc;
use std::time::Duration;

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Origin {
    OwnerTask,
    PrivateRepositoryCi,
    PublicRepositoryCi,
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Task {
    pub origin: Origin,
    pub argv: Vec<String>,
    pub memory_mib: u64,
    #[serde(default)]
    pub login: bool,
    #[serde(default)]
    pub protected_connection: bool,
    pub timeout_seconds: u64,
}

impl Task {
    pub fn validate(&self) -> Result<(), String> {
        if matches!(self.origin, Origin::PublicRepositoryCi) {
            return Err("BOUNDARY_DENIED: public-repository CI is never admitted to the weaker process fallback".into());
        }
        if self.login || self.protected_connection {
            return Err("BOUNDARY_DENIED: logins and protected connections are refused in the weaker process fallback".into());
        }
        if self.argv.is_empty()
            || !self.argv[0].starts_with('/')
            || self.argv.iter().any(|a| a.contains('\0'))
            || self.memory_mib == 0
            || !(1..=300).contains(&self.timeout_seconds)
        {
            return Err("INVALID_ARGUMENT: absolute executable, nonzero memory_mib and timeout_seconds 1..=300 required".into());
        }
        Ok(())
    }
}

#[link(name = "Security", kind = "framework")]
unsafe extern "C" {
    fn SecTaskCreateFromSelf(allocator: *const libc::c_void) -> *const libc::c_void;
    fn SecTaskCopyValueForEntitlement(
        task: *const libc::c_void,
        entitlement: *const libc::c_void,
        error: *mut *const libc::c_void,
    ) -> *const libc::c_void;
}

#[link(name = "CoreFoundation", kind = "framework")]
unsafe extern "C" {
    fn CFStringCreateWithCString(
        allocator: *const libc::c_void,
        text: *const libc::c_char,
        encoding: u32,
    ) -> *const libc::c_void;
    fn CFBooleanGetValue(value: *const libc::c_void) -> bool;
    fn CFGetTypeID(value: *const libc::c_void) -> usize;
    fn CFBooleanGetTypeID() -> usize;
    fn CFRelease(value: *const libc::c_void);
}

unsafe extern "C" {
    fn sandbox_check(pid: libc::pid_t, operation: *const libc::c_char, flags: i32, ...) -> i32;
}

pub fn entitlement(name: &str) -> bool {
    let name = std::ffi::CString::new(name).unwrap();
    unsafe {
        let task = SecTaskCreateFromSelf(std::ptr::null());
        if task.is_null() {
            return false;
        }
        let key = CFStringCreateWithCString(std::ptr::null(), name.as_ptr(), 0x08000100);
        let value = SecTaskCopyValueForEntitlement(task, key, std::ptr::null_mut());
        let enabled = !value.is_null()
            && CFGetTypeID(value) == CFBooleanGetTypeID()
            && CFBooleanGetValue(value);
        if !value.is_null() {
            CFRelease(value);
        }
        CFRelease(key);
        CFRelease(task);
        enabled
    }
}

pub fn read_task() -> Result<Task, String> {
    let mut bytes = Vec::new();
    std::io::stdin()
        .take(65537)
        .read_to_end(&mut bytes)
        .map_err(|e| e.to_string())?;
    if bytes.len() > 65536 {
        return Err("INVALID_ARGUMENT: native request exceeds 64 KiB".into());
    }
    let task: Task = serde_json::from_slice(&bytes).map_err(|e| e.to_string())?;
    task.validate()?;
    Ok(task)
}

pub fn launch(helper: &std::path::Path, task: &Task) -> Result<i32, String> {
    task.validate()?;
    if entitlement("com.apple.security.virtualization") && crate::vz::supported() {
        return Err(
            "BOUNDARY_DENIED: virtualization is available; process fallback is not selected".into(),
        );
    }
    let helper = std::fs::canonicalize(helper).map_err(|e| e.to_string())?;
    let info = helper
        .parent()
        .and_then(|p| p.parent())
        .ok_or("BOUNDARY_DENIED: helper must be packaged in an app bundle")?
        .join("Info.plist");
    let id = Command::new("/usr/bin/plutil")
        .args(["-extract", "CFBundleIdentifier", "raw", "-o", "-"])
        .arg(info)
        .output()
        .map_err(|e| e.to_string())?;
    let id_text = String::from_utf8_lossy(&id.stdout);
    let id_text = id_text.trim();
    if !id.status.success()
        || !id_text.starts_with("si.eilmer.lamp.native-task.")
        || id_text.len() > 200
        || !id_text
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'.' || c == b'-')
    {
        return Err("BOUNDARY_DENIED: invalid native task bundle identifier".into());
    }
    let used = std::path::PathBuf::from(format!("/private/var/tmp/lamp-native-used-{}", unsafe {
        libc::getuid()
    }));
    match std::fs::DirBuilder::new().mode(0o700).create(&used) {
        Ok(()) => {}
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => {}
        Err(e) => return Err(format!("native task registry: {e}")),
    }
    let meta = std::fs::symlink_metadata(&used).map_err(|e| e.to_string())?;
    if !meta.is_dir() || meta.uid() != unsafe { libc::getuid() } || meta.mode() & 0o077 != 0 {
        return Err("BOUNDARY_DENIED: native task registry is not private to the launcher".into());
    }
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .custom_flags(libc::O_NOFOLLOW)
        .open(used.join(id_text))
        .map_err(|e| format!("BOUNDARY_DENIED: native task bundles are single-use: {e}"))?;
    println!(
        "{}",
        serde_json::json!({
            "boundary": "app_sandbox_process",
            "isolation": "weaker_than_vm",
            "fallback_reason": if entitlement("com.apple.security.virtualization") {
                "virtualization_framework_unavailable"
            } else {
                "virtualization_entitlement_absent"
            },
        })
    );
    std::io::stdout().flush().map_err(|e| e.to_string())?;
    let mut child = Command::new(&helper)
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .stdin(Stdio::piped())
        .spawn()
        .map_err(|e| format!("launch App Sandbox helper: {e}"))?;
    serde_json::to_writer(child.stdin.take().unwrap(), task).map_err(|e| e.to_string())?;
    child
        .wait()
        .map(|s| s.code().unwrap_or(1))
        .map_err(|e| e.to_string())
}

fn output(mut stream: impl Read) -> (Vec<u8>, bool) {
    let mut bytes = Vec::new();
    let mut buffer = [0u8; 8192];
    let mut truncated = false;
    while let Ok(n) = stream.read(&mut buffer) {
        if n == 0 {
            break;
        }
        let keep = n.min((1usize << 20).saturating_sub(bytes.len()));
        bytes.extend_from_slice(&buffer[..keep]);
        truncated |= keep < n;
    }
    (bytes, truncated)
}

pub fn run(task: Task) -> Result<i32, String> {
    task.validate()?;
    if !entitlement("com.apple.security.app-sandbox")
        || unsafe { sandbox_check(libc::getpid(), std::ptr::null(), 0) } != 1
        || entitlement("com.apple.security.network.client")
        || entitlement("com.apple.security.network.server")
    {
        return Err(
            "BOUNDARY_DENIED: helper must run in App Sandbox without network entitlements".into(),
        );
    }
    let memory = crate::admission::Memory::detect()?;
    memory.admit(task.memory_mib, 0, 0)?;
    let container = std::path::PathBuf::from(objc2_foundation::NSHomeDirectory().to_string());
    if !container
        .to_string_lossy()
        .contains("/Library/Containers/si.eilmer.lamp.native-task.")
    {
        return Err(
            "BOUNDARY_DENIED: a uniquely packaged native task container is required".into(),
        );
    }
    std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(container.join(".lamp-task-used"))
        .map_err(|e| format!("BOUNDARY_DENIED: native task bundles are single-use: {e}"))?;
    let root = container.join("lamp-task");
    std::fs::create_dir(&root).map_err(|e| format!("create private task directory: {e}"))?;
    let root = crate::confine::real(&root);
    let result: Result<i32, String> = (|| {
        println!(
            "{}",
            serde_json::json!({
                "boundary": "app_sandbox_process",
                "isolation": "weaker_than_vm",
                "logins": "denied",
                "protected_connections": "denied",
                "public_repository_ci": "denied",
                "network": "denied",
                "memory": memory,
            })
        );
        std::io::stdout().flush().map_err(|e| e.to_string())?;
        let mut child = Command::new(&task.argv[0])
            .args(&task.argv[1..])
            .env_clear()
            .env("PATH", "/usr/bin:/bin")
            .env("HOME", &root)
            .env("TMPDIR", &root)
            .current_dir(&root)
            .process_group(0)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|e| format!("spawn native task: {e}"))?;
        let stdout = child.stdout.take().unwrap();
        let stderr = child.stderr.take().unwrap();
        let out = std::thread::spawn(move || output(stdout));
        let err = std::thread::spawn(move || output(stderr));
        let pid = child.id() as i32;
        let (cancel, rx) = mpsc::channel();
        let timeout = std::thread::spawn(move || {
            if rx
                .recv_timeout(Duration::from_secs(task.timeout_seconds))
                .is_err()
            {
                unsafe { libc::kill(-pid, libc::SIGKILL) };
                true
            } else {
                false
            }
        });
        let status = child.wait().map_err(|e| e.to_string())?;
        unsafe { libc::kill(-pid, libc::SIGKILL) };
        let _ = cancel.send(());
        let timed_out = timeout.join().unwrap_or(true);
        let (out, out_truncated) = out.join().map_err(|_| "stdout reader failed")?;
        let (err, err_truncated) = err.join().map_err(|_| "stderr reader failed")?;
        println!(
            "{}",
            serde_json::json!({
                "exit_code": status.code(),
                "timed_out": timed_out,
                "stdout": String::from_utf8_lossy(&out),
                "stderr": String::from_utf8_lossy(&err),
                "truncated": out_truncated || err_truncated,
                "boundary": "app_sandbox_process",
                "isolation": "weaker_than_vm",
            })
        );
        Ok(status.code().unwrap_or(1))
    })();
    let cleanup = std::fs::remove_dir_all(root).map_err(|e| format!("task directory cleanup: {e}"));
    let code = result?;
    cleanup?;
    Ok(code)
}
