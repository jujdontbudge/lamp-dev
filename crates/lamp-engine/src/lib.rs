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
pub mod engine;
#[cfg(target_os = "linux")]
pub mod fc;
#[cfg(target_os = "linux")]
pub mod fsutil;
#[cfg(target_os = "linux")]
pub mod guest;

pub use error::{EngineError, Result};
