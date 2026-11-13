#!/usr/bin/env bash
# Builds the Lamp Linux seed: a verified distribution root, the Lamp guest
# kernel, lamp-init and the protected task collector (lamp-guard), packed as an
# ext4 boot disk. No Docker/OCI image is used.
#
# LAMP_SEED_DIST selects the root: ubuntu-26.04 (default) or debian-13. The
# engine gate (E02) compares guest work with the same work run natively, and
# requires identical interpreter and compiler builds on both sides; on the LX
# lane (Ubuntu 26.04) only the ubuntu-26.04 root matches the host exactly.
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
DIST=${LAMP_SEED_DIST:-ubuntu-26.04}
case "$DIST" in
  ubuntu-26.04)
    SUITE=resolute
    UBUNTU=${LAMP_SEED_MIRROR:-http://archive.ubuntu.com/ubuntu}
    SECURITY=${LAMP_SEED_SECURITY_MIRROR:-http://security.ubuntu.com/ubuntu}
    SOURCES=("deb $UBUNTU $SUITE main universe" "deb $UBUNTU $SUITE-updates main universe" "deb $SECURITY $SUITE-security main universe")
    KEYRING=/usr/share/keyrings/ubuntu-archive-keyring.gpg
    DISTRIBUTION="ubuntu $SUITE"
    ;;
  debian-13)
    SUITE=trixie
    SOURCES=("${LAMP_SEED_MIRROR:-http://deb.debian.org/debian}")
    KEYRING=/usr/share/keyrings/debian-archive-keyring.gpg
    DISTRIBUTION="debian $SUITE"
    ;;
  *) echo "unknown LAMP_SEED_DIST $DIST" >&2; exit 2 ;;
esac
# gcc/libc6-dev: the benchmark compile workload; python3: the frozen workload.
PACKAGES=python3,python3-pip,python3-venv,git,curl,wget,ca-certificates,nodejs,npm,openssh-client,openssh-server,sudo,procps,iproute2,coreutils,gcc,libc6-dev
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
(cd "$ROOT" && cargo build --release --locked --target x86_64-unknown-linux-musl -p lamp-init -p lamp-guard)
BIN="${CARGO_TARGET_DIR:-$ROOT/target}/x86_64-unknown-linux-musl/release"

# 3. Debian root; mmdebstrap verifies the archive signatures with the
#    distribution keyring.
#    The tar is cached only once complete (a killed run must not leave an
#    empty cache entry). LAMP_SEED_IPV4=1 keeps apt off an unreachable IPv6.
if [ ! -s "root-$DIST-usable-v1.tar" ]; then
  APTOPTS=(--aptopt='Acquire::Retries "5"')
  [ "${LAMP_SEED_IPV4:-0}" = 1 ] && APTOPTS+=(--aptopt='Acquire::ForceIPv4 "true"')
  sudo -n rm -f "root-$DIST-usable-v1.tar.partial"
  sudo -n mmdebstrap --variant=minbase --keyring="$KEYRING" --include="$PACKAGES" "${APTOPTS[@]}" \
    --format=tar "$SUITE" "root-$DIST-usable-v1.tar.partial" "${SOURCES[@]}"
  sudo -n mv "root-$DIST-usable-v1.tar.partial" "root-$DIST-usable-v1.tar"
fi
sudo -n rm -rf rootdir && mkdir rootdir
sudo -n tar -C rootdir -xf "root-$DIST-usable-v1.tar"
sudo -n install -m 0755 "$BIN/lamp-init" rootdir/sbin/lamp-init
sudo -n install -m 0755 "$BIN/lamp-guest-helper" rootdir/sbin/lamp-guest-helper
sudo -n install -m 0755 "$BIN/lamp-guard" rootdir/sbin/lamp-guard
sudo -n chmod 1777 rootdir/var/tmp
echo lamp | sudo -n tee rootdir/etc/hostname >/dev/null
printf '127.0.0.1 localhost lamp\n::1 localhost\n' | sudo -n tee rootdir/etc/hosts >/dev/null
# Never ship mmdebstrap's copied host resolver, search domains or SSH identity.
sudo -n rm -f rootdir/etc/resolv.conf rootdir/etc/ssh/ssh_host_* rootdir/etc/apt/apt.conf.d/99mmdebstrap
printf 'nameserver 198.18.240.1\noptions timeout:2 attempts:2\n' | sudo -n tee rootdir/etc/resolv.conf >/dev/null
# Keep ordinary pip installs away from the distribution's managed Python.
sudo -n chroot rootdir python3 -m venv --system-site-packages /opt/venv
sudo -n ln -sf /opt/venv/bin/pip rootdir/usr/local/bin/pip
sudo -n ln -sf /opt/venv/bin/pip3 rootdir/usr/local/bin/pip3
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
python3 - "$OUT" "$KERNEL_VERSION" "$KERNEL_SHA256" "$DISTRIBUTION" "$BIN/lamp-init" "$BIN/lamp-guest-helper" "$BIN/lamp-guard" "$DIST" <<'PY'
import hashlib, json, os, sys
out, kver, ksha, distribution, init, helper, guard, name = sys.argv[1:]
CHUNK = 4 << 20
ZERO = hashlib.sha256(bytes(CHUNK)).digest()
def digest(p):
    # sha256-c4m: SHA-256 over the SHA-256 of each 4 MiB chunk (holes skipped).
    out = hashlib.sha256()
    size = os.path.getsize(p)
    with open(p, 'rb') as f:
        fd = f.fileno()
        for start in range(0, size, CHUNK):
            n = min(CHUNK, size - start)
            try:
                data_at = os.lseek(fd, start, os.SEEK_DATA)
            except OSError:
                data_at = size
            if n == CHUNK and data_at >= start + n:
                out.update(ZERO)
                continue
            f.seek(start)
            out.update(hashlib.sha256(f.read(n)).digest())
    return out.hexdigest()
obj = lambda p: {"digest": digest(p), "size": os.path.getsize(p)}
seed = {
    "format": "lamp.seed/v0",
    "digest_alg": "sha256-c4m",
    "name": name,
    "guest_os": "linux",
    "arch": "x86_64",
    "kernel": {**obj(os.path.join(out, "vmlinux")), "source": f"linux-{kver}.tar.xz", "source_sha256": ksha,
               "cmdline": "console=ttyS0 reboot=k panic=1 pci=off i8042.noaux i8042.nomux i8042.dumbkbd root=/dev/vda rw init=/usr/sbin/lamp-init quiet"},
    "rootfs": {**obj(os.path.join(out, "rootfs.ext4")), "filesystem": "ext4", "distribution": distribution},
    "init": {"lamp-init": digest(init), "lamp-guest-helper": digest(helper), "lamp-guard": digest(guard)},
    "packages": obj(os.path.join(out, "packages.txt")),
}
with open(os.path.join(out, "seed.json"), "w") as f:
    f.write(json.dumps(seed, sort_keys=True, separators=(",", ":")))
print("seed digest sha256:" + hashlib.sha256(open(os.path.join(out, "seed.json"), "rb").read()).hexdigest())
PY
