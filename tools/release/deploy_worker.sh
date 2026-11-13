#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
# Read-only plan by default; pass --apply only after publishing release assets.
set -eu
here=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
exec python3 "$here/deploy_worker.py" "$@"
