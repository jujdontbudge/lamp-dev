//! Vendor-only CONNECT proxy. Resolve policy names before entering confinement;
//! the serving loop only dials those pinned public addresses, without threads.
use crate::{Config, public_v4};
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
                    if v4.port() == 443 && public_v4(*v4.ip(), &config.host_denies))
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

#[cfg(test)]
mod tests {
    use super::*;

    pub(super) fn hello(domain: &str) -> Vec<u8> {
        let mut name = vec![0];
        name.extend_from_slice(&(domain.len() as u16).to_be_bytes());
        name.extend_from_slice(domain.as_bytes());
        let mut extension = (name.len() as u16).to_be_bytes().to_vec();
        extension.extend(name);
        let mut extensions = vec![0, 0];
        extensions.extend_from_slice(&(extension.len() as u16).to_be_bytes());
        extensions.extend(extension);
        let mut body = vec![3, 3];
        body.extend_from_slice(&[0; 32]);
        body.extend_from_slice(&[0, 0, 2, 0x13, 1, 1, 0]);
        body.extend_from_slice(&(extensions.len() as u16).to_be_bytes());
        body.extend(extensions);
        let mut result = vec![1, 0, (body.len() >> 8) as u8, body.len() as u8];
        result.extend(body);
        result
    }

    fn config(domains: serde_json::Value) -> Config {
        serde_json::from_value(serde_json::json!({
            "attribution":{"principal_id":"pr_owner","host_id":"hs_local","sandbox_id":"sb_test","policy_revision":1,
                "task_id":"tk_test","generation":1,"session_id":"ss_test","operation_id":"op_test"},
            "interface":"tap0","guest_ip":"10.80.0.2","queue":1,"guard_uid":65536,
            "host_denies":["8.8.8.8/32"],"spool":"/unused","spool_bytes":1048576,
            "proxy":{"address":"10.80.0.1","port":3128},"subscription_domains":domains
        })).unwrap()
    }

    #[test]
    fn resolves_only_policy_names_and_pins_bounded_public_addresses() {
        let config = config(serde_json::json!(["api.anthropic.com"]));
        let mut names = Vec::new();
        let vendors = ResolvedVendors::resolve_with(&config, |domain| {
            names.push(domain.to_owned());
            Ok([
                "127.0.0.1:443",
                "192.168.1.2:443",
                "8.8.8.8:443",
                "[2606:4700::1111]:443",
                "1.1.1.1:80",
                "1.1.1.1:443",
                "1.1.1.1:443",
            ]
            .into_iter()
            .map(|address| address.parse().unwrap())
            .collect())
        })
        .unwrap()
        .unwrap();
        assert_eq!(names, ["api.anthropic.com"]);
        assert_eq!(
            vendors.addresses["api.anthropic.com"],
            ["1.1.1.1:443".parse().unwrap()]
        );
    }

    #[test]
    fn resolution_failure_or_only_denied_answers_fails_closed() {
        let config = config(serde_json::json!(["api.anthropic.com"]));
        assert!(
            ResolvedVendors::resolve_with(&config, |_| Err(io::Error::other("DNS unavailable")))
                .is_err()
        );
        assert!(
            ResolvedVendors::resolve_with(&config, |_| Ok(vec!["127.0.0.1:443".parse().unwrap()]))
                .is_err()
        );
        assert!(ResolvedVendors::resolve_with(&config, |_| Ok(Vec::new())).is_err());
        let vendors = ResolvedVendors::resolve_with(&config, |_| {
            Ok((1..=20)
                .map(|last| format!("1.1.1.{last}:443").parse().unwrap())
                .collect())
        })
        .unwrap()
        .unwrap();
        assert_eq!(vendors.addresses["api.anthropic.com"].len(), 8);
    }

    #[test]
    fn ordinary_or_invalid_policy_never_invokes_dns() {
        let ordinary = config(serde_json::Value::Null);
        assert!(
            ResolvedVendors::resolve_with(&ordinary, |_| panic!("ordinary proxy resolved DNS"))
                .unwrap()
                .is_none()
        );
        let invalid = config(serde_json::json!(["127.0.0.1"]));
        assert!(
            ResolvedVendors::resolve_with(&invalid, |_| panic!("invalid policy resolved DNS"))
                .is_err()
        );
    }

    #[test]
    fn tls_requires_exact_visible_vendor_sni_and_complete_hello() {
        let bytes = hello("api.anthropic.com");
        assert!(client_hello(&bytes, "api.anthropic.com"));
        assert!(!client_hello(&bytes, "chatgpt.com"));
        for size in 0..bytes.len() {
            assert!(!client_hello(&bytes[..size], "api.anthropic.com"));
        }
        assert!(!client_hello(
            &hello("api.anthropic.com.evil"),
            "api.anthropic.com"
        ));
    }

    #[test]
    fn no_guest_selected_destination_headers_or_extra_authority_bytes() {
        let domains = vec!["api.anthropic.com".to_string()];
        assert_eq!(
            authority(
                "CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\n\r\n",
                &domains
            ),
            Some("api.anthropic.com")
        );
        for target in [
            "api.anthropic.com.evil",
            "api.anthropic.com@evil",
            "1.1.1.1",
            "chatgpt.com",
        ] {
            assert!(
                authority(
                    &format!("CONNECT {target}:443 HTTP/1.1\r\nHost: {target}:443\r\n\r\n"),
                    &domains
                )
                .is_none()
            );
        }
        assert!(authority("CONNECT api.anthropic.com:443 HTTP/1.1\r\nHost: api.anthropic.com:443\r\nX-Exfil: canary\r\n\r\n", &domains).is_none());
        assert!(!client_hello(
            b"GET / HTTP/1.1\r\nHost: other.example\r\n\r\n",
            "api.anthropic.com"
        ));
    }
}
