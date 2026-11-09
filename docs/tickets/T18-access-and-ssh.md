# Access: Tailscale, owner domain, local-only, Cloudflare Tunnel and SSH

## Goal
Give people and CI their ways to reach a fleet. When this is done: built-in Tailscale access reaches hosts behind NAT with no manual inbound setup and supports CI federation without stored secrets (ACC-001); an owner's own domain gets automatic HTTPS (ACC-002); a fleet works local-only and offline (ACC-003); Cloudflare Tunnel works as an access plugin (ACC-004); and ordinary OpenSSH reaches `sessions`, `attach <session_id>` and `shell <sandbox_id>` over each of these routes, with a fleet SSH host CA and no host shell or forwarding by default (SES-003).

Lamp is self-hosted: the owner controls how their machines are reached and can run everything locally. Ordinary SSH is the baseline console every person and tool already has, and CI (T26) needs the keyless connection built here.

## Context
- Ticket T18 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T10's control core and T11's plugin host. Four tickets run at the same time: T14 (format freeze), T15 (Mac devices and fallback), T16 (fleet) and T17 (broker). This ticket owns `lamp-access` and the SSH front. T24 adds the relay route and the Eilmer account; T26 uses the keyless CI connection.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- All three machines reach each other only over the owner's Tailscale network.
- eilmer.si DNS is on Cloudflare and there is no API token. The way to change it without the owner's screen is written up in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md`. The owner agreed to Lamp's DNS changes on 2026-11-09.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.

## Constraints
- eilmer.si also serves the owner's other products. Check what they use before touching any existing record or CAA, and never break them.
- Every hosted address is under eilmer.si. Never propose or use a second domain; the owner rejected that outright.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
