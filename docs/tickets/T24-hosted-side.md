# Hosted side: Eilmer account, relay and certificates

## Goal
Build the optional hosted pieces that let a team reach a fleet from anywhere without trusting Eilmer with their work. When this is done: relay use requires the one shared Eilmer account, while fleets that do not use the relay work with local accounts or their own OIDC, and identity is the immutable issuer plus subject, never an email match (ACC-005); the relay makes NAT-hidden fleets reachable using one-time enrollment credentials (RLY-001) and only forwards ciphertext, never decrypting traffic or holding model credentials, transcripts or sandbox data (RLY-002); and every Eilmer-hosted address lives under eilmer.si with cookie and origin isolation and a separate, constrained certificate issuer (OPS-005). The development relay runs on the Ubuntu laptop. E12 passes: browser attacks in Chromium, Firefox and WebKit steal or replace nothing; a hostile relay cannot decrypt, enroll or obtain a certificate; and certificate use stays inside the stated weekly budget.

Journey 5: Alice and Bea join the same fleet through the optional relay. The owner decided there is one Eilmer account for all their products (ADR 0005), everything lives under eilmer.si (ADR 0006), and the relay never decrypts traffic (ADR 0009).

## Context
- Ticket T24 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T18's access routes. Five tickets run at the same time: T19 (lifecycle and storage), T20 (Linux breadth), T21 (sessions and consoles), T22 (kits) and T23 (budgets, audit, revocation). This ticket owns the account service, the relay (a separate AGPL-3.0 build) and the certificate issuer. T30 certifies it and T31 opens it to the public.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The Eilmer account's Clerk app already exists, with a production instance on eilmer.si and verified DNS. Its IDs and what is still open are in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/eilmer-clerk-app.md`.
- eilmer.si DNS is on Cloudflare and there is no API token. The way to change it without the owner's screen is written up in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md`. The owner agreed to Lamp's DNS changes on 2026-11-09.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- eilmer.si also serves the owner's other products. Check what they use before touching any existing record or CAA, and never break them.
- Never propose or use a second domain; the owner rejected that outright.
- Nothing here is opened to the public. Public sign-up waits for T31.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
