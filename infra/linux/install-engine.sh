#!/usr/bin/env bash
# Installs the Lamp Linux engine on a host with KVM (no Docker needed).
#   infra/linux/install-engine.sh [STORE_GIB] [ALLOWED_UID...]
# Downloads Firecracker/jailer v1.17.0 (checksum-verified), creates the XFS
# reflink store, installs lamp-supervisor and lamp-guard, writes the default
# sandbox network policy (/etc/lamp/network.json) and starts the supervisor.
# Every sandbox gets a guard network: lamp-guard (NFQUEUE), the mediated
# proxy, and pasta (passt) as its userspace uplink, so the host needs nft,
# nsenter/setpriv (util-linux) and pasta; nothing on the host forwards.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
STORE_GIB=${1:-300}; shift || true
ALLOW=""
for uid in "$@"; do ALLOW+=" --allow-uid $uid"; done
FC_VERSION=v1.17.0
ARCH=$(uname -m)
case "$ARCH" in
  x86_64) FC_SHA256=06094a1108ae9e82aa4c23a775aa92758f53f1175d422270d9d6162cb9ade558 ;;
  aarch64) FC_SHA256=e351ebe4f7a16b5873bbd51005d2e6767103cff4d5ebc829df2d3f95a93e2256 ;;
  *) echo "unsupported architecture $ARCH"; exit 1 ;;
esac
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

test -w /dev/kvm || { echo "need read/write /dev/kvm (hosts without virtualization: infra/linux/install-gvisor.sh)"; exit 1; }
for tool in nft nsenter setpriv fallocate pasta; do
  command -v "$tool" >/dev/null || { echo "need $tool on the host (pasta: package passt)"; exit 1; }
done
cd "$WORK"
curl -fsSLO "https://github.com/firecracker-microvm/firecracker/releases/download/$FC_VERSION/firecracker-$FC_VERSION-$ARCH.tgz"
echo "$FC_SHA256  firecracker-$FC_VERSION-$ARCH.tgz" | sha256sum -c -
tar xzf "firecracker-$FC_VERSION-$ARCH.tgz"
R="release-$FC_VERSION-$ARCH"
sudo -n install -d /usr/local/lib/lamp
sudo -n install -m 0755 "$R/firecracker-$FC_VERSION-$ARCH" /usr/local/lib/lamp/firecracker
sudo -n install -m 0755 "$R/jailer-$FC_VERSION-$ARCH" /usr/local/lib/lamp/jailer

(cd "$ROOT" && cargo build --release --locked -p lamp-supervisor -p lamp-guard)
sudo -n install -m 0755 "${CARGO_TARGET_DIR:-$ROOT/target}/release/lamp-supervisor" /usr/local/lib/lamp/lamp-supervisor
sudo -n install -m 0755 "${CARGO_TARGET_DIR:-$ROOT/target}/release/lamp-guard" /usr/local/lib/lamp/lamp-guard
# Default policy: public destinations only, no exceptions. Exceptions are
# exact address/transport/port triples and part of the policy revision.
if [ ! -f /etc/lamp/network.json ]; then
  sudo -n install -d -m 0755 /etc/lamp
  printf '{"guard":"/usr/local/lib/lamp/lamp-guard","pasta":"%s","audit_dir":"/var/lib/lamp-audit","revision":1,"exceptions":[]}\n' \
    "$(command -v pasta)" | sudo -n tee /etc/lamp/network.json >/dev/null
fi
sudo -n install -d -m 0700 /var/lib/lamp-audit
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
