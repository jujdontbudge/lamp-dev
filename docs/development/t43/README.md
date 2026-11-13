# T43 evidence and remaining scope

This change makes release OpenCode sessions, host-held OpenRouter model keys,
OpenRouter PKCE, scoped guest capabilities and the existing broker available
through the daemon. It does **not** complete every T43 finding: Claude
subscription sign-in/custody (F59) is still unsupported by the release path.
Only OpenRouter PKCE initiation (`pending`) was observed; a completed real
sign-in exchange is unproven. No real subscription login or signed-in
third-party CLI account was used.

Actual observations, 2026-11-12/13:

- `linux-agent.jsonl`: a real Firecracker sandbox, official OpenCode 1.19.5,
  the zero-credit OpenRouter account with `cohere/north-mini-code:free`, a
  TypeScript service written and started by the agent, and its host preview
  returning `{"ok":true,"service":"lamp-t43-agent"}`. The sandbox was deleted.
- `mac-observations.json`: the same model/agent journey in a confined VZ Linux
  ARM guest, including the settings signature and public Cloudflare MCP tool
  in the transcript. The agent needed a follow-up to start its compiled server
  in the background. Host preview, nested guest CLI, scope/capacity refusals,
  approved settings return and opaque secret use passed. Its subsequent restart
  observation failed and remains failed in the record.
- `mac-capabilities.json`: after the fixes, a separate real VZ run with no
  additional model request demonstrated child creation/exec/deletion, refusal of
  a third sandbox and parent deletion, settings approval, opaque secret
  substitution/redaction and origin refusal, CLI attach/disconnect with released
  lease, distinct guest transport and host model credentials, daemon restart,
  a successful public Cloudflare MCP call afterward, and immediate refusal after
  credential-source revocation. The sandbox was deleted. The custody observation
  compared hashes of guest environment values and the guest transport credential;
  it is not a claim of exhaustive process or disk inspection.
- `secret-form.json`: the real owner API returned a password entry form; a
  disposable value submitted over its host loopback endpoint was retained with
  mode 0600 without being returned in the API response. No owner browser was used.

`failed-observations.json` preserves earlier failed and unavailable observations.
In particular, a later Linux extended run lost its managed network route during
installation. The final Linux capability attempt did not allocate because other
ticket work left less than 12 GiB available. These are not green Linux parity
results, and this MR must not be presented as completing the entire ticket.

The demos used dedicated supervisor stores, binaries, VMs and Mac worker
identities. They did not change existing installs, Docker objects, owner projects,
vaults or browser profiles. The agent app was disposable; external services were
public no-auth Cloudflare documentation MCP and httpbin with a fake secret.
Only explicit OpenRouter free models were used, with zero maximum provider price.
The real model key stayed out of argv, printed output and these evidence files.

Validation: `make build` passed after integrating origin/main, as did `go vet
./cmd/lamp ./cmd/lampd ./internal/control ./internal/broker`, Python compilation
of the two adapters, and `git diff --check`. Public/SDK generators were run;
SDK changes are generated contract synchronization only. No new tests were
written for implementation. `tools/check_contracts.py` still fails on existing
fleet/spec drift (`unavailable_host_ids`, `stranded_sandbox_ids`). No full smoke,
benchmark or manual kernel/VM test suite was run.

Sol review added one regression for attach draining retained output after a
session ends. It failed before the one-line fix and passed afterward; see
`sol-review.md`. The review build and `go vet ./cmd/lamp` also passed.
