# GitHub Releases that survive GitHub's bad moments. Sourced by scripts/mac-update and scripts/android-apk;
# not executable on its own. Tested by scripts/tests/gh-release.
#
# GitHub answers 500 or 502 now and then, even when its status page says all's well, and an upload's 5xx is
# often false: the file landed whole. So nothing here trusts an exit status on its own:
#
#   gh_retry <gh args…>                     gh, again (with backoff) when it fails with a 5xx, a 429 or a
#                                           network error; any other failure (a 4xx, "release not found")
#                                           at once
#   gh_release_exists <repo> <tag>          0 if the release (a draft too) is there, 1 if GitHub says it
#                                           isn't, 2 if GitHub couldn't say
#   gh_release_ensure <repo> <tag> <create args…>
#                                           create the release (with no files) unless it's there; a create
#                                           that fails is checked for before it's tried again, so a false
#                                           failure doesn't make a second draft
#   gh_release_upload <repo> <tag> <file…>  each file onto the release, under its own name, one at a time:
#                                           only those the release doesn't already have
#
# gh_release_upload decides from the release's own list of assets, never from an upload's exit status: a file
# is up when an asset of its name has state "uploaded" and its SHA-256 (the asset's digest; its size, for
# an asset from before GitHub kept digests). It reads the list, uploads each file that isn't up (deleting a
# half-made or different asset of its name first), and reads it again; after a failure it waits, longer
# each time. After HMK_GH_ATTEMPTS passes (5) it gives up, naming the files that aren't up. So running it
# again, or the script that called it, sends only what's missing.
#
# HMK_GH_RETRY_DELAY is the first wait, in seconds (5; doubled each time); the tests set it to 0.

# shellcheck shell=bash

GH_ATTEMPTS="${HMK_GH_ATTEMPTS:-5}"
GH_RETRY_DELAY="${HMK_GH_RETRY_DELAY:-5}"

# Whether gh's error output (a file) is a failure worth trying again: GitHub's 5xx (gh prints "HTTP 502: …"),
# a rate limit, or the network.
_gh_transient() {
  grep -Eqi 'HTTP (5[0-9][0-9]|429)|50[0-9] (Internal|Bad Gateway|Service Unavailable|Gateway Time)|Error creating policy|timed? ?out|connection (reset|refused)|unexpected EOF|TLS handshake|no such host|secondary rate limit' "$1"
}

# The wait before attempt try+1: GH_RETRY_DELAY, doubled each time.
_gh_wait() { sleep "$((GH_RETRY_DELAY * (1 << ($1 - 1))))"; }

gh_retry() {
  local try=1 out rc err
  err="$(mktemp)"
  while :; do
    if out="$(gh "$@" 2>"$err")"; then
      rm -f "$err"
      [ -z "$out" ] || printf '%s\n' "$out"
      return 0
    else
      rc=$?
    fi
    if [ "$try" -ge "$GH_ATTEMPTS" ] || ! _gh_transient "$err"; then
      cat "$err" >&2
      rm -f "$err"
      return "$rc"
    fi
    echo "GitHub: $(tr '\n' ' ' <"$err")— trying again (gh $1 $2, attempt $((try + 1)) of $GH_ATTEMPTS)." >&2
    _gh_wait "$try"
    try=$((try + 1))
  done
}

gh_release_exists() {
  local repo="$1" tag="$2" try=1 err
  err="$(mktemp)"
  while :; do
    if gh release view "$tag" --repo "$repo" --json tagName >/dev/null 2>"$err"; then
      rm -f "$err"
      return 0
    fi
    if grep -q 'release not found' "$err"; then
      rm -f "$err"
      return 1
    fi
    if [ "$try" -ge "$GH_ATTEMPTS" ] || ! _gh_transient "$err"; then
      echo "Couldn't tell whether $repo has a release $tag:" >&2
      cat "$err" >&2
      rm -f "$err"
      return 2
    fi
    _gh_wait "$try"
    try=$((try + 1))
  done
}

gh_release_ensure() {
  local repo="$1" tag="$2" try=1 rc
  shift 2
  while :; do
    gh_release_exists "$repo" "$tag" && return 0
    rc=$?
    [ "$rc" -eq 1 ] || return 1
    if [ "$try" -gt "$GH_ATTEMPTS" ]; then
      echo "Couldn't create $repo's release $tag." >&2
      return 1
    fi
    [ "$try" -eq 1 ] || _gh_wait "$((try - 1))"
    # Not gh_retry: a create that failed may still have made it, so it's looked for again first.
    gh release create "$tag" --repo "$repo" "$@" >/dev/null || true
    try=$((try + 1))
  done
}

# The release's assets as lines of name, state, digest (or "-") and size, tab-separated.
_gh_assets() {
  gh_retry release view "$2" --repo "$1" --json assets \
    -q '.assets[] | [.name, .state, (if (.digest // "") == "" then "-" else .digest end), (.size | tostring)] | @tsv'
}

# What a file's asset needs, given the asset lines: "up" (nothing), "missing" (an upload) or "replace" (the
# asset deleted, then an upload).
_gh_asset_status() { # <file> <assets>
  local f="$1" name line state digest size
  name="$(basename "$f")"
  line="$(awk -F'\t' -v n="$name" '$1 == n { print; exit }' <<<"$2")"
  [ -n "$line" ] || { echo missing; return; }
  IFS=$'\t' read -r _ state digest size <<<"$line"
  if [ "$state" = uploaded ]; then
    if [ "$digest" != "-" ]; then
      [ "$digest" = "sha256:$(shasum -a 256 "$f" | cut -d' ' -f1)" ] && { echo up; return; }
    else
      [ "$size" = "$(wc -c <"$f" | tr -d ' ')" ] && { echo up; return; }
    fi
  fi
  echo replace
}

gh_release_upload() {
  local repo="$1" tag="$2" pass=0 assets f status todo
  shift 2
  while :; do
    assets="$(_gh_assets "$repo" "$tag")" || { echo "Couldn't list the assets of $repo's release $tag." >&2; return 1; }
    todo=()
    for f in "$@"; do
      status="$(_gh_asset_status "$f" "$assets")"
      [ "$status" = up ] || todo+=("$status:$f")
    done
    [ "${#todo[@]}" -gt 0 ] || return 0
    if [ "$pass" -ge "$GH_ATTEMPTS" ]; then
      echo "Gave up after $pass tries: $repo's release $tag is missing (or has a broken copy of):" >&2
      for f in "${todo[@]}"; do echo "  $(basename "${f#*:}")" >&2; done
      return 1
    fi
    [ "$pass" -eq 0 ] || _gh_wait "$pass"
    pass=$((pass + 1))
    for f in "${todo[@]}"; do
      status="${f%%:*}" f="${f#*:}"
      if [ "$status" = replace ]; then
        echo "$(basename "$f"): the release's copy is incomplete or different; replacing it." >&2
        gh_retry release delete-asset "$tag" "$(basename "$f")" --repo "$repo" --yes >/dev/null || continue
      fi
      echo "uploading $(basename "$f")…"
      # Its exit status means little (a 5xx often comes after the file landed): the next read decides.
      gh release upload "$tag" --repo "$repo" "$f" || true
    done
  done
}
