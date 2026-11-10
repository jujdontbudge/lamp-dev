//! Post-admission host helper profile: assigned descriptors only, no opens,
//! socket creation, exec, namespace changes or subprocesses after confinement.
use std::{io, ptr};
#[repr(C)]
struct Header {
    version: u32,
    pid: i32,
}
#[repr(C)]
#[derive(Clone, Copy)]
struct Caps {
    effective: u32,
    permitted: u32,
    inheritable: u32,
}
fn check(n: libc::c_long) -> io::Result<()> {
    if n < 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}
pub fn close_other_fds(keep: &[i32]) -> io::Result<()> {
    let mut keep: Vec<u32> = keep
        .iter()
        .copied()
        .filter(|fd| *fd >= 3)
        .map(|fd| fd as u32)
        .collect();
    keep.sort_unstable();
    keep.dedup();
    let mut first = 3;
    for fd in keep {
        if first < fd {
            check(unsafe { crate::close_range(first, fd - 1, 0) } as _)?;
        }
        first = fd + 1;
    }
    check(unsafe { crate::close_range(first, u32::MAX, 0) } as _)
}
pub fn enter(uid: u32, net_admin: bool, keep: &[i32]) -> io::Result<()> {
    enter_with(uid, net_admin, keep, &[])
}

/// Syscalls the mediated proxy needs beyond the helper profile: it accepts
/// guest connections and dials one checked origin per request.
pub const PROXY_SYSCALLS: &[libc::c_long] = &[
    libc::SYS_accept4,
    libc::SYS_socket,
    libc::SYS_connect,
    libc::SYS_setsockopt,
    libc::SYS_getsockopt,
    libc::SYS_getsockname,
    libc::SYS_getpeername,
    libc::SYS_shutdown,
    libc::SYS_ioctl,
    POLL,
    libc::SYS_writev,
];
#[cfg(target_arch = "x86_64")]
const POLL: libc::c_long = libc::SYS_poll;
// aarch64 has no poll(2); libc uses ppoll, which the base profile allows.
#[cfg(not(target_arch = "x86_64"))]
const POLL: libc::c_long = libc::SYS_ppoll;

/// Like [`enter`], additionally allowing `extra` syscalls.
pub fn enter_with(
    uid: u32,
    net_admin: bool,
    keep: &[i32],
    extra: &[libc::c_long],
) -> io::Result<()> {
    close_other_fds(keep)?;
    if uid < 65536 || uid == u32::MAX {
        return Err(io::Error::other("unique subordinate guard UID required"));
    }
    unsafe {
        check(libc::prctl(libc::PR_SET_KEEPCAPS, 1, 0, 0, 0) as _)?;
        check(libc::setgroups(0, ptr::null()) as _)?;
        // Drop the capability bounding set while CAP_SETPCAP is still available.
        for cap in 0..64 {
            if net_admin && cap == 12
            /* CAP_NET_ADMIN */
            {
                continue;
            }
            let n = libc::prctl(libc::PR_CAPBSET_DROP, cap, 0, 0, 0);
            if n < 0 && io::Error::last_os_error().raw_os_error() != Some(libc::EINVAL) {
                check(n as _)?;
            }
        }
        check(libc::setresgid(uid, uid, uid) as _)?;
        check(libc::setresuid(uid, uid, uid) as _)?;
        let mut caps = [Caps {
            effective: 0,
            permitted: 0,
            inheritable: 0,
        }; 2];
        if net_admin {
            caps[0].effective = 1 << 12;
            caps[0].permitted = 1 << 12;
        }
        check(libc::syscall(
            libc::SYS_capset,
            &Header {
                version: 0x20080522,
                pid: 0,
            },
            caps.as_ptr(),
        ))?;
        check(libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0) as _)?;
        check(libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) as _)?;
    }
    let arch = if cfg!(target_arch = "x86_64") {
        0xc000003e
    } else if cfg!(target_arch = "aarch64") {
        0xc00000b7
    } else {
        return Err(io::Error::other("unsupported guard architecture"));
    };
    let ins = |code, jt, jf, k| libc::sock_filter { code, jt, jf, k };
    let mut filter = vec![
        ins(0x20, 0, 0, 4),
        ins(0x15, 1, 0, arch),
        ins(0x06, 0, 0, 0x80000000),
        ins(0x20, 0, 0, 0),
    ];
    let allowed = [
        libc::SYS_read,
        libc::SYS_write,
        libc::SYS_close,
        libc::SYS_fstat,
        libc::SYS_newfstatat,
        libc::SYS_fcntl,
        libc::SYS_lseek,
        libc::SYS_fsync,
        libc::SYS_fdatasync,
        libc::SYS_sendto,
        libc::SYS_recvfrom,
        libc::SYS_sendmsg,
        libc::SYS_recvmsg,
        libc::SYS_ppoll,
        libc::SYS_rt_sigreturn,
        libc::SYS_rt_sigaction,
        libc::SYS_rt_sigprocmask,
        libc::SYS_sigaltstack,
        libc::SYS_clock_gettime,
        libc::SYS_clock_nanosleep,
        libc::SYS_nanosleep,
        libc::SYS_brk,
        libc::SYS_mmap,
        libc::SYS_munmap,
        libc::SYS_mremap,
        libc::SYS_madvise,
        libc::SYS_mprotect,
        libc::SYS_futex,
        libc::SYS_getrandom,
        libc::SYS_restart_syscall,
        libc::SYS_exit,
        libc::SYS_exit_group,
        libc::SYS_getpid,
        libc::SYS_gettid,
    ];
    for nr in allowed.iter().chain(extra) {
        let nr = *nr;
        filter.push(ins(0x15, 0, 1, nr as u32));
        filter.push(ins(0x06, 0, 0, 0x7fff0000));
    }
    filter.push(ins(0x06, 0, 0, 0x80000000));
    let program = libc::sock_fprog {
        len: filter.len() as u16,
        filter: filter.as_mut_ptr(),
    };
    check(unsafe { libc::prctl(libc::PR_SET_SECCOMP, 2, &program, 0, 0) } as _)
}
