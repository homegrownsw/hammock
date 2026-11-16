// Markdown, rendered: an editor pane's Preview (alone, or beside the source
// in Split). It renders the editor's own text, so what you type, and what
// an agent writes to the file, shows as it happens. Each block carries the
// source lines it came from (data-line, 0-based, to data-line-end), which
// keep the two sides scrolled together and let a task's checkbox tick the
// source.
//
// The page can write the file (its messages are trusted), so the Markdown's
// own HTML is sanitised (DOMPurify): no scripts, handlers, frames or forms.

import MarkdownIt from "markdown-it"
import DOMPurify from "dompurify"
import { highlightTree } from "@lezer/highlight"
import { LanguageDescription } from "@codemirror/language"
import { languages } from "@codemirror/language-data"
import { mix, tint } from "./theme.js"

const md = new MarkdownIt({ html: true, linkify: true, highlight: highlightFence })

// Every block's source lines.
md.core.ruler.push("hammock_lines", (state) => {
  for (const t of state.tokens) {
    if (t.map && t.nesting >= 0 && t.type !== "inline") {
      t.attrSet("data-line", String(t.map[0]))
      t.attrSet("data-line-end", String(t.map[1]))
    }
  }
})

// Headings get GitHub's ids, so #links to them work.
md.core.ruler.push("hammock_ids", (state) => {
  const seen = new Map()
  const toks = state.tokens
  for (let i = 0; i < toks.length; i++) {
    if (toks[i].type !== "heading_open") continue
    const text = (toks[i + 1]?.children || []).filter((c) => c.type === "text" || c.type === "code_inline").map((c) => c.content).join("")
    let slug = text.trim().toLowerCase().replace(/[^\p{L}\p{N}\s_-]/gu, "").replace(/\s/g, "-")
    const n = seen.get(slug) || 0
    seen.set(slug, n + 1)
    if (n) slug += "-" + n
    toks[i].attrSet("id", slug)
  }
})

// GitHub's task lists: "- [ ] x" and "- [x] x".
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
    box.content = `<input type="checkbox" class="task" data-task-line="${line}"${m[1] === " " ? "" : " checked"}>`
    inline.children.unshift(box)
    toks[i - 2].attrJoin("class", "task-item")
  }
})

// ---- code blocks, highlighted as the editor highlights ----

let highlighter = null
const parsers = new Map() // language name → parser, once loaded
const loading = new Set()
let onLanguageLoaded = () => {}

function escapeHTML(s) {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]))
}

function highlightFence(code, lang) {
  if (!lang || !highlighter) return ""
  const desc = LanguageDescription.matchLanguageName(languages, lang.split(/\s/)[0], true)
  if (!desc) return ""
  const parser = parsers.get(desc.name)
  if (!parser) {
    if (!loading.has(desc.name)) {
      loading.add(desc.name)
      desc.load().then((support) => {
        parsers.set(desc.name, support.language.parser)
        onLanguageLoaded()
      }).catch((e) => console.log("preview: language", e))
    }
    return ""
  }
  let out = "", pos = 0
  try {
    highlightTree(parser.parse(code), highlighter, (from, to, classes) => {
      if (from > pos) out += escapeHTML(code.slice(pos, from))
      out += `<span class="${classes}">${escapeHTML(code.slice(from, to))}</span>`
      pos = to
    })
  } catch (e) {
    return ""
  }
  return out + escapeHTML(code.slice(pos))
}

// ---- sanitising, and where links and images point ----

let docDir = "" // the file's folder ("" while untitled)

const external = /^[a-z][a-z0-9+.-]*:/i

DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "IMG") {
    const src = node.getAttribute("src") || ""
    if (src && !external.test(src) && !src.startsWith("//")) {
      const path = resolvePath(src)
      // The app fetches it from the daemon (EditorWebView.swift), wherever the file is.
      if (path) node.setAttribute("src", "hammock-file://local" + encodeURI(path).replace(/#/g, "%23").replace(/\?/g, "%3F"))
      else node.removeAttribute("src")
    }
    node.setAttribute("loading", "lazy")
  }
  if (node.hasAttribute("target")) node.removeAttribute("target")
})

const purifyConfig = {
  FORBID_TAGS: ["style", "form", "button", "textarea", "select", "option", "dialog"],
  FORBID_ATTR: ["style"],
}

/** A link or image's path, against the file's folder ("" if there's none). */
export function resolvePath(href) {
  const clean = href.split("#")[0].split("?")[0]
  if (!clean) return ""
  let decoded = clean
  try { decoded = decodeURIComponent(clean) } catch (e) {}
  if (decoded.startsWith("/")) return decoded
  if (!docDir) return ""
  const parts = (docDir + "/" + decoded).split("/")
  const out = []
  for (const p of parts) {
    if (p === "" || p === ".") continue
    if (p === "..") out.pop()
    else out.push(p)
  }
  return "/" + out.join("/")
}

// ---- the preview ----

export class Preview {
  /** host: the element it fills; post: to the app; edit(line, fn): change a source line. */
  constructor(host, { post, editLine }) {
    this.host = host
    this.post = post
    this.editLine = editLine
    this.find = new PreviewFind(this)
    this.body = document.createElement("article")
    this.body.className = "md"
    host.appendChild(this.body)
    this.text = null
    this.reveal = null // [fromLine, toLine], 0-based: blocks an agent pointed at
    this.readOnly = false
    onLanguageLoaded = () => { if (this.text !== null) this.render(this.text, true) }
    host.addEventListener("click", (e) => this.click(e))
    host.addEventListener("mousedown", () => { this.reveal = null; this.markReveal(); post({ type: "focus" }) })
  }

  setFile(path) {
    const dir = path ? path.slice(0, path.lastIndexOf("/")) : ""
    if (dir === docDir) return
    docDir = dir
    if (this.text !== null) this.render(this.text, true)
  }

  setTheme(config, hl) {
    highlighter = hl
    let style = document.getElementById("preview-style")
    if (!style) {
      style = document.createElement("style")
      style.id = "preview-style"
      document.head.appendChild(style)
    }
    style.textContent = previewCSS(config)
    if (this.text !== null) this.render(this.text, true)
  }

  render(text, force) {
    if (!force && text === this.text) return
    this.text = text
    // Front matter (--- at the top) shows as itself, not a rule and a paragraph;
    // blanked lines keep the rest's line numbers.
    let front = ""
    const fm = /^---\r?\n([\s\S]*?)\r?\n(?:---|\.\.\.)\r?\n/.exec(text)
    if (fm) {
      front = fm[1]
      text = "\n".repeat(fm[0].split("\n").length - 1) + text.slice(fm[0].length)
    }
    let html = md.render(text)
    if (front) html = `<pre class="front-matter" data-line="0" data-line-end="${fm[0].split("\n").length - 1}"><code>${escapeHTML(front)}</code></pre>` + html
    if (!text.trim() && !front) html = `<p class="empty">Nothing to preview yet.</p>`
    this.body.innerHTML = DOMPurify.sanitize(html, purifyConfig)
    for (const box of this.body.querySelectorAll("input.task")) box.disabled = this.readOnly
    this.markReveal()
    this.find.refresh()
  }

  setReadOnly(ro) {
    this.readOnly = ro
    for (const box of this.body.querySelectorAll("input.task")) box.disabled = ro
  }

  click(e) {
    const box = e.target.closest && e.target.closest("input.task")
    if (box) {
      const line = parseInt(box.dataset.taskLine, 10)
      if (this.readOnly || !(line >= 0)) { e.preventDefault(); return }
      this.editLine(line + 1, (s) => s.replace(/^(\s*(?:[-*+]|\d+[.)])\s+\[)([ xX])(\])/, (_, a, c, b) => a + (c === " " ? "x" : " ") + b))
      return
    }
    const a = e.target.closest && e.target.closest("a[href]")
    if (!a) return
    e.preventDefault()
    const href = a.getAttribute("href")
    if (href.startsWith("#")) {
      const id = decodeURIComponent(href.slice(1))
      const el = id && (document.getElementById(id) || document.getElementById(id.toLowerCase()))
      if (el) this.host.scrollTo({ top: el.offsetTop - 12, behavior: "smooth" })
      return
    }
    if (external.test(href)) {
      if (/^(https?|mailto):/i.test(href)) this.post({ type: "openURL", url: href })
      return
    }
    const path = resolvePath(href)
    if (path) this.post({ type: "openFile", path })
  }

  // ---- lines ↔ scroll positions ----

  blocks() {
    return Array.from(this.body.querySelectorAll("[data-line]"))
  }

  /** The 1-based source line at the top of the preview, fractional. */
  topLine() {
    const top = this.host.scrollTop
    let prev = null
    for (const el of this.blocks()) {
      const y = offsetIn(el, this.host)
      if (y > top) {
        if (!prev) return parseInt(el.dataset.line, 10) + 1
        const py = offsetIn(prev, this.host), pl = parseInt(prev.dataset.line, 10)
        const nl = parseInt(el.dataset.line, 10)
        return pl + 1 + (nl - pl) * Math.min(1, (top - py) / Math.max(1, y - py))
      }
      prev = el
    }
    return prev ? parseInt(prev.dataset.line, 10) + 1 : 1
  }

  /** Scroll so 1-based line (fractional) is at the top. */
  scrollToLine(line) {
    const l = line - 1
    let before = null, after = null
    for (const el of this.blocks()) {
      const n = parseInt(el.dataset.line, 10)
      if (n <= l) before = el
      else { after = el; break }
    }
    // Up to the first block: the top, padding and all.
    if (!before || (l <= parseInt(before.dataset.line, 10) && before === this.blocks()[0])) { this.host.scrollTop = 0; return }
    const by = offsetIn(before, this.host), bl = parseInt(before.dataset.line, 10)
    let y = by
    if (after) {
      const ay = offsetIn(after, this.host), al = parseInt(after.dataset.line, 10)
      y = by + (ay - by) * (l - bl) / Math.max(1, al - bl)
    }
    this.host.scrollTop = y
  }

  /** Point at source lines from..to (1-based): mark their blocks and show them. */
  showLines(from, to) {
    this.reveal = [from - 1, to - 1]
    const first = this.markReveal()
    if (first) {
      const y = offsetIn(first, this.host)
      const h = this.host.clientHeight
      if (y < this.host.scrollTop || y > this.host.scrollTop + h - 40) this.host.scrollTo({ top: Math.max(0, y - h / 3), behavior: "smooth" })
    }
  }

  markReveal() {
    let first = null
    for (const el of this.body.querySelectorAll(".revealed")) el.classList.remove("revealed")
    if (!this.reveal) return null
    const [from, to] = this.reveal
    // The innermost blocks overlapping the lines (not the list around them).
    for (const el of this.blocks()) {
      const s = parseInt(el.dataset.line, 10), e = parseInt(el.dataset.lineEnd, 10) - 1
      if (e < from || s > to) continue
      if (el.querySelector("[data-line]") && !(s >= from && e <= to)) continue
      if (el.parentElement.closest(".revealed")) continue
      el.classList.add("revealed")
      if (!first) first = el
    }
    return first
  }
}

// ---- find, while the preview shows alone (the editor's own find is hidden) ----

class PreviewFind {
  constructor(preview) {
    this.preview = preview
    this.ranges = []
    this.index = -1
    const bar = this.bar = document.createElement("div")
    bar.className = "find-bar"
    bar.hidden = true
    bar.innerHTML = `<input type="search" placeholder="Find" spellcheck="false" autocomplete="off"><span class="count"></span>` +
      `<button class="prev" title="Previous">‹</button><button class="next" title="Next">›</button><button class="done" title="Done">×</button>`
    preview.host.appendChild(bar)
    this.input = bar.querySelector("input")
    this.count = bar.querySelector(".count")
    this.input.addEventListener("input", () => { this.index = 0; this.search(true) })
    this.input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") { e.preventDefault(); this.step(e.shiftKey ? -1 : 1) }
      else if (e.key === "Escape") { e.preventDefault(); this.close() }
    })
    bar.querySelector(".prev").addEventListener("click", () => this.step(-1))
    bar.querySelector(".next").addEventListener("click", () => this.step(1))
    bar.querySelector(".done").addEventListener("click", () => this.close())
    bar.addEventListener("mousedown", (e) => e.stopPropagation())
  }

  get open() { return !this.bar.hidden }

  show() {
    this.bar.hidden = false
    // What's selected in the preview, as a browser's find starts with.
    const sel = window.getSelection().toString().trim()
    if (sel && !sel.includes("\n")) this.input.value = sel
    this.input.focus()
    this.input.select()
    this.search(true)
  }

  close() {
    this.bar.hidden = true
    this.ranges = []
    this.index = -1
    this.paint()
  }

  /** The document changed (re-rendered): find again, staying near the match we were on. */
  refresh() { if (this.open) this.search(false) }

  search(reveal) {
    const q = this.input.value.toLowerCase()
    this.ranges = []
    if (q) {
      const walk = document.createTreeWalker(this.preview.body, NodeFilter.SHOW_TEXT)
      for (let n = walk.nextNode(); n; n = walk.nextNode()) {
        const t = n.nodeValue.toLowerCase()
        for (let i = t.indexOf(q); i >= 0; i = t.indexOf(q, i + q.length)) {
          const r = new Range()
          r.setStart(n, i)
          r.setEnd(n, i + q.length)
          this.ranges.push(r)
        }
      }
    }
    this.index = this.ranges.length ? Math.min(Math.max(this.index, 0), this.ranges.length - 1) : -1
    this.paint()
    if (reveal) this.reveal()
  }

  step(d) {
    if (!this.open) { this.show(); return }
    if (!this.ranges.length) return
    this.index = (this.index + d + this.ranges.length) % this.ranges.length
    this.paint()
    this.reveal()
  }

  paint() {
    const hl = globalThis.CSS && CSS.highlights
    this.count.textContent = !this.input.value ? "" : this.ranges.length ? `${this.index + 1} of ${this.ranges.length}` : "No results"
    if (!hl) return
    hl.delete("find")
    hl.delete("find-current")
    if (!this.ranges.length) return
    hl.set("find", new Highlight(...this.ranges))
    hl.set("find-current", new Highlight(this.ranges[this.index]))
  }

  reveal() {
    const r = this.ranges[this.index]
    if (!r) return
    const host = this.preview.host
    const y = r.getBoundingClientRect().top - host.getBoundingClientRect().top + host.scrollTop
    if (y < host.scrollTop + 50 || y > host.scrollTop + host.clientHeight - 40) host.scrollTop = Math.max(0, y - host.clientHeight / 3)
  }
}

function offsetIn(el, host) {
  return el.getBoundingClientRect().top - host.getBoundingClientRect().top + host.scrollTop
}

function previewCSS(config) {
  const th = config.theme, p = th.palette || []
  const pal = (i, f) => p[i] || f
  const fg = th.foreground, bg = th.background
  const dim = mix(fg, bg, 0.4)
  const rule = mix(bg, fg, 0.16)
  const codeBG = mix(bg, fg, 0.06)
  const link = pal(4, "#61afef")
  const mono = `"${(config.font.family || "Menlo").replace(/"/g, "")}", ui-monospace, Menlo, monospace`
  const size = Math.max(12, (config.font.size || 13) + 2)
  return `
#preview { overflow: auto; color: ${fg}; background: ${bg}; -webkit-user-select: text; user-select: text; }
#preview .md { max-width: 820px; margin: 0 auto; padding: 22px 28px 60vh; font: ${size}px/1.6 -apple-system, system-ui, sans-serif; word-wrap: break-word; }
body.ios #preview .md { padding: 16px 16px 40vh; }
.md > :first-child { margin-top: 0; }
.md h1, .md h2, .md h3, .md h4, .md h5, .md h6 { line-height: 1.25; margin: 1.5em 0 0.6em; font-weight: 650; }
.md h1 { font-size: 1.85em; padding-bottom: 0.25em; border-bottom: 1px solid ${rule}; }
.md h2 { font-size: 1.45em; padding-bottom: 0.2em; border-bottom: 1px solid ${rule}; }
.md h3 { font-size: 1.2em; } .md h4 { font-size: 1.05em; } .md h5, .md h6 { font-size: 1em; color: ${dim}; }
.md p, .md ul, .md ol, .md blockquote, .md pre, .md table, .md dl { margin: 0 0 1em; }
.md ul, .md ol { padding-left: 1.8em; } .md li + li { margin-top: 0.25em; } .md li > p { margin-bottom: 0.4em; }
.md li.task-item { list-style: none; } .md li.task-item > input.task, .md li.task-item > p > input.task { margin: 0 0.45em 0 -1.4em; vertical-align: -1px; accent-color: ${link}; }
.md a { color: ${link}; text-decoration: none; } .md a:hover { text-decoration: underline; }
.md code { font: 0.86em ${mono}; background: ${codeBG}; padding: 0.15em 0.35em; border-radius: 4px; }
.md pre { font: 0.86em/1.5 ${mono}; background: ${codeBG}; padding: 12px 14px; border-radius: 8px; overflow: auto; }
.md pre code { font: inherit; background: none; padding: 0; border-radius: 0; }
.md pre.front-matter { color: ${dim}; background: none; border: 1px solid ${rule}; }
.md blockquote { padding: 0 1em; color: ${dim}; border-left: 3px solid ${rule}; }
.md hr { height: 1px; border: 0; background: ${rule}; margin: 1.6em 0; }
.md table { border-collapse: collapse; display: block; overflow: auto; max-width: 100%; }
.md th, .md td { border: 1px solid ${rule}; padding: 5px 12px; } .md th { font-weight: 600; background: ${codeBG}; }
.md img { max-width: 100%; }
.md del { color: ${dim}; }
.md .empty { color: ${dim}; }
.md .revealed { background: ${tint(th.selection, 0.45)}; box-shadow: 0 0 0 6px ${tint(th.selection, 0.45)}; border-radius: 2px; }
#preview ::selection { background: ${th.selection}; }
::highlight(find) { background-color: ${tint(pal(3, "#e5c07b"), 0.3)}; }
::highlight(find-current) { background-color: ${tint(pal(3, "#e5c07b"), 0.7)}; color: ${bg}; }
#preview .find-bar { position: sticky; top: 0; z-index: 2; display: flex; align-items: center; gap: 6px; padding: 6px 10px;
  background: ${mix(bg, fg, 0.04)}; border-bottom: 1px solid ${mix(bg, fg, 0.12)}; font: 12px -apple-system, system-ui, sans-serif; color: ${fg}; }
#preview .find-bar[hidden] { display: none; }
#preview .find-bar input { flex: 1; min-width: 60px; max-width: 280px; font: inherit; color: ${fg}; background: ${mix(bg, fg, 0.08)};
  border: 1px solid ${mix(bg, fg, 0.18)}; border-radius: 5px; padding: 3px 7px; outline: none; }
#preview .find-bar .count { color: ${dim}; min-width: 5em; }
#preview .find-bar button { font: 14px/1 -apple-system, system-ui, sans-serif; color: ${fg}; background: none; border: 0; padding: 2px 6px; border-radius: 4px; }
#preview .find-bar button:hover { background: ${mix(bg, fg, 0.1)}; }
body.ios #preview .find-bar { font-size: 16px; } body.ios #preview .find-bar input { font-size: 16px; }
`
}
