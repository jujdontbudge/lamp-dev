#!/usr/bin/env python3
"""Full real-engine E03, invoked only through timing-run.sh on LX.

Build binaries and prepare the template before invoking this script. Sandbox
provisioning/cleanup is outside the per-command timers. Private bearer files
are kept in a separate 0700 directory and must never be published as evidence.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import signal
import subprocess
import sys
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', required=True)
    parser.add_argument('--bin-dir', required=True)
    parser.add_argument('--template', required=True)
    parser.add_argument('--integrated-e01', required=True)
    parser.add_argument('--worker-socket', default='/run/lamp/worker.sock')
    parser.add_argument('--worker-binary', default='/usr/local/lib/lamp/lamp-supervisor')
    parser.add_argument('--worker-store', default='/var/lib/lamp')
    parser.add_argument('--worker-pid', type=int, help='explicit private supervisor PID; otherwise systemd MainPID')
    parser.add_argument('--policy', default='/etc/lamp/network.json')
    parser.add_argument('--profile', action='store_true', help='diagnostic syscall trace, never qualification')
    args = parser.parse_args()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700, parents=True, exist_ok=False)
    private = root / 'private'
    private.mkdir(mode=0o700)
    source = Path(__file__).resolve().parents[2]
    sys.path.insert(0, str(source))
    from benchmarks.public import PublicLamp
    from benchmarks.control import control_suite
    from benchmarks.evidence import Evidence

    def capture(argv):
        return subprocess.check_output(argv, text=True).strip()

    grant = json.loads(capture(['sudo', '-n', args.worker_binary, 'grant-install', '--store', args.worker_store, '--caller-uid', str(os.getuid()), '--ttl-s', '7200']))
    (root / 'grant.json').write_text(json.dumps(grant))
    owner = secrets.token_hex(32)
    enrollment = private / 'identity.json'
    subjects = [secrets.token_hex(32) for _ in range(2)]
    enrollment.write_text(json.dumps(dict(owner_id='pr_e03_owner', bootstrap_sha256=hashlib.sha256(owner.encode()).hexdigest(),
        subjects=[dict(issuer='e03-independent', subject=f'owner-{i}', sha256=hashlib.sha256(token.encode()).hexdigest()) for i, token in enumerate(subjects)])))
    enrollment.chmod(0o600)
    socket = str(private / 'api.sock')
    binary = Path(args.bin_dir).resolve()
    daemon_log = (root / 'lampd.log').open('x')
    command = [str(binary / 'lampd'), '--identity-file', str(enrollment), '--state-dir', str(private / 'state'), '--socket', socket,
               '--worker-socket', args.worker_socket, '--worker-id', 'lamp-supervisor@' + capture(['hostname']),
               '--worker-boot-id', Path('/proc/sys/kernel/random/boot_id').read_text().strip(), '--worker-uid', '0',
               '--worker-pid', str(args.worker_pid) if args.worker_pid is not None else capture(['systemctl', 'show', '-p', 'MainPID', '--value', 'lamp-supervisor']), '--grant-id', grant['grant_id'], '--worker-timeout', '60s']
    if args.profile:
        command += ['--profile-dir', str(root / 'profiles')]
    daemon = subprocess.Popen(command, stdout=daemon_log, stderr=daemon_log)
    callers, tokens, created, cleanup = [], [], [], []
    tracer = None

    def cli(token, noun, action, fields):
        path = private / 'input.json'
        path.write_text(json.dumps(fields))
        env = dict(os.environ, LAMP_TOKEN=token, LAMP_SOCKET=socket)
        reply = subprocess.run([str(binary / 'lamp'), noun, action, '--input', str(path), '--json'], env=env, capture_output=True, text=True, timeout=120)
        if reply.returncode:
            raise RuntimeError(reply.stderr)
        return json.loads(reply.stdout)

    try:
        until = time.monotonic() + 30
        while not Path(socket).exists():
            if daemon.poll() is not None or time.monotonic() > until:
                raise RuntimeError('daemon did not become ready')
            time.sleep(.01)
        cli(owner, 'fleet', 'update', dict(fleet_id='fl_local', mode='team', expected_revision=1, idempotency_key='mode'))
        principals, sandboxes = {}, {}
        for i in range(32):
            owner_index = i % 2
            if owner_index not in principals:
                principal = cli(owner, 'principal', 'create', dict(issuer='e03-independent', subject=f'owner-{owner_index}', idempotency_key=f'principal-{owner_index}'))['result']['principal_id']
                cli(owner, 'membership', 'create', dict(principal_id=principal, role='member', idempotency_key=f'membership-{owner_index}'))
                principals[owner_index] = principal
            principal = principals[owner_index]
            expiry = time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime(time.time() + 7200))
            token = cli(subjects[owner_index], 'key', 'create', dict(principal_id=principal, expires_at=expiry, idempotency_key=f'key-{i}'))['result']['secret']
            tokens.append(token)
            variable = f'LAMP_E03_CALLER_{i}'
            os.environ[variable] = token
            if owner_index not in sandboxes:
                check = subprocess.run([sys.executable, str(source / 'infra/linux/admission-check.py')], capture_output=True, text=True)
                with (root / 'capacity-checks.jsonl').open('a') as checks:
                    checks.write(check.stdout)
                if check.returncode:
                    raise RuntimeError(check.stderr)
                result = cli(token, 'sandbox', 'create', dict(template_id=args.template, wait_ms=60000, idempotency_key='prepare'))
                sb = result['result']['sandbox_id']
                sandboxes[owner_index] = sb
                created.append((token, sb))
            callers.append(dict(principal_id=principal, sandbox_id=sandboxes[owner_index], token_env=variable))
            print(f'prepared credential {i + 1}/32 (two owner sandboxes)', flush=True)
        (root / 'callers.json').write_text(json.dumps(callers, indent=2))
        (private / 'tokens.json').write_text(json.dumps(tokens))
        (private / 'tokens.json').chmod(0o600)
        raw = capture([sys.executable, str(source / 'infra/linux/run-metadata.py'), str(source), '/var/tmp', args.template, '--store', args.worker_store, '--worker-binary', args.worker_binary, '--policy', args.policy])
        metadata = json.loads(raw)
        metadata['artifacts'] = {k: v for k, v in metadata['artifacts'].items() if not k.startswith(str(Path.home() / 'lamp-t6/bin'))}
        for name in ('lamp', 'lampd'):
            metadata['artifacts'][str(binary / name)] = hashlib.sha256((binary / name).read_bytes()).hexdigest()
        metadata['hardware']['cpu_core_assignment'] = dict(control=sorted(os.sched_getaffinity(daemon.pid)), observer=sorted(os.sched_getaffinity(0)), guests='scheduler-placed; 2 guests, 2 vCPUs each; 16 credentialed callers per owner')
        metadata['durability'] = PublicLamp(socket_path=socket, token=tokens[0]).get(callers[0]['sandbox_id'])['durability']
        metadata['access_route'] = 'local authenticated public HTTP Unix socket -> lampd -> lamp.worker/1 -> pinned real Firecracker supervisor'
        (root / 'metadata.json').write_text(json.dumps(metadata, indent=2))
        evidence = Evidence(root / 'run', metadata, dict(suite='E03', template=args.template, callers=32, rounds=200, engine='real Firecracker', caller_runtime='32 Linux processes', socket=socket))
        if args.profile:
            tracer = subprocess.Popen(['sudo', '-n', 'strace', '-f', '-T', '-e', 'trace=fdatasync,fsync', '-p', str(daemon.pid), '-o', str(root / 'control-sync.trace')], stderr=(root / 'strace.log').open('x'))
        try:
            result = control_suite(None, None, callers, evidence, args.integrated_e01, socket_path=socket, allocation_observer=[sys.executable, str(source / 'infra/linux/observer.py')], cleanup_prepared=True, process_callers=True)
        except Exception as error:
            evidence.observe('runner_failure', dict(type=type(error).__name__, message=str(error)))
            raise
        finally:
            summary = evidence.finish({'E03': result} if 'result' in locals() and 'control_status' in result else {'runner': {'status': 'fail'}}, diagnostic=args.profile)
        print(json.dumps(summary['results']), flush=True)
        return 0 if summary['status'] == 'pass' else 1
    finally:
        if tracer is not None:
            subprocess.run(['sudo', '-n', 'kill', '-INT', str(tracer.pid)], check=False)
            tracer.wait(timeout=10)
        # A preparation failure can still have an allocated partial resource.
        # Discover all ticket principals' resources before revoking the grant.
        known = {sb for _, sb in created}
        for token in tokens:
            try:
                client = PublicLamp(socket_path=socket, token=token, timeout=120)
                cursor = None
                while True:
                    page = client.call('sandbox', 'list', {'limit': 1000, **({'cursor': cursor} if cursor else {})})
                    for sb in page['items']:
                        if sb['id'] not in known and sb['state'] != 'deleted':
                            created.append((token, sb['id']))
                            known.add(sb['id'])
                    cursor = page.get('next_cursor')
                    if not cursor:
                        break
            except Exception as error:
                cleanup.append(dict(error='partial resource discovery: ' + str(error)))
        for token, sb in reversed(created):
            try:
                client = PublicLamp(socket_path=socket, token=token, timeout=120)
                resource = client.get(sb)
                if resource['state'] != 'deleted':
                    client.delete(sb)
                if client.get(sb)['state'] != 'deleted':
                    raise RuntimeError('cleanup did not confirm release')
            except Exception as error:
                cleanup.append(dict(sandbox_id=sb, error=str(error)))
        (root / 'cleanup.json').write_text(json.dumps(cleanup, indent=2))
        daemon.send_signal(signal.SIGTERM)
        try:
            daemon.wait(timeout=70)
        except subprocess.TimeoutExpired:
            daemon.kill()
            daemon.wait()
        daemon_log.close()
        subprocess.run(['sudo', '-n', args.worker_binary, 'grant-revoke', '--store', args.worker_store, grant['grant_id']], check=True)


if __name__ == '__main__':
    raise SystemExit(main())
