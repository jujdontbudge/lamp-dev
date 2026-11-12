import argparse
import base64
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import time


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--binaries", type=Path, required=True)
    parser.add_argument("--public-bundle", type=Path, required=True)
    parser.add_argument("--m5-root", required=True)
    parser.add_argument("--m5-source", required=True)
    parser.add_argument("--m5-python", required=True)
    parser.add_argument("--m5-claude", required=True)
    parser.add_argument("--m5-codex", required=True)
    parser.add_argument("--m5-node-dir", required=True)
    args = parser.parse_args()
    if os.geteuid() != 0:
        raise ValueError("run only in the ticket-owned Linux root runtime")
    os.umask(0o077)
    root = args.root
    manifest = json.loads((root / "manifest.json").read_text())
    for field in ["control_pid", "worker_pid"]:
        pid = manifest[field]
        expected = root / "bin" / ("lampd" if field == "control_pid" else "lamp-supervisor")
        if Path("/proc", str(pid), "exe").resolve() != expected:
            raise ValueError("refusing to stop a process outside this disposable ticket runtime")
    for field in ["control_pid", "worker_pid"]:
        pid = manifest[field]
        os.kill(pid, signal.SIGTERM)
        deadline = time.monotonic() + 15
        while Path("/proc", str(pid)).exists():
            stat = Path("/proc", str(pid), "stat")
            if stat.exists() and stat.read_text().split()[2] == "Z":
                break
            if time.monotonic() > deadline:
                raise RuntimeError("ticket process did not stop")
            time.sleep(.05)
    binaries = root / "bin"
    for source, target in [(args.source / "target/debug/lamp-supervisor", "lamp-supervisor-next"), (args.source / "target/debug/lamp-guard", "lamp-guard-next"), (args.binaries / "lampd-next-linux", "lampd-next"), (args.binaries / "lamp-broker-next-linux", "lamp-broker")]:
        shutil.copy2(source, binaries / target)
    subprocess.run(["ip", "link", "add", "lamp-t21br", "type", "dummy"], check=True)
    subprocess.run(["ip", "addr", "add", "10.219.21.1/32", "dev", "lamp-t21br"], check=True)
    subprocess.run(["ip", "link", "set", "lamp-t21br", "up"], check=True)
    private = root / "model"
    private.mkdir(mode=0o700)
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", str(private / "tls.key"), "-out", str(private / "ca.pem"), "-days", "2", "-subj", "/CN=lamp-t21-disposable", "-addext", "basicConstraints=critical,CA:TRUE", "-addext", "subjectAltName=IP:10.219.21.1,DNS:localhost,IP:127.0.0.1"], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    processes = []

    def start(argv, name):
        with (root / (name + ".log")).open("xb") as output:
            process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=output, stderr=output, start_new_session=True)
        processes.append(process)
        return process

    common = list(manifest["common"])
    supervisor = str(binaries / "lamp-supervisor-next")
    grant = json.loads(subprocess.check_output([supervisor, "grant-install", "--store", str(root / "store"), "--caller-uid", "0", "--ttl-s", "3600"]))
    policy = json.loads((root / "network.json").read_text())
    policy["guard"] = str(binaries / "lamp-guard-next")
    policy["revision"] += 1
    policy["exceptions"] = [{"address": "10.219.21.1", "transport": "tcp", "port": 17779}]
    network = root / "network-live.json"
    network.write_text(json.dumps(policy))
    worker_socket = root / "r/worker-live.sock"
    worker = start([supervisor, "serve", *common, "--socket", str(worker_socket), "--network-policy", str(network)], "worker-live")
    bundle = "bundle:" + base64.b64encode(args.public_bundle.read_bytes()).decode()
    native_hosts = {"hs_m5": {"owner_id": "pr_local", "command": [args.m5_python], "root": args.m5_root + "/i", "socket": "/home/owner/lamp-t21-bridge/runner.sock"}}
    harnesses = {"hr_guest_shell": {"owner_id": "pr_local", "argv": ["/bin/sh", "-i"], "cwd": "/tmp", "env": {"HOME": "/tmp", "PATH": "/opt/lamp/bin:/usr/bin:/bin"}, "auth_modes": ["none"]}}
    for kind, binary in [("claude", args.m5_claude), ("codex", args.m5_codex)]:
        inside = {"binary": "/opt/lamp/bin/" + kind, "endpoint": "https://10.219.21.1:17779/v1/models/" + kind, "credential_file": "/tmp/lamp-model.token", "ca_file": "/tmp/lamp-model-ca.pem"}
        outside = {"binary": binary, "endpoint": "https://localhost:44421/v1/models/" + kind, "credential_file": args.m5_root + "/model.token", "ca_file": args.m5_root + "/ca.pem", "mcp_command": [args.m5_python, args.m5_source + "/tools/session_mcp.py", "--socket", args.m5_root + "/api.sock", "--sandbox-id", "{sandbox_id}", "--session-id", "{session_id}"]}
        harnesses["hr_" + kind] = {"owner_id": "pr_local", "argv": ["python3"], "cwd": "{session_dir}/work", "env": {"HOME": "{session_dir}/home", "PATH": "/opt/lamp/bin:" + args.m5_node_dir + ":/usr/local/bin:/usr/bin:/bin"}, "auth_modes": ["api_key"], "repository": bundle, "native": {"kind": kind, "binary": "/opt/lamp/bin/" + kind, "model": "openrouter/free", "profiles": {"host:hs_m5": outside, "sandbox:hs_local": inside, "sandbox:hs_t21": inside}}}
    sessions = root / "sessions-live.json"
    sessions.write_text(json.dumps({"harnesses": harnesses, "hosts": native_hosts, "recipes": {kind: {"harness_id": "hr_" + kind, "auth_mode": "api_key"} for kind in ["claude", "codex"]}}))
    api = root / "r/public/api.sock"
    daemon = start([str(binaries / "lampd-next"), "--state-dir", str(root / "control"), "--socket", str(api), "--sessions-config", str(sessions), "--worker-socket", str(worker_socket), "--worker-id", "lamp-supervisor@hs_t21", "--worker-boot-id", Path("/proc/sys/kernel/random/boot_id").read_text().strip(), "--worker-uid", "0", "--worker-pid", str(worker.pid), "--grant-id", grant["grant_id"], "--worker-timeout", "1m"], "control-live")
    shared = {"model": "string", "stream": "boolean?", "tools": "array?", "temperature": "number?", "top_p": "number?", "metadata": "object?"}
    messages = dict(shared, messages="array", max_tokens="number?", system="array?", tool_choice="object?", thinking="object?", output_config="object?", context_management="object?", stop_sequences="array?", top_k="number?")
    responses = dict(shared, input="array", instructions="string?", store="boolean?", parallel_tool_calls="boolean?", include="array?", reasoning="object?", text="object?", service_tier="string?", prompt_cache_key="string?", max_output_tokens="number?", tool_choice="string?", truncation="string?", previous_response_id="string?", stream_options="object?", safety_identifier="string?", client_metadata="object?")
    connections = {}
    for kind, operation, fields in [("claude", "messages", messages), ("codex", "responses", responses)]:
        connections[kind] = {"kind": "model", "access": "full", "endpoint": "https://openrouter.ai/api/v1/" + ("messages" if kind == "claude" else "responses"), "credential_command": ["/usr/bin/python3", str(args.source / "tools/broker_demo.py"), "credential", "/home/owner/lamp-t21-bridge/credential.sock"], "models": ["openrouter/free"], "free_only": True, "operations": {operation: {"write": True, "fields": fields}}}
    broker_config = private / "broker.json"
    broker_config.write_text(json.dumps({"accountability_socket": str(api), "connections": connections, "settings": {}}))
    broker = start([str(binaries / "lamp-broker"), "--config", str(broker_config), "--owner-socket", str(private / "owner.sock"), "--task-listen", "10.219.21.1:17779", "--tls-cert", str(private / "ca.pem"), "--tls-key", str(private / "tls.key"), "--audit", str(private / "audit.jsonl")], "model-live")
    deadline = time.monotonic() + 30
    while not api.exists() or not (private / "owner.sock").exists():
        if any(process.poll() is not None for process in processes) or time.monotonic() > deadline:
            raise RuntimeError("ticket-owned live services did not become ready")
        time.sleep(.05)
    manifest.update({"worker_pid": worker.pid, "control_pid": daemon.pid, "model_pid": broker.pid, "api_socket": str(api), "native_host": "hs_m5", "network_interface": "lamp-t21br", "model_owner_socket": str(private / "owner.sock")})
    (root / "live-manifest.json").write_text(json.dumps(manifest, indent=2))
    print(json.dumps({"state": "prepared_without_allocating_a_sandbox", "worker_pid": worker.pid, "control_pid": daemon.pid, "model_pid": broker.pid, "template_id": manifest["template_id"]}))


if __name__ == "__main__":
    main()
