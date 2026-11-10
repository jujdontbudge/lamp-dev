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
    use std::io::{self, Read, Write};
    use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
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

    /// Completions by (sandbox, generation, exec ID); `None` while running.
    /// A capture copies this table into the child, but the child's identity
    /// differs, so it can never fetch the parent's results.
    #[derive(Default)]
    struct Table {
        by_key: std::collections::HashMap<String, Option<ExecDone>>,
        order: std::collections::VecDeque<String>,
    }

    type Results = Arc<(Mutex<Table>, std::sync::Condvar)>;

    /// Finished results kept for late or repeated waits.
    const KEPT_RESULTS: usize = 1024;

    fn key(sandbox_id: &str, generation: u64, exec_id: &str) -> String {
        format!("{sandbox_id}/{generation}/{exec_id}")
    }

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
        let results: Results = Arc::default();
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
            let results = results.clone();
            std::thread::spawn(move || serve(stream, state, results));
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
                GuestRequest::Exec(x) => exec(&x, &state, &results),
                GuestRequest::Wait(w) => wait(&w, &state, &results),
                other => handle(other, &state),
            };
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
            GuestRequest::Ping(Empty {}) => {
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
            // Served by `serve`, which owns the result table.
            GuestRequest::Exec(_) | GuestRequest::Wait(_) => {
                error("INVALID_ARGUMENT", "served with results")
            }
            GuestRequest::Freeze(Empty {}) => match freeze() {
                Ok(()) => GuestResponse::Frozen,
                Err(e) => error("FREEZE_FAILED", &e.to_string()),
            },
            GuestRequest::Thaw(Empty {}) => match thaw() {
                Ok(()) => GuestResponse::Thawed,
                Err(e) => error("THAW_FAILED", &e.to_string()),
            },
            GuestRequest::Shutdown(Empty {}) => GuestResponse::ShuttingDown,
        }
    }

    /// Starts a command and records it; its completion lands in `results`
    /// whether or not any host connection is still open.
    fn exec(x: &Exec, state: &Shared, results: &Results) -> GuestResponse {
        if let Err(refusal) = admit(&x.sandbox_id, x.generation, state) {
            return *refusal;
        }
        if x.exec_id.is_empty() {
            return error("INVALID_ARGUMENT", "exec_id is empty");
        }
        let k = key(&x.sandbox_id, x.generation, &x.exec_id);
        {
            // Reserve the ID first, so the same ID can never start twice.
            let mut t = results.0.lock().unwrap();
            if t.by_key.contains_key(&k) {
                return error("INVALID_ARGUMENT", "exec_id already used");
            }
            t.by_key.insert(k.clone(), None);
            t.order.push_back(k.clone());
            // Forget the oldest finished results; running ones always stay.
            while t.order.len() > KEPT_RESULTS {
                let finished = |o: &String| matches!(t.by_key.get(o), Some(Some(_)));
                let Some(pos) = t.order.iter().position(finished) else {
                    break;
                };
                let old = t.order.remove(pos).unwrap();
                t.by_key.remove(&old);
            }
        }
        let running = match start(x) {
            Ok(r) => r,
            Err(e) => {
                let mut t = results.0.lock().unwrap();
                t.by_key.remove(&k);
                t.order.retain(|o| o != &k);
                return error("EXEC_FAILED", &e.to_string());
            }
        };
        let started = running.started.clone();
        let results = results.clone();
        std::thread::spawn(move || {
            let done = finish(running).unwrap_or_else(|e| ExecDone {
                exit_code: -1,
                signal: 0,
                stdout_b64: String::new(),
                stderr_b64: b64_encode(format!("lamp-guest-helper: {e}").as_bytes()),
                truncated: false,
                timed_out: false,
                started_unix_ns: 0,
                finished_unix_ns: now_ns(),
            });
            results.0.lock().unwrap().by_key.insert(k, Some(done));
            results.1.notify_all();
        });
        GuestResponse::ExecStarted(started)
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

    fn cgroup_file(name: &str) -> String {
        format!("{TASKS_CGROUP}/{name}")
    }

    const CGROUP_ROOT: &str = "/sys/fs/cgroup";
    /// Frozen leaf that holds processes found in the root cgroup, which
    /// itself cannot be frozen.
    const HOLD_CGROUP: &str = "/sys/fs/cgroup/lamp-hold";

    /// What `freeze` changed, so `thaw` undoes exactly that. It lives in the
    /// helper's memory, so a copy restored from a capture inherits it and its
    /// barrier thaws the same set.
    #[derive(Default)]
    struct FrozenSet {
        froze: Vec<std::path::PathBuf>,
        held: Vec<String>,
    }

    static FROZEN: Mutex<Option<FrozenSet>> = Mutex::new(None);

    fn is_kernel_thread(pid: &str) -> bool {
        const PF_KTHREAD: u64 = 0x0020_0000;
        let Ok(stat) = std::fs::read_to_string(format!("/proc/{pid}/stat")) else {
            return false;
        };
        let Some(close) = stat.rfind(')') else {
            return false;
        };
        stat[close + 1..]
            .split_whitespace()
            .nth(6)
            .and_then(|f| f.parse::<u64>().ok())
            .is_some_and(|flags| flags & PF_KTHREAD != 0)
    }

    /// Processes allowed to stay running in the root cgroup.
    fn exempt(pid: &str) -> bool {
        pid == "1" || pid == std::process::id().to_string() || is_kernel_thread(pid)
    }

    fn frozen(dir: &std::path::Path) -> bool {
        std::fs::read_to_string(dir.join("cgroup.events"))
            .is_ok_and(|e| e.lines().any(|l| l.trim() == "frozen 1"))
    }

    fn top_level_cgroups() -> io::Result<Vec<std::path::PathBuf>> {
        let mut dirs = Vec::new();
        for entry in std::fs::read_dir(CGROUP_ROOT)? {
            let entry = entry?;
            if entry.file_type()?.is_dir() {
                dirs.push(entry.path());
            }
        }
        Ok(dirs)
    }

    /// Freezes every guest process except init and this helper, wherever it
    /// put itself. Guest root may leave any cgroup (the hierarchy has no
    /// namespace or delegation boundary), so the freeze is not tied to one:
    /// every top-level cgroup is frozen (descendants, including ones a task
    /// creates later, freeze with it) and every process in the root cgroup,
    /// which cannot be frozen, is moved into a frozen hold cgroup. This
    /// repeats until a pass changes nothing and the kernel reports every
    /// cgroup frozen; from then on no task runs, so none can move.
    fn freeze() -> io::Result<()> {
        let mut guard = FROZEN.lock().unwrap();
        let set = guard.get_or_insert_with(FrozenSet::default);
        let hold = std::path::Path::new(HOLD_CGROUP);
        let _ = std::fs::create_dir(hold);
        std::fs::write(hold.join("cgroup.freeze"), "1")?;
        let deadline = std::time::Instant::now() + Duration::from_secs(5);
        loop {
            let mut changed = false;
            let mut settled = true;
            for dir in top_level_cgroups()? {
                if dir != hold {
                    let knob = dir.join("cgroup.freeze");
                    if std::fs::read_to_string(&knob).is_ok_and(|v| v.trim() == "0") {
                        std::fs::write(&knob, "1")?;
                        set.froze.push(dir.clone());
                        changed = true;
                    }
                }
                settled &= frozen(&dir);
            }
            let root_procs = std::fs::read_to_string(format!("{CGROUP_ROOT}/cgroup.procs"))?;
            for pid in root_procs.split_whitespace().filter(|p| !exempt(p)) {
                // A process that exits meanwhile cannot be moved; that is fine.
                if std::fs::write(hold.join("cgroup.procs"), pid).is_ok() {
                    set.held.push(pid.to_string());
                }
                changed = true;
            }
            if !changed && settled {
                return Ok(());
            }
            if std::time::Instant::now() > deadline {
                drop(guard);
                let _ = thaw();
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "guest processes did not freeze",
                ));
            }
            std::thread::sleep(Duration::from_micros(100));
        }
    }

    /// Undoes `freeze` (idempotent): unfreezes what it froze and returns held
    /// processes to the root cgroup.
    fn thaw() -> io::Result<()> {
        let set = FROZEN.lock().unwrap().take().unwrap_or_default();
        let mut first_error = None;
        for dir in set.froze {
            match std::fs::write(dir.join("cgroup.freeze"), "0") {
                Ok(()) => {}
                // The guest removed that cgroup meanwhile.
                Err(e) if e.kind() == io::ErrorKind::NotFound => {}
                Err(e) => {
                    first_error.get_or_insert(e);
                }
            }
        }
        let hold = std::path::Path::new(HOLD_CGROUP);
        if hold.exists() {
            if let Err(e) = std::fs::write(hold.join("cgroup.freeze"), "0") {
                first_error.get_or_insert(e);
            }
            for pid in set.held {
                let _ = std::fs::write(format!("{CGROUP_ROOT}/cgroup.procs"), &pid);
            }
        }
        // The tasks cgroup is ours; it never stays frozen.
        if let Err(e) = std::fs::write(cgroup_file("cgroup.freeze"), "0")
            && e.kind() != io::ErrorKind::NotFound
        {
            first_error.get_or_insert(e);
        }
        first_error.map_or(Ok(()), Err)
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
        // Only now may task processes inherited from a capture run again:
        // they see the new identity, clock and reseeded CRNG first.
        thaw().map_err(|e| format!("thaw tasks: {e}"))?;
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
        let file = std::fs::OpenOptions::new()
            .write(true)
            .open("/dev/urandom")?;
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

    struct Running {
        started: ExecStarted,
        pid: libc::pid_t,
        timeout_ms: u64,
        out: mpsc::Receiver<(Vec<u8>, bool)>,
        err: mpsc::Receiver<(Vec<u8>, bool)>,
        status: mpsc::Receiver<io::Result<std::process::ExitStatus>>,
    }

    /// A close-on-exec pipe whose descriptors are both >= 10, so that moving
    /// them onto fds 3 and 4 in the child cannot clobber one another.
    fn high_pipe() -> io::Result<(OwnedFd, OwnedFd)> {
        let mut fds = [0; 2];
        // SAFETY: pipe2 fills two new descriptors we then own.
        if unsafe { libc::pipe2(fds.as_mut_ptr(), libc::O_CLOEXEC) } != 0 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: both descriptors were just created and are owned here.
        let (r, w) = unsafe { (OwnedFd::from_raw_fd(fds[0]), OwnedFd::from_raw_fd(fds[1])) };
        let high = |fd: OwnedFd| -> io::Result<OwnedFd> {
            // SAFETY: F_DUPFD_CLOEXEC returns a new descriptor we own.
            let n = unsafe { libc::fcntl(fd.as_raw_fd(), libc::F_DUPFD_CLOEXEC, 10) };
            if n < 0 {
                return Err(io::Error::last_os_error());
            }
            Ok(unsafe { OwnedFd::from_raw_fd(n) })
        };
        Ok((high(r)?, high(w)?))
    }

    /// Spawns the command inside the tasks cgroup. `spawn` returns only after
    /// the program was exec'd (std's close-on-exec status pipe), which is the
    /// witnessed start reported to the host.
    fn start(x: &Exec) -> io::Result<Running> {
        if x.argv.is_empty() {
            return Err(io::Error::new(io::ErrorKind::InvalidInput, "argv is empty"));
        }
        let limit = x.output_limit.min(MAX_GUEST_FRAME / 4);
        let procs = std::ffi::CString::new(cgroup_file("cgroup.procs")).unwrap();
        // Audited commands run only under the protected collector; if it is
        // missing or fails, nothing runs (fail closed).
        // The collector reports the command's own exit on fd 3 (`status:3`)
        // and keeps tracing background jobs after it; the exec completes on
        // that report, so background jobs outlive it as without a collector.
        // It reads the task's environment, as JSON, from fd 4 (`env:4`): a
        // pipe, so no argv/env string limit (128 KiB each) applies to it.
        lamp_init::validate_env(&x.env)
            .map_err(|e| io::Error::new(io::ErrorKind::InvalidInput, e))?;
        let mut status_pipe: Option<(OwnedFd, OwnedFd)> = None;
        let mut env_pipe: Option<(OwnedFd, OwnedFd)> = None;
        let argv: Vec<String> = match &x.audit {
            Some(a) => {
                status_pipe = Some(high_pipe()?);
                env_pipe = Some(high_pipe()?);
                let mut v = vec![
                    GUARD_BIN.to_string(),
                    "collect".into(),
                    format!("vsock:{}", a.port),
                    a.task_uid.to_string(),
                    "status:3".into(),
                    "env:4".into(),
                    "--".into(),
                ];
                v.extend(x.argv.iter().cloned());
                v
            }
            None => x.argv.clone(),
        };
        let mut command = Command::new(&argv[0]);
        command.args(&argv[1..]).env_clear();
        if x.audit.is_none() {
            command
                .env(
                    "PATH",
                    "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
                )
                .env("HOME", "/root")
                .env("LANG", "C.UTF-8")
                .envs(&x.env);
        }
        command
            .current_dir(if x.cwd.is_empty() { "/root" } else { &x.cwd })
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .process_group(0);
        let status_w = status_pipe.as_ref().map(|(_, w)| w.as_raw_fd());
        let env_r = env_pipe.as_ref().map(|(r, _)| r.as_raw_fd());
        // SAFETY: open/write/close/dup2/fcntl only, between fork and exec. A
        // command that cannot join the tasks cgroup is never exec'd (fail
        // closed).
        unsafe {
            command.pre_exec(move || {
                let fd = libc::open(procs.as_ptr(), libc::O_WRONLY | libc::O_CLOEXEC);
                if fd < 0 {
                    return Err(io::Error::last_os_error());
                }
                let ok = libc::write(fd, c"0".as_ptr().cast(), 1) == 1;
                let e = io::Error::last_os_error();
                libc::close(fd);
                if !ok {
                    return Err(e);
                }
                // fds 3 and 4 without close-on-exec, for the collector only.
                // Both sources are >= 10 (high_pipe), so neither dup2
                // overwrites the other's source.
                for (source, target) in [(status_w, 3), (env_r, 4)] {
                    if let Some(fd) = source
                        && libc::dup2(fd, target) < 0
                    {
                        return Err(io::Error::last_os_error());
                    }
                }
                Ok(())
            });
        }
        let started_unix_ns = now_ns();
        let mut child = command.spawn()?;
        if let Some((r, w)) = env_pipe {
            drop(r);
            let json = serde_json::to_vec(&x.env).map_err(io::Error::other)?;
            // A writer thread: the JSON may exceed the pipe buffer. If the
            // collector fails before reading, the write fails and nothing ran.
            std::thread::spawn(move || {
                let _ = std::fs::File::from(w).write_all(&json);
            });
        }
        let pid = child.id() as libc::pid_t;
        let out = capped_reader(child.stdout.take().unwrap(), limit);
        let err = capped_reader(child.stderr.take().unwrap(), limit);
        let (tx, status) = mpsc::channel();
        if let Some((r, w)) = status_pipe {
            drop(w);
            let (collector_tx, collector_status) = mpsc::channel();
            // Reap the collector independently. Its status is a fallback only
            // when no task status was reported, never a competing result.
            std::thread::spawn(move || {
                let _ = collector_tx.send(child.wait());
            });
            std::thread::spawn(move || {
                let result = reported_task_status(std::fs::File::from(r), collector_status);
                let _ = tx.send(result);
            });
        } else {
            std::thread::spawn(move || {
                let _ = tx.send(child.wait());
            });
        }
        Ok(Running {
            started: ExecStarted {
                pid,
                started_unix_ns,
            },
            pid,
            timeout_ms: x.timeout_ms,
            out,
            err,
            status,
        })
    }

    fn reported_task_status(
        mut report: impl Read,
        collector: mpsc::Receiver<io::Result<std::process::ExitStatus>>,
    ) -> io::Result<std::process::ExitStatus> {
        let mut line = String::new();
        report.read_to_string(&mut line)?;
        if !line.is_empty() {
            let raw = line.trim().parse::<i32>().map_err(|_| {
                io::Error::new(io::ErrorKind::InvalidData, "invalid task status report")
            })?;
            if raw < 0 || !(libc::WIFEXITED(raw) || libc::WIFSIGNALED(raw)) {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    "invalid task wait status",
                ));
            }
            return Ok(std::process::ExitStatus::from_raw(raw));
        }
        collector.recv().map_err(io::Error::other)?
    }

    fn finish(r: Running) -> io::Result<ExecDone> {
        let (status, timed_out) = if r.timeout_ms == 0 {
            (r.status.recv().map_err(io::Error::other)??, false)
        } else {
            match r.status.recv_timeout(Duration::from_millis(r.timeout_ms)) {
                Ok(status) => (status?, false),
                Err(_) => {
                    // SAFETY: signals only the command's own process group.
                    unsafe { libc::killpg(r.pid, libc::SIGKILL) };
                    (r.status.recv().map_err(io::Error::other)??, true)
                }
            }
        };
        // Background jobs keep running. If one still holds a pipe, reply with
        // what was read so far instead of waiting for it.
        let grace = Duration::from_millis(50);
        let (stdout, t1) = r.out.recv_timeout(grace).unwrap_or((Vec::new(), true));
        let (stderr, t2) = r.err.recv_timeout(grace).unwrap_or((Vec::new(), true));
        Ok(ExecDone {
            exit_code: status.code().unwrap_or(-1),
            signal: status.signal().unwrap_or(0),
            stdout_b64: b64_encode(&stdout),
            stderr_b64: b64_encode(&stderr),
            truncated: t1 || t2,
            timed_out,
            started_unix_ns: r.started.started_unix_ns,
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
    #[cfg(test)]
    mod status_tests {
        use super::*;

        #[test]
        fn malformed_report_cannot_fall_back_to_collector_success() {
            for report in ["broken", "-1", "4991"] {
                let (tx, rx) = mpsc::channel();
                tx.send(Ok(std::process::ExitStatus::from_raw(0))).unwrap();
                assert_eq!(
                    reported_task_status(report.as_bytes(), rx)
                        .unwrap_err()
                        .kind(),
                    io::ErrorKind::InvalidData
                );
            }
        }

        #[test]
        fn task_report_wins_even_when_collector_exit_arrives_first() {
            for (report, collector_code, expected) in
                [("0\n", 70, 0), ("10752\n", 0, 42), ("", 70, 70)]
            {
                let (tx, rx) = mpsc::channel();
                tx.send(Ok(std::process::ExitStatus::from_raw(collector_code << 8)))
                    .unwrap();
                assert_eq!(
                    reported_task_status(report.as_bytes(), rx).unwrap().code(),
                    Some(expected)
                );
            }
        }
    }
}
