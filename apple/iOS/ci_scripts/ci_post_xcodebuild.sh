#!/bin/zsh
# Xcode Cloud: runs after each action's xcodebuild (even a failed one).
# For the Mac with the Notarize post-action (which sets
# CI_DEVELOPER_ID_SIGNED_APP_PATH), checks that the Developer ID export
# re-signed everything Hammock.app embeds and kept each helper's entitlements:
# a browser renderer without JIT, say, only fails when a page opens.
set -euo pipefail

[[ "${CI_PRODUCT_PLATFORM:-}" == macOS && -n "${CI_DEVELOPER_ID_SIGNED_APP_PATH:-}" ]] || exit 0
# No export to check (the Developer ID export itself failed, and says why
# in its own log): nothing to add here.
if [[ ! -d "$CI_DEVELOPER_ID_SIGNED_APP_PATH" ]]; then
  echo "no Developer ID export at $CI_DEVELOPER_ID_SIGNED_APP_PATH (see developer-id-export-archive-logs); nothing to check"
  exit 0
fi
app="$CI_DEVELOPER_ID_SIGNED_APP_PATH/Hammock.app"
[[ -d "$app" ]] || app="$(find "$CI_DEVELOPER_ID_SIGNED_APP_PATH" -maxdepth 2 -name '*.app' -print -quit)"
echo "checking $app"
codesign --verify --deep --strict "$app"

fail=0
check() { # path-inside-app [entitlement…]
  local p="$app$1" name="${1#/}" e info ents
  shift
  # Captured, not piped into grep -q: with pipefail, grep stopping early
  # fails the pipeline.
  info="$(codesign -dvv "$p" 2>&1 || true)"
  ents="$(codesign -d --entitlements - --xml "$p" 2>/dev/null || true)"
  [[ "$info" == *"Authority=Developer ID Application"* ]] ||
    { echo "error: ${name:-Hammock.app} isn't Developer ID signed" >&2; fail=1; }
  [[ "$info" == *"(runtime)"* ]] ||
    { echo "error: ${name:-Hammock.app} hasn't the hardened runtime" >&2; fail=1; }
  for e in "$@"; do
    [[ "$ents" == *"<key>$e</key>"* ]] ||
      { echo "error: ${name:-Hammock.app} lost $e" >&2; fail=1; }
  done
}
h="/Contents/Helpers/Hammock Browser.app/Contents/Frameworks/Hammock Browser Helper"
check ""
check /Contents/MacOS/hmkd
check /Contents/Helpers/hmk
check "/Contents/Helpers/Hammock Pane.app"
check "/Contents/Helpers/Hammock Simulator.app" com.apple.security.cs.disable-library-validation
check "/Contents/Helpers/Hammock Browser.app"
check "$h (Renderer).app" com.apple.security.cs.allow-jit
check "$h (GPU).app" com.apple.security.cs.allow-unsigned-executable-memory
check "$h (Plugin).app" com.apple.security.cs.allow-unsigned-executable-memory com.apple.security.cs.disable-library-validation
(( fail == 0 )) && echo "Hammock.app's helpers are signed as they should be"
exit $fail
