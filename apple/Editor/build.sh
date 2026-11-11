#!/bin/sh
# Bundles editor panes' page (src/main.js and CodeMirror) and diff panes'
# (src/diff.js) into one file each, apple/Resources/editor.html and
# diff.html, which the Mac and iPhone apps load into a WKWebView. The
# output is committed, so building the apps needs no Node.
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
