# T35 independent Sol review

Provider/model: Codex / GPT-6.1 Sol, medium reasoning effort. Usage was not
exposed. Review is limited to this ticket; no benchmarks or broad suites.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | blocker | Fixed: helper state is under the traversable `store/network` parent, so startup never traverses the still-root-owned sandbox directory. | Real confined `lamp-network bridge` reported ready with the sandbox directory mode `000`; see `mac-helper-permissions.json`. No VM or elevated privileges. |
| S2 | blocker | Fixed: helper state is outside every VMM-writable subtree. | Sol's native Seatbelt probe reproduced audit truncation before the fix (exit 42, empty file). With the fixture following the new production layout, the probe is denied and audit bytes survive. `python3 tests/review/t35_mac_audit_test.py` passes. |

Round 1 verdict: CHANGES. Both findings were accepted. One targeted test from
Sol was retained; no additional implementation tests were added. The fixture's
helper directory was moved alongside the production directory. The VMM profile
itself was not broadened. `make build` passed after the fixes.

Round 2 verdict: APPROVE at `e58385dc6198e2c4d6be2e023966f86be70c7cad`.
S1 and S2 resolved; no new blocker or should-fix finding. The reviewer ran
a targeted `lamp-vz` build, the retained audit test, and `git diff --check`;
all passed. No privileged Mac engine or VM was run. Usage was not exposed.

## T39/T32 follow-up

The coordinator requested managed previews and live allow/deny policy on the
M5 after T39 merged. T35 now uses the shared managed helper over an inherited
private control stream, applies create-time policy before VZ boot, and forwards
Mac supervisor network calls. The T32 rebase retains the new release installer;
its release payload already includes `lamp-network`. Existing historical T32
and T39 statements that Mac networking was unverified remain historical.

Real M5 evidence: `mac-managed-network.json` (preview HTTP 200; DNS/HTTPS denied,
live allowed with HTTP/2 200, then denied again in the same guest). `make build`,
targeted Go vet and `git diff --check` passed. A follow-up local change makes
VMM teardown independent of helper health, so a failed helper cannot leave
`Child::wait` blocked on a still-running VMM. Targeted Rust build passed.

Follow-up Sol verdict: APPROVE at `2cdf624ca15cc4b84077c65f61bf750094c706ea`
(Codex / GPT-6.1 Sol, medium). No new blocker or should-fix finding. The
reviewer checked T39 lifecycle callers and T32 release packaging; initial
policy, pending helper/identity reuse and VMM teardown were accepted. S1/S2
protections remain intact. `git diff --check 9e67a02..HEAD` passed. No new test
was added; no VM or privileged path was rerun. Usage was not exposed.

Review totals: original implementation 2 rounds, requested T39/T32 follow-up
1 round. One reviewer-supplied regression test retained. Before the follow-up,
the diff was 46 files, 1418 insertions and 54 deletions; after integration and
rebase it is 47 files, 1787 insertions and 65 deletions (before this ledger
update). No findings were declined.
