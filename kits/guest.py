#!/usr/bin/env python3
"""Installed only in a kit VM. No raw CDP or password output leaves this worker."""
import base64
import hashlib
import hmac
import http.client
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import struct
import subprocess
import sys
import time
from urllib.parse import urlsplit

ROOT = Path('/tmp/lamp-kits')
STATE = ROOT / 'state.json'
PORT = 19222
PHASE = 'action'


def command(argv, **kwargs):
    return subprocess.run(argv, check=True, stdout=subprocess.PIPE,
                          stderr=subprocess.DEVNULL, timeout=55, **kwargs).stdout


def cipher(data, passphrase, decrypt=False):
    # Never put AES keys/passphrases in argv or environment: guest exec auditing
    # records those. A private inherited pipe carries a random 256-bit password;
    # OpenSSL derives AES/IV with a fresh salted PBKDF2 header in the ciphertext.
    read_fd, write_fd = os.pipe()
    try:
        os.write(write_fd, base64.b64encode(passphrase) + b'\n')
        os.close(write_fd)
        write_fd = None
        return command(['openssl', 'enc', *(['-d'] if decrypt else []), '-aes-256-cbc',
                        '-pbkdf2', '-iter', '10000', '-pass', 'fd:' + str(read_fd)],
                       input=data, pass_fds=(read_fd,))
    finally:
        os.close(read_fd)
        if write_fd is not None:
            os.close(write_fd)


def seal(public_key, value):
    import tempfile
    keys = secrets.token_bytes(64)  # random encryption password + independent MAC key
    raw = json.dumps(value, separators=(',', ':')).encode()
    encrypted = cipher(raw, keys[:32])
    with tempfile.NamedTemporaryFile(mode='w', suffix='.pem') as public:
        public.write(public_key)
        public.flush()
        wrapped = command(['openssl', 'pkeyutl', '-encrypt', '-pubin', '-inkey', public.name,
                           '-pkeyopt', 'rsa_padding_mode:oaep', '-pkeyopt', 'rsa_oaep_md:sha256'], input=keys)
    return {'key': base64.b64encode(wrapped).decode(),
            'data': base64.b64encode(encrypted).decode(),
            'mac': hmac.new(keys[32:], encrypted, hashlib.sha256).hexdigest()}


def unseal(private_key, envelope):
    keys = command(['openssl', 'pkeyutl', '-decrypt', '-inkey', str(private_key),
                    '-pkeyopt', 'rsa_padding_mode:oaep', '-pkeyopt', 'rsa_oaep_md:sha256'],
                   input=base64.b64decode(envelope['key'], validate=True))
    encrypted = base64.b64decode(envelope['data'], validate=True)
    if len(keys) != 64 or not hmac.compare_digest(envelope['mac'], hmac.new(keys[32:], encrypted, hashlib.sha256).hexdigest()):
        raise ValueError('sealed payload rejected')
    return json.loads(cipher(encrypted, keys[:32], decrypt=True))


def cdp_http(method, path):
    conn = http.client.HTTPConnection('127.0.0.1', PORT, timeout=10)
    try:
        conn.request(method, path)
        response = conn.getresponse()
        if response.status != 200:
            raise ValueError('CDP unavailable')
        return json.loads(response.read(1024 * 1024))
    finally:
        conn.close()


class CDP:
    """Small bounded RFC6455 client; the debug endpoint never leaves VM loopback."""
    def __init__(self, url):
        u = urlsplit(url)
        if u.scheme != 'ws' or u.hostname != '127.0.0.1' or u.port != PORT:
            raise ValueError('invalid debug endpoint')
        self.sock = socket.create_connection(('127.0.0.1', PORT), timeout=40)
        key = base64.b64encode(secrets.token_bytes(16)).decode()
        self.sock.sendall((f'GET {u.path} HTTP/1.1\r\nHost: 127.0.0.1:{PORT}\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: {key}\r\n\r\n').encode())
        headers = bytearray()
        while not headers.endswith(b'\r\n\r\n'):
            headers.extend(self.sock.recv(1))
            if len(headers) > 16384:
                raise ValueError('upgrade too large')
        accept = base64.b64encode(hashlib.sha1((key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest())
        if not headers.startswith(b'HTTP/1.1 101 ') or accept.lower() not in headers.lower():
            raise ValueError('upgrade refused')
        self.sequence = 0

    def read(self, count):
        result = bytearray()
        while len(result) < count:
            chunk = self.sock.recv(count - len(result))
            if not chunk:
                raise ValueError('debug connection lost')
            result.extend(chunk)
        return bytes(result)

    def frame(self, opcode, data):
        mask = secrets.token_bytes(4)
        header = bytes([0x80 | opcode])
        if len(data) < 126:
            header += bytes([0x80 | len(data)])
        else:
            header += bytes([0x80 | 126]) + struct.pack('!H', len(data))
        self.sock.sendall(header + mask + bytes(b ^ mask[i % 4] for i, b in enumerate(data)))

    def call(self, method, **params):
        self.sequence += 1
        data = json.dumps(dict(id=self.sequence, method=method, params=params)).encode()
        if len(data) > 65535:
            raise ValueError('debug input too large')
        self.frame(1, data)
        accumulated = bytearray()
        while True:
            first, second = self.read(2)
            length = second & 127
            if second & 128:
                raise ValueError('masked server frame')
            if length == 126:
                length = struct.unpack('!H', self.read(2))[0]
            elif length == 127:
                length = struct.unpack('!Q', self.read(8))[0]
            if length + len(accumulated) > 4 * 1024 * 1024:
                raise ValueError('debug output too large')
            payload = self.read(length)
            opcode = first & 15
            if opcode == 9:
                self.frame(10, payload)
                continue
            if opcode == 8:
                raise ValueError('debug connection closed')
            if opcode not in (0, 1):
                continue
            accumulated.extend(payload)
            if not first & 128:
                continue
            message = json.loads(accumulated)
            accumulated.clear()
            if message.get('id') == self.sequence:
                if 'error' in message:
                    raise ValueError('debug action failed')
                return message.get('result', {})

    def close(self):
        self.sock.close()


def browser():
    return CDP(cdp_http('GET', '/json/version')['webSocketDebuggerUrl'])


def page(state):
    targets = cdp_http('GET', '/json/list')
    return CDP(next(t['webSocketDebuggerUrl'] for t in targets if t['id'] == state['target']))


def evaluate(cdp, expression):
    result = cdp.call('Runtime.evaluate', expression=expression, returnByValue=True,
                      awaitPromise=True, timeout=10000)
    if 'exceptionDetails' in result:
        raise ValueError('page action failed')
    return result['result'].get('value')


def save(state):
    STATE.write_text(json.dumps(state))
    STATE.chmod(0o600)


def origin(url):
    u = urlsplit(url)
    if u.scheme != 'https' or not u.hostname or u.username or u.password or u.fragment:
        raise ValueError('HTTPS required')
    return f'{u.scheme}://{u.netloc}'


def navigate(state, url):
    global PHASE
    origin(url)
    cdp = page(state)
    try:
        PHASE = 'navigate.request'
        result = cdp.call('Page.navigate', url=url)
        if result.get('errorText'):
            PHASE = 'navigate.network'
            raise ValueError('navigation failed')
        PHASE = 'navigate.observe'
        # Bounded readiness observation, not a workload trial.
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            try:
                actual = evaluate(cdp, '({url:location.href,ready:document.readyState,body:!!document.body})')
            except ValueError:
                # Redirects replace the execution context. Only repeat this
                # read-only readiness probe, never the navigation or a submit.
                time.sleep(.05)
                continue
            if actual['url'] != 'about:blank' and actual['body']:
                return {'navigated': True, 'ready_state': actual['ready']}
            time.sleep(.05)
        raise ValueError('page not ready')
    finally:
        cdp.close()


def main(request):
    global PHASE
    op = request['operation']
    ROOT.mkdir(mode=0o700, exist_ok=True)
    if op == 'boot':
        if STATE.exists():
            raise ValueError('already booted')
        PHASE = 'boot.key'
        command(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:3072', '-out', str(ROOT / 'private.pem')])
        (ROOT / 'private.pem').chmod(0o600)
        public = command(['openssl', 'pkey', '-in', str(ROOT / 'private.pem'), '-pubout']).decode()
        extracted = Path('/tmp/lamp-toolchains/opt/google/chrome/chrome')
        binary = str(extracted) if extracted.is_file() else next((shutil.which(p) for p in ('google-chrome', 'chromium', 'chromium-browser') if shutil.which(p)), None)
        if not binary:
            raise ValueError('install browser kit first')
        profile = ROOT / 'profile'
        profile.mkdir(mode=0o700)
        # Public exec already maps namespace root to an unprivileged guest UID.
        # The dedicated guarded VM is the isolation boundary; do not bypass its
        # collector or attempt a guest setuid/root helper for Chromium.
        PHASE = 'boot.start'
        process = subprocess.Popen([binary, '--headless=new', '--no-sandbox', '--no-first-run', '--no-default-browser-check',
            '--disable-background-networking', '--disable-sync', '--disable-breakpad',
            '--password-store=basic', '--remote-debugging-address=127.0.0.1',
            f'--remote-debugging-port={PORT}', '--user-data-dir=' + str(profile), 'about:blank'],
            stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            start_new_session=True, env={'PATH': '/usr/bin:/bin', 'HOME': str(profile),
                'LD_LIBRARY_PATH': '/tmp/lamp-toolchains/usr/lib/x86_64-linux-gnu:/tmp/lamp-toolchains/usr/lib'})
        deadline = time.monotonic() + 15
        while True:
            try:
                if process.poll() is not None:
                    raise RuntimeError('browser exited')
                PHASE = 'boot.cdp'
                cdp = browser()
                break
            except (OSError, ValueError):
                if time.monotonic() > deadline:
                    raise
                time.sleep(.1)
        PHASE = 'boot.context'
        try:
            context = cdp.call('Target.createBrowserContext')['browserContextId']
            target = cdp.call('Target.createTarget', url='about:blank', browserContextId=context)['targetId']
        finally:
            cdp.close()
        save({'target': target, 'context': context, 'private': False, 'owner_key': request['owner_key']})
        return {'ready': True, 'public_key': public}
    state = json.loads(STATE.read_text())
    if op == 'view_chunk':
        view_id, index = request['view_id'], request['index']
        if not state['private'] or len(view_id) != 32 or any(c not in '0123456789abcdef' for c in view_id) or type(index) is not int or index < 0:
            raise ValueError('private view unavailable')
        path = ROOT / ('view-' + view_id)
        with path.open('rb') as file:
            file.seek(index * 24576)
            data = file.read(24576)
            finished = file.tell() == os.fstat(file.fileno()).st_size
        if not data:
            raise ValueError('private view unavailable')
        if finished:
            path.unlink()
        return {'data': base64.b64encode(data).decode()}
    if op == 'private_action':
        if not state['private']:
            raise ValueError('takeover required')
        action = unseal(ROOT / 'private.pem', request['sealed'])
        if action.get('operation') not in ('navigate', 'click'):
            raise ValueError('unsupported private action')
        return main(action)
    if op == 'stop':
        cdp = browser()
        try:
            cdp.call('Browser.close')
        finally:
            cdp.close()
        shutil.rmtree(ROOT)
        return {'stopped': True}
    if op == 'private':
        state['private'] = True
        save(state)
        return {'private': True}
    if op == 'return':
        # Blank closes every old target (including popups) before task control is
        # re-enabled. Preserve HttpOnly session cookies only; discard DOM, JS,
        # history, local/session storage, downloads and script-readable cookies.
        cdp = browser()
        try:
            cookies = cdp.call('Storage.getCookies', browserContextId=state['context'])['cookies']
            selected = []
            for cookie in cookies:
                if cookie.get('httpOnly') and not cookie.get('partitionKey'):
                    copied = {k: v for k, v in cookie.items() if k in (
                        'name', 'value', 'domain', 'path', 'secure', 'httpOnly', 'sameSite', 'expires')}
                    if cookie.get('session') or copied.get('expires', -1) <= 0:
                        copied.pop('expires', None)
                    selected.append(copied)
            context = cdp.call('Target.createBrowserContext')['browserContextId']
            cdp.call('Storage.setCookies', cookies=selected, browserContextId=context)
            target = cdp.call('Target.createTarget', url='about:blank', browserContextId=context)['targetId']
            cdp.call('Target.disposeBrowserContext', browserContextId=state['context'])
        finally:
            cdp.close()
        state = {'context': context, 'target': target, 'private': False, 'owner_key': state['owner_key']}
        save(state)
        return {'returned': True, 'safe_page': 'about:blank', 'session_policy': 'httponly-cookies-only'}
    if op == 'fill':
        if not state['private']:
            raise ValueError('private takeover required')
        credentials = unseal(ROOT / 'private.pem', request['sealed'])
        cdp = page(state)
        try:
            # Exact approved top-level origin and same-origin standard form.
            # No agent script/evaluation interface is exposed by the service.
            expression = '''(() => { const c = CREDENTIALS;
              if (location.origin !== c.origin || window !== window.top) return false;
              const ps = [...document.querySelectorAll('input[type=password]')];
              if (ps.length !== 1) return false;
              const p = ps[0], f = p.form;
              if (!f || new URL(f.action || location.href).origin !== c.origin) return false;
              const us = [...f.querySelectorAll('input')].filter(x => x.autocomplete === 'username' || x.type === 'email' || x.type === 'text');
              if (us.length !== 1 || p.disabled || us[0].disabled) return false;
              const set = (el,v) => {Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(el,v); el.dispatchEvent(new Event('input',{bubbles:true})); el.dispatchEvent(new Event('change',{bubbles:true}));};
              set(us[0],c.username); set(p,c.password); return true;
            })()'''.replace('CREDENTIALS', json.dumps(credentials))
            if evaluate(cdp, expression) is not True:
                raise ValueError('standard login form required')
            if request.get('submit'):
                evaluate(cdp, "document.querySelector('input[type=password]').form.requestSubmit(); true")
        finally:
            cdp.close()
        return {'filled': True, 'private': True}
    if op == 'export_logins':
        if not state['private']:
            raise ValueError('private takeover required')
        approved = urlsplit(origin(request['origin'])).hostname
        cdp = browser()
        try:
            cookies = cdp.call('Storage.getCookies', browserContextId=state['context'])['cookies']
        finally:
            cdp.close()
        selected = []
        for cookie in cookies:
            domain = cookie['domain'].lstrip('.')
            if cookie.get('httpOnly') and not cookie.get('partitionKey') and (approved == domain or approved.endswith('.' + domain)):
                copied = {k: v for k, v in cookie.items() if k in ('name', 'value', 'domain', 'path', 'secure', 'httpOnly', 'sameSite', 'expires')}
                if cookie.get('session') or copied.get('expires', -1) <= 0:
                    copied.pop('expires', None)
                selected.append(copied)
        result = {'sealed': seal(state['owner_key'], {'cookies': selected})}
        if len(json.dumps(result)) > 60000:
            raise ValueError('selected login set too large')
        return result
    if op == 'import':
        # Deliberate dangerous opt-in: encrypted selected cookies, not an owner's
        # profile directory. Worker offers this only to the trusted service.
        cookies = unseal(ROOT / 'private.pem', request['sealed'])
        cdp = browser()
        try:
            cdp.call('Storage.setCookies', cookies=cookies, browserContextId=state['context'])
        finally:
            cdp.close()
        return {'imported': True, 'warning': 'AGENT_CAN_READ_COPIED_LOGINS'}
    if op == 'navigate':
        return navigate(state, request['url'])
    cdp = page(state)
    try:
        if op == 'private_view':
            if not state['private']:
                raise ValueError('takeover required')
            png = cdp.call('Page.captureScreenshot', format='png')['data']
            url = evaluate(cdp, 'location.href')
            data = json.dumps(seal(state['owner_key'], {'png': png, 'url': url})).encode()
            view_id = secrets.token_hex(16)
            path = ROOT / ('view-' + view_id)
            path.write_bytes(data)
            path.chmod(0o600)
            return {'view_id': view_id, 'chunks': (len(data) + 24575) // 24576}
        if op == 'snapshot':
            if state['private']:
                raise ValueError('private output blocked')
            return evaluate(cdp, "({url:location.href,title:document.title,text:document.body.innerText.slice(0,16000)})")
        if op == 'click':
            selector = json.dumps(request['selector'])
            # No arbitrary JS, remote-debugging methods, headers or downloads.
            if not evaluate(cdp, "(() => {const e=document.querySelector(" + selector + ");if(!e || e.matches('input[type=password]'))return false;e.click();return true;})()"):
                raise ValueError('element unavailable')
            return {'clicked': True}
        if op == 'type':
            if not state['private']:
                expression = "(() => {const e=document.querySelector(" + json.dumps(request['selector']) + ");if(!e || e.type==='password' || e.autocomplete==='current-password' || e.autocomplete==='new-password')return false;e.value=" + json.dumps(request['text']) + ";e.dispatchEvent(new Event('input',{bubbles:true}));return true;})()"
                if not evaluate(cdp, expression):
                    raise ValueError('private typing required')
                return {'typed': True}
            raise ValueError('use sealed private typing')
        if op == 'private_type':
            if not state['private']:
                raise ValueError('takeover required')
            value = unseal(ROOT / 'private.pem', request['sealed'])
            evaluate(cdp, "(() => {const e=document.querySelector(" + json.dumps(value['selector']) + ");if(!e)throw Error();e.value=" + json.dumps(value['text']) + ";e.dispatchEvent(new Event('input',{bubbles:true}));return true;})()")
            return {'typed': True, 'private': True}
    finally:
        cdp.close()
    raise ValueError('unsupported operation')


if __name__ == '__main__':
    try:
        result = main(json.loads(sys.argv[1]))
    except Exception:
        # Exception strings, CDP replies, page content and command stderr may all
        # contain credentials. Never send them to the exec journal or logs.
        print(json.dumps({'error': 'KIT_ACTION_FAILED', 'phase': PHASE}))
        sys.exit(1)
    print(json.dumps(result, separators=(',', ':')))
