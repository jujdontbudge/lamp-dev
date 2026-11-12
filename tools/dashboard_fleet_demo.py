#!/usr/bin/env python3
"""T27 two-host dashboard demonstration (LX, disposable state).

Two lampd hosts form one enrolled fleet on loopback. hs_a is the compute host
(the ticket-owned Firecracker supervisor), and hs_b is control-only and serves
the dashboard. The owner sees hs_a and its sandbox from hs_b's page. hs_a then
goes to sleep (its lampd is stopped) while it holds a suspended sandbox's saved
state, and hs_b's page shows that state as stranded. Afterwards hs_a wakes and
the sandbox is deleted. Drives the page through dashboard_cdp.mjs
(LAMP_CDP_ENDPOINT).
"""
import argparse, hashlib, http.client, json, os, secrets, signal, socket, subprocess, time
from pathlib import Path

PORTS = dict(hs_a=7550, hs_b=7560)


def main():
    p = argparse.ArgumentParser()
    for flag in ('root', 'store', 'template', 'lampd'):
        p.add_argument('--' + flag, required=True)
    p.add_argument('--port', type=int, default=7480)
    args = p.parse_args()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700)
    shots = root / 'screenshots'
    shots.mkdir()
    transcript = (root / 'transcript.jsonl').open('x')

    def record(step, value):
        transcript.write(json.dumps(dict(step=step, at=time.strftime('%H:%M:%S'), value=value), sort_keys=True) + '\n')
        transcript.flush()
        print(step, flush=True)

    def wait(predicate, what, seconds=90):
        end = time.monotonic() + seconds
        while True:
            try:
                v = predicate()
            except Exception:
                v = None
            if v:
                return v
            if time.monotonic() > end:
                raise RuntimeError('timed out: ' + what)
            time.sleep(.5)

    def admission():
        end = time.monotonic() + 900
        while True:
            live = int(subprocess.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True).stdout.strip() or '0')
            avail = int(next(l for l in subprocess.check_output(['free', '-m'], text=True).splitlines() if l.startswith('Mem:')).split()[-1])
            if live < 2 and avail >= 12288:
                return dict(firecracker=live, available_mib=avail)
            if time.monotonic() > end:
                raise RuntimeError('owner sandbox capacity guard')
            time.sleep(10)

    def quiet(argv):
        subprocess.run(argv, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    # Disposable private CA and one certificate per host (SAN = host id).
    quiet(['openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', str(root / 'ca.key'), '-out', str(root / 'ca.crt'), '-days', '1', '-subj', '/CN=Lamp T27 disposable fleet'])
    for h in PORTS:
        d = root / h
        d.mkdir(mode=0o700)
        (root / (h + '.ext')).write_text(f'subjectAltName=DNS:{h}\nextendedKeyUsage=serverAuth,clientAuth\n')
        quiet(['openssl', 'req', '-new', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', str(d / 'host.key'), '-out', str(root / (h + '.csr')), '-subj', '/CN=' + h])
        quiet(['openssl', 'x509', '-req', '-in', str(root / (h + '.csr')), '-CA', str(root / 'ca.crt'), '-CAkey', str(root / 'ca.key'), '-CAcreateserial', '-out', str(d / 'host.crt'), '-days', '1', '-extfile', str(root / (h + '.ext'))])
        (d / 'host.key').chmod(0o600)

    token = secrets.token_hex(32)
    (root / 'token').write_text(token)
    (root / 'token').chmod(0o600)
    identity = root / 'identity.json'
    identity.write_text(json.dumps(dict(owner_id='pr_t27_demo', bootstrap_sha256=hashlib.sha256(token.encode()).hexdigest(), subjects=[])))
    identity.chmod(0o600)
    manifest = [
        dict(id='hs_a', api=f'https://127.0.0.1:{PORTS["hs_a"]}', raft=f'127.0.0.1:{PORTS["hs_a"] + 1}', os='linux', arch='x86_64',
             engines=['firecracker-1.17.0'], capacity=dict(vcpus=2, memory_mib=4096, disk_gib=20, pids_max=4096)),
        dict(id='hs_b', api=f'https://127.0.0.1:{PORTS["hs_b"]}', raft=f'127.0.0.1:{PORTS["hs_b"] + 1}', os='linux', arch='x86_64',
             engines=[], capacity=dict(vcpus=0, memory_mib=0, disk_gib=0, pids_max=0)),
    ]
    for h in PORTS:
        d = root / h
        (d / 'fleet.json').write_text(json.dumps(dict(host_id=h, bootstrap=h == 'hs_a', hosts=manifest, ca_file=str(root / 'ca.crt'),
                                                      cert_file=str(d / 'host.crt'), key_file=str(d / 'host.key'), guard_dir=str(d / 'guard'),
                                                      public_api_listen=f'127.0.0.1:{PORTS[h] + 2}')))
        (d / 'fleet.json').chmod(0o600)

    def call(host, method, path, body=None, rev=None, ok=True):
        c = http.client.HTTPConnection('lamp', timeout=70)
        c.sock = socket.socket(socket.AF_UNIX)
        c.sock.settimeout(70)
        c.sock.connect(str(root / host / 'api.sock'))
        hd = {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}
        if method != 'GET':
            hd['Idempotency-Key'] = secrets.token_hex(12)
        if rev is not None:
            hd['If-Match'] = str(rev)
        c.request(method, path, None if body is None else json.dumps(body), hd)
        r = c.getresponse()
        status, raw = r.status, r.read()
        c.close()
        out = json.loads(raw) if raw else {}
        if ok and status >= 300:
            raise RuntimeError(f'{host} {method} {path}: {status} {out}')
        return out

    store = Path(args.store)
    supervisor = str(store / 'bin/lamp-supervisor')
    procs = {}
    worker = grant = chrome = sandbox = None

    def lampd(h):
        d = root / h
        argv = [args.lampd, '--fleet-config', str(d / 'fleet.json'), '--identity-file', str(identity), '--state-dir', str(d / 'state'), '--socket', str(d / 'api.sock')]
        if h == 'hs_a':
            argv += ['--worker-socket', str(root / 'worker.sock'), '--worker-id', 'lamp-supervisor@hs_t27', '--worker-boot-id', Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
                     '--worker-uid', '0', '--worker-pid', (root / 'worker.pid').read_text().strip(), '--grant-id', grant['grant_id'], '--worker-timeout', '60s']
        else:
            argv += ['--dashboard-listen', f'127.0.0.1:{args.port}']
        log = (d / 'lampd.log').open('a')
        procs[h] = subprocess.Popen(argv, stdout=log, stderr=log)

    def ui(op, **kw):
        chrome.stdin.write(json.dumps(dict(op=op, **kw)) + '\n')
        chrome.stdin.flush()
        reply = json.loads(chrome.stdout.readline())
        if not reply.get('ok'):
            raise RuntimeError(f'dashboard {op} failed: {reply}')
        return reply.get('value')

    def shot(name, what):
        ui('shot', path=str(shots / (name + '.png')), full=True)
        record('screenshot', dict(file=name + '.png', shows=what))

    try:
        policy = store / 'network.json'
        subprocess.run(['sudo', '-n', 'tee', str(policy)], input=json.dumps(dict(guard=str(store / 'bin/lamp-guard'), pasta='/usr/bin/pasta', audit_dir='/var/lib/lamp-audit/t27', revision=27, principal_id='pr_t27_demo', exceptions=[])).encode(), stdout=subprocess.DEVNULL, check=True)
        log = (root / 'worker.log').open('x')
        worker = subprocess.Popen(['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', str(root / 'worker.pid'), supervisor, 'serve', '--store', str(store), '--socket', str(root / 'worker.sock'),
                                   '--network-policy', str(policy), '--mem-dir', '/run/lamp-t27/mem', '--netns-dir', '/run/lamp-t27/netns', '--uid-base', '1870000000', '--host-id', 'hs_t27', '--allow-uid', str(os.getuid())], stdout=log, stderr=log)
        wait((root / 'worker.sock').exists, 'supervisor', 300)
        grant = json.loads(subprocess.check_output(['sudo', '-n', supervisor, 'grant-install', '--store', str(store), '--caller-uid', str(os.getuid()), '--ttl-s', '3600'], text=True))
        for h in PORTS:
            lampd(h)
        hosts = wait(lambda: (lambda items: items if len(items) == 2 and all(x['state'] == 'active' for x in items) else None)(call('hs_b', 'GET', '/v1/hosts')['items']), 'two ready hosts', 90)
        record('fleet of two hosts (seen from hs_b)', hosts)

        budget = call('hs_a', 'POST', '/v1/budgets', dict(wall_time_ms=30 * 60 * 1000, token_mode='best_effort', labels=dict(name='fleet journey')))['result']['budget_id']
        task = call('hs_a', 'POST', '/v1/tasks', dict(budget_id=budget, labels=dict(name='Work on host A')))['result']['task_id']
        record('sandbox admission', admission())
        created = call('hs_a', 'POST', '/v1/sandboxes', dict(template_id=args.template, task_id=task, labels=dict(name='on-host-a'),
                                                              resources=dict(vcpus=2, memory_mib=4096, disk_gib=20, pids_max=4096),
                                                              lifecycle=dict(idle_action='suspend', idle_grace_seconds=900)))
        sandbox = created['resource_ids'][0]['id'] if created.get('resource_ids') else created['result']['sandbox_id']
        get = lambda: call('hs_a', 'GET', '/v1/sandboxes/' + sandbox)['data']
        wait(lambda: get()['state'] in ('ready', 'failed', 'deleted'), 'sandbox setup', 240)
        record('sandbox on hs_a', get())
        call('hs_a', 'POST', f'/v1/sandboxes/{sandbox}:suspend', {}, get()['revision'])
        wait(lambda: get()['state'] == 'suspended', 'suspended', 240)
        record('suspended on hs_a with saved state', get())

        dash = f'http://127.0.0.1:{args.port}/'
        chrome = subprocess.Popen(['node', str(Path(__file__).with_name('dashboard_cdp.mjs'))], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        ui('goto', url=dash)
        ui('eval', expr=f"document.querySelector('#token').value={json.dumps(token)}; document.querySelector('#signin-form').requestSubmit(); true")
        wait(lambda: 'live' == ui('eval', expr="document.querySelector('#live').textContent"), 'live feed', 30)
        ui('goto', url=dash + '#/hosts', wait=3000)
        shot('f1-hosts-from-b', "dashboard served by hs_b: both hosts, and hs_a's sandbox")
        record('hosts page from hs_b', ui('eval', expr="document.querySelector('#app').innerText"))

        # hs_a goes to sleep: its lampd stops while it holds the saved state.
        procs['hs_a'].send_signal(signal.SIGTERM)
        procs['hs_a'].wait(timeout=70)
        record('hs_a asleep (lampd stopped)', dict(exit=procs['hs_a'].returncode))
        wait(lambda: any(x['id'] == 'hs_a' and x['state'] != 'active' for x in call('hs_b', 'GET', '/v1/hosts')['items']), 'hs_b sees hs_a asleep', 120)
        record('hosts seen from hs_b while hs_a sleeps', call('hs_b', 'GET', '/v1/hosts')['items'])
        record('sandbox seen from hs_b while hs_a sleeps', call('hs_b', 'GET', '/v1/sandboxes', ok=False))
        ui('goto', url=dash + '#/hosts', wait=12000)
        shot('f2-stranded-saved-state', 'hs_b dashboard: hs_a asleep, its saved state stranded there')
        text = ui('eval', expr="document.querySelector('#app').innerText")
        record('hosts page while hs_a sleeps', text)
        ui('goto', url=dash + '#/sandboxes/' + sandbox, wait=3000)
        shot('f3-stranded-sandbox', 'the sandbox page on hs_b explains why it cannot resume')

        lampd('hs_a')
        wait(lambda: all(x['state'] == 'active' for x in call('hs_b', 'GET', '/v1/hosts')['items']), 'hs_a awake again', 120)
        record('hs_a awake again', call('hs_b', 'GET', '/v1/hosts')['items'])
        record('complete', dict(stranded_warning='stranded' in text))
    finally:
        if chrome:
            try:
                ui('quit')
            except Exception:
                chrome.kill()
        if sandbox and procs.get('hs_a') and procs['hs_a'].poll() is None:
            try:
                sb = call('hs_a', 'GET', '/v1/sandboxes/' + sandbox, ok=False).get('data')
                if sb and sb.get('state') != 'deleted':
                    call('hs_a', 'DELETE', '/v1/sandboxes/' + sandbox, None, sb['revision'], ok=False)
                    wait(lambda: call('hs_a', 'GET', '/v1/sandboxes/' + sandbox, ok=False).get('data', {}).get('state') == 'deleted', 'deleted', 120)
            except Exception as e:
                record('cleanup error', str(e))
        for proc in procs.values():
            if proc.poll() is None:
                proc.terminate()
                try:
                    proc.wait(timeout=70)
                except subprocess.TimeoutExpired:
                    proc.kill()
        if grant:
            subprocess.run(['sudo', '-n', supervisor, 'grant-revoke', '--store', str(store), grant['grant_id']], stdout=subprocess.DEVNULL, check=False)
        if worker:
            subprocess.run(['sudo', '-n', 'pkill', '-TERM', '-F', str(root / 'worker.pid')], check=False)
            worker.wait(timeout=70)
        record('released', dict(firecracker=subprocess.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True).stdout.strip()))


if __name__ == '__main__':
    main()
