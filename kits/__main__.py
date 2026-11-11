"""python3 -m kits serve ... | python3 -m kits call ... < request.json"""
import argparse
import base64
import ctypes
import http.client
from http.server import BaseHTTPRequestHandler, HTTPServer
import ipaddress
import json
import os
from pathlib import Path
import resource
import signal
import socket
import socketserver
import ssl
import stat
import struct
import sys
import threading

from .control import KitError
from .service import Kits
from .vaults import private_bytes


class OwnerServer(socketserver.UnixStreamServer):
    def verify_request(self, connection, _address):
        try:
            if sys.platform == 'darwin':
                uid, gid = ctypes.c_uint(), ctypes.c_uint()
                if ctypes.CDLL(None).getpeereid(connection.fileno(), ctypes.byref(uid), ctypes.byref(gid)):
                    return False
                return uid.value == os.getuid()
            return struct.unpack('3i', connection.getsockopt(socket.SOL_SOCKET, socket.SO_PEERCRED, 12))[1] == os.getuid()
        except Exception:
            return False


def private_directory(path):
    info = os.lstat(path)
    if not stat.S_ISDIR(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise KitError('PRIVATE_DIRECTORY_REQUIRED')


def serve(args):
    resource.setrlimit(resource.RLIMIT_CORE, (0, 0))
    private_directory(args.root)
    private_directory(str(Path(args.owner_socket).parent))
    import fcntl
    lock = open(Path(args.root) / 'service.lock', 'a')
    os.chmod(lock.name, 0o600)
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    config = json.loads(private_bytes(args.config))
    service = Kits(config, args.root)

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_args):
            pass  # URLs, authorization, bodies, page data and errors stay private.

        def do_POST(self):
            self.connection.settimeout(65)
            result, status = None, 200
            try:
                owner = isinstance(self.server, OwnerServer)
                if self.path != ('/owner' if owner else '/task') or self.headers.get('Origin') or self.headers.get('Transfer-Encoding'):
                    raise KitError('SCOPE_DENIED')
                if self.headers.get('Content-Type') != 'application/json':
                    raise KitError('INVALID_ARGUMENT')
                lengths = self.headers.get_all('Content-Length', [])
                length = int(lengths[0]) if len(lengths) == 1 else 0
                if not 0 < length <= 65536:
                    raise KitError('INVALID_ARGUMENT')
                request = json.loads(self.rfile.read(length))
                authorization = self.headers.get('Authorization', '')
                result = service.dispatch(request, owner, authorization[7:] if authorization.startswith('Bearer ') else '')
            except KitError as error:
                result, status = {'error': str(error)}, 403
            except Exception:
                result, status = {'error': 'KIT_UNAVAILABLE'}, 502
            raw = json.dumps(result).encode()
            self.send_response(status)
            self.send_header('Content-Type', 'application/json')
            self.send_header('Cache-Control', 'no-store')
            self.send_header('Content-Length', str(len(raw)))
            self.end_headers()
            try:
                self.wfile.write(raw)
            except OSError:
                pass

    # Fail rather than unlink a live/stale socket or steal another kit service.
    owner = OwnerServer(args.owner_socket, Handler)
    os.chmod(args.owner_socket, 0o600)
    task = None
    try:
        if args.task_listen:
            host, port = args.task_listen.rsplit(':', 1)
            if not ipaddress.ip_address(host).is_loopback and not (args.tls_cert and args.tls_key):
                raise KitError('REMOTE_TASK_LISTENER_REQUIRES_TLS')
            task = HTTPServer((host, int(port)), Handler)
            if args.tls_cert or args.tls_key:
                context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
                context.minimum_version = ssl.TLSVersion.TLSv1_2
                context.load_cert_chain(args.tls_cert, args.tls_key)
                task.socket = context.wrap_socket(task.socket, server_side=True)
            threading.Thread(target=task.serve_forever, daemon=True).start()
        signal.signal(signal.SIGTERM, lambda *_: (_ for _ in ()).throw(KeyboardInterrupt()))
        owner.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        if task:
            task.shutdown()
            task.server_close()
        owner.server_close()
        Path(args.owner_socket).unlink()
        # Browser custody survives restart in private mode; retain IDs for an
        # explicit owner delete. Never auto-delete the agent workload or hide a
        # cleanup failure when a service is stopped.


def call(args):
    request = json.load(sys.stdin)  # private input: never a password in argv
    timeout = 900 if request.get('operation') == 'install' else 65
    conn = http.client.HTTPConnection('localhost', timeout=timeout)
    conn.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    conn.sock.settimeout(timeout)
    conn.sock.connect(args.owner_socket)
    try:
        conn.request('POST', '/owner', json.dumps(request), {'Content-Type': 'application/json'})
        response = conn.getresponse()
        result = json.loads(response.read(4 * 1024 * 1024))
        if 'png' in result:
            if not args.private_png:
                raise KitError('PRIVATE_PNG_PATH_REQUIRED')
            fd = os.open(args.private_png, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(fd, 'wb') as file:
                file.write(base64.b64decode(result['png'], validate=True))
            print(json.dumps({'private_png_saved': True}))
        else:
            print(json.dumps(result))
        return 0 if response.status == 200 else 1
    finally:
        conn.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    daemon = commands.add_parser('serve')
    daemon.add_argument('--config', required=True)
    daemon.add_argument('--root', required=True, help='existing private owner directory; retained browser cleanup identities')
    daemon.add_argument('--owner-socket', required=True)
    daemon.add_argument('--task-listen', help='IP:port; scoped task API, TLS required off-loopback')
    daemon.add_argument('--tls-cert')
    daemon.add_argument('--tls-key')
    client = commands.add_parser('call', help='owner JSON request on stdin, never command-line secrets')
    client.add_argument('--owner-socket', required=True)
    client.add_argument('--private-png', help='new mode-0600 image, never screenshot stdout')
    args = parser.parse_args()
    try:
        return serve(args) if args.command == 'serve' else call(args)
    except Exception:
        print('lamp-kits: operation failed; private diagnostics withheld', file=sys.stderr)
        return 1


if __name__ == '__main__':
    sys.exit(main())
