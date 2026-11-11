//! Store primitives: reflink clones, hard links, sparse digests and zero-page
//! hole punching. The store must be a reflink-capable filesystem (XFS with
//! reflink=1 on the reference Linux host; APFS `clonefile` on Macs).
use crate::error::{EngineError, IoContext, Result};
use crate::manifest::{CHUNK, Object, combine, zero_chunk_digest};
use sha2::{Digest, Sha256};
use std::ffi::CString;
use std::fs::{File, OpenOptions};
use std::io::{Read, Seek, SeekFrom};
use std::os::fd::AsRawFd;
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::Path;

#[cfg(target_os = "linux")]
const FICLONE: u64 = 0x4004_9409;

/// Creates `dst` as a copy-on-write clone of `src` (APFS `clonefile`: one
/// metadata operation, independent writable inode, same volume only).
#[cfg(target_os = "macos")]
pub fn reflink(src: &Path, dst: &Path, mode: u32) -> Result<()> {
    unsafe extern "C" {
        fn clonefile(src: *const libc::c_char, dst: *const libc::c_char, flags: u32)
        -> libc::c_int;
    }
    const CLONE_NOFOLLOW: u32 = 1;
    let (s, d) = (cstr(src)?, cstr(dst)?);
    // SAFETY: valid C strings.
    if unsafe { clonefile(s.as_ptr(), d.as_ptr(), CLONE_NOFOLLOW) } != 0 {
        return Err(EngineError::host(format!(
            "clonefile {} -> {}: {} (store must be one APFS volume)",
            src.display(),
            dst.display(),
            std::io::Error::last_os_error()
        )));
    }
    chmod(dst, mode)
}

#[cfg(target_os = "macos")]
fn cstr(p: &Path) -> Result<CString> {
    CString::new(p.as_os_str().as_bytes()).map_err(|_| EngineError::invalid("NUL in path"))
}

/// Stable-storage barrier for one file or directory: `F_FULLFSYNC` on
/// macOS (an ordinary fsync there does not flush the drive's cache).
#[cfg(target_os = "macos")]
pub fn full_fsync(path: &Path) -> Result<()> {
    let f = File::open(path).ctx(format!("open {}", path.display()))?;
    // SAFETY: fcntl on our descriptor.
    if unsafe { libc::fcntl(f.as_raw_fd(), libc::F_FULLFSYNC) } != 0 {
        return Err(EngineError::host(format!(
            "F_FULLFSYNC {}: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

/// Creates `dst` as a copy-on-write clone of `src` (constant time on XFS).
#[cfg(target_os = "linux")]
pub fn reflink(src: &Path, dst: &Path, mode: u32) -> Result<()> {
    let s = File::open(src).ctx(format!("open {}", src.display()))?;
    let d = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(mode)
        .open(dst)
        .ctx(format!("create {}", dst.display()))?;
    // SAFETY: both descriptors are valid for the ioctl.
    if unsafe { libc::ioctl(d.as_raw_fd(), FICLONE as libc::Ioctl, s.as_raw_fd()) } != 0 {
        let e = std::io::Error::last_os_error();
        let _ = std::fs::remove_file(dst);
        return Err(EngineError::host(format!(
            "reflink {} -> {}: {e} (store must support reflinks)",
            src.display(),
            dst.display()
        )));
    }
    Ok(())
}

pub fn chown(path: &Path, uid: u32, gid: u32) -> Result<()> {
    let c = CString::new(path.as_os_str().as_bytes())
        .map_err(|_| EngineError::invalid("NUL in path"))?;
    // SAFETY: valid C string.
    if unsafe { libc::chown(c.as_ptr(), uid, gid) } != 0 {
        return Err(EngineError::host(format!(
            "chown {}: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

pub fn chmod(path: &Path, mode: u32) -> Result<()> {
    std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode))
        .ctx(format!("chmod {}", path.display()))
}

/// Takes an exclusive, non-blocking `flock` on `path` (created 0600). The
/// lock lasts as long as the returned file is open, and the kernel drops it
/// if the process dies, so a crashed owner never wedges the store.
pub fn lock_exclusive(path: &Path) -> Result<File> {
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .mode(0o600)
        .open(path)
        .ctx(format!("open {}", path.display()))?;
    // SAFETY: flock on a descriptor we own.
    if unsafe { libc::flock(file.as_raw_fd(), libc::LOCK_EX | libc::LOCK_NB) } != 0 {
        return Err(EngineError::in_use(format!(
            "{} is locked: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(file)
}

pub fn fsync_path(path: &Path) -> Result<()> {
    File::open(path)
        .and_then(|f| f.sync_all())
        .ctx(format!("fsync {}", path.display()))
}

/// Punches holes over all-zero 64 KiB blocks so unchanged guest memory costs
/// neither disk nor page cache.
#[cfg(target_os = "linux")]
pub fn sparsify(path: &Path) -> Result<u64> {
    const BLOCK: usize = 64 << 10;
    let mut f = OpenOptions::new()
        .read(true)
        .write(true)
        .open(path)
        .ctx(format!("open {}", path.display()))?;
    let len = f.metadata()?.len();
    let mut buf = vec![0_u8; 4 << 20];
    let mut offset = 0_u64;
    let mut punched = 0_u64;
    while offset < len {
        let n = (len - offset).min(buf.len() as u64) as usize;
        f.seek(SeekFrom::Start(offset))?;
        f.read_exact(&mut buf[..n])?;
        for (i, block) in buf[..n].chunks(BLOCK).enumerate() {
            if block.iter().all(|&b| b == 0) {
                let at = offset + (i * BLOCK) as u64;
                // SAFETY: plain fallocate on our descriptor.
                let rc = unsafe {
                    libc::fallocate(
                        f.as_raw_fd(),
                        libc::FALLOC_FL_PUNCH_HOLE | libc::FALLOC_FL_KEEP_SIZE,
                        at as libc::off_t,
                        block.len() as libc::off_t,
                    )
                };
                if rc != 0 {
                    return Err(EngineError::host(format!(
                        "punch hole: {}",
                        std::io::Error::last_os_error()
                    )));
                }
                punched += block.len() as u64;
            }
        }
        offset += n as u64;
    }
    f.sync_all()?;
    Ok(punched)
}

/// Commits the filesystem holding `path` (syncfs(2)).
#[cfg(target_os = "linux")]
pub fn syncfs(path: &Path) -> Result<()> {
    let f = File::open(path).ctx(format!("open {}", path.display()))?;
    // SAFETY: plain syscall on our descriptor.
    if unsafe { libc::syncfs(f.as_raw_fd()) } != 0 {
        return Err(EngineError::host(format!(
            "syncfs {}: {}",
            path.display(),
            std::io::Error::last_os_error()
        )));
    }
    Ok(())
}

/// True when `[start, end)` contains no data extents.
fn is_hole(fd: i32, start: u64, end: u64) -> bool {
    // SAFETY: lseek on a valid descriptor.
    let data = unsafe { libc::lseek(fd, start as libc::off_t, libc::SEEK_DATA) };
    data < 0 || data as u64 >= end
}

/// `sha256-c4m` object digest of a file, hashing data chunks in parallel and
/// substituting the known zero digest for chunks that are entirely holes.
pub fn digest_file(path: &Path) -> Result<Object> {
    let len = std::fs::metadata(path)
        .ctx(format!("stat {}", path.display()))?
        .len();
    let count = len.div_ceil(CHUNK as u64) as usize;
    let threads = std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(4)
        .min(8);
    let zero_full = zero_chunk_digest(CHUNK);
    let mut digests = vec![[0_u8; 32]; count];
    let next = std::sync::atomic::AtomicUsize::new(0);
    let results = std::sync::Mutex::new(Vec::with_capacity(count));
    let failure = std::sync::Mutex::new(None);
    std::thread::scope(|scope| {
        for _ in 0..threads {
            scope.spawn(|| {
                let mut f = match File::open(path) {
                    Ok(f) => f,
                    Err(e) => {
                        *failure.lock().unwrap() = Some(e.to_string());
                        return;
                    }
                };
                let mut buf = vec![0_u8; CHUNK];
                loop {
                    let i = next.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
                    if i >= count {
                        break;
                    }
                    let start = (i * CHUNK) as u64;
                    let n = (len - start).min(CHUNK as u64) as usize;
                    let d: [u8; 32] =
                        if n == CHUNK && is_hole(f.as_raw_fd(), start, start + n as u64) {
                            zero_full
                        } else {
                            if let Err(e) = f
                                .seek(SeekFrom::Start(start))
                                .and_then(|_| f.read_exact(&mut buf[..n]))
                            {
                                *failure.lock().unwrap() = Some(e.to_string());
                                return;
                            }
                            Sha256::digest(&buf[..n]).into()
                        };
                    results.lock().unwrap().push((i, d));
                }
            });
        }
    });
    if let Some(e) = failure.into_inner().unwrap() {
        return Err(EngineError::host(format!("digest {}: {e}", path.display())));
    }
    for (i, d) in results.into_inner().unwrap() {
        digests[i] = d;
    }
    Ok(Object {
        digest: combine(&digests),
        size: len,
    })
}

/// Verifies a file against a manifest object.
pub fn verify_object(path: &Path, want: &Object, what: &str) -> Result<()> {
    let got = digest_file(path)?;
    if &got != want {
        return Err(EngineError::new(
            "DATA_UNAVAILABLE",
            format!("{what}: digest mismatch (quarantined)"),
        ));
    }
    Ok(())
}

/// Starts asynchronous writeback of a file's dirty pages without waiting.
#[cfg(target_os = "linux")]
pub fn start_writeback(path: &Path) {
    if let Ok(f) = File::open(path) {
        // SAFETY: plain syscall on our descriptor.
        unsafe { libc::sync_file_range(f.as_raw_fd(), 0, 0, libc::SYNC_FILE_RANGE_WRITE) };
    }
}

#[cfg(target_os = "macos")]
pub fn random_bytes<const N: usize>() -> [u8; N] {
    let mut out = [0_u8; N];
    for chunk in out.chunks_mut(256) {
        // SAFETY: getentropy fills at most 256 bytes of our buffer.
        while unsafe { libc::getentropy(chunk.as_mut_ptr().cast(), chunk.len()) } != 0 {}
    }
    out
}

#[cfg(target_os = "linux")]
pub fn random_bytes<const N: usize>() -> [u8; N] {
    let mut out = [0_u8; N];
    let mut filled = 0;
    while filled < N {
        // SAFETY: writes into our buffer.
        let n = unsafe { libc::getrandom(out[filled..].as_mut_ptr().cast(), N - filled, 0) };
        if n > 0 {
            filled += n as usize;
        }
    }
    out
}

pub fn now_ms() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

pub fn now_ns() -> u64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos() as u64)
        .unwrap_or(0)
}
