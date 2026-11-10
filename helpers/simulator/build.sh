#!/usr/bin/env bash
# Builds the simulator host into bin/Hammock Simulator.app: the process that
# shows and drives simulator panes (helpers/simulator/host.m). It links no
# private framework; it loads CoreSimulator and SimulatorKit at run time.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
app="$root/bin/Hammock Simulator.app"
name="Hammock Simulator"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
clang -fobjc-arc -O2 -mmacosx-version-min=14.0 -Wall -Wno-objc-method-access \
  "$here/host.m" "$here/../media/encoder.m" -o "$app/Contents/MacOS/$name" \
  -framework AppKit -framework QuartzCore -framework CoreImage -framework IOSurface \
  -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework Security
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>CFBundleExecutable</key><string>$name</string>
  <key>CFBundleIdentifier</key><string>com.homegrownsw.hammock.simulator</string>
  <key>CFBundleName</key><string>$name</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
</dict></plist>
PLIST

# The guest helper (guest/): runs inside a foldable simulator for its fold.
mkdir -p "$app/Contents/Resources"
if xcrun -sdk iphonesimulator clang -arch arm64 -mios-simulator-version-min=26.0 -fobjc-arc -O2 -Wall \
  -framework Foundation "$here/guest/main.m" -o "$app/Contents/Resources/hmk-sim-guest"; then
  codesign -s - -f "$app/Contents/Resources/hmk-sim-guest" 2>/dev/null
else
  echo "warning: the simulator guest helper didn't build; foldables won't fold" >&2
fi

# The XCTest runner (runner/): rotation and multi-finger gestures. Built
# with xcodebuild, so cached by its sources' hash under bin/.sim-runner.
cache="$root/bin/.sim-runner"
# Its sources, how it's built (this script) and the Xcode building it.
hash="$( (cd "$here/runner" && find . -type f -not -name '.*' | LC_ALL=C sort | xargs shasum; shasum <"$here/build.sh"; xcodebuild -version 2>/dev/null) | shasum | cut -c1-16)"
if [ ! -f "$cache/$hash.ok" ]; then
  if command -v xcodegen >/dev/null; then
    rm -rf "$cache"
    mkdir -p "$cache"
    work="$(mktemp -d)"
    # Either failing only loses the runner (a warning), not the whole build.
    # Release, stripped and with source paths mapped away: the bundle carries no path from the Mac that
    # built it.
    if xcodegen generate --quiet --spec "$here/runner/project.yml" --project "$work" >"$work/build.log" 2>&1 &&
      xcodebuild -project "$work/HammockSimRunner.xcodeproj" -scheme HammockSimRunner -configuration Release \
      -destination 'generic/platform=iOS Simulator' -derivedDataPath "$work/dd" CODE_SIGNING_ALLOWED=NO \
      DEBUG_INFORMATION_FORMAT=dwarf-with-dsym DEPLOYMENT_POSTPROCESSING=YES STRIP_INSTALLED_PRODUCT=YES STRIP_STYLE=non-global \
      OTHER_CFLAGS="-ffile-prefix-map=$here/runner=runner" build-for-testing -quiet >"$work/build.log" 2>&1; then
      products="$work/dd/Build/Products"
      find "$products" -name "*.dSYM" -prune -exec rm -rf {} +
      # Xcode's runner app is Apple's (its XCTRunner, with its Info.plist filled in): not ours to ship. The
      # simulator host makes it again at first use from the Xcode on the Mac it runs on (host.m,
      # prepareRunner). Only its PlugIns, our test bundle, ship, in X-Runner.plugins: a folder named .app
      # with no app in it would fail the Mac app's code signing.
      for runnerApp in "$products"/*/*-Runner.app; do
        mv "$runnerApp/PlugIns" "${runnerApp%.app}.plugins"
        rm -rf "$runnerApp"
      done
      # Code coverage's list of source files is the only absolute path in the xctestrun.
      for f in "$products"/*.xctestrun; do plutil -remove CodeCoverageBuildableInfos "$f"; done
      mkdir -p "$cache/products"
      cp -R "$products/." "$cache/products/"
      touch "$cache/$hash.ok"
    else
      tail -20 "$work/build.log" >&2
      echo "warning: the simulator runner didn't build; no rotation or pinch in simulator panes" >&2
    fi
    rm -rf "$work"
  else
    echo "warning: xcodegen not found; no rotation or pinch in simulator panes (brew install xcodegen)" >&2
  fi
fi
if [ -f "$cache/$hash.ok" ]; then
  mkdir -p "$app/Contents/Resources"
  cp -R "$cache/products" "$app/Contents/Resources/runner"
fi
echo "$app"
