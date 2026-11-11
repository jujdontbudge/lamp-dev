#!/usr/bin/env python3
"""Read-only root helper for an enrolled supervisor's actual CI attachment.
Install root-owned with the audit root fixed in the sudoers argv, for example:
  ... /usr/local/lib/lamp/ci-network-attestation /var/lib/lamp-audit/ci
The controller appends only a sandbox ID and generation. No guest paths or
network mutations, and no owner-wide sudo permission is needed in deployment.
"""
import json
import os
from pathlib import Path
import re
import sys

root, sandbox, generation = sys.argv[1:]
if not re.fullmatch(r"sb_[a-f0-9]{32}", sandbox) or not re.fullmatch(r"[0-9]+", generation):
    raise SystemExit("invalid attachment identity")
paths = list(Path(root).glob(f"{sandbox}-g{generation}-*/network.json"))
if len(paths) != 1:
    raise SystemExit("attachment identity is not unique")
p = paths[0]
st = p.lstat()
if st.st_uid != 0 or st.st_mode & 0o022 or p.is_symlink():
    raise SystemExit("attachment is not root-owned immutable configuration")
config = json.loads(p.read_text())
attr = config.get("attribution", {})
if attr.get("sandbox_id") != sandbox or str(attr.get("generation")) != generation:
    raise SystemExit("attachment attribution mismatch")
# The full network config contains paths and attribution, not transport tokens.
print(json.dumps(config))
