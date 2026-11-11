#!/usr/bin/env python3
"""Owner-machine-only private TCP bridge; the bridge never terminates TLS.

The M5 forwarding process runs under a macOS sandbox which cannot read the DNS
custodian's credentials (or other home-directory data). Only its transport HMAC
is passed in the environment. No third-party tunnel or public bind is allowed.
"""
import argparse
import ipaddress
import json
import os
from pathlib import Path
import stat
import subprocess
import sys


def profile(binary, bind, port):
    quote = json.dumps
    return '\n'.join([
        '(version 1)', '(deny default)', '(allow process*)', '(allow sysctl-read)',
        '(allow file-read-metadata)',
        '(allow file-read* (literal "/") (subpath "/System/Library") (subpath "/usr/lib") (subpath "/usr/bin") (subpath "/bin") (subpath "/usr/share") (subpath "/Library/Apple/System/Library") (subpath "/private/var/db/dyld") (subpath "/System/Volumes/Preboot/Cryptexes/OS") (subpath "/System/Cryptexes") (literal "/dev/null") (literal "/dev/urandom") (literal "/dev/random") (literal "/private/etc/localtime") (literal ' + quote(str(binary)) + '))',
        '(allow file-write-data (literal "/dev/null"))',
        # Seatbelt's local matcher supports only localhost or *. Constrain the
        # two ports here; the verified executable binds the explicit LAN IP.
        '(allow network-bind (local ip "*:7835") (local ip ' + quote('*:' + str(port)) + '))',
        '(allow network-inbound (local ip "*:7835") (local ip ' + quote('*:' + str(port)) + '))',
    ])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, required=True)
    parser.add_argument('--bind', required=True)
    parser.add_argument('--port', type=int, required=True)
    parser.add_argument('--secret-file', type=Path, required=True)
    parser.add_argument('--log', type=Path, required=True)
    parser.add_argument('--pid-file', type=Path, required=True)
    parser.add_argument('--denied-file-probe', type=Path)
    args = parser.parse_args()
    os.umask(0o077)
    ip = ipaddress.ip_address(args.bind)
    if ip.version != 4 or not ip.is_private or ip.is_unspecified or not 1024 <= args.port <= 65535:
        raise SystemExit('An explicit private owner LAN IPv4 address/high port is required')
    if sys.platform != 'darwin':
        raise SystemExit('This launcher is the M5 bridge; Ubuntu runs the separate client')
    info = args.secret_file.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077:
        raise SystemExit('Private transport secret file required')
    policy = profile(args.binary.resolve(), args.bind, args.port)
    env = {'PATH': '/usr/bin:/bin', 'BORE_SECRET': args.secret_file.read_text().strip(), 'RUST_LOG': 'info'}
    if args.denied_file_probe:
        if not args.denied_file_probe.is_file():
            raise SystemExit('Probe target does not exist; no denial claim made')
        # Open-only probe: no credential bytes are read, printed or logged even
        # if confinement were broken. A successful open exits with 73.
        result = subprocess.run(['/usr/bin/sandbox-exec', '-p', policy, '/usr/bin/perl', '-e', 'exit(open(my $f,"<",$ARGV[0]) ? 73 : 0)', str(args.denied_file_probe)], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env, cwd='/')
        if result.returncode != 0:
            raise SystemExit('Bridge confinement probe did not demonstrate denial')
        print(json.dumps({'bridge_credential_open_denied': True}))
    with args.log.open('ab') as output:
        proc = subprocess.Popen(['/usr/bin/sandbox-exec', '-p', policy, str(args.binary.resolve()), 'server', '--bind-addr', args.bind, '--bind-tunnels', args.bind, '--min-port', str(args.port), '--max-port', str(args.port)], stdin=subprocess.DEVNULL, stdout=output, stderr=output, env=env, cwd='/', start_new_session=True)
    args.pid_file.write_text(str(proc.pid))
    print(json.dumps({'private_bridge_pid': proc.pid, 'bind': args.bind, 'port': args.port, 'sandboxed': True}))


if __name__ == '__main__':
    main()
