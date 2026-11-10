//! Durable (`local`) capture state: objects, signed manifests and the
//! journal that commits them (architecture §6.4, §7).
//!
//! ```text
//! <store>/state/journal          append-only records, preallocated, framed
//! <store>/state/objects/<name>   immutable objects: memory deltas, vm state,
//!                                reflink disks (thin disks live in the pool)
//! <store>/state/quarantine/      refused imports
//! ```
//!
//! A capture commits in this order, each step a crash boundary of E04:
//! reserve (journal) → capture → seal → object_flush → directory_flush →
//! heads (commit record with the signed manifest, written) → switch →
//! journal_flush (that record's fdatasync) → publish → ack. Nothing is
//! acknowledged before its objects, their directory entries and the commit
//! record passed fsync/fdatasync.
//!
//! Each barrier is the only thing that makes its step durable. The commit
//! record overwrites preallocated, already written journal blocks, so its
//! fdatasync carries no filesystem metadata: on ext4/XFS, whose journals
//! are global, a later metadata commit cannot quietly cover an omitted
//! `directory_flush` (E04's negative controls rely on this). Recovery
//! trusts only committed records: an uncommitted capture is unreachable
//! garbage, and a committed one keeps its ancestors (a capture with a live
//! descendant cannot be deleted). Objects are verified against the signed
//! manifest before any restore parses them; a manifest from a producer key
//! that is not enrolled is refused and quarantined.
//!
//! Journal framing: `[u32 len][8-byte SHA-256 prefix][len bytes JSON]`,
//! written in place over preallocated zero blocks so that an fdatasync of a
//! record flushes only that data. Replay stops at the first zero length; a
//! damaged frame is a torn (uncommitted) tail only if nothing valid follows
//! it, otherwise the store refuses to open.
use crate::disk::DiskRef;
use crate::error::{EngineError, IoContext, Result};
use crate::fault;
use crate::fsutil;
use crate::manifest::{self, Resources};
use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::fs::{File, OpenOptions};
use std::os::unix::fs::FileExt;
use std::path::{Path, PathBuf};
use std::sync::Mutex;

pub const FORMAT: &str = "lamp.capture/0";
const GROW: u64 = 4 << 20;
const FRAME: usize = 12;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct MemoryRef {
    /// Capture whose full image this delta applies to; `None` = template.
    pub base: Option<String>,
    pub object: String,
    /// `sha256-c4m` of the object file.
    pub digest: String,
    pub size: u64,
    pub image_bytes: u64,
    pub changed_pages: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ObjectRef {
    pub object: String,
    pub sha256: String,
    pub size: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DiskRecord {
    /// `file:<path under store>` or `thin:<id>`.
    pub disk: String,
    pub bytes: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CaptureManifest {
    pub format: String,
    pub capture_id: String,
    /// Durable ancestor (lineage); kept as long as this capture lives.
    pub parent: Option<String>,
    pub source_sandbox_id: String,
    pub source_generation: u64,
    pub template_id: String,
    pub resources: Resources,
    pub memory: MemoryRef,
    pub vmstate: ObjectRef,
    pub disk: DiskRecord,
    pub engine_digest: String,
    pub cpu_baseline: String,
    pub state_abi: String,
    pub producer_key_id: String,
    pub producer_host_id: String,
    pub created_at_unix_ms: u64,
    pub signature: String,
}

impl CaptureManifest {
    fn signing_bytes(&self) -> Result<Vec<u8>> {
        let mut unsigned = self.clone();
        unsigned.signature.clear();
        manifest::canonical_json(&unsigned)
    }

    pub fn sign(&mut self, key: &SigningKey) -> Result<()> {
        self.producer_key_id = manifest::key_id(&key.verifying_key());
        self.signature = lamp_init::hex_encode(&key.sign(&self.signing_bytes()?).to_bytes());
        Ok(())
    }

    /// Checks the producer is enrolled and the signature binds every field.
    pub fn verify(&self, trusted: &HashMap<String, VerifyingKey>) -> Result<()> {
        let key = trusted.get(&self.producer_key_id).ok_or_else(|| {
            EngineError::new(
                "HOST_NOT_TRUSTED",
                format!(
                    "capture {} was produced by {}, which is not an enrolled producer",
                    self.capture_id, self.producer_key_id
                ),
            )
        })?;
        let bytes = lamp_init::hex_decode(&self.signature)
            .and_then(|b| Signature::from_slice(&b).ok())
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "malformed capture signature"))?;
        key.verify(&self.signing_bytes()?, &bytes)
            .map_err(|_| EngineError::new("DATA_UNAVAILABLE", "capture signature does not verify"))
    }

    pub fn disk_ref(&self) -> Result<DiskRef> {
        DiskRef::decode(&self.disk.disk)
    }

    /// Every object name this manifest references (not thin volumes).
    pub fn objects(&self) -> Vec<String> {
        let mut v = vec![self.memory.object.clone(), self.vmstate.object.clone()];
        if let Ok(DiskRef::File(p)) = self.disk_ref()
            && let Some(name) = p.strip_prefix("state/objects/")
        {
            v.push(name.to_string());
        }
        v
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "op", rename_all = "snake_case")]
pub enum Record {
    Reserve {
        capture: String,
        objects: Vec<String>,
        thin: Vec<u32>,
    },
    Commit {
        capture: String,
        manifest: CaptureManifest,
    },
    Redirect {
        capture: String,
        manifest: CaptureManifest,
        retire: Vec<String>,
    },
    Delete {
        capture: String,
    },
}

fn checksum(payload: &[u8]) -> [u8; 8] {
    let d = Sha256::digest(payload);
    d[..8].try_into().unwrap()
}

struct Journal {
    file: File,
    end: u64,
    len: u64,
}

impl Journal {
    fn open(path: &Path) -> Result<(Journal, Vec<Record>)> {
        let fresh = !path.exists();
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .create(true)
            .truncate(false)
            .open(path)
            .ctx("open state journal")?;
        let mut j = Journal {
            len: file.metadata()?.len(),
            file,
            end: 0,
        };
        if fresh || j.len == 0 {
            j.grow()?;
            if let Some(dir) = path.parent() {
                fsutil::fsync_path(dir)?;
            }
        }
        let records = j.replay()?;
        Ok((j, records))
    }

    /// Extends the log with real zero blocks, made durable with their size.
    fn grow(&mut self) -> Result<()> {
        let zeros = vec![0_u8; 1 << 20];
        let mut at = self.len;
        while at < self.len + GROW {
            self.file.write_all_at(&zeros, at)?;
            at += zeros.len() as u64;
        }
        self.file.sync_all().ctx("sync journal growth")?;
        self.len = at;
        Ok(())
    }

    fn replay(&mut self) -> Result<Vec<Record>> {
        let mut out = Vec::new();
        let mut at = 0_u64;
        let mut head = [0_u8; FRAME];
        while at + FRAME as u64 <= self.len {
            self.file.read_exact_at(&mut head, at)?;
            let n = u32::from_le_bytes(head[..4].try_into().unwrap()) as u64;
            if n == 0 {
                break;
            }
            let ok = (|| {
                if at + FRAME as u64 + n > self.len || n > 1 << 20 {
                    return None;
                }
                let mut payload = vec![0_u8; n as usize];
                self.file
                    .read_exact_at(&mut payload, at + FRAME as u64)
                    .ok()?;
                if checksum(&payload) != head[4..12] {
                    return None;
                }
                serde_json::from_slice::<Record>(&payload).ok()
            })();
            match ok {
                Some(r) => {
                    out.push(r);
                    at += FRAME as u64 + n;
                }
                None => {
                    // A torn tail is the uncommitted end; anything valid
                    // after it means the committed history is damaged.
                    if self.valid_frame_after(at + 1)? {
                        return Err(EngineError::new(
                            "DATA_UNAVAILABLE",
                            "state journal is damaged inside its committed history",
                        ));
                    }
                    let zeros = vec![0_u8; (self.len - at).min(1 << 20) as usize];
                    self.file.write_all_at(&zeros, at)?;
                    self.file.sync_data()?;
                    break;
                }
            }
        }
        self.end = at;
        Ok(out)
    }

    fn valid_frame_after(&self, from: u64) -> Result<bool> {
        // Scan for any well-formed frame (rare path: only after damage).
        let mut buf = vec![0_u8; (self.len - from.min(self.len)) as usize];
        self.file.read_exact_at(&mut buf, from)?;
        for i in 0..buf.len().saturating_sub(FRAME) {
            let n = u32::from_le_bytes(buf[i..i + 4].try_into().unwrap()) as usize;
            if n == 0 || n > 1 << 20 || i + FRAME + n > buf.len() {
                continue;
            }
            let payload = &buf[i + FRAME..i + FRAME + n];
            if checksum(payload) == buf[i + 4..i + 12]
                && serde_json::from_slice::<Record>(payload).is_ok()
            {
                return Ok(true);
            }
        }
        Ok(false)
    }

    fn append(&mut self, r: &Record, sync: bool) -> Result<()> {
        let payload = serde_json::to_vec(r).map_err(|e| EngineError::host(e.to_string()))?;
        let need = (FRAME + payload.len()) as u64;
        while self.end + need + FRAME as u64 > self.len {
            self.grow()?;
        }
        let mut frame = Vec::with_capacity(need as usize);
        frame.extend_from_slice(&(payload.len() as u32).to_le_bytes());
        frame.extend_from_slice(&checksum(&payload));
        frame.extend_from_slice(&payload);
        self.file.write_all_at(&frame, self.end)?;
        self.end += need;
        if sync {
            self.sync()?;
        }
        Ok(())
    }

    fn sync(&self) -> Result<()> {
        self.file.sync_data().ctx("sync state journal")
    }
}

/// A committed, live capture.
#[derive(Debug, Clone)]
pub struct Entry {
    pub manifest: CaptureManifest,
}

#[derive(Debug, Default)]
struct Index {
    live: BTreeMap<String, Entry>,
    deleted: BTreeSet<String>,
    /// Committed but unusable at recovery (missing/damaged/untrusted).
    damaged: BTreeMap<String, String>,
    /// Objects and thin volumes reserved by captures still in flight.
    in_flight: BTreeMap<String, (Vec<String>, Vec<u32>)>,
    /// Thin volumes ever reserved for durable captures (journal).
    reserved_thin: BTreeSet<u32>,
}

pub struct Store {
    pub root: PathBuf,
    journal: Mutex<Journal>,
    index: Mutex<Index>,
}

/// What `list` reports per capture.
#[derive(Debug, Clone, Serialize)]
pub struct Listed {
    pub capture_id: String,
    pub state: String,
    pub parent: Option<String>,
    pub memory_base: Option<String>,
    pub source_sandbox_id: String,
    pub template_id: String,
    pub producer_key_id: String,
    pub reason: Option<String>,
}

pub fn sha256_file(path: &Path) -> Result<String> {
    let bytes = std::fs::read(path).ctx(format!("read {}", path.display()))?;
    Ok(manifest::sha256_hex(&bytes))
}

impl Store {
    pub fn objects(&self) -> PathBuf {
        self.root.join("objects")
    }
    pub fn object(&self, name: &str) -> PathBuf {
        self.objects().join(name)
    }

    /// Opens and recovers the state under `store/state`.
    pub fn open(store: &Path, trusted: &HashMap<String, VerifyingKey>) -> Result<Store> {
        let root = store.join("state");
        for d in ["objects", "quarantine", "incoming"] {
            std::fs::create_dir_all(root.join(d)).ctx("create state dirs")?;
        }
        fsutil::chmod(&root, 0o700)?;
        fsutil::fsync_path(&root)?;
        fsutil::fsync_path(store)?;
        let (journal, records) = Journal::open(&root.join("journal"))?;
        let s = Store {
            root,
            journal: Mutex::new(journal),
            index: Mutex::new(Index::default()),
        };
        s.recover(records, trusted);
        Ok(s)
    }

    fn recover(&self, records: Vec<Record>, trusted: &HashMap<String, VerifyingKey>) {
        let mut last: BTreeMap<String, CaptureManifest> = BTreeMap::new();
        let mut ix = self.index.lock().unwrap();
        for r in records {
            match r {
                Record::Reserve { thin, .. } => ix.reserved_thin.extend(thin),
                Record::Commit { capture, manifest }
                | Record::Redirect {
                    capture, manifest, ..
                } => {
                    last.insert(capture, manifest);
                }
                Record::Delete { capture } => {
                    last.remove(&capture);
                    ix.deleted.insert(capture);
                }
            }
        }
        for (cid, m) in last {
            match self.load_entry(&cid, m, trusted) {
                Ok(e) => {
                    ix.live.insert(cid, e);
                }
                Err(e) => {
                    eprintln!("lamp-engine: capture {cid} is not recoverable: {e}");
                    ix.damaged.insert(cid, e.to_string());
                }
            }
        }
        // A capture whose ancestor or memory base did not survive is lost too.
        loop {
            let broken: Vec<(String, String)> = ix
                .live
                .iter()
                .filter_map(|(cid, e)| {
                    let m = &e.manifest;
                    for dep in m.parent.iter().chain(m.memory.base.iter()) {
                        if !ix.live.contains_key(dep) {
                            return Some((cid.clone(), format!("ancestor {dep} is missing")));
                        }
                    }
                    None
                })
                .collect();
            if broken.is_empty() {
                break;
            }
            for (cid, why) in broken {
                ix.live.remove(&cid);
                ix.damaged.insert(cid, why);
            }
        }
    }

    fn load_entry(
        &self,
        cid: &str,
        m: CaptureManifest,
        trusted: &HashMap<String, VerifyingKey>,
    ) -> Result<Entry> {
        if m.capture_id != cid || m.format != FORMAT {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "commit record names another capture",
            ));
        }
        m.verify(trusted)?;
        for (obj, size) in [
            (&m.memory.object, m.memory.size),
            (&m.vmstate.object, m.vmstate.size),
        ] {
            let got = std::fs::metadata(self.object(obj)).map(|x| x.len()).ok();
            if got != Some(size) {
                return Err(EngineError::new(
                    "DATA_UNAVAILABLE",
                    format!("object {obj} is missing or truncated"),
                ));
            }
        }
        if let DiskRef::File(p) = m.disk_ref()? {
            let got = std::fs::metadata(self.root.parent().unwrap().join(&p))
                .map(|x| x.len())
                .ok();
            if got != Some(m.disk.bytes) {
                return Err(EngineError::new(
                    "DATA_UNAVAILABLE",
                    format!("disk {p} is missing or truncated"),
                ));
            }
        }
        Ok(Entry { manifest: m })
    }

    pub fn get(&self, cid: &str) -> Option<Entry> {
        self.index.lock().unwrap().live.get(cid).cloned()
    }

    pub fn live(&self) -> Vec<Entry> {
        self.index.lock().unwrap().live.values().cloned().collect()
    }

    pub fn exists(&self, cid: &str) -> bool {
        let ix = self.index.lock().unwrap();
        ix.live.contains_key(cid)
            || ix.deleted.contains(cid)
            || ix.damaged.contains_key(cid)
            || ix.in_flight.contains_key(cid)
    }

    pub fn list(&self) -> Vec<Listed> {
        let ix = self.index.lock().unwrap();
        let mut out: Vec<Listed> = ix
            .live
            .values()
            .map(|e| Listed {
                capture_id: e.manifest.capture_id.clone(),
                state: "committed".into(),
                parent: e.manifest.parent.clone(),
                memory_base: e.manifest.memory.base.clone(),
                source_sandbox_id: e.manifest.source_sandbox_id.clone(),
                template_id: e.manifest.template_id.clone(),
                producer_key_id: e.manifest.producer_key_id.clone(),
                reason: None,
            })
            .collect();
        for (cid, why) in &ix.damaged {
            out.push(Listed {
                capture_id: cid.clone(),
                state: "damaged".into(),
                parent: None,
                memory_base: None,
                source_sandbox_id: String::new(),
                template_id: String::new(),
                producer_key_id: String::new(),
                reason: Some(why.clone()),
            });
        }
        out
    }

    /// Thin volumes referenced by live captures, plus every thin volume a
    /// durable capture ever reserved (for garbage collection).
    pub fn thin_ids(&self) -> (BTreeSet<u32>, BTreeSet<u32>) {
        let ix = self.index.lock().unwrap();
        let mut live: BTreeSet<u32> = ix
            .live
            .values()
            .filter_map(|e| match e.manifest.disk_ref() {
                Ok(DiskRef::Thin(id)) => Some(id),
                _ => None,
            })
            .collect();
        for (_, (_, thin)) in ix.in_flight.values().map(|v| ((), v)) {
            live.extend(thin.iter().copied());
        }
        (live, ix.reserved_thin.clone())
    }

    /// Step `reserve`: the capture's object names and thin volumes are on
    /// record before any of them exists, so recovery can collect them.
    pub fn reserve(&self, cid: &str, objects: Vec<String>, thin: Vec<u32>) -> Result<()> {
        {
            let mut ix = self.index.lock().unwrap();
            if ix.live.contains_key(cid)
                || ix.deleted.contains(cid)
                || ix.damaged.contains_key(cid)
                || ix.in_flight.contains_key(cid)
            {
                return Err(EngineError::in_use(format!("capture {cid} exists")));
            }
            ix.in_flight
                .insert(cid.into(), (objects.clone(), thin.clone()));
            ix.reserved_thin.extend(thin.iter().copied());
        }
        let r = Record::Reserve {
            capture: cid.into(),
            objects,
            thin,
        };
        let res = self.journal.lock().unwrap().append(&r, true);
        if res.is_err() {
            self.abandon(cid);
        }
        res
    }

    /// Forgets an in-flight capture that failed before it committed. Its
    /// objects are garbage for the next collection.
    pub fn abandon(&self, cid: &str) {
        self.index.lock().unwrap().in_flight.remove(cid);
    }

    /// Step `directory_flush`: temporary names become final names, and the
    /// objects directory is fsynced.
    pub fn publish_objects(&self, names: &[String]) -> Result<()> {
        for n in names {
            std::fs::rename(self.object(&format!("{n}.tmp")), self.object(n))
                .ctx(format!("name object {n}"))?;
        }
        if !fault::omitted("directory_flush") {
            fsutil::fsync_path(&self.objects())?;
        }
        Ok(())
    }

    /// Step `heads`: the commit record carrying the signed manifest is
    /// written into the journal (not yet flushed).
    pub fn stage(&self, m: &CaptureManifest) -> Result<()> {
        let r = Record::Commit {
            capture: m.capture_id.clone(),
            manifest: m.clone(),
        };
        self.journal.lock().unwrap().append(&r, false)
    }

    /// Step `journal_flush`: the staged commit record reaches stable
    /// storage. From here the capture is recoverable.
    pub fn flush(&self) -> Result<()> {
        if fault::omitted("journal_flush") {
            return Ok(());
        }
        self.journal.lock().unwrap().sync()
    }

    /// Step `publish`: restorable from now on.
    pub fn insert(&self, m: CaptureManifest) {
        let mut ix = self.index.lock().unwrap();
        ix.in_flight.remove(&m.capture_id);
        ix.live.insert(m.capture_id.clone(), Entry { manifest: m });
    }

    /// Deletes a capture unless a live capture descends from it or uses it
    /// as its memory base (ancestors are kept).
    pub fn delete(&self, cid: &str) -> Result<()> {
        {
            let ix = self.index.lock().unwrap();
            if !ix.live.contains_key(cid) {
                return Err(EngineError::not_found(format!("capture {cid}")));
            }
            if let Some(child) = ix.live.values().find(|e| {
                e.manifest.parent.as_deref() == Some(cid)
                    || e.manifest.memory.base.as_deref() == Some(cid)
            }) {
                return Err(EngineError::in_use(format!(
                    "capture {cid} is an ancestor of {}",
                    child.manifest.capture_id
                )));
            }
        }
        fault::step("delete", || {
            self.journal.lock().unwrap().append(
                &Record::Delete {
                    capture: cid.into(),
                },
                true,
            )?;
            let mut ix = self.index.lock().unwrap();
            ix.live.remove(cid);
            ix.deleted.insert(cid.into());
            Ok(())
        })
    }

    /// Object files no live or in-flight capture references.
    pub fn unreferenced(&self) -> Vec<PathBuf> {
        let ix = self.index.lock().unwrap();
        let mut keep: BTreeSet<String> = BTreeSet::new();
        for e in ix.live.values() {
            keep.extend(e.manifest.objects());
        }
        for (objs, _) in ix.in_flight.values() {
            for o in objs {
                keep.insert(o.clone());
                keep.insert(format!("{o}.tmp"));
            }
        }
        std::fs::read_dir(self.objects())
            .into_iter()
            .flatten()
            .flatten()
            .filter(|e| !keep.contains(&*e.file_name().to_string_lossy()))
            .map(|e| e.path())
            .collect()
    }

    /// Removes unreferenced object files. Returns their count.
    pub fn gc_files(&self) -> Result<usize> {
        let objs = self.unreferenced();
        for p in &objs {
            std::fs::remove_file(p).ctx(format!("remove {}", p.display()))?;
        }
        if !objs.is_empty() {
            fsutil::fsync_path(&self.objects())?;
        }
        Ok(objs.len())
    }

    /// Step `compact_redirect`: atomically points `cid` at a new manifest
    /// (new objects already durable). `retire` names objects the old
    /// manifest used and the new one does not.
    pub fn redirect(&self, m: CaptureManifest, retire: Vec<String>) -> Result<()> {
        let cid = m.capture_id.clone();
        self.journal.lock().unwrap().append(
            &Record::Redirect {
                capture: cid.clone(),
                manifest: m.clone(),
                retire,
            },
            true,
        )?;
        self.index
            .lock()
            .unwrap()
            .live
            .insert(cid, Entry { manifest: m });
        Ok(())
    }

    /// Verifies the memory and state objects against the manifest digests.
    pub fn verify_objects(&self, m: &CaptureManifest) -> Result<()> {
        let mem = fsutil::digest_file(&self.object(&m.memory.object))?;
        if mem.digest != m.memory.digest || mem.size != m.memory.size {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                format!("capture {}: memory object digest mismatch", m.capture_id),
            ));
        }
        let st = sha256_file(&self.object(&m.vmstate.object))?;
        if st != m.vmstate.sha256 {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                format!("capture {}: vm state digest mismatch", m.capture_id),
            ));
        }
        Ok(())
    }

    /// Marks a committed capture unusable (digest failure at restore) and
    /// keeps the reason; its files stay for inspection.
    pub fn quarantine(&self, cid: &str, why: &str) {
        let mut ix = self.index.lock().unwrap();
        if ix.live.remove(cid).is_some() {
            ix.damaged.insert(cid.into(), why.into());
        }
    }

    /// Moves a refused import aside without parsing it further.
    pub fn quarantine_dir(&self, dir: &Path) -> PathBuf {
        let dst = self.root.join("quarantine").join(format!(
            "import-{}",
            lamp_init::hex_encode(&fsutil::random_bytes::<6>())
        ));
        if std::fs::rename(dir, &dst).is_err() {
            let _ = std::fs::create_dir_all(&dst);
        }
        dst
    }
}

/// Object names for a new capture.
pub fn object_names(cid: &str) -> (String, String, String) {
    let tag = lamp_init::hex_encode(&fsutil::random_bytes::<4>());
    (
        format!("{cid}-mem-{tag}"),
        format!("{cid}-vmstate-{tag}"),
        format!("{cid}-disk-{tag}"),
    )
}

pub fn sign_bytes(key: &SigningKey, bytes: &[u8]) -> String {
    lamp_init::hex_encode(&key.sign(bytes).to_bytes())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn dir(tag: &str) -> PathBuf {
        let d = std::env::temp_dir().join(format!("lamp-durable-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    fn manifest(cid: &str, parent: Option<&str>, key: &SigningKey) -> CaptureManifest {
        let mut m = CaptureManifest {
            format: FORMAT.into(),
            capture_id: cid.into(),
            parent: parent.map(String::from),
            source_sandbox_id: "sb_x".into(),
            source_generation: 1,
            template_id: "tp_x".into(),
            resources: Resources {
                vcpus: 2,
                memory_mib: 4096,
                disk_gib: 20,
            },
            memory: MemoryRef {
                base: None,
                object: format!("{cid}-mem"),
                digest: String::new(),
                size: 3,
                image_bytes: 4096,
                changed_pages: 0,
            },
            vmstate: ObjectRef {
                object: format!("{cid}-vmstate"),
                sha256: String::new(),
                size: 2,
            },
            disk: DiskRecord {
                disk: "thin:5".into(),
                bytes: 1 << 20,
            },
            engine_digest: String::new(),
            cpu_baseline: String::new(),
            state_abi: String::new(),
            producer_key_id: String::new(),
            producer_host_id: "h".into(),
            created_at_unix_ms: 1,
            signature: String::new(),
        };
        m.sign(key).unwrap();
        m
    }

    fn commit(s: &Store, m: &CaptureManifest) {
        s.reserve(&m.capture_id, m.objects(), vec![5]).unwrap();
        std::fs::write(s.object(&format!("{}.tmp", m.memory.object)), b"mem").unwrap();
        std::fs::write(s.object(&format!("{}.tmp", m.vmstate.object)), b"vs").unwrap();
        s.publish_objects(&[m.memory.object.clone(), m.vmstate.object.clone()])
            .unwrap();
        s.stage(m).unwrap();
        s.flush().unwrap();
        s.insert(m.clone());
    }

    #[test]
    fn committed_captures_and_ancestors_survive_reopen() {
        let d = dir("reopen");
        let key = SigningKey::from_bytes(&[9; 32]);
        let trusted: HashMap<_, _> =
            [(manifest::key_id(&key.verifying_key()), key.verifying_key())].into();
        {
            let s = Store::open(&d, &trusted).unwrap();
            commit(&s, &manifest("cp_a", None, &key));
            commit(&s, &manifest("cp_b", Some("cp_a"), &key));
            // Reserved but never committed: garbage after recovery.
            s.reserve("cp_c", vec!["cp_c-mem".into()], vec![]).unwrap();
            std::fs::write(s.object("cp_c-mem.tmp"), b"x").unwrap();
            assert!(s.delete("cp_a").is_err(), "an ancestor must be kept");
        }
        let s = Store::open(&d, &trusted).unwrap();
        assert!(s.get("cp_a").is_some() && s.get("cp_b").is_some());
        assert!(s.get("cp_c").is_none());
        assert_eq!(s.gc_files().unwrap(), 1);
        s.delete("cp_b").unwrap();
        s.delete("cp_a").unwrap();
        drop(s);
        let s = Store::open(&d, &trusted).unwrap();
        assert!(s.live().is_empty());
        assert_eq!(s.gc_files().unwrap(), 4);
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn unenrolled_producers_and_damaged_manifests_are_not_recovered() {
        let d = dir("trust");
        let key = SigningKey::from_bytes(&[9; 32]);
        let rogue = SigningKey::from_bytes(&[8; 32]);
        let trusted: HashMap<_, _> =
            [(manifest::key_id(&key.verifying_key()), key.verifying_key())].into();
        let m = manifest("cp_r", None, &rogue);
        assert_eq!(m.verify(&trusted).unwrap_err().code, "HOST_NOT_TRUSTED");
        let mut forged = manifest("cp_f", None, &key);
        forged.parent = Some("cp_x".into());
        assert!(forged.verify(&trusted).is_err());
        {
            let s = Store::open(&d, &trusted).unwrap();
            commit(&s, &manifest("cp_a", None, &key));
            // A validly framed commit of a rogue producer's capture, and one
            // whose manifest was altered after signing.
            commit(&s, &m);
            s.stage(&forged).unwrap();
            s.flush().unwrap();
        }
        let s = Store::open(&d, &trusted).unwrap();
        assert!(s.get("cp_a").is_some());
        assert!(s.get("cp_r").is_none() && s.get("cp_f").is_none());
        let damaged: Vec<_> = s
            .list()
            .into_iter()
            .filter(|l| l.state == "damaged")
            .collect();
        assert_eq!(damaged.len(), 2);
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn a_torn_journal_tail_is_dropped_and_inner_damage_refused() {
        let key = SigningKey::from_bytes(&[9; 32]);
        let trusted: HashMap<_, _> =
            [(manifest::key_id(&key.verifying_key()), key.verifying_key())].into();
        let setup = |tag: &str| {
            let d = dir(tag);
            let s = Store::open(&d, &trusted).unwrap();
            commit(&s, &manifest("cp_a", None, &key));
            let after_a = s.journal.lock().unwrap().end;
            commit(&s, &manifest("cp_b", None, &key));
            let after_b = s.journal.lock().unwrap().end;
            (d, after_a, after_b)
        };
        let poke = |d: &Path, at: u64| {
            let j = OpenOptions::new()
                .write(true)
                .open(d.join("state/journal"))
                .unwrap();
            j.write_all_at(&[0xff], at).unwrap();
        };
        // Tear the last record (cp_b's commit): cp_b is simply uncommitted.
        let (d, _, after_b) = setup("torn");
        poke(&d, after_b - 3);
        let s = Store::open(&d, &trusted).unwrap();
        assert!(s.get("cp_a").is_some() && s.get("cp_b").is_none());
        drop(s);
        std::fs::remove_dir_all(&d).unwrap();
        // Damage cp_a's commit, with valid history after it: refuse.
        let (d, after_a, _) = setup("inner");
        poke(&d, after_a - 3);
        assert_eq!(
            Store::open(&d, &trusted).err().unwrap().code,
            "DATA_UNAVAILABLE"
        );
        std::fs::remove_dir_all(&d).unwrap();
    }
}
