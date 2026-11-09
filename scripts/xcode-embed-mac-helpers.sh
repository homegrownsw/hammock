#!/usr/bin/env bash
# The Mac app target's "Embed and sign helpers" build phase (Xcode builds:
# Xcode Cloud, archiving): puts hmkd, the CLI, the pane holder and the
# browser and simulator hosts from bin/ (scripts/build-mac-payloads.sh) into
# the app, and signs them inside out, before Xcode signs the app itself.
# Reads Xcode's build environment.
set -euo pipefail
root="$SRCROOT/../.."
app="$TARGET_BUILD_DIR/$WRAPPER_NAME"
"$root/scripts/embed-mac-helpers.sh" "$app" "$PRODUCT_BUNDLE_IDENTIFIER" com.homegrownsw.hammock.daemon
# Signed with the identity Xcode signs the app with (ad hoc when signing's
# off), timestamped only for archives.
identity="${EXPANDED_CODE_SIGN_IDENTITY:--}"
[ "${CODE_SIGNING_ALLOWED:-YES}" = NO ] && identity=-
if [ "$ACTION" = install ]; then ts=--timestamp; else ts=--timestamp=none; fi
NESTED_ONLY=1 "$root/scripts/sign-mac-app.sh" "$app" "$identity" "$ts"
