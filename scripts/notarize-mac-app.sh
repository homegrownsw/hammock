#!/usr/bin/env bash
# Signs a built Hammock.app for sharing (Developer ID, hardened runtime, secure
# timestamps), notarizes it and staples the ticket, leaving a zip beside it:
#   just share   (builds dist/share/Hammock.app, takes the credentials from
#                 a keychain profile or 1Password and runs this; see the justfile)
# Credentials: APPLE_ID and APP_PASSWORD (an app-specific password) in the
# environment, or a notarytool keychain profile named by NOTARY_PROFILE.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
app="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
team="${TEAM_ID:-2G974K78BH}"
identity="${SIGN_IDENTITY:-Developer ID Application}"
if [ -n "${APPLE_ID:-}" ] && [ -n "${APP_PASSWORD:-}" ]; then
  auth=(--apple-id "$APPLE_ID" --password "$APP_PASSWORD" --team-id "$team")
elif [ -n "${NOTARY_PROFILE:-}" ]; then
  auth=(--keychain-profile "$NOTARY_PROFILE")
else
  echo "set APPLE_ID and APP_PASSWORD, or NOTARY_PROFILE" >&2
  exit 2
fi

# Inside out with the hardened runtime and secure timestamps.
"$root/scripts/sign-mac-app.sh" "$app" "$identity" --timestamp

zip="${app%.app}.zip"
rm -f "$zip"
ditto -c -k --keepParent "$app" "$zip"
echo "submitting $(basename "$zip") for notarization…"
out="$(xcrun notarytool submit "$zip" "${auth[@]}" --wait)"
echo "$out"
# notarytool exits 0 for a rejected submission; its status says.
if ! grep -q 'status: Accepted' <<<"$out"; then
  id="$(awk '/^  id:/{print $2; exit}' <<<"$out")"
  [ -n "$id" ] && xcrun notarytool log "$id" "${auth[@]}"
  exit 1
fi
xcrun stapler staple "$app"
rm -f "$zip"
ditto -c -k --keepParent "$app" "$zip"
spctl -a -vv "$app"
echo "$zip"
