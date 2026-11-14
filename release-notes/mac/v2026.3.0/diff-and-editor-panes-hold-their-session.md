tag: INTERNAL
date: 2026-11-14
Diff and editor panes hold their session with a lease (PaneLease), as the agent view does, instead of releasing it in onDisappear.
