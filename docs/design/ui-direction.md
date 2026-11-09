# UI direction — macOS app

Status: Locked (2026-10-29)

We explored a herdr-style layout (Groups sidebar + horizontal tabs) and a single-sidebar layout, each in a dark custom style and a native macOS 26 style. **Native B** is the chosen direction.

## Layout

- **One floating source-list sidebar** (macOS 26 style: inset, rounded, traffic lights inside it).
  - Search field at the top (jump to Group / tab / agent).
  - **Groups** directly below search, always in the same position. Each Group is a disclosure group; its **tabs are nested underneath** as rows. A tab row shows the tab's agent state icon, its name, and trailing detail (agent + elapsed, or pane count). Collapsed Groups show a summary state icon.
  - **Shared With You** section (multiplayer, later) lists remote Groups with the host's avatar, host machine and your role.
  - **Needs You** is pinned at the **bottom** of the sidebar (above the host footer) so the Groups list never shifts. Cards for needs-input items carry inline actions (Deny / Reply… / Allow — default button on the right). Errors and finished turns are compact rows.
  - Host footer: machine name, connected devices, health dot.
- **Content area**: unified toolbar with the tab title (bold) and `Group — cwd` subtitle; actions in Liquid Glass capsules on the right (split right / split down / new pane; share). Panes fill the rest with hairline dividers.
- **Panes**: slim title row. For agent panes the agent name is a pop-up menu (`claude ▾`) holding per-agent actions — **switching agents lives here, never as a prominent toolbar button**. A pane awaiting input gets an orange ring and a "Waiting for you" label.

## Visual language

- Follows the system: light and dark appearance, system accent colour, SF Pro / SF Mono, system semantic colours.
- Agent state is shape **and** colour: rings while all is well, filled when it needs you ("Orbit"): needs input = amber filled `!`; working = an indigo arc going round a faint ring; finished = green ring with a check; errored = red filled cross; at rest = grey ring; plain shell = terminal glyph. With `tab-status = trailing`, sidebar rows show the agent's logo instead and the status at the end of the row.
- Agent-controlled browser/simulator panes show the acting agent's cursor/touch with a labelled tag and a footer "claude is controlling this … · Take control".
- Multiplayer presence: collaborator avatars (initials, personal colour) in a toolbar capsule with a Follow button; a glowing ring while typing; the pane they're typing into gets their colour outline and a "<name> is typing" chip; viewers get a thinner outline. Sharing popover lists people with Driver/Viewer roles, invite field, connection info and a red Stop Sharing.
