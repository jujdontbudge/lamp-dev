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
