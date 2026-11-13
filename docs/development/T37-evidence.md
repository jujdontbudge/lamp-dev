# T37 control operations evidence

The demonstration used the built `lampd`, `lamp`, and `lamp-mcp` binaries and
public HTTP operations against disposable state. VM work ran on the owner's
Ubuntu laptop, using a separate T37 supervisor store and a copy of an existing
public demo template. No Docker resources or owner service accounts were used.
The CI service was a native Forgejo 17.0.1 binary with a disposable `demo`
account and a copy of `gitlab-examples/ci-debug-trace`.

Observed control behavior is recorded in
[control-demo.json](evidence/t37/control-demo.json):

- Ten shipped CI plugin descriptors and ten adapters were discoverable; the
  enrolled Forgejo plugin's test queried its actual provider queue successfully.
- A real Firecracker sandbox reached `ready` at generation 1, then was released.
- An approved webhook produced `{"message":"t37-approved-effect"}`. Approval
  survived a daemon restart; replay returned `APPROVAL_REQUIRED`.
- A second admitted principal received an owner-issued scoped key. Watch-only
  access refused input; an explicit input grant allowed typing. Share revocation
  returned 404 on subsequent access. Key revocation returned `UNAUTHENTICATED`
  with an acknowledged supervisor revocation status.
- The enrolled local webhook received three signed events: `task.done`,
  `task.needs_decision`, and `task.stuck`.

[discovery-demo.json](evidence/t37/discovery-demo.json) records a separate live
Mac daemon run: CLI help, served OpenAPI, MCP tools and daemon installed names
were identical (203 each). A session-scoped key could discover MCP tools while
ordinary documentation remained scope-denied. A revoked host control grant
remained disabled after restart, and new sandbox admission returned
`SCOPE_DENIED`. That run made no VM execution claim.

## Successful local CI job and cleanup

The final continuation completed `jb_c05c67429fb3a2484d21bf19334ec24d` with
`result=success` and `trust_class=public_pr`. Its native Forgejo runner 13.2.1
accepted exactly one job. The workflow checked that a prior-job marker did not
exist and that `LAMP_MODEL_TOKEN` was empty, then cloned the disposable public
source repository and checked its README. No model broker grant was issued.

Lamp durably saved the complete 478-byte `forgejo-job.log` artifact before
removing the VM. The [saved native log](evidence/t37/forgejo-job.log) contains
`fresh-vm-no-model-secret`, `public-source-job-success`, and `Job succeeded`.
[artifact-demo.json](evidence/t37/artifact-demo.json) records the artifact's
complete state, length and SHA-256. The sandbox was `deleted` and Forgejo had
zero registered runners. The same Ubuntu daemon exposed 203 identical names
through CLI, HTTP and MCP; host control-grant revocation persisted after restart
and refused new execution.

[cleanup-demo.json](evidence/t37/cleanup-demo.json) records final teardown:
no ticket processes remained; the disposable Forgejo account, project,
credentials, VM store and audit storage were removed; the ticket tmpfs was
unmounted and its dummy interface deleted. Shared Firecracker counts in the
records include other tickets' VMs, which were left alone.

## Verification and review

`make build` passed (C launcher, Go packages, generated SDK consistency and
locked Rust workspace build). After the review fix, `go build ./...` and
`go vet ./internal/control ./internal/surface ./cmd/lamp ./cmd/lampd ./cmd/lamp-mcp`
passed. The focused reviewer reproduction
`go test ./internal/control -run '^TestT37ScopedKeyMCPDiscovery$' -count=1`
failed before the fix with HTTP 403 discovery and passed after it. This is the
only added test.

The existing `TestControlCoreInterfacesAndAuthority` first failed its
background-work count assertion (`denied transport dispatched work`). A separate
unchanged baseline passed, and the current code passed on repeat. The initial
failure remains part of the record.

Sol round 1 requested S1: session-scoped keys could not discover tools because
MCP discovery required `docs.read`. The fix exposes only installed operation
names to authenticated callers and preserves authorization for individual calls
and other documentation. S1 is agreed and fixed; no findings were declined.
Review usage was not exposed.

## Earlier interrupted attempts

The initial source push rejected a shallow clone. Its error exposed a disposable
Forgejo token in the command arguments; that instance was stopped and discarded.
Subsequent commands passed credentials privately and sanitized command failures.
Other setup attempts encountered a private pid-file permission, missing task
budget and a quarantined copied template; these were corrected in disposable
state, with the public producer key explicitly enrolled. The first CI intake
was accepted, but the observation script incorrectly expected a synchronous
result. Shutdown cleaned up that job before CI success was observed. A later
supervisor startup exceeded the script's readiness wait under contention; its
shutdown was subsequently confirmed. The Mac network outage interrupted the
next continuation. Those attempts are not successful CI evidence.

After network recovery, a CI run reached `public_pr` in a fresh sandbox but
failed when the runner exited 70 before native completion. Its native-log
artifact was incomplete. The demonstration was still using older T19 worker
and guard binaries; the worker also rejected newer output-collection requests
as non-canonical. The VM was released and Forgejo stopped. The current branch's
worker and guard were then built successfully on Ubuntu with
`cargo build --release --locked -p lamp-supervisor -p lamp-guard` into a separate
T37 source directory for the continuation. No shared binaries were replaced.

Sol round 2 approved the behavior at `9678e5321aeb9c257afba730a14c927aea090b68`:
S1 resolved, no new blockers or should-fix findings. Its focused reproduction
passed again (1.380 seconds). Two rounds used GPT-6.1 Sol with medium effort;
usage was not returned. The product diff grew from 16 files, 722 additions and
48 deletions before review to 17 files, 767 additions and 50 deletions after
review, including the reviewer's single 34-line regression test. Evidence
records are additional documentation.

The matching-worker continuation still failed before the native job started.
A read-only `Inspect` of the runner command showed its `Declare` request to the
host's primary Wi-Fi address timing out. A ticket-owned dummy interface and
exact private endpoint (`10.237.37.1:37437`) were then enrolled for Forgejo,
with the public-copy workflow and its digest updated to that local endpoint.
The failed jobs and their incomplete native-log artifacts remain in the JSON
record; no failure was relabeled as success.
