# T1 implementation and acceptance evidence

Completed 2026-11-09. The workspace, public schema, operation dictionary and
lamp.worker/1 contract build and test on all three required native targets.
The engine, control service, guard, independent oracles and storage formats are
intentionally left to their assigned tickets.

All three runs started from the same source archive, unpacked into separate new
Git repositories with no build output. Each recorded a clean initial and final
Git status. The disposable verification commit was
`138162b952db9143ba34b26efbdf6c515140047b`; it identifies the source snapshot,
not a commit made on the implementation branch. Source tree digest:
`4271405bb1cf1f69eb2b105cf9755ddcda6f6d7b72eae603045342eed79ca904`.
The [source manifest](evidence/t1/source-manifest.sha256) lists every tested file;
report/log artifacts added afterward are excluded.

| Real lane | OS / architecture | Toolchains | Result and raw evidence |
| --- | --- | --- | --- |
| M5 | macOS 26.5.2 (25F84), arm64 | Go 1.26.2, Rust 1.99.1, Python 3.13.7 | PASS: [clean log](evidence/t1/m5-clean.log) |
| LX | Ubuntu 26.04.1, kernel 7.0.0-42, x86_64 | Go 1.27.3, Rust 1.99.1, Python 3.14.4 | PASS: [clean log](evidence/t1/lx-clean.log) |
| LA | Ubuntu 26.04.1 in Lima VZ, kernel 7.0.0-38, arm64 | Go 1.26.2, Rust 1.99.1, Python 3.14.4 | PASS: [clean log](evidence/t1/la-clean.log) |

Exact acceptance command in every clean checkout:

```sh
CARGO_BUILD_JOBS=2 ./tools/clean-check.sh
# Executes make bootstrap verify and verifies Git remains clean.
```

This builds Go and the locked Rust workspace; runs three Go worker-framing tests,
two Rust worker-framing tests and nine Python contract-test groups; verifies
public generation; runs go vet, rustfmt and clippy with warnings denied. The
Python groups include deliberately corrupted resource/nested-field/operation/
scope/error/event names, invalid enums/tags, unknown fields on every operation,
uint64 bounds, continuation/preauth combinations and secret-once result shapes.

The independent architecture checker reports **56 resources, 328 operations and
scope pairs, 31 error codes/status mappings, and 200 canonical event names**.
There are 994 named schema definitions before inline enum lifting. Standard
REST paths, addressed IDs, CLI spelling and MCP names are checked alongside
resource and nested-field names. A source fingerprint fails closed when the
spec's prose changes outside the checker's parsed grammar. The agreed spec was
not edited.

The process integration test sends a generated Go Request over a real Unix
socket to a Rust process, which decodes the command/context and returns a typed
Protobuf Response read by Go. It preserves request/operation IDs and maximum
uint64 generation/deadline/policy values. Separate Go → Rust → Go public JSON
round trips preserve uint64, Unicode/base64 and omitted versus empty scope
selectors; both processes reject unknown fields. Both framing implementations
exercise an exact 1 MiB encoded control frame, one byte over, invalid/truncated
messages and oversized prefix rejection before body allocation. The Go side also
checks short/zero writes. These are serialization/framing tests, not claims that
peer authentication, grant enforcement or an engine is already implemented.

Additional independent checks:

- [Both bindings regenerate byte-for-byte](evidence/t1/codegen.log), using
  protoc 35.0, protoc-gen-go 1.36.11 and Cargo-locked prost-build 0.14.4.
- [OpenAPI 3.1 validation](evidence/t1/openapi-validation.log), using
  openapi-spec-validator 0.7.2 independently of the authoring generator.
- Source contents matched the tested archive after verification. No generated
  file was hand-edited. All three clean-check commands exited zero.

CI is configured for Linux x86_64/arm64 and macOS arm64 plus pinned Protobuf
regeneration. GitHub Actions was not triggered; the lane logs above are actual
local/SSH/Lima evidence, not a claim about a remote CI run.

No owner's application or service was stopped. The LX Chrome user service was
active before and after the checks. Docker was untouched. No visible screen,
browser, password prompt, keychain or host startup configuration was used. LA
received free development packages and user-local toolchains for this test; its
initial stopped state was restored. Temporary source checkouts were removed.

T2–T5 should start with [the ownership and contract workflow](README.md).
The template and saved-state formats remain unfrozen until T14.
