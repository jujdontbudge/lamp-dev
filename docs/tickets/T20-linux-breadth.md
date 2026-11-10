# Linux support and devices

## Goal
Support Linux x86_64 and arm64 hosts at Linux 5.10+, including Ubuntu 22.04+, Debian 12+ and Fedora 40+. Linux sandboxes provide create, memory fork, durable suspend/resume, guard and audit. Callers choose CPU, memory, disk, process limits and requested devices, including GPU acceleration where available; admission respects host capacity without silent overcommit. Hosts without virtualization use the clearly labeled weaker gVisor fallback, refusing secrets by default and all public-repository CI jobs. Engine selection respects security, OS, devices and capture requirements while choosing the fastest supported option (HST-001, HST-006, SBX-004–006). People should be able to use their existing Linux machines.

## Context
- Feature prerequisites: T7, T8, T15. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
