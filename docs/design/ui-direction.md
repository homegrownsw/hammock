# UI direction — macOS app

Status: Chosen 2026-10-29; updated to the app as built, 2026-11-10

We explored a herdr-style layout (Groups sidebar + horizontal tabs) and a single-sidebar layout, each in a dark custom style and a native macOS 26 style. **Native B**, the single sidebar in the native style, is the chosen direction. The horizontal layout survives as an option.

## Layout

- **One floating source-list sidebar** (macOS 26 style: inset, rounded, traffic lights inside it). It can be hidden (⌃⌘S); then the pointer at the window's left edge peeks it.
  - **Groups** at the top, always in the same position. Each Group is a disclosure group with its icon (an emoji, a picture, or the default glyph); its **tabs are nested underneath** as rows. A tab row shows its agent's state icon (or the program running), its name, and trailing detail (how long the agent has been working, or the pane count), and a close button on hover. Collapsed Groups show a summary state icon, and optionally keep showing tabs with an active agent. Groups and tabs reorder by dragging.
  - Groups shared with you mix in with your own, with the host's avatar; a linked computer's Groups do too. Offline ones are dimmed with their last state.
  - **Sidebar layout** (`sidebar-layout`): Vertical (above), Compact (tighter rows), or Horizontal (Groups in the sidebar, the selected Group's tabs in a tab bar above the panes).
  - **Needs You** is pinned at the **bottom** of the sidebar (above the footer) so the Groups list never shifts. Cards carry inline actions: Deny / Reply… / Allow for a permission prompt (default button on the right), Answer… for a question, Deny / Always in Group / Allow Once for an agent asking Hammock, Decline / Let View / Let Drive for someone asking to join. **Finished** (turns done) sits below it, with Clear.
  - **Footer:** this computer's name and icon, and sharing status with a Stop button; once computers are linked, a switcher: All, then each computer, clicking one to show only its Groups.
- **Jumping around** is the command palette (⌘K): Groups, tabs, panes, agents and every command, fuzzy-searched. There's no search field in the sidebar.
- **Content area:** the unified toolbar shows the tab title (bold) and a `Group — folder` subtitle; on the right, Split Right, Split Down and New Tab (each a menu: terminal, browser, simulator, editor…), then Share (or, for a Group shared with you or on a linked computer, whose it is). Panes fill the rest: hairline dividers with Compact spacing, or cards with rounded corners on the sidebar's background with Spacious (`pane-spacing`).
- **Panes:** a slim header with the program's or agent's icon, the pane's name as a pop-up menu (what the pane shows, and for agents **Continue in…**: switching agents lives here, never as a prominent toolbar button), its folder, the agent view toggle, zoom, the agent's state and close. A pane waiting for input gets an orange ring and "Waiting for you". Optionally, inactive panes are dimmed and the active one gets a border (`dim-inactive-panes`, `active-pane-border`).

## Visual language

- Follows the system: light and dark appearance (or one of them, `appearance`), SF Pro, system semantic colours. The terminal font is your choice, falling back to Ghostty's config.
- **Themes** (`app-theme`, Settings ▸ Appearance, View ▸ Theme) set the terminal, window tint, accent, agent view colours and active pane border together: Hammock's own forest green by default, Native (the system's colours), the tints Graphite, Ocean, Forest, Rose, Lavender and Sand, the terminal themes Catppuccin, Tokyo Night, Gruvbox, Nord, Rosé Pine and Solarized, or Custom for each one separately. The paired iPhone follows.
- Agent state is shape **and** colour: rings while all is well, filled when it needs you ("Orbit"): needs input = amber filled `!`; working = an indigo arc going round a faint ring; finished = green ring with a check; errored = red filled cross; at rest = grey ring (a dot going round it while its shells still run); no agent = the program's icon, else the terminal glyph. With `tab-status = trailing`, sidebar rows show the agent's logo instead and the status at the end of the row.
- **Program icons** (`program-icons`): plain glyphs, colourful squircles in the program's colours, or off; agents always show their logo.
- **Shared Groups:** people's avatars (account picture, or initials in their colour) on the tabs and panes they're looking at, and "<name> is typing…" in the pane header; "View only" when you may only watch. The Share sheet lists people with Can view / Can drive / Remove, the invite link, and Stop Sharing.

## Not built from the mock-ups

- A presence capsule in the toolbar with a Follow button ([#3](https://github.com/homegrownsw/hammock/issues/3)), and coloured outlines on the pane someone's typing into.
- The acting agent's pointer, with a labelled tag, on browser and simulator panes it's driving, and a "… is controlling this · Take control" footer.
