# Storage and capture proof on Linux

## Goal
Prove that Lamp's disk and memory capture can be trusted, before its formats are frozen. When this is done, E04 passes on the Ubuntu laptop: dirty memory-and-disk forks with flush, discard, rotation, compaction and crashes at each commit boundary, on real ext4 and on disposable XFS-reflink and btrfs test filesystems, at least 100 on each (owner decision, 2026-11-10 20:50Z: the earlier 1,000-fork floor came from the spec rounds, not from the owner; the ext4 run already passed at 1,000 and stands as the binding Linux run); a deliberately omitted barrier is caught; a validly hashed capture from an unauthorized producer is rejected; and there are zero mixed generations, cross-fork writes, lost ancestors or lost acknowledged durable bytes. Durable (`local`) fork and suspend/resume of the standard sandbox work on ordinary ext4 without asking anyone to reformat, and every durable standard fork stays under 1000 ms (architecture §6.4 and §7).

Everything people keep in Lamp rests on this: suspended work, forks, templates and volumes. The owner's rule (ENG-001) is that storage feasibility is tested before the state format is frozen, and T14 freezes the formats on this ticket's evidence.

## Context
- Ticket T7 of 31; `docs/tickets/README.md` lists them all and what each waits for. T6 ran the engine gate and recorded it as failed on speed (`docs/development/T6-evidence.md`): creates met the target, but forks, compiling and network did not, and the replicated storage rows are not built yet. The owner saw those numbers and on 2026-11-10 accepted the result and said to build everything else. Six tickets run at the same time: T8 proves the Mac guest classes and implements this same capture contract on APFS; T9 proves subscription sign-in custody; T10 builds the control core; T11 the plugin host; T12 the network; T13 the proof suite, which now maintains the harness and its storage oracle. This ticket owns Linux storage and capture. T14 freezes the formats after T7, T8 and T9; T19 and T20 build on this.
- The Ubuntu laptop (lane `LX`) is `ssh ubuntu-server` from the M5 Mac, and `sudo -n` works there. The owner's own services also run on it: mainly a Chrome service (`excerlock-dm-chrome.service`, a user systemd unit) that held about 20 of its 30 GiB of memory on 2026-11-09, plus Beeper and a T3 Code server. No Docker containers were running.
- Hard timing runs on the Ubuntu laptop need it otherwise quiet. Other tickets build and test there at the same time, and the owner's Chrome service holds most of its memory.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- The owner's own services on the Ubuntu laptop may be stopped only for a hard timing run, and are started again as soon as the run ends (owner, 2026-11-09). The T3 Code server there stays up. Never use, change or remove Docker's existing containers, images or networks on that machine.
- Power-loss and write-reordering faults run only on disposable fixtures, never on the laptop's live disk.
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
