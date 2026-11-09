# Install, update, recovery and SDK packages

## Goal
Make Lamp something a person can install, update and remove safely on their own machines. When this is done: signed packages install, restart and uninstall cleanly on every lane (the Ubuntu laptop, its nested Linux lanes, the M5 and the Air), and uninstall never silently deletes the owner's data (PRD-002); automatic updates start disabled, update one host at a time, verify release trust, preserve active and saved state, and stop and roll back on failure (HST-005); every reused component has a recorded reason, pinned provenance and a maintenance owner, with a release SBOM (ENG-002); TypeScript and Python SDKs are generated from the canonical schema and packaged for release (INT-002, O1); and E14 passes, including restoring encrypted data onto a clean replacement OS without the original key store.

Lamp is self-hosted, so installing and updating it on your own laptop has to be as safe as the sandboxes are. Owning your machines and data is one of its four equal goals.

## Context
- Ticket T28 of 31; `docs/tickets/README.md` lists them all and what each waits for. This builds on T15's Mac work, T16's fleet and T20's Linux lanes. Three tickets run at the same time: T25 (harness catalogue), T26 (CI core and GitHub) and T27 (dashboard). This ticket owns packaging, update trust and the SDK packages. T30 certifies it and T31 publishes.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The M5 Mac (lane `M5`) is the machine this ticket starts on. The MacBook Air (lane `AIR`, 8 GB, no fan) is `ssh johansardars-macbook-air` from it. `sudo -n` works without a password on both. Both are the owner's everyday machines and run the owner's own apps.
- The M5's login keychain holds a "Developer ID Application" signing identity and no "Developer ID Installer" one. The Air holds only an "Apple Development" identity. The owner is signed in to the Apple Developer Program on both, and said on 2026-11-09 that the build may create the missing signing certificates itself.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.

## Constraints
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Nothing is published to npm, PyPI or any other public place here. T31 does that.
- Never drive the owner's visible screen, apps or browser windows. Headless tools only; the owner is working on these machines.
- Never print, log or commit a secret.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
