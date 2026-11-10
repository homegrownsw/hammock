tag: FIXED
date: 2026-11-09
What only you can do in Hammock (answering an agent's request, pairing a phone, sharing, linking computers, remote access) is now taken only from the Hammock app, a pane's own shell or a terminal of yours, not from a program left running in the background. Such a program (and `hmk` run from launchd, cron or a terminal multiplexer) is now treated like an agent: it asks before typing into, closing or driving a pane it didn't open. Pane holders also take only Hammock's own daemon.
