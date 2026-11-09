#!/usr/bin/env bash
# Builds the screen-sharing host into bin/Hammock Screen.app: the process that
# captures this Mac's displays and posts remote viewers' input
# (helpers/screen/host.m, ADR-0014).
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
app="$root/bin/Hammock Screen.app"
name="Hammock Screen"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
# Hammock's icon, so it's recognisable in System Settings ▸ Privacy & Security
# (the app's build puts in its own variant's: embed-mac-helpers.sh).
cp "$root/apple/Resources/AppIcon.icns" "$app/Contents/Resources/AppIcon.icns"
# The empty __CGPreLoginApp section: without it, the events it posts don't
# reach the lock screen's password field (as Chrome Remote Desktop's host).
clang -fobjc-arc -O2 -mmacosx-version-min=14.4 -Wall \
  "$here/host.m" "$here/../media/encoder.m" -o "$app/Contents/MacOS/$name" \
  -Wl,-sectcreate,__CGPreLoginApp,__cgpreloginapp,/dev/null \
  -framework AppKit -framework ApplicationServices -framework QuartzCore -framework ScreenCaptureKit \
  -framework IOSurface -framework IOKit -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreImage -framework ImageIO
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>$name</string>
  <key>CFBundleIdentifier</key><string>com.homegrownsw.hammock.screen</string>
  <key>CFBundleName</key><string>Hammock Screen Sharing</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundleDisplayName</key><string>Hammock Screen Sharing</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.4</string>
</dict></plist>
PLIST
echo "$app"
