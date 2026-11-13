# T36 Sol review ledger

Reviewer: Codex `gpt-6.1-sol`, medium reasoning, isolated T3 worktree.
Usage counts were not exposed.

Round 1: CHANGES.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: PTY shells supply xterm-256color by default, CLI passes local TERM, SSH passes negotiated TERM. Reconnect preserves the running shell. | Reviewer's `TestT36TerminalSuppliesTerminalType` failed on the initial implementation, then passed after the fix. |

One reviewer-written regression test retained; no declined findings. Initial
implementation diff: 45 files, 6472 insertions, 2395 deletions. Round 2 checks
S1 and the changed code only; its verdict is pending.
