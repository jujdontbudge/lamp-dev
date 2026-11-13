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

Round 2: pending, fixes and open findings only.
