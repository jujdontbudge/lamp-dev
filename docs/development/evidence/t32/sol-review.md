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
passed. No findings were declined. Round 2 pending.

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
