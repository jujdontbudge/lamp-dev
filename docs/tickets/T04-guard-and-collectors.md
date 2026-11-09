# Host network guard and audit collectors for the demo

## Goal
Build `lamp-guard` and the process and connection collectors far enough that the engine demo runs with its protections switched on. When this is done, a Linux sandbox's traffic leaves only through the host guard under the default policy (public internet open; private, LAN and metadata ranges blocked), guest root cannot change that, and every process start and every connection is recorded from outside the task's control (NET-001; architecture §8 and §17.1).

The owner's speed targets only count with security on. E02 requires compute, disk and network each to stay at or above 0.97 of native with guard, audit, copy-on-write and capture tracking enabled, and says disabled controls cannot rescue a result. Protecting the host and its secrets is one of Lamp's four equal goals.

## Context
- Ticket T4 of 31; `docs/tickets/README.md` lists them all and what each waits for. T1 delivered the repository layout and the contracts. Three tickets run at the same time, each in its own checkout: T2 builds the Linux engine these attach to; T3 builds the minimal `lampd` and CLI; T5 builds the independent oracles and benchmarks. T6 then joins the four and runs the gate. This ticket owns `lamp-guard` and the collectors. After the gate, T12 builds exits, private networks, dev URLs and allow/deny rules on the guard, and T23 builds the audit ledger and export on the collectors.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- The owner agreed on 2026-11-09 to package, firewall and kernel-setting changes on the Ubuntu laptop, including reboots.
- All three machines reach each other only over the owner's Tailscale network.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
