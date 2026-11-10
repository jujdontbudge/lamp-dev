//! One confined VZ worker process per sandbox.
//!
//! The worker owns exactly one machine. It confines itself first, then
//! builds the machine from its spec and serves two private Unix sockets in
//! the sandbox directory:
//! - `api.sock`: one JSON request per line (`spec::Api`), one reply line;
//! - `vsock.sock`: Firecracker-compatible hybrid vsock (`CONNECT <port>\n`,
//!   `OK <port>\n`, then raw bytes), so the host guest channel is the same
//!   code on Linux and Mac hosts.
use crate::confine;
use crate::macos::b64_decode;
use crate::spec::*;
use crate::vz::{self, Disk, DiskSync, LinuxSpec, MacSpec, Vm};
use std::io::{BufRead, BufReader, Read, Write};
use std::os::fd::{AsRawFd, FromRawFd};
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::Instant;

pub struct Options {
    pub spec: PathBuf,
    pub restore: Option<PathBuf>,
    /// Leave a restored machine paused (the caller resumes after its
    /// identity work); a booted machine always starts.
    pub stay_paused: bool,
    pub confine: bool,
}

fn grants(spec: &WorkerSpec, restore: Option<&Path>) -> confine::Grants {
    let mut g = confine::Grants {
        macos_guest: matches!(spec.machine, Machine::Macos { .. }),
        read_subpaths: vec![],
        read_literals: spec.read_paths.clone(),
        write_subpaths: std::iter::once(spec.dir.clone())
            .chain(spec.write_dirs.iter().cloned())
            .collect(),
        write_literals: spec.write_paths.clone(),
    };
    for d in &spec.disks {
        if d.read_only {
            g.read_literals.push(d.path.clone());
        } else {
            g.write_literals.push(d.path.clone());
        }
    }
    match &spec.machine {
        Machine::Linux { kernel, initrd, .. } => {
            g.read_literals.push(kernel.clone());
            g.read_literals.extend(initrd.clone());
        }
        Machine::Macos {
            auxiliary_storage, ..
        } => g.write_literals.push(auxiliary_storage.clone()),
    }
    g.read_literals.extend(restore.map(Path::to_path_buf));
    g
}

/// `confstr(_CS_DARWIN_USER_CACHE_DIR)` for the calling account, creating
/// it when absent.
fn darwin_user_cache_dir() -> Option<PathBuf> {
    let mut buf = vec![0u8; 1024];
    // SAFETY: the buffer is valid for its length; confstr writes a
    // NUL-terminated string of at most that length.
    let n = unsafe {
        libc::confstr(
            libc::_CS_DARWIN_USER_CACHE_DIR,
            buf.as_mut_ptr().cast(),
            buf.len(),
        )
    };
    if n == 0 || n > buf.len() {
        return None;
    }
    buf.truncate(n - 1);
    Some(PathBuf::from(String::from_utf8(buf).ok()?))
}

pub fn grants_for(spec: &WorkerSpec, restore: Option<&Path>) -> confine::Grants {
    grants(spec, restore)
}

fn disks(spec: &WorkerSpec) -> Vec<Disk> {
    spec.disks
        .iter()
        .map(|d| Disk {
            path: d.path.clone(),
            read_only: d.read_only,
            sync: match d.sync {
                Sync::Full => DiskSync::Full,
                Sync::Fsync => DiskSync::Fsync,
                Sync::None => DiskSync::None,
            },
        })
        .collect()
}

pub fn config(
    spec: &WorkerSpec,
) -> Result<objc2::rc::Retained<objc2_virtualization::VZVirtualMachineConfiguration>, String> {
    let console = Some(spec.dir.join("console.log"));
    match &spec.machine {
        Machine::Linux {
            kernel,
            initrd,
            cmdline,
            machine_identifier_b64,
        } => vz::linux_config(&LinuxSpec {
            kernel: kernel.clone(),
            initrd: initrd.clone(),
            cmdline: cmdline.clone(),
            cpus: spec.cpus,
            memory_bytes: spec.memory_mib << 20,
            disks: disks(spec),
            console_log: console,
            machine_identifier: Some(b64_decode(machine_identifier_b64)?),
            nat_network: spec.nat_network,
        }),
        Machine::Macos {
            hardware_model_b64,
            machine_identifier_b64,
            auxiliary_storage,
        } => vz::mac_config(&MacSpec {
            hardware_model: b64_decode(hardware_model_b64)?,
            machine_identifier: b64_decode(machine_identifier_b64)?,
            auxiliary_storage: auxiliary_storage.clone(),
            cpus: spec.cpus,
            memory_bytes: spec.memory_mib << 20,
            disks: disks(spec),
            console_log: console,
            nat_network: spec.nat_network,
            display: (1024, 768),
        }),
    }
}

fn reply(
    ok: bool,
    state: &str,
    error: String,
    began: Instant,
    detail: serde_json::Value,
) -> ApiReply {
    ApiReply {
        ok,
        state: state.into(),
        error,
        elapsed_us: began.elapsed().as_micros() as u64,
        detail,
    }
}

fn probe(checks: &[ProbeCheck]) -> serde_json::Value {
    let mut out = Vec::new();
    for c in checks {
        let r: Result<String, String> = match c {
            ProbeCheck::ReadFile { path } => std::fs::read(path)
                .map(|b| format!("read {} bytes", b.len()))
                .map_err(|e| e.to_string()),
            ProbeCheck::WriteFile { path } => std::fs::OpenOptions::new()
                .create(true)
                .append(true)
                .open(path)
                .map(|_| "opened for write".to_string())
                .map_err(|e| e.to_string()),
            ProbeCheck::ListDir { path } => std::fs::read_dir(path)
                .map(|d| format!("{} entries", d.count()))
                .map_err(|e| e.to_string()),
            ProbeCheck::Connect { host, port } => std::net::TcpStream::connect_timeout(
                &format!("{host}:{port}")
                    .parse()
                    .unwrap_or_else(|_| std::net::SocketAddr::from(([127, 0, 0, 1], *port))),
                std::time::Duration::from_secs(3),
            )
            .map(|_| "connected".to_string())
            .map_err(|e| e.to_string()),
            ProbeCheck::MachLookup { name } => mach_lookup(name),
            ProbeCheck::Exec { path } => std::process::Command::new(path)
                .output()
                .map(|o| format!("ran, status {}", o.status))
                .map_err(|e| e.to_string()),
            ProbeCheck::Keychain {} => keychain_probe(),
        };
        out.push(serde_json::json!({
            "check": c,
            "allowed": r.is_ok(),
            "result": match r { Ok(s) | Err(s) => s },
        }));
    }
    serde_json::Value::Array(out)
}

unsafe extern "C" {
    fn bootstrap_look_up(bp: u32, name: *const libc::c_char, sp: *mut u32) -> libc::c_int;
    static bootstrap_port: u32;
}

fn mach_lookup(name: &str) -> Result<String, String> {
    let c = std::ffi::CString::new(name).map_err(|e| e.to_string())?;
    let mut port = 0u32;
    // SAFETY: valid name and out pointer; bootstrap_port is process global.
    let kr = unsafe { bootstrap_look_up(bootstrap_port, c.as_ptr(), &mut port) };
    if kr == 0 {
        Ok(format!("looked up port {port}"))
    } else {
        Err(format!("bootstrap_look_up kern_return {kr}"))
    }
}

fn keychain_probe() -> Result<String, String> {
    // The login keychain file itself.
    let home = std::env::var("HOME").unwrap_or_default();
    let p = Path::new(&home).join("Library/Keychains/login.keychain-db");
    std::fs::read(&p)
        .map(|b| format!("read {} bytes of {}", b.len(), p.display()))
        .map_err(|e| format!("{}: {e}", p.display()))
}

fn splice(mut from: UnixStream, to_fd: i32) {
    let mut buf = vec![0u8; 256 << 10];
    loop {
        let n = match from.read(&mut buf) {
            Ok(0) | Err(_) => break,
            Ok(n) => n,
        };
        let mut off = 0;
        while off < n {
            // SAFETY: valid buffer slice and descriptor.
            let w = unsafe { libc::write(to_fd, buf[off..n].as_ptr().cast(), n - off) };
            if w <= 0 {
                let e = std::io::Error::last_os_error();
                if e.kind() == std::io::ErrorKind::Interrupted {
                    continue;
                }
                // SAFETY: shutdown on a valid descriptor.
                unsafe { libc::shutdown(from.as_raw_fd(), libc::SHUT_RD) };
                return;
            }
            off += w as usize;
        }
    }
    // SAFETY: half-close the peer's write side.
    unsafe { libc::shutdown(to_fd, libc::SHUT_WR) };
}

fn vsock_client(vm: Arc<Vm>, stream: UnixStream) {
    let mut reader = BufReader::new(match stream.try_clone() {
        Ok(s) => s,
        Err(_) => return,
    });
    let mut line = String::new();
    if reader.read_line(&mut line).is_err() {
        return;
    }
    let mut out = stream;
    let port = match line
        .trim()
        .strip_prefix("CONNECT ")
        .and_then(|p| p.parse::<u32>().ok())
    {
        Some(p) => p,
        None => {
            let _ = out.write_all(b"ERR bad handshake\n");
            return;
        }
    };
    let conn = match vm.connect(port) {
        Ok(c) => c,
        Err(e) => {
            let _ = out.write_all(format!("ERR {e}\n").as_bytes());
            return;
        }
    };
    if out.write_all(format!("OK {port}\n").as_bytes()).is_err() {
        return;
    }
    // Bytes already buffered after the handshake line go first.
    let pending = reader.buffer().to_vec();
    let gfd = conn.fd.as_raw_fd();
    if !pending.is_empty() {
        // SAFETY: valid buffer and descriptor.
        unsafe { libc::write(gfd, pending.as_ptr().cast(), pending.len()) };
    }
    // SAFETY: dup gives the reverse direction its own descriptor.
    let gdup = unsafe { libc::dup(gfd) };
    if gdup < 0 {
        return;
    }
    // SAFETY: fresh descriptor; the stream owns and closes it.
    let guest_reader = unsafe { UnixStream::from_raw_fd(gdup) };
    let host_fd = out.as_raw_fd();
    let up = std::thread::spawn(move || splice(guest_reader, host_fd));
    splice(reader.into_inner(), gfd);
    let _ = up.join();
    drop(conn);
    drop(out);
}

pub fn run(opts: Options) -> Result<(), String> {
    let t0 = Instant::now();
    let text = std::fs::read_to_string(&opts.spec).map_err(|e| format!("spec: {e}"))?;
    let spec: WorkerSpec = serde_json::from_str(&text).map_err(|e| format!("spec: {e}"))?;
    let mut g = grants(&spec, opts.restore.as_deref());
    if matches!(spec.machine, Machine::Macos { .. }) {
        // VZ needs this account's Darwin cache directory for a macOS guest.
        // It is resolved here, before confinement (the lookup goes through
        // dirhelper/opendirectoryd, which the profile does not admit, and
        // libSystem keeps the answer), and only that directory is granted.
        g.write_subpaths.extend(darwin_user_cache_dir());
    }
    if opts.confine {
        confine::apply(&confine::profile(&g)).map_err(|e| format!("confinement: {e}"))?;
    }
    let confined_us = t0.elapsed().as_micros();
    if !vz::supported() {
        return Err("Virtualization.framework is not available to this process".into());
    }
    let cfg = config(&spec)?;
    let vm = Arc::new(Vm::new(
        &format!("si.eilmer.lamp.vm.{}", spec.sandbox_id),
        cfg,
    ));
    let t_vm = Instant::now();
    match &opts.restore {
        Some(state) => {
            vm.restore(state)?;
            if !opts.stay_paused {
                vm.resume()?;
            }
        }
        None => vm.start()?,
    }
    let started_us = t_vm.elapsed().as_micros();
    let api_path = spec.dir.join("api.sock");
    let vsock_path = spec.dir.join("vsock.sock");
    let _ = std::fs::remove_file(&api_path);
    let _ = std::fs::remove_file(&vsock_path);
    let api = UnixListener::bind(&api_path).map_err(|e| format!("bind api: {e}"))?;
    let vsock = UnixListener::bind(&vsock_path).map_err(|e| format!("bind vsock: {e}"))?;
    {
        let vm = vm.clone();
        std::thread::spawn(move || {
            for s in vsock.incoming().flatten() {
                let vm = vm.clone();
                std::thread::spawn(move || vsock_client(vm, s));
            }
        });
    }
    println!(
        "{}",
        serde_json::json!({
            "ready": true,
            "pid": std::process::id(),
            "state": vm.state().name(),
            "confined": opts.confine,
            "confine_us": confined_us,
            "vm_start_us": started_us,
            "restored": opts.restore.is_some(),
        })
    );
    let _ = std::io::stdout().flush();
    // The machine ends the worker when it stops on its own (guest power off
    // or framework error).
    {
        let vm = vm.clone();
        std::thread::spawn(move || {
            loop {
                std::thread::sleep(std::time::Duration::from_millis(250));
                match vm.state() {
                    vz::State::Stopped => {
                        eprintln!("lamp-vz worker: machine stopped");
                        std::process::exit(0);
                    }
                    vz::State::Error => {
                        eprintln!("lamp-vz worker: machine in error state");
                        std::process::exit(3);
                    }
                    _ => {}
                }
            }
        });
    }
    let lock = Arc::new(Mutex::new(()));
    for s in api.incoming().flatten() {
        let vm = vm.clone();
        let lock = lock.clone();
        std::thread::spawn(move || {
            let mut reader = BufReader::new(match s.try_clone() {
                Ok(s) => s,
                Err(_) => return,
            });
            let mut out = s;
            let mut line = String::new();
            while {
                line.clear();
                matches!(reader.read_line(&mut line), Ok(n) if n > 0)
            } {
                let began = Instant::now();
                let r = match serde_json::from_str::<Api>(&line) {
                    Err(e) => reply(
                        false,
                        "",
                        format!("bad request: {e}"),
                        began,
                        serde_json::Value::Null,
                    ),
                    Ok(req) => {
                        let _g = lock.lock().unwrap();
                        let res = match &req {
                            Api::State {} => Ok(serde_json::Value::Null),
                            Api::Pause {} => vm.pause().map(|_| serde_json::Value::Null),
                            Api::Resume {} => vm.resume().map(|_| serde_json::Value::Null),
                            Api::Save { path } => vm.save(path).map(|_| {
                                serde_json::json!({"bytes": std::fs::metadata(path).map(|m| m.len()).unwrap_or(0)})
                            }),
                            Api::Stop {} => vm.stop().map(|_| serde_json::Value::Null),
                            Api::RequestStop {} => vm.request_stop().map(|_| serde_json::Value::Null),
                            Api::Probe { checks } => Ok(probe(checks)),
                            Api::Exit {} => {
                                if matches!(vm.state(), vz::State::Running | vz::State::Paused) {
                                    let _ = vm.stop();
                                }
                                let _ = out.write_all(b"{\"ok\":true,\"state\":\"exited\"}\n");
                                std::process::exit(0);
                            }
                        };
                        let state = vm.state().name();
                        match res {
                            Ok(d) => reply(true, state, String::new(), began, d),
                            Err(e) => reply(false, state, e, began, serde_json::Value::Null),
                        }
                    }
                };
                let mut text = serde_json::to_string(&r).unwrap();
                text.push('\n');
                if out.write_all(text.as_bytes()).is_err() {
                    break;
                }
            }
        });
    }
    Ok(())
}
