#!/usr/bin/env python3
"""Trusted GitHub job -> proof-bound Lamp key, with no stored Lamp secret.
Requires Python, OpenSSL, id-token: write and an enrolled private workflow.
Writes the transient bearer to an exclusive mode-0600 file; prints no credential.
Public repository and pull-request identities are rejected by Lamp, not handed
this helper or a Lamp key by the runner controller.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import uuid


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api-url", required=True)
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    origin = urllib.parse.urlsplit(args.api_url)
    if origin.scheme != "https" or origin.username or origin.password or origin.query or origin.fragment:
        raise RuntimeError("Lamp needs an authenticated HTTPS origin")
    endpoint = args.api_url.rstrip("/")
    opener = urllib.request.build_opener(NoRedirect)

    def request(url, body=None, headers=None):
        req = urllib.request.Request(url, None if body is None else json.dumps(body).encode(), headers or {})
        req.add_header("Content-Type", "application/json")
        # Never include vendor errors, URLs or request bodies in logs.
        try:
            with opener.open(req, timeout=30) as response:
                return json.load(response)
        except (urllib.error.URLError, ValueError):
            raise RuntimeError("CI identity request rejected or unavailable") from None

    with tempfile.TemporaryDirectory(prefix="lamp-ci-proof-") as tmp:
        private = Path(tmp) / "proof.pem"
        env = {"PATH": os.environ.get("PATH", "/usr/bin:/bin")}

        def openssl(*argv):
            result = subprocess.run(["openssl", *argv], capture_output=True, env=env)
            if result.returncode:
                raise RuntimeError("proof-key operation failed")
            return result.stdout

        openssl("genpkey", "-algorithm", "ED25519", "-out", str(private))
        private.chmod(0o600)
        der = openssl("pkey", "-in", str(private), "-pubout", "-outform", "DER")
        prefix = bytes.fromhex("302a300506032b6570032100")
        if not der.startswith(prefix) or len(der) != len(prefix) + 32:
            raise RuntimeError("invalid Ed25519 public key")
        public = der[len(prefix):]
        source = {"issuer": "https://token.actions.githubusercontent.com"}
        for key, variable in {
            "repository_id": "GITHUB_REPOSITORY_ID",
            "repository_owner_id": "GITHUB_REPOSITORY_OWNER_ID",
            "workflow_ref": "GITHUB_WORKFLOW_REF",
            "workflow_sha": "GITHUB_WORKFLOW_SHA",
            "ref": "GITHUB_REF", "sha": "GITHUB_SHA", "event": "GITHUB_EVENT_NAME",
        }.items():
            source[key] = os.environ[variable]
        flow = request(endpoint + "/v1/auth-flows:bootstrap", {
            "fleet_id": "fl_local", "purpose": "ci_exchange",
            "proof_key_thumbprint": hashlib.sha256(public).hexdigest(), "source_identity": source,
        })
        forge_url = urllib.parse.urlsplit(os.environ["ACTIONS_ID_TOKEN_REQUEST_URL"])
        if forge_url.scheme != "https" or not forge_url.hostname.endswith(".actions.githubusercontent.com") or forge_url.username or forge_url.password:
            raise RuntimeError("unexpected GitHub identity endpoint")
        query = urllib.parse.parse_qsl(forge_url.query, keep_blank_values=True)
        query = [(k, v) for k, v in query if k != "audience"] + [("audience", flow["audience"])]
        url = urllib.parse.urlunsplit(forge_url._replace(query=urllib.parse.urlencode(query)))
        jwt = request(url, headers={"Authorization": "Bearer " + os.environ["ACTIONS_ID_TOKEN_REQUEST_TOKEN"]})["value"]
        proof = json.dumps({"auth_flow_id": flow["auth_flow_id"], "challenge": flow["challenge"],
                            "jwt_sha256": hashlib.sha256(jwt.encode()).hexdigest(), "audience": flow["audience"]},
                           sort_keys=True, separators=(",", ":")).encode()
        message = Path(tmp) / "message.json"
        message.write_bytes(proof)
        signature = openssl("pkeyutl", "-sign", "-rawin", "-inkey", str(private), "-in", str(message))
        result = request(endpoint + "/v1/auth-flows/" + flow["auth_flow_id"] + ":exchange", {
            "jwt": jwt, "audience": flow["audience"],
            "proof": {"public_key": base64.b64encode(public).decode(), "signature": base64.b64encode(signature).decode()},
        }, {"Idempotency-Key": str(uuid.uuid4()), "If-Match": "1"})
        fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, "w") as output:
            output.write(result["result"]["secret"] + "\n")


if __name__ == "__main__":
    try:
        main()
    except Exception:
        raise SystemExit("Lamp CI identity exchange failed (no credentials were printed)") from None
