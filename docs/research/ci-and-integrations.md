# CI runners and external sandbox integrations

Research date: 2026-11-08. Sources are official documentation, official repositories, and vendor announcements. This is a capability catalog, not a tested Lamp compatibility list. Lamp's description in the research brief is an assumption; no Lamp runtime was tested.

Scope: all requested systems are below, followed by other useful runner families. No finite list can cover every CI product or user-defined trigger. The scenario catalog groups the recurring execution needs so new integrations can be mapped onto them. **Unconfirmed** means the cited material does not establish the feature. **Proposed** means a Lamp design recommendation or adapter opportunity, not an existing integration.

## Decisions for Lamp

1. **Proposed: separate the sandbox API from CI adapters.** Support agent-in-sandbox, external executor/provisioner, and workflow-step API calls. GitLab, Jenkins, Tekton, and Argo expose different extension contracts; one universal runner protocol would not cover them. [G2][g2] [J2][j2] [T1][t1] [A1][a1]
2. **Proposed: make one job per fresh VM the default isolation mode.** Agent exit does not necessarily delete a machine or revoke its registration. The provisioner must do both. GitLab provides explicit instance-use limits; CircleCI and Azure provide single-job process modes. [G3][g3] [C1][c1] [Z2][z2]
3. **Proposed: prioritize Linux shell and Docker execution, then GitLab Fleeting and Jenkins cloud adapters.** GitLab Custom is usable but is in maintenance mode. Kubernetes products need an actual Kubernetes integration or remote task adapter, not just a Docker-image importer. [G2][g2] [G3][g3] [J2][j2] [T1][t1]
4. **Proposed: expose capabilities, not just operating-system labels.** Native macOS/Xcode, native Windows, nested KVM, GPU, and physical devices need separate execution pools or backends. Installing Lamp on an OS does not establish support for that guest OS. [P1][p1] [P2][p2] [P3][p3] [P4][p4]
5. **Proposed: keep durable caches and artifacts outside disposable roots.** Separate mutable dependency caches, content-addressed build caches, immutable artifacts, and resumable workspace snapshots. Existing systems implement these at different layers. [G5][g5] [C3][c3] [TC3][tc3] [D2][d2]
6. **Proposed: treat chat, tickets, webhooks, and schedules as front ends to the same session API.** Existing agent products already use these triggers. A trigger adapter should authenticate the user, choose a template, start or resume work, and report progress. [I1][i1] [I2][i2] [I3][i3] [I4][i4] [I5][i5]

## Part 1: CI systems

“Fresh” below distinguishes a new process, container/pod, and machine. Only a fresh machine satisfies one-job-per-fresh-machine isolation. For every system, **Proposed** Lamp adapters must implement readiness, cancellation, log export, result reporting, deregistration, and orphan cleanup.

### Registration, lifetime, autoscaling, and routing

| System | Registration and auth | Fresh machine per job? | Autoscaling hook or extension; routing | Proposed Lamp attachment |
| --- | --- | --- | --- | --- |
| GitLab CI / Runner | Create a runner at project, group, or instance scope. Register the manager with its runner authentication token. Job tokens and registry/cache credentials are separate. [G1][g1] [G4][g4] | Yes: Instance or Docker Autoscaler with `capacity_per_instance=1` and `max_use_count=1`. Kubernetes gives fresh job pods, not fresh nodes. [G3][g3] [G6][g6] | Fleeting provider plugins manage instance groups; Taskscaler manages demand. [G7][g7] Custom drivers expose `config_exec`, `prepare_exec`, `run_exec`, `cleanup_exec`, but Custom is in maintenance mode. Jobs match runner tags and scope/protected-runner rules. [G2][g2] [G3][g3] [G4][g4] | Fleeting plugin plus a supported remote connection; alternatively Custom driver or a normal runner inside each sandbox. |
| Buildkite | Agent starts with an agent token for its cluster. API/scaler tokens and source-control credentials are separate. [B1][b1] [B2][b2] | `--disconnect-after-job` ends an agent after one job; external teardown makes the VM disposable. Kubernetes stack creates job pods. [B1][b1] [B3][b3] | Elastic CI Stack for AWS scales an EC2 Auto Scaling group. Agent Stack for Kubernetes watches scheduled jobs and creates Kubernetes Jobs. Agent hooks/plugins customize execution. Route with cluster queues and agent tags. [B1][b1] [B2][b2] [B3][b3] | Start single-job agents from a queue-aware controller; AWS stack is not a generic Lamp provisioner. |
| CircleCI | Create namespace/resource class and its resource-class token; runner polls CircleCI. Machine runner 3 installs on the machine; container runner controls Kubernetes task pods. [C1][c1] [C2][c2] | Machine runner `runner.mode: single-task` stops after a task; service restarts can reuse the same VM unless teardown is wired. Container runner gives fresh task pods. [C1][c1] [C2][c2] | Runner API reports queue demand. Machine Runner Orchestrator scales KubeVirt VM pools; a custom Lamp controller would need separate provisioning. Route using `resource_class`. [C4][c4] [C5][c5] | Machine runner 3 in each sandbox; own API-based scaler. Do not assume KubeVirt controller can provision Lamp. |
| Jenkins | Controller creates agent nodes. Inbound Remoting/WebSocket agents use node secrets; SSH launchers use configured SSH credentials. Cloud provider credentials stay with the controller/plugin. [J1][j1] [J3][j3] | Yes with appropriate cloud retention: Kubernetes plugin stops agent pods after builds; GCE plugin supports one-shot VM agents. One executor per VM avoids concurrent jobs during teardown. [J1][j1] [J4][j4] | Implement Jenkins `Cloud` provisioning/retention extension or use an existing cloud plugin. Queue placement uses labels and executor availability. [J1][j1] [J2][j2] | Lamp cloud plugin provisions VM, creates inbound node, supplies secret, and destroys node/VM on completion. |
| Azure Pipelines | Configure agent against organization URL and agent pool using supported registration credentials, such as PAT or service principal. After registration it uses listener/job OAuth tokens. [Z1][z1] [Z2][z2] | `run.sh --once` accepts one job. VM Scale Set pools can reimage after each job; this is not supported for Windows client images. [Z2][z2] [Z3][z3] | VMSS integration scales Azure VM Scale Sets; Managed DevOps Pools are another managed option. Neither is a generic arbitrary-host plugin. Route with pools, capabilities, and demands. [Z1][z1] [Z3][z3] | Self-hosted single-job agents plus custom controller. Keep parallel-job licensing/entitlements in capacity planning. [Z1][z1] |
| Bitbucket Pipelines | Repository/workspace runner setup supplies runner UUIDs and OAuth client credentials. Autoscaler separately uses scoped API token or OAuth credentials to manage runners. [BB1][bb1] [BB2][bb2] | Fresh step containers are normal for Linux Docker. A documented universal one-job runner/VM flag is **unconfirmed**. Kubernetes autoscaling alone does not establish one-job VM isolation. [BB1][bb1] [BB2][bb2] | Official Kubernetes autoscaler polls Runner API and creates/deletes Linux runner workloads. `runs-on` matches all labels including `self.hosted`. No online match can fail a step, so scale-to-zero needs care. [BB2][bb2] [BB3][bb3] | Runner manager/controller with label-specific warm capacity. Prove job-completion teardown before advertising ephemeral VMs. |
| Gitea Actions / act_runner | Register with instance URL and repository/org/instance registration token. Stored runner credentials then authenticate the runner. [F1][f1] | Yes: register `--ephemeral` or use `GITEA_RUNNER_EPHEMERAL=1`; provisioner still deletes the VM. [F1][f1] | Labels map workflow `runs-on` to `docker://image` or host execution. External registration/API automation can provision runners; a first-party generic Lamp autoscaler is **unconfirmed**. [F1][f1] | Fresh ephemeral runner and Docker daemon inside VM. Pin runner/server versions: newer docs also use the name Gitea Runner. [F2][f2] |
| Forgejo Actions / Forgejo Runner | Uses Forgejo's runner identity/token. Current docs support server CLI/API registration; older interactive runner registration is deprecated. Do not equate current Forgejo Runner with Gitea act_runner. [F3][f3] | Server-enforced ephemeral registration allows at most one job. Use `forgejo-runner one-job`; daemon is not the ephemeral execution command. [F3][f3] [F4][f4] | Server CLI/API can create ephemeral registrations. Runner labels select host or container environments. A built-in general-purpose Lamp scaler is **unconfirmed**. [F3][f3] [F4][f4] | External provisioner plus one-job runner; version-specific registration adapter. |
| Woodpecker CI | Agent connects to server gRPC using a system token or agent-specific token; registration records its identity. Enable secure gRPC explicitly. [W1][w1] | Current agent documents shutdown after one workflow; it forces concurrency to one. Deleting the VM remains external. [W1][w1] | Separate autoscaler with provider implementations; official docs call it not feature-complete. Route by workflow/agent labels. Labels are self-reported and are not an authorization boundary. [W1][w1] [W2][w2] | Agent-in-VM plus provider/controller integration. Pin release containing one-shot behavior. |
| Drone | Runner polls server with `DRONE_RPC_HOST`, protocol and shared `DRONE_RPC_SECRET`. Autoscaler needs server API credentials and provider credentials separately. [DR1][dr1] [DR2][dr2] | Docker gives fresh containers. VM runner explicitly creates and terminates a dedicated VM per pipeline, but its docs still mark it release-candidate/experimental. A universal one-job-only flag across runner types is **unconfirmed**. [DR1][dr1] [DR3][dr3] | Standalone autoscaler polls queue and manages cloud servers; VM runner uses provider/pool drivers. Route by pipeline type/platform and node labels. Treat runner/scaler generation as version-specific. [DR2][dr2] [DR3][dr3] [DR4][dr4] | VM driver or agents in sandboxes. Existing cloud autoscaler is not automatically a Lamp plugin. |
| TeamCity | Install agent with server URL; normal agents require authorization. Cloud-profile agents are automatically authorized subject to agent licenses. Cloud provider auth is configured in the profile. [TC1][tc1] [TC2][tc2] | Cloud profiles can terminate after first build; exact options depend on provider plugin. One agent runs one build at a time. [TC1][tc1] [TC4][tc4] | Cloud integration/plugin API provisions matching cloud images. Newer docs also describe agentless Kubernetes executors. Route by agent pools, build requirements and reported parameters. [TC2][tc2] [TC5][tc5] | Cloud plugin provisioning a unique agent identity and disposable VM; account for agent license limits. |
| Tekton | Kubernetes controllers watch `TaskRun`/`PipelineRun`; no conventional runner registration. Submitter/controller use Kubernetes RBAC. Task service accounts, Git/registry Secrets and workload identity govern task access. [T2][t2] [T3][t3] | New TaskRun pod, not necessarily a fresh node/VM. Fresh VM requires runtime integration or a remote custom task. [T2][t2] | Kubernetes scheduling via pod templates, selectors, affinity and tolerations. `CustomRun` lets a custom controller execute remote work and report status/results/cancellation. Cluster scaling is separate. [T1][t1] [T4][t4] | Implement CustomRun controller calling Lamp, or a compatible Kubernetes runtime layer. |
| Argo Workflows | Kubernetes Workflow CRs and controller, not registered runner agents. Workflow service accounts/RBAC and storage credentials control execution. [A2][a2] | New task pods, not fresh nodes. A custom executor plugin could request disposable Lamp VMs. [A1][a1] [A2][a2] | Kubernetes placement plus workflow parallelism/limits. Executor plugins implement remote template execution through an HTTP contract; ordinary container templates still follow Argo's pod contract. [A1][a1] | Executor plugin for explicit Lamp templates, or ordinary step calling Lamp SDK. |
| Dagger | CLI/SDK connects to an engine; it is not a traditional registered worker agent. Current v1.0 beta docs expose `--engine`/`DAGGER_ENGINE` with SSH/TLS/container/Kubernetes transports. Plain TCP has no auth. [D1][d1] | Can start a new engine in each VM; persistent engines preserve cache. No CI queue-driven machine lifecycle follows merely from using Dagger. [D1][d1] [D2][d2] | Caller or external CI schedules execution; select remote engine explicitly. Engine runs a dependency graph and service containers. [D1][d1] [D2][d2] | Run version-matched engine in Lamp and return authenticated endpoint. Preserve engine cache separately if disposable. |
| Earthly | Legacy CLI/remote BuildKit execution is still relevant to installed users. Official announcement ended active OSS maintenance and shut Cloud/Satellites, including self-hosted/BYOC offerings, on 2025-07-16. [E1][e1] [E2][e2] | Could wrap legacy local builds in fresh VMs; no current supported Satellite lifecycle should be assumed. [E1][e1] | Treat as legacy build-tool compatibility, not an actively supported cloud runner integration. New maintenance/fork guarantees are **unconfirmed**. [E1][e1] [E2][e2] | Low priority: pinned CLI/BuildKit image; own lifecycle, network auth and cache management. |
| Google Cloud Build private pools | Create managed WorkerPool via Google API/IAM; grant pool-use permission to submitter and select build service account. No bring-your-own Lamp worker registration is documented. [GC1][gc1] [GC2][gc2] | Each build has an isolated worker; private pool infrastructure is managed by Google. This is not a customer VM provisioning hook. [GC1][gc1] | Route with `options.pool.name`; Google scales private pools. Customer VPC connectivity is supported. [GC1][gc1] [GC2][gc2] | A build step can call Lamp's API; replacing the private-pool worker is **unconfirmed**. |
| AWS CodeBuild | Create build project and IAM service role; caller uses AWS API/IAM. Supports custom build images and managed compute/fleets. No arbitrary-host Lamp worker registration is documented. [AWS1][aws1] [AWS2][aws2] | Managed isolated build environments; reuse/local caches vary by compute mode. Fresh physical machine for every build is **unconfirmed**. [AWS1][aws1] [AWS3][aws3] | StartBuild, CodePipeline and webhooks drive managed builds. Project/fleet selects execution configuration. CodeBuild-hosted GitHub runners are managed CodeBuild compute, not a Lamp worker API. [AWS1][aws1] [AWS4][aws4] | Invoke Lamp from a build step or orchestration service; do not promise replacement workers. |
| GitHub Actions, comparison only | Registration/JIT configuration through GitHub API; GitHub App/PAT controls provisioning, runner/job tokens serve execution. [GH1][gh1] | Ephemeral runners accept one job; machine destruction is external. ARC provides Kubernetes scaling. [GH1][gh1] | Labels/groups and `runs-on`; workflow-job events/API and ARC. Copilot coding-agent infrastructure is a separate product contract. [GH1][gh1] [GH2][gh2] | Covered by separate research; reuse common lifecycle/storage primitives. |

### Extension contracts that need real adapters

| Interface | Concrete contract and proposed work |
| --- | --- |
| GitLab Custom | Driver programs handle configuration, prepare, repeated script stages, and cleanup. Carry build identity across invocations and preserve runner exit-code semantics. It is not simply one shell command. [G2][g2] |
| GitLab Fleeting | Provider interface covers instance-group initialization, updates, capacity increase/decrease and connection information; the manager decides demand. Proposed Lamp plugin must report stable instance IDs, readiness and reachable connections, not just implement create/delete. [G7][g7] [G9][g9] |
| Jenkins Cloud | Provision agents from queue/label demand; return nodes that connect to the controller and implement retention/termination. Proposed plugin needs orphan reconciliation after controller restart. [J2][j2] [J1][j1] |
| Tekton CustomRun | Custom controller watches its task kind, updates conditions/results, and implements cancellation, retries/timeouts and any remote workspace transfer. A remote resolver only resolves task definitions; it is not an execution backend. [T1][t1] [T7][t7] |
| Argo executor plugin | Opt-in plugin sidecar receives HTTP POST `/api/v1/template.execute`, authenticates the bearer token, returns node phase/outputs and can request requeue while remote work runs. Proposed adapter stores sandbox ID idempotently and separately reconciles canceled/orphaned workflows. [A1][a1] |
| Queue-based agent bootstrap | Buildkite, CircleCI, Azure and other agents can run in a VM without a native Lamp plugin. Single-task process exit must trigger VM destruction, credential revocation and registration cleanup. Ensure service managers do not restart a completed worker. [B1][b1] [C1][c1] [Z2][z2] |

### Cache, artifacts, and container execution

The agent must retain each CI system's native cache/artifact protocol. **Proposed:** Lamp supplies disks, file transfer, network and credentials underneath it, rather than silently substituting an incompatible cache API.

| System | Caches and artifacts | Docker and service-container requirements |
| --- | --- | --- |
| GitLab | Local runner caches or distributed S3/GCS/Azure caches; job artifacts upload to GitLab. Cache keys and protected-branch separation matter. [G5][g5] | Shell, SSH, Docker, Kubernetes, Instance and VM executors have different feature sets. Docker Autoscaler needs Docker in provisioned instances and supports Docker executor services. Custom driver must supply missing runtime behavior itself. Docker Machine is deprecated; scheduled removal is 20.0 in May 2027. [G8][g8] [G6][g6] [G3][g3] |
| Buildkite | Checkout/local disk reuse, external cache plugins or Buildkite Cache; artifacts use agent upload/download and configured storage. Ephemeral machines need cache restoration. [B4][b4] [B5][b5] | Shell is enough for ordinary commands. Docker/Compose plugins need a daemon. Kubernetes stack uses pods and has its own plugin/runtime compatibility limits. [B3][b3] [B6][b6] |
| CircleCI | Native dependency caches, workspaces and artifact steps work with self-hosted runners. Hosted Docker layer caching is not a generic self-hosted feature. [C3][c3] | Machine runner needs an installed daemon for Docker commands. Container runner uses Kubernetes containers/services. `setup_remote_docker` is unsupported on self-hosted runners; provide Docker yourself. [C2][c2] [C3][c3] |
| Jenkins | Local workspaces, tool caches and plugin-managed storage; archive artifacts/stash transfer via Jenkins or configured artifact manager. No single universal dependency-cache backend. [J5][j5] [J6][j6] | Shell agent needs JVM/Remoting and tools. Docker Pipeline needs daemon access. Kubernetes pod templates can carry service sidecars; shared persistent volumes need tenant isolation. [J1][j1] [J6][j6] |
| Azure Pipelines | Pipeline Cache tasks restore/save keyed caches; pipeline/build artifacts are separate. Self-hosted workspace reuse is not a clean-machine guarantee. [Z4][z4] [Z1][z1] | Container jobs and service containers need supported host/guest combinations and Docker. macOS agents do not support container jobs. [Z5][z5] |
| Bitbucket | Dependency caches/artifacts use Pipelines features; current runner docs also expose custom S3/GCS storage options. Shell and Docker runners differ. Do not assume cloud Runtime v3 Docker-cache behavior applies to every self-hosted runner version. [BB3][bb3] [BB4][bb4] | Linux Docker uses daemon and build/service containers; custom DinD services are supported. macOS, Windows and Linux shell runners run directly on their OS and have feature limitations. [BB1][bb1] [BB3][bb3] |
| Gitea | act_runner exposes a cache server reachable from job containers; durable cache directory/external cache endpoint must outlive disposable workers. Artifacts belong to the Actions service. [F1][f1] [F2][f2] | Docker labels use Docker; host labels run directly. Container socket access can expose registration credentials; keep daemon and job inside the same disposable trust boundary. [F1][f1] |
| Forgejo | Runner cache service stores cache outside Forgejo itself; preserve a dedicated cache service/disk for ephemeral runners. [F5][f5] | Docker-backed labels support job/service containers; host mode exposes host state and runner secrets. Rootless/daemon settings must match actions' needs. [F4][f4] [F5][f5] |
| Woodpecker | Cache/artifact behavior is usually plugin/storage-specific; official plugin catalog includes S3 cache, sccache and artifact publishing. [W3][w3] | Docker, Kubernetes and local backends have distinct support. Services are workflow containers; Docker backend needs a daemon. Local execution is not container isolation. [W4][w4] |
| Drone | Cache plugins persist workspace files to external storage; Docker image caches are runner-host state. Artifacts usually use publishing plugins. [DR5][dr5] | Docker runner needs daemon/socket and supports service steps. Exec runs on host without isolation; SSH and VM runners are separate contracts. Privileged plugins require explicit trust/configuration. [DR1][dr1] [DR3][dr3] [DR6][dr6] |
| TeamCity | Agent checkout/tool caches plus Build Cache feature, which publishes hidden cache artifacts. Ordinary artifacts and artifact dependencies move build outputs. [TC3][tc3] [TC6][tc6] | Native agents run host tools. Container Wrapper/Docker integration requires available Docker/Podman as documented; VM isolation does not create a container daemon automatically. [TC7][tc7] |
| Tekton | Workspaces bind emptyDir, PVCs or other volume sources; persistent cache strategy is task-defined. Results are metadata, not a bulk artifact store. Artifact upload needs tasks/storage integration. [T3][t3] [T5][t5] | Steps run in a pod, services as sidecars. DinD/BuildKit needs appropriate mounts, networking and privileges. CustomRun must implement its own remote file/volume contract. [T1][t1] [T2][t2] |
| Argo Workflows | Artifact repositories such as S3 plus volume/PVC workflows; memoization caches template outputs in ConfigMaps and is not a general compiler-cache service. [A3][a3] [A4][a4] | Container, script and sidecar/daemon templates use Kubernetes. DinD requires a suitable privileged sidecar. Remote executor plugins must explicitly transfer outputs/artifacts. [A1][a1] [A5][a5] |
| Dagger | Engine caches operations and cache volumes hold package-manager state; fresh engine storage loses these unless preserved/shared. File/directory/image outputs are explicit graph exports. [D2][d2] [D3][d3] | Engine executes containers and services; it needs supported container/runtime privileges. Docker compatibility for arbitrary pipeline commands is a separate requirement from Dagger's API. [D1][d1] [D3][d3] |
| Earthly | Legacy BuildKit layer/cache state and exported images/artifacts; Cloud-backed services were removed in 0.8.16. [E2][e2] | Legacy CLI builds need compatible BuildKit/runtime. Do not use historical Satellite docs as proof of an operating cloud service. [E1][e1] [E2][e2] |
| Cloud Build | Persist dependencies via Cloud Storage or image-layer caching. `/workspace` and named volumes share data between steps of one build, not a guaranteed cross-build worker disk. [GC3][gc3] [GC4][gc4] | Build steps are container images; Docker builder commands run in managed environment. A custom step can be a Lamp client. [GC4][gc4] |
| CodeBuild | S3 or local source/Docker-layer/custom caches. Local cache reuse is conditional; layer caching needs Linux and privileged mode. Artifacts commonly go to S3. [AWS3][aws3] [AWS2][aws2] | Custom Docker image does not mean custom host. Docker builds may need privileged mode; runtime/OS/compute-mode restrictions apply. [AWS2][aws2] [AWS3][aws3] |
| GitHub, comparison | Native cache/artifact actions need compatible endpoints/tokens; ephemeral workers still need remote persistence. [GH3][gh3] | Container jobs and services need Linux runner with Docker; native OS jobs use their native tools. [GH4][gh4] |

### Other runner families worth mapping

These extend the discovery perimeter. They are not full compatibility certifications; omitted ephemeral/cache/auth details remain **unconfirmed** until an adapter is selected.

| System/family | Official mechanism and implication |
| --- | --- |
| Bamboo | Remote agents, EC2 elastic agents, and Kubernetes ephemeral agents. Ephemeral agents perform one build/deployment. Candidate: agent in Lamp; Kubernetes templates are not a Lamp provisioning API. [O1][o1] |
| Concourse | Workers register through SSH TSA; containerd/Guardian runtime and Baggageclaim manage execution and volumes/caches. Worker tags select placement. Candidate: worker in VM, or a task calling Lamp; task isolation does not establish one-VM-per-job. [O2][o2] |
| Zuul / Nodepool | Nodepool supplies static/cloud nodes through provider drivers; Zuul connects to nodes for jobs. Candidate: provider driver, with explicit node labels and SSH/network reachability. [O3][o3] |
| Semaphore | Self-hosted agent types, registration credentials, and an agent API. Candidate: agent in VM and a type-aware scaler. [O4][o4] |
| Any remaining CLI-capable CI | **Proposed:** supply a Lamp CLI/SDK step with explicit lifecycle and callbacks. This covers custom scripts without claiming a native runner integration. n8n's generic HTTP action illustrates the same adapter pattern outside CI. [I6][i6] |

## Part 2: sandbox requirements for CI job types

The “Lamp must provide” column is a proposed capability contract. Sources establish the workload behavior and existing implementation examples, not current Lamp support.

| Job type | Lamp must provide | Boundary or acceptance check |
| --- | --- | --- |
| Shell, lint, unit tests, package builds | Command execution with exit status, working directory, environment, user/UID, streamed logs, cancellation, CPU/RAM/disk limits and toolchain image. | Agent binaries need their OS/runtime dependencies, not just a shell. Test cleanup after failure and cancellation. [G6][g6] [J3][j3] |
| Container jobs / Docker actions | Guest container runtime/daemon; image pulling and private-registry auth; mounts, writable workspaces, DNS and process cleanup. | Booting a microVM from an OCI image does not itself implement Docker's job-container API. Verify nested containers, volumes and networking. [G6][g6] [GH4][gh4] |
| Docker daemon / DinD / image builds | Per-VM Docker/BuildKit daemon, cgroups/namespaces, overlay filesystem or supported alternative, registry push/pull and build-cache export. | Running containers inside a Linux VM does not require nested hardware virtualization. DinD privileges are separate from KVM. Never share the physical host's Docker socket across untrusted jobs. [P5][p5] [DR1][dr1] |
| Service containers: Postgres, Redis, brokers | Per-job network, service DNS names, readiness checks, exposed ports, service logs and optional temporary volumes. | Preserve runner-specific localhost/container-network semantics; destroy database state after the job unless explicitly retained. [Z5][z5] [G6][g6] |
| Matrix builds, shards and fan-out | Bulk/concurrent create, template parameters, OS/architecture/resource selectors, quotas, backpressure and per-child results. | Matrix scheduling usually belongs to CI; Lamp supplies independent capacity. Avoid duplicate jobs on retries. [T6][t6] [W5][w5] |
| Dependency caches | Scoped persistent directories or object-store restore/save; keys, versioning, eviction, concurrent-writer rules and read-only mounts. | Separate trusted/untrusted branch writes. Cache hit must not restore another tenant's tokens or workspace. [G5][g5] [Z4][z4] |
| Build/layer/remote caches | Persistent BuildKit/engine storage or compatible remote content-addressed cache; architecture/toolchain keys and garbage collection. | Fast VM boot does not remove cold dependency/layer downloads. Depot demonstrates a separate remote builder/cache product. [D2][d2] [I7][i7] |
| Artifacts and test reports | Upload/download, checksums, retention, access controls and CI-compatible report paths; collect before destruction, including on failure. | Artifacts are outputs, not a cache or a resumable VM. Support large files and partial-upload retries. [J5][j5] [TC6][tc6] [GC4][gc4] |
| macOS / iOS / Xcode | Native macOS worker or macOS VM backend on appropriate Mac hardware; Xcode/SDK versions, simulators, signing keychain/profiles and Apple service access. | A Linux image on a Mac cannot run Xcode/iOS Simulator. Host installation and guest-OS support must be separate claims. Apple's VM path uses macOS installation images, not Linux Docker images. [P1][p1] [P6][p6] |
| Windows / MSVC / desktop tests | Native Windows guest/worker, PowerShell, MSVC/.NET and Windows image handling; RDP/interactive desktop when tests need UI. | Windows containers require supported host/image versions. Linux guests cannot provide native Windows kernel behavior. Signatures and Windows licensing need an explicit deployment design. [P2][p2] [Z3][z3] |
| Android compilation | Linux JDK, Android SDK/NDK, Gradle caches and signing secrets. | Compilation alone does not need an emulator/KVM. Device tests are a separate capability. [P3][p3] |
| Android emulator tests | Emulator image/SDK, enough RAM/disk, ADB control and accelerated virtualization. A nested Linux KVM design needs guest-visible `/dev/kvm` and virtualization support through every VM layer. [P11][p11] | Android officially says VM-accelerated emulators must run directly on the host, while KVM documents nested guests. Lamp emulator support is **unconfirmed** until exact host/hypervisor/emulator combinations pass tests; offer bare-metal or remote devices as alternatives. [P3][p3] [P11][p11] |
| GPU tests, CUDA and ML | GPU allocation/passthrough or mediated device, compatible drivers/runtime, device isolation, VRAM/resource selection and optional model cache. | Guest image alone cannot supply a GPU. Modal's sandbox GPU/runtime restrictions illustrate backend-specific limits. Lamp GPU support is **unconfirmed**. [P4][p4] |
| Headless browser E2E | Browser binaries and system packages, fonts, shared memory, localhost/services, artifacts and trace/video collection. | Use version-matched browser images and sufficient memory; do not assume a minimal distro image can launch Chromium. [P7][p7] [P8][p8] |
| Headed browser and desktop E2E | Display server/Xvfb on Linux; VNC/WebRTC/remote desktop for viewing, input, screenshots and video. | A virtual display is not a physical GPU or native macOS/Windows UI. GUI auth/cookies are secrets. [P7][p7] [I8][i8] |
| Long-running jobs and debugging | Detached processes, process IDs, reconnectable logs/PTY, leases/heartbeats, configurable TTL, cancellation and retained failure state. | CI controller timeouts still apply. Snapshotting files does not necessarily preserve a live process, network socket or external lease. [C1][c1] [BB3][bb3] [I9][i9] |
| Secrets, signing, deployment | Per-job credentials, secret-file/env injection or broker/proxy, outbound policy, audit trail, OIDC federation where issuer permits, revocation and snapshot exclusions. | Registration secrets, job tokens, cloud identities, model keys and image-pull credentials have different scopes. Log masking does not stop job code from reading credentials it receives. [Z1][z1] [T2][t2] [F4][f4] |
| Multi-architecture builds | Explicit guest CPU architecture, native host pools or declared emulation, multi-platform builders and architecture-aware caches. | Docker image format portability does not establish executable CPU/OS portability. Depot provides native x86/Arm builders. [I7][i7] |
| Kernel, privileged, Kubernetes and hardware tests | Dedicated privilege profile; optional nested cluster, custom kernel, device passthrough/remote device connection and exclusive device leases. | These are **proposed advanced backends**, not guaranteed by generic microVM support. KubeVirt and Windows Hyper-V nesting have explicit hardware prerequisites. USB/FPGA/iOS device passthrough in Lamp is **unconfirmed**. [C5][c5] [P9][p9] |
| Release, deployment, security scans and performance tests | Pinned tools, private network/VPN, registry/package/cloud credentials, artifact provenance inputs, controlled resource sizing and opt-in external writes. | One sandbox can run commands for these jobs, but the deployment's external side effects survive VM deletion. Container build secrets should use secret mounts rather than image layers. [P10][p10] [GC1][gc1] |

## Part 3: external integration model

**Proposed contract:** a caller can create, find, inspect, exec, stream, cancel, upload/download, expose private ports, pause/resume where supported, snapshot/clone, extend a lease and destroy. Use template/image digest, tenant, capability requirements, labels and an idempotency key as inputs. Return sandbox/session ID, state, endpoints and events. The product examples below establish demand for these operations, not one standardized API. [I9][i9] [I10][i10] [I11][i11]

Keep three roles separate: the trigger source (for example Slack), the orchestrator (for example a bot or LangGraph service), and the sandbox provider. A Slack message does not directly register a CI runner. An MCP client invokes tools; MCP itself is not a job scheduler or VM manager. **Proposed:** authenticate at the gateway, map external identity to tenant permissions, and keep webhook/bot signing credentials outside job VMs. [I1][i1] [I6][i6] [I12][i12]

For self-hosted installations, **Proposed:** choose an outbound connected agent, VPN, or authenticated public gateway for remote callers. A SaaS webhook cannot reach a laptop's loopback address by itself. Separate control-plane access, artifact transfer, job egress, and preview ingress. Coder's tunnel/port model and Google private pools show two different solutions. [I11][i11] [GC1][gc1]

Framework adapters can expose shell/files as tools to an agent running elsewhere, or run the entire agent harness inside the sandbox. These have different credential and reconnect needs. Daytona documents both external SDK tools and Claude Agent SDK execution inside a sandbox; CrewAI has provider-specific sandbox tools. [I13][i13] [I14][i14] [I15][i15]

Evaluation needs an environment-provider interface, not a chat integration. Harbor has provider implementations including Daytona, E2B and Modal; Terminal-Bench uses Harbor. SWE-bench's Docker harness needs image creation, patch application, commands and result collection; accepting OCI images alone is not Docker API compatibility. **Proposed:** prioritize a Harbor environment adapter and prove SWE-bench either through a per-sandbox Docker daemon or a modified backend. [I16][i16] [I17][i17] [I18][i18]

Lifecycle is independent of trigger: one-shot disposable job, multi-turn resumable session, long-lived workspace, PR-lived preview, warm reusable worker, or forked evaluation episode. **Proposed:** expose these as explicit policies with quotas and cleanup. A CI job can itself create many sandboxes; many chat turns can reuse one sandbox. Snapshot restore must reissue credentials and reconcile outside resources. Modal and Coder document distinct persistence/lifetime models. [I9][i9] [I19][i19] [I20][i20]

### Evidence gaps and version cautions

- **Unconfirmed:** native Lamp adapters for any product in this report, native Windows/macOS guests, GPU passthrough, nested KVM, hardware-device attachment, Docker API parity, and suspend/resume semantics. These need implementation tests, not additional marketing comparisons.
- Source conflict: Android broadly excludes accelerated emulators inside VMs; Linux KVM documents nested virtualization. Nested KVM alone does not prove supported Android emulation. Validate the full stack before exposing this capability. [P3][p3] [P11][p11]
- **Unconfirmed:** universal one-job runner flags for Bitbucket and Drone; a provider-neutral autoscaling plugin for Azure VMSS, CircleCI's KubeVirt orchestrator, Cloud Build or CodeBuild. Use documented API/agent composition until proven otherwise. [BB2][bb2] [DR2][dr2] [Z3][z3] [C5][c5] [GC1][gc1] [AWS1][aws1]
- Date-sensitive: Dagger's new remote-engine interface is v1.0 beta in October 2026. Gitea/Forgejo runner names and registration flows differ across releases. Pin adapter compatibility versions. [D1][d1] [F1][f1] [F2][f2] [F3][f3]
- Codex Cloud is now documented as legacy, with continuing Code Review and Linear/GitHub integration support. Its current product documentation redirects; do not infer an arbitrary-host Lamp backend from the old environment setup page. [I22][i22]
- Historical examples are marked: Earthly Satellites shut down; E2B's older standalone MCP server repository is archived. These prove a pattern, not current maintained support. [E1][e1] [I21][i21]
- Product examples do not establish that their hosted agent can use Lamp as an arbitrary backend. Codex cloud, Cursor and Claude Code cloud are comparisons unless their documented self-hosting contract is implemented. [I22][i22] [I23][i23] [I24][i24]

### Primary-source index

[g1]: https://docs.gitlab.com/runner/register/
[g2]: https://docs.gitlab.com/runner/executors/custom/
[g3]: https://docs.gitlab.com/runner/executors/docker_autoscaler/
[g4]: https://docs.gitlab.com/ci/runners/configure_runners/
[g5]: https://docs.gitlab.com/ci/caching/
[g6]: https://docs.gitlab.com/runner/executors/
[g7]: https://docs.gitlab.com/runner/fleet_scaling/fleeting/
[g8]: https://docs.gitlab.com/runner/executors/docker_machine/
[g9]: https://pkg.go.dev/gitlab.com/gitlab-org/fleeting/fleeting/provider
[b1]: https://buildkite.com/docs/agent/cli/reference/start
[b2]: https://buildkite.com/docs/agent/self-hosted/aws/elastic-ci-stack
[b3]: https://buildkite.com/docs/agent/self-hosted/agent-stack-k8s
[b4]: https://buildkite.com/docs/agent/reference-architectures
[b5]: https://buildkite.com/docs/pipelines/configure/artifacts
[b6]: https://buildkite.com/docs/pipelines/integrations/containers/docker-containerized-builds
[c1]: https://circleci.com/docs/runner/machine-runner-3-configuration-reference/
[c2]: https://circleci.com/docs/runner/runner-overview/
[c3]: https://circleci.com/docs/guides/execute/run-jobs-on-self-hosted-runners/
[c4]: https://circleci.com/docs/runner/runner-scaling/
[c5]: https://circleci.com/docs/runner/machine-runner-orchestrator/
[j1]: https://plugins.jenkins.io/kubernetes/
[j2]: https://www.jenkins.io/doc/developer/extensions/jenkins-core/
[j3]: https://www.jenkins.io/doc/book/using/using-agents/
[j4]: https://plugins.jenkins.io/google-compute-engine/
[j5]: https://www.jenkins.io/doc/pipeline/steps/workflow-basic-steps/
[j6]: https://www.jenkins.io/doc/book/pipeline/docker/
[z1]: https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/agents?view=azure-devops
[z2]: https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/linux-agent?view=azure-devops
[z3]: https://learn.microsoft.com/en-us/azure/devops/pipelines/agents/scale-set-agents?view=azure-devops
[z4]: https://learn.microsoft.com/en-us/azure/devops/pipelines/release/caching?view=azure-devops
[z5]: https://learn.microsoft.com/en-us/azure/devops/pipelines/process/container-phases?view=azure-devops
[bb1]: https://support.atlassian.com/bitbucket-cloud/docs/runners/
[bb2]: https://support.atlassian.com/bitbucket-cloud/docs/autoscaler-for-runners-on-kubernetes/
[bb3]: https://support.atlassian.com/bitbucket-cloud/docs/configure-your-runner-in-bitbucket-pipelines-yml/
[bb4]: https://support.atlassian.com/bitbucket-cloud/docs/set-up-and-use-runners-for-linux/
[f1]: https://docs.gitea.com/1.25/usage/actions/act-runner/
[f2]: https://docs.gitea.com/runner/2/cache/
[f3]: https://forgejo.org/docs/latest/admin/actions/registration/
[f4]: https://forgejo.org/docs/latest/admin/actions/security/
[f5]: https://forgejo.org/docs/latest/admin/actions/configuration/
[w1]: https://woodpecker-ci.org/docs/administration/configuration/agent
[w2]: https://woodpecker-ci.org/docs/administration/configuration/autoscaler
[w3]: https://woodpecker-ci.org/plugins
[w4]: https://woodpecker-ci.org/docs/administration/general
[w5]: https://woodpecker-ci.org/docs/usage/matrix-workflows
[dr1]: https://docs.drone.io/runner/docker/installation/linux/
[dr2]: https://autoscale.drone.io/
[dr3]: https://docs.drone.io/runner/vm/overview/
[dr4]: https://docs.drone.io/runner/docker/configuration/reference/drone-runner-labels/
[dr5]: https://plugins.drone.io/plugins/cache
[dr6]: https://docs.drone.io/pipeline/exec/overview/
[tc1]: https://www.jetbrains.com/help/teamcity/install-and-start-teamcity-agents.html
[tc2]: https://www.jetbrains.com/help/teamcity/teamcity-integration-with-cloud-solutions.html
[tc3]: https://www.jetbrains.com/help/teamcity/build-cache.html
[tc4]: https://www.jetbrains.com/help/teamcity/predefined-build-parameters.html
[tc5]: https://www.jetbrains.com/help/teamcity/configuring-agent-requirements.html
[tc6]: https://www.jetbrains.com/help/teamcity/build-artifact.html
[tc7]: https://www.jetbrains.com/help/teamcity/container-wrapper.html
[t1]: https://tekton.dev/docs/pipelines/customruns/
[t2]: https://tekton.dev/docs/pipelines/auth/
[t3]: https://tekton.dev/docs/pipelines/workspaces/
[t4]: https://tekton.dev/docs/pipelines/podtemplates/
[t5]: https://tekton.dev/docs/pipelines/tasks/
[t6]: https://tekton.dev/docs/pipelines/matrix/
[t7]: https://tekton.dev/docs/pipelines/resolution/
[a1]: https://argo-workflows.readthedocs.io/en/latest/executor_plugins/
[a2]: https://argo-workflows.readthedocs.io/en/latest/workflow-rbac/
[a3]: https://argo-workflows.readthedocs.io/en/latest/walk-through/artifacts/
[a4]: https://argo-workflows.readthedocs.io/en/latest/memoization/
[a5]: https://argo-workflows.readthedocs.io/en/latest/walk-through/docker-in-docker-using-sidecars/
[d1]: https://dagger.io/changelog/remote-execution/
[d2]: https://dagger.io/learn/migrate-from-jenkins/
[d3]: https://docs.dagger.io/reference/cli/
[e1]: https://earthly.dev/blog/shutting-down-earthfiles-cloud/
[e2]: https://github.com/earthly/earthly/blob/main/CHANGELOG.md
[gc1]: https://docs.cloud.google.com/build/docs/private-pools/private-pools-overview
[gc2]: https://docs.cloud.google.com/build/docs/private-pools/run-builds-in-private-pool
[gc3]: https://docs.cloud.google.com/build/docs/optimize-builds/speeding-up-builds
[gc4]: https://docs.cloud.google.com/build/docs/configuring-builds/pass-data-between-steps
[aws1]: https://docs.aws.amazon.com/codebuild/latest/userguide/welcome.html
[aws2]: https://docs.aws.amazon.com/codebuild/latest/userguide/create-project.html
[aws3]: https://docs.aws.amazon.com/codebuild/latest/userguide/build-caching.html
[aws4]: https://docs.aws.amazon.com/codebuild/latest/userguide/action-runner.html
[gh1]: https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners/autoscaling-with-self-hosted-runners
[gh2]: https://docs.github.com/en/copilot/concepts/agents/coding-agent/about-coding-agent
[gh3]: https://docs.github.com/en/actions/using-workflows/caching-dependencies-to-speed-up-workflows
[gh4]: https://docs.github.com/en/actions/using-containerized-services/about-service-containers
[o1]: https://confluence.atlassian.com/bamboo/configuring-agents-289277172.html
[o2]: https://concourse-ci.org/docs/install/running-worker/
[o3]: https://zuul-ci.org/docs/nodepool/latest/devguide.html
[o4]: https://docs.semaphore.io/using-semaphore/self-hosted
[p1]: https://developer.apple.com/xcode/system-requirements/
[p2]: https://learn.microsoft.com/en-us/virtualization/windowscontainers/deploy-containers/version-compatibility
[p3]: https://developer.android.com/studio/run/emulator-acceleration
[p4]: https://modal.com/docs/guide/sandbox-resources
[p5]: https://docs.gitlab.com/ci/docker/using_docker_build/
[p6]: https://developer.apple.com/documentation/virtualization/virtualize-macos-on-a-mac
[p7]: https://playwright.dev/docs/ci
[p8]: https://playwright.dev/docs/docker
[p9]: https://learn.microsoft.com/en-us/virtualization/windowscontainers/deploy-containers/system-requirements
[p11]: https://docs.kernel.org/virt/kvm/x86/running-nested-guests.html
[p10]: https://docs.docker.com/build/building/secrets/
[i1]: https://docs.devin.ai/integrations/slack
[i2]: https://docs.devin.ai/integrations/microsoft-teams
[i3]: https://docs.devin.ai/integrations/linear
[i4]: https://docs.devin.ai/integrations/jira
[i5]: https://cursor.com/docs/cloud-agent/automations
[i6]: https://n8n.io/integrations/webhook/and/http-request/
[i7]: https://depot.dev/docs/container-builds/overview
[i8]: https://github.com/e2b-dev/desktop
[i9]: https://modal.com/docs/guide/sandboxes
[i10]: https://www.daytona.io/docs/en/
[i11]: https://coder.com/docs/user-guides/workspace-access
[i12]: https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization
[i13]: https://www.daytona.io/docs/en/guides/openai/openai-agents-sdk-with-sandboxes/
[i14]: https://www.daytona.io/docs/en/guides/claude/claude-agent-sdk-interactive-terminal-sandbox/
[i15]: https://docs.crewai.com/v1.15.39/en/tools/ai-ml/daytona
[i16]: https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/base.py
[i17]: https://www.tbench.ai/run
[i18]: https://www.swebench.com/SWE-bench/reference/harness/
[i19]: https://modal.com/docs/guide/sandbox-snapshots
[i20]: https://coder.com/docs/user-guides/workspace-management/
[i21]: https://github.com/e2b-dev/mcp-server
[i22]: https://developers.openai.com/codex/cloud/environments
[i23]: https://cursor.com/docs/cloud-agent
[i24]: https://code.claude.com/docs/en/claude-code-on-the-web
[i25]: https://www.daytona.io/docs/en/guides/langgraph/langgraph-plan-and-execute-data-agent/
[i26]: https://www.daytona.io/docs/guides/mastra/mastra-coding-agent
[i27]: https://ona.com/stories/ona-automations-proactive-background-agents
[i28]: https://ona.com/templates
[i29]: https://modal.com/docs/examples/jupyter_sandbox
[i30]: https://github.com/e2b-dev/code-interpreter
[i31]: https://docs.openclaw.ai/channels/groups
[i32]: https://docs.devin.ai/integrations/gh
[i33]: https://coder.com/docs/get-started/customize-your-template/add-a-language
[i34]: https://namespace.so/
[i35]: https://modal.com/docs/examples/webscraper
[i36]: https://modal.com/docs/examples/safe_code_execution
[i37]: https://modal.com/examples
[i38]: https://github.com/harbor-framework/harbor/blob/main/src/harbor/environments/factory.py
[i39]: https://www.daytona.io/docs/en/playground/
[i40]: https://docs.openclaw.ai/docker
[i41]: https://docs.github.com/en/codespaces/overview
[i42]: https://www.daytona.io/docs/en/preview/
[i43]: https://ona.com/cases/automations
[i44]: https://depot.dev/docs/cache/overview

## Scenario catalog

One row is an integration pattern, not a promise of a native Lamp connector. Lifetimes and needs are **proposed Lamp requirements**. Product links establish an existing pattern. “Analogue” means the named product provides the execution primitive, while that exact end-to-end integration is **unconfirmed**. Devices means guest-visible hardware; “none” still requires ordinary CPU/RAM/disk.

| Scenario | Caller | Trigger | Sandbox lifetime | Needs (devices, network, credentials, persistence) | Existing product that does it |
| --- | --- | --- | --- | --- | --- |
| Self-hosted CI job | CI agent/scaler | Queued commit/PR job | One job; destroy afterward | None by default; CI/Git/registry egress; job-scoped tokens; remote caches/artifacts | [GitLab Docker Autoscaler][g3], [Buildkite][b1] |
| CI step offloads a subtask | Existing runner and Lamp SDK | Script reaches build/test command | One subtask or fan-out group | None; control API plus file transfer; Lamp token; artifact output | [Dagger remote execution][d1], [Depot builders][i7] |
| Container-image builder | Docker/buildx CLI or build service | `build`/push | Per build, or warm builder | Container daemon; registry access; registry token; persistent layer cache | [Depot][i7] |
| Bazel/Gradle/compiler acceleration | Local developer or CI build tool | Cache miss or compile request | Per action or warm worker | CPU/arch selection; remote execution/cache network; scoped cache identity; CAS | [Namespace remote execution][i34], [Depot cache][i44] |
| Native macOS/iOS CI | CI scheduler/mobile agent | Apple build or simulator test | One job | Mac backend/Xcode/simulator; Apple endpoints; signing credentials; SDK/build cache | [Namespace macOS instances][i34] |
| Native Windows CI or UI test | CI scheduler | Windows build/test | One job or debug session | Windows backend/interactive desktop; package services; signing token; artifacts | [Azure agents][z1], [Namespace Windows instances][i34] |
| Android device tests | CI/mobile test harness | Instrumentation test | One test shard | Validated nested KVM stack, bare metal or remote device; ADB; signing/test credentials; emulator-image cache | [Namespace nested-virtualization instances][i34]; Android nesting restriction [P3][p3] |
| GPU validation and model tests | CI or ML harness | GPU-labelled test | Per batch/trial | Allocated GPU/driver; model registry; model/data tokens; model cache and metrics | [Modal GPU sandboxes][p4] |
| Slack coding or analysis request | Chat bot/orchestrator | Mention, slash command, thread reply | Multi-turn session | None, optional browser; Git/model egress; user-mapped repo rights; thread/session mapping | [Devin Slack][i1] |
| Microsoft Teams request | Teams bot/orchestrator | Mention in channel/chat | Multi-turn session | None; Graph/Git/model network; mapped user and tenant identity; session state | [Devin Teams][i2] |
| Discord, Telegram, WhatsApp or community bot | Messaging gateway/agent | Bot command or allowed message | Per session or per user | None, optional browser; channel/model egress; bot tokens outside guest; scoped workspace | [OpenClaw channel sandboxing][i31] [Docker setup][i40] |
| Linear delegated issue | Issue integration service | Assignment, label or agent request | Until result/PR; resumable | Repo tools; Git/model network; issue/repo permissions; issue-session correlation | [Devin Linear][i3], [Ona automation templates][i28] |
| Jira delegated issue | Jira app/service account | Assignment, mention, label or event | Until result/PR | Repo tools; Jira/Git/model network; scoped Jira/repo identity; checkpoints | [Devin Jira][i4] |
| GitHub/GitLab/Bitbucket issue work | Forge app or automation | New issue, assignment or label | Per task | Repo checkout; forge/model access; installation token; branch/artifacts | [Cursor event automations][i5], [Devin GitHub][i32] |
| PR comment, review feedback or failed-check repair | Forge app/review agent | Mention, review request, check failure | One run; optionally resume PR session | Test/browser tools; forge access; restricted repo token; patch/log outputs | [Devin PR collaboration][i32], [Claude cloud auto-fix][i24] |
| Generic signed webhook | User service, webhook gateway | Any verified business event | Per event or correlated session | None; callback/API network; signature verification at gateway; dedupe state | [Cursor webhook automations][i5] |
| Cron and scheduled maintenance | Scheduler or system cron | Time expression | One run, recurring template | None; Git/package APIs; service identity; reports/cache | [Ona Automations][i27], [Cursor schedules][i5] |
| Queue/topic/event-stream consumer | Durable worker/orchestrator | Message, object upload or database event | One message/batch | None; queue/storage network; least-privilege consumer credentials; result/checkpoint store | [Modal OCR job-queue examples][i37]; exact Lamp queue adapter proposed |
| Low-code integration | n8n/Zapier-style workflow | App event, approval, form or webhook | Per workflow action | HTTP API; per-workflow Lamp credentials; callback and durable workflow state | [n8n HTTP workflows][i6] plus sandbox API; exact prebuilt Lamp node unconfirmed |
| MCP-driven shell and files | MCP host/client | Model chooses sandbox tool | Tool call or persistent conversation | Exec/files/PTY; MCP/control network; per-user authorization; session-scoped files | [E2B MCP reference][i21] (archived); [Coder client access][i11] |
| LangGraph state machine | LangGraph service | Graph node requests execution | Per run or shared run workspace | Exec/files; model/data endpoints; delegated API keys; graph checkpoints and sandbox ID | [Daytona LangGraph ETL agent][i25] |
| OpenAI Agents SDK | Application-hosted agent loop | Shell/code tool call | Per agent session | Shell/files; model/control egress; SDK/provider keys; reconnectable state | [Daytona SDK integration][i13] |
| Claude Agent SDK | Orchestrator or in-guest harness | Agent task | Per task or resumable session | CLI/PTY/files; model/Git/MCP network; model and repo credentials; transcript/worktree | [Daytona Claude Agent SDK][i14] |
| Mastra agents | TypeScript agent service | Tool invocation/delegation | Per agent run | Exec/files; control/model egress; scoped provider credentials; workspace ID | [Daytona Mastra coding agent][i26] |
| CrewAI agents/crews | CrewAI task runner | Agent uses execution/file tool | Per crew/task/session | Shell/Python/files; SDK network; provider key; shared or separate agent workspace | [CrewAI Daytona tools][i15] |
| Other frameworks and custom agent loops | ADK/Letta/OpenHands/custom app | Tool call or delegated task | Caller-selected | Stable SDK/HTTP exec/files contract; model egress; tenant identity; optional persistence | [Daytona SDK/API][i10], [Modal sandboxes][i9] |
| Browser-hosted asynchronous coding | Hosted coding product | User sends task from browser/mobile | Task/session, laptop-independent | Full repo/tools; controlled egress; scoped Git/model auth; patches and transcripts | [Cursor Cloud Agents][i23], [Codex cloud (legacy)][i22], [Claude Code cloud][i24] |
| Multi-agent parallel branches | Agent orchestrator | Fan-out/delegation | One VM per worker; optional merge workspace | Exec/files; model/repo access; worker-scoped secrets; isolated branches and output collection | [Modal sandbox fan-out primitives][i9] (orchestrator-specific analogue) |
| Human takeover and live debugging | Developer/client attached to running agent | Failed job or agent asks for help | Extend existing session | SSH/PTY/desktop; private tunnel; user auth; preserve failure workspace | [Coder workspace access][i11], [Daytona Playground][i39] |
| Agent evaluation at scale | Harbor evaluation controller | Dataset/task trial | One fresh environment per trial | Image build/exec/file copy; optional network policy/devices; provider auth; trajectories and verifier logs | [Harbor provider interface][i16] and [factory][i38] |
| Terminal-Bench | Harbor/benchmark runner | Benchmark trial | One task environment; verifier policy | Terminal/toolchain image; task-specific services/network; external provider credentials; result files | [Terminal-Bench on Harbor][i17] |
| SWE-bench patch evaluation | Evaluation harness | Model patch for repository issue | One evaluation instance | Docker images/daemon or backend adapter; patch/test execution; registry auth; test logs | [SWE-bench Docker harness][i18] |
| RL rollouts, reset and branching | Training controller | Episode rollout/reset | Many short episodes; snapshot clones | Parallel exec; optional GPU; model/data endpoints; episode isolation; trajectories/rewards | [Modal sandbox rollout product][i9], [Daytona RL positioning][i10] |
| Synthetic tasks and adversarial tool testing | Dataset/red-team harness | Generated case or attack trial | Fresh trial | Deliberately constrained network/credentials; disposable filesystem; evaluator-only outputs | [Harbor environments][i16] (harness analogue; exact study unconfirmed) |
| PR preview environment | Preview controller | PR open/update/close | PR lifetime; idle suspend | Web ports, DNS/TLS, database/services; preview-only credentials; seed data/optional disk | [Daytona preview endpoints][i42] (hosting primitive); PR controller proposed |
| App generator and interactive demo | Product backend/user session | Prompt, edit or share | Minutes to days | Build tools/web ports/browser; package/model egress; per-user API rights; project files | [E2B code execution][i30], [Daytona Playground][i39] (execution/preview analogues) |
| IDE remote development | Developer IDE/workspace service | Open repository/workspace | Hours/days; resumable | SSH/LSP/debug servers/port forwards; private Git/packages; developer auth; durable home/repo | [Coder VS Code/JetBrains/Zed templates][i33], [Ona environments][i27] |
| Onboarding, devcontainers and prebuilds | Developer portal/prebuild worker | First open or image/repo change | Prebuild job then workspace | Devcontainer tools; registries/Git; bootstrap secrets excluded from image; reusable clean snapshot | [GitHub Codespaces][i41], [Ona dev environments][i27] |
| Notebook and interactive data science | Notebook server/user | Open notebook or execute cell | Multi-cell kernel session | Python/R/Jupyter, optional GPU; data network; scoped dataset credentials; notebooks/results | [Modal Jupyter sandbox][i29] |
| Chat code interpreter | Assistant application | Generated Python/JS/code tool call | Conversation session | Language kernel/files; restricted outbound access; no ambient user credentials; chart/file outputs | [E2B Code Interpreter][i30] |
| ETL, analytics, SQL and data pipelines | Workflow engine/data agent | Dataset arrival, graph step or schedule | Per partition/run | CPU/RAM/disk; data warehouse/object-store access; scoped read/write tokens; checkpoints/results | [Daytona LangGraph ETL][i25], [Modal data examples][i37] |
| Browser automation, scraping and research | Browser agent or data service | URL/task/schedule | One task or retained browser session | Browser/display; target-site egress; isolated cookies; screenshots/downloads/session state | [Modal scraper][i35], [E2B Desktop][i8] |
| Desktop/RPA automation | Computer-use agent | User task/workflow | Per session | Display/input/clipboard; application network; protected login state; recordings/files | [E2B Desktop][i8], [Daytona VNC Playground][i39] |
| Security/CVE remediation and dependency upgrades | Security bot/platform team | Advisory, scan or scheduled audit | One repo/task or fan-out | Scan/build tools; registries/advisory/Git APIs; restricted token; reports and patches | [Ona CVE/migration templates][i28] |
| Incident and production-error investigation | On-call agent or alert gateway | PagerDuty/Sentry/monitor alert | Incident session | Private observability/service access; read-only credentials initially; preserved logs/evidence | [Ona Sentry incident templates][i28] |
| Bulk migrations and repository maintenance | Platform automation | Campaign/API call | Per repository/branch | Toolchains; multiple repos/private network; campaign-scoped tokens; checkpoints and PRs | [Ona migration automations][i43] |
| Release/deployment/IaC worker | Deployment controller or developer | Approval, tag or runbook | One operation; retain audit outputs | Cloud/cluster/private-network access; short-lived deploy identity; plans/state/artifacts outside VM | [Jenkins pipeline execution][j3], [Cloud Build private networking][gc1] (execution analogues) |
| Customer-supplied scripts, plugins and grading | SaaS backend, course or judge | Upload, API call, submission | One untrusted run | Tight CPU/RAM/time/network policy; synthetic inputs; no ambient secrets; result-only persistence | [Modal arbitrary-code sandbox example][i36] (education/grading analogue) |
| Document/media conversion and scientific batch work | Application or batch orchestrator | File upload or dataset batch | Per file/batch | Tool binaries, optional GPU; object storage; object-scoped credentials; output files/checkpoints | [Modal OCR/media/science examples][i37] |
| Internal sandbox portal and manual CLI | Human or platform service | Create button/CLI/API request | User lease, idle stop | Template/capability selection; corporate network; user identity; optional persistent disk | [Daytona Playground][i39], [Coder workspaces][i20] |
| Physical-device, embedded, USB/FPGA or custom-kernel lab | Test-lab scheduler | Hardware test or low-level task | Exclusive job lease | Device passthrough or remote-device API; lab network; device credentials; firmware/log outputs | [Namespace device/OS instances][i34] is a nearby analogue; generic USB/FPGA Lamp/product parity **unconfirmed** |
| Private-network or disconnected enterprise execution | Internal agent/controller | Any allowed trigger | Job or workspace | Private DNS/routes/proxy; internal identity and registries; local cache/artifact store; controlled egress | [Cloud Build private pools][gc1], [self-hosted Coder][i11]; fully air-gapped parity **unconfirmed** |
