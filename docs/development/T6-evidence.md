# T6 engine gate evidence (lane LX)

Lane LX is the owner's Ubuntu laptop: Intel Core Ultra 9 288V (4 P-cores +
4 LP E-cores), 31 GiB RAM, one NVMe disk (LUKS, LVM, ext4), Linux
7.0.0-42-generic, native, on AC with the `powersave`/`balanced` profile. Every
run below used the real stack end to end:

```
lamp CLI -> lampd (Go, journal) -> lamp.worker/1 -> lamp-supervisor (Rust)
  -> jailer + Firecracker 1.17 microVM per sandbox
  -> per-sandbox guard namespace: TAP, lamp-guard NFQUEUE, pasta uplink,
     mediated proxy, host audit receivers; guest collector over vsock
```

The measuring side is the independent T5 harness (`benchmarks/`), driven
through the public CLI, with the LX observer (`infra/linux/observer.py`) and
live §22 metadata (`infra/linux/run-metadata.py`). No Docker is used anywhere in
the path. The thresholds are the fixed constants in `benchmarks/protocol.py`;
none was changed, and failed runs stay on record as failed.

**Gate result: FAIL.** The rows and the reasons are below, as measured.

## E01 (`e01-final`, 2026-11-10 06:52–08:16 UTC)

Evidence: [summary](evidence/t6/e01-final/summary.json),
[manifest](evidence/t6/e01-final/manifest.json),
[metadata](evidence/t6/e01-final/e01-final-metadata.json),
[rows](evidence/t6/e01-final/e01-final.rows.txt),
[log](evidence/t6/e01-final/e01-final.log) and all 49,158 chained
observations ([gzip](evidence/t6/e01-final/e01-final-observations.jsonl.gz),
last hash `ed8fc97a…1004172`). Status **fail**; no cleanup errors. 660 resource
samples are listed as missing: they belong to the fork trials that were never
reached in the `replicated` rows.

Targets: create p50 < 500 ms; every fork < 1000 ms with the source running and
the fork useful; zero state errors. Times are caller-to-useful-command in ms.

| Row | Trials | Failures | p50 | p95 | max | Result |
| --- | --- | --- | --- | --- | --- | --- |
| `volatile-0` create | 200/200 | 0 | 368 | 628 | 986 | pass |
| `volatile-0` fork | 100/100 | 0 | 713 | 1072 | 1313 | **fail** (7 forks > 1 s) |
| `volatile-128` create | 200/200 | 0 | 378 | 593 | 747 | pass |
| `volatile-128` fork | 100/100 | 0 | 665 | 938 | 1102 | **fail** (4 > 1 s) |
| `volatile-1024` create | 200/200 | 0 | 340 | 514 | 621 | pass |
| `volatile-1024` fork | 100/100 | 0 | 680 | 888 | 1126 | **fail** (3 > 1 s) |
| `local-0` create | 200/200 | 0 | 331 | 545 | 780 | pass |
| `local-0` fork | 100/100 | 0 | 1922 | 3714 | 4542 | **fail** (all > 1 s) |
| `local-128` create | 200/200 | 0 | 396 | 650 | 1245 | pass |
| `local-128` fork | 100/100 | 0 | 2171 | 4284 | 5225 | **fail** (all > 1 s) |
| `local-1024` create | 200/200 | 0 | 394 | 607 | 798 | pass |
| `local-1024` fork | 100/100 | 0 | 5944 | 12689 | 15708 | **fail** (all > 1 s) |
| `replicated-0` create | 200/200 | 200 | 7 | 10 | 12 | **fail** (`QUORUM_REQUIRED`) |
| `replicated-0` fork | 0/100 | — | — | — | — | **fail** (not reached) |
| `replicated-128` create | 200/200 | 200 | 8 | 11 | 13 | **fail** (`QUORUM_REQUIRED`) |
| `replicated-128` fork | 0/100 | — | — | — | — | **fail** (not reached) |
| `replicated-1024` create | 200/200 | 200 | 7 | 11 | 12 | **fail** (`QUORUM_REQUIRED`) |
| `replicated-1024` fork | 0/100 | — | — | — | — | **fail** (not reached) |

Every run fork kept its source running and was useful, and every trial in this
run completed without a state error. (`e01-full-1` had 100 state errors in
`local-1024`; they are fixed, see below.) The volatile fork misses come from
the capture floor: a fork writes the full 4 GiB guest image to tmpfs, which
takes ~320–450 ms with a preallocated spare file, plus the restore, two execs
and the control path. Host jitter on this 4 P-core laptop pushes 3–7 % of
forks past 1 s.

## E02 (`e02-final-2`, 2026-11-10 08:18–08:27 UTC)

Evidence: [summary](evidence/t6/e02-final-2/summary.json),
[manifest](evidence/t6/e02-final-2/manifest.json),
[metadata](evidence/t6/e02-final-2/e02-final-metadata.json),
[log](evidence/t6/e02-final-2/e02-final-2.log) and the chained observations
([gzip](evidence/t6/e02-final-2/observations.jsonl.gz)). Status **fail**; no
missing observations or cleanup errors. Each measure has ten randomized
secured/native pairs. The ratio is secured/native in the "higher is better"
direction, given as the paired geometric mean and the seeded paired-bootstrap
one-sided 95 % lower bound. The binding target is ≥ 0.97; the confidence
objective is provisional.

| Measure | Point ratio | 95 % lower | Pair range | Result |
| --- | --- | --- | --- | --- |
| `compile` | 0.287 | 0.221 | 0.132–0.566 | **fail** |
| `compression` | 0.966 | 0.930 | 0.806–1.018 | **fail** |
| `crypto` | 0.970 | 0.962 | 0.948–0.999 | **fail** (point at the line, bound below) |
| `tree` | 18.106 | 13.928 | 8.205–38.663 | pass, see caveat |
| `seq_read` | 2.311 | 2.014 | 1.218–2.756 | pass, see caveat |
| `seq_write` | 4.163 | 3.530 | 3.032–11.178 | pass, see caveat |
| `random_read` | 2.821 | 2.591 | 1.890–3.272 | pass, see caveat |
| `random_write` | 2.638 | 1.378 | 0.185–6.251 | pass, see caveat |
| `fsync` | 17.403 | 15.975 | 13.037–21.692 | pass, see caveat |
| `cow_first_write` | 2.819 | 2.092 | 0.805–5.249 | pass, see caveat |
| `tcp_goodput` | 0.258 | 0.239 | 0.198–0.306 | **fail** |
| `tcp_packets` | 0.865 | 0.826 | 0.768–1.080 | **fail** |
| `udp_goodput` | 0.807 | 0.734 | 0.595–0.997 | **fail** |
| `udp_packets` | 0.771 | 0.702 | 0.492–0.880 | **fail** |
| `small_flows` | 0.315 | 0.258 | 0.166–0.600 | **fail** |
| `request_latency` | 0.860 | 0.760 | 0.513–1.145 | **fail** |
| `proxy` | 0.232 | 0.212 | 0.164–0.316 | **fail** |

**Caveat on the disk "passes".** The engine attaches Firecracker drives without
setting `cache_type`, so they use Firecracker's default `Unsafe` mode. In that
mode the guest's flushes are not passed to the host disk, so a guest
`fsync`/`O_SYNC` write finishes in the host page cache, while the native half
reaches the NVMe. A 17× fsync ratio and a ~18× `tree` ratio measure that
difference, not a faster guard. These rows are recorded as the harness scored
them, but they are not like-for-like durability evidence. Switching the drives
to `Writeback` (flushes honoured) is follow-up engine work and needs a new E02
run. Even if every disk row is counted as passed, the gate still fails on
compute and network.

Where compute and network fall short:

- `compile` (0.29) runs `cc -O2` on a fresh process tree. Each guest process
  pays the guard's audit-before-effect cost, about 7.5 ms, for its durable fork
  and exec records. `compression` and `crypto`, single-process loops, land at
  0.93–0.97.
- Network goes TAP → NFQUEUE guard → pasta. Every packet is inspected in user
  space, so packet rates reach 0.70–0.87 and goodput 0.24–0.81. `small_flows`
  and `proxy` (0.21–0.26) also pay a connection setup through the guard, and
  for the proxy, the mediated single-threaded proxy.

**Gate result, as measured: FAIL.**

- E01: every create row except `replicated` passes. Every fork row fails: the
  volatile rows narrowly (p50 665–713 ms, max 1.1–1.3 s), the local rows by
  2–16× (disk-bound), and the replicated rows are not built.
- E02: 7 of 17 measures pass (all of them disk, and see the caveat). Every
  compute measure and every network measure fails.
- Docker: not used.
- State errors in `e01-final`: zero.


## Runs on record

| Run | Status | What it shows |
| --- | --- | --- |
| `e01-smoke-1..5` | diagnostic, fail (smoke can never pass) | wiring checks during development |
| `e01-full-1` | **fail; end phase hung, stopped** | first full binding run (see below) |
| `e01-final` | **fail** | the one final full binding run after the fixes |
| `e02-final` | **fail** (no measurements) | I passed the fixture as inline JSON; `--network-fixture` takes a file path, so the runner stopped at once ([summary](evidence/t6/e02-final/summary.json)) |
| `e02-final-2` | **fail** | the one E02 run with the fixture file |

### e01-full-1 (2026-11-10 03:33–05:03 UTC)

[Status](evidence/t6/e01-full-1/e01-full-1.status.json),
[rows](evidence/t6/e01-full-1/e01-full-1.rows.txt),
[metadata](evidence/t6/e01-full-1/e01-full-1-metadata.json),
[manifest](evidence/t6/e01-full-1/manifest.json), and the trial records
(the first 5,231 observations, gzip) in
[e01-full-1-trials.jsonl.gz](evidence/t6/e01-full-1/e01-full-1-trials.jsonl.gz).
The complete 270 MB / 572,800-line observation file stays on LX; its SHA-256 is
in [e01-full-1.sha256](evidence/t6/e01-full-1/e01-full-1.sha256).

It found three real defects, all fixed before `e01-final`:

1. **State errors in every `local-1024` fork** ("workload exited
   unsuccessfully", then `ConnectionRefusedError` on the resident marker).
   During a local capture the 1+ GiB durable image copy and its fsync queued
   every other fsync on the same ext4 disk, including the audit receiver's
   storage barrier. The guest collector read ACKs with a 500 ms socket timeout
   and treated a slow ACK as a lost stream: it reconnected, each reconnect
   started a new receiver and spool segment (more fsyncs), and after the 5 s
   `RECONNECT_DEADLINE` the tracer exited, so `PTRACE_O_EXITKILL` killed the
   resident. Fixes: a read timeout on a connected stream now keeps waiting on
   the same stream (partial ACK bytes are kept) up to the deadline and then
   fails closed without retrying; real transport errors (the snapshot's vsock
   reset) still reconnect. The local image copy syncs every 64 MiB so no fsync
   queues behind gigabytes. Unit test
   `a_slow_ack_is_waited_for_on_the_same_stream`.
2. **The end phase hung.** The harness polled `operation get`, `exec get` and
   `exec watch` every 1 ms, spawning a `lamp` CLI process per call (~554,000
   calls), and wrote its in-memory transcript at the end as one fsynced,
   chained observation per call. After the last trial (line 5,231, a
   `row_setup_failure` for `replicated-1024`) it spent over an hour writing
   ~567,000 `public_call` records. The owner restarted their services at
   05:00 UTC; the run was stopped at 05:03 (exit 143) and is recorded as hung.
   Fix: mutations pass the public `wait_ms` and `exec watch` long-polls, so an
   exec is four calls. A full E01 now makes ~40,000 calls.
3. **Control-path latency.** lampd's journal append+fsync waited on unrelated
   ext4 metadata (the engine's jail and reflink files on the same disk):
   36–66 ms per fsync during a fork, three per operation. Fixes: the journal
   log is preallocated with real zero bytes and committed in place with
   `fdatasync` (path identity re-checked per commit, torn tail ignored on
   replay); the pre-dispatch commit overlaps the first worker RPC (readers
   still only see it once durable; recovery is unchanged because the durable
   accepted record already restarts as `outcome_unknown`); `exec watch`
   waits on a commit notification instead of a 1 ms spin under the service
   mutex; exec observation backs off from 1 ms to 4 ms after 50 polls.
   Measured on the same lane: lampd+CLI overhead per fork p50 26 ms (max 68)
   instead of 16–246 ms; harness create trial p50 272 ms instead of ~386–431.

## What is not built, and the rows it fails

- **`replicated` durability.** lampd refuses it before any effect with
  `QUORUM_REQUIRED` ("durability is not supported by the local worker").
  Replication needs a quorum of independent stores; this single-host demo has
  none. All three `replicated` rows (create and fork) are recorded as failed.
- **`local` forks under 1 s.** A local fork makes the full 4 GiB image durable
  on this laptop's single disk before it reports; that takes 2–15 s here. The
  rows are run and recorded as failed, not relabelled or moved to non-binding
  rows (architecture.md:1127, :1282).

## Deviations from the T4 integration contract

See [T4-guard.md, "As integrated at T6"](T4-guard.md#as-integrated-at-t6-lane-lx):
the VMM runs inside the guard namespace; pasta starts as root and joins through
a capability-less holder; exact fixture exceptions in policy revision 2; the
mediated proxy is single-threaded. Also:

- `source_pause_ms` comes from the supervisor's journal, not an independent
  witness, and is labelled `source_pause_origin: "engine_journal"`.
- The guest's VMM threads are not pinned; the native E02 half is pinned to
  CPUs 0,1 (`--native-cpus`). A pinning experiment moved guest Python start-up
  by 10–20 %, not enough to change any row.
- Per guest process, the audit-before-effect records (fork and exec durable
  before the child runs) cost about 7.5 ms (20 × `/bin/true`: ~150 ms).

## Host preparation and the owner's services

`infra/linux/timing-run.sh` wraps each hard timing run: it stops only the
owner's services that are running (`excerlock-dm-tt`, `-dm-ig`, `-dm-chrome`,
`-beeper`), drops clean caches and compacts memory (a fragmented host failed
KVM vCPU creation with an order-5 allocation failure during development), runs
the suite, and restarts exactly what it stopped whatever the outcome. The T3
Code server, the Lamp CI runner and Docker are never touched.

- `e01-final` and `e02-final-2`: the wrapper stopped `dm-tt`, `dm-ig` and
  `dm-chrome` and restarted all three; each reports `active` at the end of its
  log.
- `beeper`: its unit has been inactive since 05:02 UTC. The owner started
  Beeper by hand at 05:00, when `e01-full-1` hung. That process still holds
  ports 23373/9566, so the unit's own restart then failed with "Address
  already in use". The Beeper process keeps running and serving, outside its
  unit, and no timing run stopped it.
- The `e01-final` metadata was collected before the commit was amended to make
  `timing-run.sh` executable (a mode-only change), so its source hash names the
  pre-amend tree.

## Reproduce

```sh
python3 infra/linux/run-metadata.py "$SRC" /var/tmp "$TEMPLATE" > metadata.json
infra/linux/timing-run.sh -- python3 -m benchmarks.run --suite E01 \
  --cli "$HOME/lamp-t6/bin/lamp" --template "$TEMPLATE" --metadata metadata.json \
  --output "$NEW_RUN_DIR" --directory /var/tmp \
  --observer '["python3","infra/linux/observer.py"]'
```
