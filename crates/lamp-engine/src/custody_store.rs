use crate::custody::Custody;
use crate::{EngineError, Result};
use serde::{Deserialize, Serialize};
use std::fs::{File, OpenOptions};
use std::io::{Read, Write};
use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};

const LIMIT: u64 = 8 << 20;
const FORMAT: &str = "lamp.custody/private-unfrozen-1";
static NEXT: AtomicU64 = AtomicU64::new(0);

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Stored {
    format: String,
    graph: Custody,
}

pub struct CustodyStore {
    path: PathBuf,
    graph: Custody,
    broken: bool,
}

impl CustodyStore {
    pub fn open(path: &Path, owner: &str, host: &str, trusted: &[String]) -> Result<Self> {
        let parent = path
            .parent()
            .ok_or_else(|| EngineError::invalid("custody parent missing"))?;
        let directory = std::fs::symlink_metadata(parent)?;
        if !directory.is_dir() || directory.permissions().mode() & 0o077 != 0 {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "custody parent must be private",
            ));
        }
        let graph = match std::fs::symlink_metadata(path) {
            Ok(metadata) => {
                if !metadata.is_file()
                    || metadata.uid() != directory.uid()
                    || metadata.permissions().mode() & 0o077 != 0
                    || metadata.len() > LIMIT
                {
                    return Err(EngineError::new(
                        "SCOPE_DENIED",
                        "custody file must be private and bounded",
                    ));
                }
                let file = File::open(path)?;
                if file.metadata()?.ino() != metadata.ino()
                    || file.metadata()?.dev() != metadata.dev()
                {
                    return Err(EngineError::new("SCOPE_DENIED", "custody file substituted"));
                }
                let mut data = Vec::new();
                file.take(LIMIT + 1).read_to_end(&mut data)?;
                if data.len() as u64 > LIMIT {
                    return Err(EngineError::invalid("custody store exceeds bound"));
                }
                let stored: Stored = serde_json::from_slice(&data)
                    .map_err(|_| EngineError::invalid("invalid custody store"))?;
                if stored.format != FORMAT {
                    return Err(EngineError::invalid("unsupported private custody store"));
                }
                stored.graph.validate(owner, host)?;
                stored.graph
            }
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                Custody::new(owner, host, trusted.iter().map(String::as_str))?
            }
            Err(error) => return Err(error.into()),
        };
        let mut store = Self {
            path: path.into(),
            graph,
            broken: false,
        };
        store.transact(|graph| {
            graph.restrict_hosts(trusted);
            graph.recover_runs();
            Ok(())
        })?;
        Ok(store)
    }

    pub fn graph(&self) -> Result<&Custody> {
        if self.broken {
            return Err(EngineError::new(
                "DATA_UNAVAILABLE",
                "custody persistence is uncertain",
            ));
        }
        Ok(&self.graph)
    }

    pub fn transact<T>(&mut self, change: impl FnOnce(&mut Custody) -> Result<T>) -> Result<T> {
        self.graph()?;
        let mut candidate = self.graph.clone();
        let value = change(&mut candidate)?;
        if let Err(error) = self.persist(&candidate) {
            self.broken = true;
            return Err(error);
        }
        self.graph = candidate;
        Ok(value)
    }

    fn persist(&self, graph: &Custody) -> Result<()> {
        let data = serde_json::to_vec(&Stored {
            format: FORMAT.into(),
            graph: graph.clone(),
        })
        .map_err(|_| EngineError::invalid("cannot encode custody store"))?;
        if data.len() as u64 > LIMIT {
            return Err(EngineError::new(
                "CONTROL_QUOTA_EXCEEDED",
                "custody store exceeds bound",
            ));
        }
        let temporary = self.path.with_extension(format!(
            "pending-{}-{}",
            std::process::id(),
            NEXT.fetch_add(1, Ordering::Relaxed)
        ));
        let mut created = false;
        let result = (|| -> Result<()> {
            let mut file = OpenOptions::new()
                .create_new(true)
                .write(true)
                .mode(0o600)
                .open(&temporary)?;
            created = true;
            file.write_all(&data)?;
            file.sync_all()?;
            std::fs::rename(&temporary, &self.path)?;
            File::open(self.path.parent().unwrap())?.sync_all()?;
            Ok(())
        })();
        if result.is_err() && created {
            let _ = std::fs::remove_file(&temporary);
        }
        result
    }
}
