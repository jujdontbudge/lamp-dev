#!/usr/bin/env python3
"""Disposable native loopback TLS for the real Vaultwarden/Bitwarden CLI journey.

No host trust store, Docker, request logging or public listener. The disposable
certificate is trusted explicitly by this journey's client processes only.
"""
import argparse
import http.client
import http.server
import os
from pathlib import Path
import ssl
import subprocess


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--root', required=True)
    args = p.parse_args()
    os.umask(0o077)
    root = Path(args.root).resolve(); root.mkdir(mode=0o700)
    cert, key = root / 'ca.pem', root / 'key.pem'
    subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '2',
        '-subj', '/CN=T22 disposable loopback', '-addext', 'subjectAltName=IP:127.0.0.1,DNS:localhost',
        '-keyout', str(key), '-out', str(cert)], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True)
    class Proxy(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def forward(self):
            length = int(self.headers.get('Content-Length', '0'))
            if not 0 <= length <= 8 * 1024 * 1024:
                self.send_error(413); return
            upstream = http.client.HTTPConnection('127.0.0.1', 18224)
            try:
                headers = {k: v for k, v in self.headers.items() if k.lower() not in ('host','connection','transfer-encoding','content-length')}
                upstream.request(self.command, self.path, self.rfile.read(length), headers)
                response = upstream.getresponse()
                body = response.read(8 * 1024 * 1024 + 1)
                if len(body) > 8 * 1024 * 1024:
                    self.send_error(502); return
                self.send_response(response.status)
                for k, v in response.getheaders():
                    if k.lower() not in ('connection','transfer-encoding','content-length'): self.send_header(k,v)
                self.send_header('Content-Length', str(len(body))); self.end_headers()
                self.wfile.write(body)
            finally: upstream.close()
        do_GET = do_POST = do_PUT = do_DELETE = forward
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 18225), Proxy)
    context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    context.load_cert_chain(cert, key)
    server.socket = context.wrap_socket(server.socket, server_side=True)
    try: server.serve_forever()
    finally: server.server_close()


if __name__ == '__main__': main()
