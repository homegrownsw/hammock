#!/usr/bin/env bash
# Assembles the Mac app: the SwiftUI client and hmkd in Contents/MacOS; the
# CLI (hammock), pane holder and browser host in Contents/Helpers.
#   VARIANT=prod (default): dist/Hammock.app
#   VARIANT=beta:           dist/Hammock β.app, a dev build with its own
#                           bundle id, icon, login item and state directory
#                           (~/Library/Application Support/Hammock Beta), so
#                           it never touches the prod app's daemon or panes.
#                           In a worktree, dist/Hammock β <branch>.app, with a
#                           state dir and daemon of its own as well
#                           (scripts/beta-identity.sh).
#   UPDATES=1:              a release build that updates itself through
#                           Sparkle from FEED_URL (just release-mac sets it);
#                           β only with an explicit FEED_URL, for testing.
#   BUILD_NUMBER=N:         override CFBundleVersion (testing updates).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
config="${CONFIG:-release}"
variant="${VARIANT:-prod}"
case "$variant" in
  prod) name="Hammock"; dir="Hammock"; id="com.homegrownsw.hammock"; label="com.homegrownsw.hammock.daemon"; icon="AppIcon.icns"; port=7433 ;;
  # The state dir stays ASCII: Network.framework can't open a Unix socket
  # whose path contains "β".
  # β's remote port is its own, so both can take your phone at once.
  beta)
    eval "$("$root/scripts/beta-identity.sh")"
    name="$beta_name"; dir="$beta_dir"; id="com.homegrownsw.hammock.beta"; label="$beta_label"; icon="AppIcon-Beta.icns"; port="$beta_port"
    [ "$beta_menu" = 1 ] || export EMBED_MENU=0 ;;
  *) echo "unknown VARIANT $variant (prod|beta)" >&2; exit 2 ;;
esac
cd "$root/apple"
swift build -c "$config" --product HammockMac
bin="$(swift build -c "$config" --show-bin-path)"

# OUT puts the app elsewhere (a build to share, leaving your own alone).
app="${OUT:-$root/dist}/$name.app"
mkdir -p "$(dirname "$app")"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin/HammockMac" "$app/Contents/MacOS/Hammock"
# Sparkle (updates, HammockUpdates) is a dynamic framework SwiftPM leaves
# beside the binary; Xcode builds embed it themselves.
mkdir -p "$app/Contents/Frameworks"
cp -R "$bin/Sparkle.framework" "$app/Contents/Frameworks/"
otool -l "$app/Contents/MacOS/Hammock" | grep -q '@executable_path/../Frameworks' ||
  install_name_tool -add_rpath @executable_path/../Frameworks "$app/Contents/MacOS/Hammock"
cp "$root/apple/Resources/$icon" "$app/Contents/Resources/AppIcon.icns"
cp "$root/apple/Resources/NeedsYou.caf" "$app/Contents/Resources/"
# Editor panes' page (CodeMirror, built by `just editor-js`).
cp "$root/apple/Resources/editor.html" "$app/Contents/Resources/"
# Third-party software and its licences (Settings ▸ About ▸ Acknowledgements;
# `just acknowledgements` makes it). The Xcode project bundles the same file.
cp "$root/internal/acknowledgements/Acknowledgements.json" "$app/Contents/Resources/"
# The app's accent colour (NSAccentColorName, below): Hammock's forest green,
# which macOS uses while the system accent is left at multicolour.
assets_plist="$(mktemp)"
xcrun actool "$root/apple/Resources/Brand.xcassets" --compile "$app/Contents/Resources" --platform macosx \
  --minimum-deployment-target 26.0 --accent-color AccentColor --output-partial-info-plist "$assets_plist" >/dev/null
rm -f "$assets_plist"
# The menu bar item (bin/Hammock Menu.app), then hmkd, the CLI, the pane
# holder, the browser, simulator and screen hosts and the login items
# (shared with the Xcode project's HammockMac target).
CONFIG="$config" "$root/scripts/build-menu-app.sh" >/dev/null
"$root/scripts/embed-mac-helpers.sh" "$app" "$id" "$label" "$dir"
# Sparkle compares CFBundleVersion, so it only goes up: main's commit number (scripts/commit-number),
# which a release's tag carries too (Mac-v0.2.0-r615 is build 615). The
# version people see is both apps' (apple/iOS/Config/Version.xcconfig).
build="${BUILD_NUMBER:-$("$root/scripts/commit-number" "$root" 2>/dev/null || echo 0)}"
short="$(sed -nE 's/^MARKETING_VERSION = ([^ ]+).*$/\1/p' "$root/apple/iOS/Config/Version.xcconfig")"
# Release builds check FEED_URL for updates, which must be signed with the
# key whose public half is SUPublicEDKey (private half: the login keychain,
# account "fika", backed up in 1Password).
updates=""
if [ "${UPDATES:-}" = 1 ] && { [ "$variant" = prod ] || [ -n "${FEED_URL:-}" ]; }; then
  updates="  <key>SUFeedURL</key><string>${FEED_URL:-https://hammock.jsp.scot/appcast.xml}</string>
  <key>SUPublicEDKey</key><string>ho8jbNLzTzVnl0Zx+EmeH7ROWjPTiYNFUROU5cPOMlw=</string>
  <key>SUEnableAutomaticChecks</key><true/>"
fi
# Only prod claims hammock:// (invite links); β leaves them to it.
url_types=""
if [ "$variant" = prod ]; then
  url_types="  <key>CFBundleURLTypes</key><array><dict>
    <key>CFBundleURLName</key><string>$id</string>
    <key>CFBundleURLSchemes</key><array><string>hammock</string></array>
  </dict></array>"
fi
cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>$name</string>
  <key>CFBundleDisplayName</key><string>$name</string>
  <key>CFBundleIdentifier</key><string>$id</string>
  <key>CFBundleExecutable</key><string>Hammock</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>NSAccentColorName</key><string>AccentColor</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$short</string>
  <key>CFBundleVersion</key><string>$build</string>
  <key>LSMinimumSystemVersion</key><string>26.0</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.developer-tools</string>
  <!-- Remote access on a local network (Settings ▸ Devices ▸ Remote Access), and Bonjour finding
       Hammock on your other computers there (hmkd's _hammock._tcp). -->
  <key>NSLocalNetworkUsageDescription</key><string>Hammock connects to your other computers and your iPhone over your local network, when you choose it for remote access.</string>
  <key>NSBonjourServices</key><array><string>_hammock._tcp</string></array>
  <!-- hammock:// links (invites: hammock://join/…) open the app. -->
$url_types
$updates
  <!-- Read by the app, hmkd and hmk to find this variant's daemon. -->
  <key>HammockStateDir</key><string>$dir</string>
  <key>HammockAgentLabel</key><string>$label</string>
  <key>HammockRemotePort</key><string>$port</string>
</dict>
</plist>
PLIST
# A stable signing identity keeps macOS privacy grants (Files & Folders, etc.)
# across rebuilds; ad-hoc signatures are a new identity every build. Set
# SIGN_IDENTITY or put a codesigning identity (name or SHA-1) in
# .signing-identity (gitignored); otherwise builds are ad-hoc signed.
# Signed inside out with the hardened runtime, as shared builds are.
identity="${SIGN_IDENTITY:-$(cat "$root/.signing-identity" 2>/dev/null || true)}"
"$root/scripts/sign-mac-app.sh" "$app" "${identity:--}" --timestamp=none
echo "$app"
