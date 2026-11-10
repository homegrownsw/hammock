# Marketing

- `index.html`: the website, https://homegrownsw.com/hammock/. One static page, no build step; the window and
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

It lives at https://homegrownsw.com/hammock/, part of the Homegrown Software site (`homegrownsw/homegrownsw-site`,
checked out at `~/Developer/homegrownsw-site`). `just site` copies the page, the banner and the icons into that
repo's `site/hammock/`; commit them there and run its `just deploy`.

Links on the page are relative, so it works under `/hammock/`. Its Download buttons go to `download`, which the
site's nginx redirects to the latest release's `Hammock.zip` on GitHub (the site repo's `nginx/hammock.conf`).
