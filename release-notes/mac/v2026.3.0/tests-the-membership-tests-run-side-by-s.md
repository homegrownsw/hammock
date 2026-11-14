tag: INTERNAL
date: 2026-11-13
Tests: the membership tests run side by side and the daemon package takes about a third of the time; flaky tests fixed (a mesh check that didn't wait, races in the holder's proof and daemon shutdown, pane holders leaking pseudo-terminals); CI runs the holder package under -race.
