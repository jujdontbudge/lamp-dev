//! Minimal device-mapper control client over `/dev/mapper/control` ioctls
//! (`linux/dm-ioctl.h`, interface 4.x). It is used on the fork path, so it
//! avoids `dmsetup` and udev synchronisation: a thin snapshot is a few
//! ioctls. Device nodes are made by the caller from the returned `dev_t`.
use crate::error::{EngineError, Result};
use std::fs::{File, OpenOptions};
use std::os::fd::AsRawFd;

const HEADER: usize = 312; // sizeof(struct dm_ioctl)
const SPEC: usize = 40; // sizeof(struct dm_target_spec)
const NAME_LEN: usize = 128;

const DEV_CREATE: u8 = 3;
const DEV_REMOVE: u8 = 4;
const DEV_SUSPEND: u8 = 6;
const DEV_STATUS: u8 = 7;
const TABLE_LOAD: u8 = 9;
const TABLE_STATUS: u8 = 12;
const LIST_DEVICES: u8 = 2;
const TARGET_MSG: u8 = 14;

const SUSPEND_FLAG: u32 = 1 << 1;
const BUFFER_FULL_FLAG: u32 = 1 << 8;
const SKIP_LOCKFS_FLAG: u32 = 1 << 10;

fn request(nr: u8) -> libc::c_ulong {
    // _IOWR(0xfd, nr, struct dm_ioctl)
    (3 << 30) | ((HEADER as libc::c_ulong) << 16) | (0xfd << 8) | nr as libc::c_ulong
}

/// A device-mapper device number, as `major:minor`.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct DevT {
    pub major: u32,
    pub minor: u32,
}

impl DevT {
    fn decode(dev: u64) -> DevT {
        // new_encode_dev / huge_encode_dev
        DevT {
            major: ((dev & 0xfff00) >> 8) as u32,
            minor: ((dev & 0xff) | ((dev >> 12) & 0xfff00)) as u32,
        }
    }
    pub fn raw(self) -> libc::dev_t {
        libc::makedev(self.major, self.minor)
    }
    pub fn spec(self) -> String {
        format!("{}:{}", self.major, self.minor)
    }
}

pub struct Control {
    file: File,
}

struct Reply {
    buf: Vec<u8>,
}

impl Reply {
    fn u32_at(&self, off: usize) -> u32 {
        u32::from_ne_bytes(self.buf[off..off + 4].try_into().unwrap())
    }
    fn u64_at(&self, off: usize) -> u64 {
        u64::from_ne_bytes(self.buf[off..off + 8].try_into().unwrap())
    }
    fn dev(&self) -> DevT {
        DevT::decode(self.u64_at(40))
    }
    fn data_start(&self) -> usize {
        self.u32_at(16) as usize
    }
    fn data_end(&self) -> usize {
        (self.u32_at(12) as usize).min(self.buf.len())
    }
    fn target_count(&self) -> u32 {
        self.u32_at(20)
    }
    fn flags(&self) -> u32 {
        self.u32_at(28)
    }
}

impl Control {
    pub fn open() -> Result<Control> {
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .open("/dev/mapper/control")
            .map_err(|e| EngineError::host(format!("open /dev/mapper/control: {e}")))?;
        Ok(Control { file })
    }

    fn call(&self, nr: u8, name: &str, flags: u32, payload: &[u8], out: usize) -> Result<Reply> {
        if name.len() >= NAME_LEN {
            return Err(EngineError::invalid("device-mapper name too long"));
        }
        let mut size = (HEADER + payload.len()).max(HEADER + out);
        size = size.div_ceil(8) * 8;
        loop {
            let mut buf = vec![0_u8; size];
            buf[0..4].copy_from_slice(&4_u32.to_ne_bytes());
            buf[12..16].copy_from_slice(&(size as u32).to_ne_bytes());
            buf[16..20].copy_from_slice(&(HEADER as u32).to_ne_bytes());
            buf[28..32].copy_from_slice(&flags.to_ne_bytes());
            buf[48..48 + name.len()].copy_from_slice(name.as_bytes());
            if nr == TABLE_LOAD {
                buf[20..24].copy_from_slice(&1_u32.to_ne_bytes());
            }
            buf[HEADER..HEADER + payload.len()].copy_from_slice(payload);
            // SAFETY: buf is a valid, writable dm_ioctl of `size` bytes.
            let rc =
                unsafe { libc::ioctl(self.file.as_raw_fd(), request(nr) as _, buf.as_mut_ptr()) };
            if rc != 0 {
                let e = std::io::Error::last_os_error();
                return Err(EngineError {
                    code: if e.raw_os_error() == Some(libc::ENXIO) {
                        "NOT_FOUND"
                    } else {
                        "HOST_UNAVAILABLE"
                    },
                    message: format!("device-mapper {nr} {name}: {e}"),
                    retryable: false,
                });
            }
            let reply = Reply { buf };
            if reply.flags() & BUFFER_FULL_FLAG != 0 && size < 1 << 22 {
                size *= 4;
                continue;
            }
            return Ok(reply);
        }
    }

    /// Creates `name` with a single-target table and activates it.
    pub fn create(&self, name: &str, sectors: u64, target: &str, params: &str) -> Result<DevT> {
        let dev = self.call(DEV_CREATE, name, 0, &[], 0)?.dev();
        let load = (|| {
            self.load(name, sectors, target, params)?;
            self.resume(name)
        })();
        if let Err(e) = load {
            let _ = self.remove(name);
            return Err(e);
        }
        Ok(dev)
    }

    pub fn load(&self, name: &str, sectors: u64, target: &str, params: &str) -> Result<()> {
        if target.len() >= 16 {
            return Err(EngineError::invalid("target type too long"));
        }
        let mut spec = vec![0_u8; SPEC];
        spec[8..16].copy_from_slice(&sectors.to_ne_bytes());
        spec[24..24 + target.len()].copy_from_slice(target.as_bytes());
        spec.extend_from_slice(params.as_bytes());
        spec.push(0);
        while !spec.len().is_multiple_of(8) {
            spec.push(0);
        }
        let next = spec.len() as u32;
        spec[20..24].copy_from_slice(&next.to_ne_bytes());
        self.call(TABLE_LOAD, name, 0, &spec, 0).map(|_| ())
    }

    pub fn resume(&self, name: &str) -> Result<()> {
        self.call(DEV_SUSPEND, name, 0, &[], 0).map(|_| ())
    }

    /// Suspends `name`, waiting for in-flight I/O. No filesystem is frozen
    /// (the device carries a guest's disk); callers flush the page cache
    /// with `fsync` on the device first.
    pub fn suspend(&self, name: &str) -> Result<()> {
        self.call(DEV_SUSPEND, name, SUSPEND_FLAG | SKIP_LOCKFS_FLAG, &[], 0)
            .map(|_| ())
    }

    pub fn remove(&self, name: &str) -> Result<()> {
        // A just-closed device can still be briefly open (udev probing).
        let mut tries = 0;
        loop {
            match self.call(DEV_REMOVE, name, 0, &[], 0) {
                Ok(_) => return Ok(()),
                Err(e) if e.code == "NOT_FOUND" => return Ok(()),
                Err(e) if tries < 200 && e.message.contains("busy") => {
                    tries += 1;
                    std::thread::sleep(std::time::Duration::from_millis(5));
                    let _ = e;
                }
                Err(e) => return Err(e),
            }
        }
    }

    pub fn exists(&self, name: &str) -> Option<DevT> {
        self.call(DEV_STATUS, name, 0, &[], 0).ok().map(|r| r.dev())
    }

    pub fn message(&self, name: &str, msg: &str) -> Result<()> {
        let mut payload = vec![0_u8; 8];
        payload.extend_from_slice(msg.as_bytes());
        payload.push(0);
        self.call(TARGET_MSG, name, 0, &payload, 0)
            .map(|_| ())
            .map_err(|e| e.context(msg))
    }

    /// The status line of the device's first target.
    pub fn status(&self, name: &str) -> Result<String> {
        let r = self.call(TABLE_STATUS, name, 0, &[], 4096)?;
        if r.target_count() == 0 {
            return Ok(String::new());
        }
        let at = r.data_start() + SPEC;
        let end = r.data_end();
        let text = &r.buf[at.min(end)..end];
        let n = text.iter().position(|&b| b == 0).unwrap_or(text.len());
        Ok(String::from_utf8_lossy(&text[..n]).into_owned())
    }

    /// Names of all device-mapper devices.
    pub fn list(&self) -> Result<Vec<String>> {
        let r = self.call(LIST_DEVICES, "", 0, &[], 16384)?;
        let mut names = Vec::new();
        let mut at = r.data_start();
        let end = r.data_end();
        loop {
            if at + 12 > end {
                break;
            }
            let dev = r.u64_at(at);
            let next = r.u32_at(at + 8) as usize;
            let name = &r.buf[at + 12..end];
            let n = name.iter().position(|&b| b == 0).unwrap_or(0);
            if dev != 0 || n != 0 {
                names.push(String::from_utf8_lossy(&name[..n]).into_owned());
            }
            if next == 0 {
                break;
            }
            at += next;
        }
        Ok(names)
    }
}
