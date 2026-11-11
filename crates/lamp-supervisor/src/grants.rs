//! Control grants installed by the supervisor's installer.
//!
//! `lampd` sends only a grant reference (`Context.grant_id`); the authority
//! behind it is a root-only record in the store that only root can install
//! (`lamp-supervisor grant-install`). Every sandbox command is checked
//! against it before dispatch: the caller's OS UID, the sandbox scope, the
//! generation, the policy revision, the allowed commands and expiry. Deleting
//! the file revokes it at once; records are read on every request.
use serde::{Deserialize, Serialize};
use std::io::Write;
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};

pub const FORMAT: &str = "lamp.control-grant/v1";
pub const COMMANDS: &[&str] = &[
    "allocate", "restore", "start", "capture", "stop", "release", "inspect", "cancel",
];

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ControlGrant {
    pub format: String,
    pub grant_id: String,
    /// The only OS UID that may present this grant.
    pub caller_uid: u32,
    /// Sandbox IDs this grant covers start with this prefix.
    pub sandbox_prefix: String,
    /// The exact sandbox generation and policy revision it authorizes.
    pub generation: u64,
    /// When set, generations `generation..=max_generation` are authorized,
    /// so a resumed sandbox can run as a new generation (its own audit
    /// spool and identity barrier). Absent keeps the exact match.
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub max_generation: Option<u64>,
    pub policy_revision: u64,
    pub commands: Vec<String>,
    pub issued_at_unix_ms: u64,
    pub expires_at_unix_ms: u64,
}

/// The request facts a grant is checked against.
pub struct Presented<'a> {
    pub grant_id: &'a str,
    pub caller_uid: u32,
    pub sandbox_id: &'a str,
    pub generation: u64,
    pub policy_revision: u64,
    pub command: &'a str,
    pub now_ms: u64,
}

/// A refusal as (public error code, message).
pub type Refusal = (&'static str, String);

pub fn valid_grant_id(id: &str) -> bool {
    id.starts_with("gr_")
        && (4..=64).contains(&id.len())
        && id[3..]
            .bytes()
            .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit())
}

pub struct GrantStore {
    dir: PathBuf,
}

impl GrantStore {
    pub fn new(dir: impl Into<PathBuf>) -> GrantStore {
        GrantStore { dir: dir.into() }
    }

    fn path(&self, id: &str) -> PathBuf {
        self.dir.join(format!("{id}.json"))
    }

    /// Writes a grant atomically as a 0600 file in a 0700 directory.
    pub fn install(&self, grant: &ControlGrant) -> std::io::Result<()> {
        if !valid_grant_id(&grant.grant_id)
            || grant.format != FORMAT
            || grant
                .commands
                .iter()
                .any(|c| !COMMANDS.contains(&c.as_str()))
            || grant.sandbox_prefix.is_empty()
        {
            return Err(std::io::Error::other("invalid grant"));
        }
        std::fs::create_dir_all(&self.dir)?;
        std::fs::set_permissions(&self.dir, std::fs::Permissions::from_mode(0o700))?;
        let tmp = self.dir.join(format!(".{}.tmp", grant.grant_id));
        let mut f = std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(&tmp)?;
        f.write_all(serde_json::to_string(grant)?.as_bytes())?;
        f.sync_all()?;
        std::fs::rename(&tmp, self.path(&grant.grant_id))
    }

    fn load(&self, id: &str) -> Option<ControlGrant> {
        use std::os::unix::fs::MetadataExt;
        let path = self.path(id);
        let meta = std::fs::symlink_metadata(&path).ok()?;
        // Only a regular root-owned file nobody else can write is authority.
        if !meta.is_file() || meta.uid() != 0 || meta.mode() & 0o022 != 0 {
            return None;
        }
        serde_json::from_slice(&std::fs::read(path).ok()?).ok()
    }

    /// Checks a presented grant reference before any effect.
    pub fn check(&self, p: &Presented<'_>) -> Result<(), Refusal> {
        self.check_with(p, |id| self.load(id))
    }

    fn check_with(
        &self,
        p: &Presented<'_>,
        load: impl Fn(&str) -> Option<ControlGrant>,
    ) -> Result<(), Refusal> {
        let unknown = || {
            (
                "UNAUTHENTICATED",
                "no installed control grant matches grant_id".into(),
            )
        };
        if !valid_grant_id(p.grant_id) {
            return Err(unknown());
        }
        let g = load(p.grant_id).ok_or_else(unknown)?;
        if g.format != FORMAT || g.grant_id != p.grant_id {
            return Err(unknown());
        }
        if p.now_ms >= g.expires_at_unix_ms {
            return Err(("UNAUTHENTICATED", format!("grant {} expired", g.grant_id)));
        }
        let denied = |why: &str| Err(("SCOPE_DENIED", format!("grant {}: {why}", g.grant_id)));
        if g.caller_uid != p.caller_uid {
            return denied("issued to another caller");
        }
        if !p.sandbox_id.starts_with(&g.sandbox_prefix) {
            return denied("sandbox outside its scope");
        }
        let last = g.max_generation.unwrap_or(g.generation);
        if p.generation < g.generation || p.generation > last {
            return denied("generation outside its scope");
        }
        if g.policy_revision != p.policy_revision {
            return denied("policy revision outside its scope");
        }
        if !g.commands.iter().any(|c| c == p.command) {
            return denied("command not granted");
        }
        Ok(())
    }
}

/// Removes a grant (revocation).
pub fn revoke(dir: &Path, id: &str) -> std::io::Result<()> {
    if !valid_grant_id(id) {
        return Err(std::io::Error::other("invalid grant id"));
    }
    std::fs::remove_file(dir.join(format!("{id}.json")))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn grant() -> ControlGrant {
        ControlGrant {
            format: FORMAT.into(),
            grant_id: "gr_0123abcd".into(),
            caller_uid: 1000,
            sandbox_prefix: "sb_".into(),
            generation: 1,
            max_generation: None,
            policy_revision: 1,
            commands: vec!["start".into(), "inspect".into()],
            issued_at_unix_ms: 1,
            expires_at_unix_ms: 10_000,
        }
    }

    fn presented(command: &str) -> Presented<'_> {
        Presented {
            grant_id: "gr_0123abcd",
            caller_uid: 1000,
            sandbox_id: "sb_demo",
            generation: 1,
            policy_revision: 1,
            command,
            now_ms: 5_000,
        }
    }

    #[test]
    fn scope_generation_policy_and_expiry_are_enforced() {
        let store = GrantStore::new("/nonexistent");
        let g = grant();
        let load = |id: &str| (id == g.grant_id).then(|| g.clone());
        assert!(store.check_with(&presented("start"), load).is_ok());
        let code = |p: Presented<'_>| store.check_with(&p, load).unwrap_err().0;
        assert_eq!(
            code(Presented {
                grant_id: "gr_other",
                ..presented("start")
            }),
            "UNAUTHENTICATED"
        );
        assert_eq!(
            code(Presented {
                grant_id: "../../etc/x",
                ..presented("start")
            }),
            "UNAUTHENTICATED"
        );
        assert_eq!(
            code(Presented {
                now_ms: 10_000,
                ..presented("start")
            }),
            "UNAUTHENTICATED"
        );
        assert_eq!(
            code(Presented {
                caller_uid: 1001,
                ..presented("start")
            }),
            "SCOPE_DENIED"
        );
        assert_eq!(
            code(Presented {
                sandbox_id: "xx_demo",
                ..presented("start")
            }),
            "SCOPE_DENIED"
        );
        assert_eq!(
            code(Presented {
                generation: 2,
                ..presented("start")
            }),
            "SCOPE_DENIED"
        );
        assert_eq!(
            code(Presented {
                policy_revision: 0,
                ..presented("start")
            }),
            "SCOPE_DENIED"
        );
        assert_eq!(code(presented("capture")), "SCOPE_DENIED");
    }

    #[test]
    fn install_writes_a_private_file_and_load_rejects_writable_ones() {
        let dir = std::env::temp_dir().join(format!("lamp-grants-{}", std::process::id()));
        let store = GrantStore::new(&dir);
        store.install(&grant()).unwrap();
        let path = dir.join("gr_0123abcd.json");
        let mode = std::fs::metadata(&path).unwrap().permissions().mode();
        assert_eq!(mode & 0o777, 0o600);
        assert_eq!(
            std::fs::metadata(&dir).unwrap().permissions().mode() & 0o777,
            0o700
        );
        // Not root-owned when tests run unprivileged, or group-writable: never authority.
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o620)).unwrap();
        assert!(store.load("gr_0123abcd").is_none());
        let mut bad = grant();
        bad.commands.push("anything".into());
        assert!(store.install(&bad).is_err());
        let _ = std::fs::remove_dir_all(dir);
    }
}
