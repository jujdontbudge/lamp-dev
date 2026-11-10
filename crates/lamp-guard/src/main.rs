#[cfg(target_os = "linux")]
fn main() {
    use lamp_guard::{Config, audit, process, runtime};
    use std::{env, fs, io};
    fn run() -> io::Result<i32> {
        let args: Vec<String> = env::args().collect();
        let usage = "usage: lamp-guard policy CONFIG | run CONFIG | receive CONFIG FD | proxy CONFIG SPOOL | collect FD|vsock:PORT UID [status:FD] -- ARGV...";
        let config = |p: &str| -> io::Result<Config> {
            let c: Config = serde_json::from_slice(&fs::read(p)?)?;
            c.validate()?;
            Ok(c)
        };
        let fd = |s: &str| s.parse::<i32>().map_err(io::Error::other);
        match args.get(1).map(String::as_str) {
            Some("policy") if args.len() == 3 => {
                print!("{}", config(&args[2])?.nft()?);
                Ok(0)
            }
            Some("run") if args.len() == 3 => {
                runtime::run(config(&args[2])?)?;
                Ok(0)
            }
            Some("receive") if args.len() == 4 => {
                let c = config(&args[2])?;
                let mut spool = audit::Spool::create(&c.spool, c.spool_bytes, c.attribution)?;
                let socket = unsafe { audit::stream(fd(&args[3])?)? };
                use std::os::fd::AsRawFd;
                lamp_guard::confinement::enter(
                    c.guard_uid,
                    false,
                    &[spool.as_raw_fd(), socket.as_raw_fd()],
                )?;
                audit::receive(socket, &mut spool)?;
                Ok(0)
            }
            Some("proxy") if args.len() == 4 => {
                // Runs inside the guard namespace: bind the endpoint and
                // create the spool as root, then confine to the guard UID.
                let c = config(&args[2])?;
                let endpoint = c
                    .proxy
                    .ok_or_else(|| io::Error::other("policy has no proxy endpoint"))?;
                let listener = std::net::TcpListener::bind((endpoint.address, endpoint.port))?;
                let mut spool =
                    audit::Spool::create(&args[3], c.spool_bytes, c.attribution.clone())?;
                use std::os::fd::AsRawFd;
                lamp_guard::confinement::enter_with(
                    c.guard_uid,
                    false,
                    &[spool.as_raw_fd(), listener.as_raw_fd(), 1, 2],
                    lamp_guard::confinement::PROXY_SYSCALLS,
                )?;
                println!("{}", serde_json::json!({"state":"ready","proxy":endpoint}));
                lamp_guard::proxy::serve(&c, listener, &mut spool)?;
                Ok(0)
            }
            Some("collect") if args.len() >= 6 => {
                // collect FD|vsock:PORT UID [status:FD] [env:FD] -- ARGV...
                // `status:FD` receives the command's wait status when the
                // command exits, before its background jobs do. `env:FD` is
                // read to EOF before anything runs: the task's environment as
                // a JSON object (see process::task_env).
                let (mut status_fd, mut env_fd) = (None, None);
                let mut i = 4;
                while i < args.len() && args[i] != "--" {
                    if let Some(n) = args[i].strip_prefix("status:")
                        && status_fd.is_none()
                    {
                        status_fd = Some(fd(n)?);
                    } else if let Some(n) = args[i].strip_prefix("env:")
                        && env_fd.is_none()
                    {
                        env_fd = Some(fd(n)?);
                    } else {
                        return Err(io::Error::other(usage));
                    }
                    i += 1;
                }
                let rest = i + 1;
                if rest >= args.len() {
                    return Err(io::Error::other(usage));
                }
                if status_fd.is_some_and(|f| f < 3) || env_fd.is_some_and(|f| f < 3) {
                    return Err(io::Error::other(
                        "status and env need dedicated descriptors >=3",
                    ));
                }
                if status_fd.is_some() && status_fd == env_fd {
                    return Err(io::Error::other("status and env need distinct descriptors"));
                }
                let task_env = match env_fd {
                    Some(f) => {
                        use std::io::Read;
                        use std::os::fd::FromRawFd;
                        // SAFETY: the descriptor was passed for this purpose
                        // alone; reading it to EOF and closing it here means
                        // it never reaches the task.
                        let file = unsafe { fs::File::from_raw_fd(f) };
                        let mut json = String::new();
                        file.take(process::MAX_TASK_ENV_BYTES + 1)
                            .read_to_string(&mut json)?;
                        if json.len() as u64 > process::MAX_TASK_ENV_BYTES {
                            return Err(io::Error::other("task environment too large"));
                        }
                        process::task_env(&json)?
                    }
                    None => Vec::new(),
                };
                let mut keep: Vec<i32> = status_fd.into_iter().collect();
                // `vsock:PORT` (guest): the collector owns its host stream and
                // reconnects after Firecracker resets vsock at a snapshot.
                // `FD`: a single inherited stream, as the fixture uses.
                let emitter = if let Some(port) = args[2].strip_prefix("vsock:") {
                    let port: u32 = port.parse().map_err(io::Error::other)?;
                    lamp_guard::confinement::close_other_fds(&keep)?;
                    audit::Emitter::reconnecting(Box::new(move || audit::vsock_connect(port)))?
                } else {
                    let socket = unsafe { audit::stream(fd(&args[2])?)? };
                    use std::os::fd::AsRawFd;
                    keep.push(socket.as_raw_fd());
                    lamp_guard::confinement::close_other_fds(&keep)?;
                    audit::Emitter::new(socket)
                };
                // SAFETY: the descriptor was passed for this purpose alone and
                // survived close_other_fds; ownership moves to the collector.
                let status = status_fd.map(|f| unsafe {
                    use std::os::fd::FromRawFd;
                    fs::File::from_raw_fd(f)
                });
                process::run(
                    emitter,
                    args[3].parse().map_err(io::Error::other)?,
                    args[rest..].to_vec(),
                    task_env,
                    status,
                )
            }
            _ => Err(io::Error::other(usage)),
        }
    }
    match run() {
        Ok(code) => std::process::exit(code),
        Err(e) => {
            eprintln!("lamp-guard: {e}");
            std::process::exit(70)
        }
    }
}
#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("lamp-guard requires Linux; no unguarded fallback");
    std::process::exit(70);
}
