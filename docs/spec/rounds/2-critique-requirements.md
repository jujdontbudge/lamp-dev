# Round 2: requirements critique

Verdict: **changes required**. There are **43 findings: 2 critical, 25 high, 14 medium, 2 low**.
The draft names every brief feature, but several core workflows lack a consistent, testable contract.

## Review contract

The binding sources are `docs/spec/brief.md`, `GLOSSARY.md`, and ADRs 0001–0009.
The three round-1 architecture designs are evidence of missing contracts, not selected designs.
The owner's ten pending decisions are recommendations, not approved changes to the brief.
Only this critique file is being edited.

Each finding below gives an ID, severity, affected requirements, evidence, and a repair.
The coverage audit checks every brief section. The decision tables classify all 35 open questions.
The highest-priority findings are R-001, R-002, R-007, R-009, R-012, R-013, R-023 and R-036.

## Findings: core behavior

### R-001 — critical — A subscription-bearing live fork has no satisfiable contract
**Affects:** SBX-007, CRED-003/004, LIF-001/006, TPL-002, SES-005; Q11/Q19; Journey 3, ATK-22.
**Problem:** The draft requires arbitrary in-memory token removal, unchanged task state, and immediate continued sign-in. ATK-22 allows refusal, but Journey 3 requires the signed-in fork to succeed. Guest-root code can transform and hide token copies. Literal/decodable-canary scans cannot prove exclusion. ADR 0004 still forbids captured credentials.
**Fix:** Define supported capture modes and a success test for each. Require refusal before capture when exclusion is unprovable. A refused operation passes a safety test but leaves fork/suspend feature coverage unmet. Show a working API-key-proxy fork separately. Ask the owner about any proposed restriction on subscription-mode capture; do not silently weaken ADR 0004 or the fork requirement.

### R-002 — critical — Universal protected browser and password use lacks a boundary
**Affects:** KIT-001/003/005, BEN-004, CON-003; Q22; ATK-29–31.
**Problem:** Keeping the browser outside the sandbox does not hide passwords or cookies exposed by page JavaScript, screenshots, downloads, same-origin reflection, or agent-controlled form actions. The draft promises default fill and logged-in control without defining supported sites/actions or a trusted-recipient boundary. A test of one friendly page can pass while the advertised behavior fails.
**Fix:** Specify what browser actions and site adapters are supported, which observations are suppressed during entry/takeover, and when use is refused. Include hostile same-origin content and encoded reflection tests. Keep approved opt-in exposure distinct. Any restriction on the owner's general browser/password-manager promise needs an explicit owner decision, not a test-fixture exemption.

### R-003 — high — “All examples are illustrative” prevents a consistent interface contract
**Affects:** INT-001–005, INT-007, TPL-001/003, CON-001, SES-001–005, BEN-001; reading rules and journeys.
**Problem:** Names may be provisional, but examples must agree. CLI session creation returns `session`, API/MCP returns `id`; HST-002 returns `hosts` as a count while other examples use a list; session lists alternate objects and strings. TPL-001 gives MCP a caller-local path without an upload mechanism. CON-001 shows an upstream GitHub tool instead of the Lamp operation. Recipes never get a CLI/MCP creation example.
**Fix:** Add one provisional operation/resource/error/event dictionary. Use it throughout and identify genuine transport differences. Provide exec, file read/write/transfer, recipe create, connection attach, and session attach/watch/input examples in all three interfaces. Show local-file upload rather than assuming the host can read a caller's path.

### R-004 — high — Readiness and partial creation are not observable contracts
**Affects:** INT-001/004/006, TPL-003, CRED-001–005, BEN-001, OPS-001.
**Problem:** `running`, `ready`, `signed_in`, `auth_ready`, and `command_running` are used without a state model. A sandbox can boot while sign-in, VPN setup, kits, or connections fail. The draft does not say whether create blocks, returns an operation, or exposes partial resources. A badge can therefore pass before the requested setup works.
**Fix:** Require accepted/preparing/ready/failed/canceling/terminal outcomes and inspectable operation IDs, progress, retryability, and cleanup. Define ready as all requested policy, tools, credentials and connections applied plus a real command probe. Test a failed kit/auth/exit after allocation and a lost response after successful creation.

### R-005 — high — Subscription networking conflicts with the signed-in development journey
**Affects:** CRED-003, NET-002/005, CON-001–003, TPL-003; Journey 1 step 3 and Q19.
**Problem:** The journey puts a subscription login, GitHub access, package downloads and general task execution in one sandbox. Root can read the subscription token. Restricting only the harness's usual HTTP calls cannot stop another process sending it through an otherwise permitted connection. All three designs isolate broader task connections from the token-readable sandbox.
**Fix:** State how these requirements compose. Show a signed-in journey with enforceable vendor-only egress and a separate token-free task sandbox, or another proven approved boundary. Test a hostile process using every additional connection. Do not add a subscription-token proxy contrary to ADR 0004.

### R-006 — high — Plugin isolation contains an unstated trust exception
**Affects:** PLG-001/002, SBX-001, CRED-005, KIT-005; Q18, ATK-36.
**Problem:** “A bad plugin ... cannot ... reach into sandboxes” conflicts with an engine plugin needing assigned RAM and a credential plugin handling its granted secrets. “Authorized plugin effects” does not define this exception. Testing one ordinary hostile plugin cannot establish safety for a malicious engine.
**Fix:** Define ordinary confined plugins and explicitly trusted boundary implementations, their allowed objects, and who admits them. Require no ambient host, neighbor, or broker authority. Add conformance tests per plugin type, including inherited handles, subprocesses, resource floods and privilege expansion on update. Keep process/WASI/language choices in architecture.

### R-007 — high — Failover can pass while a two-host fleet cannot create work
**Affects:** HST-002/004, INT-004, ACC-007; Q03/Q06; Journey 2, ATK-43.
**Problem:** The main test uses LX + M5 + AIR. It does not require either survivor of a two-host fleet to accept new authorized work. Simplicity §12 needs a third voter; security §11 permits only bounded work without quorum; speed §11 uses host-local authority. “Any number” and automatic coordination need an outcome independent of these choices.
**Fix:** Add one-, two-, and three-host loss/partition/rejoin cases. State which create, retry, revoke and policy operations remain available, recovery bounds, and fencing rules. Verify no quota overspend, duplicate allocation or stale writer. Requiring a third host would change the brief and needs the owner.

### R-008 — high — Fork correctness lacks a required pre-execution identity barrier
**Affects:** SBX-007, LIF-001, NET-003, CRED-004, ACC-007; Q11; ATK-42.
**Problem:** A memory counter and independent files do not prove matching RAM/disk/device generations, fresh entropy, unique network/broker identity, or expired authority checks before first I/O. Both forks could act under copied handles before the test inspects them. All designs introduce a restore barrier.
**Fix:** Require consistent capture and fresh per-fork identity/authority before task execution or external traffic. Define clock/deadline behavior, userspace PRNG hooks, reconnects and external attachments. Test concurrent parent/fork randomness, stale handles, in-flight disk writes and immediate network actions, not only eventual labels.

### R-009 — high — Native full-VM fork remains unproved and cannot be hidden by Linux tests
**Affects:** SBX-003/005/007, LIF-001, TPL-002, QA-001; Q10/Q11/Q31.
**Problem:** SBX-007 has host labels but no explicit guest-class matrix. TPL-002 tests only Linux guests. Simplicity §§4/21 flags VZ identity/restore restrictions as a native macOS live-fork blocker. A Linux fork on M5 or Windows can pass while native full-VM fork never works.
**Fix:** Enumerate Linux, macOS and Windows guests separately, with every advertised engine/device combination. For native classes prove actual RAM continuity, source continuation, independent identity, save/resume and licensing. Report unsupported combinations as release gaps; accepting a reduced native-fork promise needs the owner.

### R-010 — high — The literal idle rule prevents its own default lifecycle
**Affects:** LIF-001/002, SES-002, OPS-001; Q13/Q23.
**Problem:** “No running processes and no open connections” includes init, the guest helper, a parked harness and its control channel. The test says “make a sandbox eligible” without showing how. Designs disagree about whether a parked harness is exempt. Session idle and sandbox idle are also conflated in console descriptions.
**Fix:** Define task activity, a fixed infrastructure exemption list, parked-harness behavior and unknown-observation handling. Background task processes must still prevent suspension. Show a real harness turn ending, all remaining processes/connections, automatic suspend and message-driven continuation. Display session status separately from suspension eligibility.

### R-011 — medium — Several visible defaults and qualitative promises have no pass rule
**Affects:** PRD-001/002, HST-004/006, LIF-001/003/005, INT-005, OPS-001/003; Q01/Q05/Q06/Q08/Q13/Q14/Q29.
**Problem:** “Equally,” “as easily as Docker,” “instantly,” “live,” “fastest,” deletion “after a set time,” notification delivery and warm readiness are not binary acceptance tests. Recording latency or friction alone cannot fail a bad implementation. The brief's actual numeric targets are preserved correctly; do not invent replacement numbers for them.
**Fix:** Architecture should propose explicit defaults, deadlines, supported envelopes and measurement rules, with rationale. Mark new targets as proposals until adopted. For qualitative owner promises, present concrete installation/resume journeys and ask for a product threshold only where engineering choices cannot settle the meaning.

### R-012 — high — Hard task budgets are undefined for opaque and outside agents
**Affects:** OPS-002, INT-006/007, SES-004/005, SBX-007; Q29.
**Problem:** A session ID stands in for a task, but there is no task boundary or shared budget across forks/sub-agents. Subscription and outside-agent calls can bypass observable usage. A post-response token counter can exceed the cap before Lamp notices. Killing a guest also cannot stop an already submitted remote tool.
**Fix:** Define task identity, inherited budgets, trusted usage sources and input/output reservations before dispatch. Specify streaming overshoot policy, retries, cancellation and remote work already committed. Refuse an unenforceable requested hard cap and flag the unmet mode. Owner approval is needed if “cap” becomes best effort for any promised mode.

### R-013 — high — “Every command and network request” has no observable unit
**Affects:** OPS-004, NET-001/005, KIT-004, BEN-004; Q30, ATK-40.
**Problem:** An exec is not a shell builtin, interpreter action, or every HTTP request in an opaque TLS connection. A guest-root collector can be disabled. The independent ledger is not specified, so a fixture using only Lamp exec and mediated HTTP can pass while most activity is missing. All designs disclose this limit.
**Fix:** Define command/request granularity and required fields for mediated, encrypted, raw, nested-runtime and native-guest modes. Require external attribution, sequence-gap detection and explicit behavior on audit failure/full storage. Include direct syscalls, builtins, multiplexed requests and collector tampering. Any reduction of the literal completeness promise needs the owner.

### R-014 — high — Placement does not require the person's trust in credential hosts
**Affects:** HST-003, CRED-003/006, ACC-006, STO-004; Q03/Q19/Q20.
**Problem:** Same-owner sandbox identity does not make a team compute host that person's own trusted credential host. Current placement criteria cover room, OS and devices only. A host administrator can read guest/broker RAM, even when session lists hide it. All designs add credential-host locality/trust.
**Fix:** Let a person designate credential-custody hosts and require placement to honor that set. Describe host-admin trust separately from owner/admin/member/viewer application roles. Test an otherwise eligible untrusted team host, an offline trusted host and direct host-to-host credential transfer. Never use the relay or fleet metadata as a fallback store.

### R-015 — high — Revocation and policy narrowing are missing from acceptance
**Affects:** ACC-005–007, SES-006/007, CON-001–003, NET-003/004, INT-005; Q20/Q23.
**Problem:** Tests cover key expiry and initial denial, but not revoking an active SSH attach, event stream, share, URL, broker handle or removed host. A caller can keep an authorized stream open after authority changes. Rejoining hosts can restore stale grants.
**Fix:** Add revoke operations, propagation deadlines, offline lease rules and a pending/completed result. Recheck authority at use/reconnect and close affected active channels. Test online revocation, partitioned execution hosts, role downgrade, host removal and stale replay across failover. Scope delegation must only narrow authority.

### R-016 — high — Approval is a badge rather than a complete user action
**Affects:** CRED-006, CON-003, KIT-003/005, SES-007, OPS-003; Q22/Q23.
**Problem:** Examples request or show pending approval, but do not show Lamp CLI/API/MCP approve/deny operations, authenticated phone entry, expiry, or approval cancellation. An approval can be reused after a destination/form/action changes. A fake approval UI does not prove the host binds the approved operation.
**Fix:** Define an approval object with principal, sandbox generation, exact action/destination digest, expiry and one-use identity. Show request→inspect→approve/deny→consume in all interfaces and console/phone views. Test timeout, replay, concurrent consumption, changed targets, malicious task text and a watch-only person attempting approval.

## Findings: data, access and failure cases

### R-017 — high — Shared storage and forked external state have no consistency contract
**Affects:** STO-001/002/004, SBX-007, TPL-002, HST-004; Q11/Q15.
**Problem:** “Expected file state under documented semantics” leaves the semantics unspecified. Two simultaneous attachments could be unsafe shared writable block disks. Forks may duplicate a volume writer or remote lease. Coordination failover does not preserve data on a sleeping storage host. Designs propose materially different file-service and block-writer modes.
**Fix:** Specify shared-files versus exclusive-block attachments, locks/rename/fsync behavior, cross-host coherence, disconnect timeout and durability. Declare which attachments fork shares, captures or rejects. Test simultaneous writers, partitions, storage-host loss, stale leases, source deletion and surviving descendants. Do not require any particular storage service or filesystem.

### R-018 — high — Output collection can race mandatory CI deletion
**Affects:** STO-003, LIF-003/004, CI-001/004, OPS-004; Q15.
**Problem:** Outputs must survive success, failure and cancellation, while execution is deleted at job end. No order or bounded failure behavior is defined for partial uploads, full disks, cancellation during export or an unavailable output store. “OUTPUT_NOT_AVAILABLE” can conceal silent data loss or indefinite retention of a compromised runner.
**Fix:** Define collecting, complete and incomplete output manifests, verified finalization, retry/retention limits and teardown ordering. Revoke job execution immediately on terminal/cancel events. Show which logs survived, which bytes are incomplete and whether cleanup failed. Test transfer interruption and service restart with real post-deletion downloads.

### R-019 — high — Own-format templates lack a usable and safe build contract
**Affects:** TPL-001–003, KIT-004, SBX-003, BEN-004; Q12/Q31.
**Problem:** `{"name":"linux-tools","format":"lamp"}` does not describe how a clean host gets bootable tools. The fixture `.lamp` definition is absent. Requirements fuzz the parser but omit hostile archives, disk backing paths, corrupted chunks, untrusted build steps and reference-safe deletion. All designs place builds behind a boundary and pin compatibility/provenance.
**Fix:** Include a complete illustrative Lamp definition, build progress/failure, source verification and immutable version resolution. Require confined builds/imports, bounded archive/path handling and protected ancestors during GC. Test malicious build inputs and interrupted saves. If adopted, the pending native-OS recommendation requires building macOS/Windows templates on the person's own suitable hosts with licensed media.

### R-020 — high — Update acceptance omits artifact trust and recoverable state
**Affects:** HST-005, PLG-001/002, TPL-001, LIF-001, OPS-007; Q07/Q18.
**Problem:** Opt-in and concurrency-one tests do not reject tampered releases, rollback attacks or silently expanded plugin permissions. A successful daemon version readback can hide destroyed suspended state or incompatible active workers. The three designs retain compatible workers/readers and validate provenance.
**Fix:** Require artifact verification, permission-preserving plugin updates, active-session policy, state compatibility, health checks and rollback. Test invalid signatures/digests, interrupted updates, N/N−1 operation, failed resume and a sleeping host returning on an old version. Define when a failed host prevents advancing the rollout.

### R-021 — high — Network changes and failed exits have no mandatory safe outcome
**Affects:** NET-001–006, CON-003, CI-005, BEN-004; Q16/Q17.
**Problem:** NET-002 defers fail-open behavior, NET-005 defers atomic changes, and ATK-09 expects no direct bypass. A selected VPN or guard could fail while traffic silently takes another path. Rules changed in the dashboard may leave previously allowed connections open. Private/metadata default blocking is still only a pending owner recommendation.
**Fix:** Architecture should require policy before task release, atomic revision changes, rechecking/closing newly denied flows, transport-bound identity and failure that does not bypass the selected route. Specify DNS/IP/wildcard precedence and all packet paths. Test guard death, IPv6/QUIC, rebinding, private-range redirects and existing-flow revocation. Keep the owner's proposed default policy pending explicitly.

### R-022 — medium — “Own exit IP” needs a provider-qualified observable meaning
**Affects:** NET-002/003, QA-002; Q16/Q35.
**Problem:** Installing three VPN/proxy configurations does not supply three public IPs. The E2E plan does not identify the needed independent exits, reflector or external/private fixtures. Two selected exits behind one NAT can legitimately look identical; a label-only test would misdiagnose success or failure.
**Fix:** Define selected exit versus guaranteed distinct public IP, and show three fixture routes with independently verified source addresses. State provider prerequisites, setup, expected IPs and failure checks. Do not imply Lamp manufactures an exit IP or requires a specific VPN implementation.

### R-023 — high — Public-PR “no secrets” conflicts with real runner authority
**Affects:** CI-003/005, ACC-001/007, CON-001, STO-001; Q15/Q27; Journey 4.
**Problem:** The draft both claims `secrets:false` and leaves runner/read-only checkout credentials unresolved. A native runner often needs job transport authority. Secret scanning alone cannot show that this authority cannot control another job, upload trusted artifacts or mint further credentials. An OIDC-less fork PR is left blocked despite first-release support.
**Fix:** Separate trusted intake/control, minimum one-job transport authority, source reads and forbidden owner/model/vault authority. Prefer host mediation; disclose unavoidable delegated tokens and prove their exact scope. Test token theft/replay, forged workflow trust, absent OIDC, cache poisoning and cross-job result/control attempts. Any exception to literal “no secrets” needs the owner.

### R-024 — medium — Shipped CI and trigger acceptance is too generic to execute
**Affects:** CI-001/002/006, LIF-004, STO-003; Q26–Q28.
**Problem:** “Run a real job” does not identify tested controller/runner versions, native job/step/workflow boundaries, registration cleanup or result protocol. One shell task per provider can pass while cancellation, Docker/service jobs or a restarted controller leave reusable runners. Timer behavior during sleep/DST is unspecified.
**Fix:** Add a per-provider conformance table: version, execution mode, host/guest, fixture pipeline, native result/cancel checks, deregistration and orphan reconciliation. Add signed-source replay and changed-authority tests for triggers. Choose schedule timezone/DST/overlap/missed-run rules in architecture and test duplicate claims during failover.

### R-025 — high — Relay secrecy needs independent enrollment and issuance trust
**Affects:** RLY-001/002, ACC-005, OPS-005, SES-003; ATK-14/15/37.
**Problem:** An endpoint-substitution test after trust is already established does not cover first pairing, malicious grant issuance, compromised relay DNS/certificate authority, or relay-served dashboard JavaScript. A relay can forward ciphertext yet impersonate the initial host or deliver code that steals plaintext. All designs distinguish transport, fleet authorization and bootstrap trust.
**Fix:** Define who authenticates the fleet root, caller enrollment and host certificate issuance independently of relay routing. Require relay grants to be insufficient for fleet actions. Test malicious first enrollment, counterfeit grants, browser-code substitution and host replacement, plus stream replay. State the trusted-host/account/domain limits rather than claiming protection from their compromise.

### R-026 — medium — Stock SSH through a shared relay has no demonstrated route
**Affects:** SES-003, RLY-001/002, HST-002, ACC-005; Q23; Journeys 2/5.
**Problem:** `ssh fleet-a.eilmer.si` does not explain multiplexing multiple fleets behind the relay. SSH sends no HTTPS SNI. A solution requiring a Lamp preamble, patched terminal or mandatory custom ProxyCommand would violate ordinary SSH support. One-time relay credentials also need an account-bound stock-SSH path.
**Fix:** Show standard SSH commands with assigned port/IP or another stock-compatible route, fleet host-key verification and caller account mapping. Test two fleets on one relay, unauthorized attach, resize/detach/reconnect and opaque SSH forwarding. Do not assume every HTTP access plugin automatically transports SSH.

### R-027 — high — Shared-sandbox sessions cannot provide independent secret boundaries
**Affects:** SES-002/004/007, ACC-007, CRED-003, OPS-002; Q20/Q24.
**Problem:** Separate session IDs and typed-input permissions do not isolate files, processes, inherited connections or readable credentials inside one sandbox. A less trusted sub-agent sharing the sandbox may access the parent's secrets even if its Lamp key is narrower. Watch-and-type can also cause a credentialed harness to act without transferring any token bytes.
**Fix:** Disclose the shared authority boundary and require compatible trust for shared mode; choose own-sandbox mode when separation is needed. Define delegation as an intersection of parent scope and task budget. Keep watch, type, shell/files, browser control and approval distinct. Test cross-session process/file access, delegated escalation and sharing a credential-bearing session.

### R-028 — high — Limits do not cover control-plane exhaustion or retained state
**Affects:** SBX-004, ACC-007, INT-004/005, STO-001–004, LIF-005, PLG-001, RLY-001.
**Problem:** CPU/RAM/disk saturation tests miss unbounded events, PTY output, idempotency records, failed reservations, forks' shared backing state, log/artifact uploads, plugin RPC and unauthenticated relay connections. A guest can obey its RAM allocation while exhausting host/control memory or disk. Quota release after partial failure is unspecified.
**Fix:** Define bounded admission, queues, buffers, retained-state accounting and explicit over-limit errors. Charge warm pools, captures and descendants without double-freeing shared storage. Test slow consumers, output floods, inode/disk exhaustion, canceled creates, plugin hangs and unauthenticated connection floods. Assert unrelated authorized work remains usable and cleanup releases reservations.

### R-029 — high — Idempotency does not define expiration or uncertain command outcomes
**Affects:** INT-004, HST-004, SES-002/005, CI-006; Q04.
**Problem:** Twenty concurrent create requests do not prove safe retry after ledger expiry, crash between guest delivery and receipt, coordinator loss, or a typed message already causing an external effect. “No duplicate commands with side effects” can imply impossible exactly-once behavior without a cooperating external service.
**Fix:** Define principal/fleet/operation scope, canonical input conflict, retention/tombstones and expired-key behavior. Persist operation identity before effects. Return an inspectable unknown/pending outcome when execution cannot be established; do not rerun blindly. Test crash boundaries, different bodies, lost receipts, post-retention reuse and failover with a real side-effect ledger.

### R-030 — medium — Events and interactive transports lack reconnect/backpressure semantics
**Affects:** INT-003/005, SES-002/003/007, OPS-001; Q05/Q23.
**Problem:** A live event example and “structured events” do not define event identity, ordering, replay gaps, stdout/stderr/exit framing, binary data or slow-reader behavior. `events_subscribe` assumes an implementable MCP streaming contract without naming one. A console can drop output or replay stale status after teleport and still show live changes.
**Fix:** Choose a transport-neutral event contract, per-resource revisions, output cursors, resync errors and bounded buffering. Show the actual MCP subscription or cursor-read exchange and CLI stream/error exit behavior. Test duplicate/reordered events, expired cursors, slow readers, multibyte/binary output and revocation during a stream.

## Findings: test evidence and owner experience

### R-031 — high — Code tests lack independent persistence and capture oracles
**Affects:** SBX-007, TPL-002, LIF-001/003, STO-001–003, INT-004, HST-004/005.
**Problem:** “Property-test state serialization,” “failure cleanup,” and fake storage/VMM failures do not specify the invariant or committed state. Serialization round trips can pass when both sides share a bug. A simple counter does not detect torn RAM/disk generations, corrupted disk ancestry or lost fsync writes. The designs introduce journals, references, leases and capture barriers that need stronger proof.
**Fix:** Require an independent operation-history model and injected crashes around reserve/capture/publish/ack/delete/GC. Check recovered state, real persisted bytes, parent/descendant reference safety and zero orphan authority. Use deterministic generation-pattern RAM, concurrent disk checksums and actual process/storage restart tests. Fake vendor timing; do not replace the persistence layer being proved with a successful fake.

### R-032 — medium — Security code tests need concrete protocol failure cases
**Affects:** CRED-005, CON-001/002, CI-003, RLY-002, OPS-005; ATK-13/19/20/24/26/37.
**Problem:** The existing “never mock” rules correctly reserve real enforcement for integration/E2E. They still leave code tests as broad parser/fuzz labels. Concrete omitted regressions include request smuggling, HTTP/2 cross-owner reuse, invalid upstream TLS, JWKS rotation confusion and secrets split across streaming chunks. A happy-path proxy can pass its stated basic substitution tests while failing these cases.
**Fix:** Add named fixtures and assertions for those cases, real test-key JWT signatures, algorithm/issuer/audience failures, redirect reauthorization, MCP audience/refresh mix-ups, SigV4 canonical/streaming requests and CA trust-store failures. Keep cryptographic verification, canonical request building and response filtering real inside their tests; fake external vendors and time around them.

### R-033 — medium — The five journeys are narratives, not yet runnable tests
**Affects:** QA-002, TPL-001/003, CRED-001/002, SES-002/004/005; Journeys 1–5.
**Problem:** Journey 1 never supplies the template/settings files or recipe-create step. Journey 3 sends to `se_try_a`/`se_try_b` without an operation that discovers their IDs. Fleet aliases, connection provisioning, exact public revisions, expected task outputs and cleanup are incomplete. A real model may never ask the question needed for a status/input assertion.
**Fix:** Give each journey a fixture manifest, initial state, complete commands/API requests, returned-ID capture, exact assertions and teardown. Use a deterministic harness fixture for transport/status/fault cases, plus separate real official-harness inference/continuation checks. Do not certify vendor compatibility with the fixture harness or make correctness depend on a model choosing a particular patch.

### R-034 — medium — External hosts are acknowledged, but release lanes are not executable
**Affects:** QA-001/002, HST-001, SBX-002/003, BEN-001–005; Q01/Q35.
**Problem:** The draft correctly says the three owner machines do not prove Windows or native Linux arm64. QA-001 only proposes Windows infrastructure; there is no corresponding provisioned arm64 lane definition, hourly provider qualification, recovery path or guaranteed test ownership. Testing only macOS 26/latest Linux/Windows cannot prove any subsequently approved minimum OS matrix.
**Fix:** Make qualifying Windows and arm64 evidence mandatory, while leaving provider selection architectural. Define rental preflight for virtualization, OS licensing, resource capacity, secure access, logs, teardown and cost limits. Inventory Ubuntu/Air first. If adopted, add Linux 5.10+, macOS 14+, Windows 11 and Server 2022+ boundary lanes; do not inherit the designs' newer OS minima. The M5 on macOS 26 cannot establish macOS 14 support. Use a qualifying Air or another suitable Mac. Keep nested and physical results distinct.

### R-035 — medium — “ALL as applicable” can silently erase difficult E2E combinations
**Affects:** SBX-003/005–007, KIT-001/005, CRED-001/002, CI-001/002, SES-001/003, QA-001/002.
**Problem:** Labels name host types, but “where supported,” “eligible hardware,” and “as applicable” do not identify required guest/device/harness/auth/console combinations. No listed machine proves every GPU/audio/input path. A headless rented Windows host may not prove desktop devices. NV establishes only Linux fallback, not nonvirtualized Mac/Windows behavior. The same ambiguity can mark a named harness or native job unsupported and still report a green aggregate.
**Fix:** Publish a capability-to-lane matrix with host OS/CPU, guest class, hardware, plugin/harness version, authentication method, steps and expected result. Separate not requested, architecturally unsupported, vendor unavailable and test not run. None automatically passes binding scope. Include real native-guest/device lanes and separate Windows desktop/Server coverage when promised.

### R-036 — high — Optional benchmark methodology leaves hard targets movable
**Affects:** BEN-001–005, HST-006, ENG-001; Q08/Q33/Q34; benchmark protocol.
**Problem:** The numbers are correct, but the workload/resource/cache/load envelope and almost all protocol rules are only proposed. An implementer can pick a tiny clean workload, disable expensive default audit/policy/dirty tracking, or choose favorable disk/network measures and still claim a pass. Memory size and durability make fork time materially different across designs.
**Fix:** Before implementation acceptance, freeze a versioned mandatory target configuration and workload list for both Linux architectures. Require normal security/audit/fork-tracking costs, cold versus prepared versus warm-pool labeling, dirty RAM/disk, first-write penalties, timeout treatment and equivalent native baselines. Define the ≥0.97 boundary calculation precisely. Envelope narrowing needs owner approval; Mac/Windows numeric targets follow measurements.

### R-037 — medium — The ten pending owner recommendations need a decision register
**Affects:** INT-002, HST-001, NET-001/004/005, KIT-002/005, ACC-006, OPS-007/008, BEN-004/005, QA-001/002; Q01/Q02/Q16/Q17/Q20/Q22/Q31/Q32/Q34/Q35.
**Problem:** The draft's 35 combined owner/architecture questions hide ten known pending recommendations. Several recommendations change binding interpretations, especially SDK release timing, protected-secret opt-ins and benchmark publication. A reviser could adopt them silently or leave the same decisions scattered through multiple acceptance criteria.
**Fix:** Use the ten-row register below. Mark each pending until the owner decides. If accepted, update every affected statement, example and test; record any authorized departure from the brief/ADR wording. Do not treat a review recommendation as owner approval.

### R-038 — medium — Dashboard coverage names information but omits complete interactions
**Affects:** PRD-002, OPS-001–003, TPL-003, NET-004, SES-005–007, ACC-006/007.
**Problem:** Most rows say the dashboard “shows” a resource. They do not show creating/editing a recipe, selecting a host/device, reading effective policy, sharing/revoking a session, moving it, or recovering failed setup. An informational card for every resource can satisfy those descriptions without providing the owner's requested visual workflows.
**Fix:** Add short screen-by-screen flows with inputs, enabled actions, validation, progress, success, stale/empty state and recovery. For example: New sandbox → recipe preview → compatible hosts/resources → Create → preparing stages → ready command/session → failure with retained operation ID and retry/cancel. Tie every visual mutation to the same API result and scope rules.

### R-039 — medium — The product operation inventory is missing lifecycle actions
**Affects:** INT-001/003, TPL-001–003, STO-001–004, NET-001–006, PLG-001/002, ACC-007, SES-007, OPS-008.
**Problem:** Many examples inspect or attach resources without showing how to create, update, detach, revoke or delete them. Key creation never becomes a full rotate/revoke journey; dev URL unpublish and connection removal are absent. “Every capability in all interfaces” cannot be verified without an authoritative operation list.
**Fix:** Add a compact resource/action/scope matrix and at least one complete create→use→change→remove example per resource family. Include denial, not-found, in-use and already-deleted behavior. Separate required product operations from illustrative developer tools such as `lamp test-host inspect`; do not accidentally mandate a runtime API for every engineering check.

### R-040 — medium — Concurrent edits and cancellation have no conflict semantics
**Affects:** TPL-003, NET-001/005, ACC-006/007, SES-005/007, CI-006, OPS-002/008.
**Problem:** Two consoles can overwrite policy, budgets, recipes or consent from stale views. Retries and cancellation can arrive while creation, teleport or approval consumption commits. The draft checks some isolated races but has no common object revision or cancellation contract.
**Fix:** Specify optimistic revision/precondition behavior or an equivalent observable rule. Reject stale dangerous edits and show the effective revision. Define cancellation before/after commit, noncancelable external effects, and the operation's final state. Test stale dashboard edits, concurrent consent changes, cancel-after-success and authority narrowed between admission and execution.

### R-041 — medium — Fleet modes are flags without an admission or role contract
**Affects:** ACC-005–007, SES-006/007, STO-004, PRD-001; Q20.
**Problem:** Switching a mode string proves neither team enrollment nor a usable public service. The E2E test refers to a “documented authorization matrix” that does not exist. Four proposed role names alone do not settle who invites people, admits outside callers, manages keys, approves secrets or reads private sessions.
**Fix:** After the pending role decision, define the role/action matrix, identity mapping, invitation/removal and public admission/quota flow. Preserve own-only sessions and owner-only storage by default. Test role downgrade, outside-service admission, account collisions across issuers and existing resource privacy when changing fleet mode.

### R-042 — low — One journey weakens the strict fork threshold
**Affects:** BEN-002, LIF-006; Journey 3 pass evidence, draft line 1320.
**Problem:** “≤ target Linux fork observations” admits equality, while the brief and BEN-002 require under one second. The other hard values are correctly stated: create p50 <500 ms, fork <1000 ms, each native-speed ratio ≥0.97, zero protected leaks, two macOS sandboxes per Mac and 50 new certificates/week.
**Fix:** Replace the journey text with “Every declared Linux fork trial is <1000 ms, including capture, stripping, source continuation and usable-fork proof.” Keep exactly 1000 ms failing and exactly 0.97 passing in boundary tests.

### R-043 — low — Repetition makes the owner's product hard to read
**Affects:** PRD-001–003 and all requirement families; reading rules, traceability and inventory.
**Problem:** The draft is 1,604 lines. Repeating illustrative administration commands, source paragraphs and “never mock” language hides journeys and unresolved product conflicts. “Complete topic coverage” means names are present, but reads like proof that behavior is complete. Technical terms such as VM/snapshot are justified where they describe boundaries/capture; I found no need to rename the glossary's product nouns.
**Fix:** Start with product journeys, supported host/guest classes, defaults, user-visible states and pending owner decisions. Follow with concise numbered contracts and interface examples. Move shared test rules, host lanes, attack cases and benchmarks into appendices. Use separate traceability columns for named, specified, tested and blocked. Do not call a safe refusal implemented feature support.

## Binding-source coverage audit

I checked the brief's paragraphs, bullets and target rows in order. No entire named brief feature is absent.
The gaps are incomplete contracts, unresolved conflicts and insufficient proof, not a missing product-name checklist.
“Covered” below means a requirement states the decision. It does not claim implementation or passing tests.
Every weak item points to a finding above; repeated findings describe one shared defect, not extra counts.

| Brief location | Decisions/features checked | Coverage and remaining gap |
| --- | --- | --- |
| §1, product | Open source; self-hosted; Docker-like install; isolated on-demand agent/harness/CI sandboxes; Modal/Daytona comparison | PRD-001/002, SBX-001, INT-001; installation outcome weak: R-011/033/038. |
| §1, ambition/identity | Speed, security, agent fit; public proof; Eilmer and eilmer.si | PRD-001, BEN-001–005, OPS-005/007; proof envelope/publication: R-036/037. |
| §2, audience/goals | Everybody day one; equal parallelism, safety, setup friction and ownership | PRD-001/003; “equally” has no pass rule: R-011. |
| §2, references | Cloudroom; E2B Embed; Docker Sandboxes; microsandbox; SmolVM; Matchlock; Modal/Daytona | BEN-005 and benchmark inventory preserve required competitors versus context; covered. |
| §3, callers/interfaces | Person, agent, GitHub agent, CI, outside service; API/MCP/CLI parity | INT-001, CI-002/003; operation inventory and concrete parity: R-003/039. |
| §3, SDKs | One API definition; TypeScript/Python first; Go/Rust afterward | INT-002; pending release timing: R-037. |
| §3, agent use | Every CLI command JSON; LLM-readable docs; idempotency; live events | INT-003–005; framing, safe retries/replay: R-029/030. |
| §4 | Agent inside autonomously; outside commands, files and browser; caller chooses | INT-006/007, KIT-001; ready/control contracts: R-003/004/033. |
| §5, platforms | Linux x86_64/arm64, Apple Silicon macOS, Windows in release one | HST-001, QA-001/002; actual lanes/support minima: R-034/035/037. |
| §5, fleet | One address/key set; many hosts; fleet of one | HST-002, SES-001; transport/enrollment/failure behavior: R-007/025/026. |
| §5, placement | Room/OS/devices automatically; caller pin | HST-003; capacity and credential locality: R-014/028. |
| §5, continuity | Automatic coordinator move; sleeping laptops normal | HST-004; two-host and partition outcomes: R-007/015. |
| §5, updates/engine | Opt-in, one host at a time; fastest supported engine per host | HST-005/006; trust/rollback and comparison rule: R-011/020/036. |
| §6, boundary/resources | Firecracker-like speed/isolation; requested CPU/memory/disk and devices | SBX-001/004/005; bounded accounting and real device lanes: R-028/035. |
| §6, guest classes | Linux everywhere; macOS only Mac, max two; Windows only Windows; slower full-VM label | SBX-002/003; native-class proof and pending minima: R-009/034/035. |
| §6, fallback | Clearly weaker boundary if no VMs; secrets/logins require VM or owner override | SBX-006; covered; consent/revocation details: R-015/016. |
| §6, public CI/fork | Fresh VM always for public-repo CI; live RAM-inclusive fork | SBX-006/007, CI-005; feasible capture and CI authority: R-001/008/009/023. |
| §7, timings | Linux create p50 <500 ms API→actual command; live fork <1 s | BEN-001/002; exact values preserved except R-042; envelope/proof: R-036. |
| §7, throughput | Linux compute, disk and network separately ≥97% native | BEN-003; exact floor preserved; mandatory workloads: R-036. |
| §7, secrecy | Zero API-key/password/cookie leaks everywhere; subscription exception | BEN-004, CRED-003/004; protected boundary and opt-ins: R-001/002/037. |
| §7, other OSes | Set Mac/Windows targets after hypervisor measurement | BEN-005; covered; cannot inherit speed-design aspirations as hard limits. |
| §8, templates | Own format or save running state; no Docker image/Dockerfile/runtime; installed tools persist | TPL-001/002; build definition, sanitation and save correctness: R-001/019/031. |
| §8, kits/recipes | Optional Docker inside for build/Compose/services; seven named recipe parts; request by name | KIT-004, TPL-003; actionable recipe creation/versioning: R-003/019/039/040. |
| §9, default lifecycle | Idle suspend saves RAM/disk, no CPU/RAM, instant next-message resume, timed deletion | LIF-001–003; idle exemptions/defaults/observable resume: R-010/011. |
| §9, CI/overrides | Job-end deletion default; per-sandbox overrides | LIF-003/004; export/deletion/cleanup failure: R-018/028. |
| §9, activity | No session activity, running processes or connections; background work counts | LIF-002; literal contradiction with live infrastructure: R-010. |
| §9, parallel work | Warm pool; fork mid-task, explore, keep winner | LIF-005/006; matching/accounting/fork continuity: R-001/008/011/028/033. |
| §10 | Shared package/build caches; named multiattach volumes; CI outputs/logs; updated templates; all owner-only | STO-001–004, TPL-002; storage semantics, poisoning, partial export: R-017–019/023/031. |
| §11, enforcement | Host owns all network setup; guest cannot change it; API/MCP/CLI; network plugins | NET-001, PLG-002; required failure/revision behavior: R-006/021. |
| §11.1 | Per-sandbox VPN/proxy/service/direct exits and exit IPs | NET-002; actual route/provider meaning: R-021/022. |
| §11.2 | Private membership, including separate Tailscale device | NET-003; fork identities, revocation and test fixtures: R-008/015/022. |
| §11.3 | Inbound dev-server URLs | NET-004; private/share default pending, expiry/revoke and port isolation: R-015/037/039. |
| §11.4–5 | Site allow/deny; sandbox-to-sandbox only when allowed | NET-005/006; composition, atomicity and bypasses: R-005/021. |
| §12 | Customizable/modular; anyone's separate-program plugins; no core crash or unauthorized sandbox reach; all nine type families | PLG-001/002; trust exception and real conformance: R-006/020/028. |
| §13, harnesses | Claude Code, Codex, OpenCode, Cursor CLI, Copilot CLI, Amp, Goose, Gemini CLI, Pi, Devin, Droid, others | CRED-001/002; required execution/auth/OS evidence must be explicit: R-035. |
| §13, subscriptions | Already signed in; approved routes/catalogue; person's hosts/sandboxes; vendor-only traffic; strip before capture; no Lamp-server custody | CRED-002–004; joint satisfiability, locality and continuation: R-001/005/014/025. |
| §13, API keys | Host proxy substitution; real keys never enter sandbox | CRED-005; protocol/adversarial integration proof: R-002/032. |
| §13, settings | Chosen one-way source; harness settings, skills/plugins/slash commands, CLAUDE.md/AGENTS.md, MCP list, git identity/signing; approved return only | CRED-006; real signing boundary and approved diff application: R-014/016/033. |
| §14, scope/MCP | No stateful-app migration feature; signed-in host MCP gateway; limited inside-server tokens | CON-001/003; actual delegation/audience and token exception: R-023/032/039. |
| §14, CLI | gh/stripe/aws tokens through host proxy; none inside | CON-002; signing/streaming and scope tests: R-032/035. |
| §14, local services | Laptop DB/dev server/home service; off/read-only/ask first/full | CON-003; enforceable operation semantics, approvals: R-005/016/017/021. |
| §15, browser | External default, hidden cookies; inside/copy opt-in; remote agent control | KIT-001/002; protected-site boundary, opt-in and inheritance: R-002/016/027/037. |
| §15, takeover | Human completes 2FA/CAPTCHA through console and returns control | KIT-003; exclusive control, phone/SSH route and sensitive observations: R-002/016/026. |
| §15, vaults | 1Password, Bitwarden, KeePassXC, Proton Pass; external fill default; per-secret injection opt-in; per-vault per-use phone/console approval | KIT-005; real supported fill paths and action-bound consent: R-002/016/035/037. |
| §15, other kits | Toolchains, Docker-inside and other software/state kits | KIT-004, PLG-002; optional guest kits are correctly distinct from host plugins. |
| §16, consoles | Any configured console via plugin; fleet as one machine; every session independently open/watch/type; working/blocked/idle | SES-001/002; state/stream contracts and real public-console matrix: R-010/030/033/035. |
| §16, SSH | Ordinary terminal SSH lists, attaches live, or opens sandbox shell everywhere | SES-003; stock relay routing and host trust: R-025/026. |
| §16, sub-agents | Starter chooses own copy or shared sandbox; git return; visible sessions | SES-004; exact starting files, authority and budget inheritance: R-012/027/033. |
| §16, teleport | Running session between laptop and sandbox | SES-005; supported continuation, operation/fencing/failure proof: R-004/008/029/033/040. |
| §16, sharing | Own-only team visibility; explicit watch or watch-and-type | SES-006/007; revoke/role matrix and shared authority: R-015/027/041. |
| §17, routes | Built-in NAT-friendly/keyless-CI Tailscale; Eilmer relay; own-domain automatic HTTPS; local-only; Cloudflare plugin | ACC-001–004, RLY-001; no missing route; transport-specific proof: R-025/026/035. |
| §17, relay | AGPL open source/self-run + Eilmer hosting; sign-in/linkage/one-use grants; never decrypt/model-credential/sandbox-data custody | RLY-001/002, OPS-007; bootstrap, browser-code and independent grant authority: R-025. |
| §17, accounts/modes | One Clerk Eilmer account for relay; local/own OIDC/offline otherwise; owner/team/public-service configuration | ACC-003/005/006; admission/roles/offline authority: R-007/015/041. |
| §17, keys/CI | Sandbox/recipe/host/action/resource/expiry limits; GitHub token exchange without saved secret | ACC-007, CI-003; active revocation, trust claims and job authority: R-015/023/028/032. |
| §18, CI set | GitHub Actions/Copilot/Claude/Codex; GitLab; Buildkite; Jenkins; Forgejo/Gitea; CircleCI; Azure; Bitbucket; Woodpecker; third-party plugins | CI-001/002; complete names; versioned native conformance: R-024/035. |
| §18, triggers/jobs | Slack, Linear, webhooks, schedules; fresh job sandbox with lasting caches | CI-004/006; teardown/orphan/scheduling semantics: R-018/023/024/040. |
| §18, untrusted PRs | Fresh VM, no secrets, limited network for strangers | CI-005; transport-token contradiction and real attacks: R-021/023. |
| §19 | Warm pool/fork/teleport; live full dashboard; time/token cap/watchdog; done/stuck/decision notifications; every-command/request audit; timer; no cloud overflow; all first release | PRD-003, LIF-005/006, SES-005, CI-006, OPS-001–004; weak contracts: R-001/011–013/028/038. |
| §20, domains/business | All hosted surfaces under eilmer.si; cookie/origin separation; certificate cap; no payments; room for future plans/quotas/billing | OPS-005/006; values covered; publication/trust/global accounting proof: R-025/028/036/037. |
| §20, licenses/privacy | Apache host/SDK/plugin code; AGPL relay; private until engine demo; off-by-default opt-in usage data | OPS-007/008; pending telemetry scope/90 days and native builds: R-019/037/040. |
| §21 | Public reproducible every-change suite; five named competitors; §7 plus agent-ready/fork/density/reliability/attacks | BEN-001–005; complete metric inventory; binding protocol and publication timing: R-036/037. |
| §22, sequencing/reuse | Engine create+RAM fork first, no Docker; own components except security/likely-fastest reuse; architecture chooses language with Rust/Go trade-off | ENG-001–003; correctly architecture-neutral; acceptance/proof details: R-031/036/039. |
| §22, machines/fixtures | Exact M5 10-core/24-GB/macOS-26; Ubuntu/Air inventory; no Windows machine; external testing plan; never owner projects/special adapters; public harnesses/consoles/MCP | QA-001/002; correct constraints; executable missing lanes: R-033–035. |
| §23 | Six research inputs: Modal/Daytona, remote access/CI, VM backends, CI/integrations, credentials/stateful apps, subscription auth | Reading rules and ENG requirements identify all six; research is correctly subordinate to brief/ADRs. |

ADRs 0001–0009 all have explicit references. The main unresolved binding conflicts are ADR 0001 failover, ADR 0002 network composition, ADR 0004 capture, and ADR 0009 bootstrap trust. ADR 0006's domain decision must remain even if a second domain would be easier. No requirement should rename a sandbox as a container, a template as an image, or a recipe as a profile. Technical VM/capture vocabulary is appropriate when explaining implementation boundaries.

## Architecture comparison used in this review

These are alternative mechanisms. The final requirements should demand outcomes without choosing among them.

| Source evidence | Required outcome it exposes |
| --- | --- |
| Speed §§4/11: local admission/home journals; security §11: Raft plus bounded local permits; simplicity §12: three voters | Explicit one/two/three-host availability, deduplication and fencing; R-007/029. |
| Speed §5: extent/file services; security §§4–5: dm-thin/block deltas and storage worker; simplicity §§5–6: backing chains/SMB service | Consistency, attachment semantics, crash recovery and storage-host loss; R-017/019/031. |
| Speed §7, security §7, simplicity §8: unsafe subscription capture, typed proxy/browser boundaries | Successful supported workflows plus honest refusal, trusted-recipient and exposure limits; R-001/002/005/032. |
| All engine sections; simplicity §§4/21 explicitly flags VZ native fork | Native guest/device proof separately from Linux-on-Mac/Windows; R-009/035. |
| All access sections: independent trust, raw SSH ports, per-fleet certificates | Stock-client reachability, no relay plaintext or forged fleet authority; R-025/026. |
| Security §1 and simplicity §4 propose newer minima; all API sections propose four SDKs before release | Do not copy these policies over the owner's pending older minima and TS/Python-first-release recommendation; R-034/037. |
| All observability/performance sections | Hard-cap/audit feasibility and measured secured workloads; R-012/013/036. |

## The 35 open questions: two decision groups

These groups assign each original Q ID once: **21 architecture-led, 14 owner-led**.
Several original rows bundle policy, mechanics and empirical/vendor facts. The allocation below separates those parts explicitly.
Architecture must still record concrete decisions; “architecture-led” does not mean the gap disappears.
Vendor permission, hardware capability and licensing facts need evidence. Neither owner preference nor a diagram can manufacture them.

### Group A: architecture can settle these without changing the brief

| Question | Required architectural answer |
| --- | --- |
| Q03 | Enrollment/removal identities, reservations, queue/reject/backpressure, supported fleet limits and errors. |
| Q04 | Idempotency scope, input conflicts, retention/tombstones and uncertain outcomes. |
| Q05 | Event delivery/order/cursors/retention, flow control and proposed live-update target. |
| Q06 | Safe failover/rejoin, recovery target, partition behavior and no-survivor errors. A proposed scope reduction returns to owner. |
| Q07 | Update drain, health, rollback, version and saved-state compatibility. |
| Q08 | Comparable eligible-engine ranking and refresh rules; security/required devices precede speed. |
| Q09 | Units/defaults/maxima, accounting/overcommit, OOM/disk behavior; conservatively enforce the two-macOS cap with actual licensing evidence. |
| Q10 | Hardware/device/guest support matrix and boot infrastructure. Removing a promised device class is not an architectural resolution. |
| Q12 | Template/recipe format, naming, immutable versions, compatibility and confined build primitives. |
| Q13 | Proposed idle/deletion defaults, clock basis, infrastructure exemptions, wake inputs and measurable resume target. Preserve the background-work rule. |
| Q14 | Warm matching/sizing/refill, single-use clean assignment and sign-in after owner binding. |
| Q15 | Storage consistency, quotas/retention/eviction, cache trust separation and cleanup. |
| Q18 | Plugin protocols, trust classes, permissions, containment, failure/timeouts, compatibility and provenance. |
| Q19 | Research vendor-approved versions/routes, refresh ownership, TLS/signing and capture hooks. Capture scope needing owner choice is isolated under Q11. |
| Q21 | Prove which MCP servers need inside execution; define limited token scope/lifetime and brokered alternatives. No broad-key loophole. |
| Q23 | Status definitions, stock SSH identity/I/O/reconnect/writers and graphical takeover route. |
| Q24 | Consistent parent-file selection, uncommitted/untracked/ignored files, temporary git for non-git tasks and conflict/approval mechanics. |
| Q25 | A session continuation contract, tested harness/direction matrix and single-location rollback. Do not claim arbitrary cross-OS RAM transfer. |
| Q26 | Research and pin native CI/controller/action versions, modes and supported Copilot route. Unsupported required routes remain gaps. |
| Q27 | Verified immutable forge trust, short-lived key policy, OIDC-less trusted intake and brokered runner transport. An unavoidable literal “no secrets” exception needs owner approval. |
| Q28 | Trigger identity/signature/replay and scheduling timezone/DST/missed/overlap/retry/cancel/dedup rules. |

### Group B: owner policy or product meaning is still needed

| Question | Owner decision; engineering parts stay with architecture |
| --- | --- |
| Q01 | Pending minimum OS/edition/CPU support recommendation. Architecture inventories machines and proposes install steps/privileges. |
| Q02 | Pending first-release SDK cutoff: TS/Python, with Go/Rust right after. |
| Q11 | What continuation may restart or be refused for token stripping, and whether full-VM/device fork limitations are acceptable. Identity/clock/attachment mechanics stay architectural. |
| Q16 | Pending default egress recommendation. Matching, precedence, protocol support, atomicity and failure handling stay architectural. |
| Q17 | Pending private dev-URL default with expiring sharing. Auth, port binding and revocation mechanics stay architectural. |
| Q20 | Pending four role names. Architecture supplies a least-privilege action matrix preserving own-only session/storage defaults. |
| Q22 | Pending exclusion of authorized exposures from zero-leak scoring. Also approve the protected browser/site/recipient trust boundary if it narrows the promised workflows. Vendor fill capability remains an evidence gate. |
| Q29 | Whether hard token caps must work in opaque subscription/outside modes or a narrower enforceable contract is acceptable. Architecture proposes accounting, watchdog thresholds and delivery channels. |
| Q30 | What “every command and network request” means for builtins, hostile root and opaque encrypted traffic. Architecture implements collectors/fields/retention/redaction after this meaning is fixed. |
| Q31 | Pending owner-built native-OS templates. Counsel/vendor evidence establishes redistribution rights; architecture separates Lamp-owned code from third-party licenses. |
| Q32 | Pending counts/timings-only opt-in data and 90-day retention. Architecture defines payloads, consent propagation, deletion and necessary relay metadata. |
| Q33 | Acceptable performance envelope and whether any admitted configurations are outside hard targets. Architecture fixes reproducible workloads/statistics and measures Mac/Windows. Do not turn fork <1 s into a percentile waiver. |
| Q34 | Pending private benchmarking until first public release. Architecture defines exact change/revision gating and hardware scheduling. |
| Q35 | Pending hourly Windows/arm64 rental policy, with spend cap/responsible account. Architecture qualifies provider/virtualization/licenses and automates teardown. |

### Register of the ten pending owner recommendations

All ten remain **pending** in this review. The round-3 reviser must not present them as already approved.

| # / original Q | Recommendation to put before the owner | Requirements/tests to update if accepted |
| --- | --- | --- |
| 1 / Q02 | TypeScript and Python SDKs in release one; Go and Rust right after | INT-002, PRD-003, SDK inventory and release gate; explain the authorized scope interpretation. |
| 2 / Q35 | Rent Windows and native Linux arm64 test machines by the hour | QA-001/002, real-host matrix, provisioning/teardown/cost checks; test infrastructure is not product cloud overflow. |
| 3 / Q16 | Open internet by default; block private networks and metadata; public-PR CI allowlist only | NET-001/005, CON-003, CI-005; explicit approved private connections must still work; test IPv4/IPv6/rebinding/redirect bypasses. |
| 4 / Q17 | Dev URLs private by default, shareable through expiring links | NET-004, SES-007; authorized versus anonymous/private requests, expiry/revoke, scoped ports and no link-token leakage. |
| 5 / Q22 | Authorized opt-in exposures do not count against zero leaks, but are flagged | KIT-002/005, SBX-006, BEN-004, ATK-32 and every summary; opt-in is scoped, authenticated and cannot waive public-PR rules. |
| 6 / Q34 | Benchmarks stay in private CI until first public release | BEN-005, OPS-007, ENG-001 and reporting protocol; record this change from immediate public proof, separate from engine-demo source visibility. |
| 7 / Q32 | Opt-in usage data contains counts and timings only; retained 90 days | OPS-008, consent/data schema, deletion and outbound tests; no versions, prompts, paths, URLs or output smuggled into counts/timings. |
| 8 / Q31 | People build macOS and Windows images on their own hosts | SBX-003, TPL-001/002, QA-001; call the resulting public product resource a template, retain licensed-media/host-build evidence. |
| 9 / Q20 | Roles: owner, admin, member, viewer | ACC-005–007, SES-006/007, STO-004, approval/admin tests; role names do not grant private-content access implicitly. |
| 10 / Q01 | Linux 5.10+, macOS 14+, Windows 11 and Server 2022+, x64 first | HST-001, PRD-002, QA lanes and installers; clarify editions/hardware, retain Apple Silicon and first-release Linux arm64. Do not reinterpret x64-first as deleting those binding rows. |

### Other owner questions, beyond those ten

1. **Q11 / R-001/009:** Is restarting only the credential-bearing harness, or refusing unsafe subscription/native-device forks, an acceptable supported workflow? If not, general capture remains a release blocker.
2. **Q22 / R-002:** Which protected browser/site trust boundary and unsupported-flow fallback is acceptable? The pending opt-in scoring decision alone does not solve secret reflection during ordinary protected use.
3. **Q29 / R-012:** Must hard token caps cover every subscription and outside-agent mode, or may unmeterable routes be rejected or explicitly best effort? What, precisely, does the owner accept as “cap”?
4. **Q30 / R-013:** Is external exec plus mediated-request/opaque-flow audit acceptable, or is finer hostile-guest semantic completeness required?
5. **Q33 / R-036:** Which resources, devices, durability, dirty-state and load conditions must meet the Linux hard targets? Any excluded accepted configuration must be an owner-approved scope decision.

Two contingent decisions must not become silent scope cuts: any requirement for a third host to obtain automatic two-host failover (R-007), and any unavoidable job-transport-token exception to public-PR “no secrets” (R-023). Architecture should first try to meet the binding brief. Likewise, unavailable proprietary harness routes need vendor evidence or an explicit owner-approved scope change, not a fabricated local executable.

## Round-3 response and completion rule

For each R ID, record **accepted** with the revised requirement/test location, or **rebutted** with the binding source and reasoning. A safe error path alone does not close a required successful workflow. A recommendation remains pending until the owner decides it. Preserve requirement IDs or provide an explicit old→new mapping. This critique reports specification gaps; it makes no claim that code, E2E, security or benchmark tests have run.
