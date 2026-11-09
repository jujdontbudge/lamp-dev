# How to read the Lamp spec

Lamp is open-source software that people install on their own machines to create fast, isolated sandboxes for AI agents, harnesses and CI. These documents describe what the first release must do and how it will be built. Release one runs on Linux x86_64 and arm64, and Apple Silicon Macs with macOS 26 or newer. Linux guests run on both; native macOS guests run on Macs. Nothing has been built yet. Every number in them is a target, not a measured result.

**Owner decision, 2026-11-09:** Windows hosts, guests, test lanes, CLI builds and CI runner rows are outside release one.

## The files

| File | What it is | Read it when |
| --- | --- | --- |
| [brief.md](brief.md) | Every decision the owner made, in plain words. If another document disagrees with it, the brief wins. | You want to know what was decided and why. |
| [../../GLOSSARY.md](../../GLOSSARY.md) | The 17 words the spec uses with exact meanings: sandbox, template, fork, kit, connection, host, fleet, console, and others. | A word seems to mean something specific. |
| [../adr/](../adr/) | Nine short records of the hardest-to-reverse choices, such as no Docker runtime, subscription logins staying on the owner's own hosts, and a relay that never decrypts traffic. | You want the reasoning behind a big choice. |
| [requirements.md](requirements.md) | 105 numbered requirements, each saying what must work, what you'd see, and how it gets tested. | You want to know what "done" means. |
| [architecture.md](architecture.md) | How Lamp is built: its parts, engines, storage, networking, security, and the exact names of every API call, field and error. | You want to know how something works, or the exact name of something. |
| [rounds/](rounds/) | The history: three competing first designs, two critiques, a red team and the responses. | You want to know why something changed. Not needed to understand the final design. |
| [../research/](../research/) | Background research on Modal, Daytona, VM engines, CI, credentials, subscription sign-in and remote access. | You want the evidence behind a choice. |

## A good reading order

1. **The first page of requirements.md.** The opening summary, five short stories about using Lamp, and the everyday defaults. They take about five minutes and show the whole product.
2. **The abbreviations table** right after it, for reference.
3. **Architecture §1–2** for the overall design and the key decisions.
4. **Any requirement area you care about.** The areas run from product and interfaces through sandboxes, storage, network, credentials, kits, sessions, access, relay, CI and operations to benchmarks and testing.
5. **Architecture §23** for the list of owner decisions and which sections depend on each.

## How a requirement reads

Each requirement has the same eight parts:

| Part | What it tells you |
| --- | --- |
| **Statement** | What must be true, in one or two sentences. Start here. |
| **Source** | Which brief section, ADR and architecture section it comes from. |
| **Interfaces** | The same action shown three ways: the CLI command, the API call and the MCP tool. They always do the same thing. |
| **JSON observation** | What a caller gets back. |
| **Visual/consoles** | What you'd see in the dashboard, T3 Code, Herdr or an SSH terminal. |
| **Pass/fail** | The exact bar it must clear, such as "every fork under 1000 ms" or "0 secrets leaked". |
| **E2E** | The end-to-end test: real machines, real sandboxes, real failures, run on the test lanes below. |
| **Code tests** | The unit and fuzz tests. They also list what must never be faked, such as the hypervisor or an actual escape attempt. |

## Where to find specific things

| What | Where |
| --- | --- |
| What using it looks like | Each requirement's Interfaces, JSON observation and Visual/consoles parts, plus "Core command, file and session parity examples" near the top of requirements.md |
| Whole user stories run as tests | "Journey acceptance tests" in requirements.md: one test per story |
| Security tests | "Security attack suite" in requirements.md: 44 attacks, ATK-01 to ATK-44 |
| Speed targets and how they're measured | "Benchmark suite" in requirements.md, and architecture §19 |
| Which machine runs which test | "Test-lane matrix" in requirements.md, and architecture §20 |
| Every API name, field and error | Architecture §11–12. The full list is in "Canonical parity index" at the end of requirements.md. |
| What's still open | "Remaining questions" in requirements.md. You have no open owner questions; what's left is engineering proof. |

## Markers you'll see

- **`[owner-confirmed O7]`** and similar: this rule depends on an owner decision. O1–O10, A1–A5 and B1–B4 are all accepted, and the brief and architecture §23 record each one.
- **`[proposed]`**: an engineering target that isn't binding yet. It uses "should" instead of "must" until engineering adopts it with evidence.
- **E01–E14**: experiments in architecture §22 that must pass before the design depends on them. For example, E01 proves a Linux engine can meet the create and fork speed targets.
- **Nested**: a test lane that runs inside a VM rather than directly on the machine. Nested results never certify native speed.

## The test machines

The nine test lanes below run on three machines the owner already has, alongside the development relay. Vendor tests use the owner's existing paid accounts and subscriptions; nothing new is bought. Pay-per-use API keys have a hard spend limit.

| Lane | Machine | What it covers |
| --- | --- | --- |
| `LX` | Ubuntu laptop | Native Linux x86_64. This is where the hard speed targets are measured. |
| `LX22`, `D12`, `F40`, `L510` | VMs on the Ubuntu laptop (nested) | Older Linux: Ubuntu 22.04, Debian 12, Fedora 40 and the Linux 5.10 kernel minimum. |
| `LA` | VM on the M5 Mac (nested) | Linux on arm64. It shows the feature works, but doesn't certify arm64 speed. |
| `M5` | M5 Mac, macOS 26 | Linux and native macOS guests on a native Mac host. |
| `AIR` | MacBook Air M2, macOS 26 | Linux and native macOS guests on a low-end, fanless Mac. |
| `NV` | VM on the Ubuntu laptop with nested virtualization turned off | The clearly labeled fallback for machines that can't run VMs (gVisor on Linux). |

On Macs, the fallback is a restricted process using App Sandbox, tested on `M5` and `AIR` with virtualization access denied. Both fallbacks are labeled weaker; public-repository CI always needs a fresh VM.

Run `python3 docs/spec/check_spec.py` from the repository root to check that the final architecture and requirements agree on names, routes, errors and lanes, and retain all requirement and attack IDs.

## The headline numbers

On the standard Linux sandbox (2 virtual CPUs, 4 GiB of memory, 20 GiB of disk), measured on `LX`:

- Typical create time under 500 ms.
- Every fork with memory under 1000 ms.
- At least 97% of native speed for compute, disk and network.
- No API keys, passwords or browser cookies leaked across all 44 attacks.

That last target has two documented exceptions. The first is subscription logins, which stay inside the owner's own sandboxes on the owner's own machines (ADR 0004). The second is a password or cookie the owner deliberately approves handing to a sandbox (O5). It's flagged rather than counted as a leak.
