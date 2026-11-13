# T46 live workflow evidence — 2026-11-13

This is development evidence, not a performance certification. The fixture was
the public `https://gitlab.com/gitlab-org/gitlab-test.git` repository, in
private disposable clones. No owner project, browser profile, visible app,
existing Docker resource or installed Lamp instance was changed. The GitLab
fixture substitutes for the ticket's GitHub example because this task prohibits
GitHub access.

## Observed behavior

[workflow.json](workflow.json) contains public API observations and actual
agent responses. The successful VM sequence used a disposable M5
Virtualization.framework supervisor/template, 1 GiB and two vCPUs per VM.
At most the parent and one child VM were live; the children ran sequentially.
The official guest Codex was 0.168.0; the explicitly installed host binary was
0.167.0. The model was `gpt-6.1-sol`, using the owner's explicitly imported
subscription sign-in through host custody. Credentials were excluded from
these records and the disposable custody copy was removed.

1. A local session wrote `marker.txt` (`LAMP_T46`) and remembered “kiwi”.
   The teleport check left revision and epoch unchanged. The same native
   conversation moved to a VM, recalled “kiwi”, read the marker and printed
   its `lamp/<session-id>` branch. It moved back to the enrolled host and
   recalled/read both again, then returned to the VM. The completed session
   exposed the safe-to-close signal. The first recorded progress display used
   base64 transport bytes; the final implementation reports decoded bytes
   bounded by the archive size instead.
2. The parent itself ran the guest Lamp CLI to create two children in distinct
   VMs/workspaces, releasing each child VM before creating the next. Child A
   committed `a.txt` as `c434407e528cd46016e9193c8f3b74755de9428f` and
   returned `cs_4e6b478bc5bae7466b6d8752202208b5`. Child B committed `b.txt`
   as `2a13a4fe4e1ca2316b73f801455d441f6a306e55` and returned
   `cs_1071dbea66d605e14a1e6048fe6a2980`. The parent summarized both reports.
   The API tree retained both parent links and distinct sandbox IDs.
3. Both change sets applied to the parent after the child VMs were deleted.
   A public exec read `CHILD_A` and `CHILD_B`. `lamp sandbox diff` displayed
   both file additions. `lamp sandbox pull-changes --checkout ...` fetched
   their committed contents onto `lamp/<parent-id>` while the receiver stayed
   on `master`.
4. On a fresh enrolled local session, a side chat wrote `side.txt`, said it did
   not know the main conversation's secret word, and sent its answer with
   the scoped guest CLI. The main session replied `SIDE_ACK`; reading the
   main workspace returned `SHARED_SIDE`. An active side blocked teleport.
5. After observing an actual `sleep 120` tool process, the caller queued
   another message and cancelled the current turn. Control history recorded
   `interrupted:true`, the next message returned `QUEUE_OK`, and no sleep
   process remained. Codex logged a missing interrupted tool-output warning;
   subsequent turns still completed. This warning is preserved in the raw
   evidence, not relabelled as a clean vendor log.
6. A task asking the agent to sleep and report was accepted, then the daemon
   was stopped and restarted while work was pending. The same conversation
   completed and retained `DISCONNECTED_WORK_DONE` in history. A real stdio
   MCP client initialized the shipped session gateway, listed `spawn`,
   `message`, `wait`, `cancel`, `git_return`, and used `wait` to recover its
   22 retained frames.

[bridges.json](bridges.json) records a separate actual bridge run against the
VM session. A dedicated headless T3 server used Lamp's ACP registry to list and
import it, sent a prompt, and returned `T3_BRIDGE_OK` in the thread's assistant
message. The server modules were run headlessly; the owner's visible T3 app
was not driven or relaunched. A dedicated named Herdr server (`lamp-t46`) used
`session_herdr.py` and stock SSH into Lamp access. Its pane `w2:p1` displayed
both `SANDBOX_READY` and `T3_BRIDGE_OK`. Both dedicated servers and their private
configuration/token files were removed afterwards.

[sessions.json](sessions.json) is the existing session demonstration run with
this public GitLab fixture. It also observed native PTYs, explicit watch/input
grants, stock SSH, writer exclusivity and retained output across daemon restart.
Its own exclusions remain valid for that particular run; the VM and bridge
observations above are separate evidence.

## Failures and practical limits

Earlier attempts failed and remain historical failures:

- Inline continuation archives expanded each control-journal receipt, causing
  excessive disk growth. Transfers now store bounded archives in private
  files before committing offsets; the repeated move used that implementation.
- A second move from a shallow Git clone failed because restored repositories
  lost their shallow boundary. Capture/restore now preserves validated shallow
  object IDs; the subsequent host/VM round trip succeeded.
- A resumed Codex sandbox initially denied guest Lamp networking. The VM now
  supplies the execution boundary; the subsequent two-child run succeeded.
- The first CLI diff call supplied an unsupported `session_list` filter. That
  argument was removed and the actual diff and pull then succeeded. An earlier
  file-read assertion also ran before exec output was available; waiting for
  the real exec completion returned both files.
- The original disposable task reached its one-hour budget during this long
  development session and stopped further VM work. A fresh VM then failed its
  npm install with `ERR_SOCKET_TIMEOUT` and a link-down guest route. The
  side/cancel/restart sequence therefore used the enrolled local runner, not
  that failed VM. The already successful VM observations are not a claim that
  the later install succeeded. The disposable supervisor used the prior
  ticket's binaries; final source integration was checked with the local build.
- The first local side could edit but its shell networking was denied, and
  that Codex turn did not expose the MCP fallback. Explicitly enrolled host
  workspaces now permit networking. A new side sent the message successfully.
  The direct MCP-client observation verifies the gateway independently; it
  does not claim the earlier Codex MCP discovery succeeded.

Host execution still requires explicit private host enrollment and an installed
native binary. Teleport cancellation prevents a move only before snapshot
fencing is dispatched; later cancellation refuses with `OUTCOME_UNKNOWN`.
Ignored files and running processes do not teleport. Active side chats must
end before their main session moves. No four-VM concurrency or Cloudroom timing
parity was measured. The 16 MiB continuation quota remains in force.

## Checks and cleanup

- `CARGO_TARGET_DIR=/tmp/l46cargo make build`: passed, including Go/Rust
  workspace builds and generated SDK consistency; repeated after rebase onto
  current main and after the host networking correction.
- `go vet ./cmd/lamp ./internal/control ./internal/surface`: passed.
- `python3 tools/generate.py --check`, Python AST parsing of changed adapters,
  and `git diff --check`: passed.
- Demonstrations used the actual CLI, HTTP API, MCP gateway, official model
  binary and bridge servers. No benchmark runs or feature test suite was
  added. Sol later supplied one regression test for a demonstrated host-child
  bug; it failed before the one-line fix and passed afterwards. See
  [the review ledger](review.md).
- Public cleanup reported zero live disposable sandboxes. The dedicated
  supervisor, VM store, runners, servers, credential copies, clones and large
  build caches were removed. The two disposable groups were deleted. macOS
  refused cleanup of user records `_lampvm460` (UID 100460) and `_lampvm461`
  (UID 100461): `dscl` delete hung and bounded retries timed out; removing the
  verified disposable record files returned `Operation not permitted` even
  under `sudo -n`. Those hidden, nonlogin (`/usr/bin/false`, password `*`)
  records remain. No password, approval dialog or system protection bypass
  was attempted. They have no remaining VM processes, storage or group. Existing
  T43 assets and other agents' processes were left alone. Disk free space was
  checked throughout and stayed above the required 20 GiB.
