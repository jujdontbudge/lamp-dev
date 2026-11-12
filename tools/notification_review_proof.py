#!/usr/bin/env python3
"""Short proof of the four PR #34 review fixes against a real standalone lampd."""
import hashlib, hmac, http.client, json, secrets, socket, subprocess, sys, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

lampd, root = sys.argv[1], Path(sys.argv[2])
root.mkdir(mode=0o700)
out = (root / 'transcript.jsonl').open('x')
def record(step, value):
    out.write(json.dumps(dict(step=step, value=value), sort_keys=True) + '\n'); out.flush(); print(step, json.dumps(value)[:300], flush=True)

secret = root / 'hook.secret'; secret.write_text(secrets.token_hex(32)); secret.chmod(0o600)
received = []
fail_mode = {'on': False}
class Hook(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers['Content-Length']))
        if fail_mode['on'] and self.path == '/flaky':
            self.send_response(503); self.end_headers(); return
        ok = hmac.compare_digest(self.headers.get('X-Lamp-Signature', ''), 'sha256=' + hmac.new(secret.read_text().encode(), body, hashlib.sha256).hexdigest())
        received.append((self.path, json.loads(body), ok))
        self.send_response(204); self.end_headers()
    def log_message(self, *a): pass
hook = ThreadingHTTPServer(('127.0.0.1', 0), Hook)
threading.Thread(target=hook.serve_forever, daemon=True).start()
port = hook.server_port
(root / 'notify.json').write_text(json.dumps(dict(public_url='http://127.0.0.1:7480', destinations=dict(
    phone_webhook=dict(url=f'http://127.0.0.1:{port}/phone', secret_file=str(secret)),
    flaky_pager=dict(url=f'http://127.0.0.1:{port}/flaky', secret_file=str(secret))))))
(root / 'notify.json').chmod(0o600)
token, bea_token = secrets.token_hex(32), secrets.token_hex(32)
(root / 'identity.json').write_text(json.dumps(dict(owner_id='pr_t27_proof', bootstrap_sha256=hashlib.sha256(token.encode()).hexdigest(),
    subjects=[dict(issuer='t27-proof', subject='bea', sha256=hashlib.sha256(bea_token.encode()).hexdigest())])))
(root / 'identity.json').chmod(0o600)
sock = root / 'api.sock'
log = (root / 'lampd.log').open('x')
import os
store = Path('/var/lib/lamp/t27'); sup = str(store / 'bin/lamp-supervisor')
policy = store / 'network.json'
wlog = (root / 'worker.log').open('x')
worker = subprocess.Popen(['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', str(root / 'worker.pid'), sup, 'serve', '--store', str(store), '--socket', str(root / 'worker.sock'),
    '--network-policy', str(policy), '--mem-dir', '/run/lamp-t27/mem', '--netns-dir', '/run/lamp-t27/netns', '--uid-base', '1870000000', '--host-id', 'hs_t27', '--allow-uid', str(os.getuid())], stdout=wlog, stderr=wlog)
while not (root / 'worker.sock').exists(): time.sleep(.3)
grant = json.loads(subprocess.check_output(['sudo', '-n', sup, 'grant-install', '--store', str(store), '--caller-uid', str(os.getuid()), '--ttl-s', '1800'], text=True))
wargs = ['--worker-socket', str(root / 'worker.sock'), '--worker-id', 'lamp-supervisor@hs_t27', '--worker-boot-id', Path('/proc/sys/kernel/random/boot_id').read_text().strip(),
    '--worker-uid', '0', '--worker-pid', (root / 'worker.pid').read_text().strip(), '--grant-id', grant['grant_id'], '--worker-timeout', '60s']
proc = subprocess.Popen([lampd] + wargs + ['--identity-file', str(root / 'identity.json'), '--state-dir', str(root / 'state'), '--socket', str(sock), '--notifications-config', str(root / 'notify.json')], stdout=log, stderr=log)

def call(method, path, body=None, rev=None, bearer=None, ok=True):
    c = http.client.HTTPConnection('lamp', timeout=30); c.sock = socket.socket(socket.AF_UNIX); c.sock.connect(str(sock))
    h = {'Authorization': 'Bearer ' + (bearer or token), 'Content-Type': 'application/json'}
    if method != 'GET': h['Idempotency-Key'] = secrets.token_hex(12)
    if rev is not None: h['If-Match'] = str(rev)
    c.request(method, path, None if body is None else json.dumps(body), h)
    r = c.getresponse(); status, raw = r.status, r.read(); c.close()
    o = json.loads(raw) if raw else {}
    if ok and status >= 300: raise RuntimeError(f'{method} {path}: {status} {o}')
    return o
def wait(pred, what, sec=60):
    end = time.monotonic() + sec
    while time.monotonic() < end:
        try:
            v = pred()
        except Exception:
            v = None
        if v: return v
        time.sleep(.3)
    raise RuntimeError('timed out: ' + what)
try:
    wait(sock.exists, 'lampd')
    wait(lambda: call('GET', '/v1/fleets/fl_local'), 'api')
    phone = call('POST', '/v1/notification-sinks', dict(plugin_id='webhook', destination_ref='phone_webhook', enabled=True, event_types=['task.done', 'task.stuck']))['result']
    pager = call('POST', '/v1/notification-sinks', dict(plugin_id='webhook', destination_ref='flaky_pager', enabled=True, event_types=['task.done']))['result']
    record('owner sinks created', dict(phone=phone, pager=pager))

    # 1. Times out (deadline before the watchdog) -> stopped/task.done; stalls -> blocked/task.stuck.
    b1 = call('POST', '/v1/budgets', dict(wall_time_ms=4000, token_mode='best_effort', watchdog=dict(block_after_ms=60000)))['result']['budget_id']
    b2 = call('POST', '/v1/budgets', dict(wall_time_ms=120000, token_mode='best_effort', watchdog=dict(block_after_ms=4000)))['result']['budget_id']
    fail_mode['on'] = True
    t1 = call('POST', '/v1/tasks', dict(budget_id=b1, labels=dict(name='times out')))['result']['task_id']
    t2 = call('POST', '/v1/tasks', dict(budget_id=b2, labels=dict(name='stalls')))['result']['task_id']
    s1 = wait(lambda: (lambda t: t if t['state'] != 'running' else None)(call('GET', '/v1/tasks/' + t1)['data']), 't1 ends', 30)
    s2 = wait(lambda: (lambda t: t if t['state'] != 'running' else None)(call('GET', '/v1/tasks/' + t2)['data']), 't2 ends', 30)
    wait(lambda: sum(1 for p, b, _ in received if p == '/phone') >= 2, 'phone notifications', 20)
    record('1 timed-out vs stalled task', dict(times_out=s1['state'], stalls=s2['state'],
        phone=[(b['event_type'], b.get('task_id') or b.get('resource', {})) for p, b, _ in received if p == '/phone']))

    # 2. A further revision bump (a repeated stop) after done sends no second notification.
    before = len([1 for p, b, _ in received if p == '/phone'])
    t = call('GET', '/v1/tasks/' + t1)['data']
    call('POST', f'/v1/tasks/{t1}:stop', dict(reason='again'), t['revision'], ok=False)  # bumps the revision, state stays stopped
    time.sleep(5)
    after = len([1 for p, b, _ in received if p == '/phone'])
    record('2 revision bump after done', dict(task_revision_before=t['revision'], task_revision_after=call('GET', '/v1/tasks/' + t1)['data']['revision'], phone_before=before, phone_after=after,
        done_events=len([e for e in call('GET', '/v1/events?limit=200&wait_ms=0')['items'] if e['type'] == 'task.done' and e['resource_id'] == t1])))

    # 3. The pager answers 503: after attempts at 1 s and 5 s backoff, a manual
    # retry starts a fresh round (count back to 0, 1 s then 5 s again) and the
    # attempt history is kept. (Waiting for the full 36-minute schedule to reach
    # `failed` is not needed: retry takes the same path from pending or failed.)
    find = lambda: next(n for n in call('GET', '/v1/notifications')['items'] if n.get('destination_ref') == 'flaky_pager' or json.dumps(n).find(pager['notification_sink_id']) >= 0)
    n = wait(lambda: (lambda n: n if n.get('attempt_count', 0) >= 2 else None)(find()), 'two pager attempts', 30)
    record('3a pager before retry', dict(state=n['state'], attempt_count=n['attempt_count'], next_attempt_at=n.get('next_attempt_at'), attempts=n.get('attempts')))
    call('POST', f"/v1/notifications/{n['id']}:retry", {}, n['revision'])
    n = wait(lambda: (lambda n: n if n.get('attempt_count', 0) >= 2 and len(n.get('attempts', [])) >= 4 else None)(find()), 'fresh round attempts', 30)
    record('3b pager after retry', dict(state=n['state'], attempt_count=n['attempt_count'], next_attempt_at=n.get('next_attempt_at'), updated_at=n.get('updated_at'), attempts=n.get('attempts')))

    # 4. A team member cannot point a sink at the owner's enrolled destination.
    fleet = call('GET', '/v1/fleets/fl_local')['data']
    call('PATCH', '/v1/fleets/fl_local', dict(mode='team'), fleet['revision'])
    bea = call('POST', '/v1/principals', dict(issuer='t27-proof', subject='bea', display_name='Disposable Bea'))['result']['principal_id']
    call('POST', '/v1/memberships', dict(principal_id=bea, role='member'))
    refused = call('POST', '/v1/notification-sinks', dict(plugin_id='webhook', destination_ref='phone_webhook', enabled=True), bearer=bea_token, ok=False)
    record('4 member sink to owner destination', refused)
    record('complete', dict(webhooks=len(received), signatures_valid=all(ok for _, _, ok in received)))
finally:
    proc.terminate(); proc.wait(timeout=30); hook.shutdown()
    subprocess.run(['sudo', '-n', sup, 'grant-revoke', '--store', str(store), grant['grant_id']], stdout=subprocess.DEVNULL)
    subprocess.run(['sudo', '-n', 'pkill', '-TERM', '-F', str(root / 'worker.pid')]); worker.wait(timeout=60)
