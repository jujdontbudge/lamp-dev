# Certification run

## Goal
Prove the whole first release, or show exactly what blocks it. When this is done: every one of the 22 fixed `lamp.compatibility/v1` cases and every contract-boundary case has source-keyed evidence from its exact owned lane; the five journeys pass; all 44 attacks pass with zero protected-secret leaks; the benchmark suite, including the five-competitor comparison, has run; reliability and the 24-hour soak are reported against their proposed targets; and each of Lamp's four goals — parallel work, protection, ready tools and sign-in, ownership — has at least one successful journey and one adverse test (PRD-001, PRD-003, QA-004). Anything missing is listed as `blocked` with its reason, never passed by omission.

The owner's standard is that the first release waits for the entire brief, and that a specified workflow is not a tested workflow. T31 goes public on this run's result, without waiting for the owner.

## Context
- Ticket T30 of 31; `docs/tickets/README.md` lists them all and what each waits for. T1–T29 have landed. Nothing else runs during this ticket. It runs T13's suite; a failure goes back to the ticket that owns the code as a fix. T31 goes public only if this result is clean.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- All three machines reach each other only over the owner's Tailscale network.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Nested lanes never count as speed evidence.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
