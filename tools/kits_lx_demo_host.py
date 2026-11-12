#!/usr/bin/env python3
"""Hold an isolated LX supervisor/control service for the single T22 demo.

Uses an already prepared disposable seed/store. Never manages owner services or
Docker. SIGTERM the recorded host.pid after the M5 demonstration to shut down.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import threading
import time


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', required=True)
    p.add_argument('--store', required=True)
    p.add_argument('--seed', required=True)
    p.add_argument('--lampd', required=True)
    p.add_argument('--template', help='reuse an already prepared template instead of building again')
    p.add_argument('--kits-config', help='disposable native vault configuration for bundled Lamp kits')
    args = p.parse_args()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700)
    (root / 'host.pid').write_text(str(os.getpid()))
    worker_bin = str(Path(args.store) / 'bin/lamp-supervisor')
    policy = Path(args.store) / 'network.json'
    config = {'guard': str(Path(args.store) / 'bin/lamp-guard'), 'pasta': '/usr/bin/pasta',
              'audit_dir': '/var/lib/lamp-audit/t22-kits', 'revision': 22,
              'principal_id': 'pr_t22_demo', 'exceptions': []}
    subprocess.run(['sudo', '-n', 'tee', str(policy)], input=json.dumps(config).encode(), stdout=subprocess.DEVNULL, check=True)
    from kits.service import admission
    common = ['--store', args.store, '--network-policy', str(policy),
        '--mem-dir', '/run/lamp-t22/mem', '--netns-dir', '/run/lamp-t22/netns',
        '--uid-base', '1850000000', '--host-id', 'hs_t22']
    if args.template:
        prepared = json.dumps({'template_id': args.template, 'reused_prepared_template': True})
    else:
        seed_ref = subprocess.check_output(['sudo', '-n', worker_bin, 'seed-import', args.seed] + common, text=True).strip()
        recipe = root / 'template.lamp'
        recipe.write_text('format = "lamp.template/v1"\nname = "t22-kits"\nguest_os = "linux"\narch = "x86_64"\nseed_ref = "' + seed_ref + '"\nnetwork = "none"\n\n[[steps]]\nargv = ["python3", "-c", "import ssl, pathlib"]\n')
        print(json.dumps({'template_admission': admission()}), flush=True)
        prepared = subprocess.check_output(['sudo', '-n', worker_bin, 'template-build', str(recipe)] + common, text=True)
    (root / 'template.json').write_text(prepared)
    worker, daemon, grant, expiry = None, None, None, None
    def ready(path, process):
        while not Path(path).exists():
            if process.poll() is not None:
                raise RuntimeError('owned demo service exited before becoming ready')
            time.sleep(.1)
    def interrupted(*_):
        raise KeyboardInterrupt()
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGHUP, interrupted)
    try:
        worker_log = (root / 'worker.log').open('x')
        worker_pid = root / 'worker.pid'
        worker = subprocess.Popen(['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', str(worker_pid),
            worker_bin, 'serve', '--store', args.store, '--socket', str(root / 'worker.sock'),
            '--network-policy', str(policy), '--mem-dir', '/run/lamp-t22/mem',
            '--netns-dir', '/run/lamp-t22/netns', '--uid-base', '1850000000',
            '--host-id', 'hs_t22', '--allow-uid', str(os.getuid())], stdout=worker_log, stderr=worker_log)
        ready(root / 'worker.sock', worker)
        grant = json.loads(subprocess.check_output(['sudo', '-n', worker_bin, 'grant-install', '--store', args.store,
                            '--caller-uid', str(os.getuid()), '--ttl-s', '3600'], text=True))
        # The fixture must not outlive the authority needed to clean it up.
        # This follows the existing grant expiry, not a workload timing target.
        expiry = threading.Timer(max(0, grant['expires_at_unix_ms'] / 1000 - time.time()),
                                 lambda: os.kill(os.getpid(), signal.SIGTERM))
        expiry.daemon = True
        expiry.start()
        token = secrets.token_hex(32)
        (root / 'token').write_text(token)
        (root / 'token').chmod(0o600)
        identity = root / 'identity.json'
        identity.write_text(json.dumps({'owner_id': 'pr_t22_demo', 'bootstrap_sha256': hashlib.sha256(token.encode()).hexdigest()}))
        identity.chmod(0o600)
        managed_kits = []
        if args.kits_config:
            kit_config = json.loads(Path(args.kits_config).read_text())
            kit_config['control_socket'] = str(root / 'api.sock')
            kit_config['control_token_file'] = str(root / 'token')
            kit_config['browser_template'] = json.loads(prepared)['template_id']
            private_config = root / 'kits.json'
            private_config.write_text(json.dumps(kit_config)); private_config.chmod(0o600)
            managed_kits = ['--kits-config', str(private_config)]
        daemon_log = (root / 'lampd.log').open('x')
        daemon = subprocess.Popen([args.lampd, '--identity-file', str(identity), '--state-dir', str(root / 'control'),
            '--socket', str(root / 'api.sock'), '--worker-socket', str(root / 'worker.sock'),
            '--worker-id', 'lamp-supervisor@hs_t22', '--worker-boot-id', Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
            '--worker-uid', '0', '--worker-pid', worker_pid.read_text().strip(),
            '--grant-id', grant['grant_id'], '--worker-timeout', '60s'] + managed_kits, stdout=daemon_log, stderr=daemon_log)
        ready(root / 'api.sock', daemon)
        print('T22 isolated public API ready', flush=True)
        while True:
            signal.pause()
    except KeyboardInterrupt:
        pass
    finally:
        if expiry:
            expiry.cancel()
        try:
            if daemon:
                daemon.terminate()
                daemon.wait(timeout=70)
        finally:
            try:
                if grant:
                    subprocess.run(['sudo', '-n', worker_bin, 'grant-revoke', '--store', args.store, grant['grant_id']], stdout=subprocess.DEVNULL, check=True)
            finally:
                if worker and worker.poll() is None:
                    subprocess.run(['sudo', '-n', 'kill', '-TERM', str(worker.pid)], check=True)
                    worker.wait(timeout=70)
        print('T22 isolated services stopped', flush=True)


if __name__ == '__main__':
    main()
