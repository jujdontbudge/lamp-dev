# T33 Sol review ledger

Reviewer: GPT-6.1 Sol in T3 Code (Codex), medium effort, priority tier.
Usage was not exposed.

- Round 1: CHANGES. S1 (should), first connection after both-process restart
  skipped missing-guest observation, leaving `unavailable` and charged capacity.
- S1 accepted. Initialized the observed connection epoch to zero. The reviewer's
  API regression failed before the fix (`unavailable`, one charged sandbox) and
  passed afterward. Corrected its numeric comparison for the existing decoder.
  Kept that one regression test; added no others.
- The existing authority smoke and Go vet passed. `make build` passed after the
  change. Real both-process restart, deletion, fresh guest execution and zero
  final live usage are in `ubuntu.json`.
- F44 clarification from coordinator: a supervisor that necessarily destroys
  volatile guests may report a clean, deletable loss rather than preserve the
  same guest. Both supervisor-only and both-process recovery demonstrate that
  accepted behavior. No guest-memory continuity is claimed.
- Round 2: pending; scope is S1 and code changed since round 1 only.
