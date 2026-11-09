# PR #5, review round 1

Merged `origin/main` (`fb636b3`, T1's merged PR #2) before final verification.
No workflow or shared-contract changes were needed for these fixes.

## Regression tests

Each test was run against the unfixed implementation and failed on the reported
behavior before the corresponding fix. The same focused tests now pass.

- **S1 — `TestMutationIdempotencyKeyHeader`:** create, exec and fork accept
  header-only keys; missing/duplicate keys and JSON body keys fail before worker
  dispatch; same header/input replays do not dispatch effects.
- **S1/S2 CLI — `TestCLIUsesCanonicalMutationHeaders`:** independently captures
  CLI requests for all three mutations. Keys are in `Idempotency-Key`, fork
  revision in `If-Match`; neither appears in JSON. Fork's sandbox ID is in the
  path rather than the body. Failed before the CLI transport mapping fix.
- **S2 — `TestForkIfMatchHeader`:** canonical header-only fork works; missing,
  invalid, duplicate and overflowing revisions fail; stale revisions conflict;
  a current body revision cannot override a stale header. Body revisions are
  rejected, not preferred. Failed after S1 was fixed and before S2 was fixed.
- **S3 — `TestWorkerErrorMetadataSurvivesJournalAndHTTP`:** all 31 canonical error
  codes and both retryability values travel from a real framed worker peer to
  HTTP and the operation journal. `AUDIT_UNAVAILABLE` and its retryability also
  survive daemon restart/replay without redispatch. Before the fix, valid codes
  were coerced to `DATA_UNAVAILABLE` and retryability was lost.
- **S4 — `TestSlowResponseDoesNotHoldServiceMutex`:** a deterministically blocked
  response writer (including a 60 KB exec document) cannot stall an unrelated
  read. Covers resource reads, missing/broken reads, mutation responses and error
  responses. Before the fix, every covered path held the global mutex during
  the blocked write. The fix serializes nested state under the mutex and writes
  only immutable bytes after unlocking.

## Final commands

All commands exited **0** on the resolved tree:

```sh
make verify
LAMP_CONTROL_RACE=1 go test -race ./cmd/lamp ./internal/control -count=1
go test ./cmd/lamp ./internal/control -run 'TestCLIUsesCanonicalMutationHeaders|TestMutationIdempotencyKeyHeader|TestForkIfMatchHeader|TestWorkerErrorMetadataSurvivesJournalAndHTTP|TestSlowResponseDoesNotHoldServiceMutex' -count=1 -v
```

Race mode instruments the actual CLI/daemon subprocesses too. Existing end-to-end
create/exec/fork and Rust transport tests pass with canonical HTTP headers.

Captured command output and exit codes:

- [make verify](evidence/t3/review-r1-verify.log)
- [race tests](evidence/t3/review-r1-race.log)
- [focused regressions](evidence/t3/review-r1-regressions.log)
