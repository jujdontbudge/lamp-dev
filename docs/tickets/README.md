# Lamp build tickets

These prompts describe the first-release features in [the brief](../spec/brief.md). Hand a ticket to its agent as written. [map.html](map.html) shows the same feature dependencies.

Owner decision, 2026-11-10: build the features, add almost no tests, and show one real end-to-end demonstration covering the ticket's goal. Add a test only when the feature cannot be shown working another way. There are no experiment, attack, compatibility, trial-count or soak prerequisites. Measure the owner's speed targets and zero-leak goal once at release and publish the result honestly; measurements never block implementation, merge or release. Existing failed results stay failed.

## Feature dependencies

“Waits for” means code or feature interfaces the ticket uses. Shared interfaces may allow overlapping work; the waves below are one possible ordering, not synchronized checkpoints.

| Wave | Ticket | Feature result | Waits for | Built by |
| --- | --- | --- | --- | --- |
| 1 | [T1 Skeleton and contracts](T01-skeleton-and-contracts.md) | Skeleton and contracts | nothing | B |
| 2 | [T2 Linux engine](T02-linux-engine.md) | Linux engine | T1 | A |
| 2 | [T3 Demo control path](T03-demo-control-path.md) | Demo control path | T1 | C |
| 2 | [T4 Guard and collectors](T04-guard-and-collectors.md) | Guard and collectors | T1 | D |
| 2 | [T11 Plugin host](T11-plugin-host.md) | Plugin host | T1 | C |
| 3 | [T7 Durable capture and storage](T07-storage-proof.md) | Durable capture and storage | T2 | A |
| 3 | [T8 Mac guest engines](T08-mac-feasibility.md) | Mac guest engines | T2 | A |
| 3 | [T9 Subscription sign-in custody](T09-sign-in-custody.md) | Subscription sign-in custody | T2, T3 | E |
| 3 | [T10 Control core](T10-control-core.md) | Control core | T3 | B |
| 3 | [T12 Network](T12-network.md) | Network | T4 | D |
| 4 | [T15 Mac devices and fallback](T15-mac-breadth.md) | Mac devices and fallback | T8 | E |
| 4 | [T16 Fleet](T16-fleet.md) | Fleet | T10 | B |
| 4 | [T17 Broker](T17-broker.md) | Broker | T10, T11 | C |
| 4 | [T18 Access and SSH](T18-access-and-ssh.md) | Access and SSH | T10, T11 | D |
| 4 | [T19 Lifecycle and storage](T19-lifecycle-and-storage.md) | Lifecycle and storage | T7, T10 | A |
| 5 | [T20 Linux support and devices](T20-linux-breadth.md) | Linux support and devices | T7, T8, T15 | D |
| 5 | [T21 Sessions and consoles](T21-sessions-and-consoles.md) | Sessions and consoles | T9, T17 | E |
| 5 | [T22 Kits](T22-kits.md) | Kits | T17 | C |
| 5 | [T23 Budgets and audit](T23-budgets-and-audit.md) | Budgets and audit | T16, T17 | B |
| 5 | [T24 Hosted side](T24-hosted-side.md) | Hosted side | T18 | D |
| 5 | [T26 CI core and GitHub](T26-ci-core-github.md) | CI core and GitHub | T12, T18, T19 | D |
| 6 | [T25 Harness catalogue](T25-harness-catalogue.md) | Harness catalogue | T21 | E |
| 6 | [T27 Dashboard](T27-dashboard.md) | Dashboard | T21, T22, T23 | C |
| 6 | [T28 Install, update and SDKs](T28-install-and-update.md) | Install, update and SDKs | T15, T16, T20 | B |
| 6 | [T29 Other CI and triggers](T29-other-ci-and-triggers.md) | Other CI and triggers | T26 | C |
| 7 | [T31 Public release](T31-public-release.md) | Public release | T24, T25, T27, T28, T29 | B |

## Completed and retired work

- [T5 Measurement tools](T05-oracles-and-benchmarks.md) is done. Existing tools are optional; no feature waits for their results.
- [T6 Engine integration](T06-engine-gate.md) is done and accepted by the owner on 2026-11-10. Its historical speed result remains failed in `docs/development/T6-evidence.md`. It is not a blocker.
- [T13 Proof suite](T13-proof-suite.md), [T14 Format freeze](T14-format-freeze.md) and [T30 Certification run](T30-certification-run.md) are retired. PR #8 is closed. No feature waits on them. Feature owners version formats when needed.

The active plan has 26 feature tickets and 43 dependency edges. T19 uses T7 and T10; T20 uses T7, T8 and T15; T31 uses T24, T25, T27, T28 and T29. Release depends on delivered features and packages.

## Implementation assignments

The owner's chosen setups (2026-11-09) remain assigned to the retained work. These are implementation assignments, not machine or test lanes.

| Assignment | Harness | Model | Feature tickets |
| --- | --- | --- | --- |
| A | Claude Code | Claude Opus 5.5, medium effort | T2, T7, T8, T19 |
| B | Codex | GPT-6.1 Sol, high effort, fast | T1, T10, T16, T23, T28, T31 |
| C | fx | GPT-6.1 Sol, high effort | T3, T11, T17, T22, T27, T29 |
| D | Pi | GPT-6.1 Sol, high effort | T4, T12, T18, T20, T24, T26 |
| E | Devin | GPT-6.1 Sol, high effort, fast | T9, T15, T21, T25 |

Each ticket uses its own branch and checkout and opens its own PR. The coordinating session stays on `main`, reviews the work, sends real defects back and merges it. No extra owner-rule preamble or mandatory review/test campaign is needed.

## Sharing the machines safely

Use the owner's Ubuntu laptop, M5 Mac and MacBook Air. Keep the owner's services running, including the T3 Code server, Chrome and Beeper; leave existing Docker containers, images and networks alone. Never drive the owner's visible screen, apps or browser or trigger password/approval dialogs on Macs.

Across all tickets, the Ubuntu laptop may have at most 2 live Lamp sandboxes. Before creating one, check `pgrep -c firecracker` is below 2 and `free -m` reports at least 12 GiB available. Release it promptly. Account for the Air's 8 GB and fanless design. Destructive storage work uses disposable fixtures, never live disks.

Demonstrations use public third-party projects and disposable accounts, not the owner's projects, real vault items or browser profiles. Keep secrets out of output, logs and commits. Model tokens are the only permitted expense; buy, rent or subscribe to nothing else. Each pay-per-use key has a provider-enforced $25/month cap.

## Existing owner authorization

The owner authorized sign-ins/account setup using existing accounts, signing setup, Lamp DNS changes and public release (2026-11-09). Only the owner can approve phone 2FA or read SMS codes. That authorization does not permit password dialogs or interference with everyday apps. Preserve other products' DNS and CAA records under eilmer.si. T31 publishes the completed features and packages, with honest release measurements, and retires private persistent repository runners before public visibility. Public CI may stay off; Lamp's CI product still requires fresh VMs for public-repository jobs.
