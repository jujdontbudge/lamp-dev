//! Guest PID 1: mounts the minimal runtime filesystems, starts the guest
//! helper (restarting it if it dies) and reaps orphaned processes.
#[cfg(target_os = "linux")]
fn main() {
    linux::run()
}

#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("lamp-init runs only as PID 1 inside a Linux guest");
    std::process::exit(2);
}

#[cfg(target_os = "linux")]
mod linux {
    use std::ffi::CString;
    use std::os::unix::fs::{MetadataExt, PermissionsExt};
    use std::process::Command;
    use std::time::{Duration, Instant};

    const HELPER: &str = "/run/lamp/lamp-guest-helper";

    fn mount(source: &str, target: &str, fstype: &str, flags: libc::c_ulong, data: &str) {
        let _ = std::fs::create_dir_all(target);
        let (s, t, f, d) = (
            CString::new(source).unwrap(),
            CString::new(target).unwrap(),
            CString::new(fstype).unwrap(),
            CString::new(data).unwrap(),
        );
        // SAFETY: all pointers are valid NUL-terminated strings for the call.
        let rc =
            unsafe { libc::mount(s.as_ptr(), t.as_ptr(), f.as_ptr(), flags, d.as_ptr().cast()) };
        if rc != 0 {
            let err = std::io::Error::last_os_error();
            if err.raw_os_error() != Some(libc::EBUSY) {
                eprintln!("lamp-init: mount {target}: {err}");
            }
        }
    }

    /// The only NIC is the sandbox's private link to its host guard
    /// namespace. Addresses are fixed per link; the guard binds identity to
    /// the host interface, never to these guest addresses.
    fn configure_network() {
        if !std::path::Path::new("/sys/class/net/eth0").exists() {
            return;
        }
        for args in [
            &["link", "set", "lo", "up"][..],
            &["addr", "add", lamp_init::GUEST_ADDR_CIDR, "dev", "eth0"],
            &["link", "set", "eth0", "up"],
            &["route", "add", "default", "via", lamp_init::GUEST_GATEWAY],
        ] {
            match Command::new("ip")
                .args(args)
                .env_clear()
                .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
                .status()
            {
                Ok(s) if s.success() => {}
                other => eprintln!("lamp-init: ip {args:?}: {other:?}"),
            }
        }
        // A seed may contain the build host's systemd-resolved stub. This
        // guest runs Lamp as PID 1, so no local DNS daemon serves that address.
        // Managed links intercept DNS to this numeric resolver and use the
        // sandbox's selected exit; the legacy adapter can also route it.
        let resolver = std::fs::read_to_string("/etc/resolv.conf").unwrap_or_default();
        let usable = resolver.lines().any(|line| {
            let mut words = line.split_whitespace();
            words.next() == Some("nameserver")
                && words
                    .next()
                    .and_then(|s| s.parse::<std::net::IpAddr>().ok())
                    .is_some_and(|ip| !ip.is_loopback() && !ip.is_unspecified())
        });
        if !usable {
            if let Err(e) = std::fs::write(
                "/etc/resolv.conf.lamp",
                "nameserver 1.1.1.1\noptions timeout:2 attempts:2\n",
            )
            .and_then(|()| std::fs::rename("/etc/resolv.conf.lamp", "/etc/resolv.conf"))
            {
                eprintln!("lamp-init: configure DNS: {e}");
            }
        }
    }

    fn start_subscription_relay() {
        // This optional, recipe-installed service is trusted guest infrastructure;
        // protected tasks retain the seccomp denial of direct AF_VSOCK access.
        let trusted = [
            "/opt",
            "/opt/lamp",
            "/opt/lamp/subscription.json",
            "/opt/lamp/relay.py",
        ]
        .iter()
        .all(|path| {
            std::fs::symlink_metadata(path).is_ok_and(|metadata| {
                !metadata.file_type().is_symlink()
                    && metadata.uid() == 0
                    && metadata.mode() & 0o022 == 0
            })
        });
        if trusted
            && let Err(error) = Command::new("/usr/bin/python3")
                .arg("/opt/lamp/relay.py")
                .env_clear()
                .env("PATH", "/usr/bin:/bin")
                .env("HOME", "/root")
                .stdin(std::process::Stdio::null())
                .stdout(std::process::Stdio::null())
                .stderr(std::process::Stdio::null())
                .spawn()
        {
            eprintln!("lamp-init: subscription relay could not start: {error}");
        }
    }

    pub fn run() {
        if std::process::id() != 1 {
            eprintln!("lamp-init: must run as PID 1");
            std::process::exit(2);
        }
        let nosuid = libc::MS_NOSUID | libc::MS_NODEV | libc::MS_NOEXEC;
        mount("proc", "/proc", "proc", nosuid, "");
        mount("sysfs", "/sys", "sysfs", nosuid, "");
        mount("devtmpfs", "/dev", "devtmpfs", libc::MS_NOSUID, "mode=0755");
        mount(
            "devpts",
            "/dev/pts",
            "devpts",
            libc::MS_NOSUID | libc::MS_NOEXEC,
            "gid=5,mode=620,ptmxmode=666",
        );
        mount(
            "tmpfs",
            "/dev/shm",
            "tmpfs",
            libc::MS_NOSUID | libc::MS_NODEV,
            "mode=1777",
        );
        mount(
            "tmpfs",
            "/run",
            "tmpfs",
            libc::MS_NOSUID | libc::MS_NODEV,
            "mode=0755",
        );
        // Commands start in the tasks cgroup. Guest root may move anywhere in
        // the hierarchy, so the helper's capture freeze covers every cgroup,
        // not just this one; init and the helper stay in the root cgroup.
        mount("cgroup2", "/sys/fs/cgroup", "cgroup2", nosuid, "");
        if let Err(e) = std::fs::create_dir_all(lamp_init::TASKS_CGROUP) {
            eprintln!("lamp-init: create tasks cgroup: {e}");
        }
        std::fs::create_dir_all("/run/lamp").expect("trusted runtime directory");
        std::fs::set_permissions("/run/lamp", std::fs::Permissions::from_mode(0o700))
            .expect("protect runtime");
        // Protect disk inodes too: a read-only bind alone can be bypassed by
        // making a hard link through the writable root mount. Task namespaces
        // cannot clear the kernel-root immutable flag.
        use std::os::fd::AsRawFd;
        for binary in ["lamp-init", "lamp-guest-helper", "lamp-guard"] {
            let file =
                std::fs::File::open(format!("/usr/sbin/{binary}")).expect("trusted boot binary");
            let mut flags: libc::c_long = 0;
            unsafe {
                assert_eq!(
                    libc::ioctl(file.as_raw_fd(), 0x80086601u64 as _, &mut flags),
                    0,
                    "read trusted inode flags"
                );
                flags |= 0x10;
                assert_eq!(
                    libc::ioctl(file.as_raw_fd(), 0x40086602u64 as _, &flags),
                    0,
                    "protect trusted inode"
                );
            }
        }
        for binary in ["lamp-guest-helper", "lamp-guard"] {
            std::fs::copy(format!("/usr/sbin/{binary}"), format!("/run/lamp/{binary}"))
                .expect("pin trusted runtime binary");
        }
        std::fs::set_permissions("/run", std::fs::Permissions::from_mode(0o1777))
            .expect("guest runtime directory");
        // Containerd uses /run/containerd for shim sockets/FIFOs even with a
        // custom state directory. Prepare only this guest runtime directory;
        // confined tasks keep their mapped UID and cannot write the rest of /run.
        if let Err(e) = std::fs::create_dir_all("/run/containerd").and_then(|()| {
            std::fs::set_permissions("/run/containerd", std::fs::Permissions::from_mode(0o1777))
        }) {
            eprintln!("lamp-init: create containerd runtime directory: {e}");
        }
        let _ = std::os::unix::fs::symlink("/proc/self/fd", "/dev/fd");
        // SAFETY: plain syscalls with valid arguments.
        unsafe {
            libc::sethostname(c"lamp".as_ptr(), 4);
            libc::setsid();
        }
        let _ = std::fs::write("/proc/sys/kernel/printk", "1");
        // The protected task collector refuses to run while unprivileged BPF
        // is available to tasks.
        let _ = std::fs::write("/proc/sys/kernel/unprivileged_bpf_disabled", "1");
        configure_network();
        start_subscription_relay();
        let mut helper = None;
        let mut last_start = Instant::now() - Duration::from_secs(10);
        loop {
            if helper.is_none() {
                // Back off a crashing helper so PID 1 never spins.
                let since = last_start.elapsed();
                if since < Duration::from_millis(500) {
                    std::thread::sleep(Duration::from_millis(500) - since);
                }
                last_start = Instant::now();
                match Command::new(HELPER)
                    .env_clear()
                    .env(
                        "PATH",
                        "/opt/venv/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                    )
                    .env("HOME", "/root")
                    .env("LANG", "C.UTF-8")
                    .current_dir("/")
                    .spawn()
                {
                    Ok(child) => helper = Some(child.id() as libc::pid_t),
                    Err(e) => eprintln!("lamp-init: start helper: {e}"),
                }
            }
            let mut status = 0;
            // SAFETY: reaps any child; status is a valid out pointer.
            let pid = unsafe { libc::waitpid(-1, &mut status, 0) };
            if pid > 0 && Some(pid) == helper {
                eprintln!("lamp-init: helper exited with status {status}; restarting");
                helper = None;
            } else if pid < 0 {
                std::thread::sleep(Duration::from_millis(100));
            }
        }
    }
}
