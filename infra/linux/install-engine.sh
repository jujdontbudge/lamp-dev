#!/usr/bin/env bash
# Installs the Lamp Linux engine on a host with KVM (no Docker needed).
#   infra/linux/install-engine.sh [STORE_GIB] [ALLOWED_UID...]
# Downloads Firecracker/jailer v1.17.0 (checksum-verified), creates the XFS
# reflink store, installs lamp-supervisor and starts it.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
STORE_GIB=${1:-300}; shift || true
ALLOW=""
for uid in "$@"; do ALLOW+=" --allow-uid $uid"; done
FC_VERSION=v1.17.0
FC_SHA256=06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

test -w /dev/kvm || { echo "need read/write /dev/kvm"; exit 1; }
cd "$WORK"
curl -fsSLO "https://github.com/firecracker-microvm/firecracker/releases/download/$FC_VERSION/firecracker-$FC_VERSION-x86_64.tgz"
echo "$FC_SHA256  firecracker-$FC_VERSION-x86_64.tgz" | sha256sum -c -
tar xzf "firecracker-$FC_VERSION-x86_64.tgz"
R="release-$FC_VERSION-x86_64"
sudo -n install -d /usr/local/lib/lamp
sudo -n install -m 0755 "$R/firecracker-$FC_VERSION-x86_64" /usr/local/lib/lamp/firecracker
sudo -n install -m 0755 "$R/jailer-$FC_VERSION-x86_64" /usr/local/lib/lamp/jailer

(cd "$ROOT" && cargo build --release --locked -p lamp-supervisor)
sudo -n install -m 0755 "${CARGO_TARGET_DIR:-$ROOT/target}/release/lamp-supervisor" /usr/local/lib/lamp/lamp-supervisor
sudo -n rm -f /usr/local/lib/lamp/lamp-pager

if ! mountpoint -q /var/lib/lamp; then
  sudo -n install -d /var/lib/lamp
  if [ ! -f /var/lib/lamp-store.img ]; then
    sudo -n truncate -s "${STORE_GIB}G" /var/lib/lamp-store.img
    sudo -n mkfs.xfs -q -m reflink=1 -L lampstore /var/lib/lamp-store.img
  fi
fi
sudo -n install -m 0644 "$ROOT/infra/linux/var-lib-lamp.mount" /etc/systemd/system/var-lib-lamp.mount
sed "s#@ALLOW_UIDS@#$ALLOW#" "$ROOT/infra/linux/lamp-supervisor.service" | sudo -n tee /etc/systemd/system/lamp-supervisor.service >/dev/null
sudo -n systemctl daemon-reload
mountpoint -q /var/lib/lamp || sudo -n systemctl enable --now var-lib-lamp.mount
sudo -n systemctl enable var-lib-lamp.mount
sudo -n systemctl enable --now lamp-supervisor.service
sudo -n systemctl restart lamp-supervisor.service
echo "lamp-supervisor running; socket /run/lamp/worker.sock"
