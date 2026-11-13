# T43 evidence and remaining scope

The release now supports managed OpenCode and Codex, host-held model credentials,
explicit Codex subscription sign-in import, scoped guest capabilities, settings,
opaque secrets and signed-in CLI connections. The coordinator's follow-up closed
the subscription-task, Linux-capabilities and authenticated-CLI evidence gaps.
Claude subscription enrollment remains unsupported: the Ubuntu host had no
exported Claude sign-in file, Mac keychain access was prohibited, and the release
has no verified subscription import/protocol adapter for Claude. See
`../../agents.md` for the exact limitation and vendor gateway documentation.
OpenRouter's completed browser exchange is still unobserved; the demonstrated
completed sign-in flow is the explicitly authorized Codex host import.

Follow-up observations (2026-11-13):

- `linux-subscription.json`: Lamp imported the Ubuntu owner's existing Codex
  ChatGPT sign-in into its own 0600 custody store. Official Codex 0.168.0 ran in a
  Firecracker guest, wrote `app.py`, and executed it. A separate guest execution
  verified the artifact prints `385`. That run later failed at tea scope
  validation; its task and artifact observations remain valid.
- `linux-capabilities.json`: the final run listed a private repository through
  real tea 0.11.0 against a disposable native Forgejo 13.0.2 account. The guest
  received no service token. It created/executed/deleted a child, observed the
  third-sandbox and parent-deletion refusals, approved settings, exercised opaque
  secret substitution/redaction and refused origin, attached/disconnected,
  restarted lampd, called public Cloudflare MCP afterward, and refused the next
  request after source revocation. Admission observations were 0 VMs/15067 MiB
  before the parent and 1 VM/14121 MiB immediately before the child.
- Custody observation: 20 guest session/home files up to 2 MiB, accessible process
  environment/command lines, and transcript were inspected for candidate strings
  matching known subscription and service credential hashes. None matched; the
  guest transport token differed. This is bounded plaintext observation, not a
  claim about every disk block or arbitrary encoding. The code path retains the
  upstream auth document exclusively on the host and sends only transport
  capabilities into the guest.
- `credential-preservation.json`: the owner's original still exactly matched the
  task-run custody copy after all runs. Lamp neither refreshed nor revoked it.
- `cleanup-followup.json`: all dedicated VMs, worker/store, native Forgejo process,
  disposable account database, imported custody copies and grant were removed;
  there were zero Firecracker processes afterward. Existing services and Docker
  were untouched. No Mac keychain or owner screen was accessed.
- `followup-failures.json` preserves intermediate failures and their fixes.

Earlier observations, retained without relabeling:

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
ticket work left less than 12 GiB available. Those historical attempts remain failed/unavailable. The later follow-up
capability observation above is separate real Linux evidence.

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
