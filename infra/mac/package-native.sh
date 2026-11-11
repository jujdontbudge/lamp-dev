#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)
binary=${1:?usage: package-native.sh BINARY NEW_APP_DIRECTORY}
app=${2:?usage: package-native.sh BINARY NEW_APP_DIRECTORY}
identifier="si.eilmer.lamp.native-task.task$(uuidgen | tr -d '-')"
if [ -e "$app" ]; then
    printf '%s\n' "refusing existing output: $app" >&2
    exit 1
fi
mkdir -p "$app/Contents/MacOS"
cp "$binary" "$app/Contents/MacOS/lamp-native"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleIdentifier</key><string>$identifier</string>
    <key>CFBundleExecutable</key><string>lamp-native</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleVersion</key><string>1</string>
    <key>LSBackgroundOnly</key><true/>
</dict>
</plist>
PLIST
codesign --force --sign - --entitlements "$root/crates/lamp-vz/lamp-native.entitlements" "$app"
printf '%s\n' "$app/Contents/MacOS/lamp-native"
