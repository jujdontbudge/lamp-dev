# Public release

## Goal
Put Lamp's first release in front of the public. When this is done: the repository is public under its licences (Apache-2.0 for host code, SDKs and shipped plugins; AGPL-3.0 for the relay); the release packages and the TypeScript and Python SDKs are published; one release measurement of the owner’s speed targets and the zero-leak goal is published honestly with available benchmark data (O6 as amended 2026-11-10); and the hosted eilmer.si pieces accept sign-ups within the certificate capacity actually obtained (OPS-007).

This is what the whole project is for: open-source software anyone can install on their own machines to run agents, harnesses and CI in fast, isolated sandboxes. None of it can be taken back once strangers have seen it.

## Context
- Feature prerequisites: T24, T25, T27, T28, T29. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The owner said on 2026-11-09 that the build takes Lamp public itself, without waiting for them.
- The Lamp repository is `JohanSardar/lamp` on GitHub, private; `gh` on the M5 is signed in as that account.
- The Eilmer account's Clerk app already exists, with a production instance on eilmer.si and verified DNS. Its IDs and what is still open are in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/eilmer-clerk-app.md`.
- eilmer.si DNS is on Cloudflare and there is no API token. The way to change it without the owner's screen is written up in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md`. The owner agreed to Lamp's DNS changes on 2026-11-09.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Publish the completed brief features and packages. Disable or retire private persistent repository CI runners before public visibility so untrusted jobs cannot reach the owner’s machines; public CI may remain off.
- No secret, private path or private account detail appears in anything published.
- eilmer.si also serves the owner's other products. Check what they use before touching any existing record or CAA, and never break them.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
