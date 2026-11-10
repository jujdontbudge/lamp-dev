//! Private disk backends (architecture §7, "Backing path").
//!
//! * `Reflink` (XFS with `reflink=1`, btrfs): every disk is a raw file;
//!   templates, working disks and sealed capture disks are FICLONE copies,
//!   each an independent writable inode.
//! * `Thin` (ordinary ext4 and anything without reflinks): a host-managed
//!   dm-thin pool on two loop-backed files inside the store. Templates are
//!   thin volumes, a working disk is a thin snapshot of its source, and a
//!   seal is a thin snapshot of the paused working disk. Firecracker gets a
//!   block device node at the same in-jail path (`/rootfs.ext4`), so saved
//!   state is the same for both backends. Nobody is asked to reformat.
//!
//! Durability: a reflink disk is stable once the clone and its directory are
//! fsynced. A thin disk is stable once its data was flushed (fsync of the
//! source device before the snapshot) and the pool metadata transaction that
//! created it is committed; `commit` forces that commit, and the loop devices
//! pass each flush down to the store filesystem as an fsync of the backing
//! file. E04 proves both chains on real filesystems.
use crate::dm::{Control, DevT};
use crate::error::{EngineError, IoContext, Result};
use crate::fsutil;
use std::collections::BTreeSet;
use std::ffi::CString;
use std::os::unix::ffi::OsStrExt;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::Mutex;

const XFS_MAGIC: i64 = 0x5846_5342;
pub const BTRFS_MAGIC: i64 = 0x9123_683e;
/// 64 KiB pool blocks.
const BLOCK_SECTORS: u64 = 128;

/// Where a disk lives, as recorded in capture manifests.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DiskRef {
    /// A file under the store (path relative to the store root).
    File(String),
    /// A thin volume id in this store's pool.
    Thin(u32),
}

impl DiskRef {
    pub fn encode(&self) -> String {
        match self {
            DiskRef::File(p) => format!("file:{p}"),
            DiskRef::Thin(id) => format!("thin:{id}"),
        }
    }
    pub fn decode(s: &str) -> Result<DiskRef> {
        if let Some(p) = s.strip_prefix("file:") {
            if p.is_empty() || p.starts_with('/') || p.split('/').any(|c| c == "..") {
                return Err(EngineError::new("DATA_UNAVAILABLE", "bad disk path"));
            }
            return Ok(DiskRef::File(p.into()));
        }
        if let Some(id) = s.strip_prefix("thin:")
            && let Ok(id) = id.parse::<u32>()
            && id < 1 << 24
        {
            return Ok(DiskRef::Thin(id));
        }
        Err(EngineError::new(
            "DATA_UNAVAILABLE",
            format!("bad disk reference {s:?}"),
        ))
    }
}

/// A sandbox's writable disk, attached inside its jail.
#[derive(Debug)]
pub struct Working {
    pub disk: DiskRef,
    /// Device-mapper name while a thin volume is active.
    pub dm_name: Option<String>,
}

pub enum Backend {
    Reflink,
    Thin(ThinPool),
}

pub fn statfs_type(path: &Path) -> Result<i64> {
    let c = CString::new(path.as_os_str().as_bytes()).unwrap();
    // SAFETY: zeroed statfs buffer filled by the kernel.
    let mut st: libc::statfs = unsafe { std::mem::zeroed() };
    if unsafe { libc::statfs(c.as_ptr(), &mut st) } != 0 {
        return Err(EngineError::host(format!(
            "statfs {}: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(st.f_type as i64)
}

/// Probes whether the store filesystem clones files.
fn reflinks_work(store: &Path) -> bool {
    let dir = store.join("staging");
    let _ = std::fs::create_dir_all(&dir);
    let a = dir.join(".reflink-probe");
    let b = dir.join(".reflink-probe.clone");
    let _ = std::fs::remove_file(&a);
    let _ = std::fs::remove_file(&b);
    let ok = std::fs::write(&a, b"probe").is_ok() && fsutil::reflink(&a, &b, 0o600).is_ok();
    let _ = std::fs::remove_file(&a);
    let _ = std::fs::remove_file(&b);
    ok
}

#[derive(Debug, Clone)]
pub struct ThinConfig {
    pub data_bytes: u64,
    pub meta_bytes: u64,
}

impl Default for ThinConfig {
    fn default() -> Self {
        ThinConfig {
            data_bytes: 1 << 40,
            meta_bytes: 1 << 30,
        }
    }
}

impl Backend {
    /// `auto`: reflink when the store's filesystem is XFS/btrfs and clones
    /// work, the thin pool otherwise.
    pub fn open(store: &Path, choice: &str, thin: &ThinConfig) -> Result<Backend> {
        let kind = statfs_type(store)?;
        let reflink = matches!(kind, XFS_MAGIC | BTRFS_MAGIC) && reflinks_work(store);
        match choice {
            "reflink" if !reflink => Err(EngineError::new(
                "ENGINE_UNQUALIFIED",
                "store filesystem does not support reflinks",
            )),
            "reflink" => Ok(Backend::Reflink),
            "thin" => Ok(Backend::Thin(ThinPool::open(store, thin)?)),
            "auto" | "" if reflink => Ok(Backend::Reflink),
            "auto" | "" => Ok(Backend::Thin(ThinPool::open(store, thin)?)),
            other => Err(EngineError::invalid(format!("unknown disk backend {other}"))),
        }
    }

    pub fn name(&self) -> &'static str {
        match self {
            Backend::Reflink => "reflink",
            Backend::Thin(_) => "dm-thin",
        }
    }

    pub fn thin(&self) -> Option<&ThinPool> {
        match self {
            Backend::Thin(p) => Some(p),
            Backend::Reflink => None,
        }
    }
}

fn mknod_block(path: &Path, dev: DevT, uid: u32) -> Result<()> {
    let c = CString::new(path.as_os_str().as_bytes()).unwrap();
    // SAFETY: valid path; creates a block special file.
    if unsafe { libc::mknod(c.as_ptr(), libc::S_IFBLK | 0o600, dev.raw()) } != 0 {
        return Err(EngineError::host(format!(
            "mknod {}: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    fsutil::chown(path, uid, uid)
}

fn losetup(file: &Path, direct: bool) -> Result<String> {
    let existing = Command::new("losetup")
        .args(["-j"])
        .arg(file)
        .args(["-n", "-O", "NAME"])
        .output()
        .ctx("run losetup")?;
    let text = String::from_utf8_lossy(&existing.stdout);
    if let Some(dev) = text.lines().next().filter(|l| !l.trim().is_empty()) {
        return Ok(dev.trim().to_string());
    }
    let out = Command::new("losetup")
        .args(["--show", "-f"])
        .arg(if direct {
            "--direct-io=on"
        } else {
            "--direct-io=off"
        })
        .arg(file)
        .output()
        .ctx("run losetup")?;
    if !out.status.success() {
        return Err(EngineError::host(format!(
            "losetup {}: {}",
            file.display(),
            String::from_utf8_lossy(&out.stderr).trim()
        )));
    }
    Ok(String::from_utf8_lossy(&out.stdout).trim().to_string())
}

fn dev_of(path: &str) -> Result<DevT> {
    use std::os::unix::fs::MetadataExt;
    let m = std::fs::metadata(path).ctx(format!("stat {path}"))?;
    let rdev = m.rdev();
    Ok(DevT {
        major: libc::major(rdev),
        minor: libc::minor(rdev),
    })
}

/// The store's dm-thin pool.
pub struct ThinPool {
    ctl: Control,
    meta_loop: String,
    /// Prefix of every device-mapper name this store owns.
    prefix: String,
    pool: String,
    pub dir: PathBuf,
    used: Mutex<BTreeSet<u32>>,
}

impl ThinPool {
    pub fn open(store: &Path, cfg: &ThinConfig) -> Result<ThinPool> {
        let dir = store.join("thin");
        std::fs::create_dir_all(dir.join("templates")).ctx("create thin dir")?;
        std::fs::create_dir_all(dir.join("dev")).ctx("create thin dev dir")?;
        fsutil::chmod(&dir, 0o700)?;
        let data = dir.join("data.img");
        let meta = dir.join("meta.img");
        let fresh = !meta.exists();
        for (path, len) in [(&data, cfg.data_bytes), (&meta, cfg.meta_bytes)] {
            if !path.exists() {
                let f = std::fs::File::create(path).ctx("create pool file")?;
                f.set_len(len)?;
                if path == &meta {
                    // A zeroed superblock makes the kernel format the pool.
                    use std::os::unix::fs::FileExt;
                    f.write_all_at(&[0_u8; 4096], 0)?;
                }
                f.sync_all()?;
            }
        }
        if fresh {
            fsutil::fsync_path(&dir)?;
        }
        let canon = std::fs::canonicalize(store).ctx("resolve store")?;
        let prefix = format!(
            "lamp-{}-",
            &crate::manifest::sha256_hex(canon.as_os_str().as_bytes())[..10]
        );
        let pool = format!("{prefix}pool");
        let ctl = Control::open()?;
        let meta_loop = losetup(&meta, false)?;
        if ctl.exists(&pool).is_none() {
            let data_loop = losetup(&data, true)?;
            let sectors = std::fs::metadata(&data)?.len() / 512 / BLOCK_SECTORS * BLOCK_SECTORS;
            let params = format!(
                "{} {} {BLOCK_SECTORS} 1024 1 error_if_no_space",
                dev_of(&meta_loop)?.spec(),
                dev_of(&data_loop)?.spec()
            );
            ctl.create(&pool, sectors, "thin-pool", &params)?;
        }
        let p = ThinPool {
            ctl,
            meta_loop,
            prefix,
            pool,
            dir,
            used: Mutex::new(BTreeSet::new()),
        };
        // No sandbox survives a restart: drop every thin device of ours.
        for name in p.ctl.list()? {
            if name.starts_with(&p.prefix) && name != p.pool {
                p.ctl.remove(&name)?;
            }
        }
        for e in std::fs::read_dir(p.dir.join("dev")).into_iter().flatten().flatten() {
            let _ = std::fs::remove_file(e.path());
        }
        Ok(p)
    }

    pub fn status(&self) -> String {
        self.ctl.status(&self.pool).unwrap_or_default()
    }

    /// Ids known to be in use (so a new id never collides).
    pub fn note_used(&self, id: u32) {
        self.used.lock().unwrap().insert(id);
    }

    pub fn forget(&self, id: u32) {
        self.used.lock().unwrap().remove(&id);
    }

    /// A fresh 24-bit id, not used by any known volume.
    pub fn new_id(&self) -> u32 {
        let mut used = self.used.lock().unwrap();
        loop {
            let b = fsutil::random_bytes::<4>();
            let id = u32::from_le_bytes(b) & 0x00ff_ffff;
            if id != 0 && used.insert(id) {
                return id;
            }
        }
    }

    fn thin_params(&self, id: u32) -> String {
        format!("{} {id}", self.ctl.exists(&self.pool).map(|d| d.spec()).unwrap_or_default())
    }

    fn name(&self, kind: char, id: u32) -> String {
        format!("{}{kind}{id}", self.prefix)
    }

    /// Activates volume `id` and returns its device-mapper name and number.
    pub fn activate(&self, kind: char, id: u32, bytes: u64) -> Result<(String, DevT)> {
        let name = self.name(kind, id);
        let dev = self
            .ctl
            .create(&name, bytes / 512, "thin", &self.thin_params(id))?;
        Ok((name, dev))
    }

    pub fn deactivate(&self, name: &str) -> Result<()> {
        self.ctl.remove(name)
    }

    /// Snapshot of an inactive (or suspended) volume.
    pub fn snapshot(&self, new: u32, origin: u32) -> Result<()> {
        self.ctl
            .message(&self.pool, &format!("create_snap {new} {origin}"))
    }

    pub fn delete(&self, id: u32) -> Result<()> {
        let r = self.ctl.message(&self.pool, &format!("delete {id}"));
        self.forget(id);
        r
    }

    /// Commits the pool's metadata transaction (and flushes it to the
    /// metadata file): reserving a metadata snapshot commits first.
    pub fn commit(&self) -> Result<()> {
        self.ctl.message(&self.pool, "reserve_metadata_snap")?;
        self.ctl.message(&self.pool, "release_metadata_snap")
    }

    /// Snapshot of a running (paused-VM) working volume: flush its page
    /// cache, suspend it, snapshot, resume.
    pub fn seal(&self, working: &str, node: &Path, origin: u32, new: u32) -> Result<()> {
        std::fs::File::open(node)
            .and_then(|f| f.sync_all())
            .ctx("flush working disk")?;
        self.ctl.suspend(working)?;
        let snap = self.snapshot(new, origin);
        let resumed = self.ctl.resume(working);
        snap.and(resumed)
    }

    /// Imports a raw image file as thin volume `id` (data extents only).
    pub fn import(&self, id: u32, file: &Path) -> Result<()> {
        use std::os::unix::fs::FileExt;
        let len = std::fs::metadata(file).ctx("stat image")?.len();
        self.ctl
            .message(&self.pool, &format!("create_thin {id}"))?;
        let (name, dev) = self.activate('i', id, len)?;
        let node = self.dir.join("dev").join(&name);
        let _ = std::fs::remove_file(&node);
        let copy = (|| -> Result<()> {
            mknod_block(&node, dev, 0)?;
            let src = std::fs::File::open(file)?;
            let dst = std::fs::OpenOptions::new().write(true).open(&node)?;
            let fd = std::os::fd::AsRawFd::as_raw_fd(&src);
            let mut buf = vec![0_u8; 4 << 20];
            let mut off = 0_u64;
            while off < len {
                // SAFETY: lseek on a valid descriptor.
                let data = unsafe { libc::lseek(fd, off as libc::off_t, libc::SEEK_DATA) };
                if data < 0 {
                    break;
                }
                let data = data as u64;
                // SAFETY: as above.
                let end = unsafe { libc::lseek(fd, data as libc::off_t, libc::SEEK_HOLE) } as u64;
                let mut at = data;
                while at < end.min(len) {
                    let n = (end.min(len) - at).min(buf.len() as u64) as usize;
                    src.read_exact_at(&mut buf[..n], at)?;
                    dst.write_all_at(&buf[..n], at)?;
                    at += n as u64;
                }
                off = end;
            }
            dst.sync_all()?;
            Ok(())
        })();
        let _ = std::fs::remove_file(&node);
        self.deactivate(&name)?;
        copy?;
        self.commit()
    }

    /// Every volume id in the pool, read from a metadata snapshot with
    /// `thin_dump` (thin-provisioning-tools).
    pub fn volumes(&self) -> Result<BTreeSet<u32>> {
        self.ctl.message(&self.pool, "reserve_metadata_snap")?;
        let out = Command::new("thin_dump")
            .args(["--metadata-snap", "--format", "xml"])
            .arg(&self.meta_loop)
            .output();
        let released = self.ctl.message(&self.pool, "release_metadata_snap");
        let out = out.ctx("run thin_dump")?;
        released?;
        if !out.status.success() {
            return Err(EngineError::host(format!(
                "thin_dump: {}",
                String::from_utf8_lossy(&out.stderr).trim()
            )));
        }
        let text = String::from_utf8_lossy(&out.stdout);
        let mut ids = BTreeSet::new();
        for part in text.split("dev_id=\"").skip(1) {
            if let Some(n) = part.split('"').next().and_then(|n| n.parse().ok()) {
                ids.insert(n);
            }
        }
        Ok(ids)
    }

    /// Makes the in-jail device node for an active volume.
    pub fn node(&self, at: &Path, dev: DevT, uid: u32) -> Result<()> {
        mknod_block(at, dev, uid)
    }
}

/// Bytes of a disk object (the template's or a capture's).
pub fn size_of_file(path: &Path) -> Result<u64> {
    Ok(std::fs::metadata(path)
        .ctx(format!("stat {}", path.display()))?
        .len())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn disk_refs_round_trip_and_reject_escapes() {
        for r in [DiskRef::File("state/objects/d1".into()), DiskRef::Thin(77)] {
            assert_eq!(DiskRef::decode(&r.encode()).unwrap(), r);
        }
        for bad in ["file:/etc/passwd", "file:a/../../b", "thin:16777216", "thin:x", "x"] {
            assert!(DiskRef::decode(bad).is_err(), "{bad}");
        }
    }
}
