# Independent oracles and benchmark harness

## Goal
Build the measuring side of Lamp's engine gate, independent of the code it judges. When this is done, one harness drives Lamp only through its public CLI and API and can run: E01 (create and fork timing on the standard sandbox, with the required warm-ups, trial counts and dirty-memory rows); E02 (paired secured-versus-native compute, disk and network ratios); the fork oracles (memory-marker continuity, independent writes, both sides still running, zero state errors); and the E04 storage and capture oracle (generation-pattern memory and disk checksums against an external acknowledgement history, crashes at each commit point, and a deliberately omitted flush that the oracle must catch). Every run records the evidence fields architecture §22 requires. Covers QA-003.

The owner's targets mean nothing if the thing measuring them shares the implementation's assumptions; QA-003 says serializer agreement alone must not prove correctness. T6 uses this harness to decide whether the rest of the product may be built, and T7 uses its storage oracle before the formats are frozen.

## Context
- Ticket T5 of 31; `docs/tickets/README.md` lists them all and what each waits for. T1 delivered the repository layout and the contracts. Three tickets run at the same time, each in its own checkout, building what this measures: T2 the Linux engine, T3 the minimal `lampd` and CLI, T4 the host guard and audit collectors. T6 then joins the four and runs the gate. This ticket owns the oracles, benchmark workloads and evidence records. After the gate, T13 grows this into the journeys, the attack suite and the every-change benchmarks.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Power-loss and write-reordering faults run only on disposable fixtures, never on the laptop's live disk.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
