<img src="assets/icon.svg" width="64" height="64" alt="">

# Lamp

Lamp runs Linux sandboxes on machines you own: Firecracker on Linux with KVM,
and Virtualization.framework on Apple Silicon Macs. This is a developer preview.
The 0.1.2 release adds an installed engine and default Linux template to the
control-only 0.1.1 installer.

Created in [T3 Code](https://t3.codes).

## Install and run code

On Linux amd64/arm64 with KVM, or Apple Silicon macOS 26+ with noninteractive
administrator access:

```sh
curl -fsSL https://lamp.eilmer.si/install.sh | sh
"$HOME/.local/lamp/current/bin/lamp" sandbox run -- python3 -c 'print("hello from Lamp")'
```

The command creates a VM, runs Python and deletes the VM. The installer prints
its binary directory, PATH instruction and private dashboard sign-in file.
See [installation and engine setup](docs/install.md#engine-setup) for host
packages, memory/disk requirements, credentials and a persistent sandbox;
[update, rollback and uninstall](docs/install.md#update-rollback-and-remove-an-install)
use the same prefix. macOS memory defaults adapt to available memory.

## What you can use

The CLI, MCP and HTTP API are the supported interfaces. Installed discovery
(`lamp --help --json`, MCP `tools/list`, served OpenAPI) describes this daemon's
handlers; enrollment and host capability can still be required.

- [Guest tools and devices](docs/guest-linux.md): Python, Node, git and compiler
  tools; Linux Firecracker has no display, audio or GPU devices.
- [Sleep, snapshots, budgets and encrypted recovery](docs/lifecycle-and-recovery.md):
  `lamp snapshot list`, `lamp budget list`, `lamp backup list`.
- [Templates and storage](docs/templates-and-storage.md): `lamp template list`,
  template builds, saved tools, persistent volumes and caches.
- [Networking](docs/networking.md): `lamp port expose` for loopback previews,
  private share links and live transport policy. Public preview access does not
  expose a host listener to the Internet.
- [Agents and model custody](docs/agents.md): `lamp harness list`; managed
  OpenCode and explicitly enrolled Codex. Codex subscription import keeps the
  upstream sign-in on the host. Claude subscription custody is unsupported.
- [Agent workflow](docs/agent-workflow.md): children, returned git changes,
  side chats, host/VM moves and T3 Code/Herdr bridges need the documented session
  enrollment. Host execution is off by default.
- [Sandbox kits](docs/guides/kits.md): `lamp kit list`; Python, Chromium,
  guest Docker, TypeScript LSP and a virtual desktop. On Linux arm64 hosts,
  Docker, LSP and desktop are unverified.
- [Dashboard](docs/dashboard.md): `lamp dashboard`; local sign-in, sandbox
  metrics and bounded recent logs.
- [Control operations](docs/control-operations.md) and
  [fleets and remote access](docs/release/fleets-and-access.md) require private
  owner enrollment. CI is not configured by installation. A native Forgejo
  fresh-VM job was demonstrated; the adapter catalogue is not a claim that
  every CI service was demonstrated.

The owner deferred SDK work. The generated TypeScript package is unchanged in
scope and is not a supported 0.1.2 workflow; there is no Python SDK or npm release.
Eleven ready-to-run agents, automatic gVisor fallback, hosted self-service
sign-ups and automatic recovery of private VM state onto a surviving host are
not offered by this release. See all [release limits](docs/release/0.1.2.md).

## Measured results

Ubuntu amd64 install through code and cleanup took **165.473 s** on a T32 signed
development candidate using a local HTTP origin, exceeding the two-minute goal.
This is not a public-download timing. Mac and nested Linux arm64 observations,
and the older 0.1.0 create/fork/native-speed results with their platform names,
are in [measurements](docs/release/measurements.md). Public 0.1.2 fresh installs
and Python VM execution passed on M5, M2 Air and nested Linux arm64; Ubuntu was
skipped below the owner's memory floor. The same page records their one-time
create, live-fork and native-speed observations, including misses and an Air
native compile failure. No complete zero-leak measurement is claimed.

## Build from source

```sh
git clone https://github.com/jujdontbudge/lamp.git && cd lamp
make build
```

You need Go, Rust (see `rust-toolchain.toml`), a C compiler and Python 3.
The [public schema](contracts/public/schema.json) is the complete vocabulary;
use the installed daemon's OpenAPI for its available operations.

## Licence

Host code, CLI, generated SDKs and first-party plugins are
[Apache-2.0](LICENSE). The optional relay server (`internal/relay`,
`cmd/lamp-relay`) is [AGPL-3.0-only](internal/relay/LICENSE).
See [THIRD_PARTY.md](THIRD_PARTY.md) for dependency licences.
