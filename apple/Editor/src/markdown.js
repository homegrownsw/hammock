// Markdown as the preview renders it (preview.js): markdown-it, with
// Obsidian's flavour (obsidian.js), each block's source lines, GitHub's
// heading ids and task lists. No DOM here: tests run it in Node
// (apple/Editor/test).

import MarkdownIt from "markdown-it"
import { obsidian, slug } from "./obsidian.js"

/** The checkboxes we make carry this, so ones in the Markdown's own HTML,
 * which can't know it, don't tick a line. */
export const taskKey = Math.random().toString(36).slice(2, 12)

/** highlight(code, lang): a fenced block's HTML, or "" to escape it as it is.
 * withObsidian: false for Markdown without Obsidian's (tests compare the two). */
export function createMarkdown(highlight, { withObsidian = true } = {}) {
  const md = new MarkdownIt({ html: true, linkify: true, highlight })
  if (withObsidian) md.use(obsidian)

  // An embedded note's blocks (obsidian.js) aren't this file's: no source
  // lines (they'd scroll and tick the wrong file), no ids (they'd clash).
  const own = (state) => !state.env.wiki?.embedded

  // Every block's source lines.
  md.core.ruler.push("hammock_lines", (state) => {
    if (!own(state)) return
    for (const t of state.tokens) {
      if (t.map && t.nesting >= 0 && t.type !== "inline") {
        t.attrSet("data-line", String(t.map[0]))
        t.attrSet("data-line-end", String(t.map[1]))
      }
    }
  })

  // Headings get GitHub's ids, so #links to them work.
  md.core.ruler.push("hammock_ids", (state) => {
    if (!own(state)) return
    const seen = new Map()
    const toks = state.tokens
    for (let i = 0; i < toks.length; i++) {
      if (toks[i].type !== "heading_open") continue
      const text = (toks[i + 1]?.children || []).filter((c) => c.type === "text" || c.type === "code_inline").map((c) => c.content).join("")
      let s = slug(text)
      const n = seen.get(s) || 0
      seen.set(s, n + 1)
      if (n) s += "-" + n
      // Set as the id after sanitising: DOMPurify drops ids named like
      // document's properties ("scripts", "images", "title"), and these are ours.
      toks[i].attrSet("data-anchor", s)
    }
  })

  // GitHub's task lists: "- [ ] x" and "- [x] x". An embedded note's can't be ticked here.
  md.core.ruler.push("hammock_tasks", (state) => {
    const toks = state.tokens
    for (let i = 2; i < toks.length; i++) {
      const inline = toks[i]
      if (inline.type !== "inline" || toks[i - 1].type !== "paragraph_open" || toks[i - 2].type !== "list_item_open") continue
      const first = inline.children?.[0]
      const m = first && first.type === "text" && /^\[([ xX])\][ \t]/.exec(first.content)
      if (!m) continue
      first.content = first.content.slice(m[0].length)
      const box = new state.Token("html_inline", "", 0)
      const line = toks[i - 2].map ? toks[i - 2].map[0] : -1
      const checked = m[1] === " " ? "" : " checked"
      box.content = own(state)
        ? `<input type="checkbox" class="task" data-task-line="${line}" data-task-key="${taskKey}"${checked}>`
        : `<input type="checkbox" class="task" disabled${checked}>`
      inline.children.unshift(box)
      toks[i - 2].attrJoin("class", "task-item")
    }
  })

  return md
}
