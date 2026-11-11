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

This does not yet demonstrate inside/outside sandbox operation or subscription
authorization. T9's retained evidence records an incomplete subscription login;
its merged custody implementation is not evidence of an authorized account.

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

No completed sub-agent/git-return demonstration is recorded yet.

## 5. Bidirectional handoff

No completed laptop/sandbox handoff demonstration is recorded yet.
