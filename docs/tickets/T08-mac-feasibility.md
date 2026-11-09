# Mac hosts: Linux guests with memory fork, and native macOS guests

## Goal
Prove on the M5 Mac and the MacBook Air that both Mac guest classes work, before the formats are frozen. When this is done: (E05) a Linux guest runs on each Mac and forks with its full memory, both sides continuing, across at least 1,000 full-dirty, in-flight-I/O captures per certified row with zero state, clock or isolation errors, inside the confinement architecture §3.2 requires, with APFS storage passing the same capture oracle as Linux; and (E06) a native macOS 26 guest built from the owner's own media runs on each Mac, suspends and resumes across at least 100 trials with independent disks and identities, the two-per-Mac limit holds, and where no supported memory fork exists the clearly labeled disk-only creation is offered instead (A2). Timings are published as measurements. Covers SBX-002, SBX-003, E05 and E06.

Apple Silicon Macs are first-release hosts; journey 2 is adding a Mac to a Linux fleet. The owner's rule (ENG-001) is that native-guest feasibility is tested before the state format is frozen, and T14 freezes the formats on this ticket's evidence.

## Context
- Ticket T8 of 31; `docs/tickets/README.md` lists them all and what each waits for. The engine gate (T6) has passed. Six tickets run at the same time: T7 proves storage and capture on Linux and owns the capture contract; T9 proves subscription sign-in custody; T10 builds the control core; T11 the plugin host; T12 the network, which adds the Mac guard path on this ticket's adapter; T13 the proof suite. This ticket owns the Mac engine adapters, Mac confinement and the APFS storage path. T15 later adds devices and the no-virtualization fallback on Macs, and T20 builds the Linux `msb_krun` candidate on this ticket's adapter.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The M5's login keychain holds a "Developer ID Application" signing identity and no "Developer ID Installer" one. The Air holds only an "Apple Development" identity. The owner is signed in to the Apple Developer Program on both, and said on 2026-11-09 that the build may create the missing signing certificates itself.
- The owner agreed on 2026-11-09 to Apple's macOS licence being accepted in their name for guest VMs.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
