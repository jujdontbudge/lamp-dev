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
Round 2 reviewed these fixes, sparse extraction and rebase integration.

Round 1 diff: 28 files, 1321 insertions, 136 deletions (includes generated
contracts, documentation and historical demonstration evidence).

Final review base after rebasing T33/T38/T41: `c34fabda778bb34ff1b0f1da5e3bdc42882046b3`.
The recovery continuation also required sparse disk extraction (see the retained
blocked stack and successful attempt 14). Rebase integration preserves T33
capacity cleanup, T38 behavior, and T41 help/error guidance. Post-rebase
`make build`, targeted Go vet, both reviewer tests, and human/JSON lifecycle
help checks passed. Round 2 covers those changes and S1/S2.

Round 2 head: `e4b9abd0a519fb5c59a6a2ff4c087f6fc04ea343`.
Verdict: APPROVE; S1 and S2 resolved, no new blocker or should. Both named
review tests passed (3.240s). Usage was not exposed. The trailing blank line
in the retained stack dump was excluded as a formatting nit; removed afterward.

After approval, main advanced through T39 to
`f9a3989ee4d8b4c4cb6f6f7d2ec689b077f59a80`. A mechanical rebase retained both
network-policy validation/restoration and T34 lifecycle/snapshot behavior.
`make build`, targeted Go vet and both named review regressions passed again.
The final diff is 46 files (about 2,080 insertions and 142 deletions), including
the two reviewer tests, generated declarations and retained demonstration logs.
No additional review round was started beyond the two-round budget.
