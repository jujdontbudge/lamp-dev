"""Manual Sol S1 probe: actual --job client; only root and timeout substituted."""
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
import time

source = Path('internal/control/sandbox_kits.py').read_text()
spec = importlib.util.spec_from_file_location('kits', 'internal/control/sandbox_kits.py')
kits = importlib.util.module_from_spec(spec)
spec.loader.exec_module(kits)
with tempfile.TemporaryDirectory(prefix='t44-relay-', dir='/tmp') as directory:
    root = Path(directory)
    kits.ROOT = root
    endpoint = str(root / 'runtime.sock')
    started, release, finished = (threading.Event() for _ in range(3))
    runtime = kits.Runtime()
    runtime.contexts['default'] = dict(started=started, release=release, finished=finished)
    threading.Thread(target=kits.serve, args=(endpoint, runtime), daemon=True).start()
    deadline = time.monotonic() + 3
    while not Path(endpoint).exists():
        assert time.monotonic() < deadline, 'runtime listener did not start'
        time.sleep(.001)
    client = root / 'client.py'
    client.write_text(source.replace("Path('/tmp/lamp-sandbox-kits')", 'Path(' + repr(directory) + ')').replace('settimeout(1100)', 'settimeout(0.05)'))
    (root / 'probe.request').write_text(json.dumps(dict(component='run_code', code='started.set(); release.wait(); finished.set()')))
    try:
        subprocess.run([sys.executable, str(client), '--job', 'probe'], check=True, timeout=3)
        result = json.loads((root / 'probe.result').read_text())
        still_running = started.is_set() and not finished.is_set()
        assert result.get('outcome_unknown') is True, result
        assert still_running, 'runtime was not executing when client timed out'
    finally:
        release.set()
    assert finished.wait(1), 'runtime did not complete after client timed out'
    print(json.dumps(dict(job_result=result, runtime_still_running_at_timeout=still_running,
                         effect_completed_after_timeout=finished.is_set())))
