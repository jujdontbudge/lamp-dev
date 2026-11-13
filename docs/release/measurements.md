# Lamp release measurements

## 0.1.2 public release observations — 2026-11-13

[0.1.2 is public](https://gitlab.com/johansardar/lamp/-/releases/v0.1.2).
The immutable assets were built from merged development commit
`e7babe9d658ff21f033e5ae23431f041fb7b086d`; the redacted public tag points at
`d83031de3780dae053a0fdfac900cf8d11a8cde0`. All ten public files matched the
published checksums. The Mac payload was Developer ID signed and Apple accepted
notarization submission `8a98c2e9-0f57-4e48-ada4-a1d64ff22c9b`.

These are **one observation per available host**, using the public installer
and CLI, with everyday services running. They are not p50 distributions or
certification. Earlier failures and historical results below remain historical.

| Host | Public install through first Python code | Create through useful command | Live memory fork through useful child command | Cleanup |
| --- | ---: | ---: | ---: | --- |
| M5 | 69.876 s | 553.6 ms | 750.9 ms | uninstalled; prefix removed |
| Air | 69.638 s | 733.9 ms | 1199.6 ms | uninstalled; prefix removed |
| lamp-la | 62.276 s | 4723.3 ms | 3862.8 ms | uninstalled; prefix removed |
| Ubuntu amd64 | skipped | skipped | skipped | no install or VM created |

M5 is Apple M5 / 24 GiB, macOS 26.5.2; Air is M2 / 8 GiB, macOS 26.7.1.
`lamp-la` is Linux arm64 / 8 GiB / four vCPUs, nested KVM in the existing Lima
VM on the M5; it is not bare metal. Ubuntu had **3,622 MiB available**, below
the owner's 12 GiB requirement even for installation. Chrome was left running.

Install timing starts before downloading `https://lamp.eilmer.si/install.sh`,
includes signed installation, version/CLI/MCP/OpenAPI discovery and the first
README-style Python command, and excludes uninstall. Create uses the installed,
warm default template with explicit 1 vCPU / 512 MiB / 20 GiB / 4,096 PIDs,
from the create CLI invocation through a separate useful Python exec. Fork uses
`--fork-mode memory --durability local` with a 1 MiB resident witness; timing
ends when the child answers. Parent and child retained its nonce and advanced
its RAM tick counter. This is a small local-durability fork, not the old 4 GiB
rows or a replicated-storage observation. A single create cannot establish a
p50 target; the table preserves observations above and below the 500 ms and
1 s goals without selecting a better rerun.

### Single native/sandbox workload pairs

Ratios are sandbox over native; higher is better. Fsync uses the inverse of
seconds per operation. These are actual observations, not a claim of 97% parity.

| Host | Compile | Compression | SHA-256 | Sequential write | Fsync | TCP diagnostic |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| M5 | 3.5970 | 0.9813 | 0.9461 | 0.0941 | 0.0040 | 0.1282 |
| Air | unobserved / failed | 0.9688 | 0.9702 | 0.1483 | 0.0087 | 0.1255 |
| lamp-la | 0.0371 | 0.4663 | 0.6708 | 0.3379 | 0.0012 | 0.0351 |

The fixed standard-library workload is `benchmarks/workload.py`, SHA-256
`7ef80949f5b68a839135d6ffb1ec5f246d3a67662b027280bb072dbff26a5cff`.
Each completed workload pair ran once. Air native compile failed before its
guest pair; compiler stderr was not retained, and it was not rerun. Native Mac uses Apple Clang;
the Linux guest uses GCC, and Python/OpenSSL builds differ too. Compile includes
toolchain startup and execution, so its ratio is not a CPU-only comparison.
Disk paths and flush semantics differ (APFS/native versus ext4 guest, and
Firecracker's `Unsafe` cache on Linux): **these are not durable native-speed
parity measurements**. TCP compares host loopback against a guarded owner-service
alias to a disposable echo server, not matched independent remote endpoints;
remote network parity is unobserved. Exact elapsed values and toolchains are in
the release evidence. No complete compute/disk/network parity is established.

### Bounded credential observation and missing work

- M5: the fresh installation control owner token was absent from one guest process environment.
- Air: the fresh installation control owner token was absent from one guest process environment.
- lamp-la: the fresh installation control owner token was absent from one guest process environment.

This does not inspect complete guest memory or disk, model-provider custody,
network captures or all Exposures. **The zero-leak goal is not completely
measured.** Ubuntu installation/version/VM execution and its release speed
measurements are unobserved because of the memory floor. Enrollment-only
features and the unverified Linux arm64 Docker/LSP/desktop kits retain their
release-note limits. No SDK work was added.

Air native compile exited with code 1. Its stderr was not retained by the
workload, so the cause is unobserved; no guest compile pair or rerun is claimed.
The original exception is preserved in the release evidence.

The first Python `urllib` public-URL verifier received HTTP 403; the failure
is preserved. The curl verifier then checked the same URLs successfully, and
the documented curl installer was used for the observations above. No release
asset was replaced. Bundled documentation is the pre-publication snapshot;
this live page and the GitLab release description carry the final observations.

### Install through code: T32 development candidates, 2026-11-12

These are signed candidate installations at fresh prefixes on existing hosts,
over local HTTP origins. They exclude public download/release assembly and
include sandbox cleanup. They are single observations, not p50 distributions.
They precede the final T32–T46 merge; no single final release version was measured.

| Platform | Observed elapsed time | Scope / result |
| --- | ---: | --- |
| Ubuntu amd64, Intel Core Ultra 9 288V / 31 GiB | 165.473 s | Missed two-minute goal; code ran and VM was deleted |
| macOS 26, M5 / 24 GiB | 49.316 s | Adaptive-default candidate; 2048 MiB guest |
| Linux arm64, nested KVM in Lima on M5 | 46.805 s | 2048 MiB guest; nested VM is not bare metal |
| macOS 26, M2 Air / 8 GiB, everyday apps open | 103.468 s | 512 MiB guest after memory/login-context fixes |

Earlier failures included an Air capacity refusal and VZ saved-state failure;
these are not included as successful timings. The first M5 candidate took
38.113 s; 49.316 s is the subsequent changed-default demonstration, not a
selected best trial. Installation time is different from warm sandbox create.

## Historical 0.1.0 measurements, 2026-11-10

The owner set four goals for Lamp: create a Linux sandbox in under 500 ms (p50),
live-fork a running sandbox's memory in under 1 s, run at 97 % or more of native
speed for compute, disk and network, and leak no secrets. This page reports what
was measured, once, for the first release. These numbers did not gate the
release (owner decision O6, amended 2026-11-10). A goal that was missed is
reported as missed, and a goal that was not observed is reported as not
observed, never as zero or passed.

**Summary:** create met its target. Live fork and native speed did not. The
zero-leak goal has partial observations and no complete measurement.

| Goal | Target | Result | Status |
| --- | --- | --- | --- |
| Linux create (BEN-001) | p50 < 500 ms | p50 331–396 ms across 6 storage rows (volatile and local) | **met** |
| Linux live memory fork (BEN-002) | < 1 s | volatile p50 665–713 ms, but 3–7 % of forks over 1 s; local (disk-durable) p50 1.9–5.9 s | **not met** |
| Mac live fork (M5 / Air) | < 1 s | 969 ms (M5) / 1,114 ms (Air), single observations | met once on M5, missed on Air |
| Native speed, compute (BEN-003) | ≥ 0.97 | compile 0.29, compression 0.966, crypto 0.970 (0.96990) | **not met** |
| Native speed, disk | ≥ 0.97 | 2.3–18× native, but not like-for-like (see below) | **not comparable** |
| Native speed, network | ≥ 0.97 | 0.23–0.87 | **not met** |
| Zero leaks (BEN-004) | no secret reaches agent code | targeted observations found no leak; no complete leak measurement exists | **partially observed** |

## Where the numbers come from

All Linux numbers come from the independent measurement harness in
[`benchmarks/`](../../benchmarks/). It drives Lamp only through its public CLI,
on the owner's Ubuntu laptop: Intel Core Ultra 9 288V (4 P-cores and 4 LP
E-cores), 31 GiB RAM, NVMe with LUKS, LVM and ext4, Linux 7.0, on AC power. The
full stack was in the path each time: `lamp` CLI → `lampd` → `lamp-supervisor` →
jailer + Firecracker 1.17 microVM, with each sandbox's guard network (TAP,
`lamp-guard` NFQUEUE, pasta uplink, mediated proxy). Docker was not used.

These runs took place on 2026-11-10 during development (ticket T6). Each run's
manifest records the exact code it measured. **No new run was made on the
release build.** The engine, guard and storage code these runs measured has not
changed in a way that targets speed since then. This is the "available
benchmark data" the release reports, not a fresh release-day run.

Raw data, manifests, host metadata and chained observations:
[`docs/development/evidence/t6/`](../development/evidence/t6/). Full write-up,
including the failed runs: [T6 evidence](../development/T6-evidence.md).

### Create and fork (E01, 2026-11-10, 49,158 observations)

Times run from the caller's request to a real command running in the sandbox.
200 creates and 100 forks per row, with 4 GiB guests.

| Row | Create p50 / p95 / max (ms) | Fork p50 / p95 / max (ms) | Forks over 1 s |
| --- | --- | --- | --- |
| volatile, 0 MiB dirty | 368 / 628 / 986 | 713 / 1072 / 1313 | 7 |
| volatile, 128 MiB | 378 / 593 / 747 | 665 / 938 / 1102 | 4 |
| volatile, 1024 MiB | 340 / 514 / 621 | 680 / 888 / 1126 | 3 |
| local, 0 MiB | 331 / 545 / 780 | 1922 / 3714 / 4542 | all |
| local, 128 MiB | 396 / 650 / 1245 | 2171 / 4284 / 5225 | all |
| local, 1024 MiB | 394 / 607 / 798 | 5944 / 12689 / 15708 | all |
| replicated (all sizes) | refused, `QUORUM_REQUIRED` | not reached | — |

Every fork kept its source running and produced a usable child. There were no
state errors. A volatile fork writes the full 4 GiB guest image to tmpfs
(~320–450 ms), and then restores it. Host jitter on this 4-P-core laptop pushes
3–7 % of forks past 1 s. Local forks wait for the disk. Replicated storage needs
a quorum of three storage hosts, which this single-host run did not have.

### Native speed (E02, 2026-11-10)

Each measure ran ten randomized sandbox/native pairs on the same host. The
ratio is sandbox ÷ native, where higher is better (paired geometric mean).

| Measure | Ratio | Measure | Ratio |
| --- | --- | --- | --- |
| compile | 0.287 | tcp_goodput | 0.258 |
| compression | 0.966 | tcp_packets | 0.865 |
| crypto | 0.970 | udp_goodput | 0.807 (9 of 10 pairs) |
| seq_read | 2.311 | udp_packets | 0.771 |
| seq_write | 4.163 | small_flows | 0.315 |
| random_read | 2.821 | request_latency | 0.860 |
| random_write | 2.638 | proxy | 0.232 |
| fsync | 17.403 | tree | 18.106 |
| cow_first_write | 2.819 | | |

**Why the disk numbers are not comparable:** the engine attaches Firecracker
drives in Firecracker's default `Unsafe` cache mode. A guest flush then stops in
the host page cache, while the native side reaches the NVMe. The disk rows
measure that difference, not a faster sandbox.

**Why compute and network fall short:** every guest process pays the guard's
audit-before-effect cost (about 7.5 ms per fork and exec record), which hurts
`compile`. Every network packet is inspected in user space (TAP → NFQUEUE →
pasta), and connection setup and the mediated proxy add more.

### Mac live fork (T8, 2026-11-10)

On the M5 (Apple M5, 24 GiB) and the MacBook Air (M2, 8 GiB), through the real
Virtualization.framework engine: fork from caller to useful command took
969 ms on the M5 (source paused 503 ms) and 1,114 ms on the Air (source paused
558 ms). These are single observations, not distributions. Source:
[T8 evidence](../development/T8-evidence.md).

## Zero-leak goal

There is no complete leak measurement for this release. No prescribed attack
suite was run, and nothing here certifies that Lamp cannot leak. What was
observed:

- **Broker:** a full guest memory and disk capture of a real Firecracker guest
  that used model and MCP credentials through the broker contained zero raw
  matches for the provider canaries ([T17 evidence](../development/T17-evidence.md)).
- **Hosted relay:** the relay's state and log files did not contain the public
  work marker that crossed it ([T24 evidence](../development/T24-evidence.md)).
  No memory or packet capture of the relay was made.
- **Kits:** vault passwords and session cookies stayed outside the agent VM in
  the external-browser mode. Copied-login mode deliberately puts a cookie in the
  agent VM, and the agent could read it, as documented
  ([T22 evidence](../development/T22-evidence.md)).

Historical disclosed exceptions included in-guest native subscription sign-in,
explicit owner Exposures, copied-login mode and the limited CI job token. This
is not the current managed Codex custody design: 0.1.2 keeps its imported
upstream sign-in on the host; Claude subscription custody is unsupported. `lamp exposure list` shows the
active ones.

## Reproducing

```sh
python3 -m benchmarks.run --suite E01 --cli lamp --output e01 --metadata meta.json
python3 -m benchmarks.run --suite E02 --cli lamp --output e02 --metadata meta.json
```

The harness needs only Python's standard library, plus `cc` for the compile
workload. See [T6 evidence](../development/T6-evidence.md) for the exact
configuration of the runs above.
