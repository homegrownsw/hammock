#!/bin/bash
# Moves this Mac from Fika to Hammock, keeping your Groups, tabs, running
# panes, schedules, settings, linked computers and Hammock Key (the Fika
# Key, renamed). See docs/migrate-from-fika.md for when and where to run it.
#
#   scripts/migrate-from-fika.sh            show what it would do (changes nothing)
#   scripts/migrate-from-fika.sh --go       do it
#
# Options:
#   --hammock PATH   Hammock.app to switch to (default: /Applications/Hammock.app,
#                    else the one Spotlight finds)
#   --fika PATH      the Fika.app being replaced (default: the one the fika
#                    command or the daemon's login item points at)
#   --trash-fika     move Fika.app to the Trash at the end (not one in a git checkout)
#   --reset-old-permissions
#                    forget the privacy permissions macOS gave Fika (tccutil)
#   --from-pane      allow running inside a Fika pane (the pane should
#                    survive, but run it from Terminal if you can)
#
# Prod only: β builds (Fika β) aren't migrated. Their state ("Fika Beta…")
# is left alone; delete it once you no longer need it.
set -euo pipefail

go=0 trash=0 reset_tcc=0 from_pane=0 hammock_app="" fika_app=""
while [ $# -gt 0 ]; do
  case "$1" in
    --go) go=1 ;;
    --hammock) hammock_app="$2"; shift ;;
    --fika) fika_app="$2"; shift ;;
    --trash-fika) trash=1 ;;
    --reset-old-permissions) reset_tcc=1 ;;
    --from-pane) from_pane=1 ;;
    -h|--help) sed -n '2,21p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown option: $1 (see --help)" >&2; exit 2 ;;
  esac
  shift
done

uid=$(id -u)
support="$HOME/Library/Application Support"
old_dir="$support/Fika" new_dir="$support/Hammock"
old_cfg="$HOME/.config/fika" new_cfg="$HOME/.config/hammock"
old_cache="$HOME/Library/Caches/fika" new_cache="$HOME/Library/Caches/hammock"
old_id=com.homegrownsw.fika new_id=com.homegrownsw.hammock

bold() { printf '\n\033[1m%s\033[0m\n' "$*"; }
note() { printf '  %s\n' "$*"; }
warn() { printf '  \033[33m! %s\033[0m\n' "$*"; }
die() { printf '\033[31merror: %s\033[0m\n' "$*" >&2; exit 1; }
# Runs a step, or only prints it in a dry run.
run() { if [ $go = 1 ]; then "$@"; else printf '  would: %s\n' "$*"; fi; }

# --- Finding things -----------------------------------------------------------

bundle_id() { /usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$1/Contents/Info.plist" 2>/dev/null || true; }

if [ -z "$fika_app" ]; then
  for link in /opt/homebrew/bin/fika /usr/local/bin/fika "$HOME/.local/bin/fika"; do
    if [ -L "$link" ]; then
      t=$(readlink "$link"); t=${t%/Contents/Helpers/fika}
      [ "$(bundle_id "$t")" = $old_id ] && { fika_app="$t"; break; }
    fi
  done
fi
if [ -z "$fika_app" ]; then
  t=$(launchctl print "gui/$uid/$old_id.daemon" 2>/dev/null | sed -nE 's/^[[:space:]]*program = (.*)\/Contents\/MacOS\/fikad$/\1/p' | head -1)
  [ -n "$t" ] && fika_app="$t"
fi
if [ -z "$fika_app" ]; then
  for t in /Applications/Fika.app "$HOME/Applications/Fika.app" $(mdfind "kMDItemCFBundleIdentifier == '$old_id'" 2>/dev/null); do
    [ "$(bundle_id "$t")" = $old_id ] && { fika_app="$t"; break; }
  done
fi
if [ -z "$hammock_app" ]; then
  for t in /Applications/Hammock.app "$HOME/Applications/Hammock.app" $(mdfind "kMDItemCFBundleIdentifier == '$new_id'" 2>/dev/null); do
    [ "$(bundle_id "$t")" = $new_id ] && { hammock_app="$t"; break; }
  done
fi

bold "Fika → Hammock on $(scutil --get ComputerName 2>/dev/null || hostname)$([ $go = 1 ] || echo ' (dry run: nothing changes; --go to do it)')"

[ -n "$hammock_app" ] && [ "$(bundle_id "$hammock_app")" = $new_id ] ||
  die "no Hammock.app found: install it first (or pass --hammock PATH)"
[ -x "$hammock_app/Contents/Helpers/hmk" ] || die "$hammock_app has no Contents/Helpers/hmk"
note "Hammock: $hammock_app"
old_cli=""
if [ -n "$fika_app" ] && [ -x "$fika_app/Contents/Helpers/fika" ]; then
  old_cli="$fika_app/Contents/Helpers/fika"
  note "Fika:    $fika_app"
else
  warn "no Fika.app found: steps that need its fika command (hooks, group folders) are skipped"
fi

if [ -n "${FIKA_PANE_ID:-}" ] && [ $from_pane = 0 ]; then
  die "this is a Fika pane. Run it from Terminal (or pass --from-pane: the pane should carry on in Hammock)"
fi
if [ -e "$new_dir/state.db" ]; then
  die "Hammock already has state ($new_dir): it's been run on this Mac. Quit it and move that folder aside first if you want Fika's state instead"
fi
[ -d "$old_dir" ] || warn "no Fika state ($old_dir): nothing to bring across but the settings below"

# The old CLI, aimed at prod's daemon even from inside a pane.
fika() { env -u FIKA_SOCKET -u FIKA_HOOK_SOCKET -u FIKA_PANE_ID -u FIKA_DIR "$old_cli" "$@"; }
fika_up() { [ -n "$old_cli" ] && [ -S "$old_dir/run/fikad.sock" ] && fika ls --json >/dev/null 2>&1; }

# --- 1. What Fika has now ------------------------------------------------------

bold "1. Reading Fika's Groups"
folders=() panes_before=""
if fika_up; then
  json=$(fika ls --json)
  panes_before=$(jq '[.groups[].tabs[]?.panes[]?] | length' <<<"$json" 2>/dev/null || echo "")
  note "$(jq '.groups | length' <<<"$json") Groups${panes_before:+, $panes_before panes}"
  while read -r gid; do
    f=$(fika group folder "$gid" 2>/dev/null || true)
    [ -n "$f" ] && [ -d "$f/.fika" ] && folders+=("$f")
  done < <(jq -r '.groups[].id' <<<"$json")
  [ ${#folders[@]} -gt 0 ] && note "Group folders with a .fika icon folder: ${folders[*]}"
else
  warn "Fika's daemon isn't running: Group icon folders (.fika) won't be renamed for you"
fi

# --- 2. Agent hooks and MCP ----------------------------------------------------

bold "2. Removing Fika's agent hooks and MCP server (Claude Code, Codex)"
if [ -n "$old_cli" ]; then
  run fika hooks install --uninstall
  # Fika's uninstall leaves its MCP server in Codex's config.toml.
  if grep -q '^\[mcp_servers\.fika\]' "$HOME/.codex/config.toml" 2>/dev/null; then
    run perl -0pi -e 's/\[mcp_servers\.fika\]\n(?:(?!\[)[^\n]*\n)*\n?//' "$HOME/.codex/config.toml"
  fi
else
  warn "skipped (no fika command): remove them by hand, see the guide"
fi

# --- 3. Stopping Fika ----------------------------------------------------------

bold "3. Quitting Fika and stopping its daemon (panes keep running)"
quit_app() {
  osascript -e "if application id \"$1\" is running then tell application id \"$1\" to quit" >/dev/null 2>&1 || true
}
stop_fika() {
  quit_app "$old_id.menu"
  quit_app "$old_id"
  for _ in $(seq 20); do pgrep -f "/Contents/MacOS/Fika\$" >/dev/null || break; sleep 0.5; done
  [ -n "$old_cli" ] && fika stop || true
  # The login item (KeepAlive in older builds) would start it again.
  launchctl bootout "gui/$uid/$old_id.daemon" 2>/dev/null || true
  for _ in $(seq 20); do pgrep -f "$fika_app/Contents/MacOS/fikad" >/dev/null || break; sleep 0.5; done
  pgrep -f "$fika_app/Contents/MacOS/fikad" >/dev/null &&
    die "Fika's daemon is still running (pid $(pgrep -f "$fika_app/Contents/MacOS/fikad" | tr '\n' ' ')): quit it and run this again"
  # Its hosts outlive it (the panes' holders must: Hammock picks them up). The
  # browser host especially has to go: it holds the browser profile, and
  # Chromium hands a second launch on that profile to the running one, which
  # opens a window for it, so Hammock's browser could never start.
  for host in "Fika Browser" "Fika Screen" "Fika Simulator"; do
    pkill -TERM -f "$fika_app/Contents/Helpers/$host.app/Contents/MacOS/$host" 2>/dev/null || true
  done
  for _ in $(seq 20); do pgrep -f "$fika_app/Contents/Helpers/Fika (Browser|Screen|Simulator).app/Contents/MacOS/" >/dev/null || return 0; sleep 0.5; done
  warn "some of Fika's helpers are still running: $(pgrep -fl "$fika_app/Contents/Helpers/Fika (Browser|Screen|Simulator).app/Contents/MacOS/" | cut -c1-80 | tr '\n' ' ')"
}
run stop_fika

# --- 4. State, config, caches --------------------------------------------------

bold "4. Moving Fika's state to Hammock's"
move_state() {
  mkdir -p "$support"
  [ -d "$new_dir" ] && rmdir "$new_dir" 2>/dev/null || true # an empty one left by a launch
  mv "$old_dir" "$new_dir"
  # The daemon's own sockets and links are remade; the panes' (run/panes) stay,
  # so Hammock's daemon picks up the panes that are still running.
  rm -f "$new_dir"/run/*.sock "$new_dir/run/fika-ssh-sign" "$new_dir/fika-key.pub"
  # The daemon finds each running pane's holder by an absolute socket path:
  # point those at the moved folder, or Hammock would start the panes afresh.
  local o="${old_dir//\'/\'\'}/" n="${new_dir//\'/\'\'}/"
  sqlite3 "$new_dir/state.db" "UPDATE pane_runtime SET socket = replace(socket, '$o', '$n')"
  [ -f "$new_dir/logs/fikad.log" ] && mv "$new_dir/logs/fikad.log" "$new_dir/logs/hmkd.log"
  return 0
}
if [ -d "$old_dir" ]; then
  note "$old_dir → $new_dir"
  run move_state
fi
if [ -d "$old_cfg" ] && [ ! -e "$new_cfg" ]; then
  note "$old_cfg → $new_cfg"
  run mv "$old_cfg" "$new_cfg"
  [ -f "$old_cfg/config" ] && run perl -pi -e 's{\.config/fika\b}{.config/hammock}g' "$new_cfg/config"
fi
if [ -d "$old_cache" ] && [ ! -e "$new_cache" ]; then
  note "$old_cache → $new_cache"
  run mv "$old_cache" "$new_cache"
fi

# --- 5. App settings -----------------------------------------------------------

bold "5. Copying the app's settings (sidebar, windows, notifications…)"
# Adds every setting of $1 that $2 doesn't have (any $2 already has is kept),
# one `defaults write` each, so each keeps its type. Not `defaults import`: it
# replaces the whole domain, and silently refuses one holding SwiftUI's saved
# window layouts (keys thousands of characters long). Those layouts name the
# old app's views, so they're left behind, as is the old app's migration flag.
copy_defaults() {
  local js entries e key value n=0
  js=$(mktemp)
  cat > "$js" <<'JS'
ObjC.import('Foundation');
function run(argv) {
  var ud = $.NSUserDefaults.standardUserDefaults;
  var src = ud.persistentDomainForName(argv[0]);
  if (src.isNil()) return '[]';
  var dst = ud.persistentDomainForName(argv[1]);
  var keys = src.allKeys, out = [];
  for (var i = 0; i < keys.count; i++) {
    var k = keys.objectAtIndex(i), name = k.js;
    if (name.length >= 300 || name.indexOf('migratedFrom.') === 0) continue;
    if (!dst.isNil() && !dst.objectForKey(k).isNil()) continue;
    var data = $.NSPropertyListSerialization.dataWithPropertyListFormatOptionsError(
      src.objectForKey(k), $.NSPropertyListXMLFormat_v1_0, 0, null);
    var xml = $.NSString.alloc.initWithDataEncoding(data, $.NSUTF8StringEncoding).js;
    out.push([name, xml.replace(/^[\s\S]*?<plist[^>]*>\s*/, '').replace(/\s*<\/plist>\s*$/, '')]);
  }
  return JSON.stringify(out);
}
JS
  entries=$(osascript -l JavaScript "$js" "$1" "$2") || { rm -f "$js"; warn "couldn't read $1's settings"; return 0; }
  rm -f "$js"
  while read -r e; do
    key=$(printf %s "$e" | base64 -d | jq -r '.[0]')
    value=$(printf %s "$e" | base64 -d | jq -r '.[1]')
    defaults write "$2" "$key" "$value" && n=$((n + 1))
  done < <(jq -r '.[] | @base64' <<<"$entries")
  note "$n settings copied ($2 keeps any it had already)"
}
for suffix in "" .menu; do
  if defaults read "$old_id$suffix" >/dev/null 2>&1; then
    note "$old_id$suffix → $new_id$suffix"
    run copy_defaults "$old_id$suffix" "$new_id$suffix"
  fi
done

# --- 6. Hammock Key ------------------------------------------------------------

bold "6. Carrying the Fika Key over as the Hammock Key (same key: nothing to re-add)"
copy_key() {
  local blob
  # macOS asks once to let `security` read Fika's item: Allow.
  blob=$(security find-generic-password -s "$old_id.key" -a key -w) || { warn "couldn't read the Fika Key"; return 0; }
  security add-generic-password -U -s "$new_id.key" -a key -l "Hammock Key" \
    -j "Hammock's SSH key: its Secure Enclave handle" \
    -T "$hammock_app/Contents/Helpers/Hammock Key.app" -w "$blob"
}
if security find-generic-password -s "$old_id.key" -a key >/dev/null 2>&1; then
  if security find-generic-password -s "$new_id.key" -a key >/dev/null 2>&1; then
    note "Hammock already has a key: left as it is"
  else
    run copy_key
  fi
else
  note "no Fika Key on this Mac"
fi

# --- 7. SSH config --------------------------------------------------------------

bold "7. Updating the block Fika added to ~/.ssh/config"
if grep -q '^# Added by Fika' "$HOME/.ssh/config" 2>/dev/null; then
  run cp -p "$HOME/.ssh/config" "$HOME/.ssh/config.before-hammock"
  run perl -0pi -e 's{# Added by Fika: in Fika\x27s panes, ssh uses Fika\x27s agent}{# Added by Hammock: in Hammock\x27s panes, ssh uses Hammock\x27s agent}; s{adds your Fika Key\.}{adds your Hammock Key.}; s{\$FIKA_PANE_ID}{\$HMK_PANE_ID}' "$HOME/.ssh/config"
else
  note "not there: nothing to do"
fi

# --- 8. Group icon folders -------------------------------------------------------

bold "8. Renaming Group icon folders (.fika → .hammock)"
for f in "${folders[@]+"${folders[@]}"}"; do
  if [ -e "$f/.hammock" ]; then
    warn "$f has both .fika and .hammock: left as they are"
  else
    note "$f/.fika → .hammock"
    run mv "$f/.fika" "$f/.hammock"
    git -C "$f" ls-files --error-unmatch .fika >/dev/null 2>&1 && warn "$f/.fika is in git there: commit the rename"
  fi
done
[ ${#folders[@]} -eq 0 ] && note "none"

# --- 9. The command ------------------------------------------------------------

bold "9. The command: fika → hmk"
link_dir=""
for d in /opt/homebrew/bin /usr/local/bin "$HOME/.local/bin"; do
  for n in fika fika-beta; do
    if [ -L "$d/$n" ] && readlink "$d/$n" | grep -q '/Fika'; then
      note "removing $d/$n"
      run rm "$d/$n"
      [ $n = fika ] && [ -z "$link_dir" ] && link_dir="$d"
    fi
  done
done
if [ -z "$link_dir" ]; then
  for d in /opt/homebrew/bin /usr/local/bin "$HOME/.local/bin"; do [ -w "$d" ] && { link_dir="$d"; break; }; done
fi
link_dir="${link_dir:-$HOME/.local/bin}"
note "linking $link_dir/hmk"
run mkdir -p "$link_dir"
run ln -sfn "$hammock_app/Contents/Helpers/hmk" "$link_dir/hmk"

# --- 10. Hammock ---------------------------------------------------------------

bold "10. Opening Hammock and reinstalling the agent hooks"
hmk() { env -u FIKA_SOCKET -u FIKA_HOOK_SOCKET -u FIKA_PANE_ID -u FIKA_DIR -u HMK_SOCKET -u HMK_HOOK_SOCKET -u HMK_PANE_ID -u HMK_DIR "$hammock_app/Contents/Helpers/hmk" "$@"; }
start_hammock() {
  open "$hammock_app"
  for _ in $(seq 60); do hmk ls --json >/dev/null 2>&1 && break; sleep 1; done
  hmk ls --json >/dev/null 2>&1 || die "Hammock's daemon didn't come up: see $new_dir/logs/hmkd.log"
  local after
  after=$(hmk ls --json | jq '[.groups[].tabs[]?.panes[]?] | length')
  note "Hammock is running: $after panes${panes_before:+ (Fika had $panes_before)}"
  hmk hooks install
}
run start_hammock

# --- 11. Tidying up ------------------------------------------------------------

if [ $trash = 1 ] && [ -n "$fika_app" ]; then
  bold "11. Moving Fika.app to the Trash"
  if git -C "$(dirname "$fika_app")" rev-parse >/dev/null 2>&1; then
    note "$fika_app is in a git checkout (a build): left for the next build to replace"
  else
    run osascript -e "tell application \"Finder\" to delete POSIX file \"$fika_app\""
  fi
fi
if [ $reset_tcc = 1 ]; then
  bold "Forgetting Fika's privacy permissions"
  for id in $old_id $old_id.menu $old_id.browser $old_id.screen $old_id.simulator $old_id.key; do
    run tccutil reset All "$id" >/dev/null 2>&1 || true
  done
fi

# --- Still to do -----------------------------------------------------------------

bold "Still mentioning Fika (for you, or an agent with docs/migrate-from-fika.md, to update)"
found=0
for f in "$HOME"/.zshrc "$HOME"/.zprofile "$HOME"/.zshenv "$HOME"/.bashrc "$HOME"/.bash_profile "$HOME"/.profile \
         "$HOME"/.config/fish/config.fish "$HOME"/.claude/CLAUDE.md "$HOME"/.codex/AGENTS.md "$HOME"/.codex/config.toml; do
  if [ -f "$f" ] && grep -qE '\bfika\b|FIKA_|mcp__fika__' "$f"; then note "$f"; found=1; fi
done
for d in "$HOME/.claude/skills" "$HOME/.claude/agents" "$HOME/.claude/commands" "$HOME/Library/LaunchAgents"; do
  [ -d "$d" ] || continue
  while read -r f; do note "$f"; found=1; done < <(grep -RliE '\bfika\b|FIKA_|mcp__fika__' "$d" 2>/dev/null)
done
crontab -l 2>/dev/null | grep -qE '\bfika\b' && { note "your crontab"; found=1; }
[ $go = 1 ] && hmk schedule list 2>/dev/null | grep -qiE '\bfika\b|FIKA_' && { note "Hammock's scheduled tasks (hmk schedule list)"; found=1; }
[ $found = 0 ] && note "nothing found in the usual places"

bold "By hand"
note "Hammock asks again for the permissions it needs (notifications, Screen Recording, Accessibility…): allow them as they come up."
note "Panes that carried over still have Fika's environment: agents in them don't report status, and git signing and ssh-agent point at Fika's old paths. Use new panes, and move work across (agents resume where they were) when convenient."
note "Pair your iPhone again with the new Hammock app, and migrate your other Macs too (linked computers only talk to the same version)."
[ $go = 1 ] || note "This was a dry run. Run it again with --go to do it."
