# Freeze the template and saved-state formats

## Goal
Decide on evidence that Lamp's template format and saved-state format are safe to freeze, and freeze them. When this is done, the results of E04 (storage and capture on Linux), E05 and E06 (Linux and macOS guests on Macs) and E07 (subscription sign-in custody) have been checked against their pass thresholds; `lamp.template/v1` and the saved-state compatibility keys are fixed and versioned; and a written decision records what was frozen, on which evidence, and what stays open. A format whose gate has not passed is not frozen.

The owner's rule (ENG-001) is that storage, native guests and credential capture are proved before the state format is frozen, so that early code does not lock in a capture contract nobody has qualified. Lifecycle and storage (T19), Linux breadth (T20) and sessions (T21) wait on this, and through them most of the product.

## Context
- Ticket T14 of 31; `docs/tickets/README.md` lists them all and what each waits for. T7, T8 and T9 have landed with their evidence. Four tickets run at the same time: T15 (Mac devices and fallback), T16 (fleet), T17 (broker) and T18 (access). This ticket owns the format versions and the decision record. T19, T20 and T21 start from its result.
- This ticket lands as one pull request into `main` on GitHub (`JohanSardar/lamp`, private; `gh` on the M5 is signed in as that account). The owner's pull request conventions are in `/Users/johansardar/.claude/skills/file-pr/SKILL.md`. The owner's coordinating session stays on `main`: it watches the pull request, sends back failed checks and review findings for this ticket to fix, and merges it once the checks pass.

## Constraints
- The owner's thresholds are fixed: none changes after a failed run, and a failed run stays on record as failed (ENG-001).
- Commit only to this ticket's own branch in its own checkout, push it, and open the pull request yourself. Never push to `main` or to another ticket's branch (owner, 2026-11-09).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
