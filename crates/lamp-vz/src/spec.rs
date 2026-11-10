//! What the supervisor hands one VZ worker: the machine and the exact file
//! set it may touch. Portable so the engine can build it on any host.
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Sync {
    /// Guest flushes reach stable storage (F_FULLFSYNC).
    Full,
    Fsync,
    /// Never synced: volatile scratch only.
    None,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct DiskSpec {
    pub path: PathBuf,
    pub read_only: bool,
    pub sync: Sync,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "guest", rename_all = "snake_case", deny_unknown_fields)]
pub enum Machine {
    Linux {
        kernel: PathBuf,
        #[serde(default)]
        initrd: Option<PathBuf>,
        cmdline: String,
        /// Base64 `VZGenericMachineIdentifier`.
        machine_identifier_b64: String,
    },
    Macos {
        /// Base64 `VZMacHardwareModel`.
        hardware_model_b64: String,
        /// Base64 `VZMacMachineIdentifier`.
        machine_identifier_b64: String,
        auxiliary_storage: PathBuf,
    },
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct WorkerSpec {
    pub sandbox_id: String,
    pub machine: Machine,
    pub cpus: usize,
    pub memory_mib: u64,
    pub disks: Vec<DiskSpec>,
    /// Private directory: API/vsock sockets, console log, captures.
    pub dir: PathBuf,
    /// A data NIC only when networking was requested.
    #[serde(default)]
    pub nat_network: bool,
    /// Extra read-only files (kernel, template objects) and read/write
    /// files (state destinations) beyond `dir` and the disks.
    #[serde(default)]
    pub read_paths: Vec<PathBuf>,
    #[serde(default)]
    pub write_paths: Vec<PathBuf>,
    /// Read/write directories beyond `dir` (the sandbox's capture
    /// directories on the store and on the RAM-backed volume).
    #[serde(default)]
    pub write_dirs: Vec<PathBuf>,
}

/// API request on the worker's private control socket (one JSON line).
#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "op", rename_all = "snake_case", deny_unknown_fields)]
pub enum Api {
    State {},
    Pause {},
    Resume {},
    /// Saves the paused machine's full RAM/device state to `path`.
    Save {
        path: PathBuf,
    },
    /// Hard stop.
    Stop {},
    /// Guest-visible power-off request.
    RequestStop {},
    /// Stop (if needed) and end the worker.
    Exit {},
    /// Confinement self-test: tries the listed effects from inside the
    /// worker and reports which the profile refused.
    Probe {
        checks: Vec<ProbeCheck>,
    },
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum ProbeCheck {
    ReadFile { path: PathBuf },
    WriteFile { path: PathBuf },
    ListDir { path: PathBuf },
    Connect { host: String, port: u16 },
    MachLookup { name: String },
    Exec { path: PathBuf },
    Keychain {},
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq, Eq)]
pub struct ApiReply {
    pub ok: bool,
    #[serde(default)]
    pub state: String,
    #[serde(default)]
    pub error: String,
    #[serde(default)]
    pub elapsed_us: u64,
    #[serde(default)]
    pub detail: serde_json::Value,
}
