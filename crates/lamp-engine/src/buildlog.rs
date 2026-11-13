//! Bounded build progress retained by definition digest, readable over worker authority.
use crate::{Result, error::IoContext};
use std::path::Path;
pub fn tail(log: &str) -> String {
    let mut start = log.len().saturating_sub(64 << 10);
    while !log.is_char_boundary(start) {
        start += 1;
    }
    log[start..].to_string()
}
pub fn write(store: &Path, definition: &str, log: &str) -> Result<()> {
    let dir = store.join("build-logs");
    std::fs::create_dir_all(&dir).ctx("create build logs")?;
    let id = crate::manifest::sha256_hex(definition.as_bytes());
    static NEXT: std::sync::atomic::AtomicU64 = std::sync::atomic::AtomicU64::new(0);
    let n = NEXT.fetch_add(1, std::sync::atomic::Ordering::Relaxed);
    let tmp = dir.join(format!("{id}.{}.{n}.tmp", std::process::id()));
    std::fs::write(&tmp, tail(log)).ctx("write build progress")?;
    std::fs::rename(tmp, dir.join(id)).ctx("publish build progress")
}
pub fn read(store: &Path, definition: &str) -> String {
    std::fs::read_to_string(
        store
            .join("build-logs")
            .join(crate::manifest::sha256_hex(definition.as_bytes())),
    )
    .unwrap_or_default()
}
