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

# A version's summary (release-notes/<platform>/v<ver>.md): every note in <dir>, INTERNAL included, since
# the summary is for the people making the app, under "## New", "## Fixed" and "## Internal" headings, by file
# name within a section, and a section only when it has notes.
render_consolidated_lines() { # render_consolidated_lines <dir>
  local t f heading first=1 items
  for t in NEW FIXED INTERNAL; do
    items=""
    for f in "$1"/*.md; do
      [ -f "$f" ] || continue
      if [ "$(note_tag "$f")" = "$t" ]; then items+="- $(note_body "$f")"$'\n'; fi
    done
    [ -n "$items" ] || continue
    heading=$(tag_heading "$t")
    if [ "$first" = 1 ]; then first=0; else echo; fi
    printf '## %s\n\n%s' "$heading" "$items"
  done
}

# The heading a tag's notes go under: New, Fixed, Internal.
tag_heading() { case "$1" in NEW) echo New ;; FIXED) echo Fixed ;; INTERNAL) echo Internal ;; esac; }

# The texts of the rendered lines ("[NEW] text") with one tag, one per line, without the tag.
lines_with_tag() { # lines_with_tag <TAG> <rendered lines>
  printf '%s\n' "$2" | sed -nE "s/^\[$1\] //p"
}

# Rendered lines as plain text in sections: "New" then its notes as "• text", a blank line, "Fixed" and its
# notes. A section only when it has notes; no trailing blank line.
lines_to_text() { # lines_to_text <rendered lines>
  local t items out=""
  for t in NEW FIXED; do
    items=$(lines_with_tag "$t" "$1")
    [ -n "$items" ] || continue
    [ -z "$out" ] || out+=$'\n\n'
    out+="$(tag_heading "$t")"$'\n'"$(printf '%s\n' "$items" | sed 's/^/• /')"
  done
  printf '%s' "$out"
}

# Cuts text to a limit, ending it with … where it's cut. The file it goes into ends in a newline, which
# counts too.
fit_to_limit() { # fit_to_limit <limit> <text>
  if [ "${#2}" -gt "$(($1 - 1))" ]; then printf '%s\n…' "${2:0:$(($1 - 10))}"; else printf '%s' "$2"; fi
}

html_escape() { sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }

# Rendered lines ("[NEW] text") as HTML for Sparkle's update window (and the GitHub Release): a heading for
# each tag with notes (<h4>New</h4>, <h4>Fixed</h4>), each followed by its list.
lines_to_html() { # lines_to_html <rendered lines>
  local t items
  for t in NEW FIXED; do
    items=$(lines_with_tag "$t" "$1")
    [ -n "$items" ] || continue
    echo "<h4>$(tag_heading "$t")</h4>"
    echo "<ul>"
    printf '%s\n' "$items" | html_escape | sed -E 's|^(.*)$|<li>\1</li>|'
    echo "</ul>"
  done
}

# Each app's version, from apple/iOS/Config/Version.xcconfig: YYYY.N.P, the year, its Nth joint release of both
# apps, and a patch (the Mac's releases between joint ones, or an iPhone hotfix).
version_file() { echo "$1/apple/iOS/Config/Version.xcconfig"; } # version_file <repo-root>
version_key() { case "$1" in mac) echo HAMMOCK_MAC_VERSION ;; ios) echo HAMMOCK_IOS_VERSION ;; esac; }
read_version() { # read_version <repo-root> <mac|ios>
  sed -nE "s/^$(version_key "$2") = ([^ ]+).*$/\1/p" "$(version_file "$1")"
}
write_version() { # write_version <repo-root> <mac|ios> <x.y.z>
  local f
  f=$(version_file "$1")
  sed -i.bak -E "s/^$(version_key "$2") = .*/$(version_key "$2") = $3/" "$f" && rm -f "$f.bak"
  [ "$(read_version "$1" "$2")" = "$3" ] || { echo "Couldn't set the $(label "$2") version in $f." >&2; return 1; }
}

version_ok() { printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; }
version_gt() { [ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -n1)" = "$1" ]; }

# A version's family, the joint release it belongs to: 2026.1.3 → 2026.1.
version_family() { echo "${1%.*}"; }
# The next patch: 2026.1.3 → 2026.1.4.
next_patch() { echo "$(version_family "$1").$((${1##*.} + 1))"; }
# The next joint release after the two apps' versions: this year's next N (2026.1.3 and 2026.1.0 → 2026.2.0), or
# <year>.1.0 in a new year. HMK_RELEASE_YEAR stands in for the year (the tests).
next_joint() { # next_joint <mac version> <ios version>
  local year="${HMK_RELEASE_YEAR:-$(date +%Y)}" n=0 v m
  for v in "$1" "$2"; do
    [ "${v%%.*}" = "$year" ] || continue
    m=${v#*.}; m=${m%%.*}
    if [ "$m" -gt "$n" ]; then n=$m; fi
  done
  echo "$year.$((n + 1)).0"
}
# Whether an app has a release of a version: a tag <Label>-v<version>-r<n>.
is_released() { # is_released <repo-root> <mac|ios> <version>
  [ -n "$(git -C "$1" tag -l "$(label "$2")-v$3-r*")" ]
}
# The note directories of a family's versions, newest first: release-notes/<app>/v2026.1.*/.
family_dirs() { # family_dirs <repo-root> <mac|ios> <family>
  local d
  for d in "$1/release-notes/$2/v$3".*/; do
    if [ -d "$d" ]; then echo "${d%/}"; fi
  done | sort -V -r
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
