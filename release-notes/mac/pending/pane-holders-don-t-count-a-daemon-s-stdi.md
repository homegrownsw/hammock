tag: INTERNAL
date: 2026-11-10
Pane holders don't count a daemon's stdin, stdout or stderr as its state dir's locks, check the locks are held now, and hmkd replaces a stdin, stdout or stderr that's a folder or a once-flocked file with /dev/null.
