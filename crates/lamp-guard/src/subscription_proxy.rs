//! Vendor-only CONNECT proxy. Resolve policy names before entering confinement;
//! the serving loop only dials those pinned public addresses, without threads.
use crate::{Config, Tuple, policy, public_v4};
use std::collections::BTreeMap;
use std::io;
use std::net::{SocketAddr, TcpListener, ToSocketAddrs};

#[path = "subscription_tunnels.rs"]
mod tunnels;

pub struct ResolvedVendors {
    addresses: BTreeMap<String, Vec<SocketAddr>>,
}

impl ResolvedVendors {
    /// Must run before the proxy drops privileges and installs seccomp. The
    /// result is private runtime state, never supplied by the guest or JSON.
    pub fn prepare(config: &Config) -> io::Result<Option<Self>> {
        Self::resolve_with(config, |domain| {
            Ok((domain, 443).to_socket_addrs()?.take(64).collect())
        })
    }

    fn resolve_with(
        config: &Config,
        mut resolve: impl FnMut(&str) -> io::Result<Vec<SocketAddr>>,
    ) -> io::Result<Option<Self>> {
        config.validate()?;
        let Some(domains) = &config.subscription_domains else {
            return Ok(None);
        };
        let mut addresses = BTreeMap::new();
        for domain in domains {
            let mut approved: Vec<_> = resolve(domain)?
                .into_iter()
                .filter(|address| {
                    matches!(address, SocketAddr::V4(v4)
                    if v4.port() == 443
                        && public_v4(*v4.ip(), &config.host_denies)
                        && policy::decision(config, &Tuple {
                            source: config.guest_ip,
                            destination: *v4.ip(),
                            source_port: 0,
                            destination_port: v4.port(),
                            protocol: 6,
                        }).is_ok())
                })
                .take(8)
                .collect();
            approved.sort_unstable();
            approved.dedup();
            if approved.is_empty() {
                return Err(io::Error::other(
                    "vendor has no approved public IPv4 address",
                ));
            }
            addresses.insert(domain.clone(), approved);
        }
        Ok(Some(Self { addresses }))
    }

    pub fn serve(
        &self,
        listener: TcpListener,
        mut record: impl FnMut(serde_json::Value) -> io::Result<()>,
    ) -> io::Result<()> {
        let mut reactor = tunnels::Reactor::new(listener, self)?;
        loop {
            reactor.tick(&mut record)?;
        }
    }
}

fn take<'a>(bytes: &mut &'a [u8], count: usize) -> Option<&'a [u8]> {
    if count > bytes.len() {
        return None;
    }
    let (head, tail) = bytes.split_at(count);
    *bytes = tail;
    Some(head)
}

fn word(bytes: &mut &[u8]) -> Option<usize> {
    Some(u16::from_be_bytes(take(bytes, 2)?.try_into().ok()?) as usize)
}

pub fn client_hello(body: &[u8], domain: &str) -> bool {
    let mut bytes = body;
    let Some(header) = take(&mut bytes, 4) else {
        return false;
    };
    if header[0] != 1
        || u32::from_be_bytes([0, header[1], header[2], header[3]]) as usize != bytes.len()
    {
        return false;
    }
    let parsed = (|| -> Option<()> {
        take(&mut bytes, 34)?;
        let session = *take(&mut bytes, 1)?.first()? as usize;
        take(&mut bytes, session)?;
        let ciphers = word(&mut bytes)?;
        if ciphers == 0 || ciphers % 2 != 0 {
            return None;
        }
        take(&mut bytes, ciphers)?;
        let compression = *take(&mut bytes, 1)?.first()? as usize;
        if compression == 0 {
            return None;
        }
        take(&mut bytes, compression)?;
        let length = word(&mut bytes)?;
        if length != bytes.len() {
            return None;
        }
        let mut found = false;
        while !bytes.is_empty() {
            let kind = word(&mut bytes)?;
            let length = word(&mut bytes)?;
            let mut value = take(&mut bytes, length)?;
            if kind == 0xfe0d {
                return None;
            }
            if kind == 0 {
                if found || word(&mut value)? != value.len() || take(&mut value, 1)? != [0] {
                    return None;
                }
                let length = word(&mut value)?;
                if take(&mut value, length)? != domain.as_bytes() || !value.is_empty() {
                    return None;
                }
                found = true;
            }
        }
        found.then_some(())
    })();
    parsed.is_some()
}

pub fn authority<'a>(head: &str, domains: &'a [String]) -> Option<&'a str> {
    let mut lines = head.split("\r\n");
    let line = lines.next()?;
    let target = line.strip_prefix("CONNECT ")?.strip_suffix(" HTTP/1.1")?;
    let domain = target.strip_suffix(":443")?;
    let approved = domains
        .iter()
        .find(|approved| approved.as_str() == domain)?;
    let mut host = false;
    for line in lines {
        if line.is_empty() {
            continue;
        }
        let (name, value) = line.split_once(':')?;
        if name.eq_ignore_ascii_case("host") {
            if host || value.trim() != target {
                return None;
            }
            host = true;
        } else if !name.eq_ignore_ascii_case("proxy-connection") {
            return None;
        }
    }
    host.then_some(approved.as_str())
}
