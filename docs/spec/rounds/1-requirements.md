# Lamp requirements — round 1

Status: requirements for architecture design. No architecture is selected here.
Audience: the owner, the three architects, implementers, console authors and test authors.
Purpose: define every requested first-release behavior and the evidence that proves it works.

## Reading rules and sources

[The brief](../brief.md) is binding. [The glossary](../../../GLOSSARY.md) supplies the product terms. The [ADRs](../../adr) record binding decisions. Research supplies evidence and cautions, not extra product commitments. A requirement with no applicable ADR says so. References to brief sections below always mean that binding file.

All requirements without `[proposed]` use **must**. A `[proposed]` requirement uses **should** and states its reason. Test sample sizes, test fixtures and fault injections are test designs, not invented product limits. Open questions are not resolved by sample inputs.

**Every CLI command, API route, JSON field, MCP tool, status envelope and error spelling in this document is illustrative.** The architecture sets final names, transport, schemas and codes. Examples show required outcomes, not existing Lamp functionality. Example output is expected output, not a test result. API examples use `METHOD /path request-JSON → response-JSON`. An omitted request body is `{}`. MCP examples show `tool(arguments) → result`. Every CLI example includes its JSON invocation; interactive streams may use structured events rather than one terminal JSON object.

“Visual” means the dashboard. “Consoles” means T3 Code, Herdr and plain SSH, unless a row is more specific. Administrative controls belong in the dashboard/API/CLI/MCP; consoles display their effect on sessions. A console does not need a second administration screen. The screens described here specify information and changes, not a layout or framework.

Names such as `sb_41`, `se_9`, `ubuntu-1`, `m5-1`, `air-1` and `win-test-1` are fixtures. `example.net` is an owner-controlled example domain, not an Eilmer-hosted address. Eilmer-hosted examples use only `eilmer.si` subdomains. Tokens shown as `<redacted>` are never real values.

### Research read before writing

- [Modal and Daytona](../../research/modal-daytona.md): boot headlines differ from command readiness; background work, runtime authorization and secret-bearing saved state need explicit tests.
- [VM backends](../../research/vm-backends.md): hardware/device support, memory-inclusive fork, identity, clocks and state portability vary. The OCI material is background and does not override ADR 0007.
- [Remote access and CI](../../research/remote-access-and-ci.md): NAT reachability differs from caller authorization. Runner registration, job authority and workload identity are different credentials.
- [CI and integrations](../../research/ci-and-integrations.md): the 52 scenarios are background, not 52 additional native connectors. The brief alone sets shipped plugins.
- [Credentials and stateful apps](../../research/credentials-and-stateful-apps.md): raw browser control can reveal cookies. A placeholder is not isolation unless real forwarding and response behavior are tested.
- [Subscription auth](../../research/subscription-auth.md): product support is not vendor permission. A central token store or subscription proxy is not an approved shortcut under ADR 0004.

### Real-host test matrix

| Test label | Required host type and use |
| --- | --- |
| LX | Linux x86_64 with KVM. Use the Ubuntu laptop if its architecture and KVM probe match. Otherwise obtain a separate qualifying test host; do not relabel it. |
| LA | Linux arm64 with KVM. None of the three named machines establishes this host coverage. Use an external arm64 host; a Linux guest on a Mac alone does not prove this row. |
| M5 | macOS on Apple Silicon: Apple M5 Mac, 10 cores, 24 GB RAM, macOS 26. Test Linux and macOS sandboxes and optional devices. |
| AIR | MacBook Air. Record its actual CPU, RAM and macOS version first. It supplies a second Mac and sleep/wake testing; Apple Silicon coverage requires Apple Silicon hardware. |
| WIN | A real Windows host with functioning hardware virtualization. No owner machine is available. See QA-001 for the proposed external Windows E2E lane. |
| NV | A host without virtualization. Use a Linux test host with KVM genuinely unavailable to Lamp. Test fallback labels, denials and override policy. |
| ALL | LX, LA, M5, qualifying AIR, WIN and NV as applicable. NV is never evidence for a real-VM-only requirement. |

Each E2E row names these host types by label. “Real” means released Lamp processes, actual guest execution, actual enforcement and actual public third-party software. Vendor test tenants and disposable fixture accounts keep personal state out of tests. A failed or missing host lane stays visible. This document runs no installs, vendor sign-ins or attacks.

All code-level test descriptions identify a boundary that can be faked for deterministic tests. Their “never mock” clause identifies behavior that integration/E2E proof still needs. A passing unit test with a fake VMM is never proof of isolation, speed, fork, suspension or OS support.

## Product and scope

### PRD-001 — Public-purpose product and four equal goals
**Statement.** Lamp must be an Eilmer open-source sandbox service for everybody from day one. It must address parallelism, safety, setup friction and ownership equally for agents, harnesses and CI.
**Source.** Brief §§1–2, 19–20; ADR 0008.
**What working looks like — CLI (illustrative).** `lamp about` → `Lamp · Eilmer · self-hosted`; `lamp about --json` → `{"project":"Lamp","umbrella":"Eilmer","self_hosted":true}`.
**API / MCP (illustrative).** `GET /about → {"project":"Lamp","interfaces":["api","cli","mcp"]}`; `lamp_info({}) → {"project":"Lamp","self_hosted":true}`.
**Visual and consoles.** First-run dashboard explains creating a sandbox on caller-owned hardware. T3 Code, Herdr and SSH show independent sessions rather than requiring an owner-specific application.
**Error (illustrative).** `UNAUTHORIZED: credentials are required to create a sandbox.` An unfamiliar caller is not rejected merely for not being the developer.
**Acceptance criteria.** A fresh third-party installation completes journeys 1 and 3 and the safety suite. Each of the four goals has passing evidence; no owner-project prerequisite appears.
**E2E test.** LX and M5: a fresh test account creates two concurrent Claude Code/Codex sessions on public fixture code, verifies independence, prepared tools and local execution.
**Code-level tests.** Contract-test installation/configuration without developer identifiers; fake vendor identity responses. Never mock actual separate session execution or the host boundary in product acceptance.

### PRD-002 — Self-hosted installation and control
**Statement.** Anyone must be able to install Lamp on their own machines as easily as Docker and obtain sandbox service without buying hosted compute.
**Source.** Brief §§1–2, 5, 17; ADRs 0001, 0005.
**What working looks like — CLI (illustrative).** `lamp install --user --access local` → `Fleet ready at local address`; `lamp install --user --access local --json` → `{"fleet":"fl_local","access":"local","ready":true}`.
**API / MCP (illustrative).** `GET /fleet → {"id":"fl_local","hosts":["ubuntu-1"]}`; `fleet_inspect({}) → {"hosts":["ubuntu-1"],"ready":true}`.
**Visual and consoles.** Onboarding shows supported host checks and the first sandbox action. T3 Code/Herdr link the resulting fleet; SSH reaches it with ordinary terminal tooling.
**Error (illustrative).** `HOST_PREREQUISITE_MISSING: hardware virtualization is unavailable; weaker isolation is available.` Installation effort has no numeric target in the brief; see Q01.
**Acceptance criteria.** A non-developer follows published steps on each supported host OS. Local operation has no mandatory relay or paid-compute dependency. Installation friction is recorded, not asserted from a script exit code.
**E2E test.** LX, LA, M5 and WIN: install on a clean supported OS, create a sandbox, run a command and reconnect. NV: installation exposes the fallback honestly.
**Code-level tests.** Test prerequisite detection and configuration parsing with fake OS probes. Never mock real package installation, virtualization availability or offline startup in installation acceptance.

### PRD-003 — Complete first-release scope; no cloud overflow
**Statement.** The first release must include everything in the brief, and must exclude automatic overflow to cloud providers.
**Source.** Brief §19 and §§1–23 collectively; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe linux-dev` → `No eligible host has room`; `lamp sandbox create --recipe linux-dev --json` → `{"error":{"code":"NO_CAPACITY","message":"No eligible host has room."}}` when the fleet is full.
**API / MCP (illustrative).** `POST /sandboxes {"recipe":"linux-dev"} → {"error":{"code":"NO_CAPACITY"}}`; `sandbox_create({"recipe":"linux-dev"}) → {"error":{"code":"NO_CAPACITY"}}`.
**Visual and consoles.** Dashboard shows capacity exhaustion and existing host usage. Consoles show the request as blocked/failed; no hidden paid cloud session appears.
**Error (illustrative).** `NO_CAPACITY: add room on an eligible host or choose another request.` Queue-versus-reject behavior remains Q03.
**Acceptance criteria.** Every binding requirement has first-release evidence. Exhaustion creates zero unrequested cloud resources. A cloud machine deliberately enrolled by an owner remains an ordinary host.
**E2E test.** LX + M5: fill eligible capacity and request another sandbox; inspect fleet events and external-account fixtures for absence of spillover allocation.
**Code-level tests.** Property-test placement exhaustion with fake host inventories. Never mock real resource accounting or external allocation observation in the no-overflow check.

## Callers and interfaces

### INT-001 — Credentialed callers and interface parity
**Statement.** People, agents, GitHub agents, CI jobs and outside services with valid authority must be able to create and control sandboxes through API, CLI and MCP with the same capabilities.
**Source.** Brief §§1, 3; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe linux-dev` → `sb_41 running`; `lamp sandbox create --recipe linux-dev --json` → `{"id":"sb_41","state":"running"}`.
**API / MCP (illustrative).** `POST /sandboxes {"recipe":"linux-dev"} → {"id":"sb_41","state":"running"}`; `sandbox_create({"recipe":"linux-dev"}) → {"id":"sb_41","state":"running"}`.
**Visual and consoles.** The same sandbox appears live in the dashboard. A session started through any interface appears identically in T3 Code, Herdr and SSH.
**Error (illustrative).** `FORBIDDEN: this key cannot create sandboxes.` Changing interfaces never expands authority.
**Acceptance criteria.** A contract inventory maps every product operation to all three interfaces. Identical authorized inputs produce equivalent effects, permissions and observable errors.
**E2E test.** LX, M5 and WIN: perform create, execute, fork, configure and delete through each interface using person, agent and CI fixture identities; compare resulting state.
**Code-level tests.** Contract-test generated operation coverage and authorization parity; fake VMM and vendor APIs. Never mock actual end-to-end transport or runtime effects for interface certification.

### INT-002 — Generated SDKs and delivery order
**Statement.** SDKs must come from one API definition, with TypeScript and Python first, followed by Go and Rust. First-release scope must preserve the brief's complete feature set.
**Source.** Brief §§3, 19; ADR 0008. Timing of Go/Rust relative to release is Q02.
**What working looks like — CLI (illustrative).** `lamp api describe` → `One definition; SDKs: TypeScript, Python, Go, Rust`; `lamp api describe --json` → `{"definition":"lamp-api","sdks":["typescript","python","go","rust"]}`.
**API / MCP (illustrative).** `GET /api-definition → {"operations":["sandbox_create","sandbox_exec"]}`; `api_describe({}) → {"operations":["sandbox_create","sandbox_exec"]}`. The definition format is not selected.
**Visual and consoles.** Documentation offers language examples for the same operation. Dashboard and consoles show sessions created by each SDK without language-specific differences.
**Error (illustrative).** `INVALID_REQUEST: memory must be a positive resource value.` SDK validation and service validation agree in meaning.
**Acceptance criteria.** Generation inputs have one authoritative API definition. All delivered SDKs pass the same operation and error contracts; delivery records show TypeScript/Python before Go/Rust.
**E2E test.** LX and LA: run equivalent create/exec/delete programs in the four delivered SDKs against real hosts; view their sessions in consoles.
**Code-level tests.** Regenerate in CI and compare operation coverage; fake HTTP error and event responses. Never mock real service serialization/deserialization in SDK integration tests.

### INT-003 — Agent-readable CLI and documentation
**Statement.** Every CLI command must support JSON output, and documentation must be available in an LLM-readable form.
**Source.** Brief §3; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp sandbox list` → `sb_41 running ubuntu-1`; `lamp sandbox list --json` → `{"sandboxes":[{"id":"sb_41","state":"running","host":"ubuntu-1"}]}`. `lamp docs --json` returns document text or references without terminal decoration.
**API / MCP (illustrative).** `GET /docs/agent → {"text":"Create a sandbox with a recipe..."}`; `docs_read({"topic":"sandboxes"}) → {"text":"Create a sandbox..."}`.
**Visual and consoles.** Docs show copyable machine examples. Consoles can consume structured output without scraping progress bars; people still see readable terminal output.
**Error (illustrative).** `lamp sandbox inspect missing --json` → `{"error":{"code":"NOT_FOUND","message":"Sandbox not found."}}` with a failing exit status.
**Acceptance criteria.** Enumerate all commands, including help, administrative commands, failures and streaming/interactive commands. Their JSON modes parse without prose mixed into the data stream. Agent docs describe all exposed operations.
**E2E test.** LX, M5 and WIN: invoke the command inventory under a JSON parser and exercise a command from agent docs with public Claude Code or Codex.
**Code-level tests.** Snapshot/contract-test JSON output and documentation coverage; fake clocks and API responses. Never mock the compiled CLI process and actual stdout/stderr in CLI integration tests.

### INT-004 — Idempotent retries
**Statement.** Retried operations with idempotency keys must not create duplicates.
**Source.** Brief §3; ADR: none specific. Key lifetime, scope and conflicts are Q04.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe linux-dev --idempotency-key build-274` → `sb_41`; repeating with `--json` → `{"id":"sb_41","replayed":true}`.
**API / MCP (illustrative).** `POST /sandboxes {"recipe":"linux-dev","idempotency_key":"build-274"} → {"id":"sb_41"}` on both attempts; `sandbox_create({"recipe":"linux-dev","idempotency_key":"build-274"}) → {"id":"sb_41"}`.
**Visual and consoles.** One sandbox card and one session entry appear despite a disconnected caller retry. Events identify the original work rather than a second allocation.
**Error (illustrative).** `IDEMPOTENCY_CONFLICT: this key was used with different inputs.` Conflict details are illustrative, not a settled retention policy.
**Acceptance criteria.** In a test of 20 concurrent identical keyed requests and lost responses, exactly one logical sandbox is created. Repeat for supported side-effecting operations after reconnect/failover.
**E2E test.** LX + M5: sever the caller connection after submission, retry through API/CLI/MCP and sleep the coordinating host; count actual sandboxes and commands with side effects.
**Code-level tests.** Property-test duplicate/reordered requests and crash boundaries; fake transport loss and VMM latency. Never mock the persistence/concurrency path used to guarantee one real allocation in integration tests.

### INT-005 — Live event stream
**Statement.** Lamp must expose a live event stream for callers.
**Source.** Brief §§3, 16, 19; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp events --sandbox sb_41` → `sb_41 running; se_9 working`; `lamp events --sandbox sb_41 --json` emits `{"type":"session.status","session":"se_9","status":"working"}`.
**API / MCP (illustrative).** `GET /events → {"type":"sandbox.state","id":"sb_41","state":"running"}` as a streamed event; `events_subscribe({"sandbox":"sb_41"}) → {"event":{"type":"session.status","status":"blocked"}}`.
**Visual and consoles.** Sandbox state, output and session status change without page reload. T3 Code, Herdr and an attached SSH terminal observe the same work.
**Error (illustrative).** `EVENT_CONNECTION_LOST: reconnect to continue watching.` Replay and ordering guarantees remain Q05.
**Acceptance criteria.** Real create, output, working/blocked/idle and delete transitions arrive during execution. Unauthorized callers receive no private events. Latency is measured; no latency limit is invented.
**E2E test.** LX, LA, M5 and WIN: open three observers, execute a task, disconnect/reconnect one observer and verify displayed state against actual running work.
**Code-level tests.** Contract-test event schemas, filters and permission changes; fake slow readers and transport loss. Never mock actual streaming, authentication or runtime event production in integration tests.

### INT-006 — Agent inside
**Statement.** A caller must be able to run a harness inside the sandbox to work autonomously.
**Source.** Brief §4; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp session start --sandbox sb_41 --harness claude-code --task 'Run the Flask tests'` → `se_9 working`; the same with `--json` → `{"session":"se_9","mode":"inside","status":"working"}`.
**API / MCP (illustrative).** `POST /sessions {"sandbox":"sb_41","harness":"claude-code","mode":"inside","task":"Run the Flask tests"} → {"id":"se_9"}`; `session_start({"sandbox":"sb_41","harness":"claude-code","mode":"inside"}) → {"id":"se_9"}`.
**Visual and consoles.** Dashboard identifies the harness and sandbox. Consoles stream its real task and allow the person to answer a question.
**Error (illustrative).** `HARNESS_NOT_READY: Claude Code needs an approved sign-in method.` An unsigned process is not reported as agent-ready.
**Acceptance criteria.** The harness performs a real task after caller detachment and can be rejoined. Commands execute inside the selected sandbox and its connections/policy apply.
**E2E test.** LX, M5 and WIN with Linux sandboxes: use official Claude Code and Codex on a disposable checkout of pallets/flask; detach, reconnect and inspect results.
**Code-level tests.** Test launch/configuration and session state with a fake harness protocol. Never mock real harness startup, a real authorized inference request or guest process location in signed-in acceptance.

### INT-007 — Agent outside
**Statement.** An outside agent must be able to drive a sandbox as a remote computer, running commands, editing files and using the browser.
**Source.** Brief §4 and §§14–15; ADRs 0002, 0004.
**What working looks like — CLI (illustrative).** `lamp exec sb_41 -- python -c 'print(42)'` → `42`; `lamp exec sb_41 --json -- python -c 'print(42)'` → `{"stdout":"42\n","stderr":"","exit_code":0}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/files {"path":"/work/note.txt","text":"hello"} → {"written":5}`; `sandbox_exec({"sandbox":"sb_41","argv":["cat","/work/note.txt"]}) → {"stdout":"hello","exit_code":0}`. Browser actions use KIT-001.
**Visual and consoles.** Dashboard shows command output, changed files and a requested browser view. T3 Code/Herdr show the controlling session; SSH opens the same sandbox shell.
**Error (illustrative).** `CONNECTION_DENIED: this sandbox has no browser connection.` No browser authority is implicit in command authority.
**Acceptance criteria.** An external public harness completes command, edit and browser steps against the same sandbox. Results and exit status match guest state; host files stay outside its authority.
**E2E test.** LX, LA, M5 and WIN: drive a public Flask example through shell/file operations and a browser connection; inspect the page and files independently.
**Code-level tests.** Fuzz path/command framing and test streamed I/O; fake VMM faults and browser upstream errors. Never mock filesystem confinement, actual command execution or browser authorization in integration proof.

## Hosts and fleets

### HST-001 — First-release host matrix
**Statement.** First-release hosts must support Linux x86_64, Linux arm64, macOS on Apple Silicon and Windows.
**Source.** Brief §§5, 22; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp host list` → `ubuntu-1 linux/x86_64; arm-1 linux/arm64; m5-1 macos/arm64; win-test-1 windows/x86_64`; `lamp host list --json` → `{"hosts":[{"id":"arm-1","os":"linux","arch":"arm64","online":true}]}` (one example row).
**API / MCP (illustrative).** `GET /hosts/arm-1 → {"os":"linux","arch":"arm64","virtualization":true}`; `host_inspect({"host":"arm-1"}) → {"os":"linux","arch":"arm64"}`.
**Visual and consoles.** Dashboard labels host OS, CPU architecture and availability. Consoles see eligible sessions from all hosts through one fleet.
**Error (illustrative).** `UNSUPPORTED_HOST: this OS or CPU is outside the supported matrix.` Windows editions/architectures and minimum versions are Q01.
**Acceptance criteria.** Each named OS/CPU row has actual install, Linux guest execution, reconnect and isolation evidence. A compiled binary alone does not satisfy a row.
**E2E test.** LX, LA, M5 and WIN: run host lifecycle smoke and a Linux guest task. AIR supplies extra Mac evidence only after hardware inventory.
**Code-level tests.** Test OS/CPU detection with fake probes; contract-test engine support declarations. Never mock the real hypervisor, guest boot or native host binary for matrix acceptance.

### HST-002 — One fleet address and key set
**Statement.** Any number of enrolled hosts must form one fleet behind one address and one set of keys. One host must work as a fleet of one.
**Source.** Brief §5; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp fleet inspect` → `fl_1 · 3 hosts · one address`; `lamp fleet inspect --json` → `{"id":"fl_1","address":"fleet-a.eilmer.si","hosts":3}`.
**API / MCP (illustrative).** `GET /fleet → {"id":"fl_1","hosts":["ubuntu-1","m5-1","win-test-1"]}`; `fleet_inspect({}) → {"id":"fl_1","host_count":3}`.
**Visual and consoles.** One fleet page shows all hosts. T3 Code and Herdr configure one machine entry; `ssh fleet-a.eilmer.si` lists sessions on every authorized host.
**Error (illustrative).** `WRONG_FLEET: this host belongs to another fleet.` Re-enrollment policy is Q03.
**Acceptance criteria.** The same scoped credential reaches authorized sandboxes on all hosts through the single address. A one-host fleet supports the same interfaces.
**E2E test.** LX + M5 + WIN: enroll hosts, create one session on each and access all three without adding a console machine per host.
**Code-level tests.** Test fleet/host identity and routing with fake host transports. Never mock real multi-host address routing or cross-host key enforcement in federation acceptance.

### HST-003 — Automatic and pinned placement
**Statement.** Lamp must place sandboxes by available room, required OS and devices, and must let a caller pin a sandbox to a host.
**Source.** Brief §5; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe mac-build --host m5-1` → `sb_mac on m5-1`; its `--json` form → `{"id":"sb_mac","host":"m5-1","guest_os":"macos"}`.
**API / MCP (illustrative).** `POST /sandboxes {"recipe":"linux-dev","resources":{"cpu":2,"memory_mib":2048}} → {"id":"sb_41","host":"ubuntu-1"}`; `sandbox_create({"recipe":"mac-build","host":"m5-1"}) → {"id":"sb_mac","host":"m5-1"}`.
**Visual and consoles.** Dashboard shows chosen host and resources. Consoles identify placement without requiring manual routing.
**Error (illustrative).** `NO_ELIGIBLE_HOST: pinned host cannot provide the requested OS, devices or resources.` No silent change of pin or device request occurs.
**Acceptance criteria.** Fixture requests land only on hosts satisfying all constraints. A pin is honored or fails visibly. Concurrent placements do not claim the same unavailable room.
**E2E test.** LX + LA + M5 + WIN: constrain capacity and request native macOS, Windows and device-bearing recipes; check actual guest OS/device inventory and host usage.
**Code-level tests.** Property-test scheduling against randomized host inventories; fake inventory updates. Never mock actual reservations, host capacity or device availability in placement integration tests.

### HST-004 — Coordination failover and sleeping laptops
**Statement.** Fleet coordination must move automatically to another host when one goes away. Laptop sleep must be a normal supported event.
**Source.** Brief §5; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp fleet inspect` → `Coordination available on ubuntu-1; m5-1 sleeping`; `lamp fleet inspect --json` → `{"available":true,"hosts":{"m5-1":"unavailable","ubuntu-1":"online"}}`.
**API / MCP (illustrative).** `GET /fleet → {"available":true,"host_count":3}` after loss; `sandbox_create({"recipe":"linux-dev","idempotency_key":"sleep-case"}) → {"id":"sb_52","host":"ubuntu-1"}`.
**Visual and consoles.** Dashboard marks the missing host and retains its sessions' truthful availability. T3 Code/Herdr/SSH continue reaching sessions on surviving hosts. This is not a promise to migrate execution from a sleeping host.
**Error (illustrative).** `HOST_UNAVAILABLE: this session's host is asleep or disconnected.` Recovery latency and single-host behavior are Q06.
**Acceptance criteria.** Lose the host currently doing coordination; surviving hosts accept authorized requests without manual reassignment. No duplicate sandbox is created on retry; returning hosts reconcile without inventing live execution.
**E2E test.** LX + M5 + AIR: sleep and wake each laptop in turn, disconnect network and stop a host process; create and observe work on a survivor.
**Code-level tests.** State-machine/property-test partitions and rejoining; fake clock and network loss. Never mock actual sleep/wake, process loss or split-network enforcement in failover evidence.

### HST-005 — Opt-in rolling updates
**Statement.** Automatic updates must be opt-in and must roll out one host at a time.
**Source.** Brief §5; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp updates set --automatic on` → `Automatic updates enabled; one host at a time`; its `--json` form → `{"automatic":true,"concurrent_hosts":1}`.
**API / MCP (illustrative).** `PUT /fleet/updates {"automatic":true} → {"automatic":true}`; `updates_configure({"automatic":true}) → {"automatic":true,"concurrent_hosts":1}`.
**Visual and consoles.** Dashboard shows disabled/enabled state and the one host being updated. Consoles show affected session availability rather than reporting vanished work as completed.
**Error (illustrative).** `UPDATE_FAILED: m5-1 did not become ready.` Rollback and active-session handling are Q07.
**Acceptance criteria.** A fresh installation does not auto-update before opt-in. A multi-host rollout never updates more than one host simultaneously; failure is visible.
**E2E test.** LX + M5 + WIN: observe a version change with update opt-out, enable it, then exercise rollout and inject one failed update while sessions are visible.
**Code-level tests.** Test rollout ordering and consent persistence; fake update download and clock. Never mock the real upgrade/restart path or host version readback in update acceptance.

### HST-006 — Fastest supported engine per host
**Statement.** Lamp must choose the fastest engine each host supports while satisfying the request's isolation, OS and device needs.
**Source.** Brief §§5–7, 22; ADR 0003.
**What working looks like — CLI (illustrative).** `lamp host inspect ubuntu-1` → `Selected engine: <measured eligible engine>`; `lamp host inspect ubuntu-1 --json` → `{"engine":"<selected>","isolation":"vm","selection_evidence":"<benchmark-run>"}`.
**API / MCP (illustrative).** `GET /hosts/ubuntu-1 → {"engine":"<selected>","supported_guest_os":["linux"]}`; `host_inspect({"host":"ubuntu-1"}) → {"engine":"<selected>"}`.
**Visual and consoles.** Dashboard shows selected engine and sandbox isolation class. Consoles see the actual sandbox class; changing engine never masquerades a weaker boundary as a microVM.
**Error (illustrative).** `ENGINE_UNAVAILABLE: no supported engine satisfies this request.` Selection weighting is Q08.
**Acceptance criteria.** Selection cites comparable measured eligible engines. Unsupported device/OS combinations are not silently accepted to improve speed. No engine is selected by this document.
**E2E test.** LX, LA, M5 and WIN: compare installed eligible engines on identical requests and verify the selected one and its actual boundary.
**Code-level tests.** Property-test eligibility and selection using fake benchmark records and host probes. Never mock measured engine performance or real request support in selection certification.

## Sandboxes

### SBX-001 — Fast microVM isolation
**Statement.** Lamp must provide Firecracker-like microVM sandboxes built for speed and isolation, preventing agent damage to the host and collisions between parallel work.
**Source.** Brief §§1–2, 6–7; ADR 0003.
**What working looks like — CLI (illustrative).** `lamp sandbox inspect sb_41` → `Isolation: real VM; class: microVM`; `lamp sandbox inspect sb_41 --json` → `{"id":"sb_41","isolation":"vm","class":"microvm"}`.
**API / MCP (illustrative).** `GET /sandboxes/sb_41 → {"isolation":"vm","class":"microvm"}`; `sandbox_inspect({"sandbox":"sb_41"}) → {"isolation":"vm"}`.
**Visual and consoles.** Dashboard labels the boundary. T3 Code/Herdr/SSH can run identical file names in two sessions without shared writable roots.
**Error (illustrative).** `ACCESS_DENIED: host path is outside this sandbox.` This is an illustrative effect, not a mandated host filesystem design.
**Acceptance criteria.** Real attack tests cannot read/write a host canary or another sandbox's private files. Each real-VM sandbox has an independent guest boundary; a shared kernel is never presented as equivalent.
**E2E test.** LX, LA, M5 and WIN: run guest-root filesystem/process/device probes and parallel public Flask checkouts; inspect host canaries and neighbor state.
**Code-level tests.** Fuzz host-facing device/file/control inputs and test confinement configuration; fake VMM failures. Never mock the hypervisor, host access controls or escape attempts in isolation proof.

### SBX-002 — Linux guests on every host
**Statement.** Linux sandboxes must work on every supported host OS.
**Source.** Brief §6; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp sandbox create --guest-os linux --host win-test-1` → `sb_linux running Linux`; its `--json` form → `{"id":"sb_linux","guest_os":"linux","host":"win-test-1"}`.
**API / MCP (illustrative).** `POST /sandboxes {"guest_os":"linux","host":"m5-1","template":"linux-tools"} → {"id":"sb_linux","guest_os":"linux"}`; `sandbox_create({"guest_os":"linux","host":"arm-1"}) → {"id":"sb_arm","guest_os":"linux"}`.
**Visual and consoles.** Dashboard distinguishes host OS from guest OS. All consoles open a Linux session even when its host is a Mac or Windows machine.
**Error (illustrative).** `TEMPLATE_INCOMPATIBLE: this template cannot run on the selected host architecture.` Cross-architecture execution is not assumed.
**Acceptance criteria.** Native-architecture Linux sandboxes boot and execute shell, files, networking and a harness on LX, LA, M5 and WIN. NV follows SBX-006.
**E2E test.** LX, LA, M5 and WIN: run Linux `uname`, a compiler workload and public harness smoke; verify guest architecture and host placement independently.
**Code-level tests.** Contract-test template/guest/host compatibility with fake inventories. Never mock Linux guest execution or actual architecture in host-matrix proof.

### SBX-003 — Native full-VM classes and macOS limit
**Statement.** macOS sandboxes must run only on Mac hosts, with at most 2 per Mac. Windows sandboxes must run on Windows hosts. Both must be clearly labeled slower full-VM classes.
**Source.** Brief §6; ADR 0001; VM research supplies licensing cautions.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe mac-build --host m5-1` → `sb_mac full VM (slower)`; its `--json` form → `{"id":"sb_mac","guest_os":"macos","class":"full_vm"}`.
**API / MCP (illustrative).** `POST /sandboxes {"guest_os":"windows","host":"win-test-1"} → {"id":"sb_win","class":"full_vm"}`; `sandbox_create({"guest_os":"macos","host":"m5-1"}) → {"id":"sb_mac","class":"full_vm"}`.
**Visual and consoles.** Dashboard and console session details show “macOS/Windows — full VM, slower.” They do not display Linux microVM speed claims for these classes.
**Error (illustrative).** `MACOS_LIMIT: 2 macOS sandboxes already run on m5-1.` Or `GUEST_HOST_MISMATCH: Windows requires a Windows host.`
**Acceptance criteria.** A third simultaneous macOS allocation on one Mac is denied, including concurrent requests. Native OS commands and harnesses work. The two-sandbox cap does not apply to Linux sandboxes.
**E2E test.** M5 and qualifying AIR: boot two macOS sandboxes, attempt a third and release one. WIN: boot a Windows sandbox and execute PowerShell/.NET smoke; wrong-host requests fail.
**Code-level tests.** Property-test concurrent macOS slot reservations and OS matching; fake licensing/host inventories. Never mock native macOS/Windows kernel behavior or full-VM startup for certification.

### SBX-004 — Caller-selected resources
**Statement.** Callers must choose sandbox CPU, memory and disk resources.
**Source.** Brief §§5–6; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp sandbox create --template linux-tools --cpu 2 --memory 2GiB --disk 8GiB` → `sb_41 · 2 CPU · 2 GiB · 8 GiB`; its `--json` form → `{"id":"sb_41","resources":{"cpu":2,"memory_mib":2048,"disk_mib":8192}}`.
**API / MCP (illustrative).** `POST /sandboxes {"template":"linux-tools","resources":{"cpu":2,"memory_mib":2048,"disk_mib":8192}} → {"id":"sb_41"}`; `sandbox_create({"template":"linux-tools","resources":{"cpu":2,"memory_mib":2048,"disk_mib":8192}}) → {"id":"sb_41"}`.
**Visual and consoles.** Dashboard shows requested allocation and live use. Console details show the allocation and resource-related task failures.
**Error (illustrative).** `RESOURCE_LIMIT: requested memory exceeds this key's limit.` Units, maxima and overcommit policy remain Q09.
**Acceptance criteria.** Actual allocations agree with accepted requests, within documented platform accounting. Exceeding permitted resources cannot consume another caller's allocation without policy allowing it.
**E2E test.** LX, LA, M5 and WIN: request small resources, run CPU/memory/disk saturation and observe actual host/guest use and failure behavior.
**Code-level tests.** Test units, overflow, negative values and concurrent accounting; fake host capacity. Never mock real resource enforcement or out-of-space/OOM effects in integration tests.

### SBX-005 — Devices only on request
**Statement.** Optional display, audio, keyboard and mouse, GPU and other devices must be attached only when requested by the caller.
**Source.** Brief §§5–6; ADR 0003.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe browser-lab --device display --device keyboard --device mouse` → `sb_ui · requested devices attached`; its `--json` form → `{"id":"sb_ui","devices":["display","keyboard","mouse"]}`.
**API / MCP (illustrative).** `POST /sandboxes {"template":"linux-tools","devices":[]} → {"id":"sb_headless","devices":[]}`; `sandbox_create({"template":"linux-tools","devices":["gpu"]}) → {"id":"sb_gpu","devices":["gpu"]}` on an eligible host.
**Visual and consoles.** Dashboard shows only requested optional device controls and live display/audio where requested. Consoles open those views or ordinary text sessions as appropriate.
**Error (illustrative).** `DEVICE_UNAVAILABLE: no eligible host provides the requested GPU.` Platform infrastructure and exact supported device combinations are Q10.
**Acceptance criteria.** Inspect real guest/VMM device inventories for absent unrequested optional devices and functioning requested ones. Unsupported requests fail explicitly; a rendered display is not proof of GPU access.
**E2E test.** LX, LA, M5 and WIN on eligible hardware: compare headless and requested-device sandboxes, exercise display/input/audio/GPU workloads, and record unsupported combinations.
**Code-level tests.** Test device-request translation and capability filtering; fake engine device inventories. Never mock guest-visible hardware or actual audio/input/GPU behavior in device acceptance.

### SBX-006 — Honest fallback and secret restrictions
**Statement.** Hosts unable to run VMs must offer a clearly labeled weaker fallback boundary. Sandboxes holding logins or secrets must require a real VM unless the owner explicitly overrides it. Public-repo CI must always require a fresh VM.
**Source.** Brief §6; ADRs 0003–0004. ADR 0007 still prohibits Docker as Lamp's runtime.
**What working looks like — CLI (illustrative).** `lamp sandbox create --host no-virt-1 --template public-tools` → `sb_weak · weaker isolation`; its `--json` form → `{"id":"sb_weak","isolation":"weaker","warning":"Not a real VM"}`.
**API / MCP (illustrative).** `POST /sandboxes {"host":"no-virt-1","recipe":"signed-in"} → {"error":{"code":"VM_REQUIRED"}}`; `sandbox_create({"host":"no-virt-1","recipe":"public-tools"}) → {"id":"sb_weak","isolation":"weaker"}`.
**Visual and consoles.** Dashboard and all consoles persistently identify weaker isolation. The owner can explicitly approve the secret-bearing fallback exception; an ordinary agent cannot silently choose it.
**Error (illustrative).** `VM_REQUIRED: this request contains a login or secret.` For a public PR: `FRESH_VM_REQUIRED: fallback override cannot authorize this job.`
**Acceptance criteria.** No-virtualization detection never claims VM isolation. Secret-bearing fallback is denied without explicit owner override. No override permits a stranger's public-repo PR on fallback.
**E2E test.** NV: create public work, request harness/MCP/browser secrets with and without a fixture owner override, and attempt public-PR work; compare UI and actual boundary.
**Code-level tests.** Property-test isolation eligibility and override authority; fake virtualization probe and owner identity. Never mock fallback confinement or proof that real-VM-only requests did not execute.

### SBX-007 — Fork a running sandbox including memory
**Statement.** A running sandbox must be forkable with memory included, preserving task state subject to the mandatory credential stripping rules.
**Source.** Brief §§6–7, 9, 13; ADR 0004.
**What working looks like — CLI (illustrative).** `lamp sandbox fork sb_41` → `sb_42 forked from sb_41; memory included`; `lamp sandbox fork sb_41 --json` → `{"id":"sb_42","parent":"sb_41","memory_included":true}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/forks {} → {"id":"sb_42","memory_included":true}`; `sandbox_fork({"sandbox":"sb_41"}) → {"id":"sb_42","parent":"sb_41"}`.
**Visual and consoles.** Dashboard shows the fork relationship and both sandboxes running. Each continued harness session appears separately in T3 Code, Herdr and SSH.
**Error (illustrative).** `FORK_NOT_POSSIBLE: this device or external resource cannot be safely forked.` Such a limitation is an unmet requested combination, not proof that disk-only restart meets this requirement; see Q11.
**Acceptance criteria.** An in-memory-only counter survives into the fork without application restart. Parent and fork can then diverge in memory/files. Credentials are excluded before capture. Linux timing is BEN-002.
**E2E test.** LX, LA, M5 and WIN: fork a running process with an unsaved in-memory value, open files and dirty disk data; verify parent continuation, independent writes and correct clock/network behavior.
**Code-level tests.** Property-test state serialization and failure cleanup; fake VMM errors, clock jumps and external leases. Never mock real RAM capture/restore, disk consistency or parent/fork independence in fork proof.

## Templates and recipes

### TPL-001 — Lamp's own template format
**Statement.** Templates must use Lamp's own format or be saved from running sandboxes. Lamp must accept no Docker images or Dockerfiles and must not use Docker to run sandboxes.
**Source.** Brief §8; ADR 0007.
**What working looks like — CLI (illustrative).** `lamp template build ./linux-tools.lamp` → `tpl_tools ready`; `lamp template build ./linux-tools.lamp --json` → `{"id":"tpl_tools","state":"ready","format":"lamp"}`. The file extension is illustrative.
**API / MCP (illustrative).** `POST /templates {"definition":{"name":"linux-tools","format":"lamp"}} → {"id":"tpl_tools"}`; `template_build({"definition":"./linux-tools.lamp"}) → {"id":"tpl_tools"}`.
**Visual and consoles.** Dashboard offers Lamp definitions and save-from-sandbox. Consoles launch sessions from these templates; there is no Dockerfile onboarding requirement.
**Error (illustrative).** `UNSUPPORTED_TEMPLATE_FORMAT: use a Lamp template definition or save a sandbox.` This also applies to an OCI/Docker reference submitted as a template.
**Acceptance criteria.** Clean installation creates and forks a sandbox without a Docker runtime. Docker image/Dockerfile template inputs are rejected. Docker inside a sandbox remains KIT-004.
**E2E test.** LX, LA, M5 and WIN without host Docker: build a Lamp template, run a command, then submit a Dockerfile and Docker image reference and verify explicit rejection.
**Code-level tests.** Fuzz the template parser and test format discrimination; fake downloads and VMM calls. Never mock real template preparation/boot or absence of host Docker in acceptance.

### TPL-002 — Save installed tools into a template
**Statement.** Saving a running sandbox as a template must preserve tools installed by agents so later sandboxes start with them, while excluding subscription credentials before saving.
**Source.** Brief §§8, 10, 13; ADRs 0004, 0007.
**What working looks like — CLI (illustrative).** `lamp template save sb_41 --name python-tools` → `tpl_python ready; credentials excluded`; its `--json` form → `{"id":"tpl_python","source":"sb_41","credentials_excluded":true}`.
**API / MCP (illustrative).** `POST /templates/from-sandbox {"sandbox":"sb_41","name":"python-tools"} → {"id":"tpl_python"}`; `template_save({"sandbox":"sb_41","name":"python-tools"}) → {"id":"tpl_python"}`.
**Visual and consoles.** Dashboard lists the saved template and its tools. A new console session runs the installed tool immediately, without a second setup task or inherited personal sign-in.
**Error (illustrative).** `CREDENTIAL_STRIP_FAILED: template was not saved.` A partially sanitized template is not usable.
**Acceptance criteria.** Install a pinned public tool in one sandbox, save, then create another and run that version without reinstalling. Inspect saved state for zero captured subscription credentials. Ownership follows STO-004.
**E2E test.** LX, LA, M5 and WIN: install a public formatter in a Linux sandbox, save and recreate; verify binary/configuration and scan saved disk/memory material with fixture canaries.
**Code-level tests.** Test save metadata and exclusion hooks with fake VMM/storage faults. Never mock real saved filesystem content, subsequent boot or credential exclusion in the persistence proof.

### TPL-003 — Named recipes
**Statement.** A recipe must save a template, harness, kits, devices, resources, network setup and connections, and callers must be able to request it by name.
**Source.** Brief §8; ADRs 0002, 0007.
**What working looks like — CLI (illustrative).** `lamp sandbox create --recipe flask-dev` → `sb_41 ready with Claude Code and Python`; its `--json` form → `{"id":"sb_41","recipe":"flask-dev","session":"se_9"}`.
**API / MCP (illustrative).** `POST /recipes {"name":"flask-dev","template":"tpl_python","harness":"claude-code","kits":["python"],"devices":[],"resources":{"cpu":2,"memory_mib":2048,"disk_mib":8192},"network":"restricted","connections":["github-read"]} → {"name":"flask-dev"}`; `sandbox_create({"recipe":"flask-dev"}) → {"id":"sb_41"}`.
**Visual and consoles.** Dashboard previews all seven recipe parts. T3 Code/Herdr/SSH open the resulting ready session and show which recipe was used.
**Error (illustrative).** `RECIPE_NOT_FOUND: flask-dev is unavailable to this caller.` Recipe edits/versioning are Q12.
**Acceptance criteria.** Every saved part has an observable effect in the created sandbox. Recipe selection does not bypass resource, connection, host or key limits.
**E2E test.** LX + M5 + WIN: create a named recipe with a public harness and MCP connection, launch by name through all interfaces, and inspect tools, devices and enforced network policy.
**Code-level tests.** Contract-test complete recipe serialization and authorization; fake plugin resolution. Never mock actual recipe application or enforced policy in recipe acceptance.

## Lifecycle

### LIF-001 — Idle suspension, saved state and message-driven resume
**Statement.** By default, an idle sandbox must suspend with memory and disk saved, use no CPU or RAM while suspended, and resume instantly on the next message.
**Source.** Brief §9; ADR 0004 applies to saved state. “Instantly” lacks a numeric definition; see Q13.
**What working looks like — CLI (illustrative).** `lamp sandbox inspect sb_41` → `suspended · CPU 0 · RAM 0`; its `--json` form → `{"state":"suspended","guest_cpu_running":false,"resident_guest_ram_bytes":0,"memory_saved":true,"disk_saved":true}`.
**API / MCP (illustrative).** `POST /sessions/se_9/messages {"text":"Continue"} → {"session":"se_9","sandbox_state":"running"}`; `session_send({"session":"se_9","text":"Continue"}) → {"resumed":true}`.
**Visual and consoles.** Dashboard shows suspension and persisted storage. Sending from T3 Code/Herdr or attaching/sending through SSH resumes the session and live output returns, without manual rebuild.
**Error (illustrative).** `RESUME_FAILED: saved state could not be restored.` Never replace lost memory with a cold boot while claiming a successful resume.
**Acceptance criteria.** A memory-only value and disk marker survive suspension/resume. Suspended sandboxes have no running guest CPU or resident guest RAM; host service overhead is reported separately. Measure message-to-resumed-work latency.
**E2E test.** LX, LA, M5 and WIN: make a sandbox eligible for idle, observe real memory/process reclamation, send another message and verify state continuity and credential re-preparation.
**Code-level tests.** State-machine-test suspend/resume and interruptions; fake clock and VMM faults. Never mock actual guest RAM reclamation, saved state or message-driven execution in lifecycle proof.

### LIF-002 — Exact definition of idle
**Statement.** Idle must mean no session activity, no running processes and no open connections. Background processes must count as activity.
**Source.** Brief §9; ADR: none specific. Infrastructure/harness processes versus task processes are Q13.
**What working looks like — CLI (illustrative).** `lamp sandbox inspect sb_41` → `active: background process`; its `--json` form → `{"idle":false,"reasons":["running_process"]}`.
**API / MCP (illustrative).** `GET /sandboxes/sb_41/activity → {"idle":false,"reasons":["open_connection"]}`; `sandbox_activity({"sandbox":"sb_41"}) → {"idle":false,"reasons":["session_activity"]}`.
**Visual and consoles.** Dashboard explains why suspension has not occurred. Consoles show working while a detached build runs; a quiet terminal alone does not imply idle.
**Error (illustrative).** `NOT_IDLE: background work or an open connection remains.` Whether explicit suspension overrides activity is Q13.
**Acceptance criteria.** Each of session activity, a detached task and an open connection independently prevents default idle suspension. Removing all three makes suspension eligible under the configured idle policy.
**E2E test.** LX, LA, M5 and WIN: run a quiet long process, then a held connection, then console input; verify each blocks automatic suspension and subsequent cleanup permits it.
**Code-level tests.** Truth-table/property-test activity aggregation with a fake clock. Never mock actual process-tree discovery, open-connection detection or resulting suspension decisions in integration tests.

### LIF-003 — Expiry and per-sandbox policy overrides
**Statement.** A sandbox must be deleted after a set time under its default lifecycle, and callers must be able to override lifecycle per sandbox.
**Source.** Brief §9; ADR: none specific. Default durations and expiry clock semantics are Q13.
**What working looks like — CLI (illustrative).** `lamp sandbox lifecycle sb_41 --idle suspend --delete-after 24h` → `Policy saved`; its `--json` form → `{"sandbox":"sb_41","idle":"suspend","delete_after":"24h"}`. This duration is a caller choice, not the default.
**API / MCP (illustrative).** `PUT /sandboxes/sb_41/lifecycle {"idle":"suspend","delete_after":"24h"} → {"saved":true}`; `sandbox_lifecycle_set({"sandbox":"sb_41","idle":"suspend","delete_after":"24h"}) → {"saved":true}`.
**Visual and consoles.** Dashboard shows the effective policy and deletion time. Consoles show the session's expiry and a clear terminal state after deletion.
**Error (illustrative).** `LIFECYCLE_FORBIDDEN: this key cannot extend the sandbox beyond its expiry.` Lifecycle overrides remain within access limits.
**Acceptance criteria.** A short fixture expiry deletes sandbox execution/state at the configured time basis. A permitted override changes that behavior only for the selected sandbox; durable storage follows its own rules.
**E2E test.** LX, M5 and WIN: set short fixture expiries for running and suspended sandboxes, apply an override to one, and check actual deletion and retained named storage.
**Code-level tests.** Test expiry races and overrides with a fake clock and storage failures. Never mock actual process cleanup/deletion or preservation of independent durable storage in integration proof.

### LIF-004 — CI deletion default
**Statement.** CI sandboxes must be deleted when their jobs end by default.
**Source.** Brief §§9, 18; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp job inspect job_274` → `completed · sandbox deleted`; `lamp job inspect job_274 --json` → `{"job":"job_274","result":"passed","sandbox_state":"deleted"}`.
**API / MCP (illustrative).** `GET /jobs/job_274 → {"state":"completed","sandbox":"sb_ci","sandbox_state":"deleted"}`; `job_inspect({"job":"job_274"}) → {"sandbox_state":"deleted"}`.
**Visual and consoles.** Dashboard retains job result and saved-output references. T3 Code/Herdr/SSH show ended work and no live shell into a reused job sandbox.
**Error (illustrative).** `SANDBOX_DELETED: this job ended; saved outputs are still available.` Permitted debug retention is an explicit lifecycle override, not the CI default.
**Acceptance criteria.** Success, failure and cancellation each trigger default deletion. A new job receives fresh execution state; saved outputs are collected and remain owner-scoped.
**E2E test.** LX, LA, M5 and WIN: run passing, failing and canceled jobs, check no guest continues and verify logs/build outputs survive outside the deleted sandbox.
**Code-level tests.** Test terminal job event handling and duplicate completion with fake CI events. Never mock real teardown, descendant-process cleanup or output durability in CI lifecycle proof.

### LIF-005 — Warm pool
**Statement.** Lamp must provide a warm pool of ready sandboxes.
**Source.** Brief §§9, 19; ADR: none specific. Matching, sizing and refill policy are Q14.
**What working looks like — CLI (illustrative).** `lamp warm-pool inspect` → `flask-dev: 2 ready`; `lamp warm-pool inspect --json` → `{"recipe":"flask-dev","ready":2}`.
**API / MCP (illustrative).** `GET /warm-pools → {"pools":[{"recipe":"flask-dev","ready":2}]}`; `warm_pool_inspect({"recipe":"flask-dev"}) → {"ready":2}`.
**Visual and consoles.** Dashboard shows ready versus assigned sandboxes and changes on allocation. Consoles see only assigned caller sessions, not someone else's prewarmed state.
**Error (illustrative).** `NO_READY_MATCH: no warm sandbox matches this request.` Whether to wait or create is Q14.
**Acceptance criteria.** A request can receive a previously prepared ready sandbox with correct resources, policy and owner credentials. Concurrent claims never assign the same ready sandbox twice or cross owners.
**E2E test.** LX, LA, M5 and WIN: prepare two ready sandboxes, request concurrently with two identities, verify unique allocation, clean state and measured readiness.
**Code-level tests.** Property-test matching and claim races; fake clock and preparation completion. Never mock real ready-state validation, guest cleanliness or owner separation in warm-pool tests.

### LIF-006 — Fork alternatives and keep the winner
**Statement.** Callers must be able to fork mid-task, try approaches in parallel and keep the successful result.
**Source.** Brief §§9, 16, 19; ADRs 0001, 0004.
**What working looks like — CLI (illustrative).** `lamp sandbox fork sb_41` → `sb_42`; `lamp sandbox fork sb_41 --json` → `{"id":"sb_43","parent":"sb_41"}` on a separate non-retried request. Continue both, choose `sb_42`, and delete the unwanted attempt.
**API / MCP (illustrative).** `POST /sessions/se_42/messages {"text":"Continue this successful approach"} → {"accepted":true}`; `sandbox_delete({"sandbox":"sb_43"}) → {"state":"deleted"}`. No special merge algorithm is mandated.
**Visual and consoles.** Dashboard shows alternatives and their outcomes. T3 Code/Herdr/SSH allow opening each session and continuing the chosen one; git carries selected file changes where needed.
**Error (illustrative).** `MERGE_CONFLICT: selected git changes need resolution.` Lamp does not silently overwrite another approach's work.
**Acceptance criteria.** Alternatives start with the same task state, diverge independently and the caller can continue one and remove the other. Applicable parent/sub-agent file return uses git under SES-004.
**E2E test.** LX + M5: fork a public Flask task, implement two local patches, run tests, keep the passing session and verify losing changes did not overwrite it.
**Code-level tests.** Test fork lineage and session selection with fake harness completion. Never mock RAM-inclusive fork, actual git conflict behavior or retained winner files in workflow acceptance.

## Storage that outlives a sandbox

### STO-001 — Shared caches
**Statement.** Package-download and build caches must be shareable across sandboxes and outlive them.
**Source.** Brief §§10, 18; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp cache attach packages --sandbox sb_41` → `packages attached`; its `--json` form → `{"cache":"packages","sandbox":"sb_41","attached":true}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_42/caches {"name":"packages"} → {"attached":true}`; `cache_attach({"name":"packages","sandbox":"sb_42"}) → {"attached":true}`.
**Visual and consoles.** Dashboard shows cache size and attachments. Console build output shows a real cache hit after the earlier sandbox has been deleted.
**Error (illustrative).** `CACHE_FORBIDDEN: this cache belongs to another owner.` Trust separation for public PR writes is Q15.
**Acceptance criteria.** A second authorized sandbox reuses cached package/build content after first-sandbox deletion. Owner-only default holds. No requirement implies that strangers may write trusted caches.
**E2E test.** LX, LA, M5 and WIN: build a public project twice using a named cache, delete the first sandbox and verify reuse and cross-owner denial.
**Code-level tests.** Test cache authorization, path validation and concurrent access with fake storage faults. Never mock actual cache bytes, build hits or unauthorized filesystem access attempts in integration tests.

### STO-002 — Named volumes with multiple attachments
**Statement.** Named volumes must persist beyond a sandbox and allow several authorized sandboxes to attach.
**Source.** Brief §10; ADR: none specific. Concurrent-write semantics are Q15.
**What working looks like — CLI (illustrative).** `lamp volume attach datasets --sandbox sb_41 --path /data` → `datasets attached at /data`; its `--json` form → `{"volume":"datasets","sandbox":"sb_41","path":"/data"}`.
**API / MCP (illustrative).** `POST /volumes/datasets/attachments {"sandbox":"sb_42","path":"/data"} → {"attached":true}`; `volume_attach({"volume":"datasets","sandbox":"sb_42","path":"/data"}) → {"attached":true}`.
**Visual and consoles.** Dashboard shows both attachments and storage use. Console shells can read the same authorized marker and observe its persistence after one sandbox ends.
**Error (illustrative).** `VOLUME_FORBIDDEN: this caller cannot attach datasets.` No attachment can bypass path confinement.
**Acceptance criteria.** Two permitted sandboxes attach one named volume and observe expected file state under documented semantics. Deleting both sandboxes does not delete the named volume.
**E2E test.** LX + LA and M5 + WIN where attachment is supported: write/read a fixture, delete/recreate execution sandboxes and confirm persistence and permission denial for another owner.
**Code-level tests.** Test attachment authorization and path traversal; fake storage disconnection. Never mock real shared-volume I/O, documented coherence or persistence in storage acceptance.

### STO-003 — Saved CI outputs and logs
**Statement.** CI build results and logs must be saved outside sandbox lifetime.
**Source.** Brief §§10, 18; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp job outputs job_274` → `test-results.xml · build.tar · job.log`; `lamp job outputs job_274 --json` → `{"outputs":["test-results.xml","build.tar","job.log"],"sandbox_state":"deleted"}`.
**API / MCP (illustrative).** `GET /jobs/job_274/outputs → {"outputs":[{"name":"job.log","available":true}]}`; `job_outputs({"job":"job_274"}) → {"outputs":["job.log"]}`.
**Visual and consoles.** Dashboard links logs, result and build outputs after job completion. Consoles show the ended session and an authorized path to its outputs.
**Error (illustrative).** `OUTPUT_NOT_AVAILABLE: this output was not saved.` Retention, size limits and partial uploads are Q15.
**Acceptance criteria.** Passing, failing and canceled fixture jobs retain their saved outputs after actual sandbox deletion. A different owner cannot list or download them.
**E2E test.** LX, LA, M5 and WIN: save a test report, archive and log, delete execution, then download and compare actual content under both owner and non-owner credentials.
**Code-level tests.** Test output metadata and access rules with fake transfer failure. Never mock real output content, upload/download completion or post-deletion availability in integration tests.

### STO-004 — Owner-only persistent state by default
**Statement.** Caches, named volumes, saved CI outputs and saved/updated templates must all be owner-only by default.
**Source.** Brief §10; ADR 0004 also limits credential ownership.
**What working looks like — CLI (illustrative).** `lamp storage list` → `4 resources · owner only`; `lamp storage list --json` → `{"resources":[{"name":"datasets","access":"owner_only"}]}`.
**API / MCP (illustrative).** `GET /volumes/datasets → {"name":"datasets","access":"owner_only"}` for its owner; `template_inspect({"template":"tpl_python"}) → {"access":"owner_only"}`.
**Visual and consoles.** Dashboard lists only authorized storage. T3 Code/Herdr/SSH sessions cannot browse another person's saved state simply because they share a fleet.
**Error (illustrative).** `FORBIDDEN: this saved resource is not shared with you.` Existence-disclosure policy is unresolved in Q20.
**Acceptance criteria.** For every persistent resource type, a second identity cannot list, read, attach or modify a newly created private resource. Deliberate sharing, if offered, must not be assumed from team membership.
**E2E test.** LX + M5 + WIN: two people create all four resource types; attempt cross-owner use through all interfaces and direct mounted paths.
**Code-level tests.** Property-test owner/resource/action combinations; fake identity and storage failures. Never mock real service authorization and mounted-file access boundaries in tenant isolation proof.

## Network

### NET-001 — Host-enforced network setup
**Statement.** All sandbox network setup and its credentials must live on the host. Sandbox root must be unable to remove it or read its keys. API, MCP and CLI must configure it, and network types must be plugins.
**Source.** Brief §11; ADR 0002.
**What working looks like — CLI (illustrative).** `lamp network set sb_41 --policy restricted` → `Host-enforced policy applied`; its `--json` form → `{"sandbox":"sb_41","policy":"restricted","enforced_on":"host"}`.
**API / MCP (illustrative).** `PUT /sandboxes/sb_41/network {"policy":"restricted"} → {"applied":true}`; `network_configure({"sandbox":"sb_41","policy":"restricted"}) → {"enforced_on":"host"}`.
**Visual and consoles.** Dashboard shows effective host policy and live requests. Consoles show denied attempts even if the agent changes its guest routing or proxy variables.
**Error (illustrative).** `NETWORK_DENIED: host policy blocks this destination.` Guest root cannot turn a denial into direct access.
**Acceptance criteria.** Guest route/firewall/env changes and nested containers cannot bypass policy or read VPN/proxy keys. Rules are effective on every path available to the sandbox.
**E2E test.** LX, LA, M5 and WIN: attack with guest root, IPv4/IPv6, DNS, raw traffic and nested Docker where enabled; inspect host-side enforcement and key canaries.
**Code-level tests.** Fuzz policy/plugin inputs and test rule compilation with fake network inventory. Never mock real packet paths, namespace/host enforcement or secret visibility in security tests.

### NET-002 — Independent outbound exits
**Statement.** Each sandbox must be able to route outbound traffic through a chosen VPN, proxy or service with its own exit IP, including direct traffic.
**Source.** Brief §11 item 1; ADR 0002.
**What working looks like — CLI (illustrative).** `lamp network set sb_41 --exit company-vpn` → `Exit: company-vpn`; its `--json` form → `{"sandbox":"sb_41","exit":"company-vpn"}`.
**API / MCP (illustrative).** `PUT /sandboxes/sb_42/network {"exit":"germany-proxy"} → {"applied":true}`; `network_configure({"sandbox":"sb_43","exit":"direct"}) → {"applied":true}`.
**Visual and consoles.** Dashboard shows the selected exit and observed IP per sandbox. Console `curl` requests observe three independently selected exits; no VPN configuration is installed inside the sandbox.
**Error (illustrative).** `EXIT_UNAVAILABLE: company-vpn could not connect.` Fail-open versus fail-closed details are Q16; silently bypassing the selected route cannot count as success.
**Acceptance criteria.** A test IP reflector observes the configured VPN/proxy/direct exits for three simultaneous sandboxes. Keys remain on the appropriate host and one sandbox cannot change another's route.
**E2E test.** LX, LA, M5 and WIN: exercise public third-party VPN/proxy software in fixture networks, verify source IPs, drop one exit and observe explicit failure state.
**Code-level tests.** Test plugin selection and exit isolation with fake provider failures. Never mock actual egress routing or reflector-observed source addresses in exit acceptance.

### NET-003 — Private network membership
**Statement.** Each sandbox must be able to join a private network, such as appearing as its own Tailscale device, with setup and keys held on the host.
**Source.** Brief §11 item 2; ADR 0002.
**What working looks like — CLI (illustrative).** `lamp network join sb_41 --private tailscale-dev` → `sb_41 joined as its own member`; its `--json` form → `{"sandbox":"sb_41","network":"tailscale-dev","member":"lamp-sb-41"}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/networks {"name":"tailscale-dev"} → {"member":"lamp-sb-41"}`; `network_join({"sandbox":"sb_41","network":"tailscale-dev"}) → {"member":"lamp-sb-41"}`.
**Visual and consoles.** Dashboard shows private membership and reachability. Console commands reach only allowed private fixture services; guest inspection finds no membership keys.
**Error (illustrative).** `PRIVATE_NETWORK_FORBIDDEN: this recipe cannot join tailscale-dev.` Private membership does not grant all LAN access.
**Acceptance criteria.** Two sandboxes can have distinct approved network identities/policies. Guest root cannot extract network keys, broaden membership or inherit another owner's membership.
**E2E test.** LX + M5 + WIN with a Tailscale test tailnet: create independent memberships, verify policy-scoped reachability and key exclusion through fork/delete.
**Code-level tests.** Contract-test member ownership and plugin lifecycle; fake Tailscale control API and clock. Never mock real private-network reachability, identity or key confinement in membership proof.

### NET-004 — Inbound dev-server URLs
**Statement.** Callers must be able to obtain inbound URLs for dev servers inside a sandbox.
**Source.** Brief §11 item 3, §20; ADRs 0002, 0006.
**What working looks like — CLI (illustrative).** `lamp port expose sb_41 5000` → `https://web-sb41.eilmer.si`; its `--json` form → `{"sandbox":"sb_41","port":5000,"url":"https://web-sb41.eilmer.si"}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/urls {"port":5000} → {"url":"https://web-sb41.eilmer.si"}`; `sandbox_url_create({"sandbox":"sb_41","port":5000}) → {"url":"https://web-sb41.eilmer.si"}`.
**Visual and consoles.** Dashboard shows server port, URL and live availability. T3 Code/Herdr can open the page; SSH prints the URL for a plain terminal user.
**Error (illustrative).** `SERVICE_NOT_LISTENING: no server is accepting traffic on port 5000.` URL authorization/default-publicness and lifetime remain Q17.
**Acceptance criteria.** A real Flask server is reachable at the issued URL with the chosen access policy, uses valid HTTPS where hosted, and cannot use its origin to read account-page cookies.
**E2E test.** LX, LA, M5 and WIN: run a public Flask example, request the URL, load it from another machine, stop the server and inspect visible availability/error changes.
**Code-level tests.** Test URL routing and sandbox/port binding with fake DNS/certificate services. Never mock actual HTTP forwarding, TLS validation or browser origin isolation in URL acceptance.

### NET-005 — Allow and deny lists
**Statement.** Each sandbox must support host-enforced lists of sites it may and may not reach.
**Source.** Brief §11 item 4; ADR 0002.
**What working looks like — CLI (illustrative).** `lamp network set sb_41 --allow pypi.org --deny blocked.example.net` → `Rules applied`; its `--json` form → `{"allow":["pypi.org"],"deny":["blocked.example.net"],"enforced_on":"host"}`.
**API / MCP (illustrative).** `PUT /sandboxes/sb_41/network {"allow":["pypi.org"],"deny":["blocked.example.net"]} → {"applied":true}`; `network_configure({"sandbox":"sb_41","deny":["blocked.example.net"]}) → {"applied":true}`.
**Visual and consoles.** Dashboard shows effective lists and denied requests live. Consoles receive a clear refusal while permitted package downloads work.
**Error (illustrative).** `NETWORK_DENIED: blocked.example.net is denied by sandbox policy.` Precedence, wildcard/DNS/IP semantics and atomic changes are Q16.
**Acceptance criteria.** Intended allows and denies are enforced outside the guest. Alternate DNS, redirects, IP literals, IPv6, UDP and nested runtimes cannot evade a configured prohibition.
**E2E test.** LX, LA, M5 and WIN: exercise allowed and denied fixture destinations through real DNS/HTTP/TLS and direct sockets, then change guest routing and repeat.
**Code-level tests.** Property/fuzz-test hostname normalization, redirects and policy matching; fake DNS answers and network faults. Never mock actual allow/deny packet enforcement in the bypass suite.

### NET-006 — Explicit access between sandboxes
**Statement.** Sandboxes must reach other sandboxes only when that access is allowed.
**Source.** Brief §11 item 5; ADR 0002.
**What working looks like — CLI (illustrative).** `lamp network allow sb_41 --to sb_42 --port 5432` → `Allowed sb_41 → sb_42:5432`; its `--json` form → `{"from":"sb_41","to":"sb_42","port":5432,"allowed":true}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/network/links {"to":"sb_42","port":5432} → {"allowed":true}`; `sandbox_link_allow({"from":"sb_41","to":"sb_42","port":5432}) → {"allowed":true}`.
**Visual and consoles.** Dashboard draws/lists permitted links and live requests. SSH/T3 Code/Herdr sessions see a connection fail before permission and succeed afterward.
**Error (illustrative).** `SANDBOX_LINK_DENIED: access to sb_42 is not allowed.` Sharing a host or fleet does not grant connectivity.
**Acceptance criteria.** No unsolicited cross-sandbox traffic succeeds before an explicit allow. Accepted traffic matches the authorized link and cannot expand to another owner or unapproved destination.
**E2E test.** LX + LA + M5 + WIN: test same-host and cross-host TCP/UDP probes between sandboxes before and after an allowed fixture link.
**Code-level tests.** Test owner/link/action combinations and stale identifiers with fake fleet topology. Never mock real same-host/cross-host network isolation or link enforcement in acceptance.

## Modularity

### PLG-001 — Customizable plugins with process isolation
**Statement.** Everything in Lamp must be customizable and modular within the binding product and security rules. Anyone must be able to write plugins that run as separate programs; a bad plugin must not crash Lamp or reach into sandboxes.
**Source.** Brief §12; ADRs 0003, 0008.
**What working looks like — CLI (illustrative).** `lamp plugin list` → `fixture-network failed; Lamp healthy`; `lamp plugin list --json` → `{"plugins":[{"name":"fixture-network","state":"failed"}],"lamp_healthy":true}`.
**API / MCP (illustrative).** `GET /plugins → {"plugins":[{"name":"fixture-network","state":"failed"}]}`; `plugin_inspect({"name":"fixture-network"}) → {"state":"failed"}`.
**Visual and consoles.** Dashboard isolates plugin failure to affected requests. Existing unrelated T3 Code/Herdr/SSH sessions keep running and producing output.
**Error (illustrative).** `PLUGIN_FAILED: fixture-network exited; affected setup did not complete.` Separate process alone is insufficient if it has unrestricted host/guest authority; see Q18.
**Acceptance criteria.** Crashing, malformed-output and unauthorized-access plugins neither terminate Lamp nor read a sandbox's private canary. Authorized plugin effects stay within their declared boundary.
**E2E test.** LX, LA, M5 and WIN: run a separate hostile fixture plugin that exits, hangs and probes sandbox paths/control endpoints while unrelated public harness sessions continue.
**Code-level tests.** Fuzz plugin protocol and test timeouts/fault containment with fake plugin processes. Never mock OS process separation, sandbox access denial or service survival in plugin safety proof.

### PLG-002 — Public plugin categories
**Statement.** Plugin interfaces must cover engines, network types, credential sources/password managers, harnesses, kits, CI systems, triggers, consoles and caller access methods, and permit third-party implementations.
**Source.** Brief §§11–12, 15–18; ADRs 0003, 0008.
**What working looks like — CLI (illustrative).** `lamp plugin types` → `engine network credentials harness kit ci trigger console access`; `lamp plugin types --json` → `{"types":["engine","network","credentials","harness","kit","ci","trigger","console","access"]}`.
**API / MCP (illustrative).** `GET /plugins/types → {"types":["engine","network","credentials","harness","kit","ci","trigger","console","access"]}`; `plugin_types({}) → {"types":["engine","network","credentials","harness","kit","ci","trigger","console","access"]}`.
**Visual and consoles.** Dashboard shows installed options in the appropriate selectors. A third-party console plugin adds sessions to T3 Code/Herdr; SSH remains usable independently.
**Error (illustrative).** `PLUGIN_INCOMPATIBLE: this plugin does not implement the required contract.` Versioning and permissions are Q18.
**Acceptance criteria.** Each category has a documented implementable contract and a working independent fixture or shipped plugin. A plugin is not confused with a kit's software/state inside a sandbox.
**E2E test.** LX + M5 + WIN: load separately built plugins across all categories and exercise their observable operation without patching Lamp itself.
**Code-level tests.** Contract-test third-party plugin conformance with fake engine/vendor boundaries. Never mock actual out-of-process loading, plugin effects and capability confinement in extensibility acceptance.

## Harnesses, credentials and settings

### CRED-001 — Any harness
**Statement.** Lamp must support any harness, including Claude Code, Codex, OpenCode, Cursor CLI, Copilot CLI, Amp, Goose, Gemini CLI, Pi, Devin and Droid, through its modular harness support.
**Source.** Brief §§12–13; ADRs 0003–0004.
**What working looks like — CLI (illustrative).** `lamp harness list` → `claude-code codex opencode cursor-cli copilot-cli amp goose gemini-cli pi devin droid`; its `--json` form → `{"harnesses":["claude-code","codex","opencode","cursor-cli","copilot-cli","amp","goose","gemini-cli","pi","devin","droid"]}`.
**API / MCP (illustrative).** `POST /sessions {"sandbox":"sb_41","harness":"codex"} → {"id":"se_codex"}`; `session_start({"sandbox":"sb_41","harness":"goose"}) → {"id":"se_goose"}`.
**Visual and consoles.** Dashboard shows harness/version/readiness and permitted sign-in choices. Each harness session is separately visible in T3 Code, Herdr and SSH.
**Error (illustrative).** `HARNESS_UNAVAILABLE: this version has no supported execution or sign-in route for this request.` A hosted vendor product is not assumed to offer an arbitrary-host runtime.
**Acceptance criteria.** Every named harness has real documented execution/sign-in evidence for its supported form. Unknown vendor contracts remain explicit release gaps; “any harness” also permits a third-party harness plugin.
**E2E test.** LX and M5, plus WIN where supported: run each public third-party harness against a harmless fixture task under its permitted method; record version, output and console visibility.
**Code-level tests.** Contract-test launch/input/output/status adapters with fake harness transcripts and vendor errors. Never mock the actual named harness or its supported authentication in compatibility acceptance.

### CRED-002 — Vendor-approved sign-in catalogue
**Statement.** Each harness must have a list of sign-in methods its vendor allows. Subscription use must follow the vendor's approved route, rather than another product's undocumented credential handling.
**Source.** Brief §13; ADR 0004; subscription-auth research is supporting evidence, not a permission grant.
**What working looks like — CLI (illustrative).** `lamp harness auth-methods claude-code` → `Official sign-in; claude setup-token; API key`; its `--json` form → `{"harness":"claude-code","methods":["vendor-sign-in","setup-token","api-key"]}` with policy/version references in the real catalogue.
**API / MCP (illustrative).** `GET /harnesses/codex/auth-methods → {"methods":["vendor-sign-in","approved-oss-sign-in","api-key"]}`; `harness_auth_methods({"harness":"copilot-cli"}) → {"methods":["vendor-device-login","supported-token"]}`.
**Visual and consoles.** Dashboard offers only methods supported for the chosen harness/version/use. Consoles display sign-in-needed when an approved route is absent; they never claim an unrelated subscription is interchangeable.
**Error (illustrative).** `AUTH_METHOD_NOT_APPROVED: this harness cannot use that subscription route.` Exact vendor eligibility and maintenance are Q19.
**Acceptance criteria.** Each displayed method cites current vendor evidence and is tested in its stated context. Claude official sign-in/setup-token, OpenAI's approved OSS route and Copilot device login are handled according to their own conditions.
**E2E test.** LX + M5 + WIN using fixture vendor accounts: exercise approved interactive/headless paths and refuse an unsupported cross-harness OAuth substitution.
**Code-level tests.** Test catalogue selection by harness/version/context with fake vendor metadata. Never mock official sign-in or a real authorized inference request when certifying a method.

### CRED-003 — Subscription custody and vendor-only use
**Statement.** Subscription logins must be stored only on the person's own hosts and placed only into that person's sandboxes. Host rules must restrict them to vendor servers. Lamp-run servers, including the relay, must never hold or proxy model credentials.
**Source.** Brief §§7, 13, 17; ADRs 0002, 0004, 0009.
**What working looks like — CLI (illustrative).** `lamp harness auth-status claude-code` → `Signed in on owned host; subscription token enters your sandbox`; its `--json` form → `{"signed_in":true,"custody":"owned_hosts","sandbox_token_exception":true}`.
**API / MCP (illustrative).** `GET /harnesses/claude-code/auth-status → {"signed_in":true,"method":"vendor-sign-in","credential_value":null}`; `harness_auth_status({"harness":"claude-code"}) → {"signed_in":true,"custody":"owned_hosts"}`.
**Visual and consoles.** Dashboard distinguishes subscription-token exposure from API-key isolation. T3 Code/Herdr/SSH start an already signed-in session; sharing the session does not copy the person's login into someone else's sandbox.
**Error (illustrative).** `AUTH_OWNER_MISMATCH: this login cannot be placed in that sandbox.` Revoked/expired vendor grants show `SIGN_IN_REQUIRED` without leaking values.
**Acceptance criteria.** A same-owner session makes a real vendor request without repeated sign-in. Another owner cannot receive the token. Vendor-only egress is real. Relay/hosted storage and inference paths never receive decrypted credentials.
**E2E test.** LX, LA, M5 and WIN: use separate fixture identities, inspect host/sandbox placement and egress, and instrument a test relay to confirm no model-token custody/proxying.
**Code-level tests.** Property-test owner bindings and vendor destination rules with fake vendor/relay APIs. Never mock real credential location, packet enforcement or relay ciphertext handling in custody proof.

### CRED-004 — Strip subscription credentials before state capture
**Statement.** Lamp must strip subscription credentials before every snapshot, fork or template save, including memory saved for suspension.
**Source.** Brief §§9, 13; ADR 0004. “Snapshot” here means the internal state capture named in the decision, not a replacement term for template.
**What working looks like — CLI (illustrative).** `lamp sandbox fork sb_41` → `Credentials stripped; sb_42 ready with approved sign-in prepared`; its `--json` form → `{"id":"sb_42","capture_credentials_excluded":true}`.
**API / MCP (illustrative).** `POST /templates/from-sandbox {"sandbox":"sb_41","name":"safe-tools"} → {"id":"tpl_safe","credentials_excluded":true}`; `sandbox_suspend({"sandbox":"sb_41"}) → {"state":"suspended","credentials_excluded":true}`.
**Visual and consoles.** Dashboard shows capture progress/failure without secret values. Resumed/forked console sessions regain sign-in only through the approved host-owned preparation, not a credential embedded in saved state.
**Error (illustrative).** `CREDENTIAL_STRIP_FAILED: no usable saved state was published.` Arbitrary in-process token copies and the exact-fork exception require Q11/Q19 resolution.
**Acceptance criteria.** Disk, RAM, temporary capture files and saved templates contain zero fixture subscription-token copies. The capture operation fails visibly if this cannot be ensured. Recreated state never grants another owner the old login.
**E2E test.** LX, LA, M5 and WIN: seed file/env/in-memory fixture tokens, suspend/fork/save under failure injection, inspect real state artifacts, then restore and verify fresh permitted sign-in behavior.
**Code-level tests.** Test capture hooks, fail-closed ordering and cleanup with fake VMM/write failures. Never mock actual captured memory/disk scans or process-token exclusion when claiming compliance.

### CRED-005 — API keys stay behind a host proxy
**Statement.** Model API keys must be substituted by a host proxy and must never enter a sandbox.
**Source.** Brief §13; ADR 0004.
**What working looks like — CLI (illustrative).** `lamp harness auth-status codex` → `API key: host proxy; guest value: placeholder`; its `--json` form → `{"method":"api-key","custody":"host_proxy","guest_has_key":false}`.
**API / MCP (illustrative).** `POST /sessions {"sandbox":"sb_41","harness":"codex","auth":"api-key"} → {"id":"se_9","auth_ready":true}`; `session_start({"sandbox":"sb_41","harness":"claude-code","auth":"api-key"}) → {"auth_ready":true}`.
**Visual and consoles.** Dashboard shows credential source/status, never a raw key. Consoles perform authenticated work but environment dumps, files and responses contain no real API key.
**Error (illustrative).** `CREDENTIAL_DESTINATION_DENIED: this request cannot receive the API key.` Incompatible TLS/pinning/non-HTTP clients are explicit failures, not reasons to inject the key silently.
**Acceptance criteria.** Real authenticated vendor traffic succeeds. Guest filesystem, env, memory, logs, returned errors, redirects and reflecting endpoints reveal zero API keys. Host proxy traces do not publish them either.
**E2E test.** LX, LA, M5 and WIN: real vendor test calls plus controlled reflecting/redirect endpoints; inspect guest processes and all exported logs/state while attempting theft.
**Code-level tests.** Fuzz header/URL/response matching and credential-owner selection with fake vendor APIs. Never mock the actual substitution/response path, TLS behavior or secret scans in security integration tests.

### CRED-006 — One-way settings and approved return
**Statement.** Settings must follow the person from a chosen source into every sandbox: harness settings; skills, plugins and slash commands; instruction files such as `CLAUDE.md` and `AGENTS.md`; MCP list; git identity and signing. Sandbox changes must return only with that person's approval.
**Source.** Brief §13; ADR 0004 applies to sign-in material.
**What working looks like — CLI (illustrative).** `lamp settings source set ./fixture-settings` → `One-way source selected`; its `--json` form → `{"source":"./fixture-settings","direction":"into_sandboxes","return_requires_approval":true}`.
**API / MCP (illustrative).** `POST /settings/changes {"sandbox":"sb_41"} → {"changes":["AGENTS.md"],"approval_required":true}`; `settings_changes({"sandbox":"sb_41"}) → {"changes":["AGENTS.md"],"approval_required":true}`.
**Visual and consoles.** Dashboard shows the source and a diff/approval before return. Console settings work on launch; an agent editing its instruction file cannot change the source merely by saving.
**Error (illustrative).** `APPROVAL_REQUIRED: settings changes have not been copied back.` Signing private-key handling remains Q19; git identity is not permission to export signing secrets.
**Acceptance criteria.** Each listed settings category appears in new sandboxes. Changes remain local until explicit person approval, then only the approved change returns. Denial leaves the source unchanged.
**E2E test.** LX + M5 + WIN: use fixture settings and a disposable signing identity; start Claude Code/Codex, inspect settings/git signature, change a file, deny then approve its return.
**Code-level tests.** Test source selection, diff/path confinement and approval state; fake approval UI and signing service failures. Never mock actual one-way file behavior, approval enforcement or real signature verification in acceptance.

## Connections

### CON-001 — Signed-in MCP gateway and limited-token exception
**Statement.** MCP connections must be already signed in through a host gateway, keeping upstream tokens out of the sandbox. An MCP server that must run inside may receive only limited-access tokens.
**Source.** Brief §14 item 1; ADRs 0002, 0004.
**What working looks like — CLI (illustrative).** `lamp connection list --sandbox sb_41` → `github-read · host gateway · signed in`; its `--json` form → `{"connections":[{"name":"github-read","kind":"mcp","location":"host_gateway","signed_in":true}]}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/connections {"name":"github-read"} → {"ready":true}`; through the gateway `github.get_file_contents({"owner":"pallets","repo":"flask","path":"README.md"}) → {"content":"<public README>"}` using the public GitHub MCP server (upstream shape illustrative).
**Visual and consoles.** Dashboard shows server, identity and scope without tokens. T3 Code/Herdr/SSH sessions can use approved tools immediately, including a scoped filesystem MCP fixture where applicable.
**Error (illustrative).** `MCP_SCOPE_DENIED: this connection cannot write repository contents.` In-sandbox token classification and allowed limits remain Q21.
**Acceptance criteria.** Gateway-backed tools perform real authorized work without upstream-token presence in the sandbox. The in-sandbox exception is visible, scoped and cannot gain another connection's authority.
**E2E test.** LX, LA, M5 and WIN: call public GitHub MCP with a fixture account and a scoped filesystem MCP server; test tool denial, owner mix-ups and actual token location.
**Code-level tests.** Contract/fuzz-test tool forwarding, audience and owner selection with fake OAuth/vendor errors. Never mock real gateway authorization, MCP transport or in-sandbox token scope in acceptance.

### CON-002 — Signed-in CLI connections
**Statement.** CLI tools such as `gh`, `stripe` and `aws` must use their API tokens through the host proxy; the tokens must never enter the sandbox.
**Source.** Brief §14 item 2; ADR 0004.
**What working looks like — CLI (illustrative).** `lamp exec sb_41 -- gh api repos/pallets/flask --jq .full_name` → `pallets/flask`; its `--json` form → `{"stdout":"pallets/flask\n","exit_code":0}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/commands {"argv":["gh","api","repos/pallets/flask"]} → {"exit_code":0,"stdout":"<repository JSON>"}`; `sandbox_exec({"sandbox":"sb_41","argv":["aws","sts","get-caller-identity"]}) → {"exit_code":0,"stdout":"<fixture identity JSON>"}`.
**Visual and consoles.** Dashboard shows connection identity and use. Console shell commands are signed in without readable real `GH_TOKEN`, Stripe keys or AWS credentials.
**Error (illustrative).** `CLI_AUTH_UNSUPPORTED: this signing protocol cannot use the configured host connection.` Real AWS request signing needs proof, not assumed bearer-header substitution.
**Acceptance criteria.** Named tools perform real scoped requests using supported auth. No API tokens appear in guest files/env/memory/output or saved state. Unsupported modes fail explicitly instead of injecting credentials.
**E2E test.** LX, LA, M5 and WIN: run `gh` read operations, Stripe test-mode reads and AWS fixture-account identity/read operations; inspect wire behavior and guest secret canaries.
**Code-level tests.** Test auth adaptation, SigV4 where used and error scrubbing with fake vendor APIs. Never mock the actual CLI binary, real signing/proxy path or token-exclusion scans for compatibility certification.

### CON-003 — Owner-machine services and four access levels
**Statement.** Connections to services on the owner's machines must support off, read-only, ask first and full access. Lamp must use these connections rather than a separate stateful-app migration feature.
**Source.** Brief §14 item 3; ADR 0002.
**What working looks like — CLI (illustrative).** `lamp connection set fixture-db --access read-only` → `fixture-db read-only`; its `--json` form → `{"connection":"fixture-db","access":"read-only"}`.
**API / MCP (illustrative).** `PUT /connections/fixture-db {"access":"ask-first"} → {"access":"ask-first"}`; `connection_request({"connection":"fixture-db","operation":"write","reason":"Create fixture row"}) → {"state":"awaiting_approval"}`.
**Visual and consoles.** Dashboard lists laptop database, dev server and home-network fixture services with access levels. T3 Code/Herdr/SSH show an ask-first request as blocked until the person decides.
**Error (illustrative).** `READ_ONLY: this connection cannot perform writes.` Or `CONNECTION_OFF: owner-machine service access is disabled.`
**Acceptance criteria.** Off blocks all use; read-only blocks real writes; ask first performs no operation before the approved decision; full permits intended fixture writes. A read-looking request with side effects cannot bypass the declared access level.
**E2E test.** LX + M5 + WIN: connect to a real PostgreSQL fixture and public third-party dev server on an owner-test machine; exercise reads/writes under all four levels and confirm stored state.
**Code-level tests.** Property-test access/operation/approval combinations with fake network/clock. Never mock actual database/service side effects, protocol mediation or approval enforcement in access-level acceptance.

## Kits

### KIT-001 — Logged-in browser outside the agent sandbox by default
**Statement.** The logged-in browser must run on the host or in a separate locked-down sandbox by default. The agent must control it remotely without seeing cookies.
**Source.** Brief §15; ADRs 0002, 0004.
**What working looks like — CLI (illustrative).** `lamp browser inspect --sandbox sb_41` → `Signed in · outside agent sandbox · cookies hidden`; its `--json` form → `{"location":"outside_agent_sandbox","signed_in":true,"cookies_exposed":false}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/browser/actions {"action":"navigate","url":"https://fixture.example.net"} → {"page":"Fixture account"}`; `browser_action({"sandbox":"sb_41","action":"click","target":"Account"}) → {"page":"Account"}`.
**Visual and consoles.** Dashboard and T3 Code/Herdr show the live page. SSH can use the browser action interface and obtain a trusted console takeover link. Cookie/export controls are not exposed to the agent.
**Error (illustrative).** `BROWSER_SECRET_ACCESS_DENIED: cookie export is unavailable to this session.` Raw CDP, JavaScript, network logs and downloads must not defeat the zero-cookie target.
**Acceptance criteria.** Real signed-in browser actions work. Cookie files/values are absent from agent disk, memory, tool results and logs, including hostile browser-control calls and reflective pages.
**E2E test.** LX, LA, M5 and WIN: use Chromium/Firefox fixture profiles and a public test application with disposable auth; run click/navigation and adversarial cookie/DOM/network extraction tests.
**Code-level tests.** Fuzz action authorization and result filtering with fake browser failures. Never mock real browser process placement, cookie storage or extraction paths in cookie-isolation proof.

### KIT-002 — Opt-in browser inside with copied logins
**Statement.** Running a logged-in browser inside the sandbox with copied logins must require opt-in.
**Source.** Brief §15; ADR 0004 applies to other captured credentials. The cookie leak target interaction is Q22.
**What working looks like — CLI (illustrative).** `lamp browser configure sb_41 --inside --copy-logins --approve-exposure` → `Enabled: copied browser logins are inside this sandbox`; its `--json` form → `{"location":"inside","copy_logins":true,"explicit_opt_in":true}`.
**API / MCP (illustrative).** `PUT /sandboxes/sb_41/browser {"location":"inside","copy_logins":true,"explicit_opt_in":true} → {"saved":true}`; `browser_configure({"sandbox":"sb_41","location":"inside","copy_logins":true}) → {"error":{"code":"OPT_IN_REQUIRED"}}` without the required consent.
**Visual and consoles.** Dashboard records the explicit choice and exposes the different credential boundary. All consoles identify this session as using copied browser logins.
**Error (illustrative).** `OPT_IN_REQUIRED: copying browser logins places session secrets inside the sandbox.` Site/device-bound login incompatibility is reported, not claimed universally solved.
**Acceptance criteria.** Default recipes never copy browser logins. Only an explicit authorized opt-in enables this mode. Browser state portability is tested by site/version, and its exposure is reported separately from zero-leak default-mode results.
**E2E test.** LX, M5 and WIN: use a disposable fixture browser account; verify no-copy default, denied unapproved copy, explicit opted-in copy and honest reauthentication when a profile cannot transfer.
**Code-level tests.** Test consent and mode selection with fake profile-transfer errors. Never mock actual browser authentication/state portability or exposure visibility in opt-in acceptance.

### KIT-003 — Human browser takeover
**Statement.** When 2FA or a CAPTCHA is needed, the person must be able to take over the browser from their console and hand it back to the agent.
**Source.** Brief §15; ADR: none specific.
**What working looks like — CLI (illustrative).** `lamp browser takeover se_9` → `Human control: https://console.eilmer.si/takeover/example`; its `--json` form → `{"session":"se_9","control":"human","view":"https://console.eilmer.si/takeover/example"}`.
**API / MCP (illustrative).** `POST /sessions/se_9/browser-control {"holder":"human"} → {"holder":"human"}`; `browser_control_return({"session":"se_9"}) → {"holder":"agent"}`.
**Visual and consoles.** T3 Code/Herdr show “needs a decision” and the live browser; the person completes the challenge and returns control. Plain SSH shows the prompt and authenticated takeover URL for a browser-capable console.
**Error (illustrative).** `HUMAN_CONTROL_ACTIVE: agent browser input is paused.` No automated CAPTCHA bypass is implied.
**Acceptance criteria.** The fixture challenge blocks work, the person takes control, completes it and returns control, and the original session continues. Passwords/codes/cookies do not enter agent transcripts under the protected default mode.
**E2E test.** LX + M5 + WIN: trigger a real test-app MFA/CAPTCHA challenge, take over from T3 Code and Herdr, exercise SSH handoff, and verify continued authenticated work.
**Code-level tests.** State-machine-test control ownership and races with fake challenge events/clock. Never mock real browser input ownership or secret visibility during human takeover in acceptance.

### KIT-004 — Toolchains and Docker inside a sandbox
**Statement.** Toolchains, Docker-in-sandbox and other software/state must be available as kits. Docker inside must be optional and support `docker build`, Compose and service containers for tasks that need them.
**Source.** Brief §§8, 15, 18; ADR 0007.
**What working looks like — CLI (illustrative).** `lamp exec sb_ci -- docker compose up -d` → `fixture-db Started`; its `--json` form → `{"stdout":"fixture-db Started\n","exit_code":0}` after selecting the Docker kit.
**API / MCP (illustrative).** `POST /sandboxes {"template":"linux-tools","kits":["python","docker"]} → {"id":"sb_ci"}`; `sandbox_exec({"sandbox":"sb_ci","argv":["docker","build","-t","fixture-app","."]}) → {"exit_code":0}`.
**Visual and consoles.** Dashboard shows selected kits and resource use. Consoles run build/Compose/service tasks. Sandboxes without the Docker kit have no implied Docker daemon.
**Error (illustrative).** `KIT_REQUIRED: select the Docker kit for this task.` Guest Docker is not permission to mount the host Docker socket.
**Acceptance criteria.** A Linux sandbox builds a fixture Dockerfile, runs Compose and reaches a service container, all within its boundary. No host Docker runtime is needed to create it; unselected optional kit state is absent.
**E2E test.** LX, LA, M5 and WIN with Linux sandboxes: install/select toolchain and Docker kits, build public fixture code, test service DNS/readiness and deletion, then probe host socket isolation.
**Code-level tests.** Contract-test kit composition and Docker control confinement with fake package downloads. Never mock actual guest Docker build/Compose/service behavior or host isolation for kit acceptance.

### KIT-005 — Password managers, fill-on-behalf and per-vault approvals
**Statement.** Password-manager plugins must cover 1Password, Bitwarden, KeePassXC and Proton Pass. Default use must fill from outside without exposing the password to the agent. Injection must be opt-in per secret, and per-use phone/console approval must be configurable per vault.
**Source.** Brief §15; ADRs 0003–0004. Vendor fill-interface availability is Q22.
**What working looks like — CLI (illustrative).** `lamp vault set fixture-vault --approve-each-use on` → `Per-use approval enabled`; its `--json` form → `{"vault":"fixture-vault","approve_each_use":true,"default_delivery":"external_fill"}`.
**API / MCP (illustrative).** `POST /secrets/uses {"secret":"fixture-login","sandbox":"sb_41","purpose":"Sign in"} → {"state":"awaiting_approval"}`; `password_fill({"secret":"fixture-login","target":"fixture-login-form"}) → {"filled":true,"value":null}` after approval.
**Visual and consoles.** Phone or console shows secret reference, destination and purpose, then approval/denial. Dashboard shows status without values. T3 Code/Herdr/SSH display pending/completed use and continue the task after a decision.
**Error (illustrative).** `SECRET_USE_DENIED: the person declined this use.` Or `SECRET_INJECTION_OPT_IN_REQUIRED: this secret may only be filled externally.`
**Acceptance criteria.** All four plugins perform a real supported fixture operation. Default fill reveals zero passwords through tools, DOM/control APIs, logs or guest memory. Each use prompts when enabled; opt-in exposure is per secret, never an accidental vault-wide default.
**E2E test.** LX + M5 + WIN: use dedicated test vaults for the four public managers, approve/deny distinct uses on phone and console, test external fill, and separately exercise deliberate per-secret injection.
**Code-level tests.** Test vault/secret/destination/approval binding with fake vendor APIs and clock. Never mock actual manager access, real fill behavior or password extraction attempts in plugin certification.

## Consoles and sessions

### SES-001 — One machine in any console
**Statement.** The fleet must look like one machine to T3 Code, Herdr or another configured console. Console support must be a plugin.
**Source.** Brief §16; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp console inspect t3-code` → `One fleet address · all authorized sessions`; its `--json` form → `{"console":"t3-code","fleet":"fl_1","machine_entries":1}`.
**API / MCP (illustrative).** `GET /sessions → {"sessions":[{"id":"se_9","host":"ubuntu-1"},{"id":"se_10","host":"m5-1"}]}`; `sessions_list({}) → {"sessions":["se_9","se_10"]}`.
**Visual and consoles.** Dashboard groups hosts in the fleet. T3 Code and Herdr each show one configured machine with cross-host sessions; plain SSH gives the same fleet-level view.
**Error (illustrative).** `CONSOLE_PLUGIN_UNAVAILABLE: this console adapter is not ready.` Another configured console and SSH remain usable.
**Acceptance criteria.** One console machine enrollment reveals sessions from multiple hosts without separate host entries. A separately implemented console plugin can consume the same session behavior.
**E2E test.** LX + M5 + WIN: configure real T3 Code and Herdr once each, launch one session per host and compare their lists with SSH and dashboard.
**Code-level tests.** Contract-test fleet/session mapping with fake console transport. Never mock the actual public consoles or their single-machine behavior when claiming console compatibility.

### SES-002 — Every session visible, live and interactive
**Statement.** Every session in every sandbox must appear separately in consoles, be openable, watchable live and accept typed input. Status must distinguish working, blocked and idle.
**Source.** Brief §16; ADR 0001. Visibility follows SES-006/007.
**What working looks like — CLI (illustrative).** `lamp session list` → `se_9 sb_41 working; se_10 sb_42 blocked`; its `--json` form → `{"sessions":[{"id":"se_9","sandbox":"sb_41","status":"working"},{"id":"se_10","sandbox":"sb_42","status":"blocked"}]}`.
**API / MCP (illustrative).** `POST /sessions/se_10/messages {"text":"Use the simpler approach"} → {"accepted":true}`; `session_watch({"session":"se_10"}) → {"status":"working","output":"Running tests..."}`.
**Visual and consoles.** Dashboard and T3 Code/Herdr/SSH show separate entries, live output and working→blocked→working→idle transitions. Input reaches the selected session only.
**Error (illustrative).** `SESSION_NOT_WRITABLE: this caller has watch access only.` Status detection and background-process interpretation are Q13/Q23.
**Acceptance criteria.** Two sessions in one sandbox and sessions across hosts remain individually addressable. Real output and typed responses arrive without page reload; status matches the fixture harness's actual state.
**E2E test.** LX + M5 + WIN: run public harnesses that work, ask a question and become idle; type from each console and verify destination/session-specific output.
**Code-level tests.** Test session identity/status parsing with fake harness events and network interruptions. Never mock actual interactive I/O, PTY behavior or console state transitions in acceptance.

### SES-003 — Ordinary SSH everywhere
**Statement.** Ordinary `ssh` to a fleet must list sessions, attach live to one or open a shell in its sandbox, from any terminal without Lamp-specific terminal changes.
**Source.** Brief §16; ADR 0001.
**What working looks like — CLI (illustrative).** `ssh fleet-a.eilmer.si` → `1 se_9 working; 2 se_10 idle; choose session or shell`; `lamp session list --json` → `{"sessions":["se_9","se_10"]}` is the JSON counterpart. SSH itself is not claimed to have a `--json` option.
**API / MCP (illustrative).** `POST /sessions/se_9/attach {} → {"session":"se_9","mode":"interactive"}`; `sandbox_shell({"sandbox":"sb_41"}) → {"sandbox":"sb_41","mode":"interactive"}` represent equivalent attachment outcomes.
**Visual and consoles.** Plain OpenSSH lists/attaches and supports resize/input/output in an ordinary terminal. T3 Code/Herdr and dashboard observe the same session while SSH is attached.
**Error (illustrative).** `Permission denied: this identity cannot attach to se_9.` Host-key identity, multiplexed writers and reconnect semantics are Q23.
**Acceptance criteria.** Stock OpenSSH can perform all three actions on every supported host-backed fleet. No terminal patch, custom SSH binary or per-host console enrollment is required.
**E2E test.** LX, LA, M5 and WIN fleets: connect from stock Linux/macOS/Windows OpenSSH terminals, list, attach, type, resize, detach and open a sandbox shell.
**Code-level tests.** Contract/fuzz-test SSH command/PTY negotiation with fake upstream session errors. Never mock stock SSH interoperability, terminal I/O or authorization in SSH acceptance.

### SES-004 — Caller-chosen sub-agent placement and git return
**Statement.** Whoever starts sub-agents must choose whether they share the parent's sandbox or use their own sandbox with a copy of the parent's files. Changes from separate sandboxes must come back through git, and sub-agent sessions must appear in consoles.
**Source.** Brief §16; ADR 0001. This explicit brief choice takes precedence over ADR 0001's shorter one-session-per-sandbox description.
**What working looks like — CLI (illustrative).** `lamp session spawn se_9 --sandbox own --task 'Add a test'` → `se_11 in sb_44`; its `--json` form → `{"session":"se_11","sandbox":"sb_44","parent_session":"se_9","file_return":"git"}`.
**API / MCP (illustrative).** `POST /sessions/se_9/sub-agents {"sandbox":"shared","task":"Review the test"} → {"session":"se_12","sandbox":"sb_41"}`; `sub_agent_start({"parent":"se_9","sandbox":"own","task":"Add a test"}) → {"session":"se_11","sandbox":"sb_44"}`.
**Visual and consoles.** Dashboard and T3 Code/Herdr/SSH show parent/sub-agent relationships and both placement modes. Separate file changes arrive as git changes, not uncontrolled shared-folder writes.
**Error (illustrative).** `GIT_RETURN_CONFLICT: sub-agent changes conflict with parent changes.` Uncommitted files and non-git inputs remain Q24.
**Acceptance criteria.** Shared mode uses the actual parent sandbox. Own mode starts with parent files and isolated subsequent writes; git returns the chosen patch and exposes conflicts. Each session is separately visible.
**E2E test.** LX + M5: public Claude Code/Codex task spawns both modes, changes a public Flask checkout, returns a patch through real git and resolves/observes a deliberate conflict.
**Code-level tests.** Test placement choices and lineage with fake harness messages. Never mock real file isolation, git commits/diffs/conflicts or console sub-agent discovery in acceptance.

### SES-005 — Teleport between laptop and sandbox
**Statement.** A running session must be movable between a laptop and a sandbox.
**Source.** Brief §§16, 19; ADRs 0001, 0004. Transfer semantics/support matrix remain Q25.
**What working looks like — CLI (illustrative).** `lamp session teleport se_local --to sandbox --recipe flask-dev` → `se_local now in sb_41`; its `--json` form → `{"session":"se_local","location":"sandbox","sandbox":"sb_41"}`.
**API / MCP (illustrative).** `POST /sessions/se_local/teleport {"to":"laptop","host":"air-1"} → {"session":"se_local","location":"laptop"}`; `session_teleport({"session":"se_local","to":"sandbox","recipe":"flask-dev"}) → {"session":"se_local","location":"sandbox"}`.
**Visual and consoles.** Dashboard shows the move and current location. T3 Code/Herdr/SSH reopen the continued work with its context and files; two competing copies do not both claim to be the moved session.
**Error (illustrative).** `TELEPORT_UNSUPPORTED: this harness or active resource cannot be moved safely.` Failure leaves an honest, recoverable source state.
**Acceptance criteria.** A real started task continues laptop→sandbox→laptop with its required task context/files and console visibility. Credentials obey owner and capture rules throughout. A new blank session is not success.
**E2E test.** M5 + AIR with LX destination: move official Claude Code/Codex fixture sessions in both directions while checking task context, git changes, authorization and source/destination activity.
**Code-level tests.** State-machine-test handoff/rollback with fake harness transfer, VMM and network faults. Never mock actual harness continuation, file state or credential handling in teleport acceptance.

### SES-006 — Own sessions by default
**Statement.** On team fleets, a person must see only their own sessions by default.
**Source.** Brief §16; ADRs 0001, 0005.
**What working looks like — CLI (illustrative).** `lamp session list` for Bea → `se_bea only`; `lamp session list --json` → `{"sessions":[{"id":"se_bea","owner":"bea"}]}`.
**API / MCP (illustrative).** `GET /sessions → {"sessions":[{"id":"se_bea"}]}` as Bea; `sessions_list({}) → {"sessions":["se_bea"]}` under the same identity.
**Visual and consoles.** Dashboard and T3 Code/Herdr/SSH lists and live events omit unshared Alice sessions. Guessing an ID does not make it watchable.
**Error (illustrative).** `SESSION_FORBIDDEN: this session is not shared with you.` Administrative visibility roles remain Q20.
**Acceptance criteria.** Across list, inspect, attach, search and events, a second team identity cannot view unshared sessions or their output/files through session access. Default team mode is not universal watch access.
**E2E test.** LX + M5 + WIN: run two fixture people, compare every console/API/CLI/MCP view and attempt guessed-ID attachment and event subscription.
**Code-level tests.** Property-test identity/session/action filters with fake account claims. Never mock actual authorization at each transport or private-output exclusion in team isolation tests.

### SES-007 — Explicit watch and type sharing
**Statement.** A person must be able to share a session for someone to watch, or for someone to watch and type.
**Source.** Brief §16; ADR 0005.
**What working looks like — CLI (illustrative).** `lamp session share se_9 --with bea --access watch` → `Bea can watch`; its `--json` form → `{"session":"se_9","person":"bea","access":"watch"}`.
**API / MCP (illustrative).** `POST /sessions/se_9/shares {"person":"bea","access":"watch-and-type"} → {"saved":true}`; `session_share({"session":"se_9","person":"bea","access":"watch"}) → {"saved":true}`.
**Visual and consoles.** Dashboard shows who can watch/type. Bea's T3 Code/Herdr/SSH list gains the shared session live; watch mode displays output without granting input.
**Error (illustrative).** `WATCH_ONLY: this share does not allow typing.` Session sharing is not automatic credential/storage sharing; revocation and concurrent input are Q20/Q23.
**Acceptance criteria.** Watch recipients see real output and cannot type through any interface. Watch-and-type recipients can provide input. Unshared people still cannot see the session.
**E2E test.** LX + M5 + WIN over the relay: share with two fixture people at different levels, type/watch from all three consoles and check exact input authorization.
**Code-level tests.** Contract/property-test share/action decisions with fake identity/events. Never mock real streamed output, rejected input or accepted shared typing in collaboration acceptance.

## Caller access, accounts and keys

### ACC-001 — Built-in Tailscale access and keyless CI connectivity
**Statement.** Built-in Tailscale access must reach a fleet behind NAT without networking setup and let CI connect without a stored secret.
**Source.** Brief §17; ADRs 0001, 0002.
**What working looks like — CLI (illustrative).** `lamp access enable tailscale` → `Tailscale access ready; no port forwarding`; its `--json` form → `{"method":"tailscale","ready":true,"port_forwarding_required":false}`.
**API / MCP (illustrative).** `GET /fleet/access → {"tailscale":{"ready":true}}`; `fleet_access_inspect({}) → {"methods":["tailscale"]}`. A CI workload-identity exchange supplies transient connectivity.
**Visual and consoles.** Dashboard shows reachability and identity setup. T3 Code/Herdr/SSH connect to the fleet even behind home NAT; CI session events arrive from an ephemeral caller.
**Error (illustrative).** `NETWORK_IDENTITY_DENIED: this CI workload is not allowed to join.` Tailscale account/policy setup is still documented; “no networking setup” means no manual routing/port forwarding.
**Acceptance criteria.** A caller outside NAT can connect with built-in access. A fixture CI job uses workload identity without a saved auth key/client secret. Network identity never bypasses Lamp action scopes.
**E2E test.** LX + M5 + WIN behind NAT and blocked-direct-UDP cases: connect public CI and consoles via a test tailnet, inspect secret configuration and actual authorization.
**Code-level tests.** Test identity-to-access mapping with fake Tailscale APIs/JWTs. Never mock real NAT traversal/relayed connectivity or workload-identity joining in connectivity acceptance.

### ACC-002 — Owner domain with automatic HTTPS
**Statement.** A fleet must be reachable through the owner's own domain with automatic HTTPS.
**Source.** Brief §17; ADR 0006 governs Eilmer-hosted domains, not the owner's independently hosted domain.
**What working looks like — CLI (illustrative).** `lamp access enable domain --name lamp.example.net` → `HTTPS ready at https://lamp.example.net`; its `--json` form → `{"url":"https://lamp.example.net","https":"automatic"}`.
**API / MCP (illustrative).** `PUT /fleet/access/domain {"name":"lamp.example.net"} → {"url":"https://lamp.example.net"}`; `fleet_domain_set({"name":"lamp.example.net"}) → {"https":"automatic"}`.
**Visual and consoles.** Dashboard shows DNS/reachability/certificate progress and failures. T3 Code/Herdr/SSH use the fleet address with their supported access route; the web/API endpoint validates with normal TLS trust.
**Error (illustrative).** `DOMAIN_NOT_REACHABLE: complete the documented DNS or reachability setup.` This route does not promise to defeat CGNAT without another access method.
**Acceptance criteria.** A real controlled test domain obtains valid HTTPS automatically and renews under documented behavior. Owner-domain setup requires no Eilmer relay account when independently hosted.
**E2E test.** LX + M5 + WIN: use a disposable test domain and public caller, issue/renew through a test CA and one real trusted certificate lane, then inspect HTTPS and auth.
**Code-level tests.** Test challenge/retry handling with fake ACME/DNS/clock. Never mock real TLS hostname verification and at least one actual issuance/renewal path in domain certification.

### ACC-003 — Local-only, offline access
**Statement.** Lamp must support local-only access, and non-relay fleets with local accounts or their own identity provider must work offline.
**Source.** Brief §17; ADR 0005.
**What working looks like — CLI (illustrative).** `lamp access set --local-only` → `Local-only fleet ready`; its `--json` form → `{"access":"local_only","relay_required":false,"eilmer_account_required":false}`.
**API / MCP (illustrative).** `GET /fleet/access → {"mode":"local_only"}` on the local endpoint; `sessions_list({}) → {"sessions":["se_local"]}` without internet.
**Visual and consoles.** Local dashboard and T3 Code/Herdr/SSH continue to list and control local fixture sessions. An offline local tool task works; a cloud model's inference is not promised offline.
**Error (illustrative).** `EXTERNAL_SERVICE_OFFLINE: this connection needs the internet.` Loss of Clerk must not disable local account access.
**Acceptance criteria.** With external internet and Clerk blocked, local authentication, sandbox creation and local execution still work. Local-only mode does not expose a public listener as an accidental default.
**E2E test.** LX, LA, M5 and WIN: block WAN/DNS for external services, authenticate locally, run a local public toolchain task and verify external callers cannot connect.
**Code-level tests.** Test local route/auth configuration with fake remote identity outages. Never mock actual offline operation, local listeners or network reachability in local-only acceptance.

### ACC-004 — Cloudflare Tunnel access plugin
**Statement.** Cloudflare Tunnel must be supported as a caller-access plugin, separate from the Eilmer relay.
**Source.** Brief §§12, 17; ADRs 0006, 0009.
**What working looks like — CLI (illustrative).** `lamp access enable cloudflare --plugin cloudflare-tunnel` → `Cloudflare access ready`; its `--json` form → `{"method":"cloudflare-tunnel","plugin":true,"ready":true}`.
**API / MCP (illustrative).** `POST /fleet/access/plugins {"type":"cloudflare-tunnel","configuration":"fixture-access"} → {"ready":true}`; `access_plugin_enable({"type":"cloudflare-tunnel","configuration":"fixture-access"}) → {"ready":true}`.
**Visual and consoles.** Dashboard identifies this third-party access route and its trust boundary. Sessions reached through it still appear in the same fleet in T3 Code/Herdr/SSH where the configured access transport supports them.
**Error (illustrative).** `ACCESS_PLUGIN_UNREACHABLE: Cloudflare connector is offline.` It is never presented as the relay's no-decryption guarantee.
**Acceptance criteria.** A separately running plugin reaches an authenticated fleet through Cloudflare without changing Lamp's core. Action/key/session restrictions remain enforced; Cloudflare-specific limits are documented.
**E2E test.** LX + M5 + WIN using a fixture Cloudflare account/domain: create a real connector, call the fleet from outside NAT, then interrupt the connector and inspect failure visibility.
**Code-level tests.** Contract-test access/JWT handling with fake Cloudflare errors and key rotation. Never mock real connector behavior or endpoint authentication in plugin certification.

### ACC-005 — One Eilmer account; independent identity without relay
**Statement.** Every relay user must use the one Eilmer account on Clerk across Eilmer products. Fleets not using the relay must also support local accounts or their own OIDC provider.
**Source.** Brief §17; ADR 0005. No owner project is used as a cross-product test fixture.
**What working looks like — CLI (illustrative).** `lamp auth status` → `Eilmer account: alice; relay enabled`; its `--json` form → `{"identity":"alice","provider":"eilmer","relay":true}`. A separate local fleet returns `{"provider":"local","relay":false}`.
**API / MCP (illustrative).** `GET /identity → {"provider":"eilmer","subject":"fixture-person"}`; `identity_inspect({}) → {"provider":"owner-oidc","subject":"fixture-person"}` on a non-relay fleet.
**Visual and consoles.** Relay sign-in uses Eilmer branding and account identity. Local/OIDC fleet onboarding shows the chosen provider. All consoles receive the correct person's session scope.
**Error (illustrative).** `EILMER_ACCOUNT_REQUIRED: the Eilmer relay requires an Eilmer account.` Offline fleets do not redirect involuntarily to Clerk.
**Acceptance criteria.** Relay sessions bind to the shared Eilmer identity, not a separate Lamp account. Local accounts and a public third-party OIDC provider work without the relay; offline locally hosted identity remains usable.
**E2E test.** LX + M5 + WIN: use a Clerk fixture tenant for the Eilmer account, a local account and a Keycloak fixture; verify correct routing and operation during Clerk/WAN outage.
**Code-level tests.** Test issuer/audience/subject mapping with fake Clerk/OIDC/JWKS responses. Never mock real sign-in, token validation or offline identity operation in account acceptance.

### ACC-006 — Owner-configured sharing modes
**Statement.** The owner must be able to configure a fleet as single-owner, team or public service.
**Source.** Brief §17; ADRs 0001, 0005.
**What working looks like — CLI (illustrative).** `lamp fleet set --mode team` → `Fleet mode: team`; its `--json` form → `{"fleet":"fl_1","mode":"team"}`.
**API / MCP (illustrative).** `PUT /fleet {"mode":"public-service"} → {"mode":"public-service"}`; `fleet_configure({"mode":"single-owner"}) → {"mode":"single-owner"}`.
**Visual and consoles.** Dashboard shows current mode and owner configuration. Consoles apply session ownership/shares; public-service mode does not automatically make private session output public.
**Error (illustrative).** `OWNER_REQUIRED: only the owner can change fleet sharing mode.` Enrollment/roles/public-service admission remain Q20.
**Acceptance criteria.** All three modes can be configured by the authorized owner. Unauthorized changes fail, and session/storage/key restrictions remain effective in every mode.
**E2E test.** LX + M5 + WIN: switch fixture fleet modes, attempt creation/viewing with owner, member and outside-service credentials, and verify the documented authorization matrix.
**Code-level tests.** Property-test mode/identity/action combinations with fake identities. Never mock actual multi-person authorization or cross-mode persistence of private-resource protections in acceptance.

### ACC-007 — Scoped caller keys
**Statement.** Keys must be limitable to specific sandboxes, recipes or hosts; create, run-command, watch and read actions; resource limits; and an expiry date.
**Source.** Brief §17; ADR 0001.
**What working looks like — CLI (illustrative).** `lamp key inspect ci-flask` → `recipe flask-ci; host ubuntu-1; create/run/read; expires 2026-11-09T06:00:00Z`; its `--json` form → `{"name":"ci-flask","recipes":["flask-ci"],"hosts":["ubuntu-1"],"actions":["create","run","read"],"limits":{"cpu":2,"memory_mib":2048},"expires_at":"2026-11-09T06:00:00Z"}`.
**API / MCP (illustrative).** `POST /keys {"sandboxes":["sb_41"],"actions":["watch"],"expires_at":"2026-11-09T06:00:00Z"} → {"id":"key_watch","key":"<one-time display>"}`; `key_inspect({"key":"key_watch"}) → {"sandboxes":["sb_41"],"actions":["watch"]}`.
**Visual and consoles.** Dashboard displays scopes/limits/expiry without repeatedly exposing key material. T3 Code/Herdr/SSH enforce read/watch-only credentials on attach/input, not just on creation.
**Error (illustrative).** `KEY_SCOPE_DENIED: this key cannot run commands in sb_42.` Expiry returns `KEY_EXPIRED: this key has expired.`
**Acceptance criteria.** Test each scope independently and their intersection across API/CLI/MCP/SSH/events/files. An expired key performs zero new authorized actions. Host pinning and forks do not broaden scopes.
**E2E test.** LX + LA + M5 + WIN: mint limited fixture keys, attempt permitted/forbidden create, command, watch and read operations, hit resource caps and cross the expiry boundary.
**Code-level tests.** Property/fuzz-test scope intersections, resource arithmetic and expiry with a fake clock. Never mock real endpoint authorization, command/file denial or expiry enforcement in acceptance.

## Relay

### RLY-001 — Relay reachability, identity and one-time credentials
**Statement.** The open-source relay hosted by Eilmer must let callers reach hosts without public reachability, sign people in, link hosts to accounts and hand out one-time credentials.
**Source.** Brief §17; ADRs 0005, 0008–0009.
**What working looks like — CLI (illustrative).** `lamp relay connect` → `Linked fl_1 at fleet-a.eilmer.si`; its `--json` form → `{"fleet":"fl_1","address":"fleet-a.eilmer.si","linked":true}`.
**API / MCP (illustrative).** `POST /relay/credentials {"fleet":"fl_1"} → {"credential":"<one-time>","one_time":true}`; `relay_connect({"fleet":"fl_1"}) → {"connected":true}` represents the fleet connection outcome.
**Visual and consoles.** Dashboard shows linked hosts and relay connection state. T3 Code/Herdr/SSH reach permitted sessions behind NAT using the single fleet identity.
**Error (illustrative).** `CREDENTIAL_ALREADY_USED: request a new one-time credential.` Unlinked/foreign hosts cannot claim someone else's fleet.
**Acceptance criteria.** A remote caller reaches a NAT-hidden fixture host. Linkage matches authenticated ownership. Replaying a consumed one-time credential does not open a second authorized connection. Self-running the open-source relay has documented setup.
**E2E test.** LX + M5 + WIN behind NAT: authenticate fixture Eilmer accounts, link hosts, issue/use/replay a credential, then repeat through a self-run relay test instance.
**Code-level tests.** Test account/host linkage and one-use consumption races with fake Clerk/network/clock. Never mock real NAT-hidden connectivity or one-time credential enforcement in relay acceptance.

### RLY-002 — Relay never decrypts or holds sandbox/model data
**Statement.** Caller-to-host traffic must stay end-to-end encrypted through the relay. The relay must only forward it and hold no model credentials or sandbox data.
**Source.** Brief §17; ADRs 0004, 0009.
**What working looks like — CLI (illustrative).** `lamp access inspect` → `Relay connected; traffic encrypted caller → host`; its `--json` form → `{"method":"eilmer-relay","end_to_end_encrypted":true,"relay_decrypts":false}`.
**API / MCP (illustrative).** Over that connection `POST /sandboxes/sb_41/commands {"argv":["printf","fixture-canary"]} → {"stdout":"fixture-canary","exit_code":0}`; `sandbox_exec({"sandbox":"sb_41","argv":["printf","fixture-canary"]}) → {"stdout":"fixture-canary"}`. The relay cannot read either body.
**Visual and consoles.** Dashboard explains the encrypted route and actual endpoint. All consoles see normal live sessions; the relay operator's view has no terminal output, file contents or model credentials.
**Error (illustrative).** `HOST_IDENTITY_FAILED: encrypted connection was not established.` A relay-controlled endpoint substitution must not silently become trusted.
**Acceptance criteria.** Relay capture/storage/logs cannot recover canary commands, outputs, files, passwords, cookies or model credentials, and the relay lacks decryption keys. Intentional in-flight encrypted forwarding is not a sandbox-data store.
**E2E test.** LX + M5 + WIN: instrument a real test relay, inspect traffic/logs/storage/key custody and attempt endpoint substitution while watching a canary session.
**Code-level tests.** Test endpoint authentication and invalid ciphertext/handshakes with fake relay faults. Never mock real encryption, certificate/key custody or plaintext-absence inspection in the no-decryption proof.

## CI and triggers

### CI-001 — Shipped CI plugins and extensible coverage
**Statement.** Lamp must support CI through plugins and ship plugins for GitHub Actions, GitLab, Buildkite, Jenkins, Forgejo/Gitea, CircleCI, Azure Pipelines, Bitbucket and Woodpecker. Anyone must be able to add more.
**Source.** Brief §§12, 18; ADRs 0003, 0008. CI research describes different runner contracts, not one universal protocol.
**What working looks like — CLI (illustrative).** `lamp ci list` → `github-actions gitlab buildkite jenkins forgejo gitea circleci azure-pipelines bitbucket woodpecker`; its `--json` form → `{"plugins":["github-actions","gitlab","buildkite","jenkins","forgejo","gitea","circleci","azure-pipelines","bitbucket","woodpecker"]}`.
**API / MCP (illustrative).** `POST /ci/connections {"system":"gitlab","configuration":"fixture-project"} → {"ready":true}`; `ci_configure({"system":"jenkins","configuration":"fixture-controller"}) → {"ready":true}`.
**Visual and consoles.** Dashboard shows each job, CI system, result, sandbox and outputs. T3 Code/Herdr/SSH can inspect authorized running jobs without replacing native CI logs/status.
**Error (illustrative).** `CI_PLUGIN_INCOMPATIBLE: this CI version or runner mode is unsupported.` Versions and exact execution modes remain Q26.
**Acceptance criteria.** Every named shipped plugin runs a real job, reports success/failure/cancellation, saves outputs and removes default disposable execution. Forgejo and Gitea are tested as distinct versioned contracts.
**E2E test.** LX and LA for Linux jobs; M5 for macOS jobs; WIN for Windows jobs where the CI product supports them. Run a public fixture pipeline on every named system with actual controller/service accounts.
**Code-level tests.** Contract-test each provider's registration/job/result/cancel paths with fake vendor APIs and queue events. Never mock real runner execution or native CI completion/cancellation reporting in plugin certification.

### CI-002 — GitHub Actions and coding-agent routes
**Statement.** The GitHub Actions plugin must include the Copilot cloud agent and the Claude Code and Codex actions.
**Source.** Brief §18; ADRs 0003–0004. Remote-access research identifies supported Actions-based Copilot routes and unconfirmed other vendor-hosted routes.
**What working looks like — CLI (illustrative).** `lamp ci inspect github-actions` → `Actions, Copilot Actions-based, Claude Code action, Codex action`; its `--json` form → `{"routes":["actions","copilot-actions-based","claude-code-action","codex-action"]}`.
**API / MCP (illustrative).** `GET /ci/github-actions/jobs/job_274 → {"route":"codex-action","sandbox":"sb_ci","state":"running"}`; `job_watch({"job":"job_274"}) → {"route":"codex-action","status":"working"}`.
**Visual and consoles.** Dashboard links native GitHub runs and shows the appropriate agent session. T3 Code/Herdr/SSH watch that session and its live logs, with the same access rules as other sessions.
**Error (illustrative).** `GITHUB_AGENT_ROUTE_UNSUPPORTED: select a documented supported execution route.` Agent HQ or vendor cloud support is not invented as a substitute.
**Acceptance criteria.** Real ordinary Actions, documented Copilot self-hosted Actions-based work, Claude Code action and Codex action each complete a fixture task on Lamp. Required vendor restrictions do not disable Lamp host network enforcement.
**E2E test.** LX, plus WIN for a documented supported Windows route: run the public actions and a fixture Copilot setup workflow on disposable sandboxes, then verify outputs, session visibility and cleanup.
**Code-level tests.** Contract-test route configuration, runner lifecycle and provider errors with fake GitHub events. Never mock the actual actions/Copilot execution contract or resulting guest work in compatibility evidence.

### CI-003 — GitHub OIDC exchange without stored Lamp secrets
**Statement.** GitHub Actions must sign in without a stored secret by exchanging GitHub's token for a short-lived Lamp key.
**Source.** Brief §17; ADRs 0001, 0005.
**What working looks like — CLI (illustrative).** `lamp auth github-actions` → `Short-lived key ready; no stored Lamp secret`; its `--json` form → `{"authenticated":true,"source":"github-oidc","short_lived":true}` without printing the key in logs.
**API / MCP (illustrative).** `POST /auth/github-actions {"token":"<GitHub OIDC JWT>","fleet":"fl_1"} → {"key":"<short-lived>","expires_at":"2026-11-08T22:10:00Z"}`; `identity_exchange({"issuer":"github-actions","token":"<JWT>"}) → {"authenticated":true,"short_lived":true}`.
**Visual and consoles.** Dashboard identifies repository/workflow job and key scope/expiry. Consoles show its session, not an anonymous all-powerful CI identity.
**Error (illustrative).** `WORKLOAD_IDENTITY_DENIED: this repository or workflow is not trusted.` JWT issuer/audience/signature/expiry and configured workflow claims must actually constrain exchange; exact trust configuration is Q27.
**Acceptance criteria.** A trusted job uses no stored Lamp secret and receives only its configured short-lived authority. Forged, expired, wrong-audience and untrusted-repository tokens yield no key.
**E2E test.** LX + M5 + WIN reachable from a real GitHub-hosted fixture job: exchange OIDC, create scoped work, attempt invalid exchanges and verify key expiry and workflow attribution.
**Code-level tests.** Fuzz JWT/claim parsing and test issuer/audience/signature/expiry with fake JWKS/clock. Never mock a real GitHub-issued token's validation or the service's scope enforcement in exchange certification.

### CI-004 — A fresh sandbox for every job
**Statement.** Every CI job must receive a fresh sandbox, with caches supplied through storage that outlives sandboxes rather than reused job state.
**Source.** Brief §§9–10, 18; ADR 0007.
**What working looks like — CLI (illustrative).** `lamp job inspect job_275` → `Fresh sb_ci275 · cache packages`; its `--json` form → `{"job":"job_275","sandbox":"sb_ci275","fresh":true,"caches":["packages"]}`.
**API / MCP (illustrative).** `GET /jobs/job_276 → {"sandbox":"sb_ci276","fresh":true}`; `job_inspect({"job":"job_276"}) → {"sandbox":"sb_ci276","fresh":true}`.
**Visual and consoles.** Dashboard shows distinct job sandboxes and reusable cache references. T3 Code/Herdr/SSH see separate sessions, never a prior job's live processes in the new job.
**Error (illustrative).** `FRESH_SANDBOX_UNAVAILABLE: this job cannot start in reused execution state.` Warm preparation does not authorize recycling dirty job state.
**Acceptance criteria.** A marker/process from job A is absent from job B's private root/memory, while an explicitly attached cache hit remains available. Completion follows LIF-004.
**E2E test.** LX, LA, M5 and WIN: run sequential and concurrent fixture jobs that write private canaries plus cache entries; verify fresh execution, intentional cache reuse and cleanup.
**Code-level tests.** Test job/sandbox assignment and cache separation with fake CI completion order. Never mock actual clean roots, missing prior processes or real durable-cache behavior in freshness tests.

### CI-005 — Stranger public-PR isolation
**Statement.** Jobs from strangers' public-repo pull requests must run only in fresh VMs, with no secrets and limited network access. No fallback override may weaken this rule.
**Source.** Brief §§6, 18; ADRs 0002–0004. The precise handling of necessary runner/job credentials is Q27.
**What working looks like — CLI (illustrative).** `lamp job inspect job_pr` → `Untrusted public PR · fresh VM · no secrets · restricted network`; its `--json` form → `{"trust":"untrusted_public_pr","isolation":"vm","fresh":true,"secrets":false,"network":"restricted"}`.
**API / MCP (illustrative).** `POST /jobs {"source":"public-pr","trust":"stranger","recipe":"public-ci"} → {"sandbox":"sb_pr","isolation":"vm","secrets":false}`; `job_inspect({"job":"job_pr"}) → {"trust":"untrusted_public_pr","secrets":false}`. Trust is verified from the source, not accepted from hostile input.
**Visual and consoles.** Dashboard highlights the trust class and policy. Consoles can watch permitted logs; the job has no owner's model login, vault, signed-in browser or private service connection.
**Error (illustrative).** `PUBLIC_PR_POLICY_DENIED: this job requires a fresh VM without secrets and with restricted networking.` This includes attacker-selected recipes, hosts and kits.
**Acceptance criteria.** Under a declared restricted fixture policy, a real external PR cannot access protected host/LAN/metadata endpoints, other tenants, unapproved trusted cache state or any owner secret. Malicious job completion/cancellation leaves no reusable compromised execution. Attempts through privileged trigger contexts are denied or safely isolated. The default network/cache policy still needs Q15/Q16/Q27.
**E2E test.** LX and LA, plus WIN for supported Windows public jobs: submit a PR from a separate fixture account to a disposable public fork of pallets/flask; execute the malicious fixture script and inspect canaries/network/artifacts.
**Code-level tests.** Property-test trust classification and non-overridable isolation/secret rules with fake GitHub/GitLab payloads. Never mock the actual fresh VM, untrusted job execution or host-network/credential denial in public-PR proof.

### CI-006 — Triggers and agent schedules
**Statement.** Triggers must use the plugin model, including Slack, Linear, webhooks and schedules. A caller must be able to run an agent on a timer.
**Source.** Brief §§12, 18–19; ADRs 0001, 0004.
**What working looks like — CLI (illustrative).** `lamp schedule create --recipe flask-audit --at '09:00 daily'` → `sch_1 scheduled`; its `--json` form → `{"id":"sch_1","recipe":"flask-audit","schedule":"09:00 daily"}`. Timezone and missed-run semantics are Q28.
**API / MCP (illustrative).** `POST /triggers {"type":"linear","recipe":"flask-audit","configuration":"fixture-team"} → {"id":"tr_1"}`; `schedule_create({"recipe":"flask-audit","schedule":"09:00 daily"}) → {"id":"sch_1"}`.
**Visual and consoles.** Dashboard shows trigger source, selected recipe, next run and resulting session live. T3 Code/Herdr/SSH see scheduled and Slack/Linear/webhook-started sessions just like manually started work.
**Error (illustrative).** `TRIGGER_UNAUTHORIZED: the source event cannot start this recipe.` A webhook payload cannot select arbitrary owner credentials.
**Acceptance criteria.** Each named trigger starts authorized real work with source attribution. A timer fires a fixture agent task. Duplicate delivery cannot create duplicate keyed work; forged events do not start it.
**E2E test.** LX + M5 + WIN: use Slack and Linear test spaces, signed fixture webhooks and a short timer; observe live sessions, results and denials without touching owner projects.
**Code-level tests.** Test signature/auth mapping, deduplication and timer logic with fake vendor APIs/clock. Never mock actual trigger transport or a real scheduled execution in shipped-trigger certification.

## Dashboard and operations

### OPS-001 — Live dashboard of every sandbox, session and resource
**Statement.** The dashboard must show every authorized sandbox, session and resource live.
**Source.** Brief §19; ADRs 0001, 0005. Owner/session visibility remains enforced.
**What working looks like — CLI (illustrative).** `lamp fleet status` → `3 hosts · 4 sandboxes · 5 sessions · CPU/RAM/disk use`; its `--json` form → `{"hosts":3,"sandboxes":4,"sessions":5,"resources":{"cpu_used":4,"memory_mib_used":6144}}` (fixture values).
**API / MCP (illustrative).** `GET /fleet/status → {"sandboxes":4,"sessions":5,"hosts":3}`; `fleet_status({}) → {"sandboxes":4,"sessions":5,"hosts":3}`.
**Visual and consoles.** Fleet view shows host health/capacity; sandbox view shows OS/class/devices/policy/lifecycle; session view shows working/blocked/idle and output; resource views show storage, keys/scopes, connections, kits, jobs, schedules and budgets where authorized. Allocations, output and state update live. T3 Code/Herdr/SSH show the same session facts.
**Error (illustrative).** `LIVE_CONNECTION_LOST: displayed data is from the last successful update.` Stale data must not be presented as observed live execution.
**Acceptance criteria.** Create, modify and delete each resource category and compare its dashboard state with actual service state under two identities. Changes appear without manual reload; latency is measured, not assigned an invented SLA.
**E2E test.** LX + LA + M5 + WIN: exercise real mixed-host tasks and storage/network/lifecycle changes while recording dashboard and three-console state under owner and shared views.
**Code-level tests.** Contract-test authorization, event reduction and reconnect/staleness with fake streams/clock. Never mock actual dashboard-to-service streaming or session/resource correspondence in visual acceptance.

### OPS-002 — Task budgets and watchdog
**Statement.** Lamp must cap time and tokens per task and stop agents that are stuck or looping.
**Source.** Brief §19; ADR: none specific. Token accounting, stopping scope and loop definitions are Q29.
**What working looks like — CLI (illustrative).** `lamp task budget se_9 --time 10m --tokens 20000` → `Limits: 10m / 20000 tokens`; its `--json` form → `{"session":"se_9","time_limit_seconds":600,"token_limit":20000}`. These are caller-selected limits.
**API / MCP (illustrative).** `PUT /sessions/se_9/budget {"time_seconds":600,"tokens":20000} → {"saved":true}`; `task_budget_set({"session":"se_9","time_seconds":600,"tokens":20000}) → {"saved":true}`.
**Visual and consoles.** Dashboard shows consumed/remaining budget and stop reason live. T3 Code/Herdr/SSH display blocked/stopped work with “time limit,” “token limit,” “stuck” or “looping” as appropriate.
**Error (illustrative).** `TASK_BUDGET_EXCEEDED: token limit reached; agent stopped.` Unsupported reliable usage accounting cannot be labeled a working token cap.
**Acceptance criteria.** Separate fixture tasks hit a time limit, token limit, no-progress condition and loop condition and are actually stopped under the documented policy. Legitimate long work is tested separately for false stops.
**E2E test.** LX, LA, M5 and WIN: use real public harness tasks with measured token use and controlled repetitive/no-progress commands; inspect continued descendant activity and actual vendor usage after stop.
**Code-level tests.** Test counters, time boundaries and watchdog detection with fake clock/vendor usage streams. Never mock actual harness stopping, descendant cleanup or real token accounting when certifying caps.

### OPS-003 — Notifications for done, stuck and decisions
**Statement.** Lamp must notify the person when work is done, stuck or needs a decision.
**Source.** Brief §19; ADR: none specific. Delivery channels and preferences remain Q29.
**What working looks like — CLI (illustrative).** `lamp notification list` → `se_9 done; se_10 stuck; se_11 needs a decision`; its `--json` form → `{"notifications":[{"session":"se_11","reason":"needs_decision"}]}`.
**API / MCP (illustrative).** `GET /notifications → {"notifications":[{"session":"se_9","reason":"done"}]}`; `notifications_list({}) → {"notifications":[{"session":"se_10","reason":"stuck"}]}`.
**Visual and consoles.** Dashboard provides links to the affected task/approval. T3 Code/Herdr show the event; plain SSH sees the state and can inspect/respond through ordinary session access.
**Error (illustrative).** `NOTIFICATION_DELIVERY_FAILED: the configured destination is unavailable.` The underlying task state remains inspectable.
**Acceptance criteria.** Trigger all three conditions in real sessions and observe an authorized person's notification with the right session/reason. Another person's private task details are not disclosed.
**E2E test.** LX + M5 + WIN: finish a fixture task, induce a stuck task and request browser/vault approval; observe notification and navigate to the correct live session.
**Code-level tests.** Test event-to-notification mapping and recipient permissions with fake delivery APIs. Never mock real session condition detection or at least one actual notification delivery path in acceptance.

### OPS-004 — Per-sandbox audit of every command and network request
**Statement.** The audit log must record every command and network request per sandbox.
**Source.** Brief §19; ADRs 0002, 0004 constrain secret handling. Coverage of encrypted/guest-internal requests, retention and redaction are Q30.
**What working looks like — CLI (illustrative).** `lamp audit list --sandbox sb_41` → `command: pytest; network: pypi.org:443 allowed`; its `--json` form → `{"events":[{"sandbox":"sb_41","kind":"command","argv":["pytest"]},{"sandbox":"sb_41","kind":"network","destination":"pypi.org:443","decision":"allow"}]}`.
**API / MCP (illustrative).** `GET /sandboxes/sb_41/audit → {"events":[{"kind":"network","decision":"deny","destination":"blocked.example.net"}]}`; `audit_read({"sandbox":"sb_41"}) → {"events":[{"kind":"command","exit_code":0}]}`.
**Visual and consoles.** Dashboard filters audit by sandbox/session/time and appends live events. T3 Code/Herdr/SSH can correlate task output with authorized audit records without raw credentials.
**Error (illustrative).** `AUDIT_UNAVAILABLE: audit records cannot currently be read.` Guest root deleting its local logs cannot erase authoritative evidence.
**Acceptance criteria.** A fixture command/request ledger has matching per-sandbox audit entries, including allowed and denied network attempts. Secret canaries do not appear in exported records. Unresolved coverage is reported as a gap, not silently omitted.
**E2E test.** LX, LA, M5 and WIN: run interactive, background and nested-Docker commands plus DNS/TLS/UDP requests; compare independent observation with audit and attempt guest-side tampering.
**Code-level tests.** Fuzz event/redaction inputs and test attribution with fake clock/storage/network errors. Never mock actual command/network observation, durable audit recording or secret absence in audit proof.

### OPS-005 — Hosted domains, cookie/origin isolation and certificates
**Statement.** Every Eilmer-hosted website, docs, dashboard, relay, host address and sandbox web address must be under `eilmer.si`, with isolated cookies/origins and issuance below the shared certificate rate limits.
**Source.** Brief §20; ADR 0006 specifies the shared limit of 50 new certificates per week and rejects a separate registrable domain.
**What working looks like — CLI (illustrative).** `lamp hosted-addresses list` → `docs.eilmer.si dashboard.eilmer.si fleet-a.eilmer.si web-sb41.eilmer.si`; its `--json` form → `{"addresses":["docs.eilmer.si","dashboard.eilmer.si","fleet-a.eilmer.si","web-sb41.eilmer.si"]}`.
**API / MCP (illustrative).** `GET /fleet/addresses → {"fleet":"fleet-a.eilmer.si","dashboard":"dashboard.eilmer.si"}`; `sandbox_url_create({"sandbox":"sb_41","port":5000}) → {"url":"https://web-sb41.eilmer.si"}`.
**Visual and consoles.** Dashboard and console links stay on the specified hosted domain family. An untrusted sandbox page cannot read or overwrite account authentication; live URL creation does not consume a separate certificate for every sandbox.
**Error (illustrative).** `CERTIFICATE_ISSUANCE_DEFERRED: issuance would exceed the domain's certificate budget.` Specific cookie flags/certificate strategy remain architectural choices.
**Acceptance criteria.** Inventory all hosted names: zero outside `eilmer.si`. Hostile sibling-origin cookie/CORS/WebSocket tests gain zero account authority. More than 50 fixture sandbox names can be provisioned without exceeding 50 new certificates per week, using an appropriate issuance design.
**E2E test.** LX + M5 + WIN browsers: use controlled sibling origins and actual cookie behavior; create many names against a test CA, then verify the selected real issuance path and HTTPS trust.
**Code-level tests.** Test hostname/cookie/origin checks and issuance accounting with fake ACME/DNS/clock. Never mock real browser cookie/origin enforcement or actual TLS validation in domain-isolation certification.

### OPS-006 — No payments yet; future business support
**Statement.** Lamp must have no payments in the first release and must leave room for plans, quotas and billing later.
**Source.** Brief §20; ADRs 0005, 0008.
**What working looks like — CLI (illustrative).** `lamp fleet inspect` → `Self-hosted fleet ready; no payment required`; its `--json` form → `{"ready":true,"payment_required":false}`.
**API / MCP (illustrative).** `POST /sandboxes {"recipe":"linux-dev"} → {"id":"sb_41"}` without a payment step; `sandbox_create({"recipe":"linux-dev"}) → {"id":"sb_41"}` under local owner limits.
**Visual and consoles.** Dashboard onboarding contains no checkout/paywall requirement. Consoles create authorized work normally; resource limits still exist independently of paid plans.
**Error (illustrative).** `RESOURCE_LIMIT: this request exceeds the configured limit.` No “add a card” remedy is invented for first-release self-hosted use.
**Acceptance criteria.** All authorized first-release workflows run without a payment processor. Architecture review identifies where future plan/quota/billing policy can attach without changing sandbox semantics or replacing identities; no billing architecture is chosen here.
**E2E test.** LX + M5 + WIN: complete onboarding, creation and a team session with payment endpoints absent; verify owner resource limits still apply.
**Code-level tests.** Contract-test resource accounting without commercial entitlement dependencies; fake future-policy absence. Never mock real first-run/session creation to claim that payment is optional.

### OPS-007 — Licenses and private-until-demo release condition
**Statement.** Everything running on hosts must be Apache-2.0, including engine, CLI, SDKs and plugins. The relay must be AGPL-3.0. The repository must stay private until the first engine demo works.
**Source.** Brief §20; ADR 0008. Dependency/platform licensing reconciliation is Q31.
**What working looks like — CLI (illustrative).** `lamp about --licenses` → `Host software: Apache-2.0; relay: AGPL-3.0`; its `--json` form → `{"host_license":"Apache-2.0","relay_license":"AGPL-3.0"}`.
**API / MCP (illustrative).** `GET /about → {"host_license":"Apache-2.0","relay_license":"AGPL-3.0"}`; `lamp_info({}) → {"licenses":{"host":"Apache-2.0","relay":"AGPL-3.0"}}`.
**Visual and consoles.** Dashboard/docs identify both licenses clearly; consoles' deployed host components carry matching license notices. There is no instruction to publish the private repository as part of testing.
**Error (illustrative).** Release validation: `LICENSE_CHECK_FAILED: a host artifact has incompatible or missing licensing evidence.` This is a build/release diagnostic, not a new runtime restriction.
**Acceptance criteria.** Distributed Lamp-owned artifacts match the specified licenses and preserve required notices. Repository visibility remains private until recorded engine-demo evidence exists; this document grants no permission to publish.
**E2E test.** LX, LA, M5 and WIN: inspect installed/distributed artifact notices and self-run relay source/license. Read repository visibility and engine demo evidence without changing them.
**Code-level tests.** Check artifact manifests/license inventory with fake package metadata failures. Never mock actual distributed license files or repository visibility evidence in release certification.

### OPS-008 — Opt-in usage data only
**Statement.** Usage data sent to Eilmer must be off by default and require opt-in.
**Source.** Brief §20; ADR 0009 separately forbids relay data custody.
**What working looks like — CLI (illustrative).** `lamp telemetry status` → `Usage data: off`; `lamp telemetry status --json` → `{"usage_data_to_eilmer":false,"opted_in":false}`.
**API / MCP (illustrative).** `PUT /fleet/usage-data {"enabled":true,"opt_in":true} → {"enabled":true}`; `usage_data_status({}) → {"enabled":false}` on a fresh fleet.
**Visual and consoles.** Dashboard shows a clear disabled default and deliberate enable control. T3 Code/Herdr/SSH session operation does not depend on agreeing to telemetry.
**Error (illustrative).** `OPT_IN_REQUIRED: usage data stays disabled without explicit opt-in.` Relay's necessary connection traffic is not misreported as optional usage analytics; payload definitions remain Q32.
**Acceptance criteria.** Fresh installs send zero optional usage-data events to Eilmer. Enabling sends only the documented opted-in data; disabling stops subsequent optional events. Required security custody rules still apply.
**E2E test.** LX, LA, M5, WIN and NV: capture outbound traffic during install/task/shutdown with default, enabled and disabled settings, separating necessary access traffic from analytics.
**Code-level tests.** Test consent persistence and emission gates with fake clock/collector. Never mock actual outbound telemetry observation or first-install defaults in privacy acceptance.

## Performance and proof

### BEN-001 — Linux create target
**Statement.** On Linux, creating from a template must have p50 below 500 ms from the API call to a command actually running.
**Source.** Brief §7; ADR 0003. Eligible workload/cache/host conditions remain Q33.
**What working looks like — CLI (illustrative).** `lamp benchmark run create --host ubuntu-1` → `API → running command p50: <measured> ms; target <500 ms`; its `--json` form → `{"metric":"create","p50_ms":"<measured>","target_ms":500,"comparison":"strictly_less"}`.
**API / MCP (illustrative).** `POST /sandboxes {"template":"tpl_tools","command":["printf","ready"]} → {"id":"sb_41","stdout":"ready"}`; `benchmark_results({"metric":"create"}) → {"endpoint":"command_running","p50_ms":"<measured>"}`.
**Visual and consoles.** Benchmark view shows measured distributions and endpoint definition. Dashboard/consoles mark ready only when actual work can execute, not when a VMM process starts.
**Error (illustrative).** `BENCHMARK_FAILED: create p50 is not below 500 ms.` No result is invented in this document.
**Acceptance criteria.** Independently timed API-to-command observations on LX and LA produce p50 <500 ms in the declared target scenario. Exactly 500 ms fails. Command readiness is verified inside the sandbox.
**E2E test.** LX and LA: run the benchmark protocol below on real KVM hosts with prepared Lamp templates; report cold preparation and warm-pool runs separately.
**Code-level tests.** Test timestamp/percentile calculation with fake clocks and known vectors. Never mock actual API, placement, template loading, VMM or first-command execution in timing evidence.

### BEN-002 — Linux live-fork target
**Statement.** On Linux, forking a running sandbox with memory included must take under 1 s.
**Source.** Brief §7; ADRs 0003–0004. The brief supplies no fork percentile; Q33 remains open.
**What working looks like — CLI (illustrative).** `lamp benchmark run fork --host ubuntu-1` → `Live RAM fork: <measured> ms; target <1000 ms`; its `--json` form → `{"metric":"live_fork","memory_included":true,"elapsed_ms":"<measured>","target_ms":1000}`.
**API / MCP (illustrative).** `POST /sandboxes/sb_41/forks {} → {"id":"sb_42","memory_included":true}`; `sandbox_fork({"sandbox":"sb_41"}) → {"id":"sb_42","memory_included":true}` with independently observed timing.
**Visual and consoles.** Benchmark view separates capture, parent pause/continuation and fork command-readiness times. Console output proves the fork retained the memory-only marker.
**Error (illustrative).** `BENCHMARK_FAILED: a tested live fork took 1000 ms or more.` Reporting a median alone cannot establish the unqualified brief target.
**Acceptance criteria.** Declared target trials each complete the full live capture→usable fork in <1000 ms and pass SBX-007. Parent continuation and credential stripping are included. Record outliers instead of deleting them.
**E2E test.** LX and LA: fork real running workloads with dirty RAM/disk and memory-only markers; collect full latency, parent pause and actual divergence checks.
**Code-level tests.** Test timing boundaries/failure classification with fake clocks/VMM errors. Never mock capture, restore, sanitation, storage consistency or the first command in a fork benchmark.

### BEN-003 — Linux native-speed floor by workload
**Statement.** Linux sandbox speed must be at least 97% of native host speed, measured separately for computation, disk and network.
**Source.** Brief §7; ADR 0003.
**What working looks like — CLI (illustrative).** `lamp benchmark run native-ratio` → `compute <measured>%; disk <measured>%; network <measured>%`; its `--json` form → `{"compute_ratio":"<measured>","disk_ratio":"<measured>","network_ratio":"<measured>","minimum":0.97}`.
**API / MCP (illustrative).** `GET /benchmarks/run_1 → {"native_baseline":"same-host","ratios":{"compute":"<measured>","disk":"<measured>","network":"<measured>"}}`; `benchmark_results({"run":"run_1"}) → {"minimum_ratio":0.97}` plus actual values.
**Visual and consoles.** Benchmark view has separate distributions and workloads, not one blended performance score. Consoles expose workload logs so a missing network test cannot hide behind a fast compiler.
**Error (illustrative).** `BENCHMARK_FAILED: disk ratio is below 0.97.` At least 97% includes exactly 0.97; selection of workloads/statistics is Q33.
**Acceptance criteria.** Same-host, equivalent-resource native versus sandbox tests meet ≥0.97 separately for declared compute, disk and network measures. Report cold/warm and nested virtualization separately; do not substitute an outer-host claim.
**E2E test.** LX and LA: run native and guest workloads with equal tool versions/input, CPU allocation, storage and network peer; compare output correctness and normalized throughput/latency.
**Code-level tests.** Test ratio direction, unit conversion and independent category gates with fake numeric inputs. Never mock actual native or sandbox workloads, I/O or network peers in performance proof.

### BEN-004 — Zero protected-secret leaks everywhere
**Statement.** The security attack suite must produce 0 leaks of API keys, passwords and browser cookies everywhere. Subscription tokens must be documented as the ADR 0004 exception, without excusing forbidden capture or custody.
**Source.** Brief §7; ADRs 0002, 0004, 0009. Explicit password/browser opt-ins create Q22, not an unstated waiver.
**What working looks like — CLI (illustrative).** `lamp security run` → `Protected-secret leaks: 0; subscription exception separately reported`; its `--json` form → `{"api_key_leaks":0,"password_leaks":0,"cookie_leaks":0,"subscription_exception_reported":true}` only on a real passing run.
**API / MCP (illustrative).** `GET /security-runs/sec_1 → {"leaks":{"api_keys":0,"passwords":0,"cookies":0},"coverage":"<actual host lanes>"}`; `security_results({"run":"sec_1"}) → {"leaks":0,"coverage":"<actual>"}`.
**Visual and consoles.** Report view shows every attack, host/mode and pass/fail with redacted evidence. T3 Code/Herdr/SSH outputs themselves are scanned as possible leak channels.
**Error (illustrative).** `SECURITY_GATE_FAILED: protected secret reached an unauthorized observer.` Unsupported/missing host tests are not labeled zero-leak passes.
**Acceptance criteria.** Every applicable attack below records zero protected-secret disclosures in default protected modes on ALL hosts, and no forbidden host/cross-owner action. Subscription capture, vendor egress and relay custody still pass their own requirements.
**E2E test.** ALL: run the attack suite with uniquely tagged synthetic canaries and dedicated vendor/vault/browser fixtures; inspect actual guest state, logs, artifacts and controlled sinks.
**Code-level tests.** Property/fuzz-test secret-path, identity and policy logic with fake vendors/clock. Never mock containment, cryptography, process/browser behavior, captured state or canary observations in security evidence.

### BEN-005 — Public reproducible benchmarks on every change
**Statement.** A public, reproducible suite must run on every change and compare Lamp with Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM on §7 targets plus time-to-agent-ready, fork time, density, reliability and the attack suite. Mac and Windows speed targets must be set after measuring their hypervisors.
**Source.** Brief §§1–2, 21–22; ADRs 0003, 0008. Public artifacts before the private-demo gate require Q34 resolution.
**What working looks like — CLI (illustrative).** `lamp benchmark report run_1` → `6 products · per-host metrics · raw evidence linked`; its `--json` form → `{"products":["Lamp","Cloudroom","E2B Embed","Docker Sandboxes","microsandbox","SmolVM"],"run":"run_1"}`.
**API / MCP (illustrative).** `GET /benchmarks/run_1 → {"revision":"<SHA>","results":"<per-product measurements>","missing":"<explicit gaps>"}`; `benchmark_results({"run":"run_1"}) → {"revision":"<SHA>","evidence":"<reproducible artifact>"}`.
**Visual and consoles.** Public reports show distributions, failures and host/configuration labels. Dashboard links the report; console logs permit inspection of actual work and readiness.
**Error (illustrative).** `BENCHMARK_INCOMPLETE: Windows competitor lane was unavailable.` “Unsupported” is not an infinite time, zero leaks or a fabricated competitive win.
**Acceptance criteria.** Every change has a suite run or an explicit failed/missing gate, reproducible inputs and full six-product comparison where supported. Matchlock, Modal and Daytona remain contextual references; no reference is silently substituted for a required competitor.
**E2E test.** LX, LA, M5 and WIN where supported, plus NV security/fallback lanes: install pinned real products and run the same declared workloads. Report Mac/Windows measurements without invented numeric speed targets.
**Code-level tests.** Test harness adapters, statistics and report completeness with fake result vectors/provider errors. Never mock competitors, Lamp execution, security behavior or measured latency/throughput in public evidence.

## Engineering constraints

### ENG-001 — Engine first
**Statement.** A Linux engine demo that starts and forks sandboxes within the targets must work before other implementation work. Docker images must not be needed for it.
**Source.** Brief §22; ADRs 0003, 0007–0008.
**What working looks like — CLI (illustrative).** Demo `lamp sandbox create --template demo-tools` → `Command running`; its `--json` form → `{"id":"sb_demo","command_running":true}`. `lamp sandbox fork sb_demo --json` returns a memory-inclusive fork.
**API / MCP (illustrative).** `POST /sandboxes {"template":"demo-tools"} → {"id":"sb_demo"}`; `sandbox_fork({"sandbox":"sb_demo"}) → {"id":"sb_demo2","memory_included":true}` when those interfaces are delivered; they are not a dependency for an engine-only demonstration.
**Visual and consoles.** The first demo can use a plain terminal with real outputs/timings; dashboard/T3 Code/Herdr arrive after the engine gate and later observe the same behavior.
**Error (illustrative).** `ENGINE_DEMO_GATE_FAILED: Linux create or live fork misses its target.` Requirements writing and parallel architecture design are not implementation and need not wait.
**Acceptance criteria.** Recorded real Linux-host evidence passes BEN-001/002 before implementation moves beyond the engine stage. Demo inputs use Lamp templates and no Docker image/runtime prerequisite. Both Linux architectures still need first-release evidence, without making both a new prerequisite for the first demo.
**E2E test.** LX or LA for the first demo, then both for release: run the engine on actual KVM hosts and independently verify command readiness, live-memory fork and measured timing.
**Code-level tests.** Test demo harness assertions with fake timing vectors. Never mock engine behavior, memory or API-to-command timing to pass the implementation gate.

### ENG-002 — Reuse only for security or likely best speed
**Statement.** Lamp must write its own components by default and reuse open source only when needed for security or very likely the fastest option.
**Source.** Brief §22; ADR 0003.
**What working looks like — CLI (illustrative).** `lamp about --components` → `Selected components with security/speed rationale`; its `--json` form → `{"components":[{"name":"<selected>","reuse_reason":"security or measured speed"}]}` represents inspectable build evidence, not a mandated component API.
**API / MCP (illustrative).** `GET /about → {"build":"<revision>","component_manifest":"<evidence reference>"}`; `lamp_info({}) → {"build":"<revision>"}` lets a caller identify the reviewed artifact.
**Visual and consoles.** Build/documentation evidence identifies the artifact running in dashboard and consoles. No general-purpose stack is selected merely because it exists.
**Error (illustrative).** Design/release review: `REUSE_RATIONALE_MISSING: component lacks security or speed evidence.`
**Acceptance criteria.** Each reused open-source runtime component has a written rationale tied to security necessity or credible speed evidence. Research proposals are not automatically adopted; licensing is reviewed separately.
**E2E test.** LX, LA, M5 and WIN: map running components to the actual shipped manifest and reproduce cited security/speed evidence on the applicable host type.
**Code-level tests.** Check dependency/manifest completeness with fake missing metadata. Never mock actual shipped binaries, boundary evidence or performance measurements used to justify reuse.

### ENG-003 — Language belongs to architecture
**Statement.** The architecture must choose implementation language with the stated Rust/Go trade-off considered; the requirements must not select it.
**Source.** Brief §22; ADR 0003. Firecracker, Cloud Hypervisor and libkrun are Rust; tsnet and GitHub's runner scale-set client are Go.
**What working looks like — CLI (illustrative).** `lamp about --json` → `{"build":"<revision>","api_definition":"<version>"}`; readable `lamp about` identifies the same build without imposing a language on callers.
**API / MCP (illustrative).** `GET /about → {"build":"<revision>"}`; `lamp_info({}) → {"build":"<revision>"}`. Interface outcomes remain language-independent.
**Visual and consoles.** Dashboard and T3 Code/Herdr/SSH show the same operations regardless of engine/control implementation language.
**Error (illustrative).** Build validation: `UNSUPPORTED_BUILD_TARGET: this artifact does not support the requested host.` A language choice is not proof of cross-platform support.
**Acceptance criteria.** Architecture records the language decision and Rust/Go boundary/security/speed implications. The decision preserves the API and host/feature requirements and passes real cross-platform checks.
**E2E test.** LX, LA, M5 and WIN: run resulting native artifacts and compare user-visible behavior; no language-specific substitute changes required outcomes.
**Code-level tests.** Contract-test any cross-language interface with fake upstream faults. Never mock native artifact execution or real language-boundary serialization in final integration proof.

## Test infrastructure and fixtures

### QA-001 — External Windows E2E lane [proposed]
**Statement.** The suite should use an external, disposable Windows host with verified virtualization for Windows E2E, plus ordinary Windows CI for build/unit checks. Reason: Windows is binding first-release scope but no owner Windows machine exists.
**Source.** Brief §§5–6, 22; ADR 0001. This is a proposed execution plan for mandatory Windows evidence, not a substitute for it.
**What working looks like — CLI (illustrative).** Test invocation `lamp test-host inspect win-test-1` → `Windows · virtualization verified · real E2E eligible`; its `--json` form → `{"host":"win-test-1","os":"windows","virtualization_verified":true,"lane":"real_e2e"}` represents test tooling, not a required product command.
**API / MCP (illustrative).** Against that host `POST /sandboxes {"guest_os":"linux","host":"win-test-1"} → {"id":"sb_linux"}`; `sandbox_create({"guest_os":"windows","host":"win-test-1"}) → {"id":"sb_win","class":"full_vm"}`.
**Visual and consoles.** Test report links actual Windows OS/hypervisor/build information, live dashboard evidence and T3 Code/Herdr/SSH sessions. Unavailable Windows runs are visibly missing.
**Error (illustrative).** `WINDOWS_E2E_NOT_RUN: no qualifying Windows virtualization host was available.` Cross-compilation/unit success cannot change that status.
**Acceptance criteria.** Provision licensed Windows bare metal, or a Windows provider instance with explicitly supported and probed nesting. Run host install, Linux/Windows guests, RAM fork, devices, network, credentials, lifecycle, consoles, update and attack lanes; retain logs and destroy the test host afterward.
**E2E test.** WIN: allocate a temporary external host, record edition/build/CPU/virtualization, install the actual release candidate, execute those lanes and teardown. Standard GitHub-hosted Windows runners are not assumed to support nested virtualization.
**Code-level tests.** Test provider provisioning/teardown orchestration with fake vendor APIs. Never mock Windows kernel/hypervisor/guest/runtime behavior in Windows E2E evidence. Provider, cost and supported editions remain Q01/Q35.

### QA-002 — Named machines and public third-party fixtures
**Statement.** Testing must use the M5 Mac (10 cores, 24 GB, macOS 26), Ubuntu laptop and MacBook Air appropriately, fill missing platform coverage explicitly, and never use the owner's projects or build special integrations for them.
**Source.** Brief §22 and the owner's task instructions; ADR: none specific.
**What working looks like — CLI (illustrative).** Test report `lamp test inventory` → `M5 known; Ubuntu/Air inspected; arm64/Windows lanes external`; its `--json` form → `{"m5":{"cores":10,"ram_gb":24,"macos":"26"},"ubuntu":"<observed>","air":"<observed>","missing_lanes":"<explicit>"}`.
**API / MCP (illustrative).** `GET /hosts/m5-1 → {"os":"macos","arch":"arm64"}`; `sandbox_exec({"sandbox":"sb_fixture","argv":["git","remote","get-url","origin"]}) → {"stdout":"https://github.com/pallets/flask.git\n"}`.
**Visual and consoles.** Dashboard/console recordings use public harnesses, T3 Code, Herdr, stock SSH and public MCP implementations. Tests use disposable test accounts and public fixture code, not personal app data.
**Error (illustrative).** Test preflight: `FORBIDDEN_FIXTURE: owner projects are excluded from this suite.`
**Acceptance criteria.** Fixture inventory contains no owner project, credentials or special adapter. Public examples include pallets/flask, official harnesses, GitHub MCP and the MCP filesystem server scoped to a sacrificial directory. Host results state actual hardware; unsupported/missing coverage remains open.
**E2E test.** ALL: inventory actual hardware and fixture remotes/accounts before running each lane; scan saved logs/examples for forbidden project use and real personal secrets.
**Code-level tests.** Test fixture-source allow/exclusion checks with fake repository metadata. Never mock actual checked-out remotes, public tool binaries, host inventory or artifact secret scans in test-scope certification.

## Five end-to-end user journeys

These are acceptance walkthroughs, not commands executed while writing this document. All Lamp command/API/MCP shapes and their outputs remain illustrative. The final product names come from architecture. Public fixture code is used in disposable directories/accounts. Screens describe required information, not visual styling.

### Journey 1 — One Linux host to signed-in Claude Code

Host: LX, the Ubuntu laptop only after x86_64/KVM preflight succeeds. Use LA as a second native-architecture run. Covers PRD-002, INT-001/003/006, HST-001/002, TPL-001/003, CRED-001–006, SES-002/003, OPS-001 and BEN-001.

1. Start with a clean Linux account, a downloaded verified Lamp release for that architecture and no Docker installation. Record the host facts. The release filename below is illustrative.

```console
$ uname -m
x86_64
$ test -r /dev/kvm && echo 'KVM accessible'
KVM accessible
$ tar -xf lamp-linux-x86_64.tar.gz
$ ./lamp install --user --access local
Lamp installed. Fleet fl_local: ubuntu-1. Local access ready.
$ lamp fleet inspect --json
{"id":"fl_local","hosts":["ubuntu-1"],"access":"local","payment_required":false}
```

2. Open the local dashboard. It shows one host, real-VM eligibility, CPU/RAM/disk capacity, no sessions and usage data off. It offers local identity setup, a Lamp template and a first sandbox. It does not ask for a Dockerfile, relay account or payment.

```console
$ lamp auth login --provider local
Local caller authenticated.
$ lamp template build ./fixtures/linux-tools.lamp --json
{"id":"tpl_tools","state":"ready","format":"lamp"}
$ lamp harness auth-methods claude-code --json
{"harness":"claude-code","methods":["vendor-sign-in","setup-token","api-key"]}
$ lamp harness sign-in claude-code --method vendor-sign-in
Complete official Anthropic sign-in on your owned host. No token is sent to Lamp servers.
Claude Code sign-in ready.
```

The test person uses a dedicated vendor account and the official flow. A token never appears in captured walkthrough output. If sign-in/plan eligibility is unavailable, the signed-in acceptance test is blocked, not passed using a fake model.

3. Choose a one-way fixture settings source and create a recipe containing `tpl_tools`, official Claude Code, a Python kit, no optional devices, 2 CPU/2 GiB/8 GiB, restricted host networking and a read-scoped GitHub connection. The dashboard displays every selected part and the subscription-token exception.

```console
$ lamp settings source set ./fixtures/settings --json
{"direction":"into_sandboxes","return_requires_approval":true}
$ lamp sandbox create --recipe flask-dev --idempotency-key first-session --json
{"id":"sb_41","host":"ubuntu-1","isolation":"vm","guest_os":"linux","state":"running"}
$ lamp exec sb_41 --json -- git clone https://github.com/pallets/flask.git /work/flask
{"exit_code":0,"stdout":"Cloned public fixture checkout to /work/flask\n"}
$ lamp session start --sandbox sb_41 --harness claude-code --task 'Inspect Flask and run a small test' --json
{"session":"se_9","status":"working","signed_in":true}
```

API counterpart (illustrative): `POST /sessions {"sandbox":"sb_41","harness":"claude-code","task":"Inspect Flask and run a small test"} → {"id":"se_9","status":"working"}`. MCP counterpart: `session_start({"sandbox":"sb_41","harness":"claude-code","task":"Inspect Flask and run a small test"}) → {"id":"se_9"}`.

4. Dashboard now shows `sb_41` and `se_9`, increasing resource use, live harness output and network/audit entries. In T3 Code or Herdr, configure this fleet once, open `se_9`, watch actual tool output, and answer its question. With stock SSH:

```console
$ ssh lamp-local
1  se_9  Claude Code  sb_41  working
Select 1, or choose sandbox shell: 1
[Attached to se_9; live harness output]
```

5. Verify a real authorized Claude request and actual guest commands, not just an “auth ready” badge. The host's files stay unchanged. Repeating the keyed creation returns `sb_41`, not a second sandbox. A wrong-scope key gets `FORBIDDEN: this key cannot create sandboxes.` If KVM is absent, the signed-in request gets `VM_REQUIRED` unless the owner explicitly chooses the weaker-boundary exception; that does not pass the real-VM journey.

Pass evidence: clean-install log, API-to-command timing, real vendor-auth success, guest command outputs, dashboard/console state, idempotency allocation count and protected-secret scans. No owner's project is involved.

### Journey 2 — Add Mac and Windows, watch from three consoles

Hosts: LX + M5 + WIN, with AIR for sleep/wake. WIN is the external test host in QA-001, not an assumed owner machine. Covers HST-001–006, SBX-002/003/005, SES-001–003 and ACC-001/005.

1. On M5, inspect actual macOS/hardware and install the Mac release normally. On the Windows test host, install the Windows release with the documented virtualization prerequisites. Do not override the Mac's home directory or launch password/approval-dialog commands as part of this writing task. Enrollment uses the same fixture fleet authority.

```console
Mac> lamp host join --fleet fleet-a.eilmer.si --credential <one-time> --json
{"host":"m5-1","os":"macos","arch":"arm64","fleet":"fl_1","joined":true}
Windows> lamp host join --fleet fleet-a.eilmer.si --credential <one-time> --json
{"host":"win-test-1","os":"windows","fleet":"fl_1","joined":true}
$ lamp host list
ubuntu-1    linux/x86_64   online
m5-1        macos/arm64    online
win-test-1  windows       online
```

2. Dashboard adds host cards live, with real guest/device eligibility and available capacity. Start Linux on each host, macOS on M5 and Windows on WIN. A Windows host must not merely claim Linux support based on WSL distribution sharing.

```console
$ lamp sandbox create --recipe linux-dev --host m5-1 --json
{"id":"sb_mlinux","host":"m5-1","guest_os":"linux","class":"microvm"}
$ lamp sandbox create --recipe mac-build --host m5-1 --json
{"id":"sb_mac","host":"m5-1","guest_os":"macos","class":"full_vm"}
$ lamp sandbox create --recipe windows-dev --host win-test-1 --json
{"id":"sb_win","host":"win-test-1","guest_os":"windows","class":"full_vm"}
```

The actual eligible Linux engine determines its class; a weaker/full-VM implementation cannot print `microvm` without earning it. In a separate fixture step, run two macOS sandboxes on M5, attempt a third and observe `MACOS_LIMIT`. Linux sandboxes are not counted against that macOS-only limit.

3. Start an official supported public harness session in each sandbox. T3 Code shows one fleet machine entry, with separate sessions and host labels. Herdr shows the same sessions under its one fleet entry. Opening any session streams live output, and typing a fixture response reaches that harness. Dashboard shows matching working/blocked/idle states and CPU/RAM/disk use.

```console
$ ssh fleet-a.eilmer.si
1  se_linux  sb_mlinux  m5-1        working
2  se_mac    sb_mac     m5-1        blocked
3  se_win    sb_win     win-test-1  idle
Select session, or sandbox shell: 2
[Attached to se_mac]
```

API/MCP counterparts (illustrative): `GET /sessions → {"sessions":[{"id":"se_linux"},{"id":"se_mac"},{"id":"se_win"}]}`; `session_send({"session":"se_mac","text":"Continue with the test fixture"}) → {"accepted":true}`.

4. Put a laptop host to sleep using the real test operator/OS, while the fleet has a surviving host. Dashboard marks it unavailable. Its sessions do not falsely finish or relocate. T3 Code, Herdr and SSH continue to list/watch work on survivors. Create new Linux work through the same address/key. Wake the laptop and verify reconciliation without duplicate sessions. Measure coordination recovery; do not invent an SLA.

5. Leave automatic updates off, observe no unapproved rollout, then opt in on the fixture fleet and verify one-host-at-a-time updates. A failed host update is visible. A GPU request on an ineligible host yields `DEVICE_UNAVAILABLE`, not a silently headless session.

Pass evidence: real OS/guest/device outputs from all three host types; one-machine console recordings; SSH interaction; macOS cap; sleep/wake/failover and rolling-update timeline. Missing WIN evidence is a release gap.

### Journey 3 — Agent fan-out, mid-task fork and winner

Hosts: LX + M5; repeat Linux memory/performance proof on LA. Covers INT-004/006/007, SBX-007, LIF-006, CRED-004, SES-002/004 and ACC-007.

1. Start official Claude Code or Codex in `sb_parent` against a disposable public Flask checkout. Give it a key scoped to its permitted recipe/hosts/resources. In T3 Code or Herdr, see `se_parent working`; SSH can watch the same session. Let it hold a fixture in-memory progress marker in a long-running helper.

2. The agent uses MCP to start one sub-agent in its own sandbox and one in the parent sandbox (all shapes illustrative):

```json
[
  {"tool":"sub_agent_start","arguments":{"parent":"se_parent","sandbox":"own","task":"Add a regression test"},"result":{"session":"se_test","sandbox":"sb_test","file_return":"git"}},
  {"tool":"sub_agent_start","arguments":{"parent":"se_parent","sandbox":"shared","task":"Review the test plan"},"result":{"session":"se_review","sandbox":"sb_parent"}}
]
```

CLI counterparts: `lamp session spawn se_parent --sandbox own --task 'Add a regression test' --json` returns `{"session":"se_test","sandbox":"sb_test"}`. API counterpart: `POST /sessions/se_parent/sub-agents {"sandbox":"shared","task":"Review the test plan"} → {"session":"se_review","sandbox":"sb_parent"}`.

3. Dashboard shows two sandboxes and three distinct sessions with parent links. T3 Code/Herdr show both sub-agents, even across hosts. SSH lists/attaches to each. Inspect `sb_test`: starting files match the parent fixture, but later private edits do not change the parent automatically. Review and return the test patch with actual git; a deliberate parent edit produces a visible git conflict.

4. Mid-task, fork `sb_test` into two alternatives. Capture strips subscription tokens before either saved memory state is published. Same-owner sign-in is prepared again through the approved route.

```console
$ lamp sandbox fork sb_test --json
{"id":"sb_try_a","parent":"sb_test","memory_included":true}
$ lamp sandbox fork sb_test --json
{"id":"sb_try_b","parent":"sb_test","memory_included":true}
$ lamp session send se_try_a 'Try the minimal fix' --json
{"accepted":true}
$ lamp session send se_try_b 'Try the alternate implementation' --json
{"accepted":true}
```

5. Check the memory-only marker in both forks, then let the alternatives diverge. Dashboard/console statuses update independently. Run real tests; suppose A passes and B fails in this fixture. Watch A's successful output, inspect its git diff, apply selected changes through git and continue its session. Delete B explicitly:

```console
$ lamp exec sb_parent -- git cherry-pick <fixture-winning-commit>
[Fixture winning changes applied, or a real conflict is shown]
$ lamp sandbox delete sb_try_b --json
{"id":"sb_try_b","state":"deleted"}
```

6. Attempt a forbidden host/recipe with the agent key: receive `KEY_SCOPE_DENIED`. Retry an already submitted keyed fork/create after a dropped response and verify no duplicate alternative. A `CREDENTIAL_STRIP_FAILED` outcome leaves no usable credential-bearing capture; it is not reported as a winning fork.

Pass evidence: actual git diffs/conflict/result; memory continuity and independent writes; ≤ target Linux fork observations under BEN-002; parent/sub-agent live console entries; scoped-key denial; scans of real capture files. The model choosing the correct code is not the service acceptance criterion: Lamp must preserve and expose the alternatives accurately.

### Journey 4 — GitHub Actions, including a stranger's public PR

Hosts: LX and LA for Linux jobs; WIN for Windows job coverage. Covers CI-001–005, LIF-004, STO-001/003, KIT-004, NET-001/005, ACC-001/007 and BEN-004.

1. In a dedicated test account, create a disposable public test fork of `pallets/flask`. Configure the Lamp GitHub plugin for a trusted workflow/recipe and a separate untrusted public-PR policy. Keep registration/control credentials outside the job's readable execution authority. The test fixture installs a pinned Lamp CLI before snippets that call it; no final setup-action repository name is assumed.

A trusted workflow can offload with GitHub workload identity (illustrative YAML):

```yaml
permissions:
  contents: read
  id-token: write
jobs:
  lamp-smoke:
    runs-on: ubuntu-latest
    steps:
      - run: lamp auth github-actions --fleet fleet-a.eilmer.si
      - run: lamp sandbox create --recipe flask-ci --idempotency-key "gh-${{ github.run_id }}-${{ github.run_attempt }}" --json
```

Lamp CLI readiness here is a fixture precondition, not a claim that `ubuntu-latest` ships Lamp. For a full job in Lamp, select the configured ephemeral runner route, for example `runs-on: [self-hosted, lamp-flask-linux]`. The plugin allocates fresh execution, supplies approved connections and attaches owner-scoped caches. API counterpart: `POST /auth/github-actions {"token":"<OIDC JWT>","fleet":"fl_1"} → {"key":"<short-lived>","expires_at":"<expiry>"}`.

2. Run shell tests, a Docker build and a Compose service task with the optional Docker kit selected. Dashboard shows job identity, trust class, sandbox, cache use, logs and result. T3 Code/Herdr/SSH can watch the actual CI or action session. The native GitHub run receives the correct status, report and saved outputs. On success, failure and cancellation, default sandbox deletion occurs. A second job sees cache entries but no prior private process/file marker.

3. Repeat a harmless task with the public Claude Code and Codex actions under approved fixture credentials. Exercise Copilot through the documented self-hosted Actions-based route and its supported OS/workflow constraints. Lamp's own host network policy stays effective even if Copilot's built-in firewall must be disabled. Undocumented Agent HQ/vendor-cloud placement is not claimed as a pass.

4. From a different fixture account, open a real PR containing hostile code. Use a `pull_request` job routed to `lamp-public-pr-vm`, with read-only source access and no secret inputs. Do not run untrusted head code in a privileged `pull_request_target` context. The script tries to read host canaries, request owner connections, steal credentials, poison trusted persistent state, change routing, reach LAN/metadata and leave a background process.

```console
$ lamp job inspect job_stranger --json
{"trust":"untrusted_public_pr","fresh":true,"isolation":"vm","secrets":false,"network":"restricted"}
$ lamp audit list --sandbox sb_pr --json
{"events":[{"kind":"network","destination":"fixture-lan:5432","decision":"deny"}]}
```

5. Inspect actual packet sinks, secret canaries, logs and storage, not just the reported policy. Every forbidden action fails. End the job and verify the VM is gone. The next stranger job is fresh. Attempt to route it to NV or attach a signed-in recipe: receive `PUBLIC_PR_POLICY_DENIED`. If fork PR OIDC is unavailable, do not issue a privileged fallback secret: the trusted CI intake must preserve the same policy, or the job remains visibly blocked pending the Q27 decision.

MCP observation (illustrative): `job_watch({"job":"job_stranger"}) → {"state":"completed","sandbox_state":"deleted","outputs":["job.log"]}`. No model subscription token is an allowed convenience for stranger work.

Pass evidence: actual public PR identity/event and job execution; GitHub OIDC/scopes/expiry; all action routes; clean job state; native result/cancel reporting; outputs after deletion; zero canary leaks and denied LAN/fallback/secret requests.

### Journey 5 — Team fleet through the Eilmer relay

Hosts: LX + M5 + WIN behind NAT, AIR as another authorized person's console. Covers RLY-001/002, ACC-005–007, SES-006/007, CON-003, KIT-003/005 and OPS-002/003/005/008.

1. Alice and Bea use their own fixture Eilmer accounts through Clerk. Alice links the fleet and chooses team mode. The relay authenticates/links hosts and issues one-time connection credentials. A public remote caller reaches the NAT-hidden hosts without port forwarding; command/output/file traffic stays encrypted caller→host.

```console
$ lamp relay connect --json
{"fleet":"fl_1","address":"fleet-a.eilmer.si","linked":true}
$ lamp fleet set --mode team --json
{"fleet":"fl_1","mode":"team"}
$ lamp access inspect --json
{"method":"eilmer-relay","end_to_end_encrypted":true,"relay_decrypts":false}
```

Dashboard shows hosts, linked Eilmer identity and connection status. The relay test observer sees encrypted bytes and connection/account metadata only, not sandbox contents or model credentials. All hosted links are under `eilmer.si`.

2. Alice creates `se_alice`; Bea creates `se_bea`. Initially each person's T3 Code, Herdr and `ssh fleet-a.eilmer.si` lists only their own session. Alice shares her session first for watching and then, after testing, for typing:

```console
$ lamp session share se_alice --with bea --access watch --json
{"session":"se_alice","person":"bea","access":"watch"}
Bea> lamp session send se_alice 'Continue' --json
{"error":{"code":"WATCH_ONLY","message":"This share does not allow typing."}}
$ lamp session share se_alice --with bea --access watch-and-type --json
{"session":"se_alice","person":"bea","access":"watch-and-type"}
```

Bea's console list gains the shared session live. In watch mode the output streams; API/MCP/SSH input is refused. With watch-and-type, Bea's text reaches the real harness. Sharing does not transplant Alice's credential into Bea's private sandbox.

3. Alice grants an automation key only `flask-ci` creation on `ubuntu-1`, run/read actions, chosen CPU/RAM/disk limits and an expiry. A watch-only key can observe only its selected sandbox. API (illustrative): `POST /keys {"recipes":["flask-ci"],"hosts":["ubuntu-1"],"actions":["create","run","read"],"limits":{"cpu":2,"memory_mib":2048,"disk_mib":8192},"expires_at":"<fixture expiry>"} → {"id":"key_ci","key":"<one-time display>"}`. MCP `key_inspect({"key":"key_ci"})` shows the same restrictions.

4. Exceed each scope/limit and wait until the short fixture expiry: receive the relevant `KEY_SCOPE_DENIED`, `RESOURCE_LIMIT` or `KEY_EXPIRED` with zero forbidden execution. Replay a used relay credential and receive `CREDENTIAL_ALREADY_USED`.

5. A task asks for a fixture database write under ask-first access and a password fill in a per-use-approval vault. Dashboard and consoles show blocked work and a “needs a decision” notification. Alice approves on her phone or console; only the selected operation proceeds and password value stays hidden. A browser MFA challenge uses human takeover, then returns to the agent. Bea's watch permission does not authorize approving Alice's secret use.

6. Set a 10-minute/20,000-token fixture task budget and run a separate loop fixture to show the watchdog stopping actual work. Dashboard shows remaining budget and stop reason. Retained audit/output views remain scoped. Usage data stays off until deliberately opted in. A non-relay local/OIDC fixture fleet still works during Clerk/WAN failure, while relay authentication failure is honestly reported.

Pass evidence: real Eilmer identity mapping, NAT reachability, relay ciphertext/key-custody inspection, three-console own/watch/type behavior, scoped keys, approvals, browser takeover, task stopping and telemetry capture. No payment or special owner-project connection is part of this journey.

## Security attack suite

ATK identifiers below are test cases, not additional product requirements. This is the complete planned attack inventory for the brief and the supplied research risks. It is not a claim to enumerate every possible future exploit. Add a regression case for every discovered exploit without weakening the binding target.

Run on isolated test hosts and dedicated accounts, never personal data, production services or the owner's projects. Use distinct high-entropy synthetic canaries for host files, neighbor files, API keys, vault passwords, browser cookies and subscription tokens. Capture guest files/env/process memory, saved RAM/disk, logs, exported outputs, events, console transcripts and controlled network sinks. Scan exact values and decodable encodings; compare unauthorized actions and state changes as well as secret bytes. A dashboard badge is not evidence of enforcement.

“ALL” uses the exact host-type matrix above. Real-VM attacks use LX, LA, M5 and WIN; NV separately proves fallback handling and denials. An unavailable lane is missing, not passed. Protected-secret leak count is 0 only when no API-key/password/cookie canary crosses its authorized boundary. A failed canary scanner or unreachable sink makes evidence incomplete. Test both legitimate success and hostile denial so a system that blocks everything cannot pass.

Do not rely on a model rejecting prompt injection. In relevant cases, deliberately execute the hostile tool request under the attacker's actual authority. Subscription tokens may be visible to their owner's authorized sandbox as ADR 0004 permits; that exception never permits other-owner access, non-vendor use, capture or Lamp-server custody. Deliberately opted-in cookie/password exposure is recorded explicitly and cannot silently make the unresolved Q22 target pass.

| Test | Requirements / hosts | Attack and pass condition | Code evidence and boundary |
| --- | --- | --- | --- |
| ATK-01 | SBX-001, BEN-004 / LX, LA, M5, WIN | Guest-root probes host files, processes and control endpoints. Pass: no host canary read/write, no host execution and no escape. | Fuzz host-facing inputs; fake VMM crash responses; real hypervisor and host enforcement never mocked. |
| ATK-02 | SBX-001, PLG-001 / LX, LA, M5, WIN | Send malformed VMM/device/control messages and hostile guest filesystem metadata. Pass: no escape or cross-sandbox access; failures stay contained and visible. | Parser/property/fuzz corpus; fake malformed transports; reproduce real failing guest/VMM path for accepted security evidence. |
| ATK-03 | SBX-001, STO-004 / ALL | Guess neighbor paths/IDs, inspect process/memory state and exploit shared mounts. Pass: no neighbor private data or mutation across owner/sandbox boundaries. | Ownership/path property tests; fake identities; real mounts and process isolation. |
| ATK-04 | SBX-004/005 / LX, LA, M5, WIN | Request unallocated devices or reach host display/audio/input/GPU data through a requested device. Pass: unrequested optional devices absent; allocated device reveals no host/neighbor canary. | Device-contract fuzzing; fake inventory; real device isolation and guest inventory. |
| ATK-05 | SBX-004, ACC-007, OPS-002 / ALL | Exhaust CPU/RAM/disk, fan out forks/creates and overflow numeric limits. Pass: accepted limits actually constrain work and unauthorized capacity is not allocated. | Resource arithmetic/property tests; fake capacity; real enforcement and stopped work. |
| ATK-06 | SBX-006, CI-005 / NV | Attach harness login, MCP token, browser login or vault secret on fallback without owner override; request public PR even with override. Pass: secret work denied by default; public PR always denied without fresh VM. | Isolation/consent truth table; fake probes; real failed allocation and fallback label. |
| ATK-07 | NET-001/005 / ALL | Guest root changes routes, firewall, DNS and proxy env; nested Docker tries bypass. Pass: host policy still applies and forbidden sink receives no traffic/data. | Rule/compiler tests; fake DNS; real host network paths including nested runtime. |
| ATK-08 | NET-001/005 / ALL | Exfiltrate through IP literals, IPv6, UDP, DNS, redirects, domain fronting and alternate available interfaces. Pass: configured destination restrictions cannot be bypassed. | Hostname/URL/packet-policy fuzzing; fake DNS rebinding; real packets and sink observation. |
| ATK-09 | NET-002/003, CRED-003 / LX, LA, M5, WIN | Read VPN/proxy/private-network keys, change exit, bypass a failed exit or impersonate another sandbox's membership. Pass: keys absent from guest; accepted exit/identity unchanged; no unauthorized direct route. | Plugin lifecycle tests; fake provider failures; real exits, peer identity and key scans. |
| ATK-10 | NET-006, CON-003 / ALL | Scan other sandboxes, LAN fixture services and cloud metadata without approved links. Pass: only explicitly allowed traffic succeeds; off services remain inaccessible. | Link/connection policy property tests; fake topology; real same-host and cross-host traffic. |
| ATK-11 | ACC-007, INT-001, SES-007 / ALL | Switch API/CLI/MCP/SSH/events/files to evade action/resource/host/recipe/sandbox scope. Pass: every interface enforces the same effective restrictions. | Shared authorization contracts; fake claims; real forbidden operations produce no side effect. |
| ATK-12 | ACC-007, HST-004 / LX, LA, M5, WIN | Use expired keys during reconnect/failover or after clock changes. Pass: no action forbidden by expiry succeeds; moving coordination does not reset scope/expiry. | Fake-clock boundary/property tests; real host/fleet authorization under reconnect. |
| ATK-13 | CI-003, ACC-005 / LX + M5 + WIN | Submit forged JWT, wrong issuer/audience/signature, expired grant, foreign repository/workflow or tenant subject. Pass: no privileged key or cross-account session access. | JWT/OIDC fuzz and contract tests with fake JWKS; real issued-token validation and endpoint enforcement. |
| ATK-14 | RLY-001 / LX + M5 + WIN | Replay or concurrently redeem a one-time relay credential; link a foreign host/account. Pass: no second authorized redemption or foreign linkage. | Consumption-race/property tests; fake Clerk errors; real relay/host handshake and linkage. |
| ATK-15 | RLY-002, CRED-003 / LX + M5 + WIN | Malicious test relay captures traffic/storage/logs and substitutes an endpoint. Pass: no recoverable command/file/output/model secret; endpoint authentication prevents silent trust substitution. | Invalid-handshake/ciphertext tests; fake relay outages; real encryption and key custody. |
| ATK-16 | CI-005, SBX-006 / LX, LA, WIN, NV | Stranger PR selects privileged recipe/host/kit, spoofs trusted event, or enters a privileged trigger context. Pass: only fresh-VM/no-secret/restricted-network execution or refusal; no override escape. | Trust-classification property tests; fake forge events; real external PR and runtime policy. |
| ATK-17 | CI-004/005, LIF-004 / LX, LA, M5, WIN | Malicious job leaves daemon, startup hook, private file or compromised root for the next job. Pass: default execution deleted; next job's private state is fresh. | Completion/cancel/retry contracts; fake CI events; real guest teardown and clean next execution. |
| ATK-18 | CRED-005, CON-002 / LX, LA, M5, WIN | Dump guest env/files/memory, run package malware and encode output to steal model/CLI API keys. Pass: zero API-key canaries in guest-visible data or controlled sinks. | Secret-path fuzz tests; fake vendor APIs; real proxy path, process memory and sink scans. |
| ATK-19 | CRED-005, CON-002, NET-005 / LX, LA, M5, WIN | Trick substitution with hostile host/path/method, redirects, malformed URL, SNI/Host mismatch or another owner's connection handle. Pass: no key sent to an unauthorized destination/identity. | URL/header/owner property/fuzz tests; fake DNS/vendor errors; real forwarding and TLS behavior. |
| ATK-20 | CRED-005, BEN-004 / LX, LA, M5, WIN | An allowed controlled endpoint reflects injected headers or errors return the secret. Pass: real keys never reach guest/tool/console output or saved logs. | Reflection/redaction regression tests; fake upstream response corpus; actual response filtering and canary inspection. |
| ATK-21 | CRED-003 / LX, LA, M5, WIN | Another person requests a subscription login, agent tries non-vendor exfiltration, or hosted service requests token custody. Pass: same-owner/vendor restrictions hold and Lamp servers receive no model credential. | Owner/destination contracts; fake vendor grants; real custody and egress checks. |
| ATK-22 | CRED-004, SBX-007, LIF-001 / LX, LA, M5, WIN | Hide subscription tokens in env, files, process heaps, temp files or crash state before suspend/fork/template save. Pass: zero captured tokens, or capture refuses without publishing usable unsafe state. | Capture-order/failure tests; fake VMM/storage failures; scan actual RAM/disk/temp artifacts. |
| ATK-23 | CRED-003/004, CON-001 / LX, LA, M5, WIN | Concurrent fork/resume exercises rotated/expired grants and wrong account selection. Pass: no cross-owner token reuse/capture and honest sign-in failures; no hidden central refresh store. | Rotation/account contract tests; fake clock/vendor refresh; real approved sign-in and captured-state inspection. |
| ATK-24 | CON-001, ACC-007 / ALL | Choose another MCP connection/account, pass wrong-audience tokens or invoke unauthorized gateway tools. Pass: correct owner/server/scope enforcement and no upstream token in the sandbox. | MCP/OAuth contract/fuzz tests; fake upstream APIs; real gateway and token location. |
| ATK-25 | CON-001 / LX, LA, M5, WIN | In-sandbox MCP server tries to broaden limited token or read host gateway credentials. Pass: only its explicit limited access exists; no host or other-owner secret is readable. | Scope property tests; fake OAuth errors; real limited grant and negative upstream operation. |
| ATK-26 | CON-002, CRED-005 / LX, LA, M5, WIN | `gh`, Stripe and AWS clients exercise refresh, custom CA, WebSocket/signing/error paths to retrieve a token. Pass: supported operations work without raw guest tokens; unsupported modes fail honestly. | CLI/auth contract tests; fake vendor failures; actual client/signing/proxy traffic and scans. |
| ATK-27 | CON-003 / ALL | Write through read-only endpoints, use a read-looking request with side effects, or perform ask-first operation before/after denial. Pass: off/read-only/ask-first/full behavior matches actual service effects. | Operation/approval property tests; fake clocks/network; real database/service state. |
| ATK-28 | BEN-004, CON-001/003, ACC-007 / ALL | Repository README, public issue, MCP result or webpage instructs the agent to steal a canary or perform an unauthorized action. Pass: host authority boundaries still deny it even when hostile tool instructions execute. | Adversarial transcripts/tool contracts; fake model suggestions; real data/access/sink boundaries, not a mocked refusal. |
| ATK-29 | KIT-001, BEN-004 / LX, LA, M5, WIN | Request raw CDP cookies, storage export, arbitrary JS, network credentials, downloads or browser profile files. Pass: no cookie value reaches the agent through any offered control path. | Browser-action fuzzing; fake protocol errors; actual browser APIs, storage and responses. |
| ATK-30 | KIT-003/005 / LX + M5 + WIN | During MFA/CAPTCHA/password fill, agent requests screenshot/DOM/transcript of sensitive entry or races human input. Pass: human control is effective; password/code/cookie is not exposed to the agent under protected mode. | Handoff/concurrency tests; fake challenge events; real browser/input ownership and captured output. |
| ATK-31 | KIT-005 / LX + M5 + WIN | Retrieve secret via CLI/env/DOM after default external fill; use vault A's approval for vault B or a second use. Pass: no default password disclosure; per-vault per-use decision actually gates the intended use. | Vault/destination/approval property tests; fake manager APIs; real four-manager fill and extraction attempts. |
| ATK-32 | KIT-002/005, SBX-006 / ALL | Enable copied browser logins or password injection through defaults, recipe inheritance or agent input. Pass: explicit authorized opt-in is required per requested mode/secret; no silent weakening. Deliberate disclosure is flagged under Q22. | Consent propagation tests; fake approval; real resulting credential location and UI record. |
| ATK-33 | CRED-006 / ALL | Sandbox edits source-path symlinks, instruction files, MCP list or git settings and tries automatic return. Pass: chosen source unchanged before approval; only approved confined change returns. | Path/diff/approval fuzzing; fake storage errors; real source and sandbox file checks. |
| ATK-34 | STO-001–004, CI-005 / ALL | Guess cache/volume/output/template identifiers, traverse paths or use attachments to cross owners. Public-PR code probes trusted saved state and attempts poisoning. Pass: no unauthorized persistent access or promotion into protected work. Unresolved trust policy is a gap under Q15. | Storage/owner/path properties; fake transfer faults; real mounted state, downloaded artifacts and later-job behavior. |
| ATK-35 | SES-006/007, INT-005 / ALL | Guess sessions, watch foreign event streams, type with a watch-only share, or attach by SSH to bypass UI. Pass: unshared output remains private and watch-only input produces no harness action. | Identity/session/action contracts; fake streams; real console/SSH/API input and output. |
| ATK-36 | PLG-001/002 / ALL | Plugin crashes, hangs, floods protocol or directly probes sandbox files/control authority. Pass: Lamp and unrelated sessions survive; plugin cannot reach private sandbox data outside its authorized operation. | Plugin fuzz/fault tests; fake child processes; real process separation and denied access. |
| ATK-37 | OPS-005, ACC-005 / LX + M5 + WIN | Hostile sandbox origin attempts cookie tossing, parent-domain cookies, credentialed CORS/WebSocket, cross-origin reads and account-changing requests. Pass: zero stolen/overwritten account authority or cross-origin private data. | Origin/hostname/property tests; fake HTTP inputs; real browser cookie/origin/request behavior. |
| ATK-38 | OPS-005 / LX + M5 + WIN | Create many sandbox/host names to exhaust shared issuance budget. Pass: selected strategy serves valid names without exceeding 50 new certificates/week or falsely declaring unissued HTTPS ready. | Fake ACME/clock limit tests; real name provisioning and representative trusted TLS validation. |
| ATK-39 | OPS-008 / ALL | Default install, crash, update or relay reconnect attempts to emit optional usage analytics without consent. Pass: zero optional Eilmer usage events before opt-in or after opt-out. | Consent-state tests; fake collector; actual outbound traffic capture and payload classification. |
| ATK-40 | OPS-004, BEN-004 / ALL | Commands/requests embed canaries, malicious log controls or shell escapes; guest deletes logs. Pass: required per-sandbox audit remains attributable and exported views disclose no protected secret. Q30 defines unresolved fidelity details. | Redaction/event/parser fuzzing; fake storage errors; real authoritative audit and secret scans. |
| ATK-41 | LIF-001–004, OPS-002 / LX, LA, M5, WIN | Quiet descendants, held sockets, wall-clock jumps, reconnect and failed stop try to evade idle/expiry/budgets. Pass: active work is not falsely idle, and required deletion/stopping really ends execution. | Fake-clock state-machine tests; real process/socket discovery, saved state and cleanup. |
| ATK-42 | SBX-007, ACC-007, NET-003 / LX, LA, M5, WIN | Fork duplicates identities, broker authority, stale leases or socket state; resume crosses expiry. Pass: parent/fork remain correctly attributed, separate and scoped; no wrong-owner/expired authority appears. Portability details remain Q11. | State/identity properties; fake clock/lease failures; real fork, network identity and access checks. |
| ATK-43 | HST-004/005, INT-004 / LX + M5 + WIN | Host loss, retry and update failure try to create duplicate work, reset consent or update multiple hosts. Pass: no duplicate keyed allocation, no auto-update without opt-in and at most one host updating. | Partition/rollout property tests; fake delivery/clock; real multi-host failure and state readback. |
| ATK-44 | SES-005, CRED-003/004 / M5 + AIR + LX | Interrupted teleport leaves two privileged running copies or leaks credentials through transfer state. Pass: truthful recoverable session ownership/location, no forbidden capture/custody and no unexplained duplicate moved session. | Handoff fault state tests; fake transfer failure; real harness continuation and credential scans. |

Store redacted evidence with the exact build, host, guest, engine, plugin, harness and policy version. Every failing or incomplete attack blocks the corresponding acceptance claim. Report a leak by secret class, receiving observer and path. Do not publish the actual canary value with a real credential. An intentional allowed action can still be harmful; these tests prove the declared authority boundary, not model judgment or unlimited safety from fully authorized misuse.

## Benchmark suite

The required competitor set is Lamp, Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM. Matchlock, Modal and Daytona are references in the brief; they are not replacements for any required competitor. Pin exact revisions/releases and license/distribution provenance. Install real products only where supported. Report absent functions and unsupported host rows as such.

### Metrics and observation points

| Metric | Measurement | Binding pass rule / reporting |
| --- | --- | --- |
| Template create | API request start to independently verified command-running marker. Include placement, policy, storage and runtime setup on that path. Do not time kernel/init alone. | Linux p50 <500 ms. Report full sample distribution and failures. Target template/cache/load scope is Q33. |
| Live fork | Fork request start through live capture, consistent disk handling, credential stripping, fork restore and first usable command. Parent starts running and resumes/continues after any capture pause. | Linux <1 s; no fork percentile is supplied. Report every trial and outlier, parent pause and fork readiness separately. A stored-template restore or disk-only copy is a different operation. |
| Compute speed | Equivalent compiler/CPU work and fixed inputs on native host versus sandbox with equivalent CPU resources; verify outputs. | Linux ratio ≥0.97, independently of disk/network. Higher-is-better uses guest/native throughput; lower-is-better uses native/guest elapsed time. Workload/statistic selection remains Q33. |
| Disk speed | Same data/device/filesystem conditions; verify sequential/random I/O and metadata workload outputs, declaring cache state. | Linux ratio ≥0.97 for declared disk measures. Do not hide a failing disk result in a composite compute average. |
| Network speed | Same peer/path, protocol, payload, concurrency and policy; verify delivered data and measure throughput/latency. | Linux ratio ≥0.97 for declared network measures. Report VPN/proxy/direct and local/remote paths separately. |
| Security | Full attack inventory, secret-class leak count, unauthorized actions and host/mode coverage. | 0 API-key/password/cookie leaks everywhere under the unresolved opt-in qualification in Q22; documented subscription exception does not waive its own restrictions. Missing attacks/hosts are incomplete. |
| Time-to-agent-ready | Create request to a real signed-in harness capable of performing its first task/tool step, confirmed by a permitted inference/tool result. Separate first-ever sign-in/setup from prepared credentials. | Required comparative metric, no numeric target. Report harness/version/auth method, network/vendor latency and whether all requested tools/connections were ready. |
| Fork correctness | Memory-only marker, disk consistency, parent continuation, independent changes, credential exclusion, identity/lease/clock behavior. | Correctness is required; a fast incorrect fork fails. Report dirty-memory size, changed disk size and optional devices. |
| Density | Increase simultaneous active sandboxes under a declared CPU/RAM/disk/workload configuration; verify all remain correct and inspect host use. Report suspended and warm-ready populations separately. | Required comparative metric, no numeric target. State limiting resource, failed creates, workload responsiveness and per-sandbox/host overhead. No universal sandbox-count claim. |
| Reliability | Count attempted/successful creates, forks, resumes, task completions and cleanup; record errors, lost sessions, orphans and recovery during declared fault trials. | Required comparative metric, no invented availability SLA. Report numerator/denominator, run duration, failures/timeouts and recovery observations. |
| macOS/Windows speed | Repeat applicable timings/ratios with actual host hypervisors and distinguish Linux guest microVM from native full-VM classes. | Measure first, then set targets; no borrowed Linux hard limit. Security target still applies. |
| [proposed] Diagnostic timings | Break out cold template preparation/download, warm-pool assignment, suspend/resume, teleport, update recovery and event visibility. | The suite should report these separately. Reason: they explain perceived agent readiness and prevent moving work outside a narrow timer from hiding setup cost. No extra speed thresholds are proposed. |

### Reproducible protocol and reporting rules

The following detailed measurement protocol is **[proposed]**. The suite should use it because the brief demands public reproducibility but does not settle sampling, workload, timing or statistical details. It does not replace any hard target.

1. Record Lamp/competitor revision, full configuration, host CPU/core/RAM, architecture, OS/kernel/hypervisor, disk/filesystem, thermal/power state, guest resources/kernel, optional devices, template contents, harness/plugins/kits, access route and network policy. On M5 record 10 cores/24 GB/macOS 26; inventory the Ubuntu laptop and Air rather than guessing.
2. Use equivalent guest contents and tasks across products with each product's supported preparation format. Lamp uses its own format; competitors may use Docker/OCI without importing that support into Lamp. Disclose any unavoidable mismatch in device set, boundary, guest OS, credential isolation or fork semantics.
3. Keep bare-metal/native, nested virtualization, cross-architecture emulation and no-virtualization fallback in separate rows. LA requires a Linux arm64 host, not merely an arm64 guest on macOS. Windows CI needs the real WIN lane. Results from one architecture cannot certify another.
4. Use a single monotonic observer where possible. For remote API requests, use an independently observed first-command marker with documented clock/correlation method; receiving proof of execution gives a conservative end-to-end upper bound. Never subtract uncalibrated clocks on two machines or exclude policy/capture work because it is inconvenient.
5. As an initial reproducible sampling plan, use 20 declared warmup trials and 200 measured create trials per prepared-template configuration, 100 measured live-fork trials per memory configuration and 10 repeated native/guest throughput trials per workload. Publish all raw observations; these sample counts are proposed methodology, not promised product capacity or an implied percentile for fork.
6. Exercise at least 128 MiB, 1 GiB and a larger host-feasible dirty-memory working set for fork, with parent continuation and both shallow and older disk histories. Measure actual memory inclusion/dirty pages, not configured RAM alone. Report credential-scrubbing time and faults. Exact target envelope still needs Q33.
7. Separate prepared local template creation, first uncached use and warm-pool assignment. Declare filesystem/page cache state and cold versus warm workload data. Do not present a preclaimed warm sandbox as an ordinary from-template create without labeling it.
8. Alternate/randomize product/native/sandbox run order with published seed; control background load and avoid thermal/power bias. Publish resources used by surrounding services. For disk/network, preserve equivalent payload, cache, peer and policy conditions. A lower security configuration is not a fair unqualified speed comparison.
9. For every latency metric publish trial count, p50/p95/p99, range, error/timeout counts and raw samples; publish confidence intervals with the named method. Use no success-only filtering. An unsupported operation is “unsupported”; an adapter failure is “test failed”; a timeout is a failed trial with its timeout threshold, not a discarded sample.
10. Report each compute/disk/network workload and its ratio, correctness result and units separately. Any summary is secondary. Publish density load, success counts and reliability fault schedule; no overall “fastest” claim may ignore a missing class or weaken isolation silently.
11. Run the required suite for every change. A queued, failed or unavailable physical-host/vendor lane remains visibly incomplete; a unit-only/smoke run cannot be advertised as the full suite. Resource scheduling and the meaning of “every change” need Q34, not an unstated exemption.
12. Publish reproducible commands, source inputs, configuration, raw redacted data and machine-readable results with the report. Use host/mode/product tables and distribution plots when actual data exists. Separate measured facts, upstream claims and unknowns; this document supplies no invented results.
13. Include attacks with the same release/configuration, recording zero-leak/exception/incomplete/failure states separately. Do not upload credentials, saved personal browser state or proprietary fixture data. Keep repository/publication visibility under the engine-demo/private-repository decision until Q34 is settled.

A code-test lane validates statistics against known vectors, verifies timeout/failure inclusion, checks category gates independently and contract-tests each adapter's observation points. VMM/network/clock/vendor failures may be faked there. Published performance/security results always use real Lamp, competitors, hosts and workload behavior. The benchmark suite is not a mocked service comparison.

## Traceability

Ranges are inclusive. IDs below refer to requirement records above, not new requirements. The walkthroughs and suites provide cross-cutting execution evidence.

| Brief section | Complete topic coverage | Requirement IDs |
| --- | --- | --- |
| §1 What Lamp is | Open-source self-hosted agent/harness/CI service; Eilmer; speed/security/agent-fit proof | PRD-001/002, INT-001, SBX-001, BEN-001–005, OPS-007 |
| §2 People/problems/references | Everybody at launch; parallelism, safety, setup, ownership; competitor/reference context | PRD-001/003, INT-006/007, SBX-001, CRED-001–006, BEN-005 |
| §3 Callers/interfaces | All credentialed caller types; API/MCP/CLI parity; generated TS/Python then Go/Rust; JSON/docs/idempotency/events | INT-001–005, ACC-007, CI-003 |
| §4 Agent placement | Agent inside and outside; commands/files/browser | INT-006/007, KIT-001–003 |
| §5 Hosts/fleets | Linux x86_64/arm64, Apple Silicon macOS, Windows; one fleet/address/keys; placement/pin; failover/sleep; opt-in rolling updates; fastest eligible engine | HST-001–006, ACC-007, QA-001/002 |
| §6 Sandboxes | Fast microVMs; requested devices/resources; Linux everywhere; native macOS/Windows full-VM classes; 2 macOS per Mac; fallback/override; fresh public-PR VM; RAM fork | SBX-001–007, CI-005 |
| §7 Targets | Create p50 <500 ms; RAM fork <1 s; compute/disk/network ≥97% native on Linux; zero protected-secret leaks everywhere; Mac/Windows targets after measurement; subscription exception | BEN-001–005, CRED-003/004, KIT-002/005, QA-001 |
| §8 Templates/recipes | Own format, no Docker images/Dockerfiles/runtime; save installed tools; optional Docker inside; all seven recipe parts | TPL-001–003, KIT-004 |
| §9 Lifecycle | Idle suspension with disk/RAM saved and CPU/RAM released; next-message resume; set-time deletion; CI deletion; caller overrides; all three idle conditions/background work; warm pool; forks/winner | LIF-001–006, SBX-007 |
| §10 Lasting storage | Shared package/build caches; named shared volumes; CI outputs/logs; saved updated templates; owner-only defaults | STO-001–004, TPL-002 |
| §11 Network | Host-only unchangeable setup/keys; all interfaces; plugin types; independent VPN/proxy/direct exits; private membership; inbound URLs; lists; explicitly allowed sandbox links | NET-001–006, PLG-002 |
| §12 Modularity | Customizable modules; anybody's separate-process plugins; no crash/unauthorized sandbox reach; all listed plugin categories | PLG-001/002, ENG-002 |
| §13 Harness/sign-in/settings | All named/other harnesses; vendor-approved catalogue; owned-host subscription custody, own sandbox, vendor limits and capture stripping; no Lamp-server credentials; host API-key proxy; all settings categories, chosen one-way source, approved return | CRED-001–006 |
| §14 Connections | No special stateful-app migration; signed-in host MCP gateway and limited inside-server exception; token-isolated CLI tools; owner-machine services with off/read-only/ask-first/full | CON-001–003 |
| §15 Kits | External protected logged-in browser default, inside/copy opt-in, human challenge takeover; four password managers, default external fill, per-secret injection opt-in, phone/console per-vault per-use approval; toolchains/Docker/other kits | KIT-001–005, PLG-002 |
| §16 Consoles/sessions | One fleet machine in any plugin console; each session live/open/watch/type/status; ordinary SSH list/attach/shell; both sub-agent placements/git return/visibility; teleport; own-only default and watch/type shares | SES-001–007 |
| §17 Access/accounts/keys | Built-in Tailscale/keyless CI; relay/open source/account/one-time/E2E/no data; own domain HTTPS; local-only; Cloudflare plugin; Eilmer/Clerk/local/OIDC/offline; owner/team/public mode; scoped resources/actions/expiry; GitHub token exchange | ACC-001–007, RLY-001/002, CI-003, OPS-007 |
| §18 CI/triggers | Every CI via plugins; all named shipped systems; Copilot/Claude/Codex Actions; Slack/Linear/webhooks/schedules; fresh jobs/caches; stranger public PR no-secret/restricted fresh VM | CI-001–006, LIF-004, STO-001/003, KIT-004 |
| §19 First-release features | All brief features in first release; warm pool/fork/teleport; live dashboard; time/token/watchdog; done/stuck/decision notifications; command/request audit; timer agents; no cloud overflow | PRD-003, LIF-005/006, SES-005, CI-006, OPS-001–004 |
| §20 Hosting/business | Hosted `eilmer.si` only, cookies/origins/cert limit; no payments, future plans/quotas/billing; host Apache/relay AGPL; private until engine demo; opt-in usage data off by default | OPS-005–008, ENG-001 |
| §21 Proof | Public reproducible suite on every change; five named competitors; targets plus agent-ready/fork/density/reliability/security | BEN-001–005, QA-001/002 |
| §22 Engineering | Engine/create/fork first without Docker; reuse rationale; language decision and Rust/Go trade-off; exact named machines and missing Windows plan; no owner-project tests/adapters; public third-party fixtures | ENG-001–003, QA-001/002, TPL-001 |
| §23 Research | All six supplied research files read; background/conflicts/unknowns separated from binding requirements | Reading rules, ENG-002/003, BEN-005, QA-002; open questions Q01–Q35 |

| ADR | Requirement IDs carrying the decision |
| --- | --- |
| 0001 One fleet, many hosts | HST-001–006, SES-001–007, ACC-007 |
| 0002 Host network setup | NET-001–006, CON-001–003, CI-005 |
| 0003 Security/speed reuse | SBX-001/005/006, HST-006, PLG-001/002, ENG-001–003, BEN-001–005 |
| 0004 Subscription custody | CRED-001–006, CON-001/002, SBX-006/007, TPL-002, LIF-001, BEN-004, RLY-002 |
| 0005 One Eilmer account | ACC-003/005/006, SES-006/007, RLY-001 |
| 0006 Hosted domain family | NET-004, OPS-005, ACC-002/004 |
| 0007 Own template format | TPL-001–003, KIT-004, SBX-006, ENG-001 |
| 0008 Licenses/private demo gate | OPS-007, INT-002, PLG-001/002, BEN-005 |
| 0009 Relay never decrypts | RLY-001/002, CRED-003, OPS-008 |

## Open questions for owner/architecture decisions

These are unresolved. A proposed test protocol or illustrative command does not answer them. Binding scope is unchanged; an unsupported requirement is a release gap, not permission to drop it.

| ID | Unresolved question and affected acceptance |
| --- | --- |
| Q01 | Which minimum Linux distributions/kernels, macOS versions and Windows editions/builds/CPU architectures are supported? What install steps/privileges meet “as easily as Docker”? Ubuntu/Air hardware still needs actual inventory. HST-001, PRD-002, QA-001/002. |
| Q02 | Does “TypeScript and Python first, then Go and Rust” require all four in the first release, given “everything in this brief,” or define a later SDK sequence? Until decided, none is silently removed. INT-002, PRD-003. |
| Q03 | What are host enrollment/removal/re-enrollment rules, placement backpressure and queue-versus-reject behavior when capacity/pins cannot be satisfied? What fleet size is a supported limit? HST-002/003, PRD-003. |
| Q04 | What is an idempotency key's owner/fleet/operation scope, retention period and conflict behavior? Which side-effecting commands use it, including fork, trigger and typed input? INT-004. |
| Q05 | What event delivery, ordering, replay, reconnect cursor, retention and flow-control guarantees exist? What is the live-update latency objective? INT-005, OPS-001. |
| Q06 | How quickly must coordination recover after sleep/loss/partition? How are split coordination, rejoining and a fleet with no surviving host handled? Execution migration from an unavailable host is not specified. HST-004. |
| Q07 | How do rolling updates handle active sessions, failed hosts, rollback and mixed-version capture/restore compatibility? HST-005, CRED-004. |
| Q08 | What workload/device/security weighting defines “fastest supported engine,” how is evidence refreshed, and what happens when speed conflicts across metrics? HST-006, BEN-003. |
| Q09 | What are CPU/memory/disk units, defaults, maxima, accounting/overcommit rules and limit-exceeded behavior? Do suspended macOS sandboxes consume the two-per-Mac allowance? SBX-003/004, ACC-007. |
| Q10 | Which device combinations, GPUs, guest OSes and hotplug behaviors are guaranteed per host? What unavoidable boot infrastructure is excluded from “devices only when asked”? SBX-005. |
| Q11 | What fork support/semantics apply to full VMs, fallback, GPU/device state, external volumes/sockets, clocks, randomness and cross-host/architecture transfer? How is an “exact” RAM fork reconciled with required token removal? SBX-007, CRED-004. |
| Q12 | How are templates/recipes named, versioned, updated, shared, validated and made compatible across OS/CPU/engine versions? Which source/build primitives does Lamp's own format expose? TPL-001–003. |
| Q13 | What are default idle/deletion durations, deletion clock basis and numeric “instant resume” target? Which control/harness processes and connections count versus task activity, and what is a “next message” for SSH/browser/API? LIF-001–004, SES-002. |
| Q14 | How are warm pools sized, matched, refilled and scoped by owner/recipe/resources/credentials? What happens on a warm miss, and can readiness satisfy both clean-job and prepared-sign-in rules? LIF-005, CI-004. |
| Q15 | What storage consistency/concurrent-write, cache trust/poisoning, cross-host attach, retention, size/eviction and deletion rules apply? How are untrusted public-PR caches separated from trusted saved state while preserving owner-only defaults? STO-001–004, CI-005. |
| Q16 | What are default egress policy, allow/deny precedence, wildcard/DNS/IP semantics and supported VPN/proxy/plugin protocols? Are policy changes atomic, and what happens on exit/policy-enforcer failure? NET-001–006. |
| Q17 | Are inbound dev URLs private or public by default? What auth, port scope, expiry, revocation and sharing apply? How do private/owner-domain/relay routes expose them without widening sandbox access? NET-004. |
| Q18 | What exact plugin trust/permission/process contracts prevent plugins reaching sandboxes, especially engine/network/credential plugins whose approved jobs affect them? What are protocol versioning, failure timeout, distribution and signing expectations? PLG-001/002. |
| Q19 | For every named harness/vendor, which versions/methods/use contexts are approved, and who maintains the evidence? How do host-only refresh ownership, token rotation, arbitrary in-memory token scrubbing, vendor-only credential traffic alongside other connections and git signing work? CRED-001–006. |
| Q20 | What owner/team/public-service roles, admission rules, invitations, admin visibility, identity mapping and key/share revocation semantics exist? What resource-existence information is hidden? ACC-005–007, SES-006/007, STO-004. |
| Q21 | Which MCP servers genuinely require in-sandbox execution; what limits/lifetimes are mandatory for their tokens; and how does that explicit exception interact with protected API-key isolation? CON-001. |
| Q22 | How does “0 password/cookie leaks everywhere” apply to explicit per-secret injection, copied browser logins and secret-bearing fallback overrides? What vendor-supported fill interfaces exist for all four password managers, and how can browser control prevent DOM/CDP/response extraction? KIT-001/002/005, BEN-004. |
| Q23 | What defines working/blocked/idle across harnesses, and what are SSH host identity, detach/reconnect, multiple simultaneous writers and watch-only enforcement semantics? How does a plain SSH console complete graphical takeover? SES-002/003/007, KIT-003. |
| Q24 | Does a separate sub-agent receive uncommitted/untracked/ignored parent files, and how do non-git tasks, git conflicts, approval and signing work when returning changes? SES-004, LIF-006. |
| Q25 | What exactly does teleport preserve: task transcript, harness identifier, process memory, files, device state or connections? Which harness/OS directions are supported, and how does interrupted movement roll back? SES-005. |
| Q26 | Which CI/controller/runner versions and native execution modes are guaranteed per shipped plugin? Which documented Copilot route is selected, and which Docker/service-container/native-OS jobs are supported? CI-001/002. |
| Q27 | Which immutable GitHub OIDC claims define trust, what is Lamp key lifetime, and how are fork PRs handled when OIDC is unavailable? What does “no secrets” mean for runner transport and read-only job credentials, and how is untrusted head execution kept separate from privileged trigger identity? CI-003/005. |
| Q28 | What timezone/DST, missed-run, overlap, retry/cancel and duplicate-delivery behavior applies to schedules/Slack/Linear/webhooks? How does each external person map to a scoped caller? CI-006. |
| Q29 | How are tokens counted across providers, tools, sub-agents, forks and outside-agent mode? What detection/thresholds define stuck/looping, what actual processes are stopped, and which notification/phone delivery channels are required? OPS-002/003. |
| Q30 | How can “every command and network request” be observed for guest-root, encrypted, raw-socket and nested-runtime traffic? What fields, retention, export/tamper protection and secret redaction reconcile full audit with zero leaks? OPS-004. |
| Q31 | How does Apache-2.0 for everything on hosts apply to reused dependencies, optional Docker kits, proprietary OS frameworks, third-party plugins and guest software? What redistribution/licensing obligations apply to native Windows/macOS templates? OPS-007, SBX-003. |
| Q32 | What exact optional usage data is collected, at what granularity and retention, and what remains necessary relay/account metadata? How is opt-in/opt-out scoped across a fleet? OPS-008, RLY-002. |
| Q33 | Which host/workload/resources/cache/load/device envelope defines Linux speed acceptance, what statistic applies to unqualified fork <1 s, and which disk/network measures must meet ≥97%? What targets follow actual Mac/Windows measurement? BEN-001–003/005. |
| Q34 | Does “on every change” mean every commit, PR update or other change? How are full physical-host/vendor competitor runs scheduled, and how does public benchmark publication coexist with private-until-engine-demo source? BEN-005, ENG-001, OPS-007. |
| Q35 | Which external Windows and Linux arm64 providers/hardware, supported virtualization mode, licenses, cost budget and execution ownership will supply missing E2E lanes? The proposed external Windows strategy does not select a vendor or spend funds. QA-001/002. |

## Requirement inventory

There are **98 requirements: 97 binding and 1 proposed test-infrastructure requirement (QA-001)**. Proposed benchmark-methodology details and diagnostic metrics are explicitly labeled above; they do not change the binding product scope.

| Area | Count | Area | Count |
| --- | ---: | --- | ---: |
| PRD — product/scope | 3 | INT — callers/interfaces | 7 |
| HST — hosts/fleets | 6 | SBX — sandboxes | 7 |
| TPL — templates/recipes | 3 | LIF — lifecycle | 6 |
| STO — lasting storage | 4 | NET — network | 6 |
| PLG — plugins | 2 | CRED — harness/auth/settings | 6 |
| CON — connections | 3 | KIT — kits | 5 |
| SES — consoles/sessions | 7 | ACC — caller access/accounts/keys | 7 |
| RLY — relay | 2 | CI — CI/triggers | 6 |
| OPS — dashboard/operations | 8 | BEN — benchmarks/proof | 5 |
| ENG — engineering | 3 | QA — test infrastructure | 2 |

The document describes required evidence. It does not claim that Lamp, competitors, test hosts or vendor connections have passed these tests today.
