# T27 Dashboard and notifications: evidence

Two runs of `tools/dashboard_demo.py` on the Ubuntu laptop (LX), 2026-11-11. Both used the T27-owned store `/var/lib/lamp/t27` with a real Firecracker supervisor. The dashboard was served by `lampd --dashboard-listen 127.0.0.1:7480` and driven through `tools/dashboard_cdp.mjs`, in a disposable headless Chromium shell on the M5, reached over an SSH tunnel. No visible screen was used. Notification sinks were a local signed webhook receiver and a dead port. The saved login was the public test login of the-internet.herokuapp.com, from a fixture vault.

- **Run 26** (`transcript-run26.jsonl`, top-level screenshots). The supervisor binary came from T22's build. The run went through browser takeover, approval and fill, the session answer, sharing and revoke. Suspend then failed: that older supervisor rejected lampd's newer suspend request ("unknown or non-canonical fields"). The dashboard showed this as an operation in `outcome_unknown`, with the error.
- **Run 28** (`transcript-run28.jsonl`, `run28/`). The supervisor was rebuilt from this branch. The run used `--skip-browser` because, with that rebuilt supervisor, the browser VM's guest could not reach the network (run 27: `GUEST_ACTION_FAILED:navigate.network`). Every other step completed, ending with `complete`, three signed webhooks, and zero Firecrackers left.

## What was shown

| Goal item | Evidence |
|---|---|
| One page served by the host; no data without a credential | API without bearer → 401; `01-sign-in.png`, `13-action-link-signed-out.png` |
| Setup progress and failures | `02-setup-progress.png`; earlier runs recorded real setup failures (uplink netns error), shown as failed checks |
| Idle and task-budget countdowns | `03-overview-ready.png`, `run28/07-task-budget.png` |
| Saved state | `run28/06b-saved-state.png`: suspended, with retention countdown, suspend succeeded |
| Sessions: needs an answer, answer from the page | `03b-session-needs-answer.png`; the answer reached the real shell (run 28 transcript) |
| Sharing; the other person sees only the share | `03c-bea-sees-only-shared.png`, `03d-sharing.png`; share revoked |
| Approval showing the actual effect | `05-approval-effect.png`: operation, browser, destination, vault/item names, exact arguments, digest |
| Private browser takeover | `04-private-takeover.png`, `06-approved-fill-result.png` (run 26) |
| Budgets, task stop | `run28/07-task-budget.png`, `run28/10-after-stop.png` |
| Audit | `run28/11-audit.png` |
| Feature availability from installed operations; operational errors | `run28/12-features-errors.png` |
| Done / stuck / needs-decision to sinks | signed webhooks for `task.needs_decision` (approval and session) and `task.done`; no credential in any payload |
| Inspectable retries, acknowledgement | `run28/08-notifications-retries.png`, `run28/09-retry-acknowledged.png`: offline sink, 4 attempts with errors, manual retry, acknowledged |
| Private signed-in action links | links are `PublicURL/#/…` without credentials; opening one signed out asks to sign in |

## Review follow-up (runs 29 and fleet-4, current build)

- **Browser VM had no network on this branch's supervisor (run 27).** The cause was my merge of T22 into this branch. It dropped T22's "vanished process" handling from `status()` in `crates/lamp-guard/src/process.rs`, so the guard failed whenever a short-lived browser process exited before it was observed. With that arm restored, **run 29** (`transcript-run29.jsonl`, `run29/`) used the supervisor and guard built from this branch. It went through the full journey: private takeover with the sealed view of the-internet.herokuapp.com (`run29/04-private-takeover.png`, `run29/06-approved-fill-result.png`), the approval consumed, and suspend.
- **Stuck.** A budget watchdog (`block_after_ms`) that ends a task which never finished now marks the task `blocked` rather than `stopped`. In run 29, a task with a 20 s watchdog became `blocked`, and the phone sink received a signed `task.stuck` webhook (`run29/07b-task-stuck.png`, step "stuck task and webhook").
- **Multi-host** (`tools/dashboard_fleet_demo.py`, `transcript-fleet.jsonl`, `fleet/`). Two lampd hosts formed one enrolled fleet, with a private CA and mTLS on loopback:
  - hs_a was compute, using this branch's supervisor.
  - hs_b was control-only and served the dashboard.
  - hs_b's page listed both hosts and hs_a's sandbox (`fleet/f1-hosts-from-b.png`).
  - hs_a held a suspended sandbox's saved state and was put to sleep by stopping its lampd. hs_b's page then showed hs_a `unavailable`, with "Saved state stranded on this unavailable host" (`fleet/f2-stranded-saved-state.png`), and the sandbox page explained why it could not resume (`fleet/f3-stranded-sandbox.png`).
  - hs_a then woke, and the sandbox was deleted.
  - Two fixes this needed: the fleet cache now keeps a sleeping host's suspended sandbox as `suspended` instead of `unavailable`, and the page re-reads its lists every 10 s, because a sleeping peer cannot emit an event.
  - Remaining gap: the retention end date for the stranded state shows "—" on hs_b, because the countdown fields are computed on the sandbox's home host and are not in the fleet-merged copy.

## Not shown live

- Nothing further. The earlier gaps (multi-host, stuck and current-build browser) are covered above.
