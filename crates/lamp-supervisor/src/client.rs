//! Minimal lamp.worker/1 client: Hello exchange and sequential requests.
use lamp_worker_protocol::*;
use std::io;
use std::os::unix::net::UnixStream;
use std::path::Path;
use std::sync::atomic::{AtomicU64, Ordering};
use std::time::{SystemTime, UNIX_EPOCH};

pub struct Client {
    stream: UnixStream,
    pub server: Hello,
    seq: AtomicU64,
    /// Reference to the control grant the supervisor installed for us
    /// (`LAMP_GRANT_ID`), and the policy revision it covers.
    pub grant_id: String,
    pub policy_revision: u64,
}

pub fn now_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as u64)
        .unwrap_or(0)
}

fn bad(msg: impl Into<String>) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, msg.into())
}

impl Client {
    pub fn connect(path: &Path, worker_id: &str) -> io::Result<Client> {
        let mut stream = UnixStream::connect(path)?;
        let hello = Frame {
            payload: Some(frame::Payload::Hello(Hello {
                protocol: PROTOCOL.into(),
                worker_id: worker_id.into(),
                boot_id: std::fs::read_to_string("/proc/sys/kernel/random/boot_id")
                    .map(|s| s.trim().to_string())
                    .unwrap_or_else(|_| "unknown".into()),
                max_control_bytes: MAX_CONTROL_BYTES as u32,
            })),
        };
        write_frame(&mut stream, &hello)?;
        let server = match read_frame(&mut stream)?.payload {
            Some(frame::Payload::Hello(h)) if h.protocol == PROTOCOL => h,
            other => return Err(bad(format!("expected lamp.worker/1 Hello, got {other:?}"))),
        };
        Ok(Client {
            stream,
            server,
            seq: AtomicU64::new(1),
            grant_id: std::env::var("LAMP_GRANT_ID").unwrap_or_default(),
            policy_revision: 1,
        })
    }

    /// Builds a context with fresh request id and the given operation id.
    pub fn context(
        &self,
        operation_id: &str,
        sandbox_id: &str,
        generation: u64,
        deadline_ms: u64,
    ) -> Context {
        Context {
            request_id: format!(
                "rq_{}_{}",
                std::process::id(),
                self.seq.fetch_add(1, Ordering::Relaxed)
            ),
            operation_id: operation_id.into(),
            deadline_ms: now_ms() + deadline_ms,
            sandbox_id: sandbox_id.into(),
            generation,
            grant_id: self.grant_id.clone(),
            policy_revision: self.policy_revision,
        }
    }

    /// Waits for a command started with operation `start_op` to exit, by
    /// polling `Inspect{command_operation_id}`.
    pub fn wait_command(
        &mut self,
        sandbox_id: &str,
        generation: u64,
        start_op: &str,
        timeout: std::time::Duration,
    ) -> io::Result<CommandResult> {
        let deadline = std::time::Instant::now() + timeout;
        let mut n = 0;
        loop {
            n += 1;
            let ctx = self.context(
                &format!("{start_op}.wait{n}"),
                sandbox_id,
                generation,
                10_000,
            );
            let r = self.receipt(
                ctx,
                request::Command::Inspect(Inspect {
                    command_operation_id: start_op.into(),
                }),
            )?;
            match r.detail.get("command_state").map(String::as_str) {
                Some("exited") => {
                    return r
                        .command
                        .ok_or_else(|| bad("exited command without result"));
                }
                Some("running") => {}
                other => return Err(io::Error::other(format!("command state {other:?}"))),
            }
            if std::time::Instant::now() > deadline {
                return Err(io::Error::new(
                    io::ErrorKind::TimedOut,
                    "command still running",
                ));
            }
            std::thread::sleep(std::time::Duration::from_millis(1));
        }
    }

    pub fn call(&mut self, context: Context, command: request::Command) -> io::Result<Response> {
        let request_id = context.request_id.clone();
        let frame = Frame {
            payload: Some(frame::Payload::Request(Request {
                context: Some(context),
                command: Some(command),
            })),
        };
        write_frame(&mut self.stream, &frame)?;
        match read_frame(&mut self.stream)?.payload {
            Some(frame::Payload::Response(r)) => {
                if r.context.as_ref().map(|c| c.request_id.as_str()) != Some(request_id.as_str()) {
                    return Err(bad("response for another request"));
                }
                Ok(r)
            }
            other => Err(bad(format!("expected Response, got {other:?}"))),
        }
    }

    /// Calls and converts a WorkerError into an io::Error.
    pub fn receipt(&mut self, context: Context, command: request::Command) -> io::Result<Receipt> {
        match self.call(context, command)?.result {
            Some(response::Result::Receipt(r)) => Ok(r),
            Some(response::Result::Error(e)) => {
                Err(io::Error::other(format!("{}: {}", e.code, e.message)))
            }
            None => Err(bad("empty response")),
        }
    }
}
