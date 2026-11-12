#!/bin/sh
# The demo's made-up dev server (scripts/readme-demo): a dev server's
# start-up lines, then one each time the page changes. PORT is where the
# page really is.
port=$1

printf '\n  \033[1;32mVITE\033[0m v7.1.4  ready in \033[1m412\033[0m ms\n\n'
printf '  \033[32m➜\033[0m  \033[1mLocal\033[0m:   \033[36mhttp://localhost:%s/\033[0m\n' "$port"
printf '  \033[2m➜  Network: use --host to expose\033[0m\n\n'
n=0
while :; do
  sleep 2.5
  n=$((n + 1))
  case $n in
  3) printf '\033[2m%s\033[0m \033[1;36m[vite]\033[0m \033[32mhmr update\033[0m \033[2m/src/checkout/Summary.tsx\033[0m\n' "$(date +%H:%M:%S)" ;;
  5) printf '\033[2m%s\033[0m \033[1;36m[vite]\033[0m \033[32mhmr update\033[0m \033[2m/src/checkout/bySeller.ts\033[0m\n' "$(date +%H:%M:%S)" ;;
  7) printf '\033[2m%s\033[0m \033[1;36m[vite]\033[0m \033[32mpage reload\033[0m \033[2msrc/routes/checkout.tsx\033[0m\n' "$(date +%H:%M:%S)" ;;
  esac
done
