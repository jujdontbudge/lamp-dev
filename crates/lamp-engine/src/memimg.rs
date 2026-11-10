//! Durable memory objects: a full RAM image stored as the pages that differ
//! from a base image (the template's memory, or the materialized image of
//! the durable capture the sandbox was restored from).
//!
//! The comparison is by content, page by page, over the whole configured
//! RAM: nothing here trusts dirty tracking. The tmpfs capture that a fork
//! restores from is still the full Firecracker image; this is only what
//! reaches stable storage.
//!
//! Object layout (`lamp.memdelta/0`, unfrozen until T14):
//!
//! ```text
//! [0, 4096)            header: magic "LAMPMEM0", u32 version 0, u32 page size,
//!                      u64 image bytes, u64 changed pages, u64 bitmap bytes
//! [4096, 4096+B)       bitmap, one bit per image page (LSB first), B a
//!                      multiple of 4096
//! [4096+B, ...)        the changed pages, ascending
//! ```
use crate::error::{EngineError, IoContext, Result};
use crate::manifest::{CHUNK, Object, combine};
use sha2::{Digest, Sha256};
use std::os::unix::fs::OpenOptionsExt;
use std::fs::{File, OpenOptions};
use std::os::fd::AsRawFd;
use std::os::unix::fs::FileExt;
use std::path::Path;
use std::sync::atomic::{AtomicUsize, Ordering};

pub const PAGE: usize = 4096;
/// Pages compared per work unit (2 MiB, one huge page).
const UNIT_PAGES: usize = 512;
const UNIT: usize = UNIT_PAGES * PAGE;
const MAGIC: &[u8; 8] = b"LAMPMEM0";

#[derive(Debug, Clone)]
pub struct Encoded {
    pub image_bytes: u64,
    pub changed_pages: u64,
    pub object_bytes: u64,
    /// `sha256-c4m` digest of the object, hashed from memory while it was
    /// written (the bytes are the ones handed to the kernel).
    pub digest: Object,
}

#[derive(Debug, Clone, Copy)]
pub struct Header {
    pub image_bytes: u64,
    pub changed_pages: u64,
    pub bitmap_bytes: u64,
}

fn threads() -> usize {
    std::thread::available_parallelism()
        .map(|n| n.get())
        .unwrap_or(4)
        .clamp(2, 8)
}

/// Read-only mapping of a whole file.
struct Map {
    ptr: *mut libc::c_void,
    len: usize,
}
unsafe impl Send for Map {}
unsafe impl Sync for Map {}

impl Map {
    fn new(file: &File, len: usize) -> Result<Map> {
        if len == 0 {
            return Err(EngineError::invalid("empty memory image"));
        }
        // SAFETY: read-only shared mapping of a file we keep open; the
        // image is sealed (0400, no writer) while it is mapped.
        let ptr = unsafe {
            libc::mmap(
                std::ptr::null_mut(),
                len,
                libc::PROT_READ,
                libc::MAP_SHARED,
                file.as_raw_fd(),
                0,
            )
        };
        if ptr == libc::MAP_FAILED {
            return Err(EngineError::host(format!(
                "mmap image: {}",
                std::io::Error::last_os_error()
            )));
        }
        Ok(Map { ptr, len })
    }
    fn bytes(&self) -> &[u8] {
        // SAFETY: valid mapping of len bytes for the lifetime of self.
        unsafe { std::slice::from_raw_parts(self.ptr.cast::<u8>(), self.len) }
    }
}

impl Drop for Map {
    fn drop(&mut self) {
        // SAFETY: unmapping our own mapping.
        unsafe { libc::munmap(self.ptr, self.len) };
    }
}

/// True when `[start, end)` of `file` holds no data extents.
fn hole(file: &File, start: u64, end: u64) -> bool {
    // SAFETY: lseek on a valid descriptor.
    let data = unsafe { libc::lseek(file.as_raw_fd(), start as libc::off_t, libc::SEEK_DATA) };
    data < 0 || data as u64 >= end
}

fn bitmap_bytes(pages: usize) -> usize {
    pages.div_ceil(8).div_ceil(PAGE) * PAGE
}

fn header_bytes(h: &Header) -> Vec<u8> {
    let mut b = vec![0_u8; PAGE];
    b[0..8].copy_from_slice(MAGIC);
    b[8..12].copy_from_slice(&0_u32.to_le_bytes());
    b[12..16].copy_from_slice(&(PAGE as u32).to_le_bytes());
    b[16..24].copy_from_slice(&h.image_bytes.to_le_bytes());
    b[24..32].copy_from_slice(&h.changed_pages.to_le_bytes());
    b[32..40].copy_from_slice(&h.bitmap_bytes.to_le_bytes());
    b
}

pub fn read_header(file: &File) -> Result<Header> {
    let mut b = vec![0_u8; PAGE];
    file.read_exact_at(&mut b, 0).ctx("read memory object header")?;
    if &b[0..8] != MAGIC
        || u32::from_le_bytes(b[8..12].try_into().unwrap()) != 0
        || u32::from_le_bytes(b[12..16].try_into().unwrap()) != PAGE as u32
    {
        return Err(EngineError::new(
            "DATA_UNAVAILABLE",
            "memory object header is not lamp.memdelta/0",
        ));
    }
    let h = Header {
        image_bytes: u64::from_le_bytes(b[16..24].try_into().unwrap()),
        changed_pages: u64::from_le_bytes(b[24..32].try_into().unwrap()),
        bitmap_bytes: u64::from_le_bytes(b[32..40].try_into().unwrap()),
    };
    let pages = (h.image_bytes as usize).div_ceil(PAGE);
    if !h.image_bytes.is_multiple_of(PAGE as u64)
        || h.bitmap_bytes != bitmap_bytes(pages) as u64
        || h.changed_pages > pages as u64
    {
        return Err(EngineError::new(
            "DATA_UNAVAILABLE",
            "memory object header is inconsistent",
        ));
    }
    Ok(h)
}

/// Writes the pages of `image` that differ from `base` into `out` (created,
/// mode 0600) and fdatasyncs it unless `sync` is false. `base` may be
/// sparse and must be exactly as long as `image`.
pub fn encode(image: &Path, base: &Path, out: &Path, sync: bool) -> Result<Encoded> {
    let img = File::open(image).ctx("open capture image")?;
    let len = img.metadata()?.len();
    if !len.is_multiple_of(PAGE as u64) {
        return Err(EngineError::invalid("memory image is not page aligned"));
    }
    let base_file = File::open(base).ctx("open base image")?;
    if base_file.metadata()?.len() != len {
        return Err(EngineError::new(
            "ENGINE_UNQUALIFIED",
            "base image size differs from the capture",
        ));
    }
    let map = Map::new(&img, len as usize)?;
    let pages = len as usize / PAGE;
    let units = pages.div_ceil(UNIT_PAGES);
    let mut bitmap = vec![0_u8; bitmap_bytes(pages)];
    let mut counts = vec![0_usize; units];

    // Pass 1: compare every page with the base, unit by unit.
    {
        let next = AtomicUsize::new(0);
        let results = std::sync::Mutex::new(Vec::with_capacity(units));
        let failure = std::sync::Mutex::new(None::<String>);
        std::thread::scope(|s| {
            for _ in 0..threads() {
                s.spawn(|| {
                    let mut buf = vec![0_u8; UNIT];
                    let zero = [0_u8; PAGE];
                    let mine = &mut Vec::new();
                    loop {
                        let u = next.fetch_add(1, Ordering::Relaxed);
                        if u >= units {
                            break;
                        }
                        let start = u * UNIT;
                        let end = (start + UNIT).min(len as usize);
                        let n = end - start;
                        let base_hole = hole(&base_file, start as u64, end as u64);
                        if !base_hole
                            && let Err(e) = base_file.read_exact_at(&mut buf[..n], start as u64)
                        {
                            *failure.lock().unwrap() = Some(e.to_string());
                            return;
                        }
                        let src = &map.bytes()[start..end];
                        let mut bits = [0_u8; UNIT_PAGES / 8];
                        let mut count = 0;
                        for p in 0..n / PAGE {
                            let page = &src[p * PAGE..(p + 1) * PAGE];
                            let reference = if base_hole {
                                &zero[..]
                            } else {
                                &buf[p * PAGE..(p + 1) * PAGE]
                            };
                            if page != reference {
                                bits[p / 8] |= 1 << (p % 8);
                                count += 1;
                            }
                        }
                        mine.push((u, bits, count));
                    }
                    results.lock().unwrap().append(mine);
                });
            }
        });
        if let Some(e) = failure.into_inner().unwrap() {
            return Err(EngineError::host(format!("read base image: {e}")));
        }
        for (u, bits, count) in results.into_inner().unwrap() {
            let at = u * (UNIT_PAGES / 8);
            let take = (UNIT_PAGES / 8).min(pages.div_ceil(8) - at);
            bitmap[at..at + take].copy_from_slice(&bits[..take]);
            counts[u] = count;
        }
    }
    let changed: usize = counts.iter().sum();
    let header = Header {
        image_bytes: len,
        changed_pages: changed as u64,
        bitmap_bytes: bitmap.len() as u64,
    };
    let data_at = (PAGE + bitmap.len()) as u64;
    let total = data_at + (changed * PAGE) as u64;
    let out_file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode_0600()
        .open(out)
        .ctx("create memory object")?;
    out_file.set_len(total).ctx("size memory object")?;
    out_file.write_all_at(&header_bytes(&header), 0)?;
    out_file.write_all_at(&bitmap, PAGE as u64)?;

    // Pass 2: write each unit's changed pages, as runs, at its offset,
    // and hash the object from memory at the same time. The runs come
    // straight from the page-aligned mapping, so they go to the device
    // with O_DIRECT (buffered writeback here is several times slower);
    // a filesystem without O_DIRECT gets buffered writes.
    let mut offsets = vec![0_u64; units];
    let mut at = data_at;
    for u in 0..units {
        offsets[u] = at;
        at += (counts[u] * PAGE) as u64;
    }
    let direct = OpenOptions::new()
        .write(true)
        .custom_flags(libc::O_DIRECT)
        .open(out)
        .ok();
    let changed_list: Vec<u32> = (0..pages)
        .filter(|&p| bitmap[p / 8] & (1 << (p % 8)) != 0)
        .map(|p| p as u32)
        .collect();
    let head = header_bytes(&header);
    let meta_pages = 1 + bitmap.len() / PAGE;
    let object_page = |k: usize| -> &[u8] {
        if k == 0 {
            &head[..]
        } else if k < meta_pages {
            &bitmap[(k - 1) * PAGE..k * PAGE]
        } else {
            let p = changed_list[k - meta_pages] as usize;
            &map.bytes()[p * PAGE..(p + 1) * PAGE]
        }
    };
    let object_pages = meta_pages + changed;
    let chunk_pages = CHUNK / PAGE;
    let chunks = object_pages.div_ceil(chunk_pages);
    let mut digests = vec![[0_u8; 32]; chunks];
    {
        let next = AtomicUsize::new(0);
        let next_chunk = AtomicUsize::new(0);
        let failure = std::sync::Mutex::new(None::<String>);
        let hashed = std::sync::Mutex::new(Vec::with_capacity(chunks));
        std::thread::scope(|s| {
            for _ in 0..threads().div_ceil(2) {
                s.spawn(|| {
                    let mine = &mut Vec::new();
                    loop {
                        let c = next_chunk.fetch_add(1, Ordering::Relaxed);
                        if c >= chunks {
                            break;
                        }
                        let mut h = Sha256::new();
                        let last = ((c + 1) * chunk_pages).min(object_pages);
                        for k in c * chunk_pages..last {
                            h.update(object_page(k));
                        }
                        mine.push((c, <[u8; 32]>::from(h.finalize())));
                    }
                    hashed.lock().unwrap().append(mine);
                });
            }
            for _ in 0..threads() {
                s.spawn(|| {
                    let mut written = 0_u64;
                    loop {
                        let u = next.fetch_add(1, Ordering::Relaxed);
                        if u >= units {
                            break;
                        }
                        if counts[u] == 0 {
                            continue;
                        }
                        let first = u * UNIT_PAGES;
                        let last = (first + UNIT_PAGES).min(pages);
                        let mut dst = offsets[u];
                        let mut p = first;
                        while p < last {
                            if bitmap[p / 8] & (1 << (p % 8)) == 0 {
                                p += 1;
                                continue;
                            }
                            let run = p;
                            while p < last && bitmap[p / 8] & (1 << (p % 8)) != 0 {
                                p += 1;
                            }
                            let bytes = &map.bytes()[run * PAGE..p * PAGE];
                            let r = match &direct {
                                Some(d) => d.write_all_at(bytes, dst).or_else(|e| {
                                    if e.raw_os_error() == Some(libc::EINVAL) {
                                        out_file.write_all_at(bytes, dst)
                                    } else {
                                        Err(e)
                                    }
                                }),
                                None => out_file.write_all_at(bytes, dst),
                            };
                            if let Err(e) = r {
                                *failure.lock().unwrap() = Some(e.to_string());
                                return;
                            }
                            dst += bytes.len() as u64;
                            written += bytes.len() as u64;
                        }
                        // Start writeback early so the final sync overlaps.
                        if sync && direct.is_none() && written >= 32 << 20 {
                            // SAFETY: plain syscall on our descriptor.
                            unsafe {
                                libc::sync_file_range(
                                    out_file.as_raw_fd(),
                                    0,
                                    0,
                                    libc::SYNC_FILE_RANGE_WRITE,
                                )
                            };
                            written = 0;
                        }
                    }
                });
            }
        });
        if let Some(e) = failure.into_inner().unwrap() {
            return Err(EngineError::host(format!("write memory object: {e}")));
        }
        for (c, d) in hashed.into_inner().unwrap() {
            digests[c] = d;
        }
    }
    if sync {
        out_file.sync_data().ctx("sync memory object")?;
    }
    Ok(Encoded {
        image_bytes: len,
        changed_pages: changed as u64,
        object_bytes: total,
        digest: Object {
            digest: combine(&digests),
            size: total,
        },
    })
}

/// Writes the full image `base` + `delta` into `out` (created, mode 0400
/// once complete). Holes of the base stay holes. The delta must already
/// have been verified against its manifest digest.
pub fn materialize(delta: &Path, base: &Path, out: &Path) -> Result<()> {
    let d = File::open(delta).ctx("open memory object")?;
    let h = read_header(&d)?;
    let b = File::open(base).ctx("open base image")?;
    if b.metadata()?.len() != h.image_bytes {
        return Err(EngineError::new(
            "DATA_UNAVAILABLE",
            "memory object does not fit its base image",
        ));
    }
    let pages = (h.image_bytes as usize) / PAGE;
    let mut bitmap = vec![0_u8; h.bitmap_bytes as usize];
    d.read_exact_at(&mut bitmap, PAGE as u64)?;
    let set: usize = bitmap[..pages.div_ceil(8)]
        .iter()
        .map(|b| b.count_ones() as usize)
        .sum();
    if set as u64 != h.changed_pages
        || d.metadata()?.len() != PAGE as u64 + h.bitmap_bytes + h.changed_pages * PAGE as u64
    {
        return Err(EngineError::new(
            "DATA_UNAVAILABLE",
            "memory object bitmap does not match its pages",
        ));
    }
    let o = OpenOptions::new()
        .read(true)
        .write(true)
        .create_new(true)
        .mode_0600()
        .open(out)
        .ctx("create restored image")?;
    o.set_len(h.image_bytes)?;
    let units = pages.div_ceil(UNIT_PAGES);
    // Offset of each unit's first changed page in the object.
    let mut offsets = vec![0_u64; units];
    let mut at = PAGE as u64 + h.bitmap_bytes;
    for (u, off) in offsets.iter_mut().enumerate() {
        *off = at;
        let first = u * UNIT_PAGES;
        let last = (first + UNIT_PAGES).min(pages);
        at += (first..last)
            .filter(|p| bitmap[p / 8] & (1 << (p % 8)) != 0)
            .count() as u64
            * PAGE as u64;
    }
    let next = AtomicUsize::new(0);
    let failure = std::sync::Mutex::new(None::<String>);
    std::thread::scope(|s| {
        for _ in 0..threads() {
            s.spawn(|| {
                let mut buf = vec![0_u8; UNIT];
                let mut page = vec![0_u8; PAGE];
                loop {
                    let u = next.fetch_add(1, Ordering::Relaxed);
                    if u >= units {
                        break;
                    }
                    let start = u * UNIT;
                    let end = (start + UNIT).min(h.image_bytes as usize);
                    let n = end - start;
                    let first = u * UNIT_PAGES;
                    let any_changed =
                        (first..first + n / PAGE).any(|p| bitmap[p / 8] & (1 << (p % 8)) != 0);
                    let base_hole = hole(&b, start as u64, end as u64);
                    if base_hole && !any_changed {
                        continue;
                    }
                    let r = (|| -> std::io::Result<()> {
                        if base_hole {
                            buf[..n].fill(0);
                        } else {
                            b.read_exact_at(&mut buf[..n], start as u64)?;
                        }
                        let mut src = offsets[u];
                        for p in 0..n / PAGE {
                            if bitmap[(first + p) / 8] & (1 << ((first + p) % 8)) != 0 {
                                d.read_exact_at(&mut page, src)?;
                                buf[p * PAGE..(p + 1) * PAGE].copy_from_slice(&page);
                                src += PAGE as u64;
                            }
                        }
                        // Keep all-zero ranges as holes.
                        if buf[..n].iter().any(|&x| x != 0) {
                            o.write_all_at(&buf[..n], start as u64)?;
                        }
                        Ok(())
                    })();
                    if let Err(e) = r {
                        *failure.lock().unwrap() = Some(e.to_string());
                        return;
                    }
                }
            });
        }
    });
    if let Some(e) = failure.into_inner().unwrap() {
        let _ = std::fs::remove_file(out);
        return Err(EngineError::host(format!("materialize image: {e}")));
    }
    Ok(())
}

trait Mode0600 {
    fn mode_0600(&mut self) -> &mut Self;
}
impl Mode0600 for OpenOptions {
    fn mode_0600(&mut self) -> &mut Self {
        use std::os::unix::fs::OpenOptionsExt;
        self.mode(0o600)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tmp(tag: &str) -> std::path::PathBuf {
        let d = std::env::temp_dir().join(format!("lamp-memimg-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_dir_all(&d);
        std::fs::create_dir_all(&d).unwrap();
        d
    }

    #[test]
    fn delta_round_trips_against_a_sparse_base() {
        let d = tmp("rt");
        let len = 3 * UNIT + 5 * PAGE;
        let mut base = vec![0_u8; len];
        let mut image = vec![0_u8; len];
        // base data in unit 1; image changes pages in units 0, 1 and the tail,
        // including a page set back to zero where the base had data.
        for (i, b) in base[UNIT..UNIT + 8 * PAGE].iter_mut().enumerate() {
            *b = (i % 251) as u8 + 1;
        }
        image.copy_from_slice(&base);
        image[3 * PAGE] = 7;
        image[UNIT + 2 * PAGE..UNIT + 3 * PAGE].fill(0);
        image[UNIT + 9 * PAGE + 11] = 9;
        image[len - 1] = 1;
        let bp = d.join("base");
        let ip = d.join("image");
        {
            let f = File::create(&bp).unwrap();
            f.set_len(len as u64).unwrap();
            f.write_all_at(&base[UNIT..UNIT + 8 * PAGE], UNIT as u64)
                .unwrap();
        }
        std::fs::write(&ip, &image).unwrap();
        let enc = encode(&ip, &bp, &d.join("delta"), true).unwrap();
        assert_eq!(enc.changed_pages, 4);
        assert_eq!(enc.digest, crate::fsutil::digest_file(&d.join("delta")).unwrap());
        materialize(&d.join("delta"), &bp, &d.join("out")).unwrap();
        assert_eq!(std::fs::read(d.join("out")).unwrap(), image);
        // A tampered bitmap is refused before any page is used.
        let f = OpenOptions::new()
            .write(true)
            .open(d.join("delta"))
            .unwrap();
        f.write_all_at(&[0xff], PAGE as u64 + 100).unwrap();
        assert!(materialize(&d.join("delta"), &bp, &d.join("out2")).is_err());
        std::fs::remove_dir_all(d).unwrap();
    }

    #[test]
    fn in_memory_digest_matches_the_file_across_chunks() {
        let d = tmp("dg");
        let len = 6 * UNIT;
        let image: Vec<u8> = (0..len).map(|i| (i / PAGE % 253) as u8 + 1).collect();
        let bp = d.join("base");
        File::create(&bp).unwrap().set_len(len as u64).unwrap();
        let ip = d.join("image");
        std::fs::write(&ip, &image).unwrap();
        let enc = encode(&ip, &bp, &d.join("delta"), true).unwrap();
        assert_eq!(enc.changed_pages as usize, len / PAGE);
        assert!(enc.object_bytes as usize > CHUNK);
        assert_eq!(enc.digest, crate::fsutil::digest_file(&d.join("delta")).unwrap());
        materialize(&d.join("delta"), &bp, &d.join("out")).unwrap();
        assert_eq!(std::fs::read(d.join("out")).unwrap(), image);
        std::fs::remove_dir_all(d).unwrap();
    }
}
