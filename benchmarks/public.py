import base64
import json
import http.client
import os
import socket
import subprocess
import time
import urllib.parse
import urllib.error
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, newurl):
        raise urllib.error.HTTPError(request.full_url, code, "API redirects are not allowed", headers, response)


class UnixHTTPHandler(urllib.request.HTTPHandler):
    """HTTP over the public private Unix socket, without a gateway process."""
    def __init__(self, path):
        super().__init__()
        self.path = path

    def http_open(self, request):
        path = self.path

        class Connection(http.client.HTTPConnection):
            def connect(self):
                self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                self.sock.settimeout(self.timeout)
                self.sock.connect(path)

        return self.do_open(Connection, request)


class PublicLamp:
    def __init__(self, url=None, cli=None, timeout=30, token=None, socket_path=None):
        if socket_path:
            if url or cli:
                raise ValueError("Unix API socket is a separate transport choice")
            url = "http://localhost"
        if bool(url) == bool(cli):
            raise ValueError("choose API URL or CLI executable")
        if url and urllib.parse.urlsplit(url).scheme not in ("http", "https"):
            raise ValueError("API must use HTTP(S)")
        if url:
            parsed = urllib.parse.urlsplit(url)
            if parsed.username or parsed.password or parsed.query or parsed.fragment:
                raise ValueError("API credentials belong only in LAMP_BENCH_TOKEN")
            if token and parsed.scheme != "https" and parsed.hostname not in ("localhost", "127.0.0.1", "::1"):
                raise ValueError("remote authenticated API requires HTTPS")
        self.url, self.cli, self.timeout, self.token = url, cli, timeout, token
        self.calls = []
        self.opener = urllib.request.build_opener(NoRedirect(), *([UnixHTTPHandler(socket_path)] if socket_path else []))

    def call(self, noun, action, fields=None):
        fields = dict(fields or {})
        started = time.monotonic_ns()
        record = {"noun": noun, "action": action, "started_ns": started}
        try:
            if self.cli:
                args = [self.cli, noun, action.replace("_", "-")]
                identity = noun + "_id"
                if identity in fields and action != "create":
                    args.append(str(fields.pop(identity)))
                for key, value in fields.items():
                    flag = "--" + key.replace("_", "-")
                    if isinstance(value, (dict, list)):
                        flag += "-json"
                        value = json.dumps(value, separators=(",", ":"))
                    elif isinstance(value, bool):
                        value = str(value).lower()
                    args.extend([flag, str(value)])
                args.append("--json")
                result = subprocess.run(args, capture_output=True, timeout=self.timeout, check=True)
                value = json.loads(result.stdout)
            else:
                path = "/v1/" + {"sandbox": "sandboxes", "exec": "execs", "operation": "operations", "key": "keys"}[noun]
                method = "POST"
                if action == "list":
                    method = "GET"
                elif action != "create":
                    identity = fields.pop(noun + "_id")
                    path += "/" + urllib.parse.quote(identity, safe="")
                    if action in ("get", "watch"):
                        method = "GET"
                        if action == "watch":
                            path += "/output"
                    elif action == "delete":
                        method = "DELETE"
                    else:
                        path += ":" + action.replace("_", "-")
                headers = {"Content-Type": "application/json"}
                if method == "GET":
                    if fields:
                        path += "?" + urllib.parse.urlencode(fields)
                    body = None
                else:
                    for field, header in (("idempotency_key", "Idempotency-Key"), ("expected_revision", "If-Match")):
                        if field in fields:
                            headers[header] = str(fields.pop(field))
                    body = json.dumps(fields).encode()
                if self.token:
                    headers["Authorization"] = "Bearer " + self.token
                request = urllib.request.Request(self.url.rstrip("/") + path, body, headers, method=method)
                with self.opener.open(request, timeout=self.timeout) as response:
                    raw = response.read(4 * 1024 * 1024 + 1)
                    if len(raw) > 4 * 1024 * 1024:
                        raise ValueError("public response exceeds bound")
                    value = json.loads(raw)
            record["request_id"] = value.get("request_id")
            record["operation_id"] = value.get("operation_id")
            record["resource_ids"] = value.get("resource_ids", [])
            completion = value.get("result", value.get("data", {}).get("outcome", {}).get("result", {}))
            if isinstance(completion, dict):
                record["completion_ids"] = {key: item for key, item in completion.items() if key.endswith(("_id", "_ids"))}
            if isinstance(value.get("data"), dict):
                record["resource"] = {key: item for key, item in value["data"].items() if key in ("id", "state", "revision", "readiness")}
            return value
        except Exception as error:
            record["error"] = type(error).__name__
            raise
        finally:
            record["elapsed_ns"] = time.monotonic_ns() - started
            self.calls.append(record)

    def wait_ms(self, deadline):
        """A server-side wait bounded by the caller's own deadline (API max 60 s)."""
        return max(1, min(60000, int((deadline - time.monotonic()) * 1000)))

    def mutation(self, noun, action, fields):
        # The public wait_ms makes the server hold the reply until the operation
        # completes, so completion is seen without a client polling loop.
        deadline = time.monotonic() + self.timeout
        value = self.call(noun, action, {**fields, "idempotency_key": uuid.uuid4().hex, "wait_ms": self.wait_ms(deadline)})
        operation = value.get("operation", {})
        if operation.get("state") in ("failed", "canceled", "outcome_unknown"):
            raise RuntimeError("operation " + operation["state"])
        if "result" in value and (not operation or operation.get("state") == "succeeded"):
            return value["result"]
        identity = value["operation_id"]
        while time.monotonic() < deadline:
            operation = self.call("operation", "get", {"operation_id": identity})["data"]
            if operation["state"] == "succeeded":
                return operation["outcome"]["result"]
            if operation["state"] in ("failed", "canceled", "outcome_unknown"):
                raise RuntimeError("operation " + operation["state"])
            time.sleep(.01)
        raise TimeoutError("operation completion")

    def get(self, sandbox):
        return self.call("sandbox", "get", {"sandbox_id": sandbox})["data"]

    def delete(self, sandbox):
        revision = self.get(sandbox)["revision"]
        return self.mutation("sandbox", "delete", {"sandbox_id": sandbox, "expected_revision": revision})

    def execute(self, sandbox, argv):
        identity = self.mutation("exec", "create", {"sandbox_id": sandbox, "argv": argv})["exec_id"]
        return self.wait_exec(identity)

    def wait_exec(self, identity):
        deadline = time.monotonic() + self.timeout
        cursor, output, seen = None, bytearray(), set()
        exit_observed = False
        while time.monotonic() < deadline:
            fields = {"exec_id": identity}
            if not exit_observed:
                # Long poll: the server answers when output arrives or the
                # command stops running, not on a client timer.
                fields["wait_ms"] = self.wait_ms(deadline)
            if cursor is not None:
                fields["cursor"] = cursor
            page = self.call("exec", "watch", fields)
            for frame in page["items"]:
                key = (frame["stream_id"], frame["sequence"])
                if key not in seen and frame["channel"] == "stdout":
                    output.extend(base64.b64decode(frame["data_base64"], validate=True))
                    if len(output) > 4 * 1024 * 1024:
                        raise ValueError("workload output exceeds bound")
                seen.add(key)
            if page.get("next_cursor"):
                cursor = page["next_cursor"]
                continue
            if exit_observed:
                # Drained every page published after the exit.
                return json.loads(output)
            status = self.call("exec", "get", {"exec_id": identity})["data"]
            if status["state"] in ("failed", "outcome_unknown"):
                raise RuntimeError("exec " + status["state"])
            if status["state"] == "exited":
                if status.get("exit_code") != 0:
                    raise RuntimeError("workload exited unsuccessfully")
                exit_observed = True
                continue
            time.sleep(.01)
        raise TimeoutError("independent useful command")


def token_from_environment():
    return os.environ.get("LAMP_BENCH_TOKEN")
