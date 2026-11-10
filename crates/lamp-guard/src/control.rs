//! Private supervisor-to-guard revision channel, not a guest or public RPC.
//! A preconnected AF_UNIX SOCK_SEQPACKET descriptor is the authority. No
//! listener, pathname, provider keys, inherited peer descriptors or new syscall
//! permissions are added after confinement. Channel loss fences networking.
use crate::policy::{Layer, validate_layers};
use serde::Deserialize;
use std::{
    io, mem,
    os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd},
};

pub const MAX_UPDATE: usize = 60 * 1024;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Update {
    pub expected_revision: u64,
    pub revision: u64,
    pub policy_layers: Vec<Layer>,
}
impl Update {
    pub fn validate(&self, current: u64, require_allowlist: bool) -> Result<(), &'static str> {
        if self.expected_revision != current {
            return Err("REVISION_CONFLICT");
        }
        if current.checked_add(1) != Some(self.revision) {
            return Err("INVALID_REVISION");
        }
        validate_layers(&self.policy_layers, require_allowlist).map_err(|_| "INVALID_POLICY")
    }
}

pub enum Message {
    Update(Update),
    Invalid,
}

pub struct Control {
    fd: OwnedFd,
    // Allocate once before confinement, not 60 KiB of zeroing per packet.
    buffer: Vec<u8>,
}
impl AsRawFd for Control {
    fn as_raw_fd(&self) -> RawFd {
        self.fd.as_raw_fd()
    }
}
impl Control {
    /// # Safety
    /// `fd` is exclusively transferred from the trusted supervisor. It must
    /// never have been available to the guest, worker, plugin or task.
    pub unsafe fn from_fd(fd: RawFd) -> io::Result<Self> {
        if fd < 3 {
            return Err(io::Error::other("control needs a dedicated descriptor >=3"));
        }
        let owned = unsafe { OwnedFd::from_raw_fd(fd) };
        let mut kind: libc::c_int = 0;
        let mut len = mem::size_of_val(&kind) as libc::socklen_t;
        if unsafe {
            libc::getsockopt(
                fd,
                libc::SOL_SOCKET,
                libc::SO_TYPE,
                (&raw mut kind).cast(),
                &mut len,
            )
        } != 0
        {
            return Err(io::Error::last_os_error());
        }
        let mut peer: libc::sockaddr_storage = unsafe { mem::zeroed() };
        let mut len = mem::size_of_val(&peer) as libc::socklen_t;
        if unsafe { libc::getpeername(fd, (&raw mut peer).cast(), &mut len) } != 0 {
            return Err(io::Error::last_os_error());
        }
        if kind != libc::SOCK_SEQPACKET || peer.ss_family as i32 != libc::AF_UNIX {
            return Err(io::Error::other(
                "control requires connected AF_UNIX SOCK_SEQPACKET",
            ));
        }
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFD) };
        if flags < 0 || unsafe { libc::fcntl(fd, libc::F_SETFD, flags | libc::FD_CLOEXEC) } < 0 {
            return Err(io::Error::last_os_error());
        }
        Ok(Self {
            fd: owned,
            buffer: vec![0u8; MAX_UPDATE],
        })
    }
    /// One bounded message per packet/idle tick. An invalid request cannot
    /// mutate the active revision. Truncation/disconnect is a channel fault.
    pub fn receive(&mut self) -> io::Result<Option<Message>> {
        let fd = self.as_raw_fd();
        let bytes = &mut self.buffer;
        let n = unsafe {
            libc::recv(
                fd,
                bytes.as_mut_ptr().cast(),
                bytes.len(),
                libc::MSG_DONTWAIT | libc::MSG_TRUNC,
            )
        };
        if n < 0 {
            let e = io::Error::last_os_error();
            return if e.kind() == io::ErrorKind::WouldBlock {
                Ok(None)
            } else {
                Err(e)
            };
        }
        if n == 0 || n as usize > bytes.len() {
            return Err(io::Error::other(
                "control disconnected or oversized revision",
            ));
        }
        Ok(Some(match serde_json::from_slice(&bytes[..n as usize]) {
            Ok(update) => Message::Update(update),
            Err(_) => Message::Invalid,
        }))
    }
    pub fn reply(&self, revision: u64, error: Option<&str>) -> io::Result<()> {
        let bytes = serde_json::to_vec(&serde_json::json!({
            "state":if error.is_some() {"rejected"} else {"applied"},
            "effective_policy_revision":revision,"error":error
        }))?;
        let n = unsafe {
            libc::send(
                self.as_raw_fd(),
                bytes.as_ptr().cast(),
                bytes.len(),
                libc::MSG_DONTWAIT | libc::MSG_NOSIGNAL,
            )
        };
        if n != bytes.len() as isize {
            return Err(io::Error::other("control acknowledgement unavailable"));
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn revision_cas_and_ceiling() {
        let mut u = Update {
            expected_revision: 1,
            revision: 2,
            policy_layers: vec![],
        };
        assert!(u.validate(1, false).is_ok());
        assert_eq!(u.validate(2, false), Err("REVISION_CONFLICT"));
        assert_eq!(u.validate(1, true), Err("INVALID_POLICY"));
        u.expected_revision = u64::MAX;
        u.revision = 0;
        assert_eq!(u.validate(u64::MAX, false), Err("INVALID_REVISION"));
    }
    #[test]
    fn stream_descriptors_and_oversized_messages_are_rejected() {
        use std::os::{fd::IntoRawFd, unix::net::UnixStream};
        let (stream, _peer) = UnixStream::pair().unwrap();
        assert!(unsafe { Control::from_fd(stream.into_raw_fd()) }.is_err());
        let mut fds = [-1; 2];
        assert_eq!(
            unsafe {
                libc::socketpair(
                    libc::AF_UNIX,
                    libc::SOCK_SEQPACKET | libc::SOCK_CLOEXEC,
                    0,
                    fds.as_mut_ptr(),
                )
            },
            0
        );
        let mut control = unsafe { Control::from_fd(fds[0]) }.unwrap();
        let peer = unsafe { OwnedFd::from_raw_fd(fds[1]) };
        let bytes = vec![b'x'; MAX_UPDATE + 1];
        assert_eq!(
            unsafe { libc::send(peer.as_raw_fd(), bytes.as_ptr().cast(), bytes.len(), 0) },
            bytes.len() as isize
        );
        assert!(control.receive().is_err());
    }
    #[test]
    fn seqpacket_is_bounded_and_loss_is_fatal() {
        let mut fds = [-1; 2];
        assert_eq!(
            unsafe {
                libc::socketpair(
                    libc::AF_UNIX,
                    libc::SOCK_SEQPACKET | libc::SOCK_CLOEXEC,
                    0,
                    fds.as_mut_ptr(),
                )
            },
            0
        );
        let mut control = unsafe { Control::from_fd(fds[0]) }.unwrap();
        let peer = unsafe { OwnedFd::from_raw_fd(fds[1]) };
        assert!(control.receive().unwrap().is_none());
        let bytes = b"{\"expected_revision\":1,\"revision\":2,\"policy_layers\":[]}";
        assert_eq!(
            unsafe { libc::send(peer.as_raw_fd(), bytes.as_ptr().cast(), bytes.len(), 0) },
            bytes.len() as isize
        );
        assert!(matches!(
            control.receive().unwrap(),
            Some(Message::Update(_))
        ));
        control.reply(2, None).unwrap();
        let mut buf = [0u8; 1024];
        let n = unsafe { libc::recv(peer.as_raw_fd(), buf.as_mut_ptr().cast(), buf.len(), 0) };
        let v: serde_json::Value = serde_json::from_slice(&buf[..n as usize]).unwrap();
        assert_eq!(v["effective_policy_revision"], 2);
        drop(peer);
        assert!(control.receive().is_err());
    }
}
