<img src="assets/icon.svg" width="64" height="64" alt="">

# Lamp

Lamp runs coding agents, harnesses and CI jobs in fast, isolated sandboxes on
machines you own. Each sandbox is its own virtual machine: Firecracker on Linux,
Virtualization.framework on Apple Silicon Macs. Agents get to use your model
keys, logins and tools through a broker without being handed the secrets.

Created in [T3 Code](https://t3.codes).

## Install and run code

```sh
curl -fsSL https://lamp.eilmer.si/install.sh | sh
"$HOME/.local/lamp/current/bin/lamp" sandbox run -- python3 -c 'print("hello from Lamp")'
```

The next release's installer sets up the engine, private owner credential and
an included Linux template. `run` creates a VM, runs the command, then deletes
it. **The published 0.1.1 installer predates this flow**; the coordinator must
publish a release containing the new installer before these two lines work
from the public URL. See [installation](docs/install.md) for prerequisites,
PATH, persistent sandboxes, locations and a second install. Linux needs KVM;
Apple Silicon needs macOS 26+ and administrator access. No password prompts
are opened. Signed local candidates have demonstrated this path on Ubuntu
amd64, the M5, the 8 GB M2 Air with everyday apps open, and Linux arm64.
Mac default memory adapts to available host
memory; `lamp template list` shows the selected resources. Mac networking
requires separate validation. See the [implementation evidence](docs/development/evidence/t32/README.md)
for observed results and limits.

The default install enrolls the local sandbox engine and control API. Sessions,
model custody, fleets, CI adapters and relay require their own enrollment; the
presence of a binary or schema operation does not configure those features.
The owner has deferred SDK work. Use the CLI, MCP or HTTP API.

## Build from source

```sh
git clone https://gitlab.com/johansardar/lamp.git && cd lamp
make build              # Go, Rust and the plugin launcher
```

You need Go, Rust (see `rust-toolchain.toml`), a C compiler and Python 3.

## Documentation

- [Linux guest tools, networking and device support](docs/guest-linux.md)

- [Release notes](docs/release/0.1.1.md) and [release measurements](docs/release/measurements.md)
- [Specification](docs/spec/brief.md), [requirements](docs/spec/requirements.md) and [architecture](docs/spec/architecture.md)
- [Public API schema](contracts/public/schema.json) and [OpenAPI](contracts/public/openapi.json)
- [Development evidence](docs/development/README.md): what each feature was shown doing, failures included
- [Glossary](GLOSSARY.md)

## Licence

Lamp's host code, CLI, SDKs and first-party plugins are licensed under
[Apache-2.0](LICENSE). The optional relay server (`internal/relay`,
`cmd/lamp-relay`) is [AGPL-3.0-only](internal/relay/LICENSE). Dependencies keep
their own licences; see [THIRD_PARTY.md](THIRD_PARTY.md).

Hosts, rolling updates and remote access: [release setup guide](docs/release/fleets-and-access.md).

Control setup: [CI enrollment, keys, session shares, approvals and notifications](docs/control-operations.md).
Sandbox tools are available as built-in [kits](docs/guides/kits.md): headless Chromium, a virtual desktop with recording, guest Docker, toolchains, persistent Python and a TypeScript LSP.
Templates and storage: [build templates, save tools and mount persistent volumes](docs/templates-and-storage.md).
