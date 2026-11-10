//! `lamp-supervisor`: the privileged local service that owns sandbox VMMs on
//! a Linux host and serves `lamp.worker/1` to `lampd`.
#[cfg(target_os = "linux")]
pub mod admin;
pub mod client;
#[cfg(unix)]
pub mod grants;
#[cfg(target_os = "linux")]
pub mod server;
