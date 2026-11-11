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

No completed console demonstration is recorded yet.

## 4. Caller-placed sub-agents and git return

No completed sub-agent/git-return demonstration is recorded yet.

## 5. Bidirectional handoff

No completed laptop/sandbox handoff demonstration is recorded yet.
