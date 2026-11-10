# dmgbuild's settings for Hammock.dmg (scripts/make-dmg.sh passes app and background with -D).
# The window is drawn in marketing/dmg-background.html: the icon positions and label size here and the
# pills in the background have to agree.
import os.path

app = defines["app"]  # noqa: F821 (dmgbuild defines it)
name = os.path.basename(app)

files = [app]
symlinks = {"Applications": "/Applications"}
# The volume's icon is the app's.
icon = os.path.join(app, "Contents", "Resources", "AppIcon.icns")

# LZMA: smaller than zlib's UDZO, and opens on every macOS Hammock runs on.
format = "ULMO"

# dmgbuild finds dmg-background@2x.png beside it and makes a Retina TIFF of the two.
background = defines["background"]  # noqa: F821
# The window's frame, title bar included: the background is 660×400, and the title bar is 32 more on
# macOS 26 and later (28 before, which only leaves 4 pt of the background unseen).
window_rect = ((200, 160), (660, 432))
default_view = "icon-view"
show_status_bar = False
show_tab_view = False
show_toolbar = False
show_pathbar = False
show_sidebar = False
show_icon_preview = False
include_icon_view_settings = True

icon_size = 128
text_size = 14
# Centres, in points from the top left of the background.
icon_locations = {name: (165, 204), "Applications": (495, 204)}
arrange_by = None
# Not hide_extension: it sets com.apple.FinderInfo on the app, which breaks codesign --verify --strict.
# Finder hides .app on its own unless someone shows every extension.
