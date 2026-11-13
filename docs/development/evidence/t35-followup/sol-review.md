# T35 follow-up review

Scope: only `docs/development/evidence/t35-followup/`. No product edits or new
tests. The requested outcomes are Air DNS/package/HTTPS evidence and native
macOS networking or an honest refusal. The actual native attempt was refused
for current host memory before boot; native DNS remains explicitly unverified.

Checks: `make build` and `git diff --check` passed. The real Air guest completed
DNS, HTTPS and apt install with Chrome open, then all disposable resources were
removed. Provider/model: Codex / GPT-6.1 Sol, medium; usage not yet returned.

Round 1: APPROVE at `404c6694029fbef8dd6774b3e45e7096292346ce`. No findings;
usage was not exposed. It covered only the original evidence/documents.

The coordinator subsequently required successful native macOS networking on
the M5. A later attempt was admitted, booted macOS 26.6.2 and exposed native
network setup failure; see `native-macos-failed-network.json`. The follow-up
now also changes guest setup to select the VZ-assigned MAC and configure a
native SystemConfiguration service instead of an isolated DNS store key.
This behavior change needs a new scoped Sol review after demonstration.

The final implementation publishes the IPv4 and DNS dynamic-store keys while
retaining kernel address/route setup; no network preferences service is
created. The new fresh-clone demonstration passed native DNS and HTTPS with
no manual repair. The initial refusal, fixture failure, baseline curl exit 6
and manual investigation remain distinct historical evidence. `lamp-la` was
restarted after the final run and confirmed Running. Final `make build`,
scoped clippy with warnings denied and `git diff --check` passed.

Round 2: pending; new native-helper behavior and updated evidence only.
