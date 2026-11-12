#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Redact private machine details from a tree before publication.

The patterns live in a private file outside the repository (a JSON object with
"replace": [[old, new], ...] and "regex": [[pattern, new], ...]), so this tool
never publishes what it removes. It acts only on the files tracked (or staged)
in the git repository at --root.

Text files are rewritten in place. Gzip files (including pprof profiles) are
decompressed, redacted and recompressed deterministically. A match in any other
binary, or in a file that cannot be read, is an error: it is never skipped.
`--check` changes nothing and fails on any match anywhere, gzip contents included.

    python3 tools/release/redact.py --root EXPORT --patterns PRIVATE.json [--check]
"""
import argparse
import gzip
import io
import json
import re
import subprocess
import sys
from pathlib import Path


def load(path):
    spec = json.loads(Path(path).expanduser().read_text())
    rules = [(re.compile(re.escape(o).encode()), n.encode()) for o, n in spec.get('replace', [])]
    rules += [(re.compile(p.encode()), n.encode()) for p, n in spec.get('regex', [])]
    return rules


def apply(rules, raw):
    out = raw
    for rx, new in rules:
        out = rx.sub(new, out)
    return out


def regzip(data):
    buf = io.BytesIO()
    with gzip.GzipFile(fileobj=buf, mode='wb', mtime=0, filename='') as g:
        g.write(data)
    return buf.getvalue()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--root', required=True, type=Path, help='git repository whose tracked files are redacted')
    ap.add_argument('--patterns', required=True)
    ap.add_argument('--check', action='store_true')
    a = ap.parse_args()
    rules = load(a.patterns)
    root = a.root.resolve()
    files = subprocess.run(['git', '-C', str(root), 'ls-files', '-z', '--cached'], capture_output=True, check=True).stdout.split(b'\0')
    if not any(files):
        print(f'no tracked files under {root}; stage the tree first', file=sys.stderr)
        return 2
    changed, bad = 0, []
    for name in filter(None, files):
        rel = name.decode()
        path = root / rel
        if path.is_symlink() or not path.is_file():
            continue
        try:
            raw = path.read_bytes()
        except OSError:
            bad.append(f'unreadable: {rel}')
            continue
        if raw[:2] == b'\x1f\x8b':
            try:
                inner = gzip.decompress(raw)
            except Exception:
                if apply(rules, raw) != raw:
                    bad.append(f'corrupt gzip with private detail: {rel}')
                continue
            new = apply(rules, inner)
            if new != inner:
                if a.check:
                    bad.append(f'private detail in gzip: {rel}')
                else:
                    path.write_bytes(regzip(new))
                    changed += 1
            continue
        new = apply(rules, raw)
        if new == raw:
            continue
        if a.check:
            bad.append(f'private detail: {rel}')
        elif b'\0' in raw[:8192]:
            bad.append(f'binary file with private detail, cannot redact: {rel}')
        else:
            path.write_bytes(new)
            changed += 1
    for b in bad:
        print(b)
    if not a.check:
        print(f'files redacted: {changed}')
    print(f'files with private details: {len(bad)}')
    return 1 if bad else 0


if __name__ == '__main__':
    sys.exit(main())
