#!/usr/bin/env python3
"""One real T27 dashboard journey on LX: no benchmarks, trials or gates.

Starts a ticket-owned supervisor and lampd (dashboard on loopback TCP, kits
backend, notification destinations), creates a real Firecracker agent sandbox
and a separate browser VM, and drives the host-served dashboard in headless
Chrome with a disposable profile. A local signed-webhook receiver stands in for
the person's notification endpoint. The vault reader is a disposable fixture
holding only the public test login the-internet.herokuapp.com advertises.
Never touches owner services, profiles, vaults or Docker.
"""
import argparse
import hashlib
import hmac
import html
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import socket
import subprocess
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def main():
    p = argparse.ArgumentParser(description=__doc__)
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
            v = predicate()
            if v:
                return v
            if time.monotonic() > end:
                raise RuntimeError('timed out: ' + what)
            time.sleep(.3)

    def admission():
        # Wait for a free slot under the owner's cap rather than exceed it.
        end = time.monotonic() + 900
        while True:
            try:
                return admission_now()
            except RuntimeError:
                if time.monotonic() > end:
                    raise
                time.sleep(10)

    def admission_now():
        live = int(subprocess.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True).stdout.strip() or '0')
        avail = int(next(l for l in subprocess.check_output(['free', '-m'], text=True).splitlines() if l.startswith('Mem:')).split()[-1])
        if live >= 2 or avail < 12288:
            raise RuntimeError(f'owner sandbox capacity guard: {live} firecracker, {avail} MiB available')
        return dict(firecracker=live, available_mib=avail)

    store = Path(args.store)
    supervisor = str(store / 'bin/lamp-supervisor')
    procs = []

    def start(argv, name, env=None):
        log = (root / (name + '.log')).open('x')
        proc = subprocess.Popen(argv, stdout=log, stderr=log, env=env)
        procs.append(proc)
        return proc

    # --- disposable notification destinations --------------------------------
    hook_secret = root / 'hook.secret'
    hook_secret.write_text(secrets.token_hex(32))
    hook_secret.chmod(0o600)
    received = []

    class Hook(BaseHTTPRequestHandler):
        def do_POST(self):
            body = self.rfile.read(int(self.headers['Content-Length']))
            mac = 'sha256=' + hmac.new(hook_secret.read_text().strip().encode(), body, hashlib.sha256).hexdigest()
            ok = hmac.compare_digest(self.headers.get('X-Lamp-Signature', ''), mac)
            payload = json.loads(body)
            received.append(payload)
            record('webhook received', dict(signature_valid=ok, payload=payload, bearer_in_payload='Bearer' in body.decode()))
            self.send_response(204 if ok else 401)
            self.end_headers()

        def log_message(self, *a):
            pass
    hook = ThreadingHTTPServer(('127.0.0.1', 0), Hook)
    threading.Thread(target=hook.serve_forever, daemon=True).start()
    closed = socket.socket()
    closed.bind(('127.0.0.1', 0))
    dead_port = closed.getsockname()[1]
    closed.close()  # nothing listens: deliveries fail and retry
    notify = root / 'notifications.json'
    notify.write_text(json.dumps(dict(public_url=f'http://127.0.0.1:{args.port}', destinations=dict(
        phone_webhook=dict(url=f'http://127.0.0.1:{hook.server_port}/lamp', secret_file=str(hook_secret)),
        offline_pager=dict(url=f'http://127.0.0.1:{dead_port}/down', secret_file=str(hook_secret))))))
    notify.chmod(0o600)

    # --- disposable fixture vault (public test login only) --------------------
    with urllib.request.urlopen('https://the-internet.herokuapp.com/login') as r:
        advertised = [html.unescape(x) for x in re.findall(r'<em>([^<]+)</em>', r.read(262144).decode())]
    assert len(advertised) == 2
    login = root / 'fixture-login'
    login.write_text('\n'.join(advertised) + '\n')
    login.chmod(0o600)
    reader = root / 'fixture-keepassxc-cli'
    reader.write_text('#!/usr/bin/env python3\nimport sys\nassert sys.argv[1]=="show" and sys.argv[-1]=="herokuapp-test-login"\nsys.stdout.write(open(%r).read())\n' % str(login))
    reader.chmod(0o700)
    (root / 'vault-home').mkdir(mode=0o700)

    token = secrets.token_hex(32)
    (root / 'token').write_text(token)
    (root / 'token').chmod(0o600)
    identity = root / 'identity.json'
    bea_token = secrets.token_hex(32)
    identity.write_text(json.dumps(dict(owner_id='pr_t27_demo', bootstrap_sha256=hashlib.sha256(token.encode()).hexdigest(),
                                        subjects=[dict(issuer='t27-demo', subject='bea', sha256=hashlib.sha256(bea_token.encode()).hexdigest())])))
    work = root / 'work'
    subprocess.run(['git', 'clone', '--quiet', '--depth', '1', 'https://github.com/octocat/Hello-World.git', str(work)], check=True, stdin=subprocess.DEVNULL)
    (root / 'home').mkdir(mode=0o700)
    sessions_cfg = root / 'sessions.json'
    sessions_cfg.write_text(json.dumps(dict(harnesses=dict(hr_shell=dict(owner_id='pr_t27_demo', argv=['/bin/sh', '-i'], cwd=str(work), env=dict(HOME=str(root / 'home'), PATH=os.defpath), auth_modes=['none'])),
                                            hosts=dict(hs_local=dict(owner_id='pr_t27_demo', command=['/usr/bin/python3'], root=str(root / 'r'))))))
    sessions_cfg.chmod(0o600)
    identity.chmod(0o600)
    api_sock = root / 'api.sock'

    def call(method, path, body=None, rev=None, ok=True, bearer=None):
        c = http.client.HTTPConnection('lamp', timeout=70)
        c.sock = socket.socket(socket.AF_UNIX)
        c.sock.settimeout(70)
        c.sock.connect(str(api_sock))
        h = {'Authorization': 'Bearer ' + (bearer or token), 'Content-Type': 'application/json'}
        if method != 'GET':
            h['Idempotency-Key'] = secrets.token_hex(12)
        if rev is not None:
            h['If-Match'] = str(rev)
        c.request(method, path, None if body is None else json.dumps(body), h)
        r = c.getresponse()
        status, raw = r.status, r.read()
        c.close()
        out = json.loads(raw) if raw else {}
        if ok and status >= 300:
            raise RuntimeError(f'{method} {path}: {status} {out}')
        return out

    chrome = None

    def ui(op, **kw):
        chrome.stdin.write(json.dumps(dict(op=op, **kw)) + '\n')
        chrome.stdin.flush()
        reply = json.loads(chrome.stdout.readline())
        if not reply.get('ok'):
            raise RuntimeError(f'dashboard {op} failed: {reply}')
        return reply.get('value')

    def shot(name, what, full=True):
        path = shots / (name + '.png')
        ui('shot', path=str(path), full=full)
        record('screenshot', dict(file=path.name, shows=what))

    def page_text(selector='#app'):
        return ui('eval', expr=f'document.querySelector({json.dumps(selector)}).innerText')

    agent = browser = grant = None
    try:
        policy = store / 'network.json'
        subprocess.run(['sudo', '-n', 'tee', str(policy)], input=json.dumps(dict(guard=str(store / 'bin/lamp-guard'), pasta='/usr/bin/pasta', audit_dir='/var/lib/lamp-audit/t27', revision=27, principal_id='pr_t27_demo', exceptions=[])).encode(), stdout=subprocess.DEVNULL, check=True)
        worker_pid = root / 'worker.pid'
        start(['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', str(worker_pid), supervisor, 'serve', '--store', str(store), '--socket', str(root / 'worker.sock'), '--network-policy', str(policy), '--mem-dir', '/run/lamp-t27/mem', '--netns-dir', '/run/lamp-t27/netns', '--uid-base', '1870000000', '--host-id', 'hs_t27', '--allow-uid', str(os.getuid())], 'worker')
        wait((root / 'worker.sock').exists, 'supervisor', 300)
        grant = json.loads(subprocess.check_output(['sudo', '-n', supervisor, 'grant-install', '--store', str(store), '--caller-uid', str(os.getuid()), '--ttl-s', '3600'], text=True))
        kits = root / 'kits.json'
        kits.write_text(json.dumps(dict(control_socket=str(api_sock), control_token_file=str(root / 'token'), browser_template=args.template,
                                        vaults={'demo-vault': dict(provider='keepassxc', binary=str(reader), vault=str(root / 'demo.kdbx'), home=str(root / 'vault-home'))})))
        kits.chmod(0o600)
        (root / 'demo.kdbx').write_text('fixture')
        (root / 'demo.kdbx').chmod(0o600)
        env = None
        start([args.lampd, '--identity-file', str(identity), '--state-dir', str(root / 'control'), '--socket', str(api_sock),
               '--worker-socket', str(root / 'worker.sock'), '--worker-id', 'lamp-supervisor@hs_t27',
               '--worker-boot-id', Path('/proc/sys/kernel/random/boot_id').read_text().strip(), '--worker-uid', '0',
               '--worker-pid', worker_pid.read_text().strip(), '--grant-id', grant['grant_id'], '--worker-timeout', '60s',
               '--kits-config', str(kits), '--sessions-config', str(sessions_cfg), '--notifications-config', str(notify), '--dashboard-listen', f'127.0.0.1:{args.port}'], 'lampd', env)
        wait(api_sock.exists, 'lampd')
        dash = f'http://127.0.0.1:{args.port}/'
        # Without a credential the network listener answers only the static page.
        status = urllib.request.urlopen(dash).status
        try:
            urllib.request.urlopen(dash + 'v1/sandboxes')
            unauth = 'served'
        except urllib.error.HTTPError as e:
            unauth = e.code
        record('dashboard served; API refuses unauthenticated network callers', dict(page=status, api_without_bearer=unauth))

        chrome = subprocess.Popen(['node', str(Path(__file__).with_name('dashboard_cdp.mjs'))], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True)
        ui('goto', url=dash)
        shot('01-sign-in', 'sign-in page; no data without a credential', full=False)
        ui('eval', expr=f"document.querySelector('#token').value={json.dumps(token)}; document.querySelector('#signin-form').requestSubmit(); true")
        try:
            wait(lambda: 'live' == ui('eval', expr="document.querySelector('#live').textContent"), 'live event feed', 20)
        except RuntimeError:
            record('sign-in diagnostics', ui('eval', expr="[document.querySelector('#live').textContent, document.querySelector('#signin-error').textContent, document.querySelector('#errors').innerText]"))
            raise

        # Sinks are created on the dashboard, as the owner would.
        ui('goto', url=dash + '#/notifications', wait=1500)
        ui('eval', expr="document.querySelector('[name=dest]').value='phone_webhook'; true")
        ui('click', selector='button[data-act=sink-create]', wait=2000)
        ui('eval', expr="document.querySelector('[name=dest]').value='offline_pager'; document.querySelectorAll('[name=etype]').forEach(x=>x.checked=x.value==='task.done'); true")
        ui('click', selector='button[data-act=sink-create]', wait=2000)
        sinks = call('GET', '/v1/notification-sinks')['items']
        record('sinks created on the dashboard', sinks)
        if len(sinks) != 2:
            record('sink diagnostics', ui('eval', expr="document.querySelector('#errors').innerText + ' | ' + location.hash + ' | ' + !!document.querySelector('button[data-act=sink-create]')"))
        assert len(sinks) == 2

        budget = call('POST', '/v1/budgets', dict(wall_time_ms=20 * 60 * 1000, token_mode='best_effort', labels=dict(name='journey budget')))['result']['budget_id']
        task = call('POST', '/v1/tasks', dict(budget_id=budget, labels=dict(name='Log in to the test site')))['result']['task_id']
        record('task and budget', dict(task=call('GET', '/v1/tasks/' + task)['data'], budget=call('GET', '/v1/budgets/' + budget)['data']))

        def create_agent():
            record('agent sandbox admission', admission())
            created = call('POST', '/v1/sandboxes', dict(template_id=args.template, task_id=task, labels=dict(name='agent'),
                                                        resources=dict(vcpus=2, memory_mib=4096, disk_gib=20, pids_max=4096),
                                                        lifecycle=dict(idle_action='suspend', idle_grace_seconds=900)))
            return created['resource_ids'][0]['id'] if created.get('resource_ids') else created['result']['sandbox_id']
        agent = create_agent()
        ui('goto', url=dash + '#/sandboxes/' + agent, wait=700)
        shot('02-setup-progress', 'new sandbox while setup is in progress (operation phase, checks)')
        state_of = lambda: call('GET', '/v1/sandboxes/' + agent)['data']['state']
        wait(lambda: state_of() in ('ready', 'failed', 'deleted'), 'agent setup outcome', 180)
        if state_of() != 'ready':
            ui('goto', url=dash + '#/sandboxes/' + agent, wait=2500)
            shot('02b-setup-failure', 'a real failed setup stage with the operational error and retryable flag')
            record('real setup failure shown', call('GET', '/v1/sandboxes/' + agent)['data'])
            agent = create_agent()
            wait(lambda: state_of() == 'ready', 'agent ready after retry', 180)
        time.sleep(3)  # let the idle monitor take its first observation
        ui('goto', url=dash + '#/overview', wait=2500)
        shot('03-overview-ready', 'overview: setup finished, ready sandbox with idle and task-budget countdowns')
        record('overview text', page_text())

        record('browser VM admission', admission())
        made = call('POST', '/v1/browsers', dict(sandbox_id=agent, wait_ms=60000), ok=False)
        if 'error' in made:
            record('browser setup failure shown', made)
            record('browser VM admission retry', admission())
            made = call('POST', '/v1/browsers', dict(sandbox_id=agent, wait_ms=60000))
        browser = made['result']['browser_id']
        rev = lambda: call('GET', '/v1/browsers/' + browser)['data']['revision']
        wait(lambda: call('GET', '/v1/browsers/' + browser)['data']['state'] == 'agent_control', 'browser ready', 120)

        def navigated():
            out = call('POST', f'/v1/browsers/{browser}:action', dict(action='navigate', arguments=dict(url='https://the-internet.herokuapp.com/login'), wait_ms=60000), rev(), ok=False)
            if 'error' in out:
                record('navigate refused, retrying', out['error'])
                time.sleep(5)
                return False
            return True
        wait(navigated, 'browser navigation', 90)
        ui('goto', url=dash + '#/browsers/' + browser, wait=2000)
        ui('click', selector=f'#browsers-{browser} button[data-act=takeover]', wait=4000)
        ui('click', selector=f'#browsers-{browser} button[data-act=view]', wait=6000)
        shot('04-private-takeover', 'private browser takeover with the owner-only sealed view of the browser VM')
        record('browser after takeover', call('GET', '/v1/browsers/' + browser)['data'])
        # The agent asks to use a saved login: the person sees the exact effect.
        proposal = call('POST', f'/v1/browsers/{browser}:fill-password', dict(credential_source_id='demo-vault', secret_ref='herokuapp-test-login', origin='https://the-internet.herokuapp.com', submit=True, wait_ms=60000), rev())
        record('agent proposal', proposal.get('result'))
        approval = wait(lambda: next((a for a in call('GET', '/v1/approvals')['items'] if a['state'] == 'pending'), None), 'approval listed', 10)
        ui('goto', url=dash + '#/approvals/' + approval['id'], wait=2500)
        shot('05-approval-effect', 'needs-decision: approval showing vault item, destination origin and submit')
        wait(lambda: any(r['event_type'] == 'task.needs_decision' for r in received), 'needs_decision webhook', 20)
        clicked = ui('click', selector=f'#approvals-{approval["id"]} button[data-act=approve]', wait=15000)
        record('approve click', dict(clicked=clicked, dashboard_messages=ui('eval', expr="document.querySelector('#errors').innerText")))
        record('approval after dashboard approve', call('GET', '/v1/approvals/' + approval['id'])['data'])
        ui('goto', url=dash + '#/browsers/' + browser, wait=2000)
        ui('click', selector=f'#browsers-{browser} button[data-act=view]', wait=8000)
        shot('06-approved-fill-result', 'after approval: private view shows the real logged-in page')
        ui('click', selector=f'#browsers-{browser} button[data-act=return-control]', wait=3000)

        # A real native session (stock /bin/sh in a public repo clone) on the same task.
        fleet = call('GET', '/v1/fleets/fl_local')['data']
        call('PATCH', '/v1/fleets/fl_local', dict(mode='team'), fleet['revision'])
        bea = call('POST', '/v1/principals', dict(issuer='t27-demo', subject='bea', display_name='Disposable Bea'))['result']['principal_id']
        call('POST', '/v1/memberships', dict(principal_id=bea, role='member'))
        session = call('POST', '/v1/sessions', dict(sandbox_id='', harness_id='hr_shell', task_id=task, location=dict(kind='host', host_id='hs_local')))['result']['session_id']
        wait(lambda: call('GET', '/v1/sessions/' + session)['data']['state'] == 'active', 'session active', 60)
        sess = call('GET', '/v1/sessions/' + session)['data']
        call('PATCH', '/v1/sessions/' + session, dict(status='blocked', status_reason='Which branch should I push the README fix to: main or a new branch?'), sess['revision'])
        try:
            wait(lambda: any(r['event_type'] == 'task.needs_decision' and r['resource']['kind'] == 'session' for r in received), 'session needs_decision webhook', 20)
        except RuntimeError:
            procs[1].send_signal(3)
            time.sleep(1)
            record('session event diagnostics', dict(session=call('GET', '/v1/sessions/' + session)['data'], events=[(e['type'], e['resource_kind'], e['resource_revision'], e.get('data')) for e in call('GET', '/v1/events?limit=500')['items'] if e['resource_kind'] in ('session', 'notification')], notes=call('GET', '/v1/notifications')['items']))
            raise
        ui('goto', url=dash + '#/sessions/' + session, wait=2500)
        shot('03b-session-needs-answer', 'session waiting on the person, with the question and an answer box')
        ui('eval', expr=f"document.querySelector('#sessions-{session} [name=answer]').value='echo answer-from-dashboard: new branch'; true")
        ui('click', selector=f'#sessions-{session} button[data-act=answer]', wait=3000)
        import base64

        def shell_text():
            out, cursor = '', ''
            for _ in range(50):
                page = call('GET', f'/v1/sessions/{session}/output?limit=500' + ('&cursor=' + cursor if cursor else ''), ok=False)
                out += ''.join(base64.b64decode(f.get('data_base64', '')).decode(errors='replace') for f in page.get('items', []))
                if not page.get('items') or not page.get('next_cursor'):
                    return out
                cursor = page['next_cursor']
            return out
        try:
            wait(lambda: 'answer-from-dashboard: new branch' in shell_text(), 'answer echoed by the real shell', 20)
        except RuntimeError:
            pass
        text = shell_text()
        record('answer reached the real shell', dict(output_contains_answer='answer-from-dashboard: new branch' in text, output_tail=text[-400:], dashboard_errors=ui('eval', expr="document.querySelector('#errors').innerText"), leases=call('GET', '/v1/leases')['items']))
        sess = call('GET', '/v1/sessions/' + session)['data']
        call('PATCH', '/v1/sessions/' + session, dict(status='working', status_reason='pushing to a new branch'), sess['revision'])
        # Share watch-only with Bea from the dashboard, then look as Bea.
        ui('goto', url=dash + '#/sessions/' + session, wait=2000)
        ui('eval', expr=f"document.querySelector('#sessions-{session} [name=subject]').value={json.dumps(bea)}; true")
        ui('click', selector=f'#sessions-{session} button[data-act=share]', wait=2500)
        record('share created on the dashboard', call('GET', '/v1/shares')['items'])
        ui('eval', expr=f"sessionStorage.setItem('lamp.token', {json.dumps(bea_token)}); location.hash='#/overview'; location.reload(); true")
        time.sleep(4)
        shot('03c-bea-sees-only-shared', 'second person signed in: sees only the session shared with her, nothing else')
        record('bea view', page_text())
        ui('eval', expr=f"sessionStorage.setItem('lamp.token', {json.dumps(token)}); location.hash='#/sharing'; location.reload(); true")
        time.sleep(4)
        shot('03d-sharing', 'owner sharing page: watch-only share with expiry and revoke control')
        share_id = call('GET', '/v1/shares')['items'][0]['id']
        ui('click', selector=f'button[data-act=share-revoke][data-id={share_id}]', wait=2500)
        record('share after dashboard revoke', call('GET', '/v1/shares')['items'])

        ui('goto', url=dash + '#/sandboxes/' + agent, wait=2000)
        ui('click', selector=f'#sandboxes-{agent} button[data-act=suspend]', wait=1000)
        wait(lambda: call('GET', '/v1/sandboxes/' + agent)['data']['state'] == 'suspended', 'agent suspended', 120)
        ui('goto', url=dash + '#/sandboxes/' + agent, wait=2500)
        shot('06b-saved-state', 'suspended sandbox with saved-state retention countdown')
        ui('goto', url=dash + '#/tasks/' + task, wait=2000)
        shot('07-task-budget', 'task with shared budget countdown before stop')
        ui('click', selector=f'#tasks-{task} button[data-act=task-stop]', wait=4000)
        wait(lambda: any(r['event_type'] == 'task.done' for r in received), 'task.done webhook', 30)
        record('task after dashboard stop', call('GET', '/v1/tasks/' + task)['data'])
        time.sleep(8)  # the offline sink accumulates real failed attempts (1 s, 5 s backoff)
        ui('goto', url=dash + '#/notifications', wait=2500)
        ui('eval', expr="document.querySelectorAll('details').forEach(d=>d.open=true); true")
        shot('08-notifications-retries', 'deliveries: delivered webhooks and the offline sink with attempt history and retry countdown')
        notes = call('GET', '/v1/notifications')['items']
        failing = next(n for n in notes if n['state'] == 'pending' and n['attempts'])
        before = failing['attempt_count']
        ui('click', selector=f'#notifications-{failing["id"]} button[data-act=note-retry]', wait=3000)
        wait(lambda: call('GET', '/v1/notifications/' + failing['id'])['data']['attempt_count'] > before, 'manual retry attempt', 10)
        ui('click', selector=f'#notifications-{failing["id"]} button[data-act=note-ack]', wait=2500)
        record('manual retry then acknowledge', call('GET', '/v1/notifications/' + failing['id'])['data'])
        ui('eval', expr="document.querySelectorAll('details').forEach(d=>d.open=true); true")
        shot('09-retry-acknowledged', 'offline sink after manual retry and acknowledgement')
        ui('goto', url=dash + '#/sandboxes', wait=2500)
        shot('10-after-stop', 'sandboxes after the task stop released its work')
        ui('goto', url=dash + '#/audit', wait=2500)
        shot('11-audit', 'audit records for this owner', full=False)
        ui('goto', url=dash + '#/features', wait=2500)
        shot('12-features-errors', 'actual feature availability on this host and operational errors')
        record('features text', page_text())
        # A private action link opened without signing in shows sign-in, not data.
        link = next(r['action_link'] for r in received if r['event_type'] == 'task.needs_decision')
        ui('eval', expr="sessionStorage.clear(); true")
        ui('goto', url=link, wait=2500)
        shot('13-action-link-signed-out', 'notification action link opened without a credential asks to sign in', full=False)
        record('notification deliveries', call('GET', '/v1/notifications')['items'])
        record('complete', dict(benchmarks=False, paid_services=False, webhooks=len(received)))
    finally:
        if chrome:
            try:
                ui('quit')
            except Exception:
                chrome.kill()
        try:
            if browser:
                b = call('GET', '/v1/browsers/' + browser, ok=False).get('data')
                if b and b.get('state') != 'closed':
                    call('DELETE', '/v1/browsers/' + browser, dict(wait_ms=60000), b['revision'], ok=False)
            if agent:
                sb = call('GET', '/v1/sandboxes/' + agent, ok=False).get('data')
                if sb and sb.get('state') != 'deleted':
                    call('DELETE', '/v1/sandboxes/' + agent, None, sb['revision'], ok=False)
        except Exception as e:
            record('cleanup error', str(e))
        for proc in reversed(procs[1:]):
            proc.terminate()
            try:
                proc.wait(timeout=70)
            except subprocess.TimeoutExpired:
                proc.kill()
        if procs and grant:
            subprocess.run(['sudo', '-n', supervisor, 'grant-revoke', '--store', str(store), grant['grant_id']], stdout=subprocess.DEVNULL, check=False)
        if procs:
            subprocess.run(['sudo', '-n', 'pkill', '-TERM', '-F', str(root / 'worker.pid')], check=False)
            procs[0].wait(timeout=70)
        record('released', dict(firecracker=subprocess.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True).stdout.strip()))
        hook.shutdown()


if __name__ == '__main__':
    main()
