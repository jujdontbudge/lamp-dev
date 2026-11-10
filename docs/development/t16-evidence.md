# T16 feature evidence — 2026-11-10

Implementation: `t3/fleet-host-placement`. The owner rule governs this work:
feature demonstrations on owned machines, no E-numbered gate runs, benchmark
loops, timing targets or soak/attack suites. No new test suite was added.

The demonstration driver is [tools/demo-fleet.py](../../tools/demo-fleet.py).
Every invocation uses a new output directory and retains failed attempts.
Private bearer/certificate keys stay in private `/tmp/l16-*` directories and
are not included in the repository. The transcripts contain public replies,
binary SHA-256 digests and resource identities; they omit one-time key secrets.

Machines: Ubuntu laptop (`LX`, Linux x86_64), this M5 (`M5`, Apple Silicon macOS)
and the owner's MacBook Air (`AIR`, Apple Silicon macOS). All orchestration is
headless. The existing T10 prepared template is used with a ticket-owned
supervisor; no template is built or modified. Before every allocation the driver
records `pgrep -c firecracker` and `free -m`, rejects two existing processes or
less than 12 GiB available, and releases each allocation before the next one.
Owner services and Docker are untouched. No Mac sudo or password dialog is used.

Peer transport uses a direct SSH connection for each participating host pair,
over Tailscale, with end-to-end host mTLS inside it. The two-host case has only
LX and M5 as voters; the Air is not connected to that fleet. The three-host case
has all three pair connections, including AIR–LX directly. Public ingress
connections use bearer authentication and server TLS, without a host certificate.
A single `lamp+fleet:///…/discovery.json` address names those public ingresses.

The completed journey is [demo-8/transcript.jsonl](evidence/t16/demo-8/transcript.jsonl).
It exited successfully and confirmed zero remaining ticket sandboxes. Its three
phase logs include the actual Raft voter configurations and coordinator changes.

| Feature | Real observation |
| --- | --- |
| One address; one/two/three hosts | Every CLI call used the phase's single `lamp+fleet:///…/discovery.json` URI. Discovery returned one, two and three enrolled hosts. LX coordinated the first two fleets; AIR coordinated the three-host fleet. |
| Shared key namespace | A key issued through M5 authenticated at LX. After quorum committed its deletion through M5, LX rejected it with `UNAUTHENTICATED`. No bearer is retained in the transcript. |
| Automatic placement and real execution | Create entered through M5 and produced `sb_b76d89664aa66b1e18012001675eac41_h_hs_lx`, ready on the real LX worker. Its guest command returned the actual stdout `t16-real-guest`. |
| Stable retry identity | Replaying that create through LX returned the same operation `op_1ffa3166541e740a315a43ce5b180c47_h_hs_lx` and sandbox, with `replayed:true`. |
| Strict placement | A create pinned to the control-only M5 returned `NO_CAPACITY` and allocated no fallback sandbox. |
| Unavailable custody | With the LX ticket daemon stopped by SIGSTOP, M5 listed and inspected the same sandbox as `unavailable`, retaining `host_id:hs_lx` and `custody_host_ids:[hs_lx]`. Retrying its old keyed create returned `IDEMPOTENCY_HOME_UNAVAILABLE`. |
| Permitted survivor work | With only M5's ticket daemon stopped, the ordinary CLI create supplied **no retry key and no host pin**. It discovered and created ready sandbox `sb_69cf336d294ea4bd7f29a97141a476c5_h_hs_lx` from LX's existing slice through the same fleet URI. A new key grant returned `QUORUM_REQUIRED`. |
| Rejoin | M5 resumed, both hosts returned active discovery, and the quorum key deletion succeeded. |
| Three real control hosts | AIR committed the fleet name `T16 three owned hosts`; M5 read that same name and AIR coordinator ID. AIR–LX had its own direct pair connection. |
| Stale backup rejection | A pre-key snapshot of the real one-host state was reopened against its independently advanced guard. Startup rejected it with `home journal rolled back; reconcile or explicitly fence a new authority epoch`. |

The two actual allocation guards observed zero existing Firecracker processes
and 25,560 / 25,620 MiB available respectively. Each sandbox was released by
revision-checked public deletion before the next allocation. The ticket worker,
its grant and the ticket's peer tunnels were stopped/revoked at cleanup. A later
machine observation saw another ticket's Firecracker process; it was untouched.

Earlier attempts remain in `evidence/t16/demo-1` through `demo-7`:

- `demo-1`: script omitted the required key expiry; the API rejected it.
- `demo-2` and `demo-3`: direct custom-port ingress to the M5 timed out, so
  consensus never formed. No firewall/Tailscale settings were changed.
- `demo-4`: pairwise transport formed a real two-host fleet and a shared key
  authenticated at LX. The generic installed `demo` template then rejected the
  worker's `audit` field. Its allocated sandbox was subsequently released with
  revision-checked public `sandbox.delete`; the correction is retained beside
  the original failed transcript.
- `demo-5`: binary transfer lost its SSH connection. No sandbox was allocated.
- `demo-6`: the chosen public API port was already in use at daemon startup.
  No sandbox was allocated. A separate cleanup record confirms the private
  worker was stopped and its exact grant revoked. The driver now checks fresh
  ports before starting.
- `demo-7`: real routing, guest output, retry identity, unavailable custody and
  survivor create worked. The immediate key deletion during rejoin returned
  `QUORUM_REQUIRED`; the failed attempt and daemon logs remain retained.
  The final driver retains the same revoke identity while authority recovers;
  the Raft lease configuration tolerates reconnection jitter. This changes no
  benchmark threshold and no speed qualification is claimed.

Coverage limits are material: this checkout has no Mac execution backend, so
M5/AIR run control roles with zero compute slices. New real sandbox work is
shown on LX after the M5 daemon becomes unavailable. The reverse case—new VM
work on a Mac after losing LX—is not demonstrated. SIGSTOP targets only ticket
processes; physical system sleep, wall-clock changes, delayed kernel I/O and
transferable storage/VM leases were not exercised. Existing sandbox/exec
metadata represents location/custody; full product Session and file-transfer
resources depend on their respective implementations. Enrollment is an operator
provisioned fixed manifest; online invitation/removal/rebalancing and fenced
recovery of a fleet-wide rollback are not implemented. These limits prevent a
claim that the entire original fleet journey or E10 is qualified.

Verification: the single required `make verify` invocation passed (exit 0).
The retained [verification log](evidence/t16/make-verify.log) includes workspace
builds/tests, contract checks, interoperability, generated checks, Go vet, Rust
formatting and Clippy. No new tests were added and no benchmark/trial loops ran.

The single review identified four defects: authority CAS conflicts disabled the
requesting host, exec admission used an empty source sandbox, replicated engine
receipts bypassed their authorization home, and ingress-local events omitted
remote work. The fixes distinguish definite CAS rejection from ambiguous or
durability failures and roll back staged edits; pass the actual source sandbox
to exec admission; keep terminal engine receipts routed home; and merge
authorized per-host event pages with signed resumable vector cursors for JSON
and SSE. The cursor retains unsent and unavailable-host positions.

Per the owner's review instruction, no new tests or demonstration/trial runs
were added. Review-fix verification: the single `make verify` invocation passed (exit 0);
the complete [review verification log](evidence/t16/review-make-verify.log) is retained.
