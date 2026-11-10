use crate::definition;
use crate::error::{EngineError, Result};
use crate::manifest::sha256_hex;
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};
use std::sync::Arc;
use std::sync::Mutex;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Harness {
    #[serde(rename = "claude_code", alias = "ClaudeCode")]
    ClaudeCode,
    #[serde(rename = "codex", alias = "Codex")]
    Codex,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CaptureTrust {
    Clean,
    OwnerPrivate,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CapturePurpose {
    OwnerPrivate,
    Template,
    Export,
    Share,
}

#[derive(Debug, Clone, Copy)]
pub struct Destination<'a> {
    pub principal: &'a str,
    pub host: &'a str,
    pub online: bool,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Reconstruction {
    definition: String,
    digest: String,
}

impl std::fmt::Debug for Reconstruction {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Reconstruction")
            .field("definition_digest", &self.digest)
            .finish()
    }
}

impl Reconstruction {
    pub fn verify(definition: &str, approved_nonsecret_digest: &str) -> Result<Self> {
        let digest = sha256_hex(definition.as_bytes());
        if digest != approved_nonsecret_digest {
            return Err(EngineError::new(
                "CLEAN_REBUILD_REQUIRED",
                "reconstruction does not match the approved nonsecret input",
            ));
        }
        definition::parse(definition)
            .map_err(|_| EngineError::invalid("invalid reconstruction definition"))?;
        Ok(Self {
            definition: definition.into(),
            digest,
        })
    }

    pub fn definition_digest(&self) -> &str {
        &self.digest
    }

    #[cfg(target_os = "linux")]
    pub fn build(&self, engine: &crate::engine::Engine) -> Result<crate::build::BuildReport> {
        crate::build::build_template(engine, &self.definition)
    }

    pub fn seed_ref(&self) -> String {
        definition::parse(&self.definition).unwrap().seed_ref
    }
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Subscription {
    _harness: Harness,
    hosts: BTreeSet<String>,
    #[serde(default)]
    epoch: u64,
    #[serde(default)]
    uncertain: bool,
    #[serde(default)]
    active: Option<ActiveRun>,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ActiveRun {
    record: String,
    operation: String,
    sign_in: bool,
}

#[derive(Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Record {
    parents: BTreeSet<String>,
    exposures: BTreeSet<String>,
    reconstruction: Option<Reconstruction>,
    #[serde(default)]
    epochs: BTreeMap<String, u64>,
    #[serde(default)]
    bridge_cursor: usize,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Custody {
    owner: String,
    local_host: String,
    trusted_hosts: BTreeSet<String>,
    subscriptions: BTreeMap<String, Subscription>,
    records: BTreeMap<String, Record>,
}

fn identifier(value: &str) -> Result<()> {
    if value.is_empty()
        || value.len() > 128
        || !value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-' || b == b'.')
    {
        return Err(EngineError::invalid("invalid custody reference"));
    }
    Ok(())
}

fn host_set<'a>(hosts: impl IntoIterator<Item = &'a str>) -> Result<BTreeSet<String>> {
    hosts
        .into_iter()
        .map(|host| {
            identifier(host)?;
            Ok(host.to_string())
        })
        .collect()
}

impl Custody {
    pub fn new<'a>(
        owner: &str,
        local_host: &str,
        trusted_hosts: impl IntoIterator<Item = &'a str>,
    ) -> Result<Self> {
        identifier(owner)?;
        identifier(local_host)?;
        let trusted_hosts = host_set(trusted_hosts)?;
        if !trusted_hosts.contains(local_host) {
            return Err(EngineError::new(
                "HOST_NOT_TRUSTED",
                "local host is not in custody",
            ));
        }
        Ok(Self {
            owner: owner.into(),
            local_host: local_host.into(),
            trusted_hosts,
            subscriptions: BTreeMap::new(),
            records: BTreeMap::new(),
        })
    }

    pub fn register_subscription<'a>(
        &mut self,
        id: &str,
        harness: Harness,
        hosts: impl IntoIterator<Item = &'a str>,
    ) -> Result<()> {
        identifier(id)?;
        let hosts = host_set(hosts)?;
        if !hosts.contains(&self.local_host) || !hosts.is_subset(&self.trusted_hosts) {
            return Err(EngineError::new(
                "HOST_NOT_TRUSTED",
                "subscription host is not in custody",
            ));
        }
        if self.subscriptions.contains_key(id) {
            return Err(EngineError::in_use("subscription reference already exists"));
        }
        self.subscriptions.insert(
            id.into(),
            Subscription {
                _harness: harness,
                hosts,
                epoch: 0,
                uncertain: false,
                active: None,
            },
        );
        Ok(())
    }

    pub fn create(&mut self, id: &str) -> Result<()> {
        identifier(id)?;
        if self.records.contains_key(id) {
            return Err(EngineError::in_use("custody record already exists"));
        }
        self.records.insert(id.into(), Record::default());
        Ok(())
    }

    fn record(&self, id: &str) -> Result<&Record> {
        self.records
            .get(id)
            .ok_or_else(|| EngineError::not_found("custody record"))
    }

    pub fn expose(&mut self, id: &str, subscription: &str) -> Result<()> {
        self.record(id)?;
        let source = self
            .subscriptions
            .get(subscription)
            .ok_or_else(|| EngineError::not_found("subscription reference"))?;
        if !self.trusted_hosts.contains(&self.local_host)
            || !source.hosts.contains(&self.local_host)
        {
            return Err(EngineError::new(
                "HOST_NOT_TRUSTED",
                "local custody was revoked",
            ));
        }
        let epoch = source.epoch;
        let record = self.records.get_mut(id).unwrap();
        record.exposures.insert(subscription.into());
        record.epochs.entry(subscription.into()).or_insert(epoch);
        self.propagate();
        Ok(())
    }

    pub fn depend_on(&mut self, id: &str, parent: &str) -> Result<()> {
        self.record(id)?;
        self.record(parent)?;
        let mut pending = vec![parent];
        let mut visited = BTreeSet::new();
        while let Some(next) = pending.pop() {
            if next == id {
                return Err(EngineError::invalid("cyclic custody dependency"));
            }
            if visited.insert(next) {
                pending.extend(self.record(next)?.parents.iter().map(String::as_str));
            }
        }
        let inserted = self
            .records
            .get_mut(id)
            .unwrap()
            .parents
            .insert(parent.into());
        if inserted {
            let mut affected = BTreeSet::from([id.to_string()]);
            loop {
                let descendants: Vec<_> = self
                    .records
                    .iter()
                    .filter(|(key, record)| {
                        !affected.contains(*key) && !record.parents.is_disjoint(&affected)
                    })
                    .map(|(key, _)| key.clone())
                    .collect();
                if descendants.is_empty() {
                    break;
                }
                affected.extend(descendants);
            }
            for key in affected {
                self.records.get_mut(&key).unwrap().reconstruction = None;
            }
        }
        self.propagate();
        Ok(())
    }

    fn propagate(&mut self) {
        loop {
            let mut updates = Vec::new();
            for (id, record) in &self.records {
                let mut exposures = record.exposures.clone();
                for parent in &record.parents {
                    exposures.extend(self.records[parent].exposures.iter().cloned());
                }
                if exposures != record.exposures {
                    updates.push((id.clone(), exposures));
                }
            }
            if updates.is_empty() {
                break;
            }
            for (id, exposures) in updates {
                self.records.get_mut(&id).unwrap().exposures = exposures;
            }
        }
    }

    pub fn exposures(&self, id: &str) -> Result<Vec<&str>> {
        Ok(self
            .record(id)?
            .exposures
            .iter()
            .map(String::as_str)
            .collect())
    }

    pub fn trust(&self, id: &str) -> Result<CaptureTrust> {
        Ok(if self.record(id)?.exposures.is_empty() {
            CaptureTrust::Clean
        } else {
            CaptureTrust::OwnerPrivate
        })
    }

    pub fn authorize(
        &self,
        id: &str,
        destination: Destination<'_>,
        purpose: CapturePurpose,
    ) -> Result<()> {
        let record = self.record(id)?;
        if record.exposures.is_empty() {
            return Ok(());
        }
        if purpose != CapturePurpose::OwnerPrivate {
            return Err(EngineError::new(
                "CLEAN_REBUILD_REQUIRED",
                "subscription-bearing transfer requires clean reconstruction",
            ));
        }
        if record
            .exposures
            .iter()
            .any(|source| self.subscriptions[source].active.is_some())
        {
            return Err(EngineError::in_use(
                "official harness must quiesce before private capture or restore",
            ));
        }
        if destination.principal != self.owner {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "private capture belongs to another person",
            ));
        }
        if !destination.online {
            return Err(EngineError::host("custody destination is offline"));
        }
        if !self.trusted_hosts.contains(&self.local_host)
            || !self.trusted_hosts.contains(destination.host)
            || record
                .exposures
                .iter()
                .any(|id| !self.subscriptions[id].hosts.contains(destination.host))
        {
            return Err(EngineError::new(
                "HOST_NOT_TRUSTED",
                "capture destination is not in custody",
            ));
        }
        Ok(())
    }

    pub fn derive(
        &mut self,
        source: &str,
        id: &str,
        destination: Destination<'_>,
        purpose: CapturePurpose,
    ) -> Result<()> {
        self.authorize(source, destination, purpose)?;
        let reconstruction = self.record(source)?.reconstruction.clone();
        let exposures = self.record(source)?.exposures.clone();
        let epochs = self.record(source)?.epochs.clone();
        let bridge_cursor = self.record(source)?.bridge_cursor;
        self.create(id)?;
        let record = self.records.get_mut(id).unwrap();
        record.parents.insert(source.into());
        record.exposures = exposures;
        record.epochs = epochs;
        record.bridge_cursor = bridge_cursor;
        record.reconstruction = reconstruction;
        Ok(())
    }

    pub fn revoke_host(&mut self, host: &str) -> Result<()> {
        identifier(host)?;
        self.trusted_hosts.remove(host);
        Ok(())
    }

    pub fn record_reconstruction(&mut self, id: &str, input: Reconstruction) -> Result<()> {
        let record = self.record(id)?;
        if !record.exposures.is_empty() || record.reconstruction.is_some() {
            return Err(EngineError::new(
                "CLEAN_REBUILD_REQUIRED",
                "nonsecret reconstruction must be recorded once before exposure",
            ));
        }
        self.records.get_mut(id).unwrap().reconstruction = Some(input);
        Ok(())
    }

    pub fn reconstruction(&self, id: &str) -> Result<Reconstruction> {
        self.record(id)?.reconstruction.clone().ok_or_else(|| {
            EngineError::new(
                "CLEAN_REBUILD_REQUIRED",
                "verified nonsecret reconstruction inputs are missing",
            )
        })
    }

    pub fn contains(&self, id: &str) -> bool {
        self.records.contains_key(id)
    }

    pub fn has_subscription(&self, id: &str) -> bool {
        self.subscriptions.contains_key(id)
    }

    pub fn restrict_hosts(&mut self, trusted: &[String]) {
        self.trusted_hosts.retain(|host| trusted.contains(host));
    }

    pub fn inherit(
        &mut self,
        source: &str,
        target: &str,
        destination: Destination<'_>,
    ) -> Result<()> {
        self.authorize(source, destination, CapturePurpose::OwnerPrivate)?;
        let original = self.record(source)?;
        let exposures = original.exposures.clone();
        let epochs = original.epochs.clone();
        let bridge_cursor = original.bridge_cursor;
        let reconstruction = original.reconstruction.clone();
        let target_record = self.record(target)?;
        if !target_record.parents.is_empty()
            || !target_record.exposures.is_empty()
            || target_record.reconstruction.is_some()
        {
            return Err(EngineError::in_use("restore target is not fresh"));
        }
        let target_record = self.records.get_mut(target).unwrap();
        target_record.parents.insert(source.into());
        target_record.exposures = exposures;
        target_record.epochs = epochs;
        target_record.bridge_cursor = bridge_cursor;
        target_record.reconstruction = reconstruction;
        Ok(())
    }

    pub fn harness(&self, id: &str) -> Result<Option<Harness>> {
        let exposures = &self.record(id)?.exposures;
        let mut harness = None;
        for source in exposures {
            let next = self.subscriptions[source]._harness;
            if harness.is_some_and(|previous| previous != next) {
                return Err(EngineError::new(
                    "SCOPE_DENIED",
                    "mixed-vendor subscription state",
                ));
            }
            harness = Some(next);
        }
        Ok(harness)
    }

    pub fn begin_run(&mut self, id: &str, operation: &str, sign_in: bool) -> Result<()> {
        identifier(operation)?;
        self.authorize(
            id,
            Destination {
                principal: &self.owner,
                host: &self.local_host,
                online: true,
            },
            CapturePurpose::OwnerPrivate,
        )?;
        let record = self.record(id)?;
        if record.exposures.is_empty() {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "official run requires a subscription source",
            ));
        }
        for source in &record.exposures {
            let lineage = &self.subscriptions[source];
            if !sign_in
                && (lineage.uncertain
                    || record.epochs.get(source).copied().unwrap_or(0) != lineage.epoch)
            {
                return Err(EngineError::new(
                    "CONTINUATION_UNSUPPORTED",
                    "stale or uncertain official run requires fresh vendor sign-in",
                ));
            }
        }
        let sources = record.exposures.clone();
        for source in sources {
            self.subscriptions.get_mut(&source).unwrap().active = Some(ActiveRun {
                record: id.into(),
                operation: operation.into(),
                sign_in,
            });
        }
        Ok(())
    }

    pub fn has_run(&self, id: &str, operation: &str) -> Result<bool> {
        Ok(self.record(id)?.exposures.iter().any(|source| {
            self.subscriptions[source]
                .active
                .as_ref()
                .is_some_and(|run| run.record == id && run.operation == operation)
        }))
    }

    pub fn finish_run(
        &mut self,
        id: &str,
        operation: &str,
        success: bool,
        refreshed: bool,
    ) -> Result<()> {
        let sources = self.record(id)?.exposures.clone();
        for source in sources {
            let lineage = self.subscriptions.get_mut(&source).unwrap();
            if !lineage
                .active
                .as_ref()
                .is_some_and(|run| run.record == id && run.operation == operation)
            {
                continue;
            }
            let run = lineage.active.take().unwrap();
            if !success {
                lineage.uncertain = true;
            } else if refreshed || run.sign_in {
                lineage.epoch = lineage.epoch.checked_add(1).ok_or_else(|| {
                    EngineError::new("CONTINUATION_UNSUPPORTED", "official lineage exhausted")
                })?;
                lineage.uncertain = false;
                self.records
                    .get_mut(id)
                    .unwrap()
                    .epochs
                    .insert(source, lineage.epoch);
            }
        }
        Ok(())
    }

    pub fn recover_runs(&mut self) {
        for source in self.subscriptions.values_mut() {
            if source.active.take().is_some() {
                source.uncertain = true;
            }
        }
    }

    pub fn next_bridge_step(&mut self, id: &str, limit: usize) -> Result<usize> {
        let record = self.record(id)?;
        if record.exposures.is_empty()
            || record.bridge_cursor >= limit
            || record.exposures.iter().any(|source| {
                !self.subscriptions[source]
                    .active
                    .as_ref()
                    .is_some_and(|run| run.record == id && !run.sign_in)
            })
        {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "bridge requires an active official run and an unused approved step",
            ));
        }
        let record = self.records.get_mut(id).unwrap();
        let index = record.bridge_cursor;
        record.bridge_cursor += 1;
        Ok(index)
    }

    pub fn validate(&self, owner: &str, host: &str) -> Result<()> {
        if self.owner != owner || self.local_host != host {
            return Err(EngineError::new(
                "HOST_NOT_TRUSTED",
                "custody store identity mismatch",
            ));
        }
        identifier(owner)?;
        identifier(host)?;
        for value in &self.trusted_hosts {
            identifier(value)?;
        }
        for (id, source) in &self.subscriptions {
            identifier(id)?;
            if source.hosts.is_empty() {
                return Err(EngineError::invalid("empty subscription custody"));
            }
            for host in &source.hosts {
                identifier(host)?;
            }
        }
        for (id, record) in &self.records {
            identifier(id)?;
            for source in &record.exposures {
                if !self.subscriptions.contains_key(source) {
                    return Err(EngineError::invalid(
                        "unknown subscription in custody store",
                    ));
                }
            }
            for parent in &record.parents {
                if !self.record(parent)?.exposures.is_subset(&record.exposures) {
                    return Err(EngineError::invalid(
                        "custody store loses ancestor exposure",
                    ));
                }
            }
            if let Some(input) = &record.reconstruction {
                Reconstruction::verify(&input.definition, &input.digest)?;
            }
        }
        let mut visited = BTreeSet::new();
        loop {
            let next: Vec<_> = self
                .records
                .iter()
                .filter(|(id, record)| !visited.contains(*id) && record.parents.is_subset(&visited))
                .map(|(id, _)| id.clone())
                .collect();
            if next.is_empty() {
                break;
            }
            visited.extend(next);
        }
        if visited.len() != self.records.len() {
            return Err(EngineError::invalid("cyclic custody store"));
        }
        Ok(())
    }
}

#[derive(Default)]
struct RefreshState {
    epoch: u64,
    active: bool,
    uncertain: bool,
}

#[derive(Clone, Default)]
pub struct RefreshLineage {
    state: Arc<Mutex<RefreshState>>,
}

pub struct OfficialRun {
    lineage: RefreshLineage,
    completed: bool,
}

impl RefreshLineage {
    pub fn acquire(&self, captured_epoch: u64) -> Result<OfficialRun> {
        let mut state = self.state.lock().unwrap();
        if state.uncertain || captured_epoch != state.epoch {
            return Err(EngineError::new(
                "CONTINUATION_UNSUPPORTED",
                "stale or uncertain official session requires fresh vendor sign-in",
            ));
        }
        if state.active {
            return Err(EngineError::in_use("official session is already running"));
        }
        state.active = true;
        Ok(OfficialRun {
            lineage: self.clone(),
            completed: false,
        })
    }
}

impl OfficialRun {
    pub fn finish_after_quiescence(mut self) -> Result<u64> {
        let mut state = self.lineage.state.lock().unwrap();
        let next = state.epoch.checked_add(1).ok_or_else(|| {
            EngineError::new("CONTINUATION_UNSUPPORTED", "session lineage exhausted")
        })?;
        state.epoch = next;
        state.active = false;
        self.completed = true;
        Ok(next)
    }
}

impl Drop for OfficialRun {
    fn drop(&mut self) {
        if !self.completed {
            let mut state = self.lineage.state.lock().unwrap();
            state.active = false;
            state.uncertain = true;
        }
    }
}
