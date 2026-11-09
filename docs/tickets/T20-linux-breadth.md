# Linux breadth: older distros, arm64, fallback, devices and engine choice

## Goal
Make Lamp's Linux support as wide as release one promises. When this is done: Lamp runs as a host on every Linux lane — Ubuntu 22.04, Debian 12, Fedora 40, the Linux 5.10 minimum and nested arm64 — each with create, full memory fork, local suspend and resume, guard, audit and update/recovery (HST-001; the Linux rows of `host-linux`); callers choose CPU, memory, disk and process limits with real admission and no silent overcommit (SBX-004); requested devices work on Linux guests on `LX` and `LA`, including a GPU row doing useful accelerated work on `LX` (SBX-005, Linux rows); on a host that cannot virtualize, gVisor runs as the clearly labeled weaker fallback on lane `NV`, refusing secrets by default and every public-repository CI job (SBX-006, Linux rows); and each host picks the fastest measured, qualified engine for a request, with security, OS, devices and capture as conditions speed can never override (HST-006).

Release one names Linux x86_64 and arm64 as hosts with Linux 5.10 as the floor, and the owner wants certification to be honest: a row is certified only when it was really run on its lane.

## Context
- Ticket T20 of 31; `docs/tickets/README.md` lists them all and what each waits for. The formats are frozen (T14); this builds on T7's capture proof and T8's `msb_krun` adapter. Five tickets run at the same time: T19 (lifecycle and storage), T21 (sessions and consoles), T22 (kits), T23 (budgets, audit, revocation) and T24 (hosted side). This ticket owns the Linux engine adapters beyond the reference, Linux devices, the gVisor fallback and engine selection. T15 did the Mac rows of SBX-005 and SBX-006 and owns the shared device code this builds on. T28 (install and update) starts from its result.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- Lane `LA` is a Linux arm64 VM on the M5 Mac.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.

## Constraints
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Nested lanes never count as speed evidence.
- On both Macs, never put a password or approval dialog on the owner's screen. `sudo` itself is fine: the owner said on 2026-11-09 to use it freely, including to install Lamp's privileged service and to run sleep and wake tests.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
