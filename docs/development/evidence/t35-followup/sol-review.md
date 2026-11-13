# T35 follow-up review

Original round 1 scope: only `docs/development/evidence/t35-followup/`. No product edits or new
tests. The requested outcomes are Air DNS/package/HTTPS evidence and native
macOS networking or an honest refusal. The actual native attempt was refused
for current host memory before boot; native DNS was then explicitly unverified.

Checks: `make build` and `git diff --check` passed. The real Air guest completed
DNS, HTTPS and apt install with Chrome open, then all disposable resources were
removed. Provider/model: Codex / GPT-6.1 Sol, medium; usage was not exposed.

Round 1: APPROVE at `404c6694029fbef8dd6774b3e45e7096292346ce`. No findings;
usage was not exposed. It covered only the original evidence/documents.

The coordinator subsequently required successful native macOS networking on
the M5. A later attempt was admitted, booted macOS 26.6.2 and exposed native
network setup failure; see `native-macos-failed-network.json`. The follow-up
now also changes guest setup to select the VZ-assigned MAC and configure a
native SystemConfiguration service instead of an isolated DNS store key.
This behavior change received the scoped round 2 review after demonstration.

The final implementation publishes the IPv4 and DNS dynamic-store keys while
retaining kernel address/route setup; no network preferences service is
created. The new fresh-clone demonstration passed native DNS and HTTPS with
no manual repair. The initial refusal, fixture failure, baseline curl exit 6
and manual investigation remain distinct historical evidence. `lamp-la` was
restarted after the final run and confirmed Running. Final `make build`,
scoped clippy with warnings denied and `git diff --check` passed.

Round 2: APPROVE at `30b7cc5352dc6b48d5fea8e6f57931f536b27dde`. No blocker or
should-fix finding. The reviewer confirmed MAC selection matches VZ, IPv4/DNS
state addresses the native resolver failure, and final evidence records DNS
and HTTPS 200. Scoped diff check passed and all five evidence JSON files
parsed. No edits, demonstrations or new tests were requested or performed.
Usage was not exposed. Two rounds total; no declined findings.

Final scope is one guest-helper change, guest documentation and five evidence
JSON files plus this ledger/README (9 files, 705 insertions, 34 deletions before
this verdict update). Most added lines are real transcripts. No new automated
tests or benchmarks.
