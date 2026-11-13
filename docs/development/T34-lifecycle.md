# Sandbox sleep, snapshots and recovery

`lamp sandbox create --help` prints the lifecycle defaults: idle suspension
starts after 300 seconds of observed inactivity; suspended state is kept for
604800 seconds. Unknown guest accounting counts as busy. Running task processes,
connections and active execs prevent idle suspension. Countdown timestamps and
policy survive lampd restarts. Exec, session input and SSH/session attach resume the same sandbox through
the Wake admission hook. T39 preview ingress runs in a separate worker transport; automatic preview wake
is not wired to this control-plane hook. A preview request must be authorized
before any future bridge invokes Wake.

Set or extend a sandbox's policy with `--lifecycle-json` on create or update:

```
lamp sandbox update --sandbox-id sb_... --expected-revision 3 \
  --idempotency-key policy-1 \
  --lifecycle-json '{"idle_action":"suspend","idle_grace_seconds":60,"suspended_retention_seconds":3600,"expires_at":"2026-11-13T00:00:00Z"}'
```

`idle_action` accepts `suspend`, `stop`, `delete`, or `keep`. Stop uses the same
saved-state mechanism as suspend, releasing the running VM while retaining its
state for resume. `expires_at` is an absolute maximum age and deletes the
sandbox even if busy. Deadlines must be future times within one year. Task and
budget authority still bounds execution; extending a lifecycle never extends a
Task's authority. Get/list show effective lifecycle, `idle_countdown`,
`suspend_eligibility`, and `saved_state_until` while suspended.

A budget can be supplied as `--budget-id bg_...` at sandbox creation, or attached
to a Task used with `--task-id`. Shared budgets retain their earliest deadline.
`watchdog.warn_after_ms` reports wall-time consumption in the Task's
`lamp.budget_warning` label and its state-change event; the hard budget deadline
stops the sandbox. Repeated-action watchdogs and interactive ask remain unsupported.

Create a durable memory snapshot with `lamp snapshot create --sandbox-id sb_...`.
Use `lamp snapshot list`, then `lamp sandbox create --snapshot-id ss_...` to
restore its processes and memory at the captured resource limits. Snapshots
outlive their source; remove one explicitly with `lamp snapshot delete` using
its current revision and an idempotency key. Memory snapshots default to local
durability. Private captures keep their custody restrictions.

For resume, install the ordinary daemon control grant with `lamp-supervisor
grant-install --caller-uid UID`: it authorizes successive lifecycle generations.
Explicit `--generation N` pins a grant unless `--max-generation` is also supplied.
Existing pinned grants need replacement by the host administrator; lampd cannot
widen a root-installed grant. A scope refusal leaves saved state suspended and
does not advance its generation; deletion remains possible at its existing
generation. Other restore failures preserve the capture for another attempt.

## Encrypted backup

Suspend the sandbox, then use `lamp backup create --resource-kind sandbox
--resource-id sb_...`. The host-enrolled `lampd --state-admin '[...]'` command
must reach the supervisor's root-only state administration. It exports signed
memory, VM state and disk into an owner-private directory, then encrypts the
complete bundle in the existing `.lbk` archive format. Disk extraction preserves
zero runs as sparse holes; the engine verifies its signed digest before import. Volume backups remain
separate resources and contain their last committed files.

Configure `lampd --recovery-public-key /private/recovery.pub` before creating
backups. Keep the offline recovery private key outside Lamp. Copy the `.lbk`
archives from the state directory's `backups/` directory to offline storage.
On a fresh daemon prefix start with `--recovery-key-file /private/recovery.key
--recovery-import-dir /private/archives`, then `lamp backup list` and
`lamp backup restore --backup-id bk_... --expected-revision 1` with an
idempotency key. The operation lists the recovered resource. A recovered
sandbox is suspended under a new ID and fresh bounded Task authority; resume it
normally. Restore does not revive old exposures, grants or expired deadlines.

The replacement engine must have the compatible template and an enrolled
producer signing key for the signed capture. Encrypted backups do not contain
host enrollment, signing keys, templates, connection credentials, live volume
attachments, or the whole daemon journal. A fresh prefix on the same enrolled
engine needs no extra engine migration. Cross-host recovery retains the
engine's signature and compatibility checks and also needs its retained custody
lineage. This change demonstrates a fresh daemon prefix on the same enrolled
engine; migration of the engine's custody database is outside this archive.

The existing archive implementation buffers compressed objects and encrypted
archives in daemon memory. Large incompressible sandbox backups therefore need
corresponding host memory; this is not a streaming backup implementation.
