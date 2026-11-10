//! macOS guest helper: the native macOS counterpart of `lamp-guest-helper`.
//!
//! Runs as a root launchd daemon inside an owner-built macOS guest and
//! serves the same private host channel (virtio-vsock port 1024, the same
//! frames and messages). It applies the identity/clock/entropy barrier and
//! refuses every command whose sandbox/generation does not match the last
//! barrier. macOS has no cgroup freezer, so Freeze/Thaw are refused: the
//! native class has no memory fork (A2), and suspend pauses the whole VM.
#[cfg(target_os = "macos")]
fn main() {
    mac::run()
}

#[cfg(not(target_os = "macos"))]
fn main() {
    eprintln!("lamp-mac-guest-helper runs only inside a macOS guest");
    std::process::exit(2);
}

#[cfg(target_os = "macos")]
mod mac {
    use lamp_init::*;
    use std::collections::{HashMap, VecDeque};
    use std::fs::File;
    use std::io::{self, Read};
    use std::os::fd::FromRawFd;
    use std::os::unix::process::{CommandExt, ExitStatusExt};
    use std::process::{Command, Stdio};
    use std::sync::{Arc, Condvar, Mutex};
    use std::time::{Duration, SystemTime, UNIX_EPOCH};

    const AF_VSOCK: i32 = 40;
    const VMADDR_CID_ANY: u32 = 0xFFFF_FFFF;
    const KEPT_RESULTS: usize = 1024;
    const STATE_DIR: &str = "/var/db/lamp";

    #[repr(C, packed)]
    struct SockaddrVm {
        svm_len: u8,
        svm_family: u8,
        svm_reserved1: u16,
        svm_port: u32,
        svm_cid: u32,
    }

    #[derive(Default)]
    struct Identity {
        sandbox_id: String,
        generation: u64,
    }

    #[derive(Default)]
    struct Table {
        by_key: HashMap<String, Option<ExecDone>>,
        order: VecDeque<String>,
    }

    type Shared = Arc<Mutex<Identity>>;
    type Results = Arc<(Mutex<Table>, Condvar)>;

    fn now_ns() -> u64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0)
    }

    fn sysctl(name: &str) -> String {
        Command::new("/usr/sbin/sysctl")
            .args(["-n", name])
            .output()
            .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
            .unwrap_or_default()
    }

    /// Changes on every macOS boot; unchanged across suspend/resume.
    fn boot_id() -> String {
        sysctl("kern.bootsessionuuid")
    }

    fn listen() -> io::Result<i32> {
        // SAFETY: socket/bind/listen with a fully initialised sockaddr_vm.
        unsafe {
            let fd = libc::socket(AF_VSOCK, libc::SOCK_STREAM, 0);
            if fd < 0 {
                return Err(io::Error::last_os_error());
            }
            libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC);
            let addr = SockaddrVm {
                svm_len: std::mem::size_of::<SockaddrVm>() as u8,
                svm_family: AF_VSOCK as u8,
                svm_reserved1: 0,
                svm_port: GUEST_PORT,
                svm_cid: VMADDR_CID_ANY,
            };
            let len = std::mem::size_of::<SockaddrVm>() as libc::socklen_t;
            if libc::bind(fd, (&raw const addr).cast(), len) != 0 || libc::listen(fd, 64) != 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(fd)
        }
    }

    pub fn run() {
        let listener = loop {
            match listen() {
                Ok(l) => break l,
                Err(e) => {
                    // The vsock driver may still be coming up at boot.
                    eprintln!("lamp-mac-guest-helper: vsock listen: {e}");
                    std::thread::sleep(Duration::from_secs(1));
                }
            }
        };
        let _ = std::fs::create_dir_all(STATE_DIR);
        let state: Shared = Arc::default();
        let results: Results = Arc::default();
        loop {
            // SAFETY: accept on our listening socket.
            let fd = unsafe { libc::accept(listener, std::ptr::null_mut(), std::ptr::null_mut()) };
            if fd < 0 {
                std::thread::sleep(Duration::from_millis(1));
                continue;
            }
            // SAFETY: fresh descriptor from accept.
            unsafe { libc::fcntl(fd, libc::F_SETFD, libc::FD_CLOEXEC) };
            let stream = unsafe { File::from_raw_fd(fd) };
            let (state, results) = (state.clone(), results.clone());
            std::thread::spawn(move || serve(stream, state, results));
        }
    }

    fn error(code: &str, message: &str) -> GuestResponse {
        GuestResponse::Error {
            code: code.into(),
            message: message.into(),
        }
    }

    fn serve(mut stream: File, state: Shared, results: Results) {
        loop {
            let request: GuestRequest = match read_frame(&mut stream) {
                Ok(r) => r,
                Err(e) => {
                    if e.kind() == io::ErrorKind::InvalidData {
                        let _ =
                            write_frame(&mut stream, &error("INVALID_ARGUMENT", &e.to_string()));
                    }
                    return;
                }
            };
            let response = match request {
                GuestRequest::Ping(Empty {}) => {
                    let id = state.lock().unwrap();
                    GuestResponse::Pong(Status {
                        sandbox_id: id.sandbox_id.clone(),
                        generation: id.generation,
                        kernel_boot_id: boot_id(),
                    })
                }
                GuestRequest::Barrier(b) => match barrier(&b, &state) {
                    Ok(ack) => GuestResponse::BarrierAck(ack),
                    Err(e) => error("BARRIER_FAILED", &e),
                },
                GuestRequest::Exec(x) => exec(&x, &state, &results),
                GuestRequest::Wait(w) => wait(&w, &state, &results),
                GuestRequest::Freeze(_) | GuestRequest::Thaw(_) => error(
                    "UNSUPPORTED",
                    "native macOS guests have no task freezer; the VM itself is paused",
                ),
                GuestRequest::Shutdown(Empty {}) => GuestResponse::ShuttingDown,
            };
            let stop = matches!(response, GuestResponse::ShuttingDown);
            if write_frame(&mut stream, &response).is_err() {
                return;
            }
            if stop {
                let _ = Command::new("/sbin/shutdown").args(["-h", "now"]).status();
            }
        }
    }

    fn admit(sandbox_id: &str, generation: u64, state: &Shared) -> Result<(), Box<GuestResponse>> {
        let id = state.lock().unwrap();
        if id.generation == 0 || id.sandbox_id != sandbox_id || id.generation != generation {
            return Err(Box::new(error(
                "GENERATION_MISMATCH",
                "command refused: identity barrier for this sandbox/generation not applied",
            )));
        }
        Ok(())
    }

    fn barrier(b: &Barrier, state: &Shared) -> Result<BarrierAck, String> {
        validate_barrier(b)?;
        let mut id = state.lock().unwrap();
        // Host entropy is written into the kernel's random pool (root may
        // write /dev/random on macOS to add entropy).
        let entropy = hex_decode(&b.entropy_hex).ok_or("bad entropy")?;
        std::fs::write("/dev/random", &entropy).map_err(|e| format!("add entropy: {e}"))?;
        let tv = libc::timeval {
            tv_sec: (b.unix_time_ns / 1_000_000_000) as _,
            tv_usec: ((b.unix_time_ns % 1_000_000_000) / 1000) as _,
        };
        // SAFETY: valid timeval pointer; no timezone.
        if unsafe { libc::settimeofday(&tv, std::ptr::null()) } != 0 {
            return Err(format!("set clock: {}", io::Error::last_os_error()));
        }
        // SAFETY: hostname buffer valid for its length.
        if unsafe { libc::sethostname(b.hostname.as_ptr().cast(), b.hostname.len() as _) } != 0 {
            return Err(format!("set hostname: {}", io::Error::last_os_error()));
        }
        for key in ["HostName", "LocalHostName", "ComputerName"] {
            let _ = Command::new("/usr/sbin/scutil")
                .args(["--set", key, &b.hostname])
                .status();
        }
        let _ = std::fs::create_dir_all(STATE_DIR);
        std::fs::write(
            format!("{STATE_DIR}/machine-id"),
            format!("{}\n", b.machine_id),
        )
        .map_err(|e| format!("write machine-id: {e}"))?;
        std::fs::write(
            format!("{STATE_DIR}/identity.json"),
            format!(
                "{{\"sandbox_id\":\"{}\",\"generation\":{}}}\n",
                b.sandbox_id, b.generation
            ),
        )
        .map_err(|e| format!("write identity: {e}"))?;
        let mut sample = [0_u8; 16];
        // SAFETY: valid buffer of at most 256 bytes.
        if unsafe { libc::getentropy(sample.as_mut_ptr().cast(), sample.len()) } != 0 {
            return Err("getentropy failed".into());
        }
        id.sandbox_id = b.sandbox_id.clone();
        id.generation = b.generation;
        Ok(BarrierAck {
            sandbox_id: b.sandbox_id.clone(),
            generation: b.generation,
            machine_id: b.machine_id.clone(),
            hostname: b.hostname.clone(),
            rng_sample_hex: hex_encode(&sample),
            kernel_boot_id: boot_id(),
        })
    }

    fn key(sandbox_id: &str, generation: u64, exec_id: &str) -> String {
        format!("{sandbox_id}/{generation}/{exec_id}")
    }

    /// Reads `r` to its end into `into` (at most `limit` bytes kept; the
    /// flag says some were dropped).
    fn read_into(mut r: impl Read, limit: usize, into: &Mutex<(Vec<u8>, bool)>) {
        let mut buf = [0u8; 16 << 10];
        loop {
            match r.read(&mut buf) {
                Ok(0) | Err(_) => break,
                Ok(n) => {
                    let mut g = into.lock().unwrap();
                    let room = limit.saturating_sub(g.0.len());
                    if n > room {
                        g.1 = true;
                    }
                    g.0.extend_from_slice(&buf[..n.min(room)]);
                }
            }
        }
    }

    fn exec(x: &Exec, state: &Shared, results: &Results) -> GuestResponse {
        if let Err(refusal) = admit(&x.sandbox_id, x.generation, state) {
            return *refusal;
        }
        if x.exec_id.is_empty() || x.argv.is_empty() {
            return error("INVALID_ARGUMENT", "exec_id and argv are required");
        }
        if let Err(e) = validate_env(&x.env) {
            return error("INVALID_ARGUMENT", &e);
        }
        let k = key(&x.sandbox_id, x.generation, &x.exec_id);
        {
            let mut t = results.0.lock().unwrap();
            if t.by_key.contains_key(&k) {
                return error("INVALID_ARGUMENT", "exec_id already used");
            }
            t.by_key.insert(k.clone(), None);
            t.order.push_back(k.clone());
            while t.order.len() > KEPT_RESULTS {
                let finished = |o: &String| matches!(t.by_key.get(o), Some(Some(_)));
                let Some(pos) = t.order.iter().position(finished) else {
                    break;
                };
                let old = t.order.remove(pos).unwrap();
                t.by_key.remove(&old);
            }
        }
        let mut cmd = Command::new(&x.argv[0]);
        cmd.args(&x.argv[1..])
            .env_clear()
            .env("PATH", "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin")
            .env("HOME", "/var/root")
            .env("LANG", "en_US.UTF-8")
            .envs(&x.env)
            .current_dir(if x.cwd.is_empty() { "/" } else { &x.cwd })
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .process_group(0);
        let started_ns = now_ns();
        // std's spawn returns only once the program has been exec'd.
        let mut child = match cmd.spawn() {
            Ok(c) => c,
            Err(e) => {
                let mut t = results.0.lock().unwrap();
                t.by_key.remove(&k);
                t.order.retain(|o| o != &k);
                return error("EXEC_FAILED", &e.to_string());
            }
        };
        let pid = child.id() as i32;
        let limit = x.output_limit;
        let timeout = x.timeout_ms;
        let results = results.clone();
        std::thread::spawn(move || {
            let out = child.stdout.take().unwrap();
            let err = child.stderr.take().unwrap();
            // Readers fill shared buffers: a background job that inherited
            // a pipe must not hold the command's completion (see below).
            let (read_done, reads) = std::sync::mpsc::channel::<()>();
            let obuf = Arc::new(Mutex::new((Vec::new(), false)));
            let ebuf = Arc::new(Mutex::new((Vec::new(), false)));
            {
                let (b, tx) = (obuf.clone(), read_done.clone());
                std::thread::spawn(move || {
                    read_into(out, limit, &b);
                    let _ = tx.send(());
                });
                let (b, tx) = (ebuf.clone(), read_done);
                std::thread::spawn(move || {
                    read_into(err, limit, &b);
                    let _ = tx.send(());
                });
            }
            let timed_out = Arc::new(std::sync::atomic::AtomicBool::new(false));
            // Dropped once the command has exited: the timeout then never
            // fires, so background jobs it started keep running.
            let (exited, exit_seen) = std::sync::mpsc::channel::<()>();
            if timeout > 0 {
                let flag = timed_out.clone();
                std::thread::spawn(move || {
                    if let Err(std::sync::mpsc::RecvTimeoutError::Timeout) =
                        exit_seen.recv_timeout(Duration::from_millis(timeout))
                    {
                        flag.store(true, std::sync::atomic::Ordering::SeqCst);
                        // SAFETY: signals the command's own process group.
                        unsafe { libc::kill(-pid, libc::SIGKILL) };
                    }
                });
            }
            let status = child.wait();
            drop(exited);
            // The command has exited. Output still open after a short grace
            // belongs to a background job it left running: reply with what
            // was read so far (marked truncated) instead of waiting for it.
            let grace = std::time::Instant::now() + Duration::from_millis(50);
            let mut finished = 0;
            while finished < 2 {
                let left = grace.saturating_duration_since(std::time::Instant::now());
                match reads.recv_timeout(left) {
                    Ok(()) => finished += 1,
                    Err(_) => break,
                }
            }
            let still_open = finished < 2;
            let (out, t1) = obuf.lock().unwrap().clone();
            let (err, t2) = ebuf.lock().unwrap().clone();
            let (t1, t2) = (t1 || still_open, t2);
            let (code, sig) = match status {
                Ok(s) => (s.code().unwrap_or(-1), s.signal().unwrap_or(0)),
                Err(_) => (-1, 0),
            };
            let done = ExecDone {
                exit_code: code,
                signal: sig,
                stdout_b64: b64_encode(&out),
                stderr_b64: b64_encode(&err),
                truncated: t1 || t2,
                timed_out: timed_out.load(std::sync::atomic::Ordering::SeqCst),
                started_unix_ns: started_ns,
                finished_unix_ns: now_ns(),
            };
            results.0.lock().unwrap().by_key.insert(k, Some(done));
            results.1.notify_all();
        });
        GuestResponse::ExecStarted(ExecStarted {
            pid,
            started_unix_ns: started_ns,
        })
    }

    fn wait(w: &Wait, state: &Shared, results: &Results) -> GuestResponse {
        if let Err(refusal) = admit(&w.sandbox_id, w.generation, state) {
            return *refusal;
        }
        let k = key(&w.sandbox_id, w.generation, &w.exec_id);
        let deadline = std::time::Instant::now() + Duration::from_millis(w.wait_ms.min(60_000));
        let mut t = results.0.lock().unwrap();
        loop {
            match t.by_key.get(&k) {
                None => return error("NOT_FOUND", "no command with this exec_id"),
                Some(Some(done)) => return GuestResponse::ExecDone(done.clone()),
                Some(None) => {}
            }
            let now = std::time::Instant::now();
            if now >= deadline {
                return GuestResponse::ExecRunning;
            }
            t = results.1.wait_timeout(t, deadline - now).unwrap().0;
        }
    }
}
