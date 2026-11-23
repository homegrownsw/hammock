// Obsidian's flavour of Markdown, for the preview (markdown.js): [[links]],
// ![[embeds]] (images, files, other notes' text), callouts, ==highlights==,
// %%comments%%, #tags and ^block ids. All of it is additive: none of it
// means anything in CommonMark, so other Markdown renders as before.
//
// Links name files, not paths: the page can't look, so env.wiki.lookup asks
// the app (which asks the daemon, editorlinks.go) and answers what it knows
// now: undefined while it's being asked (the preview renders again when the
// answer comes), null for nothing found, else { path, text, hasText }.
//
// No DOM here: tests run it in Node (apple/Editor/test).

/** Embeds go this deep (a note in a note in a note), and no more per render. */
export const EMBED_DEPTH = 3
export const EMBED_MAX = 30
const EMBED_TEXT_MAX = 200_000

export function escapeHTML(s) {
  return String(s).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]))
}

/** GitHub's heading ids: headings' anchors, and how [[Note#Heading]] finds one. */
export function slug(text) {
  return text.trim().toLowerCase().replace(/[^\p{L}\p{N}\s_-]/gu, "").replace(/\s/g, "-")
}

/** A path as a link or image's href (the preview makes it hammock-file:// or opens it). */
export function encodePath(p) {
  return encodeURI(p).replace(/#/g, "%23").replace(/\?/g, "%3F")
}

const imageExts = new Set(["png", "jpg", "jpeg", "gif", "webp", "svg", "bmp", "avif", "heic", "ico"])
const noteExts = new Set(["md", "markdown", "mdown", "mkd"])

function extOf(name) {
  const base = name.slice(name.lastIndexOf("/") + 1)
  const m = /\.([A-Za-z0-9]{1,5})$/.exec(base)
  return m ? m[1].toLowerCase() : ""
}

/** Whether a link's name is a note (no extension, or Markdown's). */
export function isNote(name) {
  const e = extOf(name)
  return !e || noteExts.has(e)
}

/** [[name#sub|alias]] → { name, sub, alias }. A table's cells write | as \|. */
export function parseWiki(inner) {
  const s = inner.replace(/\\\|/g, "|")
  const bar = s.indexOf("|")
  const target = (bar < 0 ? s : s.slice(0, bar)).trim()
  const alias = bar < 0 ? "" : s.slice(bar + 1).trim()
  const hash = target.indexOf("#")
  const name = (hash < 0 ? target : target.slice(0, hash)).trim()
  const sub = hash < 0 ? "" : target.slice(hash + 1).split("#").map((p) => p.trim()).filter(Boolean).join("#")
  return { name, sub, alias }
}

/** What a link shows: its alias, else what it names ("Note > Heading"). */
function shown(m) {
  if (m.alias) return m.alias
  const sub = m.sub.replace(/#/g, " > ").replace(/^\^/, "^")
  return m.name ? (sub ? `${m.name} > ${sub}` : m.name) : sub
}

// ---- %%comments%% ----

/** The text without its %%comments%% (kept in code), lines kept so lines still number the same. */
export function stripComments(text) {
  if (!text.includes("%%")) return text
  const fenceRe = /^ {0,3}(`{3,}|~{3,})/
  let out = "", fence = ""
  const lines = text.split("\n")
  let inComment = false
  for (let li = 0; li < lines.length; li++) {
    const line = lines[li]
    if (li) out += "\n"
    if (!inComment) {
      const f = fenceRe.exec(line)
      if (fence) {
        if (f && f[1][0] === fence[0] && f[1].length >= fence.length && !line.slice(f[0].length).trim()) fence = ""
        out += line
        continue
      }
      if (f && !line.includes("%%")) {
        fence = f[1]
        out += line
        continue
      }
    }
    let i = 0
    while (i < line.length) {
      if (inComment) {
        const end = line.indexOf("%%", i)
        if (end < 0) { i = line.length; break }
        inComment = false
        i = end + 2
        continue
      }
      const c = line[i]
      if (c === "`") {
        // Code: copied as it is, to its matching run of backticks.
        let n = 1
        while (line[i + n] === "`") n++
        const run = "`".repeat(n)
        let j = line.indexOf(run, i + n)
        while (j >= 0 && line[j + n] === "`") j = line.indexOf(run, j + n + 1)
        if (j < 0) { out += line.slice(i, i + n); i += n; continue }
        out += line.slice(i, j + n)
        i = j + n
        continue
      }
      if (c === "%" && line[i + 1] === "%") {
        inComment = true
        i += 2
        continue
      }
      out += c
      i++
    }
  }
  return out
}

// ---- a note's section, for ![[Note#Heading]] and ![[Note#^block]] ----

function dedent(lines) {
  const ind = Math.min(...lines.filter((l) => l.trim()).map((l) => /^[ \t]*/.exec(l)[0].length))
  return lines.map((l) => l.slice(Math.min(ind, /^[ \t]*/.exec(l)[0].length))).join("\n")
}

/** The part of a note's text sub names (a heading's section, or a block), null if it has none. */
export function noteSection(md, text, sub) {
  if (!sub) return text
  const lines = text.split("\n")
  const tokens = md.parse(text, { wiki: { embedded: true, parsing: true } })
  if (sub.startsWith("^")) {
    const id = sub.slice(1)
    const re = new RegExp(`(?:^|\\s)\\^${id.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`)
    const at = lines.findIndex((l) => re.test(l))
    if (at < 0) return null
    let best = null
    if (lines[at].trim() === "^" + id) {
      // On a line of its own: the block before it.
      for (const t of tokens) if (t.map && t.level === 0 && t.nesting >= 0 && t.map[1] <= at && (!best || t.map[0] >= best[0])) best = t.map
    } else {
      // The innermost block on that line: a list item rather than its paragraph.
      let bestItem = false
      for (const t of tokens) {
        if (!t.map || t.nesting < 0 || t.map[0] > at || t.map[1] <= at) continue
        if (!["list_item_open", "paragraph_open", "heading_open", "blockquote_open", "table_open"].includes(t.type)) continue
        const span = t.map[1] - t.map[0], item = t.type === "list_item_open"
        if (!best || span < best[1] - best[0] || (span === best[1] - best[0] && item && !bestItem)) { best = t.map; bestItem = item }
      }
    }
    if (!best) best = [at, at + 1]
    return dedent(lines.slice(best[0], best[1]))
  }
  const heads = []
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i]
    if (t.type === "heading_open" && t.map) heads.push({ level: +t.tag.slice(1), line: t.map[0], text: tokens[i + 1]?.content || "" })
  }
  let from = 0, end = heads.length, found = null
  for (const part of sub.split("#")) {
    const want = slug(part)
    let k = -1
    for (let i = from; i < end; i++) if (slug(heads[i].text) === want) { k = i; break }
    if (k < 0) return null
    found = heads[k]
    from = k + 1
    end = heads.length
    for (let i = k + 1; i < heads.length; i++) if (heads[i].level <= found.level) { end = i; break }
  }
  const stop = end < heads.length ? heads[end].line : lines.length
  return lines.slice(found.line, stop).join("\n")
}

// ---- callouts: > [!note] Title ----

const calloutKinds = {
  note: "note", abstract: "abstract", summary: "abstract", tldr: "abstract", info: "info", todo: "todo",
  tip: "tip", hint: "tip", important: "tip", success: "success", check: "success", done: "success",
  question: "question", help: "question", faq: "question", warning: "warning", caution: "warning", attention: "warning",
  failure: "failure", fail: "failure", missing: "failure", danger: "danger", error: "danger", bug: "bug",
  example: "example", quote: "quote", cite: "quote",
}
export const CALLOUT_KINDS = [...new Set(Object.values(calloutKinds))]

function callouts(state) {
  const toks = state.tokens
  for (let i = 0; i < toks.length; i++) {
    const open = toks[i]
    if (open.type !== "blockquote_open") continue
    const p = toks[i + 1], inl = toks[i + 2]
    if (!p || p.type !== "paragraph_open" || !inl || inl.type !== "inline") continue
    const nl = inl.content.indexOf("\n")
    const first = nl < 0 ? inl.content : inl.content.slice(0, nl)
    const m = /^\[!([^\]\s]+)\]([+-]?)(?:[ \t]+(.*))?$/.exec(first.trimEnd())
    if (!m) continue
    const kind = calloutKinds[m[1].toLowerCase()] || "note"
    const fold = m[2]
    const title = (m[3] || "").trim() || m[1][0].toUpperCase() + m[1].slice(1).toLowerCase()
    const tag = fold ? "details" : "div"
    open.tag = tag
    open.attrJoin("class", "callout")
    open.attrSet("data-callout", kind)
    if (fold === "+") open.attrSet("open", "")
    const T = state.Token
    const tOpen = new T("callout_title_open", fold ? "summary" : "div", 1)
    tOpen.attrSet("class", "callout-title")
    const icon = new T("callout_icon", "", 0)
    // The title is a flex row (icon, text): its text goes in one span, as Obsidian's does, or each
    // run of it (a link, bold, code) becomes a column of its own.
    const iOpen = new T("callout_title_inner_open", "span", 1)
    iOpen.attrSet("class", "callout-title-inner")
    const iClose = new T("callout_title_inner_close", "span", -1)
    const tInline = new T("inline", "", 0)
    tInline.content = title
    tInline.children = []
    const tClose = new T("callout_title_close", fold ? "summary" : "div", -1)
    const cOpen = new T("callout_content_open", "div", 1)
    cOpen.attrSet("class", "callout-content")
    const cClose = new T("callout_content_close", "div", -1)
    for (const t of [tOpen, icon, iOpen, tInline, iClose, tClose, cOpen, cClose]) { t.level = open.level + 1; t.block = true }
    for (const t of [iOpen, tInline, iClose]) t.block = false
    const rest = nl < 0 ? "" : inl.content.slice(nl + 1)
    if (rest.trim()) {
      inl.content = rest
      if (p.map) p.map = [p.map[0] + 1, p.map[1]]
      toks.splice(i + 1, 0, tOpen, icon, iOpen, tInline, iClose, tClose, cOpen)
    } else {
      toks.splice(i + 1, 3, tOpen, icon, iOpen, tInline, iClose, tClose, cOpen)
    }
    for (let j = i + 1; j < toks.length; j++) {
      if (toks[j].type === "blockquote_close" && toks[j].level === open.level) {
        toks[j].tag = tag
        toks.splice(j, 0, cClose)
        break
      }
    }
  }
}

// ---- ^block ids ----

const blockIdRe = /(?:^|[ \t])\^([A-Za-z0-9-]+)[ \t]*$/

function blockIds(state) {
  const toks = state.tokens
  const anchors = !state.env.wiki?.embedded
  for (let i = 1; i < toks.length; i++) {
    const inl = toks[i]
    if (inl.type !== "inline" || toks[i - 1].type !== "paragraph_open") continue
    const m = blockIdRe.exec(inl.content)
    if (!m) continue
    if (inl.content.trim() === "^" + m[1]) {
      // A line of its own after a list, table or quote: names that.
      let j = i - 2
      if (j < 0 || toks[j].nesting === 1) continue
      if (toks[j].nesting === -1) {
        const level = toks[j].level
        while (j > 0 && !(toks[j].nesting === 1 && toks[j].level === level)) j--
      }
      if (anchors) toks[j].attrSet("data-anchor", "^" + m[1])
      toks.splice(i - 1, 3)
      i -= 2
      continue
    }
    inl.content = inl.content.slice(0, m.index).trimEnd()
    const block = toks[i - 2]?.type === "list_item_open" ? toks[i - 2] : toks[i - 1]
    if (anchors) block.attrSet("data-anchor", "^" + m[1])
  }
}

// ---- ![[Note]] on a line of its own: the note, embedded ----

const embedLine = /^!\[\[([^[\]\n]+)\]\]$/

function embedBlocks(state) {
  const toks = state.tokens
  for (let i = 0; i + 2 < toks.length; i++) {
    const p = toks[i], inl = toks[i + 1]
    if (p.type !== "paragraph_open" || inl.type !== "inline" || toks[i + 2].type !== "paragraph_close") continue
    if (!inl.content.includes("![[")) continue
    const lines = inl.content.split("\n").map((l) => l.trim()).filter(Boolean)
    const metas = lines.map((l) => { const m = embedLine.exec(l); return m && parseWiki(m[1]) })
    if (!metas.length || !metas.every((m) => m && m.name && isNote(m.name))) continue
    const blocks = metas.map((meta) => {
      const t = new state.Token("wiki_embed_block", "div", 0)
      t.meta = meta
      t.map = p.map
      t.level = p.level
      t.block = true
      return t
    })
    toks.splice(i, 3, ...blocks)
    i += blocks.length - 1
  }
}

// ---- inline: [[links]], ![[embeds]], ==highlights==, #tags ----

function wikilink(state, silent) {
  const src = state.src
  let pos = state.pos
  const embed = src.charCodeAt(pos) === 0x21 /* ! */
  if (embed) pos++
  if (src.charCodeAt(pos) !== 0x5b || src.charCodeAt(pos + 1) !== 0x5b) return false
  const end = src.indexOf("]]", pos + 2)
  if (end < 0 || end + 2 > state.posMax) return false
  const inner = src.slice(pos + 2, end)
  if (!inner.trim() || /[\n[\]]/.test(inner)) return false
  const meta = parseWiki(inner)
  if (!meta.name && !meta.sub) return false
  if (!silent) {
    const t = state.push(embed ? "wiki_embed" : "wiki_link", "", 0)
    t.meta = meta
    t.content = inner
  }
  state.pos = end + 2
  return true
}

const tagRe = /^#([\p{L}\p{N}_/-]+)/u

function tag(state, silent) {
  const src = state.src, pos = state.pos
  if (src.charCodeAt(pos) !== 0x23 /* # */) return false
  if (pos > 0 && !/\s/.test(src[pos - 1])) return false
  const m = tagRe.exec(src.slice(pos, state.posMax))
  // Not a number (#12, an issue), and not ending in a slash or dash.
  if (!m || /^[\d/_-]+$/.test(m[1]) || /[/-]$/.test(m[1])) return false
  if (!silent) {
    const t = state.push("wiki_tag", "", 0)
    t.content = m[1]
  }
  state.pos += m[0].length
  return true
}

// ==highlight==: markdown-it's ~~strikethrough~~, with = (markdown-it-mark's way).
function markTokenize(state, silent) {
  if (silent || state.src.charCodeAt(state.pos) !== 0x3d /* = */) return false
  const scanned = state.scanDelims(state.pos, true)
  let len = scanned.length
  if (len < 2) return false
  if (len % 2) {
    state.push("text", "", 0).content = "="
    len--
  }
  for (let i = 0; i < len; i += 2) {
    state.push("text", "", 0).content = "=="
    state.delimiters.push({ marker: 0x3d, length: 0, token: state.tokens.length - 1, end: -1, open: scanned.can_open, close: scanned.can_close })
  }
  state.pos += scanned.length
  return true
}

function markPairs(state, delimiters) {
  const lone = []
  for (const start of delimiters) {
    if (start.marker !== 0x3d || start.end === -1) continue
    const end = delimiters[start.end]
    Object.assign(state.tokens[start.token], { type: "mark_open", tag: "mark", nesting: 1, markup: "==", content: "" })
    Object.assign(state.tokens[end.token], { type: "mark_close", tag: "mark", nesting: -1, markup: "==", content: "" })
    const prev = state.tokens[end.token - 1]
    if (prev.type === "text" && prev.content === "=") lone.push(end.token - 1)
  }
  while (lone.length) {
    const i = lone.pop()
    let j = i + 1
    while (j < state.tokens.length && state.tokens[j].type === "mark_close") j++
    j--
    if (i !== j) [state.tokens[i], state.tokens[j]] = [state.tokens[j], state.tokens[i]]
  }
}

function markPostProcess(state) {
  markPairs(state, state.delimiters)
  for (const meta of state.tokens_meta) if (meta && meta.delimiters) markPairs(state, meta.delimiters)
}

// Links in an embedded note are relative to its folder, not the note showing it.
function rebase(state) {
  const base = state.env.wiki?.embedded && state.env.wiki.baseDir
  if (!base) return
  const fix = (t, a) => {
    const v = t.attrGet(a)
    if (v && !/^[a-z][a-z0-9+.-]*:/i.test(v) && !v.startsWith("/") && !v.startsWith("#")) t.attrSet(a, encodePath(base) + "/" + v)
  }
  for (const t of state.tokens) {
    if (t.type !== "inline") continue
    for (const c of t.children || []) {
      if (c.type === "link_open") fix(c, "href")
      else if (c.type === "image") fix(c, "src")
    }
  }
}

// ---- rendering ----

function lookup(env, name, wantText) {
  const w = env.wiki
  if (!w || !w.lookup) return undefined
  if (!w.from) return null // untitled: nowhere to look from
  return w.lookup(w.from, name, wantText)
}

/** <a> for a link to m, found (r: {path}), not (null) or not yet (undefined). */
function linkHTML(m, r, env, cls = "wikilink", text = shown(m)) {
  const sub = m.sub ? ` data-sub="${escapeHTML(m.sub)}"` : ""
  if (!m.name) {
    // This note's own heading or block.
    const from = env.wiki?.from || ""
    const path = from ? ` data-path="${escapeHTML(from)}"` : ""
    return `<a class="${cls}" href="#${escapeHTML(m.sub.startsWith("^") ? m.sub : slug(m.sub.split("#").pop()))}"${path}${sub}>${escapeHTML(text)}</a>`
  }
  if (r === undefined) return `<a class="${cls} pending"${sub}>${escapeHTML(text)}</a>`
  if (r === null) return `<a class="${cls} unresolved" title="${escapeHTML(`No “${m.name}” in this vault`)}"${sub}>${escapeHTML(text)}</a>`
  return `<a class="${cls}" href="${escapeHTML(encodePath(r.path))}" data-path="${escapeHTML(r.path)}"${sub}>${escapeHTML(text)}</a>`
}

function embedInline(m, env) {
  const ext = extOf(m.name)
  if (imageExts.has(ext)) {
    const r = lookup(env, m.name, false)
    if (r === undefined) return `<span class="embed-pending">${escapeHTML(m.name)}</span>`
    if (r === null) return linkHTML(m, r, env)
    const size = /^(\d+)(?:x(\d+))?$/.exec(m.alias)
    const alt = size ? m.name : m.alias || m.name
    const dims = size ? ` width="${size[1]}"${size[2] ? ` height="${size[2]}"` : ""}` : ""
    return `<img src="${escapeHTML(encodePath(r.path))}" alt="${escapeHTML(alt)}"${dims}>`
  }
  // Audio, video, PDFs, other files (and notes amid text): a link to open it.
  const r = lookup(env, m.name, false)
  return linkHTML(m, r, env, isNote(m.name) ? "wikilink embed-link" : "wikilink embed-file")
}

function embedBlock(md, t, env, self) {
  const m = t.meta, w = env.wiki || {}
  const attrs = self.renderAttrs(t)
  const r = lookup(env, m.name, true)
  const title = linkHTML(m, r, env, "wikilink", m.alias || shown(m))
  const box = (cls, body) => `<div class="embed${cls}"${attrs}><div class="embed-title">${title}</div>${body}</div>\n`
  if (r === undefined) return box(" pending", "")
  if (r === null) return box(" unresolved", `<p class="embed-note">Not found in this vault.</p>`)
  const budget = w.budget || { n: 0 }
  const chain = w.chain || []
  if (chain.includes(r.path)) return box("", `<p class="embed-note">This note embeds itself.</p>`)
  if ((w.depth || 0) >= EMBED_DEPTH || budget.n >= EMBED_MAX) return box("", `<p class="embed-note">Embedded too deep to show.</p>`)
  if (!r.hasText) return box("", `<p class="embed-note">Can't show this file here.</p>`)
  budget.n++
  let text = r.text.replace(frontMatterRe, (fm) => "\n".repeat(fm.split("\n").length - 1))
  text = stripComments(text)
  let section = noteSection(md, text, m.sub)
  if (section === null) return box("", `<p class="embed-note">No “${escapeHTML(m.sub)}” in it.</p>`)
  if (section.length > EMBED_TEXT_MAX) section = section.slice(0, EMBED_TEXT_MAX)
  const dir = r.path.slice(0, r.path.lastIndexOf("/"))
  const inner = md.render(section, {
    wiki: { ...w, from: r.path, baseDir: dir, embedded: true, depth: (w.depth || 0) + 1, chain: [...chain, w.from, r.path].filter(Boolean), budget },
  })
  return box("", `<div class="embed-body">${inner}</div>`)
}

/** YAML front matter: --- at the very top, to --- (or ...) on a line of its own. */
export const frontMatterRe = /^---[ \t]*\r?\n(?:([\s\S]*?)\r?\n)?(?:---|\.\.\.)[ \t]*(?:\r?\n|$)/

/** The plugin: md.use(obsidian). Its core rules go before markdown.js's own (lines, ids, tasks). */
export function obsidian(md) {
  md.inline.ruler.before("link", "wikilink", wikilink)
  md.inline.ruler.before("link", "wiki_tag", tag)
  md.inline.ruler.before("emphasis", "mark", markTokenize)
  md.inline.ruler2.before("emphasis", "mark", markPostProcess)
  // On the blocks, before their text is parsed.
  md.core.ruler.before("inline", "wiki_callouts", callouts)
  md.core.ruler.before("inline", "wiki_embed_blocks", embedBlocks)
  md.core.ruler.before("inline", "wiki_block_ids", blockIds)
  md.core.ruler.after("inline", "wiki_rebase", rebase)

  const r = md.renderer.rules
  r.wiki_link = (toks, i, o, env) => linkHTML(toks[i].meta, toks[i].meta.name ? lookup(env, toks[i].meta.name, false) : null, env)
  r.wiki_embed = (toks, i, o, env) => embedInline(toks[i].meta, env)
  r.wiki_embed_block = (toks, i, o, env, self) => embedBlock(md, toks[i], env, self)
  r.wiki_tag = (toks, i) => `<span class="tag">#${escapeHTML(toks[i].content)}</span>`
  r.callout_icon = () => `<span class="callout-icon"></span>`
}

// ---- callout icons (drawn for Hammock: 24×24, stroked) ----

export const calloutIcons = {
  note: `<path d="M4 20h4L19 9l-4-4L4 16z"/>`,
  abstract: `<rect x="6" y="4" width="12" height="16" rx="2"/><path d="M9 9h6M9 13h6M9 17h4"/>`,
  info: `<circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7.5v.5"/>`,
  todo: `<circle cx="12" cy="12" r="9"/><path d="M8 12l3 3 5-6"/>`,
  tip: `<path d="M9 18h6M10 21h4M12 3a6 6 0 0 0-4 10.5c.8.8 1 1.5 1 2.5h6c0-1 .2-1.7 1-2.5A6 6 0 0 0 12 3z"/>`,
  success: `<path d="M5 12.5l4.5 4.5L19 7"/>`,
  question: `<circle cx="12" cy="12" r="9"/><path d="M9.5 9.5a2.5 2.5 0 1 1 3.5 2.3c-.7.3-1 .9-1 1.7M12 17v.5"/>`,
  warning: `<path d="M12 3.5L2.5 20h19z"/><path d="M12 10v4.5M12 17.5v.5"/>`,
  failure: `<path d="M6 6l12 12M18 6L6 18"/>`,
  danger: `<path d="M13 2L4 14h7l-1 8 9-12h-7z"/>`,
  bug: `<rect x="8" y="7" width="8" height="13" rx="4"/><path d="M12 11v9M4 13h4M16 13h4M5 7l3 2M19 7l-3 2M5 19l3-2M19 19l-3-2"/>`,
  example: `<path d="M9 6h11M9 12h11M9 18h11M4.5 6h.5M4.5 12h.5M4.5 18h.5"/>`,
  quote: `<path d="M5 17c2 0 4-2 4-5V7H5v5h4M15 17c2 0 4-2 4-5V7h-4v5h4"/>`,
}
