# Release agents

For child agents, change sets, side chats, teleport and bridges, see
[Agent workflow](agent-workflow.md).

The release daemon enrolls OpenCode automatically. On first use in a Linux
sandbox it installs the pinned official `opencode-ai@1.19.5` npm package.
Python 3, Node and npm come from the installed Linux guest. The same path runs
in Firecracker on Linux and Virtualization.framework on macOS.

```sh
lamp harness list --json
lamp session create --sandbox-id "$SB" --harness-id opencode \
  --prompt 'Print the Node version using your bash tool.' --wait-ms 60000 --json
lamp session attach --session-id "$SESSION"
```

Attach shows retained output and sends each entered line as a prompt. Ctrl-C
releases its input lease and disconnects. Another writer must release its lease
before attach can acquire one. `--json` retains the lower-level attach API.
The default model is OpenCode's free `opencode/deepseek-v4.1-flash-free` route.
An upstream failure remains a blocked turn; Lamp does not silently replace it
with a new conversation.

## Host-held model keys and sign-in

Enter an OpenRouter key through the hidden terminal prompt, or a private stdin
pipe with `--stdin`. Values never belong in argv or JSON evidence.

```sh
lamp secret set openrouter
lamp credential-source create --provider openrouter --account-ref openrouter \
  --wait-ms 60000 --json
lamp harness enroll --input - --wait-ms 60000 <<'JSON'
{"harness_id":"opencode","credential_source_id":"SOURCE_ID","model":"cohere/north-mini-code:free"}
JSON
```

Enrollment accepts only explicit OpenRouter `:free` models. The host broker also
sets provider maximum prices to zero. A sandbox receives a transport capability
for its own live session and generation. It receives neither the OpenRouter key
nor access to host credential files. Revoking the credential source refuses the
next gateway request, including requests on an existing HTTP connection.

`lamp auth-flow create --identity-provider-id openrouter --purpose provider_login`
starts OpenRouter PKCE. Get the flow resource and open its `authorization_url`
on the daemon's host. Its one-use loopback callback exchanges the code on the
host and creates a credential source; the completed flow's labels identify it.
For a remote daemon, forward the callback port privately or use the owner-only
`auth-flow complete` endpoint with private JSON stdin. Codes, verifiers and keys
bypass operation journaling. The callback expires after ten minutes. Daemon
restart requires starting a new pending sign-in flow.

For an existing Codex ChatGPT subscription sign-in on the daemon host:

```sh
lamp auth-flow create --identity-provider-id codex --purpose provider_login \
  --wait-ms 60000 --json
lamp auth-flow get --auth-flow-id FLOW_ID --json
# Use labels.lamp.credential_source_id from the completed flow:
lamp harness enroll --input - --wait-ms 60000 <<'JSON'
{"harness_id":"codex","credential_source_id":"SOURCE_ID","model":"gpt-6.1-sol","nested_create":true}
JSON
lamp session create --sandbox-id "$SB" --harness-id codex \
  --prompt 'Write and run a Python program in this workspace.' --wait-ms 60000 --json
```

This explicit flow imports the host user's existing `~/.codex/auth.json` into
Lamp's private custody store (0600), then completes with a source ID. It does
not launch a browser, use a keychain, modify the original, or refresh/revoke its
tokens. Sign in with the official Codex CLI on the host first. If the copied
access token expires, sign in/refresh through the owner's CLI and start a new
Lamp import; Lamp does not rotate the shared refresh token. The managed guest
installs official `@openai/codex@0.168.0`; its custom Responses provider gets
only a scoped Lamp transport token. Lamp attaches the access token and account
header on the host to the pinned ChatGPT Codex endpoint. Subscription limits
apply; an API billing credential is not substituted. Choose a model available
to that subscription. The session reports `auth_mode:subscription`.

Claude subscription enrollment remains unsupported. Lamp's existing Claude
native adapter expects a setup token in the native custody process, and its
Messages broker integration supports API authentication. This release has no
Claude subscription import or subscription protocol adapter that keeps that
token outside an in-guest Claude process. Reusing the native adapter in a guest
would expose the subscription token there. Owner authorization resolves account
permission; it does not supply the missing integration. The Ubuntu host had no `~/.claude/.credentials.json` sign-in file; accessing the
Mac keychain was explicitly prohibited and was not attempted. We therefore had
no authorized exported Claude credential with which to implement and verify the
missing subscription adapter. [Anthropic's gateway documentation](https://code.claude.com/docs/en/llm-gateway)
allows forwarding subscription OAuth with the appropriate `anthropic-beta`
capability; this is not a claim that gateways are categorically forbidden. This is an implementation limitation, not a claim that the owner's
subscription may not be used.

## Scoped guest CLI and MCP

Re-enroll OpenCode with `nested_create:true` to give its sessions a guest `lamp`
CLI and endpoint-scoped capability. Enrollment also accepts
`settings_source_ids`, `connection_ids` and `secret_names`.

Inside an enrolled session:

```sh
lamp sandbox create --wait-ms 60000 --json
lamp sandbox exec CHILD_ID -- /bin/echo hello
lamp sandbox delete CHILD_ID
```

A child inherits its parent's template, resources and task. The capability can
get, execute in and delete only its own recorded children. It permits one child
within two live sandboxes visible to that daemon. Host admission and the owner's
machine-wide safety limits still apply. Parent deletion, stopped sessions,
generation changes and source revocation refuse further gateway use. Child
sandboxes remain owner-visible and need deletion if the parent exits early.

`lamp connection create` accepts `type:mcp` with an HTTPS endpoint,
`allowed_operations`, and exact `resource_policy.operations` field schemas.
A public server can omit credentials; authenticated servers reference a
host-entered `resource_policy.secret_name`. Enroll the connection ID into the
harness. Its tools appear through the session's stdio MCP gateway. Guest
`lamp connection invoke CONNECTION OPERATION --input -` reaches the same broker.
Host CLI connections use the broker's existing pinned AWS, Stripe and GitHub
adapters, plus `tea` for Forgejo/Gitea; they do not admit arbitrary guest-supplied
host commands. The tea adapter offers only `repo_list` (no arguments), pins the
service origin, and creates an isolated private host login config for each call.
It never opens the owner's tea config. HTTPS is required except for an explicit
host-loopback HTTP service. A CLI connection can use this owner-enrolled policy:

```json
{"type":"cli","adapter_id":"tea","endpoint":"http://127.0.0.1:31443","access":"read_only","allowed_operations":["repo_list"],"resource_policy":{"binary":"/absolute/path/to/tea","secret_name":"forgejo-demo","operations":{"repo_list":{"fields":{}}}}}
```

Enter the service token with `lamp secret set forgejo-demo`, create the connection,
and enroll its ID. Guest `lamp connection invoke CONNECTION repo_list --input -`
with `{}` invokes the actual host tea CLI. The service token stays in the host
custody store and tea's temporary 0600 config, which is removed after the call.

## Settings and opaque secrets

Register a settings source with an absolute host `source_ref` directory and a
relative `path_allowlist`. Enrollment copies only those files into the session
workspace. A guest uses `lamp settings-source copy SOURCE` and
`lamp settings-source propose SOURCE` (private JSON stdin containing
`base_revision` and `files`). This creates a proposal, not a host write. The
owner explicitly calls `lamp settings-source apply` with the session, source,
change ID, original base revision and selected paths. Existing broker conflict,
path and symlink protections still apply. Unapproved proposals expire and are
not retained across daemon restart.

`lamp secret set DEMO_KEY --host api.example.com` stores a value on the host.
Enrolling `DEMO_KEY` creates an opaque `lamp_secret_…` environment value in the
session. Names must be uppercase application names; host configuration variables
such as PATH, HOME and LAMP_* are reserved. Guest `lamp secret request` accepts
JSON with a name and exact hosts and returns a host-local password entry form.
The owner can also request a form through `lamp secret request --input -`.

The guest sends explicit HTTPS requests with `lamp secret use` and JSON stdin:
`url`, optional `method`, `headers` and `body`. Only enrolled markers for an
approved exact host are substituted. Redirects and private destinations are
refused, and echoed credentials are redacted in responses. Arbitrary curl
traffic is not transparently rewritten. The host listener remains private;
forward its port privately when the owner uses a remote machine.

Agent gateway bindings survive a daemon restart on the same running VM
generation. The network attachment has a 24-hour lifetime; use a new session
after it expires. Runtime capability receipts stay in the private daemon state,
outside guest captures. A restored or forked generation needs a new session.
