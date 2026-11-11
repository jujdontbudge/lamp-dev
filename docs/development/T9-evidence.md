# T9: Subscription custody evidence and how-to

These are optional development commands, not prerequisites for implementation,
merge or release. Historical run artifacts, including failures, remain under
[`evidence/t9/`](evidence/t9/).

## Custody graph checks

```sh
cargo test -p lamp-engine custody_tests --locked
```

This exercises the metadata-only custody graph and conservative official-run
refresh coordination. Product callers authenticate the owner, persist custody
before credential delivery, enforce vendor-only egress for the login sandbox
and quiesce the official harness before releasing its run lease. A stale copied
refresh lineage requires fresh vendor sign-in; a lock alone cannot make a copied
rotating refresh token current.

## Clean reconstruction

```sh
lamp-supervisor template-rebuild FILE --approved-nonsecret-digest SHA256
```

This builds from the digest-approved definition and its pinned seed, never from
a source sandbox or capture. The digest records the authenticated caller's
approval of nonsecret inputs; it is not automatic secret detection. Approve the
seed and recipes before sign-in. Custom tools need separately verified nonsecret
recipes.

## Optional LX reconstruction probe

Run only when the diagnostic is needed, on disposable LX storage as root, and
follow the owner-machine limits in `AGENTS.md`.

```sh
cargo build -p lamp-engine --example custody_rebuild --locked
python3 -m benchmarks.custody_probe \
  --fixture target/debug/examples/custody_rebuild \
  --seed /absolute/path/to/verified/seed \
  --metadata /absolute/path/to/observations.json \
  --output /new/disposable/run
```

The fixture uses isolated store, memory and network-namespace paths and synthetic
canaries, not real credentials or owner services. Its independent scanner covers
the emitted template files. Keep all run directories, including failures.

The retained probe uses engine APIs rather than the public subscription/session
boundary. It exits nonzero even when its clean-builder control passes; inspect
the `clean_builder_control` result in `summary.json`. It is not an end-to-end
subscription sign-in demonstration.
