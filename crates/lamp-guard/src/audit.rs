//! Bounded, preallocated, host-owned demo spool. This is not the T23 signed ledger.
use crate::Attribution;
use serde_json::{Value, json};
use std::{
    fs::{File, OpenOptions},
    io::{self, Read, Write},
    os::{
        fd::{FromRawFd, RawFd},
        unix::{fs::OpenOptionsExt, net::UnixStream},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

pub const MAX_FRAME: usize = 64 * 1024;
pub fn now_ms() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_millis()
}

pub struct Spool {
    file: File,
    used: u64,
    limit: u64,
    sequence: u64,
    attribution: Attribution,
}
impl std::os::fd::AsRawFd for Spool {
    fn as_raw_fd(&self) -> RawFd {
        std::os::fd::AsRawFd::as_raw_fd(&self.file)
    }
}
impl Spool {
    pub fn create(path: &str, limit: u64, attribution: Attribution) -> io::Result<Self> {
        attribution.validate()?;
        if limit < 4096 || limit > i64::MAX as u64 {
            return Err(io::Error::other("invalid spool reservation"));
        }
        let file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .custom_flags(libc::O_NOFOLLOW | libc::O_CLOEXEC)
            .open(path)?;
        // Reserve physical capacity before task/network admission, but retain logical EOF.
        use std::os::fd::AsRawFd;
        if unsafe { libc::fallocate(file.as_raw_fd(), libc::FALLOC_FL_KEEP_SIZE, 0, limit as i64) }
            != 0
        {
            return Err(io::Error::last_os_error());
        }
        file.sync_all()?;
        if let Some(parent) = std::path::Path::new(path).parent() {
            File::open(if parent.as_os_str().is_empty() {
                std::path::Path::new(".")
            } else {
                parent
            })?
            .sync_all()?;
        }
        Ok(Self {
            file,
            used: 0,
            limit,
            sequence: 0,
            attribution,
        })
    }
    /// ACK/verdict is forbidden until this durable write succeeds.
    pub fn record(&mut self, source: &str, details: Value) -> io::Result<()> {
        let next = self
            .sequence
            .checked_add(1)
            .ok_or_else(|| io::Error::other("sequence exhausted"))?;
        let mut bytes = serde_json::to_vec(&json!({"format":"lamp.audit.demo/1", "sequence":next,
            "observed_at_ms":now_ms(), "attribution":self.attribution, "source":source,"details":details}))?;
        bytes.push(b'\n');
        if bytes.len() > MAX_FRAME || self.used + bytes.len() as u64 > self.limit {
            return Err(io::Error::other("audit spool full or oversized record"));
        }
        self.file.write_all(&bytes)?;
        self.file.sync_data()?;
        self.used += bytes.len() as u64;
        self.sequence = next;
        Ok(())
    }
}

/// Supervisor passes a connected SOCK_STREAM endpoint; never connect a pathname
/// supplied by a task. Works with AF_UNIX for tests and AF_VSOCK in the guest.
/// # Safety
/// `fd` must be a valid, exclusively owned connected stream socket from the trusted
/// supervisor. Ownership is transferred; do not use or close it elsewhere afterward.
pub unsafe fn stream(fd: RawFd) -> io::Result<UnixStream> {
    if fd < 3 {
        return Err(io::Error::other(
            "audit needs a dedicated inherited descriptor >=3",
        ));
    }
    let s = unsafe { UnixStream::from_raw_fd(fd) };
    s.set_read_timeout(Some(Duration::from_millis(500)))?;
    s.set_write_timeout(Some(Duration::from_millis(500)))?;
    let flags = unsafe { libc::fcntl(fd, libc::F_GETFD) };
    if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFD, flags | libc::FD_CLOEXEC) } < 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(s)
}

pub struct Emitter {
    socket: UnixStream,
    sequence: u64,
}
impl Emitter {
    pub fn new(socket: UnixStream) -> Self {
        Self {
            socket,
            sequence: 0,
        }
    }
    pub fn send(&mut self, details: Value) -> io::Result<()> {
        self.sequence += 1;
        let bytes = serde_json::to_vec(&json!({"sequence":self.sequence,"details":details}))?;
        if bytes.len() > MAX_FRAME / 2 {
            return Err(io::Error::other("collector frame oversized"));
        }
        self.socket.write_all(&(bytes.len() as u32).to_be_bytes())?;
        self.socket.write_all(&bytes)?;
        let mut ack = [0; 8];
        self.socket.read_exact(&mut ack)?;
        if u64::from_be_bytes(ack) != self.sequence {
            return Err(io::Error::other("audit ACK sequence mismatch"));
        }
        Ok(())
    }
}

pub fn receive(mut socket: UnixStream, spool: &mut Spool) -> io::Result<()> {
    let mut expected = 1_u64;
    loop {
        let mut length = [0; 4];
        socket.read_exact(&mut length)?;
        let length = u32::from_be_bytes(length) as usize;
        if length == 0 || length > MAX_FRAME / 2 {
            return Err(io::Error::other("invalid collector frame length"));
        }
        let mut bytes = vec![0; length];
        socket.read_exact(&mut bytes)?;
        let v: Value = serde_json::from_slice(&bytes)?;
        if v["sequence"].as_u64() != Some(expected) || !v["details"].is_object() {
            return Err(io::Error::other(
                "collector sequence gap or malformed event",
            ));
        }
        let complete = v["details"]["type"] == "collector_complete";
        spool.record("protected_process_collector", v)?;
        socket.write_all(&expected.to_be_bytes())?;
        if complete {
            return Ok(());
        }
        expected = expected
            .checked_add(1)
            .ok_or_else(|| io::Error::other("sequence exhausted"))?;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn ack_must_match() {
        let (a, mut b) = UnixStream::pair().unwrap();
        let t = std::thread::spawn(move || {
            let mut len = [0; 4];
            b.read_exact(&mut len).unwrap();
            let mut v = vec![0; u32::from_be_bytes(len) as usize];
            b.read_exact(&mut v).unwrap();
            b.write_all(&42_u64.to_be_bytes()).unwrap();
        });
        assert!(Emitter::new(a).send(json!({"type":"heartbeat"})).is_err());
        t.join().unwrap();
    }
}
