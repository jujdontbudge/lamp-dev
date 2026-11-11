//! Signed, typed, single-use handle grants (lamp.worker/1 HandleGrant).
use crate::{HandleGrant, HandleKind};
use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use prost::Message;
use std::collections::{HashMap, HashSet};
use std::sync::Mutex;

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct GrantError(pub &'static str);

impl std::fmt::Display for GrantError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.0)
    }
}
impl std::error::Error for GrantError {}

/// Bytes covered by the signature: deterministic Protobuf with signature cleared.
pub fn signing_bytes(grant: &HandleGrant) -> Vec<u8> {
    let mut unsigned = grant.clone();
    unsigned.signature.clear();
    unsigned.encode_to_vec()
}

pub fn sign_grant(grant: &mut HandleGrant, key: &SigningKey) {
    grant.signature = key.sign(&signing_bytes(grant)).to_bytes().to_vec();
}

/// What the receiving worker knows about itself and the request.
#[derive(Debug, Clone)]
pub struct Expectation<'a> {
    pub worker_id: &'a str,
    pub uid: u64,
    pub pid: u64,
    pub boot_id: &'a str,
    pub sandbox_id: &'a str,
    pub generation: u64,
    pub policy_revision: u64,
    pub now_ms: u64,
}

/// Verifies signature, binding and expiry. `issuers` maps issuer_key_id to key.
pub fn verify_grant(
    grant: &HandleGrant,
    issuers: &HashMap<String, VerifyingKey>,
    want: &Expectation<'_>,
) -> Result<(), GrantError> {
    let key = issuers
        .get(&grant.issuer_key_id)
        .ok_or(GrantError("unknown grant issuer"))?;
    let signature = Signature::from_slice(&grant.signature)
        .map_err(|_| GrantError("malformed grant signature"))?;
    key.verify(&signing_bytes(grant), &signature)
        .map_err(|_| GrantError("bad grant signature"))?;
    if grant.grant_id.is_empty() || grant.kind == HandleKind::Unspecified as i32 {
        return Err(GrantError("grant id/kind missing"));
    }
    if HandleKind::try_from(grant.kind).is_err() {
        return Err(GrantError("unknown handle kind"));
    }
    if grant.receiver_worker_id != want.worker_id
        || grant.receiver_uid != want.uid
        || grant.receiver_pid != want.pid
        || grant.receiver_boot_id != want.boot_id
    {
        return Err(GrantError("grant bound to another receiver"));
    }
    if grant.sandbox_id != want.sandbox_id
        || grant.generation != want.generation
        || grant.policy_revision != want.policy_revision
    {
        return Err(GrantError(
            "grant bound to another sandbox/generation/policy",
        ));
    }
    if grant.expires_at_ms <= want.now_ms {
        return Err(GrantError("grant expired"));
    }
    Ok(())
}

/// Remembers consumed grant IDs so a copied grant cannot be used twice.
#[derive(Default)]
pub struct ConsumedGrants(Mutex<HashSet<String>>);

impl ConsumedGrants {
    pub fn consume(&self, grant_id: &str) -> Result<(), GrantError> {
        if self.0.lock().unwrap().insert(grant_id.to_string()) {
            Ok(())
        } else {
            Err(GrantError("grant already consumed"))
        }
    }
}

/// Checks that descriptor indices are unique, in range and cover every
/// received descriptor exactly once.
pub fn check_indices(grants: &[HandleGrant], descriptor_count: usize) -> Result<(), GrantError> {
    let mut seen = HashSet::new();
    for g in grants {
        let i = g.descriptor_index as usize;
        if i >= descriptor_count {
            return Err(GrantError("descriptor index out of range"));
        }
        if !seen.insert(i) {
            return Err(GrantError("descriptor index reused"));
        }
    }
    if seen.len() != descriptor_count {
        return Err(GrantError("unclaimed descriptors"));
    }
    Ok(())
}
