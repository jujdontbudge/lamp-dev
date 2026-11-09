#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
ROOT=${LAMP_LANES_ROOT:-$HOME/.lamp-lanes}
IMAGES=/var/lib/libvirt/images/lamp-lanes
mkdir -p "$ROOT"/{media,evidence,work}
exec 9>"$ROOT/provision.lock"
flock -n 9 || { echo 'Another lane provisioner is running' >&2; exit 1; }
v() { sudo -n virsh -c qemu:///system "$@"; }
fetch() {
 local url=$1 file=$2
 printf "%s\n" "$url" > "$file.source"
 if [[ -s "$file" && -s "$file.sha256" ]]; then
  sha256sum --check --status "$file.sha256" || { echo "Cached media checksum mismatch: $file" >&2; return 1; }
 fi
 if [[ ! -s "$file" ]]; then
  curl --silent --show-error --fail --location --retry 3 --connect-timeout 30 --max-time 3600 "$url" -o "$file.part"
  mv "$file.part" "$file"
 fi
 sha256sum "$file" > "$file.sha256"
}
setup() {
 test -r /dev/kvm && test -w /dev/kvm
 test -f "$ROOT/id_ed25519" || ssh-keygen -q -t ed25519 -N '' -f "$ROOT/id_ed25519"
 sudo -n install -d -m 0755 "$IMAGES"
 if ! v net-info lamp-lanes >/dev/null 2>&1; then
  cat > "$ROOT/work/network.xml" <<'XML'
<network><name>lamp-lanes</name><bridge name="virbr-lamp"/><forward mode="nat"/><ip address="192.168.178.1" netmask="255.255.255.0"><dhcp><range start="192.168.178.100" end="192.168.178.200"/></dhcp></ip></network>
XML
  v net-define "$ROOT/work/network.xml"
 fi
 v net-info lamp-lanes | grep 'Active:.*yes' >/dev/null || v net-start lamp-lanes
 v net-autostart lamp-lanes
 local other
 other=$(v list --name | grep '^lamp-' | grep -v "^lamp-${LANE,,}$" || true)
 [[ -z $other ]] || { echo "Another Lamp lane is running: $other" >&2; return 1; }
}
reset_lane() {
 NAME=lamp-${LANE,,}
 # Only the named fixture is destroyed, never other domains or Docker workloads.
 if v dominfo "$NAME" >/dev/null 2>&1; then
  v destroy "$NAME" >/dev/null 2>&1 || true
  v undefine "$NAME" --nvram --snapshots-metadata
 fi
 sudo -n rm -f "$IMAGES/$LANE.qcow2" "$IMAGES/$LANE-seed.iso"
 mkdir -p "$ROOT/work/$LANE"
 WORK=$ROOT/work/$LANE
 : > "$WORK/known_hosts"
 trap 'rc=$?; if ((rc)); then echo "BLOCKED: $LANE line $LINENO exit $rc" | tee "$ROOT/evidence/$LANE-status.txt"; v destroy "$NAME" >/dev/null 2>&1 || true; fi' EXIT
}
address() { v domifaddr "$NAME" --source lease | awk '/ipv4/ {sub(/\/.*/,"",$4);print $4;exit}'; }
gssh() { ssh -i "$ROOT/id_ed25519" -o BatchMode=yes -o ConnectTimeout=5 -o StrictHostKeyChecking=no -o UserKnownHostsFile="$WORK/known_hosts" "$USER_NAME@$IP" "$@"; }
wait_ssh() {
 for ((i=0;i<180;i++)); do
  IP=$(address)
  if [[ -n "$IP" ]] && gssh "$1" > "$WORK/ssh-ready.txt" 2>/dev/null; then echo "$IP" > "$ROOT/evidence/$LANE-ip.txt"; return; fi
  sleep 10
 done
 v dumpxml "$NAME" > "$ROOT/evidence/$LANE-domain.xml"
 echo "Timed out waiting for SSH in $NAME (IP $IP)" >&2
 return 1
}
finish() {
 v shutdown "$NAME"
 for ((i=0;i<90;i++)); do
  [[ $(v domstate "$NAME") == 'shut off' ]] && break
  sleep 2
 done
 [[ $(v domstate "$NAME") == 'shut off' ]]
 # Offline internal qcow2 snapshot: no RAM or external backing chain.
 v snapshot-create-as "$NAME" clean --description 'Verified, powered-off Linux fixture' --atomic
 v dumpxml "$NAME" > "$ROOT/evidence/$LANE-domain.xml"
 echo VERIFIED > "$ROOT/evidence/$LANE-status.txt"
}
