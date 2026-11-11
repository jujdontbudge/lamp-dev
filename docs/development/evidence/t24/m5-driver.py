"""M5 side of the single private relay demonstration. No browser or owner state."""
import base64
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess as sp
import sys
import tempfile
import urllib.request

os.umask(0o077)
remote = sys.argv[1]
root = Path(tempfile.mkdtemp(prefix='lamp-t24-m5-'))
control = str(root / 'ssh-control')
client = '/tmp/lamp-t24-build/lamp-relay-client-mac'
records = (root / 'observations.jsonl').open('x')

def record(event, **values):
    row = {'event': event, **values}
    records.write(json.dumps(row) + '\n')
    records.flush()
    print(json.dumps(row), flush=True)

def run(args, **kw):
    return sp.run(list(map(str, args)), capture_output=True, text=True, **kw)

try:
    ready = run(['ssh', 'ubuntu-server', 'python3 -c ' + repr('from pathlib import Path; print(Path(' + repr(remote + '/ready.json') + ').read_text())')])
    assert ready.returncode == 0
    ready = json.loads(ready.stdout)
    for name in ('alice', 'bea', 'known_hosts', 'alice.jwt', 'bea.jwt', 'outsider.jwt'):
        assert run(['scp', '-q', 'ubuntu-server:' + remote + '/' + name, root / name]).returncode == 0
    # Forward ONLY the relay, never the host SSH port or the fleet API.
    p = ready['relay_port']
    # A background SSH/proxy inherits captured pipes and would keep communicate()
    # waiting after ssh -f exits. Give the tunnel its own private log instead.
    with (root / 'tunnel.log').open('w') as tunnel_log:
        assert sp.run(['ssh', '-M', '-S', control, '-fN', '-o', 'BatchMode=yes', '-o', 'ExitOnForwardFailure=yes', '-L', f'127.0.0.1:{p}:127.0.0.1:{p}', 'ubuntu-server'], stdin=sp.DEVNULL, stdout=tunnel_log, stderr=tunnel_log, timeout=20).returncode == 0
    relay = f'http://127.0.0.1:{p}'
    def ssh(user, account=None, key=None):
        account = account or user
        proxy = f'{client} connect --relay {relay} --fleet f24demo --account-token-file {root}/{account}.jwt'
        return ['ssh', '-F', '/dev/null', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=20', '-o', 'IdentitiesOnly=yes', '-o', 'StrictHostKeyChecking=yes', '-o', 'HostKeyAlias=' + ready['host_ca_alias'], '-o', 'UserKnownHostsFile=' + str(root / 'known_hosts'), '-o', 'ProxyCommand=' + proxy, '-i', str(root / (key or user)), user + '@' + ready['host_ca_alias']]
    # Public third-party content, not the owner's work.
    with urllib.request.urlopen('https://raw.githubusercontent.com/octocat/Hello-World/master/README', timeout=20) as response:
        public = response.read(4096)
    encoded = base64.b64encode(public).decode()
    marker = secrets.token_hex(12)
    lines = f"printf %s {encoded} | base64 -d > /tmp/public-hello-world; cat /tmp/public-hello-world; printf '\\n{marker}\\n'\nexit\n"
    alice = run(ssh('alice') + ['shell ' + ready['sandbox_id']], input=lines, timeout=70)
    if alice.returncode != 0:
        (root / 'alice.private-error').write_text(alice.stderr)
        raise RuntimeError('Alice relay SSH failed; inspect private error file')
    assert public.decode().strip() in alice.stdout and marker in alice.stdout
    record('alice-m5-to-ubuntu-guest', account_subject='alice', independently_pinned_host_ca=True, public_project='https://github.com/octocat/Hello-World', stdout=alice.stdout.strip())
    bea = run(ssh('bea') + ['shell ' + ready['sandbox_id']], input='exit\n', timeout=40)
    assert bea.returncode != 0 and 'sandbox_get: NOT_FOUND' in bea.stderr
    record('bea-m5-to-same-fleet', account_subject='bea', independently_pinned_host_ca=True, authenticated_to_fleet=True, alice_private_sandbox='NOT_FOUND', stderr=bea.stderr.strip())
    outsider = run(ssh('alice', account='outsider') + ['sessions'], timeout=30)
    assert outsider.returncode != 0 and 'relay connection denied' in outsider.stderr
    record('same-email-not-membership', account_subject='outsider', same_email_claim_as_alice_and_bea=True, relay_denied=True)
    wrongkey = run(ssh('alice', account='bea', key='bea') + ['sessions'], timeout=30)
    assert wrongkey.returncode != 0 and 'Permission denied' in wrongkey.stderr
    record('relay-membership-not-fleet-authority', bea_account_cannot_impersonate_alice_ssh=True)
    assert run(['scp', '-q', 'ubuntu-server:' + remote + '/relay/state.json', root / 'relay-state.json']).returncode == 0
    state = (root / 'relay-state.json').read_bytes()
    assert marker.encode() not in state and public not in state
    record('relay-durable-state', schema_fields=sorted(json.loads(state)), guest_marker_present=False, public_guest_content_present=False, qualification='durable metadata only; no memory or packet capture claimed')
    record('demonstration-complete', local_private_state=str(root), real_clerk_sign_in='NOT DEMONSTRATED', public_dns_tls_caa='NOT ACTIVATED')
finally:
    run(['ssh', '-S', control, '-O', 'exit', 'ubuntu-server'])
    run(['ssh', 'ubuntu-server', 'touch ' + remote + '/done'])
    records.close()
