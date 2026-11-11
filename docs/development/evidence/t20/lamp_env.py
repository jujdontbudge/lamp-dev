"""T20 demonstration environment (adapted from T19's) on lane LX: one private supervisor store,
one lampd with an enrolled owner and member, and a stdlib client that drives
the public HTTP API from the canonical contract routes. No timing is measured.
"""
import base64, hashlib, http.client, json, os, secrets, socket, subprocess, time, urllib.parse
from pathlib import Path

BASE = Path.home() / 'lamp-t20'
SRC = BASE / 'src'
SUP = str(SRC / 'target/release/lamp-supervisor')
STORE = '/var/lib/lamp/t20-private'
BIN = '/var/lib/lamp/t20-private-bin'
RUN = '/run/t20'
WORKER = RUN + '/w.sock'
ADMIN = RUN + '/admin.sock'
TEMPLATE = 'tp_6b295ecbcad03c2a3b7370e577'
OPS = json.loads((SRC / 'contracts/public/schema.json').read_text())['x-lamp-operations']


def sh(*argv, check=True, **kw):
    return subprocess.run(argv, check=check, capture_output=True, text=True, **kw)


class APIError(Exception):
    def __init__(self, status, body):
        super().__init__(f'{status} {body.get("error", body)}')
        self.status, self.body = status, body
        self.code = body.get('error', {}).get('code')


class Client:
    def __init__(self, sock, token, log):
        self.sock, self.token, self.log = sock, token, log

    def raw(self, method, path, headers, body):
        c = http.client.HTTPConnection('localhost', timeout=600)
        c.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        c.sock.settimeout(600)
        c.sock.connect(self.sock)
        headers = dict(headers, Authorization='Bearer ' + self.token)
        if body is not None:
            headers['Content-Type'] = 'application/json'
        c.request(method, path, None if body is None else json.dumps(body), headers)
        r = c.getresponse()
        data = json.loads(r.read() or b'{}')
        c.close()
        return r.status, data

    def op(self, name, fields=None, quiet=False):
        """Call one canonical operation by name; mutations wait up to 60 s."""
        o = OPS[name]
        fields = dict(fields or {})
        path, headers = o['path'], {}
        for k in list(fields):
            if '{' + k + '}' in path:
                path = path.replace('{' + k + '}', urllib.parse.quote(str(fields.pop(k)), safe=''))
        body = None
        if o['mutation']:
            headers['Idempotency-Key'] = fields.pop('idempotency_key', secrets.token_hex(8))
            if 'expected_revision' in fields:
                headers['If-Match'] = str(fields.pop('expected_revision'))
            fields.setdefault('wait_ms', 60000)
            body = fields
        elif fields:
            path += '?' + urllib.parse.urlencode(fields)
        status, data = self.raw(o['method'].upper(), path, headers, body)
        if not quiet:
            self.log(name, dict(request=dict(fields=fields, headers={k: v for k, v in headers.items()}), status=status, response=data))
        if status >= 400:
            raise APIError(status, data)
        if o['mutation'] and data.get('operation', {}).get('state') not in ('succeeded',):
            data = self.wait(data['operation_id'], quiet)
        return data

    def wait(self, op, quiet=False, timeout=900):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            status, data = self.raw('GET', f'/v1/operations/{op}/wait?timeout_ms=60000', {}, None)
            d = data.get('data', data)
            if d.get('state') in ('succeeded', 'failed', 'outcome_unknown', 'canceled'):
                if not quiet:
                    self.log('operation_wait', dict(operation_id=op, state=d.get('state'), error=d.get('error'), result=(d.get('outcome') or {}).get('result')))
                if d['state'] != 'succeeded':
                    raise APIError(409, dict(error=d.get('error') or {'code': d['state']}))
                return dict(operation=d, result=(d.get('outcome') or {}).get('result'), resource_ids=d.get('resource_ids'))
        raise TimeoutError(op)

    def get(self, kind, ident, quiet=True):
        return self.op(kind + '_get', {kind + '_id': ident}, quiet)['data']

    def run(self, sandbox, script, quiet=False):
        """Run a shell script as a task exec and return (exit, stdout)."""
        r = self.op('exec_create', dict(sandbox_id=sandbox, argv=['sh', '-c', script]), quiet=True)
        ex = (r.get('result') or {}).get('exec_id') or r['resource_ids'][0]['id']
        out, err, cursor = bytearray(), bytearray(), None
        while True:
            f = dict(exec_id=ex, wait_ms=20000)
            if cursor:
                f['cursor'] = cursor
            page = self.op('exec_watch', f, quiet=True)
            for fr in page['items']:
                if fr['channel'] == 'stdout':
                    out.extend(base64.b64decode(fr['data_base64']))
                elif fr['channel'] == 'stderr':
                    err.extend(base64.b64decode(fr['data_base64']))
            cursor = page.get('next_cursor') or cursor
            e = self.get('exec', ex)
            if e['state'] in ('exited', 'failed', 'outcome_unknown') and not page.get('next_cursor'):
                text = out.decode(errors='replace')
                if not quiet:
                    self.log('task exec', dict(sandbox=sandbox, script=script, state=e['state'], exit_code=e.get('exit_code'), stdout=text[-2000:], stderr=err.decode(errors='replace')[-2000:]))
                return e.get('exit_code'), text


def budget():
    """The LX rule: fewer than 2 Firecracker processes and ≥12 GiB available."""
    n = int(sh('pgrep', '-c', 'firecracker', check=False).stdout.strip() or 0)
    avail = int(sh('free', '-m').stdout.splitlines()[1].split()[6])
    return dict(firecracker=n, available_mib=avail)


class Env:
    def __init__(self, root):
        self.root = Path(root)
        self.root.mkdir(mode=0o700, exist_ok=False)
        self.private = self.root / 'private'
        self.private.mkdir(mode=0o700)
        self.transcript = (self.root / 'transcript.jsonl').open('x')
        self.worker = self.daemon = None
        self.grant = None
        self.owner = secrets.token_hex(32)
        self.member = secrets.token_hex(32)

    def log(self, step, value):
        self.transcript.write(json.dumps(dict(at=time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), step=step, value=value), sort_keys=True) + '\n')
        self.transcript.flush()
        print(step, json.dumps(value)[:300], flush=True)

    def install(self):
        sh('sudo', '-n', 'install', '-d', '-m', '0755', BIN)
        sh('sudo', '-n', 'install', '-m', '0755', str(SRC / 'target/release/lamp-guard'), BIN + '/lamp-guard')
        policy = json.loads(sh('sudo', '-n', 'cat', '/var/lib/lamp/t10-private-bin/network.json').stdout)
        policy.update(guard=BIN + '/lamp-guard', audit_dir='/var/lib/lamp-audit/t20-private')
        tmp = self.private / 'network.json'
        tmp.write_text(json.dumps(policy, indent=2))
        sh('sudo', '-n', 'install', '-m', '0644', str(tmp), BIN + '/network.json')
        sh('sudo', '-n', 'install', '-d', '-m', '0755', RUN)

    def start_worker(self):
        self.worker_log = (self.root / 'worker.log').open('a')
        pidfile = RUN + '/w.pid'
        self.worker = subprocess.Popen(['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', pidfile, SUP, 'serve',
                                        '--store', STORE, '--socket', WORKER, '--admin-socket', ADMIN, '--network-policy', BIN + '/network.json',
                                        '--mem-dir', RUN + '/m', '--netns-dir', RUN + '/n', '--uid-base', '2100000000', '--allow-uid', str(os.getuid())],
                                       stdout=self.worker_log, stderr=self.worker_log)
        deadline = time.monotonic() + 60
        while not (os.path.exists(WORKER) and os.path.exists(pidfile) and sh('sudo', '-n', 'cat', pidfile, check=False).stdout.strip()):
            if self.worker.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('worker did not start')
            time.sleep(.05)
        time.sleep(.5)
        self.worker_pid = int(sh('sudo', '-n', 'cat', pidfile).stdout)
        self.grant = json.loads(sh('sudo', '-n', SUP, 'grant-install', '--store', STORE, '--caller-uid', str(os.getuid()), '--ttl-s', '14400', '--max-generation', '64').stdout)
        self.log('worker started', dict(pid=self.worker_pid, grant_id=self.grant['grant_id'], supervisor_sha256=hashlib.sha256(Path(SUP).read_bytes()).hexdigest()))

    def stop_worker(self):
        if self.worker and self.worker.poll() is None:
            sh('sudo', '-n', 'kill', '-TERM', str(self.worker_pid))
            self.worker.wait(timeout=120)
        if self.grant:
            sh('sudo', '-n', SUP, 'grant-revoke', '--store', STORE, self.grant['grant_id'], check=False)
            self.grant = None
        self.log('worker stopped', dict(pid=self.worker_pid))

    def start_daemon(self, extra=()):
        identity = self.private / 'identity.json'
        identity.write_text(json.dumps(dict(owner_id='pr_t20_owner', bootstrap_sha256=hashlib.sha256(self.owner.encode()).hexdigest(),
                                            subjects=[dict(issuer='https://demo.invalid', subject='member-1', sha256=hashlib.sha256(self.member.encode()).hexdigest())])))
        identity.chmod(0o600)
        self.sock = str(self.private / 'api.sock')
        if os.path.exists(self.sock):
            os.remove(self.sock)
        self.daemon_log = (self.root / 'lampd.log').open('a')
        admin = json.dumps(['sudo', '-n', SUP, 'state', '--admin-socket', ADMIN])
        boot = Path('/proc/sys/kernel/random/boot_id').read_text().strip()
        self.daemon = subprocess.Popen([str(BASE / 'lampd'), '--identity-file', str(identity), '--state-dir', str(self.private / 'state'), '--socket', self.sock,
                                        '--worker-socket', WORKER, '--worker-id', 'lamp-supervisor@' + os.uname().nodename, '--worker-boot-id', boot,
                                        '--worker-uid', '0', '--worker-pid', str(self.worker_pid), '--grant-id', self.grant['grant_id'],
                                        '--worker-timeout', '60s', '--state-admin', admin, *extra], stdout=self.daemon_log, stderr=self.daemon_log)
        deadline = time.monotonic() + 30
        while not os.path.exists(self.sock):
            if self.daemon.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('lampd did not start')
            time.sleep(.02)
        self.api = Client(self.sock, self.owner, self.log)
        self.member_api = Client(self.sock, self.member, self.log)
        self.log('lampd started', dict(pid=self.daemon.pid, lampd_sha256=hashlib.sha256((BASE / 'lampd').read_bytes()).hexdigest()))

    def stop_daemon(self):
        if self.daemon and self.daemon.poll() is None:
            self.daemon.terminate()
            self.daemon.wait(timeout=120)
        self.log('lampd stopped', dict(pid=self.daemon.pid if self.daemon else None))

    def captures(self):
        return json.loads(sh('sudo', '-n', SUP, 'state', '--admin-socket', ADMIN, 'list').stdout)['captures']

    def close(self):
        try:
            self.stop_daemon()
            if self.worker and self.worker.poll() is None:
                self.log('captures left after lampd stopped', [c['capture_id'] for c in self.captures()])
        finally:
            self.stop_worker()
            self.log('final budget', budget())
            self.transcript.close()
