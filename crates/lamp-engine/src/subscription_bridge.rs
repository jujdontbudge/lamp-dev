use crate::engine::{Authority, Engine};
use crate::{EngineError, Result};
use std::io::{Read, Write};
use std::os::fd::AsRawFd;
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::net::UnixListener;
use std::sync::{
    Arc,
    atomic::{AtomicBool, Ordering},
};
use std::time::{Duration, Instant};

pub(crate) struct Bridge {
    pub uid: u32,
    stop: Arc<AtomicBool>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl Drop for Bridge {
    fn drop(&mut self) {
        self.stop.store(true, Ordering::Release);
        if let Some(thread) = self.thread.take() {
            let _ = thread.join();
        }
    }
}

pub fn admitted_frame(bytes: &[u8]) -> bool {
    bytes == b"NEXT\n"
}

impl Engine {
    pub fn ensure_subscription_bridge(self: &Arc<Self>, auth: Authority<'_>) -> Result<()> {
        if self.capture_trust(auth.sandbox_id)? != "owner_private" {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "bridge requires subscription custody",
            ));
        }
        let policy = self
            .cfg
            .subscription
            .as_ref()
            .ok_or_else(|| EngineError::invalid("subscription policy missing"))?;
        if policy.steps.is_empty()
            || policy.steps.len() > 8
            || policy.steps.iter().any(|step| {
                step.argv.is_empty()
                    || step.argv.len() > 32
                    || step
                        .argv
                        .iter()
                        .any(|arg| arg.len() > 4096 || arg.contains('\0'))
                    || !step.cwd.starts_with('/')
                    || step.timeout_s == 0
                    || step.timeout_s > 120
            })
        {
            return Err(EngineError::invalid("bounded task plan missing or invalid"));
        }
        let sandbox = self.sandbox(auth.sandbox_id)?;
        let sandbox = sandbox.lock().unwrap();
        if sandbox.generation != auth.generation || sandbox.policy_revision != auth.policy_revision
        {
            return Err(EngineError::new(
                "REVISION_CONFLICT",
                "bridge authority changed",
            ));
        }
        let uid = sandbox.uid;
        let path = sandbox
            .jail
            .as_ref()
            .ok_or_else(|| EngineError::in_use("bridge jail absent"))?
            .path("v.sock_5500");
        drop(sandbox);
        let mut bridges = self.bridges.lock().unwrap();
        if bridges
            .get(auth.sandbox_id)
            .is_some_and(|bridge| bridge.uid == uid)
            && path.exists()
        {
            return Ok(());
        }
        bridges.remove(auth.sandbox_id);
        let listener = UnixListener::bind(&path)?;
        crate::fsutil::chown(&path, uid, uid)?;
        crate::fsutil::chmod(&path, 0o600)?;
        listener.set_nonblocking(true)?;
        let stop = Arc::new(AtomicBool::new(false));
        let stopping = stop.clone();
        let weak = Arc::downgrade(self);
        let source = auth.sandbox_id.to_string();
        let generation = auth.generation;
        let thread = std::thread::spawn(move || {
            while !stopping.load(Ordering::Acquire) {
                let (mut stream, _) = match listener.accept() {
                    Ok(connection) => connection,
                    Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                        std::thread::sleep(Duration::from_millis(10));
                        continue;
                    }
                    Err(_) => break,
                };
                let Some(engine) = weak.upgrade() else { break };
                let mut credentials: libc::ucred = unsafe { std::mem::zeroed() };
                let mut size = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
                if unsafe {
                    libc::getsockopt(
                        stream.as_raw_fd(),
                        libc::SOL_SOCKET,
                        libc::SO_PEERCRED,
                        (&mut credentials as *mut libc::ucred).cast(),
                        &mut size,
                    )
                } != 0
                    || credentials.uid != uid
                {
                    continue;
                }
                let _ = stream.set_read_timeout(Some(Duration::from_secs(2)));
                let _ = stream.set_write_timeout(Some(Duration::from_secs(2)));
                let mut frame = Vec::new();
                let valid =
                    (&mut stream).take(6).read_to_end(&mut frame).is_ok() && admitted_frame(&frame);
                let result = if valid {
                    engine.bridge_step(&source, generation)
                } else {
                    Err(EngineError::new(
                        "SCOPE_DENIED",
                        "bridge accepts only the fixed next-step signal",
                    ))
                };
                let reply = match result {
                    Ok(value) => value,
                    Err(_) => serde_json::json!({"status":"denied"}),
                };
                if let Ok(mut bytes) = serde_json::to_vec(&reply) {
                    bytes.push(b'\n');
                    let _ = stream.write_all(&bytes);
                }
            }
        });
        bridges.insert(
            auth.sandbox_id.into(),
            Bridge {
                uid,
                stop,
                thread: Some(thread),
            },
        );
        Ok(())
    }

    fn bridge_step(&self, source: &str, generation: u64) -> Result<serde_json::Value> {
        let policy = self
            .cfg
            .subscription
            .as_ref()
            .ok_or_else(|| EngineError::invalid("subscription policy missing"))?;
        let index = self
            .custody
            .as_ref()
            .unwrap()
            .lock()
            .unwrap()
            .transact(|graph| graph.next_bridge_step(source, policy.steps.len()))?;
        let step = policy
            .steps
            .get(index)
            .ok_or_else(|| EngineError::new("SCOPE_DENIED", "approved task plan exhausted"))?;
        let mut task = self.bridge_task.lock().unwrap();
        if task.is_none() {
            let template = self.template(&policy.task_template)?;
            let resources = template.variant.resources.clone();
            let id = format!(
                "sb_task_{}",
                lamp_init::hex_encode(&crate::fsutil::random_bytes::<8>())
            );
            let auth = Authority {
                sandbox_id: &id,
                generation: 1,
                policy_revision: policy_revision(self),
            };
            self.allocate(auth, Some(resources))?;
            self.restore(auth, &policy.task_template)?;
            *task = Some(id);
        }
        let task = task.as_ref().unwrap();
        if self.capture_trust(task)? != "clean" {
            return Err(EngineError::new(
                "SCOPE_DENIED",
                "task sandbox is not token-free",
            ));
        }
        let mut audit = std::fs::OpenOptions::new()
            .create(true)
            .append(true)
            .mode(0o600)
            .open(self.cfg.store.join("bridge.jsonl"))?;
        serde_json::to_writer(&mut audit, &serde_json::json!({"event":"bridge_step","source":source,"generation":generation,"task":task,"step":index}))
            .map_err(|_| EngineError::host("cannot encode bridge audit"))?;
        audit.write_all(b"\n")?;
        audit.sync_all()?;
        let operation = format!(
            "op_bridge_{}",
            lamp_init::hex_encode(&crate::fsutil::random_bytes::<8>())
        );
        let auth = Authority {
            sandbox_id: task,
            generation: 1,
            policy_revision: policy_revision(self),
        };
        self.start(
            auth,
            &operation,
            step.argv.clone(),
            step.cwd.clone(),
            Default::default(),
            step.timeout_s * 1000,
        )?;
        let deadline = Instant::now() + Duration::from_secs(step.timeout_s + 5);
        loop {
            let result = self.inspect(task, &operation)?;
            if let Some(command) = result.command
                && command.finished_unix_ns != 0
            {
                let decoded = lamp_init::b64_decode(&command.stdout_b64)
                    .ok_or_else(|| EngineError::host("invalid task output encoding"))?;
                let output = String::from_utf8_lossy(&decoded[..decoded.len().min(4096)]);
                return Ok(
                    serde_json::json!({"status":"completed","step":index,"exit_code":command.exit_code,"output":output}),
                );
            }
            if Instant::now() > deadline {
                return Err(EngineError::new(
                    "OUTCOME_UNKNOWN",
                    "task did not complete inside its fixed bound",
                ));
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}

fn policy_revision(engine: &Engine) -> u64 {
    engine
        .cfg
        .network
        .as_ref()
        .map_or(1, |policy| policy.revision)
}
