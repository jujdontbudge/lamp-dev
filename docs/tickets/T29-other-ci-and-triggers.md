# Other CI systems, triggers and schedules

## Goal
Finish Lamp's CI coverage and let outside events start agent work. When this is done: each of the other nine CI systems in architecture §16 — GitLab (Fleeting and Custom executor modes separately), Buildkite, Jenkins, Forgejo, Gitea, CircleCI, Azure Pipelines, Bitbucket and Woodpecker — runs real jobs on one-job Lamp runners with a successful result, failure, cancel, controller loss, logs, artifacts and orphan cleanup, as a shipped plugin with a pinned certification manifest, for trusted-private, trusted-public and stranger-public jobs (CI-001; cases `ci-native` and `ci-trust`); and Slack, Linear, webhook and schedule triggers start work with verified delivery, retry and duplicate handling, cancellation and a useful result, with schedules correct through daylight-saving changes, overlap and loss of quorum (CI-006; case `triggers`).

Teams already have a CI system and should not have to change it to use Lamp. Triggers and schedules are how agents start work without a person at the keyboard.

## Context
- Ticket T29 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T26's CI job lifecycle. No other feature ticket starts at this step; T25, T27 and T28 may still be running. This ticket owns the nine CI plugins and the triggers. T30 certifies it.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.

## Constraints
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
