"""Standalone owner/task kit boundary, analogous to lamp-broker.

Owner operations MUST be served on an owner-UID Unix socket. Task bearer holders
get only the structured browser actions below, never the control credential,
browser VM identity, debug socket, vault reader, screenshot or private input.
"""
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import threading
import time
from urllib.parse import urlsplit

from .control import Control, KitError
from .guest import seal, unseal
from .toolchains import install_status, start_install
from .vaults import PROVIDERS, private_bytes, read_login


def origin(url):
    u = urlsplit(url)
    if u.scheme != 'https' or not u.hostname or u.username or u.password or u.fragment or u.path not in ('', '/') or u.query:
        raise KitError('EXACT_HTTPS_ORIGIN_REQUIRED')
    return f'{u.scheme}://{u.netloc}'


def admission(linux_host=None):
    if linux_host:
        if not isinstance(linux_host, str) or linux_host.startswith('-'):
            raise KitError('ADMISSION_UNAVAILABLE')
        observed = subprocess.check_output(['ssh', '-o', 'BatchMode=yes', linux_host,
            'pgrep -c firecracker; free -m'], text=True, timeout=15).splitlines()
        live, free = int(observed[0]), '\n'.join(observed[1:])
    elif sys.platform == 'linux':
        count = subprocess.run(['pgrep', '-c', 'firecracker'], capture_output=True, text=True)
        if count.returncode not in (0, 1):
            raise KitError('ADMISSION_UNAVAILABLE')
        live = int(count.stdout.strip() or '0')
        free = subprocess.check_output(['free', '-m'], text=True)
    else:
        return
    available = int(next(line for line in free.splitlines() if line.startswith('Mem:')).split()[-1])
    if live >= 2 or available < 12 * 1024:
        raise KitError('OWNER_SANDBOX_CAPACITY')
    return {'live_firecracker': live, 'available_mib': available}


class Kits:
    def __init__(self, config, root):
        self.config, self.root = config, Path(root)
        self.lock = threading.Lock()
        self.control = Control(config['control_socket'], private_bytes(config['control_token_file']).decode().strip() if config.get('control_token_file') else '')
        self.state_file = self.root / 'browsers.json'
        self.browsers = json.loads(private_bytes(self.state_file)) if self.state_file.exists() else {}
        # Restart revokes ALL task grants/approvals and resumes in private state.
        self.tokens, self.proposals = {}, {}
        for value in self.browsers.values():
            value['private'] = True
        self.private_key = self.root / 'owner.pem'
        if not self.private_key.exists():
            result = subprocess.run(['openssl', 'genpkey', '-algorithm', 'RSA', '-pkeyopt', 'rsa_keygen_bits:3072', '-out', str(self.private_key)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            if result.returncode:
                raise KitError('OWNER_KEY_UNAVAILABLE')
            self.private_key.chmod(0o600)
        private_bytes(self.private_key)
        self.public_key = subprocess.check_output(['openssl', 'pkey', '-in', str(self.private_key), '-pubout'], stderr=subprocess.DEVNULL).decode()
        self.persist()

    def persist(self):
        temporary = self.root / ('browsers.' + secrets.token_hex(8) + '.new')
        fd = os.open(temporary, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
        try:
            with os.fdopen(fd, 'w') as out:
                json.dump(self.browsers, out)
                out.flush()
                os.fsync(out.fileno())
            os.replace(temporary, self.state_file)
            directory = os.open(self.root, os.O_RDONLY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if temporary.exists():
                temporary.unlink()

    def checked(self, value):
        agent = self.control.sandbox(value['agent'], allow_suspended=value['mode'] == 'external')
        browser = self.control.sandbox(value['sandbox'])
        if agent.get('generation') != value['agent_generation'] or browser.get('generation') != value['browser_generation']:
            value['private'] = True
            self.persist()
            raise KitError('GENERATION_CHANGED')

    def grant(self, identity):
        value = self.browsers[identity]
        self.checked(value)
        token = secrets.token_hex(32)
        self.tokens[hashlib.sha256(token.encode()).hexdigest()] = (identity, time.monotonic() + 300)
        return {'browser_id': identity, 'token': token, 'ttl_seconds': 300}

    @staticmethod
    def view(identity, value):
        return {'browser_id': identity, 'mode': value['mode'], 'private': value['private'],
                'warning': 'AGENT_CAN_READ_COPIED_LOGINS' if value['mode'] == 'copied' else None}

    def private(self, value):
        if value['mode'] != 'external':
            # Revoking this service cannot revoke root inside the agent's VM.
            raise KitError('PRIVATE_TAKEOVER_REQUIRES_EXTERNAL_BROWSER')
        value['private'] = True
        self.persist()  # fail closed even if the subsequent guest action fails
        self.control.browser(value['sandbox'], 'private')

    def dispatch(self, request, owner=False, token=''):
        if owner and request.get('operation') == 'task_dispatch':
            action = request['request']
            with self.lock:
                delegated = self.grant(action['browser_id'])['token']
            return self.dispatch(action, owner=False, token=delegated)
        with self.lock:
            now = time.monotonic()
            self.tokens = {k: v for k, v in self.tokens.items() if now < v[1]}
            self.proposals = {k: v for k, v in self.proposals.items() if now < v[1]}
            op = request.get('operation')
            if not isinstance(op, str):
                raise KitError('INVALID_ARGUMENT')
            if owner and op == 'create':
                return self.create(request)
            if owner and op == 'install':
                return start_install(self.control, request['sandbox_id'], request['tools'])
            if owner and op == 'install_status':
                return install_status(self.control, request['sandbox_id'], request['job_id'])
            identity = request.get('browser_id')
            if not owner:
                key = hashlib.sha256(token.encode()).hexdigest()
                grant = self.tokens.get(key)
                if not grant or time.monotonic() >= grant[1] or identity != grant[0]:
                    raise KitError('SCOPE_DENIED')
            if identity not in self.browsers:
                raise KitError('NOT_FOUND')
            value = self.browsers[identity]
            if owner and op == 'delete':
                if value['mode'] == 'external':
                    self.control.delete(value['sandbox'])
                # Copied mode shares the agent's VM. Never delete the workload.
                else:
                    self.control.browser(value['sandbox'], 'stop')
                del self.browsers[identity]
                self.tokens = {k: g for k, g in self.tokens.items() if g[0] != identity}
                self.persist()
                return {'deleted': True}
            self.checked(value)
            if not owner and value['private'] and op != 'get':
                raise KitError('PRIVATE_CONTROL_ACTIVE')
            if op == 'get':
                return self.view(identity, value)
            if owner and op == 'grant':
                return self.grant(identity)
            if owner and op == 'takeover':
                self.private(value)
                return self.view(identity, value)
            if owner and op == 'return':
                if value['mode'] != 'external' or not value['private']:
                    raise KitError('PRIVATE_TAKEOVER_REQUIRED')
                reply = self.control.browser(value['sandbox'], 'return')
                value['private'] = False
                self.persist()
                return reply
            if owner and op == 'revoke':
                self.tokens = {k: g for k, g in self.tokens.items() if g[0] != identity}
                if value['mode'] == 'external':
                    self.private(value)
                return {'revoked': True, 'warning': self.view(identity, value)['warning']}
            if owner and op == 'private_view':
                if not value['private'] or value['mode'] != 'external':
                    raise KitError('PRIVATE_TAKEOVER_REQUIRED')
                reply = self.control.browser(value['sandbox'], 'private_view')
                if type(reply.get('chunks')) is not int or not 1 <= reply['chunks'] <= 256:
                    raise KitError('PRIVATE_VIEW_TOO_LARGE')
                encrypted = bytearray()
                for index in range(reply['chunks']):
                    chunk = self.control.browser(value['sandbox'], 'view_chunk', view_id=reply['view_id'], index=index)
                    data = base64.b64decode(chunk['data'], validate=True)
                    if len(data) > 24576:
                        raise KitError('PRIVATE_VIEW_TOO_LARGE')
                    encrypted.extend(data)
                return unseal(self.private_key, json.loads(encrypted))
            if owner and op == 'private_type':
                if not value['private'] or value['mode'] != 'external':
                    raise KitError('PRIVATE_TAKEOVER_REQUIRED')
                envelope = seal(value['public_key'], {'selector': request['selector'], 'text': request['text']})
                return self.control.browser(value['sandbox'], 'private_type', sealed=envelope)
            if op == 'propose_fill':
                vault = request['vault_id']
                if vault not in self.config.get('vaults', {}) or value['mode'] != 'external':
                    raise KitError('PRIVATE_TYPING_REQUIRED')
                item = request.get('item_id')
                if len(self.proposals) >= 256 or not isinstance(item, str) or not 1 <= len(item) <= 256 or item.startswith('-'):
                    raise KitError('INVALID_ARGUMENT')
                proposal_id = secrets.token_hex(16)
                plan = {'browser_id': identity, 'vault_id': vault, 'item_id': item,
                        'origin': origin(request['origin']), 'submit': request.get('submit') is True,
                        'generation': value['browser_generation']}
                self.proposals[proposal_id] = (plan, time.monotonic() + 60)
                return {'approval_id': proposal_id, 'plan': plan, 'state': 'approval_required'}
            if owner and op == 'approval':
                proposal = self.proposals.get(request['approval_id'])
                if not proposal or time.monotonic() >= proposal[1] or proposal[0]['browser_id'] != identity:
                    raise KitError('APPROVAL_REQUIRED')
                return {'approval_id': request['approval_id'], 'plan': proposal[0], 'state': 'approval_required'}
            if owner and op == 'approve_fill':
                proposal = self.proposals.get(request['approval_id'])
                if not proposal or request.get('plan') != proposal[0]:
                    raise KitError('APPROVAL_PLAN_REQUIRED')
                del self.proposals[request['approval_id']]
                if not proposal or time.monotonic() >= proposal[1] or proposal[0]['browser_id'] != identity:
                    raise KitError('APPROVAL_REQUIRED')
                plan = proposal[0]
                if plan['generation'] != value['browser_generation']:
                    raise KitError('GENERATION_CHANGED')
                # A per-vault/item/origin/action one-use decision; never a global
                # unlock or an agent-supplied vault-reader command.
                self.private(value)
                credentials = read_login(self.config['vaults'][plan['vault_id']], plan['item_id'])
                if time.monotonic() >= proposal[1]:
                    raise KitError('APPROVAL_REQUIRED')
                credentials['origin'] = plan['origin']
                return self.control.browser(value['sandbox'], 'fill', sealed=seal(value['public_key'], credentials), submit=plan['submit'])
            if op in ('navigate', 'click', 'type', 'snapshot'):
                if value['private'] and not owner:
                    raise KitError('PRIVATE_CONTROL_ACTIVE')
                if owner and value['private'] and op in ('snapshot', 'type'):
                    raise KitError('USE_PRIVATE_VIEW_OR_TYPE')
                fields = {'navigate': ('url',), 'click': ('selector',), 'type': ('selector', 'text'), 'snapshot': ()}[op]
                args = {k: request[k] for k in fields}
                if any(not isinstance(v, str) or len(v) > 8192 for v in args.values()):
                    raise KitError('INVALID_ARGUMENT')
                if owner and value['private']:
                    return self.control.browser(value['sandbox'], 'private_action', sealed=seal(value['public_key'], dict(operation=op, **args)))
                if op == 'type':
                    # Even a refused password-field attempt must not put its text
                    # in the public exec argv/journal before the DOM check runs.
                    return self.control.browser(value['sandbox'], 'sealed_type', sealed=seal(value['public_key'], args))
                return self.control.browser(value['sandbox'], op, **args)
            raise KitError('SCOPE_DENIED')

    def create(self, request):
        mode = request.get('mode', 'external')
        agent = self.control.sandbox(request['agent_sandbox_id'], allow_suspended=mode == 'external')
        if mode not in ('external', 'copied'):
            raise KitError('INVALID_ARGUMENT')
        if mode == 'copied' and request.get('acknowledge_agent_can_read_logins') is not True:
            raise KitError('COPIED_LOGINS_REQUIRE_EXPLICIT_CONSENT')
        if mode == 'copied' and any(v['sandbox'] == agent['id'] for v in self.browsers.values()):
            raise KitError('BROWSER_ALREADY_ATTACHED')
        if mode == 'external' and 'cookies' in request:
            raise KitError('USE_PRIVATE_LOGIN')
        if request.get('source_browser_id'):
            if mode != 'copied' or 'cookies' in request:
                raise KitError('INVALID_ARGUMENT')
            source = self.browsers.get(request['source_browser_id'])
            if not source or source['mode'] != 'external' or not source['private']:
                raise KitError('PRIVATE_SOURCE_BROWSER_REQUIRED')
            self.checked(source)
            selected = self.control.browser(source['sandbox'], 'export_logins', origin=origin(request['login_origin']))
            request = dict(request, cookies=unseal(self.private_key, selected['sealed'])['cookies'])
        identity = request.get('browser_id') or ('br_' + secrets.token_hex(16))
        if identity in self.browsers:
            raise KitError('BROWSER_ALREADY_ATTACHED')
        sandbox = agent['id']
        observed = None
        if mode == 'external':
            observed = admission(self.config.get('linux_admission_host'))  # all tickets count
            sandbox = self.control.create(self.config['browser_template'], self.config.get('browser_resources',
                {'vcpus': 2, 'memory_mib': 4096, 'disk_gib': 20, 'pids_max': 4096}), browser_custody=True)
        value = {'agent': agent['id'], 'agent_generation': agent['generation'], 'sandbox': sandbox,
                 'browser_generation': 1, 'mode': mode, 'private': True, 'public_key': ''}
        self.browsers[identity] = value
        self.persist()  # retain cleanup identity before further effects
        try:
            value['browser_generation'] = self.control.sandbox(sandbox)['generation']
            self.control.install_worker(sandbox)
            reply = self.control.browser(sandbox, 'boot', owner_key=self.public_key)
            value['public_key'] = reply['public_key']
            if mode == 'copied' and 'cookies' in request:
                self.control.browser(sandbox, 'import', sealed=seal(value['public_key'], request['cookies']))
            value['private'] = False
            self.persist()
            return dict(self.view(identity, value), grant=self.grant(identity), allocation_admission=observed)
        except Exception as error:
            # Keep a failed allocation recorded until public Stop/Release proves
            # deletion. Do not erase evidence or pretend cleanup completed.
            value['private'] = True
            self.persist()
            phases = ('boot.key', 'boot.start', 'boot.cdp', 'boot.context')
            suffix = next((':' + p for p in phases if isinstance(error, KitError) and str(error) == 'GUEST_ACTION_FAILED:' + p), '')
            raise KitError('BROWSER_SETUP_FAILED_CLEANUP_REQUIRED' + suffix) from None
