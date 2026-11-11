"""Native readers run ONLY on the owner side, after a one-use vault approval.

No unlock, biometric prompt, account discovery or owner-vault enumeration. A locked
or unsupported provider requests private typing instead of exporting a vault.
"""
import json
import os
from pathlib import Path
import stat
import subprocess

from .control import KitError

PROVIDERS = ('1password', 'bitwarden', 'keepassxc', 'proton-pass')


def private_bytes(path):
    # O_NOFOLLOW + fstat checks the opened file, not a racy path precheck.
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
    try:
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
            raise KitError('PRIVATE_FILE_REQUIRED')
        value = os.read(fd, 65537)
        if len(value) > 65536:
            raise KitError('PRIVATE_FILE_REQUIRED')
        return value
    finally:
        os.close(fd)


def read_login(config, item):
    """Return exactly a username/password pair; never forward CLI diagnostics."""
    provider = config['provider']
    binary = config['binary']
    if provider not in PROVIDERS or not Path(binary).is_absolute() or not item or item.startswith('-'):
        raise KitError('PRIVATE_TYPING_REQUIRED')
    env = {k: os.environ[k] for k in ('PATH', 'HOME', 'USER') if k in os.environ}
    # Every provider uses a disposable, explicitly configured CLI home. Never
    # discover a real owner's existing session/profile as fallback test data.
    env['HOME'] = config['home']
    env['XDG_CONFIG_HOME'] = str(Path(config['home']) / '.config')
    env['OP_BIOMETRIC_UNLOCK_ENABLED'] = 'false'
    env['OP_INTERACTIVE'] = 'false'
    env['BW_NOINTERACTION'] = 'true'
    unlock = b''
    try:
        if config.get('unlock_file'):
            unlock = private_bytes(config['unlock_file']).rstrip(b'\r\n')
        if provider == '1password':
            if unlock:
                env['OP_SERVICE_ACCOUNT_TOKEN'] = unlock.decode()
            argv = [binary, 'item', 'get', item, '--vault', config['vault'], '--format', 'json']
        elif provider == 'bitwarden':
            env['BITWARDENCLI_APPDATA_DIR'] = config['home']
            if config.get('ca_file'):
                env['NODE_EXTRA_CA_CERTS'] = config['ca_file']
            if unlock:
                env['BW_SESSION'] = unlock.decode()
            argv = [binary, 'get', 'item', item]
        elif provider == 'keepassxc':
            argv = [binary, 'show', '--quiet', '--show-protected', '-a', 'UserName', '-a', 'Password']
            if config.get('key_file'):
                argv += ['--key-file', config['key_file']]
            argv += [config['vault'], item]
        else:
            argv = [binary, 'item', 'view', '--share-id', config['vault'], '--item-id', item, '--field', 'username']
        # Native readers are trusted owner configuration. Anonymous private files
        # keep plaintext out of argv/logs and avoid unbounded in-memory PIPE reads.
        import tempfile
        def run(args):
            with tempfile.TemporaryFile() as output:
                result = subprocess.run(args, env=env, input=unlock + b'\n' if provider == 'keepassxc' else b'',
                                        stdout=output, stderr=subprocess.DEVNULL, timeout=25)
                if result.returncode:
                    raise KitError('PRIVATE_TYPING_REQUIRED')
                output.seek(0)
                raw = output.read(65537)
                if len(raw) > 65536:
                    raise ValueError('oversized item')
                return raw
        raw = run(argv)
        if provider == 'proton-pass':
            username = raw.decode().removesuffix('\n')
            password = run(argv[:-1] + ['password']).decode().removesuffix('\n')
        elif provider == 'keepassxc':
            lines = raw.decode().splitlines()
            if len(lines) != 2:
                raise ValueError('ambiguous fields')
            username, password = lines
        else:
            data = json.loads(raw)
            if provider == '1password':
                if data.get('vault', {}).get('id') != config['vault'] or data.get('id') != item:
                    raise ValueError('vault or item mismatch')
                fields = {f.get('id'): f.get('value') for f in data.get('fields', [])}
                username, password = fields.get('username'), fields.get('password')
            elif provider == 'bitwarden':
                if data.get('id') != item:
                    raise ValueError('item mismatch')
                if config['vault'] == 'personal':
                    if data.get('organizationId') is not None:
                        raise ValueError('vault mismatch')
                elif config['vault'] not in (data.get('collectionIds') or []):
                    raise ValueError('collection mismatch')
                username, password = data['login']['username'], data['login']['password']
        if not isinstance(username, str) or not isinstance(password, str) or not password:
            raise ValueError('missing login')
        if len(username.encode()) + len(password.encode()) > 16384:
            raise ValueError('oversized login')
        return {'username': username, 'password': password}
    except Exception:
        # Includes locked stores, version drift, absent CLIs and private file errors.
        # Never expose native exception strings or credential-bearing responses.
        raise KitError('PRIVATE_TYPING_REQUIRED') from None
