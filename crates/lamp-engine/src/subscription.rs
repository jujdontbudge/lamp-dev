use crate::custody::{CapturePurpose, Destination, Harness, Reconstruction};
use crate::engine::{Engine, Sandbox};
use crate::{EngineError, Result};
use serde::{Deserialize, Serialize};
use std::collections::BTreeMap;

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Registration {
    pub harness: Harness,
    pub definition: String,
    pub approved_nonsecret_digest: String,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct SubscriptionPolicy {
    pub owner_id: String,
    pub custody_host_ids: Vec<String>,
    pub templates: BTreeMap<String, Registration>,
    pub task_template: String,
    pub steps: Vec<BridgeStep>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct BridgeStep {
    pub argv: Vec<String>,
    pub cwd: String,
    pub timeout_s: u64,
}

#[derive(Default, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct HarnessReceipt {
    pub ok: bool,
    #[serde(default)]
    pub refreshed: bool,
    #[serde(default)]
    pub signed_in: bool,
    #[serde(default)]
    pub quiesced: bool,
    #[serde(default)]
    pub pid: u32,
    #[serde(default)]
    pub turns: u64,
    #[serde(default)]
    pub heap_canary_present: bool,
    #[serde(default)]
    pub disk_canary_present: bool,
}

pub fn native_run(argv: &[String]) -> Option<bool> {
    if argv.len() != 2 || argv[0] != "/usr/local/bin/lamp-subscription" {
        return None;
    }
    match argv[1].as_str() {
        "sign-in" => Some(true),
        "start" | "turn" | "refresh" | "stop" => Some(false),
        _ => None,
    }
}

pub fn domains(harness: Harness) -> Vec<String> {
    match harness {
        Harness::ClaudeCode => vec![
            "api.anthropic.com",
            "claude.ai",
            "platform.claude.com",
            "console.anthropic.com",
        ],
        Harness::Codex => vec!["auth.openai.com", "chatgpt.com", "api.openai.com"],
    }
    .into_iter()
    .map(str::to_string)
    .collect()
}

impl Engine {
    pub(crate) fn authorize_subscription_command(
        &self,
        id: &str,
        argv: &[String],
        cwd: &str,
        env: &BTreeMap<String, String>,
    ) -> Result<()> {
        if self.capture_trust(id)? == "owner_private" {
            let readiness = argv == ["/bin/true"];
            let harness = argv.len() == 2
                && argv[0] == "/usr/local/bin/lamp-subscription"
                && matches!(
                    argv[1].as_str(),
                    "status" | "sign-in" | "start" | "turn" | "refresh" | "proof" | "stop"
                );
            let initialize = argv.len() == 3
                && argv[0] == "/usr/local/bin/lamp-subscription"
                && argv[1] == "init"
                && argv[2].len() == 32
                && argv[2]
                    .bytes()
                    .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte));
            if (!readiness && !harness && !initialize)
                || !env.is_empty()
                || (!cwd.is_empty() && cwd != "/")
            {
                return Err(EngineError::new(
                    "SCOPE_DENIED",
                    "subscription sandbox only accepts bounded harness operations",
                ));
            }
        }
        Ok(())
    }

    pub(crate) fn finish_subscription_report(
        &self,
        id: &str,
        operation: &str,
        done: &mut lamp_init::ExecDone,
        lost: bool,
    ) -> Result<()> {
        if self.capture_trust(id)? != "owner_private" {
            return Ok(());
        }
        if done.finished_unix_ns == 0 && !lost {
            done.stdout_b64.clear();
            done.stderr_b64.clear();
            return Ok(());
        }
        let mut store = self.custody.as_ref().unwrap().lock().unwrap();
        let pending = store.graph()?.has_run(id, operation)?;
        let receipt = lamp_init::b64_decode(&done.stdout_b64)
            .filter(|bytes| bytes.len() <= 8192)
            .and_then(|bytes| serde_json::from_slice::<HarnessReceipt>(&bytes).ok())
            .unwrap_or_else(|| HarnessReceipt {
                ok: !pending && !lost && done.exit_code == 0 && done.stdout_b64.is_empty(),
                ..Default::default()
            });
        let success = receipt.ok && done.exit_code == 0 && !lost && (!pending || receipt.quiesced);
        if pending && (done.finished_unix_ns != 0 || lost) {
            store.transact(|graph| graph.finish_run(id, operation, success, receipt.refreshed))?;
        }
        done.stdout_b64 = lamp_init::b64_encode(&serde_json::to_vec(&receipt).unwrap());
        done.stderr_b64.clear();
        if done.finished_unix_ns != 0 && !success {
            done.exit_code = 1;
        }
        Ok(())
    }

    pub fn custody_owner(&self) -> &str {
        self.cfg
            .subscription
            .as_ref()
            .map_or("pr_local", |policy| &policy.owner_id)
    }

    pub fn capture_trust(&self, id: &str) -> Result<&'static str> {
        let custody = self
            .custody
            .as_ref()
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody is read-only"))?
            .lock()
            .unwrap();
        Ok(match custody.graph()?.trust(id)? {
            crate::custody::CaptureTrust::Clean => "clean",
            crate::custody::CaptureTrust::OwnerPrivate => "owner_private",
        })
    }

    pub fn authorize_transfer(
        &self,
        id: &str,
        purpose: CapturePurpose,
        principal: &str,
    ) -> Result<()> {
        self.custody
            .as_ref()
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?
            .lock()
            .unwrap()
            .graph()?
            .authorize(
                id,
                Destination {
                    principal,
                    host: &self.cfg.host_id,
                    online: true,
                },
                purpose,
            )
    }

    pub fn rebuild_private_state(&self, id: &str) -> Result<crate::build::BuildReport> {
        let input = self
            .custody
            .as_ref()
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?
            .lock()
            .unwrap()
            .graph()?
            .reconstruction(id)?;
        input.build(self)
    }

    pub(crate) fn prepare_subscription_restore(
        &self,
        sandbox: &mut Sandbox,
        source: &str,
        capture: bool,
        register_subscription: bool,
    ) -> Result<()> {
        let registration = if capture || !register_subscription {
            None
        } else if let Some(policy) = &self.cfg.subscription {
            let template = self.template(source)?;
            policy
                .templates
                .get(&template.id)
                .or_else(|| policy.templates.get(&template.manifest.name))
                .cloned()
        } else {
            None
        };
        let custody = self
            .custody
            .as_ref()
            .ok_or_else(|| EngineError::new("DATA_UNAVAILABLE", "custody unavailable"))?;
        let mut custody = custody.lock().unwrap();
        let harness = if capture {
            custody.graph()?.harness(source)?
        } else {
            registration.as_ref().map(|record| record.harness)
        };
        if let Some(harness) = harness {
            if self.cfg.subscription.is_none() {
                return Err(EngineError::new(
                    "AUTH_TRANSPORT_UNSUPPORTED",
                    "subscription custody policy is required",
                ));
            }
            let mut network = self.cfg.network.clone().ok_or_else(|| {
                EngineError::new(
                    "AUTH_TRANSPORT_UNSUPPORTED",
                    "subscription requires vendor-only guard",
                )
            })?;
            if network.principal_id != self.custody_owner() || !network.exceptions.is_empty() {
                return Err(EngineError::new(
                    "SCOPE_DENIED",
                    "subscription guard identity or extra connections refused",
                ));
            }
            network.subscription_domains = Some(domains(harness));
            sandbox.network_policy = Some(network);
            if let Some(pending) = sandbox.pending_net.take()
                && let Ok((uid, attachment)) = pending.join()
            {
                drop(attachment);
                self.free_uid(uid);
            }
        }
        custody.transact(|graph| {
            if capture {
                let destination = Destination {
                    principal: self.custody_owner(),
                    host: &self.cfg.host_id,
                    online: true,
                };
                let original = self.captures.lock().unwrap().get(source).cloned();
                if original.is_some_and(|capture| {
                    capture.source_sandbox == sandbox.id
                        && capture.source_generation == sandbox.generation
                }) {
                    graph.authorize(source, destination, CapturePurpose::OwnerPrivate)?;
                } else {
                    graph.inherit(source, &sandbox.id, destination)?;
                }
            } else if let Some(registration) = &registration {
                let input = Reconstruction::verify(
                    &registration.definition,
                    &registration.approved_nonsecret_digest,
                )?;
                graph.record_reconstruction(&sandbox.id, input)?;
                let reference = format!(
                    "cr_{}",
                    &crate::manifest::sha256_hex(sandbox.id.as_bytes())[..32]
                );
                if !graph.has_subscription(&reference) {
                    graph.register_subscription(
                        &reference,
                        registration.harness,
                        self.cfg
                            .subscription
                            .as_ref()
                            .unwrap()
                            .custody_host_ids
                            .iter()
                            .map(String::as_str),
                    )?;
                }
                graph.expose(&sandbox.id, &reference)?;
            }
            Ok(())
        })
    }
}
