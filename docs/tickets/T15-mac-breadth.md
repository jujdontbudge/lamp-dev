# Mac devices and fallback

## Goal
Finish what Macs must do beyond the two guest classes. When this is done, on the M5 and the Air: Linux guests and native macOS guests get a display, audio input, audio output, keyboard, mouse and extra disk only when asked, with real device I/O and useful GPU acceleration on exposed hardware (SBX-005). When Lamp's access to virtualization is denied, native tasks run in the restricted App Sandbox process fallback, clearly labeled weaker, refusing logins and protected connections by default and refusing every public-repository CI job (SBX-006). The Air admits only what fits in its 8 GB and rejects the rest before any work starts.

Agents that drive real apps need a screen, sound and input, and the owner requires the weaker fallback to be labeled honestly so nobody mistakes it for VM isolation.

## Context
- Feature prerequisites: T8. [README.md](README.md) and [map.html](map.html) show the same dependencies. Shared interfaces may allow work to overlap.
- This ticket starts on the M5 Mac. The MacBook Air (M2, 8 GB, fanless) is reached with `ssh owner-air`. All three are the owner’s everyday machines.
- The M5's login keychain holds a "Developer ID Application" signing identity and no "Developer ID Installer" one. The Air holds only an "Apple Development" identity. The owner is signed in to the Apple Developer Program on both, and said on 2026-11-09 that the build may create the missing signing certificates itself.
- Land one PR from this ticket’s own branch and checkout. The coordinating session stays on `main`, reviews the PR and merges it. Follow `<owner-skills>/file-pr/SKILL.md`.

## Constraints
- Build the features and show one real end-to-end demonstration covering this ticket’s goal. Add almost no tests: only where the feature cannot be shown working another way. Run only what iteration needs; speed measurements happen once at release and never block work or release.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Nothing of the owner's is passed into a guest: not their desktop, microphone, camera, clipboard or folders. Use virtual or dedicated test devices.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.
- Keep the owner’s services running and leave existing Docker containers, images and networks alone. On the Ubuntu laptop, allow at most 2 live Lamp sandboxes across all tickets: check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available before creating one; release it promptly.
- On Macs, never run commands that ask for a password or approval dialog. Do not drive the owner’s visible screen, apps or browser. Use disposable state and public third-party projects for the demonstration, never the owner’s projects or real vault items.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
