"""Exercise the separate issuer against real LE staging with zero public orders.
This proves account persistence and CSR admission, NOT a trusted certificate.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess as sp
import tempfile

from cryptography import x509
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric import ec, ed25519

os.umask(0o077)
root = Path(tempfile.mkdtemp(prefix='lamp-t24-issuer-'))
binary = '/tmp/lamp-t24-build/lamp-issuer-mac'
key = ed25519.Ed25519PrivateKey.generate()
(root / 'root.pem').write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
config = {'state_dir': str(root / 'issuer-state'), 'directory': 'https://acme-staging-v02.api.letsencrypt.org/directory', 'email': '', 'dns_helper': '/usr/bin/false', 'dns_helper_sha256': hashlib.sha256(Path('/usr/bin/false').read_bytes()).hexdigest(), 'authoritative_dns': ['172.64.33.153:53', '172.64.34.158:53'], 'weekly_capacity': 0, 'fleet_roots': {'f24demo': base64.b64encode(key.public_key().public_bytes(serialization.Encoding.Raw, serialization.PublicFormat.Raw)).decode()}}
(root / 'config.json').write_text(json.dumps(config))
records = []
def invoke(command, *args):
    return sp.run([binary, command, '--config', str(root / 'config.json'), *map(str, args)], capture_output=True, text=True, timeout=80)

def record(event, **values):
    row = dict(event=event, **values)
    records.append(row)
    print(json.dumps(row), flush=True)

account = invoke('init-account')
assert account.returncode == 0, 'real staging account registration failed (private stderr retained)'
record('separate-real-staging-account', persisted_private_key=True, relay_has_account_key=False, paid_services=0)
challenge = invoke('challenge', '--fleet', 'f24demo')
assert challenge.returncode == 0
nonce = challenge.stdout.strip()
csrkey = ec.generate_private_key(ec.SECP256R1())
def request(name, suffix):
    csr = x509.CertificateSigningRequestBuilder().subject_name(x509.Name([])).add_extension(x509.SubjectAlternativeName([x509.DNSName(name)]), critical=False).sign(csrkey, __import__('cryptography.hazmat.primitives.hashes', fromlist=['SHA256']).SHA256())
    (root / ('csr-' + suffix)).write_bytes(csr.public_bytes(serialization.Encoding.PEM))
    result = invoke('request', '--fleet', 'f24demo', '--nonce', nonce, '--csr', root / ('csr-' + suffix), '--root-key', root / 'root.pem', '--out', root / ('request-' + suffix))
    assert result.returncode == 0
    return root / ('request-' + suffix)
foreign = invoke('issue', '--request', request('*.another.fleet.eilmer.si', 'foreign'), '--out', root / 'foreign.pem')
assert foreign.returncode != 0 and 'exactly the allocated fleet wildcard' in foreign.stderr
record('fleet-signed-foreign-name', denied=True, acme_order_created=False)
valid = request('*.f24demo.fleet.eilmer.si', 'valid')
result = invoke('issue', '--request', valid, '--out', root / 'fleet.pem')
assert result.returncode != 0 and 'CERTIFICATE_ISSUANCE_DEFERRED' in result.stderr
record('valid-csr-with-no-allocated-capacity', state='deferred', acme_order_created=False, insecure_fallback=False)
forged = json.loads(valid.read_text())
forged['signature'] = base64.b64encode(bytes(64)).decode()
(root / 'forged').write_text(json.dumps(forged))
result = invoke('issue', '--request', root / 'forged', '--out', root / 'forged.pem')
assert result.returncode != 0 and 'fleet root proof required' in result.stderr
record('account-alone-not-fleet-authority', unsigned_or_forged_csr_denied=True)
result = invoke('check-caa', '--fleet', 'f24demo')
assert result.returncode != 0
record('unconfigured-public-caa', activation_refused=True, dns_changed=False, public_certificate='NOT ISSUED')
record('issuer-demonstration-complete', private_state=str(root), real_public_certificate='NOT DEMONSTRATED', issued_certificates=0)
(root / 'observations.jsonl').write_text(''.join(json.dumps(row) + '\n' for row in records))
