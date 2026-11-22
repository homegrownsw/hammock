// Files panes' page: a folder's tree, like an editor's file explorer. The
// daemon keeps what the pane shows (its folder, the folders open, the
// selection) and lists a folder at a time; the app (FilesBridge on the Mac
// and iPhone, FilesPane on Android) passes messages both ways:
//
//   to the page:   hammock.configure(c), setInfo(i), listing(l),
//                  changed(paths), command(name), focus()
//   from it:       ready, list, expand, collapse, select, open, menu,
//                  setRoot, hidden, rename, create, move, focus
//
// Menus are the app's own (a right-click posts "menu"); this draws the tree,
// and takes the keyboard and the mouse.

import { hexToRGB, mix } from "./colors.js"

const post = (m) => {
  try { window.webkit.messageHandlers.hammock.postMessage(m) } catch (e) { console.log("hammock:", JSON.stringify(m)) }
}
const $ = (id) => document.getElementById(id)

// ---- state ----

let config = { theme: { background: "#1e1e1e", foreground: "#d4d4d4", palette: [], dark: true }, accent: "", platform: "mac", canDrive: true }
let info = { root: "", followGroup: true, expanded: [], showHidden: false, selected: [], error: "", repo: "", reveal: "", revealSeq: 0 }
const listings = new Map() // folder → { entries, error, truncated }
const asked = new Set() // folders asked for and not yet answered
let expanded = new Set()
let selection = new Set()
let anchor = "" // where a shift-click range starts
let cursor = "" // the row the keyboard is on
let filter = ""
let rows = [] // what's drawn, top to bottom
let lastReveal = 0
let editing = null // { kind: "rename" | "file" | "folder", path (renamed), dir (created in), name }
// What we told the daemon and it hasn't echoed yet: kept over what it sends
// meanwhile, so a quick run of clicks doesn't flicker back.
const pendingOpen = new Map() // folder → { open, at }
let pendingSel = null // { paths, at }

// Renaming, creating, moving and trashing: Drive members, where the daemon can.
const canEdit = () => config.canDrive && config.ops !== false
const touch = () => config.platform === "ios" || config.platform === "android"
const H = () => (touch() ? 34 : 22)
const sep = "/"
const base = (p) => p.slice(p.lastIndexOf(sep) + 1) || p
const parent = (p) => { const i = p.lastIndexOf(sep); return i <= 0 ? sep : p.slice(0, i) }
const join = (d, n) => (d.endsWith(sep) ? d + n : d + sep + n)
const inside = (p, dir) => p === dir || p.startsWith(dir.endsWith(sep) ? dir : dir + sep)

// ---- icons ----

const svg = (d, extra = "") => `<svg viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`
const ICON = {
  chev: `<svg viewBox="0 0 10 10"><path d="M3 1.5 7 5 3 8.5" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
  up: svg(`<path d="M8 13V3M3.5 7.5 8 3l4.5 4.5"/>`),
  home: svg(`<path d="M2.5 7.5 8 3l5.5 4.5M4 6.5V13h8V6.5"/>`),
  find: svg(`<circle cx="7" cy="7" r="4.2"/><path d="m10.2 10.2 3.3 3.3"/>`),
  eye: svg(`<path d="M1.5 8s2.4-4.5 6.5-4.5S14.5 8 14.5 8 12.1 12.5 8 12.5 1.5 8 1.5 8Z"/><circle cx="8" cy="8" r="2"/>`),
  eyeOff: svg(`<path d="M1.5 8s2.4-4.5 6.5-4.5S14.5 8 14.5 8 12.1 12.5 8 12.5 1.5 8 1.5 8Z"/><circle cx="8" cy="8" r="2"/><path d="M2.5 2.5l11 11"/>`),
  collapse: svg(`<rect x="2.5" y="2.5" width="11" height="11" rx="2"/><path d="M5.5 8h5"/>`),
}
const folderIcon = (open) => svg(open
  ? `<path d="M1.8 4.2c0-.6.4-1 1-1h3l1.4 1.4h5c.6 0 1 .4 1 1v.9H4.3L1.8 12.3Z" fill="currentColor" fill-opacity=".18"/><path d="M1.8 12.3 4.3 6.5h10.2l-2.4 5.8Z" fill="currentColor" fill-opacity=".35"/>`
  : `<path d="M1.8 4.2c0-.6.4-1 1-1h3l1.4 1.4h5c.6 0 1 .4 1 1v6.4c0 .6-.4 1-1 1H2.8c-.6 0-1-.4-1-1Z" fill="currentColor" fill-opacity=".3"/>`)
const fileIcon = svg(`<path d="M4 1.8h5l3 3v9.4H4Z" fill="currentColor" fill-opacity=".14"/><path d="M9 1.8v3h3"/>`)

// A file's colour, by its kind (the terminal palette's: index, fallback).
const KINDS = [
  [/\.(js|mjs|cjs|jsx)$/, 3, "#e5c07b"], [/\.(ts|tsx|mts|cts)$/, 4, "#61afef"], [/\.go$/, 6, "#56b6c2"],
  [/\.swift$/, 1, "#e06c75"], [/\.(kt|kts|java|gradle)$/, 5, "#c678dd"], [/\.(py|rb|php|pl|lua)$/, 4, "#61afef"],
  [/\.(rs|c|h|cc|cpp|hpp|m|mm|zig)$/, 3, "#d19a66"], [/\.(md|mdx|txt|rst)$/, -1, ""], [/\.(json|ya?ml|toml|xml|plist|ini|conf|env)$/, 3, "#e5c07b"],
  [/\.(html?|css|scss|sass|less|svg)$/, 1, "#e06c75"], [/\.(png|jpe?g|gif|webp|heic|ico|icns|bmp|tiff?)$/, 5, "#c678dd"],
  [/\.(sh|zsh|bash|fish)$|^(justfile|makefile|dockerfile)$/i, 2, "#98c379"], [/\.(lock|sum)$/, -1, ""],
  [/\.(zip|tar|gz|tgz|xz|dmg|pkg|ipa|apk)$/, 3, "#d19a66"], [/\.pdf$/, 1, "#e06c75"],
]
function kindColor(name) {
  const n = name.toLowerCase()
  for (const [re, i, d] of KINDS) if (re.test(n)) return i < 0 ? "var(--dim)" : (config.theme.palette || [])[i] || d
  return "var(--dim)"
}

// ---- theme ----

function applyTheme() {
  const th = config.theme, p = th.palette || []
  const pal = (i, d) => p[i] || d
  const set = (k, v) => document.documentElement.style.setProperty(k, v)
  set("--bg", th.background)
  set("--fg", th.foreground)
  set("--dim", mix(th.foreground, th.background, 0.42))
  set("--faint", mix(th.foreground, th.background, 0.62))
  set("--line", mix(th.background, th.foreground, 0.13))
  set("--hover", mix(th.background, th.foreground, 0.08))
  const accent = config.accent || pal(4, "#61afef")
  set("--accent", accent)
  const [ar, ag, ab] = hexToRGB(accent)
  set("--on-accent", 0.2126 * ar + 0.7152 * ag + 0.0722 * ab > 170 ? "#111111" : "#ffffff")
  set("--add", pal(2, "#98c379"))
  set("--del", pal(1, "#e06c75"))
  set("--mod", pal(3, "#e5c07b"))
  set("--ren", pal(5, "#c678dd"))
  set("--warn", pal(1, "#e06c75"))
  if (config.font && config.font.size && !touch()) set("--ui-size", `${config.font.size}px`)
  document.body.style.background = th.background
  document.documentElement.style.colorScheme = th.dark ? "dark" : "light"
  document.body.classList.toggle("touch", touch())
}

// ---- the tree ----

function want(dir) {
  if (listings.has(dir) || asked.has(dir)) return
  asked.add(dir)
  post({ type: "list", path: dir })
}

function shown(e) { return info.showHidden || !e.name.startsWith(".") }

// fuzzy: the filter's letters, in order, in name; the indices matched (null: none).
function fuzzy(name, q) {
  const n = name.toLowerCase()
  const sub = n.indexOf(q)
  if (sub >= 0) return Array.from({ length: q.length }, (_, i) => sub + i)
  const at = []
  let j = 0
  for (let i = 0; i < n.length && j < q.length; i++) if (n[i] === q[j]) { at.push(i); j++ }
  return j === q.length ? at : null
}

function build() {
  rows = []
  const q = filter.toLowerCase()
  const walk = (dir, depth) => {
    const l = listings.get(dir)
    if (!l) {
      want(dir)
      if (!q) rows.push({ path: dir + "\0loading", name: "Loading…", depth, kind: "loading" })
      return false
    }
    if (l.error) {
      if (!q) rows.push({ path: dir + "\0error", name: l.error, depth, kind: "error" })
      return false
    }
    let any = false
    const creating = editing && editing.kind !== "rename" && editing.dir === dir
    if (creating) rows.push({ path: dir + "\0new", name: "", depth, kind: "new", dir: editing.kind === "folder" })
    for (const e of l.entries) {
      if (!shown(e)) continue
      const path = join(dir, e.name)
      const open = e.dir && expanded.has(path)
      const at = q ? fuzzy(e.name, q) : null
      const row = { path, name: e.name, depth, kind: e.dir ? "dir" : "file", e, open, at }
      const i = rows.length
      rows.push(row)
      let inner = false
      if (open) inner = walk(path, depth + 1)
      if (q && !at && !inner) rows.length = i // neither it nor anything in it matches
      else any = true
    }
    if (l.truncated && !q) rows.push({ path: dir + "\0more", name: "More files than are shown", depth, kind: "loading" })
    return any || creating
  }
  if (info.root) walk(info.root, 0)
  $("filtercount").textContent = q ? `${rows.filter((r) => r.at).length}` : ""
}

let drawn = new Map() // path → row element, of rows on screen

function draw() {
  const list = $("list"), sizer = $("sizer"), h = H()
  sizer.style.height = `${rows.length * h}px`
  const top = list.scrollTop, height = list.clientHeight
  const first = Math.max(0, Math.floor(top / h) - 10)
  const last = Math.min(rows.length, Math.ceil((top + height) / h) + 10)
  const keep = new Map()
  for (let i = first; i < last; i++) {
    const r = rows[i]
    const key = r.path + (r.kind === "new" ? "" : "")
    let el = drawn.get(key)
    const sig = rowSig(r, i)
    if (!el || el.dataset.sig !== sig) {
      const fresh = rowEl(r, i)
      if (el) el.replaceWith(fresh); else sizer.append(fresh)
      el = fresh
    }
    el.style.top = `${i * h}px`
    keep.set(key, el)
  }
  for (const [k, el] of drawn) if (!keep.has(k)) el.remove()
  drawn = keep
  const note = $("note")
  if (info.error) { note.textContent = info.error; note.classList.add("on") }
  else if (filter && rows.length === 0) { note.textContent = "No files match among the folders open"; note.classList.add("on") }
  else note.classList.remove("on")
}

function rowSig(r, i) {
  const e = r.e || {}
  return [r.kind, r.name, r.depth, r.open ? 1 : 0, selection.has(r.path) ? 1 : 0, cursor === r.path ? 1 : 0, e.git || "", e.ignored ? 1 : 0,
    e.symlink ? 1 : 0, r.at ? r.at.join(",") : "", editing && editing.kind === "rename" && editing.path === r.path ? 1 : 0, H()].join("|")
}

function rowEl(r, i) {
  const el = document.createElement("div")
  el.className = "row"
  el.dataset.path = r.path
  el.dataset.sig = rowSig(r, i)
  const indent = document.createElement("span")
  indent.className = "indent"
  indent.style.width = `${6 + r.depth * (touch() ? 16 : 14)}px`
  el.append(indent)
  const tw = document.createElement("span")
  tw.className = "tw"
  const ic = document.createElement("span")
  ic.className = "ic"
  if (r.kind === "dir" || (r.kind === "new" && r.dir)) {
    el.classList.add("dir")
    if (r.kind === "dir") tw.innerHTML = ICON.chev
    ic.innerHTML = folderIcon(r.open)
    ic.style.color = "var(--accent)"
  } else if (r.kind === "file" || r.kind === "new") {
    ic.innerHTML = fileIcon
    ic.style.color = r.kind === "new" ? "var(--dim)" : kindColor(r.name)
  }
  el.append(tw, ic)
  if (r.open) el.classList.add("open")
  if (r.kind === "loading" || r.kind === "error") el.classList.add(r.kind)
  const e = r.e
  if (e) {
    if (e.git) el.classList.add("g-" + e.git)
    if (e.ignored) el.classList.add("ignored")
    if (e.name.startsWith(".")) el.classList.add("hidden")
  }
  if (selection.has(r.path)) el.classList.add("sel")
  if (cursor === r.path) el.classList.add("cursor")
  const renaming = editing && editing.kind === "rename" && editing.path === r.path
  if (r.kind === "new" || renaming) {
    el.append(editInput(r))
  } else {
    const name = document.createElement("span")
    name.className = "name"
    if (r.at && r.at.length) {
      const set = new Set(r.at)
      for (let k = 0; k < r.name.length; k++) {
        if (set.has(k)) { const m = document.createElement("mark"); m.textContent = r.name[k]; name.append(m) }
        else name.append(r.name[k])
      }
    } else name.textContent = r.name
    el.append(name)
    if (e && e.symlink) { const l = document.createElement("span"); l.className = "link"; l.textContent = "↗"; el.append(l) }
    if (e) {
      const b = document.createElement("span")
      b.className = "badge"
      b.textContent = e.git || ""
      el.append(b)
    }
  }
  return el
}

// ---- renaming and creating, in place ----

function editInput(r) {
  const input = document.createElement("input")
  input.className = "rename"
  input.spellcheck = false
  input.autocapitalize = "off"
  input.value = editing.kind === "rename" ? base(editing.path) : ""
  let done = false
  const finish = (ok) => {
    if (done) return
    done = true
    const ed = editing
    editing = null
    post({ type: "editing", on: false })
    const name = input.value.trim()
    if (ok && name && !name.includes(sep) && name !== "." && name !== "..") {
      if (ed.kind === "rename" && name !== base(ed.path)) post({ type: "rename", path: ed.path, name })
      if (ed.kind !== "rename") post({ type: "create", dir: ed.dir, name, folder: ed.kind === "folder" })
    }
    refresh()
    $("list").focus()
  }
  input.addEventListener("keydown", (ev) => {
    ev.stopPropagation()
    if (ev.key === "Enter") { ev.preventDefault(); finish(true) }
    if (ev.key === "Escape") { ev.preventDefault(); finish(false) }
  })
  input.addEventListener("blur", () => finish(true))
  input.addEventListener("mousedown", (ev) => ev.stopPropagation())
  input.addEventListener("dblclick", (ev) => ev.stopPropagation())
  requestAnimationFrame(() => {
    input.focus()
    // The name, not its extension, as Finder does.
    const n = input.value, dot = n.lastIndexOf(".")
    input.setSelectionRange(0, dot > 0 && editing && editing.kind === "rename" && !r.e?.dir ? dot : n.length)
  })
  return input
}

function startRename(path) {
  if (!canEdit() || !path || path.includes("\0")) return
  editing = { kind: "rename", path }
  post({ type: "editing", on: true })
  refresh()
  scrollTo(path)
}

function startCreate(folder) {
  if (!canEdit()) return
  let dir = info.root
  const at = cursor && rows.find((r) => r.path === cursor)
  if (at && at.kind === "dir") dir = at.path
  else if (at && at.e) dir = parent(at.path)
  if (dir !== info.root && !expanded.has(dir)) setOpen(dir, true)
  editing = { kind: folder ? "folder" : "file", dir }
  post({ type: "editing", on: true })
  refresh()
  scrollTo(dir + "\0new")
}

function duplicate() {
  const p = selectedPaths()
  if (p.length) post({ type: "move", paths: p, dest: parent(p[0]), copy: true })
}

// ---- selection, opening ----

function refresh() { build(); draw() }

function setSelection(paths, at) {
  selection = new Set(paths)
  if (at !== undefined) cursor = at
  pendingSel = { paths: [...selection], at: Date.now() }
  post({ type: "select", paths: [...selection] })
  draw()
}

function setOpen(dir, open) {
  if (open) expanded.add(dir); else expanded.delete(dir)
  pendingOpen.set(dir, { open, at: Date.now() })
  post({ type: open ? "expand" : "collapse", paths: [dir] })
  refresh()
}

function open(r, how) {
  if (!r || !r.e) return
  if (r.kind === "dir" && !how) return setOpen(r.path, !r.open)
  post({ type: "open", path: r.path, dir: r.kind === "dir", how: how || "" })
}

function selectedPaths() {
  const s = [...selection].filter((p) => !p.includes("\0"))
  return s.length ? s : cursor && !cursor.includes("\0") ? [cursor] : []
}

function scrollTo(path) {
  const i = rows.findIndex((r) => r.path === path)
  if (i < 0) return
  const list = $("list"), h = H()
  if (i * h < list.scrollTop) list.scrollTop = i * h
  else if ((i + 1) * h > list.scrollTop + list.clientHeight) list.scrollTop = (i + 1) * h - list.clientHeight
  draw()
}

function rowAt(ev) {
  const el = ev.target.closest(".row")
  return el ? rows.find((r) => r.path === el.dataset.path) : null
}

function clickSelect(r, ev) {
  if (ev.shiftKey && anchor) {
    const a = rows.findIndex((x) => x.path === anchor), b = rows.findIndex((x) => x.path === r.path)
    if (a >= 0 && b >= 0) {
      const [lo, hi] = a < b ? [a, b] : [b, a]
      const range = rows.slice(lo, hi + 1).filter((x) => x.e).map((x) => x.path)
      return setSelection(ev.metaKey ? [...selection, ...range] : range, r.path)
    }
  }
  if (ev.metaKey || ev.ctrlKey) {
    const s = new Set(selection)
    if (s.has(r.path)) s.delete(r.path); else s.add(r.path)
    anchor = r.path
    return setSelection([...s], r.path)
  }
  anchor = r.path
  setSelection([r.path], r.path)
}

// ---- the mouse and touch ----

const list = $("list")
list.addEventListener("scroll", () => draw(), { passive: true })
new ResizeObserver(() => draw()).observe(list)

list.addEventListener("mousedown", (ev) => {
  post({ type: "focus" })
  if (ev.button !== 0) return
  const r = rowAt(ev)
  if (!r) { if (!ev.metaKey && !ev.shiftKey) setSelection([], ""); return }
  if (!r.e) return
  if (ev.target.closest(".tw") && r.kind === "dir") { setOpen(r.path, !r.open); ev.preventDefault(); return }
  if (touch()) return // a tap: on its click
  // Down on a row already selected keeps the selection, for a drag of all of it.
  if (selection.has(r.path) && !ev.metaKey && !ev.shiftKey) { cursor = r.path; downOnSelected = r.path; draw(); return }
  clickSelect(r, ev)
})
let downOnSelected = ""
list.addEventListener("click", (ev) => {
  const r = rowAt(ev)
  if (touch()) {
    if (!r || !r.e || ev.target.closest(".tw")) return
    if (longPressed) { longPressed = false; return }
    anchor = r.path
    setSelection([r.path], r.path)
    open(r) // a tap opens a file, or a folder opens or closes
    return
  }
  if (downOnSelected && r && r.path === downOnSelected && !dragging) setSelection([r.path], r.path)
  downOnSelected = ""
})
list.addEventListener("dblclick", (ev) => {
  if (touch()) return
  const r = rowAt(ev)
  if (r && !ev.target.closest(".tw")) open(r)
})
list.addEventListener("contextmenu", (ev) => {
  ev.preventDefault()
  menuFor(rowAt(ev), ev.clientX, ev.clientY)
})

function menuFor(r, x, y) {
  if (r && r.e) {
    if (!selection.has(r.path)) setSelection([r.path], r.path)
    else cursor = r.path
    draw()
  } else if (!r) setSelection([], "")
  else return
  const paths = r ? selectedPaths() : []
  post({ type: "menu", paths, dirs: paths.map((p) => rows.find((x) => x.path === p)?.kind === "dir"), x, y })
}

// Touch and hold: the app's menu (iPhone, Android).
let pressTimer = 0, longPressed = false, pressAt = null
list.addEventListener("touchstart", (ev) => {
  if (ev.touches.length !== 1) return
  const t = ev.touches[0]
  pressAt = { x: t.clientX, y: t.clientY, r: rowAt(ev) }
  longPressed = false
  clearTimeout(pressTimer)
  pressTimer = setTimeout(() => {
    longPressed = true
    menuFor(pressAt.r, pressAt.x, pressAt.y)
  }, 500)
}, { passive: true })
list.addEventListener("touchmove", (ev) => {
  const t = ev.touches[0]
  if (pressAt && Math.hypot(t.clientX - pressAt.x, t.clientY - pressAt.y) > 8) clearTimeout(pressTimer)
}, { passive: true })
list.addEventListener("touchend", () => clearTimeout(pressTimer), { passive: true })

// ---- dragging: within the tree (to move, ⌥ to copy) and out to the app ----

let dragging = null
list.addEventListener("dragstart", (ev) => ev.preventDefault()) // the page's own: ours is below
list.addEventListener("mousemove", (ev) => {
  if (touch() || !(ev.buttons & 1) || dragging || !canEdit()) return
  const r = rowAt(ev)
  if (!r || !r.e || !selection.has(r.path) || editing) return
  if (!dragStart) { dragStart = { x: ev.clientX, y: ev.clientY }; return }
  if (Math.hypot(ev.clientX - dragStart.x, ev.clientY - dragStart.y) < 5) return
  dragging = { paths: selectedPaths() }
  // The app takes the drag over (out to other panes, Finder): it posts back
  // dropIn(dir, copy) if the drop lands in this tree.
  post({ type: "drag", paths: dragging.paths, x: ev.clientX, y: ev.clientY })
})
let dragStart = null
document.addEventListener("mouseup", () => { dragStart = null; setTimeout(() => { dragging = null }, 0) })

let dropRow = null
function dropTarget(x, y) {
  const el = document.elementFromPoint(x, y)?.closest?.(".row")
  const r = el ? rows.find((q) => q.path === el.dataset.path) : null
  if (!r || !r.e) return info.root
  return r.kind === "dir" ? r.path : parent(r.path)
}
function showDrop(dir) {
  for (const el of $("sizer").children) el.classList.toggle("drop", !!dir && el.dataset.path === dir)
  dropRow = dir
}

// ---- the keyboard ----

list.addEventListener("focus", () => list.classList.add("focused"))
list.addEventListener("blur", () => list.classList.remove("focused"))
list.addEventListener("keydown", (ev) => {
  if (editing) return
  const i = rows.findIndex((r) => r.path === cursor)
  const r = rows[i]
  const move = (j) => {
    j = Math.max(0, Math.min(rows.length - 1, j))
    const to = rows[j]
    if (!to) return
    if (ev.shiftKey && to.e) {
      const a = rows.findIndex((x) => x.path === (anchor || cursor))
      const [lo, hi] = a < j ? [a, j] : [j, a]
      setSelection(rows.slice(Math.max(lo, 0), hi + 1).filter((x) => x.e).map((x) => x.path), to.path)
    } else if (to.e) {
      anchor = to.path
      setSelection([to.path], to.path)
    } else { cursor = to.path; draw() }
    scrollTo(to.path)
  }
  const page = Math.max(1, Math.floor(list.clientHeight / H()) - 1)
  const key = ev.key
  let handled = true
  if (key === "ArrowDown") ev.metaKey ? open(r) : move(i < 0 ? 0 : i + 1)
  else if (key === "ArrowUp") ev.metaKey ? goUp() : move(i < 0 ? 0 : i - 1)
  else if (key === "Home") move(0)
  else if (key === "End") move(rows.length - 1)
  else if (key === "PageDown") move(i + page)
  else if (key === "PageUp") move(i - page)
  else if (key === "ArrowRight" && r) {
    if (r.kind === "dir" && !r.open) setOpen(r.path, true)
    else if (r.kind === "dir") move(i + 1)
  } else if (key === "ArrowLeft" && r) {
    if (r.kind === "dir" && r.open) setOpen(r.path, false)
    else {
      const up = parent(r.path)
      if (up !== info.root && inside(up, info.root)) move(rows.findIndex((x) => x.path === up))
    }
  } else if (key === "Enter" && r) open(r)
  else if (key === "F2" && r) startRename(r.path)
  else if (key === "a" && ev.metaKey) setSelection(rows.filter((x) => x.e).map((x) => x.path))
  else if (key === "f" && ev.metaKey) showFilter("")
  else if (key === "z" && ev.metaKey && !ev.shiftKey && canEdit()) post({ type: "undo" })
  else if ((key === "Backspace" || key === "Delete") && ev.metaKey && canEdit()) {
    const p = selectedPaths()
    if (p.length) post({ type: "trash", paths: p })
  } else if (key === "Escape") {
    if (filter) hideFilter()
    else setSelection([], cursor)
  } else if (key.length === 1 && !ev.metaKey && !ev.ctrlKey && !ev.altKey && key !== " ") {
    showFilter(key) // typing filters, as in VS Code's explorer
  } else handled = false
  if (handled) ev.preventDefault()
})

// ---- the filter ----

function showFilter(start) {
  $("filterbar").classList.add("on")
  const input = $("filter")
  input.value = (filter && start ? filter : "") + start
  input.focus()
  setFilter(input.value)
}
function setFilter(q) {
  filter = q
  refresh()
  $("find").classList.toggle("on", $("filterbar").classList.contains("on"))
}
function hideFilter() {
  $("filterbar").classList.remove("on")
  $("filter").value = ""
  setFilter("")
}
$("filter").addEventListener("input", () => setFilter($("filter").value))
$("filter").addEventListener("keydown", (ev) => {
  if (ev.key === "Escape") { ev.preventDefault(); hideFilter(); list.focus() }
  else if (ev.key === "ArrowDown" || ev.key === "Enter") {
    ev.preventDefault()
    const first = rows.find((r) => r.e && r.at)
    list.focus()
    if (first) {
      setSelection([first.path], first.path)
      scrollTo(first.path)
      if (ev.key === "Enter") open(first)
    }
  }
})
$("filter").addEventListener("blur", () => { if (!filter) hideFilter() })

// ---- the bar ----

function drawBar() {
  const inner = $("crumbs").firstElementChild
  inner.textContent = ""
  const root = info.root || ""
  const home = config.home || ""
  // ~ for the home folder, as the sidebar shows folders.
  const crumbs = home && inside(root, home) ? [{ label: "~", path: home }] : [{ label: "/", path: sep }]
  let acc = crumbs[0].path
  for (const part of root.slice(crumbs[0].label === "~" ? home.length : 0).split(sep).filter(Boolean)) {
    acc = join(acc, part)
    crumbs.push({ label: part, path: acc })
  }
  crumbs.forEach((c, k) => {
    if (k > 0 && crumbs[k - 1].path !== sep) {
      const s = document.createElement("span")
      s.className = "sep"
      s.textContent = "/"
      inner.append(s)
    }
    const b = document.createElement("button")
    b.className = "crumb"
    b.textContent = c.label
    b.title = c.path
    b.onclick = () => { if (c.path !== info.root) post({ type: "setRoot", path: c.path }) }
    inner.append(b)
  })
  $("up").disabled = !root || root === sep
  $("home").style.display = info.followGroup ? "none" : ""
  $("hidden").innerHTML = info.showHidden ? ICON.eye : ICON.eyeOff
  $("hidden").classList.toggle("on", info.showHidden)
  $("hidden").title = info.showHidden ? "Hide Hidden Files" : "Show Hidden Files"
}

function goUp() {
  if (info.root && info.root !== sep) post({ type: "setRoot", path: parent(info.root) })
}

$("up").innerHTML = ICON.up
$("home").innerHTML = ICON.home
$("find").innerHTML = ICON.find
$("collapse").innerHTML = ICON.collapse
$("up").onclick = goUp
$("home").onclick = () => post({ type: "setRoot", path: "" })
$("find").onclick = () => ($("filterbar").classList.contains("on") ? hideFilter() : showFilter(""))
$("hidden").onclick = () => post({ type: "hidden", on: !info.showHidden })
$("collapse").onclick = () => {
  const all = [...expanded]
  if (!all.length) return
  expanded.clear()
  const at = Date.now()
  for (const d of all) pendingOpen.set(d, { open: false, at })
  post({ type: "collapse", paths: all })
  refresh()
}

// ---- from the app ----

function command(name) {
  switch (name) {
    case "rename": return startRename(cursor || selectedPaths()[0])
    case "newFile": return startCreate(false)
    case "newFolder": return startCreate(true)
    case "filter": return showFilter("")
    case "collapseAll": return $("collapse").onclick()
    case "up": return goUp()
    case "selectAll": return setSelection(rows.filter((x) => x.e).map((x) => x.path))
    case "duplicate": return duplicate()
    case "trash": { const p = selectedPaths(); if (p.length && canEdit()) post({ type: "trash", paths: p }); return }
  }
}

window.hammock = {
  configure(c) {
    config = { ...config, ...c }
    applyTheme()
    drawn.forEach((el) => el.remove())
    drawn = new Map()
    drawBar()
    refresh()
  },
  setInfo(i) {
    const rootMoved = i.root !== info.root
    info = { ...info, ...i }
    const now = Date.now()
    if (rootMoved) {
      listings.clear()
      asked.clear()
      pendingOpen.clear()
      pendingSel = null
      editing = null
      list.scrollTop = 0
    }
    expanded = new Set(info.expanded || [])
    for (const [d, p] of pendingOpen) {
      if (now - p.at > 3000 || expanded.has(d) === p.open) pendingOpen.delete(d)
      else if (p.open) expanded.add(d); else expanded.delete(d)
    }
    const sel = info.selected || []
    if (!pendingSel || now - pendingSel.at > 3000 || sel.join("\n") === pendingSel.paths.join("\n")) {
      pendingSel = null
      selection = new Set(sel)
      if (rootMoved || (cursor && !selection.has(cursor) && sel.length)) cursor = sel[sel.length - 1] || ""
    }
    drawBar()
    refresh()
    if (info.revealSeq && info.revealSeq !== lastReveal) {
      lastReveal = info.revealSeq
      cursor = info.reveal
      revealWhenListed(info.reveal)
    }
  },
  listing(l) {
    asked.delete(l.path)
    listings.set(l.path, { entries: l.entries || [], error: l.error || "", truncated: !!l.truncated })
    // Folders open inside it that are gone: forgotten.
    refresh()
    if (revealing) revealWhenListed(revealing)
  },
  changed(paths) {
    for (const p of paths) {
      if (listings.has(p)) { asked.add(p); post({ type: "list", path: p }) }
    }
  },
  relist() {
    for (const p of listings.keys()) { asked.add(p); post({ type: "list", path: p }) }
  },
  command,
  focus() { if (!editing) list.focus() },
  // The app's drag has come back over this tree (Mac): show where it'd go.
  dragOver(x, y) { showDrop(x == null ? null : dropTarget(x, y)); return dropRow },
  dropAt(x, y) { const d = dropTarget(x, y); showDrop(null); return d },
  // The app's drag is over (mouseup never reaches the page).
  dragEnded() { dragging = null; dragStart = null; downOnSelected = ""; showDrop(null) },
}

let revealing = ""
function revealWhenListed(path) {
  revealing = path
  // Every folder down to it listed first.
  for (let d = parent(path); inside(d, info.root); d = parent(d)) {
    if (!listings.has(d)) { want(d); return }
    if (d === info.root) break
  }
  revealing = ""
  build()
  scrollTo(path)
  const list = $("list"), i = rows.findIndex((r) => r.path === path)
  if (i >= 0) list.scrollTop = Math.max(0, i * H() - list.clientHeight / 3)
  draw()
}

document.addEventListener("mousedown", () => post({ type: "focus" }), true)
applyTheme()
drawBar()
if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => post({ type: "ready" }))
else post({ type: "ready" })
