# CI core and the GitHub family

## Goal
Let a CI system hand each job a fresh Lamp sandbox. When this is done: GitHub Actions jobs run on one-job Lamp runners with native results, logs, artifacts and cancellation, for ordinary workflows, the Copilot cloud agent, and the Claude Code and Codex actions (CI-002); a GitHub workflow identity exchanges for a short-lived scoped Lamp key with no stored Lamp secret (CI-003); every job gets a fresh sandbox, deleted when the job ends unless a trusted-job policy keeps it stopped for inspection (CI-004, LIF-004); and a stranger's public pull request runs in a fresh real VM with an allow list and only its CI system's own one-job token (CI-005, B2). The job lifecycle is ready for the other CI systems to plug into.

CI is one of the three things Lamp is for, and journey 4 is this ticket: Alice connects a CI system, each job gets a fresh computer, results go back, and outputs stay available after cleanup.

## Context
- Ticket T26 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T11's plugin host, T12's allow lists, T18's keyless connection and T19's saved outputs. Three tickets run at the same time: T25 (harness catalogue), T27 (dashboard) and T28 (install and update). This ticket owns the CI job lifecycle and the `github_actions` plugin. T29 adds the other nine CI systems, triggers and schedules.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- `gh` on the M5 is signed in as the owner's GitHub account, `JohanSardar`. The Lamp repository is `JohanSardar/lamp`, private.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
