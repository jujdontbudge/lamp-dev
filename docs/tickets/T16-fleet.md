# Fleet: many hosts, one address, work survives a sleeping laptop

## Goal
Make several hosts behave as one Lamp. When this is done, a fleet of one, two or three hosts has one address and one key namespace (HST-002); work is placed automatically or pinned strictly to a host (HST-003); and when a host sleeps or is cut off, a surviving host starts permitted new work within about 10 seconds from capacity already reserved for it, with no hidden third machine, while callers can see which saved files and sessions are still on the sleeping machine (HST-004). E10 passes on real one-, two- and three-host cases across the Ubuntu laptop, the M5 and the Air: zero duplicated keyed effects, double writers, overspend or reused approvals through abrupt sleep, partition, rejoin, a delayed `lampd` and wall-clock rollback, and old or deleted keys and stale backups stay rejected.

Journey 2: Alice adds her Mac to the same fleet as her Linux laptop and keeps working when one of them sleeps. The owner chose one fleet with many hosts and no required extra infrastructure (ADR 0001).

## Context
- Ticket T16 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T10's control core. Four tickets run at the same time: T14 (format freeze), T15 (Mac devices and fallback), T17 (broker) and T18 (access). This ticket owns fleet authority, permits and placement inside `lampd`. T23 (budgets, audit, revocation) and T28 (rolling updates) build on its result.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- All three machines reach each other only over the owner's Tailscale network.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
