#!/bin/bash
set -euo pipefail
printf 'Guest OS: '; . /etc/os-release; echo "$PRETTY_NAME"
uname -a
printf 'Guest vCPUs: '; nproc
free -b
lsblk -b -o NAME,SIZE,FSTYPE,MOUNTPOINTS
sha256sum /opt/lamp-la/Image /opt/lamp-la/initramfs.cpio.gz
dpkg-query -W qemu-system-arm busybox-static "linux-image-$(uname -r)"
id
ls -l /dev/kvm
test -c /dev/kvm && test -r /dev/kvm && test -w /dev/kvm
printf 'Kernel source: '; cat /opt/lamp-la/kernel-source
qemu-system-aarch64 --version
log=$(mktemp)
trap 'rm -f "$log"' EXIT
set +e
set -x
timeout --kill-after=5s 90s qemu-system-aarch64 \
  -machine virt -accel kvm -cpu host -smp 1 -m 512M \
  -kernel /opt/lamp-la/Image -initrd /opt/lamp-la/initramfs.cpio.gz \
  -append 'console=ttyAMA0 rdinit=/init panic=-1' \
  -nographic -no-reboot -nic none > "$log" 2>&1 < /dev/null
result=$?
set +x
set -e
cat "$log"
test "$result" -eq 0
grep -qx 'LAMP_LA_NESTED_KVM_BOOT_OK' <(tr -d '\r' < "$log")
echo 'PASS: nested Linux booted with mandatory -accel kvm and powered off (QEMU exit 0).'
