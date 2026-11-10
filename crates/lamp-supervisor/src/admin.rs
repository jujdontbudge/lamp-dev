//! Root-only administration socket for durable capture state.
//!
//! One JSON request per line, one JSON reply per line:
//!
//! ```text
//! {"op":"status"}                          backend, pool and state summary
//! {"op":"list"}                            committed and damaged captures
//! {"op":"delete","capture":"cp_x"}         refused while it has descendants
//! {"op":"gc"}                              collect unreferenced objects/volumes
//! {"op":"compact","capture":"cp_x"}        flatten a chained memory object
//! {"op":"verify","capture":"cp_x"}         signature + compatibility + digests
//! {"op":"export","capture":"cp_x","dir":D} signed self-contained bundle
//! {"op":"import","dir":D}                  refused unless an enrolled producer
//! {"op":"fault","point":P,"side":S,"omit":O}  fault-injection builds only
//! ```
//!
//! Storage maintenance is not part of lamp.worker/1 (lampd does not drive
//! it yet); this socket is reachable by root on the host only.
use lamp_engine::engine::Engine;
use lamp_worker_protocol::unix::peer_cred;
use serde_json::{Value, json};
use std::io::{BufRead, BufReader, Write};
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::Path;
use std::sync::Arc;

pub fn serve(engine: Arc<Engine>, socket: &Path) -> std::io::Result<()> {
    let _ = std::fs::remove_file(socket);
    let listener = UnixListener::bind(socket)?;
    std::fs::set_permissions(socket, std::fs::Permissions::from_mode(0o600))?;
    std::thread::spawn(move || {
        for stream in listener.incoming().flatten() {
            let engine = engine.clone();
            std::thread::spawn(move || {
                if let Err(e) = connection(&engine, stream) {
                    eprintln!("lamp-supervisor: admin connection: {e}");
                }
            });
        }
    });
    Ok(())
}

fn connection(engine: &Engine, stream: UnixStream) -> std::io::Result<()> {
    if peer_cred(&stream)?.uid != 0 {
        return Err(std::io::Error::other("admin socket is root-only"));
    }
    let mut out = stream.try_clone()?;
    for line in BufReader::new(stream).lines() {
        let line = line?;
        let reply = match serde_json::from_str::<Value>(&line) {
            Ok(req) => handle(engine, &req),
            Err(e) => json!({"ok": false, "code": "INVALID_ARGUMENT", "message": e.to_string()}),
        };
        out.write_all(format!("{reply}\n").as_bytes())?;
    }
    Ok(())
}

fn field<'a>(req: &'a Value, name: &str) -> Result<&'a str, lamp_engine::EngineError> {
    req.get(name)
        .and_then(Value::as_str)
        .ok_or_else(|| lamp_engine::EngineError::invalid(format!("{name} is required")))
}

fn handle(engine: &Engine, req: &Value) -> Value {
    let op = req.get("op").and_then(Value::as_str).unwrap_or("");
    let t = std::time::Instant::now();
    let r: lamp_engine::Result<Value> = match op {
        "status" => Ok(json!({
            "backend": engine.disk.name(),
            "pool": engine.disk.thin().map(|p| p.status()),
            "captures": engine.state_list().len(),
            "fault_injection": lamp_engine::fault::ENABLED,
            "producer_key_id": lamp_engine::manifest::key_id(&engine.producer_key().verifying_key()),
            "producer_public_key": lamp_init::hex_encode(engine.producer_key().verifying_key().as_bytes()),
        })),
        "list" => Ok(json!({"captures": engine.state_list()})),
        "delete" => field(req, "capture").and_then(|c| engine.state_delete(c).map(|_| json!({}))),
        "gc" => engine.state_gc(),
        "compact" => field(req, "capture").and_then(|c| engine.state_compact(c)),
        "verify" => field(req, "capture").and_then(|c| engine.state_verify(c).map(|_| json!({}))),
        "export" => field(req, "capture").and_then(|c| {
            field(req, "dir").and_then(|d| engine.state_export(c, Path::new(d)).map(|_| json!({})))
        }),
        "import" => field(req, "dir").and_then(|d| {
            engine
                .state_import(Path::new(d))
                .map(|c| json!({"capture_id": c}))
        }),
        "fault" => {
            let s = |k: &str| {
                req.get(k)
                    .and_then(Value::as_str)
                    .filter(|v| !v.is_empty())
                    .map(String::from)
            };
            lamp_engine::fault::arm(lamp_engine::fault::Spec {
                point: s("point"),
                side: s("side").unwrap_or_else(|| "before".into()),
                omit: s("omit"),
            })
            .map(|_| json!({}))
        }
        other => Err(lamp_engine::EngineError::invalid(format!(
            "unknown admin op {other:?}"
        ))),
    };
    let ms = t.elapsed().as_secs_f64() * 1000.0;
    match r {
        Ok(mut v) => {
            if let Some(o) = v.as_object_mut() {
                o.insert("ok".into(), json!(true));
                o.insert("elapsed_ms".into(), json!(ms));
            }
            v
        }
        Err(e) => json!({"ok": false, "code": e.code, "message": e.message, "elapsed_ms": ms}),
    }
}

/// Client side: `lamp-supervisor state [--admin-socket P] OP [ARG...]`.
pub fn client(socket: &Path, args: &[String]) -> Value {
    let op = args.first().map(String::as_str).unwrap_or("status");
    let arg = |i: usize| args.get(i).cloned().unwrap_or_default();
    let req = match op {
        "delete" | "compact" | "verify" => json!({"op": op, "capture": arg(1)}),
        "export" => json!({"op": op, "capture": arg(1), "dir": arg(2)}),
        "import" => json!({"op": op, "dir": arg(1)}),
        "fault" => json!({"op": op, "point": arg(1), "side": arg(2),
                          "omit": args.get(3).cloned()}),
        _ => json!({"op": op}),
    };
    let reply = (|| -> std::io::Result<Value> {
        let mut s = UnixStream::connect(socket)?;
        s.write_all(format!("{req}\n").as_bytes())?;
        let mut line = String::new();
        BufReader::new(s).read_line(&mut line)?;
        serde_json::from_str(&line).map_err(std::io::Error::other)
    })();
    reply.unwrap_or_else(
        |e| json!({"ok": false, "code": "HOST_UNAVAILABLE", "message": e.to_string()}),
    )
}
