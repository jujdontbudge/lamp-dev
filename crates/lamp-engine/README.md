# lamp-engine (Linux)

The Linux engine behind `lamp-supervisor`. It turns a signed Lamp template
into Firecracker microVMs, runs commands in them, and forks a running sandbox
with its memory. It is driven only through `lamp.worker/1`
(`contracts/worker/README.md`); `lampd` never calls it directly. Nothing here
freezes the template or saved-state layout (T7–T9 prove storage, T14 freezes).

## Pieces

| Piece | Where | Job |
| --- | --- | --- |
| `lamp-supervisor` | `crates/lamp-supervisor` | Root daemon: `lamp.worker/1` server, control grants, `seed-import`, `template-build`, `templates`, and `ctl` test/bench client. |
| engine | `src/engine.rs` | Sandboxes, templates, captures; allocate/restore/exec/capture/stop/release. |
| jail + API | `src/fc.rs` | Firecracker v1.17.0 under the jailer; HTTP-over-UDS client. |
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
`/mem.d/…`), which is what lets saved state move between jails.

Firecracker is the only host process that touches guest memory: there is no
page server or other helper outside the jail. Store directories
(`/var/lib/lamp/jail`, `captures`, `grants`, `keys`) and the memory tmpfs
(`/run/lamp/mem`, mounted `mode=0700`) are root-only. Capture images are
root:root 0400; a child's VMM gets its own hard link in its 0700 memory
directory, owned by the child's UID and read-only, and nothing else.

## Store ownership

`store/lock` is an exclusive `flock`. `serve` takes it and recovers the store
(kills leftover jails, removes volatile captures). `seed-import` and
`template-build` take it without recovering anything, so they refuse to run
while the service is up (`RESOURCE_IN_USE: … stop lamp-supervisor first`) and
never touch a live sandbox. `templates` takes no lock and writes nothing.

## Store

The store is any local directory. Seeds, templates and private disks live
there, and the disk backend follows the filesystem (`src/disk.rs`):

- XFS with `reflink=1` or btrfs: every sandbox disk is a FICLONE reflink of
  its template or source disk (the installed `/var/lib/lamp` on Ubuntu LX is
  a sparse XFS loop image).
- Ordinary ext4, or anything else without reflinks: Lamp runs its own dm-thin
  pool on two sparse loop-backed files inside the store, and every disk is a
  thin snapshot. Nobody reformats anything. `--disk-backend thin|reflink`
  overrides the choice, and `--thin-data-gib` sizes the pool.

Either way, creating a disk costs milliseconds and writes stay private. Templates are content-addressed: the
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
   template memory, no dirty tracking, resume, realtime clock);
3. send the identity barrier over vsock: sandbox ID, generation, hostname,
   machine-id, 64 fresh bytes mixed into the guest CRNG
   (`RNDADDENTROPY` + `RNDRESEEDCRNG`) and the host clock;
4. reply `restored` only after the barrier is acknowledged.

The guest refuses any exec whose sandbox ID/generation differ from its last
barrier, so a restored copy can never run work as its parent. Commands start
in the guest cgroup `/sys/fs/cgroup/tasks` (joined before exec; a command that
cannot join is never exec'd), but guest root may move anywhere in the
hierarchy, so the capture freeze does not rely on it (see Memory fork). A
template is captured frozen, and the barrier thaws only after the new
identity, clock and reseeded CRNG are in place.

## Memory fork

Every fork captures the source's **whole** configured RAM. Nothing decides by
dirty tracking what the child receives. Fork:

1. freeze every guest process except init and the helper: every top-level
   cgroup gets `cgroup.freeze=1` (including ones the guest created, such as
   Docker's; descendants freeze with them), and every process in the root
   cgroup, which cannot be frozen, moves into the frozen `lamp-hold` cgroup.
   This repeats until a pass changes nothing and the kernel reports every
   cgroup `frozen 1`; then nothing runs, so nothing can move. Thaw undoes
   exactly that record and returns held processes to the root cgroup;
2. pause the source;
3. Firecracker **Full** snapshot of all 4 GiB into `/mem.d/cap.mem`, a bind
   mount of the root-only tmpfs `/run/lamp/mem` (`huge=always`), so no disk
   writeback is involved;
4. reflink the source disk; resume the source and thaw its tasks;
5. seal the image as `captures/<id>.mem`/`.vmstate`, root:root 0400.

All of it is inside the timed fork (`capture.freeze_us`, `full_write_us`,
`disk_seal_us`, `thaw_us`, `source_pause_us`, `memory_bytes`). While the
snapshot is written the VMM runs in the shared `lamp/layers` cgroup, so the
image's tmpfs pages are charged there and not to the source's `memory.max`.

The child loads the image with the File backend (`MAP_PRIVATE` over its
read-only link). Its processes resume frozen and stay frozen until its own
identity barrier (the freeze record is in the helper's memory, so the child
thaws the same set), so a process that was already running in the parent never
runs in the child before the child's machine-id, hostname, clock and CRNG are
new. A capture is restored once, then its image is freed.

Commands are not tied to a connection. Firecracker resets the guest's vsock
connections when it snapshots a VM, so completion is fetched with short,
idempotent `Wait` requests by exec ID; a command started before a fork still
completes in the source. The waiter ends when that VMM process is gone
(stop, release, crash), and the command's state becomes `unknown`. Running
commands are never forgotten (at most 1,024 run per sandbox); the newest 256
completed ones are kept. A freeze whose result is uncertain is always
followed by a thaw; a sandbox whose thaw cannot be confirmed is marked
failed.

## Durable captures (`local`)

`consistency: local` commits the capture to the store before it is reported
(`src/durable.rs`; architecture §6.4, §7):

```text
<store>/state/journal     preallocated, framed, append-only commit records
<store>/state/objects/    memory deltas, vm state, reflink disks
```

The commit order is reserve → capture → seal → object_flush →
directory_flush → heads → switch → journal_flush → publish → ack. After the
ack come delete → gc → compact_objects → compact_redirect → compact_gc.
Each step is a crash point of E04 (`--features fault-injection`, admin
`state fault POINT SIDE [OMIT]`). The steps work as follows:

- **Memory.** The memory object is a delta against the parent's image: a
  header, a page bitmap and the changed pages. The pages are written with
  O_DIRECT straight from the tmpfs image, and the `sha256-c4m` digest is
  computed in memory alongside (`src/memimg.rs`).
- **Disk.** A thin disk is a pool snapshot plus a forced pool metadata
  commit. A reflink disk is a clone plus fsync.
- **Commit record.** It carries the whole signed manifest and is written
  over already-allocated journal blocks, so its fdatasync (`journal_flush`)
  syncs no filesystem metadata. On btrfs, `object_flush` ends with `syncfs`,
  because a later fsync there would otherwise also persist renames
  (`btrfs_log_new_name`). Together these mean that only its own barrier
  makes each step durable, so an omitted barrier is visible and is caught.
- **Restart.** A restart replays committed records only. It verifies each
  object against the signed manifest, and it quarantines a capture whose
  producer key is not enrolled (`--trust-producer HEX` enrols another
  host's key).
- **Lineage.** A capture with a live descendant cannot be deleted. GC
  removes unreferenced objects. Compaction re-encodes a chained capture's
  memory against its template and atomically redirects the capture to the
  new object (a signed redirect record), so its old delta and bases can go.

Suspend is a durable capture followed by release (`ctl suspend`). Resume is
a create from that capture, after any restart or crash (`ctl create CAPTURE`).
Evidence: `docs/development/T7-evidence.md`.

## Guest protocol

Host↔guest messages are 4-byte length-prefixed JSON on vsock port 1024
(`lamp_init::GuestRequest`/`GuestResponse`; ping, barrier, exec, wait,
freeze, thaw, shutdown; unknown fields rejected). This is an internal channel
and not frozen. Exec replies once the program was exec'd; it runs in its own
process group (optional timeout, none for `Start`) and keeps the last 64 KiB
of stdout and stderr. Failures before a request was written are
`HOST_UNAVAILABLE`; a lost reply after it is `OUTCOME_UNKNOWN`.

## Running it

```sh
infra/linux/install-engine.sh 300 $(id -u)        # Firecracker, store, service
images/linux-seed/build-seed.sh OUT_DIR             # kernel + Debian 13 seed
sudo systemctl stop lamp-supervisor                 # builds own the store exclusively
sudo lamp-supervisor seed-import OUT_DIR
sudo lamp-supervisor template-build images/templates/demo.lamp   # after filling @SEED_REF@
sudo systemctl start lamp-supervisor
export LAMP_GRANT_ID=$(sudo lamp-supervisor grant-install --caller-uid $(id -u) | jq -r .grant_id)
lamp-supervisor ctl create demo && lamp-supervisor ctl exec SB -- uname -a
LAMP_E2E_SOCKET=/run/lamp/worker.sock LAMP_E2E_GRANT=$LAMP_GRANT_ID \
  cargo test --release -p lamp-supervisor --test e2e
```

The service stops on SIGTERM or SIGINT: both are blocked before any thread
exists and taken only by one waiter thread, which stops every sandbox and
returns once each VMM process has exited.

## Known gaps

- There is no LSM profile; the jailer's UID, chroot, namespaces, cgroup and
  Firecracker's own seccomp filter are the boundary.
- Volatile captures and sandboxes live only as long as the supervisor; a
  restart discards them (by design for `volatile`). `local` captures survive
  restarts; `replicated`/`backup` durability does not exist yet.
- No network for builds or sandboxes until the T4 guard.
- Handle grants are verified by the protocol crate but not yet accepted by
  this engine.
- The kernel boot ID is the template's in every restored sandbox (expected
  for snapshot restore); identity comes from the barrier instead.
