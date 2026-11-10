# lamp.worker/1

`worker.proto` is the local control contract. Both bindings are checked in;
ordinary builds need no Protobuf compiler. Regeneration uses protoc **35.0**,
protoc-gen-go **1.36.11**, and locked prost-build **0.14.4**:

```sh
make generate
make check-generated
```

A frame is a four-byte unsigned **big-endian** length followed by exactly that
many Protobuf `Frame` bytes. The length excludes the prefix. Zero and lengths
above 1,048,576 are invalid. Readers reject the prefix before allocating the
body. Writers check encoded size before writing any bytes. Truncated/malformed
frames close the channel. Serialize writers; a partially failed write requires
closing the connection, never retrying on the same byte stream.

The receiver authenticates the Unix peer's OS credentials before reading any
control request (Linux SO_PEERCRED; Darwin LOCAL_PEERPID/getpeereid). The first
frame in each direction is Hello with protocol exactly `lamp.worker/1`, an
assigned worker identity, current boot identity and a nonzero control maximum
no larger than 1 MiB. Negotiate the smaller maximum. An unknown version or
unassigned peer closes the connection. Framing helpers deliberately do not
perform peer authentication, negotiation or authorization; those checks belong
to the T2 supervisor/worker and T3 client before command dispatch.

Each Request has Context: request/operation/sandbox IDs, generation, deadline,
grant and policy revision. Supervisor-global operations may omit sandbox and
generation; sandbox commands require all fields, a live signed grant, exact
current generation and matching policy revision. `deadline_ms` is an absolute
Unix-millisecond deadline, checked against the qualified authority clock and
suspend-aware local expiry enforcement. A new request ID on retry preserves the
operation ID. A worker deduplicates that operation and reports its prior receipt;
if an effect is uncertain it reports outcome_unknown and never redelivers it.
Go alone decides operations, authority, placement and quotas. Rust independently
checks grants, generation, handle ownership, expiry and hard local limits.

HandleGrant identifies a typed descriptor index in the frame's SCM_RIGHTS
ancillary data. Bind the grant to receiver worker ID, OS UID/PID, boot ID,
sandbox, generation, policy and expiry. Verify Ed25519 signature under the
supervisor's enrolled issuer key over deterministic Protobuf HandleGrant with
signature cleared. Reject unknown fields for signed grants. Descriptor indices
must be unique, in range, of the declared OS handle type and consumed once;
a copied grant cannot transfer authority. Reject extras and close all received
descriptors on any rejection. No handle is a caller-supplied host path. T2
implements the OS credential/SCM_RIGHTS/signature/consumption mechanics, not T1.

RAM, disk, packet, PTY, media and bulk bytes use their own admitted bounded
streams/handles, never control messages or the replicated log. Local limits may
narrow the public stream limits. Artifact handles and Compatibility describe
opaque engine artifacts: neither template nor saved-state storage layout is
frozen here. T7–T9 establish evidence; T14 freezes those formats.

## Linux engine (T2) behaviour

`lamp-supervisor serve` is the Linux worker. It speaks exactly the contract
above; this section records what that engine does with each command so T3 can
call it without reading engine code. None of it freezes a storage format.

**Connection.** The socket (default `/run/lamp/worker.sock`) is mode 0666;
authorization is the peer UID, checked against `--allow-uid` before Hello.
The worker sends Hello first, then requires a valid client Hello carrying no
descriptors. Requests on one connection run concurrently; responses carry the
request's Context and may arrive out of order.

**Checks before any effect.** Missing context, command, request or operation
ID → `INVALID_ARGUMENT`. Past `deadline_ms` → `RESULT_EXPIRED`. Sandbox
commands need `sandbox_id`, nonzero `generation` and nonempty `grant_id`; only
`Inspect` with an empty `sandbox_id` (and no `command_operation_id`) is
supervisor-global.

**Control grants.** `grant_id` must name a control grant installed by root
with `lamp-supervisor grant-install` (format `lamp.control-grant/v1`, a
root-owned 0600 file in the store's 0700 `grants/` directory, read on every
request; deleting it with `grant-revoke` revokes it at once). Before dispatch
the worker checks it against the request: unknown, malformed or expired →
`UNAUTHENTICATED`; issued to another peer UID, or a sandbox outside its
`sandbox_prefix`, another `generation` or `policy_revision`, or a command it
does not list → `SCOPE_DENIED`. After that, a generation or policy revision
that differs from the one fixed at `Allocate` → `REVISION_CONFLICT`. The guest
also refuses an exec whose sandbox/generation differ from its last identity
barrier. This engine accepts no handle grants
yet: any `handles`/`input_handles`/`output_handles` or SCM_RIGHTS descriptor
→ `INVALID_ARGUMENT`, and every received descriptor is closed. The grant
verification and single-use consumption mechanics live in
`lamp_worker_protocol::grant` and `::unix`, ready for when handles are used.

**Operation IDs.** The worker remembers the last 4,096 mutating operations.
The same operation ID with the same command returns the prior result (a retry
with a new request ID does not re-run it); while it is still running it
returns a Receipt with `state = "in_progress"`, `outcome_unknown = true`. The
same operation ID with another command → `IDEMPOTENCY_CONFLICT`. Results are
in memory only: after a supervisor restart, earlier operations are unknown and
all volatile sandboxes are gone.

| Command | Linux engine meaning | Receipt `state` |
| --- | --- | --- |
| `Allocate` | Reserve the sandbox ID with generation, policy revision and resources (zero fields default to the standard 2 vCPU / 4096 MiB / 20 GiB). `engine_id` must be empty, `firecracker` or `firecracker-1.17.0`. | `allocated` |
| `Restore{capture_id}` | **Create** when `capture_id` is a template ID (`tp_…`) or name; **fork child** when it is a capture ID from `Capture`. Boots the microVM, applies a fresh identity barrier, and only then returns. Resources must equal the template's (else `ENGINE_UNQUALIFIED`). A `compatibility` whose engine digest or CPU baseline differs → `ENGINE_UNQUALIFIED`. | `restored` |
| `Start{argv,cwd,env}` | Start one command in the guest and return as soon as the guest helper has witnessed its exec (after the identity barrier). `CommandResult` then carries only `started_at_unix_ns` and the guest PID in `detail.guest_pid`; `finished_at_unix_ns` is 0. The request deadline bounds the acknowledgement, never the command, which runs until it exits. Completion is observed with `Inspect{command_operation_id}`. At most 1,024 commands run at once per sandbox (more → `NO_CAPACITY`); running commands are always inspectable, and the newest 256 completed ones stay inspectable. `env` is a map, so its order on the wire is irrelevant: requests are compared and fingerprinted by content. | `command_started` |
| `Capture{capture_id, preserves_memory: true}` | Memory fork of a running source: freeze every guest process (whatever cgroup it is in), pause, write **all** configured RAM (a Firecracker Full snapshot into root-only RAM-backed tmpfs) and reflink the disk, resume, thaw; the receipt means the source is running again. No dirty tracking decides what the child receives. A capture is restored at most once (by one child), and its image stays root-only: the child's VMM gets its own read-only link. `capture_id` is opaque but must match `(cp_\|capture_)[a-z0-9_]+` (≤ 60), keeping it apart from template IDs and names. RAM and disk are always captured at one paused instant (crash-consistent). `consistency` `""`, `crash` or `volatile` keeps the image in host RAM; `local` also fsyncs image, state and disk to the store before the receipt (until a separate durability field exists). `application` and `preserves_memory: false` → `INVALID_ARGUMENT`. | `captured` |
| `Stop` | Kill the VM (no guest shutdown); keep the record. | `stopped` |
| `Release` | Destroy the VM, its jail, disk and memory references. A fork keeps running after its source is released. | `released` |
| `Inspect{command_operation_id}` | Sandbox state (`allocated`, `running`, `stopped`, `failed`) and identity in `detail`; global form lists templates and the engine's compatibility. With `command_operation_id` (a `Start` operation ID on this sandbox), `detail.command_state` is `running`, `exited` (with the full `CommandResult`: exit code/signal, the last 64 KiB of stdout and stderr, `truncated`, guest start/finish times) or `unknown` (completion can no longer be observed, for example after `Stop` or a VMM crash; `detail.command_lost` says why). A command spanning a capture completes normally. | `serving` or the sandbox state |
| `Cancel` | Not supported → `INVALID_ARGUMENT`. | — |

The engine may put stage timings and identity facts (`machine_id`,
`source_pause_us`, …) in `Receipt.detail`; callers must not depend on its keys.
Other public codes it returns: `NOT_FOUND` (unknown sandbox/template/capture/
command), `RESOURCE_IN_USE`, `NO_CAPACITY` (UID range exhausted),
`HOST_UNAVAILABLE` (local OS failure before the request reached the guest,
retryable), `OUTCOME_UNKNOWN` (the guest received the request but its reply was
lost; never retried), `DATA_UNAVAILABLE` (template or seed fails its
digest/signature checks), `HOST_NOT_TRUSTED` (producer key not enrolled).
