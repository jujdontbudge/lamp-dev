# T17 host broker

`go build ./cmd/lamp-broker` builds a **separate per-owner Go process**. It is
not an in-sandbox credential helper. The initial supported surface is explicit
broker HTTP; the canonical control API's connection/settings resource lifecycle
is not wired to this process yet. No existing unavailable operation is falsely
reported as installed.

## Host setup

Supply a private `0600` JSON configuration, a private owner-socket directory,
and a new audit filename:

```sh
lamp-broker --config /private/owner/broker.json \
  --owner-socket /private/owner/broker.sock \
  --task-listen 127.0.0.1:7778 --audit /private/owner/audit-new.jsonl
```

Non-loopback task listeners require `--tls-cert` and `--tls-key`. Install only
the public certificate/CA in tasks; never disable certificate verification.
Upstreams require verified HTTPS (explicit host CA additions are supported);
a service on numeric loopback alone may use HTTP. No ambient HTTP proxy,
redirect, upstream cookie jar or guest header forwarding is used.

The owner socket authenticates the OS peer UID as well as requiring private
socket permissions. Do not mount it into a task. The broker disables core dumps
before loading configuration, and Linux additionally disables process dumping.
The OS owner, trusted adapter configuration, approved credential recipient and
credential-store reader are trusted. macOS privileged debugging is not prevented.

Example configuration (no credentials in this file):

```json
{
  "connections": {
    "models": {
      "kind": "model",
      "access": "full",
      "endpoint": "https://openrouter.ai/api/v1/chat/completions",
      "credential_command": ["/absolute/path/to/owner-store-reader"],
      "models": ["owner-selected-free-model-id"],
      "operations": {
        "chat": {
          "write": true,
          "fields": {"model":"string", "messages":"array", "max_tokens":"number"}
        }
      }
    },
    "github": {
      "kind": "cli", "adapter": "gh", "binary": "/opt/homebrew/bin/gh",
      "resource": "cli/cli", "access": "read_only",
      "operations": {"repo_view":{"write":false}}
    }
  },
  "settings": {
    "instructions": {"root":"/private/owner/settings", "paths":["AGENTS.md"]}
  }
}
```

A credential reader is an absolute, trusted executable, invoked without a shell.
It reads an existing OS-backed/encrypted store and writes **only** the bearer to
its private stdout pipe. Its stderr, errors and invocation output never reach a
task or log. Do not put a credential in command arguments, the configuration,
environment or a new plaintext credential file. There is no plaintext fallback
and no automatic credential export. Official host CLIs discover their own
existing sign-in; no task-controlled executable, environment or arbitrary flags
are accepted. The demo's synthetic credential is held in a host fixture's memory
and delivered over a private Unix socket, not written to a credential file.

## Owner and task protocol

The owner issues `POST /owner/grants` on its Unix socket:

```json
{
  "sandbox_id":"sb_assigned", "generation":1, "peer_ip":"192.0.2.10",
  "ttl_seconds":300,
  "connections":{"models":["chat"],"github":["repo_view"]},
  "settings":["instructions"]
}
```

It returns a one-time-visible Lamp `token`, `grant_id` and expiration. Only its
hash is retained by the broker. This is **not** a provider credential: it is
restricted to the exact connection/operation set, assigned source IP, sandbox
and generation record and at most five minutes. The trusted owner must deliver
it solely through the assigned sandbox's protected channel. Behind NAT, source
IP cannot independently distinguish two guests; use per-sandbox channels and do
not treat a guest-supplied ID as attestation. Automatic control-core attachment,
fork reauthorization and fleet revocation remain integration work.

Tasks send this token as their bearer to `POST /v1/invoke`:

```json
{"connection_id":"github","operation":"repo_view","arguments":{}}
```

For base-URL-aware OpenAI-compatible clients, use
`https://broker/v1/models/models` as the base URL and the short-lived Lamp token
as the API-key value. `POST .../chat/completions` returns the provider's chat
response directly. To retry an owner-approved chat action, send its ID as
`X-Lamp-Approval-Id` or a top-level `approval_id` body field. If both are supplied
they must agree; this metadata is consumed by the broker and never forwarded to
the model provider. Only allowlisted models, bounded string-only chat messages,
`max_tokens` (1–4096), and optional configured `temperature` are accepted.
This is not transparent interception of vendor TLS. Streaming, tools, images,
HTTP/2/WebSocket interoperability and clients that cannot select a base URL are
unsupported, never grounds to inject a real key. No timing/throughput claim is
made.

`POST /owner/revoke {"grant_id":"..."}` fences in-flight calls before returning;
it cannot undo already committed upstream effects. All grants and approvals
expire on broker restart. Operation retries are **not** automatic; a dispatch
failure can be `OUTCOME_UNKNOWN`. Callers must inspect remote state before retry.

## Four service access modes and approvals

Each connection's `access` is `off`, `read_only`, `ask_first` or `full`. The owner
can change it through `POST /owner/access/<connection> {"access":"..."}`; this
invalidates its outstanding approvals. `full` still cannot escape the configured
endpoint/resource/operation/input schema or the task's grant.

Services have exact method/path operations and closed input schemas, e.g.:

```json
{
  "kind":"service", "access":"ask_first", "endpoint":"https://service.example",
  "operations": {
    "read_notes":{"method":"GET","path":"/notes","write":false,"semantic_read":true},
    "append":{"method":"POST","path":"/notes","write":true,"fields":{"body":"string"}}
  }
}
```

`semantic_read` is the **trusted adapter author's assertion** about the concrete
operation, not an inference from GET or a task's declaration. Review the service
implementation or use an actual read-only database role before enabling it.
There is deliberately no arbitrary SQL or raw TCP adapter. Fields may be
`string`, `number`, `boolean`, `object`, `array`, with `?` for optional fields;
complex MCP/service values require a trusted upstream semantic adapter to enforce
nested resource restrictions. Do not configure arbitrary query/eval tools as a
read operation.

GET arguments are URL-encoded query parameters on the exact pinned path: strings
are literal values; numbers and booleans use JSON spelling; arrays and objects
use JSON within one query value. No GET body or argument is silently discarded.
Incoming numbers retain their JSON spelling, and valid upstream JSON responses
stay opaque through the broker, preserving integer IDs without float rounding.

Ask-first returns `APPROVAL_REQUIRED`, `proposal_id` and an action digest. The
owner reads `GET /owner/approvals/<id>`, inspects the exact action, and approves
with `POST` to that path containing `{"digest":"inspected digest"}`. The task
then echoes the approval ID in its original invocation. The approval binds the
grant, generation, connection, operation and canonical argument bytes, expires
with the grant, and is consumed before dispatch. Changed inputs and replay do
not execute. There is no task-accessible approve endpoint.

## Settings and Git signing

A source has an absolute host root and an explicit path allowlist. The owner
must select **secret-free** files (harness settings, instructions, skills, MCP
references, git identity and signing references, never signing keys). A path
allowlist does not authorize credential export: credential-bearing fields such
as npm's registry-scoped `_authToken`/`_auth`, provider API keys, passwords and
session tokens are refused regardless of their values or case. The same check
covers JSON (including nested/escaped keys and embedded settings strings) and
assignment-based settings, both before copy and before accepting a proposed
return. Private-key markers, NULs and credential-bearing URLs are also refused.
This is fail-closed rejection, not token redaction or permission to copy unknown
opaque secret stores; owners must still select secret-free source material.

1. Task `POST /v1/settings/<source>/copy {}` receives `files` and a content-hash
   `base_revision`. The task writes these bytes locally; no host mount exists.
2. `POST /v1/settings/<source>/propose` takes that revision and changed `files`.
   Nothing writes back. It returns a settings-change ID.
3. Owner `GET /owner/settings/<id>` shows both original and proposed bytes.
4. Explicit owner `POST` to the same path takes `base_revision` and a nonempty
   `selected_paths` list. Only those proposed, allowlisted paths are replaced.

All source bytes are rechecked before apply and after staging. Stale content,
symlinks and path traversal fail closed. File operations use `os.Root` and atomic
per-file replacement; hard links are replaced, not modified through. Writes and
directories are fsynced. **Multiple selected paths are not one filesystem
transaction**: an interrupted apply reports `OUTCOME_UNKNOWN`, never a false
success. Prevent other host writers while applying; uncooperative external
writers cannot be given filesystem-level compare-and-swap semantics. Approvals
are one-use and remain host-only.

`git_sign` connections use an absolute host `ssh-keygen` binary and host key path,
a pinned repository reference and `ask_first`. Only `sign_commit` with a Git
commit-shaped `commit` string is supported, in SSH's `git` signing namespace.
The owner approves the exact object; the returned SSH signature and public key
can be used to verify it. Neither the private key nor an agent socket is sent to
the task. This is not a generic signing API or automatic git installation hook.

## Supported CLI and MCP adapters

| Adapter | Supported pinned operations | Explicit exclusions |
| --- | --- | --- |
| `gh` | `repo_view`, `issue_list`, `issue_create` on configured repository | token export, auth commands, arbitrary API URLs, Git HTTPS transport |
| `stripe` | `balance_get`, `customers_list`, `customer_create` through host CLI's default test-mode account | live-mode flags, key creation, arbitrary commands/URLs |
| `aws` | `identity`, `head_object` on configured bucket/region, host credential chain and official SigV4 | STS credential return, presign, arbitrary signing, chunked uploads, writes |
| HTTP MCP | host-authenticated Streamable HTTP initialize/initialized/tools-call for explicitly listed tools | credential passthrough, arbitrary tool discovery, OAuth enrollment/PKCE/refresh orchestration, stdio and in-guest server delegation |

Host CLI failures never return stderr. API/MCP results are bounded and fully
buffered before release; known raw, JSON-escaped, URL-encoded, hex and base64
credential reflection is blocked across chunk boundaries. This is defense in
depth, not arbitrary encoding detection; upstream credential recipients remain
trusted. Audit records contain only identifiers, time and outcome, never bodies,
headers, URLs, settings or credentials. Do not enable global HTTP tracing or
broker profiling. Canonical public control API wiring, OAuth lifecycle, exact
metering and the listed unsupported transports remain explicit follow-up work.

## Demonstrations

See [T17 evidence](T17-evidence.md). `tools/broker_demo.py` makes one disposable
host journey. `tools/broker_sandbox_demo.py` adds one real LX sandbox through
an already prepared isolated engine store and checks the owner's capacity rule
immediately before allocation. It uses separate supervisor `--mem-dir`,
`--netns-dir`, `--uid-base` and `--host-id` settings. `lamp-supervisor ctl capture`
observes one real full memory/disk snapshot without allocating a second guest;
an independent host scanner checks its bytes before the source is released.
Neither script is a trial/benchmark loop or an attack-suite certification.
Preserve failed run outputs; never reuse their root.
