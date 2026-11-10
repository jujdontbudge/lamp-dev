//! Protected task-tree collector. Run as guest root before releasing any task.
//! Task root is root in a subordinate user/PID namespace, not guest kernel root.
//! The tracer remains outside those namespaces and owns the only host audit FD.
use crate::audit::Emitter;
use serde_json::{Value, json};
use std::{
    collections::HashMap,
    ffi::CString,
    fs, io,
    os::unix::fs::MetadataExt,
    ptr,
    sync::{
        Arc, Mutex,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

fn trace(request: libc::c_uint, pid: i32, address: usize, data: usize) -> io::Result<libc::c_long> {
    let n = unsafe {
        libc::ptrace(
            request,
            pid,
            address as *mut libc::c_void,
            data as *mut libc::c_void,
        )
    };
    if n < 0 {
        Err(io::Error::last_os_error())
    } else {
        Ok(n)
    }
}
fn status(pid: i32) -> io::Result<Value> {
    let s = fs::read_to_string(format!("/proc/{pid}/status"))?;
    let field = |name: &str| {
        s.lines()
            .find_map(|l| l.strip_prefix(name))
            .unwrap_or("")
            .trim()
            .to_string()
    };
    Ok(
        json!({"tgid":field("Tgid:"),"parent_pid":field("PPid:"),"uid":field("Uid:"),
        "gid":field("Gid:"),"namespace_pids":field("NSpid:")}),
    )
}
fn identity(pid: i32) -> io::Result<Value> {
    let mut s = status(pid)?;
    let path = format!("/proc/{pid}/exe");
    let exe = fs::read_link(&path)?;
    let meta = fs::metadata(&path)?;
    let stat = fs::read_to_string(format!("/proc/{pid}/stat"))?;
    let tail = stat
        .rsplit_once(')')
        .ok_or_else(|| io::Error::other("invalid proc stat"))?
        .1;
    s["pid"] = json!(pid);
    s["start_ticks"] = json!(
        tail.split_whitespace()
            .nth(19)
            .ok_or_else(|| io::Error::other("missing start time"))?
    );
    s["executable"] = json!({"path":exe,"device":meta.dev(),"inode":meta.ino()});
    // Raw argv/env may contain secrets. Do not serialize them before T23 redaction.
    s["safe_argv"] = json!([]);
    s["argv_redacted"] = json!(true);
    Ok(s)
}
fn emit(emitter: &Arc<Mutex<Emitter>>, v: Value) -> io::Result<()> {
    emitter
        .lock()
        .map_err(|_| io::Error::other("audit mutex poisoned"))?
        .send(v)
}
fn register(
    known: &mut HashMap<i32, bool>,
    emitter: &Arc<Mutex<Emitter>>,
    pid: i32,
) -> io::Result<()> {
    if known.contains_key(&pid) {
        return Ok(());
    }
    let id = identity(pid)?;
    let thread = id["tgid"].as_str() != Some(&pid.to_string());
    emit(
        emitter,
        if thread {
            json!({"type":"thread_start","identity":id})
        } else {
            json!({"type":"process_start","visibility":"process","infrastructure":false,"identity":id})
        },
    )?;
    known.insert(pid, false);
    Ok(())
}
fn c(s: &str) -> CString {
    CString::new(s).unwrap()
}
fn child_fail() -> ! {
    unsafe { libc::_exit(126) }
}
fn check(n: i32) {
    if n < 0 {
        child_fail()
    }
}

// Ptrace alone is NOT a containment boundary: CLONE_UNTRACED otherwise escapes it.
// clone3 points to mutable task memory, so reject it with ENOSYS (libc falls back
// to clone). Reject compat/x32 rather than allow an unchecked syscall ABI.
fn task_seccomp() {
    let arch = if cfg!(target_arch = "x86_64") {
        0xc000003e
    } else if cfg!(target_arch = "aarch64") {
        0xc00000b7
    } else {
        child_fail()
    };
    let ins = |code, jt, jf, k| libc::sock_filter { code, jt, jf, k };
    let mut code = [
        ins(0x20, 0, 0, 4), // seccomp_data.arch
        ins(0x15, 1, 0, arch),
        ins(0x06, 0, 0, 0x80000000), // KILL_PROCESS
        ins(0x20, 0, 0, 0),          // nr
        ins(0x45, 0, 1, 0x40000000), // x32
        ins(0x06, 0, 0, 0x80000000),
        // io_uring can create sockets/workers without the socket/clone syscalls.
        ins(0x15, 0, 1, libc::SYS_io_uring_setup as u32),
        ins(0x06, 0, 0, 0x00050000 | libc::EPERM as u32),
        ins(0x15, 0, 1, libc::SYS_io_uring_enter as u32),
        ins(0x06, 0, 0, 0x00050000 | libc::EPERM as u32),
        ins(0x15, 0, 1, libc::SYS_io_uring_register as u32),
        ins(0x06, 0, 0, 0x00050000 | libc::EPERM as u32),
        ins(0x15, 0, 1, libc::SYS_clone3 as u32),
        ins(0x06, 0, 0, 0x00050000 | libc::ENOSYS as u32),
        ins(0x15, 0, 4, libc::SYS_clone as u32),
        ins(0x20, 0, 0, 16), // args[0], low word (supported little-endian ABIs)
        ins(0x45, 0, 1, libc::CLONE_UNTRACED as u32),
        ins(0x06, 0, 0, 0x00050000 | libc::EPERM as u32),
        ins(0x06, 0, 0, 0x7fff0000), // traced clone is safe
        // Never expose host vsock or another out-of-band network family to tasks.
        ins(0x20, 0, 0, 0),
        ins(0x15, 1, 0, libc::SYS_socket as u32),
        ins(0x15, 0, 6, libc::SYS_socketpair as u32),
        ins(0x20, 0, 0, 16),
        ins(0x15, 4, 0, libc::AF_UNIX as u32),
        ins(0x15, 3, 0, libc::AF_INET as u32),
        ins(0x15, 2, 0, libc::AF_INET6 as u32),
        ins(0x15, 1, 0, libc::AF_NETLINK as u32),
        ins(0x06, 0, 0, 0x00050000 | libc::EPERM as u32),
        ins(0x06, 0, 0, 0x7fff0000), // ALLOW
    ];
    let program = libc::sock_fprog {
        len: code.len() as u16,
        filter: code.as_mut_ptr(),
    };
    check(unsafe { libc::prctl(libc::PR_SET_SECCOMP, 2, ptr::from_ref(&program), 0, 0) });
}

/// No shell expansion. Caller assigns a unique subordinate UID/GID per sandbox.
/// This collector is a demo correctness path, not E02 performance qualification.
pub fn run(emitter: Emitter, uid: u32, argv: Vec<String>) -> io::Result<i32> {
    if unsafe { libc::geteuid() } != 0 || uid < 65536 || uid == u32::MAX || argv.is_empty() {
        return Err(io::Error::other(
            "collector requires guest root, unique uid >=65536 and an argv",
        ));
    }
    if fs::read_to_string("/proc/sys/kernel/unprivileged_bpf_disabled")?.trim() == "0" {
        return Err(io::Error::other("guest unprivileged BPF must be disabled"));
    }
    let args: Vec<CString> = argv
        .iter()
        .map(|v| CString::new(v.as_str()).map_err(io::Error::other))
        .collect::<io::Result<_>>()?;
    let mut pointers: Vec<_> = args.iter().map(|a| a.as_ptr()).collect();
    pointers.push(ptr::null());
    // No threads exist yet at fork. The child closes the audit socket before any task code.
    let root = unsafe { libc::fork() };
    if root < 0 {
        return Err(io::Error::last_os_error());
    }
    if root == 0 {
        unsafe {
            check(libc::close_range(3, u32::MAX, 0));
            check(libc::prctl(libc::PR_SET_PDEATHSIG, libc::SIGKILL));
            check(libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0));
            if libc::ptrace(
                libc::PTRACE_TRACEME,
                0,
                ptr::null_mut::<libc::c_void>(),
                ptr::null_mut::<libc::c_void>(),
            ) < 0
            {
                child_fail();
            }
            libc::raise(libc::SIGSTOP);
            check(libc::unshare(libc::CLONE_NEWNS | libc::CLONE_NEWPID));
            check(libc::mount(
                ptr::null(),
                c("/").as_ptr(),
                ptr::null(),
                libc::MS_REC | libc::MS_PRIVATE,
                ptr::null(),
            ));
            let task = libc::fork();
            if task < 0 {
                child_fail();
            }
            if task == 0 {
                // Detach inherited proc first: task root must not unmount the new proc
                // and reveal a view of the tracer's ancestor PID namespace underneath.
                check(libc::umount2(c("/proc").as_ptr(), libc::MNT_DETACH));
                check(libc::mount(
                    c("proc").as_ptr(),
                    c("/proc").as_ptr(),
                    c("proc").as_ptr(),
                    libc::MS_NOSUID | libc::MS_NODEV | libc::MS_NOEXEC,
                    ptr::null(),
                ));
                check(libc::setgroups(0, ptr::null()));
                check(libc::unshare(libc::CLONE_NEWUSER));
                libc::raise(libc::SIGSTOP); // only the trusted parent can install the UID maps
                check(libc::setresgid(0, 0, 0));
                check(libc::setresuid(0, 0, 0));
                task_seccomp();
                // All inherited guest environment is stripped. No host secret comes from getenv.
                check(libc::clearenv());
                check(libc::setenv(
                    c("PATH").as_ptr(),
                    c("/usr/bin:/bin").as_ptr(),
                    1,
                ));
                check(libc::setenv(c("HOME").as_ptr(), c("/tmp").as_ptr(), 1));
                libc::execvp(args[0].as_ptr(), pointers.as_ptr());
                child_fail();
            }
            let mut code = 0;
            loop {
                let n = libc::waitpid(task, &mut code, 0);
                if n == task {
                    break;
                }
                if n < 0 && *libc::__errno_location() != libc::EINTR {
                    child_fail();
                }
            }
            libc::_exit(if libc::WIFEXITED(code) {
                libc::WEXITSTATUS(code)
            } else {
                128 + libc::WTERMSIG(code)
            });
        }
    }
    // Initial stop must be consumed and EXITKILL installed before fallible audit I/O.
    let mut initial = 0;
    if unsafe { libc::waitpid(root, &mut initial, 0) } != root || !libc::WIFSTOPPED(initial) {
        unsafe { libc::kill(root, libc::SIGKILL) };
        return Err(io::Error::other("collector child did not stop"));
    }
    let options = libc::PTRACE_O_TRACEFORK
        | libc::PTRACE_O_TRACEVFORK
        | libc::PTRACE_O_TRACECLONE
        | libc::PTRACE_O_TRACEEXEC
        | libc::PTRACE_O_EXITKILL
        | libc::PTRACE_O_TRACESYSGOOD;
    if let Err(e) = trace(libc::PTRACE_SETOPTIONS, root, 0, options as usize) {
        unsafe { libc::kill(root, libc::SIGKILL) };
        return Err(e);
    }
    let emitter = Arc::new(Mutex::new(emitter));
    let alive = emitter.clone();
    let complete = Arc::new(AtomicBool::new(false));
    let completed = complete.clone();
    // Host loss while the task is in a CPU-only loop still kills the entire traced tree.
    // exit(70) exits the tracer process, activating kernel PTRACE_O_EXITKILL.
    std::thread::spawn(move || {
        loop {
            std::thread::sleep(Duration::from_millis(250));
            let Ok(mut emitter) = alive.lock() else {
                std::process::exit(70);
            };
            if completed.load(Ordering::SeqCst) {
                break;
            }
            if emitter.send(json!({"type":"heartbeat"})).is_err() {
                std::process::exit(70);
            }
        }
    });
    emit(
        &emitter,
        json!({"type":"process_start","visibility":"process","infrastructure":true,"identity":identity(root)?}),
    )?;
    let mut known = HashMap::new();
    known.insert(root, false); // value: credential syscall in flight
    let mut task_pid = None;
    let mut mapped = false;
    let mut result = 0;
    trace(libc::PTRACE_SYSCALL, root, 0, 0)?;
    loop {
        let mut code = 0;
        let pid = unsafe { libc::waitpid(-1, &mut code, libc::__WALL) };
        if pid < 0 {
            let e = io::Error::last_os_error();
            if e.raw_os_error() == Some(libc::EINTR) {
                continue;
            }
            return Err(e);
        }
        if libc::WIFEXITED(code) || libc::WIFSIGNALED(code) {
            emit(
                &emitter,
                json!({"type":"process_exit","pid":pid,"exit_code":if libc::WIFEXITED(code){libc::WEXITSTATUS(code)}else{128+libc::WTERMSIG(code)}}),
            )?;
            known.remove(&pid);
            if pid == root {
                result = if libc::WIFEXITED(code) {
                    libc::WEXITSTATUS(code)
                } else {
                    128 + libc::WTERMSIG(code)
                };
            }
            if known.is_empty() {
                break;
            }
            continue;
        }
        if !libc::WIFSTOPPED(code) {
            return Err(io::Error::other("unexpected ptrace state"));
        }
        let signal = libc::WSTOPSIG(code);
        let event = code >> 16;
        if !known.contains_key(&pid) {
            trace(libc::PTRACE_SETOPTIONS, pid, 0, options as usize)?;
            register(&mut known, &emitter, pid)?;
        }
        let mut deliver = 0;
        if event == libc::PTRACE_EVENT_FORK
            || event == libc::PTRACE_EVENT_VFORK
            || event == libc::PTRACE_EVENT_CLONE
        {
            let mut child = 0_usize;
            trace(
                libc::PTRACE_GETEVENTMSG,
                pid,
                0,
                ptr::from_mut(&mut child) as usize,
            )?;
            if pid == root && task_pid.is_none() {
                task_pid = Some(child as i32);
            }
            // Record the child's creation BEFORE resuming the parent. Waiting for
            // the child's own initial stop can miss a child killed by its parent
            // (or namespace-init death) before that stop is consumed. Children
            // inherit all ptrace options and cannot run before our resume.
            register(&mut known, &emitter, child as i32)?;
        } else if event == libc::PTRACE_EVENT_EXEC {
            let mut old = 0_usize;
            trace(
                libc::PTRACE_GETEVENTMSG,
                pid,
                0,
                ptr::from_mut(&mut old) as usize,
            )?;
            if old as i32 != pid {
                known.remove(&(old as i32));
            }
            emit(
                &emitter,
                json!({"type":"process_exec","visibility":"process","identity":identity(pid)?}),
            )?;
        } else if signal == libc::SIGTRAP | 0x80 {
            let mut info = [0_u8; 88];
            let size = trace(
                libc::PTRACE_GET_SYSCALL_INFO,
                pid,
                info.len(),
                info.as_mut_ptr() as usize,
            )?;
            if size < 24 {
                return Err(io::Error::other("missing ptrace syscall information"));
            }
            if info[0] == 1 {
                let nr = u64::from_ne_bytes(info[24..32].try_into().unwrap());
                let arch = u32::from_ne_bytes(info[4..8].try_into().unwrap());
                let relevant = match arch {
                    0xc000003e => {
                        [105, 106, 113, 114, 116, 117, 119, 122, 123, 272, 308].contains(&nr)
                    }
                    0xc00000b7 => [97, 144, 145, 146, 147, 149, 151, 152, 159, 268].contains(&nr),
                    0x40000003 => [
                        23, 46, 70, 71, 81, 138, 139, 164, 170, 199, 200, 203, 204, 206, 208, 210,
                        213, 214, 215, 310, 346,
                    ]
                    .contains(&nr),
                    _ => return Err(io::Error::other("unsupported syscall audit architecture")),
                };
                known.insert(pid, relevant);
            } else if info[0] == 2 && known.get(&pid) == Some(&true) {
                emit(
                    &emitter,
                    json!({"type":"credential_change","pid":pid,"identity":status(pid)?}),
                )?;
                known.insert(pid, false);
            }
        } else if signal == libc::SIGSTOP {
            if Some(pid) == task_pid
                && !mapped
                && fs::read_to_string(format!("/proc/{pid}/uid_map"))?
                    .trim()
                    .is_empty()
            {
                fs::write(format!("/proc/{pid}/setgroups"), "deny")?;
                fs::write(format!("/proc/{pid}/uid_map"), format!("0 {uid} 1\n"))?;
                fs::write(format!("/proc/{pid}/gid_map"), format!("0 {uid} 1\n"))?;
                mapped = true;
                emit(
                    &emitter,
                    json!({"type":"credential_change","pid":pid,"identity":status(pid)?}),
                )?;
            }
        } else if signal != libc::SIGTRAP {
            deliver = signal;
        }
        trace(libc::PTRACE_SYSCALL, pid, 0, deliver as usize)?;
    }
    if !mapped {
        return Err(io::Error::other("task namespace admission failed"));
    }
    complete.store(true, Ordering::SeqCst);
    emit(
        &emitter,
        json!({"type":"collector_complete","exit_code":result}),
    )?;
    Ok(result)
}
