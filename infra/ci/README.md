# Owner-machine CI

This repository's persistent runners are **private-only, owner-only**. No hosted
runner fallback exists. A push to main or t3/** starts three jobs; PR events do
not start jobs. Push checks attach to the PR's head SHA. Manual runs also work.

| Runner / labels | Execution host | Persistence |
| --- | --- | --- |
| lamp-lx / self-hosted, Linux, X64, lamp-lx | Ubuntu laptop | User systemd service, enabled; existing user lingering starts it after reboot |
| lamp-m5 / self-hosted, macOS, ARM64, lamp-m5 | M5 Mac | User LaunchAgent, RunAtLoad; starts at owner login after reboot |
| lamp-la / self-hosted, Linux, ARM64, lamp-la | Existing lamp-la Lima VM on M5 | Guest user systemd service + lingering; M5 LaunchAgent starts the existing VM at owner login |

The Mac services use only the owner's user launchd domain, never a system item,
visible app, overridden HOME, password or approval dialog. FileVault/login must
complete after a Mac reboot. LA stays running while its runner is available;
it reserves the existing lane's 4 vCPU/8 GiB. Stopping that VM makes LA's check
queue until restarted. There is no paid overflow. AIR is not needed for these
three build targets.

## Pause LX for hard timing

Run from M5 (or run runnerctl locally on LX):

```sh
ssh ubuntu-server '~/.local/share/lamp-ci/runnerctl pause'
# Run the separately authorized hard timing experiment, then:
ssh ubuntu-server '~/.local/share/lamp-ci/runnerctl resume'
ssh ubuntu-server '~/.local/share/lamp-ci/runnerctl status'
```

Pause writes a persistent PAUSED marker and stops only the CI runner service.
It can cancel a running CI job; wait for that job to finish first when practical.
The marker survives reboot and both the service condition and host guard refuse
new work until resume. Do not run timing until the runner and its job processes
have exited. CI does not stop Chrome, Beeper, T3 Code or any other owner service,
and never accesses the laptop's existing Docker containers/images/networks.

Linux service inspection:

```sh
systemctl --user status lamp-ci-runner.service
journalctl --user -u lamp-ci-runner.service
loginctl show-user "$(id -un)" -p Linger
```

On M5, use ~/.local/share/lamp-ci/runnerctl status/pause/resume and inspect
~/Library/LaunchAgents/com.lamp.ci.runner.plist. The LA startup item is
com.lamp.ci.lima.plist; it starts only the already-defined lamp-la VM.

## Admission and the public-release boundary

The workflow checks repository identity, private visibility and the owner's
immutable account ID, and has no pull_request, pull_request_target,
workflow_run or issue_comment trigger. All actions are GitHub-owned and must use
full commit SHAs (repository Actions settings). Checkout does not persist its
job credential. Jobs have read-only contents permission and id-token:write only
to authenticate themselves to the host guard; no cloud role trusts that token.

Workflow conditions and custom labels alone are not the security boundary:
an outsider can propose another workflow. Each runner has a process-level
ACTIONS_RUNNER_HOOK_JOB_STARTED pointing **outside the checkout** to
~/.local/share/lamp-ci/guard.sh. The runner executes this before any job steps,
including container setup. Job YAML cannot replace the runner process's hook
path. It runs guard.py in an isolated, pinned Python environment, obtains the
job's GitHub OIDC token and verifies RS256 against GitHub's JWKS, issuer,
audience, expiry and not-before time. It admits only all of:

- Repository ID 1450818687 and owner ID 68117503, with private visibility.
- Actor ID 68117503 (JohanSardar), including owner-initiated reruns.
- Push/manual events, refs/heads/main or refs/heads/t3/**, and the signed
  .github/workflows/check.yml workflow reference.
- Matching signed and runner-provided repository, run ID, SHA, ref and event.
- No PAUSED marker and no currently public repository at GitHub's public API.

A private repository returns 404 from the unauthenticated API; that is accepted
only alongside the valid signed private claim. Other HTTP failures, missing
OIDC permission, unknown signing keys, invalid signatures, timeouts and malformed
responses deny. No owner PAT is copied to a runner. The short-lived registration
token is supplied on stdin and is not retained. Runner credentials remain in the
runner's mode-0700 host directory. Guard tests cover outside actors/forks,
PR/target/downstream events, public visibility, mismatched identity and reruns.

The owner and owner-authored code remain trusted and can administer their own
machines. A compromised owner GitHub account or a malicious trusted dependency
is outside the outsider-PR guarantee. The pre-job hook prevents untrusted job
steps by sending SIGKILL to **only its own Runner.Worker ancestor** on denial.
Exiting the hook with failure is insufficient: runner 2.338.2 can still run
`if: always()` steps. Parent discovery failure holds the hook for operator
intervention instead of returning a failed hook. A rejected job appears failed
in GitHub because its worker stopped unexpectedly; the Listener survives.

**T31 must replace this execution model before enabling public CI.** Public jobs
are disabled by both the workflow and signed-identity guard, including owner
pushes. Persistent runners must be removed/disabled before changing visibility;
public-repository jobs must execute in fresh disposable VMs with no owner-host
credentials or writable host mounts. Never make public checks work by weakening
this guard, adding pull_request_target, or approving an outside job on these
runners. T1 does not claim a fresh macOS-VM public CI system is already built.

## Installation, upgrade and removal

Run infra/ci/install.py --lane lx|m5|la on the named host, piping a fresh
repository registration token to stdin. It needs Python 3.12+ with venv, free
network downloads, a working Go/Rust/C toolchain and git/make. Linux users need
lingering already enabled; the installer does not elevate privileges. Package
version and SHA-256 are pinned in runner-packages.json from the official release.
It installs under ~/.local/share/lamp-ci, not the repository, and enables the
user service. LX receives user-local protoc 35.0 for regeneration. No apt,
Docker, keychain, system LaunchDaemon or host password is used.

The runner is pinned with --disableupdate. The owner must update it within
GitHub's supported update window; stale packages stop receiving jobs. For an
upgrade, pause, verify the new official checksums, replace the package without
replacing .runner/.credentials/.env/.path, then resume. Guard upgrades require
review and installation outside checkout; changing repository guard.py does not
silently change live host admission. Compare its SHA-256 on all three hosts.

To retire: pause; remove the runner registration in repository Settings →
Actions → Runners; disable the Linux user unit or bootout only these named Mac
user LaunchAgents; then remove only ~/.local/share/lamp-ci and the corresponding
unit/plist. Remove com.lamp.ci.lima.plist if automatic LA startup is no longer
wanted. Preserve the VM definition and owner data.

Official runner references: [job hooks](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/run-scripts),
[runner source](https://github.com/actions/runner), and
[OIDC claims](https://docs.github.com/en/actions/reference/security/oidc).
