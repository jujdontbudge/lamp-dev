//! `lamp-supervisor`: the privileged local service that owns sandbox VMMs on
//! a Linux host or an Apple Silicon Mac and serves `lamp.worker/1` to `lampd`.
pub mod client;
#[cfg(unix)]
pub mod grants;
#[cfg(any(target_os = "linux", target_os = "macos"))]
pub mod server;
