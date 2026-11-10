import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


def send(value):
    print(json.dumps(value), flush=True)


def payload(nonce, generation, size, domain):
    result = bytearray()
    for index in range((size + 4095) // 4096):
        seed = f"{nonce}:{generation}:{domain}:{index}".encode()
        result.extend(hashlib.sha256(seed).digest() * 128)
    return bytes(result[:size])


def save(path, value):
    with path.open("w") as output:
        json.dump(value, output)
        output.flush()
        os.fsync(output.fileno())


def worker(root, request):
    stable = {"objects": {}, "directory": [], "journal": [], "fault_trace": []}
    point, side = request["point"], request["side"]

    def boundary(name, when):
        if name == point and when == side:
            stable["fault_trace"].append({"event": "power_cut", "point": name, "side": when,
                                          "discard_volatile": True, "reorder": request.get("fault") == "reorder"})
            save(root / "stable.json", stable)
            os._exit(79)

    for generation in (0, 1):
        identity = str(generation)
        object = {
            "nonce": request["nonce"], "generation": generation,
            "parent": None if generation == 0 else "0",
            "producer": "authorized",
            "ram": base64.b64encode(payload(request["nonce"], generation, 8192, "ram")).decode(),
            "disk": base64.b64encode(payload(request["nonce"], generation, 16384, "disk")).decode(),
        }
        for phase in request["phases"]:
            if generation == 1:
                boundary(phase, "before")
            omit = request.get("omit") if generation == 1 else None
            if phase == "object_flush" and omit != phase:
                stable["objects"][identity] = object
            if phase == "directory_flush" and omit != phase:
                stable["directory"].append(identity)
            if phase == "journal_flush" and omit != phase:
                stable["journal"].append(identity)
            stable["fault_trace"].append({"event": phase, "generation": generation, "omitted": phase == omit})
            save(root / "stable.json", stable)
            if phase == "ack":
                send({"event": "ack", "value": {
                    "id": identity, "parent": object["parent"], "nonce": request["nonce"],
                    "generation": generation, "memory_bytes": 8192, "disk_bytes": 16384,
                }})
                if json.loads(sys.stdin.readline()) != {"acknowledged": identity}:
                    raise ValueError("external history did not acknowledge")
            if generation == 1:
                boundary(phase, "after")
    os._exit(0)


def recover(root, unauthorized=False):
    stable = json.loads((root / "stable.json").read_text())
    result = {}
    order = stable["journal"]
    if any(row.get("reorder") for row in stable["fault_trace"]):
        order = list(reversed(order))
    for identity in order:
        if identity not in stable["directory"] or identity not in stable["objects"]:
            continue
        object = stable["objects"][identity]
        object["memory_sha256"] = hashlib.sha256(base64.b64decode(object.pop("ram"))).hexdigest()
        object["disk_sha256"] = hashlib.sha256(base64.b64decode(object.pop("disk"))).hexdigest()
        object["ticks"] = 1
        result[identity] = object
    if unauthorized:
        result["1"]["producer"] = "unauthorized-but-validly-hashed"
    return {"objects": result, "qualification": "disposable-model-only", "fault_trace": stable["fault_trace"]}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", required=True)
    parser.add_argument("--request", required=True)
    parser.add_argument("--worker", action="store_true")
    args = parser.parse_args()
    root = Path(args.root)
    if not (root / "lamp-disposable-fixture").is_file():
        raise ValueError("refusing a non-disposable fixture")
    request = json.loads(args.request)
    if args.worker:
        worker(root, request)
    else:
        child = subprocess.run([sys.executable, __file__, "--root", str(root), "--request", args.request, "--worker"], check=False)
        if child.returncode not in (0, 79):
            raise RuntimeError("fixture worker failed")
        send({"event": "recovered", "value": recover(root, request.get("unauthorized", False)), "crash_exit": child.returncode})


if __name__ == "__main__":
    main()
