//! Minimal NFQUEUE transport, using the Linux UAPI. No fail-open or bypass mode.
use std::{
    io, mem,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    ptr,
};

pub struct Queue {
    fd: OwnedFd,
    queue: u16,
    seq: u32,
}
pub struct Packet {
    pub id: u32,
    pub hook: u8,
    pub input: u32,
    pub output: u32,
    pub bytes: Vec<u8>,
}
fn align(n: usize) -> usize {
    (n + 3) & !3
}
fn attr(out: &mut Vec<u8>, kind: u16, data: &[u8]) {
    out.extend_from_slice(&((4 + data.len()) as u16).to_ne_bytes());
    out.extend_from_slice(&kind.to_ne_bytes());
    out.extend_from_slice(data);
    out.resize(align(out.len()), 0);
}
impl AsRawFd for Queue {
    fn as_raw_fd(&self) -> std::os::fd::RawFd {
        self.fd.as_raw_fd()
    }
}
impl Queue {
    pub fn bind(queue: u16) -> io::Result<Self> {
        let raw = unsafe {
            libc::socket(
                libc::AF_NETLINK,
                libc::SOCK_RAW | libc::SOCK_CLOEXEC,
                libc::NETLINK_NETFILTER,
            )
        };
        if raw < 0 {
            return Err(io::Error::last_os_error());
        }
        let fd = unsafe { OwnedFd::from_raw_fd(raw) };
        let mut addr: libc::sockaddr_nl = unsafe { mem::zeroed() };
        addr.nl_family = libc::AF_NETLINK as u16;
        if unsafe {
            libc::bind(
                raw,
                ptr::from_ref(&addr).cast(),
                mem::size_of_val(&addr) as _,
            )
        } < 0
        {
            return Err(io::Error::last_os_error());
        }
        let mut q = Self { fd, queue, seq: 0 };
        let mut a = vec![];
        attr(&mut a, 1, &[1, 0, 0, libc::AF_INET as u8]);
        q.config(&a)?;
        let mut a = vec![];
        let mut params = 65535_u32.to_be_bytes().to_vec();
        params.push(2);
        attr(&mut a, 2, &params);
        attr(&mut a, 3, &1024_u32.to_be_bytes());
        q.config(&a)?;
        Ok(q)
    }
    fn send(&mut self, kind: u16, flags: u16, attrs: &[u8]) -> io::Result<()> {
        self.seq = self.seq.wrapping_add(1);
        let mut b = Vec::with_capacity(20 + attrs.len());
        b.extend_from_slice(&((20 + attrs.len()) as u32).to_ne_bytes());
        b.extend_from_slice(&(0x300 | kind).to_ne_bytes());
        b.extend_from_slice(&flags.to_ne_bytes());
        b.extend_from_slice(&self.seq.to_ne_bytes());
        b.extend_from_slice(&0_u32.to_ne_bytes());
        b.extend_from_slice(&[0, 0]);
        b.extend_from_slice(&self.queue.to_be_bytes());
        b.extend_from_slice(attrs);
        let mut addr: libc::sockaddr_nl = unsafe { mem::zeroed() };
        addr.nl_family = libc::AF_NETLINK as u16;
        let n = unsafe {
            libc::sendto(
                self.fd.as_raw_fd(),
                b.as_ptr().cast(),
                b.len(),
                0,
                ptr::from_ref(&addr).cast(),
                mem::size_of_val(&addr) as _,
            )
        };
        if n != b.len() as isize {
            return Err(io::Error::last_os_error());
        }
        Ok(())
    }
    fn config(&mut self, attrs: &[u8]) -> io::Result<()> {
        self.send(2, 1 | 4, attrs)?;
        let b = self.recv()?;
        if b.len() < 20 || u16::from_ne_bytes([b[4], b[5]]) != 2 {
            return Err(io::Error::other("expected netlink ACK"));
        }
        let code = i32::from_ne_bytes(b[16..20].try_into().unwrap());
        if code != 0 {
            return Err(io::Error::from_raw_os_error(-code));
        }
        Ok(())
    }
    fn recv(&self) -> io::Result<Vec<u8>> {
        let mut b = vec![0; 131072];
        let mut addr: libc::sockaddr_nl = unsafe { mem::zeroed() };
        let mut len = mem::size_of_val(&addr) as libc::socklen_t;
        let n = unsafe {
            libc::recvfrom(
                self.fd.as_raw_fd(),
                b.as_mut_ptr().cast(),
                b.len(),
                libc::MSG_TRUNC,
                ptr::from_mut(&mut addr).cast(),
                &mut len,
            )
        };
        if n < 0 {
            return Err(io::Error::last_os_error());
        }
        if n as usize > b.len() || addr.nl_pid != 0 {
            return Err(io::Error::other("truncated or non-kernel netlink datagram"));
        }
        b.truncate(n as usize);
        Ok(b)
    }
    pub fn packets(&self) -> io::Result<Vec<Packet>> {
        decode(&self.recv()?)
    }
    pub fn verdict(&mut self, id: u32, allow: bool) -> io::Result<()> {
        let mut value = (if allow { 1_u32 } else { 0 }).to_be_bytes().to_vec();
        value.extend_from_slice(&id.to_be_bytes());
        let mut a = vec![];
        attr(&mut a, 2, &value);
        self.send(1, 1, &a)
    }
}
fn invalid() -> io::Error {
    io::Error::other("malformed/truncated NFQUEUE message")
}
fn decode(mut b: &[u8]) -> io::Result<Vec<Packet>> {
    let mut out = vec![];
    while !b.is_empty() {
        if b.len() < 20 {
            return Err(invalid());
        }
        let n = u32::from_ne_bytes(b[..4].try_into().unwrap()) as usize;
        if n < 20 || n > b.len() || u16::from_ne_bytes(b[4..6].try_into().unwrap()) != 0x300 {
            return Err(invalid());
        }
        let mut p = Packet {
            id: 0,
            hook: 0,
            input: 0,
            output: 0,
            bytes: vec![],
        };
        let mut header = false;
        let mut full_len = None;
        let mut attrs = &b[20..n];
        while !attrs.is_empty() {
            if attrs.len() < 4 {
                return Err(invalid());
            }
            let len = u16::from_ne_bytes(attrs[..2].try_into().unwrap()) as usize;
            let kind = u16::from_ne_bytes(attrs[2..4].try_into().unwrap()) & 0x3fff;
            if len < 4 || len > attrs.len() || (align(len) > attrs.len() && len != attrs.len()) {
                return Err(invalid());
            }
            let data = &attrs[4..len];
            match kind {
                1 if data.len() >= 7 => {
                    p.id = u32::from_be_bytes(data[..4].try_into().unwrap());
                    p.hook = data[6];
                    header = true;
                }
                5 if data.len() == 4 => p.input = u32::from_be_bytes(data.try_into().unwrap()),
                6 if data.len() == 4 => p.output = u32::from_be_bytes(data.try_into().unwrap()),
                10 => p.bytes = data.to_vec(),
                13 if data.len() == 4 => {
                    full_len = Some(u32::from_be_bytes(data.try_into().unwrap()) as usize)
                }
                _ => (),
            }
            // The kernel may omit trailing alignment bytes on the final attribute.
            attrs = &attrs[align(len).min(attrs.len())..];
        }
        if !header || p.bytes.is_empty() || full_len.is_some_and(|n| n != p.bytes.len()) {
            return Err(invalid());
        }
        out.push(p);
        if align(n) > b.len() && n != b.len() {
            return Err(invalid());
        }
        b = &b[align(n).min(b.len())..];
    }
    Ok(out)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn no_truncated_messages() {
        for n in 1..100 {
            assert!(decode(&vec![0; n]).is_err());
        }
        let mut b = vec![0; 20];
        b[..4].copy_from_slice(&20_u32.to_ne_bytes());
        b[4..6].copy_from_slice(&0x300_u16.to_ne_bytes());
        assert!(decode(&b).is_err());
    }
}
