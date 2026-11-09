#!/usr/bin/env bash
# Builds the menu bar item (apple/Sources/HammockMenu, ADR-0014) into
# bin/Hammock Menu.app, which embed-mac-helpers.sh puts in Hammock.app's
# Contents/Library/LoginItems with its variant's Info.plist.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
config="${CONFIG:-release}"
cd "$root/apple"
swift build -c "$config" --product HammockMenu
out="$(swift build -c "$config" --show-bin-path)"
app="$root/bin/Hammock Menu.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS"
cp "$out/HammockMenu" "$app/Contents/MacOS/Hammock Menu"
echo "$app"
