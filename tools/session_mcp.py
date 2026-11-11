import argparse
import base64
import json
import sys

from session_public import PublicSessionError, PublicSessions


TOOLS = [
    {"name": "exec", "description": "Run an argv command in the enrolled Lamp sandbox, never on the harness host.", "inputSchema": {"type": "object", "properties": {"argv": {"type": "array", "items": {"type": "string"}, "minItems": 1}, "cwd": {"type": "string"}}, "required": ["argv"], "additionalProperties": False}},
    {"name": "spawn", "description": "Start a separately visible child session at the caller-selected Lamp location.", "inputSchema": {"type": "object", "properties": {"harness_id": {"type": "string"}, "auth_mode": {"type": "string"}, "sandbox_mode": {"type": "string", "description": "same, sandbox:<id>, or host:<id>"}}, "required": ["harness_id", "sandbox_mode"], "additionalProperties": False}},
    {"name": "git_return", "description": "Return this child session's committed work to its parent through a Lamp git change set.", "inputSchema": {"type": "object", "properties": {"selected_paths": {"type": "array", "items": {"type": "string"}}}, "additionalProperties": False}},
]


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--socket")
    parser.add_argument("--url")
    parser.add_argument("--token-file")
    parser.add_argument("--sandbox-id", required=True)
    parser.add_argument("--session-id")
    args = parser.parse_args()
    client = PublicSessions(args.socket, args.url, args.token_file)
    for line in sys.stdin:
        message = json.loads(line)
        if "id" not in message:
            continue
        reply = {"jsonrpc": "2.0", "id": message["id"]}
        try:
            method = message["method"]
            if method == "initialize":
                result = {"protocolVersion": "2025-06-18", "capabilities": {"tools": {}}, "serverInfo": {"name": "lamp-sandbox", "version": "1.0"}}
            elif method == "ping":
                result = {}
            elif method == "tools/list":
                result = {"tools": TOOLS}
            elif method == "tools/call":
                params = message["params"]
                values = params.get("arguments", {})
                if params["name"] == "exec":
                    if set(values) - {"argv", "cwd"}:
                        raise ValueError("unsupported exec field")
                    identity = client.call("POST", "/v1/execs", dict(values, sandbox_id=args.sandbox_id, wait_ms=60000))["result"]["exec_id"]
                    resource = client.wait(lambda: (item if (item := client.get("execs", identity))["state"] in {"exited", "failed", "outcome_unknown"} else None), timeout=180)
                    frames = client.call("GET", "/v1/execs/" + identity + "/output")["items"]
                    output = b"".join(base64.b64decode(frame["data_base64"]) for frame in frames)
                    value = {"exec_id": identity, "state": resource["state"], "exit_code": resource.get("exit_code"), "output": output[-16384:].decode(errors="replace")}
                elif params["name"] == "spawn" and args.session_id:
                    value = client.mutate("sessions", args.session_id, ":spawn", values)
                elif params["name"] == "git_return" and args.session_id:
                    parent = client.get("sessions", args.session_id).get("parent_session_id")
                    if not parent:
                        raise ValueError("session has no parent")
                    value = client.call("POST", "/v1/change-sets", dict(values, source_session_id=args.session_id, target_session_id=parent))
                else:
                    raise ValueError("unsupported or unscoped tool")
                result = {"content": [{"type": "text", "text": json.dumps(value)}]}
            else:
                raise ValueError("unsupported method")
            reply["result"] = result
        except (PublicSessionError, ValueError, KeyError) as error:
            reply["error"] = {"code": -32602, "message": error.code if isinstance(error, PublicSessionError) else str(error)}
        print(json.dumps(reply), flush=True)


if __name__ == "__main__":
    main()
