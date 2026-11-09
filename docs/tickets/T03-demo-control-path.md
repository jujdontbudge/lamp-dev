# Control path for the engine demo

## Goal
Build the smallest real control path that lets a caller create a sandbox from a template, run a command in it and fork it, the way ENG-001's interfaces show: `lamp sandbox create --template-id … --json`, `POST /v1/sandboxes`, then `sandbox_fork`. When this is done, those calls travel from the CLI and the HTTP API through `lampd` to the Rust worker over `lamp.worker/1` and return the canonical operation and resource shapes, and being accepted is never reported as being ready (architecture §6.2).

The engine gate times create from the moment the caller sends the request to a running command, through the same path real callers will use, so this path's overhead counts against the 500 ms target. Go owning control and Rust owning the engine is a fixed decision (ENG-003).

## Context
- Ticket T3 of 31; `docs/tickets/README.md` lists them all and what each waits for. T1 delivered the repository layout and the contracts. Three tickets run at the same time, each in its own checkout: T2 builds the Linux engine and owns the worker side of `lamp.worker/1`; T4 builds the host guard and audit collectors; T5 builds the independent oracles and benchmarks, which drive Lamp only through this ticket's CLI and API. T6 then joins the four and runs the gate. This ticket owns `lampd` and the `lamp` CLI. After the gate, T10 grows this into the full control core.

## Constraints
- Only what the demo needs: create, run a command, fork and read the operation, for one local caller. The owner's rule (ENG-001) is no product expansion before the engine gate; T10 builds the rest.
- Model tokens are the only thing money may be spent on (owner, 2026-11-09): buy, rent or subscribe to nothing else. Each pay-per-use key stays under a hard cap of $25 a month, set in its provider's dashboard.

## Evidence of done
Show evidence that the goal is done, taken from the real thing: an end-to-end test, a video, screenshots, or whatever fits.
