# T3 control-path evidence

Captured on the owner's Apple Silicon macOS machine, 2026-11-09. This proves
CLI/API/control/Protobuf behavior, **not** real VM isolation, RAM continuity,
engine qualification or a timing gate. The Rust peer is an explicitly test-only
host-command fixture. T2's real supervisor/engine must be joined at T6 using the
[documented integration seam](demo-control-path.md#worker-integration-seam-for-t2t6).

## Captured run

```sh
LAMP_CONTROL_EVIDENCE_DIR="$PWD/docs/development/evidence/t3" \
  go test ./internal/control -count=1 -v
LAMP_CONTROL_RACE=1 go test -race ./internal/control -count=1
.venv/bin/python internal/control/testdata/validate_evidence.py \
  docs/development/evidence/t3
```

All three exited 0. Race mode also builds the actual CLI and daemon subprocesses
with `-race`, rather than checking only the test driver's goroutines.

- [control-test.log](evidence/t3/control-test.log): real CLI/daemon subprocesses;
  accepted-before-ready, create/exec/fork calls and a Rust-produced independent
  marker `control-path-ok`; idempotency conflict/replay, source revision,
  unsupported setup, worker rejection, uncertain effects, restart, journal
  failure, exact uint64 display, UID/PID/Hello/frame/context checks and handshake
  cancellation.
- [race-test.log](evidence/t3/race-test.log): race-instrumented control path.
- [SandboxCreateResult.json](evidence/t3/SandboxCreateResult.json),
  [SandboxGetResult.json](evidence/t3/SandboxGetResult.json),
  [ExecCreateResult.json](evidence/t3/ExecCreateResult.json),
  [SandboxForkResult.json](evidence/t3/SandboxForkResult.json),
  [OperationGetResult.json](evidence/t3/OperationGetResult.json): captured live
  results, each independently accepted by the canonical Draft 2020-12 schema
  with format checking. The create/fork completion is never synthesized from
  an accepted worker receipt.
- [marker.txt](evidence/t3/marker.txt): independent host-fixture command marker,
  explicitly labeled as not a microVM/performance result.

Linux x86_64 and arm64 CLI/daemon cross-builds also exited 0:

```sh
GOOS=linux GOARCH=amd64 go build ./cmd/lamp ./cmd/lampd
GOOS=linux GOARCH=arm64 go build ./cmd/lamp ./cmd/lampd
```

No paid service or hosted runner was used for these local checks. No guest OS,
host virtualization, Docker state, user service, or visible application was
modified. No 500 ms create or 1000 ms fork result is asserted.
