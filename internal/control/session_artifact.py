import base64
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import zipfile


ARTIFACT_LIMIT = 16 << 20

# Each harness's own native session store, relative to the session HOME.
# Credentials and caches are never included. Stores keyed by working
# directory are re-keyed to the target workspace on restore.
NATIVE_STATE = {
    "claude": ["claude/projects"],
    "codex": ["codex/sessions", "codex/session_index.jsonl"],
    "opencode": ["opencode-session.json"],
    "pi": ["pi/sessions"],
    "goose": ["data/goose/sessions"],
    "copilot": ["copilot/session-state", "copilot/session-store.db", "copilot/session-store.db-wal", "copilot/session-store.db-shm"],
    "droid": [".factory/sessions"],
    "cursor": ["config/chats"],
    "devin": ["devin-sessions"],
    # Amp threads live on ampcode.com; only the thread ID (lamp-native.json) moves.
    "amp": [],
}
CWD_KEYED = {
    "claude/projects/": lambda cwd: "".join(c if c.isalnum() else "-" for c in cwd),
    "pi/sessions/": lambda cwd: "--" + cwd.strip("/").replace("/", "-") + "--",
    ".factory/sessions/": lambda cwd: cwd.replace("/", "-"),
    "config/chats/": lambda cwd: hashlib.md5(cwd.encode()).hexdigest(),
}
NATIVE_FILES = {path for paths in NATIVE_STATE.values() for path in paths}


def native_allowed(relative):
    return relative == "lamp-native.json" or relative in NATIVE_FILES or any(relative.startswith(path + "/") for path in NATIVE_FILES)


def session_git(cwd, *argv, input=None):
    process = subprocess.run(["git", "-c", "core.hooksPath=/dev/null", "-c", "user.name=Lamp", "-c", "user.email=lamp@invalid", "-C", str(cwd), *argv], input=input, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    if process.returncode:
        raise ValueError("git operation did not complete")
    return process.stdout


def artifact_path(directory, request):
    identity = request["transfer_id"]
    if not isinstance(identity, str) or not identity or len(identity) > 180 or any(c not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-." for c in identity):
        raise ValueError("invalid transfer identity")
    return directory / ("artifact-" + identity + ".zip")


def artifact_snapshot(directory, request, native):
    path = artifact_path(directory, request)
    if path.exists():
        return path
    launch = json.loads((directory / "launch.json").read_text())
    cwd = Path(launch["cwd"]).resolve()
    if not cwd.is_relative_to(directory.resolve()) or not (cwd / ".git").is_dir():
        raise ValueError("continuation requires a private per-session git workspace")
    paths = request.get("selected_paths", [])
    if any(not isinstance(p, str) or not p or p.startswith("/") or ".." in Path(p).parts or p.startswith(".git") for p in paths):
        raise ValueError("invalid selected paths")
    base_file = cwd / ".git/lamp-base"
    base = base_file.read_text().strip() if base_file.exists() else session_git(cwd, "rev-parse", "HEAD").decode().strip()
    if session_git(cwd, "status", "--porcelain"):
        session_git(cwd, "add", "-A", "--", *(paths or ["."]))
        if session_git(cwd, "diff", "--cached", "--name-only"):
            session_git(cwd, "commit", "-q", "-m", "Preserve session work")
    head = session_git(cwd, "rev-parse", "HEAD").decode().strip()
    bundle = directory / ("bundle-" + request["transfer_id"])
    session_git(cwd, "bundle", "create", str(bundle), "--all", "HEAD")
    patch = session_git(cwd, "diff", "--binary", base, head, "--", *paths)
    metadata = {"cwd": str(cwd), "base": base, "base_tree": session_git(cwd, "rev-parse", base + "^{tree}").decode().strip(), "head": head, "commits": session_git(cwd, "rev-list", "--reverse", base + ".." + head).decode().splitlines(), "selected_paths": paths}
    temporary = path.with_suffix(".next")
    if request.get("include_native"):
        metadata["native_kind"] = (launch.get("native") or {}).get("kind", "")
        if metadata["native_kind"] not in NATIVE_STATE:
            raise ValueError("this harness has no portable native continuation store")
    with zipfile.ZipFile(temporary, "w", compression=zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("manifest.json", json.dumps(metadata))
        archive.write(bundle, "work.bundle")
        archive.writestr("changes.patch", patch)
        if request.get("include_native"):
            if not native:
                raise ValueError("native continuation adapter required")
            home = Path(launch["env"]["HOME"]).resolve()
            if not home.is_relative_to(directory.resolve()):
                raise ValueError("native state must be in the private session home")
            kind = metadata["native_kind"]
            for relative in ["lamp-native.json", *NATIVE_STATE[kind]]:
                source = home / relative
                if not source.exists():
                    continue
                if source.is_symlink():
                    raise ValueError("native continuation cannot follow symlinks")
                files = list(source.rglob("*")) if source.is_dir() else [source]
                for item in files:
                    if item.is_symlink() or not item.resolve().is_relative_to(home):
                        raise ValueError("native continuation cannot follow symlinks")
                    if item.is_file():
                        archive.write(item, "home/" + str(item.relative_to(home)))
    if temporary.stat().st_size > ARTIFACT_LIMIT:
        raise ValueError("session continuation retention quota exceeded")
    os.replace(temporary, path)
    with path.open("rb") as stream:
        os.fsync(stream.fileno())
    return path


def artifact_upload(directory, request):
    path = artifact_path(directory, request).with_suffix(".upload")
    offset = request.get("artifact_offset", 0)
    data = request["artifact_data"].encode("ascii")
    if not isinstance(offset, int) or offset < 0 or offset + len(data) > (ARTIFACT_LIMIT * 4 // 3) + 4:
        raise ValueError("continuation retention quota exceeded")
    with path.open("r+b" if path.exists() else "w+b") as target:
        target.seek(0, 2)
        size = target.tell()
        target.seek(offset)
        if offset == size:
            target.write(data)
            target.flush()
            os.fsync(target.fileno())
        elif offset > size or target.read(len(data)) != data:
            raise ValueError("non-idempotent continuation upload")
    return {"artifact_offset": offset + len(data)}

def artifact_info(directory, request):
    path = artifact_path(directory, request)
    with zipfile.ZipFile(path) as archive:
        metadata = json.loads(archive.read("manifest.json"))
    return {"artifact_sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "artifact_size": path.stat().st_size, "artifact_info": metadata}


def artifact_open(directory, request):
    data = base64.b64decode(artifact_path(directory, request).with_suffix(".upload").read_bytes(), validate=True)
    if len(data) > ARTIFACT_LIMIT or hashlib.sha256(data).hexdigest() != request["artifact_sha256"]:
        raise ValueError("continuation integrity mismatch")
    archive = zipfile.ZipFile(io.BytesIO(data))
    total = 0
    seen = set()
    for entry in archive.infolist():
        total += entry.file_size
        if total > ARTIFACT_LIMIT or entry.filename.startswith("/") or ".." in Path(entry.filename).parts or "\\" in entry.filename or entry.filename in seen:
            raise ValueError("invalid continuation archive")
        seen.add(entry.filename)
        allowed = entry.filename in ["manifest.json", "work.bundle", "changes.patch"] or entry.filename.startswith("home/") and native_allowed(entry.filename[5:])
        if not allowed or entry.is_dir() or (entry.external_attr >> 16) & 0o170000 == 0o120000:
            raise ValueError("continuation contains a forbidden native path")
    return archive


def artifact_restore(directory, request):
    with artifact_open(directory, request) as archive:
        metadata = json.loads(archive.read("manifest.json"))
        cwd = Path(request["cwd"].replace("{session_dir}", str(directory)))
        home = Path(request["env"]["HOME"].replace("{session_dir}", str(directory)))
        if not cwd.resolve().is_relative_to(directory.resolve()) or not home.resolve().is_relative_to(directory.resolve()):
            raise ValueError("continuation target must be private per-session state")
        bundle = directory / "restored.bundle"
        bundle.write_bytes(archive.read("work.bundle"))
        if not (cwd / ".git").exists():
            cwd.parent.mkdir(parents=True, mode=0o700, exist_ok=True)
            session_git(directory, "clone", "--quiet", str(bundle), str(cwd))
            session_git(cwd, "checkout", "--quiet", "--detach", metadata["head"])
        elif session_git(cwd, "rev-parse", "HEAD").decode().strip() != metadata["head"]:
            raise ValueError("continuation target already contains different work")
        (cwd / ".git/lamp-base").write_text(metadata["base"] if request.get("include_native") else metadata["head"])
        for entry in archive.infolist():
            if not entry.filename.startswith("home/"):
                continue
            relative = entry.filename[5:]
            for prefix, slug in CWD_KEYED.items():
                if relative.startswith(prefix):
                    depth = len(Path(prefix).parts)
                    tail = Path(relative).parts[depth + 1:]
                    relative = str(Path(prefix, slug(str(cwd.resolve())), *tail))
            target = home / relative
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            target.write_bytes(archive.read(entry))
            target.chmod(0o600)
        return metadata


def artifact_apply(directory, request):
    launch = json.loads((directory / "launch.json").read_text())
    cwd = Path(launch["cwd"])
    with artifact_open(directory, request) as archive:
        patch = archive.read("changes.patch")
        metadata = json.loads(archive.read("manifest.json"))
    tag = "Lamp-Transfer: " + request["transfer_id"]
    commits = session_git(cwd, "log", "--format=%H", "--grep=^" + tag + "$").decode().splitlines()
    for commit in commits:
        body = session_git(cwd, "show", "-s", "--format=%B", commit).decode()
        if "Lamp-Artifact: " + request["artifact_sha256"] in body:
            return {"applied_commit": commit, "conflicts": []}
    try:
        if session_git(cwd, "status", "--porcelain"):
            return {"conflicts": metadata["selected_paths"] or ["."]}
        if patch:
            session_git(cwd, "apply", "--3way", "--check", "-", input=patch)
            session_git(cwd, "apply", "--3way", "--index", "-", input=patch)
            session_git(cwd, "commit", "-q", "-m", "Apply child session work\n\n" + tag + "\nLamp-Artifact: " + request["artifact_sha256"])
        return {"applied_commit": session_git(cwd, "rev-parse", "HEAD").decode().strip(), "conflicts": []}
    except ValueError:
        return {"conflicts": metadata["selected_paths"] or ["."]}
