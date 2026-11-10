#!/usr/bin/env bash
# Builds a native macOS guest from the owner's own Apple restore image and
# imports it as a Lamp template.
#   infra/mac/build-macos-guest.sh [--provision-only] IPSW VMDIR NAME [CPUS MEMORY_MIB DISK_GIB]
#
# --provision-only stops after step 2 (the import needs the identities free,
# so it runs with the supervisor stopped).
#
# 1. `lamp-vz install`: Apple's VZMacOSInstaller writes a new disk from the
#    signed IPSW (the owner accepted Apple's licence for guest VMs, 2026-11-09).
# 2. Provisioning, before the guest has ever booted: the guest's Data volume
#    is mounted nobrowse on the host and gets exactly three things - the
#    lamp-mac-guest-helper binary, its root LaunchDaemon, and
#    /var/db/.AppleSetupDone - then it is unmounted again. DEVIATION from
#    §3.2 ("no guest filesystem is mounted by a privileged host parser"): the
#    only disk ever mounted is one written solely by Apple's installer from a
#    signed image and never booted, so no guest-controlled bytes reach the
#    host parser. A guest-run disk is never mounted (refused below).
# 3. `lamp-supervisor template-import-macos`: boots it confined once with a
#    fresh machine identity, checks the helper and `sw_vers`, shuts it down
#    cleanly and publishes disk + boot storage as a signed template.
#
# Runs in the owner's login session (VZ needs it, see T8-evidence). Never
# shows a dialog; privileged steps are sudo -n.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
ONLY_PROVISION=0
if [ "${1:-}" = "--provision-only" ]; then ONLY_PROVISION=1; shift; fi
IPSW=$1 VMDIR=$2 NAME=$3 CPUS=${4:-2} MEM=${5:-4096} DISK=${6:-48}
LIB=/usr/local/lib/lamp
T=${CARGO_TARGET_DIR:-$ROOT/target}/release

if [ ! -f "$VMDIR/disk.img" ]; then
  "$LIB/lamp-vz" install --ipsw "$IPSW" --dir "$VMDIR" --cpus "$CPUS" --memory-mib "$MEM" --disk-gib "$DISK"
  touch "$VMDIR/.never-booted"
fi
[ -f "$VMDIR/.never-booted" ] || { echo "refusing to mount a disk that has booted"; exit 1; }

if [ ! -f "$VMDIR/.provisioned" ]; then
  (cd "$ROOT" && cargo build --release --locked -p lamp-init --bin lamp-mac-guest-helper)
  HELPER="$T/lamp-mac-guest-helper"
  codesign --force --sign - "$HELPER"
  ATTACHED=$(sudo -n hdiutil attach -nomount -noverify -noautofsck -plist "$VMDIR/disk.img")
  # The whole image device, detached at the end; the main APFS container is
  # the synthesized disk whose physical store is the Apple_APFS partition.
  IMG=$(echo "$ATTACHED" | plutil -extract system-entities json -o - - |
    python3 -c 'import json,sys; e=json.load(sys.stdin); print(min((x["dev-entry"] for x in e), key=len))')
  trap 'sudo -n hdiutil detach "$IMG" -force >/dev/null 2>&1 || true' EXIT
  STORE=$(echo "$ATTACHED" | plutil -extract system-entities json -o - - |
    python3 -c 'import json,sys; print([x["dev-entry"] for x in json.load(sys.stdin) if x.get("content-hint")=="Apple_APFS"][0])')
  sleep 1  # the synthesized container appears just after the attach
  CONTAINER=$(diskutil apfs list -plist | plutil -convert json -o - - | python3 -c '
import json,sys
store=sys.argv[1].replace("/dev/","")
for c in json.load(sys.stdin)["Containers"]:
    if any(p["DeviceIdentifier"]==store for p in c["PhysicalStores"]): print(c["ContainerReference"])' "$STORE")
  # The installed container's Data-role volume.
  DATA=$(diskutil apfs list -plist "$CONTAINER" | plutil -extract Containers.0.Volumes json -o - - |
    python3 -c 'import json,sys; v=[x for x in json.load(sys.stdin) if "Data" in x.get("Roles",[])]; print(v[0]["DeviceIdentifier"])')
  MNT=$(mktemp -d /private/tmp/lamp-guest-data.XXXX)
  sudo -n mount_apfs -o nobrowse "/dev/$DATA" "$MNT"
  sudo -n install -d -o root -g wheel -m 0755 "$MNT/usr/local/libexec" "$MNT/Library/LaunchDaemons" "$MNT/private/var/db/lamp"
  sudo -n install -o root -g wheel -m 0755 "$HELPER" "$MNT/usr/local/libexec/lamp-mac-guest-helper"
  sudo -n tee "$MNT/Library/LaunchDaemons/dev.lamp.guest-helper.plist" >/dev/null <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>dev.lamp.guest-helper</string>
  <key>ProgramArguments</key><array><string>/usr/local/libexec/lamp-mac-guest-helper</string></array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardErrorPath</key><string>/private/var/db/lamp/helper.log</string>
</dict>
</plist>
PLIST
  sudo -n chown root:wheel "$MNT/Library/LaunchDaemons/dev.lamp.guest-helper.plist"
  sudo -n chmod 0644 "$MNT/Library/LaunchDaemons/dev.lamp.guest-helper.plist"
  sudo -n touch "$MNT/private/var/db/.AppleSetupDone"
  ls -la "$MNT" "$MNT/Library/LaunchDaemons" | sudo -n tee "$VMDIR/provision.log" >/dev/null
  sudo -n umount "$MNT"
  sudo -n hdiutil detach "$IMG" >/dev/null
  trap - EXIT
  rmdir "$MNT"
  shasum -a 256 "$HELPER" > "$VMDIR/.provisioned"
fi
[ "$ONLY_PROVISION" = 1 ] && { echo "provisioned: $VMDIR"; exit 0; }
rm -f "$VMDIR/.never-booted"
sudo -n "$LIB/lamp-supervisor" template-import-macos "$VMDIR" "$NAME" \
  --min-vcpus 2 --min-memory-mib 4096 --min-disk-gib "$DISK" --vcpus "$CPUS" --memory-mib "$MEM" --disk-gib "$DISK"
