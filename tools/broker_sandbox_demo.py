#!/usr/bin/env python3
"""One LX sandbox using the disposable authenticated broker fixtures.

Run on LX with an isolated, already prepared engine store. No benchmark, account
setup, owner service management or Docker operations. The fixture vendor is not
OpenRouter; evidence must retain that distinction.
"""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import time

from broker_demo import request


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--root", required=True)
    p.add_argument("--binary-dir", required=True)
    p.add_argument("--source-root", required=True)
    p.add_argument("--store", required=True)
    p.add_argument("--network-policy", required=True)
    p.add_argument("--host-ip", required=True)
    p.add_argument("--template", required=True)
    args = p.parse_args()
    sys.path.insert(0, args.source_root)
    from benchmarks.public import PublicLamp
    root = Path(args.root).resolve()
    root.mkdir(mode=0o700, exist_ok=False)
    private = root / "private"
    private.mkdir(mode=0o700)
    binary_dir = Path(args.binary_dir).resolve()
    worker_binary = str(Path(args.store) / "bin/lamp-supervisor")
    worker_socket = str(private / "worker.sock")
    worker_pidfile = private / "worker.pid"
    transcript = (root / "transcript.jsonl").open("x")
    def record(step, value):
        transcript.write(json.dumps(dict(step=step, value=value), sort_keys=True) + "\n")
        transcript.flush()
    def out(argv):
        return subprocess.check_output(argv, text=True).strip()
    def wait_path(path, process):
        deadline = time.monotonic() + 30
        while not Path(path).exists():
            if process.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("owned service failed to become ready")
            time.sleep(.02)
    fixture_log = (root / "fixture.log").open("x")
    fixture_root = root / "fixture"
    fixture = subprocess.Popen([sys.executable, str(Path(__file__).with_name("broker_demo.py")), "--serve", "--binary", str(binary_dir / "lamp-broker-t17-linux"), "--root", str(fixture_root), "--task-host", args.host_ip], stdin=subprocess.PIPE, stdout=fixture_log, stderr=fixture_log)
    worker_log = (root / "worker.log").open("x")
    worker = None
    daemon = None
    client = None
    sandbox = None
    grant = None
    failures = []
    try:
        wait_path(fixture_root / "private/manifest.json", fixture)
        manifest = json.loads((fixture_root / "private/manifest.json").read_text())
        owner = manifest["owner_socket"]
        def admin(path, value):
            status, reply = request("http://localhost" + path, value, unix=owner)
            if status != 200:
                raise RuntimeError("broker owner operation failed")
            return reply
        worker = subprocess.Popen(["sudo", "-n", "sh", "-c", 'echo "$$" > "$1"; shift; exec "$@"', "sh", str(worker_pidfile), worker_binary, "serve", "--store", args.store, "--socket", worker_socket, "--network-policy", args.network_policy, "--mem-dir", "/run/lamp-t17/mem", "--netns-dir", "/run/lamp-t17/netns", "--uid-base", "1800000000", "--host-id", "hs_t17", "--allow-uid", str(os.getuid())], stdout=worker_log, stderr=worker_log)
        wait_path(worker_socket, worker)
        worker_pid = int(worker_pidfile.read_text())
        grant = json.loads(out(["sudo", "-n", worker_binary, "grant-install", "--store", args.store, "--caller-uid", str(os.getuid()), "--ttl-s", "600"]))
        bearer = secrets.token_hex(32)
        identity = private / "identity.json"
        identity.write_text(json.dumps(dict(owner_id="pr_t17_demo", bootstrap_sha256=hashlib.sha256(bearer.encode()).hexdigest())))
        identity.chmod(0o600)
        api_socket = str(private / "api.sock")
        daemon_log = (root / "lampd.log").open("x")
        daemon = subprocess.Popen([str(binary_dir / "lampd-t17-linux"), "--identity-file", str(identity), "--state-dir", str(private / "control"), "--socket", api_socket, "--worker-socket", worker_socket, "--worker-id", "lamp-supervisor@hs_t17", "--worker-boot-id", Path("/proc/sys/kernel/random/boot_id").read_text().strip(), "--worker-uid", "0", "--worker-pid", str(worker_pid), "--grant-id", grant["grant_id"], "--worker-timeout", "60s"], stdout=daemon_log, stderr=daemon_log)
        wait_path(api_socket, daemon)
        client = PublicLamp(socket_path=api_socket, token=bearer, timeout=120)
        record("artifacts", {"sha256": {str(path): hashlib.sha256(path.read_bytes()).hexdigest() for path in [binary_dir / "lamp-broker-t17-linux", binary_dir / "lampd-t17-linux", Path(worker_binary), Path(args.store) / "bin/lamp-guard"]}, "host": out(["hostname"]), "vendor_boundary": "authenticated model and MCP fixtures, not external provider certification"})
        # This is immediately before the only sandbox allocation. No service is
        # stopped to pass this check; other tickets' sandboxes count too.
        count = subprocess.run(["pgrep", "-c", "firecracker"], capture_output=True, text=True)
        live = int(count.stdout.strip() or "0")
        free = out(["free", "-m"])
        available = int(next(line for line in free.splitlines() if line.startswith("Mem:")).split()[-1])
        record("allocation admission", {"firecracker_count": live, "available_mib": available, "free_m": free})
        if live >= 2 or available < 12 * 1024:
            raise RuntimeError("owner sandbox admission condition not met")
        created = client.call("sandbox", "create", dict(template_id=args.template, idempotency_key="t17-single-demo", wait_ms=60000))
        sandbox = created["result"]["sandbox_id"]
        record("real sandbox", {"id": sandbox, "state": client.get(sandbox)["state"], "template": args.template})
        scoped = admin("/owner/grants", {"sandbox_id": sandbox, "generation": 1, "peer_ip": args.host_ip, "ttl_seconds": 300, "connections": {"model": ["chat"], "mcp": ["read_notes"], "notes": ["read", "append"]}, "settings": ["owner"]})
        public_ca = Path(manifest["ca"]).read_text()
        # Only the public TLS certificate and a short-lived Lamp transport grant
        # enter task memory. No provider credential or signing key enters argv.
        common = 'import json,ssl,urllib.request,urllib.error,subprocess,sys,pathlib\n'
        common += "base=" + repr(manifest["url"]) + "; token=" + repr(scoped["token"]) + "; ca=" + repr(public_ca) + "\n"
        common += '''
def call(path, body):
 req=urllib.request.Request(base+path, data=json.dumps(body).encode(), headers={"Content-Type":"application/json","Authorization":"Bearer "+token})
 try:
  opener=urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl.create_default_context(cadata=ca)))
  with opener.open(req, timeout=45) as r: return r.status,json.load(r)
 except urllib.error.HTTPError as e: return e.code,json.load(e)
'''
        counter = 0
        def guest(program, label):
            nonlocal counter
            counter += 1
            # Public control input/journal remain private because they include a
            # scoped Lamp grant, not a provider key. Publish only safe results.
            reply = client.call("exec", "create", dict(sandbox_id=sandbox, argv=["python3", "-c", common + program], idempotency_key="t17-step-" + str(counter), wait_ms=60000))
            exec_id = reply["result"]["exec_id"]
            cursor = None
            data = bytearray()
            deadline = time.monotonic() + 65
            while True:
                page = client.call("exec", "watch", dict(exec_id=exec_id, wait_ms=1000, **({"cursor": cursor} if cursor else {})))
                for frame in page["items"]:
                    if frame["channel"] == "stdout":
                        data.extend(base64.b64decode(frame["data_base64"]))
                cursor = page.get("next_cursor")
                ex = client.call("exec", "get", dict(exec_id=exec_id))["data"]
                if ex["state"] == "exited" and not cursor:
                    if ex["exit_code"] != 0:
                        raise RuntimeError("guest step failed: " + label)
                    value = json.loads(data)
                    record(label, value)
                    return value
                if ex["state"] in ("failed", "outcome_unknown") or time.monotonic() > deadline:
                    raise RuntimeError("guest step did not complete: " + label)
        useful = guest('''
status,reply=call("/v1/models/model/chat/completions", {"model":"fixture-code","messages":[{"role":"user","content":"Sum 1 through 10 using Python"}],"max_tokens":128})
assert status==200
code=reply["choices"][0]["message"]["content"]
result=subprocess.check_output([sys.executable,"-c",code],text=True).strip()
assert result=="55"
pathlib.Path("/tmp/t17-result").write_text(result)
print(json.dumps({"status":status,"useful_code_output":result}))
''', "guest harness through model fixture")
        assert useful["useful_code_output"] == "55"
        guest('''
status,reply=call("/v1/invoke", {"connection_id":"mcp","operation":"read_notes","arguments":{}})
assert status==200
print(json.dumps(reply))
''', "guest authenticated MCP gateway")
        proposed = guest('''
status,copied=call("/v1/settings/owner/copy",{})
assert status==200
root=pathlib.Path("/tmp/t17-settings");root.mkdir()
for name,text in copied["files"].items(): (root/name).write_text(text)
(root/"AGENTS.md").write_text("Guest selected instructions\\n")
(root/"harness.json").write_text('{"theme":"guest edit"}\\n')
status,proposed=call("/v1/settings/owner/propose", {"base_revision":copied["base_revision"],"files":{name:(root/name).read_text() for name in copied["files"]}})
assert status==200
print(json.dumps(proposed))
''', "guest settings proposed")
        settings = Path(manifest["settings_root"])
        assert (settings / "AGENTS.md").read_text() == "Original instructions\n"
        admin("/owner/settings/" + proposed["settings_change_id"], {"base_revision": proposed["base_revision"], "selected_paths": ["AGENTS.md"]})
        assert (settings / "AGENTS.md").read_text() == "Guest selected instructions\n"
        assert (settings / "harness.json").read_text() == '{"theme":"dark"}\n'
        record("owner selected-path apply", {"selected_applied": True, "unselected_unchanged": True, "source_unchanged_before_approval": True})
        admin("/owner/access/notes", {"access": "full"})
        guest('''
status,reply=call("/v1/invoke", {"connection_id":"notes","operation":"append","arguments":{"body":"from real sandbox"}})
assert status==200
print(json.dumps(reply))
''', "guest real service effect")
        # One real full memory/disk capture, without allocating a second guest.
        # The disposable provider canary is read only on the trusted host and
        # goes to the independent scanner over stdin, never argv or task memory.
        import socket
        with socket.socket(socket.AF_UNIX) as s:
            s.connect(str(fixture_root / "private/credential.sock"))
            canary = s.recv(16384)
        capture_id = "cp_t17_" + sandbox[3:]
        env = dict(os.environ, LAMP_GRANT_ID=grant["grant_id"])
        captured = json.loads(subprocess.check_output([worker_binary, "ctl", "--socket", worker_socket, "capture", sandbox, capture_id], env=env, text=True))
        assert captured["state"] == "captured"
        record("full memory and disk capture", captured)
        scanner = r'''
import errno,json,os,pathlib,sys
needle=sys.stdin.buffer.read()
assert len(needle)>8
paths=[]
for arg in sys.argv[1:]:
 p=pathlib.Path(arg)
 paths.extend(sorted(f for f in p.rglob("*") if f.is_file()) if p.is_dir() else [p])
scanned=logical=hits=0
for path in paths:
 with path.open("rb",buffering=0) as f:
  size=os.fstat(f.fileno()).st_size;logical+=size;pos=0
  while pos<size:
   try:
    start=os.lseek(f.fileno(),pos,os.SEEK_DATA)
    end=os.lseek(f.fileno(),start,os.SEEK_HOLE)
   except OSError as e:
    if e.errno==errno.ENXIO: break
    start,end=pos,size
   f.seek(start);tail=b""
   while f.tell()<end:
    data=f.read(min(1048576,end-f.tell()))
    if not data: break
    hits+=(tail+data).count(needle);scanned+=len(data)
    tail=(tail+data)[-(len(needle)-1):]
   pos=end
print(json.dumps({"files":len(paths),"logical_bytes":logical,"allocated_bytes_scanned":scanned,"raw_provider_canary_occurrences":hits}))
assert hits==0
'''
        scan = subprocess.run(["sudo", "-n", "python3", "-c", scanner, str(Path(args.store) / "captures" / capture_id), "/run/lamp-t17/mem/captures/" + capture_id + ".mem", "/run/lamp-t17/mem/captures/" + capture_id + ".vmstate"], input=canary, capture_output=True, check=True)
        record("independent guest RAM disk capture scan", json.loads(scan.stdout))
        # Release immediately after useful work and its capture observation.
        client.delete(sandbox)
        assert client.get(sandbox)["state"] == "deleted"
        record("sandbox released", {"state": "deleted"})
        sandbox = None
        admin("/owner/revoke", {"grant_id": scoped["grant_id"]})
        scan = [root / "transcript.jsonl", fixture_root / "audit.jsonl", fixture_root / "broker.log", root / "worker.log", root / "lampd.log"]
        assert all(canary not in f.read_bytes() for f in scan)
        record("scan", {"raw_provider_canary_occurrences_in_published_output_and_logs": 0, "guest_ram_disk_capture_scan": "one full snapshot scanned independently"})
    finally:
        if sandbox and client:
            try:
                client.delete(sandbox)
                if client.get(sandbox)["state"] != "deleted":
                    raise RuntimeError("sandbox release not confirmed")
            except Exception as e:
                failures.append(str(e))
        if daemon:
            daemon.terminate()
            daemon.wait(timeout=70)
            daemon_log.close()
        if grant:
            subprocess.run(["sudo", "-n", worker_binary, "grant-revoke", "--store", args.store, grant["grant_id"]], check=True, stdout=subprocess.DEVNULL)
        if worker and worker.poll() is None:
            subprocess.run(["sudo", "-n", "kill", "-TERM", str(worker.pid)], check=True)
            worker.wait(timeout=70)
        if fixture.poll() is None:
            fixture.communicate(input=b"", timeout=60)
        fixture_log.close()
        worker_log.close()
        (root / "cleanup.json").write_text(json.dumps(failures))
        transcript.close()
        if failures:
            raise RuntimeError("cleanup incomplete")
    print(root / "transcript.jsonl")


if __name__ == "__main__":
    main()
