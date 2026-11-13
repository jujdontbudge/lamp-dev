#!/usr/bin/env bash
# The release installer now owns prefix-scoped engine setup. No system paths
# are replaced, no engine binaries are downloaded by this helper.
set -euo pipefail
usage() { echo 'usage: install-engine.sh --release DIR --trust FILE --prefix DIR [--store-gib 80] [--service systemd|process]'; }
for arg in "$@"; do case "$arg" in --help|-h) usage; exit 0;; esac; done
args=(); while (($#)); do
 case "$1" in
  --release|--trust|--prefix|--store-gib|--service) (($#>=2)) || { usage >&2; exit 2; }; args+=("$1" "$2"); shift 2;;
  *) echo "unknown option: $1" >&2; usage >&2; exit 2;;
 esac
done
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
exec "$ROOT/bin/lamp-installer" install --engine "${args[@]}"
