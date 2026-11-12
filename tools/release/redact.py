#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Redact private machine details from the tracked tree before publication.

The patterns live in a private file outside the repository (a JSON object with
"replace": [[old, new], ...] and "regex": [[pattern, new], ...]), so this tool
never publishes what it removes. `--check` only reports remaining matches.

    python3 tools/release/redact.py --patterns ~/.config/lamp/release/redactions.json [--check]
"""
import argparse
import json
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--patterns', required=True)
    ap.add_argument('--check', action='store_true')
    a = ap.parse_args()
    spec = json.loads(Path(a.patterns).expanduser().read_text())
    rules = [(re.compile(re.escape(o)), n) for o, n in spec.get('replace', [])]
    rules += [(re.compile(p), n) for p, n in spec.get('regex', [])]
    files = subprocess.run(['git', 'ls-files', '-z'], cwd=ROOT, capture_output=True, check=True).stdout.split(b'\0')
    changed = remaining = 0
    for name in filter(None, files):
        path = ROOT / name.decode()
        if path.is_symlink() or not path.is_file():
            continue
        raw = path.read_bytes()
        if b'\0' in raw[:8192]:
            continue  # binary; gzip evidence is checked separately by the caller
        try:
            text = raw.decode()
        except UnicodeDecodeError:
            continue
        out = text
        for rx, new in rules:
            out = rx.sub(new, out)
        if out != text:
            if a.check:
                remaining += 1
                print(f'private detail remains: {name.decode()}')
            else:
                path.write_text(out)
                changed += 1
    print(f'{"files with private details" if a.check else "files redacted"}: {remaining if a.check else changed}')
    return 1 if remaining else 0


if __name__ == '__main__':
    sys.exit(main())
