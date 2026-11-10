//! Sandbox lifecycle on one Apple Silicon Mac (Virtualization.framework).
//!
//! The same engine surface as the Linux engine (`engine.rs`), so the
//! supervisor and `lamp.worker/1` are unchanged. Each sandbox runs in its own
//! confined `lamp-vz worker` process under a dedicated hidden account
//! (`_lampvmNN`); the supervisor (root) only creates its files, spawns it and
//! talks to its two private sockets.
//!
//! Two guest classes:
//! - **Linux** (arm64 Image, ext4 root): prepared create restores a signed
//!   template state; a memory fork freezes the guest's tasks, pauses, saves
//!   the full machine state (all guest RAM, CPU and devices), seals the disk
//!   with APFS `clonefile`, resumes the source and restores the copy into a
//!   new worker with a new Lamp identity. The source keeps running.
//! - **macOS** (owner-built from the owner's restore image): fresh boot
//!   from an owner template, supported save/restore for suspend/resume, and
//!   no memory fork (A2): `preserves_memory=false` captures a disk-only
//!   copy that boots fresh with a new `VZMacMachineIdentifier`. At most
//!   `macos_slots` running + suspended macOS allocations exist per Mac.
//!
//! Store layout (one APFS volume, default `/private/var/lamp`):
//!
//! ```text
//! keys/producer.key            Ed25519 producer seed (hex), root-only
//! templates/<template_id>/     manifest.json provenance.json + objects (0444)
//! templates/names/<name>       symlink to a template id
//! captures/<capture_id>/       disk.img [aux.img] state.vzvmsave meta.json (root 0400)
//! sb/<sandbox_id>/             the worker's private 0700 directory: spec.json,
//!                              disk.img, [aux.img], console.log, api.sock,
//!                              vsock.sock, suspend.vzvmsave, suspended.json
//! ```
//! and volatile capture state on a RAM-backed volume (`ram_dir`).
use crate::error::{EngineError, IoContext, Result};
use crate::fsutil::{self, now_ms, now_ns, random_bytes};
use crate::guest;
use crate::manifest::{
    self, AccelerationVariant, Compatibility, Manifest, Object, Provenance, Resources,
};
use ed25519_dalek::{SigningKey, VerifyingKey};
use lamp_init::{Barrier, Empty, Exec, ExecDone, ExecStarted, GuestRequest, GuestResponse};
use lamp_vz::spec::{Api, ApiReply, DiskSpec, Machine, Sync as DiskSync, WorkerSpec};
use std::collections::{BTreeMap, BTreeSet, HashMap, VecDeque};
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::process::{Child, Command, Stdio};
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Duration, Instant};

#[path = "mac_build.rs"]
pub mod build;

pub const ENGINE_NAME: &str = "vz-macos26";
pub const STATE_ABI: &str = "vz/saved-machine-state";
pub const DEVICE_GRAPH_LINUX: &str =
    "vz-generic:virtio-blk:vda,virtio-vsock,virtio-console:hvc0,virtio-entropy";
pub const DEVICE_GRAPH_MACOS: &str = "vz-mac:virtio-blk:disk0,virtio-vsock,virtio-console,virtio-entropy,mac-graphics:1024x768,mac-keyboard,mac-trackpad";
/// The certified Linux-on-Mac row (fits both lanes, including the 8 GB AIR
/// with a source and a fork running at once).
pub const STANDARD: Resources = Resources {
    vcpus: 2,
    memory_mib: 1024,
    disk_gib: 20,
};
pub const MACOS_SLOTS: usize = 2;
const COMMAND_MEMORY: usize = 256;
const MAX_RUNNING: usize = 1024;
const CAPTURE_AVAILABLE: u64 = 0;
const CAPTURE_RESTORING: u64 = 1;
const CAPTURE_CONSUMED: u64 = 2;
const STATE_FILE: &str = "state.vzvmsave";
const SUSPEND_FILE: &str = "suspend.vzvmsave";
const SUSPEND_META: &str = "suspended.json";
const DISK: &str = "disk.img";
const AUX: &str = "aux.img";
const KERNEL: &str = "Image";
const MACHINE: &str = "machine.json";

#[derive(Debug, Clone)]
pub struct Config {
    pub store: PathBuf,
    /// The signed, hypervisor-entitled `lamp-vz` helper.
    pub worker: PathBuf,
    /// Hidden accounts the workers run as, one per live sandbox.
    pub identities: Vec<String>,
    pub host_id: String,
    /// RAM-backed volume for volatile captures (`None`: on the store).
    pub ram_dir: Option<PathBuf>,
    pub trusted_producers: Vec<VerifyingKey>,
    pub macos_slots: usize,
    /// Apply the worker's Seatbelt profile (always on outside diagnostics).
    pub confine: bool,
}

impl Config {
    pub fn defaults(store: PathBuf) -> Config {
        Config {
            store,
            worker: PathBuf::from("/usr/local/lib/lamp/lamp-vz"),
            identities: (1..=16).map(|i| format!("_lampvm{i:02}")).collect(),
            host_id: hostname(),
            ram_dir: Some(PathBuf::from("/private/var/lamp/ram")),
            trusted_producers: Vec::new(),
            macos_slots: MACOS_SLOTS,
            confine: true,
        }
    }
}

pub fn hostname() -> String {
    sysctl("kern.hostname").unwrap_or_else(|| "localhost".into())
}

fn sysctl(name: &str) -> Option<String> {
    let out = Command::new("/usr/sbin/sysctl")
        .arg("-n")
        .arg(name)
        .output()
        .ok()?;
    out.status
        .success()
        .then(|| String::from_utf8_lossy(&out.stdout).trim().to_string())
}

fn os_build() -> String {
    Command::new("/usr/bin/sw_vers")
        .arg("-buildVersion")
        .output()
        .map(|o| String::from_utf8_lossy(&o.stdout).trim().to_string())
        .unwrap_or_default()
}

/// Host facts saved state must match before the framework parses it.
/// VZ saved state is valid only on the same Mac, OS build and user context,
/// so all three are part of the baseline.
#[derive(Debug, Clone)]
pub struct HostCompat {
    pub engine_digest: String,
    pub cpu_baseline: String,
    pub os_build: String,
    pub cpu_model: String,
}

impl HostCompat {
    pub fn detect(worker: &Path) -> Result<HostCompat> {
        let worker_digest = fsutil::digest_file(worker)?.digest;
        let build = os_build();
        let engine_digest = manifest::sha256_hex(
            format!("{worker_digest}|Virtualization.framework|{build}").as_bytes(),
        );
        let cpu_model = sysctl("machdep.cpu.brand_string").unwrap_or_default();
        let hw = sysctl("hw.model").unwrap_or_default();
        let uuid = Command::new("/usr/sbin/ioreg")
            .args(["-rd1", "-c", "IOPlatformExpertDevice"])
            .output()
            .ok()
            .and_then(|o| {
                String::from_utf8_lossy(&o.stdout)
                    .lines()
                    .find(|l| l.contains("IOPlatformUUID"))
                    .map(|l| l.split('"').nth(3).unwrap_or("").to_string())
            })
            .unwrap_or_default();
        Ok(HostCompat {
            engine_digest,
            cpu_baseline: manifest::sha256_hex(format!("{cpu_model}|{hw}|{uuid}").as_bytes()),
            os_build: build,
            cpu_model,
        })
    }
}

/// Guest-class facts a restore needs, stored as the variant's `vmstate`
/// object (`machine.json`).
#[derive(Debug, Clone, PartialEq, Eq, serde::Serialize, serde::Deserialize)]
#[serde(tag = "guest", rename_all = "snake_case", deny_unknown_fields)]
pub enum MachineCfg {
    Linux {
        cmdline: String,
        /// The hardware identity the saved state was taken with. VZ refuses
        /// to restore a state under any other (`invalid argument`); it is
        /// not guest-visible (no DMI/serial), and the Lamp identity barrier
        /// gives every copy its own hostname, machine-id and CRNG.
        machine_identifier_b64: String,
        resources: Resources,
    },
    Macos {
        hardware_model_b64: String,
        minimum_resources: Resources,
        default_resources: Resources,
        os_version: String,
        os_build: String,
    },
}

#[derive(Debug)]
pub struct Template {
    pub id: String,
    pub dir: PathBuf,
    pub manifest: Manifest,
    pub manifest_digest: String,
    pub variant: Option<AccelerationVariant>,
    pub machine: MachineCfg,
}

impl Template {
    pub fn guest_os(&self) -> &str {
        &self.manifest.guest_os
    }
}

pub type CommandTable = (HashMap<String, CommandRec>, VecDeque<String>);

#[derive(Debug, Clone)]
pub struct CommandRec {
    pub started: ExecStarted,
    pub done: Option<ExecDone>,
    pub lost: Option<String>,
}

impl CommandRec {
    fn completed(&self) -> bool {
        self.done.is_some() || self.lost.is_some()
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum State {
    Allocated,
    Running,
    Suspended,
    Stopped,
    Failed(String),
}

impl State {
    pub fn name(&self) -> &'static str {
        match self {
            State::Allocated => "allocated",
            State::Running => "running",
            State::Suspended => "suspended",
            State::Stopped => "stopped",
            State::Failed(_) => "failed",
        }
    }
}

#[derive(Debug, Clone)]
pub struct Identity {
    pub name: String,
    pub uid: u32,
    pub gid: u32,
}

fn lookup_user(name: &str) -> Option<Identity> {
    let c = std::ffi::CString::new(name).ok()?;
    // SAFETY: getpwnam returns a static record or NULL.
    let pw = unsafe { libc::getpwnam(c.as_ptr()) };
    if pw.is_null() {
        return None;
    }
    // SAFETY: non-null record from getpwnam.
    let (uid, gid) = unsafe { ((*pw).pw_uid, (*pw).pw_gid) };
    Some(Identity {
        name: name.to_string(),
        uid,
        gid,
    })
}

struct Worker {
    child: Child,
    api: PathBuf,
    vsock: PathBuf,
}

impl Worker {
    fn alive(&mut self) -> bool {
        matches!(self.child.try_wait(), Ok(None))
    }

    fn call(&self, req: &Api, timeout: Duration) -> Result<ApiReply> {
        let mut s = UnixStream::connect(&self.api)
            .map_err(|e| EngineError::host(format!("worker api connect: {e}")))?;
        s.set_read_timeout(Some(timeout))?;
        let mut line = serde_json::to_string(req).map_err(|e| EngineError::host(e.to_string()))?;
        line.push('\n');
        s.write_all(line.as_bytes())
            .map_err(|e| EngineError::host(format!("worker api send: {e}")))?;
        let mut reply = String::new();
        BufReader::new(s)
            .read_line(&mut reply)
            .map_err(|e| EngineError::new("OUTCOME_UNKNOWN", format!("worker api reply: {e}")))?;
        let r: ApiReply = serde_json::from_str(&reply)
            .map_err(|e| EngineError::new("OUTCOME_UNKNOWN", format!("worker api reply: {e}")))?;
        if !r.ok {
            return Err(EngineError::host(format!("worker: {}", r.error)));
        }
        Ok(r)
    }

    fn kill(&mut self) {
        if self.alive() {
            let _ = self.call(&Api::Exit {}, Duration::from_secs(10));
        }
        let deadline = Instant::now() + Duration::from_secs(10);
        while self.alive() && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(20));
        }
        if self.alive() {
            let _ = self.child.kill();
        }
        let _ = self.child.wait();
    }

    fn liveness(&self) -> impl Fn() -> bool + Send + 'static {
        let pid = self.child.id() as libc::pid_t;
        // SAFETY: signal 0 only probes the process.
        move || unsafe { libc::kill(pid, 0) == 0 }
    }
}

pub struct Sandbox {
    pub id: String,
    pub generation: u64,
    pub policy_revision: u64,
    pub resources: Resources,
    pub state: State,
    pub guest_os: String,
    pub dir: PathBuf,
    pub identity: Option<Identity>,
    worker: Option<Worker>,
    pub machine: Option<WorkerSpec>,
    pub source: String,
    pub captures: Vec<String>,
    pub barrier: Option<lamp_init::BarrierAck>,
    pub commands: Arc<Mutex<CommandTable>>,
    /// Detail of the restore/boot that produced the current generation.
    pub last_start: BTreeMap<String, String>,
}

#[derive(Debug)]
pub struct CaptureRec {
    pub id: String,
    pub dir: PathBuf,
    /// Saved machine state; `None` for a disk-only capture.
    pub state: Option<PathBuf>,
    pub source_sandbox: String,
    pub source_generation: u64,
    pub resources: Resources,
    pub durability: String,
    pub preserves_memory: bool,
    pub spec: WorkerSpec,
    pub guest_os: String,
    pub restores: Mutex<u64>,
}

#[derive(Debug, Default, Clone)]
pub struct Outcome {
    pub state: String,
    pub handles: Vec<String>,
    pub command: Option<ExecDone>,
    pub detail: BTreeMap<String, String>,
}

#[derive(Debug, Clone, Copy)]
pub struct Authority<'a> {
    pub sandbox_id: &'a str,
    pub generation: u64,
    pub policy_revision: u64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Mode {
    Daemon,
    Offline,
    ReadOnly,
}

pub struct Engine {
    pub cfg: Config,
    pub compat: HostCompat,
    producer: SigningKey,
    trusted: HashMap<String, VerifyingKey>,
    templates: RwLock<HashMap<String, Arc<Template>>>,
    sandboxes: Mutex<HashMap<String, Arc<Mutex<Sandbox>>>>,
    captures: Mutex<HashMap<String, Arc<CaptureRec>>>,
    identities: Mutex<BTreeSet<String>>,
    /// Sandbox IDs holding one of the per-Mac macOS slots (running,
    /// restoring or suspended). Checked and taken under this one lock.
    macos_slots: Mutex<BTreeSet<String>>,
    _lock: Option<std::fs::File>,
}

fn running(table: &CommandTable) -> usize {
    table.0.values().filter(|r| !r.completed()).count()
}

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

fn chown_tree(path: &Path, uid: u32, gid: u32) -> Result<()> {
    fsutil::chown(path, uid, gid)?;
    if path.is_dir() {
        for e in std::fs::read_dir(path).ctx("list dir")?.flatten() {
            if e.file_type().map(|t| t.is_symlink()).unwrap_or(true) {
                continue;
            }
            chown_tree(&e.path(), uid, gid)?;
        }
    }
    Ok(())
}

/// Durable rename: the file is F_FULLFSYNCed, renamed, then the directory
/// is F_FULLFSYNCed, so the name and the bytes are both on stable storage.
fn durable_write(path: &Path, bytes: &[u8]) -> Result<()> {
    let tmp = path.with_extension("tmp");
    std::fs::write(&tmp, bytes).ctx(format!("write {}", tmp.display()))?;
    fsutil::full_fsync(&tmp)?;
    crash_point("suspend.meta_written");
    std::fs::rename(&tmp, path).ctx(format!("rename {}", path.display()))?;
    crash_point("suspend.meta_renamed");
    fsutil::full_fsync(path.parent().unwrap())
}

/// Whether the Mac's console session is locked (or nobody is logged in at
/// the console). Virtualization.framework seals every saved machine state
/// with a Secure Enclave key of the console session: a state saved while
/// locked is fine, but none can be restored again until the session is
/// unlocked (T8 finding; VZ reports "permission denied").
pub fn console_locked() -> bool {
    let Ok(out) = Command::new("/usr/sbin/ioreg")
        .args(["-n", "Root", "-d1", "-a"])
        .output()
    else {
        return false;
    };
    console_locked_in(&String::from_utf8_lossy(&out.stdout))
}

fn plist_true_after(plist: &str, key: &str) -> Option<bool> {
    let i = plist.find(&format!("<key>{key}</key>"))? + key.len() + 11;
    Some(plist[i..].trim_start().starts_with("<true/>"))
}

fn console_locked_in(plist: &str) -> bool {
    let on_console = plist_true_after(plist, "kCGSSessionOnConsoleKey").unwrap_or(false);
    let locked = plist_true_after(plist, "CGSSessionScreenIsLocked").unwrap_or(false);
    !on_console || locked
}

fn refuse_if_locked(what: &str) -> Result<()> {
    if console_locked() {
        return Err(EngineError::new(
            "HOST_UNAVAILABLE",
            format!(
                "{what} needs this Mac's console session unlocked: Virtualization.framework cannot open saved machine state while it is locked (state kept; retry after unlock)"
            ),
        ));
    }
    Ok(())
}

/// Process-crash fault injection for the APFS commit tests: when
/// `LAMP_TEST_CRASH_AT` names this point, the process ends at once with
/// status 79 (no unwinding, no cleanup, nothing flushed by Lamp).
pub fn crash_point(name: &str) {
    if std::env::var_os("LAMP_TEST_CRASH_AT").is_some_and(|v| v == name) {
        // SAFETY: _exit only ends the process.
        unsafe { libc::_exit(79) };
    }
}

impl Engine {
    pub fn open(cfg: Config, mode: Mode) -> Result<Engine> {
        let lock = if mode == Mode::ReadOnly {
            None
        } else {
            std::fs::create_dir_all(&cfg.store).ctx("create store")?;
            Some(fsutil::lock_exclusive(&cfg.store.join("lock")).map_err(|e| {
                EngineError::in_use(format!(
                    "the store at {} is owned by another process (stop lamp-supervisor first): {}",
                    cfg.store.display(),
                    e.message
                ))
            })?)
        };
        let producer = if mode == Mode::ReadOnly {
            load_key(&cfg.store.join("keys/producer.key"))?
        } else {
            for sub in [
                "templates/names",
                "captures",
                "sb",
                "staging",
                "grants",
                "keys",
            ] {
                std::fs::create_dir_all(cfg.store.join(sub)).ctx(format!("create store/{sub}"))?;
            }
            for private in ["captures", "grants", "keys"] {
                fsutil::chmod(&cfg.store.join(private), 0o700)?;
            }
            // Workers traverse store/, sb/ and staging/ (their own directory
            // is 0700; a template being built exposes only its kernel).
            fsutil::chmod(&cfg.store, 0o755)?;
            fsutil::chmod(&cfg.store.join("sb"), 0o711)?;
            fsutil::chmod(&cfg.store.join("staging"), 0o711)?;
            fsutil::chmod(&cfg.store.join("templates"), 0o755)?;
            if let Some(ram) = &cfg.ram_dir {
                std::fs::create_dir_all(ram).ctx("create RAM capture dir")?;
                fsutil::chmod(ram, 0o711)?;
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
        let compat = HostCompat::detect(&cfg.worker)?;
        let engine = Engine {
            cfg,
            compat,
            producer,
            trusted,
            templates: RwLock::new(HashMap::new()),
            sandboxes: Mutex::new(HashMap::new()),
            captures: Mutex::new(HashMap::new()),
            identities: Mutex::new(BTreeSet::new()),
            macos_slots: Mutex::new(BTreeSet::new()),
            _lock: lock,
        };
        if mode == Mode::Daemon {
            engine.recover();
        }
        engine.load_templates(mode != Mode::ReadOnly);
        Ok(engine)
    }

    /// Daemon start: stops leftover workers, discards volatile captures and
    /// unfinished sandboxes, and **keeps** durably suspended sandboxes (they
    /// still hold their macOS slot and resume with a new generation).
    fn recover(&self) {
        // Only this store's workers (another store may share the Mac). They
        // must be gone, with the Virtualization service each one owns, before
        // any disk they held is reused: VZ refuses a disk another VM holds.
        let pattern = format!(
            "^{} worker --spec {}/sb/",
            self.cfg.worker.display(),
            self.cfg.store.display()
        );
        let _ = Command::new("/usr/bin/pkill")
            .args(["-KILL", "-f", &pattern])
            .status();
        let deadline = Instant::now() + Duration::from_secs(30);
        let alive = |args: &[&str]| {
            Command::new("/usr/bin/pgrep")
                .args(args)
                .stdout(Stdio::null())
                .status()
                .is_ok_and(|s| s.success())
        };
        while Instant::now() < deadline
            && (alive(&["-f", &pattern])
                || self.cfg.identities.iter().any(|u| {
                    lookup_user(u).is_some()
                        && alive(&["-U", u, "-f", "com.apple.Virtualization.VirtualMachine"])
                }))
        {
            std::thread::sleep(Duration::from_millis(20));
        }
        for dir in ["captures", "staging"] {
            let p = self.cfg.store.join(dir);
            let _ = std::fs::remove_dir_all(&p);
            let _ = std::fs::create_dir_all(&p);
            let _ = fsutil::chmod(&p, 0o700);
        }
        if let Some(ram) = &self.cfg.ram_dir {
            for e in std::fs::read_dir(ram).into_iter().flatten().flatten() {
                let _ = std::fs::remove_dir_all(e.path());
            }
        }
        for e in std::fs::read_dir(self.cfg.store.join("sb"))
            .into_iter()
            .flatten()
            .flatten()
        {
            let dir = e.path();
            let id = e.file_name().to_string_lossy().into_owned();
            match self.load_suspended(&id, &dir) {
                Some(sb) => {
                    if sb.guest_os == "macos" {
                        self.macos_slots.lock().unwrap().insert(id.clone());
                    }
                    eprintln!("lamp-engine: kept suspended sandbox {id}");
                    self.sandboxes
                        .lock()
                        .unwrap()
                        .insert(id, Arc::new(Mutex::new(sb)));
                }
                None => {
                    let _ = std::fs::remove_dir_all(&dir);
                }
            }
        }
    }

    fn load_suspended(&self, id: &str, dir: &Path) -> Option<Sandbox> {
        let meta: serde_json::Value =
            serde_json::from_slice(&std::fs::read(dir.join(SUSPEND_META)).ok()?).ok()?;
        let spec: WorkerSpec = serde_json::from_value(meta.get("spec")?.clone()).ok()?;
        if !dir.join(SUSPEND_FILE).exists() || meta["sandbox_id"] != id {
            return None;
        }
        let resources: Resources = serde_json::from_value(meta.get("resources")?.clone()).ok()?;
        Some(Sandbox {
            id: id.into(),
            generation: meta["generation"].as_u64()?,
            policy_revision: meta["policy_revision"].as_u64()?,
            resources,
            state: State::Suspended,
            guest_os: meta["guest_os"].as_str()?.into(),
            dir: dir.to_path_buf(),
            identity: None,
            worker: None,
            machine: Some(spec),
            source: meta["source"].as_str().unwrap_or("").into(),
            captures: Vec::new(),
            barrier: None,
            commands: Arc::default(),
            last_start: meta["machine_id"]
                .as_str()
                .map(|m| BTreeMap::from([("machine_id".to_string(), m.to_string())]))
                .unwrap_or_default(),
        })
    }

    pub fn producer_key(&self) -> &SigningKey {
        &self.producer
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
    /// object digest before any saved state can reach the framework parser.
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
        let machine: MachineCfg = serde_json::from_slice(&read(MACHINE)?)
            .map_err(|e| EngineError::new("DATA_UNAVAILABLE", format!("machine: {e}")))?;
        let variant = match manifest.guest_os.as_str() {
            "linux" => {
                let v = manifest
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
                let c = &v.compatibility;
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
                fsutil::verify_object(&dir.join(KERNEL), &manifest.kernel, "kernel")?;
                fsutil::verify_object(&dir.join("root.ext4"), &manifest.root, "root")?;
                fsutil::verify_object(&dir.join(DISK), &v.disk, "variant disk")?;
                fsutil::verify_object(&dir.join(STATE_FILE), &v.memory, "variant memory")?;
                fsutil::verify_object(&dir.join(MACHINE), &v.vmstate, "variant machine")?;
                Some(v)
            }
            "macos" => {
                // No saved-state variant: an owner template always boots
                // fresh. `kernel` names the auxiliary (boot) storage.
                if provenance.device_graph_digest
                    != manifest::sha256_hex(DEVICE_GRAPH_MACOS.as_bytes())
                {
                    return Err(EngineError::new(
                        "DATA_UNAVAILABLE",
                        "provenance does not bind the device graph",
                    ));
                }
                fsutil::verify_object(&dir.join(AUX), &manifest.kernel, "auxiliary storage")?;
                fsutil::verify_object(&dir.join(DISK), &manifest.root, "macOS disk")?;
                None
            }
            other => {
                return Err(EngineError::new(
                    "ENGINE_UNQUALIFIED",
                    format!("guest_os {other} is not a Mac guest class"),
                ));
            }
        };
        Ok(Template {
            id,
            dir: dir.to_path_buf(),
            manifest,
            manifest_digest: digest,
            variant,
            machine,
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

    fn alloc_identity(&self) -> Result<Identity> {
        let mut used = self.identities.lock().unwrap();
        for name in &self.cfg.identities {
            if used.contains(name) {
                continue;
            }
            if let Some(id) = lookup_user(name) {
                used.insert(name.clone());
                return Ok(id);
            }
        }
        Err(EngineError::new(
            "NO_CAPACITY",
            "no free worker identity (install more _lampvm accounts)",
        ))
    }

    fn free_identity(&self, id: &Identity) {
        self.identities.lock().unwrap().remove(&id.name);
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

    /// Number of macOS allocations holding a slot (running, restoring or
    /// suspended) and the cap.
    pub fn macos_slots(&self) -> (usize, usize) {
        (self.macos_slots.lock().unwrap().len(), self.cfg.macos_slots)
    }

    fn take_macos_slot(&self, sandbox_id: &str) -> Result<()> {
        let mut slots = self.macos_slots.lock().unwrap();
        if slots.contains(sandbox_id) {
            return Ok(());
        }
        if slots.len() >= self.cfg.macos_slots {
            return Err(EngineError::new(
                "NO_CAPACITY",
                format!(
                    "this Mac already holds {} running or suspended macOS allocations (limit {})",
                    slots.len(),
                    self.cfg.macos_slots
                ),
            ));
        }
        slots.insert(sandbox_id.to_string());
        Ok(())
    }

    fn free_macos_slot(&self, sandbox_id: &str) {
        self.macos_slots.lock().unwrap().remove(sandbox_id);
    }

    /// Reserves the sandbox record. Identity, files and the worker come with
    /// Restore, once the template or capture (and so the guest class) is known.
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
        if resources.vcpus == 0 || resources.vcpus > 16 {
            return Err(EngineError::invalid("vcpus must be 1..=16"));
        }
        if resources.memory_mib < 256 || resources.disk_gib == 0 {
            return Err(EngineError::invalid(
                "memory_mib must be ≥256 and disk_gib ≥1",
            ));
        }
        let mut all = self.sandboxes.lock().unwrap();
        if all.contains_key(auth.sandbox_id) {
            return Err(EngineError::in_use(format!(
                "sandbox {} exists",
                auth.sandbox_id
            )));
        }
        all.insert(
            auth.sandbox_id.to_string(),
            Arc::new(Mutex::new(Sandbox {
                id: auth.sandbox_id.into(),
                generation: auth.generation,
                policy_revision: auth.policy_revision,
                resources,
                state: State::Allocated,
                guest_os: String::new(),
                dir: self.cfg.store.join("sb").join(auth.sandbox_id),
                identity: None,
                worker: None,
                machine: None,
                source: String::new(),
                captures: Vec::new(),
                barrier: None,
                commands: Arc::default(),
                last_start: BTreeMap::new(),
            })),
        );
        Ok(Outcome {
            state: "allocated".into(),
            ..Default::default()
        })
    }

    /// Restores into an allocated sandbox (template create, memory fork,
    /// disk-only fork) or resumes a suspended one (`auth.generation` must
    /// be its next generation). The identity barrier runs before any
    /// command is admitted.
    pub fn restore(&self, auth: Authority<'_>, capture_id: &str) -> Result<Outcome> {
        let t0 = Instant::now();
        let arc = self.sandbox(auth.sandbox_id)?;
        let mut sb = arc.lock().unwrap();
        if sb.state == State::Suspended {
            return self.resume(&mut sb, auth, capture_id, t0);
        }
        Self::check(&sb, auth)?;
        if sb.state != State::Allocated {
            return Err(EngineError::in_use(format!(
                "sandbox is {}, not allocated",
                sb.state.name()
            )));
        }
        let capture = self.captures.lock().unwrap().get(capture_id).cloned();
        let result = match &capture {
            Some(c) => {
                if sb.resources != c.resources {
                    return Err(mismatch(&c.resources, &sb.resources));
                }
                {
                    let mut n = c.restores.lock().unwrap();
                    if *n != CAPTURE_AVAILABLE {
                        return Err(EngineError::not_found(format!(
                            "capture {capture_id} was already restored"
                        )));
                    }
                    *n = CAPTURE_RESTORING;
                }
                if c.guest_os == "macos"
                    && let Err(e) = self.take_macos_slot(&sb.id)
                {
                    *c.restores.lock().unwrap() = CAPTURE_AVAILABLE;
                    return Err(e);
                }
                sb.guest_os = c.guest_os.clone();
                self.restore_capture(&mut sb, c, t0)
            }
            None => {
                let t = self.template(capture_id)?;
                match &t.machine {
                    MachineCfg::Linux { resources, .. } => {
                        if &sb.resources != resources {
                            return Err(mismatch(resources, &sb.resources));
                        }
                    }
                    MachineCfg::Macos {
                        minimum_resources: min,
                        ..
                    } => {
                        if sb.resources.vcpus < min.vcpus
                            || sb.resources.memory_mib < min.memory_mib
                            || sb.resources.disk_gib < min.disk_gib
                        {
                            return Err(EngineError::invalid(format!(
                                "macOS template needs at least {}/{} MiB/{} GiB",
                                min.vcpus, min.memory_mib, min.disk_gib
                            )));
                        }
                        self.take_macos_slot(&sb.id)?;
                    }
                }
                sb.guest_os = t.guest_os().to_string();
                self.restore_template(&mut sb, &t, t0)
            }
        };
        match result {
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
                if let Some(mut w) = sb.worker.take() {
                    w.kill();
                }
                if let Some(id) = sb.identity.take() {
                    self.free_identity(&id);
                }
                self.free_macos_slot(&sb.id);
                if let Some(c) = &capture {
                    *c.restores.lock().unwrap() = CAPTURE_AVAILABLE;
                }
                sb.state = State::Failed(e.message.clone());
                Err(e)
            }
        }
    }

    fn new_dir(&self, sb: &mut Sandbox) -> Result<Identity> {
        let identity = self.alloc_identity()?;
        let _ = std::fs::remove_dir_all(&sb.dir);
        std::fs::create_dir(&sb.dir).ctx("create sandbox dir")?;
        fsutil::chmod(&sb.dir, 0o700)?;
        sb.identity = Some(identity.clone());
        Ok(identity)
    }

    fn ram_dir(&self, sandbox_id: &str) -> Option<PathBuf> {
        self.cfg.ram_dir.as_ref().map(|r| r.join(sandbox_id))
    }

    /// Writes the spec, hands the directory to the worker identity and
    /// starts the confined worker; returns its ready report.
    fn spawn(
        &self,
        sb: &mut Sandbox,
        mut spec: WorkerSpec,
        restore: Option<&Path>,
        detail: &mut BTreeMap<String, String>,
    ) -> Result<()> {
        let identity = sb
            .identity
            .clone()
            .ok_or_else(|| EngineError::host("no worker identity"))?;
        spec.dir = sb.dir.clone();
        spec.sandbox_id = sb.id.clone();
        spec.write_dirs.clear();
        if let Some(ram) = self.ram_dir(&sb.id) {
            let _ = std::fs::remove_dir_all(&ram);
            std::fs::create_dir(&ram).ctx("create RAM capture dir")?;
            fsutil::chmod(&ram, 0o700)?;
            fsutil::chown(&ram, identity.uid, identity.gid)?;
            spec.write_dirs.push(ram);
        }
        std::fs::write(
            sb.dir.join("spec.json"),
            serde_json::to_vec_pretty(&spec).unwrap(),
        )
        .ctx("write worker spec")?;
        chown_tree(&sb.dir, identity.uid, identity.gid)?;
        let t = Instant::now();
        let mut cmd = Command::new(&self.cfg.worker);
        cmd.arg("worker")
            .arg("--spec")
            .arg(sb.dir.join("spec.json"));
        if let Some(r) = restore {
            cmd.arg("--restore").arg(r).arg("--paused");
        }
        if !self.cfg.confine {
            cmd.arg("--no-confine");
        }
        use std::os::unix::process::CommandExt;
        let (uid, gid) = (identity.uid, identity.gid);
        cmd.env_clear()
            .env("PATH", "/usr/bin:/bin")
            .env("HOME", "/var/empty")
            .env("USER", &identity.name)
            .current_dir("/")
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(
                std::fs::File::create(sb.dir.join("worker.log"))
                    .map(Stdio::from)
                    .unwrap_or(Stdio::null()),
            );
        // SAFETY: only async-signal-safe calls between fork and exec.
        unsafe {
            cmd.pre_exec(move || {
                if libc::setgroups(0, std::ptr::null()) != 0
                    || libc::setgid(gid) != 0
                    || libc::setuid(uid) != 0
                {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let mut child = cmd.spawn().ctx("spawn worker")?;
        let stdout = child.stdout.take().unwrap();
        let (tx, rx) = std::sync::mpsc::channel();
        std::thread::spawn(move || {
            let mut line = String::new();
            let _ = BufReader::new(stdout).read_line(&mut line);
            let _ = tx.send(line);
        });
        let line = rx
            .recv_timeout(Duration::from_secs(120))
            .unwrap_or_default();
        let ready: serde_json::Value =
            serde_json::from_str(&line).unwrap_or(serde_json::Value::Null);
        if ready["ready"] != true {
            let _ = child.kill();
            let _ = child.wait();
            let log = std::fs::read_to_string(sb.dir.join("worker.log")).unwrap_or_default();
            return Err(EngineError::host(format!(
                "worker did not start: {}",
                log.trim()
            )));
        }
        detail.insert("spawn_us".into(), us(t.elapsed()));
        if let Some(v) = ready["vm_start_us"].as_u64() {
            detail.insert(
                if restore.is_some() {
                    "vz_restore_us"
                } else {
                    "vz_start_us"
                }
                .into(),
                v.to_string(),
            );
        }
        detail.insert("worker_pid".into(), child.id().to_string());
        detail.insert("worker_identity".into(), identity.name.clone());
        detail.insert("confined".into(), ready["confined"].to_string());
        sb.worker = Some(Worker {
            child,
            api: sb.dir.join("api.sock"),
            vsock: sb.dir.join("vsock.sock"),
        });
        sb.machine = Some(spec);
        Ok(())
    }

    fn worker(sb: &Sandbox) -> Result<&Worker> {
        sb.worker
            .as_ref()
            .ok_or_else(|| EngineError::in_use("sandbox has no worker"))
    }

    fn finish_start(
        &self,
        sb: &mut Sandbox,
        resumed: bool,
        detail: &mut BTreeMap<String, String>,
    ) -> Result<()> {
        let w = Self::worker(sb)?;
        if resumed {
            let t = Instant::now();
            w.call(&Api::Resume {}, Duration::from_secs(60))?;
            detail.insert("resume_us".into(), us(t.elapsed()));
        }
        let t = Instant::now();
        let vsock = w.vsock.clone();
        guest::wait_ready(
            &vsock,
            Duration::from_secs(if sb.guest_os == "macos" { 600 } else { 60 }),
        )
        .map_err(|e| e.context("guest helper"))?;
        detail.insert("helper_ready_us".into(), us(t.elapsed()));
        let t = Instant::now();
        // A resumed sandbox keeps its own identity (the suspend record
        // carries it across a supervisor restart); a new machine gets one.
        let keep = sb
            .barrier
            .as_ref()
            .map(|b| b.machine_id.clone())
            .or_else(|| sb.last_start.get("machine_id").cloned())
            .filter(|_| resumed);
        let ack = match keep {
            Some(id) => self.barrier_with(&vsock, &sb.id, sb.generation, id)?,
            None => self.barrier(&vsock, &sb.id, sb.generation)?,
        };
        detail.insert("barrier_us".into(), us(t.elapsed()));
        detail.insert("rng_sample".into(), ack.rng_sample_hex.clone());
        detail.insert("machine_id".into(), ack.machine_id.clone());
        detail.insert("audit".into(), "none".into());
        sb.barrier = Some(ack);
        sb.state = State::Running;
        sb.last_start = detail.clone();
        Ok(())
    }

    fn restore_template(&self, sb: &mut Sandbox, t: &Template, t0: Instant) -> Result<Outcome> {
        let mut detail = BTreeMap::new();
        self.new_dir(sb)?;
        let disk = sb.dir.join(DISK);
        fsutil::reflink(&t.dir.join(DISK), &disk, 0o600)?;
        let spec = match &t.machine {
            MachineCfg::Linux {
                cmdline,
                machine_identifier_b64,
                ..
            } => WorkerSpec {
                sandbox_id: sb.id.clone(),
                machine: Machine::Linux {
                    kernel: t.dir.join(KERNEL),
                    initrd: None,
                    cmdline: cmdline.clone(),
                    machine_identifier_b64: machine_identifier_b64.clone(),
                },
                cpus: sb.resources.vcpus as usize,
                memory_mib: sb.resources.memory_mib,
                disks: vec![DiskSpec {
                    path: disk,
                    read_only: false,
                    sync: DiskSync::Full,
                }],
                dir: sb.dir.clone(),
                nat_network: false,
                read_paths: vec![],
                write_paths: vec![],
                write_dirs: vec![],
            },
            MachineCfg::Macos {
                hardware_model_b64, ..
            } => {
                // Its own boot storage and a new hardware identity (the
                // supported VZMacMachineIdentifier constructor).
                let aux = sb.dir.join(AUX);
                fsutil::reflink(&t.dir.join(AUX), &aux, 0o600)?;
                WorkerSpec {
                    sandbox_id: sb.id.clone(),
                    machine: Machine::Macos {
                        hardware_model_b64: hardware_model_b64.clone(),
                        machine_identifier_b64: new_mac_machine_identifier(&self.cfg.worker)?,
                        auxiliary_storage: aux,
                    },
                    cpus: sb.resources.vcpus as usize,
                    memory_mib: sb.resources.memory_mib,
                    disks: vec![DiskSpec {
                        path: disk,
                        read_only: false,
                        sync: DiskSync::Full,
                    }],
                    dir: sb.dir.clone(),
                    nat_network: false,
                    read_paths: vec![],
                    write_paths: vec![],
                    write_dirs: vec![],
                }
            }
        };
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        // While the console is locked the prepared state cannot be opened;
        // a Linux template then cold-boots its own sealed disk (about as
        // fast; the receipt says which way it started).
        let locked = t.variant.is_some() && console_locked();
        let state = t
            .variant
            .as_ref()
            .filter(|_| !locked)
            .map(|_| t.dir.join(STATE_FILE));
        let mut spec = spec;
        spec.read_paths.extend(state.clone());
        self.spawn(sb, spec, state.as_deref(), &mut detail)?;
        detail.insert(
            "create_mode".into(),
            match (&state, locked) {
                (Some(_), _) => "prepared_restore",
                (None, true) => "cold_boot_console_locked",
                (None, false) => "fresh_boot",
            }
            .into(),
        );
        self.finish_start(sb, state.is_some(), &mut detail)?;
        Ok(Outcome {
            state: "restored".into(),
            detail,
            ..Default::default()
        })
    }

    fn restore_capture(&self, sb: &mut Sandbox, c: &CaptureRec, t0: Instant) -> Result<Outcome> {
        if c.state.is_some() {
            refuse_if_locked("restoring a memory capture")?;
        }
        let mut detail = BTreeMap::new();
        self.new_dir(sb)?;
        let mut spec = c.spec.clone();
        // The sealed disk (and, for macOS, boot storage) become this
        // sandbox's own files; the capture is consumed.
        std::fs::rename(c.dir.join(DISK), sb.dir.join(DISK)).ctx("take capture disk")?;
        // Sealed read-only while it was a capture; writable as a live disk.
        fsutil::chmod(&sb.dir.join(DISK), 0o600)?;
        spec.disks = vec![DiskSpec {
            path: sb.dir.join(DISK),
            read_only: false,
            sync: DiskSync::Full,
        }];
        if let Machine::Macos {
            auxiliary_storage,
            machine_identifier_b64,
            ..
        } = &mut spec.machine
        {
            std::fs::rename(c.dir.join(AUX), sb.dir.join(AUX)).ctx("take capture boot storage")?;
            fsutil::chmod(&sb.dir.join(AUX), 0o600)?;
            *auxiliary_storage = sb.dir.join(AUX);
            if !c.preserves_memory {
                *machine_identifier_b64 = new_mac_machine_identifier(&self.cfg.worker)?;
            }
        }
        let state = match &c.state {
            Some(s) => {
                let dst = match &self.cfg.ram_dir {
                    // Same RAM volume as the capture, but out of the source
                    // worker's private directory and into a file only this
                    // sandbox's identity owns.
                    Some(ram) if !s.starts_with(&self.cfg.store) => {
                        ram.join(format!("{}.{STATE_FILE}", sb.id))
                    }
                    _ => sb.dir.join(STATE_FILE),
                };
                if &dst != s {
                    std::fs::rename(s, &dst).ctx("take capture state")?;
                }
                Some(dst)
            }
            None => None,
        };
        spec.read_paths = state.iter().cloned().collect();
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        if let Some(s) = &state {
            if let Some(id) = &sb.identity {
                fsutil::chown(s, id.uid, id.gid)?;
            }
        }
        self.spawn(sb, spec, state.as_deref(), &mut detail)?;
        detail.insert(
            "fork_mode".into(),
            if c.preserves_memory {
                "memory"
            } else {
                "disk_only"
            }
            .into(),
        );
        detail.insert("preserves_memory".into(), c.preserves_memory.to_string());
        self.finish_start(sb, state.is_some(), &mut detail)?;
        if let Some(s) = &state
            && s.starts_with(
                self.cfg
                    .ram_dir
                    .as_deref()
                    .unwrap_or(Path::new("/nonexistent")),
            )
        {
            // The worker has read the image; the RAM it held is released.
            let _ = std::fs::remove_file(s);
        }
        Ok(Outcome {
            state: "restored".into(),
            detail,
            ..Default::default()
        })
    }

    fn resume(
        &self,
        sb: &mut Sandbox,
        auth: Authority<'_>,
        capture_id: &str,
        t0: Instant,
    ) -> Result<Outcome> {
        if auth.generation <= sb.generation {
            return Err(EngineError::new(
                "REVISION_CONFLICT",
                format!("resume needs a generation above {}", sb.generation),
            ));
        }
        if capture_id != format!("suspend_{}", sb.id) && capture_id != sb.id {
            return Err(EngineError::not_found(format!(
                "{capture_id} is not this sandbox's suspended state"
            )));
        }
        refuse_if_locked("resuming a suspended sandbox")?;
        if sb.guest_os == "macos" {
            // Already holds its slot while suspended.
            self.take_macos_slot(&sb.id)?;
        }
        let mut detail = BTreeMap::new();
        let identity = self.alloc_identity()?;
        sb.identity = Some(identity);
        let mut spec = sb
            .machine
            .clone()
            .ok_or_else(|| EngineError::host("suspended sandbox has no machine"))?;
        let state = sb.dir.join(SUSPEND_FILE);
        spec.read_paths = vec![state.clone()];
        let prev = (sb.generation, sb.policy_revision);
        sb.generation = auth.generation;
        sb.policy_revision = auth.policy_revision;
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        let r = self
            .spawn(sb, spec, Some(&state), &mut detail)
            .and_then(|_| self.finish_start(sb, true, &mut detail));
        match r {
            Ok(()) => {
                // The saved state is consumed: a resumed machine never
                // resumes the same state twice.
                let _ = std::fs::remove_file(&state);
                let _ = std::fs::remove_file(sb.dir.join(SUSPEND_META));
                let _ = fsutil::full_fsync(&sb.dir);
                detail.insert("total_us".into(), us(t0.elapsed()));
                detail.insert("resumed_generation".into(), sb.generation.to_string());
                Ok(Outcome {
                    state: "resumed".into(),
                    detail,
                    ..Default::default()
                })
            }
            Err(e) => {
                // Saved state stays intact; the sandbox stays suspended at
                // its old generation with repair options (never cold-start).
                if let Some(mut w) = sb.worker.take() {
                    w.kill();
                }
                if let Some(id) = sb.identity.take() {
                    self.free_identity(&id);
                }
                sb.generation = prev.0;
                sb.policy_revision = prev.1;
                sb.state = State::Suspended;
                Err(e.context("resume (saved state kept)"))
            }
        }
    }

    pub fn barrier(
        &self,
        vsock: &Path,
        sandbox_id: &str,
        generation: u64,
    ) -> Result<lamp_init::BarrierAck> {
        self.barrier_with(
            vsock,
            sandbox_id,
            generation,
            lamp_init::hex_encode(&random_bytes::<16>()),
        )
    }

    /// Re-applies a running sandbox's own identity with the current host
    /// time and fresh entropy (after a pause for a capture).
    fn rebarrier(&self, vsock: &Path, sb: &Sandbox) -> Result<lamp_init::BarrierAck> {
        let machine_id = sb
            .barrier
            .as_ref()
            .map(|b| b.machine_id.clone())
            .ok_or_else(|| EngineError::host("sandbox has no applied identity"))?;
        self.barrier_with(vsock, &sb.id, sb.generation, machine_id)
    }

    fn barrier_with(
        &self,
        vsock: &Path,
        sandbox_id: &str,
        generation: u64,
        machine_id: String,
    ) -> Result<lamp_init::BarrierAck> {
        let barrier = Barrier {
            sandbox_id: sandbox_id.into(),
            generation,
            hostname: sandbox_id.replace('_', "-"),
            machine_id,
            entropy_hex: lamp_init::hex_encode(&random_bytes::<64>()),
            unix_time_ns: now_ns(),
        };
        let reply = guest::call(
            vsock,
            &GuestRequest::Barrier(barrier.clone()),
            Duration::from_secs(30),
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
        let (vsock, commands, alive) = {
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
                    format!("{MAX_RUNNING} commands are already running"),
                ));
            }
            let w = Self::worker(&sb)?;
            (w.vsock.clone(), sb.commands.clone(), w.liveness())
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
            audit: None,
        });
        let mut conn = guest::send(&vsock, &request)?;
        let started = match conn.next(Some(Duration::from_secs(30)))? {
            GuestResponse::ExecStarted(s) => s,
            GuestResponse::Error { code, message } if code == "GENERATION_MISMATCH" => {
                return Err(EngineError::new("REVISION_CONFLICT", message));
            }
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
            let result = guest::wait_exec(&vsock, &sandbox_id, generation, &op, None, alive);
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

    pub fn command(&self, sandbox_id: &str, operation_id: &str) -> Result<CommandRec> {
        let commands = self.sandbox(sandbox_id)?.lock().unwrap().commands.clone();
        let table = commands.lock().unwrap();
        table
            .0
            .get(operation_id)
            .cloned()
            .ok_or_else(|| EngineError::not_found(format!("command {operation_id}")))
    }

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

    /// Captures a running sandbox.
    ///
    /// - `preserves_memory=true` on a Linux guest: a memory fork capture
    ///   (`volatile`: state on the RAM volume; `local`: on the store with
    ///   F_FULLFSYNC barriers). The source keeps running.
    /// - `consistency=suspend` (either guest class): durable suspend; the
    ///   source's worker ends and its RAM is released.
    /// - `preserves_memory=false` on a macOS guest: A2 disk-only capture.
    /// - `preserves_memory=true` on macOS without `suspend`: refused, no
    ///   supported independent macOS RAM fork exists.
    pub fn capture(
        self: &Arc<Self>,
        auth: Authority<'_>,
        capture_id: &str,
        consistency: &str,
        preserves_memory: bool,
    ) -> Result<Outcome> {
        let t0 = Instant::now();
        let sb_arc = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb_arc.lock().unwrap();
        Self::check(&sb, auth)?;
        if sb.state != State::Running {
            return Err(EngineError::in_use(format!(
                "sandbox is {}",
                sb.state.name()
            )));
        }
        if consistency == "suspend" {
            return self.suspend(&mut sb, t0);
        }
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
        if sb.guest_os == "macos" && preserves_memory {
            return Err(EngineError::new(
                "DEVICE_NOT_FORKABLE",
                "no supported independent RAM fork for native macOS guests; use fork_mode=disk_only (fresh boot, preserves_memory=false) or suspend",
            ));
        }
        if preserves_memory {
            refuse_if_locked("a memory fork")?;
        }
        if sb.guest_os == "linux" && !preserves_memory {
            return Err(EngineError::invalid(
                "Linux guests fork with memory; disk-only is the macOS A2 path",
            ));
        }
        let cdir = self.cfg.store.join("captures").join(capture_id);
        if self.captures.lock().unwrap().contains_key(capture_id) || cdir.exists() {
            return Err(EngineError::in_use(format!("capture {capture_id} exists")));
        }
        std::fs::create_dir(&cdir).ctx("create capture dir")?;
        fsutil::chmod(&cdir, 0o700)?;
        let discard = |e: EngineError| {
            let _ = std::fs::remove_dir_all(&cdir);
            e.context("capture")
        };
        let mut detail = BTreeMap::new();
        let w = Self::worker(&sb)?;
        let vsock = w.vsock.clone();
        let spec = sb.machine.clone().unwrap();
        let state_tmp = if preserves_memory {
            Some(match (&self.ram_dir(&sb.id), durable) {
                (Some(ram), false) => ram.join(format!("{capture_id}.vzvmsave")),
                _ => sb.dir.join(format!("{capture_id}.vzvmsave")),
            })
        } else {
            None
        };
        if preserves_memory {
            let t = Instant::now();
            if let Err((e, thaw_failed)) = freeze_guest(&vsock) {
                if let Some(t) = thaw_failed {
                    sb.state = State::Failed(format!("tasks may be frozen: {}", t.message));
                }
                return Err(discard(e));
            }
            detail.insert("freeze_us".into(), us(t.elapsed()));
        } else {
            // A2: flush the guest's file system caches so the copy is a
            // clean crash-consistent disk (it boots fresh, no RAM).
            let t = Instant::now();
            let _ = self.run_quiet(&sb, &["/bin/sync"]);
            detail.insert("guest_sync_us".into(), us(t.elapsed()));
        }
        let tp = Instant::now();
        let sealed = w
            .call(&Api::Pause {}, Duration::from_secs(60))
            .and_then(|_| {
                if let Some(path) = &state_tmp {
                    let t = Instant::now();
                    let r = w.call(&Api::Save { path: path.clone() }, Duration::from_secs(600))?;
                    detail.insert("full_write_us".into(), us(t.elapsed()));
                    detail.insert("memory_bytes".into(), r.detail["bytes"].to_string());
                }
                let t = Instant::now();
                fsutil::reflink(&sb.dir.join(DISK), &cdir.join(DISK), 0o400)?;
                if sb.guest_os == "macos" {
                    fsutil::reflink(&sb.dir.join(AUX), &cdir.join(AUX), 0o400)?;
                }
                detail.insert("disk_seal_us".into(), us(t.elapsed()));
                Ok(())
            });
        let resumed = w.call(&Api::Resume {}, Duration::from_secs(60));
        let pause = tp.elapsed();
        // The guest's clock stood still while the machine was paused. The
        // source keeps its identity but gets the host time (and fresh
        // entropy) again before its tasks run: the same barrier, same
        // sandbox, generation and machine id. For Linux it also thaws.
        let t = Instant::now();
        let thawed = match self.rebarrier(&vsock, &sb) {
            Ok(_) => Ok(()),
            Err(e) if preserves_memory => {
                let r = thaw_guest(&vsock).and(Err(e.context("source clock barrier")));
                sb.state = State::Failed("source clock/thaw after capture failed".into());
                r
            }
            Err(e) => Err(e.context("source clock barrier")),
        };
        detail.insert("source_barrier_us".into(), us(t.elapsed()));
        if let Err(e) = sealed.and(resumed.map(|_| ())).and(thawed) {
            if let Some(p) = &state_tmp {
                let _ = std::fs::remove_file(p);
            }
            return Err(discard(e));
        }
        detail.insert("source_pause_us".into(), us(pause));
        let t = Instant::now();
        let state = match &state_tmp {
            Some(p) if p.starts_with(&sb.dir) => {
                let dst = cdir.join(STATE_FILE);
                std::fs::rename(p, &dst).ctx("seal state")?;
                Some(dst)
            }
            other => other.clone(),
        };
        for p in state.iter().chain([&cdir.join(DISK)]) {
            fsutil::chown(p, 0, 0)?;
            fsutil::chmod(p, 0o400)?;
        }
        if durable {
            for p in state.iter().chain([&cdir.join(DISK), &cdir]) {
                fsutil::full_fsync(p)?;
            }
        }
        let meta = serde_json::json!({
            "capture_id": capture_id,
            "source_sandbox_id": sb.id,
            "source_generation": sb.generation,
            "guest_os": sb.guest_os,
            "preserves_memory": preserves_memory,
            "fork_mode": if preserves_memory { "memory" } else { "disk_only" },
            "memory": if preserves_memory { "full" } else { "none" },
            "durability": if durable { "local" } else { "volatile" },
            "resources": sb.resources,
            "engine": ENGINE_NAME,
            "engine_digest": self.compat.engine_digest,
            "cpu_baseline": self.compat.cpu_baseline,
            "created_at_unix_ms": now_ms(),
        });
        std::fs::write(cdir.join("meta.json"), meta.to_string()).ctx("write capture meta")?;
        detail.insert("publish_us".into(), us(t.elapsed()));
        detail.insert(
            "fork_mode".into(),
            meta["fork_mode"].as_str().unwrap().into(),
        );
        detail.insert("preserves_memory".into(), preserves_memory.to_string());
        sb.captures.push(capture_id.into());
        let rec = CaptureRec {
            id: capture_id.into(),
            dir: cdir,
            state,
            source_sandbox: sb.id.clone(),
            source_generation: sb.generation,
            resources: sb.resources.clone(),
            durability: if durable { "local" } else { "volatile" }.into(),
            preserves_memory,
            spec,
            guest_os: sb.guest_os.clone(),
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

    fn run_quiet(&self, sb: &Sandbox, argv: &[&str]) -> Result<()> {
        let w = Self::worker(sb)?;
        let op = format!("op_sync_{}", lamp_init::hex_encode(&random_bytes::<6>()));
        let mut conn = guest::send(
            &w.vsock,
            &GuestRequest::Exec(Exec {
                sandbox_id: sb.id.clone(),
                generation: sb.generation,
                exec_id: op.clone(),
                argv: argv.iter().map(|s| s.to_string()).collect(),
                env: BTreeMap::new(),
                cwd: String::new(),
                timeout_ms: 60_000,
                output_limit: 4096,
                audit: None,
            }),
        )?;
        let _ = conn.next(Some(Duration::from_secs(10)))?;
        let alive = w.liveness();
        guest::wait_exec(
            &w.vsock,
            &sb.id,
            sb.generation,
            &op,
            Some(Instant::now() + Duration::from_secs(70)),
            alive,
        )
        .map(|_| ())
    }

    /// Durable suspend: tasks frozen (Linux), machine paused, full state
    /// saved to the store with F_FULLFSYNC barriers, the record committed,
    /// then the worker ends and its identity and RAM are released.
    fn suspend(&self, sb: &mut Sandbox, t0: Instant) -> Result<Outcome> {
        let mut detail = BTreeMap::new();
        let w = Self::worker(sb)?;
        let vsock = w.vsock.clone();
        if sb.guest_os == "linux" {
            let t = Instant::now();
            if let Err((e, _)) = freeze_guest(&vsock) {
                return Err(e.context("suspend"));
            }
            detail.insert("freeze_us".into(), us(t.elapsed()));
        }
        let state = sb.dir.join(SUSPEND_FILE);
        let _ = std::fs::remove_file(&state);
        let t = Instant::now();
        let saved = w
            .call(&Api::Pause {}, Duration::from_secs(60))
            .and_then(|_| {
                crash_point("suspend.paused");
                w.call(
                    &Api::Save {
                        path: state.clone(),
                    },
                    Duration::from_secs(900),
                )
            });
        let reply = match saved {
            Ok(r) => r,
            Err(e) => {
                // Not suspended: keep running.
                let _ = w.call(&Api::Resume {}, Duration::from_secs(60));
                if sb.guest_os == "linux" {
                    let _ = thaw_guest(&vsock);
                }
                let _ = std::fs::remove_file(&state);
                return Err(e.context("suspend"));
            }
        };
        detail.insert("save_us".into(), us(t.elapsed()));
        detail.insert("memory_bytes".into(), reply.detail["bytes"].to_string());
        crash_point("suspend.saved");
        let t = Instant::now();
        fsutil::full_fsync(&state)?;
        for f in [DISK, AUX] {
            let p = sb.dir.join(f);
            if p.exists() {
                fsutil::full_fsync(&p)?;
            }
        }
        crash_point("suspend.synced");
        let meta = serde_json::json!({
            "sandbox_id": sb.id,
            "generation": sb.generation,
            "policy_revision": sb.policy_revision,
            "guest_os": sb.guest_os,
            "resources": sb.resources,
            "source": sb.source,
            "spec": sb.machine,
            "machine_id": sb.barrier.as_ref().map(|b| b.machine_id.clone()),
            "engine_digest": self.compat.engine_digest,
            "cpu_baseline": self.compat.cpu_baseline,
            "os_build": self.compat.os_build,
            "suspended_at_unix_ms": now_ms(),
        });
        durable_write(&sb.dir.join(SUSPEND_META), meta.to_string().as_bytes())?;
        crash_point("suspend.committed");
        detail.insert("commit_us".into(), us(t.elapsed()));
        let t = Instant::now();
        if let Some(mut w) = sb.worker.take() {
            w.kill();
        }
        if let Some(id) = sb.identity.take() {
            self.free_identity(&id);
        }
        detail.insert("teardown_us".into(), us(t.elapsed()));
        crash_point("suspend.torn_down");
        sb.state = State::Suspended;
        sb.barrier = None;
        detail.insert("total_us".into(), us(t0.elapsed()));
        Ok(Outcome {
            state: "suspended".into(),
            handles: vec![format!("suspend_{}", sb.id)],
            detail,
            ..Default::default()
        })
    }

    fn gc_captures(&self) {
        let live: BTreeSet<String> = self.sandboxes.lock().unwrap().keys().cloned().collect();
        self.captures.lock().unwrap().retain(|_, c| {
            let state = *c.restores.lock().unwrap();
            let consumed = state == CAPTURE_CONSUMED
                || (state == CAPTURE_AVAILABLE && !live.contains(&c.source_sandbox));
            if consumed {
                let _ = std::fs::remove_dir_all(&c.dir);
                if let Some(s) = &c.state {
                    let _ = std::fs::remove_file(s);
                }
            }
            !consumed
        });
    }

    pub fn stop(&self, auth: Authority<'_>) -> Result<Outcome> {
        let sb = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb.lock().unwrap();
        Self::check(&sb, auth)?;
        if let Some(mut w) = sb.worker.take() {
            w.kill();
        }
        if let Some(id) = sb.identity.take() {
            self.free_identity(&id);
        }
        if matches!(sb.state, State::Running | State::Allocated) {
            sb.state = State::Stopped;
            self.free_macos_slot(&sb.id);
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
            if let Some(mut w) = sb.worker.take() {
                w.kill();
            }
            if let Some(id) = sb.identity.take() {
                self.free_identity(&id);
            }
            let _ = std::fs::remove_dir_all(&sb.dir);
            if let Some(ram) = self.ram_dir(&sb.id) {
                let _ = std::fs::remove_dir_all(ram);
            }
            sb.state = State::Stopped;
        }
        self.free_macos_slot(auth.sandbox_id);
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
        if sb.state == State::Running && !sb.worker.as_mut().is_some_and(Worker::alive) {
            sb.state = State::Failed("VZ worker exited".into());
        }
        let mut detail = BTreeMap::new();
        detail.insert("generation".into(), sb.generation.to_string());
        detail.insert("source".into(), sb.source.clone());
        detail.insert("boundary".into(), "vm".into());
        detail.insert("engine".into(), ENGINE_NAME.into());
        detail.insert("guest_os".into(), sb.guest_os.clone());
        detail.insert("arch".into(), "arm64".into());
        detail.insert("host_id".into(), self.cfg.host_id.clone());
        detail.insert("vcpus".into(), sb.resources.vcpus.to_string());
        detail.insert("memory_mib".into(), sb.resources.memory_mib.to_string());
        detail.insert("disk_gib".into(), sb.resources.disk_gib.to_string());
        if sb.guest_os == "macos" {
            detail.insert("class_label".into(), "macOS full VM — slower".into());
        }
        if let State::Failed(why) = &sb.state {
            detail.insert("failure".into(), why.clone());
        }
        if let Some(w) = &sb.worker {
            detail.insert("vmm_pid".into(), w.child.id().to_string());
        }
        if let Some(id) = &sb.identity {
            detail.insert("vmm_uid".into(), id.uid.to_string());
            detail.insert("vmm_identity".into(), id.name.clone());
        }
        let (used, cap) = self.macos_slots();
        detail.insert("macos_slots".into(), format!("{used}/{cap}"));
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

    /// Stops every running worker (supervisor shutdown). Suspended
    /// sandboxes keep their durable state.
    pub fn shutdown(&self) -> usize {
        let all: Vec<_> = self.sandboxes.lock().unwrap().drain().collect();
        let mut n = 0;
        for (_, sb) in all {
            let mut sb = sb.lock().unwrap();
            if let Some(mut w) = sb.worker.take() {
                w.kill();
                n += 1;
            }
        }
        n
    }

    pub fn compatibility(&self, kernel_digest: &str, graph: &str) -> Compatibility {
        Compatibility {
            engine: ENGINE_NAME.into(),
            engine_digest: self.compat.engine_digest.clone(),
            state_abi: STATE_ABI.into(),
            guest_kernel_digest: kernel_digest.into(),
            cpu_baseline: self.compat.cpu_baseline.clone(),
            device_graph_digest: manifest::sha256_hex(graph.as_bytes()),
        }
    }

    /// The worker binary that runs the machines (for template builds).
    pub fn worker_bin(&self) -> &Path {
        &self.cfg.worker
    }
}

fn mismatch(have: &Resources, asked: &Resources) -> EngineError {
    EngineError::new(
        "ENGINE_UNQUALIFIED",
        format!(
            "saved state has {}/{} MiB/{} GiB; request asked {}/{} MiB/{} GiB",
            have.vcpus,
            have.memory_mib,
            have.disk_gib,
            asked.vcpus,
            asked.memory_mib,
            asked.disk_gib
        ),
    )
}

/// A fresh `VZMacMachineIdentifier`, from the supported constructor (run in
/// the signed helper, which links the framework).
pub fn new_mac_machine_identifier(worker: &Path) -> Result<String> {
    let out = Command::new(worker)
        .arg("new-ids")
        .output()
        .ctx("run lamp-vz new-ids")?;
    let v: serde_json::Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| EngineError::host(format!("new-ids: {e}")))?;
    v["mac_machine_identifier_b64"]
        .as_str()
        .map(str::to_string)
        .ok_or_else(|| EngineError::host("new-ids returned no identifier"))
}

pub fn new_generic_machine_identifier(worker: &Path) -> Result<String> {
    let out = Command::new(worker)
        .arg("new-ids")
        .output()
        .ctx("run lamp-vz new-ids")?;
    let v: serde_json::Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| EngineError::host(format!("new-ids: {e}")))?;
    v["generic_machine_identifier_b64"]
        .as_str()
        .map(str::to_string)
        .ok_or_else(|| EngineError::host("new-ids returned no identifier"))
}

pub fn object(path: &Path) -> Result<Object> {
    fsutil::digest_file(path)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ids_are_validated() {
        assert!(valid_id("sb_abc_1", "sb_"));
        assert!(!valid_id("sb_ABC", "sb_"));
        assert!(!valid_id("sb_", "sb_"));
        assert!(!valid_id("sb_a/b", "sb_"));
    }

    #[test]
    fn machine_config_rejects_unknown_fields() {
        let ok = r#"{"guest":"linux","cmdline":"x","machine_identifier_b64":"AA==","resources":{"vcpus":2,"memory_mib":1024,"disk_gib":20}}"#;
        assert!(serde_json::from_str::<MachineCfg>(ok).is_ok());
        let bad = r#"{"guest":"linux","cmdline":"x","machine_identifier_b64":"AA==","resources":{"vcpus":2,"memory_mib":1024,"disk_gib":20},"extra":1}"#;
        assert!(serde_json::from_str::<MachineCfg>(bad).is_err());
    }

    fn temp_engine(tag: &str) -> (Arc<Engine>, PathBuf) {
        let store = std::env::temp_dir().join(format!("lamp-mac-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&store);
        let mut cfg = Config::defaults(store.clone());
        // Any file stands in for the worker binary: only its digest is used.
        cfg.worker = PathBuf::from("/bin/ls");
        cfg.ram_dir = None;
        (Arc::new(Engine::open(cfg, Mode::Offline).unwrap()), store)
    }

    /// The two-per-Mac cap is one atomic check: of many concurrent
    /// allocations exactly two win; a holder re-asking keeps its slot; a
    /// freed slot is available again.
    #[test]
    fn macos_slot_cap_is_atomic() {
        let (engine, store) = temp_engine("slots");
        let winners: Vec<bool> = (0..16)
            .map(|i| {
                let e = engine.clone();
                std::thread::spawn(move || e.take_macos_slot(&format!("sb_race_{i}")).is_ok())
            })
            .collect::<Vec<_>>()
            .into_iter()
            .map(|h| h.join().unwrap())
            .collect();
        assert_eq!(winners.iter().filter(|w| **w).count(), MACOS_SLOTS);
        assert_eq!(engine.macos_slots(), (2, 2));
        let held: Vec<String> = engine.macos_slots.lock().unwrap().iter().cloned().collect();
        assert!(engine.take_macos_slot(&held[0]).is_ok());
        let refused = engine.take_macos_slot("sb_third").unwrap_err();
        assert_eq!(refused.code, "NO_CAPACITY");
        engine.free_macos_slot(&held[0]);
        assert!(engine.take_macos_slot("sb_third").is_ok());
        std::fs::remove_dir_all(store).unwrap();
    }

    #[test]
    fn durable_write_replaces_atomically() {
        let dir = std::env::temp_dir().join(format!("lamp-dw-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let p = dir.join("suspended.json");
        durable_write(&p, b"one").unwrap();
        durable_write(&p, b"two").unwrap();
        assert_eq!(std::fs::read(&p).unwrap(), b"two");
        assert!(!p.with_extension("tmp").exists());
        std::fs::remove_dir_all(dir).unwrap();
    }

    #[test]
    fn console_lock_is_read_from_the_console_session() {
        let user = |locked: &str| {
            format!(
                "<dict><key>kCGSSessionOnConsoleKey</key>\n\t<true/><key>CGSSessionScreenIsLocked</key>\n\t<{locked}/></dict>"
            )
        };
        assert!(console_locked_in(&user("true")));
        assert!(!console_locked_in(&user("false")));
        // Unlocked sessions often omit the key; no console user counts as locked.
        assert!(!console_locked_in(
            "<key>kCGSSessionOnConsoleKey</key><true/>"
        ));
        assert!(console_locked_in("<dict></dict>"));
    }

    #[test]
    fn crash_point_is_inert_unless_named() {
        // The variable is unset in tests; reaching a point must do nothing.
        crash_point("suspend.saved");
    }
}
