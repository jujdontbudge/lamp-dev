# T22 — kits evidence

Status: **partial; do not mark the whole ticket done.** The browser/KeePassXC
journey is real. Containerd's guest runtime-directory fix is demonstrated.
Docker container execution and three native vault providers remain unproven.
No benchmark, trial-count, soak or attack gate was used as a completion target.

## Real demonstrations

The controller/native KeePassXC reader ran on the owner's M5 Mac; browsers and
toolchain work ran in disposable Firecracker VMs on `ubuntu-server` (LX).
The site was the public third-party `https://the-internet.herokuapp.com`, using
its advertised disposable login, not an owner's account/project/profile.
The script created a fresh KeePassXC database and removed its disposable unlock
material afterward. No vault password, session cookie, API token or private
screenshot is included in the committed evidence.

| Goal | Observed result | Evidence |
| --- | --- | --- |
| KIT-001 external browser default | Separate agent/browser VMs; native vault login succeeds outside the agent VM | [browser journey](evidence/t22/browser-journey.jsonl), steps 1–11 |
| KIT-002 copied login opt-in | Missing consent rejected; explicit warning returned; real authenticated session copied into the agent VM | browser journey, steps 13–16 |
| Copied mode's actual exposure | Agent code reads the deliberately copied disposable cookie; private takeover refused | [copied boundary](evidence/t22/copied-boundary.jsonl), steps 3–7 |
| KIT-003 private control/handback | Task fenced; owner receives encrypted private PNG; clean `about:blank` handback retains authenticated HttpOnly session; generic private typing succeeds | browser journey, steps 6–12 |
| KIT-004 toolchains | Git request returns a guest prefix and executes `git version 2.53.0`; explicitly reported as template reuse, not a new online install | [toolchain journey](evidence/t22/toolchain-journey.jsonl), steps 1–5 |
| KIT-004 containerd preparation | New guest PID 1 creates `/run/containerd` as `1777`; normal public exec remains mapped `0 -> 65537`; no `/var/run/docker.sock`; containerd starts | [boot fix](evidence/t22/docker-boot-fix.jsonl), [native startup diagnostic](evidence/t22/docker-startup-failure.jsonl) |
| KIT-005 KeePassXC | Owner reads authoritative vault/item/origin/submit plan; one approval causes real native read, sealed fill and successful form submission | browser journey, steps 4–11 |
| KIT-005 1Password/Bitwarden/Proton Pass | Native adapter code only; isolated real sessions/account setup and provider-specific no-prompt behavior not demonstrated | **Not done** |
| KIT-004 Docker container | Original daemon startup fails; nested user/mount namespace correction compiles but its live attempt was refused before allocation at the owner's cap | **Not done**; [capacity refusal](evidence/t22/docker-capacity-block.jsonl) |

The full browser journey exited **1 at its later Docker step**. The successful
browser sections are not a passing claim for the whole script/ticket. The
single-slot transcript also retains its failed tool-install calls. A later
strengthened password-typing journey failed during public-site navigation before
that action; [its transcript/log](evidence/t22/later-navigation-failure.jsonl)
are retained. The earlier generic private typing result is not proof that this
later password-specific variant ran.

## Containerd and Docker investigation

Containerd 2.2.2 intentionally creates its default `/run/containerd` even when
root/state/address are overridden, because shim sockets/FIFOs use that path
([upstream source](https://github.com/containerd/containerd/blob/v2.2.2/cmd/containerd/server/server.go)).
The mapped task cannot create it under guest-root-owned `/run`. Changing only a
build-time tmpfs would disappear on the next template boot, so this PR adds the
sticky directory in Linux guest PID 1, not on the laptop or via a privileged task
helper. The rest of `/run`, the task UID mapping and the guard remain unchanged.

The changed `lamp-init` was built natively on LX, installed into a **new T22-only
seed clone**, imported and captured through the normal template builder:

- Template: `tp_93b5cf5b7c7951594746d93a8a`.
- Guest-init raw SHA-256: `e6dbd8ccb58932160c628db305012083a0fabff7a92ad1a6c9a83602f2e9016c`.
- Rootfs SHA-c4m: `25ceb7806f6f57e71ffaff3eb4dafd241c9da82513a5964d78f0d169e41d4c2d`.
- Template-build admission: 0 Firecrackers, 27,672 MiB available.
- First restored-guest admission: 0 Firecrackers, 27,483 MiB available.
- The restored guest reports mode `0o1777`, UID map `0 65537 1`, and no host Docker socket.

Containerd then starts. Docker 29.1.3 instead panics during plugin-manager
initialization; its deferred shutdown dereferences an uninitialized events
object and obscures the initial error. Its log also shows an unsuccessful mount
propagation attempt. The [pinned source](https://github.com/moby/moby/blob/docker-v29.1.3/daemon/daemon.go#L1045-L1057)
matches the stack in the committed diagnostic.

The subsequent correction puts only the Docker job in a nested single-UID
user/mount namespace using unprivileged `unshare`, removes the incorrect
RootlessKit indicator, and publishes an explicit failed job if namespace
bootstrap fails. It does not add a root helper, widen the guard UID mapping,
attach host Docker, or claim a running RootlessKit service. Both generated code
branches compile. The live invocation was rejected **before creating a VM**:
`OWNER_SANDBOX_CAPACITY`; two other tickets' Firecrackers occupied the cap.
This correction therefore has **no successful live Docker/container result yet**.

## Commands and outcomes

| Command | Outcome |
| --- | --- |
| `make bootstrap` | PASS, exit 0 |
| `make verify` | PASS, exit 0; run once during implementation, before the later narrow init/namespace changes; [saved output](evidence/t22/make-verify.log) |
| `cargo fmt -p lamp-init -- --check` | PASS, exit 0 after the guest-init change |
| LX: `cargo build --locked -p lamp-init --bin lamp-init` with `/home/johan/.cargo/bin` on PATH | PASS, exit 0; [build tail](evidence/t22/guest-init-build.log) |
| `python3 -m py_compile kits/*.py tools/kits_*.py` and `python3 -m kits --help` | PASS, exit 0 on the final Python sources |
| Compile generated Git and Git+Docker launcher/runner/installer layers with Python `compile` | PASS, exit 0; syntax proof only, not a fake runtime/provider test |
| `PYTHONPATH=. python3 tools/kits_docker_demo.py --fixture /home/johan/lamp-t22/host-6 --root /home/johan/lamp-t22/docker-native-demo-1` | FAIL, exit 1; guest init/containerd work, Docker daemon exits; public release and fixture stop succeed; physical count 0 |
| Same journey with fresh `host-7` / `docker-native-demo-2` | FAIL, exit 1; captures native Docker panic; releases VM; count returns to its pre-allocation value 1 |
| Same journey with fresh `host-8` / `docker-native-demo-3` after namespace correction | BLOCKED, exit 1, `OWNER_SANDBOX_CAPACITY`; no VM created; fixture stopped; [log](evidence/t22/docker-capacity-block.log) |

Initial isolated native builds also failed before compilation because Cargo was
not on the SSH PATH (127), then because `tools/protogen` was missing from the
copied workspace (101). Both were corrected before the successful native build.
No full test suite, lint suite, trial/benchmark loop or timing objective was run.

## Cleanup incident and final resource state

The coordinator reported the old T22 fixture `sb-e7db0b9e1e0eee5544527aa219cfa039`
still live after a failed delete. A held cleanup command had not executed, and a
subsequent public delete failed; the control record's `failed` state was not
physical release. Only the verified T22 supervisor/store was sent SIGTERM.
`pgrep -c firecracker` dropped from 2 to **1 before another allocation**.
This was a cleanup failure, not a passing lifecycle result.

The fixture now handles SSH hangup/SIGTERM, stops at its existing worker-grant
expiry, and attempts worker shutdown even if an earlier teardown stage raises.
The dedicated Docker journey always stops its own verified fixture controller,
including after an unknown create/delete outcome. Its actual successful public
releases and SSH-hangup teardown are in the transcripts above.

At the final captured refusal, both remaining host Firecrackers had UID bases
`1940000000` and `2100000000`, not T22's `1850000000`; available RAM was 25,179
MiB. T22 had no live VM/controller. No other ticket's process, owner Chrome,
Beeper, T3 Code, or existing host Docker resource was stopped or managed. Failed
run directories and the original failed control record were retained.

## Remaining work

- With one LX slot available, start a **fresh** owned fixture using the prepared
  template above, then run `tools/kits_docker_demo.py` once against it. The script
  performs admission immediately before create, imports public BusyBox, attempts
  a real isolated container, and releases/stops its fixture on exit. Do not reuse
  an existing output directory or stop another ticket to obtain the slot.
- Demonstrate 1Password, Bitwarden and Proton Pass against disposable real vaults
  and native isolated sessions; do not substitute the owner's profiles/items or
  mock a provider. Account setup is not implemented by these adapters.
- Canonical `lamp`/MCP browser registration, readiness attachment and dashboard UI
  are not wired by this standalone companion. Agent credentials must not grant
  exec/read/capture access to the external browser VM.
- Arbitrary online package installation, every named language preset, non-root
  Docker image users, networking, MFA/passkeys and arbitrary website secret
  noninterference have not been demonstrated. Unsupported forms require private
  owner typing; no universal hiding/redaction proof is claimed.

See [operator instructions](T22-kits.md). The source hash manifest describes the
final checked-in implementation, not the historical binaries for every earlier
failed journey.
