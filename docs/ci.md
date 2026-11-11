# GitHub CI on fresh Lamp computers

**Implementation status:** the controller and keyless exchange are implemented.
The real-VM demonstration uses a controlled forge and runner wrapper, **not a
hosted GitHub job or a GitHub-issued JWT**. Native Actions, Copilot, Claude Code,
Codex and a stranger's hosted PR remain unverified: disposable GitHub account
signup returned HTTP 403 on all three owner machines. See [T26 evidence](development/T26-evidence.md).
Do not describe this ticket as fully done from that demonstration.

## Connect a controller

Use a dedicated enrolled **Linux x64 Firecracker supervisor**, a clean signed-out
Ubuntu template with Python 3 and an installed GitHub runner distribution, and
T12's immutable network ceiling. Runner dependencies belong in the template,
not in a privileged host setup step. For example, upstream runner `v2.338.2`
Linux x64 archive SHA-256 is
`baebf204307bac6193480852732f0aba2ae97b6c5597c244f4d5558390147c40`.
Verify upstream release inputs when preparing your template; the enrolled
immutable template ID pins what actually runs. Docker/service-container jobs
also need T22's guest kit; this controller does not silently fall back to host
processes, Docker on the host, a Mac runner, or a persistent runner.

Keep the GitHub App installation token in a host-only mode-0600 file, rotated by
the installation's custodian. Required repository permissions are Actions
read/write (demand, cancellation and outputs), Contents read (workflow identity)
and Administration write (JIT runner registration/removal). Select only the
disposable or production repositories explicitly enrolled for this controller.
The host token is **never given to a VM**. This is not a stored Lamp secret.

Pass `lampd --ci-config /private/github.json` alongside its normal enrolled worker
arguments. Example policy (IDs/digests below are placeholders, not credentials):

```json
{
  "token_file": "/private/github-installation-token",
  "network_reader": ["/trusted/read-ci-network", "/var/lib/lamp-audit/ci"],
  "poll_seconds": 10,
  "repositories": [{
    "name": "disposable-account/public-project-fork",
    "id": 12345,
    "owner_id": 67890,
    "label": "lamp",
    "template_id": "tp_clean_runner_template",
    "runner_directory": "/opt/actions-runner",
    "workflow_sha256": {".github/workflows/ci.yml": "SHA256_OF_REVIEWED_WORKFLOW_BYTES"},
    "retain_stopped": false
  }],
  "oidc": []
}
```

`poll_seconds: 0` disables polling; an authenticated host controller can instead
submit `job_create {plugin_id: pg_github, external_id: OWNER/REPO/JOB_ID}`.
Neither a workflow key nor a caller-supplied trust badge can assert job trust,
select an existing sandbox, attach a cache/secret, or override the enrolled
source. Forge API evidence supplies repository/owner IDs, run attempt, event,
visibility, SHA and requested labels. Workflow bytes must match an enrolled
SHA-256, including for trusted work. Privileged `pull_request_target` and
`workflow_run` routes are refused. A workflow edit needs explicit host review
and a new digest, not an override supplied by the PR.

Each job requests both `lamp` and a per-run-attempt label, for example:

```yaml
runs-on: [self-hosted, lamp, 'lamp-${{ github.run_id }}-${{ github.run_attempt }}-build']
```

Use a distinct suffix for each job. Matrix jobs also need a distinct suffix per
matrix entry. The controller refuses ambiguous same-label queued jobs, unknown
runner labels and unsupported architectures. Labels route jobs; they are not
credentials or an authorization boundary. GitHub's fork-token restrictions and
Lamp's no-secret clean VM remain necessary even with these labels.

`tools/ci-network-attestation.py` is a read-only helper for the supervisor's
actual root-owned attachment, not a policy-setting command. Install it trusted
and fix its audit directory in the host's narrow privileged invocation. The
controller appends sandbox ID and generation. It checks the installed
`require_allowlist`, immutable deny-by-default ceiling and absence of private
exceptions or subscription credential routes **before issuing JIT authority**.
An unreadable/missing attachment fails closed. The ceiling must permit the
approved GitHub Actions endpoints and the workload's public dependencies; T12
transport rules are numeric CIDR/port allowlists, not a claim of TLS-site
mediation. LAN/host/metadata restrictions and IPv6 denial remain in the guard.

## Native execution and cleanup

GitHub's JIT configuration creates an ephemeral, one-job runner. Lamp passes
that configuration only over its private worker envelope, never the public
exec journal or API response. Each runner runs from a new copy of the clean
distribution in its own VM. Templates containing a prior runner registration,
credentials or `_work` are refused. The native runner, rather than a simulated
status adapter, is responsible for GitHub steps, post-job actions, results,
logs and `upload-artifact`. Cancellation asks GitHub to cancel the run, then
stops and deletes execution. GitHub cancellation is run-wide, not a promise of
individual sibling-job cancellation that its public API cannot provide.

Job states are `queued → preparing → running → collecting → completed|failed|canceled`.
Incomplete revocation/release is `cleanup_failed`; `job_reconcile` retries it.
The external job claim is durable and duplicate intake returns the existing
job, never another runner. Recovery never restarts an uncertain runner: it
finds even a JIT registration whose response was lost, cancels native authority,
collects outputs and releases the allocation. A successful runner exit gets a
brief native-result reconciliation period; it is never rerun to fabricate a
result. The controller currently admits one active job at a time and observes
the owner's shared Ubuntu process-count/memory limits before allocating.

Native GitHub logs and artifacts outlive the VM according to GitHub retention.
Lamp also captures native job logs and **run-level** artifact archives into
T19's durable artifact store, up to 32 MiB total per job and the control-record
quota. Run artifacts are not falsely attributed to an individual step/job.
Missing, not-yet-ready or oversized output is `incomplete`, not a fabricated
complete download. Its native GitHub source remains available; capture is not
a performance or certification gate.

Trusted-only `retain_stopped` first deregisters authority, waits for the runner
to exit, removes its registration/transport distribution and temporary files,
keeps `_work` under `/var/tmp/lamp-ci-inspection`, and durably suspends/releases
the VM. If safe retention cannot be completed, Lamp deletes execution instead.
Public/PR work always deletes regardless of this policy. The old Task is
stopped. Inspection uses a separately authorized new Task and budget:

```text
sandbox_resume {
  sandbox_id, expected_revision,
  continuation_task_id: NEW_TASK,
  expected_task_id: OLD_JOB_TASK
}
```

Old exposures cannot be revived. The next CI job still gets a different fresh
sandbox. Inert suspended state is not erased just because the old Task ended;
normal saved-state retention and explicit deletion still apply.

## Keyless trusted workflow connection

Enroll an exact `oidc` row with `repository_id`, `repository_owner_id`,
`workflow_ref`, `workflow_sha`, `ref`, `event`, `resource_ids` and `actions`.
Policies permit resource-scoped observations and task exec, not key minting,
job trust assertions or fleet management. Workflow SHA is the immutable GitHub
claim, not the YAML file's SHA-256. Public/unknown visibility and PR identities
cannot receive a Lamp key, even when bootstrap input pretends otherwise.

A trusted private workflow with `permissions: {id-token: write}` can run:

```sh
python3 tools/github-oidc-exchange.py \
  --api-url https://YOUR_ENROLLED_LAMP_ENDPOINT \
  --output "$RUNNER_TEMP/lamp-key"
lamp sandbox get YOUR_AUTHORIZED_SANDBOX \
  --api-url https://YOUR_ENROLLED_LAMP_ENDPOINT \
  --token-file "$RUNNER_TEMP/lamp-key" --json
rm -f "$RUNNER_TEMP/lamp-key"
```

The helper generates an ephemeral Ed25519 key, bootstraps a two-minute challenge,
requests GitHub's JWT for that exact audience, signs canonical sorted JSON of
`auth_flow_id,challenge,jwt_sha256,audience`, then exchanges once. Lamp verifies
RS256 using the fixed GitHub issuer's JWKS, times, audience, proof and immutable
identity policy. The issued key expires at the earlier of ten minutes and JWT
expiry. Only its hash is persisted. Replay is refused; neither JWT nor reusable
key is journaled or printed. Bootstrap has a bounded pending-flow allowance.
Use T18's authenticated HTTPS transport; do not expose an unauthenticated
plaintext control API to make this work.

## Coding-agent routes

* **Copilot cloud agent:** select Lamp labels in the exact
  `copilot-setup-steps` job of `.github/workflows/copilot-setup-steps.yml`.
  GitHub documents self-hosted Ubuntu x64 support and recommends ephemeral
  single-use runners. Its integrated firewall must be disabled for that route;
  Lamp's host allowlist stays enabled. This covers GitHub Actions placement,
  **not** placement of vendor-hosted cloud-sandbox sessions. GitHub may synthesize
  workflow identities/demand differently; this route has not been demonstrated
  against the live service and must not be advertised as validated.
* **Claude Code Action / Codex Action:** ordinary native action steps on the
  one-job runner, under approved trusted-job auth. Examples are under
  `examples/ci/`. Public PR policy rejects model secrets and elevated workflow
  permissions. Do not place owner/subscription/model credentials in a public
  job's VM. Existing custody/broker integration is required for subscription
  authentication; these examples do not implement a new auth or spending path.

Official references consulted:
[GitHub JIT runner API](https://docs.github.com/en/rest/actions/self-hosted-runners#create-configuration-for-a-just-in-time-runner-for-a-repository),
[Copilot development environments](https://docs.github.com/en/copilot/customizing-copilot/customizing-the-development-environment-for-copilot-coding-agent),
[Claude Code Action](https://github.com/anthropics/claude-code-action),
[Codex Action](https://github.com/openai/codex-action).

## Other CI adapters

The durable `Job` resource, idempotent external claim, Task/budget binding,
fresh-VM admission, artifact publication, cleanup/reconciliation and explicit
new-Task inspection are shared control-service behavior. `internal/ci` owns
forge protocol and verification; `internal/control/ci.go` owns execution and
durable lifecycle. T29 can install another verified intake/one-job authority
adapter at this boundary without adding private worker RPCs to an independent
adapter or reusing GitHub's trust assertions. No other CI provider is claimed
implemented here.
