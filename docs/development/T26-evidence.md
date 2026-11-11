# T26 — native GitHub Actions evidence

2026-11-11. Native Actions now ran on fresh Lamp Firecracker VMs in the explicitly
authorized **private** disposable `JohanSardar/lamp-t26-fixture`, seeded with
public zlib v1.3.1. No GitHub-hosted or persistent LX/M5/LA runner was attached.
The earlier controlled-forge results below remain historical, not native proof.
Latest `origin/main` was merged after these observations; the merged Linux
`lampd` build succeeded. Passed VM goals were deliberately not rerun or relabeled
as measurements of the later merged binary.

## Transport and credential corrections

The earlier native OIDC run **used a Cloudflare quick tunnel
(`trycloudflare.com`)**. That was not the approved product transport. The tunnel
and its SSH forward were stopped during cleanup. No third-party public tunnel
may be used again, and no Lamp endpoint may be opened publicly before T31.
Further job connectivity uses a non-public path or T24's product relay, not a
rerun of that passed OIDC goal.

Copying the owner's ChatGPT subscription credential into an Actions secret was
also wrong: refresh-token rotation can log the owner out. The secret was deleted;
owner subscription credentials must never be copied into GitHub again. Further
model runs use the host credential broker, free OpenRouter models, provider-side
maximum price zero, and only a short-lived scoped Lamp grant in the job.

## Native goals passed

| Goal | Actual GitHub run | Observation |
| --- | --- | --- |
| Ordinary Actions build | [42961916224](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42961916224) | Real runner, checkout, zlib build and README round-trip; downloaded artifact verified; Lamp job `jb_977fbe75fd42fce3499825aa6badf24f` completed. |
| GitHub-issued OIDC | [42962635889](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42962635889) | Live GitHub issuer/JWKS, proof-bound exchange, scoped fleet read allowed and key-list denied. No stored Lamp credential in GitHub. |
| Cancellation | [42962867545](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42962867545) | Canceled through GitHub while the native cancel step ran; Lamp job `jb_cd4594f5d4c297a6fddb6898afe4ce86` became canceled. |
| Trusted stopped retention | [42963069751](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42963069751) | Suspended VM, old-Task resume HTTP 409, new budget/Task inspected retained public checkout and absence of runner transport directory; VM then deleted. |
| Actual untrusted same-repository PR | [42971889992](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42971889992) | Owner-authored [PR #3](https://github.com/JohanSardar/lamp-t26-fixture/pull/3), explicitly empty trusted actor list. Native build and authority checks passed; classified `public_pr`; fresh sandbox `sb_adffa9b5cc87b180b519b5fff82af34d` deleted despite `retain_stopped=true`. No repository/environment model or Lamp secrets. **Not a stranger's fork.** |
| Claude Code Action, real model reply | [42983320133](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42983320133) | Fresh one-job runner; scoped `claude`/`messages` grant from Lamp's host credential broker to free OpenRouter (`openrouter/free`, provider max price 0; served by `nvidia/nemotron-3.5-super-vl:free`). Reply `LAMP_T26_CLAUDE_FREE_OK` verified from the action's execution file; Lamp job `jb_70a712bc1705bd26eea1f88fcc44ce4c` completed. No owner subscription or new key; GitHub held no model secret. Claude Code's own `costUSD` is its estimate for an unknown model, not a charge. |
| Codex Action, real model reply | [42987215991](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42987215991) | Fresh one-job runner; scoped `codex`/`responses` grant through the same broker. Reply `LAMP_T26_CODEX_FREE_OK` verified from the action's `final-message`; Lamp job `jb_8586d9302f7561a813f29be66fe19424` completed. Workflow disables Codex's hosted `web_search` tool (broker admits only function tools) and gives the action a pass-through `sudo` (runner is already UID 0 in its task namespace). |

[Native status](evidence/t26/native-status.json) records the matching observation
metadata. Passed goals are not being rerun. Coding-agent limitations and final
fixture cleanup are recorded separately. A real
**stranger's fork PR remains open evidence**, not replaced by an owner PR.
The earlier Claude/Codex limitations below are historical; both actions later
passed through the host credential broker (rows above).

## Coding-agent observations — no inference success claimed

* **Copilot:** the existing account had an individual plan, included premium
  capacity and overage disabled. An owner-assigned issue produced GitHub's real
  `dynamic/copilot-swe-agent/copilot` demand, with the exact per-run Lamp labels.
  [42965716001](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42965716001)
  reached a fresh native runner but failed initialization for missing `jq`.
  After adding `jq`/`gh` only to the disposable seed,
  [42970831688](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42970831688)
  and [42971386194](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42971386194)
  failed GitHub's self-hosted firewall validation. The effective job value was
  still `COPILOT_AGENT_FIREWALL_ENABLED=true` after setting the legacy repository
  and `copilot` environment variables false. The provider requires its current
  repository Internet-access setting to be changed; no available public API
  setting was located. No owner browser/profile was used and no validation or
  Lamp guard was bypassed. **Native placement observed; model inference not run.**
* **Codex (historical, before the broker route):** only an existing ChatGPT subscription credential was available,
  supplied through a private Actions secret into a mode-0600 transient job file;
  no API key was created.
  [42964816530](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42964816530)
  failed archive ownership restoration in the single-UID namespace; native CI
  now uses `TAR_OPTIONS=--no-same-owner` without granting outer root.
  [42965630506](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42965630506)
  installed the official CLI/proxy but its prompt wrapper failed reading absent
  API-key proxy server information. A subscription-CLI attempt in
  [42967604409](https://github.com/JohanSardar/lamp-t26-fixture/actions/runs/42967604409)
  lost runner communication before the inference step; GitHub supplied an empty
  run-log archive. Its underlying cause was not established. **These are failed
  native attempts, not successful Codex Action or model inference evidence.**
  The Actions secret was removed before the untrusted PR was created.
* **Claude Code Action (historical):** not run at that point, for want of a credential.
  Expired cached OAuth was not relabeled as a working credential, no approval
  dialog/keychain write was attempted, and no account or key was provisioned.

The fixture repository and `copilot` environment each had zero stored secrets
before the untrusted PR. That run observed metadata-network denial and inability
to mint runner authority with its job token. LAN/host denial is the installed
immutable guard policy, not an additional claimed live probe. No new tests,
benchmark, quota-of-trials requirement or rerun of a passed goal was added.

## Fixture cleanup completed

The fixture was archived **without changing private visibility**. Both PRs and
its disposable issue were closed without merging. All 22 CI-control sandboxes
were `deleted`; GitHub reported zero fixture runners, queued/in-progress runs,
repository secrets and `copilot` environment secrets. Temporary fixture variables
were removed. The dedicated controller/supervisor were stopped, the worker grant
revoked, copied native host credential/enrollment files removed, and the HTTPS
tunnel/SSH forward stopped. The owned supervisor store had zero capture
directories and no owned live Firecracker process. Other tickets' VM/services,
owner model credential files and existing Docker resources were left untouched.
Clean public-source templates/seeds and historical evidence remain inert.

These are observed cleanup results, not a new certification or release benchmark.
The full T26 goal is **not** claimed demonstrated: agent inference limitations
above and the real stranger's fork observation remain explicit.

## Earlier controlled-forge demonstration (historical)

[`demonstration-11.log`](evidence/t26/demonstration-11.log) records one explicit
end-to-end demonstration of the production control-service job lifecycle on
Ubuntu. The same one-case driver also exercises real RSA signature verification
and an Ed25519 proof through the public bootstrap/exchange API, with a controlled
issuer transport. It is `TestCIControlledDemonstration` in
`internal/control/ci_demo_test.go`; real VM execution requires an explicit
`LAMP_CI_LIVE_CONFIG`. It is not an automatic VM smoke or a release gate.

* A controlled GitHub API supplied immutable demand, reviewed workflow content,
  one-job JIT configuration, cancellation and log-download responses. **No real
  runner was registered on GitHub.** The fixture's `run.sh` is a workload wrapper,
  not the upstream Actions runner; native scheduling/transport is not proven.
* The actual production `job_create` handler allocated sandbox
  `sb_22c2a1252611aedf7dc894b017315df8` for job
  `jb_d5a148f674d1f3fe647d23b16da289d4`, with a new Task. The root-owned installed
  guard attachment had an immutable deny-by-default allowlist, no private
  exceptions and no credential-subscription route. The controller verified that
  attachment before releasing the fixture's one-job authority.
* The VM compiled upstream **madler/zlib v1.3.1** into `minigzip`, compressed its
  README, decompressed it and compared the bytes. The workload wrote a private
  marker. Completion deleted the VM. Its log was published to a real T19 durable
  artifact and read back byte-for-byte **after sandbox deletion**.
* Job `jb_88193ca650d8be50bd2b6ecc7e56a090` got distinct sandbox
  `sb_c83b583d1b7d8164e8c0553e535d3513`. Its wrapper first proved the preceding
  job's private marker was absent. A public `job_cancel` request asked the
  controlled forge to cancel native authority, then stopped/released/deleted
  this real VM. The job became `canceled`.
* A trusted private fixture job, `jb_b86bb9c67e84bb9720d89cc1ccda2a89`, used
  sandbox `sb_4e8ad7f9314bbfcd95300a2d50ad9aaf`. Trusted retention suspended and
  released its execution after runner exit/deregistration. Resuming with the
  ended job's Task was refused. A newly created budget and Task resumed the
  saved state and read its marker from retained guest memory. Inspection then
  deleted the sandbox. The fixture recorded one issued/revoked runner authority
  per job and one native cancellation request.
* The proof-bound exchange returned a short-lived resource/action-scoped key.
  It could perform its allowed read, could not list keys, and could not replay
  the exchange. The driver scanned its durable state and found neither the JWT
  nor the reusable bearer. The RSA issuer was controlled, **not GitHub**.

The final check reported **zero live Firecracker processes**. A separate
inspection found no remaining capture directories in this demonstration's
private supervisor store. Prepared public-source templates/seeds remain as
fixtures, not running sandboxes or private job roots. No speed target was
measured; Go's ordinary elapsed-time lines are not benchmark evidence.

## Controlled demonstration safety and reproduction

Only the owner's Ubuntu laptop ran VMs. Before each allocation (including the
new-Task inspection resume), the shared Firecracker count was below two and
available memory was at least 12 GiB. The controller uses the same checks. Other
tickets' services and VMs were left alone; some early iterations observed one
of their VMs. No existing Docker resource, owner project, browser profile,
visible window, tailnet or everyday service was used as a workload or changed.
No paid service was purchased and no model action was executed.

The disposable store was `/var/lib/lamp/t26-private`, with its own supervisor,
network namespaces, UID base, grants and audit root. The worker and guard were
existing T19/T12 builds, not private RPCs added to an independent adapter.
[`demonstrated-sha256.txt`](evidence/t26/demonstrated-sha256.txt) pins the worker,
guard and Linux source files used in demonstration 11. Later small local input,
label and quota hardening was compiled; the [local controlled-identity portion](evidence/t26/local-identity-final.log) also passed without VMs. It is not relabeled as the exact demonstrated VM binary.

Reproduction helpers are beside the logs:

1. [`prepare_zlib.py`](evidence/t26/prepare_zlib.py) downloads public source,
   checks upstream archive SHA-256
   `17e88863f3600672ab49182f217281b6fc4d3c762bde361935e436a95214d05c`, and keeps
   the required build sources plus LICENSE. Generated CRC tables are omitted;
   the public code is compiled with `DYNAMIC_CRC_TABLE` instead. No third-party
   source executes on the host.
2. [`build_template.py`](evidence/t26/build_template.py) uses an existing clean
   public Ubuntu seed and the dedicated disposable store to prepare a fixture
   template. It checks the machine limits before the builder runs. The final
   fixture template was `tp_0ed22cbc1ffee6af3aca60d50d`.
3. [`run_demo.py`](evidence/t26/run_demo.py) enrolls a short-lived supervisor
   grant, writes private worker identity configuration, runs the one explicit
   Go demonstration and stops its supervisor/revokes its grant in `finally`.
   It expects this checkout's Go sources synchronized to `~/lamp-t26/src`.
   Its installation paths are the actual lane paths and need adjustment on
   another enrolled host. No credential/config file is included in evidence.

Only iteration-required builds and this demonstration were run. No full smoke
suite, lint suite, benchmark, attack matrix or release gate was added or run.

## Failed development observations stay failed

All original logs remain in [evidence/t26](evidence/t26/):

* 01 failed because the first synchronized source omitted the existing test
  harness's CLI build inputs.
* 02–03 failed on an old guard binary lacking T12 allowlist fields. They also
  exposed that a failed boot could leave an allocation when stop's response was
  not `stopped`. Deletion now attempts generation-bound release even when stop
  fails; release itself tears down the jail and namespace.
* 04–05 failed because a fixture distribution put under the builder's `/tmp`
  was not in the template's persistent disk. The public fixture moved to `/opt`.
* 06–07 failed because the clean seed had a compiler but not `make`. The final
  fixture compiles the public zlib sources directly with `gcc`.
* 08 completed its public workload but failed on observation ordering: runner
  exit preceded the forge's completion observation. The controller now briefly
  reconciles that native result without rerunning the runner.
* 09–10 demonstrated completion/cancellation/output persistence. 11 additionally
  demonstrated trusted stopped retention and new-Task inspection.

These are development results, not relabeled successes or certification rows.

## Historical signup blocker, before private-fixture authorization

The following account-access limitation describes the earlier demonstration
only. The coordinator subsequently authorized the private owner fixture above;
no further signup attempt was made.

Unauthenticated headless access to `https://github.com/signup` returned **403**
on the M5, Ubuntu and Air. The M5's normal GitHub homepage and login page were
reachable; account creation specifically was unavailable. No account or email
verification step was reached, so no verification code was requested. The
owner's authenticated `gh` account was used only for public upstream metadata
and this ticket's PR, never as the disposable CI fixture.

At that point there was no hosted run URL to supply. Native JIT execution,
GitHub OIDC, a stranger's actual public PR and the coding-agent routes were then
unfinished. The native observations above supersede that account-access blocker,
not the preserved failed development conclusions.
`docs/ci.md` documents the implemented setup and these limitations; example
workflows are outside `.github/workflows` and did not start owner-machine jobs.
Lamp's existing private persistent CI runners were not attached elsewhere.

## Broker-route agent attempts that failed (kept failed)

Before the passing runs: Claude runs 42977215770 and 42979631446 were cancelled,
and 42980322884 and 42981371273 ended `is_error:true` with no reply (fixed by
also giving the action `ANTHROPIC_AUTH_TOKEN`; free-model routing was also flaky).
Codex 42983775174 failed on missing `sudo`, 42984094522 on `SCOPE_DENIED` for the
hosted `web_search` tool, and 42985020769 hung until the job timeout with its log
lost; the 300 s grant TTL had expired by then. Cause not established.
