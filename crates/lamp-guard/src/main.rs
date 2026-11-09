#[cfg(target_os = "linux")]
fn main() {
    use lamp_guard::{Config, audit, process, runtime};
    use std::{env, fs, io};
    fn run() -> io::Result<i32> {
        let args: Vec<String> = env::args().collect();
        let usage = "usage: lamp-guard policy CONFIG | run CONFIG | receive CONFIG FD | collect FD UID -- ARGV...";
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
            Some("collect") if args.len() >= 6 && args[4] == "--" => {
                let socket = unsafe { audit::stream(fd(&args[2])?)? };
                use std::os::fd::AsRawFd;
                lamp_guard::confinement::close_other_fds(&[socket.as_raw_fd()])?;
                process::run(
                    audit::Emitter::new(socket),
                    args[3].parse().map_err(io::Error::other)?,
                    args[5..].to_vec(),
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
