# Round 4: requirements red team

Reviewed 2026-11-08. **Changes required: 29 findings — 0 critical, 10 high,
17 medium, 2 low.** Only this file was edited; no commit, push, rentals or installers.

Scope: all 105 requirements, their architecture contracts, every brief decision,
the glossary and ADRs, and the round-2 findings and round-3 answers. Findings are
ranked by severity and cite both sides of each contract mismatch. Accepted
owner decisions are binding; a difficult decision is not itself a defect.

## Evidence and limits

- Read all 105 records, architecture §§1–25, the entire brief/glossary/ADRs,
  R-001–R-043 and their round-3 responses. Existing owner decisions stay accepted.
- Scripted heading/ID extraction found 105 unique IDs and matching family counts.
  Parsed §11/§12's 53 resource rows and 315 standard/extra operation names;
  requirements directly spell 126 and reference the rest through lifecycle grammar.
- Checked concrete inline/fenced JSON payloads, including API bodies and CLI JSON
  arguments: no syntax failure found. Ellipses and explicit substitutions are
  schematic, not malformed concrete examples. Semantic errors remain in Y-026.
- Compared CLI/API/MCP names, paths, fields, events, errors and numbers. No extra
  unacknowledged operation/tool/error/event name was found; the three audit
  proposals are explicit. Duplicate REST identity remains Y-011. Source locations
  below use line numbers in the reviewed files, not implementation test results.
- No runnable product/OpenAPI implementation was tested. Hardware and vendor
  availability were assessed from the declared lanes, not newly rented machines.
  Existing untracked `GLOSSARY.md` and `docs/` were left in place.

## Findings

### Y-001 — high — Accepted B decisions still lose to old architecture gates

**Locations:** requirements.md:41, 1181–1208, 1789–1795; architecture.md:249,
1029–1030, 1145, 1157, 1164, 1169–1170, 1203–1206; brief.md:64–71,
194, 202. **Problem:** B1–B4 are accepted, but the architecture still gates all
admitted sizes, literal secretless CI and hostile semantic-command coverage.
The requirements override selected sections only, leaving E01/E13 and the risk
register contradictory. A release can pass one binding document and fail the other.
**Fix:** Apply B1–B4 to every affected architecture gate and canonical schema;
remove the old blockers and pending questions. Say: “B1–B4 are accepted. Only
implementation evidence and the rental spending cap remain outstanding.”

### Y-002 — high — Process-complete audit requires names the API rejects

**Locations:** requirements.md:1113–1118, 1815–1821; architecture.md:587,
610, 615, 937–948. **Problem:** OPS-004 requires `process_start`, `executable`
and `user`, but labels all three proposed. The canonical audit dictionary still
has only executable transitions and no actual program/user fields. Strict schema
validation can reject the very records needed to pass B4. This is a remaining
schema repair after R-013, not a request to reopen the owner's audit decision.
**Fix:** Make these three names canonical, define their types and redaction,
and add independent start/exec/user/parent assertions to exported-record tests.

### Y-004 — high — An approver cannot inspect an action from its hash

**Locations:** requirements.md:94–108, 966–971; architecture.md:446–447,
553, 646. **Problem:** Approval stores an action digest and destination, but no
normalized action, arguments, secret/form identity or immutable action-plan
reference. A hash cannot show a person the SQL, vault item or settings diff they
are approving. A correct digest comparison can pass while the card displays a
different caller-supplied description. R-016's inspect step is still incomplete.
**Fix:** Store a bounded, redacted canonical action plan, computed/validated by
the enforcing adapter, and bind its digest to the Approval. Display that plan;
test a misleading description with a valid digest for a different real effect.

### Y-006 — high — Keyless CI has no challenge-bootstrap exchange

**Locations:** requirements.md:1034–1041, 1358, 1416;
architecture.md:569, 655, 847–850. **Problem:** CI starts by exchanging an
existing `af_01`. AuthFlow creation requires the ordinary authenticated mutation
contract, but this caller has no Lamp key yet. No unauthenticated challenge
request or trusted delivery of that flow ID/challenge is defined. A test with a
precreated authenticated fixture bypasses the feature's central boundary.
**Fix:** Specify the bounded challenge bootstrap and its authentication/rate
limits, or the precise trusted-controller delivery protocol. Prove the whole
flow in a clean GitHub job with no preinstalled Lamp credentials or manual flow ID.

### Y-007 — high — Teleport's native-host execution authority is unspecified

**Locations:** requirements.md:854–858, 955, 1646–1649;
architecture.md:499–502, 606, 659–660. **Problem:** A native-host destination is
called explicit host authority, but the only named permission is
`session.teleport`; no grant distinguishes moving between sandboxes from running
the harness directly on a compute host. A key allowed sandbox moves must not
therefore acquire the host user's filesystem/process authority.
**Fix:** Define a separate explicit native-execution grant, destination owner,
working paths and confirmation boundary. Test a sandbox-only teleport key
against `location.kind=host`: deny before any host process or file effect.

### Y-008 — high — A raw-secret memory fork has no renewed-consent contract

**Locations:** requirements.md:383–390, 762–769, 846, 1404–1408;
architecture.md:225–235, 449–450, 490. **Problem:** Memory fork copies readable
injected passwords/cookies, while Exposure consent must not inherit. A1 permits
private subscription capture; it does not grant a new child's raw-secret
Exposure. Neither contract says how a captured child obtains fresh consent, or
whether such a fork is rejected before secret bytes are copied.
**Fix:** Require a new child-bound Exposure approval before capture/publication,
or fail the fork before capture. Preserve subscription A1 separately. Test an
exposed parent with both absent and explicit child consent, inspecting actual RAM.

### Y-009 — high — The fresh-VM rule drops trusted public-repository CI

**Locations:** brief.md:59, 193–194; requirements.md:372–378, 1054–1062;
architecture.md:177, 923–925. **Problem:** The brief says public-repository CI
jobs always require a fresh VM. The contracts and denial tests enforce this only
for public-PR/stranger work. A trusted branch job in a public repository can use
the weaker fallback, including an explicitly exposed secret. B2 changes allowed
job tokens; it does not narrow which public jobs require VMs.
**Fix:** Enforce fresh-VM admission for every verified public-repository CI job.
Keep stranger-only secret/network restrictions separate. Test a trusted public
branch job against a nonvirtualizing host with an owner Exposure: still deny.

### Y-010 — high — Revocation cannot report the offline authority it promises

**Locations:** requirements.md:975–982; architecture.md:609, 790–798,
970–981. **Problem:** ACC-009 promises outstanding hosts and remaining offline
expiry. Its example only counts acknowledgments; Operation has no per-enforcer
status, expiry or residual-capability field. The internal AllocationPermit is not
a public resource. A UI can show “1 of 2” without letting the owner know which
host still acts, for how long, or which external capability remains usable.
**Fix:** Canonicalize a redacted revocation-status object listing enforcer,
acknowledgment, authority expiry and residual provider capability. Test real
partitioned-host reads before/after expiry, not only a waiting badge.

### Y-011 — high — Two named event operations occupy the same REST route

**Locations:** requirements.md:164, 205, 1618, 1639;
architecture.md:573, 621–624, 657, 659. **Problem:** Standard `event_list` maps
to GET `/v1/events`; explicit `event_read` maps to that same method/path with
cursor/limit/wait arguments. They require different operation IDs and scopes.
OpenAPI cannot assign both operation IDs to one method/path, and API/MCP parity
or authorization can disagree. SSE is a third, separately specified transport.
**Fix:** Choose one canonical cursor-read operation/scope for GET `/events`, or
give the extra action a distinct route. Generate parity tests from that single
mapping, including callers granted only the intended event permission.

### Y-012 — high — The required compatibility denominator still has no fixed rows

**Locations:** requirements.md:653, 1304–1311, 1581–1594;
architecture.md:139–148, 1095–1102; brief.md:41–60, 121, 144–156.
**Problem:** The replacement for R-035 lists axes and “every advertised” row,
not a finite required host/edition/guest/device/auth/harness matrix. Removing an
advertised auth/device row shrinks the denominator; interpreting every axis as
a Cartesian product instead demands unsupported native combinations. Windows
edition probes are especially easy to confuse with promised native-guest support.
**Fix:** Freeze a versioned list of required positive rows and explicit exclusions
before implementation. Keep it independent of detected/advertised capabilities.
A gate test must fail when a required row is removed, not recompute a smaller 100%.

### Y-003 — medium — Response and download examples do not define one wire contract

**Locations:** requirements.md:45–59, 227–228, 523, 832–833, 1353–1356;
architecture.md:636, 649, 667–675, 691. **Problem:** SES-003's attach response
omits its required Operation identity. File-read is a top-level payload, while
the common rule describes wrapped reads without naming this exception. Downloads
promise bytes/manifest through CLI `--json`, but specify neither a JSON/base64
shape nor an output-file flag. Binary bytes would violate the JSON stdout rule.
**Fix:** Show the complete attach envelope. Explicitly define file-read's read
exception and MCP download chunks, including EOF/hash. Define CLI downloads to
write a chosen local file and emit JSON metadata, or emit specified base64 chunks.

### Y-005 — medium — Exposure's active state does not distinguish authorization from use

**Locations:** requirements.md:764–767, 797–800, 1624;
architecture.md:554, 647, 449. **Problem:** The flow creates an Exposure and then
activates it using Approval, but the enum has no pending/unused state or activation
marker. An `active` record could mean permission exists or that cookies/passwords
were actually delivered. Tests do not inspect creation-to-activation behavior.
**Fix:** Define both meanings: use an initial `pending_approval` state, or retain
grant state and add explicit effect/activation status. Test create→get→denied,
expired and successful activation, checking the actual credential location.

### Y-013 — medium — Restart tests can miss a missing durability barrier

**Locations:** requirements.md:436–438, 548–550, 1298–1300, 1505;
architecture.md:271, 283–285, 306–308, 1107–1108. **Problem:** Process kills and
orderly host/storage restarts can preserve or flush the OS cache even when a
required fsync is absent or ordered incorrectly. An independent application
fsync ledger alone does not establish when backing storage became durable.
Those tests can pass while an acknowledged capture disappears on abrupt failure.
**Fix:** Require an abrupt storage/host-loss fixture that loses unflushed state,
and a real filesystem/block integration test for commit ordering. Deliberately
remove/reorder a required flush and prove the recovery oracle catches the defect.

### Y-014 — medium — The default hour-long budget conflicts with later automatic resume

**Locations:** requirements.md:431–437, 458, 1091–1097;
architecture.md:257–265, 957, 962–965; brief.md:82–85, 200.
**Problem:** Default suspended retention is seven days, but the default hard
task deadline is one hour and includes suspension. An overnight next message
cannot both resume the same task automatically and preserve its expired budget.
The visible journey does not explain this normal default case.
**Fix:** Say: “Before the task deadline, a message resumes the same task. After
it, the task stays stopped until an authorized person extends its budget.” Show
both countdowns and the budget-extension operation; test a message after 24 hours.

### Y-015 — medium — CI deletion changes from an overridable default into an absolute rule

**Locations:** brief.md:82–85; requirements.md:453–460, 464–470;
architecture.md:577, 319–322. **Problem:** The brief permits per-sandbox lifecycle
overrides, including after describing CI deletion as a default. LIF-004 requires
deletion after every terminal job, and the lifecycle schema has no job-end policy.
The advertised override cannot express retaining a trusted failed job for inspection.
**Fix:** Specify the allowed trusted-job override, for example
`on_job_end=delete|retain_stopped`, default delete. Always revoke job authority,
deregister runners and use a fresh next-job sandbox. Keep stranger safety ceilings.

### Y-016 — medium — Proposed engineering targets become hard architecture gates

**Locations:** requirements.md:28, 140, 1309, 1524;
architecture.md:75–92, 1109–1110, 1158. **Problem:** Install budgets and the
2,995-trial/24-hour reliability objective are proposals in requirements. E14
makes the same allocations/trials a release-blocking threshold. Even architecture
§20 calls the statistical gate proposed. An honest tester cannot give one verdict.
**Fix:** Label each target consistently as binding or proposed in both documents
and E14. State that proposed failures are reported and cannot silently become a
release gate; adopting a target requires one dated baseline change.

### Y-017 — medium — Requirements weaken the architecture's merge gate

**Locations:** requirements.md:1225–1231, 1554;
architecture.md:1065. **Problem:** Architecture says no merge with missing
required hardware results. Requirements only prohibit advertising such a merge
as fully verified, allowing a visible block instead. O6 requires the runs; it
does not itself choose between these two different merge rules.
**Fix:** Match the architecture: “A required missing or failed result blocks merge;
a visible block is status, not a waiver.” If a different engineering merge policy
is intended, state it explicitly in both documents without claiming O6 chose it.

### Y-018 — medium — Architecture-only limits and observations lack matching contracts

**Locations:** requirements.md:444–446, 543–548, 1173–1174, 1298–1311;
architecture.md:278, 310–313, 537–538, 558–561, 788, 1022.
**Problem:** These details are absent from the corresponding requirement/schema:

| Architecture contract | Missing acceptance/wire detail |
| --- | --- |
| 32 concurrent mutations/principal; 64 preauth relay connections/source (§18) | OPS-009 says bounded, but tests neither numerical boundary. |
| Heartbeat every 2 s (§14.1); NTFS chain depth ≤2 (§7) | No matching default/depth assertion in HST-004 or capture/GC tests. |
| Desired and achieved replicas are separate fields (§7.1) | Canonical Snapshot has receipts, but no named desired/achieved fields. |
| `lost` / `restore_required` recovery outcomes (§7.1) | No recovery-state field; canonical snapshot/artifact states cannot express them. |
| Visible suspend eligibility/reason (LIF-002) | Sandbox/lifecycle/readiness schemas contain neither observation. |

**Fix:** Carry the limits into the acceptance table and boundary tests. Add named
recovery, replica and suspend-eligibility observations to the canonical schema,
then demonstrate reads in CLI/API/MCP and the dashboard. Do not invent state aliases.

### Y-019 — medium — Fixed expiries make the examples fail outside one instant

**Locations:** requirements.md:96, 591, 833, 876, 955, 966;
architecture.md:512, 644–646. **Problem:** Keys, approvals, links and streams use
`2026-11-08T20:00:00Z`. The approval fixture also demands expiration exactly
60 seconds after creation. Unless executed at the matching time, that example
either has the wrong TTL or is already expired. Only IDs/placeholders are
explicitly substituted by the shared rules.
**Fix:** Define a fixture `t0` and derive `expires_at=t0+ttl_seconds` from the
authoritative test clock. Mark historical timestamps illustrative. Test before,
at and after expiry with the same derived values and no wall-clock scheduling trick.

### Y-020 — medium — Suite-wide lane rules contradict individual attack assignments

**Locations:** requirements.md:1220, 1442–1455, 1489–1491, 1569–1573;
architecture.md:147–148, 1097–1103. **Problem:** BEN-004 says every matrix row
runs all 44 attacks, while individual rows restrict them. ATK-07 also includes
nested Docker on native and fallback rows: NV-Mac/NV-Windows cannot host the Linux
VM Docker needs. A literal all-positive-variants run is impossible on these lanes.
**Fix:** Say “Run each attack on its explicitly enumerated compatible rows.” Split
positive nested-Docker tests from unsupported/denial cases. Publish exact cases
per guest/boundary; no missing required positive row may become a denial-only pass.

### Y-021 — medium — Linux resource defaults accidentally become native-guest defaults

**Locations:** requirements.md:339–345, 352–356, 1389;
architecture.md:145–146, 166–169; brief.md:58. **Problem:** Architecture explicitly
defaults Linux to 2 vCPU/1 GiB/8 GiB. SBX-004 states those defaults without the
Linux qualifier, and native-Mac creation omits resources. No native-template
minimum/default rule says whether that request boots, fails or is silently enlarged.
**Fix:** Say “These are Linux defaults.” Publish each qualified native template's
minimum/default resources; show a valid native create and reject an explicit
undersized request before allocation. Never silently exceed the caller's limits.

### Y-022 — medium — Device control has no concrete input or watch example

**Locations:** requirements.md:225–231, 363–368, 1391, 1640;
architecture.md:576, 637, 712. **Problem:** Devices can be requested, but the
contract never defines `device_ref` or the tagged `input` variants, and supplies
no CLI/API/MCP input/watch exchange. Key codes, mouse coordinates, audio/USB
payloads and sequence acknowledgments cannot be independently tested. “Actual
input worked” can hide incompatible implementations behind request-inventory tests.
**Fix:** Canonicalize those shapes/units and returned stream descriptors. Give a
keyboard+mouse+display example with one exact observed effect, and audio/USB
fixtures with known payloads. Verify delivery and revoke/reconnect, not only `devices[]`.

### Y-023 — medium — The brief still lists telemetry that accepted O7 forbids

**Locations:** brief.md:213; requirements.md:1157–1164, 1757;
architecture.md:988–990, 1189. **Problem:** The brief's allowed usage-data
examples include OS and engine versions. Requirements and accepted O7 explicitly
forbid versions and permit fixed numeric counts/timings only. A payload containing
versions can conform to that brief sentence and fail the privacy test.
**Fix:** Preserve accepted O7 and remove versions from the brief's example list.
Use the exact same fixed payload schema and 90-day retention statement in all
three documents; add a rejected-version-field fixture.

### Y-024 — medium — Secret share URLs have no explicit lost-response rule

**Locations:** requirements.md:55, 591–596, 956–958;
architecture.md:370, 644, 669–672. **Problem:** `port_share_link` returns its
bearer in `result.url`. The secret-once inventory explicitly handles keys,
invitations and auth exchange, but not share URLs. Generic durable typed-result
storage/replay can therefore conflict with the ban on persisting link tokens.
The browser tests do not inspect Operation/event storage after a lost response.
**Fix:** Classify share URLs as transient secret results. Replay returns the same
Operation/grant identity and `secret_available=false`, never a new token or
persisted URL. Test lost responses and scan durable outcomes, events and audit.

### Y-025 — medium — Frame limits do not say whether base64 overhead counts

**Locations:** requirements.md:208, 230, 1173, 1353;
architecture.md:649, 712–715, 763. **Problem:** Frames are ≤64 KiB and chunks
≤1 MiB, but MCP carries base64 inside JSON. A 64 KiB binary payload becomes
87,384 base64 bytes before headers; a 1 MiB payload becomes 1,398,104 bytes.
Testing only decoded length can pass while the receiver rejects the wire frame.
**Fix:** Define decoded-payload and encoded-frame limits separately for every
transport. Budget framing overhead and split before encoding. Test exact maximum,
one byte over, and worst-case metadata, using an independent serialized-size check.

### Y-028 — medium — Owner transfer and root recovery have no callable procedure

**Locations:** requirements.md:1618–1639, 1646;
architecture.md:588, 629–657, 841, 860–862. **Problem:** Owner transfer is a
promised owner action and root recovery is a trust requirement, but neither has
a canonical operation or complete offline procedure. Updating read-only `owner_id`
cannot implement transfer; backup restoration's authority epoch is not root-key
recovery. Happy-path pairing tests never prove a lost-owner recovery.
**Fix:** Specify atomic transfer and fenced recovery, their proofs/keys and all
revocations. Add canonical operations, or explicitly define the necessary
offline exception and exact procedure. Test lost-owner/root recovery and rejection
of old owner, root, host certificates and mutation authority afterward.

### Y-029 — medium — Configuration references cannot yet be prepared through the interfaces

**Locations:** requirements.md:569, 580, 626, 889–904, 1319–1323, 1358;
architecture.md:547, 562, 568, 587, 600–601, 764. **Problem:** Examples assume
`cfg_vpn`, `cfg_member`, `cfg_tailnet`, `cfg_domain`, adapter IDs and plugin
digests already exist. No canonical configuration-reference type, publication
path, adapter-schema discovery call or plugin-package resolution rule prepares
them. A fixture with manually preloaded host files can pass while remote setup
through API/MCP is impossible.
**Fix:** Define how authenticated callers upload/validate/select nonsecret
configuration and discover immutable adapter schemas/packages. Keep secrets as
host-held CredentialSource references. Add empty-host setup through each interface;
the run manifest must not substitute for a missing product setup operation.

### Y-026 — low — Three observations describe a different request

**Locations:** requirements.md:330–331, 512–513, 683–685;
architecture.md:537–538, 544, 558, 612. **Problem:** Valid JSON still has these
semantic mismatches, which a copied E2E fixture would fail or observe incorrectly:

- SBX-002 pins `hs_win`, but the shown result is arm64 on `hs_mac`.
- STO-002 creates `shared_files`, but `vo_01` is shown as `exclusive_block`.
- CRED-004 creates a Snapshot, but observes an unrelated Share with no share step.

**Fix:** Show `host_id=hs_win,arch=x86_64`; show `mode=shared_files`; show the
returned Snapshot and add a separate Share-create/read sequence for the handoff.

### Y-027 — low — The owner still has to decode engineering shorthand

**Locations:** requirements.md:47, 282, 388, 548, 675, 686, 1129, 1208,
1265, 1309. **Problem:** The opening summary is recognizable, but journeys start
after roughly 1,300 lines. Dense clauses about permits, PRNG/TLS, taint, RPO and
confidence bounds hide everyday behavior. This is the remaining readability gap
after R-043, rather than a request to remove technical precision.
**Fix:** Put a short journey/defaults view immediately after the summary. Use the
ten concrete rewrites below for owner-facing prose; keep exact fields and test
rules alongside them in the engineering detail.

## Ten concrete owner-facing rewrites (Y-027)

These replace the dense explanation, with exact engineering details retained nearby.

1. **Common mutation context, requirements.md:47.** “Give each new action a retry
   key. If the connection drops, retry with the same key and unchanged inputs.
   Before editing an existing object, send the version you read; Lamp rejects an
   outdated edit. The CLI and SDK handle key discovery for you.”
2. **Two-host failover, :282.** “If either host sleeps, the other can start new
   work using capacity reserved for it beforehand. It cannot spend the sleeping
   host's capacity, recover that host's only files, or change fleet-wide permissions.”
3. **Fork safety, :388.** “The original and its fork keep running independently.
   Before the fork can use the network or credentials, Lamp gives it a new machine
   identity, refreshes its security state and checks today's permissions. Both
   copies must contain the same captured work, with no mixed or missing writes.”
4. **Durability, :548.** “Local saves survive a restart while that disk is intact.
   A save confirmed on two hosts survives losing either one without losing saved
   data. A backup restores only through its last completed save. Lamp shows that
   time and never replaces missing files with an empty sandbox.”
5. **Subscription work, :675.** “A subscription-signed-in harness runs in a
   private sandbox that can contact only its vendor. Downloads, Git and builds
   run in a second sandbox without the login. Lamp must prove that their tool
   connection cannot carry the login out, while still completing useful work.”
6. **Credential contamination, :686.** “After a sandbox can read a login or other
   exposed secret, assume its memory and files may contain hidden copies.
   Deleting the obvious token file does not make it safe to share. Rebuild shared
   tools from verified inputs and start a fresh harness.”
7. **Hosted addresses/cookies, :1129.** “Every Eilmer-hosted page stays under
   eilmer.si. An untrusted sandbox page must never obtain or replace an account
   login. Each fleet uses one wildcard certificate, so creating sandbox web
   addresses does not consume another certificate each time.”
8. **Native-speed evidence, :1208.** “Measure computation, disk and network
   separately against the same computer running directly. Every required result
   must reach 97% of that speed; a good average cannot hide one slow result.
   Repeat paired runs and publish uncertainty as well as the measured values.”
9. **Control-language boundary, :1265.** “Go decides who may act and which
   resources they may use. Rust runs and confines sandboxes. With 32 callers at
   once, test that their coordination stays within its 35 ms allocation at the
   99th percentile and never loses an action or mixes callers.”
10. **Reliability evidence, :1309.** “Report success and failure separately for
    create, fork, suspend, resume and delete on each required platform. The
    proposed confidence test needs at least 2,995 independent successes with no
    failures for each operation. A separate 24-hour busy run checks leaks and
    failures that develop over time; it does not replace those independent trials.”

## Brief coverage, bullet by bullet

Each row groups every bullet/paragraph in that brief section. “Named” means a
requirement exists, not that it is implemented. No whole feature family is absent.

| Brief section and bullets checked | Requirements / remaining defects |
| --- | --- |
| §1: self-hosted install; agents/harnesses/CI; isolated on demand; Eilmer; public superiority proof | PRD-001/002, SBX-001, BEN; setup Y-029, proof gates Y-016/017. |
| §2: everybody day one; parallelism, safety, setup, ownership; all competitor references | PRD-001/003, BEN-005; named. |
| §3: person/agent/GitHub/CI/service callers; API/MCP/CLI; TS/Python then Go/Rust; JSON/docs/retries/events | INT-001–005, CI-002/003; Y-003/006/011/029. |
| §4: caller chooses inside autonomous harness or outside commands/files/browser | INT-006/007; device/control detail Y-022, wire results Y-003. |
| §5: OS/arch/minima/editions; fleet of one/many; placement/pin; sleep/failover; opt-in rolling update; fastest engine | HST-001–006; finite rows Y-012, observations/defaults Y-018/021. |
| §6: microVM; requested devices/resources; Linux/Mac/Windows guests; Mac cap/license; local media; fallback/override; public-CI VM; memory fork/A2 | SBX-001–007; VM rule weakened Y-009, consent Y-008, defaults/devices Y-021/022. |
| §7: create/fork/native-speed/security values; standard B1; larger reports; Mac/Windows later; subscription/exposure exclusions | BEN-001–005; stale gates Y-001/016; accepted numeric thresholds retained. |
| §8: own format/no Docker runtime/images/files; installed tools; Docker kit; named recipes and all recipe components | TPL-001–003, KIT-004; configuration preparation Y-029. |
| §9: idle suspend/save/free/resume/delete; CI default; overrides; exact activity; warm pool; fork alternatives/winner | LIF-001–006; time/override conflict Y-014/015, eligibility Y-018. |
| §10: shared package/build cache; multiattach volumes; CI outputs/logs; saved tools; owner-only defaults | STO-001–005, TPL-002; persistence proof Y-013, observations Y-018, download Y-003. |
| §11: host-owned plugins/config; independent VPN/proxy/direct exits; private member; inbound URLs; site filters; allowed neighbors; three defaults | NET-001–006; Y-010/019/024/029. |
| §12: modular customization; anyone's separate-process plugins; engine trust exception; every named plugin category | PLG-001/002, ENG-002; package/config preparation Y-029. |
| §13: all 11 harnesses/others; approved subscription routes/custody/capture; API proxy; source choice and all settings categories; approved return | CRED-001–006; fixed compatibility Y-012, inspectable decisions Y-004, capture Y-008. |
| §14: no stateful-app migration; external MCP gateway/inside limited token; gh/stripe/aws; local services at all four access levels | CON-001–003; setup Y-029, exact decision display Y-004. |
| §15: external browser/hidden cookies; copied-login opt-in; 2FA/CAPTCHA takeover; four vaults; standard fill/human fallback; per-secret injection/per-vault phone approval; toolchain/Docker kits | KIT-001–005; activation Y-005, consent Y-008, approval Y-004. |
| §16: one-machine console plugin; every session watch/type/status; ordinary SSH; own/shared sub-agents/git; teleport; own-only and watch/type sharing | SES-001–007; host authority Y-007, results Y-003, revocation Y-010. |
| §17: Tailscale/keyless NAT, relay, own HTTPS, local, Cloudflare; opaque AGPL relay; Clerk/local/OIDC/offline; modes/roles; action/resource/expiry keys; GitHub exchange | ACC-001–009, RLY-001/002, CI-003; bootstrap Y-006, recovery Y-028. |
| §18: all ten CI plugins including Forgejo/Gitea; GitHub's three agent routes; extensibility; Slack/Linear/webhook/schedules; fresh jobs/caches; stranger no-secrets/B2 | CI-001–006, STO/LIF; scope Y-009/015, compatibility Y-012, test assignments Y-020. |
| §19: warm/fork/teleport; live full dashboard; time/tokens/watchdog; done/stuck/decision; process/flow/mediated audit; schedules; no cloud overflow | OPS-001–004/009, CI-006, PRD-003; Y-002/004/010/014/018. |
| §20: eilmer.si/cookies/certificates; no payments/future accounting; Apache/AGPL/private demo; opt-in counts/timings/90 days | OPS-005–008; stale version allowance Y-023. |
| §21: reproducible every-change/private-PR/public-release benchmarks; five competitors; speed/ready/density/reliability/attacks | BEN-001–005, QA; gate inconsistencies Y-001/016/017/020. |
| §22: engine first/no Docker; security/speed-only reuse; language choice; exact M5, Ubuntu, Air; capped hourly Windows/arm64 rentals; no owner-project tests/special integrations | ENG/QA; scope rows Y-012/020/021; no spend performed. |
| §23: all six research inputs subordinate to binding decisions | Named at requirements.md:1683; no separate product feature missing. |

All nine ADRs were checked: fleet, host network, reuse, custody, identity, domain,
template format, licenses and opaque relay are preserved. Defects above concern
their remaining contract details, not proposals to reverse them.

| Accepted decisions checked | Coverage / remaining issue |
| --- | --- |
| O1 SDK order; O2 capped rentals; O3 network defaults | INT-002, QA-001, NET/CI; Y-012/020 affect test scope, not approval status. |
| O4 private links; O5 deliberate exposures | NET-004, KIT/SBX/BEN; Y-005/008/019/024. |
| O6 private/public benchmark timing; O7 numeric telemetry | BEN-005, OPS-008; Y-017/023. |
| O8 licensed native media; O9 roles; O10 OS minimums | SBX/TPL, ACC/SES, HST/QA; Y-007/012/021/028. |
| A1 private login capture; A2 native-Mac fallback; A3 trusted engine exception | CRED/SBX/PLG preserve these; Y-008 distinguishes raw-secret consent from A1. |
| A4 opaque audit/best-effort tokens/hard time; A5 certified wait inactivity | OPS/LIF; Y-014/018. Neither decision permits arbitrary secret proxying. |
| B1 standard speed; B2 one-job CI token; B3 safe fill/private typing; B4 process-start audit | Accepted; Y-001/002 require propagation, not another owner vote. |

## Scope traps and lane feasibility

- **Demanded by the brief:** first-release host families, native Windows RAM fork,
  Linux-on-Mac RAM fork, all named CI/harness/vault integrations, requested device
  support, capped rentals and minimum OS evidence. Existing missing physical
  Windows/macOS-14/arm64/device fixtures remain real blockers; this review does
  not relabel them optional or repeat R-009/R-034 as new feasibility findings.
- **Over-strict or contradictory requirements:** immutable CI deletion (Y-015),
  every attack on every lane (Y-020), unqualified native resource defaults
  (Y-021), and proposed-versus-hard reliability gates (Y-016). These are repairable
  contract problems, not evidence that the owner approved feature cuts.
- **No finite release scope yet:** “every advertised” combinations (Y-012) can
  either erase required hardware or multiply into impossible combinations.
  Freeze required rows before reserving rentals; separate physical, nested,
  native-guest and denial-only cases. The missing spending cap is ordinary
  operational input, already acknowledged; it is not another finding.
