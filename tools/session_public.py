import base64
import http.client
import json
from pathlib import Path
import secrets
import socket
import time
import urllib.parse
import urllib.request


class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, path, timeout=60):
        super().__init__("localhost", timeout=timeout)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.connect(self.path)


class PublicSessionError(Exception):
    def __init__(self, code, status):
        self.code = code
        self.status = status
        super().__init__(code)


class PublicSessions:
    def __init__(self, socket_path=None, url=None, token_file=None):
        self.socket = str(socket_path) if socket_path else None
        self.url = url
        self.token = Path(token_file).read_text().strip() if token_file else None

    def call(self, method, path, value=None, revision=None, key=None):
        headers = {"Content-Type": "application/json"}
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        if method != "GET":
            headers["Idempotency-Key"] = key or secrets.token_hex(16)
        if revision is not None:
            headers["If-Match"] = str(revision)
        body = json.dumps(value).encode() if value is not None else None
        if self.socket:
            connection = UnixHTTP(self.socket)
        else:
            target = urllib.parse.urlsplit(self.url)
            if target.scheme == "https":
                connection = http.client.HTTPSConnection(target.hostname, target.port, timeout=60)
            elif target.scheme == "http" and target.hostname in {"127.0.0.1", "::1", "localhost"}:
                connection = http.client.HTTPConnection(target.hostname, target.port, timeout=60)
            else:
                raise ValueError("use HTTPS or an explicit loopback tunnel")
            path = target.path.rstrip("/") + path
        connection.request(method, path, body, headers)
        response = connection.getresponse()
        data = response.read()
        status = response.status
        connection.close()
        try:
            reply = json.loads(data)
        except ValueError:
            raise ValueError(f"invalid public reply to {method} {path}: HTTP {status}, {len(data)} bytes") from None
        if not 200 <= status < 300:
            raise PublicSessionError(reply.get("error", {}).get("code", "DATA_UNAVAILABLE"), status)
        return reply

    def get(self, kind, identity):
        return self.call("GET", "/v1/" + kind + "/" + identity)["data"]

    def mutate(self, kind, identity, action, value=None):
        return self.call("POST", "/v1/" + kind + "/" + identity + action, value or {}, self.get(kind, identity)["revision"])

    def lease(self, identity):
        return self.call("POST", "/v1/leases", {"type": "input", "resource_id": identity, "expires_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() + 240))})["result"]["lease_id"]

    def input(self, identity, lease, sequence, data):
        return self.mutate("sessions", identity, ":input", {"lease_id": lease, "location_epoch": self.get("sessions", identity)["location_epoch"], "sequence": sequence, "data_base64": base64.b64encode(data).decode()})

    def watch(self, identity, cursor=None, wait_ms=0):
        query = {"wait_ms": wait_ms}
        if cursor:
            query["cursor"] = cursor
        return self.call("GET", "/v1/sessions/" + identity + "/output?" + urllib.parse.urlencode(query))

    def text(self, identity):
        return b"".join(base64.b64decode(frame["data_base64"]) for frame in self.watch(identity)["items"] if frame["channel"] in {"stdout", "stderr"}).decode(errors="replace")

    def wait(self, condition, timeout=120):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            result = condition()
            if result:
                return result
            time.sleep(.1)
        raise TimeoutError("public session observation did not complete")
