import json
import os
from pathlib import Path
import subprocess
import sys


def emit(value):
    print("\x1eLAMP " + json.dumps(value, separators=(",", ":")), flush=True)


def atomic(path, value):
    temporary = path.with_suffix(".next")
    with temporary.open("w") as stream:
        json.dump(value, stream)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def main():
    config = json.loads(Path(sys.argv[1]).read_text())
    home = Path(os.environ["HOME"])
    home.mkdir(mode=0o700, parents=True, exist_ok=True)
    for name in ["claude", "codex"]:
        (home / name).mkdir(mode=0o700, exist_ok=True)
    state_path = home / "lamp-native.json"
    state = json.loads(state_path.read_text()) if state_path.exists() else {}
    environment = dict(os.environ)
    environment.update({"DISABLE_AUTOUPDATER": "1", "DISABLE_TELEMETRY": "1", "DISABLE_ERROR_REPORTING": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CONFIG_DIR": str(home / "claude"), "CODEX_HOME": str(home / "codex")})
    if config.get("ca_file"):
        environment.update({"NODE_EXTRA_CA_CERTS": config["ca_file"], "SSL_CERT_FILE": config["ca_file"]})
    mcp = None
    if command := config.get("mcp_command"):
        values = {"{session_id}": config.get("session_id", ""), "{sandbox_id}": config.get("sandbox_id", "")}
        argv = []
        for part in command:
            for key, value in values.items():
                part = part.replace(key, value)
            argv.append(part)
        mcp = {"command": argv[0], "args": argv[1:]}
        mcp_path = home / "lamp-mcp.json"
        mcp_path.write_text(json.dumps({"mcpServers": {"lamp": mcp}}))
        config["mcp_config"] = str(mcp_path)
    for line in sys.stdin:
        request = json.loads(line)
        identity = request["input_id"]
        prompt = request["text"]
        if config["auth"] == "api_key":
            token = Path(config["credential_file"]).read_text().strip()
            environment.update({"LAMP_MODEL_TOKEN": token, "ANTHROPIC_AUTH_TOKEN": token, "ANTHROPIC_API_KEY": "", "ANTHROPIC_BASE_URL": config["endpoint"]})
        elif config["auth"] == "subscription":
            if config["kind"] == "claude":
                environment["CLAUDE_CODE_OAUTH_TOKEN"] = Path(config["credential_file"]).read_text().strip()
            elif Path(config["credential_file"]).resolve() != (home / "codex/auth.json").resolve():
                raise ValueError("Codex subscription credentials must remain in the enrolled native custody home")
        emit({"type": "status", "status": "working", "input_id": identity})
        argv = [config["binary"]]
        if config["kind"] == "claude":
            argv += ["-p", "--output-format", "stream-json", "--verbose", "--setting-sources", "", "--permission-mode", "dontAsk", "--no-chrome", "--disallowedTools", "Agent,Task"]
            if config.get("agent_mode") == "outside":
                argv += ["--tools", ""]
            if config.get("model"):
                argv += ["--model", config["model"]]
            if state.get("session_id"):
                argv += ["--resume", state["session_id"]]
            if config.get("mcp_config"):
                argv += ["--strict-mcp-config", "--mcp-config", config["mcp_config"], "--allowedTools", "mcp__lamp__exec,mcp__lamp__spawn,mcp__lamp__git_return"]
        else:
            argv += ["--disable", "multi_agent", "-c", 'approval_policy="never"', "-c", 'web_search="disabled"']
            if config.get("agent_mode") == "outside":
                argv += ["--disable", "shell_tool", "--disable", "unified_exec"]
            if mcp:
                setting = dict(mcp, enabled_tools=["exec", "spawn", "git_return"], default_tools_approval_mode="approve", tool_timeout_sec=180)
                text = "{command=" + json.dumps(setting["command"]) + ",args=" + json.dumps(setting["args"]) + ',enabled_tools=["exec","spawn","git_return"],default_tools_approval_mode="approve",tool_timeout_sec=180}'
                argv += ["-c", "mcp_servers.lamp=" + text]
            if config["auth"] == "api_key":
                argv += ["-c", 'model_provider="lamp"', "-c", 'model_providers.lamp={name="Lamp broker",base_url=' + json.dumps(config["endpoint"]) + ',env_key="LAMP_MODEL_TOKEN",wire_api="responses"}']
            if config.get("model"):
                argv += ["-m", config["model"]]
            argv += ["exec"]
            if state.get("session_id"):
                argv += ["resume", state["session_id"]]
            else:
                argv += ["--sandbox", "workspace-write", "--skip-git-repo-check"]
            argv += ["--json", "-"]
        process = subprocess.Popen(argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=environment, text=True)
        process.stdin.write(prompt)
        process.stdin.close()
        success = False
        for raw in process.stdout:
            try:
                event = json.loads(raw)
            except ValueError:
                continue
            if config["kind"] == "claude":
                if event.get("session_id"):
                    state["session_id"] = event["session_id"]
                if event.get("type") == "assistant":
                    for content in event.get("message", {}).get("content", []):
                        if content.get("type") == "text":
                            print(content["text"], flush=True)
                if event.get("type") == "result":
                    success = not event.get("is_error", True)
            else:
                if event.get("type") == "thread.started":
                    state["session_id"] = event["thread_id"]
                if event.get("type") == "item.completed" and event.get("item", {}).get("type") == "agent_message":
                    print(event["item"]["text"], flush=True)
                if event.get("type") == "turn.completed":
                    success = True
        code = process.wait()
        atomic(state_path, state)
        emit({"type": "input_completed", "input_id": identity, "success": success and code == 0, "continuation_ref": state.get("session_id", "")})
        emit({"type": "status", "status": "idle" if success and code == 0 else "blocked", "input_id": identity})


if __name__ == "__main__":
    os.umask(0o077)
    main()
