# 0.1.2 final public release — 2026-11-13

The coordinator merged !19 as `e7babe9d658ff21f033e5ae23431f041fb7b086d`
and explicitly authorized publication. All final platform engines, injected
guest init/helper/guard binaries and Go binaries were built from that clean
commit. The redacted public source tag is
`d83031de3780dae053a0fdfac900cf8d11a8cde0`. No SDK implementation changed or SDK
package shipped. Earlier attempts and failures remain in `../publication-attempt/`.

## Live URLs

- Source: https://gitlab.com/johansardar/lamp
- Release: https://gitlab.com/johansardar/lamp/-/releases/v0.1.2
- Installer: https://lamp.eilmer.si/install.sh
- Latest: https://lamp.eilmer.si/releases/latest.json
- Assets/checksums: https://lamp.eilmer.si/releases/v0.1.2/SHA256SUMS
- Account notice: https://account.eilmer.si/ — invite-only; hosted sign-ups unavailable.

## Publication proof

[Source safety](source-safety.json) records disabled public CI, disabled shared
runners, no attached runners and wildcard no-push/no-merge branch protection.
These were checked before the source push. Only the ticket branch was briefly
allowed to push; protection was restored and that branch became public default.
The privacy export redacted 47 files and passed the private-pattern and
credential-shape/known-fixture scans. No GitHub access was used.

[Asset verification](assets.json) lists all ten files, sizes and hashes. The
three platform archives' Ed25519 index/manifest signatures, manifest digests,
every payload hash and documented binaries/engine/seed/docs were verified.
Relay archives contain their binary, AGPL license and source pointer. Apple's
read-only submission list independently confirmed the current notarization was
accepted; the builder also refused any non-Accepted submission. `release.json`
retains only the last 20 notary log lines, so the acceptance evidence is here.

[Upload ledger](upload-ledger.jsonl): exactly ten unique starting entries and
ten successful responses, one PUT per file, no retries/replacements. Each next
write started at least 20 seconds later. Some requests took several minutes;
none was interrupted. The ledger includes the checksum file itself.

[Deployment](deployment.log) updated the Worker, replaced only the account
address record with proxied Worker DNS and added its route, with 20-second
pauses. The script filtered address/CNAME records and preserved the existing
CAA records. [Public verification](public-verification.json) downloaded every
file without credentials, checked all hashes, latest=0.1.2 and the exact pinned
installer, and observed account HTTPS plus public A/AAAA answers from 1.1.1.1 and
8.8.8.8. A Python urllib verifier first received HTTP 403; that original
[failed tool request](public-check-python-403.log) is preserved. Curl then read
the same public URLs successfully. This was not an asset replacement or a
relabeling of the failure.

## Fresh public installation and one-time observations

[Observer source](observe-public.py) drives only public CLI operations and the
public installer URL. Its workload is the unchanged `benchmarks/workload.py`,
SHA-256 `7ef80949f5b68a839135d6ffb1ec5f246d3a67662b027280bb072dbff26a5cff`.
There are no new automated tests or private benchmark RPCs. Original observer
results were captured in private scratch; the evidence JSON normalizes
worker identifiers for privacy, including embedded MCP JSON text. Numeric
observations, failures, versions and capability inventories are unchanged.

| Host | Public install / Python result | Install through code | Create | Memory fork |
| --- | --- | ---: | ---: | ---: |
| M5, 24 GiB, macOS 26.5.2 | `aarch64`, `42` | 69.876 s | 553.6 ms | 750.9 ms |
| M2 Air, 8 GiB, macOS 26.7.1 | `aarch64`, `42` | 69.638 s | 733.9 ms | 1199.6 ms |
| lamp-la, existing nested KVM, 8 GiB | `aarch64`, `42` | 62.276 s | 4723.3 ms | 3862.8 ms |
| Ubuntu amd64 | skipped: 3622 MiB available | unobserved | unobserved | unobserved |

Each host's directory contains its [M5](M5/result.json), [Air](Air/result.json)
or [lamp-la](lamp-la/result.json) results and install/uninstall logs. All three
reported client, daemon, supervisor, installer and MCP 0.1.2, OpenAPI release
0.1.2 / contract 1.0.0, and 291 connected MCP tools. Read-only CLI discovery and
served OpenAPI were captured too. Installed default templates ran the README
Python command. Explicit create/exec, live memory fork with parent/child RAM
nonce/ticks, guarded owner-service networking and sandbox deletion were used.
The control owner token was absent from one guest process environment on each;
no raw token or environment dump was retained.

[Live measurements](../../../../release/measurements.md) gives exact boundaries,
resources, native/guest ratios and all comparability limits. These are single
observations, not p50s. Air and lamp-la missed the one-second live-fork goal.
Air native compile exited 1 before a guest pair; compiler stderr was not retained
and the cause is unobserved. It was not rerun. The other workload observations
remain recorded. Disk semantics/toolchains differ; TCP is a loopback versus
guarded service diagnostic, not independent remote network parity. Complete
zero-leak and native-speed parity remain unestablished. Ubuntu was skipped even
for install/version under the latest owner instruction; Chrome was untouched.

## Cleanup and outstanding scope

All three disposable prefixes were uninstalled with data deletion and removed.
Only owned sandboxes, network connections and the disposable echo fixtures were
created or stopped. lamp-la ended with zero Firecracker processes; `fstrim`
returned 2.7 GiB of freed extents. No existing Lamp installation, Docker resource,
owner project, visible app or everyday service was changed. No paid resource
was used. Completed engine/seed scratch and all six release archives were
removed after verification; the lowest observed M5 free space in this final
publication run was 39 GiB, and it had 47 GiB after live-run cleanup.

Missing observations are Ubuntu installation/version and its release workloads,
Air native compile pair, independent remote network parity and a complete
zero-leak measurement. Enrollment-only feature and Linux arm64 kit limitations
remain as written in the release notes. Immutable archive docs are the
pre-publication snapshot; final observations are published in the live source
and release description without changing any asset.

## Final review

Sol approved the publication record at `7a236da` in one round with no findings,
fixes or new tests. See the [review ledger](sol-review.md). Only this review
record changed afterward; the public release and its observations are unchanged.
