//! Host side of the private guest channel, through Firecracker's vsock UDS.
use crate::error::{EngineError, Result};
use lamp_init::{read_frame, write_frame, GuestRequest, GuestResponse, GUEST_PORT};
use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::time::{Duration, Instant};

/// Sends one request on a fresh connection. A connection never outlives a
/// capture, so restored copies never share a host stream.
pub fn call(uds: &Path, request: &GuestRequest, timeout: Duration) -> Result<GuestResponse> {
    let mut stream = UnixStream::connect(uds)
        .map_err(|e| EngineError::host(format!("guest channel connect: {e}")))?;
    stream.set_read_timeout(Some(timeout.max(Duration::from_millis(1))))?;
    stream.set_write_timeout(Some(Duration::from_secs(5)))?;
    stream.write_all(format!("CONNECT {GUEST_PORT}\n").as_bytes())?;
    let mut reader = BufReader::new(stream.try_clone()?);
    let mut line = String::new();
    reader
        .read_line(&mut line)
        .map_err(|e| EngineError::host(format!("guest channel handshake: {e}")))?;
    if !line.starts_with("OK ") {
        return Err(EngineError::host(format!("guest channel refused: {}", line.trim())));
    }
    write_frame(&mut stream, request).map_err(|e| EngineError::host(format!("guest send: {e}")))?;
    read_frame(&mut reader).map_err(|e| EngineError::host(format!("guest reply: {e}")))
}

/// Waits until the helper answers a ping.
pub fn wait_ready(uds: &Path, timeout: Duration) -> Result<GuestResponse> {
    let start = Instant::now();
    let mut last = String::new();
    while start.elapsed() < timeout {
        if uds.exists() {
            match call(uds, &GuestRequest::Ping, Duration::from_millis(500)) {
                Ok(r) => return Ok(r),
                Err(e) => last = e.message,
            }
        }
        std::thread::sleep(Duration::from_micros(500));
    }
    Err(EngineError::host(format!("guest helper not ready after {timeout:?}: {last}")))
}
