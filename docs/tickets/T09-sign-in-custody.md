# Subscription sign-in custody proof

## Goal
Prove that a person's subscription login can live in their own sandboxes the way the owner decided, before the formats are frozen. When this is done, E07 passes for `claude_code` and `codex`: on a trusted host, the official unmodified harness signed in with a subscription keeps working after a same-person memory fork and after suspend and resume, using only refresh behaviour the vendor permits; the sandbox holding the login reaches only that vendor's endpoints, while builds, downloads and git run in a separate token-free sandbox; and a template, export or share made from that work is rebuilt from non-secret inputs, starts the harness fresh, and carries zero subscription canaries in memory, disk, cache, journal or export. Covers CRED-003, CRED-004 and E07.

Ready-to-use sign-in is one of Lamp's four equal goals, and the owner decided (A1, ADR 0004) that subscription logins stay on the person's own hosts and sandboxes. ENG-001 requires this capture question answered before the state format is frozen, and T14 freezes the formats on this ticket's evidence.

## Context
- Ticket T9 of 31; `docs/tickets/README.md` lists them all and what each waits for. T6 ran the engine gate and recorded it as failed on speed (`docs/development/T6-evidence.md`): creates met the target, but forks, compiling and network did not, and the replicated storage rows are not built yet. The owner saw those numbers and on 2026-11-10 accepted the result and said to build everything else. Six tickets run at the same time: T7 proves storage and capture on Linux; T8 proves the Mac guest classes; T10 builds the control core; T11 the plugin host; T12 the network, which owns `lamp-guard`; T13 the proof suite. This ticket owns the subscription custody path and the clean-rebuild path. T21 and T25 later build sessions and the full harness catalogue on it.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
