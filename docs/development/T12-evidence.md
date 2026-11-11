# T12 network evidence (2026-11-10)

**Owner-scoped result: all five requested network features demonstrated.**
These are real packets and HTTPS requests through host-owned private Ethernet
and Linux network namespaces, not a public-API/VMM or Mac qualification. The
[implementation boundary and remaining integration](T12-network.md) are explicit.
Historical regression evidence below predates the 21:30Z owner instruction.

## Single PR review correction

Pulled the owner's updated branch `d7fbbc0` (main `09899fd`) before editing.
All four findings were accepted and corrected:

1. SSH-exit DNS now uses a bounded request to the owned endpoint through the
   selected tunnel. Resolution runs at that exit; local resolver injection and
   fallback were removed. Returned numeric candidates are still policy checked.
2. The 30-second limit now applies only to unused admission permits. Established
   streams retain policy/provider/context/link fencing without a lifetime timer;
   bridge, CONNECT, mapped TCP and dev ingress no longer impose that deadline.
   UDP/DNS resource expiry is idle-based and refreshed by activity.
3. The private control decoder renews its 1 MiB budget per JSON value, accounting
   for read-ahead without dropping buffered input or requiring new framing.
4. Published ports renew their cached leaves before the 24-hour expiry on TLS
   handshakes. Expired leaves are replaced, active sessions are undisturbed and
   existing CA trust is preserved; expired CA authority is never extended.

No tests were added or extended, no trial/demonstration was rerun and no extra
review was delegated. The one requested `make verify` run for these corrections
**passed (exit 0)**; full output is recorded in
[`review-verify.log`](evidence/t12/review-verify.log).

## 21:30Z owner scope: one real demonstration per feature

No new tests or extensions to tests were written after the instruction.
Uncommitted development test additions were removed. The independent caller is
now `tools/network-demo-peer`, a demo tool without Lamp runtime imports. No SaaS
work, trial, benchmark, soak or repeated qualification suite was started.
Headscale v0.29.7 and tsnet v1.96.5 supplied the private identity path. The owner's
main tailnet, policy, daemon and Docker objects were untouched.

The live demonstration was serialized with
`flock /tmp/lamp-lx-timing.lock`, acquired as the SSH user before sudo. There were
at most two network-namespace guest endpoints. Admission observed 25,857 MiB
available for the first phase and 25,644 MiB for the continuation, above the
12,288 MiB floor. Final available memory was 25,538 MiB. No owner service was
stopped. All demo processes, the disposable Headscale server and namespaces
were released; both completed phases recorded no cleanup failures. These are
functional observations, not timings, ratios or throughput claims.

### Exit, private identity, allow and deny

Public transcript: [`exit-identity-policy.json`](evidence/t12/exit-identity-policy.json).
Selected host audit: [`exit-identity-policy-audit.json`](evidence/t12/exit-identity-policy-audit.json).
Raw private artifacts remain on LX at
`/var/tmp/lamp-t12-demonstration-gfnjizhe`; do not export member state, enrollment
keys, CA private keys or the control database.

- **Exit:** sandbox A selected the owned numeric CONNECT exit; sandbox B's
  independent selection was direct. A normal guest `curl -4 -fsS
  https://example.com/` used the bridge's DNS gateway and CONNECT data path,
  verified the public TLS certificate and returned 577 bytes, exit 0. Body
  SHA-256: `25ddf2c883e0d1958ea971d279a7e4f0fd446724ee3db7db19dadabd4a62e484`.
  The exit's separate audit records its numeric upstream dial. This demonstrates
  one exit, not three preverified public egress IPs or an SSH/VPN failure row.
- **Private identity:** guest TCP and UDP useful bytes reached the independent
  peer. Its real `WhoIsProto` observation identified node `3`,
  `lamp-demo-sandbox-a.lamp-t12.test`, source `100.127.240.3`, not the host's
  Tailscale identity. Sent/echoed SHA-256 was
  `a193bdfaf727bc97ea2720100f1500ebb04ee8826928814484b808ccd286b474`.
  The independently enrolled B member was `100.127.240.4`.
- **Mapped inbound:** that peer reached A's explicit TCP and UDP member port
  `18080`, mapped to guest port `9090`, with actual echoed useful bytes. This
  also confirms the explicit gateway-source fix for inbound guest dials.
- **Allow and deny:** the host admitted TCP/UDP peer service `18880`. An explicit
  deny on TCP `18881` blocked the guest despite an exact private-service grant
  and a real listening peer service. The host audit records the denial. This
  is transport policy, not strict-site inspection or a zero-packet sink-counter
  qualification claim.

### Dev URL, share lifetime and sandbox-to-sandbox

Public transcript: [`url-links.json`](evidence/t12/url-links.json).
Selected host audit: [`url-links-audit.json`](evidence/t12/url-links-audit.json).
Raw private artifacts remain at `/var/tmp/lamp-t12-demonstration-un3xuy87`.
The successful exit/identity/policy actions were not repeated in this phase.

- **Dev URL:** a real guest HTTP server on `8080` was published at
  `https://p-c2e299186b8912f168219ebac3369e56.lamp-t12.test:18443`.
  The disposable Headscale DNS zone resolved that name; the independent client
  verified its hostname/certificate against the host's public private-network
  CA. The host's explicitly authorized signed-in peer received HTTP 200 and
  app `lamp-dev`; a different enrolled peer received 403. Sign-in here is the
  cryptographic private-network node identity, not an Eilmer OAuth service.
- **Shares:** a fresh one-use fragment bearer exchanged for a Secure/HttpOnly
  host cookie (HTTP 204), then the otherwise denied peer received the app (200).
  The app reported no forwarded cookie, Authorization or host identity header.
  Revoking grant `pg_ImWiIBgNSpUVbkX2yHwUMs` closed an already-open HTTPS stream;
  subsequent access returned 403. A separate explicitly expiring grant exchanged
  successfully, then returned 403 after its deadline. No bearer URL, token,
  cookie, exchange body or CA private key is in the transcript or audit. The
  fragment landing page is implemented, but no browser UI qualification is
  inferred from this HTTPS-client demonstration.
- **Sandbox-to-sandbox:** A could not call alias `198.19.0.2:9090` before the
  host created directional connection `cn_5dc8ea3531ae5ecfc2214e7afe9b68a6`, bound
  to A generation 1 and B generation 1. After creation, real bytes reached B's
  private guest endpoint and echoed the SHA-256 shown above. Port `9091` and
  reverse B-to-A access remained blocked. Revocation acknowledged and a new
  A-to-B call was blocked. Unpublishing the dev port also acknowledged.

Both phases used Linux network binary SHA-256
`dec7087e92c3d57071fdfebd2ac04835180e4b8a3c41f7167548b4a7473aa252`.
The final source additionally tightens standalone descriptor-role and provider-
subject validation and syncs the origin ledger's parent directory at startup.
Those admission/persistence tightenings were not used to repeat a demo.

### Incomplete attempts retained

- `/var/tmp/lamp-t12-demonstration-gw2ezpfj`: setup stopped because the private
  audit spool refused a traversable parent directory. Audit files were placed
  under a separate mode-0700 directory; the refusal was not relaxed.
- The first completed feature phase (`gfnjizhe`) could not open its dev URL with
  the independent caller's generic SDK hostname dial and stopped before links.
  The continuation uses the peer's explicit private `QueryDNS` context before a
  numeric dial, while retaining normal TLS hostname verification. Only the
  missing URL/link features were then demonstrated. The incomplete URL outcomes
  remain in the first transcript, not relabeled as denials or successes.
- Earlier private-network attempts (`und1nyrt`, `u81oaqjg`, `ztznbp5_`,
  `ffeeq5zl` under `/var/tmp/lamp-t12-private-network-*`) included process SIGKILL,
  all-thread privilege and inbound-source issues. The latter two code defects
  were corrected; an unexplained process SIGKILL is not a network denial proof.

## Final workspace verification

The owner's single final `make verify` invocation **passed (exit 0)**, including
workspace tests, independent harness tests, contracts, interoperability, codegen,
vet, formatting and clippy. Full output is in
[`owner-verify.log`](evidence/t12/owner-verify.log). Main `2ce9e8c` was merged first; the conflict in
`tests/benchmarks/test_harness.py` was resolved using main's complete version,
without adding or extending a test. No edits to `docs/spec` or `docs/tickets`
were made by this continuation.

## Historical successful runs (before the owner scope change)

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

## Limits of the historical transport-only artifacts

In those historical artifacts alone there are no three external exit reflectors,
VPN-failure test, own-sandbox
Tailscale peer observation, mapped inbound UDP membership, private signed-in URL
or expiring/revocable share in these artifacts. No strict-sites, DNS rebinding,
nested Docker, actual guest/VMM or Mac packet path was tested here. IPv6 is a
negative-only check: the demo refuses it entirely. The default-public/allowlist
IPv4 positives do not supply the missing useful IPv6 coverage. No speed result
or claim that full T12 networking is finished is made.
