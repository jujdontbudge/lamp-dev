# Broker

## Goal
Build `lamp-broker`, so agents can use the owner's accounts without ever holding the keys. When this is done: model API keys stay behind a host proxy and never enter the task sandbox (CRED-005); chosen settings copy one way into sandboxes, and sandbox edits come back only through an explicit, authenticated, selected-path approval, with signing keys kept out of task memory (CRED-006); signed-in MCP servers are reached through a gateway (CON-001); the signed-in CLIs `gh`, `stripe` and `aws` do real authorized work without their tokens entering the sandbox (CON-002); and each owner-machine service offers `off`, `read_only`, `ask_first` or `full`, enforced on real operations (CON-003).

Lamp's promise is zero leaked API keys, passwords or browser cookies during sandbox work, while agents still get ready-to-use sign-in. Sessions (T21), kits (T22) and metering (T23) all build on this broker.

## Context
- Feature prerequisites: T10, T11. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- An OpenRouter key is in `~/.pi/agent/auth.json` on the M5. That account has almost no credit, so only its free models work.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never print, log or commit a secret.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
