//! Per-sandbox guard network and protected-process audit attachment (T4 on T2).
//!
//! Each sandbox generation gets, inside its jail's private network namespace:
//!
//! - one TAP (`lamp0`) owned by the jail UID, the only device the VMM uses;
//! - `lamp-guard run`, which installs its own `inet lamp_guard` table and
//!   queues **every** packet on `lamp0` to userspace (no bypass), durably
//!   admitting each flow before its first packet passes;
//! - a userspace uplink (`pasta`), so guest traffic leaves through ordinary
//!   host sockets. The host never forwards or NATs sandbox packets, and no
//!   host firewall table (Docker's, libvirt's or anyone's) is touched;
//! - a host vsock listener (`/v.sock_5000` in the jail) whose every
//!   connection is handed to a fresh, confined `lamp-guard receive` with its
//!   own preallocated spool segment. Guest collectors reconnect after a
//!   snapshot resets vsock, which starts a new segment.
//!
//! The guard is ready (its queue rules installed) before the VMM loads any
//! state, so no guest packet can precede it. Losing the guard drops all
//! traffic (its queue rules stay); losing a receiver makes the collector
//! reconnect or, after `RECONNECT_DEADLINE`, kill its traced task tree.
use crate::error::{EngineError, IoContext, Result};
use crate::fc::Jail;
use serde::{Deserialize, Serialize};
use std::io::{BufRead, BufReader};
use std::os::fd::{AsRawFd, OwnedFd};
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::UnixListener;
use std::os::unix::process::CommandExt;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, AtomicU64, Ordering};
use std::time::{Duration, Instant};

pub const TAP: &str = "lamp0";
pub const UPLINK: &str = "pasta0";
pub const GATEWAY_CIDR: &str = "198.18.240.1/30";
/// Guard queue number (namespace-local).
pub const QUEUE: u16 = 314;
/// Subordinate guest UID/GID task namespace root maps to.
pub const TASK_UID: u32 = 65_537;
const NET_SPOOL_BYTES: u64 = 8 << 20;
const PROCESS_SPOOL_BYTES: u64 = 256 << 10;

/// Host-side network/audit policy, read by the supervisor at start.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    /// Host `lamp-guard` binary (root-owned).
    pub guard: PathBuf,
    /// Host `pasta` binary (userspace uplink).
    pub pasta: PathBuf,
    /// Directory for host audit spools (should be on a fast, durable disk).
    pub audit_dir: PathBuf,
    /// Policy revision every attribution carries.
    pub revision: u64,
    /// Extra denies on top of the host inventory the supervisor detects.
    #[serde(default)]
    pub host_denies: Vec<String>,
    /// Exact destination exceptions (see lamp-guard `Exception`).
    #[serde(default)]
    pub exceptions: Vec<serde_json::Value>,
    /// Owner/principal recorded in attribution.
    #[serde(default = "local_principal")]
    pub principal_id: String,
}

fn local_principal() -> String {
    "pr_local".into()
}

impl Policy {
    pub fn load(path: &Path) -> Result<Policy> {
        let text = std::fs::read_to_string(path).ctx(format!("read {}", path.display()))?;
        let p: Policy = serde_json::from_str(&text)
            .map_err(|e| EngineError::invalid(format!("{}: {e}", path.display())))?;
        if p.revision == 0 {
            return Err(EngineError::invalid(
                "network policy revision must be nonzero",
            ));
        }
        Ok(p)
    }
}

/// Every host IPv4 address (/32) and on-link prefix, so the guard denies the
/// host itself and its LANs, including publicly numbered ones.
pub fn host_inventory() -> Result<Vec<String>> {
    let mut out = Vec::new();
    for args in [
        &["-4", "-o", "addr", "show"][..],
        &["-4", "route", "show", "table", "all"],
    ] {
        let o = Command::new("ip").args(args).output().ctx("ip inventory")?;
        let text = String::from_utf8_lossy(&o.stdout);
        for line in text.lines() {
            let words: Vec<&str> = line.split_whitespace().collect();
            if args[2] == "addr" {
                if let Some(i) = words.iter().position(|w| *w == "inet") {
                    let addr = words[i + 1];
                    let ip = addr.split('/').next().unwrap();
                    out.push(format!("{ip}/32"));
                    out.push(normalize(addr));
                }
            } else if let Some(first) = words.first()
                && first.contains('/')
            {
                out.push(normalize(first));
            }
        }
    }
    out.sort();
    out.dedup();
    out.retain(|p| p != "0.0.0.0/0");
    Ok(out)
}

fn normalize(cidr: &str) -> String {
    let Some((ip, bits)) = cidr.split_once('/') else {
        return format!("{cidr}/32");
    };
    let (Ok(ip), Ok(bits)) = (ip.parse::<std::net::Ipv4Addr>(), bits.parse::<u32>()) else {
        return cidr.into();
    };
    let mask = u32::MAX.checked_shl(32 - bits.min(32)).unwrap_or(0);
    format!("{}/{bits}", std::net::Ipv4Addr::from(u32::from(ip) & mask))
}

/// Runs a root shell script inside a network namespace.
fn in_netns(netns: &Path, script: &str) -> Result<()> {
    let out = Command::new("nsenter")
        .arg(format!("--net={}", netns.display()))
        .args(["sh", "-ec", script])
        .env_clear()
        .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
        .output()
        .ctx("nsenter")?;
    if !out.status.success() {
        return Err(EngineError::host(format!(
            "netns setup failed: {}",
            String::from_utf8_lossy(&out.stderr).trim()
        )));
    }
    Ok(())
}

/// Creates the jail's TAP only (template builds: no route, no uplink).
pub fn tap_only(jail: &Jail) -> Result<()> {
    in_netns(
        &jail.netns,
        &format!(
            "ip link set lo up\nip tuntap add dev {TAP} mode tap user {u} group {u}\nip link set {TAP} up",
            u = jail.uid
        ),
    )
}

pub struct Attachment {
    guard: Option<Child>,
    pasta: Option<Child>,
    stop: Arc<AtomicBool>,
    listener: Option<std::thread::JoinHandle<()>>,
    pub guard_uid: u32,
    pub dir: PathBuf,
    /// Setup phase timings (µs).
    pub timings: Vec<(String, u64)>,
}

impl std::fmt::Debug for Attachment {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Attachment")
            .field("dir", &self.dir)
            .finish()
    }
}

pub struct Identity<'a> {
    pub host_id: &'a str,
    pub sandbox_id: &'a str,
    pub generation: u64,
    pub operation_id: &'a str,
}

impl Attachment {
    /// Builds the guard network and the audit listener for one sandbox
    /// generation, before its VMM starts.
    pub fn attach(
        policy: &Policy,
        inventory: &[String],
        jail: &Jail,
        guard_uid: u32,
        who: Identity<'_>,
    ) -> Result<Attachment> {
        let t0 = Instant::now();
        let mut timings = Vec::new();
        let dir = policy
            .audit_dir
            .join(format!("{}-g{}", who.sandbox_id, who.generation));
        std::fs::create_dir_all(&dir).ctx("create audit dir")?;
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700))
            .ctx("chmod audit dir")?;
        crate::fsutil::chown(&dir, guard_uid, guard_uid)?;
        let mut denies: Vec<String> = inventory.to_vec();
        denies.extend(policy.host_denies.iter().cloned());
        denies.push("198.18.240.1/32".into());
        let config = |spool: &Path, bytes: u64| {
            serde_json::json!({
                "attribution": {
                    "principal_id": policy.principal_id, "host_id": who.host_id,
                    "task_id": "tk_none", "sandbox_id": who.sandbox_id,
                    "generation": who.generation, "session_id": "ss_none",
                    "operation_id": who.operation_id, "policy_revision": policy.revision,
                },
                "interface": TAP, "guest_ip": lamp_init::GUEST_ADDR, "queue": QUEUE,
                "guard_uid": guard_uid, "host_denies": denies,
                "spool": spool, "spool_bytes": bytes, "exceptions": policy.exceptions,
            })
        };
        let net_config = dir.join("network.json");
        std::fs::write(
            &net_config,
            config(&dir.join("network.ndjson"), NET_SPOOL_BYTES).to_string(),
        )
        .ctx("write guard config")?;
        let mut a = Attachment {
            guard: None,
            pasta: None,
            stop: Arc::new(AtomicBool::new(false)),
            listener: None,
            guard_uid,
            dir: dir.clone(),
            timings: Vec::new(),
        };
        // 1. TAP, gateway address and forwarding inside the private namespace.
        in_netns(
            &jail.netns,
            &format!(
                "ip link set lo up\n\
                 ip tuntap add dev {TAP} mode tap user {u} group {u}\n\
                 ip addr add {GATEWAY_CIDR} dev {TAP}\n\
                 ip link set {TAP} up\n\
                 echo 1 > /proc/sys/net/ipv4/ip_forward\n\
                 echo 0 > /proc/sys/net/ipv6/conf/all/forwarding 2>/dev/null || true\n\
                 printf 'table ip lamp_uplink {{\\n chain post {{ type nat hook postrouting priority srcnat; policy accept; oifname \"{UPLINK}\" masquerade; }}\\n}}\\n' | nft -f -",
                u = jail.uid
            ),
        )?;
        timings.push(("net_tap_us".into(), t0.elapsed().as_micros() as u64));
        // 2. Guard and uplink start concurrently; both must be ready first.
        let t = Instant::now();
        let mut guard = Command::new("nsenter")
            .arg(format!("--net={}", jail.netns.display()))
            .arg(&policy.guard)
            .arg("run")
            .arg(&net_config)
            .env_clear()
            .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::from(
                std::fs::File::create(dir.join("guard.log")).ctx("guard log")?,
            ))
            .spawn()
            .ctx("start lamp-guard")?;
        let guard_out = guard.stdout.take().unwrap();
        a.guard = Some(guard);
        let pasta = Command::new(&policy.pasta)
            .args(["--foreground", "--quiet", "-4", "--config-net"])
            .args([
                "--ns-ifname",
                UPLINK,
                "--no-map-gw",
                "--no-dhcp",
                "--no-ndp",
            ])
            .args([
                "--no-dhcpv6",
                "--no-ra",
                "-t",
                "none",
                "-u",
                "none",
                "-T",
                "none",
                "-U",
                "none",
            ])
            .arg("--netns")
            .arg(&jail.netns)
            .env_clear()
            .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::from(
                std::fs::File::create(dir.join("pasta.log")).ctx("pasta log")?,
            ))
            .spawn()
            .ctx("start pasta")?;
        a.pasta = Some(pasta);
        let mut line = String::new();
        BufReader::new(guard_out)
            .read_line(&mut line)
            .ctx("guard ready line")?;
        if !line.contains("\"state\":\"ready\"") {
            let log = std::fs::read_to_string(dir.join("guard.log")).unwrap_or_default();
            a.detach();
            return Err(EngineError::host(format!(
                "lamp-guard did not become ready: {}",
                log.trim()
            )));
        }
        timings.push(("net_guard_us".into(), t.elapsed().as_micros() as u64));
        // The uplink is ready once its interface carries a default route.
        let deadline = Instant::now() + Duration::from_secs(5);
        loop {
            let route = Command::new("nsenter")
                .arg(format!("--net={}", jail.netns.display()))
                .args(["ip", "-4", "route", "show", "default"])
                .env_clear()
                .env("PATH", "/usr/sbin:/usr/bin:/sbin:/bin")
                .output()
                .ctx("uplink route")?;
            if String::from_utf8_lossy(&route.stdout).contains(UPLINK) {
                break;
            }
            if Instant::now() > deadline
                || a.pasta
                    .as_mut()
                    .unwrap()
                    .try_wait()
                    .ok()
                    .flatten()
                    .is_some()
            {
                let log = std::fs::read_to_string(dir.join("pasta.log")).unwrap_or_default();
                a.detach();
                return Err(EngineError::host(format!(
                    "uplink did not come up: {}",
                    log.trim()
                )));
            }
            std::thread::sleep(Duration::from_millis(1));
        }
        timings.push(("net_uplink_us".into(), t.elapsed().as_micros() as u64));
        // 3. Audit listener on the jail's vsock UDS for guest port 5000.
        let path = jail.path(&format!("{}_{}", crate::fc::VSOCK, lamp_init::AUDIT_PORT));
        let _ = std::fs::remove_file(&path);
        let listener = UnixListener::bind(&path).ctx("bind audit listener")?;
        crate::fsutil::chown(&path, jail.uid, jail.uid)?;
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600))
            .ctx("chmod audit listener")?;
        listener.set_nonblocking(true).ctx("audit listener")?;
        let stop = a.stop.clone();
        let guard_bin = policy.guard.clone();
        let template = config(Path::new("/dev/null"), PROCESS_SPOOL_BYTES);
        let segments = Arc::new(AtomicU64::new(0));
        a.listener = Some(std::thread::spawn(move || {
            let mut children: Vec<Child> = Vec::new();
            while !stop.load(Ordering::SeqCst) {
                match listener.accept() {
                    Ok((stream, _)) => {
                        let n = segments.fetch_add(1, Ordering::SeqCst) + 1;
                        let mut c = template.clone();
                        c["spool"] = serde_json::json!(dir.join(format!("process-{n:06}.ndjson")));
                        let cfg = dir.join(format!("process-{n:06}.json"));
                        if std::fs::write(&cfg, c.to_string()).is_err() {
                            continue;
                        }
                        let _ = stream.set_nonblocking(false);
                        let fd: OwnedFd = stream.into();
                        match receiver(&guard_bin, &cfg, fd) {
                            Ok(child) => children.push(child),
                            Err(e) => eprintln!("lamp-engine: audit receiver: {e}"),
                        }
                    }
                    Err(e) if e.kind() == std::io::ErrorKind::WouldBlock => {
                        std::thread::sleep(Duration::from_millis(1));
                    }
                    Err(e) => {
                        eprintln!("lamp-engine: audit accept: {e}");
                        std::thread::sleep(Duration::from_millis(10));
                    }
                }
                children.retain_mut(|c| c.try_wait().ok().flatten().is_none());
            }
            for mut c in children {
                let _ = c.kill();
                let _ = c.wait();
            }
        }));
        timings.push(("net_total_us".into(), t0.elapsed().as_micros() as u64));
        a.timings = timings;
        Ok(a)
    }

    /// Stops receivers, uplink and guard. Call only after the VMM is gone.
    pub fn detach(&mut self) {
        self.stop.store(true, Ordering::SeqCst);
        if let Some(l) = self.listener.take() {
            let _ = l.join();
        }
        for c in [self.pasta.take(), self.guard.take()].into_iter().flatten() {
            let mut c = c;
            let _ = c.kill();
            let _ = c.wait();
        }
    }
}

impl Drop for Attachment {
    fn drop(&mut self) {
        self.detach();
    }
}

/// Starts one confined receiver with the accepted stream as FD 3.
fn receiver(guard: &Path, config: &Path, fd: OwnedFd) -> std::io::Result<Child> {
    let raw = fd.as_raw_fd();
    let mut cmd = Command::new(guard);
    cmd.arg("receive")
        .arg(config)
        .arg("3")
        .env_clear()
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    // SAFETY: dup2 between fork and exec onto a fixed descriptor number.
    unsafe {
        cmd.pre_exec(move || {
            if libc::dup2(raw, 3) < 0 {
                return Err(std::io::Error::last_os_error());
            }
            Ok(())
        });
    }
    let child = cmd.spawn();
    drop(fd);
    child
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn prefixes_are_normalized() {
        assert_eq!(normalize("10.195.194.205/14"), "10.192.0.0/14");
        assert_eq!(normalize("192.168.178.1/24"), "192.168.178.0/24");
        assert_eq!(normalize("1.2.3.4"), "1.2.3.4/32");
    }
}
