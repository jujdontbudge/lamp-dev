#!/usr/bin/env python3
"""Root-only, isolated real-kernel T4 tests. No Docker, host firewall or service changes.
Usage: sudo python3 tests/guard/e2e.py /absolute/path/to/lamp-guard
"""
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess as sp
import sys
import tempfile
import time
import uuid

BINARY = str(Path(sys.argv[1]).resolve())
ROOT = Path(tempfile.mkdtemp(prefix="lamp-t4-"))
PROCESSES = []
NAMESPACES = []
RESULTS = []


def cmd(*args, **kw):
    return sp.run([str(a) for a in args], check=True, text=True, capture_output=True, **kw)


def launch(*args, **kw):
    p = sp.Popen([str(a) for a in args], text=True, stdout=sp.PIPE, stderr=sp.PIPE, **kw)
    PROCESSES.append(p)
    return p


def config(name, interface="task0", size=4 * 1024 * 1024):
    path = ROOT / (name + ".json")
    data = dict(attribution=dict(principal_id="test-owner", host_id=os.uname().nodename, task_id=name,
                sandbox_id=name, generation=1, session_id="e2e", operation_id="e2e",
                policy_revision=1), interface=interface, guest_ip="198.18.240.2", queue=314,
                host_denies=["198.18.240.1/32", "198.18.241.0/30", "9.9.9.0/24"],
                spool=str(ROOT / (name + ".ndjson")), spool_bytes=size, guard_uid=1000001)
    path.write_text(json.dumps(data))
    return path


def records(name):
    return [json.loads(s) for s in (ROOT / (name + ".ndjson")).read_text().splitlines()]


def profile(process, net_admin):
    status = Path(f"/proc/{process.pid}/status").read_text()
    fields = dict(line.split(":", 1) for line in status.splitlines() if ":" in line)
    assert fields["Uid"].split() == ["1000001"] * 4
    assert int(fields["CapEff"].strip(), 16) == (1 << 12 if net_admin else 0)
    assert fields["NoNewPrivs"].strip() == "1" and fields["Seccomp"].strip() == "2"
    assert len(list(Path(f"/proc/{process.pid}/fd").iterdir())) <= 5


def ok(name):
    RESULTS.append(name)
    print("PASS", name, flush=True)


def netns():
    prefix = "lt4-" + uuid.uuid4().hex[:7]
    guest, guard, edge = [prefix + s for s in ("g", "r", "e")]
    for ns in (guest, guard, edge):
        cmd("ip", "netns", "add", ns)
        NAMESPACES.append(ns)
        cmd("ip", "-n", ns, "link", "set", "lo", "up")
    # All devices are created *inside* these disposable namespaces.
    cmd("ip", "-n", guest, "link", "add", "eth0", "type", "veth", "peer", "name", "task0", "netns", guard)
    cmd("ip", "-n", guard, "link", "add", "uplink0", "type", "veth", "peer", "name", "eth0", "netns", edge)
    for ns, dev, addr in ((guest,"eth0","198.18.240.2/30"), (guard,"task0","198.18.240.1/30"),
                          (guard,"uplink0","198.18.241.1/30"), (edge,"eth0","198.18.241.2/30")):
        cmd("ip", "-n", ns, "addr", "add", addr, "dev", dev)
        cmd("ip", "-n", ns, "link", "set", dev, "up")
    cmd("ip", "-n", guest, "route", "add", "default", "via", "198.18.240.1")
    cmd("ip", "-n", guard, "route", "add", "default", "via", "198.18.241.2")
    cmd("ip", "-n", edge, "route", "add", "198.18.240.0/30", "via", "198.18.241.1")
    cmd("ip", "netns", "exec", guard, "sysctl", "-qw", "net.ipv4.ip_forward=1")
    for addr in ("8.8.8.8", "9.9.9.9", "10.0.0.7", "169.254.169.254", "168.63.129.16", "100.100.100.200"):
        cmd("ip", "-n", edge, "addr", "add", addr + "/32", "dev", "lo")
    # Reflectors deliberately listen on denied and allowed fixture IPs alike.
    server = r'''
import socket,threading
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('0.0.0.0',18080));s.listen()
def tcp():
 while True:
  c,_=s.accept()
  def echo(c):
   try:
    while True:
     b=c.recv(4096)
     if not b: break
     c.sendall(b)
   finally: c.close()
  threading.Thread(target=echo,args=(c,),daemon=True).start()
threading.Thread(target=tcp,daemon=True).start()
def udp(u):
 while True:
  b,a=u.recvfrom(4096);u.sendto(b,a)
for ip in ('8.8.8.8','9.9.9.9','10.0.0.7','169.254.169.254','168.63.129.16','100.100.100.200'):
 u=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);u.bind((ip,18080));threading.Thread(target=udp,args=(u,),daemon=True).start()
print('ready',flush=True)
threading.Event().wait()
'''
    reflector = launch("ip", "netns", "exec", edge, "python3", "-u", "-c", server)
    assert reflector.stdout.readline().strip() == "ready"
    cfg = config("network")
    daemon = launch("ip", "netns", "exec", guard, BINARY, "run", cfg)
    assert json.loads(daemon.stdout.readline())["state"] == "ready"
    profile(daemon, True)
    ok("guard drops root, retains only net-admin and enforces no-new-privileges/seccomp/assigned FDs")

    client = r'''
import socket,sys
kind,dst,source=sys.argv[1:];s=socket.socket(socket.AF_INET,socket.SOCK_STREAM if kind=='tcp' else socket.SOCK_DGRAM);s.settimeout(.35)
if source!='-':s.bind((source,0))
try:
 s.connect((dst,18080));s.sendall(b'lamp-guard');assert s.recv(32)==b'lamp-guard';print('allowed')
except (OSError,AssertionError):print('denied')
'''

    def probe(kind, dest, allow, source="-"):
        result = cmd("ip", "netns", "exec", guest, "python3", "-c", client, kind, dest, source)
        assert result.stdout.strip() == ("allowed" if allow else "denied"), (kind, dest, result)

    for kind in ("tcp", "udp"):
        probe(kind, "8.8.8.8", True)
        for ip in ("9.9.9.9", "10.0.0.7", "169.254.169.254", "168.63.129.16", "100.100.100.200", "198.18.240.1"):
            probe(kind, ip, False)
    ok("public TCP/UDP allowed; private, metadata, fleet, host and public-numbered LAN denied")
    # Root inside the task namespace can change its own routes/firewall but not guard policy.
    cmd("ip", "netns", "exec", guest, "nft", "flush", "ruleset")
    cmd("ip", "-n", guest, "addr", "add", "8.8.4.4/32", "dev", "eth0")
    probe("udp", "8.8.8.8", False, "8.8.4.4")
    probe("udp", "10.0.0.7", False)
    ok("guest root firewall changes and source spoof cannot bypass guard")
    data = records("network")
    assert any(r["details"].get("decision") == "allow" for r in data)
    assert any(r["details"].get("reason") == "return_flow" for r in data)
    assert any(r["details"].get("decision") == "deny" for r in data)
    assert all(r["sequence"] == i + 1 and r["attribution"]["generation"] == 1 for i, r in enumerate(data))
    ok("host spool contains attributed, ordered allow/deny and bidirectional byte records")

    # Protected namespace-root task and its descendants use this SAME guarded link.
    processes(guest)

    # Keep the same socket/5-tuple alive across guard death, not just new dials.
    live = r'''
import socket,time,sys
s=socket.socket(socket.AF_INET,socket.SOCK_STREAM if sys.argv[1]=='tcp' else socket.SOCK_DGRAM);s.settimeout(.5);s.connect(('8.8.8.8',18080));s.send(b'first');assert s.recv(16)==b'first';print('ready',flush=True);sys.stdin.readline();s.send(b'after')
try:print('LEAK' if s.recv(16) else 'blocked',flush=True)
except OSError:print('blocked',flush=True)
'''
    active = []
    for kind in ("tcp", "udp"):
        p = launch("ip", "netns", "exec", guest, "python3", "-u", "-c", live, kind, stdin=sp.PIPE)
        assert p.stdout.readline().strip() == "ready"
        active.append(p)
    daemon.kill(); daemon.wait(timeout=3)
    for p in active:
        p.stdin.write("go\n");p.stdin.flush()
        assert p.stdout.readline().strip() == "blocked"
        p.wait(timeout=3)
    probe("tcp", "8.8.8.8", False)
    ok("guard death closes existing and new flows with nft rules left installed")

    retry = launch("ip", "netns", "exec", guard, BINARY, "run", config("restart-rejected"))
    assert retry.wait(timeout=3) == 70 and not retry.stdout.read()
    ok("restart cannot merge into or replace an existing generation's nft table")

    # A full durable spool is also a fail-closed event.
    cmd("ip", "netns", "exec", guard, "nft", "delete", "table", "inet", "lamp_guard")
    full = launch("ip", "netns", "exec", guard, BINARY, "run", config("network-full", size=4096))
    assert json.loads(full.stdout.readline())["state"] == "ready"
    for _ in range(20):
        if full.poll() is not None: break
        cmd("ip", "netns", "exec", guest, "python3", "-c", client, "udp", "8.8.8.8", "-")
    assert full.wait(timeout=3) == 70
    assert "spool full" in full.stderr.read()
    probe("udp", "8.8.8.8", False)
    ok("spool exhaustion stops network admission, including established traffic")


def pair(name, payload, namespace):
    a,b = socket.socketpair()
    sink = launch(BINARY, "receive", config(name), a.fileno(), pass_fds=(a.fileno(),))
    collect = launch("ip", "netns", "exec", namespace, BINARY, "collect", b.fileno(), 1000000, "--", "/usr/bin/python3", "-u", "-c", payload,
                     pass_fds=(b.fileno(),))
    a.close();b.close()
    return sink, collect


def processes(namespace):
    workload = r'''
import ctypes,errno,json,os,platform,socket,threading
assert os.getuid()==0 and os.getppid()==0
assert sorted(int(p) for p in os.listdir('/proc') if p.isdigit())==[1]
assert not any('socket:' in os.readlink('/proc/self/fd/'+f) for f in os.listdir('/proc/self/fd') if os.path.exists('/proc/self/fd/'+f))
libc=ctypes.CDLL(None,use_errno=True)
clone=56 if platform.machine()=='x86_64' else 220
assert libc.syscall(clone,0x00800000|17,0,0,0,0)==-1 and ctypes.get_errno()==errno.EPERM
assert libc.syscall(435,0,0)==-1 and ctypes.get_errno()==errno.ENOSYS
assert libc.syscall(425,1,0)==-1 and ctypes.get_errno()==errno.EPERM
try: socket.socket(40,socket.SOCK_STREAM);raise AssertionError('vsock bypass')
except OSError as e: assert e.errno==errno.EPERM
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(.25);s.connect(('8.8.8.8',18080));s.send(b'protected-root');assert s.recv(64)==b'protected-root';s.close()
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.settimeout(.25);s.connect(('169.254.169.254',18080));s.send(b'must-not-leak')
try:s.recv(64);raise AssertionError('metadata bypass')
except socket.timeout:pass
s.close()
os.setresuid(0,0,0)
t=threading.Thread(target=lambda:None);t.start();t.join()
children=[]
for i in range(4):
 p=os.fork()
 if p==0:
  if i==3:os.execve('/usr/bin/true',['misleading-argv-zero'],{})
  os._exit(0)
 children.append(p)
for p in children:os.waitpid(p,0)
print(json.dumps({'task_pid':os.getpid(),'fork_pids':children,'root':os.getuid(),'tracer_hidden':True}),flush=True)
'''
    sink, collect = pair("process", workload, namespace)
    output, errors = collect.communicate(timeout=30)
    assert collect.returncode == 0, (output, errors)
    report = json.loads(output)
    assert sink.wait(timeout=3) == 0
    data = records("process")
    details = [r["details"]["details"] for r in data]
    starts = [v for v in details if v["type"] == "process_start" and not v["infrastructure"]]
    guest_pids = [int(v["identity"]["namespace_pids"].split()[-1]) for v in starts]
    assert len(starts) == 5, starts  # one namespace-init/task plus four forks; no thread double count
    assert all(p in guest_pids for p in report["fork_pids"]), (guest_pids, report)
    assert any(v["type"] == "thread_start" for v in details)
    assert any(v["type"] == "credential_change" for v in details)
    assert any(v["type"] == "process_exec" and v["identity"]["executable"]["path"] == str(Path("/usr/bin/true").resolve()) for v in details)
    assert "misleading-argv-zero" not in (ROOT / "process.ndjson").read_text()
    fast = """import os
p=os.fork()
if p==0:
 c=os.fork()
 if c==0: os._exit(0)
 try: os.kill(c,9)
 except ProcessLookupError: pass
 os._exit(0)
os.waitpid(p,0)
"""
    fast_sink, fast_collect = pair("rapid-fork", fast, namespace)
    out, error = fast_collect.communicate(timeout=10)
    assert fast_collect.returncode == 0, (out, error)
    assert fast_sink.wait(timeout=3) == 0
    starts = [r for r in records("rapid-fork") if r["details"]["details"].get("type") == "process_start"
              and not r["details"]["details"]["infrastructure"]]
    assert len(starts) == 3  # namespace init, its child, and the immediately killed grandchild
    ok("fork-without-exec, rapid parent/child death, exec identity, credentials and thread exclusion audited")
    ok("task root cannot access tracer/audit FD; CLONE_UNTRACED denied and clone3 forced to traced fallback")

    sink, collect = pair("collector-loss", "import time;print('running',flush=True)\nwhile True: pass", namespace)
    assert collect.stdout.readline().strip() == "running"
    profile(sink, False)
    ok("host audit receiver is unprivileged, capability-free and descriptor/seccomp confined")
    before = records("collector-loss")
    live_pids = [int(r["details"]["details"]["identity"]["pid"]) for r in before
                 if r["details"]["details"].get("type") == "process_start"]
    start = time.monotonic();sink.kill();sink.wait(timeout=3)
    assert collect.wait(timeout=5) == 70
    elapsed = time.monotonic()-start
    assert elapsed < 5
    time.sleep(.1)
    for pid in live_pids:
        status = Path(f"/proc/{pid}/status")
        assert not status.exists() or "State:\tZ" in status.read_text(), pid
    ok(f"host ACK loss kills CPU-only task tree through EXITKILL in {elapsed:.3f}s (<5s)")
    sink, collect = pair("tracer-kill", "print('running',flush=True)\nwhile True: pass", namespace)
    assert collect.stdout.readline().strip() == "running"
    live_pids = [int(r["details"]["details"]["identity"]["pid"]) for r in records("tracer-kill")
                 if r["details"]["details"].get("type") == "process_start"]
    collect.kill();collect.wait(timeout=3);time.sleep(.1)
    for pid in live_pids:
        status = Path(f"/proc/{pid}/status")
        assert not status.exists() or "State:\tZ" in status.read_text(), pid
    assert sink.wait(timeout=3) == 70  # abrupt stream loss is never clean completion
    ok("SIGKILL of the tracer cannot leave an unobserved task tree running")


def main():
    assert os.geteuid() == 0, "run with sudo on Linux"
    netns()
    (ROOT / "summary.json").write_text(json.dumps(dict(passed=RESULTS, kernel=os.uname().release,
        architecture=os.uname().machine, host=os.uname().nodename,
        binary_sha256=hashlib.sha256(Path(BINARY).read_bytes()).hexdigest(),
        test_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest()), indent=2) + "\n")
    print("EVIDENCE", ROOT)


try:
    main()
finally:
    for p in reversed(PROCESSES):
        if p.poll() is None:
            p.kill()
        try:p.wait(timeout=3)
        except sp.TimeoutExpired:pass
        if p.stderr and not p.stderr.closed:
            error = p.stderr.read()
            if error: print("Child stderr:", error, file=sys.stderr)
    for ns in reversed(NAMESPACES):
        sp.run(["ip", "netns", "del", ns], check=False, capture_output=True)
    # Keep host-owned records and summary for evidence inspection.
    print("Artifacts:", ROOT, flush=True)
