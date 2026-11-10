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
//! Isolated hosts: --mem-dir P --netns-dir P --uid-base N --host-id ID
//! lamp-supervisor ctl inspect [SANDBOX_ID] | release SANDBOX_ID
//! lamp-supervisor ctl suspend SANDBOX_ID | resume SANDBOX_ID GENERATION
//! lamp-supervisor ctl bench-create TEMPLATE COUNT [WARMUP]
//! lamp-supervisor ctl bench-fork TEMPLATE DIRTY_MIB COUNT [WARMUP] [--durability D]
//! lamp-supervisor ctl e05 TEMPLATE DIRTY_MIB COUNT [WARMUP] [--durability D]
//! lamp-supervisor ctl e06 TEMPLATE TRIALS [VCPUS MEM DISK] [--two-running] --store P   (root: one grant per generation)
//! lamp-supervisor template-prepare SEED NAME [--vcpus N --memory-mib M --disk-gib G]   (Mac)
//! lamp-supervisor template-import-macos VMDIR NAME [--min-vcpus ..] [--vcpus ..]       (Mac)
//! ```
//! `ctl` talks to a running supervisor over lamp.worker/1 exactly like lampd,
//! presenting the control grant named by `LAMP_GRANT_ID`.
use lamp_supervisor::client::Client;
use lamp_worker_protocol::*;
use serde_json::json;
use std::path::PathBuf;
use std::time::Instant;

#[cfg(not(target_os = "macos"))]
const DEFAULT_SOCKET: &str = "/run/lamp/worker.sock";
#[cfg(target_os = "macos")]
const DEFAULT_SOCKET: &str = "/private/var/run/lamp/worker.sock";
#[cfg(target_os = "linux")]
const DEFAULT_STORE: &str = "/var/lib/lamp";
#[cfg(target_os = "macos")]
const DEFAULT_STORE: &str = "/private/var/lamp/store";
const WORKLOAD: &str = include_str!("workload.py");
const E05_WORKLOAD: &str = include_str!("e05_workload.py");

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

#[cfg(target_os = "linux")]
fn take_bool(args: &mut Vec<String>, name: &str) -> bool {
    match args.iter().position(|a| a == name) {
        Some(i) => {
            args.remove(i);
            true
        }
        None => false,
    }
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
        "serve"
        | "seed-import"
        | "template-build"
        | "templates"
        | "grant-install"
        | "grant-revoke"
        | "template-prepare"
        | "template-import-macos" => host(&cmd, args),
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

#[cfg(any(target_os = "linux", target_os = "macos"))]
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

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn block_term_signals() {
    let set = term_set();
    // SAFETY: called from the only thread; changes its mask, inherited by all later threads.
    if unsafe { libc::pthread_sigmask(libc::SIG_BLOCK, &set, std::ptr::null_mut()) } != 0 {
        die("cannot block SIGTERM/SIGINT");
    }
}

#[cfg(not(any(target_os = "linux", target_os = "macos")))]
fn block_term_signals() {}

#[cfg(any(target_os = "linux", target_os = "macos"))]
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
    // Independent owner demonstrations must not share runtime directories or
    // jail identities with a supervisor serving another ticket/store.
    if let Some(d) = take_flag(&mut args, "--mem-dir") {
        cfg.mem_dir = d.into();
    }
    if let Some(d) = take_flag(&mut args, "--netns-dir") {
        cfg.netns_dir = d.into();
    }
    if let Some(n) = take_flag(&mut args, "--uid-base") {
        cfg.uid_base = n.parse().unwrap_or_else(|_| die("bad --uid-base"));
        if cfg.uid_base < 65_536 || cfg.uid_base.checked_add(cfg.uid_count).is_none() {
            die("--uid-base must reserve a non-system, non-overflowing range");
        }
    }
    if let Some(id) = take_flag(&mut args, "--host-id") {
        cfg.host_id = id;
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
    if let Some(size) = take_flag(&mut args, "--mem-dir-size") {
        cfg.mem_dir_size = size;
    }
    if take_bool(&mut args, "--no-capture-spare") {
        cfg.capture_spare = false;
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

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn wait_for_term() {
    let set = term_set();
    let mut sig = 0;
    // SAFETY: the signals are blocked in every thread (block_term_signals),
    // so they stay pending until taken here.
    unsafe { libc::sigwait(&set, &mut sig) };
    eprintln!("lamp-supervisor: signal {sig}: stopping every sandbox");
}

#[cfg(target_os = "macos")]
fn host(cmd: &str, mut args: Vec<String>) {
    use lamp_engine::engine::{Config, Engine, Mode};
    use lamp_engine::manifest::Resources;
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
    let mut cfg = Config::defaults(store.clone());
    if let Some(w) = take_flag(&mut args, "--worker") {
        cfg.worker = w.into();
    }
    if let Some(r) = take_flag(&mut args, "--ram-dir") {
        cfg.ram_dir = if r.is_empty() { None } else { Some(r.into()) };
    }
    if let Some(r) = take_flag(&mut args, "--identities") {
        // FIRST-LAST of the _lampvmNN pool, so two stores never share one.
        let (a, b) = r
            .split_once('-')
            .and_then(|(a, b)| Some((a.parse::<usize>().ok()?, b.parse::<usize>().ok()?)))
            .unwrap_or_else(|| die("--identities FIRST-LAST"));
        cfg.identities = (a..=b).map(|i| format!("_lampvm{i:02}")).collect();
    }
    if let Some(i) = args.iter().position(|a| a == "--no-confine") {
        // Diagnostics only: evidence runs always confine.
        args.remove(i);
        cfg.confine = false;
    }
    let res = |args: &mut Vec<String>, prefix: &str, d: Resources| Resources {
        vcpus: take_flag(args, &format!("--{prefix}vcpus"))
            .map(|v| v.parse().unwrap_or_else(|_| die("bad vcpus")))
            .unwrap_or(d.vcpus),
        memory_mib: take_flag(args, &format!("--{prefix}memory-mib"))
            .map(|v| v.parse().unwrap_or_else(|_| die("bad memory")))
            .unwrap_or(d.memory_mib),
        disk_gib: take_flag(args, &format!("--{prefix}disk-gib"))
            .map(|v| v.parse().unwrap_or_else(|_| die("bad disk")))
            .unwrap_or(d.disk_gib),
    };
    let mode = match cmd {
        "serve" => Mode::Daemon,
        "templates" => Mode::ReadOnly,
        _ => Mode::Offline,
    };
    let grants = lamp_supervisor::grants::GrantStore::new(store.join("grants"));
    let t = Instant::now();
    let engine = Arc::new(Engine::open(cfg, mode).unwrap_or_else(|e| die(e)));
    eprintln!(
        "lamp-supervisor: store open, {} verified template(s) in {} ms",
        engine.templates().len(),
        t.elapsed().as_millis()
    );
    let built = |r: lamp_engine::Result<lamp_engine::engine::build::Built>| match r {
        Ok(r) => {
            eprint!("{}", r.log);
            println!(
                "{}",
                json!({"template_id": r.template_id, "manifest_digest": r.manifest_digest})
            );
        }
        Err(e) => die(e),
    };
    match cmd {
        "serve" => {
            if let Some(dir) = socket.parent() {
                let _ = std::fs::create_dir_all(dir);
            }
            let server = Arc::new(lamp_supervisor::server::Server::new(
                engine.clone(),
                lamp_supervisor::server::ServerConfig {
                    allowed_uids: allowed,
                    worker_id: format!("lamp-supervisor@{}", engine.cfg.host_id),
                    grants,
                },
            ));
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
        "template-prepare" => {
            let seed = args
                .first()
                .cloned()
                .unwrap_or_else(|| die("template-prepare SEED NAME"));
            let name = args
                .get(1)
                .cloned()
                .unwrap_or_else(|| die("template-prepare SEED NAME"));
            let r = res(&mut args, "", lamp_engine::engine::STANDARD);
            built(engine.build_linux_template(std::path::Path::new(&seed), &name, r));
            engine.shutdown();
        }
        "template-import-macos" => {
            let min = res(
                &mut args,
                "min-",
                Resources {
                    vcpus: 2,
                    memory_mib: 4096,
                    disk_gib: 48,
                },
            );
            let def = res(&mut args, "", min.clone());
            let vm = args
                .first()
                .cloned()
                .unwrap_or_else(|| die("template-import-macos VMDIR NAME"));
            let name = args
                .get(1)
                .cloned()
                .unwrap_or_else(|| die("template-import-macos VMDIR NAME"));
            built(engine.import_macos_template(std::path::Path::new(&vm), &name, min, def));
            engine.shutdown();
        }
        _ => {
            for t in engine.templates() {
                println!(
                    "{} {} {} {}",
                    t.id,
                    t.manifest.name,
                    t.guest_os(),
                    t.manifest_digest
                );
            }
        }
    }
}

#[cfg(not(any(target_os = "linux", target_os = "macos")))]
fn host(cmd: &str, _args: Vec<String>) {
    die(format!("{cmd} requires a Linux host with KVM or a Mac"))
}

struct Ctl {
    client: Client,
    op: u64,
    tag: String,
    /// When the last `exec` was acknowledged as started.
    last_ack: Option<Instant>,
    /// Sandbox generations other than 1 (after resumes), and the control
    /// grant installed for each generation (grants pin one generation).
    gens: std::collections::HashMap<String, u64>,
    grants: std::collections::HashMap<u64, String>,
    /// Store whose grant directory `grant()` installs into (root only).
    store: Option<PathBuf>,
    deadline_ms: u64,
    /// Generation used for sandboxes not in `gens` (`--generation`).
    default_gen: u64,
    /// `--memory-mib`: allocate sandboxes of this guest size (a template
    /// built smaller than the standard), otherwise the standard.
    memory_mib: Option<u64>,
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
    fn generation(&mut self, sandbox: &str) -> u64 {
        if sandbox.is_empty() {
            return 0;
        }
        let g = self.gens.get(sandbox).copied().unwrap_or(self.default_gen);
        if let Some(grant) = self.grants.get(&g) {
            self.client.grant_id = grant.clone();
        }
        g
    }

    /// Installs (once) a control grant for generation `g` through
    /// `lamp-supervisor grant-install`, as an installer would.
    fn grant(&mut self, g: u64) {
        if self.grants.contains_key(&g) {
            return;
        }
        let store = self
            .store
            .clone()
            .unwrap_or_else(|| die("needs --store to install grants"));
        // SAFETY: getuid has no failure mode.
        let uid = unsafe { libc::getuid() };
        let out = std::process::Command::new(std::env::current_exe().unwrap())
            .args(["grant-install", "--store"])
            .arg(&store)
            .args([
                "--caller-uid",
                &uid.to_string(),
                "--generation",
                &g.to_string(),
                "--ttl-s",
                "86400",
            ])
            .output()
            .unwrap_or_else(|e| die(e));
        let grant: serde_json::Value = serde_json::from_slice(&out.stdout)
            .unwrap_or_else(|_| die(format!("grant-install: {}", text(&out.stderr))));
        self.grants
            .insert(g, grant["grant_id"].as_str().unwrap().to_string());
    }

    fn ctx(&mut self, sandbox: &str, deadline_ms: u64) -> Context {
        self.op += 1;
        let op = format!("op_{}_{}", self.tag, self.op);
        let g = self.generation(sandbox);
        self.client.context(&op, sandbox, g, deadline_ms)
    }

    fn try_call(&mut self, sandbox: &str, command: request::Command) -> Result<Receipt, String> {
        let ctx = self.ctx(sandbox, self.deadline_ms);
        self.client.receipt(ctx, command).map_err(|e| e.to_string())
    }

    fn call(&mut self, sandbox: &str, command: request::Command) -> Receipt {
        self.try_call(sandbox, command).unwrap_or_else(|e| die(e))
    }

    fn create(&mut self, template: &str, sandbox: &str) -> Receipt {
        self.try_create(template, sandbox, None)
            .unwrap_or_else(|e| die(e))
    }

    fn try_create(
        &mut self,
        template: &str,
        sandbox: &str,
        resources: Option<Resources>,
    ) -> Result<Receipt, String> {
        self.try_call(
            sandbox,
            request::Command::Allocate(Allocate {
                resources: resources.or(self.memory_mib.map(|m| Resources {
                    vcpus: 2,
                    memory_mib: m,
                    disk_gib: 20,
                    pids_max: 4096,
                })),
                engine_id: String::new(),
                handles: vec![],
            }),
        )?;
        self.try_restore(sandbox, template)
    }

    fn try_restore(&mut self, sandbox: &str, capture_id: &str) -> Result<Receipt, String> {
        self.try_call(
            sandbox,
            request::Command::Restore(Restore {
                capture_id: capture_id.into(),
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
        let g = self.generation(sandbox);
        let ctx = self.client.context(&op, sandbox, g, 30_000);
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
            .wait_command(sandbox, g, &op, std::time::Duration::from_secs(900))
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

    fn try_fork(
        &mut self,
        source: &str,
        child: &str,
        durability: &str,
    ) -> Result<(Receipt, Receipt), String> {
        let capture_id = format!("cp_{}", &child[3..]);
        let cap = self.try_call(
            source,
            request::Command::Capture(Capture {
                capture_id: capture_id.clone(),
                preserves_memory: true,
                consistency: durability.into(),
                output_handles: vec![],
            }),
        )?;
        let restored = self.try_create(&capture_id, child, None)?;
        Ok((cap, restored))
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
    let store = take_flag(&mut args, "--store").map(PathBuf::from);
    let default_gen = take_flag(&mut args, "--generation")
        .map(|g| g.parse().unwrap_or_else(|_| die("bad --generation")))
        .unwrap_or(1);
    let memory_mib = take_flag(&mut args, "--memory-mib")
        .map(|m| m.parse().unwrap_or_else(|_| die("bad --memory-mib")));
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
        gens: Default::default(),
        grants: Default::default(),
        store,
        deadline_ms: 120_000,
        default_gen,
        memory_mib,
    };
    match sub.as_str() {
        "create" => {
            let template = args
                .first()
                .unwrap_or_else(|| die("create TEMPLATE [ID [VCPUS MEMORY_MIB DISK_GIB]]"));
            let id = args
                .get(1)
                .cloned()
                .unwrap_or_else(|| format!("sb_{}", rand_tag()));
            let resources = (args.len() >= 5).then(|| Resources {
                vcpus: args[2].parse().unwrap_or_else(|_| die("VCPUS")),
                memory_mib: args[3].parse().unwrap_or_else(|_| die("MEMORY_MIB")),
                disk_gib: args[4].parse().unwrap_or_else(|_| die("DISK_GIB")),
                ..Default::default()
            });
            if resources.is_some() {
                c.deadline_ms = c.deadline_ms.max(900_000);
            }
            let t = Instant::now();
            let r = c
                .try_create(template, &id, resources)
                .unwrap_or_else(|e| die(e));
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
        "suspend" if args.len() == 1 => {
            // suspend ID: durable suspend that keeps the sandbox (resume later).
            let id = args[0].clone();
            let r = capture(&mut c, &id, "", "suspend", true).unwrap_or_else(|e| die(e));
            println!(
                "{}",
                json!({"state": r.state, "handles": r.committed_handle_ids, "detail": r.detail})
            );
        }
        "resume" => {
            // resume ID GENERATION (LAMP_GRANT_ID must cover GENERATION).
            let id = args
                .first()
                .cloned()
                .unwrap_or_else(|| die("resume ID GENERATION"));
            let g: u64 = args
                .get(1)
                .and_then(|g| g.parse().ok())
                .unwrap_or_else(|| die("resume ID GENERATION"));
            c.gens.insert(id.clone(), g);
            let r = c
                .try_restore(&id, &format!("suspend_{id}"))
                .unwrap_or_else(|e| die(e));
            println!("{}", json!({"state": r.state, "detail": r.detail}));
        }
        "capture" | "suspend" => {
            let (Some(id), Some(cid)) = (args.first().cloned(), args.get(1).cloned()) else {
                die("capture|suspend SANDBOX_ID CAPTURE_ID")
            };
            let consistency = if sub == "suspend" {
                "local".to_string()
            } else {
                durability
            };
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
        "e05" => e05(&mut c, &args, &durability),
        "e06" => e06(&mut c, &args, &socket),
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

/// Resident memory (KiB) of every process running as `user`: on a Mac that is
/// the confined worker plus its Virtualization XPC service, both of which
/// run under the sandbox's own hidden account.
fn rss_kib(user: &str) -> u64 {
    if user.is_empty() {
        return 0;
    }
    std::process::Command::new("/bin/ps")
        .args(["-U", user, "-o", "rss="])
        .output()
        .map(|o| {
            text(&o.stdout)
                .split_whitespace()
                .filter_map(|v| v.parse::<u64>().ok())
                .sum()
        })
        .unwrap_or(0)
}

fn field<'a>(s: &'a str, key: &str) -> &'a str {
    s.split_whitespace()
        .find_map(|kv| kv.strip_prefix(key).and_then(|v| v.strip_prefix('=')))
        .unwrap_or("")
}

fn num(s: &str, key: &str) -> i64 {
    field(s, key).parse().unwrap_or(-1)
}

/// E05: memory forks of a Linux guest with full-dirty RAM and continuous
/// fsync'd I/O. Every trial checks, outside the timer, that both instances
/// continue (live counter advances, I/O writer advances), every RAM page and
/// every acknowledged disk record survived on both sides, the clocks are
/// right and monotonic, and identities, entropy and disks are independent.
fn e05(c: &mut Ctl, args: &[String], durability: &str) {
    let template = args
        .first()
        .unwrap_or_else(|| die("e05 TEMPLATE DIRTY_MIB COUNT [WARMUP]"));
    let dirty: u64 = args.get(1).and_then(|s| s.parse().ok()).unwrap_or(768);
    let count: usize = args.get(2).and_then(|s| s.parse().ok()).unwrap_or(100);
    let warmup: usize = args.get(3).and_then(|s| s.parse().ok()).unwrap_or(5);
    let source = format!("sb_e5_{}", c.tag);
    let created = c.create(template, &source);
    let source_user = created
        .detail
        .get("worker_identity")
        .cloned()
        .unwrap_or_default();
    let install = c.exec(
        &source,
        &[
            "sh",
            "-c",
            &format!("cat > /root/e05.py <<'EOF'\n{E05_WORKLOAD}\nEOF\n"),
        ],
    );
    if install.exit_code != 0 {
        die("could not install the E05 workload");
    }
    let started = c.exec(
        &source,
        &["sh", "-c", &format!("setsid python3 /root/e05.py {dirty} >/root/e05.log 2>&1 & for i in $(seq 1200); do [ -f /run/lamp-workload.ready ] && exit 0; sleep 0.05; done; cat /root/e05.log; exit 1")],
    );
    if started.exit_code != 0 {
        die(format!(
            "E05 workload did not start: {}",
            text(&started.stdout_tail)
        ));
    }
    let ask = |c: &mut Ctl, id: &str, what: &str| {
        text(&c.exec(id, &["python3", "-c", ASK, what]).stdout_tail)
            .trim()
            .to_string()
    };
    let wall = |c: &mut Ctl, id: &str| -> f64 {
        text(&c.exec(id, &["date", "+%s.%N"]).stdout_tail)
            .trim()
            .parse()
            .unwrap_or(0.0)
    };
    let now_s = || {
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .unwrap()
            .as_secs_f64()
    };
    let (mut ms, mut pauses, mut rss) = (Vec::new(), Vec::new(), Vec::new());
    let (mut failures, mut state_err, mut clock_err, mut iso_err) = (0, 0, 0, 0);
    let (mut lock_waits, mut lock_wait_s, mut lock_refusals) = (0usize, 0.0f64, 0usize);
    for i in 0..warmup + count {
        // Rewrite the whole working set: every trial forks full-dirty RAM.
        let gen_now = ask(c, &source, "rewrite");
        let before = ask(c, &source, "status");
        // A Mac can only open saved VZ state while its console session is
        // unlocked: wait for that, and count (never hide) any refusal a lock
        // caused; the trial is then taken again once unlocked.
        let mut attempt = 0;
        let (child, t, cap, restored) = loop {
            let waited = wait_unlocked();
            if waited > 0.0 {
                lock_waits += 1;
                lock_wait_s += waited;
            }
            let child = format!("sb_e5c_{}_{i}_{attempt}", c.tag);
            attempt += 1;
            let t = Instant::now();
            match c.try_fork(&source, &child, durability) {
                Ok((cap, restored)) => break (child, t, cap, restored),
                Err(e) if lock_related(&e) => {
                    lock_refusals += 1;
                    println!("{}", json!({"trial": i, "lock_refusal": e, "child": child}));
                    let _ = c.try_call(&child, request::Command::Release(Release {}));
                }
                Err(e) => die(e),
            }
        };
        let a = c.exec(&child, &["sh", "-c", "echo fork-ok-$(hostname)"]);
        let b = c.exec(&source, &["sh", "-c", "echo source-ok-$(hostname)"]);
        let elapsed = t.elapsed().as_secs_f64() * 1000.0;
        // ---- untimed independent checks ----
        let child_user = restored
            .detail
            .get("worker_identity")
            .cloned()
            .unwrap_or_default();
        let rss_pair = (rss_kib(&source_user), rss_kib(&child_user));
        let mut state_ok = a.exit_code == 0 && b.exit_code == 0;
        state_ok &= text(&a.stdout_tail).trim() == format!("fork-ok-{}", child.replace('_', "-"));
        state_ok &=
            text(&b.stdout_tail).trim() == format!("source-ok-{}", source.replace('_', "-"));
        let (cs, cf) = (ask(c, &source, "check"), ask(c, &child, "check"));
        for v in [&cs, &cf] {
            state_ok &= num(v, "bad_pages") == 0
                && num(v, "lost_acked") == 0
                && num(v, "torn") == 0
                && field(v, "gen") == field(&gen_now, "gen");
        }
        // Same RAM at the fork point, then each side keeps running.
        state_ok &= field(&cs, "digest") == field(&cf, "digest")
            && field(&cs, "salt") == field(&cf, "salt");
        state_ok &= num(&cf, "acked") >= num(&before, "acked")
            && num(&cs, "acked") >= num(&before, "acked");
        let (later_s, later_f) = (ask(c, &source, "status"), ask(c, &child, "status"));
        state_ok &= num(&later_s, "counter") > num(&cs, "counter")
            && num(&later_f, "counter") > num(&cf, "counter");
        state_ok &= num(&later_s, "acked") > num(&before, "acked")
            && num(&later_f, "acked") > num(&before, "acked");
        let skew_s = (wall(c, &source) - now_s()).abs();
        let skew_f = (wall(c, &child) - now_s()).abs();
        let clock_ok = skew_s < 2.0
            && skew_f < 2.0
            && num(&cs, "mono_back") == 0
            && num(&cf, "mono_back") == 0;
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
        let iso_ok =
            !leaked && ids.0 != ids.1 && r1.len() == 64 && r1 != r2 && source_user != child_user;
        state_err += usize::from(!state_ok);
        clock_err += usize::from(!clock_ok);
        iso_err += usize::from(!iso_ok);
        let ok = state_ok && clock_ok && iso_ok;
        failures += usize::from(!ok);
        let pause = cap
            .detail
            .get("source_pause_us")
            .and_then(|v| v.parse::<f64>().ok())
            .unwrap_or(f64::NAN)
            / 1000.0;
        if i >= warmup {
            ms.push(elapsed);
            pauses.push(pause);
            rss.push((rss_pair.0 + rss_pair.1) as f64 / 1024.0);
        }
        println!(
            "{}",
            json!({"trial": i, "warmup": i < warmup, "fork_ms": elapsed, "source_pause_ms": pause, "ok": ok,
                   "state_ok": state_ok, "clock_ok": clock_ok, "isolation_ok": iso_ok,
                   "rss_mib": {"source": rss_pair.0 / 1024, "fork": rss_pair.1 / 1024},
                   "source_check": cs, "fork_check": cf, "before": before,
                   "clock_skew_s": {"source": skew_s, "fork": skew_f},
                   "disk_write_isolated": !leaked, "machine_ids_differ": ids.0 != ids.1,
                   "entropy_diverged": r1 != r2, "identities": [source_user, child_user],
                   "capture": cap.detail, "restore": restored.detail})
        );
        c.release(&child);
    }
    pauses.sort_by(f64::total_cmp);
    rss.sort_by(f64::total_cmp);
    eprintln!(
        "{}",
        summary(
            &format!("e05 fork dirty={dirty}MiB durability={durability}"),
            ms,
            json!({"failures": failures, "state_errors": state_err, "clock_errors": clock_err,
                   "isolation_errors": iso_err, "template": template,
                   "console_lock_waits": lock_waits, "console_lock_wait_s": lock_wait_s,
                   "console_lock_refusals": lock_refusals,
                   "source_pause_p50_ms": percentile(&pauses, 50.0), "source_pause_p99_ms": percentile(&pauses, 99.0),
                   "source_pause_max_ms": pauses.last().copied().unwrap_or(f64::NAN),
                   "pair_rss_p50_mib": percentile(&rss, 50.0), "pair_rss_max_mib": rss.last().copied().unwrap_or(f64::NAN)}),
        )
    );
    c.release(&source);
}

fn capture(
    c: &mut Ctl,
    sandbox: &str,
    id: &str,
    consistency: &str,
    memory: bool,
) -> Result<Receipt, String> {
    c.try_call(
        sandbox,
        request::Command::Capture(Capture {
            capture_id: id.into(),
            preserves_memory: memory,
            consistency: consistency.into(),
            output_handles: vec![],
        }),
    )
}

fn sh(c: &mut Ctl, id: &str, script: &str) -> String {
    text(&c.exec(id, &["/bin/sh", "-c", script]).stdout_tail)
        .trim()
        .to_string()
}

fn unix_s() -> f64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .unwrap()
        .as_secs_f64()
}

/// E06: native macOS guests through the supported API only. Prints one JSON
/// line per step/trial and a summary on stderr:
///  1. two-per-Mac cap: A runs then suspends (still holds a slot), B runs,
///     a third allocation is refused NO_CAPACITY; after B's release two
///     concurrent creates race for the one free slot and exactly one wins;
///  2. a memory fork of A is refused DEVICE_NOT_FORKABLE;
///  3. A2: a disk-only fork boots fresh from A's disk with its own machine
///     identity, boot session, hostname, machine id and an independent disk;
///  4. TRIALS durable suspend/resume cycles of A: a RAM-only shell counter
///     process survives each one with the same PID and boot session, the
///     clock is corrected and the disk keeps every acknowledged marker.
fn e06(c: &mut Ctl, args: &[String], socket: &std::path::Path) {
    // --two-running: SBX-003's exact sequence (create 2, suspend 1, reject
    // the third, delete 1 and retry), for Macs with RAM for two guests.
    let two_running = args.iter().any(|a| a == "--two-running");
    let mut args: Vec<String> = args
        .iter()
        .filter(|a| *a != "--two-running")
        .cloned()
        .collect();
    // A Linux template on the same Mac: Linux allocations take no macOS slot.
    let linux = take_flag(&mut args, "--linux-template");
    let template = args
        .first()
        .cloned()
        .unwrap_or_else(|| die("e06 TEMPLATE TRIALS [VCPUS MEMORY_MIB DISK_GIB]"));
    let trials: usize = args.get(1).and_then(|s| s.parse().ok()).unwrap_or(1);
    let res = Resources {
        vcpus: args.get(2).and_then(|s| s.parse().ok()).unwrap_or(2),
        memory_mib: args.get(3).and_then(|s| s.parse().ok()).unwrap_or(4096),
        disk_gib: args.get(4).and_then(|s| s.parse().ok()).unwrap_or(48),
        ..Default::default()
    };
    c.deadline_ms = 900_000;
    c.grant(1);
    let tag = c.tag.clone();
    let (a, b, x) = (
        format!("sb_m6a_{tag}"),
        format!("sb_m6b_{tag}"),
        format!("sb_m6x_{tag}"),
    );
    let mut failures = 0usize;
    let mut step = |name: &str, ok: bool, detail: serde_json::Value| {
        if !ok {
            failures += 1;
        }
        println!("{}", json!({"step": name, "ok": ok, "detail": detail}));
        ok
    };
    let boot = |c: &mut Ctl, id: &str| sh(c, id, "sysctl -n kern.bootsessionuuid");
    let platform = |c: &mut Ctl, id: &str| {
        sh(
            c,
            id,
            "ioreg -rd1 -c IOPlatformExpertDevice | awk -F'\"' '/IOPlatformUUID/{print $4}'",
        )
    };
    let slots = |c: &mut Ctl| {
        c.call("", request::Command::Inspect(Inspect::default()))
            .detail
            .get("macos_slots")
            .cloned()
            .unwrap_or_default()
    };

    // 1. The two-per-Mac cap, suspended allocations included.
    let t = Instant::now();
    let ra = c.try_create(&template, &a, Some(res));
    let boot_ms = t.elapsed().as_secs_f64() * 1000.0;
    let Ok(ra) = ra else {
        step("create_a", false, json!({"error": ra.err()}));
        die("macOS create failed");
    };
    let os = sh(c, &a, "sw_vers -productVersion; sw_vers -buildVersion");
    step(
        "create_a",
        os.starts_with("26."),
        json!({"create_to_command_ms": boot_ms, "sw_vers": os, "restore": ra.detail}),
    );
    let marker0 = sh(
        c,
        &a,
        "echo before-suspend > /var/db/lamp/m0 && sync && cat /var/db/lamp/m0",
    );
    let mut rb = None;
    if two_running {
        let r = c.try_create(&template, &b, Some(res));
        step(
            "create_b_both_running",
            r.is_ok(),
            json!({"error": r.as_ref().err(), "slots": slots(c)}),
        );
        rb = Some(r);
    }
    let s = capture(c, &a, "", "suspend", true);
    step(
        "suspend_a",
        s.is_ok() && marker0 == "before-suspend",
        json!({"result": s.as_ref().map(|r| r.detail.clone()).map_err(|e| e.clone()), "slots": slots(c)}),
    );
    if rb.is_none() {
        let r = c.try_create(&template, &b, Some(res));
        step(
            "create_b_while_a_suspended",
            r.is_ok(),
            json!({"error": r.as_ref().err(), "slots": slots(c)}),
        );
    }
    let rx = c.try_create(&template, &x, Some(res));
    let refused = matches!(&rx, Err(e) if e.starts_with("NO_CAPACITY"));
    step(
        "third_refused",
        refused,
        json!({"result": rx.as_ref().map(|r| r.state.clone()), "slots": slots(c)}),
    );
    if rx.is_ok() {
        c.release(&x);
    } else {
        let _ = c.try_call(&x, request::Command::Release(Release {}));
    }
    if let Some(lt) = &linux {
        let l = format!("sb_m6l_{tag}");
        let rl = c.try_create(lt, &l, None);
        let ok = rl.is_ok() && sh(c, &l, "uname -s") == "Linux";
        step(
            "linux_allocation_takes_no_macos_slot",
            ok,
            json!({"error": rl.as_ref().err(), "slots": slots(c)}),
        );
        let _ = c.try_call(&l, request::Command::Release(Release {}));
    }
    c.release(&b);
    if two_running {
        // Delete one, retry: the third now fits.
        let x2 = format!("sb_m6y_{tag}");
        let retry = c.try_create(&template, &x2, Some(res));
        step(
            "retry_after_delete",
            retry.is_ok(),
            json!({"error": retry.as_ref().err(), "slots": slots(c)}),
        );
        let _ = c.try_call(&x2, request::Command::Release(Release {}));
    }
    // Race two creates for the one free slot from two connections.
    let race: Vec<_> = (0..2)
        .map(|i| {
            let (socket, template, res, grant) = (
                socket.to_path_buf(),
                template.clone(),
                res,
                c.grants[&1].clone(),
            );
            let id = format!("sb_m6r{i}_{tag}");
            std::thread::spawn(move || {
                let mut client =
                    Client::connect(&socket, "lamp-ctl-race").unwrap_or_else(|e| die(e));
                client.grant_id = grant;
                let mut r = Ctl {
                    client,
                    op: 0,
                    tag: format!("{}r{i}", rand_tag()),
                    last_ack: None,
                    gens: Default::default(),
                    grants: Default::default(),
                    store: None,
                    deadline_ms: 900_000,
                    default_gen: 1,
                    memory_mib: None,
                };
                let out = r.try_create(&template, &id, Some(res));
                if out.is_err() {
                    let _ = r.try_call(&id, request::Command::Release(Release {}));
                }
                (id, out.map(|o| o.state))
            })
        })
        .collect();
    let race: Vec<_> = race.into_iter().map(|h| h.join().unwrap()).collect();
    let winners: Vec<_> = race
        .iter()
        .filter(|(_, r)| r.is_ok())
        .map(|(id, _)| id.clone())
        .collect();
    let losers_capacity = race.iter().all(|(_, r)| {
        r.as_ref()
            .map(|_| true)
            .unwrap_or_else(|e| e.starts_with("NO_CAPACITY"))
    });
    step(
        "concurrent_race_one_winner",
        winners.len() == 1 && losers_capacity,
        json!({"race": format!("{race:?}"), "slots": slots(c)}),
    );
    for w in winners {
        c.release(&w);
    }

    // An explicitly undersized request is refused before any slot is taken.
    let small = format!("sb_m6s_{tag}");
    let under = c.try_create(
        &template,
        &small,
        Some(Resources {
            memory_mib: 2048,
            ..res
        }),
    );
    let before = slots(c);
    step(
        "undersized_refused",
        matches!(&under, Err(e) if e.starts_with("INVALID_ARGUMENT")),
        json!({"result": format!("{:?}", under.as_ref().map(|r| r.state.clone())), "slots": before}),
    );
    let _ = c.try_call(&small, request::Command::Release(Release {}));

    // Resume A (generation 2) for the rest.
    let mut generation = 1;
    // A resume needs the console unlocked (VZ state is sealed to the console
    // session); waits and lock refusals are counted and the resume retried.
    let lock_waits = std::cell::Cell::new((0u64, 0.0f64, 0u64));
    let resume = |c: &mut Ctl, id: &str, generation: &mut u64| -> Result<(Receipt, f64), String> {
        *generation += 1;
        c.grant(*generation);
        c.gens.insert(id.to_string(), *generation);
        loop {
            let waited = wait_unlocked();
            let (n, s, refusals) = lock_waits.get();
            lock_waits.set((n + (waited > 0.0) as u64, s + waited, refusals));
            let t = Instant::now();
            match c.try_restore(id, &format!("suspend_{id}")) {
                Ok(r) => return Ok((r, t.elapsed().as_secs_f64() * 1000.0)),
                Err(e) if lock_related(&e) && refusals < 50 => {
                    let (n, s, refusals) = lock_waits.get();
                    lock_waits.set((n, s, refusals + 1));
                }
                Err(e) => {
                    *generation -= 1;
                    c.gens.insert(id.to_string(), *generation);
                    return Err(e);
                }
            }
        }
    };
    let r = resume(c, &a, &mut generation);
    let m0 = r
        .as_ref()
        .map(|_| sh(c, &a, "cat /var/db/lamp/m0"))
        .unwrap_or_default();
    step(
        "resume_a",
        r.is_ok() && m0 == "before-suspend",
        json!({"result": r.as_ref().map(|r| r.1).map_err(|e| e.clone())}),
    );
    if r.is_err() {
        die("resume failed");
    }

    // 2. No supported RAM fork for native macOS.
    let mf = capture(c, &a, &format!("cp_m6mem_{tag}"), "volatile", true);
    step(
        "memory_fork_refused",
        matches!(&mf, Err(e) if e.starts_with("DEVICE_NOT_FORKABLE")),
        json!({"result": format!("{mf:?}")}),
    );

    // 3. A2: disk-only creation, explicitly not a memory fork.
    let f = format!("sb_m6f_{tag}");
    sh(c, &a, "echo from-source-disk > /var/db/lamp/a2 && sync");
    let t = Instant::now();
    let cap = capture(c, &a, &format!("cp_m6f_{tag}"), "volatile", false);
    let fork = cap
        .clone()
        .and_then(|_| c.try_create(&format!("cp_m6f_{tag}"), &f, Some(res)));
    let a2_ms = t.elapsed().as_secs_f64() * 1000.0;
    if let (Ok(cap), Ok(fork)) = (&cap, &fork) {
        let (ba, bf) = (boot(c, &a), boot(c, &f));
        let (pa, pf) = (platform(c, &a), platform(c, &f));
        let (ia, i_f) = (
            sh(c, &a, "cat /var/db/lamp/machine-id"),
            sh(c, &f, "cat /var/db/lamp/machine-id"),
        );
        let host_f = sh(c, &f, "hostname");
        let copied = sh(c, &f, "cat /var/db/lamp/a2");
        sh(c, &f, "echo fork-only > /var/db/lamp/indep && sync");
        let leaked = sh(c, &a, "cat /var/db/lamp/indep 2>/dev/null || echo none");
        let ok = cap.detail.get("fork_mode").map(String::as_str) == Some("disk_only")
            && cap.detail.get("preserves_memory").map(String::as_str) == Some("false")
            && ba != bf
            && !pa.is_empty()
            && pa != pf
            && ia != i_f
            && host_f == f.replace('_', "-")
            && copied == "from-source-disk"
            && leaked == "none";
        step(
            "a2_disk_only_fork",
            ok,
            json!({"create_to_command_ms": a2_ms, "capture": cap.detail, "restore": fork.detail,
            "boot_session": [ba, bf], "platform_uuid": [pa, pf], "machine_id": [ia, i_f], "fork_hostname": host_f,
            "copied_marker": copied, "source_sees_fork_write": leaked}),
        );
        c.release(&f);
    } else {
        step(
            "a2_disk_only_fork",
            false,
            json!({"capture": format!("{cap:?}"), "fork": format!("{fork:?}")}),
        );
    }

    // 4. Suspend/resume trials with a RAM-only counter process.
    let pid = sh(
        c,
        &a,
        // No nohup: macOS nohup exits 127 without a console ("can't detach").
        "/bin/sh -c 'i=0; while :; do i=$((i+1)); echo $i > /var/db/lamp/ctr.tmp; mv /var/db/lamp/ctr.tmp /var/db/lamp/ctr; sleep 0.05; done' </dev/null >/dev/null 2>&1 & echo $!",
    );
    let boot0 = boot(c, &a);
    let (mut sus_ms, mut res_ms) = (Vec::new(), Vec::new());
    let mut trial_failures = 0;
    for i in 0..trials {
        let before = sh(c, &a, "cat /var/db/lamp/ctr")
            .parse::<u64>()
            .unwrap_or(0);
        sh(
            c,
            &a,
            &format!("echo trial-{i} > /var/db/lamp/m-{i} && sync"),
        );
        let t = Instant::now();
        let s = capture(c, &a, "", "suspend", true);
        let s_ms = t.elapsed().as_secs_f64() * 1000.0;
        let suspended_slots = slots(c);
        let r = s.clone().and_then(|_| resume(c, &a, &mut generation));
        let mut checks = serde_json::Map::new();
        let ok = match &r {
            Ok((_, r_ms)) => {
                let alive = sh(c, &a, &format!("kill -0 {pid} && echo alive"));
                let after = sh(c, &a, "sleep 0.3; cat /var/db/lamp/ctr")
                    .parse::<u64>()
                    .unwrap_or(0);
                let later = sh(c, &a, "sleep 0.3; cat /var/db/lamp/ctr")
                    .parse::<u64>()
                    .unwrap_or(0);
                let b = boot(c, &a);
                let skew = (sh(c, &a, "date +%s").parse::<f64>().unwrap_or(0.0) - unix_s()).abs();
                let marker = sh(c, &a, &format!("cat /var/db/lamp/m-{i}"));
                let host = sh(c, &a, "hostname");
                checks.insert("counter".into(), json!([before, after, later]));
                checks.insert("process_alive".into(), json!(alive == "alive"));
                checks.insert("same_boot_session".into(), json!(b == boot0));
                checks.insert("clock_skew_s".into(), json!(skew));
                checks.insert("marker".into(), json!(marker));
                sus_ms.push(s_ms);
                res_ms.push(*r_ms);
                alive == "alive"
                    && after >= before
                    && later > after
                    && b == boot0
                    && skew < 2.0
                    && marker == format!("trial-{i}")
                    && host == a.replace('_', "-")
            }
            Err(_) => false,
        };
        if !ok {
            trial_failures += 1;
        }
        println!(
            "{}",
            json!({"trial": i, "ok": ok, "generation": generation, "suspend_ms": s_ms,
                   "resume_to_command_ms": r.as_ref().map(|r| r.1).ok(), "slots_while_suspended": suspended_slots,
                   "suspend": s.as_ref().map(|r| r.detail.clone()).map_err(|e| e.clone()),
                   "resume": r.as_ref().map(|r| r.0.detail.clone()).map_err(|e| e.clone()), "checks": checks})
        );
        if r.is_err() {
            break;
        }
    }
    c.release(&a);
    let fails = failures + trial_failures;
    eprintln!(
        "{}",
        summary(
            "e06 suspend",
            sus_ms,
            json!({"step_failures": failures, "trial_failures": trial_failures, "failures": fails, "template": template, "resources": format!("{res:?}"),
                   "console_lock_waits": lock_waits.get().0, "console_lock_wait_s": lock_waits.get().1, "console_lock_refusals": lock_waits.get().2})
        )
    );
    eprintln!("{}", summary("e06 resume_to_command", res_ms, json!({})));
}

/// Seconds spent waiting for the Mac's console session to be unlocked.
fn wait_unlocked() -> f64 {
    #[cfg(target_os = "macos")]
    {
        let t = Instant::now();
        while lamp_engine::engine::console_locked() {
            std::thread::sleep(std::time::Duration::from_secs(2));
        }
        let waited = t.elapsed().as_secs_f64();
        if waited > 0.01 { waited } else { 0.0 }
    }
    #[cfg(not(target_os = "macos"))]
    0.0
}

/// A refusal caused by a locked console (the engine's own refusal, or VZ's
/// "permission denied" when the lock fell between the check and the call).
fn lock_related(e: &str) -> bool {
    e.contains("console session unlocked")
        || (e.contains("permission denied") && {
            #[cfg(target_os = "macos")]
            {
                lamp_engine::engine::console_locked()
            }
            #[cfg(not(target_os = "macos"))]
            {
                false
            }
        })
}
