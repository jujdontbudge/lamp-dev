# T32 Sol review ledger

Reviewer: Codex GPT-6.1 Sol, medium effort, separate T3 checkout. Round 1
reviewed `30d6893ff857462dfd9c93268f6807f852d5427f` against
`bcd54f76f9827aa5707716b4ef79fa7e1bf86198`. Verdict: CHANGES.
Token usage was not returned.

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: reject installed-template deletion before saved-template mutation | Reviewer's authenticated HTTP DELETE regression reproduced the nil dereference and retained mutex before the fix; passes afterward. |
| S2 | should | Fixed: resolve the installed default before lifecycle request rewriting | Reviewer's HTTP create with an omitted template and lifecycle override reproduced HTTP 400 before the fix; passes afterward. |

The reviewer's two focused regressions are retained in
`internal/control/t32_review_test.go`. They were each reproduced failing, then
passed together after the six-line product fix. Targeted Go vet and `make build`
passed. No findings were declined.

Before round 2, requested platform demonstrations exposed two additional
implementation bugs: failed Mac setup left accounts/files behind, and SSH
started VZ outside the console login context required for saved-state keys.
Cleanup was fixed and demonstrated with a disposable group collision; VZ
engine processes now adopt the login context using `launchctl asuser`, with
no system launch item bootstrap. The owner also requested a default that fits
the 8 GB Air with everyday apps open: the installer now selects its memory
from the engine's observed host memory while retaining the existing physical
reserve and admission checks. These behavior changes are included in round 2.

The required rebase included T38 (`8150e50`) and T33 (`03944fb`). Conflicts
retained fleet imports, plugin-launcher packaging and T33's validation of
explicit process limits. T33 moves worker capacity probing before admission,
so S2's fixture (deliberately using `/unused` as its worker socket) now expects
that specific HTTP 503 boundary instead of asynchronous acceptance. With the
S2 product fix temporarily removed it still fails with HTTP 400 and the
missing-template error; restored, both regressions pass. No test was added.

T33 also added a fixed 2048 MiB deduction from available pages. To retain the
owner-requested busy-Air behavior, the combined policy keeps the original
physical reserve and reservation accounting, but sets additional available
headroom to one quarter of available memory, at least 256 MiB and capped at
the physical reserve. The installer uses that exact reported budget. This
policy change and the combined-platform demonstrations are included in round 2.

Round 2 reviewed `2bed08f407d2425e231f88a599da44f5912add6c` against
`03944fbdc30e28f1062700af7ceca4d736258a89`. Verdict: APPROVE. S1 and S2
resolved; no new blocker or should findings. The reviewer ran only the two
retained HTTP regressions; both passed. It reviewed cleanup, login-context
adoption and adaptive memory, and found no admission bypass. No privileged
actions, VMs or checkout edits. Token usage was not returned.

Two rounds with GPT-6.1 Sol on medium effort. Two bugs fixed, two reviewer
regressions retained, no findings declined. Initial diff: 34 files, 1472
insertions and 290 deletions; reviewed final diff: 50 files, 2118 insertions
and 295 deletions. Growth includes the requested platform fixes and preserved
demonstration evidence. Approval covers the reviewed behavior at the recorded head. Coordinator merges.


Final integration after round 2: rebased onto T41 (`c34fabd`) and then the
latest main containing T39 (`f9a3989`). Kept T41 human help, connection flags,
wait commands and revision fetching, and T39 preview aliases and networking.
CLI/MCP credential discovery still uses the owning install and does not send
its implicit bearer to an explicit remote endpoint. Resource shorthand overlays
only supplied dimensions onto the installed default, preserving the Air's
512 MiB selection. Human create help describes adaptive memory.

After this final rebase, `make build` passed (C launcher, all Go packages,
332-operation generator check, locked Rust workspace). Targeted `go vet`
passed. Existing CLI header and T41 review regressions plus both retained T32
HTTP regressions passed. `git diff --check` passed. Built CLI/MCP help was
inspected; a disposable HTTP transport fixture verified omitted-template
create and `--vcpus 1` both preserve the discovered 512 MiB memory and 60000 ms
wait. This fixture is not VM evidence. The prior signed install demonstrations
remain the platform evidence; no repeat VM installs or privileged actions were
needed for this rebase. No third Sol review was run.
