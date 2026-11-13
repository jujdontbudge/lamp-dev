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

/// Hand the private link to a protocol such as 9p after a framed request.
pub fn handoff(uds: &Path, request: &GuestRequest) -> Result<UnixStream> {
    let delivered = send(uds, request)?;
    let stream = delivered.reader.into_inner();
    stream.set_read_timeout(None)?;
    stream.set_write_timeout(None)?;
    Ok(stream)
}
