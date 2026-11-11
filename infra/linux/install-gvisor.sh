#!/usr/bin/env bash
# Installs Lamp's weaker Linux fallback for hosts without usable
# virtualization: gVisor's standalone runsc (checksum-verified) and a
# read-only public Alpine task root. It registers nothing with Docker or
# containerd and changes no daemon configuration.
#   infra/linux/install-gvisor.sh [ROOTFS_DIR]
# Then enroll it in lampd's --native-fallback-file:
#   {"runtime":"gvisor","launcher":"<root-run lamp-supervisor wrapper>",
#    "rootfs":"<ROOTFS_DIR>","jobs":{...}}
# Every result is labeled boundary=gvisor_sandbox, isolation=weaker_than_vm.
# Logins, protected connections and public-repository CI are always refused.
set -euo pipefail
ROOTFS=${1:-/var/lib/lamp/gvisor-rootfs}
GV=release-20261026.0
ALPINE=3.20.3
case "$(uname -m)" in
  x86_64) GV_SHA=096589d2d6078ba4caa26aa0200932f571aa5da9d08a5156b98ab1d93fa54468 GA=x86_64 ;;
  aarch64) GV_SHA=6b03c9d53f46b5ac56fd32522f38f108645e9f80a26a335e86ad1e09dcdaefda GA=aarch64 ;;
  *) echo "unsupported architecture $(uname -m)"; exit 1 ;;
esac
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
cd "$WORK"
curl -fsSLO "https://github.com/google/gvisor/releases/download/$GV/gvisor-$GA.tar.bz2"
echo "$GV_SHA  gvisor-$GA.tar.bz2" | sha256sum -c -
if command -v bzip2 >/dev/null; then
  tar xjf "gvisor-$GA.tar.bz2"
else
  # Minimal cloud images lack bzip2; Python's standard library has it.
  python3 -c 'import bz2, shutil, sys; shutil.copyfileobj(bz2.open(sys.argv[1]), sys.stdout.buffer)' "gvisor-$GA.tar.bz2" | tar x
fi
sudo -n install -d -m 0755 /usr/local/lib/lamp/gvisor-bin
sudo -n install -m 0755 runsc /usr/local/lib/lamp/runsc
for f in gvisor-bin/*; do sudo -n install -m 0755 "$f" "/usr/local/lib/lamp/$f"; done
A="https://dl-cdn.alpinelinux.org/alpine/v${ALPINE%.*}/releases/$GA/alpine-minirootfs-$ALPINE-$GA.tar.gz"
curl -fsSLO "$A"; curl -fsSLO "$A.sha256"
sha256sum -c "alpine-minirootfs-$ALPINE-$GA.tar.gz.sha256"
sudo -n rm -rf "$ROOTFS"
sudo -n install -d -m 0755 "$ROOTFS"
sudo -n tar xzf "alpine-minirootfs-$ALPINE-$GA.tar.gz" -C "$ROOTFS"
/usr/local/lib/lamp/runsc --version | head -1
echo "gVisor fallback installed; task root $ROOTFS"
