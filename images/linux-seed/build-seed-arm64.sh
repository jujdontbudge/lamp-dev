#!/usr/bin/env bash
# Builds the Lamp Linux arm64 seed for Apple Silicon hosts (Virtualization
# framework): the same pinned kernel source as the x86_64 seed with
# Firecracker's aarch64 guest config plus the PL011/virtio-balloon options,
# an uncompressed arm64 `Image` (what VZLinuxBootLoader boots), lamp-init,
# the guest helper and the protected collector, packed as an ext4 boot disk.
# No Docker/OCI image is used.
#
# Run on an arm64 Linux builder (lane LA, the M5's Lima VM) as a user with
# passwordless sudo, from a copy of this checkout:
#   images/linux-seed/build-seed-arm64.sh OUT_DIR
# The result is OUT_DIR/{Image,rootfs.ext4,packages.txt,seed.json}.
set -euo pipefail

OUT=${1:?usage: build-seed-arm64.sh OUT_DIR}
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
WORK=${LAMP_SEED_WORK:-$HOME/.cache/lamp-seed-arm64}
KERNEL_VERSION=6.18.62
KERNEL_CACHE=$KERNEL_VERSION-vz-devices-v2
KERNEL_SHA256=490a7aedf1b355c565d3a6c05333ccccb5da82064eca46b590448a7099d7d651
KCONFIG_URL=https://raw.githubusercontent.com/firecracker-microvm/firecracker/v1.17.0/resources/guest_configs/microvm-kernel-ci-aarch64-6.18.config
SUITE=resolute
MIRROR=${LAMP_SEED_MIRROR:-http://ports.ubuntu.com/ubuntu-ports}
PACKAGES=python3,git,ca-certificates,procps,iproute2,coreutils,gcc,libc6-dev
DISK_GIB=${LAMP_SEED_DISK_GIB:-20}
export TMPDIR=$WORK/tmp

[ "$(uname -m)" = aarch64 ] || { echo "build on an arm64 Linux builder" >&2; exit 2; }
mkdir -p "$WORK" "$OUT" "$TMPDIR"
cd "$WORK"

# 1. Kernel: verified kernel.org source, Firecracker's pinned aarch64 config
#    (virtio-pci, vsock, ext4, cgroup freezer are already built in).
if [ ! -f "Image-$KERNEL_CACHE" ]; then
  [ -f "linux-$KERNEL_VERSION.tar.xz" ] || curl -fsSLO "https://cdn.kernel.org/pub/linux/kernel/v6.x/linux-$KERNEL_VERSION.tar.xz"
  echo "$KERNEL_SHA256  linux-$KERNEL_VERSION.tar.xz" | sha256sum -c -
  curl -fsSLo kernel.config "$KCONFIG_URL"
  rm -rf "linux-$KERNEL_VERSION" && tar xf "linux-$KERNEL_VERSION.tar.xz"
  cp kernel.config "linux-$KERNEL_VERSION/.config"
  (cd "linux-$KERNEL_VERSION" &&
    ./scripts/config -e SERIAL_AMBA_PL011 -e SERIAL_AMBA_PL011_CONSOLE -e VIRTIO_BALLOON -e CGROUP_FREEZER \
      -e DRM -e DRM_VIRTIO_GPU -e DRM_FBDEV_EMULATION -e FB -e FRAMEBUFFER_CONSOLE \
      -e INPUT -e INPUT_EVDEV -e INPUT_MISC -e INPUT_UINPUT -e HID_SUPPORT -e HID -e HID_GENERIC -e USB -e USB_XHCI_HCD -e USB_XHCI_PCI -e USB_HID &&
    make olddefconfig >/dev/null &&
    make -j"$(nproc)" Image >"$WORK/kernel-build.log" 2>&1)
  cp "linux-$KERNEL_VERSION/arch/arm64/boot/Image" "Image-$KERNEL_CACHE"
  cp "linux-$KERNEL_VERSION/.config" "kernel-$KERNEL_CACHE.config"
fi

# 2. Guest init, helper and collector: static musl builds from this checkout.
(cd "$ROOT" && cargo build --release --target aarch64-unknown-linux-musl -p lamp-init -p lamp-guard)
BIN="${CARGO_TARGET_DIR:-$ROOT/target}/aarch64-unknown-linux-musl/release"

# 3. Ubuntu root; mmdebstrap verifies the archive signatures.
if [ ! -s "root-$SUITE-arm64.tar" ]; then
  sudo -n rm -f "root-$SUITE-arm64.tar.partial"
  sudo -n env TMPDIR="$TMPDIR" mmdebstrap --variant=minbase --keyring=/usr/share/keyrings/ubuntu-archive-keyring.gpg \
    --include="$PACKAGES" --aptopt='Acquire::Retries "5"' --format=tar "$SUITE" "root-$SUITE-arm64.tar.partial" \
    "deb $MIRROR $SUITE main universe" "deb $MIRROR $SUITE-updates main universe" "deb $MIRROR $SUITE-security main universe"
  sudo -n mv "root-$SUITE-arm64.tar.partial" "root-$SUITE-arm64.tar"
fi
sudo -n rm -rf rootdir && mkdir rootdir
sudo -n tar -C rootdir -xf "root-$SUITE-arm64.tar"
sudo -n install -m 0755 "$BIN/lamp-init" rootdir/sbin/lamp-init
sudo -n install -m 0755 "$BIN/lamp-guest-helper" rootdir/sbin/lamp-guest-helper
sudo -n install -m 0755 "$BIN/lamp-guard" rootdir/sbin/lamp-guard
sudo -n chmod 1777 rootdir/var/tmp
echo lamp | sudo -n tee rootdir/etc/hostname >/dev/null
printf '127.0.0.1 localhost lamp\n::1 localhost\n' | sudo -n tee rootdir/etc/hosts >/dev/null
sudo -n rm -f rootdir/etc/machine-id && sudo -n touch rootdir/etc/machine-id
sudo -n chroot rootdir dpkg-query -W -f '${Package} ${Version}\n' >packages.txt

# 4. Boot disk, populated without mounting it on the host.
rm -f rootfs.ext4
truncate -s "${DISK_GIB}G" rootfs.ext4
sudo -n mkfs.ext4 -q -F -L lamproot -U clear -E root_owner=0:0,lazy_itable_init=0,lazy_journal_init=0 -d rootdir rootfs.ext4
sudo -n chown "$(id -u):$(id -g)" rootfs.ext4
sudo -n rm -rf rootdir

cp --sparse=always rootfs.ext4 "$OUT/rootfs.ext4"
cp "Image-$KERNEL_CACHE" "$OUT/Image"
cp "kernel-$KERNEL_CACHE.config" "$OUT/kernel.config"
cp packages.txt "$OUT/packages.txt"
python3 - "$OUT" "$KERNEL_VERSION" "$KERNEL_SHA256" "$BIN/lamp-init" "$BIN/lamp-guest-helper" "$BIN/lamp-guard" <<'PY'
import hashlib, json, os, sys
out, kver, ksha, init, helper, guard = sys.argv[1:]
def sha(p):
    h = hashlib.sha256()
    with open(p, 'rb') as f:
        for b in iter(lambda: f.read(1 << 20), b''):
            h.update(b)
    return h.hexdigest()
obj = lambda p: {"sha256": sha(p), "size": os.path.getsize(p)}
seed = {
    "format": "lamp.seed/v0",
    "name": "ubuntu-26.04-arm64",
    "guest_os": "linux",
    "arch": "arm64",
    "page_size": 4096,
    "kernel": {**obj(os.path.join(out, "Image")), "format": "arm64 Image", "source": f"linux-{kver}.tar.xz",
               "source_sha256": ksha, "config_sha256": sha(os.path.join(out, "kernel.config")),
               "cmdline": "console=hvc0 root=/dev/vda rw init=/sbin/lamp-init quiet"},
    "rootfs": {**obj(os.path.join(out, "rootfs.ext4")), "filesystem": "ext4", "distribution": "ubuntu resolute arm64"},
    "init": {"lamp-init": sha(init), "lamp-guest-helper": sha(helper), "lamp-guard": sha(guard)},
    "packages": obj(os.path.join(out, "packages.txt")),
}
with open(os.path.join(out, "seed.json"), "w") as f:
    f.write(json.dumps(seed, sort_keys=True, separators=(",", ":")))
print("seed digest sha256:" + hashlib.sha256(open(os.path.join(out, "seed.json"), "rb").read()).hexdigest())
PY
