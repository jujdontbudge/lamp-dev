//! The weaker Linux fallback (SBX-006): one bounded task in a gVisor
//! (`runsc`) sandbox, for hosts without usable virtualization.
//!
//! gVisor intercepts the task's system calls in a user-space kernel; it is a
//! real boundary but **weaker than a VM**, and every result says so. It
//! never receives logins, credentials or protected connections, has no
//! network, no devices and no memory capture, and never runs
//! public-repository CI (the control plane refuses those before here).
//! It is standalone: no container daemon is configured or contacted.
use crate::error::{EngineError, IoContext, Result};
use serde::{Deserialize, Serialize};
use std::io::Read;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

pub const BOUNDARY: &str = "gvisor_sandbox";
pub const ISOLATION: &str = "weaker_than_vm";
const OUTPUT_LIMIT: u64 = 1 << 20;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Task {
    pub origin: String,
    pub argv: Vec<String>,
    pub memory_mib: u64,
    pub timeout_seconds: u64,
    #[serde(default)]
    pub pids_max: Option<u64>,
    #[serde(default)]
    pub login: bool,
    #[serde(default)]
    pub protected_connection: bool,
}

#[derive(Debug, Serialize)]
pub struct Outcome {
    pub engine: String,
    pub boundary: &'static str,
    pub isolation: &'static str,
    pub origin: String,
    pub network: &'static str,
    pub memory_mib: u64,
    pub pids_max: u64,
    pub exit_code: i32,
    pub timed_out: bool,
    pub stdout: String,
    pub stderr: String,
}

pub struct Runtime {
    pub runsc: PathBuf,
    /// Read-only root filesystem directory for tasks.
    pub rootfs: PathBuf,
    /// Private state directory (runsc --root and bundles), root-only.
    pub state: PathBuf,
}

fn oci_spec(t: &Task, rootfs: &Path, pids: u64) -> serde_json::Value {
    serde_json::json!({
        "ociVersion": "1.0.2",
        "process": {
            "terminal": false,
            "user": {"uid": 65534, "gid": 65534},
            "args": t.argv,
            "env": ["PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/tmp", "LAMP_BOUNDARY=gvisor_sandbox"],
            "cwd": "/tmp",
            "capabilities": {"bounding": [], "effective": [], "inheritable": [], "permitted": [], "ambient": []},
            "rlimits": [{"type": "RLIMIT_NOFILE", "hard": 1024, "soft": 1024}],
            "noNewPrivileges": true
        },
        "root": {"path": rootfs, "readonly": true},
        "hostname": "lamp-gvisor",
        "mounts": [
            {"destination": "/proc", "type": "proc", "source": "proc"},
            {"destination": "/dev", "type": "tmpfs", "source": "tmpfs", "options": ["nosuid", "strictatime", "mode=755", "size=65536k"]},
            {"destination": "/tmp", "type": "tmpfs", "source": "tmpfs", "options": ["nosuid", "nodev", "mode=1777", "size=262144k"]}
        ],
        "linux": {
            "namespaces": [{"type": "pid"}, {"type": "network"}, {"type": "ipc"}, {"type": "uts"}, {"type": "mount"}],
            "resources": {
                "memory": {"limit": t.memory_mib << 20},
                "pids": {"limit": pids}
            }
        }
    })
}

fn bounded(mut r: impl Read + Send + 'static) -> std::thread::JoinHandle<String> {
    std::thread::spawn(move || {
        let mut buf = Vec::new();
        let _ = (&mut r).take(OUTPUT_LIMIT).read_to_end(&mut buf);
        let _ = std::io::copy(&mut r, &mut std::io::sink());
        String::from_utf8_lossy(&buf).into_owned()
    })
}

impl Runtime {
    pub fn version(&self) -> String {
        Command::new(&self.runsc)
            .arg("--version")
            .output()
            .ok()
            .and_then(|o| {
                String::from_utf8_lossy(&o.stdout)
                    .lines()
                    .next()
                    .map(|l| l.trim().replace("runsc version ", "gvisor-"))
            })
            .unwrap_or_else(|| "gvisor".into())
    }

    pub fn run(&self, t: &Task) -> Result<Outcome> {
        if t.login || t.protected_connection {
            return Err(EngineError::new(
                "VM_REQUIRED",
                "logins and protected connections require a VM; the weaker fallback refuses them",
            ));
        }
        if t.argv.is_empty()
            || !(64..=16384).contains(&t.memory_mib)
            || !(1..=300).contains(&t.timeout_seconds)
        {
            return Err(EngineError::invalid(
                "argv, memory_mib 64..=16384 and timeout_seconds 1..=300 required",
            ));
        }
        let pids = t.pids_max.unwrap_or(256);
        if !(8..=4096).contains(&pids) {
            return Err(EngineError::invalid("pids_max must be 8..=4096"));
        }
        std::fs::create_dir_all(&self.state).ctx("gvisor state dir")?;
        crate::fsutil::chmod(&self.state, 0o700)?;
        let id = format!(
            "lamp-{}",
            lamp_init::hex_encode(&crate::fsutil::random_bytes::<8>())
        );
        let bundle = self.state.join(&id);
        std::fs::create_dir(&bundle).ctx("bundle dir")?;
        let cleanup = |runsc: &Path, root: &Path| {
            let _ = Command::new(runsc)
                .arg("--root")
                .arg(root)
                .args(["delete", "--force", &id])
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status();
            let _ = std::fs::remove_dir_all(&bundle);
        };
        std::fs::write(
            bundle.join("config.json"),
            serde_json::to_vec(&oci_spec(t, &self.rootfs, pids)).unwrap(),
        )
        .ctx("write OCI spec")?;
        let root = self.state.join("runsc-root");
        let mut child = Command::new(&self.runsc)
            .arg("--root")
            .arg(&root)
            .args([
                "--network=none",
                "--host-uds=none",
                "--overlay2=root:memory",
                "run",
                "--bundle",
            ])
            .arg(&bundle)
            .arg(&id)
            .env_clear()
            .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .map_err(|e| {
                let _ = std::fs::remove_dir_all(&bundle);
                EngineError::host(format!("start runsc: {e}"))
            })?;
        let out = bounded(child.stdout.take().unwrap());
        let err = bounded(child.stderr.take().unwrap());
        let deadline = Instant::now() + Duration::from_secs(t.timeout_seconds);
        let mut timed_out = false;
        let status = loop {
            if let Some(s) = child.try_wait().ctx("wait runsc")? {
                break s;
            }
            if Instant::now() > deadline {
                timed_out = true;
                let _ = Command::new(&self.runsc)
                    .arg("--root")
                    .arg(&root)
                    .args(["kill", "--all", &id, "KILL"])
                    .status();
                let _ = child.kill();
                break child.wait().ctx("wait runsc")?;
            }
            std::thread::sleep(Duration::from_millis(20));
        };
        let (stdout, stderr) = (
            out.join().unwrap_or_default(),
            err.join().unwrap_or_default(),
        );
        cleanup(&self.runsc, &root);
        use std::os::unix::process::ExitStatusExt;
        Ok(Outcome {
            engine: self.version(),
            boundary: BOUNDARY,
            isolation: ISOLATION,
            origin: t.origin.clone(),
            network: "none",
            memory_mib: t.memory_mib,
            pids_max: pids,
            exit_code: status.code().unwrap_or(128 + status.signal().unwrap_or(0)),
            timed_out,
            stdout,
            stderr,
        })
    }
}
