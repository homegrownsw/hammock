#!/usr/bin/env bash
# Builds the browser-pane spike into spikes/browser/out:
#   BrowserHost.app  - CEF host (design B: hidden window + remote layer)
#   BrowserSpike.app - minimal viewer that hosts the layer and forwards input
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
cef="$("$root/scripts/fetch-cef.sh")"
out="$here/out"
mkdir -p "$out/obj"

cxx=(clang++ -std=c++20 -O2 -fobjc-arc -fno-exceptions -fno-rtti -fobjc-call-cxx-cdtors
     -fvisibility-inlines-hidden -Wno-narrowing -mmacosx-version-min=14.0 -DNDEBUG -I"$cef")
wrapper="$cef/build/libcef_dll_wrapper/libcef_dll_wrapper.a"

plist() { # name exec id
  cat <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>$2</string>
  <key>CFBundleIdentifier</key><string>$3</string>
  <key>CFBundleName</key><string>$1</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSSupportsAutomaticGraphicsSwitching</key><true/>
  <key>LSEnvironment</key><dict><key>MallocNanoZone</key><string>0</string></dict>
</dict></plist>
PLIST
}

# --- BrowserHost.app ---
app="$out/BrowserHost.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Frameworks"
"${cxx[@]}" -x objective-c++ -c "$here/host/host.mm" -o "$out/obj/host.o"
"${cxx[@]}" -c "$here/host/helper.cc" -o "$out/obj/helper.o"
clang++ -o "$app/Contents/MacOS/BrowserHost" "$out/obj/host.o" "$wrapper" \
  -framework AppKit -framework QuartzCore -framework CoreGraphics -Wl,-ObjC
plist BrowserHost BrowserHost tv.hammock.spike.browser-host > "$app/Contents/Info.plist"
# APFS clone: no extra disk for the ~300 MB framework.
cp -cR "$cef/Release/Chromium Embedded Framework.framework" "$app/Contents/Frameworks/"
# No "(Alerts)" helper: it exists only to post macOS notifications and asks for
# notification permission on first launch. Browser panes don't need them.
for s in "" " (GPU)" " (Plugin)" " (Renderer)"; do
  name="BrowserHost Helper$s"
  h="$app/Contents/Frameworks/$name.app"
  mkdir -p "$h/Contents/MacOS"
  clang++ -o "$h/Contents/MacOS/$name" "$out/obj/helper.o" "$wrapper" -framework AppKit
  id_suffix="$(echo "$s" | tr -d ' ()' | tr '[:upper:]' '[:lower:]')"
  plist "$name" "$name" "tv.hammock.spike.browser-host.helper${id_suffix:+.$id_suffix}" > "$h/Contents/Info.plist"
done
codesign -s - --force --deep "$app" >/dev/null 2>&1 || true

# --- BrowserSpike.app (viewer) ---
if [ -f "$here/viewer/viewer.m" ]; then
  v="$out/BrowserSpike.app"
  rm -rf "$v"
  mkdir -p "$v/Contents/MacOS"
  clang -fobjc-arc -O2 -mmacosx-version-min=14.0 "$here/viewer/viewer.m" -o "$v/Contents/MacOS/BrowserSpike" \
    -framework AppKit -framework QuartzCore -framework CoreGraphics
  plist BrowserSpike BrowserSpike tv.hammock.spike.viewer | sed 's#<key>LSUIElement</key><true/>##' > "$v/Contents/Info.plist"
  codesign -s - --force "$v" >/dev/null 2>&1 || true
fi
echo "built: $out"
