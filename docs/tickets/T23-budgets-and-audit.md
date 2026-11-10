# Budgets and audit

## Goal
Make every sandbox accountable and stoppable. When this is done: a task and its descendants share time and token budgets, with exact reservations where Lamp's proxy mediates the route, visible best-effort usage plus a hard time stop on opaque routes, and a watchdog that stops everything when the time runs out (OPS-002, A4); every process start, connection and mediated request is in a per-sandbox audit record that can be exported (OPS-004, B4); every active and retained resource is charged to a quota, with bounded queues, so exhaustion never breaks unrelated admitted work (OPS-009); revoking a key, role or grant stops derived authority on connected hosts and shows what is still waiting to expire offline (ACC-009); usage data is opt-in, numeric counts and timings only, kept 90 days (OPS-008); and there is no payment system, but the usage and admission ledger leaves room for plans, quotas and billing later (OPS-006).

An owner who lets agents run unattended needs to know what they did, cap what they spend, and stop them at once. Journey 5 ends with Alice revoking access and seeing which machines have stopped and which are still waiting to expire.

## Context
- Feature prerequisites: T16, T17. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh johansardars-macbook-air`. All three are the owner’s everyday machines.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
