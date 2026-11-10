# E05 guest workload: fork with full-dirty RAM and in-flight I/O.
#
# Holds a RAM-only buffer whose 4 KiB pages carry (page index, generation)
# markers and keeps two live threads running while captures are taken:
#   - a counter (proves the process continues on both sides), and
#   - a writer putting checksummed 4 KiB records into a ring in /root/io.log
#     and fsync'ing every BATCH records before publishing the last sequence
#     number as `acked`. It never pauses for a capture, so every capture
#     lands with written-but-unsynced records and a flush in flight.
# `check` validates every page and every acknowledged record still in the
# log's ring: it must be on disk intact (an acknowledged fsync survived the
# capture). Records written after the last fsync are in flight at a capture.
# Clocks: CLOCK_MONOTONIC must never go backwards inside one instance.
import hashlib, os, socket, struct, sys, threading, time

MIB = int(sys.argv[1])
N = MIB << 20
REC = 4096
LOG = "/root/io.log"
buf = bytearray(N)
gen = 0
counter = 0
acked = 0
mono_back = 0
last_mono = time.monotonic_ns()
lock = threading.Lock()
io_lock = threading.Lock()


def rewrite():
    global gen
    with lock:
        gen += 1
        tag = gen.to_bytes(8, "little")
        for i in range(0, N, 4096):
            buf[i:i + 16] = i.to_bytes(8, "little") + tag


def tick():
    global counter, last_mono, mono_back
    while True:
        counter += 1
        now = time.monotonic_ns()
        if now < last_mono:
            mono_back += 1
        last_mono = now
        time.sleep(0.001)


def record(seq, salt):
    body = struct.pack("<QQ", seq, salt) + hashlib.sha256(b"%d:%d" % (seq, salt)).digest()
    return body + b"\0" * (REC - len(body))


SALT = int.from_bytes(os.urandom(8), "little")


RING = 4096  # 16 MiB ring of records: bounded disk use over any run length.
BATCH = 8    # records per fsync; the unsynced ones are the in-flight I/O.


def writer():
    global acked
    fd = os.open(LOG, os.O_RDWR | os.O_CREAT | os.O_TRUNC, 0o600)
    seq = 0
    while True:
        for _ in range(BATCH):
            seq += 1
            os.pwrite(fd, record(seq, SALT), (seq % RING) * REC)
            time.sleep(0.005)
        os.fsync(fd)
        with io_lock:
            acked = seq


def verify_log():
    """Every acknowledged record still in the ring must be on disk intact
    (or already overwritten by a later record of the same slot)."""
    with io_lock:
        want = acked
    # Acknowledged records are clean pages; dropping clean page cache makes
    # the read below come from this instance's own disk, not from RAM.
    with open("/proc/sys/vm/drop_caches", "w") as f:
        f.write("1")
    with open(LOG, "rb") as f:
        data = f.read()
    lost = torn = 0
    newest = 0
    for seq in range(max(1, want - RING + 1), want + 1):
        rec = data[(seq % RING) * REC:(seq % RING + 1) * REC]
        if len(rec) < REC:
            lost += 1
            continue
        got, salt = struct.unpack_from("<QQ", rec)
        if salt != SALT or rec != record(got, salt) or got % RING != seq % RING:
            torn += 1
        elif got < seq:
            lost += 1
        newest = max(newest, got)
    return want, newest, lost, torn


def check():
    tag = gen.to_bytes(8, "little")
    bad = 0
    for i in range(0, N, 4096):
        if buf[i:i + 16] != i.to_bytes(8, "little") + tag:
            bad += 1
    return bad


rewrite()
threading.Thread(target=tick, daemon=True).start()
threading.Thread(target=writer, daemon=True).start()
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
            bad = check()
            digest = hashlib.sha256(buf).hexdigest()[:16]
        want, newest, lost, torn = verify_log()
        out = (f"gen={gen} bad_pages={bad} pages={N // 4096} counter={counter} "
               f"acked={want} newest_on_disk={newest} lost_acked={lost} torn={torn} "
               f"mono_back={mono_back} mono_ns={time.monotonic_ns()} salt={SALT} digest={digest}")
    else:
        out = f"gen={gen} counter={counter} acked={acked}"
    c.sendall(out.encode())
    c.close()
