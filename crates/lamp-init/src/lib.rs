//! Host/guest channel shared by `lamp-init`, the guest helper and the host engine.
//!
//! The channel runs over the private Firecracker vsock device only. It is not
//! `lamp.worker/1`, not public and not frozen: T14 freezes saved-state formats.
//! A frame is a four-byte big-endian length followed by that many JSON bytes.
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;
use std::io::{self, Read, Write};

/// Guest vsock port the helper listens on.
pub const GUEST_PORT: u32 = 1024;
/// Largest accepted guest channel frame.
pub const MAX_GUEST_FRAME: usize = 4 << 20;
/// Default bound for each captured command stream.
pub const DEFAULT_OUTPUT_LIMIT: usize = 64 << 10;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "op", rename_all = "snake_case", deny_unknown_fields)]
pub enum GuestRequest {
    /// Liveness only. Never admits task I/O.
    Ping(Empty),
    /// Identity barrier: must succeed for the current sandbox/generation
    /// before any command is admitted.
    Barrier(Barrier),
    Exec(Exec),
    /// Flush and power off (used by template builds).
    Shutdown(Empty),
}

/// Payload of commands without arguments; still rejects unknown fields.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Empty {}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Barrier {
    pub sandbox_id: String,
    pub generation: u64,
    pub hostname: String,
    /// 32 lowercase hex characters.
    pub machine_id: String,
    /// Host-drawn entropy, lowercase hex, at least 64 bytes.
    pub entropy_hex: String,
    pub unix_time_ns: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Exec {
    pub sandbox_id: String,
    pub generation: u64,
    pub argv: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    #[serde(default)]
    pub cwd: String,
    pub timeout_ms: u64,
    pub output_limit: usize,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "result", rename_all = "snake_case", deny_unknown_fields)]
pub enum GuestResponse {
    Pong(Status),
    BarrierAck(BarrierAck),
    ExecDone(ExecDone),
    ShuttingDown,
    Error { code: String, message: String },
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Status {
    pub sandbox_id: String,
    pub generation: u64,
    pub kernel_boot_id: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct BarrierAck {
    pub sandbox_id: String,
    pub generation: u64,
    pub machine_id: String,
    pub hostname: String,
    /// 16 bytes read from the guest kernel CRNG after reseeding.
    pub rng_sample_hex: String,
    pub kernel_boot_id: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExecDone {
    pub exit_code: i32,
    /// Terminating signal, 0 when the process exited normally.
    pub signal: i32,
    pub stdout_b64: String,
    pub stderr_b64: String,
    pub truncated: bool,
    pub timed_out: bool,
    pub started_unix_ns: u64,
    pub finished_unix_ns: u64,
}

pub fn read_frame<T: for<'de> Deserialize<'de>>(reader: &mut impl Read) -> io::Result<T> {
    let mut prefix = [0_u8; 4];
    reader.read_exact(&mut prefix)?;
    let size = u32::from_be_bytes(prefix) as usize;
    if size == 0 || size > MAX_GUEST_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid guest frame length",
        ));
    }
    let mut body = vec![0; size];
    reader.read_exact(&mut body)?;
    serde_json::from_slice(&body).map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))
}

pub fn write_frame<T: Serialize>(writer: &mut impl Write, value: &T) -> io::Result<()> {
    let body = serde_json::to_vec(value).map_err(io::Error::other)?;
    if body.is_empty() || body.len() > MAX_GUEST_FRAME {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid guest frame length",
        ));
    }
    let mut out = Vec::with_capacity(body.len() + 4);
    out.extend_from_slice(&(body.len() as u32).to_be_bytes());
    out.extend_from_slice(&body);
    writer.write_all(&out)?;
    writer.flush()
}

const B64: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";

pub fn b64_encode(data: &[u8]) -> String {
    let mut out = String::with_capacity(data.len().div_ceil(3) * 4);
    for chunk in data.chunks(3) {
        let b = [
            chunk[0],
            *chunk.get(1).unwrap_or(&0),
            *chunk.get(2).unwrap_or(&0),
        ];
        let n = (u32::from(b[0]) << 16) | (u32::from(b[1]) << 8) | u32::from(b[2]);
        for i in 0..4 {
            if i <= chunk.len() {
                out.push(B64[((n >> (18 - 6 * i)) & 63) as usize] as char);
            } else {
                out.push('=');
            }
        }
    }
    out
}

pub fn b64_decode(text: &str) -> Option<Vec<u8>> {
    let bytes = text.as_bytes();
    if !bytes.len().is_multiple_of(4) {
        return None;
    }
    let mut out = Vec::with_capacity(bytes.len() / 4 * 3);
    for chunk in bytes.chunks(4) {
        let mut n = 0_u32;
        let mut pad = 0;
        for (i, &c) in chunk.iter().enumerate() {
            let v = match c {
                b'A'..=b'Z' => c - b'A',
                b'a'..=b'z' => c - b'a' + 26,
                b'0'..=b'9' => c - b'0' + 52,
                b'+' => 62,
                b'/' => 63,
                b'=' if i >= 2 => {
                    pad += 1;
                    0
                }
                _ => return None,
            };
            if pad > 0 && c != b'=' {
                return None;
            }
            n = (n << 6) | u32::from(v);
        }
        let decoded = [(n >> 16) as u8, (n >> 8) as u8, n as u8];
        out.extend_from_slice(&decoded[..3 - pad]);
    }
    Some(out)
}

pub fn hex_encode(data: &[u8]) -> String {
    data.iter().map(|b| format!("{b:02x}")).collect()
}

pub fn hex_decode(text: &str) -> Option<Vec<u8>> {
    if !text.len().is_multiple_of(2) {
        return None;
    }
    (0..text.len())
        .step_by(2)
        .map(|i| u8::from_str_radix(text.get(i..i + 2)?, 16).ok())
        .collect()
}

/// Validates barrier fields before the guest applies any of them.
pub fn validate_barrier(b: &Barrier) -> Result<(), String> {
    if b.sandbox_id.is_empty() || b.sandbox_id.len() > 64 {
        return Err("sandbox_id must be 1..64 bytes".into());
    }
    if b.generation == 0 {
        return Err("generation must be nonzero".into());
    }
    let host_ok = !b.hostname.is_empty()
        && b.hostname.len() <= 63
        && b.hostname
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || c == b'-');
    if !host_ok {
        return Err("hostname must be 1..63 alphanumeric or '-' bytes".into());
    }
    if b.machine_id.len() != 32
        || !b
            .machine_id
            .bytes()
            .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
    {
        return Err("machine_id must be 32 lowercase hex characters".into());
    }
    match hex_decode(&b.entropy_hex) {
        Some(e) if e.len() >= 64 => Ok(()),
        _ => Err("entropy_hex must carry at least 64 bytes".into()),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn base64_roundtrip() {
        for len in 0..40 {
            let data: Vec<u8> = (0..len).map(|i| (i * 37 + 11) as u8).collect();
            assert_eq!(b64_decode(&b64_encode(&data)).unwrap(), data);
        }
        assert_eq!(b64_encode(b"Lamp"), "TGFtcA==");
        assert!(b64_decode("TG=t").is_none());
        assert!(b64_decode("abc").is_none());
    }

    #[test]
    fn frames_roundtrip_and_reject_unknown() {
        let req = GuestRequest::Exec(Exec {
            sandbox_id: "sb_1".into(),
            generation: 3,
            argv: vec!["true".into()],
            env: BTreeMap::new(),
            cwd: String::new(),
            timeout_ms: 1000,
            output_limit: 10,
        });
        let mut buf = Vec::new();
        write_frame(&mut buf, &req).unwrap();
        assert_eq!(
            read_frame::<GuestRequest>(&mut buf.as_slice()).unwrap(),
            req
        );
        let mut ping = Vec::new();
        write_frame(&mut ping, &GuestRequest::Ping(Empty {})).unwrap();
        assert_eq!(
            read_frame::<GuestRequest>(&mut ping.as_slice()).unwrap(),
            GuestRequest::Ping(Empty {})
        );
        let bad = br#"{"op":"ping","extra":1}"#;
        let mut framed = (bad.len() as u32).to_be_bytes().to_vec();
        framed.extend_from_slice(bad);
        assert!(read_frame::<GuestRequest>(&mut framed.as_slice()).is_err());
        let huge = ((MAX_GUEST_FRAME + 1) as u32).to_be_bytes();
        assert!(read_frame::<GuestRequest>(&mut huge.as_slice()).is_err());
    }

    #[test]
    fn barrier_validation() {
        let mut b = Barrier {
            sandbox_id: "sb_1".into(),
            generation: 1,
            hostname: "sb-1".into(),
            machine_id: "0123456789abcdef0123456789abcdef".into(),
            entropy_hex: "ab".repeat(64),
            unix_time_ns: 1,
        };
        validate_barrier(&b).unwrap();
        b.entropy_hex = "ab".repeat(63);
        assert!(validate_barrier(&b).is_err());
        b.entropy_hex = "ab".repeat(64);
        b.hostname = "bad name".into();
        assert!(validate_barrier(&b).is_err());
        b.hostname = "ok".into();
        b.generation = 0;
        assert!(validate_barrier(&b).is_err());
    }
}
