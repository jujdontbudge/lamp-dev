# T45: signed maintenance and one release version

Evidence from the M5 on 2026-11-12 (timestamps in transcripts are UTC).
These are locally signed 0.1.1 and 0.1.2 candidates, not published releases.

The real engine run in [m5-maintenance.log](m5-maintenance.log) installed a
prefix-scoped engine at `/tmp/l45`, with `--service process`. It used `sudo -n`
for that disposable engine only; it did not bootstrap a system launch item,
modify a keychain, use Docker or touch an existing Lamp install.

The run created one VM, wrote the public six 1.16.0 module into its disk and
executed it. The source archive came from PyPI on the host and its SHA-256
matched PyPI's metadata. Host delivery avoided the unrelated guest DNS issue
seen in the first attempt. The CLI then performed:

- `lamp-installer update --prefix /tmp/l45 --source http://127.0.0.1:18445`
  without `--release`: downloaded the signed platform archive, suspended the
  ready sandbox, swapped the supervisor and daemon, and resumed that same VM.
- `lamp version --json`: daemon, client and connected supervisor all reported
  0.1.2. Installer status, `lampd --version`, `lamp-installer --version`,
  `lamp-supervisor --version`, MCP initialize and served OpenAPI agreed.
  OpenAPI also reported contract version 1.0.0.
- Repeated update printed `already up to date (0.1.2)`.
- `lamp-installer rollback --prefix /tmp/l45`: suspended/resumed the same VM,
  restored 0.1.1 and left the module readable/executable on its disk.
- Deleted the sandbox, then uninstalled with `--data delete`. The prefix,
  installation-derived engine/store/runtime paths and both installation-derived
  hidden users and groups were absent afterward. Other ticket agents were
  active, so global machine-wide account equality was not asserted.

The original [first attempt](m5-first-attempt.log) remains a failed result:
the guest could not resolve PyPI, so it stopped before update. Its cleanup
removed the disposable installation. Its global account equality assertion
also failed amid concurrent ticket activity; the successful run instead
checked the exact accounts and paths belonging to this installation.

[sol-process-proof.json](sol-process-proof.json) is the independent reviewer's
separate real process-service demonstration. It additionally preserved a
telemetry setting through update, rejected unauthenticated `/v1/version` with
HTTP 401, exercised rollback and removed its prefix.

[module-version.log](module-version.log) records a real
`go install lamp.eilmer.si/cmd/lamp@v0.1.2` from a disposable local module proxy,
without version linker flags. The installed binary reported 0.1.2 from Go's
module build information. This proves the tagged-module mechanism, not a
public tag that has not been released. The binary and candidate module cache
were isolated into disposable task storage afterward.

The release worker change must ship with the next release-service deployment:
`/releases/latest.json` selects the version; the installation's pinned signing
key remains the authority for downloaded index, manifest and payload bytes.
The live public service was not deployed or changed by this ticket.

## Checks and review

`make build` (Go workspace, SDK generation check, locked Rust workspace),
focused `go vet` for changed Go packages, public/SDK generation checks and
`git diff --check` passed. No test files were added. The final post-rebase build
output is in [build.log](build.log).

Sol loop: one round, Codex GPT-6.1 Sol, medium reasoning effort. Reviewed code
head: `8c0ed41c869623d0080bc4bdc4c029c2dd1b8216`. Verdict: **APPROVE**.
No blocker, should or nit findings; no fixes or reviewer tests to apply.
Product diff before/after review: 28 files, 627 insertions, 36 deletions;
review changed no product behavior. Later changes contain evidence only.
Usage was not exposed.

| Finding | Severity | Outcome | Test |
| --- | --- | --- | --- |
| None | — | Approved in round 1 | Real process-service proof above |
