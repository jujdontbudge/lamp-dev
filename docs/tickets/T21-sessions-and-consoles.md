# Sessions and consoles

## Goal
Let people and consoles see and join every piece of agent work. When this is done: a harness can run inside a sandbox (INT-006) or drive one from outside (INT-007); any console sees one fleet machine with each session separate, live and interactive (SES-001, SES-002); people see only their own sessions unless someone shares (SES-006), and watching and typing are separate, explicit grants (SES-007); sub-agents run where the caller chooses, each separately visible, and their work returns through git (SES-004); and a session moves between a laptop and a sandbox in both directions with no lost acknowledged input and never two writers (SES-005). All of it is proved with `claude_code` and `codex`, on their API routes and their approved subscription routes, through the reference console, the public T3 Code and Herdr console contracts, and stock SSH.

Journeys 1, 3 and 5: Alice watches work in her console and answers when it needs her, sees each fork's sub-agents separately, shares a session with Bea for watching or typing, and moves a session onto her laptop and back.

## Context
- Ticket T21 of 31; `docs/tickets/README.md` lists them all and what each waits for. The formats are frozen (T14); this builds on T9's sign-in custody, T10's control core, T11's plugin host and T17's broker. Five tickets run at the same time: T19 (lifecycle and storage), T20 (Linux breadth), T22 (kits), T23 (budgets, audit, revocation) and T24 (hosted side). This ticket owns sessions, the console plugin contract, and the sub-agent and teleport machinery. T25 adds the other nine harnesses and completes E11; T27 builds the dashboard views.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- An OpenRouter key is in `~/.pi/agent/auth.json` on the M5. That account has almost no credit, so only its free models work.

## Constraints
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- T3 Code and Herdr are tested through their public console contracts only, never through a special integration with the owner's own projects.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
