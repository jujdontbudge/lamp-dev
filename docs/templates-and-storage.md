# Templates and persistent storage

`lamp template list --json` discovers verified templates from the connected
engine as well as templates created through the API. `template get ID --json`
shows the guest OS, architecture, minimum/default resources and build state.
The `seed_ref` label on an engine template identifies its enrolled base seed.
Linux templates on Apple Silicon restore prepared state at its original shape
and cold-boot when CPU, memory or disk requests differ. Omit resource flags to
use template or recipe defaults.

## Build a template

Save this as `tools.lamp`, replacing the seed pin with an enrolled seed's reference:

```toml
format = "lamp.template/v1"
name = "tools"
guest_os = "linux"
arch = "x86_64" # arm64 on Apple Silicon
seed_ref = "lamp-seed/ubuntu-26.04@sha256:REPLACE_WITH_THE_ENROLLED_SEED_DIGEST"
network = "none"
vcpus = 2
memory_mib = 1024
disk_gib = 20

[[steps]]
argv = ["sh", "-c", "mkdir -p /opt/tools; printf '#!/bin/sh\\necho template-tool\\n' > /opt/tools/hello; chmod +x /opt/tools/hello"]
timeout_s = 60
```

```sh
lamp template build ./tools.lamp --json
lamp template list --json
lamp sandbox create --template-id tp_RETURNED_ID --json
```

The file shortcut uploads a blob, creates a template and starts its build through
the public API. Completed stages and their output appear on stderr while the
build runs; stdout contains the ready template. `template get` retains the
bounded build log in `labels.build_log`, including the failing step if a build
fails. Builds run in disposable confined VMs. The current format supports offline
builds (`network="none"`); provision package archives in the enrolled seed for
package installation. It does not silently grant build-network access.
Dockerfiles and OCI references are rejected with `INVALID_ARGUMENT`; Docker is
available only through the Docker kit inside a sandbox.

HTTP and MCP clients use the same operations: upload/finalize a definition blob,
`template_create {name, definition_blob_id}`, then
`template_build {template_id, definition_blob_id, expected_revision}`. HTTP puts
the idempotency key in `Idempotency-Key` and the revision in `If-Match`.

The seed is installed by the engine owner, outside the public build interface.
Linux uses the verified `seed-import` store. Apple Silicon uses the arm64 seed
in `STORE/seeds/DIGEST`, built with `images/linux-seed/build-seed-arm64.sh`.

## Save selected tools

```sh
lamp sandbox save-template --sandbox-id sb_SOURCE --name saved-tools \
  --selected-paths-json '["/opt/tools"]' --expected-revision REV --json
lamp sandbox create --template-id tp_RETURNED_ID --json
```

Lamp retains a durable archive of the selected paths and restores it onto a
fresh base template. It retains no source RAM or login session, and the archive
can be used repeatedly, including after daemon restart. Attached storage cannot
be selected. Tainted sources require verified reconstruction inputs; missing
clean inputs return `CLEAN_REBUILD_REQUIRED`.

## Mount a volume or cache

All three interfaces create and attach the same resources:

```sh
lamp volume create --name shared --mode shared_files --quota-bytes 104857600 --json
lamp attachment create --sandbox-id sb_A --resource-kind volume \
  --resource-id vo_RETURNED_ID --mount-path /mnt/shared --mode read_write --json
lamp attachment create --sandbox-id sb_B --resource-kind volume \
  --resource-id vo_RETURNED_ID --mount-path /mnt/shared --mode read_write --json
```

A file written under `/mnt/shared` in A is immediately readable in B. Writes
are flushed to the daemon's durable storage directory. Deleting a sandbox
detaches its storage; a subsequent sandbox can attach and read the same files.
Deleting an attached volume returns `RESOURCE_IN_USE` and identifies its
attachment. `cache create` plus `attachment create --resource-kind cache` works
the same way; cache writers must match the cache's trust class. Cache publish
continues to retain content-addressed generations for backup and eviction.

The HTTP bodies are ordinary JSON, for example:

```http
POST /v1/volumes
Idempotency-Key: volume-example
Content-Type: application/json

{"name":"shared","mode":"shared_files","quota_bytes":104857600}
```

`POST /v1/caches` accepts `{"name":"packages","quota_bytes":104857600}`.
MCP uses `volume_create`, `cache_create` and `attachment_create` with those same
fields. Storage quotas include a small per-entry metadata charge. Exhaustion
returns `ENOSPC` inside the guest. Read-only mounts are enforced by both the guest
kernel and the storage server. `exclusive_block` retains its single-writer rule;
attachments expose files, not an unguarded host block device.

New Linux seeds include `CONFIG_NET_9P`, `CONFIG_NET_9P_FD`, and `CONFIG_9P_FS`.
Older seeds without that transport fail attachment with `ENGINE_UNQUALIFIED`;
Lamp never advertises an unmounted directory as ready. The transport serves Linux
guests on Firecracker and VZ; native macOS guests do not implement the Linux 9p
mount protocol. Storage channels reconnect after daemon restart and VM resume;
forks detach inherited mounts according to their attachment policy.

## Resources and pools

Explicit CPU and memory requests are checked against host maxima before create.
Linux root disks grow together with their ext4 filesystem; disks below the
template minimum are refused. Saved templates retain their source shape. Linux guests on Mac also grow whole-device ext4 roots before create completes. Warm-pool entries count
as live sandboxes; the existing lifecycle monitor replenishes claimed entries
up to `target_size`, subject to ordinary admission limits.
