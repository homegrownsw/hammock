tag: INTERNAL
date: 2026-11-08
Fixed a data race: a new pane's info was saved while its holder's reader could be changing it.
