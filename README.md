<img src="assets/icon.svg" width="64" height="64" alt="">

# Lamp

Lamp runs coding agents, harnesses and CI jobs in fast, isolated sandboxes on
machines you own. Each sandbox is its own virtual machine: Firecracker on Linux,
Virtualization.framework on Apple Silicon Macs. Agents get to use your model
keys, logins and tools through a broker without being handed the secrets.

Created in [T3 Code](https://t3.codes).

- **Fast sandboxes.** Create a Linux sandbox in about 330–400 ms (p50), and fork
  a running one, memory included. [Measured results](docs/release/measurements.md),
  including the targets Lamp does not yet meet.
- **Bring your agent.** Eleven coding agents, sessions over native PTYs, stock
  SSH, ACP and MCP, and continuation between your machine and a sandbox.
- **CI in fresh VMs.** GitHub Actions and GitLab runners, plus other CI systems.
- **Fleets.** Join several machines into one fleet; updates roll out one host at
  a time and roll back on failure.
- **Yours.** Everything runs on your own hardware, including the optional relay.

## Install

```sh
curl -fsSL https://github.com/JohanSardar/lamp/releases/download/v0.1.0/install.sh | sh
```

The installer checks the download's digest and the release signature. Then set
up the engine and, if you want it, the TypeScript SDK: see the
[0.1.0 release notes](docs/release/0.1.0.md). Linux needs KVM; macOS needs
version 26 or later on Apple Silicon.

## Build from source

```sh
make build              # Go, Rust and the plugin launcher
make bootstrap verify   # build plus selected smoke tests
```

You need Go, Rust (see `rust-toolchain.toml`), a C compiler and Python 3.

## Documentation

- [Release notes](docs/release/0.1.0.md) and [release measurements](docs/release/measurements.md)
- [Specification](docs/spec/brief.md), [requirements](docs/spec/requirements.md) and [architecture](docs/spec/architecture.md)
- [Public API schema](contracts/public/schema.json) and [OpenAPI](contracts/public/openapi.json)
- [Development evidence](docs/development/README.md): what each feature was shown doing, failures included
- [Glossary](GLOSSARY.md)

## Licence

Lamp's host code, CLI, SDKs and first-party plugins are licensed under
[Apache-2.0](LICENSE). The optional relay server (`internal/relay`,
`cmd/lamp-relay`) is [AGPL-3.0-only](internal/relay/LICENSE). Dependencies keep
their own licences; see [THIRD_PARTY.md](THIRD_PARTY.md).
