//! lamp-supervisor
//!
//! ```text
//! lamp-supervisor serve [--socket P] [--store P] [--allow-uid UID]... [--network-policy FILE]
//!                 [--admin-socket P] [--mem-dir P] [--netns-dir P] [--uid-base N]
//!                 [--disk-backend auto|reflink|thin] [--thin-data-gib N]
//! lamp-supervisor state [--admin-socket P] status|list|gc|delete C|compact C|verify C
//!                 |export C DIR|import DIR|fault POINT SIDE [OMIT]   (root)
//! lamp-supervisor seed-import DIR            (root, service stopped; prints the seed_ref)
//! lamp-supervisor template-build FILE.lamp   (root, service stopped; builds, probes, publishes)
//! lamp-supervisor templates                  (root; read-only listing, safe next to the service)
//! lamp-supervisor grant-install --caller-uid UID [--generation N] [--policy-revision N]
//!                 [--ttl-s S] [--sandbox-prefix P] [--commands a,b,..]   (root; prints the grant)
//! lamp-supervisor grant-revoke GRANT_ID      (root)
//! lamp-supervisor ctl [--socket P] create TEMPLATE [SANDBOX_ID]
//! lamp-supervisor ctl exec SANDBOX_ID -- ARGV...
//! lamp-supervisor ctl fork SANDBOX_ID [CHILD_ID] [--durability volatile|local]
//! lamp-supervisor ctl capture SANDBOX_ID CAPTURE_ID [--durability volatile|local]
//! lamp-supervisor ctl suspend SANDBOX_ID CAPTURE_ID   (durable capture, then release)
//! lamp-supervisor ctl inspect [SANDBOX_ID] | release SANDBOX_ID
//! lamp-supervisor ctl bench-create TEMPLATE COUNT [WARMUP]
//! lamp-supervisor ctl bench-fork TEMPLATE DIRTY_MIB COUNT [WARMUP] [--durability D]
//! ```
//! `ctl` talks to a running supervisor over lamp.worker/1 exactly like lampd,
//! presenting the control grant named by `LAMP_GRANT_ID`.
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
    if cmd == "serve" {
        // Before any thread exists, so every thread inherits the mask and
        // SIGTERM/SIGINT can only be taken by the waiter's sigwait, which
        // shuts the engine down. No signal can end the process past it.
        block_term_signals();
    }
    match cmd.as_str() {
        "serve" | "seed-import" | "template-build" | "templates" | "grant-install"
        | "grant-revoke" => host(&cmd, args),
        "ctl" => ctl(args),
        #[cfg(target_os = "linux")]
        "state" => {
            let socket = take_flag(&mut args, "--admin-socket")
                .unwrap_or_else(|| format!("{DEFAULT_SOCKET}.admin"));
            let reply = lamp_supervisor::admin::client(std::path::Path::new(&socket), &args);
            println!("{reply}");
            if reply.get("ok") != Some(&json!(true)) {
                std::process::exit(1);
            }
        }
        other => die(format!("unknown command {other}")),
    }
}

#[cfg(target_os = "linux")]
fn term_set() -> libc::sigset_t {
    // SAFETY: plain sigset manipulation on a local value.
    unsafe {
        let mut set: libc::sigset_t = std::mem::zeroed();
        libc::sigemptyset(&mut set);
        libc::sigaddset(&mut set, libc::SIGTERM);
        libc::sigaddset(&mut set, libc::SIGINT);
        set
    }
}

#[cfg(target_os = "linux")]
fn block_term_signals() {
    let set = term_set();
    // SAFETY: called from the only thread; changes its mask, inherited by all later threads.
    if unsafe { libc::pthread_sigmask(libc::SIG_BLOCK, &set, std::ptr::null_mut()) } != 0 {
        die("cannot block SIGTERM/SIGINT");
    }
}

#[cfg(not(target_os = "linux"))]
fn block_term_signals() {}

#[cfg(target_os = "linux")]
fn grant_command(cmd: &str, store: &std::path::Path, mut args: Vec<String>) {
    use lamp_supervisor::grants::{COMMANDS, ControlGrant, FORMAT, GrantStore, revoke};
    let dir = store.join("grants");
    if cmd == "grant-revoke" {
        let id = args.first().unwrap_or_else(|| die("grant-revoke GRANT_ID"));
        revoke(&dir, id).unwrap_or_else(|e| die(e));
        return;
    }
    let num = |args: &mut Vec<String>, flag: &str, default: u64| {
        take_flag(args, flag)
            .map(|v| {
                v.parse::<u64>()
                    .unwrap_or_else(|_| die(format!("bad {flag}")))
            })
            .unwrap_or(default)
    };
    let caller_uid = num(&mut args, "--caller-uid", u64::MAX);
    if caller_uid > u64::from(u32::MAX) {
        die("grant-install needs --caller-uid UID");
    }
    let generation = num(&mut args, "--generation", 1);
    let policy_revision = num(&mut args, "--policy-revision", 1);
    let ttl_s = num(&mut args, "--ttl-s", 24 * 3600);
    let prefix = take_flag(&mut args, "--sandbox-prefix").unwrap_or_else(|| "sb_".into());
    let commands = take_flag(&mut args, "--commands")
        .map(|c| c.split(',').map(str::to_string).collect())
        .unwrap_or_else(|| COMMANDS.iter().map(|c| c.to_string()).collect());
    let now = lamp_engine::fsutil::now_ms();
    let grant = ControlGrant {
        format: FORMAT.into(),
        grant_id: format!(
            "gr_{}",
            lamp_init::hex_encode(&lamp_engine::fsutil::random_bytes::<16>())
        ),
        caller_uid: caller_uid as u32,
        sandbox_prefix: prefix,
        generation,
        policy_revision,
        commands,
        issued_at_unix_ms: now,
        expires_at_unix_ms: now + ttl_s * 1000,
    };
    GrantStore::new(dir)
        .install(&grant)
        .unwrap_or_else(|e| die(format!("install grant: {e}")));
    println!("{}", serde_json::to_string(&grant).unwrap());
}

#[cfg(target_os = "linux")]
fn host(cmd: &str, mut args: Vec<String>) {
    use lamp_engine::engine::{Config, Engine, Mode};
    use std::sync::Arc;
    let store =
        PathBuf::from(take_flag(&mut args, "--store").unwrap_or_else(|| DEFAULT_STORE.into()));
    if cmd.starts_with("grant-") {
        return grant_command(cmd, &store, args);
    }
    let socket =
        PathBuf::from(take_flag(&mut args, "--socket").unwrap_or_else(|| DEFAULT_SOCKET.into()));
    let mut allowed = vec![0_u32];
    while let Some(uid) = take_flag(&mut args, "--allow-uid") {
        allowed.push(uid.parse().unwrap_or_else(|_| die("bad --allow-uid")));
    }
    let admin_socket = PathBuf::from(
        take_flag(&mut args, "--admin-socket")
            .unwrap_or_else(|| format!("{}.admin", socket.display())),
    );
    let mut cfg = Config::defaults(store.clone());
    if let Some(d) = take_flag(&mut args, "--mem-dir") {
        cfg.mem_dir = d.into();
    }
    if let Some(d) = take_flag(&mut args, "--netns-dir") {
        cfg.netns_dir = d.into();
    }
    if let Some(n) = take_flag(&mut args, "--uid-base") {
        cfg.uid_base = n.parse().unwrap_or_else(|_| die("bad --uid-base"));
    }
    if let Some(b) = take_flag(&mut args, "--disk-backend") {
        cfg.disk_backend = b;
    }
    if let Some(n) = take_flag(&mut args, "--thin-data-gib") {
        cfg.thin.data_bytes = n
            .parse::<u64>()
            .unwrap_or_else(|_| die("bad --thin-data-gib"))
            << 30;
    }
    // An additional enrolled producer (hex Ed25519 public key): its
    // templates and captures are trusted like this host's own.
    while let Some(hex) = take_flag(&mut args, "--trust-producer") {
        let key = lamp_init::hex_decode(&hex)
            .and_then(|b| <[u8; 32]>::try_from(b.as_slice()).ok())
            .and_then(|b| ed25519_dalek::VerifyingKey::from_bytes(&b).ok())
            .unwrap_or_else(|| die("bad --trust-producer (hex Ed25519 public key)"));
        cfg.trusted_producers.push(key);
    }
    if let Some(fc) = take_flag(&mut args, "--firecracker") {
        cfg.bins.firecracker = fc.into();
    }
    if let Some(j) = take_flag(&mut args, "--jailer") {
        cfg.bins.jailer = j.into();
    }
    // Guard network + protected-process audit for every sandbox (T4 on T2).
    if let Some(p) = take_flag(&mut args, "--network-policy") {
        cfg.network = Some(
            lamp_engine::net::Policy::load(std::path::Path::new(&p)).unwrap_or_else(|e| die(e)),
        );
    }
    let mode = match cmd {
        "serve" => Mode::Daemon,
        "templates" => Mode::ReadOnly,
        _ => Mode::Offline,
    };
    let grants = lamp_supervisor::grants::GrantStore::new(store.join("grants"));
    let t = Instant::now();
    let engine = Arc::new(Engine::open(cfg, mode).unwrap_or_else(|e| die(e)));
    eprintln!(
        "lamp-supervisor: store open ({} disks, {} durable capture(s)), {} verified template(s) in {} ms",
        engine.disk.name(),
        engine.state_list().len(),
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
                    grants,
                },
            ));
            lamp_supervisor::admin::serve(engine.clone(), &admin_socket)
                .unwrap_or_else(|e| die(format!("admin socket: {e}")));
            let e2 = engine.clone();
            let sock2 = socket.clone();
            std::thread::spawn(move || {
                wait_for_term();
                let t = Instant::now();
                let n = e2.shutdown();
                eprintln!(
                    "lamp-supervisor: stopped {n} sandbox(es) in {} ms",
                    t.elapsed().as_millis()
                );
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
    let set = term_set();
    let mut sig = 0;
    // SAFETY: the signals are blocked in every thread (block_term_signals),
    // so they stay pending until taken here.
    unsafe { libc::sigwait(&set, &mut sig) };
    eprintln!("lamp-supervisor: signal {sig}: stopping every sandbox");
}

#[cfg(not(target_os = "linux"))]
fn host(cmd: &str, _args: Vec<String>) {
    die(format!("{cmd} requires a Linux host with KVM"))
}

struct Ctl {
    client: Client,
    op: u64,
    tag: String,
    /// When the last `exec` was acknowledged as started.
    last_ack: Option<Instant>,
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

    /// Starts a command (acknowledged once the guest exec'd it), then waits
    /// for it to exit through Inspect.
    fn exec(&mut self, sandbox: &str, argv: &[&str]) -> CommandResult {
        self.op += 1;
        let op = format!("op_{}_{}", self.tag, self.op);
        let ctx = self.client.context(&op, sandbox, 1, 30_000);
        let r = self
            .client
            .receipt(
                ctx,
                request::Command::Start(Start {
                    argv: argv.iter().map(|s| s.to_string()).collect(),
                    cwd: String::new(),
                    env: Default::default(),
                }),
            )
            .unwrap_or_else(|e| die(e));
        if r.state != "command_started" {
            die(format!("Start returned {}", r.state));
        }
        self.last_ack = Some(Instant::now());
        self.client
            .wait_command(sandbox, 1, &op, std::time::Duration::from_secs(900))
            .unwrap_or_else(|e| die(e))
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
        last_ack: None,
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
            let r = c.call(&id, request::Command::Inspect(Inspect::default()));
            println!(
                "{}",
                json!({"state": r.state, "handles": r.committed_handle_ids, "detail": r.detail})
            );
        }
        "release" => {
            let id = args.first().cloned().unwrap_or_else(|| die("release ID"));
            c.release(&id);
        }
        "capture" | "suspend" => {
            let (Some(id), Some(cid)) = (args.first().cloned(), args.get(1).cloned()) else {
                die("capture|suspend SANDBOX_ID CAPTURE_ID")
            };
            let consistency = if sub == "suspend" { "local".to_string() } else { durability };
            let t = Instant::now();
            let r = c.call(
                &id,
                request::Command::Capture(Capture {
                    capture_id: cid.clone(),
                    preserves_memory: true,
                    consistency,
                    output_handles: vec![],
                }),
            );
            let capture_ms = t.elapsed().as_secs_f64() * 1000.0;
            if sub == "suspend" {
                c.release(&id);
            }
            println!(
                "{}",
                json!({"sandbox_id": id, "capture_id": cid, "state": r.state, "capture_ms": capture_ms,
                       "released": sub == "suspend", "detail": r.detail})
            );
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
        // Timed to the command's verified output, which is stricter than
        // the start acknowledgement (also recorded).
        let elapsed = t.elapsed().as_secs_f64() * 1000.0;
        let started = c.last_ack.map(|a| (a - t).as_secs_f64() * 1000.0);
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
            json!({"trial": i, "warmup": i < warmup, "sandbox_id": id, "create_to_command_ms": elapsed,
                   "create_to_started_ack_ms": started, "ok": ok, "engine": r.detail})
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
