# T31 — public release: evidence

Lamp 0.1.0 is public, 2026-11-12.

- Repository: https://github.com/JohanSardar/lamp, one commit `452d0b3`, a
  redacted copy of reviewed commit `18dcaf6`.
- Release: https://github.com/JohanSardar/lamp/releases/tag/v0.1.0
- Development history, with all ticket PRs, stays in the private `JohanSardar/lamp-dev`.

## What was done, in order

1. **Runners retired.** `lamp-la`, `lamp-lx` and `lamp-m5` were deleted from the
   repository through the API (`total_count` 0). Their services were stopped and
   disabled: the M5 user LaunchAgent was booted out and its plist moved aside;
   the LX and LA systemd user units were disabled with `--now`. No runner process
   remained on any machine. This happened after CI run 43139338684 passed on
   la, lx and m5 at `7e9b0d1`.
2. **Signed and notarized build** of `18dcaf6` with `tools/release/build.py` and
   a new production release key (kept outside the repository).
   - The Linux engine (`lamp-supervisor`, `lamp-guard`) was built on the Ubuntu
     laptop from that exact commit.
   - Every Mac binary was signed with Developer ID Application (team
     AX3C2B78YD) and the hardened runtime, by `rcodesign` from files, with no
     keychain. Apple's notary service returned **Accepted**, and `spctl` reports
     `source=Notarized Developer ID`.
   - Checksums: [SHA256SUMS](evidence/t31/SHA256SUMS); build record:
     [release.json](evidence/t31/release.json).
3. **Public repository.** `tools/release/publish_repo.sh <patterns> 18dcaf6 --go`
   ([log](evidence/t31/publish-repo.log)):
   - It exported and redacted the commit; the check found 0 private details in
     1,400 files.
   - It renamed the private repository to `lamp-dev` and rewrote `origin` of
     the one checkout that pointed at it. No checkout on the Ubuntu laptop or
     the Air pointed at it, which a separate search from `/` with `sudo`
     confirmed.
   - It created the public repository.
   - **Its push failed:** the `gh` OAuth token lacks the `workflow` scope, and
     the commit contains `.github/workflows/check.yml`. Adding that scope needs
     a browser login, so instead the same export, redaction and check were
     repeated by hand from `18dcaf6` (0 private details, 1,400 files) and
     pushed over SSH as commit `452d0b3`.
   - The script's last step was then run by hand: ruleset `27085014`, active,
     no bypass actors, on all branches (`~ALL`), blocking creation, update,
     deletion and force pushes. A probe push of a new branch was rejected
     ("push declined due to repository rule violations").
4. **GitHub release** `v0.1.0` on `452d0b3`, with the notes from
   [docs/release/0.1.0.md](../release/0.1.0.md) and every file in SHA256SUMS.
5. **Installed from the real release.**
   - **M5:** the anonymous download of `install.sh` and the SDK tarball matched
     SHA256SUMS. `curl … | sh` into a fresh prefix reported `healthy: true`.
     The installed `lampd` carries the Developer ID signature. The TypeScript
     SDK installed from its release URL with `npm install <url>` and listed
     backups from that daemon.
   - **Ubuntu laptop** ([log](evidence/t31/ubuntu-curl-install-sandbox.log),
     [script](evidence/t31/ubuntu-curl-install-sandbox.sh)):
     - Capacity before: 0 Firecracker processes and 23 GiB available.
     - `curl … | sh` into a fresh prefix reported `healthy: true`.
     - The release's own `lamp-supervisor` and `lamp-guard`, from that installed
       prefix, imported the public Ubuntu 26.04 seed into a disposable store,
       built template `tp_ac393f3…` and served it. The installed `lampd` was
       attached to that supervisor.
     - `lamp sandbox create` → `ready` (1 live Firecracker). `exec` exited 0 and
       printed `hello from a Lamp 0.1.0 sandbox`, `Linux 6.18.62` and
       `Ubuntu 26.04.1 LTS`.
     - The sandbox was deleted 3 s later. Everything was uninstalled and the
       store removed. Capacity after: 0 Firecracker processes and 22.9 GiB
       available. The owner's services (`t3code`, Chrome, Beeper) stayed active.

## Failures along the way, as they happened

- The publish script's HTTPS push was refused for the missing `workflow`
  scope (above). The rename, the remote rewrite and the empty public
  repository had already happened. The ruleset had not.
- Ubuntu sandbox attempt 1: `template-build` failed with `HOST_UNAVAILABLE …
  store must support reflinks`, because the disposable store was on ext4. It
  moved to the XFS reflink mount `/var/lib/lamp/t31`. The failed attempt's
  memory mount `/run/l31/m` had to be unmounted by hand.
- Attempt 2: the template built and the supervisor served it, but `lampd`
  could not reach the root-only runtime directory (`connect: permission
  denied`), so no sandbox was created. Separately, `sandbox delete` needed
  `--sandbox-id` rather than a positional ID. Attempt 3 is the result above.

## Dropped by the owner

- 2026-11-12: npm and PyPI publishing; hosted eilmer.si sign-ups (no
  internet-reachable owner machine, and Cloudflare edge TLS would break
  RLY-002); the Python SDK.
- Earlier, recorded in each ticket's evidence: the Codex subscription; the
  1Password and Proton Pass kits; the Gemini, Amp and Copilot subscription
  harnesses; the Copilot cloud agent and the fork PR; hosted Buildkite,
  CircleCI, Azure Pipelines and Bitbucket.

The public repository's ruleset blocks every push and merge, admins included.
The owner has to relax it before anyone can merge into the public repository.
