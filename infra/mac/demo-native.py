import argparse
import hashlib
import json
import socket
from pathlib import Path
import subprocess
import tempfile
import threading


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--launcher", type=Path, required=True)
    p.add_argument("--helper", type=Path, required=True)
    p.add_argument("--output", type=Path, required=True)
    a = p.parse_args()
    a.output.mkdir(parents=True, exist_ok=False)
    runtime = Path(tempfile.mkdtemp(prefix="lamp-t15-native-"))
    outside = runtime / "outside"
    outside.write_text("disposable outside fixture\n")
    subprocess.run(["sh", str(Path(__file__).with_name("package-native.sh")),
                    str(a.helper), str(runtime / "Native.app")], check=True, capture_output=True)
    helper = runtime / "Native.app/Contents/MacOS/lamp-native"
    server = socket.socket()
    server.bind(("127.0.0.1", 0))
    server.listen(1)
    server.settimeout(4)
    connections = []

    def accept():
        try:
            s, _ = server.accept()
            connections.append(True)
            s.close()
        except socket.timeout:
            pass

    observer = threading.Thread(target=accept)
    observer.start()
    command = (
        "set -eu; sqlite3 demo.db 'create table demo(n); insert into demo values(42); select n from demo;'; "
        f"if cat '{outside}'; then echo outside_read=allowed; else echo outside_read=denied; fi; "
        f"if printf changed > '{outside}'; then echo outside_write=allowed; else echo outside_write=denied; fi; "
        f"if curl -sS --max-time 2 'http://127.0.0.1:{server.getsockname()[1]}/'; then echo network=allowed; else echo network=denied; fi"
    )
    task = {"origin": "owner_task", "argv": ["/bin/sh", "-c", command],
            "memory_mib": 256, "timeout_seconds": 10}
    results = {"runtime": str(runtime), "fixture": "system SQLite",
               "launcher_sha256": hashlib.sha256(a.launcher.read_bytes()).hexdigest(),
               "helper_sha256": hashlib.sha256(helper.read_bytes()).hexdigest()}
    def run(label, request):
        r = subprocess.run([str(a.launcher), "native-task", "--helper", str(helper)],
                           input=json.dumps(request), capture_output=True, text=True, timeout=20)
        results[label] = {"exit": r.returncode, "stdout": r.stdout, "stderr": r.stderr}

    try:
        run("native_task", task)
        run("public_ci", dict(task, origin="public_repository_ci"))
        run("login", dict(task, login=True))
        run("protected_connection", dict(task, protected_connection=True))
        run("reuse", task)
    finally:
        observer.join()
        server.close()
    results["independent_network_connection_observed"] = bool(connections)
    results["outside_fixture_unchanged"] = outside.read_text() == "disposable outside fixture\n"
    (a.output / "result.json").write_text(json.dumps(results, indent=2) + "\n")
    print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
