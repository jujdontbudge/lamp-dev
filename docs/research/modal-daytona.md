# Modal Sandboxes and Daytona: how they work

Research for Lamp. Written 2026-11-08. E2B appears only as a short third reference point.

**Method.**
- **Sources:** current official docs (Modal Python SDK 1.6.3, released 2026-10-27; Daytona docs fetched 2026-11-08), official repos and source code, and official blog posts.
- **Complaints:** taken from GitHub issues.
- **Citations:** each claim has a key such as `[M1]` or `[D2]`, which maps to a URL in [Sources](#sources).
- **"Unconfirmed":** I could not verify the point from a primary source.

**Read this first.**

- **Daytona is no longer open source in practice.** In June 2026 it moved core development to a private codebase and archived the GitHub repo. The last open release is v0.190.0, under AGPL-3.0 [D1][D37]. On 2026-06-23 its docs removed the "Open Source Deployment" and on-prem pages, and renamed "cmc" to "BYOC" [D39].
- **Modal does not offer self-hosting.** Only its client SDKs are open source (Apache-2.0) [M28]. I found no self-host option in its docs.
- **The closest existing match to Lamp is E2B Embed.** It is Apache-2.0 and runs the whole E2B stack on one Linux host [E4]. Neither Modal nor Daytona has a maintained self-hosted product.

---

## Part A: Modal Sandboxes

### A1. Core nouns

| Noun | What it is |
|---|---|
| Sandbox | A secure container for untrusted code. Created with `Sandbox.create` [M1]. |
| App | A grouping object. A Sandbox needs one when you create it from outside Modal [M1]. |
| Image | The container filesystem. Defined in code, pulled from a registry, or built from a Dockerfile [M13][M14]. |
| Named Image | An Image published under a name, like a registry tag. Using it never triggers a rebuild [M15]. |
| Filesystem / Directory Snapshot | A copy of the root filesystem, or of one directory. Both are stored as Images [M5]. |
| SandboxSnapshot | A memory snapshot of all processes and memory. Alpha [M5]. |
| Volume / CloudBucketMount | Persistent shared storage you can mount [M4]. |
| Secret | A named set of key/value pairs. Injected as env vars or through an outbound policy [M2][M11]. |
| Tunnel / Connect Token | Ways to reach ports inside a Sandbox [M9]. |
| Sidecar | A separate trusted container on the same host. Beta [M12]. |
| Probe | A readiness check, either TCP or exec [M1]. |
| Workspace / Environment | The org and auth unit, and its subdivisions (for example `dev` and `prod`) [M17][M18]. |
| Token / Service User | An API credential (`MODAL_TOKEN_ID` + `MODAL_TOKEN_SECRET`), and a machine account [M20]. |
| Proxy | A static-IP egress proxy that can sit in front of a Sandbox [M2]. |

### A2. Lifecycle

- **Events.** Created → Scheduled → Started → Ready (only with a probe) → Finished [M1]. There is no stop, start, pause, or archive. A Sandbox runs once.
- **Finish triggers.** Entrypoint exit, `terminate()`, timeout, idle timeout, or OOM. `terminate()` sends SIGKILL, so the exit code is 137 [M1].
- **Timeouts.** Default lifetime is 5 minutes; maximum is 24 hours. For longer work, Modal says to snapshot and restore into a new Sandbox [M1].
- **Idle.** A Sandbox counts as active while an `exec` runs, while stdin is written, or while a Tunnel TCP connection is open [M1].
- **Exec.** `exec()` returns stdin, stdout, and stderr streams, and takes timeout, workdir, env, secrets, and `pty` [M2][M3]. Output stays readable for 10 minutes after the command ends [M3].
- **PTY.** Pass `pty=True` to `exec` or to the entrypoint [M2]. Or run `modal shell sb-...` to attach [M25].
- **Files.** `sandbox.filesystem` can copy files in and out, read and write, list, stat, mkdir, remove, and watch [M2]. Reads are capped at 5 GB [M4]. The old FileIO API was removed in 1.6.0 [M22].
- **Names and tags.** A name is unique among running Sandboxes in a deployed App and must be under 64 characters [M1]. A Sandbox can carry up to 10 tags, which filter `Sandbox.list` [M1]. `from_id` and `from_name` reattach to a Sandbox [M1].
- **Filesystem snapshots** [M5]. A diff from the base image, kept 30 days. New Sandboxes start from one; this is Modal's form of "fork".
- **Directory snapshots** [M5]. Kept 30 days. They can be mounted into a Sandbox that is already running.
- **Memory snapshots** [M5]. Alpha, kept 7 days. Taking one terminates the Sandbox. Not allowed while an exec runs. No GPUs. Must restore onto the same instance type. Cannot pin a region.
- **No snapshot listing.** No API lists your snapshots, so you must track their IDs yourself [M5].
- **Exit snapshots.** Beta. An automatic filesystem snapshot on any exit, including idle timeout and OOM [M6].
- **Logs.** `Sandbox.logs` returns only the entrypoint's logs [M22].

### A3. Defining environments

- **Code builder.** Chained Python methods: `pip_install`, `apt_install`, `run_commands`, `add_local_dir`, `env`, `run_function`. Each method is a cached layer. A change rebuilds every later layer [M13].
- **Existing images.** `Image.from_registry` works with public registries, Docker Hub, ECR, GAR/GCR, and ACR. `Image.from_dockerfile` also works, with notes on `USER`, `ENTRYPOINT`, `ENV`, and `ADD` [M14]. External tags are treated as immutable once pulled [M1].
- **Builds.** Builds run on gVisor [M13]. Modal recommends publishing **named images** from CI, so creating a Sandbox never waits on a build [M1][M15].
- **Speed.** On the v2 backend, the median time from create to running user code is "less than half a second". Scheduling takes "tens of milliseconds" [M23]. A benchmark created 1 million Sandboxes in under a minute [M23].
- **Warm pools.** Not built in. Modal publishes an example that builds one from a Queue and readiness probes [M29].

### A4. Interfaces

| Interface | Status |
|---|---|
| Python SDK | Primary [M21]. |
| JS/TS and Go SDKs | Beta. Now in the `modal-client` monorepo [M21][M28]. |
| Wire API | gRPC (`modal_proto/api.proto`), with calls such as `SandboxCreate`, `SandboxSnapshotFs`, and `SandboxSetOutboundPolicy` [M16][M28]. I found no public REST API (unconfirmed). |
| CLI | No `modal sandbox` command group [M37]. `modal shell <sb-id>` attaches [M25]. `modal container list/exec/logs/stop` [M26]. |
| Dashboard | Yes. Can terminate Sandboxes and shows per-Sandbox metrics [M1][M7]. |
| MCP | I found no first-party Sandbox MCP server (unconfirmed). `modal skills` installs agent skills [M27]. |

### A5. Auth and multi-tenancy

- **Workspaces** hold resources and auth. Members join by invite or through a linked GitHub org [M17].
- **Environments** subdivide a Workspace. Each has its own Secrets, and a Workspace can have up to 1,500 [M18].
- **RBAC** (Team and Enterprise plans) [M19]: Workspace roles: Owner, Manager, Member. Environment roles: Contributor, Viewer, No Access.
- **Service users** (Team and Enterprise) start with No Access to every Environment, and their tokens can expire [M20]. `modal token new --expires-in` sets a token TTL [M22].
- **Inside a Sandbox**, code cannot access other Workspace resources, which limits the "blast radius" [M9]. `include_oidc_identity_token=True` injects a `MODAL_IDENTITY_TOKEN` for federation, for example to AWS [M1].
- **Concurrency limits.** Starter allows 100 containers, Team 5,000, Enterprise custom [M24]. The blog says Modal supports "up to fifty thousand concurrent sandboxes per customer" [M23].
- **Resources and billing.** The default request is 0.125 physical cores and 128 MiB. Sandboxes can burst above it, and you can set a request plus a hard limit [M7][M8]. Billing is `max(request, actual)` at the non-preemptible rate, which is 3× list price [M7][M24].

### A6. Architecture

- **Isolation** [M1][M9]: **gVisor** is the default runtime. It implements syscalls in userspace. **`runtime="vm"`** runs a full VM with its own kernel on KVM. Use it for Docker inside the Sandbox, FUSE, or nested cgroups. GPUs need gVisor. Nested virtualization is Enterprise only.
- **Worker runtime.** Written in Rust [M16].
- **No Kubernetes.** The v2 backend has been the default since 1.6.0 (2026-09-28) [M22]. How it works [M23]: Stateless **scheduling servers** pick a worker with an in-memory load-balancing step. Each worker is its own **source of truth**. It publishes its state to a Redis stream. The scheduler calls the worker directly by RPC. The worker can accept or reject the Sandbox. No data store sits on the create path. Metadata is written asynchronously. The old design used a queue, Postgres, and central coordination. It hit O(sandboxes) and O(nodes) bottlenecks.
- **Create semantics.** In v2, `create()` blocks until the Sandbox is scheduled. If it cannot be placed, it raises `ResourceExhaustedError` [M22].
- **Placement.** `region=` and `cloud=` pin where a Sandbox runs [M2].

### A8. Networking

- **Inbound** is closed by default [M9]. **Connect Tokens** give authenticated HTTP or WebSocket access (default port 8080). The app receives an unspoofable `X-Verified-User-Data` header [M9]. **Tunnels** come as `encrypted_ports`, `h2_ports`, or raw-TCP `unencrypted_ports`, all on `*.w.modal.host` [M2][M10]. **SSH** has no built-in support. You run your own SSH server behind a TCP tunnel [M10]. **Custom domains** need Team or Enterprise and NS delegation [M9]. `inbound_cidr_allowlist` does not cover unencrypted tunnels [M9].
- **Egress** is open by default. The controls are `block_network`, `outbound_cidr_allowlist` (any protocol), and `outbound_domain_allowlist` (beta; matches the TLS SNI, port 443 only) [M9].
- **Egress caveats.** DNS is never filtered unless all traffic is blocked. Domain fronting is not prevented. The policy can change at runtime (alpha), but only if that allowlist type was set at create [M9].
- **Secret injection** (beta) [M11]. An "Outbound Policy" rewrites HTTPS headers per domain, so the secret never enters the Sandbox. It works over HTTP/1.1 only, so gRPC fails. It needs named Secrets and allows at most 25 header values. It cannot be combined with allowlists or memory snapshots, and it does not cover sidecars.

### A9. Agent-specific features

- **Sidecars** (beta) [M12]: Separate the agent harness from where its tools run. Hold credentials in a proxy container. Route HTTPS through a filter such as mitmproxy.
- **Readiness probes and `filesystem.watch()`** help with setup steps [M1][M2].
- **Not built in:** git, LSP, desktop, or browser APIs. None appear in the Sandbox reference (unconfirmed absence) [M2]. Desktop and browser use exist only as examples, such as Browser Use driving Chromium over VNC [M31].
- **Harness integrations:** Cursor Self-Hosted Machines: one Sandbox per Cloud Agent session [M30]. An official repo runs Claude Managed Agents on Modal Sandboxes [M32].

### A10. Known limitations and complaints

- **No stop or resume.** The 24-hour cap means long sessions need a snapshot, then a new Sandbox [M1].
- **Memory snapshots have many limits** [M5].
- **Orphaned processes.** An `exec` timeout kills only the top process. Children keep running (open issue) [M33].
- **VM filesystem snapshots fail early.** They fail far below the disk cap because of a 256 GiB changed-data limit and an undocumented inode limit (open issue) [M34].
- **gRPC transport.** It breaks where gRPC is blocked, such as Claude Code Web [M35] and Cloudflare Workers [M36].
- **Weak domain allowlist.** It covers TLS only, and DNS stays open [M9].

---

## Part B: Daytona

### B1. Core nouns

| Noun | What it is |
|---|---|
| Sandbox | An isolated "composable computer" with its own vCPU, RAM, disk, and network stack [D2]. |
| Sandbox class | `container`, `linux-vm`, `windows`, GPU, and now macOS [D2][D7]. The API enum also has `android` [D53]. |
| Snapshot | A reusable base, made from an image, a Dockerfile, or a sandbox. "Cold" means filesystem only; "hot" adds memory [D3]. |
| Image (declarative) | An image defined in SDK code and built on demand [D4]. |
| Volume | A FUSE mount backed by S3. Can be shared across sandboxes and scoped with `subpath` [D5]. |
| Warm pool | Pre-started sandboxes for one snapshot in one region. Gated feature [D17]. |
| Region / Runner | A group of compute, and a compute node that hosts sandboxes [D6][D20]. |
| Organization | The tenant boundary. Owns sandboxes, snapshots, volumes, and keys [D6][D13]. |
| API key | Belongs to one org and carries scopes [D12]. |
| Secret | An org-scoped credential, swapped in by an egress proxy [D16]. |
| Linked sandbox | An ephemeral child on a private network shared with its parent [D2]. |
| Toolbox API / daemon | The in-sandbox agent for fs, git, process, PTY, LSP, and computer use [D6]. |

### B2. Lifecycle

- **States** [D2]: Creating, Pulling/Building Snapshot, Pending Build, Build Failed, Starting, Started, Stopping, Stopped, Pausing, Paused, Resuming, Archiving, Archived, Restoring, Resizing, Snapshotting, Forking, Deleting, Deleted, Error, Unknown.
- **Operations by class** [D2]:

| Operation | Container | Linux VM | Windows | GPU |
|---|---|---|---|---|
| Start / stop | ✓ | ✓ | ✓ | ✓ |
| Pause / resume (keeps memory) | ✗ | ✓ | ✓ | ✗ |
| Archive to object storage | ✓ | ✗ | ✗ | ✗ |
| Fork (filesystem + memory) | ✗ | ✓ | ✓ | ✗ |
| Snapshot, filesystem only / with memory | ✓ / ✗ | ✓ / ✓ | ✓ / ✓ | ✓ / ✗ |

- **Fork changed after June 2026.** The v0.190.0 docs described fork for sandboxes in general [D54]. The current docs say VM classes only. A parent cannot be deleted while it has fork children [D2].
- **Other operations** [D2]: Force stop. Recover, when the `recoverable` flag is set. Resize: CPU and RAM can grow while running. Disk only grows, and only while stopped. Labels: setting them replaces the whole set.
- **Automatic lifecycle** [D2]: Auto-stop after 15 minutes by default (`0` turns it off). Auto-pause for VMs, after 60 minutes by default. Auto-archive after 7 days stopped. Auto-delete is off by default. A wall-clock TTL (`ttl_minutes`) deletes the sandbox in any state. Ephemeral sandboxes are deleted on stop.
- **"Activity" is narrow** [D2]: It counts preview traffic, SSH sessions, Toolbox API calls, and state changes. Background processes do not count, so a 15-minute LLM job can be stopped mid-run. A "refresh last activity" call exists.
- **Process** [D24][D25]: `code_run` for Python, JavaScript, or TypeScript. Stateless, or a stateful Python context. Shell commands. Background **sessions**. PTY sessions and log streaming.
- **Filesystem** [D26]: list, streamed upload and download, permissions, search, move, and delete.

### B3. Defining environments

- **Snapshots from images** [D3][D6]: Any OCI registry works, including private ones. A tag or digest is required. `latest`, `lts`, and `stable` are rejected. Dockerfiles also work [D4].
- **Declarative builder** [D4]: An SDK fluent API: `Image.base()`, `debian_slim()`, `pip_install()`, `run_commands()`, `add_local_dir()`, `env()`, `workdir()`. Builds are cached on the runner for 24 hours.
- **VM snapshots** cannot be built from a Dockerfile or the builder. They come only from an image reference or a sandbox [D3][D4].
- **Default snapshots** come in small, medium, and large (1 vCPU/1 GiB/3 GiB up to 4/8/10). They ship with **Claude Code, Codex, OpenCode, OpenClaw**, the Claude Agent SDK, and Python/TypeScript language servers preinstalled [D3].
- **Speed.** Container sandboxes are claimed to start in "<90ms" [D1][D2].
- **Warm pools** only claim a sandbox on an exact match: the same snapshot, region, default resources, and user, with no custom env, volumes, or secrets [D17][D32].

### B4. Interfaces

| Interface | Status |
|---|---|
| SDKs | Python, TypeScript, Ruby, Go, Java [D6]. |
| REST | A control-plane API (NestJS, OpenAPI) plus a separate in-sandbox **Toolbox API** (OpenAPI) [D6][D23]. |
| CLI | `daytona create/start/stop/pause/archive/delete/exec/ssh/preview-url/snapshot/volume/organization/mcp` [D22]. |
| MCP | Built into the CLI: `daytona mcp init claude|cursor|windsurf`. Tools cover sandboxes, fs, git, process, computer use, and preview [D21]. |
| Dashboard | Yes, plus a web terminal, VNC, and a playground [D6][D10]. |
| Events | Webhooks such as `sandbox.created` and `sandbox.state.updated`, plus audit logs and OpenTelemetry [D31][D1]. |

### B5. Auth and multi-tenancy

- **Organizations** [D13]: Each user has a personal org. Collaborative orgs add roles (Owner and Member, plus assignments such as Viewer, Developer, and Sandboxes Admin), shared billing, and a shared quota. SSO works over SAML or OIDC [D15]. JWT requests must send `X-Daytona-Organization-ID` [D14].
- **Key scopes.** Examples: `write/delete:sandboxes`, `write/delete:snapshots`, `read/write/delete:volumes`, `manage:secrets`, `read:audit_logs`, plus runner, region, and billing scopes [D12].
- **Gap: runtime access cannot be scoped.** Any valid org key, even one with no scopes, can exec, read and write files, and open previews in every running sandbox in the org. The same is true of org membership [D12][D13].
- **Managed API keys.** A gated feature that mints child keys holding a subset of the parent's scopes, for per-tenant keys [D12].
- **Tiers** [D8]:

| Tier | Requirement | Pool (vCPU / RAM / disk) | Creates per minute |
|---|---|---|---|
| 1 | Email verified | 10 / 10 GiB (page also says 20) / 30 GiB | 300 |
| 2 | $25 top-up | 100 / 200 GiB / 300 GiB | 400 |
| 3 | $500 top-up | 250 / 500 GiB / 2000 GiB | 500 |
| 4 | $2000 top-up every 30 days | 500 / 1000 GiB / 5000 GiB | 600 |

- **Other limits.** The default per-sandbox maximum is 4 vCPU, 8 GB RAM, 10 GB disk [D2]. Stopped containers still use disk quota; archived ones do not [D8]. Responses carry rate-limit headers [D8].

### B6. Architecture

- **Planes.** Daytona splits into interface, control, and compute planes [D6].
- **Control plane** [D6]: A NestJS API backed by Postgres, Redis, and Auth0/OIDC. A **sandbox manager** (scheduling and reconciling) and a **snapshot builder**, both inside the API process. A **proxy** that routes by host: `{port}-{sandboxId}.{domain}`.
- **Compute plane** [D5][D6]: Runners poll the API for jobs: create, start, stop, resize, and backup. Snapshots live in an internal OCI registry on S3. Volumes are S3 mounted through FUSE.
- **Container isolation** [D7]: Namespaces plus cgroup limits. Code runs as root inside. The v0.190.0 security page names **Sysbox** (user namespaces, procfs virtualization) [D43]. The runner installer defaults to `CONTAINER_RUNTIME=sysbox-runc` [D42]. A maintainer confirmed Sysbox for the cloud [D44]. The open runner code sets `Privileged: true` for non-GPU containers [D41].
- **VM isolation.** A full VM with its own kernel. Only VM classes get pause, fork, and hot snapshots [D7]. The hypervisor is not named (unconfirmed).
- **Networking between sandboxes.** None by default. Linked sandboxes are scheduled on their parent's runner [D7].

### B7. Open source and self-hosting

- **License and status.** AGPL-3.0, last open release v0.190.0. The repo is archived and will get no updates, fixes, or releases [D1][D37][D38].
- **Self-host stack at v0.190.0** (Docker Compose) [D38][D53]: API, Proxy, Runner, and SSH Gateway. Postgres, Redis, Dex (OIDC), a Registry, and MinIO (S3). MailDev, Jaeger, and PgAdmin. The repo also holds `snapshot-manager`, `daemon`, `otel-collector`, `dashboard`, and `cli`.
- **Host requirements** (from the removed guide) [D38]: 4 GB RAM minimum, 8 GB recommended. Ubuntu 22.04+, Debian 12+, Fedora 39+, or macOS 13+ with Docker Desktop. A public setup also needs a domain, wildcard DNS, DNS-01 TLS, and ports 80, 443, and 2222.
- **Not production-grade.** The Compose runner is privileged Docker-in-Docker with `RESOURCE_LIMITS_DISABLED=true`, because "cgroups" cannot be partitioned there [D38][D40]. Maintainers called it fit "for local dev and single-tenant evaluation only" [D44].
- **Standalone runner** [D42]: Needs AMD64, systemd, and Docker. Installs via `curl | sudo bash`. The Terraform module targets Ubuntu 22.04+ on EC2.
- **KVM and Kubernetes.** Container runners do not need KVM. `/dev/kvm` is mounted only for Android sandboxes [D41][D38]. VM-class hosts are unconfirmed. Kubernetes is not required. The Helm chart deploys only region services: proxy, snapshot manager, and a registration job [D19][D52]. Users report Kubernetes runner problems [D48].
- **BYOC today.** Your runners connect to Daytona's hosted control plane. Daytona must enable it for your org [D19].
- **Missing from the open code** compared with today's cloud: The v0.190.0 docs have no pages for Secrets, Warm pools, Linux VM, macOS, or wall-clock TTL [D54]. The open runner has only a Docker backend, from my reading of `apps/runner/pkg` [D53]. Everything built after June 2026 is closed.

### B8. Networking

- **Standard preview URL** for any HTTP port: `https://{port}-{sandboxId}.{domain}` plus an `x-daytona-preview-token` header. The token is sandbox-wide: it also opens the Toolbox API (port 2280) and the web terminal (port 22222). It cannot be revoked [D10].
- **Signed preview URL.** Bound to one port. Expires (60 s by default, up to 24 h) and can be revoked [D10].
- **Public sandboxes.** `public: true` turns off preview auth [D10].
- **SSH.** `ssh <token>@ssh.app.daytona.io`. Tokens expire after 60 minutes and can be revoked. Works with VS Code and JetBrains [D11].
- **VPN.** Tailscale or OpenVPN [D30].
- **Egress** [D9]: Choose one: `networkBlockAll`, a `networkAllowList` (up to 10 IPv4 CIDRs), or a `domainAllowList` (up to 100 entries, wildcards allowed). Optionally add an upstream `outboundProxyUrl`. Rules can change while the sandbox runs. Tiers 1–2 are locked to org rules. Tiers 3–4 are open by default, and their allowlists are strict.
- **Secrets proxy** [D16]: The sandbox holds a placeholder. The proxy swaps in the real value only in HTTPS headers, and only for allowed hosts. Responses are scrubbed of the real value. Bodies, query strings, and plain HTTP are never substituted.

### B9. Agent-specific features

- **Git module** [D27]: clone, status, branches, commit, push, and pull. A PAT is passed per operation.
- **LSP** [D28]: in-sandbox language servers. Python and TypeScript come by default.
- **Computer use** [D29]: mouse, keyboard, screenshots, recording, and VNC, on Linux and Windows. macOS is its own class.
- **Harness integrations:** An **OpenCode plugin**: one sandbox per session, synced to local `opencode/` git branches [D33]. Guides for Claude Code, Codex, Gemini CLI, Devin, Kiro, Kimi, Pi, and others [D36]. Container layer for Claude Managed Agents [D34] and for Cursor Self-Hosted pools [D35].
- **Credentials.** The secrets proxy [D16], plus volume `subpath` for per-tenant data [D5].

### B10. Known limitations and complaints

- **Open source ended** in June 2026 [D1].
- **The OSS build was unsafe.** Privileged Docker-in-Docker with no resource limits [D44]. Self-hosted disk usage blows up when sandboxes run inner Docker without Sysbox [D45].
- **Self-hosting friction.** Private registry x509 errors (28 comments) [D49] and Kubernetes runner trouble [D48].
- **Auto-stop is confusing.** Users ask what counts as "inactivity" [D47] and want auto-wake when preview traffic arrives [D46].
- **Errors during state changes.** The Toolbox API returns misleading 502/400 errors mid-transition [D50]. Sandboxes become unreachable under concurrent load on one runner [D51].
- **Features depend on class.** Containers cannot pause or fork. VMs cannot be built from a Dockerfile [D2][D4].
- **Runtime access cannot be scoped** to one sandbox [D12].

---

## Part C: E2B, briefly

- **Isolation and lifecycle.** Each sandbox is a Firecracker microVM [E3]. It can pause and resume with memory intact. Paused sandboxes are kept indefinitely. Continuous runtime is capped at 24 h on Pro and 1 h on Hobby [E1].
- **Templates.** E2B builds a container, boots it as a sandbox, runs the start command, then snapshots the running VM. That snapshot is the "template" [E2].
- **Self-hosting.** The runtime (`e2b-dev/infra`) is Apache-2.0 [E3]: Clients use REST. The API talks gRPC to a per-node orchestrator that drives Firecracker. Egress uses nftables, with domain allow and deny lists that inspect SNI and Host. **E2B Embed** runs the whole stack on one Linux host with KVM, via Compose, Terraform, or Kubernetes [E4].
- **Hosted extras.** Managed BYOC on AWS, GCP, and Azure, and secret injection on egress [E5].

---

## Side by side

| Topic | Modal | Daytona (cloud) |
|---|---|---|
| Isolation | gVisor (default) or KVM VM [M1] | Sysbox containers [D43]; VM classes [D7] |
| Stop / resume | No; runs once, 24 h max [M1] | Stop/start and archive; VMs can pause [D2] |
| Fork | New Sandboxes from a filesystem snapshot [M5] | Memory fork, VM classes only [D2] |
| Memory snapshot | Alpha; kept 7 days; terminates the source [M5] | Hot snapshot, VM classes only [D3] |
| Image definition | Python builder, registry, Dockerfile [M13][M14] | SDK builder, registry, Dockerfile [D4] |
| API style | gRPC and SDKs [M28] | REST plus Toolbox REST [D23] |
| Ingress | Connect tokens, tunnels [M9] | Preview URLs, SSH, VPN [D10][D11][D30] |
| Egress control | Block, CIDR, SNI domain list [M9] | Block, CIDR, domain list, upstream proxy [D9] |
| Secret injection | Header rewrite (beta) [M11] | Placeholder swap in headers [D16] |
| Agent extras | Sidecars [M12] | git, LSP, computer use, MCP [D21][D27][D28][D29] |
| Self-host | None | Archived AGPL code; BYOC runners [D1][D19] |

## Patterns worth copying, and gaps Lamp could fill

Patterns worth copying:

- **Two isolation tiers.** A fast default (gVisor or Sysbox containers) plus a VM class for Docker, FUSE, pause, and fork. Both vendors ended up here [M1][D7].
- **Secrets stay out of the sandbox.** An egress proxy injects credentials into HTTPS headers, for allowlisted hosts only. Both shipped this [M11][D16]. It fits agent API keys and git tokens.
- **Egress policy as data.** Block-all, CIDR, and domain lists, set at create and changeable at runtime [M9][D9].
- **Builds stay off the create path.** Named images and pre-built snapshots mean create never waits on a build [M15][D3].
- **Explicit TTLs.** Idle timeout, wall-clock TTL, auto-stop, auto-archive, and auto-delete [M1][D2].
- **Readiness probes.** TCP or exec checks, plus `wait_until_ready` [M1].
- **Two kinds of ingress credential.** Expiring, revocable per-port share links, kept separate from the owner credential [D10][M9].
- **One in-sandbox daemon.** fs, process, PTY, and git served from a single API [D6].
- **Base snapshots with the harnesses installed.** Claude Code, Codex, and OpenCode come preinstalled [D3].
- **Tags and labels.** List by tag, and use names for per-session idempotency [M1][D2].
- **Workers are the source of truth.** No data store sits on the create path [M23].

Gaps Lamp could fill:

- **Maintained, permissive, self-hosted.** Daytona's open code is archived [D1]. Modal does not offer self-hosting. Only E2B Embed is in this space [E4].
- **Single-machine first.** Daytona's open setup needed a domain, wildcard DNS, DNS-01, and open ports [D38]. A home box behind CGNAT needs a tunnel-first design.
- **Safe isolation on one box.** Daytona's open runner was privileged Docker-in-Docker with no limits [D40][D44].
- **Per-sandbox credentials.** Daytona cannot scope runtime access to one sandbox [D12]. Lamp could issue a token per sandbox or per agent.
- **Clear activity rules.** A running agent process should count as activity, or traffic should wake a sandbox. Both are open complaints [D2][D46][D47].
- **Stop and resume for plain containers.** Daytona containers cannot pause or fork [D2]. Modal cannot stop at all [M1].
- **First-class MCP and REST.** Modal has neither first-party (unconfirmed), and its gRPC is blocked in some sandboxed clients [M35][M36].
- **Kill the whole process group** when an exec times out [M33].
- **Snapshot listing and lifecycle APIs.** Modal cannot list your snapshots [M5].

---

## Sources

Modal:
- [M1] https://modal.com/docs/guide/sandboxes
- [M2] https://modal.com/docs/sdk/py/latest/Sandbox
- [M3] https://modal.com/docs/guide/sandbox-spawn
- [M4] https://modal.com/docs/guide/sandbox-files
- [M5] https://modal.com/docs/guide/sandbox-snapshots
- [M6] https://modal.com/docs/guide/sandbox-exit-snapshots
- [M7] https://modal.com/docs/guide/sandbox-resources
- [M8] https://modal.com/docs/guide/resources
- [M9] https://modal.com/docs/guide/sandbox-networking
- [M10] https://modal.com/docs/guide/tunnels
- [M11] https://modal.com/docs/guide/sandbox-secret-injection
- [M12] https://modal.com/docs/guide/sandbox-sidecars
- [M13] https://modal.com/docs/guide/images
- [M14] https://modal.com/docs/guide/existing-images
- [M15] https://modal.com/docs/guide/named-images
- [M16] https://modal.com/docs/guide/security
- [M17] https://modal.com/docs/guide/workspaces
- [M18] https://modal.com/docs/guide/environments
- [M19] https://modal.com/docs/guide/rbac
- [M20] https://modal.com/docs/guide/service-users
- [M21] https://modal.com/docs/guide/sdk-javascript-go
- [M22] https://modal.com/docs/sdk/py/releases
- [M23] https://modal.com/blog/scaling-to-1-million-concurrent-sandboxes-in-seconds (2026-07-16)
- [M24] https://modal.com/pricing
- [M25] https://modal.com/docs/cli/latest/shell
- [M26] https://modal.com/docs/cli/latest/container
- [M27] https://modal.com/docs/cli/latest/skills
- [M28] https://github.com/modal-labs/modal-client (LICENSE, `modal_proto/api.proto`)
- [M29] https://modal.com/docs/examples/sandbox_pool
- [M30] https://modal.com/docs/examples/cursor
- [M31] https://modal.com/docs/examples/computer_use_vnc
- [M32] https://github.com/modal-labs/claude-managed-agents-modal-sandbox
- [M33] https://github.com/modal-labs/modal-client/issues/3992
- [M34] https://github.com/modal-labs/modal-client/issues/4139
- [M35] https://github.com/modal-labs/modal-client/issues/4113
- [M36] https://github.com/modal-labs/modal-client/issues/4124
- [M37] https://modal.com/llms.txt (CLI command index)

Daytona (current docs fetched 2026-11-08; `v0.190.0` links are the last open release):
- [D1] https://github.com/daytonaio/daytona (README maintenance notice; repo archived)
- [D2] https://www.daytona.io/docs/en/sandboxes
- [D3] https://www.daytona.io/docs/en/snapshots
- [D4] https://www.daytona.io/docs/en/declarative-builder
- [D5] https://www.daytona.io/docs/en/volumes
- [D6] https://www.daytona.io/docs/en/architecture
- [D7] https://www.daytona.io/docs/en/isolation
- [D8] https://www.daytona.io/docs/en/limits
- [D9] https://www.daytona.io/docs/en/network-limits
- [D10] https://www.daytona.io/docs/en/preview
- [D11] https://www.daytona.io/docs/en/ssh-access
- [D12] https://www.daytona.io/docs/en/api-keys
- [D13] https://www.daytona.io/docs/en/organizations
- [D14] https://www.daytona.io/docs/en/authentication
- [D15] https://www.daytona.io/docs/en/sso
- [D16] https://www.daytona.io/docs/en/secrets
- [D17] https://www.daytona.io/docs/en/warm-pools
- [D19] https://www.daytona.io/docs/en/bring-your-own-compute
- [D20] https://www.daytona.io/docs/en/regions
- [D21] https://www.daytona.io/docs/en/mcp
- [D22] https://www.daytona.io/docs/en/tools/cli
- [D23] https://www.daytona.io/docs/en/tools/api
- [D24] https://www.daytona.io/docs/en/process-code-execution
- [D25] https://www.daytona.io/docs/en/pty
- [D26] https://www.daytona.io/docs/en/file-system-operations
- [D27] https://www.daytona.io/docs/en/git-operations
- [D28] https://www.daytona.io/docs/en/language-server-protocol
- [D29] https://www.daytona.io/docs/en/computer-use
- [D30] https://www.daytona.io/docs/en/vpn-connections
- [D31] https://www.daytona.io/docs/en/webhooks
- [D32] https://www.daytona.io/docs/en/troubleshooting
- [D33] https://www.daytona.io/docs/en/guides/opencode/opencode-plugin
- [D34] https://www.daytona.io/docs/en/guides/claude/claude-managed-agents
- [D35] https://www.daytona.io/docs/en/guides/cursor/cursor-self-hosted-machines
- [D36] https://www.daytona.io/docs/llms.txt (guides index)
- [D37] https://github.com/daytonaio/daytona/blob/v0.190.0/LICENSE
- [D38] https://github.com/daytonaio/daytona/blob/4ee2c6365b851cbc7073ca4cea2f9ebba0caf832/apps/docs/src/content/docs/en/oss-deployment.mdx (removed guide, last version)
- [D39] https://github.com/daytonaio/daytona/commit/a0ec3faa4e7df75ec8ff7e2384a99feff308fe20
- [D40] https://github.com/daytonaio/daytona/blob/v0.190.0/docker/docker-compose.yaml#L129-L151
- [D41] https://github.com/daytonaio/daytona/blob/v0.190.0/apps/runner/pkg/docker/container_configs.go#L180-L241
- [D42] https://github.com/daytonaio/daytona/blob/v0.190.0/apps/docs/src/content/docs/en/bring-your-own-compute.mdx?plain=1#L826-L888
- [D43] https://github.com/daytonaio/daytona/blob/v0.190.0/apps/docs/src/content/docs/en/security-exhibit.mdx?plain=1#L96-L103
- [D44] https://github.com/daytonaio/daytona/issues/3774
- [D45] https://github.com/daytonaio/daytona/issues/4680
- [D46] https://github.com/daytonaio/daytona/issues/2147
- [D47] https://github.com/daytonaio/daytona/issues/2007
- [D48] https://github.com/daytonaio/daytona/issues/4232
- [D49] https://github.com/daytonaio/daytona/issues/2971
- [D50] https://github.com/daytonaio/daytona/issues/4142
- [D51] https://github.com/daytonaio/daytona/issues/5137
- [D52] https://github.com/daytonaio/helm-charts
- [D53] https://github.com/daytonaio/daytona/tree/v0.190.0/apps (components; `apps/api/src/sandbox/enums/sandbox-class.enum.ts`)
- [D54] https://github.com/daytonaio/daytona/tree/v0.190.0/apps/docs/src/content/docs/en (docs pages at the last open release)

E2B:
- [E1] https://docs.e2b.dev/sandbox
- [E2] https://docs.e2b.dev/template/how-it-works
- [E3] https://github.com/e2b-dev/infra
- [E4] https://github.com/e2b-dev/infra/tree/main/embed
- [E5] https://docs.e2b.dev/llms.txt (BYOC, network and secret-injection pages)
