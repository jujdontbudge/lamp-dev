import json
import socket
import sys


MAX_REQUEST = 4096
MAX_REPLY = 32768
SIGNAL = b"NEXT\n"
TOOL = {
    "name": "next_step",
    "description": "Run the next owner-approved build, download or git step in the separate token-free sandbox. No arguments are accepted.",
    "inputSchema": {"type": "object", "properties": {}, "additionalProperties": False},
}


def exchange():
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
        connection.settimeout(180)
        connection.connect("/run/lamp/subscription-task.sock")
        connection.sendall(SIGNAL)
        connection.shutdown(socket.SHUT_WR)
        chunks = bytearray()
        while len(chunks) <= MAX_REPLY:
            block = connection.recv(min(4096, MAX_REPLY + 1 - len(chunks)))
            if not block:
                break
            chunks.extend(block)
    if len(chunks) > MAX_REPLY:
        raise ValueError("oversized task reply")
    result = json.loads(chunks)
    if not isinstance(result, dict) or result.get("status") not in ("completed", "denied"):
        raise ValueError("invalid task reply")
    if result["status"] == "completed" and (
        set(result) != {"status", "step", "exit_code", "output"}
        or type(result["step"]) is not int
        or not 0 <= result["step"] < 8
        or type(result["exit_code"]) is not int
        or not isinstance(result["output"], str)
        or len(result["output"]) > 4096
    ):
        raise ValueError("invalid bounded task result")
    return result


def dispatch(message, next_step=exchange):
    if not isinstance(message, dict) or message.get("jsonrpc") != "2.0":
        return {"jsonrpc": "2.0", "id": None, "error": {"code": -32600, "message": "invalid request"}}
    identifier = message.get("id")
    if identifier is None:
        return None
    response = {"jsonrpc": "2.0", "id": identifier}
    method = message.get("method")
    params = message.get("params", {})
    if method == "initialize":
        version = params.get("protocolVersion") if isinstance(params, dict) else None
        if version not in ("2024-11-05", "2025-03-26", "2025-06-18"):
            version = "2025-06-18"
        response["result"] = {
            "protocolVersion": version,
            "capabilities": {"tools": {"listChanged": False}},
            "serverInfo": {"name": "lamp-bounded-task", "version": "1.0.0"},
        }
    elif method == "ping":
        response["result"] = {}
    elif method == "tools/list":
        response["result"] = {"tools": [TOOL]}
    elif method == "tools/call":
        allowed = (
            isinstance(params, dict)
            and set(params) <= {"name", "arguments", "_meta"}
            and params.get("name") == "next_step"
            and params.get("arguments", {}) == {}
        )
        if not allowed:
            response["result"] = {
                "isError": True,
                "content": [{"type": "text", "text": "Only the installed no-argument tool is permitted."}],
            }
        else:
            try:
                result = next_step()
                response["result"] = {
                    "isError": result.get("status") != "completed",
                    "content": [{"type": "text", "text": json.dumps(result, separators=(",", ":"))}],
                }
            except (OSError, ValueError, TypeError):
                response["result"] = {
                    "isError": True,
                    "content": [{"type": "text", "text": "The bounded task bridge is unavailable."}],
                }
    else:
        response["error"] = {"code": -32601, "message": "method not found"}
    return response


def main():
    while line := sys.stdin.buffer.readline(MAX_REQUEST + 1):
        if len(line) > MAX_REQUEST:
            return 1
        try:
            response = dispatch(json.loads(line))
        except (ValueError, TypeError):
            response = {"jsonrpc": "2.0", "id": None, "error": {"code": -32700, "message": "invalid JSON"}}
        if response is not None:
            print(json.dumps(response, separators=(",", ":")), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
