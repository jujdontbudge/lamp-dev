# Repository skeleton and shared contracts

## Goal
Set up the Lamp repository so that many agents can build in it at once without colliding. When this is done, the Go side and the Rust side build and test from a clean checkout on Linux x86_64, Linux arm64 and Apple Silicon macOS, and the repository holds the shared contracts everything else is written against: the `lamp.worker/1` local protocol, and one public schema covering every resource, field, operation, error and event named in architecture §§11–12, with generated types on both sides. A check proves the contracts and the spec agree on every name.

Lamp is open-source software people install on their own machines to create fast, isolated sandboxes for AI agents, harnesses and CI. Nothing is built yet; the repository holds only the agreed spec. Four tickets start the moment this one lands and 26 more follow, each in its own checkout, so they need one layout and one set of names. The architecture makes "one schema and operation dictionary" a key decision (§2) so that names never drift apart.

## Context
- Ticket T1 of 31; `docs/tickets/README.md` lists them all and what each waits for. Nothing else runs until this lands. T2 (Linux engine), T3 (demo control path), T4 (host guard and audit collectors) and T5 (independent oracles and benchmarks) start from its result.

## Constraints
- Skeleton and contracts only. The engine, control service, guard and test harness belong to T2–T5.
- The template format and the saved-state format stay unfrozen. T7, T8 and T9 prove storage, Mac guests and sign-in capture first, and T14 freezes them (ENG-001).
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
