# Mac breadth: devices and the no-virtualization fallback

## Goal
Finish what Macs must do beyond the two guest classes. When this is done, on the M5 and the Air: Linux guests and native macOS guests get a display, audio input, audio output, keyboard, mouse and extra disk only when asked, each proved with real device I/O, and a GPU row does useful accelerated work on real exposed hardware (SBX-005; the Mac rows of `device-basic` and `device-physical`). When Lamp's access to virtualization is denied, native tasks run in the restricted App Sandbox process fallback, clearly labeled weaker, refusing logins and protected connections by default and refusing every public-repository CI job (SBX-006; the Mac rows of `fallback`). The Air admits only what fits in its 8 GB and rejects the rest before any work starts.

Agents that drive real apps need a screen, sound and input, and the owner requires the weaker fallback to be labeled honestly so nobody mistakes it for VM isolation.

## Context
- Ticket T15 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T8's Mac engine adapters. Four tickets run at the same time: T14 (format freeze), T16 (fleet), T17 (broker) and T18 (access). This ticket owns the Mac device paths and the Mac process fallback. T20 does the Linux rows of the same two requirements and reuses this ticket's shared device code; T28 packages the Mac install.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The M5's login keychain holds a "Developer ID Application" signing identity and no "Developer ID Installer" one. The Air holds only an "Apple Development" identity. The owner is signed in to the Apple Developer Program on both, and said on 2026-11-09 that the build may create the missing signing certificates itself.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Nothing of the owner's is passed into a guest: not their desktop, microphone, camera, clipboard or folders. Use virtual or dedicated test devices.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
