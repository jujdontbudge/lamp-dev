"""Transport for the generated Lamp client (hand-written, standard library only)."""
import http.client
import json
import secrets
import socket
import time
import urllib.parse
from typing import Any, Dict, Optional


class LampError(Exception):
    """A Lamp error envelope: code, message, retryable, details."""

    def __init__(self, status: int, body: Any):
        err = (body or {}).get("error", {}) if isinstance(body, dict) else {}
        super().__init__(err.get("message") or f"HTTP {status}")
        self.status = status
        self.code = err.get("code", "UNKNOWN")
        self.retryable = bool(err.get("retryable"))
        self.details = err.get("details")
        self.request_id = body.get("request_id") if isinstance(body, dict) else None


class _UnixConnection(http.client.HTTPConnection):
    def __init__(self, path: str, timeout: float):
        super().__init__("lamp", timeout=timeout)
        self._path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self._path)


class Transport:
    operations: Dict[str, Dict[str, Any]] = {}

    def __init__(self, socket: Optional[str] = None, base_url: Optional[str] = None,
                 token: Optional[str] = None, timeout: float = 70.0):
        if not socket and not base_url:
            raise ValueError("LampClient needs socket or base_url")
        if base_url and token and not base_url.startswith("https://"):
            raise ValueError("credentialed remote URLs must be HTTPS")
        self.socket, self.base_url, self.token, self.timeout = socket, base_url, token, timeout

    def _request(self, op: Dict[str, Any], name: str, body: Dict[str, Any]):
        body = dict(body)
        headers = {"Accept": "application/json"}
        path = op["path"]
        for k in list(body):
            ph = "{" + k + "}"
            if ph in path:
                v = body.pop(k)
                if not isinstance(v, str) or not v or "/" in v or "\0" in v:
                    raise ValueError("invalid path selector " + k)
                path = path.replace(ph, urllib.parse.quote(v, safe=""))
        if "{" in path:
            raise ValueError("missing resource ID for " + name)
        if op["mutation"]:
            headers["Idempotency-Key"] = str(body.pop("idempotency_key", None) or "sdk-" + secrets.token_hex(32))
            if "expected_revision" in body:
                headers["If-Match"] = str(body.pop("expected_revision"))
        data = None
        if op["method"] == "GET":
            q = {k: (json.dumps(v) if isinstance(v, (dict, list)) else str(v).lower() if isinstance(v, bool) else str(v)) for k, v in body.items()}
            if q:
                path += "?" + urllib.parse.urlencode(q)
        else:
            data = json.dumps(body).encode()
            headers["Content-Type"] = "application/json"
        if self.token:
            headers["Authorization"] = "Bearer " + self.token
        return op["method"], path, headers, data

    def call(self, name: str, input: Optional[Dict[str, Any]] = None) -> Any:
        op = self.operations.get(name)
        if op is None:
            raise ValueError("unknown Lamp operation " + name)
        method, path, headers, data = self._request(op, name, input or {})
        if self.socket:
            conn = _UnixConnection(self.socket, self.timeout)
        else:
            u = urllib.parse.urlsplit(self.base_url)
            conn = http.client.HTTPSConnection(u.hostname, u.port or 443, timeout=self.timeout)
        try:
            conn.request(method, path, body=data, headers=headers)
            resp = conn.getresponse()
            text = resp.read().decode()
        finally:
            conn.close()
        try:
            body = json.loads(text) if text else None
        except ValueError:
            body = {"error": {"code": "DATA_UNAVAILABLE", "message": "invalid API JSON"}}
        if resp.status >= 400:
            raise LampError(resp.status, body)
        return body

    def wait_operation(self, operation_id: str, timeout_ms: int = 60000) -> Dict[str, Any]:
        """Wait until the Operation leaves accepted/running; never implies success."""
        deadline = time.monotonic() + timeout_ms / 1000
        while True:
            left = max(0, min(30000, int((deadline - time.monotonic()) * 1000)))
            res = self.call("operation_wait", {"operation_id": operation_id, "timeout_ms": left})
            op = res.get("data", res)
            if op.get("state") not in ("accepted", "running") or time.monotonic() >= deadline:
                return op
