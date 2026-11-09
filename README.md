<img src="assets/icon.svg" width="64" height="64" alt="">

# Lamp

Created in [T3 Code](https://t3.codes).

Lamp builds fast, isolated sandboxes for agents, harnesses and CI. This checkout
currently contains the agreed spec and the T1 workspace/contracts skeleton.

```sh
make build
make test
make bootstrap verify
```

See [development and ownership](docs/development/README.md), the
[public schema](contracts/public/schema.json), [generated OpenAPI](contracts/public/openapi.json),
and [lamp.worker/1](contracts/worker/README.md). The engine and control service
arrive in subsequent [tickets](docs/tickets/README.md). Template and saved-state
formats remain unfrozen until T14.
