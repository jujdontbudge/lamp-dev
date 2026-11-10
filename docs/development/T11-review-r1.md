# T11 / PR #14 — Sol review r1

Reviewed base: `65468822738560683943b59321a7f264fdfbda5f`.
All three findings were reproduced with failing regressions before their fixes.
No CI or re-review was dispatched; those remain coordinator-owned.

## P1: prove Linux resource enforcement before admission

Systemd property assignment alone is no longer treated as enforcement. The
pinned native bootstrap has a private startup gate: it cannot exec bubblewrap
or plugin code until the host confirms the actual kernel ceilings. The host
identifies this uniquely owned service's cgroup/MainPID, verifies the cgroup-v2
filesystem, reads `memory.max`, `memory.swap.max`, `pids.max`, `cpu.max`, and
checks MainPID membership via `/proc/<pid>/cgroup`. Missing controllers/files,
unlimited or excessive ceilings, unreadable observations and wrong membership
fail closed without releasing the gate. A 100 ms watchdog plus checks before
grant installation and effect dispatch revoke authority on lost enforcement.

Regression proof:

- Before: the real Linux `TestLinuxRuntimeRefusesWeakenedLimits` issued a grant
  after its owned service's CPUQuota was changed to 100%, then failed to open
  the circuit (exit 1). After: grants are denied and the circuit opens.
- Before: `TestLauncherWaitsForLimitAdmission` failed with `no startup gate: EOF`
  (exit 1). After: closing/refusing admission cannot execute the marker program.
- Missing-memory and missing-CPU controller observations are injected at the
  cgroup-file seam; the real bootstrap/pipe/denied-exec test keeps the gate
  closed in each case. These synthetic observations are parser/fault tests,
  **not** claims of OS qualification without controllers.
- Both LX and LA exercise real systemd services, kernel controller files and
  membership. Only each test's uniquely owned disposable plugin service has its
  quota changed; no owner service/manager/slice configuration is changed.

## P2: case-exact closed records

All structured decoding first checks exact JSON field tags recursively,
including nested records, maps of records and embedded GrantRequest fields.
The ordinary Go decoder cannot use its case-insensitive fallback to override
`permissions` with `Permissions`, or a nested limit/schema/RPC field with an
alias. Opaque application data, schema property labels and timestamp scalars
retain their proper distinct handling.

Before: `TestCaseExactManifestFields` admitted the exact permission override and
three nested aliases (exit 1). After: all are rejected. Separate RPC and embedded
grant regressions pass; a legitimate schema property named `Permissions` remains
valid application data.

## P2: exact numeric integrality and bounds

`Schema.Minimum`/`Maximum` now use `*json.Number` instead of `*float64` in the Go
API; wire fields remain JSON numbers. Values and bounds use a normalized decimal
significand/order comparison with exact integer exponents. Integrality is checked
without rounding. No power of ten is expanded, so `1e1000000000` does not require
a billion-digit allocation. Quoted numeric bounds remain invalid.

Before: the review's `1.00000000000000001` and out-of-range `9007199254740993`
examples were accepted (exit 1). After: both are rejected, as are negative and
decimal-boundary variants. Exactly representable integer decimals/exponents,
large exact bounds, signed zero and huge exponents pass their expected cases.
An independent `math/big.Rat` oracle checks 1,000 deterministic bounded-exponent
value/bound pairs for both integer and number schemas (2,000 comparisons).

## Verification

All final commands completed with exit 0:

| Lane / command | Result | Transcript |
| --- | --- | --- |
| M5: `TMPDIR=/tmp CARGO_BUILD_JOBS=2 make verify` | PASS; all workspace gates reached, plugin tests executed; unchanged control package cached | [Full verification](evidence/T11/r1/m5-verify.txt) |
| M5: `TMPDIR=/tmp GOMAXPROCS=2 go test -race -p=2 ./pkg/plugin -count=1 -v` | PASS, no race reports | [Race](evidence/T11/r1/m5-race.txt) |
| LX: `GOMAXPROCS=2 go test -p=2 ./pkg/plugin -v` | PASS, including real quota weakening/revocation | [Linux x86_64](evidence/T11/r1/lx-tests.txt) |
| LA: `GOMAXPROCS=2 go test -race -p=2 ./pkg/plugin -count=1 -v` | PASS, including real quota weakening/revocation; no race reports | [Linux arm64 race](evidence/T11/r1/la-race.txt) |

Remote runs copied ticket sources into new disposable `/tmp/lamp-t11-r1-*`
directories and compiled the real worker/bootstrap there. Existing run evidence
was not overwritten. No Docker resources, owner apps or owner services were
stopped. The pre-existing Mac hard-quota/WASI/public-integration qualification
limits in [T11 evidence](T11-evidence.md) are unchanged by these fixes.
