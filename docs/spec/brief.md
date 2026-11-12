# Lamp: decisions brief

This is the complete record of what the owner decided while working out Lamp's requirements (November 2026). It includes the owner's answers to the questions raised by the first requirements draft and the round-2 critiques (O1–O10, A1–A5 and B1–B4, accepted on 2026-11-08). It's the input for the architecture spec and the requirements document. Where this brief and any research file disagree, this brief wins. Terms in **bold** are defined in [GLOSSARY.md](../../GLOSSARY.md). Hard-to-reverse decisions are recorded in [docs/adr](../adr).

## 1. What Lamp is

Lamp is open-source software anyone installs on their own machines, as easily as Docker. It creates fast, isolated **sandboxes** on demand for AI agents, **harnesses** and CI. **Callers** use an API, an MCP server or a CLI to create whatever sandbox they need. It works like Modal or Daytona, but self-hosted, open source, and built specifically for agents and harnesses. The goal is to be better than any existing option on speed, security and agent fit, and to prove it with public benchmarks.

Lamp is an Eilmer project. Eilmer (eilmer.si) is the owner's umbrella for open-source tools.

## 2. Who it's for and what it solves

- **For everybody, from day one.** It's an open-source product other people install, not a personal tool.
- **It solves four problems, all equally:**
  - parallelism: many agents at once without colliding
  - safety: agents can't damage the host or leak secrets
  - setup friction: sandboxes start signed in, with tools ready
  - ownership: cloud-sandbox features on your own hardware
- **Competitors and references:**
  - Cloudroom: an open-source Rust core, Apache-2.0, one sandbox per agent thread. The closest match.
  - E2B Embed: E2B's Firecracker-based system on one Linux host, Apache-2.0.
  - Docker Sandboxes: microVMs and a credential proxy.
  - microsandbox, SmolVM and Matchlock: lightweight VM runtimes.
  - Modal and Daytona, both now closed.

## 3. Callers and interfaces

- **Anyone with credentials can start a sandbox:** a person, an agent, a GitHub agent, a CI job, or any outside service.
- **Interfaces:** an API, an MCP server and a CLI, all with the same capabilities.
- **SDKs:** TypeScript in the first release, then Go and Rust right after, all generated from one API definition. (The Python SDK was dropped by the owner, 2026-11-12.)
- **Built for agents:** every CLI command can output JSON, docs come in an LLM-readable form, retries can't create duplicates (idempotency keys), and there's a live event stream.

## 4. Where the agent lives

Both modes are fully supported, and the caller picks:
- **Agent inside:** the harness runs in the sandbox and works on its own.
- **Agent outside:** an outside agent drives the sandbox as a remote computer, running commands, editing files and using the browser.

## 5. Hosts and fleets

- **Host OSes:** Linux (x86_64 and arm64) and macOS (Apple Silicon), both in the first release.
- **Minimum versions:** Linux 5.10+ (Ubuntu 22.04+, Debian 12+, Fedora 40+) and macOS 26+ on Apple Silicon (O10 amended 2026-11-09; see section 22).
- **One fleet spans many hosts** ([ADR 0001](../adr/0001-one-fleet-many-hosts.md)): one address, one set of keys. A single host is a fleet of one.
- **Placement:** Lamp places sandboxes automatically by available room, OS and devices. Callers can pin a sandbox to a host.
- **Coordination moves to another host automatically** when one goes away. Laptops that sleep are normal hosts.
- **Updates:** automatic updates are opt-in and roll out one host at a time.
- **Per host,** Lamp picks the fastest engine that host supports.

## 6. Sandboxes

- **Firecracker-like microVMs,** built to be as fast as possible.
- **Devices only when asked for:** display, audio, keyboard and mouse, GPU and extra disks. USB passthrough is not in release one (owner decision, 2026-11-09). Callers choose the devices and the resources (CPU, memory, disk).
- **Guest OS:**
  - Linux on every host.
  - macOS sandboxes on Mac hosts, limited to 2 per Mac by Apple's license.

  macOS sandboxes are a clearly labeled, slower full-VM class. Lamp doesn't redistribute macOS images. Each user builds them on their own Mac, from Apple's installer, under the applicable license.
- **Hosts that can't run VMs** get a weaker, clearly labeled fallback: gVisor on Linux, or a restricted process using App Sandbox on macOS. Sandboxes holding logins or secrets require a real VM unless the owner explicitly overrides that. Public-repo CI jobs always require a fresh VM.
- **Forks:** a running sandbox, memory included, can be forked. If Apple's supported APIs can't fork a running macOS guest, macOS sandboxes get suspend and resume plus a clearly labeled disk-only fork that boots fresh. Linux sandboxes on Macs still fork fully, memory included.

## 7. Speed and quality targets

| Target | Value | Release measurement |
|---|---|---|
| Create from a template | p50 under 500 ms, from API call to a command actually running | Linux |
| Fork a running sandbox, memory included | under 1 s | Linux |
| Speed inside a sandbox | at least 97% of the host's native speed, measured separately for computation, disk and network | Linux |
| Secret protection | 0 leaks of API keys, passwords and browser cookies | Everywhere |

Owner amendment, 2026-11-10: measure these speed targets and the zero-leak goal once at release and publish the results honestly. They never block implementation, merge or release. There is no required attack suite, trial count or outlier rule. The speed measurements use a **standard sandbox**: 2 vCPU, 4 GiB RAM and 20 GiB disk, with up to 1 GiB of recently written memory at fork time. Larger sizes are measured and published without timing prerequisites. Mac targets are set after measuring the supported hypervisors. Subscription tokens are a documented exception to the leak target ([ADR 0004](../adr/0004-subscription-logins-stay-on-user-hosts.md)). Exposures the owner explicitly opts into don't count either: a secret injected into a sandbox, cookies copied into one, or the weak-boundary override for secrets. They're flagged on the dashboard and recorded in the audit log.

## 8. Templates and recipes

- **No Docker images, no Dockerfiles, no Docker runtime** ([ADR 0007](../adr/0007-own-template-format-no-docker.md)). Templates come from Lamp's own format or are saved from a running sandbox.
- **Templates keep tools agents installed,** so later sandboxes start with them.
- **Docker inside a sandbox** is an optional kit, for CI and tasks that need `docker build`, compose or service containers.
- **A recipe** is a named setup: template, harness, kits, devices, resources, network and connections. Callers ask for it by name.

## 9. Lifecycle

- **Default:** suspend when idle. Memory and disk are saved, the sandbox uses no CPU or RAM, and it resumes instantly on the next message. It's deleted after a set time.
- **CI default:** deleted when the job ends.
- **Callers can override** the lifecycle for each sandbox.
- **Idle** means no session activity, no running processes and no open connections. A harness waiting for the person's next message doesn't count as activity. Builds, dev servers, background jobs and open connections do.
- **Warm pool** of ready sandboxes.
- **Fork mid-task,** to try several approaches in parallel and keep the one that works.

## 10. Storage that outlives a sandbox

- Caches shared across sandboxes, such as package downloads and build caches
- Named volumes that several sandboxes can attach
- Saved CI outputs: build results and logs
- Templates updated with installed tools

All of these are owner-only by default.

## 11. Network

All network setup lives on the host, and the sandbox can't change it ([ADR 0002](../adr/0002-network-setup-lives-on-the-host.md)). It's configured through the API, MCP or CLI, and network types are plugins. Each sandbox can have:
1. Outbound traffic through any VPN, proxy or service, with its own exit IP. For example, sandbox A through a company VPN, sandbox B through a proxy in Germany, sandbox C direct.
2. Membership in a private network, such as its own Tailscale device.
3. Inbound URLs for dev servers running inside it.
4. Allow and deny lists for which sites it can reach.
5. Access to other sandboxes, only when allowed.

Defaults:
- Outbound traffic is open internet, but the host's LAN, other private networks and cloud metadata addresses are blocked.
- Inbound dev-server URLs are private by default: only the owner, signed in, can open them. The owner can share a link that expires.
- Public-repo CI jobs get an allowlist only.

## 12. Modularity

- **Everything is customizable and modular.**
- **Anyone can write plugins.** They run as separate programs, so a bad plugin can't crash Lamp or reach into sandboxes.
- **Engines are the exception.** An engine runs sandboxes, so it handles their memory. Engines are built into Lamp and audited. A third-party engine plugin needs the owner to mark it trusted, with a clear warning.
- **Plugin types include:** engines, network types, credential sources and password managers, harnesses, kits, CI systems, triggers, consoles, and caller access methods.

## 13. Harnesses, sign-in and settings

- **Any harness:** Claude Code, Codex, OpenCode, Cursor CLI, Copilot CLI, Amp, Goose, Gemini CLI, Pi, Devin, Droid, and others.
- **Sandboxes start with the harness already signed in:**
  - Subscriptions use each vendor's approved route. The login is stored only on the user's hosts and placed only into that user's sandboxes. Network rules limit it to the vendor's servers. Forks and suspends of the user's own sandboxes keep the login, because it never leaves their hosts or their sandboxes. Lamp strips it from templates, from snapshots that leave the user's hosts, and from any sandbox shared with or handed to another person. Those start the harness fresh. Lamp-run servers never hold model credentials ([ADR 0004](../adr/0004-subscription-logins-stay-on-user-hosts.md)).
  - Each harness has a list of the sign-in methods its vendor allows.
  - API keys are swapped in by a host proxy and never enter the sandbox.
- **Settings that follow you into every sandbox,** copied one way from a source the user chooses:
  - harness settings
  - skills, plugins and slash commands
  - instruction files like `CLAUDE.md` and `AGENTS.md`
  - the MCP list
  - git identity and signing

  Changes made inside a sandbox come back only if the user approves them.

## 14. Connections

There's no separate feature for moving stateful apps into sandboxes. Agents get three kinds of connection:
1. **MCP servers,** already signed in, through a gateway on the host, so tokens never enter the sandbox. The exception is MCP servers that must run inside the sandbox, which get limited-access tokens.
2. **CLI tools with their API tokens,** such as `gh`, `stripe` and `aws`. The tokens go through the host proxy and never enter the sandbox.
3. **Services on the owner's own machines** that an agent running locally could reach, such as a laptop database, a dev server or a home-network service. Each one gets an access level: off, read-only, ask first, or full.

## 15. Kits

- **Logged-in browser.** By default it runs outside the sandbox, on the host or in a separate locked-down sandbox. The agent controls it remotely and never sees the cookies. Running it inside the sandbox with copied logins is opt-in. When a site asks for 2FA or a CAPTCHA, the person takes over the browser from their console and hands it back.
- **Password managers,** as plugins: 1Password, Bitwarden, KeePassXC and Proton Pass.
  - By default, Lamp fills a password on the agent's behalf from outside the sandbox, so the agent never sees it. This works on standard login forms. Where Lamp can't guarantee the password stays hidden from the agent, the person types it during a takeover instead.
  - Injecting a password into the sandbox is opt-in for each secret.
  - Approval for each use, on the person's phone or console, can be switched on for each vault.
- **Toolchains,** Docker-in-sandbox, and anything else, as kits.

## 16. Consoles and sessions

- **The fleet looks like one machine** to consoles such as T3 Code, Herdr, or any other the user sets up. Console support is a plugin.
- **Every session in every sandbox shows up** in the console as its own session. You can open it, watch it live and type into it. Status shows as working, blocked or idle.
- **SSH works everywhere:** `ssh` to the fleet lists sessions, attaches to one live, or opens a shell in its sandbox. This works with any terminal, with no Lamp-specific changes.
- **Sub-agents** run in their own sandbox (a copy of the parent's files, with changes coming back through git) or in the parent's sandbox. Whoever starts them chooses. Sub-agent sessions show up in the console too.
- **Teleport:** move a running session between a laptop and a sandbox.
- **Visibility on team fleets:** you see only your own sessions by default. You can share a session for someone to watch, or to type into as well.

## 17. Access, accounts and keys

- **Callers reach a fleet through:**
  - built-in Tailscale, which works behind NAT with no setup and lets CI connect with no stored secret
  - the Eilmer relay
  - the owner's own domain with automatic HTTPS
  - local-only access

  Cloudflare Tunnel is a plugin.
- **The relay** ([ADR 0009](../adr/0009-relay-never-decrypts-traffic.md)) is open source (AGPL-3.0) and hosted by Eilmer. It signs people in, links hosts and hands out one-time credentials. It never decrypts traffic and holds no model credentials or sandbox data.
- **One Eilmer account, on Clerk,** across every Eilmer product ([ADR 0005](../adr/0005-one-eilmer-account.md)). Anything using the relay requires it. Fleets that don't use the relay can also use local accounts or their own OIDC provider, so they work offline.
- **Sharing modes:** a single owner, a team, or a public service. The owner configures it. Roles: owner, admin, member and viewer.
- **Keys can be limited to:**
  - specific sandboxes, recipes or hosts
  - specific actions: create, run commands, watch, read
  - resource limits and an expiry date
- **GitHub Actions signs in without a stored secret:** Lamp trades GitHub's token for a short-lived Lamp key.

## 18. CI and triggers

- **Every CI system is supported through plugins.** Lamp ships plugins for:
  - GitHub Actions, including the Copilot cloud agent and the Claude Code and Codex actions
  - GitLab
  - Buildkite
  - Jenkins
  - Forgejo/Gitea
  - CircleCI
  - Azure Pipelines
  - Bitbucket
  - Woodpecker

  Anyone can write more.
- **Triggers work the same way:** Slack, Linear, webhooks, schedules, and more.
- **Each job gets a fresh sandbox,** with caches through lasting storage.
- **Jobs from strangers' public-repo pull requests** run only in fresh VMs, with no secrets and limited network access. The only credential such a job may hold is its CI system's own short-lived token for that one job, such as a GitHub runner job token with a read-only repository token.

## 19. Other first-release features

- **Warm pool, fork mid-task and teleport,** covered above.
- **Dashboard:** every sandbox, session and resource, live.
- **Budgets and watchdog:** cap time and tokens per task, and stop agents that are stuck or looping. Token caps are exact for traffic through Lamp's proxy. For subscription traffic, which Lamp doesn't decrypt, they use the usage the harness reports, backed by hard time caps.
- **Notifications:** done, stuck, or needs a decision.
- **Audit log:** every command and every network connection, per sandbox, with destination, bytes and timing. Full request detail is logged for traffic through Lamp's own proxy and gateway. Subscription traffic isn't decrypted ([ADR 0004](../adr/0004-subscription-logins-stay-on-user-hosts.md)). "Every command" means every process started: program, arguments, user, parent process and time. It doesn't mean the meaning of commands inside scripts.
- **Schedules:** run an agent on a timer.
- **Not included:** overflow to cloud providers.
- **Later, not in release one: Windows hosts and guests.** The engine and host plugin boundaries keep it possible.

The first release includes everything in this brief.

## 20. Hosting, domains and business

- **Everything hosted goes under eilmer.si,** with no other domain ([ADR 0006](../adr/0006-everything-under-eilmer-si.md)). The design must isolate cookies and origins within eilmer.si and stay under certificate rate limits.
- **No payments yet,** but leave room for plans, quotas and billing to be added later.
- **Licenses** ([ADR 0008](../adr/0008-licenses.md)): Apache-2.0 for everything that runs on hosts, AGPL-3.0 for the relay. The repo stays private until the first engine demo works.
- **Usage data** sent to Eilmer is off by default and opt-in. It contains only numbers: counts of creates, forks, suspends, resumes and failed operations, and how long creates, forks and resumes took (O7). It never includes versions, commands, files, prompts or names, and it's kept for 90 days.

## 21. Proving it

Owner amendment, 2026-11-10: take and publish reproducible measurements once at release, including the section 7 targets and comparisons with Cloudroom, E2B Embed, Docker Sandboxes, microsandbox and SmolVM. Report time-to-agent-ready, fork time, density, reliability and secret protection honestly from the available observations. No benchmark runs on every change or PR, and no measurement is a blocker. Build features with almost no new tests and show one real end-to-end demonstration per ticket.

## 22. Engineering rules and constraints

- **Owner decision, 2026-11-09: “skip windows for everything.”** Windows hosts, native guests, test lanes, CLI builds and CI runner rows are outside release one. O10 now means Linux 5.10+ and macOS 26+ on Apple Silicon.

- **Build order:** the engine comes first. A Linux sandbox that starts and forks has to work before anything else; the 2026-11-10 amendment removes speed as a prerequisite. It doesn't need Docker images.
- **Reuse rule** ([ADR 0003](../adr/0003-reuse-open-source-only-for-security-or-speed.md)): Lamp writes its own components, and reuses open source only when it's needed for security or is very likely the fastest option.
- **Language:** the architecture decides. The trade-off: Firecracker, Cloud Hypervisor and libkrun are Rust, while Tailscale's embeddable library (`tsnet`) and GitHub's runner scale-set client are Go.
- **Test machines:**
  - an Apple M5 Mac with 10 cores and 24 GB, on macOS 26
  - an Ubuntu laptop
  - a MacBook Air
- **No money is spent on testing.** Every test, benchmark and dev relay runs on the owner's three machines. This replaces the earlier plan to rent machines (owner decision, 2026-11-08). Vendor tests use the owner's existing paid accounts and subscriptions, which cover everything the tests need. Nothing new is bought, and pay-per-use API keys run under a hard spend limit (owner decision, 2026-11-09).

  Both Macs run macOS 26, and a macOS guest can't run Lamp's engine inside it. So **macOS 26 is the certified minimum** for the first release, replacing macOS 14 (owner said "you decide", 2026-11-08). Older macOS versions are uncertified.
- **Never test against the owner's own projects,** such as Ozi, Apple Bridge or money-bridge, and don't build special integrations for them. Use public third-party harnesses, consoles and MCP servers.

## 23. Research already done

Read these before designing. Each cites its sources.
- [modal-daytona.md](../research/modal-daytona.md): how Modal, Daytona and E2B work
- [remote-access-and-ci.md](../research/remote-access-and-ci.md): Tailscale, Cloudflare, relays, caller auth, GitHub runners
- [vm-backends.md](../research/vm-backends.md): VM engines on each host OS, forking, devices, getting images ready to boot
- [ci-and-integrations.md](../research/ci-and-integrations.md): every CI system, and 52 integration scenarios
- [credentials-and-stateful-apps.md](../research/credentials-and-stateful-apps.md): harness sign-in, MCP auth, credential proxies, browsers, password managers
- [subscription-auth.md](../research/subscription-auth.md): how other products handle subscriptions, and vendor enforcement
