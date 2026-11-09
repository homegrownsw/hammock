# Shared helpers for reading release notes, comparing versions and checking where the checkout stands.
# Sourced by scripts/note, scripts/note-check and scripts/release; not executable on its own. Shared with
# the release machinery of the owner's other apps.
#
# A note file is (release-notes/README.md):
#   tag: NEW|FIXED|INTERNAL
#   date: YYYY-MM-DD
#   <the text, one or more lines>

# shellcheck shell=bash

# The platforms with notes, and the name each goes by in tags, commits and summaries.
PLATFORMS_ALL="mac ios"
label() { case "$1" in mac) echo Mac ;; ios) echo iOS ;; esac; }

note_tag() { head -n1 "$1" | sed -E 's/^tag:[[:space:]]*//' | tr '[:lower:]' '[:upper:]'; }
note_body() { tail -n +2 "$1" | sed -E '/^date:[[:space:]]*[0-9]{4}-[0-9]{2}-[0-9]{2}$/d' | tr '\n' ' ' | sed -E 's/[[:space:]]+/ /g; s/^ +//; s/ +$//'; }

# Prints "[NEW] text" or "[FIXED] text", and nothing for INTERNAL or an empty note. INTERNAL is dropped here
# rather than by each caller, so nothing people see can carry one by accident.
render_visible() {
  local t body
  t=$(note_tag "$1")
  case "$t" in NEW | FIXED) ;; *) return 0 ;; esac
  body=$(note_body "$1")
  # `if`, not `[ -n ] &&`: as the last command, a false test would make the function return 1, and callers
  # use it in a command substitution, which `set -e` honours, so an empty NEW note would stop a release.
  if [ -n "$body" ]; then printf '[%s] %s\n' "$t" "$body"; fi
}

# Reorders rendered lines so [NEW] sits above [FIXED].
new_first() {
  printf '%s' "$1" | grep '^\[NEW\] ' || true
  printf '%s' "$1" | grep '^\[FIXED\] ' || true
}

# The visible notes in a directory, rendered and NEW first, one per line (no trailing newline).
render_dir() { # render_dir <dir>
  local f line lines=""
  for f in "$1"/*.md; do
    [ -f "$f" ] || continue
    line=$(render_visible "$f")
    if [ -n "$line" ]; then lines+="$line"$'\n'; fi
  done
  new_first "$lines"
}

# The lines of a version's summary (release-notes/<platform>/v<ver>.md): every note in <dir>, INTERNAL
# included, since the summary is for the people making the app, as "- [TAG] text", NEW then FIXED then
# INTERNAL, and by file name within a tag.
render_consolidated_lines() { # render_consolidated_lines <dir>
  local t f
  for t in NEW FIXED INTERNAL; do
    for f in "$1"/*.md; do
      [ -f "$f" ] || continue
      if [ "$(note_tag "$f")" = "$t" ]; then
        printf -- '- [%s] %s\n' "$t" "$(note_body "$f")"
      fi
    done
  done
}

# Cuts text to a limit, ending it with … where it's cut. The file it goes into ends in a newline, which
# counts too.
fit_to_limit() { # fit_to_limit <limit> <text>
  if [ "${#2}" -gt "$(($1 - 1))" ]; then printf '%s\n…' "${2:0:$(($1 - 10))}"; else printf '%s' "$2"; fi
}

html_escape() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

# Rendered lines ("[NEW] text") as an HTML list with each tag as a badge, for Sparkle's update window.
lines_to_html() { # lines_to_html <rendered lines>
  [ -n "$1" ] || return 0
  echo "<ul>"
  printf '%s\n' "$1" | html_escape |
    sed -E 's|^\[(NEW\|FIXED)\] (.*)$|<li><b>\1</b> \2</li>|'
  echo "</ul>"
}

# The version, from apple/iOS/Config/Version.xcconfig, which every target includes.
version_file() { echo "$1/apple/iOS/Config/Version.xcconfig"; } # version_file <repo-root>
read_version() { sed -nE 's/^MARKETING_VERSION = ([^ ]+).*$/\1/p' "$(version_file "$1")"; }
write_version() { # write_version <repo-root> <x.y.z>
  local f
  f=$(version_file "$1")
  sed -i.bak -E "s/^MARKETING_VERSION = .*/MARKETING_VERSION = $2/" "$f" && rm -f "$f.bak"
  [ "$(read_version "$1")" = "$2" ] || { echo "Couldn't set the version in $f." >&2; return 1; }
}

version_ok() { printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; }
version_gt() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1)" = "$1" ]; }

# The next minor version (0.2.3 → 0.3.0): only a suggestion for the interactive prompt.
suggest_next() {
  local maj rest min
  maj=${1%%.*}; rest=${1#*.}; min=${rest%%.*}
  echo "${maj}.$((min + 1)).0"
}

# The GitHub issue a branch is for, from branch names shaped <name>/<n>-<slug> (alex/44-updates → 44).
# Prints nothing when the branch doesn't match.
infer_issue() { # infer_issue <repo-root>
  git -C "$1" rev-parse --abbrev-ref HEAD 2>/dev/null |
    sed -nE 's|^[^/]+/([0-9]+)-.+$|\1|p'
}

# Preflight for the scripts that commit and push a release straight to main.
#
# A release is cut from a clean checkout exactly level with origin/main, because the commit and tag are made
# before the push: if the push were rejected, the script would stop half-done with a tagged release that was
# never published, and the next run would count that tag. Behind would cut stale code with stale notes;
# ahead would sweep unreviewed work into the release.
#
# It checks the commit, not the branch name, so a detached HEAD passes when it's on origin/main. Another
# named branch is refused even when level, because the commit would move that branch. HMK_RELEASE_BRANCH
# names a worktree's branch to allow (it must still be level with origin/main): the release commit is then
# pushed as HEAD:main, and the branch is left a commit or two ahead, as if main had been merged into it.
require_level_with_origin_main() { # require_level_with_origin_main <repo-root>
  local root="$1" branch counts behind ahead
  branch=$(git -C "$root" rev-parse --abbrev-ref HEAD)
  if [ "$branch" != main ] && [ "$branch" != HEAD ] && [ "$branch" != "${HMK_RELEASE_BRANCH:-}" ]; then
    echo "Releases are cut from main (this checkout is on '$branch'; HMK_RELEASE_BRANCH=$branch allows it)." >&2
    return 1
  fi
  if [ -n "$(git -C "$root" status --porcelain)" ]; then
    echo "The working tree isn't clean; commit or stash first." >&2
    return 1
  fi
  git -C "$root" fetch --quiet origin main
  git -C "$root" fetch --tags --quiet
  git -C "$root" rev-parse -q --verify refs/remotes/origin/main >/dev/null ||
    { echo "No origin/main to compare with; check the remote." >&2; return 1; }
  counts=$(git -C "$root" rev-list --left-right --count origin/main...HEAD)
  behind=${counts%%[[:space:]]*}
  ahead=${counts##*[[:space:]]}
  if [ "$behind" -eq 0 ] && [ "$ahead" -eq 0 ]; then return 0; fi
  if [ "$behind" -ne 0 ] && [ "$ahead" -ne 0 ]; then
    echo "HEAD has diverged from origin/main (${behind} behind, ${ahead} ahead)." >&2
  elif [ "$behind" -ne 0 ]; then
    echo "HEAD is ${behind} commit(s) behind origin/main: a release now would ship stale code and notes." >&2
  else
    echo "HEAD is ${ahead} commit(s) ahead of origin/main: push them first, so the release is reviewed code." >&2
  fi
  echo "Update first:  git pull --rebase origin main" >&2
  return 1
}
