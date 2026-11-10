# T2 Linux engine evidence

2026-11-09, native LX (Ubuntu 26.04.1, kernel 7.0.0-42, x86_64, 8 threads,
31 GiB RAM). Everything below ran on the real engine: Firecracker 1.17.0
microVMs under the jailer, the Debian 13 guest built here, driven over
`lamp.worker/1` by the `lamp-supervisor ctl` client and the e2e test exactly as
`lampd` would drive it. Nothing is mocked.

The owner's services were **not** stopped: their Chrome service and Beeper kept
running throughout. During the run the host had 18.9 GiB in use, swap 8 GiB of 8
full, load average 4–12 ([host.txt](evidence/t2/host.txt)). So these are
busy-host numbers; a quiet-host run was not needed to meet the targets.

## Result

Standard sandbox B1: 2 vCPU, 4 GiB, 20 GiB. The timer starts before the caller
sends its first request and stops when the command's result (create) or
both sides' commands (fork) are back.

| Row | Trials (+warmup) | p50 | p95 | p99 | max | Target | Failures |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Create → command ran | 200 (+20) | 35.4 ms | 45.4 ms | 50.4 ms | 65.5 ms | p50 < 500 ms ✅ | 0 |
| Fork, 0 MiB recently written | 100 (+5) | 124 ms | 217 ms | 237 ms | 280 ms | every < 1000 ms ✅ | 0 |
| Fork, 128 MiB recently written | 100 (+5) | 132 ms | 188 ms | 326 ms | 348 ms | every < 1000 ms ✅ | 0 |
| Fork, 1 GiB recently written | 100 (+5) | 351 ms | 404 ms | 465 ms | 479 ms | every < 1000 ms ✅ | 0 |
| Fork, 1 GiB, `durability=local` | 30 (+3) | 8.7 s | 14.5 s | — | 15.0 s | every < 1000 ms ❌ | 0 errors, 29 slow |

Raw per-trial records: [create-200.jsonl](evidence/t2/create-200.jsonl),
[fork-0m](evidence/t2/fork-0m.jsonl), [fork-128m](evidence/t2/fork-128m.jsonl),
[fork-1g](evidence/t2/fork-1g.jsonl), [fork-1g-local](evidence/t2/fork-1g-local.jsonl);
summaries in the matching `.err` files; run log [bench.log](evidence/t2/bench.log).
No trial is filtered out; warmups are recorded with `"warmup":true`.

Each fork trial checks, in the real guests, before it counts as a success:

- **Memory preserved (sampled).** Each dirty row rewrites a RAM-only marker on
  every 4 KiB page of its region before each trial; the marker never touches
  the disk. A full page scan takes seconds and would disturb the next trial,
  so the scan runs on every warmup, every 10th trial and the last one: 11 of
  the 100 measured trials in each dirty row, 4 of 30 in the local row, and
  none in the 0 MiB row, which has no region. Every scan found 0 bad pages in
  both source and fork, with equal generation and content digest in the two
  (`checks.source_ram` and `checks.fork_ram` in the records). The checks below
  run on every trial.
- **Both keep running.** The source runs a command after the fork, and so does
  the fork.
- **Disk private.** A file written in the fork after the fork is not visible in
  the source.
- **Fresh identity.** The machine IDs differ.
- **Entropy diverged.** 32 bytes from `/dev/urandom` differ between the two.
- **Clock correct.** The fork's clock is within 1 s of the host clock.

The fork time includes the source pause, the capture, the child's restore, the
identity barrier and a command in each guest. Source pause on its own: p50
287 ms and max 406 ms at 1 GiB. Per-stage timings are in each record
(`capture.*_us`, `restore.*_us`).

**`durability=local` misses the target, and it is the hardware.** A local fork
must fsync about 1 GiB to stable storage before it reports. This laptop's disk
sustained 35–140 MB/s for direct, fsynced 1 GiB writes, on both the Lamp store
and the root filesystem. That is at least about 7 s for the data alone, while
under 1000 ms needs at least about 1.1 GiB/s. The source pause is unaffected
(p50 262 ms): only the durable copy is slow, and it is never reported early.
Per the spec, insufficient owned storage bandwidth is a visible failure of
standard mode. It is recorded here, not hidden. `lampd` (T3) admits only
volatile forks.

### End-to-end test

`crates/lamp-supervisor/tests/e2e.rs`, run against the live supervisor:
[e2e.log](evidence/t2/e2e.log).

1. **Create:** Allocate + Restore(template), then the receipt `restored`. A
   command then reports `Linux`, the Debian trixie release and its own
   hostname, 53 ms from the first request.
2. A 64 MiB RAM marker process starts, and a file is written to disk.
3. **Memory fork:** Capture with `consistency=crash` and a `capture_…` ID, as T3
   sends it. Then the child Allocate + Restore. Commands run on both sides
   (149 ms).
4. The RAM marker is intact in both. The disk file is shared before the fork,
   and writes after it are private. Machine ID, hostname and entropy differ.
5. Authority:
   - A wrong generation gets `REVISION_CONFLICT`, refused before any guest effect.
   - A retried operation returns the earlier receipt; the command is not run again.
   - The same operation ID with another command gets `IDEMPOTENCY_CONFLICT`.
   - An unknown template gets `NOT_FOUND`.
   - A disk-only capture gets `INVALID_ARGUMENT`.
6. The source is released, and the fork keeps running with its memory.

```sh
LAMP_E2E_SOCKET=/run/lamp/worker.sock LAMP_E2E_TEMPLATE=tp_8c3b218c57ccd4dbea283a6abd \
  cargo test --release --locked -p lamp-supervisor --test e2e -- --nocapture
lamp-supervisor ctl bench-create TEMPLATE 200 20
lamp-supervisor ctl bench-fork TEMPLATE {0|128|1024} 100 5 [--durability local]
```

### Template built without Docker (TPL-001)

[seed-build.log](evidence/t2/seed-build.log) and [seed.json](evidence/t2/seed.json):

- Kernel 6.18.62, built from kernel.org source with Firecracker's pinned guest
  config.
- Debian 13 root built with mmdebstrap.
- The static musl `lamp-init` and `lamp-guest-helper`, built from this commit's
  source.
- Imported as `lamp-seed/debian-13@sha256:4c72d175…`
  ([seed-import.log](evidence/t2/seed-import.log)).

[demo.lamp](evidence/t2/demo.lamp) was built into `tp_8c3b218c57ccd4dbea283a6abd`
in 32.3 s ([template-build.log](evidence/t2/template-build.log)):

1. Boot the seed, run the steps, shut down.
2. Boot the standard VM and take a full 4 GiB capture (23.9 s, after which
   3978 MiB of zero pages are punched to holes).
3. Write the canonical manifest and signed provenance, publish by digest,
   re-verify, and run a probe sandbox.

The [manifest](evidence/t2/template-manifest.json) records the engine digest,
the CPU baseline, the device graph and the c4m object digests. No Docker,
OCI image or registry is involved.

### Bug found by the benchmark and fixed

The first `local` run aborted on trial 15 (`HOST_UNAVAILABLE`); see
[pre-fix-local-oom.log](evidence/t2/pre-fix-local-oom.log). The source VM was
killed by its own cgroup's `memory.max`. Diff layers written into tmpfs were
charged to the source sandbox's cgroup, and forks keep those layers alive, so
the charge kept growing until the source was OOM-killed.

Now the VMM runs in a shared `lamp/layers` cgroup only while it writes the
snapshot. Guest RAM stays charged to the sandbox. On the rerun, the source's
cgroup held 0 MiB of shmem while `lamp/layers` held the 2 GiB of live layers.
There were no OOM kills across all 553 trials.

## Lanes

`make verify` (build, Go/Rust/Python tests, contracts, interop, codegen, fmt,
clippy) passes on the M5 (macOS arm64). It also passes as a clean checkout via
`tools/clean-check.sh` on LX and LA, as the PR's self-hosted CI shows.

On macOS the engine modules compile out (`cfg(linux)`); the protocol, guest
protocol, manifest and definition code is tested on all three lanes. The VM
tests need KVM and run only on native LX.

## Deviations and open items

- **Full 4 GiB capture per fork.** The spec asks for a full 4 GiB capture
  (architecture §6). Here the fork writes a Firecracker Diff snapshot of the
  pages written since the last capture, and the child sees a **complete**
  image: the immutable full template image plus the diff layers above it,
  served by a userfaultfd pager. Every page of the 4 GiB is defined and
  preserved, and the 1 GiB rows verify it page by page. What is not done is
  writing 4 GiB of bytes per fork: at 8 GiB/s or more that alone would take
  500 ms, which is beyond this host. A diff-layer stack meets the same
  consistency and isolation requirements. Its storage format is unfrozen
  (T14), and T5's oracle should judge it.
- **Confinement.** `lamp-pager` has no seccomp filter yet, and there is no LSM
  profile. The boundary is the jailer: per-sandbox UID, chroot, PID and network
  namespaces, and a cgroup.
- **Grants.** `lamp.worker/1` grant verification (Ed25519, binding,
  single use) is implemented and tested in `lamp-worker-protocol`. The engine
  still accepts no handle grants, and a context `grant_id` is only required to
  be present: there is no supervisor-installed grant store yet.
- **Restarts.** A supervisor restart discards all volatile state (sandboxes,
  layers, captures and operation history). `local` captures are not reloaded
  after a restart yet.
- **Network.** Builds and sandboxes have no network, because the T4 guard is
  not attached.
- **T6 join with T4.** The engine still needs:
  - a TAP FD handed to the jail from the guard's namespace;
  - a vsock stream per generation for `lamp-guard receive`;
  - guest exec routed through `lamp-guard collect` (today the guest helper runs
    commands directly as guest root).
- **T6 join with T5.** T5's harness needs `benchmarks/workload.py` installed
  in the prepared template as `/opt/lamp/bench-workload.py`.
- **Same boot ID.** Restored sandboxes keep the template's kernel boot ID, as
  any snapshot restore does. Their identity comes from the barrier instead.
