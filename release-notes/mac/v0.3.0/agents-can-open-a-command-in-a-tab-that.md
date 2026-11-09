tag: NEW
date: 2026-11-09
Agents can open a command in a tab that closes itself when the command ends cleanly (close_on_exit), and wait for it: handy for review tools like revdiff that you quit when done. `hmk tab new` and `hmk split` take --wait, which returns when the command ends, with its exit status (and `hmk tab new` opens the tab in your current Group if you leave the Group out).
