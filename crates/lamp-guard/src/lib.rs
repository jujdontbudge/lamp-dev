//! Linux demo guard. The supervisor owns a private network namespace and TAP;
//! tasks never receive this namespace or a host socket. See docs/development/T4-guard.md.
use serde::{Deserialize, Serialize};
use std::{io, net::Ipv4Addr};

#[cfg(target_os = "linux")]
pub mod audit;
#[cfg(target_os = "linux")]
pub mod confinement;
#[cfg(target_os = "linux")]
pub mod netlink;
#[cfg(target_os = "linux")]
pub mod process;
#[cfg(target_os = "linux")]
pub mod runtime;

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Attribution {
    pub principal_id: String,
    pub host_id: String,
    pub task_id: String,
    pub sandbox_id: String,
    pub generation: u64,
    pub session_id: String,
    pub operation_id: String,
    pub policy_revision: u64,
}
impl Attribution {
    pub fn validate(&self) -> io::Result<()> {
        if [
            &self.principal_id,
            &self.host_id,
            &self.task_id,
            &self.sandbox_id,
            &self.session_id,
            &self.operation_id,
        ]
        .iter()
        .any(|v| v.is_empty() || v.len() > 128)
            || self.generation == 0
            || self.policy_revision == 0
        {
            return Err(io::Error::other("missing/oversized audit attribution"));
        }
        Ok(())
    }
}

/// Conservative default IPv4 safety ceiling, reviewed 2026-11-09. Not a site filter.
/// IANA special-purpose, multicast/reserved, CGNAT (including Tailscale), and Azure VIP.
/// IPv6 is deliberately denied in the demo, including mapped IPv4 and extension headers.
pub const DENIED_V4: &[(&str, u8)] = &[
    ("0.0.0.0", 8),
    ("10.0.0.0", 8),
    ("100.64.0.0", 10),
    ("127.0.0.0", 8),
    ("169.254.0.0", 16),
    ("172.16.0.0", 12),
    ("192.0.0.0", 24),
    ("192.0.2.0", 24),
    ("192.88.99.0", 24),
    ("192.168.0.0", 16),
    ("198.18.0.0", 15),
    ("198.51.100.0", 24),
    ("203.0.113.0", 24),
    ("224.0.0.0", 4),
    ("240.0.0.0", 4),
    ("168.63.129.16", 32),
];

#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(try_from = "String", into = "String")]
pub struct Prefix(pub Ipv4Addr, pub u8);
impl TryFrom<String> for Prefix {
    type Error = String;
    fn try_from(s: String) -> Result<Self, String> {
        let (ip, bits) = s.split_once('/').ok_or("CIDR required")?;
        let ip = ip.parse().map_err(|_| "invalid IPv4")?;
        let bits = bits.parse::<u8>().map_err(|_| "invalid prefix")?;
        if bits > 32 {
            return Err("invalid prefix".into());
        }
        Ok(Self(ip, bits))
    }
}
impl From<Prefix> for String {
    fn from(p: Prefix) -> Self {
        format!("{}/{}", p.0, p.1)
    }
}
impl Prefix {
    pub fn contains(self, ip: Ipv4Addr) -> bool {
        let mask = u32::MAX.checked_shl(32 - self.1 as u32).unwrap_or(0);
        u32::from(ip) & mask == u32::from(self.0) & mask
    }
}
pub fn public_v4(ip: Ipv4Addr, extra_denies: &[Prefix]) -> bool {
    !DENIED_V4
        .iter()
        .any(|(net, bits)| Prefix(net.parse().unwrap(), *bits).contains(ip))
        && !extra_denies.iter().any(|p| p.contains(ip))
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub attribution: Attribution,
    pub interface: String,
    pub guest_ip: Ipv4Addr,
    pub queue: u16,
    /// Unique host service UID/GID, distinct from all task and owner identities.
    pub guard_uid: u32,
    /// Supervisor supplies *all* host addresses, on-link routes and fleet/control ranges,
    /// including publicly numbered LANs. Empty lists are rejected.
    pub host_denies: Vec<Prefix>,
    pub spool: String,
    pub spool_bytes: u64,
}
impl Config {
    pub fn validate(&self) -> io::Result<()> {
        self.attribution.validate()?;
        if self.interface.is_empty()
            || self.interface.len() > 15
            || !self
                .interface
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-')
            || self.host_denies.is_empty()
            || self.spool_bytes < 4096
            || self.guard_uid < 65536
            || self.guard_uid == u32::MAX
        {
            return Err(io::Error::other(
                "invalid interface, missing host denies or insufficient spool",
            ));
        }
        Ok(())
    }
    /// A table per private guard namespace. No global flush and no queue-bypass flag.
    /// Queue on every packet: losing audit/guard drops even established connections.
    pub fn nft(&self) -> io::Result<String> {
        self.validate()?;
        Ok(format!(
            "create table inet lamp_guard {{\n chain ingress {{ type filter hook input priority -310; policy accept; iifname \"{i}\" queue num {q}; }}\n chain transit {{ type filter hook forward priority -310; policy accept; iifname \"{i}\" queue num {q}; oifname \"{i}\" queue num {q}; }}\n chain local_out {{ type filter hook output priority -310; policy accept; oifname \"{i}\" drop; }}\n}}\n",
            i = self.interface,
            q = self.queue
        ))
    }
}

#[derive(Clone, Debug, Hash, Eq, PartialEq, Serialize)]
pub struct Tuple {
    pub source: Ipv4Addr,
    pub destination: Ipv4Addr,
    pub source_port: u16,
    pub destination_port: u16,
    pub protocol: u8,
}
impl Tuple {
    pub fn reverse(&self) -> Self {
        Self {
            source: self.destination,
            destination: self.source,
            source_port: self.destination_port,
            destination_port: self.source_port,
            protocol: self.protocol,
        }
    }
}
/// No fragments, IP options, IPv6, ICMP or non-TCP/UDP. Parse lengths before reading fields.
/// Packet bytes never enter audit; argv and packet payload can contain secrets.
pub fn packet(bytes: &[u8]) -> Option<Tuple> {
    if bytes.len() < 20 || bytes[0] != 0x45 {
        return None;
    }
    let total = u16::from_be_bytes([bytes[2], bytes[3]]) as usize;
    let protocol = bytes[9];
    if total != bytes.len() || u16::from_be_bytes([bytes[6], bytes[7]]) & 0x3fff != 0 {
        return None;
    }
    let min = match protocol {
        6 => 40,
        17 => 28,
        _ => return None,
    };
    if total < min {
        return None;
    }
    if protocol == 6 {
        let end = 20 + ((bytes[32] >> 4) as usize) * 4;
        if end < 40 || end > total {
            return None;
        }
    } else if u16::from_be_bytes([bytes[24], bytes[25]]) as usize != total - 20 {
        return None;
    }
    Some(Tuple {
        source: Ipv4Addr::new(bytes[12], bytes[13], bytes[14], bytes[15]),
        destination: Ipv4Addr::new(bytes[16], bytes[17], bytes[18], bytes[19]),
        source_port: u16::from_be_bytes([bytes[20], bytes[21]]),
        destination_port: u16::from_be_bytes([bytes[22], bytes[23]]),
        protocol,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn default_ceiling() {
        for ip in [
            "0.1.1.1",
            "10.2.3.4",
            "100.100.100.200",
            "100.64.0.1",
            "127.0.0.1",
            "169.254.169.254",
            "172.31.255.255",
            "192.168.1.1",
            "198.19.1.1",
            "224.0.0.1",
            "255.255.255.255",
            "168.63.129.16",
        ] {
            assert!(!public_v4(ip.parse().unwrap(), &[]), "{ip}");
        }
        for ip in ["1.1.1.1", "8.8.8.8", "172.32.0.1"] {
            assert!(public_v4(ip.parse().unwrap(), &[]));
        }
        assert!(!public_v4(
            "8.8.8.8".parse().unwrap(),
            &[Prefix("8.8.8.0".parse().unwrap(), 24)]
        ));
        assert!(Prefix("0.0.0.0".parse().unwrap(), 0).contains("8.8.8.8".parse().unwrap()));
    }
    #[test]
    fn malformed_and_encapsulated_packets() {
        let mut p = vec![0; 28];
        p[0] = 0x45;
        p[3] = 28;
        p[9] = 17;
        p[25] = 8;
        assert!(packet(&p).is_some());
        for i in 0..p.len() {
            assert!(packet(&p[..i]).is_none());
        }
        p[6] = 0x20;
        assert!(packet(&p).is_none());
        p[6] = 0;
        p[0] = 0x60;
        assert!(packet(&p).is_none());
        p[0] = 0x46;
        assert!(packet(&p).is_none());
    }
}
