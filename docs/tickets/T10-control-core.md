# Control core: one API, CLI and MCP surface

## Goal
Grow the demo control path into Lamp's real control core. When this is done: a credentialed caller can do the same things with the same authority through the HTTP API, the `lamp` CLI and the MCP server (INT-001); every CLI command has machine-readable JSON and the product documentation is available as LLM-readable text (INT-003); retries are idempotent and never repeat an uncertain effect (INT-004); there is a live event stream (INT-005); accepted work exposes its operation, partial resources, setup checks and cleanup, "ready" means a real command ran, and dangerous stale edits fail atomically (INT-008); the owner can run a fleet in single-owner, team or public-service mode with the owner, admin, member and viewer roles (ACC-006); keys narrow authority and never broaden it (ACC-007); and protected actions use inspectable one-use approvals bound to the exact action (ACC-008). E03 passes: with 32 callers at once, control, admission and worker RPC stay within 35 ms at the 99th percentile, with zero lost or cross-owner effects (ENG-003).

Agents, harnesses, CI and people all drive Lamp through this surface, and nearly every later ticket adds its resources to it: fleet, broker, access, lifecycle, sessions, CI, the dashboard and the SDKs.

## Context
- Ticket T10 of 31; `docs/tickets/README.md` lists them all and what each waits for. The engine gate (T6) has passed; this builds on T3's control path. Six tickets run at the same time: T7 (storage proof), T8 (Mac guest classes), T9 (sign-in custody), T11 (plugin host), T12 (network) and T13 (proof suite). This ticket owns `lampd`'s operations, authorization and events, the `lamp` CLI and the MCP server. T16 (fleet), T17 (broker), T18 (access) and T19 (lifecycle and storage) start from this ticket's result and add their own resources to this surface.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Replicating state across hosts is T16's work. Budgets, the audit ledger and revocation are T23's.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
