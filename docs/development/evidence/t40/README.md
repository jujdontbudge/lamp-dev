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
as documented in `docs/templates-and-storage.md`. The initial run had only implementation/build coverage for Linux guests on
Apple Silicon; the M5 follow-up below adds the real runtime demonstration.
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

## M5 completion and final Linux rerun

Rebased onto main `43a941e` (T32/T39, plus T41/T33/T38). Conflicts preserve
T32 installed/adaptive defaults and T39 network policy behavior. The combined
worker schema keeps network_call at field 20 and assigns template/storage_mount
fields 21/22; both bindings were regenerated. T32's explicit template/recipe
selections keep their own defaults.

`final-build-demo.log`, `final-one.log`, `final-two.log`, `final-three.log`,
and `final-pool.log` are a fresh Ubuntu x86_64 runtime demonstration on that
rebased source. They repeat public six template build, 100 GiB guest root,
recipe defaults, concurrent volumes/caches, held-directory rename, selected
save/source deletion/reuse, restart recovery, MCP attachment, backup/delete/
restore/read-only mount, and two warm claims with refill. All phases completed;
all demonstration VMs were deleted and the own supervisor/daemon stopped.

The M5 run uses Linux arm64 guests in confined VZ workers, an entirely separate
engine/control store, and a disposable seed with 9p fd transport. Public six
1.17.0, pip 25.3, setuptools 80.9.0 and wheel 0.45.1 artifacts were verified
against PyPI SHA-256 metadata and provisioned before boot. No guest networking
was enabled. These are local observations, not release benchmark results.

| Observation | Evidence |
| --- | --- |
| Public Mac template build with visible logs and flexible API metadata | `mac-public-build.log`: public CLI creates a ready arm64 template; minimum 1 CPU/256 MiB/20 GiB. Creates below use this API template ID, not an engine-only reference. |
| Requested Mac resources apply | `mac-runtime.log`: request 4/1536/24, guest 4 CPUs and 24G root; request 1/512/21, guest 1 CPU, 484 MiB visible and 21G root. A subsequent omitted-resources create uses the template's 2/1024/20 defaults. |
| ST02: volume really shared and persistent | `mac-runtime.log`: real 9p entries for `/mnt/mvol` and `/mnt/mcache`; B reads A's write, A reads B's append. Both producers are deleted; a new guest reads both lines. Daemon restart reconnects before ready and the same lines remain. |
| ST01: real package cache reuse | `mac-runtime.log`: pip builds six from the provisioned source archive on A and stores its wheel in the shared cache; B reports `Using cached six-1.17.0-py2.py3-none-any.whl`. Observed installation calls: 6.718 s then 1.771 s. The package imports successfully, and the wheel remains after producer deletion. |

Mac runtime exposed and corrected three bugs after the first code review:
recovery reset the staging parent to 0700, blocking build-worker kernel access;
resize2fs tried a writable open forbidden by CONFIG_BLK_DEV_WRITE_MOUNTED=n;
and 9p reported Linux task UID 65537 while VZ commands run as root, disabling
pip's cache. Recovery now preserves 0711 traversal (without directory listing),
growth uses the ext4 resize ioctl on the mounted root with a read-only device
size query, and Mac guest-visible ownership is root without changing host
ownership. The Linux task ownership remains 65537.

Historical failures remain in `mac-staging-failure.log`,
`mac-resize2fs-failure.log`, and `mac-runtime.log`. The latter also retains the
initial disabled-cache result and an incorrect direct-archive pip invocation
that rebuilt instead of consulting its cache; the final run resolves six by
package name from a local source directory, allowing pip to use its wheel
cache. None of the failed attempts is relabeled as passing.

All M5 demonstration sandboxes were deleted, the own daemon and supervisor
stopped, and the disposable Mac store/seed and Lima build directory removed.
Existing _lampvm15/16 accounts were reused; no accounts were created or deleted.
The existing Lamp installation, Docker resources and desktop were untouched.
The owner explicitly authorized scoped noninteractive sudo for this work.

Mac-only Sol review with Codex gpt-6.1-sol on medium: round 1 found S1 blocker,
API build receipts still fixed resource shapes; changed fixed_resources to
false. Round 2 approved that fix. The public build and alternate-shape creates
above prove the corrected caller path. No tests were supplied or added for that
review; usage was not exposed. A narrowly scoped additional review covers the
three real runtime fixes above because intended Mac behavior was still broken.

Final local checks: `make build`, `go vet ./cmd/lamp ./internal/control`, the
three retained T40 reviewer tests and three existing CLI/T41 tests pass. No new
implementation tests were added for the Mac changes; the proof is the real
M5 run. Native macOS guest filesystem mounts and online build networking remain
outside this Linux-on-Mac demonstration.
