# Public release

## Goal
Put Lamp's first release in front of the public. When this is done: the repository is public under its licences (Apache-2.0 for host code, SDKs and shipped plugins; AGPL-3.0 for the relay); the release packages and the TypeScript and Python SDKs are published; the benchmark data is published with the release (O6); and the hosted eilmer.si pieces accept sign-ups within the certificate capacity actually obtained (OPS-007).

This is what the whole project is for: open-source software anyone can install on their own machines to run agents, harnesses and CI in fast, isolated sandboxes. None of it can be taken back once strangers have seen it.

## Context
- Ticket T31 of 31; `docs/tickets/README.md` lists them all and what each waits for. T30 has finished. This ticket owns the release itself.
- The owner said on 2026-11-09 that the build takes Lamp public itself, without waiting for them.
- The Lamp repository is `JohanSardar/lamp` on GitHub, private; `gh` on the M5 is signed in as that account.
- The Eilmer account's Clerk app already exists, with a production instance on eilmer.si and verified DNS. Its IDs and what is still open are in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/eilmer-clerk-app.md`.
- eilmer.si DNS is on Cloudflare and there is no API token. The way to change it without the owner's screen is written up in `/Users/johansardar/.claude/projects/-Users-johansardar--t3-projects-lamp/memory/cloudflare-headless-access.md`. The owner agreed to Lamp's DNS changes on 2026-11-09.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- Nothing is made public unless T30's result shows every required row passing and none `blocked`: the owner's standard is that the first release waits for the entire brief. Each step here is public and cannot be undone.
- No secret, private path or private account detail appears in anything published.
- eilmer.si also serves the owner's other products. Check what they use before touching any existing record or CAA, and never break them.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
