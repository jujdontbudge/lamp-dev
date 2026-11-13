//! Engine-owned Ethernet attachment. The VMM gets one datagram FD and retains
//! its no-network Seatbelt boundary. A separate, confined helper owns numeric
//! host dials, policy decisions and durable DNS/connection audit.
use super::*;
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd};
use std::os::unix::{net::UnixDatagram, process::CommandExt};

pub struct Attachment {
    child: Child,
    pub ethernet: OwnedFd,
    control: UnixStream,
}
impl Drop for Attachment {
    fn drop(&mut self) {
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}
impl Attachment {
    pub fn alive(&mut self) -> bool {
        matches!(self.child.try_wait(), Ok(None))
    }
    pub fn start(
        worker: &Path,
        network_root: &Path,
        id: &str,
        generation: u64,
        uid: u32,
        gid: u32,
        allow_egress: bool,
    ) -> Result<Self> {
        let helper = worker.with_file_name("lamp-network");
        // This root is traversable before sb.dir is handed to the VMM UID,
        // and is never among the VMM's recursive filesystem grants.
        let root = network_root.join(format!(
            "{id}-g{generation}-{}",
            lamp_init::hex_encode(&random_bytes::<8>())
        ));
        std::fs::create_dir(&root).ctx("network directory")?;
        fsutil::chmod(&root, 0o700)?;
        fsutil::chown(&root, uid, gid)?;
        let (vm, net) = UnixDatagram::pair().ctx("private Ethernet socketpair")?;
        for socket in [&vm, &net] {
            for (option, size) in [(libc::SO_SNDBUF, 65536i32), (libc::SO_RCVBUF, 262144i32)] {
                if unsafe {
                    libc::setsockopt(
                        socket.as_raw_fd(),
                        libc::SOL_SOCKET,
                        option,
                        (&size as *const i32).cast(),
                        std::mem::size_of_val(&size) as _,
                    )
                } != 0
                {
                    return Err(std::io::Error::last_os_error().into());
                }
            }
        }
        let subject = serde_json::json!({"sandbox_id":id,"generation":generation});
        let sandbox = serde_json::json!({
            "subject":subject,"revision":1,
            "policy": if allow_egress {serde_json::json!({})} else {serde_json::json!({"policy_ceiling":[{"filter_mode":"transport","default_egress":"deny","allow":[],"deny":[]}]})},"provider":{"type":"direct","endpoint":"0.0.0.0:0"},
            "audit":root.join("audit.ndjson"),"audit_bytes":8<<20,
            "bridge":{"transport":{"subject":subject,"guest_addresses":[lamp_init::GUEST_ADDR],"gateway_addresses":[lamp_init::GUEST_GATEWAY],"mtu":1500,"managed_dns":true},"frame_fd":3,"guard_uid":uid}
        });
        let (control, child_control) = UnixStream::pair().ctx("network control pair")?;
        control.set_read_timeout(Some(Duration::from_secs(10)))?;
        control.set_write_timeout(Some(Duration::from_secs(10)))?;
        let config =
            serde_json::json!({"control_fd":4,"guard_uid":uid,"state_dir":root,"sandbox":sandbox});
        let path = root.join("config.json");
        std::fs::write(&path, serde_json::to_vec(&config).unwrap()).ctx("network configuration")?;
        fsutil::chmod(&path, 0o600)?;
        fsutil::chown(&path, uid, gid)?;
        let quote =
            |p: &Path| serde_json::to_string(&lamp_vz::confine::real(p).to_string_lossy()).unwrap();
        let profile = format!(
            "(version 1)(deny default)(allow process* sysctl-read file-read-metadata)(allow file-read* file-map-executable (subpath \"/System\") (subpath \"/usr/lib\") (subpath \"/usr/share\") (subpath \"/private/etc\") (subpath \"/Library/Apple\") (subpath \"/private/var/db/dyld\") (subpath \"/System/Volumes/Preboot/Cryptexes\") (literal \"/dev/urandom\") (literal \"/dev/null\") (literal \"/\") (literal {}))(allow file-read* file-write* (subpath {}))(allow network*)(allow mach-lookup (global-name \"com.apple.system.opendirectoryd.libinfo\") (global-name \"com.apple.SystemConfiguration.configd\") (global-name \"com.apple.mDNSResponder\") (global-name \"com.apple.logd\"))",
            quote(&helper),
            quote(&root)
        );
        let mut command = Command::new("/usr/bin/sandbox-exec");
        command
            .arg("-p")
            .arg(profile)
            .arg(&helper)
            .arg("managed")
            .arg(&path)
            .env_clear()
            .env("PATH", "/usr/bin:/bin")
            .current_dir("/")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::from(
                std::fs::File::create(root.join("helper.log")).ctx("network log")?,
            ));
        // Keep source descriptors above both fixed child descriptors, so
        // neither dup2 can overwrite the other source.
        let duplicate = |fd| -> Result<OwnedFd> {
            let fd = unsafe { libc::fcntl(fd, libc::F_DUPFD_CLOEXEC, 10) };
            if fd < 0 {
                return Err(std::io::Error::last_os_error().into());
            }
            Ok(unsafe { OwnedFd::from_raw_fd(fd) })
        };
        let frames = duplicate(net.as_raw_fd())?;
        let management = duplicate(child_control.as_raw_fd())?;
        let fd = frames.as_raw_fd();
        let control_fd = management.as_raw_fd();
        unsafe {
            command.pre_exec(move || {
                if libc::dup2(fd, 3) < 0
                    || libc::fcntl(3, libc::F_SETFD, 0) < 0
                    || libc::dup2(control_fd, 4) < 0
                    || libc::fcntl(4, libc::F_SETFD, 0) < 0
                    || libc::setgroups(0, std::ptr::null()) != 0
                    || libc::setgid(gid) != 0
                    || libc::setuid(uid) != 0
                {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let mut child = command.spawn().ctx("start guarded network helper")?;
        drop(net);
        drop(child_control);
        drop(frames);
        drop(management);
        let output = child.stdout.take().unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let mut line = String::new();
            let _ = BufReader::new(output).read_line(&mut line);
            let _ = tx.send(line);
        });
        let ready = rx.recv_timeout(Duration::from_secs(15)).unwrap_or_default();
        if !ready.contains("\"state\":\"ready\"") {
            let _ = child.kill();
            let _ = child.wait();
            return Err(EngineError::host(
                "guarded network helper did not become ready; inspect network helper.log",
            ));
        }
        use std::os::fd::IntoRawFd;
        Ok(Self {
            child,
            control,
            ethernet: unsafe { OwnedFd::from_raw_fd(vm.into_raw_fd()) },
        })
    }
    pub fn call(&mut self, request: &[u8]) -> Result<serde_json::Value> {
        if request.len() > 60 * 1024 {
            return Err(EngineError::invalid("network control request too large"));
        }
        let stream = &mut self.control;
        stream.write_all(request).ctx("network request")?;
        stream.write_all(b"\n").ctx("network request")?;
        // Read exactly one response without consuming a later reply.
        let mut reply = Vec::new();
        use std::io::Read;
        loop {
            let mut byte = [0u8];
            stream.read_exact(&mut byte).ctx("network reply")?;
            if byte[0] == b'\n' {
                break;
            }
            if reply.len() >= 128 * 1024 {
                return Err(EngineError::host("network reply too large"));
            }
            reply.push(byte[0]);
        }
        let reply: serde_json::Value = serde_json::from_slice(&reply)
            .map_err(|_| EngineError::host("invalid network reply"))?;
        if reply["state"] != "applied" {
            return Err(EngineError::invalid(
                reply["error"]
                    .as_str()
                    .unwrap_or("network request rejected"),
            ));
        }
        Ok(reply["data"].clone())
    }
}
