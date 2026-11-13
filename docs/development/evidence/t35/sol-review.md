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

Follow-up Sol review: pending, new integration behavior only.
