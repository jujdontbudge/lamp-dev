"""Actual non-relay lampd/access edge, with a disposable local OIDC signer.
No VM, hosted account, browser profile or owner project is used.
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
root = Path(tempfile.mkdtemp(prefix='lamp-t24-owner-oidc-'))
binpath = Path('/tmp/lamp-t24-build')
processes = []
rows = []
def record(event, **values):
    row = dict(event=event, **values)
    rows.append(row)
    print(json.dumps(row), flush=True)
def write(name, value):
    path = root / name
    path.write_text(value if isinstance(value, str) else json.dumps(value))
    return path
def launch(name, args):
    p = sp.Popen(list(map(str, args)), stdout=open(root / (name + '.stdout'), 'w'), stderr=open(root / (name + '.stderr'), 'w'))
    processes.append(p)
    return p
def wait(path, process):
    deadline = time.monotonic() + 15
    while not path.exists():
        if process.poll() is not None or time.monotonic() > deadline:
            raise RuntimeError('readiness failed')
        time.sleep(.1)
def b64(raw):
    return base64.urlsafe_b64encode(raw).decode().rstrip('=')
try:
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    public = key.public_key().public_numbers()
    jwks = {'keys': [{'kty': 'RSA', 'alg': 'RS256', 'use': 'sig', 'kid': 'owner', 'n': b64(public.n.to_bytes(256, 'big')), 'e': b64(public.e.to_bytes(3, 'big'))}]}
    class Keys(http.server.BaseHTTPRequestHandler):
        def do_GET(self):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(json.dumps(jwks).encode())
        def log_message(self, *args):
            pass
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Keys)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    issuer = 'http://127.0.0.1:' + str(server.server_port)
    def jwt(subject, audience='owner-fleet'):
        now = int(time.time())
        claims = {'iss': issuer, 'sub': subject, 'aud': audience, 'azp': 'http://127.0.0.1', 'iat': now, 'exp': now + 300, 'email': 'same-email@example.invalid'}
        message = b64(json.dumps({'alg': 'RS256', 'kid': 'owner'}).encode()) + '.' + b64(json.dumps(claims).encode())
        return message + '.' + b64(key.sign(message.encode(), padding.PKCS1v15(), hashes.SHA256()))
    token = secrets.token_hex(32)
    write('token', token)
    write('identity', {'owner_id': 'pr_disposable', 'bootstrap_sha256': hashlib.sha256(token.encode()).hexdigest()})
    daemon = launch('lampd', [binpath / 'lampd-mac', '--socket', root / 'api.sock', '--state-dir', root / 'control', '--identity-file', root / 'identity', '--worker-socket', root / 'unused-worker', '--worker-id', 'unused', '--worker-boot-id', 'unused', '--worker-uid', os.getuid(), '--worker-pid', os.getpid(), '--grant-id', 'unused'])
    wait(root / 'api.sock', daemon)
    for ca in ('host-ca', 'user-ca'):
        assert sp.run([str(binpath / 'lamp-access-mac'), 'init-ca', '--out', str(root / ca)], capture_output=True).returncode == 0
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        port = s.getsockname()[1]
    config = {'state_dir': str(root / 'access'), 'api_socket': str(root / 'api.sock'), 'http_listen': '127.0.0.1:' + str(port), 'host_ca_file': str(root / 'host-ca'), 'user_ca_file': str(root / 'user-ca'), 'hostnames': ['h-owner.eilmer.si'], 'users': [{'name': 'alice', 'token_file': str(root / 'token'), 'identity': {'issuer': issuer, 'subject': 'alice'}}], 'oidc': {'issuer': issuer, 'audience': 'owner-fleet', 'jwks_url': issuer + '/keys', 'authorized_parties': ['http://127.0.0.1']}}
    edge = launch('access', [binpath / 'lamp-access-mac', 'serve', '--config', write('access.json', config)])
    wait(root / 'access' / 'ssh-host', edge)
    time.sleep(.2)
    def request(credential):
        c = http.client.HTTPConnection('127.0.0.1', port, timeout=10)
        c.request('GET', '/v1/fleets/fl_local', headers={'Authorization': 'Bearer ' + credential})
        response = c.getresponse()
        status, body = response.status, response.read()
        c.close()
        return status, json.loads(body) if status == 200 else None
    status, body = request(jwt('alice'))
    assert status == 200 and body['data']['id'] == 'fl_local'
    record('own-oidc-to-real-fleet', relay_used=False, eilmer_account_required=False, immutable_subject='alice', fleet=body['data']['id'], provider='local disposable OIDC signer')
    assert request(jwt('alice', 'another-product'))[0] == 401
    assert request(jwt('different-subject'))[0] == 401
    record('owner-oidc-binding', wrong_audience_denied=True, same_email_different_subject_denied=True)
    server.shutdown()
    assert request(token)[0] == 200
    record('local-account-with-provider-stopped', fleet_read=True, clerk_contacted=False, oidc_fetch_required=False)
finally:
    for p in reversed(processes):
        if p.poll() is None:
            p.terminate()
        try:
            p.wait(timeout=10)
        except sp.TimeoutExpired:
            p.kill()
            p.wait()
    record('cleanup-complete', owner_services='untouched', vms_created=0)
    (root / 'observations.jsonl').write_text(''.join(json.dumps(row) + '\n' for row in rows))
    print(json.dumps({'private_state': str(root)}))
