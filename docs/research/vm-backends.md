# VM backend research for Lamp

Research cutoff: **2026-11-08**. This is an evidence inventory, not a backend selection. Lamp's targets are **C:** create from a prepared template below 500 ms at p50; **F:** fork a running sandbox, including RAM, below 1 s; **P:** at least 97% of native workload performance. The report does not establish that any backend meets all three.

“Boot” below means the endpoint the publisher measured. Kernel entry, `/sbin/init`, agent readiness, CLI completion, and application readiness are different endpoints. Registry download and OCI conversion must be measured separately from cached-template creation. “Unknown” means no suitable primary-source measurement was found; it does not mean impossible. A disk clone, stopped-VM restart, or restore of a previously captured template does not establish the live-fork target.

## Versions and evidence quality

Released source was checked where possible. Living documentation was checked at the cutoff. Source availability establishes an implementation, not its latency, security audit, or production reliability.

| Project | Version examined | Source |
|---|---|---|
| Firecracker | 1.17.0, September 2026 | [Release][fc-release] |
| Cloud Hypervisor | 54.0, October 2026 | [Release][ch-release] |
| crosvm | November 7 commit `f0880b36`; avoids November 9 main | [Pinned source][crosvm] |
| QEMU | 11.1.3, October 2026 | [Pinned source][qemu] |
| libkrun / krunvm / muvm | 1.19.8 / 0.2.7 / 0.7.0 | [libkrun][krun], [krunvm][krunvm], [muvm][muvm] |
| Kata / microsandbox / SmolVM / Matchlock | 4.3.1 / 0.7.14 / 1.34.0 / 0.3.1 | [Kata][kata], [microsandbox][msb], [SmolVM][smol], [Matchlock][matchlock] |
| Tart | 2.41.1; repository now under OpenAI | [Tart][tart] |

Numerical claims are attributed to their publisher. Most project headlines omit hardware, sample size, percentile, guest configuration, or image-cache state. They are leads for Lamp's benchmark, not interchangeable results.

## VMMs and hypervisor layers

| Backend | Host OS and CPU | Published userspace boot evidence | RAM snapshot, clone, and fork |
|---|---|---|---|
| Firecracker | Linux KVM, x86_64 and arm64. No native macOS or Windows host backend. [README][fc] | Project specification: ≤125 ms from `InstanceStart` to `/sbin/init`, with a minimal guest, 1 vCPU, 128 MiB, tuned kernel and serial output disabled. This is not API-to-agent p50. [Specification][fc-spec] | CPU, devices, and RAM snapshots; disk files separate. Full snapshots GA; diff snapshots remain preview. Private, demand-paged RAM mapping permits repeated restores with shared clean pages. Live fork still needs capture, consistent disk clone, restore, and source resume; no universal <1 s capture-plus-fork number. [Snapshot guide][fc-snap] |
| Cloud Hypervisor | Linux KVM or Linux MSHV; x86_64 and arm64, with GICv3 on arm. MSHV support does not mean a native Windows executable. [README][ch] | Project website advertises <100 ms to userspace with direct kernel boot; no comparable p50 or complete create envelope supplied. [Project][ch-site] | Paused snapshot and restore. Version 54 has eager `copy`, UFFD `ondemand`, and private-file `copyonwrite` RAM restore. Many clones are possible with immutable backing. Disk images remain external. No comparable live-fork latency found. [Snapshot guide][ch-snap] |
| crosvm | Linux/Android KVM on x86_64 and arm64. Windows WHPX exists but is explicitly not tested upstream. Native macOS support not established. [Hypervisors][crosvm-hv] | Unknown for a current minimal agent guest. | CPU/RAM/device snapshot implementation is explicitly experimental, unsupported, format-unstable, and limited to some devices. External device processes need coordinated state. No supported subsecond fork contract. [Snapshot design][crosvm-snap] |
| QEMU `microvm` | **x86 machine type**; arm uses another machine, usually `virt`. QEMU offers KVM/Linux, HVF/macOS, WHPX/Windows and software TCG; verify the exact machine/accelerator combination. [Machine][qemu], [Accelerators][qemu-accel] | Ubuntu publishes relative kernel-start improvements: 282 ms from `microvm`, then 526 ms from qboot in its example. This is not an absolute current app-ready measurement. [Ubuntu][qemu-boot] | QEMU migration/save mechanisms can include RAM and device state where supported. `microvm` does not promise migration across QEMU versions. Compatible devices, backing disks, and capture bandwidth constrain cloning; no current end-to-end <1 s evidence. [Machine][qemu], [Migration][qemu-migration] |
| libkrun upstream | Linux KVM x86_64/arm64; macOS arm64 through **Hypervisor.framework**, not Virtualization.framework. Released 1.19.8 has no Windows backend. [README][krun] | No controlled current upstream boot-to-agent figure found. Downstream figures are listed separately. | Released public API has no general snapshot/fork interface. Downstream snapshot implementations must not be attributed to upstream libkrun. [Header][krun-api] |
| Apple Virtualization.framework (VZ) | macOS on Intel and Apple Silicon for native-architecture Linux guests; macOS guests require Apple Silicon. [Linux][vz-linux], [macOS][vz-mac] | No official comparable boot or fork timing found. | macOS 14+ `saveMachineStateTo`/`restoreMachineStateFrom`; config must pass `validateSaveRestoreSupport`. Save paused VM; restore stopped VM to paused state. No documented cheap multi-clone contract. Guest/device support must be validated on the installed OS. [Save][vz-save], [Restore][vz-restore], [Validator][vz-validate] |
| Apple Hypervisor.framework (HVF) | Low-level Mac vCPU and memory virtualization API, on the host's architecture. Requires entitlement. Device model and lifecycle belong to the VMM. [Apple][hvf] | No standalone boot time: it is a layer, not a bootable VM product. | No turnkey VM snapshot/fork API. libkrun-derived runtimes implement their own state capture above HVF. [Apple][hvf], [SmolVM branching][smol-branch] |
| Windows Hyper-V / HCS | Windows client/server Hyper-V; HCS manages compute systems and utility VMs. Availability depends on Windows edition and virtualization features. [API overview][win-api], [HCS][hcs] | No general HCS Linux-guest p50 found. Windows Sandbox publishes launch in a few seconds, with its own optimized Windows image. [Sandbox][win-sandbox] | HCS exposes pause/save/restore. Hyper-V **standard** checkpoints include memory; production checkpoints do not. Neither proves a general subsecond API for cloning a running Linux VM. [HCS][hcs], [Checkpoints][hyperv-checkpoint] |
| Windows Hypervisor Platform (WHP/WHPX) | Low-level third-party VMM interface. x86_64; Microsoft documents arm64 support on Windows 11 24H2 build 26100.3915+. Individual VMM arm64 support is separate. [WHP samples][whp] | Depends on VMM and guest; unknown as a layer. | Device and RAM serialization belong to the VMM. Current SmolVM/microsandbox use their own WHP backends, not generic HCS cloning. [WHP][whp], [SmolVM][smol], [microsandbox][msb] |
| WSL2 | Managed Linux VM on Windows, with a Linux kernel shared by WSL distributions. Global VM CPU/RAM settings are not per-sandbox isolation. [Comparison][wsl], [Configuration][wsl-config] | No comparable independent-sandbox boot or fork measurement. | Export/import is filesystem distribution state, not a RAM-inclusive fork. An inner KVM VM requires separately available nesting and `/dev/kvm`; do not assume them. [Commands][wsl-export], [Nesting requirements][hyperv-nested] |
| Kata Containers | Linux OCI runtime/shim and guest agent around a selected VMM, including QEMU, Cloud Hypervisor and Firecracker. Host architecture and features depend on that backend. [Architecture][kata], [Virtualization][kata-vmm] | No universal number: guest boot plus shim, agent, mounts, networking and container setup. | VM templating and snapshot possibilities depend on VMM/runtime/storage configuration. A Kata pod boundary is not automatically one VM per container or a live-fork API. [Architecture][kata], [Virtualization][kata-vmm] |

### Devices and optionality

The table concerns native device models, not devices that could be implemented later. `—` means no native support established in the cited interface. `?` means unconfirmed for that exact machine/backend. A serial console or shutdown controller is not a human input device. Guest drivers are still required.

| Backend | net / blk | Files: virtiofs / 9p | vsock / balloon / rng | GPU / sound / input | Hotplug and physical GPU |
|---|---|---|---|---|---|
| Firecracker 1.17 | virtio net/blk; pmem and vhost-user-blk also available | — / — | yes / yes, plus virtio-mem / yes | — / — / — | Optional PCI hotplug **preview**, blk/pmem/net; guest rescan/removal required. No VFIO GPU. [Devices][fc], [Hotplug][fc-hotplug], [Memory][fc-memory] |
| Cloud Hypervisor 54 | virtio net/blk | yes / — | yes / yes / **always enabled** | No native general GPU/sound/input model established; generic vhost-user extensions exist, but its docs say virtio-GPU does not work | CPU/RAM/device hotplug; Linux VFIO with IOMMU can expose compatible GPUs. Snapshot support only for migratable VFIO devices, not all GPUs. [Model][ch-dev], [vhost-user][ch-vhost], [VFIO][ch-vfio], [Snapshots][ch-snap] |
| crosvm | virtio net/blk | yes / yes | yes / yes / yes | virtio GPU, sound, input; graphics options include virgl/gfxstream/rutabaga | Experimental hotplug feature is network-only in the book. Linux VFIO/device-specific passthrough; GPU sharing and full passthrough are distinct. [Devices][crosvm-dev], [Hotplug][crosvm-hotplug] |
| QEMU `microvm` | virtio-mmio net/blk | virtiofs ? / 9p possible with matching build/drivers | virtio-mmio variants, where built | Exact minimal-machine support ?; full QEMU has broader models | No hotplug; no PCI, hence no PCI VFIO GPU. Eight virtio-mmio transports. Use another QEMU machine for PCI-dependent devices. [Machine][qemu], [Devices][qemu-dev] |
| libkrun 1.19.8 | virtio net/blk; optional TSI networking | yes / — | yes / free-page-reporting balloon / yes | virtio GPU, sound and input APIs | No general device hotplug or VFIO GPU interface established. Venus/native-context GPU acceleration is not PCI passthrough. **TSI can activate without a virtio NIC.** [README][krun], [API][krun-api] |
| Apple VZ | virtio net/blk | yes / — | virtio socket / Linux balloon / entropy | Linux virtio GPU, Mac graphics; audio and input configurations | No generic public PCI/GPU passthrough. Device collections are configuration-time; general live hotplug unconfirmed. Saved-state support varies by configuration. [Configuration][vz-config], [Validator][vz-validate] |
| HVF / WHP | VMM-defined | VMM-defined | VMM-defined | VMM-defined | CPU virtualization APIs do not supply a device model. [HVF][hvf], [WHP][whp] |
| Hyper-V / HCS | synthetic VMBus network/storage, VHDX | HCS schema has VirtualSMB and Plan9, not native virtiofs | Hyper-V sockets are not virtio-vsock; Dynamic Memory differs from virtio-balloon; rng model depends on VM type | HCS has optional video/keyboard/mouse entries; sound support in this utility-VM schema unconfirmed | Server DDA gives whole-device assignment; GPU-P shares supported GPUs. Server 2025 supports GPU-P live migration on supported configurations; this is not RAM-fork evidence. [HCS devices][hcs-dev], [Sockets][hyperv-socket], [Memory][hyperv-memory], [GPU][hyperv-gpu] |
| Kata | selected VMM's devices | virtiofs, 9p or block-based root depending on configuration | selected VMM | selected VMM and runtime policy | Agent/runtime mediation adds another compatibility requirement. [Architecture][kata], [Virtualization][kata-vmm] |

“Only requested devices” needs an explicit audit of the actual VM configuration. Cloud Hypervisor's unconditional RNG is a concrete mismatch with a literal interpretation. libkrun's automatic TSI means removing virtio-net alone does not disable network access. Minimal hardware still needs platform boot/timer/interrupt infrastructure. [CH model][ch-dev], [libkrun API][krun-api], [QEMU machine][qemu]

### Resource limits, licensing and maturity

| Backend | CPU, RAM and overcommit | License and operating evidence |
|---|---|---|
| Firecracker | Configurable RAM; supported maximum 32 vCPUs in 1.17. Host cgroups impose CPU/memory controls. Balloon and memory hotplug can reclaim/resize guest RAM. Host CPU scheduling and demand paging permit overcommit, not guaranteed throughput. [Machine config][fc-config], [Balloon][fc-balloon], [Memory][fc-memory], [Jailer][fc-jailer] | Apache-2.0. AWS identifies Lambda and Fargate as production users. Its compute-only specification is **>95%**, not ≥97%, and notes pending integration tests. [README][fc], [Specification][fc-spec] |
| Cloud Hypervisor | vCPU count, memory size/zones, balloon, virtio-mem and hotplug; KVM/host constraints apply. Sharing/NUMA/hugepage choices affect CoW eligibility. No single portable maximum or overcommit performance promise established. [README][ch], [Snapshots][ch-snap] | Apache-2.0/BSD-3-Clause code. Linux Foundation project with established Kata integration; sponsoring companies alone do not prove a particular production deployment. [README][ch], [Kata][kata-vmm] |
| crosvm | Configurable CPU/RAM, balloon and reclaim mechanisms. Exact supported maxima vary by platform/build; no portable overcommit performance bound found. [Book][crosvm-dev] | BSD-3-Clause. Used by ChromeOS, Android Virtualization Framework and Cuttlefish; snapshot maturity is much lower than its normal VM path. [README][crosvm], [Snapshots][crosvm-snap] |
| QEMU | CPU/RAM machine parameters; host scheduling, balloon and backing-memory policy control overcommit. `microvm` excludes resource hotplug. [Machine][qemu], [Invocation][qemu-invoke] | GPLv2 emulator with component exceptions and separate firmware licenses. Mature general VMM; that does not certify every `microvm`/accelerator/device combination. [License][qemu-license] |
| libkrun | CPU/RAM API parameters and `krun_get_max_vcpus()` runtime query; caller must enforce host process limits and isolation. Balloon is free-page reporting rather than a general guest-memory resizing contract. Specialized confidential variants have separate constraints. [API][krun-api], [README][krun] | Apache-2.0; integrations include crun, krunkit and muvm. **Guest and VMM are treated as one security context.** Host namespaces/mount isolation/UID policy must restrict what the proxy can access. A virtiofs path is not sufficient isolation by itself. [Security model][krun] |
| VZ / HVF | Query VZ `minimum/maximumAllowedCPUCount` and memory properties on the actual OS. Host scheduling and Linux ballooning permit resource management; no universal safe oversubscription ratio. HVF callers implement their own policy. [Configuration][vz-config], [HVF][hvf] | Proprietary Apple system frameworks. Used by Tart/Lima and libkrun-derived tools respectively; their licensing does not make the frameworks portable. [Tart][tart], [Lima][lima], [libkrun][krun] |
| Hyper-V / HCS / WHP | vCPU allocation, scheduler limits and Dynamic Memory; edition/version-specific scale limits. Oversubscribing does not preserve a 97% performance floor. WSL CPU/RAM limits apply to its shared VM. [Scale][hyperv-scale], [Memory][hyperv-memory], [WSL][wsl-config] | Proprietary Windows platform components. HCS underlies Microsoft container/utility-VM infrastructure; WHP is an API used by external VMMs. [API overview][win-api], [HCS][hcs] |
| Kata | OCI CPU/memory resources are translated through shim/agent/VMM configuration. Guest-kernel and agent overhead consume resources beyond container requests. [Architecture][kata] | Apache-2.0. Established containerd/Kubernetes integration; no universal boot/fork/performance guarantee follows from that integration. [Project][kata-project] |

### Memory-fork correctness and performance caveats

* **Capture is not restore.** Firecracker capture serializes RAM and state; disk synchronization is separate work. Lazy restore can avoid eagerly reading RAM, but it does not make capture free. In 1.17, disabling snapshot-file synchronization changes durability waits; block devices are still fsynced. Disk CoW and RAM CoW need coordinated immutable origins. [Firecracker snapshots][fc-snap]
* **CH CoW has narrow eligibility.** Version 54 privately maps immutable snapshot RAM. Shared memory, hugepages, mergeable RAM, reserved/NUMA configurations, virtio-mem and passthrough can force eager copying. UFFD mode disallows several external-device paths and needs host permissions. Ballooning works with CoW, but DMA pinning may fault every page and destroy its practical sharing benefit. [CH snapshots][ch-snap]
* **Clone identity is application state too.** Firecracker and CH provide VMGenID mechanisms to help a supporting guest kernel reseed randomness. Firecracker documents Linux version/architecture requirements. User-space PRNGs, cached tokens, TLS sessions and application UUIDs still need explicit clone hooks. [Firecracker randomness][fc-random], [CH snapshots][ch-snap], [SnapStart uniqueness][snap-unique]
* **Network state cannot simply be duplicated.** Allocate separate MAC/IP/hostname/machine identity; reconnect sockets and refresh external leases/credentials. Firecracker documents clone-specific networking. An old TCP connection or external lock is not made independent by copying RAM. [Networking][fc-network], [SnapStart practices][snap-practices]
* **Clock behavior must be tested.** Firecracker documents restored clock behavior and optional real-time clock handling with host-kernel requirements. microsandbox synchronizes wall time by default but resumes the captured monotonic clock. Test timeout, certificate, lease and scheduler behavior after long snapshot storage. [Firecracker snapshots][fc-snap], [microsandbox snapshots][msb-snap]
* **State portability is constrained.** CPU feature baseline, architecture, interrupt-controller version, guest kernel, VMM state format and external devices matter. CH does not guarantee cross-version snapshots. Firecracker documents CPU/format compatibility. GPU/accelerator state needs its own migration support. [CH][ch], [Firecracker snapshots][fc-snap], [CH VFIO snapshots][ch-snap]
* **Apple save/restore is not a generic Linux fork promise.** WWDC23 describes saved state bound to the same Mac/user, separately copied disk and auxiliary storage, and possible cold reboot after incompatible updates. Validate each configuration. Tart 2.41.1 explicitly exposes suspension only for macOS VMs and restricts devices; current VZ Linux memory-save support remains **unconfirmed here**. [WWDC][vz-wwdc], [Validator][vz-validate], [Tart implementation][tart-run]

## Hosts without hardware virtualization

These preserve useful service functionality, but do not provide Lamp's advertised hardware microVM boundary. Kernel/device isolation and performance must be described as a separate capability tier.

| Fallback | Isolation and host support | Performance and memory-fork evidence |
|---|---|---|
| gVisor `runsc` | Linux x86_64/arm64; Sentry implements Linux system calls in userspace, reducing direct host-kernel exposure. `systrap` does not require KVM; the optional KVM platform does. Not a hardware VM boundary. [Platforms][gvisor-platform], [Architecture][gvisor-arch] | Project benchmarks report little overhead for some CPU/memory work and larger syscall/filesystem/network costs. No workload-independent ≥97% guarantee. Checkpoint/restore can save process/memory state, with lazy/background restore options and compatibility limits; no universal <1 s fork metric. [Performance][gvisor-perf], [Checkpoint][gvisor-cr] |
| Plain containers | Linux namespaces, cgroups, capabilities, seccomp and LSM policy; shared host kernel. Rootless/user namespaces improve privilege separation. Mac/Windows Linux containers still need an outer Linux VM. [Docker security][container-security], [WSL][wsl] | Native CPU execution is plausible; filesystem/network configuration still matters. Docker's 463 ms lifecycle result below is not a universal create p50. CRIU can checkpoint supported Linux processes; disk snapshots alone do not preserve RAM. [Benchmark][bench], [CRIU][criu] |
| Sysbox | Linux container runtime with user namespaces, syscall mediation and virtualized proc/sys views; supports systemd and nested Docker without privileged containers. Still shares the host kernel. Confirm supported distro/architecture packages for the deployment. [README][sysbox], [Security][sysbox-security] | Nestybox's **2020 vendor benchmark** reports performance close to runc in its tested cases, with networking differences. Too old/narrow to establish 97% today. No supported general subsecond live-memory-fork result found. [Benchmark][sysbox-perf] |
| QEMU TCG | Software-emulated VM when hardware acceleration is absent; preserves a guest kernel/device boundary. [Accelerators][qemu-accel] | No evidence for Lamp's create/fork/97% targets. Treat native-speed execution as unconfirmed, especially across architectures. |

## Nested virtualization on clouds

Cloud support changes quickly. The presence of CPU virtualization flags alone is insufficient: check the exact SKU, region, security mode, host kernel, `/dev/kvm` access and provider support policy. Nested results must be compared both with the outer VM baseline and, if claimed, physical-host native speed.

| Provider | Current primary-source position | Consequence for Lamp |
|---|---|---|
| AWS EC2 | **Nested virtualization is now supported on selected virtual Intel instances**, not only bare metal. Current guide lists M7i/M7i-flex/M8i/M8id/M8i-flex; C7i/C7i-flex/C8i/C8id/C8i-flex; R7i/R7iz/R8i/R8id/R8i-flex/X8i; I7i/I7ie. Enable its CPU option. KVM and Hyper-V are supported L1 choices. [Guide][aws-nested] | First 2026 launch and June expansion make old “EC2 requires metal” advice obsolete. No Graviton virtual nesting listed. Query current instance availability; AWS recommends metal for latency-sensitive cases. [Guide][aws-nested], [June announcement][aws-nested-news] |
| Google Cloud | KVM L1 supported on eligible Intel and now **N4D AMD** VMs; exclusions include E2, memory-optimized and Arm. Enable nesting and satisfy organization policy. [Overview][gcp-nested] | Google warns CPU overhead can be ≥10%, and I/O overhead can exceed 10%. This directly prevents assuming ≥97% of physical-host native speed. [Overview][gcp-nested] |
| Azure | Selected SKU families, including Dv5 and Dasv5, list nesting. Hyper-V nesting guidance requires suitable security settings, commonly SecurityType Standard. Microsoft support scope is narrower than “any nested hypervisor.” [Dv5][azure-dv5], [Dasv5][azure-dasv5], [Nesting][hyperv-nested] | Confirm SKU and security mode. Microsoft's documented support does not establish support for arbitrary KVM-on-Hyper-V configurations or Arm instances. No general 97% nested-performance promise. [Support][azure-nested-support] |
| Hetzner | Cloud FAQ explicitly excludes nested virtualization. Dedicated Root Servers are a different, bare-metal product. [Cloud FAQ][hetzner], [Dedicated][hetzner-metal] | Cloud needs a no-KVM fallback; dedicated machines can run a host VMM subject to hardware/OS configuration. |
| DigitalOcean | **Unconfirmed current official support.** The located provider-hosted Q&A is old community material, not a current supported-SKU matrix. [Q&A][do-nested] | Do not advertise nested KVM without a current provider confirmation and host probe. A fallback is needed where unavailable. |
| Oracle Cloud | Oracle's January 2026 engineering post discusses Firecracker on selected VM versus bare-metal shapes. Official docs note nested instances cannot live-migrate. Exact current per-shape matrix, especially Arm/Always Free, remains unconfirmed here. [Oracle engineering][oci-nested], [Migration restriction][oci-migrate] | Check the shape and actual KVM availability. Do not generalize an Intel VM result to all OCI shapes. |
| Bare metal | EC2 `.metal`, Oracle BM and Hetzner dedicated remove an outer VM layer; CPU extensions, firmware and host privileges still matter. [AWS][aws-nested], [Oracle][oci-nested], [Hetzner][hetzner-metal] | Native KVM is an option on suitable Linux hosts. Bare metal alone does not establish a boot/fork percentile or a 97% application result. |

Apple nesting is another separate capability: Lima documents VZ nesting on M3-or-newer hardware with suitable macOS, and the API exposes a support query. It does not accelerate an ordinary non-nested sandbox; it matters if guests themselves need KVM. Do not infer support from “Apple Silicon” alone. [Lima VZ][lima-vz]

## OCI image to bootable microVM

OCI layers describe an ordered filesystem change set, including whiteouts. A normal application image does not supply the microVM's kernel, boot protocol or agent. Lamp must preserve layer semantics, ownership and architecture, then supply its guest boot environment. [OCI layer specification][oci-layer], [Firecracker rootfs guide][fc-root]

| Path | Preparation and create path | Tradeoffs relevant to the targets |
|---|---|---|
| Prebuilt ext4/raw root disk | Apply OCI layers once; populate ext4, add init/agent and required libraries; boot a separately supplied compatible kernel. Cache by image digest and boot-environment version; clone before create. [Firecracker guide][fc-root], [E2B runtime][e2b-runtime] | Keeps conversion off the template-create path. Block I/O fits Firecracker. Rebuilding/downloading a cold image is separate latency; writable root and guest flush/snapshot consistency remain necessary. |
| Shared root through virtiofs | Unpack or mount merged OCI layers on host; expose read-only root through virtiofs; give each guest a private writable upper/tmpfs or disk. libkrun, CH and Kata demonstrate the ingredients. [libkrun][krun], [CH model][ch-dev], [Kata][kata] | Avoids whole-root disk materialization. Host FUSE/metadata behavior and external file lifetime enter the execution/snapshot path. Firecracker lacks native virtiofs. Host-mounted files are not automatically captured with guest RAM. [Firecracker][fc], [microsandbox snapshots][msb-snap] |
| Layered block images | Convert OCI layers into immutable block layers plus per-sandbox writable layer; overlaybd provides remote block layering, while QEMU can use backing-image chains. [overlaybd][overlaybd], [QEMU images][qemu-images] | Can retain registry/cache sharing and cheap disk clones. Block layering is not a RAM fork. Deep chains, cache misses and compaction can affect both create and steady-state speed. [SmolVM limitations][smol-limits] |
| Guest overlayfs | Shared immutable lower with one private writable upper/work layer; keep host and guest overlay semantics distinct. [Kernel overlayfs][overlayfs] | Only changed files consume private space. Copy-up, filesystem semantics and mount configuration can affect workload speed; benchmark against the chosen block-root path. |

### Lazy pulls

| Technology | Primary-source mechanism | What it does not establish |
|---|---|---|
| eStargz / stargz-snapshotter | Indexed, seekable gzip OCI layers; optimized conversion and prioritized prefetch allow startup before full download. [containerd project][stargz] | Linux snapshotter availability is not native Mac/Windows VMM integration. First-use reads still fetch data; no generic <500 ms remote create or 97% runtime result. |
| Nydus | RAFS image/runtime path with FUSE/virtiofs/EROFS integrations; also supports lazy access to suitable ordinary OCI layers through indexing. Do not assume every path requires full image conversion. [Nydus snapshotter][nydus] | Cache warming, index generation, guest integration and remote miss costs remain. |
| AWS SOCI | Sidecar indexes enable range reads from unmodified suitable OCI image layers. [AWS project][soci] | Not a microVM boot format or RAM-snapshot system; needs a filesystem/block export path to the guest. |
| overlaybd | Remote, layered, writable block device with demand loading and conversion tools. [containerd project][overlaybd] | The host driver/service and image conversion are extra components; cold storage faults move work after VM start. |

For a fair C measurement, report both locally prepared templates and first use of an uncached digest. For P, report cold lazy faults separately from a warmed working set. These are proposed measurement distinctions, not published performance guarantees.

### Host-local copy-on-write

| Host storage | Available primitive | Constraints |
|---|---|---|
| XFS / btrfs | XFS reflink-enabled filesystems can clone file extents; btrfs supports reflinks and subvolume snapshots. [XFS manual][xfs], [btrfs][btrfs] | Same compatible filesystem; shared extents become private on write. Fragmentation, changed data and metadata still cost time. |
| Linux dm-thin | Thin logical-volume snapshots share block mappings. [Kernel guide][dmthin] | Pool sizing, exhaustion behavior, discard and metadata durability must be managed. Works below the guest filesystem. |
| ZFS | Writable clone of an immutable snapshot. [OpenZFS][zfs] | Same storage pool and origin lifetime/dependencies; snapshot cleanup is part of service management. |
| macOS APFS | `clonefile`/copy APIs can make CoW file clones. Tart uses APFS-aware VM disk cloning. [Apple APFS][apfs], [Tart][tart] | Same volume, supported API and file layout; does not clone RAM or external mounted directories. |
| Windows ReFS | Block cloning duplicates extent mappings with allocate-on-write. [Microsoft][refs] | Same volume and documented range/alignment constraints; do not assume ordinary NTFS system disks support this primitive. VHDX differencing disks are another storage path. [ReFS][refs], [Hyper-V checkpoints][hyperv-checkpoint] |

Disk CoW saves copying disk bytes. Memory CoW needs a VMM/runtime mapping mechanism as well. A consistent live clone must capture the relationship between guest dirty pages, device queues and its disk state; cloning a running disk file alone is insufficient. [Firecracker snapshots][fc-snap], [CH snapshots][ch-snap]

## Existing implementations and published numbers

| Project | How it works | Published timing and fork meaning |
|---|---|---|
| E2B | Firecracker runtime, initialized templates, lazy memory loading and storage/cache machinery. Source is more useful than assuming managed-service behavior transfers unchanged to a single host. [Runtime][e2b-runtime] | E2B customer material quotes roughly **150 ms startup**. It is a vendor claim with no comparable p50 conditions. Runtime supports snapshot-based reuse; no comparable current running-source fork number established here. [E2B publication][e2b-time], [Runtime][e2b-runtime] |
| CodeSandbox | Firecracker memory snapshots, UFFD lazy paging and shared snapshot-page/cache work. [Maintainer article][cs-uffd] | Its original article publishes **2 s to clone a running VM**, including memory. Later articles explain faster snapshot loading, not a verified current <1 s full-fork p50. Original pages blocked direct retrieval here; indexed text and maintainer discussion corroborate the design. Treat current number as **unconfirmed**. [Original][cs-fork], [Maintainer discussion][cs-maintainer] |
| Fly.io Machines | Firecracker; prepared OCI filesystem and host-local caches. API now includes memory suspend/resume. [Machines][fly], [API][fly-api] | Fly publishes about **300 ms** launch in its functions example. Historical suspend preview says a few hundred ms resume. A machine clone/config-volume copy is not evidence of running-source RAM fork. [Example][fly-time], [Suspend announcement][fly-suspend] |
| Docker Sandboxes (`sbx`) | Current docs cover Linux, macOS and Windows. Each sandbox has a microVM/kernel and private Docker Engine; workspace sharing uses virtiofs. Underlying VMM identity is not established in public architecture docs. [Install][sbx-install], [Architecture][sbx] | No trustworthy comparable boot/fork number found. “Clone” in Git workflows is not evidence of VM-memory cloning. Do not infer libkrun or a snapshot API from Docker's other products. [Architecture][sbx], [Release notes][sbx-release] |
| microsandbox 0.7.14 | Local libkrun-derived `msb_krun` runtime on Linux x86/arm, Mac arm and Windows; own snapshot/fork implementation. Apache-2.0, beta. [README][msb], [Dependency][msb-dep] | Vendor footnote: **average guest boot <100 ms on M1**, not complete CLI/API readiness or p50. Local full snapshots and CoW live forks now documented on all three OSes; cloud supports disk-only snapshots. No measured <1 s full fork. [README][msb], [Snapshots][msb-snap] |
| SmolVM 1.34 | Maintained libkrun fork; Linux x86/arm, Mac arm, Windows x86 WHP. Apache-2.0. RAM/disk/CPU checkpoints and CoW branches; own format. [README][smol], [Submodules][smol-dep] | Vendor table advertises **<200 ms agent-ready** without comparable percentile/hardware conditions. Linux/Mac branch can continue the parent. **Windows requires `--freeze-source`** for live branching, so it does not meet continuing-parent fork semantics. No measured current fork latency. [Branching][smol-branch], [Limits][smol-limits] |
| Matchlock 0.3.1 | Firecracker on Linux, VZ on Mac, experimental project. Current design copies files into/out of guest-owned storage rather than relying on host mounts. [README][matchlock] | Publisher advertises **<1 s** startup, insufficient to establish <500 ms p50. No supported RAM-fork number found. |
| krunvm / muvm | krunvm uses Buildah OCI roots and libkrun on Linux/Mac arm; Apache-2.0. muvm is a **Linux desktop** helper used on Asahi Linux, including 16K-host/4K-guest compatibility and graphics; its main crate is MIT. Apple hardware does not make it a macOS-host tool. [krunvm][krunvm], [License][krunvm-license], [muvm][muvm], [License][muvm-license], [Asahi][muvm-blog] | No current maintained boot/fork benchmark found. Upstream libkrun snapshot limitations apply unless a downstream implements additional machinery. [API][krun-api] |
| Lima | VZ default on suitable Macs; QEMU elsewhere; krunkit is an alternative for GPU-enabled Linux guests. Provisioner/SSH/agent and filesystem mounts surround the VMM. [VM types][lima], [VZ][lima-vz] | No comparable per-sandbox boot or RAM-fork figure found. A persistent development VM is a different lifecycle from one microVM per task. |
| OrbStack | Proprietary Mac product with a shared Linux VM/kernel for containers and Linux machines, WSL-like architecture. [Architecture][orb] | Publishes startup in seconds. Filesystem machine cloning does not demonstrate independent memory-inclusive VM forks; machines do not each provide a distinct hardware VM boundary. [Machines][orb-machines] |
| Tart 2.41.1 | Apple VZ, Apple Silicon macOS/Linux VMs, APFS disk clones and OCI distribution of VM artifacts. OCI VM disks are not ordinary container rootfs images. License is **FSL-1.1-ALv2**, with restrictions before its later Apache conversion; not a permissive OSS dependency today. [README][tart], [License][tart-license] | No comparable current p50 boot or RAM-fork number found. `clone` is primarily disk/config cloning; suspension in pinned implementation is macOS-only and limits devices. [Run implementation][tart-run] |
| AWS Lambda SnapStart | Captures initialized memory/disk, caches snapshot chunks, restores environments with runtime hooks. **Container-image support arrived October 2026**; older zip/Java-only summaries are stale. Managed AWS service, not an embeddable VMM. [Current guide][snap], [Container announcement][snap-container] | AWS's original launch advertised up to 10× startup improvement, not a generic <1 s live fork. Uniqueness, connections and restore hooks are directly relevant design lessons. [AWS launch][snap-launch], [Uniqueness][snap-unique], [Practices][snap-practices] |

One reproducible comparison is **Superrad's sandbox-bench**, published by the microsandbox vendor, not an independent lab. In May 2026 on GCP c3-standard-192-metal with cached Alpine artifacts, 10 runs plus warmups measured median CLI-to-process-exit: krunvm 310 ms, microsandbox 320 ms, Docker 463 ms, Firecracker 808 ms, and then-current packed SmolVM 6,219 ms. It measures an entire lifecycle, uses older versions, and excludes a broken CH adapter. It must not be used to rank November versions or contradict Firecracker's narrower init-time specification. [Benchmark and methodology][bench]

microsandbox's default full restore copies RAM; its opt-in CoW restore first prepares a local memory cache. Host-mounted files are external, device topology must match, and network recovery is not guaranteed. SmolVM can compact disk chains on its branch path; Windows active virtiofs DAX has checkpoint restrictions. These are concrete reasons to measure fork with real mounts, disk histories and dirty RAM rather than an empty guest. [microsandbox snapshots][msb-snap], [SmolVM limitations][smol-limits]

## Guest operating systems

| Guest | Feasible paths and limits |
|---|---|
| Linux | Supported native-architecture guests across Linux KVM, Mac VZ/HVF runtimes and Windows Hyper-V/WHP runtimes. Use the guest kernel/drivers expected by each VMM. Cross-architecture emulation is a different performance class; an x86 container's binaries are not automatically native on arm. [Firecracker][fc], [Apple Linux][vz-linux], [WSL][wsl], [QEMU accelerators][qemu-accel] |
| macOS | Apple VZ macOS guest API requires Apple Silicon. The macOS Tahoe license permits up to **two additional instances per Mac already running macOS**, on Apple hardware, for its stated purposes such as development/testing; read applicable license terms for each deployment. This is not a general Linux-VM concurrency cap. [Apple API][vz-mac], [Tahoe SLA §2B(iii)][mac-license] |
| macOS framework concurrency | Tart's maintained historical documentation reports a two-macOS-VM framework limit; Apple exposes `virtualMachineLimitExceeded`. Exact current limits must be reproduced on the target OS/hardware rather than assuming that historical implementation detail is timeless. No evidence here of an equivalent two-Linux-VM limit. [Tart discussion][tart-limit], [Apple error][vz-limit] |
| Windows | Full Hyper-V and suitable QEMU/Cloud Hypervisor machine configurations can run supported Windows guests; CH lists Windows 10/Server 2019. Windows needs its own licensed OS image, boot stack and drivers. A Linux OCI rootfs and minimal Linux init do not become Windows by changing a backend. Firecracker/libkrun's documented Linux-oriented boot paths do not establish arbitrary Windows guest support; QEMU `microvm` is not a generic Windows PC. [CH support][ch], [QEMU machine][qemu], [Hyper-V guest support][hyperv-guests], [libkrun][krun] |

## Evidence still needed before backend selection

No located source establishes C, F and P together across Lamp's host matrix. Test API-to-first-command p50/p95; capture pause and parent resumption separately from child readiness; repeat fanout with 128 MiB, 1 GiB and larger dirty working sets. Include OCI digest-cache misses, external mounts, network rekeying, clocks, page-cache pressure and disk-chain age. These are proposed experiments, not additional performance claims.

For P, compare the same compiler, filesystem, network, CPU topology and workload on the same host. Report CPU throughput, syscall latency, filesystem metadata, sequential/random I/O, network, and GPU separately. A compute-only ratio or hardware acceleration does not establish a 97% floor for CI/agent workloads. Firecracker's >95% specification and Google's nested-CPU warning show why a universal claim is not supported. [Firecracker specification][fc-spec], [GCP][gcp-nested]

## Per-host options

These are candidates, not recommendations. Times below are evidence-based planning bounds or explicitly unknown, not invented expectations. **C/F/P** refer to the targets defined above. Device shorthand: **minimal** = net/blk/vsock with no general GPU/audio/input; **broad** = larger optional device model; **shared kernel** = no per-sandbox hardware VM boundary. Detailed qualifications and sources are in the sections above.

### Linux x86_64 with KVM

| Candidate | Expected create evidence | Memory-inclusive fork evidence | Devices | What blocks C / F / P |
|---|---|---|---|---|
| Firecracker | ≤125 ms to init; full create p50 unknown | Repeated lazy snapshot restore; live capture+fork time unknown | Minimal; preview PCI hotplug | C: orchestration/image readiness; F: capture/fsync/disk clone; P: >95% compute spec does not prove 97%. [Spec][fc-spec], [Snapshots][fc-snap] |
| Cloud Hypervisor | <100 ms userspace headline; end-to-end unknown | CoW restore in v54; live capture time unknown | fs, hotplug, VFIO; mandatory RNG | C: full readiness unmeasured; F: CoW eligibility and external devices; P: I/O/workload evidence absent. [Project][ch-site], [Snapshots][ch-snap], [Model][ch-dev] |
| crosvm / QEMU microvm | Unknown comparable p50 | Experimental crosvm; compatible QEMU save/restore, time unknown | crosvm broad; microvm limited/no PCI | C: unmeasured; F: support/serialization limits; P: workload-specific overhead. [crosvm snapshots][crosvm-snap], [QEMU][qemu] |
| libkrun / microsandbox / SmolVM | Upstream unknown; downstream <100 ms guest average / <200 ms agent headline | Upstream absent; downstream CoW forks, latency unknown | virtiofs and optional graphics; runtime-specific | C: headline endpoint/cache gaps; F: downstream implementation and storage; P: no comparable 97% proof, host isolation required. [libkrun][krun], [msb][msb], [SmolVM][smol] |
| Kata wrapper | Selected VMM plus runtime overhead; unknown | Configuration-dependent; no universal live fork | Selected VMM plus agent | C/F: wrapper and storage setup; P: guest/agent/filesystem overhead unmeasured. [Kata][kata] |

### Linux arm64 with KVM

| Candidate | Expected create evidence | Memory-inclusive fork evidence | Devices | What blocks C / F / P |
|---|---|---|---|---|
| Firecracker | Same project init target; target-host p50 unknown | Snapshot restore; capture+fork unknown | Minimal | C/F: measure actual arm CPU/GIC/kernel compatibility and capture; P: no arm workload 97% proof here. [Support][fc], [Snapshots][fc-snap] |
| Cloud Hypervisor / crosvm | CH headline <100 ms; crosvm unknown | CH CoW with restrictions; crosvm experimental | CH fs/VFIO; crosvm broad | C: board/kernel readiness; F: GIC/device/CoW compatibility; P: no comparable floor. [CH][ch], [CH snapshots][ch-snap], [crosvm][crosvm-snap] |
| libkrun-derived runtimes | Headline figures mainly lack arm-Linux conditions | msb/SmolVM forks supported; latency unknown | virtiofs; GPU where supported | C: host-specific figure absent; F: real dirty-memory/storage workload; P: filesharing/emulation costs and isolation policy. [libkrun][krun], [msb snapshots][msb-snap], [SmolVM][smol] |
| QEMU `virt` / Kata | Unknown; `microvm` is x86-only | Compatible QEMU state; wrapper-dependent | Broader than microvm | C/F: general-machine/runtime costs; P: benchmark native arm binaries; x86 emulation changes baseline. [QEMU][qemu], [Kata][kata-vmm] |

### Linux without KVM

| Candidate | Expected create evidence | Memory-inclusive fork evidence | Devices | What blocks C / F / P |
|---|---|---|---|---|
| gVisor systrap | Unknown current Lamp p50 | Checkpoint/restore, no universal fork time | Syscall ABI, not virtual hardware | C/F: workload/compatibility; P: syscall/I/O overhead can exceed 3%; hardware boundary absent. [Platforms][gvisor-platform], [Performance][gvisor-perf], [Checkpoint][gvisor-cr] |
| Plain containers / Sysbox | Cached lifecycle can be short; current create p50 unknown | CRIU constraints; no blanket <1 s | Shared kernel | C: image/setup; F: process/external-resource capture; P: storage/network workload; hardware boundary absent. [Docker][container-security], [Sysbox][sysbox], [CRIU][criu] |
| QEMU TCG | Unknown; no target evidence | Save/restore possible with compatible machine; unknown | Full emulated machine options | C/F: emulation and capture; P: native-speed target unsupported by evidence. [Accelerators][qemu-accel], [Migration][qemu-migration] |

### macOS Apple Silicon

| Candidate | Expected create evidence | Memory-inclusive fork evidence | Devices | What blocks C / F / P |
|---|---|---|---|---|
| libkrun/HVF via microsandbox or SmolVM | msb <100 ms average M1 guest boot; SmolVM <200 ms agent headline | Local CoW/live branches; measured full-fork latency unknown | fs/net/blk/vsock; optional GPU/snd/input | C: API endpoint and cold images; F: mounts/state implementation; P: no 97% workload proof, foreign-arch translation; host isolation needs audit. [msb][msb], [msb snapshots][msb-snap], [SmolVM][smol], [libkrun][krun] |
| VZ / Matchlock / Lima | Unknown VZ p50; Matchlock <1 s headline | VZ save/restore config-dependent; Linux live-fork contract unconfirmed | Optional VZ devices; no generic PCI GPU passthrough | C: published threshold insufficient; F: Linux save support and Apple binding; P: sharing/graphics workload unmeasured. [Matchlock][matchlock], [VZ validator][vz-validate], [WWDC][vz-wwdc] |
| Tart, for macOS guests | Unknown comparable p50 | macOS suspend/resume; disk clone is separate, live fork unknown | VZ; suspend restricts devices | C/F: macOS startup/state behavior; P: no comparable proof; macOS license/concurrency restrict scale. [Tart run][tart-run], [SLA][mac-license] |
| QEMU `virt` + HVF | Unknown | Compatible-machine migration support must be tested | Broad QEMU device model | C/F: machine/device/capture work; P: architecture and I/O choices. x86 `microvm` is not the native arm machine. [QEMU][qemu], [Accelerators][qemu-accel] |

### Windows 11 / Windows Server

| Candidate | Expected create evidence | Memory-inclusive fork evidence | Devices | What blocks C / F / P |
|---|---|---|---|---|
| Hyper-V / HCS | Linux p50 unknown; Windows Sandbox is seconds | Standard checkpoint/save includes RAM; no generic <1 s clone | VMBus/storage/sockets; Server GPU-P/DDA options | C: image/utility-VM setup; F: no cheap clone contract; P: benchmark I/O/GPU and scheduling. Edition/hardware requirements apply. [HCS][hcs], [Sandbox][win-sandbox], [GPU][hyperv-gpu] |
| WHP via microsandbox / SmolVM | Current Windows p50 unknown; cross-platform headline is not a Windows measurement | msb live fork documented; SmolVM requires frozen source; timing unknown | Runtime-specific virtio graph; GPU restrictions | C: no controlled Windows figure; F: SmolVM parent semantics, state/device constraints; P: no ≥97% evidence. Native Windows arm VMM coverage unconfirmed. [msb snapshots][msb-snap], [SmolVM branching][smol-branch], [Limits][smol-limits], [WHP][whp] |
| QEMU/WHPX / crosvm/WHPX | Unknown | QEMU compatible-machine capture; crosvm snapshots experimental | QEMU machine-dependent; crosvm broad but Windows path untested | C/F: support and capture maturity; P: unmeasured. QEMU microvm is x86; crosvm upstream does not test Windows. [QEMU][qemu], [crosvm hypervisors][crosvm-hv] |
| WSL2 plus inner KVM VMM | Unknown; requires separately verified nesting | Inner VMM capability; WSL export is disk-only | Inner VMM, plus shared outer VM | C/F: extra layer and availability; P: nested overhead; WSL distributions alone lack per-sandbox VM boundary. [WSL][wsl], [Export][wsl-export], [Nesting][hyperv-nested] |

[fc-release]: https://github.com/firecracker-microvm/firecracker/releases/tag/v1.17.0
[fc]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/README.md
[fc-spec]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/SPECIFICATION.md
[fc-snap]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/snapshot-support.md
[fc-random]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/random-for-clones.md
[fc-network]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/snapshotting/network-for-clones.md
[fc-hotplug]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/device-hotplug.md
[fc-memory]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/memory-hotplug.md
[fc-balloon]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/ballooning.md
[fc-config]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/src/vmm/src/vmm_config/machine_config.rs
[fc-jailer]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/jailer.md
[fc-root]: https://github.com/firecracker-microvm/firecracker/blob/v1.17.0/docs/rootfs-and-kernel-setup.md
[ch-release]: https://github.com/cloud-hypervisor/cloud-hypervisor/releases/tag/v54.0
[ch]: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/README.md
[ch-site]: https://www.cloudhypervisor.org/
[ch-snap]: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/snapshot_restore.md
[ch-dev]: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/device_model.md
[ch-vhost]: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/generic-vhost-user.md
[ch-vfio]: https://github.com/cloud-hypervisor/cloud-hypervisor/blob/v54.0/docs/vfio.md
[crosvm]: https://github.com/google/crosvm/blob/f0880b36b6e3f25ee814222e6bd1cead4151bc85/README.md
[crosvm-hv]: https://crosvm.dev/book/hypervisors.html
[crosvm-dev]: https://crosvm.dev/book/devices/index.html
[crosvm-hotplug]: https://github.com/google/crosvm/blob/f0880b36b6e3f25ee814222e6bd1cead4151bc85/docs/book/src/devices/index.md
[crosvm-snap]: https://github.com/google/crosvm/blob/f0880b36b6e3f25ee814222e6bd1cead4151bc85/docs/book/src/architecture/snapshotting.md
[qemu]: https://github.com/qemu/qemu/blob/v11.1.3/docs/system/i386/microvm.rst
[qemu-accel]: https://www.qemu.org/docs/master/system/introduction.html
[qemu-boot]: https://ubuntu.com/server/docs/explanation/virtualisation/qemu-microvm/
[qemu-migration]: https://www.qemu.org/docs/master/devel/migration/main.html
[qemu-dev]: https://www.qemu.org/docs/master/system/device-emulation.html
[qemu-invoke]: https://www.qemu.org/docs/master/system/invocation.html
[qemu-images]: https://www.qemu.org/docs/master/system/images.html
[qemu-license]: https://github.com/qemu/qemu/blob/v11.1.3/LICENSE
[krun]: https://github.com/libkrun/libkrun/blob/v1.19.8/README.md
[krun-api]: https://github.com/libkrun/libkrun/blob/v1.19.8/include/libkrun.h
[krunvm]: https://github.com/libkrun/krunvm/blob/v0.2.7/README.md
[krunvm-license]: https://github.com/libkrun/krunvm/blob/v0.2.7/LICENSE
[muvm]: https://github.com/AsahiLinux/muvm/blob/muvm-0.7.0/README.md
[muvm-license]: https://github.com/AsahiLinux/muvm/blob/muvm-0.7.0/crates/muvm/LICENSE
[muvm-blog]: https://asahilinux.org/2024/12/muvm-x11-bridging/
[vz-linux]: https://developer.apple.com/documentation/virtualization/virtualize-linux-on-a-mac
[vz-mac]: https://developer.apple.com/documentation/virtualization/virtualize-macos-on-a-mac
[vz-config]: https://developer.apple.com/documentation/virtualization/vzvirtualmachineconfiguration
[vz-save]: https://developer.apple.com/documentation/virtualization/vzvirtualmachine/savemachinestateto(url:completionhandler:)
[vz-restore]: https://developer.apple.com/documentation/virtualization/vzvirtualmachine/restoremachinestatefrom(url:completionhandler:)
[vz-validate]: https://developer.apple.com/documentation/virtualization/vzvirtualmachineconfiguration/validatesaverestoresupport()
[vz-wwdc]: https://developer.apple.com/videos/play/wwdc2023/10007/
[vz-limit]: https://developer.apple.com/documentation/virtualization/vzerror/code/virtualmachinelimitexceeded
[hvf]: https://developer.apple.com/documentation/hypervisor
[win-api]: https://learn.microsoft.com/en-us/virtualization/api/
[hcs]: https://learn.microsoft.com/en-us/virtualization/api/hcs/overview
[hcs-dev]: https://github.com/microsoft/hcsshim/blob/main/internal/hcs/schema2/devices.go
[whp]: https://learn.microsoft.com/en-us/virtualization/api/hypervisor-platform/whp-samples
[win-sandbox]: https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/windows-sandbox-architecture
[hyperv-checkpoint]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/checkpoints
[hyperv-socket]: https://learn.microsoft.com/en-us/virtualization/hyper-v-on-windows/user-guide/make-integration-service
[hyperv-memory]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/dynamic-memory
[hyperv-scale]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/maximum-scale-limits
[hyperv-gpu]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/gpu-partitioning
[hyperv-guests]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/supported-windows-guest-operating-systems-for-hyper-v-on-windows
[hyperv-nested]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/enable-nested-virtualization
[wsl]: https://learn.microsoft.com/en-us/windows/wsl/compare-versions
[wsl-config]: https://learn.microsoft.com/en-us/windows/wsl/wsl-config
[wsl-export]: https://learn.microsoft.com/en-us/windows/wsl/reference
[kata]: https://github.com/kata-containers/kata-containers/blob/4.3.1/docs/design/architecture/README.md
[kata-vmm]: https://github.com/kata-containers/kata-containers/blob/4.3.1/docs/design/virtualization.md
[kata-project]: https://katacontainers.org/software/
[gvisor-platform]: https://gvisor.dev/docs/architecture_guide/platforms/
[gvisor-arch]: https://gvisor.dev/docs/architecture_guide/intro/
[gvisor-perf]: https://gvisor.dev/docs/architecture_guide/performance/
[gvisor-cr]: https://gvisor.dev/docs/user_guide/checkpoint_restore/
[container-security]: https://docs.docker.com/engine/security/
[criu]: https://criu.org/Checkpoint/Restore
[sysbox]: https://github.com/nestybox/sysbox/blob/master/README.md
[sysbox-security]: https://github.com/nestybox/sysbox/blob/master/docs/user-guide/security.md
[sysbox-perf]: https://blog.nestybox.com/2020/09/23/perf-comparison.html
[aws-nested]: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/amazon-ec2-nested-virtualization.html
[aws-nested-news]: https://aws.amazon.com/about-aws/whats-new/2026/06/nested-virtualization-intel-us-gov-cloud/
[gcp-nested]: https://docs.cloud.google.com/compute/docs/instances/nested-virtualization/overview
[azure-dv5]: https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dv5-series
[azure-dasv5]: https://learn.microsoft.com/en-us/azure/virtual-machines/sizes/general-purpose/dasv5-series
[azure-nested-support]: https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/nested-virtualization
[hetzner]: https://docs.hetzner.com/cloud/servers/faq/
[hetzner-metal]: https://www.hetzner.com/dedicated-rootserver/
[do-nested]: https://www.digitalocean.com/community/questions/does-digitalocean-support-nested-kvm-now
[oci-nested]: https://blogs.oracle.com/cloud-infrastructure/firecracker-oci-vm-vs-bm
[oci-migrate]: https://docs.oracle.com/en-us/iaas/Content/Compute/Tasks/movinganinstance.htm
[oci-layer]: https://github.com/opencontainers/image-spec/blob/main/layer.md
[overlayfs]: https://docs.kernel.org/filesystems/overlayfs.html
[stargz]: https://github.com/containerd/stargz-snapshotter
[nydus]: https://github.com/containerd/nydus-snapshotter
[soci]: https://github.com/awslabs/soci-snapshotter
[overlaybd]: https://github.com/containerd/overlaybd
[xfs]: https://man7.org/linux/man-pages/man8/mkfs.xfs.8.html
[btrfs]: https://btrfs.readthedocs.io/en/latest/Reflink.html
[dmthin]: https://docs.kernel.org/admin-guide/device-mapper/thin-provisioning.html
[zfs]: https://openzfs.github.io/openzfs-docs/Basic%20Concepts/Datasets/Snapshots%20and%20Clones.html
[apfs]: https://developer.apple.com/library/archive/documentation/FileManagement/Conceptual/APFS_Guide/ToolsandAPIs/ToolsandAPIs.html
[refs]: https://learn.microsoft.com/en-us/windows-server/storage/refs/block-cloning
[e2b-runtime]: https://github.com/e2b-dev/runtime
[e2b-time]: https://changelog.e2b.dev/customers/lindy-powers-ai-workflows-with-e2b-code-action
[cs-uffd]: https://codesandbox.io/blog/cloning-microvms-using-userfaultfd
[cs-fork]: https://codesandbox.io/blog/how-we-clone-a-running-vm-in-2-seconds
[cs-maintainer]: https://news.ycombinator.com/item?id=43655010
[fly]: https://fly.io/blog/fly-machines/
[fly-api]: https://fly.io/docs/machines/api/machines-resource/
[fly-time]: https://fly.io/docs/machines/guides-examples/functions-with-machines/
[fly-suspend]: https://community.fly.io/t/new-feature-in-preview-suspend-resume-for-machines/20672
[sbx-install]: https://docs.docker.com/ai/sandboxes/install/
[sbx]: https://docs.docker.com/ai/sandboxes/architecture/
[sbx-release]: https://docs.docker.com/ai/sandboxes/release-notes/
[msb]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/README.md
[msb-dep]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/crates/runtime/Cargo.toml
[msb-snap]: https://github.com/superradcompany/microsandbox/blob/v0.7.14/docs/sandboxes/snapshots.mdx
[smol]: https://github.com/smol-machines/smolvm/blob/v1.34.0/README.md
[smol-dep]: https://github.com/smol-machines/smolvm/blob/v1.34.0/.gitmodules
[smol-branch]: https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/branching.md
[smol-limits]: https://github.com/smol-machines/smolvm/blob/v1.34.0/docs/limitations.md
[matchlock]: https://github.com/jingkaihe/matchlock/blob/v0.3.1/README.md
[lima]: https://lima-vm.io/docs/config/vmtype/
[lima-vz]: https://lima-vm.io/docs/config/vmtype/vz/
[orb]: https://docs.orbstack.dev/architecture
[orb-machines]: https://docs.orbstack.dev/machines/
[tart]: https://github.com/openai/tart/blob/2.41.1/README.md
[tart-run]: https://github.com/openai/tart/blob/2.41.1/Sources/tart/Commands/Run.swift
[tart-license]: https://github.com/openai/tart/blob/2.41.1/LICENSE
[tart-limit]: https://github.com/openai/tart/blob/2.41.1/docs/blog/posts/2023-04-25-orchard-ga.md
[snap]: https://docs.aws.amazon.com/lambda/latest/dg/snapstart.html
[snap-container]: https://aws.amazon.com/blogs/compute/get-started-with-aws-lambda-snapstart-for-container-images/
[snap-launch]: https://aws.amazon.com/blogs/aws/new-accelerate-your-lambda-functions-with-lambda-snapstart/
[snap-unique]: https://docs.aws.amazon.com/lambda/latest/dg/snapstart-uniqueness.html
[snap-practices]: https://docs.aws.amazon.com/lambda/latest/dg/snapstart-best-practices.html
[bench]: https://github.com/superradcompany/sandbox-bench/blob/main/benches/boot-time/README.md
[mac-license]: https://www.apple.com/legal/sla/docs/macOSTahoe.pdf
