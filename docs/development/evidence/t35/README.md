# T35 guest usability evidence

Recorded 2026-11-12 on the owner's Ubuntu laptop and M5 Mac, using disposable
stores and public package registries. No owner projects, accounts, credentials,
browser profiles, Docker resources, or visible desktop were used.

## Real demonstrations

[linux-api.json](linux-api.json) records the HTTP API run against Firecracker:

- The enrolled engine lists disk support; a display request receives synchronous
  `DEVICE_UNAVAILABLE`, then an ordinary create succeeds.
- Audited task root sees `0:0` ownership and writes to `/etc`, `/root` and
  `/usr/local`; its full subordinate UID map is shown.
- The gateway resolver resolves example.com and registry.npmjs.org; curl receives
  HTTP/2 200. curl, wget, git, Python, Node, npm, SSH and sshd are present.
- apt installs jq, pip installs humanize, npm installs TypeScript/tsx/Node types,
  and tsx prints `42`.
- Direct DNS to 8.8.8.8 times out while mediated lookups work.
- A 1500 MiB allocation in the 1024 MiB guest reports `MemoryError` and exit 1.
- A background 3000 MiB disk write, flush and removal completes. The sandbox
  remains `ready`. The background form avoids the existing 60-second API command
  deadline; progress records are actual observations, not a throughput result.
- The shared memory-capacity check refused a subsequent fork, and cleanup
  deleted the source. Fork persistence is demonstrated separately below.

[linux-fork.json](linux-fork.json) records a separate API run: jq and a marker
survive a memory fork; the child resolves example.com and receives HTTP/2 200.
An attempted hard link to the trusted collector is refused. Killing only the
disposable child's VMM changes its public state to `failed`, sets reason
`VMM exited`, and emits a failure event carrying that reason. Both sandboxes are
deleted. Event evidence was retrieved through all API pages after the initial
page did not include the new child.

[mac-worker.json](mac-worker.json) records a headless, confined VZ Linux worker
connected to the same Seatbelt-confined Ethernet bridge used by the engine.
The gateway, interface, DNS, HTTPS, package installs and TypeScript work;
metadata HTTP is blocked. This worker-level run used a disposable disk and
current non-root account. It does not exercise the privileged Mac supervisor,
its hidden worker accounts, native macOS guests, or the Air. No Mac password or
approval dialog was requested.

## Failures found during implementation

The first Ubuntu attempt used `/tmp`, which is tmpfs and cannot provide disk
reflinks. Data was moved to disk and a ticket-specific XFS store was used.
Several attempts correctly stopped at the shared two-VM/12-GiB safety check.

The initial writable-root implementation exposed both the kernel's prohibition
on creating a user namespace after chroot and an incorrectly directed UID map.
The implementation now pivots away from the original mount root and maps disk
UID 0 to the task's subordinate kernel UID. Trusted boot inodes are immutable;
read-only bind mounts alone would not protect against hard links.

The resolver initially tried to connect its upstream socket before pasta had
installed the route. It now sends to a fixed numeric upstream through a
pre-opened socket and validates response peers.

The first Mac launch used the wrong kernel filename. The helper profile also
needed dyld's shared-cache paths and access to the root directory for loader
startup. A package run during the network outage installed jq and humanize but
timed out during npm; the subsequent recorded run completed successfully.

A targeted HTTPS check after memory fork initially failed DNS: the guest kept
its parent's gateway ARP entry while the new TAP had a random MAC. Isolated
links now use a stable gateway MAC. The final fork run records successful HTTPS.

The large Linux write reproduced a host cgroup OOM kill with the original
hard-limit-only setup. Adding early reclaim with synchronous block I/O avoided
the kill but stalled execution. The final asynchronous disk backend plus early
reclaim completed the recorded write and kept the VM ready. The device graph is
versioned so old saved templates must be rebuilt. These earlier failures remain
failures; the final run is separate evidence.

## Local checks

- `make build`: passed (Go build, generated contracts/SDK consistency, Rust
  workspace build).
- `CGO_ENABLED=0 go vet ./internal/control ./internal/network ./internal/surface ./cmd/lamp-network`:
  passed.
- Linux native supervisor/guard release builds and musl seed builds: passed.
- `git diff --check`: passed.

No new automated tests or benchmark runs were added for implementation.
Sol review subsequently supplied one targeted confinement regression test;
[review ledger](sol-review.md) records its failure and the fixes.

## T39 integration on M5

[mac-managed-network.json](mac-managed-network.json) records the real confined
Linux VZ worker and Seatbelt-managed helper on the M5 after rebasing onto T39.
An initial deny policy was installed before boot. A Python HTTP server inside
the guest returned `T35 preview on M5` with HTTP 200 at
`http://p-a6ae509f4e10d1f736bd81a23eeccee5.localhost:59556`.
In that same guest, `curl https://example.com` was denied, succeeded with
HTTP/2 200 after a live host allow rule, and was denied again after removing
that rule (policy revisions 2, 3 and 4). The preview still returned HTTP 200
after both edits. It was unpublished and the disposable VM/helper stopped;
that URL is historical evidence, not a running service.

This uses T39's actual managed helper control protocol and private VZ Ethernet
transport. No privileged Mac supervisor or public HTTP API was run; the worker
and helper were launched directly under the current account with their real
confinement. Native macOS guests and the Air remain unexercised. The existing
Mac guest-helper configures its interface/gateway/DNS; both guest classes use
the same engine attachment and management calls.
