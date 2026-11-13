//! Host-compiled numeric transport policy. This deliberately refuses site rules:
//! checking an IP is not TLS/HTTP site mediation. All layers intersect; denies
//! win even over exact private-service exceptions.
use crate::{Config, Prefix, Tuple, public_v4};
use serde::{Deserialize, Serialize};
use std::io;

pub const MAX_LAYERS: usize = 8;
pub const MAX_RULES: usize = 256;
pub const MAX_PORTS: usize = 64;

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum FilterMode {
    Transport,
    StrictSites,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum DefaultEgress {
    Public,
    Deny,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Protocol {
    Tcp,
    Udp,
}
impl Protocol {
    fn number(self) -> u8 {
        match self {
            Self::Tcp => 6,
            Self::Udp => 17,
        }
    }
}

/// Uses public NetworkRule vocabulary. Missing/ambiguous selectors and host
/// patterns are rejected at compilation, never treated as catch-all rules.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Rule {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cidr: Option<Prefix>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub host_pattern: Option<String>,
    pub protocol: Protocol,
    pub ports: Vec<u16>,
}
impl Rule {
    fn validate(&self) -> io::Result<()> {
        if self.host_pattern.is_some()
            || self.cidr.is_none_or(|p| p.1 > 32)
            || self.ports.is_empty()
            || self.ports.len() > MAX_PORTS
            || self.ports.contains(&0)
        {
            return Err(io::Error::other(
                "numeric CIDR, transport and nonzero ports required; site mediation unavailable",
            ));
        }
        Ok(())
    }
    fn matches(&self, tuple: &Tuple) -> bool {
        self.cidr.is_some_and(|p| p.contains(tuple.destination))
            && tuple.protocol == self.protocol.number()
            && self.ports.contains(&tuple.destination_port)
    }
}

/// One principal/recipe/task/sandbox/attachment ceiling. The trusted caller
/// supplies every layer; no guest-supplied labels select or omit ceilings.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Layer {
    pub filter_mode: FilterMode,
    pub default_egress: DefaultEgress,
    #[serde(default)]
    pub allow: Vec<Rule>,
    #[serde(default)]
    pub deny: Vec<Rule>,
}
impl Layer {
    pub fn validate(&self) -> io::Result<()> {
        if self.filter_mode != FilterMode::Transport {
            return Err(io::Error::other(
                "strict_sites requires a qualified TLS/HTTP gateway",
            ));
        }
        if self.allow.len() + self.deny.len() > MAX_RULES {
            return Err(io::Error::other("too many transport rules"));
        }
        for r in self.allow.iter().chain(&self.deny) {
            r.validate()?;
        }
        Ok(())
    }
}

pub fn validate_layers(layers: &[Layer], require_allowlist: bool) -> io::Result<()> {
    if layers.len() > MAX_LAYERS {
        return Err(io::Error::other("too many policy layers"));
    }
    for layer in layers {
        layer.validate()?;
    }
    if require_allowlist
        && !layers
            .iter()
            .any(|p| p.default_egress == DefaultEgress::Deny)
    {
        return Err(io::Error::other(
            "public repository requires a default-deny ceiling",
        ));
    }
    Ok(())
}

/// Both packet forwarding and the numeric HTTP proxy call this exact decision.
/// An exception only lifts the baseline private-range/inventory default, never
/// an immutable safety deny, a rule deny, or a default-deny parent ceiling.
pub fn decision(config: &Config, tuple: &Tuple) -> Result<&'static str, &'static str> {
    if [53, 853].contains(&tuple.destination_port) && !config.excepted(tuple) {
        return Err("external_dns_denied");
    }
    if config
        .safety_denies
        .iter()
        .any(|p| p.contains(tuple.destination))
    {
        return Err("immutable_safety_deny");
    }
    if config
        .policy_ceiling
        .iter()
        .chain(&config.policy_layers)
        .any(|layer| layer.deny.iter().any(|r| r.matches(tuple)))
    {
        return Err("explicit_policy_deny");
    }
    let excepted = config.excepted(tuple);
    if !excepted && !public_v4(tuple.destination, &config.host_denies) {
        return Err("private_lan_metadata_or_host");
    }
    if config
        .policy_ceiling
        .iter()
        .chain(&config.policy_layers)
        .any(|layer| {
            layer.default_egress == DefaultEgress::Deny
                && !layer.allow.iter().any(|r| r.matches(tuple))
        })
    {
        return Err("not_in_allowlist");
    }
    Ok(if excepted {
        "policy_exception"
    } else {
        "public_transport"
    })
}
