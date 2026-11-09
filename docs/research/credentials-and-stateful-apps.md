# Credentials and stateful apps for Lamp

Research date: **2026-11-08**. Scope: options for a self-hosted service on Linux, macOS, and Windows. This is evidence and trade-offs, not an architecture recommendation. Sources are vendor documentation, source code, terms, and original security research. Product claims are attributed; no integration was tested.

“Secret” below means a real upstream password, API key, OAuth token, cookie, or vault unlock key. A broker handle can keep that secret outside the agent VM while still granting powerful account access. “Headless” means no interactive sign-in during the job. It does not mean offline. Setup costs and mitigation proposals are engineering judgments, not vendor guarantees.

## 1. Harness authentication

### Methods, storage, and headless reuse

Paths are defaults. Config directories, release channels, OS credential stores, and provider integrations can change them. A copied credential file is a real secret. A supported noninteractive mode does not itself authorize account sharing or unlimited parallel use.

| Harness | Supported methods and environment | Credential storage | Mint once, reuse headlessly? |
| --- | --- | --- | --- |
| Claude Code | Claude subscription OAuth; Console/API key; supported cloud providers. `ANTHROPIC_API_KEY`, gateway bearer `ANTHROPIC_AUTH_TOKEN`, `apiKeyHelper`, `CLAUDE_CODE_OAUTH_TOKEN`; cloud selection via `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX`, or `CLAUDE_CODE_USE_FOUNDRY`. [C1] | macOS Keychain; fallback `~/.claude/.credentials.json` if Keychain write is rejected. Linux uses that JSON file, mode 0600; Windows `%USERPROFILE%\.claude\.credentials.json` with user ACLs. `CLAUDE_CONFIG_DIR` changes the location and Keychain scope. [C1] | `claude setup-token` prints a one-year subscription token for CI; it does not save it. It authorizes model requests, not Remote Control or claude.ai connectors; local MCP still works. `--bare` ignores OAuth environment credentials. Technically reusable; policy qualification below. [C1] |
| Codex | ChatGPT sign-in, browser or `codex login --device-auth`; API key through `codex login --with-api-key` reading stdin, often from `OPENAI_API_KEY`; custom provider keys/helpers. Workspace automation also has `CODEX_ACCESS_TOKEN`. [O1][O2] | `$CODEX_HOME/auth.json`, normally `~/.codex/auth.json`, or OS keyring. `cli_auth_credentials_store` supports `file`, `keyring`, `auto`, `ephemeral`. [O1] | Docs explicitly support copying `auth.json` to a headless machine. OAuth refreshes; safe simultaneous reuse of one refresh-token chain across many clones is **unconfirmed**. Business/Enterprise workspace access tokens support trusted noninteractive runners, expiry, revocation, and service identities. [O1][O2] |
| OpenCode | Provider-specific API keys and OAuth, `opencode auth login`; examples include ChatGPT Plus/Pro and Copilot sign-in. Provider environment keys include `ANTHROPIC_API_KEY`; configured provider env/commands also work. [P1][P2] | Current V2 stores provider credentials in SQLite, usually `~/.local/share/opencode/opencode.db`; inspect `opencode debug paths db`. XDG, release channel, and `OPENCODE_DB` affect it. V1 used `~/.local/share/opencode/auth.json`, imported during migration. Saved V2 account can take precedence over env. [P1] | API keys are reusable. OAuth refresh and reuse depend on the provider. There is no universal OpenCode setup-token. Integration support is not permission to reuse another vendor's subscription OAuth outside its allowed clients. [P1][P2][C2][G3] |
| Gemini CLI | Gemini API `GEMINI_API_KEY`; Vertex API key `GOOGLE_API_KEY`, or ADC/service identity with `GOOGLE_APPLICATION_CREDENTIALS`, `GOOGLE_CLOUD_PROJECT`, `GOOGLE_CLOUD_LOCATION`. Legacy/eligible organizational Google OAuth remains in the code/docs. [G1] | Version/settings dependent: legacy `~/.gemini/oauth_creds.json`; current encrypted storage uses service `gemini-cli-oauth` and hybrid OS keychain/encrypted-file fallback, with migration code. Do not assume the legacy file is always authoritative. [G4] | API keys and service credentials work noninteractively. Cached eligible organizational OAuth may work. Consumer Google OAuth stopped June 18, 2026; Google AI Pro/Ultra no longer entitle Gemini CLI. No currently supported universal consumer setup-token was found. [G1][G2] |
| Cursor CLI | Browser `agent login`; `NO_OPEN_BROWSER=1` prints a login URL. User or enterprise service API key through `CURSOR_API_KEY` or `--api-key`. [U1][U2] | Docs say securely stored locally. The exact CLI credential filename/store is **unconfirmed**; editor database paths are not evidence for CLI storage. [U1] | API keys are documented for automation and CI; service-account keys are an organizational option. No documented Claude-style long-lived subscription setup-token was found. Browser-token cloning and its refresh semantics are **unconfirmed**. [U1][U2] |
| Copilot CLI | OAuth browser/device login; fine-grained PAT with `Copilot Requests`, OAuth `gho_`, or GitHub App user-to-server `ghu_`. Classic `ghp_` PATs are unsupported. Precedence: `COPILOT_GITHUB_TOKEN`, `GH_TOKEN`, `GITHUB_TOKEN`, keychain, then supported `gh` fallback. BYOK uses `COPILOT_PROVIDER_API_KEY`/`COPILOT_PROVIDER_BASE_URL`. [H1] | OS credential store, service `copilot-cli`; without it, plaintext `~/.copilot/config.json` after prompting. [H1] | Explicit CI/container support. PAT expiry and account entitlement still apply; an Actions job's ordinary `GITHUB_TOKEN` is not automatically an eligible Copilot credential. `COPILOT_OFFLINE=true` suppresses GitHub traffic; inference is truly offline only with an available local provider. [H1] |
| Amp | Browser login or `AMP_API_KEY`; environment key takes precedence. Execute mode `amp -x` supports pipelines. Workspace service accounts can generate API keys for CI. [A1][A3][A5] | macOS/Linux `~/.local/share/amp/secrets.json`; Windows `%USERPROFILE%\.local\share\amp\secrets.json`. Revoke through the service/key controls; the security docs also describe `/api/revoke`. [A2] | Keys are reusable until revoked. Workspace runners and service identities are documented. Unlimited reuse of a personal bearer credential across shared jobs is **unconfirmed**; Amp's credit/workspace model is not a blanket flat-rate subscription entitlement. [A1][A3][A4] |
| Goose | Provider-specific API keys, cloud identities, local models, and ACP adapters delegating authentication to another harness. `OPENAI_API_KEY` is one example; provider env overrides stored values. [B1][B2] | OS keyring by default; `GOOSE_DISABLE_KEYRING` permits plaintext `secrets.yaml`. Docs list `~/.config/goose` on macOS/Linux and `%APPDATA%\Block\goose\config` on Windows. A macOS issue reports `~/Library/Application Support/Block/goose`; packaged-version path needs validation. [B1][B3] | Provider keys can run headlessly. ACP inherits the delegated harness's login and terms. Old `claude-code`, `codex`, `gemini-cli`, and `gemini_oauth` CLI providers are marked deprecated in favor of ACP; there is no universal Goose subscription token. [B2] |

Google's dated deprecation notice overrides the still-accessible generic Gemini authentication instructions. Existing Code Assist Standard/Enterprise contracts have a separate sunset: new sales stopped October 9, 2026; annual renewals end January 31, 2027; monthly renewals end December 31, 2027. Bundled Gemini Enterprise entitlement ends March 31, 2027. These are migration constraints, not new Gemini CLI subscription options. [G2][G5]

Codex's dedicated access-token page says Business and Enterprise; its general authentication page describes Enterprise. The dedicated page is the more specific source, but exact workspace availability should be checked. It warns: “Use access tokens only on trusted runners.” These are workspace automation tokens, not a claim that consumer OAuth can be redistributed. [O1][O2]

### What the vendors actually authorize

Quotes below are deliberately short. “Not found” means no explicit authorization was found in the cited material; it is not a claim that an activity is prohibited.

| Vendor/harness | Relevant words | Implication and unresolved boundary |
| --- | --- | --- |
| Anthropic | Subscription limits assume “ordinary, individual usage”. Also: “developers may not collect, store, or intermediate Claude.ai credentials or session tokens”. [C2] | Hosted, unmodified Claude Code is expressly allowed under Commercial Terms, with built-in sign-in methods preserved and each end user authenticating and paying directly. Native user sign-in is distinguished from a third-party product routing subscription requests. A Lamp-managed subscription-token proxy therefore has a material policy question even when technically possible. Unlimited parallel personal usage is not promised. [C2] |
| OpenAI | “You may not share your account credentials or make your account available to anyone else”. [O3] | Official headless login and workspace automation exist. They do not authorize multiple people sharing one personal account. Unlimited parallel consumer jobs are **unconfirmed**; workspace token features have explicit administrative controls. [O1][O2][O3] |
| OpenCode | ChatGPT integration: “Usage counts against your plan”. [P2] | OpenCode documents client integration, not a new entitlement. Each upstream provider controls subscription eligibility, limits, and permitted clients. Anthropic and Google restrictions remain relevant. [P2][C2][G3] |
| Google | Consumer notice: “you can no longer use the Login with Google option”. The CLI FAQ calls OAuth piggyback through other tools “a direct violation of our applicable terms and policies”. [G2][G3] | Do not infer that exported Google OAuth enables a supported Gemini backend for every harness. Current API/Vertex access is distinct from retired consumer subscription access. Organizational contracts have their own sunset. [G1][G2][G5] |
| Cursor | “For automation, scripts, or CI environments, use API key authentication”. Terms prohibit “rent, lease, lend, or sell the Service”. [U1][U3] | Automation is documented, and enterprise service accounts exist. Personal multiuser redistribution and an unlimited parallel subscription fleet are **unconfirmed**. [U1][U2][U3] |
| GitHub | CLI token auth is “Recommended for CI/CD pipelines, containers, and non-interactive environments”. Terms: “Your login may only be used by one person”. [H1][H2] | CI support is explicit. GitHub separately permits human-created machine accounts, but that does not itself grant a Copilot seat or license shared personal subscription tokens. [H1][H2] |
| Amp | Execute mode: “Execute mode is automatically turned on when you redirect stdout”. License: “non-transferable, non-sublicensable”. [A1][A4] | Automation and workspace service accounts are documented. No explicit personal-key sharing or unlimited parallel entitlement was found. [A3][A4] |
| Goose | Legacy CLI provider heading: “Deprecated — Use ACP Providers”. [B2] | Goose's open-source client does not confer rights to any upstream subscription. Reuse through ACP depends on the selected harness and provider; no separate universal Goose subscription license applies to inference. [B1][B2] |

## 2. MCP authentication

The current published authorization specification is **2026-07-28**. For HTTP transports, authorization is optional at the protocol level. Protected servers use OAuth 2.1: discover protected-resource metadata, discover the authorization server, obtain scoped consent with authorization code and PKCE, then send an access token for the intended resource. Client preregistration and client-ID metadata documents are supported; dynamic client registration is a deprecated compatibility option. [M1]

The security text requires audience validation and S256 PKCE, secure endpoints/redirects, short access-token lifetimes, and refresh-token rotation for public clients. It forbids token passthrough: a server must not accept an arbitrary inbound token and forward it to another API. An outbound broker obtaining a distinct token for the correct upstream audience is a different pattern. [M2]

Stdio implementations should not apply the HTTP transport authorization flow. They typically receive credentials from their parent environment or implement service-specific login. Running a stdio server inside the VM with a real API key gives that process access to the key, even if the language model never sees it. The spec does not standardize a disk token filename. [M1]

Client persistence is therefore implementation-specific:

| Client | Evidence and caveat |
| --- | --- |
| Codex | `mcp_oauth_credentials_store` selects `auto`, `file`, or `keyring`. This is separate from model-provider credential storage. [O4] |
| Gemini CLI | Hybrid token-storage code supports OS keychain and encrypted fallback; storage code names `mcp-oauth-tokens.json`. Runtime directory and version matter. [G6][G7] |
| OpenCode | Earlier docs describe `mcp-auth.json`; V2 migration issues report missing V1 OAuth state. The exact current database/file behavior needs a pinned-version test. **Unconfirmed across versions.** [P3] |

### Gateways that retain upstream credentials

| Approach | How it works | Trust and deployment trade-off |
| --- | --- | --- |
| Docker MCP Gateway/Toolkit | Gateway coordinates containerized MCP servers, secret delivery, and OAuth. The agent sees tools through a gateway rather than receiving every service credential. [M3] | The MCP server container still gets the credentials it needs. Gateway and server code remain trusted. This is not the same configuration as Docker Sandboxes' MCP service. [M3][M4] |
| Docker Sandboxes MCP gateway | Host manages registrations, OAuth, and local stdio processes; sandbox receives one endpoint. Static selection restricts the exposed servers. Dynamic mode permits discovery/loading. Header `${api-key}` references a host-managed `mcp:server:api-key` secret. [M4] | Running stdio on the host moves tool execution outside the microVM boundary. It keeps upstream tokens outside the agent VM, but broad tool capabilities remain dangerous. [M4] |
| AWS AgentCore Gateway | Managed outbound auth supports API keys, OAuth client credentials, user authorization code, token exchange, and SigV4. MCP targets do not support raw inbound-token passthrough. [M5] | Service holds credentials and signs/calls upstream; managed AWS dependency rather than a self-hosted universal replacement. [M5] |
| Composio | Connected accounts hold per-user OAuth/API credentials server-side; tools use account identifiers, with refresh managed by the service. Credential responses are redacted; Proxy Execute injects credentials server-side. [M6][M7] | Sandbox can use a restricted broker interface instead of upstream tokens. Its Composio/project credential is itself a sensitive capability. Disabling stops tool access; upstream revocation is a separate, best-effort operation. Current docs favor sessions over legacy direct account management. [M7] |

Analysis: a single host-side refresh owner avoids many cloned VMs independently updating one rotated refresh-token chain. It adds a broker availability dependency and requires tenant isolation. This follows from rotation semantics; it is not a tested guarantee for every vendor. [M2]

## 3. Placeholder credentials and host proxies

| Implementation | Mechanism | Limits and portability |
| --- | --- | --- |
| Docker Sandboxes | Guest has a sentinel such as `proxy-managed`. Host HTTP/HTTPS proxy supplies actual authorization for matching services/domains. Service kits can handle OAuth for Claude, Codex, Cursor, and Droid. Host commands can resolve secrets from external stores. [X1] | TLS termination requires the guest to trust the proxy CA. OAuth `passthrough: true` returns actual credentials, giving up isolation. Global credentials may be available to multiple sandboxes unless bindings restrict them. Support in a kit is not vendor subscription authorization. [X1][C2] |
| Matchlock | Experimental Linux/macOS microVM project. `--secret NAME@host` supplies a placeholder `SANDBOX_SECRET_…` inside; transparent HTTP/TLS interception substitutes the secret for the scoped host. `--secret`/`--allow-host` seal network access to allowed hosts. [X3] | Source generates a CA and per-host certificates. Custom placeholders handle client token-format checks. Official examples include Anthropic SDK use. Native Windows host support was not documented in the reviewed material. [X3][X4] |
| MCP/action broker | Instead of imitating an API token, expose a typed tool that performs an authenticated request outside the VM. Docker MCP, AgentCore, and Composio illustrate this. [M4][M5][M6] | Avoids guest TLS interception for the upstream API, but needs compatible tools/adapters. The broker credential and tool authority still need confinement. |

Docker stores host credentials using macOS Keychain, Windows Credential Manager, or Linux Secret Service. Its headless Linux fallback under `$XDG_CONFIG_HOME/com.docker.sandboxes` relies on file permissions rather than password encryption. A host compromise is outside the protection provided by guest placeholders. [X1]

### Compatibility and residual exposure

| Case | What the evidence supports |
| --- | --- |
| TLS trust | Forward proxy mode terminates TLS; bypass/transparent forwarding cannot rewrite encrypted authorization. Nested Docker builds have their own trust store; Docker documents `PROXY_CA_CERT_B64` for CA installation. A trusted CA on the VM OS is not automatically trusted by every nested image/runtime. [X2] |
| Harness trust configuration | Claude documents `NODE_EXTRA_CA_CERTS`; Codex documents `CODEX_CA_CERTIFICATE`, falling back to `SSL_CERT_FILE`. Amp requires WebSocket upgrades through proxies. These are configuration surfaces, not proof every feature works under every interceptor. [C3][O1][A2] |
| Pinning and non-HTTP auth | Analysis: a client that pins the vendor certificate/key rejects a replacement certificate even with a trusted local CA. Plain bearer substitution cannot implement SSH signing, client-held mTLS keys, SigV4 signatures, or device-bound proof. SSH agents and signing gateways are separate mechanisms; AgentCore documents delegated SigV4. [M5][X2][D3] |
| Which clients break? | **Unconfirmed:** no complete tested harness/SDK compatibility matrix was found. Do not label all Claude/Codex/Goose traffic incompatible. Token syntax checks, separate CA bundles, WebSocket paths, and refresh endpoints each require testing. A reported Claude voice/custom-CA issue is feature-specific, not a general pinning finding. [X3][C4] |
| What remains stealable | Analysis: files, cookies, real tokens already injected into the VM, returned account data, and broker handles remain accessible to compromised code. A proxy cannot undo an authorized destructive request. A malicious allowed endpoint that reflects an injected header can expose it; host/path/method constraints and response handling are separate controls. [M2][T2] |

TLS interception also exposes request/response plaintext to the host proxy. Protect its CA private key and logs. Domain allowlisting alone does not distinguish legitimate API calls from abuse of the same allowed API. These are trust-boundary implications, not evidence of a particular product flaw. [X1][X4][T2]

## 4. Logged-in browsers

### Moving state versus moving browser control

| Technique | What moves | Trade-off and failure modes |
| --- | --- | --- |
| Copy a Chromium user-data directory | A profile includes valuable session state, not merely browser preferences. Persistent contexts use a user-data directory. [D1][D21] | Real session secrets enter the VM. Chrome's platform protections can prevent decryption; browser/profile compatibility and concurrent profile use also matter. Playwright warns against launching multiple instances with the same user-data directory. [D21][D4] |
| Chrome Sync or managed cookie sync | Ordinary Chrome account docs list bookmarks, passwords, preferences, tabs, and history. Managed ChromeOS Desk sync separately supports cookies on enrolled ChromeOS 139+ devices. [D14][D15] | Neither is a documented general-purpose Linux/macOS/Windows microVM session-import API. Universal live-session synchronization into Lamp is **unconfirmed**. Do not equate synced passwords with a copied authenticated session. [D14][D15] |
| Explicit cookie/storage import | Playwright `storageState` captures cookies/localStorage and optionally IndexedDB. Session storage requires separate handling. [D1] | Reproducible and selective, but the exported state can impersonate the user. It is a secret file and may omit site/device state. No guarantee that reauthentication or server risk checks are avoided. [D1][D3] |
| Host or separate-browser CDP | Browser keeps its disk profile outside the agent VM; agent connects to its browser-control endpoint. [D2][D5] | Raw control can read cookies, execute JavaScript, and inspect network data. “Secret outside VM” is not “agent cannot retrieve it”. A narrower action service can withhold those methods, at the cost of compatibility; that is an analysis option. [D2][D10] |
| Browser service with persistent contexts | Service owns profile storage and exposes a session/control handle. Browserbase, Steel, Kernel, and Browser Use document variants. [D6][D7][D8][D9] | Secrets move to that service/browser boundary. Persistence, tenant isolation, export methods, and cloud versus self-hosted feature parity must be checked per product. [D6][D7][D8][D9] |

Chrome 127 introduced Windows app-bound cookie encryption tied to application identity and machine. Copying encrypted cookie files to another host is therefore not a general migration method. macOS Keychain and Linux wallet integration also make filesystem copying different from authenticated export. [D4]

Device Bound Session Credentials add device-held signing keys and short-lived cookies refreshed with proof of possession. A copied cookie can sometimes remain useful briefly, but cannot generally renew without the original key. This is site/platform dependent, not universal deployment. Profile copying is not a universal passkey migration method either. [D3]

Since Chrome 136, remote-debugging switches are ignored for the default Chrome data directory; use a nondefault data directory or Chrome for Testing as documented. This limits simply attaching CDP to the user's everyday default profile. [D5]

### Products and agent browser offerings

| Product | Documented behavior | Relevant uncertainty or boundary |
| --- | --- | --- |
| Browserbase | Contexts persist encrypted profile state across sessions; `persist: true` saves updates. Docs cover cookies and web storage; Live View supports manual MFA before reuse. [D6][D16] | Hosted service has the browser/session boundary. Site expiry still applies; persistence is not permanent authentication. [D6] |
| Steel | Profiles API uses `profileId`/`persistProfile`. Credentials API supports origin-scoped username/password/TOTP injection and obscures credential fields. Open-source browser server is available. [D7][D17][D18] | Vendor claims credential hiding; whether arbitrary raw CDP can bypass that hiding needs validation. A Steel API key can grant organization-wide access. Cloud credentials/profile features must not be assumed identical in the self-hosted distribution. [D7][D19] |
| Kernel | Managed Auth handles login workflows, MFA/SSO, profile health and relogin; named profiles persist sessions. [D8] | Shared profile is a shared cookie/session boundary. Managed authentication still requires user consent and may require human intervention. [D8] |
| Browser Use | Cloud session docs describe persistent profiles, local browser login/cookie upload, secret references, and `liveUrl` takeover. Repository also contains the open-source browser-agent framework. [D9] | Cloud vault/profile integrations are not automatically features of every self-hosted core deployment. Uploaded cookies are actual session credentials at the service. [D9] |
| Navvi | Current repository describes reusable browser scraping: local persistent `--profile`, headed human CAPTCHA handling, `NAVVI_SECRET_<NAME>`, secret files, and encrypted secret bundles. It explicitly has no CAPTCHA solver. [D11] | Older indexed descriptions of persona-vault/MCP features are not proof of current compatibility. A general authenticated-session broker is **unconfirmed** for the current release. [D11] |
| MultiZen | Open repository describes isolated persistent browser profiles and local MCP at `127.0.0.1:7777`, with cookie, JavaScript, and CDP capabilities. [D10] | Local session control is broad authority. Repository features are not an independent security assessment or a guarantee of secret isolation. [D10] |
| ChatGPT agent | User takes over the browser to enter sensitive login information; session cookies can persist. [D12] | Product-managed login experience, not documentation for exporting a ChatGPT browser session into Lamp. A general self-hosted reusable authentication API is **unconfirmed**. [D12] |
| Anthropic computer/browser use | Computer-use tools operate an environment supplied by the developer. Browser-use guidance discusses low-privilege signed-in accounts and sensitive network logs. [D13][D20] | The tool protocol does not itself supply the user's authenticated browser. Environment ownership and session isolation remain implementation work. [D13][D20] |

MFA can be completed once by a human and the resulting session retained until the site expires or challenges it. Browserbase's example explicitly follows that pattern. TOTP can be generated by a credential service such as Steel, but the seed remains a real secret at that service. Push approvals, security keys, and hardware passkeys may require the owner/device again. CAPTCHA may require human takeover; no universal bypass is documented. [D16][D7][D11]

Playwright now exposes virtual WebAuthn credential support. That supports controlled/test authenticators; it is not evidence that an existing hardware-bound passkey can be cloned. Human takeover needs a separate trusted channel so passwords and codes are not captured in the model transcript. [D2][D3][D12]

## 5. Password managers

| Product/mode | Authentication and scope | Does the agent receive the secret? |
| --- | --- | --- |
| 1Password service accounts | `OP_SERVICE_ACCOUNT_TOKEN` enables unattended access with selected vault permissions and usage visibility. [V1] | Retrieval APIs/CLI return real secrets. Keeping this token only in a host broker avoids placing it in the VM; forwarding retrieved values into the VM reverses that protection. [V1] |
| 1Password Connect | Private REST service caches vault data in the operator's infrastructure. CLI integration uses `OP_CONNECT_HOST`/`OP_CONNECT_TOKEN`. [V2] | Connect host and authorized callers can obtain secrets. An agent VM calling it directly holds a valuable access capability. [V2] |
| 1Password CLI `op run` | Resolves secret references into a subprocess environment; output concealment is supported. [V3] | **Yes.** The subprocess can read its environment. Output masking does not prevent encoding, file writes, or network exfiltration; this is an analysis consequence of environment delivery. [V3] |
| 1Password for Agents | Announced October 22, 2026. User approves a login; integration fills password/one-time code into the destination without exposing it to the agent, per vendor. [V4] | Fill-on-behalf is a materially different interface. Muse, Grok Bot, and Townie are listed available; Claude Duet, ChatGPT Work/dots, Browserbase, Kernel, and Browser Use are listed coming soon. Generic Lamp integration/SDK availability is **unconfirmed**; announcement points builders to partnerships. [V4] |
| Bitwarden Secrets Manager | `bws` with `BWS_ACCESS_TOKEN`; machine accounts receive project/secret permissions. [V10][V11] | CLI retrieval delivers secret values. Host broker can perform the use instead. Revoking an access token can leave an existing machine session valid for up to one hour. [V5] |
| Bitwarden Password Manager CLI | `bw login --apikey` authenticates; vault unlock is separate. `BW_SESSION` is a decryption-session capability; `bw get password` emits the value. Local `bw serve` needs its own access boundary. [V6] | **Yes** for direct CLI retrieval/unlocked vault access. A host browser autofill adapter is a different integration, not a property of `bw get`. [V6] |
| KeePassXC | Local encrypted `.kdbx`, CLI, auto-type, and browser integration through native messaging. Browser access can prompt for entry association and be remembered. [V7] | Offline vault is possible. Reading CLI output exposes secrets; host-side fill can avoid a secret-returning tool, but a browser with DOM/CDP access may still read filled values. A dedicated database/entry scope limits exposure; approval is not necessarily per use. [V7][D2] |
| Proton Pass | Vault-scoped access tokens last one hour to one year; CLI supports `PROTON_PASS_PERSONAL_ACCESS_TOKEN`. Agent access logs can include a reason, using `PROTON_PASS_AGENT_REASON`. [V8][V12][V13] | Item retrieval and `pass-cli run` expose actual values to the caller/subprocess despite output concealment. Self-reported reasons are audit context, not an approval barrier. Vendor says deleting a token stops access; previously copied secrets still require upstream rotation. [V8][V14] |

Scoped vaults limit which credentials a compromised agent can reach. They do not limit what the corresponding account can do after login. Per-use approval can apply to a credential request, a sign-in, or an action; those are different controls. A generic fill extension plus unrestricted JavaScript is weaker than a broker that never exposes the filled field or cookie APIs. These are analysis distinctions grounded in the interfaces above. [V4][V7][D2]

Signing agents offer another non-export pattern: Proton's SSH agent can sign without handing out the private key. The caller still has signing authority while access is granted. This helps SSH, not arbitrary website password login. [V9]

## 6. Other stateful apps

| Pattern/example | Where the session lives | Trade-offs |
| --- | --- | --- |
| App-specific SaaS API | Provider plus host-held OAuth integration. Gmail distinguishes `gmail.readonly`, `gmail.send`, `gmail.modify`, and broader mailbox access. [S1] | No browser session needs copying. API coverage differs from UI features. Restricted Gmail scopes can require verification and a security assessment when data is stored or transmitted by servers. [S1] |
| Remote SaaS MCP | Slack's MCP server exposes approved Slack actions using OAuth and scopes. A client/broker can retain the credential outside the VM. [S2][M1] | Tool schemas offer narrower handles than a whole desktop, but reads and writes remain real account authority. OAuth consent and revocation are still needed. [S2] |
| Per-user connection broker | Composio connected-account identifiers select server-held authenticated connections for tools. [M6] | Broad app coverage without session migration; introduces broker trust, account-selection correctness, cost, and hosted-service dependency. [M6] |
| Host desktop/computer use | Agent sends actions/screenshots to a computer-use controller; native apps remain signed in on that machine. Anthropic documents developer-provided computer environments. [D20] | Analysis: the controller crosses the VM boundary and may act on the owner's real desktop. Screen contents, clipboard, file dialogs, and other apps can expose data. A dedicated desktop/session narrows collateral access but adds operations work. |
| Host stdio/MCP adapter | Docker Sandboxes can run local tools on the host and expose a gateway endpoint. [M4] | Useful for existing local state. An arbitrary shell-like tool grants much more authority than typed app operations; host execution is not protected by the agent VM's filesystem boundary. [M4] |

These interfaces avoid moving a login into the VM. They do not make agent-visible email, Slack messages, screenshots, or tool results trusted instructions. Sensitive actions can still be induced through those inputs. [T2][T3]

## 7. Threats, mitigations, and evidence

Mitigations below are options inferred from the cited failure modes and security guidance. They are not claims that Lamp or every named product already implements them.

| Threat | Mitigation options and remaining limit |
| --- | --- |
| Prompt injection asks for credentials or unauthorized actions | Keep upstream tokens outside agent code; narrow tools, accounts, and request destinations; approve sensitive writes. Hiding a token does not stop exfiltration of readable data through an authorized tool. MCP security guidance and the GitHub MCP demonstration cover this boundary. [M2][T2] |
| Exfiltration from compromised VM | Enforce egress policy outside the VM, including alternate interfaces, DNS, redirects, IPv6, UDP, and nested containers. Matchlock and Docker provide host-side networking/proxy control examples. An allowed GitHub/Slack/model endpoint can still carry stolen data. [X2][X3][T2] |
| Logs, artifacts, snapshots, shared mounts | Avoid real secrets in env/files when possible; redact host proxy headers and traces; separate tenant snapshots and artifacts; restrict retention. Playwright explicitly treats auth state as impersonation material. Amp describes redaction as best effort; masking is not a containment boundary. [D1][A2] |
| Excessive lifetime or scope | Use distinct owner/service identities, least privilege, short access tokens, rotated refresh tokens, and independent revocation of broker handles and upstream grants. Revocation may have lag; Bitwarden documents up to one hour for an existing machine session. [M2][O2][V5] |
| Cross-sandbox confusion | Bind broker handles to owner, sandbox, permitted server/account, and expiry. Audit account selection and prevent one VM from selecting another user's connection. Audience checking and anti-confused-deputy guidance are relevant. [M2][M6] |
| Missing attribution | Host-side per-sandbox audit records can include owner, job, tool, destination, method, scope, approval, status, and upstream request ID, without authorization headers or secret-bearing bodies. Store outside guest control. This is an analysis option; vault usage reports alone do not reconstruct all upstream actions. [V1][V8] |
| Compromised broker or browser controller | Isolate tenants, constrain administrative access, protect CA keys/vault caches, and avoid granting arbitrary CDP or host shell access when only narrow actions are needed. Secret proxies relocate trust to the host; they do not protect against that host. [X1][D2][M4] |

Published evidence needs careful labels:

- **Real incident:** Nx's August 2025 s1ngularity supply-chain compromise installed malware that attempted to use locally installed Claude, Gemini, and Q CLIs to search for secrets and publish stolen material through GitHub. The maintainer postmortem describes the initial workflow compromise and secret theft. This was malicious package execution with agent assistance, not proof that every tested agent obeyed every prompt. [T1]
- **Demonstration, not a reported mass breach:** Invariant showed a malicious public GitHub issue influencing an MCP-connected agent to expose private repository information through a public pull request. The account's tools enabled the leak without the attacker needing its OAuth token. [T2]
- **Published research:** Anthropic documents continuing prompt-injection risks and defenses for browser/computer agents. This supports layered isolation and constrained capabilities, not a claim that a model can reliably identify every hostile instruction. [T3]

### Evidence gaps before promising first-release coverage

No universal interoperability guarantee was found for cloning every harness's OAuth session, avoiding all future sign-ins, or reusing a personal subscription across arbitrary parallel/shared workloads. Pinned-version tests remain necessary for refresh rotation, exact credential stores, proxy CA trust, WebSockets, OAuth callback paths, MCP migration, and browser export protections. The specific unconfirmed points above should remain product limitations until verified against the selected versions and contracts. [C2][O1][P3][G2][D3][X2]

## Sources

References resolve to primary sources. Source-code `main` links are moving targets; the report records observed behavior, not a release compatibility guarantee.

[C1]: https://code.claude.com/docs/en/authentication
[C2]: https://code.claude.com/docs/en/legal-and-compliance
[C3]: https://code.claude.com/docs/en/network-config
[C4]: https://github.com/anthropics/claude-code/issues/102747
[O1]: https://learn.chatgpt.com/docs/auth
[O2]: https://learn.chatgpt.com/docs/enterprise/access-tokens
[O3]: https://openai.com/policies/terms-of-use/
[O4]: https://learn.chatgpt.com/docs/config-file/config-reference
[P1]: https://opencode.ai/docs/cli/providers
[P2]: https://opencode.ai/docs/providers/
[P3]: https://github.com/anomalyco/opencode/issues/54879
[G1]: https://geminicli.com/docs/get-started/authentication/
[G2]: https://developers.google.com/gemini-code-assist/docs/deprecations/code-assist-individuals
[G3]: https://geminicli.com/docs/resources/faq/
[G4]: https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/code_assist/oauth-credential-storage.ts
[G5]: https://docs.cloud.google.com/gemini/docs/codeassist/sunset
[G6]: https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/mcp/token-storage/hybrid-token-storage.ts
[G7]: https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/config/storage.ts
[U1]: https://cursor.com/docs/cli/reference/authentication
[U2]: https://prod.cursor.com/docs/account/enterprise/service-accounts
[U3]: https://cursor.com/terms-of-service
[H1]: https://docs.github.com/en/copilot/how-tos/copilot-cli/set-up-copilot-cli/authenticate-copilot-cli
[H2]: https://docs.github.com/en/site-policy/github-terms/github-terms-of-service
[A1]: https://ampcode.com/docs/cli/execute-mode
[A2]: https://ampcode.com/security
[A3]: https://ampcode.com/docs/collaborate/workspaces
[A4]: https://ampcode.com/terms
[A5]: https://ampcode.com/docs/cli
[B1]: https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/config-files.md
[B2]: https://github.com/aaif-goose/goose/blob/main/documentation/docs/guides/cli-providers.md
[B3]: https://github.com/aaif-goose/goose/issues/8326
[M1]: https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization
[M2]: https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/security-considerations
[M3]: https://github.com/docker/mcp-gateway
[M4]: https://docs.docker.com/ai/sandboxes/mcp-gateway/
[M5]: https://docs.aws.amazon.com/bedrock-agentcore/latest/devguide/gateway-outbound-auth.html
[M6]: https://docs.composio.dev/docs/authentication
[M7]: https://docs.composio.dev/docs/auth-configuration/connected-accounts
[X1]: https://docs.docker.com/ai/sandboxes/configuration/credentials/
[X2]: https://docs.docker.com/ai/sandboxes/troubleshooting/
[X3]: https://github.com/jingkaihe/matchlock
[X4]: https://github.com/jingkaihe/matchlock/blob/main/pkg/net/tls.go
[D1]: https://playwright.dev/docs/auth
[D2]: https://playwright.dev/docs/api/class-browsercontext
[D3]: https://developer.chrome.com/docs/web-platform/device-bound-session-credentials
[D4]: https://security.googleblog.com/2024/07/improving-security-of-chrome-cookies-on.html
[D5]: https://developer.chrome.com/blog/remote-debugging-port
[D6]: https://docs.browserbase.com/features/contexts
[D7]: https://docs.steel.dev/overview/credentials-api/overview
[D8]: https://www.kernel.sh/ai-library/agent-identity-how-to-authenticate-an-ai-browser-agent
[D9]: https://github.com/browser-use/browser-use/blob/main/skills/cloud/references/sessions.md
[D10]: https://github.com/multizenteam/multizen-browser
[D11]: https://github.com/fellowship-dev/navvi
[D12]: https://help.openai.com/en/articles/11752874-chatgpt-agent
[D13]: https://platform.claude.com/docs/en/agents-and-tools/tool-use/browser-use-tool
[D14]: https://support.google.com/chrome/answer/165139?hl=en
[D15]: https://support.google.com/chrome/a/answer/16465307?hl=en
[D16]: https://www.browserbase.com/templates/manual-mfa-with-contexts
[D17]: https://docs.steel.dev/overview/profiles-api/overview
[D18]: https://github.com/steel-dev/steel-browser
[D19]: https://docs.steel.dev/overview/authentication
[D20]: https://platform.claude.com/docs/en/agents-and-tools/tool-use/computer-use-tool
[D21]: https://playwright.dev/docs/api/class-browsertype#browser-type-launch-persistent-context
[V1]: https://developer.1password.com/docs/service-accounts/
[V2]: https://developer.1password.com/docs/connect/
[V3]: https://developer.1password.com/docs/cli/reference/commands/run/
[V4]: https://1password.com/blog/1password-for-ai-agents
[V5]: https://bitwarden.com/help/access-tokens/
[V6]: https://bitwarden.com/help/cli/
[V7]: https://keepassxc.org/docs/KeePassXC_UserGuide
[V8]: https://proton.me/support/pass-access-tokens
[V9]: https://proton.me/support/ssh-agent
[V10]: https://bitwarden.com/help/secrets-manager-cli/
[V11]: https://bitwarden.com/help/machine-accounts/
[V12]: https://protonpass.github.io/pass-cli/get-started/configuration/
[V13]: https://github.com/protonmail/webclients/blob/main/applications/pass/public/assets/agent-instructions.md
[V14]: https://protonpass.github.io/pass-cli/commands/contents/run/
[S1]: https://developers.google.com/workspace/gmail/api/auth/scopes
[S2]: https://docs.slack.dev/ai/slack-mcp-server/
[T1]: https://nx.dev/blog/s1ngularity-postmortem
[T2]: https://invariantlabs.ai/blog/mcp-github-vulnerability
[T3]: https://www.anthropic.com/research/prompt-injection-defenses

## Options per item

The tables summarize the evidence above, not recommended choices. **Offline** means usable without external networking; “local only” means a local model, vault, or app must provide the function. **Subscriptions** means technical compatibility subject to the vendor restrictions above; it is not permission for account sharing. **Cost** is relative integration/operations effort. “No” in the secret column assumes the control service itself runs outside the agent VM; its handles still carry authority.

### Harness

| Approach | Secret enters sandbox | Works offline | Works with subscriptions | Setup cost | Main risk |
| --- | --- | --- | --- | --- | --- |
| User signs into each native harness in VM | Yes | No for hosted inference | Yes where currently offered; not Gemini consumer [G2] | Medium | Login friction; refresh state and account exposed [C1][O1] |
| Copy credential files or inject real env keys/tokens | Yes | No for hosted inference | Conditional; not universal fleet permission | Low–medium | Secret theft; refresh races; OS-store portability [C1][O1][G4] |
| Service/API/workspace automation identity | Yes, if delivered directly | No for hosted inference | Only vendor-supported workspace/service plans | Medium | Scope, billing and token lifetime [O2][U2][A3][H1] |
| Host placeholder/credential proxy | No | No for hosted inference | Technically some; policy conditional, especially Claude | High | TLS/client compatibility; authorized misuse [X1][X3][C2] |
| Local model/provider without cloud credentials | No | Yes, with local model | Not needed | Medium–high | Hardware/quality/provider feature differences [P2][B1] |

### MCP

| Approach | Secret enters sandbox | Works offline | Works with subscriptions | Setup cost | Main risk |
| --- | --- | --- | --- | --- | --- |
| Remote OAuth client in VM | Yes | No | Service-specific | Medium | Refresh-token theft and scope [M1][M2] |
| Stdio server plus env secrets in VM | Yes | Local tools only | Service-specific | Low–medium | Server code can read secrets [M1] |
| Host gateway with per-user connections | No | Local tools only | Service-specific | Medium–high | Host tool execution and confused account selection [M4] |
| Managed connection gateway | No | No | Service-specific | Medium | Provider dependency and powerful broker key [M5][M6] |

### Browser

| Approach | Secret enters sandbox | Works offline | Works with subscriptions | Setup cost | Main risk |
| --- | --- | --- | --- | --- | --- |
| Profile copy or storageState import | Yes | Local pages/state only | Site-specific; not a new entitlement | Medium | Cookie theft; device binding; expiry [D1][D3][D4] |
| Host/separate-browser raw CDP | No initially; retrievable | Local pages only | Site-specific | Medium–high | Cookie/DOM/network extraction; broad actions [D2][D5] |
| Narrow browser action/fill broker | No, if export methods excluded | Local pages only | Site-specific | High | Incomplete UI coverage; account misuse [V4][D2] |
| Hosted persistent context | No initially; API-dependent export | No | Site-specific | Medium | Third-party profile custody; takeover dependency [D6][D7][D8][D9] |
| Human login/takeover then retain session | Yes if browser is in VM; otherwise no | Login normally needs network | Site-specific | Medium | Repeated MFA/CAPTCHA and session expiry [D6][D12] |

### Password manager

| Approach | Secret enters sandbox | Works offline | Works with subscriptions | Setup cost | Main risk |
| --- | --- | --- | --- | --- | --- |
| CLI retrieval or `op run`/Pass run in VM | Yes | KeePassXC/local vault only; cloud auth varies | Manager plan-dependent | Low–medium | Env/output/file exfiltration [V3][V6][V7][V8] |
| Host service account/Connect/Secrets Manager broker | No, if it performs use | Cached/local service only | Manager plan-dependent | Medium–high | Broad vault scope; broker token theft [V1][V2][V5] |
| Approved fill-on-behalf integration | No to agent interface; browser remains sensitive | Usually no for website login | Integration/plan-dependent | High; Lamp availability unconfirmed | Agent may abuse resulting session [V4] |
| Host signing agent | No private key | Local signing yes | Not an inference subscription mechanism | Medium | Authorized signing misuse; protocol-specific [V9] |

### Other apps

| Approach | Secret enters sandbox | Works offline | Works with subscriptions | Setup cost | Main risk |
| --- | --- | --- | --- | --- | --- |
| App API with real OAuth/API token in VM | Yes | No for SaaS | App-specific API entitlement | Medium | Token/data exfiltration; excessive scope [S1] |
| Host/remote MCP or typed API handle | No | Local apps only | App-specific | Medium–high | Tool authority, injection, account mix-up [M4][M6][S2] |
| Computer use on logged-in host desktop | No initially; screen/control may reveal it | Local apps only | App-specific | High | Real desktop access and collateral actions [D13] |
| Dedicated remote desktop/browser session | No in agent VM; yes in remote environment | Local apps only | App-specific | High | Another privileged stateful environment to isolate [D2][D13] |
