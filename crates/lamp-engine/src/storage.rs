//! Relay an owner-owned, private storage socket to a single guest mount.
use crate::{EngineError, Result, guest};
use lamp_init::{GuestRequest, Mount};
use std::os::unix::{
    fs::{FileTypeExt, MetadataExt},
    net::UnixStream,
};
use std::path::Path;

#[allow(clippy::too_many_arguments)]
pub fn mount(
    vsock: &Path,
    sandbox: &str,
    generation: u64,
    socket: &str,
    path: &str,
    read_only: bool,
    unmount: bool,
    uid: u32,
) -> Result<()> {
    if !path.starts_with('/')
        || path == "/"
        || path.split('/').any(|p| p == "..")
        || path.contains('\0')
    {
        return Err(EngineError::invalid("invalid mount path"));
    }
    let request = GuestRequest::Mount(Mount {
        sandbox_id: sandbox.into(),
        generation,
        path: path.into(),
        read_only,
        unmount,
    });
    if unmount {
        match guest::call(vsock, &request, std::time::Duration::from_secs(10))? {
            lamp_init::GuestResponse::StorageUnmounted => return Ok(()),
            other => return Err(EngineError::host(format!("unmount: {other:?}"))),
        }
    }

    let meta = std::fs::symlink_metadata(socket)?;
    if !meta.file_type().is_socket() || meta.uid() != uid || meta.mode() & 0o077 != 0 {
        return Err(EngineError::new(
            "SCOPE_DENIED",
            "storage relay must be a private socket owned by the granted caller",
        ));
    }
    let storage = UnixStream::connect(socket)?;
    use std::os::fd::AsRawFd;
    #[cfg(target_os = "linux")]
    let peer_uid = {
        let mut cred: libc::ucred = unsafe { std::mem::zeroed() };
        let mut size = std::mem::size_of_val(&cred) as libc::socklen_t;
        if unsafe {
            libc::getsockopt(
                storage.as_raw_fd(),
                libc::SOL_SOCKET,
                libc::SO_PEERCRED,
                (&mut cred as *mut libc::ucred).cast(),
                &mut size,
            )
        } < 0
        {
            return Err(std::io::Error::last_os_error().into());
        }
        cred.uid
    };
    #[cfg(target_os = "macos")]
    let peer_uid = {
        let (mut user, mut group) = (0, 0);
        if unsafe { libc::getpeereid(storage.as_raw_fd(), &mut user, &mut group) } < 0 {
            return Err(std::io::Error::last_os_error().into());
        }
        user
    };
    if peer_uid != uid {
        return Err(EngineError::new(
            "SCOPE_DENIED",
            "storage relay peer is not the granted caller",
        ));
    }
    let link = guest::handoff(vsock, &request)?;
    let mut sr = storage.try_clone()?;
    let mut lw = link.try_clone()?;
    std::thread::spawn(move || {
        let _ = std::io::copy(&mut sr, &mut lw);
        let _ = lw.shutdown(std::net::Shutdown::Both);
    });
    std::thread::spawn(move || {
        let mut link = link;
        let mut storage = storage;
        let _ = std::io::copy(&mut link, &mut storage);
        let _ = storage.shutdown(std::net::Shutdown::Both);
    });
    // A successful mount must be observed inside the guest; a relay alone
    // is not evidence that the kernel supports 9p or accepted the mount.
    Ok(())
}
