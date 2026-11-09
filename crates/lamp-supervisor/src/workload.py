# Guest workload for fork evidence: holds a RAM-only buffer whose pages carry
# (page index, generation) markers, keeps a live counter thread running and
# answers checks on a Unix socket. Nothing it holds is written to disk.
import hashlib, os, socket, sys, threading, time

MIB = int(sys.argv[1])
N = MIB << 20
buf = bytearray(N)
gen = 0
counter = 0
lock = threading.Lock()


def rewrite():
    global gen
    with lock:
        gen += 1
        tag = gen.to_bytes(8, "little")
        for i in range(0, N, 4096):
            buf[i:i + 16] = i.to_bytes(8, "little") + tag


def tick():
    global counter
    while True:
        counter += 1
        time.sleep(0.001)


def check():
    # Every page must hold its own index and the current generation.
    tag = gen.to_bytes(8, "little")
    bad = 0
    for i in range(0, N, 4096):
        if buf[i:i + 16] != i.to_bytes(8, "little") + tag:
            bad += 1
    return bad


rewrite()
threading.Thread(target=tick, daemon=True).start()
path = "/run/lamp-workload.sock"
try:
    os.unlink(path)
except FileNotFoundError:
    pass
srv = socket.socket(socket.AF_UNIX)
srv.bind(path)
srv.listen(8)
open("/run/lamp-workload.ready", "w").write("1")
while True:
    c, _ = srv.accept()
    cmd = c.recv(64).decode().strip()
    if cmd == "rewrite":
        rewrite()
        out = f"gen={gen}"
    elif cmd == "check":
        with lock:
            out = f"gen={gen} bad_pages={check()} pages={N // 4096} counter={counter} digest={hashlib.sha256(buf).hexdigest()[:16]}"
    else:
        out = f"gen={gen} counter={counter}"
    c.sendall(out.encode())
    c.close()
