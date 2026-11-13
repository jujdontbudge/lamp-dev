# Sol review ledger

Reviewer: Codex GPT-6.1 Sol, medium effort, priority tier. Usage was not returned.
Round 1 reviewed head `7666c1383240f9e9f5c096d7006026a5f1e34e37`.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Accepted, fixed: distinguish lost relay results from completed runtime errors and propagate outcome_unknown. | `TestT44ReviewAcknowledgedRelayTimeout` failed before and passes after. Actual `--job` probe in `relay_timeout.py` reports outcome_unknown while runtime remains active and subsequently completes its effect. |
| S2 | should | Accepted, fixed: apply Task deadline to create and recheck Task authority during kit polling; create's existing failure cleanup releases its VM. | `TestT44ReviewStoppedTaskDuringCreateKit` failed before and passes after, observing operation completion and worker release after Task stop. |

The two reviewer regression tests use the HTTP API with a worker transport fixture;
they do not claim VM execution. The timeout probe uses the actual Python runtime
and actual job client locally, substituting only disposable storage and a shorter
socket timeout. Its output:

```json
{"job_result":{"outcome_unknown":true,"error":"TimeoutError","message":"timed out"},"runtime_still_running_at_timeout":true,"effect_completed_after_timeout":true}
```

No findings declined. Round 2 pending. Before review the base diff changed 30
files (9,648 insertions, 1,412 deletions); the final size is recorded after review.
