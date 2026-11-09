#!/usr/bin/env bash
# Shared Linux implementation; invoke a lane wrapper.
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
setup
case "$LANE" in
 LX22) URL=https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img ;;
 D12) URL=https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-generic-amd64.qcow2 ;;
 L510) URL=https://cloud.debian.org/images/cloud/bullseye/latest/debian-11-generic-amd64.qcow2 ;;
 F40) URL=https://archives.fedoraproject.org/pub/archive/fedora/linux/releases/40/Cloud/x86_64/images/Fedora-Cloud-Base-Generic.x86_64-40-1.14.qcow2 ;;
 NV) URL=https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img ;;
 *) exit 2 ;;
esac
fetch "$URL" "$ROOT/media/$LANE-base.qcow2"
reset_lane
sudo -n qemu-img convert -O qcow2 "$ROOT/media/$LANE-base.qcow2" "$IMAGES/$LANE.qcow2"
sudo -n qemu-img resize "$IMAGES/$LANE.qcow2" 40G
KEY=$(cat "$ROOT/id_ed25519.pub")
cat > "$WORK/user-data" <<YAML
#cloud-config
hostname: $NAME
users:
  - name: lamp
    groups: [sudo, wheel, kvm]
    sudo: ALL=(ALL) NOPASSWD:ALL
    shell: /bin/bash
    lock_passwd: true
    ssh_authorized_keys:
      - $KEY
ssh_pwauth: false
runcmd:
  - [ sh, -c, 'modprobe kvm_intel || true' ]
YAML
printf 'instance-id: %s-%s\nlocal-hostname: %s\n' "$NAME" "$(date +%s)" "$NAME" > "$WORK/meta-data"
cloud-localds "$WORK/seed.iso" "$WORK/user-data" "$WORK/meta-data"
sudo -n install -m 0644 "$WORK/seed.iso" "$IMAGES/$LANE-seed.iso"
CPU=host-passthrough
[[ $LANE != NV ]] || CPU=host-passthrough,disable=vmx,disable=svm
sudo -n virt-install --connect qemu:///system --name "$NAME" --memory 4096 --vcpus 2,sockets=1,cores=2,threads=1 --cpu "$CPU" --machine q35 --import --osinfo detect=on,require=off --disk "path=$IMAGES/$LANE.qcow2,format=qcow2,bus=virtio" --disk "path=$IMAGES/$LANE-seed.iso,device=cdrom" --network network=lamp-lanes,model=virtio --graphics none --noautoconsole
USER_NAME=lamp
wait_ssh true
gssh 'sudo cloud-init status --wait; cat /etc/os-release; uname -a; lscpu; findmnt /; ls -l /dev/kvm 2>&1; sudo sh -c "cat /sys/module/kvm_intel/parameters/nested 2>/dev/null"; true' > "$ROOT/evidence/$LANE-guest.txt"
if [[ $LANE == NV ]]; then
 gssh 'test ! -e /dev/kvm && ! grep -qw vmx /proc/cpuinfo && ! grep -qw svm /proc/cpuinfo'
else
 gssh 'sudo modprobe kvm_intel && sudo usermod -aG kvm lamp && test -c /dev/kvm && grep -qw vmx /proc/cpuinfo'
 gssh 'python3 -' < "$HERE/verify-kvm.py" > "$ROOT/evidence/$LANE-kvm.txt"
fi
[[ $LANE != L510 ]] || gssh 'uname -r | grep -E "^5\.10\."'
finish
