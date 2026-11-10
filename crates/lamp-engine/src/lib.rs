//! Lamp engines: the Linux Firecracker reference adapter and the Apple
//! Silicon Virtualization.framework adapter.
//!
//! Platform-neutral parts (definitions, manifests, errors) build everywhere.
//! On Linux `engine` is the Firecracker adapter; on macOS it is the VZ
//! adapter (`mac.rs`) with the same surface, so the supervisor and
//! `lamp.worker/1` are one implementation.
pub mod definition;
pub mod error;
pub mod manifest;

#[cfg(target_os = "linux")]
pub mod build;
#[cfg(target_os = "linux")]
pub mod engine;
#[cfg(target_os = "linux")]
pub mod fc;
#[cfg(any(target_os = "linux", target_os = "macos"))]
pub mod fsutil;
#[cfg(any(target_os = "linux", target_os = "macos"))]
pub mod guest;
#[cfg(target_os = "linux")]
pub mod net;

#[cfg(target_os = "macos")]
#[path = "mac.rs"]
pub mod engine;

pub use error::{EngineError, Result};
