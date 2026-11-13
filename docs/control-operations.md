# Control operations in the shipped daemon

`lamp --help --json` lists the connected daemon's installed operations. Without
an available daemon it lists the binary's compiled handlers. `lamp api describe`
returns OpenAPI paths for those installed handlers; MCP `tools/list` discovers
the same daemon set. The full versioned schema remains in `contracts/public`.
Installed operations can still require owner enrollment (for example a VM
supervisor, a session harness, CI credentials, or notification destinations).
An unavailable subsystem is not installed merely to inflate the catalogue.

## Connect CI

`lamp plugin list` shows the ten shipped CI adapters and whether each is enabled.
`lamp adapter list` shows their adapter identities. Credentials and project
policy are enrolled by the host owner in a private `lampd --ci-config FILE`.
For example, enroll a disposable GitLab project as follows:

```json
{
  "poll_seconds": 10,
  "network_reader": ["/trusted/ci-network-attestation", "/private/guard-audit"],
  "repositories": [{
    "system": "gitlab",
    "name": "disposable-account/public-source",
    "project": "123",
    "base_url": "https://gitlab.example",
    "token_file": "/private/gitlab-api-token",
    "label": "lamp",
    "template_id": "tp_clean_runner_template",
    "runner_directory": "/opt/gitlab-runner",
    "options": {"project_id": "123"},
    "retain_stopped": false
  }]
}
```

The credential file must be private. The GitLab account needs permission to
read the project and its jobs, create project runners, retrieve logs/artifacts,
and remove those runners. The template contains a clean, unregistered
`gitlab-runner` distribution, Python 3, and the workload's tools. Workflow jobs
select the enrolled `lamp` runner tag. The supervisor must enforce a fixed
deny-by-default network ceiling permitting the CI service and required public
build dependencies. `network_reader` reads its real per-generation attachment;
it cannot be an assertion supplied by a workflow. For a self-hosted private CI
endpoint, enroll its exact `IP:port` in `options.private_endpoint` and the matching
supervisor exception. See [CI execution and isolation](ci.md).

Forgejo uses `system: "forgejo"`, `pg_forgejo`, a Forgejo 15+ service, a clean
`/opt/forgejo-runner` distribution with runner 12+, and a `runs-on: lamp` job.
Use `id` for its numeric repository ID and `workflow_sha256` to pin reviewed
`.forgejo/workflows/ci.yml` bytes. Gitea and the other shipped adapters appear in
`plugin list`; enrollment always selects the service's native runner contract.

```sh
lamp plugin get pg_gitlab --json
lamp plugin test pg_gitlab --expected-revision 1 --json
lamp job create --plugin-id pg_gitlab \
  --external-id disposable-account/public-source/456 --json
lamp job list --json
lamp artifact list --json
```

`plugin test` reads the provider's actual job queue and validates private
credential availability; it does not claim a VM job succeeded. `job create`
verifies demand with the CI system, allocates a fresh VM, uses a one-job runner,
saves outputs, removes runner authority, and deletes the sandbox by default.
Public/untrusted jobs receive no model broker grants. Owner project policy can
retain a stopped **trusted** job explicitly; public jobs are always deleted.

Built-in packages are immutable. To re-enable a revoked, enrolled adapter, use
`plugin create` with its `package_blob_id` (`builtin:pg_gitlab`) and `digest`
from `plugin get`. `plugin delete` stops further intake; accepted work keeps its
cleanup authority. `plugin update` updates labels. Arbitrary executable blobs
and HTTP-supplied credential policy are not accepted by this built-in installer.

## Revoke keys and admit a second caller

`lamp key revoke` is an alias for the canonical `key_delete` operation: HTTP
`DELETE /v1/keys/{key_id}` and MCP `key_delete`. Revocation takes effect on the
next authentication. The mutation and operation status report host enforcement
acknowledgments separately from the durable revocation receipt.

```sh
lamp fleet update fl_local --mode team --expected-revision 1 --json
lamp principal create --issuer local --subject bea --json
lamp membership create --principal-id pr_BEA --role member --json
lamp key create --principal-id pr_BEA --expires-at FUTURE_UTC_TIME \
  --scope-json '{"actions":["session.get","session.watch","session.input","lease.create"]}' --json
```

Only the owner may issue a key for another admitted principal. Its effective
scope and expiry remain bounded by the issuer's authority. Capture the returned
secret directly into a private file or variable; never copy it into logs. It is
returned once, with no recoverable copy in the journal.

For an owner-enrolled session, grant watch access explicitly:

```sh
lamp share create --resource-kind session --resource-id ss_SESSION \
  --subject-id pr_BEA --actions-json '["session.get","session.watch"]' --json
```

Bea can get/watch that session, and cannot acquire its input lease. Revoke that
share, then create a separate share adding `session.input` to grant typing.
Typing also requires Bea to acquire the exclusive input lease (`lease create`,
`type: input`, the session's resource ID, expiry within five minutes). Each input
uses the session's current revision/location epoch and the lease's next sequence.
Revoking the share also revokes its leases; subsequent session reads return 404.
These session grants do not disclose the underlying private sandbox or template.

## Approve one exact effect

A protected action must have an enforcing adapter. The daemon can now enroll a
fixed JSON webhook using `--approval-actions-config /private/actions.json`:

```json
[{
  "url": "http://127.0.0.1:9000/effect",
  "bound": {
    "principal_id": "pr_OWNER",
    "task_id": "tk_TASK",
    "sandbox_id": "sb_SANDBOX",
    "generation": 1,
    "resource_revision": 1,
    "action_plan": {
      "operation": "post",
      "resource_ref": {"kind": "connection", "id": "cn_notice"},
      "arguments": {"message": "the exact effect to approve"}
    }
  }
}]
```

The private regular enrollment file fixes the principal, task, sandbox,
generation, resource revision, full endpoint/path, and exact JSON body. This
adapter supports credential-free POSTs; do not put secrets in the body. It
refuses redirects, derives the destination and adapter digest from enrollment,
and returns no webhook response body. Credentialed integrations belong in the
broker rather than this adapter.

`lamp docs read --topic approval-actions --json` returns the caller's bound plans.
Create an approval using that exact `action_plan` and `destination`, task,
sandbox, generation, resource reference and an expiry at most 60 seconds ahead.
`approval get` exposes the real effect and `action_digest`. The owner approves
that digest with `approval approve` and the current revision. Invoke it with
`connection invoke cn_notice`, `attachment_id` equal to the enrolled sandbox ID,
`operation: post`, the exact `arguments`, the approval ID and enrolled resource
revision. Consumption is durable before dispatch; a new invocation cannot replay
that approval, including after daemon restart. A retry with the original
idempotency key returns the original receipt. A delivery with an uncertain
outcome is never retried automatically.

## Notifications

Start a local webhook receiver supporting POST. Create a private signing key
file and enroll its destination with `lampd --notifications-config FILE`:

```json
{
  "destinations": {
    "local": {
      "url": "http://127.0.0.1:9000/notifications",
      "secret_file": "/private/notification-signing-key"
    }
  }
}
```

Then run:

```sh
lamp notification-sink create --plugin-id webhook --destination-ref local --json
lamp notification-sink list --json
lamp notification list --json
```

`destination_ref` is the key `local` in owner enrollment, **not an exposure ID**.
No port/exposure API is needed. Stopped/completed tasks produce `task.done`,
watchdog-blocked tasks produce `task.stuck`, and pending approvals or blocked
sessions produce `task.needs_decision`. Failures of arbitrary API requests are
not task lifecycle events. Each delivery includes `X-Lamp-Signature` with
`sha256=HMAC-SHA256(signing-key, raw-body)`. Delivery attempts and retries are
durable and inspectable with `notification get`.

## Enrolled control grants

`grant list/get` exposes the host's supervisor-enrolled control grant; the public
API does not mint supervisor/handle authority. `grant revoke/delete` durably
fences new execution and causes the daemon to release active sandboxes through
its existing authority monitor. Deletion and cleanup remain permitted. An old
grant stays revoked across restart; the custodian must enroll a **new** supervisor
grant to restore execution. Receipts distinguish host cleanup acknowledgment
from admission. These are daemon control grants, not broker credentials or
plugin worker handle grants.
