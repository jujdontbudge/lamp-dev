# T47 release preparation — 2026-11-13

**Not published.** The coordinator explicitly requires review and merge before
publication. This MR prepares 0.1.2; it does not claim the ticket's public-release,
DNS or final fresh-install evidence is complete. Public 0.1.1 remains live.
After the coordinator merges, continue with [publication](../../../release/publishing.md),
then record actual live URLs, public fresh installs and the one-time release
measurements. Do not call the T32 candidate timings 0.1.2 measurements.

## What changed

README and 0.1.2 notes describe default installation separately from optional
enrollment. They include Claude subscription custody being unsupported,
Linux-arm64-host Docker/LSP/desktop being unverified, the 165.473-second Ubuntu
candidate install and the owner's deferred SDK work. No SDK implementation was
changed; the release builder no longer packs an old-version SDK as a new release.
Old release measurements remain historical, including misses and partial leak
observations. User actions link to installation, lifecycle/recovery, networking,
access, relay and dashboard pages rather than development evidence. Those user
pages are included in the signed payload. The historical development pages stay
historical and were not relabeled.

The Worker carries 0.1.2, T45's latest route and a static invite-only account
notice. Deployment supports updating the existing Worker and replacing only a
private address on the account name. Default execution is read-only. Public
assets must exist before `--apply`; changes are sequential with 20-second pauses.
The existing one-time public-project creation script is not used.

## Real observations

- `CARGO_TARGET_DIR=/tmp/t47-cargo CARGO_PROFILE_DEV_DEBUG=0 CARGO_INCREMENTAL=0 make build`
  passed: C launcher, all Go packages, SDK generation consistency, locked Rust
  workspace. [Build log](build.log). No broad test suite or benchmarks were run.
- Python AST parsing of the two changed Python tools, shell syntax of installer
  and deployment wrapper, JavaScript module syntax and `git diff --check` passed.
- Thirty-seven local links in the README, new notes and related user guides
  resolved and none targeted development evidence. This is a local source-link
  check; public tag links cannot be followed before publication.
- [Worker handler calls](worker-demo.log) used the actual module and Web Request/
  Response implementation: root 200 with versioned engine-setup link,
  latest.json 200/0.1.2, checksums 302 to the versioned GitLab registry,
  account notice 200, unknown path 404. No live Worker was changed.
- [Signed packaging](package-demo.log) used the real `lamp-installer build`,
  `index` and `verify` with disposable signing material. Thirty-five user-doc,
  support and control-binary paths were signed and verified. The initial
  documentation-only attempt lacked required bin/lampd and was correctly refused
  ([failure](package-first-attempt.log)); adding real control binaries made this
  a control-only candidate, never a complete engine release.
- [Installation](control-install.log) installed that signed candidate into a new
  process-service prefix. [CLI/MCP/OpenAPI observations](control-demo.json)
  show the same release version and identical 291-operation CLI/MCP inventory.
  The engine is explicitly unenrolled, not a claimed VM success. Uninstall with
  data deletion removed the prefix. No owner installation, browser or Docker
  resource was used.

## Infrastructure read-only preflight

Cloudflare's account name still has a private IPv4 address; HTTPS from this Mac
was unreachable. The first safe deployment plan refused multiple DNS records;
inspection showed one address and two CAA records. The updated plan filters
address/CNAME records, preserves CAA, and succeeds with two additional writes
(account address replacement and Worker route) after the Worker update.
No private credentials were printed and no DNS or Worker mutation was made.

Public GitLab project 88830487 is already public, with CI enabled and shared
instance runners enabled. Its attached-runner response listed only instance
runners, not private project runners. Publication must disable public-project
CI/shared runners and recheck private runner attachments before new source goes
public. The private development project's CI remains outside this change.

The M5 had 24 GiB free at start and 23 GiB after the local build, above the
20 GiB floor. Ubuntu read-only preflight returned zero Firecracker processes
but only 4224 MiB available, below the shared 12 GiB allocation requirement.
No Ubuntu VM was created. No release mirrors, seeds or final platform artifacts
were built before merge; these must use the final merged source.

## Review

Sol review pending. No new tests were written by the implementation agent.
