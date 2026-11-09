import base64
import json
import os
import subprocess
import time
import urllib.parse
import urllib.error
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, response, code, message, headers, newurl):
        raise urllib.error.HTTPError(request.full_url, code, "API redirects are not allowed", headers, response)


class PublicLamp:
    def __init__(self, url=None, cli=None, timeout=30, token=None):
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
        self.opener = urllib.request.build_opener(NoRedirect())

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
                path = "/v1/" + {"sandbox": "sandboxes", "exec": "execs", "operation": "operations"}[noun]
                method = "POST"
                if action != "create":
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

    def mutation(self, noun, action, fields):
        value = self.call(noun, action, {**fields, "idempotency_key": uuid.uuid4().hex})
        deadline = time.monotonic() + self.timeout
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
            time.sleep(.001)
        raise TimeoutError("operation completion")

    def get(self, sandbox):
        return self.call("sandbox", "get", {"sandbox_id": sandbox})["data"]

    def delete(self, sandbox):
        revision = self.get(sandbox)["revision"]
        return self.mutation("sandbox", "delete", {"sandbox_id": sandbox, "expected_revision": revision})

    def execute(self, sandbox, argv):
        identity = self.mutation("exec", "create", {"sandbox_id": sandbox, "argv": argv})["exec_id"]
        deadline = time.monotonic() + self.timeout
        cursor, output, seen = None, bytearray(), set()
        exit_observed = False
        while time.monotonic() < deadline:
            fields = {"exec_id": identity}
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
            cursor = page.get("next_cursor", cursor)
            if exit_observed:
                if not page.get("next_cursor"):
                    return json.loads(output)
                continue
            status = self.call("exec", "get", {"exec_id": identity})["data"]
            if status["state"] in ("failed", "outcome_unknown"):
                raise RuntimeError("exec " + status["state"])
            if status["state"] == "exited":
                if status.get("exit_code") != 0:
                    raise RuntimeError("workload exited unsuccessfully")
                exit_observed = True
            time.sleep(.001)
        raise TimeoutError("independent useful command")


def token_from_environment():
    return os.environ.get("LAMP_BENCH_TOKEN")
