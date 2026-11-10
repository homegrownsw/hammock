# Marketing

- `index.html`: the website, https://homegrownsw.com/hammock/. One static page, no build step; the window and
  iPhone in it are drawn in HTML and CSS, so nothing personal ever ends up in a screenshot.
- `banner.html` → `banner.png`: the banner at the top of the repo's README (also the site's link preview).
- `dmg-background.html` → `apple/Resources/dmg-background.png` and `@2x`: the window of Hammock.dmg, the download
  (`scripts/make-dmg.sh`; `just dmg` makes one to look at).


## Re-render the banner

Headless Chrome, at 2× (2560×800), with a transparent background so the corners stay rounded:

```sh
cd marketing && "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --hide-scrollbars \
  --default-background-color=00000000 --force-device-scale-factor=2 --window-size=1280,400 \
  --virtual-time-budget=8000 --allow-file-access-from-files --screenshot="$PWD/banner.png" "file://$PWD/banner.html"
```

## Re-render the DMG's background

At 1× and 2× (dmgbuild makes a Retina TIFF of the pair). Finder draws the icons and their names over it, at the
places `scripts/dmg-settings.py` gives; the pills under the names have to stay where Finder puts them, so open
the DMG after any change (`just dmg`), and on each new major macOS.

```sh
cd marketing && for s in 1 2; do out=../apple/Resources/dmg-background$([ $s = 2 ] && echo @2x).png
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --headless --hide-scrollbars \
    --force-device-scale-factor=$s --window-size=660,400 --virtual-time-budget=8000 \
    --screenshot="$PWD/$out" "file://$PWD/dmg-background.html"; done
```

## Publish the site

It lives at https://homegrownsw.com/hammock/, part of the Homegrown Software site (`homegrownsw/homegrownsw-site`,
checked out at `~/Developer/homegrownsw-site`). `just site` copies the page, the banner and the icons into that
repo's `site/hammock/`; commit them there and run its `just deploy`.

Links on the page are relative, so it works under `/hammock/`. Its Download buttons go to `download`, which the
site's nginx redirects to the latest release's `Hammock.dmg` on GitHub (the site repo's `nginx/hammock.conf`).
