# Sol review ledger

Base: `bcd54f76f9827aa5707716b4ef79fa7e1bf86198`.
Round 1 head: `0dae23f071b88dcbfe877b9d4fa9b4d592a3cf2e`.
Reviewer: Codex `gpt-6.1-sol`, medium reasoning effort, priority service.
Usage was not returned. Round 1 verdict: CHANGES.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: recorded resumed generations schedule the existing independent readiness probe at startup and during lifecycle reconciliation, including a fresh recovered prefix without create/fork history. | `TestT34ReviewRecoveredSandboxSurvivesDaemonRestart` failed before the fix (`unavailable`, no reconciliation scheduled) and passed after it. |
| S2 | should | Fixed: snapshot deletion refuses `capturing` with `RESOURCE_IN_USE`. | `TestT34ReviewSnapshotDeleteDuringCapture` failed before the fix (snapshot resurrected as ready revision 2) and passed after it. |

Both reviewer tests were applied and reproduced locally before the product fixes.
Only these named tests ran; no other tests were added. No findings declined.
Round 2 will review these changes and any demonstration fixes only.

Round 1 diff: 28 files, 1321 insertions, 136 deletions (includes generated
contracts, documentation and historical demonstration evidence).
