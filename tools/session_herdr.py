import argparse
import json
from pathlib import Path
import shlex
import subprocess

from session_public import PublicSessions


def main():
    parser = argparse.ArgumentParser(description="Join Lamp sessions through Herdr's public machine/pane API and stock SSH.")
    parser.add_argument("--socket")
    parser.add_argument("--url")
    parser.add_argument("--token-file")
    parser.add_argument("--herdr", default="herdr")
    target = parser.add_mutually_exclusive_group(required=True)
    target.add_argument("--machine")
    target.add_argument("--herdr-session", help="dedicated named Herdr server; never selects the focused session")
    parser.add_argument("--fleet", required=True)
    parser.add_argument("--ssh-target", required=True)
    parser.add_argument("--ssh-config", required=True)
    parser.add_argument("--session", action="append", default=[])
    parser.add_argument("--input", action="store_true")
    args = parser.parse_args()
    if bool(args.socket) == bool(args.url):
        parser.error("select one public API socket or URL")
    if args.ssh_target.startswith("-") or not Path(args.ssh_config).is_absolute():
        parser.error("use an SSH target and an absolute config path on the Herdr server")
    if args.input and len(args.session) != 1:
        parser.error("input enrollment requires exactly one explicit --session")
    client = PublicSessions(args.socket, args.url, args.token_file)
    client.get("fleets", args.fleet)
    sessions = client.call("GET", "/v1/sessions")["items"]
    if args.session:
        sessions = [client.get("sessions", identity) for identity in args.session]
    else:
        sessions = [session for session in sessions if session["state"] != "ended"]

    def herdr(*arguments):
        selection = ["--machine", args.machine] if args.machine else ["--session", args.herdr_session]
        output = subprocess.check_output([args.herdr, *selection, *arguments])
        return json.loads(output)["result"] if output.strip() else None

    def snapshot():
        return herdr("api", "snapshot")["snapshot"]

    label = "Lamp fleet " + args.fleet
    view = snapshot()
    workspace = next((v for v in view["workspaces"] if v["label"] == label), None)
    fresh = workspace is None
    if fresh:
        herdr("workspace", "create", "--label", label, "--no-focus")
        view = snapshot()
        workspace = next(v for v in view["workspaces"] if v["label"] == label)
    joined = []
    for session in sessions:
        mode = "input" if args.input else "watch"
        title = mode + " " + session["id"]
        tab = next((v for v in view["tabs"] if v["workspace_id"] == workspace["workspace_id"] and v["label"] == title), None)
        if tab is not None:
            pane = next(v for v in view["panes"] if v["tab_id"] == tab["tab_id"])
            processes = herdr("pane", "process-info", "--pane", pane["pane_id"])["process_info"]["foreground_processes"]
            if any(p["name"] in {"ssh", "ssh.exe"} and p.get("argv", [])[-2:] == ["attach" if args.input else "watch", session["id"]] for p in processes):
                joined.append({"session_id": session["id"], "tab_id": tab["tab_id"], "pane_id": pane["pane_id"], "mode": mode, "existing": True})
                continue
        elif fresh:
            tab = next(v for v in view["tabs"] if v["workspace_id"] == workspace["workspace_id"])
            herdr("tab", "rename", tab["tab_id"], title)
            fresh = False
        else:
            herdr("tab", "create", "--workspace", workspace["workspace_id"], "--label", title, "--no-focus")
        view = snapshot()
        tab = next(v for v in view["tabs"] if v["workspace_id"] == workspace["workspace_id"] and v["label"] == title)
        pane = next(v for v in view["panes"] if v["tab_id"] == tab["tab_id"])
        command = shlex.join(["ssh", "-tt" if args.input else "-T", "-F", args.ssh_config, "-o", "BatchMode=yes", "--", args.ssh_target, "attach" if args.input else "watch", session["id"]])
        herdr("pane", "rename", pane["pane_id"], title)
        herdr("pane", "run", pane["pane_id"], command)
        joined.append({"session_id": session["id"], "tab_id": tab["tab_id"], "pane_id": pane["pane_id"], "mode": mode})
    print(json.dumps({"fleet_id": args.fleet, "machine": args.machine, "herdr_session": args.herdr_session, "workspace_id": workspace["workspace_id"], "sessions": joined}))


if __name__ == "__main__":
    main()
