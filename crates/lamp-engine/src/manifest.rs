//! Template manifests, object digests and signed capture provenance.
//!
//! Object digest `sha256-c4m`: SHA-256 over the concatenated SHA-256 digests
//! of each 4 MiB chunk (the last chunk may be shorter). It is verifiable in
//! parallel and cheap over sparse disk and memory images. The on-disk layout
//! is not frozen here (T14 freezes it).
use crate::error::{EngineError, Result};
use ed25519_dalek::{Signature, Signer, SigningKey, Verifier, VerifyingKey};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

pub const CHUNK: usize = 4 << 20;
pub const DIGEST_ALG: &str = "sha256-c4m";

pub fn sha256_hex(data: &[u8]) -> String {
    lamp_init::hex_encode(&Sha256::digest(data))
}

/// Digest of one all-zero chunk of `len` bytes.
pub fn zero_chunk_digest(len: usize) -> [u8; 32] {
    let zeros = vec![0_u8; len];
    Sha256::digest(&zeros).into()
}

/// Combines per-chunk digests into the object digest.
pub fn combine(chunks: &[[u8; 32]]) -> String {
    let mut h = Sha256::new();
    for c in chunks {
        h.update(c);
    }
    lamp_init::hex_encode(&h.finalize())
}

/// In-memory reference implementation (tests and small objects).
pub fn digest_bytes(data: &[u8]) -> String {
    let chunks: Vec<[u8; 32]> = data
        .chunks(CHUNK)
        .map(|c| Sha256::digest(c).into())
        .collect();
    combine(&chunks)
}

/// Canonical JSON: object keys sorted, no insignificant whitespace.
pub fn canonical_json<T: Serialize>(value: &T) -> Result<Vec<u8>> {
    // serde_json::Value uses a sorted map unless `preserve_order` is enabled.
    let v = serde_json::to_value(value).map_err(|e| EngineError::host(e.to_string()))?;
    serde_json::to_vec(&v).map_err(|e| EngineError::host(e.to_string()))
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Object {
    pub digest: String,
    pub size: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Compatibility {
    pub engine: String,
    pub engine_digest: String,
    pub state_abi: String,
    pub guest_kernel_digest: String,
    pub cpu_baseline: String,
    pub device_graph_digest: String,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Resources {
    pub vcpus: u32,
    pub memory_mib: u64,
    pub disk_gib: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AccelerationVariant {
    pub compatibility: Compatibility,
    pub resources: Resources,
    pub memory: Object,
    pub vmstate: Object,
    pub disk: Object,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct StepRecord {
    pub argv: Vec<String>,
    pub exit_code: i32,
    pub elapsed_ms: u64,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Manifest {
    pub format: String,
    pub digest_alg: String,
    pub name: String,
    pub guest_os: String,
    pub arch: String,
    pub seed_ref: String,
    pub definition_sha256: String,
    pub package_lock_blob_id: Option<String>,
    pub network: String,
    pub kernel: Object,
    pub kernel_cmdline: String,
    pub root: Object,
    pub steps: Vec<StepRecord>,
    pub built_at_unix_ms: u64,
    pub builder: String,
    pub acceleration_variants: Vec<AccelerationVariant>,
}

impl Manifest {
    /// SHA-256 of the canonical JSON; the template's immutable identity.
    pub fn digest(&self) -> Result<String> {
        Ok(sha256_hex(&canonical_json(self)?))
    }
}

pub fn template_id(manifest_digest: &str) -> String {
    format!("tp_{}", &manifest_digest[..26])
}

/// Architecture §6.1 capture provenance, signed by an enrolled producer key.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Provenance {
    pub producer_key_id: String,
    pub producer_host_id: String,
    pub owner_id: String,
    pub trust_class: String,
    pub manifest_digest: String,
    pub engine_digest: String,
    pub guest_kernel_digest: String,
    pub device_graph_digest: String,
    pub cpu_baseline: String,
    pub signature: String,
}

impl Provenance {
    fn signing_bytes(&self) -> Result<Vec<u8>> {
        let mut unsigned = self.clone();
        unsigned.signature.clear();
        canonical_json(&unsigned)
    }

    pub fn sign(&mut self, key: &SigningKey) -> Result<()> {
        self.signature = lamp_init::hex_encode(&key.sign(&self.signing_bytes()?).to_bytes());
        Ok(())
    }

    pub fn verify(&self, key: &VerifyingKey) -> Result<()> {
        let bytes = lamp_init::hex_decode(&self.signature).ok_or_else(|| {
            EngineError::new("DATA_UNAVAILABLE", "malformed provenance signature")
        })?;
        let sig = Signature::from_slice(&bytes)
            .map_err(|_| EngineError::new("DATA_UNAVAILABLE", "malformed provenance signature"))?;
        key.verify(&self.signing_bytes()?, &sig).map_err(|_| {
            EngineError::new("DATA_UNAVAILABLE", "provenance signature does not verify")
        })
    }
}

/// Key id: first 16 hex characters of SHA-256 over the public key.
pub fn key_id(key: &VerifyingKey) -> String {
    format!("pk_{}", &sha256_hex(key.as_bytes())[..16])
}
