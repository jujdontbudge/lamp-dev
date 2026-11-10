# Hosted side

## Goal
Build the optional hosted pieces that let a team reach a fleet from anywhere without trusting Eilmer with their work. When this is done: relay use requires the one shared Eilmer account, while fleets that do not use the relay work with local accounts or their own OIDC, and identity is the immutable issuer plus subject, never an email match (ACC-005); the relay makes NAT-hidden fleets reachable using one-time enrollment credentials (RLY-001) and only forwards ciphertext, never decrypting traffic or holding model credentials, transcripts or sandbox data (RLY-002); and every Eilmer-hosted address lives under eilmer.si with cookie and origin isolation and a separate, constrained certificate issuer (OPS-005). The development relay runs on the Ubuntu laptop. Hosted services preserve account, origin and cookie isolation, and operate within the certificate capacity actually available.

Journey 5: Alice and Bea join the same fleet through the optional relay. The owner decided there is one Eilmer account for all their products (ADR 0005), everything lives under eilmer.si (ADR 0006), and the relay never decrypts traffic (ADR 0009).

## Context
- Feature prerequisites: T18. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh johansardars-macbook-air`. All three are the owner’s everyday machines.
- The Eilmer account's Clerk app already exists, with a production instance on eilmer.si and verified DNS. Its IDs and what is still open are in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/eilmer-clerk-app.md`.
- eilmer.si DNS is on Cloudflare and there is no API token. The way to change it without the owner's screen is written up in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md`. The owner agreed to Lamp's DNS changes on 2026-11-09.
- The owner said on 2026-11-09 that the build does every sign-in and account setup itself, using the accounts and subscriptions they already pay for, and agreed to terms of service being accepted in their name for Lamp test accounts. Chrome on the MacBook Air was signed in to Cloudflare, and `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md` describes how that session was used without touching the owner's screen; Google rejects sign-in from automated browsers. Only the owner can approve a phone 2FA prompt or read an SMS code.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- eilmer.si also serves the owner's other products. Check what they use before touching any existing record or CAA, and never break them.
- Never propose or use a second domain; the owner rejected that outright.
- Nothing here is opened to the public. Public sign-up waits for T31.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
