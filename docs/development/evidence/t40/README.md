# T40 implementation evidence — 2026-11-12

Real public CLI, HTTP and stdio MCP calls against a disposable Ubuntu x86_64
Firecracker engine and private lampd store. The seed was a disposable copy of
Ubuntu 26.04 with the newly built guest helpers and a Linux 6.18.62 kernel with
9p fd transport enabled. The public six 1.17.0 wheel was downloaded from PyPI,
checked against its published SHA-256, and its source provisioned in that seed.
No owner repository, account, browser profile, Docker resource or live disk was
used. VM creation waited for fewer than two shared Firecracker processes and
at least 12 GiB available RAM. Every demonstration sandbox was deleted.

The transcripts preserve failed iterations. They are observations of this
implementation session, not release benchmarks or a platform certification.

| Observation | Actual evidence |
| --- | --- |
| Public `lamp template build FILE` uploads, builds and prints stages | `template-build.log`: installed public six 1.17.0, captured and published a ready template. The transcript also contains stale progress from the preceding failed build; build dispatch now clears that old log before starting. |
| Engine templates appear in public inventory | `resources-and-storage.log`: engine ID `tp_42736e322660f095381c2397a6`, Linux/x86_64, defaults 2/1024/20, plus the public build resource. |
| Requested disk and resources reach the guest | `resources-and-storage.log`: request 2/1024/100; `nproc` = 2, `free -m` total = 983, `df -h /` = 99G, six = 1.17.0. |
| Recipe and template defaults apply | `shared-storage-and-restart-failure.log`: recipe 2/1024/20 creates without resources; direct template create uses the same defaults. |
| Volumes and caches really mount | `resources-and-storage.log`: ready attachments, actual 9p entry in `/proc/mounts`, writes/appends, and cached six.py SHA-256 `c51c91f703d3d4b3696c923cb5fec213e05e75d9215393befac7f2fa6a3904df`. |
| Two guests see each other's writes | `shared-storage-and-restart-failure.log`: B writes `live-from-B`, C appends `live-from-C`, B reads both. A held directory fid survives a rename by the other guest. Deleting the attached volume names the blocking sandbox. |
| Producer deletion preserves volume and cache data | The first producer is deleted in `resources-and-storage.log`; later guests read its file and reuse the cached six module in `shared-storage-and-restart-failure.log`. No installation-speed comparison was measured. |
| Daemon restart reconnects mounts before readiness | The first restart exposed an EIO window, retained in `shared-storage-and-restart-failure.log`. Reconnection was moved into readiness reconciliation. `saved-template-and-recovery.log` then waits for ready and successfully reads/appends after restart. |
| Selected-path saves are reusable after source deletion and restart | `saved-template-and-recovery.log`: CLI create with `--template` and `--volume`, followed by another fresh create from the same saved template; both import six from selected `/opt/tools`. |
| Backups restore to real read-only mounts | `saved-template-and-recovery.log`: back up the volume, delete its backing data through the public API, restore, attach read-only, read the previous writes, and observe `Read-only file system` on write. |
| All public interfaces create storage | HTTP volume/cache creates in `resources-and-storage.log`; MCP volume/cache creates and MCP attachment in `saved-template-and-recovery.log`; CLI volume/cache creates in `cli-storage.log`. |
| Warm pools refill and preserve saved-template files | `warm-pool.log`: ready_count 1, first claim, replacement ready_count 1, second claim; both import selected six 1.17.0. Target set to zero before the second claim to avoid an unnecessary third VM. Pool and both guests deleted. |
| Invalid definitions and resources fail clearly | `docker-refusal.log`: `INVALID_ARGUMENT`, Docker only available as a kit; `cli-storage.log`: impossible CPU request rejected before operation acceptance. |

During iteration, public builds exposed a missing CLI upload chunk digest,
reflink-only input copying on an ext4/dm-thin host, and ext4 growth requiring
journal recovery. Those were fixed before the successful build and 100 GiB run.
The seed preparation initially targeted `/sbin`, a symlink that debugfs does not
follow; preparation was corrected to `/usr/sbin` before any successful VM run.

Native macOS mounts and online template-build networking remain unsupported,
as documented in `docs/templates-and-storage.md`. Linux guests on Apple Silicon
have implementation/build coverage here, not a claimed runtime demonstration.
No SDK work or benchmark suite was added.

## Sol review ledger

| ID | Severity | Outcome | Proof |
| --- | --- | --- | --- |
| S1 | should | Fixed: shared attachment registry updates fids on rename. | Reviewer's failing test now passes; real held directory across two guests also succeeds. |
| S2 | should | Fixed: reopening clears interrupted build activity and records an honest failed/unknown outcome, allowing a new retry. | Reviewer's store-reopen test fails before and passes after. |
| S3 | should | Fixed: restored trees populate a confined backing directory before mount, including read-only mounts. | Real backup/delete/restore/read-only attachment in recovery transcript. |
| S4 | should | Fixed: pool refill retains the recipe's saved-template reference so archive restoration runs. | Two real warm claims retain the selected module. |

The two round-1 reviewer-supplied regression tests were retained. `sol-before.log` records
their failures; `sol-after.log` records their passing run. Review used
Codex `gpt-6.1-sol`, medium effort; token usage was not supplied.

## Integration with T33 and T38

Rebased onto `03944fbdc30e28f1062700af7ceca4d736258a89` (including T38
`8150e50`). The capacity-accounting conflict keeps T33's implementation;
README retains both tickets' guides. Worker bindings were regenerated from the
combined protocol. Saved-template admission now sends the underlying engine
reference to T33's validation, while retaining the public template for archive
restoration. Recovery coalesces startup and reconnect probes before remounting.

After integration: `make build`, `go vet ./cmd/lamp ./internal/control` and the
two reviewer regression tests passed. The complete demonstration above predates
the rebase; `rebased-recovery.log` records the focused integrated runtime check.

## Sol round 2 and final delivery

Round 2 reviewed `a00305c6139d9178e5d5fc229317e3e3666868ab` and confirmed
S1–S4 resolved. It returned CHANGES for S5 (should): reattaching after a guest
emptied a backing directory restored stale snapshot files and replaced the
open root held by the first attachment. The reviewer supplied one focused
filesystem reproduction; it failed locally before the fix with all three
reported failures, then passed after the fix. Population now treats an existing
backing directory as initialized, preserving empty contents and its inode.
This proof used actual filesystem root handles without creating more VMs.

Three reviewer tests total were retained. No findings were declined. The review
budget ended after two rounds with Codex `gpt-6.1-sol` on medium; usage was not
exposed. **Final Sol verdict remains CHANGES; all five findings are fixed, but
the S5 fix has not been re-reviewed.** This is not reviewer approval.

After S5: `go test ./internal/control -run
'^TestT40SecondAttachmentPreservesEmptiedBackingRoot$' -count=1 -v` passed;
`go vet ./cmd/lamp ./internal/control` and `make build` passed. A final fetch
confirmed main remained at `03944fbdc30e28f1062700af7ceca4d736258a89`.
The runtime demonstration result remains the successful runs documented above;
no fresh VM demonstration is claimed for the final S5 change.

## T41 integration

Rebased again onto main `c34fabd` (T41). CLI conflicts retain T41 wait commands,
automatic revision lookup, human/error output and conflicting-resource flag
validation, together with T40 template-file builds and storage attachment
creation. Omitted resources remain omitted so recipe/template defaults apply.
HTTP retains T41 field-specific validation and T40 engine-template checks.

`make build`, `go vet ./cmd/lamp ./internal/control`, the three existing
`cmd/lamp` canonical-header/T41 reviewer tests, and all three T40 reviewer tests
passed after this integration. No tests were added for the rebase. The runtime
demonstration above was not repeated after T41; Sol's recorded verdict remains
CHANGES with all findings fixed and the final storage fix not re-reviewed.
