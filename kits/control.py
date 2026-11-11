"""Kit effects use Lamp's public API, never worker RPCs or host Docker."""
import base64
import http.client
import json
import socket
import time
import uuid
from pathlib import Path


class KitError(Exception):
    """Only constant, secret-free messages may cross the task boundary."""


class Control:
    def __init__(self, path, token=""):
        self.path, self.token = path, token

    def call(self, method, path, body=None, revision=None):
        conn = http.client.HTTPConnection("lamp", timeout=65)
        conn.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        conn.sock.settimeout(65)
        conn.sock.connect(self.path)
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        if method != "GET":
            headers["Idempotency-Key"] = uuid.uuid4().hex
        if revision is not None:
            headers["If-Match"] = str(revision)
        try:
            conn.request(method, path, None if body is None else json.dumps(body), headers)
            response = conn.getresponse()
            data = response.read(4 * 1024 * 1024 + 1)
            if response.status >= 300:
                try:
                    code = json.loads(data).get('error', {}).get('code', '')
                except (ValueError, AttributeError):
                    code = ''
                if not isinstance(code, str) or len(code) > 64 or not code.isascii() or not code.replace('_', '').isalnum() or code != code.upper():
                    code = 'UNKNOWN'
                raise KitError('CONTROL_UNAVAILABLE:' + str(response.status) + ':' + code)
            if len(data) > 4 * 1024 * 1024:
                raise KitError("CONTROL_UNAVAILABLE")
            return json.loads(data)
        finally:
            conn.close()

    def mutation(self, method, path, body, revision=None):
        reply = self.call(method, path, dict(body, wait_ms=60000), revision)
        if "result" in reply:
            return reply["result"]
        operation = reply.get("operation_id")
        deadline = time.monotonic() + 65
        while operation and time.monotonic() < deadline:
            op = self.call("GET", "/v1/operations/" + operation)["data"]
            if op["state"] == "succeeded":
                return op["outcome"]["result"]
            if op["state"] in ("failed", "canceled", "outcome_unknown"):
                break
            time.sleep(.1)  # completion observation, not a trial/benchmark
        raise KitError("CONTROL_OUTCOME_UNKNOWN")

    def sandbox(self, identity, allow_suspended=False):
        value = self.call("GET", "/v1/sandboxes/" + identity)["data"]
        states = ('ready', 'suspended') if allow_suspended else ('ready',)
        if value.get("boundary") != "vm" or value.get("state") not in states:
            raise KitError("READY_VM_REQUIRED")
        return value

    def create(self, template, resources, browser_custody=False):
        body = {"template_id": template, "resources": resources, "durability": "volatile"}
        if browser_custody:
            body['labels'] = {'lamp.browser-custody': 'external'}
        return self.mutation("POST", "/v1/sandboxes", body)["sandbox_id"]

    def delete(self, identity):
        value = self.call("GET", "/v1/sandboxes/" + identity)["data"]
        if value["state"] != "deleted":
            self.mutation("DELETE", "/v1/sandboxes/" + identity, {}, value["revision"])
        if self.call("GET", "/v1/sandboxes/" + identity)["data"]["state"] != "deleted":
            raise KitError("CLEANUP_PENDING")

    def execute(self, identity, argv, timeout=65):
        self.sandbox(identity)
        result = self.mutation("POST", "/v1/execs", {"sandbox_id": identity, "argv": argv})
        exec_id = result["exec_id"]
        deadline, cursor, output = time.monotonic() + timeout, None, bytearray()
        while time.monotonic() < deadline:
            path = "/v1/execs/" + exec_id + "/output?wait_ms=1000"
            if cursor:
                from urllib.parse import quote
                path += "&cursor=" + quote(cursor, safe="")
            page = self.call("GET", path)
            for frame in page["items"]:
                if frame["channel"] == "stdout":
                    output.extend(base64.b64decode(frame["data_base64"], validate=True))
            if len(output) > 512 * 1024:
                raise KitError("GUEST_OUTPUT_BLOCKED")
            cursor = page.get("next_cursor")
            state = self.call("GET", "/v1/execs/" + exec_id)["data"]
            if state["state"] == "exited" and not cursor:
                try:
                    value = json.loads(output)
                except (ValueError, UnicodeError):
                    raise KitError('GUEST_ACTION_FAILED' if state.get('exit_code') != 0 else 'GUEST_OUTPUT_BLOCKED') from None
                if state.get('exit_code') != 0 or 'error' in value:
                    phase = value.get('phase')
                    suffix = ':' + phase if phase in ('boot.key', 'boot.start', 'boot.cdp', 'boot.context', 'navigate.request', 'navigate.network', 'navigate.observe') else ''
                    raise KitError('GUEST_ACTION_FAILED' + suffix)
                return value
            if state["state"] in ("failed", "outcome_unknown"):
                break
        raise KitError("GUEST_OUTCOME_UNKNOWN")

    def install_worker(self, identity):
        encoded = base64.b64encode(Path(__file__).with_name("guest.py").read_bytes()).decode()
        return self.execute(identity, ["python3", "-c", "import base64,pathlib,json; p=pathlib.Path('/tmp/lamp-kits-worker.py'); p.write_bytes(base64.b64decode('" + encoded + "')); p.chmod(0o600); print(json.dumps({'installed':True}))"])

    def browser(self, identity, operation, **arguments):
        return self.execute(identity, ["python3", "/tmp/lamp-kits-worker.py", json.dumps(dict(operation=operation, **arguments), separators=(",", ":"))])
