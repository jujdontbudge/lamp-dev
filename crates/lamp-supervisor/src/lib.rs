//! `lamp-supervisor`: the privileged local service that owns sandbox VMMs on
//! a Linux host or an Apple Silicon Mac and serves `lamp.worker/1` to `lampd`.
pub fn release_version() -> &'static str {
    option_env!("LAMP_RELEASE_VERSION")
        .unwrap_or(include_str!("../../../internal/buildversion/VERSION").trim())
}

#[cfg(target_os = "linux")]
pub mod admin;
pub mod client;
#[cfg(unix)]
pub mod grants;
#[cfg(any(target_os = "linux", target_os = "macos"))]
pub mod server;

#[cfg(any(target_os = "linux", target_os = "macos"))]
pub mod templates;
