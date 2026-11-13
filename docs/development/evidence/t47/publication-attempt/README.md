# 0.1.2 publication attempt and pre-upload regression — 2026-11-13

The coordinator authorized publication after MR !18 merged as
`0556062ef77cb9ff0c5007c5eaaa40a04dccbca4`. All three platform archives and
three separate relay archives were built from that source, with refreshed
static guest init/helper/guard binaries and verified rootfs readbacks. The Mac
binaries were Developer ID signed and Apple notarization was accepted. No SDK
was built. The first candidate's [provenance](release.json), [checksums](SHA256SUMS)
and [build log](build-final.log) remain historical; **those unpublished assets
were discarded after finding the regression below.** They are not final assets.

## Completed external change

Public GitLab project 88830487 now has `builds_access_level=disabled` and
`shared_runners_enabled=false`. A fresh attached-runners read returned `[]`.
No private project/group runner remained attached. Branch protections were
inspected but not changed. No public source, asset, tag or release was pushed;
no Worker deployment or account DNS mutation occurred. The public release
service still serves 0.1.1 and the private account address remains unresolved.

The public export of merged source was redacted and checked, with zero remaining
private-detail or credential-pattern findings. It was never pushed and was
removed when the candidate was discarded. Publication must export the new
merged source and rerun those checks.

## Real candidate results

[Air candidate](air-candidate.log): signed install at a fresh disposable prefix
selected 512 MiB with everyday apps left running. It prepared a Linux template,
then `lamp sandbox run -- python3 ...` printed:

```text
signed release candidate
aarch64
42
```

The sandbox was deleted and the disposable install, engine and worker accounts
were removed by uninstall. The transcript records the successful uninstall.
This is a local signed-candidate result, **not a live-public-URL installation**.

[Linux arm64 failure](arm64-candidate-failed.log): the installer prepared and
published a real template in the existing `lamp-la` VM, but then refused its
stdout as invalid JSON. Its stdout began with ext4 recovery/summary diagnostics
before the valid template JSON. `resize_ext4` invoked `e2fsck` and `resize2fs`
with inherited stdout, contaminating `template-build`'s machine-readable output.
This is a product regression, not a failed JSON collector assertion or a seed
format problem. Cleaning the seed cannot suppress both tools' normal stdout.
The installer removed its failed managed engine; the incomplete disposable
prefix was removed separately. No Firecracker process remained.

The minimal fix sends both tools' stdout to stderr, preserving diagnostics and
their exit-status handling while reserving template-build stdout for its JSON.
It changes no filesystem operation or repair acceptance. The patched Linux
supervisor compiled from the merged source plus these four lines. A new signed
candidate reuses the same guest seed and other payload files, replacing only
the supervisor and regenerating signed manifests/index. Its installation and
code result are recorded separately below, never as a pass for the first run.

## Host constraints and remaining work

Ubuntu's fresh read reported 4887 MiB available, below the 12 GiB floor. Chrome
was left running and no Ubuntu sandbox was allocated. The requested Ubuntu
public install/version-only check awaits a published corrected artifact.

M5 free space started at 26 GiB. Nested Linux staging temporarily reduced it to
16.9 GiB, below the required 20 GiB floor; this was a staging-budget mistake.
Only this attempt's seed/build/candidate files were removed, and the existing
Lima guest trimmed its freed extents. Free space returned above 20 GiB before
further staging. No everyday service, existing Lamp installation, Docker
resource, visible app, owner repository, vault or browser profile was changed.
The successful Air candidate's large staging was also removed.

Final platform artifacts must be rebuilt from the coordinator's merge of this
fix. Upload each asset once; do not reuse the discarded candidate's checksums
or provenance as if they identify the corrected release. Public source/asset
publication, Worker/account/DNS deployment, live fresh installations on M5/Air/
Linux arm64, Ubuntu version-only install and one-time release measurements all
remain pending. No new release speed or complete zero-leak claim is made here.

## Patched candidate result

[Patched Linux arm64 candidate](arm64-candidate-fixed.log) completed the same
signed installation with the same guest seed and printed `signed release
candidate`, `aarch64`, and `42` through a real VM. The installed daemon reported
0.1.2. Uninstall deleted the sandbox, prefix and managed engine; zero
Firecracker processes remained. Filesystem diagnostics are still present in
the log, but no longer contaminate the template JSON the installer parses.
This is a separate passing candidate, not a rerun relabeling the original failure.

`make build` passed ([log](fix-build.log)), including Go, generated SDK
consistency and the locked Rust workspace. The patched Linux supervisor also
built in release mode on Linux arm64; targeted rustfmt and diff checks passed.
No new test was added: actual signed install/code is the regression proof.

The second nested installation also temporarily took host free space below
20 GiB despite removing prior build/seed copies. Its managed engine was removed
by uninstall; trimming the freed Lima extents restored host space. Future
nested staging must account for its observed roughly 6 GiB peak above retained
inputs before allocating; merely starting above 20 GiB is insufficient. No
further VM/build allocations will be attempted in this handoff.

All attempt-owned candidate archives, source exports, compiler targets, seeds,
installation stores and remote staging directories were removed after retaining
these logs. Host free-space accounting settled above 20 GiB after trimming.
Public CI/shared runners remain disabled as authorized. The next publication
must budget for the full nested-install peak, not only archive/build storage.

## Follow-up Sol review

Codex GPT-6.1 Sol on medium effort approved behavior head
`6270a4bb49df1dc4a1f713d8e5a9c3a66a56042a` in one round. No blocker or should
findings; no code fixes or tests requested. Sol reproduced the recorded JSON
parsing failure and checked the separate successful install/code/uninstall
transcript. See [review ledger](sol-review.md).

Sol found one evidence whitespace discrepancy: the full base-to-head
`git diff --check` flagged a trailing blank line in the original failure log.
That blank line was removed without changing the failure's output. The full
range check passes after this correction. Earlier working-tree diff checks
alone did not cover that committed log; they were insufficient for this check.
