tag: FIXED
date: 2026-11-10
When a tab opened with `hmk tab new --wait` or `hmk split --wait` can't start its terminal, `hmk` now says why and exits with status 125, so a script or agent waiting on it no longer mistakes it for the command failing. The pane itself says why its terminal didn't start, and says so straight away when the helper that runs it quits, rather than after five seconds.
