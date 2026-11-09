//! Guest helper: serves the private host channel on vsock. It applies the
//! identity/entropy/clock barrier and refuses every command whose
//! sandbox/generation does not match the last barrier it applied.
#[cfg(target_os = "linux")]
fn main() {
    linux::run()
}

#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("lamp-guest-helper runs only inside a Linux guest");
    std::process::exit(2);
}

#[cfg(target_os = "linux")]
mod linux {
    use lamp_init::*;
    use std::fs::File;
    use std::io::{self, Read};
    use std::os::fd::{FromRawFd, OwnedFd};
    use std::os::unix::process::{CommandExt, ExitStatusExt};
    use std::process::{Command, Stdio};
    use std::sync::mpsc;
    use std::sync::{Arc, Mutex};
    use std::time::{Duration, SystemTime, UNIX_EPOCH};

    #[derive(Default)]
    struct Identity {
        sandbox_id: String,
        generation: u64,
    }

    type Shared = Arc<Mutex<Identity>>;

    fn now_ns() -> u64 {
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_nanos() as u64)
            .unwrap_or(0)
    }

    fn boot_id() -> String {
        std::fs::read_to_string("/proc/sys/kernel/random/boot_id")
            .unwrap_or_default()
            .trim()
            .to_string()
    }

    fn listen() -> io::Result<OwnedFd> {
        // SAFETY: socket/bind/listen with a fully initialised sockaddr_vm.
        unsafe {
            let fd = libc::socket(libc::AF_VSOCK, libc::SOCK_STREAM | libc::SOCK_CLOEXEC, 0);
            if fd < 0 {
                return Err(io::Error::last_os_error());
            }
            let owned = OwnedFd::from_raw_fd(fd);
            let mut addr: libc::sockaddr_vm = std::mem::zeroed();
            addr.svm_family = libc::AF_VSOCK as libc::sa_family_t;
            addr.svm_port = GUEST_PORT;
            addr.svm_cid = libc::VMADDR_CID_ANY;
            let len = std::mem::size_of::<libc::sockaddr_vm>() as libc::socklen_t;
            if libc::bind(fd, (&raw const addr).cast(), len) != 0 || libc::listen(fd, 64) != 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(owned)
        }
    }

    pub fn run() {
        let listener = match listen() {
            Ok(l) => l,
            Err(e) => {
                eprintln!("lamp-guest-helper: vsock listen: {e}");
                std::process::exit(1);
            }
        };
        let state: Shared = Arc::default();
        loop {
            // SAFETY: accept on our listening socket; no address requested.
            let fd = unsafe {
                libc::accept4(
                    std::os::fd::AsRawFd::as_raw_fd(&listener),
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    libc::SOCK_CLOEXEC,
                )
            };
            if fd < 0 {
                std::thread::sleep(Duration::from_millis(1));
                continue;
            }
            // SAFETY: accept4 returned a new descriptor we now own.
            let stream = unsafe { File::from_raw_fd(fd) };
            let state = state.clone();
            std::thread::spawn(move || serve(stream, state));
        }
    }

    fn serve(mut stream: File, state: Shared) {
        loop {
            let request: GuestRequest = match read_frame(&mut stream) {
                Ok(r) => r,
                Err(e) => {
                    if e.kind() == io::ErrorKind::InvalidData {
                        let _ = write_frame(&mut stream, &error("INVALID_ARGUMENT", &e.to_string()));
                    }
                    return;
                }
            };
            let response = handle(request, &state);
            let stop = matches!(response, GuestResponse::ShuttingDown);
            if write_frame(&mut stream, &response).is_err() {
                return;
            }
            if stop {
                power_off();
            }
        }
    }

    fn error(code: &str, message: &str) -> GuestResponse {
        GuestResponse::Error {
            code: code.into(),
            message: message.into(),
        }
    }

    fn handle(request: GuestRequest, state: &Shared) -> GuestResponse {
        match request {
            GuestRequest::Ping => {
                let id = state.lock().unwrap();
                GuestResponse::Pong(Status {
                    sandbox_id: id.sandbox_id.clone(),
                    generation: id.generation,
                    kernel_boot_id: boot_id(),
                })
            }
            GuestRequest::Barrier(b) => match barrier(&b, state) {
                Ok(ack) => GuestResponse::BarrierAck(ack),
                Err(e) => error("BARRIER_FAILED", &e),
            },
            GuestRequest::Exec(x) => {
                {
                    let id = state.lock().unwrap();
                    if id.generation == 0 || id.sandbox_id != x.sandbox_id || id.generation != x.generation {
                        return error(
                            "GENERATION_MISMATCH",
                            "command refused: identity barrier for this sandbox/generation not applied",
                        );
                    }
                }
                match exec(&x) {
                    Ok(done) => GuestResponse::ExecDone(done),
                    Err(e) => error("EXEC_FAILED", &e.to_string()),
                }
            }
            GuestRequest::Shutdown => GuestResponse::ShuttingDown,
        }
    }

    fn barrier(b: &Barrier, state: &Shared) -> Result<BarrierAck, String> {
        validate_barrier(b)?;
        // Hold the identity lock for the whole barrier: no command can be
        // admitted for either the old or the new identity while it runs.
        let mut id = state.lock().unwrap();
        let entropy = hex_decode(&b.entropy_hex).ok_or("bad entropy")?;
        add_entropy(&entropy).map_err(|e| format!("add entropy: {e}"))?;
        let ts = libc::timespec {
            tv_sec: (b.unix_time_ns / 1_000_000_000) as _,
            tv_nsec: (b.unix_time_ns % 1_000_000_000) as _,
        };
        // SAFETY: valid timespec pointer.
        if unsafe { libc::clock_settime(libc::CLOCK_REALTIME, &ts) } != 0 {
            return Err(format!("set clock: {}", io::Error::last_os_error()));
        }
        // SAFETY: hostname buffer valid for its length.
        if unsafe { libc::sethostname(b.hostname.as_ptr().cast(), b.hostname.len()) } != 0 {
            return Err(format!("set hostname: {}", io::Error::last_os_error()));
        }
        let _ = std::fs::write("/etc/hostname", format!("{}\n", b.hostname));
        std::fs::write("/etc/machine-id", format!("{}\n", b.machine_id))
            .map_err(|e| format!("write machine-id: {e}"))?;
        let identity = format!(
            "{{\"sandbox_id\":\"{}\",\"generation\":{}}}\n",
            b.sandbox_id, b.generation
        );
        let _ = std::fs::create_dir_all("/run/lamp");
        std::fs::write("/run/lamp/identity.json.tmp", identity)
            .and_then(|_| std::fs::rename("/run/lamp/identity.json.tmp", "/run/lamp/identity.json"))
            .map_err(|e| format!("write identity: {e}"))?;
        let mut sample = [0_u8; 16];
        // SAFETY: valid buffer.
        let n = unsafe { libc::getrandom(sample.as_mut_ptr().cast(), sample.len(), 0) };
        if n != sample.len() as isize {
            return Err("getrandom failed".into());
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

    /// Mixes host entropy into the guest kernel pool, credits it and forces a
    /// CRNG reseed so copies restored from one capture diverge immediately.
    fn add_entropy(entropy: &[u8]) -> io::Result<()> {
        const RNDADDENTROPY: u64 = 0x4008_5203;
        const RNDRESEEDCRNG: u64 = 0x5207;
        let file = std::fs::OpenOptions::new().write(true).open("/dev/urandom")?;
        let fd = std::os::fd::AsRawFd::as_raw_fd(&file);
        let mut buf = Vec::with_capacity(8 + entropy.len());
        buf.extend_from_slice(&((entropy.len() * 8) as i32).to_ne_bytes());
        buf.extend_from_slice(&(entropy.len() as i32).to_ne_bytes());
        buf.extend_from_slice(entropy);
        // SAFETY: buf is a valid rand_pool_info with buf_size trailing bytes.
        if unsafe { libc::ioctl(fd, RNDADDENTROPY as libc::Ioctl, buf.as_ptr()) } != 0 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: argumentless ioctl.
        if unsafe { libc::ioctl(fd, RNDRESEEDCRNG as libc::Ioctl) } != 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    }

    fn capped_reader(
        mut source: impl Read + Send + 'static,
        limit: usize,
    ) -> mpsc::Receiver<(Vec<u8>, bool)> {
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            let mut kept = Vec::new();
            let mut truncated = false;
            let mut buf = [0_u8; 16384];
            loop {
                match source.read(&mut buf) {
                    Ok(0) | Err(_) => break,
                    Ok(n) => {
                        let room = limit.saturating_sub(kept.len());
                        if n > room {
                            truncated = true;
                        }
                        kept.extend_from_slice(&buf[..n.min(room)]);
                    }
                }
            }
            let _ = tx.send((kept, truncated));
        });
        rx
    }

    fn exec(x: &Exec) -> io::Result<ExecDone> {
        if x.argv.is_empty() {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "argv is empty"));
        }
        let limit = x.output_limit.min(MAX_GUEST_FRAME / 4);
        let mut command = Command::new(&x.argv[0]);
        command
            .args(&x.argv[1..])
            .env_clear()
            .env("PATH", "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin")
            .env("HOME", "/root")
            .env("LANG", "C.UTF-8")
            .envs(&x.env)
            .current_dir(if x.cwd.is_empty() { "/root" } else { &x.cwd })
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .process_group(0);
        let started = now_ns();
        let mut child = command.spawn()?;
        let pid = child.id() as libc::pid_t;
        let out = capped_reader(child.stdout.take().unwrap(), limit);
        let err = capped_reader(child.stderr.take().unwrap(), limit);
        let (tx, rx) = mpsc::channel();
        std::thread::spawn(move || {
            let _ = tx.send(child.wait());
        });
        let timeout = Duration::from_millis(x.timeout_ms.max(1));
        let (status, timed_out) = match rx.recv_timeout(timeout) {
            Ok(status) => (status?, false),
            Err(_) => {
                // SAFETY: signals only the command's own process group.
                unsafe { libc::killpg(pid, libc::SIGKILL) };
                (rx.recv().map_err(io::Error::other)??, true)
            }
        };
        // Background jobs keep running. If one still holds a pipe, reply with
        // what was read so far instead of waiting for it.
        let grace = Duration::from_millis(50);
        let (stdout, t1) = out.recv_timeout(grace).unwrap_or((Vec::new(), true));
        let (stderr, t2) = err.recv_timeout(grace).unwrap_or((Vec::new(), true));
        Ok(ExecDone {
            exit_code: status.code().unwrap_or(-1),
            signal: status.signal().unwrap_or(0),
            stdout_b64: b64_encode(&stdout),
            stderr_b64: b64_encode(&stderr),
            truncated: t1 || t2,
            timed_out,
            started_unix_ns: started,
            finished_unix_ns: now_ns(),
        })
    }

    fn power_off() -> ! {
        // SAFETY: flush and power off; this is PID 1's guest.
        unsafe {
            libc::sync();
            libc::reboot(libc::RB_AUTOBOOT);
        }
        std::process::exit(0)
    }
}
