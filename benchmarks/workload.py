import argparse
import gzip
import hashlib
import http.client
import json
import os
from pathlib import Path
import random
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request
import urllib.parse


def emit(value):
    print(json.dumps(value, separators=(",", ":")), flush=True)


def blocks(nonce, generation, size, domain):
    for index in range((size + 4095) // 4096):
        seed = ":".join((nonce, str(generation), domain, str(index)))
        block = hashlib.sha256(seed.encode()).digest() * 128
        yield block[:min(4096, size - index * 4096)]


def resident(path, nonce, size):
    memory = bytearray(size)
    generation = 0
    ticks = 1
    disk = path + ".disk"

    def write(number):
        offset = 0
        for block in blocks(nonce, number, size, "ram"):
            memory[offset:offset + len(block)] = block
            offset += len(block)
        with open(disk, "wb") as output:
            for block in blocks(nonce, number, 16384, "disk"):
                output.write(block)
            output.flush()
            os.fsync(output.fileno())

    write(0)
    with socket.socket(socket.AF_UNIX) as server:
        server.bind(path)
        server.listen()
        while True:
            connection, _ = server.accept()
            with connection:
                request = json.loads(connection.makefile("rb").readline())
                ticks += 1
                if request["op"] == "write":
                    generation = request["generation"]
                    write(generation)
                result = {
                    "nonce": nonce, "generation": generation, "ticks": ticks,
                    "pid": os.getpid(),
                }
                if request["op"] != "ping":
                    result["memory_sha256"] = hashlib.sha256(memory).hexdigest()
                    result["disk_sha256"] = hashlib.sha256(Path(disk).read_bytes()).hexdigest()
                connection.sendall(json.dumps(result).encode() + b"\n")


def call_resident(path, request):
    with socket.socket(socket.AF_UNIX) as client:
        client.settimeout(120)
        client.connect(path)
        client.sendall(json.dumps(request).encode() + b"\n")
        return json.loads(client.makefile("rb").readline())


def measure(name, directory, fixture=None, proxy=None, compiler="cc"):
    start = time.perf_counter_ns()
    units = 1
    with tempfile.TemporaryDirectory(dir=directory, prefix="lamp-work-") as root:
        root = Path(root)
        data = hashlib.shake_256(b"lamp.benchmark/v1 fixed input").digest(1024 * 1024)
        file = root / "data"
        if name == "compile":
            source = root / "work.c"
            source.write_text("#include <stdint.h>\nint main(void){uint64_t x=1;for(int i=0;i<1000000;i++)x=x*6364136223846793005ULL+1;return x==0;}\n")
            start = time.perf_counter_ns()
            subprocess.run([compiler, "-O2", "-o", str(root / "work"), str(source)], check=True, capture_output=True)
            subprocess.run([str(root / "work")], check=True)
        elif name == "compression":
            start = time.perf_counter_ns()
            for _ in range(8):
                compressed = gzip.compress(data, mtime=0)
                if gzip.decompress(compressed) != data:
                    raise ValueError("compression output")
            units = len(data) * 8
        elif name == "crypto":
            start = time.perf_counter_ns()
            for _ in range(128):
                actual = hashlib.sha256(data).digest()
            if actual.hex() != hashlib.sha256(data).hexdigest():
                raise ValueError("crypto output")
            units = len(data) * 128
        elif name in ("seq_read", "seq_write", "random_read", "random_write", "fsync", "cow_first_write"):
            with file.open("w+b", buffering=0) as output:
                for _ in range(32):
                    output.write(data)
                os.fsync(output.fileno())
                if name == "cow_first_write":
                    raise ValueError("cow_first_write requires a captured private head; use cow-prepare/cow-write")
                rng = random.Random(1901)
                start = time.perf_counter_ns()
                if name.startswith("seq"):
                    output.seek(0)
                    for _ in range(32):
                        if name == "seq_read":
                            if output.read(len(data)) != data:
                                raise ValueError("sequential read")
                        else:
                            output.write(data)
                    if name == "seq_write":
                        os.fsync(output.fileno())
                    units = 32 * len(data)
                elif name.startswith("random"):
                    for _ in range(512):
                        output.seek(rng.randrange(32 * 256) * 4096)
                        if name == "random_read":
                            position = output.tell()
                            if output.read(4096) != data[position % len(data):position % len(data) + 4096]:
                                raise ValueError("random read")
                        else:
                            output.write(data[:4096])
                    if name == "random_write":
                        os.fsync(output.fileno())
                    units = 512 * 4096
                else:
                    for _ in range(32):
                        output.write(data[:4096])
                        os.fsync(output.fileno())
                    units = 32
        elif name == "tree":
            start = time.perf_counter_ns()
            for index in range(256):
                with (root / str(index)).open("wb") as output:
                    output.write(data[:4096])
                    output.flush()
                    os.fsync(output.fileno())
            directory_fd = os.open(root, os.O_RDONLY)
            try:
                os.fsync(directory_fd)
            finally:
                os.close(directory_fd)
            units = 256
        else:
            return network_measure(name, fixture, proxy)
        elapsed = (time.perf_counter_ns() - start) / 1e9
    return {"value": elapsed / units if name == "fsync" else units / elapsed, "elapsed_s": elapsed, "units": units}


def network_measure(name, fixture, proxy):
    if not fixture:
        raise ValueError("independent remote network fixture required")
    host, tcp, udp = fixture["host"], fixture["tcp_port"], fixture["udp_port"]
    multiplier = (32 if name.startswith("udp") else 1024) if name.endswith("goodput") else 1
    payload = hashlib.sha256(b"lamp-network-v1").digest() * multiplier
    count = 512 if name.endswith("packets") else 64
    started = time.perf_counter_ns()
    if name == "proxy":
        if not proxy:
            raise ValueError("required proxy path missing")
        run_id = os.urandom(16).hex()
        headers = {"X-Lamp-Run": run_id}
        if proxy == "direct":
            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
            for index in range(count):
                headers["X-Lamp-Request"] = str(index)
                request = urllib.request.Request(fixture["http_url"], data=payload, headers=headers)
                with opener.open(request, timeout=10) as response:
                    if response.read() != payload:
                        raise ValueError("direct HTTP payload mismatch")
        else:
            parsed = urllib.parse.urlsplit(proxy)
            if parsed.scheme not in ("http", "https") or parsed.username or parsed.password or parsed.query:
                raise ValueError("proxy must be a credential-free HTTP(S) endpoint")
            cls = http.client.HTTPSConnection if parsed.scheme == "https" else http.client.HTTPConnection
            for index in range(count):
                connection = cls(parsed.hostname, parsed.port, timeout=10)
                try:
                    headers["X-Lamp-Request"] = str(index)
                    connection.request("POST", fixture["http_url"], body=payload, headers=headers)
                    response = connection.getresponse()
                    if response.status != 200 or response.read() != payload:
                        raise ValueError("required proxy payload mismatch")
                finally:
                    connection.close()
    elif name == "small_flows":
        for _ in range(count):
            with socket.create_connection((host, tcp), timeout=10) as connection:
                connection.sendall(payload)
                if receive(connection, len(payload)) != payload:
                    raise ValueError("small flow payload mismatch")
    else:
        family = socket.SOCK_DGRAM if name.startswith("udp") else socket.SOCK_STREAM
        with socket.socket(socket.AF_INET, family) as connection:
            connection.settimeout(10)
            connection.connect((host, udp if family == socket.SOCK_DGRAM else tcp))
            for index in range(count):
                packet = index.to_bytes(8, "big") + payload
                connection.sendall(packet)
                reply = connection.recv(65536) if family == socket.SOCK_DGRAM else receive(connection, len(packet))
                if reply != packet:
                    raise ValueError("network loss/reordering/corruption")
    elapsed = (time.perf_counter_ns() - started) / 1e9
    units = count * len(payload) if name.endswith("goodput") else count
    latency = name in ("request_latency", "proxy")
    result = {"value": elapsed / count if latency else units / elapsed, "elapsed_s": elapsed, "units": units}
    if name == "proxy":
        receipt_url = fixture["http_url"] + ("&" if "?" in fixture["http_url"] else "?") + "lamp_run=" + run_id
        opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        with opener.open(receipt_url, timeout=10) as response:
            receipt = json.load(response)
        if receipt != {"run_id": run_id, "requests": sorted(str(index) for index in range(count))}:
            raise ValueError("proxy origin receipt mismatch")
        result.update({"origin_run_id": run_id, "origin_requests": len(receipt["requests"])})
    return result


def receive(connection, size):
    result = bytearray()
    while len(result) < size:
        part = connection.recv(size - len(result))
        if not part:
            raise EOFError("short network response")
        result.extend(part)
    return bytes(result)


def serve_network(host, tcp, udp, http):
    from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

    class TCP(socketserver.BaseRequestHandler):
        def handle(self):
            while data := self.request.recv(65536):
                self.request.sendall(data)

    class UDP(socketserver.BaseRequestHandler):
        def handle(self):
            data, connection = self.request
            connection.sendto(data, self.client_address)

    receipts = {}

    class HTTP(BaseHTTPRequestHandler):
        def do_GET(self):
            run_id = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query).get("lamp_run", [""])[0]
            data = json.dumps({"run_id": run_id, "requests": sorted(receipts.pop(run_id, set()))}).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def do_POST(self):
            if not self.path.startswith("/"):
                self.send_error(400, "origin-form request required")
                return
            data = self.rfile.read(min(int(self.headers["Content-Length"]), 65536))
            run_id, index = self.headers.get("X-Lamp-Run"), self.headers.get("X-Lamp-Request")
            if run_id and index is not None:
                receipts.setdefault(run_id, set()).add(index)
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *args):
            pass

    servers = [
        socketserver.ThreadingTCPServer((host, tcp), TCP),
        socketserver.ThreadingUDPServer((host, udp), UDP),
        ThreadingHTTPServer((host, http), HTTP),
    ]
    for server in servers:
        threading.Thread(target=server.serve_forever, daemon=True).start()
    emit({"tcp_port": servers[0].server_address[1], "udp_port": servers[1].server_address[1], "http_port": servers[2].server_address[1]})
    threading.Event().wait()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("command", choices=("identity", "useful", "resident", "start", "call", "measure", "cow-prepare", "cow-write", "cow-check", "cow-remove", "serve-network"))
    parser.add_argument("--path")
    parser.add_argument("--nonce")
    parser.add_argument("--bytes", type=int, default=0)
    parser.add_argument("--request", default='{"op":"ping"}')
    parser.add_argument("--measure")
    parser.add_argument("--directory", default="/tmp")
    parser.add_argument("--fixture")
    parser.add_argument("--proxy")
    parser.add_argument("--compiler", default="cc")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--tcp", type=int, default=0)
    parser.add_argument("--udp", type=int, default=0)
    parser.add_argument("--http", type=int, default=0)
    parser.add_argument("--cpus")
    args = parser.parse_args()
    if args.cpus:
        os.sched_setaffinity(0, {int(cpu) for cpu in args.cpus.split(",")})
    if args.command == "identity":
        emit({"sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(), "python": sys.version, "compiler": subprocess.check_output([args.compiler, "--version"]).decode()})
    elif args.command == "useful":
        emit({"nonce": args.nonce, "answer": hashlib.sha256(args.nonce.encode()).hexdigest()})
    elif args.command == "resident":
        resident(args.path, args.nonce, args.bytes)
    elif args.command == "start":
        subprocess.Popen([sys.executable, __file__, "resident", "--path", args.path, "--nonce", args.nonce, "--bytes", str(args.bytes)], stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, start_new_session=True)
        deadline = time.monotonic() + 120
        while not Path(args.path).exists():
            if time.monotonic() > deadline:
                raise TimeoutError("resident start")
            time.sleep(.01)
        emit(call_resident(args.path, {"op": "ping"}))
    elif args.command == "call":
        emit(call_resident(args.path, json.loads(args.request)))
    elif args.command == "measure":
        emit(measure(args.measure, args.directory, json.loads(args.fixture) if args.fixture else None, args.proxy, args.compiler))
    elif args.command == "cow-prepare":
        with open(args.path, "xb") as output:
            output.write(hashlib.shake_256(b"cow-first-write").digest(32 * 1024 * 1024))
            output.flush()
            os.fsync(output.fileno())
        emit({"prepared": True})
    elif args.command == "cow-write":
        started = time.perf_counter_ns()
        with open(args.path, "r+b", buffering=0) as output:
            for index in range(512):
                output.seek(index * 65536)
                output.write(b"x" * 4096)
            os.fsync(output.fileno())
        elapsed = (time.perf_counter_ns() - started) / 1e9
        expected = bytearray(hashlib.shake_256(b"cow-first-write").digest(32 * 1024 * 1024))
        for index in range(512):
            expected[index * 65536:index * 65536 + 4096] = b"x" * 4096
        if Path(args.path).read_bytes() != expected:
            raise ValueError("first-write data corruption")
        emit({"value": elapsed / 512, "elapsed_s": elapsed, "units": 512})
    elif args.command == "cow-check":
        if Path(args.path).read_bytes() != hashlib.shake_256(b"cow-first-write").digest(32 * 1024 * 1024):
            raise ValueError("first-write changed the source head")
        emit({"source_unchanged": True})
    elif args.command == "cow-remove":
        if not Path(args.path).name.startswith("lamp-cow-"):
            raise ValueError("not a harness-created CoW workload")
        Path(args.path).unlink()
        emit({"removed": True})
    else:
        serve_network(args.host, args.tcp, args.udp, args.http)


if __name__ == "__main__":
    main()
