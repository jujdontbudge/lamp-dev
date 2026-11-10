//! Lamp Mac engine boundary.
//!
//! `spec` and the confinement profile text are portable; the framework
//! binding, macOS installer and worker build only on macOS.
pub mod confine;
pub mod spec;

#[cfg(target_os = "macos")]
pub mod macos;
#[cfg(target_os = "macos")]
pub mod vz;
#[cfg(target_os = "macos")]
pub mod worker;
