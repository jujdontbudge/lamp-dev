//! Engine selection (HST-001/006, SBX-006): which engine on this host may
//! run a request, preferring the fastest one that meets every requirement.
//!
//! Candidates in speed order: Firecracker (KVM VM, Linux), Virtualization
//! framework (VM, macOS), then the clearly weaker process fallbacks: gVisor
//! on Linux, the App Sandbox on macOS. A weaker fallback is chosen only when
//! no VM engine can serve the request, and never for secrets, devices,
//! captures or public-repository CI.
use serde::Serialize;
use std::path::{Path, PathBuf};

/// Oldest Linux kernel Lamp supports.
pub const MIN_LINUX: (u32, u32) = (5, 10);

#[derive(Debug, Clone, Serialize)]
pub struct Host {
    pub os: String,
    pub arch: String,
    pub kernel: String,
    pub kernel_supported: bool,
    /// `/dev/kvm` opens read-write and answers API version 12.
    pub kvm: bool,
    pub kvm_detail: String,
    pub firecracker: bool,
    pub vz: bool,
    pub gvisor: Option<PathBuf>,
    pub app_sandbox: bool,
}

#[derive(Debug, Clone, Default, Serialize)]
pub struct Needs {
    pub guest_os: String,
    pub devices: Vec<String>,
    /// Memory fork, suspend/resume or durable capture.
    pub capture: bool,
    /// Logins, credentials or protected connections.
    pub secrets: bool,
    pub public_ci: bool,
    pub engine: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Considered {
    pub engine: &'static str,
    pub boundary: &'static str,
    pub isolation: &'static str,
    pub chosen: bool,
    pub refused: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct Choice {
    pub engine: Option<&'static str>,
    pub boundary: Option<&'static str>,
    pub isolation: Option<&'static str>,
    /// Error code when nothing qualifies.
    pub code: Option<&'static str>,
    pub considered: Vec<Considered>,
}

struct Candidate {
    engine: &'static str,
    boundary: &'static str,
    isolation: &'static str,
    os: &'static str,
    guests: &'static [&'static str],
    devices: &'static [&'static str],
    capture: bool,
    weaker: bool,
}

const CANDIDATES: &[Candidate] = &[
    Candidate {
        engine: "firecracker",
        boundary: "vm",
        isolation: "vm",
        os: "linux",
        guests: &["linux"],
        devices: &["disk"],
        capture: true,
        weaker: false,
    },
    Candidate {
        engine: "vz",
        boundary: "vm",
        isolation: "vm",
        os: "macos",
        guests: &["linux", "macos"],
        devices: &[
            "display",
            "keyboard",
            "mouse",
            "audio_input",
            "audio_output",
            "gpu",
            "disk",
        ],
        capture: true,
        weaker: false,
    },
    Candidate {
        engine: "gvisor",
        boundary: "gvisor_sandbox",
        isolation: "weaker_than_vm",
        os: "linux",
        guests: &["linux"],
        devices: &[],
        capture: false,
        weaker: true,
    },
    Candidate {
        engine: "app_sandbox",
        boundary: "app_sandbox_process",
        isolation: "weaker_than_vm",
        os: "macos",
        guests: &["macos"],
        devices: &[],
        capture: false,
        weaker: true,
    },
];

fn available(c: &Candidate, h: &Host) -> Result<(), String> {
    if h.os != c.os {
        return Err(format!("runs on {}, not {}", c.os, h.os));
    }
    let ok = match c.engine {
        "firecracker" if !h.kernel_supported => {
            return Err(format!("kernel {} is older than 5.10", h.kernel));
        }
        "firecracker" if !h.kvm => return Err(format!("no usable KVM: {}", h.kvm_detail)),
        "firecracker" => h.firecracker,
        "vz" => h.vz,
        "gvisor" => h.gvisor.is_some(),
        "app_sandbox" => h.app_sandbox,
        _ => false,
    };
    if ok {
        Ok(())
    } else {
        Err(format!("{} is not installed", c.engine))
    }
}

pub fn choose(h: &Host, n: &Needs) -> Choice {
    let guest = if n.guest_os.is_empty() {
        "linux"
    } else {
        &n.guest_os
    };
    let mut considered = Vec::new();
    let mut chosen = None;
    for c in CANDIDATES {
        let refused = (|| {
            if n.engine.as_deref().is_some_and(|e| e != c.engine) {
                return Err("caller pinned another engine".to_string());
            }
            available(c, h)?;
            if !c.guests.contains(&guest) {
                return Err(format!("cannot run {guest} guests"));
            }
            if let Some(d) = n.devices.iter().find(|d| !c.devices.contains(&d.as_str())) {
                return Err(format!("cannot attach device {d}"));
            }
            if n.capture && !c.capture {
                return Err("cannot fork, suspend or save memory".into());
            }
            if c.weaker && n.public_ci {
                return Err("public-repository CI requires a VM".into());
            }
            if c.weaker && n.secrets {
                return Err("secrets require a VM; the weaker fallback refuses them".into());
            }
            Ok(())
        })()
        .err();
        let take = refused.is_none() && chosen.is_none();
        if take {
            chosen = Some(c);
        }
        considered.push(Considered {
            engine: c.engine,
            boundary: c.boundary,
            isolation: c.isolation,
            chosen: take,
            refused,
        });
    }
    let code = chosen.is_none().then_some(if n.public_ci || n.secrets {
        "VM_REQUIRED"
    } else if !n.devices.is_empty() {
        "DEVICE_UNAVAILABLE"
    } else {
        "ENGINE_UNQUALIFIED"
    });
    Choice {
        engine: chosen.map(|c| c.engine),
        boundary: chosen.map(|c| c.boundary),
        isolation: chosen.map(|c| c.isolation),
        code,
        considered,
    }
}

fn kernel_version(release: &str) -> (u32, u32) {
    let mut it = release
        .split(|c: char| !c.is_ascii_digit())
        .filter(|s| !s.is_empty())
        .map(|s| s.parse().unwrap_or(0));
    (it.next().unwrap_or(0), it.next().unwrap_or(0))
}

#[cfg(target_os = "linux")]
fn probe_kvm() -> (bool, String) {
    use std::os::fd::AsRawFd;
    const KVM_GET_API_VERSION: libc::c_ulong = 0xAE00;
    match std::fs::OpenOptions::new()
        .read(true)
        .write(true)
        .open("/dev/kvm")
    {
        Ok(f) => {
            // SAFETY: argument-less ioctl on an open KVM fd.
            let v = unsafe { libc::ioctl(f.as_raw_fd(), KVM_GET_API_VERSION as _, 0) };
            (v == 12, format!("KVM API version {v}"))
        }
        Err(e) => (false, format!("/dev/kvm: {e}")),
    }
}

#[cfg(not(target_os = "linux"))]
fn probe_kvm() -> (bool, String) {
    (false, "not Linux".into())
}

/// Reads this host's engines. `firecracker` and `runsc` are the installed
/// binaries Lamp would use; a missing file means that engine is absent.
pub fn detect(firecracker: &Path, runsc: &Path) -> Host {
    let os = match std::env::consts::OS {
        "macos" => "macos",
        other => other,
    }
    .to_string();
    let kernel = std::fs::read_to_string("/proc/sys/kernel/osrelease")
        .map(|s| s.trim().to_string())
        .unwrap_or_default();
    let (kvm, kvm_detail) = probe_kvm();
    Host {
        kernel_supported: os != "linux" || kernel_version(&kernel) >= MIN_LINUX,
        arch: std::env::consts::ARCH.into(),
        kernel,
        kvm,
        kvm_detail,
        firecracker: os == "linux" && firecracker.is_file(),
        vz: os == "macos",
        gvisor: (os == "linux" && runsc.is_file()).then(|| runsc.to_path_buf()),
        app_sandbox: os == "macos",
        os,
    }
}
