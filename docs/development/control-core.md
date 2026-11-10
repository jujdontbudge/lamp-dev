# Control core

T10 extends the T3 journal, with one authenticated HTTP dispatcher. `lamp` and
`lamp-mcp` only translate the canonical contracts into HTTP; neither opens a
worker socket. `contracts/public/schema.json` remains the source of names,
fields, paths and scopes. `GET /v1/openapi.json` returns its generated OpenAPI.
`lamp docs read --topic requirements --json` ships the product text in the
binary; `architecture`, resource names and canonical operation names are also
accepted topics. `installed_operations` distinguishes executable core handlers
from contracts implemented by later tickets. Uninstalled operations fail
`NOT_FOUND` on every transport; no placeholder success or empty resource list.

## Owner enrollment and transport

The default listener remains a mode-0600 Unix socket inside a mode-0700 parent,
with OS peer-UID authentication. Without an identity file this is an explicit
single-local-owner deployment (`pr_local`). It does not trust HTTP identity
headers. All remote access must reach this listener through a trusted gateway
or SSH tunnel; this ticket does not provision a TCP listener or TLS endpoint.

For credentialed callers, create a **new private state directory** and an owner
enrollment file with `owner_id` and `bootstrap_sha256`. Generate a random
32-byte bearer, save it in a mode-0600 file, and put only its SHA-256 hex in the
enrollment file. Start `lampd --identity-file <enrollment.json>` along with its
existing enrolled worker flags. The bootstrap bearer belongs in `LAMP_TOKEN`
or `lamp --token-file <private-file>`, never in a URL, command argument or
metadata. The CLI uses `LAMP_API_URL`/`--api-url` for a gateway or loopback
SSH tunnel, and otherwise `LAMP_SOCKET`/`--socket`. Authenticated remote URLs
require HTTPS; redirects are refused. The journal cannot be reopened with its
credential requirement removed. The trusted operator can rotate the bootstrap
hash in the enrollment file; callers cannot change the fleet owner this way.

Legacy T3 retry keys are migrated to their existing owner namespace on open.
The migration preserves operations and unknown outcomes. Use the same owner ID
(`pr_local`) when enrolling an existing T3 state directory. Do not change owner
identity as an implicit upgrade or copy a journal over a newer journal.

## Roles and private content

The owner changes mode with `fleet update fl_local --mode team
--expected-revision <revision>` and creates principals using exact `issuer` +
`subject`, never an email merge. Memberships admit enrolled principals as
`admin`, `member` or `viewer`. The owner remains the sole owner; generic
membership edits cannot transfer ownership. `public_service` admits only
credentialed principals the owner/admin explicitly enrolls. Anonymous signup,
identity-provider challenges/invitations and distributed owner transfer need
T9/T16 integration; this core does not invent identity proofs or auto-enroll
untrusted public callers.

| Role | Fleet metadata | Principal/membership management | Own workloads | Own reads | Protected decisions |
| --- | --- | --- | --- | --- | --- |
| owner | read/change mode | yes | yes | yes | yes |
| admin | read | yes, cannot create/promote administrators | yes | yes | no |
| member | read | own records only | yes | yes | no |
| viewer | read | own records only | no mutations | yes | no |

All content stays own-only, including for owner/admin. Management authority
does not reveal another principal's sandbox, exec, output or operation.
Switching to `single_owner` immediately blocks other principals. No role grants
host OS privilege or changes the enrolled worker's grant.

## Keys

`key create --principal-id <id> --scope-json '<scope>' --expires-at <UTC>` returns
`result.key_id`, `result.secret` and `secret_available=true` once. Every replay,
Operation and persisted outcome contains `secret_available=false` and no
bearer. Key secrets contain 256 random bits and are SHA-256 hashed at rest.
A child key records its authenticated issuer as parent and inherits the
intersection of actions, sandbox/recipe/host/resource selectors, numeric limits
and expiry. Omission inherits; an explicit empty array grants nothing. Issuer
role rights are frozen into the child's action ceiling as well as checked
against its current role. No caller, including the fleet owner, can issue or rotate another principal's credential. Management rights never return an impersonation bearer. The trusted identity custodian may configure `subjects` entries (`issuer`, `subject`, `sha256`) in the private startup enrollment file. Each entry authenticates the exact active principal and checks current membership/mode; only that subject receives its independently random bearer. The subject then issues its own narrowed keys. Fleet APIs cannot install bearer hashes. T9 owns external sign-in proof and custody.
Rotation preserves/narrows the existing scope and expiry and revokes the old
key. Rotation by a different key is rejected unless its live ancestry preserves the old inherited parent; rotating an ancestor of the issuer is rejected. Descendant authentication checks every ancestor, expiry and current role.

The local worker host selector is `hs_local`. Prepared-template create cannot
meet a supplied recipe selector or preexisting sandbox/resource selector, and
is denied. Workload vCPU/RAM/disk limits are enforced at admission; concurrency
bounds live sandbox admissions and commands in accepted, running or uncertain
states for that principal. A command-start receipt does not release its charge;
observed completion or confirmed sandbox Stop/Release frees it. An Exec denied before dispatch is marked failed and releases its charge. Confirmed sandbox deletion frees the reservation of an unknown Exec while preserving its historical unknown outcome. Failed or uncertain cleanup remains charged until worker Stop/Release confirms release. A prevented create/fork can release its reservation because no allocation was dispatched. Deletion remains available for cleanup. Token, wall-time and retained
state budgets require their owning subsystems and are rejected here rather than
silently accepted. Fleet-wide accounting and the audit/revocation ledger remain
T23's responsibility.

## Operations, readiness and retries

Concurrent engine journal writes share ordered group commits: all record bytes are synced before the committed-end marker is synced. Save returns, worker dispatch and public readers remain behind the corresponding durability boundary. Immutable committed operation snapshots let each reply return its own result without waiting for unrelated output commits. Exec running is an internal dispatch fence; its accepted record is the durable no-replay boundary until command-start completion is committed. A flush failure breaks publication and dispatch.

Acceptance durably records the Operation and partial resource before dispatch.
Keys are scoped by principal + operation + key, with canonical input hashing;
`wait_ms` is excluded. Equal keys return the original Operation even after
resource deletion. Results and tombstones are retained; quota exhaustion rejects
new admissions rather than discarding old retry identities. A restart never
replays uncertain worker or adapter effects. It marks them `outcome_unknown`
with pending cleanup. Failed create/fork/delete resources remain inspectable;
`cleanup_state=pending` requires a revision-checked sandbox deletion/reconciliation,
not an invented cleanup success.

`operation list`, `get`, `wait` and `cancel` use the same scopes on all surfaces.
Cancel before dispatch publishes `cancel_effect=prevented` and leaves no worker
reservation. Once work is dispatched, cancellation returns `OUTCOME_UNKNOWN`
and the original Operation remains the authority for committed effects. Cancel
after terminal work does not undo it. `If-Match` admission and source revision
advancement occur in one journal critical section; a denied edit cannot change
a revision. No caller disconnect cancels accepted work.

Every sandbox exposes the ten canonical readiness checks. The current prepared
worker path requests template, engine, identity and command checks; unrequested
network/settings/kits/auth/connections/agent setup is explicitly skipped.
Unsupported requested setup fields are rejected. Ready requires allocation,
restore and an actual `/bin/true` command-start receipt plus observed successful probe completion. A guard wrapper that starts but exits before a successful probe cannot advertise ready. After restart the core
only restores readiness after read-only Inspect proves the original command,
sandbox generation and live worker state. Otherwise it stays `unavailable`.
An actual worker rejection identifies the failed engine/template/command phase.

## Events

`event list --json` returns durable metadata events and an owner/key-bound
HMAC-signed cursor. `--follow --json` negotiates SSE on the same `/v1/events`
route and emits one JSON event per line. MCP uses `event_list` with `wait_ms`
limited to 25 seconds. Per-resource revisions are ordered; replay identities
survive restart. Metadata contains state only, no command arguments, key
bearers, approval arguments or outputs. Output remains the bounded exec stream.

Retention is seven days. Expired cursors return `CURSOR_EXPIRED` with
resource-list resync instructions; quota exhaustion never silently shortens
retention or skips a cursor gap. SSE does not retain a queue
or hold the journal mutex during writes, limits each page below 96 KiB and
disconnects blocked writes after five seconds. HTTP wait/SSE authorization is
checked at least once per second, and journal changes wake readers immediately.
Durable fleet event replication and retained-byte quota sizing remain later work.

## Protected adapters and approvals

`Config.ProtectedActions` registers trusted enforcing adapters by resource ID.
The adapter independently derives `BoundAction` from both the approval request
and eventual invocation. It must validate the immutable adapter digest, exact
operation/arguments/destination, principal/task/sandbox generation and resource
revision; a proposed display description or digest grants nothing. No adapter
is installed from caller JSON. With no matching adapter the action fails closed.
This seam is for the broker/access tickets, not a generic host command runner.

`approval create --input <file>` stores the validated immutable action plan
(maximum 64 KiB), server-computed SHA-256 binding and explicit expiry of at most
60 seconds. Owner-authenticated get/list/approve/deny/revoke inspect/decide this
plan. Sandbox, recipe, host and resource selectors apply before inspection or decision; denied decisions leave state and revision unchanged. Approval is consumed atomically with protected operation admission before
one adapter dispatch. The enforcing adapter revalidates immediately before the
effect. Changed targets/revisions/generations, expired/denied/revoked approvals
and concurrent second consumption produce no effect. Ambiguous adapter errors
stay unknown and are never retried by the core. There is no public consume API.
Broker secret normalization/HMACs and vendor-specific validation belong in the
registered enforcing adapter; the core cannot certify an unimplemented adapter.

## CLI and MCP

Every canonical CLI operation accepts `--input <JSON-file>` and schema-derived
flags (`--scope-json`, `--resource-ref-json`, etc.). Existing template/resource
and `-- argv...` shortcuts remain compatible. `lamp --help --json`, command
`--help --json`, and `version --json` are local diagnostics. JSON success goes
to stdout; JSON errors go to stderr. Exit codes: 0 success, 1 other API failure,
2 usage, 3 authentication, 4 scope denial, 5 conflict/expired cursor, 6 transport.
Mutation transport errors preserve the retry key in the unknown-outcome message;
retry with that exact key, never generate replacement work after a lost reply.

Run `lamp-mcp` with `LAMP_SOCKET`/`LAMP_API_URL` and `LAMP_TOKEN`. It implements
newline-delimited stdio MCP initialize, ping, tools/list and tools/call. Installed
core tools carry expanded canonical input schemas. At most 32 calls run at once;
a bounded event wait does not block another tool. MCP preserves uint64 JSON
integers, forwards errors and never mints broader transport authority.

## E03 measurement

The independent runner is `python3 -m benchmarks.run --suite E03 --api-url
<origin> --callers <manifest.json> --metadata <observed.json> --output <new-dir>
--integrated-e01 <summary.json> --caller-processes`. Prepare owner-private
sandboxes before timing and use 32 distinct credentials across at least two
principals. On LX, `infra/linux/run-e03.py` prepares two sandboxes, one per owner,
with 16 callers sharing each. It checks `free -m` before each allocation, refuses
less than 12 GiB available and never exceeds two live sandboxes. The manifest
is exactly 32 objects:
`{"principal_id":"pr_…","sandbox_id":"sb_…","token_env":"LAMP_CALLER_0"}`.
Environment variables contain their distinct bearers; evidence contains no
bearers. The runner never prepares templates or starts/stops owner services.

There are 200 synchronized rounds per caller (6,400 measured commands), at 32
concurrent callers. The endpoint includes authentication, durable admission and
the actual worker command-start receipt. It includes some command-start work,
so passing this upper bound also bounds the control allocation. After timing,
the observer checks actual command output/owner, a stable-key replay and foreign
resource denial. A separate bounded-capacity phase holds a live command under
a concurrency-one key while 32 admissions contend. Independently chosen guest
marker files/PIDs and kernel allocation snapshots must agree with exactly one
admitted effect; public acceptance or independent overspend fails the run.
This phase allocates no additional VM. Prepared resources are released before
evidence publication. All failures and individual latencies stay in the hash-chained
run. The fixed p99 allocation is **≤35 ms**, with zero correctness failures.
A matching-build/policy integrated E01 pass is also required; a failed E01 stays
failed and missing/mismatched evidence remains incomplete. This endpoint may
fail because of command/transport overhead even when the control-only spans
would pass; it never subtracts guessed engine time to manufacture a pass.
`--smoke` uses two rounds and can never qualify. Native LX and actual §22
metadata are still required by the existing evidence checker.

`LAMP_E03_DIAGNOSTIC_OUTPUT=<new-directory> go test ./internal/control -run
TestControlBoundaryDiagnostic -v` runs the independent observer against the
Rust **test protocol peer**, real process commands and real control journal.
That fixture cannot qualify E03 or E01, even on LX. It is useful for finding
control serialization and authority faults without stopping owner services.

For hard timing on LX, build and install workload/templates before entering
`infra/linux/timing-run.sh -- python3 infra/linux/run-e03.py ...`. Check for
other `timing-run.sh`/`benchmarks.run` processes and acquire the shared timing
lock first. The wrapper restores exactly the owner services it found running.
`--suite all` requires E03 preparation inputs or reports that suite incomplete.
The Linux process runtime avoids Python thread/GIL scheduling in the independent
caller side; it does not subtract time, reduce counts or alter the threshold.

Linux journal capacity uses `fallocate` to reserve physical blocks without
writing tens of MiB of zeros during a commit. Record `fdatasync` still commits
data and required extent metadata before the separate committed-end barrier.
Unsupported filesystems retain zero-fill reservation; the protocol and crash
recovery boundaries do not change.
