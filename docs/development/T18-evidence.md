# T18 access evidence — 2026-11-10

The real guest, offline access, embedded tailnet, fleet host CA and short-lived
SSH certificate paths work. **The ticket's full Goal is not yet demonstrated:**
the base has no T21 session backend, Cloudflare's saved Air session is signed out,
and no separate Tailscale GitHub federation has been provisioned. Automatic
public HTTPS, a registered named Cloudflare tunnel, live GitHub federation and
persistent session attachment remain unproven. The PR implements their access
adapters and documents those dependencies rather than calling them completed.

## Real LX guest journey

[`lx-demonstration.jsonl`](evidence/t18/lx-demonstration.jsonl) records one
successful journey through the real `lampd`, supervisor and Firecracker guest.
The [recipe](evidence/t18/demonstration.py) uses an existing prepared T10 template,
not a benchmark or timing run. It creates one sandbox, then deletes it immediately
after the console and route demonstration. No owner service or Docker object was
changed. The owner's system Tailscale and its ACL were untouched.

Before the sole allocation, `pgrep -c firecracker` was **0** and `free -m` showed
**25,842 MiB available**. Sandbox `sb_f8749834079fc178ebeb032418ea4b91` reached
`ready`. Ordinary OpenSSH wrote `be1d8d39a2a68e6f17ce0ce8` to a file inside that
guest. It read that same nonce using all of:

- A direct local SSH connection, with strict fleet host CA verification.
- An OpenSSH `ProxyCommand` carrying SSH through Lamp's WebSocket endpoint.
- An embedded Tailscale client reaching the embedded fleet node, enrolled in a
  disposable **owner-run Headscale 0.29.7**, with no system TUN device or manual
  inbound setup. A collision check preceded use of `100.127.240.0/24`.
- A five-minute SSH user certificate issued from the embedded stack's actual
  `WhoIs` identity. After removing the authorized-key allowlist, OpenSSH still
  authenticated with that certificate and read the nonce. Certificate inspection
  showed only principal `owner`, `permit-pty`, and no forwarding extensions.

`uname -a` as a host command was refused. OpenSSH stdio forwarding was refused.
`sessions` returned `control session_list: NOT_FOUND`, honestly reflecting the
base's absent session implementation. The service never substituted a host shell.
A guest `exit 37` also returned SSH status **37**. The final public delete reached
`deleted`; independently counted Firecracker processes returned to **0**. The temporary edge, client, Headscale, daemon,
supervisor and grant were stopped/revoked. Private credentials and state stay
outside the repository at `/var/tmp/lamp-t18-demo-32qha0mz` on LX.

The final run includes binary SHA-256 hashes. The preceding successful journey
is retained in `lx-demonstration-before-review.jsonl`; the final demonstration
checks the code after exit-status and file-handling adjustments.

The earlier preparation attempt in `/var/tmp/lamp-t18-demo-h9uefo9i` failed to
read a root-owned PID file before creating any sandbox. Its supervisor was
explicitly stopped; the recipe now reads that PID through `sudo -n`. The directory
is retained. No repeated trials or benchmark loops were run.

## Offline demonstration

[`offline-demonstration.jsonl`](evidence/t18/offline-demonstration.jsonl) comes
from a disposable Linux **network namespace with only loopback and no routes**.
The [recipe](evidence/t18/offline-demonstration.py) ran the real `lampd`, access
edge and ordinary OpenSSH there. The authenticated API read fleet `fl_local`,
OpenSSH verified the fleet host CA over direct and WebSocket connections, and
host commands/forwarding were refused. No Tailscale, ACME or tunnel configuration
was present. This demonstrates that local fleet control and console admission
do not need an Internet connection. Its control daemon did not allocate a VM.

## Session adapter and account-dependent routes

There is one added adapter test because no real session backend exists in the
requested base. `TestPublicSessionAdapter` exercises real SSH against a public
HTTP protocol fixture: listing, input-lease acquisition, attach, epoch/sequence
input, revision-bound PTY resize, output and lease release. It passed. This is
not a claim of a real persistent guest session. The implemented guest `shell`
is explicitly a fresh shell per input line, with the last guest exit status
returned to SSH; persistent shells and interactive programs require T21.

The owner-authorized headless Cloudflare procedure was attempted using a scratch
copy of the Air's Cloudflare-only cookies. The copied session reached
`https://dash.cloudflare.com/login`. No DNS, CAA, account or tunnel changes were
made; only an intended `eilmer.si` hostname would be used. The scratch Chrome
was stopped and its profile removed. No credential was printed or committed.
[`plugin-demonstration.jsonl`](evidence/t18/plugin-demonstration.jsonl) separately
records the real confined planner requesting its typed access effect and starting
the real pinned `cloudflared` on M5 (T11's explicit diagnostic soft-limit mode).
Only deliberately invalid disposable credentials were used. The connector was
explicitly killed, the access service reported connector loss with exit 1, and
both processes were cleaned up. This proves plugin/process integration, not a
registered Cloudflare route. An initial version waited for invalid credentials
to fail; cloudflared kept retrying and the wrapper timed out. That private scratch
directory is retained, and its remaining connector was explicitly stopped. The
corrected [recipe](evidence/t18/plugin-demonstration.py) ends its own connector
as soon as the start event arrives.

Without an authenticated Cloudflare session/credential, the named-tunnel and
public-domain demonstrations cannot be performed. The ACME route requires the
owner's DNS and reachable public TCP 443; those prerequisites are not fabricated.

The CI command uses Tailscale's upstream OIDC federation hook, GitHub's job token,
an ephemeral userspace node and an in-memory SSH key. The real Headscale journey
proves the subsequent WhoIs-to-certificate and SSH connection, **not** the GitHub
issuer-to-Tailscale exchange. That exchange needs a separate owner-provisioned
federated identity and tag policy; the owner's main tailnet was not changed.

Setup and operational details are in [access.md](access.md). There are no new
timing targets, trial counts, attack suites, soak runs or E-number gate claims.

## Workspace verification

The required **single invocation of `make verify` passed on M5**: Go/Rust builds
and tests, contract checks, interoperability, generated-file checks, Go vet, Rust
formatting and Clippy. The full output is in
[`make-verify.log`](evidence/t18/make-verify.log). Only focused builds, vet and the
single session adapter test were used while editing.
`docs/spec` and `docs/tickets` are unchanged.

## Single review fixes

Merged `origin/main` at `8e7636e` with a merge commit, retaining the access and
fleet dependency sets. All four review findings were accepted and fixed:

- The reverse proxy restores the checked inbound `Authorization` in its rewrite
  callback, after hop-by-hop stripping. `Connection: Authorization` cannot turn
  a bearer request into a credential-free Unix-socket owner request.
- CI applies the configured SSH negotiation timeout as an actual connection
  deadline, bounded by the caller's earlier deadline, and clears it only after
  successful negotiation. Context cancellation closes that connection throughout
  negotiation, session creation and execution.
- Stream copying selects on cancellation and closes the connection before
  returning, allowing an idle `tailnet-connect` to terminate on SIGTERM.
- Public stdout and stderr frames go to their respective SSH channel writers
  for both the guest command console and the session adapter.

The daemon's explicit `operation not installed` response for session operations
now becomes `sessions are not available yet on this daemon`. Ordinary missing
session resources retain their original errors. No session backend was added.

No tests were added or changed, and no trial or demonstration was rerun for
this review. The single required review `make verify` invocation passed (exit 0),
including builds, existing tests, contracts, interoperability and lint, as recorded in
[`review-make-verify.log`](evidence/t18/review-make-verify.log).
