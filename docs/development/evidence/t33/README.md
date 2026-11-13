# T33 live evidence — 2026-11-12

The Ubuntu demonstration used the real `lamp` CLI, `lampd`, supervisor,
Firecracker, guard and a prepared public Ubuntu seed. It used a disposable
28 GiB loop-backed btrfs store and 512 MiB guests, never Docker or owner data.
Before each create/fork the driver checked fewer than two shared Firecracker
processes and at least 12 GiB available memory. All guest/storage cleanup is
limited to this demonstration's own paths.

`ubuntu.json` contains the incremental observations. The run was interrupted by
the Mac's network outage after the observation at 40 cycles; the recovered
journal showed 41 completed deletes and an interrupted create. That create was
recovered as failed, with no resource charge, and was deletable. Nine further
successful cycles brought the completed total to 50. This is a resumed run,
not a claim of 50 uninterrupted cycles. Another attempted create failed its
readiness command with `AUDIT_UNAVAILABLE`; its resources were reaped and the
failed record was subsequently deleted. These failures are not relabeled passes.

Observed through the public CLI:

- Create/delete usage returned to zero after 10, 20, 30, 40 and 50 completions.
- Unknown template: `NOT_FOUND`; display device: `DEVICE_UNAVAILABLE`;
  5 GiB disk: `INVALID_ARGUMENT`; 32768 MiB guest on a 31140 MiB host:
  `NO_CAPACITY`. No new sandbox records for these synchronous refusals.
- `exec create --env-json '{"EMPTY":""}'` ran
  `sh -c 'printf "[%s]\\n" "$EMPTY"; uname -s'` and returned `[]` and `Linux`;
  the sandbox stayed ready.
- A direct real-supervisor channel probe sent an Inspect with unknown
  Protobuf field 100, received `INVALID_ARGUMENT`, then received `serving`
  for a valid Inspect on the same connection. It used no sandbox or grant.

The effective CLI operations were `sandbox create --template-id <prepared-id>
--resources-json '{"vcpus":2,"memory_mib":512,"disk_gib":20,"pids_max":4096}'`,
`sandbox delete --expected-revision <current> --wait-ms 60000`, `quota`,
`sandbox list`, and `sandbox list --include-deleted true`. The daemon used the
documented guard-audit-release command pointing at this disposable audit root.
No grant, private release key or authentication material is included here.

On the M5, a signed disposable process-mode installation was created with
`lamp-installer`, its daemon was SIGKILLed, and status returned `healthy:false`,
`supervision:"unsupervised; run lamp-installer restart if the daemon exits"`,
and the concrete restart command. `lamp-installer restart --prefix <disposable>`
returned exit 0 and `saved_items_preserved:0`; status then returned healthy true.
The installation was uninstalled. This did not create LaunchAgents, prompt for
a password or operate any visible app. Automatic service-manager restart is
configured by the existing launchd/systemd service definitions; it was not
exercised on the owner's service manager in this demonstration.

Local checks: `make build`, selected `go vet`, contracts generation validation,
clippy for the changed Rust packages, and the existing
`TestControlCoreInterfacesAndAuthority` smoke passed. One regression test from the Sol review was added for its proven restart bug.
The workspace `cargo fmt --all -- --check` still reports pre-existing wrapping
in `crates/lamp-guard/src/audit.rs:439`; that unrelated file was not changed.

Limits: no Air or Linux arm64 guest execution was performed. The engine's
existing restart contract kills volatile guests. T33 reconnects without pinning
the old supervisor PID and reconciles lost guests rather than leaving ghost
reservations. It does **not** preserve the same live guest across a supervisor
restart, this is the clean, deletable loss behavior explicitly accepted by the coordinator on 2026-11-12. Retained operation records
still consume the configured control-record quota; live capacity returning to
zero does not mean deleting durable history or resetting budget usage.


## Recovery demonstration completed

The same `lampd` process reached the restarted supervisor (PID 1148627 became
1150478). Before restart `sb_3111a108cee7ce3fe89bbeb742d78916` was ready and
executed `[]` / `Linux`. After restart it was `failed`, with all live and audit
reservations zero. Deleting it succeeded and returned `deleted`. A fresh guest
then executed the same command successfully. This demonstrates the accepted
clean-loss path, not preservation of volatile guest memory.

For an offline delete, the public operation was accepted with sandbox state
`deleting`, then `lampd` was SIGKILLed before the supervisor returned. Restart
recovered the stale socket without manual removal. The original delete operation
completed successfully, with the same ID and state `deleted`.

A create accepted with `wait_ms=0` was interrupted by SIGKILL. Restart recovered
the stale socket, the same create key returned `replayed:true` and the original
operation ID, and the sandbox became `failed` and was successfully deleted.
The final list was empty; CPU, memory, disk, sandbox, command, retained-capture
and audit usage were all zero. The 117 retained operation records were reported
separately, not represented as live capacity.

Sol's S1 found a separate first-connection bug when **both** processes restart.
Observing connection epoch zero now includes the first new connection. The
reviewer's API regression reproduced `unavailable` with one charged sandbox
before the fix, and passed after the fix. Its quota comparison was adjusted to
use the repository decoder's numeric representation.

The real VM demonstration then killed `lampd`, restarted the supervisor (PID
1168929 became 1169438), and restarted `lampd` without removing its stale socket.
The previously executing sandbox became `failed` with zero CPU/memory/disk
charges; a pending 24 MiB audit reservation was released by cleanup. Delete
succeeded, a fresh guest executed `[]` / `Linux`, and final usage returned to
zero with no visible sandboxes (125 retained operation records reported).

An attempted fork demonstration initially stopped before fork because the
shared two-VM limit remained occupied; it released its own guest. A subsequent
parent create failed readiness with `AUDIT_UNAVAILABLE` and `cleanup_state:
complete`; the next recovery run deleted that failed record. These attempts
are retained as failures rather than counted as successful fork evidence.

The final memory fork succeeded on the disposable default-size 28 GiB store:
both parent and child reported `ready`, and the child executed `[]` / `Linux`.
The store's `stat -f %a` free-block observation showed zero additional allocated
bytes at the observation point (btrfs may defer allocation accounting). This is
not a total-write measurement. Both deletes succeeded. Quota then reported zero
live resources, audit reservations and retained capture bytes, default lists
were empty, and `--include-deleted true` showed 62 retained deleted records.
The 130 operation records remained visible in quota. This specifically proves
that the temporary fork no longer creates a permanent retained-capture charge.
