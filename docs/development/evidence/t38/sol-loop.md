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
