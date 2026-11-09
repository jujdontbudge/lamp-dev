# Lamp architecture: simplicity and buildability first

Round 1 design. Binding inputs: [brief](../brief.md), [glossary](../../../GLOSSARY.md), and ADRs 0001–0009.
The brief wins over research and this proposal. The parallel requirements draft is a coverage check, not a source of new binding decisions.
All choices below are proposals. Upstream facts have source links. Timing budgets are estimates, not Lamp results.

## 1. Design position

Build the Linux engine first. Prove API-to-command create below 500 ms at p50, a memory-inclusive live fork below 1 s, and at least 97% native computation, disk and network speed separately.
Do not confuse a fast restore with a fast live fork. Do not count prewarming as free work in density or cost measurements.
Keep one host control program, one guest control program and a small number of engine adapters.
Use host-enforced connections. Keep API keys, passwords and browser cookies outside ordinary sandboxes.
Treat subscription logins as the narrow exception fixed by ADR 0004.
Keep every feature in the first-release brief. Plugins bound implementation complexity; they do not defer scope.
Choose Go for control and Rust for VM work. Reuse one libkrun-derived core for Linux guests across hosts.
Keep the dashboard small. Do not require Kubernetes, Docker, a registry, Redis, Postgres or an object-storage service.
The principal uncertainty is whether this small engine can meet every Linux performance gate. Section 21 makes that a stop condition.

## 2. System overview

```text
Caller: API / MCP / lamp CLI / console / stock SSH
  | local socket, Tailscale, owner HTTPS, or encrypted relay path
  v
Any reachable host: lampd [Go]
  API + authorization + placement + sessions + embedded coordination
  local SQLite + content-addressed files + dashboard + event journal
  | authenticated host-to-host streams; same API on every host
  +--> another host's lampd
  +--> lamp-priv: small installed resource/network helper
  +--> lamp-engine [Rust, one process per sandbox]
  |      KVM / Hypervisor.framework / Windows Hypervisor Platform
  |        -> Linux kernel -> lamp-init [Rust] -> tasks and harnesses
  +--> separate engine adapter: Apple VZ macOS / Hyper-V Windows
  +--> lamp-broker [Go, separate process per owner]
  |      API-key proxy; MCP; CLI signing; approvals; connection policy
  +--> browser service [separate locked-down sandbox by default]
  +--> separate plugin processes, started only for enabled features

Eilmer: Clerk identity + relay control service + opaque byte forwarding
  No model credentials, sandbox disks, transcripts, or fleet database.
```

`lamp` is a CLI and can run `lampd`; packaging supplies a normal OS service.
`lamp-priv` receives validated operations, never shell text. It opens only allocated resources, then drops authority.
On Mac, the normal signed app/service runs with its real home directory. No HOME overrides or password-prompting diagnostics are part of this plan.
The broker runs as a distinct OS identity from engine workers. An engine compromise must not inherit a vault or CA key.
The browser service and broker are outside the agent sandbox. They are trusted, separately scoped components.
Local execution survives a console disconnect. Fleet coordination never carries terminal byte streams through its replicated log.
Keep small fleet metadata replicated. Keep bulk state on the host that owns it, with explicit replication when requested.

## 3. Languages and reuse

| Choice | Reason and rejected alternative |
| --- | --- |
| Go: daemon, CLI, broker, relay, most native plugins | Direct use of `tsnet` and `actions/scaleset`; one HTTP/auth/event stack. Both libraries expose the required embedding contracts. [^S1][^S2] |
| Rust: engine, init, guest service, WASI plugin runner | Direct access to the Rust VM core; bounded device parsers; no Go runtime or FFI inside a vCPU loop. |
| Rust calls Apple Objective-C APIs through generated bindings | A thin VZ adapter avoids a maintained Swift application. Verify bindings against the installed SDK. [^S3] |
| HTML templates, CSS and small JavaScript modules | Ship static dashboard assets in `lampd`. No Node service or required SPA toolchain. Generated TypeScript SDK is a separate deliverable. |
| Any language for third-party plugins | A process protocol is the contract. Jenkins may need a small Java-side shim; it does not become a Lamp runtime language. |

Go-only would still need an external Rust VMM and would not simplify its lifecycle.
Rust-only would require a Go access helper and a Go CI helper, or reimplement their security-sensitive protocols.
Two deliberately separated systems languages are simpler than either approach. They communicate over local sockets, never a shared heap.
The first-party host programs and plugins are Apache-2.0. The relay service is a separate AGPL-3.0 build.
Preserve dependency notices; Apache-2.0 on Lamp code does not relicense Linux, browsers, Docker kits or OS frameworks.

| Reused component | ADR 0003 justification |
| --- | --- |
| `msb_krun` and its device crates | Hardware boundary plus existing native capture/restore; likely faster than implementing three VMMs. Prove speed before adoption. [^S4] |
| Firecracker, as a Linux comparison/alternate engine plugin | Mature restricted device boundary; comparison decides whether it is faster for eligible requests. [^S5] |
| `tsnet`, TLS, SSH, OIDC, ACME libraries | Security protocols and cryptography should not be reimplemented. [^S1][^S16] |
| `actions/scaleset` | Correct GitHub registration and lease handling; prevents a new authentication implementation. [^S2] |
| SQLite, embedded Raft, WASI runtime, packet stack | Crash consistency, distributed authorization consistency, hostile-plugin isolation and packet-parser safety. [^S12][^S13][^S14] |

Lamp writes its resource model, placement, template builder, lifecycle, session protocol and policy engine.
Do not import microsandbox's product daemon, OCI pipeline or SDKs. Pin dependencies and record a security/speed rationale in the component manifest.

## 4. Engines and guest construction

### Default engine family

Start the engine experiment with `msb_krun` 0.1.45, published November 7, 2026.
Source exposes paused execution capture, memory capture, private memory restore, device quiescence, and generation/clock acknowledgment. [^S4]
This is a downstream core. Upstream libkrun 1.19.8 is not a snapshot API and does not have this Windows backend. [^S6]
Use the same Rust process wrapper and guest disk format across all three host OSes.
No `fork()` of a multithreaded VMM with inherited KVM handles. Construct a fresh hypervisor instance from captured state.

| Host | Linux guest engine | Native guest class | Host controls |
| --- | --- | --- | --- |
| Linux x86_64/arm64 | `msb_krun` on KVM; Firecracker plugin is the required comparator | Linux only in this proposal | cgroup v2 CPU/memory/pids/I/O; separate UID, mount/network namespaces, seccomp, closed descriptors |
| macOS Apple Silicon | Same core on Hypervisor.framework | Apple Virtualization.framework for macOS; maximum two allocated macOS sandboxes per Mac | separate worker identity and file ACLs; framework CPU/RAM configuration; disk quota/reservation; no host directory export |
| Windows x86_64 | Same downstream core on WHP | Hyper-V management API with standard memory checkpoints for Windows | restricted token, per-worker ACLs and Job Object; WHP allocation; Hyper-V resource controls for full VMs |

KVM, HVF, WHP and VZ are different interfaces; a common library does not make their behavior identical. [^S6][^S7][^S8]
Initial certification targets Linux kernel 6.12+, macOS 26 on Apple Silicon, and Windows 11 24H2 Pro/Enterprise or Server 2025 x86_64.
Probe APIs and required OS features at install. Minimum versions are proposed support policy, not claims of upstream compatibility.
Publish the actual tested distro/build matrix. Windows arm64 is a later certification candidate, not silently inferred from an API announcement.
Linux x86_64 and arm64 both ship in release one. Do not translate x86 binaries on arm and report native performance.

`lamp-init` is PID 1. It mounts the root disk, reaps processes, starts the guest service and owns task cgroups.
Use a pinned minimal Linux LTS kernel with virtio drivers built in, a generation/clock driver, and the Docker kit's required namespaces/cgroups/filesystems.
For the krun engine, package that kernel as a signed `libkrunfw` bundle and select `krunfw_path`/`init_path`; Firecracker receives the corresponding raw kernel. Never load a caller-supplied host library from a template. [^S4]
Disable arbitrary kernel modules, kexec and `/dev/mem` in the supported guest kernel. Custom kernels require separate certification.
The root contains a normal package-managed distribution. Init does not start SSH, cloud-init or a general service manager on the fast path.
The guest service offers exec, file transfer, PTY, process inventory, activity, capture hooks and typed device control.
The host treats its messages as untrusted. Guest-side identity assertions never authorize a host connection.

### Devices, limits and alternate classes

The mandatory platform contract is CPU, RAM, boot storage, clocks/interrupts and a private control transport.
The public `devices` list contains optional hardware. Network access also requires a declared network attachment.
Headless builds omit graphics/audio/input code. Device builds add only requested display, keyboard, mouse, audio or virtual GPU devices.
Inspect the constructed inventory; do not infer absence from an empty request list. Disable automatic TSI socket forwarding explicitly. [^S6]
Native host desktops, microphones, cameras and arbitrary host paths are never inherited.
The core has graphics, sound and input features; this establishes code, not every host/device combination. [^S4][^S6]
Use virtual graphics on compatible Macs. A Cloud Hypervisor plugin handles Linux VFIO GPU requests; the Hyper-V adapter handles Windows GPU-P where certified. These paths are outside the headless core. [^S9]
No GPU emulation claim substitutes for actual GPU access. Passthrough devices need IOMMU isolation and exclusive leases.
An unforkable device configuration returns `DEVICE_NOT_FORKABLE` before capture. It remains visible as an unsupported combination.
Configuration-time attachment is sufficient for release one; no general hotplug promise is needed.

CPU limits include engine overhead. Reserve RAM plus measured overhead; do not overcommit by default.
Use explicit units: vCPU count, MiB RAM and GiB disk. Default Linux requests are 2 vCPU/2,048 MiB/8 GiB; recipe and caller limits may narrow them. OS controls and all enforcement failures are tested. [^S44]
Use virtual disk capacity plus host allocation quotas and a free-space reserve. Kill the complete task tree on expiry/OOM policy.
On Mac, hard CPU scheduling controls are less precise: admit fixed vCPU counts, reserve capacity, and stop a worker exceeding its enforced budget.
Do not advertise a percentage CPU quota on that platform until measured enforcement exists.

macOS templates are built from the owner's Apple restore media, with auxiliary storage and machine identity kept together.
Use VZ's support validator before memory save/restore. Its saved state has compatibility restrictions; no cheap Linux-fork claim follows. [^S7]
Windows templates use owner-licensed installation media, unattended setup and the Lamp guest service.
Use standard Hyper-V checkpoints, not production checkpoints, when memory must be retained. [^S8]
Full-VM fork preserves RAM through a supported checkpoint export/import with fresh machine/network identity; measure parent continuation and guest licensing.
Both native guest classes are labeled “full VM, slower.” Never distribute an unlicensed OS template.
Capture/fork of native guests is a release gate, including VZ same-Mac identity restrictions; an unsupported API is not a disk-only substitute.

Without virtualization, Linux uses gVisor systrap with Lamp-prepared roots and no Docker runtime. [^S10]
Mac/Windows offer a clearly labeled restricted native-process engine under a separate identity, ACLs and denied host services.
Use an entitled App Sandbox helper on Mac and an AppContainer on Windows, without network grants; expose approved network operations only through typed broker IPC. These are weaker boundaries and require escape tests. [^S43]
That fallback runs native tasks; a Linux request is placed on another eligible host or rejected, never mislabeled as Linux.
Weak engines forbid sign-ins, secret connections and logged-in browsers unless the owner explicitly records a scoped override.
A public-repo stranger job can never use that override. It must have a fresh real VM.

## 5. Templates, create, fork and lifecycle

### Format and builds

A `.lamp` definition is TOML. A resolved template is an immutable manifest plus content-addressed kernel, init and disk objects.
The manifest fixes guest OS/CPU, kernel/init hashes, boot arguments, disk layout, engine ABI and optional prepared memory state.
Use SHA-256 digests, bounded manifest sizes and signed provenance. Names resolve to immutable revisions; existing sandboxes retain the resolved revision.
Templates and recipes are owner-only by default. A recipe references a template, harness, kits, devices, resources, network and connections.

```toml
format = 1
name = "python-tools"
guest = "linux"
arch = "arm64"
rootfs_url = "https://<distribution-mirror>/<release-rootfs>.tar.xz"
rootfs_sha256 = "<required digest>"
packages = ["python3", "git", "ca-certificates"]
steps = [["python3", "-m", "pip", "install", "ruff==<pinned-version>"]]
```

Bootstrap a signed distro root archive, a Lamp kernel and init into an ext4 disk in a disposable build sandbox.
Run package installation and ordered argv steps there. Resolve package locks and record mirrors/checksums before publishing.
The host never extracts arbitrary archives as root. Reject traversal, device files and unsafe links during import.
No Dockerfile, Docker reference, OCI layer conversion, container registry or host Docker daemon is involved.
An optional Docker kit installs a daemon *inside* the Linux sandbox. It supports build, Compose and service containers without sharing a host socket.

Saving installed tools uses a quiesced running sandbox. Detach credentials and external storage; flush, capture the private root and publish a new template revision.
Disk-only template save is the default. An optional prepared-memory template must pass the same sanitation and identity gates as suspend.
Saving never changes the original template in place. Restore resumes the source after success or rollback.

### Disk and RAM ownership

| Host/storage | Fast disk path | Portable fallback |
| --- | --- | --- |
| Linux XFS reflink or btrfs | Reflink a raw ext4 disk; fork while vCPUs and device writers are quiesced | Short raw/qcow2 backing chain through the Rust block backend on ext4 |
| macOS APFS | `clonefile` on the same volume | Same short block chain when cloning is unavailable |
| Windows ReFS | Block-clone where the volume and alignment qualify | Same block chain on NTFS; full Windows guests use VHDX differencing disks |

These are native extent-sharing primitives, not memory-fork primitives. NTFS is not assumed to support ReFS cloning. [^S11]
The existing Rust backend accepts explicitly resolved raw/qcow2 layers and refuses implicit host paths from disk headers. [^S4]
Cap backing depth at two; compact inactive generations off the request path. Test first-write allocation costs against the 97% disk gate.
Never make immutable ancestors writable. Persist references before garbage collection; retain an ancestor until its last descendant is gone.
For a layered-disk fork, pause writers, seal the old head and atomically install two new writable heads before resuming either sandbox. A raw reflink instead gives each its own writable inode.
Head rotation is required engine work, not an assumed property of a COW file format; crash-test descriptor replacement and journal recovery before using the ext4/NTFS fallback.
Guest dirty page cache may be retained in a full capture. Disk-only saves must flush it first.

### Create and live fork

Prepare kernel, disk and a signed-out boot checkpoint ahead of time. Cache them locally before marking a template ready.
Create reserves resources, claims a pristine warm sandbox or restores that checkpoint, installs host policy and fresh identity, then executes a command.
The warm pool is keyed by owner, template revision, kernel/engine ABI, resources, devices and policy revision.
Default minimum is zero; recipes can request a positive minimum. Refill after claim, within explicit RAM/disk budgets.
Warm misses use checkpoint restore. Never recycle a sandbox that has executed a caller task into the pristine pool.
Bind credentials after assignment. A warm sandbox does not hold another person's subscription or browser state.

Live fork sequence: authorize/reserve; run sanitation hooks; freeze tasks; pause vCPUs; drain device workers; capture CPU/device/RAM; isolate disk state; restore a new worker; run identity hooks; prove a command runs; resume both.
The fork is an exact execution copy subject to required credential removal and new identity. It is not a fresh harness with copied files.
Use the core's capture-generation tokens to reject mixed CPU/device/RAM generations. Parent rollback resumes the original device continuations. [^S4]
Initially stream RAM into a private host-memory-backed immutable file. Restore maps it privately; writes diverge.
This avoids a storage round trip but still copies live RAM at capture. Count that copy in the fork budget.
On Windows, qualify the core's private restore mapping separately. On Mac, qualify page size and HVF memory registration separately.
Do not assume an APFS disk clone makes live anonymous RAM shareable.
Only add incremental capture after the full-capture path is correct and measured. Its dirty tracking must also pass the steady-state speed gate.
Ephemeral fork state can be lost on host failure until durable capture completes. The API exposes durability; durable mode includes fsync and its measured latency.

### Suspend, restore and uniqueness

Default policy: suspend after five minutes of verified idle; delete after seven days suspended. These are proposed defaults, configurable within caller limits.
Idle requires no session activity, task processes or open task connections. Quiet background work and sleeping task processes count.
Only named Lamp control services and a quiescent harness control loop are exempt; publish the exemption list.
Keep the control socket out of task connection accounting. Shell attachment/input, a session message or a permitted inbound request wakes suspended work.
CI defaults to deletion on every terminal outcome, after outputs are collected.
Suspend persists and fsyncs state, exits the worker, unmaps RAM and closes its disk handles.
Evict saved-memory pages from the host cache as well; report daemon overhead separately. A suspended sandbox has no resident guest allocation.
Resume demand-pages saved memory. “Instant” means no rebuild or sign-in dialogue on the normal prepared path; actual latency is always reported.

Before a restored task executes: install a fresh generation identifier, reseed kernel entropy, correct wall time, acknowledge the generation, and reset delegated authority. [^S4][^S15]
Give a fork a new sandbox ID, vsock identity, MAC/IP, hostname, machine ID, SSH keys and private-network member.
Rotate the broker handle even for same-sandbox resume. Check all expiries against current host time.
Preserve logical monotonic progress while accounting for elapsed suspend time in deadline policy; test long sleeps and clock rollback.
Kernel reseeding does not reset arbitrary userspace PRNGs. Harness/kit restore hooks refresh UUIDs, TLS sessions, connection pools and PRNG state. [^S15]
Block external access until these hooks finish. Reset inherited TCP/UDP flows in the fork; the parent keeps its own flows.
External database locks, device state and shared volumes are not duplicated. The API lists these attachments and requires a compatible fork policy.
Cross-host RAM restore requires the same architecture, CPU baseline, engine ABI, kernel and device contract. Teleport uses a separate session-level path.

## 6. Lasting storage

Keep one host-local content store, with SQLite metadata and immutable chunks on disk. No mandatory S3 service.

| Resource | Semantics |
| --- | --- |
| Cache | Owner + trust class + toolchain/architecture key. Restore immutable generations; each task writes a private delta. Publish with compare-and-swap; evict by quota/LRU. |
| Named volume | Directory service on one chosen storage host. Several sandboxes attach concurrently through SMB3 on all guest OSes, confined to a dedicated export. |
| Artifact | Immutable checked chunks, manifest, retention and resumable transfer. Finalize atomically; retain logs/reports on success, failure and cancellation. |
| Template | Immutable versioned objects. Save installed tools explicitly; do not absorb attached caches/volumes or credentials. |

Use a small dedicated Linux storage sandbox running Samba on every host OS. One protocol avoids NFS/SMB cross-protocol locking problems and three host-server implementations.
Reuse is justified by filesystem-protocol security; benchmark it separately from private-root disk speed and against an equivalent native network mount.
Host grants each attachment its own network route and credentials; guest mount credentials authorize only that named export.
SMB3 supplies one server's locking/lease rules across guest OSes; applications still need their own coordination. Test case sensitivity, symlinks, permissions and disconnect recovery explicitly. [^S17]
Never attach one writable ext4 block disk to several independent kernels. A single-writer block-volume mode is also available.
Cross-host attachments route through authenticated host links to the storage host. If it sleeps, I/O fails or blocks within a stated timeout; data is not silently replaced.
Optional synchronous replication gives a second storage host durability. Coordination failover alone does not replicate file bytes.
Owner-only authorization applies to names, listings, contents and attachments. Team membership does not automatically grant access.
Stranger PR jobs use a separate cache namespace; no writes promote into trusted caches. They cannot attach protected volumes.

## 7. Host-enforced network

The host owns the only route out of each sandbox. Policy changes replace a complete revision atomically and invalidate disallowed flows.
The engine cannot open arbitrary internet sockets. Disable TSI and AF_UNIX forwarding; its control transport speaks only the bounded guest protocol. [^S6]

| Host | Packet path and enforcement |
| --- | --- |
| Linux | virtio-net → dedicated TAP/network namespace → nftables policy → selected egress. No default host bridge. Credential destinations redirect to the broker. |
| macOS | virtio-net → private frame transport → isolated host packet service → policy → explicit host sockets or selected exit. No shared/NAT framework network attachment. |
| Windows | WHP guest NIC → private frame transport → same packet service and policy. Windows full VMs use an isolated Hyper-V switch plus host filtering. No default-switch escape. |

Reuse a maintained userspace packet stack for Mac/Windows parsing; do not write TCP. It receives no vault or broker key. [^S14]
For full macOS guests, VZ's file-handle network attachment feeds the same host policy path. [^S7]
Keep Linux's direct kernel packet path for the network speed target. Benchmark the actual filtered path; bypassing policy is not an optimization.
The baseline private/LAN/metadata/control-plane ranges are denied unless a named connection authorizes them.
Default egress permits public HTTP(S) and scoped package/Git endpoints; raw internet TCP/UDP needs an explicit rule.
Deny wins. Normalize IDNA/trailing dots/IPv4/IPv6; wildcard rules are label-boundary suffix matches, never substring matches.
Use host DNS; validate every resolved address and connection. Block alternate DNS, DoH/DoT and QUIC in strict domain-policy mode.
DNS names are not proof of HTTP identity. Strict mode enforces SNI, Host and certificate destination agreement through the HTTP broker.
Recheck redirects. Block domain fronting, DNS rebinding, IP-literal bypass, forbidden IP ranges and proxy CONNECT escapes.
Opaque non-HTTP protocols use explicit destination/CIDR policy; do not claim a site allowlist inspects arbitrary encrypted protocols.

Network plugins implement `Prepare`, `Dial/PacketEndpoint`, `Health` and `Close` outside the guest.
Ship direct, HTTP CONNECT/SOCKS, WireGuard and Tailscale adapters; other VPN clients can supply an isolated host-side endpoint.
Each attachment has its own state and keys. VPN/proxy failure closes egress; never fall back to direct.
Several distinct proxy/VPN exits can coexist on one host. “Own exit IP” means the selected provider's observed address, not a new address invented by Lamp.
For private membership, start a distinct host-side `tsnet.Server` for each requested sandbox identity. Keys stay on the host. [^S1]
Permit only approved private services; a tailnet identity is not blanket LAN authority. Fork gets a fresh identity, with independent enrollment.
Inbound URLs bind exactly one sandbox and port. Default private, one-hour grant, revocable, with separate watch/control authorization.
Public publication is an explicit choice. No URL token opens the guest control transport or another port.
Links between sandboxes are directional owner-authorized connection resources, checked at both ends across hosts.
No implicit link exists because two sandboxes share an owner, host, recipe or fleet.

## 8. Credentials and approvals

### API keys and CLI connections

Each connection binds owner, sandbox, exact upstream identity, operation policy, expiry and approval policy.
The guest receives a random useless sentinel. The broker identifies the sandbox from its transport, not a caller-supplied header.
Intercept only configured credential destinations. Terminate guest TLS in the host broker, validate upstream TLS, replace the sentinel and discard it upstream.
Install a public owner-specific Lamp CA in Linux/macOS/Windows guest trust stores. The CA private key stays in the owner broker.
Configure separate language trust stores, including Node, Python, Java and nested Docker builds. Never disable certificate verification. [^S18]
Block direct credential-destination traffic, QUIC and alternate proxy paths. Pinned clients use a supported connection adapter or fail explicitly.
Do not silently inject a real key to make an incompatible client work.
Support HTTP streaming, WebSocket upgrades and HTTP/2 where required; test each harness version and refresh/error path.
Do not log authorization, request secrets or secret-bearing bodies. Restrict provider host/path/method and allowed response shape.
Strip credentials on redirects. Reject reflection/debug endpoints. Scan returned bodies/errors and common encodings for known protected values.
This last scan is defense in depth; the primary boundary is that an approved upstream protocol does not return credentials.
An arbitrary malicious upstream can transform a key in unlimited ways. No generic redactor proves zero leakage in that threat model.

`gh` and Stripe use sentinels and host header substitution. Git HTTPS uses the same connection; SSH/signing use a constrained host signing service.
AWS uses fake guest credentials to satisfy the CLI, then a service-aware host adapter discards the guest signature and computes SigV4 with real credentials.
The adapter validates canonical service, region, operation, body and streaming mode before signing. Unsupported signing variants fail closed. [^S19]
Signing and git identity follow the person; private signing keys remain outside the sandbox.
An opaque raw socket cannot be advertised as read-only. Owner services use real read-only database roles or a protocol-aware adapter.
Access levels are off, read-only, ask first and full. Unknown protocols can offer off/full; adding read-only requires an actual enforcement implementation.

### Subscription logins: ADR 0004

Maintain a versioned harness catalogue of permitted methods, source URL, tested version, execution context and expiry of the review.
Preserve the official harness binary and built-in sign-in routes. No token exchange that impersonates another harness.
Claude Code uses official sign-in or `claude setup-token`; Codex uses its approved open-source/self-hosted route; Copilot uses documented device login. [^S20]
Each person's approved credential store exists only on hosts that person has designated for credential custody.
A team compute host is not automatically one of that person's approved hosts. Place elsewhere or use an API-key connection.
Place subscription tokens only in that person's sandboxes. Vendor traffic remains direct end-to-end TLS; do not apply subscription-token substitution.
Use a vendor-only egress policy for a subscription-bearing sandbox. For broader tools/network, run those tools in a separate connected sandbox.
This is the simplest enforceable interpretation of vendor-only egress: a copied token must not gain a general internet route.
Coordinate refresh ownership on the person's hosts according to the vendor route; do not invent a shared refresh service for a vendor that forbids it.
The relay transports control ciphertext. It never brokers inference or stores provider credentials.

Capture enters a sign-out barrier before any RAM or disk capture starts.
Stop credential-reading harness processes, revoke their sandbox delivery handles, unmount transient credential storage, clear guest environment/keyring/temp state, and zero freed pages.
Resume the harness from its supported session record after fresh same-owner sign-in preparation. Other task RAM remains captured.
Hooks cover suspend, fork, prepared-memory template save, crash dumps and temporary files. Failure publishes no usable state.
Deleting a credential file is insufficient: a token may already be in a heap, page cache, swap or another task's file.
Disable guest swap and core dumps for this class. Never persist host worker dumps containing guest RAM.
If untrusted code could copy a subscription token arbitrarily, neither a path blacklist nor a byte scanner proves complete stripping.
Such capture must refuse unless a verified compartment/reset scheme proves exclusion. This is a release-blocking tension in the brief, not a claimed solved feature; see §21.
API-key mode avoids that tension because no upstream token enters captured memory. The subscription exception permits runtime exposure, not capture.

### MCP, browser and password use

The host MCP gateway owns upstream OAuth grants and stdio servers. It authenticates the sandbox and exposes only selected servers/tools.
Use separate audience-bound OAuth grants upstream. Do not forward the gateway's inbound token to an unrelated MCP server. [^S21]
Stdio servers run in their own restricted process/service sandbox, never with the owner's full environment.
An MCP server that must run inside gets a short-lived Lamp capability for one connection and tool set, preferably through a local protocol adapter.
If the server requires an actual upstream limited token, show the explicit exception and scope; never treat it as an isolated API key.
No broad provider API key enters under the name “limited token.” Certification must distinguish these cases.

The logged-in browser kit defaults to a dedicated locked-down browser sandbox with a private owner profile.
The agent gets navigate/click/type-public-text/read-page/screenshot/download actions, not CDP, arbitrary JS, cookie/storage APIs or raw network logs.
The browser controller holds raw automation authority and never exposes it to the agent. Raw CDP can retrieve cookies. [^S22]
Keep session-cookie handling in a host-side browser connection where feasible: remove `Set-Cookie` from agent-visible material and send cookies only to the exact approved origin.
Block profile exports, credential pages, developer tools, arbitrary extensions, `file:` URLs and downloads of browser state.
Apply output filtering before returning page text or screenshots; secret-bearing pages remain under human control until cleared.
Approved sites remain trusted for the authentication protocol. A hostile authenticated site that renders an encoded cookie is an unresolved general information-flow case.
Report the finite attack suite's zero-leak result without claiming a theorem for arbitrary sites.
Opt-in copied-logins mode runs the browser inside. It is explicitly marked secret-exposing and never enabled by recipe inheritance alone.
Hardware/device-bound sessions may need fresh sign-in; copying a profile does not guarantee portability. [^S22]

Ship credential-source plugins for 1Password, Bitwarden, KeePassXC and Proton Pass.
Use their supported local/service interfaces in the host broker; do not depend on an unconfirmed vendor-specific Lamp partnership. [^S23]
Default `password_fill` accepts a secret reference and approved origin/form, never returns its value.
Pause agent output/input, perform the fill and submission externally, clear fields, navigate to a clean document, then return control.
Do not allow agent-supplied JavaScript, arbitrary form destinations or screenshots during entry. Approval binds the destination as well as the secret.
Per-secret injection requires a separate explicit owner grant and is marked deliberate exposure.
Each vault can require approval per use. A phone uses the same authenticated approval view; console plugins display the same request.
Approval objects contain owner, task, secret/connection reference, action digest, destination, reason, expiry and one-use nonce.
Changing the action invalidates approval. Denial, expiry or replay performs no action. Watch sharing never grants approval rights.
2FA/CAPTCHA hands exclusive browser control to the person, hides sensitive input from agent transcripts, then hands it back.
The four manager adapters must pass real fill/deny/approve tests. Secret-returning vendor CLIs are allowed only inside the trusted broker.

Settings use a chosen one-way source: harness configuration, skills/plugins/slash commands, instruction files, MCP list and git identity/signing references.
Copy a filtered revision into each sandbox. Sandbox changes create a diff proposal; only a person's approval writes selected paths back.
Source symlinks and executable settings are validated. A kit cannot turn settings synchronization into host code execution.

## 9. Sessions and consoles

The fleet API owns stable session IDs; the execution host owns each live PTY and harness process.
Session records contain owner, sandbox, parent session, harness/version, lifecycle, status reason, output cursor and current location.
Every session appears separately, including several sessions in one sandbox. Hosts are placement details under one console machine entry.
T3 Code and Herdr plugins implement a documented fleet provider: list, watch, attach, send, interrupt, resize, approval and browser takeover.
Do not fake this by mounting all hosts' home directories under one SSH account.

`lampd` terminates SSH at the host, using a shared fleet host identity protected on enrolled hosts.
Stock SSH with no command opens a session selector. `ssh fleet session se_9` attaches; `ssh fleet shell sb_4` opens a shell.
Read-only watchers get output and resize/view metadata, never stdin. Interactive input has one short renewable writer lease; takeover is explicit and audited.
Reconnect uses a durable output cursor. Slow watchers are disconnected with a replay cursor rather than blocking the harness.
SSH public keys/certificates map to the same action/resource scopes as API keys. No host shell is exposed by the fleet selector.
For graphical takeover, a plain terminal prints a short-lived authenticated browser URL; SSH remains usable from any terminal without implementing a browser inside it.
Behind the relay, stock SSH uses an assigned TCP port, for example `ssh -p 22418 <fleet-address>`.
The relay routes by that port before the SSH handshake. It cannot route raw SSH by an HTTPS SNI name and must not terminate SSH.
Additional relay IPs supply more ports. Ordinary OpenSSH flags suffice; no custom terminal or `ProxyCommand` is required.

Harness plugins map native events to working, blocked and idle, with a reason and observation time.
Working means an active turn/tool/task; blocked means approval, sign-in, human input or a dependency; idle means ready for a new turn.
Keep host-unavailable and stale-observation fields separate from these three statuses. Do not guess completion from silence.
Background task activity prevents sandbox idle even if one session is idle.
Ship a generic PTY harness adapter plus specific adapters for Claude Code, Codex, OpenCode, Cursor CLI, Copilot CLI, Amp, Goose, Gemini CLI, Pi, Devin and Droid.
The catalogue distinguishes local executables and documented remote-provider routes. A closed hosted product without such a route is a compatibility gap, not a fabricated executable.

The starter selects sub-agent placement. Shared mode starts a separate session in the same sandbox and states that files are shared.
Own mode captures the parent's selected file tree, including an explicit uncommitted/untracked patch, into a separate sandbox.
Ignored files are excluded unless selected. Non-git tasks use a temporary git repository for transfer.
Sub-agent authority is an intersection of parent authority and delegated limits. Budgets charge the parent task as well.
Shared mode has one sandbox/network/credential boundary. It cannot enforce stricter file or secret isolation between its sessions; choose own mode whenever the authority differs.
Changes return as git commits/bundles with a reviewable diff and normal conflicts. Never overwrite the parent's files implicitly.
Mid-task memory fork can create independent alternatives. Keeping a winner means continuing that session and returning selected changes through git.

Teleport moves a session, not arbitrary laptop process memory across operating systems.
Harness adapters export a continuation record, transcript/checkpoint and git/file delta at a safe turn boundary.
Prepare the destination, reauthorize connections/settings, acquire a higher session-location epoch, stop the source writer, then resume the same session ID there.
On failure before commit, resume the source. After commit, stale source input and broker handles are fenced.
Claude Code/Codex must pass laptop→sandbox→laptop continuation tests; a blank new prompt is not teleport.
Active nonportable devices or an unsupported harness transfer return a precise error. Do not advertise process migration for this path.
Native laptop execution is explicit outside-sandbox execution under the person's account, never presented as protected sandbox work.
Team visibility is owner-only by default. Shares separately grant watch or watch-and-type, expire, and close reachable streams immediately on revocation; partitioned-host closure follows the explicit lease rules in §13.

## 10. API, MCP, CLI and SDKs

Use OpenAPI 3.1 as the one resource/operation schema, with JSON Schema definitions for events and plugin messages.
The service implements one authorization and operation layer. REST, CLI and MCP call it; no interface has an independent permission model.
Generate TypeScript and Python first, then Go and Rust before first release. Streaming helpers are thin tested wrappers around generated schemas.

| Resource | Main routes under `/v1` |
| --- | --- |
| Fleet, host, identities, keys | `/fleet`, `/hosts`, `/auth/*`, `/keys`, `/shares` |
| Template, recipe, kit | `/templates`, `/templates:build`, `/sandboxes/{id}/template`, `/recipes`, `/kits` |
| Sandbox, operation | `/sandboxes`, `/{id}/exec`, `/{id}/files/*`, `/{id}/fork`, `/{id}/suspend`, `/{id}/resume`, `/operations/{id}` |
| Session and sub-agent | `/sessions`, `/{id}/messages`, `/{id}/attach`, `/{id}/sub-agents`, `/{id}/teleport` |
| Network and connection | `/network-policies`, `/connections`, `/sandboxes/{id}/attachments`, `/{id}/urls`, `/links` |
| Remote computer and browser | `/sandboxes/{id}/display`, `/{id}/input`, `/{id}/audio`; `/browser-sessions`, `/{id}/actions`, `/{id}/takeover`, `/password-fill` |
| Lasting storage | `/caches`, `/volumes`, `/artifacts`, resumable chunk upload/download |
| Operations | `/jobs`, `/triggers`, `/schedules`, `/approvals`, `/budgets`, `/audit`, `/notifications`, `/plugins`, `/events` |

```sh
lamp template build tools.lamp --json
lamp sandbox create --recipe python-dev --idempotency-key task-274 --json
lamp exec sb_4 --json -- python -c 'print(42)'
lamp sandbox fork sb_4 --idempotency-key attempt-b --json
lamp session watch se_9 --json
lamp session teleport se_9 --to sandbox --recipe python-dev --json
lamp connection set local-db --access ask-first --json
```

```http
POST /v1/sandboxes
Authorization: Bearer <scoped-key>
Idempotency-Key: task-274

{"recipe":"python-dev","command":["python","-c","print(42)"]}
```

```json
{"tool":"sandbox_fork","arguments":{"sandbox_id":"sb_4","idempotency_key":"attempt-b"}}
```

The CLI exposes every operation, including administrative ones, with `--json`; streaming mode emits NDJSON and no progress prose on stdout.
MCP exposes the same operation inventory with typed inputs/results, plus authenticated resource subscriptions or cursor-based event reads.
No MCP tool returns a host credential. Docs include complete plain-text operation references and `llms.txt`.
Create/exec/fork accept synchronous wait or return `202` with an operation ID and observable phases.
An operation is ready only after a real guest command marker; agent-ready is a separate phase requiring the requested harness and connections.
Errors carry stable code, retryability, operation ID and safe detail. Unsupported guest/device/auth combinations are explicit.

Idempotency scope is fleet + principal + operation kind + key; retain results/tombstones for at least 30 days, longer for living resources.
Same key and request hash returns the same operation; a different body returns `409`.
Commit placement and operation identity before starting execution. On a lost response, inspect/retry that operation instead of allocating again.
Exec/input messages have operation/message IDs too. Persist acceptance before delivery; after an uncertain crash, return `outcome_unknown`, never blindly rerun a side effect.
Exactly-once arbitrary external effects are impossible without cooperation from that external service; do not make that promise.
Events use SSE with per-host sequence numbers plus a fleet cursor vector. They are ordered per resource, delivered at least once and deduplicated by ID.
Resource revisions and session-location epochs order cross-host changes; discard stale-epoch input/events rather than infer order from wall clocks.
Retain event metadata seven days by default. Expired cursors return a resync instruction; output/artifact retention is separate.
WebSocket multiplexing carries PTY/browser binary data with bounded frames and flow control. Recheck shares and expiry on active streams.

## 11. Plugins

Use length-bounded JSON-RPC 2.0 over inherited stdin/stdout pipes; binary transfers use host-created stream handles.
Do not dynamically load code into `lampd`. A plugin crash, hang or malformed message terminates only that plugin.
Manifest: type, executable/digest, protocol major/minor, minimum Lamp version, permitted operations, resource ceilings and settings schema.
Handshake negotiates features. Major mismatch refuses start; minor changes are additive. Pin versions per recipe/job and support N/N−1 during updates.
Calls carry deadline, cancellation, owner, request ID and nontransferable capability references. The host verifies every resulting operation.
No plugin chooses a host filesystem path or receives a fleet-admin bearer key.

Ordinary third-party plugins run as WASI modules in a separate Rust runner, with no preopened sandbox directory, raw socket or environment secrets.
Their useful effects go through typed host operations. A module cannot open another sandbox merely because it knows an ID. [^S13]
Native engine/access/credential adapters need OS APIs or vendor libraries; mark these as trusted boundary components with narrowly granted handles.
An engine necessarily accesses its assigned guest RAM. “Plugins cannot reach into sandboxes” must mean no authority beyond the explicitly assigned function.
Process separation alone does not enforce that rule. Separate worker identities/ACLs and closed handles are mandatory for native adapters.
A malicious privileged engine is not contained from its own guest; installing it is an explicit trust decision. It must still be isolated from neighbors and the broker.
Ship a conformance runner with crash, timeout, flood, path traversal and unauthorized-access fixtures.

| Plugin type | Narrow contract |
| --- | --- |
| Engine | probe, validate, prepare, create, exec transport, capture, fork, resume, destroy; report boundary/device/ABI support |
| Network | host-side prepare, endpoint/dial, health, close; no guest keys |
| Credential/password manager | resolve a scoped reference for an approved use; fill/sign/proxy through the broker |
| Harness | install plan, permitted auth catalogue, launch, native events, continuation and capture hooks |
| Kit | versioned software/state plan applied inside a build or assigned sandbox |
| CI | demand, register once, start, cancel, result, outputs, deregister, reconcile orphans |
| Trigger | verify source identity/signature, map to scoped caller/recipe, deduplicate, report result |
| Console | one fleet machine, sessions, events, watch/input, approvals and human takeover |
| Caller access | authenticated listener or opaque transport; never bypass common authorization |

## 12. Fleet coordination

Enrollment uses a one-use invitation bound to a host public key. The fleet signs a host certificate and records owner-approved role/labels.
Roles are compute, storage and control; a host may hold several. Removal revokes new transport access and assigned authority.
Store a small embedded Raft log on control hosts; apply it transactionally to local SQLite. No external coordinator process or database. [^S12]
Replicate policy, membership, operation/idempotency records, reservations, session directory, approvals and schedule claims.
Do not replicate model credentials or bulk output into this log. Owner credential stores have separate explicit host-to-host transfer policy.
Every host can serve the API and route live traffic. The elected coordinator commits placement and global state changes.
An access plugin owns the stable fleet endpoint. Relay SNI/SSH-port routing chooses a healthy enrolled ingress; Tailscale uses `tsnet.ListenService` with redundant service hosts and one virtual address. [^S1]
Owner-domain access uses one hostname with approved ingress addresses and health-driven DNS updates. Local-only uses a stable socket; it cannot survive loss of its only reachable host.
Consoles retain one fleet identity/address and reconnect with their output cursor. Established TCP connections do not migrate; enrollment records provide alternate ingress endpoints for safe retries.
Hosts advertise CPU/RAM/disk reservations, guest OS, engine certificates, devices and cached template revisions.
Filter by authority, boundary, OS/CPU, devices, storage/credential locality and pin; then prefer the fastest certified engine and least costly available placement.
Engine rankings use command-ready latency, then fork, then resource overhead among engines passing security and throughput gates.
Never trade security or a requested device for a better timing number. Refresh measurements after OS/engine changes.
Reserve before create. If no capacity exists, return a reason or join an explicitly requested bounded queue. No automatic cloud overflow.

Use one voter for a fleet of one; normally use three stable control hosts for automatic failure tolerance.
Two compute hosts need a third control-only host for safe automatic write failover after either compute host disappears.
A witness on the same physical Mac does not survive that Mac's loss. The relay must not become a hidden fleet database.
With quorum, election and routing move automatically; proposed recovery objective is under five seconds on a LAN.
Without quorum, keep live sessions reachable and enforce already committed scopes/leases, but refuse new global allocations and uncertain retries.
Raft cannot provide safe automatic writes in both halves of an isolated two-host fleet. This is an explicit availability limitation requiring resolution against the literal brief. [^S12]
Graceful sleep drains the coordinator role first. Unexpected sleep never implies its sandboxes migrated or completed.
Returning hosts reconcile committed generations and expire stale reservations. A sleeping execution host's RAM remains on that host.

Offline fleets use local accounts or a reachable local OIDC issuer, cached trusted keys and locally available templates/kits.
WAN, Clerk and relay outages do not disable local work. Expired external credentials still expire; offline mode does not extend authority.
Opt-in updates acquire one fleet-wide update lease, drain one host, retain active workers on their pinned engine version, update the daemon, probe, then continue.
Pause rollout on failure. Keep old binaries/state readers for rollback. Never rewrite active captures to a new ABI in place.
N/N−1 protocol support permits mixed versions. Incompatible saved state stays on its original engine until drained or deliberately converted.
One-host updates have a visible control interruption; no pretend failover to another process on the same failed machine.

## 13. Access and identity

Embed `tsnet` in the shipped Go access plugin process; no separately installed `tailscaled` is required.
Use `WhoIs` and approved network grants as identity input, then intersect with Lamp scopes. Network membership is not create authority. [^S1]
Support its configured control URL for an owner's independent control service; do not make Tailscale SaaS a prerequisite for local operation.
CI can join ephemerally through workload identity federation, then exchange its job identity with Lamp. No saved Tailscale auth key or Lamp key is required. [^S1]

The Eilmer relay has a TLS control endpoint for Eilmer account sign-in, host linkage and one-time rendezvous grants.
Hosts keep outbound authenticated connections to it. A data connection is a bounded `OPEN/ACCEPT/DATA/WINDOW/CLOSE` stream over that connection.
The relay selects an enrolled fleet/host, then copies opaque caller-to-host TLS or SSH bytes with backpressure and short buffering.
TLS record decryption occurs only at the host. Outer relay transport TLS is separate and contains only inner ciphertext and routing metadata.
Use standard TLS 1.3/SSH cryptography. No homegrown encryption, inference endpoint, recording or disk queue in the relay.
One-time grants expire after 60 seconds, bind the caller public key/fleet/nonce, and are consumed atomically by the relay control service.
Host authorization still checks the encrypted request. Replaying a grant or substituting a host must not create authority.
Native callers pin the fleet identity during trusted enrollment. SSH pins the fleet host key/certificate authority through stock OpenSSH.
Web access uses the host certificate. An active compromise of domain/CA issuance or delivered browser code is a separate trust boundary; passive byte forwarding alone does not solve it.
Require out-of-band host fingerprint verification for high-assurance first enrollment; test an actively malicious forwarding relay.
Relay logs hold account/host linkage and connection metadata only, with short retention. They contain no requests, files, transcripts or model credentials.
Self-run relay is the same AGPL-3.0 service. All relay use requires an Eilmer account on Clerk, per ADR 0005; offline fleets choose local, owner-domain or private-network access instead.
Native callers can send the grant in an outer relay stream before inner TLS. Stock browsers use SNI pass-through to the host, which consumes a signed sign-in grant inside TLS and establishes its own session.
Stock SSH uses the assigned raw port and enrolled account-bound SSH identity. Neither browser nor SSH requires a custom wire protocol at the caller; their host-side authorization remains mandatory.

Owner-domain access uses the Go ACME library with HTTP-01/TLS-ALPN or DNS-01 and persistent certificate state. [^S16]
It requires real reachability or an explicitly selected access plugin; a certificate does not solve CGNAT.
Local-only binds loopback/socket by default. LAN access requires an explicit listener and authentication.
Cloudflare Tunnel is a separate access plugin with its own trust statement; do not give it the relay's no-decryption claim.

Use one Eilmer account identity from the shared Clerk tenant. Fleet policy maps issuer+subject to local principals, not email alone.
Non-relay fleets support local passkeys/public keys and local password accounts with Argon2id, plus OIDC authorization code + PKCE. [^S24]
Local account and locally hosted OIDC flows work without Clerk. A remote OIDC provider cannot authenticate a new caller during its outage.
Fleet modes are single-owner, team and public service. Public admission still creates an isolated principal with owner-set quotas; it does not publish private sessions.
Keys are random, shown once, hashed at rest, revocable, and constrained by actions, sandbox/recipe/host selectors, CPU/RAM/disk/concurrency budgets and expiry.
Delegated keys only narrow authority. Watch, read files, run commands, type, approve, manage connections and administer are separate actions.
Check effective authority at admission and execution, and terminate streams/delegations on revocation or expiry.
A revocation operation completes only after affected hosts acknowledge closure or their short authorization leases expire; partitioned-host closure remains visibly pending. Proposed share/input leases last 10 seconds.

GitHub OIDC exchange verifies signature/JWKS, exact issuer, exact requested audience, expiry/not-before and one-use exchange state. [^S25]
Lamp first issues a challenge. The job requests GitHub's token with audience `lamp:<fleet-id>:<challenge>`; consume that challenge and token fingerprint atomically. Do not assume GitHub supplies a caller-chosen nonce claim.
Trust policy pins immutable repository/owner IDs, workflow identity/ref, environment, event and branch/protection constraints; do not trust a repository name alone.
Issue a five-minute Lamp key limited to the bound job/recipe/hosts/resources. Never make `id-token: write` itself a trust decision.
Fork PRs without OIDC use the trusted CI intake controller, with untrusted-job policy fixed independently of job input.

## 14. eilmer.si origin and certificate isolation

Use `www.eilmer.si`, `docs.eilmer.si`, `account.eilmer.si` and `relay.eilmer.si` for hosted product services.
For fleet `f123`, use `api.f123.fleet.eilmer.si` and `web-<random-port-grant>.f123.fleet.eilmer.si`.
Separate random origins for each sandbox/port/grant generation. Never serve sandbox code beneath an account/dashboard path.
The dashboard for sandbox administration is served by the host at the fleet API origin, not stored in Eilmer's infrastructure.
Untrusted responses cannot override routing headers or obtain a privileged origin.

All Lamp authentication cookies use `__Host-`, Secure, HttpOnly, Path=/ and no Domain. Fleet dashboards use a server-side session, never a Clerk browser SDK.
Clerk's own `__session` cookie is deliberately JavaScript-readable; do not claim it has those Lamp cookie properties. Configure the shared instance at `account.eilmer.si` using Clerk's **Secondary application** domain mode, with FAPI at `clerk.account.eilmer.si`. This scopes its infrastructure to that trusted subtree without adding another account system. [^S26]
Enable Clerk's exact subdomain allowlist; never allow fleet/sandbox origins. Reject any vendor deployment that emits `Domain=eilmer.si`, and do not use satellite authentication to distribute a browser credential across Eilmer products.
Use Clerk's OAuth/OIDC identity-provider flow with exact registered callbacks for other Eilmer products. Each product issues its own host-only session for the same issuer/subject; accounts remain shared without shared cookies. [^S26]
SameSite is insufficient: sibling `eilmer.si` origins can be same-site. Check exact Origin for mutations/WebSocket upgrades; OAuth callbacks instead require exact registered URI, state, PKCE and nonce, since redirects may omit Origin.
Use CSRF nonces, no state-changing GETs, no wildcard credentialed CORS, and no suffix-based origin trust.
Validate Host/SNI and explicit redirect targets; disable `document.domain` relaxation with origin-keyed isolation.
Use CSP `frame-ancestors 'none'` for account/control pages, `noopener` for sandbox links and explicit-origin `postMessage` checks.
The host strips parent-domain `Set-Cookie` from sandbox responses. Host-only sandbox cookies may remain for the application.
Reject ambiguous duplicate auth cookies; ignore all unrecognized parent-domain cookies, so sibling cookie tossing grants no account authority.
Do not serve other Eilmer products with domain-wide auth cookies. This is an umbrella-wide deployment requirement of ADR 0006.
Test Chromium, Firefox and WebKit with hostile sibling pages, service workers, navigation, framing and cookie flooding.

Issue one wildcard certificate `*.f123.fleet.eilmer.si` per fleet, not per sandbox or host.
Its private key is generated on the fleet and replicated only to that fleet's approved ingress hosts. Never share one global wildcard key across installations.
An Eilmer DNS challenge service authorizes only the fleet's ACME TXT record and CSR names. It receives no private key.
The relay forwards SNI-selected TLS without having this certificate key. Ingress failover reuses the fleet certificate on another approved host.
Hosted account/docs/relay have separate keys/certificates from all fleet keys.
Maintain a global issuance ledger below 50 new certificates per seven days under the default Let's Encrypt limit; persist accounts and use ARI renewals. [^S27]
More than 50 sandbox URLs in a week consume no extra certificates. More than 50 new fleets still need a granted CA quota increase or a contracted higher-quota CA.
Obtain that capacity before unrestricted hosted onboarding; queue issuance transparently while exhausted. A wildcard does not remove the per-fleet scaling limit.
No alternate registrable domain or shared cross-fleet certificate key is part of this design.

## 15. CI and triggers

Every shipped CI plugin calls one job state machine: demand → reserve → fresh sandbox → register → ready → run → collect → revoke → destroy → deregister.
Cancellation and timeout take the same cleanup path. Reconcile abandoned registrations and sandboxes after either controller restarts.
Provisioning keys stay in the trusted controller. Deliver only a job-bound runner identity; separate its control channel from job commands where the vendor permits it.
Keep vendor cache/artifact protocols intact. Lamp supplies lasting storage underneath; Docker/service-container jobs get the Docker kit inside their sandbox.

| CI family | First-release plugin contract |
| --- | --- |
| GitHub Actions | Go `actions/scaleset` controller, outbound demand polling and one-use JIT runners. Use `TotalAssignedJobs` for capacity, not message counts. GitHub App provisioning authority stays outside. [^S2] |
| Copilot cloud agent; Claude Code and Codex actions | Select Lamp's scale set through the documented Actions-based route. Run official actions with their declared auth. This does not promise a backend for every hosted GitHub agent mode. [^S28] |
| GitLab | Fleeting provider returns sandbox instance IDs and connections; `capacity_per_instance=1`, `max_use_count=1`. Custom executor is a compatibility fallback. [^S29] |
| Buildkite | Queue/tag-aware controller starts an agent with `--disconnect-after-job`, then destroys its sandbox. [^S30] |
| Jenkins | Small `Cloud`/retention plugin provisions one inbound agent, one executor and one sandbox; deletes the agent record on completion. [^S31] |
| Forgejo | Server-enforced ephemeral registration plus `forgejo-runner one-job`. Pin server/runner compatibility. [^S32] |
| Gitea | Its distinct runner adapter registers ephemeral execution and tears down the sandbox afterward. Forgejo registration is not interchangeable. [^S32] |
| CircleCI | Resource-class demand controller and machine runner `single-task`; disable service restart after completion. [^S33] |
| Azure Pipelines | Pool/demand-aware controller starts a supported agent with `--once`, then deregisters and deletes it. [^S34] |
| Bitbucket | Label-specific registered warm capacity; completion-driven deletion/deregistration. Prove this lifecycle; no universal one-job flag is assumed. [^S35] |
| Woodpecker | Label-aware agent with shutdown after one workflow and concurrency one; external sandbox teardown. [^S36] |

A stranger public-repo PR receives an immutable untrusted intake class, computed by the trusted controller from verified forge events.
It always gets a fresh real VM, including on hosts with weaker-engine overrides. Never reuse a completed job's writable state.
No owner/model/vault/browser/private-service credentials or trusted caches. Deny LAN, metadata and fleet control; allow required public dependencies and job transport only.
Necessary one-job runner/read-only checkout credentials are short-lived job authority, never reusable owner authority; document each vendor's residual exposure.
Untrusted workflow input cannot choose a privileged recipe, credential-bearing host or trust class. A privileged `pull_request_target` context must never execute the untrusted head.
Separate trusted intake from untrusted execution; collect artifacts as hostile files. Cancellation revokes authority even if the guest ignores termination.

Ship Slack, Linear, signed webhooks and schedules as trigger plugins. Verify signatures/timestamps, deduplicate event IDs, map external subjects to scoped callers, and bind an approved recipe.
Replies/notifications go only to the authorized initiating destination. Message text never grants credentials or changes its trust class.
Schedules store an IANA timezone and UTC next occurrence. Default: run once at ambiguous DST time, skip nonexistent time, skip missed runs, forbid overlap; allow bounded catch-up/overlap by policy.
Commit schedule claims before launch. Retry and leadership changes retain the occurrence's original idempotency key.

## 16. Observability and limits

The host-served dashboard lists every authorized sandbox, session, task, host, storage object, plugin, grant, budget and operation, including stale/offline state.
Stream changes within a proposed one second on a LAN. Show working/blocked/idle, placement, reservations, real usage, warm-pool costs and suspended-state disk costs.
Dashboard and console plugins use the same API and scopes.

Audit records bind fleet/host/sandbox/session/principal, operation, policy revision, decision, sequence and time. Hash-chain and sign batches; copy them to another owner-selected host.
Log every Lamp command before dispatch. The supported Linux kernel emits process-exec records with nested-Docker attribution through a built-in audit path; no unloadable guest agent is the sole collector.
Disable kernel replacement, module loading, raw kernel memory and audit reconfiguration. Host sequence/heartbeat checks fail closed on gaps; dropping or flooding audit cannot silently grant unaudited execution.
Host egress records every connection, DNS decision and broker-visible HTTP request. Full URL/method is available only where decrypted by an authorized broker; opaque TLS yields destination/connection metadata.
A guest agent does not prove complete audit under hostile guest root. Test collector tampering, direct syscalls, interpreters and nested runtimes; native macOS/Windows need protected OS collectors too.
If the brief means every shell builtin, instruction or plaintext request inside arbitrary encryption, ordinary VM telemetry cannot prove that. Resolve audit granularity before claiming compliance (§21).
Keep bodies/secrets out of audit. Default metadata retention is 30 days, configurable by owner quota; export JSON. Collector loss stops new task/network activity in mandatory-audit mode.

Time budgets use host monotonic deadlines, charge blocked time according to policy, and include descendants. Forks/sub-agents share the parent task ledger unless the owner raises it.
Token budgets reserve input plus maximum output before proxied requests; reconcile provider usage afterward, including streaming, retries and cached/reasoning token categories.
Subscription/direct and outside-agent usage requires a trusted harness/provider accounting hook. Reject a requested strict cap if the route cannot enforce it; show best-effort counters separately.
A watchdog detects repeated tool/action digests, repeated errors and no observable progress. Proposed defaults: 10 identical cycles or 10 minutes without progress; suspend for a decision, terminate at the time budget.
Stopping closes input, revokes broker handles, cancels remote tools and kills all descendant tasks. Verify actual stopped state; a console status change is insufficient.
Notify done, stuck and needs-decision through consoles, authenticated phone web push and optional signed webhooks. Send references/safe summaries, never secrets.
Optional usage telemetry is off by default fleet-wide. Opt-in sends coarse counts/timings/versions, never prompts, filenames, URLs, output or credentials; opt-out stops queued delivery.
Relay/account security metadata is separately disclosed. No payments in release one; local usage/admission ledgers leave a future plan/billing boundary.

## 17. Security model

Assume malicious repository code, guest root, prompt injection, hostile MCP/page content, hostile neighbors and ordinary third-party plugins.
Test malicious callers, network peers and a malicious forwarding relay. A trusted host administrator, compromised hypervisor, broker, vault or approved authentication upstream can defeat its own boundary.
Do not promise protection from physical host compromise or arbitrary hardware side channels. Patch/minimize trusted components and publish the threat model with results.

| Boundary | Enforcement and failure behavior |
| --- | --- |
| Guest → host/neighbor | Hardware VM plus restricted engine process; no host mounts, inherited sockets, keys or broad helper API. Device/backend fuzzing and host ACLs are separate layers. |
| Guest → network | Host-owned sole egress; identity from transport; deny private ranges; no automatic socket translation or direct fallback. |
| Agent → API/CLI credentials | Broker substitution/signing; real credentials never enter guest RAM, disks, events or transcripts. CA private key remains outside. |
| Agent → password/browser cookie | External narrow controller; exclusive broker/human entry; no raw browser protocol or profile export. |
| Caller → other owner | Common per-resource authorization on every interface, listing and stream; unauthorized IDs return the same not-found response. |
| Plugin → Lamp | WASI process with no ambient authority; native adapters explicitly trusted and isolated from unrelated state. |
| Relay → fleet data | Authenticated inner TLS/SSH, pinned identities, no fleet decryption key at relay. Domain/bootstrap/browser-code trust remains separate. |
| Persisted state | Owner-scoped files, host full-disk encryption, encrypted exported captures/artifacts and wrapped per-owner keys. No secrets in replicated metadata. |

The 0-leak target is a release gate over the published attack suite on every supported host/boundary.
Plant distinct API-key, password and cookie canaries; attempt extraction through files, RAM, procfs, output, logs, browser actions, redirects, MCP, DNS and controlled network sinks.
Pass requires zero protected canary values or tested encodings in agent-visible output, unauthorized destinations and saved/exported state. One leak blocks release.
Subscription tokens are the ADR 0004 runtime exception, but must still be absent from every capture and forbidden destination.
Explicit password injection and copied browser logins intentionally reveal protected values. Test consent, but do not count those tests as zero-leak passes; the brief needs an explicit classification for these opt-ins.
Arbitrary malicious authenticated upstreams can encode secrets in useful-looking responses. Restrict trusted protocol/origin surfaces and expand adversarial tests; redaction cannot prove universal noninterference.
Keep remaining conflicts (§21) as release blockers. Never quietly remove a feature, weaken a target or call refusal of a supported workflow a successful implementation.

## 18. Performance budget

These are planning budgets. No Lamp benchmark has run. Feature APIs and published boot claims do not prove these totals.
Proposed baseline: local caller, cached template, 2 vCPU, 2 GiB RAM, 8 GiB private root, no optional device, idle host, enforcing network/audit, no subscription sign-out barrier.
Measure API receipt → actual command-start acknowledgment, including auth, placement, policy and identity. Also report caller-observed latency; do not hide WAN transit as engine time.

| Linux phase, milliseconds | Create checkpoint restore / pristine warm hit | Live fork, 2 GiB fully dirty |
| --- | ---: | ---: |
| Authenticate, authorize, commit reservation | 20 / 20 | 20 |
| Capture hooks, freeze and quiesce | 0 / 0 | 60 |
| RAM + CPU/device capture | 0 / 0 | 230 |
| Disk COW generation and metadata | 20 / 10 | 25 |
| New worker, hypervisor and memory mapping | 120 / 10 | 125 |
| Host network/policy and connections | 35 / 35 | 40 |
| Entropy, clocks, identities, hooks, ACK | 80 / 60 | 100 |
| Run command and observe marker | 35 / 35 | 40 |
| Contingency | 100 / 100 | 200 |
| **Budget total** | **410 / 270** | **840** |

RAM allowance needs roughly 8.7 GiB/s for 2 GiB, before fixed capture costs; measure copy bandwidth under memory pressure first.
Private restore mapping avoids eager duplication but not the capture copy. Firecracker documents ≤125 ms to guest init and >95% compute, different/weaker observations than Lamp's gates. [^S4][^S5]
At 16 GiB, that copy rate alone exceeds one second. Test larger sizes and incremental pre-copy; disclose the supported fast-fork envelope, never imply size-independent latency.
The brief does not define an envelope. If <1 s means all RAM sizes/devices/loads plus durable storage, this design cannot certify it; resolve this before release.
Create gate: p50 <500 ms on both Linux architectures, reporting warm-hit/checkpoint-miss distributions separately. Cold import/build/download and pool refill are separately costed.
Fork gate: every trial <1 s in the standard suite; report p50/p95/p99/max and failures. Do not silently reinterpret the unqualified target as p50.
Durable fork, subscription sanitation, devices, cold caches and loaded hosts remain visible; pooled success cannot hide their latency or unsupported status.
Throughput gate: computation, disk and network each ≥0.97 of pinned native baselines, with confidence intervals, equivalent resources/durability, and default policy/audit costs enabled.
Use compilation/compression/CPU kernels; sequential/random read/write plus fsync; TCP/UDP throughput, request rate and latency. Report each, including first-write COW penalties, rather than one favorable average.

| Other configurations | Initial hypothesis, not a target or published result |
| --- | --- |
| Linux guest on M5/HVF | Create 200–800 ms; 2 GiB live fork 0.5–2 s; cold boot 0.5–3 s. |
| Linux guest on Windows/WHP | Create 300–1,200 ms; 2 GiB live fork 0.7–3 s; cold boot 1–5 s. |
| Native macOS/Windows full VM | Prepared create/resume 1–10 s; RAM fork 3–30 s; cold boot tens of seconds. Native macOS fork feasibility is unproven. |

Replace these ranges with measurements before setting Mac/Windows targets. Report native-guest and Linux-guest numbers separately.

## 19. Verification

Unit/property tests cover lifecycle, policy normalization, authorization, deduplication, placement, budgets, cursors, fencing and capture reference counting.
Fuzz manifests, guest/device messages, block headers, JSON-RPC, HTTP/SSH routing and hostile archives. Fake clocks, JWKS, ACME, CI vendors and credential endpoints make failure tests deterministic.
Integration tests use real kernels/hypervisors and crashes at every capture/commit/cleanup boundary. Verify actual RAM contents, dirty disks, parent continuation and fresh identities.
E2E covers inside/outside agents, all interfaces, T3 Code/Herdr/SSH, browser takeover, four vaults, CLI/MCP credentials, teleport, storage and every shipped CI/trigger plugin.
Use public fixture repositories, official harnesses, GitHub MCP and filesystem MCP fixtures. Never use the owner's projects or special owner-product adapters.

| Lane | Machines and cadence |
| --- | --- |
| Fast every change | Go/Rust unit, fuzz corpus, schemas/SDK parity and cross-compilation for every shipped OS/architecture. Compilation is not E2E evidence. |
| Linux x86_64, real KVM | Ubuntu laptop: inventory CPU/RAM/kernel/filesystem first; every-change engine/E2E/throughput/security on controlled fixtures. |
| Linux arm64, real KVM | Rent an actual arm64 Linux KVM-capable host or dedicated machine; probe access before selection. An arm64 guest on a Mac is not this lane. |
| macOS | M5, 10 cores/24 GB/macOS 26: every-change HVF, native macOS and security. MacBook Air: inventory model/OS/RAM; test sleep, low memory, mixed fleet and laptop teleport. |
| Windows nested | Disposable licensed Windows Server 2025 on Azure Dsv5 with Standard security type; probe nesting/WHP, then run Linux and Windows guest E2E per change. Azure documents this path. [^S37] |
| Windows physical | Rent persistent Windows 11 Pro/Enterprise bare metal with remote console/recovery. Require per-change devices/security/performance before desktop certification; nested Server results alone are insufficient. |
| Fleet | Three control hosts, plus one/two-host cases: sleep, partitions, disk-full, broker failure, loss of relay/Clerk/internet, revocation and interrupted update. |

No infrastructure purchase is made by this proposal. Provision missing Windows/Linux arm64 lanes before the release gate; until then their status is unverified, never green.
On every candidate source change, run the reproducible comparison suite against Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM.
Pin releases/commits, kernels, hardware, mitigations, resources, workload and policy; randomize order. Retain raw samples, scripts, costs, failures and unavailable combinations.
Measure create, RAM fork, each throughput class, time-to-agent-ready, idle/loaded density, reliability and the same applicable attack suite. Never report disk-only fork as RAM fork.
Use at least 1,000 lifecycle trials and a 24-hour parallel-task soak for release candidates. Proposed reliability gate: ≥99.9% successful standard operations, zero orphaned authority or cross-task state.
Each change gets its own full result; queue scarce physical-host runs rather than reuse an old green badge. Vendor failures are reported, never replaced by invented measurements.
Keep source/results private until the first engine demo works. Then publish suite/fixtures/raw reports; competitors' Docker paths do not become Lamp's template path.

## 20. Build plan

Each milestone is a bounded vertical slice. All features below belong to release one; order does not defer scope.

| Milestone | Deliverable and exit condition |
| --- | --- |
| 0: engine experiment only | Ubuntu native-disk template, command-ready create, real RAM fork, parent continuation and CPU/disk/network proof. Meet Linux speed gates before product services. Keep repo private until this demo works. |
| 1: portable engine | Linux guest on HVF/WHP and Linux arm64; devices, controls, fallback, full macOS/Windows adapters, capture/restore/identity. Resolve native-fork and credential-capture blockers early. |
| 2: local product | API/CLI/MCP, own template builder/recipes/kits, warm pool/lifecycle, storage, events, four generated SDKs and dashboard. Clean-machine install/uninstall/recovery. |
| 3: trust boundaries | Host network/exits/private membership, API-key/CLI broker, vendor catalogue, MCP, four vaults, browser, approvals, audit, budgets/watchdog; zero-leak suites. |
| 4: sessions and fleet | Generic/all named harnesses, T3 Code/Herdr/SSH, shares, sub-agents/git return, teleport, failover, offline auth, updates and notifications. |
| 5: access and automation | Tailscale, encrypted relay/Clerk, local/OIDC/owner HTTPS, eilmer.si isolation/cert capacity, GitHub exchange, all CI/triggers/schedules and public-PR safety. |
| 6: release gate | Complete requirements matrix, every-change competitor proof, Linux targets, all-platform security, licensing/SBOM, hardware lanes, install/upgrade/recovery docs and no unresolved binding-scope blocker. |

Keep one Go workspace and one Cargo workspace. Ship signed native packages with pinned engine/guest/plugin manifests; prebuild official templates without installing a host build toolchain.
Use captured protocol fixtures for adapter development, then real vendor conformance tests. Avoid a marketplace, custom filesystem, bespoke VPN stack or a VMM abstraction larger than the required engine contract.

## 21. Risks and first experiments

Run these in order of ability to invalidate the design. A fail changes implementation or blocks release; it does not change the brief automatically.

| Experiment | Pass threshold | Fail consequence |
| --- | --- | --- |
| Shared core vs Firecracker | Create p50 <500 ms; every standard 2 GiB RAM fork <1 s; each throughput class ≥97%, including policy/audit | Select fastest passing core; if none pass, stop product expansion and investigate. |
| Live-memory torture | 1,000 dirty/in-flight-I/O forks; state oracle correct, both continue, no cross-generation writes | Fix capture/backend or reject core. No disk-only substitute. |
| Filesystem paths | Reflink and portable chain meet disk gate with fsync/first writes | Change backend; require qualifying filesystem only if practical and disclosed. |
| Credential capture | Hidden token in heap/file/page cache/temp/dump: zero retained, or refuse before publishing | Find verified compartment/reset scheme. Refusal is safe but does not deliver general fork+subscription support. |
| Native full-VM fork | RAM preserved, parent continues, independent identity, licensing valid | VZ restore requires original machine identity. Seek supported solution; absent one, native macOS fork remains a release blocker. [^S7] |
| Network bypass | Root/nested Docker/raw sockets cannot reach forbidden sink via TSI/DNS/IPv6/QUIC/failed VPN | Remove bypass/engine feature; no release with unenforced path. |
| Secret/browser attacks | Zero canaries in reflection, DOM/CDP/export, fill races and capture suite | Narrow protocol/controller surface; resolve deliberate-exposure vs zero-leak classification. |
| Clerk/origins/ACME | Three browsers: zero stolen/replaced authority; hostile FAPI origins denied; >50 dev URLs reuse valid fleet cert | Block hosted launch until isolation and issuance capacity pass. |
| Fleet partitions | Quorum recovery <5 s; no double placement/input/approval; surviving execution sessions reachable | Three voters give safe failover. Literal automatic writes in both halves of two-host partition need scope resolution. |
| OS hardware | Real KVM arm64/HVF/WHP and physical Windows boot/devices/capture/security pass | Provision lanes before release; no certification by cross-compilation. |
| Audit/token caps | Hostile guest cannot suppress required audit; strict task cannot exceed token reservation | Add collectors/accounting; clarify inaccessible events instead of claiming false completeness. |
| Plugin/teleport/crashes | No neighbor access/daemon failure; at most one writable session location; no leaked handles | Fix capabilities/fencing before more adapters. |

Open interpretations are not feature deletions: native macOS RAM fork, arbitrary subscription-token stripping, universal fork size/latency, two-host partition availability, audit granularity, opaque token caps and intentional exposure.
The simplest design cannot prove these stronger interpretations from available APIs. Keep them explicit for the architecture merge and resolve them before claiming the complete brief is met.

## 22. Deliberate differences

These compare documented approaches, not measured superiority. Exact versions belong in the benchmark manifest.

| Reference | Lamp's deliberate choice |
| --- | --- |
| Cloudroom | Its thread-oriented product is a useful reference; its security page allows agents to read keys kept inside a sandbox. Lamp is a generic fleet/session service and keeps API keys outside. ADR 0004 also rejects the centrally stored subscription approach considered during research. [^S38] |
| E2B Embed | Keep Linux microVMs as a comparator. Choose a portable core, local Go control program and native templates without Docker or a required Linux-only deployment stack. [^S39] |
| Docker Sandboxes | Keep the useful host credential-proxy idea. Proxy API keys rather than subscription inference; Docker is an optional in-sandbox kit, not Lamp's template/runtime foundation. [^S18][^S40] |
| microsandbox | Reuse downstream low-level capture work rather than invent RAM fork. Do not adopt its whole daemon, OCI path or API; Lamp supplies fleet/session/credential/CI contracts. [^S4][^S41] |
| SmolVM | Keep a small Rust engine. Require a continuing parent, cross-host-OS qualification and security proof; never infer Linux fork semantics from Windows backend support. [^S42] |

## 23. Requirement coverage

Checked against the parallel [requirements draft](1-requirements.md), without editing it. Coverage means an architectural path and gate, not a claim that unresolved gates pass.

| Brief sections / requirement families | Design sections |
| --- | --- |
| 1–4: PRD-001–003, INT-001–007 | 1–3, 8–10, 19–20; outside callers use exec/files/browser without an inside harness |
| 5: HST-001–006 | 2–4, 9, 12–13, 19, 21 |
| 6–7: SBX-001–007, BEN-001–005 | 4–5, 17–19, 21 |
| 8–10: TPL-001–003, LIF-001–006, STO-001–004 | 5–6, 9, 15 |
| 11–12: NET-001–006, PLG-001–002 | 7, 11, 17 |
| 13–15: CRED-001–006, CON-001–003, KIT-001–005 | 5, 8–9, 17, 21 |
| 16: SES-001–007 | 9–10, 12–13 |
| 17: ACC-001–007, RLY-001–002 | 10, 12–14, 17 |
| 18: CI-001–006 | 13, 15, 19 |
| 19–20: OPS-001–008 | 3, 5, 9, 12, 14–17, 19–20 |
| 21–22: ENG-001–003, QA-001–002 | 3–4, 18–21; all competitors, exact owner hardware and missing Windows/arm64 lanes |
| 23: research; ADRs 0001–0009 | Six reports read. ADR 0001→12, 0002→7, 0003→3, 0004→8, 0005→13, 0006→14, 0007→5, 0008→3/19, 0009→13. |

## 24. Primary sources

Accessed November 8, 2026. Versioned source outranks unversioned feature summaries. No cited API alone proves Lamp's performance or security.

[^S1]: [tsnet Go API, including federation, identity and ListenService](https://pkg.go.dev/tailscale.com/tsnet); [Tailscale service virtual address and redundant hosts](https://tailscale.com/docs/features/tailscale-services).
[^S2]: [GitHub scale-set Go client and JIT example](https://github.com/actions/scaleset/blob/main/README.md).
[^S3]: [Apple Virtualization framework](https://developer.apple.com/documentation/virtualization); [Rust objc2 VZ bindings](https://docs.rs/objc2-virtualization/latest/objc2_virtualization/struct.VZVirtualMachine.html).
[^S4]: [msb_krun 0.1.45 capture/restore API](https://docs.rs/crate/msb_krun/0.1.45/source/src/api/vm.rs); [private memory mapping](https://docs.rs/crate/msb_krun_vmm/0.1.45/source/src/private_memory.rs); [explicit block layers](https://docs.rs/crate/msb_krun/0.1.45/source/src/api/builders.rs); [block backend](https://docs.rs/crate/msb_krun_devices/0.1.45/source/src/virtio/block/backend.rs); [release metadata](https://crates.io/api/v1/crates/msb_krun/0.1.45); [microsandbox capture semantics](https://docs.microsandbox.dev/sandboxes/snapshots).
[^S5]: [Firecracker 1.17 specification](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md); [capture support](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md). Boot/init endpoints differ from Lamp command-ready.
[^S6]: [Upstream libkrun 1.19.8 README](https://github.com/libkrun/libkrun/blob/v1.19.8/README.md); [API and implicit networking behavior](https://github.com/libkrun/libkrun/blob/v1.19.8/include/libkrun.h).
[^S7]: [VZ save/restore validator](https://developer.apple.com/documentation/virtualization/vzvirtualmachineconfiguration/validatesaverestoresupport()); [save/restore presentation](https://developer.apple.com/videos/play/wwdc2023/10007/); [Mac identity requirements](https://developer.apple.com/documentation/virtualization/vzmacplatformconfiguration); [file-handle NIC](https://developer.apple.com/documentation/virtualization/vzfilehandlenetworkdeviceattachment); [macOS Tahoe license](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf).
[^S8]: [WHP samples](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/whp-samples); [Hyper-V standard versus production checkpoints](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/checkpoints).
[^S9]: [Cloud Hypervisor VFIO](https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/vfio.md); [Hyper-V GPU partitioning](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/gpu-partitioning). Qualification is hardware/OS-specific.
[^S10]: [gVisor execution platforms, including systrap](https://gvisor.dev/docs/architecture_guide/platforms/).
[^S11]: [XFS reflink](https://man7.org/linux/man-pages/man8/mkfs.xfs.8.html); [btrfs reflink](https://btrfs.readthedocs.io/en/latest/Reflink.html); [APFS clone APIs](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/ToolsandAPIs/ToolsandAPIs.html); [ReFS block cloning](https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning).
[^S12]: [Embedded Go Raft library](https://github.com/etcd-io/raft); [Raft safety, leader election and majority requirements](https://raft.github.io/raft.pdf).
[^S13]: [Wasmtime security model](https://docs.wasmtime.dev/security.html).
[^S14]: [gVisor userspace network stack](https://gvisor.dev/docs/architecture_guide/networking/). Using the stack does not imply using gVisor as Lamp's VM boundary.
[^S15]: [Firecracker clone randomness](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/random-for-clones.md); [AWS uniqueness guidance](https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html); [runtime restore guidance](https://docs.aws.amazon.com/lambda/latest/dg/snapstart-best-practices.html).
[^S16]: [CertMagic Go ACME implementation](https://github.com/caddyserver/certmagic); [automatic HTTPS behavior](https://caddyserver.com/docs/automatic-https).
[^S17]: [Samba service configuration](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html); [SMB2/3 protocol](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-smb2/); [SMB locking](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-smb2/6178b960-48b6-4999-b589-669f88e9017d).
[^S18]: [Docker credential substitution](https://docs.docker.com/ai/sandboxes/configuration/credentials/); [Docker CA troubleshooting](https://docs.docker.com/ai/sandboxes/troubleshooting/); [Claude Code network/CA configuration](https://code.claude.com/docs/en/network-config).
[^S19]: [AWS SigV4 canonical signing](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv-create-signed-request.html). The host re-signing adapter is Lamp's proposal, not an AWS CLI feature.
[^S20]: [Claude Code authentication](https://code.claude.com/docs/en/authentication); [Claude compliance](https://code.claude.com/docs/en/legal-and-compliance); [OpenAI open-source token-sharing route](https://developers.openai.com/siwc/token-sharing-open-source); [self-hosted execution](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms); [Copilot CLI authentication](https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli).
[^S21]: [MCP authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization); [MCP authorization security](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations).
[^S22]: [BrowserContext cookie/storage APIs](https://playwright.dev/docs/api/class-browsercontext); [browser auth-state sensitivity](https://playwright.dev/docs/auth); [device-bound sessions](https://developer.chrome.com/docs/web-platform/device-bound-session-credentials).
[^S23]: [1Password service accounts](https://developer.1password.com/docs/service-accounts/); [Bitwarden CLI](https://bitwarden.com/help/cli/); [KeePassXC guide](https://keepassxc.org/docs/KeePassXC_UserGuide); [Proton Pass CLI](https://protonpass.github.io/pass-cli/get-started/configuration/). These supply trusted broker-side retrieval, not a claim of four equivalent fill APIs.
[^S24]: [OpenID Connect core](https://openid.net/specs/openid-connect-core-1_0.html); [PKCE](https://www.rfc-editor.org/rfc/rfc7636); [Argon2id](https://www.rfc-editor.org/rfc/rfc9106.html).
[^S25]: [GitHub Actions OIDC claims and trust](https://docs.github.com/en/actions/reference/security/oidc).
[^S26]: [Cookie prefixes and Domain semantics](https://developer.mozilla.org/en-US/docs/Web/HTTP/Reference/Headers/Set-Cookie); [Clerk cookie architecture](https://clerk.com/docs/guides/how-clerk-works/overview); [Clerk domain configuration](https://clerk.com/docs/guides/development/deployment/changing-domains); [Clerk explicit subdomain allowlist](https://clerk.com/docs/guides/dashboard/dns-domains/subdomain-allowlist); [Clerk as OAuth/OIDC identity provider](https://clerk.com/docs/guides/configure/auth-strategies/oauth/single-sign-on).
[^S27]: [Let's Encrypt limits, registered-domain budget, overrides and ARI exemptions](https://letsencrypt.org/docs/rate-limits/); [DNS-01 wildcard challenges](https://letsencrypt.org/docs/challenge-types/).
[^S28]: [Copilot Actions self-hosted environment](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/customize-cloud-agent/customize-the-agent-environment); [Claude Code Action](https://github.com/anthropics/claude-code-action); [Codex Action](https://github.com/openai/codex-action).
[^S29]: [GitLab Fleeting](https://docs.gitlab.com/runner/fleet_scaling/fleeting/); [single-use capacity settings](https://docs.gitlab.com/runner/executors/docker_autoscaler/); [Custom executor maintenance status](https://docs.gitlab.com/runner/executors/custom/).
[^S30]: [Buildkite agent start and disconnect-after-job](https://buildkite.com/docs/agent/cli/reference/start).
[^S31]: [Jenkins Cloud extension points](https://www.jenkins.io/doc/developer/extensions/jenkins-core/); [agent transport](https://www.jenkins.io/doc/book/using/using-agents/).
[^S32]: [Forgejo ephemeral registration](https://forgejo.org/docs/latest/admin/actions/registration/); [Forgejo runner security and one-job](https://forgejo.org/docs/latest/admin/actions/security/); [Gitea 1.25 runner registration](https://docs.gitea.com/1.25/usage/actions/act-runner/).
[^S33]: [CircleCI machine runner single-task mode](https://circleci.com/docs/runner/machine-runner-3-configuration-reference/); [scaling](https://circleci.com/docs/runner/runner-scaling/).
[^S34]: [Azure self-hosted agent and once mode](https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/linux-agent?view=azure-devops).
[^S35]: [Bitbucket runner contracts](https://support.atlassian.com/bitbucket-cloud/docs/runners/); [autoscaler and label demand](https://support.atlassian.com/bitbucket-cloud/docs/autoscaler-for-runners-on-kubernetes/).
[^S36]: [Woodpecker agent configuration](https://woodpecker-ci.org/docs/administration/configuration/agent).
[^S37]: [Azure Dsv5 nested-virtualization support](https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dsv5-series); [Hyper-V nesting and Standard security type](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/enable-nested-virtualization).
[^S38]: [Cloudroom security and credential handling](https://www.cloudroom.dev/security); [Cloudroom FAQ and thread/sandbox model](https://www.cloudroom.dev/docs/faq).
[^S39]: [E2B Embed architecture/deployment scope](https://e2b.dev/resources/introducing-e2b-embed); [E2B runtime source](https://github.com/e2b-dev/runtime).
[^S40]: [Docker Sandboxes architecture](https://docs.docker.com/ai/sandboxes/architecture/).
[^S41]: [microsandbox 0.7.14](https://github.com/superradcompany/microsandbox/blob/v0.7.14/README.md); [runtime dependencies](https://github.com/superradcompany/microsandbox/blob/v0.7.14/crates/runtime/Cargo.toml).
[^S42]: [SmolVM 1.34.0](https://github.com/smol-machines/smolvm/blob/v1.34.0/README.md); [versioned branching behavior](https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/branching.md); [limitations](https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/limitations.md).
[^S43]: [App Sandbox entitlements and child inheritance](https://developer.apple.com/library/archive/documentation/Miscellaneous/Reference/EntitlementKeyReference/Chapters/EnablingAppSandbox.html); [AppContainer resource isolation](https://learn.microsoft.com/en-us/windows/win32/secauthz/appcontainer-isolation).

[^S44]: [Linux cgroup v2 controls](https://docs.kernel.org/admin-guide/cgroup-v2.html); [Windows Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects); [WHP partition properties](https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/funcs/whvsetpartitionproperty).
