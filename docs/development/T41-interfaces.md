# T41: CLI, MCP and HTTP interfaces

`lamp --help`, `lamp sandbox --help` and `lamp sandbox create --help` show
human help. `--help --json` retains the machine catalogue and uses the connected
daemon's installed inventory when it can be read; offline help falls back to
this build's installed handlers. MCP tools/list uses the same discovery.
`GET /v1/openapi.json` publishes installed paths; the checked-in contract remains
the complete vocabulary for feature owners. Fleet hosts appear on connected
fleet catalogues. Human help and examples work offline.

Use schema field names with hyphens (`--resources`, `--labels`, `--scope`,
`--argv`); existing `-json` aliases still work. Objects and arrays take JSON.
`--socket`, `--api-url`, `--token-file` and `--json` work before or after the
command. Positional IDs merge with matching IDs in `--input`; conflicting IDs
are rejected. `exec list --sandbox-id ID` is a server-side filter, applied before
pagination; cursors bind the filter.

CLI and MCP read LAMP_TOKEN_FILE, or LAMP_TOKEN, and discover credentials and
socket from the installation containing their executable. An explicit token
file wins. A development build falls back to the development daemon socket.
MCP --help includes a stdio client configuration example.

With `--json`, success is on stdout and failures are on stderr, one JSON document
per call. The exit codes are 0 success, 1 API/operation failure (including not
found), 2 local usage/validation, 3 unauthenticated, 4 scope denied, 5 conflict,
precondition or expired result, and 6 transport or wait timeout. Non-JSON lists
show tables and empty-list guidance. Slow calls and waits report progress on
stderr; `--json` suppresses progress. An accepted operation is displayed as
accepted, never as succeeded. A terminal failure remains a failed operation
envelope, gives a nonzero CLI exit, and sets MCP isError.

CLI mutations fetch the current resource revision when omitted. Explicit stale
revisions still conflict, and concurrent edits are never automatically retried.
New records store optional `retry_hash_v1` HMAC metadata alongside the original
hash, so retrying a saved intent can refresh its revision precondition without
re-executing it. Old journal records retain their original hash semantics.
Different operation inputs still conflict. Secrets and private screenshot
observations retain their one-time delivery rules.

`lamp sandbox wait ID --state ready --timeout 60s` polls readiness and reports
failed/deleted/unavailable states. `lamp operation wait ID --timeout 60s` waits
for a terminal outcome and exits nonzero on failure. API and MCP retain their
canonical operation_wait timeout_ms field (at most 60000 per request).

SSE `limit` bounds the total number of events in that response (default 100,
maximum 1000). `wait_ms` bounds the response duration (default 0, maximum 25000).
Resume with `cursor=<last-event-cursor>`. `lamp event list --follow` reconnects
these bounded streams from the last cursor until interrupted. It defaults to a
25000 ms wait per connection.

Create validates the published input before dispatch. Unsupported host fields
are rejected by name with the action to take; they are never silently ignored.
Update similarly names fields this host cannot update. NUL bytes in strings or
field names are refused with their JSON path. Failed-sandbox exec reports
FAILED_PRECONDITION and the sandbox's actual state. Revision conflicts include
the current revision in the message (and engine mutations include details).

Schedules publish last_run_at and last_run_status for the newest started
occurrence and update its final result durably. An older overlapping run cannot
overwrite the newest occurrence's status.

Keys accept several actions. The existing security policy requires an explicit
expires_at, now required by the published create schema and shown in help:

```sh
lamp key create --scope '{"actions":["sandbox.get","exec.create"],"sandbox_ids":["sb_example"]}' --expires-at 2026-12-01T00:00:00Z --json
```

REST resource_ids uses `{kind,id}` references. For convenience, the CLI also
accepts sandbox ID strings in --scope resource_ids and expands them to references.
An invalid action's error includes its array index.

## Demonstration

Recorded output: [interfaces.json](evidence/t41/interfaces.json). Built the real
`cmd/lamp`, `cmd/lampd`, `cmd/lamp-mcp` and `cmd/lamp-installer` into
`/tmp/lamp-t41-bin` and ran a disposable daemon under `/tmp/l41-le9r05mo` with a
fresh owner credential. No owner projects, credentials or sandboxes were used.

The real CLI, HTTP API and MCP each reported 192 installed operations. Successful
principal creates and failed sandbox creates replayed with the same operation
ID on all three fronts. The failed operation survived daemon restart. The
engine refusal was HOST_UNAVAILABLE because this disposable daemon had no
enrolled supervisor; it was not a successful VM demonstration.

The run also exercised human help and typo suggestions, token-file-only MCP,
readiness and operation waits, exec filtering, a two-action key bound to one
sandbox, named unsupported fields, NUL rejection, errors on stderr with exits
2/1/4/5, automatic revision lookup and replay after revision change, and matching
positional/input IDs. SSE returned two events, resumed after the last event and
closed an empty stream at its 250 ms deadline (observed 252 ms).

A disabled schedule was explicitly run once against an unavailable worker. Its
last_run_at was populated and last_run_status changed from running to failure
when daemon shutdown canceled the run; the final status survived restart.
At that stage, a successful VM-backed schedule and a sandbox reaching ready
were not run. The later real-VM readiness demonstration below covers readiness;
a successful VM-backed schedule remains unobserved.


## F101: real Ubuntu VM readiness and immediate exec

On 2026-11-13 UTC, the CLI and daemon built from `ae6eb5d` ran on the owner's
Ubuntu laptop against a separately started Firecracker supervisor. A disposable
identity and store used a copy of the public Ubuntu 26.04 template
`tp_42736e322660f095381c2397a6`; no owner project was mounted. The supervisor
trusted its producer's public verification key; signing keys stayed in place.
The requested 1 CPU / 1024 MiB / 20 GiB shape cold-booted the template.
Immediately before create, there were 0 live Firecracker processes and 19,115 MiB
available. Only one sandbox was allocated at a time, using one shared slot.

Raw commands, stdout, stderr, exit codes and decoded guest output:
[vm-readiness.json](evidence/t41/vm-readiness.json).

```text
lamp sandbox create --template-id tp_42736e322660f095381c2397a6 --resources '{"vcpus":1,"memory_mib":1024,"disk_gib":20,"pids_max":4096}' --wait-ms 0 --json
  accepted: sb_0574d6b70006aeccd7b9d43a34d0c50d
lamp sandbox wait sb_0574d6b70006aeccd7b9d43a34d0c50d --state ready --timeout 60s
  stderr: Waiting: preparing
          Waiting: ready
  exit: 0; boundary: vm; state: ready
lamp exec create --sandbox-id sb_0574d6b70006aeccd7b9d43a34d0c50d --argv '["sh","-c","echo T41-immediate-exec-after-readiness-wait; uname -s; cat /etc/os-release"]' --wait-ms 60000 --json
  exit: 0; exec: ex_90b4d1950c17bef6d8c81b1bf7246f62
```

The exec command immediately followed wait, with no probe, sleep or retry
between them. Its process reached `exited`, `exit_code: 0`; `lamp exec watch`
returned stdout frames containing:

```text
T41-immediate-exec-after-readiness-wait
Linux
PRETTY_NAME="Ubuntu 26.04.1 LTS"
```

The sandbox was deleted immediately after collecting output: deletion succeeded
with cleanup complete, and 0 Firecracker processes remained. The temporary
grant was revoked, the owned daemon and supervisor stopped, its private tmpfs
unmounted and its disposable store removed. Existing services were kept running.

The wrapper's output assertion failed because it filtered `Frame.stream`
instead of the public `Frame.channel`. The recorded watch response already
contained the guest output; the decoded text above was recovered from those
frames afterward. This was a collector error, without a product change or
another VM run. Earlier attempts are retained in
[vm-prior-attempts.json](evidence/t41/vm-prior-attempts.json): a saved-template
guest-handshake failure, a readiness-only run whose output was fetched before
process completion, and a readiness timeout. Each sandbox was deleted. Shared
supervisor shutdowns also interrupted setup; the isolated supervisor initially
needed a longer startup wait to finish verifying the copied template.

## F97: command and flag suggestions

Fresh executions of the built CLI produced the following stderr, each with
exit 2 and empty stdout. Raw evidence:
[typo-suggestions.json](evidence/t41/typo-suggestions.json).

```text
$ lamp sandbx list
lamp: unknown command "sandbx list"; did you mean "sandbox list"?
$ lamp sandbox create --templat-id unused
lamp: flag provided but not defined: -templat-id; did you mean "-template-id"?
```

These additions change documentation and evidence only. Product behavior is
unchanged; the existing two-round Sol APPROVE remains the review verdict.
