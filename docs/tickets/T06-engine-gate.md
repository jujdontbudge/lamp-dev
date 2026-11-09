# Engine gate: the Linux demo meets the owner's targets

## Goal
Join the Linux engine (T2), the demo control path (T3), the guard and collectors (T4) and the harness (T5), and get the engine gate to pass on the Ubuntu laptop. When this is done, E01 and E02 pass on lane `LX` with recorded evidence: typical create under 500 ms from the caller's request to a running command; every memory fork under 1000 ms with the source still running and the fork useful; zero state errors; each secured compute, disk and network measure at or above 0.97 of native; and no Docker anywhere in the path. Covers ENG-001, BEN-001, BEN-002, BEN-003, E01 and E02.

This is the owner's first rule: a working Linux create and memory fork within targets comes before any other product work, and the repository stays private until this demo succeeds. Twenty-five tickets wait on this result.

## Context
- Ticket T6 of 31; `docs/tickets/README.md` lists them all and what each waits for. T2, T3, T4 and T5 have landed. Nothing else runs during this ticket. It may change any of their code to make the gate pass. T7–T13 start from its result.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Nested lanes never count as speed evidence.
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Passing this gate does not make the repository public. That is the owner's call.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
