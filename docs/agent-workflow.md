# Agent workflow

[Agent enrollment and credential custody](agents.md) describes starting an
OpenCode or Codex session. Add `--repository https://gitlab.com/group/public.git`
to session creation to clone a public repository into private session storage.
Repository URLs cannot contain credentials, queries or fragments. Each normal
session checks out `lamp/<session-id>`; ordinary children get copied files and
a fresh conversation on their own branch.

## Children and returned changes

With `nested_create:true` on the enrolled harness, an agent has the following
CLI inside its sandbox, and equivalent `spawn`, `message`, `wait`, `cancel` and
`git_return` MCP tools:

```sh
lamp session spawn --sandbox-mode new --prompt 'Write a.txt, commit it, then run lamp change-set return and report the commit and change-set IDs.'
lamp session wait CHILD_SESSION
lamp session message CHILD_SESSION 'Please explain the change.'
lamp session cancel CHILD_SESSION
lamp change-set return
lamp change-set apply CHANGE_SET
lamp sandbox delete CHILD_SANDBOX
```

`new` provisions a separate sandbox from the parent's template and task, then
copies the parent's files. `same` uses the current sandbox with a private
workspace and branch. The guest capability reaches only its own session,
direct children, its parent's messaging endpoint, and change sets addressed to
it. It cannot choose a different harness or host. It permits one child sandbox
within the daemon's two-live-sandbox capacity; release that child before
creating another. This is capacity enforcement, not a four-VM concurrency claim.
`lamp session list --json` shows `parent_session_id`, sandbox and turn status,
so callers can display the agent tree.

From the owner's CLI/API, spawn can also select `sandbox:<id>` or `host:<id>`.
A returned change set is proposed first; applying it requires target input
authority. A dirty target or conflicting patch reports `conflicted` without
replacing the caller's work. Select paths when creating the change set.

```sh
lamp sandbox diff SB --session-id SESSION
lamp sandbox pull-changes SB --session-id SESSION --checkout ./public-clone
```

Diff compares the session workspace with its recorded base. Pull fetches the
committed head onto local branch `lamp/<session-id>` and leaves the caller's
current branch and files in place. Commit edits before pulling; teleport and
change-set capture automatically commit dirty nonignored files. These helpers
use Lamp's public exec API. They require a running sandbox/session and Git in
the receiving checkout.

## Side chats and messaging

```sh
lamp session side SESSION --prompt 'Quoted context: explain this function; send your answer to the main session.'
lamp session message MAIN_SESSION --text 'The side chat found ...'
```

A side chat has fresh conversation history but **shares the main session's
files and branch**. Concurrent edits are visible immediately. Inside the side
chat, use `lamp session message MAIN_SESSION 'answer'` to send the result back.
End side sessions before teleporting their main session. An active side chat
blocks teleport; it is not silently abandoned or moved separately.

## Laptop ↔ sandbox teleport

Host execution is opt-in. The daemon needs a private `--sessions-config` with
an enrolled host owned by the caller, a disposable session root and Python 3.
It also needs a native harness profile for that host's explicitly installed
binary. For example, merge the following entries into the owner's private
configuration (substitute the actual principal and binary):

```json
{
  "hosts": {"hs_local": {"owner_id":"PRINCIPAL", "command":["/usr/bin/python3"], "root":"/private/path/lamp-sessions"}},
  "harnesses": {"codex": {"owner_id":"PRINCIPAL", "argv":["/usr/local/bin/codex"], "cwd":"{session_dir}/workspace", "env":{"HOME":"{session_dir}/home", "PATH":"/usr/local/bin:/usr/bin:/bin"}, "auth_modes":["subscription"], "native":{"kind":"codex", "binary":"/usr/local/bin/codex", "managed":true, "profiles":{"host:hs_local":{"binary":"/absolute/path/to/codex"}}}}}
}
```

The host enrollment authorizes command execution in that private session
workspace; no host runner is enabled by default. Do not point its root or
harness cwd at an existing working checkout. Managed Codex uses workspace-write
on the host and the VM boundary inside the sandbox. Model credentials remain
in host custody at both locations; each location epoch gets a new scoped
transport capability. Host model calls retain the session's task budget.

```sh
lamp session teleport SESSION --to sandbox --sandbox-id SB --check
lamp session teleport SESSION --to sandbox --sandbox-id SB --wait-ms 60000
lamp session get --session-id SESSION --json
lamp session teleport SESSION --to local --wait-ms 60000
```

`--check` is a GET and does not fence a runner or change revision/epoch. Inspect
`transfer.phase`, `bytes_transferred`, `artifact_bytes` and
`safe_to_close_source` on the session. Close the source only after the operation
succeeds and that signal is true. `lamp operation cancel` can prevent a queued
teleport before snapshot dispatch. Once fencing has started, cancellation
refuses with `OUTCOME_UNKNOWN`; inspect the original operation rather than
starting a competing move.

Teleport preserves the native conversation, queued input, branch and tracked
or nonignored changed files. Native continuation support depends on the
harness adapter. Ignored files, running processes, credentials and caches do
not move. A new location epoch uses a new directory; existing source files
remain retained, and different target work is refused rather than overwritten.
A snapshot failure leaves the source epoch usable; a later transfer failure
retains source files and reports failure. Transfer archives have a 16 MiB
retention limit. A running turn must finish or be cancelled before its native
snapshot can proceed.

## Cancel and reconnect

```sh
lamp session message SESSION --text 'Run sleep 120, then report.'
lamp session message SESSION --text 'Reply OK afterwards.'
lamp session cancel SESSION
```

Cancel interrupts the active native turn and its tool descendants. Its control
history records `input_completed` with `interrupted:true`; session `last_turn`
reports `interrupted` until a later turn completes. Queued messages remain and
run next. A message is accepted durably before the caller disconnects. Rejoin
through attach or read `/v1/sessions/SESSION/output` to recover retained output,
including after a daemon restart. Ctrl-C on attach disconnects the client; use
session cancel to stop an agent turn.

## T3 Code and Herdr

Release archives include `tools/session_acp.py`, `session_herdr.py`,
`session_public.py`, `session_ssh.py` and `session_mcp.py`. Run them with Python
3; `--help` lists their public-API connection options. For T3, configure a local
ACP provider command pointing to `python3 /path/to/tools/session_acp.py`, with
`--socket`, `--token-file`, `--harness`, `--auth-mode`, location flags and
`--join-mode input --prompt-envelope t3`. List/import existing Lamp sessions
through T3's ACP registry. ACP cancellation uses the same native turn cancel.

For Herdr, provide a stock SSH target enrolled through Lamp access, then run:

```sh
python3 /path/to/tools/session_herdr.py --socket /path/api.sock \
  --token-file /private/owner.token --fleet FLEET --ssh-target lamp \
  --ssh-config /private/ssh-config --session SESSION --herdr-session lamp
```

This opens an SSH-backed pane in that named Herdr server. `--machine` instead
selects a configured remote Herdr machine. Authentication stays in private
files, never URLs or command values. The adapter does not install or launch
T3 Code or Herdr automatically.
