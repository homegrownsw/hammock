# Marketing

- `index.html`: the website, https://hammock.jsp.scot. One static page, no build step; the window and
  iPhone in it are drawn in HTML and CSS, so nothing personal ever ends up in a screenshot.
- `banner.html` → `banner.png`: the banner at the top of the repo's README (also the site's link preview).

The migration page, hammock.jsp.scot/migration, lives in [../docs/migration-page](../docs/migration-page).

## Re-render the banner

Headless Chrome, at 2× (2560×800), with a transparent background so the corners stay rounded:

```sh
cd marketing && "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --hide-scrollbars \
  --default-background-color=00000000 --force-device-scale-factor=2 --window-size=1280,400 \
  --virtual-time-budget=8000 --allow-file-access-from-files --screenshot="$PWD/banner.png" "file://$PWD/banner.html"
```

## Publish the site

It sits at the root of the update server, beside the Sparkle feed and the release zips (`/download`
redirects to the latest). Copy only these files, and never with `--delete`: the releases live there too.

```sh
d=$(mktemp -d) && cp marketing/{index.html,banner.png} "$d" &&
  sips -Z 512 apple/Resources/AppIcon-1024.png --out "$d/icon-512.png" &&
  sips -Z 256 apple/Resources/AppIcon-1024.png --out "$d/icon-256.png" &&
  chmod 644 "$d"/* && chmod 755 "$d" &&
  . ./.release.env && rsync -a "$d/" "$UPDATE_HOST:$UPDATE_DIR/"
```
