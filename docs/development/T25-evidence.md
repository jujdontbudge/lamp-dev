# T25: harness catalogue evidence

This records what was actually observed on 2026-11-11. It is stacked on T21
(PR #25) and reuses its sessions, teleport and custody paths. Every run used
disposable homes and the public `octocat/Hello-World` repository. Model
traffic went through Lamp's broker to OpenRouter's zero-price route
(`openrouter/free`, max price forced to 0). The provider key never left the
broker; harnesses received only short-lived scoped Lamp grants.

## What was built

- `internal/control/harness_catalogue.json`: all 11 harnesses from
  architecture §9.2. Each entry records its official routes per auth mode,
  dated permission sources (every URL checked to return 200 on 2026-11-11),
  vendor endpoints, trust variables, continuation mechanism, native sub-agent
  handling and actual vendor limitations.
- `GET /v1/harnesses` returns enrolled harnesses with the exact binary
  `sha256` digest and catalogue data, plus every catalogue harness the owner
  has not enrolled yet (`state=disabled`), so people can see which harness
  they can bring and on which route.
- `internal/control/native_harness.py`: one adapter per harness kind
  (`claude`, `codex`, `opencode`, `pi`, `goose`, `copilot`, `cursor`,
  `gemini`, `droid`, `devin`, `amp`). Each runs the unmodified vendor binary
  headlessly in an isolated home, re-reads credentials each turn, and
  continues with the harness's own resume key.
- Broker: a native OpenAI chat-completions route
  (`/v1/models/<connection>/native/chat/completions`, operation
  `chat_completions`) with streaming and tool shapes, usage accounting from
  streamed chunks, and logging of rejected field *names* (never values).
- Continuation artifacts (`session_artifact.py`) now carry each harness's own
  session store, never credentials or caches. Stores keyed by working
  directory are re-keyed for the new workspace. Two harnesses need their own
  relocation mechanism:
  - opencode: its official `export`/`import` (it records the directory).
  - pi: its official `--fork`, which copies the history into a new session
    linked by `parentSession`. Lamp reports this as `forked_from`; it is not
    presented as the same native session.
- Native sub-agents: a harness enrolled with `native_subagents: true` uses
  its own sub-agent feature, and each sub-agent is recorded as a separate
  observed child session (`parent_session_id`, label
  `lamp.native_subagent=true`, its own output). Observed children accept no
  input lease. Without that flag, native sub-agents are disabled and Lamp's
  spawn is used, as in T21.

## 1. Seven harnesses continue across a move (M5)

`tools/harness_catalogue_demo.py --move`: turn one gives a fresh codeword.
Lamp's continuation artifact code packs the session. It is restored into a
different directory, and **the original workspace and home are deleted**.
Turn two asks for the codeword. Raw: `evidence/t25-relocation.json`,
`evidence/t25-relocation-pi-fork.json`.

| Harness | Version | Route | Recalled after move | Native continuation |
| --- | --- | --- | --- | --- |
| claude_code | 2.1.322 | Anthropic Messages via broker | yes | same session ID |
| codex | 0.167.0 | Responses via broker | yes | same thread ID |
| opencode | 1.19.5 | chat via broker | yes | same ID, export/import |
| droid | 1.6.0 | Factory BYOK custom model via broker, no Factory login | yes | same session ID |
| copilot_cli | 1.0.109 | Official BYOK (`COPILOT_PROVIDER_BASE_URL`, offline) via broker | yes | same session ID |
| goose | 1.59.0 | Direct OpenAI provider mode via broker | yes | same named session |
| pi | 1.5.0 | chat via broker | yes | `--fork`, new ID linked to the old one |

The strict run caught pi refusing to resume once its recorded directory was
gone. That failure is retained in `t25-relocation.json`, and the fork route
was then added and observed separately.

## 2. Through lampd's public API (M5)

`tools/harness_catalogue_live.py` started a real `lampd` and broker with two
enrolled host locations, `hs_a` and `hs_b`, each with its own private root.
Raw: `evidence/t25-lampd-live.json`, `evidence/t25-lampd-live-pi.json`.

- `GET /v1/harnesses` returned 12 items: 8 enrolled harnesses with
  binary digests (claude twice, once with native sub-agents) and the 4
  not-yet-enrolled catalogue entries (`amp`, `cursor_cli`, `devin`,
  `gemini_cli`) as `disabled`.
- For each of the 7 harnesses: session created on `hs_a`, codeword turn,
  `POST /v1/sessions/{id}:teleport` to `hs_b` (epoch 1 → 2), then a recall
  turn. All 7 recalled their codeword. In the full run pi's free model
  answered `READY`, even though the forked session file contained the codeword
  turn and the model's own reasoning named it. That miss is kept in
  `t25-lampd-live.json`. The pi-only rerun recalled `LAMP300D54`, with
  `forked_from` = the original native ID.
- Native sub-agents: Claude Code on `hr_claude_subagents` launched its own
  sub-agents. `GET /v1/sessions` listed them as separate child sessions of
  the parent, labelled `lamp.native_subagent=true`, each with its own output
  (one ended with `LAMP_SUBAGENT_DONE`; one was the model's malformed call
  that the CLI rejected). Both ended when the parent ended.

## 3. Host → Firecracker sandbox → host (Ubuntu)

See section 4 below for the result of `tools/harness_catalogue_guest.py`.
It runs in its own disposable root, `/var/lib/lamp/t25`. Its guest seed is
T21's verified seed plus the official Linux `opencode` 1.19.5 and `droid`
1.8.0 binaries (checksum verified against Factory's published sha256). The
broker uses a `CA:FALSE` serverAuth leaf on a ticket-only address allowed by
the network policy. The OpenRouter key stays on the M5 and reaches the
broker's credential reader only over an SSH-forwarded socket. The run checked
`pgrep -c firecracker` < 2 and ≥ 12 GiB free before allocating, and deleted
its sandbox afterwards. Earlier attempts are kept as
`/var/lib/lamp/t25-attempt*`: one hit an exec-body error, one was correctly
refused while two other tickets' sandboxes were live, and one used the wrong
sandbox host ID.

## Routes not yet exercised, and why

These adapters are built and their argv was accepted by the real binaries.
Each needs a vendor account sign-in that only the owner can complete:

| Harness | What is needed |
| --- | --- |
| cursor_cli | A Cursor API key, or `agent login` approval. Cursor has no bring-your-own-endpoint route, so the broker cannot hold this credential. |
| amp | An Amp access token (ampcode.com/settings), or approval of its device login. |
| gemini_cli | An AI Studio `GEMINI_API_KEY`. Google rejects automated sign-in, and the broker does not speak the Gemini wire format. |
| devin | `devin auth login --force-manual-token-flow` approval. Cloud (`--cloud`) sessions are not relocated or presented as local. |
| copilot_cli subscription, droid Factory models | Copilot device-login approval, or a Factory API key. The BYOK routes above already work without them. |

Claude Code and Codex subscription sign-ins stay T21's custody path. They
were not rerun here.

## Limitations stated honestly

- Free-model answers vary. Each recall is one observation, not a quota. The
  failed observations above are kept.
- goose's ACP-delegated mode inherits the delegate's continuation; goose
  itself does not resume it.
- Amp threads and Devin cloud sessions live on the vendor's servers; Lamp
  does not move them.
- Pi relocation creates a new linked native session (`forked_from`).
- No tests were added.
