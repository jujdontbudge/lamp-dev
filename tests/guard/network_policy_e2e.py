#!/usr/bin/env python3
"""Real-kernel transport-policy regression; NOT full E09 qualification.

sudo env TMPDIR=/var/tmp python3 tests/guard/network_policy_e2e.py /path/lamp-guard
All interfaces, routes, sysctls and nft tables live in disposable namespaces.
Independent edge nft counters count *packets*, not only successful app requests.
No owner service, host firewall, host route or Docker state is changed.
"""
import hashlib
import json
import os
from pathlib import Path
import socket
import subprocess as sp
import sys
import tempfile
import uuid

BINARY = str(Path(sys.argv[1]).resolve())
ROOT = Path(tempfile.mkdtemp(prefix="lamp-t12-network-"))
PROCESSES = []
NAMESPACES = []
RESULTS = []
OBSERVATIONS = []
CLEANUP_ERRORS = []


def cmd(*argv, **kw):
    try:
        return sp.run([str(a) for a in argv], text=True, capture_output=True, check=True, **kw)
    except sp.CalledProcessError as error:
        raise RuntimeError(f"fixture command {argv} failed: {error.stderr}") from error


def launch(*argv, **kw):
    p = sp.Popen([str(a) for a in argv], text=True, stdout=sp.PIPE,
                 stderr=open(ROOT / f"child-{len(PROCESSES)}.stderr", "w"), **kw)
    PROCESSES.append(p)
    return p


def passed(name):
    RESULTS.append(name)
    print("PASS", name, flush=True)


def rule(cidr, protocol="tcp", port=18080):
    return dict(cidr=cidr, protocol=protocol, ports=[port])


def layer(default="public", allow=(), deny=()):
    return dict(filter_mode="transport", default_egress=default, allow=list(allow), deny=list(deny))


def write_config(name, **extra):
    data = dict(attribution=dict(principal_id="fixture-owner", host_id="fixture-host", task_id=name,
                sandbox_id=name, generation=1, session_id="fixture", operation_id="fixture", policy_revision=1),
                interface="task0", guest_ip="198.18.240.2", queue=314, guard_uid=1000001,
                host_denies=["198.18.240.1/32", "198.18.241.0/30", "9.9.9.0/24"],
                safety_denies=["10.0.0.8/32"], require_allowlist=True,
                policy_ceiling=[layer("deny", [rule("8.8.0.0/16", p) for p in ("tcp", "udp")] +
                    [rule("10.0.0.7/32"), rule("10.0.0.8/32"), rule("9.9.9.9/32")])],
                policy_layers=[layer(deny=[rule("8.8.4.4/32", p) for p in ("tcp", "udp")])],
                exceptions=[dict(address=ip, transport="tcp", port=18080) for ip in ("10.0.0.7", "10.0.0.8")],
                spool=str(ROOT / (name + ".ndjson")), spool_bytes=16 * 1024 * 1024)
    data.update(extra)
    path = ROOT / (name + ".json")
    path.write_text(json.dumps(data))
    return path


def setup():
    prefix = "ln12-" + uuid.uuid4().hex[:7]
    guest, guard, edge = [prefix + s for s in ("g", "r", "e")]
    for ns in (guest, guard, edge):
        cmd("ip", "netns", "add", ns)
        NAMESPACES.append(ns)
        cmd("ip", "-n", ns, "link", "set", "lo", "up")
    cmd("ip", "-n", guest, "link", "add", "eth0", "type", "veth", "peer", "name", "task0", "netns", guard)
    cmd("ip", "-n", guard, "link", "add", "uplink0", "type", "veth", "peer", "name", "eth0", "netns", edge)
    for ns, dev, address in ((guest, "eth0", "198.18.240.2/30"), (guard, "task0", "198.18.240.1/30"),
                             (guard, "uplink0", "198.18.241.1/30"), (edge, "eth0", "198.18.241.2/30")):
        cmd("ip", "-n", ns, "addr", "add", address, "dev", dev)
        cmd("ip", "-n", ns, "link", "set", dev, "up")
    cmd("ip", "-n", guest, "route", "add", "default", "via", "198.18.240.1")
    cmd("ip", "-n", guard, "route", "add", "default", "via", "198.18.241.2")
    cmd("ip", "-n", edge, "route", "add", "198.18.240.0/30", "via", "198.18.241.1")
    cmd("ip", "netns", "exec", guard, "sysctl", "-qw", "net.ipv4.ip_forward=1")
    addresses = ["8.8.8.8", "8.8.4.4", "1.1.1.1", "9.9.9.9", "10.0.0.7", "10.0.0.8",
                 "169.254.169.254", "168.63.129.16", "100.64.0.7"]
    for ip in addresses:
        cmd("ip", "-n", edge, "addr", "add", ip + "/32", "dev", "lo")
    for ns, dev, address in ((guest, "eth0", "fd00:12::2/64"), (guard, "task0", "fd00:12::1/64"),
                            (guard, "uplink0", "fd00:13::1/64"), (edge, "eth0", "fd00:13::2/64")):
        cmd("ip", "-n", ns, "-6", "addr", "add", address, "dev", dev, "nodad")
    cmd("ip", "-n", guest, "-6", "route", "add", "default", "via", "fd00:12::1")
    cmd("ip", "-n", guard, "-6", "route", "add", "default", "via", "fd00:13::2")
    cmd("ip", "netns", "exec", guard, "sysctl", "-qw", "net.ipv6.conf.all.forwarding=1")
    # Ensure IPv6 negatives actually enter the guard, instead of merely failing
    # neighbour discovery (the demo intentionally denies ICMPv6 too).
    mac = json.loads(cmd("ip", "-j", "-n", guard, "link", "show", "task0").stdout)[0]["address"]
    cmd("ip", "-n", guest, "-6", "neigh", "replace", "fd00:12::1", "lladdr", mac, "nud", "permanent", "dev", "eth0")
    v6 = "2606:4700:4700::1111"
    cmd("ip", "-n", edge, "-6", "addr", "add", v6 + "/128", "dev", "lo", "nodad")
    # Independent sink counters remain active across guard death and revisions.
    rules = []
    for address in addresses + [v6]:
        for protocol in ("tcp", "udp"):
            for port in (18080, 18081, 18082):
                key = f"{address}-{protocol}-{port}"
                family = "ip6" if ":" in address else "ip"
                rules.append(f'{family} daddr {address} {protocol} dport {port} counter comment "{key}";')
    cmd("ip", "netns", "exec", edge, "nft", "-f", "-", input=(
        "table inet witness { chain ingress { type filter hook prerouting priority -300; policy accept;\n" +
        "\n".join(rules) + "\n}\n}\n"))
    server = r'''
import socket,threading
s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);s.bind(('0.0.0.0',18080));s.listen()
def stream(listener):
 while True:
  c,_=listener.accept()
  def echo(c):
   try:
    while True:
     b=c.recv(4096)
     if not b:break
     c.sendall(b)
   except OSError:pass
   finally:c.close()
  threading.Thread(target=echo,args=(c,),daemon=True).start()
threading.Thread(target=stream,args=(s,),daemon=True).start()
def http(listener):
 while True:
  c,_=listener.accept()
  try:
   c.settimeout(1);c.recv(16384)
   c.sendall(b'HTTP/1.1 200 OK\r\nContent-Length: 7\r\nConnection: close\r\n\r\nfixture')
  except OSError:pass
  finally:c.close()
h=socket.socket();h.bind(('0.0.0.0',18082));h.listen()
threading.Thread(target=http,args=(h,),daemon=True).start()
def datagram(s):
 while True:
  b,a=s.recvfrom(4096);s.sendto(b,a)
for address in __import__('json').loads(__import__('sys').argv[1]):
 s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM);s.bind((address,18080))
 threading.Thread(target=datagram,args=(s,),daemon=True).start()
print('ready',flush=True);threading.Event().wait()
'''
    reflector = launch("ip", "netns", "exec", edge, "python3", "-u", "-c", server, json.dumps(addresses))
    assert reflector.stdout.readline().strip() == "ready"
    return guest, guard, edge, v6


def snapshot(edge, label):
    data = json.loads(cmd("ip", "netns", "exec", edge, "nft", "-j", "list", "table", "inet", "witness").stdout)
    values = {}
    for item in data["nftables"]:
        if "rule" in item:
            r = item["rule"]
            values[r["comment"]] = next(e["counter"]["packets"] for e in r["expr"] if "counter" in e)
    OBSERVATIONS.append(dict(phase=label, packets=values))
    (ROOT / "independent-packets.json").write_text(json.dumps(OBSERVATIONS, indent=2) + "\n")
    return values


CLIENT = r'''
import socket,sys
protocol,address,port,source,live=sys.argv[1:]
s=socket.socket(socket.AF_INET6 if ':' in address else socket.AF_INET,socket.SOCK_STREAM if protocol=='tcp' else socket.SOCK_DGRAM)
s.settimeout(.25)
if source!='-':s.bind((source,0))
try:s.connect((address,int(port)))
except OSError:print('denied',flush=True);sys.exit(0)
def probe():
 try:s.sendall(b'fixture');return 'allowed' if s.recv(16)==b'fixture' else 'denied'
 except OSError:return 'denied'
print(probe(),flush=True)
if live=='yes':
 for _ in sys.stdin:print(probe(),flush=True)
'''


def probe(guest, protocol, address, allowed, port=18080, source="-"):
    result = cmd("ip", "netns", "exec", guest, "python3", "-c", CLIENT,
                 protocol, address, port, source, "no").stdout.strip()
    assert result == ("allowed" if allowed else "denied"), (protocol, address, port, result)


def active(guest, protocol):
    p = launch("ip", "netns", "exec", guest, "python3", "-u", "-c", CLIENT,
               protocol, "8.8.8.8", 18080, "-", "yes", stdin=sp.PIPE)
    assert p.stdout.readline().strip() == "allowed"
    return p


def repeat(p, allowed):
    p.stdin.write("probe\n")
    p.stdin.flush()
    assert p.stdout.readline().strip() == ("allowed" if allowed else "denied")


def main():
    assert os.geteuid() == 0 and sys.platform == "linux", "requires Linux root"
    guest, guard, edge, v6 = setup()
    host, child = socket.socketpair(socket.AF_UNIX, socket.SOCK_SEQPACKET)
    host.settimeout(3)
    cfg = write_config("network")
    daemon = launch("ip", "netns", "exec", guard, BINARY, "run", cfg,
                    f"control:{child.fileno()}", pass_fds=(child.fileno(),))
    child.close()
    assert json.loads(daemon.stdout.readline())["state"] == "ready"
    status = dict(line.split(":", 1) for line in Path(f"/proc/{daemon.pid}/status").read_text().splitlines() if ":" in line)
    assert status["Uid"].split() == ["1000001"] * 4
    assert int(status["CapEff"].strip(), 16) == 1 << 12 and status["Seccomp"].strip() == "2"
    assert len(list(Path(f"/proc/{daemon.pid}/fd").iterdir())) == 6
    passed("revision channel retains subordinate UID, seccomp and assigned-descriptor-only profile")
    for protocol in ("tcp", "udp"):
        probe(guest, protocol, "8.8.8.8", True)
    probe(guest, "tcp", "10.0.0.7", True)
    passed("allowlisted public TCP/UDP and an exact private TCP service work")
    before = snapshot(edge, "before-denials")
    denied = ("8.8.4.4", "1.1.1.1", "9.9.9.9", "10.0.0.8", "169.254.169.254",
              "168.63.129.16", "100.64.0.7", v6)
    for protocol in ("tcp", "udp"):
        for address in denied:
            probe(guest, protocol, address, False)
        probe(guest, protocol, "8.8.8.8", False, port=18081)
    probe(guest, "udp", "10.0.0.7", False)
    after = snapshot(edge, "after-denials")
    denied_keys = [f"{a}-{p}-18080" for a in denied for p in ("tcp", "udp")]
    denied_keys += [f"8.8.8.8-{p}-18081" for p in ("tcp", "udp")] + ["10.0.0.7-udp-18080"]
    assert all(before[k] == after[k] == 0 for k in denied_keys)
    passed("independent sinks see zero denied IPv4/IPv6, LAN, metadata, neighbour, wrong-port or wrong-transport packets")
    cmd("ip", "netns", "exec", guest, "nft", "flush", "ruleset")
    cmd("ip", "-n", guest, "addr", "add", "8.8.4.5/32", "dev", "eth0")
    probe(guest, "udp", "8.8.8.8", False, source="8.8.4.5")
    probe(guest, "udp", "169.254.169.254", False)
    root_after = snapshot(edge, "after-guest-root-changes")
    assert all(after[k] == root_after[k] for k in ("8.8.8.8-udp-18080", "169.254.169.254-udp-18080"))
    passed("guest root firewall changes and source spoofing do not widen host policy")
    live = [active(guest, p) for p in ("tcp", "udp")]

    def update(expected, revision, layers):
        host.send(json.dumps(dict(expected_revision=expected, revision=revision, policy_layers=layers)).encode())
        try:
            reply = json.loads(host.recv(4096))
        except (OSError, ValueError):
            diagnostic = dict(guard_exit=daemon.poll())
            for name in ("wchan", "stack", "syscall"):
                try:
                    diagnostic[name] = Path(f"/proc/{daemon.pid}/{name}").read_text()
                except OSError:
                    pass
            (ROOT / "revision-channel-failure.json").write_text(json.dumps(diagnostic, indent=2))
            raise
        OBSERVATIONS.append(dict(phase="revision-reply", value=reply))
        return reply

    reply = update(1, 2, [dict(filter_mode="strict_sites", default_egress="deny")])
    assert reply["state"] == "rejected" and reply["effective_policy_revision"] == 1
    for p in live:
        repeat(p, True)
    passed("unavailable strict-site mediation is rejected without mutating the active revision")
    reply = update(1, 2, [layer("deny")])
    assert reply["state"] == "applied" and reply["effective_policy_revision"] == 2
    before = snapshot(edge, "after-narrowing-ack")
    for p in live:
        repeat(p, False)
    for protocol in ("tcp", "udp"):
        probe(guest, protocol, "8.8.8.8", False)
    probe(guest, "tcp", "10.0.0.7", False)
    assert before == snapshot(edge, "after-narrowed-probes")
    assert update(1, 2, [layer()])["error"] == "REVISION_CONFLICT"
    assert update(2, 3, []) == dict(state="applied", effective_policy_revision=3, error=None)
    probe(guest, "tcp", "8.8.8.8", True)
    before = snapshot(edge, "before-parent-ceiling-probe")
    probe(guest, "tcp", "1.1.1.1", False)
    after = snapshot(edge, "after-parent-ceiling-probe")
    assert before["1.1.1.1-tcp-18080"] == after["1.1.1.1-tcp-18080"] == 0
    passed("atomic CAS narrowing fences live TCP/UDP and private grants; widening cannot remove immutable parent ceiling")
    death_live = [active(guest, p) for p in ("tcp", "udp")]
    host.close()
    assert daemon.wait(timeout=3) == 70
    before = snapshot(edge, "after-supervisor-channel-death")
    for p in death_live:
        repeat(p, False)
    probe(guest, "tcp", "8.8.8.8", False)
    assert before == snapshot(edge, "after-channel-death-probes")
    passed("supervisor channel death drops both established and new traffic with kernel queue rules intact")
    rows = [json.loads(line) for line in (ROOT / "network.ndjson").read_text().splitlines()]
    assert any(r["details"].get("reason") == "policy_revision_fence" for r in rows)
    assert any(r["details"].get("ip_version") == 6 and r["details"].get("decision") == "deny" for r in rows)
    assert any(r["details"].get("phase") == "start" and r["attribution"]["policy_revision"] == 3 for r in rows)
    assert [r["sequence"] for r in rows] == list(range(1, len(rows) + 1))
    digests = [r["details"]["policy_sha256"] for r in rows if "policy_sha256" in r["details"]]
    assert len(digests) == 3 and len(set(digests)) == 3
    assert all(len(d) == 64 and set(d) <= set("0123456789abcdef") for d in digests)
    passed("durable flow fences and new admissions carry correct revisions, policy digests and consecutive audit sequences")
    # Fail before installation: a partial hot update must never leave a stale
    # mediated proxy upstream after the packet guard acknowledges a new revision.
    invalid = write_config("proxy-hot-update", proxy=dict(address="198.18.240.1", port=3128))
    host, child = socket.socketpair(socket.AF_UNIX, socket.SOCK_SEQPACKET)
    partial = launch("ip", "netns", "exec", guard, BINARY, "run", invalid,
                     f"control:{child.fileno()}", pass_fds=(child.fileno(),))
    child.close()
    assert partial.wait(timeout=3) == 70 and not partial.stdout.read()
    host.close()
    passed("proxy hot-update admission refuses an uncoordinated partial policy swap")
    proxy_policy()


def proxy_policy():
    guest, guard, edge, _ = setup()
    cfg = write_config("proxy", proxy=dict(address="198.18.240.1", port=3128),
        policy_ceiling=[layer("deny", [rule("8.8.0.0/16", port=18082), rule("10.0.0.7/32", port=18082)])],
        policy_layers=[layer(deny=[rule("8.8.4.4/32", port=18082), rule("10.0.0.7/32", port=18082)])],
        exceptions=[dict(address="10.0.0.7", transport="tcp", port=18082)])
    daemon = launch("ip", "netns", "exec", guard, BINARY, "run", cfg)
    assert json.loads(daemon.stdout.readline())["state"] == "ready"
    proxy = launch("ip", "netns", "exec", guard, BINARY, "proxy", cfg, ROOT / "proxy-requests.ndjson")
    assert json.loads(proxy.stdout.readline())["state"] == "ready"
    client = r'''
import socket,sys
s=socket.create_connection(('198.18.240.1',3128),timeout=2)
s.sendall(('GET http://'+sys.argv[1]+':18082/ HTTP/1.1\r\nHost: ignored\r\nConnection: close\r\n\r\n').encode())
data=b''
while True:
 b=s.recv(4096)
 if not b:break
 data+=b
print(data.decode())
'''
    result = cmd("ip", "netns", "exec", guest, "python3", "-c", client, "8.8.8.8").stdout
    assert result.startswith("HTTP/1.1 200") and result.strip().endswith("fixture")
    before = snapshot(edge, "proxy-before-denials")
    denied = ("8.8.4.4", "10.0.0.7", "169.254.169.254", "1.1.1.1", "10.0.0.8")
    for address in denied:
        result = cmd("ip", "netns", "exec", guest, "python3", "-c", client, address).stdout
        assert result.startswith("HTTP/1.1 403"), (address, result)
    after = snapshot(edge, "proxy-after-denials")
    assert all(before[f"{a}-tcp-18082"] == after[f"{a}-tcp-18082"] == 0 for a in denied)
    passed("real numeric HTTP proxy enforces the same parent, explicit-deny, exception and safety decisions; denied origins see zero packets")


if __name__ == "__main__":
    failure = None
    try:
        main()
    except BaseException as error:
        failure = type(error).__name__ + ": " + str(error)
        raise
    finally:
        if failure is not None:
            diagnostics = []
            for p in PROCESSES:
                if BINARY not in p.args:
                    continue
                state = dict(pid=p.pid, exit=p.poll())
                for name in ("wchan", "stack", "syscall"):
                    try:
                        state[name] = Path(f"/proc/{p.pid}/{name}").read_text()
                    except OSError:
                        pass
                diagnostics.append(state)
            (ROOT / "failed-helper-state.json").write_text(json.dumps(diagnostics, indent=2))
        for p in reversed(PROCESSES):
            if p.poll() is None:
                p.kill()
            try:
                p.wait(timeout=3)
            except sp.TimeoutExpired:
                CLEANUP_ERRORS.append(f"process {p.pid} did not terminate")
        for ns in reversed(NAMESPACES):
            result = sp.run(["ip", "netns", "del", ns], text=True, capture_output=True)
            if result.returncode:
                CLEANUP_ERRORS.append(f"namespace {ns}: {result.stderr}")
        summary = dict(qualification="isolated-linux-transport-policy-regression-not-E09", passed=RESULTS,
                       failure=failure, cleanup_errors=CLEANUP_ERRORS, observations=OBSERVATIONS,
                       kernel=os.uname().release, architecture=os.uname().machine,
                       binary_sha256=hashlib.sha256(Path(BINARY).read_bytes()).hexdigest(),
                       test_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest())
        (ROOT / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")
        print("EVIDENCE", ROOT, flush=True)
        if CLEANUP_ERRORS and failure is None:
            raise RuntimeError("fixture cleanup failed; inspect summary.json")
