#!/usr/bin/env python3
"""Trusted guard-spool release for one deleted sandbox (root-only).

  guard-audit-release.py AUDIT_ROOT capture SANDBOX_ID   # print its records
  guard-audit-release.py AUDIT_ROOT remove  SANDBOX_ID   # delete its spools

lampd runs `capture`, stores the records durably, then runs `remove`. Run
through `sudo -n` with a fixed AUDIT_ROOT, e.g.
  --audit-release '["sudo","-n","/usr/local/lib/lamp/guard-audit-release","/var/lib/lamp-audit/ci"]'
"""
import os, re, shutil, sys

root, action, sandbox = sys.argv[1:4]
if action not in ("capture", "remove") or not re.fullmatch(r"sb_[0-9a-f]{32}", sandbox):
    raise SystemExit(2)
pattern = re.compile(re.escape(sandbox) + r"-g\d+-[0-9a-f]{32}")
dirs = sorted(e.path for e in os.scandir(root) if e.is_dir(follow_symlinks=False) and pattern.fullmatch(e.name))
out = sys.stdout.buffer
for d in dirs:
    if action == "remove":
        shutil.rmtree(d)
        continue
    for name in sorted(os.listdir(d)):
        path = os.path.join(d, name)
        if name.endswith(".ndjson") and os.path.isfile(path) and not os.path.islink(path):
            with open(path, "rb") as f:
                for line in f:
                    if line.strip():
                        out.write(line if line.endswith(b"\n") else line + b"\n")
