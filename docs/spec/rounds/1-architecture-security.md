# Lamp architecture: security first

Status: independent round-one proposal. Audience: Lamp's owner and the other architects.
This document implements [the binding brief](../brief.md), uses [the glossary](../../../GLOSSARY.md), and respects ADRs 0001–0009.
The parallel requirements draft is a coverage check, not an edited input.
External facts will have primary-source URLs. Timing allocations are proposed budgets, not measured Lamp results.

## Design position

Put the hostile harness behind hardware virtualization. Keep API keys, passwords and browser cookies outside its address space.
Keep network policy outside the guest. Keep the relay outside the plaintext path.
Treat plugins as hostile programs with narrow, revocable authority.
Subscription logins follow ADR 0004 and remain the explicitly documented exception.
Linux create, memory-inclusive fork and workload speed remain release gates.

## 1. Components and trust boundaries

Lamp is a fleet service, including when the fleet has one host. It supports both agent-inside and agent-outside operation.
The host owns policy. Guest root is hostile. A connection grants an operation, never implicit host access.

```text
Caller: API / CLI / MCP / console / ordinary SSH
    | local socket, Tailscale, owner HTTPS, or inner TLS through relay
    v
Any reachable host: lampd [identity, authorization, placement, session directory]
    | signed requests + mutual TLS                  | replicated control log
    v                                              v
Execution host                                 Other coordination hosts
  lamp-supervisor: narrow privileged operations; no user-facing listener
  engine worker per sandbox -> hardware boundary -> Linux / macOS / Windows
  network guard per sandbox -> approved exit / private network / permitted links
  credential broker -> API proxy / MCP gateway / CLI signer / vault adapters
  browser kit worker -> isolated browser; filtered actions and human takeover
  plugin workers -> capability RPC only; no ambient host or sandbox access
  storage worker -> private disks, caches, volumes, templates, artifacts

Eilmer services: Clerk sign-in + relay registration and encrypted byte forwarding
Relay has no host TLS private key, fleet signing key, model credential or plaintext.
```

| Process | Runs where | Authority |
| --- | --- | --- |
| `lampd` | Every host, unprivileged service identity | Fleet API, policy decisions, journal, sessions; no hypervisor handles or vault master key |
| `lamp-supervisor` | Every host, minimal OS service | Validated disk/VM/network operations; accepts local authenticated RPC only |
| Engine worker | Execution host, one restricted process per sandbox | Exactly its assigned guest RAM, disks and network endpoint |
| Credential worker | Credential owner's host, separate identity per owner/provider | Only the bound account; memory-lock where supported; no core dumps |
| Network guard | Execution host, outside every guest | Mandatory packet path, flow audit, policy epoch and exit selection |
| Browser worker | Dedicated locked-down sandbox by default; host option | One owner's dedicated browser profile; no everyday desktop/profile access |
| Plugin worker | Wasmtime process or dedicated service sandbox | Explicit RPC operations and object references only |
| Relay | Eilmer or self-hosted public service | Routing/account metadata and short-lived routing grants; bounded ciphertext buffers |

`lampd` serves the dashboard locally too. No Kubernetes, Docker daemon or hosted database is required.
Install signed packages through normal OS package mechanisms. Preflight reports missing virtualization or privileges.
This design task runs no installer, privileged command, password prompt or desktop permission dialog.
Linux initially targets maintained distributions with KVM, cgroup v2 and kernel 6.6+; macOS targets Apple Silicon on macOS 26.
Windows initially targets x64 Windows 11 Pro/Enterprise and Server 2025 with required virtualization features.
Unsupported Windows editions expose the weaker fallback. Windows arm64 is an explicit additional certification row, not implied by an x64 binary.

## 2. Languages and reuse

Choose Rust for `lampd`, engine integration, supervisor, guest init, storage, policy and credential brokers, CLI and relay.
Use Swift only for the thin Apple Virtualization.framework binding. Use TypeScript for the dashboard.
Keep Go in two separate shipped programs: embedded Tailscale and the GitHub scale-set plugin.
`tsnet` embeds a userspace tailnet endpoint in Go; `actions/scaleset` supplies standalone autoscaling/JIT APIs without Kubernetes [S06][S07].
Reimplementing either protocol adds security and update risk. Calling them over bounded local RPC avoids Go inside the VMM or a large FFI boundary.
An all-Go control service is viable, but does not remove the Rust VMM boundary or give safer device parsers.
An all-Rust rewrite of Tailscale is rejected. Rust reduces memory-safety exposure; it does not prove isolation.

Reuse Firecracker and selected Rust VMM components for an established isolation boundary and likely speed.
Reuse cryptography, TLS, WebAuthn, JWT verification, SQLite and a Raft library for security and crash correctness.
Use Wasmtime for hostile plugin isolation [S08]. Write Lamp's resource model, scheduler, template builder and brokers.
Record each dependency's security/speed justification, license, upstream revision and advisory response owner.
All first-party host programs, SDKs and shipped plugins are Apache-2.0; the relay is AGPL-3.0.
Preserve permissive dependency notices. Guest kernels, optional Docker kit and proprietary OS frameworks retain their own licenses.
Do not embed GPL QEMU or non-Apache-convertible Tart into the host distribution to evade ADR 0008.

## 3. Engines, guests and devices

| Host / guest | Selected engine and reason | Capture and storage |
| --- | --- | --- |
| Linux x86_64/arm64 / Linux | Firecracker/KVM first. Minimal device surface and jailer. Cloud Hypervisor is a separately certified option if I/O or requested hardware is faster with it [S01][S02][S03]. | Paused CPU/RAM/device capture; private RAM restore; reflink raw disks or dm-thin |
| macOS arm64 / Linux | Hardened, pinned `msb_krun` engine component using HVF. Reuse its existing cross-platform capture work for speed; do not import the microsandbox product or template path [S04][S05][S61]. | Engine-owned CPU/device/RAM state; immutable memory files and APFS disk clones |
| Windows x64 / Linux | Same restricted engine component with WHP, not a shared WSL distribution [S05][S12]. | Explicit RAM capture, independent restored partitions, differencing disks or ReFS clones |
| macOS arm64 / macOS | Apple Virtualization.framework full-VM class; signed restore media and guest helper [S09]. | Validate save/restore configuration; APFS disks plus auxiliary storage; slower capture |
| Windows x64 / Windows | Hyper-V/HCS full-VM class; licensed installation media, guest helper and Windows drivers [S10]. | Standard checkpoint/save state includes memory; production checkpoints do not [S11]. |

These are implementation selections, not claims that upstream meets Lamp's targets.
Linux workers use the matching jailer, unique UID, mount/PID/network namespaces, `no_new_privs`, seccomp and an LSM policy; the jailer is specific to Firecracker, not a generic plugin jail [S63].
On Mac/Windows, the engine runs as a hostile process with only assigned block handles and a guard socket.
Disable TSI and host-root virtiofs entirely. Upstream libkrun treats VMM and guest as one security context and warns about host access [S04].
The hardening patch removes implicit socket forwarding, filesystem exports, unused devices and ambient handles.
Audit the exact downstream revision; run escape tests before admitting secrets. VZ remains an alternative Linux backend only if its exact configuration passes Linux RAM-save/fork tests.
Apple's save APIs are not evidence of a universal cheap Linux fork [S09]. No false fallback from RAM fork to file-only restart.

Linux guest: a maintained LTS kernel validated against the engine; initial Firecracker branch is its supported 6.1 line with required arm64 VMGenID backport [S02].
Use a tiny Rust PID 1, `lamp-init`, with a vsock command/file/session service, process reaping and lifecycle hooks.
Use a minimal verified distro root for tools. Full-VM classes use native boot stacks and a signed helper service.
Separate the guest helper's management identity from task processes. Never accept its claim as proof of external security policy.
For strict command auditing, offer a signed locked guest kernel with task root unable to unload its audit hook; see §14.

Boot hardware includes CPU, RAM, interrupts, timers, boot storage, entropy/generation identity and one management transport.
These mandatory safety components are disclosed. A data NIC appears only for requested network access.
Display, audio, keyboard, mouse, clipboard, USB and GPU are absent unless requested. No hidden host mounts or guest SSH key.
Firecracker stays headless. Linux display/audio/input requests select a certified device-capable engine worker.
GPU uses exclusive IOMMU-isolated assignment where supported, or a separately audited mediated device; never a shared privileged host driver socket.
Mac has no promised generic physical GPU passthrough. Advertise only measured supported graphics devices.
Unsupported device combinations return `DEVICE_UNAVAILABLE`; changing an immutable device graph recreates or explicitly stops the sandbox.
RAM fork with external device state requires a migratable device. Otherwise drain/detach with consent or return `DEVICE_NOT_FORKABLE`.
No Linux latency claim is attached to slower full-VM or accelerator paths, and their actual numbers remain published.

CPU is integer vCPUs; memory and disk use MiB/GiB. Default: 2 vCPU, 1 GiB RAM, 8 GiB disk, no optional devices; initial standard Linux maximum is 8 GiB RAM, subject to passing the complete envelope below.
Enforce host cgroup CPU/memory/I/O limits on Linux, worker scheduling plus guest size on Mac, and Hyper-V controls/Windows Job Objects on Windows [S13][S14].
Reserve VMM/guard overhead separately. Guaranteed-speed recipes forbid RAM/CPU overcommit and reserve device emulation CPU.
Disk quotas, process limits and packet rates stop fork bombs and disk/inode exhaustion. An OOM terminates the sandbox visibly.
Count suspended macOS instances against a conservative two-per-Mac lease until deleted/exported; never exceed the brief's cap [S15].

Without hardware virtualization: Linux uses gVisor systrap over a Lamp-built root; no Docker runtime [S16].
Mac/Windows expose native restricted task execution as `isolation=process`, with a weaker boundary and reduced Linux compatibility.
If no Linux-capable fallback exists locally, placement uses another enrolled Linux-capable host; never claim native Linux execution there.
Secret-bearing recipes require a VM unless the owner records an explicit override. Stranger public-PR jobs always require a fresh VM.
Fallback cannot silently inherit protected connections. Unsupported fork or guest OS requests fail explicitly.

## 4. Templates, create, fork, suspend and restore

Lamp's format is a signed, content-addressed manifest plus chunks. It is neither OCI nor a Dockerfile.
Portable filesystem content is separate from architecture/engine-specific acceleration state.

```yaml
format: lamp.template/v1
guest: { os: linux, arch: arm64, kernel: sha256:... }
root: { filesystem: ext4, chunks: [sha256:...] }
init: { digest: sha256:..., protocol: 1 }
build:
  - fetch: { url: https://trusted.example/tool.tar.gz, sha256: ... }
  - packages: { repository: signed-distro-repository, install: [git, python3] }
  - run: [sh, -c, "install-fixture-tools"]
acceleration: { engine: firecracker, abi: pinned-revision, checkpoint: sha256:... }
provenance: { builder: host-id, inputs: [sha256:...] }
```

The example URL is placeholder input, not an Eilmer-hosted address.
Build in a fresh secretless build sandbox. Verify signatures/checksums, materialize a raw filesystem disk and boot it with Lamp's kernel/init.
Package-manager and build commands run inside that boundary; the host never mounts an untrusted guest filesystem with a privileged parser.
Build steps, source archives and file imports have confined paths, size limits and reproducible input digests.
Boot once to a clean management barrier. Save optional acceleration state before account data, tasks or credentials enter.
Publish only verified content; keep names mutable with compare-and-swap updates and immutable version digests for execution.
A recipe pins template, harness, kits, devices, resources, network and connections, plus lifecycle/security policy.

Saving a running sandbox retains installed tools and selected files. Stop credential use, freeze I/O and make a consistent disk capture.
The builder reads a read-only exported block stream inside a separate build sandbox, strips excluded state, then performs a clean boot.
Exclude runtime mounts, vault material, subscription state, browser profiles, histories containing secrets, transient identities and signing handles.
Templates are disk/tool state with optional *clean* boot acceleration; task-memory captures are private resume records, not automatically publishable templates.
Known-secret scanning is a check, not proof that arbitrary hostile files are credential-free.
If a subscription token entered task-readable memory/files, ordinary in-place scrubbing cannot certify a clean capture; use the refusal/rebuild rule in §7.

Create fast path: authorization and idempotency reservation → eligible host → immutable template verification cache → private disk → guard rules → paused restore → identity barrier → command marker.
Prepare/download/build stays a distinct operation with its own timings. A cached template without a warm-pool hit must still meet the Linux create target.
Warm pool entries contain no job history, owner credentials or live external account session. They are never returned to the pool after assignment.
Key the pool by owner/trust class, template digest, engine ABI, devices and resources. Allocate zero to two ready entries per active recipe initially, under a memory budget.
Refill after consumption; disclose pool hit/miss and first-use preparation in benchmark results.

| Host filesystem | Private disk path | Constraint |
| --- | --- | --- |
| Linux XFS/btrfs | Reflink immutable raw disk | Same compatible filesystem; preflight real clone support [S17] |
| Linux ext4 | Host-owned dm-thin pool prepared at installation | Pool exhaustion blocks allocation; no hidden full-disk copy on fast path [S18] |
| macOS APFS | `clonefile`/clone-aware copy of raw disk and compatible memory backing | Same volume; disk cloning is not RAM capture [S19] |
| Windows NTFS | Lamp-managed immutable parent plus private block-delta file; Hyper-V uses VHDX differencing | Bound chain depth and compact away from create |
| Windows ReFS | Block clone when compatible, otherwise same block-delta path | Respect alignment/range constraints [S20] |

Linux memory-inclusive fork begins with a *running* source. It is not a restore-only benchmark.
1. Reserve the new identity, budget, disk quota and idempotency record. Stop credential-bearing lanes first.
2. Freeze task processes, drain device queues, flush guest filesystem, pause vCPUs, and freeze disk writes.
3. Serialize CPU/device state and RAM into a private immutable generation. Clone disk extents at the same barrier.
4. Restore the fork in a fresh worker with private RAM mappings; give it distinct disk writes and network identity.
5. Run restore hooks in parent and fork, then resume both. Observe an in-memory marker and independent mutations before reporting success.

Start with Firecracker full RAM capture into reserved local fast storage; keep clean captured pages available for private demand-paged restores [S02].
Do not count asynchronous durability as durable success. A volatile fork is labeled volatile; it still includes every page at the barrier.
For large working sets, qualify Cloud Hypervisor's reflink-plus-dirty-page capture from CoW-restored memory generations [S03].
Its private mappings avoid eagerly copying RAM on restore; capture still costs changed bytes. Keep origins immutable and reference-counted.
Never `fork()` a VMM and reuse inherited KVM handles; the KVM API does not support that lifecycle [S21].
If stock capture cannot meet the budget, add a reviewed memory-generation implementation; do not relabel disk copying as fork.
Full-dirty large-RAM fork is the main engine experiment and a release blocker, not an upstream-proven result.
Fork disk and private scratch are independent. Shared volumes remain external objects; default fork attachments are read-only or privately captured.
Explicit shared-write attachments retain shared semantics and are listed in the result. External transactions/sockets are reconnected, never duplicated as independent transactions.

Suspend commits an encrypted durable RAM/device/disk record, destroys the worker, and releases allocated guest CPU and RAM.
Drop checkpoint page-cache residency where possible; it must not remain pinned as a hidden running guest.
Wake on a new session message, SSH attach/input, API exec or permitted inbound request. Buffer bounded input until resume completes.
Proposed idle grace is five minutes; retention after suspension is seven days. Callers can override these; CI defaults to deletion at job end.
Idle requires no session activity, task process or open task connection. Background daemons count as work.
The minimal management loop is infrastructure; a harness may exit to resumable session state at a turn boundary. A running waiting harness still prevents automatic idle.
If process/connection inventory is uncertain, stay active. Guest-root evasion cannot defeat an independent wall-time budget.
“Instant” means demand-driven local restore with no human setup; provisional warm resume objective is p50 <500 ms on Linux, measured separately.
No network or disk can promise literal zero-time resume. Mac/Windows objectives are set after measurement.

Every create/restore/fork stays network-quarantined until a trusted guest barrier completes:
fresh sandbox/generation UUID, host-generated entropy, VMGenID handling and explicit RNG reseed before task release [S22].
Restart cryptographic processes or require registered userspace PRNG restore hooks; kernel RNG reseed alone does not fix cached application PRNGs [S22].
Issue new MAC/IP/DNS name, machine identity, broker handles and guest management channel binding.
Rotate SSH host identity if present, reset copied DHCP leases/TCP sessions and reconnect approved services under new scoped grants.
Set wall time from an authenticated host sample; preserve monotonic continuity and notify applications of elapsed suspension.
Recheck expiry/approval with host time, never restored guest time. Treat cached TLS sessions and bearer grants as invalid until rebound.
CPU model, architecture, VMM state ABI, guest kernel and device graph must match. Transfer disk/tool state across incompatible hosts; restart the harness for teleport.

## 5. Caches, volumes and artifacts

All lasting storage is owner-only by default, with separate encryption keys, quotas and explicit sharing ACLs.
Disk encryption protects theft at rest; host administration remains a trust boundary.

| Resource | Semantics | Security and lifecycle |
| --- | --- | --- |
| Cache | Content-addressed downloads/build entries, immutable published generations | Namespace by owner, trust class, architecture and toolchain; verify digest; evict by quota/LRU |
| Volume | Named persistent storage with multiple authorized attachments | Read-only multiattach or exclusive block writer; explicit shared-files mode uses a storage service with filesystem consistency |
| Artifact | Immutable output plus checksum, provenance and retention | Resumable upload; no active HTML on account origins; scan/untrusted download treatment |
| Template | Immutable manifest/digests plus owner-controlled name | Installed tools preserved; safe publication/build gate; referenced chunks remain until no dependent capture exists |

Shared-files volumes use a confined file protocol to a dedicated storage worker, not arbitrary host path mounts.
Open files by confined handles; reject traversal, symlink escapes and special-device requests. Distinguish crash consistency from application transactions.
Cross-host attachment goes over authenticated encrypted streams; placement follows exclusive writers, and write leases fence partitions.
Public PRs read only explicitly published dependency objects and write into disposable/untrusted namespaces.
Never promote untrusted compiler outputs into a protected build cache automatically. Trusted jobs may rebuild and verify them.
Export logs/artifacts on success, failure and cancellation before default deletion; record incomplete exports without keeping a secret-bearing runner alive.
Deletion revokes access immediately; encrypted chunk garbage collection follows reference counts and configured backup retention.

## 6. Network that guest root cannot remove

Every route crosses a guard outside the sandbox. The guest sees a NIC or broker transport, never a VPN key or private host adapter.
The supervisor binds a guard endpoint to one worker, sandbox, owner and policy epoch; caller-provided IDs cannot select a neighbor's endpoint.
Policy installation is atomic and precedes vCPU release. Guard/exit failure drops traffic; it never falls back to direct egress.

| Host | Mandatory path | Defense in depth |
| --- | --- | --- |
| Linux | Per-sandbox TAP in a host network namespace, nftables default-drop, then selected guard/exit | Anti-spoofing; no host namespace bridge; veth quotas; privileged helper accepts typed rules only |
| macOS | HVF worker's only data endpoint is an inherited packet socket to guard; VZ uses file-handle network attachment | No shared/NAT framework attachment; App Sandbox denies worker Internet access; separate broker owns actual sockets [S23] |
| Windows | WHP worker packet endpoint to guard; full-VM class uses private Hyper-V switch and mandatory gateway | Restricted worker token and WFP application/interface filters; test IPv4/IPv6 and vSwitch path separately [S24] |

Use kernel packet forwarding for explicitly allowed raw flows; use the HTTP/TLS guard for site policy and credential-aware requests.
Do not route all direct bulk traffic through a slow userspace TCP stack merely for convenience; each permitted path still emits external flow audit.
Policies select direct, WireGuard/OpenVPN, HTTP CONNECT or SOCKS egress plugins per sandbox, with distinct exit state/IP where the provider supplies it.
No promise of unique public IP when two sandboxes deliberately select the same NAT exit.
Private-network membership is a per-sandbox host-held tsnet instance or equivalent plugin identity. The guest cannot obtain its machine key.
Advertise only the intended sandbox ports, never a subnet route to the host/LAN. Revocation destroys membership and guard state.
Links are directional object grants: source, destination, ports, protocols, expiry. Across hosts they use fleet mTLS, not an open overlay mesh.

Default egress denies LAN, loopback, host APIs, link-local, metadata and all unrequested protocols.
A recipe explicitly grants internet destinations; deny rules always win over allow rules and over private-network/exit choices.
Resolve names in the host guard, pin approved addresses per connection and revalidate redirects/reconnects.
Normalize IDNA, trailing dots, IPv4 variants and IPv6; wildcard matches DNS label boundaries, never arbitrary suffix substrings.
Constrain SNI, HTTP Host/`:authority`, actual destination and certificate together. Do not treat SNI inspection alone as a secure site allowlist.
DNS uses a guard resolver with approved exact names and bounded queries. Block guest DoH/DoT, QUIC/ECH or arbitrary DNS when site policy requires visibility.
For raw private services authorize endpoint/protocol explicitly; read-only means protocol-level authorization, not “TCP connected to a database.”
Changing guest routes, proxy variables or nested Docker rules cannot create another physical packet path.
Docker-in-sandbox traffic follows the same guard; no host Docker socket is ever shared.

Inbound dev URLs are private by default, bound to one sandbox generation and port, expiring and revocable.
Ingress authenticates outside untrusted application code and strips spoofable identity headers before adding trusted identity metadata.
Watch permission does not grant shell/files or other ports. Public exposure requires an owner-authorized port grant.
Apply quotas, WebSocket authorization and request-size limits. URLs for suspended sandboxes wake them under a bounded request queue.

## 7. Credentials and approvals

### API-key proxy and CLI connections

An agent gets a useless sentinel and a connection reference. The actual API key stays in the owner's credential worker.
Persist it in macOS Keychain, Windows DPAPI/Credential Manager or an encrypted Linux owner store backed by Secret Service/TPM where available. Headless operation requires an explicitly provisioned unlock policy; never silently fall back to plaintext files.
Exclude broker memory from dumps, tracing and ordinary diagnostic bundles; keep per-owner encryption keys outside fleet metadata and avoid secret values in environment/argv.
The host proxy authenticates by assigned channel/sandbox generation, not by an attacker-selected sentinel string.
Map each connection to account, vendor origin, method/path schema, action scope, budget, expiry and permitted caller.
Strip any incoming real authorization/header overrides; inject only after the entire destination and request policy passes.
Do not follow redirects with credentials. Separate providers and owners into different worker identities.

TLS interception terminates sandbox TLS **on the owner's host** and creates a new validated TLS connection to the vendor.
This is deliberately different from the relay, which cannot terminate the inner TLS connection.
Use a per-owner proxy CA with its private key only in the broker; guest receives the public trust anchor.
Install trust into OS, Node, Python, Java and requested nested Docker kit stores; test every supported harness/runtime.
Verify upstream hostname, chain and permitted endpoint; forbid TLS verification bypasses. Pinned or incompatible clients use typed adapters, or fail with `AUTH_TRANSPORT_UNSUPPORTED`.
Support HTTP/1.1, HTTP/2 and WebSockets with one canonical parser and strict framing; fuzz smuggling, CONNECT recursion and multiplexed identity confusion.
Docker's sentinel/CA design is technical precedent, not subscription permission [S25].

Use typed provider adapters for credential-bearing requests. Validate response schemas and suppress authentication headers, cookies and credential-like error fields.
For reflection tests, hold enough response data to reject raw/encoded canaries before release; generic regex redaction alone is inadequate.
Never inject into an arbitrary caller-selected URL. An untrusted endpoint with no approved adapter gets no credential.
A fully compromised authorized vendor could transform a secret into arbitrary output; no generic proxy can prove noninterference for that endpoint.
Treat provider adapters and authorized destinations as part of credential trust, constrain their surface, and test deliberate reflection attacks.
API-key secrecy does not prevent abuse of authorized inference or data access; budgets and action scope constrain that authority.

`gh` and `stripe` get sentinel token configuration and narrowly scoped host API substitution.
AWS requires host-side SigV4 signing, not simple bearer rewriting; a credential-process adapter returns surrogate material usable only by Lamp's signing path [S26].
Bind the signing request to account, region, service, action/resources and canonical payload. Never offer a general signing oracle or return STS keys.
Git HTTPS uses the same host token path; SSH/git signing uses a constrained agent with destination, repository and approval binding.
Tests cover streaming uploads, redirects, token refresh and errors. Unsupported proprietary/device-bound authentication fails explicitly.

### Subscription logins: ADR 0004

Do not proxy subscription inference credentials. The unmodified official harness signs in by its approved vendor flow on the person's own hosts.
Inject only the minimum vendor-permitted state into that same person's sandbox after the create/restore barrier.
Keep vendor authentication and inference traffic on an unintercepted, vendor-only lane; other task connections use separate guards.
Root that can read a subscription token can misuse it through allowed vendor traffic. This is the documented exception, not a zero-leak claim.
While real subscription state is task-readable, the whole sandbox's general data egress is vendor-only. Additional broad CLI/MCP/private connections require a separate token-free task sandbox driven by the signed-in harness; a proxy-variable convention cannot enforce this separation.
No Eilmer service stores, refreshes, inspects or proxies that token. Cross-host transfer is direct owner-host mTLS, never relay application plaintext.
If the credential owner's host is offline, use a deliberately enrolled owner-host replica or report sign-in blocked; never use a central fallback vault.

Maintain a versioned sign-in catalogue: official binary digest, allowed methods, use context, terms/source URL, account ownership and refresh/capture hooks.
Catalogue entries are reviewed when vendors or binaries change. “Any harness” means a documented adapter contract, not permission to impersonate another vendor's client.

| Harness | Initial route and gate |
| --- | --- |
| Claude Code | Native vendor sign-in / documented setup-token use; preserve built-in choices and unmodified binary; hosting terms apply [S27]. |
| Codex | Native login/device auth, or registered ChatGPT OSS flow with per-sandbox host identity; transferred session refresh belongs to that runtime [S28]. |
| OpenCode, Goose, Pi | Approved upstream API-key providers behind proxy; subscription only for that provider's approved native route/adapter |
| Cursor CLI, Amp, Droid | Official automation/API authentication behind adapter; native subscription sign-in only with pinned supporting vendor evidence |
| Copilot CLI | Official device login or documented scoped automation identity; no assumption ordinary Actions job tokens confer a Copilot subscription [S29] |
| Gemini CLI | Current API/Vertex or eligible documented organizational auth; do not promise retired consumer subscription routes [S30] |
| Devin and others | Official supported local harness/remote-control adapter or API connection; no claim a proprietary hosted service runs arbitrary Lamp compute |

Refresh has one owner per vendor-issued session. For OpenAI's VM-transfer route that owner is the sandbox runtime, as its docs require [S28].
Do not have the host and several forks race one rotated refresh chain. Obtain independent permitted grants or serialize a single authorized runtime.
“Ready” includes a real valid signed-in check; expired/unsupported grants yield a useful blocked state, not fake readiness.

Before **any** suspend, fork, template save or other capture: fence inference → stop/exit the credential-bearing harness → remove its transient files/env/mounts → discard credential-bearing memory → freeze/capture → reauthenticate/restart from transcript.
Use tmpfs with swap/core dumps disabled, and a managed credential process compartment whose lifetime ends before capture.
Preserve ordinary task RAM; restart only credential-bearing harness state. The fork result states which processes restarted for stripping.
Deletion of a credential file does not erase heaps, filesystem journals or arbitrary copies made by hostile root.
If a token reached uncontrolled task state, mark the capture dirty and refuse it until a clean rebuild/restart boundary; scanner success cannot clear that mark.
Never take an unsafe capture “temporarily” and scrub it afterward. Fault injection must prove no usable capture is published on partial failure.
This is a real compatibility limit: unrestricted token-readable guest root, exact arbitrary RAM copying and guaranteed token-free capture cannot all hold.
Prefer capture-safe API-key mode for fork-heavy recipes. Subscription adapters must demonstrate managed capture safety or return `CREDENTIAL_STRIP_FAILED`.

### MCP gateway and owner-machine services

Gateway connections select one owner/account/server and a tool/method allowlist. OAuth discovery/PKCE and resource audience validation happen on the host [S31].
The guest receives a sandbox-bound short-lived Lamp handle, never the upstream OAuth refresh token.
Do not pass an inbound Lamp token through to an upstream API. Obtain an upstream token with its intended audience.
Run stdio MCP programs in restricted plugin/service sandboxes, not a general host shell.
An MCP server that truly requires in-sandbox files gets a short-lived, resource/action-limited Lamp delegation token and file scope.
Prefer broker-backed tokens even here. A required real upstream limited token is recorded as explicit delegated exposure; never silently inject an owner-wide API key.
MCP results, issues and web pages are untrusted data. Schema annotations like “read-only” do not replace service-side enforcement.
Owner-machine connections have off, read-only, ask-first or full access. Implement SQL read-only with DB permissions/transactions; HTTP/tool adapters enumerate allowed operations.
Raw TCP cannot honestly promise read-only semantics. Offer ask-first/full, or require a protocol-aware adapter.

### Browser kit, password managers and human takeover

The default browser runs outside the agent sandbox in its own locked-down browser worker, with an owner-specific dedicated profile.
The agent gets navigation, constrained interaction, screenshots and approved downloads. It does not get raw CDP, evaluate-JS, cookie/storage APIs, devtools, browser filesystem or arbitrary extension installation.
Raw browser control can retrieve cookies even when the profile is outside the agent sandbox [S32].
An origin/destination-aware action broker denies browser settings pages, password reveal/copy, credential export, arbitrary network inspection and secret-bearing downloads.
Use separate browser contexts per trust class; do not allow hostile extensions or dev-server code into a signed-in credential context.
Sensitive login pages enter a private broker-controlled phase: suspend agent input, screenshots, DOM, network output, clipboard and recording until completion.
Fill and submit only to the approved HTTPS origin/frame/form; block changed action URLs, cross-origin frames and script/debug interfaces during fill.
Never try to hide a password with CSS while still allowing arbitrary DOM access.
After login, resume constrained operations only when the adapter can ensure secret fields/results are absent. Unknown flows remain human-controlled or fail closed.
Authenticated sites and browser/adapter code are trusted for their own credentials. A malicious authenticated site echoing/encoding its own secret defeats a universal screenshot guarantee; do not claim that guarantee.

Vault plugins cover 1Password, Bitwarden, KeePassXC and Proton Pass using supported host APIs/CLI/browser interfaces [S33][S57][S58][S59].
Their adapter returns an opaque fill result, never a password-returning MCP tool. CLI retrieval, if needed, runs only in the credential worker.
Do not claim a generic “1Password for Agents” Lamp SDK exists; use documented primitives and test the actual supported route.
Vault scope and per-use approval are separate. An unlocked vault does not approve all future uses.
Human takeover is a single-writer lease from console/phone. The agent sees blocked status; MFA/CAPTCHA interaction stays on the private channel.
SSH prints an expiring private takeover URL/QR instruction usable in a normal browser; the terminal itself need not implement graphics.
Return control explicitly, expire the lease and erase transient sensitive recordings. Screenshots of private login never enter transcripts.

Each approval binds owner, vault/secret reference, sandbox generation, destination/action digest, expiry and one-use nonce.
Display the exact target and action on the trusted console/phone; recheck the digest at execution to stop approval races.
Deny/timeout performs no action. Retry reuses the approved operation ID, not a second secret use.
Settings, skills, slash commands, instruction files, MCP list and git identity flow one way from a chosen source. Signing keys remain behind a signing connection.
Return settings only as a reviewed confined diff with a distinct approval; never mount the source writable.

Copied browser logins and per-secret password injection remain explicit owner opt-ins, with destination, lifetime and exposure warnings in the grant.
An agent cannot self-approve, inherit a broad opt-in through recipe edits, or export that opt-in to its sub-agents.
In these modes a hostile process can read deliberately delivered bytes; a literal zero-disclosure promise would contradict the requested feature.
Report them as `explicit_exposure`, alongside the protected-mode zero-leak result, never count them as a passing containment test.
The only blanket credential exception is still ADR 0004's subscription tokens. The brief's opt-in exposure versus universal zero-leak wording needs explicit interpretation at merge; this proposal does not conceal it.

## 8. Sessions, consoles, sub-agents and teleport

A session has a stable fleet-wide ID, owner, sandbox generation, harness adapter, parent session, transcript cursor and input lease.
The session directory aggregates every host. T3 Code and Herdr configure one fleet endpoint; placement never creates another machine entry.
Console plugins implement discovery, list/subscribe, attach output, send input, approve and private browser takeover.
Ship conformance adapters against public T3 Code and Herdr interfaces; where an interface is unavailable, mark its adapter unverified rather than claiming compatibility.
Any third-party console can implement the same protocol; stock SSH always supplies a baseline console.

`ssh fleet-name` uses one fleet SSH host CA/key identity and a normal menu: list own sessions, attach, or select a sandbox shell.
Also accept `ssh fleet-name sessions`, `ssh -t fleet-name attach se_1` and `ssh -t fleet-name shell sb_1`.
Use OpenSSH-compatible certificates/keys and keyboard-interactive/local authentication; no custom terminal protocol.
Over the relay forward the SSH byte stream end to end; publish the standard SSH port and key fingerprint during trusted enrollment.
Host keys/CA are owner-controlled, not keys supplied by the relay. No SSH forwarding, agent forwarding or arbitrary host shell by default.
PTY reconnect uses cursor-based replay. One writer lease prevents two consoles interleaving accidental input; viewers may watch concurrently.
Watch shares cannot send input through SSH, API, MCP, files or browser actions. Sharing typing does not authorize vault approval.
Owner-only session visibility applies to list, search, events, errors and artifacts; unauthorized object IDs return indistinguishable not-found responses.
Team administrators can manage capacity without reading sessions. Host administrators remain trusted; this is not cryptographic protection from them.

`working` means an active turn/tool/process. `blocked` means an explicit question, approval, sign-in, capacity or external dependency.
`idle` means no active session turn; sandbox idle suspension separately requires the stronger process/connection rule in §4.
Use harness-native events where available and PTY/process observation otherwise. Report uncertain status honestly; do not infer success from silence.
Agent-outside sessions use the same status, transcript and control objects while API commands/files/browser actions drive the sandbox.

Sub-agents choose `sandbox=shared` or `sandbox=own`. Shared means shared files/process/credential authority; display that clearly.
Own means fresh sandbox with a consistent parent-file capture, including uncommitted and selected untracked files, excluding credential paths by default.
Track ignored files explicitly in the capture manifest; never silently omit requested task inputs or copy an entire credential-bearing home directory.
Changes return through a git bundle/commit in a task repository, with file modes and binary changes preserved.
For non-git work create a private task git repository for exchange. Apply only the selected commit; report conflicts without overwriting parent work.
Sub-agent grants are the intersection of starter authority, recipe policy and task budget. Neither forks nor sharing reset budgets or broaden credentials.

Teleport moves the **session** between an enrolled laptop host and a sandbox, not arbitrary incompatible process RAM.
Pause the harness at a supported turn/tool boundary; checkpoint transcript/harness resume ID, consistent task files, settings digests and tool outcomes.
Strip credentials before transfer. Transfer directly between owner hosts with end-to-end encryption and verify hashes.
Prepare destination tools and approved sign-in, acquire a new fenced session-location epoch, then resume the official harness using its supported continuation API.
Stop the source before accepting destination input; an interrupted move resumes exactly one authoritative location or remains blocked for recovery.
Local mode is explicit host execution authority. Do not restart a stranger's CI session onto the owner's ordinary laptop account.
Same-engine compatible sandbox migration may additionally preserve RAM; cross-OS/architecture teleport preserves logical harness work and files.
Unsupported harness continuation returns `TELEPORT_UNSUPPORTED`; custom harnesses supply a continuation adapter rather than pretending a transcript is process memory.

## 9. API, MCP, CLI and generated SDKs

One OpenAPI 3.1 definition owns resources, operations, authorization requirements, errors and event schemas.
Generate TypeScript and Python SDKs first, then Go and Rust; ship all four before first release. Generate CLI/MCP coverage from operation metadata.
Use REST/JSON for control, SSE for events and authenticated WebSockets for PTY/browser streams; local IPC carries the same operations.

| Resources / endpoints under `/v1` | Operations | Example CLI / MCP |
| --- | --- | --- |
| `/fleet`, `/hosts`, `/memberships` | Inspect, join/remove, drain, pin, update | `lamp host list --json` / `host_list` |
| `/templates`, `/recipes`, `/kits` | Build, inspect, save, publish owner-scoped versions | `lamp template save sb_1 --name tools` / `template_save` |
| `/sandboxes` | Create/list; per-ID exec, files, fork, suspend, resume, delete | `lamp sandbox fork sb_1` / `sandbox_fork` |
| `/sessions` | Start/list/attach/input, spawn, share, teleport | `lamp session spawn se_1 --sandbox own` / `session_spawn` |
| `/networks`, `/links`, `/endpoints` | Exits, membership, rules, explicit links, port grants | `lamp endpoint create sb_1 --port 3000` / `endpoint_create` |
| `/connections`, `/approvals` | Bind account/actions, request/decide/revoke | `lamp connection set db --access ask-first` / `connection_set` |
| `/caches`, `/volumes`, `/artifacts` | Create, attach, transfer, retain, delete | `lamp volume attach data sb_1 --read-only` / `volume_attach` |
| `/jobs`, `/triggers`, `/schedules` | CI lifecycle, signed intake, timed sessions | `lamp schedule create --recipe maintenance --cron '0 9 * * 1'` / `schedule_create` |
| `/keys`, `/shares`, `/plugins`, `/budgets` | Scoped authority, admission, limits | `lamp key create --recipe test --actions create,run,read --ttl 10m` / `key_create` |
| `/events`, `/audit`, `/metrics`, `/operations` | Watch, replay, inspect asynchronous work | `lamp events --json` / `events_read` |

```text
POST /v1/sandboxes   Idempotency-Key: build-274
{"recipe":"linux-test@sha256:…","resources":{"vcpus":2,"memory_mib":1024},"host":"auto"}
-> 202 {"id":"sb_1","operation":"op_1","state":"creating"}
POST /v1/sandboxes/sb_1/exec {"argv":["python3","-c","print(42)"],"session":"se_1"}
-> {"exec":"ex_1","stream":"/v1/operations/ex_1/output"}
lamp exec sb_1 --json -- python3 -c 'print(42)'
MCP sandbox_exec({sandbox:"sb_1", argv:["python3","-c","print(42)"]})
```

`202` is admission, not ready. SDK `create(wait="command")` measures through the executed readiness marker.
All commands, including errors/help/interactive variants, offer `--json`; interactive data uses framed JSON events and no prose on stdout.
`lamp docs --json` and versioned plain-text agent docs describe every operation and safe retries.
All state-changing operations accept idempotency keys scoped to fleet, principal and operation. Persist canonical input hash/result before effect.
Same key/different body is `409 IDEMPOTENCY_CONFLICT`. Retain active-operation records indefinitely and completed records for at least seven days.
Exec and typed input have operation/sequence IDs too. Do not promise exactly-once arbitrary external side effects after a guest crash; return `OUTCOME_UNKNOWN` rather than retry blindly.
Delete is idempotent. Precondition/ETag versions prevent stale policy, recipe and approval edits.
Events are at least once, ordered per resource with `(host, boot_epoch, sequence)` plus a fleet control-log cursor.
Retain seven days by default; reconnect with cursor, deduplicate by event ID, return `CURSOR_EXPIRED` with resync instruction.
Bound slow-consumer queues, scope/filter before sending, and reauthorize long-lived streams at revocation.
Use durable operation events for control decisions; terminal bulk output has separate bounded retention/backpressure.
Every error includes stable code, safe message, request ID and retryability. Examples: `VM_REQUIRED`, `NO_CAPACITY`, `KEY_SCOPE_DENIED`, `CREDENTIAL_STRIP_FAILED`.

## 10. Plugin protocol, isolation and versioning

Anyone can author a plugin. Separate processes are necessary but insufficient isolation.
Default portable plugins are WASI components, each in its own Wasmtime process, with fuel/memory/time quotas and **no** ambient filesystem/network imports [S08].
Native Go or vendor-specific plugins run in a dedicated service sandbox; first-party host adapters receive narrowly jailed native workers where system APIs require them.
Plugins exchange length-delimited, size-bounded JSON-RPC over inherited pipes/named pipes, with request IDs, deadlines, cancellation and peer identity.
Startup negotiates `lamp.plugin/1`, type-specific API version and features. Major versions coexist; unsupported features fail before any resource change.
Manifest pins publisher, executable digest, permissions, compatibility range and protocol; a signature establishes provenance, never that code is safe.
Crash, hang or output flood terminates that worker, revokes grants and leaves unrelated sandboxes running. No plugin can load code into `lampd`.
Installation has explicit permission review; updates cannot expand grants. Rollback includes executable and manifest; pin per recipe/connection.

| Plugin type | Permitted contract, not ambient access |
| --- | --- |
| Engine | Declare eligible guest/device graph and request supervisor operations; backend worker receives only assigned sandbox handles |
| Network / caller access | Request authorized route or byte stream; cannot mutate host routes/firewall directly or terminate relay inner TLS |
| Credential / password manager | Select approved account/secret and perform scoped use in a credential worker; no secret-returning agent API |
| Harness / kit | Signed install/configuration/continuation descriptors; software runs inside selected sandbox, never installer scripts on host |
| CI / triggers | Typed admission, logs, cancellation and result operations with mapped caller identity |
| Console | Authorized session enumeration/streams/input; no unrelated sandbox filesystem or vault access |

Engine implementations inherently see their assigned guest RAM; vault implementations inherently handle their granted secret.
Those specific implementations enter the TCB for that object. It is impossible to let an arbitrary engine read assigned RAM while claiming it cannot reach that RAM.
Keep third-party engine/network *controllers* unprivileged; admitting a new native VMM backend requires explicit owner trust and the attack suite.
Plugins cannot acquire `/dev/kvm`, Hypervisor entitlements, raw host adapters, arbitrary process handles or vault access through protocol strings.
The supervisor revalidates effective grants, object ownership and path confinement on each call; plugin self-reported labels are never authorization.

## 11. Fleet membership, placement and failover

Hosts enroll with a one-use owner-signed invitation, generate hardware-protected keys where available, and receive a fleet membership certificate.
Bind host role, owner/security domain, allowed guest OS/devices and update policy. Removal revokes certificates, routes and future placement.
Store control metadata in a Raft-replicated log over mTLS; choose three voter hosts when available, with other hosts as learners/workers.
The log contains ACLs, keys' verifiers, recipes, placement reservations and audit roots, not upstream model/vault secrets.
Keep a local SQLite journal for durable operations and active sandbox ownership. The host holding a sandbox is execution truth.
Coordinator failover changes routing/scheduling authority, never fabricates migration of a sleeping host's RAM.
Heartbeats every two seconds; propose election/reconnect within ten seconds after loss under healthy network conditions. Publish actual recovery data.
Hosts reconnect outbound through all configured transports; each API entry can forward to the current coordinator without caller reconfiguration.

Placement filters owner trust, VM requirement, guest OS/architecture, device availability, policy, volume locality and capacity, then scores measured readiness/load.
Pick the fastest **certified** eligible engine for the actual template/resources/device/security tuple; security exclusions precede speed ranking.
Pins are hard constraints. No capacity returns `NO_CAPACITY` or an explicitly requested bounded queue; never silently changes the pin or overflows to cloud compute.
Reserve task-wide CPU/RAM/disk/time/token allowances atomically, including forks/sub-agents. Preauthorize bounded per-host allocation permits for offline use.
Native performance recipes reserve CPU/RAM and required I/O headroom; admission rejects unsafe overcommit.

One host uses a local durable log. Two-host fleets cannot have both quorum safety and unlimited partition availability.
On loss of quorum, a survivor automatically coordinates discovery/attach and executes already delegated, disjoint host-local allocation permits.
It can start authorized work within those permits and expiry; it cannot invent new global grants, spend another host's quota or accept membership/ACL changes.
This supplies useful laptop failover without a relay voting witness. Global security changes wait for quorum; display degraded status.
Use fencing epochs for exclusive volumes, session teleport and placement claims. Old hosts must reconcile before taking new work after sleep.
No trusted service quorum remains: control operations are unavailable, not silently reset. Active isolated work obeys its existing bounded lease.
Local accounts and local OIDC continue without Clerk/WAN. Cached public keys never waive token expiry; keep a preconfigured local recovery administrator.

Updates are off by default. Opt-in rollouts drain one host, preserve sessions on a compatible old worker, update, health-check, then proceed.
Never assume a checkpoint survives a VMM/OS ABI change; retain old workers/readers until resume or an explicit cold migration completes.
Rollback on failed health check and stop the rollout. Do not update an additional host while one is unhealthy or quorum would be lost.
Compatibility window covers control protocol N/N−1; opaque engine state requires its exact declared ABI, with validated migration tooling where supplied.

## 12. Access, identities, relay and domains

Built-in tsnet runs on each host with host-held state. Network identity is an input to Lamp authorization, never automatic administrator authority [S06].
Use tailnet grants/identity plus Lamp action scopes; support a chosen Tailscale control URL, including compatible self-hosted control services.
GitHub callers can join ephemerally through Tailscale workload identity federation with no stored join secret [S34].
Tailscale/DERP supplies network reachability; it neither replaces Lamp's account ACLs nor proves Linux direct-network throughput.
Local-only mode uses an ACL-protected Unix socket/named pipe and optional loopback HTTPS. Owner-domain mode uses direct HTTPS with ACME HTTP-01/TLS-ALPN-01 or DNS-01.
DNS-01 can issue behind NAT, but does not itself make an unreachable host reachable. Use Tailscale or relay when inbound routing is absent.
A Cloudflare Tunnel caller-access plugin is separate and labels where Cloudflare terminates TLS; it is not the ADR 0009 relay.
Owner-chosen domains are self-hosted endpoints; every Eilmer-hosted endpoint remains under `eilmer.si`.

### End-to-end encrypted relay protocol

Use a small audited pass-through implementation, borrowing the SNI-routing principle of SniTun, not its entire Python protocol stack [S35].
Host opens outbound TLS 1.3/QUIC transport to `relay.eilmer.si`; reliable ordered streams also work over WebSocket when UDP is blocked.
An outer routing channel carries fleet ID, host ID, bounded stream metadata and liveness. Relay authentication terminates here, not sandbox traffic.
Caller then opens a separate **inner** TLS 1.3 session directly to the host's fleet endpoint through an allocated stream.
Host alone holds the endpoint private key. SSH retains its native end-to-end encryption. Disable inner TLS 0-RTT for state-changing calls.
Multiplex only ciphertext; enforce frame size, bandwidth/connection limits, timeout and fair queuing without parsing application bytes.
No relay disk spool, payload logs, content inspection, inference path, template store or sandbox backup.
The relay inevitably sees account, routing name, source IP, size/timing and transient ciphertext. Document that metadata and minimize retention.

Clerk signs a person into their one Eilmer account. Relay verifies issuer/audience/signature and links already enrolled host keys to that identity.
Issue a one-use routing grant: account, fleet, endpoint key fingerprint, caller public key, nonce and ≤60-second expiry.
Bind redemption to caller proof-of-possession and consume atomically. The host checks a separate owner-signed caller enrollment/action grant inside the encrypted channel.
Relay-issued routing permission is not a fleet authorization key. A compromised relay cannot enroll itself or grant exec merely by forging its own routing grant.
Initial caller enrollment verifies the fleet root by local pairing/QR or an existing trusted caller; subsequent changes need owner signatures.
Native callers pin the fleet root and endpoint key delegation. Web dashboards are served from the host over inner TLS; the relay cannot substitute their JavaScript.
Keep DNS/ACME authorization in a separately secured issuance service which requires the enrolled fleet key on each CSR; relay process credentials cannot request replacement host certificates.
The trust claim covers relay-process compromise. Compromise of the owner's host, fleet root, trusted console, account-origin code or domain/CA administration is a larger trust failure.
Relay can drop, delay or correlate traffic. It cannot silently read/modify a valid pinned connection. Test endpoint substitution and replay, not just packet ciphertext.

### Accounts, scopes and GitHub exchange

Relay use always requires an Eilmer account on Clerk. Non-relay fleets accept local accounts or their own OIDC issuer, independently of Clerk.
Map `(issuer, subject)` to an immutable principal. Do not join accounts by email alone. Local accounts use passkeys or a memory-hard password verifier plus optional second factor.
Single-owner, team and public-service modes use the same authorization engine; public service is explicit admission with quotas, no anonymous default administrator.
Keys are opaque 256-bit random values, displayed once, stored as keyed hashes, revocable by ID and short-lived by default.
Effective scopes intersect principal rights, parent delegation, recipe restrictions, object grants, budget and current policy.
Scope to sandbox/recipe/host IDs, create/run/watch/read actions, resource maxima, task budget and expiry. Additional admin/approve/share scopes are distinct.
A create-only key cannot execute in an existing sandbox; watch-only cannot read files. Child keys cannot exceed parent expiry/scope.
Revoke active streams/grants promptly, with a proposed ≤5-second online propagation target and bounded offline lease expiry.

`POST /v1/auth/github-oidc` accepts GitHub's JWT and returns a ≤10-minute, job-bound Lamp key [S36].
Validate pinned issuer/discovered JWKS, algorithm, audience `lamp:<fleet-id>`, signature, `exp`/`nbf`, run identity and replay binding.
Match immutable repository/owner IDs, permitted event/ref/environment and the exact trusted workflow/reusable-workflow identity; compare permitted workflow SHA where available.
Do not trust a mutable repository name or generic `sub` alone; bind repository metadata against forge API evidence.
OIDC authentication does not make PR code trusted. Intake decides trust class and intersects it with the maximum no-secret public-PR recipe.
Fork PRs without OIDC use a trusted host-side GitHub App intake. Never put an alternate long-lived Lamp key into PR code.
The returned job key can create only its allocated fresh sandbox and bounded task; it cannot mint credentials or access the host.

### `eilmer.si` cookie, origin and certificate strategy

| Surface | Name pattern | Authority separation |
| --- | --- | --- |
| Website/docs | `eilmer.si`, `docs.eilmer.si` | No fleet session credential in static docs |
| Account/sign-in | `account.eilmer.si` | Clerk identity; no sandbox HTML, artifact HTML or user script |
| Relay control | `relay.eilmer.si` | Account-scoped routing grants only |
| Fleet API/dashboard | `api.<fleet>.fleets.eilmer.si` | Host-terminated TLS and host-only fleet auth |
| Host address | `h-<host>.<fleet>.fleets.eilmer.si` | Same fleet's host identity, separate authorization |
| Sandbox content | `p-<random-generation-port>.<fleet>.fleets.eilmer.si` | One origin per exposed port/generation; never reused for another owner |

All sensitive cookies use `__Host-`, Secure, HttpOnly, Path=/ and no Domain attribute. Prefer host-bound session credentials over ambient cross-product cookies.
Do not set `Domain=eilmer.si` in any Eilmer product; audit Clerk configuration and its actual emitted cookies [S37][S38].
Terminate product sessions in an Eilmer-controlled backend using `__Host-` cookies after Clerk validation. Do not assume Clerk's default SDK cookie names/Domain settings satisfy this rule; require host-only sign-in configuration and server-side session exchange, with a browser cookie-tossing test as the release gate.
Single Eilmer identity does not require one cookie shared across every subdomain. Use redirect-based code/PKCE sign-in and host-local sessions.
Sibling subdomains remain same-site until a relevant public-suffix boundary exists; SameSite alone does not stop CSRF.
Require exact Origin on mutations/WebSockets, CSRF tokens, strict CORS without reflected origins, and Fetch Metadata checks.
Validate Host against enrolled names. Do not enable `document.domain`; use origin-agent clustering, restrictive CSP/frame policy and exact-target `postMessage`.
No secret tokens in query URLs, Referer or untrusted page storage. Port-link fragments exchange once for a host-only opaque session; upstream app never gets that credential.
Ingress strips untrusted parent-domain cookies and Domain-bearing Set-Cookie, bounds headers, and separates ingress auth from app cookies.
Service workers are origin-scoped; random nonreused endpoint names prevent an old worker controlling a new sandbox.
Sandbox HTML is never embedded with account-origin privileges. Downloads use attachment handling on sandbox origins.

Issue one wildcard `*.<fleet>.fleets.eilmer.si` per fleet, with the private key generated on owner hosts; never a platform-wide wildcard copied to unrelated hosts.
Only explicitly trusted ingress hosts in that fleet receive an encrypted copy for failover, or use separately issued keys subject to issuance quotas.
One certificate covers API/host/port labels for that fleet; zero certificates are minted per sandbox.
Central issuance admission reserves at most 40 new certificates per rolling seven days under `eilmer.si`, leaving ten for other Eilmer needs/recovery; share the actual global budget across products.
Queue fleet HTTPS activation on exhaustion; never claim a certificate is ready or bypass verification. Existing local/Tailscale access remains usable.
Use DNS-01, staging for tests, persistent ACME state and renewal information; Let's Encrypt documents 50/week per registered domain and a separate exact-identifier limit [S39].
Request a certificate limit override for growth; independently request a Public Suffix List tenant boundary for cookie/site isolation, not merely to evade issuance limits. Neither approval is assumed [S40].
An accepted `fleets.eilmer.si` suffix gives separate tenant sites/registered-domain issuance accounting, but would still leave same-fleet sandbox/account separation needing the controls above.
No second registrable domain is proposed. DNS issuance authority and wildcard key exposure remain explicit infrastructure risks.

## 13. CI and triggers

One plugin job contract: authenticate intake → classify immutable event/code identity → create fresh sandbox → run one job → export results → revoke/deregister/delete.
Registration, job transport and application credentials are separate objects. Controllers keep organization-wide credentials outside the job sandbox.
Native CI runners execute inside fresh sandboxes; Lamp preserves the CI system's log/cache/artifact/exit/cancel protocol.

| CI family | Shipped plugin pattern and evidence |
| --- | --- |
| GitHub Actions | Go `actions/scaleset` outbound listener plus JIT one-job runners; webhook/JIT alternative [S07][S41] |
| Copilot cloud agent | Supported Actions-based scale-set route with the required `copilot-setup-steps` workflow/OS; disable only Copilot's built-in firewall when required, retaining Lamp's host guard [S42] |
| Claude Code / Codex Actions | Official actions on fresh runners, approved API/sign-in route; disposable runner removes persistent account changes [S43][S60] |
| GitLab | Fleeting instance provider, one use/one job; Custom compatibility adapter for deployments needing it [S44] |
| Buildkite | Queue-aware controller starts agent with disconnect-after-job; teardown is controller-enforced [S45] |
| Jenkins | `Cloud` provisioning/retention plugin, one executor and unique inbound agent identity [S46] |
| Forgejo / Gitea | Versioned distinct adapters: Forgejo one-job ephemeral registration; Gitea ephemeral act_runner [S47][S62] |
| CircleCI | Machine runner single-task mode plus resource-class demand scaler; VM deletion remains Lamp's job [S48] |
| Azure Pipelines | One-job native agent and pool-demand controller; do not pretend Azure VMSS provisions arbitrary Lamp hosts [S49] |
| Bitbucket | Runner API/label controller, warm registration if required; stop and delete after exactly one step/job before reassignment [S50] |
| Woodpecker | One-workflow agent and provider adapter; concurrency one, explicit VM teardown [S51] |

Docker jobs, Compose, service containers and image builds use the optional **inside** Docker kit; this never changes Lamp template/build/runtime input.
Native macOS/Windows jobs select slower licensed full-VM recipes. GPU, nesting and physical-device jobs require certified explicit devices.
Any CLI-capable CI can call the same API/SDK for remote subtasks without pretending it has a native provisioning hook.
Pin and publish the tested runner/controller/action versions for every shipped plugin; a protocol sketch alone is not first-release completion.

Public PRs get a newly instantiated secretless VM from a verified clean template, a new writable disk, no owner connections, no subscription, no browser profile, no host mounts and no trusted writable cache.
The guard permits only source/package mirrors, result upload and required CI endpoints; block LAN/metadata/private networks and arbitrary internet uploads.
Keep runner registration/control in a separate restricted runner lane or use a host executor shim with job commands delegated into the fresh VM.
Upstream runners often carry job-scoped transport/read-only source tokens; these are not owner vault/model secrets, but still must not grant cross-job control [S41].
Where protocol-compatible, broker these tokens through a host channel; otherwise record the minimal one-job transport capability and prove it cannot reach trusted resources.
Never execute untrusted head code in privileged `pull_request_target` or trusted reusable workflow context. Do not trust PR-provided labels, recipes, cache keys or workflow edits [S52].
Admission refuses any attempt to downgrade VM isolation or attach a secret. Owner approval to run a stranger's PR does not make its code trusted.

Slack, Linear and generic webhook trigger plugins verify signatures/replay windows and map the external person/service to a scoped caller.
Schedules use explicit IANA timezone/DST policy, stable run IDs, no overlap by default, and one coalesced missed run after outage.
Trigger delivery is at least once; deduplicate source event/run IDs before session creation. Revalidate authority at execution, not only schedule creation.
Notifications/replies operate under a separately granted destination/action scope; an agent cannot choose arbitrary recipients through task text.

## 14. Observability, budgets and watchdog

Dashboard shows every authorized host, sandbox, session, resource, device, connection, job, approval, task budget and plugin state live.
Show working/blocked/idle, location, lifecycle, actual isolation class, auth/capture limits, queued operations and degraded coordination.
Metrics/logs remain local by default. Export owner-selected OpenTelemetry events; redact before serialization, never after upload.
Usage data to Eilmer is off on install, upgrade and crash handling; fleet-level opt-in names fields/retention, and opt-out stops collection.
Necessary relay account/routing metadata is separately documented and minimized; no covert product analytics through “operational” payloads.

Audit every admitted command/input, executable transition and network request/flow under sandbox/session/owner/job/operation IDs.
HTTP proxies record sanitized destination/method/path template, policy decision, bytes/status and approval ID, never Authorization, Cookie, query secrets or bodies by default.
For encrypted vendor lanes/raw protocols record externally observed flow/request envelopes and visibility limits; do not claim decrypted request detail.
Commands from arbitrary guest root need a kernel boundary: signed locked Linux kernel with enforced exec audit, no module/BPF unloading, and an external acknowledged sink.
Block task execution on audit loss in strict mode; detect sequence gaps. Shell built-ins/interpreters require mediated command/PTY records as well as exec records.
Guest-supplied logs alone are not tamper-proof. Full Windows/macOS arbitrary-admin command fidelity needs native enforcement validation; otherwise label audit completeness uncertain and block a strict-audit recipe.
“Every command/request” cannot mean semantic HTTP requests inside arbitrary opaque encrypted streams. This is a brief ambiguity; publish visibility per mode rather than invent completeness.
Store append-only hash-chained audit batches outside guests, sign roots and replicate/export to owner storage. A compromised host can still falsify future events; another-host receipt preserves past roots.

Budgets aggregate wall time, token usage, CPU, storage, network and optionally spend across a task's sessions/forks/sub-agents.
For proxy/API mode reserve model output allowance before dispatch and reconcile provider usage after completion; request cancellation cannot undo already generated tokens.
Subscription/agent-outside adapters provide signed usage events where supported; exact enforcement is unverified when the provider does not expose authoritative counters.
For a hard token cap, require a metered adapter or approved API route; never show a fake exact remaining count for opaque subscription traffic.
Host deadline enforcement independently cuts network and stops the complete sandbox process tree. Freeze before asking about suspected loops.
Watchdog defaults: repeated identical tool/action hashes with no observed progress, excessive retries, sustained failure or missed harness heartbeat; configurable thresholds and explicit stop reasons.
Notifications cover done, stuck and needs-decision on console/phone; signed, expiring links carry references without secret contents.
Plans/quotas/billing have resource-accounting hooks, but no payments or mandatory hosted billing service in first release.

## 15. Threat model and zero-leak proof obligation

Assets: host integrity, other owners' data, API keys, passwords, cookies, subscription grants, fleet keys, audit integrity and finite resources.
Trusted computing base: host kernel/hypervisor, supervisor, selected engine/device code, guard, approved credential/browser adapters, owner console and account/issuance authority.
Guest root, repository code, tool output, pages, plugins and relay transport are hostile. Authentication never makes code trustworthy.

| Attacker | Boundary / mitigation | Residual and required evidence |
| --- | --- | --- |
| Agent with guest root | Hardware boundary, no host mounts, external policy, scoped broker operations, quotas | VMM/kernel escape and side channels remain risks; fuzz virtio/vsock/block and escape probes |
| Stolen Lamp key | Scope intersection, short TTL, proof binding where supported, revocation and quotas | Can exercise its remaining authority; cannot be made harmless retroactively; test every interface/reconnect |
| Malicious plugin | WASI/service sandbox, no ambient handles, supervisor validation, bounded RPC | Its expressly granted engine/vault object is trusted to that implementation; crash/escape/confused-deputy tests |
| Compromised relay | Inner pinned TLS/SSH, independent fleet enrollment, separate issuance authority | DoS and metadata disclosure; test hostile routing, replay and counterfeit endpoint grants |
| Malicious public PR | Fresh secretless VM, immutable trust policy, separate caches/runner control, limited network | Supply-chain results remain untrusted; execute real hostile scripts and inspect sinks/next job |
| Compromised host | Revoke host, isolate owners' credential hosts, key rotation and reinstallation | Host can read its guest/broker RAM and forge local enforcement; no software-only zero-leak promise against host root |
| Malicious page/MCP output | Tool scope, approved origins, no raw browser control, private fill/takeover | Authorized account/data misuse remains possible; test forced malicious actions rather than model refusal |

Protect secrets by absence: real API keys never enter task RAM; browser/password values stay behind bounded action interfaces; VPN/private-network keys stay outside guests.
Do not mistake ciphertext at rest, output masking or prompt filtering for a credential boundary.
For every supported host/mode run a deterministic attack corpus with independent canaries for API keys, passwords and cookies; pass count is **zero unauthorized leaks**.
Inspect guest RAM/files/env, process handles, captures, templates, caches, artifacts, events, logs, consoles and decoded controlled network sinks.
Include encoding/chunking/compression, reflected headers, DNS/IPv6/UDP/QUIC, SSRF/rebinding/fronting, nested Docker, endpoint swapping, revoked keys and approvals raced across forks.
Browser tests attempt cookie/storage export, JS/DOM/devtools, malicious forms, screenshots/downloads, reveal-password actions and takeover races.
Plugin tests use a deliberately malicious native/WASI plugin that reads host/sandbox files, floods RPC and requests unauthorized handles.
Subscription tests permit same-owner runtime token visibility but require zero other-owner/non-vendor/captured/Eilmer-held tokens, or an explicit safe capture refusal.
A copied-login/password-injection test must prove explicit consent and report intended exposure; it cannot be represented as protected-mode zero containment leaks.
An unreadable sink, missing host or untested required adapter makes evidence incomplete. Test permitted real operations too, so blocking everything cannot pass.
No architecture proves universal zero leakage under arbitrary host/provider compromise. The release claim is the declared boundary plus a fully passing public attack suite, with exceptions and limits visible.

## 16. Performance budget and evidence

All figures below are **engineering allocations**, not measured Lamp results. Linux acceptance remains create p50 <500 ms, RAM fork <1 s, and ≥97% native for compute, disk and network independently.
Reference path: local API, cached prepared template, headless Linux, reserved host capacity, private block storage and active mandatory policy. Report remote RTT/relay paths separately without subtracting them from observed caller latency.
Initial budget calculation uses 2 vCPU/1 GiB; certify 128 MiB, 1 GiB and 8 GiB with real full/partial dirty memory before claiming the supported range.

| Phase | Create allocation | Live fork allocation | Evidence or test obligation |
| --- | ---: | ---: | --- |
| Auth, idempotency, placement/reservation | 35 ms | 35 ms | Local cached verification plus durable control operation; instrument actual fsync/quorum costs |
| Guard, disk preparation / source freeze+flush | 45 ms | 90 ms | Metadata CoW [S17][S18]; dirty disk flush still measured, not free |
| Credential/identity preparation or removal | 25 ms | 65 ms | No vendor round trip in command-ready metric; unsafe capture is an error, not a fast result |
| VMM launch / immutable RAM+CPU/device capture | 25 ms | 430 ms | Firecracker typical process start ~12 ms; capture has no upstream subsecond guarantee [S01][S02] |
| Restore, reseed, guest barrier / parent resume | 130 ms | 140 ms | Firecracker ≤125 ms is init-only; private memory mapping avoids eager restore copies [S01][S02][S03] |
| Actual task command and independently observed marker | 35 ms | 40 ms | End-to-end marker, with RAM continuity/independent source/fork checks |
| Contingency | 80 ms | 100 ms | Scheduling, page faults, guard and filesystem variance |
| **Total** | **475 ms** | **900 ms** | Leaves 25 ms create and 100 ms fork headroom to hard thresholds |

Warm-pool assignment aims at 100–200 ms but is separately labeled; cold cached-template creation must also pass.
Full capture of 8 GiB in 430 ms needs roughly 19 GiB/s of effective capture bandwidth before extra work. This is a calculated requirement, not a measured laptop capability.
Use reserved volatile RAM-backed generations for running forks and background durable export when requested; suspension waits for durable disk state.
Incremental CoW capture reduces changed-byte cost but cannot bound an arbitrarily dirty workload. Release the latency claim only over a published, honestly bounded host/resource/workload envelope; no unqualified worst-case guarantee follows from these budgets.
Do not weaken the brief at merge: any target failure within that envelope blocks the engine milestone; narrowing the envelope to hide a failure is not a pass.
Firecracker's published >95% compute-only specification does **not** prove ≥97%, and its published I/O figures do not prove modern NVMe parity [S01].
Measure native versus guest compiler/CPU, sequential/random/fsync/metadata disk, and TCP throughput/latency independently with equal resources, data and paths.
The native baseline has no VMM and is not artificially throttled. Show direct, enforced policy, credential proxy and VPN overhead as separate rows, plus complete secured end-to-end results.
Use multiqueue/in-kernel paths or Cloud Hypervisor only if they retain mandatory enforcement and pass both latency and ≥97% gates. No VFIO bypass of guard to obtain a favorable network number.
If device emulation cannot reach 97%, optimize/reselect the engine before building the product surface; current sources provide no proof it already does.

| Platform/class | Initial planning expectation, not acceptance target | Main uncertainty |
| --- | --- | --- |
| M5 / Linux microVM | Cached create 0.2–1 s; fork/resume 0.5–3 s | Hardened HVF engine, dirty RAM, APFS and device state; downstream headlines use other endpoints [S05] |
| Windows / Linux microVM | Cached create 0.3–2 s; fork/resume 1–5 s | WHP capture, NTFS delta disks, filter path; no controlled published p50 |
| macOS / full-VM | Prepared restore 1–10 s; cold boot tens of seconds | Apple save constraints, guest startup, auxiliary/device state [S09] |
| Windows / full-VM | Prepared restore 1–15 s; cold boot tens of seconds | Standard checkpoint/export, storage and guest initialization [S11] |

These wide ranges are estimates to schedule experiments only. Set Mac/Windows product targets after real hardware measurements; no borrowed vendor boot headline is agent-ready evidence.

## 17. Testing and missing machines

Unit/property/fuzz tests cover authorization intersection, URL/DNS normalization, RPC framing, idempotency, state machines, budget arithmetic, manifest/path parsing and approval binding.
Integration tests use real VMMs, packet sinks, immutable storage generations, crash/restart persistence, real TLS and private browser/vault fixtures. Fake providers isolate failure cases, not isolation/performance acceptance.
E2E reproduces all five journeys in the draft requirements: signed-in Linux; mixed fleet/consoles; sub-agents/fork/winner; trusted and hostile CI; team/relay/approvals.
Use public disposable Flask/Hello World repositories, official Claude Code/Codex, public T3 Code/Herdr builds/interfaces and public third-party MCP servers with dedicated test accounts.
Never use Ozi, Apple Bridge, money-bridge or other owner projects, even as convenient fixtures.

| CI lane / machine | Required proof |
| --- | --- |
| Ubuntu laptop | Inventory CPU/RAM/disk/KVM first; real Linux x86_64 engine, policy, fork, escape and native-performance lanes |
| M5 Mac: 10 cores, 24 GB, macOS 26 | Linux microVM, two macOS full-VM cap, browser/vault, APFS, consoles, teleport and sleep/update tests |
| MacBook Air | Inventory architecture/OS first; independent sleep/wake/failover and constrained-RAM testing; no assumption it is Apple Silicon |
| Linux arm64 | Enroll a dedicated remote native arm64 Linux test host; a Linux guest on M5 does not certify a Linux host |
| Hosted Windows CI | Build, unit, static analysis, signed packaging and protocol tests; never treat this as hypervisor E2E |
| External Windows host | Rent disposable Azure Dv5 nested-virtualization test capacity with Windows Server 2025 and verified WHP/Hyper-V features; add a leased physical Windows 11 Pro host for desktop/WHP/AppContainer proof before release [S53] |

Windows provisioning is test infrastructure, never product cloud overflow. Specify image license, network lockdown, ephemeral credentials, cost quota, teardown and artifact collection in its checked-in runner definition.
Probe actual nested support on the selected SKU/security type; if unavailable fail the lane, not silently switch to WSL. Physical Windows evidence closes nested/desktop-specific gaps.
This proposal incurs no rental or account changes. Infrastructure provisioning becomes an explicit implementation milestone; missing Windows/arm64 evidence blocks release.
Every change runs the public reproducible benchmark/security suite through a serial hardware queue, keyed by exact commit; no change receives a green aggregate while a required lane is pending.
Initially keep results private with the repository until the engine demo gate; then publish the suite, redacted raw observations, versions, scripts, host facts and failures.
Pin all five competitors: Cloudroom, E2B Embed, Docker Sandboxes, microsandbox, SmolVM. Run actual supported products; mark missing/unsupported functions rather than inventing scores.
Use 20 declared warmups/200 creates, 100 running-source forks per RAM configuration and 10 paired native/guest throughput trials; randomize order, control power/thermals and report p50/p95/p99/max, errors and bootstrap intervals.
The fork target has no percentile in the brief: report every trial and maximum, with parent pause and first usable fork command. Timeouts/failures are never discarded.
Measure time-to-agent-ready through real permitted inference/tool use, density with active workloads, and reliability under sleep, partitions, storage exhaustion, guard crashes and rolling updates.
Run the full draft ATK-01–44 inventory plus discovered regressions on every supported host boundary; scanner/sink health and allowed-operation success are prerequisites to a zero-leak result.

## 18. Build plan: engine first, complete first release

| Milestone | Exit gate |
| --- | --- |
| E0: engine only, private repo | Lamp-format Linux template boots without Docker; real command create <500 ms p50; running-source memory fork <1 s; correctness and source continuation pass |
| E1: secured Linux datapath | Guard/root-bypass, API proxy, audit and compute/disk/network ≥97% pass together; choose Firecracker/CH from actual evidence; publish engine demo, then open repository |
| E2: complete host engines | Linux arm64, hardened Mac HVF/Windows WHP, macOS/Windows full-VM classes, requested devices, fallback labels and capture tests; obtain external Windows infrastructure |
| E3: templates/storage/lifecycle | Builder/save, kits/recipes, caches/volumes/artifacts, warm pool, durable suspend/resume and uniqueness pass |
| E4: credentials and sessions | Approved harness catalogue, all credential types/vaults/browser, capture refusal safety, settings, consoles/SSH, sub-agents and teleport pass |
| E5: fleet/access | Failover, pinning/placement, scoped keys, relay/Eilmer/local/OIDC, Tailscale, domains/cookies/certificates, offline/update tests pass |
| E6: full product proof | Every named CI/trigger plugin, dashboard, budgets/watchdog/notifications, generated four SDKs, all five journeys and every benchmark/attack lane pass |

Milestones are sequencing, not feature deferrals. E0 precedes implementation of other product components; all brief scope is required before first release.
Each milestone delivers reproducible evidence and a rollback path. Do not market target compliance before E1 or cross-platform security before all required host lanes pass.

## 19. First experiments and explicit fail thresholds

| Experiment | Pass threshold | Failure action |
| --- | --- | --- |
| Linux create/live fork including dirty RAM | Create p50 <500 ms; every admitted fork trial <1 s; parent and fork retain marker and diverge correctly | Stop product implementation; profile capture/fsync; test CH CoW path; no disk-only substitute |
| Secured native-speed floor | Every declared compute/disk/network measure ≥0.97 native; correct payloads and mandatory guard active | Optimize/reselect datapath; do not average away failure or throttle native |
| Credential stripping against hostile root | Zero captured subscription canaries, or safe refusal before any capture; normal managed adapter still resumes work | Block adapter capture; clean rebuild or vendor-supported process boundary |
| API/CLI response reflection | Zero API-key canaries in guest/output/log/capture across raw and encoded reflection/error corpus; ordinary calls work | Disable adapter until a bounded typed response path passes |
| Browser/password boundary | Zero password/cookie leaks through all offered controls; four vault routes and human takeover work | Restrict unsupported flow to private human control; never expose raw CDP as workaround |
| Root network bypass on three OSes | Zero packets at forbidden sinks, expected allowed traffic succeeds; guard/VPN crash never opens direct path | Block affected engine/security mode |
| Two-host loss/three-host partition | Survivor routes within proposed 10 s; no duplicate keyed allocation, quota overspend or unfenced writer | Fix permits/fencing/consensus before fleet release |
| Malicious relay and sibling origins | Zero plaintext recovery, unauthorized enrollment, cookie theft/tossing or cross-origin mutation/read | Block hosted access until independent key/issuance/origin checks pass |
| Certificate scale | Thousands of sandbox names require zero additional certs; global new issuance ≤40/7 days plus reserved product allowance | Queue new fleet HTTPS; obtain override/PSL approval without shared platform key |
| Windows and Mac capture matrix | Real Linux RAM fork/source continuation, durable suspend and host isolation pass; full-VM supported configs clearly reported | Patch/audit backend or reject unsupported device graph; missing core guest support blocks release |
| Hostile plugin/public PR | Zero host/neighbor secrets, no unauthorized action or trusted-cache mutation; cleanup leaves next job clean | Revoke plugin/backend, fix isolation; no process-only CI exception |

Open risks remain: upstream VMM security patches; downstream Mac/Windows fork maturity; full-dirty capture bandwidth; Windows licensing/access cost; GPU migration; subscription terms/refresh; exact arbitrary-root audit; literal opt-in zero-leak interpretation; hosted certificate growth.
Resolve the draft's Q11/Q19/Q22/Q29/Q30/Q33 with measured contracts. These are not grounds to silently remove fork, subscriptions, kits, budgets or audit.

## 20. Deliberate differences and coverage check

| Reference | Lamp's choice and reason |
| --- | --- |
| Cloudroom | Generic self-hosted fleet/API/CI rather than a desktop-centered agent product; no Eilmer-decryptable subscription store; hardware boundary and action-scoped runtime keys [S54] |
| E2B Embed | Own non-Docker template path, multi-OS fleet and ordinary SSH/consoles; keep Firecracker's boundary while adding explicit credential/browser and untrusted-CI policy [S55] |
| Docker Sandboxes | No Docker requirement to build/run Lamp; optional Docker only inside; adopt API sentinel principle but do not infer subscription proxy permission [S25] |
| microsandbox | Reuse only selected engine work where speed/security justify it; remove implicit TSI/host filesystem authority and add narrow plugins, fleet consensus and credential brokers [S04][S05] |
| SmolVM | Continuing source is required for fork; never call Windows frozen-source branching equivalent; isolate device/credential authority and use Lamp format rather than OCI input [S56] |

Coverage against [1-requirements.md](1-requirements.md): PRD/ENG → §§1–4,18; INT → §9; HST → §§3,11; SBX/TPL/LIF → §§3–4; STO → §5; NET → §6; PLG → §10; CRED/CON/KIT → §7; SES → §8; ACC/RLY → §12; CI → §13; OPS → §§12,14; BEN/QA → §§15–19.
All six supplied research reports informed this proposal. Binding brief/ADRs win over research; a primary-source claim is not a measured Lamp result.

## Primary sources

Checked for this design on 2026-11-08. Pin exact upstream commits during implementation; unversioned documentation may change.

[S01]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md
[S02]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md
[S03]: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/snapshot_restore.md
[S04]: https://github.com/libkrun/libkrun/blob/v1.19.8/README.md
[S05]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/docs/sandboxes/snapshots.mdx
[S06]: https://tailscale.com/kb/1244/tsnet
[S07]: https://github.com/actions/scaleset
[S08]: https://docs.wasmtime.dev/security.html
[S09]: https://developer.apple.com/videos/play/wwdc2023/10007/
[S10]: https://learn.microsoft.com/en-us/virtualization/api/hcs/overview
[S11]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/checkpoints
[S12]: https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/whp-samples
[S13]: https://docs.kernel.org/admin-guide/cgroup-v2.html
[S14]: https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects
[S15]: https://www.apple.com/legal/sla/docs/macOSTahoe.pdf
[S16]: https://gvisor.dev/docs/architecture_guide/platforms/
[S17]: https://btrfs.readthedocs.io/en/latest/Reflink.html
[S18]: https://docs.kernel.org/admin-guide/device-mapper/thin-provisioning.html
[S19]: https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/ToolsandAPIs/ToolsandAPIs.html
[S20]: https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning
[S21]: https://docs.kernel.org/virt/kvm/api.html
[S22]: https://github.com/firecracker-microvm/firecracker/blob/main/docs/snapshotting/random-for-clones.md
[S23]: https://developer.apple.com/documentation/virtualization/vzfilehandlenetworkdeviceattachment
[S24]: https://learn.microsoft.com/en-us/windows/win32/fwp/about-windows-filtering-platform
[S25]: https://docs.docker.com/ai/sandboxes/configuration/credentials/
[S26]: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html
[S27]: https://code.claude.com/docs/en/legal-and-compliance
[S28]: https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms
[S29]: https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli
[S30]: https://developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals
[S31]: https://modelcontextprotocol.io/specification/2026-07-28/basic/security_best_practices
[S32]: https://playwright.dev/docs/api/class-browsercontext
[S33]: https://developer.1password.com/docs/service-accounts/
[S34]: https://tailscale.com/kb/1581/workload-identity-federation
[S35]: https://github.com/NabuCasa/snitun
[S36]: https://docs.github.com/en/actions/reference/security/oidc
[S37]: https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Set-Cookie
[S38]: https://clerk.com/docs/guides/how-clerk-works/cookies
[S39]: https://letsencrypt.org/docs/rate-limits/
[S40]: https://publicsuffix.org/learn/
[S41]: https://docs.github.com/en/actions/reference/runners/self-hosted-runners
[S42]: https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-environment
[S43]: https://github.com/openai/codex-action
[S44]: https://docs.gitlab.com/runner/fleet_scaling/fleeting/
[S45]: https://buildkite.com/docs/agent/cli/reference/start
[S46]: https://www.jenkins.io/doc/developer/extensions/jenkins-core/
[S47]: https://forgejo.org/docs/latest/admin/actions/registration/
[S48]: https://circleci.com/docs/runner/machine-runner-3-configuration-reference/
[S49]: https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/linux-agent?view=azure-devops
[S50]: https://support.atlassian.com/bitbucket-cloud/docs/runners/
[S51]: https://woodpecker-ci.org/docs/administration/configuration/agent
[S52]: https://docs.github.com/en/actions/reference/security/secure-use
[S53]: https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dv5-series
[S54]: https://www.cloudroom.dev/security
[S55]: https://github.com/e2b-dev/runtime/blob/main/embed/README.md
[S56]: https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/branching.md
[S57]: https://bitwarden.com/help/cli/
[S58]: https://keepassxc.org/docs/KeePassXC_UserGuide
[S59]: https://proton.me/support/pass-access-tokens
[S60]: https://github.com/anthropics/claude-code-action/blob/main/docs/setup.md
[S61]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/crates/runtime/Cargo.toml
[S62]: https://docs.gitea.com/1.25/usage/actions/act-runner/
[S63]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/jailer.md
