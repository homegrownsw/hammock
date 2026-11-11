#!/usr/bin/env bash
# Signs a built Hammock.app inside out with the hardened runtime, giving each
# helper only the entitlements it needs:
#   scripts/sign-mac-app.sh APP IDENTITY [codesign options…]
# IDENTITY "-" signs ad hoc. Extra options (--timestamp, --timestamp=none)
# go to every codesign call. Used by build-mac-app.sh and
# notarize-mac-app.sh.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
app="$1"
identity="$2"
shift 2
opts=("$@")
ent="$root/scripts/entitlements"

sign() {
  codesign --force --options runtime ${opts[@]+"${opts[@]}"} --sign "$identity" "$@" 2>&1 |
    { grep -v 'replacing existing signature' || true; } >&2
  return "${PIPESTATUS[0]}"
}

# Entitlements for code that loads the Chromium framework (an entitlements
# file in scripts/entitlements, or "" for none). Library validation only
# lets a hardened process load code with its own Team ID, and an ad-hoc
# signature has none, so ad-hoc builds also turn library validation off
# there; a real identity keeps it.
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cef_ent() { # name-or-empty -> path (empty: none)
  local src="${1:+$ent/$1.plist}" out
  if [ "$identity" != "-" ]; then
    echo "$src"
    return
  fi
  out="$tmp/${1:-none}.plist"
  if [ -n "$src" ]; then cp "$src" "$out"; else plutil -create xml1 "$out"; fi
  /usr/libexec/PlistBuddy -c 'Delete :com.apple.security.cs.disable-library-validation' "$out" >/dev/null 2>&1 || true
  /usr/libexec/PlistBuddy -c 'Add :com.apple.security.cs.disable-library-validation bool true' "$out" >/dev/null || return 1
  echo "$out"
}
sign_cef() { # name-or-empty bundle
  local e
  e="$(cef_ent "$1")" || exit 1
  if [ -n "$e" ]; then sign --entitlements "$e" "$2"; else sign "$2"; fi
}

# Browser panes' host: the Chromium framework's libraries, then the
# framework, then each helper and the host. Entitlements per helper, as
# Chromium signs its own: JIT for the renderer, unsigned executable memory
# for the GPU process, and only the plugin helper loads code signed by
# others. The host and the other helpers get none.
sign_browser() { # path to Hammock Browser.app
  local b="$1" fw h
  fw="$b/Contents/Frameworks/Chromium Embedded Framework.framework"
  find "$fw" -type f \( -name '*.dylib' -o -name '*.so' \) -print0 |
    while IFS= read -r -d '' f; do sign "$f"; done
  sign "$fw"
  h="$b/Contents/Frameworks/Hammock Browser Helper"
  sign_cef browser-renderer "$h (Renderer).app"
  sign_cef browser-gpu "$h (GPU).app"
  sign_cef browser-plugin "$h (Plugin).app"
  sign_cef "" "$h.app"
  sign_cef "" "$b"
}

# Code the simulator host keeps in its Resources, to install into
# simulators (the XCTest runner's apps and test bundle, the guest helper):
# signing the host doesn't reach it, and notarization needs every binary
# signed with a secure timestamp. Inside out: loose Mach-O files (dylibs,
# helpers, frameworks' binaries) first, then the bundles, deepest first.
sign_sim_resources() { # path to Hammock Simulator.app
  local res="$1/Contents/Resources" f
  [ -d "$res" ] || return 0
  find "$res" -type f -print0 | while IFS= read -r -d '' f; do
    case "$(file -b "$f")" in *Mach-O*) ;; *) continue ;; esac
    # The guest helper runs inside simulators, which need it to take their DYLD_* variables (sim-guest.plist).
    if [ "$(basename "$f")" = hmk-sim-guest ]; then sign --entitlements "$ent/sim-guest.plist" "$f"; else sign "$f"; fi
  done
  find "$res" -type d \( -name '*.app' -o -name '*.xctest' -o -name '*.framework' -o -name '*.appex' \) -print0 |
    while IFS= read -r -d '' f; do printf '%s\t%s\0' "$(tr -cd / <<<"$f" | wc -c)" "$f"; done |
    sort -z -rn | while IFS=$'\t' read -r -d '' _ f; do sign "$f"; done
}

[ -d "$app/Contents/Helpers/Hammock Browser.app" ] && sign_browser "$app/Contents/Helpers/Hammock Browser.app"
[ -d "$app/Contents/Helpers/Hammock Simulator.app" ] && sign_sim_resources "$app/Contents/Helpers/Hammock Simulator.app"
[ -d "$app/Contents/Helpers/Hammock Simulator.app" ] && sign --entitlements "$ent/simulator.plist" "$app/Contents/Helpers/Hammock Simulator.app"
[ -d "$app/Contents/Helpers/Hammock Screen.app" ] && sign "$app/Contents/Helpers/Hammock Screen.app"
# The Hammock Key's helper: its keychain item trusts this signature, so keep
# the identity stable (an ad-hoc one changes with every build: macOS then
# asks before it may read its own key).
[ -d "$app/Contents/Helpers/Hammock Key.app" ] && sign "$app/Contents/Helpers/Hammock Key.app"
[ -d "$app/Contents/Library/LoginItems/Hammock Menu.app" ] && sign "$app/Contents/Library/LoginItems/Hammock Menu.app"
# Sparkle (updates), as its docs sign it: XPC services, installer and UI,
# then the framework.
sparkle="$app/Contents/Frameworks/Sparkle.framework"
if [ -d "$sparkle" ]; then
  sign "$sparkle/Versions/B/XPCServices/Installer.xpc"
  sign --preserve-metadata=entitlements "$sparkle/Versions/B/XPCServices/Downloader.xpc"
  sign "$sparkle/Versions/B/Autoupdate"
  sign "$sparkle/Versions/B/Updater.app"
  sign "$sparkle"
fi
sign "$app/Contents/Helpers/Hammock Pane.app"
sign "$app/Contents/Helpers/hmk"
sign "$app/Contents/MacOS/hmkd"
# NESTED_ONLY=1: everything but the app itself, which Xcode signs after its
# build phases (the HammockMacApp target in apple/iOS/HammockIOS.xcodeproj).
[ "${NESTED_ONLY:-}" = 1 ] && exit 0
# Ad hoc, the app can only load Sparkle with library validation off (as
# for Chromium above); a real identity keeps it.
sign_cef "" "$app"
codesign --verify --deep --strict "$app"
