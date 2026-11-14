#!/usr/bin/env bash
# Call Hammock's MCP tools as an agent in the run would: from inside a pane of
# the run's daemon (hmk mcp offers no tools outside one). The first call makes
# a Group "verify-agent" with a tab "agent" running /bin/sh; every call runs
# there, so panes opened through MCP belong to that same agent, and later calls
# may drive them without asking.
#   mcp.sh RUN NAME TOOL 'ARGS-JSON' [TOOL 'ARGS-JSON' ...]
#   e.g. mcp.sh "$RUN" 01-open browser_open '{"url":"https://example.com"}'
# Keeps RUN/NAME.calls (what was asked) and RUN/NAME.mcp.jsonl (every
# response), prints each call's text content, and exits 1 if any call errored.
set -uo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
. "$here/lib.sh"
load_run "${1:-}"
name="${2:?usage: mcp.sh RUN NAME TOOL ARGS-JSON [TOOL ARGS-JSON ...] | mcp.sh RUN NAME --list}"
shift 2
calls="$run/$name.calls" out="$run/$name.mcp.jsonl"
: >"$calls"
list=""
if [ "${1:-}" = --list ]; then
  list=1; echo LIST >"$calls"; shift
else
  [ $(( $# % 2 )) -eq 0 ] && [ $# -gt 0 ] || { echo "error: give TOOL ARGS-JSON pairs" >&2; exit 2; }
fi
while [ $# -gt 0 ]; do
  jq -c --arg n "$1" '{name: $n, arguments: .}' <<<"$2" >>"$calls" || { echo "error: bad JSON for $1: $2" >&2; exit 2; }
  shift 2
done
rm -f "$out" "$out.err"

# The agent pane, made once per run.
pane="$(cat "$run/agent.pane" 2>/dev/null || true)"
if [ -z "$pane" ] || ! "$HMK" ls --json | jq -e --arg p "$pane" '[.groups[].tabs[].panes[].id] | index($p)' >/dev/null; then
  "$HMK" ls --json | jq -e '.groups[] | select(.title=="verify-agent")' >/dev/null || "$HMK" group new verify-agent >/dev/null
  # It runs in RUN, with short names for the client and hmk there, so its
  # screen shows `./mcp-client.sh ./hmk NAME…`, not this Mac's paths.
  ln -sf "$here/mcp-client.sh" "$run/mcp-client.sh"
  ln -sf "$HMK" "$run/hmk"
  "$HMK" tab new verify-agent --name agent --cwd "$run" -- /bin/sh >/dev/null
  pane="$("$HMK" ls --json | jq -r '.groups[] | select(.title=="verify-agent") | .tabs[] | select(.title=="agent") | .panes[0].id' | head -1)"
  [ -n "$pane" ] || { echo "error: couldn't make the agent pane" >&2; exit 1; }
  echo "$pane" >"$run/agent.pane"
  sleep 0.5
fi

"$HMK" send "$pane" "$(printf './mcp-client.sh ./hmk %q %q' "$name.calls" "$name.mcp.jsonl")" --enter
for _ in $(seq 1200); do [ -f "$out" ] && break; sleep 0.1; done
[ -f "$out" ] || { echo "error: no answer in 120 s; the agent pane shows:" >&2; "$HMK" read "$pane" >&2; exit 1; }
[ -s "$out.err" ] && cat "$out.err" >&2

if [ -n "$list" ]; then
  # Each tool, its arguments (* required) and the first line of its description.
  jq -r 'select(.id == 1) | .result.tools[] | "\(.name)(\((.inputSchema.required // []) as $r | [(.inputSchema.properties // {}) | keys[] | if (. as $k | $r | index($k)) then . + "*" else . end] | join(", ")))  \(.description | split("\n")[0] | .[0:110])"' "$out"
  exit 0
fi
# Images (browser_screenshot, sim_screenshot) to RUN/NAME-<call>-<n>.png.
jq -r 'select(.id > 0) | .id as $i | [.result.content[]? | select(.type=="image")] | to_entries[] | "\($i)-\(.key) \(.value.data)"' "$out" |
  while read -r key data; do
    printf %s "$data" | base64 -d >"$run/$name-$key.png" && echo "image: $run/$name-$key.png"
  done
jq -r 'select(.id > 0) | "[\(.id)] " + (if .error then "ERROR " + .error.message else ((if .result.isError then "ERROR " else "" end) + ([.result.content[]? | select(.type=="text") | .text] | join("\n"))) end)' "$out"
! jq -e 'select(.id > 0) | select(.error or .result.isError)' "$out" >/dev/null
