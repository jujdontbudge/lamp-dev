# Dashboard and notifications

## Goal
Give the owner one live page that shows everything their fleet is doing. When this is done, the dashboard served by any host shows, live, every host, sandbox, session, task and resource the viewer is allowed to see (OPS-001), including setup progress and failures, both countdowns (idle and task budget), sharing, approvals that show the actual requested effect, private browser takeover, budgets and audit, saved state stranded on a sleeping host, and capability evidence labeled specified, implemented, tested or blocked. Done, stuck and needs-decision events reach the configured notification sinks with inspectable retries, acknowledgement and private signed-in action links (OPS-003).

Every requirement in the spec has a "what you'd see" part, and this is where a person sees it. In journey 1 Alice sees when setup is finished, watches the work and answers when it needs her; in journey 5 she sees the real effect of an action before approving it.

## Context
- Ticket T27 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T21's sessions, T22's browser takeover and T23's budgets and audit. Three tickets run at the same time: T25 (harness catalogue), T26 (CI core and GitHub) and T28 (install and update). This ticket owns the dashboard and the notification sinks. T30 certifies it.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.

## Constraints
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
