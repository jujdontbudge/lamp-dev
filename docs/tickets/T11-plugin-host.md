# Plugin host

## Goal
Build the way anyone extends Lamp without being able to hurt it. When this is done, a separately run plugin in any language speaks `lamp.plugin/1` to the host, declares its type, permissions, limits and digest in a manifest, and runs under its trust class: confined (WASI, or a native program in a service sandbox, with typed grants only), credential, platform, or engine-trusted after an explicit owner warning. A plugin that crashes, hangs or floods loses its grants and takes down only the work that depends on it (PLG-001; architecture §13). One versioned contract covers every public plugin type: engine, network, credential source, harness, kit, CI, trigger, console, access and notification sink, each shown working with a reference plugin (PLG-002).

Lamp's adapters for CI systems, consoles, password managers, harnesses, access methods and network exits are all plugins, so most later tickets plug into what this one builds: the broker (T17) and access (T18) next, then sessions, kits, CI and notifications.

## Context
- Ticket T11 of 31; `docs/tickets/README.md` lists them all and what each waits for. T6 ran the engine gate and recorded it as failed on speed (`docs/development/T6-evidence.md`): creates met the target, but forks, compiling and network did not, and the replicated storage rows are not built yet. The owner saw those numbers and on 2026-11-10 accepted the result and said to build everything else. Six tickets run at the same time: T7 (storage proof), T8 (Mac guest classes), T9 (sign-in custody), T10 (control core, which owns `lampd`'s operations and authorization), T12 (network) and T13 (proof suite). This ticket owns the plugin protocol, the plugin runners and the manifest. T17 and T18 start from this ticket's result and T10's.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.

### Live verification
The deliverable is a host library, not a new console/UI surface. Drive its real
public API with separately executed workers (no mocked process confinement):
- New plugin author: package/admit an exact executable+manifest, negotiate, grant
  one useful effect for each of the ten types; reject additional authority.
- Established owner: validate immutable Adapter/Configuration references, refuse
  changed packages/schemas/permissions and require exact engine warning approval.
- Mid-task user: crash/hang/flood or revoke a worker, verify external grant
  revocation/dependent cleanup, then continue using an unrelated worker.
- Hostile author: probe actual filesystem, TCP, Unix IPC, non-CLOEXEC descriptors,
  environment and subprocess escape; forge/replay effect bindings.

Results, reproducible commands and qualification gaps are recorded in
[`../development/T11-evidence.md`](../development/T11-evidence.md). Passing the
reference fixtures alone is not a full PLG-001/002 release certification.
