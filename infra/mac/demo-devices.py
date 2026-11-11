import argparse
import base64
import json
import hashlib
import io
import math
import secrets
import os
from pathlib import Path
import shutil
import socket as unix_socket
import subprocess
import tempfile
import time
import struct
import wave


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--bin", type=Path, required=True)
    p.add_argument("--guest", choices=["linux", "macos"], required=True)
    p.add_argument("--memory-mib", type=int, required=True)
    p.add_argument("--output", type=Path, required=True)
    p.add_argument("--gpu-demo", type=Path)
    p.add_argument("--source-store", type=Path, default=Path("/private/var/lamp/store"))
    p.add_argument("--template-name")
    p.add_argument("--guest-bridge", type=Path)
    p.add_argument("--mac-input-observer", type=Path)
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
    template = source / "templates" / os.readlink(source / "templates/names" / name).split("/")[-1]
    (store / "templates" / template.name).symlink_to(template)
    devices = {
        "display": {"width": 640, "height": 480},
        "gpu": a.guest == "macos",
        "keyboard": True, "mouse": True,
        "extra_disks": [{"id": "demo", "size_mib": 64}],
    }
    if a.guest_bridge:
        devices.update(audio_input=True, audio_output=True)
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
        started=time.time()
        clock=time.monotonic()
        r = subprocess.run([sup, "ctl", "--socket", socket, "--command-timeout-ms", "300000", *args],
                           capture_output=True, text=True, env=env, timeout=950)
        results[label] = {"exit": r.returncode, "stdout": r.stdout, "stderr": r.stderr,
                          "host_started_unix":started,"host_finished_unix":time.time(),"elapsed_s":time.monotonic()-clock}
        (a.output/"result.json").write_text(json.dumps(results,indent=2)+"\n")
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
        def guest_request(data):
            with unix_socket.socket(unix_socket.AF_UNIX,unix_socket.SOCK_STREAM) as s:
                s.settimeout(60);s.connect(str(spec_path.parent/"vsock.sock"))
                s.sendall(b"CONNECT 1024\n")
                f=s.makefile("rb")
                if f.readline()!=b"OK 1024\n":raise RuntimeError("guest handshake failed")
                payload=json.dumps(data).encode()
                s.sendall(struct.pack(">I",len(payload))+payload)
                header=f.read(4)
                if len(header)!=4:raise RuntimeError("guest frame absent")
                n=struct.unpack(">I",header)[0]
                if n>4*1024*1024:raise RuntimeError("guest frame too large")
                return json.loads(f.read(n))
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
        if a.mac_input_observer:
            if a.guest!="macos": raise RuntimeError("macOS fixture only")
            payload=base64.b64encode(a.mac_input_observer.read_bytes()).decode()
            ctl("observer_upload","exec",sid,"--","/bin/sh","-c",f"printf %s '{payload}' | base64 -D > /tmp/t15-input; chmod 755 /tmp/t15-input")
            script=(
                "set -eu; test ! -e /etc/kcpassword; "
                "if dscl . -list /Users UniqueID | awk '$2==501 {found=1} END {exit !found}'; then exit 1; fi; "
                "dscl . -create /Users/lampfixture; "
                "dscl . -create /Users/lampfixture UniqueID 501; "
                "dscl . -create /Users/lampfixture PrimaryGroupID 20; "
                "dscl . -create /Users/lampfixture RealName 'Lamp Fixture'; "
                "dscl . -create /Users/lampfixture NFSHomeDirectory /Users/lampfixture; "
                "dscl . -create /Users/lampfixture UserShell /bin/zsh; "
                'password=$(/usr/bin/openssl rand -hex 12); dscl . -passwd /Users/lampfixture "$password"; '
                "createhomedir -c -u lampfixture >/dev/null; "
                "defaults write /Library/Preferences/com.apple.loginwindow autoLoginUser -string lampfixture; "
                "defaults write /Library/Preferences/com.apple.loginwindow autoLoginUserUID -int 501; "
                'umask 077; printf %s "$password" | /tmp/t15-input --kcpassword; unset password; '
                "sync"
            )
            provision=ctl("gui_fixture_provision","exec",sid,"--","/bin/sh","-c",script)
            if provision.returncode:raise RuntimeError("disposable guest GUI fixture provisioning failed")
            before=guest_request({"op":"ping"})
            reboot=guest_request({"op":"exec","sandbox_id":sid,"generation":1,"exec_id":"op_t15_fixture_reboot",
                                  "argv":["/sbin/shutdown","-r","now"],"env":{},"cwd":"","timeout_ms":300000,"output_limit":65536})
            results["guest_reboot"]=reboot
            deadline=time.monotonic()+180
            while time.monotonic()<deadline:
                try:
                    after=guest_request({"op":"ping"})
                    if after.get("kernel_boot_id")!=before.get("kernel_boot_id") and after.get("result")=="pong":break
                except (OSError,ValueError,RuntimeError):pass
                time.sleep(.5)
            else:raise RuntimeError("guest reboot did not complete")
            results["guest_reboot_status"]={"before":before,"after":after}
            (a.output/"result.json").write_text(json.dumps(results,indent=2)+"\n")
            detail=json.loads(created.stdout)["detail"]
            ack=guest_request({"op":"barrier","sandbox_id":sid,"generation":1,"hostname":sid.replace("_","-"),
                               "machine_id":detail["machine_id"],"entropy_hex":secrets.token_hex(64),"unix_time_ns":time.time_ns()})
            results["guest_reboot_barrier"]={"before":before,"after":after,"ack":ack}
            if ack.get("result")!="barrier_ack":raise RuntimeError("post-reboot identity barrier refused")
            ctl("gui_status","exec",sid,"--","/bin/sh","-c","ps -axo uid,comm | awk '$2 ~ /WindowServer|loginwindow|Finder|Dock/ {print}'; defaults read /Library/Preferences/com.apple.loginwindow; launchctl print gui/501 | head -20")
            ctl("observer_reupload","exec",sid,"--","/bin/sh","-c",f"printf %s '{payload}' | base64 -D > /tmp/t15-input; chmod 755 /tmp/t15-input")
            script="i=0; until launchctl print gui/501 >/dev/null 2>&1; do i=$((i+1)); test $i -lt 600 || exit 1; sleep .2; done; /tmp/t15-input --detach /tmp/lamp-observer.log /bin/launchctl asuser 501 /usr/bin/sudo -n -u lampfixture /tmp/t15-input /tmp/lamp-input-observer"
            ctl("observer_start","exec",sid,"--","/bin/sh","-c",script)
            observer=ctl("observer_ready","exec",sid,"--","/bin/sh","-c","i=0; until test -f /tmp/lamp-input-observer/events.json; do i=$((i+1)); test $i -lt 600 || { launchctl print gui/501; cat /tmp/lamp-observer.log; exit 1; }; sleep .2; done; cat /tmp/lamp-input-observer/events.json")
            if observer.returncode:raise RuntimeError("guest input observer did not start")
        if a.guest_bridge:
            bridge="/tmp/lamp-guest-devices"
            ctl("bridge_init","exec",sid,"--","/bin/sh","-c",f"umask 077; : > {bridge}")
            binary=a.guest_bridge.read_bytes()
            for offset in range(0,len(binary),30000):
                payload=base64.b64encode(binary[offset:offset+30000]).decode()
                decode="-D" if a.guest=="macos" else "-d"
                r=ctl(f"bridge_upload_{offset}","exec",sid,"--","/bin/sh","-c",f"printf %s '{payload}' | base64 {decode} >> {bridge}")
                if r.returncode: raise RuntimeError("guest bridge upload failed")
            ctl("bridge_mode","exec",sid,"--","/bin/chmod","700",bridge)
            directory="/tmp/lamp-devices"
            bridge_config=dict(devices)
            if a.guest=="macos" and a.mac_input_observer:bridge_config["input_endpoint"]="/tmp/lamp-input-observer/input.sock"
            ctl("bridge_start","exec",sid,"--",bridge,"start",directory,json.dumps(bridge_config))
            def request(label,data):
                return ctl(label,"exec",sid,"--",bridge,"request",directory,json.dumps(data))
            for attempt in range(50):
                r=request("bridge_status",{"op":"status"})
                if r.returncode==0:break
                time.sleep(0.1)
            else: raise RuntimeError("guest device bridge did not start")
            pcm=b"".join(struct.pack("<h",round(12000*math.sin(2*math.pi*440*i/48000))) for i in range(4800))
            def wav(data):
                out=io.BytesIO()
                with wave.open(out,"wb") as f:
                    f.setnchannels(1);f.setsampwidth(2);f.setframerate(48000);f.writeframes(data)
                return out.getvalue()
            (a.output/"injected.wav").write_bytes(wav(pcm))
            request("audio_feed",{"op":"audio_input","pcm_hex":pcm.hex()})
            ctl("audio_record","exec",sid,"--",bridge,"record",directory,str(len(pcm)))
            recorded=request("audio_recorded",{"op":"recording"})
            recorded_wav=bytes.fromhex(json.loads(recorded.stdout)["wav_hex"])
            (a.output/"recorded.wav").write_bytes(recorded_wav)
            ctl("audio_play","exec",sid,"--",bridge,"play",directory,"{}")
            played=request("audio_drain",{"op":"audio_output","bytes":len(pcm)})
            actual=bytes.fromhex(json.loads(played.stdout)["pcm_hex"])
            (a.output/"played.wav").write_bytes(wav(actual))
            if recorded_wav!=wav(pcm) or actual!=pcm:raise RuntimeError("PCM round trip mismatch")
            results["audio_roundtrip"]={"bytes":len(pcm),"sha256":hashlib.sha256(actual).hexdigest(),"recorded_and_played":True}
            code=0 if a.guest=="macos" else 30
            if a.guest=="macos" and a.mac_input_observer:
                request("key_down",{"op":"input","events":[{"kind":"key","code":code,"down":True}]})
                request("key_up",{"op":"input","events":[{"kind":"key","code":code,"down":False}]})
                request("pointer_move",{"op":"input","events":[{"kind":"pointer","x":123,"y":234}]})
                observed=ctl("input_delivery","exec",sid,"--","/bin/cat","/tmp/lamp-input-observer/events.json")
                actual=json.loads(observed.stdout)
                kinds=[(e["kind"],e.get("down")) for e in actual["events"]]
                if ("key",True) not in kinds or ("key",False) not in kinds or ("pointer",None) not in kinds:raise RuntimeError("guest application did not receive keyboard and pointer events")
                image=ctl("guest_display_png","exec",sid,"--","/usr/bin/base64","-i","/tmp/lamp-input-observer/display.png")
                if image.returncode==0:(a.output/"guest-display.png").write_bytes(base64.b64decode(image.stdout))
            elif a.guest=="macos":
                raise RuntimeError("macOS input demonstration requires an application receiver")
            else:
                request("input_delivery",{"op":"input","events":[{"kind":"key","code":code,"down":True},{"kind":"key","code":code,"down":False},{"kind":"pointer","x":123,"y":234}]})
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
