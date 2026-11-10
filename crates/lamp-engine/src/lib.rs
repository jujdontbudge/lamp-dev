//! Lamp Linux engine (Firecracker reference adapter).
//!
//! Platform-neutral parts (definitions, manifests, errors) build everywhere;
//! the VMM adapter, store and template builder are Linux-only.
pub mod custody;
#[cfg(unix)]
pub mod custody_store;
pub mod definition;
pub mod error;
pub mod manifest;

#[cfg(test)]
mod custody_tests;

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
#[cfg(target_os = "linux")]
pub mod net;
#[cfg(target_os = "linux")]
pub mod subscription;
#[cfg(target_os = "linux")]
pub mod subscription_bridge;
#[cfg(target_os = "linux")]
pub mod transfer;

pub use error::{EngineError, Result};
