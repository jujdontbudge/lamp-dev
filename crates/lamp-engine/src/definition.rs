//! `lamp.template/v1` TOML definitions. Dockerfiles, OCI/Docker image
//! references and host Docker runtimes are rejected.
use crate::error::{EngineError, Result};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

pub const FORMAT: &str = "lamp.template/v1";
pub const MAX_DEFINITION_BYTES: usize = 256 << 10;
pub const MAX_STEPS: usize = 256;

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Definition {
    pub format: String,
    pub name: String,
    pub guest_os: String,
    pub arch: String,
    pub seed_ref: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub package_lock_blob_id: Option<String>,
    /// Build network policy. Only `none` is available until the host guard
    /// (T4) attaches to build sandboxes.
    #[serde(default = "no_network")]
    pub network: String,
    #[serde(default)]
    pub steps: Vec<Step>,
}

fn no_network() -> String {
    "none".into()
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Step {
    pub argv: Vec<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub env: BTreeMap<String, String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cwd: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub timeout_s: Option<u64>,
}

/// A parsed `seed_ref` such as `lamp-seed/debian-13@sha256:<64 hex>`.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct SeedRef {
    pub name: String,
    pub digest: String,
}

pub fn parse_seed_ref(text: &str) -> Result<SeedRef> {
    let rest = text
        .strip_prefix("lamp-seed/")
        .ok_or_else(|| EngineError::invalid("seed_ref must start with lamp-seed/"))?;
    let (name, digest) = rest
        .split_once("@sha256:")
        .ok_or_else(|| EngineError::invalid("seed_ref must pin @sha256:<digest>"))?;
    if !valid_name(name) {
        return Err(EngineError::invalid(
            "seed name must be lowercase [a-z0-9.-]",
        ));
    }
    if !is_hex64(digest) {
        return Err(EngineError::invalid(
            "seed digest must be 64 lowercase hex characters",
        ));
    }
    Ok(SeedRef {
        name: name.into(),
        digest: digest.into(),
    })
}

pub fn is_hex64(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|c| c.is_ascii_digit() || (b'a'..=b'f').contains(&c))
}

fn valid_name(s: &str) -> bool {
    !s.is_empty()
        && s.len() <= 63
        && s.as_bytes()[0].is_ascii_alphanumeric()
        && s.bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == b'-' || c == b'.')
}

fn looks_like_container_build(text: &str) -> bool {
    let first = text
        .lines()
        .map(str::trim)
        .find(|l| !l.is_empty() && !l.starts_with('#'))
        .unwrap_or("");
    let upper = first.to_ascii_uppercase();
    upper.starts_with("FROM ") || upper.starts_with("ARG ") || upper.starts_with("# SYNTAX=")
}

pub fn parse(text: &str) -> Result<Definition> {
    if text.len() > MAX_DEFINITION_BYTES {
        return Err(EngineError::invalid("definition larger than 256 KiB"));
    }
    if looks_like_container_build(text) {
        return Err(EngineError::invalid(
            "Dockerfile/OCI build input is not a lamp.template/v1 definition",
        ));
    }
    let def: Definition =
        toml::from_str(text).map_err(|e| EngineError::invalid(format!("definition: {e}")))?;
    def.validate()?;
    Ok(def)
}

impl Definition {
    pub fn validate(&self) -> Result<()> {
        if self.format != FORMAT {
            return Err(EngineError::invalid(format!("format must be {FORMAT}")));
        }
        if !valid_name(&self.name) {
            return Err(EngineError::invalid("name must be 1..63 of [a-z0-9.-]"));
        }
        if self.guest_os != "linux" {
            return Err(EngineError::invalid(
                "this engine builds guest_os = \"linux\" only",
            ));
        }
        if !matches!(self.arch.as_str(), "x86_64" | "arm64") {
            return Err(EngineError::invalid("arch must be x86_64 or arm64"));
        }
        parse_seed_ref(&self.seed_ref)?;
        if let Some(lock) = &self.package_lock_blob_id
            && (!lock.starts_with("bl_") || lock.contains('<'))
        {
            return Err(EngineError::invalid(
                "package_lock_blob_id must be a bl_ blob id",
            ));
        }
        if self.network != "none" {
            return Err(EngineError::invalid(
                "only network = \"none\" builds are available until the host guard is attached",
            ));
        }
        if self.steps.len() > MAX_STEPS {
            return Err(EngineError::invalid("too many steps"));
        }
        for (i, step) in self.steps.iter().enumerate() {
            if step.argv.is_empty() || step.argv.len() > 4096 {
                return Err(EngineError::invalid(format!(
                    "step {i}: argv must have 1..4096 items"
                )));
            }
            let size: usize = step.argv.iter().map(String::len).sum();
            if size > 64 << 10 || step.argv.iter().any(|a| a.contains('\0')) {
                return Err(EngineError::invalid(format!(
                    "step {i}: argv too large or contains NUL"
                )));
            }
            let docker = ["docker", "dockerd", "podman", "buildah"];
            let program = step.argv[0].rsplit('/').next().unwrap_or("");
            if docker.contains(&program) {
                return Err(EngineError::invalid(format!(
                    "step {i}: container runtimes are not template build steps; use the Docker kit inside a sandbox"
                )));
            }
            if let Some(cwd) = &step.cwd
                && !cwd.starts_with('/')
            {
                return Err(EngineError::invalid(format!(
                    "step {i}: cwd must be absolute"
                )));
            }
            if step.timeout_s.is_some_and(|t| t == 0 || t > 3600) {
                return Err(EngineError::invalid(format!(
                    "step {i}: timeout_s must be 1..3600"
                )));
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const GOOD: &str = r#"
format = "lamp.template/v1"
name = "python-tools"
guest_os = "linux"
arch = "x86_64"
seed_ref = "lamp-seed/debian-13@sha256:64e9b8da0aee3825979ab333d48557612cce7a51fea64516acd8ee90255ad81d"
[[steps]]
argv = ["python3", "-c", "print('tools-installed')"]
"#;

    #[test]
    fn accepts_journey_definition() {
        let d = parse(GOOD).unwrap();
        assert_eq!(d.steps.len(), 1);
        assert_eq!(d.network, "none");
        assert_eq!(parse_seed_ref(&d.seed_ref).unwrap().name, "debian-13");
    }

    #[test]
    fn rejects_container_inputs_and_bad_fields() {
        assert!(parse("FROM debian:13\nRUN apt-get install git\n").is_err());
        assert!(parse(&GOOD.replace("lamp.template/v1", "docker/v1")).is_err());
        assert!(parse(&format!("{GOOD}\nimage = \"debian:13\"\n")).is_err());
        assert!(parse(&GOOD.replace("@sha256:64e9", "@sha256:<verified")).is_err());
        assert!(parse(&GOOD.replace("\"python3\"", "\"docker\"")).is_err());
        assert!(parse(&GOOD.replace("x86_64", "riscv64")).is_err());
        assert!(parse(&format!("{GOOD}\n[[steps]]\nargv = []\n")).is_err());
        let net = GOOD.replace("arch = \"x86_64\"", "arch = \"x86_64\"\nnetwork = \"open\"");
        assert!(parse(&net).is_err());
    }
}
