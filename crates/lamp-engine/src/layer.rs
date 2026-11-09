//! Guest memory layers. A sandbox's memory is a stack: the bottom layer is a
//! complete image (a template variant on the store), each layer above holds
//! only pages dirtied since the layer below was sealed (sparse files on a
//! RAM-backed tmpfs). A page's content is the topmost layer that has it.
//! Layers are immutable once sealed and shared by reference count.
use crate::error::{EngineError, IoContext, Result};
use std::fs::File;
use std::io::{Read, Seek, SeekFrom, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::FileExt;
use std::path::{Path, PathBuf};
use std::sync::{Arc, OnceLock};

pub const PAGE: u64 = 4096;

#[derive(Debug)]
pub struct Layer {
    pub name: String,
    pub file: File,
    pub len: u64,
    /// Bottom layers are complete; holes there read as zero pages.
    pub complete: bool,
    /// Unlinked when the last reference goes away.
    pub owned_path: Option<PathBuf>,
    bitmap: OnceLock<Vec<u64>>,
}

impl Drop for Layer {
    fn drop(&mut self) {
        if let Some(p) = &self.owned_path {
            let _ = std::fs::remove_file(p);
        }
    }
}

pub type Stack = Vec<Arc<Layer>>;

impl Layer {
    pub fn open(path: &Path, complete: bool, owned: bool) -> Result<Arc<Layer>> {
        let file = File::open(path).ctx(format!("open layer {}", path.display()))?;
        let len = file.metadata()?.len();
        Ok(Arc::new(Layer {
            name: path
                .file_name()
                .map(|n| n.to_string_lossy().into_owned())
                .unwrap_or_default(),
            file,
            len,
            complete,
            owned_path: owned.then(|| path.to_path_buf()),
            bitmap: OnceLock::new(),
        }))
    }

    /// Pages present in this layer (data extents), cached after first use.
    pub fn bitmap(&self) -> &[u64] {
        self.bitmap.get_or_init(|| {
            let pages = self.len.div_ceil(PAGE) as usize;
            let mut bits = vec![0_u64; pages.div_ceil(64)];
            let fd = self.file.as_raw_fd();
            let mut off = 0_i64;
            loop {
                // SAFETY: lseek on our descriptor.
                let data = unsafe { libc::lseek(fd, off, libc::SEEK_DATA) };
                if data < 0 || data as u64 >= self.len {
                    break;
                }
                // SAFETY: as above.
                let hole = unsafe { libc::lseek(fd, data, libc::SEEK_HOLE) };
                let end = if hole < 0 {
                    self.len
                } else {
                    (hole as u64).min(self.len)
                };
                for p in (data as u64 / PAGE)..end.div_ceil(PAGE) {
                    bits[(p / 64) as usize] |= 1 << (p % 64);
                }
                off = end as i64;
            }
            bits
        })
    }

    pub fn page_count(&self) -> u64 {
        self.bitmap()
            .iter()
            .map(|w| u64::from(w.count_ones()))
            .sum()
    }
}

/// True when every page of `lower` is also in one of `upper`.
pub fn covered(lower: &Layer, upper: &[Arc<Layer>]) -> bool {
    let lb = lower.bitmap();
    lb.iter().enumerate().all(|(i, w)| {
        let mut cover = 0_u64;
        for u in upper {
            cover |= u.bitmap().get(i).copied().unwrap_or(0);
        }
        w & !cover == 0
    })
}

/// Drops diff layers fully hidden by layers above them and merges the rest
/// into one layer once more than `max_diffs` remain. Returns the new stack,
/// or None when nothing changed. The bottom layer is never touched.
pub fn compact(stack: &Stack, dir: &Path, name: &str, max_diffs: usize) -> Result<Option<Stack>> {
    if stack.len() < 3 {
        return Ok(None);
    }
    let mut kept: Stack = vec![stack[0].clone()];
    let diffs = &stack[1..];
    for (i, d) in diffs.iter().enumerate() {
        if i + 1 == diffs.len() || !covered(d, &diffs[i + 1..]) {
            kept.push(d.clone());
        }
    }
    if kept.len() - 1 <= max_diffs {
        return Ok((kept.len() != stack.len()).then_some(kept));
    }
    // Merge every diff except the newest into one layer, topmost page wins.
    let to_merge = &kept[1..kept.len() - 1];
    let len = to_merge.iter().map(|l| l.len).max().unwrap_or(0);
    let path = dir.join(name);
    let mut out = File::create(&path).ctx("create merged layer")?;
    out.set_len(len)?;
    let pages = len.div_ceil(PAGE) as usize;
    let mut buf = vec![0_u8; PAGE as usize];
    for p in 0..pages {
        let (w, b) = (p / 64, p % 64);
        if let Some(src) = to_merge
            .iter()
            .rev()
            .find(|l| l.bitmap().get(w).is_some_and(|x| x >> b & 1 == 1))
        {
            src.file.read_exact_at(&mut buf, p as u64 * PAGE)?;
            out.write_all_at(&buf, p as u64 * PAGE)?;
        }
    }
    out.flush()?;
    drop(out);
    let merged = Layer::open(&path, false, true)?;
    let mut result: Stack = vec![kept[0].clone(), merged];
    result.push(kept[kept.len() - 1].clone());
    Ok(Some(result))
}

/// Reads one page through the stack (tests and verification).
pub fn read_page(stack: &Stack, offset: u64, buf: &mut [u8]) -> Result<()> {
    let p = offset / PAGE;
    for l in stack.iter().rev() {
        let has = l.complete
            || l.bitmap()
                .get((p / 64) as usize)
                .is_some_and(|w| w >> (p % 64) & 1 == 1);
        if has {
            let mut f = &l.file;
            f.seek(SeekFrom::Start(offset))?;
            let n = f.read(buf)?;
            buf[n..].fill(0);
            return Ok(());
        }
    }
    Err(EngineError::host("page not in any layer"))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn layer(dir: &Path, name: &str, pages: &[(u64, u8)], len: u64, complete: bool) -> Arc<Layer> {
        let path = dir.join(name);
        let f = File::create(&path).unwrap();
        f.set_len(len).unwrap();
        for (p, v) in pages {
            f.write_all_at(&vec![*v; PAGE as usize], p * PAGE).unwrap();
        }
        Layer::open(&path, complete, true).unwrap()
    }

    #[test]
    fn stack_lookup_prune_and_merge() {
        let dir = std::env::temp_dir().join(format!("lamp-layer-{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let len = 4096 * PAGE;
        let base = layer(&dir, "base", &[(0, 1), (1, 1), (3000, 1)], len, true);
        let d1 = layer(&dir, "d1", &[(1, 2), (2, 2)], len, false);
        let d2 = layer(&dir, "d2", &[(2, 3)], len, false);
        let d3 = layer(&dir, "d3", &[(1, 4), (2, 4)], len, false);
        let d4 = layer(&dir, "d4", &[(5, 5)], len, false);
        let stack: Stack = vec![base, d1, d2, d3.clone(), d4.clone()];
        let mut buf = vec![0_u8; PAGE as usize];
        let expect = |s: &Stack, page: u64, v: u8| {
            let mut b = vec![0_u8; PAGE as usize];
            read_page(s, page * PAGE, &mut b).unwrap();
            assert!(b.iter().all(|x| *x == v), "page {page}");
        };
        for (p, v) in [(0, 1), (1, 4), (2, 4), (3, 0), (5, 5), (3000, 1)] {
            expect(&stack, p, v);
        }
        // d1 and d2 are hidden by d3: pruned, nothing to merge.
        let pruned = compact(&stack, &dir, "m1", 4).unwrap().unwrap();
        assert_eq!(pruned.len(), 3);
        for (p, v) in [(0, 1), (1, 4), (2, 4), (3, 0), (5, 5)] {
            expect(&pruned, p, v);
        }
        // Force a merge of everything below the newest diff.
        let merged = compact(
            &vec![
                stack[0].clone(),
                d4.clone(),
                d3,
                layer(&dir, "d5", &[(9, 9)], len, false),
            ],
            &dir,
            "m2",
            1,
        )
        .unwrap()
        .unwrap();
        assert_eq!(merged.len(), 3);
        for (p, v) in [(0, 1), (1, 4), (2, 4), (5, 5), (9, 9), (3000, 1)] {
            expect(&merged, p, v);
        }
        read_page(&merged, 7 * PAGE, &mut buf).unwrap();
        drop((stack, pruned, merged, d4));
        std::fs::remove_dir_all(dir).unwrap();
    }
}
