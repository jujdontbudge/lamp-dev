# Plugin host

## Goal
Build the way anyone extends Lamp without being able to hurt it. When this is done, a separately run plugin in any language speaks `lamp.plugin/1` to the host, declares its type, permissions, limits and digest in a manifest, and runs under its trust class: confined (WASI, or a native program in a service sandbox, with typed grants only), credential, platform, or engine-trusted after an explicit owner warning. A plugin that crashes, hangs or floods loses its grants and takes down only the work that depends on it (PLG-001; architecture §13). One versioned contract covers every public plugin type: engine, network, credential source, harness, kit, CI, trigger, console, access and notification sink (PLG-002).

Lamp's adapters for CI systems, consoles, password managers, harnesses, access methods and network exits are all plugins, so most later tickets plug into what this one builds: the broker (T17) and access (T18) next, then sessions, kits, CI and notifications.

## Context
- Feature prerequisites: T1. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh owner-air`. All three are the owner’s everyday machines.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `<owner-skills>/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
