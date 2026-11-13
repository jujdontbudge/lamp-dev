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
No new tests or benchmark runs were added.

Enrollment provisions a new fleet with an immutable initial manifest. It does
not migrate existing standalone state or implement online membership changes.
Generated host certificates expire after one year; the guide calls out renewal.
Unavailable sandbox disks retain their home and custody. Nothing promotes or
copies private VM state to another machine automatically.
