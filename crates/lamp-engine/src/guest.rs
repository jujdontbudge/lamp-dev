//! Host side of the private guest channel, through Firecracker's vsock UDS.
//!
//! Failures are split by whether the request may have reached the guest:
//! before the request frame is fully written nothing can have happened
//! (`HOST_UNAVAILABLE`, retryable); once it was written, a lost or broken
//! reply leaves the effect uncertain (`OUTCOME_UNKNOWN`, never retried).
use crate::error::{EngineError, Result};
use lamp_init::{
    Empty, ExecDone, GUEST_PORT, GuestRequest, GuestResponse, Wait, read_frame, write_frame,
};
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::time::{Duration, Instant};

/// A connection whose request has been delivered; more replies may follow.
pub struct Delivered {
    reader: BufReader<UnixStream>,
}

impl Delivered {
    /// Reads the next reply; `None` timeout waits forever.
    pub fn next(&mut self, timeout: Option<Duration>) -> Result<GuestResponse> {
        self.reader
            .get_ref()
            .set_read_timeout(timeout.map(|t| t.max(Duration::from_millis(1))))
            .map_err(|e| uncertain(&format!("guest reply: {e}")))?;
        read_frame(&mut self.reader).map_err(|e| uncertain(&format!("guest reply: {e}")))
    }
}

fn uncertain(message: &str) -> EngineError {
    EngineError::new(
        "OUTCOME_UNKNOWN",
        format!("{message}; the request was delivered and may have taken effect"),
    )
}

/// Connects and sends one request. Errors before the frame is fully written
/// are retryable: a partial frame is never acted on by the guest.
pub fn send(uds: &Path, request: &GuestRequest) -> Result<Delivered> {
    let mut stream = UnixStream::connect(uds)
        .map_err(|e| EngineError::host(format!("guest channel connect: {e}")))?;
    stream.set_read_timeout(Some(Duration::from_secs(10)))?;
    stream.set_write_timeout(Some(Duration::from_secs(5)))?;
    stream
        .write_all(format!("CONNECT {GUEST_PORT}\n").as_bytes())
        .map_err(|e| EngineError::host(format!("guest channel handshake: {e}")))?;
    let mut reader = BufReader::new(stream.try_clone()?);
    let mut line = String::new();
    reader
        .read_line(&mut line)
        .map_err(|e| EngineError::host(format!("guest channel handshake: {e}")))?;
    if !line.starts_with("OK ") {
        return Err(EngineError::host(format!(
            "guest channel refused: {}",
            line.trim()
        )));
    }
    write_frame(&mut stream, request).map_err(|e| EngineError::host(format!("guest send: {e}")))?;
    Ok(Delivered { reader })
}

/// Sends one request on a fresh connection and reads its reply. A
/// connection never outlives a capture, so restored copies never share a
/// host stream.
pub fn call(uds: &Path, request: &GuestRequest, timeout: Duration) -> Result<GuestResponse> {
    send(uds, request)?.next(Some(timeout))
}

/// Waits for a started command's completion with short `Wait` requests,
/// each on a fresh connection. Waiting is idempotent, so a broken or reset
/// connection (a capture resets the guest's vsock connections) is simply
/// retried; `alive` returning false or `deadline` passing ends the wait.
pub fn wait_exec(
    uds: &Path,
    sandbox_id: &str,
    generation: u64,
    exec_id: &str,
    deadline: Option<Instant>,
    alive: impl Fn() -> bool,
) -> Result<ExecDone> {
    const POLL_MS: u64 = 1000;
    let request = GuestRequest::Wait(Wait {
        sandbox_id: sandbox_id.into(),
        generation,
        exec_id: exec_id.into(),
        wait_ms: POLL_MS,
    });
    let mut last = String::new();
    loop {
        if !alive() {
            return Err(uncertain(&format!(
                "sandbox gone before completion; {last}"
            )));
        }
        if deadline.is_some_and(|d| Instant::now() > d) {
            return Err(uncertain(&format!("no completion before deadline; {last}")));
        }
        match call(uds, &request, Duration::from_millis(POLL_MS + 3000)) {
            Ok(GuestResponse::ExecDone(d)) => return Ok(d),
            Ok(GuestResponse::ExecRunning) => {}
            Ok(GuestResponse::Error { code, message }) => {
                return Err(uncertain(&format!("wait: {code}: {message}")));
            }
            Ok(other) => return Err(uncertain(&format!("wait: unexpected reply {other:?}"))),
            Err(e) => {
                last = e.message;
                std::thread::sleep(Duration::from_millis(20));
            }
        }
    }
}

/// Waits until the helper answers a ping.
pub fn wait_ready(uds: &Path, timeout: Duration) -> Result<GuestResponse> {
    let start = Instant::now();
    let mut last = String::new();
    while start.elapsed() < timeout {
        if uds.exists() {
            match call(
                uds,
                &GuestRequest::Ping(Empty {}),
                Duration::from_millis(500),
            ) {
                Ok(r) => return Ok(r),
                Err(e) => last = e.message,
            }
        }
        std::thread::sleep(Duration::from_micros(500));
    }
    Err(EngineError::host(format!(
        "guest helper not ready after {timeout:?}: {last}"
    )))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Read;
    use std::os::unix::net::UnixListener;

    fn socket(tag: &str) -> std::path::PathBuf {
        let p = std::env::temp_dir().join(format!("lamp-guest-{tag}-{}", std::process::id()));
        let _ = std::fs::remove_file(&p);
        p
    }

    /// A fake Firecracker vsock endpoint that accepts the handshake, reads
    /// the whole request and then drops the connection without replying.
    fn serve_and_drop(path: &Path, handshake: &'static str) -> std::thread::JoinHandle<Vec<u8>> {
        let listener = UnixListener::bind(path).unwrap();
        std::thread::spawn(move || {
            let (mut s, _) = listener.accept().unwrap();
            let mut line = [0_u8; 13]; // "CONNECT 1024\n"
            s.read_exact(&mut line).unwrap();
            s.write_all(handshake.as_bytes()).unwrap();
            let mut prefix = [0_u8; 4];
            let mut got = Vec::new();
            if s.read_exact(&mut prefix).is_ok() {
                let mut body = vec![0; u32::from_be_bytes(prefix) as usize];
                s.read_exact(&mut body).unwrap();
                got.extend(prefix);
                got.extend(body);
            }
            got
        })
    }

    #[test]
    fn lost_reply_after_delivery_is_outcome_unknown() {
        let path = socket("lost");
        let server = serve_and_drop(&path, "OK 1024\n");
        let e = call(&path, &GuestRequest::Ping(Empty {}), Duration::from_secs(2)).unwrap_err();
        assert_eq!(e.code, "OUTCOME_UNKNOWN");
        assert!(!e.retryable);
        assert!(server.join().unwrap().len() > 4, "request was delivered");
        let _ = std::fs::remove_file(path);
    }

    #[test]
    fn failure_before_dispatch_is_retryable() {
        let missing = socket("missing");
        let e = call(
            &missing,
            &GuestRequest::Ping(Empty {}),
            Duration::from_secs(1),
        )
        .unwrap_err();
        assert_eq!((e.code, e.retryable), ("HOST_UNAVAILABLE", true));
        let path = socket("refused");
        let server = serve_and_drop(&path, "FAILURE\n");
        let e = call(&path, &GuestRequest::Ping(Empty {}), Duration::from_secs(1)).unwrap_err();
        assert_eq!((e.code, e.retryable), ("HOST_UNAVAILABLE", true));
        assert!(server.join().unwrap().is_empty(), "nothing was sent");
        let _ = std::fs::remove_file(path);
    }

    /// A command spanning a capture: the first wait's connection is reset
    /// (as Firecracker resets guest vsock connections on snapshot), the
    /// next one finds it still running, the third gets its completion.
    #[test]
    fn completion_survives_a_reset_connection() {
        let path = socket("reset");
        let listener = UnixListener::bind(&path).unwrap();
        let done = ExecDone {
            exit_code: 0,
            signal: 0,
            stdout_b64: lamp_init::b64_encode(b"slept\n"),
            stderr_b64: String::new(),
            truncated: false,
            timed_out: false,
            started_unix_ns: 1,
            finished_unix_ns: 2,
        };
        let replies = [
            None,
            Some(GuestResponse::ExecRunning),
            Some(GuestResponse::ExecDone(done.clone())),
        ];
        let server = std::thread::spawn(move || {
            for reply in replies {
                let (mut s, _) = listener.accept().unwrap();
                let mut line = [0_u8; 13];
                s.read_exact(&mut line).unwrap();
                s.write_all(b"OK 1024\n").unwrap();
                let request: GuestRequest = read_frame(&mut s).unwrap();
                assert!(matches!(request, GuestRequest::Wait(ref w) if w.exec_id == "op_1"));
                if let Some(r) = reply {
                    write_frame(&mut s, &r).unwrap();
                }
            }
        });
        let got = wait_exec(&path, "sb_1", 1, "op_1", None, || true).unwrap();
        assert_eq!(got, done);
        server.join().unwrap();
        // A gone sandbox ends the wait as an uncertain outcome.
        let e = wait_exec(&path, "sb_1", 1, "op_1", None, || false).unwrap_err();
        assert_eq!(e.code, "OUTCOME_UNKNOWN");
        let _ = std::fs::remove_file(path);
    }
}
