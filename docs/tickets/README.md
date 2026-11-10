# Lamp build tickets

These 31 tickets turn the agreed spec (`docs/spec/architecture.md`, `docs/spec/requirements.md`) into the first release. Each ticket is the complete prompt for one agent and is handed over exactly as written. [map.html](map.html) draws the same plan.

Planned 2026-11-09. The build started the same day.

## Tickets

"Waits for" lists only real dependencies: the ticket edits the same code, or needs the other's result.

| Step | Ticket | Covers | Waits for | Built by |
| --- | --- | --- | --- | --- |
| 1 | [T1 Skeleton and contracts](T01-skeleton-and-contracts.md) | S1 | nothing | B |
| 2 | [T2 Linux engine](T02-linux-engine.md) | SBX-001, SBX-007, TPL-001 | T1 | A |
| 2 | [T3 Demo control path](T03-demo-control-path.md) | S2 | T1 | C |
| 2 | [T4 Guard and collectors](T04-guard-and-collectors.md) | NET-001, S3 | T1 | D |
| 2 | [T5 Oracles and benchmarks](T05-oracles-and-benchmarks.md) | QA-003 | T1 | E |
| 3 | [T6 Engine gate](T06-engine-gate.md) | ENG-001, BEN-001–003, E01, E02 | T2, T3, T4, T5 | A |
| 4 | [T7 Storage proof](T07-storage-proof.md) | E04 | T6 | A |
| 4 | [T8 Mac feasibility](T08-mac-feasibility.md) | SBX-002, SBX-003, E05, E06 | T6 | A |
| 4 | [T9 Sign-in custody](T09-sign-in-custody.md) | CRED-003, CRED-004, E07 | T6 | E |
| 4 | [T10 Control core](T10-control-core.md) | INT-001, INT-003–005, INT-008, ACC-006–008, ENG-003, E03 | T6 | B |
| 4 | [T11 Plugin host](T11-plugin-host.md) | PLG-001, PLG-002 | T6 | C |
| 4 | [T12 Network](T12-network.md) | NET-002–006, E09 | T6 | D |
| 4 | [T13 Proof suite](T13-proof-suite.md) | QA-001, QA-002, BEN-004, BEN-005 | T6 | E |
| 5 | [T14 Format freeze](T14-format-freeze.md) | S4 | T7, T8, T9 | A |
| 5 | [T15 Mac breadth](T15-mac-breadth.md) | SBX-005 and SBX-006 (Mac rows) | T8 | E |
| 5 | [T16 Fleet](T16-fleet.md) | HST-002–004, E10 | T10 | B |
| 5 | [T17 Broker](T17-broker.md) | CRED-005, CRED-006, CON-001–003 | T10, T11 | C |
| 5 | [T18 Access and SSH](T18-access-and-ssh.md) | ACC-001–004, SES-003 | T10, T11 | D |
| 6 | [T19 Lifecycle and storage](T19-lifecycle-and-storage.md) | LIF-001–003, LIF-005, LIF-006, STO-001–005, TPL-002, TPL-003 | T14, T10 | A |
| 6 | [T20 Linux breadth](T20-linux-breadth.md) | HST-001, HST-006, SBX-004, SBX-005 and SBX-006 (Linux rows) | T14 | D |
| 6 | [T21 Sessions and consoles](T21-sessions-and-consoles.md) | INT-006, INT-007, SES-001, SES-002, SES-004–007 | T14, T17 | E |
| 6 | [T22 Kits](T22-kits.md) | KIT-001–005, E08 | T17 | C |
| 6 | [T23 Budgets and audit](T23-budgets-and-audit.md) | OPS-002, OPS-004, OPS-006, OPS-008, OPS-009, ACC-009, E13 | T16, T17 | B |
| 6 | [T24 Hosted side](T24-hosted-side.md) | ACC-005, RLY-001, RLY-002, OPS-005, E12 | T18 | D |
| 7 | [T25 Harness catalogue](T25-harness-catalogue.md) | CRED-001, CRED-002, E11 | T21 | E |
| 7 | [T26 CI core and GitHub](T26-ci-core-github.md) | CI-002–005, LIF-004 | T12, T18, T19 | D |
| 7 | [T27 Dashboard](T27-dashboard.md) | OPS-001, OPS-003 | T21, T22, T23 | C |
| 7 | [T28 Install and update](T28-install-and-update.md) | PRD-002, HST-005, ENG-002, INT-002, E14 | T15, T16, T20 | B |
| 8 | [T29 Other CI and triggers](T29-other-ci-and-triggers.md) | CI-001, CI-006 | T26 | C |
| 9 | [T30 Certification run](T30-certification-run.md) | PRD-001, PRD-003, QA-004 | T13, T24, T25, T27, T28, T29 | A |
| 10 | [T31 Public release](T31-public-release.md) | OPS-007 | T30, with a clean result | B |

## Who builds what

The owner chose five agent setups on 2026-11-09 and asked for the tickets to be split evenly between them, with the hardest ticket on Opus. Each ticket runs on its own branch in its own checkout and lands as its own pull request into `main`. The coordinating session stays on `main`, watches every pull request, sends failed checks and review findings back to the ticket's agent, and merges once the checks pass.

| Lane | Harness | Model | Tickets |
| --- | --- | --- | --- |
| A | Claude Code | Claude Opus 5.5, medium effort | T2 (the hardest), T6, T7, T8, T14, T19, T30 |
| B | Codex | GPT-6.1 Sol, high effort, fast | T1, T10, T16, T23, T28, T31 |
| C | fx | GPT-6.1 Sol, high effort | T3, T11, T17, T22, T27, T29 |
| D | Pi | GPT-6.1 Sol, high effort | T4, T12, T18, T20, T24, T26 |
| E | Devin | GPT-6.1 Sol, high effort, fast | T5, T9, T13, T15, T21, T25 |

## What is covered

123 items, each in exactly one ticket:

- All 105 requirements in `requirements.md`. Two of them, SBX-005 (devices) and SBX-006 (fallback), are split by machine: Mac rows in T15, Linux rows in T20.
- All 14 experiment gates in architecture §22, E01–E14.
- Four pieces of work the spec implies but doesn't number:
  - S1: the repository skeleton and shared contracts (architecture §4, §§11–12).
  - S2: the minimal control path the engine demo is measured through (ENG-001's interfaces).
  - S3: the audit collectors that E02 needs switched on.
  - S4: the format freeze that ENG-001 puts after E04–E07.

## Checkpoints

- **T1** blocks everything, so it is kept small.
- **T6** was the owner's engine-first rule (ENG-001). It merged on 2026-11-10 with the gate recorded as failed on speed (`docs/development/T6-evidence.md`). The owner accepted that result the same day and said to build everything else, so T7–T13 started.
- **T14** freezes the template and saved-state formats once E04–E07 have passed.
- **T30** proves the whole release.
- **T31** is public and can't be undone. It runs only when T30's result is clean. The owner said on 2026-11-09 that the build goes public itself, without waiting for them.

## Sharing the machines

Every ticket runs on the same three machines. Hard timing runs on the Ubuntu laptop need it quiet, and capacity-heavy lanes run one at a time (architecture §20.1).

The owner's own services run on those machines too. On the Ubuntu laptop a Chrome service (`excerlock-dm-chrome.service`) held about 20 of 30 GiB of memory on 2026-11-09. It may be stopped for a hard timing run and is started again as soon as the run ends. The T3 Code server there stays up, and Docker's existing containers, images and networks are never touched.

## What the owner allowed

Set on 2026-11-09. Every ticket carries the parts it needs.

- The build does everything itself: sign-ins and account setup, signing certificates, DNS, and the public release. It does not wait for the owner.
- `sudo` is fine on all three machines and needs no password on any of them.
- Model tokens are the only thing money may be spent on. Each pay-per-use key stays under a hard cap of $25 a month.
- Never put a password or approval dialog on the owner's screen, and never drive their visible screen, apps or browser.
- Only the owner can approve a phone 2FA prompt or read an SMS code.
