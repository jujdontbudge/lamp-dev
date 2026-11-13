//! Sandbox lifecycle on one Linux host: create from a template, run commands,
//! memory fork (capture + restore) and release.
//!
//! Store layout under the store root (an XFS filesystem with reflinks):
//!
//! ```text
//! keys/producer.key           Ed25519 producer seed (hex), root-only
//! seeds/<digest>/             verified seeds (seed.json, vmlinux, rootfs.ext4)
//! templates/<template_id>/    manifest.json provenance.json vmlinux root.ext4 rootfs.ext4 mem vmstate
//! templates/names/<name>      symlink to a template id
//! captures/<capture_id>/      rootfs.ext4 meta.json [mem vmstate when durable]
//! jail/firecracker/<jail>/    per-sandbox jailer chroot (0700 root above it)
//! grants/<grant_id>.json      installed control grants (root-only)
//! lock                        exclusive store ownership (flock)
//! ```
//!
//! and RAM-backed memory state under `mem_dir` (tmpfs, huge pages, 0700):
//!
//! ```text
//! captures/<capture_id>.mem       full RAM image (root:root 0400)
//! captures/<capture_id>.vmstate   CPU/device state (root:root 0400)
//! jails/<jail>/                   bound into each jail as /mem.d (0700, jail UID)
//! ```
//!
//! A memory capture is a Firecracker **Full** snapshot: every page of the
//! configured guest RAM is written, whatever was recently touched (dirty
//! tracking is off). The fork maps that complete image privately through
//! Firecracker's File backend inside its own jail, so no page server or
//! other host process ever handles guest memory. A capture image is readable
//! only by root until it is handed to the one sandbox restoring it, which
//! then owns it. No live KVM handle, process fork or mutable shared page is
//! ever inherited.
use crate::disk::{self, DiskRef};
use crate::durable;
use crate::error::{EngineError, IoContext, Result};
use crate::fault;
use crate::fc::{self, Binaries, Jail, Limits};
use crate::fsutil::{self, now_ms, now_ns, random_bytes};
use crate::guest;
use crate::manifest::{self, AccelerationVariant, Compatibility, Manifest, Provenance, Resources};
use crate::memimg;
use ed25519_dalek::{SigningKey, VerifyingKey};
use lamp_init::{Barrier, Empty, Exec, ExecDone, ExecStarted, GuestRequest, GuestResponse};
use std::collections::{BTreeMap, BTreeSet, HashMap, VecDeque};
use std::fs::File;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Duration, Instant};

pub const ENGINE_NAME: &str = "firecracker-1.17.0";
pub const STATE_ABI: &str = "firecracker-1.17.0/snapshot";
pub const DEVICE_GRAPH: &str = "virtio-blk:rootfs:async-v1,vsock:cid3,serial,virtio-net:eth0-lamp0";
pub const STANDARD: Resources = Resources {
    vcpus: 2,
    memory_mib: 4096,
    disk_gib: 20,
};
pub const DEFAULT_PIDS_MAX: u64 = 4096;
/// Host memory left to the owner's own work and never admitted to guests.
const HOST_RESERVE_MIB: u64 = 4096;
/// Charged per sandbox on top of its guest RAM (VMM, guard, page tables).
const VMM_ALLOWANCE_MIB: u64 = 256;
/// Most extra disks one sandbox may request.
const MAX_EXTRA_DISKS: usize = 8;

/// One device a caller asked for at create. Firecracker exposes block
/// devices only; every other kind is refused before any work starts.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DeviceRequest {
    pub id: String,
    pub kind: String,
    pub size_mib: u64,
}

/// Device kinds this engine can attach, and why the others are refused.
pub fn device_support(kind: &str) -> std::result::Result<(), &'static str> {
    match kind {
        "disk" => Ok(()),
        "gpu" => Err(
            "Firecracker has no GPU passthrough or virtio-gpu device; GPU acceleration is unavailable on this Linux engine",
        ),
        "display" | "keyboard" | "mouse" => Err("Firecracker has no display or input devices"),
        "audio_input" | "audio_output" => Err("Firecracker has no audio devices"),
        _ => Err("unknown device type"),
    }
}

/// Host capacity, read at admission. Nothing is overcommitted: guest RAM
/// plus allowance must fit both physical RAM less the host reserve and what
/// the host has available now; vCPUs must fit the host's online CPUs; disks
/// must fit the store's free space.
#[derive(Debug, Clone, serde::Serialize)]
pub struct HostCapacity {
    pub cpus: u32,
    pub physical_mib: u64,
    pub available_mib: u64,
    pub reserve_mib: u64,
    pub store_free_gib: u64,
}

impl HostCapacity {
    pub fn detect(store: &Path) -> Result<HostCapacity> {
        let meminfo = std::fs::read_to_string("/proc/meminfo").ctx("read meminfo")?;
        let field = |name: &str| {
            meminfo
                .lines()
                .find_map(|l| l.strip_prefix(name))
                .and_then(|v| v.trim().trim_end_matches("kB").trim().parse::<u64>().ok())
                .map(|kib| kib >> 10)
                .ok_or_else(|| EngineError::host(format!("meminfo has no {name}")))
        };
        let physical_mib = field("MemTotal:")?;
        let available_mib = field("MemAvailable:")?;
        let cpus = std::thread::available_parallelism()
            .map(|n| n.get() as u32)
            .unwrap_or(1);
        let st = nix_statvfs(store)?;
        Ok(HostCapacity {
            cpus,
            physical_mib,
            available_mib,
            reserve_mib: HOST_RESERVE_MIB.min(physical_mib / 4),
            store_free_gib: st >> 30,
        })
    }
}

fn nix_statvfs(path: &Path) -> Result<u64> {
    use std::os::unix::ffi::OsStrExt;
    let c = std::ffi::CString::new(path.as_os_str().as_bytes())
        .map_err(|_| EngineError::invalid("store path has NUL"))?;
    let mut st: libc::statvfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statvfs(c.as_ptr(), &mut st) } != 0 {
        return Err(EngineError::host(format!(
            "statvfs {}: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok((st.f_bavail as u64).saturating_mul(st.f_frsize as u64))
}
/// Completed commands remembered per sandbox for completion lookups. Running
/// commands are never forgotten; at most `MAX_RUNNING` run at once.
const COMMAND_MEMORY: usize = 256;
/// Longest chain of memory deltas a durable capture may rest on.
const MAX_CHAIN: usize = 64;
const MAX_RUNNING: usize = 1024;
/// Capture states (`CaptureRec::restores`): a capture is restored once.
const CAPTURE_AVAILABLE: u64 = 0;
const CAPTURE_RESTORING: u64 = 1;
const CAPTURE_CONSUMED: u64 = 2;
/// File a restored fork maps its memory from, inside its `/mem.d`.
const SRC_MEM: &str = "src.mem";

#[derive(Debug, Clone)]
pub struct Config {
    pub store: PathBuf,
    pub netns_dir: PathBuf,
    pub bins: Binaries,
    pub uid_base: u32,
    pub uid_count: u32,
    pub host_id: String,
    /// RAM-backed (tmpfs) directory for volatile memory captures.
    pub mem_dir: PathBuf,
    pub mem_dir_size: String,
    /// Keep one preallocated image ready for a sandbox's next capture
    /// (fast forks; costs one guest-size of RAM per forked sandbox).
    pub capture_spare: bool,
    /// Extra trusted producer keys beyond this host's own.
    pub trusted_producers: Vec<VerifyingKey>,
    /// Guard network and protected-process audit. `None` leaves sandboxes
    /// with an unconnected link and unaudited commands (diagnostic only).
    pub network: Option<crate::net::Policy>,
    pub subscription: Option<crate::subscription::SubscriptionPolicy>,
    pub exports_dir: Option<PathBuf>,
    /// Disk backend: `auto` (reflink on XFS/btrfs, dm-thin otherwise),
    /// `reflink` or `thin`.
    pub disk_backend: String,
    pub thin: crate::disk::ThinConfig,
}

impl Config {
    pub fn defaults(store: PathBuf) -> Config {
        Config {
            store,
            netns_dir: PathBuf::from("/run/lamp/netns"),
            bins: Binaries {
                firecracker: PathBuf::from("/usr/local/lib/lamp/firecracker"),
                jailer: PathBuf::from("/usr/local/lib/lamp/jailer"),
            },
            mem_dir: PathBuf::from("/run/lamp/mem"),
            mem_dir_size: "75%".into(),
            capture_spare: true,
            uid_base: 900_000,
            uid_count: 65_536,
            host_id: hostname(),
            trusted_producers: Vec::new(),
            network: None,
            subscription: None,
            exports_dir: None,
            disk_backend: "auto".into(),
            thin: crate::disk::ThinConfig::default(),
        }
    }
}

pub fn hostname() -> String {
    std::fs::read_to_string("/proc/sys/kernel/hostname")
        .map(|s| s.trim().to_string())
        .unwrap_or_else(|_| "localhost".into())
}

/// Host facts that saved state must match before Firecracker parses it.
#[derive(Debug, Clone)]
pub struct HostCompat {
    pub engine_digest: String,
    pub cpu_baseline: String,
}

impl HostCompat {
    pub fn detect(bins: &Binaries) -> Result<HostCompat> {
        let engine_digest = fsutil::digest_file(&bins.firecracker)?.digest;
        let cpuinfo = std::fs::read_to_string("/proc/cpuinfo").ctx("read cpuinfo")?;
        let mut model = String::new();
        let mut flags = String::new();
        for line in cpuinfo.lines() {
            if model.is_empty() && line.starts_with("model name") {
                model = line.split(':').nth(1).unwrap_or("").trim().to_string();
            }
            if flags.is_empty() && line.starts_with("flags") {
                let mut f: Vec<&str> = line
                    .split(':')
                    .nth(1)
                    .unwrap_or("")
                    .split_whitespace()
                    .collect();
                f.sort_unstable();
                flags = f.join(" ");
            }
        }
        let cpu_baseline = format!("{model}|{}", manifest::sha256_hex(flags.as_bytes()));
        Ok(HostCompat {
            engine_digest,
            cpu_baseline: manifest::sha256_hex(cpu_baseline.as_bytes()),
        })
    }
}

#[derive(Debug)]
pub struct Template {
    pub id: String,
    pub dir: PathBuf,
    pub manifest: Manifest,
    pub manifest_digest: String,
    pub variant: AccelerationVariant,
}

enum Source {
    Template {
        mem: PathBuf,
        vmstate: PathBuf,
        disk: DiskRef,
        template_id: String,
        /// Cold boot (kernel, cmdline) instead of the prepared image.
        cold: Option<(PathBuf, String)>,
    },
    Capture(Arc<CaptureRec>),
    Durable(Box<durable::Entry>),
}

/// The full memory image a sandbox's durable captures are encoded against.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Base {
    /// The template's memory image.
    Template,
    /// The durable capture this sandbox was restored from; its full image
    /// is the sandbox's own read-only `src.mem`.
    Capture(String),
}

/// Commands by operation ID, plus their insertion order for bounding.
pub type CommandTable = (HashMap<String, CommandRec>, VecDeque<String>);

/// One command started through `Start`, observable by its operation ID.
#[derive(Debug, Clone)]
pub struct CommandRec {
    pub started: ExecStarted,
    pub done: Option<ExecDone>,
    /// Set when completion can no longer be observed (VMM gone, channel lost).
    pub lost: Option<String>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum State {
    Allocated,
    Running,
    Stopped,
    Failed(String),
}

impl State {
    pub fn name(&self) -> &'static str {
        match self {
            State::Allocated => "allocated",
            State::Running => "running",
            State::Stopped => "stopped",
            State::Failed(_) => "failed",
        }
    }
}

#[derive(Debug)]
pub struct Sandbox {
    pub id: String,
    pub generation: u64,
    pub policy_revision: u64,
    pub resources: Resources,
    pub state: State,
    pub jail: Option<Jail>,
    pub uid: u32,
    pub source: String,
    pub captures: Vec<String>,
    /// The writable disk attached in the jail.
    pub working: Option<disk::Working>,
    pub template_id: String,
    pub base: Base,
    /// Durable ancestor of the current state (lineage for new captures).
    pub parent: Option<String>,
    pub barrier: Option<lamp_init::BarrierAck>,
    /// Commands by the operation ID of their `Start`, oldest first.
    pub commands: Arc<Mutex<CommandTable>>,
    /// Guard network and audit listener of this generation.
    pub net: Option<crate::net::Attachment>,
    /// The same, still being built (started at Allocate so it overlaps the
    /// source's capture during a fork); Restore joins it. Carries its guard
    /// UID so a failed or abandoned attachment frees it.
    pub pending_net: Option<PendingNet>,
    pub network_policy: Option<crate::net::Policy>,
    /// Guest process limit (pids.max of the guest's tasks cgroup).
    pub pids_max: u64,
    /// Extra block devices requested at create (cold boot only).
    pub disks: Vec<DeviceRequest>,
    /// Booted from the template's kernel and disk at caller-chosen size
    /// rather than restored from its prepared memory image.
    pub cold: bool,
}

pub type PendingNet = std::thread::JoinHandle<(u32, Result<crate::net::Attachment>)>;

impl Sandbox {
    /// Kills the VMM and removes its jail (and with it the memory it mapped),
    /// then its guard, uplink and audit receivers.
    fn teardown(&mut self) -> Option<u32> {
        if let Some(j) = self.jail.take() {
            j.destroy();
        }
        if let Some(pending) = self.pending_net.take() {
            // Dropping a built attachment detaches it.
            if let Ok((uid, _)) = pending.join() {
                return Some(uid);
            }
        }
        self.net.take().map(|mut n| {
            n.detach();
            n.guard_uid
        })
    }
}

#[derive(Debug)]
pub struct CaptureRec {
    pub id: String,
    pub dir: PathBuf,
    /// Sealed disk (volatile: owned by this record; durable: the store's).
    pub disk: DiskRef,
    pub template_id: String,
    pub base: Base,
    pub parent: Option<String>,
    /// Committed to the durable store; this record only holds its tmpfs
    /// image for a fast first restore.
    pub durable: bool,
    /// Full RAM image and VM state on tmpfs, root:root 0400.
    pub mem: PathBuf,
    pub vmstate: PathBuf,
    pub source_sandbox: String,
    pub source_generation: u64,
    pub resources: Resources,
    pub durability: String,
    pub restores: Mutex<u64>,
}

/// What an operation reports back through lamp.worker/1.
#[derive(Debug, Default, Clone)]
pub struct Outcome {
    pub state: String,
    pub handles: Vec<String>,
    pub command: Option<ExecDone>,
    pub detail: BTreeMap<String, String>,
}

/// Authority every sandbox command must present (checked independently of Go).
#[derive(Debug, Clone, Copy)]
pub struct Authority<'a> {
    pub sandbox_id: &'a str,
    pub generation: u64,
    pub policy_revision: u64,
}

pub struct Engine {
    pub cfg: Config,
    pub compat: HostCompat,
    producer: SigningKey,
    trusted: HashMap<String, VerifyingKey>,
    templates: RwLock<HashMap<String, Arc<Template>>>,
    sandboxes: Mutex<HashMap<String, Arc<Mutex<Sandbox>>>>,
    pub(crate) captures: Mutex<HashMap<String, Arc<CaptureRec>>>,
    uids: Mutex<BTreeSet<u32>>,
    pub disk: disk::Backend,
    pub state: durable::Store,
    /// Template disks in the active backend, by template id.
    template_disks: RwLock<HashMap<String, DiskRef>>,
    /// Host addresses and on-link prefixes every guard denies.
    pub inventory: Vec<String>,
    pub custody: Option<Mutex<crate::custody_store::CustodyStore>>,
    pub(crate) bridges: Mutex<HashMap<String, crate::subscription_bridge::Bridge>>,
    pub(crate) bridge_task: Mutex<Option<String>>,
    /// Exclusive store ownership while this engine may change the store.
    _lock: Option<std::fs::File>,
}

/// How a process uses the store.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    /// The supervisor daemon: owns the store exclusively and, being the only
    /// owner, recovers it (kills leftover jails, drops volatile state).
    Daemon,
    /// Offline maintenance (seed import, template build): owns the store
    /// exclusively but never touches leftover jails or captures, and refuses
    /// to start while a daemon holds the store.
    Offline,
    /// Read-only listing: takes no lock and writes nothing.
    ReadOnly,
}

/// A command result that has started but not (observably) finished.
impl CommandRec {
    fn completed(&self) -> bool {
        self.done.is_some() || self.lost.is_some()
    }
}

fn running(table: &CommandTable) -> usize {
    table.0.values().filter(|r| !r.completed()).count()
}

/// Records a started command. Only completed commands are ever forgotten,
/// oldest first, so a long-running command stays observable however many
/// short ones follow it.
fn remember(table: &mut CommandTable, op: &str, rec: CommandRec) {
    if table.0.insert(op.to_string(), rec).is_none() {
        table.1.push_back(op.to_string());
    }
    forget_old_completed(table);
}

fn forget_old_completed(table: &mut CommandTable) {
    let mut completed = table.0.values().filter(|r| r.completed()).count();
    while completed > COMMAND_MEMORY {
        let Some(pos) = table
            .1
            .iter()
            .position(|op| table.0.get(op).is_none_or(CommandRec::completed))
        else {
            break;
        };
        let op = table.1.remove(pos).unwrap();
        if table.0.remove(&op).is_some() {
            completed -= 1;
        }
    }
}

/// Freezes the guest's task processes. The freeze may have applied although
/// its reply was lost, late or wrong, so any uncertain result is followed by a
/// thaw; the error then says whether that thaw could not be confirmed (the
/// caller must not leave such a sandbox marked running).
fn freeze_guest(vsock: &Path) -> std::result::Result<(), (EngineError, Option<EngineError>)> {
    match guest::call(
        vsock,
        &GuestRequest::Freeze(Empty {}),
        Duration::from_secs(10),
    ) {
        Ok(GuestResponse::Frozen) => Ok(()),
        uncertain => {
            let e = match uncertain {
                Ok(other) => EngineError::host(format!("freeze: {other:?}")),
                Err(e) => e,
            };
            Err((e, thaw_guest(vsock).err()))
        }
    }
}

/// Thaws the guest's task processes, retrying a few times: thawing is
/// idempotent, so an uncertain attempt is simply repeated.
fn thaw_guest(vsock: &Path) -> Result<()> {
    let mut last = EngineError::host("thaw not attempted");
    for _ in 0..3 {
        match guest::call(vsock, &GuestRequest::Thaw(Empty {}), Duration::from_secs(5)) {
            Ok(GuestResponse::Thawed) => return Ok(()),
            Ok(other) => last = EngineError::host(format!("thaw: {other:?}")),
            Err(e) => last = e,
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    Err(last)
}

fn pending(started: &ExecStarted) -> ExecDone {
    ExecDone {
        exit_code: 0,
        signal: 0,
        stdout_b64: String::new(),
        stderr_b64: String::new(),
        truncated: false,
        timed_out: false,
        started_unix_ns: started.started_unix_ns,
        finished_unix_ns: 0,
    }
}

fn us(d: Duration) -> String {
    d.as_micros().to_string()
}

/// Sets the guest's process limit (pids.max of the tasks cgroup every
/// command runs in) and hands extra disks (/dev/vdb onward, in request
/// order) to the task user. Runs as the guest helper's own direct command.
fn apply_guest_limits(
    vsock: &Path,
    sandbox_id: &str,
    generation: u64,
    n: u64,
    extra_disks: usize,
) -> Result<()> {
    let disks: String = (0..extra_disks)
        .map(|i| {
            format!(
                " chown {} /dev/vd{};",
                crate::net::TASK_UID,
                (b'b' + i as u8) as char
            )
        })
        .collect();
    let op = format!("op_limits_{}", lamp_init::hex_encode(&random_bytes::<8>()));
    let req = GuestRequest::Exec(Exec {
        sandbox_id: sandbox_id.into(),
        generation,
        exec_id: op.clone(),
        argv: vec![
            "/bin/sh".into(),
            "-c".into(),
            format!(
                "grep -qw pids /sys/fs/cgroup/cgroup.subtree_control || echo +pids > /sys/fs/cgroup/cgroup.subtree_control; echo {n} > {}/pids.max;{disks}",
                lamp_init::TASKS_CGROUP
            ),
        ],
        env: BTreeMap::new(),
        cwd: String::new(),
        timeout_ms: 10_000,
        output_limit: 4096,
        stdin_path: String::new(),
        pty: false,
        stdin_open: false,
        audit: None,
    });
    let mut conn = guest::send(vsock, &req)?;
    match conn.next(Some(Duration::from_secs(10)))? {
        GuestResponse::ExecStarted(_) => {}
        other => return Err(EngineError::host(format!("{other:?}"))),
    }
    let done = guest::wait_exec(
        vsock,
        sandbox_id,
        generation,
        &op,
        Some(Instant::now() + Duration::from_secs(15)),
        || true,
    )?;
    if done.exit_code != 0 {
        return Err(EngineError::host(format!(
            "exit {}: {}",
            done.exit_code, done.stderr_b64
        )));
    }
    Ok(())
}

fn valid_device_id(id: &str) -> bool {
    !id.is_empty()
        && id.len() <= 32
        && id
            .bytes()
            .all(|b| b.is_ascii_lowercase() || b.is_ascii_digit() || b == b'_')
}

pub fn valid_id(id: &str, prefix: &str) -> bool {
    id.starts_with(prefix)
        && (prefix.len() + 1..=60).contains(&id.len())
        && id
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_')
}

fn jail_id(sandbox_id: &str) -> String {
    sandbox_id.replace('_', "-")
}

fn load_key(path: &Path) -> Result<SigningKey> {
    let text = std::fs::read_to_string(path).ctx("read producer key")?;
    let bytes = lamp_init::hex_decode(text.trim())
        .filter(|b| b.len() == 32)
        .ok_or_else(|| EngineError::host("malformed producer key"))?;
    Ok(SigningKey::from_bytes(&bytes.try_into().unwrap()))
}

pub fn load_or_create_key(path: &Path) -> Result<SigningKey> {
    if path.exists() {
        return load_key(path);
    }
    let seed: [u8; 32] = random_bytes();
    std::fs::create_dir_all(path.parent().unwrap()).ctx("create key dir")?;
    let tmp = path.with_extension("tmp");
    std::fs::write(&tmp, lamp_init::hex_encode(&seed)).ctx("write producer key")?;
    fsutil::chmod(&tmp, 0o600)?;
    std::fs::rename(&tmp, path).ctx("install producer key")?;
    Ok(SigningKey::from_bytes(&seed))
}

impl Engine {
    /// Opens the store and verifies every template. Only `Mode::Daemon`
    /// discards state from a previous supervisor run (volatile in this
    /// release); the others never touch jails or captures.
    pub fn open(cfg: Config, mode: Mode) -> Result<Engine> {
        let lock = if mode == Mode::ReadOnly {
            None
        } else {
            std::fs::create_dir_all(&cfg.store).ctx("create store")?;
            Some(
                fsutil::lock_exclusive(&cfg.store.join("lock")).map_err(|e| {
                    EngineError::in_use(format!(
                        "the store at {} is owned by another process \
                     (stop lamp-supervisor first): {}",
                        cfg.store.display(),
                        e.message
                    ))
                })?,
            )
        };
        let producer = if mode == Mode::ReadOnly {
            load_key(&cfg.store.join("keys/producer.key"))?
        } else {
            for sub in [
                "seeds",
                "templates/names",
                "captures",
                "jail/firecracker",
                "staging",
                "grants",
            ] {
                std::fs::create_dir_all(cfg.store.join(sub)).ctx(format!("create store/{sub}"))?;
            }
            // Jails, captures and grants are never readable by other users.
            for private in ["jail", "jail/firecracker", "captures", "grants", "keys"] {
                let _ = std::fs::create_dir_all(cfg.store.join(private));
                fsutil::chmod(&cfg.store.join(private), 0o700)?;
            }
            fc::init_cgroups()?;
            fc::ensure_tmpfs(&cfg.mem_dir, &cfg.mem_dir_size)?;
            fsutil::chmod(&cfg.mem_dir, 0o700)?;
            for sub in ["captures", "jails"] {
                std::fs::create_dir_all(cfg.mem_dir.join(sub)).ctx("create memory dirs")?;
                fsutil::chmod(&cfg.mem_dir.join(sub), 0o700)?;
            }
            load_or_create_key(&cfg.store.join("keys/producer.key"))?
        };
        let mut trusted = HashMap::new();
        trusted.insert(
            manifest::key_id(&producer.verifying_key()),
            producer.verifying_key(),
        );
        for k in &cfg.trusted_producers {
            trusted.insert(manifest::key_id(k), *k);
        }
        let compat = HostCompat::detect(&cfg.bins)?;
        let cfg_network = cfg.network.is_some() && mode == Mode::Daemon;
        let custody = if mode == Mode::ReadOnly {
            None
        } else {
            let directory = cfg.store.join("custody");
            std::fs::create_dir_all(&directory)?;
            fsutil::chmod(&directory, 0o700)?;
            let owner = cfg
                .subscription
                .as_ref()
                .map_or("pr_local", |policy| policy.owner_id.as_str());
            let hosts = cfg.subscription.as_ref().map_or_else(
                || vec![cfg.host_id.clone()],
                |policy| policy.custody_host_ids.clone(),
            );
            let store = crate::custody_store::CustodyStore::open(
                &directory.join("graph.json"),
                owner,
                &cfg.host_id,
                &hosts,
            )?;
            Some(Mutex::new(store))
        };
        // Read-only listings never touch the pool or the state journal.
        let backend = if mode == Mode::ReadOnly {
            disk::Backend::Reflink
        } else {
            disk::Backend::open(&cfg.store, &cfg.disk_backend, &cfg.thin)?
        };
        let state = if mode == Mode::ReadOnly {
            durable::Store::open(&std::env::temp_dir().join("lamp-readonly-state"), &trusted)?
        } else {
            durable::Store::open(&cfg.store, &trusted)?
        };
        let engine = Engine {
            cfg,
            compat,
            producer,
            trusted,
            templates: RwLock::new(HashMap::new()),
            sandboxes: Mutex::new(HashMap::new()),
            captures: Mutex::new(HashMap::new()),
            uids: Mutex::new(BTreeSet::new()),
            disk: backend,
            state,
            template_disks: RwLock::new(HashMap::new()),
            inventory: if cfg_network {
                crate::net::host_inventory()?
            } else {
                Vec::new()
            },
            _lock: lock,
            custody,
            bridges: Mutex::new(HashMap::new()),
            bridge_task: Mutex::new(None),
        };
        match mode {
            Mode::Daemon => {
                engine.clean_stale();
                if let Some(pool) = engine.disk.thin() {
                    pool.remove_stale();
                }
            }
            // Never reuse the identity of a jail left behind by a daemon.
            Mode::Offline => {
                engine.reserve_leftover_uids();
                if let Some(pool) = engine.disk.thin() {
                    pool.remove_stale();
                }
            }
            Mode::ReadOnly => {}
        }
        engine.load_templates(mode != Mode::ReadOnly);
        if mode != Mode::ReadOnly {
            for t in engine.templates() {
                if let Err(e) = engine.template_disk(&t) {
                    eprintln!("lamp-engine: template {} has no disk: {e}", t.id);
                }
            }
        }
        if mode == Mode::Daemon {
            engine.recover_state();
        }
        Ok(engine)
    }

    /// Collects what no committed capture references: uncommitted objects
    /// and manifests, and (thin backend) every volume that is neither a
    /// template nor a live durable disk. Nothing else survives a restart.
    fn recover_state(&self) {
        match self.state.gc_files() {
            Ok(n) if n > 0 => eprintln!("lamp-engine: removed {n} uncommitted state file(s)"),
            Ok(_) => {}
            Err(e) => eprintln!("lamp-engine: state gc: {e}"),
        }
        if let Some(pool) = self.disk.thin() {
            let (live, _) = self.state.thin_ids();
            let keep: BTreeSet<u32> = live
                .into_iter()
                .chain(
                    self.template_disks
                        .read()
                        .unwrap()
                        .values()
                        .filter_map(|d| match d {
                            DiskRef::Thin(id) => Some(*id),
                            DiskRef::File(_) => None,
                        }),
                )
                .collect();
            for id in &keep {
                pool.note_used(*id);
            }
            match pool.volumes() {
                Ok(all) => {
                    let mut removed = 0;
                    for id in all.difference(&keep) {
                        if pool.delete(*id).is_ok() {
                            removed += 1;
                        }
                    }
                    let _ = pool.commit();
                    if removed > 0 {
                        eprintln!("lamp-engine: removed {removed} unreferenced thin volume(s)");
                    }
                }
                Err(e) => eprintln!("lamp-engine: cannot list thin volumes: {e}"),
            }
        }
    }

    /// The template's disk in the active backend (thin: imported once).
    pub fn template_disk(&self, t: &Template) -> Result<DiskRef> {
        if let Some(d) = self.template_disks.read().unwrap().get(&t.id) {
            return Ok(d.clone());
        }
        let d = match &self.disk {
            disk::Backend::Reflink => DiskRef::File(format!("templates/{}/{}", t.id, fc::ROOTFS)),
            disk::Backend::Thin(pool) => {
                let map = pool.dir.join("templates").join(&t.id);
                let known = std::fs::read_to_string(&map)
                    .ok()
                    .and_then(|s| s.trim().parse::<u32>().ok());
                match known {
                    Some(id) => {
                        pool.note_used(id);
                        DiskRef::Thin(id)
                    }
                    None => {
                        let id = pool.new_id();
                        let t0 = Instant::now();
                        pool.import(id, &t.dir.join(fc::ROOTFS))?;
                        let tmp = map.with_extension("tmp");
                        std::fs::write(&tmp, format!("{id}\n")).ctx("record template volume")?;
                        fsutil::fsync_path(&tmp)?;
                        std::fs::rename(&tmp, &map).ctx("record template volume")?;
                        fsutil::fsync_path(&pool.dir.join("templates"))?;
                        eprintln!(
                            "lamp-engine: template {} imported as thin volume {id} in {} ms",
                            t.id,
                            t0.elapsed().as_millis()
                        );
                        DiskRef::Thin(id)
                    }
                }
            }
        };
        self.template_disks
            .write()
            .unwrap()
            .insert(t.id.clone(), d.clone());
        Ok(d)
    }

    fn disk_path(&self, p: &str) -> PathBuf {
        self.cfg.store.join(p)
    }

    /// A new writable disk for `jail`, descending from `src`.
    fn attach_disk(&self, src: &DiskRef, jail: &Jail, bytes: u64) -> Result<disk::Working> {
        let node = jail.path(fc::ROOTFS);
        match (&self.disk, src) {
            (disk::Backend::Reflink, DiskRef::File(p)) => {
                fsutil::reflink(&self.disk_path(p), &node, 0o600)?;
                fsutil::chown(&node, jail.uid, jail.uid)?;
                Ok(disk::Working {
                    disk: DiskRef::File(String::new()),
                    dm_name: None,
                })
            }
            (disk::Backend::Thin(pool), DiskRef::Thin(origin)) => {
                let id = pool.new_id();
                pool.snapshot(id, *origin)
                    .inspect_err(|_| pool.forget(id))?;
                let (name, dev) = match pool.activate('w', id, bytes) {
                    Ok(v) => v,
                    Err(e) => {
                        let _ = pool.delete(id);
                        return Err(e);
                    }
                };
                let w = disk::Working {
                    disk: DiskRef::Thin(id),
                    dm_name: Some(name),
                };
                if let Err(e) = pool.node(&node, dev, jail.uid) {
                    self.release_disk(Some(w));
                    return Err(e);
                }
                Ok(w)
            }
            _ => Err(EngineError::new(
                "ENGINE_UNQUALIFIED",
                format!("disk {} is not in this store's backend", src.encode()),
            )),
        }
    }

    /// Detaches and frees a working disk (its VMM must be gone).
    fn release_disk(&self, w: Option<disk::Working>) {
        if let (Some(w), disk::Backend::Thin(pool)) = (w, &self.disk) {
            if let Some(name) = &w.dm_name
                && let Err(e) = pool.deactivate(name)
            {
                eprintln!("lamp-engine: deactivate {name}: {e}");
                return;
            }
            if let DiskRef::Thin(id) = w.disk
                && let Err(e) = pool.delete(id)
            {
                eprintln!("lamp-engine: delete thin volume {id}: {e}");
            }
        }
    }

    fn reserve_leftover_uids(&self) {
        use std::os::unix::fs::MetadataExt;
        let jails = self.cfg.store.join("jail/firecracker");
        let mut used = self.uids.lock().unwrap();
        for e in std::fs::read_dir(jails).into_iter().flatten().flatten() {
            if let Ok(m) = std::fs::metadata(e.path().join("root")) {
                used.insert(m.uid());
            }
        }
    }

    pub fn producer_key(&self) -> &SigningKey {
        &self.producer
    }

    fn clean_stale(&self) {
        let jails = self.cfg.store.join("jail/firecracker");
        if let Ok(entries) = std::fs::read_dir(&jails) {
            for e in entries.flatten() {
                let id = e.file_name().to_string_lossy().into_owned();
                Jail {
                    id: id.clone(),
                    dir: e.path(),
                    root: e.path().join("root"),
                    uid: 0,
                    netns: self.cfg.netns_dir.join(&id),
                    memdir: self.cfg.mem_dir.join("jails").join(&id),
                    pid: None,
                }
                .destroy();
            }
        }
        for dir in ["captures", "staging"] {
            let path = self.cfg.store.join(dir);
            let _ = std::fs::remove_dir_all(&path);
            let _ = std::fs::create_dir_all(&path);
        }
        let _ = fsutil::chmod(&self.cfg.store.join("captures"), 0o700);
        for dir in ["layers", "captures", "jails"] {
            let path = self.cfg.mem_dir.join(dir);
            let _ = std::fs::remove_dir_all(&path);
            if dir != "layers" {
                let _ = std::fs::create_dir_all(&path);
                let _ = fsutil::chmod(&path, 0o700);
            }
        }
    }

    fn load_templates(&self, may_quarantine: bool) {
        let dir = self.cfg.store.join("templates");
        let Ok(entries) = std::fs::read_dir(&dir) else {
            return;
        };
        for e in entries.flatten() {
            let name = e.file_name().to_string_lossy().into_owned();
            if !name.starts_with("tp_") {
                continue;
            }
            match self.verify_template_dir(&e.path()) {
                Ok(t) => {
                    self.templates
                        .write()
                        .unwrap()
                        .insert(t.id.clone(), Arc::new(t));
                }
                Err(err) if !may_quarantine => {
                    eprintln!("lamp-engine: template {name} does not verify: {err}");
                }
                Err(err) => {
                    eprintln!("lamp-engine: template {name} quarantined: {err}");
                    let q = self.cfg.store.join("quarantine");
                    let _ = std::fs::create_dir_all(&q);
                    let _ = std::fs::rename(e.path(), q.join(&name));
                }
            }
        }
    }

    /// Verifies signature, producer authority, compatibility and every
    /// object digest before any saved state can reach the VMM parser.
    pub fn verify_template_dir(&self, dir: &Path) -> Result<Template> {
        let read = |n: &str| std::fs::read(dir.join(n)).ctx(format!("read {n}"));
        let manifest: Manifest = serde_json::from_slice(&read("manifest.json")?)
            .map_err(|e| EngineError::new("DATA_UNAVAILABLE", format!("manifest: {e}")))?;
        let provenance: Provenance = serde_json::from_slice(&read("provenance.json")?)
            .map_err(|e| EngineError::new("DATA_UNAVAILABLE", format!("provenance: {e}")))?;
        let digest = manifest.digest()?;
        let key = self
            .trusted
            .get(&provenance.producer_key_id)
            .ok_or_else(|| {
                EngineError::new(
                    "HOST_NOT_TRUSTED",
                    "capture producer is not an enrolled key",
                )
            })?;
        provenance.verify(key)?;
        if provenance.manifest_digest != digest {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "provenance names another manifest",
            ));
        }
        let id = manifest::template_id(&digest);
        if dir.file_name().and_then(|n| n.to_str()) != Some(id.as_str()) {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "template directory does not match digest",
            ));
        }
        let variant = manifest
            .acceleration_variants
            .iter()
            .find(|v| {
                v.compatibility.engine_digest == self.compat.engine_digest
                    && v.compatibility.cpu_baseline == self.compat.cpu_baseline
                    && v.compatibility.state_abi == STATE_ABI
            })
            .cloned()
            .ok_or_else(|| {
                EngineError::new(
                    "ENGINE_UNQUALIFIED",
                    "no acceleration variant for this host",
                )
            })?;
        let c = &variant.compatibility;
        if provenance.engine_digest != c.engine_digest
            || provenance.cpu_baseline != c.cpu_baseline
            || provenance.guest_kernel_digest != c.guest_kernel_digest
            || provenance.device_graph_digest != c.device_graph_digest
        {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "provenance does not bind the variant",
            ));
        }
        fsutil::verify_object(&dir.join(fc::KERNEL), &manifest.kernel, "kernel")?;
        fsutil::verify_object(&dir.join("root.ext4"), &manifest.root, "root")?;
        fsutil::verify_object(&dir.join(fc::ROOTFS), &variant.disk, "variant disk")?;
        fsutil::verify_object(&dir.join(fc::MEM), &variant.memory, "variant memory")?;
        fsutil::verify_object(&dir.join(fc::VMSTATE), &variant.vmstate, "variant state")?;
        Ok(Template {
            id,
            dir: dir.to_path_buf(),
            manifest,
            manifest_digest: digest,
            variant,
        })
    }

    pub fn register_template(&self, t: Template) -> Arc<Template> {
        let t = Arc::new(t);
        self.templates
            .write()
            .unwrap()
            .insert(t.id.clone(), t.clone());
        t
    }

    pub fn unregister_template(&self, id: &str) {
        self.templates.write().unwrap().remove(id);
    }

    /// Resolves a template id or a mutable name to an immutable template.
    pub fn template(&self, reference: &str) -> Result<Arc<Template>> {
        let id = if reference.starts_with("tp_") {
            reference.to_string()
        } else {
            let link = self.cfg.store.join("templates/names").join(reference);
            std::fs::read_link(&link)
                .ok()
                .and_then(|p| p.file_name().map(|n| n.to_string_lossy().into_owned()))
                .ok_or_else(|| EngineError::not_found(format!("template {reference}")))?
        };
        self.templates
            .read()
            .unwrap()
            .get(&id)
            .cloned()
            .ok_or_else(|| EngineError::not_found(format!("template {reference}")))
    }

    pub fn templates(&self) -> Vec<Arc<Template>> {
        self.templates.read().unwrap().values().cloned().collect()
    }

    pub fn sign_provenance(&self, p: &mut Provenance) -> Result<()> {
        p.producer_key_id = manifest::key_id(&self.producer.verifying_key());
        p.sign(&self.producer)
    }

    pub fn alloc_uid(&self) -> Result<u32> {
        let mut used = self.uids.lock().unwrap();
        for i in 0..self.cfg.uid_count {
            let uid = self.cfg.uid_base + i;
            if used.insert(uid) {
                return Ok(uid);
            }
        }
        Err(EngineError::new(
            "NO_CAPACITY",
            "no free sandbox identities",
        ))
    }

    pub fn free_uid(&self, uid: u32) {
        self.uids.lock().unwrap().remove(&uid);
    }

    pub fn new_jail(&self, id: &str) -> Result<Jail> {
        let uid = self.alloc_uid()?;
        Jail::create(
            &self.cfg.store.join("jail"),
            &self.cfg.netns_dir,
            &self.cfg.mem_dir,
            id,
            uid,
        )
        .inspect_err(|_| self.free_uid(uid))
    }

    pub(crate) fn sandbox(&self, id: &str) -> Result<Arc<Mutex<Sandbox>>> {
        self.sandboxes
            .lock()
            .unwrap()
            .get(id)
            .cloned()
            .ok_or_else(|| EngineError::not_found(format!("sandbox {id}")))
    }

    fn check(sb: &Sandbox, auth: Authority<'_>) -> Result<()> {
        if sb.generation != auth.generation {
            return Err(EngineError::new(
                "REVISION_CONFLICT",
                format!(
                    "sandbox {} is at generation {}, request names {}",
                    sb.id, sb.generation, auth.generation
                ),
            ));
        }
        if sb.policy_revision != auth.policy_revision {
            return Err(EngineError::new(
                "REVISION_CONFLICT",
                "policy revision does not match",
            ));
        }
        Ok(())
    }

    /// Reserves identity and a jail. No guest state is touched yet.
    pub fn allocate(&self, auth: Authority<'_>, requested: Option<Resources>) -> Result<Outcome> {
        self.allocate_with(auth, requested, None, &[])
    }

    /// Allocate with the caller's process limit and devices. Capacity is
    /// admitted here, before any jail, disk or VMM exists.
    pub fn allocate_with(
        &self,
        auth: Authority<'_>,
        requested: Option<Resources>,
        pids_max: Option<u64>,
        devices: &[DeviceRequest],
    ) -> Result<Outcome> {
        self.allocate_plan(auth, requested, pids_max, devices, false, "", false)
    }

    /// The same admission used by Allocate, without creating host resources.
    #[allow(clippy::too_many_arguments)]
    pub fn allocate_plan(
        &self,
        auth: Authority<'_>,
        requested: Option<Resources>,
        pids_max: Option<u64>,
        devices: &[DeviceRequest],
        validate_only: bool,
        template_id: &str,
        shared_disk: bool,
    ) -> Result<Outcome> {
        if !valid_id(auth.sandbox_id, "sb_") {
            return Err(EngineError::invalid(
                "sandbox_id must match sb_[a-z0-9_]+ (≤60)",
            ));
        }
        if auth.generation == 0 {
            return Err(EngineError::invalid("generation must be nonzero"));
        }
        let resources = requested.unwrap_or(STANDARD);
        if resources.vcpus == 0
            || resources.vcpus > 32
            || (resources.vcpus > 1 && resources.vcpus % 2 == 1)
        {
            return Err(EngineError::invalid(
                "vcpus must be 1 or an even number ≤ 32",
            ));
        }
        if resources.memory_mib < 128
            || resources
                .memory_mib
                .checked_add(VMM_ALLOWANCE_MIB)
                .is_none()
            || resources.disk_gib == 0
            || resources.disk_gib > 65536
        {
            return Err(EngineError::invalid(
                "memory_mib must be ≥128 and disk_gib 1..=65536",
            ));
        }
        let pids_max = pids_max.unwrap_or(DEFAULT_PIDS_MAX);
        if !(16..=1_048_576).contains(&pids_max) {
            return Err(EngineError::invalid("pids_max must be 16..=1048576"));
        }
        if devices.len() > MAX_EXTRA_DISKS {
            return Err(EngineError::invalid(format!(
                "at most {MAX_EXTRA_DISKS} devices"
            )));
        }
        let mut ids = BTreeSet::new();
        let mut extra_mib = 0u64;
        for d in devices {
            if let Err(why) = device_support(&d.kind) {
                return Err(EngineError::new(
                    "DEVICE_UNAVAILABLE",
                    format!("device {} ({}): {why}", d.id, d.kind),
                ));
            }
            if !valid_device_id(&d.id) || !ids.insert(d.id.clone()) {
                return Err(EngineError::invalid(
                    "device ids must be unique [a-z0-9_]{1,32}",
                ));
            }
            if !(1..=1_048_576).contains(&d.size_mib) {
                return Err(EngineError::invalid("disk size_mib must be 1..=1048576"));
            }
            extra_mib += d.size_mib;
        }
        let mut all = self.sandboxes.lock().unwrap();
        if all.contains_key(auth.sandbox_id) {
            return Err(EngineError::in_use(format!(
                "sandbox {} exists",
                auth.sandbox_id
            )));
        }
        if !template_id.is_empty() {
            let t = self.template(template_id)?;
            self.template_disk(&t)?;
            if resources.disk_gib < t.variant.resources.disk_gib {
                return Err(EngineError::invalid(format!(
                    "template {} needs resources.disk_gib of at least {} GiB",
                    t.id, t.variant.resources.disk_gib
                )));
            }
        }
        self.admit(&all, &resources, extra_mib, shared_disk)?;
        if validate_only {
            return Ok(Outcome {
                state: "validated".into(),
                ..Default::default()
            });
        }
        // A released id that is allocated again (a resumed sandbox) keeps its
        // custody record: its lineage and exposures carry over, never reset.
        self.custody
            .as_ref()
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?
            .lock()
            .unwrap()
            .transact(|graph| {
                if graph.contains(auth.sandbox_id) {
                    Ok(())
                } else {
                    graph.create(auth.sandbox_id)
                }
            })?;
        let jail = self.new_jail(&jail_id(auth.sandbox_id))?;
        let uid = jail.uid;
        // Build the guard network now, in the background: for a fork the
        // caller captures the source next, and the two overlap.
        let pending_net = match &self.cfg.network {
            Some(policy) if self.cfg.subscription.is_none() => {
                let guard_uid = match self.alloc_uid() {
                    Ok(uid) => uid,
                    Err(err) => {
                        jail.destroy();
                        self.free_uid(uid);
                        return Err(err);
                    }
                };
                let (policy, inventory, host) = (
                    policy.clone(),
                    self.inventory.clone(),
                    self.cfg.host_id.clone(),
                );
                let (target, sandbox, generation) = (
                    crate::net::Target::of(&jail),
                    auth.sandbox_id.to_string(),
                    auth.generation,
                );
                Some(std::thread::spawn(move || {
                    let t = Instant::now();
                    let attached = crate::net::Attachment::attach(
                        &policy,
                        &inventory,
                        &target,
                        guard_uid,
                        crate::net::Identity {
                            host_id: &host,
                            sandbox_id: &sandbox,
                            generation,
                            operation_id: "op_restore",
                        },
                    )
                    .map(|mut a| {
                        a.timings
                            .push(("net_build_us".into(), t.elapsed().as_micros() as u64));
                        a
                    });
                    (guard_uid, attached)
                }))
            }
            _ => None,
        };
        all.insert(
            auth.sandbox_id.to_string(),
            Arc::new(Mutex::new(Sandbox {
                id: auth.sandbox_id.into(),
                generation: auth.generation,
                policy_revision: auth.policy_revision,
                resources,
                state: State::Allocated,
                jail: Some(jail),
                uid,
                source: String::new(),
                captures: Vec::new(),
                working: None,
                template_id: String::new(),
                base: Base::Template,
                parent: None,
                barrier: None,
                commands: Arc::default(),
                net: None,
                pending_net,
                network_policy: self.cfg.network.clone(),
                pids_max,
                disks: devices.to_vec(),
                cold: false,
            })),
        );
        Ok(Outcome {
            state: "allocated".into(),
            ..Default::default()
        })
    }

    /// Refuses a sandbox the host cannot hold without overcommitting RAM,
    /// CPUs or store space, counting every sandbox this engine still holds.
    fn admit(
        &self,
        all: &HashMap<String, Arc<Mutex<Sandbox>>>,
        r: &Resources,
        extra_mib: u64,
        shared_disk: bool,
    ) -> Result<()> {
        let host = HostCapacity::detect(&self.cfg.store)?;
        let (mut mem, mut pending, mut cpus, mut disk) = (0u64, 0u64, 0u32, 0u64);
        for sb in all.values() {
            let sb = sb.lock().unwrap();
            if sb.state == State::Stopped {
                continue;
            }
            let charge = sb.resources.memory_mib + VMM_ALLOWANCE_MIB;
            mem += charge;
            if sb.state == State::Allocated {
                // Not yet resident, so not yet visible in MemAvailable.
                pending += charge;
            }
            cpus += sb.resources.vcpus;
            disk += if sb.state == State::Allocated {
                sb.resources.disk_gib
            } else {
                0
            } + sb
                .disks
                .iter()
                .map(|d| d.size_mib)
                .sum::<u64>()
                .div_ceil(1024);
        }
        let charge = r.memory_mib + VMM_ALLOWANCE_MIB;
        // Reflink forks reserve 1 GiB for initial CoW writes. Existing resident
        // disks are already reflected in filesystem free space; only pending
        // allocations need an additional physical reservation.
        let want_disk = if shared_disk { 1 } else { r.disk_gib } + extra_mib.div_ceil(1024);
        let refuse = |what: String| {
            Err(EngineError::new(
                "NO_CAPACITY",
                format!(
                    "{what}; host: {} CPUs, {} MiB physical, {} MiB available, {} MiB reserved for the host, {} GiB store free; held: {cpus} vCPUs, {mem} MiB, {disk} GiB",
                    host.cpus,
                    host.physical_mib,
                    host.available_mib,
                    host.reserve_mib,
                    host.store_free_gib
                ),
            ))
        };
        if charge
            > host
                .physical_mib
                .saturating_sub(host.reserve_mib)
                .saturating_sub(mem)
        {
            return refuse(format!(
                "{} MiB guest + {VMM_ALLOWANCE_MIB} MiB allowance exceeds unreserved physical memory",
                r.memory_mib
            ));
        }
        if charge
            > host
                .available_mib
                .saturating_sub(host.reserve_mib)
                .saturating_sub(pending)
        {
            return refuse(format!(
                "{} MiB guest + {VMM_ALLOWANCE_MIB} MiB allowance exceeds memory available now",
                r.memory_mib
            ));
        }
        if cpus + r.vcpus > host.cpus {
            return refuse(format!(
                "{} vCPUs would overcommit the host's CPUs",
                r.vcpus
            ));
        }
        if want_disk > host.store_free_gib.saturating_sub(disk) {
            return refuse(format!(
                "{want_disk} GiB of disk exceeds the store's free space"
            ));
        }
        Ok(())
    }

    /// Restores a template variant, a memory capture or a durable capture
    /// into an allocated sandbox and applies the identity barrier before
    /// any command. A durable capture is verified (enrolled producer,
    /// signature, compatibility, object digests) before anything parses it,
    /// and may be restored any number of times.
    pub fn restore(&self, auth: Authority<'_>, capture_id: &str) -> Result<Outcome> {
        self.restore_with_registration(auth, capture_id, true)
    }

    /// Only the builder's fresh-template boot probe bypasses login registration.
    pub(crate) fn restore_build_probe(
        &self,
        auth: Authority<'_>,
        template_id: &str,
    ) -> Result<Outcome> {
        self.restore_with_registration(auth, template_id, false)
    }

    fn restore_with_registration(
        &self,
        auth: Authority<'_>,
        capture_id: &str,
        register_subscription: bool,
    ) -> Result<Outcome> {
        let t0 = Instant::now();
        let sb = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb.lock().unwrap();
        Self::check(&sb, auth)?;
        if !register_subscription && sb.state != State::Allocated {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "a builder probe requires a newly allocated sandbox",
            ));
        }
        if sb.state == State::Stopped {
            let source = self
                .captures
                .lock()
                .unwrap()
                .get(capture_id)
                .cloned()
                .ok_or_else(|| EngineError::not_found("resume capture"))?;
            if source.source_sandbox != sb.id || source.source_generation != sb.generation {
                return Err(EngineError::new(
                    "SCOPE_DENIED",
                    "resume capture belongs to another execution",
                ));
            }
            self.custody
                .as_ref()
                .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?
                .lock()
                .unwrap()
                .graph()?
                .authorize(
                    capture_id,
                    crate::custody::Destination {
                        principal: self.custody_owner(),
                        host: &self.cfg.host_id,
                        online: true,
                    },
                    crate::custody::CapturePurpose::OwnerPrivate,
                )?;
            if let Some(uid) = sb.teardown() {
                self.free_uid(uid);
            }
            self.free_uid(sb.uid);
            let jail = self.new_jail(&jail_id(&sb.id))?;
            sb.uid = jail.uid;
            sb.jail = Some(jail);
            sb.network_policy = self.cfg.network.clone();
            sb.state = State::Allocated;
        }
        if sb.state != State::Allocated {
            return Err(EngineError::in_use(format!(
                "sandbox is {}, not allocated",
                sb.state.name()
            )));
        }
        // A durable capture's tmpfs image serves its first restore only.
        let capture = self
            .captures
            .lock()
            .unwrap()
            .get(capture_id)
            .filter(|c| *c.restores.lock().unwrap() == CAPTURE_AVAILABLE || !c.durable)
            .cloned();
        let durable = self.state.get(capture_id);
        let is_capture = capture.is_some() || durable.is_some();
        if !register_subscription && is_capture {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "a builder probe must restore a fresh template, not a capture",
            ));
        }
        self.prepare_subscription_restore(&mut sb, capture_id, is_capture, register_subscription)?;
        let (source, resources) = match &capture {
            Some(c) => (Source::Capture(c.clone()), c.resources.clone()),
            None => match durable {
                Some(e) => {
                    let r = e.manifest.resources.clone();
                    (Source::Durable(Box::new(e)), r)
                }
                None => {
                    let t = self.template(capture_id)?;
                    let disk = self.template_disk(&t)?;
                    // The prepared image fixes CPUs, RAM and devices. Any
                    // other shape the caller chose boots the same kernel
                    // and disk cold, at exactly that shape.
                    let prepared = t.variant.resources.clone();
                    let cold = (sb.resources.vcpus != prepared.vcpus
                        || sb.resources.memory_mib != prepared.memory_mib
                        || sb.resources.disk_gib < prepared.disk_gib
                        || sb.resources.disk_gib > prepared.disk_gib
                        || !sb.disks.is_empty())
                    .then(|| (t.dir.join(fc::KERNEL), t.manifest.kernel_cmdline.clone()));
                    if sb.resources.disk_gib < prepared.disk_gib {
                        return Err(EngineError::invalid(format!(
                            "template {} needs a disk of at least {} GiB",
                            t.id, prepared.disk_gib
                        )));
                    }
                    let shape = if cold.is_some() {
                        sb.resources.clone()
                    } else {
                        prepared
                    };
                    (
                        Source::Template {
                            mem: t.dir.join(fc::MEM),
                            vmstate: t.dir.join(fc::VMSTATE),
                            disk,
                            template_id: t.id.clone(),
                            cold,
                        },
                        shape,
                    )
                }
            },
        };
        if sb.resources != resources {
            return Err(EngineError::new(
                "ENGINE_UNQUALIFIED",
                format!(
                    "saved state has {}/{} MiB/{} GiB; request asked {}/{} MiB/{} GiB",
                    resources.vcpus,
                    resources.memory_mib,
                    resources.disk_gib,
                    sb.resources.vcpus,
                    sb.resources.memory_mib,
                    sb.resources.disk_gib
                ),
            ));
        }
        if let Some(c) = &capture {
            // A capture image is restored at most once; claim it first.
            let mut n = c.restores.lock().unwrap();
            if *n != CAPTURE_AVAILABLE {
                return Err(EngineError::not_found(format!(
                    "capture {capture_id} was already restored"
                )));
            }
            *n = CAPTURE_RESTORING;
        }
        match self.restore_into(&mut sb, source, t0) {
            Ok(mut out) => {
                sb.source = capture_id.to_string();
                if let Some(c) = &capture {
                    *c.restores.lock().unwrap() = CAPTURE_CONSUMED;
                    drop(sb);
                    self.gc_captures();
                }
                out.detail.insert("total_us".into(), us(t0.elapsed()));
                Ok(out)
            }
            Err(e) => {
                if let Some(j) = sb.jail.as_mut() {
                    j.kill();
                }
                let w = sb.working.take();
                self.release_disk(w);
                if let Some(c) = &capture {
                    // Hand the image back to root and allow another attempt.
                    let _ = fsutil::chown(&c.mem, 0, 0);
                    let _ = fsutil::chown(&c.vmstate, 0, 0);
                    *c.restores.lock().unwrap() = CAPTURE_AVAILABLE;
                }
                sb.state = State::Failed(e.message.clone());
                Err(e)
            }
        }
    }

    /// Checks a durable capture end to end before its bytes are used.
    pub fn verify_durable(&self, m: &durable::CaptureManifest) -> Result<()> {
        m.verify(&self.trusted)?;
        if m.engine_digest != self.compat.engine_digest
            || m.cpu_baseline != self.compat.cpu_baseline
            || m.state_abi != STATE_ABI
        {
            return Err(EngineError::new(
                "ENGINE_UNQUALIFIED",
                format!(
                    "capture {} was produced for another engine/CPU",
                    m.capture_id
                ),
            ));
        }
        if let Err(e) = self.state.verify_objects(m) {
            self.state.quarantine(&m.capture_id, &e.message);
            return Err(e);
        }
        Ok(())
    }

    /// Memory-base links below durable capture `cid` (0: against the template).
    fn chain_depth(&self, cid: &str) -> usize {
        let mut depth = 0;
        let mut at = self
            .state
            .get(cid)
            .and_then(|e| e.manifest.memory.base.clone());
        while let Some(b) = at {
            depth += 1;
            if depth > MAX_CHAIN {
                break;
            }
            at = self
                .state
                .get(&b)
                .and_then(|e| e.manifest.memory.base.clone());
        }
        depth
    }

    /// The image a template-based delta is encoded against: the template's
    /// prepared memory when the sizes match, otherwise (a cold-booted
    /// sandbox of another size) an all-zero sparse image of that size.
    fn template_base(&self, t: &Template, bytes: u64) -> Result<PathBuf> {
        let mem = t.dir.join(fc::MEM);
        if std::fs::metadata(&mem).ctx("stat template memory")?.len() == bytes {
            return Ok(mem);
        }
        let zero = self.cfg.mem_dir.join(format!("zero-{bytes}.mem"));
        if std::fs::metadata(&zero).ok().map(|m| m.len()) != Some(bytes) {
            let tmp = zero.with_extension("tmp");
            let f = File::create(&tmp).ctx("create zero base")?;
            f.set_len(bytes).ctx("size zero base")?;
            fsutil::chmod(&tmp, 0o400)?;
            std::fs::rename(&tmp, &zero).ctx("publish zero base")?;
        }
        Ok(zero)
    }

    /// Writes the full memory image of durable capture `cid` to `out`.
    fn materialize(&self, cid: &str, out: &Path, scratch: &Path, depth: usize) -> Result<()> {
        if depth > MAX_CHAIN {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "capture chain too deep",
            ));
        }
        let e = self.state.get(cid).ok_or_else(|| {
            EngineError::new("DATA_UNAVAILABLE", format!("capture {cid} is gone"))
        })?;
        let m = &e.manifest;
        self.verify_durable(m)?;
        let delta = self.state.object(&m.memory.object);
        match &m.memory.base {
            None => {
                let t = self.template(&m.template_id)?;
                let h = memimg::read_header(&File::open(&delta).ctx("open memory object")?)?;
                memimg::materialize(&delta, &self.template_base(&t, h.image_bytes)?, out)
            }
            Some(b) => {
                let tmp = scratch.join(format!("base-{b}.mem"));
                let _ = std::fs::remove_file(&tmp);
                self.materialize(b, &tmp, scratch, depth + 1)?;
                let r = memimg::materialize(&delta, &tmp, out);
                let _ = std::fs::remove_file(&tmp);
                r
            }
        }
    }

    fn restore_into(&self, sb: &mut Sandbox, source: Source, t0: Instant) -> Result<Outcome> {
        let limits = Limits {
            vcpus: sb.resources.vcpus,
            memory_mib: sb.resources.memory_mib,
            pids_max: DEFAULT_PIDS_MAX,
        };
        let cold = match &source {
            Source::Template { cold, .. } => cold.clone(),
            _ => None,
        };
        let mut detail = BTreeMap::new();
        let (disk_src, template_id, base, parent) = match &source {
            Source::Template {
                disk, template_id, ..
            } => (disk.clone(), template_id.clone(), Base::Template, None),
            Source::Capture(c) => (
                c.disk.clone(),
                c.template_id.clone(),
                // A durable capture's image is the full image of a durable
                // generation; a volatile one's is not durable anywhere.
                if c.durable {
                    Base::Capture(c.id.clone())
                } else {
                    match &c.base {
                        Base::Template => Base::Template,
                        Base::Capture(_) => Base::Template,
                    }
                },
                if c.durable {
                    Some(c.id.clone())
                } else {
                    c.parent.clone()
                },
            ),
            Source::Durable(e) => (
                e.manifest.disk_ref()?,
                e.manifest.template_id.clone(),
                Base::Capture(e.manifest.capture_id.clone()),
                Some(e.manifest.capture_id.clone()),
            ),
        };
        let disk_bytes = sb.resources.disk_gib << 30;
        let jail = sb
            .jail
            .as_ref()
            .ok_or_else(|| EngineError::in_use("sandbox has no jail"))?;
        let uid = jail.uid;
        let mem_path = match &source {
            Source::Template { .. } if cold.is_some() => String::new(),
            Source::Template { mem, vmstate, .. } => {
                // Prepared-template create: the kernel maps the immutable,
                // public template image privately.
                std::fs::hard_link(mem, jail.path(fc::MEM)).ctx("link memory image")?;
                std::fs::hard_link(vmstate, jail.memdir.join(fc::VMSTATE))
                    .or_else(|_| std::fs::copy(vmstate, jail.memdir.join(fc::VMSTATE)).map(|_| ()))
                    .ctx("place vm state")?;
                format!("/{}", fc::MEM)
            }
            Source::Capture(c) => {
                // The private image becomes this sandbox's own: it is linked
                // into its 0700 memdir and owned by its UID alone. The
                // capture is consumed, so no other identity can read it.
                for (from, to) in [(&c.mem, SRC_MEM), (&c.vmstate, fc::VMSTATE)] {
                    let dst = jail.memdir.join(to);
                    std::fs::hard_link(from, &dst).ctx("link capture into jail")?;
                    fsutil::chown(&dst, uid, uid)?;
                    fsutil::chmod(&dst, 0o400)?;
                }
                format!("/{}/{SRC_MEM}", fc::MEMDIR)
            }
            Source::Durable(e) => {
                // Verified before any byte is used, then rebuilt privately
                // in this sandbox's own memdir.
                let t = Instant::now();
                let m = &e.manifest;
                let src = jail.memdir.join(SRC_MEM);
                self.materialize(&m.capture_id, &src, &jail.memdir, 0)?;
                let state = jail.memdir.join(fc::VMSTATE);
                std::fs::copy(self.state.object(&m.vmstate.object), &state)
                    .ctx("place vm state")?;
                if durable::sha256_file(&state)? != m.vmstate.sha256 {
                    return Err(EngineError::new("DATA_UNAVAILABLE", "vm state changed"));
                }
                for p in [&src, &state] {
                    fsutil::chown(p, uid, uid)?;
                    fsutil::chmod(p, 0o400)?;
                }
                detail.insert("materialize_us".into(), us(t.elapsed()));
                format!("/{}/{SRC_MEM}", fc::MEMDIR)
            }
        };
        let t = Instant::now();
        if let Some((kernel, _)) = &cold {
            // Extra disks are raw, sparse and private to this sandbox's UID.
            for d in &sb.disks {
                let path = jail.path(&format!("disk-{}.img", d.id));
                let f = std::fs::OpenOptions::new()
                    .write(true)
                    .create_new(true)
                    .open(&path)
                    .ctx("create extra disk")?;
                f.set_len(d.size_mib << 20).ctx("size extra disk")?;
                fsutil::chown(&path, uid, uid)?;
                fsutil::chmod(&path, 0o600)?;
            }
            std::fs::hard_link(kernel, jail.path(fc::KERNEL))
                .or_else(|_| std::fs::copy(kernel, jail.path(fc::KERNEL)).map(|_| ()))
                .ctx("place kernel")?;
        }
        let working = self.attach_disk(&disk_src, jail, disk_bytes)?;
        if cold.is_some() {
            // Reflinked disks keep the template's size; grow the file to the
            // caller's disk_gib (the guest sees a larger /dev/vda). Thin
            // volumes were activated at that size already.
            let grown = (|| -> Result<()> {
                let path = jail.path(fc::ROOTFS);
                let meta = std::fs::metadata(&path).ctx("stat working disk")?;
                if meta.is_file() && meta.len() < disk_bytes {
                    std::fs::OpenOptions::new()
                        .write(true)
                        .open(&path)
                        .and_then(|f| f.set_len(disk_bytes))
                        .ctx("grow working disk")?;
                }
                crate::build::resize_ext4(&path)?;
                Ok(())
            })();
            if let Err(e) = grown {
                self.release_disk(Some(working));
                return Err(e);
            }
        }
        detail.insert("disk_attach_us".into(), us(t.elapsed()));
        sb.working = Some(working);
        sb.template_id = template_id;
        sb.base = base;
        sb.parent = parent;
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        let t = Instant::now();
        if sb.net.is_none() {
            match &sb.network_policy {
                Some(policy) => {
                    let (guard_uid, attached) = match sb.pending_net.take() {
                        Some(pending) => pending
                            .join()
                            .map_err(|_| EngineError::host("network attachment thread panicked"))?,
                        None => {
                            let guard_uid = self.alloc_uid()?;
                            let attached = crate::net::Attachment::attach(
                                policy,
                                &self.inventory,
                                &crate::net::Target::of(jail),
                                guard_uid,
                                crate::net::Identity {
                                    host_id: &self.cfg.host_id,
                                    sandbox_id: &sb.id,
                                    generation: sb.generation,
                                    operation_id: "op_restore",
                                },
                            );
                            (guard_uid, attached)
                        }
                    };
                    match attached {
                        Ok(a) => {
                            for (k, v) in &a.timings {
                                detail.insert(k.clone(), v.to_string());
                            }
                            sb.net = Some(a);
                        }
                        Err(e) => {
                            self.free_uid(guard_uid);
                            return Err(e);
                        }
                    }
                }
                None => crate::net::tap_only(jail)?,
            }
        }
        detail.insert("attach_us".into(), us(t.elapsed()));
        let jail = sb.jail.as_mut().unwrap();
        let t = Instant::now();
        jail.spawn(&self.cfg.bins, limits)?;
        detail.insert("spawn_us".into(), us(t.elapsed()));
        let t = Instant::now();
        // vCPUs resume here, but every task process was frozen before the
        // capture (and a template has none): only init and the helper run
        // until the barrier below thaws the tasks with the new identity.
        // Firecracker refuses clock_realtime on aarch64 ("not supported");
        // there the guest clock is set after the identity barrier instead.
        let clock = if cfg!(target_arch = "x86_64") {
            r#","clock_realtime":true"#
        } else {
            ""
        };
        let body = format!(
            r#"{{"snapshot_path":"/{}/{}","mem_backend":{{"backend_type":"File","backend_path":{}}},"track_dirty_pages":false,"resume_vm":true{}}}"#,
            fc::MEMDIR,
            fc::VMSTATE,
            fc::js(&mem_path),
            clock
        );
        match &cold {
            None => {
                jail.api("PUT", "/snapshot/load", &body)?;
            }
            Some((_, cmdline)) => {
                self.cold_boot(jail, cmdline, &sb.resources, &sb.disks)?;
                detail.insert("boot".into(), "cold".into());
            }
        }
        detail.insert("load_us".into(), us(t.elapsed()));
        let t = Instant::now();
        let vsock = jail.path(fc::VSOCK);
        let ack = self.barrier(&vsock, &sb.id, sb.generation)?;
        detail.insert(
            "audit".into(),
            if sb.net.is_some() {
                "collector"
            } else {
                "none"
            }
            .into(),
        );
        detail.insert("barrier_us".into(), us(t.elapsed()));
        if let Err(e) =
            apply_guest_limits(&vsock, &sb.id, sb.generation, sb.pids_max, sb.disks.len())
        {
            return Err(EngineError::host(format!(
                "apply guest process limit: {}",
                e.message
            )));
        }
        detail.insert("pids_max".into(), sb.pids_max.to_string());
        detail.insert("rng_sample".into(), ack.rng_sample_hex.clone());
        detail.insert("machine_id".into(), ack.machine_id.clone());
        sb.barrier = Some(ack);
        sb.cold = cold.is_some();
        sb.state = State::Running;
        detail.insert("capture_trust".into(), self.capture_trust(&sb.id)?.into());
        detail.insert(
            "custody_host_ids".into(),
            serde_json::to_string(&self.cfg.subscription.as_ref().map_or_else(
                || vec![self.cfg.host_id.clone()],
                |policy| policy.custody_host_ids.clone(),
            ))
            .unwrap(),
        );
        Ok(Outcome {
            state: "restored".into(),
            detail,
            ..Default::default()
        })
    }

    /// Boots the template's kernel and working disk at the sandbox's own
    /// CPU, memory and device shape, then waits for the guest helper.
    fn cold_boot(
        &self,
        jail: &Jail,
        cmdline: &str,
        r: &Resources,
        disks: &[DeviceRequest],
    ) -> Result<()> {
        jail.api(
            "PUT",
            "/boot-source",
            &format!(
                r#"{{"kernel_image_path":"/{}","boot_args":{}}}"#,
                fc::KERNEL,
                fc::js(cmdline)
            ),
        )?;
        jail.api(
            "PUT",
            "/drives/rootfs",
            &format!(r#"{{"drive_id":"rootfs","path_on_host":"/{}","is_root_device":true,"is_read_only":false,"io_engine":"Async"}}"#, fc::ROOTFS),
        )?;
        for d in disks {
            jail.api(
                "PUT",
                &format!("/drives/x_{}", d.id),
                &format!(
                    r#"{{"drive_id":"x_{}","path_on_host":"/disk-{}.img","is_root_device":false,"is_read_only":false,"io_engine":"Async"}}"#,
                    d.id, d.id
                ),
            )?;
        }
        jail.api(
            "PUT",
            "/machine-config",
            &format!(
                r#"{{"vcpu_count":{},"mem_size_mib":{},"track_dirty_pages":false}}"#,
                r.vcpus, r.memory_mib
            ),
        )?;
        jail.api(
            "PUT",
            "/vsock",
            &format!(r#"{{"guest_cid":3,"uds_path":"/{}"}}"#, fc::VSOCK),
        )?;
        jail.api(
            "PUT",
            "/network-interfaces/eth0",
            &format!(
                r#"{{"iface_id":"eth0","guest_mac":"{}","host_dev_name":"{}"}}"#,
                lamp_init::GUEST_MAC,
                crate::net::TAP
            ),
        )?;
        jail.api("PUT", "/actions", r#"{"action_type":"InstanceStart"}"#)?;
        guest::wait_ready(&jail.path(fc::VSOCK), Duration::from_secs(60)).map(|_| ())
    }

    /// New identity, entropy and clock; refuses old identities afterwards.
    pub fn barrier(
        &self,
        vsock: &Path,
        sandbox_id: &str,
        generation: u64,
    ) -> Result<lamp_init::BarrierAck> {
        let barrier = Barrier {
            sandbox_id: sandbox_id.into(),
            generation,
            hostname: sandbox_id.replace('_', "-"),
            machine_id: lamp_init::hex_encode(&random_bytes::<16>()),
            entropy_hex: lamp_init::hex_encode(&random_bytes::<64>()),
            unix_time_ns: now_ns(),
        };
        let reply = guest::call(
            vsock,
            &GuestRequest::Barrier(barrier.clone()),
            Duration::from_secs(10),
        )
        .map_err(|e| e.context("identity barrier"))?;
        match reply {
            GuestResponse::BarrierAck(ack)
                if ack.sandbox_id == barrier.sandbox_id
                    && ack.generation == generation
                    && ack.machine_id == barrier.machine_id =>
            {
                Ok(ack)
            }
            GuestResponse::Error { code, message } => Err(EngineError::host(format!(
                "identity barrier refused: {code}: {message}"
            ))),
            other => Err(EngineError::host(format!(
                "identity barrier: unexpected reply {other:?}"
            ))),
        }
    }

    pub fn network_call(&self, auth: Authority<'_>, request: Vec<u8>) -> Result<Outcome> {
        let sandbox = self.sandbox(auth.sandbox_id)?;
        let mut sb = sandbox.lock().unwrap();
        Self::check(&sb, auth)?;
        if sb.state == State::Allocated && sb.net.is_none() {
            if let Some(pending) = sb.pending_net.take() {
                let (uid, attached) = pending
                    .join()
                    .map_err(|_| EngineError::host("network attachment thread panicked"))?;
                match attached {
                    Ok(a) => sb.net = Some(a),
                    Err(e) => {
                        self.free_uid(uid);
                        return Err(e);
                    }
                }
            }
        }
        if sb.state != State::Running && sb.state != State::Allocated {
            return Err(EngineError::in_use(
                "sandbox has no active network generation",
            ));
        }
        let net = sb.net.as_mut().ok_or_else(|| {
            EngineError::new("ENGINE_UNQUALIFIED", "sandbox has no managed network")
        })?;
        let data = net.call(&request)?;
        Ok(Outcome {
            state: "network_reply".into(),
            handles: vec![],
            command: None,
            detail: BTreeMap::from([("response_json".into(), data.to_string())]),
        })
    }

    pub fn session_call(
        &self,
        auth: Authority<'_>,
        request_json: Vec<u8>,
        bridge_source: String,
    ) -> Result<Outcome> {
        if request_json.len() > 131072 || bridge_source.is_empty() {
            return Err(EngineError::invalid("invalid session relay request"));
        }
        if self.capture_trust(auth.sandbox_id)? == "owner_private" {
            return Err(EngineError::new(
                "AUTH_TRANSPORT_UNSUPPORTED",
                "private native custody requires its protected continuation adapter",
            ));
        }
        let (vsock, audited) = {
            let sb = self.sandbox(auth.sandbox_id)?;
            let sb = sb.lock().unwrap();
            Self::check(&sb, auth)?;
            if sb.state != State::Running {
                return Err(EngineError::in_use("session sandbox is not running"));
            }
            (sb.jail.as_ref().unwrap().path(fc::VSOCK), sb.net.is_some())
        };
        let request = GuestRequest::SessionCall(lamp_init::SessionCall {
            sandbox_id: auth.sandbox_id.into(),
            generation: auth.generation,
            request_json: String::from_utf8(request_json)
                .map_err(|_| EngineError::invalid("session request is not UTF-8"))?,
            bridge_source,
            audit: audited.then_some(lamp_init::Audit {
                port: lamp_init::AUDIT_PORT,
                task_uid: crate::net::TASK_UID,
            }),
        });
        match guest::call(&vsock, &request, Duration::from_secs(10))? {
            GuestResponse::SessionReply { response_json } => Ok(Outcome {
                state: "session_reply".into(),
                handles: vec![],
                command: None,
                detail: BTreeMap::from([("response_json".into(), response_json)]),
            }),
            GuestResponse::Error { code, message } => Err(EngineError::new(
                match code.as_str() {
                    "GENERATION_MISMATCH" => "REVISION_CONFLICT",
                    "INVALID_ARGUMENT" => "INVALID_ARGUMENT",
                    _ => "DATA_UNAVAILABLE",
                },
                message,
            )),
            _ => Err(EngineError::host("unexpected session relay response")),
        }
    }

    /// Starts one command and returns once the guest has witnessed its exec
    /// (`command_started`); completion is observed separately (`command`).
    /// The sandbox lock is not held while the command runs, so a fork can
    /// capture a sandbox in the middle of a command.
    pub fn start(
        &self,
        auth: Authority<'_>,
        operation_id: &str,
        argv: Vec<String>,
        cwd: String,
        env: BTreeMap<String, String>,
        timeout_ms: u64,
    ) -> Result<Outcome> {
        self.start_io(
            auth,
            operation_id,
            argv,
            cwd,
            env,
            timeout_ms,
            String::new(),
            false,
            false,
        )
    }

    #[allow(clippy::too_many_arguments)]
    pub fn start_io(
        &self,
        auth: Authority<'_>,
        operation_id: &str,
        argv: Vec<String>,
        cwd: String,
        env: BTreeMap<String, String>,
        timeout_ms: u64,
        stdin_path: String,
        pty: bool,
        stdin_open: bool,
    ) -> Result<Outcome> {
        if argv.is_empty() {
            return Err(EngineError::invalid("argv is empty"));
        }
        self.authorize_subscription_command(auth.sandbox_id, &argv, &cwd, &env)?;
        let (vsock, commands, vmm_alive, audited, subscription_run) = {
            let sb = self.sandbox(auth.sandbox_id)?;
            let sb = sb.lock().unwrap();
            Self::check(&sb, auth)?;
            if sb.state != State::Running {
                return Err(EngineError::in_use(format!(
                    "sandbox is {}",
                    sb.state.name()
                )));
            }
            if running(&sb.commands.lock().unwrap()) >= MAX_RUNNING {
                return Err(EngineError::new(
                    "NO_CAPACITY",
                    format!("{MAX_RUNNING} commands are already running in this sandbox"),
                ));
            }
            let subscription_run = if self.capture_trust(auth.sandbox_id)? == "owner_private" {
                crate::subscription::native_run(&argv)
            } else {
                None
            };
            if let Some(sign_in) = subscription_run {
                self.custody
                    .as_ref()
                    .unwrap()
                    .lock()
                    .unwrap()
                    .transact(|graph| graph.begin_run(auth.sandbox_id, operation_id, sign_in))?;
            }
            let jail = sb.jail.as_ref().unwrap();
            (
                jail.path(fc::VSOCK),
                sb.commands.clone(),
                jail.liveness(),
                sb.net.is_some(),
                subscription_run.is_some(),
            )
        };
        let request = GuestRequest::Exec(Exec {
            sandbox_id: auth.sandbox_id.into(),
            generation: auth.generation,
            exec_id: operation_id.into(),
            argv,
            env,
            cwd,
            timeout_ms,
            output_limit: lamp_init::DEFAULT_OUTPUT_LIMIT,
            stdin_path,
            pty,
            stdin_open,
            audit: audited.then_some(lamp_init::Audit {
                port: lamp_init::AUDIT_PORT,
                task_uid: crate::net::TASK_UID,
            }),
        });
        let admission = (|| {
            let mut conn = guest::send(&vsock, &request)?;
            match conn.next(Some(Duration::from_secs(10)))? {
                GuestResponse::ExecStarted(s) => Ok(s),
                GuestResponse::Error { code, message } if code == "GENERATION_MISMATCH" => {
                    Err(EngineError::new("REVISION_CONFLICT", message))
                }
                // Refused before exec (spawn failure): nothing ran.
                GuestResponse::Error { code, message } => {
                    Err(EngineError::invalid(format!("{code}: {message}")))
                }
                other => Err(EngineError::new(
                    "OUTCOME_UNKNOWN",
                    format!("exec: unexpected reply {other:?}"),
                )),
            }
        })();
        let started = match admission {
            Ok(started) => started,
            Err(error) => {
                if subscription_run {
                    self.custody
                        .as_ref()
                        .unwrap()
                        .lock()
                        .unwrap()
                        .transact(|graph| {
                            graph.finish_run(auth.sandbox_id, operation_id, false, false)
                        })?;
                }
                return Err(error);
            }
        };
        remember(
            &mut commands.lock().unwrap(),
            operation_id,
            CommandRec {
                started: started.clone(),
                done: None,
                lost: None,
            },
        );
        let op = operation_id.to_string();
        let (sandbox_id, generation) = (auth.sandbox_id.to_string(), auth.generation);
        std::thread::spawn(move || {
            // Completion is fetched with short idempotent waits, so a capture
            // that resets the guest's connections mid-command loses nothing.
            // The waiter ends as soon as this VMM process is gone (stop,
            // release or crash): the outcome is then unknown, never running.
            let result = guest::wait_exec(&vsock, &sandbox_id, generation, &op, None, vmm_alive);
            let mut table = commands.lock().unwrap();
            if let Some(rec) = table.0.get_mut(&op) {
                match result {
                    Ok(d) => rec.done = Some(d),
                    Err(e) => rec.lost = Some(e.message),
                }
            }
            forget_old_completed(&mut table);
        });
        Ok(Outcome {
            state: "command_started".into(),
            command: Some(pending(&started)),
            detail: BTreeMap::from([("guest_pid".into(), started.pid.to_string())]),
            ..Default::default()
        })
    }

    /// The state of a command started on this sandbox.

    pub fn exec_io(&self, auth: Authority<'_>, bytes: Vec<u8>) -> Result<Outcome> {
        let mut request: lamp_init::ExecIo =
            serde_json::from_slice(&bytes).map_err(|e| EngineError::invalid(e.to_string()))?;
        request.sandbox_id = auth.sandbox_id.into();
        request.generation = auth.generation;
        let vsock = {
            let sb = self.sandbox(auth.sandbox_id)?;
            let sb = sb.lock().unwrap();
            Self::check(&sb, auth)?;
            sb.jail
                .as_ref()
                .ok_or_else(|| EngineError::host("sandbox is not running"))?
                .path(fc::VSOCK)
        };
        match guest::call(
            &vsock,
            &GuestRequest::ExecIo(request),
            Duration::from_secs(5),
        )? {
            GuestResponse::SessionReply { response_json } => Ok(Outcome {
                state: "session_reply".into(),
                detail: BTreeMap::from([("response_json".into(), response_json)]),
                ..Default::default()
            }),
            GuestResponse::Error { code, message } => Err(EngineError::new(
                if code == "GENERATION_MISMATCH" {
                    "REVISION_CONFLICT"
                } else {
                    "DATA_UNAVAILABLE"
                },
                message,
            )),
            _ => Err(EngineError::host("unexpected command I/O reply")),
        }
    }
    pub fn command(&self, sandbox_id: &str, operation_id: &str) -> Result<CommandRec> {
        let commands = self.sandbox(sandbox_id)?.lock().unwrap().commands.clone();
        let table = commands.lock().unwrap();
        table
            .0
            .get(operation_id)
            .cloned()
            .ok_or_else(|| EngineError::not_found(format!("command {operation_id}")))
    }

    /// Starts a command and waits for it to exit (template probes).
    pub fn run(
        &self,
        auth: Authority<'_>,
        argv: Vec<String>,
        timeout: Duration,
    ) -> Result<ExecDone> {
        let op = format!("op_run_{}", lamp_init::hex_encode(&random_bytes::<8>()));
        self.start(
            auth,
            &op,
            argv,
            String::new(),
            BTreeMap::new(),
            timeout.as_millis() as u64,
        )?;
        let deadline = Instant::now() + timeout + Duration::from_secs(5);
        loop {
            let rec = self.command(auth.sandbox_id, &op)?;
            if let Some(d) = rec.done {
                return Ok(d);
            }
            if let Some(why) = rec.lost {
                return Err(EngineError::new("OUTCOME_UNKNOWN", why));
            }
            if Instant::now() > deadline {
                return Err(EngineError::host("command did not finish"));
            }
            std::thread::sleep(Duration::from_millis(2));
        }
    }

    /// Memory capture of a running sandbox at one RAM/CPU/device/disk
    /// generation: task processes are frozen, the VM paused, **all** of its
    /// configured RAM written by a Firecracker Full snapshot (to RAM-backed
    /// tmpfs) and the disk head sealed by reflink; then the source resumes
    /// and its tasks thaw. Every step is inside the timed fork.
    pub fn capture(
        self: &Arc<Self>,
        auth: Authority<'_>,
        capture_id: &str,
        consistency: &str,
    ) -> Result<Outcome> {
        let t0 = Instant::now();
        // Capture IDs are opaque to callers; the prefixes keep them disjoint
        // from template IDs and names, which Restore also resolves.
        if !valid_id(capture_id, "cp_") && !valid_id(capture_id, "capture_") {
            return Err(EngineError::invalid(
                "capture_id must match (cp_|capture_)[a-z0-9_]+ (≤60)",
            ));
        }
        let durable = match consistency {
            "" | "crash" | "volatile" => false,
            "local" => true,
            other => {
                return Err(EngineError::invalid(format!(
                    "unsupported consistency {other:?}"
                )));
            }
        };
        let sb_arc = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb_arc.lock().unwrap();
        Self::check(&sb, auth)?;
        if sb.state != State::Running {
            return Err(EngineError::in_use(format!(
                "sandbox is {}",
                sb.state.name()
            )));
        }
        if !sb.disks.is_empty() {
            return Err(EngineError::new(
                "DEVICE_UNAVAILABLE",
                "captures do not carry extra disks; fork, suspend or save a sandbox without them",
            ));
        }
        self.custody
            .as_ref()
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?
            .lock()
            .unwrap()
            .transact(|graph| {
                graph.derive(
                    &sb.id,
                    capture_id,
                    crate::custody::Destination {
                        principal: self.custody_owner(),
                        host: &self.cfg.host_id,
                        online: true,
                    },
                    crate::custody::CapturePurpose::OwnerPrivate,
                )
            })?;
        let cdir = self.cfg.store.join("captures").join(capture_id);
        if self.captures.lock().unwrap().contains_key(capture_id)
            || cdir.exists()
            || self.state.exists(capture_id)
        {
            return Err(EngineError::in_use(format!("capture {capture_id} exists")));
        }
        std::fs::create_dir(&cdir).ctx("create capture dir")?;
        fsutil::chmod(&cdir, 0o700)?;
        let (mem_name, vmstate_name, disk_name) = durable::object_names(capture_id);
        let sealed = match &self.disk {
            disk::Backend::Reflink if durable => {
                DiskRef::File(format!("state/objects/{disk_name}"))
            }
            disk::Backend::Reflink => {
                DiskRef::File(format!("captures/{capture_id}/{}", fc::ROOTFS))
            }
            disk::Backend::Thin(pool) => DiskRef::Thin(pool.new_id()),
        };
        let mut detail = BTreeMap::new();
        detail.insert("disk_backend".into(), self.disk.name().into());
        if durable {
            let mut objects = vec![mem_name.clone(), vmstate_name.clone()];
            if matches!(sealed, DiskRef::File(_)) {
                objects.push(disk_name.clone());
            }
            let thin: Vec<u32> = match &sealed {
                DiskRef::Thin(id) => vec![*id],
                DiskRef::File(_) => Vec::new(),
            };
            let t = Instant::now();
            if let Err(e) = fault::step("reserve", || self.state.reserve(capture_id, objects, thin))
            {
                let _ = std::fs::remove_dir_all(&cdir);
                if let (DiskRef::Thin(id), Some(pool)) = (&sealed, self.disk.thin()) {
                    pool.forget(*id);
                }
                return Err(e.context("capture"));
            }
            detail.insert("reserve_us".into(), us(t.elapsed()));
        }
        // Where the sealed disk is cloned to (reflink: a temporary name
        // for a durable object until directory_flush).
        let seal_path = match &sealed {
            DiskRef::File(p) if durable => Some(self.disk_path(&format!("{p}.tmp"))),
            DiskRef::File(p) => Some(self.disk_path(p)),
            DiskRef::Thin(_) => None,
        };
        let jail = sb.jail.as_ref().unwrap();
        let vsock = jail.path(fc::VSOCK);
        let tmp_mem = jail.memdir.join("cap.mem");
        let tmp_state = jail.memdir.join("cap.vmstate");
        let _ = std::fs::remove_file(&tmp_mem);
        let _ = std::fs::remove_file(&tmp_state);
        // A preallocated image (see fc::prepare_spare) takes the place of
        // the file Firecracker would otherwise allocate during the pause.
        let spare = std::fs::rename(jail.memdir.join(fc::SPARE_READY), &tmp_mem).is_ok();
        detail.insert("spare".into(), u8::from(spare).to_string());
        let sealed_disk = sealed.clone();
        let discard = |e: EngineError| {
            let _ = std::fs::remove_dir_all(&cdir);
            let _ = std::fs::remove_file(&tmp_mem);
            let _ = std::fs::remove_file(&tmp_state);
            if let Some(p) = &seal_path {
                let _ = std::fs::remove_file(p);
            }
            if let (DiskRef::Thin(id), Some(pool)) = (&sealed_disk, self.disk.thin()) {
                let _ = pool.delete(*id);
            }
            if durable {
                self.state.abandon(capture_id);
            }
            e.context("capture")
        };

        // --- task processes frozen from here ---
        let t = Instant::now();
        if let Err((e, thaw_failed)) = freeze_guest(&vsock) {
            if let Some(t) = thaw_failed {
                sb.state = State::Failed(format!("tasks may be frozen: {}", t.message));
            }
            return Err(discard(e));
        }
        detail.insert("freeze_us".into(), us(t.elapsed()));
        // --- source paused from here ---
        let tp = Instant::now();
        let working = sb.working.as_ref();
        let seal_result = jail
            .api("PATCH", "/vm", r#"{"state":"Paused"}"#)
            .and_then(|_| {
                // The image outlives this sandbox's own limits (the fork owns
                // it), so its pages are charged to the shared layer cgroup.
                let _charge = fc::LayerCharge::begin(&jail.id)?;
                let t = Instant::now();
                let body = format!(
                    r#"{{"snapshot_type":"Full","mem_file_path":"/{m}/cap.mem","snapshot_path":"/{m}/cap.vmstate","sync_snapshot_files":false}}"#,
                    m = fc::MEMDIR
                );
                fault::step("capture", || jail.api("PUT", "/snapshot/create", &body))?;
                detail.insert("full_write_us".into(), us(t.elapsed()));
                let t = Instant::now();
                fault::step("seal", || match (&sealed, &self.disk) {
                    (DiskRef::File(_), _) => fsutil::reflink(
                        &jail.path(fc::ROOTFS),
                        seal_path.as_ref().unwrap(),
                        0o400,
                    ),
                    (DiskRef::Thin(new), disk::Backend::Thin(pool)) => {
                        let w = working
                            .ok_or_else(|| EngineError::host("sandbox has no working disk"))?;
                        let (DiskRef::Thin(origin), Some(name)) = (&w.disk, &w.dm_name) else {
                            return Err(EngineError::host("working disk is not a thin volume"));
                        };
                        pool.seal(name, &jail.path(fc::ROOTFS), *origin, *new)
                    }
                    _ => Err(EngineError::host("disk backend mismatch")),
                })?;
                detail.insert("disk_seal_us".into(), us(t.elapsed()));
                Ok(())
            });
        let resumed = jail.api("PATCH", "/vm", r#"{"state":"Resumed"}"#);
        let pause = tp.elapsed();
        // --- source running again; thaw its tasks (same identity) ---
        let t = Instant::now();
        let thawed = thaw_guest(&vsock);
        detail.insert("thaw_us".into(), us(t.elapsed()));
        if let Err(e) = &thawed {
            sb.state = State::Failed(format!("tasks may be frozen: {}", e.message));
        }
        if let Err(e) = seal_result.and(resumed.map(|_| ())).and(thawed) {
            return Err(discard(e));
        }
        detail.insert("source_pause_us".into(), us(pause));
        let t = Instant::now();
        let mem = self
            .cfg
            .mem_dir
            .join("captures")
            .join(format!("{capture_id}.mem"));
        let vmstate = mem.with_extension("vmstate");
        let sealed_tmpfs = (|| -> Result<u64> {
            std::fs::rename(&tmp_mem, &mem).ctx("seal memory image")?;
            std::fs::rename(&tmp_state, &vmstate).ctx("seal state")?;
            // Root-only until handed to the one sandbox that restores it.
            for p in [&mem, &vmstate] {
                fsutil::chown(p, 0, 0)?;
                fsutil::chmod(p, 0o400)?;
            }
            if let (DiskRef::File(_), Some(p)) = (&sealed, &seal_path) {
                fsutil::chown(p, 0, 0)?;
            }
            Ok(std::fs::metadata(&mem).map(|m| m.len()).unwrap_or(0))
        })();
        let mem_bytes = match sealed_tmpfs {
            Ok(n) => n,
            Err(e) => {
                let _ = std::fs::remove_file(&mem);
                let _ = std::fs::remove_file(&vmstate);
                return Err(discard(e));
            }
        };
        detail.insert("memory_bytes".into(), mem_bytes.to_string());
        if durable {
            let committed = self.commit_durable(
                &mut sb,
                capture_id,
                &mem,
                &vmstate,
                &sealed,
                seal_path.as_deref(),
                (&mem_name, &vmstate_name, &disk_name),
                &mut detail,
            );
            if let Err(e) = committed {
                self.state.abandon(capture_id);
                let _ = std::fs::remove_file(&mem);
                let _ = std::fs::remove_file(&vmstate);
                let _ = std::fs::remove_dir_all(&cdir);
                // Objects already written are uncommitted garbage, collected
                // by the next gc; a thin seal goes with them.
                return Err(e.context("capture"));
            }
        }
        let meta = serde_json::json!({
            "capture_id": capture_id,
            "source_sandbox_id": sb.id,
            "source_generation": sb.generation,
            "preserves_memory": true,
            "memory": "full",
            "memory_bytes": mem_bytes,
            "durability": if durable { "local" } else { "volatile" },
            "disk": sealed.encode(),
            "resources": sb.resources,
            "engine": ENGINE_NAME,
            "engine_digest": self.compat.engine_digest,
            "cpu_baseline": self.compat.cpu_baseline,
            "created_at_unix_ms": now_ms(),
        });
        std::fs::write(cdir.join("meta.json"), meta.to_string()).ctx("write capture meta")?;
        detail.insert("publish_us".into(), us(t.elapsed()));
        sb.captures.push(capture_id.into());
        let rec = CaptureRec {
            id: capture_id.into(),
            dir: cdir,
            disk: sealed,
            template_id: sb.template_id.clone(),
            base: sb.base.clone(),
            parent: sb.parent.clone(),
            durable,
            mem,
            vmstate,
            source_sandbox: sb.id.clone(),
            source_generation: sb.generation,
            resources: sb.resources.clone(),
            durability: if durable { "local" } else { "volatile" }.into(),
            restores: Mutex::new(CAPTURE_AVAILABLE),
        };
        self.captures
            .lock()
            .unwrap()
            .insert(capture_id.into(), Arc::new(rec));
        detail.insert("total_us".into(), us(t0.elapsed()));
        // A sandbox that forked keeps one image ready for its next fork.
        if self.cfg.subscription.is_none()
            && let Some(jail) = sb.jail.as_ref().filter(|_| self.cfg.capture_spare)
        {
            fc::prepare_spare(jail, sb.resources.memory_mib * 1024 * 1024);
        }
        Ok(Outcome {
            state: "captured".into(),
            handles: vec![capture_id.into()],
            detail,
            ..Default::default()
        })
    }

    /// The durable half of a `local` capture, after the source resumed:
    /// object_flush → directory_flush → heads → switch → journal_flush →
    /// publish. Returns only once the capture is recoverable.
    #[allow(clippy::too_many_arguments)]
    fn commit_durable(
        &self,
        sb: &mut Sandbox,
        cid: &str,
        mem: &Path,
        vmstate: &Path,
        sealed: &DiskRef,
        seal_path: Option<&Path>,
        (mem_name, vmstate_name, disk_name): (&str, &str, &str),
        detail: &mut BTreeMap<String, String>,
    ) -> Result<()> {
        let template = self.template(&sb.template_id)?;
        // Encode against the full image this sandbox started from, when
        // that is still a live durable capture; the template otherwise.
        // A delta whose chain would be longer than materialization follows
        // is encoded against the template instead, so every acknowledged
        // capture can be resumed.
        let (base, base_path) = match &sb.base {
            Base::Capture(c) if self.state.get(c).is_some() && self.chain_depth(c) < MAX_CHAIN => (
                Some(c.clone()),
                sb.jail.as_ref().unwrap().memdir.join(SRC_MEM),
            ),
            _ => (
                None,
                self.template_base(&template, std::fs::metadata(mem).ctx("stat capture")?.len())?,
            ),
        };
        let t = Instant::now();
        let mem_tmp = self.state.object(&format!("{mem_name}.tmp"));
        let state_tmp = self.state.object(&format!("{vmstate_name}.tmp"));
        let (enc, mem_digest, vm_sha, vm_size) = fault::step("object_flush", || {
            // An omitted object_flush (negative control) leaves the pages
            // in the page cache: only a flush would make them durable.
            let skip = fault::omitted("object_flush");
            let enc = memimg::encode(mem, &base_path, &mem_tmp, false, !skip)?;
            detail.insert("durable_encode_us".into(), us(t.elapsed()));
            let ts = Instant::now();
            if !skip {
                File::open(&mem_tmp)
                    .and_then(|f| f.sync_data())
                    .ctx("sync memory object")?;
            }
            let digest = enc.digest.clone();
            detail.insert("durable_mem_sync_us".into(), us(ts.elapsed()));
            std::fs::copy(vmstate, &state_tmp).ctx("persist vm state")?;
            let vm_sha = durable::sha256_file(&state_tmp)?;
            let vm_size = std::fs::metadata(&state_tmp)?.len();
            if !skip {
                fsutil::fsync_path(&state_tmp)?;
                detail.insert("durable_vmstate_us".into(), us(ts.elapsed()));
                match (sealed, &self.disk) {
                    (DiskRef::File(_), _) => fsutil::fsync_path(seal_path.unwrap())?,
                    (DiskRef::Thin(_), disk::Backend::Thin(pool)) => pool.commit()?,
                    _ => {}
                }
                // btrfs logs a rename of an inode it already logged in the
                // running transaction, so any later fsync would make the
                // objects' final names durable as a side effect. Committing
                // the transaction here leaves directory_flush as the only
                // barrier for them, as on every other filesystem.
                if disk::statfs_type(&self.state.objects())? == disk::BTRFS_MAGIC {
                    fsutil::syncfs(&self.state.objects())?;
                }
            }
            detail.insert("durable_sync_us".into(), us(ts.elapsed()));
            Ok((enc, digest, vm_sha, vm_size))
        })?;
        detail.insert("changed_pages".into(), enc.changed_pages.to_string());
        detail.insert("durable_bytes".into(), enc.object_bytes.to_string());
        let t = Instant::now();
        let mut names = vec![mem_name.to_string(), vmstate_name.to_string()];
        if matches!(sealed, DiskRef::File(_)) {
            names.push(disk_name.to_string());
        }
        fault::step("directory_flush", || self.state.publish_objects(&names))?;
        let disk_bytes = sb.resources.disk_gib << 30;
        let mut m = durable::CaptureManifest {
            format: durable::FORMAT.into(),
            capture_id: cid.into(),
            parent: sb.parent.clone(),
            source_sandbox_id: sb.id.clone(),
            source_generation: sb.generation,
            template_id: sb.template_id.clone(),
            resources: sb.resources.clone(),
            memory: durable::MemoryRef {
                base,
                object: mem_name.into(),
                digest: mem_digest.digest,
                size: mem_digest.size,
                image_bytes: enc.image_bytes,
                changed_pages: enc.changed_pages,
            },
            vmstate: durable::ObjectRef {
                object: vmstate_name.into(),
                sha256: vm_sha,
                size: vm_size,
            },
            disk: durable::DiskRecord {
                disk: sealed.encode(),
                bytes: disk_bytes,
                digest: None,
            },
            engine_digest: self.compat.engine_digest.clone(),
            cpu_baseline: self.compat.cpu_baseline.clone(),
            state_abi: STATE_ABI.into(),
            producer_key_id: String::new(),
            producer_host_id: self.cfg.host_id.clone(),
            created_at_unix_ms: now_ms(),
            signature: String::new(),
        };
        if let DiskRef::File(p) = sealed {
            m.disk.bytes = disk::size_of_file(&self.disk_path(p))?;
        }
        m.sign(&self.producer)?;
        fault::step("heads", || self.state.stage_checked(&m))?;
        fault::step("switch", || {
            // The source now descends from this generation.
            sb.parent = Some(cid.to_string());
            Ok(())
        })?;
        fault::step("journal_flush", || self.state.flush())?;
        fault::step("publish", || {
            self.state.insert(m);
            Ok(())
        })?;
        detail.insert("durable_commit_us".into(), us(t.elapsed()));
        Ok(())
    }

    /// Removes captures that were restored (or whose source is gone and
    /// nobody is restoring them). A restored fork keeps its own image link.
    /// A durable capture only gives up its tmpfs image: the store keeps it.
    fn gc_captures(&self) {
        let live: BTreeSet<String> = self.sandboxes.lock().unwrap().keys().cloned().collect();
        self.captures.lock().unwrap().retain(|_, c| {
            let state = *c.restores.lock().unwrap();
            let consumed = state == CAPTURE_CONSUMED
                || (state == CAPTURE_AVAILABLE && !live.contains(&c.source_sandbox));
            if consumed {
                // The restored fork keeps its own link to the image.
                let _ = std::fs::remove_dir_all(&c.dir);
                let _ = std::fs::remove_file(&c.mem);
                let _ = std::fs::remove_file(&c.vmstate);
                if !c.durable
                    && let (DiskRef::Thin(id), Some(pool)) = (&c.disk, self.disk.thin())
                {
                    let _ = pool.delete(*id);
                }
            }
            !consumed
        });
    }

    /// Committed durable captures (and damaged ones, with the reason).
    pub fn state_list(&self) -> Vec<durable::Listed> {
        self.state.list()
    }

    /// Deletes a durable capture (refused while a live capture descends
    /// from it). Its objects go at the next gc.
    pub fn state_delete(&self, cid: &str) -> Result<()> {
        // A live sandbox restored from (or captured as) a generation keeps
        // it: its next capture names it as parent and delta base.
        let all: Vec<_> = self.sandboxes.lock().unwrap().values().cloned().collect();
        let mut pinned = BTreeSet::new();
        for sb in all {
            let sb = sb.lock().unwrap();
            pinned.extend(sb.parent.clone());
            if let Base::Capture(b) = &sb.base {
                pinned.insert(b.clone());
            }
        }
        self.state.delete_unless_pinned(cid, &pinned)
    }

    /// Removes every object, manifest and durable thin volume that no
    /// committed capture references.
    pub fn state_gc(&self) -> Result<serde_json::Value> {
        fault::step("gc", || {
            let files = self.state.gc_files()?;
            let mut volumes = 0;
            if let Some(pool) = self.disk.thin() {
                let (live, reserved) = self.state.thin_ids();
                let templates: BTreeSet<u32> = self
                    .template_disks
                    .read()
                    .unwrap()
                    .values()
                    .filter_map(|d| match d {
                        DiskRef::Thin(id) => Some(*id),
                        DiskRef::File(_) => None,
                    })
                    .collect();
                for id in reserved.difference(&live) {
                    if !templates.contains(id) && pool.delete(*id).is_ok() {
                        volumes += 1;
                    }
                }
                pool.commit()?;
            }
            Ok(serde_json::json!({"files": files, "thin_volumes": volumes}))
        })
    }

    /// Re-encodes a chained capture's memory against its template, then
    /// atomically redirects the capture to the new object and collects the
    /// old one: compact_objects → compact_redirect → compact_gc.
    pub fn state_compact(&self, cid: &str) -> Result<serde_json::Value> {
        let e = self
            .state
            .get(cid)
            .ok_or_else(|| EngineError::not_found(format!("capture {cid}")))?;
        if e.manifest.memory.base.is_none() {
            return Ok(serde_json::json!({"capture_id": cid, "compacted": false}));
        }
        let t = self.template(&e.manifest.template_id)?;
        let (mem_name, _, _) = durable::object_names(cid);
        // Scratch is private to this compaction: named after its own random
        // object and created exclusively, so no other compaction (of the same
        // capture or not) can replace or remove an image it is encoding.
        let parent = self.cfg.mem_dir.join("captures");
        std::fs::create_dir_all(&parent).ctx("create compaction scratch")?;
        let scratch = parent.join(format!("compact-{mem_name}"));
        std::fs::create_dir(&scratch).ctx("create compaction scratch")?;
        // One hold per compaction (the object name is random), so one
        // compaction's release never drops another's. Not a capture ID (no
        // `cp_` prefix), so it cannot collide with one either.
        let hold = format!("compact:{mem_name}");
        if let Err(e) = self.state.hold(&hold, vec![mem_name.clone()]) {
            let _ = std::fs::remove_dir_all(&scratch);
            return Err(e);
        }
        let r = (|| {
            let tmp = self.state.object(&format!("{mem_name}.tmp"));
            let (enc, digest) = fault::step("compact_objects", || {
                let full = scratch.join("full.mem");
                self.materialize(cid, &full, &scratch, 0)?;
                let enc = memimg::encode(&full, &t.dir.join(fc::MEM), &tmp, true, true)?;
                let _ = std::fs::remove_file(&full);
                let digest = enc.digest.clone();
                self.state
                    .publish_objects(std::slice::from_ref(&mem_name))?;
                Ok((enc, digest))
            })?;
            let old = e.manifest.memory.object.clone();
            let redirected = fault::step("compact_redirect", || {
                let mut m = e.manifest.clone();
                m.memory = durable::MemoryRef {
                    base: None,
                    object: mem_name.clone(),
                    digest: digest.digest.clone(),
                    size: digest.size,
                    image_bytes: enc.image_bytes,
                    changed_pages: enc.changed_pages,
                };
                m.sign(&self.producer)?;
                self.state.redirect(m, vec![old.clone()])
            });
            // Referenced by the committed redirect now, or garbage if it failed.
            self.state.release(&hold);
            redirected?;
            let removed = fault::step("compact_gc", || self.state.gc_files())?;
            Ok(serde_json::json!({
                "capture_id": cid, "compacted": true, "object": mem_name,
                "retired": old, "removed_files": removed,
            }))
        })();
        self.state.release(&hold);
        let _ = std::fs::remove_dir_all(&scratch);
        r
    }

    /// Fully verifies a durable capture (signature, compatibility, digests).
    pub fn state_verify(&self, cid: &str) -> Result<()> {
        let e = self
            .state
            .get(cid)
            .ok_or_else(|| EngineError::not_found(format!("capture {cid}")))?;
        self.verify_durable(&e.manifest)
    }

    /// Writes a self-contained, signed bundle of a durable capture to `dir`
    /// (memory against the template, vm state, raw disk, manifest).
    pub fn state_export(&self, cid: &str, dir: &Path) -> Result<()> {
        let e = self
            .state
            .get(cid)
            .ok_or_else(|| EngineError::not_found(format!("capture {cid}")))?;
        self.verify_durable(&e.manifest)?;
        let t = self.template(&e.manifest.template_id)?;
        std::fs::create_dir(dir).ctx("create export dir")?;
        let full = dir.join("full.mem");
        self.materialize(cid, &full, dir, 0)?;
        let base = self.template_base(&t, e.manifest.memory.image_bytes)?;
        memimg::encode(&full, &base, &dir.join("mem"), true, true)?;
        std::fs::remove_file(&full)?;
        std::fs::copy(
            self.state.object(&e.manifest.vmstate.object),
            dir.join("vmstate"),
        )?;
        let disk_out = dir.join("disk");
        match (e.manifest.disk_ref()?, &self.disk) {
            (DiskRef::File(p), _) => {
                if fsutil::reflink(&self.disk_path(&p), &disk_out, 0o600).is_err() {
                    sparse_copy(&self.disk_path(&p), &disk_out)?;
                }
            }
            (DiskRef::Thin(id), disk::Backend::Thin(pool)) => {
                let (name, dev) = pool.activate('x', id, e.manifest.disk.bytes)?;
                let node = pool.dir.join("dev").join(&name);
                let _ = std::fs::remove_file(&node);
                let r = pool
                    .node(&node, dev, 0)
                    .and_then(|_| sparse_copy(&node, &disk_out));
                let _ = std::fs::remove_file(&node);
                pool.deactivate(&name)?;
                r?;
            }
            _ => return Err(EngineError::host("disk backend mismatch")),
        }
        let mut m = e.manifest.clone();
        let mem = fsutil::digest_file(&dir.join("mem"))?;
        let header = memimg::read_header(&File::open(dir.join("mem"))?)?;
        m.parent = None;
        m.memory = durable::MemoryRef {
            base: None,
            object: "mem".into(),
            digest: mem.digest,
            size: mem.size,
            image_bytes: header.image_bytes,
            changed_pages: header.changed_pages,
        };
        m.vmstate.object = "vmstate".into();
        m.disk.disk = "file:disk".into();
        m.disk.bytes = disk::size_of_file(&disk_out)?;
        m.disk.digest = Some(fsutil::digest_file(&disk_out)?.digest);
        m.sign(&self.producer)?;
        std::fs::write(dir.join("manifest.json"), manifest::canonical_json(&m)?)?;
        // The administrator exports into a caller-owned private parent. Hand
        // the bundle to that owner so lampd can seal it without root access.
        use std::os::unix::fs::MetadataExt;
        let parent = std::fs::metadata(
            dir.parent()
                .ok_or_else(|| EngineError::invalid("export parent required"))?,
        )?;
        for name in ["mem", "vmstate", "disk", "manifest.json"] {
            fsutil::chown(&dir.join(name), parent.uid(), parent.gid())?;
            fsutil::chmod(&dir.join(name), 0o600)?;
        }
        fsutil::chown(dir, parent.uid(), parent.gid())?;
        fsutil::chmod(dir, 0o700)?;
        Ok(())
    }

    /// Imports a capture bundle. The manifest's producer must be enrolled
    /// and its signature and digests must verify before any object is
    /// copied; otherwise the bundle is quarantined and nothing is published.
    pub fn state_import(&self, dir: &Path) -> Result<String> {
        let refuse = |e: EngineError| {
            let q = self.state.quarantine_dir(dir);
            eprintln!(
                "lamp-engine: refused capture import ({e}); quarantined at {}",
                q.display()
            );
            e
        };
        let bytes = std::fs::read(dir.join("manifest.json")).ctx("read bundle manifest")?;
        let m: durable::CaptureManifest = serde_json::from_slice(&bytes).map_err(|e| {
            refuse(EngineError::new(
                "DATA_UNAVAILABLE",
                format!("manifest: {e}"),
            ))
        })?;
        // Producer authority first: an unenrolled key is refused even when
        // every digest is correct.
        m.verify(&self.trusted).map_err(refuse)?;
        if m.memory.object != "mem" || m.vmstate.object != "vmstate" || m.disk.disk != "file:disk" {
            return Err(refuse(EngineError::new(
                "DATA_UNAVAILABLE",
                "not an export bundle",
            )));
        }
        // The signed manifest binds all three: memory, vm state and disk.
        let disk_digest = m.disk.digest.as_deref().ok_or_else(|| {
            refuse(EngineError::new(
                "DATA_UNAVAILABLE",
                "bundle manifest binds no disk digest",
            ))
        })?;
        if fsutil::digest_file(&dir.join("mem"))?.digest != m.memory.digest
            || durable::sha256_file(&dir.join("vmstate"))? != m.vmstate.sha256
            || fsutil::digest_file(&dir.join("disk"))?.digest != disk_digest
            || disk::size_of_file(&dir.join("disk"))? != m.disk.bytes
        {
            return Err(refuse(EngineError::new(
                "DATA_UNAVAILABLE",
                "bundle digest mismatch",
            )));
        }
        let t = self.template(&m.template_id).map_err(refuse)?;
        if m.engine_digest != self.compat.engine_digest
            || m.cpu_baseline != self.compat.cpu_baseline
        {
            return Err(refuse(EngineError::new(
                "ENGINE_UNQUALIFIED",
                "bundle is for another host",
            )));
        }
        let cid = if self.state.exists(&m.capture_id) {
            format!(
                "cp_{}",
                lamp_init::hex_encode(&fsutil::random_bytes::<16>())
            )
        } else {
            m.capture_id.clone()
        };
        if cid != m.capture_id {
            self.custody
                .as_ref()
                .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?
                .lock()
                .unwrap()
                .transact(|graph| {
                    graph.derive(
                        &m.capture_id,
                        &cid,
                        crate::custody::Destination {
                            principal: self.custody_owner(),
                            host: &self.cfg.host_id,
                            online: true,
                        },
                        crate::custody::CapturePurpose::OwnerPrivate,
                    )
                })?;
        }
        let (mem_name, vmstate_name, disk_name) = durable::object_names(&cid);
        let thin = match &self.disk {
            disk::Backend::Thin(pool) => Some(pool.new_id()),
            disk::Backend::Reflink => None,
        };
        let mut objects = vec![mem_name.clone(), vmstate_name.clone()];
        if thin.is_none() {
            objects.push(disk_name.clone());
        }
        self.state
            .reserve(&cid, objects, thin.into_iter().collect())?;
        let r = (|| {
            for (from, to) in [("mem", &mem_name), ("vmstate", &vmstate_name)] {
                let dst = self.state.object(&format!("{to}.tmp"));
                std::fs::copy(dir.join(from), &dst)?;
                fsutil::fsync_path(&dst)?;
            }
            let mut names = vec![mem_name.clone(), vmstate_name.clone()];
            let disk = match (thin, &self.disk) {
                (Some(id), disk::Backend::Thin(pool)) => {
                    pool.import(id, &dir.join("disk"))?;
                    DiskRef::Thin(id)
                }
                _ => {
                    let dst = self.state.object(&format!("{disk_name}.tmp"));
                    sparse_copy(&dir.join("disk"), &dst)?;
                    fsutil::fsync_path(&dst)?;
                    names.push(disk_name.clone());
                    DiskRef::File(format!("state/objects/{disk_name}"))
                }
            };
            self.state.publish_objects(&names)?;
            let mut local = m.clone();
            local.capture_id = cid.clone();
            local.memory.object = mem_name.clone();
            local.vmstate.object = vmstate_name.clone();
            local.disk.disk = disk.encode();
            local.template_id = t.id.clone();
            local.sign(&self.producer)?;
            self.state.stage(&local)?;
            self.state.flush()?;
            self.state.insert(local);
            Ok(cid.clone())
        })();
        if r.is_err() {
            self.state.abandon(&cid);
        }
        r
    }

    pub fn stop(&self, auth: Authority<'_>) -> Result<Outcome> {
        let sb = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb.lock().unwrap();
        Self::check(&sb, auth)?;
        if let Some(j) = sb.jail.as_mut() {
            j.kill();
        }
        if sb.state == State::Running || sb.state == State::Allocated {
            sb.state = State::Stopped;
        }
        Ok(Outcome {
            state: sb.state.name().into(),
            ..Default::default()
        })
    }

    pub fn release(&self, auth: Authority<'_>) -> Result<Outcome> {
        let arc = self.sandbox(auth.sandbox_id)?;
        {
            let mut sb = arc.lock().unwrap();
            // Release is authorized by the supervisor's cleanup grant, across
            // generations, so a failed resume cannot make a sandbox undeletable.
            if let Some(uid) = sb.teardown() {
                self.free_uid(uid);
            }
            self.free_uid(sb.uid);
            let w = sb.working.take();
            self.release_disk(w);
            sb.state = State::Stopped;
        }
        self.sandboxes.lock().unwrap().remove(auth.sandbox_id);
        self.gc_captures();
        Ok(Outcome {
            state: "released".into(),
            ..Default::default()
        })
    }

    pub fn inspect(&self, sandbox_id: &str, command_operation_id: &str) -> Result<Outcome> {
        let sb = self.sandbox(sandbox_id)?;
        let mut sb = sb.lock().unwrap();
        if sb.state == State::Running && !sb.jail.as_ref().is_some_and(Jail::alive) {
            let reason = sb
                .jail
                .as_ref()
                .and_then(|jail| {
                    std::fs::read_to_string(fc::cgroup_dir(&jail.id).join("memory.events")).ok()
                })
                .is_some_and(|events| {
                    events.lines().any(|line| {
                        line.strip_prefix("oom_kill ")
                            .and_then(|n| n.parse::<u64>().ok())
                            .is_some_and(|n| n > 0)
                    })
                });
            sb.state = State::Failed(if reason { "oom_killed" } else { "VMM exited" }.into());
        }
        let mut detail = BTreeMap::new();
        detail.insert("generation".into(), sb.generation.to_string());
        detail.insert("source".into(), sb.source.clone());
        detail.insert("boundary".into(), "vm".into());
        detail.insert(
            "capture_trust".into(),
            self.capture_trust(sandbox_id)?.into(),
        );
        detail.insert(
            "custody_host_ids".into(),
            serde_json::to_string(&self.cfg.subscription.as_ref().map_or_else(
                || vec![self.cfg.host_id.clone()],
                |policy| policy.custody_host_ids.clone(),
            ))
            .unwrap(),
        );
        detail.insert("engine".into(), ENGINE_NAME.into());
        detail.insert("vcpus".into(), sb.resources.vcpus.to_string());
        detail.insert("memory_mib".into(), sb.resources.memory_mib.to_string());
        detail.insert("disk_gib".into(), sb.resources.disk_gib.to_string());
        detail.insert("pids_max".into(), sb.pids_max.to_string());
        detail.insert(
            "boot".into(),
            if sb.cold { "cold" } else { "prepared_image" }.into(),
        );
        detail.insert(
            "devices".into(),
            sb.disks
                .iter()
                .map(|d| format!("{}:disk:{}MiB", d.id, d.size_mib))
                .collect::<Vec<_>>()
                .join(","),
        );
        if let State::Failed(why) = &sb.state {
            detail.insert("failure".into(), why.clone());
        }
        if let Some(pid) = sb.jail.as_ref().and_then(|j| j.pid) {
            detail.insert("vmm_pid".into(), pid.to_string());
            detail.insert("vmm_uid".into(), sb.uid.to_string());
        }
        if command_operation_id.is_empty()
            && let Some(jail) = &sb.jail
        {
            detail.insert(
                "boot_log".into(),
                fsutil::log_tail(&jail.dir.join("vmm.log")),
            );
        }
        // Empty Inspect is the read-only telemetry path. Command polling stays cheap.
        if command_operation_id.is_empty()
            && sb.state == State::Running
            && let Some(vsock) = sb.jail.as_ref().map(|j| j.path(fc::VSOCK))
            && let Ok(GuestResponse::Pong(status)) = guest::call(
                &vsock,
                &GuestRequest::Ping(Empty {}),
                Duration::from_secs(2),
            )
            && status.sandbox_id == sb.id
            && status.generation == sb.generation
        {
            detail.insert(
                "metrics".into(),
                serde_json::to_string(&status.metrics).unwrap(),
            );
            detail.insert("metrics_boot_id".into(), status.kernel_boot_id);
        }
        let command = if command_operation_id.is_empty() {
            None
        } else {
            let table = sb.commands.lock().unwrap();
            let rec =
                table.0.get(command_operation_id).cloned().ok_or_else(|| {
                    EngineError::not_found(format!("command {command_operation_id}"))
                })?;
            let (state, mut done) = match (&rec.done, &rec.lost) {
                (Some(d), _) => ("exited", d.clone()),
                (None, Some(why)) => {
                    detail.insert("command_lost".into(), why.clone());
                    ("unknown", pending(&rec.started))
                }
                (None, None) => ("running", pending(&rec.started)),
            };
            self.finish_subscription_report(
                sandbox_id,
                command_operation_id,
                &mut done,
                rec.lost.is_some(),
            )?;
            detail.insert("command_state".into(), state.into());
            Some(done)
        };
        Ok(Outcome {
            state: sb.state.name().into(),
            handles: sb.captures.clone(),
            command,
            detail,
        })
    }

    /// Kills every sandbox (supervisor shutdown).
    /// Stops every sandbox and returns once each VMM has fully exited.
    pub fn shutdown(&self) -> usize {
        self.bridges.lock().unwrap().clear();
        let all: Vec<_> = self.sandboxes.lock().unwrap().drain().collect();
        let n = all.len();
        for (_, sb) in all {
            let mut sb = sb.lock().unwrap();
            let _ = sb.teardown();
            let w = sb.working.take();
            self.release_disk(w);
        }
        n
    }

    pub fn compatibility(&self, kernel_digest: &str) -> Compatibility {
        Compatibility {
            engine: ENGINE_NAME.into(),
            engine_digest: self.compat.engine_digest.clone(),
            state_abi: STATE_ABI.into(),
            guest_kernel_digest: kernel_digest.into(),
            cpu_baseline: self.compat.cpu_baseline.clone(),
            device_graph_digest: manifest::sha256_hex(DEVICE_GRAPH.as_bytes()),
        }
    }
}

/// Copies a file's data extents only (sparse images stay sparse).
fn sparse_copy(src: &Path, dst: &Path) -> Result<()> {
    use std::os::fd::AsRawFd;
    use std::os::unix::fs::FileExt;
    let s = File::open(src).ctx(format!("open {}", src.display()))?;
    let len = {
        use std::io::Seek;
        let mut f = &s;
        f.seek(std::io::SeekFrom::End(0))?
    };
    let d = std::fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .open(dst)
        .ctx(format!("create {}", dst.display()))?;
    d.set_len(len)?;
    let fd = s.as_raw_fd();
    let mut buf = vec![0_u8; 4 << 20];
    let mut off = 0_u64;
    let block = std::fs::metadata(src)
        .map(|m| m.file_type().is_file())
        .unwrap_or(false);
    while off < len {
        let (data, end) = if block {
            // SAFETY: lseek on a valid descriptor.
            let data = unsafe { libc::lseek(fd, off as libc::off_t, libc::SEEK_DATA) };
            if data < 0 {
                break;
            }
            let end = unsafe { libc::lseek(fd, data, libc::SEEK_HOLE) };
            (data as u64, (end as u64).min(len))
        } else {
            (off, len)
        };
        let mut at = data;
        while at < end {
            let n = (end - at).min(buf.len() as u64) as usize;
            s.read_exact_at(&mut buf[..n], at)?;
            if buf[..n].iter().any(|&b| b != 0) {
                d.write_all_at(&buf[..n], at)?;
            }
            at += n as u64;
        }
        off = end;
    }
    d.sync_all()?;
    Ok(())
}

impl Engine {
    pub fn mount_storage(
        &self,
        auth: Authority<'_>,
        socket: &str,
        path: &str,
        read_only: bool,
        unmount: bool,
        caller_uid: u32,
    ) -> Result<Outcome> {
        let arc = self.sandbox(auth.sandbox_id)?;
        let sb = arc.lock().unwrap();
        Self::check(&sb, auth)?;
        let vsock = sb
            .jail
            .as_ref()
            .ok_or_else(|| EngineError::in_use("no jail"))?
            .path(fc::VSOCK);
        drop(sb);
        crate::storage::mount(
            &vsock,
            auth.sandbox_id,
            auth.generation,
            socket,
            path,
            read_only,
            unmount,
            caller_uid,
        )?;
        Ok(Outcome {
            state: if unmount { "unmounted" } else { "mounted" }.into(),
            ..Default::default()
        })
    }
}
