# T42 dashboard and sandbox observations

Implemented from the external T42 ticket (F49, P21, P22, Q10).
No new test files or benchmarks were added.

## Fresh signed install on the M5

Built `lamp`, `lampd`, and `lamp-installer` with `go build`, made a disposable
signed 0.1.42 release with `lamp-installer build` and `index`, and installed it
with `--service process --agent=false` under `/tmp/t42-fresh/install`.
No owner service or visible browser was used.

Observed installer output (no credentials):

```json
{"dashboard_sign_in":"file:///tmp/t42-fresh/install/run/dashboard.html","dashboard_url":"http://127.0.0.1:50005/dashboard","event":"installed","service":"process","version":"0.1.42"}
```

`lamp dashboard --socket /tmp/t42-fresh/install/run/api.sock --json` returned
the private file URL. An HTTP client opened the form in that file, submitted
its one-use code by POST, and kept the returned browser cookie:

```text
dashboard page: 200
unsigned API: 401
signed-in API: 0 sandboxes, no pasted token
one-time code replay: 401
private sign-in file mode: 0o600
sign out: 204
signed-out API: 401
```

The installation was then uninstalled with its data deleted.
This demonstrates the real install and HTTP sign-in flow; it is not a browser
screenshot or a VM execution demonstration.

## macOS kernel collector

Invoked the actual `mac_metrics::collect` implementation on the M5 with a
disposable command-line program. It returned all seven cumulative/current
counters (CPU microseconds, memory used/total, disk used/total, network RX/TX).
This checks the native macOS collection code, not a macOS VM lifecycle.

## Local checks

`make build` passed (C launcher, Go workspace, generated SDK check, locked Rust
workspace build). Scoped `go vet`, Rust clippy with `-D warnings`, JavaScript
syntax, `tools/check_contracts.py`, and generated-code consistency passed.

## Live Ubuntu demonstration

The disposable seed needed both the current guest helper and current guest
guard. An initial attempt with an older seed guard failed the audited readiness
probe; that is a failed result, not a passing sandbox demonstration. The seed
was rebuilt with matching binaries before continuing.

The completed run is in [ubuntu-demonstration.log](ubuntu-demonstration.log).
A real Firecracker sandbox created through the CLI appeared in the signed-in
dashboard API. `yes` ran for 10 seconds; CPU reached **99.35%** for one core.
The sample also reported 118,472,704 memory-used bytes, 521,859,072 disk-used
bytes, and network RX/TX counters. `sandbox logs` returned the actual engine
console tail and `T42 exec log` / `T42 exec finished` output.

The run verified both metrics/logs HTTP routes, exactly two SSE events followed
by stream closure, long-poll waking on CLI suspend, suspended state through the
dashboard API, retained samples/logs after daemon restart, and CLI deletion
visible through that API. The ticket-owned sandbox and supervisor were released.
The final global Firecracker count was one: this machine is shared with other
tickets, so a global count is not itself proof of this ticket's resource leak.

## Sol review

Reviewed with sol-loop: one completed round with GPT-6.1 Sol on medium effort.
Sol verdict: approved at `6995496c0d77888d0390e5230c8720b1b6b9f111`.
No blocker, should, or nit findings; no fixes or tests added by review. The
review covered authentication, installer, telemetry, log authorization, SSE,
CLI and MCP, and read the real demonstration evidence without repeating VMs.
`git diff --check` passed in the independent checkout.

Ledger: the initial review attempt failed with proxy HTTP 502 during the Mac's
network outage and produced no findings or verdict. It was confirmed failed
and replaced. The completed replacement returned APPROVE with no findings.
Usage was not exposed. The reviewed diff was 31 files, 1,363 insertions and
20 deletions; review added no behavior changes. Only this review record was
added afterward.

## Fresh-install integration after rebase

Rebased onto the merged T32/T41/T43/T44/T45/T40 work. The dashboard CLI now
uses T32's installed-executable and `LAMP_PREFIX` discovery and preserves
T41's extracted global `--socket` / `--json` flags. Its help describes the
local sign-in command. Upstream operations and schema definitions were
preserved when regenerating bindings.

A focused second Sol round approved `74e068c4035fa5026dbbe130672e50bf28a1cb9c`
with zero findings. Its real compiled CLI demonstration covered discovery
through `current/bin/lamp`, dashboard output, JSON, global flags and help.
The fixture initially mismatched `/var` and `/private/var`; canonicalizing
that fixture resolved it. No product changes, tests or VM runs were added
by review. Later rebase conflicts retained both upstream storage types/docs
and T42 additions; no T42 behavior changed. Total: two completed rounds,
GPT-6.1 Sol on medium effort, no open/declined findings, usage unavailable.

`make build`, scoped Go vet, JavaScript syntax, generated-code consistency
and `git diff --check` passed after the final behavior integration.
`tools/check_contracts.py` reports fleet field drift for
`unavailable_host_ids` / `stranded_sandbox_ids`; the identical failure was
reproduced using unchanged upstream files. It is not reported as passing.

The first assembled engine candidate omitted the newly required
`lamp-network` binary and was refused before installation. After adding it,
the older reused T32 seed failed template probing because its guest helper
rejected T41's `stdin_path` field. The installer removed that disposable
engine. Those are preserved demonstration failures, not passing results.
The disposable seed was then updated offline with the current cross-compiled
Linux arm64 helper, its rootfs digest recomputed, and the candidate re-signed.
The optimized engine avoids the initial debug build's slow seed hashing.

`fresh-install.log` records the corrected signed candidate installed through
`lamp-installer install --engine --service process` at a new custom prefix.
Its default Linux template booted and ran the real probe (aarch64, Python,
compiler and page size). The installed `current/bin/lamp dashboard --json`
discovered its prefix with no socket or environment; explicit socket,
leading global JSON, `LAMP_PREFIX` and help also worked. The public dashboard
returned HTTP 200; cookie-authenticated sandbox listing worked, the CLI
automatically discovered owner credentials, the sign-in file was mode 0600,
sign-out returned 204 and the subsequent API request returned 401.

The demonstration initially compared `/tmp` against its `/private/tmp` alias
and used the old fixed installation name for deletion confirmation. Those
fixture assumptions were corrected on the same installation; no candidate
reinstall was needed. Cleanup then removed its daemon, engine, isolated
workers and owner data. The offline seed workspace and signed candidate
were removed from both the Mac and its existing Lima VM. No existing
installation, Docker object or visible app was changed. The Mac retained
more than 30 GiB free throughout this continuation. The seed refresh is
disposable demonstration preparation, not publication of new public archives.
