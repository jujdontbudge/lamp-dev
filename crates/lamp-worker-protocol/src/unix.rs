//! Unix-socket mechanics for lamp.worker/1: peer OS credentials and frames
//! that carry SCM_RIGHTS descriptors.
use crate::{Frame, MAX_CONTROL_BYTES};
use prost::Message;
use std::io;
use std::os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd};

/// Most descriptors accepted with one frame.
pub const MAX_FRAME_DESCRIPTORS: usize = 16;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct PeerCred {
    pub pid: u32,
    pub uid: u32,
    pub gid: u32,
}

#[cfg(target_os = "linux")]
pub fn peer_cred(fd: &impl AsRawFd) -> io::Result<PeerCred> {
    let mut cred: libc::ucred = unsafe { std::mem::zeroed() };
    let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
    // SAFETY: cred/len are valid out-pointers sized for SO_PEERCRED.
    let rc = unsafe {
        libc::getsockopt(
            fd.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            (&raw mut cred).cast(),
            &mut len,
        )
    };
    if rc != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(PeerCred {
        pid: cred.pid as u32,
        uid: cred.uid,
        gid: cred.gid,
    })
}

#[cfg(target_os = "macos")]
pub fn peer_cred(fd: &impl AsRawFd) -> io::Result<PeerCred> {
    let (mut uid, mut gid) = (0, 0);
    // SAFETY: valid out-pointers.
    if unsafe { libc::getpeereid(fd.as_raw_fd(), &mut uid, &mut gid) } != 0 {
        return Err(io::Error::last_os_error());
    }
    let mut pid: libc::pid_t = 0;
    let mut len = std::mem::size_of::<libc::pid_t>() as libc::socklen_t;
    // SAFETY: LOCAL_PEERPID writes a pid_t.
    let rc = unsafe {
        libc::getsockopt(
            fd.as_raw_fd(),
            libc::SOL_LOCAL,
            libc::LOCAL_PEERPID,
            (&raw mut pid).cast(),
            &mut len,
        )
    };
    if rc != 0 {
        return Err(io::Error::last_os_error());
    }
    Ok(PeerCred {
        pid: pid as u32,
        uid,
        gid,
    })
}

fn cmsg_space(count: usize) -> usize {
    // SAFETY: CMSG_SPACE is a pure size computation.
    unsafe { libc::CMSG_SPACE((count * std::mem::size_of::<RawFd>()) as u32) as usize }
}

/// Writes one frame; `fds` travel as SCM_RIGHTS on the first byte.
pub fn send_frame_with_fds(sock: &impl AsRawFd, frame: &Frame, fds: &[RawFd]) -> io::Result<()> {
    let size = frame.encoded_len();
    if frame.payload.is_none() || size == 0 || size > MAX_CONTROL_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "invalid worker frame length",
        ));
    }
    if fds.len() > MAX_FRAME_DESCRIPTORS {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "too many descriptors",
        ));
    }
    let mut bytes = Vec::with_capacity(size + 4);
    bytes.extend_from_slice(&(size as u32).to_be_bytes());
    frame.encode(&mut bytes).map_err(io::Error::other)?;
    let mut sent = 0;
    let mut control = vec![0_u8; cmsg_space(fds.len().max(1))];
    while sent < bytes.len() {
        let mut iov = libc::iovec {
            iov_base: bytes[sent..].as_ptr() as *mut _,
            iov_len: bytes.len() - sent,
        };
        // SAFETY: msghdr points at live buffers for the duration of sendmsg.
        let n = unsafe {
            let mut msg: libc::msghdr = std::mem::zeroed();
            msg.msg_iov = &mut iov;
            msg.msg_iovlen = 1;
            if sent == 0 && !fds.is_empty() {
                msg.msg_control = control.as_mut_ptr().cast();
                msg.msg_controllen = cmsg_space(fds.len()) as _;
                let cmsg = libc::CMSG_FIRSTHDR(&msg);
                (*cmsg).cmsg_level = libc::SOL_SOCKET;
                (*cmsg).cmsg_type = libc::SCM_RIGHTS;
                (*cmsg).cmsg_len = libc::CMSG_LEN(std::mem::size_of_val(fds) as u32) as _;
                std::ptr::copy_nonoverlapping(
                    fds.as_ptr(),
                    libc::CMSG_DATA(cmsg).cast(),
                    fds.len(),
                );
            }
            libc::sendmsg(sock.as_raw_fd(), &msg, 0)
        };
        if n < 0 {
            let e = io::Error::last_os_error();
            if e.kind() == io::ErrorKind::Interrupted {
                continue;
            }
            return Err(e);
        }
        sent += n as usize;
    }
    Ok(())
}

/// Reads exactly `buf.len()` bytes, collecting any descriptors that arrive.
fn recv_exact(sock: RawFd, buf: &mut [u8], fds: &mut Vec<OwnedFd>) -> io::Result<()> {
    let mut got = 0;
    let mut control = vec![0_u8; cmsg_space(MAX_FRAME_DESCRIPTORS)];
    while got < buf.len() {
        let mut iov = libc::iovec {
            iov_base: buf[got..].as_mut_ptr().cast(),
            iov_len: buf.len() - got,
        };
        // SAFETY: msghdr points at live buffers; received descriptors are
        // immediately wrapped in OwnedFd so every one is closed on error.
        let (n, truncated) = unsafe {
            let mut msg: libc::msghdr = std::mem::zeroed();
            msg.msg_iov = &mut iov;
            msg.msg_iovlen = 1;
            msg.msg_control = control.as_mut_ptr().cast();
            msg.msg_controllen = control.len() as _;
            #[cfg(target_os = "linux")]
            let flags = libc::MSG_CMSG_CLOEXEC;
            #[cfg(not(target_os = "linux"))]
            let flags = 0;
            let n = libc::recvmsg(sock, &mut msg, flags);
            if n > 0 {
                let mut cmsg = libc::CMSG_FIRSTHDR(&msg);
                while !cmsg.is_null() {
                    if (*cmsg).cmsg_level == libc::SOL_SOCKET
                        && (*cmsg).cmsg_type == libc::SCM_RIGHTS
                    {
                        let data = libc::CMSG_DATA(cmsg) as *const RawFd;
                        let header = libc::CMSG_LEN(0) as usize;
                        let count =
                            ((*cmsg).cmsg_len as usize - header) / std::mem::size_of::<RawFd>();
                        for i in 0..count {
                            fds.push(OwnedFd::from_raw_fd(std::ptr::read_unaligned(data.add(i))));
                        }
                    }
                    cmsg = libc::CMSG_NXTHDR(&msg, cmsg);
                }
            }
            (n, msg.msg_flags & libc::MSG_CTRUNC != 0)
        };
        if n < 0 {
            let e = io::Error::last_os_error();
            if e.kind() == io::ErrorKind::Interrupted {
                continue;
            }
            return Err(e);
        }
        if n == 0 {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                "worker channel closed",
            ));
        }
        if truncated || fds.len() > MAX_FRAME_DESCRIPTORS {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "too many descriptors",
            ));
        }
        got += n as usize;
    }
    Ok(())
}

#[derive(Debug)]
pub struct RejectedFrame(pub Frame);
impl std::fmt::Display for RejectedFrame {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("unknown or non-canonical fields")
    }
}
impl std::error::Error for RejectedFrame {}

/// Reads one frame plus every descriptor sent with it. On any error all
/// received descriptors are closed.
pub fn recv_frame_with_fds(sock: &impl AsRawFd) -> io::Result<(Frame, Vec<OwnedFd>)> {
    let mut fds = Vec::new();
    let mut prefix = [0_u8; 4];
    recv_exact(sock.as_raw_fd(), &mut prefix, &mut fds)?;
    let size = u32::from_be_bytes(prefix) as usize;
    if size == 0 || size > MAX_CONTROL_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "invalid worker frame length",
        ));
    }
    let mut body = vec![0; size];
    recv_exact(sock.as_raw_fd(), &mut body, &mut fds)?;
    let frame = Frame::decode(body.as_slice())
        .map_err(|e| io::Error::new(io::ErrorKind::InvalidData, e))?;
    if frame.payload.is_none() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "missing worker payload",
        ));
    }
    // Unknown fields are dropped by decode; re-encoding exposes them. Map
    // entries may arrive in any order, so compare field multisets.
    if !crate::wire::equivalent(&frame.encode_to_vec(), &body) {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            RejectedFrame(frame),
        ));
    }
    Ok((frame, fds))
}
