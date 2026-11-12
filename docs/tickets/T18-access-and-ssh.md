# Access and SSH

## Goal
Give people and CI their ways to reach a fleet. When this is done: built-in Tailscale access reaches hosts behind NAT with no manual inbound setup and supports CI federation without stored secrets (ACC-001); an owner's own domain gets automatic HTTPS (ACC-002); a fleet works local-only and offline (ACC-003); Cloudflare Tunnel works as an access plugin (ACC-004); and ordinary OpenSSH reaches `sessions`, `attach <session_id>` and `shell <sandbox_id>` over each of these routes, with a fleet SSH host CA and no host shell or forwarding by default (SES-003).

Lamp is self-hosted: the owner controls how their machines are reached and can run everything locally. Ordinary SSH is the baseline console every person and tool already has, and CI (T26) needs the keyless connection built here.

## Context
- Feature prerequisites: T10, T11. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh owner-air`. All three are the owner’s everyday machines.
- All three machines reach each other only over the owner's Tailscale network.
- eilmer.si DNS is on Cloudflare and there is no API token. The way to change it without the owner's screen is written up in `<owner-notes>/cloudflare-headless-access.md`. The owner agreed to Lamp's DNS changes on 2026-11-09.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `<owner-notes>/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `<owner-skills>/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- eilmer.si also serves the owner's other products. Check what they use before touching any existing record or CAA, and never break them.
- Every hosted address is under eilmer.si. Never propose or use a second domain; the owner rejected that outright.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
