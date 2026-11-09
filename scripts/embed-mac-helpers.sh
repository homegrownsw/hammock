#!/usr/bin/env bash
# Puts what the Mac app bundles besides its own executable into a built
# Hammock.app: hmkd (Contents/MacOS), the CLI, the pane holder and the browser
# and simulator hosts (Contents/Helpers), and the daemon's login item
# (Contents/Library/LaunchAgents). From bin/, built by `just build browser
# simulator` (or scripts/build-mac-payloads.sh):
#   scripts/embed-mac-helpers.sh APP BUNDLE_ID AGENT_LABEL [STATE_DIR]
# STATE_DIR is the app's HammockStateDir (default: Hammock, or Hammock Beta for β's
# label). EMBED_MENU=0 leaves out the menu bar item (a worktree's β).
# Used by build-mac-app.sh and by the Xcode project's HammockMac target.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
app="$1" id="$2" label="$3" statedir="${4:-}"
if [ -z "$statedir" ]; then
  case "$label" in *.beta) statedir="Hammock Beta" ;; *) statedir="Hammock" ;; esac
fi
bin="${HMK_BIN:-$root/bin}"

for f in hmkd hmk pane-holder; do
  [ -x "$bin/$f" ] || { echo "error: $bin/$f isn't built (just build, or scripts/build-mac-payloads.sh)" >&2; exit 1; }
done
mkdir -p "$app/Contents/MacOS" "$app/Contents/Helpers"
cp "$bin/hmkd" "$app/Contents/MacOS/hmkd"
# The CLI lives in Helpers, apart from the app and daemon executables.
cp "$bin/hmk" "$app/Contents/Helpers/hmk"
# The pane holder (one per terminal pane) is a helper bundle whose
# executable is "Hammock Pane": Activity Monitor then lists each as a Hammock
# Pane rather than "pane-holder" or one more "Hammock", and tools that name
# the terminal from the shell's parent process (fastfetch) say Hammock Pane.
# The bundle id stays .terminal, as when it was "Hammock Terminal.app/…/Hammock",
# so privacy grants made to it carry over.
rm -rf "$app/Contents/Helpers/Hammock Terminal.app"
helper="$app/Contents/Helpers/Hammock Pane.app"
rm -rf "$helper"
mkdir -p "$helper/Contents/MacOS"
cp "$bin/pane-holder" "$helper/Contents/MacOS/Hammock Pane"
cat > "$helper/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Hammock Pane</string>
  <key>CFBundleDisplayName</key><string>Hammock Pane</string>
  <key>CFBundleIdentifier</key><string>$id.terminal</string>
  <key>CFBundleExecutable</key><string>Hammock Pane</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
</dict>
</plist>
PLIST
# Browser panes' host (helpers/browser, built by `just browser`). An APFS
# clone, so the ~300 MB Chromium framework costs no extra disk.
browser="$bin/Hammock Browser.app"
rm -rf "$app/Contents/Helpers/Hammock Browser.app"
if [ -d "$browser" ]; then
  cp -cR "$browser" "$app/Contents/Helpers/"
else
  echo "warning: $browser not built; browser panes won't work (just browser)" >&2
fi
# Simulator panes' host (helpers/simulator, built by `just simulator`).
simulator="$bin/Hammock Simulator.app"
rm -rf "$app/Contents/Helpers/Hammock Simulator.app"
if [ -d "$simulator" ]; then
  cp -R "$simulator" "$app/Contents/Helpers/"
else
  echo "warning: $simulator not built; simulator panes won't work (just simulator)" >&2
fi
# Screen sharing's host (helpers/screen, built by `just screen`).
screen="$bin/Hammock Screen.app"
rm -rf "$app/Contents/Helpers/Hammock Screen.app"
if [ -d "$screen" ]; then
  cp -R "$screen" "$app/Contents/Helpers/"
  # Named for this variant (β's com.homegrownsw.hammock.beta.screen), as
  # the Key's helper is: Accessibility is granted to the helper itself, so
  # with one id β and prod would share that grant, and resetting one would
  # reset the other.
  /usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $id.screen" "$app/Contents/Helpers/Hammock Screen.app/Contents/Info.plist"
  # This variant's icon (β's for Hammock β), as System Settings shows it.
  if [ -f "$app/Contents/Resources/AppIcon.icns" ]; then
    cp "$app/Contents/Resources/AppIcon.icns" "$app/Contents/Helpers/Hammock Screen.app/Contents/Resources/AppIcon.icns"
  fi
else
  echo "warning: $screen not built; screen sharing won't work (just screen)" >&2
fi
# The Hammock Key's helper (helpers/key, built by `just key`).
keyapp="$bin/Hammock Key.app"
rm -rf "$app/Contents/Helpers/Hammock Key.app"
if [ -d "$keyapp" ]; then
  cp -R "$keyapp" "$app/Contents/Helpers/"
  # Named for this variant (β's com.homegrownsw.hammock.beta.key): its
  # signature's identifier is what the Hammock Key's keychain item trusts, so
  # β's helper can't silently read the real one's.
  /usr/libexec/PlistBuddy -c "Set :CFBundleIdentifier $id.key" "$app/Contents/Helpers/Hammock Key.app/Contents/Info.plist"
  if [ -f "$app/Contents/Resources/AppIcon.icns" ]; then
    cp "$app/Contents/Resources/AppIcon.icns" "$app/Contents/Helpers/Hammock Key.app/Contents/Resources/AppIcon.icns"
  fi
else
  echo "warning: $keyapp not built; no Hammock Key (just key)" >&2
fi
# The menu bar item (apple/Sources/HammockMenu, built by build-menu-app.sh): a
# login item of its own (the app registers it), with this variant's state
# dir and daemon label, so it talks to this variant's server.
menu="$bin/Hammock Menu.app"
rm -rf "$app/Contents/Library/LoginItems/Hammock Menu.app"
if [ "${EMBED_MENU:-1}" = 0 ]; then
  :
elif [ -x "$menu/Contents/MacOS/Hammock Menu" ]; then
  mkdir -p "$app/Contents/Library/LoginItems"
  cp -R "$menu" "$app/Contents/Library/LoginItems/"
  cat > "$app/Contents/Library/LoginItems/Hammock Menu.app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Hammock Menu</string>
  <key>CFBundleIdentifier</key><string>$id.menu</string>
  <key>CFBundleExecutable</key><string>Hammock Menu</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1.0</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>26.0</string>
  <key>HammockStateDir</key><string>$statedir</string>
  <key>HammockAgentLabel</key><string>$label</string>
</dict>
</plist>
PLIST
else
  echo "warning: $menu not built; no menu bar item (scripts/build-menu-app.sh)" >&2
fi
# The daemon's login item (SMAppService.agent, DaemonService.swift).
mkdir -p "$app/Contents/Library/LaunchAgents"
cat > "$app/Contents/Library/LaunchAgents/$label.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>BundleProgram</key><string>Contents/MacOS/hmkd</string>
  <key>ProgramArguments</key><array><string>hmkd</string></array>
  <key>RunAtLoad</key><true/>
  <!-- Restart after a crash, not a clean exit (hmk stop). -->
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <!-- Pane holders must outlive daemon restarts. -->
  <key>AbandonProcessGroup</key><true/>
  <key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
PLIST
