# Remote access and GitHub CI: research notes

Checked against live docs on 2026-11-08. Every claim has a source link.
"(unconfirmed)" marks claims that the fetched primary source did not state outright.
These notes lay out options and trade-offs. They make no design decisions.

Terms follow `GLOSSARY.md`: **host** (the machine running Lamp), **caller**, **sandbox**, **relay** (a service run by the Lamp project).
Third-party relays keep their own names (DERP, SniTun, and so on) so they don't get mixed up with a Lamp relay.

---

## Part 1: Reaching a self-hosted host

### 1.0 The core problem

- A host at home often sits behind NAT. It may also sit behind carrier-grade NAT (CGNAT, the shared `100.64.0.0/10` space in [RFC 6598](https://datatracker.ietf.org/doc/html/rfc6598)). Port forwarding doesn't work there.
- Callers include GitHub-hosted runners. Those are short-lived machines on the public internet, outside any private network.
- So each option has to answer three questions: how packets get in, who terminates TLS, and how the host knows who the caller is.

### 1.1 Tailscale (tailnet, tsnet, Funnel)

**How it gets through NAT.** Every connection starts relayed through Tailscale's DERP servers. Devices then try NAT traversal to get a direct WireGuard UDP path. If that fails they try a peer relay, and failing that they stay on DERP. DERP has "limited quality of service", with higher latency and lower throughput. Hard NAT on both sides, or blocked UDP, can rule out a direct path. ([connection types](https://tailscale.com/kb/1257/connection-types))

**Embedding (tsnet).** `tailscale.com/tsnet` puts a full tailnet node inside a Go program, using a userspace network stack. ([tsnet KB](https://tailscale.com/kb/1244/tsnet)) Module v1.104.5 was published 2026-11-02. ([pkg.go.dev](https://pkg.go.dev/tailscale.com/tsnet)) Useful `tsnet.Server` fields and methods ([pkg.go.dev](https://pkg.go.dev/tailscale.com/tsnet)):

| API | What it does |
|---|---|
| `Hostname`, `Dir`, `Store` | Node name, and where state lives (defaults to the user config directory). |
| `AuthKey` | Pre-approved join. It falls back to the `TS_AUTHKEY` env var. With no key, `Start` prints a login URL. |
| `ClientSecret`; `ClientID` + `IDToken` + `Audience` | Join with an OAuth client secret, or with workload identity federation. Federation needs the `tailscale.com/feature/identityfederation` import. |
| `ControlURL` | Coordination server. It falls back to `TS_CONTROL_URL`, then Tailscale's default. This is how a host points at Headscale. |
| `Ephemeral`, `AdvertiseTags` | An ephemeral node, and tags for policy. |
| `ListenTLS` | Tailnet-only listener using the tailnet's `*.ts.net` certificate. |
| `ListenFunnel` | Public internet listener over Funnel, on ports 443, 8443 or 10000. `FunnelOnly()` turns off tailnet listening. |
| `ListenService` | Hosts a named Tailscale Service. The node must be tagged. |
| `LocalClient().WhoIs(addr)` | Returns the tailnet owner of a remote address. ([local pkg](https://pkg.go.dev/tailscale.com/client/local)) The `CapMap` field name in the response is (unconfirmed). |

The licence is BSD-3-Clause per the [tailscale repo](https://github.com/tailscale/tailscale) (unconfirmed: the licence file was not fetched).

**Access control.** Grants are deny-by-default rules with `src`, `dst`, `ip` (for example `tcp:443`) and `app`. `app` carries *application capabilities* such as `example.com/cap/name` with JSON parameters that the destination app reads and enforces. Tags work as `src` and `dst`. ([grants](https://tailscale.com/kb/1324/grants)) So Lamp could define its own capability and let tailnet policy say who may create sandboxes. Devices registered with OAuth credentials are tag-owned. ([OAuth clients](https://tailscale.com/kb/1215/oauth-clients))

**Credentials:**

| Credential | Lifetime | Notes |
|---|---|---|
| Auth key | 1 to 90 days (default 90) | One-off or reusable, ephemeral, pre-approved, tagged. Revoking a key doesn't remove the devices that used it. ([auth keys](https://tailscale.com/kb/1085/auth-keys)) |
| OAuth client | Access tokens last 1 hour | Scoped (for example `auth_keys`, `devices:core`). An `auth_keys` client must carry tags. Its secret can go to `tailscale up --auth-key` with `?ephemeral=` and `preauthorized`. ([OAuth clients](https://tailscale.com/kb/1215/oauth-clients)) |
| Workload identity federation | Short-lived API token | Exchanges an OIDC JWT from GitHub Actions, AWS, GCP, Azure or a custom issuer. Matches on `sub` (wildcards allowed) and custom claims. Needs the `auth_keys` scope to register nodes. Status and plan are not stated. ([WIF](https://tailscale.com/kb/1581/workload-identity-federation)) |

**GitHub Actions callers.** `tailscale/github-action@v4` adds the runner as an ephemeral, tagged node. Tailscale recommends workload identity federation (`oauth-client-id` + `audience`, with `id-token: write`). OAuth secret and auth key also work. `tags` is required. Federated nodes are pre-approved. New nodes take time to propagate, so a `ping` input can wait up to 3 minutes. It works on GitHub-hosted and self-hosted runners. ([GitHub Action KB](https://tailscale.com/kb/1276/tailscale-github-action))

**Funnel (public exposure).** It is in beta and on all plans. It needs MagicDNS, HTTPS certificates and a `funnel` node attribute in policy. Names are only `<tailnet>.ts.net`. TLS is terminated on the device; relays pass encrypted bytes through. Bandwidth limits apply and can't be configured. DNS can take 10 minutes to propagate. ([Funnel](https://tailscale.com/kb/1223/funnel)) Custom domains look unsupported (unconfirmed). Funnel traffic comes from the public internet, so Lamp would need its own app-level auth on it.

**Cost.** The Personal plan is $0 with 6 users and unlimited user devices. It includes 50 tagged resources (then $1/month each) and 1,000 ephemeral-node minutes a month. It is **non-commercial use only**. Standard is $8 and Premium $18 per user a month. Tagged and ephemeral overages are "not currently enforced", but enforcement is planned. ([pricing](https://tailscale.com/pricing))

**Failure modes:**
- DERP-only paths are slow.
- GitHub jobs eat the ephemeral-minute pool.
- Funnel is beta, with bandwidth caps.
- The Personal plan bars commercial use.
- Node propagation lag hits CI.
- Callers need an account in the owner's tailnet, or shared access.

**Owner setup:** create a tailnet, add tags and grants, then log the host in (a URL or an auth key). **Caller setup:** install Tailscale on the device, or use the GitHub Action, and join the same tailnet.

### 1.2 Headscale (self-hosted Tailscale control server)

- It is open source (BSD-3-Clause) and serves "a single Tailscale network". It targets personal use or small open-source organisations. It is not linked to Tailscale Inc. ([repo](https://github.com/juanfont/headscale), [docs](https://headscale.net/stable/))
- Supported features include pre-auth keys, tags, ephemeral nodes, an embedded DERP server, peer relays, ACLs and grants, Tailscale SSH, OIDC registration and MagicDNS. Funnel, Serve and network flow logs link to open issues, so they look unsupported. ([features](https://headscale.net/stable/about/features/))
- The project does not "support nor encourage" running Headscale behind reverse proxies or in containers. ([repo](https://github.com/juanfont/headscale))
- Trade-off: no third-party account is needed. But Headscale itself must be reachable by every node, so it needs a public server. That makes it a poor fit for "only a box behind NAT".
- tsnet joins Headscale through `ControlURL`. ([pkg.go.dev](https://pkg.go.dev/tailscale.com/tsnet)) Whether `tailscale/github-action` takes a custom control URL is (unconfirmed).

### 1.3 Cloudflare Tunnel and Access

**How it works.** `cloudflared` makes outbound-only connections to Cloudflare, so no inbound ports are opened. You can run several connectors per tunnel for HA. ([Tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/)) Outbound port 7844 must be open. Publishing a public hostname requires a domain added to Cloudflare. ([create remote tunnel](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/get-started/create-remote-tunnel/)) TLS for public hostnames ends at Cloudflare's edge (unconfirmed on the fetched pages; it is the implied model).

**Quick Tunnels** (`cloudflared tunnel --url ...`) need no account and no domain. But they are "for testing and development". They give a random `trycloudflare.com` hostname each time, with no uptime guarantee, a 200 in-flight request cap and no SSE support. ([Quick Tunnels](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/do-more-with-tunnels/trycloudflare/))

**Caller auth: Access service tokens.** The caller sends `CF-Access-Client-Id` and `CF-Access-Client-Secret`, or one JSON `Authorization` header. The policy must use the **Service Auth** action. Tokens have a set duration and can be refreshed. New secrets use a `cfast_` prefix with a checksum. "Strict" mode returns 401/403 instead of a login redirect. ([service tokens](https://developers.cloudflare.com/cloudflare-one/identity/service-tokens/)) The origin should validate the `Cf-Access-Jwt-Assertion` header: RS256, keys from `https://<team>.cloudflareaccess.com/cdn-cgi/access/certs`, check `iss` and the app's `aud` tag. Keys rotate about every 6 weeks. ([JWT validation](https://developers.cloudflare.com/cloudflare-one/access-controls/applications/http-apps/authorization-cookie/validating-json/))

**Cost.** Zero Trust Free covers up to 50 users, and Tunnel is included. ([Cloudflare blog](https://blog.cloudflare.com/zero-trust-private-networking-rules/), [pricing](https://en-us.www.cloudflare.com/teams-pricing/)) The exact current limits were not on the fetched plan page (unconfirmed).

**Embedding.** `cloudflared` is a Go daemon and CLI under Apache-2.0. Nothing suggests library use. Cloudflare only supports versions within one year of the latest release. ([repo](https://github.com/cloudflare/cloudflared)) Lamp would ship or wrap it as a sidecar.

**Failure modes:**
- A domain on Cloudflare is required.
- A third-party account is required.
- Quick Tunnel URLs change on every run.
- Outbound 7844 may be blocked.
- Old `cloudflared` builds lose support after a year.

### 1.4 Direct public HTTPS (Caddy / CertMagic, port forwarding, DDNS)

- **Needs:** A/AAAA records pointing at the host, ports 80 and 443 reachable from outside (forwarded if needed), and persistent storage. Challenges are HTTP-01, TLS-ALPN-01, or DNS-01 through a DNS-provider plugin. DNS-01 opens no ports and allows wildcards. Default CAs are Let's Encrypt and ZeroSSL, with fallback. ([Caddy automatic HTTPS](https://caddyserver.com/docs/automatic-https))
- **Embedding:** CertMagic, the library behind Caddy, takes one call: `certmagic.HTTPS([]string{"example.com"}, mux)`. DNS-01 works through any libdns provider. Apache-2.0. ([CertMagic](https://github.com/caddyserver/certmagic))
- **Rate limits:** 50 new certificates per registered domain per 7 days, and 5 duplicate certificates per 7 days. Registered domains are found with the Public Suffix List. ARI renewals are exempt. ([LE rate limits](https://letsencrypt.org/docs/rate-limits/)) This matters if a relay hands out `<host>.lamp-domain` names: many hosts under one domain share the 50/week cap unless the domain is on the PSL or has an override.
- **IP-address certificates:** These went GA in January 2026, but only as 6-day ("shortlived" profile) certificates, validated by HTTP-01 or TLS-ALPN-01. So inbound 80/443 is still needed. ([LE GA post](https://letsencrypt.org/2026/01/15/6day-and-ip-general-availability))
- **Failure modes:**
  - CGNAT or no public IP makes this impossible.
  - Many ISPs block port 80/443 (unconfirmed; no primary source fetched).
  - Dynamic IPs need DDNS.
  - The host is exposed to internet scanning.
  - The owner needs a domain or a DDNS name.
- **Caller setup** is trivial: a plain HTTPS URL plus a token. It suits GitHub-hosted runners.

### 1.5 Hosted and self-run tunnel services

| Service | Model | Free tier / cost | Embeddable | Source |
|---|---|---|---|---|
| ngrok | Agent dials out to ngrok's edge. | Free: 3 endpoints, 1 GB, 20k requests, one dev domain, interstitial page on browser HTTP traffic. Custom domains from $20/month. TCP needs a verified card. mTLS is a paid feature. | Yes: `ngrok-go` (MIT) gives a `net.Listener`. It needs an ngrok authtoken. | [pricing](https://ngrok.com/pricing), [ngrok-go](https://github.com/ngrok/ngrok-go) |
| frp | `frpc` behind NAT dials a public `frps` that you run. | Free (Apache-2.0); you pay for a VPS. | Binaries are documented. Library use is (unconfirmed). | [frp](https://github.com/fatedier/frp) |
| wgtunnel (Coder) | The client registers a WireGuard key and gets a subdomain derived from it. `tunneld` runs on your server behind Caddy. | Free (MIT), self-hosted. | Go, so likely (unconfirmed). | [wgtunnel](https://github.com/coder/wgtunnel) |
| SniTun (Nabu Casa) | TCP multiplexer plus an SNI proxy. TLS ends on the host. | Open source; Nabu Casa runs the service. | Python. | [snitun](https://github.com/NabuCasa/snitun) |

frp details: proxy types are TCP, UDP, HTTP(S), STCP and XTCP (P2P). Auth is a token or OIDC client credentials. Transport TLS is on by default, but `frpc` does not verify the server certificate unless configured to. v2 will break compatibility with v1. ([frp](https://github.com/fatedier/frp))

### 1.6 How other self-hosted products handle remote access

| Product | Mechanism | Who runs the infrastructure | Funding |
|---|---|---|---|
| Home Assistant (Nabu Casa remote UI) | The instance dials out to a Nabu Casa proxy. The proxy routes by TLS SNI and does not decrypt. The instance holds its own Let's Encrypt certificate, and the private key stays only on the instance. Certificates show up in CT logs. ([HA cloud](https://www.home-assistant.io/cloud/), [snitun](https://github.com/NabuCasa/snitun), [deep dive](https://support.nabucasa.com/hc/en-us/articles/25619268678557-Remote-access-Deep-dive) (403 on fetch; details via search summary, unconfirmed)) | Nabu Casa (company) | Paid plan, $6.50/month or $65/year. "Subscriptions also help fund the development of Home Assistant itself." ([HA cloud](https://www.home-assistant.io/cloud/), [Nabu Casa](https://www.nabucasa.com/link/)) |
| Coder | With no access URL, `coder server` sets up a `*.try.coder.app` URL "for evaluation". Production needs `--access-url`. ([README](https://github.com/coder/coder), [setup](https://coder.com/docs/admin/setup)) wgtunnel powers those trial tunnels. ([wgtunnel](https://github.com/coder/wgtunnel)) Workspaces connect over WireGuard: STUN (Google's server by default) for direct paths, with a **DERP server built into the control plane** as fallback. The control plane must be reachable with no NAT in between. ([networking](https://coder.com/docs/admin/networking)) | Coder (company), for trials only | Commercial product |
| Syncthing | Relays are a fallback. They forward end-to-end-encrypted data but see IPs and device IDs. ([relaying](https://docs.syncthing.net/users/relaying.html)) Relays are "community-contributed" and join the pool `relays.syncthing.net` by default. ([strelaysrv](https://docs.syncthing.net/users/strelaysrv.html)) | Volunteers. The pool and discovery servers are run by the project (unconfirmed). | Donations (relay funding unconfirmed) |
| Portainer | The Edge Agent polls the server (every 5 s by default), plus a TLS tunnel to server port 8000. The server must be reachable by the agent. ([Edge agent](https://docs.portainer.io/admin/environments/add/docker/edge)) | The user hosts the server. No project relay. | Commercial editions |
| Daytona | It moved its production code to closed source on 2026-06-11. The last open release is v0.190.0 (AGPL). ([announcement](https://www.daytona.io/dotfiles/updates/daytona-is-going-closed-source), [repo](https://github.com/daytonaio/daytona)) BYOC "custom regions" are Helm-deployed and managed by Daytona's control plane. The diagram shows a "Reverse Proxy Tunnel", but connection direction isn't stated (unconfirmed). ([BYOC](https://www.daytona.io/docs/en/bring-your-own-compute)) | Daytona (company) | Commercial |
| Tailscale / Cloudflare / ngrok | Covered above | Company | Free tier that leads to paid plans |

Pattern: projects that run a relay either charge for it (Nabu Casa), limit it to trials (Coder), or lean on volunteers (Syncthing). Nabu Casa's design shows a relay can avoid seeing plaintext if TLS ends on the host.

### 1.7 Caller authentication patterns

| Pattern | How it works | Fits | Trade-offs |
|---|---|---|---|
| Scoped API tokens | Bearer token with scopes and expiry, stored hashed. Example: Cloudflare's `cfast_` prefix plus checksum, which secret scanners can spot. ([service tokens](https://developers.cloudflare.com/cloudflare-one/identity/service-tokens/)) Tailscale OAuth scopes such as `auth_keys` and `devices:core`. ([OAuth](https://tailscale.com/kb/1215/oauth-clients)) | CLI, agents, any CI | A long-lived secret that leaks until revoked. |
| GitHub OIDC (keyless) | The job needs `permissions: id-token: write`. It fetches a JWT through `ACTIONS_ID_TOKEN_REQUEST_URL`/`_TOKEN`, with a custom `audience`. Issuer `https://token.actions.githubusercontent.com`, discovery at `/.well-known/openid-configuration`. ([OIDC reference](https://docs.github.com/en/actions/reference/security/oidc)) The example token lives 300 s. ([OIDC concepts](https://docs.github.com/en/actions/concepts/security/openid-connect)) The service validates claims and swaps the JWT for a short-lived token. PyPI's token lasts 15 minutes. ([PyPI trusted publishers](https://docs.pypi.org/trusted-publishers/)) | GitHub Actions callers | No stored secret. The Lamp endpoint must be reachable from the job. Fork PRs can't get write permissions, so `id-token` is likely unavailable there (inferred from the write-to-read rule in [workflow syntax](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax); unconfirmed for `id-token`). |
| Network identity | Tailscale `WhoIs` plus grant app capabilities ([grants](https://tailscale.com/kb/1324/grants)), or a Cloudflare Access JWT. | Tailnet or tunnel callers | Ties auth to that network vendor. |
| mTLS | Client certificates checked by the server (Go `crypto/tls` `RequireAndVerifyClientCert`, [pkg](https://pkg.go.dev/crypto/tls#ClientAuthType)). | Machine-to-machine | Certificate issuing and rotation is up to you. ngrok charges for it. ([ngrok pricing](https://ngrok.com/pricing)) |

**GitHub OIDC claims worth binding:** `repository_id`, `repository_owner_id`, `job_workflow_ref`, `environment`, `ref`, `event_name`, `runner_environment`, `repository_visibility`.
- `sub` formats: `repo:ORG/REPO:environment:NAME`, `...:pull_request`, `...:ref:refs/heads/BRANCH`.
- **Repos created (or renamed or transferred) after 2026-07-15 use an immutable `sub`**: `repo:OWNER@OWNER-ID/REPO@REPO-ID:ref:...`.
- `sub` can be customised with `PUT /repos/{owner}/{repo}/actions/oidc/customization/sub`.
- Source: [OIDC reference](https://docs.github.com/en/actions/reference/security/oidc).

---

## Part 2: GitHub CI and GitHub agents

### 2.1 Building blocks

| Piece | Facts | Source |
|---|---|---|
| Self-hosted runner | Free to use with Actions; you pay for the machine. Can be repo, org or enterprise level (enterprise needs Enterprise Cloud). | [concepts](https://docs.github.com/en/actions/concepts/runners/self-hosted-runners), [add runners](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/add-runners) |
| Network | Outbound HTTPS 443 only, to github.com, api.github.com and `*.actions.githubusercontent.com`, at 70 kbps or more. No inbound access is listed. A job queued over 24 h fails. | [reference](https://docs.github.com/en/actions/reference/runners/self-hosted-runners) |
| Runner updates | Auto-updates by default. With `--disableupdate` you must update within 30 days, or GitHub stops queueing jobs to that runner. Sandbox images must keep the runner binary current. | [reference](https://docs.github.com/en/actions/reference/runners/self-hosted-runners) |
| OS | Linux (x64 and ARM64), Windows, macOS 11+. ARM64 on Windows and macOS is in preview. Docker container actions, job containers and service containers need **Linux plus Docker**. | [reference](https://docs.github.com/en/actions/reference/runners/self-hosted-runners), [service containers](https://docs.github.com/actions/tutorials/communicating-with-docker-service-containers) |
| Ephemeral runner | `--ephemeral` makes GitHub deregister the runner after one job. GitHub recommends ephemeral runners for autoscaling. | [reference](https://docs.github.com/en/actions/reference/runners/self-hosted-runners) |
| Registration token | `POST /repos/{o}/{r}/actions/runners/registration-token` (or `/orgs/{org}/...`). Expires in 1 h. | [REST](https://docs.github.com/en/rest/actions/self-hosted-runners) |
| JIT config | `POST /repos/{o}/{r}/actions/runners/generate-jitconfig` (or `/orgs/{org}/...`). Body: `name`, `runner_group_id`, `labels` (1 to 100), `work_folder`. Returns `encoded_jit_config`, which is passed to the runner at startup. No config step and no reusable token. | [REST](https://docs.github.com/en/rest/actions/self-hosted-runners) |
| `workflow_job` webhook | Actions `queued`, `waiting`, `in_progress`, `completed`. A GitHub App needs at least **Actions: read**. Needs a public endpoint to receive it. | [webhooks](https://docs.github.com/en/webhooks/webhook-events-and-payloads) |
| Runner scale sets | A listener holds an HTTPS long-poll to GitHub for "Job Available" messages. Runners register with JIT configs. | [ARC concepts](https://docs.github.com/en/actions/concepts/runners/actions-runner-controller) |
| `actions/scaleset` | GitHub's standalone Go client for scale sets (MIT, Go 1.25+, public preview since February 2026). Build autoscaling "without requiring Kubernetes". Each poll blocks about 50 s. Scale to `statistics.TotalAssignedJobs`. A GitHub App is preferred (custom JWT signer supported); a PAT works. ARC 0.14.0 (2026-03-19) uses it as its only client. | [repo](https://github.com/actions/scaleset), [changelog](https://github.blog/changelog/2026-02-05-github-actions-early-february-2026-updates/), [ARC 0.14.0](https://github.blog/changelog/2026-03-19-actions-runner-controller-release-0-14-0/) |
| Runner groups and labels | Every org gets a default group. Custom org groups need the Team plan. **Public repos can't use a group's runners by default.** | [manage access](https://docs.github.com/en/actions/how-tos/manage-runners/self-hosted-runners/manage-access) |

**Two intake models, and what they mean for a host behind NAT:**

| Model | Flow | Inbound needed? |
|---|---|---|
| Webhook + JIT | A `workflow_job queued` event reaches your endpoint. You call `generate-jitconfig`, boot a sandbox and run the runner. | Yes, a public webhook URL. Or a hosted control plane that takes the webhook and passes work to an agent that pulls it (Blacksmith's model, §2.4). |
| Scale set long-poll | The host long-polls GitHub with `actions/scaleset` and creates JIT runners on demand. | **No**, outbound HTTPS only. RunsOn "Fleet mode" works this way and has "no inbound endpoint". ([RunsOn](https://runs-on.com/)) |

### 2.2 GitHub App versus PAT

| Credential | Repo-level runners | Org-level runners | Notes |
|---|---|---|---|
| GitHub App | Administration: read and write | Self-hosted runners: read and write | Short-lived installation tokens. Can also get `workflow_job` webhooks (Actions: read). Can't register **enterprise**-level runners. ([App permissions](https://docs.github.com/en/rest/authentication/permissions-required-for-github-apps), [ARC auth](https://docs.github.com/en/actions/tutorials/use-actions-runner-controller/authenticate-to-the-api)) |
| Fine-grained PAT | Administration: read and write | Administration: read, plus Self-hosted runners: read and write | Tied to a user. ([ARC auth](https://docs.github.com/en/actions/tutorials/use-actions-runner-controller/authenticate-to-the-api)) |
| Classic PAT | `repo` | `admin:org` | Broadest scope. ([REST](https://docs.github.com/en/rest/actions/self-hosted-runners)) |

A real app's request, from Namespace: Actions RW, Administration RW, Organization self-hosted runners RW, Metadata R. Org-level runners need fewer permissions than repo-level ones. ([Namespace](https://namespace.so/docs/solutions/github-actions))

### 2.3 Actions Runner Controller (ARC)

- It is a Kubernetes operator. The controller reconciles `AutoScalingRunnerSet`, a listener pod long-polls GitHub, and ephemeral runner pods register with JIT configs. Failed pods retry up to 5 times. ([ARC](https://docs.github.com/en/actions/concepts/runners/actions-runner-controller))
- Container modes: Kubernetes mode (needs runner container hooks) or Docker-in-Docker. ([ARC](https://docs.github.com/en/actions/concepts/runners/actions-runner-controller))
- ARC is still GitHub's recommended Kubernetes option. The scale set client is the non-Kubernetes path. ([changelog](https://github.blog/changelog/2026-02-05-github-actions-early-february-2026-updates/))

### 2.4 How products do it

| Product | Compute location | Isolation | Job intake | Open source | Pricing | Sources |
|---|---|---|---|---|---|---|
| actuated | **Your** servers (bare metal or nested virt), Linux amd64 and arm64. | Firecracker microVM, one build per VM. | GitHub App webhooks (workflow runs and jobs). The SaaS control plane (OpenFaaS Ltd, on Linode UK) sends a short-lived registration token encrypted to the agent's public key, over HTTPS. Whether the agent dials out is (unconfirmed). | No | Fixed monthly subscription | [docs](https://docs.actuated.com/), [FAQ](https://docs.actuated.com/faq/) |
| Ubicloud | Ubicloud's cloud on leased bare metal (Hetzner, Leaseweb, AWS Bare Metal), or your own install. | Cloud Hypervisor VMs in Linux namespaces. | "Ubicloud Managed Runners" GitHub App. Label `ubicloud-standard-2`. | Yes, AGPL-3.0 | $2.50/month credit (about 1,250 min) | [repo](https://github.com/ubicloud/ubicloud), [quickstart](https://www.ubicloud.com/docs/github-actions-integration/quickstart) |
| RunsOn | Your AWS account (CloudFormation or Terraform). | Fresh EC2 instance per job, destroyed afterwards. | Flex: webhooks to API Gateway in your account. Fleet: pulls from scale sets, no inbound. The label is the spec (`cpu=4`, ...). | Licensed; full source on Enterprise | Yearly licence from €300/$350, free for non-commercial use | [site](https://runs-on.com/) |
| Namespace | Namespace's own hardware and racks. | A VM per job, with its own kernel; cleaned up after. | GitHub App, `runs-on: namespace-profile-<name>`. Public repos are supported because every job gets a clean VM. | No | not fetched | [docs](https://namespace.so/docs/solutions/github-actions), [security](https://namespace.so/blog/defense-in-depth-how-we-think-about-security), [2023 blog](https://namespace.so/blog/completing-the-circle-with-github-runners) |
| Blacksmith | Own bare metal (gaming CPUs) in the US and EU. | Ephemeral Firecracker microVM, boots in under 3 s, own network namespace, destroyed after the job. | The AWS control plane mints a **JIT token per job (1 h expiry)** and queues it in Redis. Agents on bare metal pull it over Tailscale. GitHub orgs only. | No | 3,000 free min/month; half GitHub's per-minute price | [security](https://www.blacksmith.sh/security), [overview](https://docs.blacksmith.sh/blacksmith-runners/overview), [quickstart](https://docs.blacksmith.sh/introduction/quickstart) |
| Depot | Depot's AWS, or your AWS account. | Single-tenant EC2 "never reused". | Webhook-driven. `runs-on: depot-ubuntu-24.04`. Orgs only. | No | $20/month for 2,000 min, then $0.006/min | [docs](https://depot.dev/docs/github-actions/overview) |
| GARM | Your clouds: LXD, Incus, k8s, OpenStack, EC2, Azure, GCP, OCI, ... | Ephemeral instances through provider plugins. | `workflow_job` webhooks matched by label, **or native scale sets**. Supports GitHub, GHES and Gitea. | Yes, Apache-2.0 | Free | [repo](https://github.com/cloudbase/garm) |
| terraform-aws-github-runner | Your AWS (spot instances, scales to zero). | Ephemeral EC2. | Webhook, then SQS and Lambdas. JIT. Needs a GitHub App. | Yes, MIT | Free | [repo](https://github.com/github-aws-runners/terraform-aws-github-runner) |

Pattern: the vendors that serve public repos (actuated, Namespace, Blacksmith) all use a fresh VM with its own kernel per job, never containers on a shared kernel.

### 2.5 Security on public repositories

- GitHub: "self-hosted runners should almost never be used for public repositories". Non-ephemeral runners "can be persistently compromised". JIT runners improve registration security, but "re-using hardware to host JIT runners can risk exposing information". Ask whether the machine "has network access to sensitive services" such as cloud metadata. Use separate runner groups as boundaries. ([secure use](https://docs.github.com/en/actions/reference/security/secure-use))
- Fork PR approval levels for public repos: first-time contributors new to GitHub, first-time contributors, or **all external contributors**. ([repo Actions settings](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository))
- Fork `pull_request` runs get no secrets and a read-only `GITHUB_TOKEN`. `pull_request_target` gets write access and "may lead to security vulnerabilities". ([events](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows))
- For a home host, the risky network is the owner's LAN, not cloud metadata (an inference from GitHub's guidance).
- Codex's action warns that its account changes "can outlive a job on reused self-hosted runners; use this strategy on disposable runners". ([codex-action](https://github.com/openai/codex-action))
- actuated says its VMs "can be used with public repositories" because nothing carries over between builds. ([FAQ](https://docs.actuated.com/faq/))

### 2.6 Billing note

GitHub announced a $0.002/min "cloud platform charge" on self-hosted runner use from 2026-03-01. It then said: "We're postponing the announced billing change for self-hosted GitHub Actions to take time to re-evaluate our approach." No new date is given. ([GitHub pricing changes](https://github.com/resources/insights/2026-pricing-changes-for-github-actions), [changelog](https://github.blog/changelog/2025-12-16-coming-soon-simpler-pricing-and-a-better-experience-for-github-actions/))

### 2.7 GitHub coding agents on custom compute

| Agent | Where it runs by default | Custom or self-hosted option | Constraints | Sources |
|---|---|---|---|---|
| Copilot cloud agent (formerly "coding agent") | A cloud sandbox is the default. Other options: GitHub Actions-based, or "Remote" (a custom environment). Cloud sandboxes don't support custom compute or self-hosted runners. | **Actions-based:** in `.github/workflows/copilot-setup-steps.yml`, set `runs-on` in the single job named `copilot-setup-steps`. Target an ARC scale set or a scale set built with the **Runner Scale Set Client**. **Remote:** `copilot --environment-host` registers your own computer or VM. Its protocol and setup were (unconfirmed). | Ubuntu x64 or Windows x64 only. Ephemeral single-use runners recommended. The built-in firewall must be **disabled** for self-hosted runners. `timeout-minutes` max is 59. The workflow file must be on the default branch. | [env docs](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/coding-agent/customize-the-agent-environment), [about](https://docs.github.com/en/copilot/concepts/agents/cloud-agent/about-cloud-agent), [2025-10-28](https://github.blog/changelog/2025-10-28-copilot-coding-agent-now-supports-self-hosted-runners/), [2026-10-28](https://github.blog/changelog/2026-10-28-work-from-anywhere-with-a-new-connected-copilot-cloud-experience/) |
| Third-party agents in Agent HQ (Claude, Codex) | GitHub's agent infrastructure; each is enabled as its own GitHub App. | Self-hosted runner support for these was (unconfirmed). | Paid Copilot plans. | [third-party agents](https://docs.github.com/en/copilot/concepts/agents/about-third-party-coding-agents), [Agent HQ blog](https://github.blog/news-insights/company-news/pick-your-agent-use-claude-and-codex-on-agent-hq/) |
| Claude Code GitHub Action | "executes entirely on your own GitHub runner". It is a normal action, so any `runs-on` works, including self-hosted (inferred). | Anthropic API (key or workload identity federation), Bedrock, Vertex AI, or Microsoft Foundry. | Install with `/install-github-app`, which needs repo admin. | [claude-code-action](https://github.com/anthropics/claude-code-action) |
| Codex GitHub Action | `codex exec` on any runner. | Self-hosted Linux needs unprivileged user namespaces plus AppArmor or SELinux setup for bubblewrap sandboxing. | Default `drop-sudo` changes can outlive a job, so use disposable runners. | [codex-action](https://github.com/openai/codex-action) |
| Codex cloud (`@codex` GitHub integration) | A container on OpenAI-managed machines (`universal` image, setup scripts, cache up to 12 h). | No self-hosted option documented. | none noted | [cloud env](https://learn.chatgpt.com/docs/environments/cloud-environment), [help](https://help.openai.com/en/articles/20001545-using-codex-cloud) |
| OpenAI Agents API: self-hosted sandboxes | OpenAI runs the harness. You run `codex exec-server` in your sandbox. | Connects **outbound** over WebSocket to `wss://codex-cloud-environments.chatgpt.com` using an environment ID and a restricted key. Providers listed include Daytona, E2B, Modal, Cloudflare and Vercel. | The beta SDK namespace suggests preview (unconfirmed). | [self-hosted sandboxes](https://developers.openai.com/api/docs/guides/agents-api/environments/self-hosted) |

---

## Unconfirmed items to check before relying on them

1. Whether `tailscale/github-action` accepts a Headscale control URL.
2. The `WhoIs` response field for capabilities (`CapMap`).
3. Funnel custom domains.
4. Cloudflare Zero Trust Free limits on the current plan page.
5. Whether `frp` and `wgtunnel` are usable as Go libraries.
6. Who runs Syncthing's relay pool and discovery servers, and how they are funded.
7. Nabu Casa deep-dive details (the page returned 403).
8. Whether the actuated agent dials out or accepts inbound connections.
9. Daytona BYOC tunnel direction.
10. Whether fork PRs can get `id-token`.
11. The protocol behind Copilot's `--environment-host`.
12. Whether Agent HQ's Claude and Codex agents can use self-hosted runners.

---

## Options matrix

Remote-access options for a caller to reach a host:

| Option | Zero-config behind NAT | Works for GitHub Actions callers | Needs third-party account | Self-hostable end to end | Embeddable in one binary |
|---|---|---|---|---|---|
| Tailscale tailnet (tsnet) | Yes (NAT traversal, DERP fallback) | Yes, `tailscale/github-action` with OIDC federation, no stored secret | Yes (Tailscale) | No (Tailscale control plane) | Yes (`tsnet`) |
| Tailscale Funnel | Yes | Yes, public HTTPS; Lamp does its own auth | Yes (Tailscale) | No | Yes (`ListenFunnel`) |
| Headscale + tsnet | No (Headscale needs a public server) | Yes in principle; the action's support is unconfirmed | No | Yes | Client yes (`ControlURL`); server is separate |
| Cloudflare named tunnel + Access | Yes (outbound 7844) | Yes, service-token headers | Yes, plus a domain on Cloudflare | No | No (ship `cloudflared` as a sidecar) |
| Cloudflare Quick Tunnel | Yes | Weak (random URL, 200 in-flight cap, no SSE, testing only) | No | No | No |
| Direct HTTPS (CertMagic/Caddy) | No (port forward, public IP, no CGNAT) | Yes | A domain or DDNS name (none with 6-day IP certificates) | Yes | Yes (CertMagic) |
| ngrok | Yes | Yes | Yes (ngrok) | No | Yes (`ngrok-go`) |
| frp | Yes, once `frps` runs on a VPS | Yes | No (a VPS only) | Yes | Unconfirmed |
| Lamp relay (wgtunnel or SniTun style) | Yes | Yes | Only Lamp's own service | Yes, if the relay code is open | Yes (client side) |

CI intake options for a host serving GitHub jobs:

| Option | Zero-config behind NAT | Needs a public endpoint | Needs Kubernetes | GitHub credential |
|---|---|---|---|---|
| Scale set client (`actions/scaleset`) | Yes | No | No | GitHub App (preferred) or PAT |
| ARC | Yes | No | Yes | GitHub App or PAT |
| Webhook + JIT on the host | No | Yes | No | GitHub App |
| Lamp-hosted control plane + agent pull (Blacksmith style) | Yes | Lamp's service only | No | GitHub App held by the relay or the host |
