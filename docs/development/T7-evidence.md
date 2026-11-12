# T7 storage and capture evidence (lane LX)

Lane LX is the owner's Ubuntu laptop: Intel Core Ultra 9 288V, 31 GiB RAM,
Linux 7.0.0-42-generic, Ubuntu 26.04.1, mke2fs 1.47.2, mkfs.xfs 6.18.0,
btrfs-progs 6.17.1. Every run below drives the real engine end to end:

```
independent harness (benchmarks/, E04 oracle)
  -> infra/linux/e04/fixture.py (root; disposable volumes only)
     -> lamp-supervisor (this tree, --features fault-injection)
        -> jailer + Firecracker microVM, frozen bench workload in the guest
```

The thresholds are the fixed constants in `benchmarks/protocol.py`. Every run
directory is kept, including the stopped one.

**Result:**

- ext4 passed E04 at 1,000 forks. The owner's decision of 2026-11-10 makes
  that the binding Linux run.
- XFS-reflink passed at 100.
- btrfs has a 30-scenario smoke run only. On 21:30Z the owner stopped all
  further trials.

## What one scenario does

The harness picks the nonce, the crash point and side, the fault (`discard` or
`reorder`) and, for controls, an omitted barrier or the unauthorized producer.
The fixture then does the following (all calls go to the supervisor's public
`lamp.worker/1` API through `lamp-supervisor ctl`, plus its admin socket for
`state` and fault arming):

1. **Volumes.** Snapshot the lane's golden volume and put it under
   `dm-log-writes`. Mount it and start a supervisor on it. The volume stack is
   tmpfs file → loop → dm-thin pool → thin snapshot → dm-log-writes →
   ext4|XFS|btrfs.
2. **Generation 0.** Create sandbox A from the 512 MiB `e04` template. Start
   the workload's resident process, which holds 8 KiB of pattern RAM and an
   fsynced 16 KiB file. Make the durable (`local`) capture `cp0`. The fixture
   acks generation 0 to the harness, and the harness fsyncs its own history
   before it releases the ack.
3. **The fork.** Capture `cpx` (deleted later). Create B from `cp0` while A
   still runs, then release A. In B, write generation 1 to RAM and disk.
4. **The crash.** Arm the fault, then capture `cp1` from B and ack it if the
   capture succeeds. Run maintenance: delete `cpx`, gc, compact `cp1`
   (rotation and compaction). The armed point ends the supervisor with
   `_exit(79)`. The fixture then writes the `crash` mark into the block log.
5. **Power cut.** Replay the log onto a fresh snapshot (`logwrites.py`).
   Writes before the last FLUSH ahead of the mark are durable, and so are
   FUA writes. All other writes are dropped (`discard`) or applied as a
   random half in random order (`reorder`).
6. **Recovery.** Mount the replay and start a new supervisor on it. Restore
   every committed capture into a sandbox, and read RAM and disk back from
   the guest. The harness checks the reported digests against digests it
   computes from its own nonce. It also checks what the store lists, and
   which generations were acknowledged.

The fixture keeps every call in `transcript.jsonl`, every block-log entry with
its fate in `barrier-trace.txt.gz` (d durable, x dropped, v applied out of
order, a after the mark, m non-write), and both supervisor logs. The harness
re-hashes these files and requires the crash mark and the public calls
(`benchmarks/storage.py`, `qualification_evidence`).

So each scenario is also the end-to-end demonstration of the durable fork
and of suspend/resume on ext4:

- **Durable fork.** B is restored from a durable capture while its source
  keeps running.
- **Suspend/resume.** A suspend is a durable capture followed by release.
  Resume is a restore after a supervisor crash and restart. The live
  resident process comes back with byte-identical RAM and disk.
- **No reformatting.** On ext4, Lamp's own dm-thin backend (two sparse loop
  files inside the store) is used on the filesystem as it is.

## Runs

| Run | Filesystem / backend | Scenarios | Result | Window (UTC) |
| --- | --- | --- | --- | --- |
| [`full1-ext4`](evidence/t7/full1-ext4/) | ext4 / dm-thin | 1,000 + 3 controls + 1 unauthorized | **pass** | 19:33–20:36 |
| [`x100-xfs`](evidence/t7/x100-xfs/) | XFS `reflink=1` / reflink | 100 + 3 + 1 | **pass** (see note) | 21:17–21:22 |
| [`smoke3-btrfs`](evidence/t7/smoke3-btrfs/) | btrfs / reflink | 30 + 3 + 1 | smoke only: `incomplete` by design | 19:30–19:32 |
| [`full1-xfs`](evidence/t7/full1-xfs/) | XFS / reflink | 355 recorded | stopped, no summary | 20:36–21:09 |

`full1-ext4`:

- All 30 boundaries (`reserve` … `compact_gc`, before and after each) were
  covered, with 500 `discard` and 500 `reorder` scenarios.
- The oracle found zero failures: no mixed generation, no cross-fork write, no
  lost ancestor, no lost acknowledged byte.
- Generation 1 was recovered in 495 scenarios. In the other 505, it was
  correctly absent, because the crash came before its ack.
- The three omitted-barrier controls each fail with "lost acknowledged object
  1", so each is caught.
  - Without `object_flush` or `directory_flush`, recovery marks `cp1`
    damaged; the sample artifacts are `99999-1e1a…` and `99999-e650…`.
  - Without `journal_flush`, the commit record is gone (`99999-eadd…`).
- A validly hashed capture signed by a never-enrolled producer is refused on
  import with `HOST_NOT_TRUSTED` ("produced by pk_46d5…, which is not an
  enrolled producer"). Only generation 0 exists afterwards (`99999-dd8a…`).
- Observation chain: 2,374 entries, last hash `80b9708f…ca255efb1`. The
  digests of the raw observation files are in `evidence/t7/observations.sha256`.

`x100-xfs` has the same coverage and results at 100 scenarios (last hash
`adaf4bdd…7048d4`). It ran under `STORAGE_FORKS = 100`, after the owner
decision of 20:50Z. That decision was then withdrawn at 21:30Z, before this
PR, and the constant in this tree stays at 1,000. Judged under that constant,
the same observations would read `incomplete` (100 < 1,000), not `fail`.

`smoke3-btrfs` covered all 30 boundaries, caught all three controls and
rejected the unauthorized capture, with zero failures. Smoke runs can never
pass a gate.

`full1-xfs` was stopped on purpose after the 20:50Z decision; see its
[`STOPPED.txt`](evidence/t7/full1-xfs/STOPPED.txt). Its 355 recorded
recoveries have zero oracle errors. The stop was not clean, and these
iterations count as failed in that run:

- The harness ignored SIGINT (a background job of a non-interactive shell).
- I then ended the next fixture while it waited, before it had started. The
  harness counted that iteration as failed and went on. Its next iterations
  failed against the lock I held, and I ended the harness with SIGTERM, so
  the run has no summary.
- The orphaned fixture processes all refused to run, because they found no
  disposable marker (`orphans-refused.log.gz`), so none touched a volume.

Sample per-scenario artifacts, kept for the ext4 run: the three controls, the
unauthorized case, `journal_flush:before` and `compact_redirect:after`. The
full 1,004 ext4 artifact directories (45 MiB) stay on LX in
`/var/lib/lamp-t7/e04-artifacts/full1/ext4`, next to the run directories in
`/var/lib/lamp-t7/e04-runs/`.

## What the reorder fault did and did not exercise

Across every run, `volatile_applied` and `volatile_dropped` are 0. At each
boundary the device's volatile cache was already empty, because the last
FLUSH or FUA came one or two entries before the crash mark:

- On ext4, the seal's dm-thin metadata commit and the loop-file fsyncs flush.
- On XFS and btrfs, the filesystem's own log commits flush.

So for these boundary crashes, `discard` and `reorder` produced the same
image. The randomized reordering never had a write to reorder. What the runs
do exercise is data never written back from the page cache, which is lost.
That is how each omitted barrier loses generation 1. Crashes in the middle of
a write burst, between flushes, are not covered by these runs.

## Engine changes behind it

`crates/lamp-engine/README.md` ("Durable captures") has the design:

- **Commit record.** It carries the whole signed manifest and is written into
  preallocated journal blocks. Its fdatasync (`journal_flush`) therefore syncs
  no filesystem metadata, and a later journal commit cannot mask a missing
  `directory_flush`.
- **btrfs.** `object_flush` ends with `syncfs`. Without it,
  `btrfs_log_new_name` persisted already-logged renames on any later fsync,
  and an omitted `directory_flush` went undetected on btrfs during
  development.
- **Memory deltas.** They are written with O_DIRECT from the tmpfs image, with
  the digest computed in memory. An omitted `object_flush` writes them
  buffered, so the omission is visible.
- **Template size.** Template definitions may set `memory_mib`, and the boot
  probe uses it. `make-bench.py SEED e04 512` builds the 512 MiB E04
  template. The oracle's pattern is 8 KiB of RAM, and a 4 GiB guest pinned
  4 GiB of host RAM per capture image.
- **Supervisor flags.**
  - `--trust-producer` lets the rogue host use the shared template.
  - `--mem-dir-size` and `--no-capture-spare` bound host memory.
  - Admin status reports the producer key.

## Harness changes (T13's files)

- `benchmarks/storage.py` (`qualification_evidence`): a real fixture's
  `real-filesystem-block-barrier` claim passes only when the harness can
  re-hash the kept transcript and block trace, and finds the crash mark and
  public calls in them. Otherwise the run is `incomplete`.
- The test for it: `tests/benchmarks/test_harness.py`,
  `test_real_e04_passes_only_with_traces_the_harness_can_check`.
- No threshold changed.

## Deviations and incidents

- **No lampd.** The fixture uses the supervisor's public worker API, not
  lampd, because it has to start, crash and restart supervisors on
  disposable stores. lampd has no such store. No private RPC was added to the
  harness.
- **Metadata fields.** The §22 metadata's generic `filesystem.guest_disk_store`
  describes the installed `/var/lib/lamp` store. The volumes E04 actually used
  are in `e04_volume` (`infra/linux/e04/metadata.py`).
- **Timing lock.** `full1-ext4` started before the shared LX timing lock
  existed. Every later lane took `/tmp/lamp-lx-timing.lock` for its own run
  (`run-lane.sh`).
- **Two OOMs on 2026-11-10, both caused by this ticket.**
  - Leaked 4 GiB sandboxes got the owner's Chrome service OOM-killed. systemd
    restarted it.
  - At 18:57Z, a 12 GiB E04 RAM disk plus capture images got gnome-shell
    killed.
  - Since then the fixture refuses a scenario below 12 GiB + 4 GiB available,
    and a watchdog stops it below 12 GiB. RAM disks are 3 GiB, discarded
    volumes return their RAM, and teardown unmounts them after every run.
  - The seat0 session and `owner-app-dm-chrome` are active again
    (`/json/version` answers). This ticket stopped no owner service.
- **Durable fork speed.** It is no longer a goal (owner, 2026-11-10). In
  development on an ext4/dm-thin store (diagnostic `ctl bench-fork`, not a
  harness run), local forks took about 0.6–0.85 s at 0 and 128 MiB dirty, and
  1.2–5 s at 1 GiB dirty, depending on the NVMe write cache.

## Reproduce

```sh
cargo build --release -p lamp-supervisor --features fault-injection   # on LX
images/templates/make-bench.py "$SEED_REF" e04 512 > e04.lamp          # template-build in a reflink store
E04_BINARY=... E04_TEMPLATE_STORE=... infra/linux/e04/run-lane.sh ext4 NAME
infra/linux/e04/summarize.py /var/lib/lamp-t7/e04-runs/NAME-ext4
```
