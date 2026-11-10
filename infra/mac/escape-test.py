#!/usr/bin/env python3
"""Escape tests for the confined Mac VZ worker (architecture §3.2, E05).

Creates a real Linux sandbox through the running supervisor, then asks *that
sandbox's own confined worker* (over its private API socket) to try effects
it must not have: the owner's files and keychains, other sandboxes, the
network, host services, process execution. The guest is checked as well: it
must have no network device and reach nothing. Each probe records the OS
error so it shows which layer refused it ("Operation not permitted" is the
Seatbelt profile; "Permission denied" is the per-sandbox account).

Usage (root, supervisor running, LAMP_GRANT_ID set):
  sudo -n env LAMP_GRANT_ID=... infra/mac/escape-test.py TEMPLATE OWNER_HOME > escape.json
Exit status 0 only when every expectation holds.
"""
import json
import os
import socket
import subprocess
import sys
import time

SUP = "/usr/local/lib/lamp/lamp-supervisor"
STORE = "/private/var/lamp/store"
TEMPLATE, HOME = sys.argv[1], sys.argv[2]
sid = "sb_esc_%x" % (int(time.time() * 1000) & 0xFFFFFFFF)


def ctl(*args):
    r = subprocess.run([SUP, "ctl", *args], capture_output=True, text=True)
    return r.returncode, r.stdout, r.stderr


rc, out, err = ctl("create", TEMPLATE, sid)
if rc != 0:
    sys.exit("create failed: " + err)
created = json.loads(out)
sbdir = f"{STORE}/sb/{sid}"
others = [d for d in os.listdir(f"{STORE}/sb") if d != sid]
other = f"{STORE}/sb/{others[0]}" if others else f"{STORE}/sb"

deny = [
    {"kind": "list_dir", "path": HOME},
    {"kind": "list_dir", "path": f"{HOME}/Documents"},
    {"kind": "read_file", "path": f"{HOME}/.zshrc"},
    {"kind": "list_dir", "path": f"{HOME}/.ssh"},
    {"kind": "read_file", "path": f"{HOME}/Library/Keychains/login.keychain-db"},
    {"kind": "read_file", "path": "/Library/Keychains/System.keychain"},
    {"kind": "write_file", "path": f"{HOME}/lamp-escape-probe"},
    {"kind": "write_file", "path": "/private/tmp/lamp-escape-probe"},
    {"kind": "list_dir", "path": other},
    {"kind": "read_file", "path": f"{other}/disk.img"},
    {"kind": "read_file", "path": f"{STORE}/keys/producer.key"},
    {"kind": "list_dir", "path": f"{STORE}/grants"},
    {"kind": "connect", "host": "1.1.1.1", "port": 443},
    {"kind": "connect", "host": "127.0.0.1", "port": 22},
    {"kind": "mach_lookup", "name": "com.apple.pasteboard.1"},
    {"kind": "mach_lookup", "name": "com.apple.securityd.xpc"},
    {"kind": "mach_lookup", "name": "com.apple.windowserver.active"},
    {"kind": "mach_lookup", "name": "com.apple.coreservices.launchservicesd"},
    {"kind": "mach_lookup", "name": "com.apple.tccd.system"},
    {"kind": "exec", "path": "/bin/sh"},
    {"kind": "exec", "path": "/usr/bin/osascript"},
    {"kind": "keychain"},
]
allow = [
    {"kind": "write_file", "path": f"{sbdir}/probe-allowed"},
    {"kind": "read_file", "path": f"{sbdir}/spec.json"},
]
s = socket.socket(socket.AF_UNIX)
s.connect(f"{sbdir}/api.sock")
s.sendall((json.dumps({"op": "probe", "checks": deny + allow}) + "\n").encode())
buf = b""
while not buf.endswith(b"\n"):
    chunk = s.recv(65536)
    if not chunk:
        break
    buf += chunk
reply = json.loads(buf)
results = reply["detail"]
failures = []
for r in results[: len(deny)]:
    if r["allowed"]:
        failures.append(r)
for r in results[len(deny):]:
    if not r["allowed"]:
        failures.append(r)

# The guest side: no NIC beyond loopback, nothing reachable.
rc, guest_links, _ = ctl("exec", sid, "--", "sh", "-c", "ls /sys/class/net")
rc2, _, _ = ctl("exec", sid, "--", "sh", "-c", "timeout 3 bash -c 'exec 3<>/dev/tcp/1.1.1.1/443'")
guest = {"interfaces": guest_links.split(), "tcp_1.1.1.1_443_connected": rc2 == 0}
if guest["interfaces"] != ["lo"] or rc2 == 0:
    failures.append({"guest": guest})

# Processes behind this sandbox run as its own hidden account, not root/owner.
ident = created["detail"].get("worker_identity", "")
ps = subprocess.run(["ps", "-U", ident, "-o", "pid=,user=,comm="], capture_output=True, text=True).stdout
procs = [l.split(None, 2) for l in ps.strip().splitlines()]
if not any("Virtualization" in p[2] for p in procs) or not any("lamp-vz" in p[2] for p in procs):
    failures.append({"processes": procs})
ctl("release", sid)
print(json.dumps({"sandbox": sid, "identity": ident, "confined": created["detail"].get("confined"),
                  "probes": results, "guest": guest, "processes": procs,
                  "failures": failures, "pass": not failures}, indent=1))
sys.exit(1 if failures else 0)
