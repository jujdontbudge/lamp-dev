#!/usr/bin/env bash
# Builds the Lamp Linux seed: a verified Debian 13 root, the Lamp guest kernel
# and lamp-init, packed as an ext4 boot disk. No Docker/OCI image is used.
#
# Usage (on a Linux x86_64 host, as a user with passwordless sudo):
#   images/linux-seed/build-seed.sh OUT_DIR
# Inputs are pinned below. The result is OUT_DIR/{vmlinux,rootfs.ext4,seed.json}.
set -euo pipefail

OUT=${1:?usage: build-seed.sh OUT_DIR}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=${LAMP_SEED_WORK:-$HOME/.cache/lamp-seed}
KERNEL_VERSION=6.18.62
KERNEL_SHA256=490a7aedf1b355c565d3a6c05333ccccb5da82064eca46b590448a7099d7d651
# Firecracker's own guest config for this kernel line, at the pinned VMM tag.
KCONFIG_URL=https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.17.0/resources/guest_configs/microvm-kernel-ci-x86_64-6.18.config
KCONFIG_SHA256=${KCONFIG_SHA256:-}
SUITE=trixie
MIRROR=${LAMP_SEED_MIRROR:-http://deb.debian.org/debian}
PACKAGES=python3,git,ca-certificates,procps,iproute2,coreutils
DISK_GIB=20

mkdir -p "$WORK" "$OUT"
cd "$WORK"

# 1. Guest kernel, built from verified kernel.org source and the pinned config.
if [ ! -f "vmlinux-$KERNEL_VERSION" ]; then
  [ -f "linux-$KERNEL_VERSION.tar.xz" ] || curl -fsSLO "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$KERNEL_VERSION.tar.xz"
  echo "$KERNEL_SHA256  linux-$KERNEL_VERSION.tar.xz" | sha256sum -c -
  curl -fsSLo kernel.config "$KCONFIG_URL"
  if [ -n "$KCONFIG_SHA256" ]; then echo "$KCONFIG_SHA256  kernel.config" | sha256sum -c -; fi
  rm -rf "linux-$KERNEL_VERSION" && tar xf "linux-$KERNEL_VERSION.tar.xz"
  cp kernel.config "linux-$KERNEL_VERSION/.config"
  make -C "linux-$KERNEL_VERSION" olddefconfig >/dev/null
  make -C "linux-$KERNEL_VERSION" -j"$(nproc)" vmlinux >kernel-build.log 2>&1
  cp "linux-$KERNEL_VERSION/vmlinux" "vmlinux-$KERNEL_VERSION"
fi

# 2. Guest init and helper, static musl builds from this checkout.
(cd "$ROOT" && cargo build --release --locked --target x86_64-unknown-linux-musl -p lamp-init)
BIN="${CARGO_TARGET_DIR:-$ROOT/target}/x86_64-unknown-linux-musl/release"

# 3. Debian root; mmdebstrap verifies the archive signatures with the
#    distribution keyring.
if [ ! -f "root-$SUITE.tar" ]; then
  sudo -n mmdebstrap --variant=minbase --include="$PACKAGES" --format=tar "$SUITE" "root-$SUITE.tar" "$MIRROR"
fi
sudo -n rm -rf rootdir && mkdir rootdir
sudo -n tar -C rootdir -xf "root-$SUITE.tar"
sudo -n install -m 0755 "$BIN/lamp-init" rootdir/sbin/lamp-init
sudo -n install -m 0755 "$BIN/lamp-guest-helper" rootdir/sbin/lamp-guest-helper
echo lamp | sudo -n tee rootdir/etc/hostname >/dev/null
printf '127.0.0.1 localhost lamp\n::1 localhost\n' | sudo -n tee rootdir/etc/hosts >/dev/null
sudo -n rm -f rootdir/etc/machine-id && sudo -n touch rootdir/etc/machine-id
sudo -n chroot rootdir dpkg-query -W -f '${Package} ${Version}\n' >packages.txt

# 4. Boot disk, populated without mounting it on the host.
rm -f rootfs.ext4
truncate -s "${DISK_GIB}G" rootfs.ext4
sudo -n mkfs.ext4 -q -F -L lamproot -U clear -E root_owner=0:0,lazy_itable_init=1,lazy_journal_init=1 -d rootdir rootfs.ext4
sudo -n chown "$(id -u):$(id -g)" rootfs.ext4
sudo -n rm -rf rootdir

cp --sparse=always rootfs.ext4 "$OUT/rootfs.ext4"
cp "vmlinux-$KERNEL_VERSION" "$OUT/vmlinux"
cp packages.txt "$OUT/packages.txt"
python3 - "$OUT" "$KERNEL_VERSION" "$KERNEL_SHA256" "$SUITE" "$BIN/lamp-init" "$BIN/lamp-guest-helper" <<'PY'
import hashlib, json, os, sys
out, kver, ksha, suite, init, helper = sys.argv[1:]
def digest(p):
    h = hashlib.sha256()
    with open(p, 'rb') as f:
        while chunk := f.read(1 << 22):
            h.update(chunk)
    return h.hexdigest()
obj = lambda p: {"sha256": digest(p), "size": os.path.getsize(p)}
seed = {
    "format": "lamp.seed/v0",
    "name": "debian-13",
    "guest_os": "linux",
    "arch": "x86_64",
    "kernel": {**obj(os.path.join(out, "vmlinux")), "source": f"linux-{kver}.tar.xz", "source_sha256": ksha,
               "cmdline": "console=ttyS0 reboot=k panic=1 pci=off i8042.noaux i8042.nomux i8042.dumbkbd root=/dev/vda rw init=/sbin/lamp-init quiet"},
    "rootfs": {**obj(os.path.join(out, "rootfs.ext4")), "filesystem": "ext4", "distribution": f"debian {suite}"},
    "init": {"lamp-init": digest(init), "lamp-guest-helper": digest(helper)},
    "packages": {"sha256": digest(os.path.join(out, "packages.txt"))},
}
with open(os.path.join(out, "seed.json"), "w") as f:
    f.write(json.dumps(seed, sort_keys=True, separators=(",", ":")))
print("seed digest sha256:" + hashlib.sha256(open(os.path.join(out, "seed.json"), "rb").read()).hexdigest())
PY
