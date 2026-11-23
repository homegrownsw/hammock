// The agent view's conversation, drawn in a web view (agent.html): an
// experiment beside the native one (AgentViews.swift), which it matches row
// for row. The app keeps the model (AgentSession) and sends the rows that
// changed (AgentWebView.swift); this page draws them and owns the scrolling:
// following the end as rows arrive, keeping your place when rows above you
// change, Latest, and earlier messages. The composer and prompt cards are the
// app's own, floating over the page (its bottom inset).

import MarkdownIt from "markdown-it"

const post = (m) => { try { window.webkit.messageHandlers.hammock.postMessage(m) } catch (e) { /* not in the app */ } }
const $ = (id) => document.getElementById(id)
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]))
/** What the scrolling did, to the app's DevLog (the agent view's). */
window.addEventListener("error", (e) => post({ type: "log", text: `error: ${e.message} at ${e.lineno}:${e.colno}` }))
const log = (what) => post({ type: "log", text: `${what}: top ${Math.round(se?.scrollTop ?? 0)} max ${Math.round(maxTop?.() ?? 0)} view ${se?.clientHeight} insets ${insetTop}/${insetBottom} follow ${follow}` })
const firstLine = (s) => String(s || "").split("\n")[0]

// ---- what the app sets ----

let cfg = { platform: "mac", codeWrap: "prose", canOpenDiff: false, agentName: "Claude" }
const symbolNames = new Set()

function configure(c) {
  cfg = { ...cfg, ...c }
  const r = document.documentElement.style
  const f = c.fonts || {}
  if (f.family) r.setProperty("--font", f.family)
  if (f.size) r.setProperty("--size", f.size + "px")
  if (f.lineHeight) r.setProperty("--lh", f.lineHeight + "px")
  if (f.gap != null) r.setProperty("--gap", f.gap + "px")
  if (f.code) r.setProperty("--code", f.code)
  if (f.codeSize) r.setProperty("--code-size", f.codeSize + "px")
  if (f.codeLineHeight) r.setProperty("--code-lh", f.codeLineHeight + "px")
  if (f.spacing != null) r.setProperty("--sp", f.spacing + "px")
  // SwiftUI sets SF's tracking for the size; WebKit's can differ (its SF
  // Mono was 4% narrower). The app measured a sample in its own fonts: the
  // difference, spread over its letters, is the page's letter spacing.
  if (c.calibrate) {
    for (const [key, family, size, prop] of [["body", f.family, f.size, "--track"], ["code", f.code, f.codeSize, "--code-track"]]) {
      const cal = c.calibrate[key]
      if (!cal) continue
      const m = $("measure")
      m.style.font = `${size}px ${family}`
      m.style.letterSpacing = "0px"
      m.textContent = cal.text
      const web = m.getBoundingClientRect().width
      const track = (cal.width - web) / cal.text.length
      r.setProperty(prop, (Math.abs(track) < 0.01 ? 0 : track) + "px")
      post({ type: "log", text: `${key}: native ${cal.width.toFixed(1)} web ${web.toFixed(1)} track ${track.toFixed(3)}` })
    }
  }
  for (const k of ["caption", "caption2", "callout", "button"]) if (c.metrics?.[k]) r.setProperty("--" + k, c.metrics[k] + "px")
  for (const mode of ["light", "dark"]) {
    for (const [k, v] of Object.entries(c.colors?.[mode] || {})) r.setProperty(`--${mode[0]}-${k}`, v)
  }
  if (c.symbols) {
    let css = ""
    for (const [name, s] of Object.entries(c.symbols)) {
      symbolNames.add(name)
      css += `.sf-${cls(name)}{--m:url(${s.url});--w:${s.w};--h:${s.h}}\n`
    }
    let style = $("symbols")
    if (!style) { style = document.createElement("style"); style.id = "symbols"; document.head.appendChild(style) }
    style.textContent = css
  }
  document.body.classList.toggle("touch", cfg.platform === "ios")
  if (c.fonts || c.codeWrap || c.canOpenDiff != null) rebuildAll()
}

const cls = (name) => name.replace(/\./g, "-")
/** An SF Symbol, in the text's colour and at its size. */
function sym(name, extra = "") {
  return `<i class="sf sf-${cls(name)}${extra ? " " + extra : ""}" aria-hidden="true"></i>`
}

// ---- Markdown, as the native view reads it ----

// Bare absolute paths become file links, as Markdown.inline makes them.
const barePath = /(?<![\w`(\[/])(\/(?:Users|tmp|private|var|opt|Volumes)\/[^\s`)\]"'<>]+[^\s`)\]"'<>.,:;])/g

function createMd() {
  // Single newlines break lines, as the native view keeps them.
  const md = new MarkdownIt({ html: false, linkify: true, breaks: true })
  md.renderer.rules.fence = (toks, i) => {
    const t = toks[i]
    return codeBlock(t.content.replace(/\n$/, ""), (t.info || "").trim().split(/\s/)[0])
  }
  md.renderer.rules.code_block = (toks, i) => codeBlock(toks[i].content.replace(/\n$/, ""), "")
  md.renderer.rules.table_open = () => `<div class="tbl"><table>`
  md.renderer.rules.table_close = () => `</table></div>`
  md.renderer.rules.ordered_list_open = (toks, i) => {
    const start = toks[i].attrGet("start")
    return start ? `<ol start="${esc(start)}" style="counter-reset:n ${Number(start) - 1}">` : "<ol>"
  }
  md.core.ruler.push("hammock_tasks", (state) => {
    const toks = state.tokens
    for (let i = 2; i < toks.length; i++) {
      const inline = toks[i]
      if (inline.type !== "inline" || toks[i - 1].type !== "paragraph_open" || toks[i - 2].type !== "list_item_open") continue
      const first = inline.children?.[0]
      const m = first && first.type === "text" && /^\[([ xX~])\][ \t]/.exec(first.content)
      if (!m) continue
      first.content = first.content.slice(m[0].length)
      const on = m[1] === "x" || m[1] === "X"
      const box = new state.Token("html_inline", "", 0)
      box.content = `<span class="box${on ? " on" : ""}">${on ? "☑︎" : "☐"}</span>`
      inline.children.unshift(box)
      toks[i - 2].attrJoin("class", "task")
    }
  })
  md.core.ruler.push("hammock_paths", (state) => {
    for (const t of state.tokens) {
      if (t.type !== "inline" || !t.children) continue
      const out = []
      let inLink = 0
      for (const c of t.children) {
        if (c.type === "link_open") inLink++
        if (c.type === "link_close") inLink--
        barePath.lastIndex = 0
        if (c.type !== "text" || inLink > 0 || !barePath.test(c.content)) { out.push(c); continue }
        barePath.lastIndex = 0
        let last = 0, m
        const text = (s) => { const x = new state.Token("text", "", 0); x.content = s; return x }
        while ((m = barePath.exec(c.content))) {
          if (m.index > last) out.push(text(c.content.slice(last, m.index)))
          const open = new state.Token("link_open", "a", 1)
          open.attrs = [["href", "file://" + encodeURI(m[1])]]
          out.push(open, text(m[1]), new state.Token("link_close", "a", -1))
          last = m.index + m[0].length
        }
        if (last < c.content.length) out.push(text(c.content.slice(last)))
      }
      t.children = out
    }
  })
  return md
}
const md = createMd()

/** A message's Markdown: prose blocks, code blocks and tables. */
function markdown(text) {
  // Prose that runs together (headings, paragraphs, lists, quotes) goes in
  // one block, at the native view's spacing; code and tables stand apart.
  const html = md.render(text || "")
  const tmp = document.createElement("div")
  tmp.innerHTML = html
  let out = "", prose = ""
  for (const el of Array.from(tmp.children)) {
    if (el.classList.contains("code") || el.classList.contains("tbl") || el.tagName === "HR") {
      if (prose) { out += `<div class="prose sel">${prose}</div>`; prose = "" }
      out += el.outerHTML
    } else {
      prose += el.outerHTML
    }
  }
  if (prose) out += `<div class="prose sel">${prose}</div>`
  return `<div class="md">${out}</div>`
}

/** One line of Markdown, without links (a row you tap as a whole). */
function inlineText(s) {
  return md.renderInline(s || "").replace(/<a [^>]*>|<\/a>/g, "")
}

/** One line or more, with links (your messages). */
function inlineLinked(s) {
  return md.renderInline(s || "")
}

// ---- code blocks, highlighted as CodeHighlight does ----

const KEYWORDS = new Set(("func let var if else for while return import package type struct class enum case switch const def fn pub use go defer " +
  "interface public private static final async await try catch throw throws guard in of new this self nil null true false none None True False " +
  "export from default break continue extension protocol range map select chan echo then fi do done").split(" "))
const TOKEN = /(\/\/[^\n]*|#[^\n]*|--[^\n]*)|("(?:[^"\\\n]|\\.)*"|'(?:[^'\\\n]|\\.)*'|`[^`]*`)|\b(\d[\d_.xXa-fA-F]*)\b|\b([A-Za-z_][A-Za-z0-9_]*)\b/g
const PLAIN = new Set(["", "text", "txt", "plain", "output", "log"])
const HASH = new Set(["sh", "bash", "zsh", "shell", "python", "py", "ruby", "rb", "yaml", "yml", "toml", "make", "makefile", "just", "conf", "dockerfile"])
const DASH = new Set(["sql", "lua", "haskell", "hs"])

function highlight(code, lang) {
  const l = (lang || "").toLowerCase()
  if (PLAIN.has(l) || code.length >= 40000) return esc(code)
  let out = "", last = 0, m
  TOKEN.lastIndex = 0
  while ((m = TOKEN.exec(code))) {
    let c = null
    if (m[1] !== undefined) {
      if (m[1][0] === "#" && !HASH.has(l)) c = null
      else if (m[1].startsWith("--") && !DASH.has(l)) c = null
      else c = "hl-c"
    } else if (m[2] !== undefined) c = "hl-s"
    else if (m[3] !== undefined) c = "hl-n"
    else if (KEYWORDS.has(m[4])) c = "hl-k"
    if (!c) continue
    out += esc(code.slice(last, m.index)) + `<span class="${c}">${esc(m[0])}</span>`
    last = m.index + m[0].length
  }
  return out + esc(code.slice(last))
}

const PROSE_LANGS = new Set(["markdown", "md", "text", "txt", "plain", "plaintext"])
// What you've switched on a code block (wrapped or not), by what it holds,
// so it lasts as the row is drawn again.
const wrapChoice = new Map()

function codeBlock(code, lang, { max = 420, wraps = false } = {}) {
  const l = (lang || "").toLowerCase()
  const key = l + "\0" + code
  let wrap = wrapChoice.get(key)
  if (wrap === undefined) wrap = cfg.codeWrap === "always" ? true : cfg.codeWrap === "never" ? false : wraps || PROSE_LANGS.has(l)
  return `<div class="code${wrap ? " wrap" : ""}" style="--max:${max}px" data-key="${esc(key)}">` +
    `<div class="code-head"><span class="lang">${esc(lang || "code")}</span>` +
    `<button data-act="wrap" class="${wrap ? "" : "off"}">${sym("arrow.turn.down.left")}Wrap</button>` +
    `<button data-act="copy">${sym("doc.on.doc")}<span>Copy</span></button></div>` +
    `<pre class="sel"><code>${highlight(code, lang)}</code></pre></div>`
}

// ---- rows ----

const opened = new Set()

function toolIcon(b) {
  switch (b.tool) {
    case "Bash": return "terminal"
    case "Read": return "doc.text"
    case "Write": return "doc.badge.plus"
    case "Edit": case "MultiEdit": case "NotebookEdit": return "pencil"
    case "Grep": case "Glob": case "ToolSearch": return "magnifyingglass"
    case "WebFetch": case "WebSearch": return "globe"
    case "TodoWrite": return "checklist"
    case "Skill": return "wand.and.stars"
    default: return b.tool?.startsWith("mcp__") ? "puzzlepiece.extension" : "wrench.and.screwdriver"
  }
}

function runningTime(sinceMs) {
  return sinceMs > 0 ? `<span class="time" data-since="${sinceMs}">${elapsed(sinceMs)}</span>` : ""
}
function elapsed(sinceMs) {
  const s = Math.max(0, Math.floor((Date.now() - sinceMs) / 1000))
  if (s < 60) return s + "s"
  if (s < 3600) return `${Math.floor(s / 60)}m ${String(s % 60).padStart(2, "0")}s`
  return `${Math.floor(s / 3600)}h ${String(Math.floor((s % 3600) / 60)).padStart(2, "0")}m`
}

function status(b) {
  if (b.pending) return `<span class="st">${runningTime(b.time)}<span class="spin"></span></span>`
  if (b.error) return `<span class="st bad">${sym("xmark.circle.fill")}</span>`
  return `<span class="st ok">${sym("checkmark.circle.fill")}</span>`
}

const chev = (open, shown = true) => `<span class="chev${shown ? "" : " none"}">${sym(open ? "chevron.down" : "chevron.right")}</span>`

function pics(refs, max) {
  return refs.map((r) => `<img class="pic" data-ref="${esc(r)}" style="--max:${max}px" alt="">`).join("")
}

function diffView(d, preview) {
  const all = d.lines || []
  const shown = all.slice(0, preview || 600)
  const more = preview ? all.length - shown.length : 0
  const top = all.reduce((m, l) => Math.max(m, l[1] || 0, l[2] || 0), 0)
  const digits = top ? String(top).length : 0
  const pad = (n) => { const s = n ? String(n) : ""; return " ".repeat(Math.max(0, digits - s.length)) + s }
  let lines = ""
  for (const [k, o, n, t] of shown) {
    const gut = digits ? `<span class="gut">${pad(o)} ${pad(n)}  </span>` : ""
    lines += `<div class="ln ${k}">${gut}${esc(t || " ")}</div>`
  }
  const head = `<div class="dhead"><span class="doc">${sym("doc.text")}</span><span class="dname">${esc(d.name)}</span>` +
    (d.folder ? `<span class="dfolder">&lrm;${esc(d.folder)}&lrm;</span>` : "") + `<span class="spacer"></span>` +
    `<button data-act="copydiff" title="Copy the diff">${sym("doc.on.doc")}</button>` +
    (cfg.canOpenDiff ? `<button data-act="opendiff" data-path="${esc(d.path)}" title="Open in Diff Pane">${sym("rectangle.split.2x1")}</button>` : "") + `</div>`
  return `<div class="diff${preview ? " peek" : ""}${more > 0 ? " more" : ""}">${head}<div class="lines${preview ? "" : " sel"}">${lines}</div>` +
    (more > 0 ? `<div class="dmore">${more === 1 ? "1 more line" : more + " more lines"}</div>` : "") + `</div>`
}

function toolRow(b, compact = false) {
  const toggled = opened.has(b.id)
  const open = b.bang ? !toggled : toggled
  const showsInput = !!b.input && (b.tool === "Bash" || !b.tool.startsWith("Read")) && !(b.bang && !b.input.includes("\n"))
  const opens = !!b.diff || showsInput || !!b.output
  let h = `<button class="head" data-toggle="${esc(b.id)}"${opens ? "" : " disabled"}>`
  if (b.bang) h += `<span class="bang">!</span><span class="cmd">${esc(firstLine(b.input))}</span>`
  else h += `<span class="icon">${sym(toolIcon(b))}</span><span class="title">${inlineText(b.title || b.tool)}</span>`
  if (b.added > 0 || b.removed > 0) h += `<span class="add">+${b.added}</span><span class="del">−${b.removed}</span>`
  h += `<span class="spacer"></span>${status(b)}${chev(open, opens)}</button>`
  let body = ""
  if (b.images?.length) body += pics(b.images, 300)
  if (!open && b.diff) body += diffView(b.diff, compact ? 4 : 8).replace('class="diff peek', `data-open="${esc(b.id)}" class="diff peek`)
  if (open && opens) {
    if (b.diff) body += diffView(b.diff, 0)
    else if (showsInput) body += codeBlock(b.input, b.tool === "Bash" ? "bash" : "", { max: 200, wraps: true })
    if (b.output) body += codeBlock(b.output, "output", { max: 260, wraps: true })
  }
  return `<div class="tool${compact ? " compact" : ""}${open ? " open" : ""}">${h}${body ? `<div class="body">${body}</div>` : ""}</div>`
}

function groupRow(r) {
  const open = opened.has(r.id)
  const running = [...r.tools].reverse().find((t) => t.pending)
  const last = r.tools[r.tools.length - 1]
  let h = `<button class="head" data-toggle="${esc(r.id)}"><span class="icon">${sym("square.stack.3d.up")}</span>` +
    `<span class="title">${esc(r.summary)}</span>` +
    (r.failed ? `<span class="fail">· ${esc(r.failed)}</span>` : "") +
    (last?.pending ? `<span class="last">· ${inlineText(last.title)}</span>` : "") +
    `<span class="spacer"></span>` +
    (running ? `<span class="st">${runningTime(running.time)}<span class="spin"></span></span>` : "") + chev(open) + `</button>`
  let body = ""
  if (open) body = `<div class="body">${r.tools.map((t) => toolRow(t, true)).join("")}</div>`
  else {
    const imgs = r.tools.flatMap((t) => t.images || [])
    if (imgs.length) body = `<div class="body pics">${pics(imgs, 300)}</div>`
  }
  return `<div class="tool group${open ? " open" : ""}">${h}${body}</div>`
}

function userRow(r, pending = false) {
  const text = (r.text || "").replace(/￼/g, "")
  return `<div class="user${pending ? " pending" : ""}">${pics(r.images || [], 200)}` +
    (text.trim() ? `<div class="bubble${pending ? "" : " sel"}">${inlineLinked(text)}</div>` : "") + `</div>`
}

function thinkingRow(r) {
  if (r.text.trim()) return markdown(r.text.trim())
  const open = opened.has(r.id)
  const words = r.seconds != null ? (r.seconds > 1 ? `Thought for ${r.seconds}s` : "Thought briefly") : "Thinking"
  return `<div class="thought${open ? " open" : ""}"><button class="head" data-toggle="${esc(r.id)}"${r.text ? "" : " disabled"}>` +
    `<span class="ic">${sym("brain")}</span>${words}${r.text ? chev(open) : ""}</button>` +
    (open ? `<div class="body sel">${esc(r.text)}</div>` : "") + `</div>`
}

function noteRow(b) {
  const open = opened.has(b.id)
  return `<div class="note${open ? " open" : ""}"><button class="head" data-toggle="${esc(b.id)}"${b.output ? "" : " disabled"}>` +
    `<span>${esc(b.text)}</span>${b.output ? chev(open) : ""}</button>` + (open ? `<div class="body">${markdown(b.output)}</div>` : "") + `</div>`
}

function questionCard(b) {
  let h = `<div class="card question"><div class="label">${sym("questionmark.bubble")}${esc(cfg.agentName)} asked</div>`
  for (const q of b.questions || []) {
    h += `<div class="q"><div class="ask sel">${esc(q.question)}</div>`
    if (q.answer) {
      h += `<div class="answer">${sym("arrowshape.turn.up.left.fill")}<span class="sel">${esc(q.answer)}</span></div>`
      if (q.notes) h += `<div class="label">${sym("text.bubble")}${esc(q.notes)}</div>`
    } else if (b.pending) h += `<div class="label">Waiting for your answer</div>`
    h += `</div>`
  }
  if (b.output && b.error) h += `<div class="label">${esc(b.output)}</div>`
  return h + `</div>`
}

function planCard(b) {
  const st = b.pending ? `<span class="label">Waiting for approval</span>`
    : b.error ? `<span class="label">${sym("arrow.uturn.backward")}Not approved</span>`
    : `<span class="label ok">${sym("checkmark")}Approved</span>`
  return `<div class="card plan"><div class="plan-head"><span class="label">${sym("list.bullet.clipboard")}Plan</span>${st}</div>${markdown(b.text)}</div>`
}

function subagentCard(b) {
  const open = opened.has(b.id)
  let h = `<div class="card subagent${open ? " open" : ""}"><button class="sub-head" data-toggle="${esc(b.id)}">` +
    `<span class="icon">${sym("person.2.wave.2")}</span><span class="who"><span>${esc(b.title || "Subagent")}</span>` +
    (b.text ? `<span class="label">${esc(b.text)}</span>` : "") + `</span>` +
    `${status(b)}${chev(open)}</button>`
  if (open) {
    if (b.input) h += `<div class="label">Asked to</div>${markdown(b.input)}`
    if (b.output) h += `<hr><div class="label">Reported</div>${markdown(b.output)}`
  }
  return h + `</div>`
}

function filesRow(b) {
  const others = (b.input || "").split("\n").filter((n) => n && !(b.images || []).some((r) => r.endsWith("/" + n)))
  return `<div class="files"><div class="label">${sym("paperclip")}${esc(b.title)}</div>` +
    (b.text ? markdown(b.text) : "") + pics(b.images || [], 360) +
    (others.length ? `<div class="others">${esc(others.join(", "))}</div>` : "") + `</div>`
}

function rowHTML(r) {
  switch (r.kind) {
    case "user": return userRow(r)
    case "text": return markdown(r.text)
    case "thinking": return thinkingRow(r)
    case "tool": return toolRow(r.b)
    case "group": return groupRow(r)
    case "files": return filesRow(r.b)
    case "question": return questionCard(r.b)
    case "plan": return planCard(r.b)
    case "subagent": return subagentCard(r.b)
    case "note": return noteRow(r.b)
    case "done": return `<div class="done">${sym("sparkle")}<span>${esc(r.words)}</span></div>`
    default: return ""
  }
}

function build(r) {
  const el = document.createElement("div")
  el.className = "row"
  el.dataset.id = r.id
  el.innerHTML = rowHTML(r)
  return el
}

// ---- the rows, as the app sends them ----

const rowsEl = $("rows"), tailEl = $("tail"), mainEl = $("main")
let order = []
const data = new Map()
const els = new Map()
let meta = {}

function rebuildAll() {
  for (const id of order) {
    const r = data.get(id)
    const el = els.get(id)
    if (r && el) { const n = build(r); el.replaceWith(n); els.set(id, n) }
  }
  if (lastTail) renderTail(lastTail, true)
  loadImages()
}

function reconcile(next, changed, live) {
  const was = new Set(order)
  const fresh = !order.length || !next.some((id) => was.has(id))
  const prepended = !fresh && next[0] !== order[0] && next.indexOf(order[0]) > 0
  const nextSet = new Set(next)
  for (const id of order) if (!nextSet.has(id)) { els.get(id)?.remove(); els.delete(id); data.delete(id) }
  const added = []
  let prev = null
  for (const id of next) {
    let el = els.get(id)
    if (!el || changed.has(id)) {
      const n = build(data.get(id))
      if (el) el.replaceWith(n)
      el = n
      els.set(id, el)
      if (!was.has(id)) added.push(id)
      if (live.has(id) && !fresh) {
        const len = (data.get(id).text || "").length
        if (len > 160 && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
          el.classList.add("reveal")
          el.style.setProperty("--reveal", Math.min(1.6, Math.max(0.45, len / 1800)) + "s")
          el.addEventListener("animationend", () => el.classList.remove("reveal"), { once: true })
        }
      }
    }
    const want = prev ? prev.nextElementSibling : rowsEl.firstElementChild
    if (want !== el) rowsEl.insertBefore(el, want)
    prev = el
  }
  order = next
  return { fresh, prepended, added }
}

let lastTail = null
const pendingShown = new Set()

function renderTail(t, force = false) {
  const json = JSON.stringify(t)
  if (!force && lastTail && JSON.stringify(lastTail) === json) return
  const wasWorking = !!lastTail?.working
  lastTail = t
  let h = ""
  for (const p of t.pending || []) {
    if (p.kind === "tool") h += `<div class="row" data-pending="${esc(p.id)}">${toolRow(p.b)}</div>`
    else h += `<div class="row" data-pending="${esc(p.id)}">${userRow(p, true)}</div>`
  }
  if (t.working) h += `<div class="row working${wasWorking ? "" : " arriving"}"><span class="sparkle">${sym("sparkle")}</span><span class="status">${esc(t.status || "Working…")}</span></div>`
  tailEl.innerHTML = h
  for (const el of tailEl.querySelectorAll("[data-pending]")) {
    const id = el.dataset.pending
    if (!pendingShown.has(id)) { pendingShown.add(id); el.firstElementChild?.classList.add("arriving") }
  }
}

function applyMeta(m) {
  const before = meta
  meta = { ...meta, ...m }
  const earlier = $("earlier")
  earlier.hidden = !meta.hasMore
  earlier.textContent = meta.loadingOlder ? "Loading…" : "Show earlier messages"
  const empty = $("empty")
  empty.hidden = !meta.error
  $("empty-why").textContent = meta.error || ""
  // Done, or asking something, while you read further up: a dot on Latest.
  if (latestShown && ((before.working && !meta.working) || (!before.prompt && meta.prompt))) { attention = true; drawLatest() }
}

// ---- scrolling ----

// The conversation scrolls in an element of its own, not the page: on the
// iPhone, a page scrolled by script in its first moments (to its end, as
// the conversation opens) could disagree with its UIScrollView about where
// it was, and show blank. An element's scroll view (WebKit makes one, with
// the same momentum and bounce) takes the script's position reliably.
// The Mac scrolls the page itself: it has no such trouble, and WebKit there
// only bounces at the ends of the page, not of an element.
const pageScroll = navigator.maxTouchPoints === 0
document.documentElement.classList.toggle("page-scroll", pageScroll)
const se = pageScroll ? document.scrollingElement : $("scroller")
let insetTop = 0, insetBottom = 0
/** Following the end: rows arriving glide into view. */
let follow = true
/** Just loaded: kept at the end at once while fonts, code and pictures settle. */
let settleUntil = 0
let glider = null
/** The first row in view and where it was, kept in place when what's above it changes. */
let anchor = null
/** A reply that arrived too long to show with the end: the glide stops at its top. */
let replyStop = null
const reduceMotion = () => matchMedia("(prefers-reduced-motion: reduce)").matches

const maxTop = () => Math.max(0, se.scrollHeight - se.clientHeight)
const gap = () => maxTop() - se.scrollTop
const docTop = (el) => el.getBoundingClientRect().top - (pageScroll ? 0 : se.getBoundingClientRect().top) + se.scrollTop

function setTop(y) { se.scrollTop = Math.max(0, Math.min(maxTop(), y)) }

function endTarget() {
  if (replyStop?.isConnected) return Math.min(maxTop(), docTop(replyStop) - insetTop - 24)
  return maxTop()
}

/** To target(), moving, as SwiftUI's .smooth: the target read each frame, so it follows rows still arriving. */
function glide(target, ms = 320, then) {
  if (reduceMotion()) { setTop(target()); then?.(); return }
  const from = se.scrollTop, t0 = performance.now()
  const g = { target }
  glider = g
  const step = (now) => {
    if (glider !== g) return
    const t = Math.min(1, (now - t0) / ms)
    const e = 1 - Math.pow(1 - t, 3)
    setTop(from + (target() - from) * e)
    if (t < 1) requestAnimationFrame(step)
    else { glider = null; recordAnchor(); drawLatest(); then?.() }
  }
  requestAnimationFrame(step)
}

function glideToEnd() {
  if (glider?.toEnd) return // already going there: it reads the end each frame
  glide(endTarget, 320, () => {
    // A reply too long to show with the end: stopped at its first line, no
    // longer following (Latest is for the rest).
    if (replyStop) { replyStop = null; follow = false; drawLatest() }
  })
  if (glider) glider.toEnd = true
}

function cancelGlide() { glider = null }

function recordAnchor() {
  const kids = [...rowsEl.children, ...tailEl.children]
  let lo = 0, hi = kids.length - 1, found = null
  while (lo <= hi) {
    const mid = (lo + hi) >> 1
    const r = kids[mid].getBoundingClientRect()
    if (r.bottom > insetTop + 1) { found = kids[mid]; hi = mid - 1 } else lo = mid + 1
  }
  anchor = found ? { el: found, top: found.getBoundingClientRect().top } : null
}

function restoreAnchor() {
  if (!anchor || !anchor.el.isConnected) return recordAnchor()
  const d = anchor.el.getBoundingClientRect().top - anchor.top
  if (Math.abs(d) > 0.5) setTop(se.scrollTop + d)
  anchor.top = anchor.el.getBoundingClientRect().top
}

/** The content changed size (rows, pictures, fonts, the pane's width). */
function contentResized() {
  if (performance.now() < settleUntil) log("resized")
  if (follow) {
    if (performance.now() < settleUntil) { cancelGlide(); setTop(maxTop()) }
    else if (gap() > 0.5 || glider) glideToEnd()
  } else if (!glider) restoreAnchor()
  drawLatest()
}
new ResizeObserver(contentResized).observe(mainEl)
window.addEventListener("resize", contentResized)
document.fonts?.ready.then(contentResized)

// Your hand on it ends any glide; where you leave it decides following.
// Only your hand: WebKit and UIKit move the page themselves too (putting a
// new page back at its top, clamping as it shrinks), and those don't mean
// you've scrolled away. A scroll counts as yours while you touch, and while
// scrolls keep coming after your last input (momentum, a scroll bar drag).
let touching = false, lastInput = -1e9
const input = () => { lastInput = performance.now(); cancelGlide(); replyStop = null }
for (const ev of ["wheel", "mousedown"]) window.addEventListener(ev, input, { passive: true })
// A drag down the conversation pulls the keyboard away, as the native one's does.
let touchY = null, dismissed = false
window.addEventListener("touchstart", (e) => { touching = true; touchY = e.touches[0]?.clientY ?? null; dismissed = false; input() }, { passive: true })
window.addEventListener("touchmove", (e) => {
  input()
  const y = e.touches[0]?.clientY
  if (!dismissed && touchY != null && y - touchY > 24) { dismissed = true; post({ type: "dismissKeyboard" }) }
}, { passive: true })
for (const ev of ["touchend", "touchcancel"]) window.addEventListener(ev, () => { touching = false; touchY = null; lastInput = performance.now() }, { passive: true })
window.addEventListener("keydown", (e) => { if (["PageUp", "PageDown", "ArrowUp", "ArrowDown", "Home", "End", " "].includes(e.key)) input() })
let scrollQueued = false
const scrolls = pageScroll ? window : se
scrolls.addEventListener("scroll", () => {
  if (glider || scrollQueued) return
  scrollQueued = true
  requestAnimationFrame(() => {
    scrollQueued = false
    if (glider) return
    const now = performance.now()
    if (touching || now - lastInput < 250) {
      lastInput = now
      follow = gap() < 60
    } else if (follow && gap() > 0.5) {
      // Moved, not by you, while following: back to the end, once whatever
      // moved it is done.
      log("moved")
      setTimeout(() => { if (follow && !glider) setTop(maxTop()) }, 0)
    }
    recordAnchor()
    drawLatest()
  })
}, { passive: true })

function setInsets(i) {
  const top = Math.round(i.top || 0), bottom = Math.round(i.bottom || 0)
  if (top === insetTop && bottom === insetBottom) return
  const wasFollowing = follow && gap() < 60
  insetTop = top; insetBottom = bottom
  log("insets")
  document.documentElement.style.setProperty("--top", top + "px")
  document.documentElement.style.setProperty("--bottom", bottom + "px")
  if (wasFollowing) {
    // The keyboard or the message box: moving with it, not jumping.
    if (performance.now() < settleUntil) setTop(maxTop())
    else { cancelGlide(); glide(maxTop, 260) }
  } else restoreAnchor()
}

// ---- Latest ----

let latestShown = false, seenLast = null, attention = false

function drawLatest() {
  const g = gap()
  if (!latestShown && g > 350 && performance.now() > settleUntil) {
    latestShown = true
    seenLast = order[order.length - 1]
    attention = false
  } else if (latestShown && g < 150) {
    latestShown = false
  }
  const b = $("latest")
  b.hidden = !latestShown
  if (!latestShown) return
  const i = order.indexOf(seenLast)
  const n = i < 0 ? 0 : order.length - 1 - i
  b.innerHTML = `${sym("arrow.down")}<span>${n > 0 ? n + " new" : "Latest"}</span>${attention ? '<span class="dot"></span>' : ""}`
}

function toLatest() {
  follow = true
  replyStop = null
  cancelGlide()
  // Far up, most of the way at once: a glide through the whole conversation is a blur.
  const far = gap() - se.clientHeight * 1.5
  if (far > 0) setTop(se.scrollTop + far)
  glideToEnd()
  latestShown = false
  drawLatest()
}

// ---- the update from the app ----

function update(u) {
  if (u.replace) {
    rowsEl.innerHTML = ""; tailEl.innerHTML = ""
    order = []; data.clear(); els.clear(); lastTail = null
    follow = true; replyStop = null; cancelGlide()
  }
  if (u.meta) applyMeta(u.meta)
  if (!follow) recordAnchor()
  const wasFollowing = follow
  let result = null
  if (u.order) {
    const changed = new Set()
    const live = new Set()
    for (const r of u.rows || []) { data.set(r.id, r); changed.add(r.id); if (r.live) live.add(r.id) }
    const next = u.order.filter((id) => data.has(id))
    // Earlier messages loaded where you asked for them: what you were
    // reading stays in view, with them above it.
    if (order.length && next[0] !== order[0] && next.indexOf(order[0]) > 0) follow = false
    result = reconcile(next, changed, live)
  }
  if (u.tail) renderTail(u.tail)
  loadImages()
  if (result?.fresh) {
    setTop(maxTop())
    follow = true
    settleUntil = performance.now() + 1200
    recordAnchor()
    log("loaded")
  } else if (result && wasFollowing && follow) {
    // A reply too long to show with the end: glide to its top, no further.
    for (const id of result.added) {
      const r = data.get(id), el = els.get(id)
      if (r?.kind === "text" && r.live && el.offsetHeight > se.clientHeight - insetTop - insetBottom - 40) replyStop = el
    }
  }
  // Sent while scrolled up: down to see it go.
  if (u.sent) toLatest()
  drawLatest()
}

// ---- pictures ----

const images = new Map() // ref → data URL
const asked = new Set()

function loadImages() {
  for (const img of document.querySelectorAll("img.pic:not([src])")) {
    const ref = img.dataset.ref
    if (images.has(ref)) setImage(img, images.get(ref))
    else if (!asked.has(ref)) { asked.add(ref); post({ type: "image", ref }) }
  }
}

function setImage(img, url) {
  img.addEventListener("load", () => img.classList.add("loaded"), { once: true })
  img.src = url
}

function image(ref, url) {
  images.set(ref, url)
  for (const img of document.querySelectorAll("img.pic:not([src])")) if (img.dataset.ref === ref) setImage(img, url)
}

// ---- what you do ----

function rowOf(el) { return el.closest(".row") }

function redraw(rowEl) {
  if (rowEl.dataset.id) {
    const r = data.get(rowEl.dataset.id)
    const n = build(r)
    rowEl.replaceWith(n)
    els.set(r.id, n)
    loadImages()
    return n
  }
  renderTail(lastTail, true)
  return null
}

/** A row you opened: brought into view (all of it if it fits, else its top), or kept at the end if it shows from there. */
function reveal(el, wasFollowing) {
  const r = el.getBoundingClientRect()
  const viewTop = insetTop, viewBottom = se.clientHeight - insetBottom
  if (wasFollowing && docTop(el) - insetTop >= maxTop() - 1) { follow = true; glideToEnd(); return }
  let y = null
  if (r.height > viewBottom - viewTop || r.top < viewTop) y = se.scrollTop + r.top - viewTop - 8
  else if (r.bottom > viewBottom) y = se.scrollTop + r.bottom - viewBottom + 8
  if (y != null) glide(() => Math.min(maxTop(), y), 300)
}

document.addEventListener("click", (e) => {
  const a = e.target.closest("a[href]")
  if (a) { e.preventDefault(); post({ type: "link", href: a.getAttribute("href") }); return }
  const img = e.target.closest("img.pic")
  if (img) { post({ type: "openImage", ref: img.dataset.ref }); return }
  const act = e.target.closest("[data-act]")
  if (act) {
    const code = act.closest(".code")
    switch (act.dataset.act) {
      case "copy": {
        post({ type: "copy", text: code.querySelector("pre").textContent })
        const label = act.querySelector("span")
        act.querySelector(".sf").outerHTML = sym("checkmark")
        label.textContent = "Copied"
        setTimeout(() => { act.querySelector(".sf").outerHTML = sym("doc.on.doc"); label.textContent = "Copy" }, 1200)
        return
      }
      case "wrap": {
        const on = !code.classList.contains("wrap")
        wrapChoice.set(code.dataset.key, on)
        code.classList.toggle("wrap", on)
        act.classList.toggle("off", !on)
        return
      }
      case "copydiff": {
        const row = rowOf(act), id = row?.dataset.id
        const d = findDiff(data.get(id), act) || findDiff(pendingRow(row), act)
        if (d) post({ type: "copy", text: d.raw })
        const old = act.innerHTML
        act.innerHTML = sym("checkmark")
        setTimeout(() => { act.innerHTML = old }, 1500)
        return
      }
      case "opendiff": post({ type: "openDiff", path: act.dataset.path }); return
    }
  }
  const peek = e.target.closest("[data-open]")
  const toggle = e.target.closest("[data-toggle]")
  const id = peek?.dataset.open || toggle?.dataset.toggle
  if (!id || toggle?.disabled) return
  const row = rowOf(peek || toggle)
  if (peek) opened.add(id)
  else if (opened.has(id)) opened.delete(id)
  else opened.add(id)
  const wasFollowing = follow && gap() < 60
  const opening = peek || opened.has(id) !== !!data.get(id)?.b?.bang
  follow = false
  recordAnchor()
  const n = redraw(row) || row
  if (opening && n?.isConnected) reveal(n, wasFollowing)
  else follow = wasFollowing
})

function pendingRow(row) {
  const id = row?.dataset.pending
  const p = lastTail?.pending?.find((x) => x.id === id)
  return p?.kind === "tool" ? { b: p.b } : null
}

function findDiff(r, act) {
  if (!r) return null
  const blocks = r.b ? [r.b] : r.tools || []
  const path = act.closest(".diff")?.querySelector("[data-act=opendiff]")?.dataset.path
  return (blocks.find((b) => b.diff && (!path || b.diff.path === path)) || blocks.find((b) => b.diff))?.diff
}

$("earlier").addEventListener("click", () => { if (!meta.loadingOlder) post({ type: "loadOlder" }) })
$("latest").addEventListener("click", toLatest)

// Pending messages' menu: Send Now, while the agent holds them.
document.addEventListener("contextmenu", (e) => {
  if (e.target.closest(".user.pending")) { e.preventDefault(); post({ type: "pendingMenu", x: e.clientX, y: e.clientY }) }
})

setInterval(() => {
  for (const t of document.querySelectorAll("[data-since]")) t.textContent = elapsed(Number(t.dataset.since))
}, 1000)

/** The demo's takes (DemoDirector: scroll), as a finger would: top, bottom, latest, or by n points. */
function debugScroll(to) {
  if (to === "latest") return toLatest()
  cancelGlide()
  if (to === "bottom") { follow = true; return glideToEnd() }
  follow = false
  const y = to === "top" ? 0 : se.scrollTop + Number(to || 0)
  glide(() => Math.max(0, Math.min(maxTop(), y)), 600)
}

window.hammock = { configure, update, setInsets, image, toLatest, debugScroll }
// Ready once the empty page is on screen: on the iPhone, WebKit puts a new
// page back at its top as its first frame reaches the screen, and a
// conversation already scrolled to its end by then was moved with it.
requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(() => post({ type: "ready" }), 50)))
