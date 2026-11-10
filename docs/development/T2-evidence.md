# T2 Linux engine evidence

Review round 1, 2026-11-09, native LX: Ubuntu 26.04.1, kernel 7.0.0-42,
Intel Core Ultra 9 288V (8 threads), 31 GiB RAM, governor `powersave`
([host.txt](evidence/t2/host.txt)). Everything below ran on the real engine:

- Firecracker 1.17.0 microVMs under the jailer;
- the Debian 13 guest built here;
- driven over `lamp.worker/1` by `lamp-supervisor ctl` and the e2e test, the
  way `lampd` would drive it, with a supervisor-installed control grant.

Nothing is mocked.

**Owner services.** The owner's Chrome service was stopped for the timing runs
only. It was not actually down for most of them:

- **Run 1.** Chrome was stopped at 19:55:43, and something outside the run
  started it again at 19:55:48, before the first timed trial. The unit has only
  `Restart=on-failure`, and the run did not touch it again. So the create row
  and all three volatile fork rows ran with Chrome running.
- **Run 2 (the `local` row).** Chrome was stopped at 20:06:55. It was started
  from outside again at 20:10:58, about a third of the way through the row.

The script started Chrome again at the end of each run, and the T3 Code server
stayed up throughout. Docker was not touched. The details are in
[host.txt](evidence/t2/host.txt), [bench.log](evidence/t2/bench.log) and
[bench-local.log](evidence/t2/bench-local.log).

## Result

Standard sandbox B1 is 2 vCPU, 4 GiB RAM and a 20 GiB disk. The timer starts
before the caller sends its first request. It stops when the command's result
is back (create), or when both sides' commands are back (fork).

**Every fork captures the full configured 4 GiB.** Each trial reports
`capture.memory_bytes = 4294967296`. Nothing chooses the child's memory by
dirty tracking. The cost of writing it all, `capture.full_write_us`, is inside
every timed fork. There is no dirty-only option.

| Row | Trials (+warmup) | p50 | p95 | p99 | max | Target | Result |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Create → command ran | 200 (+20) | 57.6 ms | 90.3 ms | 102.0 ms | 103.3 ms | p50 < 500 ms | ✅ met, 0 failures |
| Fork, 0 MiB recently written | 100 (+5) | 550 ms | 595 ms | 751 ms | 809 ms | every fork < 1000 ms | ✅ 100/100, 0 failures |
| Fork, 128 MiB recently written | 100 (+5) | 559 ms | 609 ms | 637 ms | 638 ms | every fork < 1000 ms | ✅ 100/100, 0 failures |
| Fork, 1 GiB recently written | 100 (+5) | 563 ms | 602 ms | 619 ms | 628 ms | every fork < 1000 ms | ✅ 100/100, 0 failures |
| Fork, 1 GiB, `durability=local` | 100 (+5) | 6.22 s | 15.4 s | 16.5 s | 16.5 s | every fork < 1000 ms | ❌ **missed**: 0/100 under 1 s, 0 errors |
| Fork, 1 GiB, `local`, first attempt | 27 (+5), aborted | 9.21 s | 15.5 s | — | 15.7 s | every fork < 1000 ms | ❌ missed, then aborted (see below) |

How the fork time breaks down at 1 GiB volatile:

| Stage | p50 | max |
| --- | --- | --- |
| Full 4 GiB write | 493 ms | 555 ms |
| Source pause | 512 ms | 578 ms |
| Task freeze | — | 0.7 ms |

The rest is the child's restore and barrier, plus a command on each side. All
three volatile rows look alike because the write is always the whole 4 GiB.

The thresholds are unchanged (ENG-001). A miss is recorded as a miss.

Raw per-trial records, one JSON line per trial with warmups marked
`"warmup":true` and nothing filtered out:

- [create-200](evidence/t2/create-200.jsonl)
- [fork-0m](evidence/t2/fork-0m.jsonl)
- [fork-128m](evidence/t2/fork-128m.jsonl)
- [fork-1g](evidence/t2/fork-1g.jsonl)
- [fork-1g-local](evidence/t2/fork-1g-local.jsonl)
- [fork-1g-local-aborted](evidence/t2/fork-1g-local-aborted.jsonl)

The summaries are the last line of each matching `.err` file.

Each fork trial checks the following in the real guests before it counts as a
success:

- **Memory preserved (sampled).** Each dirty row rewrites a RAM-only marker on
  every 4 KiB page of its region before each trial. A full page scan runs on
  every warmup, on every 10th trial and on the last one: 16 of the 105 trials
  in each dirty row. Every scan found 0 bad pages in source and fork, with
  equal generation and digest.
- **Both keep running.** The source and the fork each run a command after the
  fork.
- **Disk private.** A file the fork writes is not visible in the source.
- **Fresh identity and entropy.** The machine IDs differ, and so do 32 bytes
  read from `/dev/urandom` on each side.
- **Clock.** The fork's clock is within 1 s of the host clock.

**`durability=local` misses, and it is the disk.** A local fork must fsync the
image's non-zero pages, the VM state and the disk to stable storage before it
reports. That took 1.4–15.9 s here (`capture.publish_us`), while the source
pause stayed at p50 527 ms. This laptop's disk sustained 35–140 MB/s for
direct, fsynced writes, and the result is never reported early. Per the spec,
insufficient owned storage bandwidth is a visible failure of standard mode.
`lampd` (T3) admits only volatile forks.

**Why the first `local` attempt aborted.** Trial 32 failed with
`HOST_UNAVAILABLE: firecracker API: Connection refused`. The child's API
socket file exists from `bind()`, a moment before Firecracker calls `listen()`,
and the engine waited only for the file. The failure was reported, the
capture was handed back unconsumed, and no other sandbox was affected.

The fix (`fc::connect_api`, test `api_connect_waits_for_listen_after_bind`)
retries a refused connect for up to 2 s. That is safe because nothing has been
sent at that point. The `local` row was then rerun in full with 0 errors. Its
first 27 measured trials are kept as recorded.

The volatile rows ran on the binary from before this connect fix. The fix
changes only how the engine connects to a starting VMM.

## Review items and their evidence

| Item | Change | Test or evidence |
| --- | --- | --- |
| **M1** full capture | Freeze → pause → Firecracker **Full** snapshot of all RAM into root-only tmpfs (`huge=always`) → reflink disk → resume → thaw. The child loads it with the File backend (`MAP_PRIVATE`), with no dirty tracking. The diff layers, the pager and compaction are removed. | Every bench record has `memory_bytes=4294967296` and `full_write_us`. The e2e asserts both (`tests/e2e.rs`, "M1"). Table above. |
| **S1** pager unconfined | The pager is gone. The only host process serving a sandbox is its jailed Firecracker VMM, with its own seccomp filter (Firecracker's default filter, applied by the VMM itself). | e2e asserts that the jail cgroup holds only `firecracker`. [s1-s2.log](evidence/t2/s1-s2.log): source and fork VMMs each have `Seccomp: 2`, `NoNewPrivs: 1`, and no other sandbox-UID process exists. |
| **S2** readable captures | `/run/lamp/mem`, `captures`, `jails` and the store's `jail/`, `captures/`, `grants/` and `keys/` are 0700 root. Images are root:root 0400. A child gets a hard link that it owns, 0400, in its own 0700 memdir. | e2e: an unprivileged `read_dir` of each directory gets `PermissionDenied`. [s1-s2.log](evidence/t2/s1-s2.log) has the modes. |
| **S3** tasks run before the barrier | Every command joins the guest cgroup `/sys/fs/cgroup/tasks` before exec (fail closed). Capture freezes that cgroup (`frozen 1`) before the pause. The child resumes with its tasks frozen, and only its barrier thaws them, after machine-id, hostname, clock and CRNG are new. | e2e "S3": a probe started in the parent logs machine-id and urandom every 1 ms. After the fork, every child line (657 checked) carries the child's machine-id and entropy never seen in the parent. [e2e.log](evidence/t2/e2e.log). |
| **S4** map order | Unknown-field detection without byte equality (`wire::equivalent`, a multiset field comparison). Fingerprints encode the command without `env` plus sorted env pairs. | `wire` tests `map_order_is_accepted_and_unknown_fields_are_not`, `socket_receive_accepts_go_map_order`, `malformed_input_is_never_equivalent`. `server` test `fingerprint_ignores_map_order_but_not_content`. e2e: a replay with reversed env returns the same receipt. |
| **S5** Start blocks until exit | Start returns `command_started` once the guest helper has witnessed the exec (`ExecStarted`). The deadline bounds only that acknowledgement. Completion comes through `Inspect{command_operation_id}` (`command_state`). The helper keeps results by exec ID, and the host fetches them with short idempotent `Wait`s, so a command spanning a capture completes, even though Firecracker resets guest vsock connections at snapshot. | e2e "S5": `sleep 3; echo slept` is acknowledged in < 1 s with `finished_at=0`, spans the fork, and then completes in the source with `(0, "slept")`. Unit test `completion_survives_a_reset_connection`. |
| **S6** offline commands kill jails | `store/lock` is an exclusive flock. `serve` takes it and recovers the store. `seed-import`/`template-build` take it and never recover. `templates` is read-only and takes no lock. | `fsutil` test `store_lock_is_exclusive_until_released`. [s6-s7.log](evidence/t2/s6-s7.log): with the service serving a live sandbox, `template-build` and `seed-import` get `RESOURCE_IN_USE … stop lamp-supervisor first`, `templates` lists, and the sandbox still runs a command afterwards. |
| **S7** signals | SIGTERM/SIGINT are blocked at the top of `main`, before any thread exists. A single waiter takes them with `sigwait`, stops every sandbox, and exits. The first live run showed VMM processes still exiting after the service stopped (a task leaves its cgroup before it finishes exiting), so `kill` now also waits until the VMM process is gone. | [s6-s7.log](evidence/t2/s6-s7.log) final section: 10× `systemctl stop` (SIGTERM) and 5× SIGINT, each with 3 live sandboxes. Every run logs `signal N: stopping every sandbox` / `stopped 3 sandbox(es) in 49–89 ms`, with no VMM process left. The earlier failing runs are kept in the same log. |
| **S8** lost reply retryable | Guest failures before the request frame is fully written are `HOST_UNAVAILABLE` (retryable). Failures after it are `OUTCOME_UNKNOWN`, never retried. | `guest` tests `lost_reply_after_delivery_is_outcome_unknown`, `failure_before_dispatch_is_retryable`. |
| **S9** any grant_id passes | `grant_id` must name a root-installed `lamp.control-grant/v1`, checked on every request before dispatch: caller UID, sandbox prefix, exact generation, policy revision, command and expiry. | `grants` tests `scope_generation_policy_and_expiry_are_enforced`, `install_writes_a_private_file_and_load_rejects_writable_ones`. e2e: an unknown grant gets `UNAUTHENTICATED`, and generation 2 gets `SCOPE_DENIED`. |

### End-to-end test

`crates/lamp-supervisor/tests/e2e.rs`, run against the live supervisor
([e2e.log](evidence/t2/e2e.log), Chrome running):

- **Create to command:** 76.8 ms.
- **Memory fork:** 988 ms, with a 925 ms full write and a 948 ms source pause.
  That single fork was under the threshold, but much slower than the bench
  rows. It ran while the host was busy.
- **Identity probe:** passed.

What the test covers, in order:

1. The grant checks (S9).
2. Create.
3. The asynchronous Start (S5).
4. RAM and disk markers, and the 1 ms identity probe.
5. The full capture (M1).
6. The child restore.
7. Permission checks (S2).
8. Completion of the spanning command (S5).
9. RAM markers, disk isolation, machine ID and entropy.
10. The probe analysis (S3).
11. A wrong generation, an idempotent replay with reversed env (S4), an
    idempotency clash, `NOT_FOUND`, `INVALID_ARGUMENT`, then release.

```sh
G=$(sudo lamp-supervisor grant-install --caller-uid $(id -u) | jq -r .grant_id)
LAMP_E2E_SOCKET=/run/lamp/worker.sock LAMP_E2E_GRANT=$G LAMP_E2E_TEMPLATE=tp_d1afab53f18d795751b1bb4112 \
  cargo test --release --locked -p lamp-supervisor --test e2e -- --nocapture
LAMP_GRANT_ID=$G lamp-supervisor ctl bench-create TEMPLATE 200 20
LAMP_GRANT_ID=$G lamp-supervisor ctl bench-fork TEMPLATE {0|128|1024} 100 5 [--durability local]
```

### Template built without Docker (TPL-001)

The seed ([seed-build.log](evidence/t2/seed-build.log),
[seed.json](evidence/t2/seed.json)) contains:

- kernel 6.18.62;
- a Debian 13 root built with mmdebstrap;
- the static musl `lamp-init` and `lamp-guest-helper`, built from this
  branch's source.

It was imported as `lamp-seed/debian-13@sha256:50eacc7c…`
([seed-import.log](evidence/t2/seed-import.log)), with the service stopped as
S6 requires.

[demo.lamp](evidence/t2/demo.lamp) was built into
`tp_d1afab53f18d795751b1bb4112` in 27.7 s
([template-build.log](evidence/t2/template-build.log)):

1. Boot the seed, run the steps and shut down.
2. Boot the standard VM, freeze its tasks and take a Full 4 GiB capture.
3. Write the canonical manifest and the signed provenance.
4. Publish by digest, verify again, and run a probe sandbox.

The [manifest](evidence/t2/template-manifest.json) records the engine digest,
the CPU baseline and the object digests. No Docker, OCI image or registry is
involved.

### Earlier bug kept for the record

[pre-fix-local-oom.log](evidence/t2/pre-fix-local-oom.log) is from the first
round. Snapshot pages written into tmpfs were charged to the source sandbox's
cgroup until it was OOM-killed. The VMM still moves to the shared `lamp/layers`
cgroup while it writes a capture, so the image is charged there.

## Lanes

`make verify` passes on the M5 (macOS arm64), and so does `make
check-generated`. On LX, `cargo test --workspace` passes, including the
Linux-only engine tests. The PR's self-hosted CI runs clean checkouts on LX
and LA.

## Open items

- **Restarts.** A supervisor restart discards all volatile state. `local`
  captures are not reloaded yet.
- **Network.** There is none until the T4 guard is attached (T6).
- **T6 join with T4.** The engine still needs:
  - a TAP FD from the guard's namespace;
  - a vsock stream per generation for `lamp-guard receive`;
  - exec routed through `lamp-guard collect`.
- **T6 join with T5.** `benchmarks/workload.py` must be installed in the
  prepared template as `/opt/lamp/bench-workload.py`.
- **Boot ID.** Restored sandboxes keep the template's kernel boot ID, as any
  snapshot restore does. Identity comes from the barrier.
- **No LSM profile.** The boundary is the jailer's UID, chroot, namespaces and
  cgroup, plus Firecracker's seccomp filter.
