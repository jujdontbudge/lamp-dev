# lamp-engine (Linux)

The Linux engine behind `lamp-supervisor`. It turns a signed Lamp template
into Firecracker microVMs, runs commands in them, and forks a running sandbox
with its memory. It is driven only through `lamp.worker/1`
(`contracts/worker/README.md`); `lampd` never calls it directly. Nothing here
freezes the template or saved-state layout (T7–T9 prove storage, T14 freezes).

## Pieces

| Piece | Where | Job |
| --- | --- | --- |
| `lamp-supervisor` | `crates/lamp-supervisor` | Root daemon: `lamp.worker/1` server, `seed-import`, `template-build`, and `ctl` test/bench client. |
| engine | `src/engine.rs` | Sandboxes, templates, captures; allocate/restore/exec/capture/stop/release. |
| jail + API | `src/fc.rs` | Firecracker v1.17.0 under the jailer; HTTP-over-UDS client. |
| memory layers | `src/layer.rs` | Immutable memory layers and background compaction. |
| `lamp-pager` | `src/bin/lamp-pager.rs` | Per-sandbox userfaultfd page server for forks. |
| template build | `src/build.rs`, `definition.rs`, `manifest.rs` | Docker-free build from a seed, canonical manifest, signed provenance. |
| guest | `crates/lamp-init` | Static musl PID 1 (`lamp-init`) and vsock server (`lamp-guest-helper`). |
| seed | `images/linux-seed/build-seed.sh` | Pinned kernel 6.18 + Debian 13 root built with mmdebstrap. |
| host setup | `infra/linux/` | Install script, store mount, systemd unit. |

## Confinement

Every sandbox gets its own Firecracker process started by the jailer with a
fresh UID/GID (from 900000), a chroot under
`/var/lib/lamp/jail/firecracker/<jail>/root`, a new PID namespace, its own
network namespace (no interfaces: builds and sandboxes have no network until
the T4 guard exists) and a cgroup v2 leaf under `lamp/` with `memory.max`
(guest memory + 1 GiB), `pids.max` and `cpu.max`. Teardown uses
`cgroup.kill`, so nothing in the jail survives. Firecracker always sees the
same in-jail paths (`/rootfs.ext4`, `/v.sock`, `/api.sock`, `/vmlinux`,
`/mem.d/…`, `/uffd.sock`), which is what lets saved state move between jails.

## Store

`/var/lib/lamp` is XFS with `reflink=1` (on Ubuntu LX: a sparse loop image).
Seeds, templates and private disks live there; every sandbox disk is a
FICLONE reflink of its template or source disk, so creating one costs
milliseconds and writes stay private. Templates are content-addressed: the
object digest `sha256-c4m` is SHA-256 over the SHA-256 of each 4 MiB chunk
(holes skipped), the manifest is canonical JSON (`lamp.template/v1`), and
the template ID is `tp_` + the first 26 hex characters of the manifest
digest. Provenance is signed with the host's Ed25519 producer key; a
template is accepted only when the key is enrolled, the signature verifies,
every object digest matches and its engine digest, CPU baseline and state ABI
match this host.

## Create

A template carries one acceleration variant: a Full Firecracker snapshot of
the booted standard sandbox (2 vCPU, 4 GiB, 20 GiB), hole-punched. Create:

1. reflink the template disk into a new jail;
2. load the snapshot with the File backend (`MAP_PRIVATE` over the immutable
   template memory, dirty tracking on, resume, realtime clock);
3. send the identity barrier over vsock: sandbox ID, generation, hostname,
   machine-id, 64 fresh bytes mixed into the guest CRNG
   (`RNDADDENTROPY` + `RNDRESEEDCRNG`) and the host clock;
4. reply `running` only after the barrier is acknowledged.

The guest refuses any exec whose sandbox ID/generation differ from its last
barrier, so a restored copy can never run work as its parent.

## Memory fork

A sandbox's memory is a stack of immutable layers: the bottom one is the
complete template image, the ones above are sparse dirty-page layers. Fork:

1. pause the source;
2. Firecracker Diff snapshot (pages dirtied since the last snapshot) into
   `/mem.d/cap.mem`, which is a bind mount of a tmpfs
   (`/run/lamp/mem`), so no disk writeback is involved;
3. reflink the source disk; resume the source;
4. seal the diff as a new read-only layer and record the capture
   (layer stack + vmstate).

The child restores from the same stack through Firecracker's UFFD backend.
Its `lamp-pager` receives the layer files as inherited descriptors, binds the
jail's `/uffd.sock`, drops to the sandbox UID and resolves each page fault
with `UFFDIO_COPY` from the topmost layer that holds the page (`SEEK_DATA`).
Source and child then each receive their own identity barrier. A background
task compacts stacks (at most two diff layers; covered layers are pruned).
Layers are reference-counted, so releasing the source never affects a fork.

Why not reflink the memory file instead: on XFS, FICLONE flushes the
source's dirty page cache first. With the Ubuntu host short on memory that
produced 0.7–24 s stalls; tmpfs layers avoid writeback entirely.

`consistency: local` additionally copies the top layer, vmstate and disk into
the store and fsyncs them before the capture is reported.

## Guest protocol

Host↔guest messages are 4-byte length-prefixed JSON on vsock port 1024
(`lamp_init::GuestRequest`/`GuestResponse`; ping, barrier, exec, shutdown;
unknown fields rejected). This is an internal channel and not frozen. Exec
runs in its own process group with a timeout and keeps the last 64 KiB of
stdout and stderr.

## Running it

```sh
infra/linux/install-engine.sh 300 $(id -u)        # Firecracker, store, service
images/linux-seed/build-seed.sh OUT_DIR             # kernel + Debian 13 seed
sudo lamp-supervisor seed-import OUT_DIR
sudo lamp-supervisor template-build images/templates/demo.lamp   # after filling @SEED_REF@
lamp-supervisor ctl create demo && lamp-supervisor ctl exec SB -- uname -a
LAMP_E2E_SOCKET=/run/lamp/worker.sock cargo test --release -p lamp-supervisor --test e2e
```

## Known gaps

- `lamp-pager` has no seccomp filter and there is no LSM profile yet; the
  jail, UID, namespaces and cgroup are the boundary.
- Volatile captures, layers and sandboxes live only as long as the
  supervisor; a restart discards them (by design for `volatile`). Nothing
  reloads `local` captures after a restart yet, and a `local` capture of a
  sandbox that is itself a fork does not copy the lower volatile layers.
- No network for builds or sandboxes until the T4 guard.
- Handle grants are verified by the protocol crate but not yet accepted by
  this engine.
- The kernel boot ID is the template's in every restored sandbox (expected
  for snapshot restore); identity comes from the barrier instead.
