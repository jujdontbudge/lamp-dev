# Lifecycle, lasting storage and saved templates

## Goal
Make sandboxes cheap to keep and easy to come back to. When this is done: an idle sandbox suspends after its grace period, and a message, exec, SSH attach or allowed dev request resumes it (LIF-001), using the owner's exact definition of idle (LIF-002, A5); expiry and per-sandbox overrides work (LIF-003); warm pools hand out never-used, signed-out sandboxes (LIF-005); a caller can fork mid-task, run alternatives, keep the winner and bring the chosen changes back through git without blind overwrite (LIF-006); shared caches (STO-001), named volumes with several attachments (STO-002), CI outputs and logs that outlive their job (STO-003), owner-only persistence by default (STO-004), and durability, encrypted backup and restore (STO-005) all work; installed tools can be saved into a template, rebuilt clean when the source sandbox is tainted (TPL-002); and named recipes resolve atomically to immutable versions (TPL-003).

Journey 3 is trying two approaches and keeping the winner; journey 4 is keeping a CI job's outputs after its sandbox is gone. Owning your machines and your data is one of Lamp's four equal goals, and so is having tools ready without setting them up again.

## Context
- Ticket T19 of 31; `docs/tickets/README.md` lists them all and what each waits for. The formats are frozen (T14); this builds on T7's capture and T10's control core. Five tickets run at the same time: T20 (Linux breadth), T21 (sessions and consoles), T22 (kits), T23 (budgets, audit, revocation) and T24 (hosted side). This ticket owns lifecycle policy, the content store's features, volumes and template saving. T26 (CI) starts from its result: saved outputs, CI deletion and warm pools.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Power-loss and write-reordering faults run only on disposable fixtures, never on a machine's live disk.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
