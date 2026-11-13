"""Runs one enrolled official harness binary for a Lamp session.

Each adapter keeps the vendor's unmodified binary and its built-in sign-in or
API route. Continuation is always the harness's own resume mechanism keyed by
its native session identifier; a fresh prompt with a pasted transcript is
never presented as a continued session. Native sub-agent features are disabled
here because Lamp spawns sub-agents as separately visible sessions.
"""

import json
import re
import signal
import os
from pathlib import Path
import subprocess
import sys
import uuid

SECRET_NAMES = ["LAMP_MODEL_TOKEN", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_OAUTH_TOKEN", "OPENAI_API_KEY", "CODEX_API_KEY", "COPILOT_PROVIDER_API_KEY", "COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "CURSOR_API_KEY", "AMP_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "FACTORY_API_KEY", "WINDSURF_API_KEY", "DEVIN_API_KEY"]
LAMP_TOOLS = ["exec", "spawn", "git_return"]


# Each harness's own project-level config directory. It can carry permission
# overrides, hooks or MCP servers that would run with the vendor credential.
VENDOR_PROJECT_CONFIG = {"cursor": ".cursor", "devin": ".devin", "droid": ".factory"}


def vendor_credential(c):
    """A real vendor credential, as opposed to an endpoint-scoped broker grant."""
    return c["auth"] == "subscription" or c["auth"] == "api_key" and not c.get("endpoint")


def custody_pattern(c, home):
    """T21's pattern for a real vendor credential: the vendor's own controls
    deny every built-in tool that could read files, the environment or the
    process table, and the agent works only through Lamp's MCP tools. Those run
    in the sandbox from an environment without credentials (main() starts the
    bridge through env -u)."""
    if not c.get("mcp"):
        raise ValueError(c["kind"] + " with a vendor credential needs the Lamp exec bridge (mcp_command)")
    marker = VENDOR_PROJECT_CONFIG.get(c["kind"])
    here = Path.cwd().resolve()
    for directory in [here, *here.parents]:
        if directory != home.resolve() and marker and (directory / marker).exists():
            raise ValueError(c["kind"] + " with a vendor credential refuses a workspace that carries " + str(directory / marker) + ": project config could re-enable tools or run hooks")


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
        if c["auth"] == "api_key" or c.get("brokered_subscription"):
            env["LAMP_MODEL_TOKEN"] = secret(c)
        elif c["auth"] == "subscription" and Path(c["credential_file"]).resolve() != (home / "codex/auth.json").resolve():
            raise ValueError("Codex subscription credentials must remain in the enrolled native custody home")

    def argv(self, c, state, prompt):
        argv = [c["binary"]] + ([] if c.get("native_subagents") else ["--disable", "multi_agent"]) + ["-c", 'approval_policy="never"', "-c", 'web_search="disabled"', "-c", 'sandbox_mode="workspace-write"', "-c", "sandbox_workspace_write.network_access=true"]
        if c.get("location_kind") == "sandbox":
            # The VM is the isolation boundary. Permit the agent's own scoped
            # Lamp gateway, package installs and ordinary development traffic.
            argv += ["-c", 'sandbox_mode="danger-full-access"']
        if c.get("agent_mode") == "outside" or (c["auth"] == "subscription" and not c.get("brokered_subscription")):
            argv += ["--disable", "shell_tool", "--disable", "unified_exec"]
        if mcp := c.get("mcp"):
            argv += ["-c", "mcp_servers.lamp={command=" + json.dumps(mcp["command"]) + ",args=" + json.dumps(mcp["args"]) + ',default_tools_approval_mode="approve",tool_timeout_sec=180}']
        if c["auth"] == "api_key" or c.get("brokered_subscription"):
            argv += ["-c", 'model_provider="lamp"', "-c", 'model_providers.lamp={name="Lamp broker",base_url=' + json.dumps(c["endpoint"]) + ',env_key="LAMP_MODEL_TOKEN",wire_api="responses"}']
        if c.get("model"):
            argv += ["-m", c["model"]]
        argv += ["exec"]
        if state.get("session_id"):
            argv += ["resume", state["session_id"]]
        else:
            argv += ["--skip-git-repo-check"]
            if c.get("location_kind") != "sandbox":
                argv += ["--sandbox", "workspace-write"]
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
        if e.get("type") == "tool_use" and part.get("type") == "tool":
            tool = part.get("state") or {}
            if tool.get("status") in ("completed", "error"):
                return "[" + str(part.get("tool", "tool")) + "] " + str(tool.get("title", "")) + "\n" + str(tool.get("output") or tool.get("error") or ""), None
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
        if c["auth"] in ("api_key", "subscription"):
            custody_pattern(c, home)
            # Cursor's documented permissions: deny shell, every file read and
            # write, and web fetch; allow only Lamp's MCP tools. Deny rules
            # take precedence over allow rules.
            path = home / "config/cli-config.json"
            try:
                current = json.loads(path.read_text())
            except (OSError, ValueError):
                current = {}
            current["permissions"] = {"allow": ["Mcp(lamp:*)"], "deny": ["Shell(*)", "Read(/**)", "Read(**)", "Write(/**)", "Write(**)", "WebFetch(*)"]}
            write_private(path, current)
            write_private(home / ".cursor/mcp.json", {"mcpServers": {"lamp": c["mcp"]}})
        if c["auth"] == "api_key":
            env["CURSOR_API_KEY"] = secret(c)
        elif c["auth"] == "subscription" and Path(c["credential_file"]).resolve() != (home / ".config/cursor/auth.json").resolve():
            raise ValueError("Cursor's login must remain in the enrolled native custody home")

    def after(self, c, state, env):
        # Cursor saves an environment-supplied key to its own login file;
        # Read(...) is denied, but no copy of the key is kept between turns.
        if c["auth"] == "api_key":
            (Path(env["HOME"]) / ".config/cursor/auth.json").unlink(missing_ok=True)

    def argv(self, c, state, prompt):
        argv = [c["binary"], "--print", "--output-format", "stream-json", "--trust"]
        argv += ["--approve-mcps"] if vendor_credential(c) else ["--force"]
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
            # Factory-hosted models: the key stays on the custody host; droid
            # runs with only Lamp's MCP tools (--only-tools MCP:lamp).
            custody_pattern(c, home)
            env["FACTORY_API_KEY"] = secret(c)
            write_private(home / ".factory/mcp.json", {"mcpServers": {"lamp": dict(c["mcp"], type="stdio")}})

    def argv(self, c, state, prompt):
        argv = [c["binary"], "exec", "-o", "stream-json", "--auto", "high", "--disable-builtin-skills"]
        if vendor_credential(c):
            argv += ["--only-tools", "MCP:lamp"]
        elif not c.get("native_subagents"):
            argv += ["--remove-tools", "Task,SpawnAgent"]
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
        custody_pattern(c, home)
        if c["auth"] == "api_key":
            env["WINDSURF_API_KEY"] = secret(c)
        elif c["auth"] == "subscription":
            # Devin reads its device login only from $XDG_DATA_HOME/devin/
            # credentials.toml, so it is used in place, never copied.
            login = Path(c["credential_file"]).resolve()
            if login.name != "credentials.toml" or login.parent.name != "devin":
                raise ValueError("Devin subscription credential must be the login's devin/credentials.toml")
            env["XDG_DATA_HOME"] = str(login.parent.parent)
        # Devin's documented permissions: deny its read, edit, grep, glob and
        # exec tools and web fetch; allow only Lamp's MCP tools. A deny rule
        # always wins; project config is refused above.
        config = home / ".config/devin/config.json"
        try:
            current = json.loads(config.read_text())
        except (OSError, ValueError):
            current = {}
        current["permissions"] = {"deny": ["read", "edit", "grep", "glob", "exec", "Fetch(http://*)", "Fetch(https://*)"], "allow": ["mcp__lamp__*"]}
        write_private(config, current)
        write_private(home / ".config/devin/mcp_config.json", {"mcpServers": {"lamp": c["mcp"]}})
        env["DEVIN_SESSIONS_DIR"] = str(home / "devin-sessions")

    def argv(self, c, state, prompt):
        # Default permission mode: only the allow rule (Lamp's MCP tools) runs.
        argv = [c["binary"], "--respect-workspace-trust", "false"]
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
    active = [None]
    interrupted = [False]
    def cancel_turn(signum, frame):
        if active[0] is not None:
            interrupted[0] = True
            # Tool PTYs can create their own process groups. Kill only this
            # dedicated turn's descendants as well as its group, so cancelling
            # a tool such as sleep does not leave it running in the VM.
            root = active[0].pid
            try:
                listing = subprocess.check_output(["ps","-axo","pid=,ppid="],text=True)
                pairs = [tuple(map(int,line.split())) for line in listing.splitlines() if line.strip()]
                descendants = {root}
                for _ in range(len(pairs)):
                    found = {pid for pid,parent in pairs if parent in descendants}
                    if found <= descendants: break
                    descendants.update(found)
                for pid in reversed([pid for pid,_ in pairs if pid in descendants and pid != root]):
                    try: os.kill(pid,signal.SIGKILL)
                    except ProcessLookupError: pass
            except (OSError,subprocess.SubprocessError,ValueError):
                pass
            try:
                os.killpg(root, signal.SIGKILL)
            except ProcessLookupError:
                pass
    signal.signal(signal.SIGUSR1, cancel_turn)
    def end_session(signum, frame):
        cancel_turn(signum, frame)
        raise SystemExit(0)
    signal.signal(signal.SIGTERM, end_session)
    config = json.loads(Path(sys.argv[1]).read_text())
    if config.get("managed") and config["kind"] in ("opencode", "codex"):
        if not Path(config["binary"]).is_file():
            if config.get("location_kind") == "host":
                raise ValueError("install the official harness explicitly on the enrolled host first")
            package = "opencode-ai@1.19.5" if config["kind"] == "opencode" else "@openai/codex@0.168.0"
            print("lamp: installing " + package + " in the sandbox…", flush=True)
            subprocess.run(["npm", "install", "-g", "--no-audit", "--no-fund", "--progress=false", "--loglevel=error", "--maxsockets=1", package], check=True)
        if token := config.pop("transport_token", None):
            write_private(Path(config["credential_file"]), token)
    if cli_source := config.pop("guest_cli_source", None):
        cli = Path(config["credential_file"]).parent / "lamp.py"
        write_private(cli, cli_source)
        cli.chmod(0o700)
        # Guest-only executable; HOME never changes on the host.
        link = Path("/usr/local/bin/lamp")
        if not link.exists():
            link.symlink_to(cli)
        os.environ["LAMP_SESSION_ID"] = config["session_id"]
        os.environ["LAMP_API_URL"] = config["gateway"]
        os.environ["LAMP_TOKEN_FILE"] = config["credential_file"]
        os.environ.update(config.get("secret_placeholders", {}))
        os.environ["LAMP_MCP_TOOLS"] = json.dumps(config.get("gateway_tools", []))
        if config.get("gateway_tools"):
            config["mcp_command"] = [sys.executable, str(cli), "mcp"]
        for source in config.get("settings_sources") or []:
            import urllib.request
            req = urllib.request.Request(config["gateway"] + "/v1/settings/" + source + "/copy", data=b"{}", headers={"Authorization": "Bearer " + Path(config["credential_file"]).read_text().strip()})
            with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req) as response:
                snapshot = json.load(response)
            for relative, content in snapshot["files"].items():
                path = Path.cwd() / relative
                if path.is_absolute() and path.resolve().is_relative_to(Path.cwd().resolve()):
                    write_private(path, content)
            write_private(Path(config["credential_file"]).parent / (source + ".settings.json"), snapshot)
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
    if vendor_credential(config) and config.get("location_kind") == "sandbox":
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
        interrupted[0] = False
        process = subprocess.Popen(argv, stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, env=child, text=True, pass_fds=passed, start_new_session=True)
        active[0] = process
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
        active[0] = None
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
        if interrupted[0]:
            completion["interrupted"] = True
        emit(completion)
        emit({"type": "status", "status": "idle" if ok or interrupted[0] else "blocked", "input_id": identity})


if __name__ == "__main__":
    os.umask(0o077)
    main()
