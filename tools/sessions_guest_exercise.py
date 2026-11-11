import argparse
import base64
import json
from pathlib import Path

from session_public import PublicSessions
from sessions_guest_demo import capacity


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, required=True)
    parser.add_argument("--public-bundle", type=Path, required=True)
    args = parser.parse_args()
    manifest = json.loads((args.root / "manifest.json").read_text())
    client = PublicSessions(socket_path=args.root / "r/public/api.sock")
    sandbox = None
    session = None
    evidence = [{"step": "allocation", **capacity()}]
    try:
        sandbox = client.call("POST", "/v1/sandboxes", {"template_id": manifest["template_id"], "wait_ms": 60000})["result"]["sandbox_id"]
        session = client.call("POST", "/v1/sessions", {"sandbox_id": sandbox, "harness_id": "hr_guest_shell", "agent_mode": "inside"})["result"]["session_id"]
        client.wait(lambda: client.get("sessions", session)["state"] in {"active", "failed"})
        if client.get("sessions", session)["state"] != "active":
            raise RuntimeError("real guest PTY did not start")
        lease = client.lease(session)
        bundle = base64.b64encode(args.public_bundle.read_bytes()).decode()
        command = "stty -echo; printf 'T21_GUEST_TTY='; test -t 0 && echo yes; uname -s; hostname; printf %s " + bundle + " | base64 -d >/tmp/public.bundle; git clone -q /tmp/public.bundle /tmp/public-project; cat /tmp/public-project/README; printf '\\nT21_GUEST_COMPLETED\\n'\n"
        client.input(session, lease, 0, command.encode())
        client.wait(lambda: "Hello World!" in client.text(session))
        observed = client.text(session)
        if "T21_GUEST_TTY=yes" not in observed:
            raise RuntimeError("guest stdin is not a TTY")
        evidence.append({"step": "real-guest-pty", "sandbox_id": sandbox, "boundary": client.get("sandboxes", sandbox)["boundary"], "session_id": session, "public_repository": "octocat/Hello-World", "repository_delivery": "offline git bundle from the public repository", "output": observed})
    finally:
        if session:
            client.mutate("sessions", session, ":end", {"reason": "real guest demonstration complete"})
            client.wait(lambda: client.get("sessions", session)["state"] == "ended")
        if sandbox:
            client.call("DELETE", "/v1/sandboxes/" + sandbox, {"wait_ms": 60000}, client.get("sandboxes", sandbox)["revision"])
            client.wait(lambda: client.get("sandboxes", sandbox)["state"] == "deleted")
            evidence.append({"step": "released", "sandbox_state": "deleted"})
        (args.root / "guest-pty-evidence.json").write_text(json.dumps(evidence, indent=2) + "\n")
        print(json.dumps(evidence, indent=2))


if __name__ == "__main__":
    main()
