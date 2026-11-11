"""One private Ubuntu relay journey, driven from M5. Not a benchmark.

Uses real Lamp access/control/supervisor/guest processes. The account signer is
explicitly a local fixture, NOT Clerk; production account setup is not proved.
Run on Ubuntu after copying the three ticket binaries to /tmp/lamp-t24-build.
The M5 driver uses ready.json and private client files, then creates done.
"""
import base64
import hashlib
import http.client
import http.server
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess as sp
import tempfile
import threading
import time

from cryptography.hazmat.primitives import hashes
from cryptography.hazmat.primitives.asymmetric import padding, rsa

os.umask(0o077)
R = Path(tempfile.mkdtemp(prefix='lamp-t24-demo-', dir='/var/tmp'))
B = Path('/tmp/lamp-t24-build')
A = '/tmp/lamp-access-t18-linux'
D = '/tmp/lampd-t18-linux'
W = '/usr/local/lib/lamp/lamp-supervisor'
STORE = '/var/lib/lamp/t10-private'
processes = []
sandbox = grant = workerpid = worker = None
observations = (R / 'observations.jsonl').open('x')

def record(event, **values):
    row = {'event': event, **values}
    observations.write(json.dumps(row) + '\n')
    observations.flush()
    print(json.dumps(row), flush=True)

def command(*args, **kw):
    p = sp.run(list(map(str, args)), capture_output=True, text=True, **kw)
    if p.returncode:
        raise RuntimeError('command failed; private run directory retains details')
    return p.stdout.strip()

def write(name, value):
    p = R / name
    p.write_text(value if isinstance(value, str) else json.dumps(value))
    return p

def launch(name, args):
    p = sp.Popen(list(map(str, args)), stdout=open(R / (name + '.stdout'), 'w'), stderr=open(R / (name + '.stderr'), 'w'))
    processes.append(p)
    return p

def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]

def waitfile(path, process):
    deadline = time.monotonic() + 40
    while not path.exists():
        if process.poll() is not None or time.monotonic() > deadline:
            raise RuntimeError('process readiness failed')
        time.sleep(.1)

class UnixHTTP(http.client.HTTPConnection):
    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.connect(str(R / 'api.sock'))

def call(method, path, body=None, token=None, revision=None):
    c = UnixHTTP('lamp', timeout=70)
    headers = {'Authorization': 'Bearer ' + (token or bootstrap), 'Content-Type': 'application/json'}
    if method != 'GET':
        headers['Idempotency-Key'] = secrets.token_hex(16)
    if revision is not None:
        headers['If-Match'] = str(revision)
    c.request(method, path, None if body is None else json.dumps(body), headers)
    response = c.getresponse()
    data = json.loads(response.read())
    c.close()
    if not 200 <= response.status < 300:
        raise RuntimeError('public control request failed: ' + str(response.status))
    return data

def b64(raw):
    return base64.urlsafe_b64encode(raw).decode().rstrip('=')

try:
    record('binaries', sha256={p.name: hashlib.sha256(p.read_bytes()).hexdigest() for p in (B / 'lamp-relay', B / 'lamp-relay-client')})
    signer = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public = signer.public_key().public_numbers()
    jwks = {'keys': [{'kty': 'RSA', 'alg': 'RS256', 'use': 'sig', 'kid': 'private-demo', 'n': b64(public.n.to_bytes(256, 'big')), 'e': b64(public.e.to_bytes(3, 'big'))}]}
    class Keys(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            raw = json.dumps(jwks).encode()
            self.send_response(200)
            self.end_headers()
            self.wfile.write(raw)
        def log_message(self, *args):
            pass
    keys = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Keys)
    threading.Thread(target=keys.serve_forever, daemon=True).start()
    issuer = 'http://127.0.0.1:' + str(keys.server_port)
    def jwt(subject):
        now = int(time.time())
        claims = {'iss': issuer, 'sub': subject, 'iat': now, 'exp': now + 900, 'azp': 'http://127.0.0.1', 'email': 'same-disposable-email@example.invalid'}
        message = b64(json.dumps({'alg': 'RS256', 'kid': 'private-demo'}).encode()) + '.' + b64(json.dumps(claims).encode())
        return message + '.' + b64(signer.sign(message.encode(), padding.PKCS1v15(), hashes.SHA256()))
    for user in ('alice', 'bea', 'outsider'):
        write(user + '.jwt', jwt(user))
    relayport = port()
    relayurl = 'http://127.0.0.1:' + str(relayport)
    identity = lambda user: {'issuer': issuer, 'subject': user}
    config = {'listen': '127.0.0.1:' + str(relayport), 'hostname': 'relay.eilmer.si', 'state': str(R / 'relay' / 'state.json'), 'issuer': issuer, 'authorized_parties': ['http://127.0.0.1'], 'private_development': True, 'fleets': [{'id': 'f24demo', 'owner': identity('alice'), 'members': [identity('bea')]}]}
    relay = launch('relay', [B / 'lamp-relay', '--config', write('relay.json', config)])
    deadline = time.monotonic() + 10
    while True:
        try:
            with socket.create_connection(('127.0.0.1', relayport), timeout=1):
                break
        except OSError:
            if relay.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('relay not ready')
            time.sleep(.1)
    command(B / 'lamp-relay-client', 'issue-enrollment', '--relay', relayurl, '--fleet', 'f24demo', '--account-token-file', R / 'alice.jwt', '--out', R / 'enrollment')
    command(B / 'lamp-relay-client', 'enroll', '--relay', relayurl, '--fleet', 'f24demo', '--enrollment-file', R / 'enrollment', '--credential-file', R / 'host-credential')
    replay = sp.run([str(B / 'lamp-relay-client'), 'enroll', '--relay', relayurl, '--fleet', 'f24demo', '--enrollment-file', str(R / 'enrollment'), '--credential-file', str(R / 'replayed-credential')], capture_output=True)
    assert replay.returncode != 0
    # Prove the consumed enrollment remains consumed after a process restart.
    relay.terminate()
    relay.wait(timeout=10)
    relay = launch('relay-restarted', [B / 'lamp-relay', '--config', R / 'relay.json'])
    time.sleep(.3)
    replay2 = sp.run([str(B / 'lamp-relay-client'), 'enroll', '--relay', relayurl, '--fleet', 'f24demo', '--enrollment-file', str(R / 'enrollment'), '--credential-file', str(R / 'replayed-after-restart')], capture_output=True)
    assert replay2.returncode != 0
    record('one-time-enrollment', replay_denied=True, replay_after_restart_denied=True, reconnect_secret='host-generated; relay stores SHA-256 only')
    bootstrap = secrets.token_hex(32)
    tokens = {name: secrets.token_hex(32) for name in ('alice', 'bea')}
    write('identity', {'owner_id': 'pr_bootstrap', 'bootstrap_sha256': hashlib.sha256(bootstrap.encode()).hexdigest(), 'subjects': [dict(identity(name), sha256=hashlib.sha256(tokens[name].encode()).hexdigest()) for name in tokens]})
    for name in ('host-ca', 'user-ca', 'alice', 'bea'):
        command(A, 'init-ca', '--out', R / name)
    for name, token in tokens.items():
        write(name + '.fleet-token', token)
    write('known_hosts', '@cert-authority h-demo.f24demo.fleet.eilmer.si ' + (R / 'host-ca.pub').read_text())
    pidfile = R / 'worker.pid'
    worker = launch('worker', ['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', pidfile, W, 'serve', '--store', STORE, '--socket', R / 'worker.sock', '--mem-dir', '/run/lamp-t24/mem', '--netns-dir', '/run/lamp-t24/netns', '--uid-base', '2240000000', '--network-policy', '/var/lib/lamp/t10-private-bin/network.json', '--allow-uid', os.getuid()])
    waitfile(R / 'worker.sock', worker)
    workerpid = int(command('sudo', '-n', 'cat', pidfile))
    grant = json.loads(command('sudo', '-n', W, 'grant-install', '--store', STORE, '--caller-uid', os.getuid(), '--ttl-s', '1200'))
    daemon = launch('lampd', [D, '--identity-file', R / 'identity', '--socket', R / 'api.sock', '--state-dir', R / 'control', '--worker-socket', R / 'worker.sock', '--worker-id', 'lamp-supervisor@' + socket.gethostname(), '--worker-boot-id', Path('/proc/sys/kernel/random/boot_id').read_text().strip(), '--worker-uid', '0', '--worker-pid', workerpid, '--grant-id', grant['grant_id'], '--worker-timeout', '1m'])
    waitfile(R / 'api.sock', daemon)
    call('PATCH', '/v1/fleets/fl_local', {'mode': 'team', 'wait_ms': 60000}, revision=1)
    for name in tokens:
        principal = call('POST', '/v1/principals', dict(identity(name), wait_ms=60000))['result']['principal_id']
        call('POST', '/v1/memberships', {'principal_id': principal, 'role': 'member', 'wait_ms': 60000})
    live = int(sp.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True).stdout.strip() or '0')
    available = int(next(line.split()[6] for line in command('free', '-m').splitlines() if line.startswith('Mem:')))
    record('allocation-admission', firecracker_count=live, available_mib=available)
    if live >= 2 or available < 12288:
        raise RuntimeError('owner sandbox admission denied')
    sandbox = call('POST', '/v1/sandboxes', {'template_id': 'tp_6b295ecbcad03c2a3b7370e577', 'wait_ms': 60000}, token=tokens['alice'])['result']['sandbox_id']
    record('sandbox-created', sandbox_id=sandbox, state=call('GET', '/v1/sandboxes/' + sandbox, token=tokens['alice'])['data']['state'])
    sshport = port()
    access = {'state_dir': str(R / 'access'), 'api_socket': str(R / 'api.sock'), 'ssh_listen': '127.0.0.1:' + str(sshport), 'host_ca_file': str(R / 'host-ca'), 'user_ca_file': str(R / 'user-ca'), 'hostnames': ['h-demo.f24demo.fleet.eilmer.si'], 'users': [{'name': name, 'authorized_keys': [(R / (name + '.pub')).read_text().strip()], 'token_file': str(R / (name + '.fleet-token'))} for name in tokens]}
    edge = launch('access', [A, 'serve', '--config', write('access.json', access)])
    waitfile(R / 'access' / 'ssh-host', edge)
    host = launch('outbound-host', [B / 'lamp-relay-client', 'host', '--relay', relayurl, '--credential-file', R / 'host-credential', '--target', '127.0.0.1:' + str(sshport)])
    time.sleep(.4)
    write('ready.json', {'root': str(R), 'relay_port': relayport, 'sandbox_id': sandbox, 'host_ca_alias': 'h-demo.f24demo.fleet.eilmer.si'})
    record('ready-for-m5', root=str(R), relay_port=relayport, sandbox_id=sandbox, account_provider='LOCAL FIXTURE, NOT CLERK', inbound_host_listeners='numeric loopback only')
    deadline = time.monotonic() + 600
    while not (R / 'done').exists():
        if time.monotonic() > deadline:
            raise RuntimeError('M5 driver did not finish')
        time.sleep(.2)
    record('m5-driver-complete')
finally:
    if sandbox:
        try:
            sb = call('GET', '/v1/sandboxes/' + sandbox, token=tokens['alice'])['data']
            call('DELETE', '/v1/sandboxes/' + sandbox, {'wait_ms': 60000}, token=tokens['alice'], revision=sb['revision'])
            record('sandbox-released', state=call('GET', '/v1/sandboxes/' + sandbox, token=tokens['alice'])['data']['state'])
        except Exception as e:
            record('cleanup-failure', error=type(e).__name__)
    for p in reversed(processes):
        if p.poll() is None:
            if p is worker:
                if workerpid is None:
                    workerpid = int(command('sudo', '-n', 'cat', R / 'worker.pid'))
                sp.run(['sudo', '-n', 'kill', '-TERM', str(workerpid)], check=True)
            else:
                p.terminate()
        try:
            p.wait(timeout=15)
        except sp.TimeoutExpired:
            p.kill()
            p.wait()
    if grant:
        sp.run(['sudo', '-n', W, 'grant-revoke', '--store', STORE, grant['grant_id']], capture_output=True, check=True)
    record('cleanup-complete', firecracker_count=int(sp.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True).stdout.strip() or '0'), owner_services='untouched', docker='untouched')
    observations.close()
