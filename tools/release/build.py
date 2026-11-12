#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Build Lamp's public release from a clean checkout.

Produces, under --out:
  lamp-<v>-<os>-<arch>.tar.gz   signed installer release (manifest.json, manifest.sig,
                                payload/, index.json) for lamp-installer install/update
  lamp-relay-<v>-<os>-<arch>.tar.gz  the AGPL-3.0 relay, built and shipped separately
  lamp-sdk (npm .tgz) and lamp_sdk (wheel)
  release-trust.pub, SHA256SUMS

Mac binaries are signed with the Developer ID Application certificate and
notarized with an App Store Connect API key, all read from files by rcodesign;
no keychain is touched. Linux engine binaries are built on a Linux host given
by --linux-host (ssh), which needs Go, Rust and this commit.

    python3 tools/release/build.py --version 0.1.0 --out /tmp/lamp-release \
      --release-key ~/.config/lamp/release/release.key \
      --apple-dir ~/.config/lamp/apple --linux-host ubuntu-server
"""
import argparse
import hashlib
import json
import os
import shutil
import subprocess
import sys
import tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GO_BINS = ['lampd', 'lamp', 'lamp-installer', 'lamp-access', 'lamp-mcp', 'lamp-broker',
           'lamp-network', 'lamp-relay-client', 'lamp-gitlab-executor', 'lamp-fleeting']
RCODESIGN = os.environ.get('RCODESIGN', str(Path.home() / '.local/rcodesign/bin/rcodesign'))


def sh(cmd, **kw):
    print('+', ' '.join(map(str, cmd)), flush=True)
    return subprocess.run(list(map(str, cmd)), check=True, **kw)


def go_build(goos, goarch, out, names):
    env = {**os.environ, 'GOOS': goos, 'GOARCH': goarch, 'CGO_ENABLED': '0'}
    out.mkdir(parents=True, exist_ok=True)
    sh(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(out) + '/'] + [f'./cmd/{n}' for n in names], cwd=ROOT, env=env)


def linux_engine(host, commit, out):
    """Build lamp-supervisor and lamp-guard on a Linux host from this exact commit."""
    work = f'lamp-release-{commit[:12]}'
    archive = subprocess.run(['git', 'archive', '--format=tar', commit], cwd=ROOT, capture_output=True, check=True).stdout
    sh(['ssh', host, f'rm -rf {work} && mkdir {work} && tar -x -C {work}'], input=archive)
    sh(['ssh', host, f'cd {work} && . "$HOME/.cargo/env" 2>/dev/null; cargo build --release --locked -p lamp-supervisor -p lamp-guard'])
    out.mkdir(parents=True, exist_ok=True)
    for b in ['lamp-supervisor', 'lamp-guard']:
        sh(['scp', f'{host}:{work}/target/release/{b}', out / b])
    sh(['ssh', host, f'rm -rf {work}'])


def mac_engine(out):
    sh(['cargo', 'build', '--release', '--locked', '-p', 'lamp-vz', '-p', 'lamp-supervisor'], cwd=ROOT)
    target = Path(os.environ.get('CARGO_TARGET_DIR', ROOT / 'target')) / 'release'
    for b in ['lamp-vz', 'lamp-supervisor']:
        shutil.copy2(target / b, out / b)


def apple_files(d):
    d = Path(d).expanduser()
    need = {k: d / v for k, v in {
        'app_cert': 'devid-application.cer', 'app_key': 'devid-application.key',
        'api_key': 'app-store-connect-api-key.json'}.items()}
    missing = [str(p.name) for p in need.values() if not p.exists()]
    if missing:
        raise SystemExit(f'missing signing material in {d}: {", ".join(missing)}')
    return need


def mac_sign(binaries, apple):
    for b in binaries:
        args = [RCODESIGN, 'sign', '--certificate-der-file', apple['app_cert'], '--pem-file', apple['app_key'],
                '--code-signature-flags', 'runtime', '--for-notarization']
        ent = ROOT / 'crates/lamp-vz' / f'{b.name}.entitlements'
        if ent.exists():
            args += ['--entitlements-xml-file', ent]
        sh(args + [b])


def mac_notarize(binaries, apple, scratch):
    """Submit the signed binaries as one zip and wait for Apple's verdict."""
    z = scratch / 'notarize.zip'
    z.unlink(missing_ok=True)
    stage = scratch / 'notarize'
    shutil.rmtree(stage, ignore_errors=True)
    stage.mkdir()
    for b in binaries:
        shutil.copy2(b, stage / b.name)
    sh(['ditto', '-c', '-k', '--keepParent', stage, z])
    r = subprocess.run([RCODESIGN, 'notary-submit', '--api-key-file', str(apple['api_key']), '--wait', str(z)],
                       capture_output=True, text=True)
    print(r.stdout[-4000:], r.stderr[-4000:], sep='\n')
    if r.returncode != 0 or 'Accepted' not in (r.stdout + r.stderr):
        raise SystemExit('notarization was not accepted')
    return (r.stdout + r.stderr).strip().splitlines()[-20:]


def common_files():
    return [('LICENSE', ROOT / 'LICENSE'), ('THIRD_PARTY.md', ROOT / 'THIRD_PARTY.md'),
            ('licenses/relay-AGPL-3.0.txt', ROOT / 'internal/relay/LICENSE')]


def installer_release(inst, version, goos, goarch, key, out, files, snapshot_format=None):
    args = [inst, 'build', '--version', version, '--os', goos, '--arch', goarch, '--key', key, '--out', out]
    for dst, src in files:
        args += ['--file', f'{dst}={src}']
    if snapshot_format:
        args += ['--snapshot-format', snapshot_format]
    sh(args)
    sh([inst, 'index', '--release', out, '--key', key, '--metadata-version', '1'])


def tar_dir(src, dst, arcname):
    with tarfile.open(dst, 'w:gz') as t:
        t.add(src, arcname=arcname)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--version', required=True)
    ap.add_argument('--out', required=True, type=Path)
    ap.add_argument('--release-key', required=True, type=Path)
    ap.add_argument('--apple-dir', type=Path, help='Developer ID + notary key files; omit for an unsigned local build')
    ap.add_argument('--linux-host', help='ssh host that builds the Linux engine; omit to ship Linux control binaries only')
    ap.add_argument('--snapshot-format', default='1')
    a = ap.parse_args()
    if subprocess.run(['git', 'status', '--porcelain'], cwd=ROOT, capture_output=True, text=True).stdout.strip():
        raise SystemExit('refusing to build a release from a dirty checkout')
    commit = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=ROOT, capture_output=True, text=True, check=True).stdout.strip()
    out = a.out
    if out.exists():
        raise SystemExit(f'refusing existing output: {out}')
    scratch = out / '.work'
    scratch.mkdir(parents=True)
    host_tools = scratch / 'host'
    sh(['go', 'build', '-o', str(host_tools) + '/', './cmd/lamp-installer'], cwd=ROOT)
    inst = host_tools / 'lamp-installer'
    key = a.release_key.expanduser()
    key_pub = key.with_suffix('.pub')
    if not key.exists():
        key.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        sh([inst, 'keygen', '--out', key, '--public', key_pub])
    shutil.copy2(key_pub, out / 'release-trust.pub')
    record = {'version': a.version, 'commit': commit, 'platforms': {}}
    rels = scratch / 'releases'

    # Linux amd64: control binaries plus, when a Linux host is given, the engine.
    lb = scratch / 'linux-amd64'
    go_build('linux', 'amd64', lb, GO_BINS)
    files = [(f'bin/{n}', lb / n) for n in GO_BINS] + common_files() + [
        ('tools/guard-audit-release.py', ROOT / 'tools/guard-audit-release.py'),
        ('infra/linux/install-engine.sh', ROOT / 'infra/linux/install-engine.sh'),
        ('infra/linux/lamp-supervisor.service', ROOT / 'infra/linux/lamp-supervisor.service'),
        ('infra/linux/var-lib-lamp.mount', ROOT / 'infra/linux/var-lib-lamp.mount')]
    if a.linux_host:
        linux_engine(a.linux_host, commit, lb)
        files += [('lib/lamp-supervisor', lb / 'lamp-supervisor'), ('lib/lamp-guard', lb / 'lamp-guard')]
    installer_release(inst, a.version, 'linux', 'amd64', key, rels, files)
    record['platforms']['linux-amd64'] = {'engine': bool(a.linux_host)}

    # macOS arm64: control binaries, the VZ engine and supervisor, Developer ID signed.
    mb = scratch / 'darwin-arm64'
    go_build('darwin', 'arm64', mb, GO_BINS)
    mac_engine(mb)
    mac_bins = [mb / n for n in GO_BINS + ['lamp-vz', 'lamp-supervisor']]
    if a.apple_dir:
        apple = apple_files(a.apple_dir)
        mac_sign(mac_bins, apple)
        record['platforms']['darwin-arm64'] = {'notarization': mac_notarize(mac_bins, apple, scratch)}
    else:
        sh(['codesign', '--force', '--sign', '-', '--entitlements', ROOT / 'crates/lamp-vz/lamp-vz.entitlements', mb / 'lamp-vz'])
        record['platforms']['darwin-arm64'] = {'notarization': None}
    files = [(f'bin/{n}', mb / n) for n in GO_BINS + ['lamp-vz', 'lamp-supervisor']] + common_files()
    installer_release(inst, a.version, 'darwin', 'arm64', key, rels, files, a.snapshot_format)

    for plat in ['linux-amd64', 'darwin-arm64']:
        d = scratch / f'lamp-{a.version}-{plat}'
        shutil.copytree(rels / plat, d / plat)
        for f in rels.iterdir():
            if f.is_file():  # the signed index and its signature
                shutil.copy2(f, d / f.name)
        tar_dir(d, out / f'lamp-{a.version}-{plat}.tar.gz', f'lamp-{a.version}')

    # The relay: AGPL-3.0-only, its own archive with the full licence and source pointer.
    for goos, goarch in [('linux', 'amd64'), ('linux', 'arm64')]:
        rb = scratch / f'relay-{goos}-{goarch}'
        go_build(goos, goarch, rb, ['lamp-relay'])
        shutil.copy2(ROOT / 'internal/relay/LICENSE', rb / 'LICENSE')
        (rb / 'SOURCE').write_text(f'Corresponding source: https://github.com/JohanSardar/lamp/tree/{commit}\n'
                                   'internal/relay and cmd/lamp-relay are AGPL-3.0-only.\n')
        tar_dir(rb, out / f'lamp-relay-{a.version}-{goos}-{goarch}.tar.gz', f'lamp-relay-{a.version}')

    sh(['npm', 'pack', '--pack-destination', out], cwd=ROOT / 'sdk/typescript')
    shutil.copytree(ROOT / 'sdk/python', scratch / 'sdk-python')  # keep build debris out of the checkout
    sh([sys.executable, '-m', 'pip', 'wheel', '--no-deps', '-w', out, scratch / 'sdk-python'])

    (out / 'release.json').write_text(json.dumps(record, indent=1) + '\n')
    shutil.rmtree(scratch)
    sums = []
    for f in sorted(out.iterdir()):
        if f.is_file() and f.name != 'SHA256SUMS':
            sums.append(f'{hashlib.sha256(f.read_bytes()).hexdigest()}  {f.name}')
    (out / 'SHA256SUMS').write_text('\n'.join(sums) + '\n')
    print('\n'.join(sums))


if __name__ == '__main__':
    main()
