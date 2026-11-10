# `lamp.plugin/1`

Public, language-neutral extension boundary (architecture §13, PLG-001/002).
This is **not** `lamp.worker.v1`, and does not expose private supervisor RPCs.
The implementation is `pkg/plugin`; `schema.json` describes the manifest and
payload vocabulary. No plugin code is dynamically loaded into `lampd`.

## Transport and negotiation

The implemented transport is inherited anonymous pipes: stdin receives requests,
stdout emits responses/requests, stderr is bounded diagnostics. Each frame is a
4-byte **big-endian unsigned** byte length followed by exactly that many UTF-8
JSON bytes. Length must be 1–1,048,576 inclusive. No newline framing, batches,
compressed control payloads, duplicate JSON keys, invalid UTF-8, trailing JSON or
nesting deeper than 64. The host closes the circuit on malformed frames.

Messages are JSON-RPC 2.0 objects with `jsonrpc:"2.0"`; request/response IDs are
nonempty strings of at most 128 bytes. A request has `id,method,params`; a reply
has `id` and exactly one of `result` or `error:{code,message}`. Additional envelope
fields are rejected. Only the host sends the ID-less `cancel` notification.
Use disjoint ID prefixes (`h-` and `p-`). Duplicate plugin request IDs are fatal;
workers are recycled after 4,096 effect request IDs rather than growing history
without bound. Pending calls, effect dispatch and outbound queues are bounded by
`limits.concurrent` (maximum/default 16); overload never grows a queue.

First the host calls `initialize`, passing a `Hello` (see schema): exact protocol,
API major, selected minor, type, exact manifest-byte digest, features and declared
method names. The plugin returns its `Hello`. Identity/type/digests/method sets
must match; missing `typed-grants`, `cancel` or any required feature refuses
startup before grants exist. Current host minor is 1 and previous minor 0 is
supported: choose the highest minor in the manifest's compatible range. A future
minor requiring new behavior cannot pretend to be additive. Unknown majors/API
majors and unknown required features fail admission. No downgrade of authority.

The `run` method in the reference package is an example, not a private method
required of every plugin. An admitted manifest declares up to 64 public methods
and their immutable input/result schemas. `initialize`, `cancel`, `host.effect`
are reserved. Invocation params are `{binding,grant,input}`. The declared input
schema validates `input`; the declared result schema validates the reply.

Default deadline is 10 seconds, shortened by the caller context and binding
expiry, never lengthened by the worker. Cancellation sends best-effort
`{"jsonrpc":"2.0","method":"cancel","params":{"id":"h-2"}}` and terminates
the worker; a plugin cannot ignore cancellation and retain grants. A timed-out
or crashed effect may have **unknown outcome**. Core idempotency/reconciliation
must resolve it; do not retry effects automatically.

## Manifests, provenance and admission

A manifest fixes type, publisher, semantic version, SHA-256 executable digest,
protocol, trust class, permissions, limits, configuration schema, compatibility,
required features and method schemas. All required keys must be explicit; no
implicit permission grant. The exact raw manifest bytes are SHA-256 pinned too.
`Admit` needs authenticated owner approval of that exact digest. Every update
requires a fresh approval (including narrowing updates); old approval cannot
expand permissions, swap schemas, change trust or admit a new executable.
`VerifySignature` optionally verifies an Ed25519 signature of the exact manifest
bytes against a separately trusted publisher key. Provenance never grants trust.

The initial typed permission vocabulary is exactly `<type>.use`, scoped further
by individual grants. No wildcard, arbitrary host path, network socket, key or
RAM permission exists. Canonical types:

| Type | Reference conformance effect / integration seam |
| --- | --- |
| `engine` | Assigned RAM buffer through explicitly owner-warned `ram` grant |
| `network` | Route a packet through an authorized core-owned echo endpoint |
| `credential_source` | Host-held signing operation; no key returned to worker |
| `harness` | Emit an authorized installation/continuation plan |
| `kit` | Emit an authorized installation plan |
| `ci` | Create a scoped fixture job |
| `trigger` | Enqueue a scoped fixture job |
| `console` | Deliver input to an assigned fixture session stream |
| `access` | Deliver data through an assigned fixture transport |
| `notification_sink` | Deliver to an assigned fixture notification sink |

These are reference/service fixtures, not vendor/guest/engine certifications.
Each type is tested across the actual worker process, framing and OS sandbox,
including one effect denied by narrowed current core policy and revoked-token
rejection. T17/T18 and later adapter tickets supply real brokers/transports.

Trust classes are owner-reviewed, never self-asserted by a worker:

* `confined`: native service sandbox, typed tokens only.
* `credential`: only credential sources; no ambient vault access or raw-secret
  return path. T17 supplies host-held fill/sign/proxy authorization.
* `platform`: only audited network/access adapters; core supplies scoped endpoint
  effects and remains the final policy enforcer. A label does not grant OS access.
* `engine_trusted`: only engines; exact manifest/executable approval additionally
  requires the exact exported `EngineWarning` acknowledgement. Only these may
  receive a `ram` grant. Even engines cannot receive neighbor or broker authority.

## Grants and every-effect authorization

`Grant` is a random 192-bit opaque token with kind, assigned resource and binding:
`owner_id,task_id,operation_id,generation,parent_grant,deadline`. Tokens live only
in their worker's bounded (64-entry) host registry. A plugin cannot install a
grant or choose an FD. Ordinary/credential/platform types get zero RAM tokens.

A worker requests an effect with JSON-RPC `host.effect`; params are
`{call_id,grant_id,kind,resource_id,binding,input}`. It must reference an active
host call and its exact binding, a live assigned grant, the correct kind/resource
and unexpired authority. At most 16 effect requests are allowed per invocation.
The mandatory core `Authority.Effect(ctx,effect)` **revalidates every effect**,
including current task, parent policy, boot/epoch/lease validity, schema,
configuration reference, idempotency and any credential-source authorization.
The host's token checks are not a replacement for this authorizer. Plugin input,
plans, errors and results remain untrusted. Honor ctx and fence effects at their
actual resource sink; an in-flight syscall cannot be undone by a token deletion.

Bulk data must be mediated by typed resource tokens/streams. The implemented
pipe transport never passes raw FDs or plaintext credential material; raw FD/Unix
socket transport and WASI are not implemented. A signed manifest alone never
opens either route. The confined native alternative is the available runner.

Crash, hang, malformed RPC, output rate/size flood or stderr quota breach opens
the worker circuit, clears all local grants, cancels active effects, kills only
its process group/service cgroup, and invokes core `Revoke(pluginID)` then
`FailDependent(pluginID,reason)`. Wait for `Done()`/`Close()` before considering
external revocation/cleanup complete. Cleanup errors are returned separately by
`Close`, never hidden. Hooks must not block on the worker invoking them. No
automatic restart or implicit re-grant exists. Unrelated workers remain usable.

## Native execution and qualification

The host rehashes executable bytes at startup, stages a private read-only copy,
clears the environment, gives no `ExtraFiles`, and invokes the installed,
digest-pinned **host** `tools/plugin-launcher.c`. This launcher is essential:
`os/exec` alone does not close arbitrary non-`CLOEXEC` descriptors. macOS uses
`POSIX_SPAWN_CLOEXEC_DEFAULT|POSIX_SPAWN_SETEXEC`, explicitly preserving 0–2;
Linux uses `close_range(3, UINT_MAX, 0)`. Missing facilities fail closed. Core
dumps are disabled and the file-descriptor limit is 64.

Linux requires an available user systemd manager, cgroup v2 delegation and
`/usr/bin/bwrap`. A unique `lamp-plugin-*` service gets default `MemoryMax=64M`,
`MemorySwapMax=0`, `CPUQuota=25%`, `TasksMax=32`, `NoNewPrivileges=yes` and an
absolute one-hour runtime ceiling. Bubblewrap unshares all namespaces, drops
capabilities, mounts only public system libraries and the exact worker read-only,
provides isolated proc/dev/tmp, and clears the environment again. No host home,
store, IPC endpoint, device, network namespace, system binary tree or credential
is mounted. Descendants remain in the same bounded cgroup; cleanup stops only
that uniquely owned unit. Required kernel/runtime failures never fall back to
ordinary exec. Plugins are native executable bundles; an interpreter/runtime
bundle must not be supplied as an ambient host path.

macOS uses canonical-path `sandbox-exec` deny-default profiles: read only the
literal worker, the root directory itself (dyld opens `/`, not its descendants),
public `/usr/lib` and `/System/Library`; deny writes, network, IPC and other
executables/fork. Resource observation terminates on sampled RSS/CPU excess but
is **not a hard memory/CPU scheduler quota**. Production starts therefore fail
closed on macOS. Only explicit `DiagnosticSoftLimits:true` admits diagnostic
runs; they cannot qualify PLG-001's resource ceiling. Windows is unsupported.
No dialog, visible app, Docker or owner-service change is involved.

## Schemas and configurations

The embedded `Schema` vocabulary is deliberately closed: `type`, `properties`,
`required`, `items`, `enum`, `maxLength`, `maxItems`, `minimum`, `maximum`.
Objects always reject extra fields; arrays require an item schema. Empty enums
and null-valued schema keywords are rejected (a literal `null` inside a nonempty
enum remains valid). Zero optional length/item limits mean no additional ceiling. Schema nesting is limited to 16;
no references, remote resolution or executable validators. This is not a full
JSON Schema runtime. `schema.json` uses standard JSON Schema to describe this
restricted vocabulary. Control frames and nonsecret config documents retain
independent 1 MiB and 64 KiB byte ceilings.

`Package.Adapter(pluginID)` publishes immutable schema/digest/compatibility
snapshots, with `certification:"unqualified"` until independently certified.
`Adapter.Configure` validates a nonsecret document against the admitted bytes,
checks its exact schema digest and host-held `cr_*` references, and derives an
immutable config digest without rounding integers. Caller mutation of exported
discovery fields cannot change validation. `ConfigRef` selects ID+digest;
`ValidateRef` rejects mismatches/deletion. The control core must persist unique
IDs, authorize CredentialSources, reject secret-bearing documents, publish
validating/failed states and discovery records, and recheck readiness on use.
These helpers do not implement T10's Blob upload or public resource endpoints.

## Build and reproduce

```sh
make build
# Installed host bootstrap: bin/lamp-plugin-launcher (pin its actual SHA-256).
go build -o bin/lamp-plugin-reference ./cmd/lamp-plugin-reference
python3 tools/package_plugin.py --executable bin/lamp-plugin-reference \
  --type console --output /tmp/new-console-package
# Output is unapproved. Review exact manifest, permissions, trust and digest.
go test ./pkg/plugin -v
go test -race ./pkg/plugin
make bootstrap verify
```

Package output directories must be new. Authenticate/approve in T10 core, call
`Admit`, start with installed launcher path+digest and private staging directory,
install only current core-authorized grants, then call declared methods. Never
turn a plugin JSON object into `Approval` or permissive `Authority` callbacks.
See `docs/development/T11-evidence.md` for observed lanes and remaining gaps.
