# T38 Sol review ledger

Reviewer: GPT-6.1 Sol, Codex in T3 Code, medium reasoning effort (reported by
orchestrator). Token usage was not returned. Base: bcd54f76f9827aa5707716b4ef79fa7e1bf86198.

Round 1 reviewed 6c53072959b2b538147b9a443e647d820a49c932 and returned CHANGES.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | blocker | Fixed: snapshot unavailable-host observations before authorizing sandbox visibility, so lease checks cannot recursively lock the fleet mutex. | Reviewer's `TestT38FleetGetWithStrandedSandbox` failed before the fix with the leaseValid/sandboxAllowed lock stack; passed afterward (`go test ./internal/control -run '^TestT38FleetGetWithStrandedSandbox$' -count=1 -timeout=15s`, package 3.633s). |

One reviewer-written regression test was retained. No other findings were raised
or declined. Round 2 reviewed 8e2f2717c5ab287fb81482ca8437dd95f232fe2b and
returned APPROVE: S1 resolved, no new blocker/should findings. The same focused
test passed in the reviewer checkout (package 7.618s).

The implementation diff was 26 files / 1,794 insertions / 33 deletions before
the fix, and 32 files / 1,957 insertions / 33 deletions afterward, including the
57-line regression test and packaging/cleanup/review evidence. Two rounds; no
extra tests; one agreed finding fixed. No findings were declined.

## Coordinator-requested cross-machine follow-up

A new, narrow follow-up review used GPT-6.1 Sol / Codex / medium effort on
850f0ef1a3f3d1c225400d33457b6e71b7ebad6b against
0b956d272b715a2d5b36f75cd73d92362da1f912. Only documentation and evidence changed;
the previously approved implementation was not reopened. Token usage was not
returned. Follow-up diff: 5 files / 71 insertions.

Verdict: CHANGES, with one nit and no blocker/should findings. The reviewer
confirmed that the evidence supports second-physical-host VM execution, exit
zero, decoded stdout and cleanup. JSONL parsing and `git diff --check` passed.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S2 | nit | Fixed the prose's available-memory value from the preceding attempt to 14,861 MiB, matching the final run. | `compute-cross-machine.jsonl` shared-machine create guard. |

Stopped after this one narrow follow-up round as required for nit-only feedback;
the prose-only correction needs no further review. No findings declined, no
product changes, tests or new remote demonstration requested by the reviewer.
The final evidence/report update adds this ledger; the actual CHANGES verdict
is preserved rather than relabeled as a new approval.
