import argparse
from http.server import BaseHTTPRequestHandler
import os
from pathlib import Path
import shlex
import socketserver
import subprocess
from urllib.parse import unquote, urlsplit


RELAY = """import socket,sys
data=sys.stdin.buffer.read(131073)
if len(data)>131072: raise SystemExit(1)
with socket.socket(socket.AF_UNIX) as s:
 s.settimeout(65)
 s.connect(sys.argv[1])
 s.sendall(data)
 s.shutdown(socket.SHUT_WR)
 size=0
 while True:
  block=s.recv(65536)
  if not block: break
  size+=len(block)
  if size>16777216: raise SystemExit(1)
  sys.stdout.buffer.write(block)
 sys.stdout.buffer.flush()
"""


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--host", required=True)
    parser.add_argument("--remote-socket", required=True)
    parser.add_argument("--socket", type=Path, required=True)
    parser.add_argument("--control-path")
    args = parser.parse_args()
    if args.host.startswith("-") or not args.remote_socket.startswith("/"):
        raise ValueError("use an SSH host and an absolute public API socket")
    parent = args.socket.parent
    parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    if parent.is_symlink() or parent.stat().st_mode & 0o077:
        raise ValueError("console transport requires a private parent directory")
    command = ["ssh", "-T", "-o", "BatchMode=yes"]
    if args.control_path:
        command += ["-o", "ControlMaster=auto", "-o", "ControlPersist=120", "-o", "ControlPath=" + args.control_path]
    command += [args.host, "sudo", "-n", "python3", "-c", shlex.quote(RELAY), shlex.quote(args.remote_socket)]

    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def handle_request(self):
            path = unquote(urlsplit(self.path).path)
            if not path.startswith("/v1/") or ".." in path.split("/") or self.headers.get("Transfer-Encoding"):
                self.send_error(400)
                return
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 <= length <= 128 << 10:
                self.send_error(413)
                return
            headers = [(k, v) for k, v in self.headers.items() if k.lower() not in {"host", "connection", "content-length", "transfer-encoding"}]
            request = f"{self.command} {self.path} HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\nContent-Length: {length}\r\n"
            request += "".join(k + ": " + v + "\r\n" for k, v in headers) + "\r\n"
            result = subprocess.run(command, input=request.encode("ascii") + self.rfile.read(length), stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=70)
            if result.returncode or not result.stdout.startswith(b"HTTP/1."):
                self.send_error(502)
            else:
                self.wfile.write(result.stdout)
            self.close_connection = True

        do_GET = do_POST = do_PATCH = do_DELETE = handle_request

    os.umask(0o077)
    with socketserver.ThreadingUnixStreamServer(str(args.socket), Handler) as server:
        os.chmod(args.socket, 0o600)
        server.serve_forever()


if __name__ == "__main__":
    main()
