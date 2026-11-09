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
//! captures/<capture_id>/      mem vmstate rootfs.ext4 meta.json (memory captures)
//! jail/firecracker/<jail>/    per-sandbox jailer chroot
//! ```
//!
//! A memory capture is a complete standalone image: the previous image is
//! reflinked (constant time) and Firecracker writes only pages dirtied since
//! then into the clone. No live KVM handle, process fork or mutable shared
//! file is ever inherited: every mapped memory file is immutable.
use crate::error::{EngineError, IoContext, Result};
use crate::fc::{self, Binaries, Jail, Limits};
use crate::fsutil::{self, now_ms, now_ns, random_bytes};
use crate::guest;
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
            },
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
                let mut f: Vec<&str> = line.split(':').nth(1).unwrap_or("").split_whitespace().collect();
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
    /// Immutable complete memory image this VM's next capture builds on.
    pub base_mem: Option<PathBuf>,
    pub source: String,
    pub captures: Vec<String>,
    pub barrier: Option<lamp_init::BarrierAck>,
}

#[derive(Debug)]
pub struct CaptureRec {
    pub id: String,
    pub dir: PathBuf,
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
        && id.bytes().all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'_')
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
        for sub in ["seeds", "templates/names", "captures", "jail/firecracker", "staging"] {
            std::fs::create_dir_all(cfg.store.join(sub)).ctx(format!("create store/{sub}"))?;
        }
        fc::init_cgroups()?;
        let producer = load_or_create_key(&cfg.store.join("keys/producer.key"))?;
        let mut trusted = HashMap::new();
        trusted.insert(manifest::key_id(&producer.verifying_key()), producer.verifying_key());
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
    }

    fn load_templates(&self) {
        let dir = self.cfg.store.join("templates");
        let Ok(entries) = std::fs::read_dir(&dir) else { return };
        for e in entries.flatten() {
            let name = e.file_name().to_string_lossy().into_owned();
            if !name.starts_with("tp_") {
                continue;
            }
            match self.verify_template_dir(&e.path()) {
                Ok(t) => {
                    self.templates.write().unwrap().insert(t.id.clone(), Arc::new(t));
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
            .ok_or_else(|| EngineError::new("HOST_NOT_TRUSTED", "capture producer is not an enrolled key"))?;
        provenance.verify(key)?;
        if provenance.manifest_digest != digest {
            return Err(EngineError::new("DATA_UNAVAILABLE", "provenance names another manifest"));
        }
        let id = manifest::template_id(&digest);
        if dir.file_name().and_then(|n| n.to_str()) != Some(id.as_str()) {
            return Err(EngineError::new("DATA_UNAVAILABLE", "template directory does not match digest"));
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
            .ok_or_else(|| EngineError::new("ENGINE_UNQUALIFIED", "no acceleration variant for this host"))?;
        let c = &variant.compatibility;
        if provenance.engine_digest != c.engine_digest
            || provenance.cpu_baseline != c.cpu_baseline
            || provenance.guest_kernel_digest != c.guest_kernel_digest
            || provenance.device_graph_digest != c.device_graph_digest
        {
            return Err(EngineError::new("DATA_UNAVAILABLE", "provenance does not bind the variant"));
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
        self.templates.write().unwrap().insert(t.id.clone(), t.clone());
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
        Err(EngineError::new("NO_CAPACITY", "no free sandbox identities"))
    }

    pub fn free_uid(&self, uid: u32) {
        self.uids.lock().unwrap().remove(&uid);
    }

    pub fn new_jail(&self, id: &str) -> Result<Jail> {
        let uid = self.alloc_uid()?;
        Jail::create(&self.cfg.store.join("jail"), &self.cfg.netns_dir, id, uid).inspect_err(|_| self.free_uid(uid))
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
                format!("sandbox {} is at generation {}, request names {}", sb.id, sb.generation, auth.generation),
            ));
        }
        if sb.policy_revision != auth.policy_revision {
            return Err(EngineError::new("REVISION_CONFLICT", "policy revision does not match"));
        }
        Ok(())
    }

    /// Reserves identity and a jail. No guest state is touched yet.
    pub fn allocate(&self, auth: Authority<'_>, requested: Option<Resources>) -> Result<Outcome> {
        if !valid_id(auth.sandbox_id, "sb_") {
            return Err(EngineError::invalid("sandbox_id must match sb_[a-z0-9_]+ (≤60)"));
        }
        if auth.generation == 0 {
            return Err(EngineError::invalid("generation must be nonzero"));
        }
        let resources = requested.unwrap_or(STANDARD);
        if resources.vcpus == 0 || resources.vcpus > 32 || (resources.vcpus > 1 && resources.vcpus % 2 == 1) {
            return Err(EngineError::invalid("vcpus must be 1 or an even number ≤ 32"));
        }
        if resources.memory_mib < 128 || resources.disk_gib == 0 {
            return Err(EngineError::invalid("memory_mib must be ≥128 and disk_gib ≥1"));
        }
        let mut all = self.sandboxes.lock().unwrap();
        if all.contains_key(auth.sandbox_id) {
            return Err(EngineError::in_use(format!("sandbox {} exists", auth.sandbox_id)));
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
                base_mem: None,
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
            return Err(EngineError::in_use(format!("sandbox is {}, not allocated", sb.state.name())));
        }
        let (mem, vmstate, disk, resources, capture) = if let Some(c) = self.captures.lock().unwrap().get(capture_id).cloned() {
            (c.dir.join(fc::MEM), c.dir.join(fc::VMSTATE), c.dir.join(fc::ROOTFS), c.resources.clone(), Some(c))
        } else {
            let t = self.template(capture_id)?;
            (
                t.dir.join(fc::MEM),
                t.dir.join(fc::VMSTATE),
                t.dir.join(fc::ROOTFS),
                t.variant.resources.clone(),
                None,
            )
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
        let result = self.restore_into(&mut sb, &mem, &vmstate, &disk, t0);
        match result {
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
                sb.state = State::Failed(e.message.clone());
                Err(e)
            }
        }
    }

    fn restore_into(&self, sb: &mut Sandbox, mem: &Path, vmstate: &Path, disk: &Path, t0: Instant) -> Result<Outcome> {
        let limits = Limits {
            vcpus: sb.resources.vcpus,
            memory_mib: sb.resources.memory_mib,
            pids_max: DEFAULT_PIDS_MAX,
        };
        let jail = sb.jail.as_mut().ok_or_else(|| EngineError::in_use("sandbox has no jail"))?;
        let uid = jail.uid;
        let mut detail = BTreeMap::new();
        fsutil::reflink(disk, &jail.path(fc::ROOTFS), 0o600)?;
        fsutil::chown(&jail.path(fc::ROOTFS), uid, uid)?;
        std::fs::hard_link(mem, jail.path(fc::MEM)).ctx("link memory image")?;
        std::fs::hard_link(vmstate, jail.path(fc::VMSTATE)).ctx("link vm state")?;
        detail.insert("prepare_us".into(), us(t0.elapsed()));
        let t = Instant::now();
        jail.spawn(&self.cfg.bins, limits)?;
        detail.insert("spawn_us".into(), us(t.elapsed()));
        let t = Instant::now();
        let body = format!(
            r#"{{"snapshot_path":"/{}","mem_backend":{{"backend_type":"File","backend_path":"/{}"}},"track_dirty_pages":true,"resume_vm":true,"clock_realtime":true}}"#,
            fc::VMSTATE,
            fc::MEM
        );
        jail.api("PUT", "/snapshot/load", &body)?;
        detail.insert("load_us".into(), us(t.elapsed()));
        let t = Instant::now();
        let vsock = jail.path(fc::VSOCK);
        let ack = self.barrier(&vsock, &sb.id, sb.generation)?;
        detail.insert("barrier_us".into(), us(t.elapsed()));
        detail.insert("rng_sample".into(), ack.rng_sample_hex.clone());
        detail.insert("machine_id".into(), ack.machine_id.clone());
        detail.insert("kernel_boot_id".into(), ack.kernel_boot_id.clone());
        sb.barrier = Some(ack);
        sb.base_mem = Some(mem.to_path_buf());
        sb.state = State::Running;
        Ok(Outcome {
            state: "running".into(),
            detail,
            ..Default::default()
        })
    }

    /// New identity, entropy and clock; refuses old identities afterwards.
    pub fn barrier(&self, vsock: &Path, sandbox_id: &str, generation: u64) -> Result<lamp_init::BarrierAck> {
        let barrier = Barrier {
            sandbox_id: sandbox_id.into(),
            generation,
            hostname: sandbox_id.replace('_', "-"),
            machine_id: lamp_init::hex_encode(&random_bytes::<16>()),
            entropy_hex: lamp_init::hex_encode(&random_bytes::<64>()),
            unix_time_ns: now_ns(),
        };
        let reply = guest::call(vsock, &GuestRequest::Barrier(barrier.clone()), Duration::from_secs(10))
            .map_err(|e| e.context("identity barrier"))?;
        match reply {
            GuestResponse::BarrierAck(ack)
                if ack.sandbox_id == barrier.sandbox_id
                    && ack.generation == generation
                    && ack.machine_id == barrier.machine_id =>
            {
                Ok(ack)
            }
            GuestResponse::Error { code, message } => {
                Err(EngineError::host(format!("identity barrier refused: {code}: {message}")))
            }
            other => Err(EngineError::host(format!("identity barrier: unexpected reply {other:?}"))),
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
                return Err(EngineError::in_use(format!("sandbox is {}", sb.state.name())));
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
            GuestResponse::Error { code, message } => Err(EngineError::host(format!("{code}: {message}"))),
            other => Err(EngineError::host(format!("exec: unexpected reply {other:?}"))),
        }
    }

    /// Memory capture of a running sandbox at one RAM/CPU/device/disk
    /// generation. The source is paused only while its dirty pages and disk
    /// head are sealed, then continues.
    pub fn capture(&self, auth: Authority<'_>, capture_id: &str, consistency: &str) -> Result<Outcome> {
        let t0 = Instant::now();
        if !valid_id(capture_id, "cp_") {
            return Err(EngineError::invalid("capture_id must match cp_[a-z0-9_]+ (≤60)"));
        }
        let durable = match consistency {
            "" | "volatile" => false,
            "local" => true,
            other => return Err(EngineError::invalid(format!("unsupported consistency {other:?}"))),
        };
        let sb_arc = self.sandbox(auth.sandbox_id)?;
        let mut sb = sb_arc.lock().unwrap();
        Self::check(&sb, auth)?;
        if sb.state != State::Running {
            return Err(EngineError::in_use(format!("sandbox is {}", sb.state.name())));
        }
        let cdir = self.cfg.store.join("captures").join(capture_id);
        if self.captures.lock().unwrap().contains_key(capture_id) || cdir.exists() {
            return Err(EngineError::in_use(format!("capture {capture_id} exists")));
        }
        std::fs::create_dir(&cdir).ctx("create capture dir")?;
        let base = sb.base_mem.clone().ok_or_else(|| EngineError::host("sandbox has no memory base"))?;
        let jail = sb.jail.as_ref().unwrap();
        let uid = jail.uid;
        let mut detail = BTreeMap::new();
        let tmp_mem = jail.path("cap.mem");
        let tmp_state = jail.path("cap.vmstate");
        let _ = std::fs::remove_file(&tmp_mem);
        let _ = std::fs::remove_file(&tmp_state);

        // --- source paused from here ---
        let tp = Instant::now();
        jail.api("PATCH", "/vm", r#"{"state":"Paused"}"#)?;
        let sealed = (|| -> Result<()> {
            let t = Instant::now();
            fsutil::reflink(&base, &tmp_mem, 0o600)?;
            fsutil::chown(&tmp_mem, uid, uid)?;
            std::fs::File::create(&tmp_state).ctx("create state file")?;
            fsutil::chown(&tmp_state, uid, uid)?;
            detail.insert("mem_clone_us".into(), us(t.elapsed()));
            let t = Instant::now();
            let body = format!(
                r#"{{"snapshot_type":"Diff","mem_file_path":"/cap.mem","snapshot_path":"/cap.vmstate","sync_snapshot_files":{durable}}}"#
            );
            jail.api("PUT", "/snapshot/create", &body)?;
            detail.insert("dirty_write_us".into(), us(t.elapsed()));
            let t = Instant::now();
            fsutil::reflink(&jail.path(fc::ROOTFS), &cdir.join(fc::ROOTFS), 0o400)?;
            if durable {
                fsutil::fsync_path(&cdir.join(fc::ROOTFS))?;
            }
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
        let mem = cdir.join(fc::MEM);
        let state = cdir.join(fc::VMSTATE);
        std::fs::rename(&tmp_mem, &mem).ctx("seal memory image")?;
        std::fs::rename(&tmp_state, &state).ctx("seal state")?;
        for p in [&mem, &state] {
            fsutil::chown(p, 0, 0)?;
            fsutil::chmod(p, 0o444)?;
        }
        // The source keeps a link to its newest complete image as its next base.
        let base_link = jail.path("base.mem");
        let _ = std::fs::remove_file(&base_link);
        std::fs::hard_link(&mem, &base_link).ctx("link capture as base")?;
        let meta = serde_json::json!({
            "capture_id": capture_id,
            "source_sandbox_id": sb.id,
            "source_generation": sb.generation,
            "preserves_memory": true,
            "durability": if durable { "local" } else { "volatile" },
            "resources": sb.resources,
            "engine": ENGINE_NAME,
            "engine_digest": self.compat.engine_digest,
            "cpu_baseline": self.compat.cpu_baseline,
            "created_at_unix_ms": now_ms(),
        });
        std::fs::write(cdir.join("meta.json"), meta.to_string()).ctx("write capture meta")?;
        if durable {
            for p in [&mem, &state, &cdir.join("meta.json"), &cdir] {
                fsutil::fsync_path(p)?;
            }
        }
        detail.insert("publish_us".into(), us(t.elapsed()));
        sb.base_mem = Some(mem);
        sb.captures.push(capture_id.into());
        let rec = CaptureRec {
            id: capture_id.into(),
            dir: cdir,
            source_sandbox: sb.id.clone(),
            source_generation: sb.generation,
            resources: sb.resources.clone(),
            durability: if durable { "local" } else { "volatile" }.into(),
            restores: Mutex::new(0),
        };
        self.captures.lock().unwrap().insert(capture_id.into(), Arc::new(rec));
        detail.insert("total_us".into(), us(t0.elapsed()));
        Ok(Outcome {
            state: "captured".into(),
            handles: vec![capture_id.into()],
            detail,
            ..Default::default()
        })
    }

    /// Removes captures that were restored (or whose source is gone) and
    /// that no live sandbox still links as its memory base.
    fn gc_captures(&self) {
        let live: BTreeSet<String> = self.sandboxes.lock().unwrap().keys().cloned().collect();
        let mut caps = self.captures.lock().unwrap();
        caps.retain(|_, c| {
            let consumed = *c.restores.lock().unwrap() > 0 || !live.contains(&c.source_sandbox);
            let links = std::fs::metadata(c.dir.join(fc::MEM))
                .map(|m| std::os::unix::fs::MetadataExt::nlink(&m))
                .unwrap_or(1);
            if consumed && links <= 1 {
                let _ = std::fs::remove_dir_all(&c.dir);
                false
            } else {
                true
            }
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
            if let Some(j) = sb.jail.take() {
                j.destroy();
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
            if let Some(j) = sb.lock().unwrap().jail.take() {
                j.destroy();
            }
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
