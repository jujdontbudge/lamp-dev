# Owner-operated kits

`lampd --kits-config /private/config.json` starts the bundled headless kits
backend. Canonical `browser_*` and `kit_*` routes are installed in Lamp's public
API and generic `lamp browser …` / `lamp kit …` CLI. `--kits-socket` can instead
attach an existing owner-only backend; do not specify both options. The config
below supplies its public control socket, token-file path and prepared template.
The backend uses only the public sandbox/exec API, Python's standard library,
`openssl` and the selected native password-manager CLI. Dashboard views remain
T27's work. The standalone companion remains available for operator diagnostics.

The integration builds and its managed service starts, but its complete live
browser/CLI and agent-isolation demonstration is still pending; see
[T22 evidence](T22-evidence.md). Browser VM allocation carries an owner-only
`lamp.browser-custody=external` label from birth. Non-bootstrap agent credentials,
including broad keys sharing the owner principal, are denied the underlying VM
and its source snapshots. Browser resource scopes also bind to the agent sandbox.
Private screenshot responses are transient, not persisted in operation outcomes.

Use `--input -` for private JSON input rather than browser arguments on argv.
For screenshots, `lamp browser action … --input - --private-png /private/new.png`
writes a new mode-0600 file and does not print the image or private page URL.

## Start

Prepare a disposable Linux x86-64 template with Python 3, OpenSSL and
Google Chrome/Chromium already installed. Browser installation
is also available through the `browser` toolchain kit. The initial package
installer supports apt-based Linux guests, not macOS guests. On Ubuntu it uses
the vendor's public Chrome .deb rather than Ubuntu's snap-only Chromium launcher.
The vendor download currently tracks stable, not a reproducibly pinned build.

Create an owner-only directory and mode-0600 config, for example:

```json
{
  "control_socket": "/private/path/api.sock",
  "control_token_file": "/private/path/control-token",
  "browser_template": "tp_prepared",
  "linux_admission_host": "ubuntu-server",
  "vaults": {
    "disposable": {
      "provider": "keepassxc",
      "binary": "/opt/homebrew/bin/keepassxc-cli",
      "home": "/private/disposable-cli-home",
      "vault": "/private/disposable.kdbx",
      "unlock_file": "/private/disposable-unlock"
    }
  }
}
```

`linux_admission_host` is for a Mac service using a Unix-socket SSH tunnel to
LX. The fixed SSH command observes total Firecracker count and `free -m` right
before browser allocation. Without it, Linux uses local observations; a Mac
using a remote Linux control socket must set it. The checks are not a distributed
reservation across tickets: operators must coordinate simultaneous admissions.

```sh
python3 -m kits serve --config /private/config.json --root /private/kits-state \
  --owner-socket /private/kits.sock --task-listen 127.0.0.1:19223
python3 -m kits call --owner-socket /private/kits.sock < /private/request.json
```

The owner socket checks peer UID and requires a private directory. Task access
uses a 5-minute random bearer bound to a browser and both VM generations.
Non-loopback task HTTP requires `--tls-cert`/`--tls-key`. Use a separately scoped
canonical credential for the agent: it must **not** be able to exec, capture,
fork or read the external browser VM. Never give the kit control credential or
owner socket to the agent. A trusted operator retains the kit's broad control
credential. Identity routing/enrollment is not supplied by this companion.

State contains cleanup IDs/public keys, not vault passwords. A restart revokes
all task grants and approvals and fences recovered browsers in private state.
Owner `grant` issues a new task bearer after rechecking the VM generations.
Explicit `delete` releases the external browser VM; it stops only the browser in
copied mode, not the agent's workload. Failed setup retains its ID in
`browsers.json` so an owner can delete it. Do not abandon that allocation.
Stopping the companion does not delete its VMs; restart with the same state and
use `delete`. No background trial, soak or benchmark runner is installed.

## Browser and private control

Owner requests use `/owner`; task requests use `/task`. Both accept JSON POST.
Every request after create includes `browser_id`.

| Operation | Who | Additional fields |
| --- | --- | --- |
| `create` | owner | `agent_sandbox_id`, optional `mode` (default `external`) |
| `get` | both | none |
| `navigate` | both outside private control; owner during it | `url` |
| `snapshot` | task or owner outside private control | none; bounded text only |
| `click` | both outside private control; owner during it | `selector` |
| `type` | both outside private control | `selector`, nonsecret `text` |
| `takeover` | owner | none |
| `private_view` | owner | none; CLI requires `--private-png /new/private/image.png` |
| `private_type` | owner | `selector`, `text` on private stdin, never argv |
| `return` | owner | none |
| `propose_fill` | both | `vault_id`, `item_id`, exact HTTPS `origin`, optional `submit` |
| `approval` | owner | `approval_id`; reads the authoritative server plan |
| `approve_fill` | owner | `approval_id` and exact reviewed `plan` |
| `revoke`, `grant`, `delete` | owner | none |
| `install` | owner | `sandbox_id`, requested `tools` array; returns `job_id`/`installing` |
| `install_status` | owner | `sandbox_id`, `job_id`; returns `installing`, `ready` or `failed` |

External browsers run in a separate guarded Lamp VM with a fresh profile, not
in the owner's browser or the agent's filesystem. Chromium runs through the
protected audit collector, as namespace root mapped to an unprivileged guest UID.
Its own namespace sandbox is disabled for minimal guest-kernel support;
the dedicated Lamp VM is the isolation boundary. CDP listens on **VM loopback
only** and no raw CDP/evaluate/download/cookie API is exposed to task callers.
Text snapshots are not a general secret-redaction mechanism.

Takeover fences task calls before private effects. Owner screenshots, private
navigation/clicks/typing and vault fills use authenticated hybrid encryption:
RSA-OAEP-SHA256 wraps a fresh random 256-bit encryption password and independent
HMAC-SHA256 key. Salted PBKDF2 derives AES-256-CBC/IV; HMAC authenticates the entire
ciphertext before decryption. Encryption passwords travel over inherited pipes,
not argv/environment or guest process-audit records. Private screenshots are
encrypted back to an owner-only key. Public exec
journals thus hold ciphertext rather than screenshots/password input. Keys and
plaintext still exist in the trusted owner process and browser VM: no protection
against a hostile host owner, VM administrator, vault CLI, website or physical
memory inspection is claimed. Python cannot promise complete memory zeroization.

Return creates a clean browser context on `about:blank`, closes all old private
targets/popups, and transfers **only unpartitioned HttpOnly cookies**. It does not copy private
DOM, history, local/session storage or script-readable cookies. The blank page
is the safe handback page. Websites using JS-readable session storage may need
to stay private or log in again; this is not silently treated as successful
session transfer. A trusted approved website can receive the submitted password;
Lamp does not prove arbitrary site JavaScript harmless.

`mode: "copied"` requires `acknowledge_agent_can_read_logins: true` and reports
`AGENT_CAN_READ_COPIED_LOGINS`. Optional `cookies` supplies only deliberately
selected disposable cookies, encrypted on delivery. Alternatively, specify
`source_browser_id` for an external browser already in private control and
`login_origin` for an explicitly selected HTTPS origin. That route transfers only
matching unpartitioned HttpOnly cookies, encrypted in both directions, without
returning cookies to the CLI or task API. It never discovers or copies an owner's
real profile. Root inside that VM can inspect browser memory/logins.
Consequently private takeover and automatic vault fills are **refused** there.
Use an external browser for private typing, not a false redaction promise.

## Vault adapters

Each configured vault has a provider, absolute native `binary`, isolated CLI
`home`, and `vault` identity. There is no task-controlled executable or account
search. Each approval is one-use, expires after 60 seconds and binds the exact
browser generation, vault, item, destination origin and submit choice. Approval
enters private control before reading a credential. Locked stores, native errors,
unrecognized output and nonstandard/cross-origin forms fall back to
`PRIVATE_TYPING_REQUIRED`/private owner control; no CLI stderr is forwarded.

- `1password`: `op item get --vault <id> --format json`; verifies returned vault
  ID. Optional private `unlock_file` supplies a service-account token via env.
  Biometric unlock is disabled. It does not enroll/unlock an owner's account.
- `bitwarden`: `bw get item`; private `unlock_file` supplies `BW_SESSION` via env.
  `vault` is an explicit collection ID or `personal` (rejects organization items).
  Optional `ca_file` supplies `NODE_EXTRA_CA_CERTS` for an explicitly trusted
  self-hosted HTTPS endpoint, without changing host trust or disabling TLS.
  The real disposable demonstration uses **Vaultwarden 1.37.5** and Bitwarden
  CLI 2026.10.1, not an owner's Bitwarden account. That CLI refuses HTTP even on
  loopback; the native fixture pins HTTPS discovery and every CLI service URL.
  `tools/kits_vaultwarden_tls.py` and `tools/kits_vaultwarden_provision.py` are
  explicit disposable operator helpers, not automatic owner-account discovery.
  The provisioning helper additionally requires Python `cryptography`.
- `keepassxc`: `keepassxc-cli show --show-protected` for exactly username/password;
  private database password goes over stdin. Optional `key_file` is supported.
- `proton-pass`: `pass-cli item view --share-id <vault> --item-id <item> --field
  username|password`, using an already provisioned isolated CLI home. The command
  shape follows the [public CLI documentation](https://github.com/protonpass/pass-cli/blob/main/docs/public/docs/commands/contents/view.md).

All are host-side readers, not browser extensions. Only actual demonstrated
vendors are claimed in `T22-evidence.md`; code paths alone are not real vendor
integration evidence. MFA, passkeys, iframes, multi-step/SPA forms, extension UI
and automatic account setup are not implemented. A person can use private typing
for unsupported fields without giving the agent a vault/session export.

## Toolchains and Docker

`tools` accepts `python`, `node`, `go`, `rust`, `c`, `git`, `browser`, `docker`.
Package installation runs through public exec **in the chosen ready VM**; no
host package/Docker action is used by the kit. `install` starts one background
guest job, and `install_status` reads its generation-bound completion file.
`installing` is not success: only `ready` includes the installed tool result.
The job continues through the existing audited background-process path, rather
than trying to keep one public exec observed past the control worker's deadline.
Status observation never replays installation; a guest lock prevents concurrent
package writers. A lost response/restart does not authorize an automatic retry. It first reuses already-installed tools or packages cached in the authenticated
prepared template. Missing packages are resolved/downloaded using apt's signed
indices. Each job has a separate download directory, so an incomplete download
cannot be extracted by a later job. Archives are extracted into
`/tmp/lamp-toolchains` without maintainer scripts, setuid helpers or global dpkg
changes. The response supplies a usable `path` of guest-local wrappers. This is
a user-prefix toolchain, not a claim that arbitrary apt packages can relocate
unchanged. Complex toolchains may still need a prepared template.

Docker's explicit socket is `unix:///tmp/lamp-kit-docker/docker.sock` inside that
VM; containerd's socket/data/state and Docker's data/exec roots are private guest
paths too. Containerd also hard-codes `/run/containerd` for shim sockets/FIFOs.
Linux guest PID 1 now creates only that directory with sticky mode `1777` after
mounting `/run`; it does not make the rest of `/run` writable. Preparing the
build-time tmpfs alone would not survive the template's fresh boot.

The Docker job uses `unshare --user --map-root-user --mount
--propagation=private` inside the existing guarded VM. This maps only the existing
task UID into a nested namespace, not guest or host root, and uses no privileged
helper. Namespace bootstrap failure publishes a failed job, never permanent
`installing` or a false ready result. No RootlessKit service is claimed or faked.
Private guest startup diagnostics live in `/tmp/lamp-kit-docker/runtime.log` and
`/tmp/lamp-kit-jobs/<job>.bootstrap.log`; the task API does not forward them.

The daemon uses `--rootless`, vfs and no bridge/iptables changes; an actual guest
`docker info` must succeed before the kit reports installation success. That is
not proof that a container ran. `tools/kits_docker_demo.py` exercises a public
BusyBox container with `--network=none`, normalizing archive ownership for the
single-UID mapping. Arbitrary images/users, networking and complex builds are
not qualified. No host socket, daemon, container, image or network is imported
or managed. This is Linux Docker-in-VM, not native Docker on macOS guests. See
`T22-evidence.md` for the current live result; no root/collector bypass or false
success is substituted.

The disposable LX fixture launcher handles SIGTERM and SSH hangup, shuts down
at its existing worker-grant expiry, and attempts worker shutdown even when an
earlier teardown step fails. The Docker journey always stops that owned fixture
controller, including after an unknown create/delete outcome. It never stops
another ticket's worker. Output directories must be new; failed runs are kept.
