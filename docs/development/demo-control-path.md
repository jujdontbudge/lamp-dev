# Engine demo control path (T3)

The only engine path in `lamp` is HTTP over `lampd`'s owner-only Unix socket.
`lampd` journals admission before dispatch, then calls the enrolled Rust
supervisor over the existing `lamp.worker/2` Protobuf framing. Go owns public
operation/resource state; it does not boot VMs, fork processes, open guest RAM,
or accept caller-supplied host storage paths.

This is the single-local-caller demo, not T10's multi-user control core. The local
OS owner is represented as `pr_local` in `fl_local`. There is no TCP listener,
MCP server, template builder, account setup, secrets support, bulk output
streaming, operation cancellation, or background resource reconciler here.
Unsupported setup fields are rejected, never silently skipped. Volatile and local
memory forks are admitted; replicated and backup durability fail with
`QUORUM_REQUIRED` before any worker effect. T2 owns the real engine and
template preparation;
T4 owns its guard/audit attachment. T6 must join them and measure the engine gate.

## Start against the enrolled supervisor

Build `go build -o bin/lamp ./cmd/lamp` and
`go build -o bin/lampd ./cmd/lampd` (create `bin/` first).
The supervisor installer must supply its assigned OS UID, worker identity,
boot identity, socket, and installed control grant. A string passed as
`--grant-id` is only a reference, not authority minted by `lampd`; the Rust
receiver must validate its live supervisor-installed authority before effect.
Never disable the worker's grant/generation/handle checks to run the demo.

```sh
bin/lampd --worker-socket "$WORKER_SOCKET" \
  --worker-id "$WORKER_ID" --worker-boot-id "$WORKER_BOOT_ID" \
  --worker-uid "$WORKER_UID" \
  --grant-id "$CONTROL_GRANT_ID" \
  --audit-release "$AUDIT_RELEASE_ARGV"
```

Default socket: `~/.local/state/lamp-demo/api.sock`; journal:
`~/.local/state/lamp-demo/control/`. Override with `--socket` and `--state-dir`.
Their final directories must be owner-owned mode 0700, with trusted parent
paths; the socket is mode 0600. HTTP peers must match the daemon's OS UID.
Worker UID is checked before Hello, followed by exact enrolled worker/host-boot
identities, protocol version, and negotiated frame maximum. The legacy
`--worker-pid` hint is accepted but no longer pins a supervisor process across
restarts. A state-directory lock prevents simultaneous writers. Startup removes
only a stale Unix socket that refuses connections, preserving live listeners.

On Linux, `AUDIT_RELEASE_ARGV` is a JSON argv for the trusted
`tools/guard-audit-release.py` with this supervisor's audit root. For example,
`["sudo","-n","/usr/bin/python3","/absolute/path/guard-audit-release.py","/var/lib/lamp/audit"]`.
The release command captures records durably before removing host spools. Linux
worker installs refuse startup without it; the installer supplies it. On macOS,
which has no host guard spools, lampd defaults to `/usr/bin/true`.
Do not substitute a no-op for a Linux guard-spool release command.

Deleting a sandbox releases its spools before a waiting delete returns. A failed
release retains the reservation, logs the command and sandbox ID, and is retried
on startup and every minute. Fix the command and restart lampd to retry immediately.
`lamp quota --json` (`GET /v1/metrics`, MCP `metric_list`) shows effective control
limits, charged usage, and pending audit-release IDs. Engine admission additionally
checks current host resources and reports their numbers in `NO_CAPACITY` errors.

`sandbox list` hides deleted records. Use `--include-deleted true` (HTTP
`?include_deleted=true`, MCP `include_deleted:true`) to inspect them for 24 hours;
after audit release and that retention period their sandbox records are purged.
Audit captures and operation/idempotency history retain their own quotas.

Upgrade lampd and the supervisor together: worker protocol 2 adds side-effect-free
admission preflight and a shared-disk charge. Reflink forks reserve 1 GiB of initial
CoW headroom instead of the full logical disk, and further writes remain subject
to actual store space and the sandbox's logical disk limit. Existing resident
disks are already reflected in filesystem free space.

Systemd and launchd installs restart crashes automatically. Process installs are
unsupervised; `lamp-installer status` reports that and gives the restart command.
`lamp-installer restart --prefix PREFIX` works even if lampd is down. Volatile
guests are destroyed by engine recovery after a supervisor crash; lampd reconnects,
marks those guests failed, and releases their reservations. Durable suspended
captures retain their existing recovery behavior. A lampd crash does not restart
the engine: incomplete creates are reaped without replaying user commands, and
retrying the original key returns the original operation envelope.

## Caller interface

```sh
bin/lamp sandbox create --template-id "$TEMPLATE_ID" \
  --idempotency-key create-demo-1 --wait-ms 1000 --json
bin/lamp operation get "$OPERATION_ID" --json
bin/lamp sandbox get --sandbox-id "$SANDBOX_ID" --json
bin/lamp exec create --sandbox-id "$SANDBOX_ID" \
  --idempotency-key exec-demo-1 --wait-ms 1000 --json -- /bin/echo hello
bin/lamp exec get "$EXEC_ID" --json
bin/lamp exec watch "$EXEC_ID" --limit 100 --wait-ms 1000 --json
bin/lamp sandbox fork --sandbox-id "$SANDBOX_ID" \
  --expected-revision "$REVISION" --idempotency-key fork-demo-1 \
  --durability local --wait-ms 1000 --json
bin/lamp sandbox delete "$SANDBOX_ID" \
  --expected-revision "$REVISION" --idempotency-key delete-demo-1 \
  --wait-ms 1000 --json
```

`--socket` or `LAMP_SOCKET` selects the same local API socket for every command.
Resource IDs can be positional immediately after the action, or supplied with
`--sandbox-id`, `--exec-id`, or `--operation-id`. Create accepts
`--resources-json '{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}'`
and `--durability volatile|local`. Exec accepts `--argv-json '["/bin/echo","hello"]'`
as an alternative to `-- argv...`.
Mutation keys are generated when omitted; supply an explicit key when a request
may need retrying. JSON API errors go to stderr and exit 1; usage errors exit 2.
`--json` emits the API document unchanged, including exact uint64 values.

Equivalent REST routes:

All mutations require exactly one nonempty `Idempotency-Key` header. The CLI
maps `--idempotency-key` there; `idempotency_key` is rejected in REST bodies.
Fork and delete also require a single uint64 `If-Match` header, mapped from
`--expected-revision`. Body `expected_revision` is rejected, including values
that disagree with the header. Keys/revisions are bound to the internal input
before idempotency hashing and source-revision admission checks.

- `POST /v1/sandboxes`: body `template_id`, optional `engine_id`, complete
  `resources`, `durability` (default `volatile`), and `wait_ms`.
- `POST /v1/execs`: body `sandbox_id`, nonempty `argv`, optional guest `cwd`,
  nonsecret `env`, and `wait_ms`.
- `POST /v1/sandboxes/{sandbox_id}:fork`: optional body `fork_mode=memory`,
  `durability=volatile|local`, and `wait_ms`; source identity is in the path and its
  required revision is in `If-Match`.
- `DELETE /v1/sandboxes/{sandbox_id}`: optional `wait_ms`; source identity is
  in the path, revision in `If-Match`. Stop then Release must finish before
  publishing `deleted`. Worker `NOT_FOUND` counts as already cleaned up. Failed
  and unavailable sandboxes can also be deleted.
- `GET /v1/execs/{exec_id}/output`: canonical `ExecWatchResult`; accepts `cursor`,
  `limit` (0–1000, default 100), and `wait_ms` (0–60000, default 0). Other routes
  reject query parameters.
- `GET /v1/operations/{operation_id}`, `GET /v1/sandboxes/{sandbox_id}`,
  `GET /v1/execs/{exec_id}` return canonical `{data,request_id}` documents.

For example:

```sh
curl --unix-socket "$HOME/.local/state/lamp-demo/api.sock" \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: create-demo-1' \
  -d '{"template_id":"tpl_demo"}' \
  http://lamp/v1/sandboxes
```

Resources default to 2 vCPU, 4096 MiB RAM, 20 GiB disk, 4096 pids. Sandboxes
publish `boundary=vm`, the exact admitted resources, and requested durability;
fork children inherit resources and receive the fork request’s durability.
Request bodies are limited to 64 KiB, keys to 256 bytes, waits to 60 s, and
retained operations to 1,000,000 (well above the roughly 20,000-operation gate).
`wait_ms` defaults to zero. Its expiration returns acceptance,
not cancellation. Caller disconnect does not discard an admitted operation.

Mutations return the generated canonical envelope with operation ID, typed
resource references, request ID, replay status and current Operation. HTTP 202
means only accepted/in progress; no completion or readiness is invented.
HTTP 200 plus `result` requires terminal success. Admission/worker failures use
the canonical ErrorEnvelope; accepted failures retain a readable Operation and
resource, including `cleanup_state=pending` when reconciliation is required.
Deletion publishes completion only after Stop/Release evidence or worker
`NOT_FOUND`; uncertain cleanup keeps a failed operation with pending cleanup.

## Worker integration seam for T2/T6

No shared schema or `.proto` was changed by T3. These are the exact command and
receipt semantics the client currently requires; they must be joined with T2's
receiver at T6, not treated as proof that the separately developed engine is
already integrated:

1. Create: `Allocate(resources, engine_id)` → receipt `allocated`;
   `Restore(capture_id=template_id)` → `restored`;
   `Start(argv=["/bin/true"], cwd="/")` → `command_started`.
2. Exec: `Start(argv,cwd,env)` → `command_started` completes the create operation.
   A separate observer polls `Inspect(command_operation_id=<Start operation ID>)`
   about every millisecond without holding the service mutex. A nonzero
   `Receipt.command.finished_at_unix_ns` proves completion: Exec becomes `exited`
   with exit code and signal. `detail.command_state=running` keeps polling;
   `unknown`, missing evidence, transport loss, or deadline becomes
   `outcome_unknown`. No exit status is fabricated.
   Completed stdout/stderr tails become canonical base64 Frames (one stable
   stream per channel, sequence 0, byte offset "0", location epoch 1). Pagination
   cursors identify the exec and next frame index. `next_cursor` is omitted only
   when the exec is terminal and the page drains available frames. Tails are
   bounded by the worker protocol (64 KiB per channel); these are retained tails,
   not a bulk output transport. Output is published only at command completion.
3. Fork: source `Capture(capture_id=<new opaque ID>, preserves_memory=true,
   consistency="crash" for volatile or "local" for local)` → `captured`;
   child Allocate → `allocated`;
   child Restore with that capture ID → `restored`; child command probe →
   `command_started`. The captured receipt must also mean the source is resumed.
   The receiver must enforce the coherent RAM/disk generation, private child
   writes, uniqueness/reseed/guard barriers and capture custody, not merely copy
   a path. Capture IDs and template IDs resolve inside the enrolled supervisor;
   no engine bytes or guest disk/RAM handles cross the Go boundary.

Every context has a fresh request ID, sandbox ID, generation 1, policy revision
1, installed grant reference, and absolute deadline (30 s default, at most 1 m).
Each effect uses a stable **step-specific** operation ID under the public
operation ID (`.allocate`, `.capture`, `.restore`, `.start`), so the worker's
operation deduplication cannot mistake different steps for retries.

`accepted`, `preparing`, `running`, or `waiting` receipts cause bounded read-only
Inspect calls for the exact required evidence. Other unexpected receipts,
response-context mismatches, frame failures after a write, or lost terminal
evidence become `outcome_unknown`. Effects are never automatically redelivered.
Inspect uses a fresh operation ID; sandbox mutation admission is serialized so
an Inspect result cannot be confused with another in-flight sandbox mutation.
Worker requests share one persistent connection with a serialized frame writer
and a demultiplexing reader. Replies must match the entire request Context and
may arrive out of order, so a slow capture cannot block another sandbox’s Inspect.
Transport loss fails all outstanding attempted requests as uncertain; effects
are never redelivered.

`command_started` must be emitted only after an independent real guest command
marker—not on engine allocation, VM resume, or guest-init acceptance. The
supervisor must withhold it until the requested identity/guard/setup barriers
pass. Only then does `lampd` record the UTC marker-receipt time in
`readiness.command_started_at` and publish `ready`. No agent-ready timestamp is
claimed because the demo admits no harness/sign-in setup.

The caller's create clock starts **before** sending the CLI/API request. The
journal commits, admission, Hello (if not warm), RPC, restore and guest command
probe all count. Never use only the daemon's marker-receipt timestamp as elapsed
create latency. No 500 ms or memory-fork performance result is claimed by T3.

## Failure and replay rules

The owner-private journal appends one framed write-set record per state change,
`J1 <CRC32C hex> <JSON>\n`, written in place over preallocated zero bytes and
made durable with `fdatasync`. Then the change's committed end is published in
`journal.commit`: one 512-byte sector `JC1 <log generation> <end> <sequence>
<CRC32C>`, rewritten in place and `fdatasync`ed. Only then is the change
acknowledged. The first marker (new state directory, or migration from a snapshot
alone) is written and synced under a temporary name, renamed into place and
the directory synced, so a killed start never leaves an empty or partial
marker; leftover temporaries are removed on open. This second sync costs about 0.6 ms per commit on lane LX
(p50 0.64 → 1.27 ms, 1,000 commits, ext4 on NVMe). Save work is independent of retained history;
idempotency lookups use an in-memory index rebuilt on open. Admission is durable
before effect dispatch; terminal evidence is durable before publication.
Every 16,384 records (and on open), compaction writes and fsyncs an atomic
`journal.json` snapshot, fsyncs the directory, then truncates/fsyncs `journal.log`.
Sequence numbers make a crash between snapshot rename and log truncation safe.
Open migrates existing JSON snapshots and replays the log up to the marker's
committed end. Every byte inside that end must be a valid record, and the last
sequence must match the marker; any damage there, a damaged marker, or records
without a marker fail closed. Open then leaves the log untouched and does not
compact, for the operator to inspect. Bytes past the committed end were never
acknowledged and are discarded. They include a record write that a SIGKILL
or OOM kill cut short between pages, so an ordinary process death never stops
a restart. Compaction gives each snapshot a new log generation; a marker
older than the snapshot means the log holds only history the snapshot already
contains. A write failure
closes admission and reads with
`DATA_UNAVAILABLE` rather than exposing an uncommitted success. This local
journal is deliberately not the later replicated/SQLite control core.

Idempotency is scoped by operation name plus local caller/key; typed input is
hashed after default-resource resolution, excluding `wait_ms`. Reuse returns the
same operation/resource without worker effects; changed inputs return
`IDEMPOTENCY_CONFLICT`. Fork checks the source revision before capture and blocks
new commands until it finishes. An uncertain fork invalidates parent readiness.

Restart preserves records but never replays an incomplete operation. Such an
operation becomes `outcome_unknown`; sandbox availability is cleared pending
real engine reconciliation (T10). Deleted sandboxes remain deleted. Completed
operation history and command
output are retained, not confused with a live-ready resource after restart.

## Evidence and its limits

`go test ./internal/control -count=1 -v` builds real CLI/daemon subprocesses,
checks their private sockets and framed RPC, and runs a **test-only Rust peer**.
The Rust peer executes only fixed host probe/marker commands in a temporary
directory; it implements no VM isolation or real RAM fork. A separate Go peer
injects delayed acceptance, worker errors, dropped replies, restarts and journal
failure. Tests cover out-of-order worker replies, completion/output pagination,
failed/unavailable deletion, unchanged `benchmarks.public.PublicLamp` CLI calls
against the Rust fixture, and 20,000 journal records across replay/compaction.
Negative tests check UID/PID, Hello, frame limits and response context.

To retain live Rust-path results and independently validate canonical output:

```sh
LAMP_CONTROL_EVIDENCE_DIR="$PWD/docs/development/evidence/t3" \
  go test ./internal/control -count=1 -v
.venv/bin/python internal/control/testdata/validate_evidence.py \
  docs/development/evidence/t3
```

See [T3-evidence.md](T3-evidence.md) for captured results. The real T2 worker,
signed-grant issuance, guest marker, engine/guard attachment and the <500 ms
create / <1000 ms fork claims remain T6 integration/gate evidence. The host
fixture is explicitly not a substitute for those tests.

T33 also removes permanent retained-capture charges for temporary sandbox forks.
The live child keeps its ordinary resource reservation; explicit snapshots and
shares keep their retained-capture charges. On startup, old fork charges are
subtracted once from the corresponding budget using the retained fork records.
`lamp quota` includes retained capture bytes, command slots and operation records
as well as live resources. Retained operation records still count toward the
configured control-record quota.

Real T33 results and remaining platform limits are recorded in
[evidence/t33/README.md](evidence/t33/README.md).
