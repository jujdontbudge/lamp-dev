# Control core

## Goal
Grow the demo control path into Lamp's real control core. When this is done: a credentialed caller can do the same things with the same authority through the HTTP API, the `lamp` CLI and the MCP server (INT-001); every CLI command has machine-readable JSON and the product documentation is available as LLM-readable text (INT-003); retries are idempotent and never repeat an uncertain effect (INT-004); there is a live event stream (INT-005); accepted work exposes its operation, partial resources, setup checks and cleanup, "ready" means a real command ran, and dangerous stale edits fail atomically (INT-008); the owner can run a fleet in single-owner, team or public-service mode with the owner, admin, member and viewer roles (ACC-006); keys narrow authority and never broaden it (ACC-007); and protected actions use inspectable one-use approvals bound to the exact action (ACC-008). Go owns control and authorization; Rust owns execution through bounded typed worker RPC (ENG-003).

Agents, harnesses, CI and people all drive Lamp through this surface, and nearly every later ticket adds its resources to it: fleet, broker, access, lifecycle, sessions, CI, the dashboard and the SDKs.

## Context
- Feature prerequisites: T3. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Replicating state across hosts is T16's work. Budgets, the audit ledger and revocation are T23's.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
