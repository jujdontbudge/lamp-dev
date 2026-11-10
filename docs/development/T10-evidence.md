# T10 control-core evidence — 2026-11-10

The local control core is implemented and exercised through credentialed HTTP,
the actual `lamp` CLI and the actual stdio `lamp-mcp` binary. **T10 is not fully
qualified: E03 has not passed.** The retained diagnostic timings exceed the
unchanged 35 ms allocation; neither uses a qualified engine or complete §22
metadata. T6's integrated engine gate remains failed. No deterministic test or
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

## E03 remains failed and unqualified

| Preserved run | Trials | Correctness failures | p99 | Fixed allocation | Result |
| --- | ---: | ---: | ---: | ---: | --- |
| [Mac diagnostic](evidence/t10/e03-mac-1/summary.json) | 64 | 0 | 3192.924375 ms | ≤35 ms | fail, diagnostic |
| [LX diagnostic](evidence/t10/e03-lx-1/summary.json) | 64 | 0 | 70.575689 ms | ≤35 ms | fail, diagnostic |

Each directory contains its manifest, summary, run log and compressed chained
raw observations. These are earlier implementation-generation smoke diagnostics
against the Rust protocol test worker, with incomplete metadata. They are not
final-source certification. Owner services stayed running; Docker was untouched.
Only ticket-owned source/evidence directories were written on LX.

The new independent E03 runner requires 32 prepared private sandboxes with
separate principals/credentials and 200 synchronized rounds per caller. It times
caller send through durable admission and the worker command-start receipt,
then independently checks output, replay and foreign-owner denial. It requires
a matching-build/policy integrated E01 pass and complete qualified lane metadata.
It never subtracts guessed engine overhead, changes a threshold, prepares a
template in the timed path or stops an owner service. A smoke run cannot pass.
An actual-engine 6,400-command qualified E03 run remains outstanding; the failed
T6 integrated E01 is not superseded by this ticket.

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
