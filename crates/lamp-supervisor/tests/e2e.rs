//! End-to-end test against a real supervisor, real Firecracker microVMs and
//! the real guest. Nothing is mocked. Runs only when `LAMP_E2E_SOCKET` names
//! a live supervisor socket (and `LAMP_E2E_TEMPLATE` a template, default
//! `demo`); otherwise it reports itself skipped.
//!
//! `LAMP_E2E_SOCKET=/run/lamp/worker.sock cargo test -p lamp-supervisor --test e2e -- --nocapture`
use lamp_supervisor::client::Client;
use lamp_worker_protocol::*;
use std::path::PathBuf;
use std::time::Instant;

struct T {
    c: Client,
    n: u64,
    tag: String,
}

impl T {
    fn ctx(&mut self, sb: &str, generation: u64) -> Context {
        self.n += 1;
        let op = format!("op_e2e_{}_{}", self.tag, self.n);
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
    fn sh(&mut self, sb: &str, script: &str) -> (i32, String) {
        let r = self.ok(
            sb,
            request::Command::Start(Start {
                argv: vec!["sh".into(), "-c".into(), script.into()],
                ..Default::default()
            }),
        );
        let c = r.command.unwrap();
        (
            c.exit_code,
            String::from_utf8_lossy(&c.stdout_tail).trim().to_string(),
        )
    }
    fn release(&mut self, sb: &str) {
        self.ok(sb, request::Command::Release(Release {}));
    }
}

#[test]
fn create_command_and_memory_fork() {
    let Some(socket) = std::env::var_os("LAMP_E2E_SOCKET").map(PathBuf::from) else {
        eprintln!("skipped: set LAMP_E2E_SOCKET to run against a live supervisor");
        return;
    };
    let template = std::env::var("LAMP_E2E_TEMPLATE").unwrap_or_else(|_| "demo".into());
    let tag = format!("{:x}", std::process::id());
    let mut t = T {
        c: Client::connect(&socket, "lamp-e2e").unwrap(),
        n: 0,
        tag: tag.clone(),
    };
    assert_eq!(t.c.server.protocol, PROTOCOL);
    let (src, child) = (format!("sb_e2e_src_{tag}"), format!("sb_e2e_kid_{tag}"));

    // Create: accepted is not ready; the receipt comes after the barrier.
    let start = Instant::now();
    t.allocate(&src);
    let r = t.restore(&src, &template);
    assert_eq!(r.state, "running");
    let (code, out) = t.sh(
        &src,
        "uname -s; grep -c ^VERSION_CODENAME=trixie /etc/os-release; hostname",
    );
    let create_ms = start.elapsed().as_secs_f64() * 1000.0;
    assert_eq!(code, 0);
    assert_eq!(out, format!("Linux\n1\n{}", src.replace('_', "-")));
    eprintln!("create to command: {create_ms:.1} ms");

    // A RAM-only marker (never written to disk) plus a live counter.
    let (code, _) = t.sh(
        &src,
        "setsid python3 -c \"import time,os\nb=bytearray(64<<20)\nfor i in range(0,len(b),4096): b[i:i+8]=i.to_bytes(8,'little')\nimport socket\ns=socket.socket(socket.AF_UNIX); s.bind('/run/m.sock'); s.listen(1)\nwhile True:\n  c,_=s.accept(); bad=sum(1 for i in range(0,len(b),4096) if b[i:i+8]!=i.to_bytes(8,'little')); c.send(str(bad).encode()); c.close()\" >/dev/null 2>&1 & \
         for i in $(seq 200); do [ -S /run/m.sock ] && exit 0; sleep 0.05; done; exit 1",
    );
    assert_eq!(code, 0);
    t.sh(&src, "echo shared-before-fork > /root/f; sync");

    // Fork with memory.
    let start = Instant::now();
    let cap = t.ok(
        &src,
        request::Command::Capture(Capture {
            capture_id: format!("cp_e2e_{tag}"),
            preserves_memory: true,
            consistency: "volatile".into(),
            output_handles: vec![],
        }),
    );
    assert_eq!(cap.state, "captured");
    t.allocate(&child);
    t.restore(&child, &format!("cp_e2e_{tag}"));
    let (a, _) = t.sh(&child, "true");
    let (b, _) = t.sh(&src, "true");
    let fork_ms = start.elapsed().as_secs_f64() * 1000.0;
    assert_eq!((a, b), (0, 0));
    eprintln!(
        "memory fork: {fork_ms:.1} ms, source pause {} us",
        cap.detail["source_pause_us"]
    );

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
    assert_ne!(
        t.sh(&src, "cat /etc/machine-id").1,
        t.sh(&child, "cat /etc/machine-id").1
    );
    assert_eq!(t.sh(&child, "hostname").1, child.replace('_', "-"));
    let rnd = "head -c 32 /dev/urandom | od -An -tx1 | tr -d ' \\n'";
    assert_ne!(t.sh(&src, rnd).1, t.sh(&child, rnd).1, "entropy diverged");

    // Authority: wrong generation is refused before any guest effect.
    let ctx = t.ctx(&child, 2);
    let e = t.err(
        ctx,
        request::Command::Start(Start {
            argv: vec!["true".into()],
            ..Default::default()
        }),
    );
    assert_eq!(e.code, "REVISION_CONFLICT");
    // Idempotency: a replay returns the prior receipt, a different command conflicts.
    let ctx = t.ctx(&src, 1);
    let probe = request::Command::Start(Start {
        argv: vec!["true".into()],
        ..Default::default()
    });
    let r1 = t.c.receipt(ctx.clone(), probe.clone()).unwrap();
    let mut again = ctx.clone();
    again.request_id.push_str("_retry");
    let r2 = t.c.receipt(again.clone(), probe).unwrap();
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

    // Source deletion leaves the fork running.
    t.release(&src);
    assert_eq!(t.sh(&child, ask), (0, "0".into()));
    t.release(&child);
    t.release(&bad);
}
