tag: INTERNAL
date: 2026-11-08
β keeps its own config file in its state dir (a copy of ~/.config/hammock/config the first time), so testing doesn't change your settings; its panes and just beta-env set HMK_CONFIG_DIR. Any config key given as a launch argument (--args -notify-style in-app) beats the file's. Settings read through views go through Prefs (HammockKit), UserDefaults only on the iPhone.
