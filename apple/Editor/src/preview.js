// Markdown, rendered: an editor pane's Preview (alone, or beside the source
// in Split). It renders the editor's own text, so what you type, and what
// an agent writes to the file, shows as it happens. Each block carries the
// source lines it came from (data-line, 0-based, to data-line-end), which
// keep the two sides scrolled together and let a task's checkbox tick the
// source.
//
// The page can write the file (its messages are trusted), so the Markdown's
// own HTML is sanitised (DOMPurify): no scripts, handlers, frames or forms.
//
// Obsidian's notes render as Obsidian shows them (obsidian.js): their
// [[links]] name files anywhere in the vault, which the app finds (the
// daemon's ResolveEditorLinks) and the preview keeps a while (LinkCache).

import DOMPurify from "dompurify"
import { highlightTree } from "@lezer/highlight"
import { LanguageDescription } from "@codemirror/language"
import { languages } from "@codemirror/language-data"
import { mix, tint } from "./theme.js"
import { createMarkdown, taskKey } from "./markdown.js"
import { stripComments, frontMatterRe, slug, CALLOUT_KINDS, calloutIcons } from "./obsidian.js"

const md = createMarkdown(highlightFence)

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
// What may differ in a block that hasn't changed: its lines, and a <details> opened.
const lineAttrs = / data-(?:task-)?line(?:-end)?="\d+"| open=""/g
const frontMatter = frontMatterRe

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

// ---- Obsidian's links, as the app finds them ----

const LINK_FRESH = 15_000 // ms an answer is used before it's asked again (a file moved, a note edited)
const LINK_WAIT = 10_000  // ms to wait for an answer before asking again
const LINK_KEEP = 5_000   // answers kept, at most

/** Where [[links]] lead (and embedded notes' text), asked of the app in a
 * batch after each render ({type: "resolveLinks"}), answered by
 * hammock.links. onChange: an answer changed what shows. */
export class LinkCache {
  constructor(post, onChange) {
    this.post = post
    this.onChange = onChange
    this.known = new Map()    // from + "\n" + target → { path, text, hasText, withText, at }
    this.asked = new Map()    // the same key (+ "\ntext") → when
    this.queue = new Map()    // from → { targets, notes }: to ask
    this.requests = new Map() // id → { from, notes }
    this.nextID = 1
  }

  /** What's known of target from the note at from: undefined (asking), null (nothing), or { path, text, hasText }. */
  lookup(from, target, wantText) {
    const key = from + "\n" + target
    const e = this.known.get(key)
    const usable = e && (!wantText || e.withText)
    if (!usable || performance.now() - e.at > LINK_FRESH) this.ask(from, target, wantText || !!e?.withText, key)
    if (!usable) return undefined
    return e.path ? { path: e.path, text: e.text, hasText: e.hasText } : null
  }

  ask(from, target, wantText, key) {
    const k = wantText ? key + "\ntext" : key
    const t = this.asked.get(k)
    if (t && performance.now() - t < LINK_WAIT) return
    this.asked.set(k, performance.now())
    let q = this.queue.get(from)
    if (!q) this.queue.set(from, q = { targets: new Set(), notes: new Set() })
    q.targets.add(target)
    if (wantText) q.notes.add(target)
  }

  /** Asks for what the render wanted. */
  flush() {
    for (const [from, q] of this.queue) {
      const id = this.nextID++
      this.requests.set(id, { from, notes: q.notes })
      this.post({ type: "resolveLinks", id, from, targets: [...q.targets], notes: [...q.notes] })
    }
    this.queue.clear()
  }

  /** The app's answer: { id, links: [{ target, path, text, hasText }] }, or { id, error }. */
  receive(r) {
    const req = this.requests.get(r?.id)
    if (!req) return
    this.requests.delete(r.id)
    if (r.error || !Array.isArray(r.links)) return // asked again after LINK_WAIT
    if (this.known.size > LINK_KEEP) this.known.clear()
    let changed = false
    for (const l of r.links) {
      const key = req.from + "\n" + l.target
      const withText = req.notes.has(l.target)
      const old = this.known.get(key)
      const next = { path: l.path || "", text: withText ? l.text || "" : "", hasText: withText && !!l.hasText, withText, at: performance.now() }
      if (!old || old.path !== next.path || old.text !== next.text || old.hasText !== next.hasText || old.withText !== next.withText) changed = true
      this.known.set(key, next)
      this.asked.delete(key)
      this.asked.delete(key + "\ntext")
    }
    if (changed) this.onChange()
  }
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
    this.path = ""
    this.reveal = null // [fromLine, toLine], 0-based: blocks an agent pointed at
    this.readOnly = false
    this.links = new LinkCache(post, () => { if (this.text !== null) this.render(this.text, true) })
    onLanguageLoaded = () => { if (this.text !== null) this.render(this.text, true) }
    host.addEventListener("click", (e) => this.click(e))
    host.addEventListener("mousedown", () => { this.reveal = null; this.markReveal(); post({ type: "focus" }) })
  }

  setFile(path) {
    const dir = path ? path.slice(0, path.lastIndexOf("/")) : ""
    if (dir === docDir && (path || "") === this.path) return
    docDir = dir
    this.path = path || ""
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
    let front = null
    const fm = frontMatter.exec(text)
    let frontLines = 0
    if (fm) {
      front = fm[1] || ""
      frontLines = fm[0].split("\n").length - (fm[0].endsWith("\n") ? 1 : 0)
      text = "\n".repeat(frontLines) + text.slice(fm[0].length)
    }
    // %%comments%% (Obsidian's) don't show; their lines stay, as blanks.
    text = stripComments(text)
    const lookup = (from, target, wantText) => this.links.lookup(from, target, wantText)
    let html = md.render(text, { wiki: { from: this.path, lookup, depth: 0, chain: [], budget: { n: 0 } } })
    this.links.flush()
    if (front !== null) html = `<pre class="front-matter" data-line="0" data-line-end="${frontLines}"><code>${escapeHTML(front)}</code></pre>` + html
    if (!text.trim() && front === null) html = `<p class="empty">Nothing to preview yet.</p>`
    const next = DOMPurify.sanitize(html, { ...purifyConfig, RETURN_DOM_FRAGMENT: true })
    for (const el of next.querySelectorAll("[data-anchor]")) el.id = el.dataset.anchor
    this.patch(next)
    for (const box of this.body.querySelectorAll("input.task[data-task-key]")) box.disabled = this.readOnly
    this.markReveal()
    this.find.refresh()
  }

  /** Takes the new blocks in place of the ones that changed, keeping the
   * rest (their images, open <details>, a selection) and renumbering their lines. */
  patch(next) {
    const old = Array.from(this.body.childNodes), fresh = Array.from(next.childNodes)
    const key = (n) => n.nodeType === 1 ? n.outerHTML.replace(lineAttrs, "") : n.nodeType === 3 ? "#" + n.nodeValue : ""
    const ok = old.map(key), nk = fresh.map(key)
    let start = 0
    while (start < ok.length && start < nk.length && ok[start] === nk[start]) start++
    let end = 0
    while (end < ok.length - start && end < nk.length - start && ok[ok.length - 1 - end] === nk[nk.length - 1 - end]) end++
    // Kept blocks take their new line numbers.
    const renumber = (o, n) => {
      if (o.nodeType !== 1) return
      const os = [o, ...o.querySelectorAll("[data-line]")], ns = [n, ...n.querySelectorAll("[data-line]")]
      if (os.length !== ns.length) return
      os.forEach((e, i) => {
        for (const a of ["data-line", "data-line-end", "data-task-line"]) {
          const v = ns[i].getAttribute(a)
          if (v !== null) e.setAttribute(a, v)
        }
      })
      for (const [i, b] of Array.from(o.querySelectorAll("input.task[data-task-key]")).entries()) {
        const nb = n.querySelectorAll("input.task[data-task-key]")[i]
        if (nb) b.dataset.taskLine = nb.dataset.taskLine
      }
    }
    for (let i = 0; i < start; i++) renumber(old[i], fresh[i])
    for (let i = 0; i < end; i++) renumber(old[old.length - 1 - i], fresh[fresh.length - 1 - i])
    const before = old[old.length - end] || null
    for (const n of old.slice(start, old.length - end)) n.remove()
    for (const n of fresh.slice(start, fresh.length - end)) this.body.insertBefore(n, before)
  }

  setReadOnly(ro) {
    this.readOnly = ro
    for (const box of this.body.querySelectorAll("input.task[data-task-key]")) box.disabled = ro
  }

  /** Scrolls to a heading (by its text, as Obsidian links them) or a ^block in this note. */
  scrollToSub(sub) {
    const last = sub.split("#").filter(Boolean).pop() || ""
    let el = null
    if (last.startsWith("^")) el = document.getElementById(last)
    else {
      el = document.getElementById(slug(last))
      if (!el || !this.body.contains(el)) {
        const want = slug(last)
        el = Array.from(this.body.querySelectorAll("h1, h2, h3, h4, h5, h6")).find((h) => !h.closest(".embed") && slug(h.textContent) === want)
      }
    }
    if (el && this.body.contains(el)) this.host.scrollTo({ top: offsetIn(el, this.host) - 12, behavior: "smooth" })
  }

  click(e) {
    const box = e.target.closest && e.target.closest("input.task")
    if (box) {
      const line = parseInt(box.dataset.taskLine, 10)
      // Only ours, on a line we can change: else the box stays as the file has it.
      const changed = !this.readOnly && box.dataset.taskKey === taskKey && line >= 0 &&
        this.editLine(line + 1, (s) => s.replace(/^((?:\s*>)*\s*(?:[-*+]|\d+[.)])\s+\[)([ xX])(\])/, (_, a, c, b) => a + (c === " " ? "x" : " ") + b))
      if (!changed) e.preventDefault()
      return
    }
    if (e.target.closest && e.target.closest("input")) { e.preventDefault(); return }
    // [[Links]]: to a note's file (and its heading, when it's this one); not found, nowhere.
    const w = e.target.closest && e.target.closest("a.wikilink")
    if (w) {
      e.preventDefault()
      if (w.classList.contains("unresolved") || w.classList.contains("pending")) return
      const path = w.dataset.path || "", sub = w.dataset.sub || ""
      if (!path || path === this.path) { if (sub) this.scrollToSub(sub) }
      else if (path.startsWith("/")) this.post({ type: "openFile", path })
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
${obsidianCSS(th, pal, { fg, bg, dim, rule, link })}`
}

// Obsidian's: highlights, tags, links found and not, embeds, callouts.
function obsidianCSS(th, pal, { fg, bg, dim, rule, link }) {
  const kinds = {
    note: pal(4, "#61afef"), abstract: pal(6, "#56b6c2"), info: pal(4, "#61afef"), todo: pal(4, "#61afef"),
    tip: pal(6, "#56b6c2"), success: pal(2, "#98c379"), question: pal(3, "#e5c07b"), warning: pal(3, "#e5c07b"),
    failure: pal(1, "#e06c75"), danger: pal(1, "#e06c75"), bug: pal(1, "#e06c75"), example: pal(5, "#c678dd"), quote: dim,
  }
  const icon = (paths) => `url("data:image/svg+xml,${encodeURIComponent(
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#000" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${paths}</svg>`)}")`
  const perKind = CALLOUT_KINDS.map((k) =>
    `.md .callout[data-callout="${k}"] { --c: ${kinds[k]}; --cb: ${tint(kinds[k], 0.1)}; --icon: ${icon(calloutIcons[k])}; }`).join("\n")
  return `
.md mark { color: inherit; background: ${tint(pal(3, "#e5c07b"), 0.38)}; border-radius: 3px; padding: 0 1px; }
.md .tag { color: ${link}; background: ${tint(link, 0.14)}; border-radius: 999px; padding: 0.05em 0.55em; font-size: 0.9em; white-space: nowrap; }
.md a.wikilink.pending { cursor: default; }
.md a.wikilink.unresolved { color: ${mix(link, bg, 0.45)}; cursor: default; text-decoration: underline dotted; text-underline-offset: 2px; }
.md a.embed-file { padding: 0.1em 0.5em; border: 1px solid ${rule}; border-radius: 5px; }
.md .embed-pending { color: ${dim}; }
.md .embed { margin: 0 0 1em; padding: 2px 0 2px 14px; border-left: 3px solid ${tint(link, 0.55)}; }
.md .embed-title { font-size: 0.85em; font-weight: 600; margin: 0.15em 0 0.45em; }
.md .embed.pending .embed-title, .md .embed.unresolved .embed-title { margin-bottom: 0.15em; }
.md .embed-body > :first-child { margin-top: 0; }
.md .embed-body > :last-child,.md .embed-body > :last-child > :last-child { margin-bottom: 0; }
.md .embed-note { color: ${dim}; font-size: 0.9em; margin: 0; }
.md .callout { --c: ${kinds.note}; --cb: ${tint(kinds.note, 0.1)}; --icon: ${icon(calloutIcons.note)};
  margin: 0 0 1em; padding: 9px 14px; border-radius: 8px; background: var(--cb); border-left: 3px solid var(--c); }
${perKind}
.md .callout-title { display: flex; align-items: center; gap: 0.45em; color: var(--c); font-weight: 600; }
.md .callout-icon { flex: none; width: 1.05em; height: 1.05em; background: currentColor;
  -webkit-mask: var(--icon) center / contain no-repeat; mask: var(--icon) center / contain no-repeat; }
.md .callout-content { margin-top: 0.45em; }
.md .callout-content:empty { display: none; }
.md .callout-content > :last-child { margin-bottom: 0; }
.md details.callout > summary { cursor: pointer; list-style: none; }
.md details.callout > summary::-webkit-details-marker { display: none; }
.md details.callout > summary::after { content: "›"; margin-left: 0.2em; font-size: 1.2em; line-height: 1; transition: transform 0.15s; }
.md details.callout[open] > summary::after { transform: rotate(90deg); }
`
}
