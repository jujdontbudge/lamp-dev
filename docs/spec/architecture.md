# Lamp architecture

Status: feature design revised 2026-11-10 under the owner’s near-zero-test instruction. This document makes no implementation or measurement claim.
Audience: implementers, reviewers and the final requirements author.
The [brief](brief.md), [glossary](../../GLOSSARY.md) and [ADRs](../adr) bind this specification.
`[owner-confirmed Ox]`, `[owner-confirmed Ax]` and `[owner-confirmed Bx]` mark accepted owner decisions. Section 23 records the current release scope, owner-machine limits and macOS 26 minimum.
Complete feature tickets with one real end-to-end demonstration and almost no new tests. Release measurements are reported honestly and never block work, merge or release.

## 1. Overview

Lamp is open-source software installed on the caller's machines. It creates isolated sandboxes for agents, harnesses and CI.
A fleet has one address, one key namespace and any number of hosts. A single host is a fleet of one.
Linux guests run on Linux x86_64/arm64 and Apple Silicon macOS hosts. Native macOS guests run on Macs as a slower full-VM class.
Callers choose an inside harness or outside control through commands, files, browser and devices. API, CLI and MCP have the same authority and operations.
The dashboard and consoles expose every authorized session independently. Ordinary SSH is a baseline console.

Go owns control state and authorization. Rust owns engines, guest support and confinement-sensitive helpers.
Firecracker is the Linux reference. The msb_krun family provides supported per-host execution paths.
Control metadata is small and replicated. Disks, RAM, terminal bytes and credentials take separate paths.
A two-host survivor can coordinate new work using previously committed, disjoint local permits. Global authority changes still need quorum.
Coordination recovery does not recover work whose only disk or credential host is unavailable.

```text
Caller: API / lamp CLI / MCP / console / ordinary SSH
  | local IPC, Tailscale, owner HTTPS, or inner TLS/SSH through relay
  v
Any awake host: lampd (Go) ---- mTLS ---- other hosts' lampd
  | auth + operations + sessions + embedded control log + dashboard
  |                 |                         |
  |                 +-- owner broker (Go)     +-- confined plugin programs
  |                     keys/MCP/sign/fill        typed grants only
  +-- supervisor (Rust, local privileged resource operations)
  |     +-- engine worker (Rust, per sandbox) -- hypervisor -- guest helper
  |     +-- host network guard -- selected exit/private membership/port URL
  |     +-- assigned local disks and content store
  +-- optional browser sandbox; optional shared-files storage sandbox

Eilmer: account service + opaque relay + separate constrained DNS/ACME issuer
        No model credentials, transcripts, sandbox storage or inner TLS keys
```

The first release includes the whole brief: all host families, guest classes, named adapters, persistent storage, warm pools, fork, teleport, CI, triggers, budgets and notifications.
There is no automatic cloud overflow, required Kubernetes, Redis, Postgres, Docker runtime or hosted database.
Templates use Lamp's own format. Docker is available only as an optional kit inside a sandbox.
All Eilmer-hosted surfaces use `eilmer.si`. The relay forwards ciphertext and cannot grant fleet execution authority.

The owner’s Linux targets are create p50 under 500 ms, live memory fork under 1 s, and at least 97% of native computation, disk and network speed. Measure these once at release using B1’s standard sandbox and publish actual results (§19); no speed result blocks work or release.
Mac targets follow measurements. The zero-leak goal protects API keys, passwords and browser cookies; there is no prescribed attack suite.
Subscription runtime exposure remains the ADR 0004 exception. Same-person private capture retains sign-in [owner-confirmed A1]. Explicit exposures are reported separately [owner-confirmed O5].
Accepted decisions do not establish vendor permission or platform capability. Report actual support and limitations. Development and the dev relay use the owner’s three machines without new spending.

## 2. Key decisions

| Decision | Reason / consequence |
| --- | --- |
| Go control; Rust engines; bounded process RPC | Fits tsnet and GitHub's Go client without a Go heap in the VMM. Ownership and maintainability decide. [S01][S02] |
| Firecracker Linux reference; per-host supported engines | Documented snapshot/jailer lifecycle, not an assumed speed win. msb_krun needs a compatible, pinned dependency graph. [S03][S04][S05] |
| File-backed block paths first | Stock Firecracker vhost-user-block shares RAM and does not support snapshots. Do not combine incompatible fast paths. [S06] |
| Private memory state stays on trusted owner hosts | Same-owner fork/suspend keeps login [owner-confirmed A1]; exports, templates and sharing require clean state. |
| macOS disk-only creation is distinct from memory fork | Use suspend/resume and clearly labeled disk-only creation if supported APIs cannot fork RAM [owner-confirmed A2]. [S07] |
| One global commit model plus disjoint local permits | Safe two-host survivor work without a hidden third machine or a relay voter. Uncertain old work is never rerun. |
| Immutable content and single storage authority | Local CoW roots, explicit volume semantics and backup classes; no invented distributed filesystem. |
| Mandatory host network guard | Guest root cannot change routes, VPN keys or policy. Open public egress with blocked private/metadata ranges [owner-confirmed O3]. |
| Protected credentials use typed external brokers | Keys never enter the task sandbox; secrecy and permission to act are separate. Subscription TLS remains unintercepted. |
| Trusted engine implementation exception | Built-ins audited; third-party engine requires explicit owner trust [owner-confirmed A3]. Other plugins never receive RAM. |
| Honest audit and metering modes | Every command/connection; full requests where mediated. Exact proxy token accounting, best-effort opaque usage plus hard time cap [owner-confirmed A4]. |
| One schema and operation dictionary | Sections 11–12 fix names, fields, scopes, transports and lifecycle rather than leaving inconsistent examples. |
| Independent enrollment, relay and issuance authority | A routing grant cannot enroll a caller or obtain a fleet certificate. No second hosted domain. |
| Engine-first implementation | Keep the repository private until the working engine demo. Publish measurements once at release, without a timing prerequisite [owner-confirmed O6, amended 2026-11-10]. |

## 3. Host components and trust boundaries

### 3.1 Process inventory

These are implementation reservations for capacity accounting, not feature done conditions or performance targets. Adjust them to actual host needs; unused workers stay stopped.
The clean fleet-of-one baseline is `lampd` plus the supervisor, local store and embedded dashboard. Unused features stay stopped.

| Process / activation | Identity and authority | Initial capacity allocation |
| --- | --- | --- |
| `lampd`, always | Unprivileged service; API, SQLite WAL projection, control log, operation journal; no vault keys or guest RAM handles | 256 MiB initial service reservation |
| `lamp-supervisor`, always | Installed privileged local service; typed open/allocate/filter/kill operations only; no shell text or public listener | 64 MiB initial reservation |
| `lamp-engine`, per active sandbox | Unique worker identity, assigned hypervisor/disk/control handles and RAM only | Guest reservation plus measured overhead; accounted worker overhead |
| `lamp-guard`, when networking enabled | Separate confined host packet/policy service; no vault keys; worker cannot bypass it | 64 MiB base + bounded per-flow state; charged per host and sandbox |
| `lamp-broker`, per active owner | Separate owner identity; account-scoped proxy CA, provider/vault grants and MCP/signing authority | 64 MiB base; downstream plugins charged separately; dumps disabled |
| `lamp-access`, per enabled access method | Confined Go listener/tsnet state; forwards authenticated transport to common authorization | 64 MiB provisional per enabled base endpoint; measure per private member, never assume free nodes |
| Browser service, on request | Dedicated locked-down browser sandbox/profile; full automation API remains private | ≥512 MiB reservation plus engine overhead; guest/browser actual usage charged |
| Shared-files service, on first shared-files volume | Dedicated minimal Linux storage sandbox with Samba; only selected exports | ≥256 MiB RAM + engine cost; Linux-capable remote storage required if local virtualization absent |
| Plugin runner, on request | WASI or native service sandbox, explicit object grants only | Default 64 MiB/0.25 CPU, 16 concurrent RPCs; plugin may declare a higher admitted ceiling |

Installation and restart expose actual readiness and resource use without fixed latency or footprint targets.
Installer preflight distinguishes downloadable components, virtualization features and privileged setup. Signed packages use normal OS mechanisms; no runtime elevation.
Uninstall revokes host membership, kills workers, removes listeners/startup services and offers explicit retain/export/delete for owner data. It never silently deletes volumes.

### 3.2 Confinement contract

| Worker boundary | Required operating-system enforcement |
| --- | --- |
| Linux VMM | Matching Firecracker jailer where applicable; unique UID; private mount/PID/network namespaces; no_new_privs; deny-by-default seccomp; cgroup v2 memory/pids/CPU/I/O; LSM policy; no home/store traversal |
| Mac VMM | Signed hypervisor-entitled helper; separate identity; App Sandbox/profile; no home/keychain access; only allocated read/write file handles and local guard/control endpoints; deny ambient network and unrelated Mach/IPC services |
| Native VZ manager | Privileged framework-facing portion remains TCB; typed supervisor interface limits it to its assigned VM and disk set; guest helper is not host authority |
| Broker/browser/storage | Independent identity and grants; no engine-memory handles; deny unrelated owner roots and ambient inherited environment; cap requests and data |

Every profile ships a machine-readable inventory of files, handles, IPC, network endpoints, entitlements and syscalls.
Code signing alone is not confinement. libkrun treats guest and VMM as one security context; Lamp must confine the VMM externally [S05].
No guest filesystem is mounted by a privileged host parser. Builders and storage parsers use confined service sandboxes.
Failure of a guard, audit collector or required broker closes its path. One plugin crash must not crash `lampd` or unrelated sandboxes.
Host root, the hypervisor and an admitted malicious engine can read their assigned guest. Application roles cannot remove host-administrator trust.

## 4. Languages and protocol boundary

Go implements `lampd`, `lamp`, owner brokers, API/MCP/console adapters, CI controllers, access and the separate AGPL relay build.
First-release CLI binaries target Linux x86_64/arm64 and macOS arm64 only.
Rust implements engine workers, `lamp-supervisor`, `lamp-init`, guest helper, packet/confinement helpers and WASI runner.
Apple bindings use a thin generated Objective-C boundary in Rust. A small platform shim is allowed when the SDK demands it; no second control state machine.
The dashboard is static HTML/CSS/JavaScript served by hosts. No Node server is required.
OpenAPI 3.1 and shared JSON Schemas generate resource, event, CLI/MCP metadata and SDK types.
TypeScript/Python SDKs ship first; Go/Rust follow immediately [owner-confirmed O1].

Go is the only owner of operation, authorization, placement and quota state. Rust validates signed grants, generations, handle ownership and hard local limits independently.
Local protocol `lamp.worker/1` uses bounded generated Protobuf frames over peer-authenticated Unix sockets.
Control frames are ≤1 MiB; handle grants are typed and nontransferable. Every request carries `request_id`, `operation_id`, `deadline_ms`, `sandbox_id`, `generation`, `grant_id` and `policy_revision` where applicable.
No cgo/shared heap in a vCPU path. Pass handles once; RAM, disks, packets, PTY and video use separate bounded streams outside the replicated log.
The worker boundary preserves caller isolation, operation identity and quotas without an RPC timing target.

ADR 0003 reuse exceptions are security or likely speed: VMMs/hypervisors for the boundary; tsnet/TLS/SSH/OIDC/ACME for protocols; SQLite/Raft for durable authority; Wasmtime for hostile code; Samba for interoperable file locking; a maintained packet stack for Mac.
Lamp writes policy composition, scheduling, templates, sessions and the operation model. Existing availability alone is not a reuse reason.
Keep source and build inputs maintainable and preserve dependency notices and licenses. All Lamp host code, SDKs and shipped plugins are Apache-2.0; the separate relay server is AGPL-3.0.
Dependencies and guest software retain their licenses. Do not relicense Linux, Docker, proprietary OS media or a GPL VMM as Apache Lamp code.

## 5. Engines, guests, devices and selection

### 5.1 Supported hosts and guests

Minimum host policy is Linux 5.10+ on x86_64/arm64 and macOS 26+ on Apple Silicon [owner-confirmed O10, amended 2026-11-09]. Older macOS versions are outside first-release support.
Linux x86_64, Linux arm64 and Apple Silicon are first-release requirements. Host minimum, guest kernel and actual OS build are separate fields.

| Host / guest class | Engine implementation | Feature behavior |
| --- | --- | --- |
| Linux x86_64 / Linux microVM | Firecracker 1.17.0/KVM reference; msb_krun candidate; [Cloud Hypervisor 54](https://github.com/cloud-hypervisor/cloud-hypervisor/releases/tag/v54.0) candidate for eligible devices/I/O | Continuing-source RAM fork, durable suspend, guard and audit |
| Linux arm64 / Linux microVM | Firecracker/KVM and supported device adapters | Linux arm64 execution with memory fork, durable suspend, guard and audit; label nested measurements honestly |
| Apple Silicon macOS / Linux microVM | msb_krun 0.1.45 lineage over HVF; VZ candidate where Linux memory capture is supported | Full memory fork remains required; actual Mac page sizes, clocks, device graph and external confinement |
| Apple Silicon macOS / macOS full VM | Small VZ adapter; owner restore media [owner-confirmed O8] | Supported suspend/resume; if independent RAM fork is unavailable, only clearly labeled disk-only creation [owner-confirmed A2] |
| Nonvirtualizing Linux | gVisor systrap over Lamp-built root | Clearly labeled weaker `gvisor` boundary |
| Nonvirtualizing Mac | Restricted native process via App Sandbox | Weaker `process` boundary; native tasks only; Linux request routes to an eligible fleet host or fails |

Firecracker 1.17 documents full RAM/state capture and private lazy restore; disk capture is the integrator's job [S03]. Diff capture remains preview.
The msb_krun fork has private-memory and capture code. Upstream libkrun 1.19.8 lacks the same public capture contract [S04][S05].
The candidate implementation starts from msb_krun 0.1.45 and downstream commit `344cb2488c0475e644ed21152344a408973d4369`; pin the complete crate/kernel/device graph and checksum lock before use. This is not a claim that a build was reproduced.
Apple save requires compatible configuration and same Mac/user. It does not prove safe concurrent macOS RAM copies [S07]. A2 is the explicit fallback, never an inference that Linux-on-Mac may lose RAM fork [owner-confirmed A2].
Count running, suspended and retained resumable macOS allocations toward at most two per Mac. License evidence must cover actual host/guest versions and permitted use, not only the count [S10].
The cited SLA permits enumerated development/test/personal uses and restricts service-bureau/time-sharing uses. A public-service fleet cannot offer native macOS under an assumed two-instance exemption; that row needs appropriate license evidence before admission [S10].

### 5.2 Selection and resource rules

Filter by principal/credential-host trust, VM requirement, guest OS/architecture, CPU baseline, storage locality, requested devices, capture ability and pinned host.
Choose the fastest supported eligible engine using available performance information. A new benchmark is not required for selection. Placement also accounts for free reservations and cache locality.
No speed score can override security/device requirements. A pin is strict. `HOST_UNAVAILABLE`, `NO_CAPACITY` and `DEVICE_UNAVAILABLE` explain failures; no automatic cloud overflow.
Engine compatibility is keyed by digest, OS build, CPU family, filesystem, kernel, device graph and security policy. A missing benchmark certificate never makes supported features unavailable. Return `ENGINE_UNQUALIFIED` for an unsupported or unsafe configuration, not an unmeasured one.

Default Linux resources and B1 standard: `vcpus=2`, `memory_mib=4096`, `disk_gib=20`, `pids_max=4096`. Integer units; one GiB is 2^30 bytes. A supported native macOS Template publishes `minimum_resources` and `default_resources`; omitted resources use those defaults. Explicit undersized requests fail before allocation, never grow silently beyond caller limits. Native guest defaults are not Linux defaults.
Admit only supported engine maxima and available reservations. CPU/RAM overcommit is off by default; reserve VMM, guard, shared backing and device overhead separately.
Disk reserves private-growth liability plus metadata/inodes and a host reserve of max(2 GiB, 10% free capacity). Exceeding disk quota returns ENOSPC; no writes outside allocation.
OOM or hard time expiry stops the full sandbox/task tree with an explicit reason. Mac fixed vCPU admission is not advertised as a precise Linux-style CPU percentage quota.

Mandatory boot substrate: CPU, RAM, timer/interrupts, boot disk and private management/generation/entropy transport. A data NIC is added only for requested network access.
Public `devices[].type` is exactly `display`, `audio_input`, `audio_output`, `keyboard`, `mouse`, `gpu` or `disk`. USB passthrough is not in release one [owner decision 2026-11-09]; a request for it fails `INVALID_ARGUMENT`.
Display/audio/input use supported msb_krun device builds or VZ native devices. GPU uses supported virtual/mediated acceleration or exclusive IOMMU assignment; display alone is not GPU support.
No everyday desktop, microphone, clipboard, USB or host directory is inherited. Configuration-time devices suffice; no blanket hotplug claim.
Audit constructed hardware, not only request flags. Disable TSI/Unix forwarding and unwanted compiled devices; headless builds omit graphics code.
Unforkable GPU/device rows return `DEVICE_NOT_FORKABLE` before capture; report unsupported combinations honestly.
Weak boundaries forbid logins/protected connections unless the owner issues an explicit scoped override. Every verified public-repository CI job, including a trusted branch job, requires a fresh real VM. No Exposure can override this. Stranger jobs also get the stricter secret/network ceiling in §16.2.

## 6. Templates, create, fork, suspend and resume

### 6.1 Template format and build

Public format `lamp.template/v1` uses a TOML `.lamp` definition and a resolved canonical-JSON manifest with SHA-256 objects.
The manifest pins guest OS/architecture, root objects/filesystem, kernel/init, package locks, CPU baseline, device graph, build provenance and restore hooks.
Portable tool/disk content is separate from `acceleration_variants[]`, each keyed by engine/state ABI/kernel/device/CPU compatibility.
An acceleration variant is executable host-facing state, not an ordinary guest disk. Firecracker explicitly trusts snapshot inputs [S49]. Only a supervisor-authorized, confined Lamp capture/build worker may sign one with its enrolled producer key. Template ownership or an engine trust acknowledgement alone grants no producer authority.
The signed provenance binds producer key/host, owner, trust class, manifest digest and exact compatible engine/kernel/device/CPU configuration. Verify signature, current producer authorization, revocation, compatibility and every referenced object before any state parser runs. Arbitrary imports are guest disk/build inputs; regenerate acceleration in a locally authorized worker after a normal guarded boot.
An invalid hash or unauthorized producer quarantines the variant and dependent captures until rebuilt or independently verified.
Mutable template/recipe names resolve atomically to immutable digests. Existing sandboxes never follow a changed name.

```toml
format = "lamp.template/v1"
name = "python-tools"
guest_os = "linux"
arch = "x86_64"
seed_ref = "lamp-seed/debian-13@sha256:<verified-seed-digest>"
package_lock_blob_id = "bl_<uploaded-package-lock>"
[[steps]]
argv = ["apt-get", "install", "--yes", "git", "python3"]
[[steps]]
argv = ["python3", "-c", "print('tools-installed')"]
```

Angle-bracket values are fixture substitutions, never executable fake digests. The caller uploads a verified seed and signed repository/package lock before building.
The seed contains a verified distribution root, Lamp kernel/init and ext4 boot disk. Build steps run in a disposable build sandbox with a declared network policy.
No Dockerfile/OCI/Docker image ingestion, Docker runtime, registry conversion or host Docker daemon participates. The optional Docker kit installs Docker inside a guest, with no host socket.
Untrusted archives are unpacked inside the builder with bounded sizes/counts, confined relative paths, no traversal/device nodes/escaping symlinks and explicit ownership rules.
Never load a template-specified host library or disk-header backing path. Resolve all backing objects through authorized digest handles.
Build progress is an Operation. Verify hashes and boot probe before publishing. Failure retains diagnostics and cleans reservations; interrupted publication exposes no half template.
macOS templates are built by users on suitable own hosts from licensed media; Lamp does not redistribute them [owner-confirmed O8].

### 6.2 Creation and readiness

Create: authorize and persist identity → reserve → resolve/prepare template → private disk → guard/attachments → paused restore or pristine warm assignment → uniqueness barrier → settings/kits/auth/connections → command probe → ready.
`202 accepted` returns resource/operation IDs; it is never readiness. Requested setup failure leaves `failed` with its operation and cleanup state.
`command_started_at` requires an independent marker from a real command. `ready` additionally requires all requested kits, exits, auth and connections to pass checks.
`agent_ready_at` requires a valid signed-in harness turn/tool probe. Benchmark command-ready and agent-ready separately without omitting setup from observed create elapsed time.
Preparation/download/sign-in/private-member enrollment is visible; cold cases are not silently counted as cached creates.

Warm pools contain never-used, signed-out, connection-free sandboxes. Match owner/trust class, immutable recipe/template, resources, devices, engine ABI and policy revision.
Default minimum zero; configurable target/refill within resource budgets. Claim once, bind owner, then attach secrets. Never recycle a task-used sandbox into the pristine pool.
Pool miss uses prepared restore. Charge idle pool RAM, backing and refill CPU in density/cost results.

### 6.3 Capture contracts and contamination

| State / destination | Contract |
| --- | --- |
| API-key proxy task, no raw-secret exposure | Capture RAM/disk; real API keys are absent by construction. Taint propagates if a permitted response contains other sensitive state. |
| Same person's private fork/suspend on that person's trusted hosts | Preserve login and task RAM, including possible hidden subscription copies; encrypt and restrict capture custody [owner-confirmed A1]. No “stripped” claim. |
| Template, export of saved state beyond trusted owner hosts, sandbox shared/handed to another person | Must be stripped and start the harness fresh [owner-confirmed A1]. Arbitrary tainted RAM/disk cannot be exported after path deletion or byte scans. |
| macOS native disk-only request | `fork_mode=disk_only`, `preserves_memory=false`, boot fresh, clear UI label [owner-confirmed A2]. Never score as glossary Fork. |

Track `exposures[]` and `capture_trust`: `clean`, `owner_private`, `rebuild_required`. Taint covers RAM, private disk, journals, caches, artifacts and descendant captures.
Taint is monotonic across arbitrary task writes. A scan may detect contamination; it cannot prove its absence after hostile code has read a bearer.
For tainted template save, reconstruct installed tools in a clean builder from recorded package locks, installation recipes and verified nonsecret sources. Do not copy the tainted filesystem or boot it and scrub afterward.
For unrecorded custom tools/files, require explicit verified nonsecret reconstruction inputs. If absent, `CLEAN_REBUILD_REQUIRED` exposes the missing feature workflow; refusal passes safety only.
Clean task state may be saved directly after quiescence. Saving tools never absorbs attached volumes, caches, browser profiles, signing keys or credential mounts.
A1 does not permit retained credentials in transferable templates or shared sandboxes. Private encrypted RAM is not portable sanitized content [owner-confirmed A1].
Watch sharing of a credential-bearing sandbox/session is also a handoff boundary: create a clean fresh-harness descendant first, move the session if supported, then activate sharing [owner-confirmed A1]. No token-bearing raw snapshot/transcript is sent to another person.

### 6.4 Memory fork transaction

1. Persist keyed operation, source generation and disjoint reservation. Gate capture destination/custody and attachment policies before writing any capture. If raw passwords/cookies or a weak-boundary Exposure can enter the child, first call sandbox_prepare_fork to reserve an accepted child ID/generation without copying bytes. Obtain new child-bound Exposure approvals for the exact inherited secret set, then call sandbox_fork with prepared_sandbox_id and exposure_ids. Recheck the bound source revision/generation and complete exposure history, then consume that prepared identity/consent before capture; changed inputs need new preparation/consent. Absent approval, fail before copying bytes; expired preparation deletes the empty child and releases reservations. Subscription retention under A1 is separate and needs no new raw-secret consent.
2. Stop new command/input admission; freeze task filesystems; drain device writers; pause vCPUs at one RAM/CPU/device/disk generation.
3. Seal disk generation and full immutable RAM/state. Atomically install independent writable disk descendants. No inherited live KVM handles or process `fork()` shortcut.
4. Resume the parent; restore a new engine from read-only RAM backing with private writes. Keep backing immutable until the last reference is gone.
5. With external I/O closed, give the fork new sandbox/generation, machine/network identity, entropy, channel and broker handles; reauthorize all attachments.
6. Resume both copies with the captured work, independent private writes and usable command execution. Publish the fork result and source pause interval.

Start with full capture; optimize incremental/pre-copy only after correctness. Count capture copies, device DMA tracking, page faults and storage sync in the selected mode.
A volatile fork may use charged RAM-backed backing. `durability=local` waits for §7's stable-storage barriers; `replicated` waits for its two recoverable replica commits.
A volatile fork is not durable suspend. Capture preserves the complete configured memory state; recently written bytes do not describe the whole capture. Durability is reported according to the requested mode, without a fork timing prerequisite.
Forks reset inherited network flows; parent flows may continue when the engine permits. Reconnect remote operations through explicit hooks, not duplicated TCP/DB transactions.
Kernel reseed cannot reset arbitrary userspace PRNGs. Harness/kit hooks restart crypto contexts, cached TLS, UUID/PRNG state and check generation acknowledgement before I/O [S11]. Unsafe or unknown hooks keep the affected sandbox’s external I/O closed until identity is refreshed.
Wall time is corrected from the host; monotonic guest progression is preserved where supported. Deadlines and credential expiry use current host time and include elapsed suspension.
Shared-files/block/device attachments must choose `fork_policy=detach`, `share_read_only`, `share_write` or `capture`. Defaults are detach; capture requires a quiesced independent version; share-write is explicitly external shared state.

### 6.5 Idle, suspend and resume

Default idle grace is 300 s; suspended deletion after 604800 s. Both are visible overridable engineering defaults. CI defaults to `on_job_end=delete`; trusted jobs may select `retain_stopped` (§7.2).
A harness identified as waiting solely for the person's next message is not activity [owner-confirmed A5]. Builds, dev servers, background jobs, sleeping task processes and open task connections are activity [owner-confirmed A5].
Only `lamp-init`, guest helper, fixed audit/generation services and their private control channel are infrastructure. A task cannot add itself to the exemption list.
The waiting-harness exception covers its control wait, not tool processes or a general keepalive network connection [owner-confirmed A5]. Unknown accounting means active.
Session working/blocked/idle is separate from sandbox suspend eligibility. Observe both process inventory and external flows; hostile accounting cannot extend the hard task deadline.
Suspend durably commits memory/disk/manifest, tears down worker and releases vCPU/RAM allocation, pinned/shared private RAM and associated cache residency. Fixed host/template cache overhead is reported separately.
Same-person subscription state remains private and signed in [owner-confirmed A1]. Suspended native macOS state stays on compatible original Mac/user [S07].
Before the Task deadline, message, exec, SSH attach or allowed dev request triggers one coalesced resume operation. Bounded wake input waits until the identity/auth barrier passes.
After expiry the old Task stays stopped; automatic wake returns `TASK_AUTHORITY_EXPIRED` with the saved sandbox/session references. It never resets a budget. Dashboard and SSH show both saved-state retention and expired task authority, with a trusted link/CLI action to continue.
An authorized caller creates a new Task/Budget, then calls `sandbox_resume` with `continuation_task_id` and `expected_task_id`. This is an explicit new grant within caller capacity, not a revival of the stopped Task. A quorum-committed compare-and-swap fences the prior task/generation, rebinds preserved sandbox and sessions, increments generation/location epochs, consumes any fresh approvals and issues new handles before vCPUs/I/O resume. Old usage/outcomes remain on the prior Task. Concurrent continuations yield one winner and one revision conflict. Expired approvals and Exposure grants never revive. If saved RAM contains an intentionally exposed raw secret, fresh generation/task-bound exposure_ids must authorize that continuation before any restore parser, vCPU or I/O is released; revocation cannot scrub the existing bytes. A caller may extend an active Task before its deadline through the existing Task/Budget updates; stopped/expired Tasks cannot reactivate by update.
Resume uses saved work without rebuilding or asking the person to sign in again. Report actual readiness without an added resume-latency target.
A failed resume remains `failed` with intact saved state and repair options; never cold-start and call it resume.

## 7. Storage, durability and recovery

Private roots use immutable parents with independent writable descendants. Store manifests/references in local SQLite and checksummed content in owner-scoped files.
No writable raw disk is attached to independent kernels concurrently. Acknowledged application writes are durable only after the application's fsync reaches the stated storage commit point.

| Backing path | Storage behavior |
| --- | --- |
| Linux reflink XFS/btrfs | File-backed raw disk through Firecracker `path_on_host`; clone only at the paused/drained barrier; parent/fork use independent writable inodes |
| Linux ordinary ext4 | Support ordinary ext4 without reformatting. Copy or host-managed file/loop-backed dm-thin paths preserve flush/seal and independent writable heads. [S12] |
| APFS | Same-volume raw-disk `clonefile`; separately captured RAM. Drain devices and sync the filesystem before capture. [S13] |

Never use Firecracker's stock vhost-user block path for RAM fork; its documented snapshot incompatibility disqualifies that combination [S06].
Do not hide a custom extent-map backend behind a stock adapter. Describe its actual compatibility and durable behavior.
Here and below, “stable-storage barrier” means a storage barrier that persists acknowledged bytes across power loss, not just a successful write or process restart. Every backend must order sealed data → persisted ancestry/directory entries → durable manifest/journal commit → acknowledgement or RAM release.
Linux uses fsync/fdatasync plus parent-directory fsync [S52], with verified flush/FUA propagation through raw/loop/dm-thin/CoW layers to the device. macOS APFS uses `fcntl(F_FULLFSYNC)` on object and journal files, with correct rename/directory persistence ordering; ordinary fsync alone is insufficient [S50]. Persist created/renamed parent directories with the APFS directory-fsync path, then issue F_FULLFSYNC on an allocated file on that same device before publication. XNU documents that it drains the device queue and persists earlier fsynced writes [S50]. The filesystem/VFS/device must honor directory→device-barrier ordering before durable acknowledgement.
All safety SQLite connections use WAL and `synchronous=FULL`. On macOS also set and read back `fullfsync=ON` and `checkpoint_fullfsync=ON`, and ensure the VFS actually issues F_FULLFSYNC; the pragma is off by default and an ignored setting provides no durability [S51]. Apply the same barrier to Raft logs, receipts, permits, audit and encryption-key envelopes.
Filesystem/device combinations that cannot honor the required barriers cannot acknowledge durable operation. No filesystem checksum repairs missing acknowledged bytes.
Head rotation journal: reserve objects → stable-store sealed disk/state and key envelopes → persist both new head references → switch assigned handles while paused → stable-store manifest/journal commit → resume → release old reservations.
Recover by manifest generation: incomplete new heads are unreachable garbage; a committed generation retains every ancestor. Never modify an immutable ancestor.
Compaction creates new verified objects off-path, atomically redirects references, then collects only unreferenced objects.

| Product resource | Consistency, retention and authority |
| --- | --- |
| Cache | Owner + trust class + architecture + toolchain namespace; immutable generations, private writes, compare-and-swap publish. Quota/LRU eviction; pinned readers keep references. No untrusted-to-trusted compiler-cache promotion. |
| Volume `mode=shared_files` | One designated Samba storage service, SMB3 over authenticated private transport. Server orders rename/locks and fsync; close-to-open coherence with SMB leases. Case/symlink/permissions are explicit volume settings, not claimed identical to private Linux disk. |
| Volume `mode=exclusive_block` | One fenced writer or multiple read-only attachments. Mount/fsck and DB recovery belong to the owning guest/application. No transparent second writer. |
| Artifact | Immutable hashed chunks and atomic final manifest; resumable transfers, owner-only ACL, default 30-day retention. Hostile downloads never render on trusted control origins. |
| Template / saved Snapshot | Immutable verified manifest and reference graph. Templates retained until delete; private snapshots follow sandbox retention. Taint and custody from section 6 apply [owner-confirmed A1]. |

Start a shared-files service only when needed. Charge its extra VM/RAM and patching. On a host without VMs, use an explicitly selected reachable storage host; do not silently expose an unconfined local SMB server.
Private roots remain the default. A shared-volume measurement is labeled separately from a private-disk measurement.
Each mount receives a short-lived per-export credential and fixed route; it grants that volume only. After a 30 s disconnect deadline return I/O failure; do not replace missing data with an empty volume.
The server rejects expired attachment epochs. On storage-host loss there is no automatic live-volume promotion in release one; block new writers until the original is fenced and an explicit recovery is selected.
This avoids an unproved synchronous distributed file service. Replicated immutable checkpoints/backups below do not imply live shared-volume HA.

### 7.1 Recovery classes

| `durability` / object class | Acknowledgement and loss contract |
| --- | --- |
| `volatile` fork/private scratch | Identity/reference transaction persisted; RAM backing may be lost on reboot. API discloses RPO as all unpersisted state; no durable resume claim. |
| `local` snapshot/artifact/template; local volume | Ack only after object data, key envelopes, manifest and parent-directory commit pass the stable-storage barriers on its designated host. Survives process/host reboot with intact disk; sole-disk loss loses all writes since last backup. |
| `replicated` immutable snapshot/artifact/template | Hash-verify and stable-store all objects, key envelopes and manifest on two named authorized owner hosts; ack after both signed recoverability receipts. Loss of one host has RPO zero for acknowledged objects. No concurrent mutable-disk replication claim. |
| `backup` volume/sandbox export | Quiesce application or declare crash-consistent; create immutable version with `captured_at`/`consistency`; encrypt direct to owner-selected destination. RPO is last completed backup, not last write. |

Default resources are `local`; replicas are explicit and quota charged. `desired_replicas` and `achieved_replicas` are separate fields. A replica counts only after durable bytes and an authorized usable decryption path are present.
Backups include content refs, authority epoch, operation receipts/tombstones, manifest and audit roots; never model credentials in fleet metadata.
Credential-bearing private backups remain restricted to the same person's trusted hosts [owner-confirmed A1]. Off-host export requires clean reconstruction, fresh harness and no transferable credentials [owner-confirmed A1].
Restore verifies digests, ACLs, CPU/state ABI and custody; stages resources before activation; exposes `lost`, `unavailable` or `restore_required` when data is missing.
Restoring stale metadata cannot roll authority backward: reconcile with current quorum or recover under a new fleet authority epoch and reject all prior-epoch mutation keys. Do not reissue old effects.
Lost-host reconciliation inventories resource IDs/references and revokes old host certificates, leases and routes before recovery. A disk recovered later rejoins read-only until its generations reconcile.
Each immutable owner object gets a fresh random data key and authenticated encryption binding owner, object digest and format. The pinned crypto manifest specifies an audited AEAD/wrapping suite and unique nonce derivation per chunk; nonces are unique and tampered or wrong-owner ciphertext is rejected. Wrap that key separately for each explicitly authorized custody host and the owner's offline data-recovery public key. Host unwrapping keys use the local protected store; that store alone is not portable recovery (a host-bound protected store is not an independent recovery key). Fleet signing-root recovery and data-key recovery are distinct keys.
The owner exports the data-recovery private material through a trusted local secret-once ceremony, keeps it off the machines and can use it for recovery. Refusing recovery setup visibly marks objects without an independent host key path as nonrecoverable; it cannot produce a replicated/backup recovery acknowledgement. Credential-bearing state may wrap only to the same person's designated custody hosts/recovery key under A1, never a teammate merely holding an admin role.
Receipts include the envelope digest and a signed successful unwrap/authenticated-decrypt check. A clean replacement OS imports offline data-recovery material into a host-only CredentialSource through the trusted local setup view, then restores with the original host/store absent, verifies real bytes and replays no old effects. Restore accepts recovery_credential_source_id only as an owner-authorized broker handle; no recovery private key appears in API bodies, Operation results, relay storage or metadata. Native macOS save files may still require the original Mac/user; cryptographic recovery alone does not override VZ limits. Such a save cannot claim cross-host recoverability.
Rotation rewraps keys to current custody/recovery recipients and durably commits envelopes before retiring old ones. Removal revokes future unwrap grants; already copied keys/bytes cannot be recalled. Deletion removes all object references/envelopes/replicas under retention policy; disclose backup expiry before claiming cryptographic erasure.

### 7.2 CI output finalization

On job terminal/cancel: revoke job execution/network immediately → freeze task → collect from read-only disk/output spool → finalize verified artifact manifest → delete execution → deregister runner. Default `on_job_end=delete` removes the sandbox. An authorized trusted-job `retain_stopped` override may keep encrypted stopped state for its configured retention, with no live worker, runner registration, job token or network authority. A later inspection needs fresh Task authority. Stranger jobs require delete. Every next job still gets a fresh VM/root.
Collection has a 120 s default deadline and bounded retries. Failure produces `artifact.state=incomplete` with expected/present chunks and safe diagnostics; teardown still proceeds.
A dedicated collector may retain encrypted, inaccessible output blocks for 24 h to retry; it never retains a runnable compromised runner. All retained bytes count against quota.
Logs stream out during execution. After deletion, users can download finalized bytes and see missing output or cleanup errors.

## 8. Host-owned networking

A complete effective policy revision must be installed before task release. Bind packet identity to an assigned engine handle, never a guest header/MAC claim.
Linux: private TAP/network namespace → nftables/anti-spoof → guard/broker → selected exit. No host bridge or alternate interface.
Mac: private Ethernet transport → confined packet stack/guard → selected sockets/exit; deny VMM ambient networking. VZ uses file-handle attachment. Every path, including nested Docker, crosses the guard.
Disable/audit downstream TSI, Unix-socket forwarding, virtiofs host roots and implicit routing; source defaults alone are not security proof.

Default egress is public internet TCP/UDP, with private/LAN, loopback, link-local, metadata and fleet-control destinations blocked [owner-confirmed O3]. Every public-repository CI job is allowlist-only under brief §11; stranger jobs also obey §16.2.
Include IPv4 special-use ranges, IPv6 ULA/link-local, mapped IPv4, host routes and provider metadata VIPs. Normalize addresses before classification; track current IANA/provider lists in policy data.
A named approved Connection can open one private service endpoint. It does not disable the baseline private deny globally [owner-confirmed O3].
Policy composition is: immutable public-PR/host safety ceiling ∩ principal/recipe/task policy ∩ sandbox policy ∩ attachment scope. Explicit denies win within each layer.
Private Connection grants are narrow exceptions to the general private-range default, never to explicit safety denies. Enforce destination, port/protocol, subject, time and operation where mediated.

Two explicit filter modes avoid false claims:

| `network_policy.filter_mode` | Meaning |
| --- | --- |
| `transport` | IP/CIDR/protocol/port enforcement plus host DNS. Arbitrary public raw TCP/UDP is permitted by the default [owner-confirmed O3]. No claim to distinguish encrypted applications sharing an IP. |
| `strict_sites` | Exact DNS names or label-boundary wildcards; normalize IDNA/trailing dot/IP forms; enforce DNS answer, SNI, TLS certificate and HTTP authority/redirect agreement through the host gateway. Deny alternative DNS/DoH/DoT, QUIC/ECH, CONNECT tunneling or opaque protocols that defeat required inspection. |

Strict opaque subscription TLS uses approved vendor endpoint/IP controls and certificate-preserving forwarding; it is not full decrypted site/request inspection. Never intercept subscriptions just to close an audit gap.
Network changes use revision preconditions and atomic compiler/swap. Recheck and close newly denied existing flows. Guard/VPN/proxy failure drops, never switches to direct.
Host logs allow/deny DNS and connection decisions. Flow quotas and connection deadlines prevent idle sockets exhausting guard memory.

Network plugins supply direct, WireGuard/VPN, HTTP CONNECT/SOCKS or another host-side endpoint. Keys stay on hosts; policy is applied before the selected exit.
Resolve in the selected host/member DNS context and classify every candidate final IP. Bind the grant to that IP, port and protocol; send a numeric IP in SOCKS/CONNECT/UDP requests, preserving the approved name only as TLS SNI/HTTP authority. SOCKS domain-name addressing exists but is not permitted for an unchecked second resolution [S54]. No plugin-side DNS retry, alternate address, proxy redirect or private-range fallback may change that grant. Re-resolve/recheck at each new dial.
A private member resolves and dials within its own identity/context, using the checked numeric address and narrow approved private endpoint. Reject exits unable to preserve this contract. The remote exit operator is trusted to honor the specified dial target; a hostile remote proxy can lie even when sent an IP.
A selected exit yields the provider-observed source IP. Distinct public IPs require distinct leased/provider exits, not three labels behind the same NAT.
Sandbox links are directional Connection resources with source/destination IDs, generations, ports and protocol. Both ends validate them; shared owner/host/fleet gives no implied link.

### 8.1 Private-network membership

A tsnet instance is not a TAP/router [S01]. Give each `network_attachment.type=private_member` its own state directory, host-held keys and registered sandbox identity.
Guest TCP goes through the guard into that instance's `Dial`. UDP uses the pinned version's supported packet/datagram API; if absent, implement a bounded adapter before claiming UDP membership.
Tailnet inbound TCP listeners map only explicitly declared guest ports; UDP listeners use the corresponding datagram bridge. DNS resolves inside that member context and is rechecked by policy.
Return traffic uses the same member and transport binding. Never expose host listeners or advertise a LAN subnet route. Raw Ethernet/ICMP/multicast are unsupported on this endpoint mode and reported explicitly.
The peer sees the sandbox member IP/identity; mapped inbound/outbound TCP/UDP works without exposing the host or neighbors. Fork enrolls a new member; resume reauthorizes the old logical member.
Measure registration, propagation, memory, churn and DERP paths outside cached local engine diagnostics, but within any caller operation that waits for them.
Tailscale SaaS federation and services need configured tags, service approval, issuer/claims/scopes and feature registration [S01][S15]. Headscale support is described separately; `ControlURL` alone does not supply SaaS federation features.
No saved-secret fallback when keyless CI federation is unavailable. Local/owner-domain/relay auth still exists independently.

### 8.2 Dev-server URLs

`Port` binds one sandbox generation, TCP port, protocol, origin and expiry. Host ingress authenticates before untrusted app code.
Ports are private signed-in by default; shareable links expire, with default TTL 3600 s [owner-confirmed O4]. Public publication is an explicit scoped action [owner-confirmed O4].
Relay-hosted links/public ports still require an Eilmer account; subject_id can restrict the recipient further. An owner-domain route may explicitly allow an anonymous expiring port grant [owner-confirmed O4].
Exchange one-use link fragment for origin-local host-only session; strip grant/cookies/identity headers before app forwarding. Never send the link token in Referer, logs or upstream URLs.
Unpublish/revoke closes HTTP/2/WebSocket streams and wakes no further work. Port authority cannot reach guest management, SSH, another port or another generation.
Use unique never-reassigned content origins; section 15 supplies cookie/certificate rules. Suspended apps resume once with bounded queued requests.

## 9. Credentials, connections, settings and kits

### 9.1 API-key and CLI boundary

Owner brokers keep keys in an OS-backed or explicitly provisioned encrypted owner store; no silent plaintext fallback, dumps, environment/argv secrets or global trace capture.
The sandbox receives a syntactically valid sentinel plus a transport-bound Connection grant. The sentinel itself is useless outside that assigned channel.
For supported endpoints the host terminates guest TLS, validates the complete request/destination, strips guest auth, substitutes real headers and opens separately verified upstream TLS.
A per-owner interception CA private key stays in the broker. Install only its public root in OS/runtime/nested Docker trust stores. Never disable verification or inject a raw key for compatibility [S16].
Separate HTTP/1.1, HTTP/2 and WebSocket framing, streaming and owner pools. Reauthorize redirects, upgrades and retries. Prohibit smuggling, cross-owner connection reuse and credentials on redirects.
Typed adapters allow method/path/resource/body schemas and safe response shapes; block token export/minting, debug/reflection and arbitrary signed URL generation.
Known-value/encoding detection is defense in depth. Approved upstream credential recipients and their authentication protocol remain trusted; generic redaction cannot hide arbitrary transformed reflection.

| Connection type / operation | Supported contract and default exclusions |
| --- | --- |
| `cli` / `gh` | Repository issue/PR/read/write operations explicitly granted; Git HTTPS via proxy. Deny `auth token`, credential export and token creation. Repo-write is an action scope, never implied by sign-in. |
| `cli` / `stripe` | Pinned API operations with account/resource limits and streaming support. Deny key creation/export and unrestricted request URLs. |
| `cli` / `aws` | Dummy guest credentials; broker validates canonical service/region/action/resource/body and computes SigV4. Certify chunked streaming variants separately. No STS credential-return, presigned URL or arbitrary signing oracle by default. [S17] |
| `mcp` | Host gateway owns upstream discovery/PKCE/audience-bound OAuth and refresh; tool allowlist/approval. Stdio servers are confined owner processes. No inbound token passthrough. [S18] |
| `service` | Exact owner-machine endpoint; access `off`, `read_only`, `ask_first`, `full`. Read-only requires database read-only role or a semantic adapter. Raw TCP cannot promise read-only. |
| `sandbox_link` | Directed sandbox/port/protocol grant from section 8, no implicit network membership. |
| `git_sign` | Broker signs only approved repository/object digests; no private signing key or generic SSH-agent socket in sandbox. |

An inside MCP server receives a ≤5-minute, sandbox/generation/tool/resource-bound Lamp token where possible. A required limited upstream bearer is an explicit delegated-token class under the brief's exception, with audience, actions, expiry and taint recorded.
No owner-wide API key may be renamed “limited.” Unsupported export/signing variants fail closed and remain missing positive-compatibility rows, not blanket claims of complete CLI support.

### 9.2 Subscription custody and harness catalogue

Official unmodified harnesses use vendor-approved sign-in, only on hosts the person has explicitly designated for credential custody.
A team compute host is not implicitly trusted for that person's credentials. Placement fails visibly if no trusted host is available.
No Lamp-run service stores, refreshes or proxies model credentials. Host-to-host credential transfer is direct encrypted owner-authorized transport; never relay application plaintext.
Subscription TLS goes directly to approved vendor endpoints. Refresh follows that vendor's rules; cloned rotating refresh chains must not race. Independent permitted sessions or a serialized single refresh owner are required.
Same-person private fork/suspend retains sign-in [owner-confirmed A1], but A1 cannot authorize a vendor-forbidden refresh/token-copy scheme.

A subscription-bearing sandbox is vendor-only at the data network. General builds/downloads/Git/private services run in a separate token-free task sandbox.
Prefer a supported native harness tool hook that directs all task execution to that sandbox; do not expose a second arbitrary network route in the credential-bearing one.
The signed-in route supports useful coding while preserving vendor-only credential custody. Tool arguments and results cannot provide a second exfiltration path; separate sandboxes alone do not enforce that restriction.
Use host-mediated bounded tool operations; an unrestricted bridge that breaks vendor-only token confinement is not a supported credential route. A1 does not waive the egress rule.

The catalogue records exact `binary_digest`, `version`, supported guest/host, `auth_modes`, official permission URL/date, endpoints, trust-store configuration, refresh/capture/continuation/spawn hooks and positive/negative evidence.
The table describes integration routes, not completed implementation. Advertise actual platform and vendor support; no signed test manifest is required.

| Harness ID | Official route and transport behavior |
| --- | --- |
| `claude_code` | Native subscription sign-in/setup-token where authorized; API route via proxy. Preserve binary and built-in auth methods; hosting Commercial Terms and credential-custody conditions apply. NODE_EXTRA_CA_CERTS for streaming, tools and refresh. [S19] |
| `codex` | Native ChatGPT/device sign-in or registered OSS VM transfer; API route via broker. Stable VM host ID and VM-owned refresh. CODEX_CA_CERTIFICATE/SSL_CERT_FILE, HTTPS/WebSocket support. [S20] |
| `opencode` | Pinned provider API modes and explicitly approved provider subscription routes; configure CLI and background service trust and auth precedence. No resurrected Anthropic OAuth impersonation. [Network source](https://github.com/anomalyco/opencode/blob/dev/packages/web/src/content/docs/network.mdx). |
| `cursor_cli` | Official user/service API credential and documented native login; proxy/runtime CA, streaming and service-account scope must pass. [Configuration](https://cursor.com/docs/cli/reference/configuration). |
| `copilot_cli` | Official device login/eligible scoped token or BYOK; entitlement separate from Actions token. Use compatible native TLS/runtime and refresh behavior. [Official SDK proxy tests](https://github.com/github/copilot-sdk/commit/c9433c4c3e3e190945a7097c7da17bfdc4090813) establish a test surface, not every CLI build. |
| `amp` | Official service/API mode and only documented eligible plan route; proxy/CA/WebSocket and remote/local execution location described separately. [Settings](https://ampcode.com/docs/cli/settings). |
| `goose` | Direct provider API mode; ACP delegates auth/transport to its official harness. Report ACP continuation limitations honestly. [S21] |
| `gemini_cli` | Current API/Vertex/eligible organization route. Do not assume historical consumer Google OAuth still applies; verify dated vendor eligibility. [Configuration](https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md). |
| `pi` | Pinned provider/runtime API route and per-provider trust/streaming support; no universal subscription entitlement. [Proxy implementation](https://github.com/badlogic/pi-mono/blob/main/packages/ai/src/utils/node-http-proxy.ts). |
| `devin` | Official local CLI/service API route; OS CA/proxy support and local/cloud handoff described separately. No fabricated relocation of a hosted-only mode. [Troubleshooting](https://docs.devin.ai/cli/troubleshooting). |
| `droid` | Official Factory API and supported BYOK/native login routes; OS/runtime CA, streaming, refresh and continuation support. [Network/deployment](https://docs.factory.com/enterprise/network-and-deployment). |

### 9.3 Browser and vault kits

Default logged-in browser is external to task memory, preferably a locked-down browser sandbox with dedicated owner profile. Host browser mode uses an equally scoped controller, never the everyday default profile.
The agent gets `navigate`, `click`, `type`, `read`, `screenshot`, `download` within an origin/action adapter. No raw CDP, arbitrary evaluate, cookie/storage APIs, devtools, profile export, arbitrary extensions or filesystem URLs [S22].
Credential pages, secret-bearing downloads, password reveal and unknown post-login flows remain private human control until the adapter proves safe observations.
During fill/MFA/CAPTCHA, revoke agent input and suppress DOM, screenshots, network logs, clipboard and recordings. Human has an exclusive lease; resume only after clearing sensitive fields and returning to an approved page.
A trusted site may legitimately receive its password; it must not expose credentials back through supported observations. A hostile same-origin script or encoded reflection defeats a universal automatic redactor.
Protected browser support is site/action-specific [owner-confirmed B3]. Standard HTTPS forms use an approved exact origin/frame/form-action adapter for protected fill and submit; a password input type alone is insufficient. If secrecy is uncertain, including custom widgets or changed targets, use private human takeover. Return agent control only on a safe page; otherwise remain human-controlled. The agent must never receive the password.
Every browser network producer, including subresources, redirects, beacons, service workers, downloads, WebSockets, WebRTC and browser-native UDP/DNS, uses a task-bound guard. Its effective policy intersects the task safety ceiling/private grants, uses the same selected exit, and supplies the same audit, quotas and deadlines. No host-direct bypass is allowed. Partition profiles, storage, workers and connection pools by owner/task/effective policy and exit; do not share a context across conflicting policies. Host-browser mode must provide equivalent enforced network confinement or is rejected. An allowed page with a denied background request, then VPN death, must emit zero denied/direct packets [S55].

| Vault plugin ID | Host-only source and protected fill |
| --- | --- |
| `onepassword` | Documented local/service interface, scoped vault; secret retrieval stays inside trusted broker, never agent output. |
| `bitwarden` | Pinned CLI/SDK and scoped unlock; no master/session secret delivered to task. |
| `keepassxc` | Supported local database/browser/CLI path; isolated database handles and host-only retrieval. |
| `proton_pass` | Supported local/CLI/browser interface with documented rights; access-token availability is not by itself a safe-fill API. |

All four provide protected fill on approved HTTPS forms and private takeover where secrecy is uncertain. Changed or hostile targets cannot receive the password [S23].
Per-vault approval can be required for every use. `Approval` binds principal, task, sandbox generation, secret/connection reference, adapter-computed canonical action plan and its digest, destination, expiry and one-use nonce. The trusted view renders that immutable plan, including normalized arguments, exact secret/form identity or settings diff. It never trusts caller display text. Redacted secret values remain symbolic references; the enforcer retains the exact normalized private action and checks it before use.
Flow is request → inspect on trusted console/phone → approve/deny → compare digest → consume atomically → perform once. A changed target, timeout, revoked principal or replay performs no action.
Phone uses the same authenticated host-served approval view through enabled access. Ordinary SSH prints a short-lived private takeover link; it need not render a browser.
Inside browser with copied logins and per-secret password injection require separate authenticated Exposure grants, never inherited by recipe edits/sub-agents or a memory fork. Creation is `pending_approval` with no credential effect. `exposure_activate` atomically consumes approval and makes the grant active; `activated_at` stays absent until the actual credential effect is recorded. For an empty prepared-fork child, activation authorizes only its pending inherited-state capture; it does not inject a separate secret. The fork records activated_at when it first copies the authorized bytes. For a normal injection/profile it records the actual delivery. An uncertain delivery taints the target conservatively and remains an unknown Operation outcome, never a clean unused grant. Denied/expired approval delivers nothing. Revocation cannot undo copied bytes. They are flagged on dashboard/audit and excluded from zero unauthorized-leak scoring [owner-confirmed O5].
They remain tainted for transferable capture; stripping/rebuild rules still apply [owner-confirmed A1]. Public stranger CI cannot opt into them.

### 9.4 Settings

`SettingsSource` selects one-way source revisions for harness settings, skills/plugins/slash commands, CLAUDE.md/AGENTS.md, MCP list, git identity and signing references.
Copy filtered content; validate symlinks/paths/executable configuration. No writable mount to the source and no installation script runs on the host.
Sandbox edits produce a `SettingsChange` with base revision and path/content diff. Only the person with separate approve scope can apply selected paths; stale source revisions conflict.
Git signing keys remain behind `git_sign`; identity settings do not carry a private key.

## 10. Sessions, consoles, sub-agents and teleport

A Session is one harness's piece of work. A sandbox may contain many sessions; their IDs, parentage, status and output streams are distinct.
Session status is exactly `working`, `blocked` or `idle`, with `status_reason`, `observed_at` and `observation_state=fresh|stale|unknown`. Silence never proves success or idle.
Inside sessions supervise a harness in a sandbox. Outside sessions register the external harness identity and drive the same exec/files/browser surface. Outside model traffic is not silently claimed metered [owner-confirmed A4].
T3 Code, Herdr and third-party Console plugins discover one fleet machine, enumerate authorized sessions and implement watch/input/interrupt/resize/approval/takeover through common operations.
Use public console contracts and describe actual availability. Do not build special integrations with the owner’s projects.

One renewable `Lease.type=input` grants a writer; watchers have output only. Watch/type never implies shell, file, approval, browser or vault access.
Type permission is still power to make the credentialed harness act; sharing must disclose that authority. Same-sandbox sessions share files/processes/network and cannot enforce separate secret boundaries.
Sharing activates only after the required clean handoff for any credential-bearing sandbox [owner-confirmed A1]. Own-only team visibility covers lists, searches, events, errors and transcripts, not merely dashboard cards.

SSH terminates on a fleet host with a fleet SSH host CA and distinct host certificates. Enrolled callers pin the CA/fingerprint independently of the relay.
The standard command grammar is `sessions`, `attach <session_id>`, and `shell <sandbox_id>`; empty command opens an authorized session selector. No host shell, agent forwarding or arbitrary TCP forwarding by default.
Direct/Tailscale uses port 22. Relay assigns a fleet TCP port and forwards opaque SSH bytes; additional relay IPs supply port capacity. SSH has no HTTPS SNI [S24].

```sh
ssh -p 22418 person@ssh.f123.fleet.eilmer.si sessions
ssh -t -p 22418 person@ssh.f123.fleet.eilmer.si attach se_01
ssh -t -p 22418 person@ssh.f123.fleet.eilmer.si shell sb_01
```

These commands use ordinary OpenSSH and returned real IDs/port. Account-bound enrolled keys or short-lived host-issued SSH certificates map to the same scopes. A relay login alone is insufficient.
Reconnect preserves session/output cursor, PTY resize, stdout/stderr/exit and input lease. Disconnect leaves authorized work running until its task budget/authority expires.

### 10.1 Native sub-agents and git return

`session_spawn` chooses `sandbox_mode=own|shared`. Shared mode requires compatible trust and explicit disclosure of the common authority boundary.
Own mode freezes a consistent selected task-file tree with tracked/uncommitted/selected untracked files; ignored files require explicit selection and never implicitly include credential paths.
Record base commit/tree digest, file modes, binaries and selected paths. Non-git work gets a private temporary task repository. Transfer a git bundle plus selected patch; inspect returned session/sandbox IDs.
Return creates a `ChangeSet` of commits/bundle and diff. Apply chosen commits against the parent's current tree with revision precondition; conflicts leave files intact for resolution. Keeping a winner never blindly overwrites the parent.
Grants intersect parent, recipe, delegated actions and Task budget. Fork/spawn cannot reset allowance or inherit raw-secret exposure consent.
Each official harness adapter must intercept its native spawn hook or discover native same-sandbox sessions with stable IDs, separate output/input and descendant charging. One aggregate PTY does not provide separately visible sub-agent sessions.
A requested own-sandbox spawn that the native harness cannot route remains a compatibility gap. Do not silently run shared instead.

### 10.2 Teleport state machine

Teleport transfers stable Session identity and logical harness continuation; it does not promise arbitrary cross-OS process memory migration.
`teleport` Operation phases: `checkpoint` → `stage_destination` → `fence_source` → `commit_location` → `resume_destination` → `complete`.
Export adapter-native continuation, acknowledged message/tool ledger, transcript cursor, task-file ChangeSet, settings digests and outstanding safe tool outcomes. No provider secret is in the transfer envelope.
Destination is an enrolled laptop host (`location.kind=host`) or sandbox (`kind=sandbox`), with approved tools/auth prepared. Native laptop execution is host authority, never sandbox isolation. It additionally requires `native_execution_grant_id`: a separate owner-approved NativeExecutionGrant bound to destination host/OS user, allowed working paths and harness digest. `session.teleport` alone cannot authorize host files/processes. The destination person inspects the host-authority warning and exact action plan; the local supervisor checks that grant before any file staging or process start. Sandbox-only teleport keys are denied before host effects.
Before commit the source retains its location epoch and can resume on rollback. At commit a higher quorum-authorized location epoch fences source input, tools and broker grants before destination effects.
If source fencing is unconfirmed, wait for its short lease expiry before destination execution. After commit only destination can resume; recovery never guesses by running both.
Teleport needs quorum for cross-host exclusive location changes; a local survivor cannot invent a new global session lease from a permit.
Same-person sign-in is obtained locally/directly under vendor rules; cross-person move requires clean fresh-harness handoff [owner-confirmed A1].
Harness continuation preserves pending tool outcomes and interrupted moves. Goose ACP currently documents no session fork/resume [S21]; report the actual supported mode and limitations. A transcript and new prompt are not continuation.

## 11. Canonical resource and field dictionary

This section fixes the vocabulary for final requirements. No round-file names override it. API version is `/v1`; JSON fields are snake_case.
IDs are opaque prefix + random 128-bit value, never display-name authority. Names are owner-scoped; references resolve to immutable IDs/digests at admission.
All persistent resources have `id`, `kind`, `fleet_id`, `owner_id`, `revision` (uint64), `created_at`, `updated_at`, `labels` (bounded string map), `state` and `visibility` (`private|shared`, default private; sharing needs a Share).
`kind` is always the resource discriminator. `type` selects a resource subtype; nested `location.kind` is a separate location discriminator. A subtype never overwrites resource `kind`.
Server-derived fields are read-only. Optional references are absent, not overloaded strings. Timestamps are RFC3339 UTC; durations are integer milliseconds or explicitly `_seconds`; bytes use integer `_bytes`.
`state` enums are per resource below. `effective_policy_revision`, `durability`, `custody_host_ids` and `exposures` are present where state/security applies.

### 11.1 Resources

The exact `kind`, CLI noun, MCP stem, collection and fields are given below. A hyphenated CLI/URL word maps to the shown underscore stem, without aliases.
`R` actions are `get,list`; `C` adds `create`; `U` adds `update`; `D` adds `delete`. The Action column enumerates the complete standard set for each row plus extra actions in §12.

| Kind / CLI noun / MCP stem | Collection | Actions | Type-specific fields and state |
| --- | --- | --- | --- |
| `fleet` / `fleet` / `fleet` | `/fleets` | R,U | `name,mode(single_owner\|team\|public_service),authority_epoch,root_fingerprint,recovery_key_fingerprint,coordinator_host_id,coordination_state(healthy\|degraded\|unavailable),limits,update_policy` |
| `host` / `host` / `host` | `/hosts` | R,C,U,D | `name,os,os_version,arch,cpu_baseline,boot_id,clock_uncertainty_ms,roles[],access_route_ids[],capacity,reserved,engine_ids[],credential_owner_ids[],heartbeat_at,state(enrolling\|ready\|draining\|sleeping\|unavailable\|removed)` |
| `principal` / `principal` / `principal` | `/principals` | R,C,U,D | `issuer,subject,display_name,disabled`; immutable issuer/subject, never email-based account merge |
| `membership` / `membership` / `membership` | `/memberships` | R,C,U,D | `principal_id,role,admission_policy`; roles owner/admin/member/viewer [owner-confirmed O9] |
| `invitation` / `invitation` / `invitation` | `/invitations` | R,C,D | `target_kind(host\|principal),target_key_hash,role,expires_at,used_at,state(pending\|used\|revoked\|expired)`; secret returned only once |
| `identity_provider` / `identity-provider` / `identity_provider` | `/identity-providers` | R,C,U,D | `type(local\|oidc\|eilmer),issuer,client_id,discovery_url,allowed_audiences[],enabled`; secret references only |
| `key` / `key` / `key` | `/keys` | R,C,D | `principal_id,parent_key_id,scope,expires_at,last_used_at,state(active\|revoking\|revoked\|expired)`; create/rotate returns `secret` once |
| `grant` / `grant` / `grant` | `/grants` | R,D | `parent_grant_id,principal_id,task_id,sandbox_id,generation,scope,expires_at,policy_revision,revocation_state` |
| `engine` / `engine` / `engine` | `/engines` | R | `plugin_id,digest,host_id,guest_os,arch,devices[],boundary,state_abi,certification(qualified\|unqualified\|revoked),evidence_ref,measurements` |
| `harness` / `harness` / `harness` | `/harnesses` | R | `plugin_id,version,binary_digest,guest_support[],auth_modes[],permission_sources[],hooks,certification,evidence_ref` |
| `kit` / `kit` / `kit` | `/kits` | R,C,U,D | `name,digest,plugin_id,install_plan_blob_id,device_requirements[],state_schema,tools[]`; resolved versions immutable |
| `template` / `template` / `template` | `/templates` | R,C,U,D | `name,digest,definition_blob_id,guest_os,arch,minimum_resources,default_resources,root_objects[],kernel_digest,init_digest,acceleration_variants[],provenance,key_envelopes[],desired_replicas,achieved_replicas,replica_receipts[],recovery_state,state(building\|ready\|failed\|deleted)` |
| `recipe` / `recipe` / `recipe` | `/recipes` | R,C,U,D | `name,digest,template_id,harness_id,auth_mode,kit_ids[],devices[],resources,network_policy_id,network_plans[],connection_ids[],settings_source_ids[],lifecycle,budget_id`; update publishes immutable version |
| `task` / `task` / `task` | `/tasks` | R,C,U,D | `parent_task_id,previous_task_id,budget_id,session_ids[],sandbox_ids[],deadline_at,state(active\|blocked\|stopping\|stopped\|completed)` |
| `budget` / `budget` / `budget` | `/budgets` | R,C,U,D | `wall_time_ms,token_limit,token_mode(exact\|best_effort\|unavailable),token_home_host_id,reserved_tokens,used_tokens,resource_limits,watchdog,deadline_at` [owner-confirmed A4] |
| `sandbox` / `sandbox` / `sandbox` | `/sandboxes` | R,C,U,D | `recipe_id,template_id,host_id,engine_id,guest_os,arch,boundary(vm\|gvisor\|process),resources,devices[],task_id,parent_sandbox_id,generation,fork_preparation,harness_id,auth_mode,kit_ids[],connection_ids[],settings_source_ids[],network_policy_id,network_plans[],network_attachment_ids[],attachment_ids[],lifecycle,suspend_eligibility,readiness,capture_trust,snapshot_id,warm_pool_id` |
| `snapshot` / `snapshot` / `snapshot` | `/snapshots` | R,C,D | `sandbox_id,generation,manifest_digest,preserves_memory,capture_trust,custody_host_ids[],durability,desired_replicas,achieved_replicas,replica_receipts[],key_envelopes[],capture_provenance,recovery_state,compatibility,consistency,state(capturing\|ready\|failed\|deleted)` [owner-confirmed A1] |
| `warm_pool` / `warm-pool` / `warm_pool` | `/warm-pools` | R,C,U,D | `recipe_id,target_size,max_size,ready_count,reserved_resources,match_digest,state(active\|paused\|failed)` |
| `exec` / `exec` / `exec` | `/execs` | R,C,D | `sandbox_id,session_id,task_id,argv[],cwd,env,stdin_blob_id,pty,started_at,exit_code,signal,output_cursor,state(accepted\|running\|exited\|failed\|outcome_unknown)` |
| `session` / `session` / `session` | `/sessions` | R,C,U,D | `sandbox_id,harness_id,auth_mode,task_id,parent_session_id,agent_mode(inside\|outside),status,status_reason,observation_state,observed_at,location,location_epoch,output_cursor,continuation_ref,state(starting\|active\|moving\|ended\|failed)` |
| `message` / `message` / `message` | `/messages` | R,C | `session_id,location_epoch,text,attachment_blob_ids[],delivered_at,state(accepted\|delivered\|outcome_unknown)` |
| `lease` / `lease` / `lease` | `/leases` | R,C,D | `type(input\|browser_control\|volume_writer),resource_id,holder_id,generation,location_epoch,boot_id,expires_at,state(active\|released\|expired\|revoked)` |
| `share` / `share` / `share` | `/shares` | R,C,U,D | `resource_kind,resource_id,subject_id,actions[],expires_at,state(pending_clean_handoff\|active\|revoking\|revoked\|expired)` [owner-confirmed A1] |
| `change_set` / `change-set` / `change_set` | `/change-sets` | R,C,D | `source_session_id,target_session_id,base_tree_digest,bundle_blob_id,patch_blob_id,selected_paths[],commit_ids[],conflicts[],state(proposed\|applied\|conflicted\|rejected)` |
| `network_policy` / `network-policy` / `network_policy` | `/network-policies` | R,C,U,D | `filter_mode,default_egress,allow[],deny[],dns,flow_limits`; rule: `host_pattern,cidr,protocol,ports[]` [owner-confirmed O3] |
| `network_attachment` / `network-attachment` / `network_attachment` | `/network-attachments` | R,C,U,D | `sandbox_id,generation,type(exit\|private_member),plugin_id,config_ref,identity,port_maps[],observed_exit_ip,state(preparing\|ready\|failed\|revoked)` |
| `port` / `port` / `port` | `/ports` | R,C,U,D | `sandbox_id,generation,port,protocol(http\|https),origin,access(private\|link\|public),expires_at,state(publishing\|ready\|revoked\|expired)` [owner-confirmed O4] |
| `credential_source` / `credential-source` / `credential_source` | `/credential-sources` | R,C,U,D | `plugin_id,provider,account_ref,vault_ref,custody_host_ids[],approval_policy,state(locked\|ready\|failed\|revoked)`; never secret value |
| `connection` / `connection` / `connection` | `/connections` | R,C,U,D | `type,credential_source_id,endpoint,adapter_id,allowed_operations[],access,resource_policy,expires_at,approval_policy,state(preparing\|ready\|failed\|revoked)` |
| `attachment` / `attachment` / `attachment` | `/attachments` | R,C,U,D | `sandbox_id,generation,resource_kind(connection\|volume\|cache\|kit),resource_id,mount_path,mode(read_only\|read_write),fork_policy,lease_id,state(attaching\|ready\|failed\|detached)` |
| `browser` / `browser` / `browser` | `/browsers` | R,C,U,D | `task_id,sandbox_id,kit_id,profile_ref,adapter_id,network_policy_id,network_attachment_ids[],effective_policy_revision,allowed_origins[],control_lease_id,exposure_id,state(starting\|agent_control\|human_control\|blocked\|closed),mode(external\|copied),warning` |
| `approval` / `approval` / `approval` | `/approvals` | R,C,D | `principal_id,task_id,sandbox_id,generation,resource_ref,action_plan,action_digest,destination,expires_at,consumed_at,state(pending\|approved\|denied\|consumed\|expired\|revoked)` |
| `exposure` / `exposure` / `exposure` | `/exposures` | R,C,D | `sandbox_id,generation,secret_ref,type(password_injection\|browser_logins\|weak_boundary),destination,expires_at,approved_by,approval_id,activated_at,state(pending_approval\|active\|revoking\|revoked\|expired)` [owner-confirmed O5] |
| `settings_source` / `settings-source` / `settings_source` | `/settings-sources` | R,C,U,D | `host_id,source_ref,path_allowlist[],categories[],content_digest,signing_connection_id` |
| `settings_change` / `settings-change` / `settings_change` | `/settings-changes` | R,C,D | `sandbox_id,settings_source_id,base_revision,diff_blob_id,selected_paths[],approval_id,state(proposed\|applied\|conflicted\|rejected)` |
| `cache` / `cache` / `cache` | `/caches` | R,C,U,D | `name,trust_class,arch,toolchain_digest,generation_digest,quota_bytes,used_bytes,retention_seconds` |
| `volume` / `volume` / `volume` | `/volumes` | R,C,U,D | `name,mode(shared_files\|exclusive_block),storage_host_id,quota_bytes,used_bytes,filesystem_options,attachment_ids[],backup_id,recovery_state,state(ready\|unavailable\|recovering\|deleted)` |
| `blob` / `blob` / `blob` | `/blobs` | R,C,D | `sha256,size_bytes,media_type,uploaded_ranges[],expires_at,state(uploading\|ready\|failed\|deleted)`; upload scratch expires after 24 h |
| `artifact` / `artifact` / `artifact` | `/artifacts` | R,C,U,D | `job_id,task_id,manifest_blob_id,download_blob_id,expected_bytes,present_bytes,desired_replicas,achieved_replicas,replica_receipts[],recovery_state,key_envelopes[],expires_at,state(collecting\|complete\|incomplete\|deleted)` |
| `backup` / `backup` / `backup` | `/backups` | R,C,D | `resource_kind,resource_id,target_host_ids[],captured_at,consistency,manifest_digest,authority_epoch,desired_replicas,achieved_replicas,receipts[],key_envelopes[],recovery_state,state(capturing\|ready\|failed\|deleted)` |
| `plugin` / `plugin` / `plugin` | `/plugins` | R,C,U,D | `type,publisher,version,digest,package_blob_id,manifest_blob_id,adapter_ids[],protocol_version,trust_class,permissions,limits,config_ref,state(installed\|enabled\|disabled\|failed\|revoked)` |
| `job` / `job` / `job` | `/jobs` | R,C,D | `plugin_id,external_id,source_identity,repository_visibility,job_token,trust_class,recipe_id,sandbox_id,task_id,artifact_ids[],runner_ref,result,state(queued\|preparing\|running\|collecting\|completed\|failed\|canceled\|cleanup_failed)` |
| `trigger` / `trigger` / `trigger` | `/triggers` | R,C,U,D | `plugin_id,source_ref,principal_id,recipe_id,verification_policy,replay_window_seconds,enabled` |
| `schedule` / `schedule` / `schedule` | `/schedules` | R,C,U,D | `principal_id,recipe_id,cron,timezone,dst_policy,missed_policy,overlap_policy,next_run_at,enabled` |
| `notification_sink` / `notification-sink` / `notification_sink` | `/notification-sinks` | R,C,U,D | `plugin_id,destination_ref,event_types[],enabled`; destination itself requires grant |
| `notification` / `notification` / `notification` | `/notifications` | R | `sink_id,task_id,event_id,attempt_count,next_attempt_at,state(pending\|delivered\|failed\|acknowledged)` |
| `access_route` / `access-route` / `access_route` | `/access-routes` | R,C,U,D | `plugin_id,type(local\|tailscale\|relay\|owner_https\|cloudflare),address,ssh_host,ssh_port,trust_fingerprint,config_ref,state(preparing\|ready\|failed\|disabled)` |
| `auth_flow` / `auth-flow` / `auth_flow` | `/auth-flows` | R,C,D | `identity_provider_id,connection_id,purpose,challenge,proof_key_thumbprint,source_identity,authorization_url,expires_at,state(pending\|completed\|failed\|expired)`; callbacks carry code, never model secret |
| `update` / `update` / `update` | `/updates` | R,C,D | `release_digest,metadata_versions,security_revocations[],migration,host_ids[],current_host_id,compatibility,rollback_ref,state(pending\|draining\|applying\|checking\|completed\|failed\|rolled_back)` |
| `telemetry` / `telemetry` / `telemetry` | `/telemetry` singleton | get,update | `enabled,consent_revision,counts_enabled,timings_enabled,retention_days=90` [owner-confirmed O7] |
| `configuration` / `configuration` / `configuration` | `/configurations` | R,C,D | `name,digest,plugin_id,adapter_id,schema_digest,document,credential_source_refs[],state(validating\|ready\|failed\|deleted)`; immutable validated nonsecret configuration |
| `adapter` / `adapter` / `adapter` | `/adapters` | R | `plugin_id,digest,operation_schemas,config_schema,result_schemas,compatibility,certification,evidence_ref`; immutable published JSON Schemas |
| `native_execution_grant` / `native-execution-grant` / `native_execution_grant` | `/native-execution-grants` | R,C,D | `principal_id,host_id,user,working_paths[],harness_digest,action_digest,approval_id,expires_at,state(active\|revoking\|revoked\|expired)`; destination owner's explicit host authority |
| `operation` / `operation` / `operation` | `/operations` | R | `operation_name,principal_id,request_hash,resource_ids[],phase,progress,state,accepted_at,completed_at,outcome,error,cleanup_state,cancel_effect,revocation_status` |
| `event` / `event` / `event` | `/events` | list | `event_id,host_id,host_epoch,sequence,resource_kind,resource_id,resource_revision,location_epoch,operation_id,type,data,occurred_at,cursor` |
| `audit_record` / `audit-record` / `audit_record` | `/audit-records` | R | `host_id,task_id,sandbox_id,generation,session_id,principal_id,operation_id,sequence,policy_revision,type,visibility,decision,destination,bytes_sent,bytes_received,started_at,ended_at,duration_ms,details,previous_hash,signature` |

`resources` is `{vcpus,memory_mib,disk_gib,pids_max}`. `devices[]` is `{id,type,model,resource_ref,options}` with the types in §5.
`lifecycle` is `{idle_action:suspend|delete|keep,idle_grace_seconds,suspended_retention_seconds,expires_at,on_job_end:delete|retain_stopped}`. `on_job_end` defaults to delete and may be overridden only for trusted jobs (§7.2). `readiness` is `{command_started_at,agent_ready_at,checks[]}`; each check has `name,state,error`.
`location` is `{kind:host|sandbox,host_id,sandbox_id}`. `scope` is `{actions[],sandbox_ids[],recipe_ids[],host_ids[],resource_ids[],limits,expires_at}`; omitted selector means the issuer's authorized set, never global public authority.
`scope.resource_ids[]` contains `{kind,id}` references; the named sandbox/recipe/host arrays contain IDs. Supplied selector fields intersect; alternatives within one array are a union. An empty supplied array authorizes nothing.
`limits` names `vcpus,memory_mib,disk_gib,concurrency,wall_time_ms,token_limit,retained_bytes,control_records`. A delegated limit can only narrow.
`sandbox.state` is `accepted|preparing|ready|suspending|suspended|resuming|deleting|deleted|failed|unavailable`; never use `running` as a synonym for ready.
`operation.state` is `accepted|preparing|waiting|running|canceling|succeeded|failed|canceled|outcome_unknown`; `outcome` includes committed effects even after cancellation.
Immutable version updates change the owner's name pointer with compare-and-swap; `digest` objects remain readable by their existing references.

### 11.2 Field schemas and input rules

The tables are the canonical wire vocabulary. OpenAPI must expand these types without renaming fields; admission rejects unknown fields and invalid enum combinations. No unvalidated scope or engine configuration bypass is allowed.
Standard read-only fields are common IDs/revisions/timestamps/state plus actual placement, digests/provenance, observed usage/status/readiness, cursors, receipts, outcomes and generated URLs. A create request may supply owner_id only with explicit delegation; otherwise owner is the authenticated Principal.
All get/update/delete/extra MCP inputs name the addressed resource `<stem>_id`; HTTP puts it in the path and CLI takes the positional ID. Referenced IDs retain their named fields. Singleton telemetry has no ID parameter.
Unlisted state enums use `active|disabled|deleted`; immutable ready objects use `ready|deleted`. `audit_record.visibility` is the audit granularity enum in §17.1, not the content-sharing field; audit ACL remains owner-scoped. Event/Frame/StreamDescriptor are transport records, not mutable CRUD objects.

| Nested field | Exact shape / defaults |
| --- | --- |
| `exposures[]` | All Exposure IDs that authorized readable material/weak-boundary access, including revoked/expired grants after delivery. This history is monotonic across writes/captures; current grant state is read separately and revocation cannot erase taint. `capture_trust=clean\|owner_private\|rebuild_required` [owner-confirmed A1] |
| `capacity`, `reserved` | `{vcpus,memory_mib,disk_gib,pids_max,retained_bytes,control_records}`; actual host totals and admitted reservations are separate |
| `readiness.checks[]` | `{name,state:pending\|running\|passed\|failed\|skipped,error}`; names `template,engine,identity,network,settings,kits,auth,connections,command,agent`; unrequested setup is skipped explicitly; requested setup cannot be skipped |
| `host.roles[]`, `connection.type`, `connection.access` | Roles `control,compute,storage,credential`; types `cli,mcp,service,sandbox_link,git_sign`; access `off,read_only,ask_first,full`, narrowed by allowed operations and semantic enforcement |
| `resource_ref`, `plugin.type` | Reference `{kind,id}`; plugin type `engine,network,credential_source,harness,kit,ci,trigger,console,access,notification_sink`; credential_source includes vault plugins |
| `auth_mode`, `approval_policy`, `fork_policy` | Auth `none,api_key,subscription,delegated`; approval `{mode:never\|each_use,approver_ids[],ttl_seconds}` (never means no per-use prompt, not extra authority); fork `detach,share_read_only,share_write,capture` |
| `network_plans[]` | `{type:exit\|private_member,plugin_id,config_ref,port_maps[]}`; immutable recipe plans resolve to actual NetworkAttachments before readiness; at most one selected exit |
| `allowed_operations[]`, `resource_policy`, `arguments` | Adapter-published JSON Schema referenced by immutable adapter digest; no unvalidated generic command/signing method |
| `network_policy.allow[]`, `deny[]` | `{host_pattern,cidr,protocol:tcp\|udp,ports[]}`; ports are integer 1–65535, name/IP selectors must agree, omitted selector is unconstrained only within the parent ceiling |
| `network_policy.default_egress`, `dns`, `flow_limits` | Egress `public\|deny`; DNS `{resolver_mode:host\|member,strict_answers}`; flow limits `{max_concurrent,max_new_per_second,idle_timeout_seconds}`; all remain below the guard/parent ceiling [owner-confirmed O3] |
| `port_maps[]` | `{protocol:tcp\|udp,member_port,guest_port}`; exact per-member mapping, no whole-host/subnet listener |
| `attachment_overrides[]` | `{attachment_id,fork_policy}`; `consistency=application\|crash`, `durability=volatile\|local\|replicated\|backup` with legal resource combinations from §7.1 |
| `location` | `kind=host` requires host_id only; `kind=sandbox` requires host_id+sandbox_id. `scope.actions[]` contains the exact §12 operation-scope grammar |
| `watchdog`, `update_policy` | `{warn_after_ms,block_after_ms,repeated_action_limit,on_stuck:ask\|stop}`; `{automatic:false,max_unavailable:1,channel,drain_timeout_ms}` |
| `hooks`, `compatibility`, `acceleration_variants[]` | Versioned schemas with hook kind/digest and required generation acknowledgement; `{engine_digest,state_abi,guest_kernel_digest,cpu_baseline,device_graph_digest,host_os_build}` compatibility key |
| `operation.outcome`, `progress`, `cleanup_state`, `cancel_effect` | `{result,committed_effects[],unknown_effects[]}`; `{completed,total,unit}`; cleanup `not_needed,pending,running,complete,failed`; cancel effect `none,prevented,stopped_remaining,outcome_unknown`; effect `{resource_ref,action,state}` |
| `audit_record.destination`, `decision`, `details` | Destination `{hostname,ip,port,protocol}`; decision `allow,deny,error`; details is the discriminated shape below. Audit visibility is `mediated,process,request,opaque,control`; record state is immutable `ready` |

Additional canonical nested schemas:

| Field | Shape and enforcement |
| --- | --- |
| `suspend_eligibility` | Read-only `{eligible:boolean,reasons:string[],observed_at,observation_state:fresh\|stale\|unknown}`. Reasons are `session_activity,task_process,task_connection,harness_wait_unqualified,accounting_unknown`; empty only for fresh eligible observations. Stale/unknown is ineligible. |
| `recovery_state` | Read-only `available\|unavailable\|lost\|restore_required`; independent of capture/artifact lifecycle state. `desired_replicas`, `achieved_replicas` are uint32 host counts; local defaults to desired_replicas=1, replicated to 2. A replicated acknowledgement requires achieved_replicas≥desired_replicas and at least 2. Achieved counts recoverable receipts only; losing a verified key path/host decreases it and changes recovery_state. A larger requested count must actually complete before acknowledgement. |
| `key_envelopes[]` | Read-only `{recipient_kind:host\|owner_recovery,recipient_id,key_id,wrapping_algorithm,wrapped_key_blob_id,envelope_digest}`. Ciphertext reference is owner/custody ACL-protected; no unwrapped key in any resource. |
| `replica_receipts[]` on Snapshot/Artifact/Template, `backup.receipts[]` | `{host_id,manifest_digest,envelope_digest,decrypt_verified_at,committed_at,signature}`. A recovery receipt requires successful authenticated decryption under an authorized recipient key and stable-storage completion. |
| `capture_provenance` | `{producer_key_id,producer_host_id,owner_id,trust_class,manifest_digest,engine_digest,guest_kernel_digest,device_graph_digest,cpu_baseline,signature}`. Every `acceleration_variants[]` item has this field plus `compatibility`; producer authorization is checked separately from template ownership. |
| `action_plan` | Adapter-computed `{adapter_digest,operation,resource_ref,arguments,destination,secret_refs[],form_ref,diff_blob_id,redacted_fields[]}`. Omit inapplicable form/diff fields. `arguments` follows the pinned operation schema. `form_ref` is `{origin,frame_origin,action_url,method,form_digest}`. Store at most 64 KiB of canonical UTF-8 JSON; larger effects use an immutable bounded Blob manifest/diff. Secret values never appear. |
| `action_digest` | SHA-256 of the canonical redacted plan plus the enforcing adapter's HMAC-SHA-256 digest of any secret-bearing normalized inputs (key held only by that broker), principal/task/generation, resource revision and expiry. Store those private inputs only in the broker. The adapter verifies both bindings immediately before effect. Approval create accepts the proposed action_plan plus task/sandbox/generation/resource/expiry; the enforcing adapter validates and recomputes it. action_digest is server-derived on create, then echoed on approve/use. A mismatched plan is rejected, even if caller description or supplied digest looks plausible. Caller descriptions cannot replace the plan; a stale target requires new approval. |
| `revocation_status` | Read-only `{enforcers[],complete}`. Each entry is `{host_id,enforcer,acknowledgment:pending\|acknowledged\|expired,acknowledged_at,authority_expires_at,residual_capabilities[]}`. `enforcer` is `supervisor,guard,broker,access,storage,plugin`. Each residual is `{provider,resource_ref,capability_kind,expires_at,revoke_state:pending\|revoked\|unsupported\|unknown}`; omit unknown expiry and mark unknown, never invent it. No bearer/secret URL. Complete means local enforcers acknowledged/expired, not that irreversible remote effects were undone. |
| `fork_preparation` | Read-only `{source_sandbox_id,source_generation,source_revision,exposure_set_digest,expires_at}` on an empty prepared child. The exposure-set digest covers the source’s full monotonic exposure history, not just active grants. Fork rechecks source revision, generation, current authority and this exact history before copying; any change requires a new preparation/consent. It clears the preparation only after committed capture. |
| `native_execution_grant` inputs | Create requires `host_id,user,working_paths[],harness_digest,expires_at,approval_id`; principal is authenticated. Absolute canonical working paths are no broader than the destination owner's approved set. Supervisor denies symlink escapes. `user` is effective UID, never an arbitrary privileged account. Only destination owner authority plus independent approval may create this grant. |
| `config_ref`, `configuration.document` | `config_ref={configuration_id,digest}` addresses immutable validated JSON. `document` is ≤64 KiB nonsecret JSON matching `schema_digest`; `credential_source_refs[]` holds authorized CredentialSource IDs for host-only resolution. No inline token, password, private key or caller-local path. Config revisions publish a new ID/digest. |
| `plugin` package inputs, `adapter` schemas | Plugin create requires `package_blob_id,digest`; verify package manifest/provenance before installation. Its manifest publishes immutable adapter IDs/digests and all config/operation/result JSON Schemas. An adapter ID includes its plugin digest binding; unknown/stale schemas cannot execute. First-party packages follow §14.3 update trust; third-party packages require explicit owner publisher/digest trust and A3 for engines. |
| `auth_flow.purpose`, bootstrap inputs | `ci_exchange\|fleet_recovery\|provider_login`; `proof_key_thumbprint` is SHA-256 of the proof public-key bytes: a generated ephemeral key for ci_exchange, or the enrolled offline fleet-recovery key for fleet_recovery. Bootstrap accepts only these two purposes; provider_login uses authenticated AuthFlow create. `source_identity` is required for CI as an untrusted hint until exchange verifies forge evidence; omit it for local recovery. For CI auth it is `{issuer,repository_id,repository_owner_id,workflow_ref,workflow_sha,ref,sha,event,environment}` with provider-absent claims omitted. `auth_flow_exchange.proof` and `fleet_recover.proof` are `{public_key,signature}` using Ed25519 public-key/signature bytes encoded as base64; its SHA-256 thumbprint must match bootstrap. Sign canonical `{auth_flow_id,challenge,jwt_sha256,audience}` for exchange and `{auth_flow_id,challenge,fleet_fingerprint,new_root_public_key}` with the enrolled recovery key for fleet recovery. No proof contains a private key. The bounded bootstrap has no execution authority (§15.1). |
| `repository_visibility`, `job_token` | Visibility is forge-verified `public\|private\|unknown`; unknown uses the public safety ceiling. Job token descriptor is `{issuer,audience,actions[],resource_ids[],expires_at}` with no token bytes. B2 permits only this job's CI-owned short-lived authority. |
| `metadata_versions`, `security_revocations[]`, `migration` | `{root,targets,snapshot,timestamp}` uint64 versions; revocation `{digest,effective_at,reason,metadata_version}`; migration `{log_version,snapshot_version,projection_version,phase:expand\|barrier\|contract,rollback_safe:boolean}`. Server-derived, not caller overrides. |
| `root_fingerprint`, `recovery_key_fingerprint`, `previous_task_id` | Fingerprints are SHA-256 of canonical public-key bytes, base64 encoded; root and offline fleet-recovery key are distinct. previous_task_id is a read-only link set on the new Task at continuation commit, preserving old usage/outcomes. |
| `acceptance_proof`, `fence_proofs[]` | Transfer acceptance is `{public_key,signature}` by the new owner’s enrolled key over canonical `{fleet_id,authority_epoch,new_owner_principal_id,action_digest}`. A fence proof is `{host_id,boot_id,authority_epoch,method:stopped_receipt\|physical_fence\|expired,stopped_at,expires_at,receipt}` with inapplicable timestamps absent. A stopped receipt is enforcer-signed after drain; physical fencing is attested by the trusted local recovery operator. Expiry is sufficient only after all old authority issuers are fenced and cannot renew (§15.2). |
| `host.boot_id`, `clock_uncertainty_ms` | Random identity generated by supervisor for each OS boot/service cold recovery; bounded authority-clock uncertainty (§14.1). Omit clock_uncertainty_ms if the bound is unknown; timed authority is then invalid. Sleep/restart validation is independent of the serializable wall clock. `lease.boot_id` binds the enforcer instance. |

Sandbox create accepts exactly one of recipe_id or template_id, plus optional `host_id,engine_id,resources,devices,harness_id,auth_mode,kit_ids,connection_ids,settings_source_ids,network_policy_id,network_plans,task_id,lifecycle,durability`. Overrides must narrow policy and are recorded after immutable resolution. Optional host_id is a strict pin; engine_id may select only an eligible supported engine.
If task_id is omitted, admission creates Task+Budget under the caller's limits; Session/Exec inherit the sandbox Task by default. A recipe harness starts one initial Session and its ID is returned; a tool-only recipe waits for session_create.
Mutable name-pointer updates publish a new immutable Template/Recipe/Kit version ID/digest. Existing IDs do not change content. WarmPool references one immutable recipe; Sandbox.warm_pool_id is provenance, avoiding a recipe/pool reference cycle.
Audit `details` fields by type: command `{input_sequence,command_digest,argv,command_text,cwd,exit_code,signal}`; process_start and process_exec `{process_id,parent_process_id,process_instance_id,parent_process_instance_id,executable,executable_digest,argv,user,runtime,exit_code}`; request `{request_id,method,tool,url,route_template,headers,arguments,body_blob_id,redacted_fields,approval_id,status_code}`; connection `{flow_id}`; control types `{resource_ref,action_digest,grant_id,approval_id,reason,outcome}`. Only applicable fields appear; sensitive argv/text is redacted before persistence.

For process records, `process_id` and `parent_process_id` are uint64 native PIDs. `process_instance_id` is `{boot_id,pid,start_sequence}` and the parent field has the same shape, avoiding PID-reuse ambiguity. `executable` is the actual resolved program path/native identity as a string, never argv[0]. `user` is the effective guest UID as a string. `argv` is an array of strings; sensitive values/path components are redacted with explicit `details.redacted_fields[]` (also available on process/command records). `runtime` is `{kind:native|nested_docker,runtime_id,container_id}` with container fields present only when applicable. `started_at` is the process start time for process_start; process_exec uses its transition time. Required identity/user/parent/start attribution cannot be silently omitted after redaction.

`device_ref` is `{sandbox_id,generation,device_id}` and must address an admitted `devices[].id`. `input` is a tagged object: `{type:key,code,action:down|up,modifiers[]}` uses USB HID keyboard usage codes and modifiers `shift,control,alt,meta`; `{type:mouse,x_px,y_px,buttons[],wheel_x,wheel_y}` uses integer display pixels from the top-left, buttons `left,middle,right`, and signed wheel detents; `{type:audio,format:pcm_s16le,sample_rate_hz,channels,data_base64}` uses interleaved signed 16-bit PCM. Keyboard code is uint16 on the HID keyboard usage page; mouse coordinates are nonnegative integers within the admitted display. Device sequences are uint64, increasing per device_ref/generation; key-up in the example is sequence 2. A reused sequence with changed input conflicts before delivery. Unknown variants or out-of-range coordinates fail before delivery. Device input acknowledges `{device_ref,sequence,delivered_at}` only after the backend confirms delivery; duplicate sequence/Operation never redelivers. Display/audio watch yields MediaFrames plus StreamDescriptor `media_type,width_px,height_px,sample_rate_hz,channels` where applicable. Disk devices use the file/block attachment contract; GPU command APIs are engine/kit-specific and never generic host ioctls.

## 12. Canonical operations, errors, events and transports

### 12.1 Exact naming grammar

For every standard action in §11, operation ID and MCP tool are `<stem>_<action>`; CLI is `lamp <noun> <action>`.
Thus `network_policy_update` is exactly `lamp network-policy update`, not `network_configure` or `connection_set`.
REST mapping is fixed: list `GET <collection>`, get `GET <collection>/{id}`, create `POST <collection>`, update `PATCH <collection>/{id}`, delete `DELETE <collection>/{id}`.
Telemetry is a singleton: get/update use `GET/PATCH /v1/telemetry` without ID. Fleet get/update require the fleet ID; lists use `items`, never a count named `hosts`.
All paths below are under `/v1`. Extra actions use `POST <collection>/{id}:<kebab-action>` unless explicitly marked GET. CLI action converts underscores to hyphens; MCP retains underscores.
The bounded preauth bootstrap exchange below creates no work or durable Operation. Every resource mutation uses `idempotency_key`; HTTP maps it to `Idempotency-Key`. CLI uses `--idempotency-key`; MCP uses the JSON field. Do not send competing body/header values.
CLI input fields map snake_case to kebab-case flags. Arrays/objects use `--<field>-json` or `--input <caller-local-json-file>`. `--input` reads locally and sends JSON; it never makes the host read a caller path.

| Extra operation IDs / MCP tools | REST suffix and exact input/result fields |
| --- | --- |
| `fleet_transfer_owner`, `fleet_recover` | `:transfer-owner {new_owner_principal_id,acceptance_proof,approval_id}` → Operation; `:recover {auth_flow_id,proof,new_root_public_key,fence_proofs[]}` → Operation over the local recovery endpoint only (§15.2) |
| `auth_flow_bootstrap` | POST `/auth-flows:bootstrap {fleet_id,purpose,proof_key_thumbprint,source_identity}` → `{auth_flow_id,challenge,audience,expires_at,request_id}`; ephemeral preauth challenge exchange, no credential, durable Operation or mutation key. Normal auth_flow_get cannot read a preauth flow without its proof; no new preauth get/list route |
| `host_drain`, `host_enroll`, `host_bench` | `:drain {deadline_ms}`; `:enroll {invitation_id,proof}`; `:bench {suite_digest}` → Operation |
| `key_rotate`, `grant_revoke`, `share_revoke`, `exposure_revoke` | `:rotate {expires_at}` → new Key secret once; `:revoke {reason}` → revocation Operation |
| `template_build`, `template_validate` | `:build {definition_blob_id}` and `:validate {}` on a created Template → Operation |
| `sandbox_prepare_fork` | `:prepare-fork {fork_mode,durability,attachment_overrides[],task_id}` → Operation result `{sandbox_id,generation,fork_preparation,expires_at}` for an empty accepted child; no RAM/disk copy or worker. Preparation expires after 300 s and is source-generation-bound. Obtain Exposure approval against this returned child, then consume it once in sandbox_fork |
| `sandbox_fork`, `sandbox_suspend`, `sandbox_resume` | `:fork {fork_mode:memory\|disk_only,durability,attachment_overrides[],task_id,prepared_sandbox_id,exposure_ids[]}`; `:suspend {durability}`; `:resume {continuation_task_id,expected_task_id,exposure_ids[]}` → Operation; both continuation fields are absent for an unexpired same-Task resume and required together after expiry; disk_only is only the native-Mac fallback [owner-confirmed A2] |
| `sandbox_save_template`, `sandbox_restore` | `:save-template {name,selected_paths[],reconstruction_blob_id}`; `:restore {snapshot_id,recovery_credential_source_id}` → Operation [owner-confirmed A1] |
| `sandbox_file_read`, `sandbox_file_write`, `sandbox_file_transfer`, `sandbox_file_list`, `sandbox_file_delete` | `:file-read {path,offset_bytes,length_bytes}` → `{data_base64,sha256,eof}`; `:file-write {path,blob_id,expected_sha256,mode}`; `:file-transfer {path,blob_id,direction:upload\|download}`; `:file-list {path}` → `{items[]}`; `:file-delete {path,recursive}` → Operation for writes/deletes/transfers |
| `sandbox_device_input`, `sandbox_device_watch` | `:device-input {device_ref,sequence,input}` → Operation; GET `/{id}/device-stream?device_ref=...` → `{stream:StreamDescriptor,items:MediaFrame[],next_cursor,request_id}`; input tagged as key/mouse/audio message |
| `exec_watch`, `exec_input`, `exec_resize`, `exec_interrupt` | GET `/{id}/output?cursor=...`; `:input {sequence,data_base64}`; `:resize {rows,columns}`; `:interrupt {signal}`; output Frame or input Operation |
| `session_watch`, `session_attach`, `session_input`, `session_resize`, `session_interrupt` | GET `/{id}/output?cursor=...`; `:attach {lease_id,cursor}` → `{stream:StreamDescriptor}`; `:input {lease_id,location_epoch,sequence,data_base64}`; `:resize {lease_id,rows,columns}`; `:interrupt {signal}` → Operation |
| `session_spawn`, `session_teleport`, `session_checkpoint` | `:spawn {sandbox_mode,recipe_id,selected_paths[],delegated_scope}` → Operation with new session/sandbox IDs; `:teleport {location,recipe_id,native_execution_grant_id}`; `:checkpoint {}` → Operation |
| `lease_renew`, `lease_release` | `:renew {expires_at}` / `:release {}` → Lease; deadline may never exceed parent authority |
| `change_set_apply`, `change_set_reject`, `settings_change_apply`, `settings_change_reject` | `:apply {selected_paths[],approval_id}` → Operation; `:reject {reason}` → resource |
| `connection_test`, `connection_invoke`, `connection_revoke` | `:test {}`; `:invoke {attachment_id,operation,arguments,approval_id}`; `:revoke {reason}` → Operation; invocation is adapter-schema-bound, never arbitrary upstream tool name |
| `network_attachment_test`, `port_publish`, `port_unpublish`, `port_share_link` | `:test {}`; `:publish {}`; `:unpublish {}` → Operation; `:share-link {expires_at,subject_id}` → `{grant_id,url,expires_at}` transient secret-once result [owner-confirmed O4] |
| `browser_action`, `browser_fill_password`, `browser_takeover`, `browser_return_control` | `:action {action:navigate\|click\|type\|read\|screenshot\|download,arguments,lease_id}`; `:fill-password {credential_source_id,secret_ref,action_digest,approval_id,origin,submit,plan}`; `:takeover {lease_id}`; `:return-control {lease_id}` → Operation with filtered result/blob reference |
| `kit_install`, `kit_install_status` | `:install {sandbox_id,lease_id}` starts a guest toolchain install job → `{job_id,state}`; `GET /kits/{kit_id}/installations/{job_id}` `install-status {job_id}` → `{state(installing\|ready\|failed),result,failure_code,phase}` |
| `approval_approve`, `approval_deny`, `approval_revoke` | `:approve {action_digest}` / `:deny {reason}` / `:revoke {reason}` → Approval; consumption is internal atomic transition, not a reusable approve badge |
| `invitation_accept`, `session_end`, `exposure_activate` | `:accept {proof}` consumes target-key-bound invitation; `:end {reason}` ends session; `:activate {approval_id,action_digest}` performs scoped exposure [owner-confirmed O5] → Operation |
| `cache_publish`, `cache_evict`, `volume_snapshot`, `volume_recover` | `:publish {generation_digest,base_digest}`; `:evict {target_bytes}`; `:snapshot {consistency}`; `:recover {backup_id,fence_proof}` → Operation |
| `blob_upload`, `blob_download`, `blob_finalize`, `artifact_finalize`, `artifact_download` | PUT `/blobs/{id}/content` with Content-Range; GET `/blobs/{id}/content`; `:finalize {sha256,size_bytes}`; artifact `:finalize {manifest_blob_id}` / GET `/{id}/content` → bytes/manifest; MCP download inputs `{offset_bytes,length_bytes}` return `{blob_id,offset_bytes,data_base64,chunk_sha256,sha256,size_bytes,eof}`; artifact reads also return `manifest_blob_id` |
| `backup_restore`, `backup_export` | `:restore {target_host_id,new_authority_epoch,recovery_credential_source_id}` / `:export {target_host_ids[]}` → Operation, custody check first [owner-confirmed A1] |
| `plugin_enable`, `plugin_disable`, `plugin_trust`, `plugin_test` | `:enable {}`; `:disable {}`; `:trust {digest,warning_acknowledged}` [owner-confirmed A3]; `:test {suite_digest}` → Operation |
| `job_cancel`, `job_reconcile`, `task_stop` | `:cancel {reason}`; `:reconcile {}`; task `:stop {reason}` → Operation |
| `trigger_deliver`, `schedule_run`, `schedule_pause`, `schedule_resume` | `:deliver {source_event_id,payload_blob_id,signature_metadata}`; `:run {occurrence_at}`; `:pause {}`; `:resume {}` → Operation |
| `notification_acknowledge`, `notification_retry`, `notification_sink_test` | `:acknowledge {}` → Notification; `:retry {}`; sink `:test {}` → Operation |
| `auth_flow_complete`, `auth_flow_exchange` | `:complete {code,state,proof}`; `:exchange {jwt,audience,proof}` → scoped credential once, with provider challenge checks |
| `update_rollback`, `operation_cancel`, `operation_wait` | `:rollback {}`; `:cancel {reason}` → Operation; GET `/{id}/wait?timeout_ms=...` returns current Operation, never implies success on timeout |
| `audit_record_export`, `telemetry_purge`, `api_describe`, `docs_read`, `metric_list` | POST `/audit-records:export {filter}`; POST `/telemetry:purge {}`; GET `/openapi.json`; GET `/docs?topic=...`; GET `/metrics` |

Scope actions are exactly `<resource_kind>.<action>` for the operation dictionary, including extra actions. `api.describe`, `docs.read`, `metric.list`, `event.list` follow the same rule; they are not administrator bypasses.
For example, sandbox_file_write needs `sandbox.file_write`, session_spawn needs `session.spawn`, and blob_upload needs `blob.upload`. Action names never silently expand into shell, approval or another resource's rights. `native_execution_grant.create` is separate from `session.teleport`; native grant delete uses revocation and reports its status. `auth_flow.bootstrap` has only the preauth challenge policy; `fleet.recover` uses local recovery-key proof rather than ordinary key scopes. All other operations need normal authenticated scope.
Authorization also filters referenced objects, list/event rows and stream frames. Unauthorized IDs return `NOT_FOUND`; cross-owner existence is not disclosed.
Deleting a missing/already-deleted owned resource returns the same successful deletion result. In-use ancestors return `RESOURCE_IN_USE` unless explicit cascading cancellation was authorized; never detach another owner's state.
Local development scripts need no additional product API. `lamp --help --json`, `lamp version --json` and install diagnostics are CLI-local commands, separate from product parity.

### 12.2 Result, error and concurrency contract

Single reads return `{data:<resource>,request_id}`; lists return `{items:[],next_cursor,request_id}`. No list is a bare array of IDs.
Mutation-envelope and Operation `resource_ids` are arrays of `{kind,id}` references. Standard create `result` is `{<stem>_id}`; sandbox create/fork adds `{sandbox_id,session_ids[]}`, session create/spawn adds `{session_id,sandbox_id}`, and exec create returns `{exec_id}`. Returned IDs, never caller guesses, drive the next operation.
Mutations return `{operation_id,resource_ids,request_id,replayed}`; HTTP 202 by default. `wait_ms` optionally returns the same envelope plus `operation` at its current state. SDK wait helpers inspect Operation, not a readiness badge.
Explicit typed results in §12.1 occupy envelope `result` on synchronous completion (HTTP 200/201) and `operation.outcome.result` when durable and nonsecret. Every accepted mutation still has the same durable Operation identity.
A named resource result (Lease, Approval, Notification or “resource” for reject) has shape `{<stem>:<resource>}`. An Operation-only result has `{}` unless that operation's create/fork/spawn/file/browser contract specifies IDs or payloads. Download/file-list/watch reads retain their stated byte/list/frame results. File-read is a read-only POST with the explicit unwrapped `{data_base64,sha256,eof,request_id}` result; it creates no Operation and needs no mutation key. Its hash covers that returned decoded range. File-list returns `{items,next_cursor,request_id}`. Watch/download reads also create no Operation; POST session_attach is a mutation and returns the complete envelope with `result.stream` containing the StreamDescriptor.
Key create/rotate, Invitation create and auth exchange return a transient `result.secret` once. `port_share_link` likewise returns its `result.url` only once. Never store these secrets/URLs in Operation/audit/events or request hashes. Persist only their keyed digest and nonsecret grant identity/expiry. Replays return the same resource/Operation/grant identity with `secret_available=false`, never another bearer. A lost secret response requires a newly authorized rotate/new invitation/exchange; a lost share URL requires revoke of that grant and a newly authorized link. Retrying its old key cannot mint another token.
Key delete is revoke, Share/Grant/Exposure delete starts revocation, and Attachment delete detaches; all obey §17.3. Key rotate returns a new child-equivalent scope and revokes the old key within the same operation; no implicit widening or unbounded overlap.
Artifact finalize also creates download_blob_id: an immutable ready Blob containing a deterministic archive of the exact manifest files, with a full hash and bounded chunks. Artifact download returns those bytes/chunks and manifest_blob_id; it never substitutes only a metadata manifest for missing outputs or extracts hostile files onto the host. All bundle bytes/workspace count against quota.
Snapshot create captures the named sandbox without suspending it. sandbox_restore requires a stopped/suspended destination, fences its old generation and applies compatibility/custody checks before replacing state; it never overwrites running work or grants another owner's state.
StreamDescriptor is `{stream_id,transport,url,expires_at,cursor,max_frame_bytes,max_payload_bytes}`; never a raw upstream credential or local filesystem path.
Error is `{error:{code,message,retryable,operation_id,details},request_id}`. Safe details include revisions and resource-local phases, never credentials/content from another owner.

| HTTP / code | Meaning |
| --- | --- |
| 400 `INVALID_ARGUMENT`; 401 `UNAUTHENTICATED`; 404 `NOT_FOUND` | Schema/identity/authorized visibility failure |
| 403 `SCOPE_DENIED`, `VM_REQUIRED`, `HOST_NOT_TRUSTED`, `APPROVAL_REQUIRED` | No effect; explain the required authority/boundary safely |
| 409 `REVISION_CONFLICT`, `IDEMPOTENCY_CONFLICT`, `RESOURCE_IN_USE`, `OUTCOME_UNKNOWN`, `TASK_AUTHORITY_EXPIRED` | Inspect current revision/original operation; never blindly rerun |
| 410 `RESULT_EXPIRED`, `KEY_EPOCH_EXPIRED`, `CURSOR_EXPIRED` | Retry safety/replay record cannot be reused as fresh work |
| 422 `ENGINE_UNQUALIFIED`, `DEVICE_UNAVAILABLE`, `DEVICE_NOT_FORKABLE`, `AUTH_TRANSPORT_UNSUPPORTED`, `CONTINUATION_UNSUPPORTED`, `CLEAN_REBUILD_REQUIRED`, `TOKEN_CAP_UNENFORCEABLE` | Required supported workflow absent; remains visible release coverage failure |
| 429 `NO_CAPACITY`, `CONTROL_QUOTA_EXCEEDED`, `RATE_LIMITED` | Retry-After; no silent unbounded queue |
| 503 `HOST_UNAVAILABLE`, `DATA_UNAVAILABLE`, `QUORUM_REQUIRED`, `IDEMPOTENCY_HOME_UNAVAILABLE`, `TOKEN_HOME_UNAVAILABLE`, `AUDIT_UNAVAILABLE` | Explicit unavailable authority/data, not a fresh replacement |

Every update and dangerous extra action requires `expected_revision`, mapped to HTTP If-Match. Stale policy/budget/consent/recipe edits fail atomically. Recheck current authority before effect.
Cancellation before commit releases reservations and yields canceled. After commit it stops remaining work and reports committed resource IDs/effects; cancel-after-success returns succeeded with `cancel_effect=none`.
An external action already completed cannot be undone by cancel. Unknown delivery/effect stays `outcome_unknown`, independently inspectable.
REST downloads return authenticated bytes with Content-Range and manifest/full-object SHA-256 metadata. CLI `blob download`/`artifact download` requires `--output <caller-local-path>` and writes verified bytes there, then emits `{blob_id,output_path,size_bytes,sha256,eof:true,request_id}` JSON metadata. It emits no binary stdout in JSON mode. MCP uses the bounded base64 chunk result above; verify chunk hash, offset, EOF and final whole-object hash. A sandbox file-transfer download returns a ready Blob ID in its durable Operation result; upload requires blob_id and download omits it.
CLI JSON emits one envelope, or NDJSON frames for `--follow`; stdout contains no prose/progress. Exit codes: 0 success, 2 invalid input, 3 auth, 4 conflict, 5 unavailable, 6 failed/canceled Operation. Exec exits are in `exit_code`; transport success is distinct.

### 12.3 Retry safety

Idempotency scope is fleet authority epoch + immutable principal + operation name + key, with canonical input hash. Persist accepted identity before any effect; worker deduplicates the operation ID too.
Same key/body returns original operation; different body conflicts. Results remain 30 days after terminal state; compact tombstones remain until fleet deletion, even after resource deletion. Quota admission reserves tombstone space first; safety is never evicted for space.
Structured keys `lk1.<authority_epoch>.<home_host_id>.<nonce>` route to the named immutable home, chosen from authenticated discovery by SDK/CLI. The same key never fails over to another home.
Opaque/imported keys require a quorum-committed home registration before effect. Without quorum, only structured keys with a reachable home and local permit can start; unknown opaque keys return unavailable.
If home/receipt is missing, retries inspect or wait. They never reallocate. An awake host can accept genuinely new structured keys under its permits even while old home keys remain unavailable.
Receipt/tombstone backups are mandatory. Lost/rolled-back home journals require a new authority epoch with all old keys rejected, or recovery/reconciliation before write service. Restoring an old backup cannot make a deleted key fresh.
Messages/input/approval/job claims use operation IDs before delivery. A command crash between delivery and receipt may have external effects; do not promise exactly-once arbitrary side effects.

### 12.4 Events and bulk streams

Events are at least once, ordered per resource revision, with unique `event_id=(host_id,host_epoch,sequence)` and opaque fleet cursor vector. Cross-host events use resource/location epochs, not wall-clock total order.
Metadata replay retention is seven days; expired cursor returns `CURSOR_EXPIRED` and authorized full-list resync cursor. Session output retention is separately quota-bound and explicit.
Canonical event names are `<kind>.created`, `<kind>.updated`, `<kind>.deleted`, `<kind>.state_changed` for resources that have those operations/transitions.
Additional event names are exactly `operation.phase_changed`, `operation.completed`, `session.status_changed`, `session.output`, `session.message_accepted`, `session.location_changed`, `session.sub_agent_created`, `exec.output`, `exec.exited`, `lease.revoked`, `grant.revocation_pending`, `grant.revoked`, `approval.requested`, `approval.consumed`, `budget.threshold_reached`, `task.stuck`, `task.needs_decision`, `task.done`, `audit.gap`, `notification.delivery_failed`, `fleet.coordination_changed`, `host.heartbeat_lost`, `exposure.activated`.
Payload is changed canonical fields/resource references; no secret values. Private event filtering precedes serialization and applies during replay/revocation.

REST uses SSE on GET `/v1/events?cursor=...` with `Accept: text/event-stream`; this is the same event_list operation/path and event.list scope, negotiated by response media type. There is no separate /events:stream operation. CLI `lamp event list --follow --json` wraps SSE as NDJSON. GET `/v1/events?cursor=...&limit=...&wait_ms=...` and MCP `event_list({cursor,limit,wait_ms})` are the single canonical cursor-read operation with `event.list` scope. MCP uses that operation with bounded long-poll ≤25 s and returns `{items,next_cursor,request_id}`; do not assume nonstandard MCP subscriptions.
PTY/output Frame is `{stream_id,sequence,channel:stdout|stderr|stdin|exit|control,data_base64,byte_offset,location_epoch}`. MediaFrame is `{stream_id,sequence,device_ref,media_type,pts_us,duration_us,keyframe,payload}`; payload is binary, or `data_base64` instead for MCP chunks. Watch reads accept `{cursor,limit,wait_ms}` and return `{items,next_cursor,request_id}` with the same frame sequence.
REST/CLI may use authenticated WebSocket/HTTP2 streams; MCP uses cursor/chunk tools with the same framing, lease and cancellation. Ordinary SSH adapts its channels/resize/EOF to this contract.
Default stream queue is 1 MiB of queued encoded bytes. Stream payloads are ≤64 KiB decoded; bulk file payloads are ≤1 MiB decoded. REST binary frames/chunks add at most 4 KiB framing. MCP/JSON/NDJSON stream records are ≤96 KiB encoded UTF-8 including base64 and all metadata; bulk chunk messages are ≤1408 KiB encoded. Limit transport metadata to 4 KiB encoded and split payloads before encoding; if necessary reduce the decoded chunk further. These are distinct wire and payload limits, not a claim that 64 KiB base64 fits a 64 KiB frame. StreamDescriptor advertises the applicable `max_payload_bytes` and encoded `max_frame_bytes`. Bulk uses a separate charged one-chunk buffer, not the stream queue. Worker/plugin control messages keep their separate ≤1 MiB encoded-message cap; bulk bytes use the bulk channel. Slow observers disconnect with last replay cursor; they cannot block execution or cause authoritative audit loss.
If output spool quota fills, stop its task with `CONTROL_QUOTA_EXCEEDED`; do not silently discard acknowledged output. CLI/MCP allow cancellation of the same Operation.
All interfaces preserve the same operation authority, byte framing, cursor and revocation semantics.

### 12.5 Concrete interface journeys

Example names below resolve to returned real IDs; placeholder IDs are schematic. Set `t0` from the authoritative clock and derive `expires_at=t0+ttl_seconds`; historical timestamps are illustrative only. A caller uploads local bytes before any host-side build or write.

```sh
lamp blob create --size-bytes 1234 --sha256 '<digest>' --json
lamp blob upload bl_01 --file ./tools.lamp --json
lamp blob finalize bl_01 --size-bytes 1234 --sha256 '<digest>' --json
lamp template create --name tools --definition-blob-id bl_01 --json
lamp template build tp_01 --definition-blob-id bl_01 --json
lamp recipe create --name dev --template-id tp_01 --resources-json '{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}' --json
lamp sandbox create --recipe-id rc_01 --idempotency-key lk1.1.hs_01.random --json
lamp exec create --sandbox-id sb_01 --argv-json '["python3","-c","print(42)"]' --json
lamp sandbox file-write sb_01 --path /work/input.txt --blob-id bl_02 --json
lamp sandbox file-read sb_01 --path /work/input.txt --offset-bytes 0 --length-bytes 4096 --json
lamp attachment create --sandbox-id sb_01 --resource-kind connection --resource-id cn_01 --json
lamp session create --sandbox-id sb_01 --harness-id hr_01 --task-id tk_01 --agent-mode inside --json
lamp lease create --type input --resource-id se_01 --json
lamp session attach se_01 --lease-id le_01 --json
lamp session input se_01 --lease-id le_01 --location-epoch 1 --sequence 1 --data-base64 aGkK --json
lamp sandbox delete sb_01 --json
```

The uploader hashes the actual file; 1234 and `<digest>` above must be replaced with its real size/hash. No server reads `./tools.lamp`.
Equivalent REST/MCP examples use exactly the same JSON inputs:

| Operation | REST request | MCP tool and arguments |
| --- | --- | --- |
| Recipe create | `POST /v1/recipes {"name":"dev","template_id":"tp_01","resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}}` | `recipe_create` with that object |
| Exec | `POST /v1/execs {"sandbox_id":"sb_01","argv":["python3","-c","print(42)"]}` | `exec_create` with that object |
| File read | `POST /v1/sandboxes/sb_01:file-read {"path":"/work/input.txt","offset_bytes":0,"length_bytes":4096}` | `sandbox_file_read` adds `sandbox_id:"sb_01"` |
| File write/transfer | `POST /v1/sandboxes/sb_01:file-write {"path":"/work/input.txt","blob_id":"bl_02"}` / `:file-transfer {"path":"/work/input.txt","direction":"download"}` | `sandbox_file_write` / `sandbox_file_transfer` add `sandbox_id` |
| Connection attach | `POST /v1/attachments {"sandbox_id":"sb_01","resource_kind":"connection","resource_id":"cn_01"}` | `attachment_create` with that object; invoke through `connection_invoke`, never an unrelated upstream MCP tool |
| Watch/attach/input | `GET /v1/sessions/se_01/output`; `POST /v1/sessions/se_01:attach {"lease_id":"le_01"}`; `:input {"lease_id":"le_01","location_epoch":1,"sequence":1,"data_base64":"aGkK"}` | `session_watch`, `session_attach`, `session_input` add `session_id` and return cursor/framing above |

Every mutation also supplies its key and, for existing mutable objects, revision precondition. Examples omit only these common headers/fields for readability.
Complete family lifecycles: template create→build→get→update name pointer→delete; recipe create→get→update→delete; volume/cache create→attachment_create→update quota→attachment_delete→delete; artifact/blob create→upload→finalize→download→delete.
Connection lifecycle is create→connection_test→attachment_create→update→attachment_delete→connection_revoke/delete. Kit lifecycle is create with verified install plan→attachment_create→new-version update→attachment_delete→delete. Network is policy create→sandbox_update→policy_update→port_create/publish→unpublish→attachment_delete→policy_delete.
Authority is principal/membership create→key_create→key_rotate→grant/share revoke→membership_delete; browser is create→action/fill/takeover/return-control→delete; settings is source_create→change_create→approve/apply→source_update/delete.
Automation is plugin_create→enable→trigger/schedule_create→job_create→cancel/reconcile→artifact_download→delete/disable. Recovery is snapshot/backup_create→get→restore/export→delete; update create→get→rollback; consent telemetry_update→get→purge→disable.
Dashboard flows use the same resource lifecycles and documented errors.

A complete session attach response is `{"operation_id":"op_attach","resource_ids":[{"kind":"session","id":"se_01"}],"request_id":"rq_attach","replayed":false,"result":{"stream":{"stream_id":"st_01","transport":"websocket","url":"wss://api.f123.fleet.eilmer.si/v1/sessions/se_01/output","expires_at":"<t0+30s>","cursor":"c1","max_frame_bytes":69632,"max_payload_bytes":65536}}}`. The timestamp is a fixture substitution. The URL contains no bearer; authorization stays in the host session/current grant.

Device parity example, with the returned keyboard/mouse IDs and generation 1: CLI `lamp sandbox device-input sb_01 --device-ref-json '{"sandbox_id":"sb_01","generation":1,"device_id":"kbd_01"}' --sequence 1 --input-json '{"type":"key","code":4,"action":"down","modifiers":[]}' --json`; REST `POST /v1/sandboxes/sb_01:device-input` has the same `device_ref,sequence,input` object; MCP sandbox_device_input adds sandbox_id. Send the corresponding key-up, then mouse `{"type":"mouse","x_px":12,"y_px":8,"buttons":["left"],"wheel_x":0,"wheel_y":0}` to the mouse device. `sandbox_device_watch`/GET `/v1/sandboxes/sb_01/device-stream?device_ref=<URL-encoded-canonical-JSON>` and `lamp sandbox device-watch sb_01 --device-ref-json '<returned-display-ref>' --json` observe a controlled 64×64 app: HID code 4 inserts “a”; clicking (12,8) turns its known pixel green. Delivered sequences acknowledge actual backend input. Audio delivery uses the declared PCM format and sample rate.

Empty-host configuration journey: upload the signed plugin package as a Blob; plugin_create with its verified digest; adapter_list/get to inspect exact config/operation/result schemas; credential_source_create plus approved auth_flow for any host-held secret; configuration_create with schema-validated nonsecret document and credential_source_refs; configuration_get verifies ready/digest. Use that exact config_ref in network_plans or access_route_create, then test the attachment/access method. CLI/API/MCP expose setup from a new host without relying on an undiscoverable preloaded adapter.

Native create parity uses owner-built ready tp_mac: read template_get, inspect its minimum_resources/default_resources, then `lamp sandbox create --template-id tp_mac --resources-json '<exact returned default_resources JSON>' --json`; REST sandbox create uses that same resources object and MCP sandbox_create the same body. An explicit value below one published minimum returns INVALID_ARGUMENT before reservation. Callers use actual admitted values rather than Linux defaults for a native template.

Continuation parity: after the default hour expires but before saved-state retention ends, message/SSH/port wake reports TASK_AUTHORITY_EXPIRED. The person creates a Task/Budget within current limits, reads the sandbox revision, then calls `lamp sandbox resume sb_01 --continuation-task-id tk_new --expected-task-id tk_old --expected-revision 7 --json`; REST `POST /v1/sandboxes/sb_01:resume {"continuation_task_id":"tk_new","expected_task_id":"tk_old"}` with If-Match 7; MCP sandbox_resume adds sandbox_id/expected_revision. Fresh exposure_ids are required if retained raw-secret state needs them. Inspect returned Operation before retrying input. Concurrent continuation allows only one binding/resume; expired approvals never authorize saved raw-secret state.


## 13. Plugins and trust classes

Anyone may write a separately run program in any language. No dynamic plugin code loads into `lampd`.
Protocol `lamp.plugin/1` is length-bounded JSON-RPC 2.0 over inherited anonymous pipes or peer-authenticated Unix sockets. Bulk transfers use typed handles; control frames ≤1 MiB, default deadline 10 s, bounded concurrency/queues and cancellation.
Manifest fixes `type,publisher,version,digest,protocol_version,permissions,limits,config_schema,compatibility`. Major mismatch refuses start; additive minors negotiate; support N/N−1 during rollout.
Calls bind owner, task, operation, resource generation, parent grant and deadline. Host revalidates every requested effect; a plugin cannot gain authority by reporting itself trusted.

| Trust class | Execution / permitted authority |
| --- | --- |
| `confined` | WASI in a separate Wasmtime process with no ambient network, environment, filesystem or RAM handles; or native program in a dedicated service sandbox with the same typed grants. WASI is an option, not a language restriction. |
| `credential` | Owner-selected credential-source worker may access explicitly granted vault material, only via broker fill/sign/proxy. No sandbox RAM access, task-visible secret-return method or unrelated account authority. |
| `platform` | Audited built-in network/access/OS adapters receive narrowly assigned endpoint/resource handles. They never receive sandbox RAM. Final policy remains in core guard/supervisor. |
| `engine_trusted` | Built-in audited engine or third-party engine whose exact digest the owner marked trusted after a clear warning: it can read/alter its assigned sandbox RAM [owner-confirmed A3]. Never grant neighbors or broker keys. |

Ordinary plugin failure cannot crash core or inspect sandbox memory. Engine trust is the explicit exception [owner-confirmed A3], not something process separation can enforce away.
Network plugins transform/route packets; they receive no VMM memory mapping. Harness/kit plugins provide install/status/continuation/spawn plans and authorized guest operations, not direct RAM inspection.
CI/trigger plugins map verified demand to scoped jobs; Console plugins discover/watch/type; access plugins provide transport; credential plugins use only approved vault refs; engine plugins implement probe/prepare/create/capture/restore/destroy.
Install/update shows trust class, permissions and digest. Permission expansion or engine digest change needs a new explicit owner trust decision [owner-confirmed A3]; signatures establish provenance, not safety.
Crash/hang/flood revokes plugin grants and opens a circuit breaker; fail only dependent work.

## 14. Fleet authority, two-host failover and updates

### 14.1 One authority model

Control hosts embed a Raft implementation and transactionally project committed metadata to local SQLite. Use one voter for one host, two for two hosts, three voters when available; additional hosts can be learners/compute.
Only committed log entries create membership, ACLs, key authority, global quota partitions, approvals, schedule claims or exclusive cross-host location/writer epochs.
Local journals record effects under that authority; they are not a second ACL/placement system. Replicate small metadata, never RAM, packets, output or provider credentials.
Enrollment consumes a one-use invitation bound to host public key and approved roles. Removal revokes host certificate, routes and future permits; re-enrollment gets a new identity.
Every host serves discovery/API and routes requests to the proper authority/home. Heartbeats show actual host reachability; recovery has no fixed timing target.

A global `AllocationPermit` (internal authority record) is `{permit_id,host_id,principal_id,policy_revision,authority_epoch,resource_slice,allowed_operations,selectors,not_before,expires_at,sequence}` signed from a quorum commit.
Slices are disjoint across hosts and sum to no more than fleet/owner quotas, including retained bytes/control records. Local reserve uses compare-and-swap and a durable increasing sequence; an operation spends once.
Permit state machine: committed → active → consumed reservations/released reservations → exhausted or expired → reconciled. Unused capacity is not reassigned until expiry or explicit fencing/return receipt is committed.
Permits cannot authorize new members, broader policy, fresh global approvals, cross-host teleport, volume promotion or another host's quota. Existing one-use approval consumption has exactly one committed home; unavailable home means wait.
Local worker grants narrow the permit, bind resource generation and expire no later than parent authority. Host boot/recovery cannot reset consumed sequences.

Default permit horizon is 24 h, renewable with quorum; replenishment keeps useful disjoint headroom on each eligible host. Owners may shorten it; admission shows actual available slice and expiry.
A connected host applies revocation to active authority. A partitioned permit holder may continue its preauthorized work until the earlier parent/permit expiry, at most that declared horizon; show the actual offline expiry.
Default team secret use needs fresh broker authorization with a 30 s lease; renewing it requires reachable current authority. Secretless permit work has the longer offline bound. Explicit predelegated credential authority must show its longer revocation limit.
No host may mint broader grants without quorum. Authority leases use Linux CLOCK_BOOTTIME and macOS mach_continuous_time (both include sleep), never Go's serializable monotonic value alone [S56]. Bind every timed grant to the supervisor's `boot_id`; a service cold recovery or OS reboot invalidates prior local handles. Persist the absolute expiry and last trusted authority-time lower bound. Backward wall-clock changes cannot extend a deadline.
Bound authority-clock uncertainty, including synchronization error and oscillator drift through the lease horizon. Shorten/expire a grant when that bound cannot be maintained. With uncertainty u, an enforcer stops by authority expiry minus u; a destination waits until expiry plus u, or obtains a verified physical/source fence. A serialized guest clock supplies no authority.
Supervisor, guard and storage server enforce boot/epoch/expiry independently of lampd on every vCPU-run admission, packet/flow operation and disk submission; drain/cancel queued I/O before a fence acknowledgement. Remote storage additionally checks writer epoch at the service. A host power-transition gate must quiesce workers before sleep and keep vCPU execution, resumed sockets and storage queues closed on wake until authority revalidation. The OS kernel/framework must enforce this ordering independently of a delayed Go wake callback. If that ordering is unavailable after abrupt sleep, reject transferable exclusive execution/writer leases on that platform.

### 14.2 Availability cases

| Case | Available work | Blocked work / safety rule |
| --- | --- | --- |
| One healthy host | Local global commits, creates, sessions, storage and policy | Physical host loss leaves no survivor; recover actual local/backup data, never invent a new fleet |
| Two healthy hosts | Quorum commits plus local permit admission | Both global voters needed for new authority changes |
| Either of two hosts sleeps/fails | Survivor automatically accepts new authorized structured-key creates within its disjoint permit, serves its sessions and routes fleet discovery | No new global grants, reallocating lost slices, uncertain old-home replay or promotion of unavailable private disks. No third host required for this useful failover. |
| Two-host partition | Each side spends only its own committed slice and local identities | Neither spends the other's slice or assumes the other's exclusive resource is dead |
| Three voters, one lost | Remaining quorum can commit and rebalance after fencing/expiry | Never duplicate an old keyed/exclusive effect |
| Quorum lost and permits exhausted/expired | Observe/attach still-authorized reachable work within remaining grant lifetime | New work returns QUORUM_REQUIRED/NO_CAPACITY until quorum or explicit fenced recovery |
| Host rejoins after sleep | Authenticate, learn current epoch/policy, reconcile journal/permits, expire stale grants, then admit | Old memory/input/volume writer cannot revive authority from a saved guest clock |

Unlimited mutually independent partition writes cannot preserve finite global quotas and immediate revocation. Bounded survivor authority is the concrete failover contract, not a hidden requirement for a witness.
If the owner requires indefinite global mutations after either two-host loss, that remains a separate product decision; no CAP claim manufactures it.
Lost responses, restarts or partitions cannot authorize duplicate effects, concurrent exclusive writers or quota overspend.
Pins remain strict; capacity failure is immediate unless caller requests a queue. Default queue disabled; explicit queue ≤100 operations/principal, TTL 60 s, reauthorization at dequeue.
Initial verified fleet scale is 1/2/3 and 32 hosts; algorithm has no three-host minimum. Larger fleets expose unqualified scale rather than silently dropping “many hosts.”

### 14.3 Routing, sleep and update

Relay routes to an enrolled healthy ingress; Tailscale Services uses separately approved tagged service hosts; owner-domain access uses configured L4/health-aware endpoints or bounded DNS failover.
A local-only socket on a lost host cannot be reached; the caller uses authenticated advertised alternative ingress under the same fleet identity. Established TCP does not migrate; reconnect using cursors.
Graceful sleep transfers coordination first and advertises unavailable local data. Unexpected sleep never implies session completion or RAM migration.
Opt-in updates take one global rollout lease, drain one host, verify signed digest/provenance and anti-rollback policy, retain compatible active workers, update, probe, then advance.
Failure pauses the entire rollout and rolls back that host. In two-host fleets do not update the second until the first returns healthy; bounded permits cover the interruption.
Control/plugins support N/N−1 protocol rollout. Saved engine state needs its exact declared ABI/CPU/kernel/device reader; retain old readers only while they remain security-authorized. No incompatible guest-state rewrite in place. Conversion requires verified resume/recapture or explicit cold reconstruction with continuity loss shown. Sleeping old hosts reconcile before serving.

Update trust uses a pinned TUF root, with 2-of-3 offline root keys and 2-of-3 release-target signatures including an offline signer. Online timestamp/snapshot keys cannot authorize new executable targets [S57]. The verified OS installer embeds the root fingerprint; initial installers are checked through the independently published release/root fingerprints, not a digest supplied by the same download. Owner-installed third-party publishers have separately approved pinned roots/digests; they cannot replace first-party metadata.
Targets bind platform, component, version, length, SHA-256 and dependency-set digest for installers, supervisor, engines, guest kernels/seeds, first-party kits and plugins. Persist highest trusted metadata/component versions and consistent snapshots; prevent rollback and mixed releases. Initial metadata validity ceilings are root 365 days, targets 30 days, snapshot 7 days and timestamp 24 h. Verify expiry with current authority time before accepting an update or new installation. Offline fleets may continue a previously verified, nonrevoked installed set; they visibly report stale update knowledge and accept no new package from expired metadata. Manual offline updates carry the same fresh signed metadata.
Rotate root versions sequentially with both old and new root thresholds; revoke stolen target/online keys through new root/delegation metadata. Distribute recovery root media signed by the still-trusted offline threshold, then reverify the target set. Compromise of that threshold requires independently reverified bootstrap; an online signing key alone is never a recovery root.
Signed security revocations bind exact engine/reader/kernel/plugin digests, effective_at and reason. At that deadline, supervisor blocks new restore/dispatch, fences and stops affected active workers, and invalidates retained readers/rankings. Sleeping hosts apply the revocation before releasing their power gate; disconnected hosts cannot learn new revocations sooner than their published authority horizon. Continuity and ordinary rollout drain never override a known revocation. Health-check rollback cannot reinstall a revoked digest. Preserve encrypted original captures as inert recovery data; use a patched compatible reader, confined disk-only extraction without the revoked state parser, or explicit cold reconstruction.

Control log commands, Raft snapshots and SQLite projections have independent monotonic format versions. N first performs journaled expand-only migrations; N−1 must read every state N can acknowledge during the rollback window. N writes only the old log/snapshot format until a quorum-committed capability barrier confirms every voter and rollback candidate can read the new one. Unknown mandatory commands are rejected before commit.
Record migration start/step/commit and rollback_safe in the stable safety journal. Crash recovery resumes or undoes each idempotent step. Contract/removal happens only after a separate committed barrier closes the N−1 rollback window and verifies backups/readers; thereafter rollback to N−1 is rejected, with forward repair required. Never restore a stale safety database to roll back a binary.

## 15. Access, auth, relay and eilmer.si

### 15.1 Access and roles

Built-in access routes are local IPC/loopback, Tailscale, Eilmer relay and owner-domain automatic HTTPS; Cloudflare Tunnel ships as a separate plugin with its own TLS-termination trust statement.
Local accounts/passkeys/public keys and owner OIDC work without Clerk on non-relay fleets. OIDC uses code + PKCE, exact issuer/audience/state/nonce and redirect checks; passwords use Argon2id.
Every relay caller needs the shared Clerk Eilmer account, even on a self-run relay. Identity is `(issuer,subject)`; email collision never joins accounts.
Keys are random 256-bit secrets shown once, hashed at rest, scoped to actions/sandbox/recipe/host/resource limits and expiry. Delegation is intersection; transport membership never confers application admin.
Fleet mode changes do not publish existing private data. Single-owner admits its owner; team uses invitations; public service explicitly maps outside identities to isolated principals/quotas and rate-limits before admission.

| Role [owner-confirmed O9] | Capacity/membership | Content and task actions | Credential approval |
| --- | --- | --- | --- |
| owner | Fleet policy, owner transfer, trusted engines, admission, all admin configuration | Own resources plus explicit content grants; app role alone does not read others' private sessions | Own credentials only; another person must explicitly delegate |
| admin | Hosts/plugins/quotas, member invitations/removal within delegated limits; no owner transfer | Own resources plus explicit grants; no implicit private-content read | No implicit vault/password approval |
| member | Own tasks, recipes, keys and permitted resources | Create/exec/files/watch/type on own permitted resources; share only owned shareable resources | Own permitted credential grants |
| viewer | No creation/admin by default | Explicit watch/read grants only; no implicit team-wide session listing | None |

Host administrators remain trusted for memory/disk access regardless of application role [owner-confirmed O9]. Revocation covers existing grants, streams and descendants (§17).
Keyless GitHub auth begins without a Lamp key. A configured public CI intake permits `auth_flow_bootstrap` for `purpose=ci_exchange` with a generated ephemeral proof-key thumbprint and nonauthoritative source hints. It creates only a challenge/flow, not a Principal, grant, reservation or login. This is an explicit ephemeral preauth control exchange: no durable Operation/tombstone or ordinary idempotency key. Delete its bounded state at expiry/consumption; rate limits and pending-flow quotas apply before allocation. A successful credential exchange is the durable mutation and uses the common mutation envelope/key with proof-authenticated Principal identity. Return audience `lamp:<fleet_id>:<challenge>`, expires_at and flow ID through the authenticated server TLS connection. Default challenge TTL is 60 s; bound the request to 4 KiB, 10 requests/minute/source with burst 10, 100 pending flows/fleet, and the 64-connection preauth ceiling. Return generic errors, expire storage promptly and rate-limit exchange too.
The clean CI job obtains its own forge JWT for that audience, then calls auth_flow_exchange with a signature by the bound ephemeral key over flow ID/challenge/JWT hash. Consuming the flow and persisting the credential effect/receipt is atomic at its fixed home; a missing home/quorum returns unavailable. Standard AuthFlow create remains authenticated for provider login. `purpose=fleet_recovery` bootstrap is local-only and uses the same bounded challenge, bound to the enrolled recovery-key thumbprint and recovery-key proof at fleet_recover. Neither flow can be converted to another purpose.
GitHub exchange validates exact issuer, JWKS/signature/algorithm, time, audience and one-use challenge/token fingerprint [S25].
Match immutable repository/owner IDs, workflow/ref/SHA, event, environment and protection policy against forge evidence. No assumption of a caller-controlled JWT nonce claim.
Return a ≤5-minute job/recipe/host/resource-limited Lamp key. Tailscale federation separately matches issuer/claims/tags/scopes and explicitly imports the pinned federation feature. No stored secret is needed on qualified SaaS lanes [S15].
Fork PRs lacking OIDC enter via trusted CI intake, with immutable untrusted policy. Never substitute an owner key or claim Headscale automatically has SaaS federation.

### 15.2 Relay and independent trust

Relay control signs people in, links already enrolled host public keys, and issues ≤60 s one-use routing grants bound to account/fleet/endpoint/caller proof and nonce.
Hosts maintain outbound authenticated carriers. Data framing is bounded OPEN/ACCEPT/DATA/WINDOW/CLOSE over TLS/QUIC, with HTTP2 transport fallback.
Native transports present routing grants in carrier setup. Stock browser TLS on 443 and stock SSH on an assigned port cannot carry a custom pre-TLS grant; their bounded SNI/port routing is unauthenticated until the host checks Eilmer sign-in and fleet enrollment inside TLS/SSH. Routing alone grants no app access; rate limits apply before authentication, and relay/account outage cannot silently waive host authentication.
Inside it, native callers establish pinned TLS 1.3 to the host; browser HTTPS is raw SNI pass-through; SSH uses its assigned opaque TCP port. Disable inner 0-RTT mutations.
Only hosts hold inner TLS/SSH private keys. Relay sees route/account/IP/byte/timing metadata and bounded in-flight ciphertext; no disk spool, request logs, inference service, files or transcripts.
Fleet action authority requires a separate host/owner-signed caller enrollment and current scopes inside the encrypted connection. Forging a relay grant cannot create a fleet key.
First pairing verifies fleet root via local fingerprint/QR or an existing trusted caller. Relay-supplied identity alone is not bootstrap trust. Account recovery alone cannot rotate the fleet root.
DNS/ACME issuer is a different service/account with no relay process credentials. Fleet signs exact allocated-name CSR requests with challenge nonce; issuer checks fleet registry/root, allowed names and quota. CSR key is generated on owner hosts and never leaves them.
Fleet root recovery uses the separately enrolled offline fleet-recovery key; account recovery alone is insufficient. Malicious replacement CSR, cross-fleet name and replay must fail.
`fleet_transfer_owner` requires the current owner, the new existing Principal's signed acceptance and a one-use Approval of the exact transfer. In one quorum commit, change the fleet owner role, increment authority_epoch, rotate fleet root/SSH CA and revoke the former owner's fleet-management descendants. Reissue host/caller certificates only with new-owner approval; old root/epoch certificates cannot join. Private content ownership and credential custody do not transfer implicitly.
If owner/root access is lost, use the trusted local supervisor recovery endpoint, not a relay endpoint. Obtain a local recovery AuthFlow challenge; sign flow/challenge/current fleet fingerprint/new root with the enrolled recovery key; submit fleet_recover with verifiable fence proofs for every old authority issuer and enforcer. Physically fence/decommission every old voter/issuer first so no old quorum can renew leases; then accept drained signed stop receipts, physical shutdown/removal or elapsed published expiry plus uncertainty for remaining enforcers. A stale backup or mere lease wait with a still-live issuing quorum is insufficient. Supervisor keeps all effects stopped until fencing completes, writes a new root/authority epoch and recovery receipt with stable barriers, then requires explicit host/caller re-enrollment. No old receipt, key, certificate or saved guest authority becomes current. CLI reads the signed recovery bundle locally; API/MCP over the local recovery socket use identical inputs. No ordinary fleet key can invoke this emergency path. Old owner/root/host/mutation authority and stale backups remain rejected after recovery. Data keys still need §7.1 recovery; rotating the signing root does not decrypt work.
The hostile-relay claim covers the forwarding process, not compromise of account-origin code, trusted host/browser, fleet root, domain administration or public CA. Native pins add protection ordinary browser TLS cannot supply against those larger compromises.

### 15.3 Origins, cookies and certificates

Hosted names are exactly `eilmer.si`, `docs.eilmer.si`, `account.eilmer.si`, `clerk.account.eilmer.si`, `relay.eilmer.si`, and one-label services under `<fleet_id>.fleet.eilmer.si`.
For fleet `f123`: `api.f123.fleet.eilmer.si` serves host-terminated API/dashboard; `h-<id>.f123.fleet.eilmer.si` is a host; `ssh.f123.fleet.eilmer.si` plus assigned port is SSH; `p-<random>.f123.fleet.eilmer.si` is one sandbox generation/port.
Never serve task HTML beneath trusted account/API paths or reuse content origins across owners. Self-hosted owner-chosen domains remain supported under the brief; this proposes no alternative Eilmer hosting domain.

Configure the one shared Clerk instance as a Secondary application at `account.eilmer.si`, with FAPI scoped to `clerk.account.eilmer.si` [S26]. No fleet/sandbox origin is an allowed Clerk subdomain/satellite.
Clerk's JS-readable application session is not a Lamp HttpOnly cookie. Test actual FAPI/session cookies; reject `Domain=eilmer.si` in any Eilmer product.
Products exchange a short-lived, exact-audience, one-use account sign-in code with PKCE/state into their own server-side session for the same issuer/subject. Do not spread a Clerk bearer over product subdomains.
Fleet cookies use `__Host-lamp_session`, Secure, HttpOnly, Path=/, no Domain; port ingress uses its own `__Host-lamp_port` cookie. Never forward these to sandbox apps.
Sibling origins remain same-site: exact Origin checks on mutation/WebSocket, CSRF nonce, no state-changing GET, strict credentialed CORS, exact callback URI/state/PKCE, Host/SNI validation. SameSite alone is insufficient.
Use restrictive frame CSP/COOP/CORP, `noopener`, exact `postMessage` origins and Origin-Agent-Cluster; no document.domain relaxation. Browser process isolation is not inferred from OAC.
Strip parent-domain Set-Cookie and spoofed identity/proxy headers from task responses; ignore unrecognized parent cookies and reject ambiguous auth-cookie duplicates. Header filtering cannot stop JS cookie tossing, so use host-prefixed cookies and isolated origins [S27].

One fleet-owned wildcard `*.f123.fleet.eilmer.si` covers exactly one label below that fleet, including API/host/content names. Private key is replicated only to that fleet's approved ingress hosts, never unrelated fleets or relay.
No certificate per sandbox, host or port; no global platform wildcard distributed to customers. DNS-01 challenge authorization is constrained to that fleet and exact CSR names.
A separate issuer alone does not stop a hostile TCP/443 relay answering TLS-ALPN-01. Domain administration centrally controls CAA for `fleet.eilmer.si` and each allocated fleet subtree. Publish both ordinary and wildcard restrictions, using the issuer's actual ACME account URI [S48]:

```dns
fleet.eilmer.si. CAA 0 issue "letsencrypt.org; accounturi=https://acme-v02.api.letsencrypt.org/acme/acct/<issuer-account-id>; validationmethods=dns-01"
fleet.eilmer.si. CAA 0 issuewild "letsencrypt.org; accounturi=https://acme-v02.api.letsencrypt.org/acme/acct/<issuer-account-id>; validationmethods=dns-01"
```

The account ID is a deployment substitution, not a second account authorization. No additive permissive issue/issuewild record is allowed. Use only CAs that enforce both parameters. Audit every descendant RRset, CNAME target, delegation and DNS-01 alias so none overrides/inherits a weaker policy; customers/relay cannot edit them. Keep effective policy cached only within the CA's supported freshness rules and block activation until the policy is authoritative. Other Eilmer product CA policies cannot weaken this subtree.
Run an independent Certificate Transparency monitor over all eilmer.si names, compare SANs/account-authorized issuance to the certificate ledger, and alert/quarantine affected routes on unexpected issuance [S48]. CT is detection, not prevention; domain administration and public CAs remain trusted. Unauthorized accounts cannot issue certificates for fleet names.
A shared issuance ledger respects actual CA limits and preserves capacity for the owner’s other products and repair [S28].
Persist ACME accounts/certificates, obey exact-identifier limits and use ARI-aware renewals; use appropriate staging for setup. Certificate exhaustion queues new hosted activation, not insecure fallback.
Obtain approved CA capacity before unrestricted onboarding beyond that cap. No quota override or Public Suffix List change is assumed. Existing fleets can create thousands of content names without further issuance.
All hosted origins remain under eilmer.si.

## 16. CI and triggers

Each native job uses `queued → preparing → running → collecting → completed|failed|canceled|cleanup_failed`. Reconciliation continues until execution and registration are gone.
Trusted intake verifies the forge/controller event and immutable source identity. Persist the external job claim before registering a one-job runner or accepting commands.
The controller retains organization registration credentials. Native runner lifecycle, results, cancellation, caches and artifacts remain compatible with the CI service.
Each plugin implements its CI provider’s actual native runner protocol and lifecycle.

| CI family / exact plugin ID | Native implementation | Provider behavior and primary contract |
| --- | --- | --- |
| GitHub Actions / `github_actions` | Go scale-set listener, `TotalAssignedJobs` demand, JIT ephemeral one-job runner | Redelivered demand, cancel-before-acquire, lost listener, deregistration, log/artifact result; no Kubernetes [S02] |
| Copilot cloud agent / same plugin | Documented Actions-based self-hosted scale-set route and `copilot-setup-steps` | Respect the eligible plan, OS and workflow. A vendor guest-firewall setting never disables Lamp's host guard [S29] |
| Claude Code and Codex Actions / same plugin | Official pinned actions on a disposable runner using an approved auth mode | Useful inference/tool result, scoped auth, failure/cancel; does not relocate every vendor-hosted mode [S30] |
| GitLab / `gitlab` | Fleeting Instance provider, capacity one/max use one; separate Custom executor compatibility mode | Prepare/run/cleanup and runner/controller loss per mode; inside-Docker job/service behavior [S31] |
| Buildkite / `buildkite` | Queue-aware controller; disconnect-after-job native agent | Cancellation, disconnect retry, uploads and no second assignment [S32] |
| Jenkins / `jenkins` | Jenkins-side Cloud/retention shim; unique inbound agent, one executor | Real controller plugin install, pipeline cancel/restart, node removal and orphan cleanup [S33] |
| Forgejo / `forgejo` | Version-matched ephemeral registration and one-job runner | Server token contract, workflow result/artifacts and registration deletion [S34] |
| Gitea / `gitea` | Separate ephemeral act_runner adapter | Its own server/runner versions, labels and container/service modes; no assumed Forgejo equivalence [S34] |
| CircleCI / `circleci` | Resource-class demand controller; machine runner single-task | Claim/cancel/result, no service auto-restart into a second task [S35] |
| Azure Pipelines / `azure_pipelines` | Pool-demand controller, native `--once` agent | One job, native cancellation/artifact result, agent deletion; no required Azure VMSS [S36] |
| Bitbucket / `bitbucket` | Label-specific runner lifecycle with completion-driven stop/deregister | Complete teardown before reassignment, including interrupted completion; no invented one-shot flag [S37] |
| Woodpecker / `woodpecker` | One-workflow agent, concurrency one, external teardown | Native result/artifact semantics, stop-after-workflow and lost controller [S38] |

Linux, native macOS jobs select the matching guest class. Requested device/service/Docker jobs use their supported implementations; Docker runs inside the task sandbox only.
API/SDK invocation from another CI is supported remote-task access, not proof of that system's native runner integration.
Runner readiness includes the intended job protocol, not just a shell probe. Teardown and the trusted stopped-retention override follow §7.2 even on failure/cancel; a controller restart reconciles external IDs before spawning replacements.

### 16.1 Trigger and schedule contract

Ship `slack`, `linear`, `webhook` and `schedule` trigger plugins. Verify source signatures/timestamps and immutable delivery IDs before parsing task-controlled content [S39].
Map external `(issuer,subject)` to a scoped Principal; recipe and reply destination are preauthorized, never selected by an untrusted instruction claiming privilege.
Deduplicate source event IDs using durable job/operation receipts. Retry the same occurrence identity, recheck current authority and expose the original operation on duplicate delivery.
Schedules store cron plus IANA timezone and UTC occurrence. Defaults: earlier occurrence once in an ambiguous DST hour, skip nonexistent local times, skip missed runs with a missed-run notification, forbid overlap.
Optional bounded catch-up is at most one coalesced missed run; overlap requires explicit concurrency admission. Quorum commits one claim per `(schedule_id,revision,occurrence_at)` before launch.
No new schedule claim without quorum. A claimed operation can continue on its permit home; failover cannot invent a second occurrence. Pause/cancel closes queued claims and stops descendants under the same task policy.

### 16.2 Stranger public-PR boundary

Trusted intake records forge-verified repository_visibility. Every public-repository job requires a fresh VM and allowlist, including a trusted branch. Stranger PR intake additionally computes `trust_class=public_pr` from verified forge evidence, not PR labels, workflow edits or supplied JWT claims alone. The remaining restrictions in this subsection are for stranger work.
Always instantiate a fresh real VM and new writable root from a clean verified template. Never reuse a task-used warm sandbox, owner settings, host mount, protected Connection or trusted writable cache.
Network is allowlist only [owner-confirmed O3]; block private/metadata/fleet control and arbitrary upload destinations. Approval cannot waive these immutable restrictions.
Keep runner registration/control and source credentials outside task authority where protocol-compatible: host-mediated checkout/results and a restricted runner controller delegate commands into the VM.
B2 permits the CI system's own short-lived one-job token, including a read-only repository token [owner-confirmed B2]. Record issuer, audience, actions, resource IDs, expiry and replay boundary in job_token; strip it from captures, artifacts and logs. Restrict it to that job's checkout/log/artifact/result protocol; it cannot mint credentials, access another job, write the repository or create a trusted cache/result outside this job. Revoke at terminal/cancel and prevent reuse even before expiry. No owner, model, vault, runner-registration or Lamp key enters stranger task memory. This is an explicit secret exception, never a “nonsecret” relabeling; useful native jobs retain these authority limits.
OIDC-less fork PRs use trusted authenticated controller intake; absence of job OIDC is not a reason to inject an owner key. Never run untrusted head code in privileged `pull_request_target` context [S40].

## 17. Observability, budgets and audit

### 17.1 Audit units and integrity

Every record is attributable to principal, host, task, sandbox generation, session, operation, policy revision and ordered sequence. Missing attribution is an audit fault.

| `audit_record.type` / `visibility` | Required event and fields |
| --- | --- |
| `command` / `mediated` | Before-dispatch Lamp/SSH/console command, input sequence, command/argv digest, safe argv or redacted text, cwd, result/exit and timing. Builtins submitted through a shell have their complete command envelope. |
| `process_start` / `process` | Every new process, including fork/clone without exec: actual executable identity, safe argv, effective user, parent/instance identity and start time, with native/nested-runtime attribution. Threads within the same process are not separate starts. |
| `process_exec` / `process` | Additional executable transition for an existing process, with actual program, effective user, safe argv, parent/instance identity and transition time. Keep the original start record. |
| `request` / `request` | Every proxy/gateway-handled HTTP/MCP/CLI operation: destination, method/tool, normalized URL/query, safe headers and validated arguments/body or encrypted body reference, decision, approval, bytes, status, request ID and timing. Multiplexed requests get separate records. |
| `connection` / `opaque` | Every allowed/denied network connection: destination IP/name/port/protocol, bytes sent/received, start/end/duration and policy decision [owner-confirmed A4]. Not an HTTP-request count. |
| `approval`, `authority`, `exposure`, `lifecycle` / `control` | Exact action/grant/resource references, caller, reason, revision, outcome and inherited task. No raw secret, session code or bearer. |

Under [owner-confirmed A4], opaque subscription TLS remains encrypted and flow-audited; full request detail is required wherever Lamp handles plaintext. No content interception is added to subscriptions.
B4 defines every command as every process started, not the semantics of script/builtin/interpreter code [owner-confirmed B4]. Observe process creation and executable changes separately: Linux fork/clone (count a new thread group, not CLONE_THREAD) and native macOS process creation/exec. Protected collectors also observe effective-user changes so later executable transitions retain correct attribution. Locked Linux kernels prevent audit disable/module replacement/kexec/raw kernel-memory bypass; host collectors verify sequence/heartbeat and nested attribution.
Native macOS also needs protected process collectors. Include fork-without-exec, actual program identity, PID reuse, effective-user changes and nested runtime attribution. Mediated command envelopes do not replace process-start records; script semantics are outside B4.
Mandatory audit admission reserves durable spool space before execution; records are acknowledged outside guest-writable storage. Collector loss, gaps or full spool stop new task/network effects and stop/freeze affected work.
Append encrypted hash-chained segments and signed roots, optionally with another owner host's receipt. Default retention is 30 days with quota/export controls; no silently dropped acknowledged records.
Full mediated request detail is the complete semantic request after mandatory credential redaction [owner-confirmed A4]. Strip Authorization/Cookie, passwords and secret query/body fields before serialization; preserve nonsecret arguments/content in encrypted owner-only audit blobs and list redacted field names. Protected fill/sign adapters log symbolic secret references, never values. Redaction covers streaming boundaries; audit content never enters telemetry.
Host root can falsify future local observations. Independent receipts protect previously acknowledged history, not a compromised collector's future truth.

### 17.2 Task budgets and watchdog

Task is the accounting root. Forks, native sub-agents, sessions, brokers and remote tools inherit the same root budget unless separately authorized capacity is explicitly granted.
Wall time uses §14.1's suspend-aware clocks/boot-bound enforcement plus persistent absolute deadline; sleep, blocked approval and teleport do not reset it. Default task ceiling is 3600000 ms, visible and overridable within caller scope.
Exact proxy mode [owner-confirmed A4] admits only model routes with supported tokenization and authoritative usage. Each root Task/Budget has one immutable token_home_host_id. That host's durable ledger atomically accounts used plus all outstanding worst-case reservations for every descendant/broker across the fleet. AllocationPermits reserve owner/host capacity; they do not duplicate Task allowance.
Before dispatch, every broker obtains a home-signed one-use reservation bound to task, request hash, broker, model and maximum charge: conservative input/tool/context plus maximum output/reasoning. The home checks `used_tokens + reserved_tokens + requested_bound <= token_limit` in one stable transaction. Broker records dispatch before transmission and consumes the reservation only once. A disconnected broker may dispatch an already issued unused reservation within its grant, never infer another allowance from stale counters. If home is unavailable and no reservation exists, return TOKEN_HOME_UNAVAILABLE.
Fork/teleport carries the same accounting root/home; no copy of remaining capacity is spendable. Usage/unknown reservations survive crashes. Moving the home needs quorum, confirmed old-home fencing and the complete durable ledger; if any receipt is uncertain, preserve its full charge and deny transfer that would overallocate.
Exact means the named proxied model Connections, not all possible inference at arbitrary public destinations. Deny configured unmetered model routes for an exact-cap task; unknown outside model activity remains best effort [owner-confirmed A4]. Show that boundary with the budget instead of an all-traffic exact badge.
Reconcile authoritative final usage; release unused reservation only after final evidence. Interrupted/unknown requests keep worst-case reservations. Retries need a fresh reservation unless the provider proves deduplication; cached/reasoning tokens follow the documented accounting contract.
Stop streaming at the reserved bound where supported; cancellation does not refund tokens already generated remotely. If no sound maximum or trusted usage exists, the route cannot advertise `token_mode=exact`.
Opaque subscription and unobserved outside-agent model usage are best effort [owner-confirmed A4]. Show unknown usage as unknown, with hard time limits and optional conservative request limits. An explicitly requested exact cap returns `TOKEN_CAP_UNENFORCEABLE` before work.
Hard time stop closes guard/broker/input routes and stops every local descendant at the task deadline. Remote tools receive cancellation; irreversible committed remote work remains in Operation outcomes and is never claimed undone.
Watchdog engineering defaults: warn after 300 s without observable progress; block for a decision after 10 repeated equivalent action digests or 600 s without progress. Long builds with observable CPU/output progress are not loops.
Pending human approval pauses loop detection, not the wall deadline. Record the evidence/reason and allow an authorized person to continue or stop; no automatic budget increase.
Future plans/quotas/billing can consume the same usage ledger. No payment system or mandatory hosted accounting service is included.

### 17.3 Revocation matrix

Every derived capability carries parent grant, authority epoch, task, resource generation, policy revision and expiry. Recheck on dispatch, reconnect, lease renewal and authority narrowing.
Revocation Operation stays pending until all required enforcers acknowledge or their published offline expiry passes. operation.revocation_status lists each host/enforcer, acknowledgment, authority expiry and residual provider capability. Refresh that object during real partitions and after expiry; a count alone is insufficient. No successful-looking delete hides active authority.

| Derived authority | Online action | Partition/expiry and residual |
| --- | --- | --- |
| Key, membership, host or task | Block admission; revoke descendants, worker/broker handles and routes | Local permits bound offline continuation to declared horizon (§14.1); no fresh authority without quorum |
| SSH, event/output stream, input/share lease | Close channels; stop further frames/input; bump relevant epoch | Already received content cannot be recalled; each stream renews ≤30 s authority |
| Port/link/network attachment | Remove route and close existing HTTP2/WebSocket/TCP/UDP state | Host guard enforces local deadlines even without coordinator; link reuse fails |
| Credential/MCP/CLI/browser/signing action | Revoke handle; cancel in-flight request; release/deny control and pending approval | Credential lease ≤30 s by default; completed upstream action and already issued outside capability may require provider revoke |
| Volume writer / export credential | Stop I/O, invalidate epoch, close mounts/locks | New writer waits for fencing/lease expiry and storage recovery; never parallel promotion |
| Exposure | Stop future injection/profile access; flag retained contamination | Revocation cannot erase bytes already read. Destroy/rebuild before a clean-boundary claim [owner-confirmed O5] |
| Remote tool / returned capability | Cancel/revoke at its issuing service where supported; record status | A presigned URL or finished deployment may remain effective until upstream expiry; default broker denies such export |

### 17.4 Metrics, notifications, telemetry and dashboard

Local metrics/traces expose latency phases, actual resources, reservations, failures, audit gaps, quota and recovery state without content attributes. OpenTelemetry export is owner-selected; payloads are redacted locally.
Done/stuck/needs-decision notifications use `NotificationSink` plugins, authenticated console/phone links or signed webhooks. Default retry schedule is 1, 5, 30, 300 and 1800 s, then failed; manual retry/ack is inspectable.
Show current resource/status events and notification delivery state, including provider or phone unavailability, without a fixed delivery-time target.
Usage collection is disabled on install/upgrade/crash. The canonical outbound schema is only `{counts:{create,fork,suspend,resume,failed_operation},timings_ms:{create,fork,resume}}`, with fixed numeric keys and bounded histogram buckets [owner-confirmed O7].
No versions, IDs, prompts, paths, URLs, content, user labels or crash dumps in telemetry. Transport authentication metadata is not added to analytics. Consent revision gates each send; opt-out deletes unsent batches and supports purge of stored batches.
Each opted-in batch expires after 90 days, including backups by expiry design [owner-confirmed O7]. Relay security routing metadata is separate: active account/host links until removal, minimal IP/route security records for seven days, then delete; no payload spool or product analytics reuse.

The host-served dashboard implements these flows with the common API, revision checks and scopes. Empty/stale states show last observation and never invent readiness.

| Screen flow | Inputs, action and observable outcome |
| --- | --- |
| New sandbox → recipe preview → compatible hosts | Edit recipe/tools/devices/resources and effective policy; validate trust/capacity; Create shows Operation stages, real command/session readiness or failed stage with retry/cancel |
| Sandbox/session detail → fork/suspend/teleport/share | Preview memory/disk semantics, destination and authority; show pending operation, continuity result or safe failure; sharing has separate watch/type and revoke controls [owner-confirmed A1] [owner-confirmed A2] |
| Connections/kits → auth/test/attach | Pick source/custody host and scoped operations; see blocked/failed setup, exposure warnings [owner-confirmed O5], approval and private takeover controls |
| Storage/jobs → output/recovery | Inspect durability, replica receipts, incomplete artifacts, cleanup failure; download verified output, detach, backup or recover with explicit fencing |
| Policy/access/admin → inspect/change/revoke | View effective revisions, role [owner-confirmed O9], key/port expiry, network changes and revocation acknowledgements; stale edits conflict, and retry starts from current data |
| Operations/budgets/notifications → decision | Show phase/error/committed effects, budget route/remaining/unknown usage [owner-confirmed A4], stop/resume/approve/deny, delivery retry and unresolved offline hosts |

## 18. Security and threat model

Assets are host/neighbor integrity, private work, credentials, fleet authority, audit history and finite capacity. Authentication grants actions; it does not make repository code, pages or tool output trustworthy.
TCB: host kernel/hypervisor, supervisor, selected engine/device backend, guard, broker, admitted credential recipients, trusted console and independent account/enrollment/issuance services.
Guest root, stranger code, network peers, ordinary plugins, sibling content origins and the relay forwarding process are hostile. Host administrators can read their assigned memory; hardware side channels and compromised trusted credential recipients are residual risks.

| Attacker / boundary | Product enforcement |
| --- | --- |
| Guest root → host/neighbor | Real VM plus per-OS worker confinement, minimal requested devices, no ambient mount/TSI/socket path |
| Caller/console → another owner | Common operation scopes, own-only lists/search/events/transcripts, separate watch/type/files/approval |
| Agent → protected credentials | Absence from task memory, typed proxy/sign/fill operations, recipient and origin constraints |
| Plugin → core/broker | Confined separate programs, typed grants and bounded RPC; only explicitly trusted engine may receive assigned RAM [owner-confirmed A3] |
| Public PR → owner/jobs | Fresh VM with only B2's one-job CI token, immutable verified intake, separate caches, allowlisted network [owner-confirmed O3], hostile artifacts |
| Relay/sibling page → authority | Independent pairing and issuance, inner TLS/SSH, trusted-origin code, cookie/Origin controls entirely within eilmer.si |
| Flooding tenant → availability | Admission reservations, cgroup/Job limits, bounded queues/streams/events/receipts/inodes/objects, per-source relay limits; unrelated admitted work retains its reservation |

Protect API keys, passwords and browser cookies from unauthorized access in memory, files, captures, templates, artifacts, logs, output and network traffic. Report the zero-leak goal once at release without claiming protection against unknown exploits.
Subscription exposure stays the ADR exception within custody/vendor-only rules [owner-confirmed A1]. Explicit raw password/browser exposures have separate consent and conspicuous results [owner-confirmed O5]; they never waive public-PR restrictions or template sanitation.
Supported features must let authorized users do useful work while enforcing their permission limits.
Initial control limits: 32 concurrent mutations/principal, 100 explicit queued operations/principal, 16 plugin RPCs, 1 MiB stream queues, 64 KiB decoded stream payloads, 1 MiB decoded bulk chunks (encoded limits in §12.4) and 64 preauth relay connections/source. All are configurable within host reservations.
Reserve retained bytes/control records before receipt, fork, output or upload creation; charge shared backing once physically and its growth liability to each admitted writer. Release by durable ownership transitions, never double-free on cancel.
Resource exhaustion or plugin failure preserves unrelated admitted work. Safety journals/tombstones cannot be evicted to repair a capacity mistake.
End every SQLite read transaction before writing a network response. Copy bounded pages into charged buffers; long exports use short snapshot pages or immutable export segments, never one slow stream-held transaction. Bound transaction lifetimes; cancel overdue readers and briefly gate new readers for RESTART/TRUNCATE checkpoints. SQLite warns that overlapping readers can otherwise grow WAL without bound [S58].
Physical admission includes database/WAL/temp/export files, inode use and worst-case migration/compaction copies, not only logical record counts. Initial WAL high-water is 64 MiB per control database: pause nonterminal writes, cancel readers and checkpoint there. The checkpoint worker reserves another 64 MiB of WAL headroom; stopping admission precedes either limit. Large atomic migrations need their own explicit disk reservation.
Preallocate a separate supervisor-owned 64 MiB emergency journal and its inodes, inaccessible to task data and ordinary application allocation. It is fixed-capacity, checksummed and stable-stored; no file growth or fresh inode is needed to record fences/terminal outcomes. Never overwrite unreconciled records. On ENOSPC/SQLITE_FULL or inode exhaustion: close admission and external effects first, drain/fence workers, write outcomes/reservation changes to that journal, then recover space/checkpoint and replay it into the normal store. If the reserve cannot be written, remain fenced and expose unknown outcome; do not acknowledge an unrecorded effect. Delete only unreachable scratch/content, never safety history.

## 19. Release measurements

Owner amendment, 2026-11-10: take measurements once at release and publish them honestly. The retained Linux targets are create p50 under 500 ms, live memory fork under 1 s, and at least 97% of native speed measured separately for computation, disk and network. The zero-leak goal remains. These are observations to report, never prerequisites for implementation, merge or release. There is no every-fork/no-outlier rule, required trial count, confidence threshold, fixed workload matrix, soak or every-change benchmark.

B1's standard measurement sandbox remains 2 vCPU, 4 GiB RAM and 20 GiB disk, with up to 1 GiB recently written RAM at fork. Larger sizes are reported separately; Mac targets follow supported-hypervisor measurements. Use normal guard, audit, storage and credential behavior. A faster result obtained by disabling product controls does not describe normal Lamp behavior.

Time create from the caller request to a real command. Time live fork through consistent capture, source continuation, new identity and a usable child. Distinguish prepared-template create, warm assignment, cold build, command-ready, agent-ready and durable suspend/resume. State actual hardware, OS, resources, storage, durability, access route and whether execution is nested. For native-speed comparisons, use the same host directly; do not slow the baseline or substitute a different endpoint. Publish observed failures and limitations alongside results. Historical failed runs remain failed; no rerun is required to turn them green.

Release comparison data can include Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM. Describe feature and endpoint differences; unsupported features are not zero-latency wins. Reproducibility means explaining what was measured, not imposing a new certification process.

## 20. Feature demonstrations and owner-machine safety

Build features and show one real end-to-end demonstration covering each ticket's goal. Add almost no tests: only when the feature cannot be shown working another way. Run only what iteration needs. No prescribed compatibility matrix, attack list, test lanes or full-suite prerequisite applies. The historical design rounds and existing development evidence are records, not current work instructions.

Use only the owner's Ubuntu laptop, M5 Mac and MacBook Air for development and the development relay. Buy, rent or subscribe to nothing. Model tokens are the only permitted expense; each pay-per-use key has a $25/month cap set at its provider. Use existing permitted accounts and disposable public third-party projects, never the owner's projects, real vault items or browser profiles. Never print, log or commit a secret.

Keep the owner's services running, including Chrome, Beeper and the T3 Code server, and leave existing Docker resources alone. Never drive visible apps or trigger password/approval dialogs on Macs. Before creating a Lamp sandbox on Ubuntu, check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available. The total across tickets is at most 2 live Lamp sandboxes; release them promptly. The Air has 8 GB and no fan, so admission must fit its actual available capacity. Any destructive storage work uses disposable fixtures, never live disks.

## 21. Feature implementation order

The Linux engine comes first: create from a Lamp template, run a command and fork with memory while both copies continue. T5's tools and T6's integration are done. The owner accepted T6 on 2026-11-10 despite the recorded failed speed result; later feature work proceeds. Repository visibility still follows the first working engine demo and the owner's release authorization.

Build storage, Mac guest adapters, custody, control, plugins and networking according to the actual feature dependencies in [the tickets](../tickets/README.md). Feature owners version formats as needed. No global format freeze or certification ticket stands between features. T31 publishes the delivered brief features and packages with honest release measurements. Before public visibility, retire private persistent repository runners so untrusted jobs cannot reach everyday owner machines; public repository CI may stay off. Lamp's CI product retains its fresh-VM and secret rules.

## 22. Product limitations and recovery behavior

| Limitation | User-visible behavior |
| --- | --- |
| Native Mac save/fork APIs | Use compatible supported readers and owner-built media. If RAM fork is unavailable, label fresh-boot disk-only creation; Linux-on-Mac retains memory fork. |
| Credential-contaminated state | Preserve same-owner private work; reconstruct clean nonsecret content before templates, exports beyond custody or sharing. Scanning arbitrary state does not make it clean. |
| Subscription vendor limits | Retain approved sign-in only within trusted custody and vendor-only egress. General task work uses a token-free sandbox; unsupported methods report their actual limitation. |
| Password secrecy uncertain | Use private human takeover and return agent control only on a safe page. Standard supported forms still provide protected fill. |
| Host sleep, lost storage or lost custody host | Show unavailable data and bounded survivor capacity. Restore only from acknowledged durable replicas or backups with fencing and explicit recovery material. |
| Public-repository jobs | Always fresh VMs and allowlists; strangers receive only the CI system's short-lived one-job token. No owner, model, vault or Lamp credentials. |
| Shared engine lineage or revoked readers | Disclose dependencies, retain compatible authorized readers and stop revoked code. Recover inert captures through a patched reader or explicit cold reconstruction. |
| Certificate capacity or hosted outage | Keep existing safe routes and queue new activation within actual CA limits. Never fall back to insecure transport or another hosted domain. |
| Compromised trusted host or credential recipient | Revoke, fence, reinstall and rotate affected credentials. A restricted app role cannot make a hostile physical host safe. |

## 23. Owner decisions

**Owner decision, 2026-11-09: “skip windows for everything.”** Windows hosts, native guests, test lanes, CLI builds and CI runner rows are outside release one. O10 is amended to Linux 5.10+ and macOS 26+ on Apple Silicon.

The owner accepted O1–O10 and A1–A5 on 2026-11-08, and the brief and ADR 0004 record them. The brief’s §22 replaces O2 with the three-owned-machine no-spending constraint; O10 uses the minimums above. The `[owner-confirmed …]` markers show which sections depend on each decision. The 2026-11-10 owner amendment removes experiment and certification prerequisites. The last column says what would have to change if the owner reverses a decision later.

| Marker | Decision / affected sections | If reversed later |
| --- | --- | --- |
| [owner-confirmed O1] | TS/Python SDKs in release one; Go/Rust immediately after (§4, §12, §21) | Change SDK delivery order; all interfaces still use the canonical schema |
| [owner-confirmed O2, replaced 2026-11-08; process amended 2026-11-10] | Use only the owner’s Ubuntu laptop, M5 Mac and MacBook Air; no new spending or required test lanes (§20; brief §22) | A different infrastructure plan needs owner authorization; no spending inferred |
| [owner-confirmed O3] | Open public internet with private/LAN/metadata blocked; stranger PR allowlist (§2, §8, §11, §16, §18) | Recompile defaults/fixtures; explicit private-service grants and immutable CI safety remain |
| [owner-confirmed O4] | Private signed-in dev URLs and expiring shares (§8.2, §11–12, §15) | Change Port defaults and admission behavior, preserving scope/revoke/origin rules |
| [owner-confirmed O5] | Authenticated opt-in exposures excluded from zero-leak score but flagged (§1, §9, §11, §17–18) | Literal all-mode zero leaks conflicts with intentional password/cookie delivery; retain blocker |
| [owner-confirmed O6, amended 2026-11-10] | Publish honest measurements once at release; no every-change/PR benchmarks or measurement blocker (§19, §24) | Any different cadence requires an owner decision |
| [owner-confirmed O7] | Opt-in numeric counts/timings only, 90-day retention (§11, §17.4) | Keep collection off until new schema/retention and consent are specified |
| [owner-confirmed O8] | Users build macOS guest templates on own hosts; no Lamp redistribution (§5–6, §20–21) | Require license/distribution evidence before another delivery path |
| [owner-confirmed O9] | Owner/admin/member/viewer roles (§11, §15.1, §17, §20) | Rename/revise action matrix and migration, never implicitly publish private content |
| [owner-confirmed O10, amended 2026-11-09] | Linux 5.10+, macOS 26+ on Apple Silicon (supported minimum; older versions outside release-one support) (§5, §20–22) | Change supported platform minimums; first-release Linux arm64/Apple Silicon remain binding |
| [owner-confirmed A1] | Own private fork/suspend keeps login; no credential leaves person's hosts/sandboxes. Templates, exports beyond custody, sharing/handoff are clean and fresh-harness (§1–2, §6–7, §9–12, §17–18) | Without amendment, arbitrary subscription state cannot both retain exact RAM and be universally stripped: C-008/R-001 remain binding blockers |
| [owner-confirmed A2] | Native macOS gets suspend/resume and labeled fresh-boot disk-only creation if supported RAM fork fails; Linux-on-Mac retains RAM fork (§2, §5–6, §11–12, §20–22) | Require supported native-macOS memory fork; unsupported VZ identity copies remain unavailable |
| [owner-confirmed A3] | Audited built-in engines; owner trusts exact third-party engine digest with warning; other plugin types never touch RAM (§2–3, §11–14, §18) | Literal bad-engine isolation cannot hold while engine owns guest RAM; C-023/R-006 remain blockers |
| [owner-confirmed A4] | Every command plus connection metadata for opaque TLS; full mediated requests; exact proxy caps, best-effort opaque usage plus hard time caps (§2, §10–11, §17) | Opaque request/token completeness remains unsatisfied; no subscription interception workaround |
| [owner-confirmed A5] | Waiting solely for person's next message is inactive; builds/dev servers/background work/open task connections are active (§6.5) | Require harness exit/verified continuation before idle suspend; no silent waiting-loop exemption |

The owner also accepted four product decisions on 2026-11-08, with B1’s process amended on 2026-11-10:

| Decision | Meaning |
| --- | --- |
| B1, amended 2026-11-10 | Speed targets are measured once at release for a standard sandbox: 2 vCPU, 4 GiB RAM, 20 GiB disk, with up to 1 GiB of recently written memory at fork. Larger sizes are measured and published separately. Measurements never block implementation, merge or release. |
| B2 | A public-PR job may hold only its CI system's own short-lived token for that one job, such as a GitHub runner job token with a read-only repository token. No other secrets. |
| B3 | Fill-on-behalf covers standard login forms. Where Lamp can't guarantee the password stays hidden from the agent, the person types it during a takeover instead. |
| B4 | "Every command" means every process started: program, arguments, user, parent process and time. It doesn't mean the meaning of commands inside scripts. |

B1–B4 retain the owner’s product decisions under the 2026-11-10 process amendment. They impose no test campaign or measurement prerequisite.

## 24. Differences from competitors

These are design choices, not measured superiority. Compare exact released versions and installation manifests at run time; dated vendor claims do not establish Lamp results.

| Reference | Primary-source baseline | Lamp's intended distinction and equivalent-work requirement |
| --- | --- | --- |
| Cloudroom | Per-thread sandbox, direct model requests and real keys accessible inside its assigned sandbox [S42] | Owner-host-only subscription custody, API-key broker and heterogeneous fleet; compare signed-in readiness without mislabeling custody differences |
| E2B Embed | Firecracker-based single-Linux-host deployment [S43] | Cross-host/OS fleet and own non-Docker templates; compare same command-ready endpoint, root persistence, network/audit and continuing-source fork |
| Docker Sandboxes | MicroVM plus credential proxy and supported-client substitution [S16][S44] | Own format/no Docker host runtime, approved opaque subscriptions and independent fleet/console model; Docker-inside is an optional guest kit |
| microsandbox | msb_krun-family multi-host-OS execution and full capture/private restore [S04][S45] | Qualify independent adapters, per-OS confinement and exact audit/credential semantics. Shared engine code is disclosed; compare mountless vs host-mounted roots and continuing vs frozen source |
| SmolVM | Lightweight krun-derived runtime with documented mounting/state limits [S46] | Explicit portable template/engine acceleration split, typed credential/task/fleet authority and matched device/disk/RAM-capture work |
| Modal / Daytona, context | Their sandbox lifecycle/API/template/credential products [S47] | Self-hosted ownership across all required OSes, Apache host components and agent sessions/sub-agents/teleport; no claim to reproduce unavailable closed internals |

Pin competitor binary/source/license, template, resources, cache state, host mounting, sandbox boundary, guard/audit, parent continuation, private-vs-copied RAM and volatile/durable completion.
Publish endpoint and feature differences next to each result. Unsupported features say unsupported, never zero latency or a timeout win. Do not aggregate incomparable boot/command/agent-ready results into one ranking.
Publish available reproducible comparison measurements once at release with their actual configurations and limitations [owner-confirmed O6, amended 2026-11-10].

## 25. Sources

Repository authority: [brief](brief.md), [glossary](../../GLOSSARY.md), [ADRs 0001–0009](../adr). Merge inputs: the three round-1 designs, requirements draft, both round-2 critiques and all six research files. This document defines the merged contract without requiring those rounds to understand it.
Web sources were checked on 2026-11-08. Versioned source establishes mechanisms, not a reproduced Lamp build, vendor permission for a different workflow or measured performance. Use current vendor contracts when implementing their integrations.

| ID | Primary source and claim supported |
| --- | --- |
| S01 | [tsnet API](https://pkg.go.dev/tailscale.com/tsnet): embedded Go endpoint, isolated state, Dial/Listen, service identities and explicit federation import; qualification pins module 1.104.5 |
| S02 | [GitHub scale-set Go client](https://github.com/actions/scaleset): standalone non-Kubernetes controller, JIT runners, demand statistic and redelivery semantics; pin commit before use |
| S03 | Firecracker 1.17.0 [snapshot lifecycle](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md), [specification](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md), [jailer](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/jailer.md), [kernel policy](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/kernel-policy.md): independent restore, private RAM, sync costs and actual scope of performance/confinement claims |
| S04 | msb_krun 0.1.45 [crate metadata](https://crates.io/api/v1/crates/msb_krun/0.1.45), pinned [VM API](https://github.com/superradcompany/libkrun/blob/344cb2488c0475e644ed21152344a408973d4369/src/krun/src/api/vm.rs), [private RAM](https://github.com/superradcompany/libkrun/blob/344cb2488c0475e644ed21152344a408973d4369/src/vmm/src/private_memory.rs), [backend state](https://github.com/superradcompany/libkrun/blob/344cb2488c0475e644ed21152344a408973d4369/src/vmm/src/execution_state.rs): released capture/private mappings/KVM/HVF mechanisms, not Lamp certification |
| S05 | Upstream libkrun 1.19.8 [README/security model](https://github.com/libkrun/libkrun/blob/v1.19.8/README.md), [C header](https://github.com/libkrun/libkrun/blob/v1.19.8/include/libkrun.h): host support, external VMM confinement, TSI/virtiofs risks and absence of downstream capture contract |
| S06 | Firecracker 1.17.0 [vhost-user block](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/api_requests/block-vhost-user.md#snapshot-support): RAM sharing and explicitly unsupported snapshots |
| S07 | Apple [VZ platform identity](https://developer.apple.com/documentation/virtualization/vzmacplatformconfiguration), [WWDC23 save/restore](https://developer.apple.com/videos/play/wwdc2023/10007/): compatible configuration, separately copied external state and original Mac/user save-file binding |
| S10 | Apple [macOS Sequoia SLA](https://www.apple.com/legal/sla/docs/macOSSequoia.pdf), §2B(iii): two additional virtual instances on qualifying Apple hardware for enumerated uses; actual host/guest license must be rechecked |
| S11 | Firecracker [snapshot uniqueness/userspace](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md#snapshot-security-and-uniqueness): clone entropy, reused credentials/state and application hooks |
| S12 | Linux [dm-thin protocol](https://docs.kernel.org/admin-guide/device-mapper/thin-provisioning.html): snapshot, pool, flush, metadata and exhaustion responsibilities |
| S13 | Apple [APFS features](https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/Features/Features.html): space-sharing file clones and copy-on-write; disk clone is not RAM capture |
| S15 | Tailscale [tsnet federation](https://tailscale.com/kb/1581/tsnet-workload-identity-federation): issuer/claim/tag/scope setup and feature import; SaaS capability is not Headscale proof |
| S16 | Docker [credential substitution](https://docs.docker.com/ai/sandboxes/configuration/credentials/): host sentinel/header proxy, declared endpoints and OAuth passthrough exception; not permission to proxy every subscription |
| S17 | AWS [SigV4](https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html): canonical request, scoped signing and different signing variants |
| S18 | MCP 2026-07-28 [authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization), [security](https://modelcontextprotocol.io/specification/2026-07-28/basic/security_best_practices): HTTP OAuth/audience/PKCE, stdio distinction and no token passthrough |
| S19 | Claude Code [legal/auth conditions](https://code.claude.com/docs/en/legal-and-compliance), [network configuration](https://code.claude.com/docs/en/network-config): unmodified hosted harness, user-owned auth/billing, subscription-custody limits and custom CA/proxy |
| S20 | OpenAI [Codex authentication](https://learn.chatgpt.com/docs/auth), [OSS self-hosted VM transfer](https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms), [session refresh](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions): protected personal transfer, stable distinct host ID and serialized refresh ownership |
| S21 | Goose [ACP provider contract](https://github.com/block/goose/blob/main/documentation/docs/guides/acp-providers.md): delegated harness auth and currently documented continuation limits; check the current revision when implementing continuation |
| S22 | Chromium [DevTools protocol](https://chromedevtools.github.io/devtools-protocol/tot/Network/): powerful cookie/network surface that remains unavailable to task callers |
| S23 | Vault host interfaces: [1Password Connect](https://developer.1password.com/docs/connect/), [Bitwarden CLI](https://bitwarden.com/help/cli/), [KeePassXC manual](https://keepassxc.org/docs/KeePassXC_UserGuide), [Proton Pass CLI](https://proton.me/pass/developer-features): retrieval/control mechanisms, not universal safe-fill certification |
| S24 | [RFC 4251 SSH architecture](https://www.rfc-editor.org/rfc/rfc4251.html): server authentication and encrypted connection channels; stock SSH does not carry HTTPS SNI |
| S25 | GitHub [OIDC reference](https://docs.github.com/en/actions/reference/security/oidc): issuer, audience and immutable repository/workflow identity claims |
| S26 | Clerk [architecture](https://clerk.com/docs/guides/how-clerk-works/overview), [Secondary deployment](https://clerk.com/docs/guides/development/deployment/changing-domains): FAPI/session distinction and subdomain-scoped deployment |
| S27 | [Cookie specification](https://datatracker.ietf.org/doc/html/draft-ietf-httpbis-rfc6265bis), [RFC 9525 §6.3](https://www.rfc-editor.org/rfc/rfc9525.html#section-6.3): host-prefix/domain semantics and one-label wildcard matching; browser origin and host-prefix semantics |
| S28 | Let's Encrypt [limits and ARI](https://letsencrypt.org/docs/rate-limits/): global registered-domain 50/7-day limit, token-bucket behavior, exact-set limits and renewal treatment |
| S29 | GitHub [Copilot Actions environment](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-environment): setup workflow and supported self-hosted route, distinct from other cloud modes |
| S30 | [Claude Code Action setup](https://github.com/anthropics/claude-code-action/blob/main/docs/setup.md), OpenAI [Codex GitHub Action](https://developers.openai.com/codex/github-action): official action/auth contracts; use compatible official action versions |
| S31 | GitLab [Fleeting](https://docs.gitlab.com/runner/fleet_scaling/fleeting/), [Custom executor](https://docs.gitlab.com/runner/executors/custom/): provider and prepare/run/cleanup interfaces |
| S32 | Buildkite [agent start](https://buildkite.com/docs/agent/cli/reference/start): disconnect-after-job and native agent options |
| S33 | Jenkins [core extension points](https://www.jenkins.io/doc/developer/extensions/jenkins-core/): Cloud provisioning/retention implementation seam |
| S34 | Forgejo [registration](https://forgejo.org/docs/latest/admin/actions/registration/), Gitea [act_runner](https://docs.gitea.com/1.25/usage/actions/act-runner/): separate ephemeral execution/version contracts |
| S35 | CircleCI [machine runner 3 config](https://circleci.com/docs/runner/machine-runner-3-configuration-reference/): single-task agent mode and resource-class contract |
| S36 | Azure [Linux agent](https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/linux-agent?view=azure-devops): native agent lifecycle and run-once mode |
| S37 | Bitbucket [runner contract](https://support.atlassian.com/bitbucket-cloud/docs/runners/): registration, labels and supported execution models; one-job deletion is Lamp's obligation |
| S38 | Woodpecker [agent configuration](https://woodpecker-ci.org/docs/administration/configuration/agent): workflow/concurrency/shutdown controls |
| S39 | Slack [request verification](https://docs.slack.dev/authentication/verifying-requests-from-slack/), Linear [webhooks](https://linear.app/developers/webhooks): signed-source and replay checks |
| S40 | GitHub [secure Actions use](https://docs.github.com/en/actions/reference/security/secure-use): untrusted pull-request code and privileged workflow risk |
| S42 | Cloudroom [security](https://www.cloudroom.dev/security): per-thread sandbox, direct model traffic, real keys accessible within assigned sandbox and shared-subagent limitations |
| S43 | E2B [Embed announcement](https://e2b.dev/resources/introducing-e2b-embed): single-machine Firecracker stack and published feature exclusions |
| S44 | Docker [sandbox architecture](https://docs.docker.com/ai/sandboxes/architecture/): microVM architecture and credential boundary |
| S45 | microsandbox 0.7.14 [snapshots](https://github.com/superradcompany/microsandbox/blob/v0.7.14/docs/sandboxes/snapshots.mdx): continuing-source capture/private restore and nonconstant preparation costs |
| S46 | SmolVM 1.34.0 [limitations](https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/limitations.md): host mounts and device/state limits |
| S47 | Modal [sandbox guide](https://modal.com/docs/guide/sandboxes), Daytona [sandbox lifecycle](https://www.daytona.io/docs/en/sandboxes/): public product contracts; no inference about inaccessible internals |

Round-5 primary sources below were fetched on 2026-11-08. They describe mechanisms, not a completed Lamp implementation.

| ID | Primary source and claim supported |
| --- | --- |
| S48 | [RFC 8657](https://www.rfc-editor.org/rfc/rfc8657.html), [Let's Encrypt CAA](https://letsencrypt.org/docs/caa/), [CT monitoring](https://certificate.transparency.dev/howctworks/): accounturi/validationmethods constrain both issue/issuewild; CAA follows CNAME/nearest policy and grants are additive. CT monitors detect logged issuance; they do not prevent it. |
| S49 | [Firecracker 1.17 snapshot input trust](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md#snapshot-files-management): snapshot files are trusted input; CRC protects only partial accidental corruption. Lamp must authenticate producer authority before restore. |
| S50 | Apple XNU [fsync](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/fsync.2), [fcntl](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/fcntl.2): fsync can leave volatile reordered device writes; F_FULLFSYNC drains the queue and persists earlier fsynced writes on that device, including APFS support. Device honesty and exact directory ordering still need qualification. |
| S51 | SQLite [fullfsync](https://www.sqlite.org/pragma.html#pragma_fullfsync), [checkpoint_fullfsync](https://www.sqlite.org/pragma.html#pragma_checkpoint_fullfsync), [synchronous](https://www.sqlite.org/pragma.html#pragma_synchronous): FULL WAL commit durability differs from NORMAL; Mac fullfsync is off by default and applies to checkpoints too when enabled. |
| S52 | Linux [fsync directory contract](https://man7.org/linux/man-pages/man2/fsync.2.html): explicit data/metadata/device barriers; calling them does not itself certify an arbitrary backing stack. |
| S54 | [SOCKS5 RFC 1928 §§5/7](https://www.rfc-editor.org/rfc/rfc1928.html): numeric IPv4/IPv6 and domain destination forms, including UDP; a second name resolution is a different dial decision. |
| S55 | WHATWG [Fetch](https://fetch.spec.whatwg.org/#goals): navigation is only one browser network producer; subresources, workers and beacons also need task-policy enforcement. |
| S56 | Linux [CLOCK_BOOTTIME](https://docs.kernel.org/core-api/timekeeping.html), Apple [mach_continuous_time source](https://github.com/apple-oss-distributions/xnu/blob/main/osfmk/mach/mach_time.h), Go [monotonic-clock limits](https://pkg.go.dev/time#hdr-Monotonic_Clocks): suspend-aware clocks; power transitions must preserve authority expiry. |
| S57 | [TUF specification](https://theupdateframework.github.io/specification/latest/): threshold root/target trust, delegated roles, expiry, monotonic metadata and sequential root rotation. TUF does not bootstrap trust in an arbitrary initial installer. |
| S58 | SQLite [WAL checkpoint starvation](https://www.sqlite.org/wal.html#avoiding_excessively_large_wal_files), [SQLITE_FULL](https://www.sqlite.org/rescode.html#full): overlapping readers can prevent WAL recycling; database/temp space failures require physical reservation and bounded readers. |
| S59 | Apple [nested virtualization probe](https://developer.apple.com/documentation/virtualization/vzgenericplatformconfiguration/isnestedvirtualizationsupported): M3-and-later nested Linux support; actual API support determines available nesting. |

[S01]: https://pkg.go.dev/tailscale.com/tsnet
[S02]: https://github.com/actions/scaleset
[S03]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md
[S04]: https://github.com/superradcompany/libkrun/tree/344cb2488c0475e644ed21152344a408973d4369
[S05]: https://github.com/libkrun/libkrun/blob/v1.19.8/README.md
[S06]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/api_requests/block-vhost-user.md
[S07]: https://developer.apple.com/videos/play/wwdc2023/10007/
[S10]: https://www.apple.com/legal/sla/docs/macOSSequoia.pdf
[S11]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md#snapshot-security-and-uniqueness
[S12]: https://docs.kernel.org/admin-guide/device-mapper/thin-provisioning.html
[S13]: https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/Features/Features.html
[S15]: https://tailscale.com/kb/1581/tsnet-workload-identity-federation
[S16]: https://docs.docker.com/ai/sandboxes/configuration/credentials/
[S17]: https://docs.aws.amazon.com/IAM/latest/UserGuide/reference_sigv.html
[S18]: https://modelcontextprotocol.io/specification/2026-07-28/basic/security_best_practices
[S19]: https://code.claude.com/docs/en/legal-and-compliance
[S20]: https://developers.openai.com/siwc/token-sharing-open-source/self-hosted-vms
[S21]: https://github.com/block/goose/blob/main/documentation/docs/guides/acp-providers.md
[S22]: https://chromedevtools.github.io/devtools-protocol/tot/Network/
[S23]: https://developer.1password.com/docs/connect/
[S24]: https://www.rfc-editor.org/rfc/rfc4251.html
[S25]: https://docs.github.com/en/actions/reference/security/oidc
[S26]: https://clerk.com/docs/guides/development/deployment/changing-domains
[S27]: https://datatracker.ietf.org/doc/html/draft-ietf-httpbis-rfc6265bis
[S28]: https://letsencrypt.org/docs/rate-limits/
[S29]: https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-environment
[S30]: https://github.com/anthropics/claude-code-action/blob/main/docs/setup.md
[S31]: https://docs.gitlab.com/runner/fleet_scaling/fleeting/
[S32]: https://buildkite.com/docs/agent/cli/reference/start
[S33]: https://www.jenkins.io/doc/developer/extensions/jenkins-core/
[S34]: https://forgejo.org/docs/latest/admin/actions/registration/
[S35]: https://circleci.com/docs/runner/machine-runner-3-configuration-reference/
[S36]: https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/linux-agent?view=azure-devops
[S37]: https://support.atlassian.com/bitbucket-cloud/docs/runners/
[S38]: https://woodpecker-ci.org/docs/administration/configuration/agent
[S39]: https://docs.slack.dev/authentication/verifying-requests-from-slack/
[S40]: https://docs.github.com/en/actions/reference/security/secure-use
[S42]: https://www.cloudroom.dev/security
[S43]: https://e2b.dev/resources/introducing-e2b-embed
[S44]: https://docs.docker.com/ai/sandboxes/architecture/
[S45]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/docs/sandboxes/snapshots.mdx
[S46]: https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/limitations.md
[S47]: https://modal.com/docs/guide/sandboxes

[S48]: https://www.rfc-editor.org/rfc/rfc8657.html
[S49]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md#snapshot-files-management
[S50]: https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/fcntl.2
[S51]: https://www.sqlite.org/pragma.html#pragma_fullfsync
[S52]: https://man7.org/linux/man-pages/man2/fsync.2.html
[S54]: https://www.rfc-editor.org/rfc/rfc1928.html
[S55]: https://fetch.spec.whatwg.org/#goals
[S56]: https://docs.kernel.org/core-api/timekeeping.html
[S57]: https://theupdateframework.github.io/specification/latest/
[S58]: https://www.sqlite.org/wal.html#avoiding_excessively_large_wal_files
[S59]: https://developer.apple.com/documentation/virtualization/vzgenericplatformconfiguration/isnestedvirtualizationsupported
