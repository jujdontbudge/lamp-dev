//! lamp.worker/1 server: peer-authenticated Unix socket, Hello negotiation,
//! context/authority checks, operation de-duplication and dispatch to the
//! engine. Go (lampd) decides operations and authority; this side checks
//! them again independently and enforces local limits.
use lamp_engine::EngineError;
use lamp_engine::engine::{Authority, ENGINE_NAME, Engine, Outcome};
use lamp_engine::manifest::Resources;
use lamp_worker_protocol::unix::{peer_cred, recv_frame_with_fds, send_frame_with_fds};
use lamp_worker_protocol::*;
use std::collections::{HashMap, VecDeque};
use std::os::fd::OwnedFd;
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::Path;
use std::sync::{Arc, Mutex};
use std::time::Duration;

const OPERATION_MEMORY: usize = 4096;

pub struct ServerConfig {
    pub allowed_uids: Vec<u32>,
    pub worker_id: String,
}

enum OpState {
    InProgress(String),
    Done(String, Box<Response>),
}

pub struct Server {
    engine: Arc<Engine>,
    cfg: ServerConfig,
    boot_id: String,
    ops: Mutex<(HashMap<String, OpState>, VecDeque<String>)>,
}

fn now_ms() -> u64 {
    lamp_engine::fsutil::now_ms()
}

fn error(code: &str, message: impl Into<String>, retryable: bool) -> response::Result {
    response::Result::Error(WorkerError {
        code: code.into(),
        message: message.into(),
        retryable,
    })
}

fn from_engine(e: EngineError) -> response::Result {
    error(e.code, e.message, e.retryable)
}

fn receipt(o: Outcome) -> response::Result {
    response::Result::Receipt(Receipt {
        state: o.state,
        committed_handle_ids: o.handles,
        outcome_unknown: false,
        command: o.command.map(|c| CommandResult {
            exit_code: c.exit_code,
            signal: c.signal,
            stdout_tail: lamp_init::b64_decode(&c.stdout_b64).unwrap_or_default(),
            stderr_tail: lamp_init::b64_decode(&c.stderr_b64).unwrap_or_default(),
            truncated: c.truncated,
            timed_out: c.timed_out,
            started_at_unix_ns: c.started_unix_ns,
            finished_at_unix_ns: c.finished_unix_ns,
        }),
        detail: o.detail.into_iter().collect(),
    })
}

/// A short fingerprint of the command so a reused operation id with a
/// different command is detected.
fn fingerprint(command: &request::Command) -> String {
    use prost_free::encode;
    encode(command)
}

mod prost_free {
    use lamp_worker_protocol::{Request, request};
    use prost::Message;
    pub fn encode(command: &request::Command) -> String {
        let r = Request {
            context: None,
            command: Some(command.clone()),
        };
        lamp_engine::manifest::sha256_hex(&r.encode_to_vec())
    }
}

impl Server {
    pub fn new(engine: Arc<Engine>, cfg: ServerConfig) -> Server {
        Server {
            engine,
            cfg,
            boot_id: std::fs::read_to_string("/proc/sys/kernel/random/boot_id")
                .map(|s| s.trim().to_string())
                .unwrap_or_default(),
            ops: Mutex::new((HashMap::new(), VecDeque::new())),
        }
    }

    pub fn serve(self: Arc<Self>, socket: &Path) -> std::io::Result<()> {
        if let Some(dir) = socket.parent() {
            std::fs::create_dir_all(dir)?;
        }
        let _ = std::fs::remove_file(socket);
        let listener = UnixListener::bind(socket)?;
        // Authorization is by peer credentials, checked on every connection.
        std::fs::set_permissions(socket, std::fs::Permissions::from_mode(0o666))?;
        eprintln!(
            "lamp-supervisor: serving lamp.worker/1 on {}",
            socket.display()
        );
        for stream in listener.incoming() {
            let Ok(stream) = stream else { continue };
            let me = self.clone();
            std::thread::spawn(move || {
                if let Err(e) = me.connection(stream) {
                    eprintln!("lamp-supervisor: connection closed: {e}");
                }
            });
        }
        Ok(())
    }

    fn connection(self: Arc<Self>, stream: UnixStream) -> std::io::Result<()> {
        let cred = peer_cred(&stream)?;
        if !self.cfg.allowed_uids.contains(&cred.uid) {
            return Err(std::io::Error::other(format!(
                "peer uid {} is not an assigned caller",
                cred.uid
            )));
        }
        stream.set_read_timeout(Some(Duration::from_secs(5)))?;
        let hello = Frame {
            payload: Some(frame::Payload::Hello(Hello {
                protocol: PROTOCOL.into(),
                worker_id: self.cfg.worker_id.clone(),
                boot_id: self.boot_id.clone(),
                max_control_bytes: MAX_CONTROL_BYTES as u32,
            })),
        };
        send_frame_with_fds(&stream, &hello, &[])?;
        let (first, fds) = recv_frame_with_fds(&stream)?;
        match first.payload {
            Some(frame::Payload::Hello(h))
                if h.protocol == PROTOCOL
                    && !h.worker_id.is_empty()
                    && h.max_control_bytes > 0
                    && fds.is_empty() => {}
            _ => {
                return Err(std::io::Error::other(
                    "peer did not send a valid lamp.worker/1 Hello",
                ));
            }
        }
        stream.set_read_timeout(None)?;
        let writer = Arc::new(Mutex::new(stream.try_clone()?));
        loop {
            let (frame, fds) = recv_frame_with_fds(&stream)?;
            let Some(frame::Payload::Request(request)) = frame.payload else {
                return Err(std::io::Error::other("expected Request"));
            };
            let me = self.clone();
            let writer = writer.clone();
            std::thread::spawn(move || {
                let context = request.context.clone();
                let result = me.handle(request, fds);
                let frame = Frame {
                    payload: Some(frame::Payload::Response(Response {
                        context,
                        result: Some(result),
                    })),
                };
                let w = writer.lock().unwrap();
                if send_frame_with_fds(&*w, &frame, &[]).is_err() {
                    let _ = w.shutdown(std::net::Shutdown::Both);
                }
            });
        }
    }

    fn handle(&self, request: Request, fds: Vec<OwnedFd>) -> response::Result {
        let Some(ctx) = request.context else {
            return error("INVALID_ARGUMENT", "context is required", false);
        };
        let Some(command) = request.command else {
            return error("INVALID_ARGUMENT", "command is required", false);
        };
        if ctx.request_id.is_empty() || ctx.operation_id.is_empty() {
            return error(
                "INVALID_ARGUMENT",
                "request_id and operation_id are required",
                false,
            );
        }
        if ctx.deadline_ms <= now_ms() {
            return error("RESULT_EXPIRED", "deadline already passed", false);
        }
        // No handle grants are accepted by this engine yet; every received
        // descriptor is closed when `fds` drops.
        let carries_handles = match &command {
            request::Command::Allocate(a) => !a.handles.is_empty(),
            request::Command::Capture(c) => !c.output_handles.is_empty(),
            request::Command::Restore(r) => !r.input_handles.is_empty(),
            _ => false,
        };
        if carries_handles || !fds.is_empty() {
            return error(
                "INVALID_ARGUMENT",
                "this engine accepts no handle grants",
                false,
            );
        }
        drop(fds);
        let global = matches!(command, request::Command::Inspect(_)) && ctx.sandbox_id.is_empty();
        if !global && (ctx.sandbox_id.is_empty() || ctx.generation == 0 || ctx.grant_id.is_empty())
        {
            return error(
                "INVALID_ARGUMENT",
                "sandbox commands need sandbox_id, generation and grant_id",
                false,
            );
        }
        let print = fingerprint(&command);
        let read_only = matches!(command, request::Command::Inspect(_));
        if !read_only {
            let mut ops = self.ops.lock().unwrap();
            match ops.0.get(&ctx.operation_id) {
                Some(OpState::Done(p, prior)) if *p == print => {
                    return prior
                        .result
                        .clone()
                        .unwrap_or_else(|| error("OUTCOME_UNKNOWN", "no prior result", false));
                }
                Some(OpState::InProgress(p)) if *p == print => {
                    return response::Result::Receipt(Receipt {
                        state: "in_progress".into(),
                        outcome_unknown: true,
                        ..Default::default()
                    });
                }
                Some(_) => {
                    return error(
                        "IDEMPOTENCY_CONFLICT",
                        "operation id reused with another command",
                        false,
                    );
                }
                None => {}
            }
            ops.0
                .insert(ctx.operation_id.clone(), OpState::InProgress(print.clone()));
            ops.1.push_back(ctx.operation_id.clone());
            while ops.1.len() > OPERATION_MEMORY {
                if let Some(old) = ops.1.pop_front() {
                    ops.0.remove(&old);
                }
            }
        }
        let auth = Authority {
            sandbox_id: &ctx.sandbox_id,
            generation: ctx.generation,
            policy_revision: ctx.policy_revision,
        };
        let remaining = ctx.deadline_ms.saturating_sub(now_ms());
        let result = match command {
            request::Command::Allocate(a) => {
                if !a.engine_id.is_empty()
                    && a.engine_id != ENGINE_NAME
                    && a.engine_id != "firecracker"
                {
                    Err(EngineError::new(
                        "ENGINE_UNQUALIFIED",
                        format!("engine {} is not on this host", a.engine_id),
                    ))
                } else {
                    let resources = a.resources.map(|r| Resources {
                        vcpus: if r.vcpus == 0 { 2 } else { r.vcpus },
                        memory_mib: if r.memory_mib == 0 {
                            4096
                        } else {
                            r.memory_mib
                        },
                        disk_gib: if r.disk_gib == 0 { 20 } else { r.disk_gib },
                    });
                    self.engine.allocate(auth, resources)
                }
            }
            request::Command::Restore(r) => {
                let mismatch = r.compatibility.as_ref().is_some_and(|c| {
                    !c.engine_digest.is_empty()
                        && c.engine_digest != self.engine.compat.engine_digest
                        || !c.cpu_baseline.is_empty()
                            && c.cpu_baseline != self.engine.compat.cpu_baseline
                });
                if mismatch {
                    Err(EngineError::new(
                        "ENGINE_UNQUALIFIED",
                        "saved state was produced for another engine/CPU",
                    ))
                } else {
                    self.engine.restore(auth, &r.capture_id)
                }
            }
            request::Command::Start(s) => self.engine.exec(
                auth,
                s.argv,
                s.cwd,
                s.env.into_iter().collect(),
                remaining.saturating_sub(250).max(1),
            ),
            request::Command::Capture(c) => {
                if !c.preserves_memory {
                    Err(EngineError::invalid(
                        "disk-only capture is not offered by the Linux engine; use a memory fork",
                    ))
                } else {
                    self.engine.capture(auth, &c.capture_id, &c.consistency)
                }
            }
            request::Command::Stop(_) => self.engine.stop(auth),
            request::Command::Release(_) => self.engine.release(auth),
            request::Command::Inspect(_) if global => Ok(self.global_inspect()),
            request::Command::Inspect(_) => self.engine.inspect(&ctx.sandbox_id),
            request::Command::Cancel(_) => Err(EngineError::invalid(
                "cancel is not supported; operations are short and atomic",
            )),
        };
        let result = match result {
            Ok(o) => receipt(o),
            Err(e) => from_engine(e),
        };
        if !read_only {
            let mut ops = self.ops.lock().unwrap();
            ops.0.insert(
                ctx.operation_id.clone(),
                OpState::Done(
                    print,
                    Box::new(Response {
                        context: None,
                        result: Some(result.clone()),
                    }),
                ),
            );
        }
        result
    }

    fn global_inspect(&self) -> Outcome {
        let mut o = Outcome {
            state: "serving".into(),
            ..Default::default()
        };
        for t in self.engine.templates() {
            o.handles.push(t.id.clone());
            o.detail
                .insert(format!("template.{}", t.id), t.manifest.name.clone());
        }
        o.detail.insert("engine".into(), ENGINE_NAME.into());
        o.detail.insert(
            "engine_digest".into(),
            self.engine.compat.engine_digest.clone(),
        );
        o.detail.insert(
            "cpu_baseline".into(),
            self.engine.compat.cpu_baseline.clone(),
        );
        o
    }
}
