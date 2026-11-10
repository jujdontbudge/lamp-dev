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

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn config(layers: serde_json::Value) -> Config {
        serde_json::from_value(json!({
            "attribution":{"principal_id":"p","host_id":"h","task_id":"t","sandbox_id":"s",
                "generation":1,"session_id":"ss","operation_id":"o","policy_revision":1},
            "interface":"task0","guest_ip":"198.18.240.2","queue":314,"guard_uid":1000001,
            "host_denies":["198.18.240.1/32"],"spool":"unused","spool_bytes":8192,
            "policy_layers":layers
        }))
        .unwrap()
    }
    fn tuple(address: &str, protocol: u8, port: u16) -> Tuple {
        Tuple {
            source: "198.18.240.2".parse().unwrap(),
            destination: address.parse().unwrap(),
            source_port: 12345,
            destination_port: port,
            protocol,
        }
    }
    fn rule(cidr: &str, protocol: &str, port: u16) -> serde_json::Value {
        json!({"cidr":cidr,"protocol":protocol,"ports":[port]})
    }
    #[test]
    fn layers_intersect_and_denies_win() {
        let c = config(json!([
            {"filter_mode":"transport","default_egress":"deny","allow":[rule("8.8.8.0/24","tcp",443)]},
            {"filter_mode":"transport","default_egress":"deny","allow":[rule("8.8.0.0/16","tcp",443)],
             "deny":[rule("8.8.8.9/32","tcp",443)]}
        ]));
        c.validate().unwrap();
        assert!(decision(&c, &tuple("8.8.8.8", 6, 443)).is_ok());
        assert_eq!(
            decision(&c, &tuple("8.8.8.9", 6, 443)),
            Err("explicit_policy_deny")
        );
        for t in [
            tuple("8.8.4.4", 6, 443),
            tuple("8.8.8.8", 17, 443),
            tuple("8.8.8.8", 6, 80),
        ] {
            assert_eq!(decision(&c, &t), Err("not_in_allowlist"));
        }
    }
    #[test]
    fn exceptions_never_override_explicit_or_parent_denies() {
        let mut c = config(json!([{"filter_mode":"transport","default_egress":"deny",
            "allow":[rule("10.0.0.7/32","tcp",8080)]}]));
        c.exceptions.push(crate::Exception {
            address: "10.0.0.7".parse().unwrap(),
            transport: "tcp".into(),
            port: 8080,
        });
        c.validate().unwrap();
        let t = tuple("10.0.0.7", 6, 8080);
        assert_eq!(decision(&c, &t), Ok("policy_exception"));
        assert!(decision(&c, &tuple("10.0.0.7", 6, 8081)).is_err());
        c.policy_layers[0].allow.clear();
        assert_eq!(decision(&c, &t), Err("not_in_allowlist"));
        c.policy_layers.clear();
        c.safety_denies.push(Prefix("10.0.0.0".parse().unwrap(), 8));
        assert_eq!(decision(&c, &t), Err("immutable_safety_deny"));
    }
    #[test]
    fn unsupported_or_ambiguous_rules_fail_admission() {
        for value in [
            json!({"host_pattern":"example.org","protocol":"tcp","ports":[443]}),
            json!({"cidr":"8.8.8.8/32","host_pattern":"example.org","protocol":"tcp","ports":[443]}),
            json!({"cidr":"8.8.8.8/32","protocol":"tcp","ports":[]}),
            json!({"cidr":"8.8.8.8/32","protocol":"tcp","ports":[0]}),
        ] {
            let c = config(
                json!([{"filter_mode":"transport","default_egress":"public","allow":[value]}]),
            );
            assert!(c.validate().is_err());
        }
        let c = config(json!([{"filter_mode":"strict_sites","default_egress":"deny"}]));
        assert!(c.validate().is_err());
        for invalid in [
            "1.2.3.4/33",
            "::ffff:8.8.8.8/128",
            "1.2.3.4",
            "0177.0.0.1/32",
        ] {
            assert!(Prefix::try_from(invalid.to_string()).is_err());
        }
    }
    #[test]
    fn public_repository_ceiling_cannot_be_omitted() {
        let mut c = config(json!([]));
        c.require_allowlist = true;
        assert!(c.validate().is_err());
        c.policy_layers =
            serde_json::from_value(json!([{"filter_mode":"transport","default_egress":"deny"}]))
                .unwrap();
        c.validate().unwrap();
        assert!(decision(&c, &tuple("8.8.8.8", 6, 443)).is_err());
    }
}
