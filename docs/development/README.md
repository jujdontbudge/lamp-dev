# Building and dividing the work

Requirements: Go 1.26.2+, Rust 1.99.1 (rustup reads rust-toolchain.toml), a system
linker/C toolchain, and Python 3.10+ with venv for contract checks. Linux x86_64,
Linux arm64 and Apple Silicon macOS are supported build targets. Kernel/OS
minimums for the eventual engine remain those in architecture §5; this skeleton
does not require virtualization, privileges, an account or a network service.

```sh
make build                 # Go + Rust, checked-in generated bindings, locked Cargo
make test                  # native language tests
make bootstrap             # local ignored Python venv, pinned schema validator
make verify                # build/test + spec parity + negative schemas + interop + lint
make check-generated       # additionally requires protoc 35.0
```

Dependency downloads use the normal Go/Cargo/Python caches. There is no paid
service. Run in your own checkout: target/, .venv/, .tools/ and generated build
outputs are checkout-local. No command starts a daemon, modifies system startup,
uses Docker, or stops the owner's apps/services. Contract tests allocate only
short-lived unprivileged processes and Unix sockets. The examples/probes are test
fixtures; they are not production workers.

## Shared integration surfaces

| Path | Owns | Initial ticket |
| --- | --- | --- |
| `contracts/public/schema.json` | Canonical resource/field/input/result/error/event vocabulary, operation/scope/transport dictionary and public limits | T1, coordinated changes thereafter |
| `contracts/worker/worker.proto` | lamp.worker/1 messages and typed handle grants | T1; T2/T3 coordinate changes |
| `pkg/api`, `crates/lamp-contracts` | Generated public types and operation metadata | Generate only |
| `pkg/workerpb`, `crates/lamp-worker-protocol` | Generated worker messages plus bounded framing helpers | T1; shared T2/T3 boundary |
| `crates/lamp-engine`, `crates/lamp-supervisor`, `crates/lamp-init` | Rust engine, privileged service, guest-init seams | T2 |
| `cmd/lamp`, `cmd/lampd`, `internal/control` | Go CLI, service, operation/admission state | T3, then T10 |
| `cmd/lamp-broker`, `internal/broker` | Standalone owner credential proxy, scoped adapters and selected-path settings return | T17 |
| `internal/guard`, `crates/lamp-guard` | Guard integration boundary; Linux packet/process collectors and host spool. [T4 integration contract](T4-guard.md) | T4 |
| `tests/` (outside `tests/contracts`), `benchmarks/` | Independent oracles and benchmark harness; create subdirectories as needed | T5 |
| `tools/`, `tests/contracts`, workspace manifests, CI | Shared build/codegen/conformance infrastructure | T1; coordinate modifications |

Each ticket owns its implementation directories and tests. Avoid adding engine,
control or test-harness implementations to shared contracts. New Rust crates get
picked up by `crates/*`; new Go packages share the root module. Add dependencies
in the owning crate/package and update lockfiles with the same change. Manifest
or contract edits are deliberate integration points: merge/rebase them in order,
regenerate from canonical inputs, and rerun make verify. Never hand-edit generated
types to solve a local mismatch. No CODEOWNERS user identities are invented here.

## Contract workflow

The JSON Schema is Draft 2020-12. `x-lamp-resources` fixes kinds/nouns/stems/
collections/actions; `x-lamp-operations` fixes every ID, scope, input/result/
completion schema, REST method/path, MCP name and CLI spelling. The generator
produces OpenAPI 3.1 and Go/Rust types from that one source. Public resource
objects and operation inputs are closed. Adapter-owned documents are explicitly
marked extension points and require validation against the exact immutable
adapter schema before effect. JSON Schema does not grant authority: caller scope,
read-only policy, quotas, custody, revisions and state-dependent rules still need
admission in T3/T10. Generated Go structs are wire representations and do not
validate enum/required/oneOf constraints on their own. Rust serde rejects unknown
fields/enums but also needs JSON Schema and semantic admission validation.
Optional Go fields use pointers, including slices, to retain absent vs supplied
empty authorization selectors. UInt64 values use native uint64/u64; consumers
must avoid decoding them through floating point.

Single reads, mutation envelopes and transient secret-once completions have
separate types. A synchronous completion belongs in envelope result; a durable,
nonsecret completion belongs in Operation.outcome.result. Never persist a secret
or share URL in that outcome, an event, an audit record or an input hash.

For a contract change:

1. Review the agreed architecture §§11–12 and edit the schema/proto source.
2. Extend `tools/check_contracts.py` and negative fixtures for new shapes. The
   independent checker derives standard actions and names from the spec, checks
   nested shapes, scope/CLI/REST mappings, error statuses and canonical events.
3. If the spec changed, review the whole change before updating
   `x-lamp-spec-sha256` (SHA-256 of the exact UTF-8 text from `## 11. ` up to,
   excluding, `## 13. `). This fails closed for prose grammar not yet parsed;
   changing the fingerprint alone is not a substitute for shape review.
4. Run `make generate check-generated verify` with pinned generation tools.

`openapi.json` is generated; use it for API description, not as a second schema
source. The worker README defines framing and the authentication/authority checks
future receivers must implement. Framing helpers alone are not a security boundary.

Template definitions and engine acceleration/saved-state bytes are opaque
Blob/handle references. Compatibility metadata is public; their storage layout,
ABI and package format stay unfrozen until T14, after T7/T8/T9.

## Current executable behavior

`lamp`, HTTP and the stdio `lamp-mcp` server share the credentialed control core:
operations, fleet modes/roles, scoped keys, one-use approvals and live events.
See [control-core.md](control-core.md) for enrollment, usage and extension seams,
and [T10-evidence.md](T10-evidence.md) for verification and the unmet E03 gate.
[demo-control-path.md](demo-control-path.md) records the original worker path and
its explicit fixture/engine evidence boundaries.
`version --json` still reports build/protocol metadata. The Linux engine
(`lamp-supervisor`, T2) boots templates as Firecracker microVMs and forks them
with memory; see [crates/lamp-engine/README.md](../../crates/lamp-engine/README.md)
and [T2-evidence.md](T2-evidence.md). T6 joins the parallel tickets and runs
the gate on lane LX; the measured result (failed) is in
[T6-evidence.md](T6-evidence.md). T7's durable local captures and their E04
storage proof (ext4 at 1,000 forks, XFS-reflink at 100) are in
[T7-evidence.md](T7-evidence.md).

The standalone [owner broker](T17-broker.md) keeps provider credentials and signing
keys host-side. [T17 evidence](T17-evidence.md) records the real LX sandbox,
canary-free full capture, host CLI work and explicit remaining integration/account
gaps; it does not claim the entire ticket is complete.

## CI on owner machines

All workflow jobs use the repository’s private-only self-hosted LX/M5/LA runners.
See [runner services, signed admission, pause/resume and the public-release gate](../../infra/ci/README.md).
