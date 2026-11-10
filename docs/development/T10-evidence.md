# T10 control-core evidence — 2026-11-10

The local control core is implemented and exercised through credentialed HTTP,
the actual `lamp` CLI and the actual stdio `lamp-mcp` binary. **T10 is not fully
qualified: E03 has not passed.** Full 6,400-command runs against the installed real LX engine exceed the
unchanged 35 ms allocation and report command-effect failures. T6's integrated engine gate remains failed. No deterministic test or
protocol fixture result below replaces that gate.

## Behavior exercised

The implementation extends T3's real durable journal and worker path. CLI and
MCP translate canonical contracts into the same HTTP dispatcher and carry the
same bearer. All canonical commands have JSON help/input; uninstalled later-ticket
operations explicitly fail. Product requirements and architecture text are
embedded and available through `docs_read` on each transport.

| Contract | Observed evidence |
| --- | --- |
| INT-001 / INT-003 | Real CLI and stdio MCP replay the HTTP-created operation and reproduce permission denials; every canonical command's help parses as JSON. Header/path translation and documented JSON exit codes, including event follow, are checked. |
| INT-004 | Twenty concurrent equal retry keys produce one resource; owner namespaces isolate equal keys. Secret-once responses stay secret-free on replay and restart. Existing T3 crash/restart and unknown-outcome tests remain in workspace verification. |
| INT-005 | Live HTTP SSE, durable ordered replay, signed owner/key-bound cursors, private owner isolation and active reader revocation are exercised. Seven-day retention floors have explicit boundary tests. |
| INT-008 | Ten readiness checks and the worker command-start receipt are exposed. Same-revision fork races admit one effect. Denied admission leaves source revision and worker dispatch unchanged. Before-dispatch cancellation prevents work; dispatched work is never represented as undone. |
| ACC-006 | Single-owner, team and credentialed public-service admission; owner/admin/member/viewer decisions; private content remains own-only across mode changes. |
| ACC-007 | Real random bearers are hashed at rest. Delegation intersects actions/selectors/limits/expiry; empty selectors remain empty. Rotation retains the issuer's action ceiling, expiry and live ancestry. Revoking an ancestor rejects its descendant. |
| ACC-008 | A trusted enforcing adapter derives the immutable action. Twenty distinct concurrent consumers produce exactly one file write, independently read by the test. Changed destination, out-of-selector approval decisions and expiry between consumption and dispatch produce no effect. |
| ENG-003 correctness | Thirty-two simultaneous credentialed principals using identical retry keys produce 32 distinct resources, each readable only by its owner. This protocol-fixture test makes no latency claim. |

The instrumented end-to-end output is retained in
[`conformance-final.log`](evidence/t10/conformance-final.log). `LAMP_CONTROL_RACE=1`
builds the CLI, daemon and MCP subprocess binaries with Go's race detector as
well as instrumenting the test process. Additional transport and security checks
are retained in [`transports-final.log`](evidence/t10/transports-final.log),
[`security-regressions.log`](evidence/t10/security-regressions.log) and
[`dispatch-review.log`](evidence/t10/dispatch-review.log).

## Verification and preserved failures

Workspace verification uses `make bootstrap verify`: Go/Rust builds and tests,
independent benchmark harness tests, infrastructure tests, canonical contracts,
Go/Rust interoperability, generated-file checks, Go vet, Rust formatting and
Clippy. Full Mac verification passed in
[`verify-mac-submission.log`](evidence/t10/verify-mac-submission.log); full LX
verification passed in
[`verify-lx-submission.log`](evidence/t10/verify-lx-submission.log). The final
transport adjustment was separately checked with affected-package tests and vet
in [`final-delta-lx.log`](evidence/t10/final-delta-lx.log). The final source snapshot
is pinned in `evidence/t10/source-sha256.json`; earlier logs are historical runs.

The first Mac workspace run failed and remains in
[`verify-mac-failed.log`](evidence/t10/verify-mac-failed.log). Its journal child
startup and suite timeouts were investigated; crash-test children now skip
unneeded transport-binary builds. A separate benchmark resident-socket race now
waits for a successful ping rather than path existence before exercising the
workload. The Python harness then passed all 30 tests.

The initial focused Mac race run failed its 32-caller correctness assertion when
the fixture's one-second worker deadline expired under contention. Its log stays
in [`race-mac-failed.log`](evidence/t10/race-mac-failed.log). The fixture now uses a
bounded 30-second worker/wait allowance for this correctness test. The subsequent
instrumented conformance run passed. This change does not modify the independent
E03 timing constants or relabel the failed run.

## Follow-up verification

Fresh `make bootstrap verify` passed on LX after Linux preallocation:
[`r1-preallocation-verify.log`](evidence/t10/r1-preallocation-verify.log).
`make verify` also passed on the Mac for the preceding source generation in
[`r1-current-verify.log`](evidence/t10/r1-current-verify.log). Final focused LX
race checks, including the new reservation and review regressions, passed in
[`r1-final-race.log`](evidence/t10/r1-final-race.log); the native 32-process
6,400-row harness regression passed in
[`r1-final-harness.log`](evidence/t10/r1-final-harness.log).
The merged Mac run initially failed because its original-command assertion
included the new sandbox readiness Inspect; the assertion now isolates requests
after sandbox preparation. That failure remains in
[`r1-merged-verify.log`](evidence/t10/r1-merged-verify.log). The earlier full race
suite's ten-minute timeout stays in [`r1-race.log`](evidence/t10/r1-race.log).
These are correctness checks, not speed evidence.

## Review r1 corrections

M1–M4 each have a behavioral regression that was first run with its fix removed
in a temporary source overlay. Every negative control failed for the reproduced
reason, then passed with the fix. The redacted transcript is
[`r1-negative-regressions.log`](evidence/t10/r1-negative-regressions.log).

- M1: owner management cannot mint or rotate another principal's bearer. Target
  authentication uses a trusted custody enrollment, outside fleet HTTP writes.
- M2: approval inspection and decisions apply sandbox, recipe, host and resource
  selectors before mutation; denied decisions preserve revision and state.
- M3: rotation retains inherited ancestry and rotating issuer constraints, or
  rejects a rotation that cannot retain both.
- M4: failed, pending and uncertain cleanup keep their sandbox reservation until
  worker release is confirmed. Proven prevented create/fork is the exception.
- M5: E03 now contends a concurrency-one key with 32 exec admissions. Independent
  guest marker/PID observations and kernel allocation snapshots must agree with
  exactly one accepted effect. Injected public and independent overspend both
  fail the oracle. This exposed another core bug: a command-started Operation
  had freed concurrency while the command was still running. Live and uncertain
  Exec resources now keep that charge. Its negative control is retained in
  [`r1-live-command-negative.log`](evidence/t10/r1-live-command-negative.log).
- S1: `--suite all` includes E03, or explicitly reports it incomplete when caller
  preparation is missing. Completeness checks include all four suites.

Main was merged with a merge commit after PR #11 (`f36b2ff`), keeping its early
kill-child TestMain exit and `storeConfigAt(dir)`, together with the ticket's
race-instrumented transport binaries. Branch pushes now use main's dispatched
CI policy.

## E03 remains failed and unqualified

The smoke diagnostics below remain historical failures. The later runs use the
real installed Firecracker supervisor, credentialed public HTTP over its Unix
socket, actual measured metadata and the full 32 × 200 command plan. Every hard
run goes through `infra/linux/timing-run.sh`, after scanning for another timing
run and acquiring `/tmp/lamp-hard-timing.lock`. The wrapper logs the owner
services originally running and restores exactly those. Docker and T3 Code
are untouched.

| Preserved run | Trials | Correctness failures | p99 ms | Result |
| --- | ---: | ---: | ---: | --- |
| [Mac smoke](evidence/t10/e03-mac-1/summary.json) | 64 | 0 | 3192.924375 | fail, fixture diagnostic |
| [LX smoke](evidence/t10/e03-lx-1/summary.json) | 64 | 0 | 70.575689 | fail, fixture diagnostic |
| [Actual engine 1](evidence/t10/e03-actual-1/summary.json) | 6400 rows; 2336 commands | 4065 | 3770.546617 | fail; barrier-abort bug made remaining rows errors |
| [Lock profile](evidence/t10/e03-profile-1/summary.json) | 6400 | 1 | 1476.519540 | fail, diagnostic |
| [Group commits](evidence/t10/e03-group-1/summary.json) | 6400 | 70 | 1636.435953 | fail |
| [Snapshot profile](evidence/t10/e03-snapshot-profile-1/summary.json) | 6400 | 29 | 248.463165 | fail, diagnostic |
| [Two sandboxes](evidence/t10/e03-two-sandboxes-1/summary.json) | 6400 | 24 | 172.010064 | fail |
| [Linux processes](evidence/t10/e03-processes-1/summary.json) | 6400 | 1 | 510.440825 | fail |

The fixed allocation is **p99 ≤35 ms**, with zero lost effects, cross-owner effects
or quota overspend. Every run remains failed; profiles and earlier source
snapshots are diagnostics, not final certification. T6's integrated E01 remains
failed and independently prevents an E03 gate pass.

The lock profile attributed 4,133.92 aggregate mutex-delay seconds to journal
syncs under the control mutex. Engine writes now queue immutable bounded write
sets for ordered group commits, releasing the mutex while the log and committed
end marker sync. Dispatch still waits for durable admission. Public readers do
not see staged records; a failed flush breaks publication and further dispatch.
Per-operation committed snapshots also avoid waiting on unrelated output flushes.
The next profile reduced aggregate mutex delay to 86.02 seconds. Its 10,116
control sync calls totaled 21,388.896 ms (p50 1.355 ms, p99 15.856 ms), while the
installed supervisor's command-start handler itself measured p99 66.925 ms.
No measured time was subtracted from the result.

After the owner's 18:57Z memory constraint, the driver uses **two owner-private
sandboxes and 32 distinct keys**, with 16 callers per owner. It checks `free -m`
before each allocation, aborts below 12 GiB available, and refuses an occupied
two-sandbox ceiling. It retires the other owner's VM immediately after the
fixed command plan; quota contention uses one held command in the remaining VM
and allocates no third VM. Both VMs are released before slow evidence publication.
The two-sandbox run passed the quota oracle: one accepted command among 32,
matching guest marker/PID, six stable kernel allocation observations and no quota
errors. All 24 command-effect failures were observed exit 70 with signal 0;
matching stdout does not erase an unsuccessful exit. Accepted-to-worker-receipt
spans were p50 39.113 ms / p99 134.722 ms; remaining caller time including the
terminal commit was p50 17.454161 ms / p99 69.019686 ms.

The subsequent run used 32 Linux caller processes instead of Python threads to
remove shared-GIL caller scheduling. It retained the complete command count,
barriers, effect checks and threshold, and still failed: p50 60.126641 ms, p95
107.178550 ms, p99 510.440825 ms, one exit-70 failure with empty stderr. Quota
contention passed, with one accepted effect and nine allocation samples.
Both VMs were released and all four initially running owner services restored.
The accepted-to-worker-receipt p99 was 98.441 ms; remaining caller time p99
was 470.032002 ms. The journal synchronously zero-filled new capacity, including during
compaction, before acknowledging commits. Linux log reservation now uses
physical `fallocate` extents rather than synchronous zero filling; record and commit-marker durability barriers remain unchanged.
Full control/crash-recovery tests and a prefix/zero-tail reservation regression
passed. A new hard-run attempt was refused because T7 was running full E04
(`benchmarks.run`, PID 3560538). No services were changed and no E03 measurement
was started. The preallocation change therefore has no qualified timing result
yet; [the blocked preflight](evidence/t10/e03-preallocation-preflight.txt) is
retained. No installed shared-engine or guard binary was replaced. The process-runtime regression verifies all 6,400 rows
and 32 distinct child PIDs on two logical owner sandboxes, including an injected
individual failure without skipping later commands.

Each published run retains its manifest, summary, compressed hash-chained raw
observations, cleanup outcome and wrapper log. Current two-VM runs also retain
allocation-guard observations. Private identity/token/state directories and
worker grant files are excluded. The exact per-run source/artifact hashes remain
in each manifest; the final source inventory is a separate snapshot.

## Integration boundaries

This is one local authority. Distributed fleet replication, owner transfer,
epoch fencing and invitations belong to T16; fleet budgets/audit/revocation
ledger belong to T23. Public-service mode admits explicitly enrolled credentials;
it does not assert anonymous registration, identity-provider or invitation proof.
T9/T16 must supply that integration. The daemon retains its private peer-UID
Unix listener; remote callers need an authenticated gateway/tunnel. No listener
or TLS provisioning is claimed.

Prepared create/fork/exec/delete use the existing worker. Unsupported setup and
limit dimensions fail closed. Broker/access adapters must independently validate
vendor effects and normalized secret HMACs; the core's one-use approval seam does
not certify an unimplemented broker, database, vault or browser integration.
No full product-wide all-operation certification or engine/power-loss
certification is claimed. See [control-core.md](control-core.md) for the public
usage, guarantees and extension seams that T16–T19 can build on.
