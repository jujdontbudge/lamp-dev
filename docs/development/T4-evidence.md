# T4 implementation evidence

2026-11-09. These are **real Linux kernel integration tests**, not mocked policy
checks. They demonstrate the guard and protected task collector together over an
isolated network fixture. **They do not establish the full microVM demo, NET-001
on the T2 engine, capture/fork integration, or the E02 >=0.97 secured speed floor.**
Those remain explicit gates in the [integration contract](T4-guard.md).

## Reproduction and raw evidence

- [LX run](evidence/t4/lx.log): native Linux x86_64, kernel 7.0.0-42-generic;
  seven Rust unit tests, strict clippy, and all twelve integration assertions pass.
- [LA run](evidence/t4/la.log): Linux aarch64 in the owner's existing Lima VM,
  kernel 7.0.0-38-generic; the same tests and clippy pass. This is functional
  evidence only, not native arm64 speed qualification.
- [M5 workspace verification](evidence/t4/m5-verify.log): `make verify` passes,
  including Go/Rust build/tests, contract and CI admission checks, interop,
  generation parity, formatting and clippy. The non-Linux guard executable fails
  explicitly; no alternate unguarded implementation is selected.
- [Tested source hashes](evidence/t4/source.sha256).
- Summaries include the actual kernel/architecture, binary SHA-256 and test-file
  SHA-256: [LX](evidence/t4/lx/summary.json), [LA](evidence/t4/la/summary.json).
- Actual host-written, payload-free records:
  [LX connections](evidence/t4/lx/network.ndjson),
  [LX processes](evidence/t4/lx/process.ndjson),
  [LA connections](evidence/t4/la/network.ndjson),
  [LA processes](evidence/t4/la/process.ndjson), and immediately dying fork trees
  ([LX](evidence/t4/lx/rapid-fork.ndjson), [LA](evidence/t4/la/rapid-fork.ndjson)).

Commands on each Linux lane:

```sh
cargo test -p lamp-guard --locked
cargo clippy -p lamp-guard --all-targets --locked -- -D warnings
cargo build -p lamp-guard --locked
sudo -n python3 tests/guard/e2e.py "$PWD/target/debug/lamp-guard"
```

The test makes a guest link, guard namespace and isolated edge with TCP/UDP
reflectors listening on both allowed and denied fixture addresses. An actual
namespace-root task is launched through the protected collector on that same
link; its public UDP exchange succeeds and metadata exchange times out. The
edge's `8.8.8.8` address is a **local fixture**, not a claim that Google/public
internet connectivity was used. No external metadata endpoint is contacted.

The twelve assertions cover:

1. Guard drops root, retains only net-admin, and has seccomp/no-new-privileges
   plus only assigned descriptors.
2. Public IPv4 TCP and UDP work; RFC1918, metadata, fleet/control, host and
   public-numbered LAN destinations fail despite live upstream reflectors.
3. Guest-root firewall changes and source-address spoofing cannot bypass it.
4. Ordered, attributed host records include allow/deny and both byte directions.
5. Every independently reported fork is recorded (three without exec), executable
   transitions use real file identity despite deceptive argv[0], credentials are
   observed, and a thread is not counted as another process start. An additional
   immediate-kill/parent-exit case verifies that creation is recorded at the
   parent's fork stop, before allowing it to run and kill the new child.
6. Task root cannot access the ancestor tracer or audit FD; CLONE_UNTRACED is
   denied, clone3 forces a traced fallback, and alternate vsock/io_uring paths
   are denied.
7. The host audit receiver is unprivileged, capability-free and confined.
8. Killing the host receiver kills an otherwise CPU-only task tree within the
   required five seconds, without relying on another task syscall.
9. SIGKILL of the guest tracer cannot leave an unobserved tree running.
10. SIGKILL of the guard blocks **existing TCP and UDP** sockets and new dials;
    the host-owned nft queue rules stay installed.
11. Restart does not merge into/replace an old generation's table.
12. Filling the reserved spool terminates the guard and closes the network path.

The unit tests also reject malformed/truncated packet/control framing, cover
NFQUEUE's unpadded final attribute, reject truncated packet copies and audit
sequence/ACK mismatches, and prove a full spool cannot overwrite acknowledged
history. Expected stderr for an already-existing nft table and an abruptly
closed audit stream belongs to intentional negative tests, not a failed run.

No owner's service was stopped. The LX log ends with the Chrome user service
still `active`. The T3 server was untouched. No Docker object, host firewall,
host sysctl, startup service or paid service was used/changed. Forwarding changes
were confined to disposable namespaces; LA was already running and remains so.

## What this evidence does not cover

See [explicit remaining gates](T4-guard.md#explicit-remaining-gates): T2 TAP/vsock
and all-session wiring, generation/fork/capture rebinding, host route-change
fencing, VM/kernel/service confinement, actual nested Docker and forced PID
reuse, IPv6/public DNS/PMTU support, native macOS, and T23's encrypted signed
ledger. The conservative per-packet durable write and syscall tracing are **not
performance-certified**. No speed claim here uses disabled security controls.
