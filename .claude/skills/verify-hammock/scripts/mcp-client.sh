#!/bin/sh
# Runs inside the run's agent pane (mcp.sh puts it there): starts `hmk mcp`,
# initialises it, then sends each tools/call in CALLS (one JSON object per
# line: {"name":…,"arguments":{…}}), waiting for each answer before the next.
# Every response line goes to OUT; DONE is written last.
#   mcp-client.sh HMK CALLS OUT
hmk="$1" calls="$2" out="$3"
dir="$(mktemp -d)"
mkfifo "$dir/in"
"$hmk" mcp <"$dir/in" >"$out.part" 2>"$out.err" &
mcp=$!
exec 3>"$dir/in"

# wait_id N: until OUT has the response with id N (60 s at most).
wait_id() {
  i=0
  while ! grep -q "\"id\":$1[,}]" "$out.part" 2>/dev/null; do
    i=$((i + 1)); [ "$i" -gt 600 ] && return 1
    sleep 0.1
  done
}

echo '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"verify-hammock","version":"1"}}}' >&3
wait_id 0
echo '{"jsonrpc":"2.0","method":"notifications/initialized"}' >&3
n=0
while IFS= read -r call || [ -n "$call" ]; do
  [ -n "$call" ] || continue
  n=$((n + 1))
  if [ "$call" = LIST ]; then
    printf '{"jsonrpc":"2.0","id":%d,"method":"tools/list"}\n' "$n" >&3
  else
    printf '{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":%s}\n' "$n" "$call" >&3
  fi
  wait_id "$n" || { echo "timeout waiting for call $n" >>"$out.err"; break; }
done <"$calls"
exec 3>&-
wait "$mcp" 2>/dev/null
rm -rf "$dir"
mv "$out.part" "$out"
