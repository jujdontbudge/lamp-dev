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

`tools/harness_catalogue_guest.py` runs as root in its own disposable root,
`/var/lib/lamp/t25`. Its guest seed is T21's verified seed plus the official
Linux `opencode` 1.19.5 and `droid` 1.8.0 binaries (droid's checksum matched
Factory's published sha256). The worker is T21's `lamp-supervisor-subscription`.
The broker uses a `CA:FALSE` serverAuth leaf on a ticket-only address allowed
by the network policy. The OpenRouter key stays on the M5 and reaches the
broker's credential reader only over an SSH-forwarded socket. Each run checked
that fewer than 2 Firecracker processes were live and at least 12 GiB was free
before allocating, and deleted its sandbox afterwards.
Raw: `evidence/t25-host-vm-host.json`.

**opencode, one native session `ses_e347c9207ffeyf7jBusF98I92p`:**

| Location | Epoch | Asked for | Answer |
| --- | --- | --- | --- |
| host `hs_lx` | 1 | (given `LAMP024CFF`) | `READY` |
| VM `sb_7f850f842b1dbe40608dba0f6eaeee73` | 2 | `LAMP024CFF` | `LAMP024CFF` (given `LAMPD3C119`) |
| host `hs_lx` | 3 | `LAMPD3C119` | `LAMPD3C119` |

**droid now completes inside the VM.** Raw: `evidence/t25-droid-vm.json`.
Earlier I suspected droid's BYOK mode needed Factory's servers. The guard
audit showed otherwise: the host network guard itself had stopped, and it
fails closed, so the VM had no network.

- Run 7 (`t25-run7`): `lamp-guard: No buffer space available (os error 105)`
  at 16:51:12, as droid's first in-VM turn started after opencode's turns.
  Each queued packet is copied up to 64 KiB into the default netlink receive
  buffer, so droid's large first request overflowed it. The guard treated
  ENOBUFS as fatal.
- Droid-only rerun: `audit storage deadline exceeded` two seconds after the
  sandbox started, three minutes before droid ran. This followed this script's
  20 GB seed copy. The 1 s durable-audit deadline is intended fail-closed
  behaviour and is unchanged.

Fixes: the guard's queue socket now reserves a 32 MiB receive buffer. A
reported overflow is treated as dropped packets (no allow verdict, still
fail-closed) instead of stopping the guard. The script also runs `sync`
before allocating. Both changes were applied together in this run, so the
run does not show which one mattered here.

With them, droid's session `dc29698d-09c3-4ac1-8959-95a9b491e1b2` ran
host → VM `sb_c873cee0fa624554ae71a964d830c2a1` → host. Inside the VM it
recalled `LAMPE20B48`. The guard stayed up to sandbox deletion. Its audit
shows the only destination was the broker (`10.219.25.1:17780`). Droid's BYOK
turn reached no Factory host, so no vendor-endpoint exception is needed for
this route. On the last hop back to the host, the model repeated
`LAMPE20B48` instead of the newer codeword it had been given. That is a model
miss on a continued session and is kept as observed.

Failed attempts are kept as `/var/lib/lamp/t25-attempt1..6` and
`t25-run7`. One hit an exec-body error. One was correctly refused while two
other tickets' sandboxes were live. One used the wrong sandbox host ID. One
stalled at upload offset 0 with a worker that did not match the seed.

## 4. Native replies stream through the broker

Previously the broker buffered native replies and allowed 40 s and 2 MiB per
call, which cut off long turns. The native routes now forward bytes as they
arrive, with a 90 s stall watchdog in place of the wall clock. Grant expiry
gates whether a call starts but no longer ends a running stream; a budget
deadline still does. Known credential encodings are scanned on the stream
with a held-back tail. Usage frames are kept and settled when the stream ends.
`max_completion_tokens` is now reserved under its own key; it used to be
refused.

`tools/broker_stream_demo.py` sent the same long streamed request through each
broker binary. A budget-accounting stub recorded what the broker reserved and
settled.

- Old broker (0584815), `evidence/t25-stream-old.json`: HTTP 502 after
  exactly 40.0 s, nothing delivered, settled with no usage. Two of three
  earlier runs failed the same way; the faster routes finished under 40 s.
- New broker, `evidence/t25-stream-new.json`, with `max_completion_tokens`:
  first byte at 10.5 s, whole at 43.9 s, `[DONE]` with
  `finish_reason=length`, 17,358 characters. It reserved 5,416 tokens and
  settled 4,138 actual tokens.

## 5. Restored native state is untrusted

A continuation archive can come from a sandbox, so its `lamp-native.json` is
untrusted. On load, the wrapper keeps only resume keys that match
`[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}`, so they can select a session but never
be a path or an option, plus the directory and turn count. Pi's fork source is
computed during the turn, must resolve inside pi's own sessions directory, and
is never persisted. A planted
`{"session_id":"--fork=/etc/hosts","fork_from":"~/.ssh/id_ed25519","forked_from_id":"planted"}`
produced a pi argv with neither, and a fresh session ID. The ignored fields
are reported on stderr.

## 6. Cursor CLI on its own API-key route (Ubuntu)

Cursor has no bring-your-own-endpoint route, so its API key stays on the
custody host and is passed to the official `cursor-agent` (2026.11.11-043ef28)
as `CURSOR_API_KEY`, re-read each turn. It never enters the session home or
the evidence. `tools/harness_catalogue_demo.py --move`: turn one answered
`READY`. The chat store (`config/chats/<md5 of cwd>/<chatId>`) was packed,
restored under a new directory and re-keyed, and the original was deleted.
`--resume` continued chat `b319aee7-7000-4c4e-b1d8-e2c1d246d648`, which
recalled `LAMP77BF30`. Raw: `evidence/t25-cursor-relocation.json`.

A first M5 run also passed, but the CLI tried to save the login to the macOS
keychain and printed a warning when that failed. No prompt appeared. Keychain
writes are not allowed on the owner's Macs, so the recorded run is on Ubuntu,
which has no keychain. The catalogue lists this as a limitation.

## 7. Devin on the owner's device login (Ubuntu)

The owner approved `devin auth login --force-manual-token-flow`, which ran in
an isolated home on the Ubuntu laptop that uses it. Lamp pasted the code from
the owner's file and never printed it. Devin 3000.12.43 reads its login only from
`$XDG_DATA_HOME/devin/credentials.toml`. Setting `WINDSURF_API_KEY` alone was
observed as `Not logged in`. The adapter therefore points `XDG_DATA_HOME` at
the login in place (it is never copied) and sets `DEVIN_SESSIONS_DIR` to the
session's own home, so the native store (`cli/sessions.db`) travels with the
continuation artifact. Print mode cannot show Devin's workspace-trust prompt,
so the adapter passes `--respect-workspace-trust false`.

`--move`: turn one answered `READY`. The session store was packed, restored
under a new directory, and the original was deleted. `--resume
prickle-textbook` recalled `LAMP557A9A` in the same native session. The
account email and organisation are redacted. Raw:
`evidence/t25-devin-relocation.json`.

## 8. Native sub-agents for opencode, copilot_cli, goose and droid (M5)

When a harness is enrolled with `native_subagents`, its own sub-agent feature
stays on. Each sub-agent is reported from the harness's own events as an
observed child session (section 2's mechanism, now for each harness). Without
the flag the feature is off and Lamp's spawn is used, as before. Raw:
`evidence/t25-native-subagents.json`.

**opencode through `lampd` (free model via the broker).** `tools/harness_catalogue_live.py --subagent-kind opencode --skip-teleport`:

- The parent session `se_3d688f6a5a6ae6378db6c9a9bf19170c` (native `ses_e33d7f977ffePTv5NHf9hYfKfg`) used opencode's `task` tool once. It answered `LAMP_SUBAGENT_DONE`.
- `GET /v1/sessions` listed the child `se_5969766a86322c7b7269af09f3e458e2` next to it, with `parent_session_id` set to the parent and labels `lamp.native_subagent=true` and `lamp.native_label="Run sub-agent with specific prompt"`.
- The child has its own stable native ID (`continuation_ref` = opencode child session `ses_e33d7d52effe6tBcNKzsrFye35`). It also has its own output (`<task id=…> … LAMP_SUBAGENT_DONE`), and it ended as `idle`.
- Descendant charging: opencode's own export of the child records `parentID` = the parent's native session. Its model call went through the `lamp` provider (Lamp's broker) on `openrouter/free`, 5,293 tokens. The broker audit shows all four model calls of that turn, the parent's and the child's, under the parent's one grant (`sb_t25_live`). The child is charged to the parent's budget.

**copilot_cli, goose and droid through the wrapper** (`tools/harness_catalogue_demo.py --subagents`, BYOK or provider routes via the broker):

| Harness | Native feature | Stable child ID | Own output | Observed |
| --- | --- | --- | --- | --- |
| copilot_cli 1.0.109 | `task` sub-agents | `subagent.started` `toolCallId` | `tool.execution_complete` result, plus `totalTokens` per sub-agent | Two children, each returned `LAMP_SUBAGENT_DONE` (14,610 and 12,305 tokens). |
| goose 1.59.0 | `delegate` | `_meta.subagent_session_id` (`20261111_2`) | Goose's own run and end notices only. In `run` mode goose does not stream a separate sub-agent transcript. | Child ran and `completed`. |
| droid 1.6.0 | `Task` / `SpawnAgent` | tool call ID | the tool result | Each call was reported as a child. Each ended with droid's error, because on the BYOK route without a Factory sign-in droid refuses to start the sub-agent session. Its daemon log gives `reason: missing_authed_user_or_org`. Native droid sub-agents need a Factory account. That is stated in the catalogue entry as a compatibility gap. |

Codex's `multi_agent` route is built (section 2's reporting code) but was not
run here.

## Routes not exercised, and why

The owner dropped these signed-in runs on 2026-11-11. Their adapters and
catalogue entries stay as built.

| Harness or route | Status |
| --- | --- |
| amp | Not run. The adapter continues by thread ID. An earlier attempt used a token file holding an SSH key instead of an Amp token, and Amp rejected it (`Invalid or missing API key`). |
| gemini_cli | Not run. |
| copilot_cli subscription | Not run. Four device codes were issued on Ubuntu. The owner approved one, but the CLI's poll never picked it up before it expired, and its log recorded nothing about the poll even at `--log-level all`. The cause was not found. Copilot's official BYOK route is shown in sections 1 and 2. |
| droid Factory models | Not needed: droid's BYOK route runs on the host and inside a VM (section 3) without a Factory login. |

Claude Code and Codex subscription sign-ins stay T21's custody path. They
were not rerun here.

## Goal check

| Ticket clause | Evidence |
| --- | --- |
| Ship the 11 §9.2 integrations | All 11 have a catalogue entry and an adapter, and `GET /v1/harnesses` lists all 11 (section 2). Nine ran on a real route: claude_code, codex, opencode, pi, goose, droid and copilot_cli through the broker (sections 1–3), cursor_cli on its API key (section 6) and devin on the owner's device login (section 7). amp and gemini_cli are built but were not run because the owner dropped them. |
| Each uses its official permitted sign-in or API route | Each catalogue entry cites dated vendor permission sources. The runs used only documented routes: vendor BYOK or provider settings, Cursor's `CURSOR_API_KEY`, and Devin's own device login used in place. |
| Sessions and native sub-agents visible separately | Native sub-agents stay on when enrolled. Each is reported as an observed child session with a stable native ID, its own output, and charging to the parent's grant. Shown through `lampd` for claude_code (section 2) and opencode (section 8), and through the wrapper for copilot_cli and goose (section 8). droid reports its sub-agent calls as children but cannot start them without a Factory sign-in, a stated gap. codex's route is built but not shown. |
| Continuing work moves between host and sandbox through each harness's mechanism | Nine harnesses continued their native session after a move with the original deleted (sections 1, 6, 7). Seven continued across a `lampd` teleport between hosts (section 2). opencode and droid went host → Firecracker VM → host (section 3). The VM path was shown for those two only. |
| Vendor limits described honestly; a new prompt with a transcript is not continuation | Each run resumed the harness's own native session ID. Pi's relocation is reported as `forked_from` and is not presented as the same session. Amp threads and Devin cloud sessions are stated as not moved. Model misses and failed attempts are kept. |

## Limitations stated honestly

- Free-model answers vary. Each recall is one observation, not a quota. The
  failed observations above are kept.
- goose's ACP-delegated mode inherits the delegate's continuation; goose
  itself does not resume it.
- Amp threads and Devin cloud sessions live on the vendor's servers; Lamp
  does not move them.
- Pi relocation creates a new linked native session (`forked_from`).
- No tests were added.
