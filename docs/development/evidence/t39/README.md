# T39 real networking evidence — 2026-11-12

The canonical HTTP API drove disposable Linux Firecracker sandboxes from a
historical public Debian/Python template in a separate T39 engine store.
No Docker resources, owner projects, browser profiles or real credentials
were used. Every create checked fewer than two Firecracker processes and
at least 12 GiB available. Sandboxes were deleted in `finally` cleanup.

- `core.jsonl`: `hi-from-sandbox` returned 200 through a private bearer preview;
  no bearer returned 403; revocation and expiry returned 403; public access
  returned 200. SSE delivered two events; WebSocket upgrade returned 101 and
  echoed a message. A registry-only policy returned 200 for npm and refused
  example.com; a live edit admitted example.com and advanced effective policy
  revision from 2 to 3. A→B:8080 returned `from-b`, port 22 was refused, and
  revocation refused subsequent access. Control-daemon restart retained the
  live preview and neither bearer was in lifecycle state. Unpublish closed
  its listener. Both demo VMs were released.
- `cli.jsonl`: the real `lamp port expose` command returned its URL;
  `lamp port share-link` with its default wait returned a working bearer once.
  Reusing its idempotency key returned no bearer. The preview returned 200.
- `services.jsonl`: actual loopback dummy HTTP service observed no methods at
  `off` or `ask_first`; `read_only` observed GET while POST returned 403;
  `full` observed GET and POST (200 and 201). Connections were revoked after
  each access choice. The attempt to start a second sandbox stopped because
  another ticket occupied the other VM slot, and cleanup released ours.
- `exits.jsonl`: independent sandbox generations selected a Mac CONNECT exit
  through an authenticated SSH reverse tunnel, Ubuntu direct egress, and an
  unavailable enrolled exit. The first two returned 200 from example.com;
  the unavailable exit failed without a direct fallback. These sandboxes ran
  sequentially to leave shared VM capacity available. This demonstrates
  provider selection across two owner machines, not distinct public IPs.
- `mac-exit.jsonl`: the Mac exit's own durable network events confirm DNS and
  numeric TCP admission for the tunneled request. Heartbeats are omitted.

The historical template had `nameserver 127.0.0.53` without systemd-resolved.
The policy/exit demonstrations therefore queried Lamp's gateway resolver
explicitly with the guest's Python standard library before making real HTTPS
requests with normal certificate verification. Fresh guest init now repairs
missing/loopback-only resolver configuration. These records do not claim that
an existing immutable template automatically receives that init update.

Early development attempts failed: sidecar authority creation after dropping
privileges could not traverse the audit ancestor (fixed by opening authority
and publication state before dropping); a stale readiness socket was removed
from the disposable launcher; the template had no curl; Python exhausted the
default small process-audit reservation (the demonstration enrolled 16 MiB
segments); a generated app was initially placed under inaccessible `/root`
(the app moved to `/tmp`). An expiry request timed out in an earlier attempt;
subsequent core attempts returned 403 on expiry. That timeout remains a failed
observation, not a certified timing result. No performance claim is made.

Local validation: `CGO_ENABLED=0 make build`, scoped `go vet` for control,
network and both changed commands, and `git diff --check` passed. The updated
Linux supervisor and guest init also built on Ubuntu. No new tests or broad
smoke suites were added. Native macOS VZ still has no guest data NIC and rejects
managed network calls; the Mac served only as an owner-enrolled outbound exit.
See [the usage notes](../../t39-network.md) for commands and limitations.
