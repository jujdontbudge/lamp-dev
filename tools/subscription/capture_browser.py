#!/usr/bin/python3
import json
import os
from pathlib import Path
import sys
from urllib.parse import parse_qsl, urlencode, urlsplit


def main():
    if len(sys.argv) != 2:
        return 1
    url = sys.argv[1]
    parsed = urlsplit(url)
    if parsed.scheme != "https" or parsed.hostname not in ("claude.ai", "claude.com", "platform.claude.com", "console.anthropic.com"):
        return 1
    query = [
        (key, "https://platform.claude.com/oauth/code/callback" if key == "redirect_uri" and urlsplit(value).hostname in ("localhost", "127.0.0.1", "::1") else value)
        for key, value in parse_qsl(parsed.query, keep_blank_values=True)
    ]
    url = parsed._replace(query=urlencode(query)).geturl()
    path = Path("/home/lamp-private/challenge.json")
    descriptor = os.open(path, os.O_CREAT | os.O_TRUNC | os.O_WRONLY, 0o600)
    with os.fdopen(descriptor, "w") as output:
        json.dump({"type": "claude_code", "authUrl": url}, output)
        output.flush()
        os.fsync(output.fileno())
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
