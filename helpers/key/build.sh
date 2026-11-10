#!/usr/bin/env bash
# Builds the Hammock Key helper into bin/Hammock Key.app: the process that holds
# this Mac's Hammock Key (an SSH key in the Secure Enclave) and signs with it
# only once you approve (helpers/key/main.swift). A bundle, so the
# Touch ID prompt names it "Hammock Key" with Hammock's icon.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
app="$root/bin/Hammock Key.app"
name="Hammock Key"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$root/apple/Resources/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns"
# rendezvous.h: how the daemon hands it a request (on
# helpers/common/rendezvous.h, which internal/daemon builds from too).
swiftc -O -swift-version 5 -target arm64-apple-macos26.0 -import-objc-header "$here/rendezvous.h" \
  "$here/main.swift" "$here/panel.swift" -o "$app/Contents/MacOS/$name"
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>$name</string>
  <key>CFBundleIdentifier</key><string>com.homegrownsw.hammock.key</string>
  <key>CFBundleName</key><string>Hammock Key</string>
  <key>CFBundleDisplayName</key><string>Hammock Key</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>26.0</string>
</dict></plist>
PLIST
echo "$app"
