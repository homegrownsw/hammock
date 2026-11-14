# Editor panes

A text file in a pane (CodeMirror on the Mac) that the person can edit, opened beside their terminal with lines selected. Agents point people at code with it and read what they have open and selected. Changes to the file on disk reload in the pane.

## Sub-features

- `editor-open`: open a file with a line or range selected (`hmk edit FILE:LINE`, `editor_open`).
- `editor-read`: its text as the person sees it, unsaved edits included, with path and selection (`hmk edit --read`, `editor_read`).
- `editor-select`: select and scroll to lines (`hmk edit --select`, `editor_select`).
- `editor-reload`: a change on disk shows in the pane.
- `editor-reuse`: opening the same file again in that tab reuses the pane.

## How to get to it (user POV)

- CLI: `hmk edit FILE[:LINE[:COL][-LINE[:COL]]] [--tab|--split PANE|--group G]`, `hmk edit --read PANE [--json|--numbers]`, `hmk edit --select PANE LINE[-LINE]`.
- MCP: `editor_open`, `editor_read`, `editor_select`.
- Mac: File ▸ Open in Editor, the split menus' "with Editor" items, the palette.

## Driving it with verify-hammock

Preconditions:

- Baseline run. A file inside the run's scratch dir, so cleanup removes it: `eval "$(sed 's/^/export /' "$RUN/run.env")"; F=$SCRATCH/notes.txt; printf 'line one\nline two verify\nline three\n' >"$F"`. Group `verify-editor`.

- **Open at a line.** `$S/record.sh "$RUN" 01-open edit "$F:2" --group verify-editor` prints the pane id (`E`).
- **Read.** `$S/record.sh "$RUN" 02-read edit --read "$E" --json` gives `path` ending `notes.txt`, `lines: 3`, `selection.from: "2:1"` and the file's text.
- **Change on disk, see it reload.** Append `line four added on disk` to `$F`. Within about two seconds, `hmk edit --read "$E" --numbers` ends with `4	line four added on disk`.
- **Select.** `$S/record.sh "$RUN" 03-select edit --select "$E" 4`, then `04-read edit --read "$E" --json`. `selection` is `{"from":"4:1","to":"4:24","text":"line four added on disk"}`.
- **MCP entry.** `$S/mcp.sh "$RUN" 05-mcp editor_open '{"path":"'$F'","line":1}'` returns a pane id, and `editor_read` on it returns the same text.

## Gotchas

- `ls --json` reports editor panes as `"kind":"editor"`. `hmk read` works on them too and prints the text.
- The editor's content doesn't render in `snapshot.sh` PNGs: you get its header (`notes.txt`, `1:1`, `Plain Text`) over a blank page. The text proof is `edit --read`. A picture of the content needs `HMK_DEBUG_CAPTURE=1` with β's Screen Recording.
- `hmk edit` without `--group`/`--tab` means "beside this pane", meaningless from outside one, so always pass `--group`.
- Unsaved edits only come from a person typing in the app, so `dirty`/unsaved-text paths need the person.
