#!/bin/sh
# Bundles the pages the Mac and iPhone apps load into a WKWebView, one file
# each in apple/Resources: editor panes' (src/main.js and CodeMirror), diff
# panes' (src/diff.js), files panes' (src/files.js) and the agent view's web
# conversation (src/agent.js). The output is committed, so building the apps
# needs no Node.
set -eu
cd "$(dirname "$0")"
npm ci --silent --no-audit --no-fund
page() { # page ENTRY TEMPLATE OUT
  out="$(mktemp)"
  npm exec -- esbuild "$1" --bundle --minify --format=iife --target=safari18 --legal-comments=none --outfile="$out" --log-level=warning
  # The script goes in place of the marker; "</script" can't appear inside it.
  if grep -q '</script' "$out"; then sed -i '' 's#</script#<\\/script#g' "$out"; fi
  awk -v js="$out" '/\/\*SCRIPT\*\// { while ((getline l < js) > 0) print l; next } { print }' "$2" > "../Resources/$3"
  rm -f "$out"
  printf 'apple/Resources/%s: %s bytes\n' "$3" "$(wc -c < "../Resources/$3" | tr -d ' ')"
}
page src/main.js template.html editor.html
page src/diff.js diff-template.html diff.html
page src/files.js files-template.html files.html
page src/agent.js agent-template.html agent.html
