# Network

## Goal
Finish Lamp's host-owned networking. When this is done, a caller can pick an independent outbound exit for a sandbox (NET-002); give a sandbox its own private-network identity with only mapped inbound ports (NET-003); open a dev-server port at a private signed-in URL or an expiring, revocable share (NET-004); set allow and deny lists (NET-005); and explicitly allow one sandbox to reach another (NET-006). All of it is enforced on the host, so guest root, nested Docker included, cannot change routes, VPN keys or policy. Guard or exit failure closes the affected path rather than falling back to direct traffic. A private-network peer sees the sandbox’s own identity.

Protecting the host and the owner's network from whatever an agent runs is one of Lamp's four equal goals, and running strangers' pull requests in CI (T26) depends on these allow lists.

## Context
- Feature prerequisites: T4. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh owner-air`. All three are the owner’s everyday machines.
- All three machines reach each other only over the owner's Tailscale network.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `<owner-notes>/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `<owner-skills>/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
