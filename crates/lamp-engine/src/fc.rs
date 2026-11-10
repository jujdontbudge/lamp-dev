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
/// RAM-backed directory visible inside each jail for memory capture output.
pub const MEMDIR: &str = "mem.d";

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
    /// Host side of the jail's RAM-backed `/mem.d`.
    pub memdir: PathBuf,
    pub pid: Option<i32>,
}

pub fn cgroup_dir(id: &str) -> PathBuf {
    Path::new("/sys/fs/cgroup").join(CGROUP_PARENT).join(id)
}

/// Shared cgroup charged for capture images. An image outlives the source
/// that wrote it (the fork owns it), so its RAM must not count against the
/// source's own `memory.max`.
pub const LAYER_CGROUP: &str = "layers";

/// Makes sure the parent cgroup exists with the controllers jails need.
pub fn init_cgroups() -> Result<()> {
    let parent = Path::new("/sys/fs/cgroup").join(CGROUP_PARENT);
    std::fs::create_dir_all(&parent).ctx("create lamp cgroup")?;
    std::fs::write(
        parent.join("cgroup.subtree_control"),
        "+cpu +memory +pids +io",
    )
    .ctx("enable lamp cgroup controllers")?;
    std::fs::create_dir_all(cgroup_dir(LAYER_CGROUP)).ctx("create layer cgroup")
}

/// The next capture's memory image, already allocated (`SPARE_READY`), or
/// being allocated (`SPARE_PREP`), inside the jail's memdir.
pub const SPARE_READY: &str = "cap.mem.ready";
const SPARE_PREP: &str = "cap.mem.prep";

/// Allocates the next capture image of `bytes` in the background, outside
/// any pause and outside any caller's operation. Allocating and zeroing 4 GiB
/// of tmpfs huge pages costs ~0.3 s on LX; Firecracker then only copies guest
/// RAM into pages that already exist (it does not truncate an existing
/// file). The pages are charged to the shared layer cgroup, exactly like a
/// capture image, because that is what the file becomes. Failure is harmless:
/// the next capture falls back to letting Firecracker allocate.
pub fn prepare_spare(jail: &Jail, bytes: u64) {
    let (memdir, uid) = (jail.memdir.clone(), jail.uid);
    std::thread::spawn(move || {
        let prep = memdir.join(SPARE_PREP);
        let _ = std::fs::remove_file(&prep);
        let layer = CString::new(
            cgroup_dir(LAYER_CGROUP)
                .join("cgroup.procs")
                .as_os_str()
                .as_bytes(),
        )
        .unwrap();
        let mut cmd = Command::new("fallocate");
        cmd.arg("-l").arg(bytes.to_string()).arg(&prep);
        cmd.stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        // SAFETY: between fork and exec the hook makes only async-signal-safe
        // calls (open/write/close on a path built beforehand), moving this
        // process into the layer cgroup so the new pages are charged there.
        unsafe {
            use std::os::unix::process::CommandExt;
            cmd.pre_exec(move || {
                let fd = libc::open(layer.as_ptr(), libc::O_WRONLY | libc::O_CLOEXEC);
                if fd < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                let n = libc::write(fd, b"0".as_ptr().cast(), 1);
                libc::close(fd);
                if n != 1 {
                    return Err(std::io::Error::other("join layer cgroup"));
                }
                Ok(())
            });
        }
        let ok = cmd.output().map(|o| o.status.success()).unwrap_or(false)
            && fsutil::chown(&prep, uid, uid).is_ok()
            && fsutil::chmod(&prep, 0o600).is_ok()
            && std::fs::rename(&prep, memdir.join(SPARE_READY)).is_ok();
        if !ok {
            let _ = std::fs::remove_file(&prep);
        }
    });
}

/// While alive, the jail's processes run in the shared layer cgroup, so
/// memory they newly allocate (the snapshot's tmpfs pages) is charged there.
/// Pages already charged, such as guest RAM, stay with the sandbox: cgroup v2
/// never moves existing charges. Dropping it moves exactly those processes
/// back, even if the capture failed.
pub struct LayerCharge {
    home: PathBuf,
    pids: Vec<String>,
}

impl LayerCharge {
    pub fn begin(jail_id: &str) -> Result<LayerCharge> {
        let home = cgroup_dir(jail_id);
        let procs = std::fs::read_to_string(home.join("cgroup.procs")).ctx("read cgroup.procs")?;
        let mut charge = LayerCharge {
            home,
            pids: Vec::new(),
        };
        let layer = cgroup_dir(LAYER_CGROUP).join("cgroup.procs");
        for pid in procs.split_whitespace() {
            std::fs::write(&layer, pid).ctx("move VMM to layer cgroup")?;
            charge.pids.push(pid.to_string());
        }
        Ok(charge)
    }
}

impl Drop for LayerCharge {
    fn drop(&mut self) {
        for pid in &self.pids {
            if let Err(e) = std::fs::write(self.home.join("cgroup.procs"), pid) {
                // Only a process that already exited can fail to move.
                eprintln!("lamp-engine: return pid {pid} to its jail cgroup: {e}");
            }
        }
    }
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
                    return Err(EngineError::host(format!(
                        "unshare netns: {}",
                        std::io::Error::last_os_error()
                    )));
                }
                let src = c"/proc/thread-self/ns/net";
                if libc::mount(
                    src.as_ptr(),
                    target.as_ptr(),
                    std::ptr::null(),
                    libc::MS_BIND,
                    std::ptr::null(),
                ) != 0
                {
                    return Err(EngineError::host(format!(
                        "bind netns: {}",
                        std::io::Error::last_os_error()
                    )));
                }
            }
            Ok(())
        })
        .join()
        .unwrap()
    })
}

fn bind_mount(src: &Path, dst: &Path) -> Result<()> {
    let s = CString::new(src.as_os_str().as_bytes()).unwrap();
    let d = CString::new(dst.as_os_str().as_bytes()).unwrap();
    // SAFETY: valid paths; a plain bind mount.
    if unsafe {
        libc::mount(
            s.as_ptr(),
            d.as_ptr(),
            std::ptr::null(),
            libc::MS_BIND,
            std::ptr::null(),
        )
    } != 0
    {
        return Err(EngineError::host(format!(
            "bind {}: {}",
            dst.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

pub fn unmount(path: &Path) {
    if let Ok(c) = CString::new(path.as_os_str().as_bytes()) {
        // SAFETY: valid path; failure is fine (not mounted).
        unsafe { libc::umount2(c.as_ptr(), libc::MNT_DETACH) };
    }
}

/// Mounts a tmpfs at `dir` unless something is already mounted there. It
/// is root-only and uses transparent huge pages, which roughly halves the
/// cost of writing a full RAM image into it.
pub fn ensure_tmpfs(dir: &Path, size: &str) -> Result<()> {
    std::fs::create_dir_all(dir).ctx("create memory dir")?;
    let mounts = std::fs::read_to_string("/proc/self/mounts").unwrap_or_default();
    let want = dir.to_string_lossy();
    let d = CString::new(dir.as_os_str().as_bytes()).unwrap();
    if mounts
        .lines()
        .any(|l| l.split_whitespace().nth(1) == Some(&want))
    {
        // Already mounted (e.g. by an earlier run): enforce huge pages.
        // SAFETY: valid strings; a tmpfs remount changes only `huge`.
        if unsafe {
            libc::mount(
                std::ptr::null(),
                d.as_ptr(),
                std::ptr::null(),
                libc::MS_REMOUNT | libc::MS_NOSUID | libc::MS_NODEV | libc::MS_NOEXEC,
                c"huge=always".as_ptr().cast(),
            )
        } != 0
        {
            return Err(EngineError::host(format!(
                "remount {}: {}",
                dir.display(),
                std::io::Error::last_os_error()
            )));
        }
        return Ok(());
    }
    let opts = CString::new(format!("size={size},mode=0700,huge=always")).unwrap();
    // SAFETY: valid strings.
    if unsafe {
        libc::mount(
            c"lamp-mem".as_ptr(),
            d.as_ptr(),
            c"tmpfs".as_ptr(),
            libc::MS_NOSUID | libc::MS_NODEV | libc::MS_NOEXEC,
            opts.as_ptr().cast(),
        )
    } != 0
    {
        return Err(EngineError::host(format!(
            "mount tmpfs {}: {}",
            dir.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

fn remove_netns(path: &Path) {
    if let Ok(c) = CString::new(path.as_os_str().as_bytes()) {
        // SAFETY: valid path; failure is fine (not mounted).
        unsafe { libc::umount2(c.as_ptr(), libc::MNT_DETACH) };
    }
    let _ = std::fs::remove_file(path);
}

impl Jail {
    /// Creates the jail directory (owned by the jail UID), its private
    /// network namespace and its RAM-backed `/mem.d`.
    pub fn create(
        base: &Path,
        netns_dir: &Path,
        mem_dir: &Path,
        id: &str,
        uid: u32,
    ) -> Result<Jail> {
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
        let memdir = mem_dir.join("jails").join(id);
        let jail = Jail {
            id: id.into(),
            dir,
            root,
            uid,
            netns,
            memdir,
            pid: None,
        };
        let bind = (|| -> Result<()> {
            std::fs::create_dir_all(&jail.memdir).ctx("create memdir")?;
            fsutil::chown(&jail.memdir, uid, uid)?;
            fsutil::chmod(&jail.memdir, 0o700)?;
            let target = jail.root.join(MEMDIR);
            std::fs::create_dir(&target).ctx("create jail memdir")?;
            bind_mount(&jail.memdir, &target)
        })();
        if let Err(e) = bind {
            jail.destroy();
            return Err(e);
        }
        Ok(jail)
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
            .args([
                "--uid",
                &self.uid.to_string(),
                "--gid",
                &self.uid.to_string(),
            ])
            .arg("--chroot-base-dir")
            .arg(self.dir.parent().and_then(Path::parent).unwrap())
            .args(["--cgroup-version", "2", "--parent-cgroup", CGROUP_PARENT])
            .args(["--cgroup", &format!("memory.max={memory_max}")])
            .args([
                "--cgroup",
                &format!("pids.max={}", limits.pids_max.clamp(64, 4096)),
            ])
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
            return Err(EngineError::host(format!(
                "jailer failed ({status}): {}",
                tail(&log)
            )));
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

    /// A check that stays true exactly while this VMM process lives; it
    /// outlives the jail record (stop, release) and ignores a reused PID.
    pub fn liveness(&self) -> impl Fn() -> bool + Send + 'static {
        process_liveness(self.pid)
    }

    pub fn alive(&self) -> bool {
        // SAFETY: signal 0 only checks existence.
        self.pid
            .is_some_and(|pid| unsafe { libc::kill(pid, 0) } == 0)
    }

    /// Kills every process in the jail's cgroup and returns only once the
    /// VMM process is gone. A task leaves its cgroup before it has finished
    /// exiting (closing the KVM VM, freeing guest RAM), so an empty or
    /// removed cgroup alone is not enough.
    pub fn kill(&mut self) {
        let vmm = self.pid.and_then(|pid| start_time(pid).map(|t| (pid, t)));
        let cg = cgroup_dir(&self.id);
        let start = Instant::now();
        if cg.exists() {
            let _ = std::fs::write(cg.join("cgroup.kill"), "1");
            while let Err(e) = std::fs::remove_dir(&cg) {
                if start.elapsed() > Duration::from_secs(10) {
                    eprintln!("lamp-engine: VMM cgroup {} not empty: {e}", cg.display());
                    break;
                }
                std::thread::sleep(Duration::from_millis(1));
            }
        }
        // The VMM is reparented to init, which reaps it; the start time
        // guards against a reused PID.
        if let Some((pid, t)) = vmm {
            while start_time(pid) == Some(t) {
                if start.elapsed() > Duration::from_secs(10) {
                    eprintln!("lamp-engine: VMM {pid} still exiting after 10 s");
                    break;
                }
                std::thread::sleep(Duration::from_millis(1));
            }
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

    /// Kills the VMM and removes the jail, its cgroup, namespace and memdir.
    pub fn destroy(mut self) {
        self.kill();
        remove_netns(&self.netns);
        unmount(&self.root.join(MEMDIR));
        let _ = std::fs::remove_dir_all(&self.memdir);
        let _ = std::fs::remove_dir_all(&self.dir);
    }
}

/// True while the process that has `pid` now still lives (a later process
/// reusing the PID does not count).
pub fn process_liveness(pid: Option<i32>) -> impl Fn() -> bool + Send + 'static {
    let vmm = pid.and_then(|pid| start_time(pid).map(|t| (pid, t)));
    move || vmm.is_some_and(|(pid, t)| start_time(pid) == Some(t))
}

/// A process's start time (clock ticks since boot), `None` once it is gone.
fn start_time(pid: i32) -> Option<u64> {
    let stat = std::fs::read_to_string(format!("/proc/{pid}/stat")).ok()?;
    // Fields after the parenthesised command; starttime is field 22.
    let rest = &stat[stat.rfind(')')? + 2..];
    rest.split_whitespace().nth(19)?.parse().ok()
}

fn tail(s: &str) -> String {
    let lines: Vec<&str> = s.lines().collect();
    lines[lines.len().saturating_sub(5)..].join(" | ")
}

/// Connects to the API socket. The socket file appears at `bind()`, a moment
/// before Firecracker calls `listen()`; a connect in between is refused.
/// Nothing has been sent then, so a refused connect is retried briefly.
fn connect_api(sock: &Path) -> Result<UnixStream> {
    let start = Instant::now();
    loop {
        match UnixStream::connect(sock) {
            Ok(s) => return Ok(s),
            Err(e)
                if e.kind() == std::io::ErrorKind::ConnectionRefused
                    && start.elapsed() < Duration::from_secs(2) =>
            {
                std::thread::sleep(Duration::from_micros(200));
            }
            Err(e) => return Err(EngineError::host(format!("firecracker API: {e}"))),
        }
    }
}

/// Minimal HTTP/1.1 client for Firecracker's API socket.
pub fn api_call(sock: &Path, method: &str, path: &str, body: &str) -> Result<String> {
    let mut stream = connect_api(sock)?;
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
        return Err(EngineError::host(format!(
            "firecracker {method} {path}: {status} {text}"
        )));
    }
    Ok(text)
}

/// Quotes a JSON string value.
pub fn js(s: &str) -> String {
    serde_json::to_string(s).unwrap()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};

    /// A socket that is bound but not yet listening refuses connects, as
    /// Firecracker's API socket does for a moment at startup.
    #[test]
    fn api_connect_waits_for_listen_after_bind() {
        let path = std::env::temp_dir().join(format!("lamp-api-{}", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let c = CString::new(path.as_os_str().as_bytes()).unwrap();
        // SAFETY: plain socket/bind with a zeroed, filled sockaddr_un.
        let fd = unsafe {
            let fd = libc::socket(libc::AF_UNIX, libc::SOCK_STREAM, 0);
            assert!(fd >= 0);
            let mut addr: libc::sockaddr_un = std::mem::zeroed();
            addr.sun_family = libc::AF_UNIX as libc::sa_family_t;
            for (d, s) in addr.sun_path.iter_mut().zip(c.as_bytes()) {
                *d = *s as libc::c_char;
            }
            let len = std::mem::size_of::<libc::sockaddr_un>() as libc::socklen_t;
            assert_eq!(libc::bind(fd, (&raw const addr).cast(), len), 0);
            OwnedFd::from_raw_fd(fd)
        };
        assert_eq!(
            UnixStream::connect(&path).unwrap_err().kind(),
            std::io::ErrorKind::ConnectionRefused
        );
        let raw = fd.as_raw_fd();
        let listener = std::thread::spawn(move || {
            std::thread::sleep(Duration::from_millis(50));
            // SAFETY: listen on the bound socket owned by `fd`.
            assert_eq!(unsafe { libc::listen(raw, 1) }, 0);
            fd
        });
        assert!(connect_api(&path).is_ok());
        drop(listener.join().unwrap());
        let _ = std::fs::remove_file(&path);
        // A socket nobody will ever listen on still fails, after the window.
        assert!(connect_api(&path).is_err());
    }

    /// R3: liveness follows the process itself, not files it leaves behind.
    #[test]
    fn liveness_ends_with_the_process() {
        let mut child = std::process::Command::new("sleep")
            .arg("30")
            .spawn()
            .unwrap();
        let alive = process_liveness(Some(child.id() as i32));
        assert!(alive());
        child.kill().unwrap();
        child.wait().unwrap();
        assert!(!alive());
        assert!(!process_liveness(None)());
    }
}
