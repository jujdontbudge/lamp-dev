# Linux engine: create a sandbox and fork it with its memory

## Goal
Build the Linux engine the whole product stands on. When this is done, on the Ubuntu laptop a Lamp template built without Docker boots as a microVM and runs a real command, and a running sandbox can be forked with its memory so that the original and the fork both keep running independently, each with its own disk, identity and entropy (architecture §§5–6; SBX-001, SBX-007, TPL-001). It is built to meet the owner's hard targets on the standard sandbox (2 vCPU, 4 GiB memory, 20 GiB disk, up to 1 GiB of recently written memory): typical create under 500 ms from the caller's request to a running command, and every memory fork under 1000 ms.

The owner's rule is engine first (ENG-001): no other product work starts until create and fork work within those targets, and the repository stays private until they do. Fast create and live fork are what let agents work in parallel and try two approaches from the same unfinished state.

## Context
- Ticket T2 of 31; `docs/tickets/README.md` lists them all and what each waits for. T1 delivered the repository layout and the `lamp.worker/1` contract. Three tickets run at the same time, each in its own checkout: T3 builds the minimal `lampd` and `lamp` CLI that call this engine over `lamp.worker/1`; T4 builds `lamp-guard` and the audit collectors that attach to this engine's sandboxes; T5 builds the independent oracles and benchmarks that will judge it. T6 then joins the four and runs the gate. This ticket owns `lamp-supervisor`, `lamp-engine`, `lamp-init`, the guest helper, the seed and the template build.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.

## Constraints
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- The saved-state format is not frozen here. T7, T8 and T9 prove storage, Mac guests and sign-in capture first, and T14 freezes it.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
