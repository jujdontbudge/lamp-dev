# Task budgets, accountability and admission

`lampd` owns task budgets and admission at each resource's immutable home. A
child task inherits its parent's budget and cannot extend its deadline or select
a different budget. A budget's clock starts with its first task and is shared by
all tasks using it. Sandboxes and forks carry the task ID. Existing sandbox
callers receive an automatic one-hour task, narrowed by any applicable scope
wall-time limit. Restart preserves deadlines, reservations and charges.
Continuous host ticks, including sleep, prevent a wall-clock rollback during a
daemon lifetime from extending deadlines.

The watchdog force-stops and releases expired, stopped or revoked work. It keeps
uncertain resources charged until the supervisor independently confirms release.
Each cleanup runs independently so an unavailable worker request does not put
other sandboxes behind it. The watchdog runs in `lampd`; the supervisor must
remain reachable for a confirmed stop. A failed stop remains `unavailable` and
charged, and is retried. This is an operational limitation, not a claim that an
unreachable host has stopped.

## Public API and CLI

The installed operations are `budget create/get/list/update/delete`,
`task create/get/list/stop/delete`, `audit-record get/list/export` and
`telemetry get/update/purge`. Their input schemas and mutation headers are the
existing public contracts. For example:

```sh
lamp budget create --input budget.json --json
# budget.json: {"wall_time_ms":600000,"token_limit":10000,"token_mode":"exact",
#               "idempotency_key":"my-budget"}
lamp task create --input task.json --json
# task.json: {"budget_id":"bg_...","idempotency_key":"my-task"}
lamp sandbox create --input sandbox.json --json
# sandbox.json includes template_id, task_id and idempotency_key.
```

Task and budget IDs carry their fleet home. A descendant and its budget remain
on that home; a strict pin to another host is refused. Lists describe the queried
home. In-use budgets cannot be edited to replenish a running task's authority.
Deleting a budget stops attached work. This implementation supports watchdog
`block_after_ms` with stop behavior; warning, repeated-action detection and
ask-first watchdog behavior are rejected rather than silently accepted.

## Broker accounting

Set `accountability_socket` in the private broker configuration to the local
`lampd` owner socket. The bridge is host-only: the authenticated owner UID may
call it, and fleet/public TLS ingress does not install its routes. A task never
chooses its budget, accounting socket or settlement count. Grants retain their
existing sandbox/generation/peer binding.

Before dispatch, the broker commits a token reservation and a request audit
record. A model connection can pin `tokenizer_command`, an absolute host
executable receiving the canonical request JSON on stdin and returning
`{"prompt_tokens":123}`. It must implement that vendor's complete prompt
framing/tokenization. Exact mode refuses a model route without that adapter.
The reservation is the prompt count plus the requested maximum output; usage
from the bounded provider response settles it. Missing/uncertain usage consumes
the entire reservation. Duplicate settlement cannot refund it twice. A provider
report exceeding its reservation disables the budget and stops attached work.
Without a tokenizer, best-effort mode reserves a conservative byte-based
estimate and labels that budget accordingly; this is not an exact vendor-token
claim. Opaque commands have opaque audit visibility and a hard time stop;
mediated token counts must not be interpreted as total tokens spent through
unobserved subscriptions or external routes.

A bounded broker queue rejects excess requests. Grant/access revocation cancels
in-flight work; accounting checks also cancel mediated work when the sandbox's
underlying task or authority is stopped. Already completed upstream effects are
not undone.

## Quotas and retained resources

`lampd --quota-file <private-json>` configures `sandboxes`, `memory_mib`,
`disk_gib`, `execs`, `records` and `audit_bytes`. Defaults are 16 sandboxes,
65,536 MiB RAM, 512 GiB disk, 64 concurrent commands, 8,192 retained records and
512 MiB of audit reservations. These are configurable admission policies, not
performance objectives. Owner resource limits and the fleet's existing host
capacity admission both apply. Budget `resource_limits` further bounds aggregate
CPU, RAM, disk, concurrency, retained capture bytes and control records.

Active, failed, uncertain and retained objects remain charged. A fork, snapshot, share rebuild or suspension reserves
its retained memory/disk capture before dispatch; uncertain captures are never
refunded speculatively. Confirmed sandbox deletion frees active RAM/disk, while
capture and audit retention charges remain. A sandbox reserves 24 MiB for its
8 MiB network spool and at most 64 process spool segments of 256 KiB each. The
65th collector is refused before its task starts. Each sandbox's mediated audit
records and reservations have independent bounded limits, so exhausting one
does not consume another admitted sandbox's allowance. Audit exports are also
charged and bounded. Stop/delete/revoke operations have reserved control-record
headroom. There is no payment processor, purchase flow, price calculation or
provider-account configuration; typed resource and token ledger entries are
available for later plan/quota/billing policy.

## Audit and revocation

`--audit-reader /absolute/executable` selects a trusted host reader. It receives
only a validated sandbox ID and generation, and returns the guard's existing
newline-delimited JSON records. It must read that sandbox's host-owned spools;
no guest-supplied checksums or fabricated process observations are accepted.
The reader runs outside the control lock with a bounded output and deadline.
Exports validate attribution, combine process/connection records with control
and mediated request records, and write a private, fsynced JSON artifact. Export
completion names an audit record whose `details` contain the artifact path,
SHA-256 and record count. For example:

```sh
lamp audit-record export --input export.json --json
# {"expected_revision":1,"filter":{"sandbox_id":"sb_..."},
#  "idempotency_key":"export-1"}
lamp audit-record get au_... --json
```

Control audit records form a SHA-256 predecessor chain. This is not a signed
ledger or a claim of tamper-proof archival storage. Guard records retain their
own per-spool sequences and attribution. No request bodies, provider credentials,
settings bytes or command environments are added to mediated audit records.

Key, principal and membership mutation receipts expose `revocation_status` when
read through `operation get`. A host acknowledges only after observing current
committed authority and confirming that revoked work has been released.
Disconnected hosts remain pending, with their last authority expiry and a
residual offline-authority capability. Reconnecting hosts project revocation,
stop derived work, and acknowledge. A quorum commit alone is never presented as
proof that an offline machine stopped.

## Optional usage data

Security audit and cap/admission bookkeeping are required local operational
records. Optional usage telemetry is separate, disabled by default, and contains
only numeric timestamps, counts and durations. It sends nothing to an external
service. `telemetry update` enables count/timing collection independently with
fixed 90-day retention. Hourly retention cleanup compacts away expired journal
history; disabling or purging telemetry immediately compacts away those samples.
The owner-only bridge's `usage` diagnostic returns numeric samples for the
selected sandbox's owner. The only added unit test checks the 90-day boundary,
which cannot be demonstrated by waiting during an interactive journey.
