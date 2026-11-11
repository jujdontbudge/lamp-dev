#!/usr/bin/env python3
"""One LX Docker journey against an owned kits_lx_demo_host.py fixture.

Uses one admitted VM, the public control API and the template's BusyBox package.
Always shuts down that fixture controller, including after an unknown outcome.
No host Docker command, socket, image, container or network is used.
"""
import argparse
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import time

from kits.control import Control
from kits.service import admission
from kits.toolchains import install_status, start_install


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--fixture', required=True)
    parser.add_argument('--root', required=True)
    parser.add_argument('--lamp', help='use canonical lamp kit create/install/status instead of the companion SDK')
    args = parser.parse_args()
    fixture = Path(args.fixture).resolve()
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700)
    controller = int((fixture / 'host.pid').read_text())

    def controller_alive():
        result = subprocess.run(['ps', '-p', str(controller), '-o', 'args='],
                                text=True, stdout=subprocess.PIPE)
        if result.returncode:
            return False
        argv = shlex.split(result.stdout)
        assert len(argv) > 1 and Path(argv[1]).name == 'kits_lx_demo_host.py'
        assert argv[argv.index('--root') + 1] == str(fixture)
        return True

    assert controller_alive()
    transcript = (root / 'transcript.jsonl').open('x')

    def record(operation, value):
        transcript.write(json.dumps({'operation': operation, 'value': value}, sort_keys=True) + '\n')
        transcript.flush()
        os.fsync(transcript.fileno())

    def interrupted(*_):
        raise KeyboardInterrupt()

    signal.signal(signal.SIGHUP, interrupted)
    signal.signal(signal.SIGTERM, interrupted)
    control = Control(str(fixture / 'api.sock'), (fixture / 'token').read_text())
    template = json.loads((fixture / 'template.json').read_text())['template_id']
    sandbox = None
    try:
        record('allocation admission', admission())
        sandbox = control.create(template, {'vcpus': 2, 'memory_mib': 4096, 'disk_gib': 20, 'pids_max': 4096})
        record('created disposable VM', {'sandbox_id': sandbox})
        runtime = control.execute(sandbox, ['python3', '-c',
            "import json,pathlib,stat; p=pathlib.Path('/run/containerd'); print(json.dumps({'runtime_mode':oct(stat.S_IMODE(p.stat().st_mode)),'uid_map':pathlib.Path('/proc/self/uid_map').read_text().strip(),'host_socket_present':pathlib.Path('/var/run/docker.sock').exists()}))"])
        assert runtime['runtime_mode'] == '0o1777'
        assert not runtime['host_socket_present']
        record('guest boot preparation and confinement', runtime)
        def lamp(action, body, resource=None):
            command = [args.lamp, 'kit', action]
            if resource: command.append(resource)
            result = subprocess.run(command + ['--socket', str(fixture / 'api.sock'), '--token-file', str(fixture / 'token'), '--input', '-', '--json'],
                input=json.dumps(body).encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            if result.returncode: raise RuntimeError('canonical Lamp kit request failed')
            return json.loads(result.stdout)
        kit_id = None
        if args.lamp:
            kit_id = lamp('create', {'name': 'Disposable Docker tools', 'tools': ['git', 'docker']})['result']['kit_id']
            revision = lamp('get', {}, kit_id)['data']['revision']
            job = lamp('install', {'sandbox_id': sandbox, 'expected_revision': revision}, kit_id)['result']
            record('canonical lamp kit installed', {'kit_id': kit_id})
        else:
            job = start_install(control, sandbox, ['git', 'docker'])
        record('started VM toolchain installation', job)
        while job['state'] == 'installing':
            time.sleep(1)
            job = lamp('install-status', {'job_id': job['job_id']}, kit_id)['data'] if args.lamp else install_status(control, sandbox, job['job_id'])
        record('VM toolchain installation outcome', job)
        if job['state'] != 'ready':
            # Only a pristine public-package fixture is eligible for this dump.
            diagnostic = "import json,pathlib\nlogs={}\nfor p in [pathlib.Path('/tmp/lamp-kit-docker/runtime.log'),pathlib.Path('/tmp/lamp-kit-jobs')/" + repr(job['job_id'] + '.bootstrap.log') + "]:\n if p.exists():\n  with p.open('rb') as f:\n   f.seek(max(0,p.stat().st_size-8192));logs[p.name]=f.read(8192).decode(errors='replace')\nprint(json.dumps({'native_startup_logs':logs}))"
            diagnostics = control.execute(sandbox, ['python3', '-c', diagnostic])
            record('pristine guest startup diagnostic', diagnostics)
        assert job['state'] == 'ready', 'VM kit installation failed'
        script = '''import json,os,pathlib,subprocess,tarfile
sock='unix:///tmp/lamp-kit-docker/docker.sock'
env=dict(os.environ,HOME='/tmp/lamp-kit-docker',DOCKER_CONFIG='/tmp/lamp-kit-docker/client')
def docker(args):
 result=subprocess.run(['/tmp/lamp-toolchains/bin/docker','--host='+sock]+args,env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
 if result.returncode:
  # This fresh fixture contains only public BusyBox and no registry credentials.
  print(json.dumps({'native_failure':result.stderr[-4096:],'docker_exit_code':result.returncode,'docker_args':args}))
  raise SystemExit(0)
 return result.stdout.strip()
info=json.loads(docker(['info','--format','{{json .}}']))
source=pathlib.Path('/tmp/lamp-toolchains/usr/bin/busybox')
if not source.exists():source=pathlib.Path('/usr/bin/busybox')
with tarfile.open('/tmp/t22-busybox.tar','w') as archive:
 entry=archive.gettarinfo(str(source),arcname='bin/busybox');entry.uid=entry.gid=0;entry.uname=entry.gname='root'
 with source.open('rb') as payload:archive.addfile(entry,payload)
docker(['import','/tmp/t22-busybox.tar','t22-disposable-busybox'])
output=docker(['run','--rm','--network=none','t22-disposable-busybox','/bin/busybox','printf','lamp-kit-docker-working'])
assert output=='lamp-kit-docker-working'
assert any('rootless' in option for option in info['SecurityOptions'])
print(json.dumps({'output':output,'server_version':info['ServerVersion'],'docker_root':info['DockerRootDir'],'security_options':info['SecurityOptions'],'docker_host':sock}))
'''
        result = control.execute(sandbox, ['python3', '-c', script])
        record('real isolated Docker container', result)
        assert result.get('output') == 'lamp-kit-docker-working', 'VM Docker operation failed'
    finally:
        signal.signal(signal.SIGHUP, signal.SIG_IGN)
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        try:
            if sandbox:
                control.delete(sandbox)
                record('public release', {'released': True})
        finally:
            if controller_alive():
                os.kill(controller, signal.SIGHUP)
                while controller_alive():
                    time.sleep(.1)
            result = subprocess.run(['pgrep', '-c', 'firecracker'], text=True, stdout=subprocess.PIPE)
            record('fixture teardown', {'controller_stopped': True, 'live_firecracker': int(result.stdout.strip() or 0)})
            transcript.close()
    print('T22 Docker journey passed; disposable VM and fixture released')


if __name__ == '__main__':
    main()
