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

No findings declined. Round 2 approved at
`69b0b0471f7c5b40a8977802e9bc6f547b4101b0`: S1 and S2 resolved; no new
blocker or should findings. The focused API regressions and actual Python probe
passed; no reviewer files edited or VMs created. Before review the base diff changed 30
files (9,648 insertions, 1,412 deletions); after review the diff changes 33 files (9,905 insertions, 1,414 deletions),
before this final ledger update.


## Coordinator follow-up review

The first scoped round reviewed head `792f0958619ab30a7779b60962d137c7e10c4ded`
against `1e6b9bbbf9dd48b9edc83d93033f4e77ad8e4598`: network director,
Mac installer/helper diagnostic, host receiver idle reads, and kit status polling.
Codex GPT-6.1 Sol on medium, priority returned **APPROVE**, with no blocker,
should or nit findings. It reviewed durable admission, helper placement, guest
ACK deadlines and receiver cleanup; both retained API regressions and scoped
whitespace checks passed. No new tests or files were added by the reviewer.
The behavior diff remained unchanged through review; the full follow-up diff
at review was 18 files, 4,371 insertions and 68 deletions, mostly evidence.
No findings were declined and usage was not returned.

Round 2 approved `9802edbbde2f14f96faac56c4e8ed1dc3021bf08`, reviewing only
the shared bridge's connection handling added after the first approval. Sol
ran the actual dropped-caller probe against both revisions: before, the bridge
exited and refused the next connection; after, it stayed alive and returned 42.
It also sent an incomplete request followed by EOF, then successfully ran the
next command. No findings or edits.

Round 3 approved `ad1f47dede52d5e3ddcb29a10233ba7264bc2286`, reviewing only
the delayed-readiness fix and its extracted read-only retry helper. This extra
round used the skill's critical/main-feature exception: the real fresh arm64
kit entry operation had failed before job submission. Sol confirmed generation,
state, authority, Task stop/expiry and context checks remain intact; bootstrap
writes and job submission stay single-attempt, and runtime uncertainty remains
terminal. No findings, files, tests or demo activity were added.

All follow-up rounds used the same model, effort and tier. No findings were
open or declined, and usage was not returned. Each review after round 1 covered
only new behavior added after the previous approval.

Round 4 approved `b3801c56203284f7aead3c1cb873e62d988b4def`, checking only
`--only-shell` and the headless launch caller. This critical/main-feature check
followed the real arm64 browser operation timing out while installing the full
interactive browser. Sol verified the flag against official Playwright docs;
it matches headless launch without a channel override. No findings, edits,
tests or runtime demonstrations were added by the reviewer.

Round 5 approved `f413c5299b936d104c0eecb5601237cf0e937722`, checking only
the bounded two-minute navigation wait after the real cold-page timeout. Sol
confirmed Playwright interprets `timeout=120000` as two minutes, with
`domcontentloaded` retained and the existing relay allowance covering the wait.
No findings, edits or tests were added. The critical/main-feature exception
applied to the first cold navigation returning failure before its page loaded.
