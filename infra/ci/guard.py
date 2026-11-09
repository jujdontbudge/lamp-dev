#!/usr/bin/env python3
"""Installed outside checkout. Admit only signed owner/private Lamp jobs.

The runner invokes this before job steps. Job YAML cannot replace its process-
level hook path. Missing OIDC, bad signatures and all network errors fail closed.
"""
import json
import os
from pathlib import Path
import sys
import signal
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

REPOSITORY = "JohanSardar/lamp"
REPOSITORY_ID = "1450818687"
OWNER_ID = "68117503"
OWNER = "JohanSardar"
ISSUER = "https://token.actions.githubusercontent.com"
AUDIENCE = "lamp-ci-private/v1"


def authorize(claims, env, publicly_visible):
    required = {
        "repository": REPOSITORY,
        "repository_id": REPOSITORY_ID,
        "repository_owner_id": OWNER_ID,
        "repository_visibility": "private",
        "actor_id": OWNER_ID,
    }
    for key, value in required.items():
        if claims.get(key) != value:
            raise ValueError("unauthorized " + key)
    event = claims.get("event_name")
    if event not in {"push", "workflow_dispatch"}:
        raise ValueError("pull-request and other event types are forbidden")
    ref = claims.get("ref", "")
    if ref != "refs/heads/main" and not ref.startswith("refs/heads/t3/"):
        raise ValueError("unapproved branch")
    if claims.get("workflow_ref") != REPOSITORY + "/.github/workflows/check.yml@" + ref:
        raise ValueError("unapproved workflow")
    if not claims.get("sha") or not claims.get("run_id"):
        raise ValueError("missing signed job identity")
    for key, claim in [("GITHUB_REPOSITORY", "repository"), ("GITHUB_REF", "ref"),
                       ("GITHUB_SHA", "sha"), ("GITHUB_RUN_ID", "run_id"),
                       ("GITHUB_EVENT_NAME", "event_name"), ("GITHUB_ACTOR_ID", "actor_id")]:
        if env.get(key) != claims.get(claim):
            raise ValueError("runner context differs from signed identity")
    if env.get("GITHUB_TRIGGERING_ACTOR") != OWNER:
        raise ValueError("rerun must be initiated by owner")
    if publicly_visible:
        raise ValueError("public jobs require a fresh VM; persistent runners are disabled")


def read_json(url, token=None):
    headers = {"User-Agent": "lamp-private-runner-guard", "Accept": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    request = urllib.request.Request(url, headers=headers)
    # No caller-controlled redirects: do not send a job token to another origin.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    with urllib.request.build_opener(NoRedirect).open(request, timeout=15) as response:
        data = response.read(256 * 1024 + 1)
        if len(data) > 256 * 1024:
            raise ValueError("oversized identity response")
        return json.loads(data)


def deny_job():
    """Kill only this hook's Worker; hook failure alone permits always() steps.

    Walk parents, never search the host for workers or signal the Listener.
    No workflow process has started when the runner invokes this hook.
    """
    pid = os.getppid()
    try:
        seen = set()
        while pid > 1 and pid not in seen:
            seen.add(pid)
            row = subprocess.check_output(
                ['/bin/ps', '-p', str(pid), '-o', 'ppid=', '-o', 'comm='],
                text=True, timeout=5).strip().split(None, 1)
            parent, command = int(row[0]), row[1]
            if Path(command).name == 'Runner.Worker':
                os.kill(pid, signal.SIGKILL)
                print('Lamp host guard: terminated denied Runner.Worker ' + str(pid),
                      file=sys.stderr, flush=True)
                return
            pid = parent
    except (OSError, ValueError, IndexError, subprocess.SubprocessError):
        pass
    # Never turn a process-discovery error into a completed failed hook.
    # Stop here for operator intervention; even SIGINT/SIGTERM must not cause
    # a normal hook return. Production installation/probes verify ancestry.
    print('Lamp host guard: cannot terminate Worker; holding hook for operator intervention',
          file=sys.stderr, flush=True)
    signal.signal(signal.SIGTERM, signal.SIG_IGN)
    signal.signal(signal.SIGINT, signal.SIG_IGN)
    while True:
        time.sleep(3600)


def main():
    prefix = Path(__file__).resolve().parent
    if (prefix / "PAUSED").exists():
        raise ValueError("runner is paused for host work or timing")
    # These endpoint variables are installed by the runner's ScriptHandler,
    # after workflow environment evaluation, not supplied by repository code.
    endpoint = os.environ["ACTIONS_ID_TOKEN_REQUEST_URL"]
    parsed = urllib.parse.urlsplit(endpoint)
    if parsed.scheme != "https" or not parsed.hostname or not parsed.hostname.endswith(".actions.githubusercontent.com"):
        raise ValueError("untrusted OIDC request endpoint")
    query = urllib.parse.parse_qsl(parsed.query)
    query = [(k, v) for k, v in query if k != "audience"] + [("audience", AUDIENCE)]
    endpoint = urllib.parse.urlunsplit(parsed._replace(query=urllib.parse.urlencode(query)))
    token = read_json(endpoint, os.environ["ACTIONS_ID_TOKEN_REQUEST_TOKEN"])["value"]
    import jwt
    key = jwt.PyJWKClient(ISSUER + "/.well-known/jwks", timeout=15).get_signing_key_from_jwt(token)
    claims = jwt.decode(token, key.key, algorithms=["RS256"], audience=AUDIENCE, issuer=ISSUER,
                        options={"require": ["exp", "iat", "nbf", "iss", "aud", "repository_id", "run_id"]})
    # A signed private claim is required even when the public API returns 404.
    # 403/rate limit, server error, timeout, redirect or malformed JSON deny.
    try:
        repo = read_json("https://api.github.com/repos/" + REPOSITORY)
        if str(repo.get("id")) != REPOSITORY_ID:
            raise ValueError("repository identity changed")
        publicly_visible = repo.get("private") is not True
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        publicly_visible = False
    authorize(claims, os.environ, publicly_visible)
    print("Lamp host guard: admitted signed owner/private " + claims["event_name"] + " run " + claims["run_id"])


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        # Never print the JWT, request URL, job token, payload or raw HTTP error.
        print("Lamp host guard: DENIED (" + type(error).__name__ + "); no job steps authorized", file=sys.stderr, flush=True)
        deny_job()
        sys.exit(1)
