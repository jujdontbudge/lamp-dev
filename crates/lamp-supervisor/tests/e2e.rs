//! End-to-end test against a real supervisor, real Firecracker microVMs and
//! the real guest. Nothing is mocked. Runs only when `LAMP_E2E_SOCKET` names
//! a live supervisor socket and `LAMP_E2E_GRANT` an installed control grant
//! for this UID (`LAMP_E2E_TEMPLATE` names the template, default `demo`);
//! otherwise it reports itself skipped.
//!
//! ```text
//! G=$(sudo lamp-supervisor grant-install --caller-uid $(id -u) | jq -r .grant_id)
//! LAMP_E2E_SOCKET=/run/lamp/worker.sock LAMP_E2E_GRANT=$G \
//!   cargo test -p lamp-supervisor --test e2e -- --nocapture
//! ```
use lamp_supervisor::client::Client;
use lamp_worker_protocol::*;
use std::collections::HashSet;
use std::path::PathBuf;
use std::time::{Duration, Instant};

struct T {
    c: Client,
    n: u64,
    tag: String,
}

impl T {
    fn op(&mut self) -> String {
        self.n += 1;
        format!("op_e2e_{}_{}", self.tag, self.n)
    }
    fn ctx(&mut self, sb: &str, generation: u64) -> Context {
        let op = self.op();
        self.c.context(&op, sb, generation, 60_000)
    }
    fn ok(&mut self, sb: &str, cmd: request::Command) -> Receipt {
        let ctx = self.ctx(sb, 1);
        self.c.receipt(ctx, cmd).unwrap()
    }
    fn err(&mut self, ctx: Context, cmd: request::Command) -> WorkerError {
        match self.c.call(ctx, cmd).unwrap().result {
            Some(response::Result::Error(e)) => e,
            other => panic!("expected error, got {other:?}"),
        }
    }
    fn allocate(&mut self, sb: &str) {
        self.ok(sb, request::Command::Allocate(Allocate::default()));
    }
    fn restore(&mut self, sb: &str, from: &str) -> Receipt {
        self.ok(
            sb,
            request::Command::Restore(Restore {
                capture_id: from.into(),
                ..Default::default()
            }),
        )
    }
    fn start_cmd(script: &str) -> request::Command {
        request::Command::Start(Start {
            argv: vec!["sh".into(), "-c".into(), script.into()],
            ..Default::default()
        })
    }
    /// Starts a command; returns its operation id and the receipt.
    fn start(&mut self, sb: &str, script: &str) -> (String, Receipt) {
        let op = self.op();
        let ctx = self.c.context(&op, sb, 1, 60_000);
        let r = self.c.receipt(ctx, Self::start_cmd(script)).unwrap();
        assert_eq!(r.state, "command_started");
        (op, r)
    }
    fn wait(&mut self, sb: &str, op: &str) -> (i32, String) {
        let c = self
            .c
            .wait_command(sb, 1, op, Duration::from_secs(60))
            .unwrap();
        (
            c.exit_code,
            String::from_utf8_lossy(&c.stdout_tail).trim().to_string(),
        )
    }
    fn sh(&mut self, sb: &str, script: &str) -> (i32, String) {
        let (op, _) = self.start(sb, script);
        self.wait(sb, &op)
    }
    fn capture(&mut self, sb: &str, id: &str) -> Receipt {
        self.ok(
            sb,
            request::Command::Capture(Capture {
                capture_id: id.into(),
                preserves_memory: true,
                consistency: "crash".into(),
                output_handles: vec![],
            }),
        )
    }
    fn release(&mut self, sb: &str) {
        self.ok(sb, request::Command::Release(Release {}));
    }
}

/// Processes in a sandbox's jail cgroup, by name.
fn jail_processes(sb: &str) -> Vec<String> {
    let cg = format!("/sys/fs/cgroup/lamp/{}/cgroup.procs", sb.replace('_', "-"));
    std::fs::read_to_string(cg)
        .unwrap()
        .split_whitespace()
        .map(|pid| {
            std::fs::read_to_string(format!("/proc/{pid}/comm"))
                .unwrap_or_default()
                .trim()
                .to_string()
        })
        .collect()
}

/// A process that, every millisecond, reads the machine-id and 16 bytes of
/// kernel randomness and appends them to a guest RAM file.
const PROBE: &str = "setsid python3 -c \"import os,time\nf=open('/run/probe.log','a',buffering=1)\ni=0\nwhile True:\n  m=open('/etc/machine-id').read().strip(); r=os.urandom(16).hex()\n  f.write(f'{i} {m} {r}\\n'); i+=1; time.sleep(0.001)\" >/dev/null 2>&1 & sleep 0.3";

#[test]
fn create_command_and_memory_fork() {
    let Some(socket) = std::env::var_os("LAMP_E2E_SOCKET").map(PathBuf::from) else {
        eprintln!("skipped: set LAMP_E2E_SOCKET to run against a live supervisor");
        return;
    };
    let template = std::env::var("LAMP_E2E_TEMPLATE").unwrap_or_else(|_| "demo".into());
    let tag = format!("{:x}", std::process::id());
    let mut c = Client::connect(&socket, "lamp-e2e").unwrap();
    c.grant_id = std::env::var("LAMP_E2E_GRANT").expect("LAMP_E2E_GRANT names a control grant");
    let mut t = T {
        c,
        n: 0,
        tag: tag.clone(),
    };
    assert_eq!(t.c.server.protocol, PROTOCOL);
    let (src, child) = (format!("sb_e2e_src_{tag}"), format!("sb_e2e_kid_{tag}"));

    // S9: no or a wrong grant is refused before any effect.
    let mut ctx = t.ctx(&src, 1);
    ctx.grant_id = "gr_doesnotexist".into();
    assert_eq!(
        t.err(ctx, request::Command::Allocate(Allocate::default()))
            .code,
        "UNAUTHENTICATED"
    );
    let ctx = t.ctx(&src, 2);
    assert_eq!(
        t.err(ctx, request::Command::Allocate(Allocate::default()))
            .code,
        "SCOPE_DENIED",
        "generation outside the grant"
    );

    // Create: accepted is not ready; the receipt comes after the barrier.
    let start = Instant::now();
    t.allocate(&src);
    let r = t.restore(&src, &template);
    assert_eq!(r.state, "restored");
    let (code, out) = t.sh(
        &src,
        "uname -s; grep -c ^VERSION_CODENAME=trixie /etc/os-release; hostname",
    );
    let create_ms = start.elapsed().as_secs_f64() * 1000.0;
    assert_eq!(code, 0);
    assert_eq!(out, format!("Linux\n1\n{}", src.replace('_', "-")));
    eprintln!("create to command: {create_ms:.1} ms");

    // S1: the jail holds only the jailed VMM; no other host process
    // (page server or helper) serves guest memory.
    assert_eq!(jail_processes(&src), vec!["firecracker".to_string()]);

    // S5: Start is acknowledged when the command started, not when it exits.
    let t0 = Instant::now();
    let (long_op, ack) = t.start(&src, "sleep 3; echo slept");
    assert!(
        t0.elapsed() < Duration::from_secs(1),
        "acknowledged at start"
    );
    assert_eq!(ack.command.as_ref().unwrap().finished_at_unix_ns, 0);

    // A RAM-only marker (never written to disk), the identity probe, a disk file.
    let (code, _) = t.sh(
        &src,
        "setsid python3 -c \"b=bytearray(64<<20)\nfor i in range(0,len(b),4096): b[i:i+8]=i.to_bytes(8,'little')\nimport socket\ns=socket.socket(socket.AF_UNIX); s.bind('/run/m.sock'); s.listen(1)\nwhile True:\n  c,_=s.accept(); bad=sum(1 for i in range(0,len(b),4096) if b[i:i+8]!=i.to_bytes(8,'little')); c.send(str(bad).encode()); c.close()\" >/dev/null 2>&1 & \
         for i in $(seq 200); do [ -S /run/m.sock ] && exit 0; sleep 0.05; done; exit 1",
    );
    assert_eq!(code, 0);
    assert_eq!(t.sh(&src, PROBE).0, 0);
    t.sh(&src, "echo shared-before-fork > /root/f; sync");

    // Fork with memory while `sleep 3` still runs in the source.
    let start = Instant::now();
    let cap = t.capture(&src, &format!("capture_e2e_{tag}"));
    assert_eq!(cap.state, "captured");
    // M1: the capture holds all configured RAM, not a recently-written subset.
    assert_eq!(cap.detail["memory_bytes"], (4_u64 << 30).to_string());
    assert!(cap.detail.contains_key("full_write_us"));
    t.allocate(&child);
    t.restore(&child, &format!("capture_e2e_{tag}"));
    let (a, _) = t.sh(&child, "true");
    let (b, _) = t.sh(&src, "true");
    let fork_ms = start.elapsed().as_secs_f64() * 1000.0;
    assert_eq!((a, b), (0, 0));
    eprintln!(
        "memory fork: {fork_ms:.1} ms (full write {} us, source pause {} us)",
        cap.detail["full_write_us"], cap.detail["source_pause_us"]
    );
    assert_eq!(jail_processes(&child), vec!["firecracker".to_string()]);

    // S2: other users can reach neither capture images nor jail memory.
    for private in [
        "/run/lamp/mem",
        "/var/lib/lamp/jail",
        "/var/lib/lamp/captures",
    ] {
        let e = std::fs::read_dir(private).unwrap_err();
        assert_eq!(e.kind(), std::io::ErrorKind::PermissionDenied, "{private}");
    }

    // S5: the command that spanned the capture completes in the source.
    assert_eq!(t.wait(&src, &long_op), (0, "slept".into()));

    let ask = "python3 -c \"import socket; s=socket.socket(socket.AF_UNIX); s.connect('/run/m.sock'); print(s.recv(64).decode())\"";
    assert_eq!(t.sh(&src, ask), (0, "0".into()), "source RAM marker");
    assert_eq!(t.sh(&child, ask), (0, "0".into()), "fork RAM marker");
    assert_eq!(t.sh(&child, "cat /root/f").1, "shared-before-fork");
    t.sh(&child, "echo fork-only > /root/f; sync");
    assert_eq!(
        t.sh(&src, "cat /root/f").1,
        "shared-before-fork",
        "disk writes are private"
    );
    let (src_mid, kid_mid) = (
        t.sh(&src, "cat /etc/machine-id").1,
        t.sh(&child, "cat /etc/machine-id").1,
    );
    assert_ne!(src_mid, kid_mid);
    assert_eq!(t.sh(&child, "hostname").1, child.replace('_', "-"));
    let rnd = "head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \\n'";
    assert_ne!(t.sh(&src, rnd).1, t.sh(&child, rnd).1, "entropy diverged");

    // S3: a process already running in the parent never sees the parent's
    // identity or entropy after the fork. Lines both copies share were
    // written before the freeze; at most one line may straddle it. Every
    // later fork line must carry the fork's machine-id and fresh entropy.
    std::thread::sleep(Duration::from_millis(300));
    let parent_log = t.sh(&src, "cat /run/probe.log").1;
    let fork_log = t.sh(&child, "cat /run/probe.log").1;
    let (p, f): (Vec<&str>, Vec<&str>) = (parent_log.lines().collect(), fork_log.lines().collect());
    let shared = p.iter().zip(&f).take_while(|(a, b)| a == b).count();
    let parent_rand: HashSet<&str> = p.iter().filter_map(|l| l.split(' ').nth(2)).collect();
    let after: Vec<&str> = f.iter().skip(shared + 1).copied().collect();
    assert!(
        after.len() >= 20,
        "probe kept running in the fork ({} lines)",
        after.len()
    );
    for line in &after {
        let parts: Vec<&str> = line.split(' ').collect();
        assert_eq!(
            parts[1], kid_mid,
            "fork task saw the parent's machine-id: {line}"
        );
        assert!(
            !parent_rand.contains(parts[2]),
            "fork task repeated parent entropy: {line}"
        );
    }
    assert!(
        p.iter()
            .skip(shared)
            .any(|l| l.split(' ').nth(1) == Some(&src_mid)),
        "probe kept running in the parent"
    );
    eprintln!(
        "identity probe: {shared} shared lines, {} fork lines checked",
        after.len()
    );

    // Authority: wrong generation is refused before any guest effect.
    let ctx = t.ctx(&child, 2);
    let e = t.err(ctx, T::start_cmd("true"));
    assert_eq!(e.code, "SCOPE_DENIED");
    // Idempotency: a replay returns the prior receipt, a different command
    // conflicts. Map order never matters (S4): env is sent from a HashMap.
    let ctx = t.ctx(&src, 1);
    let probe = request::Command::Start(Start {
        argv: vec!["env".into()],
        env: (0..16)
            .map(|i| (format!("K{i}"), format!("v{i}")))
            .collect(),
        ..Default::default()
    });
    let r1 = t.c.receipt(ctx.clone(), probe.clone()).unwrap();
    let mut again = ctx.clone();
    again.request_id.push_str("_retry");
    let reordered = match probe {
        request::Command::Start(mut s) => {
            s.env = s
                .env
                .into_iter()
                .collect::<Vec<_>>()
                .into_iter()
                .rev()
                .collect();
            request::Command::Start(s)
        }
        _ => unreachable!(),
    };
    let r2 = t.c.receipt(again.clone(), reordered).unwrap();
    assert_eq!(
        r1.command.unwrap().started_at_unix_ns,
        r2.command.unwrap().started_at_unix_ns,
        "replayed, not rerun"
    );
    let mut clash = ctx.clone();
    clash.request_id.push_str("_clash");
    let e = t.err(clash, request::Command::Stop(Stop { force: true }));
    assert_eq!(e.code, "IDEMPOTENCY_CONFLICT");
    // Unknown template and disk-only capture are rejected.
    let bad = format!("sb_e2e_bad_{tag}");
    t.allocate(&bad);
    let ctx = t.ctx(&bad, 1);
    let e = t.err(
        ctx,
        request::Command::Restore(Restore {
            capture_id: "no-such-template".into(),
            ..Default::default()
        }),
    );
    assert_eq!(e.code, "NOT_FOUND");
    let ctx = t.ctx(&src, 1);
    let e = t.err(
        ctx,
        request::Command::Capture(Capture {
            capture_id: format!("cp_x_{tag}"),
            preserves_memory: false,
            ..Default::default()
        }),
    );
    assert_eq!(e.code, "INVALID_ARGUMENT");

    // Source deletion leaves the fork running with its memory.
    t.release(&src);
    assert_eq!(t.sh(&child, ask), (0, "0".into()));
    t.release(&child);
    t.release(&bad);
}
