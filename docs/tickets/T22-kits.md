# Kits

## Goal
Give sandboxes ready-to-use tools without handing agents the owner's secrets. When this is done: a logged-in browser runs outside the agent's sandbox by default, in its own locked-down sandbox (KIT-001); a browser inside the sandbox with copied logins is opt-in and flagged (KIT-002); a person can privately take over the browser and hand it back on a safe page (KIT-003); toolchain kits install requested tools, and Docker runs only inside the sandbox with no host daemon or socket (KIT-004); and 1Password, Bitwarden, KeePassXC and Proton Pass each fill standard login forms on the person's behalf with per-vault approvals, with the person typing privately wherever Lamp cannot prove the password stays hidden from the agent (KIT-005, B3).

Ready tools and sign-in is one of Lamp's four equal goals, and so is protecting secrets. Journey 5 has Alice privately take over a browser and hand it back on a safe page.

## Context
- Feature prerequisites: T17. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh johansardars-macbook-air`. All three are the owner’s everyday machines.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- The owner named 1Password and Proton among the accounts they already pay for.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Test jobs and fixtures use public third-party projects and disposable test accounts, never the owner's own projects or repositories.
- The owner's real vault items, browser profiles and logins are never test data. Use disposable test vaults and test sites.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
