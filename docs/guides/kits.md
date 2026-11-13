# Sandbox kits

Kits ship in `lampd` and run in a Linux guest on Linux and Apple Silicon hosts.
No `--kits-config` is needed for sandbox kits. `lamp kit list --json` lists the
built-in catalogue. Packages download into the disposable guest, so the first
installation needs internet access and can take several minutes. The sandbox's
network policy still applies. No host packages, browser profiles or Docker
resources are used.

Create with one or several kits:

```sh
lamp sandbox create --kit browser --kit python --json
```

The create operation becomes successful only after installation. Use its
`operation_id` with `lamp operation get` if it returns while still running.
Add another kit to an existing ready VM:

```sh
lamp sandbox kit "$SB" --kit desktop --wait-ms 60000 --json
```

Available names: `browser`, `desktop`, `docker`, `python`, `node`, `go`, `rust`,
`c`, `lsp`. Toolchains install on the normal guest PATH. The `lsp` kit includes
Node.js, TypeScript 5.9.3 and `typescript-language-server` (the TypeScript
version retains the tsserver protocol this server uses). The Python kit includes
pip, venv and matplotlib.

All commands below are also MCP tools (`sandbox_run_code`, `sandbox_browser`,
`sandbox_desktop`, `sandbox_lsp`, `sandbox_kit`) and HTTP operations
(`POST /v1/sandboxes/{sandbox_id}:run-code`, etc.). Mutations require an
`Idempotency-Key`; reuse it to inspect/replay an operation, rather than executing
code again. A lost result or a sandbox generation change after a job starts
returns `outcome_unknown`: the guest job may still run, so inspect before
submitting a new execution. CLI keys are generated automatically. Scope actions use the same
names with a dot after `sandbox`, for example `sandbox.run_code`.

## Stateful Python

```sh
lamp sandbox run-code "$SB" --lang python --code 'x=41; print(x); x+1' --wait-ms 60000 --json
lamp sandbox run-code "$SB" --code 'x+2' --wait-ms 60000 --json
lamp sandbox run-code "$SB" --code '1/0' --wait-ms 60000 --json
lamp sandbox run-code "$SB" --context fresh --code 'x' --wait-ms 60000 --json
lamp sandbox run-code "$SB" --code 'import matplotlib.pyplot as plt; plt.plot([1,2],[3,4])' --wait-ms 60000 --json
```

Replies contain `stdout`, `stderr`, the final expression's `result` as a Python
representation, structured `exception` details, and `outputs` containing guest
file paths, MIME types and sizes. Read image/video bytes with the existing
sandbox file-read/file-transfer APIs. Outputs are files so a large image need
not fit in a JSON/control-channel reply. Text replies are bounded; write large
results to a guest file. Named contexts are isolated Python namespaces in the
same sandbox. They survive a control-daemon restart while the guest remains
alive; a cold guest restart starts fresh contexts. Guest capture/resume retains
running interpreter memory. Delete the sandbox to dispose of its contexts,
processes, packages and artifacts. User code and services run under the existing
audited session bridge and VM resource limits.

## Chromium

```sh
lamp sandbox browser "$SB" --action navigate --url https://example.com --wait-ms 60000 --json
lamp sandbox browser "$SB" --action evaluate --expression 'document.title' --wait-ms 60000 --json
lamp sandbox browser "$SB" --action screenshot --wait-ms 60000 --json
lamp sandbox browser "$SB" --action close --wait-ms 60000 --json
```

The browser is headless and controllable through these operations. It can reach
guest-local apps, such as `http://127.0.0.1:8000`. It uses a throwaway guest profile
that sandbox code can read. This is separate from the optional owner-enrolled
external browser custody backend and its private login/takeover operations.

## Virtual desktop

The desktop uses Xvfb at 1024×768, Fluxbox and an xterm. It needs no physical
display, GPU, host desktop session or display passthrough device.

```sh
lamp sandbox desktop "$SB" --action info --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action move --x 100 --y 100 --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action click --button 1 --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action type --text 'echo hi' --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action key --key Return --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action screenshot --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action record_start --wait-ms 60000 --json
lamp sandbox desktop "$SB" --action record_stop --wait-ms 60000 --json
```

Use `record_start` and `record_stop` for the action values (underscores are part
of the API enum). Recordings are MP4 files. `info` returns screen dimensions,
pointer coordinates and `view_port: 6080`. For a view URL, publish that guest
HTTP port using the ordinary private preview flow:

```sh
lamp port create --sandbox-id "$SB" --port 6080 --protocol http --access private --wait-ms 60000 --json
lamp port get "$PORT_ID" --json
umask 077
lamp port share-link "$PORT_ID" --wait-ms 60000 --json > desktop-link.json
```

The port's `origin` is the desktop view URL. Enrolled signed-in viewers can
open it directly. Otherwise open the one-time-disclosed `result.url` from the
private `desktop-link.json` file to establish a preview session. That URL is a
bearer: keep it out of logs and evidence. An API token alone does not authorize
a preview origin. The view refreshes screenshots; input uses the desktop
operations. Set `DISPLAY=:99` when launching additional GUI apps with exec.

## Docker

```sh
lamp sandbox kit "$SB" --kit docker --wait-ms 60000 --json
lamp exec create --sandbox-id "$SB" --argv '["docker","run","--rm","alpine:3.20","echo","DIND_OK"]' --wait-ms 60000 --json
```

Docker's socket, image store and containers are inside the guest. VFS storage
and a private guest network served by slirp4netns avoid a privileged bridge or
host Docker socket. Use `--network host` for containers to share that private
Docker network when a container needs outbound connectivity. The default
container network runs local commands but has no forwarding. Here `host`
remains inside the sandbox VM. The guest init
exposes TUN to tasks; creating interfaces still requires a network namespace
owned by the task. Templates made with older init binaries need rebuilding.
Container processes retain Lamp's guest audit and resource limits.

## TypeScript LSP

Write `/tmp/example.ts` using sandbox file APIs (or exec), then:

```sh
lamp sandbox lsp "$SB" --action start --path /tmp --wait-ms 60000 --json
lamp sandbox lsp "$SB" --action open --path /tmp/example.ts --wait-ms 60000 --json
lamp sandbox lsp "$SB" --action symbols --path /tmp/example.ts --wait-ms 60000 --json
lamp sandbox lsp "$SB" --action completions --path /tmp/example.ts --line 1 --character 3 --wait-ms 60000 --json
lamp sandbox lsp "$SB" --action diagnostics --path /tmp/example.ts --wait-ms 60000 --json
lamp sandbox lsp "$SB" --action close --path /tmp/example.ts --wait-ms 60000 --json
lamp sandbox lsp "$SB" --action stop --wait-ms 60000 --json
```

Symbol/completion replies include `total` and `truncated` when the bounded
control-channel response omits entries. Positions are zero based UTF-16 LSP positions. `open` accepts optional `text` for
an unsaved buffer. Diagnostics return the latest published notification; an
empty list can mean the server has not published yet. One language server runs
per sandbox; stop it before starting it for a different project root.
