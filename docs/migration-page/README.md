# hammock.jsp.scot/migration

The page for moving a Mac from Fika to Hammock: `index.html` (the steps, for people) and `agent.md` (what
Claude follows when pointed at it). The tidy-up prompt in `index.html` is a copy of the one in
[../migrate-from-fika.md](../migrate-from-fika.md): change both.

Published by hand, with the script and the icons beside it:

```sh
d=$(mktemp -d) && cp docs/migration-page/{index.html,agent.md} scripts/migrate-from-fika.sh "$d" &&
  sips -Z 512 apple/Resources/AppIcon-1024.png --out "$d/icon-512.png" &&
  sips -Z 256 apple/Resources/AppIcon-1024.png --out "$d/icon-256.png" &&
  chmod 644 "$d"/* && chmod 755 "$d" &&
  . ./.release.env && rsync -a "$d/" "$UPDATE_HOST:$UPDATE_DIR/migration/"
```
