# Lamp requirements

Round 6 · 2026-11-09 · Requirements baseline aligned with the final architecture. All owner decisions are accepted; implementation evidence is still required.

Lamp is open-source software that people install on their own machines. It creates isolated computers, called sandboxes, for AI agents, harnesses (the programs that run agents) and continuous integration (CI). One fleet can contain one host or many. Callers use one address and one set of keys. They can use an application programming interface (API), command-line interface (CLI) or Model Context Protocol (MCP) server, or watch and join sessions through a dashboard, T3 Code, Herdr or Secure Shell (SSH).

Lamp serves four equal needs: parallel work without collisions, protection of the host and secrets, ready-to-use tools and sign-in, and ownership of the machines and data. The first release includes the entire owner brief under its accepted release timing: TypeScript/Python software development kits (SDKs) ship then, and Go/Rust follow immediately. Hosts are Linux x86_64 and arm64, and Apple Silicon Macs with macOS 26 or newer. Linux guests run on both host families. Native macOS guests use a clearly labeled slower full virtual machine (VM) class. Lamp uses its own template format. Docker is available inside a sandbox as a kit, never as Lamp's runtime or template format.

The owner can run everything locally. Eilmer's optional relay only forwards end-to-end encrypted traffic. It never holds model credentials or sandbox data. All Eilmer-hosted addresses live under eilmer.si. API keys stay in host brokers. Subscription logins stay on the person's trusted hosts and own sandboxes. Their presence in a same-owner memory fork or suspend is an explicit exception to the zero-secret-leak target. Templates and anything shared or exported start the harness fresh.

This document is a release acceptance contract for the owner, implementers and testers. It describes visible behavior, failure behavior and evidence required before calling a feature complete. It does not claim the implementation or tests already exist. The binding sources are [the brief](brief.md), [the glossary](../../GLOSSARY.md), and [Architectural Decision Records (ADRs) 0001–0009](../adr/). The merged [architecture](architecture.md), especially §§11–12, supplies canonical operations and fields. Accepted O1–O10, A1–A5 and B1–B4 are part of the brief. Requirements that depend on B1–B4 are marked `[owner-confirmed Bx]`.

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

All project tests and the development relay run on the owner's Ubuntu laptop, M5 Mac and MacBook Air. The secondary Linux host tests run inside virtual machines and are labeled nested. Native arm64 speed is certified only when someone runs the open benchmark on native hardware.

## Abbreviations

| Term | Meaning |
| --- | --- |
| API / CLI / MCP | Application programming interface / command-line interface / Model Context Protocol: three ways to control the same Lamp features. |
| CI / SDK / LLM / TS | Continuous integration / software development kit / large language model / TypeScript. |
| VM / CPU / vCPU / RAM | Virtual machine / central processing unit / virtual CPU / random-access memory (working memory). A nested lane runs the Lamp host inside another VM. |
| MiB / GiB / KiB | Binary memory/storage units: 2^20 / 2^30 / 2^10 bytes. |
| JSON / NDJSON / SSE | JavaScript Object Notation / newline-delimited JSON / server-sent events. |
| SSH / TLS / HTTP / HTTPS | Secure Shell / Transport Layer Security / Hypertext Transfer Protocol / HTTP protected by TLS. |
| TTL / RPO / RSS | Time to live / recovery point objective (how much saved work can be lost) / resident memory use. |
| PR / OIDC / JWT / PKCE | Pull request / OpenID Connect / JSON Web Token / Proof Key for Code Exchange. |
| p50 / p95 / p99 | Median / 95th / 99th percentile: the value at or below which that share of observations falls. |
| CoW / PRNG / fsync | Copy-on-write / pseudorandom number generator / a filesystem synchronization call; durable qualification needs the stronger per-OS barriers specified below. |
| ACL / RPC / IPC / ABI | Access-control list / remote procedure call / interprocess communication / application binary interface. |
| KVM / HVF / VZ | Linux Kernel-based Virtual Machine / Apple's Hypervisor and Virtualization frameworks. |
| HID / PCM / UID | Human Interface Device code / pulse-code modulation audio / Unix user ID. |
| CA / CAA / ACME / CT / TUF | Certificate authority / Certificate Authority Authorization / Automated Certificate Management Environment / Certificate Transparency / The Update Framework. |

Further security terms are explained where they first affect a requirement; exact wire shapes follow architecture §§11–12.

## Table of contents

1. [Reading and test rules](#reading-and-test-rules)
2. [Requirements by area](#requirements-by-area)
3. [Journey acceptance tests](#journey-acceptance-tests)
4. [Security attack suite](#security-attack-suite)
5. [Benchmark suite](#benchmark-suite)
6. [Test-lane matrix](#test-lane-matrix)
7. [Traceability and ID changes](#traceability-and-id-changes)
8. [Remaining questions](#remaining-questions)
9. [Canonical parity index](#canonical-parity-index)
10. [Requirement inventory by area](#requirement-inventory-by-area)

## Reading and test rules

Every numbered record below is a requirement. “Must” is binding. A subtarget labeled [proposed] uses “should” until engineering adopts it. Numbers here are acceptance thresholds, not measured results. A safe refusal passes the denial test only; it cannot pass the required successful workflow. No implementation is certified by this document.

The 98 draft IDs remain. Seven new IDs isolate missing contracts: INT-008, STO-005, ACC-008, ACC-009, OPS-009, QA-003 and QA-004. QA-001 uses O2 as replaced by the no-spending decision in brief §22. No old ID is deleted or merged. CRED-004 has a corrected title and scope under amended ADR 0004. The final inventory lists all 105 records.

The owner accepted these four decisions on 2026-11-08:

| Marker | Default used in this document |
| --- | --- |
| [owner-confirmed B1] | Hard Linux speed targets apply to a standard 2 vCPU, 4 GiB RAM, 20 GiB disk sandbox, with up to 1 GiB recently written memory at fork. Larger sizes are measured and published without hard targets. |
| [owner-confirmed B2] | A public-PR job may hold only its CI system's own short-lived token for that job, such as a GitHub runner job token with a read-only repository token. No other secrets. |
| [owner-confirmed B3] | Standard login forms use protected fill. If password secrecy cannot be guaranteed, the person types during private takeover. The agent never receives the password. |
| [owner-confirmed B4] | Every command means every process started: program, arguments, user, parent process and time. It excludes the meaning of commands inside scripts. |

B1–B4 are accepted in the brief and canonical architecture, including its schemas, gates and risk register. No product-policy vote or spending-cap input remains pending. Brief §22 replaces O2's earlier infrastructure plan and sets macOS 26 as the certified minimum under O10. Only implementation, vendor and owned-hardware evidence remains outstanding.

### Common interface contract

All examples use architecture §§11–12. IDs such as `sb_01` are schematic; tests capture actual returned IDs. Object fragments show selected canonical fields, not complete resources. An API body shown beside a CLI command is also the MCP create input unless stated otherwise. For get/update/delete/extra MCP calls, add the addressed `<stem>_id`; HTTP uses its path and CLI its positional ID. Bodies never contain server-derived fields. Set fixture `t0` from the authoritative test clock and derive each expiry as `t0+ttl_seconds`. Values such as `<t0+60s>` are substitutions for an actual RFC3339 Coordinated Universal Time (UTC) timestamp, not literal inputs. Test just before, at and after expiry. Historical timestamps illustrate record structure only.

Give each new action a retry key. If the connection drops, retry with the same key and unchanged inputs. Before editing an existing object, send the version you read; Lamp rejects an outdated edit. The CLI and SDK handle key discovery for you.

In technical examples, every resource mutation includes this common context even when not repeated: a fresh `idempotency_key` (HTTP `Idempotency-Key`, CLI `--idempotency-key`) and, for update or dangerous extra action, `expected_revision` (HTTP `If-Match`, CLI `--expected-revision`). A retry uses the original key and identical body. The fixture obtains structured keys from authenticated discovery; it never guesses another operation's home. Caller-local `--input` files contain JSON; binary/content files are uploaded as Blobs. No MCP argument asks the host to read a caller-local path.

CLI JSON, API and MCP share envelopes:

```json
{"operation_id":"op_01","resource_ids":[{"kind":"sandbox","id":"sb_01"}],"request_id":"rq_01","replayed":false}
```

Wait through `lamp operation wait op_01 --timeout-ms 25000 --json`, `GET /v1/operations/op_01/wait?timeout_ms=25000`, or `operation_wait({"operation_id":"op_01","timeout_ms":25000})`. A timeout returns current state, not success. On completion inspect `operation.outcome.result`, such as `{"sandbox_id":"sb_01","session_ids":["se_01"]}`. Follow-up reads return `{"data":{...},"request_id":"rq_02"}`; lists return `{"items":[...],"next_cursor":"...","request_id":"rq_03"}`. Key, invitation, auth-exchange and share-link secret results are never persisted in operations/events/audit or request hashes; replay preserves the same Operation/resource/grant identity and reports `secret_available=false`. See the exact read/download exceptions below.

Errors use `{"error":{"code":"SCOPE_DENIED","message":"Action is outside this grant","retryable":false,"details":{}},"request_id":"rq_04"}`. Include `operation_id` when accepted work failed. Canonical codes and meanings are architecture §12.2. Unauthorized foreign IDs return `NOT_FOUND`. Schema failures use `INVALID_ARGUMENT`. CLI exits are 0 success, 2 input, 3 auth, 4 conflict, 5 unavailable, 6 failed/canceled operation. Exec's process exit is a separate `exit_code`.

Where a record says “operation result,” use the envelope above with that record's returned resource references. Where it says “read data,” wrap the shown JSON in `data`. This avoids inventing a separate output shape per requirement. Every CLI example requests `--json`; text UX can also show progress.

The complete error acceptance set below is binding across API, CLI and MCP. Test every code at its boundary, including expired retained results rather than treating them as fresh work.

| HTTP / code | Meaning |
| --- | --- |
| 400 `INVALID_ARGUMENT`; 401 `UNAUTHENTICATED`; 404 `NOT_FOUND` | Schema/identity/authorized visibility failure |
| 403 `SCOPE_DENIED`, `VM_REQUIRED`, `HOST_NOT_TRUSTED`, `APPROVAL_REQUIRED` | No effect; explain the required authority/boundary safely |
| 409 `REVISION_CONFLICT`, `IDEMPOTENCY_CONFLICT`, `RESOURCE_IN_USE`, `OUTCOME_UNKNOWN`, `TASK_AUTHORITY_EXPIRED` | Inspect current revision/original operation; never blindly rerun |
| 410 `RESULT_EXPIRED`, `KEY_EPOCH_EXPIRED`, `CURSOR_EXPIRED` | Retry safety/replay record cannot be reused as fresh work |
| 422 `ENGINE_UNQUALIFIED`, `DEVICE_UNAVAILABLE`, `DEVICE_NOT_FORKABLE`, `AUTH_TRANSPORT_UNSUPPORTED`, `CONTINUATION_UNSUPPORTED`, `CLEAN_REBUILD_REQUIRED`, `TOKEN_CAP_UNENFORCEABLE` | Required supported workflow absent; remains visible release coverage failure |
| 429 `NO_CAPACITY`, `CONTROL_QUOTA_EXCEEDED`, `RATE_LIMITED` | Retry-After; no silent unbounded queue |
| 503 `HOST_UNAVAILABLE`, `DATA_UNAVAILABLE`, `QUORUM_REQUIRED`, `IDEMPOTENCY_HOME_UNAVAILABLE`, `TOKEN_HOME_UNAVAILABLE`, `AUDIT_UNAVAILABLE` | Explicit unavailable authority/data, not a fresh replacement |

### Common visual and console contract

“Consoles” in each record means all three: T3 Code, Herdr and stock SSH. T3 Code and Herdr configure one fleet machine, list each authorized Session, open/watch it, and type only with an input Lease. SSH lists with `ssh -p 22418 person@ssh.f123.fleet.eilmer.si sessions`, attaches with `ssh -t -p 22418 person@ssh.f123.fleet.eilmer.si attach se_01`, and opens a sandbox shell with `ssh -t -p 22418 person@ssh.f123.fleet.eilmer.si shell sb_01`. Actual AccessRoute supplies host/port; direct and Tailscale use port 22. These are ordinary OpenSSH commands with a pinned fleet SSH CA. They never open a host shell.

Each dashboard mutation has input validation, effective-policy preview, an Operation progress view, success detail and a retained failure with cancel/retry controls. Stale edits reload on `REVISION_CONFLICT`. Empty lists explain whether there are no resources or insufficient scope without exposing other owners. Stale observations show time and `observation_state`; silence is not completion. Console-only resources appear in session detail or a trusted dashboard link; SSH need not render graphical forms. All three must preserve the same IDs, status, scope and errors.

### Protected browser support boundary

A standard login form means a browser-rendered HTML username/password form on HTTPS, with an approved exact origin/frame/form action and trusted credential recipient. The adapter validates that target immediately before use and controls every offered agent observation afterward. A standard form is not safe merely because its input has `type=password`; changed actions, hostile same-origin reflection, reveal/download paths or unproved site scripts invalidate automatic fill [owner-confirmed B3].

For standard forms whose adapter proves secrecy, fill and submit must work. For canvas/custom widgets, unapproved frames, changed targets or any unproved secrecy, Lamp must offer successful private human takeover instead; the person types, all sensitive observations stay private, and agent control returns only to a proven safe page. If no such page exists, that workflow stays human-controlled. The agent never receives the password [owner-confirmed B3]. Useful protected browser controls are navigate/click/type/read/screenshot/download under explicit site/action adapters, never unrestricted CDP or JavaScript. Release tests include real permitted sites as well as hostile controlled fixtures; a friendly fixture alone cannot certify general support.

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

Input's completed Operation result acknowledges `{device_ref,sequence,delivered_at}` only after backend delivery. Replaying a sequence never redelivers; changed input at the same sequence conflicts. Check the actual letter/pixel, reconnect and revoke; inventory alone is insufficient. Watch returns `{stream,items,next_cursor,request_id}`. StreamDescriptor adds applicable `media_type,width_px,height_px,sample_rate_hz,channels`; MediaFrame is `{stream_id,sequence,device_ref,media_type,pts_us,duration_us,keyframe,payload}`, substituting `data_base64` for binary payload on MCP. Ordinary output Frame and media share the common decoded/encoded limits.

Canonical input variants: key `{type:key,code,action:down|up,modifiers}` uses uint16 USB HID usage codes with modifiers `shift,control,alt,meta`; mouse coordinates are integer pixels from top-left, buttons `left,middle,right` and signed wheel detents. Audio `{type:audio,format:pcm_s16le,sample_rate_hz,channels,data_base64}` is interleaved signed 16-bit PCM: deliver 480 mono samples at 48000 Hz and compare actual loopback bytes. Unknown tags or out-of-range coordinates fail before delivery. Disk uses file/block attachments; graphics processing unit (GPU) uses qualified engine/kit APIs, never generic host ioctls.

### Complete approval exchange

The request JSON file below contains the canonical Approval fields shown in ACC-008, including exact principal/task/sandbox generation/resource/action plan/destination/expiry. The enforcing adapter recomputes the plan and digest; authenticated inspection displays that immutable plan, not task-supplied display text. Tests set the relevant vault/Connection approval policy to `mode=each_use,ttl_seconds=60` and test expiration at 60 s. There is no public consume operation.

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

### Common acceptance and test rules

A record's end-to-end (E2E) tests reference the fixed cases below. Expand only the exact lane/mode pairs of those cases and the relevant behavior in the record, never an axis-wide Cartesian product. Secondary Linux hosts, including LA, are nested; every evidence record must carry that label. M5/AIR are native Mac hosts; their native macOS guests are separate full VMs. Run every assigned required positive, not a hand-picked host; denial alone cannot pass it. Each test captures returned IDs, waits for Operations, checks real effects with an independent oracle, then deletes its own resources and verifies reservations/authority are gone. Tests use dedicated accounts and public third-party fixtures. No tests touch the owner's projects.

A fake can replace a vendor, clock, Domain Name System (DNS) answer or fault injector at a stated boundary. Never mock the hypervisor, guard, broker authorization, real persisted bytes, cryptography, actual process/device effects, or console transport when those are the behavior under test. Code tests combine fast models with real component integration. E2E adds actual hardware and official binaries. Both positive use and adversarial denial are mandatory.

Common local area network (LAN) objectives [proposed] are resource/status visibility p95 ≤1 s, notification enqueue ≤1 s and first attempt ≤5 s. Local restart ≤5 s and survivor recovery ≤10 s are [proposed]. Online revocation and hard task stopping must complete within 5 s. These do not imply 5 s revocation for partitioned hosts: declared permits last at most 24 h by default; credential/stream leases last at most 30 s. An offline grant cannot renew without current authority.

## Requirements by area

### Product and scope

#### PRD-001 — Public-purpose product and four equal goals

**Statement.** Lamp must serve people, agents, harnesses and CI from release one. Parallelism, safety, setup and ownership must each have successful release evidence.
**Source.** Brief §§1–2, 21; ADRs 0003, 0008; architecture §§1, 21, 24.
**Interfaces.** CLI `lamp fleet get fl_01 --json`; API `GET /v1/fleets/fl_01`; MCP `fleet_get({"fleet_id":"fl_01"})`. Read data includes `{"kind":"fleet","mode":"single_owner"}`. `NOT_FOUND` hides an unauthorized fleet.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","mode":"single_owner"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard opens to fleet hosts, sandboxes, sessions and capacity. T3 Code, Herdr and SSH show two separate concurrent sessions and their independent output.
**Pass/fail.** All 4 goals have at least 1 successful journey and 1 adverse test; 0 goals may be replaced by a benchmark headline. Run 2 concurrent tasks with identical private file paths and 0 collisions.
**E2E.** Fixed cases `host-linux, access-console`: install on a clean test account; run Journeys 1 and 3 with two public fixtures; verify private files, ready tools and local ownership.
**Code tests.** Fake caller identities and capacity; verify isolation/ownership admission. Never mock actual parallel execution or host-canary checks.

#### PRD-002 — Self-hosted installation and control

**Statement.** Lamp must install as packaged software on the person's hosts, with no required hosted database, Kubernetes, Redis, Postgres or Docker runtime. Unused feature workers must stay stopped.
**Source.** Brief §§1, 17, 22; ADRs 0005, 0007; architecture §§3–4, 20.
**Interfaces.** `lamp host get hs_01 --json`; `GET /v1/hosts/hs_01`; `host_get({"host_id":"hs_01"})`; read data `{"state":"ready","roles":["control","compute"]}`. Preflight failure explains missing virtualization; execution returns `ENGINE_UNQUALIFIED` rather than hiding it.
**JSON observation.** `{"data":{"id":"hs_01","kind":"host","state":"ready","roles":["control","compute"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard setup runs preflight → install/enroll → local ready. It explains privileged operating system (OS) setup separately. Consoles reach sessions after normal enrollment. Uninstall offers retain/export/delete owner data.
**Pass/fail.** Zero required external services for a local fleet. Clean unused install should use ≤160 MiB RSS, ≤0.6% of one host CPU and ≤1 GiB installed assets; restart to API ready should take ≤5 s [proposed]. No runtime elevation. Uninstall leaves 0 active workers/listeners/startup services and does not delete retained volumes.
**E2E.** Fixed cases `host-linux, access-console`: packaged install, local task, restart, explicit retain-data uninstall and reinstall. Record every prerequisite and interaction. This writing task runs no installers or password prompts.
**Code tests.** Fake package download/preflight failures; test retain/delete choice and restart state. Never mock real service registration/removal, resource use or offline execution.

#### PRD-003 — Complete first-release scope; no cloud overflow

**Statement.** Release one must include every brief feature and required compatibility row under the accepted O1 SDK cutoff. Lamp must not overflow work into cloud providers automatically.
**Source.** Brief §§18–22; ADRs 0001, 0008; architecture §§20–22.
**Interfaces.** `lamp sandbox create --recipe-id rc_01 --json`; `POST /v1/sandboxes {"recipe_id":"rc_01"}`; `sandbox_create` with that body. A full fleet returns `NO_CAPACITY`, with no cloud allocation.
**JSON observation.** `{"error":{"code":"NO_CAPACITY","message":"No admitted capacity remains","retryable":true,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows capacity and failed admission with choices to free resources or add an owner-selected host. Consoles retain existing sessions and show the failed new operation.
**Pass/fail.** 105 requirement records accounted for; 0 required rows silently excluded and 0 automatic external compute purchases. Project tests and the dev relay stay on the three owned machines under replaced O2.
**E2E.** Fixed cases `host-linux, access-console`: fill admitted capacity, request one more task, inspect admission and host inventory for 0 unrequested resources or external provisioning attempts; verify all release rows are tested or visibly blocked.
**Code tests.** Fake scheduler capacity and cloud endpoints; ensure no overflow path exists. Never mock actual admission accounting or the release coverage manifest.

### Callers and interfaces

#### INT-001 — Credentialed callers and interface parity

**Statement.** A credentialed person, agent, CI job or outside service must get the same permitted product operations through CLI, API and MCP.
**Source.** Brief §§3–4, 17; ADRs 0001, 0005; architecture §§11–12.
**Interfaces.** `lamp exec create --sandbox-id sb_01 --argv-json '["printf","42\\n"]' --json`; `POST /v1/execs {"sandbox_id":"sb_01","argv":["printf","42\n"]}`; `exec_create` with that body. Operation result is `{"exec_id":"ex_01"}`. `SCOPE_DENIED` gives 0 effects.
**JSON observation.** `{"operation_id":"op_01","resource_ids":[{"kind":"exec","id":"ex_01"}],"replayed":false,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard command action and T3 Code/Herdr/SSH shell views show the same actual output. Transport choice never adds shell/files/approval rights.
**Pass/fail.** Generated parity tests cover 100% of architecture §§11–12 operation/scope pairs. Each has success, denial and malformed-input cases on all 3 interfaces; 0 differences in effective authority.
**E2E.** Fixed cases `host-linux, access-console, secret-result-transport`: repeat create→exec→watch→delete through each interface with person, agent, CI and service principals; compare effects and errors.
**Code tests.** Fake caller claims, not authorizer. Generate adapter contracts from the schema; retain real authorization, serialization and byte framing.

#### INT-002 — Generated SDKs and delivery order

**Statement.** TypeScript and Python SDKs must ship in release one. Go and Rust SDKs must follow immediately afterward. All must derive from one API definition.
**Source.** Brief §3, O1; ADR 0008; architecture §§4, 12, 21.
**Interfaces.** `lamp api describe --json`; `GET /v1/openapi.json`; `api_describe({})` return the canonical OpenAPI definition. SDKs perform `sandbox_create` semantics, wait for the Operation and read the returned sandbox. Unknown fields produce `INVALID_ARGUMENT`.
**JSON observation.** OpenAPI excerpt: `{"openapi":"3.1.0","info":{"title":"Lamp","version":"v1"}}`; the complete document must contain every canonical path.
**Visual/consoles.** Dashboard API help links generated SDK examples. Sessions created by each SDK appear normally in T3 Code, Herdr and SSH.
**Pass/fail.** 2 SDKs included in first-release artifacts; 2 follow-on SDKs have the same schema generation and conformance release gate. Zero hand-maintained renamed fields or untested streaming adapters. “Immediately after” fixes sequence, not an invented calendar date.
**E2E.** Fixed cases `host-linux, access-console, secret-result-transport`: install published TS/Python packages in clean fixture callers; create, upload a file, receive output and delete. Repeat Go/Rust before those packages release.
**Code tests.** Fake HTTP errors/time, use real generated types, schema validation and pagination/cancellation adapters. Never mock the SDK-to-running-Lamp path in certification.

#### INT-003 — Agent-readable CLI and documentation

**Statement.** Every CLI command must support machine-readable JSON. Product documentation must also be available as LLM-readable text with canonical fields, errors and examples.
**Source.** Brief §3; ADR 0008; architecture §§4, 12.
**Interfaces.** `lamp docs read --topic sandbox --json`; `GET /v1/docs?topic=sandbox`; `docs_read({"topic":"sandbox"})`. `lamp --help --json` and `lamp version --json` are local diagnostics, not extra product APIs. JSON errors use `INVALID_ARGUMENT`.
**JSON observation.** `{"error":{"code":"INVALID_ARGUMENT","message":"Unknown field","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard docs offer human and text views. T3 Code/Herdr can open linked docs; SSH users run the same CLI and parse stdout without stripping progress text.
**Pass/fail.** 100% of commands emit valid JSON envelopes or documented NDJSON under `--follow`; 0 prose bytes on JSON stdout. Exercise exit codes 0, 2, 3, 4, 5, 6. Every product operation is documented. First-release CLI builds target Linux x86_64/arm64 and macOS arm64 only.
**E2E.** Fixed cases `host-linux, access-console, secret-result-transport`: run the CLI builds on LX/LA and native M5/AIR callers; invoke every generated help/operation example against a fixture fleet; parse stdout and inspect stream cancellation and errors.
**Code tests.** Fake transport faults; snapshot semantic schemas and parse output. Never replace the real CLI parser or serializer with canned JSON.

#### INT-004 — Idempotent retries

**Statement.** Retrying a mutation must never create fresh duplicate work. Unknown command or external side effects must remain explicitly unknown.
**Source.** Brief §3; ADR 0001; architecture §§12.2–12.3, 14.
**Interfaces.** `lamp sandbox create --recipe-id rc_01 --idempotency-key lk1.1.hs_01.nonce --json`; same API body plus `Idempotency-Key`; `sandbox_create({"recipe_id":"rc_01","idempotency_key":"lk1.1.hs_01.nonce"})`. Repeat returns the original `operation_id` with `replayed=true`. Different input returns `IDEMPOTENCY_CONFLICT`.
**JSON observation.** `{"operation_id":"op_01","resource_ids":[{"kind":"sandbox","id":"sb_01"}],"replayed":true,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard reconnect opens the original Operation and resource. Consoles do not add a second session after a dropped response. Unknown delivery shows `outcome_unknown`, never a retry-as-new button.
**Pass/fail.** 20 concurrent same-key requests allocate exactly 1 resource. Results remain 30 days; tombstones last until fleet deletion. Day-31/deleted-resource replay cannot execute again. Missing home yields `IDEMPOTENCY_HOME_UNAVAILABLE`; expired authority yields `KEY_EPOCH_EXPIRED`.
**E2E.** Fixed cases `storage-fleet, host-linux`: drop responses before/after commit, kill home, delete resource, restore stale backup, advance retention; use an independent side-effect ledger for exec/input.
**Code tests.** Fake network/time; model epoch+principal+operation+key+canonical hash independently. Crash real journals around delivery/receipt; never mock persistence or claim exactly-once arbitrary remote effects.

#### INT-005 — Live event stream

**Statement.** Callers must observe authorized events with replay, ordering, gap detection and bounded backpressure.
**Source.** Brief §3; ADR 0001; architecture §12.4.
**Interfaces.** `lamp event list --follow --json`; API `GET /v1/events?cursor=c1` with `Accept: text/event-stream`; MCP `event_list({"cursor":"c1","limit":100,"wait_ms":25000})` → `{"items":[],"next_cursor":"c2"}`. Names include `sandbox.state_changed`, `operation.completed`, `session.status_changed`. Expired replay returns `CURSOR_EXPIRED` and resync guidance.
**JSON observation.** `{"items":[{"event_id":"<host-epoch-sequence>","type":"session.status_changed","resource_kind":"session","resource_id":"se_01","resource_revision":2,"data":{"status":"blocked"}}],"next_cursor":"c2","request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr reconcile resource revisions; SSH reconnect resumes output cursor. Stale location events cannot move a session backward.
**Pass/fail.** At-least-once unique event identities, ordered per resource revision; 7-day metadata replay. MCP wait ≤25 s. Decoded stream payloads ≤64 KiB; encoded JSON frames ≤96 KiB; encoded queues ≤1 MiB. REST binary framing adds ≤4 KiB. Slow observers disconnect with last cursor and cannot stall work. LAN visibility should be p95 ≤1 s [proposed].
**E2E.** Fixed cases `access-console, secret-result-transport`: duplicate/reorder events, disconnect, expire cursor, revoke reader and flood output including binary and split multibyte text; check 0 cross-owner frames.
**Code tests.** Fake event delivery/time; retain real cursor/revision filtering, framing and bounded spool. Never mock actual reconnect or active-stream revoke in E2E.

#### INT-006 — Agent inside

**Statement.** A caller must be able to run a harness inside a sandbox and let it work independently while its session remains observable and budgeted.
**Source.** Brief §§3–4, 13, 16; ADR 0004; architecture §§9–10, 17.
**Interfaces.** `lamp session create --sandbox-id sb_01 --harness-id hr_01 --agent-mode inside --json`; `POST /v1/sessions {"sandbox_id":"sb_01","harness_id":"hr_01","agent_mode":"inside"}`; `session_create` with that body. Result `{"session_id":"se_01","sandbox_id":"sb_01"}`; unsupported auth returns `AUTH_TRANSPORT_UNSUPPORTED`.
**JSON observation.** `{"data":{"id":"se_01","kind":"session","agent_mode":"inside","state":"active","status":"working"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows sandbox and Session independently. T3 Code/Herdr/SSH disconnect and reattach without restarting the harness.
**Pass/fail.** 1 useful official-harness tool task completes while caller is disconnected; 1 blocked question can be answered after reconnect; 0 host process execution from task commands. Budget remains unchanged.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: start official harness, disconnect caller, observe tool result from another authorized console and resume input.
**Code tests.** Fake harness protocol for deterministic working/blocked/idle transitions. Never use that fake as evidence of official sign-in, inference or guest process location.

#### INT-007 — Agent outside

**Statement.** An outside agent must control commands, files, browser and requested devices through the same scoped remote-computer operations.
**Source.** Brief §§3–4, 6, 15; ADRs 0002, 0004; architecture §§10–12.
**Interfaces.** `lamp sandbox file-read sb_01 --path /work/a.txt --offset-bytes 0 --length-bytes 4096 --json`; `POST /v1/sandboxes/sb_01:file-read {"path":"/work/a.txt","offset_bytes":0,"length_bytes":4096}`; `sandbox_file_read` adds `sandbox_id`. Result `{"data_base64":"NDIK","sha256":"<actual-hash>","eof":true}`. Escaping paths fail `SCOPE_DENIED`.
**JSON observation.** `{"data_base64":"NDIK","sha256":"<actual-sha256>","eof":true,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard file/browser/device panes and T3 Code/Herdr show outside Session and actual actions. SSH sees the same files and process outputs; graphical takeover is a trusted link.
**Pass/fail.** Upload→write→read→download preserves every byte of a 1 MiB fixture; chunks ≤1 MiB. Exec, file-list/delete and requested device input/watch succeed; 0 ungranted operations. Unknown outside model usage is never labeled exact.
**E2E.** Fixed cases `host-linux, device-basic, device-physical, broker-browser-vault, secret-result-transport`: public outside-agent fixture edits file, runs tests, navigates approved browser and watches requested display; compare hashes and device observations.
**Code tests.** Fake outside model decisions, not file path confinement, transfer hashes or action authorization. Exercise offset overflow, traversal, binary data and revoked handles.

#### INT-008 — Readiness, concurrent edits and cancellation [added]

**Statement.** Accepted work must expose its Operation, partial resources, setup checks and cleanup. Ready must mean all requested setup passed and a real command ran. Dangerous stale edits must fail atomically.
**Source.** Brief §§3, 8, 13, 19; ADRs 0001–0002, 0004; architecture §§6.2, 11–12.
**Interfaces.** `lamp operation get op_01 --json`; `GET /v1/operations/op_01`; `operation_get({"operation_id":"op_01"})`. Read data includes `{"state":"failed","phase":"kits","cleanup_state":"complete"}`. `sandbox_get` exposes `readiness.checks` and `command_started_at`; no `running` sandbox alias. `REVISION_CONFLICT` includes current revision.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"failed","phase":"kits","cleanup_state":"complete"},"request_id":"rq_01"}`
**Visual/consoles.** Create → preparing checks → command-ready → agent-ready. Failure retains Operation and failed stage. T3 Code/Herdr/SSH show Session `starting` until real readiness and truthful failure afterward.
**Pass/fail.** All 10 checks (`template,engine,identity,network,settings,kits,auth,connections,command,agent`) are inspectable; unrequested checks alone may be skipped. 2 concurrent edits at one revision yield exactly 1 success. Cancel before commit releases 100% of reservations; after commit reports every committed/unknown effect.
**E2E.** Fixed cases `host-linux, access-console, secret-result-transport`: fail kit/auth/exit after allocation; lose success response; cancel before/after commit and after success; compare actual effects with outcomes.
**Code tests.** Fake setup dependencies and clocks; independently model revision/cancel state machine. Never mock durable accepted identity, cleanup accounting or real command readiness marker.

### Hosts and fleets

#### HST-001 — First-release host matrix

**Statement.** Release one must support Linux x86_64 and arm64, and Apple Silicon macOS at the accepted minimums.
**Source.** Brief §§5–6, 22, O10; ADR 0001; architecture §§5, 20.
**Interfaces.** `lamp host list --json`; `GET /v1/hosts`; `host_list({})`. Items expose `os,os_version,arch,engine_ids`. Incompatible requests return `ENGINE_UNQUALIFIED`, never fabricated compatibility.
**JSON observation.** `{"items":[{"id":"hs_01","kind":"host","os":"linux","arch":"x86_64","state":"ready"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard host setup shows OS/build/CPU and qualified guest/device rows. All three consoles show the actual session host and guest class.
**Pass/fail.** Required minimums: Linux 5.10+, Ubuntu 22.04+, Debian 12+, Fedora 40+; macOS 26+ on Apple Silicon (certified minimum; older versions uncertified). Qualify native Linux x86_64 and nested Linux arm64 functional evidence independently; native arm64 speed needs an outside native-hardware benchmark. Zero latest-only substitutions for boundary lanes.
**E2E.** Fixed host-linux/host-macos/fallback cases: native LX; nested LX22/D12/F40/L510/LA; M5/AIR on macOS 26. Install, create appropriate guest, exec/files/network/session and uninstall. Record each exact OS-build probe independently. Older macOS has no certification claim.
**Code tests.** Fake OS probes to cover eligibility parsing; never mock actual boundary OS boot, hardware virtualization or installed OS build in release proof.

#### HST-002 — One fleet address and key set

**Statement.** One or many enrolled hosts must present one fleet identity, address and caller key namespace.
**Source.** Brief §§5, 16–17; ADR 0001; architecture §§14–15.
**Interfaces.** `lamp host enroll hs_02 --invitation-id iv_01 --proof '<proof>' --json`; `POST /v1/hosts/hs_02:enroll {"invitation_id":"iv_01","proof":"<proof>"}`; `host_enroll` adds `host_id`. Read fleet data `{"id":"fl_01","coordination_state":"healthy"}`. Invalid enrollment is `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","coordination_state":"healthy","authority_epoch":1},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard invitation→enroll→host ready; T3 Code and Herdr add no second machine entry. SSH lists authorized sessions across all hosts.
**Pass/fail.** 1-, 2-, 3- and 32-host fleets keep 1 fleet identity/key namespace. Each invitation is consumed at most once and bound to target key. Removed host re-enrolls with a new identity.
**E2E.** Fixed storage-fleet/access-console cases: real LX+M5, then LX+M5+AIR; nested Linux peers use LX22/D12/F40/L510/LA. Enroll with pinned proof, list all authorized sessions from one address/key, remove a host and reject old certificates. Thirty-two-host control tests use bounded simulated metadata endpoints on the owned machines; they do not prove 32-VM density or independent failure domains.
**Code tests.** Fake transport/discovery; retain real invitation signature, key binding, one-use journal and common authorizer. Never mock actual cross-host session discovery.

#### HST-003 — Automatic and pinned placement

**Statement.** Placement must satisfy room, OS, devices, storage and each person's credential-custody trust. A requested host pin must be strict.
**Source.** Brief §§5–6, 13; ADRs 0001, 0004; architecture §§5.2, 9.2, 14.
**Interfaces.** `lamp sandbox create --recipe-id rc_01 --host-id hs_02 --json`; `POST /v1/sandboxes {"recipe_id":"rc_01","host_id":"hs_02"}`; `sandbox_create` with body. Read sandbox has `host_id`. Fail with `HOST_NOT_TRUSTED`, `HOST_UNAVAILABLE`, `DEVICE_UNAVAILABLE` or `NO_CAPACITY` as appropriate.
**JSON observation.** `{"error":{"code":"HOST_NOT_TRUSTED","message":"Host is outside credential custody","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard filters compatible hosts, explains rejected options and previews effective policy. Consoles show actual host; a failed pin never appears elsewhere.
**Pass/fail.** 100% of admitted requests meet every selector; 0 credentials on untrusted team hosts. CPU/RAM overcommit defaults off. Queue defaults off; explicit queue ≤100 operations/principal and TTL 60 s with reauthorization.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: fill one host, pin another, request missing device, then take trusted credential host offline while an untrusted host has room; verify failure and 0 credential transfer.
**Code tests.** Fake inventories/rankings; property-test selector intersections and reservations. Never mock actual placement or custody storage inspection in E2E.

#### HST-004 — Coordination failover and sleeping laptops

**Statement.** A surviving host must automatically accept new authorized work under its precommitted disjoint capacity. Failover must preserve quotas, retry identity and exclusive writers without requiring a third host. If either host sleeps, the other can start new work using capacity reserved for it beforehand. It cannot spend the sleeping host's capacity, recover that host's only files, or change fleet-wide permissions.
**Source.** Brief §5; ADR 0001; architecture §14.
**Interfaces.** `lamp fleet get fl_01 --json`; `GET /v1/fleets/fl_01`; `fleet_get` reads `{"coordination_state":"degraded"}`. New structured-key create works within survivor permit; global edits without quorum return `QUORUM_REQUIRED`; old unreachable homes return `IDEMPOTENCY_HOME_UNAVAILABLE`.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","coordination_state":"degraded","authority_epoch":1},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard distinguishes control availability from unavailable local disks or sleeping sessions. T3 Code/Herdr/SSH reconnect to survivor work; no false migration/completion.
**Pass/fail.** Recovery should be ≤10 s after loss on healthy LAN [proposed]. Test both survivors of a 2-host fleet. Zero duplicate allocations, overspent slices or stale writers in 1/2/3-host loss/partition/rejoin. Default permits expire within 24 h; no indefinite new authority without quorum. Schedule heartbeats every 2 s and show lost/stale observations. Timed authority binds `host.boot_id` and `lease.boot_id`; qualify `clock_uncertainty_ms≤1000`, omit it and invalidate timed authority if unknown. Use suspend-aware per-OS clocks and conservative expiry margins; wall-clock jumps never extend authority. Test abrupt sleep with delayed control process and queued I/O, independently fenced by supervisor/guard/storage before wake effects. Supervisor generates a random boot identity for each OS boot/service cold recovery. At maximum 1000 ms uncertainty, stop by expiry minus uncertainty and admit a replacement only after expiry plus uncertainty (at least 2000 ms separation) or a verified physical fence. Independently prove the kernel/framework power gate before vCPU/I/O wake; reject exclusive transferable modes without it.
**E2E.** Fixed cases `storage-fleet, limits-observations`: stop coordinator, sleep either laptop, partition/rejoin, exhaust/expire permit and retry a lost response. Check real allocations and quota ledger.
**Code tests.** Fake clocks/network; independent permit/quorum model includes sequence recovery, fencing and quota return. Never mock actual host loss, durable journals or survivor admission.

#### HST-005 — Opt-in rolling updates

**Statement.** Automatic updates must start disabled, update one host at a time, verify release trust and preserve active/saved state. Failure must stop rollout and permit rollback.
**Source.** Brief §5; ADRs 0001, 0003, 0008; architecture §§13–14.
**Interfaces.** `lamp fleet update fl_01 --update-policy-json '{"automatic":true,"max_unavailable":1,"channel":"stable","drain_timeout_ms":60000}' --json`; `PATCH /v1/fleets/fl_01` with `update_policy`; `fleet_update` adds `fleet_id`. `update_get` exposes `state=failed|rolled_back`; tampering fails `INVALID_ARGUMENT` before apply. Read Update `metadata_versions={root,targets,snapshot,timestamp}`, `security_revocations=[{digest,effective_at,reason,metadata_version}]` and `migration={log_version,snapshot_version,projection_version,phase,rollback_safe}`; phases are `expand|barrier|contract`. These are observations, never caller overrides.
**JSON observation.** `{"data":{"id":"up_01","kind":"update","current_host_id":"hs_01","state":"rolled_back"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard consent→release/provenance preview→drain/apply/check→next host. Consoles show affected availability; failed resume is never replaced by a new session.
**Pass/fail.** 0 updates before opt-in; max 1 host applying/unavailable for rollout. Reject all bad signatures/digests and rollback attacks. N/N−1 controls work; retain exact unrevoked state readers while references remain; a signed security revocation fences them at its deadline. Never update host 2 until host 1 is healthy. Use pinned The Update Framework (TUF) bootstrap, offline threshold root/targets trust, component/dependency identity and monotonic root/targets/snapshot/timestamp metadata versions/expiry. Test old+new-threshold rotation, stolen-key recovery, and every installer/supervisor/engine/seed/kernel/kit/plugin package. Security digest revocation overrides reader retention; its deadline fences active/asleep workers and blocks restore/rollback to revoked bytes. Keep captures inert for patched-reader/disk extraction/reconstruction. Version control-log/snapshot/SQLite formats; journal expand→barrier→contract migrations, retain N−1 readability until quorum capability barrier, and expose `migration.rollback_safe`. Crash every step and roll back after acknowledged writes without restoring stale safety history. Offline hosts install no stale new package; published expiry bounds unknown revocation honestly.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: update with active and suspended sessions, corrupt artifact, interrupt restart, force failed resume, rollback, wake old AIR version; check acknowledged files/RAM.
**Code tests.** Fake downloader/health clock; real signature verification and compatibility/reference journal. Never mock actual upgrade/restart/rollback or saved-state resume.

#### HST-006 — Fastest supported engine per host

**Statement.** Lamp must select the fastest measured qualified engine that meets the request. Security, OS, devices and capture support must be eligibility conditions.
**Source.** Brief §§5–7, 22; ADR 0003; architecture §5.
**Interfaces.** `lamp engine list --json`; `GET /v1/engines`; `engine_list({})`. Items show `digest,certification,evidence_ref,measurements`; sandbox exposes selected `engine_id`. Unqualified engine requests return `ENGINE_UNQUALIFIED`.
**JSON observation.** `{"items":[{"id":"en_01","kind":"engine","certification":"qualified","evidence_ref":"<run-manifest>"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard explains eligible ranking and evidence. T3 Code/Herdr/SSH details show actual boundary and guest; a faster weak boundary never substitutes for a VM.
**Pass/fail.** Choose minimum comparable caller-to-command latency, then fork latency, then overhead. 100% of selected engine records match current OS/CPU/FS/device/security digests. Updates invalidate stale rankings.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: LX/LA Firecracker reference and competing msb_krun/Cloud Hypervisor rows; M5/AIR HVF/VZ. Measure identical requests with guard/audit on; compare selection and actual support.
**Code tests.** Fake measurement records/probes; test deterministic ranking and invalidation. Never mock performance or actual feature qualification as selection evidence.

### Sandboxes

#### SBX-001 — Fast microVM isolation

**Statement.** Real-VM sandboxes must isolate task execution from the host and neighbors, with externally confined engine workers on each OS.
**Source.** Brief §§1–2, 6–7; ADR 0003; architecture §§3, 5, 18.
**Interfaces.** `lamp sandbox get sb_01 --json`; `GET /v1/sandboxes/sb_01`; `sandbox_get` reads `{"boundary":"vm","engine_id":"en_01"}`. Host/neighbor path attempts return `SCOPE_DENIED` or `NOT_FOUND` without effects.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","boundary":"vm","engine_id":"en_01","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard labels VM boundary and native full-VM class where relevant. Consoles run identical task paths in separate sessions and never expose host shells.
**Pass/fail.** 0 host/neighbor canary reads, writes or executions across ATK-01–04. Worker inventory permits only assigned handles. Linux namespaces/seccomp/Linux Security Module (LSM) and Mac App Sandbox/profile/IPC limits each need independent proof.
**E2E.** Fixed cases `host-linux, host-macos`: guest-root escape probes, malformed device paths, inherited-handle and dump attempts; inspect real host canaries.
**Code tests.** Fuzz device/control parsing; fake worker crash. Never mock hypervisor, OS confinement, assigned-handle restrictions or actual escape attempt.

#### SBX-002 — Linux guests on every host

**Statement.** Linux guests must execute on every supported virtualizing host family at native CPU architecture.
**Source.** Brief §§5–6; ADR 0001; architecture §5.
**Interfaces.** `lamp sandbox create --template-id tp_linux --host-id hs_mac --json`; `POST /v1/sandboxes {"template_id":"tp_linux","host_id":"hs_mac"}`; `sandbox_create` with body. Read data shows `guest_os=linux,arch,host_id`. Wrong architecture fails `ENGINE_UNQUALIFIED`.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","guest_os":"linux","arch":"arm64","host_id":"hs_mac"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard separates host OS from guest OS. T3 Code/Herdr/SSH attach to real Linux execution on either Linux or Mac hosts.
**Pass/fail.** Linux command/files/compiler/network/harness probes pass on Linux x86_64, Linux arm64 and Apple Silicon macOS. Emulation cannot substitute for independent microVM proof at the host's native architecture.
**E2E.** Fixed cases `host-linux`: boot, inspect `uname`, run compiler and official harness, then memory fork/suspend.
**Code tests.** Fake compatibility inventory; real guest OS/architecture and process execution are never mocked in host certification.

#### SBX-003 — Native macOS full-VM class and two-per-Mac limit

**Statement.** Native macOS must run only on Macs as a visibly slower full-VM class, built from owner-licensed media. At most 2 resumable macOS allocations may exist per Mac.
**Source.** Brief §§5–6, O8; ADR 0001; architecture §§5–6.
**Interfaces.** Read `template_get` for an owner-built ready native Template and its `minimum_resources,default_resources`. CLI `lamp sandbox create --template-id tp_mac --host-id hs_mac --resources-json '<exact returned default_resources JSON>' --json`; API `POST /v1/sandboxes` with `template_id,host_id,resources`; MCP `sandbox_create` with the identical body. Third macOS allocation is `NO_CAPACITY`; wrong host is `ENGINE_UNQUALIFIED`; an explicit undersized resource value is `INVALID_ARGUMENT` before reservation.
**JSON observation.** `{"data":{"id":"sb_mac","kind":"sandbox","guest_os":"macos","boundary":"vm","host_id":"hs_mac"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/console detail says “macOS full VM — slower,” never a Linux microVM target badge. Native media setup is guided and local.
**Pass/fail.** Running+suspended+retained resumable macOS count ≤2 under concurrent requests. Linux allocations do not count. Actual native OS commands and suspend/resume succeed. Native Mac uses explicit disk-only fallback only when supported RAM fork is unavailable. No redistributed proprietary guest media.
**E2E.** Fixed cases `host-macos`: create 2, suspend 1, reject third, delete 1 and retry. Run actual macOS boot/build and supported save/restore; prove continuing-source RAM fork if supported, or the labeled A2 fresh-boot disk-only path. Record license permitted-use evidence; public-service macOS needs separate license qualification.
**Code tests.** Fake media/license inventories; test atomic slot reservation. Never mock native kernel, supported save APIs, license evidence or independent fork behavior.

#### SBX-004 — Caller-selected resources

**Statement.** Callers must select CPU, memory, disk and process limits, with real admission/enforcement and no silent overcommit.
**Source.** Brief §§5–6, 17; ADR 0001; architecture §§3, 5.2.
**Interfaces.** `lamp sandbox create --template-id tp_01 --resources-json '{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}' --json`; API `/v1/sandboxes` with `template_id,resources`; `sandbox_create` with body. `NO_CAPACITY` or `SCOPE_DENIED` occurs before over-allocation.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard preview shows requested resources, overhead and effective quota. All consoles show limits and actual OOM/ENOSPC/stop reason.
**Pass/fail.** Default Linux resources are exactly 2 vCPU/4096 MiB/20 GiB/4096 process identifiers (PIDs) for Linux, matching B1. MiB/GiB are binary units defined above. Reserve VM/guard/shared-backing overhead and max(2 GiB,10% free disk) host reserve. 0 unauthorized overspend; disk writes fail ENOSPC at quota. Native templates must publish finite `minimum_resources` and `default_resources`; omitted resources use the latter. Reject explicit undersizing before allocation, without silently raising any caller limit. AIR admission includes its 8 GB capacity and actual sustained thermal limits.
**E2E.** Fixed cases `host-linux, host-macos`: saturate CPU/memory/disk/PIDs, fan out forks and cancel partial creates; check actual allocations and cleanup. Mac fixed-vCPU limits are not advertised as precise CPU percentage quotas.
**Code tests.** Fake host capacity; property-test overflow/negative/limit arithmetic and shared-reference release. Never mock actual resource controls or host usage in proof.

#### SBX-005 — Devices only on request

**Statement.** Optional devices must appear only when explicitly requested and qualified. Unsupported or unforkable combinations must fail before unsafe execution/capture.
**Source.** Brief §§5–6; ADR 0003; architecture §5.2.
**Interfaces.** `lamp sandbox create --template-id tp_01 --devices-json '[{"id":"screen","type":"display"},{"id":"keys","type":"keyboard"},{"id":"pointer","type":"mouse"}]' --json`; API `/v1/sandboxes` with same `devices`; `sandbox_create` with body. Errors `DEVICE_UNAVAILABLE` and `DEVICE_NOT_FORKABLE` identify missing rows.
**JSON observation.** `{"error":{"code":"DEVICE_UNAVAILABLE","message":"No qualified requested device","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard device picker shows compatibility and live display/audio/input only when selected. T3 Code/Herdr open authorized streams; SSH offers the trusted device-view link and text session.
**Pass/fail.** Optional types are exactly `display,audio_input,audio_output,keyboard,mouse,gpu,disk`. USB passthrough is not in release one [owner decision 2026-11-09]; a `usb` request fails `INVALID_ARGUMENT`. Zero inherited desktop/microphone/clipboard/USB/home mounts. Mandatory CPU/RAM/timer/boot disk/private management transport are exempt; data NIC exists only for requested network. GPU proof needs actual acceleration, not a rendered display.
**E2E.** Run exactly device-basic and device-physical rows. Verify actual display pixels, loopback audio, keyboard/mouse input, extra disk bytes and accelerated GPU work. GPU: Linux on LX, Linux/macOS on M5/AIR. Missing exposed GPU stays blocked. Other pairings are exclusions; headless rows prove optional-device absence.
**Code tests.** Fake device inventory; retain real constructed-device inspection, input/output memory management unit (IOMMU)/mediated isolation and media/input effects. Fuzz unrequested downstream device options.

#### SBX-006 — Honest fallback and secret restrictions

**Statement.** Nonvirtualizing hosts must label weaker `gvisor` or `process` boundaries. Secrets require a real VM unless the owner grants an explicit Exposure. All verified public-repository CI jobs, including trusted branches, must always require a fresh VM.
**Source.** Brief §6, O5; ADRs 0003–0004, 0007; architecture §§5, 9.3, 16.2.
**Interfaces.** `lamp sandbox create --template-id tp_public --host-id hs_nv --json`; API `/v1/sandboxes` with same IDs; `sandbox_create`. Read data `{"boundary":"gvisor"}` or `{"boundary":"process"}`. Secret defaults fail `VM_REQUIRED`; no exposure can waive any public-job VM policy.
**JSON observation.** `{"data":{"id":"sb_nv","kind":"sandbox","boundary":"gvisor","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard and T3 Code/Herdr/SSH retain a weaker-boundary warning. Override requires separate owner action, shows contamination and never becomes a default recipe flag.
**Pass/fail.** 0 default secret-bearing fallback allocations; 0 public-repository CI fallback allocations even with owner override. Linux fallback uses gVisor; Mac native process fallback cannot pretend to run local Linux. Route to qualified host or fail.
**E2E.** Fixed fallback cases: NV Linux/gVisor; M5/AIR native-process tasks with hypervisor access explicitly denied. Public non-CI work succeeds with weak label; default secret work fails, explicit permitted owner exposure is recorded. Trusted-public and stranger-public CI both fail before allocation even with Exposure. Native process fallback cannot run a local Linux guest or nested Linux Docker.
**Code tests.** Fake virtualization probes and principals; verify exposure truth table. Never mock actual fallback confinement or the absence of execution after denial.

#### SBX-007 — Fork a running sandbox including memory

**Statement.** A memory fork must preserve one consistent RAM/disk/device generation, continue the source, and establish fresh identity and current authority before fork task I/O.
**Source.** Brief §§6–7, 9, 13, A1–A2; ADR 0004 amended; architecture §§5–7.
**Interfaces.** `lamp sandbox fork sb_01 --fork-mode memory --durability local --json`; `POST /v1/sandboxes/sb_01:fork {"fork_mode":"memory","durability":"local"}`; `sandbox_fork` adds `sandbox_id`. Operation result `{"sandbox_id":"sb_02","session_ids":["se_02"]}`; Snapshot reports `preserves_memory=true`. Unsafe device fails `DEVICE_NOT_FORKABLE` before capture. If exposed material can be inherited, first `lamp sandbox prepare-fork sb_01 --fork-mode memory --durability local --json`, `POST /v1/sandboxes/sb_01:prepare-fork {"fork_mode":"memory","durability":"local"}`, or `sandbox_prepare_fork({"sandbox_id":"sb_01","fork_mode":"memory","durability":"local"})`. Capture the empty child `sandbox_id,generation,fork_preparation,expires_at`, obtain new child-bound Exposure approvals, then call `sandbox_fork` with `prepared_sandbox_id,exposure_ids`. No material is copied by preparation.
**JSON observation.** `{"data":{"id":"sn_01","kind":"snapshot","preserves_memory":true,"durability":"local","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard previews memory/attachment/custody semantics and shows source pause plus both descendants. T3 Code/Herdr/SSH discover returned Session IDs. Native-Mac fallback explicitly says disk-only fresh boot, `preserves_memory=false`.
**Pass/fail.** Zero mixed generations or shared private writes in ≥1,000 captures/backend. New machine/network/broker identities, host-time deadlines and userspace PRNG/TLS hooks acknowledge generation before external I/O. Default attachment fork policy is detach; share/capture is explicit. Linux timing is BEN-002 [owner-confirmed B1]. Linux RAM continuity and continuing source are required on both host families; native Mac fallback is A2 only. The original and fork keep running independently. Before the fork can use network or credentials, Lamp gives it a new identity, refreshes its security state and checks current permissions. Both copies contain the same captured work with no mixed or missing writes. Preparation expires in 300 s and is consumed once. `fork_preparation={source_sandbox_id,source_generation,source_revision,exposure_set_digest,expires_at}` binds the full monotonic exposure history, including revoked/expired delivered grants. Recheck revision/history before any raw-secret byte is copied; changed inputs require new preparation/consent. Absent/expired approval fails before capture; expiry deletes the empty child/releases reservations. Inspect real child RAM in separate absent/approved runs. A1 subscription retention is independent of this raw-secret consent.
**E2E.** Fixed cases `host-linux, host-macos, approval-exposure`: dirty generation-pattern RAM/files, in-flight disk writes, concurrent randomness and immediate network, parent continuation, expired handle and source deletion with surviving fork. Verify against independent checksums/history.
**Code tests.** Fake capture failure/time/external lease; crash real capture/head-rotation journal at reserve, seal, fsync, switch, publish, ack and GC. Never mock RAM capture, disk persistence, entropy barrier or source/fork independence.

### Templates and recipes

#### TPL-001 — Lamp's own template format

**Statement.** Templates must use `lamp.template/v1`, with verified immutable content and a confined build. Lamp must reject Dockerfiles, Docker/OCI images and host Docker runtime dependencies.
**Source.** Brief §8, O8; ADRs 0003, 0007; architecture §§6.1, 7.
**Interfaces.** `lamp template create --name tools --definition-blob-id bl_01 --json`; `POST /v1/templates {"name":"tools","definition_blob_id":"bl_01"}`; `template_create` with body. Then `lamp template build tp_01 --definition-blob-id bl_01 --json`, API `POST /v1/templates/tp_01:build`, MCP `template_build`. `INVALID_ARGUMENT` rejects unsupported formats; corrupted input never becomes ready.
**JSON observation.** `{"data":{"id":"tp_01","kind":"template","state":"building","definition_blob_id":"bl_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard upload definition→validate→build stages→boot probe→ready or retained failure. T3 Code/Herdr/SSH start sessions from returned template without a Docker setup step.
**Pass/fail.** Hash-check 100% of seed/lock/root/kernel/init objects before publication; 0 partially published templates. Archive paths stay confined; reject traversal, escaping symlinks, device nodes, hostile disk backing paths and size/count overruns. Native media is built only on suitable owner hosts. Hash integrity alone is insufficient for captures: `capture_provenance={producer_key_id,producer_host_id,owner_id,trust_class,manifest_digest,engine_digest,guest_kernel_digest,device_graph_digest,cpu_baseline,signature}` must prove enrolled authorized capture-worker production before parsing; Template acceleration variants bind it plus compatibility. Reject a correctly hashed but unauthorized capture and quarantine descendants; imported guest disks go through a clean local build.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: upload the complete Journey 1 definition and locked seed, build with no host Docker, boot and run tool. Submit hostile archive, corrupted chunk and interrupted publication; verify no usable bad template.
**Code tests.** Fake downloads/vendor repository; real parser/hash verification/build confinement/reference GC remain. Fuzz archive counts/paths, integer sizes and unauthorized backing handles. Never mock actual build/boot.

#### TPL-002 — Save installed tools into a template

**Statement.** Saving tools must produce reusable clean template content. Tainted subscription/exposure state must be reconstructed from verified nonsecret inputs, never declared clean by byte scanning.
**Source.** Brief §§8, 10, 13, A1; ADRs 0004 amended, 0007; architecture §6.3.
**Interfaces.** `lamp sandbox save-template sb_01 --name python-tools --selected-paths-json '["/opt/tools"]' --reconstruction-blob-id bl_plan --json`; `POST /v1/sandboxes/sb_01:save-template {"name":"python-tools","selected_paths":["/opt/tools"],"reconstruction_blob_id":"bl_plan"}`; `sandbox_save_template` adds `sandbox_id`. Missing clean inputs returns `CLEAN_REBUILD_REQUIRED`.
**JSON observation.** `{"error":{"code":"CLEAN_REBUILD_REQUIRED","message":"Verified reconstruction inputs required","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard explains direct clean save versus clean rebuild, shows tools/provenance and fresh sign-in requirement. All consoles run installed versions from a new sandbox; no inherited personal login.
**Pass/fail.** A pinned tool runs from a new template with 0 reinstallation steps at use time. Zero subscription/password/cookie canaries in transferable content. Attached volumes/caches/browser profiles/signing keys are never absorbed. Arbitrary hidden bearer copies cannot be cleared by scan.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: install recorded tool, save/rebuild, create and run it; hide token in encoded file/journal and assert reconstruction or explicit refusal. Successful required save workflow is tested separately from refusal.
**Code tests.** Fake source package servers and faults; real installation provenance, monotonic taint, saved content hashes and actual recreated guest. Crash publication/GC with live descendants.

#### TPL-003 — Named recipes

**Statement.** Recipes must save template, harness/auth, kits, devices, resources, networking, connections, settings, lifecycle and budget. Names must resolve atomically to immutable versions.
**Source.** Brief §§8–9, 13–15; ADRs 0002, 0004, 0007; architecture §§6, 11–12.
**Interfaces.** `lamp recipe create --input ./recipe.json --json`; `POST /v1/recipes {"name":"dev","template_id":"tp_01","harness_id":"hr_01","auth_mode":"api_key","kit_ids":[],"devices":[],"resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096},"network_policy_id":"np_01","connection_ids":["cn_01"],"settings_source_ids":["ss_01"]}`; `recipe_create` with body. `recipe_get` and `recipe_list` resolves name to ID before create. `REVISION_CONFLICT` prevents stale pointer updates.
**JSON observation.** `{"data":{"id":"rc_01","kind":"recipe","name":"dev","template_id":"tp_01","revision":1,"digest":"<actual-digest>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard New/edit recipe→effective-policy preview→compatible hosts→save version→create sandbox. T3 Code/Herdr/SSH show resolved recipe and resulting tools, not only a name badge.
**Pass/fail.** All requested parts match immutable admitted IDs/digests; 0 changes to existing sandbox when recipe name moves. Concurrent pointer updates allow exactly 1 winner. Delete in-use version returns `RESOURCE_IN_USE`; old references stay readable.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: create recipe, create sandbox by resolved name, update recipe, create second and compare versions; exercise conflicting edit and missing dependency.
**Code tests.** Fake registry lookup/capacity; real schema, immutable references and compare-and-swap. Never mock actual requested-kit/network/credential readiness.

### Lifecycle

#### LIF-001 — Idle suspension, saved state and message-driven resume

**Statement.** By default an eligible idle sandbox must save memory and disk durably, release task CPU and memory, and resume the same task on authorized activity before its deadline. After that deadline, saved work needs explicitly authorized continuation under a new Task and Budget.
**Source.** Brief §9, A1, A5; ADR 0004 amended; architecture §6.5.
**Interfaces.** `lamp sandbox suspend sb_01 --durability local --json`; `POST /v1/sandboxes/sb_01:suspend {"durability":"local"}`; `sandbox_suspend` adds `sandbox_id`. `message_create({"session_id":"se_01","location_epoch":1,"text":"Continue"})` triggers coalesced resume. Bad saved state yields failed Operation, never a successful cold start. After expiry, automatic message/exec/SSH/port wake returns `TASK_AUTHORITY_EXPIRED`. Create a new Budget and Task, read revision 7, then `lamp sandbox resume sb_01 --continuation-task-id tk_new --expected-task-id tk_old --expected-revision 7 --json`; REST `POST /v1/sandboxes/sb_01:resume {"continuation_task_id":"tk_new","expected_task_id":"tk_old"}` with If-Match 7; MCP `sandbox_resume` adds `sandbox_id,expected_revision`. For an unexpired same-Task resume, omit both continuation fields.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","state":"suspended","snapshot_id":"sn_01","durability":"local"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows suspended storage, released allocations and wake progress. T3 Code/Herdr/SSH reconnect to continued session and memory marker. Native Mac states retain original compatible Mac/user restriction.
**Pass/fail.** Default idle grace 300 s. After suspend, 0 task vCPUs/guest RAM/pinned fork backing remain allocated; fixed host/template cache is separately counted. Reboot and resume preserve acknowledged state. Prepared Linux resume should have p50 <500 ms [proposed]. Default wall deadline is 3600000 ms including suspension; saved-state retention is separately 604800 s. Show both countdowns. After 24 h the old Task remains stopped. Continuation atomically fences/rebinds preserved sandbox/sessions, advances generation/location epochs and sets new Task `previous_task_id`; old usage/outcomes remain. Require fresh task/generation-bound `exposure_ids` before restore parsing or execution of raw-secret saved state. Test expired approval, old handles and two concurrent continuations: one winner only. Active Task/Budget may be extended within scope before expiry; update never reactivates a stopped Task.
**E2E.** Fixed cases `host-linux, host-macos, continuation-authority`: end a real harness turn, wait 300 s, inspect processes/flows, suspend, reboot host and send message/exec/SSH attach/dev request; assert only 1 resume and no repeat sign-in for permitted private capture.
**Code tests.** Fake timers/wake requests; real fsync/state commit and restored memory are never mocked. Test partial resume failure retaining intact snapshot.

#### LIF-002 — Exact definition of idle

**Statement.** Idle eligibility must require no task session activity, task process or task connection, except a certified harness wait solely for the person's next message. Unknown accounting must count as active.
**Source.** Brief §9, A5; ADR 0004; architecture §6.5.
**Interfaces.** `lamp session get se_01 --json`; `GET /v1/sessions/se_01`; `session_get` reads `{"status":"idle","observation_state":"fresh"}`. Sandbox lifecycle is separately inspected. A request to suspend unsafe active state can return `RESOURCE_IN_USE`. Read Sandbox `suspend_eligibility={eligible,reasons,observed_at,observation_state}` through `sandbox_get` on CLI/API/MCP and dashboard. Reasons are exactly `session_activity,task_process,task_connection,harness_wait_unqualified,accounting_unknown`; stale/unknown is ineligible and only a fresh eligible observation has no reasons.
**JSON observation.** `{"data":{"id":"se_01","kind":"session","status":"idle","observation_state":"fresh"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows both session status and suspend eligibility/reason. T3 Code/Herdr/SSH distinguish blocked human wait from active build, sleeping job or open server socket.
**Pass/fail.** Exempt infrastructure is exactly `lamp-init`, guest helper, fixed audit/generation services and private control channel. Test 1 certified parked harness, 1 build, 1 dev server, 1 sleeping background process and 1 open task connection; only parked wait permits automatic suspend.
**E2E.** Fixed cases `host-linux, host-macos, limits-observations`: run each activity for >300 s, then stop it; compare real process/flow inventory with eligibility and eventual suspend. Task code cannot add an exemption.
**Code tests.** Fake harness wait notifications and clocks; independent activity truth table. Never mock process/connection discovery or real parked-harness continuation.

#### LIF-003 — Expiry and per-sandbox policy overrides

**Statement.** Callers must override lifecycle within their authority. Default suspended retention must delete expired sandbox state without resetting deadlines on sleep, fork or restore.
**Source.** Brief §9; ADRs 0001, 0004; architecture §§6.5, 11, 17.
**Interfaces.** `lamp sandbox update sb_01 --lifecycle-json '{"idle_action":"suspend","idle_grace_seconds":60,"suspended_retention_seconds":3600}' --json`; `PATCH /v1/sandboxes/sb_01` with `lifecycle`; `sandbox_update` adds `sandbox_id`. Widening beyond scope is `SCOPE_DENIED`; stale update is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","lifecycle":{"idle_action":"suspend","idle_grace_seconds":60,"suspended_retention_seconds":3600}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows idle action, countdown, expiry and impact before save/delete. All consoles show pending deletion and ended Session after cleanup.
**Pass/fail.** Default suspended retention is 604800 s. Fixture override 3600 s expires at host-authoritative deadline; clock rollback never extends it. Delete releases 100% of unreferenced resources; attached owner volumes/artifacts survive by their own policies.
**E2E.** Fixed cases `host-linux, host-macos, continuation-authority`: set short test override, sleep/reboot around expiry, wake and verify deletion plus retained volume/artifact; explicitly keep a second sandbox within quota.
**Code tests.** Fake clock and expiry scheduler; real journal deletion/reference safety. Never mock actual stopped processes or stored-byte cleanup.

#### LIF-004 — CI deletion default

**Statement.** A terminal or canceled CI job must lose execution and network authority immediately. The default deletes its sandbox after bounded output collection; an authorized trusted-job policy may retain encrypted stopped state for inspection.
**Source.** Brief §§9, 18; ADRs 0002–0004; architecture §§7.2, 16.
**Interfaces.** `lamp job cancel jb_01 --reason test --json`; `POST /v1/jobs/jb_01:cancel {"reason":"test"}`; `job_cancel` adds `job_id`. `job_get` exposes `collecting` then terminal/`cleanup_failed`; no invented deleted-job success hides a live worker. Lifecycle `on_job_end=delete|retain_stopped` defaults to delete. Configure a trusted job with `sandbox_update` and `lifecycle.on_job_end=retain_stopped`; stranger jobs cannot select this override.
**JSON observation.** `{"data":{"id":"jb_01","kind":"job","state":"collecting","artifact_ids":["ar_01"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard job→collection manifest→execution gone/cleanup failure. T3 Code/Herdr/SSH stop accepting input immediately at cancellation and keep retained logs read-only.
**Pass/fail.** Authority closes within 5 s online. Default collection deadline 120 s; teardown proceeds even with incomplete output. Zero reusable runners or live descendants remain after successful cleanup; reconcile after controller restart. Retained trusted jobs have no live worker, network authority, job token or registered runner. Retention stays bounded/quota-charged; inspection requires a fresh Task. Test default deletion and retained failure separately, and prove both lead to a fresh next-job root. Stranger jobs always delete.
**E2E.** Fixed cases `ci-native, ci-trust`: native CI success, failure, cancellation, lost controller and full output disk; verify actual worker/runner deregistration and post-deletion downloads.
**Code tests.** Fake CI terminal events/store outage; real freeze, output finalize, revoke and worker deletion remain. Assert cancel/complete race yields one terminal result.

#### LIF-005 — Warm pool

**Statement.** Warm pools must hold never-used, signed-out, connection-free sandboxes, match immutable request/security inputs and claim each entry once.
**Source.** Brief §§9, 19; ADRs 0001, 0004; architecture §6.2.
**Interfaces.** `lamp warm-pool create --recipe-id rc_01 --target-size 2 --max-size 2 --json`; `POST /v1/warm-pools {"recipe_id":"rc_01","target_size":2,"max_size":2}`; `warm_pool_create`. Read data `{"ready_count":2,"state":"active"}`; quota exhaustion is `NO_CAPACITY`.
**JSON observation.** `{"data":{"id":"wp_01","kind":"warm_pool","target_size":2,"max_size":2,"ready_count":2,"state":"active"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard pool target/ready/claimed/refilling and charged resources; all consoles see only claimed sessions after owner binding and requested setup.
**Pass/fail.** Default target 0. A target-2 pool never exceeds max 2 ready entries; 20 concurrent claims use any entry at most once. No task-used sandbox returns to pool. Miss uses prepared restore; credential setup stays in observed readiness time.
**E2E.** Fixed cases `host-linux, host-macos, continuation-authority`: claim/refill/disable pool under concurrent creates, mutate one claimed root, then verify later job has no marker or credential residue.
**Code tests.** Fake refill timing; real claim journal, immutable match digest and quota/reference accounting. Never mock pristine-state inspection or active pool resource costs.

#### LIF-006 — Fork alternatives and keep the winner

**Statement.** A caller must fork mid-task, run alternatives independently, select a result and return chosen changes through git without blind overwrite.
**Source.** Brief §§9, 16; ADRs 0001, 0004; architecture §§6.4, 10.1.
**Interfaces.** `lamp change-set apply cs_01 --selected-paths-json '["src/fix.py"]' --json`; `POST /v1/change-sets/cs_01:apply {"selected_paths":["src/fix.py"]}`; `change_set_apply` adds `change_set_id`. Result conflicts stay `state=conflicted`; stale target returns `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"cs_01","kind":"change_set","state":"conflicted","conflicts":["src/fix.py"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard fork tree→compare tests/diff→apply selected winner→delete loser. T3 Code/Herdr/SSH show every alternative and real git conflict/output.
**Pass/fail.** 2 alternatives preserve the same initial RAM marker and independent later writes. Exactly selected commits/paths return; parent changes survive conflict. Every standard Linux fork trial is strictly <1000 ms [owner-confirmed B1]; 1000 ms fails.
**E2E.** Fixed cases `host-linux, host-macos, continuation-authority`: deterministic A-passes/B-fails fixture, discover fork Session IDs from outcomes, apply A, intentionally conflict once, delete B and keep parent/A usable.
**Code tests.** Fake test result chooser, not real git tree/patch application or memory fork. Model reference release after deleting parent/loser.

### Storage that outlives a sandbox

#### STO-001 — Shared caches

**Statement.** Package/build caches must survive sandbox deletion, be shareable within explicit owner/trust namespaces and resist untrusted-to-trusted poisoning.
**Source.** Brief §§10, 18; ADRs 0001, 0004; architecture §7.
**Interfaces.** `lamp cache create --name pip --trust-class trusted --arch x86_64 --quota-bytes 1073741824 --json`; `POST /v1/caches` with those fields; `cache_create`. Attach through `attachment_create` with `resource_kind=cache`. Publish uses `cache_publish({"cache_id":"ca_01","generation_digest":"<actual>","base_digest":"<actual>"})`; stale publication conflicts.
**JSON observation.** `{"data":{"id":"ca_01","kind":"cache","trust_class":"trusted","quota_bytes":1073741824,"used_bytes":4096},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard cache usage/trust/toolchain/version→attach/evict/delete; consoles show actual reuse and independent private task roots.
**Pass/fail.** 2 authorized sandboxes reuse verified package bytes. Public-PR and trusted work have 0 shared writable generations. Quota/LRU eviction retains pinned readers and releases unreferenced bytes exactly once.
**E2E.** Fixed cases `storage-fleet, host-linux, host-macos`: fill cache, delete task, reuse; poison untrusted namespace and verify trusted build checksum unchanged; evict while reader is active.
**Code tests.** Fake package server/eviction pressure; real compare-and-swap publication, hashes, trust ACLs and GC. Never mock subsequent actual build behavior.

#### STO-002 — Named volumes with multiple attachments

**Statement.** Named volumes must support explicit shared-files or exclusive-block semantics, real durability and fenced recovery. Fork must not duplicate a block writer implicitly.
**Source.** Brief §10; ADRs 0001–0002; architecture §§6.4, 7.
**Interfaces.** `lamp volume create --name work --mode shared_files --storage-host-id hs_01 --quota-bytes 1073741824 --json`; `POST /v1/volumes` with those fields; `volume_create`. Attach via `attachment_create`; duplicate block writer returns `RESOURCE_IN_USE`; lost data host returns `DATA_UNAVAILABLE`.
**JSON observation.** `{"data":{"id":"vo_01","kind":"volume","mode":"shared_files","state":"ready","attachment_ids":[]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard mode/settings/storage host→attach/mount→locks/leases→snapshot/recover. T3 Code/Herdr/SSH see actual shared files or exclusive-writer failure, not an empty replacement volume.
**Pass/fail.** Shared-files close-to-open coherence, atomic rename, locks and fsync follow one Samba authority; test case/symlink/permission settings. Exclusive-block allows exactly 1 writer or multiple read-only attachments. Disconnect ≥30 s yields I/O failure. New writer waits for fencing; release one reference never deletes live ancestor.
**E2E.** Fixed cases `storage-fleet, host-linux, host-macos`: simultaneous file writers/locks, rename/fsync, partition and storage-host loss, fork detach/read-only/share-write/capture variants; verify actual surviving files and no stale writer. NV uses selected remote storage service.
**Code tests.** Fake lease time/faults; real Server Message Block (SMB)/block mount, generation rejection, persisted bytes and fsync history. Never mock consistency or writer fencing.

#### STO-003 — Saved CI outputs and logs

**Statement.** Outputs must remain downloadable after execution deletion, with verified complete/incomplete manifests and bounded failed collection.
**Source.** Brief §§10, 18–19; ADRs 0001, 0004; architecture §7.2.
**Interfaces.** `lamp artifact get ar_01 --json`; `GET /v1/artifacts/ar_01`; `artifact_get` reads `{"state":"incomplete","expected_bytes":1048576,"present_bytes":524288}`. `lamp artifact download ar_01 --output ./artifact.tar --json`, `GET /v1/artifacts/ar_01/content`, `artifact_download` return verified manifest/bytes. Missing storage is `DATA_UNAVAILABLE`.
**JSON observation.** `{"data":{"id":"ar_01","kind":"artifact","state":"incomplete","expected_bytes":1048576,"present_bytes":524288},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard jobs→outputs shows exact missing bytes/chunks, retry and cleanup failure. All consoles retain live logs then read-only artifact link; incomplete is never green success.
**Pass/fail.** 1 MiB expected fixture downloads with matching SHA-256 after VM deletion. Collection ends within 120 s default. Only encrypted nonrunnable retry blocks may remain for ≤24 h. Default artifact retention 30 days; every byte is quota-charged.
**E2E.** Fixed cases `storage-fleet, host-linux, host-macos`: success/fail/cancel during upload, full disk and collector restart; download after teardown and compare independent expected bytes, including deliberately incomplete case.
**Code tests.** Fake network/store outage; real hashes, finalize transaction and retry-block expiry. Never mock post-deletion download or actual worker absence.

#### STO-004 — Owner-only persistent state by default

**Statement.** Caches, volumes, artifacts, templates, snapshots and backups must be private to their owner unless explicitly shared. Application admin status must not imply private-content access.
**Source.** Brief §§10, 16–17, O9; ADRs 0001, 0004–0005; architecture §§7, 15.
**Interfaces.** `lamp volume list --json`; `GET /v1/volumes`; `volume_list({})` returns only authorized `items`. Foreign `volume_get` returns `NOT_FOUND`. Explicit Share grants named resource/actions, never guessed-ID access.
**JSON observation.** `{"items":[],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard own storage→share preview→recipient/actions/expiry→revoke. T3 Code/Herdr/SSH file/session lists and errors hide unshared state consistently.
**Pass/fail.** 0 foreign rows, bytes, event fields or existence hints across all storage families and 4 roles. Mode changes leave 100% of existing private resources private. Sharing cannot bypass taint/custody requirements.
**E2E.** Fixed cases `storage-fleet, host-linux, host-macos`: Alice/Bea/admin/viewer create distinct state, guess IDs, list/events/download/attach, share one resource then revoke; verify all unauthorized paths.
**Code tests.** Fake principals, retain real ACL/filtering before serialization and transfer. Never mock actual mount/download or query-result filtering in integration.

#### STO-005 — Durability, backup and restore [added]

**Statement.** Local saves survive a restart while that disk is intact. A save confirmed on two hosts survives losing either one without losing saved data. A backup restores only through its last completed save. Lamp must show that time and never replace missing files with an empty sandbox.
**Source.** Brief §§5, 9–10, 13; ADRs 0001, 0004; architecture §7.1.
**Interfaces.** `lamp backup create --resource-kind volume --resource-id vo_01 --target-host-ids-json '["hs_02"]' --json`; `POST /v1/backups` with same fields; `backup_create`. `backup_restore({"backup_id":"bk_01","target_host_id":"hs_02","new_authority_epoch":2})` is explicit recovery. Missing content yields `DATA_UNAVAILABLE`; wrong custody `HOST_NOT_TRUSTED`. `backup_restore` and `sandbox_restore` may take owner-authorized `recovery_credential_source_id`, a host-held handle, never private key bytes. Read desired/achieved counts, receipts and `recovery_state` on CLI/API/MCP/dashboard; transitions unavailable/lost/restore_required must be observable without invented state aliases.
**JSON observation.** `{"data":{"id":"bk_01","kind":"backup","state":"ready","consistency":"application","authority_epoch":1,"manifest_digest":"<actual-digest>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard backup→consistency/time/receipts→restore preview with RPO→staged verify→activate. Consoles show unavailable/restore-required work honestly; coordination recovery alone never claims recovered files.
**Pass/fail.** Durable acknowledgement follows sealed data/key envelopes → persisted ancestry/directories → manifest/journal commit → acknowledgement or RAM release. Linux needs verified fsync/fdatasync/directory flush through every backing layer; APFS needs actual F_FULLFSYNC and qualified directory→same-device barrier ordering. SQLite safety stores use write-ahead log (WAL)/synchronous=FULL; Mac also fullfsync=ON/checkpoint_fullfsync=ON and observed VFS calls. Time these barriers. Local `desired_replicas=1`; replicated defaults to 2 and acknowledges only when `achieved_replicas≥desired_replicas` and ≥2. Only authorized durable bytes plus tested decryption count. `recovery_state=available|unavailable|lost|restore_required` is separate from lifecycle state. Snapshot/Artifact/Template `replica_receipts` and Backup `receipts` include `host_id,manifest_digest,envelope_digest,decrypt_verified_at,committed_at,signature`. Backup recovery point is its last completed save; volatile fork loses unpersisted state. A stale backup cannot revive old keys, effects, epochs or writers. Encrypt each owner object under a fresh data key with authenticated owner/object/format binding and qualified unique nonces. `key_envelopes` entries use `recipient_kind,recipient_id,key_id,wrapping_algorithm,wrapped_key_blob_id,envelope_digest`; recipients are authorized custody hosts or offline owner data recovery. Signing-root recovery is distinct from data recovery. Rehearse real-byte restore on a clean replacement OS without the original protected store; refusal to set recovery up is visibly nonrecoverable and cannot earn a replica receipt. A1 personal custody and original-Mac VZ limits still apply. Rotate/recommit envelopes before retiring keys, reject tampering/wrong owner, and disclose already-copied keys and backup expiry on deletion.
**E2E.** Fixed cases `storage-fleet, host-linux, host-macos`: local reboot, destroy sole copy in disposable fixture, lose one replica, restore backup, later rejoin old disk read-only; compare independent fsync ledger and rejected old mutation keys.
**Code tests.** Fake disk-loss signals, not filesystem persistence. Crash before/after each reserve/fsync/publish/ack/delete/GC; independently check retained ancestors, bytes, signed receipts and authority epoch. Fault a disposable block backend to discard/reorder unflushed writes; use real filesystem/block integration for each qualified barrier. Deliberately omit or reorder object, directory and journal flushes and require the external acknowledgement oracle to detect lost acknowledged data. Process kill, orderly reboot or an application fsync ledger alone cannot certify durability. Never fault the owner's live disk.

### Network

#### NET-001 — Host-enforced network setup

**Statement.** All routing, membership, filtering and ingress keys must remain on the host. Policy must be installed before task execution and cannot be bypassed by guest root or nested Docker.
**Source.** Brief §11, O3; ADR 0002; architecture §8.
**Interfaces.** `lamp sandbox update sb_01 --network-policy-id np_01 --json`; `PATCH /v1/sandboxes/sb_01 {"network_policy_id":"np_01"}`; `sandbox_update` adds `sandbox_id`. Inspect `effective_policy_revision`; unavailable guard fails `HOST_UNAVAILABLE` or `AUDIT_UNAVAILABLE` where audit is cause.
**JSON observation.** `{"data":{"id":"sb_01","kind":"sandbox","network_policy_id":"np_01","effective_policy_revision":2},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard effective-policy preview→atomic apply→acknowledged revision. T3 Code/Herdr/SSH show denied flow reason; guest route edits never change authoritative policy.
**Pass/fail.** 0 denied packets at independent sinks across IPv4/IPv6/Transmission Control Protocol (TCP)/User Datagram Protocol (UDP) and nested Docker. Disable transparent socket impersonation (TSI), implicit forwarding/host roots and alternate VMM networking. Guard death stops path, never direct fallback. Default public internet is open while private/LAN/loopback/link-local/metadata/fleet control is blocked.
**E2E.** Fixed cases `host-linux, host-macos, fallback`: guest root changes routes/DNS/firewall/env; kill guard and probe every packet path; verify allowed public fixture still works before failure.
**Code tests.** Fake DNS/provider fault; real compiler/swap, transport-bound identity and kernel/packet-stack enforcement. Never mock packet sinks or actual route isolation.

#### NET-002 — Independent outbound exits

**Statement.** Each sandbox must choose direct, VPN or proxy egress independently. Selected-exit failure must drop traffic without bypass. A provider supplies the public IP; Lamp must report the observed exit honestly.
**Source.** Brief §11; ADR 0002; architecture §8.
**Interfaces.** `lamp network-attachment create --sandbox-id sb_01 --type exit --plugin-id pg_vpn --config-ref-json '{"configuration_id":"cfg_vpn","digest":"<verified>"}' --json`; `POST /v1/network-attachments` with same fields; `network_attachment_create`. Read data `{"type":"exit","observed_exit_ip":"<verified-IP>","state":"ready"}`. Failed attachment leaves create failed; no ready badge.
**JSON observation.** `{"data":{"id":"na_01","kind":"network_attachment","type":"exit","state":"ready","observed_exit_ip":"<verified-IP>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard selects provider/config→test reflector→observed exit→attach. Consoles show route/failure in task detail while host keeps provider credentials private.
**Pass/fail.** At most 1 selected exit/sandbox. Three independent fixture routes produce exactly their preverified direct/VPN/proxy source IPs; provider configurations behind one network address translation (NAT) are not promised unique IPs. Zero direct packets after VPN/proxy death. Resolve/classify every candidate in the selected exit/member context, then bind the numeric IP, port and protocol. Preserve hostname only for TLS/HTTP identity; deny plugin re-resolution, alternate-address/private fallback and unapproved redirects. Test split proxy DNS, IPv6/mapped IPv4, UDP and remote-private endpoints. The remote exit operator remains trusted to honor that numeric target. All exit fixtures run on owned machines using existing permitted no-charge routes; a missing external-route positive remains blocked.
**E2E.** Fixed cases `host-linux, host-macos, fallback`: external reflector plus independently configured direct, VPN and proxy fixtures on owned machines using permitted no-charge routes, each with recorded IP; run simultaneously, kill/restart selected exit and inspect sinks.
**Code tests.** Fake provider config/error, not observed network source or routing. Test atomic attachment revision, credential isolation and fail-closed reconnect.

#### NET-003 — Private network membership

**Statement.** A sandbox must have its own approved private-network identity, separate state and only mapped inbound ports. Membership must not expose the host or neighbors.
**Source.** Brief §11; ADR 0002; architecture §8.1.
**Interfaces.** `lamp network-attachment create --sandbox-id sb_01 --type private_member --plugin-id pg_ts --config-ref-json '{"configuration_id":"cfg_member","digest":"<verified>"}' --port-maps-json '[{"protocol":"tcp","member_port":8080,"guest_port":8080}]' --json`; API `/v1/network-attachments` same fields; `network_attachment_create`. `network_attachment_test` checks ready identity. Unsupported endpoint protocols fail `INVALID_ARGUMENT`.
**JSON observation.** `{"data":{"id":"na_02","kind":"network_attachment","type":"private_member","state":"ready","identity":"<provider-member-identity>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard membership enrollment/approval→peer identity→mapped ports/revoke. T3 Code/Herdr/SSH show sandbox member, not host member masquerading as guest.
**Pass/fail.** Peer sees unique sandbox member IP/identity. Outbound and mapped inbound TCP/UDP pass; 0 whole-host/subnet listeners. DNS uses member context and guard. Raw Ethernet/ICMP/multicast are explicitly unsupported in endpoint mode; required UDP bridge must qualify before support claim.
**E2E.** Fixed cases `host-linux, host-macos, fallback`: connect TCP/UDP both directions, verify identity, fork new member, resume reauthorized logical member, probe host/neighbor. Qualify SaaS and Headscale separately.
**Code tests.** Fake enrollment/provider outages; real tsnet bridge/datagrams/state isolation and peer observations. Never equate a constructed endpoint object with working guest membership.

#### NET-004 — Inbound dev-server URLs

**Statement.** Dev-server ports must be private signed-in by default, generation-scoped, shareable with expiry and immediately revocable without widening sandbox authority.
**Source.** Brief §§11, 17, O4; ADRs 0002, 0006; architecture §§8.2, 15.3.
**Interfaces.** `lamp port create --sandbox-id sb_01 --port 8080 --protocol http --access private --json`; `POST /v1/ports` with same fields; `port_create`. Then `port_publish({"port_id":"po_01"})`; `port_share_link({"port_id":"po_01","expires_at":"<t0+3600s>"})` returns `{"url":"<host-issued>","expires_at":"..."}`. Revoked/foreign resource is `NOT_FOUND`.
**JSON observation.** `{"data":{"id":"po_01","kind":"port","access":"private","state":"ready","origin":"https://p-random.f123.fleet.eilmer.si"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard Ports→publish→open authenticated page→share TTL/recipient→unpublish. T3 Code/Herdr open page; SSH prints authorized URL. No forwarding of owner cookies to app.
**Pass/fail.** Default link TTL 3600 s; exact port/generation only. Online revoke closes existing HTTP2/WebSocket/TCP paths within 5 s. Relay callers require Eilmer sign-in; anonymous expiring grants require explicit owner-domain route. One-use link fragment never reaches Referer/log/app. Share-link result is transient `{grant_id,url,expires_at}`; lose the response and retry the same key: same Operation/grant, `secret_available=false`, no second token. Scan Operation outcomes, request hashes, events and audit for bearer URL; none persists. Lost URL needs revoke and a newly authorized link. Derive its default expiry from t0+3600 s; approvals use their separate t0+60 s fixture.
**E2E.** Fixed cases `host-linux, host-macos, fallback`: serve fixture, anonymous default denial, owner access/share/expiry/revoke while socket open, suspend/resume once and verify new generation cannot reuse origin/grant.
**Code tests.** Fake time/ingress failures; real grant exchange, origin isolation and active-channel closure. Never mock browser cookie or upstream-header behavior.

#### NET-005 — Allow and deny lists

**Statement.** Policies must enforce normalized destination rules, deny precedence, parent ceilings and atomic narrowing, with transport and strict-site modes clearly distinguished.
**Source.** Brief §11, O3; ADR 0002; architecture §8.
**Interfaces.** `lamp network-policy create --filter-mode strict_sites --default-egress deny --allow-json '[{"host_pattern":"packages.example.org","protocol":"tcp","ports":[443]}]' --json`; `POST /v1/network-policies` with those fields; `network_policy_create`. `network_policy_update` uses revision; stale edit is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"np_01","kind":"network_policy","filter_mode":"strict_sites","default_egress":"deny","revision":2},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard rule editor previews effective allow/deny and affected active flows before apply. All consoles receive truthful denied destinations; strict mode never claims to inspect opaque subscription requests.
**Pass/fail.** Deny wins; all policy layers intersect. Normalize Internationalized Domain Names in Applications (IDNA)/trailing dots/IP forms and wildcard label boundaries. Strict sites enforce DNS/Server Name Indication (SNI)/certificate/HTTP authority/redirect agreement and deny bypass DNS over HTTPS (DoH)/DNS over TLS (DoT)/QUIC/Encrypted Client Hello (ECH)/CONNECT. Newly denied flows close ≤5 s online. Transport mode permits raw public TCP/UDP without false application filtering claims.
**E2E.** Fixed cases `host-linux, host-macos, fallback`: rebinding, private redirect, IPv4-mapped IPv6, literals, fronting, QUIC, existing-flow narrowing and guard/exit death. Allowed fixture also succeeds.
**Code tests.** Fake DNS/rebinding, retain actual URL/packet normalization and policy compiler. Never mock enforcement or sink absence; concrete malformed/conflicting selectors must deny.

#### NET-006 — Explicit access between sandboxes

**Statement.** Neighbor access must be denied by default and allowed only by directed, port/protocol-scoped Connections within parent policy.
**Source.** Brief §11; ADR 0002; architecture §§8–9.
**Interfaces.** `lamp connection create --type sandbox_link --endpoint '<authorized-endpoint>' --access full --json`; `POST /v1/connections` same fields; `connection_create`. Attach with `attachment_create`; `connection_invoke` remains schema-bound where mediated. Missing scope is `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","type":"sandbox_link","state":"ready","access":"full"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source→target port→scope→attach/test→detach/revoke. T3 Code/Herdr/SSH show explicit link and actual reachable service only.
**Pass/fail.** A→B:8080 grant permits exactly that directed transport; B→A and B:8081 remain denied. Zero implicit fleet-wide membership. Revocation closes existing flow ≤5 s online; fork defaults detach.
**E2E.** Fixed cases `host-linux, host-macos, fallback`: same-host/cross-host listeners, scan before link, use allowed port, attempt reverse/other ports, fork and revoke while transfer active.
**Code tests.** Fake topology, not intersection authorizer or host identity binding. Never mock real cross-host packets or active revoke.

### Modularity

#### PLG-001 — Customizable plugins with process isolation

**Statement.** Anyone must be able to write separately run plugins. Ordinary plugins must have no ambient host/sandbox authority. Trusted engines are the explicit assigned-RAM exception, admitted by exact digest with owner warning.
**Source.** Brief §12, A3; ADRs 0003, 0008; architecture §§3, 13.
**Interfaces.** Upload a verified plugin package as a Blob, then `lamp plugin create --package-blob-id bl_package --digest '<verified>' --json`; `POST /v1/plugins {"package_blob_id":"bl_package","digest":"<verified>"}`; `plugin_create` with that body. Manifest/provenance supplies type, trust class, protocol and immutable schema bindings. `plugin_enable` enables admitted permissions. `plugin_trust({"plugin_id":"pg_engine","digest":"<verified>","warning_acknowledged":true})` additionally requires owner authority for an engine. Unauthorized trust is `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"pg_01","kind":"plugin","type":"console","trust_class":"confined","state":"enabled"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard install→provenance/permissions/trust preview→enable/test→disable. Engine warning states it can read/change assigned RAM. T3 Code/Herdr/SSH keep unrelated sessions alive after plugin failure.
**Pass/fail.** Confined/credential/platform plugins receive 0 RAM handles. Protocol frames ≤1 MiB, default call deadline 10 s, default 16 concurrent RPCs, 64 MiB/0.25 CPU. Crash/hang/flood trips circuit breaker; 0 core or unrelated session failures. Native programs and WebAssembly System Interface (WASI) both qualify.
**E2E.** Fixed cases `host-linux, host-macos, access-console`: hostile plugin probes filesystem/network/IPC/inherited handles/subprocesses; update attempts permission expansion and new engine digest; verify new trust required and no neighbor/broker access.
**Code tests.** Fake plugin endpoints/faults; real OS profile/token/ACL, typed grants and permission comparison. Never mock actual process confinement or engine warning/owner admission.

#### PLG-002 — Public plugin categories

**Statement.** Lamp must support engine, network, credential-source/vault, harness, kit, CI, trigger, console, access and notification-sink plugins under one versioned contract.
**Source.** Brief §§11–12, 15–18; ADRs 0002–0003, 0008; architecture §13.
**Interfaces.** `lamp plugin test pg_01 --suite-digest '<verified>' --json`; `POST /v1/plugins/pg_01:test {"suite_digest":"<verified>"}`; `plugin_test` adds `plugin_id`. `plugin_get` shows `type,protocol_version,trust_class,permissions,limits`; major protocol mismatch returns `INVALID_ARGUMENT`. Discover `adapter_list` and `adapter_get` (`lamp adapter list --json` and `lamp adapter get ad_01 --json`, `GET /v1/adapters` and item path); each immutable Adapter publishes `plugin_id,digest,operation_schemas,config_schema,result_schemas,compatibility,certification,evidence_ref`. Create Configuration with `lamp configuration create --input ./configuration.json --json`, `POST /v1/configurations`, or `configuration_create`: `name,plugin_id,adapter_id,schema_digest,document,credential_source_refs`. Inspect `configuration_get/list`; delete through `configuration_delete`. No Configuration/Adapter update exists. `config_ref={configuration_id,digest}` always selects an immutable ready validated document.
**JSON observation.** `{"data":{"id":"pg_01","kind":"plugin","type":"console","protocol_version":"lamp.plugin/1","state":"enabled"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard catalog groups the 10 canonical types and their granted objects. All consoles can discover/use a third-party console adapter without bespoke owner-project code.
**Pass/fail.** At least 1 conformance fixture/type, all 10 types. N/N−1 protocol rollout works; additive minors negotiate. Every effect binds owner/task/operation/generation/deadline and is reauthorized by host core. No dynamic plugin loads into `lampd`. From an empty fixture, every interface uploads package→creates Plugin→discovers adapter/schema→creates and authorizes host-held CredentialSource→creates/gets validated Configuration→selects its exact config_ref for network/access→tests a useful effect. Nonsecret `document` is ≤64 KiB; secret material stays in CredentialSource. Plugin `package_blob_id,manifest_blob_id,adapter_ids` and schema digests must match admitted bytes. Unknown/stale schemas fail; no preloaded cfg_vpn/cfg_member/cfg_tailnet/cfg_domain or local-host file substitutes. Config changes publish new ID/digest. Configuration state transitions are `validating|ready|failed|deleted`; read its server-derived digest and reject references before ready or after deletion. Adapter schemas and result schemas stay immutable and discoverable after publication.
**E2E.** Fixed cases `host-linux, host-macos, access-console`: install each type, perform one useful authorized effect, deny one extra effect, disable/delete and verify dependent cleanup.
**Code tests.** Fake external service per type; real protocol validation/cancellation/grant enforcement. Never mock actual plugin process boundary or claimed useful effect.

### Harnesses, credentials and settings

#### CRED-001 — Any harness

**Statement.** Lamp must run official public harnesses without an owner-specific integration and provide working install, sign-in, useful tool work, session observation and supported continuation/spawn hooks.
**Source.** Brief §§13, 16, 22; ADRs 0003–0004; architecture §§9.2, 10.
**Interfaces.** `lamp harness list --json`; `GET /v1/harnesses`; `harness_list({})` returns version/digest/auth/certification records. `session_create` selects returned `harness_id`; unsupported route is `AUTH_TRANSPORT_UNSUPPORTED`, continuation gap `CONTINUATION_UNSUPPORTED`.
**JSON observation.** `{"items":[{"id":"hr_01","kind":"harness","version":"<pinned>","binary_digest":"<actual>","certification":"unqualified"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard chooses harness/version/auth mode and shows qualified rows. T3 Code/Herdr/SSH open each real harness and its distinct native sub-agents.
**Pass/fail.** The fixed harness-api cases require exact-version rows for all 11 named harnesses: Claude Code, Codex, OpenCode, Cursor CLI, Copilot CLI, Amp, Goose, Gemini CLI, Pi, Devin, Droid. Each fixed required auth/guest row completes ≥1 useful inference+tool task. “Any” also permits a public third-party harness plugin. Missing rows remain release gaps.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: official binaries plus pinned public project; verify actual inference, file edit/test, question/input and session discovery. A hosted-only vendor mode is not fabricated as local support.
**Code tests.** Fake deterministic harness status/tool protocol; never use fake as vendor compatibility proof. Real binaries, runtime CA, auth precedence and tool execution remain in integration.

#### CRED-002 — Vendor-approved sign-in catalogue

**Statement.** Every harness auth route must have dated vendor-permission evidence and exact-version transport tests. Unsupported methods must fail without raw-secret compatibility fallback.
**Source.** Brief §13; ADR 0004 amended; architecture §§9.1–9.2.
**Interfaces.** `lamp harness get hr_01 --json`; `GET /v1/harnesses/hr_01`; `harness_get` exposes `auth_modes,permission_sources,hooks,evidence_ref`. `auth_flow_create` starts approved native route; `AUTH_TRANSPORT_UNSUPPORTED` names the absent method safely.
**JSON observation.** `{"error":{"code":"AUTH_TRANSPORT_UNSUPPORTED","message":"Auth mode has no qualified transport","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard route→vendor URL/permission date→native sign-in→real auth probe. Consoles see signed-in readiness only after actual turn, never because a token file exists.
**Pass/fail.** 100% of catalogue rows pin binary/runtime digest, allowed plan/context, endpoints, CA configuration, refresh/capture/continuation/spawn evidence. No Anthropic OAuth impersonation or assumed Gemini consumer entitlement. Test streaming, WebSocket where used, refresh and useful tool call, not only CA acceptance.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: run each approved mode of all 11 harnesses with dedicated permitted accounts; expire/rotate credentials and test both success and honest denial.
**Code tests.** Fake vendor errors/time/refresh endpoint, retain real TLS/JWT/route selection. Never mock actual vendor login/entitlement when certifying a route.

#### CRED-003 — Subscription custody and vendor-only use

**Statement.** Subscription logins must stay on the person's explicitly trusted custody hosts and own sandboxes, with vendor-only data egress. Lamp-run servers must never store, refresh or proxy them.
**Source.** Brief §13, A1; ADR 0004 amended; architecture §§8, 9.2.
**Interfaces.** `lamp credential-source get cr_01 --json`; `GET /v1/credential-sources/cr_01`; `credential_source_get` exposes `custody_host_ids` and state, never bearer. Subscription recipe uses `auth_mode=subscription`; untrusted placement returns `HOST_NOT_TRUSTED`.
**JSON observation.** `{"data":{"id":"cr_01","kind":"credential_source","custody_host_ids":["hs_01"],"state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows subscription exception, trusted host set and vendor-only policy. T3 Code/Herdr/SSH show token-free task work separately from credential-bearing harness execution.
**Pass/fail.** 0 subscription bytes in relay/control metadata/Lamp servers/other person's state. General Git/download/build/private-service work runs in a separate token-free sandbox via bounded certified tool hooks. Test every bridge argument/result for encoded exfiltration. Approved refresh chains serialize or use independent permitted sessions; no copied refresh race.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: real signed-in useful coding across two sandboxes; hostile token-bearing process tries every extra Connection and tool bridge; offline trusted host must fail placement. Direct authorized host transfer only.
**Code tests.** Fake vendor refresh/time, not custody ACL, egress guard or bridge schema. Never mock actual continued approved sign-in, credential location or exfiltration sinks. Unproved unrestricted bridge stays blocked.

#### CRED-004 — Private capture versus clean transfer

**Statement.** Same-person private fork/suspend must retain login under A1. Templates, saved-state exports beyond trusted hosts and any cross-person share/handoff must be clean and start the harness fresh.
**Source.** Brief §§6, 8–9, 13, A1; ADR 0004 amended; architecture §6.3.
**Interfaces.** `lamp snapshot create --sandbox-id sb_01 --durability local --json`; `POST /v1/snapshots {"sandbox_id":"sb_01","durability":"local"}`; `snapshot_create`. Read data `{"capture_trust":"owner_private","preserves_memory":true,"custody_host_ids":["hs_01"]}`. Unsafe transferable save returns `CLEAN_REBUILD_REQUIRED` before publication.
**JSON observation.** `{"data":{"id":"sn_01","kind":"snapshot","capture_trust":"owner_private","preserves_memory":true,"custody_host_ids":["hs_01"],"state":"ready"},"request_id":"rq_01"}` Then separately call `share_create({"resource_kind":"session","resource_id":"se_01","subject_id":"pr_bea","actions":["session.watch"],"expires_at":"<t0+60s>"})`, wait, and `share_get` the returned ID. It reads `state=pending_clean_handoff` until clean handoff completes; snapshot creation alone never creates a Share.
**Visual/consoles.** Dashboard previews retained private login versus clean fresh-harness transfer. Sharing stays `pending_clean_handoff` until reconstruction/continuation completes. All consoles retain same-owner task state and never send token-bearing snapshot/transcript to another person.
**Pass/fail.** Hidden subscription copies may remain only in owner-private custody. Taint propagates through RAM/disk/journals/caches/artifacts/descendants and never clears by path deletion or scans. Zero bearer canaries in successful transferable outputs. Missing reconstruction is visible feature failure, not success-by-refusal.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: hide literal/encoded canaries, private fork/suspend continues real sign-in; save/export/watch-share/handoff reconstruct clean tool content and fresh harness. Probe old captures and transfer bytes.
**Code tests.** Fake capture destination faults; real monotonic taint/custody/ref graph and clean builder. Never mock hidden-state retention or successful clean transfer oracle.

#### CRED-005 — API keys stay behind a host proxy

**Statement.** API/model/CLI keys must remain in an owner broker. Guests must receive only useless sentinels and scoped transport-bound authority; unsupported clients must never get a real-key fallback.
**Source.** Brief §§13–14; ADRs 0002, 0004; architecture §9.1.
**Interfaces.** `lamp connection test cn_01 --json`; `POST /v1/connections/cn_01:test {}`; `connection_test` adds `connection_id`. Read Connection contains references, not secrets. Wrong destination/action returns `SCOPE_DENIED`; unsupported TLS/auth returns `AUTH_TRANSPORT_UNSUPPORTED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","credential_source_id":"cr_01","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source→scope→test→attach shows protected mode. T3 Code/Herdr/SSH see real authorized results and redacted audit, never raw API keys in output/env/files/argv.
**Pass/fail.** Zero key canaries in guest RAM/disk/streams/logs/captures/sinks. Per-owner CA private key remains broker-only; public root installed in required OS/runtime/nested trust stores. Separate HTTP1/HTTP2/WebSocket pools; validate upstream TLS and reauthorize redirect/retry/upgrade. Deny credential mint/export and unbounded signing.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: actual official clients perform useful streaming/tool operation through broker, dump guest state, attack reflection/smuggling/owner pools and invalid CA. Verify independent sinks and outputs.
**Code tests.** Fake upstream vendors/time; retain actual TLS, framing, canonical request, secret injection and filtering. Named regressions in QA-003 include chunk-split secrets and malformed authority; encoded reflection from untrusted recipient must be denied, not trusted to generic redaction.

#### CRED-006 — One-way settings and approved return

**Statement.** Chosen settings must copy one way into sandboxes. Returning sandbox edits must require an authenticated explicit selected-path approval and matching source revision. Signing keys must stay outside task memory.
**Source.** Brief §13; ADR 0004; architecture §9.4.
**Interfaces.** `lamp settings-source create --host-id hs_01 --source-ref fixture-settings --path-allowlist-json '["AGENTS.md","harness.json"]' --json`; `POST /v1/settings-sources` same fields; `settings_source_create`. `settings_change_apply({"settings_change_id":"sc_01","selected_paths":["AGENTS.md"],"approval_id":"ap_01"})` returns Operation. Stale base is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"sc_01","kind":"settings_change","state":"proposed","base_revision":1,"selected_paths":["AGENTS.md"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source/categories→preview copy→sandbox diff→approve selected paths→apply/reject. T3 Code/Herdr show trusted diff/decision; SSH links to approval or uses scoped CLI, without automatic writeback.
**Pass/fail.** Cover 6 categories: harness settings; skills/plugins/slash commands; instruction files; MCP list; git identity; signing references. Zero source writes before approval; no writable source mount or host install-script execution. Symlink escape and unselected changes never apply.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, auth-subscription`: copy all categories, edit inside, verify source unchanged, approve 1 path, change source concurrently and reject stale apply; perform approved git signing through broker.
**Code tests.** Fake storage/approval timing, retain real diff/path confinement/base revision/signing digest policy. Never mock actual source files or signing-key absence.

### Connections

#### CON-001 — Signed-in MCP gateway and limited-token exception

**Statement.** External MCP servers must use a host gateway with host-held OAuth/tokens. A server that must run inside may receive only explicit limited scoped short-lived authority.
**Source.** Brief §14; ADRs 0002, 0004; architecture §9.1.
**Interfaces.** `lamp connection create --type mcp --endpoint '<approved-MCP-endpoint>' --credential-source-id cr_01 --adapter-id ad_01 --allowed-operations-json '["read_issue"]' --access read_only --json`; `POST /v1/connections` with those fields; `connection_create`. Attach via `attachment_create`; invoke `connection_invoke({"connection_id":"cn_01","attachment_id":"at_01","operation":"read_issue","arguments":{"issue":1}})` only if that exact adapter schema declares it. Unauthorized tools fail `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","type":"mcp","access":"read_only","state":"ready","allowed_operations":["read_issue"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard discover/auth/test→tool/resource scope→attach/revoke; all consoles see usable signed-in tool result and limited-token exposure classification, never host gateway bearer.
**Pass/fail.** 0 upstream tokens in task default. Host OAuth uses discovery/PKCE/exact resource/audience/refresh identity. Inside Lamp token TTL ≤300 s and binds sandbox/generation/tool/resource; unavoidable limited upstream bearer is disclosed/tainted and never an owner-wide key.
**E2E.** Fixed cases `broker-browser-vault`: public third-party MCP fixture completes real authorized request, deny another owner/tool/resource, rotate refresh, test necessary inside-server route and expired token.
**Code tests.** Fake MCP vendor/JWKS/refresh, retain actual OAuth validation, typed invocation and gateway filtering. Never mock bearer location or upstream negative effects.

#### CON-002 — Signed-in CLI connections

**Statement.** `gh`, `stripe` and `aws` must complete supported scoped operations while their API tokens remain host-side. Supported operation lists and exclusions must be explicit.
**Source.** Brief §14; ADRs 0002, 0004; architecture §9.1.
**Interfaces.** `lamp connection create --type cli --endpoint '<approved-endpoint>' --adapter-id ad_aws --credential-source-id cr_01 --access read_only --json`; API `/v1/connections` same fields; `connection_create`. `connection_invoke` uses adapter `operation,arguments`; no generic arbitrary signing endpoint. Export/mint attempt returns `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"cn_01","kind":"connection","type":"cli","adapter_id":"ad_aws","access":"read_only","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard CLI adapter→account/service/region/resource/actions→test→attach. T3 Code/Herdr/SSH run actual CLI commands and see scoped success/denial, not raw tokens.
**Pass/fail.** At least 1 useful pinned operation/client and every fixed required mode. Deny `gh auth token`, token/key creation, AWS STS credential return, presigned URL/arbitrary signing by default. Certify SigV4 canonical and chunked streaming separately; HTTP/WebSocket/refresh errors cannot leak secrets.
**E2E.** Fixed cases `broker-browser-vault`: dedicated GitHub fixture repository, Stripe test account and scoped AWS test resource; execute authorized read/write variants and denied export/mint, inspect actual state and scans.
**Code tests.** Fake upstream endpoints/errors; keep real SigV4, TLS, redirect/owner binding and response filter. Never mock official CLI transport or useful service effect in certification.

#### CON-003 — Owner-machine services and four access levels

**Statement.** Each owner-machine service must offer `off`, `read_only`, `ask_first` or `full`, enforced on real operations. Raw TCP must not claim semantic read-only.
**Source.** Brief §14; ADR 0002; architecture §9.1.
**Interfaces.** `lamp connection create --type service --endpoint '<exact-service>' --access ask_first --adapter-id ad_db --json`; API `/v1/connections` same fields; `connection_create`. `connection_invoke` with exact adapter operation requires matching `approval_id`; absent approval is `APPROVAL_REQUIRED`.
**JSON observation.** `{"error":{"code":"APPROVAL_REQUIRED","message":"Exact service action requires approval","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard endpoint→access→semantic-role preview→test/attach. T3 Code/Herdr/SSH show pending ask-first with inspect/approve/deny link and actual DB result.
**Pass/fail.** Off gives 0 effects; read-only permits reads and 0 writes including side-effecting functions; ask-first consumes exactly 1 bound approval; full stays within named endpoint/resource and parent grant. Private grant opens no other LAN service.
**E2E.** Fixed cases `broker-browser-vault`: dedicated local PostgreSQL/dev-service fixture, run read/write/side-effect queries at all 4 levels, replay/deny/change target/revoke during active connection; inspect actual database.
**Code tests.** Fake service errors/time; retain actual semantic role/adapter/schema and approval consumption. Never mock database state or packet restrictions.

### Kits

#### KIT-001 — Logged-in browser outside the agent sandbox by default

**Statement.** Default logged-in browsing must use a separate locked-down browser/profile with protected origin/action controls. Cookies must never reach task memory or agent observations.
**Source.** Brief §15; ADRs 0002, 0004; architecture §9.3.
**Interfaces.** `lamp browser create --task-id tk_01 --sandbox-id sb_01 --kit-id kt_browser --adapter-id ad_site --allowed-origins-json '["https://site.example.org"]' --json`; API `/v1/browsers` same fields; `browser_create`. `browser_action` accepts only `navigate,click,type,read,screenshot,download` under adapter schema. Raw cookie/CDP/evaluate requests fail `SCOPE_DENIED` or `INVALID_ARGUMENT`.
**JSON observation.** `{"data":{"id":"br_01","kind":"browser","state":"agent_control","sandbox_id":"sb_01","allowed_origins":["https://site.example.org"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard browser opens approved remote view and clearly shows agent/human control. T3 Code/Herdr embed/link that view; SSH provides private takeover URL. Unsafe password workflows enter private human control [owner-confirmed B3].
**Pass/fail.** Zero cookie canaries in agent DOM/text/screenshots/downloads/streams. No raw CDP/storage/devtools/profile/export/extensions/filesystem URLs. Dedicated profile never inherits everyday host browser. Hostile same-origin reflection must be blocked or kept human-only; one friendly page is insufficient proof. Browser `network_policy_id,network_attachment_ids,effective_policy_revision` inherit task safety ceiling, approved private grants, selected exit, audit and deadlines for every network producer, including workers/background fetches. Partition profiles/contexts/workers by task/policy/exit; host mode requires equivalent confinement. Denied background requests and VPN death must fail closed.
**E2E.** Fixed cases `broker-browser-vault`: actual browser visits controlled HTTPS login/app and hostile same-origin fixture, performs useful authenticated task, attacks cookie APIs/reflection/downloads. Unknown secret-bearing flows stay private human control [owner-confirmed B3].
**Code tests.** Fake site errors, retain real browser action allowlist, filtered observations and control leases. Never mock cookie storage, JS reflection or screenshot/download data.

#### KIT-002 — Opt-in browser inside with copied logins

**Statement.** Browser login copying into the task sandbox must require a separate authenticated Exposure, with persistent warning/taint. No recipe/sub-agent default may enable it.
**Source.** Brief §15, O5, A1; ADR 0004 amended; architecture §§6.3, 9.3.
**Interfaces.** `lamp exposure create --sandbox-id sb_01 --generation 1 --type browser_logins --destination '<approved-profile>' --json`; `POST /v1/exposures` same fields; `exposure_create`. `exposure_activate` needs matching approval/action digest. Missing permission returns `APPROVAL_REQUIRED` or `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"ep_01","kind":"exposure","type":"browser_logins","state":"pending_approval","sandbox_id":"sb_01","generation":1},"request_id":"rq_01"}` Before activation there is no `activated_at` and no cookie copy.
**Visual/consoles.** Dashboard explains readable copied logins before activation and keeps warning after revoke. T3 Code/Herdr/SSH show active exposure in session detail; it is never labeled protected-cookie mode.
**Pass/fail.** Zero copied cookies before explicit authorization. Deliberate fixture exposure is excluded from unauthorized-leak score but recorded. Revoke stops future profile access ≤5 s; it cannot erase bytes already read. Taint requires destroy/rebuild before clean export/share. Create→get must show `pending_approval` with zero delivery. Approved activation makes permission `active`; `activated_at` independently records actual delivery/copy, including prepared-fork consent. Denied/expired/failed or unknown delivery never appears as successful injection. Inspect the actual credential location in every transition. `exposures[]` retains delivered grants after revoke/expiry; revocation cannot remove contamination history.
**E2E.** Fixed cases `broker-browser-vault, approval-exposure`: default denial, owner approves one profile, inspect actual copied location, revoke, attempt recipe inheritance and child reuse, then attempt transferable save and require clean rebuild.
**Code tests.** Fake identity/time, retain actual exposure activation/taint/approval binding. Never mock resulting credential location or warning/audit record.

#### KIT-003 — Human browser takeover

**Statement.** A person must take exclusive private control for multifactor authentication (MFA), human-verification challenge (CAPTCHA) or unsafe password entry, then return control only after sensitive observations are cleared.
**Source.** Brief §15; ADR 0004; architecture §9.3.
**Interfaces.** `lamp browser takeover br_01 --lease-id le_human --json`; `POST /v1/browsers/br_01:takeover {"lease_id":"le_human"}`; `browser_takeover` adds `browser_id`. Return via `browser_return_control`. Stale/wrong holder action fails `SCOPE_DENIED` or `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"id":"br_01","kind":"browser","state":"human_control","control_lease_id":"le_human"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr show agent paused and trusted private person view; phone uses same authenticated view. SSH displays short-lived private URL. Person types password if secrecy of automated fill cannot be guaranteed [owner-confirmed B3].
**Pass/fail.** Exactly 1 control holder. During human entry, agent gets 0 input acceptance and 0 DOM/screenshots/network logs/clipboard/recording bytes. Resume only on approved cleared page; sensitive fields/code never appear after return. Leases expire/revoke normally.
**E2E.** Fixed cases `broker-browser-vault, access-console`: force 2FA/CAPTCHA/form failure, race agent input/screenshots/stream reconnect against takeover/return; complete actual human challenge using dedicated account.
**Code tests.** Fake challenge timing, retain real lease compare-and-swap and observation gate. Never mock browser input ownership or recorded frames.

#### KIT-004 — Toolchains and Docker inside a sandbox

**Statement.** Kits must install requested tools and saved state. Optional Docker must run only inside the sandbox, with no host daemon socket or bypass of host policies.
**Source.** Brief §§8, 15, 18; ADRs 0002–0003, 0007; architecture §§6.1, 9.
**Interfaces.** `lamp attachment create --sandbox-id sb_01 --resource-kind kit --resource-id kt_docker --json`; `POST /v1/attachments` same fields; `attachment_create`. Ready checks include kits. Unsupported device/VM request returns `DEVICE_UNAVAILABLE`/`VM_REQUIRED`.
**JSON observation.** `{"data":{"id":"at_01","kind":"attachment","resource_kind":"kit","resource_id":"kt_docker","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard kit picker→verified plan/version→install progress→test/remove. T3 Code/Herdr/SSH run tool version, `docker build` and Compose service output inside the guest.
**Pass/fail.** Zero host Docker dependency/socket; unrequested kits absent. Actual Docker build, compose/service containers and toolchain compile succeed. Nested traffic/PIDs/resources/audit remain attributed; process completeness includes nested runtime [owner-confirmed B4].
**E2E.** Fixed cases `docker`: build pinned public Docker fixture, run service tests and denied-network probe; delete sandbox and verify 0 nested survivors/host effects.
**Code tests.** Fake package repository/plan faults; real plan verification, guest install and nested guard/accounting. Never mock actual Docker operations or process attribution.

#### KIT-005 — Password managers, fill-on-behalf and per-vault approvals

**Statement.** 1Password, Bitwarden, KeePassXC and Proton Pass must provide useful protected login. Standard forms use external fill; unsafe forms use private person typing. Password must never be exposed to agent [owner-confirmed B3]. Per-secret injection must remain a separate opt-in Exposure.
**Source.** Brief §15, O5; ADR 0004; architecture §9.3, modified default B3.
**Interfaces.** `lamp browser fill-password br_01 --credential-source-id cr_vault --secret-ref login_ref --action-digest '<actual-digest>' --approval-id ap_01 --json`; `POST /v1/browsers/br_01:fill-password` with same fields; `browser_fill_password` adds `browser_id`. Pending per-vault decision returns `APPROVAL_REQUIRED`; unsupported safe-fill transitions to human control [owner-confirmed B3].
**JSON observation.** `{"data":{"id":"ap_01","kind":"approval","state":"consumed","resource_ref":{"kind":"credential_source","id":"cr_vault"}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard vault→each-use toggle→exact origin/frame/form/action→phone/console approval→private fill/submit or takeover→authenticated task. T3 Code/Herdr show decision and safe result; SSH links trusted view without secret text [owner-confirmed B3].
**Pass/fail.** All 4 vaults complete ≥1 standard-form login and useful task; hostile/encoded reflection, reveal, changed form/action and malicious same-origin page yield 0 password bytes to agent. Per-use approval consumes once and cannot cross vault/secret/form. Unknown secrecy always uses human takeover [owner-confirmed B3]. Injection requires per-secret authenticated Exposure and audit warning.
**E2E.** Fixed cases `broker-browser-vault`: actual pinned vendor source interfaces, standard and hostile forms, phone/console approve/deny/expiry/replay/concurrent use. Scan guest, output, browser observations, logs and sinks; prove human fallback works [owner-confirmed B3].
**Code tests.** Fake vault service errors/time around real retrieval-to-fill boundary; real action digest, atomic approval and observation suppression. Never mock all-four-vault useful fill, browser reflection or password secrecy.

### Consoles and sessions

#### SES-001 — One machine in any console

**Statement.** Console plugins must present the fleet as one machine while exposing every authorized Session independently, including sessions on different hosts.
**Source.** Brief §§5, 16; ADR 0001; architecture §10.
**Interfaces.** `lamp session list --json`; `GET /v1/sessions`; `session_list({})` returns object `items` with `id,sandbox_id,location,status`. Foreign session get returns `NOT_FOUND`.
**JSON observation.** `{"items":[{"id":"se_01","kind":"session","sandbox_id":"sb_01","status":"working"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard session list and T3 Code/Herdr one-machine lists match IDs/parentage. SSH `sessions` enumerates the same authorized set. Install/configure console plugin once per fleet, not per host.
**Pass/fail.** 1 configured fleet entry shows 3 sessions across ≥2 hosts and each native sub-agent. Zero hidden/duplicate entries after reconnect/failover. Public T3 Code and Herdr versioned contracts must qualify; no owner-project substitute.
**E2E.** Fixed cases `access-console, harness-api, harness-arm, harness-native`: configure public T3 Code, Herdr and reference console, start sessions on each host, compare API/SSH membership and reconnect.
**Code tests.** Fake console discovery transport, retain real ACL/pagination/ID mapping. Never mock actual console contract/visible live sessions as compatibility proof.

#### SES-002 — Every session visible, live and interactive

**Statement.** Every session must support live watch and authorized input with working/blocked/idle status and truthful observation freshness.
**Source.** Brief §16; ADR 0001; architecture §§10, 12.4.
**Interfaces.** `lamp session watch se_01 --json`; `GET /v1/sessions/se_01/output?cursor=c1`; `session_watch({"session_id":"se_01","cursor":"c1","limit":100,"wait_ms":25000})` returns framed `items,next_cursor`. `session_input` requires lease/location epoch/sequence/base64. Wrong writer is `SCOPE_DENIED`.
**JSON observation.** `{"items":[{"stream_id":"st_01","sequence":1,"channel":"stdout","data_base64":"NDIK","byte_offset":0,"location_epoch":1}],"next_cursor":"c2","request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr/SSH open/watch/type/resize/interrupt/reconnect same Session. Status carries `status_reason,observed_at,observation_state`; no-output is not automatically idle or completed.
**Pass/fail.** Exactly 3 statuses: working, blocked, idle; freshness exactly fresh/stale/unknown. One input writer, arbitrary authorized watchers within quota. Zero lost acknowledged input, duplicated input effects or mixed stdout/stderr/exit channels. Spool-full stops task, never drops acknowledged output.
**E2E.** Fixed cases `access-console, harness-api, harness-arm, harness-native`: deterministic harness forces all statuses and binary/multibyte output; official harness separately proves useful input; disconnect/resize/revoke/reconnect and inspect sequences.
**Code tests.** Fake harness time/status, retain actual input dedup, frame bytes and leases. Never mock pseudoterminal (PTY) effects or real console delivery.

#### SES-003 — Ordinary SSH everywhere

**Statement.** Stock SSH must list sessions, attach live or open a sandbox shell on every access route without patched terminals, mandatory custom ProxyCommand or host shell access.
**Source.** Brief §§16–17; ADRs 0001, 0009; architecture §§10, 15.2.
**Interfaces.** CLI parity `lamp session attach se_01 --lease-id le_01 --json`; API `POST /v1/sessions/se_01:attach {"lease_id":"le_01"}`; MCP `session_attach` adds `session_id`, result StreamDescriptor. SSH uses the exact common `sessions`, `attach <session_id>`, `shell <sandbox_id>` grammar. Unauthorized attach returns denial with no output.
**JSON observation.** `{"operation_id":"op_attach","resource_ids":[{"kind":"session","id":"se_01"}],"request_id":"rq_attach","replayed":false,"result":{"stream":{"stream_id":"st_01","transport":"websocket","url":"wss://api.f123.fleet.eilmer.si/v1/sessions/se_01/output","expires_at":"<t0+30s>","cursor":"c1","max_frame_bytes":69632,"max_payload_bytes":65536}}}` The substituted expiry is 30 s after the authoritative fixture clock; the URL has no bearer.
**Visual/consoles.** Dashboard AccessRoute displays returned SSH host/port/fingerprint. T3 Code/Herdr may use console plugin or ordinary SSH; terminal selector opens each live session.
**Pass/fail.** Two fleets on one relay use distinct assigned TCP ports with 0 cross-fleet delivery. Fleet host CA is independently pinned; caller enrollment maps same scopes. No agent/TCP forwarding by default. Resize/EOF/exit/detach/reconnect preserve byte/frame/input semantics.
**E2E.** Fixed cases `access-console, harness-api, harness-arm, harness-native`: OpenSSH through local, Tailscale, relay, owner-domain and Cloudflare plugin route; verify each route's explicit SSH transport, 2 fleets, unauthorized user and key rotation.
**Code tests.** Fake carrier loss, not SSH host certificate verification/channel policy. Never mock ordinary unmodified SSH handshake, terminal bytes or relay opacity.

#### SES-004 — Caller-chosen sub-agent placement and git return

**Statement.** Sub-agents must run in their own sandbox or share the parent's as the starter chooses. Own mode must copy consistent selected files and return changes through reviewable git state.
**Source.** Brief §16; ADRs 0001, 0004; architecture §10.1.
**Interfaces.** `lamp session spawn se_01 --sandbox-mode own --recipe-id rc_01 --selected-paths-json '["src","tests"]' --json`; `POST /v1/sessions/se_01:spawn {"sandbox_mode":"own","recipe_id":"rc_01","selected_paths":["src","tests"]}`; `session_spawn` adds `session_id`. Result `{"session_id":"se_02","sandbox_id":"sb_02"}`; unsupported native hook is `CONTINUATION_UNSUPPORTED`, never silent shared mode.
**JSON observation.** `{"data":{"id":"se_02","kind":"session","parent_session_id":"se_01","sandbox_id":"sb_02","task_id":"tk_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard parent tree→own/shared authority disclosure→spawn→returned diff/apply/reject. T3 Code/Herdr/SSH see native and explicit sub-agents separately with independent input/output.
**Pass/fail.** Selected tracked/uncommitted/untracked files, modes/binaries and base tree digest match; ignored files require explicit selection, credentials never implicit. Shared mode requires compatible trust and discloses common files/process/network/secrets. Child grants intersect parent+recipe+delegation; all share Task budget. Conflicts preserve parent files.
**E2E.** Fixed cases `access-console, harness-api, harness-arm, harness-native`: own and shared spawn, inspect real files/process access, selected git return/conflict and non-git temporary repository; native sub-agent must have stable Session ID and descendant charging.
**Code tests.** Fake spawn notifications, retain real git bundle/patch/revision application, scope intersection and native session discovery integration. Never mock actual separate execution or returned file content.

#### SES-005 — Teleport between laptop and sandbox

**Statement.** Teleport must move stable logical harness continuation both directions with one authoritative location, preserved acknowledged messages/files/tool outcomes and locally approved auth.
**Source.** Brief §16; ADRs 0001, 0004; architecture §10.2.
**Interfaces.** For host destination, first obtain destination-owner Approval and `native_execution_grant_create` for `host_id,user,working_paths,harness_digest,expires_at,approval_id`; it also needs `native_execution_grant.create`. Then `lamp session teleport se_01 --location-json '{"kind":"host","host_id":"hs_laptop"}' --native-execution-grant-id ng_01 --json`; `POST /v1/sessions/se_01:teleport {"location":{"kind":"host","host_id":"hs_laptop"},"native_execution_grant_id":"ng_01"}`; `session_teleport` adds `session_id`. Read `native_execution_grant_get/list` and delete to revoke. Sandbox destination uses `location={kind:sandbox,host_id,sandbox_id}`. Unsupported continuation is `CONTINUATION_UNSUPPORTED`. NativeExecutionGrant reads expose `principal_id,host_id,user,working_paths,harness_digest,action_digest,approval_id,expires_at,state`; state is exactly `active|revoking|revoked|expired`, and delete reports revocation status rather than silently discarding live authority.
**JSON observation.** `{"data":{"id":"se_01","kind":"session","location_epoch":2,"location":{"kind":"host","host_id":"hs_laptop"},"state":"active"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard Move→tools/auth/destination preview→checkpoint/stage/fence/commit/resume→same Session at new location. T3 Code/Herdr/SSH reconnect by stable ID/cursor; native laptop execution is clearly host authority.
**Pass/fail.** 0 concurrent authoritative source/destination effects and 0 lost acknowledged inputs. Before commit source can rollback; after commit only new epoch acts. Unknown source fence waits for lease expiry. Quorum required for cross-host location. Transcript+new prompt is not continuation; arbitrary cross-OS RAM migration is not promised. `session.teleport` alone cannot stage host files or start any host process. A sandbox-move-only key targeting `location.kind=host` is denied before any effect. Grant binds authenticated principal, exact destination host/effective UID, canonical absolute working paths, harness digest, action digest, Approval and expiry. Reject symlink escapes and arbitrary privileged accounts. Independent destination-owner approval is mandatory; test an allowed move and out-of-path/user/digest/expired/revoked denials.
**E2E.** Fixed cases `harness-api, harness-arm, harness-native, continuation-authority`: each named harness's laptop→sandbox→laptop route with pending tools/files/messages; kill carrier/source/destination at all 6 phases; assert continuation and exact committed/unknown outcomes.
**Code tests.** Fake transfer faults/time, real continuation format/epoch/fencing journal. Never mock official harness continuation, actual source stop or credential-free envelope.

#### SES-006 — Own sessions by default

**Statement.** Team and public-service callers must see only their own sessions unless explicitly granted content access. Admin roles alone must not reveal another person's work.
**Source.** Brief §§16–17, O9; ADRs 0001, 0005; architecture §§10, 15.1.
**Interfaces.** `lamp session list --json`; `GET /v1/sessions`; `session_list({})` filters `items`. Foreign `session_get`, `session_watch` and `session_attach` returns `NOT_FOUND`, including event replay/search-derived paths.
**JSON observation.** `{"error":{"code":"NOT_FOUND","message":"Resource not found","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr/SSH show own sessions; explicit shares appear with grant source and expiry. Empty/private views leak no names, statuses, transcripts or counts.
**Pass/fail.** Across owner/admin/member/viewer, 0 ungranted content fields or existence signals in lists/get/events/errors/transcripts. Host-admin trust is disclosed separately from application role.
**E2E.** Fixed cases `access-console, harness-api, harness-arm, harness-native`: create private sessions, guess IDs, enumerate API/CLI/MCP/SSH and console caches, change mode/role and replay old cursor.
**Code tests.** Fake identities, retain actual query/event/frame filtering before serialization. Never mock actual console/SSH forbidden output observations.

#### SES-007 — Explicit watch and type sharing

**Statement.** Sharing must grant watch and type separately, with expiring/revocable authority. Credential-bearing sessions must complete a clean fresh-harness handoff before any cross-person sharing activates.
**Source.** Brief §16, A1, O9; ADRs 0004 amended, 0005; architecture §§6.3, 10, 17.3.
**Interfaces.** `lamp share create --resource-kind session --resource-id se_01 --subject-id pr_bea --actions-json '["session.watch"]' --expires-at <t0+60s> --json`; `POST /v1/shares` same fields; `share_create`. `share_revoke` starts pending revocation. Watch-only input fails `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"sh_01","kind":"share","state":"active","actions":["session.watch"],"resource_kind":"session","resource_id":"se_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard Share→person/watch-or-type/action authority/expiry→clean-handoff progress→active→revoke. T3 Code/Herdr/SSH watchers receive no input lease; typing does not grant shell/files/vault/browser/approval.
**Pass/fail.** Watch grant causes 0 harness actions from attempted input. Type needs exclusive Lease and discloses power to direct harness. Zero credentials in shared state/transcript; unsupported clean continuation remains blocked. Online revoke closes stream/input ≤5 s; prior received content cannot be recalled.
**E2E.** Fixed cases `access-console, harness-api, harness-arm, harness-native`: Alice shares with Bea watch then type, races second writer, shares subscription-bearing session, revokes while open and after role downgrade; inspect real output/effects/capture.
**Code tests.** Fake notification/time; real Share state machine, clean handoff, Lease and frame authorization. Never mock actual attempted harness action or stream closure.

### Access, accounts and keys

#### ACC-001 — Built-in Tailscale access and keyless CI connectivity

**Statement.** Built-in Tailscale access must reach NAT-hidden hosts without manual inbound setup and support qualified CI federation without stored secrets.
**Source.** Brief §17; ADRs 0001–0002; architecture §§8.1, 15.1.
**Interfaces.** `lamp access-route create --type tailscale --config-ref-json '{"configuration_id":"cfg_tailnet","digest":"<verified>"}' --json`; `POST /v1/access-routes {"type":"tailscale","config_ref":{"configuration_id":"cfg_tailnet","digest":"<verified>"}}`; `access_route_create`. Read data `{"state":"ready","ssh_port":22}`. Missing federation cannot silently inject a saved key; exchange fails `UNAUTHENTICATED`/`SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"tailscale","ssh_port":22,"state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard bootstrap→tags/service approval→ready→remove, with network prerequisites. T3 Code/Herdr/SSH connect to one fleet identity behind NAT.
**Pass/fail.** Zero manual NAT port forwards and zero stored CI Lamp/tailnet secret on qualified federation route. Isolate state/auth environment per member; issuer/claims/tags/scopes/feature registration verified. SaaS and Headscale separate manifests; DERP/churn/propagation/memory are reported.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: enroll, connect API/MCP/CLI/SSH, remove ingress and reconnect to survivor, test unsupported Headscale federation honesty.
**Code tests.** Fake control-service outage/JWT timing; real federation validation/tag policy and tsnet identity bridge. Never mock actual NAT traversal or job credential absence.

#### ACC-002 — Owner domain with automatic HTTPS

**Statement.** Owners must use their own domain with automatic valid HTTPS while retaining common fleet identity, authorization and SSH routing.
**Source.** Brief §17; ADRs 0001, 0006, 0009; architecture §§14.3, 15.
**Interfaces.** `lamp access-route create --type owner_https --config-ref-json '{"configuration_id":"cfg_domain","digest":"<verified>"}' --json`; `POST /v1/access-routes` same fields; `access_route_create`. Read `address,ssh_host,ssh_port,trust_fingerprint,state`. Issuance failure stays failed, never insecure ready.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"owner_https","state":"ready","address":"<owner-selected-HTTPS-address>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard domain/DNS validation→certificate/progress→API/dashboard/SSH instructions→renew/failure. T3 Code/Herdr/SSH use advertised endpoints, same session IDs and host trust.
**Pass/fail.** Valid hostname chain and renewal with 0 TLS-verification bypasses; expiry/outage cannot fall back to plaintext. Explicit owner domain is allowed; Eilmer-operated services still use only eilmer.si. Health-aware endpoint/DNS failover retains fleet pin.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: dedicated test-owned domain, staging ACME then representative trusted TLS, renewal/outage/failed DNS, ingress loss and OpenSSH route. No owner's production DNS changed by this writing task.
**Code tests.** Fake ACME/time/DNS; real hostname/chain/certificate signing request (CSR) authorization. Never mock browser/client certificate verification in HTTPS certification.

#### ACC-003 — Local-only, offline access

**Statement.** Non-relay fleets must work locally and offline with local identities or owner OIDC independent of Eilmer/Clerk.
**Source.** Brief §17; ADR 0005; architecture §15.1.
**Interfaces.** `lamp access-route create --type local --json`; `POST /v1/access-routes {"type":"local"}`; `access_route_create`. Local `sandbox_create` works with valid enrolled key; absent identity returns `UNAUTHENTICATED`.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"local","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard local setup/login→create/session→offline indicator. T3 Code/Herdr/SSH access local LAN/loopback route without relay sign-in.
**Pass/fail.** With Eilmer/Clerk/internet blocked, local account create/exec/files/watch/input/delete succeeds; 0 mandatory hosted calls. Owner OIDC requires reachable owner provider, while already authorized local keys remain within expiry.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: provision local identity, block all public traffic, run full local journey, restart services and inspect packets; separately test owner OIDC outage/expired key.
**Code tests.** Fake external identity outage; retain real local key/password/passkey verification and operation authorization. Never mock offline packet evidence or actual task execution.

#### ACC-004 — Cloudflare Tunnel access plugin

**Statement.** Cloudflare Tunnel must be a separately configured access plugin with an explicit termination/trust statement, common scopes and an independently proved stock-SSH route.
**Source.** Brief §17; ADRs 0002, 0009; architecture §§15.1, 20.2.
**Interfaces.** `lamp access-route create --type cloudflare --plugin-id pg_cf --config-ref-json '{"configuration_id":"cfg_cf","digest":"<verified>"}' --json`; API `/v1/access-routes` with same fields; `access_route_create`. Read `address,ssh_host,ssh_port,state`; unproved transport remains failed/unqualified rather than silently requiring a patched SSH client.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"cloudflare","state":"failed"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard install/configure→TLS termination disclosure→test HTTP and SSH→ready/disable. T3 Code/Herdr/SSH retain same fleet/session scopes.
**Pass/fail.** Useful API/dashboard/CLI/MCP and ordinary SSH paths qualify. No assumption HTTP Tunnel automatically provides stock SSH; mandatory custom ProxyCommand does not pass SES-003. Cloudflare termination is never advertised as Eilmer relay opacity.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: validate actual route, SSH list/attach/shell, revoke and tunnel loss. If no stock-compatible route is available, row remains blocked.
**Code tests.** Fake provider failures, real access authorization and route mapping. Never mock actual third-party route or TLS trust behavior.

#### ACC-005 — One Eilmer account; independent identity without relay

**Statement.** Relay use must require the one shared Clerk Eilmer account. Non-relay fleets must also support local accounts and own OIDC. Identity must be immutable issuer+subject, not email matching.
**Source.** Brief §17; ADRs 0005, 0009; architecture §15.
**Interfaces.** `lamp identity-provider create --type oidc --issuer '<owner-issuer>' --client-id lamp --discovery-url '<owner-discovery>' --json`; API `/v1/identity-providers` same fields; `identity_provider_create`. `auth_flow_complete` validates code/state/proof; wrong issuer/audience fails `UNAUTHENTICATED`. Emergency root recovery is local only: `auth_flow_bootstrap` with `purpose=fleet_recovery` and enrolled recovery-key thumbprint, then `lamp fleet recover fl_01 --input ./recovery.json --json`, `POST /v1/fleets/fl_01:recover` over the local recovery socket, or `fleet_recover` with `auth_flow_id,proof,new_root_public_key,fence_proofs`. No ordinary fleet key or relay route can use this exception.
**JSON observation.** `{"data":{"id":"pr_01","kind":"principal","issuer":"<verified-issuer>","subject":"<immutable-subject>","disabled":false},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard chooses Eilmer/local/owner provider appropriately; relay sign-in cannot skip host enrollment. T3 Code/Herdr/SSH enrolled key maps to same Principal and explicit actions.
**Pass/fail.** Zero relay callers without Eilmer sign-in and fleet enrollment; 0 account merges for same email/different issuer or subject. PKCE/state/nonce/exact callback and audience required. Local passwords use Argon2id; Clerk bearer never spreads across sandbox/product origins. Use distinct `root_fingerprint,recovery_key_fingerprint` (base64 SHA-256 canonical public-key bytes). Recovery proof signs `auth_flow_id,challenge,fleet_fingerprint,new_root_public_key` with enrolled offline key. Fence/decommission every old voter/issuer so no old quorum can renew; require verifiable stopped receipts, physical fences or published expiry plus uncertainty for all remaining enforcers. `fence_proofs` entries carry `host_id,boot_id,authority_epoch,method,stopped_at,expires_at,receipt` with method `stopped_receipt|physical_fence|expired` and inapplicable times absent. Keep effects stopped until stable recovery receipt/new root/epoch commit, then require explicit host/caller re-enrollment. Test lost all owner keys, stale backup, old owner/root/host/mutation-key rejection. Account recovery cannot replace this proof; signing recovery alone cannot decrypt saved work.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: two fixture Eilmer users, local and test OIDC provider; email collision, account outage, malicious first pairing and account recovery attempts; verify independent fleet-root trust.
**Code tests.** Fake identity provider/JWKS/time, real signature/algorithm/issuer/audience/nonce validation. Never mock actual deployed Clerk cookies or host enrollment in trust certification.

#### ACC-006 — Owner-configured sharing modes

**Statement.** Owner must configure single-owner, team or public-service mode with explicit admission and the accepted owner/admin/member/viewer role matrix. Mode changes must never publish existing private content.
**Source.** Brief §17, O9; ADRs 0001, 0005; architecture §15.1.
**Interfaces.** `lamp fleet update fl_01 --mode team --json`; `PATCH /v1/fleets/fl_01 {"mode":"team"}`; `fleet_update` adds `fleet_id`. Team `invitation_create` and `invitation_accept` binds target key/role/expiry; public `principal_create` and `membership_create` uses owner admission policy. Unauthorized elevation is `SCOPE_DENIED`. Owner transfer is `lamp fleet transfer-owner fl_01 --input ./transfer.json --json`, `POST /v1/fleets/fl_01:transfer-owner`, or `fleet_transfer_owner` with `new_owner_principal_id,acceptance_proof,approval_id`. `acceptance_proof={public_key,signature}` signs canonical `fleet_id,authority_epoch,new_owner_principal_id,action_digest` with the new owner's enrolled key. A one-use Approval shows the actual transfer.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","mode":"team","revision":2},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard mode→admission/roles/quota preview→invite/accept/remove; public-service registration shows isolated quota. T3 Code/Herdr/SSH views remain own-only unless explicit content share.
**Pass/fail.** Role matrix below governs 100% of action tests. Single owner admits only owner; team invitation is one-use; public caller gets distinct issuer/subject principal and bounded capacity. Admin never inherits private content/vault approval; host-admin trust remains separate. One quorum commit changes fleet owner role, advances authority_epoch, rotates root/SSH CA and revokes former-owner management descendants. New-owner approval is needed to reissue host/caller certificates. Reject old root/epoch authority; private content and personal credential custody never transfer implicitly.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: exercise all 4 roles, invitations/removal, outside service admission, same-email collision and single→team→public→team changes with preexisting private sessions/volumes.
**Code tests.** Fake caller identity/intake, real membership/role/delegation/filtering. Never mock actual admission quota or active downgrade effects.

#### ACC-007 — Scoped caller keys

**Statement.** Keys must narrow actions, sandboxes, recipes, hosts, resources, limits and expiry. Delegation/rotation must never broaden authority; secrets must be shown once and hashed at rest.
**Source.** Brief §17; ADRs 0001, 0005; architecture §§11–12, 15.1.
**Interfaces.** `lamp key create --principal-id pr_01 --scope-json '{"actions":["sandbox.create","exec.create"],"recipe_ids":["rc_01"],"host_ids":["hs_01"],"limits":{"concurrency":2}}' --expires-at <t0+60s> --json`; API `/v1/keys` same fields; `key_create`. Result includes transient `secret` once. `key_rotate` creates equivalent/narrower key and revokes old; invalid action/resource returns `SCOPE_DENIED`.
**JSON observation.** `{"operation_id":"op_01","resource_ids":[{"kind":"key","id":"ky_01"}],"replayed":true,"result":{"secret_available":false},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard key wizard→effective intersection/expiry→one-time copy→rotate/revoke acknowledgments. T3 Code/Herdr/SSH use same action scopes; transport membership confers no admin.
**Pass/fail.** Random keys have 256 bits. Supplied selectors intersect; each array is union; empty supplied array grants nothing. Omitted selector never escapes issuer set. Key lost-response replay returns `secret_available=false`, never the secret. All action/resource/limit combinations deny widening.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: create agent key, try wrong host/recipe/action/volume, delegate, rotate during SSH/stream/request, expire across sleep/failover; inspect no forbidden side effect.
**Code tests.** Fake time/claims; real cryptographic generation/hash and scope intersection. Never mock actual API/CLI/MCP/SSH authorization or key-at-rest inspection.

#### ACC-008 — Bound one-use approvals [added]

**Statement.** Protected actions must use authenticated inspectable approve/deny/revoke and one-use consumption bound to principal, task, generation, resource, exact action/destination and expiry.
**Source.** Brief §§13–16, 19; ADRs 0002, 0004–0005; architecture §§9.3, 11–12, 14.
**Interfaces.** `lamp approval create --input ./approval.json --json`; `POST /v1/approvals`; `approval_create` takes the same body: `{"principal_id":"pr_01","task_id":"tk_01","sandbox_id":"sb_01","generation":1,"resource_ref":{"kind":"connection","id":"cn_01"},"action_plan":{"adapter_digest":"<verified>","operation":"read_issue","resource_ref":{"kind":"connection","id":"cn_01"},"arguments":{"issue":1},"destination":"<exact>","secret_refs":[],"redacted_fields":[]},"destination":"<exact>","expires_at":"<t0+60s>"}`. The adapter validates the proposed plan and derives `action_digest`; create never takes an authoritative caller digest. `approval_get` inspects; `approval_approve({"approval_id":"ap_01","action_digest":"<returned>"})`, `approval_deny`, `approval_revoke` decide. Missing approval gives `APPROVAL_REQUIRED`.
**JSON observation.** `{"data":{"id":"ap_01","kind":"approval","state":"denied","action_digest":"<actual>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/T3 Code/Herdr trusted approval card shows effect/recipient/expiry independently of task-supplied text. Phone opens same authenticated view; SSH uses scoped CLI or private link. Watch permission alone cannot approve.
**Pass/fail.** 20 concurrent consumers perform at most 1 effect. Changed action/form/destination/generation, expiry, denial, revoke or wrong approver performs 0 effects. Home unavailable waits; approval cannot be consumed in both partitions. Default TTL is explicit per vault/Connection, never infinite implicitly. Store an immutable, adapter-computed `action_plan` of ≤64 KiB canonical UTF-8 JSON: `adapter_digest,operation,resource_ref,arguments,destination,secret_refs,form_ref,diff_blob_id,redacted_fields`; omit inapplicable form/diff. `form_ref={origin,frame_origin,action_url,method,form_digest}`. Larger effects refer to an immutable bounded diff/manifest Blob. The server derives SHA-256 over plan/context/resource revision/expiry plus broker keyed hash (HMAC-SHA-256) for normalized secret inputs; private values stay in the broker. Render this stored plan, never caller display text, and revalidate it immediately before effect. A misleading description, different SQL, vault item or settings diff must cause zero wrong effects even when its supplied digest looks valid.
**E2E.** Fixed cases `approval-exposure, broker-browser-vault`: ask-first DB/fill/settings actions, approve/deny on console/phone, race replay/change target/role downgrade/cancel; compare real service effects with consumed approval.
**Code tests.** Fake vendor/time/UI text, real digest construction, authorizer and durable atomic consume. Never mock actual protected effect or approval binding.

#### ACC-009 — Active revocation and narrowing [added]

**Statement.** Revoke must stop derived authority, not merely delete a record. Pending and completed outcomes must expose online acknowledgments and remaining offline expiry.
**Source.** Brief §§11, 14, 16–17, 19; ADRs 0001–0002, 0004–0005; architecture §17.3.
**Interfaces.** `lamp grant revoke gr_01 --reason removed --json`; `POST /v1/grants/gr_01:revoke {"reason":"removed"}`; `grant_revoke` adds `grant_id`. `share_revoke`, `connection_revoke`, `key_delete`, `host_delete` and membership removal cascade. Inspect Operation `state,progress,outcome`; no false completion while offline authority survives.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"waiting","revocation_status":{"enforcers":[{"host_id":"hs_02","enforcer":"guard","acknowledgment":"pending","authority_expires_at":"<t0+30s>","residual_capabilities":[{"provider":"fixture","resource_ref":{"kind":"connection","id":"cn_01"},"capability_kind":"fixture_remote_action","revoke_state":"unknown"}]}],"complete":false}},"request_id":"rq_01"}` Unknown provider expiry is omitted, never guessed; adapter-defined fixture capability kind is not a new operation.
**Visual/consoles.** Dashboard revoke impact→pending hosts/leases→complete at acknowledgments/expiry. T3 Code/Herdr/SSH close affected stream/input, remove shares and explain denied reconnect.
**Pass/fail.** Online stop ≤5 s for keys/roles/hosts/tasks/streams/ports/brokers/browser/volume writers. Streams/credential leases ≤30 s default; partitioned secretless permit work ≤declared 24 h default. Recheck on dispatch/reconnect/renew; no new epoch/expiry from saved guest time. Revoke cannot recall delivered bytes/committed remote actions. Read `operation.revocation_status={enforcers,complete}` during real partition and after expiry. Each entry carries `host_id,enforcer,acknowledgment,acknowledged_at,authority_expires_at,residual_capabilities`; enforcer is `supervisor|guard|broker|access|storage|plugin`, acknowledgment `pending|acknowledged|expired`. Residuals carry `provider,resource_ref,capability_kind,expires_at,revoke_state`; revoke state `pending|revoked|unsupported|unknown`. Omit unknown timestamps and all bearers. Complete means local enforcers stopped/expired, not that remote irreversible effects were undone.
**E2E.** Fixed cases `storage-fleet, limits-observations, access-console`: active SSH/event/HTTP2/WebSocket/MCP/fill/volume writer, remove role/host/key and partition enforcer, advance expiry/rejoin; verify closures and no stale writer or reauthorization.
**Code tests.** Fake network/time/provider cancel, real descendant graph/lease enforcement/fencing. Never mock actual live channel close, I/O stop or reported residual remote capability.

### Relay

#### RLY-001 — Relay reachability, identity and one-time credentials

**Statement.** Optional Eilmer/self-hosted relay must connect NAT-hidden hosts, require Eilmer account and independent fleet enrollment, and issue only short-lived one-use routing grants.
**Source.** Brief §17; ADRs 0005, 0008–0009; architecture §15.2.
**Interfaces.** `lamp access-route create --type relay --config-ref-json '{"configuration_id":"cfg_relay","digest":"<verified>"}' --json`; `POST /v1/access-routes {"type":"relay","config_ref":{"configuration_id":"cfg_relay","digest":"<verified>"}}`; `access_route_create`. `auth_flow_exchange` uses challenge/audience/proof; read AccessRoute provides host/port/fingerprint. Replayed grant fails `UNAUTHENTICATED`/`SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"relay","state":"ready","ssh_host":"ssh.f123.fleet.eilmer.si","ssh_port":22418},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard local fingerprint/QR pairing→Eilmer sign-in/link→route ready. T3 Code/Herdr/SSH enroll caller separately and use returned route; a relay login alone cannot attach sessions.
**Pass/fail.** Grant TTL ≤60 s, bound account/fleet/endpoint/caller proof/nonce and consumed once. 0 manual port forwards. Stock browser/SSH route inside host-authenticated TLS/SSH; raw SNI/port routing grants no app rights. Relay/account outage never waives host auth.
**E2E.** Fixed cases `access-console`: two fleets, malicious first enrollment/grant/foreign host/concurrent redemption, carrier loss/HTTP2 fallback and ordinary SSH assigned ports.
**Code tests.** Fake Clerk/carrier loss, real proof/signature/nonce consumption and fleet-root pin. Never mock actual NAT route or independent enrollment trust.

#### RLY-002 — Relay never decrypts or holds sandbox/model data

**Statement.** The relay must forward ciphertext only. Caller-to-host TLS/SSH keys, model credentials, files and transcripts must never be held by relay or Lamp-run credential services.
**Source.** Brief §17; ADRs 0004, 0009; architecture §§15.2–15.3.
**Interfaces.** `lamp access-route get ar_route --json`; `GET /v1/access-routes/ar_route`; `access_route_get` returns `trust_fingerprint`, not inner keys. `sandbox_file_transfer`/session streams through that route retain canonical payloads inside encryption. Substitute endpoint fails `UNAUTHENTICATED`/TLS trust before effect.
**JSON observation.** `{"data":{"id":"ar_route","kind":"access_route","type":"relay","trust_fingerprint":"<independently-pinned>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows independently pinned fleet identity and trusted issuance boundary. T3 Code/Herdr/SSH reject changed root/host CA; ordinary browser relies on trusted account code/domain/CA as disclosed.
**Pass/fail.** Zero recoverable plaintext canaries in relay RAM/disk/logs/captures beyond bounded ciphertext buffers; no payload disk spool. Relay grant cannot enroll caller, authorize action or request certificate. Separate issuer accepts fleet-signed exact-name CSR only; private key remains host-side. Host/account-code/domain/CA compromise is outside relay-process secrecy claim.
**E2E.** Fixed cases `access-console`: capture/modify/replay streams, swap first/known endpoint, forge CSR/caller grant and attempt browser-code substitution; host auth/pins and issuer deny. Trusted code must be host-served, never relay-injected.
**Code tests.** Fake relay outages/issuer transport, retain real TLS1.3/SSH verification and CSR signatures. Never mock ciphertext/endpoint authentication or scan only a friendly relay.

### CI and triggers

#### CI-001 — Shipped CI plugins and extensible coverage

**Statement.** Lamp must ship native adapters for every named CI family and let others add plugins. Native result/cancel/runner lifecycle compatibility must be demonstrated per provider/mode.
**Source.** Brief §18; ADRs 0001, 0003, 0008; architecture §16.
**Interfaces.** `lamp job create --plugin-id pg_gitlab --external-id fixture-job --recipe-id rc_ci --json`; `POST /v1/jobs` same fields; `job_create`. `job_get` exposes `queued|preparing|running|collecting|completed|failed|canceled|cleanup_failed`. Duplicate external claim returns original operation, not second runner.
**JSON observation.** `{"data":{"id":"jb_01","kind":"job","state":"running","sandbox_id":"sb_01","task_id":"tk_01"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard CI plugin→controller registration/queue→job→logs/results/artifacts→deregister/reconcile. T3 Code/Herdr/SSH watch actual job/action sessions and final result.
**Pass/fail.** All 10 plugin IDs in the CI matrix qualify, including separate Forgejo/Gitea. Each pipeline tests success/failure/cancel/controller loss/orphan cleanup, real native result and artifact download; 0 second assignments to one-job execution. Merely calling Lamp API from a CI does not certify native integration.
**E2E.** Fixed ci-native/ci-trust cases on LX for all ten CI plugins and their enumerated modes/actions; GitHub ordinary Actions additionally on native macOS guest on M5. Perform real native result, failure/cancel, lost controller, artifacts and orphan cleanup. Run Docker/service positives only on the enumerated LX GitHub/GitLab/Gitea cases; no provider/native-OS Cartesian product.
**Code tests.** Fake provider demand/control errors, retain signed intake/external-ID claim and lifecycle state machine. Never mock actual native runner/result/cancel/deregistration in provider certification.

#### CI-002 — GitHub Actions and coding-agent routes

**Statement.** GitHub Actions must support ephemeral one-job runners plus documented Copilot cloud agent, Claude Code Action and Codex Action routes under approved auth.
**Source.** Brief §18; ADRs 0003–0004; architecture §16.
**Interfaces.** `lamp plugin get pg_github --json`; `GET /v1/plugins/pg_github`; `plugin_get` shows pinned `github_actions` adapter. `job_create` maps verified demand to recipe/Task; invalid route/auth is `AUTH_TRANSPORT_UNSUPPORTED`.
**JSON observation.** `{"data":{"id":"jb_01","kind":"job","plugin_id":"pg_github","state":"completed","artifact_ids":["ar_01"]},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard GitHub demand→JIT runner→agent/action session→result→cleanup. T3 Code/Herdr/SSH show useful action output and native sub-agents, without an invented vendor-cloud placement promise.
**Pass/fail.** Qualify 4 rows: ordinary Actions, Copilot, official Claude Code Action, official Codex Action. Go scale-set listener handles `TotalAssignedJobs`, redelivered demand, cancel-before-acquire and listener loss. Copilot plan/OS/setup workflow is pinned; guest firewall changes cannot disable Lamp host guard.
**E2E.** Fixed cases `ci-native, ci-trust`: run tests and each official agent route, cancel and lose listener; inspect actual GitHub results/logs/artifacts and zero leftover registration.
**Code tests.** Fake demand/vendor timing, not claim dedup or scope enforcement. Never mock actual agent inference/useful tools/native result as compatibility evidence.

#### CI-003 — GitHub OIDC exchange without stored Lamp secrets

**Statement.** GitHub workload identity must exchange for a short-lived scoped Lamp key without stored Lamp secrets. Trust must derive from immutable forge evidence and exact challenge-bound audience.
**Source.** Brief §17; ADRs 0001, 0005; architecture §15.1.
**Interfaces.** `lamp auth-flow exchange af_01 --input ./auth-exchange.json --json` (caller-local mode-0600 JSON contains the body below); `POST /v1/auth-flows/af_01:exchange {"jwt":"<job-JWT>","audience":"lamp:fl_01:<challenge>","proof":"<proof>"}`; `auth_flow_exchange` adds `auth_flow_id`. Result secret is transient once; wrong/replayed identity is `UNAUTHENTICATED`/`SCOPE_DENIED`. Begin from a clean job: generate an ephemeral Ed25519 proof key, then `lamp auth-flow bootstrap --input ./bootstrap.json --json`; `POST /v1/auth-flows:bootstrap`; `auth_flow_bootstrap` accepts `fleet_id,purpose=ci_exchange,proof_key_thumbprint,source_identity`. Return `{auth_flow_id,challenge,audience,expires_at,request_id}` through authenticated server TLS; no Lamp key, precreated flow, mutation key or Operation. Obtain the forge JWT for the returned audience, then exchange using the returned flow ID. `proof={public_key,signature}` is base64 Ed25519 over canonical `auth_flow_id,challenge,jwt_sha256,audience`; its public-key SHA-256 matches bootstrap. The durable exchange uses common idempotency authenticated by this proof.
**JSON observation.** `{"error":{"code":"UNAUTHENTICATED","message":"Workload identity rejected","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard job identity→repository/workflow/ref/SHA/policy→issued key scope/expiry. All consoles see Task owner and CI source, never exchange token. Public-PR job does not receive this Lamp key [owner-confirmed B2].
**Pass/fail.** Key TTL ≤300 s. Validate issuer/JWKS/algorithm/time/audience/challenge/token fingerprint and immutable repository/owner IDs/workflow/ref/SHA/event/environment/protection evidence. Zero assumed caller nonce claim. OIDC-less public PR uses trusted controller intake, never owner key. Challenge TTL 60 s; bootstrap request ≤4 KiB, 10 requests/minute/source with burst 10, 100 pending flows/fleet and 64 preauth connections/source. Expire/consume bounded ephemeral state; challenge grants no identity/reservation/execution. Source hints `issuer,repository_id,repository_owner_id,workflow_ref,workflow_sha,ref,sha,event,environment` become authoritative only after forge checks; omit provider-absent claims. Test full bootstrap→JWT→exchange without preinstalled Lamp credentials/manual flow ID, flood/replay/wrong proof key/purpose; provider_login still needs authenticated create.
**E2E.** Fixed cases `access-console, keyless-and-recovery`: successful exchange, missing OIDC intake, forged workflow/source/issuer/audience, rotation/replay/expiry. Keep exchanged Lamp authority host-side for public PR [owner-confirmed B2].
**Code tests.** Fake JWKS network/time; real signed JWT fixtures/key rotation and forge-evidence match. Never mock crypto or actual CI job identity in positive proof.

#### CI-004 — A fresh sandbox for every job

**Statement.** Each CI job must get a new private writable root and no prior task processes/state. Lasting caches/artifacts must use explicit storage resources.
**Source.** Brief §§9–10, 18; ADRs 0001, 0004; architecture §§6.2, 16.
**Interfaces.** `lamp job get jb_01 --json`; `GET /v1/jobs/jb_01`; `job_get` exposes distinct `sandbox_id,task_id,artifact_ids`. `sandbox_get` shows new generation/root provenance; requested unsafe reuse fails `SCOPE_DENIED`.
**JSON observation.** `{"data":{"id":"jb_02","kind":"job","sandbox_id":"sb_02","task_id":"tk_02","state":"running"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard per-job sandbox/cache/output shows new execution and later deletion. T3 Code/Herdr/SSH never attach job 2 to job 1's live process or private root.
**Pass/fail.** In 2 consecutive jobs, 0 private markers/daemons/startup hooks survive; immutable clean template may be reused. Task-used warm entries never return to pool. All native one-job runners deregister before replacement reuse. Every forge-verified `repository_visibility=public` job, including trusted branches, requires a fresh real VM and allowlist. Unknown visibility uses the public ceiling. Stranger-only credential/cache/retention restrictions remain separate. A trusted public job on a nonvirtualizing host is denied even with owner Exposure.
**E2E.** Fixed cases `ci-native, ci-trust`: malicious first job installs startup daemon/file and leaves process; finish/cancel, start second with shared approved cache and verify only permitted cache entries survive.
**Code tests.** Fake controller completion, real root/generation allocation and teardown. Never mock real next-job filesystem/process inventory.

#### CI-005 — Stranger public-PR isolation

**Statement.** Verified public-PR work must use a fresh real VM, clean template, allowlist-only network and no owner/model/vault credentials. Only its CI system's own short-lived one-job authority may be job-readable: for example, a GitHub runner job token with its read-only repository token. No other secrets [owner-confirmed B2].
**Source.** Brief §§6, 11, 18, O3; ADRs 0002, 0004; architecture §16.2, modified B2 default.
**Interfaces.** `lamp job get jb_pr --json`; `GET /v1/jobs/jb_pr`; `job_get` reads `{"trust_class":"public_pr","sandbox_id":"sb_pr"}`. Protected attachment/fallback/exposure attempts fail `VM_REQUIRED`/`SCOPE_DENIED`; no override. No fictitious `secrets=false` badge hides CI token [owner-confirmed B2].
**JSON observation.** `{"data":{"id":"jb_pr","kind":"job","trust_class":"public_pr","sandbox_id":"sb_pr","state":"running"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows forge-verified trust, VM freshness, exact allowed token audience/actions/resource/expiry and denied attachments. T3 Code/Herdr/SSH show restricted job; any allowed CI token is disclosed as an exposure class [owner-confirmed B2].
**Pass/fail.** Zero owner settings/mounts/protected Connections/trusted writable caches, zero private/metadata/fleet-control/arbitrary upload access. Allowed CI token cannot mint credentials, control another job or submit trusted-job results; expires no later than its job. No Lamp key, model login or owner-supplied checkout secret in guest [owner-confirmed B2]. Read the forge-verified `repository_visibility` and redacted `job_token={issuer,audience,actions,resource_ids,expires_at}`; never token bytes. Assert trusted-public fresh-VM/allowlist rules separately from stranger-only secret and deletion rules.
**E2E.** Fixed ci-trust Linux provider rows on LX plus fallback denials on NV and hypervisor-denied M5/AIR. Real stranger PR with/without OIDC, forged trust/workflow, privileged pull_request_target trap, token theft/replay, cross-job control/results, cache poisoning and next-job persistence must yield zero forbidden effects. Separately deny a trusted-public job on fallback despite owner Exposure. B2 one-job pipeline completes useful work.
**Code tests.** Fake forge transport, real immutable trust classification/token scope/controller separation. Never mock real PR event, runtime boundary, token attack or output/teardown.

#### CI-006 — Triggers and agent schedules

**Statement.** Slack, Linear, webhook and schedule triggers must launch scoped tasks after verified source identity, durable deduplication and current-authority checks. Timers must have explicit daylight saving time (DST)/missed/overlap rules.
**Source.** Brief §§18–19; ADRs 0001, 0005; architecture §16.1.
**Interfaces.** `lamp schedule create --principal-id pr_01 --recipe-id rc_01 --cron '0 9 * * *' --timezone America/Chicago --json`; `POST /v1/schedules` same fields; `schedule_create`. `trigger_deliver` carries `source_event_id,payload_blob_id,signature_metadata`; duplicate returns original Operation. New claim without quorum is `QUORUM_REQUIRED`.
**JSON observation.** `{"data":{"id":"sk_01","kind":"schedule","timezone":"America/Chicago","enabled":true},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard source/auth/recipe/recipient→test→enable→next occurrence/run history→pause/resume/cancel. T3 Code/Herdr/SSH see resulting sessions with source identity and shared Task budget.
**Pass/fail.** Ambiguous DST hour runs earlier occurrence once; nonexistent local time skipped. Missed default skip plus notification; optional catch-up ≤1 coalesced run. Overlap forbidden by default. Exactly 1 quorum claim per schedule/revision/UTC occurrence; invalid/replayed source or changed authority produces 0 new effects.
**E2E.** Fixed cases `triggers`: signed Slack/Linear/webhook event, retry/replay/malicious task text, fake-clock component DST then real failover/host sleep/missed timer, pause racing claim; verify native acknowledgment and single job.
**Code tests.** Fake source/time, real signature, immutable issuer/subject mapping, cron/DST and durable claim. Never mock real task launch/dedup journal or source protocol certification.

### Dashboard and operations

#### OPS-001 — Live dashboard of every sandbox, session and resource

**Statement.** The host-served dashboard must provide complete authorized create/use/change/remove/recover flows for all resources, through common operations and scopes.
**Source.** Brief §19; ADRs 0001–0002, 0005; architecture §§12, 17.4.
**Interfaces.** `lamp metric list --json`; `GET /v1/metrics`; `metric_list({})` exposes redacted local metrics; resource create/read/update/delete (CRUD)/actions use canonical dictionary. `operation_get` carries progress/errors/outcomes/revocation_status. Adapter and Configuration discovery/setup, NativeExecutionGrant admission, fleet transfer/recovery and prepare-fork all have the same authorized dashboard flows. Stale visual mutation returns `REVISION_CONFLICT` with no overwrite.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"preparing","phase":"network","progress":{"completed":3,"total":10,"unit":"checks"}},"request_id":"rq_01"}`
**Visual/consoles.** New sandbox→recipe edit→host/device/resources→effective policy→Create→checks→ready/session. Details allow fork/suspend/move/share/revoke; Connections/kits allow auth/test/attach/approval/takeover; Storage/jobs allow downloads/backup/fenced recovery; Access allows roles/keys/ports/revoke; Operations allows cancel/budget stop/notification retry. T3 Code/Herdr show same Session state; SSH links to graphical-only actions.
**Pass/fail.** 100% of resource families in lifecycle matrix have reachable permitted flow, validation, progress, success, stale/empty/error and recovery states. No premature ready or lost Operation. LAN status should be p95 ≤1 s [proposed]. No Node server required.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: execute each flow with owner/member/viewer, fail setup, stale edit, empty list and offline observation; compare real effects/IDs with API.
**Code tests.** Fake API faults/time in UI component tests; retain real permission gating/result/error schema. Never mock browser-to-running-API paths or final resource effects in E2E.

#### OPS-002 — Task budgets and watchdog

**Statement.** Tasks must share descendant time/token budgets, exact reservations on certified proxy routes and visible best-effort usage on opaque/outside routes. Hard time stop must stop all local descendants and authority.
**Source.** Brief §19, A4; ADR 0004; architecture §17.2.
**Interfaces.** `lamp budget create --wall-time-ms 3600000 --token-limit 1000 --token-mode exact --json`; `POST /v1/budgets` same fields; `budget_create`. `task_stop({"task_id":"tk_01","reason":"budget"})` returns Operation. Exact opaque request fails `TOKEN_CAP_UNENFORCEABLE` before work.
**JSON observation.** `{"data":{"id":"bu_01","kind":"budget","token_limit":1000,"token_mode":"exact","reserved_tokens":200,"used_tokens":300,"wall_time_ms":3600000},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard route/accounting boundary→limit/used/reserved/unknown→warning/stop/decision. T3 Code/Herdr/SSH show same root Task, reason and no reset on spawn/fork/move.
**Pass/fail.** Default hard wall ceiling 3600000 ms, overridable within scope; stop ≤5 s after deadline. Exact reserve input/context/tools plus max output/reasoning before dispatch; unknown requests retain worst-case reservation; used+reserved never exceeds limit. Opaque subscription/outside usage best effort plus hard time cap. Warn at 300 s no progress; block at 10 equivalent actions or 600 s. Approval pauses loop detection only. Each root Task/Budget has one immutable durable `token_home_host_id`; every broker reserves there before dispatch. Allocation permits never duplicate its remaining token allowance. No home and no existing valid unused reservation returns `TOKEN_HOME_UNAVAILABLE`. Unknown delivery retains full charge. Partition two/three hosts while simultaneous 80-token requests hit a 100-token budget: at most one dispatch. Fork/teleport preserve home/charges; home transfer requires quorum, complete ledger and old-home fencing. Expired Task continuation follows LIF-001, not a reset of old usage.
**E2E.** Fixed cases `storage-fleet, continuation-authority`: concurrent 1000-token task requests, streaming/interrupted/retried calls, fork/sub-agent/teleport and outside mode, sleep/deadline, stuck fixture versus progressing build; verify guard/broker/process stop and residual remote outcomes.
**Code tests.** Fake provider usage/time, real certified tokenizer/canonical max bounds/reservation ledger. Never mock actual stopped descendants, route closure or authoritative vendor usage in exact-route certification.

#### OPS-003 — Notifications for done, stuck and decisions

**Statement.** Done, stuck and needs-decision events must reach configured notification sinks with inspectable retries, acknowledgment and private authenticated action links.
**Source.** Brief §19; ADRs 0004–0005; architecture §17.4.
**Interfaces.** `lamp notification-sink create --plugin-id pg_notify --destination-ref fixture_phone --event-types-json '["task.done","task.stuck","task.needs_decision"]' --enabled true --json`; API `/v1/notification-sinks` same fields; `notification_sink_create`. `notification_retry` and `notification_acknowledge` expose state; failed delivery emits `notification.delivery_failed`.
**JSON observation.** `{"data":{"id":"nt_01","kind":"notification","state":"failed","attempt_count":6},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard sinks→test→event→attempts/retry/ack. T3 Code/Herdr trusted notification opens matching task/approval; SSH displays private link without bearer in task output.
**Pass/fail.** Test all 3 event types. Retry delays exactly 1,5,30,300,1800 s then failed; manual retry remains authorized/deduplicated. Enqueue should be ≤1 s and first attempt ≤5 s [proposed]; provider-offline latency is reported. Zero ungranted destination or secret-bearing payload.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: actual test webhook/console/phone path, force sink offline/recovery, generate deterministic done/stuck/decision, approve/deny through authenticated view; compare source event and Notification IDs.
**Code tests.** Fake sink outage/time; retain actual recipient scope/redaction and retry state. Never mock actual delivery/phone-console auth in sink qualification.

#### OPS-004 — Per-sandbox process, connection and request audit

**Statement.** Audit must record every process start with program, arguments, user, parent and time [owner-confirmed B4], every network connection with destination/bytes/timing, and full redacted request semantics where Lamp handles plaintext. Subscription TLS must stay opaque.
**Source.** Brief §19, A4; ADRs 0002, 0004; architecture §§11–12, 17.1; B4 default.
**Interfaces.** `lamp audit-record list --json`; `GET /v1/audit-records`; `audit_record_list({})` returns attributable items; `audit_record_export` uses a scoped filter. Canonical `process_start` records every process creation, including fork/clone without exec; `process_exec` adds executable transitions. Both use `process_id,parent_process_id,process_instance_id,parent_process_instance_id,executable,executable_digest,argv,user,runtime,exit_code` as applicable. Collector failure yields `AUDIT_UNAVAILABLE` and `audit.gap`.
**JSON observation.** `{"data":{"id":"au_01","kind":"audit_record","type":"process_start","visibility":"process","sandbox_id":"sb_01","generation":1,"started_at":"2026-11-08T19:00:00Z","details":{"process_id":42,"parent_process_id":1,"process_instance_id":{"boot_id":"boot_01","pid":42,"start_sequence":2},"parent_process_instance_id":{"boot_id":"boot_01","pid":1,"start_sequence":1},"executable":"/usr/bin/python3","executable_digest":"<actual>","argv":["python3","probe.py"],"user":"1000","runtime":{"kind":"native"}}},"request_id":"rq_01"}` Historical start time is illustrative.
**Visual/consoles.** Dashboard sandbox/task audit→process tree/flow/request/control→redacted detail/export/gap. T3 Code/Herdr/SSH correlate command/output to record and visibility; opaque flow is never labeled full HTTP request.
**Pass/fail.** Zero missing process starts against independent ledger, including fork/clone without exec, direct syscalls, nested runtime and native guests [owner-confirmed B4]. Script builtins/in-process semantics need no invented process record [owner-confirmed B4]. Every external allowed/denied flow is attributed; multiplexed mediated requests each have record. Missing collector/spool stops effects ≤5 s; default retention 30 days. Secrets redacted before durable serialization; signed hash-chain roots protect acknowledged history. PIDs are uint64; instance identity is `{boot_id,pid,start_sequence}` to survive process identifier (PID) reuse. `executable` is actual resolved program, never argv[0]; `user` is effective UID string. `runtime={kind:native|nested_docker,runtime_id,container_id}` omits container fields when inapplicable. Redact sensitive argv/path values before persistence with `details.redacted_fields`, preserving required attribution. Exported-record tests independently assert start, exec, user change, parent, time, deceptive argv[0], rapid PID reuse and nested runtime; threads are not extra process starts. B4 requires no script semantic interpretation.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: process tree with argv/user/parent/time, fork-without-exec, shell builtins/interpreter/direct exec [owner-confirmed B4]; Docker variants run only on fixed docker positives, with native/fallback denial cases separately; TLS multiplexing/raw sockets, redacted HTTP/MCP request, collector tamper/full spool and log controls. Compare independent observer and real durable exported bytes.
**Code tests.** Fake storage-full/time/vendor responses, real collector attribution/sequence, redaction across chunks and signature/hash chain. Never mock actual process starts, external flows or authoritative persisted audit. Host-root future fabrication remains outside guarantee.

#### OPS-005 — Hosted domains, cookie/origin isolation and certificates

**Statement.** All Eilmer-hosted surfaces must stay under eilmer.si with protected account/product cookies, isolated untrusted origins and shared-domain certificate limits. Every Eilmer-hosted page stays under eilmer.si. An untrusted sandbox page must never obtain or replace an account login. Each fleet uses one wildcard certificate, so creating sandbox web addresses does not consume a certificate each time.
**Source.** Brief §20; ADRs 0005–0006, 0009; architecture §15.3.
**Interfaces.** `lamp port get po_01 --json`; `GET /v1/ports/po_01`; `port_get` reads `origin=https://p-<random>.f123.fleet.eilmer.si`. AccessRoute exposes `api.f123.fleet.eilmer.si`; certificate exhaustion leaves preparing/failed, never insecure ready.
**JSON observation.** `{"data":{"id":"po_01","kind":"port","origin":"https://p-random.f123.fleet.eilmer.si","access":"private","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard shows hosted activation/issuer queue and unique content origin. T3 Code/Herdr/SSH use same origin/link and independent trust fingerprints; no alternate hosted domain offered.
**Pass/fail.** Origins: eilmer.si, docs.eilmer.si, account.eilmer.si, clerk.account.eilmer.si, relay.eilmer.si and one-label services under fleet subtree. Fleet `__Host-lamp_session` and port `__Host-lamp_port` are Secure/HttpOnly/Path=/ with no Domain, never forwarded to apps. Actual Clerk cookies stay account subtree. Exact Origin/cross-site request forgery (CSRF)/Cross-Origin Resource Sharing (CORS)/Host/SNI/PKCE/frame/postMessage defenses; no state-changing GET. Lamp ≤40 new certificates/rolling 7 days with 10 reserved within shared 50; 1 wildcard/fleet, 0 certs per 1,000 new content names. Central CAA binds issue and issuewild to the authorized ACME accounturi and dns-01; deny permissive overrides and hostile CNAME/delegation. Test attacker-account exact API/content-name issuance via HTTP-01/TLS-ALPN-01 plus wildcard/DNS-01 attempts, not only forged issuer RPCs. Independent Certificate Transparency monitoring detects/quarantines unexpected issuance; detection does not prevent it, and domain/CA trust remains explicit.
**E2E.** Fixed cases `access-console`: hostile sibling cookies/JS tossing/CORS/WebSocket/CSRF/frame/service-worker attacks against real Clerk/product sessions; staging ACME ledger/exhaustion/renewal, malicious cross-fleet CSR and 1,000 origins. No owner's project content is tested; shared deployment policy uses isolated fixtures.
**Code tests.** Fake ACME/DNS/time, real origin/cookie parsing/CSR/ledger policy. Never mock browser behavior, actual emitted auth cookies or representative TLS chain. Independent issuer and capacity approval precede unrestricted onboarding.

#### OPS-006 — No payments yet; future business support

**Statement.** Lamp must require no payment system and retain extensible plans/quotas/billing boundaries through the usage/admission ledger.
**Source.** Brief §20; ADRs 0001, 0008; architecture §17.2.
**Interfaces.** `lamp fleet get fl_01 --json`; `GET /v1/fleets/fl_01`; `fleet_get` exposes `limits`, not a payment prerequisite. `budget_get` exposes usage and the canonical token home. Exceeding quota yields `NO_CAPACITY`/`CONTROL_QUOTA_EXCEEDED`, never payment prompt.
**JSON observation.** `{"data":{"id":"fl_01","kind":"fleet","limits":{"concurrency":4}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard local quota/budget works without billing signup; all consoles create permitted sessions offline without commercial account dependency.
**Pass/fail.** 0 payment calls/secrets/accounts required to install or use local product. Usage records remain attributable to principal/task/resource for future accounting; no first-release billing UI/API is invented.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: block public billing endpoints, install/use local quota/task flows and inspect egress; exceeding configured quota fails honestly.
**Code tests.** Fake future ledger consumer, real usage/admission transaction. Never mock offline useful work or actual quota enforcement.

#### OPS-007 — Licenses and private-until-demo release condition

**Statement.** Lamp host code/CLI/SDKs/first-party plugins must be Apache-2.0; relay AGPL-3.0. Repository visibility must stay private until first engine demo works. Dependencies/guest software must keep their own licenses.
**Source.** Brief §§20, 22; ADRs 0003, 0007–0008; architecture §§4, 21.
**Interfaces.** `lamp plugin get pg_01 --json`; `GET /v1/plugins/pg_01`; `plugin_get` points to pinned `digest,publisher,version`; release software bill of materials (SBOM)/notices accompany artifacts. Invalid provenance fails `INVALID_ARGUMENT`/`ENGINE_UNQUALIFIED` before enable.
**JSON observation.** `{"data":{"id":"pg_01","kind":"plugin","publisher":"<verified>","version":"<pinned>","digest":"<actual>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard About/plugin install shows notices and trust evidence. T3 Code/Herdr/SSH use same licensed host packages; guest Docker/native media is separately identified.
**Pass/fail.** 100% of shipped artifacts have source/license/SBOM/provenance. Zero proprietary macOS image redistribution. No relabeling GPL/Linux/Docker dependencies Apache. Repository public switch follows actual M0 demo evidence; it is not full release certification.
**E2E.** Fixed cases `host-linux, host-macos, storage-fleet`: inspect install artifact/notices/provenance and run engine demo gate; verify release automation refuses public switch before recorded success.
**Code tests.** Fake release registry, real artifact digest/signature and license manifest checks. Never mock demo execution or repository visibility evidence as already passed.

#### OPS-008 — Opt-in usage data only

**Statement.** Usage analytics must be off by default, send only numeric counts/timings after explicit consent and expire after 90 days. Opt-out must stop sends and purge unsent batches.
**Source.** Brief §20, O7; ADRs 0004, 0009; architecture §17.4.
**Interfaces.** `lamp telemetry update --enabled true --json`; `PATCH /v1/telemetry {"enabled":true}`; `telemetry_update` same body. `telemetry_get` reads `enabled,consent_revision,retention_days=90`; `telemetry_purge({})` removes retained batches. Stale consent is `REVISION_CONFLICT`.
**JSON observation.** `{"data":{"kind":"telemetry","enabled":false,"consent_revision":2,"retention_days":90},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard privacy→exact payload preview→opt in→opt out/purge→confirmed revision. T3 Code/Herdr/SSH show consent setting/link, never auto-enable from console use.
**Pass/fail.** Default/upgrade/crash/reconnect emits 0 optional usage events. Allowed schema only counts create/fork/suspend/resume/failed_operation and timing histograms create/fork/resume; zero versions/IDs/names/paths/URLs/prompts/files/commands/dumps. Expire all opted-in batches/backups at 90 days. Relay minimal security records separate: 7 days; active links until removal. The exact payload is `{counts:{create,fork,suspend,resume,failed_operation},timings_ms:{create,fork,resume}}`, fixed numeric keys and bounded histogram buckets. Reject OS/engine version fields and arbitrary numeric labels. Accepted O7 overrides brief §20's stale version examples; correcting that brief sentence is outside these two authorized files, not an open privacy decision.
**E2E.** Fixed cases `telemetry-expiry`: capture actual egress before consent, enable/use, inspect payload, race opt-out/send/stale edit, restart and test 90-day expiry with controlled time in persistence lane.
**Code tests.** Fake collector/time, real schema allowlist/consent transaction/expiry queue. Never mock actual outbound payload classification or persisted purge.

#### OPS-009 — Bounded admission and retained-state accounting [added]

**Statement.** Every active and retained resource must be quota-charged, with bounded control/bulk queues and safe cleanup. Exhaustion must not evict retry/audit safety or break unrelated admitted work.
**Source.** Brief §§5–7, 9–10, 12, 17, 19; ADRs 0001–0003; architecture §§3, 5, 12, 18.
**Interfaces.** `lamp fleet update fl_01 --limits-json '{"concurrency":4,"retained_bytes":10737418240,"control_records":100000}' --json`; `PATCH /v1/fleets/fl_01` with `limits`; `fleet_update` adds `fleet_id`. Over-limit returns `CONTROL_QUOTA_EXCEEDED`, `RATE_LIMITED` or `NO_CAPACITY`, with Retry-After where appropriate.
**JSON observation.** `{"error":{"code":"CONTROL_QUOTA_EXCEEDED","message":"Durable control capacity exhausted","retryable":true,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard distinguishes reserved/used pool/RAM backing/captures/volumes/artifacts/audit/control records and cleanup failures. T3 Code/Herdr/SSH show output-spool stop and resource reason without losing acknowledged output.
**Pass/fail.** Reserve tombstone/audit space before effects; count shared backing once plus private-growth liability, release exactly once. Stream queue ≤1 MiB encoded; stream payload ≤64 KiB decoded, JSON/MCP/NDJSON record ≤96 KiB encoded; bulk chunk ≤1 MiB decoded and ≤1408 KiB encoded. Metadata ≤4 KiB; REST binary framing adds ≤4 KiB. Control/plugin messages stay ≤1 MiB encoded. Bulk has its own charged one-chunk buffer. Under one tenant's flood, another admitted tenant completes 100/100 probe operations with 0 stolen reservations; no unbounded unauthenticated relay buffers. At defaults admit 32 concurrent mutations/principal, reject the 33rd with `RATE_LIMITED` and no effect; allow 64 preauth relay connections/source and reject the 65th. Check exact maximum, one byte over and worst-case metadata with independent serialization; split decoded payloads before base64 encoding. End SQLite reads before network responses, bound transactions to 100 ms, cancel overdue readers and force checkpoint gaps. Reserve 64 MiB WAL high-water plus 64 MiB checkpoint headroom and a physically preallocated supervisor-owned 64 MiB emergency journal/inodes. Include database/temp/export/migration amplification. External byte/inode exhaustion and overlapping readers mid-commit fence effects before journaling outcomes; if reserve fails, remain fenced/unknown. Never evict retry/audit safety history.
**E2E.** Fixed cases `limits-observations, secret-result-transport`: flood creates/forks/events/PTY/files/inodes/artifacts/plugin RPC/relay connections, slow readers, canceled failed creates and full audit disk; compare real memory/disk/capacity and unrelated probe results.
**Code tests.** Fake capacity/time/flood source, real admission/references/buffers/reservation cleanup. Never mock actual process/resource use or durable safety-record retention.

### Benchmarks and proof

#### BEN-001 — Linux create target

**Statement.** Standard Linux create from a prepared template must have caller-send-to-real-command p50 <500 ms with required setup/security enabled [owner-confirmed B1]. Cold/warm/setup cases must be separately labeled.
**Source.** Brief §7; ADRs 0003, 0007; architecture §§6.2, 19; B1 default.
**Interfaces.** `lamp sandbox create --template-id tp_bench --resources-json '{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}' --json`; API `/v1/sandboxes` same fields; `sandbox_create`. Read `readiness.command_started_at`; failed create is failed trial, never discarded [owner-confirmed B1].
**JSON observation.** `{"data":{"id":"sb_bench","kind":"sandbox","state":"ready","resources":{"vcpus":2,"memory_mib":4096,"disk_gib":20,"pids_max":4096}},"request_id":"rq_01"}` [owner-confirmed B1]
**Visual/consoles.** Dashboard readiness/latency view shows phase and endpoint; T3 Code/Herdr/SSH witness actual first useful command, not boot badge.
**Pass/fail.** Native LX: 20 warmups then ≥200 measured creates/case, p50 strictly <500 ms; 500 ms fails [owner-confirmed B1]. All failures/timeouts remain raw data. Larger sizes and Mac runs are measured/published without hard threshold pending later targets [owner-confirmed B1]. LA is nested functional/timing evidence only; native arm64 speed targets are certified only by an outside open-benchmark run on native hardware. No paid or external project lane is required. Mac-host Linux has published measurements pending its own targets.
**E2E.** Fixed cases `host-linux`: frozen benchmark standard, independent command marker, same caller clock, normal guard/audit/CoW/fork tracking; test each access route without subtracting RTT [owner-confirmed B1].
**Code tests.** Fake timing traces/error responses, real statistics and endpoint association. Never mock actual elapsed product time or command execution in benchmark evidence.

#### BEN-002 — Linux live-fork target

**Statement.** Every measured standard Linux memory-fork trial must finish in <1000 ms including consistent capture, source continuation, identity barrier and usable fork [owner-confirmed B1].
**Source.** Brief §§6–7; ADRs 0003–0004; architecture §§6.4, 19; B1 default.
**Interfaces.** `lamp sandbox fork sb_bench --fork-mode memory --durability local --json`; `POST /v1/sandboxes/sb_bench:fork {"fork_mode":"memory","durability":"local"}`; `sandbox_fork` adds `sandbox_id`. Snapshot `preserves_memory=true`; refusal/timeout is a failed feature trial.
**JSON observation.** `{"data":{"id":"sn_01","kind":"snapshot","preserves_memory":true,"durability":"local","state":"ready"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard reports source pause, full elapsed fork and durability; all consoles observe independently continuing source/new Session. Native-Mac disk-only never scores here.
**Pass/fail.** ≥100 trials per standard dirty-memory/durability row; every trial <1000 ms, equality fails. Standard resources 2 vCPU/4 GiB/20 GiB, with 0/128 MiB/1 GiB recently written memory cases [owner-confirmed B1]. Count configured RAM capture and faults honestly; larger working sets are measured/published without hard target [owner-confirmed B1]. Hard standard timing applies on native LX only in the owned matrix; LA/Mac-host runs are labeled nested or host-class measurements. Architecture §19 phase allocations are 20/35/75/70/500/100/35/65 ms, total 900 ms with 100 ms headroom. Full 4 GiB capture in 500 ms requires ≥8 GiB/s before other work; 8 GiB would need ≥16 GiB/s but has no hard timing gate. Include qualified power-loss barriers and replicated decryption receipts; no hidden asynchronous completion.
**E2E.** Fixed cases `host-linux`: generation-pattern RAM, dirty filesystem and source continuation, local/replicated/volatile rows with actual commit costs; record failures/max/pause/RSS/backing. Repeat non-Linux as measurements.
**Code tests.** Fake traces/capture fault, real boundary/statistical logic. Never mock RAM capture, durability fsync/receipts or both useful-command proofs.

#### BEN-003 — Linux native-speed floor by workload

**Statement.** Each standard Linux compute, disk and network measure must reach ≥0.97 of direct native speed with all normal security/audit/capture costs enabled [owner-confirmed B1]. Measure computation, disk and network separately against the same computer running directly. Every required result must reach 97% of that speed; a good average cannot hide one slow result. Repeat paired runs and publish uncertainty as well as measured values.
**Source.** Brief §7; ADR 0003; architecture §19; B1 default.
**Interfaces.** `lamp host bench hs_01 --suite-digest '<frozen-suite>' --json`; `POST /v1/hosts/hs_01:bench {"suite_digest":"<frozen-suite>"}`; `host_bench` adds `host_id`. Operation/evidence references record actual result; no invented benchmark-result fields in API.
**JSON observation.** `{"data":{"id":"en_01","kind":"engine","evidence_ref":"<source-keyed-run>","certification":"unqualified"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard evidence link shows per-workload raw ratios and failures; T3 Code/Herdr/SSH watch workloads and logs, never a single averaged native-speed badge.
**Pass/fail.** Work-rate ratio guest/native; latency ratio native/guest. Every mandatory measure ≥0.97; exactly 0.97 passes [owner-confirmed B1]. ≥10 randomized paired trials/measure. One-sided 95% lower confidence bound should also be ≥0.97 [proposed]; a passing point estimate with a lower confidence bound below the proposed objective is visibly provisional statistical evidence, not a release failure by itself. No averaged category or slowed native rescue.
**E2E.** Fixed cases `host-linux`: mandatory benchmark workload table below, same physical storage/peer/power/thermal/compiler/resources, first-write and sustained runs; larger rows reported separately [owner-confirmed B1].
**Code tests.** Fake known numeric datasets, real ratio/confidence/failure inclusion. Never mock actual native or secured guest performance.

#### BEN-004 — Zero protected-secret leaks everywhere

**Statement.** The security attack suite must produce zero unauthorized API-key, password or browser-cookie leaks on every required host/guest row. Subscription runtime and explicit owner Exposures must be disclosed separately.
**Source.** Brief §7, O5, A1, A4; ADR 0004 amended; architecture §§9, 18, 22.
**Interfaces.** `lamp exposure list --json`; `GET /v1/exposures`; `exposure_list({})` lists explicit exceptions, not secret values. `audit_record_list` shows denied/allowed effects and redaction. Unauthorized exception request is `SCOPE_DENIED`.
**JSON observation.** `{"items":[],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard and all consoles display exposure/subscription warnings, safety result and positive-feature result independently. Protected password uncertainty invokes human takeover [owner-confirmed B3]. Allowed CI token is separately scored, never counted as an absent secret [owner-confirmed B2].
**Pass/fail.** 0 canary bytes/decoded equivalents at unauthorized sinks across all attacks. Include guest memory/disk/output/capture/log/browser/download/relay. Scanner/sink self-test must detect deliberately released canary; otherwise result is invalid. Explicit exposure cannot waive public-PR policy. No secret bytes reach agent during fallback typing [owner-confirmed B3].
**E2E.** Run each ATK-01–44 on its explicitly enumerated compatible cases in the attack table. Keep positive useful work, hostile fixture and healthy independent scanner/sinks. Docker positives run only on docker cases; native/process fallback denials never substitute for them. Process completeness uses B4, not script semantics.
**Code tests.** Fake model suggestions/vendor failures, real crypto/filter/authorizer/enforcement and canary detection. Never mock attack denial or use a model's verbal refusal as boundary proof.

#### BEN-005 — Public reproducible benchmarks on every change

**Statement.** Every source change must queue the reproducible performance/reliability/security suite. Every PR must run it privately before release; reports must become public from first public release.
**Source.** Brief §§2, 21–22, O6; ADRs 0003, 0008; architecture §§19–24.
**Interfaces.** `lamp host bench hs_01 --suite-digest '<frozen-suite>' --json`; API `/v1/hosts/hs_01:bench` same input; `host_bench`. `engine_get` exposes `evidence_ref,measurements`. Missing lane is blocked evidence, not a zero-latency result.
**JSON observation.** `{"data":{"id":"en_01","kind":"engine","evidence_ref":"<source-keyed-run>"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/report links exact source/configuration/raw data. T3 Code/Herdr/SSH observe benchmark tasks; no invented runtime API is needed for local competitor test scripts.
**Pass/fail.** Compare all 5 required competitors: Cloudroom, E2B Embed, Docker Sandboxes, microsandbox, SmolVM. Publish create/fork/native speed/agent-ready/density/reliability/attacks and incompatibilities. Every fixed required owned-machine row needs a fresh passing source-keyed result; a missing or failed result blocks merge. Visible blocked status is not a waiver. Pending community native-arm64 speed certification does not block this owned-machine matrix. Standard-vs-larger gates use B1 [owner-confirmed B1].
**E2E.** All fixed cases: pinned competitor installs, equivalent task/state and independent observations; nested/emulated/fallback separate; private CI publish check and first-release public reproducibility package.
**Code tests.** Fake comparator transport/data, real adapter endpoint/statistics/report completeness. Never mock published Lamp/competitor measurements or claim design superiority as measured fact.

### Engineering and test infrastructure

#### ENG-001 — Engine first

**Statement.** A working Linux create and continuing-source RAM fork within targets must precede product expansion. Feasibility of storage/native guests/credential capture must be tested before freezing state ABI.
**Source.** Brief §22; ADRs 0003, 0007–0008; architecture §21.
**Interfaces.** `lamp sandbox create --template-id tp_demo --json`, API `/v1/sandboxes` with `template_id`, MCP `sandbox_create`; then canonical `sandbox_fork`. Failed gate stays failed Operation/evidence, never a roadmap waiver. Hard standard resources use B1 [owner-confirmed B1].
**JSON observation.** `{"operation_id":"op_demo","resource_ids":[{"kind":"sandbox","id":"sb_demo"}],"replayed":false,"request_id":"rq_01"}`
**Visual/consoles.** Early demo may use CLI/SSH; later dashboard/T3 Code/Herdr expose same actual engine behavior. A polished console cannot substitute for engine evidence.
**Pass/fail.** M0 proves real Linux command+RAM fork with 0 Docker dependency, then E04–E07 feasibility before ABI freeze. First release waits for all fixed required product rows; pending community native-arm64 speed certification stays outside that owned-machine gate. Repo remains private until demo succeeds; no threshold changed after a failed run [owner-confirmed B1].
**E2E.** Fixed cases `host-linux, storage-fleet`: execute frozen create/fork/oracle suite, followed by M5/AIR capture/native/storage feasibility; record release gate decisions.
**Code tests.** Fake pipeline gate metadata, never engine execution or source-keyed evidence. Check release tooling cannot promote missing/failed gate.

#### ENG-002 — Reuse only for security or likely best speed

**Statement.** Every reused component must have a recorded security necessity or credible speed reason, pinned provenance and maintenance owner. Existing availability alone must not justify adoption.
**Source.** Brief §22; ADR 0003; architecture §§4–5.
**Interfaces.** `lamp engine get en_01 --json`; `GET /v1/engines/en_01`; `engine_get` shows `digest,evidence_ref,certification`. Plugin digest/provenance and release SBOM link dependency justification; unqualified graph returns `ENGINE_UNQUALIFIED`.
**JSON observation.** `{"data":{"id":"en_01","kind":"engine","digest":"<locked-engine-graph>","certification":"unqualified"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard install/evidence shows engine/dependency version and qualification. All consoles run same pinned worker; common microsandbox/msb_krun lineage is disclosed, not called independent diversity.
**Pass/fail.** 100% of production dependencies have rationale, locked hash/features/notices/advisory/backport owner. Firecracker remains independent Linux reference. Qualify exact msb_krun 0.1.45 downstream graph/commit from architecture before adoption; upstream names alone prove nothing.
**E2E.** Fixed cases `host-linux, storage-fleet`: rebuild pinned graph, compare actual worker digest and capture/confinement evidence; withdraw vulnerable candidate and verify selection invalidation.
**Code tests.** Fake dependency index/advisory transport, real lock/hash/SBOM matching. Never mock reproduced binary graph or runtime qualification.

#### ENG-003 — Architecture-owned language boundary

**Statement.** Go must own control/authorization/operations; Rust must own engines/supervisor/guest/confinement helpers through bounded typed RPC. Bulk data must stay outside replicated metadata. Go decides who may act and which resources they may use. Rust runs and confines sandboxes. With 32 callers at once, test that coordination stays within its 35 ms allocation at the 99th percentile and never loses an action or mixes callers.
**Source.** Brief §22; ADR 0003; architecture §4.
**Interfaces.** `lamp operation get op_01 --json`; `GET /v1/operations/op_01`; `operation_get` exposes the same authoritative operation across workers. `metric_list` reports redacted phase timings. Worker failure returns failed Operation, never a second control-state decision.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"succeeded","cleanup_state":"not_needed"},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard phase view and T3 Code/Herdr/SSH show one outcome despite component failure. No language claim appears as a speed result without measurements.
**Pass/fail.** `lamp.worker/1` control frame ≤1 MiB; peer-authenticated socket/ACL pipe and typed nontransferable handles. Under 32 concurrent callers, control/admission/RPC p99 ≤35 ms engineering allocation with 0 lost/cross-owner effects or quota overspend. No cgo/shared heap in vCPU path.
**E2E.** Fixed cases `host-linux, storage-fleet`: same Rust worker through Go controller, durable semantics, 32 callers, restart each side, inspect framing/identity and integrated benchmark.
**Code tests.** Fake transport faults, real generated protocol validation/local peer credentials/handle ownership. Never mock actual end-to-end RPC allocation timing or quota effects.

#### QA-001 — Owned-machine test lanes and honest certification

**Statement.** All project-run tests, benchmarks and the development relay must use only the owner's Ubuntu laptop, M5 Mac and MacBook Air, without spending money. Required nested compatibility lanes must remain visible and must not be dropped.
**Source.** Brief §22, O2, O10; ADRs 0001, 0003; architecture §20.1.
**Interfaces.** `lamp host get hs_test --json`; `GET /v1/hosts/hs_test`; `host_get` exposes actual OS/build/architecture/capacity/engines. `host_bench` runs a pinned workload; unavailable nesting/API returns `ENGINE_UNQUALIFIED` and a blocked required case, never an external replacement. Test provisioning is infrastructure, not another Lamp API.
**JSON observation.** `{"data":{"id":"hs_test","kind":"host","os":"linux","arch":"arm64","state":"ready"},"request_id":"rq_01"}` The LA manifest additionally labels its M5-backed topology nested; this is not native-speed evidence.
**Visual/consoles.** Dashboard and consoles show actual lane, host/guest distinction, topology and blocked evidence. The dev relay runs on always-on LX; compare idle relay baseline with separately labeled contended measurements.
**Pass/fail.** Use only LX,LX22,D12,F40,L510,LA,M5,AIR,NV. Secondary distro/kernel and LA host lanes are nested. Use the owner's existing paid accounts and permitted service/device access without buying anything new; unavailable positive stays blocked. Schedule capacity-heavy cases serially. Native arm64 speed is certified only when someone publishes the open benchmark on native arm64 hardware; pending community certification is outside the required owned matrix.
**E2E.** Preflight the exact nine lanes on the three owned machines: inventory, real guest boot/capture/restore/guard, assigned useful positive and denial tests, sanitized evidence export, credential revocation and disposable-state cleanup. A failed nested feature remains failed/blocked. No paid infrastructure or extra machine is a fallback.
**Code tests.** Test immutable lane/case manifest validation, resource scheduling and disposable-fixture cleanup. Never mock actual nesting, licensed media, guest effects, measured topology or cleanup as certification.

#### QA-002 — Named machines and public third-party fixtures

**Statement.** Tests must use the named M5 Mac, Ubuntu laptop and MacBook Air only. They must use public third-party projects/harnesses/consoles/MCP servers, never owner projects or special integrations for them.
**Source.** Brief §§22–23; ADRs 0001, 0003; architecture §20.
**Interfaces.** `lamp host list --json`; `GET /v1/hosts`; `host_list({})` supplies real inventory. `blob_create`, `blob_upload` and `blob_finalize` delivers fixture inputs; `template_build` exposes progress. Wrong fixture reference fails `INVALID_ARGUMENT`/`NOT_FOUND`.
**JSON observation.** `{"items":[{"id":"hs_mac","kind":"host","os":"macos","arch":"arm64","os_version":"26"}],"next_cursor":null,"request_id":"rq_01"}`
**Visual/consoles.** Dashboard fixture tasks and T3 Code/Herdr/SSH use public contracts and real IDs. No Ozi, Apple Bridge or money-bridge data/code is opened or tested.
**Pass/fail.** Every journey manifest pins public source revision, package lock, official binary/plugin/console digests, expected bytes, initial state, host lane, returned-ID capture and cleanup. 0 mutable “latest” fixtures or owner's project dependencies. LX is Core Ultra 9 288V/8 cores/30 GB/Ubuntu 26.04; M5 is 10-core/24 GB/macOS 26; AIR is M2/8 GB/fanless/macOS 26. Inventory actual storage/builds/devices before use; older macOS is uncertified.
**E2E.** All fixed cases: run all five manifests below with disposable fixture accounts; reproduce from empty fixture storage and verify zero leaked resources.
**Code tests.** Fake model decisions for deterministic states; real fixture revision/hash validation. Never fake official compatibility or copy a result from another OS row.

#### QA-003 — Independent code-level oracles and protocol regressions [added]

**Statement.** Code tests must derive authority/persistence/capture expectations independently and include concrete security-protocol failure fixtures. Serializer agreement alone must not prove correctness.
**Source.** Brief §§7, 21–22; ADRs 0001–0004, 0009; architecture §20.2.
**Interfaces.** `lamp operation get op_01 --json`; `GET /v1/operations/op_01`; `operation_get` exposes committed/unknown effects for independent ledger comparison. `audit_record_export` provides redacted evidence. Unknown outcomes use `OUTCOME_UNKNOWN`, not reexecution.
**JSON observation.** `{"data":{"id":"op_01","kind":"operation","state":"outcome_unknown","outcome":{"result":{},"committed_effects":[],"unknown_effects":[{"resource_ref":{"kind":"exec","id":"ex_01"},"action":"exec.create","state":"outcome_unknown"}]}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard/test evidence links observed effect history and fault boundary; T3 Code/Herdr/SSH output remains real input to oracle, never canned success.
**Pass/fail.** Zero invariant violations at reserve/capture/fsync/publish/ack/delete/GC crash points. ≥1,000 storage/capture trials/backend; generation-pattern RAM+disk checksums+external fsync history must agree. All named protocol regression fixtures below run with real cryptography/authorization/filtering. Required process ledger uses B4 [owner-confirmed B4].
**E2E.** All fixed cases: actual process/storage restarts, corrupt/truncated chunks, immutable parent deletion/compaction, fork randomness/deadline and external side-effect observer; compare recovered bytes/authority.
**Code tests.** Fake only vendor/DNS/time/fault delivery outside proved boundary. Keep real canonical hash, JWT/JWKS, TLS, request framing, SigV4, persisted journal, policy and response filtering. No successful storage/crypto mock stands in for tested behavior. Deliberately remove/reorder a required flush on a volatile-write-loss/reordering backend and prove the external acknowledgement oracle catches the defect; then repeat through each real filesystem/block barrier. Orderly restart is insufficient. Include authorized-producer forgery, token-home partition, update-trust/revocation, browser background egress, numeric selected-exit dialing, suspend-aware power gates and emergency-journal exhaustion regressions from architecture round 5.

#### QA-004 — Complete capability certification and reliability [added]

**Statement.** Release evidence must cover the fixed lamp.compatibility/v1 cases below, independently of what the implementation detects or advertises. It must distinguish specified, implemented, tested and blocked. A missing or failed required positive blocks merge and release certification.
**Source.** Brief §§5–7, 13, 16, 18, 21–22; ADRs 0001, 0003–0004; architecture §§20, 22.
**Interfaces.** `lamp engine list --json`; `GET /v1/engines`; `engine_list({})` shows `certification,evidence_ref`; analogous Harness/Plugin records pin compatible rows. Unqualified request returns `ENGINE_UNQUALIFIED` or appropriate capability error, never false ready.
**JSON observation.** `{"error":{"code":"ENGINE_UNQUALIFIED","message":"Required capability evidence missing","retryable":false,"details":{}},"request_id":"rq_01"}`
**Visual/consoles.** Dashboard capability/evidence matrix and all consoles label actual class/support. `not_requested`, `unsupported`, `vendor_unavailable`, `not_run` are distinct; none automatically passes binding scope.
**Pass/fail.** 100% of required rows have source-keyed evidence. Reliability should be ≥2,995 independent zero-failure trials per create/fork/suspend/resume/delete per required engine/platform class, a one-sided 95% lower success bound ≥99.9%, plus separate 24 h loaded soak [proposed]. Safety failure always fails. No correlated soak samples counted independent. Deleting a required case must fail the frozen-ID gate rather than shrink the denominator. No Cartesian product is implied. Report success and failure separately for create, fork, suspend, resume and delete on each required platform. The proposed confidence test needs ≥2,995 independent successes with zero failures per denominator; the separate proposed 24 h busy run checks leaks, heat and time-dependent failures. Report proposed-target misses; adoption requires a dated baseline revision. Safety and owner point thresholds remain hard.
**E2E.** Run all frozen lamp.compatibility/v1 positives and contract-boundary cases, on their exact owned lane/mode pairs. Remove a required ID in a negative gate fixture and require conformance failure. Report missing hardware/vendor positives blocked; nested evidence never becomes physical/native-speed proof. Report ordinary, injected and cleanup failures separately; proposed confidence/soak misses stay visible proposals.
**Code tests.** Fake missing evidence to test gate, real manifest/schema/coverage/denominator logic. Never mock actual required hardware, official binaries or successful positive workflow.

## Journey acceptance tests

These are executable test specifications, not records of completed runs. A fixture runner implements the listed canonical calls and captures IDs; it must stop on a failed assertion. It may not synthesize IDs such as `se_try_a`. Common mutation keys/revisions and Operation waits apply to every step. Run each journey through CLI once, API once and MCP once; the same expected effects apply.

### Shared fixture manifest and preparation

Before execution, write a run manifest outside the product resource schema. It records source SHA, suite version, lane/hardware/OS, engine graph, template seed/package lock digests, exact official harness/auth/plugin/console versions, dedicated principals, expected bytes, external sink/reflector identities, preflight, cleanup list and evidence destination. Unknown binary/vendor permission/console contract means blocked, never a guessed latest version.

Public project fixture is Flask 3.1.2, tag object `80be49be88b534d2a72ef6bf5ea4aabf89f3305b`, peeled commit `2c1b30d0503cfb064f1cb252e6614a06915a362a` at `https://github.com/pallets/flask.git`. The reviser checked this reference read-only on 2026-11-08. Tests verify the commit again and freeze the archive/hash before use. No owner's repository is used. The deterministic transport fixture is a small public test harness built by the test suite: it emits known bytes, asks one fixed question, accepts one reply and reports working→blocked→idle. It does not certify vendor auth or continuation.

The fixture creates these caller-local files. All `<...>` values are required substitutions from verified inputs, not fake runnable digests. Preflight fails if any placeholder remains. Exact Debian seed/kernel/package lock and official binaries must be frozen in the run manifest before execution; this document claims none already qualified.

`tools.lamp` is a complete format example:

```toml
format = "lamp.template/v1"
name = "python-tools"
guest_os = "linux"
arch = "x86_64"
seed_ref = "lamp-seed/debian-13@sha256:<verified-seed-digest>"
package_lock_blob_id = "<returned-lock-blob-id>"
[[steps]]
argv = ["apt-get", "install", "--yes", "git", "python3"]
[[steps]]
argv = ["python3", "-c", "print('tools-installed')"]
```

Use `arch=arm64` and independently locked matching seed in LA/Mac Linux rows. Native templates use owner licensed media and corresponding qualified build plan. `AGENTS.md` fixture contents are exactly `Work only in /work. Ask before returning settings changes.\n`; this is test data, never an instruction to the document author. A separate locked harness-settings JSON, skills/plugin/slash-command fixture, MCP list and git identity file cover all settings categories. Signing contains a Connection reference, never a private key.

For every local content file, hash its exact bytes and execute this upload sequence with actual size/hash. CLI shown; API/MCP mappings are explicit below.

```sh
lamp blob create --size-bytes <actual-size> --sha256 <actual-sha256> --json
lamp blob upload <returned-blob-id> --file ./tools.lamp --json
lamp blob finalize <returned-blob-id> --size-bytes <actual-size> --sha256 <actual-sha256> --json
```

| Purpose | API | MCP |
| --- | --- | --- |
| Allocate upload | `POST /v1/blobs {"size_bytes":<actual>,"sha256":"<actual>"}` | `blob_create` with same fields; capture `blob_id` from completed outcome |
| Upload bytes | `PUT /v1/blobs/<blob_id>/content`, real Content-Range and bytes | `blob_upload({"blob_id":"<returned>","offset_bytes":0,"data_base64":"<actual-chunk>"})`, chunks ≤1 MiB |
| Verify/finalize | `POST /v1/blobs/<blob_id>:finalize {"size_bytes":<actual>,"sha256":"<actual>"}` | `blob_finalize` adds `blob_id`; assert `blob_get.data.state=ready` |

CLI receives the common Operation envelope and waits; reads show `{"data":{"kind":"blob","state":"ready","sha256":"<actual>","size_bytes":<actual>},"request_id":"<returned>"}`. A mismatch fails `INVALID_ARGUMENT` and the blob cannot build/write. Upload lock, definition, settings bundle and later file/patch payloads separately. No server reads a caller-local path.

Create dedicated Alice(owner), Bea(member), administrator and viewer Principals/Memberships using canonical create operations and target-key invitations. Select only dedicated custody hosts. Provision broker CredentialSources through approved native `auth_flow_create/complete` or owner vault interface; no example stores secret text in a resource. Create test Connections with adapter schema/endpoint/resource policy; `connection_test` must pass before use. All real vendor permissions, permitted free accounts and test credentials are preconditions, not fabricated success. Every test runner, relay, controlled service, sink and peer runs on the three owned machines; existing permitted external vendor endpoints supply service behavior, not additional test infrastructure. Phone approval views are exercised in a browser on those machines; no fourth device is required.

### Test journey 1 — One Linux host to signed-in useful development

**Lanes and start.** LX clean fixture account; repeat assigned functional cases on nested LA and Linux guests on M5/AIR. Only native LX carries owned B1 timing gates. Covers PRD, INT, TPL, CRED, CON, KIT and OPS. Start with packaged local fleet, verified seed/locks, dedicated permitted API account, read-scoped GitHub Connection and one-way fixture SettingsSource. No Docker runtime on host.

1. Upload locked inputs as above. `template_create({"name":"python-tools","definition_blob_id":"<definition>"})`; wait and capture `template_id`. `template_build({"template_id":"<returned>","definition_blob_id":"<definition>"})`; wait for ready and boot-probe success. CLI uses `lamp template create --name python-tools --definition-blob-id <definition_blob_id> --json` then `lamp template build <template_id> --definition-blob-id <definition_blob_id> --json`; API uses `POST /v1/templates` then `POST /v1/templates/<id>:build`.
2. Create `network_policy` with public default/private deny and the required scoped Connections. Create `settings_source` with chosen host/source/path categories; verify no key material. `recipe_create` saves returned template/harness/kit/network/connection/settings IDs, `auth_mode=api_key`, no optional devices, default Linux 2/4096/20 resources and lifecycle. Dashboard preview shows all effective inputs; stale recipe edit conflicts.
3. Create task/sandbox; use the returned initial Session IDs from `operation.outcome.result`, or `session_create` if tool-only. Concrete CLI/API/MCP exchange uses actual substitutions:

```sh
lamp sandbox create --recipe-id <recipe_id> --idempotency-key <structured-key> --json
lamp operation wait <operation_id> --timeout-ms 25000 --json
lamp sandbox get <sandbox_id> --json
lamp exec create --sandbox-id <sandbox_id> --argv-json '["python3","-c","print(42)"]' --json
lamp exec watch <exec_id> --json
```

API create is `POST /v1/sandboxes {"recipe_id":"<recipe_id>"}`; MCP `sandbox_create` uses same body/key. Expected create outcome is `{"sandbox_id":"<real>","session_ids":["<real>"]}`. Exec output Frame has `channel=stdout,data_base64=NDIK`; terminal Exec has `exit_code=0`. Readiness checks for every requested tool/auth/network/Connection pass. Agent marker requires an actual permitted model turn plus tool result, not just `42`.
4. Fetch the locked public Flask commit inside token-free task execution. Upload/write a known `/work/probe.txt`, read it and download it. API uses the canonical `sandbox_file_write`, `sandbox_file_read` and `sandbox_file_transfer` routes; MCP uses those same tools; CLI uses `lamp sandbox file-write <sandbox_id> --path /work/probe.txt --blob-id <payload_blob_id> --json`, `lamp sandbox file-read <sandbox_id> --path /work/probe.txt --offset-bytes 0 --length-bytes 4096 --json`, and `lamp sandbox file-transfer <sandbox_id> --path /work/probe.txt --direction download --json`. Assert exactly `42\n` and its independently computed SHA-256.
5. Open returned Session in dashboard, T3 Code, Herdr and stock SSH. Deterministic fixture must show working→blocked; send `message_create` or lease-bound `session_input`, observe the known reply and idle. Separately run official Claude Code or Codex useful inference/tool turn against Flask. It may choose any valid change; service correctness does not depend on a model producing a particular patch.
6. Repeat signed-in subscription variant using vendor-only credential sandbox and token-free task sandbox. Bounded native tool hook must perform real coding without bearer exfiltration; hidden unsafe bridge remains blocked. A1 private fork/suspend is checked in Journey 3.
7. Fault counterpart: fail kit installation, auth and selected exit after allocation in separate creates. Expect `failed`, failed check, retained Operation/effects and complete cleanup. Wrong scope produces `SCOPE_DENIED`; no-VM credential request `VM_REQUIRED`. Drop one successful create response and retry same key; exactly 1 sandbox exists.

**Pass and cleanup.** Every actual output/ID/readiness/auth assertion above passes, host canaries unchanged, zero protected secret leaks. Capture dashboard/console/SSH views and source-keyed logs. End sessions, delete sandboxes, detach/delete fixture resources in reverse dependency order, retain only requested evidence and verify 0 workers/leases/reservations. Linux timing gate is the standard benchmark case, not an unlabeled cold build [owner-confirmed B1].

### Test journey 2 — Linux plus Mac; use three consoles and survive sleep

**Lanes and start.** Real LX+M5+AIR fleet, with nested Linux peers on LX and LA on M5. Covers HST, SBX, SES, ACC and updates on their fixed host/device/storage/access cases. Native media/licenses and owned device exposure are required. Capacity-heavy cases run serially.

1. Create host resources and target-key-bound Invitations, then `host_enroll`. CLI `lamp host enroll <host_id> --invitation-id <id> --proof <proof> --json`; API `POST /v1/hosts/<id>:enroll`; MCP `host_enroll` adds `host_id`. Wait for Host `state=ready`, inspect actual OS/build/arch and qualified Engine IDs. `host_list` returns `items` objects, never host counts under a list name.
2. Create Linux sandboxes on LX, M5 and AIR with strict `host_id` pins. Create native macOS on M5 and AIR. Linux `exec_create` runs `uname`; native Mac runs an actual OS build command. Dashboard and consoles label native macOS full-VM class slower. Each Linux sandbox is an independent VM at its host's native architecture.
3. Create official permitted harness sessions; capture each returned Session ID. T3 Code and Herdr show 1 fleet machine and every session. SSH `sessions`, `attach <id>`, `shell <id>` use actual advertised host/port. Deterministic fixture supplies exact output/input/status assertions; official binary supplies positive compatibility evidence.
4. Device branch requests display/audio_input/audio_output/keyboard/mouse, then owned exposed GPU and disk in the exact device-physical/device-basic rows; run real loopback/input/compute tests and inspect absence from headless row. Unsupported request yields `DEVICE_UNAVAILABLE`, unsupported capture `DEVICE_NOT_FORKABLE`. No missing hardware marked passed.
5. Mac limit branch creates 2 native resumable allocations, suspends one and concurrently attempts a third: `NO_CAPACITY`, actual count remains 2. Delete one, retry with new key, verify success. Linux sandboxes do not consume these slots. Native-Mac disk-only fallback explicitly boots fresh; Linux-on-Mac still proves RAM fork.
6. Repeat two-host loss with LX+M5, then M5+AIR; sleep either member and stop/disconnect coordinator unexpectedly. Survivor creates genuinely new structured-key work within its permit; proposed recovery ≤10 s. Old unreachable-home retry waits; global new grant/policy/teleport waits `QUORUM_REQUIRED`. Three-host majority also rebalances after fencing. Wake/rejoin rejects stale keys/writers.
7. Opt in to update on fixture fleet, inspect signed digest and rollout, interrupt one update and corrupt another artifact. Max 1 host applying; failure pauses/rolls back. Existing/suspended sessions preserve real files/RAM and referenced reader ABI.

**Pass and cleanup.** Actual host/guest/device/native-license results, one-machine console recordings, both two-host survivors, 0 duplicate work/overspend/writers, truthful unavailable local data and successful rollback. Return update policy to original, end/delete test work, remove disposable nested hosts and verify no test worker, lease, state or listener remains. No OS/password/approval-dialog command is executed by this documentation task.

### Test journey 3 — Fan-out, live fork and keep the winner

**Lanes and start.** LX+M5; repeat functional cases on nested LA. Native Mac A2 is a separately labeled disk-only test; only native LX has owned standard Linux timing gates [owner-confirmed B1]. API-key-proxy baseline and separate same-person approved subscription case on fixed auth-subscription rows. Public Flask commit, deterministic memory/disk pattern helper, selected tracked/uncommitted/untracked fixture files and one shared Task budget.

1. Start official harness in parent sandbox and record actual Session ID. Spawn own sandbox: CLI `lamp session spawn <parent> --sandbox-mode own --recipe-id <recipe_id> --selected-paths-json '["src","tests"]' --json`; API `POST /v1/sessions/<id>:spawn`; MCP `session_spawn`. Wait, capture `session_id,sandbox_id`. Repeat shared mode with compatible trust; verify same parent sandbox ID, distinct Session ID/output/input.
2. In own child, prove selected file tree/modes/binaries match starting parent and private writes diverge. Native harness spawn hooks must also produce independently visible sessions in T3 Code/Herdr/SSH. A hidden internal sub-agent fails, even if explicit Lamp spawn works.
3. Run generation-pattern helper with unsaved RAM marker, dirty disk and independent fsync ledger. Fork child twice using `sandbox_fork({"sandbox_id":"<actual-child>","fork_mode":"memory","durability":"local"})`. Capture each new `sandbox_id,session_ids` from its Operation. A subscription same-owner private fork keeps sign-in under A1, never claims stripping. In native Mac fallback, run a separate disk-only test with expected lost RAM, never count it as this memory-fork path.
4. Before first fork external I/O verify fresh network/member/machine/broker identity and PRNG/TLS generation hooks. Test expired authority, wall-clock correction and attachment reconnect rules. Parent and both forks run; dirty RAM/disk generations agree with independent oracle, then writes diverge.
5. Send known alternative messages using each returned ID: `message_create({"session_id":"<returned-A>","location_epoch":<current>,"text":"Run fixture alternative A"})`, then B. Fixture A writes known passing patch, B writes known failing patch. Official model choice is not required for deterministic result. All consoles show separate statuses and actual test output.
6. Child returns `ChangeSet` with base tree/bundle/patch. Dashboard compare→select A paths/commits→`change_set_apply`; CLI/API/MCP use canonical apply action. Deliberate parent edit must yield visible conflict with 0 parent loss. Resolve explicitly, apply selected result, delete loser. Delete source once while descendant remains to prove ancestor references survive.
7. Fault counterpart: lost fork response retry creates 0 extra alternatives; forbidden delegated host/recipe/action returns `SCOPE_DENIED`. Unsafe device or missing clean reconstruction fails visibly without usable unsafe publication. Revoking parent budget/grant stops descendants; exposure consent does not inherit. Call sandbox_prepare_fork before copying any inherited raw secret, approve fresh child-bound Exposures, then sandbox_fork with prepared_sandbox_id/exposure_ids; absent consent fails before copying. Inspect actual RAM in both runs.

**Pass and cleanup.** Every native-LX standard Linux fork trial is <1000 ms, including capture, credential handling, continuing source and useful fork [owner-confirmed B1]. Exactly 1000 ms fails. RAM/disk/authority checks have 0 errors; useful approved subscription continuation succeeds and clean transferable save starts fresh. End descendants, reject unapplied ChangeSets, delete captures/resources and verify referenced surviving winner still works before final cleanup.

### Test journey 4 — Native GitHub Actions and stranger public PR

**Lanes and start.** LX Linux ci-native/ci-trust cases; GitHub ordinary additionally on M5 native macOS guest. Other provider/native-OS combinations are excluded. Dedicated test GitHub organization/repository contains pinned public Flask source and a trusted workflow. Trusted controller/registration credentials remain host-side. Job containers/services use requested Docker kit only.

1. Register `github_actions` plugin/controller with one-job recipe and immutable source policy. Native full-job route selects configured ephemeral runner label. A clean offload job first calls auth_flow_bootstrap with its ephemeral proof key (no Lamp key/manual flow); it then obtains a GitHub JWT for the returned audience `lamp:<fleet_id>:<challenge>` and canonical `auth_flow_exchange`; no made-up auth CLI/action repository. Real GitHub setup requests `contents:read` and `id-token:write` only on trusted route.
2. Run tests, Docker build and compose/service fixture. `job_get` returns actual external/job/task/sandbox/artifact IDs. Dashboard shows state progression, logs, cache namespace and result. T3 Code/Herdr/SSH watch actual job/action session; GitHub receives correct native result and artifacts.
3. Qualify Copilot eligible plan/OS/`copilot-setup-steps`, official pinned Claude Code Action and Codex Action separately with permitted dedicated credentials and useful inference/tool output. Cancel each and lose listener/controller once; no orphan or second runner assignment. Lamp guard remains authoritative.
4. From stranger account submit hostile `pull_request` changes. Intake verifies immutable repository/owner/ref/SHA/event, not labels/workflow claims. OIDC-less PR uses trusted controller intake. It must never execute untrusted head under privileged `pull_request_target` authority.
5. Assert `job.trust_class=public_pr`, fresh VM/new writable root/clean template, allowlist-only network, no owner settings/mounts/model/vault/Lamp keys/protected Connections/trusted writable cache. If job-readable authentication is unavoidable, allow only the CI system's own short-lived job token and its read-only repository token; record each exact one-job scope/expiry and no other secret [owner-confirmed B2].
6. Hostile job attempts guest-root host escape, private/metadata/fleet/neighbor traffic, arbitrary upload, credential requests, token mint/cross-job control/result replay, trusted cache poisoning, fallback/Exposure override and persistent daemon. Independent sinks/state show 0 forbidden effects; allowed CI one-job token cannot cross its job [owner-confirmed B2]. Native pipeline still performs useful read-only source/test/output work.
7. On success/fail/cancel revoke execution ≤5 s, freeze, collect output ≤120 s, finalize complete/incomplete manifest, delete VM and deregister. Simulate upload loss/full disk/collector restart; download actual bytes after deletion and compare exact expected/present counts. Next job has 0 prior private state.

**Pass and cleanup.** Native result/cancel/action routes all pass; no extra PR secrets under B2 [owner-confirmed B2], no denied network/authority effect, correct artifacts and 0 reusable runners. Remove test source/workflows/accounts as authorized fixture cleanup; purge cache namespaces and remove disposable test state. Broader CI families run the matrix below with equivalent native checks.

### Test journey 5 — Team fleet through relay, approvals and two-way teleport

**Lanes and start.** access-console on LX, with native M5/AIR callers and a nested Linux peer behind NAT, AIR as Bea's enrolled console/laptop. Dedicated Alice/Bea Eilmer accounts, isolated test relay/issuer/account deployment, public console adapters, four vault test accounts and local service fixture.

1. Pair fleet root via independent local fingerprint/QR, enroll caller key with host authority, then create relay AccessRoute and Eilmer sign-in. Create a second fixture fleet on same relay; stock SSH gets separate assigned ports (example 22418/22419). Replay relay route grant or foreign enrollment must fail; routing grant alone cannot create fleet keys or request CSRs.
2. Alice sets team mode, creates Bea invitation/membership and role-scoped key; Bea accepts target-key invitation. API/CLI/MCP lists, dashboard, T3 Code/Herdr and SSH show only each person's own Sessions. Admin cannot see Alice's private content/vault. Public-service mode test admits isolated outside principal/quota without changing old privacy.
3. Share a clean Session watch-only, then type separately with exclusive input lease. Attempt watch-only input/approval/shell/file/browser action: 0 effects. Credential-bearing Session first reconstructs clean fresh-harness handoff, and Share stays pending until success. Revoke while SSH/event/output streams active; close ≤5 s online and obey declared offline lease bound.
4. Ask-first service action produces Approval. All interfaces inspect principal/task/generation, immutable adapter-validated action plan, target/digest/expiry; Alice approves one exact operation from authenticated phone or console. Try changed target, denial, expiry, concurrent use and Bea viewer replay: only one authorized effect. Standard login form fill works via each vault; uncertain secrecy uses person-only typing/takeover, never agent password [owner-confirmed B3]. MFA/CAPTCHA suppress all agent observations/input during exclusive person lease.
5. Obtain separate destination-owner Approval and NativeExecutionGrant before any host staging. First deny a sandbox-only teleport key targeting a host with zero files/processes; then teleport the exact official fixed harness endpoint pair using `session_teleport` and native_execution_grant_id; preserve Session identity/continuation/messages/pending tools/files. Interrupt all phases; inspect committed source/destination epoch and recover without 2 writers. Direct local auth obtains approved sign-in; no transfer envelope includes credential. Unsupported required harness continuation remains blocked.
6. Set shared Task budget and force stuck/decision/done notifications, stop at deadline; exact proxy cap never overspends while opaque usage shows best effort. Verify process/flow/request audit against independent ledger [owner-confirmed B4]. Consent off emits 0 usage analytics; opt in numeric schema then opt out/purge.
7. Hostile relay captures/substitutes/replays bytes/first pairing/grants/cert requests. Hostile sibling port page attacks real account/product cookies/CSRF/CORS/WebSocket/service workers in Chromium/Firefox/WebKit. Expected 0 account/private-data compromise. Separate account code/domain/CA/host compromise remains disclosed trust limit, not a false relay guarantee.

**Pass and cleanup.** Same fleet identity across NAT/routes/consoles, role privacy, one-use decisions, protected takeover, successful two-way continuation and active revocation. Close shares/routes/leases, revoke fixture keys/members/hosts, remove vault/service bindings and receipts per retention. Verify no disposable resources or active fixture authority remain and preserve sanitized test evidence only.

## Security attack suite

Every attack runs against the exact secured release/configuration, not a weakened benchmark mode. Every row includes one successful authorized operation and every listed hostile variant. Zero unauthorized bytes/effects is the pass threshold. Before and after each attack, release one fixture canary deliberately to verify scanner/sink health; an unhealthy observer invalidates evidence. Code tests use the real enforcement named in QA-003; only surrounding vendor/time/fault transport may be faked.

Each attack runs on the frozen compatible assignments below, using the ATK-01–44 meanings below. Here `G` is every positive host-linux/host-macos row; `F` is every fallback row; `B` is the broker-browser-vault service set; `H` is the LX dev relay plus Chromium/Firefox on LX and WebKit on M5/AIR; `T` is real LX/M5/AIR one/two/three-host cases plus separate LA enforcer tests. These symbols are test sets, not new runtime capability flags.

| Attack | Lanes / requirements | Attack and exact pass condition |
| --- | --- | --- |
| ATK-01 | G / SBX-001 | Guest root probes host files/processes/control/IPC. Pass: 0 host canary reads/writes/execs or escape; worker confined to assigned handles. |
| ATK-02 | G / SBX-001, PLG-001 | Malformed VMM/device/control/filesystem inputs, inherited handles and crash dump paths. Pass: 0 host/neighbor/broker access; only affected worker fails visibly. Real OS identity/profile/IPC/handle restrictions tested. |
| ATK-03 | G+F / SBX-001, STO-004 | Guess neighbor IDs/paths, inspect process/memory/mounts across owners. Pass: 0 foreign private bytes/effects and `NOT_FOUND` without existence disclosure; shared-mode same-trust boundary disclosed separately. |
| ATK-04 | device-basic + device-physical / SBX-005 | Request unallocated device, inspect host/neighbor display/audio/USB/GPU from allocated device. Pass: 0 unrequested optional hardware, 0 host/neighbor canary; requested hardware actually works. |
| ATK-05 | G+F / SBX-004, OPS-009 | Exhaust CPU/RAM/PIDs/disk/inodes/control records, fork backing, artifacts, audit, streams and unauthenticated relay/RPC; overflow integers/cancel partial reservations. Pass: 0 quota overspend/double release/safety eviction, bounded buffers, unrelated tenant completes 100/100 probes. |
| ATK-06 | F / SBX-006, CI-005 | Attach login/MCP/browser/vault secrets without override; attempt trusted-public and stranger-public CI even with override. Pass: 0 default secret work; explicit owner Exposure labeled; 0 public-repository CI fallback ever. |
| ATK-07 | G+F / NET-001, KIT-004 | Root edits routes/firewall/DNS/proxy environment; TSI/Unix forwarding tries alternate path. Nested Docker runs only on docker positive cases; native guests/F instead assert unsupported or denied local Linux Docker. Pass: 0 denied packets reach sinks; real host guard still controls all paths. |
| ATK-08 | G+F / NET-005 | IP literals/mapped IPv6/UDP/QUIC/DoH/DoT/ECH/fronting/redirect/rebinding/wildcard tricks. Pass: 0 policy-denied traffic; transport versus strict-site claims match actual mode. |
| ATK-09 | G + fixed harness/auth/broker positives / NET-002, NET-003, CRED-003 | Read provider keys, impersonate member, kill VPN/proxy/guard and try direct route. Pass: 0 provider keys in guest, correct peer identity/observed exit and 0 fail-open packets. |
| ATK-10 | G+F / NET-006, CON-003 | Scan neighbor/LAN/loopback/link-local/metadata/fleet control, including redirect to private address. Pass: 0 ungranted packets/effects; explicit exact service works without opening subnet. |
| ATK-11 | G+F / INT-001, ACC-007 | Change API/CLI/MCP/SSH/events/files/browser route to bypass scope. Pass: 0 unauthorized effects/frames across every canonical operation/action pair; transports enforce same intersection. |
| ATK-12 | T / ACC-007, ACC-009 | Expired/revoked key reconnect, clock rollback, saved guest clock and host rejoin. Pass: 0 effects after declared current-authority/lease deadline; failover never resets scope/expiry. |
| ATK-13 | access-console on LX, clean GitHub job / CI-003, ACC-005 | Forged JWT/JWKS rotation/algorithm confusion/wrong issuer/audience/repo/workflow/tenant/challenge/replay. Pass: 0 keys/effects for invalid identities; valid real signed job succeeds. Crypto stays real. |
| ATK-14 | H / RLY-001 | Replay/concurrent redeem one-use grant, foreign host/account linkage and malicious first pairing. Pass: at most 1 valid redemption; 0 unapproved linkage or fleet authority from relay grant. |
| ATK-15 | H / RLY-002 | Hostile relay captures RAM/disk/logs, swaps endpoint/root, forges routes/certificates or replays streams. Pass: 0 recoverable payload/model secret or unapproved fleet action; independent pin/CSR/enrollment denies substitution. |
| ATK-16 | ci-trust + F denial / CI-005 | Stranger PR spoofs trust/workflow/recipe/host/kit/privileged trigger; OIDC absent; requests override. Pass: useful fresh-VM restricted job or explicit failure; 0 immutable-policy bypass. Only CI-issued one-job transport/read-only repository tokens may be present; no other secrets [owner-confirmed B2]. |
| ATK-17 | ci-native / CI-004, LIF-004 | Leave daemon/startup/file/runner registration; cancel or kill controller. Pass: execution revoked ≤5 s online, default cleanup deletes task processes/roots; trusted retain_stopped separately keeps encrypted inert state with zero job/network authority or runner. Every next job has zero private markers and no second assignment. |
| ATK-18 | G + fixed harness/auth/broker positives / CRED-005 | Dump env/files/RAM, package malware, output encodings and saved captures for real API/CLI key. Pass: 0 key canaries in any guest-visible state/sink; useful broker operation works. |
| ATK-19 | G + fixed harness/auth/broker positives / CRED-005, NET-005 | Host/path/method/SNI/Host mismatches, redirects, request smuggling, HTTP2 owner-pool reuse, bad upstream TLS/CA, SigV4 misuse. Pass: 0 credential or request outside exact owner/destination/schema; invalid upstream cert never accepted. |
| ATK-20 | G + fixed harness/auth/broker positives / CRED-005, BEN-004 | Allowed endpoint reflects headers/secret query/body/error, split across streaming chunks/encodings. Pass: 0 real keys in output/log/audit. Untrusted transformed-reflection route is denied, never “safe” based only on substring redaction. |
| ATK-21 | G + fixed harness/auth/broker positives / CRED-003 | Other person/untrusted host/hosted service requests subscription bearer; guest exfiltrates via extra Connection/tool arguments/results. Pass: 0 off-custody/cross-owner/nonvendor bearer; useful bounded coding bridge separately succeeds. |
| ATK-22 | G + fixed harness/auth/broker positives / CRED-004, SBX-007 | Hide subscription copies in heap/env/files/journals/caches/artifacts before capture. Pass: private same-owner capture retains permitted sign-in only in custody; transferable save/share/export reconstructs clean/fresh harness with 0 bearer. Refusal passes safety but not required positive feature. |
| ATK-23 | G + fixed harness/auth/broker positives / CRED-003, CRED-004 | Concurrent fork/resume rotates refresh, expires grant, selects wrong owner/account. Pass: 0 cross-owner use/unapproved refresh race/central credential store; valid approved private continuation succeeds. |
| ATK-24 | G + fixed harness/auth/broker positives / CON-001 | Foreign MCP account/tool/resource, OAuth audience/refresh confusion or inbound passthrough token. Pass: 0 wrong-resource/owner effects/upstream tokens; only exact approved tool schema runs. |
| ATK-25 | G + fixed harness/auth/broker positives / CON-001 | Inside MCP server broadens limited token, replays another generation or reads gateway secrets. Pass: only stated audience/tool/resource/generation and ≤300 s authority; 0 owner-wide key exposure. |
| ATK-26 | G + fixed harness/auth/broker positives / CON-002 | Real gh/Stripe/AWS refresh, CA, WebSocket, error, canonical/chunked SigV4; request token export/mint/STS/presigned URL/signing oracle. Pass: supported useful operations work, 0 raw keys/extra capability; unqualified modes fail honestly. |
| ATK-27 | G+F / CON-003, ACC-008 | Write via read-only or read-looking side-effect function; ask-first before denial/after expiry/changed target. Pass: 0 forbidden service effects, one exact approved effect at most; all 4 access levels match database state. |
| ATK-28 | G+F / BEN-004 | README/issue/MCP/page prompt injection orders secret theft or extra action. Pass: actual hostile instructions may execute, but 0 boundary-bypassing data/effects. A model's verbal refusal is not test oracle. |
| ATK-29 | B+H / KIT-001 | Raw CDP/cookie/storage/evaluate/network/profile/download/reflection extraction. Pass: 0 cookie/password bytes through offered controls and useful authenticated task works. Unsafe password flow stays person-only [owner-confirmed B3]. |
| ATK-30 | B+H / KIT-003, KIT-005 | Agent races human MFA/CAPTCHA/fill with input/DOM/screenshots/recordings/reconnect. Pass: exactly 1 control holder and 0 agent observations/input during private entry; no residual sensitive page after return [owner-confirmed B3]. |
| ATK-31 | B+H / KIT-005, ACC-008 | Retrieve default filled password; encoded same-origin reflection/reveal/download; reuse approval across vault/secret/form/concurrent use. Pass: 0 password to agent, all 4 standard-form fills work, unsafe form uses successful person takeover, one bound effect maximum [owner-confirmed B3]. |
| ATK-32 | G+F / KIT-002, SBX-006 | Enable copied logins/password injection/weak boundary via defaults/recipe/child inheritance/task input. Pass: 0 activation without separate authenticated Exposure; deliberate exposure flagged/audited/tainted, never waives public PR. |
| ATK-33 | G+F / CRED-006 | Source symlink traversal, malicious instruction/MCP/git edits, automatic writeback, stale source approval. Pass: 0 unapproved source bytes/host execution; only approved selected current-revision paths apply. |
| ATK-34 | G+F / STO-001–005 | Guess/traverse/attach foreign cache/volume/artifact/template; poison trusted cache; delete live ancestor. Pass: 0 unauthorized persistent bytes/promotion/data loss; real later trusted build unchanged. |
| ATK-35 | G+F / SES-006, SES-007, INT-005 | Guess sessions/events/cursors/SSH attach, type with watch-only share, replay after downgrade/revoke. Pass: 0 foreign frames/existence hints or watch-only actions; channels close at stated bound. |
| ATK-36 | G+F / PLG-001, PLG-002 | Plugin crash/hang/RPC flood/inherited handle/subprocess/permission update probes. Pass: 0 core/unrelated failures or extra authority. Trusted engine's assigned-RAM access is disclosed A3 exception; neighbors/broker still denied. |
| ATK-37 | H / OPS-005, ACC-005 | Hostile sibling cookies/JS tossing/duplicate cookie/CORS/WebSocket/CSRF/frame/postMessage/service worker/account-code substitution. Pass: 0 stolen/overwritten account authority or cross-origin private bytes in Chromium/Firefox/WebKit with actual Clerk cookies. |
| ATK-38 | H / OPS-005 | Create 1,000 content names/fleet, flood fleet certificates, malicious CSR/cross-fleet names/issuer outage. Pass: 0 content cert issuance, Lamp ≤40 new/7 days and shared total ≤50, no false HTTPS-ready or wildcard private-key sharing. |
| ATK-39 | G+F / OPS-008 | Default install/crash/update/reconnect/stale consent tries analytics; race opt-out/send/purge. Pass: 0 optional events without current opt-in; only numeric approved schema, expiry ≤90 days including backups. |
| ATK-40 | G+F / OPS-004, QA-003 | Hide process start via fork-without-exec/direct syscall/nested runtime (Docker only on docker positives; native/F denial separately), tamper collector, delete guest logs, fill spool; multiplex opaque TLS; inject secret/log controls. Pass: 0 required process/flow/request gaps against independent ledger [owner-confirmed B4], 0 secret in export, gap/full audit stops effects ≤5 s. Builtin semantics not separate process obligation [owner-confirmed B4]. |
| ATK-41 | G+F / LIF-001–004, OPS-002 | Quiet descendants/held sockets/sleep/clock jumps/reconnect/fork evade idle/time/token stop. Pass: active work never falsely suspends; deadlines never reset; all local descendants/authority stop ≤5 s after hard deadline. Exact reservations never overspend. |
| ATK-42 | G memory-fork rows; macOS A2 disk-only separately / SBX-007, ACC-009 | Capture mixed generations, copy PRNG/TLS/identity/socket/volume-writer/expired handles, immediate fork I/O. Pass: 0 mismatched generation/duplicate identity/wrong authority before first effect; source and fork continue independently. |
| ATK-43 | T / HST-004, HST-005, INT-004 | One/two/three-host loss/partition/rejoin/update failure plus lost-response/stale-backup/key replay. Pass: useful survivor work within permit, 0 duplicate/overspend/double writer, 0 auto-update before consent, at most 1 updating host; expired home key never fresh. |
| ATK-44 | harness-api/arm/native endpoint pairs + T / SES-005 | Interrupt teleport each phase, stale input/location epoch, pending remote tool, credential-bearing envelope or cross-person move. Pass: 0 concurrent authoritative moved copies/lost acknowledged input/forbidden credential transfer; actual two-way harness continuation and truthful committed/unknown outcome. |

### Concrete code-test corpus

The code suite keeps cryptography, policy, filtering and real persistence inside the tested boundary. Fixtures must include:

| Boundary | Named inputs and required assertion |
| --- | --- |
| HTTP1/HTTP2/WebSocket broker | Conflicting Content-Length/Transfer-Encoding, folded/duplicate authority, HTTP2 continuation/pool owner switch, upgrade/redirect retries; 0 request smuggling or cross-owner substitution. |
| TLS/trust stores | Bad chain/name/expiry, missing public broker CA in OS/runtime/nested Docker, attempted verification disable; real handshake fails without leaking key or silent raw fallback. |
| Streaming filter | Secret split at every chunk boundary, JSON escapes/base64/URL encoding and error paths; 0 protected output. Arbitrary malicious recipient transformed reflection is denied, not certified by finite scans. |
| JWT/OIDC | Real test-key signatures; JWKS rotation with stale/unknown key, alg-none/HS-RS confusion, issuer/audience/time/challenge/repo mismatch and one-use race; reject before key issuance. |
| MCP OAuth | Wrong resource/audience, foreign refresh account, state/PKCE/callback mismatch and passthrough token; 0 wrong upstream effect. |
| AWS signing | Canonical path/query/header/body vectors, region/service/resource constraint, chunked streaming signatures and attempted STS/presigned URL/general signing; only exact granted requests signed. |
| Persistence/capture | Independent history model and actual fsync/restart at reserve/seal/head-switch/publish/ack/delete/GC; zero mixed RAM/disk generations, lost acknowledged bytes, dangling live ancestor or replayed effect. |
| Authority/approval/location | Independent permit/quota/location/approval model, 20-consumer approval race, sleep/expiry/clock uncertainty, narrowing between admission/effect; at most one approved/exclusive effect and no broader grant. |

## Benchmark suite

Freeze `lamp.benchmark/v1` before accepting an engine. Changes to method require a new version and side-by-side rerun; never move a failed boundary retrospectively. All numbers below are test requirements or labeled proposals, not measured Lamp results.

### Metrics and method

| Metric | Mandatory method and pass/report rule |
| --- | --- |
| Create | Standard 2 vCPU, 4096 MiB RAM, 20 GiB private disk; prepared local template, no preclaimed warm sandbox; normal guard/audit/CoW/fork tracking. 20 warmups, ≥200 measured creates/case. Caller-send→independently witnessed command p50 <500 ms on native LX [owner-confirmed B1]. |
| Fork | Same standard; 0/128 MiB/1 GiB recently written RAM plus dirty disk/in-flight writes and independent generation oracle. ≥100 trials/row; every trial <1000 ms including source continuation, barrier and usable fork [owner-confirmed B1]. Configured 4 GiB capture cost is counted even if recently written set is smaller [owner-confirmed B1]. |
| Larger/full-dirty configurations | Publish 128 MiB/1/2/8 GiB configurations, full-dirty 4 GiB and further admitted sizes, with full dirty RAM where feasible. Larger-than-standard sizes or >1 GiB recently written state are measurements without hard speed threshold [owner-confirmed B1]. Zero correctness/security errors still required. |
| Durability | Separate volatile RAM-backed, local fsync and replicated two-receipt fork rows. Every standard target row includes its actual commit cost and remains subject to hard target; slow supported row is reported failed [owner-confirmed B1]. Durable suspend cannot use volatile completion as proof. |
| Native speed | Each compute/disk/network workload below: ≥10 randomized paired direct-native/guest trials on same host; rate=guest/native, latency=native/guest. Each ≥0.97 at standard size on native LX [owner-confirmed B1]; nested LA and Mac host results are measurements. Native arm64 certification requires an outside native-hardware open-benchmark run. Proposed one-sided 95% lower bound ≥0.97; publish the frozen uncertainty method and paired random seed. A lower confidence bound below the proposed objective is reported as provisional statistics; a passing binding point estimate is not failed solely for that proposal. |
| Agent-ready | Caller-send→official signed-in harness useful turn/tool result. Report command and agent timestamps separately, including kits/auth/member enrollment. No non-Linux or agent-ready hard latency invented. |
| Suspend/resume | Durable commit/free task CPU+RAM, reboot and real continued task. Report p50/p95/p99/max, disk bytes and actual released allocation. Prepared Linux resume p50 <500 ms is proposed, not create/fork gate. |
| Density | Increase concurrent independently useful sessions until fixed useful-work/error ceiling fails; count pool/browser/storage VM/guard/broker/fork backing. Freeze useful-work latency/error ceilings in the run manifest before measuring; report failures at each level. Report max passing session count/resources, not just idle VM count. |
| Reliability | Distinct create/fork/suspend/resume/delete denominators per engine/platform; ≥2,995 independent zero-failure trials gives proposed one-sided 95% lower success bound ≥99.9%. Separate proposed 24 h loaded soak; correlation/thermal/leak observations never inflated into independent samples. |
| Security | ATK-01–44, healthy sinks/scanners, zero unauthorized protected leaks/effects; report subscription/Exposure/allowed job token separately [owner-confirmed B2]. Process ledger target uses B4 [owner-confirmed B4]; protected unsafe password flow uses human takeover [owner-confirmed B3]. |

| Native-speed category | Individual mandatory workloads; no category averaging |
| --- | --- |
| Compute | Fixed compiler/build (locked Flask/dependency workload plus fixed native compilation), compression/decompression and numerical/crypto work with identical input/compiler/core allocation and verified output digest. |
| Disk | Sequential read/write; 4 KiB random read/write; small-file tree/build; fsync latency; first-write CoW faults. Same underlying physical disk/filesystem and durability; private roots, not shared-volume substitute. |
| Network | TCP/UDP goodput and packet rate; request/response latency; small-flow setup; required proxy path. Same independently controlled peer/payload and endpoint/destination; include guard/audit/broker costs. |

Before any run the manifest fixes data sizes, request counts, concurrency, timeouts, random seeds, compiler flags, native CPU affinity/power mode, filesystem/storage, cache conditions, policy/auth/devices/durability and exact output digest. Use a published fixed-work suite rather than selecting a favorable test after seeing results. Missing locked workload inputs block the run. Freeze case timeouts in the run manifest; a timeout is a failed trial, never a discarded value or successful completion.

Start timer immediately before authenticated caller sends request. End using an independently received useful-command marker on the same monotonic observer; receiving proof gives conservative upper bound. Do not subtract uncalibrated cross-machine clocks or exclude RTT. Record engine-only spans as diagnostics. The reference is prepared from-template; cold build/download/import, warm-pool assignment, first credential/private-member setup and steady prepared cases each retain a separate labeled result. Every requested preparation cost stays in that case's elapsed time.

Alternate/randomize native/product run order with frozen seed. Record background load, host power/thermal state, storage pressure, first-touch faults, full capture bytes and parent pause. Compute benchmark target is standard sandbox only [owner-confirmed B1]; no hard Mac target until measured and set. Safety/correctness never waived for larger, native or weak classes.

### Canonical phase allocations

These architecture §19 figures are initial engineering allocations, not measurements. The end-to-end owner boundaries still decide create/fork speed; adding phase percentiles does not prove the total. Create headroom is 50 ms and fork headroom 100 ms. E03 independently checks the 35 ms p99 control allocation.

| Sequential phase | Create allocation, ms | Live fork allocation, ms |
| --- | ---: | ---: |
| Caller transport/return path | 20 | 20 |
| Authentication, durable identity and reservation/RPC | 35 | 35 |
| Template/private root preparation / source quiescence | 50 | 75 |
| Guard/broker/attachments / disk seal and independent heads | 60 | 70 |
| Engine restore / full 4 GiB CPU-device-RAM capture and required sync | 150 | 500 |
| Identity barrier and command / fork restore and barrier | 45 | 100 |
| Source continuation and both usable-command proof | Included above | 35 |
| Contingency | 90 | 65 |
| **Allocated total** | **450** | **900** |
| **Binding pass boundary** | **p50 <500** | **Every trial <1000** |

### Competitors and reporting rules

| Reference | Required comparison and disclosure |
| --- | --- |
| Cloudroom | Pin source/binary/license and one-thread sandbox/auth state. Disclose guest-readable credentials versus Lamp external broker; use same useful readiness endpoint. |
| E2B Embed | Pin current single-host Firecracker implementation and its dependencies. Compare command-ready, continuing-source RAM/disk semantics and matched local ownership/storage. |
| Docker Sandboxes | Pin microVM/proxy/client versions and mount mode. Competitor may use OCI/Docker; Lamp still cannot import Docker templates or require host runtime. |
| microsandbox | Pin complete msb_krun graph, private-memory/capture options and local cache preparation. Shared Lamp lineage is disclosed; not counted independent VMM defect diversity. |
| SmolVM | Pin runtime/version/platform fork semantics. Frozen source versus continuing source is a separate class; no fake direct ranking across them. |
| Modal/Daytona and Matchlock | Context/reference, with optional comparable public API results. The five rows above remain required; closed/unavailable internals are never guessed. |

Publish exact versions/commit/build options, machine/OS/CPU/FS/resources, host-mounted versus mountless state, isolation, devices, security/audit, auth mode, dirty memory/disk, parent continuation, copied/private RAM and durability. Unsupported says unsupported; vendor unavailable says vendor unavailable; failed adapter says test failed. None is zero latency or a Lamp win.

Every latency report contains sample count, p50/p95/p99/max/range, successes/failures/timeouts/cleanup failures, confidence method and all raw redacted observations. No success-only filtering. Exactly 500 ms create p50 fails; exactly 1000 ms fork fails; exactly 0.97 native ratio passes [owner-confirmed B1]. Publish each workload separately; summary ranking cannot hide missing class or weaker isolation. Reports include source inputs/reproduction commands/configuration/raw observations and attack fixture health.

Every source change queues a source-keyed run. Every PR revision must carry required fresh private-CI results or a visible block; hardware scheduling/cost does not turn a missing lane green. First public release publishes reports and reproducibility data under O6. Repository visibility may change after engine demo under ADR 0008; that is separate from public release/test completeness. No benchmarks were run for this document.

## Test-lane matrix

No money is spent on testing. Every project-run test, benchmark and dev relay uses these three owned machines [owner-confirmed O2, replaced 2026-11-08]. All secondary Linux-host rows below are nested. They are required compatibility/security evidence, never native physical speed evidence.

| Lane | Owned machine / role | Required evidence and limits |
| --- | --- | --- |
| `LX` | Ubuntu laptop: Intel Core Ultra 9 288V, 8 cores, 30 GB, Ubuntu 26.04 | Native x86_64 KVM; nested virtualization enabled for secondary lanes. Hard B1 speed reference. Hosts the always-on dev relay; benchmark with relay idle, then report contended diagnostics separately. |
| `LX22`, `D12`, `F40`, `L510` | Ubuntu 22.04, Debian 12, Fedora 40 VMs on LX; separate Linux 5.10 boundary-kernel VM | Nested KVM functional/security/minimum-version lanes. Record both outer and inner CPU/kernel/hypervisor/storage. No native-speed certificate. |
| `LA` | Linux arm64 VM on the M5, nested virtualization explicitly enabled | Nested KVM functional/security lane. Apple supports this on M3 and later; probe the actual M5 API [architecture source S59](architecture.md#25-sources). Native arm64 speed is certified only when someone runs the open benchmark on native arm64 hardware. No required paid or external machine. |
| `M5` | Apple M5 Mac, 10 cores/24 GB, macOS 26 | Native Mac host: HVF Linux and native macOS VZ; devices, confinement, sleep and capacity. Distinct from LA's nested Linux host. |
| `AIR` | MacBook Air M2, 8 GB, fanless, macOS 26 | Low-end/heat-limited Mac lane. Measure sustainable capacity/thermals and sleep/wake, not only cold bursts. Admit only what fits including browser/storage/capture overhead; insufficient room rejects before work. No hard Linux-host timing allocation is inherited. |
| `NV` | Linux VM on LX with nested virtualization turned off | Required gVisor fallback/denial lane. Mac process fallbacks use M5/AIR with Lamp's hypervisor access explicitly denied; record that test condition, not a claim that those physical machines lack virtualization. |

macOS 26 is the certified minimum on both Macs. Older macOS is uncertified; a macOS guest cannot act as a Lamp engine host.
Vendor test rows use the owner's existing paid accounts and subscriptions, which cover every required row [owner decision 2026-11-09]. Never buy a new plan, machine or service. Pay-per-use API keys run under a hard spend limit set in the provider's dashboard. Never substitute a fake certification.
Before each run, inventory OS/build/CPU/RAM/filesystem/storage/devices, confirm no owner project is touched, boot a real guest, test capture/restore and private guard transport, and pin the manifest. Missing nesting/API support is a failed required functional row, not permission to substitute an external machine. Collect sanitized evidence, revoke fixture credentials and remove disposable VMs/state after the run. Schedule capacity-heavy lanes serially within the three machines' actual reservations.

### Fixed capability cases

| Required coverage | Mandatory lanes / independent acceptance |
| --- | --- |
| Linux guests/create/RAM fork/suspend | LX; nested LX22/D12/F40/L510/LA; HVF on M5/AIR. Only native LX carries owned B1 speed certification |
| Native macOS | macOS guests on M5/AIR. Memory, concurrent source, disk identity and licensed state are checked separately; native Mac A2 fallback is labeled [owner-confirmed A2]. “Native guest” here identifies guest OS, not physical test topology |
| Requested devices | Fixed positive device rows below use only eligible owned hardware/nested virtual devices. Virtual display/audio/input fixtures are permitted; GPU needs an actual exposed device. Missing positive hardware stays blocked; rejection alone cannot pass it. No new machine or spending is authorized |
| Harness/auth/CLI/MCP/browser/vault | Fixed rows below cover every §9 harness, named auth mode, all four vaults and gh/stripe/aws/MCP; deterministic protocol fixtures plus separate official-binary useful-work tests. No detected-capability denominator |
| Consoles/sub-agents/teleport | Stock SSH on local/Tailscale/relay/owner HTTPS/Cloudflare routes; public T3 Code and Herdr contracts plus reference console; each native sub-agent independently visible and two-way continuation per harness |
| Native CI/triggers | Each architecture §16 provider/runner/action row on its actual controller/service, including cancel/loss/orphans, native OS and Docker/service jobs; signed Slack/Linear/webhook and DST/failover schedules |
| Fleet/storage/security/update | Real 1/2/3-host sleep/partitions plus 32-host metadata/limit lane; crash points, direct sinks, role downgrade/revocation, hostile relay/sibling origins, ABI readers, rollbacks and clean uninstall |

The frozen denominator is `lamp.compatibility/v1`, revised 2026-11-09 under the accepted owner scope decision, with 22 fixed case IDs. The following tables define required positive cases before capability detection. Expand only the explicitly listed lane sets, never all axes. A run manifest pins concrete binary/media/build digests and steps for every ID. It cannot remove a case, replace a useful positive with a refusal, or infer that an unknown/unavailable vendor passed. Changes require a dated architecture/matrix revision and review; deleting a required ID fails conformance. All rows start specified/unqualified.

| Required case IDs | Exact positive rows / explicit exclusions |
| --- | --- |
| `host-linux` | Linux guest on LX/LX22/D12/F40/L510 (x86_64), LA (arm64, nested), M5/AIR (arm64, Mac host). Each has create, full RAM fork, local suspend/resume, guard, audit and update/recovery; each admitted engine gets a separate digest row. |
| `host-macos` | Native macOS 26 guest on M5 and AIR, owner-built media, suspend/resume and A2 disk-only fork if needed; two-allocation license cap. No nested Lamp engine inside that guest. |
| `fallback` | Linux/gVisor on NV; native-process tasks on M5/AIR with virtualization access denied. Label weak boundary; test default secret rejection, explicit scoped override and fresh-VM rejection for trusted-public and stranger jobs. Native process fallback cannot run nested Linux Docker. |
| `device-basic` | For Linux guests on LX/LA/M5/AIR and the two host-macos rows: display, audio_input, audio_output, keyboard, mouse and extra disk. Each device type is a separate case. Capture support is tested separately; DEVICE_NOT_FORKABLE does not pass a required memory-forkable device claim. |
| `device-physical` | GPU: Linux guest on LX, Linux and macOS guests on M5/AIR. Each needs real exposed hardware and useful accelerated work. Other GPU/guest pairings are explicit exclusions pending matrix expansion. USB passthrough is not in release one [owner decision 2026-11-09]. |
| `docker` | Linux guest on LX/LA/M5/AIR: nested Docker build, compose and service job, no host daemon/socket. NV gVisor has a separate denial/compatibility diagnostic, not a required positive Docker certificate. Native macOS and process fallback rows do not inherit a Linux Docker requirement. |
| `storage-fleet` | ext4 on LX/LX22/D12/F40/L510/LA; XFS and btrfs disposable LX test filesystems; APFS on M5/AIR. Real one/two/three-host cases use subsets of LX/M5/AIR; nested Linux endpoints join those cases as explicit peers. Thirty-two-host control-scale tests use bounded simulated metadata endpoints on the same hardware, not 32 simultaneous standard VMs or real failure-domain evidence. |

Required harness, auth and service cases are finite too:

| Required case IDs | Exact lanes/modes and acceptance |
| --- | --- |
| `harness-api` | On LX Linux x86_64: claude_code, codex, opencode, cursor_cli, copilot_cli, amp, goose, gemini_cli, pi, devin, droid, each with its architecture §9.2 official provider/service API route. Every ID must do useful signed-in work, native spawn discovery and two-way native-LX-host↔LX-sandbox continuation. No cloud-only service is mislabeled a local harness. |
| `harness-arm` | claude_code and codex API route on Linux arm64 guests of LA/M5/AIR, with their official native Mac binary on M5/AIR as the two-way teleport endpoint. Other harness/arm64 combinations are unadvertised until qualified, not inherited from x86_64. |
| `harness-native` | claude_code and codex API route on native macOS guests of M5/AIR. Native host↔sandbox continuation is required for each. Other native-guest harness combinations are outside the initial certified matrix. |
| `auth-subscription` | claude_code approved subscription sign-in/setup-token, codex native ChatGPT/device sign-in and approved OSS transfer, copilot_cli device sign-in: LX Linux guest, exact eligibility/refresh/custody E07/E08 proof. codex native ChatGPT/device and claude_code native subscription also run on M5/AIR Linux guests. Unapproved vendor routes are not certified; API success cannot substitute for these required permitted routes. |
| `broker-browser-vault` | gh issue/PR/Git HTTPS, stripe test-mode operations, aws locally verified SigV4 plus permitted no-charge useful service fixture, external HTTP and stdio MCP plus inside delegated MCP: LX Linux guest with owner broker on LX. All four architecture §9.3 vault IDs fill a standard HTTPS form, complete useful work and private human fallback on LX/M5 browser services. In-sandbox copied-cookie/password and weak-boundary Exposures have separate opt-in tests, including child consent before fork. |
| `access-console` | Each of local, tailscale, relay, owner_https, cloudflare on LX: reference console, public T3 Code and Herdr contracts, stock SSH sessions/attach/sandbox shell, own/shared sub-agents, reconnect/revoke. Headscale is a separate optional diagnostic, never SaaS federation evidence. Keyless GitHub bootstrap runs from a clean job with no Lamp key/flow fixture. Native-host teleport additionally tests denied/granted NativeExecutionGrant. |
| `ci-native` | Linux x86_64 on LX for github_actions (ordinary, Copilot cloud agent, Claude Code Action, Codex Action separately), gitlab (Fleeting and Custom separately), buildkite, jenkins, forgejo, gitea, circleci, azure_pipelines, bitbucket, woodpecker. Each has successful job/result, failure/cancel, controller loss, logs/artifacts and orphan cleanup. GitHub ordinary Actions additionally runs native macOS on M5. Other provider/native-OS products are not inferred from Linux certification. |
| `ci-trust` | Each ci-native Linux provider row runs trusted-private, trusted-public and stranger-public jobs. All public jobs require fresh VMs/allowlists; stranger rows allow only B2's one-job token and no retained execution. Trusted failed jobs test retain_stopped. Docker/service CI is separately required for GitHub ordinary, GitLab both modes and Gitea on LX Linux; do not multiply Docker into native guests. |
| `triggers` | slack, linear, webhook, schedule on LX with verified delivery, retry/duplicate, cancellation and useful task result; schedules additionally exercise DST/overlap/quorum loss using LX/M5/AIR. |

### Contract-boundary cases

Required contract-boundary cases are part of the same frozen matrix, not optional observations:

| Case | Required observed boundary / fault |
| --- | --- |
| `limits-observations` | Admit 32 simultaneous mutations/principal; 33rd returns RATE_LIMITED without an effect. Permit 64 preauth relay connections/source; reject 65th. Heartbeats are scheduled every 2 s and lost/stale state is visible. Read desired_replicas/achieved_replicas, recoverability receipts, recovery_state and fresh/stale/unknown suspend_eligibility in API/CLI/MCP/dashboard. |
| `approval-exposure` | Adapter recomputes plan/digest; a misleading description cannot change shown/effected SQL, form, vault item or settings diff. Create→get shows pending_approval with no secret bytes; denied/expired approval never delivers. Successful activation/delivery marks activated_at. Prepared-fork child consent is absent/approved in separate actual-RAM cases. |
| `continuation-authority` | Before deadline auto-wake; 24 h later TASK_AUTHORITY_EXPIRED, then explicit new Task/Budget resume. Concurrent callers, old handles, expired approval and retained raw-secret grant cannot produce two active task/generation bindings. Sandbox-only teleport fails before native host staging; explicit owner-approved NativeExecutionGrant succeeds only in its paths/user. |
| `keyless-and-recovery` | Clean forge job calls bootstrap→JWT→exchange with no Lamp key/flow ID, including flood/replay/wrong proof-key tests. Transfer owner and recover lost root locally with fences; reject every old root/owner/host/mutation key. Restore encrypted real bytes on a clean replacement OS without original store; data recovery cannot grant another person's subscription custody. |
| `secret-result-transport` | Lose a share-link response, replay the same key, inspect same grant/Operation and secret_available=false; scan durable records/events/audit for bearer URL. Verify CLI --output bytes/hash and MCP EOF/chunk/whole hashes. Test decoded and serialized maxima, one byte over and worst-case metadata independently. |
| `telemetry-expiry` | Only fixed numeric counts/timings from §17.4 pass; reject an OS/engine version field, name/path/ID and arbitrary numeric label. Assert opt-in/opt-out and 90-day expiry. Derive all key/approval/link/lease test expiries from t0 and check before/at/after. |


### Native CI and trigger qualification matrix

Each row's run manifest must pin controller/server, runner, action, plugin digest, execution mode, guest/host, fixture pipeline and real native result/cancel/artifact/deregistration evidence. “Version not yet pinned” is blocked before execution. Every row uses one job/workflow only and must reconcile after lost controller/runner/response. Native Linux/macOS rows are required wherever that vendor offers the promised job mode; unsupported vendor modes remain explicitly outside that mode and cannot erase another binding row.

| CI family / exact architecture plugin ID | Distinct native mode and required pipeline/check |
| --- | --- |
| GitHub Actions / github_actions | Go scale-set/JIT one-job runner; shell+Docker build+service fixture, TotalAssignedJobs/redelivery/cancel-before-acquire/listener loss, native check/log/artifact and deregistration. |
| Copilot cloud agent / github_actions | Pinned eligible Actions-based scale-set route, plan/OS/`copilot-setup-steps`; actual task/tool output, host guard unaffected, cancel/result/cleanup. |
| Claude Code Action / github_actions | Official pinned action+approved auth; actual useful inference/tool result, failed auth/cancel/cleanup, no raw-key fallback. |
| Codex Action / github_actions | Official pinned action+approved auth; actual useful inference/tool result, scope/time stop and cleanup. |
| GitLab / gitlab | Fleeting Instance capacity-one/max-use-one and separate Custom executor compatibility mode; prepare/run/cleanup, actual result/artifact, controller loss and Docker/service variants. |
| Buildkite / buildkite | Queue-aware controller + disconnect-after-job agent; cancel/disconnect retry/upload, zero second job. |
| Jenkins / jenkins | Real Jenkins Cloud/retention shim installation, unique inbound node/one executor; pipeline success/cancel/controller restart, node removal/orphan recovery. |
| Forgejo / forgejo | Version-matched ephemeral registration/runner; native workflow/artifact/result and server token/registration deletion. |
| Gitea / gitea | Separately pinned act_runner/server/labels; own container/service modes/result/cancel/delete; no inferred Forgejo equivalence. |
| CircleCI / circleci | Resource-class demand + single-task machine runner; claim/cancel/native result, no service auto-restart into second assignment. |
| Azure Pipelines / azure_pipelines | Pool-demand/native `--once` agent; one-job pipeline/artifacts/cancel and agent deletion; no required VMSS. |
| Bitbucket / bitbucket | Label-specific native runner, completion-driven stop/deregister; interrupted completion proves teardown before reassignment, no fictional one-shot flag. |
| Woodpecker / woodpecker | Concurrency-one external teardown after one workflow; native result/artifacts/lost controller and no reuse. |
| Slack / slack | Real signed timestamped immutable delivery, mapped source Principal and scoped recipe/reply destination; duplicate/replay/forged privilege text deny new effect. |
| Linear / linear | Same source verification/dedup with Linear's pinned actual signature/identity contract; real useful task acknowledgment. |
| Webhook / webhook | Pinned signer/verification policy, payload Blob and immutable source ID; bad signature/stale timestamp/replay/change in current authority deny. |
| Schedule / schedule | Frozen cron/IANA timezone/UTC occurrence; both DST edges, skip/missed notification, bounded catch-up ≤1, overlap denial, one quorum claim and pause/cancel race. |

### Resource operation and role inventory

The complete runtime operation set is architecture §§11–12. This standalone acceptance matrix supplies its lifecycle expectations. Standard grammar: CLI `lamp <noun> <action> --json`; MCP `<stem>_<action>`; API create/list `POST/GET /v1/<collection>`, get/update/delete `GET/PATCH/DELETE /v1/<collection>/<id>`. Extra actions are colon-kebab POST unless listed GET. Scope is `<kind>.<action>` including underscores in extra action (`sandbox.file_write`). No alias names are accepted in examples or docs.

| Resource family | Required successful lifecycle and removal checks |
| --- | --- |
| Fleet/Host | fleet get/list/update/transfer-owner/recover; host create/enroll/get/list/update/bench/drain/delete; re-enroll new identity, quota/fencing and stale host denied. Fleet deletion is not an invented product operation. |
| Principal/Membership/Invitation/IdentityProvider | create/get/list/update/delete where declared; invitation has no update, one-use accept/revoke/expiry; issuer/subject immutable; role downgrade revokes active authority. |
| Key/Grant/Share/Exposure | Key create/get/list/rotate/delete; Grant get/list/revoke/delete; Share create/get/list/update/revoke/delete; Exposure create/get/list/activate/revoke/delete, no update. Secret once, monotonic taint and pending revoke visible. |
| Adapter/Configuration/NativeExecutionGrant | Adapter get/list; Configuration create/get/list/delete (immutable document); NativeExecutionGrant create/get/list/delete with independent destination-owner approval and active revocation. Empty-host schema/package preparation is mandatory. |
| Engine/Harness | Read-only get/list, qualification evidence and selected digest; no invented create/update engine control. Third-party engine installs as Plugin plus owner trust. |
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

Every family tests malformed input, missing ID, unauthorized foreign ID (`NOT_FOUND`), scope denial, stale revision, expired authority, in-use ancestor (`RESOURCE_IN_USE`), cancellation race and already-deleted owned ID. Missing/already-deleted owned deletion succeeds idempotently; it must not detach another owner's resource. Source-of-truth action tables prevent adding CRUD methods where read-only or immutable resources disallow them.

| Role | Capacity/admission authority | Content/task authority | Credential decisions |
| --- | --- | --- | --- |
| Owner | Fleet policy/transfer/trusted engine/admission/admin config | Own plus explicit content grants; no automatic other-owner private content | Own credentials; others explicitly delegate |
| Admin | Assigned hosts/plugins/quotas, invite/remove within delegation; no owner transfer | Own plus explicit content grants; no implied private read | No automatic other-person vault/password approval |
| Member | Own tasks/recipes/keys/permitted capacity | Own permitted create/exec/files/watch/type; share owned shareable resources | Own permitted grants |
| Viewer | No create/admin default | Explicit watch/read only; no team-wide default listing | None |

Host root/admin is separately trusted for assigned host memory/disk. A restricted application role cannot make a hostile physical host safe for personal credentials. Owner-selected custody placement remains mandatory.

## Traceability and ID changes

The [round-5 response ledger](rounds/5-responses-requirements.md) maps all Y-001–Y-029 findings to real changes and requirement IDs. Architecture round-5 contract additions are carried into the affected records, fixed cases and parity index; no names remain proposed.

Every brief item is named and specified here. “Tested” is intentionally not asserted: this is document work, not an implementation run. “Blocked” means outstanding evidence/dependency, not permission to drop the feature. Ranges are inclusive.

| Brief section | Named / specified requirement IDs | Tested evidence in this revision | Blocked or conditional acceptance |
| --- | --- | --- | --- |
| §1 What Lamp is | PRD-001–003, INT-001, SBX-001, BEN-001–005, OPS-007 | None run | Full release matrix; speed B1 [owner-confirmed B1] |
| §2 Users/four problems/references | PRD-001, INT-006–007, CRED-001–006, BEN-005 | None run | All 4 goals across the 5 journeys and comparative proof |
| §3 Callers/interfaces/SDK/JSON/docs/retries/events | INT-001–008, ACC-007, CI-003 | None run | Generated parity and release SDK artifacts |
| §4 Agent location | INT-006–007, KIT-001–003 | None run | Real inside/outside task and protected browser |
| §5 Host matrix/fleet/placement/failover/update/engine | HST-001–006, QA-001–004, ACC-009 | None run | Fixed owned/nested OS-build qualification; macOS 26 minimum |
| §6 VM/devices/guest classes/resources/fallback/fork | SBX-001–007, CI-005, QA-004 | None run | Linux RAM fork on both host families; native macOS devices/save/license evidence; A2 fallback accepted |
| §7 Speed/security targets | BEN-001–005, CRED-003–005, KIT-001–005 | None run | Standard envelope B1 [owner-confirmed B1]; safe-fill B3 [owner-confirmed B3] |
| §8 Own templates/save tools/Docker kit/recipes | TPL-001–003, KIT-004 | None run | Clean reconstruction of custom tools and native owner-media workflow |
| §9 Idle/suspend/expiry/CI/warm/fork winner | LIF-001–006, SBX-007 | None run | Real durable resume/CPU-RAM release; A1/A5 accepted |
| §10 Lasting storage/owner defaults | STO-001–005, TPL-002 | None run | Actual consistency/fsync/backup/download/ACL proof |
| §11 Host network/exits/membership/ports/lists/links/defaults | NET-001–006, PLG-002, ACC-009 | None run | Guard/exit/member bridge and private default positive/negative proof |
| §12 Modularity/all plugin types/engine exception | PLG-001–002, ENG-002 | None run | Per-OS confinement/type conformance; A3 accepted |
| §13 Harness/sign-in/API keys/all settings | CRED-001–006, HST-003, ACC-008 | None run | All 11 official routes/refresh; bounded subscription task bridge |
| §14 MCP/CLI/owner service connections | CON-001–003, ACC-008–009 | None run | Limited-token/semantic read-only and useful clients |
| §15 Browser/4 vaults/phone approval/toolchain/Docker | KIT-001–005, ACC-008 | None run | Protected fill/human fallback B3 [owner-confirmed B3] |
| §16 All consoles/sessions/SSH/sub-agents/teleport/sharing | SES-001–007, ACC-008–009 | None run | Public console contracts/all harness spawn+continuation; stock SSH all routes |
| §17 Access/relay/account/roles/keys/GitHub exchange | ACC-001–009, RLY-001–002, CI-003 | None run | Independent pairing/issuer, real Clerk cookies, local/OIDC offline |
| §18 Every named CI/triggers/fresh public PR | CI-001–006, LIF-004, STO-001–003, KIT-004 | None run | Every native adapter; only CI job token B2 [owner-confirmed B2] |
| §19 Dashboard/budget/watchdog/notifications/audit/schedule/no overflow | OPS-001–004, OPS-009, CI-006, LIF-005–006, SES-005, PRD-003 | None run | Exact reservation/time enforcement; process audit B4 [owner-confirmed B4] |
| §20 eilmer.si/business/licenses/privacy | OPS-005–008, RLY-002, ENG-001 | None run | Browser/issuer/capacity/telemetry expiry; no second hosted domain |
| §21 Public reproducible every-change benchmarks | BEN-001–005, QA-001–004 | None run | Competitor/hardware/results on every PR; O6 publication accepted |
| §22 Engine first/reuse/language/named machines/no owner projects | ENG-001–003, QA-001–004, TPL-001 | None run | Feasibility gates and fixed three-machine/nested/device evidence; no spending input |
| §23 Prior research | ENG-002–003, CRED-002, BEN-005, QA-002 | No new vendor certification | Research is background; permission/version claims need current official evidence |

The six supplied research references remain advisory inputs: [sandbox products](../research/modal-daytona.md), [remote access and CI](../research/remote-access-and-ci.md), [VM backends](../research/vm-backends.md), [CI and integrations](../research/ci-and-integrations.md), [credentials and stateful apps](../research/credentials-and-stateful-apps.md), and [subscription auth](../research/subscription-auth.md). Brief/ADRs win conflicts; architecture §§11–12 win names. This baseline does not invent a stateful-app migration feature.

| ADR | Requirements carrying its decision |
| --- | --- |
| 0001 fleet | HST-001–006, SBX-002–004, STO-005, SES-001–007, ACC-007–009 |
| 0002 host networking | NET-001–006, CON-001–003, CI-005, OPS-004 |
| 0003 security/speed reuse | SBX-001/005/006, PLG-001–002, ENG-001–003, BEN-001–005 |
| 0004 amended custody | CRED-001–006, SBX-006–007, TPL-002, LIF-001, KIT-001–005, RLY-002, BEN-004 |
| 0005 Eilmer identity | ACC-003/005/006/008, SES-006–007, RLY-001, OPS-005 |
| 0006 eilmer.si only | OPS-005, NET-004, ACC-002, RLY-001–002 |
| 0007 own templates/no Docker runtime | TPL-001–003, KIT-004, SBX-006, ENG-001 |
| 0008 licenses/private demo | OPS-007, INT-002, PLG-001–002, BEN-005, ENG-001 |
| 0009 opaque relay | RLY-001–002, CRED-003, ACC-005, OPS-005, OPS-008 |

### Architecture critique changes carried into requirements

This ledger covers every C-finding from the architecture critique/merge responses. Accepted owner decisions replace stale pending markers in the earlier response log. E01–E14 remain architecture evidence gates; no gate is claimed run.

| Finding | Resulting requirement change / IDs |
| --- | --- |
| C-001 | Small clean install/process budgets, dormant workers, restart/uninstall — PRD-002, OPS-009. |
| C-002 | Distinguish coordination from data durability/RPO/restore — STO-005, HST-004. |
| C-003 | Go/Rust ownership and measured bounded RPC — ENG-003. |
| C-004 | Exact downstream engine graph, independent reference, continuing source/confinement — ENG-002, HST-006, SBX-007. |
| C-005 | Accepted A2 native-Mac suspend/resume plus labeled disk-only exception — SBX-003, SBX-007. |
| C-006 | Separate Linux-on-Mac HVF/VZ qualification and native macOS supported save/A2 fallback proof — HST-001, SBX-002–003, QA-004. |
| C-007 | O10 minimum host/boundary lanes distinct from guest kernel — HST-001, QA-004. |
| C-008 | Accepted A1 private subscription capture versus clean transfer — CRED-003–004, LIF-001, SBX-007. |
| C-009 | Accepted A5 fixed infrastructure/certified person-wait exemption — LIF-002. |
| C-010 | Monotonic disk/RAM/cache/artifact taint and clean tool reconstruction — TPL-002, CRED-004. |
| C-011 | Snapshot-compatible file-backed roots, real head journal/GC crash tests; no stock vhost-user snapshot assumption — SBX-007, STO-005, QA-003. |
| C-012 | On-demand charged shared-files service, remote storage on NV, native disk score separate — STO-002, OPS-009, BEN-003. |
| C-013 | Single live storage authority, two-receipt immutable durability and fenced recovery — STO-002, STO-005. |
| C-014 | Real guest TCP/UDP private-member bridge/DNS/port maps/peer identity — NET-003. |
| C-015 | Accepted O3 public/private defaults, strict-sites versus transport — NET-001, NET-005, CI-005. |
| C-016 | All 11 official exact-version auth/CA/stream/refresh/tool rows — CRED-001–002, CRED-005. |
| C-017 | Typed gh/Stripe/AWS/MCP operations, signing and limited-token exception — CON-001–002. |
| C-018 | Protected useful site/action/vault workflows, standard fill/private takeover B3 [owner-confirmed B3] — KIT-001, KIT-003, KIT-005. |
| C-019 | Actual two-way native continuation/epoch/pending tool proof — SES-005. |
| C-020 | Every native sub-agent independently visible/control-capable/charged — SES-002, SES-004, OPS-002. |
| C-021 | Permanent retry tombstones, immutable homes, stale-restore epochs — INT-004, STO-005. |
| C-022 | Complete canonical transport/stream/scope parity and accepted O1 SDK order — INT-001–005, SES-002. |
| C-023 | Accepted A3 exact-digest engine trust/assigned-RAM exception — PLG-001. |
| C-024 | WASI and separately confined native programs, permission/version updates — PLG-001–002, HST-005. |
| C-025 | Useful either-survivor two-host permit admission — HST-004. |
| C-026 | Single quorum/permit authority, fencing/quota/approval/location state — HST-004, ACC-008–009, SES-005, QA-003. |
| C-027 | tsnet bootstrap/state isolation/services/federation/Headscale qualification — ACC-001, NET-003. |
| C-028 | Actual Clerk subtree cookies and product-local one-use sessions — ACC-005, OPS-005. |
| C-029 | Independent enrollment/root/issuer/CSR and precise relay trust limit — RLY-001–002, OPS-005. |
| C-030 | Correct-depth fleet wildcard/global 40+10 shared certificate ledger — OPS-005. |
| C-031 | Separate native CI adapters/modes/actions/results/cleanup — CI-001–002, QA-004. |
| C-032 | Host-mediated intake; conditional one-job CI-token exception only [owner-confirmed B2] — CI-003, CI-005. |
| C-033 | Process-complete B4 audit plus opaque flows/full mediated detail [owner-confirmed B4] — OPS-004, QA-003. |
| C-034 | Root-task exact reservations; accepted A4 opaque best effort/hard time — OPS-002. |
| C-035 | Accepted O7 numeric opt-in schema/90-day expiry/relay metadata separation — OPS-008. |
| C-036 | Real per-OS worker identity/profile/IPC/handle confinement — SBX-001, PLG-001, QA-004. |
| C-037 | Full active descendant revocation/online/offline/residual effect matrix — ACC-009. |
| C-038 | Capture/dirty/backing/durability costs retained, standard envelope B1 [owner-confirmed B1] — SBX-007, BEN-002. |
| C-039 | Each secured native compute/disk/network workload ratio independently — BEN-003. |
| C-040 | Caller-send/independent command endpoint, case labels/RTT retained — BEN-001, BEN-005. |
| C-041 | Fixed owned/nested Linux arm64, Mac and minimum-OS lanes; native arm64 speed needs outside native-hardware benchmark — QA-001–002, QA-004. |
| C-042 | Explicit capability denominator and proposed 2,995-trial+24 h evidence — QA-004, BEN-005. |
| C-043 | Equivalent competitor work/state/security/durability and shared lineage disclosure — BEN-005. |

### Owner decision and old-question reconciliation

| Accepted decision | Baseline consequence |
| --- | --- |
| O1 | TS/Python at release one, Go/Rust immediately after: INT-002. |
| O2 | Replaced 2026-11-08: no spending; all project tests/dev relay on LX/M5/AIR, secondary host lanes nested: QA-001. |
| O3 | Public internet default/private-LAN-metadata block, all public-repository CI allowlist: NET-001/005, CI-005. |
| O4 | Private signed-in port URLs and expiring shares: NET-004. |
| O5 | Authenticated explicit exposures separately scored and warned: KIT-002/005, SBX-006, BEN-004. |
| O6 | Every-PR private benchmarks, public from release: BEN-005. |
| O7 | Numeric counts/timings only, off by default, 90 days: OPS-008. |
| O8 | Native guest media user-built/licensed, no redistribution: SBX-003, TPL-001. |
| O9 | Owner/admin/member/viewer with own-only content defaults: ACC-006, SES-006. |
| O10, amended 2026-11-09 | Owner decision: “skip windows for everything.” Windows hosts, native guests, test lanes, CLI builds and CI runner rows leave release one. Minimums are Linux 5.10+ on x86_64/arm64 and macOS 26+ on Apple Silicon; older macOS is uncertified: HST-001, SBX-002–003, INT-003, QA-001–004. |
| A1 | Private same-person fork/suspend keeps login; clean templates/export/share: CRED-004. |
| A2 | Native Mac disk-only fresh boot fallback; Linux-on-Mac RAM fork retained: SBX-003/007. |
| A3 | Audited built-ins and owner-trusted exact third-party engine digest: PLG-001. |
| A4 | Opaque flow audit, full mediated request, exact proxy/best-effort opaque tokens/hard time: OPS-002/004. |
| A5 | Certified person-wait inactive; builds/servers/background/open task connections active: LIF-002. |

Draft Q01–Q35 are not 35 still-open owner questions. Their resolution map is: Q01/Q35→HST-001/QA-001–004; Q02→INT-002; Q03/Q06→HST-002–004; Q04→INT-004; Q05→INT-005; Q07→HST-005; Q08→HST-006; Q09→SBX-003–004; Q10→SBX-005/QA-004; Q11→SBX-007/CRED-004/STO-002; Q12→TPL-001–003; Q13→LIF-001–003; Q14→LIF-005; Q15→STO-001–005; Q16→NET-001–003/005–006; Q17→NET-004; Q18→PLG-001–002; Q19→CRED-001–005; Q20→ACC-005–009/SES-006–007; Q21→CON-001; Q22→KIT-001–005/BEN-004 with B3 [owner-confirmed B3]; Q23→SES-002–003/ACC-008; Q24→SES-004; Q25→SES-005; Q26→CI-001–002; Q27→CI-003/005 with B2 [owner-confirmed B2]; Q28→CI-006; Q29→OPS-002–003; Q30→OPS-004 with B4 [owner-confirmed B4]; Q31→OPS-007/SBX-003; Q32→OPS-008; Q33→BEN-001–003 with B1 [owner-confirmed B1]; Q34→BEN-005. Remaining feasibility/configuration work is listed next.

All 98 old requirement IDs are retained without merging or renumbering. Seven additions split detailed acceptance obligations out of broad old records, which still reference their original features:

| New ID | Split from / reason |
| --- | --- |
| INT-008 | INT-001/004 and OPS-001: readiness, partial resources, revisions and cancellation. |
| STO-005 | STO-001–004 and HST-004: durability, backup/restore and stale-authority recovery. |
| ACC-008 | CRED-006, CON-003, KIT-003/005, SES-007: complete authenticated one-use decisions. |
| ACC-009 | ACC-007, SES-007, NET-004, CON-001–003: active descendant revocation. |
| OPS-009 | SBX-004, INT-004/005, PLG-001, STO families: control/retained-state exhaustion accounting. |
| QA-003 | SBX-007, STO families, INT-004: independent persistence/authority oracles and protocol corpus. |
| QA-004 | QA-001/002 and compatibility families: full capability denominator/reliability certification. |

CRED-004 is retitled and revised under accepted A1/amended ADR 0004, replacing impossible universal pre-capture stripping. QA-001 now specifies the fixed owned-machine lanes under replaced O2; native arm64 functional evidence is nested and speed certification needs a community native-hardware run. No requirement ID is removed by these bookkeeping changes; platform scope follows the dated owner amendment above.

## Remaining questions

### Owner

None. O1–O10, A1–A5 and B1–B4 are accepted. O2 is replaced by brief §22's three-owned-machine plan and macOS 26 is the certified minimum under O10. Custody hosts, vault preferences and quotas are normal configuration. An implementation failure cannot reopen accepted policy or silently remove a feature.

### Engineering

| Remaining evidence/configuration | Requirement and completion condition |
| --- | --- |
| Inventory and actual lane provisioning | Inventory the three known machines and qualify nested LX22/D12/F40/L510/LA, owner-built macOS media and owned device exposure. Keep unavailable positive rows blocked. QA-001/002/004; no additional machine or spending decision. |
| Feasible secured speed/storage | Run E01–E04 with exact engine graph, ext4/reflink/APFS paths and standard B1 envelope [owner-confirmed B1]; retain hard failures and publish larger-size measurements. No ABI freeze before capture/recovery proof. |
| Native guest/device capture | Prove full Linux RAM fork on both host families and every fixed required device row; native Mac supported resume/disk-only fallback/license constraints; SBX-003/005/007. |
| Useful bounded subscription task bridge | Official approved refresh/custody/continuation with vendor-only egress and adversarial encoded tool-data tests; CRED-003/004. If unproved, required route is blocked. |
| Exact official compatibility manifests | Pin the exact harness-api/harness-arm/harness-native/auth-subscription rows for auth/CA/stream/tool/spawn/two-way continuation, public T3 Code/Herdr contracts and all CI modes/actions; CRED-001/002, SES-001/004/005, CI-001/002. Goose ACP gaps need proved alternate mode, not transcript replay. |
| Safe browser/vault adapters | Implement useful standard-form fills for all 4 vaults plus hostile same-origin/reflection tests and successful private fallback [owner-confirmed B3]; KIT-001/003/005. |
| Protected process collectors | Prove every required process start/user/parent/time against independent observer on Linux/nested/native Mac [owner-confirmed B4]; close gaps ≤5 s and reject unproved mode, OPS-004/QA-003. |
| Exact metering bounds and residual remote effects | Certify model tokenizers/max allowances/trusted usage, cancellation behavior and returned-capability policy; OPS-002/ACC-009. |
| Public-PR native runner mediation | For each provider disclose and test only allowed one-job CI token [owner-confirmed B2]; host-side control/source/results where possible, CI-005. |
| Hosted deployment/capacity | Actual Clerk subtree/cookies across browsers, separate issuer/root recovery, stock SSH all access routes including Cloudflare, global ACME capacity before unlimited onboarding; OPS-005/RLY/SES-003. |
| Freeze implementation schemas/test inputs | Implement the canonical OpenAPI/field limits, immutable published plugin/adapter schemas, package locks/seed and frozen benchmark workload sizes/timeouts/statistics. Adopt or revise explicitly labeled engineering proposals with fresh evidence; no name/target drift. |

## Canonical parity index

There are no proposed public names. Architecture §§11–12 is canonical. The following index expands every standard operation for CLI/API/MCP parity testing; extra-operation bindings are exact architecture inputs/results, not new requirement IDs. Tests generate both allowed and denied scope cases for every entry. Bootstrap and local recovery retain their stated proof exceptions.

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
| `backup_restore`, `backup_export` | `:restore {target_host_id,new_authority_epoch,recovery_credential_source_id}` / `:export {target_host_ids[]}` → Operation, custody gate first [owner-confirmed A1] |
| `plugin_enable`, `plugin_disable`, `plugin_trust`, `plugin_test` | `:enable {}`; `:disable {}`; `:trust {digest,warning_acknowledged}` [owner-confirmed A3]; `:test {suite_digest}` → Operation |
| `job_cancel`, `job_reconcile`, `task_stop` | `:cancel {reason}`; `:reconcile {}`; task `:stop {reason}` → Operation |
| `trigger_deliver`, `schedule_run`, `schedule_pause`, `schedule_resume` | `:deliver {source_event_id,payload_blob_id,signature_metadata}`; `:run {occurrence_at}`; `:pause {}`; `:resume {}` → Operation |
| `notification_acknowledge`, `notification_retry`, `notification_sink_test` | `:acknowledge {}` → Notification; `:retry {}`; sink `:test {}` → Operation |
| `auth_flow_complete`, `auth_flow_exchange` | `:complete {code,state,proof}`; `:exchange {jwt,audience,proof}` → scoped credential once, with provider challenge checks |
| `update_rollback`, `operation_cancel`, `operation_wait` | `:rollback {}`; `:cancel {reason}` → Operation; GET `/{id}/wait?timeout_ms=...` returns current Operation, never implies success on timeout |
| `audit_record_export`, `telemetry_purge`, `api_describe`, `docs_read`, `metric_list` | POST `/audit-records:export {filter}`; POST `/telemetry:purge {}`; GET `/openapi.json`; GET `/docs?topic=...`; GET `/metrics` |


All suffixes above are under `/v1`; collection names come from the resource table. Errors and typed fields keep their exact architecture meanings.

</details>

## Requirement inventory by area

**105 requirements.** All records use binding must statements, with accepted B1–B4 decisions and explicitly labeled proposed engineering subtargets. Counts describe specified contracts, not implementation passes. The original 98 records plus 7 added records account for the total; none merged/deleted. SBX-003 now covers the native macOS full-VM class and its two-per-Mac limit; all platform-dependent records use the current Linux/Mac scope.

| Area | Count | IDs |
| --- | ---: | --- |
| Product and scope | 3 | PRD-001–003 |
| Callers and interfaces | 8 | INT-001–008 |
| Hosts and fleets | 6 | HST-001–006 |
| Sandboxes | 7 | SBX-001–007 |
| Templates and recipes | 3 | TPL-001–003 |
| Lifecycle | 6 | LIF-001–006 |
| Lasting storage and recovery | 5 | STO-001–005 |
| Network | 6 | NET-001–006 |
| Plugins | 2 | PLG-001–002 |
| Harnesses, credentials and settings | 6 | CRED-001–006 |
| Connections | 3 | CON-001–003 |
| Kits | 5 | KIT-001–005 |
| Consoles and sessions | 7 | SES-001–007 |
| Access, accounts, approvals and keys | 9 | ACC-001–009 |
| Relay | 2 | RLY-001–002 |
| CI and triggers | 6 | CI-001–006 |
| Dashboard and operations | 9 | OPS-001–009 |
| Benchmarks and proof | 5 | BEN-001–005 |
| Engineering | 3 | ENG-001–003 |
| Test infrastructure and evidence | 4 | QA-001–004 |
| **Total** | **105** | **98 retained + 7 added** |
