# T1 review round 1: S1–S3

Verified on 2026-11-09. Scope: denied-job termination and exact resource / extra-operation conformance.

## S1: real denied workflow with `if: always()`

The [probe fixture](../../tests/ci/fixtures/denied-always.yml) was temporarily
installed as `.github/workflows/denied-probe.yml` in commit
`cdac0c2f9cd755e07b02feb532772b8c9ffa2557` on the implementation branch. The
signed workflow reference is outside the guard's allowlist. Admission was never
weakened. Its only workflow step uses `if: always()` to write a harmless marker
under the runner prefix; it has no checkout/action dependencies.

Actual run: https://github.com/JohanSardar/lamp/actions/runs/42719518063
([saved job/step status](evidence/t1/review-r1-denied-probe.json)). All three jobs
failed during the start hook. Every hostile marker step has
`startedAt=0001-01-01T00:00:00Z` and remains pending: no workflow step ran.

The host Listener logs independently show the job's Worker being killed:

| Lane | Listener log | Denied job start (UTC) | Worker PID | Worker exit |
| --- | --- | --- | --- | --- |
| LX | Runner_20261109-231838-utc.log | 23:20:49 | 3157912 | 137 at 23:20:51 |
| M5 | Runner_20261109-231839-utc.log | 23:21:04 | 40236 | 137 at 23:21:07 |
| LA | Runner_20261109-231839-utc.log | 23:20:46 | 9779 | 137 at 23:20:49 |

On each host, Python checked that
`~/.local/share/lamp-ci/denied-always-42719518063` does not exist. All three
installed guards have SHA-256
`593816c35afa977d9e4c3be63fe6686e5dd9351df28761d979feebebfbee9f77`.
The Listener survived on each lane. Only the runner services were paused to
install the guard; owner applications and Docker were untouched.

The temporary workflow is removed after the probe. To reproduce, copy the
fixture to that workflow path on this branch and push as owner; inspect job step
start times, Listener worker exit and the marker's absence on each lane, then
remove the workflow again. The expected workflow conclusion is failure.

Unit tests also verify that only the hook's Worker ancestor receives SIGKILL
and a process-discovery failure holds the hook instead of returning failure.

## S2 and S3: mutation regressions

`tests/contracts/test_schema.py::test_reviewed_resource_and_operation_drift`
first failed against the old checker, then passed with the fixes. It rejects:

- Removing Sandbox.owner_id or adding Sandbox.invented_authority.
- Moving deadline_ms from HostDrainInput to HostBenchInput.
- Changing sandbox_resume's REST path or HTTP method.
- Removing a typed result field or inventing an input/result field.

An additional in-memory sensitivity run rejected 112 resource mutations
(remove the discriminator / invent a field for each resource), plus 95 wrong
routes, 95 invented inputs and 95 invented results, one per extra operation.

The checker compares each resource's exact fields, including common fields,
the explicit state/security applicability set and Event's transport-record
exception. Each extra action has its own exact input/completion names and
method/path. Checks resolve result aliases. This exposed BlobDownloadResult's
accidental artifact-only manifest_blob_id; its schema and generated Go/Rust
bindings now omit that field, while ArtifactDownloadResult retains it. Schema
validation tests prove both download result shapes.

`make verify` passed locally. The normal self-hosted CI run on the fix commit
passed LX, M5 and LA, including regeneration on LX:
https://github.com/JohanSardar/lamp/actions/runs/42719517441
The final cleanup push runs the same three checks again.
