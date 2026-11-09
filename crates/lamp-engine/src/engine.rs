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
//! captures/<capture_id>/      rootfs.ext4 meta.json [mem.layer vmstate when durable]
//! jail/firecracker/<jail>/    per-sandbox jailer chroot
//! ```
//!
//! and RAM-backed memory state under `mem_dir` (tmpfs):
//!
//! ```text
//! layers/<capture_id>.mem     sealed dirty-page layers (sparse)
//! captures/<capture_id>.vmstate
//! jails/<jail>/               bound into each jail as /mem.d
//! ```
//!
//! A sandbox's memory is a stack of immutable layers (see `layer`). A memory
//! capture seals one more layer holding only pages dirtied since the last
//! one; the stack is the complete RAM image. A fork restores through
//! Firecracker's UFFD backend, served by a per-sandbox `lamp-pager` that
//! reads the stack. No live KVM handle, process fork or mutable shared page
//! is ever inherited.
use crate::error::{EngineError, IoContext, Result};
use crate::fc::{self, Binaries, Jail, Limits};
use crate::fsutil::{self, now_ms, now_ns, random_bytes};
use crate::guest;
use crate::layer::{self, Layer, Stack};
use crate::manifest::{self, AccelerationVariant, Compatibility, Manifest, Provenance, Resources};
use ed25519_dalek::{SigningKey, VerifyingKey};
use lamp_init::{Barrier, Exec, ExecDone, GuestRequest, GuestResponse};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex, RwLock};
use std::time::{Duration, Instant};

pub const ENGINE_NAME: &str = "firecracker-1.17.0";
pub const STATE_ABI: &str = "firecracker-1.17.0/snapshot";
pub const DEVICE_GRAPH: &str = "virtio-blk:rootfs,vsock:cid3,serial;no-net";
pub const STANDARD: Resources = Resources {
    vcpus: 2,
    memory_mib: 4096,
    disk_gib: 20,
};
pub const DEFAULT_PIDS_MAX: u64 = 4096;

#[derive(Debug, Clone)]
pub struct Config {
    pub store: PathBuf,
    pub netns_dir: PathBuf,
    pub bins: Binaries,
    pub uid_base: u32,
    pub uid_count: u32,
    pub host_id: String,
    /// RAM-backed (tmpfs) directory for volatile memory layers.
    pub mem_dir: PathBuf,
    pub mem_dir_size: String,
    /// Extra trusted producer keys beyond this host's own.
    pub trusted_producers: Vec<VerifyingKey>,
}

impl Config {
    pub fn defaults(store: PathBuf) -> Config {
        Config {
            store,
            netns_dir: PathBuf::from("/run/lamp/netns"),
            bins: Binaries {
                firecracker: PathBuf::from("/usr/local/lib/lamp/firecracker"),
                jailer: PathBuf::from("/usr/local/lib/lamp/jailer"),
                pager: PathBuf::from("/usr/local/lib/lamp/lamp-pager"),
            },
            mem_dir: PathBuf::from("/run/lamp/mem"),
            mem_dir_size: "75%".into(),
            uid_base: 900_000,
            uid_count: 65_536,
            host_id: hostname(),
            trusted_producers: Vec::new(),
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
    layer: std::sync::OnceLock<Arc<Layer>>,
}

impl Template {
    /// The variant's memory image as a complete bottom layer.
    pub fn memory_layer(&self) -> Result<Arc<Layer>> {
        if let Some(l) = self.layer.get() {
            return Ok(l.clone());
        }
        let l = Layer::open(&self.dir.join(fc::MEM), true, false)?;
        Ok(self.layer.get_or_init(|| l).clone())
    }
}

enum Source {
    Template {
        layer: Arc<Layer>,
        mem: PathBuf,
        vmstate: PathBuf,
        disk: PathBuf,
    },
    Capture {
        stack: Stack,
        vmstate: PathBuf,
        disk: PathBuf,
    },
}

/// Starts the page server for a UFFD-backed restore and waits until its
/// socket is bound.
fn spawn_pager(bin: &Path, socket: &Path, uid: u32, stack: &Stack) -> Result<std::process::Child> {
    use std::io::BufRead;
    use std::os::unix::process::CommandExt;
    let fds: Vec<i32> = stack
        .iter()
        .map(|l| std::os::fd::AsRawFd::as_raw_fd(&l.file))
        .collect();
    let mut cmd = std::process::Command::new(bin);
    cmd.arg(socket)
        .arg(uid.to_string())
        .arg(fds.len().to_string())
        .env_clear()
        .stdin(std::process::Stdio::null())
        .stdout(std::process::Stdio::piped());
    // SAFETY: only async-signal-safe dup2 calls between fork and exec.
    unsafe {
        cmd.pre_exec(move || {
            // Move every layer above the target range first, then into place.
            let base = 1000;
            for (i, fd) in fds.iter().enumerate() {
                if libc::dup2(*fd, base + i as i32) < 0 {
                    return Err(std::io::Error::last_os_error());
                }
            }
            for i in 0..fds.len() as i32 {
                if libc::dup2(base + i, 3 + i) < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                libc::close(base + i);
            }
            Ok(())
        });
    }
    let mut child = cmd.spawn().ctx("start lamp-pager")?;
    let mut line = String::new();
    std::io::BufReader::new(child.stdout.take().unwrap())
        .read_line(&mut line)
        .ctx("wait for lamp-pager")?;
    if line.trim() != "ready" {
        let _ = child.kill();
        let _ = child.wait();
        return Err(EngineError::host("lamp-pager failed to start"));
    }
    Ok(child)
}

/// Copies a sparse layer's data pages to a store file (durable captures).
fn copy_sparse(l: &Layer, dst: &Path) -> Result<()> {
    use std::os::unix::fs::FileExt;
    let out = std::fs::File::create(dst).ctx("create durable layer")?;
    out.set_len(l.len)?;
    let mut buf = vec![0_u8; 1 << 20];
    let bits = l.bitmap();
    let mut p = 0_u64;
    let pages = l.len / layer::PAGE;
    while p < pages {
        if bits[(p / 64) as usize] >> (p % 64) & 1 == 0 {
            p += 1;
            continue;
        }
        let start = p;
        while p < pages && p - start < 256 && bits[(p / 64) as usize] >> (p % 64) & 1 == 1 {
            p += 1;
        }
        let n = ((p - start) * layer::PAGE) as usize;
        l.file.read_exact_at(&mut buf[..n], start * layer::PAGE)?;
        out.write_all_at(&buf[..n], start * layer::PAGE)?;
    }
    Ok(())
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
    /// Memory layers this VM was restored from plus its sealed captures.
    pub stack: Stack,
    pub pager: Option<std::process::Child>,
    pub source: String,
    pub captures: Vec<String>,
    pub barrier: Option<lamp_init::BarrierAck>,
}

impl Sandbox {
    fn stop_pager(&mut self) {
        if let Some(mut p) = self.pager.take() {
            let _ = p.kill();
            let _ = p.wait();
        }
    }

    /// Kills the VMM and page server and drops this sandbox's layer refs.
    fn teardown(&mut self) {
        if let Some(j) = self.jail.take() {
            j.destroy();
        }
        self.stop_pager();
        self.stack.clear();
    }
}

#[derive(Debug)]
pub struct CaptureRec {
    pub id: String,
    pub dir: PathBuf,
    pub vmstate: PathBuf,
    pub stack: Stack,
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

pub fn load_or_create_key(path: &Path) -> Result<SigningKey> {
    if let Ok(text) = std::fs::read_to_string(path) {
        let bytes = lamp_init::hex_decode(text.trim())
            .filter(|b| b.len() == 32)
            .ok_or_else(|| EngineError::host("malformed producer key"))?;
        return Ok(SigningKey::from_bytes(&bytes.try_into().unwrap()));
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
    /// Opens the store, discards state from a previous supervisor run
    /// (volatile in this release) and verifies every template.
    pub fn open(cfg: Config) -> Result<Engine> {
        for sub in [
            "seeds",
            "templates/names",
            "captures",
            "jail/firecracker",
            "staging",
        ] {
            std::fs::create_dir_all(cfg.store.join(sub)).ctx(format!("create store/{sub}"))?;
        }
        fc::init_cgroups()?;
        fc::ensure_tmpfs(&cfg.mem_dir, &cfg.mem_dir_size)?;
        let producer = load_or_create_key(&cfg.store.join("keys/producer.key"))?;
        let mut trusted = HashMap::new();
        trusted.insert(
            manifest::key_id(&producer.verifying_key()),
            producer.verifying_key(),
        );
        for k in &cfg.trusted_producers {
            trusted.insert(manifest::key_id(k), *k);
        }
        let compat = HostCompat::detect(&cfg.bins)?;
        let engine = Engine {
            cfg,
            compat,
            producer,
            trusted,
            templates: RwLock::new(HashMap::new()),
            sandboxes: Mutex::new(HashMap::new()),
            captures: Mutex::new(HashMap::new()),
            uids: Mutex::new(BTreeSet::new()),
        };
        engine.clean_stale();
        engine.load_templates();
        Ok(engine)
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
        for dir in ["layers", "captures", "jails"] {
            let path = self.cfg.mem_dir.join(dir);
            let _ = std::fs::remove_dir_all(&path);
            let _ = std::fs::create_dir_all(&path);
        }
    }

    fn load_templates(&self) {
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
            layer: Default::default(),
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
                stack: Vec::new(),
                pager: None,
                source: String::new(),
                captures: Vec::new(),
                barrier: None,
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
            Some(c) => (
                Source::Capture {
                    stack: c.stack.clone(),
                    vmstate: c.vmstate.clone(),
                    disk: c.dir.join(fc::ROOTFS),
                },
                c.resources.clone(),
            ),
            None => {
                let t = self.template(capture_id)?;
                let layer = t.memory_layer()?;
                (
                    Source::Template {
                        layer,
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
        match self.restore_into(&mut sb, source, t0) {
            Ok(mut out) => {
                sb.source = capture_id.to_string();
                if let Some(c) = capture {
                    *c.restores.lock().unwrap() += 1;
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
                if let Some(mut p) = sb.pager.take() {
                    let _ = p.kill();
                    let _ = p.wait();
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
        let (disk, stack) = match &source {
            Source::Template { disk, layer, .. } => (disk, vec![layer.clone()]),
            Source::Capture { disk, stack, .. } => (disk, stack.clone()),
        };
        fsutil::reflink(disk, &jail.path(fc::ROOTFS), 0o600)?;
        fsutil::chown(&jail.path(fc::ROOTFS), uid, uid)?;
        let backend = match &source {
            Source::Template { mem, vmstate, .. } => {
                // Prepared-template create: the kernel maps the immutable
                // image privately; no page server is needed.
                std::fs::hard_link(mem, jail.path(fc::MEM)).ctx("link memory image")?;
                std::fs::hard_link(vmstate, jail.memdir.join(fc::VMSTATE))
                    .or_else(|_| std::fs::copy(vmstate, jail.memdir.join(fc::VMSTATE)).map(|_| ()))
                    .ctx("place vm state")?;
                format!(r#"{{"backend_type":"File","backend_path":"/{}"}}"#, fc::MEM)
            }
            Source::Capture { vmstate, .. } => {
                std::fs::hard_link(vmstate, jail.memdir.join(fc::VMSTATE)).ctx("link vm state")?;
                detail.insert("layers".into(), stack.len().to_string());
                format!(
                    r#"{{"backend_type":"Uffd","backend_path":"/{}"}}"#,
                    fc::UFFD
                )
            }
        };
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        if matches!(source, Source::Capture { .. }) {
            let t = Instant::now();
            sb.pager = Some(spawn_pager(
                &self.cfg.bins.pager,
                &jail.path(fc::UFFD),
                uid,
                &stack,
            )?);
            detail.insert("pager_us".into(), us(t.elapsed()));
        }
        let t = Instant::now();
        jail.spawn(&self.cfg.bins, limits)?;
        detail.insert("spawn_us".into(), us(t.elapsed()));
        let t = Instant::now();
        let body = format!(
            r#"{{"snapshot_path":"/{}/{}","mem_backend":{backend},"track_dirty_pages":true,"resume_vm":true,"clock_realtime":true}}"#,
            fc::MEMDIR,
            fc::VMSTATE
        );
        jail.api("PUT", "/snapshot/load", &body)?;
        detail.insert("load_us".into(), us(t.elapsed()));
        let t = Instant::now();
        let vsock = jail.path(fc::VSOCK);
        let ack = self.barrier(&vsock, &sb.id, sb.generation)?;
        detail.insert("barrier_us".into(), us(t.elapsed()));
        detail.insert("rng_sample".into(), ack.rng_sample_hex.clone());
        detail.insert("machine_id".into(), ack.machine_id.clone());
        sb.barrier = Some(ack);
        sb.stack = stack;
        sb.state = State::Running;
        Ok(Outcome {
            state: "running".into(),
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

    /// Runs one command to completion. The sandbox lock is not held while it
    /// runs, so a fork can capture a sandbox in the middle of a command.
    pub fn exec(
        &self,
        auth: Authority<'_>,
        argv: Vec<String>,
        cwd: String,
        env: BTreeMap<String, String>,
        timeout_ms: u64,
    ) -> Result<Outcome> {
        if argv.is_empty() {
            return Err(EngineError::invalid("argv is empty"));
        }
        let vsock = {
            let sb = self.sandbox(auth.sandbox_id)?;
            let sb = sb.lock().unwrap();
            Self::check(&sb, auth)?;
            if sb.state != State::Running {
                return Err(EngineError::in_use(format!(
                    "sandbox is {}",
                    sb.state.name()
                )));
            }
            sb.jail.as_ref().unwrap().path(fc::VSOCK)
        };
        let request = GuestRequest::Exec(Exec {
            sandbox_id: auth.sandbox_id.into(),
            generation: auth.generation,
            argv,
            env,
            cwd,
            timeout_ms,
            output_limit: lamp_init::DEFAULT_OUTPUT_LIMIT,
        });
        let reply = guest::call(&vsock, &request, Duration::from_millis(timeout_ms + 5_000))?;
        match reply {
            GuestResponse::ExecDone(done) => Ok(Outcome {
                state: "exited".into(),
                command: Some(done),
                ..Default::default()
            }),
            GuestResponse::Error { code, message } if code == "GENERATION_MISMATCH" => {
                Err(EngineError::new("REVISION_CONFLICT", message))
            }
            GuestResponse::Error { code, message } => {
                Err(EngineError::host(format!("{code}: {message}")))
            }
            other => Err(EngineError::host(format!(
                "exec: unexpected reply {other:?}"
            ))),
        }
    }

    /// Memory capture of a running sandbox at one RAM/CPU/device/disk
    /// generation. The source is paused only while Firecracker writes the
    /// pages dirtied since its previous layer (to RAM-backed tmpfs) and the
    /// disk head is sealed by reflink; then it continues.
    pub fn capture(
        self: &Arc<Self>,
        auth: Authority<'_>,
        capture_id: &str,
        consistency: &str,
    ) -> Result<Outcome> {
        let t0 = Instant::now();
        if !valid_id(capture_id, "cp_") {
            return Err(EngineError::invalid(
                "capture_id must match cp_[a-z0-9_]+ (≤60)",
            ));
        }
        let durable = match consistency {
            "" | "volatile" => false,
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
        let jail = sb.jail.as_ref().unwrap();
        let mut detail = BTreeMap::new();
        let tmp_mem = jail.memdir.join("cap.mem");
        let tmp_state = jail.memdir.join("cap.vmstate");
        let _ = std::fs::remove_file(&tmp_mem);
        let _ = std::fs::remove_file(&tmp_state);

        // --- source paused from here ---
        let tp = Instant::now();
        jail.api("PATCH", "/vm", r#"{"state":"Paused"}"#)?;
        let sealed = (|| -> Result<()> {
            let t = Instant::now();
            let body = format!(
                r#"{{"snapshot_type":"Diff","mem_file_path":"/{m}/cap.mem","snapshot_path":"/{m}/cap.vmstate","sync_snapshot_files":false}}"#,
                m = fc::MEMDIR
            );
            jail.api("PUT", "/snapshot/create", &body)?;
            detail.insert("dirty_write_us".into(), us(t.elapsed()));
            let t = Instant::now();
            fsutil::reflink(&jail.path(fc::ROOTFS), &cdir.join(fc::ROOTFS), 0o400)?;
            detail.insert("disk_seal_us".into(), us(t.elapsed()));
            Ok(())
        })();
        let resumed = jail.api("PATCH", "/vm", r#"{"state":"Resumed"}"#);
        let pause = tp.elapsed();
        // --- source running again ---
        if let Err(e) = sealed.and(resumed.map(|_| ())) {
            let _ = std::fs::remove_dir_all(&cdir);
            let _ = std::fs::remove_file(&tmp_mem);
            let _ = std::fs::remove_file(&tmp_state);
            return Err(e.context("capture"));
        }
        detail.insert("source_pause_us".into(), us(pause));
        let t = Instant::now();
        let layer_path = self
            .cfg
            .mem_dir
            .join("layers")
            .join(format!("{capture_id}.mem"));
        let vmstate = self
            .cfg
            .mem_dir
            .join("captures")
            .join(format!("{capture_id}.vmstate"));
        std::fs::rename(&tmp_mem, &layer_path).ctx("seal memory layer")?;
        std::fs::rename(&tmp_state, &vmstate).ctx("seal state")?;
        for p in [&layer_path, &vmstate] {
            fsutil::chown(p, 0, 0)?;
            fsutil::chmod(p, 0o444)?;
        }
        let layer = Layer::open(&layer_path, false, true)?;
        let mut stack = sb.stack.clone();
        stack.push(layer.clone());
        if durable {
            // Local durability: the dirty layer, state and disk reach stable
            // storage before the capture is reported.
            copy_sparse(&layer, &cdir.join("mem.layer"))?;
            std::fs::copy(&vmstate, cdir.join(fc::VMSTATE)).ctx("persist state")?;
            for p in [
                &cdir.join("mem.layer"),
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
            "durability": if durable { "local" } else { "volatile" },
            "resources": sb.resources,
            "layers": stack.iter().map(|l| l.name.clone()).collect::<Vec<_>>(),
            "engine": ENGINE_NAME,
            "engine_digest": self.compat.engine_digest,
            "cpu_baseline": self.compat.cpu_baseline,
            "created_at_unix_ms": now_ms(),
        });
        std::fs::write(cdir.join("meta.json"), meta.to_string()).ctx("write capture meta")?;
        detail.insert("publish_us".into(), us(t.elapsed()));
        detail.insert("layers".into(), stack.len().to_string());
        sb.stack = stack.clone();
        sb.captures.push(capture_id.into());
        let rec = CaptureRec {
            id: capture_id.into(),
            dir: cdir,
            vmstate,
            stack,
            source_sandbox: sb.id.clone(),
            source_generation: sb.generation,
            resources: sb.resources.clone(),
            durability: if durable { "local" } else { "volatile" }.into(),
            restores: Mutex::new(0),
        };
        self.captures
            .lock()
            .unwrap()
            .insert(capture_id.into(), Arc::new(rec));
        drop(sb);
        self.compact_later(sb_arc, capture_id);
        detail.insert("total_us".into(), us(t0.elapsed()));
        Ok(Outcome {
            state: "captured".into(),
            handles: vec![capture_id.into()],
            detail,
            ..Default::default()
        })
    }

    /// Bounds the source's layer depth off the fork path: drops hidden diff
    /// layers and merges older ones. Captures and forks keep their own
    /// references, so nothing they use changes.
    fn compact_later(self: &Arc<Self>, sb_arc: Arc<Mutex<Sandbox>>, tag: &str) {
        let dir = self.cfg.mem_dir.join("layers");
        let name = format!("{tag}.merged.mem");
        std::thread::spawn(move || {
            let before = sb_arc.lock().unwrap().stack.clone();
            match layer::compact(&before, &dir, &name, 2) {
                Ok(Some(new)) => {
                    let mut sb = sb_arc.lock().unwrap();
                    if sb.stack.len() >= before.len()
                        && sb.stack.iter().zip(&before).all(|(a, b)| Arc::ptr_eq(a, b))
                    {
                        let mut stack = new;
                        stack.extend(sb.stack[before.len()..].iter().cloned());
                        sb.stack = stack;
                    }
                }
                Ok(None) => {}
                Err(e) => eprintln!("lamp-engine: layer compaction: {e}"),
            }
        });
    }

    /// Removes captures that were restored (or whose source is gone).
    /// Memory layers stay alive while any sandbox stack references them.
    fn gc_captures(&self) {
        let live: BTreeSet<String> = self.sandboxes.lock().unwrap().keys().cloned().collect();
        self.captures.lock().unwrap().retain(|_, c| {
            let consumed = *c.restores.lock().unwrap() > 0 || !live.contains(&c.source_sandbox);
            if consumed {
                let _ = std::fs::remove_dir_all(&c.dir);
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
        sb.stop_pager();
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
            sb.teardown();
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

    pub fn inspect(&self, sandbox_id: &str) -> Result<Outcome> {
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
        Ok(Outcome {
            state: sb.state.name().into(),
            handles: sb.captures.clone(),
            detail,
            ..Default::default()
        })
    }

    /// Kills every sandbox (supervisor shutdown).
    pub fn shutdown(&self) {
        let all: Vec<_> = self.sandboxes.lock().unwrap().drain().collect();
        for (_, sb) in all {
            sb.lock().unwrap().teardown();
        }
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
