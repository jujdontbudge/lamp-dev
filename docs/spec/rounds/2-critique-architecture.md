# Round 2: architecture critique

Status: complete. Round 3 must answer all 43 findings.

Scope: the speed, security and simplicity designs from round 1. The brief, glossary and ADRs bind the merged design. The requirements draft and research are evidence, not authority over those decisions.

The ten pending owner decisions remain proposals. Recommendations below may assume them for experiments, but must not silently promote them into binding policy.

## Review method

Each finding gets a stable C-number, severity, affected design, evidence and a resolution. Critical means a binding decision is violated. High means a central feasibility, correctness or security claim lacks a safe implementation. Medium means a material specification gap. Low means a smaller clarity issue.

Shorthand: **Sp** = speed, **Se** = security, **Si** = simplicity. Section references identify the reviewed design's own sections. Sources were checked on 2026-11-08. API support is not measured Lamp performance.

## 1. System overview

| Sp | Se | Si |
| --- | --- | --- |
| Rust execution authority; local admission; Go access helpers | Rust policy core; separated supervisor, guard and brokers | Go control; Rust workers; embedded database/coordination |

- **C-001 — medium — all, overview and build plans.** The diagrams hide required infrastructure behind optional-looking workers. A normal install needs privileged resource setup, a credential identity, network enforcement and sometimes a storage sandbox. None gives a measured clean-install resource/startup budget. A fleet of one on a laptop could consume its useful capacity before tasks start. **Resolve:** publish the default process/resource inventory and a clean-machine install, restart and uninstall acceptance test. Keep unused services stopped.
- **C-002 — high — all, lifecycle and ownership.** Control failover is described much more precisely than recovery after losing the only host holding disks, transcripts, credentials or idempotency receipts. Users can recover coordination while permanently losing work. **Resolve:** expose durability and recovery-point classes per resource; define backup/export, restore and lost-host reconciliation. Never infer data recovery from control availability.

**Strongest:** Si's small Go control surface plus Se's explicit authority boundaries. Keep bulk RAM, packets and terminal data outside the replicated metadata path, as all three recommend.

## 2. Languages

| Sp | Se | Si |
| --- | --- | --- |
| Rust daemon, policy, execution; Go tsnet/CI adapters | Rust throughout trusted core; small Go adapters | Go daemon/brokers/relay; Rust engines and guest service |

- **C-003 — medium — Sp/Se §2; Si §3.** None measures its language argument. Rust avoids a Go heap in the VMM in all three designs; that does not establish a Rust HTTP controller advantage. Si also still specifies a separate Go access process, so “direct embedding” does not eliminate that boundary. **Resolve:** choose ownership and maintainability first; measure bounded RPC and control allocation latency. Do not attribute VM I/O speed to the daemon language.

**Strongest:** Si's Go control plane and Rust engines. The final language decision below explains the boundary and its limits.

## 3. Engines

| Sp | Se | Si |
| --- | --- | --- |
| Firecracker first; CH for I/O/devices; downstream krun on Mac/Windows | Same basic split; strongest confinement requirements | Shared `msb_krun` 0.1.45 first; Firecracker comparator |

- **C-004 — high — all; especially Si §4.** A usable downstream core exists, but the designs lack qualification of its exact shipped configuration. Upstream libkrun 1.19.8 documents Linux/KVM and Mac/HVF; its C header has no capture/restore API or Windows backend [P01]. `msb_krun` 0.1.45 was published on November 7, is Apache-2.0 and exposes paused CPU/device/RAM capture [P02]. Its RAM code has Unix private mappings and a Windows private-view path [P03]. Microsandbox documents continuing-source full capture and fork, including Windows [P04]. This rebuts “krun cannot fork”; it does not prove hardened Lamp support or timing on any host. **Resolve:** pin the complete crate/kernel/device graph, reproduce continuing-parent forks on each host, audit confinement, and retain independent engine adapters. Do not commit all hosts to one unqualified release.
- **C-005 — critical — all, native macOS fork.** VZ save/restore is not evidence for an independent live macOS fork. Apple requires the saved hardware model, machine identifier and auxiliary storage to be restored unchanged; multiple VMs should have unique identities [P05]. Saved state is tied to the original Mac/user [P06]. Si admits this blocker; Sp/Se's generic VZ validation does not solve it. The brief §6 requires running memory forks without excluding this guest class. **Resolve:** prove two independently writable, concurrently running instances through supported APIs. Otherwise retain an explicit binding-scope blocker for the owner; disk-only creation is not compliance.
- **C-006 — high — all, Windows backend choice.** WHP, HCS and the full Hyper-V management role are conflated. WHP exposes memory/register primitives; HCS explicitly offers VM save/restore [P07][P08]. Canonical's HCS backend supports Windows Home [P09], while Microsoft's full Hyper-V role excludes Home [P10]. Se's automatic weak fallback for unsupported editions and Si's Pro-only certification may unnecessarily reject a real VM path. HCS documentation alone does not prove independent Windows-guest fork. **Resolve:** separately probe WHP, HCS and Hyper-V per edition/build/guest; compare eligible backends, including Home, before declaring a fallback.
- **C-007 — medium — Se §1, Si §4; Sp §3.** Se proposes Linux 6.6/macOS 26/Server 2025; Si proposes Linux 6.12/macOS 26/24H2/Server 2025. These exceed likely minimums of 5.10/14/Windows 11/Server 2022. Those owner choices remain pending, so this is not yet a binding violation. Sp's 6.12 guest also needs separate qualification against Firecracker's documented supported/backported kernels [P11]. **Resolve:** distinguish host minimum, guest kernel and tested release; budget older-OS lanes and obtain an explicit support decision.

**Strongest:** Sp/Se's per-host certified selection, with Si's shared worker protocol. Use Firecracker as the Linux reference; let the downstream core win eligible rows only with evidence. HVF maps caller-owned host memory but supplies no complete VM serializer [P12]. VZ owns higher-level state, with save restrictions and no demonstrated public RAM-sharing contract. Neither framework name proves cheap CoW fork.

## 4. Templates, create and fork

| Sp | Se | Si |
| --- | --- | --- |
| Own chunks/extent map; prepared restore; staged dirty capture | Own chunks; stricter capture/identity barriers; dm-thin on ext4 | TOML; raw/qcow2 layers; full capture before incremental |

- **C-008 — critical — all, subscription capture.** The designs correctly admit that hostile task code can hide a subscription bearer in arbitrary RAM/files. Stopping the harness or zeroing freed pages cannot erase those copies. Their safe refusal therefore leaves brief §§6/9/13 and ADR 0004 jointly unsatisfied for ordinary signed-in mid-task fork/suspend. Restarting the harness also changes the glossary's exact-memory-copy meaning. **Resolve:** keep this as a release blocker, with a precise supported state contract and a tested compartment scheme, or an explicit owner amendment. A refusal is a security success and a feature failure, never a completed requirement.
- **C-009 — critical — Si §5.** Exempting a “quiescent harness control loop” from idle detection contradicts brief §9: running/background processes count as activity. Sp/Se instead require the waiting harness to exit into continuation state. **Resolve:** remove the extra exemption or ask the owner to amend the idle definition; retain only clearly identified Lamp infrastructure.
- **C-010 — high — all, template-save sanitation.** Removing credential paths and regenerating clean boot RAM does not remove a token encoded into an ordinary task file, package layer or journal. This affects disk-only templates too. **Resolve:** propagate secret-exposure taint to disk captures and artifacts, not just RAM capture. Use a clean reconstruction from declared nonsecret inputs, or refuse the save; a literal-byte scan cannot clear taint.

**Strongest:** Si's explicit full-capture-first sequence, Se's fail-before-write sanitation, and Sp's separation of portable templates from engine acceleration. Keep external-volume and device semantics explicit.

## 5. Storage

| Sp | Se | Si |
| --- | --- | --- |
| Reflink/custom extent map; confined file service | Reflink/dm-thin/deltas; confined shared-files service | Reflink/qcow2; Samba in a dedicated Linux storage sandbox |

- **C-011 — high — Sp §§4–5, Se §§4–5.** Their proposed extent backend needs a snapshot-compatible integration. Firecracker 1.17 supports both a file-backed `path_on_host` and an external vhost-user-block socket [P13]. However, its vhost-user path is preview, shares guest RAM with the backend and explicitly cannot snapshot [P32]. It cannot supply the proposed custom block path plus RAM fork unchanged. Si's existing raw/qcow2 backend is a more concrete seam, but still needs atomic writable-head rotation. **Resolve:** identify exact backend interfaces and required changes, then crash-test barriers, discard, flush, compaction and recovery before relying on their speed/security.
- **C-012 — medium — Si §6.** One Samba sandbox per host adds a Linux VM, patching, boot dependencies and memory to a supposedly small install. It also fails on a nonvirtualizing host unless remote storage is available. SMB POSIX/case/locking behavior is not identical to a private Linux disk. **Resolve:** make that cost and fallback explicit; test real build/database workloads and default to local private disks where sharing is unnecessary. Do not fold an equivalent-network-mount comparison into the binding native-disk score.
- **C-013 — high — all, volume replication.** “Optional synchronous replication” or a fenced storage host is not a replication protocol. Crash consistency, acknowledged-write durability, lock recovery and sole-writer fencing across partitions remain undefined. **Resolve:** specify the commit point and recovery oracle. Admit only the durability class actually implemented; a second copy without write-order and fencing proof is not HA storage.

**Strongest:** native CoW for private roots; Si's bounded existing block layers as the portable candidate; one explicit shared-files service for multiattach. Avoid inventing a general distributed filesystem.

## 6. Network

| Sp | Se | Si |
| --- | --- | --- |
| Host guard; kernel fast path; tsnet per identity | Most explicit transport binding and fail-closed rules | Same guard model; shared userspace stack on Mac/Windows |

- **C-014 — high — all, private membership.** A `tsnet.Server` is a userspace endpoint, not a guest TAP or a general packet router. `Listen`/`Dial` expose connections; allocating a server alone does not give guest applications an independent private-network device [P14]. **Resolve:** specify guest-to-tsnet TCP/UDP forwarding, inbound port mapping, DNS, identity attribution and unsupported protocols. Prove a peer sees the correct sandbox identity and cannot reach the host or another sandbox.
- **C-015 — medium — all, network defaults.** Se requires explicit internet destinations; Si defaults to public HTTP(S)/package/Git endpoints; Sp permits public web/package traffic. These differ from the likely open-internet default and from one another. **Resolve:** keep the pending owner choice visible; propose public egress with private/metadata blocking and public-PR allowlists. Define raw TCP/UDP separately from strict site filtering. No default changes are authorized by this review.

**Strongest:** Se's mandatory external guard, Sp's measured Linux kernel path, and Si's reused Mac/Windows packet stack. TSI removal is necessary, but the downstream source already defaults its internet hijack off; audit the exact fork rather than blindly applying upstream defaults [P02].

## 7. Credentials and connections

| Sp | Se | Si |
| --- | --- | --- |
| TLS sentinels; typed signing/brokers; capture taint | Strongest scopes, private fill/takeover and response tests | Similar broker; fewer core languages; explicit unresolved limits |

- **C-016 — high — all, harness compatibility.** No design supplies the promised per-version TLS-interception/auth matrix. A client accepting a CA is weaker evidence than successful sentinel substitution, streaming, refresh and tool use. “Pinned clients use adapters” is a task, not coverage. **Resolve:** certify the rows below against official binaries and selected auth modes. Do not inject real keys as a compatibility fallback.
- **C-017 — high — all, CLI/MCP adapters.** SigV4 signing, token-minting endpoints, presigned URLs, streaming payloads and inside-MCP delegated tokens have different authority semantics. A generic credential connection cannot safely imply complete `aws`, `gh`, `stripe` or arbitrary MCP compatibility. **Resolve:** publish supported operations and denied credential-export/mint paths, with schema-bound requests, resource policies and negative tests. A required limited upstream bearer must be classified separately from an isolated API key.
- **C-018 — high — all, browser/password kits.** Narrow browser controls protect cookies better than raw CDP, but no design names the supported authenticated-site/fill workflows. Hiding login output does not stop a later account page, download or approved form from exposing credentials. Each design admits that gap. **Resolve:** use tested origin/operation adapters, verify useful authenticated tasks, and label unsupported workflows. A service-account password retrieval API is not a safe fill API. Keep the opt-in-exposure interpretation pending.

### Independently checked harness transport evidence

“Plausible” below means an official transport surface exists, not that Lamp passed a live test. No blanket certificate-pinning claim is supported.

| Named harness | Primary evidence and conclusion for the proposed proxy |
| --- | --- |
| Claude Code | Official HTTPS proxy/custom-CA settings, including `NODE_EXTRA_CA_CERTS`; API-key route is plausible. Desktop-managed sessions and background agents have distinct configuration scope [P15]. Subscription traffic must follow ADR 0004 instead. |
| Codex | Official `CODEX_CA_CERTIFICATE`, fallback `SSL_CERT_FILE`, covers login, HTTPS and secure WebSockets. API mode is plausible; Node-only trust setup is insufficient [P16]. |
| OpenCode | Official proxy/CA support; its managed background service has a separate environment. Configure both CLI and service, plus provider/auth precedence [P17]. |
| Cursor CLI | Official proxy variables, `NODE_USE_ENV_PROXY=1` and custom CA; documented user/service API keys. Plausible, but endpoint and streaming tests remain [P18]. |
| Copilot CLI | Official SDK source has proxy/custom-CA test configuration. Upstream reports record version-specific TLS regressions. Test exact binary/build; do not assume one Node setting works forever [P19]. Entitlement and token type remain separate. |
| Amp | Official settings document proxy/custom-CA variables. Its service API credential is distinct from direct provider keys; test all live streaming/WebSocket paths [P20]. |
| Goose | Official proxy support; ACP delegates transport/auth to another installed harness. Test both processes. ACP currently documents no session fork/resume, a separate teleport blocker [P21]. |
| Gemini CLI | Official configuration supports a proxy; API/Vertex routes are plausible. Custom-CA behavior and provider SDKs need exact-build tests. Consumer subscription eligibility cannot be inferred from old login instructions [P22]. |
| Pi | Upstream provider code/docs expose HTTP(S) proxy configuration and provider adapters. That is not a complete CLI CA/auth certificate. Pin the Pi/provider/runtime version and test it [P23]. |
| Devin | An official local CLI exists. Official troubleshooting explicitly supports TLS-inspecting proxies through the OS CA store, including mitmproxy [P24]. Interception is plausible; the credential type, header substitution and local/cloud handoff still need certification. |
| Droid | Official proxy/custom-CA configuration and Factory API-key route make interception plausible. Certify Factory and BYOK routes independently [P25]. |

Docker documents host-side sentinel/header substitution for several of these clients, including the fact that header replacement does not replace bodies and OAuth passthrough returns real tokens [P26]. That is a strong technical precedent, not a Lamp conformance result or permission to proxy subscriptions.

**Strongest:** Se's operation-scoped broker and private human takeover, with Si's explicit client/runtime configuration. API-key mode should be the first capture-safe reference workflow; subscription support still owes C-008.

## 8. Sessions and consoles

| Sp | Se | Si |
| --- | --- | --- |
| Replay cursors; writer leases; git return; logical teleport | Strong watch/input/approval separation and fencing | Clearest stock SSH relay-port plan and location epochs |

- **C-019 — high — all, teleport.** A generic PTY or transcript does not provide harness continuation. Goose's documented ACP route currently lacks session resume/fork [P21]. Public T3 Code/Herdr contracts and every named harness's continuation direction remain unqualified. **Resolve:** pin concrete console and harness adapters; prove laptop→sandbox→laptop continuity, pending tool outcomes and single-writer recovery. Unsupported continuation remains a coverage gap.
- **C-020 — high — all, sub-agent visibility.** The explicit Lamp sub-agent API does not capture sub-agents a harness starts through its own internal mechanism. Those can bypass separate-sandbox selection, session discovery and descendant budgets. **Resolve:** define a supported spawn hook for each harness, or an observable same-sandbox session adapter; prove every native sub-agent appears independently and charges the parent. Do not rename one aggregate PTY “every session.”

**Strongest:** Se's authorization model, Si's relay-port routing, and Sp's git-return semantics. A raw SSH connection has no HTTP SNI; keep the explicit standard port/address solution.

## 9. API, MCP and CLI

| Sp | Se | Si |
| --- | --- | --- |
| OpenAPI; durable home receipts; permanent key tombstones | Common operation layer and stricter scopes | OpenAPI plus event cursor vectors; 30-day tombstones |

- **C-021 — critical — Se §9, Si §10.** Se retains completed records for at least seven days; Si retains results/tombstones for at least 30 days. Neither defines rejection of an old key after eviction. The same lost request retried later can create another sandbox, violating brief §3's no-duplicate retry rule. Sp gives permanent tombstones or reject-after-expiry keys. **Resolve:** adopt that rule, including imported keys, backup restore, failover and resource deletion. Bounded result retention must not mean bounded deduplication safety.
- **C-022 — medium — all, API parity.** OpenAPI alone does not generate PTY leases, binary file transfer, browser frames, SSH behavior or MCP event subscriptions. These surfaces can drift even with shared JSON types. **Resolve:** specify one operation/stream contract, map each interface's framing and cancellation, and run a generated operation-and-scope parity matrix. Treat SDK release order as pending, not a mandate for four SDKs in release one.

**Strongest:** one authorization/operation layer from all three, Sp's durable deduplication semantics, and Si's explicit resync/event model.

## 10. Plugins

| Sp | Se | Si |
| --- | --- | --- |
| Native processes with OS confinement; typed grants | WASI or service sandboxes; native trusted adapters | WASI default; narrow native adapters; JSON-RPC |

- **C-023 — critical — all, plugin trust contract.** Brief §12 says a bad plugin cannot reach into sandboxes. All three admit engine plugins read guest RAM and vault plugins see granted secrets, then reinterpret that rule as no *extra* access. That is sensible engineering, but changes the literal binding promise. **Resolve:** explicitly distinguish sandboxed extension controllers from trusted engine/credential implementations in the merged contract and obtain owner resolution of the exception. Process separation cannot prove the original statement for malicious engines.
- **C-024 — medium — Se/Si, plugin packaging.** A WASI-only default is narrower than an arbitrary separately run program. Native-only vendor SDKs, CI tools and network adapters need real confinement, update and permission-change paths on each OS. **Resolve:** publish both tiers, installation trust labels and conformance tests; avoid implying the Wasmtime boundary covers native workers. Preserve Apache licensing for first-party host components and dependencies' own notices.

**Strongest:** Se/Si's sandboxed extension tier plus Sp's explicit trusted-role model. Keep engine/network/security enforcement authority in independently audited components.

## 11. Fleet

| Sp | Se | Si |
| --- | --- | --- |
| No elected scheduler; home-host receipts and quota slices | Raft for global policy; bounded delegated local permits | Raft placement; third voter needed for two-host write failover |

- **C-025 — critical — Si §12.** A two-host fleet loses new allocations when either voter disappears unless the owner adds a third host. This contradicts automatic coordination transfer with sleeping laptops in brief §5/ADR 0001. The CAP observation is correct; requiring an extra always-on machine does not fulfill the binding product behavior. **Resolve:** adopt Se's predelegated disjoint local permits or a comparably proven design for surviving-host work. Explicitly separate ordinary host-loss availability from impossible unlimited writes in both partitions.
- **C-026 — high — Sp §11; Se/Si permit paths.** Sp's signed restrictive merges, immutable home routing and policy leases lack an executable consistency model. “Merge restrictively” does not define concurrent ownership changes, invitations, quota return, approval consumption or revoked-but-partitioned hosts. Se/Si's delegated permits also need a formal boundary from global commits. **Resolve:** write state machines for grants, permits and fencing; model partitions/sleep/rejoin. State which operations can proceed, which wait, and who can issue fresh authority without quorum. Keep uncertain keyed work unavailable rather than duplicate it.

**Strongest:** Se's Raft policy plus bounded local permits. Borrow Sp's local admission and bulk-path separation. Do not combine the two consistency systems without specifying a single authority model.

## 12. Access, auth, relay and domains

| Sp | Se | Si |
| --- | --- | --- |
| Pinned inner TLS; independent issuance; ≤40 new fleet certs/week | Most complete hostile-relay model; possible PSL request | Concrete Clerk secondary-domain setup; wildcard per fleet |

- **C-027 — high — all, tsnet access/failover.** `ListenService` requires tagged host identities, a defined service and approvals; federation needs configured issuer/claims/scopes and an explicit Go feature import [P14][P27]. `ControlURL` alone proves neither those features on Headscale nor keyless GitHub join there. Multiple sandbox nodes add enrollment latency and resource/control-plane costs. **Resolve:** pin tsnet, ship an explicit enrollment/bootstrap flow, isolate each state directory and auth environment, and qualify SaaS/Headscale separately. Measure node churn, memory, propagation and DERP paths outside the local create budget. No silent fallback to a stored CI secret.
- **C-028 — high — Sp/Se §12.** `__Host-` application cookies do not automatically protect Clerk's own FAPI/session cookies. Official Clerk docs describe JavaScript-set application sessions and distinguish primary from secondary subdomain deployment [P28]. Si provides the strongest concrete configuration. **Resolve:** keep one shared Eilmer identity, scope Clerk to the trusted account subtree, exchange into product-local host-only sessions, and test actual emitted cookies and FAPI origins in all supported browsers. This is an umbrella deployment constraint, not just a Lamp header filter.
- **C-029 — high — all; especially Si §13.** Native pinning protects against relay key substitution; ordinary browser TLS still trusts DNS/CA issuance and delivered application code. A relay process able to request replacement wildcard certificates could impersonate a host. Sp/Se separate issuance, but do not specify its complete authorization/recovery protocol; Si states the trust boundary without enforcing that separation. **Resolve:** isolate relay and DNS/ACME authority, require fleet-authorized exact-name CSRs, and test malicious certificate/routing/enrollment requests. State the narrower relay-process-compromise claim clearly.
- **C-030 — medium — all, certificates.** Per-fleet wildcards solve sandbox churn, not fleet onboarding scale. Under ADR 0006 all fleets share the default 50 new certificates per registered domain per seven days; a wildcard spans one DNS label only [P29]. Se's PSL application and every design's quota increase remain external approvals. Si reserves no numeric cross-product headroom. **Resolve:** use one correct-depth wildcard per fleet, a global issuance ledger with repair/product reserves, ARI-aware renewal, and measured outage handling. Obtain capacity before unrestricted onboarding. Stay entirely under `eilmer.si`; never distribute one platform wildcard key to unrelated fleets.

**Strongest:** Se's relay trust boundary and fleet grants, Si's concrete Clerk isolation, Sp/Se's issuance headroom. Add Si's one-use OIDC audience challenge; keep local/OIDC auth independent of Clerk for non-relay fleets.

## 13. CI

| Sp | Se | Si |
| --- | --- | --- |
| Detailed native adapter table and isolated untrusted caches | Strongest trusted-intake/public-PR boundary | Clear one-job state machine and scale-set demand metric |

- **C-031 — high — all, named CI support.** Ten plugin sketches do not certify ten CI families. Bitbucket teardown, Jenkins-side code, GitLab executor modes, Forgejo/Gitea version differences and Copilot's supported scale-set route are material implementations. GitHub's standalone scale-set library is real and does not require Kubernetes [P30]; it is not an adapter for the other systems. **Resolve:** pin controller/runner/action/OS versions and run success, cancellation, lost-controller and orphan cleanup through each native service. Prove fresh execution and result/artifact behavior, not just API invocation.
- **C-032 — medium — all, public-PR credentials.** All allow necessary short-lived runner/checkout capabilities inside untrusted execution, while brief §18 says “no secrets.” This is a reasonable distinction from owner credentials, but remains an unresolved definition, not automatic compliance. **Resolve:** list each exposed job capability, its audience/expiry/permissions and brokerability; obtain a precise no-owner-secret interpretation. Test cross-job access and token reuse. Never grant owner/model/vault authority because a job needs transport authentication.

**Strongest:** Se's forge-verified intake and immutable untrusted policy, Si's one-job lifecycle, and Sp's per-vendor execution details. Keep optional Docker entirely inside the task sandbox.

## 14. Observability

| Sp | Se | Si |
| --- | --- | --- |
| Host audit; strict admission on gaps; broker token budgets | Most explicit audit trust and opaque-accounting limits | Small dashboard; descendant ledger; coarse opt-in telemetry |

- **C-033 — critical — all, audit.** Flow records for opaque TLS and syscall exec records for interpreters are weaker than brief §19's “every command and network request.” Each design acknowledges this while allowing such paths. An unload-protected guest audit hook alone does not define semantic shell commands, in-process code or requests over an encrypted stream. **Resolve:** define the required event granularity with the owner; otherwise keep a release blocker. Preserve subscription TLS under ADR 0004. Label visibility per mode and fail closed on missing required events; never call a flow one audited request.
- **C-034 — high — all, token ceilings.** Hard caps cannot be enforced from untrusted harness counters or unobserved outside-agent/subscription traffic. All designs admit unmetered modes, but the first-release budget feature still needs a usable contract. **Resolve:** specify supported metering/admission per route, reserve worst-case request usage before dispatch, aggregate descendants, and reject only explicitly unsupported hard-cap combinations. Separate best-effort reporting and prove actual stop behavior.
- **C-035 — medium — all, usage data.** Retention and exact opt-in fields are not settled. Si adds versions to counts/timings; the pending recommendation allows counts/timings only for 90 days. Relay security metadata is separate but can still identify users. **Resolve:** keep collection off; publish proposed schemas, expiry/deletion/queued-opt-out behavior and relay metadata retention. Do not silently adopt the pending recommendation or expand it.

**Strongest:** Se's acknowledged audit limits and external sink, Si's small live dashboard, and a shared task ledger from all three. Complete attribution before adding analytics.

## 15. Security model

| Sp | Se | Si |
| --- | --- | --- |
| Strong attack model; broad native plugin confinement | Most complete TCB, trust classes and hostile-output tests | Clear secret absence and native/WASI distinction |

- **C-036 — high — all, platform confinement.** Dedicated identities, Job Objects and signed Mac workers do not by themselves prove filesystem/network isolation. Upstream libkrun explicitly treats guest and VMM as one security context [P01]. A signed entitled worker or restricted token can still hold ambient handles, IPC or driver access. **Resolve:** publish the actual sandbox profile/token/ACL/handle inventory per worker and OS; test escape, neighbor access, broker access and crash-dump paths on real hardware. Do not copy Firecracker's Linux jailer assurance to HVF/WHP.
- **C-037 — high — all, revocation and retained capabilities.** Deleting a key or closing a console stream does not revoke a spawned process, cached provider response, active network stream or in-flight approved action. The proposals mention leases but do not provide one complete revocation matrix across these objects. **Resolve:** bind every derived grant to task/sandbox generation and parent grant, list online/offline revocation deadlines, and test cancellation while HTTP/2, WebSocket, browser, MCP and shared-volume operations are active. Report irreversible completed effects honestly.

**Strongest:** Se's trust model and absence-based secret protection. Count intended raw-secret opt-ins separately only if the pending owner decision is accepted; do not claim an unconditional all-mode zero-leak result today.

## 16. Performance budget

| Phase, milliseconds | Sp §16 | Se §16 | Si §18 |
| --- | --- | --- | --- |
| Reference RAM / vCPU | 1 GiB / 2 | 1 GiB / 2 | 2 GiB / 2 |
| Create: auth and durable admission | 40 | 35 | 20 |
| Create: disk, guard and credentials | 55 | 70 | 55 |
| Create: VMM, restore, identity | 145 | 155 | 200 |
| Create: command / contingency | 35 / 15 | 35 / 80 | 35 / 100 |
| **Prepared create total** | **290** | **475** | **410**; warm hit 270 |
| Fork: admission and capture hooks | 70 | 100 | 80 |
| Fork: disk, guard and quiescence | 70 | 90 | 65 |
| Fork: CPU/device/RAM capture | 220 | 430 | 230 |
| Fork: restore, parent, identity | 155 | 140 | 225 |
| Fork: command / contingency | 35 / 50 | 40 / 100 | 40 / 200 |
| **Live fork total** | **600** | **900** | **840** |

The sums are correct. Grouping above preserves each design's original allocations; their phase boundaries differ. None is a measured Lamp result. Se has only 25 ms of create headroom. Its 80 ms contingency is already in the 475 ms total.

- **C-038 — high — all, performance sections.** Capture bandwidth is the largest unsupported allocation. Sp needs 4.55 GiB/s for 1 GiB in 220 ms; Se needs 2.33 GiB/s for 1 GiB, or 18.60 GiB/s for its proposed 8 GiB lane, in 430 ms; Si needs 8.70 GiB/s for 2 GiB in 230 ms. These are byte-rate lower bounds before device state, faults and persistence. Stock Firecracker full capture faults in and writes all guest RAM; memory/state files sync by default and block files always sync [P31]. RAM-backed fork generations can avoid memory-file storage latency, but still consume host memory and do not prove durable suspend. **Resolve:** measure every phase under full dirty RAM, disk writes and pressure; declare volatile versus durable completion. Include failed sanitation and cold preparation outcomes. No admitted slow case becomes compliant by renaming the fast envelope.
- **C-039 — high — all, ≥97% claim.** No cited engine establishes all three native-speed floors with Lamp's guard, audit, proxy, CoW and capture tracking enabled. Firecracker's specification is >95% compute-only; several I/O statements have pending integration tests [P11]. Dirty tracking has runtime costs [P31]. Its faster external block route also conflicts with snapshots [P32]. **Resolve:** select on measured secured compute, disk and network results separately. Count first-write faults, small I/O, fsync and request latency. Report direct native baselines and equivalent-path diagnostics; a slower native comparator or a category average cannot repair a failing measure.
- **C-040 — medium — all; especially Si §18.** The budgets cannot yet be compared as product latency. Si starts its main timer at API receipt, while the brief starts at the API call; it reports caller latency separately. RAM sizes, warm hits, durable commits and cached preparation also differ. **Resolve:** freeze one versioned caller-to-command protocol on both Linux architectures. Preserve engine-only spans as diagnostics. Label template restore, warm assignment, cold build/import, agent-ready and live fork separately; include RTT and failures in their observed cases.

**Strongest:** Se's explicit persistence/capacity costs and honest headroom, Sp's detailed command endpoint, and Si's larger dirty-RAM reference. Adopt no numerical budget as proof. Create p50 must be <500 ms; every declared target fork must be <1000 ms; each throughput ratio must be ≥0.97. Mac/Windows hypotheses remain experiments, not owner-approved targets.

## 17. Testing

| Sp §17 | Se §17 | Si §19 |
| --- | --- | --- |
| Full per-change hardware queue; 200 creates/100 forks | Best real-boundary/negative-test detail; nested plus physical Windows | Adds 1,000 lifecycle trials and 24-hour soak; same missing hardware |

- **C-041 — high — all, machine plans.** All correctly identify missing Windows and Linux arm64 hosts, but none provides a qualified, executable lane yet. Azure Dsv5 documents nesting, not a guarantee for every selected image/security type or desktop device [P33]. A server VM does not certify physical Windows 11, and an arm64 Linux guest on M5 does not certify a Linux/KVM host. The proposed latest-OS lanes also cannot establish the pending older minimums. **Resolve:** record provider/SKU, native or nested CPU, OS/edition, license, virtualization preflight, recovery, runner ownership and teardown. Qualify physical Windows plus native arm64 KVM; inventory Ubuntu/Air. If older minima are accepted, add boundary-version lanes, including a suitable Mac for macOS 14. Hourly rentals remain a pending policy, not spending authorization.
- **C-042 — medium — all, reliability and coverage gates.** Sp/Se specify latency sample counts but no release reliability threshold. Si proposes ≥99.9% over 1,000 operations plus a soak, without a confidence rule or per-operation denominator. Even zero failures in 1,000 independent trials gives only about a 99.70% one-sided 95% lower success bound. Broad “applicable” lanes can also hide untested native guests, audio/GPU/input, fallback hosts or harness/auth combinations. **Resolve:** define required capability-to-lane rows, denominators, fault classes and confidence rules. If adopting 99.9% as a statistical gate, at least 2,995 independent zero-failure trials are needed for that bound; correlated soak samples need separate treatment. Missing required rows remain incomplete.

**Strongest:** Se's scanner/sink health checks and successful allowed operations, Si's soak, Sp's full per-change queue. Use public third-party fixtures and dedicated accounts only. Every source change gets fresh results keyed to its revision; build-only Windows CI, emulation and unavailable hardware never turn a required lane green. Publication timing must track the pending owner choice separately from test execution.

## 18. Build plan

| Sp §18 | Se §18 | Si §20 |
| --- | --- | --- |
| Small Linux gate, then performance/security, other hosts, product | Linux gate then secured datapath; explicit complete-release gates | Linux gate then portable engines; local product precedes full trust slice |

**Findings carried forward:** C-004/C-011 (high, engine/storage feasibility), C-005/C-008 (critical, native fork and credential capture), and C-041 (high, missing machines). Evidence: Sp milestones 2–3 and Se E2/E4 leave some fatal compatibility work after the first engine gate; Si flags it in milestone 1 but still has no passing proof. **Resolve:** after the mandatory Linux demo, run these blocking experiments before committing the template/engine ABI or expanding adapter work. Keep the Linux demo small; it is a sequencing gate, not full product acceptance.

**Strongest:** Se's secured Linux gate, then Si's bounded vertical slices. Suggested order: Linux create/live fork → secured ≥97% and storage proof → native-host/capture/credential feasibility → local product → fleet/access/CI → complete release. Preserve all first-release features. Bind each gate to artifacts and an acceptance row, not a coverage paragraph. Include TS/Python first and Go/Rust immediately after only if the pending SDK cutoff is approved; all three current build plans list four SDKs.

## 19. Risks and experiments

| Sp §19 | Se §19 | Si §21 |
| --- | --- | --- |
| Widest phase/platform experiment list; aggressive Mac/Windows aspirations | Strongest hostile-input and fail-closed tests | Best early shared-core comparison and explicit native-Mac blocker |

**Findings carried forward:** C-005/C-008/C-023/C-033 (critical, unresolved product contracts) and C-018/C-026/C-034/C-038 (high, useful secrecy, authority, accounting and bandwidth). Each design names these risks, but an experiment cannot settle the owner's intended meaning. Refusing an unsafe capture passes safety while leaving the required successful fork/suspend workflow unmet. **Resolve:** keep separate safety and feature-success results, with positive workflows and binding blockers. The experiment register below supplies concrete pass/fail gates; a failed gate blocks adoption or release rather than silently changing scope.

**Strongest:** Si's invalidate-the-core-first ordering, Se's adversarial oracles, Sp's phase measurements. Vendor permission, native-OS licensing and API restrictions need documentary evidence alongside tests. New minimum OSes, raw-secret scoring, token-cap scope and audit meaning cannot be decided by a fast empty guest.

## 20. Deliberate differences from the five references

| Reference | Sp | Se | Si | Independently checked conclusion |
| --- | --- | --- | --- | --- |
| Cloudroom | Generic fleet; owner-host logins | Stronger credential/action boundary | Keys outside guest; smaller control plane | Its own security page says agents can read sandbox-held keys and optional Mac access runs as the user [P34]. Lamp must prove its stricter absence boundary; console polish alone is not the difference. |
| E2B Embed | Multi-OS, no Docker | Own templates and credential/CI policy | Shared portable core | Official Embed is one-machine Firecracker, with local databases/templates/logs; it excludes secrets, volumes, workload identity and BYO proxy [P35]. Lamp adds those contracts and a fleet. Use the current runtime repository, not a stale infra deployment link. |
| Docker Sandboxes | Own format; API sentinel | Separate subscription permission | Optional inside-Docker only | Docker already has host auth-header substitution, per-kit bindings and OAuth passthrough exceptions [P26]. Its workspace can be mountless, directly mounted or privately cloned [P36]. Do not claim Lamp invented isolated workspaces or external credentials. |
| microsandbox | Selected low-level reuse | Hardened policy/credential/fleet boundary | Shared downstream engine | Full capture resumes the running source; private-memory restore is optional, and a local cache is prepared first [P04]. Its docs allow 30-minute capture timeouts. Lamp's difference must be measured end-to-end speed and stricter contracts, not claiming it lacks RAM fork. |
| SmolVM | Continuing Windows parent | Same, plus confinement | Same; own template format | Version 1.34 requires `--freeze-source` on Windows; source execution then stops [P37]. That is a real fork-semantic difference. Its Linux/Mac branching cannot be dismissed because Windows has that limit. |

- **C-043 — medium — all, comparative proof.** The designs promise a shared suite but do not freeze equivalent product states and feature eligibility. Mountless versus host-mounted workspaces, continuing versus frozen parents, copied versus private-mapped RAM, and prepared versus newly built templates change the result. A headline boot comparison would reward different work. **Resolve:** pin versions and installation manifests, test equivalent states and security policies, and publish endpoint/resource/durability differences. Show unsupported features as unsupported, never as zero latency or a timeout win. Shared `msb_krun` code also means microsandbox is not an independent VMM implementation for defect diversity.

**Strongest:** all three separate reuse from adopting a whole competitor stack. Retain the tested engine work; own Lamp's template, policy, session and fleet contracts. ADR 0003 still requires a security or likely-speed reason per reused component. Cloudroom's separate-domain policy is not an option for Lamp: every hosted Lamp surface stays under `eilmer.si`.

## Cross-cutting contradictions

These are merge hazards, not additional findings. Resolve them under the cited IDs so the synthesizer cannot accept two incompatible answers.

| Tempting combination | Conflict | Required merged decision |
| --- | --- | --- |
| Sp's custom block backend + stock Firecracker private-RAM restore | External vhost-user block shares RAM and cannot snapshot in 1.17 [P32]; C-011/C-038 | Choose a proven file-backed CoW path, or qualify the complete modified backend. Never budget one path and implement another. |
| Sp's local home journals + Se's global Raft + Si's placement log | Two systems may each grant placement, quota or approval authority; C-021/C-025/C-026 | One commit/permit model. Partitioned hosts spend only disjoint delegated authority; unknown old operations remain unknown. |
| Exact mid-task memory + subscription sign-in + universal stripping | Task code can retain transformed credentials; C-008/C-010 | A successful, proven supported state contract or an explicit owner amendment. No scan can certify arbitrary hidden copies. |
| Se's strict egress + likely open-internet default + private-network plugins | Open transport can bypass strict name/semantic policy; a blanket private deny can also block an approved local service; C-014/C-015/C-033 | Define route/policy composition and explicit connection exceptions. Public-PR restrictions cannot be overridden by a recipe or VPN. |
| Se's audit/token ceilings + opaque subscription/outside-agent traffic | TLS-preserving traffic lacks request/token visibility; C-033/C-034 | A route-by-route enforcement/visibility contract. Do not intercept subscription TLS to repair accounting. |
| Si's shared small core + Se's Linux confinement assurance | A shared API does not transplant the jailer or prove Mac/Windows confinement; C-004/C-036 | Per-OS worker profiles and real escape tests; separately certify each device build. |
| Si's Samba service + Sp's ≥97% native disk score | Extra VM, network filesystem and locking semantics change the baseline and capacity; C-012/C-013/C-039 | Keep private local-disk performance distinct from shared-volume performance. Charge the storage service and its recovery costs. |
| “Idle harness” exemption + exact task continuation + default suspend | A running/background harness is activity under the brief; C-009/C-019 | Only Lamp infrastructure is exempt. Exporting a harness continuation is a distinct operation whose fidelity must be tested. |
| Relay never decrypts + relay-controlled ACME/account recovery | Browser/native trust can be replaced without decrypting the original stream; C-028/C-029 | Separate forwarding, enrollment and issuance authority; scope Clerk cookies under `eilmer.si`. No second domain. |
| Permanent retry safety + quotas/retention + disaster recovery | Deleting receipts, pruning tombstones or restoring an old database can admit a second effect; C-002/C-021/C-026 | Preserve rejection/deduplication across retention and restore. Bound accepted key volume before admission; never evict safety to free disk. |
| Fast RAM-backed fork + idle suspension using no RAM + durable recovery | CoW backing still occupies host RAM/cache; volatile capture does not survive reboot; C-001/C-002/C-038 | Separate volatile fork and durable suspend commit points; account all backing resources and release guest allocations after durable save. |

## Unmet brief requirements

This is a design-sufficiency check, not a report that software tests failed. No implementation is supplied. A coverage reference or safe refusal does not satisfy a required successful workflow.

| Binding promise | What no design establishes | Findings / round-3 obligation |
| --- | --- | --- |
| §§6/9/13: signed-in mid-task fork, suspend and safe template save | General exact-state continuation together with removal of hostile retained subscription copies | C-008/C-010; retain the blocker or obtain an explicit change to the supported contract. |
| §6: running memory fork, including native guest classes | Independent continuing macOS parent/fork through supported VZ APIs; Windows/native-device rows also lack proof | C-005/C-006/C-004; qualify every required guest/device row, within the two-macOS limit. |
| §12: a bad plugin cannot reach into sandboxes | Literal isolation from the guest RAM an engine implementation must handle | C-023; resolve the trusted implementation exception with the owner. |
| §19: every command/request; task token ceilings | Complete semantic audit and enforceable caps for opaque subscription/outside modes | C-033/C-034; define owner-approved meaning and retain unsupported required modes as gaps. |
| §§7/21/22: Linux speed and comparative proof | Measured secured create/fork/97% results on x86_64 and arm64 | C-038–C-043; all figures remain allocations, with no accepted envelope waiver. |
| §§13–16/18: named harnesses, kits, consoles and CI | Exact-version successful auth, protected fill, every native sub-agent session and two-way continuation/native-runner contracts | C-016–C-020/C-031; implementation sketches need certified positive and hostile workflows. |
| §§5/22: first-release host/guest matrix | Provisioned Windows, native Linux arm64 and complete device/fallback lanes | C-036/C-041/C-042; missing hardware remains a release blocker. |
| §§1/5/10: easy install, surviving-host coordination and persistent work | Measured clean install, complete authority model and specified data recovery/durability | C-001/C-002/C-013/C-026; Si additionally violates two-host failover, C-025. |

No design omits an entire obvious feature family such as schedules, notifications, outside-agent control or persistent volumes. Their unresolved mechanics must not be described as implemented coverage. The other direct violations are specific to Si's idle rule (C-009) and Se/Si's unspecified post-retention retry behavior (C-021); Sp supplies alternatives.

The ten pending recommendations remain pending: TS/Python at first release; hourly Windows/arm64 rentals; open public egress with private/metadata blocking and public-PR allowlists; private dev URLs with expiring shares; flagged opt-in exposures outside the leak score; private benchmarks until public release; counts/timings-only usage data; owner-built macOS/Windows templates; owner/admin/member/viewer roles; and Linux 5.10+/macOS 14+/Windows 11/Server 2022+ minimums, x64 first. None authorizes dropping Linux arm64 or Apple Silicon from release one. The requirements critique separately proposes 90-day telemetry retention; that period also needs a decision.

## Language decision

**Recommend Go for `lampd`, CLI, orchestration, API/MCP/session adapters and the separate owner brokers; Rust for engines, guest/init, confinement-sensitive helpers and the WASI runner.** Use Si's ownership split with Se's process and authority boundaries. Keep the AGPL relay a separate build from Apache host components.

| Reason | Consequence |
| --- | --- |
| `tsnet` and the official scale-set client are Go libraries [P14][P30] | Go control code reduces adapter-language crossings and one control-stack duplication. A separately confined access process may still be necessary. |
| Firecracker and the downstream krun engine already expose Rust integration paths [P02][P11] | Keep vCPU, device, RAM and block operations out of Go and out of a shared FFI heap. |
| All designs already use multiple native processes | Bounded local RPC is already a requirement. Rust throughout control does not remove every IPC boundary; Go does not make one unnecessary. |
| No daemon-language benchmark demonstrates a decisive result | Do not trade delivery simplicity for an assumed GC or allocator advantage. Measure C-003 with the same Rust engine. |
| Brokers are trusted regardless of language | Keep secrets in distinct owner processes; use reviewed TLS/signing libraries, bounded parsing, typed requests and hostile-output tests. Rust engines still require unsafe/platform-code review. |

Make Go the single owner of operation/authorization state. Rust workers independently validate typed grants, generation, handle ownership and hard local limits; they do not maintain a competing placement/ACL database. Use a versioned socket/named-pipe protocol with deadlines, peer authentication, bounded frames and capability handles. Stream RAM, disks, packets and terminal bytes outside the control log. Generate schemas; do not hand-copy policy rules between languages.

Adopt this split provisionally. The experiment below must show that control plus RPC overhead fits its allocation under concurrent load. If it fails, profile lock/fsync/quorum/protocol/GC spans before changing language. Revisit a hot component only on evidence; do not rewrite the whole daemon to explain a VMM or storage bottleneck.

## Linux engine decision

**Use Firecracker first as the Linux reference and initial headless engine. Do not commit Linux to the shared krun core yet.** This is a qualification order, not a permanent fastest-engine exception. Run the downstream core against the same gate immediately after the first Linux demo. Select the fastest certified engine per actual resource/device/security tuple, as the brief requires.

| Exact implementation | Linux x86_64/arm64 | Apple Silicon macOS | Windows x86_64 | What is established |
| --- | --- | --- | --- | --- |
| Upstream libkrun 1.19.8 | KVM execution; no public general capture/restore API | HVF execution; same API gap | No backend in this release | README and C header establish execution support, not memory fork [P01]. |
| Maintained `superradcompany/libkrun`, `msb_krun` 0.1.45 | KVM state/RAM capture and restore code | HVF state/RAM capture and restore code | WHP state/RAM capture and restore code | Published Nov 7; repository active at commit `344cb2488c0475e644ed21152344a408973d4369`; non-TEE paths expose paused capture and reconstruction [P02][P38]. |
| Same downstream private-memory layer | Unix private mappings | Unix private mappings | Windows private file view with checked RAM-region slices | Code supports independent writes; it does not show capture is constant-time or Lamp isolation passes [P03]. |
| microsandbox 0.7.14 above that family | Full capture/live fork documented | Same documented local fork path | Same; continuing source documented | Running source pauses then resumes. CoW restore is optional; its first local cache preparation has cost. Docs permit 30-minute capture timeouts [P04]. |
| Firecracker 1.17 | Supported snapshot path on both architectures | No native host backend | No native host backend | Full capture, original-source resume and private demand-mapped restores; disks and capture lifecycle belong to the integrator [P31]. |

Therefore “libkrun cannot snapshot or fork on Mac/Windows” is false when applied to the maintained downstream. “One shared core already satisfies Lamp on all hosts” is also unproved. Ordinary Linux arm64 snapshots are distinct from nested virtualization, which the downstream product does not support on Linux arm64 [P04]. No Windows-arm64 commitment follows from x86_64 support. These rows concern Linux guests; they do not solve native macOS/Windows fork.

Firecracker is the safer first reference because its restricted device path, snapshot lifecycle and Linux jailer have documented contracts [P31][P39]. This is a maturity judgment, not a measured speed ranking. Its 97% I/O feasibility, dirty-memory bandwidth and ext4 CoW path remain gates. Start with file-backed raw disks on a proven reflink filesystem; qualify an ordinary-ext4 strategy rather than making all users reformat. Reject the unsupported vhost-user/snapshot combination.

The shared core can win Linux if it passes the same useful-work, continuing-parent, crash, isolation and performance gates with lower measured cost. Its existing cross-OS state/device work is a substantial maintenance advantage. Pin the complete crate/kernel/device graph and maintain a security patch/backport plan. Keep engine-specific prepared RAM artifacts under a portable Lamp template manifest; a common disk format does not make KVM, HVF and WHP execution state interchangeable. Retain separate VZ and Windows-native adapters regardless of which Linux core wins.

## Experiments required before commitment

Run the Linux engine demo first, as brief §22 requires. Then settle the remaining feasibility gates before committing engine/storage formats or declaring the release plan achievable. The brief's numerical limits bind. Other thresholds below are proposed engineering gates, not new owner decisions. Hardware rental or account changes are outside this document's authorization.

Every run records source/dependency hashes, OS/build, CPU, filesystem, power/thermal state, policy, resources and raw failures. Use independent command/RAM/disk/sink oracles, not the API's own ready flag. Pin one workload manifest across engines. An unavailable lane is incomplete. A refusal can pass safety only; it cannot pass required feature success.

| Experiment / findings | Pass threshold | Fail threshold and consequence |
| --- | --- | --- |
| E01: Linux engine bake-off; C-004/C-038/C-040 | On native x86_64 and arm64: 20 warmups, 200 cached-template creates, 100 live forks per RAM row; create p50 <500 ms, every fork <1000 ms, zero state errors and continuing source. Test 128 MiB/1/2/8 GiB, full dirty RAM/disk, and every further admitted size. Separate warm-pool, volatile and durable cases. | Any target breach, state mismatch, timeout or hidden preparation fails that row. Profile first; no product expansion after a failing initial Linux demo. No resource-envelope narrowing without resolving brief scope. |
| E02: secured native-speed floor; C-011/C-039 | At least 10 paired randomized trials per compute, disk and network measure; each guest/native work-rate ratio ≥0.97 with normal guard/audit/capture tracking active. Convert latency to an inverse ratio. Proposed confidence gate: one-sided 95% lower bound ≥0.97. | Any measure below 0.97 fails. A point estimate ≥0.97 with a lower bound below 0.97 is incomplete under the proposed confidence gate. Averages, disabled policy and artificial native overhead cannot rescue it. Try another eligible engine/datapath. |
| E03: Go control boundary; C-003/C-040 | Same Rust worker and durable-admission semantics for both control prototypes; 32 concurrent local callers, proposed combined auth/admission/RPC p99 ≤35 ms, zero lost/cross-owner operations or quota overspend; total create still passes E01. | >35 ms p99 or any correctness fault fails the proposed control allocation. Profile RPC, lock, fsync, quorum and runtime spans; change only the measured bottleneck. |
| E04: capture/storage correctness; C-010/C-011/C-013 | 1,000 forks with concurrent dirty pages, flush/discard, chain rotation and crashes at each commit boundary; zero mixed generations, cross-fork writes or lost acknowledged durable writes. Test actual ext4, reflink, APFS and NTFS paths. | One oracle mismatch or dependence on unsupported vhost-user snapshots rejects the backend. Unacknowledged work may be lost only within its declared durability contract. |
| E05: Mac/Windows Linux engine; C-004/C-036/C-041 | On real HVF/WHP hosts, 1,000 full dirty/in-flight-I/O capture/restore trials with both parent and fork running; zero state/clock/isolation errors. Prove native confinement and all requested device rows; record p50/p95/p99/max, RSS and pause. | One state/isolation failure rejects the row. No frozen-source substitute. Timing establishes proposed Mac/Windows targets afterward; the Linux one-second target is not imposed on them. |
| E06: native macOS/Windows fork; C-005/C-006 | Supported-API RAM continuity, concurrently running parent/fork, independent writable disks/auxiliary state and safe identities for 100 trials per advertised configuration; zero cross-instance effects and no license/cap violation. | Any unsupported identity change, inability to continue both, or disk-only recovery fails. Native-Mac failure remains a binding release blocker; no Linux-guest result closes it. |
| E07: subscription capture; C-008/C-010 | A documented state/credential boundary excludes bearer material from captured RAM, disk, page cache, temp, dumps and exports; approved signed-in work successfully forks/suspends/saves and continues. Adversarial raw/encoded/compressed copies yield zero captures or refusal before writing. | One retained bearer fails safety. Only refusing hostile states does not establish general feature success. Finite canary scans alone cannot prove arbitrary erasure; absent a sound exclusion contract, record the unresolved binding blocker. |
| E08: useful credential/kit compatibility; C-016–C-018 | Exact-version named harness/auth rows, `gh`/`stripe`/`aws`, public MCP fixtures and all four vaults complete real permitted operations; zero API-key/password/cookie canaries in guest, output, logs, captures or hostile sinks. Test reflection, redirects, streaming and human takeover races. | Any leak, unauthorized effect or missing required positive workflow fails the adapter. Raw injection is no compatibility fallback. Score explicit opt-ins separately only after the owner decides. |
| E09: host network and private identity; C-014/C-015/C-036 | Guest root/nested Docker sends zero packets to denied IPv4/IPv6, LAN, metadata and neighbor sinks through DNS, UDP/QUIC, rebinding, VPN crash or TSI. Allowed traffic and approved local services succeed; peer sees the assigned sandbox private identity. | One bypass, host identity substitution or fail-open packet rejects the engine/network pairing. A deny-everything implementation fails useful-work coverage. |
| E10: fleet, retries and revocation; C-021/C-025–C-027/C-037 | 1/2/3-host sleep/partition/rejoin tests: already-permitted survivor work starts within proposed 10 s; zero duplicate keyed allocations, double writers, quota overspend or twice-used approvals. Retry old/deleted keys after simulated retention and backup restore. Online revoke ≤5 s; offline authority expires within its declared lease. | Any duplicate/unfenced effect fails. No survivor coordination within 10 s fails the proposed availability gate. A partition may block new global grants; it cannot invent them. Record revocation beyond the deadline as failure. |
| E11: session/console continuation; C-019/C-020 | Each required harness has laptop→sandbox→laptop and interrupted-move tests; zero lost acknowledged messages or concurrent writers, every native sub-agent separately visible/watchable/controllable, real git return/conflicts and stock SSH on every access route. | One hidden sub-agent, unsupported required direction, lost operation or unfenced writer fails the row. Transcript replay alone is insufficient. |
| E12: hosted trust and certificate scale; C-028–C-030 | Chromium/Firefox/WebKit attacks produce zero stolen/replaced account authority or cross-origin private data. Hostile relay cannot decrypt, enroll or obtain unauthorized fleet certificates. 1,000 sandbox names/fleet need zero additional certs; aggregate issuance stays ≤50/7 days, with proposed 40 maximum for Lamp and 10 reserved for other products/repair. | Any trust bypass or budget overrun blocks hosted access. The reserve is global, not an extra per-product allowance. Obtain capacity before unrestricted fleet onboarding; all hosted names stay under `eilmer.si`. |
| E13: audit, caps and resource exhaustion; C-001/C-033/C-034/C-037 | Independent fixture ledger has zero missing required command/request events; strict token reservations never exceed the cap; all descendants stop by the declared time/revoke deadline. Flood events, receipts, streams, captures and artifacts within one tenant: unrelated admitted work retains its reserved resources. | One silent audit gap, cap bypass or unbounded host exhaustion fails. Owner-unsettled audit/opaque-accounting meaning stays unresolved even if selected fixtures pass. |
| E14: install, recovery, updates and reliability; C-001/C-002/C-041/C-042 | Every required real-host lane installs, boots, forks, suspends durably, resumes after reboot, updates one host and rolls back without lost acknowledged state; uninstall leaves no active worker/grant. Proposed standard-operation gate: 2,995 independent zero-failure trials plus 24-hour loaded soak, zero cross-task state or orphaned authority. | Missing hardware/OS evidence, one safety failure or failure of the proposed reliability bound blocks certification. Preserve old readers/workers for retained state; no cold reset labeled resume. External host loss must match the published backup/RPO class. |

## Findings summary

Counts are unique finding IDs, not references repeated in later sections. Recommendations and experiments do not add findings.

| Severity | Count |
| --- | ---: |
| Critical | 7 |
| High | 23 |
| Medium | 13 |
| Low | 0 |
| **Total** | **43** |

| ID | Severity | Designs | Finding |
| --- | --- | --- | --- |
| C-002 | High | All | Control failover lacks a complete data-loss/recovery contract. |
| C-004 | High | All | The downstream shared core lacks exact-build qualification. |
| C-005 | Critical | All | Native macOS live memory fork is unproved under VZ identity restrictions. |
| C-006 | High | All | Windows backend/edition selection conflates WHP, HCS and Hyper-V. |
| C-008 | Critical | All | Exact signed-in capture and arbitrary subscription-token removal remain incompatible. |
| C-009 | Critical | Si | Its idle exemption permits a running harness against the binding idle rule. |
| C-010 | High | All | Template sanitation cannot remove arbitrary secret copies from task files. |
| C-011 | High | Sp/Se | Custom Firecracker storage has no proven snapshot-compatible integration. |
| C-013 | High | All | Shared-volume replication lacks commit, durability and fencing semantics. |
| C-014 | High | All | A tsnet endpoint alone is not sandbox private-network membership. |
| C-016 | High | All | No exact-version harness/proxy/auth compatibility matrix is certified. |
| C-017 | High | All | CLI and MCP credential operations need protocol-specific authority contracts. |
| C-018 | High | All | Protected browser/password workflows lack useful secrecy certification. |
| C-019 | High | All | Harness/console teleport continuation remains unqualified. |
| C-020 | High | All | Harness-native sub-agents may bypass separate session visibility/accounting. |
| C-021 | Critical | Se/Si | Post-retention idempotency behavior can permit duplicate work. |
| C-023 | Critical | All | Trusted engine plugins contradict the literal plugin-isolation promise. |
| C-025 | Critical | Si | Two-host coordination requires a third voter for new allocations. |
| C-026 | High | All | Placement, delegated authority and revocation lack one executable consistency model. |
| C-027 | High | All | tsnet enrollment, federation and Headscale equivalence are unqualified. |
| C-028 | High | Sp/Se | Host-only app cookies do not isolate Clerk's own auth surfaces. |
| C-029 | High | All | Relay compromise can cross unspecified enrollment/certificate authority boundaries. |
| C-031 | High | All | Named CI plugin sketches do not establish native job support. |
| C-033 | Critical | All | Exec/flow telemetry does not fulfill every-command/request audit. |
| C-034 | High | All | Opaque model routes cannot enforce hard token ceilings as specified. |
| C-036 | High | All | Mac/Windows worker confinement needs actual profiles and escape evidence. |
| C-037 | High | All | Revocation omits retained and in-flight derived capabilities. |
| C-038 | High | All | RAM/disk capture bandwidth and durability do not substantiate fork budgets. |
| C-039 | High | All | No engine evidence proves all secured ≥97% native-speed floors. |
| C-041 | High | All | Windows/arm64 and pending-minimum-OS test lanes remain unqualified. |

Round 3 must answer **every C-001–C-043**, including medium findings: accepted with the merged section/test location, or rebutted with primary evidence and binding reasoning. State pending owner resolutions separately. Do not count an architectural promise, a source API or safe refusal as a passing product test. This review ran no Lamp tests, rentals, sign-ins or infrastructure changes.

## Primary sources

Checked on 2026-11-08. Versioned code establishes available mechanisms; living docs establish current vendor contracts. Neither is measured Lamp performance. Sources P01–P30 also resolve the labels left by the interrupted critic. The C-011 correction above is limited to the demonstrably incorrect stock-backend claim.

Additional source detail: upstream [libkrun's C header](https://github.com/libkrun/libkrun/blob/v1.19.8/include/libkrun.h) has no general save/restore entry point. The fork's [crate metadata](https://crates.io/api/v1/crates/msb_krun/0.1.45) records publication/license/repository, and its [execution-state source](https://docs.rs/crate/msb_krun_vmm/0.1.45/source/src/execution_state.rs) identifies KVM/HVF/WHP formats. Firecracker's [kernel policy](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/kernel-policy.md) must be checked separately from its performance specification. Copilot [issue 5085](https://github.com/github/copilot-cli/issues/5085) records a version-specific native-TLS regression; an upstream user report is a test lead, not a universal incompatibility claim. Tailscale's [federation feature registration](https://github.com/tailscale/tailscale/tree/main/feature/condregister/identityfederation) supplements its setup docs. Clerk's [deployment-domain guide](https://clerk.com/docs/guides/development/deployment/changing-domains) supplements its cookie architecture. Wildcard matching follows [RFC 9525 §6.3](https://www.rfc-editor.org/rfc/rfc9525.html#section-6.3); Azure's [nesting requirements](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/enable-nested-virtualization) supplement its SKU table.

[P01]: https://github.com/libkrun/libkrun/blob/v1.19.8/README.md
[P02]: https://docs.rs/crate/msb_krun/0.1.45/source/src/api/vm.rs
[P03]: https://docs.rs/crate/msb_krun_vmm/0.1.45/source/src/private_memory.rs
[P04]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/docs/sandboxes/snapshots.mdx
[P05]: https://developer.apple.com/documentation/virtualization/vzmacplatformconfiguration
[P06]: https://developer.apple.com/videos/play/wwdc2023/10007/
[P07]: https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/whp-samples
[P08]: https://learn.microsoft.com/en-us/virtualization/api/hcs/overview
[P09]: https://canonical.com/multipass/docs/latest/reference/release-notes/1.17.0/
[P10]: https://learn.microsoft.com/en-us/virtualization/hyper-v-on-windows/quick-start/enable-hyper-v
[P11]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md
[P12]: https://developer.apple.com/documentation/hypervisor/hv_vm_map(_:_:_:_:)
[P13]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/src/firecracker/swagger/firecracker.yaml
[P14]: https://pkg.go.dev/tailscale.com/tsnet
[P15]: https://code.claude.com/docs/en/network-config
[P16]: https://learn.chatgpt.com/docs/auth
[P17]: https://github.com/anomalyco/opencode/blob/dev/packages/web/src/content/docs/network.mdx
[P18]: https://cursor.com/docs/cli/reference/configuration
[P19]: https://github.com/github/copilot-sdk/commit/c9433c4c3e3e190945a7097c7da17bfdc4090813
[P20]: https://ampcode.com/docs/cli/settings
[P21]: https://github.com/block/goose/blob/main/documentation/docs/guides/acp-providers.md
[P22]: https://github.com/google-gemini/gemini-cli/blob/main/docs/reference/configuration.md
[P23]: https://github.com/badlogic/pi-mono/blob/main/packages/ai/src/utils/node-http-proxy.ts
[P24]: https://docs.devin.ai/cli/troubleshooting
[P25]: https://docs.factory.com/enterprise/network-and-deployment
[P26]: https://docs.docker.com/ai/sandboxes/configuration/credentials/
[P27]: https://tailscale.com/kb/1581/tsnet-workload-identity-federation
[P28]: https://clerk.com/docs/guides/how-clerk-works/overview
[P29]: https://letsencrypt.org/docs/rate-limits/
[P30]: https://github.com/actions/scaleset
[P31]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md
[P32]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/api_requests/block-vhost-user.md
[P33]: https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dsv5-series
[P34]: https://www.cloudroom.dev/security
[P35]: https://e2b.dev/resources/introducing-e2b-embed
[P36]: https://docs.docker.com/ai/sandboxes/architecture/
[P37]: https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/limitations.md
[P38]: https://github.com/superradcompany/libkrun/commit/344cb2488c0475e644ed21152344a408973d4369
[P39]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/jailer.md
