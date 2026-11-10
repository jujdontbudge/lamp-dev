# T11 plugin host evidence — 2026-11-10

PR #14 review r1 follow-up: [three fixes, red/green regressions and current
verification](T11-review-r1.md). Earlier transcripts below are retained history.

## Delivered boundary

`pkg/plugin` implements the public `lamp.plugin/1` pipe transport, exact-byte
manifest/executable admission, owner-reviewed trust classes, typed scoped grants,
immutable Adapter/configuration helpers, and native Linux/macOS sandbox runners.
`contracts/plugin/schema.json` and its README specify the language-neutral wire
vocabulary. No private engine/supervisor protocol or control-core operation was
added. `cmd/lamp-plugin-reference` exercises all ten declared plugin types.

The installed host bootstrap is `tools/plugin-launcher.c` (built by `make build`);
the host pins its digest. `tools/package_plugin.py` makes digest-pinned,
**unapproved** reference packages and refuses existing output directories.

## Real process and confinement observations

The tests build actual executable workers and a native launcher, then communicate
through inherited anonymous pipes under real OS confinement. The process/profile
boundary is not mocked. Only external type-specific services/core authority are
fixtures; these are not vendor, guest, engine or release certifications.

| Lane | Observed execution | Retained transcript |
| --- | --- | --- |
| LX, Linux x86_64 | systemd user service, cgroup memory/CPU limits, bubblewrap namespaces; all ten reference types and hostile cases passed | [LX](evidence/T11/lx-plugin-tests.txt) |
| LA, Linux aarch64 | Same native runner; all ten types, hostile cases and admission/configuration checks passed | [LA](evidence/T11/la-plugin-tests.txt) |
| M5, Darwin arm64 | Actual deny-default Seatbelt profile; resource enforcement is diagnostic-only | [race run before resume](evidence/T11/m5-race.txt), [final race run](evidence/T11/m5-race-final.txt) |

[Runtime identities and pinned Python dependency versions](evidence/T11/lanes.txt).
LA lacked bubblewrap: installed the free package in the already-running CI VM,
and added it to its provisioning declaration. No VM was started/stopped, no
Docker resource was touched, and no owner app/service was stopped.

All six hostile ambient-authority observations were false on LX, LA and M5:
`file`, `tcp`, `unix`, `env`, `fd`, `subprocess`. The FD test deliberately clears
CLOEXEC on an unrelated host file before launching the worker. It initially
exposed a real leak through plain `os/exec`; the native bootstrap now closes all
unassigned descriptors (macOS close-by-default spawn, Linux close_range).

Crash, hang, oversized control frame, malformed JSON, unsolicited flood,
stderr flood and memory over-allocation each opened the worker circuit,
revoked its grants and invoked dependent cleanup. An unrelated worker still
completed a useful effect after these failures. Forged resource/RAM requests
were denied; replaying a plugin request ID was rejected before a second effect.
Sixteen concurrent RPCs completed without confusing calls and effect replies.

## Personas and effects exercised through the library API

There is no new user-facing console/UI in this ticket. Library API/process seams
are the live interface; screenshots would not prove this boundary.

- **New plugin author:** admit each of engine, network, credential_source,
  harness, kit, CI, trigger, console, access and notification_sink, negotiate,
  and exercise one authorized effect. Fixtures observe assigned-buffer writes,
  a real local TCP echo, host-held signing, persisted install plans/scoped jobs,
  stream delivery and notification delivery respectively. Narrow current core
  policy and deny the next effect for every type; revoke and reject token reuse.
- **Established owner:** reject substituted executable/manifest bytes, missing
  permission review, protocol mismatch, ordinary RAM permission, changed schema
  digests and deleted configuration references. Engines require the exact owner
  warning. Publisher signatures verify provenance but cannot replace approval.
  Large configuration integers retain distinct digests; a zero-value unvalidated
  Configuration cannot pass reference validation.
- **Mid-task user/hostile author:** inject worker faults, forge/replay effects,
  probe the actual profile, and continue unrelated work. The final schema
  regression rejects empty enums, nested empty enums and null values for every
  schema keyword, while preserving a legitimate `null` value inside an enum.
  Independent JSON Schema tests reject another type's `.use` permission for all
  ten types and validate each generated reference package.

## Verification

| Command | Result | Evidence |
| --- | --- | --- |
| `TMPDIR=/tmp CARGO_BUILD_JOBS=2 make verify` after merging `origin/main` | PASS, exit 0; control suite completed in 448.476 s, all workspace gates reached | [Final full transcript](evidence/T11/m5-verify-after-main.txt) |
| Same command before the main merge | FAIL, exit 2: existing control daemon bind failure and ten-minute package timeout; plugin package passed | [Retained failed transcript](evidence/T11/m5-verify.txt) |
| `TMPDIR=/tmp GOMAXPROCS=2 go test -race -p=2 ./pkg/plugin -count=1 -v` | PASS, exit 0; no race reports | [Final race transcript](evidence/T11/m5-race-final.txt) |
| `go test ./pkg/plugin -run 'TestEmbeddedSchema\|TestNullEnum' -v` | PASS, exit 0 | Also included in the final race transcript |
| `.venv/bin/python -m unittest discover -s tests/contracts -p test_plugin.py -v` | PASS, exit 0; both independent contract tests | Full contract suite also passed in the separate remaining-gates transcript below |

Before the main merge, the full run stopped in `go test ./...`:
`TestDurabilityRejectionHasNoEffect` reported that lampd did not bind its socket,
then the package reached its overall ten-minute timeout during
`TestRustWorkerControlPath`. The plugin package passed in that same invocation.
Those failures remain failures in the retained transcripts.

Then PR #11 merged. This branch merged `origin/main` at
`0dec89b`, including the coordinator's test-temp cleanup and
main-only push CI policy. A fresh complete `make verify` passed after that merge;
no control startup deadline was increased and no test was excluded. T11 itself adds no
control-core implementation changes. Review CI is left to the coordinator.

Before merging, all remaining gates also passed **separately**, exit 0; this did
not relabel the earlier full verification failure.
[Remaining-gates transcript](evidence/T11/m5-remaining-checks.txt):

```sh
cargo test --workspace --locked
python3 -m unittest discover -s tests/benchmarks -v
python3 -m unittest discover -s tests/infra -v
make check-contracts interop
python3 tools/generate.py --check
go vet ./...
cargo fmt --all -- --check
cargo clippy --workspace --all-targets --locked -- -D warnings
```

Rust compilation used `CARGO_BUILD_JOBS=2`. Independent contract tests ran with
the pinned `.venv` dependencies. These gates, the final race suite and the LX/LA
follow-up tests completed before handback; no CI run was awaited.

Earlier failures are retained, not presented as passes:

- [Initial M5 plugin run](evidence/T11/m5-plugin-tests.txt): hostile fixture's
  one-second startup deadline was too short on the shared host. The fixture now
  keeps the normal startup deadline and applies a short caller timeout only to
  its deliberate hang case.
- [Earlier full verification](evidence/T11/m5-verify-before-resume.txt): the
  existing control suite reached Go's ten-minute package timeout. Its active
  `TestForkIfMatchHeader` subsequently passed alone with `TMPDIR=/tmp` (exit 0).
  No control-core code was changed to make verification pass.

Reproduction:

```sh
make bootstrap
TMPDIR=/tmp CARGO_BUILD_JOBS=2 make verify
TMPDIR=/tmp GOMAXPROCS=2 go test -race -p=2 ./pkg/plugin -count=1 -v
.venv/bin/python -m unittest discover -s tests/contracts -p test_plugin.py -v
```

The recorded LX/LA runs copied only ticket sources into fresh `/tmp/lamp-t11-*`
directories, compiled on the measured lane, and ran `go test -p=2 ./pkg/plugin -v`
with `GOMAXPROCS=2`. Their directories/transcripts were retained. The final
schema-only follow-up additionally passed focused host tests on both
[LX](evidence/T11/lx-schema-final.txt) and [LA](evidence/T11/la-schema-final.txt),
and the independent contract tests. Older full-lane transcripts must not be
mistaken for that later schema-only run.

## Remaining scope and qualification gaps

This is **not full PLG-001/PLG-002 certification**:

- Linux uses hard cgroup quotas. macOS has only sampled RSS/CPU termination,
  not a qualified hard memory/CPU scheduler ceiling. Production native starts on
  macOS therefore fail closed. Tests opt into `DiagnosticSoftLimits:true`; a
  positive profile test cannot qualify those resource ceilings.
- The confined native executable-bundle alternative is implemented. WASI,
  peer-authenticated Unix-socket transport and raw bulk-FD transport are not.
  Pipe effects use opaque typed resource tokens; no ambient interpreter path,
  raw credential or ordinary RAM FD is passed.
- T10 must wire authenticated owner approvals, package Blob/resource lifecycle,
  immutable publication/readiness and current-policy authorization to this API.
  T17/T18 must provide real broker/access effects and final sink fencing. These
  tests do not substitute preloaded configurations or claim the public
  upload/discover/configure CLI/API lifecycle already works.

Review was local against the ticket/spec and repository conventions; no child
agents were started. The PR is for coordinator review with these limits visible,
not a claim that reference fixtures certify the entire product.
