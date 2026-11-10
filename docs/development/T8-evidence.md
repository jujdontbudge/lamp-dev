# T8 Mac hosts evidence (lanes M5, AIR)

Two Apple Silicon Macs, both the owner's everyday machines:

- **M5**: Mac17,2 (Apple M5), 24 GiB RAM, macOS 26.5.2 (Darwin 25.5.0),
  internal SSD, APFS.
- **AIR**: Mac14,2 (Apple M2), 8 GiB RAM, no fan, macOS 26.7.1 (25G241),
  internal SSD, APFS; reached as `ssh johansardars-macbook-air`.

Everything below ran on the real engine on these machines:

```
lamp-supervisor ctl (driver) -> lamp-supervisor serve (root, in the owner's login session)
  -> lamp-vz worker per sandbox, as its own hidden account _lampvmNN,
     under a deny-by-default Seatbelt profile (§3.2) -> Virtualization.framework VM
  -> guest helper over vsock (lamp-guest-helper / lamp-mac-guest-helper)
```

No dialog was shown and no password was asked on either Mac. Privileged steps
were `sudo -n`, the worker is signed ad hoc, and no keychain write and no
launchd bootstrap of system items were done. Failed and stopped runs stay on
record. All evidence is in [`evidence/t8/`](evidence/t8/).

Owner decisions on 2026-11-10 (the floors live in the coordinator's
test-budget PR, not here):

- E05 is ≥100 captures per row.
- At 21:30Z the owner replaced the trial counts with one end-to-end
  demonstration on each Mac. Speed is not a goal or a blocker.

The trial runs started before that decision are kept; they were stopped
cleanly between trials.

## Demonstration on each Mac (final binaries, commit `efbbad8`)

The commits after `efbbad8` contain clippy fixes and the merge of `main`
(T7). On macOS they change no behavior. The guest helper's refusal is now
boxed, so the provisioned guests carry the helper built from `efbbad8`:
`d854b148…` on the M5 and `90ebdd76…` on the AIR.

| | M5 | AIR |
| --- | --- | --- |
| **Linux guest, memory fork** ([M5](evidence/t8/M5-e05-demo-20261110T2147Z/), [AIR](evidence/t8/AIR-e05-demo-20261110T2222Z/)) | ok | ok |
| guest | Ubuntu 26.04 arm64, 768 MiB working set fully rewritten, ring log with fsync'd acks in flight | same |
| both sides continue, state | 196,608/196,608 pages valid on both; 0 lost acked, 0 torn, monotonic clock never back | same |
| identity / isolation | `_lampvm01` → `_lampvm02`, new machine id, entropy diverged, independent disk writes | same |
| clock skew after fork (source / fork) | 10 ms / 12 ms | 6 ms / 10 ms |
| fork (caller to useful command) / source pause | 1,114 ms / 558 ms | 969 ms / 503 ms |
| **macOS 26.6.2 guest, suspend + resume** ([M5](evidence/t8/M5-e06-20261110T2144Z/), [AIR](evidence/t8/AIR-e06-20261110T2148Z/)) | ok | ok |
| RAM-only process across suspend/resume | alive, counter 1 → 7 → 10 | alive, 1 → 8 → 11 |
| same boot session, same machine id, disk marker, hostname | yes | yes |
| suspend / resume to command | 8.3 s / 6.9 s | 42.0 s / 11.9 s (memory pressure) |
| two-per-Mac cap | two running; third `NO_CAPACITY`; retry after delete; 2-thread race has one winner; suspended holds its slot; Linux takes none | suspended A + B; third `NO_CAPACITY`; race one winner; Linux takes none |
| undersized (2048 MiB) | `INVALID_ARGUMENT` | `INVALID_ARGUMENT` |
| memory fork | refused, `DEVICE_NOT_FORKABLE` | refused, `DEVICE_NOT_FORKABLE` |
| **A2 disk-only fork** (labeled `fork_mode=disk_only`, `preserves_memory=false`) | new boot session, IOPlatformUUID, machine id and hostname; source disk marker copied; independent write | same |
| **Escape test** ([M5](evidence/t8/M5-escape-20261110T2148Z.json), [AIR](evidence/t8/AIR-escape-20261110T2222Z.json)) | pass: 24 worker-side probes, guest has only `lo`, TCP out fails, VM runs as `_lampvm01` | pass, same |

The macOS guest is built from the owner's own IPSW, macOS 26.6.2 (25G83),
sha256 `885503b7…dd51c95` (19,772,231,540 bytes; the IPSW was deleted after
install to save disk space). It was installed by `VZMacOSInstaller` through
`infra/mac/build-macos-guest.sh`. The same never-booted disk (sha256
`10bd2bbc…`) was copied to the Air and provisioned there with that machine's
own helper build. The AIR is the 8 GiB lane, so it runs one macOS guest
alongside a suspended one rather than two running.

The confinement inventory is published by `lamp-vz profile --spec`; see
`confinement` in each run's `metadata.json`. For macOS guests it adds only:

- the seven `com.apple.virtualization.extension.*` sandbox extensions that VZ
  hands its VM service (found with a reporting profile);
- `IOSurfaceRootUserClient`;
- the worker account's own Darwin cache directory;
- file-extension issue only on paths that are already granted.

No Mach service was added.

## Review fixes after the demonstrations (not re-run)

The PR #17 review found engine defects, which were fixed after the
demonstrations above. The owner rule allowed no new tests or trial runs, so
none of these was exercised on the Macs:

- **Crash-safe resume.** A resume first pins the suspended disks (APFS
  clones). The suspend record is removed only once the guest is back. A
  failed resume, or recovery after a crash during one, puts the pinned disks
  back under the saved RAM.
- **Local captures survive a restart.** A `consistency=local` capture
  commits a durable record. Recovery keeps committed local captures, and they
  are not discarded when their source goes away.
- **A failed capture restore gives its files back.** The files are re-sealed,
  so the capture can be restored again. It is marked available only if all
  its files are present.
- **A failed suspend after the save resumes the guest.** The flush or record
  write fails, the record and state are withdrawn, and the guest gets its
  clock barrier and thaw again.
- **The guest helper no longer waits on pipes a background job inherited.**
  It waits a 50 ms grace, then replies with what it read, marked truncated.
- **macOS creates refuse a disk size other than the template's.** The cloned
  guest disk is not resized.

## Earlier trial runs (kept as run)

| Run | Mac | Trials (measured) | Errors | Status |
| --- | --- | --- | --- | --- |
| [M5-e05-20261110T1848Z](evidence/t8/M5-e05-20261110T1848Z/) | M5 | 191 (186) | 187 clock | **failed**: the source clock fell about 0.4 s behind after each fork; fixed by re-applying the source's identity barrier |
| [M5-e05-20261110T1901Z](evidence/t8/M5-e05-20261110T1901Z/) | M5 | 648 (643) | 0, then restore refused | **failed**: aborted when the Mac locked (finding 2) |
| [M5-e05-20261110T2000Z](evidence/t8/M5-e05-20261110T2000Z/) | M5 | 772 (767) | 0 state/clock/isolation, 0 lost acked | stopped at the owner's revised floor |
| [AIR-e05-20261110T1919Z](evidence/t8/AIR-e05-20261110T1919Z/) | AIR | 216 (211) | 0 | stopped to move to the lock-aware build |
| [AIR-e05-20261110T2004Z](evidence/t8/AIR-e05-20261110T2004Z/) | AIR | 159 (154) | 0 state/clock/isolation, 0 lost acked | stopped at the owner's revised floor |
| [M5-e06-20261110T2111Z](evidence/t8/M5-e06-20261110T2111Z/) | M5 | 2 | 2 | **failed**: the helper's timeout killed the background jobs of finished commands, and resume applied a new machine id; both fixed |
| [M5-e06-20261110T2134Z](evidence/t8/M5-e06-20261110T2134Z/) | M5 | 1 | 1 | **failed**: the driver's counter used `nohup`, which exits 127 without a console on macOS; the driver was fixed |
| [M5-apfs-crash-20261110T1855Z](evidence/t8/M5-apfs-crash-20261110T1855Z/) | M5 | 16 cases | 0 | APFS durable-suspend process-crash smoke, `infra/mac/apfs-crash-test.py`: crashes before the metadata rename come back absent, and from the rename on they come back whole with the same RAM and disk hashes (T7 pattern, `benchmarks.oracles`) |

Run 2000Z on the M5 (767 forks) published p50/p95/p99/max fork times of
801/2,314/3,057/4,598 ms. The pause was 313/456/617/2,625 ms and the RSS was
1,162 MiB (fork) and 1,958 MiB (source). Forks slowed from about trial 400,
while the 21 GiB guest disk was being copied to the Air
(`STOPPED.md`). On the AIR, run 2004Z had a fork p50 of 4.5 s under swap
pressure. These are published as measured; there is no Mac speed target.

## Two platform findings that bound the VZ engine

1. **VZ needs the console user's login session.** From an ssh session on the
   Air, `saveMachineStateToURL` fails. As root it fails with "permission
   denied". Under a hidden account the VM crashes, with
   `VirtualMachineArchiveKeyManager -25316` in the log. The same binary works
   when started in the owner's session (`sudo -n launchctl asuser 501 …`), and
   the supervisor then drops each worker to its `_lampvmNN` account. A system
   LaunchDaemon, which has no login session, would fail the same way. The VZ
   engine therefore has to be hosted from the logged-in user's session, for
   example as a LaunchAgent handing off to root.
2. **VZ seals saved machine state with a Secure Enclave key of the console
   session.** Saving works with the screen locked. Restoring does not while
   the session is locked: the log shows
   `SecKeyCreateDecryptedDataWithParameters -25308 <sepk:p256(u)>`, and VZ
   reports "permission denied". This was reproduced with a bare `lamp-vz` save
   and restore. The engine reads the lock state from the console session
   (`ioreg -n Root -d1`, `CGSSessionScreenIsLocked`). While the screen is
   locked:
   - memory fork and resume are refused with `HOST_UNAVAILABLE`;
   - a Linux create cold-boots the template disk
     (`create_mode=cold_boot_console_locked`).

   Drivers wait for an unlock and count the waits. Keeping the Mac unlocked
   was ruled out: it would leave the owner's Mac open while they are away.
   On a locked Mac, VZ memory fork and suspend/resume are unavailable. This
   bounds the VZ adapter only. The HVF `msb_krun` path (T20) keeps its own
   state and is not affected.

## Deviation from §3.2: provisioning the macOS guest disk

`infra/mac/build-macos-guest.sh` mounts the guest's Data volume on the host
once, `nobrowse`, to install the guest helper, its LaunchDaemon and
`.AppleSetupDone`. It does this only on a disk written solely by Apple's
installer from the signed IPSW that has never booted (the `.never-booted`
marker; any other disk is refused). So no guest-controlled bytes reach the
host's APFS parser. Templates and sandboxes are reflinked clones; the
provisioned media disk itself never boots.

## Gaps (not claimed)

- APFS: a 16-case **process-crash** smoke with `F_FULLFSYNC`. It is not power
  loss or block reordering, and not yet against T7's storage-fixture
  protocol.
- The drivers are the engine's own `ctl`, not the T5 public harness through
  `lampd`. The control core does not run on Macs yet.
- VZ compresses saved RAM. The fully rewritten 768 MiB working set saves to
  about 36 MB, so incompressible memory would take longer.
- The worker is signed ad hoc. Developer ID signing, notarization and a
  per-user LaunchAgent install are left to packaging.
- The macOS escape test ran on a Linux guest's worker. The macOS guest
  worker's extra grants are listed above but were not probed separately.

## Reproduce

```sh
infra/mac/install-engine.sh 4                     # engine, worker accounts, store, RAM volume
lamp-supervisor template-prepare SEED ubuntu-26.04-arm64 --identities 1-12
infra/mac/build-macos-guest.sh IPSW VMDIR macos-26.6.2   # then template-import-macos
lamp-supervisor serve --identities 1-12 &         # from the owner's login session
lamp-supervisor ctl e05 ubuntu-26.04-arm64 768 1 0
lamp-supervisor ctl --store /private/var/lamp/store e06 macos-26.6.2 1 2 4096 48 [--two-running] --linux-template ubuntu-26.04-arm64
sudo -n env LAMP_GRANT_ID=… infra/mac/escape-test.py ubuntu-26.04-arm64 "$HOME"
```

State after this ticket:

- **M5**: the engine is installed and stopped. It keeps the Linux and macOS
  templates and the provisioned macOS media.
- **AIR**: the engine is installed and stopped. It keeps the Linux template;
  the macOS media and template copy were deleted for disk space.
