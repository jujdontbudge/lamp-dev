# Lamp requirements

Feature requirements · revised 2026-11-10 under the owner’s instruction to build features with almost no new tests.

Lamp is open-source software that people install on their own machines. It creates isolated computers, called sandboxes, for AI agents, harnesses (the programs that run agents) and continuous integration (CI). One fleet can contain one host or many. Callers use one address and one set of keys. They can use an application programming interface (API), command-line interface (CLI) or Model Context Protocol (MCP) server, or watch and join sessions through a dashboard, T3 Code, Herdr or Secure Shell (SSH).

Lamp serves four equal needs: parallel work without collisions, protection of the host and secrets, ready-to-use tools and sign-in, and ownership of the machines and data. The first release includes the entire owner brief under its accepted release timing: a TypeScript software development kit (SDK) ships then, and Go/Rust follow immediately (the Python SDK was dropped by the owner, 2026-11-12). Hosts are Linux x86_64 and arm64, and Apple Silicon Macs with macOS 26 or newer. Linux guests run on both host families. Native macOS guests use a clearly labeled slower full virtual machine (VM) class. Lamp uses its own template format. Docker is available inside a sandbox as a kit, never as Lamp's runtime or template format.

The owner can run everything locally. Eilmer's optional relay only forwards end-to-end encrypted traffic. It never holds model credentials or sandbox data. All Eilmer-hosted addresses live under eilmer.si. API keys stay in host brokers. Subscription logins stay on the person's trusted hosts and own sandboxes. Their presence in a same-owner memory fork or suspend is an explicit exception to the zero-secret-leak target. Templates and anything shared or exported start the harness fresh.

This document describes product behavior and visible failures for owners and implementers. It does not claim implementation status. Complete a feature ticket with one real end-to-end demonstration covering its goal, using almost no new tests. Release measurements never block work, merge or release. The binding sources are [the brief](brief.md), [the glossary](../../GLOSSARY.md), and [Architectural Decision Records (ADRs) 0001–0009](../adr/). The merged [architecture](architecture.md), especially §§11–12, supplies canonical operations and fields. Accepted O1–O10, A1–A5 and B1–B4 are part of the brief. Requirements that depend on B1–B4 are marked `[owner-confirmed Bx]`.

## Five user journeys

### 1. Start useful work on one machine

Alice installs Lamp on her laptop, chooses her tools and sign-in method, and asks for a sandbox. She can let a harness work inside it or direct it from outside. She sees when setup is finished, watches the work in her console and answers when it needs her. A failed setup tells her what failed and keeps the result available to inspect.

### 2. Add machines and keep working when a laptop sleeps

Alice adds her Mac to the same fleet as her Linux laptop. Her console still shows one machine, with each piece of work listed separately. She chooses where work runs or lets Lamp find room. If a laptop sleeps, the other host can start work using capacity already reserved for it. Lamp tells her which saved files and sessions are still on the sleeping machine.

### 3. Try two approaches and keep the winner

Alice forks a running sandbox while it holds unfinished work. The original and its fork continue independently. She tries two approaches, compares their results and brings the chosen changes back through git. Private subscription work stays signed in on her trusted hosts. If a fork would copy an exposed password or login cookie, she must separately approve that child before Lamp copies it.

### 4. Run a fresh computer for each CI job

Alice connects a CI system and runs a job. Lamp starts a fresh sandbox, sends the result back and keeps the outputs available after cleanup. Every public-repository job uses a fresh virtual machine and an allowed list of network destinations. A stranger's job also gets the stricter secret rules. Alice may retain a trusted failed job in a stopped state for inspection; the next job still starts fresh.

### 5. Share work and move it between laptop and sandbox

Alice and Bea join the same fleet through the optional relay. They see their own work unless someone shares it. Alice can share a session for watching, or separately allow typing. When an action needs her decision, she sees the actual requested effect before approving it. She can privately take over a browser, then hand it back on a safe page. Moving a session onto her laptop needs separate permission to run there. Revoking access shows which machines have stopped and which are still waiting to expire.

### Everyday defaults

Linux sandboxes start with 2 virtual CPUs, 4 GiB of memory and 20 GiB of disk. Native macOS templates publish their own minimums and defaults. Macs run at most two native macOS sandboxes, counting suspended copies too. An eligible sandbox suspends after 300 seconds of inactivity and keeps saved state for 7 days. The task's default time budget is 1 hour, including suspension: before it expires, a message resumes the same task; afterward, the task stays stopped until an authorized caller creates a new task and budget to continue the saved work. The dashboard shows both countdowns. CI deletes execution when the job ends unless a trusted-job policy explicitly retains stopped state.

When virtualization is unavailable, Linux uses gVisor and macOS uses a restricted process with App Sandbox. Both are labeled weaker; public-repository CI always needs a fresh VM.

Development and the development relay use only the owner's Ubuntu laptop, M5 Mac and MacBook Air. Demonstrations use disposable state and public third-party projects. Report the actual hardware and whether a measurement is nested; do not present it as a native measurement.

## Abbreviations

| Term | Meaning |
| --- | --- |
| API / CLI / MCP | Application programming interface / command-line interface / Model Context Protocol: three ways to control the same Lamp features. |
| CI / SDK / LLM / TS | Continuous integration / software development kit / large language model / TypeScript. |
| VM / CPU / vCPU / RAM | Virtual machine / central processing unit / virtual CPU / random-access memory (working memory). Nested execution runs the Lamp host inside another VM. |
| MiB / GiB / KiB | Binary memory/storage units: 2^20 / 2^30 / 2^10 bytes. |
| JSON / NDJSON / SSE | JavaScript Object Notation / newline-delimited JSON / server-sent events. |
| SSH / TLS / HTTP / HTTPS | Secure Shell / Transport Layer Security / Hypertext Transfer Protocol / HTTP protected by TLS. |
| TTL / RPO / RSS | Time to live / recovery point objective (how much saved work can be lost) / resident memory use. |
| PR / OIDC / JWT / PKCE | Pull request / OpenID Connect / JSON Web Token / Proof Key for Code Exchange. |
| p50 | Median: the value at or below which half of observations fall. |
| CoW / PRNG / fsync | Copy-on-write / pseudorandom number generator / a filesystem synchronization call; durable saves use the per-OS barriers in the architecture. |
| ACL / RPC / IPC / ABI | Access-control list / remote procedure call / interprocess communication / application binary interface. |
| KVM / HVF / VZ | Linux Kernel-based Virtual Machine / Apple's Hypervisor and Virtualization frameworks. |
| HID / PCM / UID | Human Interface Device code / pulse-code modulation audio / Unix user ID. |
| CA / CAA / ACME / CT / TUF | Certificate authority / Certificate Authority Authorization / Automated Certificate Management Environment / Certificate Transparency / The Update Framework. |

Further security terms are explained where they first affect a requirement; exact wire shapes follow architecture §§11–12.

## Reading these requirements

Each record describes its behavior, source, interfaces, returned JSON and what a person sees. “Must” describes product behavior, not a demand for a test suite. The old proof-only QA-003 and QA-004 records are retired; their IDs remain in the historical rounds. The other IDs remain stable for feature references.

The owner’s 2026-11-10 decision supersedes earlier testing and performance process decisions: almost no new tests, one real end-to-end demonstration per ticket, and measurements taken once at release with honest publication. There are no experiment or certification prerequisites, compatibility case matrix, attack list, sample quotas or soak obligations. Add a test only when the feature cannot be shown working another way.

B1 retains the standard measurement size (2 vCPU, 4 GiB RAM, 20 GiB disk, up to 1 GiB recently written RAM), without a timing blocker. B2 permits only the CI system’s short-lived one-job token in public-PR jobs. B3 uses protected fill for standard forms and private human takeover when secrecy is uncertain. B4 records each process start, not script semantics. See [the brief](brief.md) for the owner’s decisions and attribution.

### Common interface contract

All examples use architecture §§11–12. IDs such as `sb_01` are schematic; callers use actual returned IDs. Object fragments show selected canonical fields, not complete resources. An API body shown beside a CLI command is also the MCP create input unless stated otherwise. For get/update/delete/extra MCP calls, add the addressed `<stem>_id`; HTTP uses its path and CLI its positional ID. Bodies never contain server-derived fields. Set `t0` from the authoritative clock and derive each expiry as `t0+ttl_seconds`. Values such as `<t0+60s>` are substitutions for an actual RFC3339 Coordinated Universal Time (UTC) timestamp, not literal inputs. Historical timestamps illustrate record structure only.

Give each new action a retry key. If the connection drops, retry with the same key and unchanged inputs. Before editing an existing object, send the version you read; Lamp rejects an outdated edit. The CLI and SDK handle key discovery for you.

In technical examples, every resource mutation includes this common context even when not repeated: a fresh `idempotency_key` (HTTP `Idempotency-Key`, CLI `--idempotency-key`) and, for update or dangerous extra action, `expected_revision` (HTTP `If-Match`, CLI `--expected-revision`). A retry uses the original key and identical body. The fixture obtains structured keys from authenticated discovery; it never guesses another operation's home. Caller-local `--input` files contain JSON; binary/content files are uploaded as Blobs. No MCP argument asks the host to read a caller-local path.

CLI JSON, API and MCP share envelopes:

```json
{"operation_id":"op_01","resource_ids":[{"kind":"sandbox","id":"sb_01"}],"request_id":"rq_01","replayed":false}
```

Wait through `lamp operation wait op_01 --timeout-ms 25000 --json`, `GET /v1/operations/op_01/wait?timeout_ms=25000`, or `operation_wait({"operation_id":"op_01","timeout_ms":25000})`. A timeout returns current state, not success. On completion inspect `operation.outcome.result`, such as `{"sandbox_id":"sb_01","session_ids":["se_01"]}`. Follow-up reads return `{"data":{...},"request_id":"rq_02"}`; lists return `{"items":[...],"next_cursor":"...","request_id":"rq_03"}`. Key, invitation, auth-exchange and share-link secret results are never persisted in operations/events/audit or request hashes; replay preserves the same Operation/resource/grant identity and reports `secret_available=false`. See the exact read/download exceptions below.

Errors use `{"error":{"code":"SCOPE_DENIED","message":"Action is outside this grant","retryable":false,"details":{}},"request_id":"rq_04"}`. Include `operation_id` when accepted work failed. Canonical codes and meanings are architecture §12.2. Unauthorized foreign IDs return `NOT_FOUND`. Schema failures use `INVALID_ARGUMENT`. CLI exits are 0 success, 2 input, 3 auth, 4 conflict, 5 unavailable, 6 failed/canceled operation. Exec's process exit is a separate `exit_code`.

Where a record says “operation result,” use the envelope above with that record's returned resource references. Where it says “read data,” wrap the shown JSON in `data`. This avoids inventing a separate output shape per requirement. Every CLI example requests `--json`; text UX can also show progress.

These errors have the same meanings across API, CLI and MCP. Expired results cannot be treated as fresh work.

| HTTP / code | Meaning |
| --- | --- |
| 400 `INVALID_ARGUMENT`; 401 `UNAUTHENTICATED`; 404 `NOT_FOUND` | Schema/identity/authorized visibility failure |
| 403 `SCOPE_DENIED`, `VM_REQUIRED`, `HOST_NOT_TRUSTED`, `APPROVAL_REQUIRED` | No effect; explain the required authority/boundary safely |
| 409 `REVISION_CONFLICT`, `IDEMPOTENCY_CONFLICT`, `RESOURCE_IN_USE`, `OUTCOME_UNKNOWN`, `TASK_AUTHORITY_EXPIRED` | Inspect current revision/original operation; never blindly rerun |
| 410 `RESULT_EXPIRED`, `KEY_EPOCH_EXPIRED`, `CURSOR_EXPIRED` | Retry safety/replay record cannot be reused as fresh work |
| 422 `ENGINE_UNQUALIFIED`, `DEVICE_UNAVAILABLE`, `DEVICE_NOT_FORKABLE`, `AUTH_TRANSPORT_UNSUPPORTED`, `CONTINUATION_UNSUPPORTED`, `CLEAN_REBUILD_REQUIRED`, `TOKEN_CAP_UNENFORCEABLE` | Requested workflow is unavailable; report it honestly |
| 429 `NO_CAPACITY`, `CONTROL_QUOTA_EXCEEDED`, `RATE_LIMITED` | Retry-After; no silent unbounded queue |
| 503 `HOST_UNAVAILABLE`, `DATA_UNAVAILABLE`, `QUORUM_REQUIRED`, `IDEMPOTENCY_HOME_UNAVAILABLE`, `TOKEN_HOME_UNAVAILABLE`, `AUDIT_UNAVAILABLE` | Explicit unavailable authority/data, not a fresh replacement |

### Common visual and console contract

“Consoles” in each record means all three: T3 Code, Herdr and stock SSH. T3 Code and Herdr configure one fleet machine, list each authorized Session, open/watch it, and type only with an input Lease. SSH lists with `ssh -p 22418 person@ssh.f123.fleet.eilmer.si sessions`, attaches with `ssh -t -p 22418 person@ssh.f123.fleet.eilmer.si attach se_01`, and opens a sandbox shell with `ssh -t -p 22418 person@ssh.f123.fleet.eilmer.si shell sb_01`. Actual AccessRoute supplies host/port; direct and Tailscale use port 22. These are ordinary OpenSSH commands with a pinned fleet SSH CA. They never open a host shell.

Each dashboard mutation has input validation, effective-policy preview, an Operation progress view, success detail and a retained failure with cancel/retry controls. Stale edits reload on `REVISION_CONFLICT`. Empty lists explain whether there are no resources or insufficient scope without exposing other owners. Stale observations show time and `observation_state`; silence is not completion. Console-only resources appear in session detail or a trusted dashboard link; SSH need not render graphical forms. All three must preserve the same IDs, status, scope and errors.

### Protected browser support boundary

A standard login form means a browser-rendered HTML username/password form on HTTPS, with an approved exact origin/frame/form action and trusted credential recipient. The adapter validates that target immediately before use and controls every offered agent observation afterward. A standard form is not safe merely because its input has `type=password`; changed actions, hostile same-origin reflection, reveal/download paths or unproved site scripts invalidate automatic fill [owner-confirmed B3].

For standard forms whose adapter proves secrecy, fill and submit must work. For canvas/custom widgets, unapproved frames, changed targets or any unproved secrecy, Lamp must offer successful private human takeover instead; the person types, all sensitive observations stay private, and agent control returns only to a proven safe page. If no such page exists, that workflow stays human-controlled. The agent never receives the password [owner-confirmed B3]. Useful protected browser controls are navigate/click/type/read/screenshot/download under explicit site/action adapters, never unrestricted CDP or JavaScript. Support descriptions identify the permitted site and action rather than promising universal safe fill.

### Core command, file and session parity examples

These are complete operation-specific inputs; common keys/revisions remain required where applicable. A caller uploads `bl_01` and captures every referenced real ID before using these schematic values. The CLI reads JSON arrays/objects locally. No upstream MCP server's tool name substitutes for a Lamp tool.

| Operation | CLI, always JSON | API JSON request | MCP call |
| --- | --- | --- | --- |
| Recipe create | `lamp recipe create --name dev --template-id tp_01 --json` | `POST /v1/recipes {"name":"dev","template_id":"tp_01"}` | `recipe_create({"name":"dev","template_id":"tp_01"})` |
| Exec create | `lamp exec create --sandbox-id sb_01 --argv-json '["python3","-c","print(42)"]' --json` | `POST /v1/execs {"sandbox_id":"sb_01","argv":["python3","-c","print(42)"]}` | `exec_create({"sandbox_id":"sb_01","argv":["python3","-c","print(42)"]})` |
| File write | `lamp sandbox file-write sb_01 --path /work/a.txt --blob-id bl_01 --json` | `POST /v1/sandboxes/sb_01:file-write {"path":"/work/a.txt","blob_id":"bl_01"}` | `sandbox_file_write({"sandbox_id":"sb_01","path":"/work/a.txt","blob_id":"bl_01"})` |
| File read | `lamp sandbox file-read sb_01 --path /work/a.txt --offset-bytes 0 --length-bytes 4096 --json` | `POST /v1/sandboxes/sb_01:file-read {"path":"/work/a.txt","offset_bytes":0,"length_bytes":4096}` | `sandbox_file_read({"sandbox_id":"sb_01","path":"/work/a.txt","offset_bytes":0,"length_bytes":4096})` |
| File upload | `lamp sandbox file-transfer sb_01 --path /work/a.txt --blob-id bl_01 --direction upload --json` | `POST /v1/sandboxes/sb_01:file-transfer {"path":"/work/a.txt","blob_id":"bl_01","direction":"upload"}` | `sandbox_file_transfer({"sandbox_id":"sb_01","path":"/work/a.txt","blob_id":"bl_01","direction":"upload"})` |
| File download | `lamp sandbox file-transfer sb_01 --path /work/a.txt --direction download --json` | `POST /v1/sandboxes/sb_01:file-transfer {"path":"/work/a.txt","direction":"download"}` | `sandbox_file_transfer({"sandbox_id":"sb_01","path":"/work/a.txt","direction":"download"})` |
| Connection attach | `lamp attachment create --sandbox-id sb_01 --resource-kind connection --resource-id cn_01 --json` | `POST /v1/attachments {"sandbox_id":"sb_01","resource_kind":"connection","resource_id":"cn_01"}` | `attachment_create({"sandbox_id":"sb_01","resource_kind":"connection","resource_id":"cn_01"})` |
| Session create | `lamp session create --sandbox-id sb_01 --harness-id hr_01 --agent-mode inside --json` | `POST /v1/sessions {"sandbox_id":"sb_01","harness_id":"hr_01","agent_mode":"inside"}` | `session_create({"sandbox_id":"sb_01","harness_id":"hr_01","agent_mode":"inside"})` |
| Input lease | `lamp lease create --type input --resource-id se_01 --json` | `POST /v1/leases {"type":"input","resource_id":"se_01"}` | `lease_create({"type":"input","resource_id":"se_01"})` |
| Session watch | `lamp session watch se_01 --cursor c1 --json` | `GET /v1/sessions/se_01/output?cursor=c1` | `session_watch({"session_id":"se_01","cursor":"c1","limit":100,"wait_ms":25000})` |
| Session attach | `lamp session attach se_01 --lease-id le_01 --cursor c1 --json` | `POST /v1/sessions/se_01:attach {"lease_id":"le_01","cursor":"c1"}` | `session_attach({"session_id":"se_01","lease_id":"le_01","cursor":"c1"})` |
| Session input | `lamp session input se_01 --lease-id le_01 --location-epoch 1 --sequence 1 --data-base64 aGkK --json` | `POST /v1/sessions/se_01:input {"lease_id":"le_01","location_epoch":1,"sequence":1,"data_base64":"aGkK"}` | `session_input({"session_id":"se_01","lease_id":"le_01","location_epoch":1,"sequence":1,"data_base64":"aGkK"})` |

Read exceptions: `sandbox_file_read` is a read-only POST with unwrapped `{data_base64,sha256,eof,request_id}`, range hash, no Operation and no mutation key. File-list uses `{items,next_cursor,request_id}`. Watch/download reads also create no Operation. CLI download first obtains the ready Blob from file-transfer, then uses `lamp blob download bl_01 --output ./a.txt --json`. Blob/artifact downloads write verified local bytes and emit `{"blob_id":"bl_01","output_path":"./a.txt","size_bytes":3,"sha256":"<actual>","eof":true,"request_id":"rq_01"}`. MCP `blob_download`/`artifact_download` adds its addressed ID plus `offset_bytes,length_bytes` and returns `{blob_id,offset_bytes,data_base64,chunk_sha256,sha256,size_bytes,eof}`; artifacts also return `manifest_blob_id`. Check every offset/chunk hash and final whole-object hash at end of file (EOF). Artifact finalization creates `download_blob_id` for the deterministic archive of actual files; a manifest alone is insufficient.

Creates return the common envelope, then typed outcome IDs: `recipe_id`, `exec_id`, `attachment_id`, `session_id/sandbox_id` and `lease_id`. File writes/uploads complete only after actual bytes match. Download exposes its resulting authorized Blob reference in Operation `resource_ids`; capture that `{kind,id}` and use `blob_download` for bytes. Reads return `data_base64,sha256,eof`; watch returns framed `items,next_cursor,request_id`; attach returns the full mutation envelope with `result.stream` holding StreamDescriptor. None returns a raw upstream credential. Lost-response retries reuse Operation identity; stale input location/lease is denied before delivery.

### Device delivery and watch example

Use actual device IDs returned from SBX-005. This controlled app has a 64×64 display, a keyboard, a mouse and audio endpoints. `device_ref={sandbox_id,generation,device_id}` binds an admitted device. Sequences are uint64 and increase per reference/generation.

| Step | CLI | API / MCP |
| --- | --- | --- |
| Key down | `lamp sandbox device-input sb_01 --device-ref-json '{"sandbox_id":"sb_01","generation":1,"device_id":"kbd_01"}' --sequence 1 --input-json '{"type":"key","code":4,"action":"down","modifiers":[]}' --json` | `POST /v1/sandboxes/sb_01:device-input {"device_ref":{"sandbox_id":"sb_01","generation":1,"device_id":"kbd_01"},"sequence":1,"input":{"type":"key","code":4,"action":"down","modifiers":[]}}`; `sandbox_device_input({"sandbox_id":"sb_01","device_ref":{"sandbox_id":"sb_01","generation":1,"device_id":"kbd_01"},"sequence":1,"input":{"type":"key","code":4,"action":"down","modifiers":[]}})` |
| Key up | Repeat the key request with `sequence=2,action=up` | Same API/tool; verify only one “a” is inserted |
| Mouse click | `lamp sandbox device-input sb_01 --device-ref-json '{"sandbox_id":"sb_01","generation":1,"device_id":"mouse_01"}' --sequence 1 --input-json '{"type":"mouse","x_px":12,"y_px":8,"buttons":["left"],"wheel_x":0,"wheel_y":0}' --json` | Same API/tool with mouse device_ref; a known pixel turns green |
| Display watch | `lamp sandbox device-watch sb_01 --device-ref-json '{"sandbox_id":"sb_01","generation":1,"device_id":"screen_01"}' --json` | `GET /v1/sandboxes/sb_01/device-stream?device_ref=<URL-encoded-canonical-JSON>`; `sandbox_device_watch({"sandbox_id":"sb_01","device_ref":{"sandbox_id":"sb_01","generation":1,"device_id":"screen_01"},"cursor":"c1","limit":100,"wait_ms":25000})` |

Input's completed Operation result acknowledges `{device_ref,sequence,delivered_at}` only after backend delivery. Replaying a sequence never redelivers; changed input at the same sequence conflicts. Input affects the actual backend; replayed sequences cannot redeliver it. Watch returns `{stream,items,next_cursor,request_id}`. StreamDescriptor adds applicable `media_type,width_px,height_px,sample_rate_hz,channels`; MediaFrame is `{stream_id,sequence,device_ref,media_type,pts_us,duration_us,keyframe,payload}`, substituting `data_base64` for binary payload on MCP. Ordinary output Frame and media share the common decoded/encoded limits.

Canonical input variants: key `{type:key,code,action:down|up,modifiers}` uses uint16 USB HID usage codes with modifiers `shift,control,alt,meta`; mouse coordinates are integer pixels from top-left, buttons `left,middle,right` and signed wheel detents. Audio `{type:audio,format:pcm_s16le,sample_rate_hz,channels,data_base64}` is interleaved signed 16-bit PCM: sample rate and channel count describe the delivered audio. Unknown tags or out-of-range coordinates fail before delivery. Disk uses file/block attachments; graphics processing unit (GPU) uses supported engine/kit APIs, never generic host ioctls.

### Complete approval exchange

The request JSON file below contains the canonical Approval fields shown in ACC-008, including exact principal/task/sandbox generation/resource/action plan/destination/expiry. The enforcing adapter recomputes the plan and digest; authenticated inspection displays that immutable plan, not task-supplied display text. For example, a vault/Connection may use `mode=each_use,ttl_seconds=60`. There is no public consume operation.

| Step | CLI, always JSON | API | MCP / expected state |
| --- | --- | --- | --- |
| Request | `lamp approval create --input ./approval.json --json` | `POST /v1/approvals` with the ACC-008 request object | `approval_create` with the same object; capture returned `approval_id`, read `state=pending` |
| Inspect | `lamp approval get ap_01 --json` | `GET /v1/approvals/ap_01` | `approval_get({"approval_id":"ap_01"})`; shows exact bound fields, never password |
| Approve | `lamp approval approve ap_01 --action-digest <actual> --json` | `POST /v1/approvals/ap_01:approve {"action_digest":"<actual>"}` | `approval_approve({"approval_id":"ap_01","action_digest":"<actual>"})`; `result.approval.state=approved` |
| Deny alternative | `lamp approval deny ap_01 --reason declined --json` | `POST /v1/approvals/ap_01:deny {"reason":"declined"}` | `approval_deny({"approval_id":"ap_01","reason":"declined"})`; `result.approval.state=denied`, 0 effect |
| Revoke alternative | `lamp approval revoke ap_01 --reason canceled --json` | `POST /v1/approvals/ap_01:revoke {"reason":"canceled"}` | `approval_revoke({"approval_id":"ap_01","reason":"canceled"})`; unconsumed grant becomes revoked |
| Use approved action | `lamp connection invoke cn_01 --attachment-id at_01 --operation read_issue --arguments-json '{"issue":1}' --approval-id ap_01 --json` | `POST /v1/connections/cn_01:invoke {"attachment_id":"at_01","operation":"read_issue","arguments":{"issue":1},"approval_id":"ap_01"}` | `connection_invoke({"connection_id":"cn_01","attachment_id":"at_01","operation":"read_issue","arguments":{"issue":1},"approval_id":"ap_01"})`; adapter must declare that exact schema |
| Verify consumption | `lamp approval get ap_01 --json` | `GET /v1/approvals/ap_01` | `approval_get({"approval_id":"ap_01"})`; `state=consumed,consumed_at` after exactly 1 effect |

Approve and deny are alternate runs, not sequential mutations of a consumed approval. A changed target/digest/role/lease or second invocation returns `APPROVAL_REQUIRED`/`SCOPE_DENIED` with 0 new effects. Console/phone views show the same object and decisions; SSH supports these commands or a trusted private view link. For password fill, the analogous consuming operation is `browser_fill_password`; unsafe entry stays human-only [owner-confirmed B3].

## Requirements by area

### Product and scope

#### PRD-001 — Public-purpose product and four equal goals

**Statement.** Lamp must serve people, agents, harnesses and CI from release one, supporting parallelism, safety, ready tools and sign-in, and ownership equally.
**Source.** Brief §§1–2, 21; ADRs 0003, 0008; architecture §§1, 21, 24.
**Interfaces.** CLI `lamp fleet get fl_01 --json`; API `GET /v1/fleets/fl_01`; MCP `fleet_get({"fleet_id":"fl_01"})`. Read data includes `{"kind":"fleet","mode":"single_owner"}`. `NOT_FOUND` hides an unauthorized fleet.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","mode":"single_owner"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard opens to fleet hosts, sandboxes, sessions and capacity. T3 Code, Herdr and SSH show two separate concurrent sessions and their independent output.

#### PRD-002 — Self-hosted installation and control

**Statement.** Lamp must install as packaged software on the person's hosts, with no required hosted database, Kubernetes, Redis, Postgres or Docker runtime. Unused feature workers must stay stopped.
**Source.** Brief §§1, 17, 22; ADRs 0005, 0007; architecture §§3–4, 20.
**Interfaces.** `lamp host get hs_01 --json`; `GET /v1/hosts/hs_01`; `host_get({"host_id":"hs_01"})`; read data `{"state":"ready","roles":["control","compute"]}`. Preflight failure explains missing virtualization; execution returns `ENGINE_UNQUALIFIED` for an unsupported or unsafe configuration rather than hiding it.
**JSON observation.** `{"data":{"id":"hs_01","kind":"host","state":"ready","roles":["control","compute"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard setup runs preflight → install/enroll → local ready. It explains privileged operating system (OS) setup separately. Consoles reach sessions after normal enrollment. Uninstall offers retain/export/delete owner data.

#### PRD-003 — Complete first-release scope; no cloud overflow

**Statement.** Release one must include every brief feature under the accepted O1 SDK cutoff. Lamp must not overflow work into cloud providers automatically.
**Source.** Brief §§18–22; ADRs 0001, 0008; architecture §§20–22.
**Interfaces.** `lamp sandbox create --recipe-id rc_01 --json`; `POST /v1/sandboxes {"recipe_id":"rc_01"}`; `sandbox_create` with that body. A full fleet returns `NO_CAPACITY`, with no cloud allocation.
**JSON observation.** `{"error":{"code":"NO_CAPACITY","message":"No admitted capacity remains","retryable":true,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows capacity and failed admission with choices to free resources or add an owner-selected host. Consoles retain existing sessions and show the failed new operation.

### Callers and interfaces

#### INT-001 — Credentialed callers and interface parity

**Statement.** A credentialed person, agent, CI job or outside service must get the same permitted product operations through CLI, API and MCP.
**Source.** Brief §§3–4, 17; ADRs 0001, 0005; architecture §§11–12.
**Interfaces.** `lamp exec create --sandbox-id sb_01 --argv-json '["printf","42\\n"]' --json`; `POST /v1/execs {"sandbox_id":"sb_01","argv":["printf","42\n"]}`; `exec_create` with that body. Operation result is `{"exec_id":"ex_01"}`. `SCOPE_DENIED` gives 0 effects.
**JSON observation.** `{"operation_id":"op_01","resource_ids":[{"kind":"exec","id":"ex_01"}],"replayed":false,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard command action and T3 Code/Herdr/SSH shell views show the same actual output. Transport choice never adds shell/files/approval rights.

#### INT-002 — Generated SDKs and delivery order

**Statement.** A TypeScript SDK must ship in release one. Go and Rust SDKs must follow immediately afterward. There is no Python SDK [owner, amended 2026-11-12]. All must derive from one API definition.
**Source.** Brief §3, O1; ADR 0008; architecture §§4, 12, 21.
**Interfaces.** `lamp api describe --json`; `GET /v1/openapi.json`; `api_describe({})` return the canonical OpenAPI definition. SDKs perform `sandbox_create` semantics, wait for the Operation and read the returned sandbox. Unknown fields produce `INVALID_ARGUMENT`.
**JSON observation.** OpenAPI excerpt: `{"openapi":"3.1.0","info":{"title":"Lamp","version":"v1"}}`; the complete document describes every canonical path.
**Visual/consoles.** Dashboard API help links generated SDK examples. Sessions created by each SDK appear normally in T3 Code, Herdr and SSH.

#### INT-003 — Agent-readable CLI and documentation

**Statement.** Every CLI command must support machine-readable JSON. Product documentation must also be available as LLM-readable text with canonical fields, errors and examples.
**Source.** Brief §3; ADR 0008; architecture §§4, 12.
**Interfaces.** `lamp docs read --topic sandbox --json`; `GET /v1/docs?topic=sandbox`; `docs_read({"topic":"sandbox"})`. `lamp --help --json` and `lamp version --json` are local diagnostics, not extra product APIs. JSON errors use `INVALID_ARGUMENT`.
**JSON observation.** `{"error":{"code":"INVALID_ARGUMENT","message":"Unknown field","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard docs offer human and text views. T3 Code/Herdr can open linked docs; SSH users run the same CLI and parse stdout without stripping progress text.

#### INT-004 — Idempotent retries

**Statement.** Retrying a mutation must never create fresh duplicate work. Unknown command or external side effects must remain explicitly unknown.
**Source.** Brief §3; ADR 0001; architecture §§12.2–12.3, 14.
**Interfaces.** `lamp sandbox create --recipe-id rc_01 --idempotency-key lk1.1.hs_01.nonce --json`; same API body plus `Idempotency-Key`; `sandbox_create({"recipe_id":"rc_01","idempotency_key":"lk1.1.hs_01.nonce"})`. Repeat returns the original `operation_id` with `replayed=true`. Different input returns `IDEMPOTENCY_CONFLICT`.
**JSON observation.** `{"operation_id":"op_01","resource_ids":[{"kind":"sandbox","id":"sb_01"}],"replayed":true,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard reconnect opens the original Operation and resource. Consoles do not add a second session after a dropped response. Unknown delivery shows `outcome_unknown`, never a retry-as-new button.

#### INT-005 — Live event stream

**Statement.** Callers must observe authorized events with replay, ordering, gap detection and bounded backpressure.
**Source.** Brief §3; ADR 0001; architecture §12.4.
**Interfaces.** `lamp event list --follow --json`; API `GET /v1/events?cursor=c1` with `Accept: text/event-stream`; MCP `event_list({"cursor":"c1","limit":100,"wait_ms":25000})` → `{"items":[],"next_cursor":"c2"}`. Names include `sandbox.state_changed`, `operation.completed`, `session.status_changed`. Expired replay returns `CURSOR_EXPIRED` and resync guidance.
**JSON observation.** `{"items":[{"event_id":"<host-epoch-sequence>","type":"session.status_changed","resource_kind":"session","resource_id":"se_01","resource_revision":2,"data":{"status":"blocked"}}],"next_cursor":"c2","request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr reconcile resource revisions; SSH reconnect resumes output cursor. Stale location events cannot move a session backward.

#### INT-006 — Agent inside

**Statement.** A caller must be able to run a harness inside a sandbox and let it work independently while its session remains observable and budgeted.
**Source.** Brief §§3–4, 13, 16; ADR 0004; architecture §§9–10, 17.
**Interfaces.** `lamp session create --sandbox-id sb_01 --harness-id hr_01 --agent-mode inside --json`; `POST /v1/sessions {"sandbox_id":"sb_01","harness_id":"hr_01","agent_mode":"inside"}`; `session_create` with that body. Result `{"session_id":"se_01","sandbox_id":"sb_01"}`; unsupported auth returns `AUTH_TRANSPORT_UNSUPPORTED`.
**JSON observation.** `{"data":{"id":"se_01","kind":"session","agent_mode":"inside","state":"active","status":"working"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows sandbox and Session independently. T3 Code/Herdr/SSH disconnect and reattach without restarting the harness.

#### INT-007 — Agent outside

**Statement.** An outside agent must control commands, files, browser and requested devices through the same scoped remote-computer operations.
**Source.** Brief §§3–4, 6, 15; ADRs 0002, 0004; architecture §§10–12.
**Interfaces.** `lamp sandbox file-read sb_01 --path /work/a.txt --offset-bytes 0 --length-bytes 4096 --json`; `POST /v1/sandboxes/sb_01:file-read {"path":"/work/a.txt","offset_bytes":0,"length_bytes":4096}`; `sandbox_file_read` adds `sandbox_id`. Result `{"data_base64":"NDIK","sha256":"<actual-hash>","eof":true}`. Escaping paths fail `SCOPE_DENIED`.
**JSON observation.** `{"data_base64":"NDIK","sha256":"<actual-sha256>","eof":true,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard file/browser/device panes and T3 Code/Herdr show outside Session and actual actions. SSH sees the same files and process outputs; graphical takeover is a trusted link.

#### INT-008 — Readiness, concurrent edits and cancellation [added]

**Statement.** Accepted work must expose its Operation, partial resources, setup checks and cleanup. Ready must mean all requested setup passed and a real command ran. Dangerous stale edits must fail atomically.
**Source.** Brief §§3, 8, 13, 19; ADRs 0001–0002, 0004; architecture §§6.2, 11–12.
**Interfaces.** `lamp operation get op_01 --json`; `GET /v1/operations/op_01`; `operation_get({"operation_id":"op_01"})`. Read data includes `{"state":"failed","phase":"kits","cleanup_state":"complete"}`. `sandbox_get` exposes `readiness.checks` and `command_started_at`; no `running` sandbox alias. `REVISION_CONFLICT` includes current revision.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"failed","phase":"kits","cleanup_state":"complete"},"request_id":"rq_01"}`
**Visual/consoles.** Create → preparing checks → command-ready → agent-ready. Failure retains Operation and failed stage. T3 Code/Herdr/SSH show Session `starting` until real readiness and truthful failure afterward.

### Hosts and fleets

#### HST-001 — First-release host matrix

**Statement.** Release one must support Linux x86_64 and arm64, and Apple Silicon macOS at the accepted minimums.
**Source.** Brief §§5–6, 22, O10; ADR 0001; architecture §§5, 20.
**Interfaces.** `lamp host list --json`; `GET /v1/hosts`; `host_list({})`. Items expose `os,os_version,arch,engine_ids`. Incompatible requests return `ENGINE_UNQUALIFIED`, never fabricated compatibility.
**JSON observation.** `{"items":[{"id":"hs_01","kind":"host","os":"linux","arch":"x86_64","state":"ready"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard host setup shows OS/build/CPU and supported guests and devices. All three consoles show the actual session host and guest class.

#### HST-002 — One fleet address and key set

**Statement.** One or many enrolled hosts must present one fleet identity, address and caller key namespace.
**Source.** Brief §§5, 16–17; ADR 0001; architecture §§14–15.
**Interfaces.** `lamp host enroll hs_02 --invitation-id iv_01 --proof '<proof>' --json`; `POST /v1/hosts/hs_02:enroll {"invitation_id":"iv_01","proof":"<proof>"}`; `host_enroll` adds `host_id`. Read fleet data `{"id":"fl_01","coordination_state":"healthy"}`. Invalid enrollment is `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","coordination_state":"healthy","authority_epoch":1},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard invitation→enroll→host ready; T3 Code and Herdr add no second machine entry. SSH lists authorized sessions across all hosts.

#### HST-003 — Automatic and pinned placement

**Statement.** Placement must satisfy room, OS, devices, storage and each person's credential-custody trust. A requested host pin must be strict.
**Source.** Brief §§5–6, 13; ADRs 0001, 0004; architecture §§5.2, 9.2, 14.
**Interfaces.** `lamp sandbox create --recipe-id rc_01 --host-id hs_02 --json`; `POST /v1/sandboxes {"recipe_id":"rc_01","host_id":"hs_02"}`; `sandbox_create` with body. Read sandbox has `host_id`. Fail with `HOST_NOT_TRUSTED`, `HOST_UNAVAILABLE`, `DEVICE_UNAVAILABLE` or `NO_CAPACITY` as appropriate.
**JSON observation.** `{"error":{"code":"HOST_NOT_TRUSTED","message":"Host is outside credential custody","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard filters compatible hosts, explains rejected options and previews effective policy. Consoles show actual host; a failed pin never appears elsewhere.

#### HST-004 — Coordination failover and sleeping laptops

**Statement.** A surviving host must automatically accept new authorized work under its precommitted disjoint capacity. Failover must preserve quotas, retry identity and exclusive writers without requiring a third host. If either host sleeps, the other can start new work using capacity reserved for it beforehand. It cannot spend the sleeping host's capacity, recover that host's only files, or change fleet-wide permissions.
**Source.** Brief §5; ADR 0001; architecture §14.
**Interfaces.** `lamp fleet get fl_01 --json`; `GET /v1/fleets/fl_01`; `fleet_get` reads `{"coordination_state":"degraded"}`. New structured-key create works within survivor permit; global edits without quorum return `QUORUM_REQUIRED`; old unreachable homes return `IDEMPOTENCY_HOME_UNAVAILABLE`.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","coordination_state":"degraded","authority_epoch":1},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard distinguishes control availability from unavailable local disks or sleeping sessions. T3 Code/Herdr/SSH reconnect to survivor work; no false migration/completion.

#### HST-005 — Opt-in rolling updates

**Statement.** Automatic updates must start disabled, update one host at a time, verify release trust and preserve active/saved state. Failure must stop rollout and permit rollback.
**Source.** Brief §5; ADRs 0001, 0003, 0008; architecture §§13–14.
**Interfaces.** `lamp fleet update fl_01 --update-policy-json '{"automatic":true,"max_unavailable":1,"channel":"stable","drain_timeout_ms":60000}' --json`; `PATCH /v1/fleets/fl_01` with `update_policy`; `fleet_update` adds `fleet_id`. `update_get` exposes `state=failed|rolled_back`; tampering fails `INVALID_ARGUMENT` before apply. Read Update `metadata_versions={root,targets,snapshot,timestamp}`, `security_revocations=[{digest,effective_at,reason,metadata_version}]` and `migration={log_version,snapshot_version,projection_version,phase,rollback_safe}`; phases are `expand|barrier|contract`. These are observations, never caller overrides.
**JSON observation.** `{"data":{"id":"up_01","kind":"update","current_host_id":"hs_01","state":"rolled_back"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard consent→release/provenance preview→drain/apply/check→next host. Consoles show affected availability; failed resume is never replaced by a new session.

#### HST-006 — Fastest supported engine per host

**Statement.** Lamp must choose the fastest supported engine that meets the request. Security, OS, devices, credential custody and capture support determine eligibility; benchmark certification is not an admission requirement.
**Source.** Brief §§5–7, 22; ADR 0003; architecture §5.
**Interfaces.** `lamp engine list --json`; `GET /v1/engines`; `engine_list({})`. Items identify the digest, supported guest/devices and measurements if available. Sandbox exposes selected `engine_id`. `ENGINE_UNQUALIFIED` means the requested configuration is unsupported or unsafe, not that a benchmark is missing.
**JSON observation.** `{"items":[{"id":"en_01","kind":"engine","digest":"<actual>","guest_os":"linux","arch":"x86_64"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard explains eligible ranking and evidence. T3 Code/Herdr/SSH details show actual boundary and guest; a faster weak boundary never substitutes for a VM.

### Sandboxes

#### SBX-001 — Fast microVM isolation

**Statement.** Real-VM sandboxes must isolate task execution from the host and neighbors, with externally confined engine workers on each OS.
**Source.** Brief §§1–2, 6–7; ADR 0003; architecture §§3, 5, 18.
**Interfaces.** `lamp sandbox get sb_01 --json`; `GET /v1/sandboxes/sb_01`; `sandbox_get` reads `{"boundary":"vm","engine_id":"en_01"}`. Host/neighbor path attempts return `SCOPE_DENIED` or `NOT_FOUND` without effects.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","boundary":"vm","engine_id":"en_01","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard labels VM boundary and native full-VM class where relevant. Consoles run identical task paths in separate sessions and never expose host shells.

#### SBX-002 — Linux guests on every host

**Statement.** Linux guests must execute on every supported virtualizing host family at native CPU architecture.
**Source.** Brief §§5–6; ADR 0001; architecture §5.
**Interfaces.** `lamp sandbox create --template-id tp_linux --host-id hs_mac --json`; `POST /v1/sandboxes {"template_id":"tp_linux","host_id":"hs_mac"}`; `sandbox_create` with body. Read data shows `guest_os=linux,arch,host_id`. Wrong architecture fails `ENGINE_UNQUALIFIED`.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","guest_os":"linux","arch":"arm64","host_id":"hs_mac"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard separates host OS from guest OS. T3 Code/Herdr/SSH attach to real Linux execution on either Linux or Mac hosts.

#### SBX-003 — Native macOS full-VM class and two-per-Mac limit

**Statement.** Native macOS must run only on Macs as a visibly slower full-VM class, built from owner-licensed media. At most 2 resumable macOS allocations may exist per Mac.
**Source.** Brief §§5–6, O8; ADR 0001; architecture §§5–6.
**Interfaces.** Read `template_get` for an owner-built ready native Template and its `minimum_resources,default_resources`. CLI `lamp sandbox create --template-id tp_mac --host-id hs_mac --resources-json '<exact returned default_resources JSON>' --json`; API `POST /v1/sandboxes` with `template_id,host_id,resources`; MCP `sandbox_create` with the identical body. Third macOS allocation is `NO_CAPACITY`; wrong host is `ENGINE_UNQUALIFIED`; an explicit undersized resource value is `INVALID_ARGUMENT` before reservation.
**JSON observation.** `{"data":{"id":"sb_mac","kind":"sandbox","guest_os":"macos","boundary":"vm","host_id":"hs_mac"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/console detail says “macOS full VM — slower,” never a Linux microVM target badge. Native media setup is guided and local.

#### SBX-004 — Caller-selected resources

**Statement.** Callers must select CPU, memory, disk and process limits, with real admission/enforcement and no silent overcommit.
**Source.** Brief §§5–6, 17; ADR 0001; architecture §§3, 5.2.
**Interfaces.** `lamp sandbox create --template-id tp_01 --resources-json '{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}' --json`; API `/v1/sandboxes` with `template_id,resources`; `sandbox_create` with body. `NO_CAPACITY` or `SCOPE_DENIED` occurs before over-allocation.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard preview shows requested resources, overhead and effective quota. All consoles show limits and actual OOM/ENOSPC/stop reason.

#### SBX-005 — Devices only on request

**Statement.** Optional devices must appear only when explicitly requested and supported. Unsupported or unforkable combinations must fail before unsafe execution/capture.
**Source.** Brief §§5–6; ADR 0003; architecture §5.2.
**Interfaces.** `lamp sandbox create --template-id tp_01 --devices-json '[{"id":"screen","type":"display"},{"id":"keys","type":"keyboard"},{"id":"pointer","type":"mouse"}]' --json`; API `/v1/sandboxes` with same `devices`; `sandbox_create` with body. Errors `DEVICE_UNAVAILABLE` and `DEVICE_NOT_FORKABLE` identify missing rows.
**JSON observation.** `{"error":{"code":"DEVICE_UNAVAILABLE","message":"Requested device unavailable","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard device picker shows compatibility and live display/audio/input only when selected. T3 Code/Herdr open authorized streams; SSH offers the trusted device-view link and text session.

#### SBX-006 — Honest fallback and secret restrictions

**Statement.** Nonvirtualizing hosts must label weaker `gvisor` or `process` boundaries. Secrets require a real VM unless the owner grants an explicit Exposure. All verified public-repository CI jobs, including trusted branches, must always require a fresh VM.
**Source.** Brief §6, O5; ADRs 0003–0004, 0007; architecture §§5, 9.3, 16.2.
**Interfaces.** `lamp sandbox create --template-id tp_public --host-id hs_nv --json`; API `/v1/sandboxes` with same IDs; `sandbox_create`. Read data `{"boundary":"gvisor"}` or `{"boundary":"process"}`. Secret defaults fail `VM_REQUIRED`; no exposure can waive any public-job VM policy.
**JSON observation.** `{"data":{"id":"sb_nv","kind":"sandbox","boundary":"gvisor","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard and T3 Code/Herdr/SSH retain a weaker-boundary warning. Override requires separate owner action, shows contamination and never becomes a default recipe flag.

#### SBX-007 — Fork a running sandbox including memory

**Statement.** A memory fork must preserve one consistent RAM/disk/device generation, continue the source, and establish fresh identity and current authority before fork task I/O.
**Source.** Brief §§6–7, 9, 13, A1–A2; ADR 0004 amended; architecture §§5–7.
**Interfaces.** `lamp sandbox fork sb_01 --fork-mode memory --durability local --json`; `POST /v1/sandboxes/sb_01:fork {"fork_mode":"memory","durability":"local"}`; `sandbox_fork` adds `sandbox_id`. Operation result `{"sandbox_id":"sb_02","session_ids":["se_02"]}`; Snapshot reports `preserves_memory=true`. Unsafe device fails `DEVICE_NOT_FORKABLE` before capture. If exposed material can be inherited, first `lamp sandbox prepare-fork sb_01 --fork-mode memory --durability local --json`, `POST /v1/sandboxes/sb_01:prepare-fork {"fork_mode":"memory","durability":"local"}`, or `sandbox_prepare_fork({"sandbox_id":"sb_01","fork_mode":"memory","durability":"local"})`. Capture the empty child `sandbox_id,generation,fork_preparation,expires_at`, obtain new child-bound Exposure approvals, then call `sandbox_fork` with `prepared_sandbox_id,exposure_ids`. No material is copied by preparation.
**JSON observation.** `{"data":{"id":"sn_01","kind":"snapshot","preserves_memory":true,"durability":"local","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard previews memory/attachment/custody semantics and shows source pause plus both descendants. T3 Code/Herdr/SSH discover returned Session IDs. Native-Mac fallback explicitly says disk-only fresh boot, `preserves_memory=false`.

### Templates and recipes

#### TPL-001 — Lamp's own template format

**Statement.** Templates must use `lamp.template/v1`, with verified immutable content and a confined build. Lamp must reject Dockerfiles, Docker/OCI images and host Docker runtime dependencies.
**Source.** Brief §8, O8; ADRs 0003, 0007; architecture §§6.1, 7.
**Interfaces.** `lamp template create --name tools --definition-blob-id bl_01 --json`; `POST /v1/templates {"name":"tools","definition_blob_id":"bl_01"}`; `template_create` with body. Then `lamp template build tp_01 --definition-blob-id bl_01 --json`, API `POST /v1/templates/tp_01:build`, MCP `template_build`. `INVALID_ARGUMENT` rejects unsupported formats; corrupted input never becomes ready.
**JSON observation.** `{"data":{"id":"tp_01","kind":"template","state":"building","definition_blob_id":"bl_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard upload definition→validate→build stages→boot probe→ready or retained failure. T3 Code/Herdr/SSH start sessions from returned template without a Docker setup step.

#### TPL-002 — Save installed tools into a template

**Statement.** Saving tools must produce reusable clean template content. Tainted subscription/exposure state must be reconstructed from verified nonsecret inputs, never declared clean by byte scanning.
**Source.** Brief §§8, 10, 13, A1; ADRs 0004 amended, 0007; architecture §6.3.
**Interfaces.** `lamp sandbox save-template sb_01 --name python-tools --selected-paths-json '["/opt/tools"]' --reconstruction-blob-id bl_plan --json`; `POST /v1/sandboxes/sb_01:save-template {"name":"python-tools","selected_paths":["/opt/tools"],"reconstruction_blob_id":"bl_plan"}`; `sandbox_save_template` adds `sandbox_id`. Missing clean inputs returns `CLEAN_REBUILD_REQUIRED`.
**JSON observation.** `{"error":{"code":"CLEAN_REBUILD_REQUIRED","message":"Verified reconstruction inputs required","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard explains direct clean save versus clean rebuild, shows tools/provenance and fresh sign-in requirement. All consoles run installed versions from a new sandbox; no inherited personal login.

#### TPL-003 — Named recipes

**Statement.** Recipes must save template, harness/auth, kits, devices, resources, networking, connections, settings, lifecycle and budget. Names must resolve atomically to immutable versions.
**Source.** Brief §§8–9, 13–15; ADRs 0002, 0004, 0007; architecture §§6, 11–12.
**Interfaces.** `lamp recipe create --input ./recipe.json --json`; `POST /v1/recipes {"name":"dev","template_id":"tp_01","harness_id":"hr_01","auth_mode":"api_key","kit_ids":[],"devices":[],"resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096},"network_policy_id":"np_01","connection_ids":["cn_01"],"settings_source_ids":["ss_01"]}`; `recipe_create` with body. `recipe_get` and `recipe_list` resolves name to ID before create. `REVISION_CONFLICT` prevents stale pointer updates.
**JSON observation.** `{"data":{"id":"rc_01","kind":"recipe","name":"dev","template_id":"tp_01","revision":1,"digest":"<actual-digest>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard New/edit recipe→effective-policy preview→compatible hosts→save version→create sandbox. T3 Code/Herdr/SSH show resolved recipe and resulting tools, not only a name badge.

### Lifecycle

#### LIF-001 — Idle suspension, saved state and message-driven resume

**Statement.** By default an eligible idle sandbox must save memory and disk durably, release task CPU and memory, and resume the same task on authorized activity before its deadline. After that deadline, saved work needs explicitly authorized continuation under a new Task and Budget.
**Source.** Brief §9, A1, A5; ADR 0004 amended; architecture §6.5.
**Interfaces.** `lamp sandbox suspend sb_01 --durability local --json`; `POST /v1/sandboxes/sb_01:suspend {"durability":"local"}`; `sandbox_suspend` adds `sandbox_id`. `message_create({"session_id":"se_01","location_epoch":1,"text":"Continue"})` triggers coalesced resume. Bad saved state yields failed Operation, never a successful cold start. After expiry, automatic message/exec/SSH/port wake returns `TASK_AUTHORITY_EXPIRED`. Create a new Budget and Task, read revision 7, then `lamp sandbox resume sb_01 --continuation-task-id tk_new --expected-task-id tk_old --expected-revision 7 --json`; REST `POST /v1/sandboxes/sb_01:resume {"continuation_task_id":"tk_new","expected_task_id":"tk_old"}` with If-Match 7; MCP `sandbox_resume` adds `sandbox_id,expected_revision`. For an unexpired same-Task resume, omit both continuation fields.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","state":"suspended","snapshot_id":"sn_01","durability":"local"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows suspended storage, released allocations and wake progress. T3 Code/Herdr/SSH reconnect to continued session and memory marker. Native Mac states retain original compatible Mac/user restriction.

#### LIF-002 — Exact definition of idle

**Statement.** Idle eligibility must require no task session activity, task process or task connection, except a harness wait solely for the person's next message. Unknown accounting must count as active.
**Source.** Brief §9, A5; ADR 0004; architecture §6.5.
**Interfaces.** `lamp session get se_01 --json`; `GET /v1/sessions/se_01`; `session_get` reads `{"status":"idle","observation_state":"fresh"}`. Sandbox lifecycle is separately inspected. A request to suspend unsafe active state can return `RESOURCE_IN_USE`. Read Sandbox `suspend_eligibility={eligible,reasons,observed_at,observation_state}` through `sandbox_get` on CLI/API/MCP and dashboard. Reasons are exactly `session_activity,task_process,task_connection,harness_wait_unqualified,accounting_unknown`; stale/unknown is ineligible and only a fresh eligible observation has no reasons.
**JSON observation.** `{"data":{"id":"se_01","kind":"session","status":"idle","observation_state":"fresh"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows both session status and suspend eligibility/reason. T3 Code/Herdr/SSH distinguish blocked human wait from active build, sleeping job or open server socket.

#### LIF-003 — Expiry and per-sandbox policy overrides

**Statement.** Callers must override lifecycle within their authority. Default suspended retention must delete expired sandbox state without resetting deadlines on sleep, fork or restore.
**Source.** Brief §9; ADRs 0001, 0004; architecture §§6.5, 11, 17.
**Interfaces.** `lamp sandbox update sb_01 --lifecycle-json '{"idle_action":"suspend","idle_grace_seconds":60,"suspended_retention_seconds":3600}' --json`; `PATCH /v1/sandboxes/sb_01` with `lifecycle`; `sandbox_update` adds `sandbox_id`. Widening beyond scope is `SCOPE_DENIED`; stale update is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","lifecycle":{"idle_action":"suspend","idle_grace_seconds":60,"suspended_retention_seconds":3600}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows idle action, countdown, expiry and impact before save/delete. All consoles show pending deletion and ended Session after cleanup.

#### LIF-004 — CI deletion default

**Statement.** A terminal or canceled CI job must lose execution and network authority immediately. The default deletes its sandbox after bounded output collection; an authorized trusted-job policy may retain encrypted stopped state for inspection.
**Source.** Brief §§9, 18; ADRs 0002–0004; architecture §§7.2, 16.
**Interfaces.** `lamp job cancel jb_01 --reason test --json`; `POST /v1/jobs/jb_01:cancel {"reason":"test"}`; `job_cancel` adds `job_id`. `job_get` exposes `collecting` then terminal/`cleanup_failed`; no invented deleted-job success hides a live worker. Lifecycle `on_job_end=delete|retain_stopped` defaults to delete. Configure a trusted job with `sandbox_update` and `lifecycle.on_job_end=retain_stopped`; stranger jobs cannot select this override.
**JSON observation.** `{"data":{"id":"jb_01","kind":"job","state":"collecting","artifact_ids":["ar_01"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard job→collection manifest→execution gone/cleanup failure. T3 Code/Herdr/SSH stop accepting input immediately at cancellation and keep retained logs read-only.

#### LIF-005 — Warm pool

**Statement.** Warm pools must hold never-used, signed-out, connection-free sandboxes, match immutable request/security inputs and claim each entry once.
**Source.** Brief §§9, 19; ADRs 0001, 0004; architecture §6.2.
**Interfaces.** `lamp warm-pool create --recipe-id rc_01 --target-size 2 --max-size 2 --json`; `POST /v1/warm-pools {"recipe_id":"rc_01","target_size":2,"max_size":2}`; `warm_pool_create`. Read data `{"ready_count":2,"state":"active"}`; quota exhaustion is `NO_CAPACITY`.
**JSON observation.** `{"data":{"id":"wp_01","kind":"warm_pool","target_size":2,"max_size":2,"ready_count":2,"state":"active"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard pool target/ready/claimed/refilling and charged resources; all consoles see only claimed sessions after owner binding and requested setup.

#### LIF-006 — Fork alternatives and keep the winner

**Statement.** A caller must fork mid-task, run alternatives independently, select a result and return chosen changes through git without blind overwrite.
**Source.** Brief §§9, 16; ADRs 0001, 0004; architecture §§6.4, 10.1.
**Interfaces.** `lamp change-set apply cs_01 --selected-paths-json '["src/fix.py"]' --json`; `POST /v1/change-sets/cs_01:apply {"selected_paths":["src/fix.py"]}`; `change_set_apply` adds `change_set_id`. Result conflicts stay `state=conflicted`; stale target returns `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"cs_01","kind":"change_set","state":"conflicted","conflicts":["src/fix.py"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard fork tree→compare results/diff→apply selected winner→delete loser. T3 Code/Herdr/SSH show every alternative and real git conflict/output.

### Storage that outlives a sandbox

#### STO-001 — Shared caches

**Statement.** Package/build caches must survive sandbox deletion, be shareable within explicit owner/trust namespaces and resist untrusted-to-trusted poisoning.
**Source.** Brief §§10, 18; ADRs 0001, 0004; architecture §7.
**Interfaces.** `lamp cache create --name pip --trust-class trusted --arch x86_64 --quota-bytes 1073741824 --json`; `POST /v1/caches` with those fields; `cache_create`. Attach through `attachment_create` with `resource_kind=cache`. Publish uses `cache_publish({"cache_id":"ca_01","generation_digest":"<actual>","base_digest":"<actual>"})`; stale publication conflicts.
**JSON observation.** `{"data":{"id":"ca_01","kind":"cache","trust_class":"trusted","quota_bytes":1073741824,"used_bytes":4096},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard cache usage/trust/toolchain/version→attach/evict/delete; consoles show actual reuse and independent private task roots.

#### STO-002 — Named volumes with multiple attachments

**Statement.** Named volumes must support explicit shared-files or exclusive-block semantics, real durability and fenced recovery. Fork must not duplicate a block writer implicitly.
**Source.** Brief §10; ADRs 0001–0002; architecture §§6.4, 7.
**Interfaces.** `lamp volume create --name work --mode shared_files --storage-host-id hs_01 --quota-bytes 1073741824 --json`; `POST /v1/volumes` with those fields; `volume_create`. Attach via `attachment_create`; duplicate block writer returns `RESOURCE_IN_USE`; lost data host returns `DATA_UNAVAILABLE`.
**JSON observation.** `{"data":{"id":"vo_01","kind":"volume","mode":"shared_files","state":"ready","attachment_ids":[]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard mode/settings/storage host→attach/mount→locks/leases→snapshot/recover. T3 Code/Herdr/SSH see actual shared files or exclusive-writer failure, not an empty replacement volume.

#### STO-003 — Saved CI outputs and logs

**Statement.** Outputs must remain downloadable after execution deletion, with verified complete/incomplete manifests and bounded failed collection.
**Source.** Brief §§10, 18–19; ADRs 0001, 0004; architecture §7.2.
**Interfaces.** `lamp artifact get ar_01 --json`; `GET /v1/artifacts/ar_01`; `artifact_get` reads `{"state":"incomplete","expected_bytes":1048576,"present_bytes":524288}`. `lamp artifact download ar_01 --output ./artifact.tar --json`, `GET /v1/artifacts/ar_01/content`, `artifact_download` return verified manifest/bytes. Missing storage is `DATA_UNAVAILABLE`.
**JSON observation.** `{"data":{"id":"ar_01","kind":"artifact","state":"incomplete","expected_bytes":1048576,"present_bytes":524288},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard jobs→outputs shows exact missing bytes/chunks, retry and cleanup failure. All consoles retain live logs then read-only artifact link; incomplete is never green success.

#### STO-004 — Owner-only persistent state by default

**Statement.** Caches, volumes, artifacts, templates, snapshots and backups must be private to their owner unless explicitly shared. Application admin status must not imply private-content access.
**Source.** Brief §§10, 16–17, O9; ADRs 0001, 0004–0005; architecture §§7, 15.
**Interfaces.** `lamp volume list --json`; `GET /v1/volumes`; `volume_list({})` returns only authorized `items`. Foreign `volume_get` returns `NOT_FOUND`. Explicit Share grants named resource/actions, never guessed-ID access.
**JSON observation.** `{"items":[],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard own storage→share preview→recipient/actions/expiry→revoke. T3 Code/Herdr/SSH file/session lists and errors hide unshared state consistently.

#### STO-005 — Durability, backup and restore [added]

**Statement.** Local saves survive a restart while that disk is intact. A save confirmed on two hosts survives losing either one without losing saved data. A backup restores only through its last completed save. Lamp must show that time and never replace missing files with an empty sandbox.
**Source.** Brief §§5, 9–10, 13; ADRs 0001, 0004; architecture §7.1.
**Interfaces.** `lamp backup create --resource-kind volume --resource-id vo_01 --target-host-ids-json '["hs_02"]' --json`; `POST /v1/backups` with same fields; `backup_create`. `backup_restore({"backup_id":"bk_01","target_host_id":"hs_02","new_authority_epoch":2})` is explicit recovery. Missing content yields `DATA_UNAVAILABLE`; wrong custody `HOST_NOT_TRUSTED`. `backup_restore` and `sandbox_restore` may take owner-authorized `recovery_credential_source_id`, a host-held handle, never private key bytes. Read desired/achieved counts, receipts and `recovery_state` on CLI/API/MCP/dashboard; transitions unavailable/lost/restore_required must be observable without invented state aliases.
**JSON observation.** `{"data":{"id":"bk_01","kind":"backup","state":"ready","consistency":"application","authority_epoch":1,"manifest_digest":"<actual-digest>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard backup→consistency/time/receipts→restore preview with RPO→staged verify→activate. Consoles show unavailable/restore-required work honestly; coordination recovery alone never claims recovered files.

### Network

#### NET-001 — Host-enforced network setup

**Statement.** All routing, membership, filtering and ingress keys must remain on the host. Policy must be installed before task execution and cannot be bypassed by guest root or nested Docker.
**Source.** Brief §11, O3; ADR 0002; architecture §8.
**Interfaces.** `lamp sandbox update sb_01 --network-policy-id np_01 --json`; `PATCH /v1/sandboxes/sb_01 {"network_policy_id":"np_01"}`; `sandbox_update` adds `sandbox_id`. Inspect `effective_policy_revision`; unavailable guard fails `HOST_UNAVAILABLE` or `AUDIT_UNAVAILABLE` where audit is cause.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","network_policy_id":"np_01","effective_policy_revision":2},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard effective-policy preview→atomic apply→acknowledged revision. T3 Code/Herdr/SSH show denied flow reason; guest route edits never change authoritative policy.

#### NET-002 — Independent outbound exits

**Statement.** Each sandbox must choose direct, VPN or proxy egress independently. Selected-exit failure must drop traffic without bypass. A provider supplies the public IP; Lamp must report the observed exit honestly.
**Source.** Brief §11; ADR 0002; architecture §8.
**Interfaces.** `lamp network-attachment create --sandbox-id sb_01 --type exit --plugin-id pg_vpn --config-ref-json '{"configuration_id":"cfg_vpn","digest":"<verified>"}' --json`; `POST /v1/network-attachments` with same fields; `network_attachment_create`. Read data `{"type":"exit","observed_exit_ip":"<verified-IP>","state":"ready"}`. Failed attachment leaves create failed; no ready badge.
**JSON observation.** `{"data":{"id":"na_01","kind":"network_attachment","type":"exit","state":"ready","observed_exit_ip":"<verified-IP>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard selects provider/config→test reflector→observed exit→attach. Consoles show route/failure in task detail while host keeps provider credentials private.

#### NET-003 — Private network membership

**Statement.** A sandbox must have its own approved private-network identity, separate state and only mapped inbound ports. Membership must not expose the host or neighbors.
**Source.** Brief §11; ADR 0002; architecture §8.1.
**Interfaces.** `lamp network-attachment create --sandbox-id sb_01 --type private_member --plugin-id pg_ts --config-ref-json '{"configuration_id":"cfg_member","digest":"<verified>"}' --port-maps-json '[{"protocol":"tcp","member_port":8080,"guest_port":8080}]' --json`; API `/v1/network-attachments` same fields; `network_attachment_create`. `network_attachment_test` checks ready identity. Unsupported endpoint protocols fail `INVALID_ARGUMENT`.
**JSON observation.** `{"data":{"id":"na_02","kind":"network_attachment","type":"private_member","state":"ready","identity":"<provider-member-identity>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard membership enrollment/approval→peer identity→mapped ports/revoke. T3 Code/Herdr/SSH show sandbox member, not host member masquerading as guest.

#### NET-004 — Inbound dev-server URLs

**Statement.** Dev-server ports must be private signed-in by default, generation-scoped, shareable with expiry and immediately revocable without widening sandbox authority.
**Source.** Brief §§11, 17, O4; ADRs 0002, 0006; architecture §§8.2, 15.3.
**Interfaces.** `lamp port create --sandbox-id sb_01 --port 8080 --protocol http --access private --json`; `POST /v1/ports` with same fields; `port_create`. Then `port_publish({"port_id":"po_01"})`; `port_share_link({"port_id":"po_01","expires_at":"<t0+3600s>"})` returns `{"url":"<host-issued>","expires_at":"..."}`. Revoked/foreign resource is `NOT_FOUND`.
**JSON observation.** `{"data":{"id":"po_01","kind":"port","access":"private","state":"ready","origin":"https://p-random.f123.fleet.eilmer.si"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard Ports→publish→open authenticated page→share TTL/recipient→unpublish. T3 Code/Herdr open page; SSH prints authorized URL. No forwarding of owner cookies to app.

#### NET-005 — Allow and deny lists

**Statement.** Policies must enforce normalized destination rules, deny precedence, parent ceilings and atomic narrowing, with transport and strict-site modes clearly distinguished.
**Source.** Brief §11, O3; ADR 0002; architecture §8.
**Interfaces.** `lamp network-policy create --filter-mode strict_sites --default-egress deny --allow-json '[{"host_pattern":"packages.example.org","protocol":"tcp","ports":[443]}]' --json`; `POST /v1/network-policies` with those fields; `network_policy_create`. `network_policy_update` uses revision; stale edit is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"np_01","kind":"network_policy","filter_mode":"strict_sites","default_egress":"deny","revision":2},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard rule editor previews effective allow/deny and affected active flows before apply. All consoles receive truthful denied destinations; strict mode never claims to inspect opaque subscription requests.

#### NET-006 — Explicit access between sandboxes

**Statement.** Neighbor access must be denied by default and allowed only by directed, port/protocol-scoped Connections within parent policy.
**Source.** Brief §11; ADR 0002; architecture §§8–9.
**Interfaces.** `lamp connection create --type sandbox_link --endpoint '<authorized-endpoint>' --access full --json`; `POST /v1/connections` same fields; `connection_create`. Attach with `attachment_create`; `connection_invoke` remains schema-bound where mediated. Missing scope is `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","type":"sandbox_link","state":"ready","access":"full"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source→target port→scope→attach/test→detach/revoke. T3 Code/Herdr/SSH show explicit link and actual reachable service only.

### Modularity

#### PLG-001 — Customizable plugins with process isolation

**Statement.** Anyone must be able to write separately run plugins. Ordinary plugins must have no ambient host/sandbox authority. Trusted engines are the explicit assigned-RAM exception, admitted by exact digest with owner warning.
**Source.** Brief §12, A3; ADRs 0003, 0008; architecture §§3, 13.
**Interfaces.** Upload a verified plugin package as a Blob, then `lamp plugin create --package-blob-id bl_package --digest '<verified>' --json`; `POST /v1/plugins {"package_blob_id":"bl_package","digest":"<verified>"}`; `plugin_create` with that body. Manifest/provenance supplies type, trust class, protocol and immutable schema bindings. `plugin_enable` enables admitted permissions. `plugin_trust({"plugin_id":"pg_engine","digest":"<verified>","warning_acknowledged":true})` additionally requires owner authority for an engine. Unauthorized trust is `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"pg_01","kind":"plugin","type":"console","trust_class":"confined","state":"enabled"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard install→provenance/permissions/trust preview→enable/test→disable. Engine warning states it can read/change assigned RAM. T3 Code/Herdr/SSH keep unrelated sessions alive after plugin failure.

#### PLG-002 — Public plugin categories

**Statement.** Lamp must support engine, network, credential-source/vault, harness, kit, CI, trigger, console, access and notification-sink plugins under one versioned contract.
**Source.** Brief §§11–12, 15–18; ADRs 0002–0003, 0008; architecture §13.
**Interfaces.** `lamp plugin test pg_01 --suite-digest '<verified>' --json`; `POST /v1/plugins/pg_01:test {"suite_digest":"<verified>"}`; `plugin_test` adds `plugin_id`. `plugin_get` shows `type,protocol_version,trust_class,permissions,limits`; major protocol mismatch returns `INVALID_ARGUMENT`. Discover `adapter_list` and `adapter_get` (`lamp adapter list --json` and `lamp adapter get ad_01 --json`, `GET /v1/adapters` and item path); each immutable Adapter publishes `plugin_id,digest,operation_schemas,config_schema,result_schemas,compatibility`. Create Configuration with `lamp configuration create --input ./configuration.json --json`, `POST /v1/configurations`, or `configuration_create`: `name,plugin_id,adapter_id,schema_digest,document,credential_source_refs`. Inspect `configuration_get/list`; delete through `configuration_delete`. No Configuration/Adapter update exists. `config_ref={configuration_id,digest}` always selects an immutable ready validated document.
**JSON observation.** `{"data":{"id":"pg_01","kind":"plugin","type":"console","protocol_version":"lamp.plugin/1","state":"enabled"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard catalog groups the 10 canonical types and their granted objects. All consoles can discover/use a third-party console adapter without bespoke owner-project code.

### Harnesses, credentials and settings

#### CRED-001 — Any harness

**Statement.** Lamp must run official public harnesses without an owner-specific integration and provide working install, sign-in, useful tool work, session observation and supported continuation/spawn hooks.
**Source.** Brief §§13, 16, 22; ADRs 0003–0004; architecture §§9.2, 10.
**Interfaces.** `lamp harness list --json`; `GET /v1/harnesses`; `harness_list({})` returns version/digest/auth support records. `session_create` selects returned `harness_id`; unsupported route is `AUTH_TRANSPORT_UNSUPPORTED`, continuation gap `CONTINUATION_UNSUPPORTED`.
**JSON observation.** `{"items":[{"id":"hr_01","kind":"harness","version":"<pinned>","binary_digest":"<actual>"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard chooses harness/version/auth mode and shows supported platforms and modes. T3 Code/Herdr/SSH open each real harness and its distinct native sub-agents.

#### CRED-002 — Vendor-approved sign-in catalogue

**Statement.** Harness sign-in and API routes must follow the vendor's permitted methods and show actual supported capabilities. Unsupported methods fail without a raw-secret compatibility fallback.
**Source.** Brief §13; ADR 0004 amended; architecture §§9.1–9.2.
**Interfaces.** `lamp harness get hr_01 --json`; `GET /v1/harnesses/hr_01`; `harness_get` exposes `auth_modes,permission_sources,hooks`. `auth_flow_create` starts approved native route; `AUTH_TRANSPORT_UNSUPPORTED` names the absent method safely.
**JSON observation.** `{"error":{"code":"AUTH_TRANSPORT_UNSUPPORTED","message":"Auth mode has no supported transport","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard route→vendor URL/permission date→native sign-in→real auth probe. Consoles see signed-in readiness only after actual turn, never because a token file exists.

#### CRED-003 — Subscription custody and vendor-only use

**Statement.** Subscription logins must stay on the person's explicitly trusted custody hosts and own sandboxes, with vendor-only data egress. Lamp-run servers must never store, refresh or proxy them.
**Source.** Brief §13, A1; ADR 0004 amended; architecture §§8, 9.2.
**Interfaces.** `lamp credential-source get cr_01 --json`; `GET /v1/credential-sources/cr_01`; `credential_source_get` exposes `custody_host_ids` and state, never bearer. Subscription recipe uses `auth_mode=subscription`; untrusted placement returns `HOST_NOT_TRUSTED`.
**JSON observation.** `{"data":{"id":"cr_01","kind":"credential_source","custody_host_ids":["hs_01"],"state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows subscription exception, trusted host set and vendor-only policy. T3 Code/Herdr/SSH show token-free task work separately from credential-bearing harness execution.

#### CRED-004 — Private capture versus clean transfer

**Statement.** Same-person private fork/suspend must retain login under A1. Templates, saved-state exports beyond trusted hosts and any cross-person share/handoff must be clean and start the harness fresh.
**Source.** Brief §§6, 8–9, 13, A1; ADR 0004 amended; architecture §6.3.
**Interfaces.** `lamp snapshot create --sandbox-id sb_01 --durability local --json`; `POST /v1/snapshots {"sandbox_id":"sb_01","durability":"local"}`; `snapshot_create`. Read data `{"capture_trust":"owner_private","preserves_memory":true,"custody_host_ids":["hs_01"]}`. Unsafe transferable save returns `CLEAN_REBUILD_REQUIRED` before publication.
**JSON observation.** `{"data":{"id":"sn_01","kind":"snapshot","capture_trust":"owner_private","preserves_memory":true,"custody_host_ids":["hs_01"],"state":"ready"},"request_id":"rq_01"}` Then separately call `share_create({"resource_kind":"session","resource_id":"se_01","subject_id":"pr_bea","actions":["session.watch"],"expires_at":"<t0+60s>"})`, wait, and `share_get` the returned ID. It reads `state=pending_clean_handoff` until clean handoff completes; snapshot creation alone never creates a Share.
**Visual/consoles.** Dashboard previews retained private login versus clean fresh-harness transfer. Sharing stays `pending_clean_handoff` until reconstruction/continuation completes. All consoles retain same-owner task state and never send token-bearing snapshot/transcript to another person.

#### CRED-005 — API keys stay behind a host proxy

**Statement.** API/model/CLI keys must remain in an owner broker. Guests must receive only useless sentinels and scoped transport-bound authority; unsupported clients must never get a real-key fallback.
**Source.** Brief §§13–14; ADRs 0002, 0004; architecture §9.1.
**Interfaces.** `lamp connection test cn_01 --json`; `POST /v1/connections/cn_01:test {}`; `connection_test` adds `connection_id`. Read Connection contains references, not secrets. Wrong destination/action returns `SCOPE_DENIED`; unsupported TLS/auth returns `AUTH_TRANSPORT_UNSUPPORTED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","credential_source_id":"cr_01","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source→scope→test→attach shows protected mode. T3 Code/Herdr/SSH see real authorized results and redacted audit, never raw API keys in output/env/files/argv.

#### CRED-006 — One-way settings and approved return

**Statement.** Chosen settings must copy one way into sandboxes. Returning sandbox edits must require an authenticated explicit selected-path approval and matching source revision. Signing keys must stay outside task memory.
**Source.** Brief §13; ADR 0004; architecture §9.4.
**Interfaces.** `lamp settings-source create --host-id hs_01 --source-ref fixture-settings --path-allowlist-json '["AGENTS.md","harness.json"]' --json`; `POST /v1/settings-sources` same fields; `settings_source_create`. `settings_change_apply({"settings_change_id":"sc_01","selected_paths":["AGENTS.md"],"approval_id":"ap_01"})` returns Operation. Stale base is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"sc_01","kind":"settings_change","state":"proposed","base_revision":1,"selected_paths":["AGENTS.md"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source/categories→preview copy→sandbox diff→approve selected paths→apply/reject. T3 Code/Herdr show trusted diff/decision; SSH links to approval or uses scoped CLI, without automatic writeback.

### Connections

#### CON-001 — Signed-in MCP gateway and limited-token exception

**Statement.** External MCP servers must use a host gateway with host-held OAuth/tokens. A server that must run inside may receive only explicit limited scoped short-lived authority.
**Source.** Brief §14; ADRs 0002, 0004; architecture §9.1.
**Interfaces.** `lamp connection create --type mcp --endpoint '<approved-MCP-endpoint>' --credential-source-id cr_01 --adapter-id ad_01 --allowed-operations-json '["read_issue"]' --access read_only --json`; `POST /v1/connections` with those fields; `connection_create`. Attach via `attachment_create`; invoke `connection_invoke({"connection_id":"cn_01","attachment_id":"at_01","operation":"read_issue","arguments":{"issue":1}})` only if that exact adapter schema declares it. Unauthorized tools fail `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","type":"mcp","access":"read_only","state":"ready","allowed_operations":["read_issue"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard discover/auth/test→tool/resource scope→attach/revoke; all consoles see usable signed-in tool result and limited-token exposure classification, never host gateway bearer.

#### CON-002 — Signed-in CLI connections

**Statement.** `gh`, `stripe` and `aws` must complete supported scoped operations while their API tokens remain host-side. Supported operation lists and exclusions must be explicit.
**Source.** Brief §14; ADRs 0002, 0004; architecture §9.1.
**Interfaces.** `lamp connection create --type cli --endpoint '<approved-endpoint>' --adapter-id ad_aws --credential-source-id cr_01 --access read_only --json`; API `/v1/connections` same fields; `connection_create`. `connection_invoke` uses adapter `operation,arguments`; no generic arbitrary signing endpoint. Export/mint attempt returns `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","type":"cli","adapter_id":"ad_aws","access":"read_only","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard CLI adapter→account/service/region/resource/actions→test→attach. T3 Code/Herdr/SSH run actual CLI commands and see scoped success/denial, not raw tokens.

#### CON-003 — Owner-machine services and four access levels

**Statement.** Each owner-machine service must offer `off`, `read_only`, `ask_first` or `full`, enforced on real operations. Raw TCP must not claim semantic read-only.
**Source.** Brief §14; ADR 0002; architecture §9.1.
**Interfaces.** `lamp connection create --type service --endpoint '<exact-service>' --access ask_first --adapter-id ad_db --json`; API `/v1/connections` same fields; `connection_create`. `connection_invoke` with exact adapter operation requires matching `approval_id`; absent approval is `APPROVAL_REQUIRED`.
**JSON observation.** `{"error":{"code":"APPROVAL_REQUIRED","message":"Exact service action requires approval","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard endpoint→access→semantic-role preview→test/attach. T3 Code/Herdr/SSH show pending ask-first with inspect/approve/deny link and actual DB result.

### Kits

#### KIT-001 — Logged-in browser outside the agent sandbox by default

**Statement.** Default logged-in browsing must use a separate locked-down browser/profile with protected origin/action controls. Cookies must never reach task memory or agent observations.
**Source.** Brief §15; ADRs 0002, 0004; architecture §9.3.
**Interfaces.** `lamp browser create --task-id tk_01 --sandbox-id sb_01 --kit-id kt_browser --adapter-id ad_site --allowed-origins-json '["https://site.example.org"]' --json`; API `/v1/browsers` same fields; `browser_create`. `browser_action` accepts only `navigate,click,type,read,screenshot,download` under adapter schema. Raw cookie/CDP/evaluate requests fail `SCOPE_DENIED` or `INVALID_ARGUMENT`.
**JSON observation.** `{"data":{"id":"br_01","kind":"browser","state":"agent_control","sandbox_id":"sb_01","allowed_origins":["https://site.example.org"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard browser opens approved remote view and clearly shows agent/human control. T3 Code/Herdr embed/link that view; SSH provides private takeover URL. Unsafe password workflows enter private human control [owner-confirmed B3].

#### KIT-002 — Opt-in browser inside with copied logins

**Statement.** Browser login copying into the task sandbox must require a separate authenticated Exposure, with persistent warning/taint. No recipe/sub-agent default may enable it.
**Source.** Brief §15, O5, A1; ADR 0004 amended; architecture §§6.3, 9.3.
**Interfaces.** `lamp exposure create --sandbox-id sb_01 --generation 1 --type browser_logins --destination '<approved-profile>' --json`; `POST /v1/exposures` same fields; `exposure_create`. `exposure_activate` needs matching approval/action digest. Missing permission returns `APPROVAL_REQUIRED` or `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"ep_01","kind":"exposure","type":"browser_logins","state":"pending_approval","sandbox_id":"sb_01","generation":1},"request_id":"rq_01"}` Before activation there is no `activated_at` and no cookie copy.
**Visual/consoles.** Dashboard explains readable copied logins before activation and keeps warning after revoke. T3 Code/Herdr/SSH show active exposure in session detail; it is never labeled protected-cookie mode.

#### KIT-003 — Human browser takeover

**Statement.** A person must take exclusive private control for multifactor authentication (MFA), human-verification challenge (CAPTCHA) or unsafe password entry, then return control only after sensitive observations are cleared.
**Source.** Brief §15; ADR 0004; architecture §9.3.
**Interfaces.** `lamp browser takeover br_01 --lease-id le_human --json`; `POST /v1/browsers/br_01:takeover {"lease_id":"le_human"}`; `browser_takeover` adds `browser_id`. Return via `browser_return_control`. Stale/wrong holder action fails `SCOPE_DENIED` or `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"br_01","kind":"browser","state":"human_control","control_lease_id":"le_human"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr show agent paused and trusted private person view; phone uses same authenticated view. SSH displays short-lived private URL. Person types password if secrecy of automated fill cannot be guaranteed [owner-confirmed B3].

#### KIT-004 — Toolchains and Docker inside a sandbox

**Statement.** Kits must install requested tools and saved state. Optional Docker must run only inside the sandbox, with no host daemon socket or bypass of host policies.
**Source.** Brief §§8, 15, 18; ADRs 0002–0003, 0007; architecture §§6.1, 9.
**Interfaces.** `lamp attachment create --sandbox-id sb_01 --resource-kind kit --resource-id kt_docker --json`; `POST /v1/attachments` same fields; `attachment_create`. Ready checks include kits. Unsupported device/VM request returns `DEVICE_UNAVAILABLE`/`VM_REQUIRED`.
**JSON observation.** `{"data":{"id":"at_01","kind":"attachment","resource_kind":"kit","resource_id":"kt_docker","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard kit picker→verified plan/version→install progress→test/remove. T3 Code/Herdr/SSH run tool version, `docker build` and Compose service output inside the guest.

#### KIT-005 — Password managers, fill-on-behalf and per-vault approvals

**Statement.** 1Password, Bitwarden, KeePassXC and Proton Pass must provide useful protected login. Standard forms use external fill; unsafe forms use private person typing. Password must never be exposed to agent [owner-confirmed B3]. Per-secret injection must remain a separate opt-in Exposure.
**Source.** Brief §15, O5; ADR 0004; architecture §9.3, modified default B3.
**Interfaces.** `lamp browser fill-password br_01 --credential-source-id cr_vault --secret-ref login_ref --action-digest '<actual-digest>' --approval-id ap_01 --json`; `POST /v1/browsers/br_01:fill-password` with same fields; `browser_fill_password` adds `browser_id`. Pending per-vault decision returns `APPROVAL_REQUIRED`; unsupported safe-fill transitions to human control [owner-confirmed B3].
**JSON observation.** `{"data":{"id":"ap_01","kind":"approval","state":"consumed","resource_ref":{"kind":"credential_source","id":"cr_vault"}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard vault→each-use toggle→exact origin/frame/form/action→phone/console approval→private fill/submit or takeover→authenticated task. T3 Code/Herdr show decision and safe result; SSH links trusted view without secret text [owner-confirmed B3].

### Consoles and sessions

#### SES-001 — One machine in any console

**Statement.** Console plugins must present the fleet as one machine while exposing every authorized Session independently, including sessions on different hosts.
**Source.** Brief §§5, 16; ADR 0001; architecture §10.
**Interfaces.** `lamp session list --json`; `GET /v1/sessions`; `session_list({})` returns object `items` with `id,sandbox_id,location,status`. Foreign session get returns `NOT_FOUND`.
**JSON observation.** `{"items":[{"id":"se_01","kind":"session","sandbox_id":"sb_01","status":"working"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard session list and T3 Code/Herdr one-machine lists match IDs/parentage. SSH `sessions` enumerates the same authorized set. Install/configure console plugin once per fleet, not per host.

#### SES-002 — Every session visible, live and interactive

**Statement.** Every session must support live watch and authorized input with working/blocked/idle status and truthful observation freshness.
**Source.** Brief §16; ADR 0001; architecture §§10, 12.4.
**Interfaces.** `lamp session watch se_01 --json`; `GET /v1/sessions/se_01/output?cursor=c1`; `session_watch({"session_id":"se_01","cursor":"c1","limit":100,"wait_ms":25000})` returns framed `items,next_cursor`. `session_input` requires lease/location epoch/sequence/base64. Wrong writer is `SCOPE_DENIED`.
**JSON observation.** `{"items":[{"stream_id":"st_01","sequence":1,"channel":"stdout","data_base64":"NDIK","byte_offset":0,"location_epoch":1}],"next_cursor":"c2","request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr/SSH open/watch/type/resize/interrupt/reconnect same Session. Status carries `status_reason,observed_at,observation_state`; no-output is not automatically idle or completed.

#### SES-003 — Ordinary SSH everywhere

**Statement.** Stock SSH must list sessions, attach live or open a sandbox shell on every access route without patched terminals, mandatory custom ProxyCommand or host shell access.
**Source.** Brief §§16–17; ADRs 0001, 0009; architecture §§10, 15.2.
**Interfaces.** CLI parity `lamp session attach se_01 --lease-id le_01 --json`; API `POST /v1/sessions/se_01:attach {"lease_id":"le_01"}`; MCP `session_attach` adds `session_id`, result StreamDescriptor. SSH uses the exact common `sessions`, `attach <session_id>`, `shell <sandbox_id>` grammar. Unauthorized attach returns denial with no output.
**JSON observation.** `{"operation_id":"op_attach","resource_ids":[{"kind":"session","id":"se_01"}],"request_id":"rq_attach","replayed":false,"result":{"stream":{"stream_id":"st_01","transport":"websocket","url":"wss://api.f123.fleet.eilmer.si/v1/sessions/se_01/output","expires_at":"<t0+30s>","cursor":"c1","max_frame_bytes":69632,"max_payload_bytes":65536}}}` The substituted expiry is 30 s after the authoritative fixture clock; the URL has no bearer.
**Visual/consoles.** Dashboard AccessRoute displays returned SSH host/port/fingerprint. T3 Code/Herdr may use console plugin or ordinary SSH; terminal selector opens each live session.

#### SES-004 — Caller-chosen sub-agent placement and git return

**Statement.** Sub-agents must run in their own sandbox or share the parent's as the starter chooses. Own mode must copy consistent selected files and return changes through reviewable git state.
**Source.** Brief §16; ADRs 0001, 0004; architecture §10.1.
**Interfaces.** `lamp session spawn se_01 --sandbox-mode own --recipe-id rc_01 --selected-paths-json '["src","tests"]' --json`; `POST /v1/sessions/se_01:spawn {"sandbox_mode":"own","recipe_id":"rc_01","selected_paths":["src","tests"]}`; `session_spawn` adds `session_id`. Result `{"session_id":"se_02","sandbox_id":"sb_02"}`; unsupported native hook is `CONTINUATION_UNSUPPORTED`, never silent shared mode.
**JSON observation.** `{"data":{"id":"se_02","kind":"session","parent_session_id":"se_01","sandbox_id":"sb_02","task_id":"tk_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard parent tree→own/shared authority disclosure→spawn→returned diff/apply/reject. T3 Code/Herdr/SSH see native and explicit sub-agents separately with independent input/output.

#### SES-005 — Teleport between laptop and sandbox

**Statement.** Teleport must move stable logical harness continuation both directions with one authoritative location, preserved acknowledged messages/files/tool outcomes and locally approved auth.
**Source.** Brief §16; ADRs 0001, 0004; architecture §10.2.
**Interfaces.** For host destination, first obtain destination-owner Approval and `native_execution_grant_create` for `host_id,user,working_paths,harness_digest,expires_at,approval_id`; it also needs `native_execution_grant.create`. Then `lamp session teleport se_01 --location-json '{"kind":"host","host_id":"hs_laptop"}' --native-execution-grant-id ng_01 --json`; `POST /v1/sessions/se_01:teleport {"location":{"kind":"host","host_id":"hs_laptop"},"native_execution_grant_id":"ng_01"}`; `session_teleport` adds `session_id`. Read `native_execution_grant_get/list` and delete to revoke. Sandbox destination uses `location={kind:sandbox,host_id,sandbox_id}`. Unsupported continuation is `CONTINUATION_UNSUPPORTED`. NativeExecutionGrant reads expose `principal_id,host_id,user,working_paths,harness_digest,action_digest,approval_id,expires_at,state`; state is exactly `active|revoking|revoked|expired`, and delete reports revocation status rather than silently discarding live authority.
**JSON observation.** `{"data":{"id":"se_01","kind":"session","location_epoch":2,"location":{"kind":"host","host_id":"hs_laptop"},"state":"active"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard Move→tools/auth/destination preview→checkpoint/stage/fence/commit/resume→same Session at new location. T3 Code/Herdr/SSH reconnect by stable ID/cursor; native laptop execution is clearly host authority.

#### SES-006 — Own sessions by default

**Statement.** Team and public-service callers must see only their own sessions unless explicitly granted content access. Admin roles alone must not reveal another person's work.
**Source.** Brief §§16–17, O9; ADRs 0001, 0005; architecture §§10, 15.1.
**Interfaces.** `lamp session list --json`; `GET /v1/sessions`; `session_list({})` filters `items`. Foreign `session_get`, `session_watch` and `session_attach` returns `NOT_FOUND`, including event replay/search-derived paths.
**JSON observation.** `{"error":{"code":"NOT_FOUND","message":"Resource not found","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr/SSH show own sessions; explicit shares appear with grant source and expiry. Empty/private views leak no names, statuses, transcripts or counts.

#### SES-007 — Explicit watch and type sharing

**Statement.** Sharing must grant watch and type separately, with expiring/revocable authority. Credential-bearing sessions must complete a clean fresh-harness handoff before any cross-person sharing activates.
**Source.** Brief §16, A1, O9; ADRs 0004 amended, 0005; architecture §§6.3, 10, 17.3.
**Interfaces.** `lamp share create --resource-kind session --resource-id se_01 --subject-id pr_bea --actions-json '["session.watch"]' --expires-at <t0+60s> --json`; `POST /v1/shares` same fields; `share_create`. `share_revoke` starts pending revocation. Watch-only input fails `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"sh_01","kind":"share","state":"active","actions":["session.watch"],"resource_kind":"session","resource_id":"se_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard Share→person/watch-or-type/action authority/expiry→clean-handoff progress→active→revoke. T3 Code/Herdr/SSH watchers receive no input lease; typing does not grant shell/files/vault/browser/approval.

### Access, accounts and keys

#### ACC-001 — Built-in Tailscale access and keyless CI connectivity

**Statement.** Built-in Tailscale access must reach NAT-hidden hosts without manual inbound setup and support supported CI federation without stored secrets.
**Source.** Brief §17; ADRs 0001–0002; architecture §§8.1, 15.1.
**Interfaces.** `lamp access-route create --type tailscale --config-ref-json '{"configuration_id":"cfg_tailnet","digest":"<verified>"}' --json`; `POST /v1/access-routes {"type":"tailscale","config_ref":{"configuration_id":"cfg_tailnet","digest":"<verified>"}}`; `access_route_create`. Read data `{"state":"ready","ssh_port":22}`. Missing federation cannot silently inject a saved key; exchange fails `UNAUTHENTICATED`/`SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"tailscale","ssh_port":22,"state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard bootstrap→tags/service approval→ready→remove, with network prerequisites. T3 Code/Herdr/SSH connect to one fleet identity behind NAT.

#### ACC-002 — Owner domain with automatic HTTPS

**Statement.** Owners must use their own domain with automatic valid HTTPS while retaining common fleet identity, authorization and SSH routing.
**Source.** Brief §17; ADRs 0001, 0006, 0009; architecture §§14.3, 15.
**Interfaces.** `lamp access-route create --type owner_https --config-ref-json '{"configuration_id":"cfg_domain","digest":"<verified>"}' --json`; `POST /v1/access-routes` same fields; `access_route_create`. Read `address,ssh_host,ssh_port,trust_fingerprint,state`. Issuance failure stays failed, never insecure ready.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"owner_https","state":"ready","address":"<owner-selected-HTTPS-address>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard domain/DNS validation→certificate/progress→API/dashboard/SSH instructions→renew/failure. T3 Code/Herdr/SSH use advertised endpoints, same session IDs and host trust.

#### ACC-003 — Local-only, offline access

**Statement.** Non-relay fleets must work locally and offline with local identities or owner OIDC independent of Eilmer/Clerk.
**Source.** Brief §17; ADR 0005; architecture §15.1.
**Interfaces.** `lamp access-route create --type local --json`; `POST /v1/access-routes {"type":"local"}`; `access_route_create`. Local `sandbox_create` works with valid enrolled key; absent identity returns `UNAUTHENTICATED`.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"local","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard local setup/login→create/session→offline indicator. T3 Code/Herdr/SSH access local LAN/loopback route without relay sign-in.

#### ACC-004 — Cloudflare Tunnel access plugin

**Statement.** Cloudflare Tunnel must be a separately configured access plugin with an explicit termination/trust statement, common scopes and an working stock-SSH route.
**Source.** Brief §17; ADRs 0002, 0009; architecture §§15.1, 20.2.
**Interfaces.** `lamp access-route create --type cloudflare --plugin-id pg_cf --config-ref-json '{"configuration_id":"cfg_cf","digest":"<verified>"}' --json`; API `/v1/access-routes` with same fields; `access_route_create`. Read `address,ssh_host,ssh_port,state`; unsupported transport remains unavailable rather than silently requiring a patched SSH client.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"cloudflare","state":"failed"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard install/configure→TLS termination disclosure→test HTTP and SSH→ready/disable. T3 Code/Herdr/SSH retain same fleet/session scopes.

#### ACC-005 — One Eilmer account; independent identity without relay

**Statement.** Relay use must require the one shared Clerk Eilmer account. Non-relay fleets must also support local accounts and own OIDC. Identity must be immutable issuer+subject, not email matching.
**Source.** Brief §17; ADRs 0005, 0009; architecture §15.
**Interfaces.** `lamp identity-provider create --type oidc --issuer '<owner-issuer>' --client-id lamp --discovery-url '<owner-discovery>' --json`; API `/v1/identity-providers` same fields; `identity_provider_create`. `auth_flow_complete` validates code/state/proof; wrong issuer/audience fails `UNAUTHENTICATED`. Emergency root recovery is local only: `auth_flow_bootstrap` with `purpose=fleet_recovery` and enrolled recovery-key thumbprint, then `lamp fleet recover fl_01 --input ./recovery.json --json`, `POST /v1/fleets/fl_01:recover` over the local recovery socket, or `fleet_recover` with `auth_flow_id,proof,new_root_public_key,fence_proofs`. No ordinary fleet key or relay route can use this exception.
**JSON observation.** `{"data":{"id":"pr_01","kind":"principal","issuer":"<verified-issuer>","subject":"<immutable-subject>","disabled":false},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard chooses Eilmer/local/owner provider appropriately; relay sign-in cannot skip host enrollment. T3 Code/Herdr/SSH enrolled key maps to same Principal and explicit actions.

#### ACC-006 — Owner-configured sharing modes

**Statement.** Owner must configure single-owner, team or public-service mode with explicit admission and the accepted owner/admin/member/viewer role matrix. Mode changes must never publish existing private content.
**Source.** Brief §17, O9; ADRs 0001, 0005; architecture §15.1.
**Interfaces.** `lamp fleet update fl_01 --mode team --json`; `PATCH /v1/fleets/fl_01 {"mode":"team"}`; `fleet_update` adds `fleet_id`. Team `invitation_create` and `invitation_accept` binds target key/role/expiry; public `principal_create` and `membership_create` uses owner admission policy. Unauthorized elevation is `SCOPE_DENIED`. Owner transfer is `lamp fleet transfer-owner fl_01 --input ./transfer.json --json`, `POST /v1/fleets/fl_01:transfer-owner`, or `fleet_transfer_owner` with `new_owner_principal_id,acceptance_proof,approval_id`. `acceptance_proof={public_key,signature}` signs canonical `fleet_id,authority_epoch,new_owner_principal_id,action_digest` with the new owner's enrolled key. A one-use Approval shows the actual transfer.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","mode":"team","revision":2},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard mode→admission/roles/quota preview→invite/accept/remove; public-service registration shows isolated quota. T3 Code/Herdr/SSH views remain own-only unless explicit content share.

#### ACC-007 — Scoped caller keys

**Statement.** Keys must narrow actions, sandboxes, recipes, hosts, resources, limits and expiry. Delegation/rotation must never broaden authority; secrets must be shown once and hashed at rest.
**Source.** Brief §17; ADRs 0001, 0005; architecture §§11–12, 15.1.
**Interfaces.** `lamp key create --principal-id pr_01 --scope-json '{"actions":["sandbox.create","exec.create"],"recipe_ids":["rc_01"],"host_ids":["hs_01"],"limits":{"concurrency":2}}' --expires-at <t0+60s> --json`; API `/v1/keys` same fields; `key_create`. Result includes transient `secret` once. `key_rotate` creates equivalent/narrower key and revokes old; invalid action/resource returns `SCOPE_DENIED`.
**JSON observation.** `{"operation_id":"op_01","resource_ids":[{"kind":"key","id":"ky_01"}],"replayed":true,"result":{"secret_available":false},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard key wizard→effective intersection/expiry→one-time copy→rotate/revoke acknowledgments. T3 Code/Herdr/SSH use same action scopes; transport membership confers no admin.

#### ACC-008 — Bound one-use approvals [added]

**Statement.** Protected actions must use authenticated inspectable approve/deny/revoke and one-use consumption bound to principal, task, generation, resource, exact action/destination and expiry.
**Source.** Brief §§13–16, 19; ADRs 0002, 0004–0005; architecture §§9.3, 11–12, 14.
**Interfaces.** `lamp approval create --input ./approval.json --json`; `POST /v1/approvals`; `approval_create` takes the same body: `{"principal_id":"pr_01","task_id":"tk_01","sandbox_id":"sb_01","generation":1,"resource_ref":{"kind":"connection","id":"cn_01"},"action_plan":{"adapter_digest":"<verified>","operation":"read_issue","resource_ref":{"kind":"connection","id":"cn_01"},"arguments":{"issue":1},"destination":"<exact>","secret_refs":[],"redacted_fields":[]},"destination":"<exact>","expires_at":"<t0+60s>"}`. The adapter validates the proposed plan and derives `action_digest`; create never takes an authoritative caller digest. `approval_get` inspects; `approval_approve({"approval_id":"ap_01","action_digest":"<returned>"})`, `approval_deny`, `approval_revoke` decide. Missing approval gives `APPROVAL_REQUIRED`.
**JSON observation.** `{"data":{"id":"ap_01","kind":"approval","state":"denied","action_digest":"<actual>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr trusted approval card shows effect/recipient/expiry independently of task-supplied text. Phone opens same authenticated view; SSH uses scoped CLI or private link. Watch permission alone cannot approve.

#### ACC-009 — Active revocation and narrowing [added]

**Statement.** Revoke must stop derived authority, not merely delete a record. Pending and completed outcomes must expose online acknowledgments and remaining offline expiry.
**Source.** Brief §§11, 14, 16–17, 19; ADRs 0001–0002, 0004–0005; architecture §17.3.
**Interfaces.** `lamp grant revoke gr_01 --reason removed --json`; `POST /v1/grants/gr_01:revoke {"reason":"removed"}`; `grant_revoke` adds `grant_id`. `share_revoke`, `connection_revoke`, `key_delete`, `host_delete` and membership removal cascade. Inspect Operation `state,progress,outcome`; no false completion while offline authority survives.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"waiting","revocation_status":{"enforcers":[{"host_id":"hs_02","enforcer":"guard","acknowledgment":"pending","authority_expires_at":"<t0+30s>","residual_capabilities":[{"provider":"fixture","resource_ref":{"kind":"connection","id":"cn_01"},"capability_kind":"fixture_remote_action","revoke_state":"unknown"}]}],"complete":false}},"request_id":"rq_01"}` Unknown provider expiry is omitted, never guessed; adapter-defined fixture capability kind is not a new operation.
**Visual/consoles.** Dashboard revoke impact→pending hosts/leases→complete at acknowledgments/expiry. T3 Code/Herdr/SSH close affected stream/input, remove shares and explain denied reconnect.

### Relay

#### RLY-001 — Relay reachability, identity and one-time credentials

**Statement.** Optional Eilmer/self-hosted relay must connect NAT-hidden hosts, require Eilmer account and independent fleet enrollment, and issue only short-lived one-use routing grants.
**Source.** Brief §17; ADRs 0005, 0008–0009; architecture §15.2.
**Interfaces.** `lamp access-route create --type relay --config-ref-json '{"configuration_id":"cfg_relay","digest":"<verified>"}' --json`; `POST /v1/access-routes {"type":"relay","config_ref":{"configuration_id":"cfg_relay","digest":"<verified>"}}`; `access_route_create`. `auth_flow_exchange` uses challenge/audience/proof; read AccessRoute provides host/port/fingerprint. Replayed grant fails `UNAUTHENTICATED`/`SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"relay","state":"ready","ssh_host":"ssh.f123.fleet.eilmer.si","ssh_port":22418},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard local fingerprint/QR pairing→Eilmer sign-in/link→route ready. T3 Code/Herdr/SSH enroll caller separately and use returned route; a relay login alone cannot attach sessions.

#### RLY-002 — Relay never decrypts or holds sandbox/model data

**Statement.** The relay must forward ciphertext only. Caller-to-host TLS/SSH keys, model credentials, files and transcripts must never be held by relay or Lamp-run credential services.
**Source.** Brief §17; ADRs 0004, 0009; architecture §§15.2–15.3.
**Interfaces.** `lamp access-route get ar_route --json`; `GET /v1/access-routes/ar_route`; `access_route_get` returns `trust_fingerprint`, not inner keys. `sandbox_file_transfer`/session streams through that route retain canonical payloads inside encryption. Substitute endpoint fails `UNAUTHENTICATED`/TLS trust before effect.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"relay","trust_fingerprint":"<independently-pinned>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows independently pinned fleet identity and trusted issuance boundary. T3 Code/Herdr/SSH reject changed root/host CA; ordinary browser relies on trusted account code/domain/CA as disclosed.

### CI and triggers

#### CI-001 — Shipped CI plugins and extensible coverage

**Statement.** Lamp must ship native adapters for every named CI family, including native result, cancellation and one-job runner lifecycle, and let others add plugins.
**Source.** Brief §18; ADRs 0001, 0003, 0008; architecture §16.
**Interfaces.** `lamp job create --plugin-id pg_gitlab --external-id fixture-job --recipe-id rc_ci --json`; `POST /v1/jobs` same fields; `job_create`. `job_get` exposes `queued|preparing|running|collecting|completed|failed|canceled|cleanup_failed`. Duplicate external claim returns original operation, not second runner.
**JSON observation.** `{"data":{"id":"jb_01","kind":"job","state":"running","sandbox_id":"sb_01","task_id":"tk_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard CI plugin→controller registration/queue→job→logs/results/artifacts→deregister/reconcile. T3 Code/Herdr/SSH watch actual job/action sessions and final result.

#### CI-002 — GitHub Actions and coding-agent routes

**Statement.** GitHub Actions must support ephemeral one-job runners plus documented Copilot cloud agent, Claude Code Action and Codex Action routes under approved auth.
**Source.** Brief §18; ADRs 0003–0004; architecture §16.
**Interfaces.** `lamp plugin get pg_github --json`; `GET /v1/plugins/pg_github`; `plugin_get` shows pinned `github_actions` adapter. `job_create` maps verified demand to recipe/Task; invalid route/auth is `AUTH_TRANSPORT_UNSUPPORTED`.
**JSON observation.** `{"data":{"id":"jb_01","kind":"job","plugin_id":"pg_github","state":"completed","artifact_ids":["ar_01"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard GitHub demand→JIT runner→agent/action session→result→cleanup. T3 Code/Herdr/SSH show useful action output and native sub-agents, without an invented vendor-cloud placement promise.

#### CI-003 — GitHub OIDC exchange without stored Lamp secrets

**Statement.** GitHub workload identity must exchange for a short-lived scoped Lamp key without stored Lamp secrets. Trust must derive from immutable forge evidence and exact challenge-bound audience.
**Source.** Brief §17; ADRs 0001, 0005; architecture §15.1.
**Interfaces.** `lamp auth-flow exchange af_01 --input ./auth-exchange.json --json` (caller-local mode-0600 JSON contains the body below); `POST /v1/auth-flows/af_01:exchange {"jwt":"<job-JWT>","audience":"lamp:fl_01:<challenge>","proof":"<proof>"}`; `auth_flow_exchange` adds `auth_flow_id`. Result secret is transient once; wrong/replayed identity is `UNAUTHENTICATED`/`SCOPE_DENIED`. Begin from a clean job: generate an ephemeral Ed25519 proof key, then `lamp auth-flow bootstrap --input ./bootstrap.json --json`; `POST /v1/auth-flows:bootstrap`; `auth_flow_bootstrap` accepts `fleet_id,purpose=ci_exchange,proof_key_thumbprint,source_identity`. Return `{auth_flow_id,challenge,audience,expires_at,request_id}` through authenticated server TLS; no Lamp key, precreated flow, mutation key or Operation. Obtain the forge JWT for the returned audience, then exchange using the returned flow ID. `proof={public_key,signature}` is base64 Ed25519 over canonical `auth_flow_id,challenge,jwt_sha256,audience`; its public-key SHA-256 matches bootstrap. The durable exchange uses common idempotency authenticated by this proof.
**JSON observation.** `{"error":{"code":"UNAUTHENTICATED","message":"Workload identity rejected","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard job identity→repository/workflow/ref/SHA/policy→issued key scope/expiry. All consoles see Task owner and CI source, never exchange token. Public-PR job does not receive this Lamp key [owner-confirmed B2].

#### CI-004 — A fresh sandbox for every job

**Statement.** Each CI job must get a new private writable root and no prior task processes/state. Lasting caches/artifacts must use explicit storage resources.
**Source.** Brief §§9–10, 18; ADRs 0001, 0004; architecture §§6.2, 16.
**Interfaces.** `lamp job get jb_01 --json`; `GET /v1/jobs/jb_01`; `job_get` exposes distinct `sandbox_id,task_id,artifact_ids`. `sandbox_get` shows new generation/root provenance; requested unsafe reuse fails `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"jb_02","kind":"job","sandbox_id":"sb_02","task_id":"tk_02","state":"running"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard per-job sandbox/cache/output shows new execution and later deletion. T3 Code/Herdr/SSH never attach job 2 to job 1's live process or private root.

#### CI-005 — Stranger public-PR isolation

**Statement.** Verified public-PR work must use a fresh real VM, clean template, allowlist-only network and no owner/model/vault credentials. Only its CI system's own short-lived one-job authority may be job-readable: for example, a GitHub runner job token with its read-only repository token. No other secrets [owner-confirmed B2].
**Source.** Brief §§6, 11, 18, O3; ADRs 0002, 0004; architecture §16.2, modified B2 default.
**Interfaces.** `lamp job get jb_pr --json`; `GET /v1/jobs/jb_pr`; `job_get` reads `{"trust_class":"public_pr","sandbox_id":"sb_pr"}`. Protected attachment/fallback/exposure attempts fail `VM_REQUIRED`/`SCOPE_DENIED`; no override. No fictitious `secrets=false` badge hides CI token [owner-confirmed B2].
**JSON observation.** `{"data":{"id":"jb_pr","kind":"job","trust_class":"public_pr","sandbox_id":"sb_pr","state":"running"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows forge-verified trust, VM freshness, exact allowed token audience/actions/resource/expiry and denied attachments. T3 Code/Herdr/SSH show restricted job; any allowed CI token is disclosed as an exposure class [owner-confirmed B2].

#### CI-006 — Triggers and agent schedules

**Statement.** Slack, Linear, webhook and schedule triggers must launch scoped tasks after verified source identity, durable deduplication and current-authority checks. Timers must have explicit daylight saving time (DST)/missed/overlap rules.
**Source.** Brief §§18–19; ADRs 0001, 0005; architecture §16.1.
**Interfaces.** `lamp schedule create --principal-id pr_01 --recipe-id rc_01 --cron '0 9 * * *' --timezone America/Chicago --json`; `POST /v1/schedules` same fields; `schedule_create`. `trigger_deliver` carries `source_event_id,payload_blob_id,signature_metadata`; duplicate returns original Operation. New claim without quorum is `QUORUM_REQUIRED`.
**JSON observation.** `{"data":{"id":"sk_01","kind":"schedule","timezone":"America/Chicago","enabled":true},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source/auth/recipe/recipient→test→enable→next occurrence/run history→pause/resume/cancel. T3 Code/Herdr/SSH see resulting sessions with source identity and shared Task budget.

### Dashboard and operations

#### OPS-001 — Live dashboard of every sandbox, session and resource

**Statement.** The host-served dashboard must provide complete authorized create/use/change/remove/recover flows for all resources, through common operations and scopes.
**Source.** Brief §19; ADRs 0001–0002, 0005; architecture §§12, 17.4.
**Interfaces.** `lamp metric list --json`; `GET /v1/metrics`; `metric_list({})` exposes redacted local metrics; resource create/read/update/delete (CRUD)/actions use canonical dictionary. `operation_get` carries progress/errors/outcomes/revocation_status. Adapter and Configuration discovery/setup, NativeExecutionGrant admission, fleet transfer/recovery and prepare-fork all have the same authorized dashboard flows. Stale visual mutation returns `REVISION_CONFLICT` with no overwrite.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"preparing","phase":"network","progress":{"completed":3,"total":10,"unit":"checks"}},"request_id":"rq_01"}`
**Visual/consoles.** New sandbox→recipe edit→host/device/resources→effective policy→Create→checks→ready/session. Details allow fork/suspend/move/share/revoke; Connections/kits allow auth/test/attach/approval/takeover; Storage/jobs allow downloads/backup/fenced recovery; Access allows roles/keys/ports/revoke; Operations allows cancel/budget stop/notification retry. T3 Code/Herdr show same Session state; SSH links to graphical-only actions.

#### OPS-002 — Task budgets and watchdog

**Statement.** Tasks must share descendant time/token budgets, exact reservations on supported proxy routes and visible best-effort usage on opaque/outside routes. Hard time stop must stop all local descendants and authority.
**Source.** Brief §19, A4; ADR 0004; architecture §17.2.
**Interfaces.** `lamp budget create --wall-time-ms 3600000 --token-limit 1000 --token-mode exact --json`; `POST /v1/budgets` same fields; `budget_create`. `task_stop({"task_id":"tk_01","reason":"budget"})` returns Operation. Exact opaque request fails `TOKEN_CAP_UNENFORCEABLE` before work.
**JSON observation.** `{"data":{"id":"bu_01","kind":"budget","token_limit":1000,"token_mode":"exact","reserved_tokens":200,"used_tokens":300,"wall_time_ms":3600000},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard route/accounting boundary→limit/used/reserved/unknown→warning/stop/decision. T3 Code/Herdr/SSH show same root Task, reason and no reset on spawn/fork/move.

#### OPS-003 — Notifications for done, stuck and decisions

**Statement.** Done, stuck and needs-decision events must reach configured notification sinks with inspectable retries, acknowledgment and private authenticated action links.
**Source.** Brief §19; ADRs 0004–0005; architecture §17.4.
**Interfaces.** `lamp notification-sink create --plugin-id pg_notify --destination-ref fixture_phone --event-types-json '["task.done","task.stuck","task.needs_decision"]' --enabled true --json`; API `/v1/notification-sinks` same fields; `notification_sink_create`. `notification_retry` and `notification_acknowledge` expose state; failed delivery emits `notification.delivery_failed`.
**JSON observation.** `{"data":{"id":"nt_01","kind":"notification","state":"failed","attempt_count":6},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard sinks→test→event→attempts/retry/ack. T3 Code/Herdr trusted notification opens matching task/approval; SSH displays private link without bearer in task output.

#### OPS-004 — Per-sandbox process, connection and request audit

**Statement.** Audit must record every process start with program, arguments, user, parent and time [owner-confirmed B4], every network connection with destination/bytes/timing, and full redacted request semantics where Lamp handles plaintext. Subscription TLS must stay opaque.
**Source.** Brief §19, A4; ADRs 0002, 0004; architecture §§11–12, 17.1; B4 default.
**Interfaces.** `lamp audit-record list --json`; `GET /v1/audit-records`; `audit_record_list({})` returns attributable items; `audit_record_export` uses a scoped filter. Canonical `process_start` records every process creation, including fork/clone without exec; `process_exec` adds executable transitions. Both use `process_id,parent_process_id,process_instance_id,parent_process_instance_id,executable,executable_digest,argv,user,runtime,exit_code` as applicable. Collector failure yields `AUDIT_UNAVAILABLE` and `audit.gap`.
**JSON observation.** `{"data":{"id":"au_01","kind":"audit_record","type":"process_start","visibility":"process","sandbox_id":"sb_01","generation":1,"started_at":"2026-11-08T19:00:00Z","details":{"process_id":42,"parent_process_id":1,"process_instance_id":{"boot_id":"boot_01","pid":42,"start_sequence":2},"parent_process_instance_id":{"boot_id":"boot_01","pid":1,"start_sequence":1},"executable":"/usr/bin/python3","executable_digest":"<actual>","argv":["python3","probe.py"],"user":"1000","runtime":{"kind":"native"}}},"request_id":"rq_01"}` Historical start time is illustrative.
**Visual/consoles.** Dashboard sandbox/task audit→process tree/flow/request/control→redacted detail/export/gap. T3 Code/Herdr/SSH correlate command/output to record and visibility; opaque flow is never labeled full HTTP request.

#### OPS-005 — Hosted domains, cookie/origin isolation and certificates

**Statement.** All Eilmer-hosted surfaces must stay under eilmer.si with protected account/product cookies, isolated untrusted origins and shared-domain certificate limits. Every Eilmer-hosted page stays under eilmer.si. An untrusted sandbox page must never obtain or replace an account login. Each fleet uses one wildcard certificate, so creating sandbox web addresses does not consume a certificate each time.
**Source.** Brief §20; ADRs 0005–0006, 0009; architecture §15.3.
**Interfaces.** `lamp port get po_01 --json`; `GET /v1/ports/po_01`; `port_get` reads `origin=https://p-<random>.f123.fleet.eilmer.si`. AccessRoute exposes `api.f123.fleet.eilmer.si`; certificate exhaustion leaves preparing/failed, never insecure ready.
**JSON observation.** `{"data":{"id":"po_01","kind":"port","origin":"https://p-random.f123.fleet.eilmer.si","access":"private","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows hosted activation/issuer queue and unique content origin. T3 Code/Herdr/SSH use same origin/link and independent trust fingerprints; no alternate hosted domain offered.

#### OPS-006 — No payments yet; future business support

**Statement.** Lamp must require no payment system and retain extensible plans/quotas/billing boundaries through the usage/admission ledger.
**Source.** Brief §20; ADRs 0001, 0008; architecture §17.2.
**Interfaces.** `lamp fleet get fl_01 --json`; `GET /v1/fleets/fl_01`; `fleet_get` exposes `limits`, not a payment prerequisite. `budget_get` exposes usage and the canonical token home. Exceeding quota yields `NO_CAPACITY`/`CONTROL_QUOTA_EXCEEDED`, never payment prompt.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","limits":{"concurrency":4}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard local quota/budget works without billing signup; all consoles create permitted sessions offline without commercial account dependency.

#### OPS-007 — Licenses and private-until-demo release condition

**Statement.** Lamp host code/CLI/SDKs/first-party plugins must be Apache-2.0; relay AGPL-3.0. Repository visibility must stay private until first engine demo works. Dependencies/guest software must keep their own licenses.
**Source.** Brief §§20, 22; ADRs 0003, 0007–0008; architecture §§4, 21.
**Interfaces.** `lamp plugin get pg_01 --json`; `GET /v1/plugins/pg_01`; `plugin_get` points to pinned `digest,publisher,version`; dependency license notices accompany artifacts. Invalid provenance fails `INVALID_ARGUMENT`/`ENGINE_UNQUALIFIED` before enable.
**JSON observation.** `{"data":{"id":"pg_01","kind":"plugin","publisher":"<verified>","version":"<pinned>","digest":"<actual>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard About/plugin install shows notices and trust evidence. T3 Code/Herdr/SSH use same licensed host packages; guest Docker/native media is separately identified.

#### OPS-008 — Opt-in usage data only

**Statement.** Usage analytics must be off by default, send only numeric counts/timings after explicit consent and expire after 90 days. Opt-out must stop sends and purge unsent batches.
**Source.** Brief §20, O7; ADRs 0004, 0009; architecture §17.4.
**Interfaces.** `lamp telemetry update --enabled true --json`; `PATCH /v1/telemetry {"enabled":true}`; `telemetry_update` same body. `telemetry_get` reads `enabled,consent_revision,retention_days=90`; `telemetry_purge({})` removes retained batches. Stale consent is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"kind":"telemetry","enabled":false,"consent_revision":2,"retention_days":90},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard privacy→exact payload preview→opt in→opt out/purge→confirmed revision. T3 Code/Herdr/SSH show consent setting/link, never auto-enable from console use.

#### OPS-009 — Bounded admission and retained-state accounting [added]

**Statement.** Every active and retained resource must be quota-charged, with bounded control/bulk queues and safe cleanup. Exhaustion must not evict retry/audit safety or break unrelated admitted work.
**Source.** Brief §§5–7, 9–10, 12, 17, 19; ADRs 0001–0003; architecture §§3, 5, 12, 18.
**Interfaces.** `lamp fleet update fl_01 --limits-json '{"concurrency":4,"retained_bytes":10737418240,"control_records":100000}' --json`; `PATCH /v1/fleets/fl_01` with `limits`; `fleet_update` adds `fleet_id`. Over-limit returns `CONTROL_QUOTA_EXCEEDED`, `RATE_LIMITED` or `NO_CAPACITY`, with Retry-After where appropriate.
**JSON observation.** `{"error":{"code":"CONTROL_QUOTA_EXCEEDED","message":"Durable control capacity exhausted","retryable":true,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard distinguishes reserved/used pool/RAM backing/captures/volumes/artifacts/audit/control records and cleanup failures. T3 Code/Herdr/SSH show output-spool stop and resource reason without losing acknowledged output.

### Release measurements

#### BEN-001 — Linux create target

**Statement.** Measure the owner's standard Linux template-create target once at release: p50 under 500 ms from caller request to a real running command. Publish the actual result and setup, without a timing blocker [owner-confirmed B1, amended 2026-11-10].
**Source.** Brief §7; ADRs 0003, 0007; architecture §§6.2, 19; B1 default.
**Interfaces.** `lamp sandbox create --template-id tp_bench --resources-json '{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}' --json`; API `/v1/sandboxes` same fields; `sandbox_create`. Read `readiness.command_started_at`; failed create is reported honestly, never discarded.
**JSON observation.** `{"data":{"id":"sb_bench","kind":"sandbox","state":"ready","resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}},"request_id":"rq_01"}` [owner-confirmed B1]
**Visual/consoles.** Dashboard readiness/latency view shows phase and endpoint; T3 Code/Herdr/SSH witness actual first useful command, not boot badge.

#### BEN-002 — Linux live-fork target

**Statement.** Measure the owner's standard Linux live memory-fork target of under 1 s once at release. Include capture, source continuation, fresh identity and a usable child; report actual elapsed time without an every-fork or no-outlier rule. It never blocks work or release [owner-confirmed B1, amended 2026-11-10].
**Source.** Brief §§6–7; ADRs 0003–0004; architecture §§6.4, 19; B1 default.
**Interfaces.** `lamp sandbox fork sb_bench --fork-mode memory --durability local --json`; `POST /v1/sandboxes/sb_bench:fork {"fork_mode":"memory","durability":"local"}`; `sandbox_fork` adds `sandbox_id`. Snapshot `preserves_memory=true`; report any refusal or timeout honestly.
**JSON observation.** `{"data":{"id":"sn_01","kind":"snapshot","preserves_memory":true,"durability":"local","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard reports source pause, full elapsed fork and durability; all consoles observe independently continuing source/new Session. Native-Mac disk-only never scores here.

#### BEN-003 — Linux native-speed release measurement

**Statement.** Measure the owner's target of at least 97% of native speed separately for computation, disk and network once at release. Use the same host and normal security/storage behavior and publish results honestly. No workload matrix, sample quota or confidence threshold is required, and a slower result is not a blocker [owner-confirmed B1, amended 2026-11-10].
**Source.** Brief §7; ADR 0003; architecture §19; B1 default.
**Interfaces.** The optional `lamp host bench hs_01 --suite-digest '<workload-digest>' --json`, `POST /v1/hosts/hs_01:bench` and `host_bench` can run a named measurement workload. Record actual compute, disk and network results in the release report; they do not authorize or disqualify an engine.
**JSON observation.** `{"operation_id":"op_measure","resource_ids":[],"replayed":false,"request_id":"rq_01"}`
**Visual/consoles.** The release report shows actual native and sandbox results and limitations. Consoles show the workload's output without a certification badge.

#### BEN-004 — Protect secrets and report the zero-leak goal

**Statement.** Protect API keys, passwords and browser cookies from unauthorized access. The owner's zero-leak goal stays; report the release observation honestly without a prescribed attack suite or certification blocker. Subscription runtime custody and explicit owner Exposures remain disclosed exceptions; authorization and isolation are still required product behavior.
**Source.** Brief §7, O5, A1, A4; ADR 0004 amended; architecture §§9, 18, 22.
**Interfaces.** `lamp exposure list --json`; `GET /v1/exposures`; `exposure_list({})` lists explicit exceptions, not secret values. `audit_record_list` shows denied/allowed effects and redaction. Unauthorized exception request is `SCOPE_DENIED`.
**JSON observation.** `{"items":[],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard and consoles flag subscription custody and explicit Exposures without displaying secret values. Password uncertainty invokes private takeover [owner-confirmed B3]. Disclose the limited CI job-token exception [owner-confirmed B2].

#### BEN-005 — Publish release measurements

**Statement.** Publish the release measurements and available reproducible comparison data with the first public release. Measure once at release, not on each change or PR. Missing or slower measurements do not create a feature, merge or release prerequisite.
**Source.** Brief §§2, 21–22, O6; ADRs 0003, 0008; architecture §§19–24.
**Interfaces.** Publish the release report with its configuration and available raw measurements. The existing public `host_bench` operation is optional; reporting does not require a new runtime API or evidence format.
**JSON observation.** `{"data":{"id":"op_measure","kind":"operation","state":"succeeded"},"request_id":"rq_01"}`
**Visual/consoles.** Release documentation links the measurements, failures and reproduction instructions. A missing observation says unavailable rather than zero or passed.

### Engineering and owner-machine safety

#### ENG-001 — Engine first

**Statement.** Build the Linux create and continuing-source memory-fork engine first. The owner accepted T6 integration on 2026-11-10; later feature work proceeds without a speed or format-freeze prerequisite. The repository stays private until the first working engine demo.
**Source.** Brief §22; ADRs 0003, 0007–0008; architecture §21.
**Interfaces.** `lamp sandbox create --template-id tp_demo --json`, API `/v1/sandboxes` with `template_id`, MCP `sandbox_create`; then canonical `sandbox_fork`. Failed operations remain inspectable; historical failed measurements remain failed.
**JSON observation.** `{"operation_id":"op_demo","resource_ids":[{"kind":"sandbox","id":"sb_demo"}],"replayed":false,"request_id":"rq_01"}`
**Visual/consoles.** Early demo may use CLI/SSH; later dashboard/T3 Code/Herdr expose same actual engine behavior. A polished console cannot substitute for engine evidence.

#### ENG-002 — Reuse only for security or likely best speed

**Statement.** Reuse open source only for security necessity or likely best speed, as the owner decided. Maintain compatible dependencies and preserve their licenses; availability alone does not justify adoption.
**Source.** Brief §22; ADR 0003; architecture §§4–5.
**Interfaces.** `lamp engine get en_01 --json`; `GET /v1/engines/en_01`; `engine_get` identifies the installed engine digest and supported configuration. Plugin trust remains bound to the exact digest.
**JSON observation.** `{"data":{"id":"en_01","kind":"engine","digest":"<actual-engine-graph>"},"request_id":"rq_01"}`
**Visual/consoles.** Installation details show actual component versions and licenses. Shared microsandbox/msb_krun lineage is disclosed rather than presented as independent implementations.

#### ENG-003 — Architecture-owned language boundary

**Statement.** Go owns control, authorization and operations; Rust owns engines, supervisor, guest and confinement helpers through bounded typed RPC. Bulk data stays outside replicated metadata. The boundary preserves caller authority, quotas and operation identity without a latency target.
**Source.** Brief §22; ADR 0003; architecture §4.
**Interfaces.** `lamp operation get op_01 --json`; `GET /v1/operations/op_01`; `operation_get` exposes the same authoritative operation across workers. `metric_list` reports redacted phase timings. Worker failure returns failed Operation, never a second control-state decision.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"succeeded","cleanup_state":"not_needed"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard phase view and T3 Code/Herdr/SSH show one outcome despite component failure. No language claim appears as a speed result without measurements.

#### QA-001 — Safe use of owner machines

**Statement.** Development, demonstrations, release measurements and the development relay use only the owner's Ubuntu laptop, M5 Mac and MacBook Air, without new spending. Keep the owner's services and existing Docker resources intact. Do not drive visible apps or trigger Mac password/approval dialogs. Across tickets, allow at most 2 live Lamp sandboxes on Ubuntu, check below 2 Firecracker processes and at least 12 GiB available before creating one, and release it promptly.
**Source.** Brief §22, O2, O10; ADRs 0001, 0003; architecture §20.
**Interfaces.** `lamp host get hs_01 --json`; `GET /v1/hosts/hs_01`; `host_get` exposes OS/build/architecture/capacity/engines. Inspect actual free host resources before allocating disposable demonstration work.
**JSON observation.** `{"data":{"id":"hs_01","kind":"host","os":"linux","arch":"x86_64","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Show actual host capacity and availability. The development relay runs on the Ubuntu laptop without stopping the owner's services.

#### QA-002 — Disposable public demonstrations

**Statement.** Demonstrations use public third-party projects, harnesses, consoles, MCP servers and disposable accounts, never the owner's projects or special integrations for them. Keep real vault items, browser profiles and private data out of demonstrations; never print, log or commit secrets. Model tokens are the only permitted expense, with each pay-per-use key capped at $25/month in its provider dashboard.
**Source.** Brief §§22–23; ADRs 0001, 0003; architecture §20.
**Interfaces.** `lamp host list --json`; `GET /v1/hosts`; `host_list({})` supplies real inventory. `blob_create`, `blob_upload` and `blob_finalize` delivers fixture inputs; `template_build` exposes progress. Wrong fixture reference fails `INVALID_ARGUMENT`/`NOT_FOUND`.
**JSON observation.** `{"items":[{"id":"hs_mac","kind":"host","os":"macos","arch":"arm64","os_version":"26"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard fixture tasks and T3 Code/Herdr/SSH use public contracts and real IDs. No Ozi, Apple Bridge or money-bridge data/code is opened or tested.

## Resource operations and roles

The complete runtime operation set is architecture §§11–12. The following inventory describes resource lifecycles. Standard grammar: CLI `lamp <noun> <action> --json`; MCP `<stem>_<action>`; API create/list `POST/GET /v1/<collection>`, get/update/delete `GET/PATCH/DELETE /v1/<collection>/<id>`. Extra actions are colon-kebab POST unless listed GET. Scope is `<kind>.<action>` including underscores in extra action (`sandbox.file_write`). No alias names are accepted in examples or docs.

| Resource family | Lifecycle behavior |
| --- | --- |
| Fleet/Host | fleet get/list/update/transfer-owner/recover; host create/enroll/get/list/update/bench/drain/delete; re-enroll new identity, quota/fencing and stale host denied. Fleet deletion is not an invented product operation. |
| Principal/Membership/Invitation/IdentityProvider | create/get/list/update/delete where declared; invitation has no update, one-use accept/revoke/expiry; issuer/subject immutable; role downgrade revokes active authority. |
| Key/Grant/Share/Exposure | Key create/get/list/rotate/delete; Grant get/list/revoke/delete; Share create/get/list/update/revoke/delete; Exposure create/get/list/activate/revoke/delete, no update. Secret once, monotonic taint and pending revoke visible. |
| Adapter/Configuration/NativeExecutionGrant | Adapter get/list; Configuration create/get/list/delete (immutable document); NativeExecutionGrant create/get/list/delete with independent destination-owner approval and active revocation. Schemas and packages are discoverable from a new host. |
| Engine/Harness | Read-only get/list, actual compatibility and selected digest; no invented create/update engine control. Third-party engine installs as Plugin plus owner trust. |
| Template/Recipe/Kit | create/get/list/update/delete; Template build/validate and Sandbox save-template; immutable versions/name-pointer revision; Kit install plan and attachment. In-use ancestor cannot be collected. |
| Task/Budget/WarmPool | create/get/list/update/delete; task stop, pool claim/refill via sandbox create; all descendants charged and no new budget on fork/spawn. |
| Sandbox/Snapshot | sandbox create/get/list/update/delete/prepare-fork/fork/suspend/resume/save-template/restore; Snapshot create/get/list/delete, explicit compatibility/custody; restore only stopped/suspended destination after fence. |
| Exec/Session/Message/Lease | Exec create/get/list/delete/watch/input/resize/interrupt; Session create/get/list/update/delete/watch/attach/input/resize/interrupt/spawn/teleport/checkpoint/end; Message create/get/list; Lease create/get/list/delete/renew/release. No Message update/delete or Exec update. |
| ChangeSet/SettingsSource/SettingsChange | source create/get/list/update/delete; changes create/get/list/apply/reject/delete with selected path/base revision/approval, no update. Git/settings conflicts preserve existing content. |
| NetworkPolicy/NetworkAttachment/Port | create/get/list/update/delete; attachment test, port publish/share-link/unpublish; atomic narrowing/flow close, no public default. |
| CredentialSource/Connection/Attachment | create/get/list/update/delete; Connection test/invoke/revoke; Attachment delete detaches; no token value in resources. |
| Browser/Approval | Browser create/get/list/update/delete/action/fill-password/takeover/return-control; Approval create/get/list/delete/approve/deny/revoke, internal atomic consume. No reusable badge or Approval update. |
| Cache/Volume | create/get/list/update/delete; cache publish/evict, volume snapshot/recover; attachment create/delete, exact writer fence and live-reader refs. |
| Blob/Artifact/Backup | Blob create/get/list/delete/upload/download/finalize; Artifact create/get/list/update/delete/finalize/download; Backup create/get/list/delete/restore/export. No mutable content update of a ready immutable object. |
| Plugin/Job/Trigger/Schedule | Plugin create/get/list/update/delete/enable/disable/trust/test; Job create/get/list/delete/cancel/reconcile; Trigger create/get/list/update/delete/deliver; Schedule create/get/list/update/delete/run/pause/resume. No Job update. |
| NotificationSink/Notification | sink create/get/list/update/delete/test; Notification get/list/acknowledge/retry, no manual create/update/delete. |
| AccessRoute/AuthFlow/Update | route create/get/list/update/delete; AuthFlow create/get/list/delete/bootstrap/complete/exchange; Update create/get/list/delete/rollback. No invented update-in-place action for immutable flow/update content. |
| Telemetry/Operation/Event/AuditRecord/metrics/docs | singleton telemetry get/update/purge; Operation get/list/wait/cancel; Event list; AuditRecord get/list/export; metric_list/api_describe/docs_read reads. No create/delete for audit or event. |
| Files/devices/bulk transports | Sandbox file-read/write/transfer/list/delete and device-input/watch; Exec/Session streams use exact cursor/Frame/Lease; Blob upload/download use bounded chunks; MCP uses cursor tools, not invented subscriptions. |

Malformed input, missing or foreign IDs, stale revisions, expired authority and in-use ancestors return the documented errors. Deleting an already-deleted owned resource is idempotent and never detaches another owner’s resource. Read-only and immutable resources keep their declared operation limits.

| Role | Capacity/admission authority | Content/task authority | Credential decisions |
| --- | --- | --- | --- |
| Owner | Fleet policy/transfer/trusted engine/admission/admin config | Own plus explicit content grants; no automatic other-owner private content | Own credentials; others explicitly delegate |
| Admin | Assigned hosts/plugins/quotas, invite/remove within delegation; no owner transfer | Own plus explicit content grants; no implied private read | No automatic other-person vault/password approval |
| Member | Own tasks/recipes/keys/permitted capacity | Own permitted create/exec/files/watch/type; share owned shareable resources | Own permitted grants |
| Viewer | No create/admin default | Explicit watch/read only; no team-wide default listing | None |

Host root/admin is separately trusted for assigned host memory/disk. A restricted application role cannot make a hostile physical host safe for personal credentials. Owner-selected custody placement remains mandatory.

## Canonical parity index

There are no proposed public names. Architecture §§11–12 is canonical. The following index expands every standard operation for CLI/API/MCP use; extra-operation bindings are exact architecture inputs/results, not new requirement IDs. Bootstrap and local recovery retain their stated proof exceptions.

<details>
<summary>Engineering reference: 56 resources and 328 operations</summary>

| Kind / CLI noun / MCP stem | Collection | Standard operation IDs |
| --- | --- | --- |
| `fleet` / `fleet` / `fleet` | `/v1/fleets` | `fleet_get`, `fleet_list`, `fleet_update` |
| `host` / `host` / `host` | `/v1/hosts` | `host_get`, `host_list`, `host_create`, `host_update`, `host_delete` |
| `principal` / `principal` / `principal` | `/v1/principals` | `principal_get`, `principal_list`, `principal_create`, `principal_update`, `principal_delete` |
| `membership` / `membership` / `membership` | `/v1/memberships` | `membership_get`, `membership_list`, `membership_create`, `membership_update`, `membership_delete` |
| `invitation` / `invitation` / `invitation` | `/v1/invitations` | `invitation_get`, `invitation_list`, `invitation_create`, `invitation_delete` |
| `identity_provider` / `identity-provider` / `identity_provider` | `/v1/identity-providers` | `identity_provider_get`, `identity_provider_list`, `identity_provider_create`, `identity_provider_update`, `identity_provider_delete` |
| `key` / `key` / `key` | `/v1/keys` | `key_get`, `key_list`, `key_create`, `key_delete` |
| `grant` / `grant` / `grant` | `/v1/grants` | `grant_get`, `grant_list`, `grant_delete` |
| `engine` / `engine` / `engine` | `/v1/engines` | `engine_get`, `engine_list` |
| `harness` / `harness` / `harness` | `/v1/harnesses` | `harness_get`, `harness_list` |
| `kit` / `kit` / `kit` | `/v1/kits` | `kit_get`, `kit_list`, `kit_create`, `kit_update`, `kit_delete` |
| `template` / `template` / `template` | `/v1/templates` | `template_get`, `template_list`, `template_create`, `template_update`, `template_delete` |
| `recipe` / `recipe` / `recipe` | `/v1/recipes` | `recipe_get`, `recipe_list`, `recipe_create`, `recipe_update`, `recipe_delete` |
| `task` / `task` / `task` | `/v1/tasks` | `task_get`, `task_list`, `task_create`, `task_update`, `task_delete` |
| `budget` / `budget` / `budget` | `/v1/budgets` | `budget_get`, `budget_list`, `budget_create`, `budget_update`, `budget_delete` |
| `sandbox` / `sandbox` / `sandbox` | `/v1/sandboxes` | `sandbox_get`, `sandbox_list`, `sandbox_create`, `sandbox_update`, `sandbox_delete` |
| `snapshot` / `snapshot` / `snapshot` | `/v1/snapshots` | `snapshot_get`, `snapshot_list`, `snapshot_create`, `snapshot_delete` |
| `warm_pool` / `warm-pool` / `warm_pool` | `/v1/warm-pools` | `warm_pool_get`, `warm_pool_list`, `warm_pool_create`, `warm_pool_update`, `warm_pool_delete` |
| `exec` / `exec` / `exec` | `/v1/execs` | `exec_get`, `exec_list`, `exec_create`, `exec_delete` |
| `session` / `session` / `session` | `/v1/sessions` | `session_get`, `session_list`, `session_create`, `session_update`, `session_delete` |
| `message` / `message` / `message` | `/v1/messages` | `message_get`, `message_list`, `message_create` |
| `lease` / `lease` / `lease` | `/v1/leases` | `lease_get`, `lease_list`, `lease_create`, `lease_delete` |
| `share` / `share` / `share` | `/v1/shares` | `share_get`, `share_list`, `share_create`, `share_update`, `share_delete` |
| `change_set` / `change-set` / `change_set` | `/v1/change-sets` | `change_set_get`, `change_set_list`, `change_set_create`, `change_set_delete` |
| `network_policy` / `network-policy` / `network_policy` | `/v1/network-policies` | `network_policy_get`, `network_policy_list`, `network_policy_create`, `network_policy_update`, `network_policy_delete` |
| `network_attachment` / `network-attachment` / `network_attachment` | `/v1/network-attachments` | `network_attachment_get`, `network_attachment_list`, `network_attachment_create`, `network_attachment_update`, `network_attachment_delete` |
| `port` / `port` / `port` | `/v1/ports` | `port_get`, `port_list`, `port_create`, `port_update`, `port_delete` |
| `credential_source` / `credential-source` / `credential_source` | `/v1/credential-sources` | `credential_source_get`, `credential_source_list`, `credential_source_create`, `credential_source_update`, `credential_source_delete` |
| `connection` / `connection` / `connection` | `/v1/connections` | `connection_get`, `connection_list`, `connection_create`, `connection_update`, `connection_delete` |
| `attachment` / `attachment` / `attachment` | `/v1/attachments` | `attachment_get`, `attachment_list`, `attachment_create`, `attachment_update`, `attachment_delete` |
| `browser` / `browser` / `browser` | `/v1/browsers` | `browser_get`, `browser_list`, `browser_create`, `browser_update`, `browser_delete` |
| `approval` / `approval` / `approval` | `/v1/approvals` | `approval_get`, `approval_list`, `approval_create`, `approval_delete` |
| `exposure` / `exposure` / `exposure` | `/v1/exposures` | `exposure_get`, `exposure_list`, `exposure_create`, `exposure_delete` |
| `settings_source` / `settings-source` / `settings_source` | `/v1/settings-sources` | `settings_source_get`, `settings_source_list`, `settings_source_create`, `settings_source_update`, `settings_source_delete` |
| `settings_change` / `settings-change` / `settings_change` | `/v1/settings-changes` | `settings_change_get`, `settings_change_list`, `settings_change_create`, `settings_change_delete` |
| `cache` / `cache` / `cache` | `/v1/caches` | `cache_get`, `cache_list`, `cache_create`, `cache_update`, `cache_delete` |
| `volume` / `volume` / `volume` | `/v1/volumes` | `volume_get`, `volume_list`, `volume_create`, `volume_update`, `volume_delete` |
| `blob` / `blob` / `blob` | `/v1/blobs` | `blob_get`, `blob_list`, `blob_create`, `blob_delete` |
| `artifact` / `artifact` / `artifact` | `/v1/artifacts` | `artifact_get`, `artifact_list`, `artifact_create`, `artifact_update`, `artifact_delete` |
| `backup` / `backup` / `backup` | `/v1/backups` | `backup_get`, `backup_list`, `backup_create`, `backup_delete` |
| `plugin` / `plugin` / `plugin` | `/v1/plugins` | `plugin_get`, `plugin_list`, `plugin_create`, `plugin_update`, `plugin_delete` |
| `job` / `job` / `job` | `/v1/jobs` | `job_get`, `job_list`, `job_create`, `job_delete` |
| `trigger` / `trigger` / `trigger` | `/v1/triggers` | `trigger_get`, `trigger_list`, `trigger_create`, `trigger_update`, `trigger_delete` |
| `schedule` / `schedule` / `schedule` | `/v1/schedules` | `schedule_get`, `schedule_list`, `schedule_create`, `schedule_update`, `schedule_delete` |
| `notification_sink` / `notification-sink` / `notification_sink` | `/v1/notification-sinks` | `notification_sink_get`, `notification_sink_list`, `notification_sink_create`, `notification_sink_update`, `notification_sink_delete` |
| `notification` / `notification` / `notification` | `/v1/notifications` | `notification_get`, `notification_list` |
| `access_route` / `access-route` / `access_route` | `/v1/access-routes` | `access_route_get`, `access_route_list`, `access_route_create`, `access_route_update`, `access_route_delete` |
| `auth_flow` / `auth-flow` / `auth_flow` | `/v1/auth-flows` | `auth_flow_get`, `auth_flow_list`, `auth_flow_create`, `auth_flow_delete` |
| `update` / `update` / `update` | `/v1/updates` | `update_get`, `update_list`, `update_create`, `update_delete` |
| `telemetry` / `telemetry` / `telemetry` | `/v1/telemetry` singleton | `telemetry_get`, `telemetry_update` |
| `configuration` / `configuration` / `configuration` | `/v1/configurations` | `configuration_get`, `configuration_list`, `configuration_create`, `configuration_delete` |
| `adapter` / `adapter` / `adapter` | `/v1/adapters` | `adapter_get`, `adapter_list` |
| `native_execution_grant` / `native-execution-grant` / `native_execution_grant` | `/v1/native-execution-grants` | `native_execution_grant_get`, `native_execution_grant_list`, `native_execution_grant_create`, `native_execution_grant_delete` |
| `operation` / `operation` / `operation` | `/v1/operations` | `operation_get`, `operation_list` |
| `event` / `event` / `event` | `/v1/events` | `event_list` |
| `audit_record` / `audit-record` / `audit_record` | `/v1/audit-records` | `audit_record_get`, `audit_record_list` |

Standard REST mapping is list GET collection; get GET collection/{id}; create POST collection; update PATCH item; delete DELETE item. Singleton telemetry uses GET/PATCH collection. CLI action flags follow the common contract.

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
| `browser_action`, `browser_fill_password`, `browser_takeover`, `browser_return_control` | `:action {action:navigate\|click\|type\|read\|screenshot\|download,arguments,lease_id}`; `:fill-password {credential_source_id,secret_ref,action_digest,approval_id}`; `:takeover {lease_id}`; `:return-control {lease_id}` → Operation with filtered result/blob reference |
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


All suffixes above are under `/v1`; collection names come from the resource table. Errors and typed fields keep their exact architecture meanings.

</details>

## Requirement inventory

Product behavior IDs remain available for ticket references. QA-003 and QA-004 are retired proof-only records. BEN-001–005 now describe release measurements and secret protection, not gates. Historical rounds preserve prior wording and attribution; they do not add work to the current feature requirements.
