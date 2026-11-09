# Proof suite: journeys, attacks, fixtures and benchmarks on every change

## Goal
Grow the gate harness into the proof the release will be judged by, kept independent of the code it judges. When this is done: the five journey tests in requirements run from pinned public fixtures and tear everything down; all 44 attacks (ATK-01 to ATK-44) exist as runnable tests on their assigned cases, and protected secrets leak in none of them (BEN-004); the nine lanes on the owner's three machines pass preflight and are scheduled without overloading those machines (QA-001); every fixture is a pinned public third-party project, harness, console or MCP server (QA-002); and every source change queues the performance, reliability and security suite, privately until the first public release, including the comparison with the five required competitors: Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM (BEN-005). A test for something not built yet exists and reports `blocked`, never passed.

The owner's standard is that a specified workflow is not a tested workflow: missing or failed evidence blocks release. This suite tells every other ticket whether it is done, and T30 runs it for certification.

## Context
- Ticket T13 of 31; `docs/tickets/README.md` lists them all and what each waits for. The engine gate (T6) has passed; this builds on T5's harness. It runs alongside every feature ticket from here to T30. They build what this measures, and a failure found here goes back to the ticket that owns the code. This ticket owns the journeys, the attack suite, the fixtures, lane scheduling and the benchmarks.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- Lane `LA` is the Lima VM `lamp-la` on the M5 (`limactl`). The nested lanes `LX22`, `D12`, `F40`, `L510` and `NV` are libvirt VMs on the Ubuntu laptop named `lamp-lx22`, `lamp-d12`, `lamp-f40`, `lamp-l510` and `lamp-nv` (`sudo virsh`). All of them were stopped on 2026-11-09.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- The Lamp repository is `JohanSardar/lamp` on GitHub, private; `gh` on the M5 is signed in as that account.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
