"""Runs one enrolled official harness binary for a Lamp session.

Each adapter keeps the vendor's unmodified binary and its built-in sign-in or
API route. Continuation is always the harness's own resume mechanism keyed by
its native session identifier; a fresh prompt with a pasted transcript is
never presented as a continued session. Native sub-agent features are disabled
here because Lamp spawns sub-agents as separately visible sessions.
"""

import json
import re
import os
from pathlib import Path
import subprocess
import sys
import uuid

SECRET_NAMES = ["LAMP_MODEL_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY", "COPILOT_PROVIDER_API_KEY", "COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "CURSOR_API_KEY", "AMP_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "FACTORY_API_KEY", "WINDSURF_API_KEY", "DEVIN_API_KEY"]
LAMP_TOOLS = ["exec", "spawn", "git_return"]


def refuse_vendor_credential(kind, how):
    """A real vendor credential must never be reachable by the agent. Only
    endpoint-scoped broker grants may sit in a harness environment."""
    raise ValueError(kind + " accepts its vendor credential only " + how + ", which its own tools can reach; Lamp does not offer this route")


def emit(value):
    print("\x1eLAMP " + json.dumps(value, separators=(",", ":")), flush=True)


def display(text, environment):
    for name in SECRET_NAMES:
        if value := environment.get(name):
            text = text.replace(value, "[REDACTED]")
    print(text, flush=True)


def atomic(path, value):
    temporary = path.with_suffix(".next")
    with temporary.open("w") as stream:
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def secret(config):
    return Path(config["credential_file"]).read_text().strip()


def write_private(path, value):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    path.write_text(value if isinstance(value, str) else json.dumps(value))
    path.chmod(0o600)


SUBAGENTS = set()


def subagent(native_id, status, label="", text=""):
    """Report a native sub-agent so Lamp can show it as its own child session."""
    SUBAGENTS.add(native_id)
    emit({"type": "native_subagent", "native_id": native_id, "status": status, "label": label[:200], "text": text[-4000:]})


def text_of(content):
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return "".join(part.get("text", "") for part in content if isinstance(part, dict) and part.get("type") in (None, "text", "output_text"))
    return ""


# Each adapter: prepare(config, home, environment) once; argv(config, state,
# prompt) per turn returning (argv, stdin); event(config, state, event)
# returning (display_text, finished_ok_or_None).

class Claude:
    homes = ["claude"]

    def prepare(self, c, home, env):
        env.update({"DISABLE_AUTOUPDATER": "1", "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CONFIG_DIR": str(home / "claude")})
        if c["auth"] == "api_key":
            token = secret(c)
            env.update({"LAMP_MODEL_TOKEN": token, "ANTHROPIC_AUTH_TOKEN": token, "ANTHROPIC_API_KEY": "", "ANTHROPIC_BASE_URL": c["endpoint"]})
        # A subscription token is handed over a one-shot pipe in main(), never
        # through the environment.

    def argv(self, c, state, prompt):
        argv = [c["binary"], "-p", "--output-format", "stream-json", "--verbose", "--setting-sources", "", "--permission-mode", "dontAsk", "--no-chrome"]
        if not c.get("native_subagents"):
            argv += ["--disallowedTools", "Agent,Task"]
        if c.get("agent_mode") == "outside" or c["auth"] == "subscription":
            # With a subscription credential the agent gets no host tools;
            # its commands run only through the Lamp exec bridge.
            argv += ["--tools", ""]
        if c.get("model"):
            argv += ["--model", c["model"]]
        if state.get("session_id"):
            argv += ["--resume", state["session_id"]]
        if c.get("mcp_config"):
            argv += ["--strict-mcp-config", "--mcp-config", c["mcp_config"], "--allowedTools", ",".join("mcp__lamp__" + t for t in LAMP_TOOLS)]
        return argv, prompt

    def event(self, c, state, e):
        if e.get("session_id"):
            state["session_id"] = e["session_id"]
        parent = e.get("parent_tool_use_id")
        content = e.get("message", {}).get("content")
        if parent:
            # Native sub-agent traffic: shown in its own child session.
            if e.get("type") == "assistant" and (text := text_of(content)):
                subagent(parent, "working", text=text)
            return "", None
        for part in content if isinstance(content, list) else []:
            if part.get("type") == "tool_use" and part.get("name") in ("Task", "Agent"):
                subagent(part["id"], "working", label=part.get("input", {}).get("description", "sub-agent"))
            if part.get("type") == "tool_result" and part.get("tool_use_id") in SUBAGENTS:
                subagent(part["tool_use_id"], "completed", text=text_of(part.get("content")))
        if e.get("type") == "assistant":
            return text_of(content), None
        if e.get("type") == "result":
            return "", not e.get("is_error", True)
        return "", None


class Codex:
    homes = ["codex"]

    def prepare(self, c, home, env):
        env["CODEX_HOME"] = str(home / "codex")
        if c["auth"] == "api_key":
            env["LAMP_MODEL_TOKEN"] = secret(c)
        elif c["auth"] == "subscription" and Path(c["credential_file"]).resolve() != (home / "codex/auth.json").resolve():
            raise ValueError("Codex subscription credentials must remain in the enrolled native custody home")

    def argv(self, c, state, prompt):
        argv = [c["binary"]] + ([] if c.get("native_subagents") else ["--disable", "multi_agent"]) + ["-c", 'approval_policy="never"', "-c", 'web_search="disabled"']
        if c.get("agent_mode") == "outside" or c["auth"] == "subscription":
            argv += ["--disable", "shell_tool", "--disable", "unified_exec"]
        if mcp := c.get("mcp"):
            argv += ["-c", "mcp_servers.lamp={command=" + json.dumps(mcp["command"]) + ",args=" + json.dumps(mcp["args"]) + ',enabled_tools=["exec","spawn","git_return"],default_tools_approval_mode="approve",tool_timeout_sec=180}']
        if c["auth"] == "api_key":
            argv += ["-c", 'model_provider="lamp"', "-c", 'model_providers.lamp={name="Lamp broker",base_url=' + json.dumps(c["endpoint"]) + ',env_key="LAMP_MODEL_TOKEN",wire_api="responses"}']
        if c.get("model"):
            argv += ["-m", c["model"]]
        argv += ["exec"]
        if state.get("session_id"):
            argv += ["resume", state["session_id"]]
        else:
            argv += ["--sandbox", "workspace-write", "--skip-git-repo-check"]
        return argv + ["--json", "-"], prompt

    def event(self, c, state, e):
        if e.get("type") == "thread.started":
            state["session_id"] = e["thread_id"]
        item = e.get("item", {})
        if item.get("type") == "collab_tool_call":
            for thread in item.get("receiver_thread_ids", []):
                subagent(thread, "completed" if e.get("type") == "item.completed" and item.get("tool") == "close_agent" else "working", label=item.get("prompt", "") or item.get("tool", ""))
        if e.get("type") == "item.completed" and item.get("type") == "agent_message":
            return item["text"], None
        if e.get("type") == "turn.completed":
            return "", True
        if e.get("type") == "turn.failed":
            return str(e.get("error", "")), False
        return "", None


class OpenCode:
    homes = ["config", "data", "state", "cache"]

    def prepare(self, c, home, env):
        env.update({"XDG_CONFIG_HOME": str(home / "config"), "XDG_DATA_HOME": str(home / "data"), "XDG_STATE_HOME": str(home / "state"), "XDG_CACHE_HOME": str(home / "cache"), "OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_SHARE": "1", "OPENCODE_DISABLE_LSP_DOWNLOAD": "1"})
        config = {"$schema": "https://opencode.ai/config.json", "autoupdate": False, "share": "disabled", "permission": {"edit": "allow", "bash": "allow", "webfetch": "deny"}}
        if not c.get("native_subagents"):
            config["agent"] = {"general": {"disable": True}}
        if c["auth"] == "api_key":
            env["LAMP_MODEL_TOKEN"] = secret(c)
            config["provider"] = {"lamp": {"npm": "@ai-sdk/openai-compatible", "name": "Lamp broker", "options": {"baseURL": c["endpoint"], "apiKey": "{env:LAMP_MODEL_TOKEN}"}, "models": {c["model"]: {"name": c["model"]}}}}
            config["enabled_providers"] = ["lamp"]
        if mcp := c.get("mcp"):
            config["mcp"] = {"lamp": {"type": "local", "command": [mcp["command"], *mcp["args"]], "enabled": True}}
        if c.get("agent_mode") == "outside":
            config["tools"] = {"bash": False, "edit": False, "write": False, "patch": False}
        env["OPENCODE_CONFIG_CONTENT"] = json.dumps(config)

    def before(self, c, state, env):
        # opencode records the session's directory; after a move, re-import
        # its own export at the new workspace (opencode export/import).
        exported = Path(env["HOME"]) / "opencode-session.json"
        if state.get("session_id") and state.get("directory") != os.getcwd() and exported.exists():
            data = json.loads(exported.read_text())
            data["info"]["directory"] = os.getcwd()
            moved = exported.with_suffix(".import.json")
            write_private(moved, data)
            subprocess.run([c["binary"], "import", str(moved)], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=120, check=True)
        state["directory"] = os.getcwd()

    def after(self, c, state, env):
        if state.get("session_id"):
            out = subprocess.run([c["binary"], "export", state["session_id"]], env=env, capture_output=True, text=True, timeout=120)
            if out.returncode == 0 and out.stdout.strip():
                write_private(Path(env["HOME"]) / "opencode-session.json", out.stdout)

    def argv(self, c, state, prompt):
        argv = [c["binary"], "run", "--format", "json"]
        if c.get("model"):
            argv += ["-m", ("lamp/" if c["auth"] == "api_key" else "") + c["model"]]
        if state.get("session_id"):
            argv += ["-s", state["session_id"]]
        return argv + ["--", prompt], None

    def event(self, c, state, e):
        part = e.get("part") or {}
        if part.get("type") == "tool" and part.get("tool") == "task":
            # opencode's own task tool runs its sub-agent as a child session
            # (metadata.sessionId, parentSessionId) on the parent's model route.
            st = part.get("state") or {}
            child = (st.get("metadata") or {}).get("sessionId") or part.get("callID")
            if child:
                done = st.get("status") in ("completed", "error")
                subagent(child, "completed" if done else "working", label=(st.get("input") or {}).get("description", "sub-agent"), text=str(st.get("output") or "") if done else "")
        if e.get("sessionID"):
            state["session_id"] = e["sessionID"]
        if part.get("sessionID"):
            state["session_id"] = part["sessionID"]
        if e.get("type") == "text":
            return part.get("text", ""), None
        if e.get("type") == "step_finish" and part.get("reason") not in ("tool-calls", "tool_calls"):
            return "", True
        if e.get("type") == "error":
            return json.dumps(e.get("error", "")), False
        return "", None


class Pi:
    homes = ["pi"]

    def prepare(self, c, home, env):
        agent = home / "pi"
        env["PI_CODING_AGENT_DIR"] = str(agent)
        env["PI_SKIP_VERSION_CHECK"] = "1"
        if c["auth"] == "api_key":
            env["LAMP_MODEL_TOKEN"] = secret(c)
            write_private(agent / "models.json", {"providers": {"lamp": {"baseUrl": c["endpoint"], "api": "openai-completions", "apiKey": "$LAMP_MODEL_TOKEN", "models": [{"id": c["model"], "name": c["model"], "reasoning": False, "input": ["text"], "contextWindow": 128000, "maxTokens": 4096}]}}})

    def before(self, c, state, env):
        # Pi records the session's working directory. After a move it reopens
        # the session only through its own --fork, which copies the history
        # into a new session (new ID, parentSession link) at the new directory.
        here = os.getcwd()
        if state.get("session_id") and state.get("directory") not in (None, here):
            sessions = (Path(env["PI_CODING_AGENT_DIR"]) / "sessions").resolve()
            found = [f for f in sessions.glob("*/*_" + state["session_id"] + ".jsonl") if not f.is_symlink() and f.resolve().is_relative_to(sessions)]
            if found:
                # Held only for this turn, never persisted: restored state can
                # never name the file pi forks.
                self.fork_from = str(found[0].resolve())
                state["forked_from_id"] = state["session_id"]
                state["session_id"] = str(uuid.uuid4())
        state["directory"] = here

    def argv(self, c, state, prompt):
        # Pi accepts a caller-chosen session ID, so the continuation key exists
        # before the first turn and the same native session is reopened later.
        state.setdefault("session_id", str(uuid.uuid4()))
        argv = [c["binary"], "-p", "--mode", "json", "--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--session-id", state["session_id"]]
        if fork := getattr(self, "fork_from", None):
            self.fork_from = None
            argv += ["--fork", fork]
        if c["auth"] == "api_key":
            argv += ["--provider", "lamp"]
        if c.get("model"):
            argv += ["--model", c["model"]]
        if c.get("agent_mode") == "outside":
            argv += ["--no-builtin-tools"]
        return argv + [prompt], None

    def event(self, c, state, e):
        kind = e.get("type")
        if kind == "message_end" and e.get("message", {}).get("role") == "assistant":
            m = e["message"]
            if m.get("stopReason") == "error":
                return m.get("errorMessage", "pi model error"), False
            return text_of(m.get("content")), None
        if kind == "agent_end":
            return "", True
        return "", None


class Goose:
    homes = ["config", "data", "state"]
    deltas = True

    def prepare(self, c, home, env):
        env.update({"XDG_CONFIG_HOME": str(home / "config"), "XDG_DATA_HOME": str(home / "data"), "XDG_STATE_HOME": str(home / "state"), "GOOSE_DISABLE_KEYRING": "1", "GOOSE_TELEMETRY_ENABLED": "false", "GOOSE_MODE": "auto"})
        if c["auth"] == "api_key":
            # Goose's direct provider mode, pointed at Lamp's broker route.
            base, _, path = c["endpoint"].partition("/v1/models/")
            env.update({"GOOSE_PROVIDER": "openai", "GOOSE_MODEL": c["model"], "OPENAI_HOST": base, "OPENAI_BASE_PATH": "v1/models/" + path + "/chat/completions", "OPENAI_API_KEY": secret(c)})
        write_private(home / "config/goose/config.yaml", "GOOSE_TELEMETRY_ENABLED: false\nextensions: {}\n")

    def argv(self, c, state, prompt):
        name = state.setdefault("session_name", "lamp-" + c.get("session_id", uuid.uuid4().hex))
        argv = [c["binary"], "run", "--output-format", "stream-json", "--name", name, "--max-turns", "20", "-t", prompt]
        if state.get("turns"):
            argv.insert(2, "--resume")
        if c.get("agent_mode") != "outside":
            argv += ["--with-builtin", "developer"]
        if mcp := c.get("mcp"):
            argv += ["--with-extension", " ".join([mcp["command"], *mcp["args"]])]
        return argv, None

    def event(self, c, state, e):
        if e.get("session_id"):
            state["session_id"] = e["session_id"]
        kind = e.get("type")
        if kind == "message":
            m = e.get("message", {})
            for part in m.get("content") or []:
                # Goose's own delegate tool names its subagent session in
                # _meta.subagent_session_id; it then reports the subagent's end.
                if part.get("type") == "toolResponse":
                    meta = ((part.get("toolResult") or {}).get("value") or {}).get("_meta") or {}
                    if isinstance(meta.get("subagent_session_id"), str):
                        subagent(meta["subagent_session_id"], "working", label="delegate")
                elif part.get("type") == "systemNotification":
                    found = re.match(r"(Running )?[Ss]ubagent (\S+?)(:| \(| (completed|failed|finished))(.*)", part.get("msg", ""), re.S)
                    if found and found.group(2) in SUBAGENTS:
                        subagent(found.group(2), "working" if found.group(1) else "completed", text=part.get("msg", ""))
            if m.get("role") == "assistant":
                return text_of(m.get("content")), None
        if kind == "complete":
            return "", True
        if kind == "error":
            return str(e.get("error", "")), False
        return "", None


class Copilot:
    homes = ["copilot"]

    def prepare(self, c, home, env):
        env.update({"COPILOT_HOME": str(home / "copilot"), "COPILOT_AUTO_UPDATE": "false", "COPILOT_DISABLE_LOGIN_SHELL_ENV": "1"})
        if c["auth"] == "api_key":
            # Official BYOK provider route; GitHub entitlement is not used.
            env.update({"COPILOT_PROVIDER_BASE_URL": c["endpoint"], "COPILOT_PROVIDER_TYPE": "openai", "COPILOT_PROVIDER_API_KEY": secret(c), "COPILOT_MODEL": c["model"], "COPILOT_OFFLINE": "true"})
        elif c["auth"] == "subscription":
            refuse_vendor_credential("copilot_cli", "as a GitHub token in its environment or its own login file")

    def argv(self, c, state, prompt):
        argv = [c["binary"], "-p", prompt, "--output-format", "json", "--allow-all-tools", "--no-auto-update", "--no-ask-user", "--disable-builtin-mcps"] + ([] if c.get("native_subagents") else ["--deny-tool", "task"])
        if state.get("session_id"):
            argv += ["--resume", state["session_id"]]
        if c.get("model") and c["auth"] != "api_key":
            argv += ["--model", c["model"]]
        if c.get("mcp_config"):
            argv += ["--additional-mcp-config", "@" + c["mcp_config"]]
        return argv, None

    def event(self, c, state, e):
        for key in ("sessionId", "session_id"):
            if isinstance(e.get(key), str):
                state["session_id"] = e[key]
        data = e.get("data") or {}
        if isinstance(data.get("sessionId"), str):
            state["session_id"] = data["sessionId"]
        kind = e.get("type", "")
        # Copilot's own task sub-agents: subagent.started/completed/failed
        # keyed by the spawning toolCallId, with the sub-agent's token total;
        # its result arrives as that tool call's tool.execution_complete.
        call = data.get("toolCallId")
        if isinstance(call, str) and call:
            if kind == "subagent.started":
                subagent(call, "working", label=data.get("agentDisplayName") or data.get("agentName") or "sub-agent")
            elif kind in ("subagent.completed", "subagent.failed"):
                note = "sub-agent " + ("failed: " + str(data.get("error", "")) if kind == "subagent.failed" else "completed") + "; total_tokens=" + str(data.get("totalTokens", "unknown"))
                subagent(call, "completed", text=note)
            elif kind == "tool.execution_complete" and call in SUBAGENTS:
                result = data.get("result") or {}
                subagent(call, "working", text=str(result.get("content") or data.get("error") or ""))
        if kind == "assistant.message":
            return data.get("content", ""), None
        if kind in ("result", "session.idle", "assistant.turn_end"):
            return "", e.get("exitCode", 0) == 0 if kind == "result" else None
        if kind == "session.error":
            return data.get("message", "copilot error"), False
        return "", None


class StreamJSON:
    """cursor-agent, gemini, droid and devin share Claude-style stream-json."""
    homes = ["config"]

    def event(self, c, state, e):
        for key in ("session_id", "sessionId", "chatId", "chat_id"):
            if isinstance(e.get(key), str) and e[key]:
                state["session_id"] = e[key]
        kind = e.get("type")
        if kind == "assistant" or kind == "message" and e.get("role") == "assistant":
            content = e.get("message", {}).get("content") if "message" in e else e.get("content", e.get("text"))
            return text_of(content), None
        if kind == "completion":
            return "", True
        if kind == "result":
            ok = not e.get("is_error", False) and e.get("subtype", "success") == "success" and e.get("status", "success") == "success"
            return "", ok
        if kind == "error":
            return str(e.get("message", e.get("error", ""))), False
        return "", None


class Cursor(StreamJSON):
    def prepare(self, c, home, env):
        env["CURSOR_CONFIG_DIR"] = str(home / "config")
        if c["auth"] == "api_key":
            refuse_vendor_credential("cursor_cli", "through CURSOR_API_KEY or --api-key")

    def argv(self, c, state, prompt):
        argv = [c["binary"], "--print", "--output-format", "stream-json", "--trust", "--force"]
        if state.get("session_id"):
            argv += ["--resume", state["session_id"]]
        if c.get("model"):
            argv += ["--model", c["model"]]
        return argv + [prompt], None


class Gemini(StreamJSON):
    def prepare(self, c, home, env):
        env["GEMINI_CLI_HOME"] = str(home / "config")
        if c["auth"] == "api_key":
            refuse_vendor_credential("gemini_cli", "through GEMINI_API_KEY or its own login file")
        write_private(home / "config/.gemini/settings.json", {"security": {"auth": {"selectedType": "gemini-api-key" if c["auth"] == "api_key" else "oauth-personal"}}, "general": {"disableAutoUpdate": True}, "privacy": {"usageStatisticsEnabled": False}, "experimental": {"enableAgents": False}})

    def argv(self, c, state, prompt):
        argv = [c["binary"], "--output-format", "stream-json", "--approval-mode", "yolo", "--skip-trust"]
        if state.get("session_id"):
            argv += ["--resume", state["session_id"]]
        if c.get("model"):
            argv += ["-m", c["model"]]
        return argv + ["-p", prompt], None


class Droid(StreamJSON):
    def prepare(self, c, home, env):
        if c.get("endpoint"):
            # Factory's documented BYOK custom model, pointed at Lamp's broker.
            write_private(home / ".factory/settings.json", {"customModels": [{"model": c["model"], "displayName": "Lamp", "baseUrl": c["endpoint"], "apiKey": secret(c), "provider": "generic-chat-completion-api", "maxOutputTokens": 4096}], "enableTelemetry": False})
        elif c["auth"] in ("api_key", "subscription"):
            refuse_vendor_credential("droid", "through FACTORY_API_KEY or its own login file")

    def argv(self, c, state, prompt):
        argv = [c["binary"], "exec", "-o", "stream-json", "--auto", "high", "--disable-builtin-skills"] + ([] if c.get("native_subagents") else ["--remove-tools", "Task,SpawnAgent"])
        if state.get("session_id"):
            argv += ["-s", state["session_id"]]
        if c.get("model"):
            argv += ["-m", ("custom:" if c.get("endpoint") else "") + c["model"]]
        return argv + [prompt], None

    def event(self, c, state, e):
        # Droid's own Task/SpawnAgent tools: the tool call ID names the
        # sub-agent; its tool_result carries the sub-agent's report or error.
        kind = e.get("type")
        if kind == "tool_call" and e.get("toolName") in ("Task", "SpawnAgent") and e.get("id"):
            subagent(e["id"], "working", label=(e.get("parameters") or {}).get("description", e["toolName"]))
        elif kind == "tool_result" and e.get("id") in SUBAGENTS:
            error = e.get("error") or {}
            subagent(e["id"], "completed", text=("error: " + str(error.get("message", ""))) if e.get("isError") else str(e.get("value", e.get("content", ""))))
        return super().event(c, state, e)


class Devin:
    """Devin's local CLI prints plain text; its own session list names the
    native session afterwards, and --resume reopens exactly that session."""
    homes = []
    text_mode = True

    def prepare(self, c, home, env):
        env["DEVIN_DISABLE_AUTOUPDATE"] = "1"
        if c["auth"] == "api_key":
            refuse_vendor_credential("devin", "through WINDSURF_API_KEY")
        elif c["auth"] == "subscription":
            # Devin reads its device login only from $XDG_DATA_HOME/devin/
            # credentials.toml, a file its own read and exec tools can reach.
            refuse_vendor_credential("devin", "from its own login file")
        env["DEVIN_SESSIONS_DIR"] = str(home / "devin-sessions")

    def argv(self, c, state, prompt):
        argv = [c["binary"], "--respect-workspace-trust", "false", "--permission-mode", "dangerous"]
        if state.get("session_id"):
            argv += ["--resume", state["session_id"]]
        if c.get("model"):
            argv += ["--model", c["model"]]
        return argv + ["-p", prompt], None

    def after(self, c, state, env):
        if state.get("session_id"):
            return
        listed = subprocess.run([c["binary"], "list", "--format", "json"], capture_output=True, text=True, env=env, timeout=60)
        try:
            sessions = json.loads(listed.stdout)
        except ValueError:
            return
        if isinstance(sessions, dict):
            sessions = sessions.get("sessions", [])
        if sessions:
            newest = max(sessions, key=lambda x: str(x.get("updated_at", x.get("created_at", ""))))
            state["session_id"] = newest.get("id") or newest.get("session_id", "")


class Amp:
    homes = ["config", "data"]

    def prepare(self, c, home, env):
        env.update({"XDG_CONFIG_HOME": str(home / "config"), "XDG_DATA_HOME": str(home / "data"), "AMP_SKIP_UPDATE_CHECK": "1"})
        if c["auth"] in ("api_key", "subscription"):
            refuse_vendor_credential("amp", "through AMP_API_KEY or its own login file")

    def argv(self, c, state, prompt):
        argv = [c["binary"]]
        if state.get("session_id"):
            argv += ["threads", "continue", state["session_id"]]
        return argv + ["--execute", prompt, "--stream-json", "--dangerously-allow-all"], None

    def event(self, c, state, e):
        if isinstance(e.get("session_id"), str):
            state["session_id"] = e["session_id"]
        if e.get("type") == "assistant":
            return text_of(e.get("message", {}).get("content")), None
        if e.get("type") == "result":
            return "", not e.get("is_error", False)
        return "", None


ADAPTERS = {"claude": Claude, "codex": Codex, "opencode": OpenCode, "pi": Pi, "goose": Goose, "copilot": Copilot, "cursor": Cursor, "gemini": Gemini, "droid": Droid, "devin": Devin, "amp": Amp}


RESUME_KEY = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,199}")


def load_state(path):
    """lamp-native.json may arrive in a continuation archive from a sandbox,
    so it is untrusted. Keep only resume keys that can select a session (never
    a path or an option) and the working directory; fork fields are set only
    by the adapter during a turn."""
    try:
        raw = json.loads(path.read_text()) if path.exists() else {}
    except ValueError:
        raw = {}
    state = {}
    for key in ("session_id", "session_name"):
        value = raw.get(key)
        if isinstance(value, str) and RESUME_KEY.fullmatch(value):
            state[key] = value
    if isinstance(raw.get("directory"), str):
        state["directory"] = raw["directory"]
    if isinstance(raw.get("turns"), int) and raw["turns"] >= 0:
        state["turns"] = raw["turns"]
    dropped = sorted(set(raw) - set(state))
    if dropped:
        print("lamp: ignored untrusted native state fields: " + ", ".join(dropped), file=sys.stderr, flush=True)
    return state


def main():
    config = json.loads(Path(sys.argv[1]).read_text())
    adapter = ADAPTERS[config["kind"]]()
    home = Path(os.environ["HOME"])
    home.mkdir(mode=0o700, parents=True, exist_ok=True)
    for name in adapter.homes:
        (home / name).mkdir(mode=0o700, exist_ok=True)
    state_path = home / "lamp-native.json"
    state = load_state(state_path)
    environment = dict(os.environ)
    if config.get("ca_file"):
        environment.update({"NODE_EXTRA_CA_CERTS": config["ca_file"], "SSL_CERT_FILE": config["ca_file"], "CODEX_CA_CERTIFICATE": config["ca_file"], "REQUESTS_CA_BUNDLE": config["ca_file"]})
    if command := config.get("mcp_command"):
        values = {"{session_id}": config.get("session_id", ""), "{sandbox_id}": config.get("sandbox_id", "")}
        argv = []
        for part in command:
            for key, value in values.items():
                part = part.replace(key, value)
            argv.append(part)
        # The MCP bridge and everything it starts never see model credentials.
        strip = [part for name in SECRET_NAMES for part in ("-u", name)]
        config["mcp"] = {"command": "/usr/bin/env", "args": strip + argv}
        path = home / "lamp-mcp.json"
        path.write_text(json.dumps({"mcpServers": {"lamp": config["mcp"]}}))
        config["mcp_config"] = str(path)
    subscription = config["auth"] == "subscription"
    if subscription and config.get("location_kind") == "sandbox":
        # Inside a sandbox the agent's exec shares this runner's identity, so a
        # subscription credential there must use the guest custody route.
        raise ValueError("subscription credentials run inside a sandbox only through guest custody")
    for name in SECRET_NAMES:
        environment.pop(name, None)
    # Private native event trace for adapter diagnosis; stays in the session home.
    trace = (home / "native-events.jsonl").open("a") if config.get("trace") else None
    for line in sys.stdin:
        request = json.loads(line)
        identity = request["input_id"]
        emit({"type": "status", "status": "working", "input_id": identity})
        # Re-read short-lived transport grants and custody files every turn.
        adapter.prepare(config, home, environment)
        if hasattr(adapter, "before"):
            adapter.before(config, state, environment)
        argv, stdin = adapter.argv(config, state, request["text"])
        passed = ()
        child = dict(environment)
        if subscription and config["kind"] == "claude":
            # Hand the plan token over a one-shot pipe: it never enters any
            # process environment, and the drained pipe holds nothing later.
            reader, writer = os.pipe()
            os.write(writer, Path(config["credential_file"]).read_bytes().strip())
            os.close(writer)
            child["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] = str(reader)
            passed = (reader,)
        process = subprocess.Popen(argv, stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=child, text=True, pass_fds=passed)
        for descriptor in passed:
            os.close(descriptor)
        if stdin is not None:
            process.stdin.write(stdin)
            process.stdin.close()
        success = None
        pending = ""
        for raw in process.stdout:
            if getattr(adapter, "text_mode", False):
                display(raw.rstrip(), environment)
                continue
            try:
                event = json.loads(raw)
                if not isinstance(event, dict):
                    raise ValueError
            except ValueError:
                if raw.strip():
                    display(raw.rstrip(), environment)
                continue
            if trace:
                trace.write(raw)
                trace.flush()
            text, finished = adapter.event(config, state, event)
            if text and getattr(adapter, "deltas", False):
                pending += text
            elif text:
                display(text, environment)
            if pending and finished is not None:
                display(pending, environment)
                pending = ""
            if finished is not None:
                success = finished if success is not False else False
        if pending:
            display(pending, environment)
        code = process.wait()
        if getattr(adapter, "text_mode", False):
            success = code == 0
        if hasattr(adapter, "after"):
            adapter.after(config, state, environment)
        ok = bool(success) and code == 0
        if ok:
            state["turns"] = state.get("turns", 0) + 1
        atomic(state_path, state)
        completion = {"type": "input_completed", "input_id": identity, "success": ok, "continuation_ref": state.get("session_id", state.get("session_name", ""))}
        if state.get("forked_from_id"):
            # Reported, never hidden: this native session was forked from that one.
            completion["forked_from"] = state["forked_from_id"]
        emit(completion)
        emit({"type": "status", "status": "idle" if ok else "blocked", "input_id": identity})


if __name__ == "__main__":
    os.umask(0o077)
    main()
