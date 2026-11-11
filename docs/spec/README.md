# Reading the Lamp spec

Lamp is self-hosted sandbox software for agents, harnesses and CI. Release one supports Linux x86_64/arm64 and Apple Silicon Macs on macOS 26+. Linux guests run on both host families; native macOS guests run on Macs. [The brief](brief.md) records the owner's decisions and wins when another document disagrees.

Owner amendment, 2026-11-10: build features with almost no new tests and show one real end-to-end demonstration per ticket. Add a test only when the feature cannot be shown working another way. Measure the owner's speed targets and zero-leak goal once at release and publish actual results honestly. Measurements never block implementation, merge or release. There is no experiment, attack, compatibility, trial-count, soak, every-change benchmark or test-lane prerequisite.

## Current documents

| File | Purpose |
| --- | --- |
| [brief.md](brief.md) | The owner's scope, decisions and attributed amendments. |
| [requirements.md](requirements.md) | Features, behavior, interfaces, returned JSON and what people see. |
| [architecture.md](architecture.md) | Components, safety boundaries, storage, networking and canonical API names and fields. |
| [../../GLOSSARY.md](../../GLOSSARY.md) | Domain terms. |
| [../adr/](../adr/) | Architectural decisions and their reasons. Current owner amendments supersede old process language. |
| [../tickets/README.md](../tickets/README.md) | Complete feature prompts and their real dependencies. |
| [../tickets/map.html](../tickets/map.html) | The feature dependency map; completed and retired work has no blocking arrows. |

Start with the five user journeys in requirements.md, then architecture §§1–2. Requirements keep statement, source, interface, JSON and visual descriptions. They no longer carry mandatory pass/fail, end-to-end suite or code-test sections. Feature behavior still requires authorization, isolation, durable recovery and secret protection.

## Release observations

The owner's Linux targets remain create p50 under 500 ms, live memory fork under 1 s, and at least 97% of native computation, disk and network speed. The standard measurement size remains 2 vCPU, 4 GiB RAM, 20 GiB disk and up to 1 GiB recently written memory. The zero-leak goal also stays, with the brief's subscription-custody and explicit-exposure exceptions. Record actual results once at release without sample quotas, an every-fork/no-outlier rule or a passing prerequisite. Historical failed results remain failed.

## Historical material

[rounds/](rounds/) preserves the design drafts, critiques and responses unchanged. [../research/](../research/) preserves source research, and [../development/](../development/) preserves implementation evidence. Earlier process demands in those records are superseded by the owner's 2026-11-10 decision; they are not work prompts or release blockers. The old spec checker encodes the withdrawn frozen IDs and test matrix and is not a condition for this feature rewrite. Its code is owned by the separate test-cleanup task.

T5 and T6 are done. T13, T14 and T30 are retired. Feature owners version formats as needed; there is no format-freeze or certification ticket before release. The product scope, supported platform minimums, safe machine use, privacy, spending limits and licenses remain in force.
