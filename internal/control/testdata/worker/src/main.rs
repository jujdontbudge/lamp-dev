//! Test-only transport peer. No hypervisor, isolation, or memory-fork claim.
//! Accepts only the test's fixed probe/marker commands, never arbitrary host exec.
use lamp_worker_protocol::*;
use std::{
    collections::{HashMap, HashSet},
    env, fs,
    os::unix::net::{UnixListener, UnixStream},
    path::PathBuf,
    process::Command,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

// Only echo a bounded inert JSON marker. This extends the transport diagnostic,
// never allows arbitrary host commands, shell text or environment overrides.
fn diagnostic_echo(argv: &[String]) -> bool {
    argv.len() == 2
        && argv[0] == "/bin/echo"
        && argv[1].len() <= 512
        && argv[1].starts_with("{\"principal_id\":")
        && argv[1].contains("\"nonce\":")
}

fn serve(
    mut stream: UnixStream,
    directory: &std::path::Path,
) -> Result<(), Box<dyn std::error::Error>> {
    stream.set_read_timeout(Some(Duration::from_secs(60)))?;
    stream.set_write_timeout(Some(Duration::from_secs(5)))?;
    let hello = read_frame(&mut stream)?;
    let Some(frame::Payload::Hello(hello)) = hello.payload else {
        return Err("Hello required".into());
    };
    if hello.protocol != "lamp.worker/1"
        || hello.worker_id != "worker-test"
        || hello.boot_id != "boot-test"
        || hello.max_control_bytes != MAX_CONTROL_BYTES as u32
    {
        return Err("wrong assignment".into());
    }
    write_frame(
        &mut stream,
        &Frame {
            payload: Some(frame::Payload::Hello(Hello {
                protocol: "lamp.worker/1".into(),
                worker_id: "worker-test".into(),
                boot_id: "boot-test".into(),
                max_control_bytes: MAX_CONTROL_BYTES as u32,
            })),
        },
    )?;
    let mut allocated = HashSet::new();
    let mut restored = HashSet::new();
    let mut captures = HashSet::new();
    let mut operations = HashSet::new();
    let mut commands = HashMap::new();
    loop {
        let input = match read_frame(&mut stream) {
            Ok(frame) => frame,
            Err(_) => return Ok(()),
        };
        let Some(frame::Payload::Request(request)) = input.payload else {
            return Err("request required".into());
        };
        let context = request.context.ok_or("context required")?;
        if context.request_id.is_empty()
            || context.operation_id.is_empty()
            || !operations.insert(context.operation_id.clone())
            || context.sandbox_id.is_empty()
            || context.generation != 1
            || context.policy_revision != 1
            || context.grant_id != "grant-test"
            || context.deadline_ms
                <= SystemTime::now().duration_since(UNIX_EPOCH)?.as_millis() as u64
        {
            return Err("invalid context or duplicate effect".into());
        }
        let mut command = None;
        let mut detail = HashMap::new();
        let state = match request.command.ok_or("command required")? {
            request::Command::Allocate(allocate) => {
                let r = allocate.resources.ok_or("resources required")?;
                if r.vcpus != 2 || r.memory_mib != 4096 || r.disk_gib != 20 || r.pids_max != 4096 {
                    return Err("resource mapping changed".into());
                }
                allocated.insert(context.sandbox_id.clone());
                "allocated"
            }
            request::Command::Restore(restore) => {
                if !allocated.contains(&context.sandbox_id)
                    || (restore.capture_id != "tpl_demo" && !captures.contains(&restore.capture_id))
                {
                    return Err("invalid template/capture mapping".into());
                }
                restored.insert(context.sandbox_id.clone());
                "restored"
            }
            request::Command::Start(start) => {
                if !restored.contains(&context.sandbox_id) {
                    return Err("start before restore".into());
                }
                let marker = directory.join("independent-command-marker");
                let marker_argv = vec![
                    "/bin/sh".to_string(),
                    "-c".to_string(),
                    "printf control-path-ok > \"$1\"".to_string(),
                    "fixture".to_string(),
                    marker.to_string_lossy().into_owned(),
                ];
                if start.argv != ["/bin/true"]
                    && start.argv != marker_argv
                    && start.argv != ["/bin/echo", "{\"fixture\":\"ok\"}"]
                    && !diagnostic_echo(&start.argv)
                {
                    return Err("fixture rejects arbitrary host commands".into());
                }
                // Translate the Linux guest probe only inside this host fixture:
                // macOS provides true at /usr/bin/true, not /bin/true.
                let executable = if start.argv == ["/bin/true"] {
                    "/usr/bin/true"
                } else {
                    &start.argv[0]
                };
                let output = Command::new(executable)
                    .args(&start.argv[1..])
                    .env_clear()
                    .current_dir(directory)
                    .output()?;
                if !output.status.success() {
                    return Err("probe failed".into());
                }
                let now = SystemTime::now().duration_since(UNIX_EPOCH)?.as_nanos() as u64;
                commands.insert(
                    context.operation_id.clone(),
                    CommandResult {
                        exit_code: output.status.code().unwrap_or(-1),
                        stdout_tail: output.stdout,
                        stderr_tail: output.stderr,
                        started_at_unix_ns: now,
                        finished_at_unix_ns: now,
                        ..Default::default()
                    },
                );
                fs::write(
                    directory.join(format!("{}.started", context.sandbox_id)),
                    b"started\n",
                )?;
                "command_started"
            }
            request::Command::Capture(capture) => {
                if !restored.contains(&context.sandbox_id)
                    || !capture.preserves_memory
                    || !["crash", "local"].contains(&capture.consistency.as_str())
                {
                    return Err("invalid capture mapping".into());
                }
                captures.insert(capture.capture_id);
                "captured"
            }
            request::Command::Inspect(inspect) => {
                command = commands.get(&inspect.command_operation_id).cloned();
                detail.insert(
                    "command_state".into(),
                    if command.is_some() {
                        "exited"
                    } else {
                        "unknown"
                    }
                    .into(),
                );
                "inspected"
            }
            request::Command::Stop(_) => "stopped",
            request::Command::Release(_) => {
                allocated.remove(&context.sandbox_id);
                restored.remove(&context.sandbox_id);
                "released"
            }
            _ => return Err("unexpected fixture command".into()),
        };
        write_frame(
            &mut stream,
            &Frame {
                payload: Some(frame::Payload::Response(Response {
                    context: Some(context),
                    result: Some(response::Result::Receipt(Receipt {
                        state: state.into(),
                        committed_handle_ids: vec![],
                        outcome_unknown: false,
                        command,
                        detail,
                    })),
                })),
            },
        )?;
    }
}
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<_> = env::args().collect();
    let directory = PathBuf::from(args.get(2).ok_or("temporary directory required")?);
    let listener = UnixListener::bind(args.get(1).ok_or("socket required")?)?;
    let (stream, _) = listener.accept()?;
    serve(stream, &directory)
}
