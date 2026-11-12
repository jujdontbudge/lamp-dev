#!/usr/bin/env python3
"""T28 end-to-end demonstration on the owner's three machines, driven from the M5.

Builds signed releases, installs them on the Ubuntu laptop (systemd), the M5
and the MacBook Air, keeps owner work (a public third-party project tarball
saved as an artifact plus an encrypted backup) through restart, a tampered
release, a one-host-at-a-time rollout, a failing release that rolls back and
stops the rollout, anti-rollback, uninstall choices, and recovery of the
Linux host's backup on a fresh macOS install with only the owner's recovery
key. Writes a JSONL transcript. Secrets (release key, recovery key, bearers)
stay in a private work directory outside the repository and are never printed.

usage: tools/install_demo.py WORK_DIR TRANSCRIPT
"""
import json
import os
import shlex
import shutil
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORK = Path(sys.argv[1]).resolve()
OUT = Path(sys.argv[2]).resolve()
HOME = os.path.expanduser('~')
PROJECT = 'https://github.com/psf/requests/archive/refs/tags/v2.32.3.tar.gz'
HOSTS = [
    {'name': 'ubuntu', 'ssh': 'ubuntu-server', 'goos': 'linux', 'goarch': 'amd64', 'prefix': '/home/owner/lamp-t28/demo', 'service': 'systemd'},
    {'name': 'm5', 'ssh': None, 'goos': 'darwin', 'goarch': 'arm64', 'prefix': HOME + '/lt28/m5', 'service': 'process'},
    {'name': 'air', 'ssh': 'owner-air', 'goos': 'darwin', 'goarch': 'arm64', 'prefix': '/Users/owner/lt28/air', 'service': 'process'},
]
log = open(OUT, 'w')


def record(step, **kv):
    row = {'step': step, 'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), **kv}
    log.write(json.dumps(row) + '\n')
    log.flush()
    print(json.dumps(row)[:400])


def sh(cmd, check=True, **kw):
    p = subprocess.run(cmd, capture_output=True, text=True, **kw)
    if check and p.returncode != 0:
        raise SystemExit(f'failed: {cmd}\n{p.stdout}\n{p.stderr}')
    return p


def on(h, cmd, check=True):
    """Run a shell command on host h; returns (rc, stdout+stderr)."""
    argv = ['bash', '-lc', cmd] if h['ssh'] is None else ['ssh', h['ssh'], cmd]
    p = sh(argv, check=False)
    if check and p.returncode != 0:
        raise SystemExit(f'failed on {h["name"]}: {cmd}\n{p.stdout}\n{p.stderr}')
    return p.returncode, (p.stdout + p.stderr).strip()


def step(name, h, cmd, expect=0):
    rc, out = on(h, cmd, check=False)
    record(name, host=h['name'], command=cmd, rc=rc, output=out.splitlines()[-12:])
    if rc != expect:
        raise SystemExit(f'{name}: expected rc {expect}, got {rc}\n{out}')
    return out


def jline(out, key):
    for line in out.splitlines():
        if line.startswith('{') and key in line:
            return json.loads(line)
    raise SystemExit('no JSON line with ' + key)


def tool(h):
    return '"' + h['remote_dir'] + '/lamp-installer"'


def build_release(version, goos, goarch, bad=False, out=None):
    bins = WORK / f'bin-{goos}-{goarch}'
    out = out or WORK / f'release-{version}-{goos}-{goarch}'
    lampd = bins / 'lampd'
    if bad:
        # A correctly signed release whose daemon cannot start.
        lampd = WORK / 'bad-lampd'
        lampd.write_text('#!/bin/sh\necho "lampd: simulated broken release" >&2\nexit 1\n')
        lampd.chmod(0o755)
    shutil.rmtree(out, ignore_errors=True)
    sh([str(WORK / 'bin-host' / 'lamp-installer'), 'build', '--version', version, '--os', goos, '--arch', goarch,
        '--key', str(WORK / 'keys' / 'release.key'), '--out', str(out),
        '--file', f'bin/lampd={lampd}', '--file', f'bin/lamp={bins / "lamp"}', '--file', f'bin/lamp-installer={bins / "lamp-installer"}',
        '--file', f'LICENSE={ROOT / "LICENSE"}', '--file', f'THIRD_PARTY.md={ROOT / "THIRD_PARTY.md"}',
        '--file', f'licenses/relay-AGPL-3.0.txt={ROOT / "internal/relay/LICENSE"}',
        '--file', f'tools/guard-audit-release.py={ROOT / "tools/guard-audit-release.py"}'])
    return out


def main():
    WORK.mkdir(parents=True, exist_ok=True, mode=0o700)
    (WORK / 'keys').mkdir(exist_ok=True, mode=0o700)
    # Build host tool and per-platform binaries.
    sh(['go', 'build', '-o', str(WORK / 'bin-host') + '/', './cmd/lamp-installer'], cwd=ROOT)
    for goos, goarch in {('linux', 'amd64'), ('darwin', 'arm64')}:
        env = {**os.environ, 'GOOS': goos, 'GOARCH': goarch, 'CGO_ENABLED': '0'}
        sh(['go', 'build', '-trimpath', '-o', str(WORK / f'bin-{goos}-{goarch}') + '/', './cmd/lampd', './cmd/lamp', './cmd/lamp-installer'], cwd=ROOT, env=env)
    inst = str(WORK / 'bin-host' / 'lamp-installer')
    if not (WORK / 'keys' / 'release.key').exists():
        p = sh([inst, 'keygen', '--out', str(WORK / 'keys' / 'release.key'), '--public', str(WORK / 'keys' / 'release-trust.pub')])
        record('release key created outside the repository', output=p.stdout.strip())
        p = sh([inst, 'recovery-keygen', '--out', str(WORK / 'keys' / 'owner-recovery.key'), '--public', str(WORK / 'keys' / 'owner-recovery.pub')])
        record('owner recovery material created (kept off the hosts; only the public key is installed)', output=p.stdout.strip())
    sh(['curl', '-fsSL', '-o', str(WORK / 'requests-2.32.3.tar.gz'), PROJECT])
    releases = {}
    for v in ['0.1.0', '0.2.0']:
        for goos, goarch in {('linux', 'amd64'), ('darwin', 'arm64')}:
            releases[(v, goos)] = build_release(v, goos, goarch)
    for goos, goarch in {('linux', 'amd64'), ('darwin', 'arm64')}:
        releases[('0.3.0', goos)] = build_release('0.3.0', goos, goarch, bad=True)
    # Tampered copy of 0.2.0: one payload byte changed after signing.
    tampered = WORK / 'release-0.2.0-linux-amd64-tampered'
    shutil.rmtree(tampered, ignore_errors=True)
    shutil.copytree(releases[('0.2.0', 'linux')], tampered)
    f = tampered / 'payload' / 'bin' / 'lamp'
    data = bytearray(f.read_bytes()); data[4096] ^= 1; f.write_bytes(bytes(data))
    record('releases built', releases=sorted(f'{v} {o}' for v, o in releases), tampered=str(tampered.name),
           manifest_components=len(json.loads((releases[('0.1.0', 'linux')] / 'manifest.json').read_text())['components']['bin/lampd']))

    # Ship tools, releases, SDK and workload to each host's private demo dir.
    for h in HOSTS:
        h['remote_dir'] = h['prefix'] + '-kit'
        on(h, f'mkdir -p {shlex.quote(h["remote_dir"])} && chmod 700 {shlex.quote(h["remote_dir"])}')
        files = [str(WORK / f'bin-{h["goos"]}-{h["goarch"]}' / 'lamp-installer'), str(WORK / 'keys' / 'release-trust.pub'),
                 str(WORK / 'keys' / 'owner-recovery.pub'), str(WORK / 'requests-2.32.3.tar.gz'), str(ROOT / 'tools' / 'install_demo_work.py')]
        rels = [str(releases[(v, h['goos'])]) for v in ['0.1.0', '0.2.0', '0.3.0']] + ([str(tampered)] if h['name'] == 'ubuntu' else [])
        if h['ssh'] is None:
            for x in files + rels:
                sh(['cp', '-R', x, h['remote_dir'] + '/'])
        else:
            sh(['scp', '-q', '-r'] + files + rels + [f'{h["ssh"]}:{h["remote_dir"]}/'])
        h['rel'] = lambda v, h=h: f'{h["remote_dir"]}/release-{v}-{h["goos"]}-{h["goarch"]}'

    ubuntu, m5, air = HOSTS
    # Install 0.1.0 everywhere: signed, pinned trust, automatic updates off.
    for h in HOSTS:
        step('install 0.1.0', h, f'{tool(h)} install --release {h["rel"]("0.1.0")} --prefix {h["prefix"]} --name t28demo --trust {h["remote_dir"]}/release-trust.pub --service {h["service"]} --recovery-public-key {h["remote_dir"]}/owner-recovery.pub')
        step('status after install', h, f'{tool(h)} status --prefix {h["prefix"]}')
    if ubuntu['service'] == 'systemd':
        step('systemd unit active', ubuntu, 'systemctl is-active lamp-t28demo.service && systemctl is-enabled lamp-t28demo.service')
    step('automatic updates start disabled: timer run is a no-op', ubuntu, f'{tool(ubuntu)} auto --prefix {ubuntu["prefix"]} --release {ubuntu["rel"]("0.2.0")}')

    # Owner work through the public API on each host.
    work = {}
    for h in HOSTS:
        out = step('save owner work via the public API', h, f'python3 {h["remote_dir"]}/install_demo_work.py save {h["prefix"]} {h["remote_dir"]}/requests-2.32.3.tar.gz job_t28_{h["name"]}')
        work[h['name']] = jline(out, 'artifact_id')
    # TypeScript SDK on the M5 against the same daemon.
    ts = ("node --input-type=module -e \"import {LampClient} from '" + str(ROOT / 'sdk/typescript/index.js') + "';"
          "import fs from 'node:fs';const P='" + m5['prefix'] + "';"
          "const c=new LampClient({socket:P+'/run/api.sock',token:fs.readFileSync(P+'/config/owner.token','utf8').trim()});"
          "const a=await c.backupList({});const op=await c.blobCreate({size_bytes:2,media_type:'text/plain'});const w=await c.waitOperation(op.operation_id);"
          "console.log(JSON.stringify({ts_sdk:true,backups:a.items.map(b=>[b.id,b.recovery_state]),blob_create:w.state}))\"")
    step('TypeScript SDK call', m5, ts)

    # Restart keeps saved work.
    step('restart service (systemd)', ubuntu, 'sudo -n systemctl restart lamp-t28demo.service && sleep 1 && systemctl is-active lamp-t28demo.service')
    step('saved work after restart', ubuntu, f'python3 {ubuntu["remote_dir"]}/install_demo_work.py check {ubuntu["prefix"]} {work["ubuntu"]["artifact_id"]} {work["ubuntu"]["sha256"]}')

    # Tampered release refused before apply.
    step('tampered release refused before apply', ubuntu, f'{tool(ubuntu)} update --prefix {ubuntu["prefix"]} --release {ubuntu["remote_dir"]}/release-0.2.0-linux-amd64-tampered', expect=1)

    # One host at a time: good release completes on all three.
    # Rollout runs from the M5's installed tool; each remote host verifies with its own installed, pinned-trust tool.
    def hosts_for(v):
        return ' '.join(f'--host {h["ssh"] or "local"}={h["prefix"]}={releases[(v, h["goos"])] if h["ssh"] else h["rel"](v)}' for h in HOSTS)
    rel_args = [hosts_for('0.2.0')]
    rc, out = on(m5, f'{m5["prefix"]}/current/bin/lamp-installer rollout ' + ' '.join(rel_args), check=False)
    record('rollout 0.2.0 one host at a time', host='m5', rc=rc, output=[l for l in out.splitlines() if l.startswith('{')])
    if rc != 0:
        raise SystemExit('rollout 0.2.0 failed')
    for h in HOSTS:
        step('saved work after 0.2.0', h, f'python3 {h["remote_dir"]}/install_demo_work.py check {h["prefix"]} {work[h["name"]]["artifact_id"]} {work[h["name"]]["sha256"]}')

    # A signed but broken release: first host fails its check, rolls back, rollout stops.
    bad = [hosts_for('0.3.0')]
    rc, out = on(m5, f'{m5["prefix"]}/current/bin/lamp-installer rollout ' + ' '.join(bad), check=False)
    record('rollout of broken 0.3.0 stops after first host', host='m5', rc=rc, output=[l for l in out.splitlines() if l.startswith('{')])
    if rc != 3:
        raise SystemExit('broken rollout did not stop')
    for h in HOSTS:
        step('version after stopped rollout', h, f'{tool(h)} status --prefix {h["prefix"]}')
    step('saved work after rollback', ubuntu, f'python3 {ubuntu["remote_dir"]}/install_demo_work.py check {ubuntu["prefix"]} {work["ubuntu"]["artifact_id"]} {work["ubuntu"]["sha256"]}')
    step('anti-rollback refuses an older signed release', ubuntu, f'{tool(ubuntu)} update --prefix {ubuntu["prefix"]} --release {ubuntu["rel"]("0.1.0")}', expect=1)
    step('owner opts in to automatic updates', air, f'{tool(air)} policy --prefix {air["prefix"]} --automatic true')
    step('automatic update now applies (still verified)', air, f'{tool(air)} auto --prefix {air["prefix"]} --release {air["rel"]("0.3.0")}', expect=3)

    # Recovery: the Linux host's sealed archive leaves as an ordinary offsite copy.
    arch = ubuntu['remote_dir'] + '/offsite'
    step('copy sealed backup offsite', ubuntu, f'mkdir -p {arch} && cp {ubuntu["prefix"]}/state/control/backups/{work["ubuntu"]["backup_id"]}.lbk {arch}/ && ls -l {arch}')
    step('uninstall without a data choice changes nothing', ubuntu, f'{tool(ubuntu)} uninstall --prefix {ubuntu["prefix"]}', expect=1)
    step('uninstall Ubuntu, delete owner data and host key store', ubuntu, f'{tool(ubuntu)} uninstall --prefix {ubuntu["prefix"]} --data delete --confirm-delete t28demo && ! test -e {ubuntu["prefix"]} && ! systemctl cat lamp-t28demo.service 2>/dev/null')
    local_arch = WORK / 'offsite'
    shutil.rmtree(local_arch, ignore_errors=True)
    sh(['scp', '-q', '-r', f'{ubuntu["ssh"]}:{arch}', str(local_arch)])
    on(ubuntu, f'rm -rf {shlex.quote(ubuntu["remote_dir"])}')
    # Replacement: a fresh macOS install on the Air, separate prefix, no old state.
    rep = dict(air, prefix='/Users/owner/lt28/replacement')
    sh(['scp', '-q', '-r', str(local_arch), f'{air["ssh"]}:{air["remote_dir"]}/offsite'])
    step('fresh install on replacement OS (macOS, Air)', rep, f'{tool(air)} install --release {air["rel"]("0.2.0")} --prefix {rep["prefix"]} --name t28replacement --trust {air["remote_dir"]}/release-trust.pub --service process --recovery-public-key {air["remote_dir"]}/owner-recovery.pub')
    # Wrong material is refused.
    wrong = WORK / 'keys' / 'wrong-recovery.key'
    if not wrong.exists():
        sh([inst, 'recovery-keygen', '--out', str(wrong), '--public', str(WORK / 'keys' / 'wrong-recovery.pub')])
    sh(['scp', '-q', str(wrong), f'{air["ssh"]}:{air["remote_dir"]}/wrong-recovery.key'])
    step('recovery with a different key is refused', rep, f'chmod 600 {air["remote_dir"]}/wrong-recovery.key && {tool(air)} recover --prefix {rep["prefix"]} --archives {air["remote_dir"]}/offsite --recovery-key {air["remote_dir"]}/wrong-recovery.key; rc=$?; tail -1 {rep["prefix"]}/log/lampd.log; exit $rc', expect=1)
    # The owner brings the recovery material for the ceremony and removes it after.
    sh(['scp', '-q', str(WORK / 'keys' / 'owner-recovery.key'), f'{air["ssh"]}:{air["remote_dir"]}/owner-recovery.key'])
    step('import with owner recovery key', rep, f'chmod 600 {air["remote_dir"]}/owner-recovery.key && {tool(air)} recover --prefix {rep["prefix"]} --archives {air["remote_dir"]}/offsite --recovery-key {air["remote_dir"]}/owner-recovery.key; rc=$?; rm -f {air["remote_dir"]}/owner-recovery.key {air["remote_dir"]}/wrong-recovery.key; exit $rc')
    step('restore and verify bytes on replacement', rep, f'python3 {air["remote_dir"]}/install_demo_work.py restore {rep["prefix"]} {work["ubuntu"]["sha256"]}')
    step('recovery key not in service args or state', rep, f'! grep -rl owner-recovery.key {rep["prefix"]}/config/install.json; ! grep -rl private_key {rep["prefix"]}/state; echo clean')

    # Remaining uninstall choices.
    step('uninstall M5, retain owner data', m5, f'{tool(m5)} uninstall --prefix {m5["prefix"]} --data retain && ls {m5["prefix"]}/state/control | head -5 && ! test -e {m5["prefix"]}/current')
    step('uninstall Air, export owner data', air, f'{tool(air)} uninstall --prefix {air["prefix"]} --data export --export-dir {air["remote_dir"]}/export && ls {air["remote_dir"]}/export/control/backups')
    step('uninstall replacement, delete', rep, f'{tool(air)} uninstall --prefix {rep["prefix"]} --data delete --confirm-delete t28replacement')
    # Disposable demo directories go away.
    on(m5, f'rm -rf {m5["prefix"]} {m5["remote_dir"]}')
    on(air, f'rm -rf {air["prefix"]} {air["remote_dir"]} /Users/owner/lt28')
    record('demo complete')


if __name__ == '__main__':
    main()
