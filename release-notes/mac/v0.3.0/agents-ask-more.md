tag: FIXED
date: 2026-11-10
Agents now ask before running JavaScript of their own in a browser pane they didn't open, before running, pausing or skipping a scheduled task they didn't make, and before searching past sessions outside the folder they were started in. Starting an agent with a message runs it directly, so nothing in the message can be typed into the shell as keystrokes, and from wherever your shell finds it.
