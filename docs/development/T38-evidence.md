# T38: hosts, fleets and remote access

The release now carries a `lamp-fleet init` / `join` enrollment path, the
Cloudflare wrapper, the plugin launcher for engine builds, and a separate relay
server archive for Linux amd64/arm64 and macOS arm64. The shipped
[setup guide](../release/fleets-and-access.md) covers initial hosts, placement,
rolling updates and each access method. CLI, MCP and HTTP share host/engine
reads, update version resolution and relay status.

## Runs from the real product

[fleet.jsonl](evidence/t38/fleet.jsonl) records two independent installer-managed
macOS control daemons on loopback, with private generated host bundles and a
fresh release signing key. Both reported version 1.0.0. `update create --version
1.1.0` resolved the signed release mirror and updated hs_one before hs_two. Both
reported 1.1.0. Stopping hs_two made `fleet get` report it unavailable. Starting
it again with the retained installer arguments recovered the durable fleet state.
A signed 1.2.0 whose daemon executable exits immediately failed its health check,
restored 1.1.0 on hs_one and stopped the rollout; hs_two stayed on 1.1.0.

The first attempt to restart an already stopped daemon with `lamp-installer
restart` failed because that existing command inventories the daemon before
starting it. The run resumed by launching its existing installer arguments,
without editing state. This command's recovery behavior is outside T38.

[relay.jsonl](evidence/t38/relay.jsonl) records the real relay server and outbound
client, a disposable local RS256 issuer, one-time host enrollment, a real
lamp-access SSH edge, strict independent SSH host-CA checking and stock OpenSSH
`sessions` reaching the daemon through the relay. `relay get` returned connected;
MCP advertised and called `relay_get` / `relay_list`. All disposable relay, edge
and daemon processes were stopped. No production account, owner browser/profile,
real vault data or paid service was used. This run proves the relay protocol and
SSH encryption on numeric loopback, not production DNS/TLS/account enrollment or
a disposable Tailscale tailnet. Those live service enrollments were not run.

## Checks and limits

`go build ./...`, targeted `go vet` for the changed Go packages, public codegen
and SDK generator checks passed. `rustfmt --check` passed for the generated Rust
contract file. The workspace-wide `cargo fmt --all -- --check` reports existing
formatting in `crates/lamp-engine/src/engine.rs` and
`crates/lamp-guard/src/audit.rs`; those unrelated files were left alone.
Sol reproduced an outage-reporting lock deadlock with one focused regression
test. It failed before the fix and passed afterward; that single reviewer test
is retained. No other tests or benchmark runs were added.

Enrollment provisions a new fleet with an immutable initial manifest. It does
not migrate existing standalone state or implement online membership changes.
Generated host certificates expire after one year; the guide calls out renewal.
Unavailable sandbox disks retain their home and custody. Nothing promotes or
copies private VM state to another machine automatically.

## Packaging and local build

`make build` passed, including the workspace Rust build and SDK generation check.
[package.json](evidence/t38/package.json) records an actual signed macOS control
payload archive containing the Cloudflare wrapper, enrollment helper, relay
client, fleet HTTPS ingress, compiled plugin launcher and the release guide.
The archive also contains the account/issuer/DNS helpers used by the access guide.
[relay-packages.json](evidence/t38/relay-packages.json) records actual relay-server
cross-builds and archive inspection for Linux amd64, Linux arm64 and macOS arm64.
This packaging check does not claim a newly published or notarized public release.

[fleet-cleanup.json](evidence/t38/fleet-cleanup.json) records the four disposable
installer daemon/updater processes stopped after the successful update and
rollback run. [compute-capacity-denied.jsonl](evidence/t38/compute-capacity-denied.jsonl)
preserves the compute attempt that stopped at the owner's 12 GiB available-memory
limit (11,118 MiB available); no sandbox was created in that attempt.

## Compute attempt after the network recovered

[compute-guest-protocol-failure.jsonl](evidence/t38/compute-guest-protocol-failure.jsonl)
records two compute daemons on the Ubuntu laptop, with the existing enrolled
Firecracker supervisor and a public Debian 13 sample template. Both hosts and
their engines appeared through the real CLI. The owner capacity guard passed
(one existing Firecracker process and 14,930 MiB available). A pinned create
reached hs_two and created its operation there, but guest readiness failed:
the existing template's helper rejected the `audit` field in the supervisor's
command protocol. No successful running sandbox, automatic survivor placement
or real stranded sandbox is claimed from this run. This old template/guest
protocol mismatch was left unchanged; it is outside the fleet/relay change.
The run stopped its two daemons and revoked its temporary grant, but its failed
create path initially missed the demo VM during cleanup. The exact demo sandbox
was subsequently released through the public supervisor CLI with a temporary
release-only grant, which was then revoked. A process check confirmed no
Firecracker process remained for that demo ID. The evidence preserves the
script’s original incorrect cleanup report and the explicit correction. The outage-reporting regression is covered by
Sol's focused retained-observation test, not relabeled as a VM success.

Sol approved the current behavior in two rounds. S1 was fixed and its single
regression test was retained. See the [review ledger](evidence/t38/sol-loop.md).

## Cross-machine compute follow-up

[compute-cross-machine.jsonl](evidence/t38/compute-cross-machine.jsonl) records a
successful run across two physical machines: hs_one is this M5 Mac's control
ingress; hs_two is the Ubuntu laptop's compute host. The CLI discovery document
contained only the Mac ingress. Fleet peer traffic used mutual TLS over disposable
SSH port forwards because direct private-network ports timed out. This is real
cross-machine routing, not two daemons sharing a local worker.

The prior `audit` rejection came from the old November 10 template's guest-helper
JSON decoder. The current `crates/lamp-init/src/lib.rs` already defines
`Exec.audit`, and `lamp-guest-helper` runs audited tasks under `lamp-guard`.
No guest, daemon or exec product patch, and no dependency on T33/T35/T36, was
needed. A disposable clone of the verified public Debian 13 seed received this
branch's newly built `lamp-init`, `lamp-guest-helper` and `lamp-guard`, with updated
seed digests; a dedicated supervisor built template tp_a6a355c1659a92200433d2edbc.
Neither shared templates nor other tickets' services were modified. The release
guide now explains immutable template compatibility and short supervisor paths.

With zero existing VMs and 14,861 MiB available at the create guard, the Mac CLI
created sb_afd37d66f84cdb7fbc24b723c457a7f2_h_hs_two, observed `host_id: hs_two`,
`boundary: vm` and `state: ready`, then started Python inside it. The exec exited
0; `exec watch` through the Mac returned `t38-second-host-ok Linux x86_64`.
`fleet get` reported healthy coordination with no unavailable hosts. The CLI
deleted the sandbox. Both fleet daemons and the SSH tunnel stopped afterward.
The [dedicated supervisor cleanup](evidence/t38/compute-worker-cleanup.jsonl)
confirms its temporary grant was revoked and no demo Firecracker process remained. This follow-up proves
live compute on the second physical host; it does not claim survivor placement,
live VM migration, or production remote service enrollment.

[compute-followup-attempts.jsonl](evidence/t38/compute-followup-attempts.jsonl)
preserves setup failures and demo-script mistakes before that successful run:
a worker identity mismatch, a Unix-socket path too long for the disposable store,
a shared-capacity refusal, and assertions that incorrectly expected `running`
instead of sandbox `ready` or exec completion immediately after start. The old
protocol failure above remains historical; it has not been relabeled successful.
