//! Namespace-local resolver. Queries and answers pass durable audit; replies are
//! constructed from policy-approved IPv4 addresses, never relayed as raw DNS.
use crate::{Config, Tuple, audit::Spool, policy};
use serde_json::json;
use sha2::{Digest, Sha256};
use std::{
    collections::HashMap,
    io,
    net::{Ipv4Addr, SocketAddr, UdpSocket},
    os::fd::{AsRawFd, RawFd},
    time::{Duration, Instant},
};
struct Query {
    peer: SocketAddr,
    bytes: Vec<u8>,
    end: usize,
    revision: u64,
    since: Instant,
}
pub struct Resolver {
    guest: UdpSocket,
    upstream: UdpSocket,
    pending: HashMap<u16, Query>,
}
fn question(bytes: &[u8]) -> Option<usize> {
    if bytes.len() < 17
        || bytes[2] & 0xf8 != 0
        || bytes[4..10] != [0, 1, 0, 0, 0, 0]
        || u16::from_be_bytes([bytes[10], bytes[11]]) > 1
    {
        return None;
    }
    let mut p = 12;
    loop {
        let n = *bytes.get(p)? as usize;
        p += 1;
        if n == 0 {
            break;
        }
        if n > 63 || p + n > bytes.len() {
            return None;
        }
        p += n;
    }
    if p + 4 > bytes.len() || p > 267 {
        return None;
    }
    Some(p + 4)
}
fn name(bytes: &[u8], p: &mut usize) -> Option<()> {
    loop {
        let n = *bytes.get(*p)?;
        *p += 1;
        if n == 0 {
            return Some(());
        }
        if n & 0xc0 == 0xc0 {
            *p += 1;
            return (*p <= bytes.len()).then_some(());
        }
        if n > 63 {
            return None;
        }
        *p += n as usize;
        if *p > bytes.len() {
            return None;
        }
    }
}
fn allowed(config: &Config, address: Ipv4Addr) -> bool {
    // A name may be useful at any admitted transport/port. The actual dial is
    // independently checked, including revisions and immutable ceilings.
    let mut ports = vec![80, 443];
    for layer in config.policy_ceiling.iter().chain(&config.policy_layers) {
        for rule in layer.allow.iter().chain(&layer.deny) {
            ports.extend(&rule.ports);
        }
    }
    for exception in &config.exceptions {
        ports.push(exception.port);
    }
    if let Some(other) = (1..=u16::MAX).find(|p| !ports.contains(p)) {
        ports.push(other);
    }
    config.subscription_domains.is_none()
        && ports.into_iter().any(|port| {
            [6, 17].into_iter().any(|protocol| {
                policy::decision(
                    config,
                    &Tuple {
                        source: config.guest_ip,
                        destination: address,
                        source_port: 0,
                        destination_port: port,
                        protocol,
                    },
                )
                .is_ok()
            })
        })
}
impl Resolver {
    pub fn bind(config: &Config) -> io::Result<Option<Self>> {
        let Some(proxy) = config.proxy else {
            return Ok(None);
        };
        // Subscription sandboxes use the pre-resolved vendor tunnel exclusively.
        if config.subscription_domains.is_some() {
            return Ok(None);
        }
        let guest = UdpSocket::bind((proxy.address, 53))?;
        let upstream = UdpSocket::bind((Ipv4Addr::UNSPECIFIED, 0))?;
        guest.set_nonblocking(true)?;
        upstream.set_nonblocking(true)?;
        Ok(Some(Self {
            guest,
            upstream,
            pending: HashMap::new(),
        }))
    }
    pub fn fds(&self) -> [RawFd; 2] {
        [self.guest.as_raw_fd(), self.upstream.as_raw_fd()]
    }
    fn reply(&self, query: &Query, addresses: &[Ipv4Addr], refused: bool) -> io::Result<()> {
        let mut bytes = query.bytes[..query.end].to_vec();
        bytes[2] = 0x81;
        bytes[3] = if refused { 0x85 } else { 0x80 };
        bytes[10..12].fill(0);
        bytes[6..8].copy_from_slice(&(addresses.len() as u16).to_be_bytes());
        for address in addresses {
            bytes.extend_from_slice(&[0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 30, 0, 4]);
            bytes.extend_from_slice(&address.octets());
        }
        match self.guest.send_to(&bytes, query.peer) {
            Ok(_) => Ok(()),
            Err(e) if e.kind() == io::ErrorKind::WouldBlock => Ok(()),
            Err(e) => Err(e),
        }
    }
    pub fn tick(&mut self, config: &Config, spool: &mut Spool) -> io::Result<()> {
        self.pending.retain(|_, q| {
            q.since.elapsed() < Duration::from_secs(2)
                && q.revision == config.attribution.policy_revision
        });
        let mut bytes = [0; 4096];
        for _ in 0..32 {
            let (n, peer) = match self.guest.recv_from(&mut bytes) {
                Ok(v) => v,
                Err(e) if e.kind() == io::ErrorKind::WouldBlock => break,
                Err(e) => return Err(e),
            };
            if peer.ip() != config.guest_ip {
                continue;
            }
            let Some(end) = question(&bytes[..n]) else {
                continue;
            };
            let query = Query {
                peer,
                bytes: bytes[..n].to_vec(),
                end,
                revision: config.attribution.policy_revision,
                since: Instant::now(),
            };
            spool.record("host_guard",json!({"type":"dns_query","question_sha256":Sha256::digest(&bytes[12..end]).iter().map(|b| format!("{b:02x}")).collect::<String>(),"qtype":u16::from_be_bytes([bytes[end-4],bytes[end-3]])}))?;
            if bytes[end - 2..end] != [0, 1] || bytes[end - 4..end - 2] != [0, 1] {
                self.reply(&query, &[], bytes[end - 4..end - 2] != [0, 28])?;
                continue;
            }
            if self.pending.len() >= 128 {
                self.reply(&query, &[], true)?;
                continue;
            }
            let mut random = [0; 2];
            if unsafe { libc::getrandom(random.as_mut_ptr().cast(), 2, 0) } != 2 {
                return Err(io::Error::last_os_error());
            }
            let id = u16::from_ne_bytes(random);
            if self.pending.contains_key(&id) {
                continue;
            }
            bytes[..2].copy_from_slice(&id.to_be_bytes());
            if self
                .upstream
                .send_to(&bytes[..n], (Ipv4Addr::new(1, 1, 1, 1), 53))
                .is_ok()
            {
                self.pending.insert(id, query);
            }
        }
        for _ in 0..32 {
            let n = match self.upstream.recv_from(&mut bytes) {
                Ok((n, peer)) if peer == SocketAddr::from((Ipv4Addr::new(1, 1, 1, 1), 53)) => n,
                Ok(_) => continue,
                Err(e) if e.kind() == io::ErrorKind::WouldBlock => break,
                Err(e) => return Err(e),
            };
            if n < 12 {
                continue;
            }
            let id = u16::from_be_bytes([bytes[0], bytes[1]]);
            let Some(query) = self.pending.remove(&id) else {
                continue;
            };
            if n < query.end
                || bytes[2] & 0x80 == 0
                || bytes[12..query.end] != query.bytes[12..query.end]
                || bytes[4..6] != [0, 1]
            {
                continue;
            }
            let mut p = query.end;
            let mut addresses = Vec::new();
            let mut refused = bytes[3] & 0x0f != 0 || bytes[2] & 2 != 0;
            for _ in 0..u16::from_be_bytes([bytes[6], bytes[7]]).min(65) {
                if name(&bytes[..n], &mut p).is_none() || p + 10 > n {
                    refused = true;
                    break;
                }
                let kind = u16::from_be_bytes([bytes[p], bytes[p + 1]]);
                let class = u16::from_be_bytes([bytes[p + 2], bytes[p + 3]]);
                let len = u16::from_be_bytes([bytes[p + 8], bytes[p + 9]]) as usize;
                p += 10;
                if p + len > n {
                    refused = true;
                    break;
                }
                if kind == 1 && class == 1 && len == 4 {
                    let ip = Ipv4Addr::new(bytes[p], bytes[p + 1], bytes[p + 2], bytes[p + 3]);
                    if !allowed(config, ip) {
                        refused = true;
                    } else {
                        addresses.push(ip);
                    }
                }
                p += len;
            }
            refused |= addresses.len() > 64;
            spool.record("host_guard",json!({"type":"dns_answer","decision":if refused {"deny"}else{"allow"},"addresses":addresses}))?;
            self.reply(&query, if refused { &[] } else { &addresses }, refused)?;
        }
        Ok(())
    }
}
