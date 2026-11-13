"""Emit public exec input for the current Browser implementation in a kit VM.

python3 cold-browser-probe.py internal/control/sandbox_kits.py SANDBOX_ID > /tmp/cold-exec.json
lamp exec create --input /tmp/cold-exec.json --wait-ms 60000 --json
Then use exec get/watch and sandbox file-read for the returned PNG path.
"""
import base64
import json
from pathlib import Path
import sys

source = base64.b64encode(Path(sys.argv[1]).read_bytes()).decode()
code = ("import base64,json; ns={'__name__':'t44_cold_browser'}; "
        "exec(compile(base64.b64decode(" + repr(source) + "),'<branch-kits>','exec'),ns); "
        "b=ns['Browser'](); "
        "print(json.dumps(b.call({'action':'navigate','url':'https://example.com'})),flush=True); "
        "print(json.dumps(b.call({'action':'screenshot'})),flush=True); "
        "b.call({'action':'close'})")
print(json.dumps({'sandbox_id': sys.argv[2],
                  'argv': ['/tmp/lamp-sandbox-kits/playwright/bin/python', '-c', code],
                  'timeout_ms': 600000}))
