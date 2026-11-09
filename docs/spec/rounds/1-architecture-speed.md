# Lamp architecture, round 1: speed first

Status: round-1 proposal. Numbers called budgets are not measured Lamp results.
Binding inputs: `docs/spec/brief.md`, `GLOSSARY.md`, and ADRs 0001–0009.
Coverage input: `1-requirements.md`; it is not modified.
Research input: all six reports under `docs/research/`.

## 1. Decision and scope

Build the Linux engine first. Keep control work out of the execution path.
Use hardware virtualization for the default sandbox boundary.
Aim for create below 500 ms, memory-inclusive fork below 1 s, and 97% native computation, disk and network throughput on every host OS.
The brief makes these Linux release gates. Mac and Windows numbers remain hypotheses until measured.
Do not trade away isolation, credential custody, or any first-release feature to meet a timer.
All features in the brief belong in the first release. Milestones sequence work; they do not reduce scope.
There is no automatic overflow to cloud providers. A deliberately enrolled cloud machine is an ordinary host.
Optimize parallel work, safety, prepared tools and caller ownership together.
The speed lens chooses local execution authority, prepared state, native hypervisors and few data copies.
It does not justify a shared-kernel default, a central credential store, or an unaudited network shortcut.

Three distinctions govern the design:

| Distinction | Decision |
|---|---|
| Create versus preparation | Build and warm templates ahead of requests; publish both cached and uncached timings. |
| Restore versus fork | Include live capture, disk consistency, parent continuation and usable fork in the fork timer. |
| Secrecy versus authority | A hidden credential still authorizes actions. Enforce scopes and approvals outside the sandbox. |

There are real unresolved gates, especially arbitrary subscription-token removal, universal browser secrecy and 97% I/O throughput.
Sections 15 and 19 identify them. This proposal does not claim that a paper design has passed them.

## 2. Components and languages

Rust owns execution, policy and the data path.
A separate Go program embeds tsnet. Another Go plugin handles GitHub runner scale sets.
Use a small Swift/Objective-C adapter for Apple's full-VM APIs.
Use TypeScript for the dashboard and generated SDK, and Python for its generated SDK and benchmark drivers.
Do not embed a Go runtime in the VMM through cgo.
Do not rewrite WireGuard, TLS, OIDC, SSH or consensus algorithms to save a dependency.

```text
Caller: API / MCP / CLI / console / ordinary SSH
       | local socket, Tailscale, owner HTTPS, or encrypted relay path
       v
Any awake host: lampd [auth, stateless placement, sessions, events, local journal]
       |             |                   |                     |
       |             +-- lamp-access-go [tsnet, automatic HTTPS]
       |             +-- restricted plugins [CI, triggers, consoles, kits]
       |             +-- owner credential / MCP / browser brokers
       v
Small privileged supervisor [allocate handles; enforce resources and network]
       +-- engine process per sandbox -- KVM / HVF / WHP -- Linux guest
       |                                                  lamp-init / lamp-agent
       +-- full-VM engine -- Apple VZ or Hyper-V -- macOS / Windows guest
       +-- network enforcement -- selected exit / private network / permitted links
       +-- local content store, persistent volumes, artifacts and saved state

Host <=== mutually authenticated encrypted control/data ===> other hosts
Eilmer: Clerk-linked account service + AGPL relay; no sandbox-data decryption keys
```

`lampd` is unprivileged. The supervisor exposes typed operations, never a host shell.
Engine processes receive only their assigned disk, memory, transport and hypervisor handles.
`lamp-agent` offers exec, files, PTYs, process tracking and capture hooks over a host-bound channel.
No guest-facing channel is a general host-management API.
Trusted brokers run per owner, with a stronger separate browser sandbox where available.
The dashboard is served by hosts. Eilmer may serve its static assets; private API responses terminate on a host.

| Choice | Reason and reuse exception under ADR 0003 |
|---|---|
| Rust execution and network core | Fits Firecracker/libkrun, limits allocator pauses and keeps page ownership explicit. Reuse VMM code for the security boundary. |
| Go access program | tsnet is a Go userspace stack. Its tested encrypted networking is a security reason to reuse it, not to rewrite the engine in Go. [tsnet](https://tailscale.com/kb/1244/tsnet) |
| Go GitHub plugin | The official standalone scale-set library provides JIT registration and demand polling without Kubernetes. Reuse avoids security-sensitive registration mistakes. [source](https://github.com/actions/scaleset) |
| Rust TLS/SSH/OIDC libraries | Reuse reviewed protocol implementations for security. Pin versions, fuzz their exposed inputs and preserve notices. |
| Lamp control logic and storage index | Write placement, policy composition, lifecycle and the API for Lamp's execution model. No Kubernetes, Redis or Postgres prerequisite. |

Local RPC uses bounded Protobuf frames on Unix sockets or ACL-protected Windows named pipes.
Pass file handles once, then stream large data directly. Do not serialize RAM through JSON or a Go process.
Host-to-host streams use mutually authenticated TLS. Benchmark language boundaries as part of create.

## 3. Engines, guest software and devices

Engine selection is constrained by security, guest OS and requested devices before speed.
Linux's first prototype uses Firecracker and KVM.
Apple Silicon and Windows need native hypervisor paths rather than a mandatory nested Linux host.
Full macOS and Windows sandboxes remain a slower class. Native Linux guests never require WSL or a persistent outer Linux sandbox.

| Host / request | First engine choice | Why; limits |
|---|---|---|
| Linux x86_64 or arm64, minimal Linux | Firecracker 1.17 lineage over KVM | Small device model, jailer and lazy private RAM restore. Its published init bound is 125 ms, not complete create. [specification](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md) |
| Linux, higher I/O or assigned GPU | Cloud Hypervisor 54 lineage over KVM | Trial multiqueue/vhost and IOMMU-isolated VFIO. Select only after it wins comparable tests and passes isolation. Snapshot compatibility depends on devices. [state support](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/snapshot_restore.md) |
| Apple Silicon, Linux | A narrowly vendored `msb_krun` engine over Hypervisor.framework | Reuse its existing cross-platform capture work for likely speed. Lamp supplies its own policy, block storage and agent. Do not adopt microsandbox's control plane or template builder. [runtime dependency](https://github.com/superradcompany/microsandbox/blob/main/crates/runtime/Cargo.toml) |
| Windows x86_64, Linux | The same engine family over WHP | Independent native partitions and serialized CPU/device/RAM state. Continuing-parent fork must be demonstrated; a frozen source is insufficient. [local state support](https://github.com/superradcompany/microsandbox/blob/main/docs/sandboxes/snapshots.mdx) |
| Mac host, macOS | Lamp's small VZ adapter, Apple restore media and native guest agent | Maximum two allocated macOS sandboxes per Mac, including suspended ones. Validate save/restore for the exact configuration. [Apple lifecycle](https://developer.apple.com/videos/play/wwdc2023/10007/) |
| Windows host, Windows | Lamp Hyper-V management adapter; licensed generalized Windows disk and guest service | Standard memory checkpoints plus differencing VHDX; production checkpoints omit RAM. Full fork uses an independent imported saved-state instance, tested for parent continuation. [checkpoint semantics](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/checkpoints) |

These are initial selections, not permanent rankings. `lamp doctor --bench` records eligible engines and measured create, fork and throughput results.
Select the fastest engine that passes security and satisfies guest OS, architecture and requested devices.
Keep the chosen engine/version fixed for the sandbox's lifetime. Never silently convert a live capture between engines.
Released libkrun 1.19.8 does not provide the downstream fork contract. Its development branch is not a stable substitute. [release README](https://github.com/libkrun/libkrun/blob/v1.19.8/README.md)
Pin downstream source and transitive VMM revisions before implementation; moving `main` links here are research evidence, not build pins.

Linux uses one pinned LTS kernel build per architecture, starting with 6.12 and backported fixes.
Enable required virtio, ext4, overlayfs, cgroups, audit and VM-generation support. Disable unused drivers, module loading, kexec and host filesystem discovery.
`lamp-init` is a static Rust PID 1. It mounts the root, configures the control channel, reaps processes and launches the agent.
Direct kernel boot avoids firmware, DHCP, SSH readiness probes and a general init system on the microVM path.
An optional systemd kit and kernels for Docker/nested virtualization are prepared separately.

Required boot substrate is CPU, RAM, interrupts, timers, boot storage and a private management channel.
Display, audio, input, GPU, extra disks, entropy hardware and networking are explicit device selections.
Fresh entropy can arrive over the management channel without exposing a general RNG device.
Build-time device defaults are audited, including Cloud Hypervisor's RNG; patch out an unavoidable optional default or reject that engine for the request.

Display/audio/input use isolated virtio backends in the libkrun device engine; Linux may select that engine for these requests.
GPU choices are Linux VFIO with an exclusive IOMMU group, or validated mediated rendering on supported Mac/Linux hosts.
Windows/Linux GPU acceleration is an explicit engine-development gate, not a claim implied by WHP. Native Windows can use supported Hyper-V GPU partitioning.
Never emulate a GPU request with a display-only device. Unsupported hardware gives `DEVICE_UNAVAILABLE`.
Physical GPU state without a valid capture protocol blocks fork rather than dropping the device. Offer a migratable virtual device when appropriate.

| Host | Resource enforcement and engine confinement |
|---|---|
| Linux | Per-sandbox UID, namespaces, seccomp, read-only engine files, cgroup v2 CPU/memory/I/O limits, bounded disk allocation; no overcommit in performance mode. |
| macOS | Signed entitled engine, dedicated service identity and OS sandbox rules, no home directory access, fixed guest memory reservation, vCPU scheduling budget and hard disk limit. Host overhead is reserved separately. |
| Windows | Restricted service identity, ACL-only inherited handles, Job Object process/CPU/memory limits, WFP restrictions and fixed guest allocation. WHP compatibility with these restrictions is a test gate. |

Reserve resources before launch. Default to 2 vCPUs, 1 GiB RAM and 8 GiB writable disk; callers can change them.
Reject allocations beyond tested engine/platform maxima. Charge warm and suspended disk state to quota.
The macOS guest limit is required by the brief and Apple's licensed deployment conditions. [Tahoe SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf)

Without virtualization, Linux uses gVisor systrap over a Lamp-built root; Windows uses a restricted-token/AppContainer process; Mac uses an OS-sandboxed process.
All carry `isolation=process` or `isolation=gvisor`, visibly weaker than `isolation=vm`.
The non-Linux fallback exposes native commands and remote placement of Linux work; it does not pretend to boot a local Linux kernel.
Secrets/logins are refused without a signed owner override per sandbox. Public stranger CI always refuses fallback, regardless of override.
Fallback capture support is reported separately. It never counts toward real-VM benchmarks. [gVisor platforms](https://gvisor.dev/docs/architecture_guide/platforms/)

## 4. Templates, create, fork and lifecycle

Templates use Lamp's own manifest and content-addressed disk/state format.
No Docker image, Dockerfile or Docker runtime is part of template creation.
Disk copy-on-write and RAM copy-on-write are separate requirements.
Fork must include capture, source continuation, storage consistency and first command in the new sandbox.
Credentials must be excluded before capture; deleting a file alone is insufficient.

`lamp.template/v1` is a signed manifest plus content-addressed chunks, not an OCI manifest.
It records guest OS/architecture, kernel/init hashes, package locks, root chunks, optional prepared RAM, CPU baseline, engine state ABI, device graph and restore hooks.
Separate portable filesystem content from an engine-specific prepared-state variant.
Recipe versions pin template, harness, kits, devices, CPU/RAM/disk, network and connections by digest.
Mutable names point atomically at immutable versions. Existing sandboxes keep the old version.

```yaml
format: lamp.template/v1
guest: {os: linux, arch: arm64}
root: {source: debian-release-tar, sha256: "<verified digest>"}
steps:
  - packages: {lock: packages.lock}
  - run: ["python3", "-m", "pip", "install", "--require-hashes", "-r", "requirements.txt"]
prepare: {command: ["/usr/lib/lamp/prepare"], network: off}
```

The builder verifies a distribution tarball/package repository, expands it with safe path handling, and formats guest-owned ext4 disks in a disposable build sandbox.
Bootstrap uses a signed minimal Lamp seed. It does not shell out to Docker, Buildah, an OCI importer or a Dockerfile parser.
Builders can run public package managers and arbitrary declared build steps inside that boundary.
Build credentials use the same host brokers; they do not become template layers.
Windows/Linux disk formatting on non-Linux hosts runs inside the build sandbox, not a host `mkfs.ext4` dependency.
For native guests, accept owner-supplied licensed installation media and unattended installation steps; never redistribute personal licensed installations.

Saving a running sandbox quiesces writes, detaches credentials/connections, and seals its disk as a new owner-only template.
Installed tools remain. Personal settings and task files remain private unless the owner chooses to remove them or explicitly shares the result.
The default saved template is disk state; the builder boots it once, sanitizes it, and generates a prepared RAM variant.
A caller can request a compatible prepared execution variant, subject to the stricter capture gate below.
No template, cache or warm sandbox ever contains a model key or browser login by default.

| Host storage | Create and fork disk primitive | Ordinary-filesystem path |
|---|---|---|
| Linux XFS/btrfs | Reflink an immutable raw disk; freeze/flush before sealing a running disk. | On ext4, Lamp's block engine uses immutable 64 KiB extents and a bounded-depth writable extent map. No whole-disk copy. Optional preconfigured dm-thin is another measured path. [thin provisioning](https://docs.kernel.org/admin-guide/device-mapper/thin-provisioning.html) |
| macOS APFS | `clonefile` sealed raw disks, with independent writable descendants. | Same extent-map engine if native cloning is unavailable. File cloning is not RAM cloning. [Apple APIs](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/ToolsandAPIs/ToolsandAPIs.html) |
| Windows NTFS | Lamp extent-map block backend by default; no assumption of NTFS reflinks. Full Windows class uses differencing VHDX. | Use ReFS block clone only after probing support/alignment. ReFS cloning is a metadata operation with documented limits. [Microsoft](https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning) |

The custom extent map is performance-sensitive new code, not a new guest filesystem.
Attach it at the engine's block-backend seam; stock raw-file I/O remains the reflink fast path. Capture drains and versions every external block backend too.
Reads resolve through one flattened index; background compaction prevents O(fork-depth) lookups.
Checksummed journal updates preserve ordered writes, barriers, fsync and discard. Never acknowledge durability before the selected durability policy is met.
Encrypted host storage protects state at rest. Per-owner keys and signed manifests protect transferred state.

Create first reserves a ready slot matching owner, template/recipe digest, resources, device graph, kernel and policy epoch.
A warm slot is a never-used sandbox prepared without credentials or external connections. Assign it once and destroy it after use.
Install policy, identity, settings and broker handles before releasing its execution gate.
A warm miss maps prepared RAM privately, opens a writable disk descendant, restores CPU/devices, runs uniqueness hooks and executes the first command.
Build, remote template fetch, first-ever sign-in and VPN enrollment are visible preparation operations, not hidden successful creates.
If requested preparation is unavailable, create remains pending with its full elapsed time; publish that outcome beside cached timings.
Keep two slots for frequently used eligible recipes, subject to owner memory budgets; refill off the request path.

Fork is a transaction with these barriers:

1. Freeze new commands, revoke old broker handles and quiesce external writes. Run the credential gate; failure aborts before any capture file is written.
2. Freeze guest filesystems, drain device queues, stop all vCPUs and record matching CPU/device/disk/RAM generations.
3. Seal an immutable disk generation. Capture changed RAM and CPU/device state; account for guest and device-backend writes.
4. Give source and fork independent writable descendants and fresh external identities. Resume the source promptly; create a new VMM for the fork.
5. Run uniqueness hooks with external I/O closed, reauthorize connections, then run a probe command in the fork. Commit its visible session IDs.

No host `fork()` inherits a live KVM object. KVM explicitly does not support that descriptor trick. [KVM API](https://docs.kernel.org/virt/kvm/api.html)
Initial Linux implementation uses Firecracker's full RAM capture plus `MAP_PRIVATE` restore; RAM is not streamed across the fleet coordinator. [snapshot mechanism](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md)
For the faster steady path, maintain an immutable RAM generation and background dirty-page epochs; final pause captures only the remainder.
The dirty set includes vCPU writes and emulated-device DMA. Never enable speculative pre-copy while subscription material might be present.
Diff capture is an engine capability with independent correctness tests; experimental upstream support is not automatically release-ready.
Fork state can initially remain in host page cache without a power-loss survival promise. Durable fork adds flush time and reports it.
This is not a suspended sandbox: fork backing pages consume charged host RAM until evicted or flushed.

Mac maps per-instance private saved RAM over HVF and records registers, interrupt controller, timers and queues.
Windows first trials WHP over private file views; if GPA pinning defeats host CoW, trap first writes and remap private pages in the VMM.
Microsoft exposes file-backed GPA mapping and dirty tracking, but these APIs alone do not prove cheap CoW. [mapping](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/funcs/whvmapgparange), [dirty bitmap](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/funcs/whvquerygparangedirtybitmap)
`FILE_MAP_COPY` charges commit for the full view, so admission reserves that potential commitment. [memory semantics](https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-mapviewoffile)
Parent continuation is mandatory on all three OSes. SmolVM's Windows freeze-source restriction is exactly what Lamp must improve. [upstream limitation](https://github.com/smol-machines/smolvm/blob/main/docs/limitations.md)

| Restored state | Before any task can send traffic |
|---|---|
| Entropy | New VM generation and 256 bits from host CSPRNG; reseed kernel, then harness/runtime restore hooks. User PRNG state needs an explicit hook; no generic reseed promise. [Firecracker guidance](https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md) |
| Time | Set wall time from the host; preserve monotonic progression and rebase suspend deltas. Recompute deadlines, token expiry and leases from host authority. Test long sleeps and clock rollback. |
| Identity | New sandbox ID, machine ID, hostname, MAC/IP, vsock identity, SSH keys and private-network registration for a fork. Resume keeps logical sandbox ID but rotates connection handles and VM generation. |
| External state | Drop inherited TCP/TLS sessions, locks and lease handles; reconnect through approved hooks. Copied memory never duplicates remote-service transactions safely. |
| Secrets | Captured state has none of the stripped subscription material. Reattach only the same owner's currently authorized sign-in, after policy is active. |

A default idle grace is 60 seconds. Idle requires no task/session activity, no task processes and no task connections.
Only fixed infrastructure and an adapter-certified parked harness wait are exempt; a shell, detached daemon, open web socket or background build prevents idle suspension.
Use guest process accounting plus host network accounting. If either is uncertain, treat the sandbox as active.
Suspend uses the same capture gate, durably writes RAM/disk, destroys the VMM, releases all guest allocation and drops its private cached RAM pages.
No guest RAM is pinned as a hidden warm slot. Shared template cache and host service memory are reported separately.
The next message, exec, SSH attach or allowed inbound request wakes it once; concurrent wakes share one operation.
Target local prepared resume below 500 ms; cold restore and native full-VM resume are measured separately.
Default deletion is seven days after suspension; callers can change idle and wall-clock expiry. CI deletes on completion.
Expiry, denied capture and failed resume have explicit states and events. An unsafe suspension never masquerades as success.

## 5. Persistent storage

Caches, volumes, artifacts and saved templates are owner-only by default.
Their IDs are unguessable, but authorization never relies on that fact.
Store data on enrolled hosts; advertise locality and free space to placement.
The relay is not a storage service. Cross-host transfers are encrypted directly between hosts.

| Resource | Semantics and fast path |
|---|---|
| Cache | Content-addressed package/build objects; keys include owner, trust class, architecture and toolchain digest. Local hot objects are reused without copying. Mutable directory caches have explicit writer leases. |
| Volume | Named file storage with explicit read-only/read-write attachments. Multiple sandboxes can attach concurrently. One designated storage host serializes metadata and file operations; writes are durable after fsync. |
| Artifact | Immutable streamed objects with size/hash/retention, resumable upload and a final atomic publish. Collect successful, failed and canceled CI output before deleting execution. |
| Template | Immutable signed manifest and chunks; owner-only until an explicit share grants read/create rights. Content deduplication never creates cross-owner read rights. |

Volumes use a confined file-service protocol over host transports, surfaced through virtiofs for Linux and suitable guest file adapters for native guests.
The file broker receives an already-open volume root, rejects traversal/symlink escape, and implements file locks and atomic rename.
It never exports the owner's home directory. Native Windows may use a per-sandbox SMB endpoint and scoped local broker capability.
Several writers share filesystem semantics, not the same raw ext4 disk. Databases needing block ownership use an exclusive block attachment.
A volume is external state during fork: attach the same volume only with explicit semantics, or request a consistent volume version and independent descendant.
Do not silently give a fork a second writer to the parent's external database.
Cross-host mounts work but cost network RTT; placement prefers storage locality. Durable replication is configurable and reports its recovery point.
Do not claim automatic volume recovery after losing the only storage host.

Default artifact retention is 30 days, suspended-state retention seven days, and cache eviction is LRU within quota.
Templates and volumes are retained until explicit deletion. Reference counting protects live descendants during compaction/garbage collection.
Fork charges reserved private growth and shared physical bytes separately; one owner cannot evade disk/RAM budgets through fan-out.
Stranger public-PR caches are isolated by repository and untrusted trust class. They cannot write or promote into trusted caches.
Only verified public package blobs may cross trust classes. Signing keys and credential directories are never cache paths.

## 6. Host-enforced network

Every packet path is controlled outside the sandbox.
Network intent becomes a compiled policy generation before vCPUs are released.
The management channel cannot be used as a generic dial or filesystem service.
In particular, disable libkrun's automatic TSI and host Unix-socket impersonation; no-NIC must really mean no task network. [upstream security model](https://github.com/libkrun/libkrun/blob/v1.19.8/README.md)

| Host | Mandatory packet path |
|---|---|
| Linux | Per-sandbox TAP/network namespace; nftables default drop, anti-spoof and connection limits; fast tc/eBPF classification where measured. Traffic then enters a host policy gateway or an explicitly approved direct path. |
| macOS | VMM virtio-net sends Ethernet frames to Lamp's isolated host switch through an inherited local socket. No shared/NAT attachment that bypasses the switch; an OS network restriction blocks VMM internet dialing. |
| Windows | WHP virtio-net uses the same host switch; WFP denies unrelated VMM sockets. Native Hyper-V guests use isolated virtual-switch ports with enforced ACLs and no alternative default route. |

The switch binds traffic to its engine handle, not guest-supplied IP/MAC fields.
It enforces IPv4, IPv6, ARP/NDP, fragments, DNS, UDP and source-address checks.
Unknown extension headers and unsupported protocols fail closed under restricted policy.
Drop LAN, loopback, link-local and metadata destinations unless an explicit connection grants a specific service.
Changes swap policy atomically. Existing flows are rechecked and closed if newly denied.
If an enforcer, VPN or selected proxy fails, stop that path; never choose direct egress as an automatic fallback.

| Requested behavior | Implementation |
|---|---|
| Independent exit IP | A host network plugin supplies a VPN route, SOCKS/HTTP proxy or direct exit. Its keys and control sockets stay on the host. A separately leased provider exit is needed for a distinct public IP. |
| Private membership | A separate host-side tsnet instance/registration per sandbox, with its own state directory and policy. Only approved destinations are reachable. First enrollment is preparation work; warm identities are never recycled across owners. |
| Inbound URL | A host HTTP router binds a URL to one sandbox, port, owner/share and expiry. Default private; public publication requires an authorized choice. It cannot open the guest agent or SSH-management port. |
| Site allow/deny | Normalize IDNA/trailing dots; deny wins. Exact names by default; explicit `*.name` matches subdomains only. Resolve through host DNS and pin accepted answers for each connection; reevaluate redirects. |
| Sandbox links | A directed source/destination/port/protocol grant enforced at both ends, including across hosts. Same host, owner or fleet membership is not implicit connectivity. |

Normal developer recipes allow public web/package traffic but deny private networks by default.
Public stranger CI starts with a small repository/package endpoint allowlist and no owner connections.
Strict domain policy requires HTTP/TLS mediation: an IP-only allowlist cannot distinguish two sites sharing an address.
Enforce SNI, certificate name and HTTP authority agreement. Disable direct QUIC, ECH and alternate DNS paths when they prevent required inspection.
Opaque traffic needs an explicit CIDR/protocol connection; do not advertise semantic site filtering or per-request audit inside an opaque stream.
VPN egress happens after policy and audit, so the VPN cannot hide guest requests from the host enforcer.
An inside-Docker kit shares only its sandbox's permitted path and never the physical host's Docker socket.

## 7. Credentials, connections, browser and approvals

API keys, CLI tokens, MCP credentials, passwords and browser cookies stay outside the agent sandbox by default.
Subscription logins follow ADR 0004 and remain a documented exception to raw-token secrecy.

### Host credential proxy

A broker capability is bound to owner, sandbox, connection, account, permitted operation and expiry.
Its ID in the guest is not accepted from any other engine channel. Revocation closes active authorized streams.
The host resolves secrets from a per-owner encrypted store or password-manager plugin; never from an Eilmer service.
Use short-lived in-memory unlocks, no crash dumps, bounded retention and explicit host filesystem ACLs.
No API-key value goes into guest files, environment, RAM, saved state or generated settings.

The guest has syntactically valid sentinels. A host HTTP proxy terminates guest TLS, checks policy, then injects the real authorization into a new validated upstream TLS connection.
Support HTTP/1.1, HTTP/2 and WebSockets; stream model output without buffering an entire response.
Every credential binding limits destination, TLS name, port, method, path and relevant resource identifiers.
Strip guest-supplied authorization to protected destinations. Never follow a redirect with credentials attached.
Disable generic credential-bearing requests to caller-chosen URLs and endpoints that mint/export credentials.
Response parsers remove secret-bearing fields, cookies, auth headers and reflected known values, including supported encodings, before returning bytes.
Trusted upstream credential recipients remain part of the trust model; an arbitrary malicious recipient can encode a key beyond any generic redactor's knowledge.

Each host has an owner-specific interception CA. Its private key stays in that owner's broker; only the public CA enters templates or sandboxes.
Install trust in OS, Node, Python, Java, Git and nested Docker build stores through kits; do not disable certificate verification.
Upstream TLS uses the system roots, not the interception CA. Restrict issuance and the network path to configured destinations.
Certificate pinning, mTLS and signed requests need typed adapters; incompatibility fails visibly rather than injecting a real secret.
Sentinel replacement and trust-store requirements have a working precedent, but Lamp must test every selected harness. [Docker credential design](https://docs.docker.com/ai/sandboxes/configuration/credentials/)

### Subscription sign-in and capture

Each harness plugin ships a versioned sign-in catalogue: vendor evidence URL/date, permitted context, storage path, refresh owner, egress endpoints and capture method.
Default to each person's official unmodified harness and approved sign-in. Never impersonate another application's OAuth identity.
Claude uses its native sign-in or approved `setup-token` context; preserve its built-in authentication choices and terms. [vendor conditions](https://code.claude.com/docs/en/legal-and-compliance)
Codex uses official sign-in/device flow, or the documented OSS registration/consent flow; self-hosted VM transfer keeps a distinct host ID and VM-owned refresh. [official VM flow](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms)
Copilot uses its native browser/device login or documented automation credential, without claiming ordinary Actions tokens grant Copilot entitlement. [official methods](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli)
OpenCode, Cursor CLI, Amp, Goose, Gemini CLI, Pi, Devin and Droid each get an execution adapter and current catalogue.
Where a vendor offers no approved subscription path, use its documented API/service route or report the unsupported method; do not fabricate consent.

Real subscription material is transferred directly between that person's hosts and their authorized sandbox over an encrypted channel.
Placement must select a host that person has explicitly trusted for credential custody; team membership alone does not grant it.
It lives in a nonpersistent credential mount and the harness's memory, never template layers, host-wide settings or fleet metadata.
A subscription-bearing sandbox gets vendor-only egress. General web, Git and MCP work uses approved host brokers or a separate task sandbox.
Once arbitrary task code can read a bearer, process-based endpoint rules cannot prevent that code from reusing it; the whole sandbox's egress must reflect that risk.
Refresh ownership follows vendor rules. Never copy one rotating refresh chain into concurrent forks and hope races work.
The relay never stores, decrypts or forwards model requests as an inference proxy.

Before capture, stop subscription requests, revoke handles, detach the credential mount and run a certified harness clean-state hook.
It must remove files, environment, heap copies, TLS buffers, page caches, swap and crash data before capture starts.
Deleting a token file or searching RAM for its literal bytes is not a proof: guest-root code can transform or hide copies.
Therefore mark a sandbox that has exposed a real bearer to arbitrary code as `capture_unsafe`; refuse capture with `SECRET_STATE_UNPROVABLE`.
Certified cooperative adapters may resume from a clean checkpoint; they must prove both state continuity and token exclusion.
For unrestricted mid-task forks, prefer API-key proxy mode or an approved outside harness that never places its bearer in the target sandbox.
Universal hostile-guest subscription scrubbing remains an unresolved release gate, not an exception invented by this design.

### MCP, CLI tools and local services

The host MCP gateway owns OAuth discovery, consent, tokens, refresh and per-tool policy.
The sandbox receives a single authenticated local endpoint with permitted tools and separate upstream audiences.
Do not forward a Lamp bearer as an upstream OAuth token; use audience-bound authorization and PKCE. [MCP security rules](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations)
Host stdio MCP programs run in restricted per-connection processes; file tools see only approved roots.
If a server must run inside, mint a limited, short-lived delegated connection token. It must not expose the upstream API key.
If upstream only offers an unscoped raw key, that configuration does not meet the limited-access exception.

`gh` and `stripe` use destination-scoped sentinel replacement; block auth-export commands and token-creating operations.
For AWS, the guest uses dummy credentials and the host re-signs validated final requests with SigV4, scoped by service/region/resource/action.
Support streaming signatures only with a tested streaming signer; never return STS credentials or private signing material to guest code.
Git HTTPS credentials use the same proxy. SSH and git signing use a host signing broker with per-operation approval, not a copied private key.
Settings carry git identity and signing configuration; they do not carry signing secrets.

Owner-machine services support `off`, `read-only`, `ask-first` and `full`.
Read-only requires a genuinely read-only database identity or a semantic operation adapter; HTTP GET is not sufficient evidence.
Unknown protocols cannot promise read-only; refuse that mode until an enforceable adapter exists.
Full access still limits service, account and owner. There is no general host-shell connection.

### Browser, password managers and approvals

The browser kit starts a dedicated per-owner browser on the host or in a locked-down browser sandbox, outside the agent sandbox.
Do not attach raw CDP or Playwright object handles to the agent. Those APIs expose cookies and script execution. [browser context API](https://playwright.dev/docs/api/class-browsercontext)
Expose typed navigation, click, safe text entry, screenshot, sanitized accessibility and download operations.
Deny arbitrary JS, cookie/storage export, request headers, browser profile access, debug ports and unrestricted extension installation.
Auth cookies remain in the broker-controlled jar. Protect authenticated origins and reject routes that expose session material through page content.
Screenshots/DOM are suppressed during credential entry and human takeover; recordings omit that interval.
Site adapters must account for JavaScript-readable tokens and malicious same-origin content; remote control alone is not proof of zero leaks.

Password plugins for 1Password, Bitwarden, KeePassXC and Proton Pass return only handles to the action broker.
Use vendor CLI/API retrieval inside the restricted host credential process, then perform the approved destination fill there.
Prefer request-level form submission so the password never becomes readable agent-controlled DOM.
If a site requires secret-dependent page JavaScript, use trusted human takeover or a proven site adapter; do not provide a general DOM-value escape.
A 1Password announcement is not evidence of a public Lamp-compatible fill SDK. [vendor announcement](https://1password.com/blog/1password-for-ai-agents)

An approval binds owner, sandbox/session, vault/entry, origin, operation hash, expiry and nonce.
Approve on the console or phone; atomically consume once immediately before use. Changing the destination invalidates it.
Per-vault per-use approval is configurable. Approval grants an operation, not permission to read the vault.
MFA/CAPTCHA transfers an exclusive input lease to the person. The agent sees `blocked`; it cannot race input or inspect sensitive output.
Stock SSH prints a short-lived trusted-console URL for graphical takeover, then continues when control returns.

Copied browser logins inside the agent sandbox and raw password injection are supported only by explicit owner opt-in, per mode and per secret.
They carry `secret_exposure=owner_authorized` and are excluded from protected-mode claims; see the binding-target conflict in section 15.
Settings, skills, plugins, slash commands, instruction files and MCP lists are copied one way from a caller-selected source.
Changes produce a confined diff proposal. Only the person's approval writes them back; a session's write scope does not imply that approval.

## 8. Sessions, consoles and teleport

One fleet address exposes every authorized session as a separate session.
Sandbox and session are different resources: one sandbox may contain several sessions.
Both inside and outside harnesses use the same command/files/browser surface and event model.
Sub-agent parentage is visible across hosts, even when several sessions share one sandbox.

`lampd` maintains a per-host PTY/session supervisor, append-only output segments and a replay cursor.
Detaching a console leaves the harness running. Reattaching replays from the last acknowledged byte/event.
Watch permission cannot request stdin, terminal control, signals, file writes or shell access.
Typing requires an input lease; multiple viewers are unlimited within quotas, and a person can explicitly transfer the writer lease.
Sharing grants `watch` or `watch+type` to a named caller with expiry and immediate revocation.
Team membership alone does not reveal session names, files, events or output. Host administration is a stronger disclosed trust role.

Harness adapters report working, blocked and idle with a reason and confidence.
Working means task/tool execution; blocked means approval, human challenge, missing input or resource wait; idle means awaiting a new task without running task activity.
Unknown status is displayed as such, never guessed idle to save RAM. Session idle and sandbox eligibility for suspension are distinct.

The console plugin contract is discover/list/open/watch/input/cancel/status/share/takeover, with stable fleet/session IDs and reconnect cursors.
T3 Code and Herdr register one machine endpoint. The plugin aggregates all hosts; it never exposes one console machine per sandbox.
Use public third-party console contracts and fixtures; no special integration with the owner's projects.

```console
ssh person@api.f-7.eilmer.si                  # interactive session list
ssh person@api.f-7.eilmer.si sessions         # list authorized sessions
ssh -t person@api.f-7.eilmer.si attach se_42  # live harness PTY
ssh -t person@api.f-7.eilmer.si shell sb_41   # authorized guest shell
```

The host terminates standard SSH with a fleet host key held only on enrolled hosts.
Authorized keys/short-lived SSH certificates map to the same actions and resource scopes as API credentials.
Ordinary SSH can use a reachable tailnet address or owner endpoint. For a NAT-hidden relay-only fleet, expose a public opaque TCP rendezvous port keyed by a per-fleet destination address/port.
The relay forwards raw SSH handshakes and encrypted packets; it cannot require a custom SSH preamble from a stock terminal.
HTTPS-only networks can use the CLI transport helper, but that is an additional option, not the promised plain-SSH path.
On a shared relay IPv4 address, return a standard `ssh -p PORT` command or ordinary SSH configuration entry; SSH has no SNI for hostname-based multiplexing. [transport protocol](https://www.rfc-editor.org/rfc/rfc4253)

A sub-agent request chooses `sandbox=shared` or `sandbox=own`.
Own mode versions parent files, including approved uncommitted/untracked files, through a temporary git commit; private/ignored credential paths are excluded.
Transfer a git bundle to a fresh sandbox and return changes as commits/cherry-picks. Conflicts require an explicit resolution; never overwrite the parent automatically.
Non-git task files can be placed in a temporary git repository so the return contract remains git.
Shared mode shares filesystem/network/resources intentionally; it is not an isolation boundary between sessions.

Teleport moves a live session identity, files, transcript and harness continuation to a laptop or sandbox.
At a harness checkpoint, fence new input, capture portable nonsecret continuation plus a git file version, prepare the destination, then switch the session's execution lease.
The source stops before destination tools can write. On interruption, one fenced source or destination remains authoritative; never run both to guess which wins.
Reauthenticate locally under ADR 0004. No provider secret is part of the transfer envelope.
Same-engine compatible hosts can optionally transfer full RAM state; macOS-to-Linux or native process moves use harness checkpoint/resume.
Each named harness needs a tested continuation adapter. Lack of one is a teleport release gap; starting a blank harness is not teleport.

## 9. API, MCP, CLI and SDKs

One operation model drives every interface and SDK.
Use OpenAPI 3.1 with shared JSON Schemas for resources, errors, events and streaming control frames.
Generate REST handlers/types, MCP schemas, CLI argument metadata and SDKs from it.
Ship TypeScript and Python first, then Go and Rust before the complete first release.
No second handwritten API with weaker authorization hides behind MCP or SSH.

Resources are fleet, host, caller/key, template/version, recipe/version, sandbox, session, connection, network policy, kit, cache, volume/attachment, artifact, job, trigger/schedule, approval, plugin and operation.
Every object includes owner, revision, timestamps, effective policy and lifecycle state where relevant.
Sandbox lifecycle is pending → preparing → running → suspending → suspended → resuming → running → deleting → deleted, with explicit failed/blocked states.

| Operation | REST under `/v1` | MCP / CLI example |
|---|---|---|
| Create and inspect | `POST /sandboxes`, `GET /sandboxes/{id}` | `sandbox_create`; `lamp sandbox create --recipe python-dev --json` |
| Execute and files | `POST /sandboxes/{id}/execs`, `/files:read`, `/files:write`, `/files:transfer` | `sandbox_exec`; `lamp exec sb_41 --json -- python -V` |
| Fork/state | `POST /sandboxes/{id}:fork`, `:suspend`, `:resume`; `DELETE /sandboxes/{id}` | `sandbox_fork`; `lamp sandbox fork sb_41 --json` |
| Save/build | `POST /templates:build`, `/sandboxes/{id}:save-template`; `/recipes` | `template_save`; `lamp template save sb_41 --name tools --json` |
| Sessions | `/sessions`, `/sessions/{id}/messages`, `/shares`, `/input-lease` | `session_send`; `lamp session watch se_42 --json` |
| Policy and connections | `/sandboxes/{id}/network`, `/connections`, `/urls`, `/links` | `network_configure`; `lamp connection set db --access ask-first --json` |
| Durable storage | `/caches`, `/volumes`, `/attachments`, `/artifacts` | `volume_attach`; `lamp artifact download ar_1 --json` |
| Admin and integrations | `/hosts`, `/keys`, `/plugins`, `/jobs`, `/triggers`, `/schedules`, `/approvals`, `/audit` | Matching generated tools and command groups, with identical scopes. |

```http
POST /v1/sandboxes
Authorization: Bearer <scoped Lamp key>
Idempotency-Key: op_h3_7c...
Content-Type: application/json

{"recipe":"python-dev@sha256:...","placement":{"host":"auto"},
 "resources":{"vcpus":2,"memoryMiB":1024,"diskMiB":8192},
 "firstCommand":["python3","-c","print('running')"]}
```

The response includes sandbox/operation IDs, assigned host, effective resources, policy and `firstCommand.started` proof.
Default create can wait for that proof; `wait=accepted` returns 202 plus an operation URL and never counts as ready.
Example SDK: `await lamp.sandboxes.create({recipe: "python-dev", firstCommand: ["python3", "-V"]}, {idempotencyKey})`.
Errors carry stable code, message, retryability, operation ID and safe details; distinguish unsupported, forbidden, capacity and in-progress states.
Every CLI command, including errors/help, supports JSON; streaming uses NDJSON with no prose on stdout.
Publish `/openapi.json`, `/llms.txt` and complete Markdown operation documentation without credentials in examples.

Idempotency scope is fleet + owner + operation type + key, with request-body hash and ten-day result retention.
Persist an accepted intent before side effects. Same key/body returns the same operation; another body returns 409.
After result eviction, retain compact tombstones for opaque keys until fleet deletion. Generated keys can expire only if all later uses are rejected by their embedded immutable expiry.
A tombstoned key returns `RESULT_EXPIRED`; it is never silently treated as new. Include tombstone space in metadata quota.
Use operation IDs and sequences for command starts, messages, approval consumption, artifacts, CI deliveries and teleport.
Unknown execution after a crash is `outcome_unknown`; never retry an arbitrary command and claim exactly-once external effects.

SSE `/events?cursor=...` provides at-least-once events, per-resource ordered revisions and durable reconnect cursors.
Retain metadata events seven days; return `CURSOR_EXPIRED` with a fresh authorized listing path rather than hiding a gap.
PTY/file streams use WebSocket or HTTP/2 binary frames with separate sequence numbers and flow control.
Slow readers cannot block a VMM or discard the authoritative audit log. Event filtering happens before bytes enter the caller's stream.

## 10. Plugins

Process separation must be paired with authority confinement.
Each plugin is a separately installed executable plus a signed manifest, semantic version, API range, declared methods and requested permissions.
The default protocol is JSON-RPC 2.0 over stdio, with length and concurrency limits, cancellation and deadlines.
Handshake negotiates `lamp.plugin/1`, methods, supported guest/host versions and a manifest digest.
Bulk data uses scoped inherited streams. No plugin receives a host filesystem path or global credential simply for discovering options.
Versioned Protobuf handle transport is available to trusted engine plugins; it does not change the authority rules.

Linux uses namespaces/seccomp/cgroups; Mac uses restricted identities and sandbox profiles; Windows uses restricted identities, ACLs and Job Objects.
Plugins have no ambient network or sandbox-store access. A broker performs each permitted operation using a narrow grant.
The protocol host rejects unknown fields where security-relevant, limits buffers, restarts failed workers and opens a circuit breaker on repeated failure.
A crash can fail its own operation, not `lampd` or unrelated sessions.

| Type | Granted interface |
|---|---|
| Engine | Assigned sandbox handles and explicit lifecycle operations; trusted boundary code, isolated per sandbox. |
| Network | Declared route/packet-transform endpoints; the core retains admission, anti-spoof and final policy enforcement. |
| Credential/password | Owner-selected vault handles and approved use requests; no arbitrary agent-facing secret-return API. |
| Harness | Guest launch/continuation/status over a scoped agent channel. |
| Kit | Declarative software/state preparation and device needs, built inside the sandbox. |
| CI | Scoped job demand, create/cancel/result/report; upstream CI credentials stay in its owner broker. |
| Trigger | Verified external event → scoped operation request, never direct privileged execution. |
| Console | Authorized session discovery/watch/input/share/takeover, no host shell. |
| Access | An authenticated transport stream; Lamp still checks every action. Cloudflare Tunnel is one such plugin. |

An engine necessarily accesses its assigned sandbox RAM; a credential plugin necessarily handles its selected credential.
These are explicit trusted roles, not permission for arbitrary plugins to inspect sandboxes.
Installing a new trusted engine requires owner approval of its trust class and provenance. An untrusted console/trigger plugin can never request engine handles.
Publish conformance fixtures, schema compatibility tests and a plugin SDK. Third parties can write in any language.
Minor releases add negotiated methods. Breaking protocols use a new major version; run two majors during rolling upgrades.
Plugin updates are pinned and owner-controlled. No downloaded plugin acquires more authority through a silent manifest change.

## 11. Fleet coordination and updates

Hosts own execution state. Fleet coordination owns placement authority and key policy.
Sleeping laptops must not cause duplicate execution.
Use stateless placement coordinators on every host, rather than a mandatory elected scheduler on the create path.
They exchange signed capacity adverts and session indexes. The execution host atomically admits or rejects its own resources.
Choose OS/architecture/devices/isolation first, then requested pin, warm-state locality, free room and measured speed.
A pin either succeeds there or returns `PIN_UNAVAILABLE`; never move it silently. Default capacity failure is 429 with retry information; queueing is explicit.

Admission uses host-owned quota slices whose sum never exceeds fleet limits. Offline hosts retain their reserved slice until fenced or its lease expires.
Rebalancing quota is a signed policy transaction; it is not required for each create.
Each idempotency key has an immutable home host and routing epoch, encoded in generated keys or deterministically resolved for opaque keys.
That host durably journals intent and the selected execution host before launch. The execution host also deduplicates the operation ID.
Replicate receipts to other hosts asynchronously, but never reassign uncertain keyed work after losing its home.
Retries can report `OUTCOME_PENDING` until the old host/receipt is available; creating a duplicate is forbidden.
New operations can use an awake home. SDKs learn available homes from the fleet event stream without a per-create discovery call.
This trades transparent replay during total ledger loss for safe automatic scheduling on the remaining hosts.

One public fleet address routes to an awake coordinator through host registrations, Tailscale Services, or an owner-configured L4/DNS failover endpoint.
No process is a permanent coordination leader, so two hosts can lose either coordinator without needing a third quorum voter.
Target rerouting within five seconds; existing execution on surviving hosts continues. Execution on a sleeping host remains unavailable, not silently recreated.
Membership, grants and revocations are owner-authorized, signed, revisioned records; concurrent conflicting grants merge restrictively and require explicit reconciliation.
Hosts reject policy rollback. New credential use requires a current short lease; a disconnected host cannot invent fresh authority from stale adverts.
Offline local accounts can issue local-only grants under their configured offline policy; they cannot expand an online team's cached authority.
Remove a host by revoking its host certificate, routes and budget slice after fencing. Re-enrollment gives it a new identity.

Automatic updates are off by default. Opt-in rolls one host at a time, verifies signatures, drains admission, preserves running compatible engine processes and health-checks before advancing.
Keep old engine binaries until their saved state is gone or explicitly converted through a verified boot-and-recapture operation.
An incompatible update waits for sessions or an authorized maintenance move; it never discards saved RAM.
Failure rolls back that host and stops the rollout. Test mixed control/plugin versions and repeated laptop sleep during updates.

## 12. Access, identity, relay and domains

All Eilmer-hosted names stay under eilmer.si.
The relay routes encrypted bytes and never receives the keys that decrypt sandbox traffic.
Certificate issuance must not require one certificate per sandbox.

| Access route | Behavior |
|---|---|
| Local only | Unix socket / ACL-protected named pipe, or loopback HTTPS; no public listener by default. Local account authentication works without internet. |
| Built-in Tailscale | Go access program embeds tsnet; encrypted direct paths with its relay fallback. Map verified tailnet identity/grants to Lamp scopes, never to blanket administrator. [tsnet](https://tailscale.com/kb/1244/tsnet) |
| Owner's domain | Embedded CertMagic handles automatic HTTPS and renewal, DNS-01 for wildcard names, or reachable HTTP/TLS challenges. DNS-01 does not make a NAT-hidden host reachable. [automatic HTTPS](https://caddyserver.com/docs/automatic-https) |
| Eilmer relay | Outbound host carrier, public SNI/TCP routing and one-time rendezvous grants; inner caller↔host encryption ends on hosts only. |
| Cloudflare plugin | Explicit optional access method with its own edge trust/termination properties; never described as the ADR 0009 relay. |

Tailscale workload federation lets GitHub callers join without a saved tailnet secret; Lamp's separate OIDC exchange grants application actions. [federation](https://tailscale.com/kb/1581/workload-identity-federation)
Tailscale setup/account approval is a one-time human enrollment task. It is not part of every create timer.
Support a configured Headscale control URL; offline fleet operation does not depend on reaching Tailscale's hosted control service. [tsnet options](https://pkg.go.dev/tailscale.com/tsnet)

Eilmer account authentication uses one shared Clerk instance, with identity keyed by issuer and immutable subject.
Clerk serves account authentication, not model sign-in. Fleets explicitly map an Eilmer account to owner/team/public-service roles.
Every relay caller needs an Eilmer account. A non-relay fleet may instead use Argon2id-backed local accounts or its own OIDC provider, including a locally hosted provider offline.
Public-service admission is owner-configured; it does not make sessions or storage public. Quotas apply before allocation.
Store random API keys hashed, display once and attach an intersected scope: sandbox/recipe/host, actions, CPU/RAM/disk/concurrency/budget and expiry.
Delegated keys can only narrow authority. Check scopes again on streams, reconnect, forks, mounts, SSH and approvals.

GitHub OIDC exchange validates issuer/JWKS, algorithm, audience, timestamps and replay ID.
Bind immutable repository/owner IDs, event, ref, approved workflow SHA and environment; names or actor display text alone are insufficient. [claim reference](https://docs.github.com/en/actions/reference/security/oidc)
Issue a ten-minute job-scoped Lamp key; a running job can renew only while its verified job authority remains valid.
Fork/public-PR trust is determined from forge data, not workflow-supplied labels. If the job lacks OIDC, trusted host-side CI intake supplies restricted execution; never substitute an owner secret.

The relay control service validates Clerk identity, records account↔host public-key links, and issues 60-second single-use rendezvous grants bound to fleet, host, caller key and nonce.
Hosts authenticate with enrolled keys and establish outbound carriers; QUIC streams are preferred, HTTP/2 CONNECT is the blocked-UDP fallback.
For native callers, run mutually authenticated inner TLS with host-key pinning inside the carrier. For browsers, pass raw HTTPS TLS through by SNI to the host.
The host verifies the one-use grant and its own caller authorization. A relay grant alone never authorizes exec.
The relay may terminate its control/carrier encryption, but never the inner sandbox-data encryption. It has no inner session keys or host certificate private keys.
Bootstrap native trust from an owner-authenticated host fingerprint/QR code, not a public key supplied solely by the relay; certificate-issuance/DNS authority is a separate trusted service.
No disk spool, payload logging, content inspection, model endpoint or artifact cache exists there. Metadata is limited to linkage, route, expiry and traffic accounting.
Bounded in-flight ciphertext buffers are forwarding, not durable storage. Relay compromise can deny service and observe routing metadata, not decrypt pinned connections.
Use standard TLS implementations; no new cryptographic protocol. [TLS 1.3](https://www.rfc-editor.org/rfc/rfc8446)

Use `auth.eilmer.si`, `docs.eilmer.si`, `relay.eilmer.si`, and per-fleet `api.f-7.eilmer.si`, `h-ID.f-7.eilmer.si`, `s-ID-p3000.f-7.eilmer.si`.
Untrusted sandbox pages never share an origin with an account, dashboard or API. Sandbox HTTP responses pass through a trusted host router.
Account/control cookies use unique `__Host-` names, Secure, HttpOnly, Path=/ and no Domain attribute; no Eilmer-wide cookies. [prefix rules](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie)
Treat sibling origins as hostile despite their shared same-site status: exact Origin checks, CSRF tokens, no state-changing GET, exact credentialed CORS, and Origin checks on WebSocket upgrades.
Use CSP, COOP, CORP and `Origin-Agent-Cluster: ?1`; never use `document.domain`. OAC is defense in depth, not a guaranteed process boundary. [browser behavior](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Origin-Agent-Cluster)
Strip sandbox-set cookies targeting parent domains, reserved header names and account routes. Script-set parent cookies remain possible, so control services accept only prefix-enforced host-only session cookies and ignore unrelated cookies.
Require supported browser cookie-prefix behavior; filtering response headers alone cannot stop `document.cookie` attacks. Per-origin service workers cannot control control-plane origins; never reuse an old sandbox hostname for a different owner.
Hostile pages cannot supply trusted reverse-proxy identity headers. Private URL authorization uses a port-bound expiring grant, exchanged for an origin-local cookie, not a fleet API key.
Clerk redirects exchange short-lived codes through trusted origins; do not deploy a Clerk SDK that sets parent-domain session cookies onto the sandbox namespace.

Generate one wildcard certificate key on a fleet host for `*.f-7.eilmer.si`; distribute it only to that owner's enrolled ingress hosts.
It covers any number of that fleet's API/host/sandbox labels without sharing keys across fleets or with the relay.
Central trusted account/static names use a separate certificate/key. A constrained DNS-01 issuer authorizes only the fleet's exact assigned wildcard CSR; it never receives its private key.
Persist certificates, use ACME staging in tests and ARI renewal where supported. New fleets still share the 50-new-certificates/7-day limit. [Let's Encrypt limits](https://letsencrypt.org/docs/rate-limits/)
Maintain a global issuance ledger and queue below 40 new fleet certificates/week, leaving repair headroom; existing fleets can create unlimited names without issuance.
Obtain a CA rate-limit override before broader hosted onboarding. Pending certificates are visibly pending; do not issue one certificate per sandbox or distribute a universal wildcard private key.
The rate-limit override is an external launch dependency. No other Eilmer-hosted domain is proposed.

## 13. CI and triggers

Each job gets fresh execution state. Public-repo stranger jobs require a fresh VM without owner secrets.
Each CI plugin translates its native contract to reserve → fresh create → job execution → output collection → unregister → delete, with cancellation and orphan reconciliation.
CI credentials remain in host brokers; an inside runner receives only the minimum job transport capability required by its protocol.
Never pass a long-lived runner registration, repository-write or installation credential to untrusted job code.

| Shipped CI plugin | Native attachment pattern |
|---|---|
| GitHub Actions | Go `actions/scaleset` listener, outbound polling and JIT single-job runners; no Kubernetes. [official client](https://github.com/actions/scaleset) |
| GitLab | Fleeting provider with capacity/use count one; supports native Instance and optional inside-Docker execution. [provider contract](https://docs.gitlab.com/runner/fleet_scaling/fleeting/) |
| Buildkite | Queue-aware controller starts `--disconnect-after-job` agents in fresh sandboxes. [agent options](https://buildkite.com/docs/agent/cli/reference/start) |
| Jenkins | Small Jenkins Cloud shim requests sandboxes and scoped inbound/WebSocket agents, one executor, one-shot retention. [extension contract](https://www.jenkins.io/doc/developer/extensions/jenkins-core/) |
| Gitea | Version-pinned ephemeral act_runner registration; host or inside-Docker job execution. [runner](https://docs.gitea.com/1.25/usage/actions/act-runner/) |
| Forgejo | Separate versioned registration adapter and `one-job` execution, never assumed identical to Gitea. [registration](https://forgejo.org/docs/latest/admin/actions/registration/) |
| CircleCI | Machine runner 3 in single-task mode; custom demand scaler and teardown. [configuration](https://circleci.com/docs/runner/machine-runner-3-configuration-reference/) |
| Azure Pipelines | Pool/demand-aware controller and `--once` agents, with unique registration and cleanup. [agent](https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/linux-agent?view=azure-devops) |
| Bitbucket | Label-specific controller and per-job runner lifecycle; intercept completion and prove teardown rather than assume a universal one-shot flag. [runner contracts](https://support.atlassian.com/bitbucket-cloud/docs/runners/) |
| Woodpecker | Provider/controller starts one-workflow agents with concurrency one; always delete the sandbox itself. [agent configuration](https://woodpecker-ci.org/docs/administration/configuration/agent) |

Preserve each CI's native cache/artifact/result APIs above Lamp's persistent storage. Optional inside-Docker kits supply build, Compose, job and service containers.
Native Xcode jobs use the macOS class; MSVC/Windows UI jobs use the Windows class. Linux-on-Mac does not substitute for either.
GitHub's documented Actions-based Copilot route uses `copilot-setup-steps` and an eligible scale set; its guest firewall requirements cannot disable Lamp's host policy. [official route](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-environment)
Run the official Claude Code and Codex actions with their allowed credentials on disposable runners. Do not claim arbitrary vendor-hosted agent relocation.
Stranger jobs get clean sealed-template descendants, no owner API/subscription/browser/vault/MCP connections, isolated untrusted caches, restricted package/source egress and blocked LAN/metadata.
Keep the runner transport outside job authority where feasible; otherwise its scoped job capability is disclosed and cannot retrieve owner secrets. Literal zero transport credentials inside all vendor runners is an unresolved contract issue.
Reject `pull_request_target` or forged trusted labels that execute untrusted head code with privileged connections. Never reuse an attacked sandbox or its writable root.
Triggers for Slack, Linear, signed webhooks and schedules map external identity to a scoped caller and deduplicate source delivery IDs.
Schedules use IANA timezones, run once per scheduled UTC occurrence across DST, default to no overlap, and report one missed-run summary after offline time. Retries retain the same operation ID.

## 14. Observability and limits

Dashboard, audit, budgets, watchdog and notifications are first-release work.
The dashboard subscribes to the authorized event stream and displays all sandboxes, sessions, hosts, device/resource use, durable storage, policy, approvals, jobs and failures.
Expose local OpenTelemetry-compatible metrics/traces; keep sandbox content out of tracing attributes.
Record every Lamp command, SSH/API exec and guest process exec, plus every mediated HTTP/tool request and allowed/denied network flow, outside guest-writable storage.
Use Linux immutable kernel audit/exec hooks and equivalent native guest adapters; detect missing audit coverage and block strict-audit admission.
Opaque encrypted streams have flow records, not invented application-request records. Full command/request completeness against hostile guest root is an explicit gate in section 19.
Audit entries carry owner, sandbox/session/job, sequence, policy, operation, destination, decision, approval and safe request ID; omit authorization and secret-bearing bodies.
Append to bounded host buffers and batched durable segments. Stop new work on audit overflow instead of silently losing events. Hash-chain and sign exported segments.
Default audit retention is 30 days, encrypted and owner-scoped, with explicit retention/export controls.

Reserve task budgets before create/fork and divide them across sub-agents, so parallelism cannot multiply an allowance.
Enforce wall time and token ceilings in host brokers before forwarding. Provider/harness usage reconciles reservations; deny a request that could exceed the remaining reservation.
Outside agents must route metered model calls through their authorized broker or supply trustworthy usage; unknown usage cannot be labeled an enforced token cap.
Watchdog defaults: warn after five minutes without observable progress, then block for a decision after three repeated equivalent tool cycles or ten minutes without progress.
Owner policy chooses cancel/terminate after a grace period; host enforcement kills the whole task process group or sandbox, not just the shell parent.
Blocked human approvals pause the no-progress detector, not the hard wall-clock budget. Native CPU-intensive builds count as progress through process/CPU signals.
Notifications for done, stuck and needs-decision go to configured console/phone/webhook plugins with owner-only links and no secret payload.
Usage data to Eilmer is off by default, including crash reports. Opt-in names each metric and retention; relay account/routing metadata is disclosed separately.
Leave quota and usage ledgers with stable task/owner IDs for later plans/billing; ship no payments or payment processor.

## 15. Security model

The protected-secret suite must report zero API-key, password and browser-cookie leaks on every host.
Assume malicious repositories, packages, prompts, guest root, hostile callers, public PRs, sibling web origins and ordinary untrusted plugins.
Trust the host kernel/hypervisor, confined engine boundary, authorized credential recipients, owner-approved brokers and the owner's administration of their hosts.
A compromised host administrator is outside guest isolation; no self-hosted design can hide host RAM from that administrator.

| Boundary | Required protection |
|---|---|
| Guest → host/neighbor | Hardware VM, minimal devices, confined engine process, no ambient host mounts, denied management operations. |
| Sandbox → network | Host-only routes/keys, default isolation, anti-spoof, strict DNS/TLS mediation and connection scopes. |
| Agent → credentials | Opaque per-sandbox broker handles, no secret-return APIs, destination/operation constraints and one-use approvals. |
| Caller → fleet | Shared authorization on every interface, owner-only resources/events, bounded delegation and expiry. |
| Plugin → core | Process/OS confinement, typed grants, bounded parsing and explicit trusted-engine roles. |
| Relay → plaintext | Host-only keys, pinned inner TLS or browser TLS pass-through, no payload persistence. |
| Untrusted web → account | Distinct origins, host-only cookies, exact Origin/CSRF checks, constrained certificates and no parent-domain cookies. |

Zero leaks is a falsifiable release test, not a mathematical guarantee against all future exploits.
Count real keys/passwords/cookies reaching agent memory, files, transcripts, logs, saved state or forbidden sinks; also check unauthorized actions even if no credential bytes leak.
Subscription exposure to its owner is the only ADR 0004 exception; ownership, vendor egress and exclusion from saved state still apply.
VM confinement does not make authorized data reads, deployment writes or authenticated browser actions harmless; scope and approvals control those effects.

Three incompatible general promises need resolution, not optimistic prose:
1. An arbitrary guest that has read a subscription bearer can hide copies. Exact RAM fork and provable universal removal cannot both be guaranteed; unsafe capture is refused.
2. Deliberately injecting a password or copying cookies gives guest code those values. Such opt-in modes cannot honestly score zero disclosures under an unconditional all-modes metric.
3. Unrestricted browser scripting or opaque encrypted networking defeats generic cookie secrecy or semantic request auditing. Protected modes restrict these interfaces and report unsupported cases.
These remain first-release requirements/gates. This design does not silently waive them or count a refusal-only system as full feature acceptance.

## 16. Performance budget

Budgets are engineering allocations, not measured Lamp results.
Timing starts when the API request is sent and ends at independently observed command execution, including authorization and policy.
Initial test configuration: native-architecture Linux, 2 vCPUs, 1 GiB RAM, local prepared template, SSD, no optional human devices, and a short local API path.
Measure 128 MiB, 1/2/8 GiB, full dirty RAM, memory pressure, old disk chains and requested devices too. This initial envelope is not a waiver for slower accepted configurations.

| Linux create phase | Budget, ms | Mechanism / evidence |
|---|---:|---|
| Transport, auth and scope | 20 | Cached validated identity; no Clerk round trip. |
| Placement, intent and quota | 20 | One home journal transaction and host-local atomic admission. |
| Writable disk descendant | 25 | Reflink or extent-map generation, not disk-byte copy. |
| Network/connection policy | 30 | Prepared ports, atomic policy swap, broker handles. |
| Engine and RAM restore | 100 | Firecracker privately maps saved RAM; upstream does not establish this Lamp budget. [mechanism](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md) |
| Uniqueness and settings | 45 | Prebuilt settings chunks; synchronous rekey/reseed hooks. |
| Command start and observation | 35 | Existing guest agent channel and independent running marker. |
| Jitter allowance | 15 | Included in total, not subtracted from observations. |
| **Prepared create total** | **290** | Leaves 210 ms to the binding 500 ms boundary; equality fails. Warm-slot hypothesis: 100–160 ms. |

| Linux running-source fork phase | Budget, ms |
|---|---:|
| Authorize, reserve and close new I/O | 20 |
| Certified clean-state credential gate | 50 |
| Quiesce queues, flush and seal disks | 70 |
| Capture RAM/CPU/device state, including final dirty pages | 220 |
| Restore fork VMM; resume source | 90 |
| Reseed, rekey, clocks and connection hooks | 65 |
| First command and observation | 35 |
| Jitter allowance | 50 |
| **Live fork total** | **600** |

The 220 ms RAM allocation needs measured bandwidth: 1 GiB eager capture in that time requires about 4.55 GiB/s effective copy bandwidth before other work.
An 8 GiB fully dirty capture cannot be called constant-time merely because restore is lazy. Pre-copy must converge faster than the guest dirties pages, or capture must use verified first-write CoW.
Never hide pre-copy CPU/RAM/disk costs: publish them and run a sustained 97% throughput test with fork support enabled.
Firecracker publishes ≤125 ms to init and >95% compute-only, not ≥97% full throughput. These are feasibility clues, not proof. [specification](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md)

| Host / Linux microVM | Prepared create hypothesis | Live fork hypothesis | Throughput objective |
|---|---:|---:|---|
| Linux x86_64 / arm64 | 220–350 ms p50 | 450–750 ms | ≥97% independently for compute, disk and network. |
| M5 Mac / HVF | 250–380 ms p50 | 550–850 ms | Same objective; not established by Apple framework support. |
| Windows x86_64 / WHP | 300–450 ms p50 | 650–950 ms | Same objective; memory mapping and packet path are critical experiments. |

Set published Mac/Windows SLOs only after measurements, as the brief requires; retain this lens's faster objectives meanwhile.
Native macOS/Windows full-VM planning allowances are 1–10 seconds prepared resume and 10–90 seconds cold boot, unmeasured; no microVM-speed claim applies to them.
Use native CPU execution, batched multiqueue block/network paths, direct block I/O where safe, and prefault only the command's working set.
Compare compute, sequential/random disk, fsync/metadata and network throughput/latency separately, with equivalent host resources and correct outputs.
Report native direct baselines plus equivalent VPN/proxy-path diagnostics; never weaken sandbox policy or inflate the native baseline's overhead to meet 97%.
Any failing declared measure remains a failing gate. High-throughput bulk I/O cannot hide a failing metadata or small-request result.
WAN RTT, vendor login and uncached downloads are included in observed end-to-end cases; a physical network delay cannot be removed by a faster VMM.

## 17. Verification and machines

Use the M5 Mac, Ubuntu laptop and MacBook Air, plus external Windows hardware and Linux arm64 coverage.
Use public third-party fixtures and dedicated test accounts.
Unit/property tests cover policy algebra, parsers, budgets, extent-map crash recovery, idempotency, epochs, origin checks and plugin negotiation.
Integration tests run real engines, guest commands, durable mounts, proxy TLS, browser actions, network enforcement and failover. Fake vendors exercise errors; real packet paths and RAM capture are never mocked as proof.
E2E tests implement all five requirements journeys: fresh install/sign-in, mixed fleet/consoles, fan-out/fork/winner, public-PR CI, and team relay/approvals.
Run all named harnesses, shipped CI plugins and four password managers against dedicated supported fixture accounts before release.

| Lane | Real evidence required |
|---|---|
| Ubuntu laptop | Inventory CPU/RAM/KVM first; Linux x86_64 if qualified, engine demo, disk/network performance and no-KVM fallback. |
| M5 Mac, 10 cores/24 GB, macOS 26 | HVF Linux, VZ macOS, two-macOS cap, device absence/presence, APFS fork and thermal/density tests. |
| MacBook Air | Record actual architecture/version; second-host sleep/wake, teleport, console takeover and failover. Do not assume it supplies arm64 Linux-host evidence. |
| External Linux arm64 | Dedicated arm64 Linux/KVM host, native guest/kernel, all benchmark/security lanes. A Linux guest on a Mac is insufficient. |
| External Windows | Licensed dedicated Windows 11 Pro/Server bare metal with virtualization enabled; Linux WHP and full Windows Hyper-V guests, network/resource confinement and continuing-parent fork. |
| Hosted Windows CI | Build, unit and protocol tests on standard runners; virtualization E2E still runs on the dedicated host. Never count compilation as host support. |

Rent a dedicated Windows-capable bare-metal test host or arrange a hardware lab loan; no owner Windows purchase is assumed.
If a provider offers only nested virtualization, probe actual WHP/Hyper-V support first and label those results nested; rent bare metal for native-speed certification.
No Windows provider is assumed qualified without install/boot/fork/isolation evidence. Missing hardware blocks release, not first-release scope.
Every change queues the full Linux/macOS/Windows matrix, benchmark and attack suites; scarce physical hosts serialize jobs and keep the change pending until complete.
Benchmark Lamp against pinned Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM where supported; include create, agent-ready, RAM fork, density, reliability and attacks.
Use 20 warmups, 200 creates, 100 forks/configuration and ten alternating native/guest throughput trials; publish all failures, raw samples, p50/p95/p99, max and confidence intervals.
Fork has no percentile exemption: all declared target trials must finish below one second. Report cold/warm state and foreground pause separately.
Execute requirements attacks ATK-01–44 on every applicable host, including malicious root/plugin code, encoded canaries, reflections, DNS/IPv6/UDP bypass, cookie tossing, unsafe captures and interrupted teleport.
Fixtures include public Flask/CPython projects, official harnesses, public MCP servers and public console contracts. Never use Ozi, Apple Bridge, money-bridge or owner credentials/projects.
Keep initial evidence private until the first engine demo works; then publish reproducible benchmark source/results under the release policy. This design grants no publication or spending authorization.

## 18. Build plan

The first gate is a real Linux create and memory-inclusive fork demonstration within the binding limits.
No non-engine implementation begins before that gate.

| Milestone | Required exit evidence |
|---|---|
| 0. Linux engine first | Lamp seed/template builder, direct boot, command channel, isolated disk, memory-only marker fork, parent continuation; real create <500 ms p50 and every target fork <1 s. No Docker inputs. Repo remains private. |
| 1. Engine performance/security | Linux arm64, CoW on ordinary filesystems, full/dirty RAM tests, suspend/reseed/clocks, 97% category gates and guest-root attacks. |
| 2. Native host engines | M5/Air and external Windows lanes; continuing-parent fork, device/resource confinement, full macOS/Windows classes and honest fallback. |
| 3. Credential/data boundaries | API/CLI signing proxy, MCP, browser/fill, four vault plugins, subscription catalogue/capture gates, persistent storage and host-only networking. |
| 4. Fleet and interfaces | Operation home journals, failover/sleep/update, API/MCP/CLI, all four SDKs, session consoles/SSH/sub-agents/teleport. |
| 5. Access and automation | tsnet/WIF, relay/Clerk/local/OIDC, owner HTTPS, certificate/origin attacks, every CI/trigger/schedule plugin and public-PR isolation. |
| 6. Complete first release | Dashboard/audit/budgets/watchdog/notifications, installer usability, license/SBOM review, every requirement and public comparative evidence. No unresolved gate relabeled done. |

Distribute Lamp-owned host code, CLI, SDKs and shipped plugins under Apache-2.0; relay code under AGPL-3.0.
Keep the AGPL relay implementation separate from Apache-licensed host packages; shared wire schemas may remain Apache-2.0.
Preserve compatible dependency notices; platform frameworks and guest OS/software retain their own licenses. Avoid a GPL-only VMM dependency in the host engine distribution.
Optional inside-Docker is guest software, never Lamp's host runtime. Test redistributed package rights separately from Lamp's license.

## 19. Risks and first experiments

Unresolved platform, credential-scrubbing and audit limitations must remain visible release gates.

| First experiment / risk | Pass threshold | Fail consequence |
|---|---|---|
| Linux end-to-end engine | 200 creates p50 <500 ms; 100 live forks each <1 s; memory/disk markers diverge correctly and source continues. | Stop before non-engine implementation; profile actual capture/disk phases. |
| 97% performance | Every declared compute/disk/network measure ≥0.97 with normal policy/audit/fork tracking enabled. | Trial CH/multiqueue/extent paths; do not average away failure. |
| Mac HVF + APFS | Create p50 <500 ms, all target forks <1 s; state, clocks and source continuation correct under pressure. | Investigate native mapping/device capture; do not substitute VZ Linux assumptions. |
| Windows WHP | Same speed gates, NTFS CoW path, guest-memory isolation and no frozen-source requirement. | Implement verified write-fault CoW or choose another audited WHP engine; Windows release remains blocked. |
| Arbitrary subscription copies | Hide bearer copies in files, heap, compressed data and buffers; capture contains none or refuses before writing any state. Cooperative continuity also passes. | Universal scrubbing is unproved; restrict unsafe capture and record the unresolved fork/lifecycle requirement. |
| Browser/vault secrecy | Four managers fill real fixtures; zero canaries through DOM, CDP-like requests, screenshots, cookie reflection or MFA takeover races. | Narrow adapter surface or human takeover; no universal protected-browser claim. |
| Proxy compatibility | Real `gh`, `stripe`, `aws` and named harness operations succeed; zero key bytes/encodings through reflections, errors, redirects or saved state. | Add typed protocol adapters; never fall back to raw token injection. |
| Host policy | Guest root/nested Docker cannot reach denied IPv4/IPv6/DNS/UDP/LAN/metadata sinks on any host. | Block the engine/network plugin from selection. |
| Two-host failover/dedup | Either coordinator disappears; fresh work proceeds on survivor within 5 s; retries create zero duplicates; uncertain old operations remain pending. | Fix authority/journal routing before fleet release. No unsafe re-placement. |
| Audit fidelity | Fixture ledger matches every exec/request; guest tampering and buffer exhaustion produce no silent gaps or secret logs. | Resolve semantic/opaque protocol limits; strict-audit support remains incomplete. |
| Domains/certificates | 1,000 sandbox names in one fleet need no new certificate; hostile sibling gains zero account authority; new issuance stays below quota. | Correct origin controls; queue new fleets or obtain the CA override. |
| Full-VM/device state | macOS/Windows RAM marker forks with source continuation; every advertised requested device works and unrequested devices are absent. | Do not advertise that combination; first-release device/fork scope remains a gate. |
| Suspend/teleport/updates | Suspended guest RAM/CPU allocation is zero; no lost in-memory marker; interrupted move has one writer; only one host updates. | Fix lifecycle fencing and state compatibility; never cold-start and call it continuity. |
| External Windows/arm64 access | Reproducible native install/guest/fork/isolation and resource tests run on actual qualified hosts. | Obtain a qualified lab host; emulation/build-only lanes cannot close the gap. |

Finite dirty-memory bandwidth, nonmigratable physical devices, unavailable harness continuation formats and malicious trusted recipients are architectural limits to measure explicitly.
Draft questions Q11/Q19/Q22/Q25/Q30/Q33 are not settled by a fast empty-guest demo.
Do not invent stronger vendor permission, general secret erasure, same-origin secrecy or fault-tolerant storage than the evidence supports.

## 20. Deliberate differences and coverage

Compare with Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM using the same measurement endpoints.

| Reference | Deliberate Lamp choice |
|---|---|
| Cloudroom | No developer-operated subscription vault; owner-host custody and policy outside the guest. Fleet/session/console primitives are public interfaces rather than one console's session lifecycle. Its credential synchronization is documented, not inherited vendor permission. [security](https://www.cloudroom.dev/security) |
| E2B Embed | Native Linux guests on Mac/Windows hosts, one fleet across hosts, no Docker/Compose prerequisite and no OCI template import. Reuse Firecracker security mechanics, not its entire deployment stack. [Embed](https://github.com/e2b-dev/infra/tree/main/embed) |
| Docker Sandboxes | Adopt the useful host sentinel pattern for API keys; do not proxy subscription tokens without approval from their vendor. Own template format, explicit fork/capture correctness and self-hosted fleet protocol. [credential mechanism](https://docs.docker.com/ai/sandboxes/configuration/credentials/) |
| microsandbox | Reuse only audited low-level engine work where it is the likely fastest path. Own admission, templates, block storage, host policy, credentials and fleet control. Its CoW full restore is opt-in and not proof of subsecond capture. [state behavior](https://github.com/superradcompany/microsandbox/blob/main/docs/sandboxes/snapshots.mdx) |
| SmolVM | Require continued parent execution on Windows, disable automatic TSI escape paths and measure actual API-to-command/fork endpoints. No OCI pack/import path or required branch helper in an arbitrary task. [fork contract](https://github.com/smol-machines/smolvm/blob/main/docs/branching.md) |

| Binding brief / draft inventory | Design sections |
|---|---|
| §§1–4; PRD, INT | 1–2, 8–9; all callers, both agent modes, SDK/interface parity. |
| §§5–7; HST, SBX, BEN | 3–4, 11, 15–17; host/guest matrix, resources, fallback, every speed/security gate. |
| §§8–10; TPL, LIF, STO | 4–5; own templates, recipes, idle/suspend/delete, warm pool/fork and owner-only persistent state. |
| §§11–12; NET, PLG | 6, 10; all five network functions and every plugin category. |
| §§13–15; CRED, CON, KIT | 7; every named harness, one-way settings, three connection kinds, browser/password/toolchain/Docker kits and approvals. |
| §16; SES | 8; one machine, all sessions, ordinary SSH, sharing, sub-agents/status and teleport. |
| §§17/20; ACC, RLY, OPS | 11–12, 14–15; every access route, Clerk/local/OIDC, scoped keys, E2E relay and eilmer.si isolation/certificates. |
| §§18–19; CI, OPS | 13–14; every shipped CI family, agent actions, public safety, triggers/schedules, dashboard/audit/budgets/watchdog/notifications. |
| §§20–23; ENG, QA | 2, 16–19; reuse/languages/licenses, no payments/telemetry by default, benchmark/release order and actual test machines. |

All six supplied research reports informed this design. Their proposals do not override the brief or ADRs.
External statements are linked next to the claim; source capability, vendor claims, engineering budgets and unresolved release gates are kept distinct.
