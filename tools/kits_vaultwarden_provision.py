#!/usr/bin/env python3
"""Provision one disposable native Vaultwarden account with the real Bitwarden CLI.

Run only against the T22 loopback Vaultwarden, with a fresh --root. No owner
profile is read. Native CLI output/secrets stay private; evidence is booleans/IDs.
Requires Python cryptography for Bitwarden's client-side registration envelope.
"""
import argparse
import base64
import hashlib
import hmac
import html
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import urllib.request

from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
from cryptography.hazmat.primitives import padding


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', required=True)
    p.add_argument('--binary', required=True)
    p.add_argument('--ca-file', required=True)
    p.add_argument('--resume', action='store_true', help='continue only this disposable account after failed login')
    args = p.parse_args()
    root = Path(args.root).resolve()
    home, appdata = root / 'home', root / 'appdata'
    credential = root / 'disposable-account.json'
    if args.resume:
        from kits.vaults import private_bytes
        saved = json.loads(private_bytes(credential))
        email, master_password = saved['email'], saved['password']
    else:
        root.mkdir(mode=0o700); home.mkdir(mode=0o700); appdata.mkdir(mode=0o700)
        master_password = secrets.token_urlsafe(32)
        email = 'lamp-' + secrets.token_hex(8) + '@lamp.invalid'
        fd = os.open(credential, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        with os.fdopen(fd, 'w') as f: json.dump({'email': email, 'password': master_password}, f)
    iterations = 600000
    master = hashlib.pbkdf2_hmac('sha256', master_password.encode(), email.encode(), iterations, 32)
    authentication = hashlib.pbkdf2_hmac('sha256', master, master_password.encode(), 1, 32)
    enc = hmac.new(master, b'enc\x01', hashlib.sha256).digest()
    mac = hmac.new(master, b'mac\x01', hashlib.sha256).digest()
    key = secrets.token_bytes(64); iv = secrets.token_bytes(16)
    padder = padding.PKCS7(128).padder(); padded = padder.update(key) + padder.finalize()
    encryptor = Cipher(algorithms.AES(enc), modes.CBC(iv)).encryptor()
    ciphertext = encryptor.update(padded) + encryptor.finalize()
    b64 = lambda value: base64.b64encode(value).decode()
    wrapped = '2.' + '|'.join(map(b64, [iv, ciphertext, hmac.new(mac, iv + ciphertext, hashlib.sha256).digest()]))
    from cryptography.hazmat.primitives.asymmetric import rsa
    from cryptography.hazmat.primitives import serialization
    asymmetric = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    private_der = asymmetric.private_bytes(serialization.Encoding.DER, serialization.PrivateFormat.PKCS8, serialization.NoEncryption())
    padder = padding.PKCS7(128).padder(); padded = padder.update(private_der) + padder.finalize()
    private_iv = secrets.token_bytes(16)
    encryptor = Cipher(algorithms.AES(key[:32]), modes.CBC(private_iv)).encryptor()
    private_cipher = encryptor.update(padded) + encryptor.finalize()
    encrypted_private = '2.' + '|'.join(map(b64, [private_iv, private_cipher, hmac.new(key[32:], private_iv + private_cipher, hashlib.sha256).digest()]))
    public_der = asymmetric.public_key().public_bytes(serialization.Encoding.DER, serialization.PublicFormat.SubjectPublicKeyInfo)
    request = {'email': email, 'masterPasswordHash': b64(authentication), 'key': wrapped,
               'keys': {'publicKey': b64(public_der), 'encryptedPrivateKey': encrypted_private},
               'kdf': 0, 'kdfIterations': iterations}
    origin = 'https://127.0.0.1:18225'
    if not args.resume:
        import ssl
        context = ssl.create_default_context(cafile=args.ca_file)
        verification = urllib.request.Request(origin + '/identity/accounts/register/send-verification-email',
            json.dumps({'email': email, 'name': 'Disposable T22'}).encode(),
            {'Content-Type': 'application/json', 'Accept': 'application/json'}, method='POST')
        with urllib.request.urlopen(verification, context=context) as response:
            request['emailVerificationToken'] = json.load(response)
        req = urllib.request.Request(origin + '/identity/accounts/register/finish',
            json.dumps(request).encode(), {'Content-Type': 'application/json'}, method='POST')
        with urllib.request.urlopen(req, context=context) as response:
            assert response.status == 200
    env = {'PATH': '/usr/bin:/bin', 'HOME': str(home), 'NODE_EXTRA_CA_CERTS': args.ca_file,
           'BITWARDENCLI_APPDATA_DIR': str(appdata), 'BW_NOINTERACTION': 'true'}
    def cli(argv, value=None):
        result = subprocess.run([args.binary] + argv, env=env,
            input=value, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if result.returncode:
            # Emit only a fixed diagnostic vocabulary, never CLI text, tokens,
            # account addresses or password values. Keep failure recovery private.
            vocabulary = {'error','invalid','master','password','key','keys','decrypt','mac','sdk','crypto',
                          'fetch','connect','server','response','kdf','unsupported','missing','public','private',
                          'typeerror','unauthorized','verification','login','undefined','not','found','timeout'}
            words = set(re.findall(r'[a-z]+', (result.stdout + result.stderr).decode(errors='replace').lower()))
            frames = re.findall(r'^\s*at ([A-Za-z0-9_.$<>]+) ', result.stderr.decode(errors='replace'), re.M)[:8]
            raise RuntimeError('native CLI '+argv[0]+' failed '+str(result.returncode)+'; classes='+','.join(sorted(words & vocabulary))+'; frames='+','.join(frames))
        return result.stdout
    if cli(['config', 'server']).decode().strip() != origin:
        cli(['config', 'server', origin, '--api', origin+'/api', '--identity', origin+'/identity',
             '--web-vault', origin, '--notifications', origin+'/notifications'])
    env['BW_PASSWORD'] = master_password
    status = json.loads(cli(['status']))['status']
    session = cli((['login', email] if status == 'unauthenticated' else ['unlock']) + ['--passwordenv', 'BW_PASSWORD', '--raw']).strip()
    if not session:
        raise RuntimeError('native session unavailable')
    env.pop('BW_PASSWORD'); env['BW_SESSION'] = session.decode()
    private = root / 'session'
    fd = os.open(private, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    with os.fdopen(fd, 'wb') as f: f.write(session)
    with urllib.request.urlopen('https://the-internet.herokuapp.com/login') as response:
        advertised = re.findall(r'<em>([^<]+)</em>', response.read(262144).decode())
    assert len(advertised) == 2
    username, password = map(html.unescape, advertised)
    item = json.loads(cli(['get', 'template', 'item']))
    item.update({'type': 1, 'name': 'T22 disposable public login',
                 'login': {'username': username, 'password': password,
                           'uris': [{'uri': 'https://the-internet.herokuapp.com/login', 'match': None}], 'totp': None}})
    encoded = b64(json.dumps(item).encode())
    created = json.loads(cli(['create', 'item'], encoded.encode()))
    # Login fields use private stdin, never command-line arguments.
    from kits.vaults import read_login
    config = {'provider': 'bitwarden', 'binary': args.binary, 'home': str(appdata),
              'vault': 'personal', 'unlock_file': str(private), 'ca_file': args.ca_file}
    actual = read_login(config, created['id'])
    assert actual == {'username': username, 'password': password}
    config_file = root / 'kits.json'
    config_file.write_text(json.dumps({'vaults': {'disposable-vaultwarden': config}}));config_file.chmod(0o600)
    evidence = {'server': 'Vaultwarden', 'server_version': '1.37.5', 'native_no_host_docker': True,
                'disposable_registration': True, 'native_cli_login': True,
                'native_adapter_read_matches_public_fixture': True,
                'credential_source_id': 'disposable-vaultwarden', 'secret_ref': created['id']}
    (root / 'evidence.json').write_text(json.dumps(evidence, sort_keys=True))
    print(json.dumps(evidence, sort_keys=True))


if __name__ == '__main__':
    main()
