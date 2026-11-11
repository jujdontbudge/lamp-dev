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
    use std::os::unix::fs::PermissionsExt;
    use std::process::Command;
    use std::time::{Duration, Instant};

    const HELPER: &str = "/sbin/lamp-guest-helper";

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
        mount(
            "tmpfs",
            "/tmp",
            "tmpfs",
            libc::MS_NOSUID | libc::MS_NODEV,
            "mode=1777",
        );
        // Commands start in the tasks cgroup. Guest root may move anywhere in
        // the hierarchy, so the helper's capture freeze covers every cgroup,
        // not just this one; init and the helper stay in the root cgroup.
        mount("cgroup2", "/sys/fs/cgroup", "cgroup2", nosuid, "");
        if let Err(e) = std::fs::create_dir_all(lamp_init::TASKS_CGROUP) {
            eprintln!("lamp-init: create tasks cgroup: {e}");
        }
        let _ = std::fs::create_dir_all("/run/lamp");
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
                        "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
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
