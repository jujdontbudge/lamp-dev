# T43 Sol review ledger

Reviewer: Codex `gpt-6.1-sol`, medium effort, priority service tier.
Usage was not exposed. Maximum two rounds; round 2 covers fixes and open findings.

## Round 1

Head: `b8a014ea5351707c532191112b7b9968faafe049`. Verdict: CHANGES.

- S1, should, agreed/fixed: attach exited on ended/failed before draining the
  watch cursor. The smallest fix waits until the cursor is empty.
- Reviewer regression: `go test ./cmd/lamp -run
  '^TestT43AttachDrainsOutputWhenSessionEnds$' -count=1`.
- Before fix: `attach exited successfully after 1 watch page(s), dropping final
  result; printed 100 of 101 frames` (FAIL).
- After fix: PASS (`ok lamp.eilmer.si/cmd/lamp 0.663s`).
- `go vet ./cmd/lamp` and `make build` passed.
- No declined findings. One reviewer-supplied test added; no other review tests.
- Evidence limitation acknowledged: PKCE initiation is observed, completed
  OpenRouter sign-in exchange is unproven. T43 remains incomplete for this and
  the Claude, signed-in third-party CLI and Linux capability gaps in README.md.

Diff before review: 31 files, 4382 insertions, 144 deletions.

## Round 2

Head: `a7fbab14d99bcdef71bc809a0fb7149d459a1a61`. Verdict: APPROVE.
S1 resolved; no remaining findings. The reviewer independently ran the targeted
regression: PASS (`ok lamp.eilmer.si/cmd/lamp 0.604s`). No files changed by review.
Usage was not exposed. No findings declined.

Reviewed diff: 33 files, 4487 insertions, 144 deletions (before this ledger update).
The review added one regression and the one-line drain fix. Two rounds with
`gpt-6.1-sol` on verified medium effort. Final delivery pushes the approved
behavior plus this documentation update; T43 remains explicitly incomplete.
