# T20 Linux support and devices — evidence, 2026-11-11

Owner rule of 2026-11-10: one real end-to-end demonstration, almost no tests,
no invented gates. No tests were added and nothing here measures time. Every
run used disposable state, public third-party media (Alpine minirootfs, gVisor
and Firecracker official releases) and the existing Lamp template; no owner
project, vault item or Docker resource was used or touched. gVisor runs
standalone: nothing was registered with Docker or containerd.

## What was built

| Feature | Where |
| --- | --- |
| Caller-chosen CPUs, memory and disk. A shape that differs from the template's prepared image cold-boots the template's own kernel and disk at exactly that shape; the prepared image stays the fast path. | `engine.rs` `restore`/`cold_boot` |
| Caller-chosen process limit: `pids.max` of the guest's tasks cgroup, applied after the identity barrier on every create/fork/resume | `engine.rs` `apply_guest_limits` |
| Requested devices on the public create API (`devices`), carried to the worker (`Allocate.devices`, worker proto field 4). Firecracker attaches extra disks (raw, sparse, private, owned by the task user in the guest); GPU, display, input and audio are refused with `DEVICE_UNAVAILABLE` before anything is allocated. Captures of a sandbox with extra disks are refused (they would not carry the disk). | `http.go` `workerDevices`, `server.rs`, `engine.rs` |
| Host-capacity admission without overcommit: guest RAM + 256 MiB must fit both physical RAM less a 4 GiB host reserve and current `MemAvailable`; total vCPUs must fit online CPUs; disks must fit store free space. Refused with `NO_CAPACITY` before any jail exists. | `engine.rs` `admit` |
| Durable deltas of differently sized sandboxes are encoded against a sparse all-zero base of the same size (the template image when sizes match), so cold-booted sandboxes suspend/resume durably | `engine.rs` `template_base` |
| Engine selection: Firecracker → VZ → gVisor → App Sandbox, fastest first, respecting OS, guest OS, KVM usability (opened and `KVM_GET_API_VERSION`=12), kernel ≥ 5.10, devices, capture, secrets and public CI | `select.rs`, `lamp-supervisor engines` |
| The weaker gVisor fallback: one bounded task in standalone `runsc`, read-only root, no network, no host UDS, nobody user, no capabilities, memory limit, `RLIMIT_NPROC` process limit; refuses on hosts with a usable VM engine unless the custodian opts in; refuses logins/protected connections | `gvisor.rs`, `lamp-supervisor gvisor-task` |
| lampd's trusted native route accepts `runtime: gvisor`, keeping the custodian-enrolled job authority: public-repository CI and login/protected tasks are `VM_REQUIRED`; every answer carries `X-Lamp-Boundary: gvisor_sandbox`, `X-Lamp-Isolation: weaker_than_vm` | `native.go` |
| Installers: `install-engine.sh` picks the x86_64 or aarch64 Firecracker build; new `install-gvisor.sh` (checksum-pinned, x86_64/aarch64, works without `bzip2`). The supervisor builds as a static musl binary (one `ioctl` cast fixed) so it runs on older glibc distributions. | `infra/linux/`, `dm.rs` |

## LX (Ubuntu 26.04, kernel 7.0, x86_64) — real Firecracker sandboxes through the public API

Driver: [`evidence/t20/demo.py`](evidence/t20/demo.py) with
[`lamp_env.py`](evidence/t20/lamp_env.py) (T19's environment, own store
`/var/lib/lamp/t20-private`, own UID base). Final run
[`vm/transcript.jsonl`](evidence/t20/vm/transcript.jsonl), `lampd`
`d5d0bf3d…7505ab`, `lamp-supervisor` `d24449e9…479d85`. The LX budget
(fewer than 2 Firecracker processes, ≥ 12 GiB available) was checked before
every create and before the fork; every sandbox was deleted, no captures were
left and the final count of our Firecracker processes was 0.

* **Caller-chosen shape + extra disk.** `resources {vcpus 4, memory_mib 3072,
  disk_gib 24, pids_max 200}`, `devices [{id scratch, type disk, size_mib 512}]`
  on the 2 vCPU / 4 GiB / 20 GiB template. Inside the guest: `nproc=4`,
  `MemTotal 3066868 kB`, `vda 24576 MiB`, `vdb 512 MiB`, `pids.max=200`.
  A random 4 KiB block written to `/dev/vdb` read back identically
  (`extra-disk-roundtrip-ok`).
* **Process limit.** Forking paused children stopped at 194 children with
  `EAGAIN` and `pids.current=200`.
* **Refused before any work.** Forking the extra-disk sandbox:
  `DEVICE_UNAVAILABLE` (captures do not carry extra disks). Creating with a
  GPU: `DEVICE_UNAVAILABLE` ("Firecracker has no GPU passthrough or virtio-gpu
  device"). 40,960 MiB: `NO_CAPACITY` (31,140 MiB physical, 4,096 reserved,
  held 3,328). 16 vCPUs: `NO_CAPACITY` (8 CPUs, 4 held).
* **Fork, durable suspend/resume, guard on a caller-shaped sandbox.**
  `{vcpus 2, memory_mib 2048, disk_gib 20, pids_max 512}` (cold boot, since the
  template image is 4 GiB). A process held a random nonce only in memory. The
  fork answered the same nonce (`6d8b51bf…8692d`) with a new machine ID and the
  same shape. After `sandbox_suspend` (no VMM left) and `sandbox_resume`, the
  same process answered the same nonce with uptime continuing (1.64 s →
  364.30 s): a durable memory restore of a 2 GiB image through the zero base.
  Egress to `example.com` was refused by the guard network; commands ran
  audited by the guest collector.

## LX — the gVisor fallback through lampd

Final run [`gvisor/transcript.jsonl`](evidence/t20/gvisor/transcript.jsonl).
LX has KVM, so the custodian's file set `allow_with_vm: true` for this
demonstration (without it the launcher refuses, see the lanes).

| Request | Result |
| --- | --- |
| Owner task | 200, `boundary gvisor_sandbox`, `isolation weaker_than_vm`; guest kernel `4.19.0-gvisor`, uid 65534, no network; with `pids_max 48`, 46 background processes started before refusal (+2 shells) |
| Private-repository CI job (enrolled credential) | 200, same labels, job `jb_private` |
| Public-repository CI job | 403 `VM_REQUIRED` |
| Owner task with `login` / `protected_connection` | 403 `VM_REQUIRED` |

## Nested lanes on LX: Debian 11 (5.10), Ubuntu 22.04, Debian 12, Fedora 40, NV (no virtualization)

[`evidence/t20/lanes.sh`](evidence/t20/lanes.sh) booted each existing libvirt
lane from its `clean` snapshot, one at a time, ran `infra/linux/install-gvisor.sh`,
copied in the static supervisor (`bd9d691f…87ce1`) and Firecracker, recorded
detection, selection and gVisor tasks, then powered it off and reverted it to
`clean`. Raw records: [`evidence/t20/lanes/`](evidence/t20/lanes/).

| Lane | OS | Kernel | KVM | Default engine | Fallback task, default | With custodian override | Secrets + public CI | Capture | Login task |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| NV | Ubuntu 24.04.5 | 6.8.0-147 | none | **gvisor** | exit 0 | exit 0 | `VM_REQUIRED` | `ENGINE_UNQUALIFIED` | `VM_REQUIRED` |
| L510 | Debian 11 | 5.10.0-46 | API 12 | firecracker | `ENGINE_UNQUALIFIED` (VM available) | exit 0 | firecracker | firecracker | `VM_REQUIRED` |
| LX22 | Ubuntu 22.04.5 | 5.15.0-203 | API 12 | firecracker | `ENGINE_UNQUALIFIED` | exit 0 | firecracker | firecracker | `VM_REQUIRED` |
| D12 | Debian 12 | 6.1.0-54 | API 12 | firecracker | `ENGINE_UNQUALIFIED` | exit 0 | firecracker | firecracker | `VM_REQUIRED` |
| F40 | Fedora 40 | 6.8.5-301 | API 12 | firecracker | `ENGINE_UNQUALIFIED` | exit 0 | firecracker | firecracker | `VM_REQUIRED` |

On NV, a real host without virtualization, the fallback ran with no
override; secrets, public CI and memory capture had no qualifying engine.

## LA (Ubuntu 26.04 arm64 on the M5, nested KVM)

The supervisor was built natively for aarch64 inside the running LA guest
(own directory only; its CI runner was left alone). With the official
aarch64 runsc and Alpine root, a gVisor task returned
`Linux 4.19.0-gvisor aarch64`, uid 65534, no network
([`la-no-firecracker.json`](evidence/t20/la/la-no-firecracker.json); with no
Firecracker installed, selection chose gVisor and secrets/public CI were
`VM_REQUIRED`). With the official Firecracker v1.17.0 aarch64 binary
present, selection chose Firecracker, including for capture plus a disk
device, and the fallback refused by default
([`la-with-firecracker.json`](evidence/t20/la/la-with-firecracker.json)).
These LA records came from the tree before the last fallback fix (the
`RLIMIT_NPROC` change and the `--firecracker` flag message); they were not rerun.

## Review fix: gVisor task cleanup

The review of `d78ed4f` found that every fallback task left its state
directory and runsc root on the `/run` tmpfs, and that some failure paths
left the bundle or a running container behind. Each task now has a private
directory under the state directory. A guard runs on every exit path: it
kills and deletes the container and reaps runsc. It also detaches the
`null-netns` bind mount that runsc `--network=none` leaves in its root and
never unmounts (the reason the leftovers could not even be removed). Then it
removes the task directory and the state directory if it is empty.

Before the fix, LX had 7 leaked entries under `/run/lamp-gvisor`, each with a
busy `null-netns` mount. They were detached and removed. One run of the
gVisor part through lampd with the fixed supervisor (`167eed35…a0b25a`,
[`gvisor-cleanup/transcript.jsonl`](evidence/t20/gvisor-cleanup/transcript.jsonl))
served the owner and private-CI tasks and refused public CI, login and
protected-connection tasks as before. Afterwards `/run/lamp-gvisor` was empty
and no mount under it remained.

## Not run, and why

* **Firecracker sandboxes on Debian, Fedora, Ubuntu 22.04, the 5.10 kernel
  and arm64.** Only LX ran real Lamp sandboxes. The lanes are 4 GiB nested
  guests with no Lamp store; there is no arm64 guest kernel/template. The lanes
  show detection, selection and the fallback only.
* **GPU acceleration on Linux.** Firecracker has no GPU device; LX's Intel Arc
  GPU is not exposed. GPU requests are refused, not emulated.
* **Display, input and audio on Linux.** Refused (`DEVICE_UNAVAILABLE`).
* **gVisor fork, suspend or durable capture.** The fallback runs bounded tasks
  only; selection never picks it for capture.
* No owner-machine Debian/Fedora or arm64 hardware exists; nothing was rented.

## Observed during development (kept, not relabeled)

* The guard collector's intermittent **exit 70** (T10/T19) appeared in
  development runs on both cold-booted and prepared-image sandboxes, including
  a plain `true` on a fresh prepared-image sandbox. The demo logs each one as a
  failure (`collector_exit70_failures`) and reissues that idempotent command
  once; the final VM run had none. It is not fixed here.
* Early runs failed because the guest's tasks cgroup lacked the `pids`
  controller (now enabled first), extra disks were root-only in the guest
  (now owned by the task user), and the OCI `pids` limit under runsc counted
  gVisor's own host threads (now `RLIMIT_NPROC` inside the sandbox).
  `install-gvisor.sh` first failed on lanes without `bzip2`.

## Commands run for the changed code

```sh
cargo clippy -p lamp-engine -p lamp-supervisor --all-targets --locked -- -D warnings   # Linux (LX) and macOS
cargo build --release --locked --target x86_64-unknown-linux-musl -p lamp-supervisor  # LX
go build ./... && go vet ./internal/control ./cmd/lampd
python3 tools/protobuf.py   # protoc 35.0, regenerated worker bindings
```
