# T21: sessions and consoles evidence

This is a record of actual observations, not a statement that T21 is complete.
Demonstrations use disposable state and the public `octocat/Hello-World`
repository. No owner browser profile, project or vault item is used.

## 1. PTY inside a real sandbox

On 2026-11-11 UTC, `tools/sessions_guest_exercise.py` used the public Lamp API
to create a Firecracker sandbox, create a session in it, acquire an exclusive
input lease, write commands to its PTY and read its output.

- Sandbox: `sb_2af1c3b3a754f2e246674611c164e23f`.
- Session: `se_e61fc0a77a57b0782264da9a9302c2cd`.
- Sandbox resource: `boundary=vm`, `state=ready`.
- Immediately before allocation: zero live Firecracker processes and
  27,395 MiB available on the Ubuntu host.
- The public repository was delivered as a real git bundle because the first
  guest attempt could not resolve `github.com` directly.

Observed output, after the echoed command:

```text
T21_GUEST_TTY=yes
Linux
sb-2af1c3b3a754f2e246674611c164e23f
Hello World!

T21_GUEST_COMPLETED
```

The session was ended, then the sandbox was deleted through the public API.
The retained raw observation is
`/var/lib/lamp/t21a/guest-pty-evidence.json` on Ubuntu.

## 2. Official Claude Code and Codex

The native API gateway route has been exercised on the M5 in disposable clones
of the public repository:

| Official client | Version | Observed model reply | Native continuation |
| --- | --- | --- | --- |
| Claude Code | 2.1.319 | `LAMP_CLAUDE_API_OK` | `42caf173-c2cc-4523-b24a-8a589d59d8a0` |
| Codex CLI | 0.167.0 | `LAMP_CODEX_API_OK` | `01a1c80c-15a5-7142-8f85-2b8c98704d26` |

These were actual model turns, not fixture responses. Both clients spoke their
native protocols through Lamp's credential broker: Anthropic Messages for
Claude Code and OpenAI Responses for Codex. The upstream provider credential
remained in the broker; clients received only a short-lived, scoped Lamp
transport grant. The configured provider key reported a hard limit of $10.
The selected route was `openrouter/free`, with a provider-side maximum price
of zero forced on each request.

The successful observations are retained in `/tmp/l21-native-d/evidence.json`
and `/tmp/l21-native-e/evidence.json` on the M5. Earlier failed attempts remain
in their separate disposable directories.

Those initial M5 observations alone did not demonstrate inside/outside sandbox
operation or subscription authorization. Real guest observations follow below.
Outside driving completed below. Protected subscription turns are still being
completed.

### Real-guest recovery run

A later fresh Firecracker guest, `sb_44b932385a10032dfb1462b049c6bf66`,
first completed an ordinary public exec with exit 0 and output
`T21_BASELINE_EXEC_OK` and `Linux`. Official Claude Code then completed a real
API turn inside that guest:

```text
session: se_70e905cbe374b519a5de22eeb0180eb7
output: T21_CLAUDE_REAL_GUEST
native continuation: 3c94e1aa-74f0-4440-9ad3-408de594135d
```

Codex subsequently reached `active` but failed before producing output. A
following ordinary exec also exited 70. This was traced to the host audit
listener's existing 64 retained collector-segment limit: each session poll was
starting a fresh protected helper process. The guest's audit directory ended at
`process-000064`. The Codex attempt is not counted as a successful model turn.
Its acknowledged input remains in the session journal.

The diagnostic guest was deleted through the public API. The correction in
progress uses one protected persistent session relay over the authenticated,
generation-fenced guest channel; it does not raise or remove the audit limit.
The new worker command and guest helper pass focused Go vet, Mac/Linux
`cargo check`, Linux `cargo clippy -- -D warnings`, and a static musl guest-helper
build. At that point deployment and a completed real Codex turn were pending.
The guest observations are retained in
`/var/lib/lamp/t21a/claude-inside-evidence.json` and
`/var/lib/lamp/t21a/codex-inside-evidence.json` on Ubuntu.

### Completed official Codex turn in a real guest

On 2026-11-11 at 06:04 UTC, official Codex completed the pending real guest turn:

```text
sandbox: sb_bc3648f8ab83916b13a02283de61190d
boundary: vm
session: se_d022be7ac1495f1673d94ac901373781
output: T21_CODEX_REAL_GUEST
status after turn: idle
native continuation: 01a1c934-eaae-7bc2-884e-b57a5cf2f25c
```

The preceding TLS failure was a deployment error: the broker's certificate had
`CA:TRUE` and was being used as its server certificate. The Rust client rejected
it with `unknown certificate`. A separate `CA:FALSE`, `serverAuth` leaf signed
by the existing CA corrected the route. CA verification remained enabled.

The existing API/provider custody, zero-price route, and scoped transport grant
were retained. The protected relay also handled short idle/stat/read/append
infrastructure work without consuming a collector segment for every poll or
file chunk; the audit bound remains 64. No passed demonstration was rerun.

Before this sandbox was created, Ubuntu had one live Firecracker process and
25,648 MiB available, within the shared owner limit.
The raw completed turn is retained in
`/var/lib/lamp/t21a/codex-inside-evidence.json`.

### Completed outside harness driving the sandbox

At 06:12 UTC, official Codex `0.167.0` on the M5 drove the same real
Firecracker sandbox through `tools/session_mcp.py` and the public Lamp API:

- Outside session: `se_e4b0f42f7afb7114af75e52c407b27d9`.
- Location: `host:hs_m5`; `agent_mode=outside`; native continuation
  `01a1c937-3d15-7d60-963c-ac0d40db2734`.
- Sandbox: `sb_bc3648f8ab83916b13a02283de61190d`.
- Acknowledged input: `op_a6b5ecd3b61b66df7e6d9b93d6e4ed11`.
- Public sandbox exec: `ex_343090df55ce6ef5f5368f4e9348a49c`, exit 0.
- Native input completed and the outside session returned to `idle`.

The sandbox's actual stdout, also reported by Codex after its MCP call:

```text
T21_CODEX_OUTSIDE_DRIVES_VM
Linux
Hello World!
```

The command read `README` from the disposable public repository in the guest.
The harness's local shell tools were disabled; its sandbox tool used only the
public exec create/get/output endpoints, not worker RPCs.

Earlier tool calls executed commands but failed to return their results to the
harness. The first reply was empty during a blocking create wait; after removing
that redundant wait, the adapter incorrectly expected the optional completion
`result`. It now gets the exec ID from the asynchronous response's required
`resource_ids`, then observes completion. These failures remain in the retained
transcript; only the final complete round trip is counted.

Raw observations remain in `/var/lib/lamp/t21a/outside-evidence.json`.

### Subscription authorization remains pending

Fresh, separate official sign-in flows have been started with isolated Lamp
HOME/config directories. Codex uses `login --device-auth`; Claude uses
`setup-token` with private PTY capture and a private code-delivery socket.
Approval links and one-time codes are delivered only to the coordinator, not
recorded in this evidence. Returned authorization codes will be read only from
the owner-provided private file.

The M5's existing Claude Code and Codex credentials have not been copied or
used for these sign-ins. No browser profile or visible screen was accessed.
There is not yet a successful subscription model turn for either provider;
authorization and the two real turns remain outstanding.

Cleanup was requested for the completed API sessions, and their disposable
sandbox was deleted. The ticket's expired worker grant was renewed with the same one-hour
lifetime for that cleanup; no audit or authorization limit was changed.

## 3. T3 Code and Herdr

Both public consoles have now driven real, separate Lamp PTYs headlessly. No
third-party source was modified, no owner project was registered, and no owner
UI was opened. These are terminal-console observations, not claims of a model
turn inside a guest.

### T3 Code public ACP

The isolated T3 CLI `0.1.8` uses `/tmp/l21-consoles/state-t3`, with one provider
entry labelled `Lamp fleet fl_local`. Its only project is the disposable public
Hello-World clone. The provider runs `tools/session_acp.py` over the public Lamp
API, enrolled explicitly with `--shell --join-mode input --prompt-envelope t3`.
Ordinary adapter enrollment defaults to watch; shell execution and input
enrollment are separate explicit choices.

Two different T3 threads completed and received standalone output lines, not
only echoed commands:

| T3 thread | Observed terminal output | T3 result |
| --- | --- | --- |
| `mcp:561b7a25-36d7-4ce4-a7c7-de0579fe4ca4` | `T21_T3_PUBLIC_A` | `completed` |
| `mcp:1fd6521a-8748-46dc-8496-73393a063290` | `T21_T3_PUBLIC_B` | `completed` |

A follow-up initially failed because the console appends runtime instructions
to subsequent prompts. The explicit T3 terminal mode now extracts the public
user-request envelope and removes the console-owned trailing runtime/linking
blocks instead of executing them as shell commands.

The corrected real follow-up in thread
`mcp:2a7b1e99-4390-4680-8d5f-46b048a9e82e` returned:

```text
T21_T3_LIVE_FOLLOW_UP
LAMP_SHELL_6d0efa61e4d322c7c22243226db9ca52:0
```

The CLI reported `status=completed`, `error=null`. Raw observations are retained
under the isolated T3 directory as `terminal-a-observed.json`,
`terminal-b-observed.json`, and `terminal-final-follow-up.json`.

### Herdr public machine/pane API and stock SSH

Herdr `0.9.5` runs as a headless named session `t21-c704` on Ubuntu, with its
binary, configuration, and state isolated under
`/home/johan/lamp-t21-console-c704`. The M5 client uses
`/var/tmp/l21-herdr-c704`. Its saved machine is:

```text
1f2a6bb002e4de07be346162e0e05ac2  Lamp fleet fl_local
```

The normal Herdr machine API, snapshot, pane creation, literal input, and output
inspection are used. A ticket-only SSH environment shim selects the isolated
remote installation; it does not edit the owner's SSH configuration or install
anything into the owner's usual PATH.

The Lamp SSH edge uses a pinned disposable host CA, a short-lived session-only
API key, and the same public session endpoints used by other clients. Separate
Herdr panes attached to:

- `se_c8f455a176f380f93ab223fcd37d1077`: actual output `T21_HERDR_A`.
- `se_5d2ee34c9b96849aba9534398af6f68f`: actual output `T21_HERDR_B`.

After explicitly selecting the public clone, the first session also returned
`T21_HERDR_PUBLIC_REPO` and `Hello World!`. An earlier relative `cat README`
failed because that shell started in `/tmp`; it was not counted as a successful
repository read.

The reusable `tools/session_herdr.py` client then created workspace
`Lamp fleet fl_local`, with one separate watch tab per authorized selected
session. Re-running it reused the existing SSH watch process rather than
creating duplicate tabs. Explicit `--input --session <id>` created an input tab;
it is not the default and requires one selected session.

Typing `printf 'T21_HERDR_LIVE_FOLLOW_UP\n'` in input pane `w2:p3` produced the
standalone `T21_HERDR_LIVE_FOLLOW_UP` line in both the input pane and the separate
watch pane `w2:p1`. The Herdr public snapshot retained separate pane and terminal
IDs. No private Lamp worker interface or private Herdr TUI protocol was used.

## 4. Caller-placed sub-agents and git return

On 2026-11-11 UTC, the public session API ran a parent PTY on the M5 and
spawned its separately visible child on Ubuntu using the caller's explicit
`sandbox_mode=host:hs_linux_git` and an owner-enrolled public git-work recipe.
This demonstrates cross-host placement and git return; it is not an additional
claim of an LLM turn or guest placement.

- Parent: `se_9df7b6fb0f7cfd7a541179f0aee6864e`, `host:hs_m5`.
- Child: `se_687dc36467b3040dab7629ce74cfb934`, `host:hs_linux_git`.
- Both appeared independently in `GET /v1/sessions`; the child retained its
  `parent_session_id`.
- The parent read `Hello World!` and `uname -s` returned `Darwin`.
- Through its own input lease, the child wrote `lamp-agent-result.txt`,
  committed it, and printed `T21_CHILD_PATCH_READY` and `Linux`.
- Child commit: `99ce17e80c7787126194fce753409579ba87806b`.
- Public change set: `cs_2d74980f6af833baabae9ac8d66a8c5f`, selected path
  `lamp-agent-result.txt`, final state `applied`.

The parent then read the returned file from its own workspace:

```text
Work returned by caller-placed child
Apply child session work
T21_GIT_RETURN_APPLIED
Darwin
```

The first real spawn exposed a nil transfer-map panic: management mutations
clone their journal through JSON, and the new empty maps were omitted.
Transfer maps are now retained by serialization and initialized when reading
legacy state. `go vet ./internal/control ./cmd/lampd` and the Linux daemon
build passed. The controller was restarted before the successful run.
No tests were added. Both demo sessions were explicitly ended afterward.

Raw observations are retained at
`/var/lib/lamp/t21a/host-git-evidence.json` on Ubuntu. The public git-return
result was inspected directly; terminal escape sequences around the git pager
initially made an overly strict output assertion time out, but did not prevent
the file or commit from being applied.

## 5. Bidirectional handoff

Both fenced moves completed in the corrected real run below. The earlier
failed run remains recorded separately.

The first real queued-input-before-move run used native Claude session
`se_53bf7898998c623df51fb17d69f6330e`, continuation
`cb876e30-9589-4623-bcea-89c2822f42dc`, in sandbox
`sb_6e61baa2b84d024a03aceb45fc049c96`. Its initial model turn succeeded.
Input operation `op_b8ef6a227cbc44b71e19f9f69e3e9bf4` was acknowledged before
teleport operation `op_400394c3bd013327696acaa406d707a0` began.

The handoff did not complete: an in-flight source reply could overwrite the
newer `moving` state, after which the next request used the target epoch
against the source runner. The resource reported failure with acknowledged
pending input retained and no automatic restart. No collector failure was
recorded in that guest.

Replies and transport failures are now checked against their runner location
and effective epoch, and the moving state is read again after the RPC.
Terminal resources cannot be revived by a stale reply. Focused Go vet and the
Linux daemon build pass.
The failed observation and accepted-input receipt remain in
`/var/lib/lamp/t21a/handoff-epoch-race.json`. Only that disposable sandbox was
deleted afterward.

### Corrected real run: sandbox → M5 → sandbox

Session `se_80cf9ddfb22d0bc5a39257b29a2504ec` began in sandbox
`sb_e5e250084c52234dd4cf26813edb8105` at epoch 1, with native Claude
continuation `4c67f2b3-62ba-4bf5-ac85-6392ea0d0346`.

- The initial real guest turn printed `T21_CLAUDE_REAL_GUEST`.
- Input `op_a2a2d5a9f658d8662995c1c23cc784c3` was acknowledged immediately
  before move `op_9a8cbd2fd642ed5589995bc8fc1fc989`.
- The input completed as `T21_ACK_BEFORE_LAPTOP`, and the session became active
  on `hs_m5` at epoch 2. The original input lease was revoked; a stale writer
  request was rejected.
- A laptop input asking about the first conversation marker was acknowledged
  and delivered. Its model request failed: the broker recorded
  `UPSTREAM_UNAVAILABLE`, and Claude reported `ECONNRESET`. This was **not** a
  successful model response or an outside-driving demonstration.
- Return move `op_0050d26f3510b15a01a6ca95f3ba3474` made the same session active
  in the sandbox at epoch 3 with the same native continuation ID.
- The laptop input lease `le_a330b4c310069b57c4c81eba050c3f38` was revoked.
  Typing with that lease and epoch 2 returned `SCOPE_DENIED`.
- The actual laptop source runner recorded `fenced`, epoch 2, and no
  outstanding input. Its exported native archive contains the initial turn,
  the acknowledged pre-move input, and the acknowledged laptop input. All
  three were found in the same native conversation JSONL file; no input was
  replayed to establish this.

The exported archive SHA-256 is
`96a3080fe6a90b57517afc70c643763f1a03007fa45a96ff42f3083d9a57c1aa`.
The transfer verifies archive integrity before restoring and starts the
destination only after the source fence and output drain.

Raw public observations remain in `/var/lib/lamp/t21a/handoff-evidence.json`.
The inspected source archive remains in the disposable M5 runner directory
`/tmp/l21-m5-f/i/se_80cf9ddfb22d0bc5a39257b29a2504ec.e2/`.

Subsequent guest metadata/file helpers reached the unchanged 64-segment audit
bound and prevented the pending Codex setup. Short idle/stat/read/append
helpers now use the protected relay instead of allocating a new collector
each time. The already completed moves were not rerun.

## 6. T19: a session message wakes its suspended sandbox

On 2026-11-11 UTC, the public API resumed the existing PTY after a real
controller restart and Firecracker suspension:

- Sandbox `sb_e905f4e449bd74266406f686bc304a1d`.
- Session `se_86e3d0c97816835e3425efbe882c5c9c`, initially active in generation 1.
- The same PTY printed `T21_WAKE_BEFORE` and `Linux` before suspension.
- Only the ticket controller was restarted. The worker grant explicitly
  authorized generations 1–2; the session reattached with fresh observation.
- `:suspend` reached `suspended` at generation 1 with local durability.
- Before the message-triggered resume: zero live Firecracker processes and
  26,857 MiB available.
- A message submitted under the existing input lease invoked `Service.Wake`
  before dispatch. The sandbox returned to `ready` at generation 2.
- The same session retained location epoch 1 and produced:

```text
# T21_MESSAGE_WOKE_VM
Linux
```

The shell prompt prefixed the marker, so an initial newline-specific demo
assertion timed out. Direct public output inspection confirmed that the
message had run; no repeated run was required. The session was ended and the
ticket sandbox was deleted afterward.

Raw observations are retained in `/var/lib/lamp/t21a/wake-evidence.json` on
Ubuntu. This is a successful wake/PTY continuation observation, not a claim
that the remaining laptop/sandbox native handoff is complete.
