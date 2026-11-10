#!/usr/bin/env bash
# Builds a native macOS guest from the owner's own Apple restore image and
# imports it as a Lamp template.
#   infra/mac/build-macos-guest.sh IPSW VMDIR NAME [CPUS MEMORY_MIB DISK_GIB]
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
  DEV=$(sudo -n hdiutil attach -nomount -noverify -noautofsck "$VMDIR/disk.img" | awk '/Apple_APFS/{print $1}' | head -1)
  trap 'sudo -n hdiutil detach "$DEV" -force >/dev/null 2>&1 || true' EXIT
  # The installed container's Data-role volume.
  DATA=$(diskutil apfs list -plist "$DEV" | plutil -extract Containers.0.Volumes json -o - - |
    python3 -c 'import json,sys; v=[x for x in json.load(sys.stdin) if "Data" in x.get("Roles",[])]; print(v[0]["DeviceIdentifier"])')
  MNT=$(mktemp -d /private/tmp/lamp-guest-data.XXXX)
  sudo -n mount_apfs -o nobrowse,noowners "/dev/$DATA" "$MNT"
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
  sudo -n hdiutil detach "$DEV" >/dev/null
  trap - EXIT
  rmdir "$MNT"
  shasum -a 256 "$HELPER" > "$VMDIR/.provisioned"
fi
rm -f "$VMDIR/.never-booted"
sudo -n "$LIB/lamp-supervisor" template-import-macos "$VMDIR" "$NAME" \
  --min-vcpus 2 --min-memory-mib 4096 --min-disk-gib "$DISK" --vcpus "$CPUS" --memory-mib "$MEM" --disk-gib "$DISK"
