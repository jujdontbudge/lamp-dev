//! Lamp Linux engine (Firecracker reference adapter).
//!
//! Platform-neutral parts (definitions, manifests, errors) build everywhere;
//! the VMM adapter, store and template builder are Linux-only.
pub mod definition;
pub mod error;
pub mod manifest;

#[cfg(target_os = "linux")]
pub mod build;
#[cfg(target_os = "linux")]
pub mod disk;
#[cfg(target_os = "linux")]
pub mod dm;
#[cfg(target_os = "linux")]
pub mod durable;
#[cfg(target_os = "linux")]
pub mod engine;
#[cfg(target_os = "linux")]
pub mod fault;
#[cfg(target_os = "linux")]
pub mod fc;
#[cfg(target_os = "linux")]
pub mod fsutil;
#[cfg(target_os = "linux")]
pub mod guest;
#[cfg(target_os = "linux")]
pub mod memimg;
#[cfg(target_os = "linux")]
pub mod net;

pub use error::{EngineError, Result};
