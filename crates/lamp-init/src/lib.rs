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
    /// Starts a command. The helper replies `ExecStarted` once the program
    /// has been exec'd; completion is fetched with `Wait`.
    Exec(Exec),
    /// Waits up to `wait_ms` for a started command's completion. Each wait is
    /// a short, idempotent request on a fresh connection, so a capture that
    /// resets the guest's vsock connections never loses a completion.
    Wait(Wait),
    SessionCall(SessionCall),
    ExecIo(ExecIo),
    Mount(Mount),
    /// Freezes every task process (the `tasks` cgroup) before a capture, so
    /// nothing runs between the copy and the copy's own identity barrier.
    Freeze(Empty),
    /// Thaws the task processes of a source after its capture. A restored
    /// copy is thawed only by its identity barrier.
    Thaw(Empty),
    /// Flush and power off (used by template builds).
    Shutdown(Empty),
}

/// Guest cgroup v2 directory every command and its descendants run in.
pub const TASKS_CGROUP: &str = "/sys/fs/cgroup/tasks";
/// Subordinate guest UID/GID that task namespace root maps to.
pub const TASK_UID: u32 = 65_537;
/// Files that delegate a cgroup v2 subtree (kernel cgroup-v2 "Delegation").
/// Only a per-exec leaf under `TASKS_CGROUP` is ever delegated; the tasks
/// cgroup itself stays guest-root-owned so no task can block later execs.
pub const DELEGATED_CGROUP_FILES: [&str; 3] =
    ["cgroup.procs", "cgroup.threads", "cgroup.subtree_control"];

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

/// Checks an exec's requested environment before anything runs: names are
/// non-empty and contain neither `=` nor NUL, values contain no NUL. Both the
/// direct and the audited (collector) paths apply exactly this environment.
pub fn validate_env(env: &BTreeMap<String, String>) -> Result<(), String> {
    for (k, v) in env {
        if k.is_empty() || k.contains('=') || k.contains('\0') {
            return Err(format!("invalid environment name {k:?}"));
        }
        if v.contains('\0') {
            return Err(format!("environment value for {k:?} contains NUL"));
        }
    }
    Ok(())
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Exec {
    pub sandbox_id: String,
    pub generation: u64,
    /// Host-chosen ID (the Start operation ID) that `Wait` refers to.
    pub exec_id: String,
    pub argv: Vec<String>,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    #[serde(default)]
    pub cwd: String,
    /// Kill the command's process group after this long; 0 means never.
    pub timeout_ms: u64,
    pub output_limit: usize,
    #[serde(default)]
    pub stdin_path: String,
    #[serde(default)]
    pub pty: bool,
    #[serde(default)]
    pub stdin_open: bool,
    /// When set, the command runs only under the protected task collector
    /// (`lamp-guard collect vsock:PORT TASK_UID -- argv`), which streams its
    /// process records to the host before releasing each effect.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub audit: Option<Audit>,
}

/// Host-chosen collector attachment for one command.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Audit {
    /// Host vsock port (CID 2) the collector connects to.
    pub port: u32,
    /// Subordinate guest UID/GID the task namespace root maps to (>= 65536).
    pub task_uid: u32,
}

/// Guest path of the protected collector (installed by the seed).
pub const GUARD_BIN: &str = "/run/lamp/lamp-guard";
/// Guest side of the private sandbox link (a /30 shared with the guard).
pub const GUEST_ADDR: &str = "198.18.240.2";
pub const GUEST_ADDR_CIDR: &str = "198.18.240.2/30";
pub const GUEST_GATEWAY: &str = "198.18.240.1";
pub const GUEST_MAC: &str = "06:00:c6:12:f0:02";

/// Host vsock port the guest collectors connect to.
pub const AUDIT_PORT: u32 = 5000;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Wait {
    pub sandbox_id: String,
    pub generation: u64,
    pub exec_id: String,
    pub wait_ms: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SessionCall {
    pub sandbox_id: String,
    pub generation: u64,
    pub request_json: String,
    pub bridge_source: String,
    pub audit: Option<Audit>,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "result", rename_all = "snake_case", deny_unknown_fields)]
pub enum GuestResponse {
    StorageUnmounted,
    Pong(Status),
    BarrierAck(BarrierAck),
    /// The program was exec'd (the close-on-exec status pipe closed empty).
    ExecStarted(ExecStarted),
    ExecDone(ExecDone),
    /// `Wait` elapsed while the command was still running.
    ExecRunning,
    SessionReply {
        response_json: String,
    },
    Frozen,
    Thawed,
    ShuttingDown,
    Error {
        code: String,
        message: String,
    },
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Status {
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub metrics: BTreeMap<String, u64>,
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
pub struct ExecStarted {
    pub pid: i32,
    pub started_unix_ns: u64,
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

/// Incremental command I/O. Identity is checked before looking up the command.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ExecIo {
    pub sandbox_id: String,
    pub generation: u64,
    pub exec_id: String,
    #[serde(default)]
    pub stdout_offset: u64,
    #[serde(default)]
    pub stderr_offset: u64,
    #[serde(default)]
    pub signal: i32,
    #[serde(default)]
    pub input_b64: String,
    #[serde(default)]
    pub input_eof: bool,
    #[serde(default)]
    pub rows: u16,
    #[serde(default)]
    pub columns: u16,
}
pub mod command_io;
/// Private host-mediated filesystem attachment, handed directly to the
/// kernel's 9p fd transport. Tasks never get the channel descriptor.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Mount {
    pub sandbox_id: String,
    pub generation: u64,
    pub path: String,
    pub read_only: bool,
    pub unmount: bool,
}

#[cfg(target_os = "macos")]
pub mod mac_metrics;
