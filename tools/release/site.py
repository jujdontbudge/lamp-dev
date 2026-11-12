#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Build the lamp.eilmer.si site that GitLab Pages serves from the public project.

    python3 tools/release/site.py --out public --version 0.1.1 --packages URL

It serves three things:
  /install.sh and /releases/v<version>/...  the release files, fetched from the
      project's generic package registry (URL/<version>/<file>) and checked
      against that release's SHA256SUMS before they are published;
  go-import pages for the Go module lamp.eilmer.si and each of its packages,
      pointing at https://gitlab.com/johansardar/lamp.git;
  a small index page.
"""
import argparse
import hashlib
import html
import os
import shutil
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MODULE = 'lamp.eilmer.si'
REPO = 'https://gitlab.com/johansardar/lamp'
SKIP = {'.git', 'target', 'testdata', 'node_modules', '.tools', 'public'}


def fetch(url):
    token = os.environ.get('CI_JOB_TOKEN')  # lets the job read a private project's packages
    req = urllib.request.Request(url, headers={'JOB-TOKEN': token} if token else {})
    with urllib.request.urlopen(req, timeout=300) as r:
        return r.read()


def page(path, version):
    meta = (f'<meta name="go-import" content="{MODULE} git {REPO}.git">\n'
            f'<meta name="go-source" content="{MODULE} {REPO} {REPO}/-/tree/main{{/dir}} {REPO}/-/blob/main{{/dir}}/{{file}}#L{{line}}">')
    title = html.escape(f'{MODULE}{path}')
    return f'''<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width">
{meta}
<title>{title}</title></head>
<body style="font-family:system-ui,sans-serif;max-width:40rem;margin:2rem auto;padding:0 1rem">
<h1>Lamp</h1>
<p>Fast, isolated sandboxes for agents, harnesses and CI on your own machines.</p>
<pre>curl -fsSL https://{MODULE}/install.sh | sh</pre>
<p><a href="{REPO}">Source on GitLab</a> &middot; <a href="{REPO}/-/releases/v{version}">Release {version}</a>
&middot; <a href="/releases/v{version}/SHA256SUMS">SHA256SUMS</a></p>
</body></html>
'''


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--out', required=True, type=Path)
    ap.add_argument('--version', required=True)
    ap.add_argument('--packages', required=True, help='generic package registry URL of the lamp package')
    a = ap.parse_args()
    out = a.out
    if out.exists():
        shutil.rmtree(out)
    rel = out / 'releases' / f'v{a.version}'
    rel.mkdir(parents=True)
    base = f'{a.packages}/{a.version}'
    sums = fetch(f'{base}/SHA256SUMS')
    (rel / 'SHA256SUMS').write_bytes(sums)
    for line in sums.decode().splitlines():
        digest, name = line.split()
        data = fetch(f'{base}/{name}')
        if hashlib.sha256(data).hexdigest() != digest:
            raise SystemExit(f'{name}: digest mismatch, not publishing')
        (rel / name).write_bytes(data)
        print(f'{name}: ok')
    shutil.copy2(rel / 'install.sh', out / 'install.sh')

    dirs = {Path('.')}
    for f in ROOT.rglob('*.go'):
        r = f.relative_to(ROOT)
        if not SKIP.intersection(r.parts) and not f.name.endswith('_test.go'):
            dirs.add(r.parent)
    for d in sorted(dirs):
        sub = '' if d == Path('.') else '/' + d.as_posix()
        (out / d).mkdir(parents=True, exist_ok=True)
        (out / d / 'index.html').write_text(page(sub, a.version))
    print(f'site: {len(dirs)} go-import pages, release {a.version}')


if __name__ == '__main__':
    main()
