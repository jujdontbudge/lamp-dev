# Round 4: architecture red team

Status: complete, 2026-11-08. **16 findings: 1 critical, 11 high, 4 medium, 0 low.**

Scope: `docs/spec/architecture.md`, checked against the final requirements, brief, glossary, ADRs and rounds 2–3. All O1–O10, A1–A5 and B1–B4 decisions are binding. All proposed hosted addresses remain under `eilmer.si`.

Method: concrete attacker/failure traces, with primary-source verification of external technical claims. This is a design review, not an implementation penetration test or a benchmark result. Existing release blockers alone are not new findings.

The most urgent repairs are certificate-issuance control, snapshot producer authority, sleep-safe fencing and real stable-storage barriers. A written requirement to fail closed is useful, but it does not identify the component or primitive that makes these traces fail. The final response round should name that mechanism and its acceptance test, or rebut the trace with a specific existing clause/source.

## Findings

### X-001 — critical — A hostile relay can obtain a valid browser certificate without using Lamp's issuer

**Sections:** §15.2–15.3, E12.

**Attack:** Control the public relay's TCP/443 forwarding process. Register an attacker-owned ACME account and request a certificate for `api.f123.fleet.eilmer.si`, not the wildcard. Answer the CA's `acme-tls/1` challenge locally instead of forwarding it. Then serve a fake dashboard with a publicly valid certificate and steal browser authority/content. No fleet-signed CSR, DNS credential, account-service compromise or CA compromise is needed. Native callers with independent pins remain protected.

**Evidence:** The independent issuer only constrains requests sent to that issuer. [RFC 8737 §3](https://www.rfc-editor.org/rfc/rfc8737.html#section-3) validates control by connecting to the name's public TCP/443 endpoint. The architecture specifies no CAA restriction preventing another ACME account or validation method. [Let's Encrypt's CAA documentation](https://letsencrypt.org/docs/caa/) supports account and method binding; [RFC 8657](https://www.rfc-editor.org/rfc/rfc8657.html) defines both. This is a new bypass of the C-029/R-025 repair, within its stated relay-process threat model.

**Fix:** Require centrally controlled CAA for the hosted fleet namespace that authorizes only the independent issuer's ACME account and DNS-01, for both ordinary and wildcard issuance. Audit CNAME/delegation inheritance and every allowed CA. Test an attacker account requesting exact API/content names through a hostile relay, not only forged requests to Lamp's issuer. Keep all names under `eilmer.si`.

### X-002 — high — A content hash is not authority to restore VMM execution state

**Sections:** §5.1, §6.1, §7.1, §18.

**Attack:** A public-service member supplies a template with a correctly hashed acceleration variant containing crafted CPU/device state. Compatibility and CRC/hash checks pass. The engine restores attacker-authored host-facing state rather than booting hostile guest code through the normal device interface. The spec does not identify who is authorized to produce executable acceleration state, or distinguish that authority from template ownership. Ordinary corruption with a mismatching hash should already be rejected; this attack supplies a matching hash for hostile bytes.

**Evidence:** [Firecracker 1.17 snapshot management](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md#snapshot-files-management) explicitly treats snapshot files as trusted input. Its upstream [device-state hardening change](https://github.com/firecracker-microvm/firecracker/commit/14108ca14ef160043aa518d1450b749311b3f0bb) describes a crafted state combination that could bypass a device write gate, while retaining that trust assumption. This is not a claim of an unpatched 1.17 exploit: it is an input-trust mismatch that confinement and checksums alone do not resolve.

**Fix:** Admit acceleration state only with authenticated provenance from an authorized Lamp capture/build worker running the exact qualified engine and approved kernel/device configuration. Bind producer, owner/trust class and manifest digest. Treat arbitrary imported templates as guest disk/build inputs and regenerate acceleration locally. Verify all state before restore; quarantine corrupt variants and their descendants. Test a validly hashed, unauthorized state producer.

### X-003 — high — Host-local allocation permits do not partition a Task's token allowance

**Sections:** §10.1, §14.1–14.2, §17.2.

**Failure:** Two descendants of the same Task run on different hosts. Partition them while both brokers see 100 remaining tokens. Each independently reserves and sends an 80-token request. Both obey their host's capacity permit and local atomic transaction, but the root Task spends 160. A smaller version occurs during normal replication lag. Global owner quotas can remain correct while this task cap is exceeded.

**Evidence:** §14 partitions fleet/owner capacity by host and principal; it gives no per-Task token suballocation, token-reservation authority or transfer protocol. §17 requires atomic parallel reservations across all descendants. A local transaction cannot serialize a write on another host; [SQLite's transaction contract](https://www.sqlite.org/lang_transaction.html) only serializes writers to its database. This is a specific hole in the C-034 reservation repair, not a renewed objection to opaque-traffic accounting.

**Fix:** Give exact-cap Tasks one authoritative reservation home, or precommit disjoint token slices for each task/broker. Include used and outstanding worst-case reservations in slice transfers, fork, teleport and crash recovery. Without the home or a remaining slice, deny dispatch. Test two- and three-host partitions with a shared Task, not only per-owner resource slices.

### X-004 — high — Signature verification lacks a recoverable update trust root

**Sections:** §4, §6.1, §13, §14.3.

**Attack:** Steal the online release-signing key, sign a new supervisor/engine or seed, and distribute it through the normal update path. Alternatively, keep a new installation on an old signed release forever. "Signed digest/provenance" and an unspecified anti-rollback policy do not define trusted key bootstrap, key revocation, freshness, delegation or recovery after that key is stolen. Their coverage of independently fetched guest seeds/kits is also undefined.

**Evidence:** The architecture names no signing-root metadata, threshold, expiration or cross-signing protocol. [TUF's threat model](https://theupdateframework.io/docs/security/) separates signed-but-malicious updates, freeze and mix-and-match attacks; its [metadata roles](https://theupdateframework.io/docs/metadata/) give concrete root, target and freshness responsibilities. Dependency hashes establish selected bytes, not who may authorize replacement bytes.

**Fix:** Specify an offline-root, threshold-capable update trust scheme, with signed target/platform/component identity, length/hash, monotonic versions, expiry and root rotation/revocation. Cover installers, supervisor, engines, kernels/seeds, first-party kits and plugins. Preserve offline use without silently accepting stale update metadata. Exercise online-key theft, expired metadata, mixed component releases and recovery-root rotation.

### X-005 — high — Protected browser traffic has no binding to the requesting task's network policy

**Sections:** §3.1–3.2, §8, §9.3, §11 Browser.

**Attack:** A task restricted to a VPN and a short allowlist opens an approved page through the external browser. The page makes subresource, beacon, worker or WebSocket requests. In host-browser mode those requests can originate from the host; in browser-sandbox mode they use that sandbox's separately unspecified policy/exit. The action adapter can approve the visible origin while background requests leak the host's direct IP or reach an endpoint the task cannot. A compromised page need not get CDP or a cookie-read API.

**Evidence:** Browser has `allowed_origins` and an action adapter, but no specified task-policy/exit binding or rule for shared profiles serving differently restricted tasks. The [Fetch standard](https://fetch.spec.whatwg.org/#goals) documents many network producers beyond navigation, including images, scripts and beacons. The general guest guard does not automatically cover a browser running on the host. This is separate from the already acknowledged same-origin password-reflection problem.

**Fix:** Route every browser network path through a task-bound guard with the same safety ceiling, selected exit, private-service grants, audit and deadlines. Partition profiles/contexts when task policies differ. Cover redirects, subresources, workers, downloads, WebSockets and browser-native UDP; constrain host-browser mode equivalently or reject that mode. Test VPN death and a denied background request while an allowed page remains open.

### X-006 — high — Retaining old state readers can retain an exploitable engine indefinitely

**Sections:** §5.2, §14.3, §17.3, E14.

**Attack:** A critical engine defect is fixed. Lamp updates `lampd`, but keeps vulnerable active workers and the exact old reader required by suspended snapshots. An attacker leaves a snapshot dormant, then resumes it to reactivate the vulnerable parser/VMM. Certification invalidation prevents new selection but does not define a security denylist for retained readers or an evacuation deadline for active guests.

**Evidence:** §14.3 requires compatible old binaries/readers while references remain; §5.2 invalidates rankings. Neither defines the precedence of a security revocation over continuity. [Firecracker's November 2026 advisory](https://github.com/firecracker-microvm/firecracker/security/advisories/GHSA-48ww-rqpp-2mgm) gives a concrete example of a guest-root memory-corruption bug dependent on file-backed restore and certain devices. Lamp's 1.17 reference is patched; this example establishes the lifecycle risk, not a vulnerability in that reference.

**Fix:** Add signed per-digest security revocation with an effective deadline. Block restore with revoked readers, stop/drain affected active workers, and offer a patched compatible reader, isolated disk recovery or explicit cold reconstruction. Preserve original state for recovery without making it runnable. Test revocation against active, suspended, sleeping-host and rollback paths; a health-check rollback must not reinstall a security-revoked engine.

### X-007 — high — Rolling rollback has no control-database migration contract

**Sections:** §11.2, §14.1, §14.3, E14.

**Failure:** Host A upgrades, applies a new SQLite schema or writes a new Raft command representation, then fails its resume probe. Rolling back its binary does not roll back that durable state. The old binary may refuse to start, reinterpret metadata, or require a stale database restore that loses acknowledged operations. Host B may already have replicated the new log entry. Keeping old guest-state readers does not repair the control store.

**Evidence:** N/N−1 RPC compatibility and "no incompatible state rewrite" for engine captures do not specify database/log schema compatibility, upgrade commit points or irreversible migrations. [HashiCorp Raft's FSM description](https://github.com/hashicorp/raft/blob/main/docs/README.md) distinguishes replicated commands, state-machine application, snapshots and restore; these are all formats an upgrade must handle.

**Fix:** Version the control log, snapshots and SQLite projection. Require an expand/contract window in which N−1 reads all state N may acknowledge; activate new command formats only after a committed fleet capability barrier. Journal migrations and their recovery/rollback boundary. Crash after every migration step and roll back after acknowledged writes with the other host still on N−1. Never recover rollback by restoring a stale safety journal.

### X-008 — high — A remote proxy can resolve a different destination after the guard approves it

**Sections:** §8 transport/strict-site modes and selected exits, §8.1, E09.

**Attack:** The guard resolves an allowed hostname to a public address and permits it. The SOCKS/CONNECT plugin sends the hostname to the remote proxy, whose resolver maps it to loopback, metadata or another private network. The final connection therefore violates the private-range ceiling even though the guard checked its own DNS answer. Split DNS can cause this without a malicious plugin. IPv6 and UDP need the same treatment.

**Evidence:** §8 applies policy before the selected exit but does not bind the proxy's final dial to the checked IP. [SOCKS5 §5](https://www.rfc-editor.org/rfc/rfc1928.html#section-5) expressly supports a domain-name destination. Docker's [upstream-proxy contract](https://docs.docker.com/ai/sandboxes/configuration/upstream-proxy/#set-a-proxy-manually) distinguishes local SOCKS resolution from proxy-side resolution. A checked name and an unchecked downstream resolution are different authorities.

**Fix:** Pass a validated IP/port to the proxy and preserve the approved hostname only for TLS SNI/HTTP authority. Prohibit hidden re-resolution, alternate-address fallback and proxy redirects; reject or explicitly limit proxies that cannot satisfy this contract. Bind private-member DNS to that member's dial. A remote exit must be trusted to honor its dial target; sending an IP cannot force a malicious server to obey. Test contradictory proxy DNS, mapped IPv4, IPv6 and remote-private addresses, and state that exit trust explicitly.

### X-009 — high — Lease safety has no concrete clock or wake-before-I/O barrier

**Sections:** §6.4–6.5, §10.2, §14.1–14.3, §17.2–17.3.

**Failure:** A laptop sleeps during teleport or a volume-writer handoff. The other hosts wait for its 30-second lease to expire and activate the destination. On wake, the old VMM/guard resumes before `lampd` finishes reconciliation. A normal Go monotonic deadline may still have time remaining because that clock stopped during sleep. Existing sockets or disk requests can act under the old location/writer authority. A clock rollback after reboot creates a second version of the problem.

**Evidence:** The spec requires sleep invalidation and conservative expiry but identifies no suspend-aware time source, boot identity, skew bound or ordering mechanism for already-running workers. The [Go time contract](https://pkg.go.dev/time#hdr-Monotonic_Clocks) warns that monotonic clocks may stop during sleep and lose their monotonic component on serialization. Linux explicitly distinguishes suspend-excluding `CLOCK_MONOTONIC` from [suspend-including `CLOCK_BOOTTIME`](https://docs.kernel.org/core-api/timekeeping.html). A daemon-level rejoin rule alone cannot gate an independently resumed data path.

**Fix:** Specify per-OS suspend-aware clocks, boot IDs, conservative cross-host expiry/skew rules and restart invalidation. Guard/supervisor/storage enforcers must reject expired authority independently of `lampd`; wake cannot release task I/O/vCPUs before revalidation. A destination must wait the uncertainty margin or obtain a physical fence. Test abrupt sleep and wall-clock jumps with old live flows and queued disk I/O, not only new admission.

### X-010 — high — Logical record reservations do not bound SQLite's physical WAL growth

**Sections:** §3.1, §5.2, §12.3–12.4, §17.1, §18, E13/E14.

**Failure:** A teammate keeps overlapping audit/export/replay readers open while ordinary control writes continue. If a database read transaction survives into a slow response, checkpoints cannot recycle the WAL. Physical disk usage grows despite bounded retained records and stream queues. An unrelated host application can also consume the nominal free-space reserve. `SQLITE_FULL` then prevents recording cleanup, stop outcomes or permit reconciliation, precisely when operations need durable terminal states.

**Evidence:** SQLite documents [checkpoint starvation and unbounded WAL growth](https://www.sqlite.org/wal.html#avoiding_excessively_large_wal_files) with overlapping readers. [`SQLITE_FULL`](https://www.sqlite.org/rescode.html#full) can affect both the database and temporary storage. The spec reserves logical records/bytes and promises full-disk tests, but gives no read-transaction lifetime, WAL/temp-file allowance, physically protected emergency capacity or no-space recovery order. This is a storage-amplification case beyond R-028's logical quota repair; it does not imply SQLite corrupts previously committed transactions.

**Fix:** End database reads before network streaming; bound/cancel reader transactions and create checkpoint opportunities. Account for WAL, temporary files and migration/compaction amplification. Keep preallocated emergency journal capacity unavailable to task data, and stop admission before it is needed. Specify no-space recovery that first fences effects, then records/reconciles outcomes without deleting safety history. Test overlapping readers plus byte/inode exhaustion while fork/suspend/revoke is mid-commit.

### X-011 — high — Encrypted replicas have no specified key-recovery contract

**Sections:** §6.3, §7.1, §9.1, §15.2, E14.

**Failure:** A private snapshot receives two durable receipts, or an encrypted backup completes. The original host and its OS key store are then lost. The surviving host has every checksummed object but cannot unwrap the data key. Reinstalling Lamp and recovering fleet authority does not recover encrypted work. The replicated snapshot's single-host-loss RPO of zero, and the backup's completed recovery point, are therefore incomplete promises.

**Evidence:** The architecture specifies encryption and an OS-backed/encrypted owner store, but no content-key envelopes, independent recovery material, replica decryptability receipt or clean-OS restore procedure. The fleet-root recovery key in §15.2 is not specified as data-key recovery. OS protection is not automatically portable: Microsoft's [DPAPI contract](https://learn.microsoft.com/en-us/windows/win32/api/dpapi/nf-dpapi-cryptprotectdata) normally binds decryption to the original logon/computer, with documented exceptions. This illustrates the missing contract; it does not assume Lamp has selected DPAPI for all content.

**Fix:** Define owner-scoped content-key generation, wrapping for each authorized custody host, offline recovery material, rotation and deletion. A replica counts as recoverable only when its authorized key path is present and tested. Run a restore on a clean replacement OS with the original host/store absent, checking bytes, audit and retry safety. Preserve A1: credential-bearing recovery remains on the same person's explicitly trusted hosts, and data recovery must not transfer credentials to a teammate.

### X-012 — high — The macOS durability boundary stops at a weaker flush than the promise requires

**Sections:** §6.4–6.5, §7/§7.1, §12.3, §14.1, E04/E14.

**Failure:** A Mac acknowledges a local suspend and releases the only live RAM. Ordinary fsync has returned for the object and metadata, but the storage device has not durably ordered them. Power loss leaves a committed manifest with missing state, or loses a local operation receipt/permit-consumption record after its effect. A process-kill or orderly reboot test can pass while this failure remains untested.

**Evidence:** §7 uses fsync as the commit primitive without a per-OS stable-storage definition or SQLite sync configuration. Apple's [fsync contract](https://developer.apple.com/library/archive/documentation/System/Conceptual/ManPages_iPhoneOS/man2/fsync.2.html), also present in the [current XNU manual source](https://github.com/apple-oss-distributions/xnu/blob/main/bsd/man/man2/fsync.2), warns that fsync alone can leave reordered/lost writes on power loss and identifies `F_FULLFSYNC` for the stronger request. SQLite's [`fullfsync`](https://www.sqlite.org/pragma.html#pragma_fullfsync) is off by default; its [WAL synchronous modes](https://www.sqlite.org/pragma.html#pragma_synchronous) also distinguish consistency from power-loss durability. Filesystem checksums do not create missing acknowledged bytes.

**Fix:** Define the stable-storage barrier for every supported OS/filesystem/device and reject durable qualification when it is unavailable. On Mac, qualify `F_FULLFSYNC` and the required SQLite/VFS settings, including journal/object/directory ordering; use corresponding proven barriers elsewhere. Include these costs in timing. Fault-test loss of volatile device writes/reordering and abrupt power loss on disposable fixtures, checking state and safety journals against an external acknowledgement ledger.

### X-013 — medium — B1 is recorded but the executable benchmark contract still uses the rejected envelope

**Sections:** §6.4, §19, E01, §22.1, §23 B1.

**Failure:** An implementer follows §19 and tunes full capture for 1 GiB, while CI gates every admitted 8 GiB/full-dirty/durable row at one second. The accepted 4 GiB/20 GiB standard is absent from E01's enumerated rows. The product can pass the old reference while missing B1, or stay blocked on a larger row the owner explicitly made measurement-only.

**Evidence:** Requirements “Reading and test rules” and “Benchmark suite” explicitly replace the older envelope: 2 vCPU, 4 GiB RAM, 20 GiB disk, up to 1 GiB recently written RAM; larger/full-dirty cases remain measured. §19 instead specifies 1 GiB/8 GiB and denies every admitted-size waiver. Recently written memory is not the whole capture: [Firecracker's full-snapshot contract](https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md) still requires the full memory snapshot. Writing 4 GiB in the allocated 400 ms requires 10 GiB/s before sync, disk sealing, restore or replication; the 1 GiB calculation hides that obligation.

**Fix:** Propagate B1 through §6.4, §19, E01 and the risk register. Retain separate correctness gates for all sizes and hard speed gates for the accepted standard only. Rebudget full 4 GiB capture and each actual durability mode; measure Linux storage bandwidth immediately. For AIR, publish measured latency, sustained thermals and capacity limits instead of inheriting a Linux-host timing allocation. Do not turn the dirty-set allowance into an unsupported incremental-snapshot claim.

### X-014 — medium — Executable transitions miss B4's processes created without exec

**Sections:** §11.2 audit details, §17.1, E13, §22.1, §23 B4.

**Attack:** A hostile process forks a child that runs inherited program code without calling exec. The collector emits no `process_exec` event for that child. Another process supplies a deceptive argv[0]; the schema lacks an actual program field and effective user. A ledger of executable transitions can therefore pass its own fixture while violating the accepted process-start contract, even without tampering with the collector.

**Evidence:** B4 and requirements OPS-004 require program, arguments, user, parent and time for every process started, including fork without exec. §17.1 requires executable transitions, and §11.2 lacks effective user and program path/native identity. The Linux [fork contract](https://man7.org/linux/man-pages/man2/fork.2.html) creates a process by duplicating its parent; exec is a separate operation. Requirements already propose a start event and additional fields but explicitly leave their canonical adoption to the architecture. The remaining semantic-builtin blocker also exceeds B4.

**Fix:** Adopt a canonical process-start event and fields for actual executable identity, effective user, parent, start time and task/runtime attribution. Observe fork/clone/process creation as well as exec on every guest class; specify process versus thread counting. Preserve mediated command envelopes and exec-change events without requiring script semantics. Test a child that never execs, misleading argv[0], rapid PID reuse, user changes and nested Docker against an independent observer.

### X-015 — medium — Stale B2/B3 blockers would reject workflows the owner has accepted

**Sections:** §9.3, §16.2, E08, §22.1, §23 B2–B3.

**Failure:** A qualified public-PR runner uses only its forge's one-job, read-only token, yet §16.2 says no owner amendment permits it and release must remain blocked. A password workflow safely falls back to private human entry, yet the browser text still treats required general workflows without automatic safe adapters as an unresolved owner promise. Implementers and test authors receive contradictory release instructions.

**Evidence:** B2 expressly accepts the CI-owned one-job token class; B3 expressly accepts standard-form fill plus successful private takeover when secrecy is unproved. Requirements CI-005 and KIT-001/003/005 carry those decisions into acceptance tests. The architecture's introductory acceptance statement and §23 coexist with the old explicit “no owner amendment” and browser-generality blockers. These are already decided policy questions, not requests to weaken the boundaries or reopen rounds 2–3.

**Fix:** Replace the stale clauses with the exact accepted token audience/actions/resource/expiry limits and B3's useful fallback path. Update risk and experiment rows together. Keep host/model/vault/Lamp authority outside public PRs, and keep unsafe browser pages private until a safe return page exists. Add consistency checks that each owner decision maps to the operative acceptance text.

### X-016 — medium — A normally expired Task has no defined path back to its persistent Session

**Sections:** §6.5, §11.1–11.2, §12.1–12.2, §17.2.

**Failure:** A person ends a turn, the sandbox suspends after five minutes, and they return the next morning. Its seven-day saved state still exists, but the one-hour Task deadline expired during suspension. Message/SSH/port access promises an automatic resume; inherited task authority promises no effects after expiry. The schemas permit Task/Budget updates but never define whether a stopped Task can reactivate, how a preserved Session binds to newly authorized capacity, or what the ordinary resume action returns in this case.

**Evidence:** §17.2 explicitly counts suspension against the default 3600000 ms deadline; §6.5 retains state for 604800 s and coalesces automatic wake. `sandbox_resume` has no new Task/budget input, while Session/Sandbox each retain `task_id`. Requirements LIF-002 and OPS-002 preserve both promises. This is a missing authorized continuation transition, not a request to reset budgets automatically or extend the old grant.

**Fix:** Define an explicit caller-authorized continuation flow: show expired authority, obtain a new Task/budget or authorized extension, atomically bind the preserved sandbox/session generation, issue new handles and then resume. Keep prior usage/outcomes attributable and prevent concurrent old/new authority. Specify the exact error and dashboard/SSH action for an expired automatic wake. Test overnight return, an expired approval and two concurrent continuation attempts.

## Attack and failure coverage without additional findings

These are design controls to preserve and test, not certifications from this review. Repeating an already accepted implementation gate would add no new finding.

| Attack or failure | Review result / remaining proof |
| --- | --- |
| Guest root, nested Docker, device escape | §§3.2/8 require external VMM confinement, private handles and one guarded network path. Guest containers are not the security boundary. Actual OS profiles/device fuzzing remain E05/E09 gates; X-002 adds a different, host-facing snapshot-input path. |
| DNS, IPv6, QUIC/ECH, VPN loss | §8 explicitly normalizes/classifies addresses, separates transport from strict-sites mode, denies inspection bypasses and drops on exit failure. Preserve direct-IP, rebinding, fragmented/mapped-address and live-flow tests. New exit/browser holes are X-005/X-008; open public transport is accepted O3. |
| Neighbor side channels | No implicit sandbox links or shared writable roots; engine confinement must deny neighbor handles. Hardware side channels are expressly residual. Disable cross-trust memory deduplication if introduced: Linux [KSM](https://docs.kernel.org/admin-guide/mm/ksm.html) is an opt-in sharing mechanism, not proof these engines currently enable it. Test shared guard/cache resource exhaustion rather than claiming a VM removes all timing channels. |
| API-key proxy and MCP gateway | §9.1 has channel-bound sentinels, separate owner pools, typed action/response schemas, redirect/smuggling checks and no default capability export. Test malicious upstream schemas/results and returned URLs as data, never fresh authority. Arbitrarily malicious approved credential recipients are already TCB; byte scanning cannot prove transformed-secret secrecy. |
| Password fill and browser cookies | Private takeover, suppressed observations and safe return pages remain required under B3. Do not reopen the universal-redactor dispute. X-005 concerns network authority even when password handling is otherwise sound. |
| `confined` plugin | Hostile RPC/output must spend only core-checked typed grants. §13 already bounds frames, queues, handles and subprocess authority; native confinement must qualify separately from WASI. No new ambient-authority path established here. |
| `credential` plugin | It can see explicitly granted vault material; therefore that recipient is trusted for that material under §18. It must not receive guest RAM, unrelated secrets or task-visible secret-return authority. Preserve canary/exfiltration and malicious-response tests; do not describe granted-secret access as cryptographically hidden from its source worker. |
| `platform` plugin | Built-in audited adapters receive narrow endpoint/resource handles; final guard/supervisor policy must remain independent of their answers. X-008 tests a concrete downstream resolution mismatch. Arbitrary endpoint transforms are not permission to bypass the guard. |
| `engine_trusted` plugin | A3 accepts assigned-RAM access only after exact-digest trust; neighbor RAM and broker keys remain forbidden. Reopening that accepted exception is unnecessary. X-002/X-004/X-006 address state inputs, provenance and revocation around this class. |
| Stolen keys / GitHub OIDC | Scoped expiring keys, descendant revocation, exact challenge/audience and immutable forge evidence are specified. GitHub exposes token minting to steps/custom actions in an authorized job: [OIDC reference](https://docs.github.com/en/actions/reference/security/oidc). Test fresh JWTs as well as byte replay, cancellation, aggregate per-job limits, and caller versus [reusable-workflow claims](https://docs.github.com/en/actions/how-tos/secure-your-work/security-harden-deployments/oidc-with-reusable-workflows). A trusted step name is not an independent identity boundary. |
| Public PR / malicious teammate | Fresh roots, immutable untrusted policy, separate caches, role-filtered content and explicit custody hosts cover the settled attacks. B2 permits only its stated CI job token, not a Lamp/model/vault key. Application admin status does not grant content; a host administrator is expressly TCB. X-003/X-010 cover shared-budget and storage pressure. |
| Relay, Clerk, cookies and origins | Clerk outage cannot waive relay authentication; independent local/owner routes remain available. Clerk/account-code compromise exceeds the relay-process claim and cannot replace independent fleet-root pairing. Host-only cookies, exact Origin/CSRF checks and unique content origins address same-site siblings; browser tests remain mandatory. X-001 defeats the separate issuance claim without compromising Clerk or a CA. |
| Power loss during fork/suspend | The generation journal and immutable ancestry define rollback before publication and retained state after commit; volatile loss is disclosed. X-010/X-012 expose missing physical persistence/resource details. Test parent continuation after failed capture as well as child recovery; orderly restart alone is insufficient. |
| Two-/three-host partitions, sleeping handoff | Disjoint permits and quorum-only global grants are a coherent bounded-availability contract. Do not reargue the settled third-voter question. X-003/X-009 identify task-budget and expiry enforcement that must fit that model. |
| Partial rolling update / corrupted template | X-004/X-006/X-007 cover trust, revocation and schema rollback. Hash failure must leave a failed, quarantined object and the last usable state intact; validly hashed unauthorized VMM state is X-002. Never fall back to an unverified template or call a cold boot a resume. |
| Install, uninstall, quotas, logs and debugging | §§3.1/12/17/18 specify preflight, inactive unused services, explicit retain/export/delete, typed errors, operation phases, quota admission and redacted OTel. New restore/space/overnight gaps are X-010/X-011/X-016. A concrete installer diagnostic should identify the failed hypervisor/profile/exit probe and safe repair, rather than only reporting `ENGINE_UNQUALIFIED`. |

## Feasibility and build order

The first Linux implementation has a coherent bounded path: one pinned engine, private file-backed roots, the capture journal, guard and API-proxy reference; then fault injection before extra adapters. This agrees with M0/M1. Nothing in this review supplies measured create/fork/native-speed results or proves the Mac/Windows/device gates. X-013 is the concrete allocation correction; A2's native-Mac disk-only fallback stays accepted.

AIR is a passively cooled laptop: use its actual SoC/RAM/OS, battery/mains state, sustained loaded soak, sleep/wake and memory pressure, not only a cold burst. Charge parent/child growth and immutable backing before admission. The existing thermal/capacity tests are appropriate; a broad claim that every Air meets the standard would not be. No hard Mac timing target has yet been accepted.

One developer with agents can build M0; the document gives no evidence-based estimate for the entire first release. The qualification workload is itself substantial: every guest/OS boundary, 11 harnesses, 12 CI-family rows, four vaults, consoles, devices, auth modes and version changes. Agents do not replace vendor eligibility or physical hardware. Before M1 expansion, inventory exact required manifests, dependency/advisory ownership, automated fixture cost and unavailable hardware. Keep the accepted scope, but make this maintenance workload visible; do not treat a generated adapter as a supported adapter. This is delivery risk, not a new unresolved owner decision.

## Competitors: verified differences and useful practices

No additional finding is warranted merely because another project implements a similar feature. §24 generally distinguishes design obligations from measured superiority. The following primary sources still constrain any eventual comparison:

| Competitor / source | What Lamp should copy or avoid claiming |
| --- | --- |
| [Cloudroom security](https://www.cloudroom.dev/security) | Copy its direct disclosure that agents can read assigned in-sandbox keys, shared sub-agents share files, and host access grants host authority. Its externally stored session history is a useful recovery pattern to compare with Lamp's separately quota-bound output. Lamp's protected API-key path is a real proposed distinction; ordinary guest-key storage is not equivalent. |
| [E2B Embed announcement](https://e2b.dev/resources/introducing-e2b-embed) | It already offers self-hosted Firecracker, local database/templates/logs, Python/JS SDKs and a dashboard. Self-hosting alone is not Lamp's difference. Copy the install-readiness sequence, explicit SDK endpoints and simple OTel setup. Do not copy its Docker runtime into Lamp; ADR 0007 forbids that path. Its listed gaps include secrets, volumes, workload identity and BYO proxy. |
| Docker [isolation](https://docs.docker.com/ai/sandboxes/security/isolation/), [credentials](https://docs.docker.com/ai/sandboxes/configuration/credentials/), [upstream proxy](https://docs.docker.com/ai/sandboxes/configuration/upstream-proxy/) | MicroVM isolation and credential substitution already exist. Compare equivalent mount/network/proxy configurations, not a bare-container baseline. Copy explicit credential-use bindings and proxy-resolution diagnostics. Its documented plaintext-file fallback for a headless secret store must not become Lamp's fallback. X-008 needs a tested dial-address contract regardless of competitor behavior. |
| [microsandbox v0.7.14 snapshots](https://github.com/superradcompany/microsandbox/blob/v0.7.14/docs/sandboxes/snapshots.mdx) | It documents disk/full snapshots, independent restore and local capture timeouts up to 30 minutes, without automatic retry after uncertain timeout. Copy that explicit outcome handling; a timeout is not permission to recapture blindly. Feature existence or shared msb_krun lineage does not establish Lamp's subsecond performance, confinement or independent defect diversity. |
| [SmolVM v1.34 limitations](https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/limitations.md) | Copy precise startup/entitlement, mount, network-backend and capacity diagnostics. Its Windows branch requires source freezing and has no GPU support; keep those differences beside any benchmark. A shared krun ancestry or fast unrelated startup cannot qualify Lamp's continuing-source/device rows. |

## Priority for the response round

Address X-001, X-002, X-009, X-012, X-004, X-006, X-005, X-008, X-003 and X-007 first, in that order. Then close X-010/X-011's full-disk and decryptable-recovery contracts. X-013–X-016 are bounded specification corrections that should land before implementers freeze schemas, fixtures or continuation behavior.
