import argparse
import base64
from concurrent.futures import ThreadPoolExecutor
import json
import re
import secrets
import sys
import threading

from session_public import PublicSessionError, PublicSessions


class LampACP:
    def __init__(self, args):
        self.args = args
        self.client = PublicSessions(args.socket, args.url, args.token_file)
        self.sessions = {}
        self.output_lock = threading.Lock()
        self.closed = threading.Event()

    def send(self, value):
        with self.output_lock:
            print(json.dumps(value), flush=True)

    def update(self, identity, text, role="agent"):
        self.send({"jsonrpc": "2.0", "method": "session/update", "params": {"sessionId": identity, "update": {"sessionUpdate": role + "_message_chunk", "content": {"type": "text", "text": text}}}})

    def release(self, state):
        lease = state.get("lease")
        if lease:
            try:
                self.client.mutate("leases", lease, ":release")
            except PublicSessionError:
                pass
            state["lease"] = None

    def adopt(self, identity, cwd, mode="watch"):
        resource = self.client.get("sessions", identity)
        state = self.sessions.get(identity)
        if state is None:
            state = {"cwd": cwd, "mode": mode, "lease": None, "epoch": resource.get("location_epoch"), "sequence": 0, "cursor": None, "done": threading.Event(), "turn_lock": threading.Lock(), "closed": threading.Event()}
            self.sessions[identity] = state
            threading.Thread(target=self.watch, args=(identity, state), daemon=True).start()
        return state

    def watch(self, identity, state):
        while not self.closed.is_set() and not state["closed"].is_set():
            try:
                page = self.client.watch(identity, state["cursor"], wait_ms=1000)
                for frame in page["items"]:
                    data = base64.b64decode(frame["data_base64"])
                    if frame["channel"] in {"stdout", "stderr"}:
                        text = data.decode(errors="replace")
                        self.update(identity, text)
                        if marker := state.get("shell_marker"):
                            state["shell_output"] = (state.get("shell_output", "") + text)[-4096:]
                            if match := re.search(r"(?:^|\r?\n)" + re.escape(marker) + r":([0-9]+)\r?\n", state["shell_output"]):
                                state["completion"] = {"success": match[1] == "0"}
                                state["done"].set()
                    elif frame["channel"] == "control":
                        event = json.loads(data)
                        if event.get("type") == "input_completed" and event.get("input_id") == state.get("pending"):
                            state["completion"] = event
                            state["done"].set()
                state["cursor"] = page.get("next_cursor")
                if state["cursor"] is None:
                    state["error"] = "session ended"
                    state["done"].set()
                    return
            except (PublicSessionError, OSError, ValueError) as error:
                if isinstance(error, (OSError, ValueError)) or error.status >= 500:
                    if self.closed.wait(.25):
                        return
                    continue
                state["error"] = error.code
                state["done"].set()
                return

    def response(self, identity, state):
        return {"sessionId": identity, "modes": {"currentModeId": state["mode"], "availableModes": [{"id": "watch", "name": "Watch", "description": "Observe without an input lease"}, {"id": "input", "name": "Input", "description": "Acquire the exclusive input lease"}]}}

    def handle(self, method, params):
        if method == "initialize":
            fleet = self.client.call("GET", "/v1/fleets")["items"][0]
            return {"protocolVersion": 1, "agentInfo": {"name": "lamp", "title": "Lamp fleet " + fleet["id"], "version": "1.0"}, "agentCapabilities": {"loadSession": True, "sessionCapabilities": {"list": {}, "resume": {}, "close": {}}, "promptCapabilities": {"image": False, "audio": False, "embeddedContext": False}}, "authMethods": [], "_meta": {"lamp/fleet_id": fleet["id"]}}
        if method == "session/list":
            page = self.client.call("GET", "/v1/sessions")
            sessions = []
            for item in page["items"]:
                sessions.append({"sessionId": item["id"], "cwd": params.get("cwd", self.args.cwd), "title": item.get("harness_id", "Lamp session") + " · " + item["id"], "updatedAt": item["updated_at"], "_meta": {"lamp": {k: item.get(k) for k in ["fleet_id", "state", "status", "location", "location_epoch", "parent_session_id"]}}})
            return {"sessions": sessions}
        if method == "session/new":
            value = {"sandbox_id": self.args.sandbox_id or "", "harness_id": self.args.harness, "auth_mode": self.args.auth_mode, "agent_mode": "outside" if self.args.location_kind == "host" else "inside", "location": {"kind": self.args.location_kind, "host_id": self.args.host_id}}
            if self.args.sandbox_id:
                value["sandbox_id"] = self.args.sandbox_id
                if self.args.location_kind == "sandbox":
                    value["location"]["sandbox_id"] = self.args.sandbox_id
            identity = self.client.call("POST", "/v1/sessions", value)["result"]["session_id"]
            self.client.wait(lambda: self.client.get("sessions", identity)["state"] in {"active", "failed"})
            state = self.adopt(identity, params["cwd"], "input")
            return self.response(identity, state)
        identity = params["sessionId"]
        if method in {"session/load", "session/resume"}:
            state = self.adopt(identity, params.get("cwd", self.args.cwd), self.args.join_mode)
            return self.response(identity, state)
        if method == "session/close":
            state = self.sessions.pop(identity, None)
            if state:
                state["closed"].set()
                self.release(state)
            return {}
        state = self.sessions[identity]
        if method == "session/set_mode":
            mode = params["modeId"]
            if mode not in {"watch", "input"}:
                raise ValueError("unknown mode")
            if mode == "watch":
                self.release(state)
            elif not state["lease"]:
                state["lease"] = self.client.lease(identity)
                state["sequence"] = 0
            state["mode"] = mode
            return {}
        if method == "session/cancel":
            self.client.mutate("sessions", identity, ":cancel")
            return {}
        if method == "session/prompt":
            if state["mode"] != "input":
                raise ValueError("select Input mode explicitly before typing")
            if any(block.get("type") != "text" for block in params["prompt"]):
                raise ValueError("this enrolled session accepts text input")
            with state["turn_lock"]:
                resource = self.client.get("sessions", identity)
                if resource.get("location_epoch") != state["epoch"]:
                    self.release(state)
                    state["epoch"] = resource.get("location_epoch")
                if not state["lease"]:
                    state["lease"] = self.client.lease(identity)
                    state["sequence"] = 0
                text = "\n".join(block["text"] for block in params["prompt"])
                state["done"].clear()
                state.pop("completion", None)
                state["pending"] = state["lease"] + "." + str(state["sequence"])
                raw = resource.get("auth_mode", "none") == "none"
                if raw:
                    if not self.args.shell:
                        raise ValueError("PTY sessions require the explicit --shell command-agent enrollment for ACP turns")
                    if self.args.prompt_envelope == "t3" and text.startswith("<t3_code_instructions>"):
                        match = re.match(r"<t3_code_instructions>[\s\S]*?</t3_code_instructions>\s*<user_request>\n([\s\S]*)\n</user_request>", text)
                        if match is None:
                            raise ValueError("unsupported public T3 command envelope")
                        text = match[1]
                    if self.args.prompt_envelope == "t3":
                        text = re.sub(r"(?:\s*<runtime_info>[\s\S]*?</runtime_info>|\s*<pull_request_linking>[\s\S]*?</pull_request_linking>)+\s*$", "", text)
                    marker = "LAMP_SHELL_" + secrets.token_hex(16)
                    state["shell_marker"], state["shell_output"] = marker, ""
                    text = "{\n" + text + "\n}\n_lamp_status=$?; printf '\\n" + marker + ":%s\\n' \"$_lamp_status\"\n"
                self.client.input(identity, state["lease"], state["sequence"], text.encode())
                state["sequence"] += 1
                state["done"].wait()
                if state.get("error"):
                    raise ValueError(state["error"])
                if state.get("completion", {}).get("interrupted"):
                    return {"stopReason": "cancelled"}
                if not state.get("completion", {}).get("success"):
                    raise ValueError("native turn did not complete successfully; acknowledged input remains recorded")
                return {"stopReason": "end_turn"}
        raise ValueError("method not supported by this enrolled Lamp adapter")

    def dispatch(self, message):
        try:
            result = self.handle(message["method"], message.get("params", {}))
            self.send({"jsonrpc": "2.0", "id": message["id"], "result": result})
        except (PublicSessionError, ValueError, KeyError, OSError) as error:
            self.send({"jsonrpc": "2.0", "id": message["id"], "error": {"code": -32000, "message": error.code if isinstance(error, PublicSessionError) else str(error)}})


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--socket")
    parser.add_argument("--url")
    parser.add_argument("--token-file")
    parser.add_argument("--harness", required=True)
    parser.add_argument("--auth-mode", default="api_key")
    parser.add_argument("--location-kind", choices=["host", "sandbox"], default="host")
    parser.add_argument("--host-id", required=True)
    parser.add_argument("--sandbox-id")
    parser.add_argument("--cwd", default="/tmp")
    parser.add_argument("--shell", action="store_true")
    parser.add_argument("--join-mode", choices=["watch", "input"], default="watch")
    parser.add_argument("--prompt-envelope", choices=["plain", "t3"], default="plain")
    args = parser.parse_args()
    agent = LampACP(args)
    with ThreadPoolExecutor(max_workers=8) as pool:
        try:
            for line in sys.stdin:
                message = json.loads(line)
                if "id" in message and "method" in message:
                    pool.submit(agent.dispatch, message)
                elif message.get("method") == "session/cancel":
                    try:
                        agent.handle("session/cancel", message["params"])
                    except (PublicSessionError, ValueError, KeyError, OSError) as error:
                        agent.update(message["params"]["sessionId"], "\nCancellation failed: " + str(error) + "\n")
        finally:
            agent.closed.set()
            for state in agent.sessions.values():
                state["error"] = "console disconnected; backend session remains durable"
                state["done"].set()
                agent.release(state)


if __name__ == "__main__":
    main()
