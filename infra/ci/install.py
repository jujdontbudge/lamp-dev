#!/usr/bin/env python3
"""Install this repository's private-only runner as a persistent user service.

Run headlessly on each owner host. Supply a short-lived repository runner
registration token on stdin; no PAT or registration token is stored by this tool.
Host Python + venv, Go and Rust must already exist (see README).
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import plistlib
import shlex
import shutil
import subprocess
import sys
import tarfile
import urllib.request
import zipfile

HERE = Path(__file__).resolve().parent


def run(args, **kwargs):
    subprocess.run([str(a) for a in args], check=True, **kwargs)


def download(url, path):
    with urllib.request.urlopen(url, timeout=60) as source, path.open('wb') as target:
        shutil.copyfileobj(source, target)


def install(lane, token):
    expected = {'m5': ('Darwin', 'arm64', 'osx-arm64'),
                'lx': ('Linux', 'x86_64', 'linux-x64'),
                'la': ('Linux', 'aarch64', 'linux-arm64')}[lane]
    if (platform.system(), platform.machine()) != expected[:2]:
        raise SystemExit('wrong host for lane ' + lane)
    prefix = Path.home() / '.local/share/lamp-ci'
    prefix.mkdir(parents=True, exist_ok=True, mode=0o700)
    prefix.chmod(0o700)
    runner = prefix / 'runner'
    if (runner / '.runner').exists():
        raise SystemExit('runner already configured; do not replace a live service')
    package = json.loads((HERE / 'runner-packages.json').read_text())
    version = package['version']; target = expected[2]
    archive = prefix / 'runner.tar.gz'
    download(f'https://github.com/actions/runner/releases/download/v{version}/actions-runner-{target}-{version}.tar.gz', archive)
    with archive.open('rb') as source:
        digest = hashlib.file_digest(source, 'sha256').hexdigest()
    if digest != package['sha256'][target]:
        raise SystemExit('runner checksum mismatch')
    runner.mkdir(exist_ok=True)
    with tarfile.open(archive) as tar:
        tar.extractall(runner, filter='data')
    archive.unlink()
    shutil.copy2(HERE / 'guard.py', prefix / 'guard.py')
    (prefix / 'guard.py').chmod(0o500)
    run([sys.executable, '-m', 'venv', prefix / 'guard-venv'])
    run([prefix / 'guard-venv/bin/pip', 'install', '--disable-pip-version-check', '-r', HERE / 'guard-requirements.txt'])
    hook = prefix / 'guard.sh'
    hook.write_text('#!/bin/sh\nexec ' + shlex.quote(str(prefix / 'guard-venv/bin/python')) + ' -I ' + shlex.quote(str(prefix / 'guard.py')) + '\n')
    hook.chmod(0o500)
    path = ':'.join([str(prefix / 'tools/protoc/bin'), str(Path.home() / '.cargo/bin'),
                     str(Path.home() / 'go-sdk/bin'), str(Path.home() / 'lamp-t1-tools/go/bin'),
                     '/opt/homebrew/bin', '/usr/local/bin', '/usr/bin', '/bin', '/usr/sbin', '/sbin'])
    env = dict(os.environ, PATH=path)
    # Token is short-lived and only passed to runner configuration, never logged.
    result = subprocess.run([str(runner / 'config.sh'), '--unattended', '--url', 'https://github.com/JohanSardar/lamp',
                             '--token', token, '--name', 'lamp-' + lane, '--labels', 'lamp-' + lane,
                             '--work', '_work', '--disableupdate'], cwd=runner, env=env,
                            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
    # Configuration output should not contain the token; redact defensively.
    print(result.stdout.replace(token, '[REDACTED]'))
    if result.returncode:
        raise SystemExit('runner configuration failed')
    # Match svc.sh: the release keeps this wrapper under bin until install.
    shutil.copy2(runner / 'bin/runsvc.sh', runner / 'runsvc.sh')
    (runner / 'runsvc.sh').chmod(0o700)
    (runner / '.path').write_text(path + '\n')
    (runner / '.env').write_text('ACTIONS_RUNNER_HOOK_JOB_STARTED=' + str(hook) + '\nDOTNET_CLI_TELEMETRY_OPTOUT=1\n')
    (runner / '.env').chmod(0o600)
    service = prefix / 'service.sh'
    service.write_text('#!/bin/sh\nset -eu\nif test -e ' + shlex.quote(str(prefix / 'PAUSED')) + '; then exit 0; fi\ncd ' + shlex.quote(str(runner)) + '\nexec ./runsvc.sh\n')
    service.chmod(0o700)
    if lane == 'lx':
        # Generation is checked on LX; this does not touch host Docker or apt.
        protoc = prefix / 'tools/protoc'
        protoc.mkdir(parents=True, exist_ok=True)
        z = prefix / 'protoc.zip'
        download('https://github.com/protocolbuffers/protobuf/releases/download/v35.0/protoc-35.0-linux-x86_64.zip', z)
        with zipfile.ZipFile(z) as archive:
            archive.extractall(protoc)
        (protoc / 'bin/protoc').chmod(0o755)
        z.unlink()
    if lane == 'm5':
        agents = Path.home() / 'Library/LaunchAgents'
        agents.mkdir(parents=True, exist_ok=True)
        label = 'com.lamp.ci.runner'
        plist = agents / (label + '.plist')
        with plist.open('wb') as f:
            plistlib.dump({'Label': label, 'ProgramArguments': [str(service)], 'WorkingDirectory': str(runner),
                           'RunAtLoad': True, 'KeepAlive': {'SuccessfulExit': False}, 'ThrottleInterval': 30,
                           'EnvironmentVariables': {'PATH': path},
                           'StandardOutPath': str(prefix / 'service.log'), 'StandardErrorPath': str(prefix / 'service-error.log')}, f)
        run(['launchctl', 'bootstrap', 'gui/' + str(os.getuid()), plist])
        lima = shutil.which('limactl', path=path)
        if not lima: raise SystemExit('Lima is required to restart LA at owner login')
        with (agents / 'com.lamp.ci.lima.plist').open('wb') as f:
            plistlib.dump({'Label': 'com.lamp.ci.lima', 'ProgramArguments': [lima, 'start', '--tty=false', 'lamp-la'],
                           'RunAtLoad': True, 'EnvironmentVariables': {'PATH': path},
                           'StandardOutPath': str(prefix / 'lima.log'), 'StandardErrorPath': str(prefix / 'lima-error.log')}, f)
        run(['launchctl', 'bootstrap', 'gui/' + str(os.getuid()), agents / 'com.lamp.ci.lima.plist'])
        control = f'''case "$1" in
pause) touch {shlex.quote(str(prefix / 'PAUSED'))}; launchctl kill SIGINT gui/{os.getuid()}/com.lamp.ci.runner ;;
resume) rm -f {shlex.quote(str(prefix / 'PAUSED'))}; launchctl kickstart -k gui/{os.getuid()}/com.lamp.ci.runner ;;
status) launchctl print gui/{os.getuid()}/com.lamp.ci.runner ;;
*) exit 2 ;;
esac
'''
    else:
        unitdir = Path.home() / '.config/systemd/user'
        unitdir.mkdir(parents=True, exist_ok=True)
        (unitdir / 'lamp-ci-runner.service').write_text(f'''[Unit]
Description=Lamp private owner-only CI runner ({lane.upper()})
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
WorkingDirectory={runner}
ExecCondition=/usr/bin/test ! -e {prefix}/PAUSED
ExecStart={service}
Environment=PATH={path}
Restart=on-failure
RestartSec=15
KillSignal=SIGINT
KillMode=mixed
TimeoutStopSec=300
UMask=0077
[Install]
WantedBy=default.target
''')
        run(['systemctl', '--user', 'daemon-reload'])
        run(['systemctl', '--user', 'enable', '--now', 'lamp-ci-runner.service'])
        control = f'''case "$1" in
pause) touch {shlex.quote(str(prefix / 'PAUSED'))}; systemctl --user stop lamp-ci-runner.service ;;
resume) rm -f {shlex.quote(str(prefix / 'PAUSED'))}; systemctl --user start lamp-ci-runner.service ;;
status) systemctl --user status lamp-ci-runner.service --no-pager ;;
*) exit 2 ;;
esac
'''
    ctl = prefix / 'runnerctl'
    ctl.write_text('#!/bin/sh\nset -eu\n' + control)
    ctl.chmod(0o700)
    print('installed lamp-' + lane + ' at ' + str(prefix))


if __name__ == '__main__':
    parser = argparse.ArgumentParser(); parser.add_argument('--lane', choices=['lx', 'm5', 'la'], required=True)
    args = parser.parse_args()
    registration = sys.stdin.readline().strip()
    if not registration or len(registration) > 1024:
        raise SystemExit('short-lived repository registration token required on stdin')
    install(args.lane, registration)
