#!/usr/bin/env python3
"""T28 part 2: Lamp's own fleet rolls a signed update one host at a time.

Three hosts form one Lamp fleet (Raft over pairwise SSH tunnels with host
mTLS, as in T16): the Ubuntu laptop (systemd units), the M5 and the Air (user
LaunchAgents in gui/<uid>). Each runs lampd plus the lamp-installer updater
agent as separate services. The M5 lampd is enrolled with its VZ supervisor and
runs a real Linux VM sandbox with a live guest process.

Shown: automatic updates off by default; `lamp fleet update --update-policy-json`
turning them on; Lamp creating the Update itself and rolling 3.1.0 through
hs_air -> hs_lx -> hs_m5 with update_get observed throughout; the sandbox and
its guest process surviving the M5 update; a tampered release refused with
INVALID_ARGUMENT; a signed but broken 3.2.0 failing its check on the first host,
rolling back and stopping the rollout; LaunchAgent restart and removal.

usage: tools/fleet_update_demo.py WORK_DIR EVIDENCE_DIR
Secrets (release key, bearer, host keys) stay in WORK_DIR, never in evidence.
"""
import hashlib
import json
import os
import secrets
import shlex
import shutil
import subprocess
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
WORK = Path(sys.argv[1]).resolve()
EVID = Path(sys.argv[2]).resolve()
EVID.mkdir(parents=True, exist_ok=True)
LOG = open(EVID / 'transcript.jsonl', 'w')
HOME = os.path.expanduser('~')
TEMPLATE = 'tp_12cf5d609585b24c3beee70840'  # Ubuntu 26.04 arm64 in the M5 store
STORE = '/private/var/lamp/store'
SUP = '/usr/local/lib/lamp/lamp-supervisor'
H = {
    'hs_lx': dict(ssh='ubuntu-server', goos='linux', goarch='amd64', base='/home/johan/lamp-t28', service='systemd', os='linux', arch='x86_64'),
    'hs_m5': dict(ssh=None, goos='darwin', goarch='arm64', base=HOME + '/lt28', service='launchd', os='darwin', arch='arm64'),
    'hs_air': dict(ssh='johansardars-macbook-air', goos='darwin', goarch='arm64', base='/Users/johansardar/lt28', service='launchd', os='darwin', arch='arm64'),
}
ORDER = ['hs_lx', 'hs_m5', 'hs_air']
PORT = 31000 + secrets.randbelow(4000)
PORTS = {h: PORT + 10 * i for i, h in enumerate(ORDER)}
for h, c in H.items():
    c['prefix'] = c['base'] + '/f'
    c['kit'] = c['base'] + '/kit'
TOKEN = None
CLEAN = []


def record(step, **kv):
    row = {'step': step, 'at': time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()), **kv}
    LOG.write(json.dumps(row) + '\n')
    LOG.flush()
    print(json.dumps(row)[:300], flush=True)


def sh(argv, check=True, **kw):
    p = subprocess.run(argv, capture_output=True, text=True, **kw)
    if check and p.returncode:
        raise SystemExit(f'failed: {argv}\n{p.stdout}\n{p.stderr}')
    return p


def on(h, cmd, check=True):
    c = H[h]
    argv = ['bash', '-c', cmd] if c['ssh'] is None else ['ssh', '-o', 'BatchMode=yes', c['ssh'], cmd]
    p = sh(argv, check=False)
    if check and p.returncode:
        raise SystemExit(f'failed on {h}: {cmd}\n{p.stdout}\n{p.stderr}')
    return p.returncode, (p.stdout + p.stderr).strip()


def put(h, path, data, mode=0o600):
    local = WORK / 'put.tmp'
    local.write_bytes(data if isinstance(data, bytes) else data.encode())
    local.chmod(mode)
    if H[h]['ssh'] is None:
        shutil.copy(local, path)
        os.chmod(path, mode)
    else:
        sh(['scp', '-q', '-p', str(local), f'{H[h]["ssh"]}:{path}'])


def copy(h, src, dst):
    if H[h]['ssh'] is None:
        sh(['cp', '-R', str(src), dst])
    else:
        sh(['scp', '-q', '-r', str(src), f'{H[h]["ssh"]}:{dst}'])


def tool(h):
    return shlex.quote(H[h]['kit'] + '/lamp-installer')


def lamp(args, host='hs_m5', check=True):
    """The lamp CLI against a host's local socket; the bearer goes by env."""
    c = H[host]
    env = f'LAMP_SOCKET={c["prefix"]}/run/api.sock LAMP_TOKEN="$(cat {c["kit"]}/owner.token)"'
    rc, out = on(host, f'{env} {c["kit"]}/lamp {args} --json', check=False)
    try:
        body = json.loads(out.splitlines()[-1]) if out else {}
    except ValueError:
        body = {'raw': out}
    if check and rc:
        raise SystemExit(f'lamp {args} on {host}: {out}')
    return rc, body


def updates(host='hs_m5'):
    _, b = lamp('update list', host, check=False)
    return sorted(b.get('items', []), key=lambda u: u['created_at'])


def brief(u):
    return {k: u.get(k) for k in ['id', 'state', 'current_host_id', 'host_ids', 'release_digest', 'metadata_versions', 'security_revocations', 'migration', 'rollback_ref', 'labels', 'revision']}


def watch_update(want_terminal, label, timeout=900, skip=()):
    """Poll update_get and record each state change of the newest Update."""
    seen, deadline = None, time.time() + timeout
    while time.time() < deadline:
        for host in ['hs_m5', 'hs_air', 'hs_lx']:
            us = updates(host)
            if us:
                break
        us = [x for x in us if x['id'] not in skip]
        if us:
            u = us[-1]
            key = (u['id'], u['state'], u.get('current_host_id'))
            if key != seen:
                seen = key
                record(label + ': update_get', update=brief(u))
            if u['state'] in want_terminal:
                return u
        time.sleep(3)
    raise SystemExit(label + ': timed out')


def guest(sb, script):
    """Run a shell script in the sandbox through Lamp and return its stdout."""
    for attempt in range(20):
        rc, b = lamp(f"exec create --sandbox-id {sb} --argv-json {shlex.quote(json.dumps(['sh', '-c', script]))} --wait-ms 60000", check=False)
        if rc == 0:
            break
        time.sleep(3)
    else:
        raise SystemExit(f'exec create: {b}')
    ex = b['result']['exec_id'] if 'result' in b else b['resource_ids'][0]['id']
    deadline = time.time() + 90
    while time.time() < deadline:
        _, e = lamp(f'exec get {ex}')
        if e['data']['state'] in ('exited', 'failed', 'killed', 'completed', 'succeeded'):
            break
        time.sleep(1)
    _, w = lamp(f'exec watch {ex}', check=False)
    frames = w.get('frames') or w.get('items') or []
    import base64
    out = ''.join(base64.b64decode(f.get('data_base64', '')).decode(errors='replace') for f in frames if f.get('stream', 'stdout') == 'stdout')
    return out.strip(), e['data'], w


def build_releases():
    inst = WORK / 'bin-host' / 'lamp-installer'
    rels = WORK / 'fleet-releases'
    if (rels / '3.2.0' / 'index.json').exists() and not os.environ.get('REBUILD'):
        record('reusing signed releases built earlier', versions=sorted(os.listdir(rels)))
        return rels
    sh(['go', 'build', '-o', str(WORK / 'bin-host') + '/', './cmd/lamp-installer'], cwd=ROOT)
    for goos, goarch in [('linux', 'amd64'), ('darwin', 'arm64')]:
        env = {**os.environ, 'GOOS': goos, 'GOARCH': goarch, 'CGO_ENABLED': '0'}
        sh(['go', 'build', '-trimpath', '-o', str(WORK / f'bin-{goos}-{goarch}') + '/', './cmd/lampd', './cmd/lamp', './cmd/lamp-installer'], cwd=ROOT, env=env)
    bad = WORK / 'bad-lampd'
    bad.write_text('#!/bin/sh\necho "lampd: simulated broken release" >&2\nexit 1\n')
    bad.chmod(0o755)
    key = WORK / 'keys' / 'release.key'
    rels = WORK / 'fleet-releases'
    shutil.rmtree(rels, ignore_errors=True)
    for v, mv, broken in [('3.0.0', 10, False), ('3.1.0', 11, False), ('3.2.0', 12, True)]:
        for goos, goarch in [('linux', 'amd64'), ('darwin', 'arm64')]:
            b = WORK / f'bin-{goos}-{goarch}'
            sh([str(inst), 'build', '--version', v, '--os', goos, '--arch', goarch, '--key', str(key), '--out', str(rels / v),
                '--file', f'bin/lampd={bad if broken else b / "lampd"}', '--file', f'bin/lamp={b / "lamp"}', '--file', f'bin/lamp-installer={b / "lamp-installer"}',
                '--file', f'LICENSE={ROOT / "LICENSE"}', '--file', f'THIRD_PARTY.md={ROOT / "THIRD_PARTY.md"}', '--file', f'licenses/relay-AGPL-3.0.txt={ROOT / "internal/relay/LICENSE"}'])
        args = [str(inst), 'index', '--release', str(rels / v), '--key', str(key), '--metadata-version', str(mv)]
        if v == '3.1.0':
            args += ['--revoke', 'sha256:' + '0' * 64 + '=example: an earlier release withdrawn for a security fix']
        out = sh(args).stdout.strip()
        record('release built and indexed', version=v, broken=broken, output=out)
    # Tampered: 3.1.1 is 3.1.0 with its signed index edited after signing.
    t = rels / '3.1.1-tampered'
    shutil.copytree(rels / '3.1.0', t)
    ix = json.loads((t / 'index.json').read_text())
    ix['version'] = '3.1.1'
    (t / 'index.json').write_text(json.dumps(ix, indent=1))
    return rels


def certs():
    ca = WORK / 'fleet-ca'
    shutil.rmtree(ca, ignore_errors=True)
    ca.mkdir(mode=0o700)
    sh(['openssl', 'req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', str(ca / 'ca.key'), '-out', str(ca / 'ca.crt'), '-days', '2', '-subj', '/CN=lamp-t28-fleet'])
    for h in ORDER:
        sh(['openssl', 'req', '-new', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-keyout', str(ca / f'{h}.key'), '-out', str(ca / f'{h}.csr'), '-subj', '/CN=' + h])
        (ca / f'{h}.ext').write_text(f'subjectAltName=DNS:{h}\nextendedKeyUsage=serverAuth,clientAuth\n')
        sh(['openssl', 'x509', '-req', '-in', str(ca / f'{h}.csr'), '-CA', str(ca / 'ca.crt'), '-CAkey', str(ca / 'ca.key'), '-CAcreateserial', '-out', str(ca / f'{h}.crt'), '-days', '2', '-extfile', str(ca / f'{h}.ext')])
    return ca


def main():
    global TOKEN
    rels = build_releases()
    ca = certs()
    TOKEN = secrets.token_hex(32)
    identity = json.dumps({'owner_id': 'pr_t28_owner', 'bootstrap_sha256': hashlib.sha256(TOKEN.encode()).hexdigest()})
    manifest = []
    for h in ORDER:
        cap = dict(vcpus=4, memory_mib=8192, disk_gib=40, pids_max=4096) if h == 'hs_m5' else dict(vcpus=0, memory_mib=0, disk_gib=0, pids_max=0)
        manifest.append(dict(id=h, api=f'https://127.0.0.1:{PORTS[h]}', raft=f'127.0.0.1:{PORTS[h] + 1}', os=H[h]['os'], arch=H[h]['arch'], engines=['vz'] if h == 'hs_m5' else [], capacity=cap))

    # Ship kits: installer, CLI, releases, fleet files, shared owner enrollment.
    for h in ORDER:
        c = H[h]
        # Keep a host's copy of the releases when its index hashes match.
        on(h, f'mkdir -p {c["kit"]} && cd {c["kit"]} && ls -A | grep -v "^releases$" | xargs rm -rf; rm -rf {c["prefix"]}; mkdir -p {c["prefix"]} && chmod 700 {c["base"]} {c["kit"]}')
        b = WORK / f'bin-{c["goos"]}-{c["goarch"]}'
        for f in [b / 'lamp-installer', b / 'lamp', WORK / 'keys' / 'release-trust.pub', ca / 'ca.crt', ca / f'{h}.crt', ca / f'{h}.key']:
            copy(h, f, c['kit'] + '/')
        lines = [hashlib.sha256((rels / v / 'index.json').read_bytes()).hexdigest() + '  releases/' + v + '/index.json' for v in sorted(os.listdir(rels))]
        tool_sum = 'sha256sum' if c['goos'] == 'linux' else 'shasum -a 256'
        rc, _ = on(h, f"cd {c['kit']} && printf '%s\\n' {' '.join(shlex.quote(l) for l in lines)} | {tool_sum} -c --status", check=False)
        if rc:
            on(h, f'rm -rf {c["kit"]}/releases')
            copy(h, rels, c['kit'] + '/releases')
        record('releases on host', host=h, reused=rc == 0)
        put(h, c['kit'] + '/identity.json', identity)
        put(h, c['kit'] + '/owner.token', TOKEN + '\n')
        cfg = dict(host_id=h, bootstrap=h == 'hs_lx', hosts=manifest, ca_file=c['kit'] + '/ca.crt', cert_file=c['kit'] + f'/{h}.crt', key_file=c['kit'] + f'/{h}.key',
                   guard_dir=c['kit'] + '/guard', public_api_listen=f'127.0.0.1:{PORTS[h] + 2}')
        put(h, c['kit'] + '/fleet.json', json.dumps(cfg))
        on(h, f'mkdir -p -m 700 {c["kit"]}/guard')
    record('fleet manifest', hosts=[{k: m[k] for k in ['id', 'os', 'arch', 'engines', 'capacity']} for m in manifest], transport='pairwise SSH over Tailscale, host mTLS inside')

    # Pairwise tunnels between every host pair (no relay through a third host).
    for src, dst in [('hs_m5', 'hs_lx'), ('hs_m5', 'hs_air'), ('hs_air', 'hs_lx')]:
        args = ['ssh', '-N', '-o', 'BatchMode=yes', '-o', 'ExitOnForwardFailure=yes', '-o', 'ServerAliveInterval=15']
        for off in (0, 1, 2):
            args += ['-L', f'127.0.0.1:{PORTS[dst] + off}:127.0.0.1:{PORTS[dst] + off}', '-R', f'127.0.0.1:{PORTS[src] + off}:127.0.0.1:{PORTS[src] + off}']
        args.append(H[dst]['ssh'])
        _, pid = on(src, f'nohup {shlex.join(args)} > {H[src]["kit"]}/ssh-{dst}.log 2>&1 & echo $!')
        CLEAN.append(('tunnel', src, pid.strip()))

    # M5 VZ supervisor (root, its own socket) and a control grant for lampd.
    m5 = H['hs_m5']
    sock = m5['kit'] + '/worker.sock'
    grant = json.loads(sh(['sudo', '-n', SUP, 'grant-install', '--store', STORE, '--caller-uid', str(os.getuid()), '--ttl-s', '10800']).stdout)
    CLEAN.append(('grant', grant['grant_id']))
    subprocess.Popen(['sudo', '-n', 'sh', '-c', 'echo "$$" > "$1"; shift; exec "$@"', 'sh', m5['kit'] + '/worker.pid', SUP, 'serve', '--store', STORE, '--socket', sock, '--allow-uid', str(os.getuid())],
                     stdout=open(m5['kit'] + '/supervisor.log', 'w'), stderr=subprocess.STDOUT, start_new_session=True)
    deadline = time.time() + 120
    while not os.path.exists(sock) and time.time() < deadline:
        time.sleep(1)
    sup_pid = open(m5['kit'] + '/worker.pid').read().strip()
    CLEAN.append(('supervisor', sup_pid))
    boot = sh(['sysctl', '-n', 'kern.bootsessionuuid']).stdout.strip()
    # The macOS supervisor names itself after kern.hostname at start.
    worker_id = 'lamp-supervisor@' + sh(['sysctl', '-n', 'kern.hostname']).stdout.strip()
    record('M5 VZ supervisor enrolled', worker_id='lamp-supervisor@<kern.hostname>', pid=sup_pid, grant_ttl_s=10800)

    # Install 3.0.0 on every host: lampd + updater agent, fleet-enrolled.
    for h in ORDER:
        c = H[h]
        extra = ['--fleet-config', c['kit'] + '/fleet.json']
        if h == 'hs_m5':
            extra += ['--worker-socket', sock, '--worker-id', worker_id, '--worker-boot-id', boot, '--worker-uid', '0', '--worker-pid', sup_pid, '--grant-id', grant['grant_id'], '--worker-timeout', '60s']
        la = ' '.join('--lampd-arg=' + shlex.quote(a) for a in extra)
        rc, out = on(h, f'{tool(h)} install --release {c["kit"]}/releases/3.0.0 --prefix {c["prefix"]} --name t28fleet --trust {c["kit"]}/release-trust.pub --service {c["service"]} '
                        f'--identity-file {c["kit"]}/identity.json --token-file {c["kit"]}/owner.token --wait=false {la}', check=False)
        record('install 3.0.0', host=h, service=c['service'], rc=rc, output=out.splitlines()[-3:])
        if rc:
            raise SystemExit(out)
        CLEAN.append(('install', h))
    deadline = time.time() + 120
    while time.time() < deadline:
        if all(lamp('fleet get fl_local', h, check=False)[0] == 0 for h in ORDER):
            break
        time.sleep(2)
    _, hosts = lamp('host list')
    record('fleet ready', hosts=[{k: x.get(k) for k in ['id', 'state', 'os', 'engine_ids']} for x in hosts.get('items', [])])
    for h in ORDER:
        if H[h]['service'] == 'launchd':
            record('LaunchAgents loaded (gui domain)', host=h, output=on(h, f'launchctl print gui/$(id -u)/si.eilmer.lamp.t28fleet | grep -E "state =|pid =|program" | head -4; launchctl print gui/$(id -u)/si.eilmer.lamp.t28fleet-agent | grep -E "state =" | head -1')[1].splitlines())
        else:
            record('systemd units active', host=h, output=on(h, 'systemctl is-active lamp-t28fleet.service lamp-t28fleet-agent.service')[1].splitlines())

    # Item 3: restart the Mac LaunchAgent in place.
    for h in ['hs_air']:
        rc, out = on(h, f'{tool(h)} restart --prefix {H[h]["prefix"]}', check=False)
        record('LaunchAgent restart (launchctl kickstart -k)', host=h, rc=rc, output=out.splitlines()[-2:])

    # Active work: a VM sandbox on the M5 with a live guest process.
    _, created = lamp(f"sandbox create --template-id {TEMPLATE} --host-id hs_m5 --resources-json '{{\"vcpus\":2,\"memory_mib\":1024,\"disk_gib\":20,\"pids_max\":1024}}' --wait-ms 60000")
    sb = created['resource_ids'][0]['id']
    CLEAN.insert(0, ('sandbox', sb))
    deadline = time.time() + 300
    while time.time() < deadline:
        _, g = lamp(f'sandbox get {sb}')
        if g['data']['state'] in ('running', 'failed'):
            break
        time.sleep(3)
    record('sandbox on hs_m5', sandbox={k: g['data'].get(k) for k in ['id', 'state', 'host_id', 'engine_id', 'template_id']})
    start = ("setsid sh -c 'echo $$ > /dev/shm/beat.pid; while :; do date +%s >> /dev/shm/beat; sleep 1; done' >/dev/null 2>&1 < /dev/null & "
             "sleep 2; echo boot=$(cat /proc/sys/kernel/random/boot_id) pid=$(cat /dev/shm/beat.pid) beats=$(wc -l < /dev/shm/beat)")
    out, e, _ = guest(sb, start)
    record('guest process started (tmpfs, memory only)', stdout=out, exec_state=e['state'])
    before = dict(x.split('=', 1) for x in out.split())

    # Automatic updates start disabled: 3.1.0 is in every mirror, nothing happens.
    for h in ORDER:
        on(h, f'{tool(h)} publish --prefix {H[h]["prefix"]} --release {H[h]["kit"]}/releases/3.1.0')
    time.sleep(12)
    _, f = lamp('fleet get fl_local')
    record('3.1.0 published; automatic updates off, so no Update', update_policy=f['data'].get('update_policy'), updates=len(updates()))

    # Tampered release refused before anything is applied.
    for h in ORDER:
        on(h, f'{tool(h)} publish --prefix {H[h]["prefix"]} --release {H[h]["kit"]}/releases/3.1.1-tampered')
    d = 'sha256:' + hashlib.sha256((rels / '3.1.1-tampered' / 'index.json').read_bytes()).hexdigest()
    rc, body = lamp(f'update create --release-digest {d}', check=False)
    record('update_create with tampered release', rc=rc, response=body)

    # The owner turns automatic updates on through Lamp itself.
    pol = '{"automatic":true,"max_unavailable":1,"channel":"stable","drain_timeout_ms":60000}'
    rc, body = lamp(f"fleet update fl_local --update-policy-json '{pol}' --expected-revision {f['data']['revision']}")
    for _ in range(20):  # the local host may be a follower that has not applied the commit yet
        _, f = lamp('fleet get fl_local')
        if (f['data'].get('update_policy') or {}).get('automatic'):
            break
        time.sleep(0.5)
    record('lamp fleet update --update-policy-json', command=f"lamp fleet update fl_local --update-policy-json '{pol}'", rc=rc, update_policy=f['data'].get('update_policy'))
    u = watch_update({'completed', 'failed', 'rolled_back'}, 'rollout 3.1.0')
    for h in ORDER:
        record('host after 3.1.0', host=h, status=json.loads(on(h, f'{tool(h)} status --prefix {H[h]["prefix"]}')[1].splitlines()[-1]))
    out, e, _ = guest(sb, "echo boot=$(cat /proc/sys/kernel/random/boot_id) pid=$(cat /dev/shm/beat.pid) alive=$(kill -0 $(cat /dev/shm/beat.pid) && echo yes) beats=$(wc -l < /dev/shm/beat) "
                          "maxgap=$(awk 'NR>1&&$1-p>m{m=$1-p}{p=$1}END{print m+0}' /dev/shm/beat)")
    after = dict(x.split('=', 1) for x in out.split())
    _, g = lamp(f'sandbox get {sb}')
    record('active work after the M5 update', sandbox_state=g['data']['state'], before=before, after=after,
           same_vm_boot=before['boot'] == after['boot'], same_process=before['pid'] == after['pid'])
    if u['state'] != 'completed' or before['boot'] != after['boot'] or before['pid'] != after['pid'] or after.get('alive') != 'yes':
        raise SystemExit('3.1.0 rollout did not preserve active work')

    # A signed but broken release: the first host fails, rolls back, rollout stops.
    for h in ORDER:
        on(h, f'{tool(h)} publish --prefix {H[h]["prefix"]} --release {H[h]["kit"]}/releases/3.2.0')
    u = watch_update({'completed', 'failed', 'rolled_back'}, 'rollout 3.2.0 (broken)', skip={u['id']})
    for h in ORDER:
        record('host after broken 3.2.0', host=h, status=json.loads(on(h, f'{tool(h)} status --prefix {H[h]["prefix"]}')[1].splitlines()[-1]))
    time.sleep(10)
    record('no new Update for the rolled-back release', updates=[{k: x[k] for k in ['id', 'state']} for x in updates()])
    out, _, _ = guest(sb, "echo pid=$(cat /dev/shm/beat.pid) alive=$(kill -0 $(cat /dev/shm/beat.pid) && echo yes) beats=$(wc -l < /dev/shm/beat) maxgap=$(awk 'NR>1&&$1-p>m{m=$1-p}{p=$1}END{print m+0}' /dev/shm/beat)")
    record('guest still running at the end', stdout=out)


def cleanup():
    for item in CLEAN:
        try:
            if item[0] == 'sandbox':
                _, g = lamp(f'sandbox get {item[1]}', check=False)
                rc, b = lamp(f'sandbox delete {item[1]} --expected-revision {g["data"]["revision"]} --wait-ms 60000', check=False)
                record('sandbox deleted', rc=rc)
        except SystemExit:
            pass
    for item in CLEAN:
        if item[0] == 'install':
            h = item[1]
            rc, out = on(h, f'{tool(h)} uninstall --prefix {H[h]["prefix"]} --data delete --confirm-delete t28fleet', check=False)
            check = on(h, 'launchctl print gui/$(id -u)/si.eilmer.lamp.t28fleet >/dev/null 2>&1 && echo still-loaded || echo removed; ls ~/Library/LaunchAgents | grep -c t28fleet || true' if H[h]['service'] == 'launchd'
                       else 'systemctl list-unit-files "lamp-t28fleet*" --no-legend | wc -l', check=False)[1]
            record('uninstall (service removed)', host=h, rc=rc, output=out.splitlines()[-1:], service_check=check.splitlines())
    for item in CLEAN:
        if item[0] == 'tunnel':
            on(item[1], f'kill {item[2]}', check=False)
        if item[0] == 'supervisor':
            sh(['sudo', '-n', 'kill', '-TERM', item[1]], check=False)
        if item[0] == 'grant':
            sh(['sudo', '-n', SUP, 'grant-revoke', '--store', STORE, item[1]], check=False)
    for h in ORDER:
        # Releases stay for a quick retry; FINAL=1 removes everything.
        keep = '' if os.environ.get('FINAL') else f' -not -path "{H[h]["kit"]}/releases*"'
        on(h, f'find {H[h]["base"]} -mindepth 1 -maxdepth 2{keep} -exec rm -rf {{}} + ; [ -n "{os.environ.get("FINAL", "")}" ] && rm -rf {H[h]["base"]}', check=False)
    record('cleanup complete')


if __name__ == '__main__':
    try:
        main()
    finally:
        cleanup()
