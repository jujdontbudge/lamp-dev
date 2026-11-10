# Install, update and SDKs

## Goal
Make Lamp safe to install, update, recover and remove on supported Linux and Mac hosts. Signed packages install and restart normally; uninstall offers explicit choices and never silently deletes owner data. Automatic updates start disabled, roll one host at a time, verify release trust, preserve active and saved work, and stop or roll back on failure. Reused components follow the owner's security-or-speed reuse rule and retain their licenses. TypeScript and Python SDKs are generated from the canonical schema and packaged for release. Encrypted data can be restored on a replacement OS with the owner's recovery material rather than relying on the lost original key store (PRD-002, HST-005, ENG-002, INT-002). This makes ownership practical beyond the first installation.

## Context
- Feature prerequisites: T15, T16, T20. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- The Ubuntu laptop is reached with `ssh ubuntu-server` from the M5. It also runs the owner’s Chrome, Beeper and T3 Code services; leave them and Docker’s existing resources alone.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh johansardars-macbook-air`. All three are the owner’s everyday machines.
- The M5's login keychain holds a "Developer ID Application" signing identity and no "Developer ID Installer" one. The Air holds only an "Apple Development" identity. The owner is signed in to the Apple Developer Program on both, and said on 2026-11-09 that the build may create the missing signing certificates itself.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Nothing is published to npm, PyPI or any other public place here. T31 does that.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
