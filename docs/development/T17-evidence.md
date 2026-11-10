# T17 broker evidence — 2026-11-10

## Result and limits

The new standalone `lamp-broker` runs on the owner's Mac and LX machines. One
real LX Firecracker guest completed a useful-code journey through authenticated
TLS model/MCP fixtures, proposed settings edits, received a selected-path owner
apply and wrote to a disposable SQLite-backed service. A real full guest
memory/disk capture had **zero raw provider-canary matches**. The real signed-in
`gh` CLI also read the public third-party `cli/cli` repository through the broker
on the Mac.

**This is partial T17 delivery, not completion of every Goal compatibility row.**
The model/MCP vendors in these demonstrations are explicitly disposable,
authenticated fixtures, not OpenRouter or an external signed-in MCP provider.
Real OpenRouter, Stripe and scoped AWS account demonstrations remain missing;
Stripe/AWS command adapters are implemented but not claimed proven against those
accounts. One no-charge real `aws sts get-caller-identity` request through the
broker returned HTTP 502 / `UPSTREAM_UNAVAILABLE`; the
[redacted result](evidence/t17/aws-identity.json) records the failure without
credentials or account details. The broker intentionally withheld CLI stderr,
so this is not a claim about the presence/absence of AWS credentials. The host
transcript's remaining-account labels describe unexercised routes, not an
account inventory. No credentials were acquired, printed or exported to work
around the credential-access safety hold. No money was spent.

The initial usable interface is the broker's own owner/task HTTP protocol.
Canonical `lampd` connection/settings lifecycle and attachment wiring, automatic
fork/generation reauthorization, MCP OAuth enrollment/refresh, transparent TLS
interception, streaming and broad unmodified-client compatibility remain
unimplemented. These are explicit gaps, not passing evidence. See the
[supported surface and exclusions](T17-broker.md).

## One real LX sandbox journey

The [transcript](evidence/t17/sandbox-demo/transcript.jsonl) pins all four real
binaries by SHA-256 and records sandbox
`sb_456e2f3ad448593aca47a76eb65b691c` from T17's own signed template
`tp_8a6922170222418fabafdc0c95`.

1. Immediately before allocation, `pgrep -c firecracker` returned **0** and
   `free -m` showed **25,909 MiB available**. No owner service was stopped.
2. The public control HTTP API created the real sandbox and observed `ready`.
3. A guest Python harness called the broker's OpenAI-compatible chat route over
   verified TLS. The authenticated model fixture returned Python code; the guest
   ran it and independently obtained `55`, writing `/tmp/t17-result`.
4. The same guest invoked the authenticated MCP gateway and read the fixture's
   real SQLite notes through initialize/initialized/tools-call.
5. The guest copied chosen settings into its own filesystem, edited both
   `AGENTS.md` and `harness.json`, and proposed their return. The host source was
   unchanged. The owner-UID Unix endpoint explicitly applied only `AGENTS.md`;
   the host's unselected `harness.json` stayed unchanged.
6. With the service's configured access set to `full`, the guest appended a note
   to the actual disposable database and observed count `2`.
7. `lamp-supervisor ctl capture` produced **one full memory/disk capture**, without
   allocating another guest. An independent host process scanned all non-hole
   bytes of its four files: **4,799,963,923 bytes scanned**, spanning
   **25,769,825,043 logical bytes**, with **0 raw canary occurrences**. Sparse
   zero-only holes cannot contain this nonzero ASCII canary. The provider canary
   entered the scanner only over host stdin, never guest memory, argv or logs.
8. The API deleted the sandbox and observed `deleted` immediately after the
   useful work/capture observation. The scoped broker grant was revoked.
9. The transcript, broker audit/stderr, control stderr and worker log were
   independently checked for the raw provider canary: **0 matches**.

This is a real guest, actual engine capture, actual filesystem writes, actual
cryptographic TLS and actual service database effects. The **vendor boundary**
is the stated model/MCP fixture. It is not proof of arbitrary transformed-secret
noninterference or the whole attack suite; the independent canary scan is raw
byte matching. No external-provider key was used in this journey.

[Worker log](evidence/t17/sandbox-demo/worker.log),
[broker audit](evidence/t17/sandbox-demo/audit.jsonl),
[broker stderr](evidence/t17/sandbox-demo/broker.log),
[control stderr](evidence/t17/sandbox-demo/lampd.log),
[cleanup](evidence/t17/sandbox-demo/cleanup.json), and
[post-cleanup observations](evidence/t17/sandbox-demo/post-cleanup.json) are
retained. The published worker-log copy normalizes trailing whitespace only;
the original remains unchanged on LX. The actual script is
`tools/broker_sandbox_demo.py`.

## Host feature demonstration

Command (exit **0**):

```sh
python3 tools/broker_demo.py --binary /tmp/lamp-broker-t17 \
  --root /tmp/lamp-t17-host-demo-1
```

The [host transcript](evidence/t17/host-demo/transcript.jsonl) and
[audit](evidence/t17/host-demo/audit.jsonl) record:

- **CRED-005 route:** host-memory-only fixture bearer injection over verified
  upstream TLS, useful runnable code, and blocked chunk-split credential
  reflection (`OUTPUT_BLOCKED`), with no canary in published output/logs.
- **CRED-006:** no host write before approval; only the selected file returned;
  concurrent source edits rejected with `REVISION_CONFLICT`; symlink reads
  rejected. A host-only Ed25519 key signed an approved Git commit-shaped object
  using the SSH `git` namespace, and real `ssh-keygen -Y verify` accepted it.
- **CON-001 gateway:** actual authenticated TLS MCP initialize/tools-call,
  useful database read, and denial of an unlisted token-export operation. OAuth
  lifecycle and a third-party signed-in server are not demonstrated.
- **CON-002:** the actual signed-in `gh` binary read repository `cli/cli`, returning
  its name, description, URL and star count. An auth-token operation was denied.
  This CLI proof runs on the Mac host through the scoped task interface, not an
  unmodified CLI in the LX guest. Stripe/AWS real-account work remains unproven.
- **CON-003:** `off` denied a read; `read_only` allowed a read and denied append;
  `ask_first` produced no database effect until owner approval, allowed exactly
  the approved body, and denied changed-body/replay attempts; `full` appended
  within the same pinned endpoint. Independently read database rows were exactly
  `initial`, `approved`, `full`.

## Isolation, failed setup and cleanup

All writable resources were T17-owned:

- `/home/johan/lamp-t17/` — sources, binaries, fixture state and retained runs;
- `/var/lib/lamp/t17-broker/` — engine store, producer key and root-owned binaries;
- `/run/lamp-t17/` — private runtime memory and network namespaces;
- `/var/lib/lamp-audit/t17-broker/` — guard audit;
- temporary interface `lamp-t17br`, with one `/32` address and an exact broker
  TCP-port exception, removed after the demonstration.

The initial prepared seed/template were copied **read-only** from the prepared
T10 store; no source store, key or service was changed. Importing the template
correctly failed producer trust, so a new template was built from the seed and
signed by T17's own producer, rather than copying another producer's key or
weakening trust. Both supervisor and guard were built from this checkout and
installed only in T17's directory. The supervisor now exposes its existing
runtime path/UID/host settings so independent stores need not share jail UIDs.

Failed/incomplete setup is retained, never overwritten: missing rsync parent;
quarantined copied template (`sandbox-demo-1`); a shell-quoting error in the first
template preparation; and a primary-address namespace routing timeout
(`sandbox-demo-2`). The corrected `sandbox-demo-3` passed. These were diagnosed
setup/feature iterations, not repeated trials or a benchmark loop. Admission was
checked before each actual guest/template allocation. Cleanup confirmed no T17
processes or sandboxes, no temporary interface, and zero total Firecracker
processes at observation. Other tickets' stores/services and all Docker objects
were left untouched.

Private control journals contain short-lived **Lamp transport authority**, never
provider keys; they are not published. Fixture TLS/signing material was removed
by the fixture's cleanup. Captures were scanned while the source existed and
released with it; only secret-free observations are published here.

## Verification

No new unit/benchmark/attack-suite tests were added. Focused builds/vet and the
real demonstrations were used while iterating. `make bootstrap` passed (exit 0).
The required single `make verify` passed (exit **0**), including workspace builds,
existing Go/Rust/Python suites, contract checks, cross-process interoperability,
vet, formatting and clippy. The [complete log](evidence/t17/make-verify.log) is
retained. No benchmark workload or trial loop was run.

That full check and the pinned engine demonstration preceded integration of
`origin/main`'s T7 storage merge (`09899fd`). The broker implementation was
unchanged by the base update. Its overlapping supervisor flags/capture command
were reconciled with T7's implementations rather than duplicated. Post-merge
focused build/vet/format/clippy and Linux supervisor-check outcomes:

- `go build -o /tmp/lamp-broker-t17-final ./cmd/lamp-broker`: **PASS**, exit 0.
- `go vet ./internal/broker ./cmd/lamp-broker`: **PASS**, exit 0.
- `cargo fmt --all -- --check`: **PASS**, exit 0.
- `cargo clippy --locked -p lamp-supervisor --all-targets -- -D warnings`:
  initially failed (exit 101) on the duplicate capture match introduced by the
  merge; the duplicate was removed, and the final focused check **passed**,
  exit 0 ([log](evidence/t17/post-merge-clippy.log)).
- On LX, `cargo check --locked -p lamp-supervisor`: **PASS**, exit 0, without
  warnings after reconciliation ([log](evidence/t17/post-merge-linux-check.log)).

The full suite and real guest journey were not repeated after the base update;
the exact demonstrated binaries remain pinned in the transcript. No new final
engine/storage certification is implied by these focused compilation checks.
