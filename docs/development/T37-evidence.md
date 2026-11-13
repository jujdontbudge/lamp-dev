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

## Integration with T33 and T38

Rebased onto `03944fb` after both tickets merged (`8150e50` is also an
ancestor). Conflict resolution retains relay enrollment together with approval
actions, metrics discovery together with plugin/grant discovery, both release
guides, and T33's fork accounting together with T37's revocation checks.

`make build`, targeted Go vet and `TestT37ScopedKeyMCPDiscovery` passed after
the rebase. A new disposable daemon demonstration with the rebuilt binaries
found 210 identical operation names across CLI, HTTP and MCP; session-scoped
discovery, documentation scope denial, and persisted grant revocation also
passed. See [rebase-discovery-demo.json](evidence/t37/rebase-discovery-demo.json).
The earlier 203-operation observations and native CI run remain historical
evidence. Sol's two-round approval covers the T37 behavior before this rebase;
no additional review round was run for the integration.

## Exhaustive ownership audit and completion

The coordinator requested an exhaustive audit of all uninstalled schema
operations. The schema now has 332 names, including T38's two relay operations.
T37 installed the 12 otherwise unowned operations; all remaining 110 belong to
other tickets. The full list and ownership rationale are in
[T37-operation-ownership.md](T37-operation-ownership.md).

A disposable daemon and native public-source session demonstrated eleven new
operations, configuration pin persistence and rejection of a different enrolled
repository. CLI, HTTP and MCP each exposed the same 222 names. The job deletion
proof deliberately used an offline local provider and a terminal failed job;
it does not claim another successful CI run. The successful native Forgejo
fresh-VM CI demonstration above remains the provider execution evidence.
See [completion-demo.json](evidence/t37/completion-demo.json).

A separate real Ubuntu VM demonstrated approval deletion after approval,
persistence across daemon restart, and rejection of the deleted approval before
any webhook effect. The VM reached ready and was deleted. Admission observed
one existing Firecracker and 17703 MiB available. The ticket daemon and worker
stopped; its tmpfs, store, audit directory and temporary source directory were
removed. See [completion-approval-demo.json](evidence/t37/completion-approval-demo.json).
Setup first required T33's trusted audit-release helper and a private socket
parent directory; neither failed setup admitted a VM. An earlier local approval
attempt without a worker returned 503 and is not successful approval evidence.

`make build`, targeted Go vet and the existing scoped MCP discovery regression
passed for the expansion. No tests were added for these twelve operations;
they were demonstrated using the actual binaries.

### T41 integration

Rebased onto `c34fabd`. Retained T41's `Client.Operations`, CLI help and
served OpenAPI filtering. Its shared discovery request now uses the narrow
`operations` topic so scoped keys retain authenticated inventory access.
Removed T37's duplicate discovery helpers. Rebuilt all three binaries and
repeated the disposable completion demonstration: all eleven operations passed
and CLI/HTTP/MCP still exposed 222 identical names. `make build` passed.

### Completion Sol review ledger

Completion round 1 (GPT-6.1 Sol, medium effort, priority) found S2, should:
clean-handoff recipient reads ignored an expiry accepted by `share_update`.
The reviewer's focused HTTP reproducer failed locally with HTTP 200 after
expiry. Recipient authorization now checks that expiry. The same reproducer
and the existing scoped MCP discovery regression passed together (1.687s),
and `make build` passed. One 46-line reviewer regression was retained; no
other expansion tests were added. Prior S1 remains resolved, and no findings
were declined. Token usage was not returned. Before the completion review,
the expansion diff was 18 files, 814 additions and 77 deletions.

### T39 integration

Main advanced to `f9a3989` while completion review ran. Rebased again and
retained T39's lifecycle network configuration and preview grant operations
alongside T37's CI configurations and host control grants. Initial rerun caught
the network handler intercepting CI configuration creation (400); dispatch now
selects by adapter or resource, and shared reads combine both inventories.
The real daemon rerun created/read both network and CI configurations, passed
all eleven local completion operations and observed **244 identical CLI, HTTP
and MCP names**. The ownership audit now lists **88** remaining operations;
T39 also supplies five connection operations originally assigned to T43.
Both T37 focused regressions and T39's interrupted-preview recovery regression
passed together (2.788s). `make build` passed after this integration.

Completion round 2 approved `bd09955`: S2 resolved, S1 remains resolved,
no new blockers or should-fix findings in shared dispatch/inventory. Its three
focused regressions passed (2.505s); usage was not returned. The completion
diff after review was 20 files, 910 additions and 79 deletions, including
evidence, integration changes and the single retained expiry regression.

Main subsequently merged T32 at `43a941e`; the final rebase applied cleanly.
`make build` and the same three regressions passed (2.460s). The first demo
continuation connected before the daemon accepted connections; no operation
ran. After waiting for an accepting socket, the actual daemon demonstration
passed all eleven operations again, both configuration inventories, and
244-name interface parity. The previous approved product changes were retained;
no additional review round was run for this clean rebase.

### T36 integration and final ownership refresh

Rebased onto `0b6c257` after Sol approval. The single dispatch conflict was
resolved by preserving T36's dedicated file-read route before lifecycle routing
and retaining T37's shared configuration/grant dispatch. No other product
changes were made. Rebuilt CLI, HTTP and MCP demonstrated **255 identical
installed names** against the **336-operation schema**. T36's four new schema
operations are installed; seven previously missing exec/file operations are
also installed. Only `exec_delete` remains in T36's row. T39's 17 network
operations and five connection operations remain accounted for as installed.
All **81** remaining names belong to other tickets; the complete list is in
the updated ownership audit and MR description.

`make build`, the disposable completion demonstration (including both network
and CI configurations), and the three focused T37/T39 regressions passed
(1.528s). Sol's latest approval remains the completion round-2 verdict before
the subsequent T32/T36 integrations; no further review round was run for the
route-preserving rebase.
