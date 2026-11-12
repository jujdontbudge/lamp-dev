# Subscription sign-in and fork: real attempt

## Result

**Incomplete. A real subscription sign-in that continues after a fork has not
been demonstrated.** This is not acceptance evidence for that requirement.

On 2026-11-10 the owner released the Ubuntu laptop for one demonstration, with
at most two simultaneous sandboxes and at least 12 GiB available. The corrected
tunnel handler was built on LX and used by an actual Firecracker guest running
the official, unmodified Codex 0.166.0 binary.

The official client successfully obtained a `chatgptDeviceCode` challenge through
the confined vendor proxy. Completing that challenge required account
authorization. A fresh, isolated headless Chrome instance reached OpenAI's
`Just a moment...` verification page, with no sign-in form available.
The secure owner-approval request was first interrupted and then timed out.
During that attempt, no owner-app credentials or browser profiles were read, and the verification
challenge was not bypassed.

A later fresh headless retry, intended to allow ordinary page verification to
settle, failed navigation with `net::ERR_INTERNET_DISCONNECTED`. This does not
establish that the vendor page could never complete; no authorization was
obtained during this attempt.

The sign-in operation was `outcome_unknown` when inspected before release.
No successful account/subscription receipt or inference result was obtained.
The retained summary's negative sign-in fields describe the missing proof;
they are not an independent inspection of the vendor's final account state.
Forking an unauthenticated guest would not demonstrate the requested behavior,
so no fork was attempted.

## Real artifacts

- Run: `attempts/t9-signin-20261110-07/summary.json`.
- Actual vendor tunnel admission and counters: `proxy-audit.ndjson` in that run.
- Resource observations: `resource-samples.jsonl` in that run.
- Guest: `sb_17c535fe8282cee6bdcae41bb4ef7b4c`, fixture `/var/lib/lamp/t9e07`.
- Codex binary SHA-256:
  `b8a11fedf24b2dfb59a70c9e52b030f9a508dcd49cd2dd0d41956f4c785156b8`.
- Deployed implementation: `9c9b45c8aacac49a214ad34ff22d4147aded6b1d`.
  This attempt preceded the subsequent T10 control-core merge.

The device challenge and short-lived code stayed in private files, not in this
document or the committed evidence. The proxy audit contains vendor names and
counters, not CONNECT headers, authentication URLs/codes or TLS payloads.
The real workload asks for the fixed nonsecret response
`LAMP_SUBSCRIPTION_FORK_OK` without tools, so a successful future source/fork
pair cannot accidentally create a third task sandbox. No tests were added or
extended for this attempt.

## Limits and release

- Maximum simultaneous sandboxes: **1**, including the sequential template builds.
- Minimum observed available memory: **24,885,805,056 bytes (23.18 GiB)**.
- Public sandbox deletion: `op_bea23047ab0828d52513d38173ede87c`, succeeded.
- Remaining host Firecracker processes after release: **0**.
- Both `/tmp/lamp-lx-timing.lock` and `/tmp/lamp-hard-timing.lock` were confirmed free.
- The demonstration's supervisor and control services were stopped; raw artifacts
  remain under `/var/lib/lamp/t9e07` and
  `/home/owner/lamp-t9-custody-20261110/demo-20261110-01`.

## Main integration

Main at `2ce9e8c8c3a2b02aac787f006d2d4f04294732ab` was merged afterward.
Conflict resolution retains T10 identity enrollment, profiling, scoped callers,
and grouped journal commits alongside T9's clean exports and read-only shares.
Custody objects are included in the new journal write set and replay; custody
routes are registered with the unified CLI/API/MCP catalogue and enforce the
existing caller/resource checks. No spec, ticket or seccomp profile was changed.

The requested single `make verify` run for that merge is retained as
`verify-macos-main-2ce9e8c-01.log`. Go and Rust passed; it then exited 2 because
the historical clean-builder evidence check expects exactly four control
directories and the new live attempt had been placed alongside them.
The live attempt was moved unchanged into `attempts/`, separate from the
clean-builder controls in `runs/`. No test or recorded result was changed.
The existing failing check and the remaining verification steps are recorded in
`verify-macos-main-2ce9e8c-continuation-01.log`, without repeating the full
`make verify` command. That continuation exited 0. Verification is not a substitute for the missing real
demonstration.

## Authorized browser follow-up

At the owner's request, one additional attempt used an authorized, restricted
copy of the M5 Chrome Default session. This was a browser preflight, not a new
sandbox sign-in-and-fork run.

`Local State` and a read-only SQLite backup of `Default/Cookies` were copied to
a new private scratch user-data directory. Only cookies for `chatgpt.com`,
`openai.com` and their subdomains were retained in the copy. The original
profile's contents were not changed. No cookie values or tokens were printed
or committed.

A separate Chrome was launched through `open -n -g` with `--headless=new`,
the scratch user-data directory and loopback CDP on port 9334. Playwright
connected over CDP and visited `https://auth.openai.com/codex/device`.
After a 30-second wait for a visible input, the page still said
`Just a moment...` and had zero visible inputs.

The requested automation-detection override and user-agent spoofing were
**not used**. This result therefore does not establish that the exact proposed
anti-detection route failed. Nor does it establish that Chrome decrypted the
copied session successfully or that the copied account session was still valid:
authorization never advanced past the verification page.

No fresh guest device challenge, inference or fork was attempted after that
browser preflight failed. No LX sandbox was created and no LX memory was
allocated. No new owner-approval request was made.

Only the owned headless Chrome (PID 6238) was stopped, and the scratch profile
was deleted. Follow-up checks found no owned Chrome process or port-9334
listener, and confirmed the scratch directory was absent. The owner's visible
window was not driven.

The token-free record is
`attempts/t9-authorized-browser-20261110-08/summary.json`. This follow-up changes
only evidence/docs; it does not change implementation or tests, and it does not
supersede the verification results recorded for the previous implementation.

## Remaining status

The owner instructed that an unsuccessful follow-up be recorded and then
stopped, with PR #10 eligible to merge as-is. This thread makes no further
attempts. Sign-in-and-fork acceptance and E07 remain incomplete; the format
freeze remains unauthorized. The owner has authorized the accounts; no new
owner-approval requirement is being imposed.
