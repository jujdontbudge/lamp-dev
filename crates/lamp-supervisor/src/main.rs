//! lamp-supervisor
//!
//! ```text
//! lamp-supervisor serve [--socket P] [--store P] [--allow-uid UID]...
//! lamp-supervisor seed-import DIR            (root; prints the seed_ref)
//! lamp-supervisor template-build FILE.lamp   (root; builds, probes, publishes)
//! lamp-supervisor ctl [--socket P] create TEMPLATE [SANDBOX_ID]
//! lamp-supervisor ctl exec SANDBOX_ID -- ARGV...
//! lamp-supervisor ctl fork SANDBOX_ID [CHILD_ID] [--durability volatile|local]
//! lamp-supervisor ctl inspect [SANDBOX_ID] | release SANDBOX_ID
//! lamp-supervisor ctl bench-create TEMPLATE COUNT [WARMUP]
//! lamp-supervisor ctl bench-fork TEMPLATE DIRTY_MIB COUNT [WARMUP] [--durability D]
//! ```
//! `ctl` talks to a running supervisor over lamp.worker/1 exactly like lampd.
use lamp_supervisor::client::Client;
use lamp_worker_protocol::*;
use serde_json::json;
use std::path::PathBuf;
use std::time::Instant;

const DEFAULT_SOCKET: &str = "/run/lamp/worker.sock";
#[cfg(target_os = "linux")]
const DEFAULT_STORE: &str = "/var/lib/lamp";
const WORKLOAD: &str = include_str!("workload.py");

fn die(msg: impl std::fmt::Display) -> ! {
    eprintln!("lamp-supervisor: {msg}");
    std::process::exit(1)
}

fn take_flag(args: &mut Vec<String>, name: &str) -> Option<String> {
    let i = args.iter().position(|a| a == name)?;
    if i + 1 >= args.len() {
        die(format!("{name} needs a value"));
    }
    let v = args.remove(i + 1);
    args.remove(i);
    Some(v)
}

fn main() {
    let mut args: Vec<String> = std::env::args().skip(1).collect();
    if args.is_empty() {
        die("usage: lamp-supervisor serve|seed-import|template-build|ctl ...");
    }
    let cmd = args.remove(0);
    match cmd.as_str() {
        "serve" | "seed-import" | "template-build" | "templates" => host(&cmd, args),
        "ctl" => ctl(args),
        other => die(format!("unknown command {other}")),
    }
}

#[cfg(target_os = "linux")]
fn host(cmd: &str, mut args: Vec<String>) {
    use lamp_engine::engine::{Config, Engine};
    use std::sync::Arc;
    let store =
        PathBuf::from(take_flag(&mut args, "--store").unwrap_or_else(|| DEFAULT_STORE.into()));
    let socket =
        PathBuf::from(take_flag(&mut args, "--socket").unwrap_or_else(|| DEFAULT_SOCKET.into()));
    let mut allowed = vec![0_u32];
    while let Some(uid) = take_flag(&mut args, "--allow-uid") {
        allowed.push(uid.parse().unwrap_or_else(|_| die("bad --allow-uid")));
    }
    let mut cfg = Config::defaults(store);
    if let Some(fc) = take_flag(&mut args, "--firecracker") {
        cfg.bins.firecracker = fc.into();
    }
    if let Some(j) = take_flag(&mut args, "--jailer") {
        cfg.bins.jailer = j.into();
    }
    let t = Instant::now();
    let engine = Arc::new(Engine::open(cfg).unwrap_or_else(|e| die(e)));
    eprintln!(
        "lamp-supervisor: store open, {} verified template(s) in {} ms",
        engine.templates().len(),
        t.elapsed().as_millis()
    );
    match cmd {
        "serve" => {
            let server = Arc::new(lamp_supervisor::server::Server::new(
                engine.clone(),
                lamp_supervisor::server::ServerConfig {
                    allowed_uids: allowed,
                    worker_id: format!("lamp-supervisor@{}", engine.cfg.host_id),
                },
            ));
            let e2 = engine.clone();
            let sock2 = socket.clone();
            std::thread::spawn(move || {
                wait_for_term();
                e2.shutdown();
                let _ = std::fs::remove_file(&sock2);
                std::process::exit(0);
            });
            server.serve(&socket).unwrap_or_else(|e| die(e));
        }
        "seed-import" => {
            let dir = args.first().unwrap_or_else(|| die("seed-import DIR"));
            println!(
                "{}",
                lamp_engine::build::import_seed(&engine, std::path::Path::new(dir))
                    .unwrap_or_else(|e| die(e))
            );
        }
        "template-build" => {
            let file = args.first().unwrap_or_else(|| die("template-build FILE"));
            let text = std::fs::read_to_string(file).unwrap_or_else(|e| die(e));
            match lamp_engine::build::build_template(&engine, &text) {
                Ok(r) => {
                    eprint!("{}", r.log);
                    println!(
                        "{}",
                        json!({"template_id": r.template_id, "manifest_digest": r.manifest_digest})
                    );
                }
                Err(e) => die(e),
            }
            engine.shutdown();
        }
        _ => {
            for t in engine.templates() {
                println!("{} {} {}", t.id, t.manifest.name, t.manifest_digest);
            }
        }
    }
}

#[cfg(target_os = "linux")]
fn wait_for_term() {
    // SAFETY: block SIGTERM/SIGINT in this thread and wait for one.
    unsafe {
        let mut set: libc::sigset_t = std::mem::zeroed();
        libc::sigemptyset(&mut set);
        libc::sigaddset(&mut set, libc::SIGTERM);
        libc::sigaddset(&mut set, libc::SIGINT);
        libc::pthread_sigmask(libc::SIG_BLOCK, &set, std::ptr::null_mut());
        let mut sig = 0;
        libc::sigwait(&set, &mut sig);
    }
}

#[cfg(not(target_os = "linux"))]
fn host(cmd: &str, _args: Vec<String>) {
    die(format!("{cmd} requires a Linux host with KVM"))
}

struct Ctl {
    client: Client,
    op: u64,
    tag: String,
}

fn rand_tag() -> String {
    let t = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap();
    format!(
        "{:x}{:x}",
        t.as_secs() & 0xffff,
        t.subsec_nanos() ^ std::process::id()
    )
}

impl Ctl {
    fn ctx(&mut self, sandbox: &str, deadline_ms: u64) -> Context {
        self.op += 1;
        let op = format!("op_{}_{}", self.tag, self.op);
        self.client.context(
            &op,
            sandbox,
            if sandbox.is_empty() { 0 } else { 1 },
            deadline_ms,
        )
    }

    fn call(&mut self, sandbox: &str, command: request::Command) -> Receipt {
        let ctx = self.ctx(sandbox, 120_000);
        self.client.receipt(ctx, command).unwrap_or_else(|e| die(e))
    }

    fn create(&mut self, template: &str, sandbox: &str) -> Receipt {
        self.call(
            sandbox,
            request::Command::Allocate(Allocate {
                resources: None,
                engine_id: String::new(),
                handles: vec![],
            }),
        );
        self.call(
            sandbox,
            request::Command::Restore(Restore {
                capture_id: template.into(),
                compatibility: None,
                input_handles: vec![],
            }),
        )
    }

    fn exec(&mut self, sandbox: &str, argv: &[&str]) -> CommandResult {
        let r = self.call(
            sandbox,
            request::Command::Start(Start {
                argv: argv.iter().map(|s| s.to_string()).collect(),
                cwd: String::new(),
                env: Default::default(),
            }),
        );
        r.command
            .unwrap_or_else(|| die("Start returned no command result"))
    }

    fn fork(&mut self, source: &str, child: &str, durability: &str) -> (Receipt, Receipt) {
        let capture_id = format!("cp_{}", &child[3..]);
        let cap = self.call(
            source,
            request::Command::Capture(Capture {
                capture_id: capture_id.clone(),
                preserves_memory: true,
                consistency: durability.into(),
                output_handles: vec![],
            }),
        );
        let restored = self.create(&capture_id, child);
        (cap, restored)
    }

    fn release(&mut self, sandbox: &str) {
        self.call(sandbox, request::Command::Release(Release {}));
    }
}

fn text(b: &[u8]) -> String {
    String::from_utf8_lossy(b).into_owned()
}

fn percentile(sorted: &[f64], p: f64) -> f64 {
    if sorted.is_empty() {
        return f64::NAN;
    }
    let rank = ((p / 100.0) * sorted.len() as f64).ceil() as usize;
    sorted[rank.clamp(1, sorted.len()) - 1]
}

fn summary(name: &str, mut ms: Vec<f64>, extra: serde_json::Value) -> serde_json::Value {
    ms.sort_by(f64::total_cmp);
    json!({
        "case": name,
        "trials": ms.len(),
        "p50_ms": percentile(&ms, 50.0),
        "p95_ms": percentile(&ms, 95.0),
        "p99_ms": percentile(&ms, 99.0),
        "max_ms": ms.last().copied().unwrap_or(f64::NAN),
        "min_ms": ms.first().copied().unwrap_or(f64::NAN),
        "extra": extra,
    })
}

const ASK: &str = "import socket,sys\ns=socket.socket(socket.AF_UNIX)\ns.connect('/run/lamp-workload.sock')\ns.send(sys.argv[1].encode())\nprint(s.recv(4096).decode())";

fn ctl(mut args: Vec<String>) {
    let socket =
        PathBuf::from(take_flag(&mut args, "--socket").unwrap_or_else(|| DEFAULT_SOCKET.into()));
    let durability = take_flag(&mut args, "--durability").unwrap_or_else(|| "volatile".into());
    if args.is_empty() {
        die("ctl needs a subcommand");
    }
    let sub = args.remove(0);
    let client = Client::connect(&socket, "lamp-ctl")
        .unwrap_or_else(|e| die(format!("connect {}: {e}", socket.display())));
    let mut c = Ctl {
        client,
        op: 0,
        tag: rand_tag(),
    };
    match sub.as_str() {
        "create" => {
            let template = args.first().unwrap_or_else(|| die("create TEMPLATE [ID]"));
            let id = args
                .get(1)
                .cloned()
                .unwrap_or_else(|| format!("sb_{}", rand_tag()));
            let t = Instant::now();
            let r = c.create(template, &id);
            let cmd = c.exec(&id, &["true"]);
            println!(
                "{}",
                json!({"sandbox_id": id, "state": r.state, "create_to_command_ms": t.elapsed().as_secs_f64() * 1000.0,
                       "probe_exit": cmd.exit_code, "detail": r.detail})
            );
        }
        "exec" => {
            let id = args
                .first()
                .cloned()
                .unwrap_or_else(|| die("exec ID -- ARGV"));
            let argv: Vec<&str> = args
                .iter()
                .skip_while(|a| *a != "--")
                .skip(1)
                .map(String::as_str)
                .collect();
            let r = c.exec(&id, &argv);
            print!("{}", text(&r.stdout_tail));
            eprint!("{}", text(&r.stderr_tail));
            std::process::exit(r.exit_code);
        }
        "fork" => {
            let source = args
                .first()
                .cloned()
                .unwrap_or_else(|| die("fork ID [CHILD]"));
            let child = args
                .get(1)
                .cloned()
                .unwrap_or_else(|| format!("sb_{}", rand_tag()));
            let t = Instant::now();
            let (cap, restored) = c.fork(&source, &child, &durability);
            let a = c.exec(&child, &["true"]);
            let b = c.exec(&source, &["true"]);
            println!(
                "{}",
                json!({"source": source, "fork": child, "fork_ms": t.elapsed().as_secs_f64() * 1000.0,
                       "probe_exits": [b.exit_code, a.exit_code], "capture": cap.detail, "restore": restored.detail})
            );
        }
        "inspect" => {
            let id = args.first().cloned().unwrap_or_default();
            let r = c.call(&id, request::Command::Inspect(Inspect {}));
            println!(
                "{}",
                json!({"state": r.state, "handles": r.committed_handle_ids, "detail": r.detail})
            );
        }
        "release" => {
            let id = args.first().cloned().unwrap_or_else(|| die("release ID"));
            c.release(&id);
        }
        "bench-create" => bench_create(&mut c, &args),
        "bench-fork" => bench_fork(&mut c, &args, &durability),
        other => die(format!("unknown ctl subcommand {other}")),
    }
}

fn bench_create(c: &mut Ctl, args: &[String]) {
    let template = args
        .first()
        .unwrap_or_else(|| die("bench-create TEMPLATE COUNT [WARMUP]"));
    let count: usize = args.get(1).and_then(|s| s.parse().ok()).unwrap_or(200);
    let warmup: usize = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(20);
    let mut ms = Vec::new();
    let mut failures = 0;
    for i in 0..warmup + count {
        let id = format!("sb_bc_{}_{i}", c.tag);
        let t = Instant::now();
        let r = c.create(template, &id);
        // A real command whose output is an independent marker of execution.
        let cmd = c.exec(&id, &["sh", "-c", "echo command-ran-in-$(hostname)"]);
        let elapsed = t.elapsed().as_secs_f64() * 1000.0;
        let ok = cmd.exit_code == 0
            && text(&cmd.stdout_tail).trim() == format!("command-ran-in-{}", id.replace('_', "-"));
        if !ok {
            failures += 1;
        }
        if i >= warmup {
            ms.push(elapsed);
        }
        println!(
            "{}",
            json!({"trial": i, "warmup": i < warmup, "sandbox_id": id, "create_to_command_ms": elapsed, "ok": ok, "engine": r.detail})
        );
        c.release(&id);
    }
    eprintln!(
        "{}",
        summary(
            "create",
            ms,
            json!({"failures": failures, "template": template})
        )
    );
}

fn bench_fork(c: &mut Ctl, args: &[String], durability: &str) {
    let template = args
        .first()
        .unwrap_or_else(|| die("bench-fork TEMPLATE DIRTY_MIB COUNT [WARMUP]"));
    let dirty: u64 = args.get(1).and_then(|s| s.parse().ok()).unwrap_or(1024);
    let count: usize = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(100);
    let warmup: usize = args.get(3).and_then(|s| s.parse().ok()).unwrap_or(5);
    let source = format!("sb_bf_{}", c.tag);
    c.create(template, &source);
    let mut workload = c.exec(
        &source,
        &[
            "sh",
            "-c",
            &format!("cat > /root/workload.py <<'EOF'\n{WORKLOAD}\nEOF\n"),
        ],
    );
    if workload.exit_code != 0 {
        die("could not install workload");
    }
    if dirty > 0 {
        workload = c.exec(
            &source,
            &["sh", "-c", &format!("setsid python3 /root/workload.py {dirty} >/root/workload.log 2>&1 & for i in $(seq 600); do [ -f /run/lamp-workload.ready ] && exit 0; sleep 0.05; done; exit 1")],
        );
        if workload.exit_code != 0 {
            die("workload did not start");
        }
    }
    let ask = |c: &mut Ctl, id: &str, what: &str| {
        text(&c.exec(id, &["python3", "-c", ASK, what]).stdout_tail)
            .trim()
            .to_string()
    };
    let mut ms = Vec::new();
    let mut pauses = Vec::new();
    let mut failures = 0;
    for i in 0..warmup + count {
        if dirty > 0 {
            // Rewrite the whole working set so every trial forks with DIRTY_MIB
            // of recently written RAM.
            ask(c, &source, "rewrite");
        }
        let child = format!("sb_fk_{}_{i}", c.tag);
        let t = Instant::now();
        let (cap, restored) = c.fork(&source, &child, durability);
        let a = c.exec(&child, &["sh", "-c", "echo fork-ok-$(hostname)"]);
        let b = c.exec(&source, &["sh", "-c", "echo source-ok-$(hostname)"]);
        let elapsed = t.elapsed().as_secs_f64() * 1000.0;
        // Untimed independent checks: RAM markers, identity, entropy, disk.
        let mut checks = serde_json::Map::new();
        let mut ok = a.exit_code == 0 && b.exit_code == 0;
        ok &= text(&a.stdout_tail).trim() == format!("fork-ok-{}", child.replace('_', "-"));
        ok &= text(&b.stdout_tail).trim() == format!("source-ok-{}", source.replace('_', "-"));
        if dirty > 0 && (i < warmup || i % 10 == 0 || i + 1 == warmup + count) {
            let (cs, cf) = (ask(c, &source, "check"), ask(c, &child, "check"));
            let gen_of = |s: &str| s.split_whitespace().next().unwrap_or("").to_string();
            let digest_of = |s: &str| s.split_whitespace().last().unwrap_or("").to_string();
            ok &= cs.contains("bad_pages=0") && cf.contains("bad_pages=0");
            ok &= gen_of(&cs) == gen_of(&cf) && digest_of(&cs) == digest_of(&cf);
            checks.insert("source_ram".into(), json!(cs));
            checks.insert("fork_ram".into(), json!(cf));
        }
        let mark = format!("written-by-fork-{i}");
        c.exec(
            &child,
            &[
                "sh",
                "-c",
                &format!("echo {mark} > /root/independent && sync"),
            ],
        );
        let leaked = c
            .exec(
                &source,
                &["sh", "-c", &format!("grep -qs {mark} /root/independent")],
            )
            .exit_code
            == 0;
        let ids = (
            text(&c.exec(&source, &["cat", "/etc/machine-id"]).stdout_tail),
            text(&c.exec(&child, &["cat", "/etc/machine-id"]).stdout_tail),
        );
        // Without a reseed both kernels would emit the same CRNG stream.
        let rnd = |c: &mut Ctl, id: &str| {
            text(
                &c.exec(
                    id,
                    &[
                        "sh",
                        "-c",
                        "head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \\n'",
                    ],
                )
                .stdout_tail,
            )
        };
        let (r1, r2) = (rnd(c, &source), rnd(c, &child));
        let host_s = std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs_f64();
        let guest_s: f64 = text(&c.exec(&child, &["date", "+%s.%N"]).stdout_tail)
            .trim()
            .parse()
            .unwrap_or(0.0);
        let skew = (guest_s - host_s).abs();
        ok &= !leaked && ids.0 != ids.1 && r1.len() == 64 && r1 != r2 && skew < 2.0;
        checks.insert("entropy_diverged".into(), json!(r1 != r2));
        checks.insert("fork_clock_skew_s".into(), json!(skew));
        checks.insert("disk_write_isolated".into(), json!(!leaked));
        checks.insert("machine_ids_differ".into(), json!(ids.0 != ids.1));
        if !ok {
            failures += 1;
        }
        let pause: f64 = cap
            .detail
            .get("source_pause_us")
            .and_then(|v| v.parse().ok())
            .unwrap_or(f64::NAN)
            / 1000.0;
        if i >= warmup {
            ms.push(elapsed);
            pauses.push(pause);
        }
        println!(
            "{}",
            json!({"trial": i, "warmup": i < warmup, "fork_ms": elapsed, "source_pause_ms": pause, "ok": ok,
                   "capture": cap.detail, "restore": restored.detail, "checks": checks})
        );
        c.release(&child);
    }
    pauses.sort_by(f64::total_cmp);
    let pause_max = pauses.last().copied().unwrap_or(f64::NAN);
    let under = ms.iter().filter(|m| **m < 1000.0).count();
    eprintln!(
        "{}",
        summary(
            &format!("fork dirty={dirty}MiB durability={durability}"),
            ms,
            json!({"failures": failures, "trials_under_1000ms": under, "source_pause_p50_ms": percentile(&pauses, 50.0), "source_pause_max_ms": pause_max}),
        )
    );
    c.release(&source);
}
