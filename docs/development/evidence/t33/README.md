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
`TestControlCoreInterfacesAndAuthority` smoke passed. No new tests were added.
The workspace `cargo fmt --all -- --check` still reports pre-existing wrapping
in `crates/lamp-guard/src/audit.rs:439`; that unrelated file was not changed.

Limits: no Air or Linux arm64 guest execution was performed. The engine's
existing restart contract kills volatile guests. T33 reconnects without pinning
the old supervisor PID and reconciles lost guests rather than leaving ghost
reservations. It does **not** preserve the same live guest across a supervisor
restart, so that part of F44's retest remains unmet. Retained operation records
still consume the configured control-record quota; live capacity returning to
zero does not mean deleting durable history or resetting budget usage.
