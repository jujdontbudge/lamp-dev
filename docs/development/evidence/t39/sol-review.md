# T39 Sol review ledger

Reviewer: GPT-6.1 Sol through Codex in T3 Code, medium reasoning effort,
priority service tier. Usage was not exposed.

Round 1 reviewed head `01367970` against `8150e50` and returned **CHANGES**.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: startup clears network admission locks from the previous daemon; uncertain operations remain outcome_unknown and no effects are replayed. Fresh owner close/revoke requests can proceed. | Reviewer's `TestT39ClosePreviewAfterInterruptedClose` failed with HTTP 409 before the fix, then passed after the fix. |

No findings were declined. One reviewer-authored regression test was retained;
implementation otherwise added no tests. The test uses the real close planner,
persists its admission boundary, reopens the service and sends a fresh canonical
HTTP close request. It also confirms the interrupted operation remains unknown.

The branch was rebased onto `03944fb` (including T38 and T33). The create-input
conflict retains T33's explicit pids_max validation and T39's network_policy_id.
Worker protobuf regeneration check passed after the automatic merges.

Round 2 will check S1 and the changed recovery/conflict-resolution code only.
Initial review diff: 31 files, 2186 insertions, 316 deletions. Final size and
verdict are recorded after round 2.
