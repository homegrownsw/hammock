#!/bin/sh
# Bundles the editor (src/main.js and CodeMirror) into one page,
# apple/Resources/editor.html, which the Mac and iPhone apps load into a
# WKWebView. The output is committed, so building the apps needs no Node.
set -eu
cd "$(dirname "$0")"
npm ci --silent --no-audit --no-fund
out="$(mktemp)"
npm exec -- esbuild src/main.js --bundle --minify --format=iife --target=safari18 --legal-comments=none --outfile="$out" --log-level=warning
# The script goes in place of the marker; "</script" can't appear inside it.
if grep -q '</script' "$out"; then sed -i '' 's#</script#<\\/script#g' "$out"; fi
awk -v js="$out" '/\/\*SCRIPT\*\// { while ((getline l < js) > 0) print l; next } { print }' template.html > ../Resources/editor.html
rm -f "$out"
printf 'apple/Resources/editor.html: %s bytes\n' "$(wc -c < ../Resources/editor.html | tr -d ' ')"
