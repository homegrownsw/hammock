// Hammock's diff panes: a git diff laid out like a pull request's Files
// Changed, in a WKWebView on the Mac and the iPhone. The daemon works the
// diff out; the app (DiffWebView.swift) hands us the file list (load) and
// each file's two sides as we ask for them (needFile → fileText), and we
// show them with CodeMirror's merge views, mounting a file's only as it
// nears the screen so a diff of any size stays quick.

import { EditorState, Compartment, RangeSet } from "@codemirror/state"
import { EditorView, lineNumbers, highlightSpecialChars, ViewPlugin, Decoration, WidgetType, GutterMarker, gutterLineClass } from "@codemirror/view"
import { LanguageDescription } from "@codemirror/language"
import { languages } from "@codemirror/language-data"
import { MergeView, unifiedMergeView, diff as textDiff, Change, getChunks, getOriginalDoc } from "@codemirror/merge"
import { themeExtension, mix, tint, hexToRGB } from "./theme.js"

const post = (m) => {
  try { window.webkit.messageHandlers.hammock.postMessage(m) } catch (e) { console.log("hammock:", JSON.stringify(m)) }
}

// ---- state ----

let config = {
  theme: { background: "#1e1e1e", foreground: "#d4d4d4", selection: "#264f78", cursor: "#d4d4d4", palette: [], dark: true },
  font: { family: "Menlo", size: 13 },
  platform: "mac",
  // What the pane shows: the settings, with the pane's own choices over them.
  view: { style: "unified", whitespace: "show", wrap: false },
  overridden: false, // the pane has choices of its own (Use My Settings clears them)
  canDrive: true,
  // Reviews: the agent comments go to ({ id, name } or null), the agents to
  // choose from, whether you can send (not a guest), and the Send tick.
  target: null, agents: [], canSend: true,
  accent: "", // the app's (the theme's); "" the palette's blue
}
let info = { title: "", stale: false, error: "", generation: 0, files: 0, added: 0, removed: 0, source: { kind: "uncommitted" }, branch: "" }
let content = null // { generation, files, commits, branches, defaultBranch }
let order = [] // files in the tree's order
const byPath = new Map() // path → file
const texts = new Map() // path → its sides, for content's generation
const asked = new Set() // paths asked for and not yet back
const mounted = new Map() // path → { destroy, views }
// Per file, kept across refreshes: open or closed, whole file or changes only.
const fileUI = new Map()
let treeHidden = false
const closedDirs = new Set()
let activePath = ""

const $ = (id) => document.getElementById(id)
const el = (tag, cls, text) => {
  const e = document.createElement(tag)
  if (cls) e.className = cls
  if (text != null) e.textContent = text
  return e
}

const svg = {
  chevron: '<svg class="icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 6l4 4 4-4"/></svg>',
  sidebar: '<svg class="icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4"><rect x="1.5" y="2.5" width="13" height="11" rx="2"/><path d="M6 2.5v11"/></svg>',
  gear: '<svg class="icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round"><path d="M2 4h7M12 4h2M2 8h2M7 8h7M2 12h8M13 12h1"/><circle cx="10.5" cy="4" r="1.5"/><circle cx="5.5" cy="8" r="1.5"/><circle cx="11.5" cy="12" r="1.5"/></svg>',
  comment: '<svg class="icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linejoin="round"><path d="M2.5 3.5a1 1 0 0 1 1-1h9a1 1 0 0 1 1 1v6.5a1 1 0 0 1-1 1H7l-3 2.5v-2.5h-.5a1 1 0 0 1-1-1z"/></svg>',
  open: '<svg class="icon" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><path d="M9 2.5h4.5V7M13.5 2.5L7.5 8.5M11.5 9.5v3a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1h3"/></svg>',
}

// ---- helpers ----

const statusLetter = (f) => ({ modified: "M", added: "A", deleted: "D", renamed: "R", copied: "C", typeChanged: "T" })[f.status] || "M"
const statusWords = (f) => f.untracked ? "New, not yet added to git" :
  ({ M: "Modified", A: "Added", D: "Deleted", R: "Renamed", C: "Copied", T: "Type changed" })[statusLetter(f)]
const lineHeight = () => Math.round(config.font.size * 1.45)
const big = (f) => f.added + f.removed > 1000
const plural = (n, one, many) => `${n.toLocaleString()} ${n === 1 ? one : many}`

function sizeText(n) {
  if (n < 1024) return `${n} bytes`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10240 ? 1 : 0)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

function ago(unix) {
  const s = Date.now() / 1000 - unix
  if (s < 90) return "just now"
  if (s < 3600) return `${Math.round(s / 60)}m ago`
  if (s < 86400) return `${Math.round(s / 3600)}h ago`
  if (s < 86400 * 30) return `${Math.round(s / 86400)}d ago`
  return new Date(unix * 1000).toLocaleDateString()
}

function uiFor(f) {
  let u = fileUI.get(f.path)
  if (!u) {
    // Lockfiles, generated files and huge diffs start closed.
    u = { open: !(f.generated || big(f)), whole: false }
    fileUI.set(f.path, u)
  }
  return u
}

// ---- the theme, as the page's colours ----

function applyTheme() {
  const th = config.theme, p = th.palette || []
  const pal = (i, d) => p[i] || d
  const r = document.documentElement.style
  const set = (k, v) => r.setProperty(k, v)
  set("--bg", th.background)
  set("--fg", th.foreground)
  set("--dim", mix(th.foreground, th.background, 0.42))
  set("--faint", mix(th.foreground, th.background, 0.62))
  set("--line", mix(th.background, th.foreground, 0.13))
  set("--panel", mix(th.background, th.foreground, 0.035))
  set("--hover", mix(th.background, th.foreground, 0.09))
  const accent = config.accent || pal(4, "#61afef")
  set("--accent", accent)
  // Text on it: white, or black on a light one.
  const [ar, ag, ab] = hexToRGB(accent)
  set("--on-accent", 0.2126 * ar + 0.7152 * ag + 0.0722 * ab > 170 ? "#111111" : "#ffffff")
  set("--add", pal(2, "#98c379"))
  set("--del", pal(1, "#e06c75"))
  set("--mod", pal(4, "#61afef"))
  set("--ren", pal(5, "#c678dd"))
  set("--warn", pal(3, "#e5c07b"))
  set("--code", `"${(config.font.family || "Menlo").replace(/"/g, "")}", ui-monospace, Menlo, monospace`)
  set("--code-size", `${config.font.size}px`)
  document.body.style.background = th.background
  document.documentElement.style.colorScheme = th.dark ? "dark" : "light"
}

// What CodeMirror shows in a file: the editor's theme, then the diff's
// colours (red on the old side, green on the new) over it.
function diffTheme() {
  const th = config.theme
  const dim = mix(th.foreground, th.background, 0.45)
  // The backgrounds are a clear green and red of their own, not the
  // theme's: many themes' are muted, and faded they turn grey, so the
  // code's colours on them get hard to read. Faint for a changed line, more
  // for the words that changed in it (drawn over the line's).
  const add = th.dark ? "#2ea043" : "#1f9e3e", del = th.dark ? "#f85149" : "#e5383b"
  const line = th.dark ? 0.15 : 0.11, word = th.dark ? 0.32 : 0.24
  const lineDel = th.dark ? 0.13 : 0.09, wordDel = th.dark ? 0.32 : 0.2
  return EditorView.theme({
    "&": { height: "auto" },
    ".cm-content": { padding: "2px 0" },
    ".cm-gutters": { borderRight: `1px solid ${mix(th.background, th.foreground, 0.07)}` },
    "&.cm-merge-a .cm-changedLine, & .cm-deletedChunk": { backgroundColor: tint(del, lineDel) + " !important" },
    "&.cm-merge-b .cm-changedLine": { backgroundColor: tint(add, line) + " !important" },
    // Unified: a new line's text is in an <ins> as well: the line's tint is enough.
    "& ins.cm-insertedLine": { background: "none !important", textDecoration: "none" },
    // A deleted line's text is all in a <del>: only its changed words are marked.
    "& del, & .cm-deletedLine, & .cm-deletedChunk .cm-deletedLine del": { background: "none !important", textDecoration: "none" },
    "&.cm-merge-a .cm-changedText, & .cm-deletedChunk .cm-deletedText": { background: tint(del, wordDel) + " !important", borderRadius: "2px" },
    "&.cm-merge-b .cm-changedText, & .cm-insertedLine .cm-changedText": { background: tint(add, word) + " !important", borderRadius: "2px" },
    // Words fill the line's height, so marks on lines one under another meet.
    "& .cm-changedText, & .cm-deletedText": { padding: "0.14em 0", boxDecorationBreak: "clone", WebkitBoxDecorationBreak: "clone" },
    "& .cm-wholeChange .cm-changedText": { background: "none !important" },
    "&.cm-merge-a .cm-changedLineGutter, & .cm-deletedLineGutter": { background: del + " !important" },
    "&.cm-merge-b .cm-changedLineGutter": { background: add + " !important" },
    ".cm-changeGutter": { width: "3px", paddingLeft: "0" },
    ".cm-collapsedLines": {
      background: mix(th.background, th.foreground, 0.05) + " !important", color: dim, fontFamily: "-apple-system, system-ui, sans-serif",
      fontSize: "12px", padding: "4px 12px", cursor: "pointer",
      borderTop: `1px solid ${mix(th.background, th.foreground, 0.08)}`, borderBottom: `1px solid ${mix(th.background, th.foreground, 0.08)}`,
    },
    ".cm-collapsedLines:before, .cm-collapsedLines:after": { display: "none" },
    ".cm-collapsedLines:hover": { color: th.foreground },
    ".cm-activeLine": { background: "none" },
  }, { dark: th.dark })
}

// ---- whole lines ----

// A line that's new (or gone) all through is marked by its background:
// marking every word in it as changed too only makes it harder to read.
// wholeLines flags those lines (cm-wholeChange), and the theme leaves
// their words unmarked; words in lines that were edited stay marked.
const wholeLine = Decoration.line({ class: "cm-wholeChange" })
const wholeLines = ViewPlugin.fromClass(class {
  constructor(view) { this.decorations = this.build(view) }
  update(u) { if (u.docChanged || u.viewportChanged || u.transactions.length) this.decorations = this.build(u.view) }
  build(view) {
    const found = getChunks(view.state)
    if (!found) return Decoration.none
    const isA = found.side === "a", doc = view.state.doc, marks = []
    const { from: vFrom, to: vTo } = view.viewport
    for (const c of found.chunks) {
      const from = isA ? c.fromA : c.fromB, to = Math.min(isA ? c.toA : c.toB, doc.length)
      if (to < vFrom || from > vTo || from >= to) continue
      // What changed in the chunk, in this side's positions.
      const ranges = c.changes.map((ch) => isA ? [from + ch.fromA, from + ch.toA] : [from + ch.fromB, from + ch.toB]).filter(([a, b]) => b > a)
      for (let pos = from; pos < to;) {
        const line = doc.lineAt(pos)
        const text = line.text, start = text.search(/\S/)
        if (start >= 0) {
          const end = text.length - (text.length - text.trimEnd().length)
          const s = line.from + start, e = line.from + end
          // Covered, start to end, by the changes?
          let at = s, moved = true
          while (at < e && moved) {
            moved = false
            for (const [a, b] of ranges) if (a <= at && b > at) { at = b; moved = true }
            while (at < e && /\s/.test(text[at - line.from])) { at++; moved = true }
          }
          if (at >= e) marks.push(wholeLine.range(line.from))
        }
        pos = line.to + 1
      }
    }
    return Decoration.set(marks, true)
  }
}, { decorations: (v) => v.decorations })

// ---- ignoring whitespace ----

// diffIgnoringSpace compares a and b line by line, as git diff -w does:
// lines that differ only in their spaces count as the same.
function diffIgnoringSpace(a, b) {
  const la = lines(a), lb = lines(b)
  const codes = new Map()
  let next = 0x100
  const encode = (ls) => {
    let s = ""
    for (const l of ls) {
      const key = l.text.replace(/\s+/g, "")
      let c = codes.get(key)
      if (c === undefined) {
        if (next === 0xd800) next = 0xe000 // no lone surrogates
        if (next > 0xfffd) return null
        c = String.fromCharCode(next++)
        codes.set(key, c)
      }
      s += c
    }
    return s
  }
  const ea = encode(la), eb = encode(lb)
  if (ea === null || eb === null) return textDiff(a, b) // more distinct lines than we can count: the usual diff
  const at = (ls, text, i) => (i < ls.length ? ls[i].from : text.length)
  return textDiff(ea, eb).map((c) => new Change(at(la, a, c.fromA), at(la, a, c.toA), at(lb, b, c.fromB), at(lb, b, c.toB)))
}

function lines(text) {
  const out = []
  let from = 0
  while (from < text.length) {
    let end = text.indexOf("\n", from)
    if (end < 0) end = text.length
    else end += 1
    out.push({ from, text: text.slice(from, end) })
    from = end
  }
  return out
}

const sameIgnoringSpace = (a, b) => a.replace(/\s+/g, "") === b.replace(/\s+/g, "")

// ---- the bar ----

function sourceLabel() {
  if (!content && !info.title) return "Loading…"
  return info.title || "Changes"
}

function renderBar() {
  const bar = $("bar")
  bar.textContent = ""
  const tree = el("button", "btn")
  tree.innerHTML = svg.sidebar
  tree.title = treeHidden ? "Show the files" : "Hide the files"
  tree.onclick = () => { treeHidden = !treeHidden; renderTreeVisibility(); renderBar() }
  bar.append(tree)

  const src = el("button", "btn")
  src.id = "source"
  src.append(el("span", "t", sourceLabel()))
  const ch = el("span", "chev")
  ch.innerHTML = svg.chevron
  src.append(ch)
  src.title = "Choose what to compare"
  src.onclick = (e) => { e.stopPropagation(); openPop("sourcePop", src, renderSourcePop) }
  bar.append(src)

  const totals = el("span")
  totals.id = "totals"
  if (content) {
    totals.append(el("span", "files", plural(order.length, "file", "files") + " "))
    totals.append(el("span", "add", `+${info.added.toLocaleString()}`), " ", el("span", "del", `−${info.removed.toLocaleString()}`))
  }
  bar.append(totals, el("span", "spacer"))

  // Who comments go to, and sending the ones not yet sent.
  if (config.canSend) {
    const n = unsentCount()
    if (n && config.target) {
      const narrow = window.innerWidth <= 640
      const send = el("button", "btn send", narrow ? `Send ${n}` : `Send ${n} to ${agentName()}`)
      send.title = "Send every comment not yet sent, as one message"
      send.onclick = () => post({ type: "review", command: { action: "send" } })
      bar.append(send)
    }
    const to = el("button", "btn")
    to.id = "target"
    // A setting, not an action: "Agent: Claude".
    to.append(el("span", "k", "Agent:"), el("span", "t", config.target ? config.target.name : "None"))
    const ch = el("span", "chev")
    ch.innerHTML = svg.chevron
    to.append(ch)
    to.title = "The agent your comments are sent to"
    to.onclick = (e) => { e.stopPropagation(); openPop("targetPop", to, renderTargetPop) }
    bar.append(to)
  }

  const gear = el("button", "btn")
  gear.innerHTML = svg.gear
  gear.title = "Diff settings"
  gear.onclick = (e) => { e.stopPropagation(); openPop("settingsPop", gear, renderSettingsPop) }
  bar.append(gear)
}

function renderTreeVisibility() {
  $("tree").classList.toggle("hidden", treeHidden)
}

function renderStale() {
  const stale = info.stale || (content && info.generation && info.generation !== content.generation)
  $("stale").classList.toggle("on", !!stale && !info.error)
}

// ---- popovers ----

let openPopID = ""

function openPop(id, anchor, render) {
  if (openPopID === id) { closePops(); return }
  closePops()
  const pop = $(id)
  render(pop)
  pop.classList.add("on")
  $("scrim").classList.add("on")
  openPopID = id
  const r = anchor.getBoundingClientRect()
  const w = pop.offsetWidth
  let left = r.left
  if (left + w > window.innerWidth - 8) left = Math.max(8, r.right - w)
  pop.style.left = `${left}px`
  pop.style.top = `${r.bottom + 4}px`
  pop.style.maxHeight = `${window.innerHeight - r.bottom - 16}px`
  pop.style.overflowY = "auto"
}

function closePops() {
  for (const id of ["sourcePop", "settingsPop", "targetPop"]) $(id).classList.remove("on")
  $("scrim").classList.remove("on")
  openPopID = ""
}

function setSource(s) {
  closePops()
  post({ type: "setSource", source: s })
}

function renderSourcePop(pop) {
  pop.textContent = ""
  const kind = info.source?.kind || "uncommitted"
  const enabled = config.canDrive
  const item = (label, sub, on, run) => {
    const b = el("button", "item" + (on ? " on" : ""))
    b.append(el("span", null, label))
    if (sub) b.append(el("span", "sub", sub))
    b.disabled = !enabled
    b.onclick = run
    return b
  }
  pop.append(el("h4", null, "Compare"))
  pop.append(item("Uncommitted changes", "against HEAD", kind === "uncommitted", () => setSource({ kind: "uncommitted" })))

  // This branch against a base.
  const branches = content?.branches || []
  const def = content?.defaultBranch || ""
  const cur = info.branch || ""
  if (cur && branches.length) {
    const base = kind === "branch" ? (info.source.base || def) : def
    const with_ = kind === "branch" ? !!info.source.withUncommitted : true
    pop.append(item(`${cur} vs ${base}`, with_ ? "with uncommitted changes" : "", kind === "branch",
      () => setSource({ kind: "branch", base, withUncommitted: with_ })))
    const line = el("div", "line")
    const sel = el("select")
    for (const b of branches) if (b !== cur) { const o = el("option", null, b); o.value = b; o.selected = b === base; sel.append(o) }
    const box = el("input")
    box.type = "checkbox"
    box.checked = with_
    const lab = el("label")
    lab.append(box, "with uncommitted")
    const go = el("button", "btn go", "Compare")
    go.disabled = !enabled
    go.onclick = () => setSource({ kind: "branch", base: sel.value, withUncommitted: box.checked })
    line.append(el("span", null, "against"), sel, lab, go)
    pop.append(line)
  }

  // A commit.
  const commits = content?.commits || []
  if (commits.length) {
    pop.append(el("hr"), el("h4", null, "A commit"))
    const list = el("div", "commits")
    for (const c of commits) {
      const b = el("button", "item" + (kind === "commit" && info.source.rev === c.sha ? " on" : ""))
      b.append(el("span", "sha", c.sha.slice(0, 7)), el("span", "msg", c.subject), el("span", "when", ago(c.unix)))
      b.title = `${c.subject}\n${c.author}`
      b.disabled = !enabled
      b.onclick = () => setSource({ kind: "commit", rev: c.sha })
      list.append(b)
    }
    pop.append(list)
  }

  // A range.
  pop.append(el("hr"), el("h4", null, "A range"))
  const line = el("div", "line")
  const from = el("input")
  from.type = "text"
  from.placeholder = def || "main"
  from.value = kind === "range" ? info.source.base : ""
  const to = el("input")
  to.type = "text"
  to.placeholder = "HEAD"
  to.value = kind === "range" ? info.source.rev : ""
  const mb = el("input")
  mb.type = "checkbox"
  mb.checked = kind === "range" ? !!info.source.mergeBase : false
  const lab = el("label")
  lab.append(mb, "from the merge base")
  lab.title = "Compare from where the two diverged (FROM...TO)"
  const go = el("button", "btn go", "Compare")
  go.disabled = !enabled
  const run = () => setSource({ kind: "range", base: from.value.trim() || from.placeholder, rev: to.value.trim() || "HEAD", mergeBase: mb.checked })
  go.onclick = run
  for (const i of [from, to]) i.onkeydown = (e) => { if (e.key === "Enter") run() }
  line.append(from, el("span", null, ".."), to, lab, go)
  pop.append(line)
}

function setView(v) {
  post({ type: "setView", view: { ...config.view, ...v } })
}

function renderSettingsPop(pop) {
  pop.textContent = ""
  const v = config.view
  const enabled = config.canDrive
  pop.append(el("h4", null, "Layout"))
  const seg = el("div", "line")
  const s = el("div", "seg")
  for (const [style, label] of [["unified", "Unified"], ["split", "Split"]]) {
    const b = el("button", v.style === style ? "on" : "", label)
    b.disabled = !enabled
    b.onclick = () => { setView({ style }); closePops() }
    s.append(b)
  }
  seg.append(s)
  pop.append(seg)
  const toggle = (label, on, run) => {
    const b = el("button", "item" + (on ? " on" : ""), label)
    b.disabled = !enabled
    b.onclick = () => { run(); closePops() }
    return b
  }
  pop.append(el("hr"))
  pop.append(toggle("Ignore whitespace", v.whitespace === "ignore", () => setView({ whitespace: v.whitespace === "ignore" ? "show" : "ignore" })))
  pop.append(toggle("Wrap long lines", !!v.wrap, () => setView({ wrap: !v.wrap })))
  pop.append(el("hr"))
  pop.append(toggle("Open all files", false, () => { for (const f of order) uiFor(f).open = true; renderFiles(true) }))
  pop.append(toggle("Close all files", false, () => { for (const f of order) uiFor(f).open = false; renderFiles(true) }))
  if (config.overridden) {
    pop.append(el("hr"))
    pop.append(toggle("Use My Settings", false, () => post({ type: "setView", view: null })))
  }
}

// ---- the tree ----

function buildTree(files) {
  const root = { name: "", dirs: new Map(), files: [] }
  for (const f of files) {
    const parts = f.path.split("/")
    let d = root
    for (const p of parts.slice(0, -1)) {
      if (!d.dirs.has(p)) d.dirs.set(p, { name: p, dirs: new Map(), files: [] })
      d = d.dirs.get(p)
    }
    d.files.push(f)
  }
  // A folder holding only a folder shows as one: a/b/c.
  const squash = (d) => {
    for (const [k, c] of [...d.dirs]) {
      let x = c
      while (x.files.length === 0 && x.dirs.size === 1) {
        const [only] = x.dirs.values()
        x = { name: x.name + "/" + only.name, dirs: only.dirs, files: only.files }
      }
      d.dirs.set(k, x)
      squash(x)
    }
  }
  squash(root)
  return root
}

function treeOrder(d, prefix = "", out = []) {
  const dirs = [...d.dirs.values()].sort((a, b) => a.name.localeCompare(b.name))
  for (const c of dirs) treeOrder(c, prefix + c.name + "/", out)
  for (const f of [...d.files].sort((a, b) => a.path.localeCompare(b.path))) out.push(f)
  return out
}

function renderTree() {
  const list = $("tree").querySelector(".list")
  list.textContent = ""
  if (!content) return
  const q = $("filter").value.trim().toLowerCase()
  const match = (f) => !q || f.path.toLowerCase().includes(q)
  const root = buildTree(content.files.filter(match))
  const walk = (d, depth, prefix) => {
    const dirs = [...d.dirs.values()].sort((a, b) => a.name.localeCompare(b.name))
    for (const c of dirs) {
      const key = prefix + c.name
      const row = el("div", "row dir")
      row.style.paddingLeft = `${6 + depth * 12}px`
      const closed = closedDirs.has(key) && !q
      row.append(el("span", "tw", closed ? "›" : "⌄"), el("span", "name", c.name))
      row.onclick = () => { closedDirs.has(key) ? closedDirs.delete(key) : closedDirs.add(key); renderTree() }
      list.append(row)
      if (!closed) walk(c, depth + 1, key + "/")
    }
    for (const f of [...d.files].sort((a, b) => a.path.localeCompare(b.path))) {
      const row = el("div", "row leaf" + (f.path === activePath ? " active" : ""))
      row.dataset.path = f.path
      row.style.paddingLeft = `${6 + depth * 12}px`
      const st = el("span", "st " + statusLetter(f), statusLetter(f))
      st.title = statusWords(f)
      row.append(el("span", "tw"), st, el("span", "name", f.path.split("/").pop()))
      const n = el("span", "n")
      const open = threadsOf(f.path).filter((t) => t.state !== "resolved").length
      const fresh = unreadIn(f.path)
      if (open || fresh) n.append(bubble(open, fresh), " ")
      if (!f.binary && (f.added || f.removed)) n.append(el("span", "add", `+${f.added}`), " ", el("span", "del", `−${f.removed}`))
      row.append(n)
      row.title = f.oldPath ? `${f.oldPath} → ${f.path}` : f.path
      row.onclick = () => { reveal(f.path); if (window.innerWidth <= 640) { treeHidden = true; renderTreeVisibility(); renderBar() } }
      list.append(row)
    }
  }
  walk(root, 0, "")
}

function setActive(path) {
  if (path === activePath) return
  activePath = path
  for (const r of $("tree").querySelectorAll(".row.leaf")) r.classList.toggle("active", r.dataset.path === path)
  const row = $("tree").querySelector(".row.leaf.active")
  if (row) {
    const list = $("tree").querySelector(".list")
    const rr = row.getBoundingClientRect(), lr = list.getBoundingClientRect()
    if (rr.top < lr.top || rr.bottom > lr.bottom) row.scrollIntoView({ block: "nearest" })
  }
}

// reveal scrolls a file to the top, opening it.
function reveal(path) {
  const f = byPath.get(path)
  if (!f) return
  const u = uiFor(f)
  const sec = sectionOf(path)
  if (!sec) return
  if (!u.open) { u.open = true; renderSection(sec, f) }
  pin = { path, off: -10 }
  holdPin()
  setActive(path)
}

// ---- keeping your place ----

// WebKit doesn't keep the scroll steady when something above what you're
// looking at changes height (files load and unload as you scroll, at sizes
// guessed until then), so we do: pin is the file at the top and how far
// into it you are, and when heights change the scroll follows it.
let pin = null
let sectionList = []
const resizes = new ResizeObserver(() => holdPin())

// pinTop notes the file at the top (one whose header sits just below the
// top counts: that's where reveal puts it).
function pinTop() {
  const top = $("files").scrollTop
  let lo = 0, hi = sectionList.length - 1, at = -1
  while (lo <= hi) {
    const m = (lo + hi) >> 1
    if (sectionList[m].offsetTop <= top + 12) { at = m; lo = m + 1 } else hi = m - 1
  }
  const s = sectionList[Math.max(at, 0)]
  pin = s ? { path: s.dataset.path, off: top - s.offsetTop } : null
}

function holdPin() {
  const s = pin && sections.get(pin.path)
  if (!s) return
  const box = $("files"), want = s.offsetTop + pin.off
  if (Math.abs(box.scrollTop - want) > 1) box.scrollTop = want
}

// keepHeader: a file you open or close stays where it is, its header in
// view if you were inside it.
function keepHeader(path) {
  pinTop()
  if (pin && pin.path === path && pin.off > -10) pin.off = -10
}

// ---- the files ----

const sections = new Map() // path → section element
const sectionOf = (path) => sections.get(path)

let mountObserver = null, unmountObserver = null

function observers() {
  mountObserver?.disconnect()
  unmountObserver?.disconnect()
  const root = $("files")
  mountObserver = new IntersectionObserver((entries) => {
    for (const e of entries) if (e.isIntersecting && e.target.isConnected) want(e.target.dataset.path)
  }, { root, rootMargin: "1500px 0px" })
  // Far off screen: let CodeMirror go, keeping the space it took.
  unmountObserver = new IntersectionObserver((entries) => {
    // (A body replaced since isn't the file's any more.)
    for (const e of entries) if (!e.isIntersecting && e.target.isConnected) unmount(e.target.dataset.path, true)
  }, { root, rootMargin: "8000px 0px" })
}

// renderFiles lays out every file's section (bodies mount as they near the
// screen). keepScroll: stay on the file that was at the top.
function renderFiles(keepScroll) {
  const box = $("files")
  if (keepScroll) pinTop()
  const anchor = keepScroll ? pin : null
  for (const p of [...mounted.keys()]) unmount(p, false)
  box.textContent = ""
  sections.clear()
  resizes.disconnect()
  observers()
  const q = $("filter").value.trim().toLowerCase()
  for (const f of order) {
    if (q && !f.path.toLowerCase().includes(q)) continue
    const sec = el("section", "file")
    sec.dataset.path = f.path
    sections.set(f.path, sec)
    renderSection(sec, f)
    box.append(sec)
    resizes.observe(sec)
  }
  sectionList = [...sections.values()]
  renderEmpty()
  if (anchor && sections.has(anchor.path)) { pin = anchor; holdPin() } else { pin = null; box.scrollTop = 0 }
  onScroll()
}

function renderSection(sec, f) {
  unmount(f.path, false)
  const old = sec.querySelector(".fbody")
  if (old) { mountObserver?.unobserve(old); unmountObserver?.unobserve(old) }
  const u = uiFor(f)
  sec.textContent = ""
  sec.classList.toggle("closed", !u.open)
  const head = el("div", "fhead")
  const tw = el("button", "btn twisty")
  tw.innerHTML = svg.chevron
  tw.title = u.open ? "Close this file" : "Open this file"
  tw.onclick = () => { keepHeader(f.path); u.open = !u.open; renderSection(sec, f) }
  head.append(tw)
  const st = el("span", "st " + statusLetter(f), statusLetter(f))
  st.title = statusWords(f)
  head.append(st)
  const path = el("span", "path")
  if (f.oldPath) path.append(el("span", "from", f.oldPath + " → "))
  const slash = f.path.lastIndexOf("/")
  if (slash >= 0) path.append(el("span", "dir", f.path.slice(0, slash + 1)))
  path.append(el("span", null, f.path.slice(slash + 1)))
  path.title = f.oldPath ? `${f.oldPath} → ${f.path}` : f.path
  head.append(path)
  if (!f.binary) {
    const c = el("span", "counts")
    c.append(el("span", "add", `+${f.added}`), " ", el("span", "del", `−${f.removed}`))
    head.append(c)
  }
  const cc = el("span", "ccount")
  fillCount(cc, f.path)
  head.append(cc)
  if (f.untracked) head.append(el("span", "tag", "untracked"))
  if (f.generated) head.append(el("span", "tag", "generated"))
  head.append(el("span", "spacer"))
  // Whole file or the changes, where there's a difference.
  if (!f.binary && f.status !== "added" && f.status !== "deleted") {
    const seg = el("div", "seg")
    for (const [whole, label] of [[false, "Changes"], [true, "Whole File"]]) {
      const b = el("button", u.whole === whole ? "on" : "", label)
      b.onclick = () => { if (u.whole !== whole) { keepHeader(f.path); u.whole = whole; u.open = true; renderSection(sec, f) } }
      seg.append(b)
    }
    head.append(seg)
  }
  if (config.canDrive) {
    const cm = el("button", "btn")
    cm.innerHTML = svg.comment
    cm.title = "Comment on This File"
    cm.onclick = () => { u.open = true; if (sec.classList.contains("closed")) renderSection(sec, f); openComposer({ path: f.path, side: "new", line: 0, endLine: 0 }) }
    head.append(cm)
  }
  if (f.status !== "deleted") {
    const o = el("button", "btn")
    o.innerHTML = svg.open
    o.title = "Open in an Editor Pane"
    o.onclick = () => post({ type: "openFile", path: f.path })
    head.append(o)
  }
  head.ondblclick = (e) => { if (e.target === head || e.target.classList.contains("spacer")) { keepHeader(f.path); u.open = !u.open; renderSection(sec, f) } }
  sec.append(head)

  const body = el("div", "fbody")
  body.dataset.path = f.path
  sec.append(body)
  if (!u.open) return
  if ((f.generated || big(f)) && !u.loaded) {
    const n = el("div", "note")
    n.append(el("span", null, f.generated ? "A generated file: its changes aren't shown." : `A large diff: ${plural(f.added + f.removed, "line", "lines")} changed.`))
    const b = el("button", "btn", "Show Them")
    b.onclick = () => { keepHeader(f.path); u.loaded = true; renderSection(sec, f) }
    n.append(b)
    body.append(n)
    return
  }
  body.style.minHeight = `${estimate(f, u)}px`
  body.append(el("div", "loading", "Loading…"))
  mountObserver?.observe(body)
  unmountObserver?.observe(body)
}

function estimate(f, u) {
  const n = f.binary ? 6 : u.whole ? Math.max(f.added + f.removed + 40, 20) : f.added + f.removed + 8
  return Math.min(n, 6000) * lineHeight()
}

function renderEmpty() {
  const e = $("empty")
  e.textContent = ""
  let on = false
  if (info.error) {
    e.append(el("div", "big", "Nothing to show"), el("div", null, info.error))
    on = true
  } else if (content && order.length === 0) {
    e.append(el("div", "big", "No changes"), el("div", null, `Nothing differs in ${info.title || "this diff"}.`))
    on = true
  } else if (content && sections.size === 0) {
    e.append(el("div", null, "No files match the filter."))
    on = true
  }
  e.classList.toggle("on", on)
}

// want asks for path's sides (once), and mounts it when they're here.
function want(path) {
  const f = byPath.get(path)
  if (!f || mounted.has(path) || !uiFor(f).open) return
  if (texts.has(path)) { mount(path); return }
  if (asked.has(path)) return
  asked.add(path)
  post({ type: "needFile", path, generation: content.generation })
}

function unmount(path, keepHeight) {
  const m = mounted.get(path)
  if (!m) return
  const body = sectionOf(path)?.querySelector(".fbody")
  if (body && keepHeight) body.style.minHeight = `${body.offsetHeight}px`
  m.destroy()
  mounted.delete(path)
  if (body && keepHeight) {
    body.textContent = ""
    body.append(el("div", "loading", ""))
  }
}

function mount(path) {
  const f = byPath.get(path), t = texts.get(path), sec = sectionOf(path)
  if (!f || !t || !sec) return
  const u = uiFor(f)
  const body = sec.querySelector(".fbody")
  if (!body || !u.open) return
  body.textContent = ""
  // File comments, and threads whose lines aren't in what's shown.
  const top = el("div", "fthreads")
  body.append(top)
  const done = (destroy = () => {}, views = [], thr = null) => {
    body.style.minHeight = ""
    mounted.set(path, { destroy, views, thr, top })
    refreshThreads(path)
  }

  if (t.error) { body.append(el("div", "note", t.error)); return done() }
  if (t.tooLarge) {
    body.append(el("div", "note", `Too large to show here (${[t.oldSize && sizeText(t.oldSize), t.newSize && sizeText(t.newSize)].filter(Boolean).join(" → ")}).`))
    return done()
  }
  if (f.binary) {
    if (t.mime && (t.oldData || t.newData)) {
      const pics = el("div", "pics")
      const fig = (data, label, size) => {
        const fg = el("figure")
        if (data) { const img = el("img"); img.src = `data:${t.mime};base64,${data}`; fg.append(img) } else fg.append(el("div", "gone", "None"))
        fg.append(el("figcaption", null, data ? `${label} · ${sizeText(size)}` : label))
        return fg
      }
      if (f.status !== "added" && !f.untracked) pics.append(fig(t.oldData, "Before", t.oldSize))
      if (f.status !== "deleted") pics.append(fig(t.newData, "After", t.newSize))
      body.append(pics)
    } else {
      const sizes = [t.oldSize && sizeText(t.oldSize), t.newSize && sizeText(t.newSize)].filter(Boolean).join(" → ")
      body.append(el("div", "note", `A binary file${sizes ? ` (${sizes})` : ""}: not shown.`))
    }
    return done()
  }
  const ignore = config.view.whitespace === "ignore"
  if (t.oldText === t.newText || (ignore && sameIgnoringSpace(t.oldText, t.newText))) {
    body.append(el("div", "note", t.oldText === t.newText
      ? (f.oldPath ? "Renamed, without changes." : "No changes to its text.")
      : "Only whitespace changed (it's being ignored)."))
    return done()
  }

  const lang = new Compartment(), thr = new Compartment()
  const exts = [
    // Read-only, but focusable, so the code can be selected and copied;
    // no caret, and no keyboard on the iPhone.
    EditorState.readOnly.of(true), EditorView.contentAttributes.of({ inputmode: "none", "aria-readonly": "true" }),
    lineNumbers({ domEventHandlers: { mousedown: (view, block, e) => gutterDown(view, block, e, path) } }),
    highlightSpecialChars(), EditorState.tabSize.of(4),
    // The diff's colours first: of two themes, CodeMirror lets the first win.
    diffTheme(), themeExtension(config),
    config.view.wrap ? EditorView.lineWrapping : [],
    lang.of([]), wholeLines, thr.of([]),
  ]
  const collapse = u.whole ? undefined : { margin: 3, minSize: 4 }
  const diffConfig = { scanLimit: 5000, timeout: 1500, ...(ignore ? { override: diffIgnoringSpace } : {}) }
  const host = el("div")
  body.append(host)
  let views, destroy
  // A file that's all new, or all gone, has one side: one column, without
  // its every word marked as changed.
  const oneSided = !t.oldText || !t.newText
  if (config.view.style === "unified" || window.innerWidth <= 640 || oneSided) {
    const v = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: t.newText,
        extensions: [...exts, unifiedMergeView({
          original: t.oldText, mergeControls: false, highlightChanges: !oneSided, gutter: true, syntaxHighlightDeletions: true,
          collapseUnchanged: collapse, diffConfig,
        })],
      }),
    })
    views = [v]
    destroy = () => v.destroy()
  } else {
    const mv = new MergeView({
      a: { doc: t.oldText, extensions: exts }, b: { doc: t.newText, extensions: exts },
      parent: host, highlightChanges: true, gutter: true, collapseUnchanged: collapse, diffConfig,
    })
    views = [mv.a, mv.b]
    destroy = () => mv.destroy()
  }
  done(destroy, views, thr)
  // Highlighting comes when its language has loaded.
  const desc = LanguageDescription.matchFilename(languages, f.path.split("/").pop())
  if (desc) {
    desc.load().then((support) => {
      if (mounted.get(path)?.destroy !== destroy) return
      for (const v of views) v.dispatch({ effects: lang.reconfigure(support) })
    }).catch(() => {})
  }
}

// The file at the top is the tree's active one.
let scrollQueued = false
function onScroll() {
  pinTop()
  if (scrollQueued) return
  scrollQueued = true
  requestAnimationFrame(() => {
    scrollQueued = false
    const box = $("files"), top = box.scrollTop + 20
    let current = ""
    for (const [path, s] of sections) {
      if (s.offsetTop <= top) current = path
      else break
    }
    if (!current && sections.size) current = sections.keys().next().value
    setActive(current)
    markStuck(current)
    updateNewPill()
  })
}

// A header stuck to the top squares its corners: rounded, the code
// scrolling under it would show at them.
let stuckSec = null
function markStuck(path) {
  const s = sectionOf(path)
  const head = s?.querySelector(".fhead")
  const stuck = !!head && head.getBoundingClientRect().top - s.getBoundingClientRect().top > s.clientTop + 0.5
  const now = stuck ? s : null
  if (stuckSec && stuckSec !== now) stuckSec.classList.remove("stuck")
  if (now) now.classList.add("stuck")
  stuckSec = now
}

// ---- reviews: comment threads ----

// The pane's review (the daemon's: setReview), the comment being written,
// and what's typed in replies, kept as threads redraw.
let review = { id: "", threads: [] }
let composer = null // { path, side, line, endLine } (line 0: the file)
const drafts = new Map() // "new:path:side:line" or "reply:threadID" → text
const openReplies = new Set() // threads whose reply box is open
const shownResolved = new Set() // resolved threads opened to read
const shownOutdated = new Set()
const editing = new Set() // your comments being edited

const threadsOf = (path) => review.threads.filter((t) => t.path === path)
const agentName = () => config.target ? config.target.name : "the agent"

// sideOf is which side a view shows: split's left is the old one.
const sideOf = (view) => view.dom.classList.contains("cm-merge-a") ? "old" : "new"
const isUnified = (views) => views.length === 1

// posFor is where a thread's widget goes in view (after its last line), or
// -1 when its lines aren't there (shown above the file instead).
function posFor(view, t, unified) {
  const doc = view.state.doc
  if (t.side === sideOf(view)) {
    if (t.endLine > doc.lines || t.endLine < 1) return -1
    return doc.line(t.endLine).to
  }
  if (!unified || t.side !== "old") return -1
  // Unified shows the new side: an old-side thread goes after where its
  // lines were (the change they're in, or the same place among unchanged ones).
  const orig = getOriginalDoc(view.state), found = getChunks(view.state)
  if (!orig || t.endLine > orig.lines) return -1
  const at = orig.line(t.endLine).from
  let delta = 0
  for (const c of found ? found.chunks : []) {
    if (at < c.fromA) break
    if (at < c.toA) return doc.lineAt(Math.min(c.toB > c.fromB ? c.toB - 1 : c.fromB, doc.length)).to
    delta = c.toB - c.toA
  }
  const p = Math.min(Math.max(at + delta, 0), doc.length)
  return doc.lineAt(p).to
}

// A widget's height changes as it lays out (and as you type in it): the
// editor measures it again, or its line numbers drift from its lines.
// fitWidgets keeps threads and comment boxes to the width you can see of
// a view (its code can be wider, scrolling sideways), beside its gutter.
function fitWidgets(view) {
  if (view._fit) return
  const fit = () => {
    const g = view.dom.querySelector(".cm-gutters")
    const gw = g ? g.offsetWidth : 0
    view.dom.style.setProperty("--gutw", `${gw}px`)
    view.dom.style.setProperty("--wvis", `${Math.max(view.scrollDOM.clientWidth - gw, 200)}px`)
  }
  view._fit = new ResizeObserver(fit)
  view._fit.observe(view.scrollDOM)
  fit()
}

// (In a padded wrapper: the editor doesn't count a widget's margins.)
function measured(view, inner) {
  const dom = el("div", "wwrap")
  dom.append(inner)
  const ro = new ResizeObserver(() => view.requestMeasure())
  ro.observe(dom)
  dom._ro = ro
  return dom
}

class ThreadWidget extends WidgetType {
  constructor(t, key) { super(); this.t = t; this.key = key }
  eq(o) { return o.key === this.key }
  toDOM(view) { return measured(view, threadDOM(this.t)) }
  destroy(dom) { dom._ro?.disconnect() }
  ignoreEvent() { return true }
  get estimatedHeight() { return 90 + 50 * this.t.comments.length }
}

class ComposerWidget extends WidgetType {
  constructor(c) { super(); this.c = c; this.key = composerKey(c) }
  eq(o) { return o.key === this.key }
  toDOM(view) { return measured(view, composerDOM(this.c)) }
  destroy(dom) { dom._ro?.disconnect() }
  ignoreEvent() { return true }
  get estimatedHeight() { return 130 }
}

const composerKey = (c) => `new:${c.path}:${c.side}:${c.line}:${c.endLine}`
const rangeLine = Decoration.line({ class: "cm-commentRange" })
// …and their line numbers.
const rangeGutter = new class extends GutterMarker { elementClass = "cm-commentRangeGutter" }()
const threadKey = (t) => JSON.stringify([t, openReplies.has(t.id), shownResolved.has(t.id), shownOutdated.has(t.id),
  t.comments.filter((c) => editing.has(c.id)).map((c) => c.id), config.target && config.target.id, config.canSend])

// refreshThreads redraws a mounted file's threads: in its views, and above them.
function refreshThreads(path) {
  const m = mounted.get(path)
  if (!m) return
  const all = threadsOf(path)
  const placed = new Set()
  const unified = isUnified(m.views)
  for (const view of m.views) {
    const side = sideOf(view), marks = [], numbers = []
    for (const t of all) {
      if (t.line < 1) continue
      const pos = posFor(view, t, unified)
      if (pos < 0) continue
      placed.add(t.id)
      marks.push(Decoration.widget({ widget: new ThreadWidget(t, threadKey(t)), block: true, side: 1 }).range(pos))
    }
    const r = dragRange
    if (r && r.path === path && r.side === side) {
      for (let l = Math.min(r.from, r.to); l <= Math.max(r.from, r.to) && l <= view.state.doc.lines; l++) {
        marks.push(rangeLine.range(view.state.doc.line(l).from))
        numbers.push(rangeGutter.range(view.state.doc.line(l).from))
      }
    }
    const c = composer
    if (c && c.path === path && c.line > 0 && (c.side === side) && c.endLine <= view.state.doc.lines) {
      marks.push(Decoration.widget({ widget: new ComposerWidget(c), block: true, side: 2 }).range(view.state.doc.line(c.endLine).to))
      // (While dragging out another range, that's the one highlighted.)
      for (let l = c.line; l <= c.endLine && !(r && r.path === path && r.side === side); l++) {
        marks.push(rangeLine.range(view.state.doc.line(l).from))
        numbers.push(rangeGutter.range(view.state.doc.line(l).from))
      }
    }
    view.dispatch({ effects: m.thr.reconfigure([EditorView.decorations.of(Decoration.set(marks, true)), gutterLineClass.of(RangeSet.of(numbers, true))]) })
    fitWidgets(view)
  }
  // Above the file: its file comments, threads whose lines aren't shown,
  // and a new file comment.
  m.top.textContent = ""
  for (const t of all) if (!placed.has(t.id)) m.top.append(threadDOM(t))
  if (composer && composer.path === path && composer.line === 0) m.top.append(composerDOM(composer))
  m.top.style.display = m.top.childNodes.length ? "" : "none"
}

function refreshAllThreads() {
  for (const p of mounted.keys()) refreshThreads(p)
  renderTree()
  renderBar()
  for (const [path, s] of sections) {
    const b = s.querySelector(".fhead .ccount")
    if (b) fillCount(b, path)
  }
  updateNewPill()
}

// ---- finding comments, new ones especially ----

const openIn = (path) => threadsOf(path).filter((t) => t.state !== "resolved").length
const unreadIn = (path) => threadsOf(path).reduce((n, t) => n + t.comments.filter((c) => c.unread).length, 0)

// bubble is a file's comment icon: its open threads, filled with the
// accent when it has replies you haven't seen.
function bubble(open, fresh) {
  const b = el("span", "bubble" + (fresh ? " lit" : ""))
  b.innerHTML = svg.comment
  if (open) b.append(el("span", null, String(open)))
  b.title = (open ? plural(open, "open thread", "open threads") : "") + (fresh ? (open ? ", " : "") + plural(fresh, "new reply", "new replies") : "")
  return b
}

function fillCount(cc, path) {
  const open = openIn(path), fresh = unreadIn(path)
  cc.textContent = ""
  cc.style.display = open || fresh ? "" : "none"
  if (open || fresh) cc.append(bubble(open, fresh))
}

// unreadList is every reply you haven't seen, in the files' order, then
// by where they are in the file.
function unreadList() {
  const out = []
  for (const f of order) {
    const ts = threadsOf(f.path).slice().sort((a, b) => a.line - b.line)
    for (const t of ts) for (const c of t.comments) if (c.unread) out.push({ path: f.path, thread: t, comment: c })
  }
  return out
}

// where is a reply's place in the files (to tell above from below): its
// element's, once its file is showing it, else its file's.
function where(u) {
  const box = $("files")
  const elx = box.querySelector(`[data-comment="${CSS.escape(u.comment.id)}"]`)
  if (elx) return elx.getBoundingClientRect().top - box.getBoundingClientRect().top + box.scrollTop
  const s = sectionOf(u.path)
  return s ? s.offsetTop + (u.thread.line || 0) * lineHeight() : 0
}

function onScreen(u) {
  const box = $("files"), elx = box.querySelector(`[data-comment="${CSS.escape(u.comment.id)}"]`)
  if (!elx) return false
  const r = elx.getBoundingClientRect(), b = box.getBoundingClientRect()
  return r.bottom > b.top + 30 && r.top < b.bottom - 30
}

// nextUnread is the reply to go to: the first below what's on screen
// (back: the last above it), going round when there's none that way.
function nextUnread(back) {
  const list = unreadList()
  if (!list.length) return null
  const box = $("files"), top = box.scrollTop + 40, bottom = box.scrollTop + box.clientHeight - 40
  const placed = list.map((u) => ({ u, y: where(u) }))
  if (back) {
    const above = placed.filter((p) => p.y < top)
    return (above.length ? above[above.length - 1] : placed[placed.length - 1]).u
  }
  const visible = placed.find((p) => onScreen(p.u))
  if (visible) return visible.u
  const below = placed.find((p) => p.y > bottom)
  return (below || placed[0]).u
}

// jumpTo shows a reply (opening its file and its thread), flashes it, and
// marks it seen.
async function jumpTo(u) {
  if (!u) return
  const f = byPath.get(u.path)
  if (!f) return
  if (u.thread.state === "resolved") shownResolved.add(u.thread.id)
  const box = $("files")
  const frame = () => new Promise((r) => requestAnimationFrame(r))
  const find = () => box.querySelector(`[data-comment="${CSS.escape(u.comment.id)}"]`)
  let elx = find()
  if (!elx) {
    reveal(u.path)
    // Its file's sides come from the daemon, then it's drawn…
    for (let i = 0; i < 400 && !mounted.get(u.path)?.views; i++) await frame()
    const m = mounted.get(u.path)
    if (!m) return
    refreshThreads(u.path)
    // …but only near the screen: a reply far down a long file isn't
    // drawn yet. Go to its line, as the editor has it laid out, first.
    const unified = isUnified(m.views)
    for (const view of m.views) {
      const pos = posFor(view, u.thread, unified)
      if (pos < 0) continue
      const block = view.lineBlockAt(pos)
      const y = view.documentTop + block.bottom - box.getBoundingClientRect().top
      box.scrollTop += y - box.clientHeight / 3
      pinTop()
      break
    }
    for (let i = 0; i < 120 && !(elx = find()); i++) await frame()
  }
  if (!elx) return
  const r = elx.getBoundingClientRect(), b = box.getBoundingClientRect()
  box.scrollTop += r.top - b.top - box.clientHeight / 3
  pinTop()
  elx.classList.add("flash")
  setTimeout(() => elx.classList.remove("flash"), 1600)
  markSeen([u.comment.id])
}

function markSeen(ids) {
  for (const t of review.threads) for (const c of t.comments) if (ids.includes(c.id)) c.unread = false
  post({ type: "review", command: { action: "markRead", commentIds: ids } })
  refreshAllThreads()
}

// The pill over the files: how many new replies, and which way the next one is.
function updateNewPill() {
  let pill = $("newPill")
  if (!pill) {
    pill = el("button", "newpill")
    pill.id = "newPill"
    pill.onclick = () => jumpTo(nextUnread(false))
    $("main").append(pill)
  }
  const list = unreadList()
  const next = list.length ? nextUnread(false) : null
  if (!next || onScreen(next)) { pill.classList.remove("on"); return }
  const box = $("files")
  const up = where(next) < box.scrollTop
  pill.textContent = `${plural(list.length, "new reply", "new replies")} ${up ? "↑" : "↓"}`
  pill.title = "Go to the next reply you haven't seen (N; ⇧N for the one before)"
  pill.classList.toggle("up", up)
  pill.classList.add("on")
}

// ---- writing a comment ----

let drag = null
let dragRange = null // { path, side, from, to }: lines being dragged over, shown as you go
function gutterDown(view, block, e, path) {
  if (e.button !== 0) return false
  const line = view.state.doc.lineAt(block.from).number
  // Touch (no drag, no shift): with a comment box open in this file, a
  // second line stretches it over the lines between.
  if (touch() && composer && composer.path === path && composer.side === sideOf(view) && composer.line > 0) {
    const anchor = composer.anchor || composer.line
    if (line !== anchor || composer.endLine !== composer.line) {
      openComposer({ path, side: sideOf(view), line: Math.min(anchor, line), endLine: Math.max(anchor, line), anchor })
    }
    e.preventDefault()
    return true
  }
  // Shift-click: from the line the comment box is on to this one.
  if (e.shiftKey && composer && composer.path === path && composer.side === sideOf(view)) {
    openComposer({ path, side: sideOf(view), line: Math.min(composer.line, line), endLine: Math.max(composer.line, line) })
    return true
  }
  drag = { view, path, from: line }
  const lineAt = (ev) => {
    const b = view.lineBlockAtHeight(ev.clientY - view.documentTop)
    return view.state.doc.lineAt(Math.min(Math.max(b.from, 0), view.state.doc.length)).number
  }
  dragRange = { path, side: sideOf(view), from: line, to: line }
  refreshThreads(path)
  let queued = false
  const move = (ev) => {
    if (!drag) return
    const to = lineAt(ev)
    if (dragRange && to !== dragRange.to) {
      dragRange.to = to
      if (!queued) { queued = true; requestAnimationFrame(() => { queued = false; refreshThreads(path) }) }
    }
  }
  const up = (ev) => {
    window.removeEventListener("mouseup", up, true)
    window.removeEventListener("mousemove", move, true)
    const d = drag
    drag = null
    dragRange = null
    if (!d) return
    const to = lineAt(ev)
    const c = { path: d.path, side: sideOf(d.view), line: Math.min(d.from, to), endLine: Math.max(d.from, to) }
    // The lines the box is already on: it stays as it is.
    if (composer && composerKey(composer) === composerKey(c)) {
      refreshThreads(d.path)
      document.querySelector(`.composer[data-key="${CSS.escape(composerKey(c))}"] textarea`)?.focus()
      return
    }
    openComposer(c)
  }
  window.addEventListener("mousemove", move, true)
  window.addEventListener("mouseup", up, true)
  e.preventDefault()
  return true
}

function openComposer(c) {
  const was = composer
  // Stretched to more lines (or moved): what's typed comes along.
  if (was && drafts.has(composerKey(was)) && composerKey(was) !== composerKey(c)) {
    drafts.set(composerKey(c), drafts.get(composerKey(was)))
    drafts.delete(composerKey(was))
  }
  composer = c
  if (was && was.path !== c.path) refreshThreads(was.path)
  refreshThreads(c.path)
  requestAnimationFrame(() => {
    const ta = document.querySelector(`.composer[data-key="${CSS.escape(composerKey(c))}"] textarea`)
    if (ta) { ta.focus(); ta.selectionStart = ta.value.length }
  })
}

function closeComposer() {
  const was = composer
  composer = null
  if (was) { drafts.delete(composerKey(was)); refreshThreads(was.path) }
}

// touch is a phone (or tablet): no mouse to drag with.
const touch = () => config.platform === "ios" || matchMedia("(pointer: coarse)").matches

function linesText(c) {
  if (c.line === 0) return "Commenting on the file"
  const side = c.side === "old" ? " (before the change)" : ""
  const hint = touch() ? " · tap another line number for more lines" : ""
  return (c.endLine > c.line ? `Commenting on lines ${c.line}–${c.endLine}` : `Commenting on line ${c.line}`) + side + hint
}

// buttons is a comment box's row: Cancel, then keep it (Add Comment,
// Reply), and with an agent to send to, Send to it, the main one.
// submit(send) posts it; ⌘Return does the main one.
function buttons(keep, submit, cancel) {
  const row = el("div", "crow")
  const can = !!config.target && config.canSend
  const no = el("button", "btn", "Cancel")
  no.onclick = cancel
  const add = el("button", can ? "btn" : "btn primary", keep)
  add.onclick = () => submit(false)
  row.append(el("span", "spacer"), no, add)
  if (can) {
    const send = el("button", "btn primary", `Send to ${agentName()}`)
    send.onclick = () => submit(true)
    send.title = "Post it and send it to the agent now"
    add.title = "Post it, to send later (or keep as a note)"
    row.append(send)
  }
  return { row, main: () => submit(can) }
}

function textBox(key, placeholder, submit, cancel) { // submit: ⌘Return
  const ta = el("textarea")
  ta.placeholder = placeholder
  ta.value = drafts.get(key) || ""
  ta.rows = 3
  ta.oninput = () => { drafts.set(key, ta.value); grow(ta) }
  ta.onkeydown = (e) => {
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) { e.preventDefault(); submit() }
    if (e.key === "Escape") { e.preventDefault(); cancel() }
    e.stopPropagation()
  }
  requestAnimationFrame(() => grow(ta))
  return ta
}

function grow(ta) {
  ta.style.height = "auto"
  ta.style.height = `${Math.min(ta.scrollHeight + 2, 320)}px`
}

function composerDOM(c) {
  const key = composerKey(c)
  const box = el("div", "composer")
  box.dataset.key = key
  box.append(el("div", "cfor", linesText(c)))
  const submit = (send) => {
    const text = ta.value.trim()
    if (!text) return
    post({ type: "review", command: { action: "addThread", path: c.path, side: c.side, line: c.line, endLine: c.endLine, text, send } })
    drafts.delete(key)
    composer = null
    refreshThreads(c.path)
  }
  const b = buttons("Add Comment", submit, closeComposer)
  const ta = textBox(key, "Leave a comment (Markdown)", () => b.main(), closeComposer)
  box.append(ta, b.row)
  return box
}

// ---- a thread ----

function when(ms) {
  const s = (Date.now() - ms) / 1000
  if (s < 60) return "just now"
  if (s < 3600) return `${Math.floor(s / 60)}m`
  if (s < 86400) return `${Math.floor(s / 3600)}h`
  return new Date(ms).toLocaleDateString()
}

function threadDOM(t) {
  const box = el("div", `thread st-${t.state}` + (t.outdated ? " outdated" : ""))
  box.dataset.thread = t.id
  const resolvedClosed = t.state === "resolved" && !shownResolved.has(t.id)
  // A resolved thread, folded to a line.
  if (resolvedClosed) {
    const line = el("button", "tfold")
    line.append(el("span", "badge resolved", "Resolved"), el("span", "tsum", firstLine(t.comments[0]?.text || "")),
      el("span", "tcount", plural(t.comments.length, "comment", "comments")))
    line.onclick = () => { shownResolved.add(t.id); refreshThreads(t.path) }
    box.append(line)
    return box
  }
  const head = el("div", "thead")
  if (t.line > 0 && t.endLine > t.line) head.append(el("span", "tlines", `Lines ${t.line}–${t.endLine}`))
  else if (t.line > 0) head.append(el("span", "tlines", `Line ${t.line}`))
  else head.append(el("span", "tlines", "The file"))
  if (t.side === "old") head.append(el("span", "badge", "Before the change"))
  if (t.outdated) head.append(el("span", "badge outdated", "Outdated"))
  if (t.state === "addressed") head.append(el("span", "badge addressed", "Addressed"))
  if (t.state === "resolved") {
    const fold = el("button", "btn link", "Hide")
    fold.onclick = () => { shownResolved.delete(t.id); refreshThreads(t.path) }
    head.append(el("span", "badge resolved", "Resolved"), el("span", "spacer"), fold)
  }
  box.append(head)
  // Outdated: the code as it was when commented on.
  if (t.outdated && t.quote) {
    if (shownOutdated.has(t.id)) {
      const pre = el("pre", "quote", t.quote)
      box.append(pre)
    }
    const b = el("button", "btn link", shownOutdated.has(t.id) ? "Hide the code it was on" : "Show the code it was on")
    b.onclick = () => { shownOutdated.has(t.id) ? shownOutdated.delete(t.id) : shownOutdated.add(t.id); refreshThreads(t.path) }
    box.append(b)
  }
  for (const c of t.comments) box.append(commentDOM(t, c))
  // Reply, resolve.
  const actions = el("div", "tact")
  if (openReplies.has(t.id)) {
    const key = "reply:" + t.id
    const submit = (send) => {
      const text = ta.value.trim()
      if (!text) return
      post({ type: "review", command: { action: "reply", threadId: t.id, text, send } })
      drafts.delete(key)
      openReplies.delete(t.id)
      refreshThreads(t.path)
    }
    const cancel = () => { openReplies.delete(t.id); refreshThreads(t.path) }
    const b = buttons("Reply", submit, cancel)
    const ta = textBox(key, "Reply (Markdown)", () => b.main(), cancel)
    actions.append(ta, b.row)
    actions.classList.add("open")
    requestAnimationFrame(() => ta.focus())
  } else if (config.canDrive) {
    const reply = el("button", "replybox", "Reply…")
    reply.onclick = () => { openReplies.add(t.id); refreshThreads(t.path) }
    actions.append(reply)
    const st = el("button", "btn")
    if (t.state === "resolved") {
      st.textContent = "Reopen"
      st.onclick = () => post({ type: "review", command: { action: "setState", threadId: t.id, state: "open" } })
    } else {
      st.textContent = "Resolve"
      st.onclick = () => { shownResolved.delete(t.id); post({ type: "review", command: { action: "setState", threadId: t.id, state: "resolved" } }) }
    }
    actions.append(st)
  }
  box.append(actions)
  return box
}

function firstLine(s) { const l = s.split("\n")[0]; return l.length > 90 ? l.slice(0, 90) + "…" : l }

function commentDOM(t, c) {
  const box = el("div", "cmt" + (c.agent ? " agent" : "") + (c.unread ? " unread" : ""))
  box.dataset.comment = c.id
  const who = el("div", "who")
  who.append(el("span", "name", c.agent ? cap(c.author || "agent") : c.author || "You"), el("span", "when", when(c.unixMs)))
  if (c.unread) who.append(el("span", "badge new", "New"))
  if (c.delivery === "unsent") {
    const pill = el("span", "badge unsent", "Not sent")
    who.append(pill)
    if (config.target && config.canSend) {
      const b = el("button", "btn link", `Send to ${agentName()}`)
      b.onclick = () => post({ type: "review", command: { action: "send", commentIds: [c.id] } })
      who.append(b)
    }
  } else if (c.delivery === "queued") {
    const pill = el("span", "badge queued", `Queued: ${agentName()} is waiting on you`)
    pill.title = "It goes once you've answered what the agent is asking"
    who.append(pill)
  } else if (c.delivery === "sent") {
    who.append(el("span", "sent", "Sent"))
  }
  const mine = !c.agent && c.delivery === "unsent" && config.canDrive
  if (mine && !editing.has(c.id)) {
    who.append(el("span", "spacer"))
    const ed = el("button", "btn link", "Edit")
    ed.onclick = () => { editing.add(c.id); drafts.set("edit:" + c.id, c.text); refreshThreads(t.path) }
    const del = el("button", "btn link danger", "Delete")
    del.onclick = () => post({ type: "review", command: { action: "deleteComment", commentId: c.id } })
    who.append(ed, del)
  }
  box.append(who)
  if (editing.has(c.id)) {
    const key = "edit:" + c.id
    const save = () => {
      const text = ta.value.trim()
      if (!text) return
      post({ type: "review", command: { action: "editComment", commentId: c.id, text } })
      editing.delete(c.id)
      drafts.delete(key)
      refreshThreads(t.path)
    }
    const cancel = () => { editing.delete(c.id); drafts.delete(key); refreshThreads(t.path) }
    const ta = textBox(key, "", save, cancel)
    const row = el("div", "crow")
    const ok = el("button", "btn primary", "Save")
    ok.onclick = save
    const no = el("button", "btn", "Cancel")
    no.onclick = cancel
    row.append(el("span", "spacer"), no, ok)
    box.append(ta, row)
  } else {
    const body = el("div", "md")
    body.innerHTML = markdown(c.text)
    box.append(body)
  }
  if (c.unread) seen.observe(box)
  return box
}

const cap = (s) => s.charAt(0).toUpperCase() + s.slice(1)

// Agents' comments you've had on screen are read.
// A reply counts as seen once it's been on screen for a second (not as
// you scroll past it), with the window in front.
const seenTimers = new Map()
const seen = new IntersectionObserver((entries) => {
  for (const e of entries) {
    const id = e.target.dataset.comment
    if (!e.isIntersecting) {
      clearTimeout(seenTimers.get(e.target))
      seenTimers.delete(e.target)
      continue
    }
    if (seenTimers.has(e.target)) continue
    seenTimers.set(e.target, setTimeout(() => {
      seenTimers.delete(e.target)
      if (!e.target.isConnected || !document.hasFocus()) return
      seen.unobserve(e.target)
      markSeen([id])
    }, 1000))
  }
}, { threshold: 0.6 })

// ---- a little Markdown ----

const escapeHTML = (s) => s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c])

// markdown renders the Markdown comments use: paragraphs, lists, quotes,
// code (fenced and inline), bold, italics and links (web ones only).
function markdown(src) {
  const out = []
  const lines = src.replace(/\r\n?/g, "\n").split("\n")
  let i = 0
  const inline = (s) => {
    const codes = []
    s = s.replace(/`([^`]+)`/g, (_, c) => { codes.push(c); return `\u0000${codes.length - 1}\u0000` })
    s = escapeHTML(s)
    s = s.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>").replace(/(^|[^*\w])\*([^*\s][^*]*)\*/g, "$1<em>$2</em>")
      .replace(/(^|[^_\w])_([^_\s][^_]*)_/g, "$1<em>$2</em>")
      .replace(/\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)/g, '<a href="$2">$1</a>')
      .replace(/(^|[\s(])(https?:\/\/[^\s<)]+)/g, '$1<a href="$2">$2</a>')
    return s.replace(/\u0000(\d+)\u0000/g, (_, n) => `<code>${escapeHTML(codes[+n])}</code>`)
  }
  while (i < lines.length) {
    const l = lines[i]
    if (/^```/.test(l)) {
      const code = []
      i++
      while (i < lines.length && !/^```/.test(lines[i])) code.push(lines[i++])
      i++
      out.push(`<pre><code>${escapeHTML(code.join("\n"))}</code></pre>`)
      continue
    }
    if (/^\s*[-*+]\s+/.test(l) || /^\s*\d+[.)]\s+/.test(l)) {
      const ordered = /^\s*\d/.test(l)
      const items = []
      while (i < lines.length && (/^\s*[-*+]\s+/.test(lines[i]) || /^\s*\d+[.)]\s+/.test(lines[i]))) {
        items.push(`<li>${inline(lines[i].replace(/^\s*([-*+]|\d+[.)])\s+/, ""))}</li>`)
        i++
      }
      out.push(ordered ? `<ol>${items.join("")}</ol>` : `<ul>${items.join("")}</ul>`)
      continue
    }
    if (/^>\s?/.test(l)) {
      const q = []
      while (i < lines.length && /^>\s?/.test(lines[i])) q.push(lines[i++].replace(/^>\s?/, ""))
      out.push(`<blockquote>${inline(q.join(" "))}</blockquote>`)
      continue
    }
    if (!l.trim()) { i++; continue }
    const para = []
    while (i < lines.length && lines[i].trim() && !/^```|^\s*[-*+]\s+|^\s*\d+[.)]\s+|^>/.test(lines[i])) para.push(lines[i++])
    out.push(`<p>${para.map(inline).join("<br>")}</p>`)
  }
  return out.join("")
}

// ---- who comments go to ----

function renderTargetPop(pop) {
  pop.textContent = ""
  pop.append(el("h4", null, "Send comments to"))
  const agents = config.agents || []
  if (!agents.length) pop.append(el("div", "line", "No agents running in this Group."))
  for (const a of agents) {
    const b = el("button", "item" + (config.target && config.target.id === a.id ? " on" : ""))
    b.append(el("span", null, a.name))
    if (a.detail) b.append(el("span", "sub", a.detail))
    b.onclick = () => { closePops(); post({ type: "review", command: { action: "setTarget", targetPaneId: a.id } }) }
    pop.append(b)
  }
  if (config.target) {
    pop.append(el("hr"))
    const none = el("button", "item", "No One")
    none.onclick = () => { closePops(); post({ type: "review", command: { action: "setTarget", targetPaneId: "" } }) }
    pop.append(none)
  }
}

function unsentCount() {
  let n = 0
  for (const t of review.threads) for (const c of t.comments) if (c.delivery === "unsent") n++
  return n
}

// ---- what the app calls (window.hammock) ----

window.hammock = {
  /** Theme, font and the effective view; anything left out stays. */
  configure(c) {
    const before = JSON.stringify([config.theme, config.font, config.view])
    config = { ...config, ...c, theme: { ...config.theme, ...(c.theme || {}) }, font: { ...config.font, ...(c.font || {}) }, view: { ...config.view, ...(c.view || {}) } }
    applyTheme()
    renderBar()
    if (content && JSON.stringify([config.theme, config.font, config.view]) !== before) renderFiles(true)
    else if (content) for (const p of mounted.keys()) refreshThreads(p)
  },

  /** The pane's DiffInfo: title, totals, stale, error. */
  setInfo(i) {
    const was = info
    info = { ...info, ...i }
    renderBar()
    renderStale()
    if (was.error !== info.error) renderEmpty()
  },

  /** The diff's files (a new generation: what's shown starts again). */
  load(c) {
    const fresh = !content || content.generation !== c.generation
    content = c
    order = treeOrder(buildTree(c.files))
    byPath.clear()
    for (const f of c.files) byPath.set(f.path, f)
    if (fresh) { texts.clear(); asked.clear() }
    // Files that have gone forget their state.
    for (const p of [...fileUI.keys()]) if (!byPath.has(p)) fileUI.delete(p)
    renderBar()
    renderStale()
    renderTree()
    renderFiles(true)
  },

  /** A file's two sides (data in base64). */
  fileText(t) {
    if (!content || t.generation !== content.generation) return
    asked.delete(t.path)
    texts.set(t.path, t)
    mount(t.path)
  },

  fileError(path, generation, message) {
    if (!content || generation !== content.generation) return
    asked.delete(path)
    texts.set(path, { error: message })
    mount(path)
  },

  /** Scroll to a file (an agent pointing at one). */
  reveal(path) { reveal(path) },

  /** The next reply you haven't seen (the app's "new replies" pill). */
  nextUnread() { jumpTo(nextUnread(false)) },

  /** The pane's review: its threads. */
  setReview(r) {
    review = { id: r.id || "", threads: r.threads || [] }
    refreshAllThreads()
  },
}

// ---- wiring ----

$("refresh").onclick = () => post({ type: "refresh" })
$("scrim").onclick = closePops
$("files").addEventListener("scroll", onScroll, { passive: true })
$("filter").addEventListener("input", () => { renderTree(); renderFiles(false) })
$("filter").addEventListener("keydown", (e) => { if (e.key === "Escape") { e.target.value = ""; renderTree(); renderFiles(false) } })
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closePops()
  // N / ⇧N: the next (or previous) reply you haven't seen; not while typing.
  const typing = e.target instanceof HTMLTextAreaElement || e.target instanceof HTMLInputElement
  if (!typing && !e.metaKey && !e.ctrlKey && !e.altKey && e.key.toLowerCase() === "n") {
    e.preventDefault()
    jumpTo(nextUnread(e.shiftKey))
  }
})
document.addEventListener("mousedown", () => post({ type: "focus" }), true)
window.addEventListener("resize", () => { if (openPopID) closePops(); renderBar() })
if (window.innerWidth <= 640) treeHidden = true
applyTheme()
renderBar()
renderTreeVisibility()

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => post({ type: "ready" }))
else post({ type: "ready" })
