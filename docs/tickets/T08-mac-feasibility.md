# Mac guest engines

## Goal
Run Linux and native macOS guests on Apple Silicon Macs with macOS 26+. Linux guests fork with memory and both copies continue independently, using APFS storage and confined engine workers. Native macOS guests use owner-built media and supported suspend/resume. Where supported APIs cannot fork native macOS memory, offer a clearly labeled disk-only fresh boot (A2). Enforce the two-native-macOS-allocation limit per Mac, including retained resumable state. This makes Macs usable as first-release fleet hosts.

## Context
- Feature prerequisites: T2. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh johansardars-macbook-air`. All three are the owner’s everyday machines.
- The M5's login keychain holds a "Developer ID Application" signing identity and no "Developer ID Installer" one. The Air holds only an "Apple Development" identity. The owner is signed in to the Apple Developer Program on both, and said on 2026-11-09 that the build may create the missing signing certificates itself.
- The owner agreed on 2026-11-09 to Apple's macOS licence being accepted in their name for guest VMs.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `/Users/johansardar/.claude/skills/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
