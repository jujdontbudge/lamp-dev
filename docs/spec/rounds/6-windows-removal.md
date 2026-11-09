# Round 6: Windows removal

Status: complete, 2026-11-09. Dated owner removal record for “skip windows for everything.” No commit, installation, runtime certification, new purchase or password/approval prompt was performed. Earlier rounds, research and infra are unchanged (40 existing protected files checked). The glossary makes no platform-support claim and needed no edit.

## Sections changed

- `brief.md` §§5–7, 19 and 22: host minimums, native Mac class, exact fallback rules, nine named lanes, dated scope decision and the single noncommittal later note.
- `architecture.md` §§1, 3–9, 11–14, 16–17 and 19–25: engines/confinement, local transports, device/storage/network paths, UID attribution, CLI targets, capability/attack/benchmark matrices, M2, experiment gates, risks, decisions and source pruning. M2 is now the macOS host and guest classes.
- `requirements.md`: opening, five stories/everyday defaults, abbreviations/test rules, platform-dependent records, detailed Journeys 1/2/4/5, attack and benchmark suites, lane/compatibility/CI matrices, traceability, owner reconciliation, remaining evidence and inventory. Journey 2 is Linux plus Mac.
- `README.md`: plain-English release scope/removal record, nine-lane guide, fallback explanation, existing-account policy and checker command.
- ADR 0001: dated amendment, with the original wording preserved verbatim as explicitly superseded history. Other ADRs need no amendment.
- Added `check_spec.py`, copied from round 5's Python block; its allowed set contains the nine retained lanes and its retired set includes the three removed lanes. No other source changes to the check were needed.

USB passthrough stays outside release one. Tests use the owner's existing paid accounts/subscriptions and buy nothing new; pay-per-use keys retain hard spend limits. O1–O10 (with O10 amended), A1–A5 and B1–B4 remain accepted. Linux RAM fork on both host families, native Mac A2 fallback and the two-per-Mac allocation limit remain binding.

## IDs rescoped

All 105 IDs are retained, with no additions, deletions or renumbering. SBX-003 is retitled “Native macOS full-VM class and two-per-Mac limit.” The following 46 records have scope, examples, acceptance text or fixed-case references updated:

INT-003; HST-001–003, HST-005–006; SBX-001–007; TPL-001–003; LIF-001–003, LIF-005–006; STO-001–005; NET-001–006; PLG-001–002; SES-005; CI-001, CI-005; OPS-001, OPS-003–004, OPS-006–007; BEN-001–002; ENG-001; QA-001.

The traceability and requirement inventory reflect these scopes. Every record retains all eight required fields.

## Cases and attacks reassigned

`lamp.compatibility/v1` is dated 2026-11-09 and has 22 surviving fixed case IDs, identical in both documents. The host-windows case and W11/WS22S/WS22D lane rows are retired by this removal record. Host/device/GPU/Docker/storage/harness/browser/CI positives now list only their exact surviving Linux/Mac pairs; no new provider/OS product is inferred. CLI builds are Linux x86_64/arm64 and macOS arm64. No required surviving positive becomes optional.

The nine lanes are LX, LX22, D12, F40, L510, LA, M5, AIR and NV. Attack set G is all host-linux/host-macos rows; F is NV gVisor plus hypervisor-denied M5/AIR process fallback; B retains LX/M5 browser/broker/vault fixtures; H is the LX dev relay and Chromium/Firefox plus M5/AIR WebKit; T is real LX/M5/AIR fleets plus LA enforcer tests.

All ATK-01–44 meanings and IDs remain. G/F assignments narrow to those surviving rows (01–03, 05–11, 18–28, 32–36, 39–42); device positives govern 04; T governs 12/43; the LX keyless job governs 13; H governs 14–15/37–38; surviving CI cases govern 16–17; B/H governs 29–31; surviving harness endpoint pairs plus T govern 44. Native macOS uses A2's separate disk-only identity test for 42 where needed. ATK-02's confinement wording now names OS identity/profile/IPC/handle restrictions. Docker positives remain confined to Linux cases.

## Check-script result

Command: `python3 docs/spec/check_spec.py` (repository root), exit **0**.

- **0 mismatches**: operations, method/path pairs, errors, lanes and literal paths all agree.
- **0 JSON failures**: 434 examples parsed; 9 explicitly schematic fragments.
- **105 requirements / 105 unique IDs**; **44 attacks / 44 unique IDs**.
- 56 resource families; 328 operations and 328 method/path pairs.

Additional document checks: exact same original 105 IDs; 22 matching fixed case definitions; exact nine-lane matrices; eight fields per record; five plain stories and five detailed journeys; consistent Markdown table widths. These are document checks, not implementation, speed or hardware passes.

## Final grep output

The source sweep below includes every changed final-spec/ADR file and the maintained checker. This response log is excluded from its own embedded transcript to avoid recursively matching the transcript. A sweep including this log additionally finds this dated removal record and its repeated transcript only.

```sh
grep -niE 'windows|WHP|HCS|Hyper-V|WFP|NTFS|AppContainer|named pipe|W11|WS22' docs/spec/brief.md docs/spec/architecture.md docs/spec/requirements.md docs/spec/README.md docs/adr/0001-one-fleet-many-hosts.md docs/spec/check_spec.py
```

```text
docs/spec/brief.md:204:- **Later, not in release one: Windows hosts and guests.** The engine and host plugin boundaries keep it possible.
docs/spec/brief.md:221:- **Owner decision, 2026-11-09: “skip windows for everything.”** Windows hosts, native guests, test lanes, CLI builds and CI runner rows are outside release one. O10 now means Linux 5.10+ and macOS 26+ on Apple Silicon. The nine test lanes are `LX`, `LX22`, `D12`, `F40`, `L510`, `LA`, `M5`, `AIR` and `NV`; `W11`, `WS22S` and `WS22D` are retired.
docs/spec/architecture.md:1315:**Owner decision, 2026-11-09: “skip windows for everything.”** Windows hosts, native guests, test lanes, CLI builds and CI runner rows are outside release one. Retire `W11`, `WS22S` and `WS22D`; the nine retained lanes are `LX`, `LX22`, `D12`, `F40`, `L510`, `LA`, `M5`, `AIR` and `NV`. O10 is amended to Linux 5.10+ and macOS 26+ on Apple Silicon.
docs/spec/requirements.md:1895:| O10, amended 2026-11-09 | Owner decision: “skip windows for everything.” Windows hosts, native guests, test lanes, CLI builds and CI runner rows leave release one. Minimums are Linux 5.10+ on x86_64/arm64 and macOS 26+ on Apple Silicon; older macOS is uncertified: HST-001, SBX-002–003, INT-003, QA-001–004. |
docs/spec/README.md:5:**Owner decision, 2026-11-09:** Windows hosts, guests, test lanes, CLI builds and CI runner rows are outside release one.
docs/adr/0001-one-fleet-many-hosts.md:5:> **Superseded by the 2026-11-09 removal decision; original wording preserved:** A Lamp install is a fleet: any number of hosts behind one address and one set of keys. Hosts can be laptops, spare computers or cloud VMs, running Linux, macOS or Windows. Lamp places each sandbox on a host with room and the right OS and devices, and a caller can pin it to a specific host. Consoles such as T3 Code or Herdr see the whole fleet as a single machine, and each session in it lives in its own sandbox. We chose this over one independent install per machine for two reasons: consoles link machines one at a time by hand, and sub-agents spread across hosts still have to show up in one place. Coordination moves to another host automatically when one goes away, because laptops that sleep are normal hosts.
docs/adr/0001-one-fleet-many-hosts.md:9:Owner decision: “skip windows for everything.” The original paragraph above is a historical record; its Windows support statement is superseded for release one. Hosts are Linux x86_64/arm64 (Linux 5.10+) and Apple Silicon macOS 26+. Linux guests run on both host families; native macOS guests run only on Macs. The fleet model and placement rules still apply. No host, guest, test lane, CLI build or CI runner for the removed platform is in release one.
docs/spec/check_spec.py:96:retired = {'WN','WP','WS','MIN','NV-LX','NV-Mac','NV-Windows','W11','WS22S','WS22D'}  # Owner removal record, 2026-11-09; reject retired lanes.
```

Eight source hits, all intentional: the brief's single later note; dated owner-removal records in brief §22, architecture §23, requirements' O10 row and README; ADR 0001's dated superseded quotation and amendment; and the checker's dated retirement set, which rejects removed lanes. No operative support statement, adapter, confinement rule, transport, storage path, edition/evaluation rule or matrix row for the removed platform remains.

## Unresolved

None for this scope change. Existing engineering qualification questions remain explicitly listed in the final spec; no runtime or vendor evidence is newly claimed.
