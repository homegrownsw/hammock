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
  # Not --password on notarytool's command line, where ps shows it to every
  # process on the Mac: the password goes, on stdin, into a keychain made
  # for this run and deleted after it, and notarytool reads it from there.
  # notarytool asks for a password it isn't given on the terminal, if it has
  # one: run without a controlling terminal (perl's setsid; macOS has no
  # setsid command), it reads stdin instead. printf is bash's own, not a
  # process.
  scratch="$(mktemp -d)"
  keychain="$scratch/notary.keychain-db"
  trap 'security delete-keychain "$keychain" 2>/dev/null; rm -rf "$scratch"' EXIT
  # The throwaway keychain's own password is random and never reused.
  security create-keychain -p "$(uuidgen)" "$keychain"
  security set-keychain-settings "$keychain" # no auto-lock while notarizing
  printf '%s\n' "$APP_PASSWORD" | perl -MPOSIX -e '
    my $pid = fork // die "fork: $!";
    if ($pid) { waitpid($pid, 0); exit($? & 127 ? 1 : $? >> 8) }
    POSIX::setsid() // die "setsid: $!";
    exec @ARGV or die "exec: $!"' \
    xcrun notarytool store-credentials hammock-notarize --apple-id "$APPLE_ID" --team-id "$team" \
      --keychain "$keychain" --no-validate >/dev/null
  auth=(--keychain-profile hammock-notarize --keychain "$keychain")
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
