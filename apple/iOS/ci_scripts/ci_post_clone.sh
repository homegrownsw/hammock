#!/bin/zsh
# Xcode Cloud: runs after the clone, before each action's xcodebuild. Xcode
# Cloud looks for ci_scripts/ next to the project it builds
# (HammockIOS.xcodeproj, which has both apps: the Hammock and Hammock Mac schemes),
# and only one ci_scripts/ in the repo.
#
# What a fresh clone lacks:
#   iOS and Mac: libghostty-vt's xcframework (git-ignored; HammockKit's
#     GhosttyVt binary target), built from the pinned Ghostty commit with zig.
#   iOS: the push relay's URL in the app's Info.plist (HMK_PUSH_RELAY, a
#     workflow environment variable; unset, the default relay).
#   Mac: what Hammock.app embeds besides its Swift code, in bin/ (hmkd, hmk,
#     pane-holder, the CEF browser host and the simulator host), which the
#     HammockMacApp target's build phase embeds and signs.
#
# Packages resolve from apple/Package.resolved (Xcode Cloud never resolves
# automatically), so a changed dependency must be committed there.
set -euo pipefail

repo="${CI_PRIMARY_REPOSITORY_PATH:?not running in Xcode Cloud}"
platform="${CI_PRODUCT_PLATFORM:-iOS}"
echo "Hammock post-clone: $platform ${CI_XCODEBUILD_ACTION:-?} on ${CI_BRANCH:-${CI_TAG:-?}}"

export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_INSTALL_CLEANUP=1 HOMEBREW_NO_ENV_HINTS=1
need=(zig)
[[ "$platform" == macOS ]] && need+=(go cmake pkgconf xcodegen)
missing=()
for tool in $need; do
  case $tool in pkgconf) command -v pkg-config >/dev/null || missing+=($tool) ;; *) command -v $tool >/dev/null || missing+=($tool) ;; esac
done
(( ${#missing} )) && brew install $missing
echo "zig $(zig version)"

# libghostty-vt, from the pinned commit (scripts/build-libghostty.sh fetches it).
"$repo/scripts/build-libghostty.sh"
want="$(sed -n 's/.*minimum_zig_version = "\(.*\)".*/\1/p' "$repo/third_party/ghostty/src/build.zig.zon")"
if [[ -n "$want" && "$(zig version)" != "$want" ]]; then
  echo "warning: Ghostty wants zig $want, Homebrew has $(zig version)" >&2
fi
rm -rf "$repo/apple/Frameworks/ghostty-vt.xcframework"
mkdir -p "$repo/apple/Frameworks"
cp -R "$repo/third_party/ghostty/out/lib/ghostty-vt.xcframework" "$repo/apple/Frameworks/"

case "$platform" in
  iOS)
    # just ios-device passes the relay as a build setting; Xcode Cloud can't,
    # so it's written into the Info.plist here.
    relay="${HMK_PUSH_RELAY:-https://hmk-push.homegrownsw.workers.dev}"
    /usr/libexec/PlistBuddy -c "Set :HammockPushRelay $relay" "$repo/apple/iOS/Hammock/Info.plist"
    echo "push relay: $relay"
    ;;
  macOS)
    echo "go: $(go version)"
    # hmkd's version (the app restarts a daemon whose version differs from
    # the one it bundles): the commit and Xcode Cloud's build number.
    export HMK_VERSION="$(git -C "$repo" describe --always 2>/dev/null || echo "${CI_COMMIT:0:7}")+xc${CI_BUILD_NUMBER:-0}"
    "$repo/scripts/build-mac-payloads.sh"
    ls "$repo/bin"
    ;;
esac
