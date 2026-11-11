import argparse
import base64
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--bin", type=Path, required=True)
    p.add_argument("--guest", choices=["linux", "macos"], required=True)
    p.add_argument("--memory-mib", type=int, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--gpu-demo", type=Path)
    p.add_argument("--source-store", type=Path, default=Path("/private/var/lamp/store"))
    p.add_argument("--template-name")
    a = p.parse_args()
    a.output.mkdir(parents=True, exist_ok=False)
    runtime = Path(tempfile.mkdtemp(prefix="lamp-t15-", dir="/private/var/tmp"))
    runtime.chmod(0o755)
    store = runtime / "store"
    (store / "keys").mkdir(parents=True)
    (store / "templates").mkdir()
    source = a.source_store
    shutil.copyfile(source / "keys/producer.key", store / "keys/producer.key")
    (store / "keys/producer.key").chmod(0o600)
    name = a.template_name or ("macos-26.6.2" if a.guest == "macos" else "ubuntu-26.04-arm64")
    template = (source / "templates/names" / name).resolve()
    (store / "templates" / template.name).symlink_to(template)
    devices = {
        "display": {"width": 640, "height": 480},
        "gpu": a.guest == "macos",
        "keyboard": True, "mouse": True,
        "extra_disks": [{"id": "demo", "size_mib": 64}],
    }
    sup = str(a.bin / "lamp-supervisor")
    socket = str(runtime / "control.sock")
    log = (a.output / "supervisor.log").open("w")
    daemon = subprocess.Popen([
        sup, "serve", "--store", str(store), "--socket", socket,
        "--worker", str(a.bin / "lamp-vz"), "--identities", "13-14",
        "--ram-dir", "", "--devices", json.dumps(devices),
    ], stdout=log, stderr=log)
    env = dict(os.environ)
    results = {"guest": a.guest, "runtime": str(runtime), "devices": devices}
    sid = "sb_t15_demo"

    def ctl(label, *args):
        r = subprocess.run([sup, "ctl", "--socket", socket, *args],
                           capture_output=True, text=True, env=env, timeout=950)
        results[label] = {"exit": r.returncode, "stdout": r.stdout, "stderr": r.stderr}
        return r

    try:
        end = time.monotonic() + 180
        while not Path(socket).exists():
            if daemon.poll() is not None or time.monotonic() > end:
                raise RuntimeError("supervisor did not become ready")
            time.sleep(0.1)
        grant = subprocess.run([
            sup, "grant-install", "--store", str(store), "--caller-uid", "0",
            "--generation", "1", "--ttl-s", "3600",
        ], capture_output=True, text=True, check=True)
        env["LAMP_GRANT_ID"] = json.loads(grant.stdout)["grant_id"]
        ctl("oversized", "create", template.name, "sb_t15_oversized", "2", "32768", "20")
        results["oversized_directory_created"] = (store / "sb/sb_t15_oversized").exists()
        disk_gib = 48 if a.guest == "macos" else 20
        created = ctl("create", "create", template.name, sid, "2", str(a.memory_mib), str(disk_gib))
        if created.returncode:
            return
        spec_path = store / "sb" / sid / "spec.json"
        spec = json.loads(spec_path.read_text())
        results["worker_spec"] = spec
        baseline = dict(spec, devices={})
        baseline_path = runtime / "default-spec.json"
        baseline_path.write_text(json.dumps(baseline))
        r = subprocess.run([str(a.bin / "lamp-vz"), "validate", "--spec", str(baseline_path)],
                           capture_output=True, text=True)
        results["unrequested_devices"] = {"exit": r.returncode, "stdout": r.stdout, "stderr": r.stderr}
        if a.guest == "linux":
            script = (
                "import os,json,hashlib,pathlib\n"
                "p=bytes((i*7+11)%256 for i in range(4096))\n"
                "f=open('/dev/vdb','r+b',buffering=0); f.write(p); os.fsync(f.fileno()); f.seek(0); assert f.read(4096)==p\n"
                "r={'disk_roundtrip_bytes':4096,'disk_sha256':hashlib.sha256(p).hexdigest(),'inputs':pathlib.Path('/proc/bus/input/devices').read_text()}\n"
                "r['virtio_devices']={d.name:(d/'device').read_text().strip() for d in pathlib.Path('/sys/bus/virtio/devices').iterdir()}\n"
                "r['pci_devices']={d.name:{k:(d/k).read_text().strip() for k in ['class','vendor','device']} for d in pathlib.Path('/sys/bus/pci/devices').iterdir()}\n"
                "r['usb_devices']=[d.name for d in pathlib.Path('/sys/bus/usb/devices').iterdir()]\n"
                "fb=pathlib.Path('/sys/class/graphics/fb0')\n"
                "if fb.exists():\n"
                " w,h=map(int,(fb/'virtual_size').read_text().split(',')); bpp=int((fb/'bits_per_pixel').read_text()); data=bytes([77])*(w*h*bpp//8)\n"
                " with open('/dev/fb0','r+b',buffering=0) as f:\n"
                "  f.write(data); f.seek(0); actual=f.read(len(data)); assert actual==data\n"
                " r['framebuffer']={'width':w,'height':h,'bpp':bpp,'roundtrip_bytes':len(data),'sha256':hashlib.sha256(actual).hexdigest()}\n"
                "else: r['framebuffer']='not available'\n"
                "print(json.dumps(r))"
            )
            ctl("guest_io", "exec", sid, "--", "python3", "-c", script)
        else:
            if not a.gpu_demo:
                raise RuntimeError("--gpu-demo required for macos")
            payload = base64.b64encode(a.gpu_demo.read_bytes()).decode()
            script = (
                "set -eu; "
                f"printf %s '{payload}' | base64 -D > /tmp/t15-gpu; chmod 755 /tmp/t15-gpu; "
                "diskutil list; "
                "disk=$(diskutil list | awk '/67.1 MB/{print $NF}'); "
                'test -n "$disk"; /tmp/t15-gpu "/dev/r$disk"; '
                "system_profiler SPDisplaysDataType -json"
            )
            ctl("guest_io", "exec", sid, "--", "/bin/sh", "-c", script)
        ctl("capture_refusal", "suspend", sid)
    finally:
        if Path(socket).exists() and "LAMP_GRANT_ID" in env:
            ctl("release", "release", sid)
        daemon.terminate()
        daemon.wait(timeout=45)
        log.close()
        results["worker_directory_released"] = not (store / "sb" / sid).exists()
        (a.output / "result.json").write_text(json.dumps(results, indent=2) + "\n")
        print(json.dumps(results, indent=2))


if __name__ == "__main__":
    main()
