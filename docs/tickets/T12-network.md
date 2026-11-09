# Network: exits, private networks, dev-server URLs and rules

## Goal
Finish Lamp's host-owned networking. When this is done, a caller can pick an independent outbound exit for a sandbox (NET-002); give a sandbox its own private-network identity with only mapped inbound ports (NET-003); open a dev-server port at a private signed-in URL or an expiring, revocable share (NET-004); set allow and deny lists (NET-005); and explicitly allow one sandbox to reach another (NET-006). All of it is enforced on the host, so guest root, nested Docker included, cannot change routes, VPN keys or policy. E09 passes on every required host and engine pairing: zero denied IPv4, IPv6, LAN, metadata or neighbour packets under rebinding, QUIC, and guard or VPN death; an approved service and three verified exits work; and a peer sees the sandbox's own Tailscale identity.

Protecting the host and the owner's network from whatever an agent runs is one of Lamp's four equal goals, and running strangers' pull requests in CI (T26) depends on these allow lists.

## Context
- Ticket T12 of 31; `docs/tickets/README.md` lists them all and what each waits for. The engine gate (T6) has passed; this builds on T4's guard. Six tickets run at the same time: T7 (storage proof), T8 (Mac guest classes, whose engine adapter the Mac guard path needs), T9 (sign-in custody), T10 (control core), T11 (plugin host) and T13 (proof suite). This ticket owns `lamp-guard` and network policy. T26 (CI) starts from this ticket's result.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- All three machines reach each other only over the owner's Tailscale network.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.

## Constraints
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
