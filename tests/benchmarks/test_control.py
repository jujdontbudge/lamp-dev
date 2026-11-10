import unittest
from unittest import mock
from benchmarks.control import control_suite
from benchmarks.protocol import CONTROL_CALLERS, CONTROL_SAMPLES, CONTROL_P99_MS


class ControlProtocolTests(unittest.TestCase):
    def test_fixed_allocation_and_invalid_preparation(self):
        self.assertEqual((CONTROL_CALLERS, CONTROL_SAMPLES, CONTROL_P99_MS), (32, 200, 35))
        with self.assertRaisesRegex(ValueError, "exactly 32"):
            control_suite("http://localhost", None, [], None)
        callers = [dict(principal_id="pr", sandbox_id="sb", token_env="TOKEN")] * 32
        with self.assertRaisesRegex(ValueError, "at least two"):
            control_suite("http://localhost", None, callers, None)

    def test_bearers_cannot_be_loaded_from_a_manifest(self):
        callers = [dict(principal_id=f"p{i}", sandbox_id=f"s{i}", token_env=f"T{i}", token="not-allowed") for i in range(32)]
        with self.assertRaisesRegex(ValueError, "unknown fields"):
            control_suite("http://localhost", None, callers, None)
        for c in callers:
            del c["token"]
        with mock.patch.dict("os.environ", {}, clear=True):
            with self.assertRaisesRegex(ValueError, "environment"):
                control_suite("http://localhost", None, callers, None)

    def test_individual_failure_does_not_skip_remaining_commands(self):
        import base64
        import json
        import threading
        callers = [dict(principal_id=f'p{i}', sandbox_id=f's{i}', token_env=f'T{i}') for i in range(32)]
        lock = threading.Lock()
        attempts = 0
        identities = {}

        class Fixture:
            def __init__(self, token=None, **kwargs):
                self.token, self.calls = token, []

            def get(self, sandbox):
                return dict(owner_id=self.token, state='ready')

            def call(self, noun, action, fields):
                nonlocal attempts
                if action == 'create':
                    key = fields['idempotency_key']
                    with lock:
                        replay = key in identities
                        if not replay:
                            attempts += 1
                            if attempts == 1:
                                raise ValueError('one failed command')
                            identities[key] = (self.token, fields['argv'][1])
                    return dict(operation=dict(state='succeeded'), operation_id=key, result=dict(exec_id=key), replayed=replay)
                owner, marker = identities[fields['exec_id']]
                if self.token != owner:
                    import urllib.error
                    raise urllib.error.HTTPError('', 404, 'not found', {}, None)
                if action == 'watch':
                    return dict(items=[dict(channel='stdout', data_base64=base64.b64encode(marker.encode()).decode())])
                return dict(data=dict(owner_id=owner, state='exited', exit_code=0))

        class Evidence:
            metadata = {}
            def observe(self, kind, value):
                pass

        with mock.patch.dict('os.environ', {f'T{i}': f'p{i}' for i in range(32)}), mock.patch('benchmarks.control.PublicLamp', Fixture):
            result = control_suite(None, None, callers, Evidence(), socket_path='/fixture')
        self.assertEqual(attempts, 6400)
        self.assertEqual(result['trials'], 6400)
        self.assertEqual(result['failures'], 1)
        self.assertEqual(result['status'], 'fail')

    def test_unix_http_uses_public_headers_and_json(self):
        import json
        from http.server import BaseHTTPRequestHandler
        from pathlib import Path
        import socketserver
        import tempfile
        import threading
        from benchmarks.public import PublicLamp
        seen = []

        class Handler(BaseHTTPRequestHandler):
            def do_POST(self):
                seen.append((self.path, self.headers['Authorization'], self.headers['Idempotency-Key'], json.loads(self.rfile.read(int(self.headers['Content-Length'])))))
                body = b'{"operation_id":"op","result":{"exec_id":"ex"}}'
                self.send_response(200)
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)
            def log_message(self, *args):
                pass

        with tempfile.TemporaryDirectory(prefix='lamp-e03-', dir='/tmp') as directory:
            path = str(Path(directory) / 'api.sock')
            with socketserver.UnixStreamServer(path, Handler) as server:
                thread = threading.Thread(target=server.serve_forever)
                thread.start()
                try:
                    reply = PublicLamp(socket_path=path, token='fixture').call('exec', 'create', dict(sandbox_id='sb', argv=['/bin/true'], idempotency_key='same'))
                finally:
                    server.shutdown()
                    thread.join()
            self.assertEqual(reply['result']['exec_id'], 'ex')
            self.assertEqual(seen, [('/v1/execs', 'Bearer fixture', 'same', dict(sandbox_id='sb', argv=['/bin/true']))])

    def test_quota_contention_rejects_public_and_independent_overspend(self):
        import json
        import threading
        from benchmarks.control import quota_contention

        for admits, ghost, expected in ((1, False, 'pass'), (2, False, 'fail'), (1, True, 'fail')):
            effects, lock = {}, threading.Lock()

            class Client:
                def __init__(self, **kwargs):
                    self.calls = []
                def call(self, noun, action, fields):
                    return {'result': {'secret': 'never-record', 'key_id': 'ky'}} if action == 'create' else {'data': {'revision': 1}}
                def mutation(self, noun, action, fields):
                    import io
                    import urllib.error
                    with lock:
                        if len(effects) >= admits:
                            raise urllib.error.HTTPError('', 403, '', {}, io.BytesIO(b'{"error":{"code":"SCOPE_DENIED"}}'))
                        identity = f'ex_{len(effects)}'
                        effects[identity] = fields['argv'][-1]
                        return {'exec_id': identity}
                def execute(self, sandbox, argv):
                    if 'os.listdir' in argv[2]:
                        observed = {m: i + 10 for i, m in enumerate(effects.values())}
                        if ghost:
                            observed['unadmitted-marker'] = 99
                        return observed
                    return True
                def wait_exec(self, identity):
                    return {'marker': effects[identity]}

            class Evidence:
                def observe(self, kind, value):
                    self_outer.assertNotIn('never-record', json.dumps(value))

            def observer(*args, **kwargs):
                return mock.Mock(stdout=json.dumps({'sandbox_ids': ['sb_prepared']}))

            self_outer = self
            with mock.patch('benchmarks.control.PublicLamp', Client), mock.patch('benchmarks.control.subprocess.run', observer):
                result = quota_contention(Client(), dict(principal_id='pr', sandbox_id='sb_prepared'), Evidence(), None, '/socket', ['witness'])
            self.assertEqual(result['status'], expected, result)

    def test_all_requires_e03_in_completeness(self):
        import json
        from pathlib import Path
        import tempfile
        from benchmarks.run import main
        results = {}

        class Evidence:
            def __init__(self, *args):
                self.root = Path('/tmp')
            def observe(self, *args):
                pass
            def finish(self, value, **kwargs):
                results.update(value)
                return {'status': 'incomplete'}

        with tempfile.TemporaryDirectory() as directory:
            metadata = Path(directory) / 'metadata.json'
            metadata.write_text('{}')
            with mock.patch('sys.argv', ['benchmarks.run', '--suite', 'all', '--api-url', 'http://localhost', '--model', '--metadata', str(metadata), '--output', directory]), mock.patch('benchmarks.run.Evidence', Evidence), mock.patch('benchmarks.run.EngineHarness') as harness, mock.patch('benchmarks.run.storage_suite', return_value={'status': 'pass'}):
                harness.return_value.e01.return_value = {'status': 'pass'}
                harness.return_value.e02.return_value = {'status': 'pass'}
                self.assertEqual(main(), 1)
        self.assertEqual(set(results), {'E01', 'E02', 'E03', 'E04'})
        self.assertEqual(results['E03']['status'], 'incomplete')

    @unittest.skipUnless(__import__('sys').platform == 'linux', 'native Linux caller-process runtime')
    def test_process_callers_keep_full_count_on_two_owner_sandboxes(self):
        import base64
        import json
        import multiprocessing
        callers = [dict(principal_id=f'p{i%2}', sandbox_id=f's{i%2}', token_env=f'T{i}') for i in range(32)]
        runtime = multiprocessing.get_context('fork')
        attempts = runtime.Value('i', 0)
        identities = {}

        class Fixture:
            def __init__(self, token=None, **kwargs):
                self.token, self.calls = token, []
                self.owner = f'p{int(token[1:])%2}'
            def get(self, sandbox):
                return dict(owner_id=self.owner, state='ready')
            def call(self, noun, action, fields):
                if action == 'create':
                    key = fields['idempotency_key']
                    replay = key in identities
                    if not replay:
                        with attempts.get_lock():
                            attempts.value += 1
                            if attempts.value == 1:
                                raise ValueError('one failed command')
                        identities[key] = self.owner, fields['argv'][1]
                    return dict(operation=dict(state='succeeded'), operation_id=key, result=dict(exec_id=key), replayed=replay)
                owner, marker = identities[fields['exec_id']]
                if self.owner != owner:
                    import urllib.error
                    raise urllib.error.HTTPError('', 404, 'not found', {}, None)
                if action == 'watch':
                    return dict(items=[dict(channel='stdout', data_base64=base64.b64encode(marker.encode()).decode())])
                return dict(data=dict(owner_id=owner, state='exited', exit_code=0))

        class Evidence:
            metadata = {}
            runtime = None
            def observe(self, kind, value):
                if kind == 'caller_runtime':
                    self.runtime = value

        evidence = Evidence()
        with mock.patch.dict('os.environ', {f'T{i}': f'k{i}' for i in range(32)}), mock.patch('benchmarks.control.PublicLamp', Fixture):
            result = control_suite(None, None, callers, evidence, socket_path='/fixture', process_callers=True)
        self.assertEqual(attempts.value, 6400)
        self.assertEqual((result['trials'], result['failures'], result['principals'], result['prepared_sandboxes']), (6400, 1, 2, 2))
        self.assertEqual(len(set(evidence.runtime['pids'])), 32)
