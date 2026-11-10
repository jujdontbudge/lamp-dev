# T12 transport-policy regression evidence (2026-11-10)

**Status: partial implementation; T12 / E09 not passed; T26 remains blocked.**
This is real Linux-kernel correctness evidence in isolated namespaces, not a
microVM, cross-host identity, provider-exit or Mac qualification. The full
[implementation boundary and remaining work](T12-network.md) are explicit.

## Successful runs

Artifacts are in [`evidence/t12/`](evidence/t12/):

- `m5-verify.log`: final `make bootstrap verify` passed (workspace builds/tests,
  contracts, independent benchmark tests, interoperability, formatting and
  lint). A test-only startup race was fixed: Unix `bind()` creates the resident
  socket pathname before `listen()` accepts connections. The oracle now waits
  for a real state response within its existing five-second readiness bound;
  no measured workload, ratio or qualification threshold was changed.
- `lx-rust.log`: Linux x86_64 `cargo test --workspace --locked` and workspace
  clippy passed. All **28 guard tests** passed, including revision CAS, immutable
  parent intersection, denied exception overrides, unsupported selectors,
  descriptor/message validation, flow fencing and failed durable swap.
- `lx-network-repeat.log`: five consecutive complete runs of the new real-kernel
  test passed against the final binary (earlier versions also had successful
  repeats). No owner services were
  stopped, no host firewall or global route changed, and Docker was untouched.
- `summary.json`: the last repeat's ten checks, per-phase independent packet
  counters, no failure and no cleanup errors. The matching host configs,
  `network.ndjson`, `proxy.ndjson` and `proxy-requests.ndjson` are retained.
- `lx-t4-regression.log`: all 16 existing T4 checks passed against the final
  binary, including collector confinement, guest root spoofing, guard SIGKILL
  with live TCP/UDP, audit-spool exhaustion, and FIN/crash incomplete endings.

Final real-kernel run:

- OS/kernel: Linux `7.0.0-42-generic`, x86_64, lane LX.
- Guard binary SHA-256:
  `e085c6fd73e736e1ae9104a8b59774717f4d1fbcad81fa5fd41304b40cb67cd4`.
- Raw artifacts on LX: `/var/tmp/lamp-t12-network-q15zxpup`.
- The independent sink rules count every TCP/UDP packet at prerouting, including
  SYNs/datagrams that never establish an application connection. Denied tuple
  counters remain zero; comparisons do not mistake late FIN/ACK packets on an
  unrelated **allowed** tuple for denied traffic.
- Public TCP/UDP and exact private TCP service positives succeeded. Explicit
  denies, non-allowlisted destinations, immutable safety exclusions, wrong
  ports/transports, metadata, neighbour and IPv6 negatives delivered zero packets
  to their independent sinks. IPv6 denial was also witnessed in the guard spool.
- Revision 2 fenced already-established TCP/UDP and the previously approved
  private exception. Stale revision was rejected; revision 3 could not remove
  the immutable parent allowlist. Closing the supervisor channel stopped the
  guard; existing and new packets remained blocked by the kernel queue rules.
- The separately confined numeric HTTP proxy returned the real allowed fixture
  response and denied origins received zero packets, even when a legacy exact
  exception would otherwise admit one.

## Failures retained, not promoted to passes

All failed/incomplete run directories remain on LX. Early fixture development
runs exposed nft script syntax, an echo-fixture closure bug, and a comparison
which counted an unrelated allowed flow's delayed FIN/ACKs; those fixture bugs
were corrected before the final repeats.

Two later failures are **not explained or waived** by the subsequent passes:

- `/var/tmp/lamp-t12-network-jtv9tblg`: revision 3 acknowledgement exceeded the
  driver's three-second timeout. `failed-revision-timeout.json` retains the
  observed phases and failure. This run used the preceding guard binary
  `74da5af095530301f92a0b8b8a807f1e4c6a7971c4c3dd57bbe01f234ab71a59`, before
  adding helper-state diagnostics to the test driver and reusing the control
  receive buffer instead of allocating 60 KiB before every verdict. No causal
  explanation is established by that allocation improvement.
- `/var/tmp/lamp-t12-network-lyazfzjo`: the initial allowed UDP probe did not
  complete within the driver's 250 ms timeout. `failed-positive.json` retains
  the failed result. No guard error was present in its stderr. This is a failed
  useful-coverage attempt, not a successful denial result.

No service was stopped to quiet the lane, no probe/revision timeout was raised to erase these
failures, and no E09 pass is inferred from retrying. Investigation/qualified-lane
replication remains necessary. A combined test command also hit its outer
180-second harness timeout; the T4 summary showed completed assertions, but the
subsequent standalone run with an observed successful exit is the one cited
above. The final full workspace verification passed after correcting the
independent test's bind/listen readiness race.

## What this evidence cannot establish

There are no three external exit reflectors, VPN-failure test, own-sandbox
Tailscale peer observation, mapped inbound UDP membership, private signed-in URL
or expiring/revocable share in these artifacts. No strict-sites, DNS rebinding,
nested Docker, actual guest/VMM or Mac packet path was tested here. IPv6 is a
negative-only check: the demo refuses it entirely. The default-public/allowlist
IPv4 positives do not supply the missing useful IPv6 coverage. No speed result
or claim that full T12 networking is finished is made.
