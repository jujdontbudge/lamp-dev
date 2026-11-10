#!/usr/bin/env bash
# Installs the Lamp Mac engine on an Apple Silicon Mac (macOS 26+).
#   infra/mac/install-engine.sh [RAM_GIB] [--start]
#
# - builds lamp-vz (the Virtualization.framework worker) and lamp-supervisor,
#   signs lamp-vz ad hoc with only com.apple.security.virtualization, and
#   installs both root-owned in /usr/local/lib/lamp;
# - creates the hidden worker accounts _lampvm01.._lampvm16 (one per live
#   sandbox: no shell, no home, hidden from the login window);
# - creates the store /private/var/lamp/store and, when RAM_GIB > 0, a
#   RAM-backed volume at /private/var/lamp/ram for volatile fork state
#   (mounted nobrowse so it never appears in Finder);
# - with --start, runs the supervisor in the background under sudo.
#
# Never shows a dialog: no keychain access (ad hoc signature), no launchd
# bootstrap of system items, every privileged step is `sudo -n`.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
RAM_GIB=${1:-4}
START=0
for a in "$@"; do [ "$a" = "--start" ] && START=1; done
LIB=/usr/local/lib/lamp
BASE=/private/var/lamp
FIRST_ID=452

[ "$(uname -s)/$(uname -m)" = "Darwin/arm64" ] || { echo "needs an Apple Silicon Mac"; exit 1; }
sudo -n true || { echo "needs passwordless sudo (sudo -n)"; exit 1; }

(cd "$ROOT" && cargo build --release --locked -p lamp-vz -p lamp-supervisor)
T=${CARGO_TARGET_DIR:-$ROOT/target}/release
STAGE=$(mktemp -d)
trap 'rm -rf "$STAGE"' EXIT
cp "$T/lamp-vz" "$STAGE/lamp-vz"
codesign --force --sign - --options runtime \
  --entitlements "$ROOT/crates/lamp-vz/lamp-vz.entitlements" "$STAGE/lamp-vz"
codesign -d --entitlements - "$STAGE/lamp-vz" 2>/dev/null | grep -q com.apple.security.virtualization
sudo -n install -d -o root -g wheel -m 0755 "$LIB"
sudo -n install -o root -g wheel -m 0755 "$STAGE/lamp-vz" "$LIB/lamp-vz"
sudo -n install -o root -g wheel -m 0755 "$T/lamp-supervisor" "$LIB/lamp-supervisor"

for i in $(seq 1 16); do
  name=$(printf '_lampvm%02d' "$i")
  id=$((FIRST_ID + i - 1))
  if ! dscl . -read "/Groups/$name" >/dev/null 2>&1; then
    sudo -n dscl . -create "/Groups/$name"
    sudo -n dscl . -create "/Groups/$name" PrimaryGroupID "$id"
    sudo -n dscl . -create "/Groups/$name" RealName "Lamp VM worker $i"
  fi
  if ! dscl . -read "/Users/$name" >/dev/null 2>&1; then
    sudo -n dscl . -create "/Users/$name"
    sudo -n dscl . -create "/Users/$name" UniqueID "$id"
    sudo -n dscl . -create "/Users/$name" PrimaryGroupID "$id"
    sudo -n dscl . -create "/Users/$name" UserShell /usr/bin/false
    sudo -n dscl . -create "/Users/$name" NFSHomeDirectory /var/empty
    sudo -n dscl . -create "/Users/$name" RealName "Lamp VM worker $i"
    sudo -n dscl . -create "/Users/$name" IsHidden 1
    sudo -n dscl . -create "/Users/$name" Password '*'
  fi
done

sudo -n install -d -o root -g wheel -m 0755 "$BASE" "$BASE/store"
if [ "$RAM_GIB" -gt 0 ] && ! mount | grep -q " on $BASE/ram "; then
  sudo -n install -d -o root -g wheel -m 0755 "$BASE/ram"
  dev=$(hdiutil attach -nomount "ram://$((RAM_GIB * 2097152))" | awk '{print $1}')
  sudo -n newfs_hfs -v lampram "$dev" >/dev/null
  sudo -n mount -t hfs -o nobrowse,noatime "$dev" "$BASE/ram"
  sudo -n chown root:wheel "$BASE/ram"
  sudo -n chmod 0755 "$BASE/ram"
fi
echo "installed: $LIB/lamp-vz (ad hoc, virtualization entitlement), $LIB/lamp-supervisor"
if [ "$START" = 1 ]; then
  sudo -n sh -c "nohup $LIB/lamp-supervisor serve --allow-uid $(id -u) >>$BASE/supervisor.log 2>&1 &"
  for _ in $(seq 100); do [ -S /private/var/run/lamp/worker.sock ] && break; sleep 0.1; done
  echo "supervisor: /private/var/run/lamp/worker.sock (log $BASE/supervisor.log)"
fi
