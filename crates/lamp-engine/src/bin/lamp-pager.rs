//! Per-sandbox page server for Firecracker's UFFD memory backend.
//!
//! Started by the supervisor with the sandbox's memory layers as inherited
//! descriptors 3..3+N (bottom first). It binds the jail's UFFD socket, drops
//! to the sandbox's UID, receives the userfaultfd from Firecracker and fills
//! each missing guest page from the topmost layer holding it.
//!
//! `lamp-pager SOCKET UID LAYER_COUNT`
#[cfg(target_os = "linux")]
fn main() {
    if let Err(e) = linux::run() {
        eprintln!("lamp-pager: {e}");
        std::process::exit(1);
    }
}

#[cfg(not(target_os = "linux"))]
fn main() {
    eprintln!("lamp-pager runs only on Linux");
    std::process::exit(2);
}

#[cfg(target_os = "linux")]
mod linux {
    use std::io::{self, Write};
    use std::os::fd::{AsRawFd, FromRawFd, OwnedFd, RawFd};
    use std::os::unix::net::UnixListener;

    const PAGE: u64 = 4096;
    const UFFD_EVENT_PAGEFAULT: u8 = 0x12;
    const UFFDIO_COPY: u64 = 0xC028_AA03;

    #[repr(C)]
    struct UffdioCopy {
        dst: u64,
        src: u64,
        len: u64,
        mode: u64,
        copy: i64,
    }

    #[derive(serde::Deserialize)]
    struct Region {
        base_host_virt_addr: u64,
        size: u64,
        offset: u64,
        page_size: u64,
    }

    struct Layer {
        fd: RawFd,
        base: *const u8,
        len: u64,
        complete: bool,
    }

    impl Layer {
        fn has(&self, off: u64) -> bool {
            if off >= self.len {
                return false;
            }
            if self.complete {
                return true;
            }
            // SAFETY: lseek on an inherited descriptor.
            let d = unsafe { libc::lseek(self.fd, off as i64, libc::SEEK_DATA) };
            d >= 0 && (d as u64) < off + PAGE
        }
    }

    fn os(what: &str) -> io::Error {
        let e = io::Error::last_os_error();
        io::Error::new(e.kind(), format!("{what}: {e}"))
    }

    fn recv_uffd(sock: RawFd) -> io::Result<(Vec<Region>, OwnedFd)> {
        let mut buf = vec![0_u8; 16384];
        let mut control = [0_u8; 64];
        let mut iov = libc::iovec {
            iov_base: buf.as_mut_ptr().cast(),
            iov_len: buf.len(),
        };
        // SAFETY: msghdr refers to live buffers; the received fd is wrapped.
        let (n, fd) = unsafe {
            let mut msg: libc::msghdr = std::mem::zeroed();
            msg.msg_iov = &mut iov;
            msg.msg_iovlen = 1;
            msg.msg_control = control.as_mut_ptr().cast();
            msg.msg_controllen = control.len();
            let n = libc::recvmsg(sock, &mut msg, libc::MSG_CMSG_CLOEXEC);
            if n < 0 {
                return Err(os("recvmsg"));
            }
            let cmsg = libc::CMSG_FIRSTHDR(&msg);
            if cmsg.is_null() || (*cmsg).cmsg_type != libc::SCM_RIGHTS {
                return Err(io::Error::other("no userfaultfd received"));
            }
            let fd = std::ptr::read_unaligned(libc::CMSG_DATA(cmsg) as *const RawFd);
            (n as usize, OwnedFd::from_raw_fd(fd))
        };
        let regions: Vec<Region> = serde_json::from_slice(&buf[..n]).map_err(io::Error::other)?;
        if regions.is_empty() || regions.iter().any(|r| r.page_size != PAGE) {
            return Err(io::Error::other("unsupported guest memory regions"));
        }
        Ok((regions, fd))
    }

    fn drop_privileges(uid: u32) -> io::Result<()> {
        // SAFETY: plain credential syscalls.
        unsafe {
            libc::prctl(libc::PR_SET_DUMPABLE, 0, 0, 0, 0);
            if libc::setgroups(0, std::ptr::null()) != 0
                || libc::setgid(uid) != 0
                || libc::setuid(uid) != 0
            {
                return Err(os("drop privileges"));
            }
            libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0);
        }
        Ok(())
    }

    pub fn run() -> io::Result<()> {
        let args: Vec<String> = std::env::args().collect();
        if args.len() != 4 {
            return Err(io::Error::other("usage: lamp-pager SOCKET UID LAYER_COUNT"));
        }
        let socket = &args[1];
        let uid: u32 = args[2].parse().map_err(io::Error::other)?;
        let count: i32 = args[3].parse().map_err(io::Error::other)?;
        let mut layers = Vec::new();
        for i in 0..count {
            let fd = 3 + i;
            // SAFETY: fstat/mmap on inherited, read-only layer descriptors.
            unsafe {
                let mut st: libc::stat = std::mem::zeroed();
                if libc::fstat(fd, &mut st) != 0 {
                    return Err(os("fstat layer"));
                }
                let len = st.st_size as u64;
                let base = libc::mmap(
                    std::ptr::null_mut(),
                    len as usize,
                    libc::PROT_READ,
                    libc::MAP_SHARED,
                    fd,
                    0,
                );
                if base == libc::MAP_FAILED {
                    return Err(os("mmap layer"));
                }
                layers.push(Layer {
                    fd,
                    base: base.cast(),
                    len,
                    complete: i == 0,
                });
            }
        }
        let _ = std::fs::remove_file(socket);
        let listener = UnixListener::bind(socket)?;
        let c = std::ffi::CString::new(socket.as_str()).unwrap();
        // SAFETY: valid path.
        unsafe {
            libc::chown(c.as_ptr(), uid, uid);
            libc::chmod(c.as_ptr(), 0o600);
        }
        drop_privileges(uid)?;
        println!("ready");
        io::stdout().flush()?;
        let (stream, _) = listener.accept()?;
        drop(listener);
        let (regions, uffd) = recv_uffd(stream.as_raw_fd())?;
        serve(&layers, &regions, uffd.as_raw_fd(), stream.as_raw_fd())
    }

    fn serve(layers: &[Layer], regions: &[Region], uffd: RawFd, stream: RawFd) -> io::Result<()> {
        let mut msgs = [0_u8; 32 * 64];
        loop {
            let mut fds = [
                libc::pollfd {
                    fd: uffd,
                    events: libc::POLLIN,
                    revents: 0,
                },
                libc::pollfd {
                    fd: stream,
                    events: libc::POLLRDHUP,
                    revents: 0,
                },
            ];
            // SAFETY: valid pollfd array.
            let n = unsafe { libc::poll(fds.as_mut_ptr(), 2, -1) };
            if n < 0 {
                if io::Error::last_os_error().kind() == io::ErrorKind::Interrupted {
                    continue;
                }
                return Err(os("poll"));
            }
            if fds[0].revents & (libc::POLLHUP | libc::POLLERR) != 0
                || fds[1].revents & (libc::POLLHUP | libc::POLLRDHUP) != 0
            {
                return Ok(()); // Firecracker is gone.
            }
            if fds[0].revents & libc::POLLIN == 0 {
                continue;
            }
            // SAFETY: reads whole uffd_msg records into our buffer.
            let got = unsafe { libc::read(uffd, msgs.as_mut_ptr().cast(), msgs.len()) };
            if got < 0 {
                let e = io::Error::last_os_error();
                if matches!(e.raw_os_error(), Some(libc::EAGAIN) | Some(libc::EINTR)) {
                    continue;
                }
                return Err(e);
            }
            for m in msgs[..got as usize].as_chunks::<32>().0 {
                if m[0] != UFFD_EVENT_PAGEFAULT {
                    continue;
                }
                let addr = u64::from_ne_bytes(m[16..24].try_into().unwrap()) & !(PAGE - 1);
                fill(layers, regions, uffd, addr)?;
            }
        }
    }

    fn fill(layers: &[Layer], regions: &[Region], uffd: RawFd, addr: u64) -> io::Result<()> {
        let region = regions
            .iter()
            .find(|r| addr >= r.base_host_virt_addr && addr < r.base_host_virt_addr + r.size)
            .ok_or_else(|| io::Error::other(format!("fault outside guest memory at {addr:#x}")))?;
        let off = region.offset + (addr - region.base_host_virt_addr);
        let layer = layers
            .iter()
            .rev()
            .find(|l| l.has(off))
            .ok_or_else(|| io::Error::other("page not in any layer"))?;
        let mut copy = UffdioCopy {
            dst: addr,
            // SAFETY: off < layer.len (checked by has()).
            src: unsafe { layer.base.add(off as usize) } as u64,
            len: PAGE,
            mode: 0,
            copy: 0,
        };
        loop {
            // SAFETY: UFFDIO_COPY with a valid struct.
            let rc = unsafe { libc::ioctl(uffd, UFFDIO_COPY as libc::Ioctl, &mut copy) };
            if rc == 0 {
                return Ok(());
            }
            match io::Error::last_os_error().raw_os_error() {
                Some(libc::EEXIST) => return Ok(()),
                Some(libc::EAGAIN) => {
                    copy.copy = 0;
                    continue;
                }
                _ => return Err(os("UFFDIO_COPY")),
            }
        }
    }
}
