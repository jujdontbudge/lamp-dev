# Harness catalogue

## Goal
Make every harness Lamp names work for real. When this is done, each of the eleven harnesses in architecture §9.2 (`claude_code`, `codex`, `opencode`, `cursor_cli`, `copilot_cli`, `amp`, `goose`, `gemini_cli`, `pi`, `devin`, `droid`) has a signed, exact-version catalogue row and, on its official route, does useful signed-in work in a sandbox, shows its native sub-agents separately, and continues a session in both directions between host and sandbox (CRED-001, CRED-002; cases `harness-api`, `harness-arm`, `harness-native` and `auth-subscription`). E11 passes for every named harness and console row.

People should be able to bring the agent they already use and pay for. The owner's rule is that a row is certified only by its exact official binary doing real work; a harness that cannot do a required direction stays visibly blocked instead of being faked with a transcript replay.

## Context
- Ticket T25 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T21's sessions, sub-agent and teleport machinery, proved there with `claude_code` and `codex`. Three tickets run at the same time: T26 (CI core and GitHub), T27 (dashboard) and T28 (install and update). This ticket owns the catalogue rows and each harness's hooks. T30 certifies it.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- An OpenRouter key is in `~/.pi/agent/auth.json` on the M5. That account has almost no credit, so only its free models work.
- The owner named Copilot, the OpenAI and Anthropic APIs and Factory among the accounts they already pay for.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
