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
use crate::error::{EngineError, IoContext, Result};
use crate::fc::{self, Binaries, Jail, Limits};
use crate::fsutil::{self, now_ms, now_ns, random_bytes};
use crate::guest;
use crate::manifest::{self, AccelerationVariant, Compatibility, Manifest, Provenance, Resources};
use ed25519_dalek::{SigningKey, VerifyingKey};
use lamp_init::{Barrier, Empty, Exec, ExecDone, ExecStarted, GuestRequest, GuestResponse};
use std::collections::{BTreeMap, BTreeSet, HashMap, VecDeque};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Duration, Instant};

pub const ENGINE_NAME: &str = "firecracker-1.17.0";
pub const STATE_ABI: &str = "firecracker-1.17.0/snapshot";
pub const DEVICE_GRAPH: &str = "virtio-blk:rootfs,vsock:cid3,serial,virtio-net:eth0-lamp0";
pub const STANDARD: Resources = Resources {
    vcpus: 2,
    memory_mib: 4096,
    disk_gib: 20,
};
pub const DEFAULT_PIDS_MAX: u64 = 4096;
/// Completed commands remembered per sandbox for completion lookups. Running
/// commands are never forgotten; at most `MAX_RUNNING` run at once.
const COMMAND_MEMORY: usize = 256;
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
    /// Extra trusted producer keys beyond this host's own.
    pub trusted_producers: Vec<VerifyingKey>,
    /// Guard network and protected-process audit. `None` leaves sandboxes
    /// with an unconnected link and unaudited commands (diagnostic only).
    pub network: Option<crate::net::Policy>,
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
            uid_base: 900_000,
            uid_count: 65_536,
            host_id: hostname(),
            trusted_producers: Vec::new(),
            network: None,
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
        disk: PathBuf,
    },
    Capture(Arc<CaptureRec>),
}

/// Copies a full RAM image to the store for a durable capture, leaving
/// all-zero pages as holes. Every page is still read and checked.
fn copy_nonzero(src: &Path, dst: &Path) -> Result<()> {
    use std::os::unix::fs::FileExt;
    const PAGE: usize = 4096;
    let input = std::fs::File::open(src).ctx("open capture image")?;
    let len = input.metadata()?.len();
    let out = std::fs::File::create(dst).ctx("create durable image")?;
    out.set_len(len)?;
    let mut buf = vec![0_u8; 4 << 20];
    let mut off = 0_u64;
    while off < len {
        let n = buf.len().min((len - off) as usize);
        input.read_exact_at(&mut buf[..n], off)?;
        let mut p = 0;
        while p < n {
            let end = (p + PAGE).min(n);
            if buf[p..end].iter().any(|&b| b != 0) {
                // Extend the run while pages are nonzero.
                let mut q = end;
                while q < n && buf[q..(q + PAGE).min(n)].iter().any(|&b| b != 0) {
                    q = (q + PAGE).min(n);
                }
                out.write_all_at(&buf[p..q], off + p as u64)?;
                p = q;
            } else {
                p = end;
            }
        }
        off += n as u64;
    }
    Ok(())
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
    pub barrier: Option<lamp_init::BarrierAck>,
    /// Commands by the operation ID of their `Start`, oldest first.
    pub commands: Arc<Mutex<CommandTable>>,
    /// Guard network and audit listener of this generation.
    pub net: Option<crate::net::Attachment>,
}

impl Sandbox {
    /// Kills the VMM and removes its jail (and with it the memory it mapped),
    /// then its guard, uplink and audit receivers.
    fn teardown(&mut self) -> Option<u32> {
        if let Some(j) = self.jail.take() {
            j.destroy();
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
    captures: Mutex<HashMap<String, Arc<CaptureRec>>>,
    uids: Mutex<BTreeSet<u32>>,
    /// Host addresses and on-link prefixes every guard denies.
    pub inventory: Vec<String>,
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
        let engine = Engine {
            cfg,
            compat,
            producer,
            trusted,
            templates: RwLock::new(HashMap::new()),
            sandboxes: Mutex::new(HashMap::new()),
            captures: Mutex::new(HashMap::new()),
            uids: Mutex::new(BTreeSet::new()),
            inventory: if cfg_network {
                crate::net::host_inventory()?
            } else {
                Vec::new()
            },
            _lock: lock,
        };
        match mode {
            Mode::Daemon => engine.clean_stale(),
            // Never reuse the identity of a jail left behind by a daemon.
            Mode::Offline => engine.reserve_leftover_uids(),
            Mode::ReadOnly => {}
        }
        engine.load_templates(mode != Mode::ReadOnly);
        Ok(engine)
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

    fn sandbox(&self, id: &str) -> Result<Arc<Mutex<Sandbox>>> {
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
        if resources.memory_mib < 128 || resources.disk_gib == 0 {
            return Err(EngineError::invalid(
                "memory_mib must be ≥128 and disk_gib ≥1",
            ));
        }
        let mut all = self.sandboxes.lock().unwrap();
        if all.contains_key(auth.sandbox_id) {
            return Err(EngineError::in_use(format!(
                "sandbox {} exists",
                auth.sandbox_id
            )));
        }
        let jail = self.new_jail(&jail_id(auth.sandbox_id))?;
        let uid = jail.uid;
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
                barrier: None,
                commands: Arc::default(),
                net: None,
            })),
        );
        Ok(Outcome {
            state: "allocated".into(),
            ..Default::default()
        })
    }

    /// Restores a template variant or a memory capture into an allocated
    /// sandbox and applies the identity barrier before any command.
    pub fn restore(&self, auth: Authority<'_>, capture_id: &str) -> Result<Outcome> {
        let t0 = Instant::now();
        let sb = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb.lock().unwrap();
        Self::check(&sb, auth)?;
        if sb.state != State::Allocated {
            return Err(EngineError::in_use(format!(
                "sandbox is {}, not allocated",
                sb.state.name()
            )));
        }
        let capture = self.captures.lock().unwrap().get(capture_id).cloned();
        let (source, resources) = match &capture {
            Some(c) => (Source::Capture(c.clone()), c.resources.clone()),
            None => {
                let t = self.template(capture_id)?;
                (
                    Source::Template {
                        mem: t.dir.join(fc::MEM),
                        vmstate: t.dir.join(fc::VMSTATE),
                        disk: t.dir.join(fc::ROOTFS),
                    },
                    t.variant.resources.clone(),
                )
            }
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
            // A capture is restored at most once; claim it before any effect.
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

    fn restore_into(&self, sb: &mut Sandbox, source: Source, t0: Instant) -> Result<Outcome> {
        let limits = Limits {
            vcpus: sb.resources.vcpus,
            memory_mib: sb.resources.memory_mib,
            pids_max: DEFAULT_PIDS_MAX,
        };
        let jail = sb
            .jail
            .as_mut()
            .ok_or_else(|| EngineError::in_use("sandbox has no jail"))?;
        let uid = jail.uid;
        let mut detail = BTreeMap::new();
        let disk = match &source {
            Source::Template { disk, .. } => disk.clone(),
            Source::Capture(c) => c.dir.join(fc::ROOTFS),
        };
        fsutil::reflink(&disk, &jail.path(fc::ROOTFS), 0o600)?;
        fsutil::chown(&jail.path(fc::ROOTFS), uid, uid)?;
        let mem_path = match &source {
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
        };
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        let t = Instant::now();
        match &self.cfg.network {
            Some(policy) => {
                let guard_uid = self.alloc_uid()?;
                let attached = crate::net::Attachment::attach(
                    policy,
                    &self.inventory,
                    jail,
                    guard_uid,
                    crate::net::Identity {
                        host_id: &self.cfg.host_id,
                        sandbox_id: &sb.id,
                        generation: sb.generation,
                        operation_id: "op_restore",
                    },
                );
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
        detail.insert("attach_us".into(), us(t.elapsed()));
        let jail = sb.jail.as_mut().unwrap();
        let t = Instant::now();
        jail.spawn(&self.cfg.bins, limits)?;
        detail.insert("spawn_us".into(), us(t.elapsed()));
        let t = Instant::now();
        // vCPUs resume here, but every task process was frozen before the
        // capture (and a template has none): only init and the helper run
        // until the barrier below thaws the tasks with the new identity.
        let body = format!(
            r#"{{"snapshot_path":"/{}/{}","mem_backend":{{"backend_type":"File","backend_path":{}}},"track_dirty_pages":false,"resume_vm":true,"clock_realtime":true}}"#,
            fc::MEMDIR,
            fc::VMSTATE,
            fc::js(&mem_path)
        );
        jail.api("PUT", "/snapshot/load", &body)?;
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
        detail.insert("rng_sample".into(), ack.rng_sample_hex.clone());
        detail.insert("machine_id".into(), ack.machine_id.clone());
        sb.barrier = Some(ack);
        sb.state = State::Running;
        Ok(Outcome {
            state: "restored".into(),
            detail,
            ..Default::default()
        })
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
        if argv.is_empty() {
            return Err(EngineError::invalid("argv is empty"));
        }
        let (vsock, commands, vmm_alive, audited) = {
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
            let jail = sb.jail.as_ref().unwrap();
            (
                jail.path(fc::VSOCK),
                sb.commands.clone(),
                jail.liveness(),
                sb.net.is_some(),
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
            audit: audited.then_some(lamp_init::Audit {
                port: lamp_init::AUDIT_PORT,
                task_uid: crate::net::TASK_UID,
            }),
        });
        let mut conn = guest::send(&vsock, &request)?;
        let started = match conn.next(Some(Duration::from_secs(10)))? {
            GuestResponse::ExecStarted(s) => s,
            GuestResponse::Error { code, message } if code == "GENERATION_MISMATCH" => {
                return Err(EngineError::new("REVISION_CONFLICT", message));
            }
            // Refused before exec (spawn failure): nothing ran.
            GuestResponse::Error { code, message } => {
                return Err(EngineError::invalid(format!("{code}: {message}")));
            }
            other => {
                return Err(EngineError::new(
                    "OUTCOME_UNKNOWN",
                    format!("exec: unexpected reply {other:?}"),
                ));
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
        let cdir = self.cfg.store.join("captures").join(capture_id);
        if self.captures.lock().unwrap().contains_key(capture_id) || cdir.exists() {
            return Err(EngineError::in_use(format!("capture {capture_id} exists")));
        }
        std::fs::create_dir(&cdir).ctx("create capture dir")?;
        fsutil::chmod(&cdir, 0o700)?;
        let jail = sb.jail.as_ref().unwrap();
        let vsock = jail.path(fc::VSOCK);
        let mut detail = BTreeMap::new();
        let tmp_mem = jail.memdir.join("cap.mem");
        let tmp_state = jail.memdir.join("cap.vmstate");
        let _ = std::fs::remove_file(&tmp_mem);
        let _ = std::fs::remove_file(&tmp_state);
        let discard = |e: EngineError| {
            let _ = std::fs::remove_dir_all(&cdir);
            let _ = std::fs::remove_file(&tmp_mem);
            let _ = std::fs::remove_file(&tmp_state);
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
        let sealed = jail
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
                jail.api("PUT", "/snapshot/create", &body)?;
                detail.insert("full_write_us".into(), us(t.elapsed()));
                let t = Instant::now();
                fsutil::reflink(&jail.path(fc::ROOTFS), &cdir.join(fc::ROOTFS), 0o400)?;
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
        if let Err(e) = sealed.and(resumed.map(|_| ())).and(thawed) {
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
        std::fs::rename(&tmp_mem, &mem).ctx("seal memory image")?;
        std::fs::rename(&tmp_state, &vmstate).ctx("seal state")?;
        // Root-only until handed to the one sandbox that restores it.
        for p in [&mem, &vmstate, &cdir.join(fc::ROOTFS)] {
            fsutil::chown(p, 0, 0)?;
            fsutil::chmod(p, 0o400)?;
        }
        let mem_bytes = std::fs::metadata(&mem).map(|m| m.len()).unwrap_or(0);
        detail.insert("memory_bytes".into(), mem_bytes.to_string());
        if durable {
            // Local durability: image, state and disk reach stable storage
            // before the capture is reported.
            copy_nonzero(&mem, &cdir.join(fc::MEM))?;
            std::fs::copy(&vmstate, cdir.join(fc::VMSTATE)).ctx("persist state")?;
            for p in [
                &cdir.join(fc::MEM),
                &cdir.join(fc::VMSTATE),
                &cdir.join(fc::ROOTFS),
                &cdir,
            ] {
                fsutil::fsync_path(p)?;
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
        Ok(Outcome {
            state: "captured".into(),
            handles: vec![capture_id.into()],
            detail,
            ..Default::default()
        })
    }

    /// Removes captures that were restored (or whose source is gone and
    /// nobody is restoring them). A restored fork keeps its own image link.
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
            }
            !consumed
        });
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
            Self::check(&sb, auth)?;
            if let Some(uid) = sb.teardown() {
                self.free_uid(uid);
            }
            self.free_uid(sb.uid);
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
            sb.state = State::Failed("VMM exited".into());
        }
        let mut detail = BTreeMap::new();
        detail.insert("generation".into(), sb.generation.to_string());
        detail.insert("source".into(), sb.source.clone());
        detail.insert("boundary".into(), "vm".into());
        detail.insert("engine".into(), ENGINE_NAME.into());
        detail.insert("vcpus".into(), sb.resources.vcpus.to_string());
        detail.insert("memory_mib".into(), sb.resources.memory_mib.to_string());
        detail.insert("disk_gib".into(), sb.resources.disk_gib.to_string());
        if let State::Failed(why) = &sb.state {
            detail.insert("failure".into(), why.clone());
        }
        if let Some(pid) = sb.jail.as_ref().and_then(|j| j.pid) {
            detail.insert("vmm_pid".into(), pid.to_string());
            detail.insert("vmm_uid".into(), sb.uid.to_string());
        }
        let command = if command_operation_id.is_empty() {
            None
        } else {
            let table = sb.commands.lock().unwrap();
            let rec =
                table.0.get(command_operation_id).cloned().ok_or_else(|| {
                    EngineError::not_found(format!("command {command_operation_id}"))
                })?;
            let (state, done) = match (&rec.done, &rec.lost) {
                (Some(d), _) => ("exited", d.clone()),
                (None, Some(why)) => {
                    detail.insert("command_lost".into(), why.clone());
                    ("unknown", pending(&rec.started))
                }
                (None, None) => ("running", pending(&rec.started)),
            };
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
        let all: Vec<_> = self.sandboxes.lock().unwrap().drain().collect();
        let n = all.len();
        for (_, sb) in all {
            let _ = sb.lock().unwrap().teardown();
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

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::{Read, Write};
    use std::os::unix::net::UnixListener;

    /// A fake guest channel: each connection gets the next scripted reply
    /// (`None` drops the connection after reading the request). Returns the
    /// requests it received.
    fn fake_guest(
        tag: &str,
        replies: Vec<Option<GuestResponse>>,
    ) -> (PathBuf, std::thread::JoinHandle<Vec<GuestRequest>>) {
        let path = std::env::temp_dir().join(format!("lamp-eng-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_file(&path);
        let listener = UnixListener::bind(&path).unwrap();
        let server = std::thread::spawn(move || {
            let mut got = Vec::new();
            for reply in replies {
                let (mut s, _) = listener.accept().unwrap();
                let mut line = [0_u8; 13];
                s.read_exact(&mut line).unwrap();
                s.write_all(b"OK 1024\n").unwrap();
                got.push(lamp_init::read_frame::<GuestRequest>(&mut s).unwrap());
                if let Some(r) = reply {
                    lamp_init::write_frame(&mut s, &r).unwrap();
                }
            }
            got
        });
        (path, server)
    }

    /// R4: a freeze whose reply is lost may still have applied, so it is
    /// followed by a thaw; a confirmed thaw leaves the sandbox usable.
    #[test]
    fn uncertain_freeze_is_thawed() {
        let (path, server) = fake_guest("freeze-lost", vec![None, Some(GuestResponse::Thawed)]);
        let (e, thaw_failed) = freeze_guest(&path).unwrap_err();
        assert_eq!(e.code, "OUTCOME_UNKNOWN");
        assert!(thaw_failed.is_none(), "thaw confirmed");
        let got = server.join().unwrap();
        assert_eq!(
            got,
            vec![GuestRequest::Freeze(Empty {}), GuestRequest::Thaw(Empty {})]
        );
        let _ = std::fs::remove_file(path);
    }

    /// R4: when no thaw can be confirmed the caller is told, so it marks
    /// the sandbox failed instead of running with frozen tasks.
    #[test]
    fn unconfirmed_thaw_after_uncertain_freeze_is_reported() {
        let wrong = Some(GuestResponse::Pong(lamp_init::Status {
            sandbox_id: String::new(),
            generation: 0,
            kernel_boot_id: String::new(),
        }));
        let (path, server) = fake_guest("thaw-lost", vec![wrong, None, None, None]);
        let (_, thaw_failed) = freeze_guest(&path).unwrap_err();
        assert!(thaw_failed.is_some());
        let got = server.join().unwrap();
        assert_eq!(
            got.iter()
                .filter(|r| **r == GuestRequest::Thaw(Empty {}))
                .count(),
            3
        );
        let _ = std::fs::remove_file(path);
    }

    fn rec() -> CommandRec {
        CommandRec {
            started: ExecStarted {
                pid: 1,
                started_unix_ns: 1,
            },
            done: None,
            lost: None,
        }
    }

    /// R2: a long-running command stays observable however many short
    /// commands complete after it; only completed ones are forgotten.
    #[test]
    fn running_commands_are_never_evicted() {
        let mut table = CommandTable::default();
        remember(&mut table, "op_dev_server", rec());
        for i in 0..COMMAND_MEMORY * 2 {
            let op = format!("op_short_{i}");
            remember(&mut table, &op, rec());
            // Its completion arrives, as the waiter records it.
            table.0.get_mut(&op).unwrap().done = Some(pending(&ExecStarted {
                pid: 2,
                started_unix_ns: 2,
            }));
            forget_old_completed(&mut table);
        }
        assert!(table.0.contains_key("op_dev_server"));
        assert!(!table.0["op_dev_server"].completed());
        assert_eq!(running(&table), 1);
        assert_eq!(table.0.len(), COMMAND_MEMORY + 1);
        assert_eq!(table.1.len(), COMMAND_MEMORY + 1);
        // The newest completed commands are the ones kept.
        assert!(
            table
                .0
                .contains_key(&format!("op_short_{}", COMMAND_MEMORY * 2 - 1))
        );
        assert!(!table.0.contains_key("op_short_0"));
        // Once the long command completes it ages out like any other.
        table.0.get_mut("op_dev_server").unwrap().lost = Some("vmm gone".into());
        forget_old_completed(&mut table);
        assert!(!table.0.contains_key("op_dev_server"));
    }
}
