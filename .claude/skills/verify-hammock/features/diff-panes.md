# Diff panes

A git diff laid out like a pull request's Files Changed, for the person to review an agent's work. They leave comments on lines, and the threads go to the agent, which replies, marks them addressed and waits for more. Opened by `hmk diff`, the `diff_open` tool, or the Mac's Open a Diff sheet.

## Sub-features

- `diff-open`: a branch against its base, uncommitted changes, a commit or a range (`hmk diff`, `diff_open`).
- `review-comment`: a thread on a line of the new side (`hmk review comment`, `review_comment`).
- `review-reply-address`: reply in a thread, mark it addressed (`hmk review reply|address`, `review_reply`, `review_mark_addressed`).
- `review-list`: the threads, their lines and comments (`hmk review show|list --json`, `review_threads`).
- `review-wait`: block until the person comments or resolves (`hmk review wait`, `review_wait`).

## How to get to it (user POV)

- CLI: `hmk diff [REV|A..B] [--uncommitted|--branch] [--repo DIR] [--group G] [--name N]`, `hmk review …`.
- MCP: `diff_open`, `review_*`.
- Mac: File ▸ Open a Diff (`HMK_DEBUG_OPEN_DIFF=1` renders the sheet), the palette's "Split Right with Diff…".

## Driving it with verify-hammock

Preconditions:

- Baseline run. A throwaway repo in the scratch dir, made without the person's git config:

  ```sh
  eval "$(sed 's/^/export /' "$RUN/run.env")"; R=$SCRATCH/repo; mkdir -p "$R"
  export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_AUTHOR_NAME=Sam GIT_AUTHOR_EMAIL=sam@example.com GIT_COMMITTER_NAME=Sam GIT_COMMITTER_EMAIL=sam@example.com
  git -C "$R" init -q -b main && printf 'alpha\nbeta\ngamma\n' >"$R/app.txt" && git -C "$R" add -A && git -C "$R" commit -qm init
  printf 'alpha\nbeta verify-changed\ngamma\ndelta\n' >"$R/app.txt"
  ```

- Group `verify-diff`.

- **Open.** `$S/record.sh "$RUN" 01-diff diff --uncommitted --repo "$R" --group verify-diff --name verify-diff` prints the pane id (`D`). `ls --json` lists it as `"kind":"diff"`, titled `Uncommitted changes`.
- **Comment on a changed line.** `$S/record.sh "$RUN" 02-comment review comment "$D" app.txt:2 'verify: why beta changed?'` prints `rt_…`.
- **The daemon diffed it.** `$S/record.sh "$RUN" 03-show review show "$D" --json` has one thread on `app.txt`, line 2, with `"lines":"beta verify-changed"` (the new side's text, so the diff was computed), `state: open`.
- **Reply and address.** `04-reply review reply <rt> 'verify: reply'` and `05-address review address <rt> 'verify: done'` each print `rc_…`. `06-show` shows `state: addressed` and 3 comments.
- **In the window** (β run). `$S/snapshot.sh "$RUN" 07-diff HMK_DEBUG_TAB=verify-diff`. The header shows `Changes`, the repo path, and a `3 new replies` badge.
- **MCP entry.** `$S/mcp.sh "$RUN" 08-mcp diff_open '{"repo":"'$R'","source":"uncommitted"}'` returns a pane id. `review_threads` lists the agent's threads.

## Gotchas

- `hmk read` on a diff pane prints nothing: the diff is drawn by the app's web view. Prove the content through threads (`lines`), not `read`.
- Snapshots show the diff pane's header and badge but not the diff itself (web content). The file list and highlighting need `HMK_DEBUG_CAPTURE=1` and β's Screen Recording, or the person.
- `review wait` blocks until the person acts. Only use it with someone at the Mac, with `--timeout`.
- Comments from an agent are marked `"agent":true`. A person's comment, the main path, needs them to type it in the app.
