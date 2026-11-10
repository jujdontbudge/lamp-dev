# T4 review round 1: connection-granular audit and TCP lifecycle

Both S1 and S2 are addressed. `origin/main` (including T1's fb636b3) is merged;
the temporary denied-probe workflow is removed. No confinement, queue-bypass,
process-audit or host-network policy restriction was relaxed.

## S1: remove per-packet storage barriers without losing fail-closed behavior

A SYN/first UDP datagram gets a durable connection admission before its verdict.
Established packets update counters only. Counter snapshots, denial summaries
and a heartbeat share one bounded durable batch at a 250ms maintenance interval.
Idle polling is 100ms; no packet is needed to discover a full spool or expire a
flow. Every verdict and every completed storage barrier checks the one-second
audit deadline. A late successful barrier cannot rescue a missed deadline.
Storage/queue/liveness failure exits the guard, leaving its non-bypass queue rules
installed, including for established traffic.

Pending denials aggregate by tuple/reason and are capped at 128 identities. A
repeat-denial flood does not force fsync per packet. Capacity overflow flushes
the pending batch and one fault, then closes the path instead of silently losing
identities or continuing an unbounded sync storm. Batch bytes are capped at
4 MiB, individual records at 64 KiB, and the reserved spool quota still applies.

Focused tests (both Linux architectures):

- **`established_flow_adds_no_per_packet_durable_writes`**: one SYN makes one real
  `sync_data` call; 1,000 established data packets add **zero**. One counter batch
  adds one storage barrier and includes all observed bytes. Expired liveness
  rejects another established packet.
- `denials_are_batched_and_capacity_fails_closed`: 1,000 repeated denies add zero
  barriers until one batch; overflowing the identity cap records one fault and
  returns a fatal error.
- `idle_tick_expires_and_audit_failure_stops_established_traffic`: idle expiry and
  full-spool detection run without another packet/admission.
- Real-kernel `idle audit spool exhaustion...`: establish a UDP socket, then
  produce **no more admissions or packets**. Periodic audit writes fill the
  small quota; both that existing socket and new traffic are blocked afterward.

## S2: do not merge a reused TCP tuple into its predecessor

RST durably ends and removes a connection. First FIN durably closes its record;
a bounded drain entry permits half-close data and final ACKs, with batched final
counter updates. Any fresh SYN on a closed tuple gets a new admission. An open
tuple with the same SYN sequence number is a retransmission; a changed ISN ends
the previous identity and admits a new connection.

- **`tcp_rst_then_reconnect_has_two_admissions`**: identical tuple, RST, reconnect;
  two start records, distinct IDs and reset counters.
- `fin_closes_and_same_isn_retransmission_does_not_readmit`: duplicate SYNs do not
  re-admit; FIN closes; reuse gets a fresh identity, including a changed ISN.
- Real-kernel **`tcp_rst_then_reconnect`**: SO_LINGER RST, reconnect from the same
  local port, real echo traffic both times. Two durable admissions/endings have
  distinct IDs and start times, with fresh counters. See the source-port-19001
  rows in the [LX](evidence/t4/review-r1/lx/network.ndjson) and
  [LA](evidence/t4/review-r1/la/network.ndjson) spools.

## Rough LX throughput, not E02

The same isolated edge receives **16 MiB** over TCP, with guard, protected process
collector and durable external audit enabled in both runs. Both binaries are
release builds, with the same requested 16 KiB socket buffers. Spools reside on
**disk-backed ext4 at `/var/tmp`**, not `/tmp` tmpfs. Owner services remained up;
this is one quick pair, not a native baseline, randomized gate or >=0.97 claim.

- Before (`12eace3`): **0.4107 MiB/s**, 38.9558 seconds.
- After: **124.1010 MiB/s**, 0.1289 seconds.

[Raw paired log](evidence/t4/review-r1/lx-throughput.log),
[before result/binary hash](evidence/t4/review-r1/before/throughput.json),
[after result/binary hash](evidence/t4/review-r1/after/throughput.json),
[chart](evidence/t4/review-r1/throughput.html).
The network spool had 18,043 records before versus four after this short transfer;
that is a record count, not a general equation between records and barriers.
The focused S1 test explicitly counts actual successful `sync_data` calls.

An initial unrestricted-burst smoke run hit the old guard's ENOBUFS fail-closed
path rather than completing a transfer. The matched 16 KiB buffers avoid that
burst artifact for this comparison; no queue, policy, audit or safety control was
disabled. T6 must still run E02, including CoW/capture tracking and native trials.

## Reproduction and complete checks

- [LX](evidence/t4/review-r1/lx.log) and [LA](evidence/t4/review-r1/la.log):
  **12 Rust tests, strict clippy and all 13 real-kernel integration assertions
  pass**, including all previous fail-closed assertions and the new RST case.
  Both integration runs use ext4-backed `/var/tmp` spools. LA is a functional
  nested arm64 lane, not native-speed evidence.
- [M5 `make verify`](evidence/t4/review-r1/m5-verify.log): passes after merging main.
- [Tested source hashes](evidence/t4/review-r1/source.sha256),
  [LX summary](evidence/t4/review-r1/lx/summary.json),
  [LA summary](evidence/t4/review-r1/la/summary.json), and actual host spools in the
  adjacent directories. Binary/test hashes identify the tested implementation.

```sh
cargo test -p lamp-guard --locked
cargo clippy -p lamp-guard --all-targets --locked -- -D warnings
cargo build -p lamp-guard --release --locked
sudo -n env TMPDIR=/var/tmp python3 tests/guard/e2e.py "$PWD/target/release/lamp-guard"
# Run once per binary on LX for the rough comparison:
sudo -n env TMPDIR=/var/tmp LAMP_GUARD_BENCH=label \
  python3 tests/guard/e2e.py /absolute/path/to/release/lamp-guard
make verify
```

No host firewall/sysctl, Docker object, owner service or paid service was changed.
Forwarding changes remain confined to the disposable namespaces. The LX log
confirms Chrome remains active. Existing T2/T6 engine/fork/IPv6 and T23 ledger
qualification gaps in [the integration contract](T4-guard.md) remain explicit.
