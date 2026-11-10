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
    #[cfg(test)]
    pub(crate) durable_writes: u64,
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
            #[cfg(test)]
            durable_writes: 0,
        })
    }
    /// Admission ACK/verdict is forbidden until this durable write succeeds.
    pub fn record(&mut self, source: &str, details: Value) -> io::Result<()> {
        self.record_batch(source, [details])
    }
    /// One storage barrier for a bounded set of connection counters/denials.
    /// Sequence and capacity checks happen before writing any part of the batch.
    pub fn record_batch(
        &mut self,
        source: &str,
        details: impl IntoIterator<Item = Value>,
    ) -> io::Result<()> {
        let mut next = self.sequence;
        let mut batch = Vec::new();
        for details in details {
            next = next
                .checked_add(1)
                .ok_or_else(|| io::Error::other("sequence exhausted"))?;
            let mut bytes = serde_json::to_vec(
                &json!({"format":"lamp.audit.demo/1", "sequence":next,
                "observed_at_ms":now_ms(), "attribution":self.attribution, "source":source,"details":details}),
            )?;
            bytes.push(b'\n');
            if bytes.len() > MAX_FRAME
                || batch.len() + bytes.len() > 4 * 1024 * 1024
                || self.used + (batch.len() + bytes.len()) as u64 > self.limit
            {
                return Err(io::Error::other(
                    "audit spool full or oversized batch/record",
                ));
            }
            batch.extend_from_slice(&bytes);
        }
        if batch.is_empty() {
            return Ok(());
        }
        self.file.write_all(&batch)?;
        self.file.sync_data()?;
        #[cfg(test)]
        {
            self.durable_writes += 1;
        }
        self.used += batch.len() as u64;
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

type Connector = Box<dyn FnMut() -> io::Result<UnixStream> + Send>;

/// How long a reconnecting collector keeps trying before it gives up (and
/// its tracer exits, killing the traced tree through `PTRACE_O_EXITKILL`).
pub const RECONNECT_DEADLINE: Duration = Duration::from_secs(5);

/// Connects the trusted guest collector to the host over AF_VSOCK (host CID 2).
/// Only guest root outside the task namespaces can do this; the task seccomp
/// filter denies AF_VSOCK.
#[cfg(target_os = "linux")]
pub fn vsock_connect(port: u32) -> io::Result<UnixStream> {
    unsafe {
        let fd = libc::socket(libc::AF_VSOCK, libc::SOCK_STREAM | libc::SOCK_CLOEXEC, 0);
        if fd < 0 {
            return Err(io::Error::last_os_error());
        }
        let mut addr: libc::sockaddr_vm = std::mem::zeroed();
        addr.svm_family = libc::AF_VSOCK as libc::sa_family_t;
        addr.svm_port = port;
        addr.svm_cid = libc::VMADDR_CID_HOST;
        let len = std::mem::size_of::<libc::sockaddr_vm>() as libc::socklen_t;
        if libc::connect(fd, (&raw const addr).cast(), len) != 0 {
            let e = io::Error::last_os_error();
            libc::close(fd);
            return Err(e);
        }
        stream(fd)
    }
}

pub struct Emitter {
    socket: UnixStream,
    sequence: u64,
    reconnect: Option<Connector>,
    /// Frames acknowledged across every segment of this collector.
    acknowledged: u64,
    segment: u64,
}
impl Emitter {
    pub fn new(socket: UnixStream) -> Self {
        Self {
            socket,
            sequence: 0,
            reconnect: None,
            acknowledged: 0,
            segment: 1,
        }
    }
    /// An emitter whose stream may be reset underneath it, as Firecracker does
    /// to every guest vsock connection when it snapshots a VM. On a transport
    /// error it opens a new stream (a new host segment, sequence restarting at
    /// 1), records a `segment_resume` linking it to the previous segment, and
    /// resends the unacknowledged frame. The frame may therefore appear in both
    /// segments (at least once); it is never silently dropped. If no stream is
    /// accepted within `RECONNECT_DEADLINE` the error is returned.
    pub fn reconnecting(mut connect: Connector) -> io::Result<Self> {
        let socket = connect()?;
        Ok(Self {
            socket,
            sequence: 0,
            reconnect: Some(connect),
            acknowledged: 0,
            segment: 1,
        })
    }
    fn exchange(&mut self, details: &Value) -> io::Result<()> {
        let sequence = self.sequence + 1;
        let bytes = serde_json::to_vec(&json!({"sequence":sequence,"details":details}))?;
        if bytes.len() > MAX_FRAME / 2 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "collector frame oversized",
            ));
        }
        self.socket.write_all(&(bytes.len() as u32).to_be_bytes())?;
        self.socket.write_all(&bytes)?;
        let mut ack = [0; 8];
        self.socket.read_exact(&mut ack)?;
        if u64::from_be_bytes(ack) != sequence {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "audit ACK sequence mismatch",
            ));
        }
        self.sequence = sequence;
        self.acknowledged += 1;
        Ok(())
    }
    pub fn send(&mut self, details: Value) -> io::Result<()> {
        let first = match self.exchange(&details) {
            Ok(()) => return Ok(()),
            Err(e) => e,
        };
        if first.kind() == io::ErrorKind::InvalidData || self.reconnect.is_none() {
            return Err(first);
        }
        let deadline = std::time::Instant::now() + RECONNECT_DEADLINE;
        let mut last = first;
        while std::time::Instant::now() < deadline {
            let connect = self.reconnect.as_mut().unwrap();
            match connect() {
                Ok(socket) => {
                    self.socket = socket;
                    self.sequence = 0;
                    self.segment += 1;
                    let resume = json!({"type":"segment_resume","segment":self.segment,
                        "previous_acknowledged":self.acknowledged,"resending_unacknowledged":true,
                        "reason":last.to_string()});
                    match self.exchange(&resume).and_then(|_| self.exchange(&details)) {
                        Ok(()) => return Ok(()),
                        Err(e) if e.kind() == io::ErrorKind::InvalidData => return Err(e),
                        Err(e) => last = e,
                    }
                }
                Err(e) => last = e,
            }
            std::thread::sleep(Duration::from_millis(5));
        }
        Err(last)
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
    fn spool(suffix: &str) -> (std::path::PathBuf, Spool) {
        let path =
            std::env::temp_dir().join(format!("lamp-audit-test-{}-{suffix}", std::process::id()));
        let attribution = Attribution {
            principal_id: "p".into(),
            host_id: "h".into(),
            task_id: "t".into(),
            sandbox_id: "s".into(),
            generation: 1,
            session_id: "s".into(),
            operation_id: "o".into(),
            policy_revision: 1,
        };
        let s = Spool::create(path.to_str().unwrap(), 4096, attribution).unwrap();
        (path, s)
    }
    #[test]
    fn full_spool_never_overwrites_acknowledged_history() {
        let (path, mut s) = spool("full");
        while s.record("test", json!({"type":"heartbeat"})).is_ok() {}
        let before = std::fs::read(&path).unwrap();
        assert!(before.len() <= 4096);
        assert!(s.record("test", json!({"type":"heartbeat"})).is_err());
        assert_eq!(before, std::fs::read(&path).unwrap());
        assert!(Spool::create(path.to_str().unwrap(), 4096, s.attribution.clone()).is_err());
        std::fs::remove_file(path).unwrap();
    }
    #[test]
    fn gap_is_not_acknowledged_or_spooled() {
        let (path, mut s) = spool("gap");
        let (mut a, b) = UnixStream::pair().unwrap();
        let t = std::thread::spawn(move || assert!(receive(b, &mut s).is_err()));
        let v =
            serde_json::to_vec(&json!({"sequence":2,"details":{"type":"process_start"}})).unwrap();
        a.write_all(&(v.len() as u32).to_be_bytes()).unwrap();
        a.write_all(&v).unwrap();
        let mut ack = [0; 8];
        assert!(a.read_exact(&mut ack).is_err());
        t.join().unwrap();
        assert_eq!(std::fs::metadata(&path).unwrap().len(), 0);
        std::fs::remove_file(path).unwrap();
    }
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
