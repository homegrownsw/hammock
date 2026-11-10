#!/usr/bin/env bash
# Builds the browser host into bin/Hammock Browser.app: the CEF process
# that runs every browser pane (helpers/browser/host.mm), with the Chromium
# framework (an APFS clone of the pinned CEF build) and its helper apps.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
cef="$("$root/scripts/fetch-cef.sh")"
obj="$root/bin/obj/browser"
app="$root/bin/Hammock Browser.app"
name="Hammock Browser"
id="com.homegrownsw.hammock.browser"
mkdir -p "$obj"

cxx=(clang++ -std=c++20 -O2 -fobjc-arc -fno-exceptions -fno-rtti -fobjc-call-cxx-cdtors
     -fvisibility-inlines-hidden -Wno-narrowing -mmacosx-version-min=14.0 -DNDEBUG -I"$cef")
wrapper="$cef/build/libcef_dll_wrapper/libcef_dll_wrapper.a"

plist() { # name id
  cat <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>$1</string>
  <key>CFBundleIdentifier</key><string>$2</string>
  <key>CFBundleName</key><string>$1</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSSupportsAutomaticGraphicsSwitching</key><true/>
  <key>LSEnvironment</key><dict><key>MallocNanoZone</key><string>0</string></dict>
</dict></plist>
PLIST
}

"${cxx[@]}" -x objective-c++ -c "$here/host.mm" -o "$obj/host.o"
"${cxx[@]}" -c "$here/helper.cc" -o "$obj/helper.o"
clang -fobjc-arc -O2 -mmacosx-version-min=14.0 -c "$here/../media/encoder.m" -o "$obj/encoder.o"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Frameworks"
clang++ -o "$app/Contents/MacOS/$name" "$obj/host.o" "$obj/encoder.o" "$wrapper" \
  -framework AppKit -framework QuartzCore -framework CoreGraphics -framework ImageIO \
  -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework IOSurface -framework Security -Wl,-ObjC
plist "$name" "$id" > "$app/Contents/Info.plist"
# Chromium's credits (the licences of Chromium and what it includes), from
# the CEF build: the Mac app's Acknowledgements show them, as does
# `hmk acknowledgements chromium`.
mkdir -p "$app/Contents/Resources"
cp "$cef/CREDITS.html" "$app/Contents/Resources/Chromium Credits.html"
# APFS clone: no extra disk for the ~300 MB framework.
cp -cR "$cef/Release/Chromium Embedded Framework.framework" "$app/Contents/Frameworks/"
# No "(Alerts)" helper: it exists only to post macOS notifications and asks for
# notification permission on first launch. Browser panes don't need them.
for s in "" " (GPU)" " (Plugin)" " (Renderer)"; do
  h="$app/Contents/Frameworks/$name Helper$s.app"
  mkdir -p "$h/Contents/MacOS"
  clang++ -o "$h/Contents/MacOS/$name Helper$s" "$obj/helper.o" "$wrapper" -framework AppKit
  suffix="$(echo "$s" | tr -d ' ()' | tr '[:upper:]' '[:lower:]')"
  plist "$name Helper$s" "$id.helper${suffix:+.$suffix}" > "$h/Contents/Info.plist"
done
echo "$app"
