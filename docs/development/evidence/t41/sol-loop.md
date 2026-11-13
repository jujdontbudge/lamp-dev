# T41 Sol review ledger

Reviewer: Codex gpt-6.1-sol, medium effort (reported by T3).
Usage: not exposed.

Round 1 reviewed head 17085a052262abf91c2d7982361502019950a174 against
bcd54f76f9827aa5707716b4ef79fa7e1bf86198. Verdict: CHANGES.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: human mutation output preserves its completion result, including the one-time key secret. Replays still omit the secret. | Reviewer TestReviewHumanKeyCreationPreservesOneTimeResult failed before the fix and passed afterward. |
| S2 | should | Fixed: revision reads use fields from the getter schema, including empty inputs for singleton telemetry. | Reviewer TestReviewTelemetryUpdateFetchesRevision failed before the fix with TelemetryGetInput.telemetry_id: unknown field and passed afterward. |

Both reviewer regression tests use a disposable real control service. They were
applied and reproduced locally. No other new tests were added. The existing CLI
mutation-header smoke, scoped Go vet and make build passed with the fixes.
No findings declined.

Round 2 reviewed the fixes at a54d36dfdb128cb90dc2e4ad22dcbbb340cd9367.
Verdict: APPROVE. S1 and S2 resolved, no new blockers or shoulds. The reviewer
reran both regression tests successfully and made no changes. Two rounds total.
The approved behavior is pushed to GitLab MR !3; the final ledger update is
documentation only.

Diff before review: 34 files, 1378 insertions, 184 deletions. The round 1 fixes
add 11 product lines and the reviewer's 86-line regression file; this ledger is
the remaining review evidence.

Final diff: 36 files; see the MR for the complete source, two reviewer tests and evidence.
