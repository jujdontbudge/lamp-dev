# Budgets, audit, revocation and usage data

## Goal
Make every sandbox accountable and stoppable. When this is done: a task and its descendants share time and token budgets, with exact reservations where Lamp's proxy mediates the route, visible best-effort usage plus a hard time stop on opaque routes, and a watchdog that stops everything when the time runs out (OPS-002, A4); every process start, connection and mediated request is in a per-sandbox audit record that can be exported (OPS-004, B4); every active and retained resource is charged to a quota, with bounded queues, so exhaustion never breaks unrelated admitted work (OPS-009); revoking a key, role or grant stops derived authority within 5 seconds online and shows what is still waiting to expire offline (ACC-009); usage data is opt-in, numeric counts and timings only, kept 90 days (OPS-008); and there is no payment system, but the usage and admission ledger leaves room for plans, quotas and billing later (OPS-006). E13 passes.

An owner who lets agents run unattended needs to know what they did, cap what they spend, and stop them at once. Journey 5 ends with Alice revoking access and seeing which machines have stopped and which are still waiting to expire.

## Context
- Ticket T23 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T16's fleet authority, T17's broker and the collectors from T4. Five tickets run at the same time: T19 (lifecycle and storage), T20 (Linux breadth), T21 (sessions and consoles), T22 (kits) and T24 (hosted side). This ticket owns budgets, the audit ledger, quotas and revocation. T27 builds the budget and audit views in the dashboard.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
