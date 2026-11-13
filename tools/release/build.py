#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Build Lamp's public release from a clean checkout.

Produces, under --out:
  lamp-<v>-<os>-<arch>.tar.gz   signed installer release (manifest.json, manifest.sig,
                                payload/, index.json) for lamp-installer install/update
  lamp-relay-<v>-<os>-<arch>.tar.gz  the AGPL-3.0 relay, built and shipped separately
  the TypeScript SDK (npm .tgz)
  release-trust.pub, SHA256SUMS

Mac binaries are signed with the Developer ID Application certificate and
notarized with an App Store Connect API key, all read from files by rcodesign;
no keychain is touched. Linux engine binaries are built on a Linux host given
by --linux-host (ssh), which needs Go, Rust and this commit.

    python3 tools/release/build.py --version 0.1.1 --out /tmp/lamp-release \
      --release-key ~/.config/lamp/release/release.key \
      --apple-dir ~/.config/lamp/apple --linux-host ubuntu-server
"""
import argparse
import gzip
import hashlib
import json
import os
import shutil
import subprocess
import tarfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
GO_BINS = ['lampd', 'lamp', 'lamp-installer', 'lamp-access', 'lamp-mcp', 'lamp-broker',
           'lamp-network', 'lamp-access-cloudflare', 'lamp-fleet', 'lamp-relay-client',
           'lamp-ingress', 'lamp-account', 'lamp-issuer', 'lamp-dns-cloudflare',
           'lamp-gitlab-executor', 'lamp-fleeting']
SITE = 'https://lamp.eilmer.si'  # a Cloudflare Worker in front of the public project (tools/release/worker.js)
RCODESIGN = os.environ.get('RCODESIGN', str(Path.home() / '.local/rcodesign/bin/rcodesign'))


def sh(cmd, **kw):
    print('+', ' '.join(map(str, cmd)), flush=True)
    return subprocess.run(list(map(str, cmd)), check=True, **kw)


def go_build(goos, goarch, out, names):
    env = {**os.environ, 'GOOS': goos, 'GOARCH': goarch, 'CGO_ENABLED': '0'}
    out.mkdir(parents=True, exist_ok=True)
    sh(['go', 'build', '-trimpath', '-ldflags=-s -w', '-o', str(out) + '/'] + [f'./cmd/{n}' for n in names], cwd=ROOT, env=env)


def linux_engine(host, commit, out, ssh_opts=()):
    """Build lamp-supervisor and lamp-guard on a Linux host from this exact commit."""
    work = f'lamp-release-{commit[:12]}'
    ssh = ['ssh', *ssh_opts, host]
    archive = subprocess.run(['git', 'archive', '--format=tar', commit], cwd=ROOT, capture_output=True, check=True).stdout
    sh(ssh + [f'rm -rf {work} && mkdir {work} && tar -x -C {work}'], input=archive)
    sh(ssh + [f'cd {work} && . "$HOME/.cargo/env" 2>/dev/null; CARGO_TARGET_DIR=target RUSTFLAGS="--remap-path-prefix=$HOME=/build" cargo build --release --locked -p lamp-supervisor -p lamp-guard'])
    out.mkdir(parents=True, exist_ok=True)
    sh(ssh + [f'cd {work} && cc -O2 -Wall -Wextra -Werror -o lamp-plugin-launcher tools/plugin-launcher.c'])
    sh(['scp', *ssh_opts, f'{host}:{work}/lamp-plugin-launcher', out / 'lamp-plugin-launcher'])
    for b in ['lamp-supervisor', 'lamp-guard']:
        sh(['scp', *ssh_opts, f'{host}:{work}/target/release/{b}', out / b])
    sh(ssh + [f'rm -rf {work}'])


def mac_engine(out):
    # Keep build-machine paths (home, cargo registry, checkout) out of the shipped binaries.
    env = {**os.environ, 'RUSTFLAGS': f'--remap-path-prefix={Path.home()}=/build'}
    sh(['cargo', 'build', '--release', '--locked', '-p', 'lamp-vz', '-p', 'lamp-supervisor'], cwd=ROOT, env=env)
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
            ('licenses/relay-AGPL-3.0.txt', ROOT / 'internal/relay/LICENSE'),
            ('docs/release/fleets-and-access.md', ROOT / 'docs/release/fleets-and-access.md'),
            ('docs/development/access.md', ROOT / 'docs/development/access.md'),
            ('docs/development/hosted.md', ROOT / 'docs/development/hosted.md'),
            ('tools/plugin-launcher.c', ROOT / 'tools/plugin-launcher.c')]


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


def seed_files(seed, scratch, platform):
    if seed is None:
        raise SystemExit(f'{platform} needs --seed-{platform}: a prepared seed directory, never a tester-side kernel build')
    seed = seed.expanduser()
    meta = json.loads((seed / 'seed.json').read_text())
    want = 'x86_64' if platform == 'linux-amd64' else 'arm64'
    if meta['arch'] != want or meta['guest_os'] != 'linux':
        raise SystemExit(f'{seed} must be a Linux {want} seed')
    kernel = 'Image' if platform == 'darwin-arm64' else 'vmlinux'
    if platform.startswith('linux-') and meta.get('digest_alg') != 'sha256-c4m':
        raise SystemExit(f'{platform} needs a Firecracker seed (sha256-c4m), not a VZ seed')
    # Compressed seed disks keep the signed payload small. Installation expands
    # with bounded memory and restores sparse zero extents, including on the Air.
    packed = scratch / f'{platform}-rootfs.ext4.gz'
    with (seed / 'rootfs.ext4').open('rb') as src, packed.open('wb') as dst:
        with gzip.GzipFile(filename='', mode='wb', fileobj=dst, mtime=0, compresslevel=1) as gz:
            shutil.copyfileobj(src, gz, 1 << 20)
    return [(f'seed/{n}', seed / n) for n in ['seed.json', kernel, 'packages.txt']] + [('seed/rootfs.ext4.gz', packed)]


def digest_file(path):
    with path.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--version', required=True)
    ap.add_argument('--out', required=True, type=Path)
    ap.add_argument('--release-key', required=True, type=Path)
    ap.add_argument('--apple-dir', type=Path, help='Developer ID + notary key files; omit for an unsigned local build')
    ap.add_argument('--linux-host', help='ssh host that builds the required amd64 Linux engine')
    ap.add_argument('--linux-arm64-host', help='ssh host (Linux arm64) that builds the required arm64 engine')
    ap.add_argument('--ssh-config', help='ssh -F file for the engine hosts (for example a Lima VM)')
    ap.add_argument('--platforms', default='linux-amd64,linux-arm64,darwin-arm64')
    ap.add_argument('--reuse', type=Path, help='an earlier output of this version: keep its files byte for byte, '
                    'build only --platforms, and regenerate install.sh, release.json and SHA256SUMS')
    ap.add_argument('--snapshot-format', default='1')
    for plat in ['linux-amd64', 'linux-arm64', 'darwin-arm64']:
        ap.add_argument('--seed-' + plat, type=Path, help='verified prepared Linux seed for this platform')
    ap.add_argument('--firecracker-amd64', type=Path, help='directory containing firecracker and jailer to sign into the amd64 release')
    ap.add_argument('--firecracker-arm64', type=Path, help='directory containing firecracker and jailer to sign into the arm64 release')
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
    if a.reuse and (a.reuse / 'release-trust.pub').read_bytes() != key_pub.read_bytes():
        raise SystemExit('--reuse output was signed with a different release key')
    record = {'version': a.version, 'commit': commit, 'platforms': {}}
    platforms = a.platforms.split(',')
    if a.reuse:
        for f in a.reuse.iterdir():
            if f.is_file() and f.name not in ('install.sh', 'release.json', 'SHA256SUMS'):
                shutil.copy2(f, out / f.name)
        record = json.loads((a.reuse / 'release.json').read_text())
    ssh_opts = ['-F', a.ssh_config] if a.ssh_config else []

    def package(plat, files, snapshot_format=None):
        # Each new platform gets its own signed index, so reused archives keep theirs.
        goos, goarch = plat.split('-')
        rels = scratch / f'releases-{plat}'
        installer_release(inst, a.version, goos, goarch, key, rels, files, snapshot_format)
        d = scratch / f'lamp-{a.version}-{plat}'
        shutil.copytree(rels / plat, d / plat)
        for f in rels.iterdir():
            if f.is_file():  # the signed index and its signature
                shutil.copy2(f, d / f.name)
        tar_dir(d, out / f'lamp-{a.version}-{plat}.tar.gz', f'lamp-{a.version}')

    # Linux: control binaries plus, when a Linux host of that architecture is given, the engine.
    for arch, host in [('amd64', a.linux_host), ('arm64', a.linux_arm64_host)]:
        plat = f'linux-{arch}'
        if plat not in platforms:
            continue
        lb = scratch / plat
        go_build('linux', arch, lb, GO_BINS)
        files = [(f'bin/{n}', lb / n) for n in GO_BINS] + common_files() + [
            ('tools/guard-audit-release.py', ROOT / 'tools/guard-audit-release.py'),
            ('infra/linux/install-engine.sh', ROOT / 'infra/linux/install-engine.sh'),
            ('infra/linux/lamp-supervisor.service', ROOT / 'infra/linux/lamp-supervisor.service'),
            ('infra/linux/var-lib-lamp.mount', ROOT / 'infra/linux/var-lib-lamp.mount')]
        if host:
            linux_engine(host, commit, lb, ssh_opts)
            files += [('bin/lamp-supervisor', lb / 'lamp-supervisor'), ('bin/lamp-guard', lb / 'lamp-guard'),
                      ('bin/lamp-plugin-launcher', lb / 'lamp-plugin-launcher')]
            fc = getattr(a, 'firecracker_' + arch)
            if fc is None:
                raise SystemExit(f'{plat} needs --firecracker-{arch}; downloads never run on the installing host')
            files += [(f'bin/{n}', fc / n) for n in ['firecracker', 'jailer']]
            files += seed_files(getattr(a, 'seed_' + plat.replace('-', '_')), scratch, plat)
        else:
            raise SystemExit(f'{plat} needs a Linux engine builder; a control-only archive is not a sandbox release')
        package(plat, files, a.snapshot_format)
        record['platforms'][plat] = {'engine': bool(host), 'commit': commit}

    # macOS arm64: control binaries, the VZ engine and supervisor, Developer ID signed.
    if 'darwin-arm64' in platforms:
        mb = scratch / 'darwin-arm64'
        go_build('darwin', 'arm64', mb, GO_BINS)
        mac_engine(mb)
        sh(['cc', '-O2', '-Wall', '-Wextra', '-Werror', '-o', mb / 'lamp-plugin-launcher', ROOT / 'tools/plugin-launcher.c'])
        mac_bins = [mb / n for n in GO_BINS + ['lamp-vz', 'lamp-supervisor', 'lamp-plugin-launcher']]
        if a.apple_dir:
            apple = apple_files(a.apple_dir)
            mac_sign(mac_bins, apple)
            record['platforms']['darwin-arm64'] = {'notarization': mac_notarize(mac_bins, apple, scratch)}
        else:
            sh(['codesign', '--force', '--sign', '-', '--entitlements', ROOT / 'crates/lamp-vz/lamp-vz.entitlements', mb / 'lamp-vz'])
            record['platforms']['darwin-arm64'] = {'notarization': None}
        package('darwin-arm64', [(f'bin/{n}', mb / n) for n in GO_BINS + ['lamp-vz', 'lamp-supervisor', 'lamp-plugin-launcher']] + common_files() + [('infra/mac/install-engine.sh', ROOT / 'infra/mac/install-engine.sh')] + seed_files(a.seed_darwin_arm64, scratch, 'darwin-arm64'),
                a.snapshot_format)

    # The relay: AGPL-3.0-only, its own archive with the full licence and source pointer.
    for goos, goarch in [('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'arm64')]:
        if (out / f'lamp-relay-{a.version}-{goos}-{goarch}.tar.gz').exists():
            continue
        rb = scratch / f'relay-{goos}-{goarch}'
        go_build(goos, goarch, rb, ['lamp-relay'])
        shutil.copy2(ROOT / 'internal/relay/LICENSE', rb / 'LICENSE')
        (rb / 'SOURCE').write_text(f'Corresponding source: https://gitlab.com/johansardar/lamp (built from development commit {commit})\n'
                                   'internal/relay and cmd/lamp-relay are AGPL-3.0-only.\n')
        tar_dir(rb, out / f'lamp-relay-{a.version}-{goos}-{goarch}.tar.gz', f'lamp-relay-{a.version}')

    if not list(out.glob('*-lamp-sdk-*.tgz')):
        sh(['npm', 'pack', '--pack-destination', out], cwd=ROOT / 'sdk/typescript')

    # curl | sh installer: archive digests and the release key are built in.
    base = f'{SITE}/releases/v{a.version}'
    script = (ROOT / 'tools/release/install.sh.in').read_text()
    script = script.replace('@VERSION@', a.version).replace('@BASE@', base)
    script = script.replace('@TRUST@', (out / 'release-trust.pub').read_text().strip())
    missing = [p for p in ['linux-amd64', 'linux-arm64', 'darwin-arm64'] if not (out / f'lamp-{a.version}-{p}.tar.gz').exists()]
    for plat in ['linux-amd64', 'linux-arm64', 'darwin-arm64']:
        if plat not in missing:
            digest = digest_file(out / f'lamp-{a.version}-{plat}.tar.gz')
            script = script.replace(f'@SHA_{plat}@', digest)
        engine = plat == 'darwin-arm64' or record['platforms'].get(plat, {}).get('engine', False)
        script = script.replace(f'@ENGINE_{plat}@', 'yes' if engine else 'no')
    if missing:  # a partial pass; a later --reuse pass completes the release
        print(f'install.sh not written: {",".join(missing)} still to build')
    else:
        (out / 'install.sh').write_text(script)

    (out / 'release.json').write_text(json.dumps(record, indent=1) + '\n')
    shutil.rmtree(scratch)
    sums = []
    for f in sorted(out.iterdir()):
        if f.is_file() and f.name != 'SHA256SUMS':
            sums.append(f'{digest_file(f)}  {f.name}')
    (out / 'SHA256SUMS').write_text('\n'.join(sums) + '\n')
    print('\n'.join(sums))


if __name__ == '__main__':
    main()
