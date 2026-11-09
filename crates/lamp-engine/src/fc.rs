//! Firecracker 1.17 adapter: jailed VMM processes and their HTTP API.
//!
//! Every VMM runs under the matching jailer: a unique UID/GID, chroot, a new
//! PID namespace, a private network namespace, cgroup v2 limits and
//! Firecracker's built-in seccomp filters. Paths inside a jail are fixed
//! (`/rootfs.ext4`, `/v.sock`), so saved state restores into any jail.
use crate::error::{EngineError, IoContext, Result};
use crate::fsutil;
use std::ffi::CString;
use std::io::{BufRead, BufReader, Read, Write};
use std::os::unix::ffi::OsStrExt;
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

pub const ROOTFS: &str = "rootfs.ext4";
pub const VSOCK: &str = "v.sock";
pub const API: &str = "api.sock";
pub const MEM: &str = "mem";
pub const VMSTATE: &str = "vmstate";
pub const KERNEL: &str = "vmlinux";
pub const CGROUP_PARENT: &str = "lamp";

#[derive(Debug, Clone)]
pub struct Binaries {
    pub firecracker: PathBuf,
    pub jailer: PathBuf,
}

#[derive(Debug, Clone, Copy)]
pub struct Limits {
    pub vcpus: u32,
    pub memory_mib: u64,
    pub pids_max: u64,
}

/// One jail directory, cgroup and network namespace.
#[derive(Debug)]
pub struct Jail {
    pub id: String,
    pub dir: PathBuf,
    pub root: PathBuf,
    pub uid: u32,
    pub netns: PathBuf,
    pub pid: Option<i32>,
}

pub fn cgroup_dir(id: &str) -> PathBuf {
    Path::new("/sys/fs/cgroup").join(CGROUP_PARENT).join(id)
}

/// Makes sure the parent cgroup exists with the controllers jails need.
pub fn init_cgroups() -> Result<()> {
    let parent = Path::new("/sys/fs/cgroup").join(CGROUP_PARENT);
    std::fs::create_dir_all(&parent).ctx("create lamp cgroup")?;
    std::fs::write(parent.join("cgroup.subtree_control"), "+cpu +memory +pids +io")
        .ctx("enable lamp cgroup controllers")
}

/// Creates a persistent network namespace bound at `path`.
fn create_netns(path: &Path) -> Result<()> {
    std::fs::File::create(path).ctx(format!("create {}", path.display()))?;
    let target = CString::new(path.as_os_str().as_bytes()).unwrap();
    std::thread::scope(|s| {
        s.spawn(|| -> Result<()> {
            // SAFETY: unshare affects only this short-lived thread; the bind
            // mount pins the new namespace to `path`.
            unsafe {
                if libc::unshare(libc::CLONE_NEWNET) != 0 {
                    return Err(EngineError::host(format!("unshare netns: {}", std::io::Error::last_os_error())));
                }
                let src = c"/proc/thread-self/ns/net";
                if libc::mount(src.as_ptr(), target.as_ptr(), std::ptr::null(), libc::MS_BIND, std::ptr::null()) != 0 {
                    return Err(EngineError::host(format!("bind netns: {}", std::io::Error::last_os_error())));
                }
            }
            Ok(())
        })
        .join()
        .unwrap()
    })
}

fn remove_netns(path: &Path) {
    if let Ok(c) = CString::new(path.as_os_str().as_bytes()) {
        // SAFETY: valid path; failure is fine (not mounted).
        unsafe { libc::umount2(c.as_ptr(), libc::MNT_DETACH) };
    }
    let _ = std::fs::remove_file(path);
}

impl Jail {
    /// Creates the jail directory (owned by the jail UID) and namespace.
    pub fn create(base: &Path, netns_dir: &Path, id: &str, uid: u32) -> Result<Jail> {
        let dir = base.join("firecracker").join(id);
        let root = dir.join("root");
        if dir.exists() {
            return Err(EngineError::in_use(format!("jail {id} already exists")));
        }
        std::fs::create_dir_all(&root).ctx("create jail")?;
        fsutil::chown(&root, uid, uid)?;
        std::fs::create_dir_all(netns_dir).ctx("create netns dir")?;
        let netns = netns_dir.join(id);
        if let Err(e) = create_netns(&netns) {
            let _ = std::fs::remove_dir_all(&dir);
            remove_netns(&netns);
            return Err(e);
        }
        Ok(Jail {
            id: id.into(),
            dir,
            root,
            uid,
            netns,
            pid: None,
        })
    }

    pub fn path(&self, name: &str) -> PathBuf {
        self.root.join(name)
    }

    /// Starts the jailed Firecracker and waits for its API socket.
    pub fn spawn(&mut self, bins: &Binaries, limits: Limits) -> Result<()> {
        let log = std::fs::File::create(self.dir.join("vmm.log")).ctx("create vmm log")?;
        let memory_max = (limits.memory_mib + 1024) << 20;
        let cpu_max = format!("cpu.max={} 100000", (u64::from(limits.vcpus) + 1) * 100_000);
        let status = Command::new(&bins.jailer)
            .args(["--id", &self.id])
            .arg("--exec-file")
            .arg(&bins.firecracker)
            .args(["--uid", &self.uid.to_string(), "--gid", &self.uid.to_string()])
            .arg("--chroot-base-dir")
            .arg(self.dir.parent().and_then(Path::parent).unwrap())
            .args(["--cgroup-version", "2", "--parent-cgroup", CGROUP_PARENT])
            .args(["--cgroup", &format!("memory.max={memory_max}")])
            .args(["--cgroup", &format!("pids.max={}", limits.pids_max.clamp(64, 4096))])
            .args(["--cgroup", &cpu_max])
            .arg("--netns")
            .arg(&self.netns)
            .args(["--new-pid-ns", "--resource-limit", "no-file=4096"])
            .args(["--", "--api-sock", &format!("/{API}"), "--level", "Warning"])
            .stdin(Stdio::null())
            .stdout(log.try_clone()?)
            .stderr(log)
            .env_clear()
            .status()
            .ctx("run jailer")?;
        if !status.success() {
            let log = std::fs::read_to_string(self.dir.join("vmm.log")).unwrap_or_default();
            return Err(EngineError::host(format!("jailer failed ({status}): {}", tail(&log))));
        }
        let pid_file = self.root.join("firecracker.pid");
        let api = self.path(API);
        let start = Instant::now();
        while !(api.exists() && pid_file.exists()) {
            if start.elapsed() > Duration::from_secs(5) {
                return Err(EngineError::host("firecracker API socket did not appear"));
            }
            std::thread::sleep(Duration::from_micros(200));
        }
        let pid = std::fs::read_to_string(&pid_file)
            .ctx("read firecracker pid")?
            .trim()
            .parse::<i32>()
            .map_err(|_| EngineError::host("bad firecracker pid file"))?;
        self.pid = Some(pid);
        Ok(())
    }

    pub fn api(&self, method: &str, path: &str, body: &str) -> Result<String> {
        api_call(&self.path(API), method, path, body)
    }

    pub fn alive(&self) -> bool {
        // SAFETY: signal 0 only checks existence.
        self.pid.is_some_and(|pid| unsafe { libc::kill(pid, 0) } == 0)
    }

    /// Kills every process in the jail's cgroup and waits for it to empty.
    pub fn kill(&mut self) {
        let cg = cgroup_dir(&self.id);
        if cg.exists() {
            let _ = std::fs::write(cg.join("cgroup.kill"), "1");
            let start = Instant::now();
            while start.elapsed() < Duration::from_secs(5) {
                match std::fs::read_to_string(cg.join("cgroup.procs")) {
                    Ok(p) if !p.trim().is_empty() => std::thread::sleep(Duration::from_millis(1)),
                    _ => break,
                }
            }
            let _ = std::fs::remove_dir(&cg);
        }
        self.pid = None;
    }

    /// Waits for the VMM to exit on its own (guest power-off).
    pub fn wait_exit(&mut self, timeout: Duration) -> bool {
        let start = Instant::now();
        while start.elapsed() < timeout {
            if !self.alive() {
                self.kill();
                return true;
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        false
    }

    /// Kills the VMM and removes the jail, its cgroup and namespace.
    pub fn destroy(mut self) {
        self.kill();
        remove_netns(&self.netns);
        let _ = std::fs::remove_dir_all(&self.dir);
    }
}

fn tail(s: &str) -> String {
    let lines: Vec<&str> = s.lines().collect();
    lines[lines.len().saturating_sub(5)..].join(" | ")
}

/// Minimal HTTP/1.1 client for Firecracker's API socket.
pub fn api_call(sock: &Path, method: &str, path: &str, body: &str) -> Result<String> {
    let mut stream = UnixStream::connect(sock).map_err(|e| EngineError::host(format!("firecracker API: {e}")))?;
    stream.set_read_timeout(Some(Duration::from_secs(300)))?;
    let request = format!(
        "{method} {path} HTTP/1.1\r\nHost: localhost\r\nAccept: application/json\r\nContent-Type: application/json\r\nContent-Length: {}\r\n\r\n{body}",
        body.len()
    );
    stream.write_all(request.as_bytes())?;
    let mut reader = BufReader::new(stream);
    let mut status_line = String::new();
    reader.read_line(&mut status_line)?;
    let status: u16 = status_line
        .split_whitespace()
        .nth(1)
        .and_then(|s| s.parse().ok())
        .ok_or_else(|| EngineError::host(format!("bad API status line: {status_line:?}")))?;
    let mut length = 0_usize;
    loop {
        let mut line = String::new();
        reader.read_line(&mut line)?;
        let line = line.trim_end();
        if line.is_empty() {
            break;
        }
        if let Some((k, v)) = line.split_once(':')
            && k.eq_ignore_ascii_case("content-length")
        {
            length = v.trim().parse().unwrap_or(0);
        }
    }
    let mut response = vec![0_u8; length.min(1 << 20)];
    reader.read_exact(&mut response)?;
    let text = String::from_utf8_lossy(&response).into_owned();
    if !(200..300).contains(&status) {
        return Err(EngineError::host(format!("firecracker {method} {path}: {status} {text}")));
    }
    Ok(text)
}

/// Quotes a JSON string value.
pub fn js(s: &str) -> String {
    serde_json::to_string(s).unwrap()
}
