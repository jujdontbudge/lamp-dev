# T4 Linux guard and protected process collector

For subsequent numeric policy layers and the limited host-only live revision
channel, see [T12 transport-policy foundation](T12-network.md). That update
supersedes the static-policy statements below only for the documented path;
full T12 networking and E09 remain incomplete.

This is an executable **correctness-first demo path**, not an E02 speed result or
a claim that the T2 microVM is already integrated. The real-kernel fixture uses
isolated network namespaces, a root task, both collectors and a durable external
spool together. [Evidence](T4-evidence.md). No Docker or host firewall is involved.

## Supervisor / engine integration (T2 and T6)

Do all of these before releasing a command. Never substitute an unrestricted
root shell or skip a collector to meet a timing target.

1. Allocate a **private host guard network namespace per sandbox generation**.
   Give it only the assigned TAP and a host-controlled uplink; enable IPv4
   forwarding inside it. Pass the TAP **FD**, not namespace authority, to the
   confined engine. The engine has no ambient host sockets, TSI, Unix forwarding,
   host filesystem shares or alternate NIC. The task cannot enter this namespace.
   The interface index from NFQUEUE, not a packet's MAC or IP, binds identity.
2. Allocate unique, non-owner, non-task host service UID/GID(s), and a unique
   subordinate task UID/GID in the guest (all >=65536). Set pids/memory/CPU/disk
   limits externally. The guard allows at most 4096 flows, a 1024-packet kernel
   queue and a 30-second idle flow deadline. No unbounded packet/flow buffers.
3. Inventory **every host address, on-link LAN prefix (even public-numbered),
   management/fleet route and provider-specific control endpoint**. Supply these
   as `host_denies`, in addition to the built-in special-use ceiling. Do not use
   only RFC1918. Keep host routing stable for the generation; a host route/address
   change requires fencing networking and rebuilding this guard before release.
   No dynamic policy update or endpoint exception is implemented in T4.
4. Prepare fresh host-only spool paths in a supervisor-owned directory (0700).
   `lamp-guard run CONFIG` runs inside the guard namespace as root, binds NFQUEUE,
   installs its own `inet lamp_guard` table in one nft transaction, confines itself,
   durably records readiness and prints a single JSON `state=ready` line. Wait for
   that line. Never remove the table to recover a failed guard. Explicit teardown
   is safe only after stopping the VM/task and disconnecting the TAP. An existing
   table, queue conflict, invalid config or unavailable spool rejects admission.
5. Set up one private, connected stream from the trusted guest collector to a
   host `lamp-guard receive CONFIG FD`. An inherited Unix stream is exercised by
   the fixture. The framing is stream-socket agnostic; **AF_VSOCK wiring itself
   is not yet exercised**. T2 must bind each stream to the sandbox generation,
   withhold it from task code and expose no public listener. It is not
   `lamp.worker/1` and carries no control grants or guest RAM. `receive` obtains
   attribution from its host-owned config, never a guest/task message.
6. In the guest, with unprivileged BPF disabled, execute
   `lamp-guard collect FD TASK_UID -- /absolute/program arg...` as trusted guest
   root. Launch **all** Lamp/SSH/console/nested work through this root tree. The
   collector creates a separate PID/mount/user namespace, detaches the inherited
   `/proc`, mounts a task-only procfs, maps namespace root to the assigned
   subordinate UID/GID, empties groups/environment and closes inherited FDs.
   Only PATH and HOME are supplied, overridden by exactly the requested task
   environment. The caller passes it as a JSON object on a dedicated
   descriptor (`collect … [status:FD] [env:FD] -- argv`), which the collector
   reads to EOF, up to 8 MiB, before anything runs. A pipe has no 128 KiB
   per-string argv/env limit, so the public contract's maximum fits (64 values
   of 4096 characters). Names must be non-empty and contain no `=` or NUL;
   values must contain no NUL. The collector's own environment never reaches
   the task. `no_new_privs` is inherited. Task root has no
   capabilities in the ancestor kernel/network namespace, cannot see/signal the
   tracer, and cannot acquire its audit socket. Do not grant a task unrestricted
   guest-kernel root or initial-user-namespace capabilities. No audit process
   inside a VM can resist a task allowed to replace that VM's kernel.
7. Watch all helpers. On collector/receiver/stream loss, fence admissions and
   stop the affected VM/attachments within 5 seconds. The tracer already kills
   its entire traced task tree through `PTRACE_O_EXITKILL` on ACK loss, including
   a CPU-only task. This does not replace the supervisor's VM lifecycle watchdog,
   capture/restore coordination or guest-kernel confinement inventory.

Example host config (supervisor substitutes paths, identity and route inventory):

```json
{
  "attribution": {
    "principal_id": "owner", "host_id": "lx", "task_id": "task-1",
    "sandbox_id": "sb-1", "generation": 1, "session_id": "session-1",
    "operation_id": "op-1", "policy_revision": 1
  },
  "interface": "lamp_tap0", "guest_ip": "198.18.240.2", "queue": 314,
  "guard_uid": 1000001,
  "host_denies": ["198.18.240.1/32", "198.18.241.0/30", "100.64.0.0/10"],
  "spool": "/var/lib/lamp/audit/sb-1-g1-network.ndjson",
  "spool_bytes": 268435456
}
```

The example inventory is **not** sufficient for an arbitrary host. Queue numbers
are namespace-local. Process and network receivers use separate fresh spools;
T23 joins them by attribution, not by inventing a cross-source ordering.
`lamp-guard policy CONFIG` prints the nft transaction without changing the host.
There is no non-Linux or unguarded fallback.

## As integrated at T6 (lane LX)

T6 joins this guard and collector to the T2 Firecracker engine. Where it
differs from the steps above, the difference is stated here rather than hidden:

- **Namespace and TAP.** Each sandbox generation gets its own jail network
  namespace with the TAP, the guard (NFQUEUE) and a userspace uplink (pasta,
  started as root and joined through a capability-less holder). The jailed VMM
  itself runs inside that namespace; it is not handed only a TAP FD. The task
  still cannot reach the namespace.
- **Mediated proxy.** `lamp-guard proxy CONFIG SPOOL` runs in the same
  namespace at `198.18.240.1:3128`, under the guard UID with its own seccomp
  allowlist. It accepts only absolute-form `http://IPv4[:port]` requests,
  refuses CONNECT, names, userinfo, TLS, chunked bodies and malformed framing,
  and applies the same allow/deny policy as the guard. Each request record is
  durable before the upstream dial. It is single-threaded.
- **Endpoint exceptions.** `/etc/lamp/network.json` (policy revision 2) carries
  exact host/port exceptions for the benchmark fixtures only: the same-host
  fixture namespace (diagnostic) and the owner's M5 on the LAN (binding E02
  fixture). There is still no dynamic policy update.
- **Collector.** Namespace PID 1 is now a reaper that forks the command, so
  background jobs survive the command's exit; the command's wait status goes to
  a dedicated `status:FD`. Records of the collector's own setup are pipelined
  and become durable before the command's exec'd code runs. Process-exit
  records (the exit has already happened) are written without waiting and are
  durable before the next blocking record and before `collector_complete`.
  Fork, exec and credential records still block the traced task until durable.
- **Cost.** Each short-lived guest process costs about 7–8 ms in audit and
  tracing (50 × `/bin/true`: 384 ms in the guest, 55 ms on the host). This is
  the audit-before-effect price and is visible in E02.

## Network enforcement and connection records

Every assigned-interface IPv4 packet in both directions goes through NFQUEUE;
there is **no bypass flag, kernel established-flow bypass or offloaded rule**.
The first SYN/UDP datagram is durably admitted before its verdict. Established
packets only update in-memory counters; they do not issue storage barriers.
Counters, aggregated denials and a durable heartbeat share a bounded batch at a
250ms maintenance interval (100ms idle polling). A one-second audit deadline is
checked before every verdict and after every storage barrier; missing it closes
the path even if a late write eventually succeeds. Maintenance also runs on an
idle link. Kernel queue overflow drops packets; netlink ENOBUFS, truncation,
malformed control, full spool or write failure terminates the guard. The installed queue rules then
drop both existing and new traffic even if the daemon was SIGKILLed. No route is
switched on failure. Locally addressed guest traffic is denied at INPUT; local
host-to-task IP traffic is blocked at OUTPUT. The private L2 segment still needs
ARP; it is not a host bridge.

Only public IPv4 TCP/UDP from the assigned source address is allowed. Return
packets must match a recorded reverse tuple. A new TCP flow needs SYN without
ACK; UDP starts on its first datagram. Denies include RFC1918, CGNAT/Tailscale,
loopback, link-local/metadata, special-use/documentation, benchmark,
multicast/reserved ranges and Azure's 168.63.129.16 VIP. Additional host denies
are intersections, not overrides. Policy data lives in `DENIED_V4`; reviewers
must maintain it against the [IANA IPv4 registry](https://www.iana.org/assignments/iana-ipv4-special-registry/)
and provider metadata inventories.

The demo **denies all IPv6**, including mapped IPv4, and rejects IPv4 options,
fragments and non-TCP/UDP protocols. There is no IPv6/ICMP bypass, but public IPv6,
PMTU/fragment support and complete unsupported-packet destination detail remain
T6/T12 qualification gaps. Use an explicit IPv4 guest DNS resolver; DNS is
transport-audited, not a hostname/site filter. Host DNS mediation is not built
here. No VPN, private connection exception or strict-sites mode is claimed.

Connection `start`, `checkpoint` and `end` records contain the tuple, policy
reason, unique flow identity, start/end times and cumulative accepted L3 bytes.
RST durably ends and removes a flow. The first FIN durably checkpoints a bounded
drain entry for half-close data/final ACKs. It and subsequent checkpoints remain
explicitly unterminated (`closed:false`, `ended_at_ms:null`). Only retirement by
RST, idle expiry or SYN replacement writes a terminal end and stops accepting
packets under that identity. A fresh SYN on a draining tuple starts a new identity.
An identical initial sequence number on an open tuple is a SYN retransmission;
a changed ISN ends the previous identity and admits a new one. Idle expiry runs
without incoming packets, records final counters and removes the entry.
Retransmissions count as bytes, not delivered application content. Abrupt helper
loss can lose the last unacknowledged counter batch; it never fabricates an exact
end. T23 must preserve incomplete endings and audit gaps.

Denials aggregate packet/byte counts and first/last observations by tuple/reason
rather than syncing each denied packet. There are at most 128 pending denial
identities. Overflow durably records the pending batch and a fault, then closes
this sandbox's path instead of forcing an unbounded sync storm or silently
losing identities. A batch is <=4 MiB and each framed record remains <=64 KiB.

## Process observation and integrity

Kernel ptrace events cover fork, vfork, clone and exec, not just command envelopes.
Creation is durably recorded at the parent's fork event before resuming it; a
parent cannot erase the start by immediately killing its newly forked child.
Each new thread group gets a `process_start`; threads get `thread_start`, not an
extra B4 process. Records use the actual `/proc/PID/exe` path/device/inode, kernel
start ticks, outer/nested PIDs, parent, effective/real/saved/filesystem UID/GID and
host timestamps. Credential-changing syscalls and namespace changes are also
observed. Deceptive argv[0] cannot change executable identity. Raw argv/env is
intentionally **all redacted**; no argv/body/packet payload is serialized to the
host spool. This is not T23's semantic request-redaction implementation.

Ptrace alone is bypassable: a mandatory inherited seccomp filter denies
`CLONE_UNTRACED`, returns ENOSYS for clone3 (libc uses traced clone), and denies
io_uring to prevent alternate worker/socket creation. Only native x86_64/aarch64
syscall ABIs are accepted; compat/x32 tasks are killed, not silently unobserved.
Only AF_UNIX/INET/INET6/NETLINK socket families are available to tasks; AF_VSOCK
and other alternate transports are denied. The engine must separately disable
host Unix forwarding and inherited sockets. SIGSTOP group stops are suppressed
by this demo tracer; arbitrary job-control compatibility is not qualified.

Each collector frame is <=32 KiB, length-prefixed (u32 BE), JSON
`{"sequence":N,"details":{...}}`. The host requires exactly consecutive source
sequences starting at 1, appends host attribution/sequence/time, syncs, then ACKs
with u64 BE N. Reordered ACKs or missing ACKs fail closed. A 250ms heartbeat and
500ms per socket operation bound detection during CPU-only execution. A final
`collector_complete` is acknowledged only after all tracees have exited. Task
threads never inherit the stream. Host spool files are create-new/no-follow, 0600,
physically preallocated before admission, directory-synced and never truncated
or overwritten. Restart requires a new segment; acknowledged history stays put.

After admission the network daemon drops root to `guard_uid`, retaining only
CAP_NET_ADMIN for NFQUEUE in its private namespace. The receiver drops every
capability. Both close ambient FDs, disable dumps, set no_new_privs and install a
native-ABI syscall allowlist: descriptor read/write/close/stat/fcntl/seek/sync,
netlink/stream send/receive, signal/clock, allocator/futex/getrandom and exit.
No open, socket creation, exec, fork, mount, ptrace or setns remains available.
The exact syscall inventory is `src/confinement.rs`. Only stdio plus the spool
and assigned netlink/stream FD survive. The tracer stays privileged **inside the
guest only**; its guest rootfs, kernel and service allowlist are T2's TCB.

## Reproduce

```sh
cargo test -p lamp-guard --locked
cargo clippy -p lamp-guard --all-targets --locked -- -D warnings
cargo build -p lamp-guard --locked
sudo env TMPDIR=/var/tmp python3 tests/guard/e2e.py "$PWD/target/debug/lamp-guard"
```

Linux only, root, iproute2, nftables, Python 3 and a kernel supporting user/PID/net
namespaces, seccomp, ptrace syscall info, NFQUEUE and fallocate. The fixture makes
three disposable namespaces and veths **inside them**, and changes forwarding
only there. Its public/private/metadata addresses are deterministic reflectors
inside the isolated edge namespace, not access to the real metadata service or
proof of a public-internet/VM boot. It removes its namespaces/processes and keeps
root-only spool artifacts in the printed directory. Use a disk-backed TMPDIR
(`/var/tmp` on LX), not tmpfs, for durable-spool/throughput evidence. It never
alters the host firewall, touches Docker or stops an owner service.

## Explicit remaining gates

- T2/T6: TAP/vsock microVM wiring; every exec/session through the collector;
  host route inventory and watchdog; capture/fork stream identity and tracer
  state coordination; kernel/LSM/cgroup/service confinement; actual engine E09/E13.
- T5/T6: E02 secured compute/disk/network ratios. Connection audit batching and
  syscall tracing are **not certified >=0.97**. The review-r1 bulk transfer is a
  rough guard regression measurement, not E02. Disabled controls are invalid.
  Optimization must retain
  audit-before-effect and fail-closed behavior, not silently bypass these checks.
- T6/T12: IPv6/public DNS/PMTU and additional network policy qualification.
- T23: encryption, hash chaining, signed/remote receipts, retention, export,
  semantic redaction, flow normalization, and ledger recovery. The current
  plaintext demo spool is host-permission protected, not that finished ledger.
- E13 breadth: actual nested Docker, forced PID reuse, all kernel/host families,
  rapid exhaustion and native macOS collectors have not been proved by this test.
