# T36 Sol review ledger

Reviewer: Codex `gpt-6.1-sol`, medium reasoning, isolated T3 worktree.
Usage counts were not exposed.

Round 1: CHANGES.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: PTY shells supply xterm-256color by default, CLI passes local TERM, SSH passes negotiated TERM. Reconnect preserves the running shell. | Reviewer's `TestT36TerminalSuppliesTerminalType` failed on the initial implementation, then passed after the fix. |

One reviewer-written regression test retained; no declined findings. Initial
implementation diff: 45 files, 6472 insertions, 2395 deletions. Round 2 checks
S1 and the changed code only; verdict: APPROVE. S1 is resolved, with no
new blocker/should findings.

Before round 2, rebased on origin/main 03944fbd (T38 and T33 merged).
Contract conflicts were resolved semantically and all bindings regenerated:
T33 Allocate validation/template/shared-disk fields and inventory remain; T38
relay resources and operations remain alongside T36 file/exec operations. Main's
wire comparator already handles empty map defaults and was retained unchanged.
Failed-boot cleanup keeps T33 release/accounting and T36's fresh 90-second
cleanup deadline. `make build`, targeted Go vet, Rust clippy, the S1 regression,
and generated consistency checks passed after rebase. Linux supervisor/init/
guard and Go builds also passed. No additional VM was needed for conflict
resolution. Post-fix/rebase diff: 47 files, 6527 insertions, 2386 deletions (before
this ledger note).

Round 2 approved a9df402. The approval covers the TERM fix and integration at that head. Two rounds total with gpt-6.1-sol on medium reasoning.
One agreed finding fixed, none declined, one reviewer-written test retained.

Subsequent coordinator-requested integration rebased onto main 43a941ed
(T32, T33, T38, T39 and T41 included). CLI dispatch retains preview, quickstart,
global flags and help; shell/copy use T32 installation socket and endpoint-aware
credential discovery. Worker bindings retain NetworkCall and the extended Start.
The existing two-round approval predates this integration; no third review was
run. Fresh `make build`, targeted Go vet, the retained TERM regression, public/
protobuf generation checks, and Linux supervisor/init/guard plus Go builds passed.
CLI help was exercised directly for sandbox, shell and copy, including a leading
global connection flag. No additional VM or tests were added for this rebase.
