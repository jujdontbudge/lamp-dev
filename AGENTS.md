# Verification and independent benchmark harness

Run `make bootstrap verify` for the workspace build and selected smoke tests,
including contracts, generators and interoperability. CI runs this on LX, M5
and LA; Linux also runs two selected Rust smokes. Run `make bootstrap test-all`
for the full existing suites or `make lint` for vet, formatting and clippy, only
when requested. For harness-only iteration use
`python3 -m unittest discover -s tests/benchmarks -v`. The benchmark harness uses
only Python's standard library; its workloads additionally need `cc` for the
fixed build row.

# Owner implementation rule — 2026-11-10

The owner said: “assume everything we had decided about test, how many test, when to run test is wrong and complete opposite. i am seeing that AI agent write way to many test. i want them to write almost none. they are invent imaginary barrier and performance goals i never asked them”. This later instruction governs implementation work and supersedes older mandatory verification or benchmark process language, including the preserved paragraph above.

Build the features in the ticket and show one real end-to-end demonstration covering its goal. Add almost no tests: only when the feature cannot be shown working another way. Run only what iteration needs. Do not invent gates, attack lists, compatibility matrices, trial/sample quotas, soak runs or timing prerequisites. No benchmark runs on every change or PR. Measure the owner's create, live-fork, native-speed targets and zero-leak goal once at release and publish actual results honestly; measurements never block implementation, merge or release.

T5 and T6 are done. T13, T14 and T30 are retired. Follow the feature dependencies in `docs/tickets/README.md` and `docs/tickets/map.html`; feature owners version formats when needed. Historical development evidence and design rounds remain historical, including failed results. Do not relabel failures or require a rerun to turn them green.

Keep real product behavior: authorization, isolation, durable capture and recovery, secret custody, quotas, budgets and fresh VMs for public-repository CI. The optional measurement tools use Lamp's public CLI/API; do not add private worker RPCs to the independent adapter. Missing observations are reported honestly, without a certification barrier.

# Owner-machine and spending safety

- Use only the owner's Ubuntu laptop, M5 Mac and MacBook Air. Keep everyday services running, including Chrome, Beeper and the T3 Code server. Never use, change or remove existing Docker containers, images or networks.
- Across all tickets, Ubuntu may run at most 2 live Lamp sandboxes. Before creating one, check `pgrep -c firecracker` is below 2 and `free -m` shows at least 12 GiB available. Release it promptly. Account for the Air's 8 GB and fanless design.
- Never drive the owner's visible screen, apps or browser. On Macs, never run commands that ask for a password or approval dialog: no `sudo`, `sfltool`, keychain writes or system launch-item bootstrap. Never launch a desktop app with an overridden HOME.
- Use disposable storage for destructive work, never live disks. Demonstrations use public third-party projects and disposable accounts, never owner projects, real vault items or browser profiles.
- Never print, log or commit secrets. Authentication stays out of URLs and evidence metadata. Model tokens are the only permitted expense: buy, rent or subscribe to nothing else. Each pay-per-use key has a provider-enforced $25/month cap.
- Work on the ticket's own branch and checkout, push it and open its PR; never push to `main` or another ticket's branch. The coordinator reviews and merges.
- Before public repository visibility, disable or retire private persistent repository runners so public jobs cannot reach the owner's machines. Public repository CI may remain off; Lamp's CI feature retains its fresh-VM policy.
