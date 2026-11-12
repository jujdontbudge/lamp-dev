#!/usr/bin/env python3
"""Real copied-browser/toolchain journey using only one free LX slot.

This deliberately does NOT claim external browser/private takeover evidence.
Docker failure is recorded as a blocker, never substituted with host Docker.
"""
import argparse
import http.client
import json
from pathlib import Path
import socket
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from kits.control import Control
from kits.service import admission


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ('socket', 'token-file', 'template', 'root'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--linux-host', default='ubuntu-server')
    args = parser.parse_args()
    root = Path(args.root)
    root.mkdir(mode=0o700)
    config = {'control_socket': args.socket, 'control_token_file': args.token_file,
              'browser_template': args.template, 'linux_admission_host': args.linux_host}
    (root / 'config.json').write_text(json.dumps(config))
    (root / 'config.json').chmod(0o600)
    control = Control(args.socket, Path(args.token_file).read_text().strip())
    owner_socket = str(root / 'kits.sock')
    service = subprocess.Popen([sys.executable, '-m', 'kits', 'serve', '--config', str(root / 'config.json'),
        '--root', str(root), '--owner-socket', owner_socket, '--task-listen', '127.0.0.1:19223'],
        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    transcript = (root / 'transcript.jsonl').open('x')
    def record(step, value):
        transcript.write(json.dumps({'step': step, 'value': value}, sort_keys=True) + '\n')
        transcript.flush()
    def owner(value):
        conn = http.client.HTTPConnection('localhost', timeout=900)
        conn.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        conn.sock.settimeout(900)
        conn.sock.connect(owner_socket)
        return send(conn, '/owner', value)
    def send(conn, path, value, token=''):
        try:
            conn.request('POST', path, json.dumps(value), {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
            response = conn.getresponse()
            return response.status, json.loads(response.read(4 * 1024 * 1024))
        finally:
            conn.close()
    def install_tools(tools):
        code, job = owner({'operation': 'install', 'sandbox_id': agent, 'tools': tools})
        if code != 200:
            return code, job
        record('toolchain job accepted', job)
        while job['state'] == 'installing':
            time.sleep(1)
            code, job = owner({'operation': 'install_status', 'sandbox_id': agent, 'job_id': job['job_id']})
            if code != 200:
                return code, job
        return (200, job['result']) if job['state'] == 'ready' else (403, job)
    agent, browser_id = None, None
    try:
        deadline = time.monotonic() + 10
        while not Path(owner_socket).exists():
            if service.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('kit startup failed')
            time.sleep(.05)
        record('allocation admission', admission(args.linux_host))
        agent = control.create(args.template, {'vcpus': 2, 'memory_mib': 4096, 'disk_gib': 20, 'pids_max': 4096})
        record('real agent VM', {'id': agent, 'state': control.sandbox(agent)['state']})
        code, denied = owner({'operation': 'create', 'agent_sandbox_id': agent, 'mode': 'copied'})
        assert code == 403 and denied['error'] == 'COPIED_LOGINS_REQUIRE_EXPLICIT_CONSENT'
        record('copied login consent', denied)
        code, copied = owner({'operation': 'create', 'agent_sandbox_id': agent, 'mode': 'copied',
            'acknowledge_agent_can_read_logins': True, 'cookies': [{'name': 'disposable-login', 'value': 'public-demo-value', 'domain': 'example.com', 'path': '/', 'secure': True, 'httpOnly': True}]})
        assert code == 200, copied
        browser_id = copied['browser_id']
        record('real opt-in browser', {k: v for k, v in copied.items() if k != 'grant'})
        probe = "import importlib.util,json; s=importlib.util.spec_from_file_location('g','/tmp/lamp-kits-worker.py');g=importlib.util.module_from_spec(s);s.loader.exec_module(g); c=g.browser(); cookies=c.call('Storage.getCookies',browserContextId=json.loads(g.STATE.read_text())['context'])['cookies'];c.close();print(json.dumps({'copied_login_cookie_readable':any(x['name']=='disposable-login' and x['value']=='public-demo-value' for x in cookies)}))"
        observed = control.execute(agent, ['python3', '-c', probe])
        assert observed['copied_login_cookie_readable']
        record('agent can inspect opted-in copied login', observed)
        def task(op, **fields):
            return send(http.client.HTTPConnection('127.0.0.1', 19223, timeout=65), '/task',
                        dict(operation=op, browser_id=browser_id, **fields), copied['grant']['token'])
        assert task('navigate', url='https://the-internet.herokuapp.com/login')[0] == 200
        code, page = task('snapshot')
        assert code == 200 and 'Login Page' in page['text'], page
        record('real task browser work', {'title': page['title'], 'public_page_visible': True})
        code, denied = owner({'operation': 'takeover', 'browser_id': browser_id})
        assert code == 403 and denied['error'] == 'PRIVATE_TAKEOVER_REQUIRES_EXTERNAL_BROWSER'
        record('no false privacy in copied mode', denied)
        assert owner({'operation': 'delete', 'browser_id': browser_id})[0] == 200
        browser_id = None
        code, tools = install_tools(['git'])
        record('toolchain install', {'status': code, 'result': tools})
        if code == 200:
            record('installed tool useful work', control.execute(agent, ['python3', '-c', "import json,subprocess; print(json.dumps({'git':subprocess.check_output(['/tmp/lamp-toolchains/bin/git','--version'],text=True).strip()}))"]))
        code, docker = install_tools(['docker'])
        record('rootless Docker installation', {'status': code, 'result': docker})
        if code == 200:
            record('Docker useful command', control.execute(agent, ['python3', '-c', "import json,subprocess; print(json.dumps({'server':subprocess.check_output(['/tmp/lamp-toolchains/bin/docker','--host=unix:///tmp/lamp-kit-docker/docker.sock','version','--format','{{.Server.Version}}'],text=True).strip()}))"]))
    finally:
        if browser_id:
            code, result = owner({'operation': 'delete', 'browser_id': browser_id})
            record('browser cleanup', {'status': code, 'result': result})
        if agent:
            control.delete(agent)
            record('agent released', {'state': 'deleted'})
        service.terminate()
        service.wait(timeout=10)
        transcript.close()
    print(root / 'transcript.jsonl')


if __name__ == '__main__':
    main()
