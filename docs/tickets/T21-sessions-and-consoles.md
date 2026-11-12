# Sessions and consoles

## Goal
Let people and consoles see and join every piece of agent work. When this is done: a harness can run inside a sandbox (INT-006) or drive one from outside (INT-007); any console sees one fleet machine with each session separate, live and interactive (SES-001, SES-002); people see only their own sessions unless someone shares (SES-006), and watching and typing are separate, explicit grants (SES-007); sub-agents run where the caller chooses, each separately visible, and their work returns through git (SES-004); and a session moves between a laptop and a sandbox in both directions with no lost acknowledged input and never two writers (SES-005). Integrate Claude Code and Codex, their permitted API and subscription routes, the public T3 Code and Herdr console contracts, and stock SSH.

Journeys 1, 3 and 5: Alice watches work in her console and answers when it needs her, sees each fork's sub-agents separately, shares a session with Bea for watching or typing, and moves a session onto her laptop and back.

## Context
- Feature prerequisites: T9, T17. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh owner-air`. All three are the owner’s everyday machines.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `<owner-notes>/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- An OpenRouter key is in `~/.pi/agent/auth.json` on the M5. That account has almost no credit, so only its free models work.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `<owner-skills>/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- T3 Code and Herdr use their public console contracts only, never through a special integration with the owner's own projects.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
