tag: INTERNAL
date: 2026-11-19
The daemon reads files clients and agents name (the agent view's files, editor and preview images, hmk show, Group pictures, diffs) only if they're regular files, within their size limit however much they grow, so a device or FIFO can't exhaust its memory or block a connection.
