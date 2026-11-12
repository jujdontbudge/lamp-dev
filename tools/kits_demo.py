#!/usr/bin/env python3
"""One disposable T22 journey. Run on M5 with a private LX API socket tunnel.

Needs a prepared x86 Linux browser template and the real keepassxc-cli. No trial
loops, owner profile/account discovery, rented services or host Docker commands.
"""
import argparse
import http.client
import json
import os
from pathlib import Path
import re
import secrets
import subprocess
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from kits.control import Control
from kits.service import admission


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--socket', required=True)
    p.add_argument('--token-file', required=True)
    p.add_argument('--template', required=True)
    p.add_argument('--linux-host', default='ubuntu-server')
    p.add_argument('--root', required=True)
    p.add_argument('--keepassxc', default='/opt/homebrew/bin/keepassxc-cli')
    args = p.parse_args()
    count = subprocess.check_output(['ssh', '-o', 'BatchMode=yes', args.linux_host,
        'pgrep -c firecracker || test "$?" = 1'], text=True, timeout=15)
    if int(count.strip() or '0') != 0:
        raise RuntimeError('this two-VM journey needs both LX slots; no guest allocated')
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700)
    home = root / 'vault-home'
    home.mkdir(mode=0o700)
    vault = root / 'disposable.kdbx'
    unlock = secrets.token_hex(24)
    (root / 'unlock').write_text(unlock)
    (root / 'unlock').chmod(0o600)
    def cli(argv, data):
        subprocess.run([args.keepassxc] + argv, input=data.encode(), stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, check=True, timeout=30)
    cli(['db-create', '-q', '-p', str(vault)], unlock + '\n' + unlock + '\n')
    # Read the public third-party test site's advertised disposable login. No
    # real owner's credential, browser profile or existing vault item is used.
    import html
    import urllib.request
    with urllib.request.urlopen('https://the-internet.herokuapp.com/login', timeout=40) as response:
        advertised = re.findall(r'<em>([^<]+)</em>', response.read(262144).decode())
    if len(advertised) != 2:
        raise RuntimeError('public fixture login instructions changed')
    username, password = map(html.unescape, advertised)
    cli(['add', '-q', '-u', username, '--url', 'https://the-internet.herokuapp.com/login', '-p', str(vault), 'login'],
        unlock + '\n' + password + '\n')
    vault.chmod(0o600)
    config = {'control_socket': args.socket, 'control_token_file': args.token_file,
              'browser_template': args.template, 'linux_admission_host': args.linux_host,
              'vaults': {'disposable': {'provider': 'keepassxc', 'binary': args.keepassxc,
                  'home': str(home), 'vault': str(vault), 'unlock_file': str(root / 'unlock')}}}
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
    def owner(request):
        import socket
        conn = http.client.HTTPConnection('localhost', timeout=900)
        conn.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        conn.sock.settimeout(900)
        conn.sock.connect(owner_socket)
        return send(conn, '/owner', request)
    def send(conn, path, request, token=''):
        try:
            conn.request('POST', path, json.dumps(request), {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + token})
            response = conn.getresponse()
            return response.status, json.loads(response.read(4 * 1024 * 1024))
        finally:
            conn.close()
    def task(op, **fields):
        return send(http.client.HTTPConnection('127.0.0.1', 19223, timeout=65), '/task',
                    dict(operation=op, browser_id=browser_id, **fields), token)
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
    agent, browser_id, copied_id = None, None, None
    try:
        deadline = time.monotonic() + 10
        while not Path(owner_socket).exists():
            if service.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('kit service startup failed')
            time.sleep(.05)
        # One agent VM and one external browser VM, all tickets included.
        record('allocation admission', admission(args.linux_host))
        agent = control.create(args.template, {'vcpus': 2, 'memory_mib': 4096, 'disk_gib': 20, 'pids_max': 4096})
        record('agent', {'id': agent, 'state': control.sandbox(agent)['state']})
        code, created = owner({'operation': 'create', 'agent_sandbox_id': agent})
        if code != 200:
            # Failed resources remain recorded for cleanup below.
            raise RuntimeError('external browser setup failed: ' + str(created))
        browser_id, token = created['browser_id'], created['grant']['token']
        record('default browser', {k: v for k, v in created.items() if k != 'grant'})
        assert task('navigate', url='https://the-internet.herokuapp.com/login')[0] == 200
        assert task('snapshot')[0] == 200
        code, proposed = task('propose_fill', vault_id='disposable', item_id='login', origin='https://the-internet.herokuapp.com', submit=True)
        assert code == 200
        record('per-vault approval proposed', proposed)
        code, reviewed = owner({'operation': 'approval', 'browser_id': browser_id, 'approval_id': proposed['approval_id']})
        assert code == 200
        record('owner reads authoritative approval plan', reviewed)
        code, filled = owner({'operation': 'approve_fill', 'browser_id': browser_id, 'approval_id': proposed['approval_id'], 'plan': reviewed['plan']})
        assert code == 200, filled
        record('real KeePassXC fill and submit', filled)
        code, denied = task('snapshot')
        assert code == 403 and denied['error'] == 'PRIVATE_CONTROL_ACTIVE'
        record('task fenced during takeover', denied)
        code, view = owner({'operation': 'private_view', 'browser_id': browser_id})
        assert code == 200 and 'png' in view
        assert view['url'].split('?')[0].endswith('/secure'), 'public test login did not finish'
        record('private authenticated page', {'login_completed': True})
        # Private screenshot is never printed/published. Only owner API receives it;
        # public exec logs hold ciphertext. Discard after observing successful read.
        record('owner private view', {'png_received': True})
        code, returned = owner({'operation': 'return', 'browser_id': browser_id})
        assert code == 200
        record('safe handback', returned)
        assert task('navigate', url='https://the-internet.herokuapp.com/secure')[0] == 200
        code, logged_in = task('snapshot')
        assert code == 200 and 'Secure Area' in logged_in['text'], logged_in
        record('useful authenticated work', {'secure_area_visible': True, 'session_survived_safe_handback': True})
        # Real owner takeover with sealed private typing, no task output.
        assert owner({'operation': 'takeover', 'browser_id': browser_id})[0] == 200
        assert owner({'operation': 'navigate', 'browser_id': browser_id, 'url': 'https://the-internet.herokuapp.com/login'})[0] == 200
        code, typed = owner({'operation': 'private_type', 'browser_id': browser_id, 'selector': '#password', 'text': password})
        assert code == 200
        record('private typing route', typed)
        code, denied = owner({'operation': 'create', 'agent_sandbox_id': agent, 'mode': 'copied'})
        assert code == 403 and denied['error'] == 'COPIED_LOGINS_REQUIRE_EXPLICIT_CONSENT'
        record('copied logins require opt-in', denied)
        # Deliberately copy the public test account's real HttpOnly session into
        # the agent VM. This never allocates a third sandbox or reads a profile.
        code, copied = owner({'operation': 'create', 'agent_sandbox_id': agent, 'mode': 'copied',
            'acknowledge_agent_can_read_logins': True, 'source_browser_id': browser_id,
            'login_origin': 'https://the-internet.herokuapp.com'})
        assert code == 200, copied
        copied_id = copied['browser_id']
        record('opt-in browser inside agent VM', {k: v for k, v in copied.items() if k != 'grant'})
        assert owner({'operation': 'delete', 'browser_id': browser_id})[0] == 200
        browser_id = None
        def copied_task(op, **fields):
            return send(http.client.HTTPConnection('127.0.0.1', 19223, timeout=65), '/task',
                dict(operation=op, browser_id=copied_id, **fields), copied['grant']['token'])
        assert copied_task('navigate', url='https://the-internet.herokuapp.com/secure')[0] == 200
        code, copied_page = copied_task('snapshot')
        assert code == 200 and 'Secure Area' in copied_page['text']
        record('copied login useful work', {'real_authenticated_session_inside_agent_vm': True})
        code, refused = owner({'operation': 'takeover', 'browser_id': copied_id})
        assert code == 403 and refused['error'] == 'PRIVATE_TAKEOVER_REQUIRES_EXTERNAL_BROWSER'
        record('cannot promise privacy inside agent VM', refused)
        assert owner({'operation': 'delete', 'browser_id': copied_id})[0] == 200
        copied_id = None
        code, tools = install_tools(['git', 'docker'])
        assert code == 200, tools
        record('toolchain install inside VM', tools)
        # Import a guest-local static BusyBox and run an actual nested container.
        # No remote image, host Docker client/daemon/socket or existing resource.
        script = '''import json,subprocess,tarfile,pathlib,time
sock='unix:///tmp/lamp-kit-docker/docker.sock'
def docker(args):return subprocess.check_output(['/tmp/lamp-toolchains/bin/docker','--host='+sock]+args,stderr=subprocess.DEVNULL,text=True).strip()
deadline=time.monotonic()+15
while True:
 try:docker(['info']);break
 except subprocess.CalledProcessError:
  if time.monotonic()>deadline:raise
  time.sleep(.1)
def root_owner(info):
 info.uid=info.gid=0;info.uname=info.gname='root';return info
with tarfile.open('/tmp/t22-busybox.tar','w') as t:t.add('/usr/bin/busybox',arcname='bin/busybox',filter=root_owner)
docker(['import','/tmp/t22-busybox.tar','lamp-t22-disposable'])
result=docker(['run','--rm','--network=none','lamp-t22-disposable','/bin/busybox','echo','container-inside-lamp'])
assert result=='container-inside-lamp'
print(json.dumps({'container_output':result,'guest_socket':sock,'git':subprocess.check_output(['git','--version'],text=True).strip()}))
'''
        record('real Docker container inside VM', control.execute(agent, ['python3', '-c', script]))
    finally:
        state = root / 'browsers.json'
        if state.exists() and service.poll() is None:
            for identity in list(json.loads(state.read_text())):
                code, value = owner({'operation': 'delete', 'browser_id': identity})
                record('browser cleanup', {'status': code, 'result': value})
        if agent:
            control.delete(agent)
            record('agent released', {'state': 'deleted'})
        service.terminate()
        service.wait(timeout=10)
        # The disposable vault/master password never become committed evidence.
        for path in (root / 'unlock', vault):
            path.unlink(missing_ok=True)
        transcript.close()
    print(root / 'transcript.jsonl')


if __name__ == '__main__':
    main()
