// Hammock's editor panes: CodeMirror in a WKWebView, on the Mac and the
// iPhone. The daemon keeps the buffer; this is one client of it, through the
// app (EditorWebView.swift): the app hands us the buffer and the updates
// others make (receive), and sends on what we push. Edits follow
// @codemirror/collab: we push our updates at the version we have, and the
// daemon takes them only if nobody got there first (else we rebase on what
// arrives and push again).

import { EditorState, Compartment, StateField, StateEffect, RangeSet, ChangeSet, EditorSelection } from "@codemirror/state"
import {
  EditorView, keymap, lineNumbers, highlightActiveLineGutter, highlightSpecialChars, drawSelection,
  dropCursor, rectangularSelection, crosshairCursor, highlightActiveLine, gutter, GutterMarker,
} from "@codemirror/view"
import {
  defaultKeymap, history, historyKeymap, indentWithTab, undo, redo, selectAll,
  cursorCharLeft, cursorCharRight, cursorLineUp, cursorLineDown, cursorGroupLeft, cursorGroupRight,
  cursorLineBoundaryBackward, cursorLineBoundaryForward, indentMore, indentLess,
} from "@codemirror/commands"
import {
  indentOnInput, syntaxHighlighting, bracketMatching, foldGutter, foldKeymap, indentUnit,
  HighlightStyle, LanguageDescription,
} from "@codemirror/language"
import { languages } from "@codemirror/language-data"
import {
  searchKeymap, highlightSelectionMatches, openSearchPanel, closeSearchPanel, findNext, findPrevious,
  gotoLine, search,
} from "@codemirror/search"
import { autocompletion, completionKeymap, closeBrackets, closeBracketsKeymap } from "@codemirror/autocomplete"
import { collab, receiveUpdates, sendableUpdates, getSyncedVersion } from "@codemirror/collab"
import { unifiedMergeView } from "@codemirror/merge"
import { vim } from "@replit/codemirror-vim"
import { tags as t } from "@lezer/highlight"

const post = (m) => {
  try { window.webkit.messageHandlers.hammock.postMessage(m) } catch (e) { console.log("hammock:", JSON.stringify(m)) }
}

// ---- configuration (the app's settings and theme) ----

let config = {
  theme: { background: "#1e1e1e", foreground: "#d4d4d4", selection: "#264f78", cursor: "#d4d4d4", palette: [], dark: true },
  font: { family: "Menlo", size: 13 },
  wrap: false, lineNumbers: true, tabWidth: 4, indent: "spaces", vim: false, readOnly: false, platform: "mac",
}
let info = { name: "", language: "", tabWidth: 0, indent: "", readOnly: false }

const themeC = new Compartment(), wrapC = new Compartment(), numbersC = new Compartment(), tabC = new Compartment()
const vimC = new Compartment(), langC = new Compartment(), readC = new Compartment(), diffC = new Compartment()

function hexToRGB(h) {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})/i.exec(h || "")
  return m ? [parseInt(m[1], 16), parseInt(m[2], 16), parseInt(m[3], 16)] : [128, 128, 128]
}
function mix(a, b, f) {
  const x = hexToRGB(a), y = hexToRGB(b)
  return "#" + x.map((v, i) => Math.round(v + (y[i] - v) * f).toString(16).padStart(2, "0")).join("")
}
// c at alpha f: over the background it looks like mix(background, c, f), but
// lets the selection (drawn beneath the text) show through.
function tint(c, f) {
  const x = hexToRGB(c)
  return `rgba(${x[0]}, ${x[1]}, ${x[2]}, ${f})`
}

// The terminal theme's colours, as syntax colours: its palette carries the
// theme's character into code.
function themeExtension() {
  const th = config.theme, p = th.palette || []
  const pal = (i, fallback) => p[i] || fallback
  const dim = mix(th.foreground, th.background, 0.45)
  const fam = `"${(config.font.family || "Menlo").replace(/"/g, "")}", ui-monospace, Menlo, monospace`
  const base = EditorView.theme({
    "&": { color: th.foreground, backgroundColor: th.background, height: "100%", fontSize: `${config.font.size}px` },
    ".cm-scroller": { fontFamily: fam, lineHeight: "1.45" },
    ".cm-content": { caretColor: th.cursor, padding: "6px 0" },
    ".cm-cursor, .cm-dropCursor": { borderLeftColor: th.cursor, borderLeftWidth: "2px" },
    "&.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground, .cm-selectionBackground, .cm-content ::selection":
      { backgroundColor: th.selection + (th.selection.length === 7 ? "" : "") },
    ".cm-activeLine": { backgroundColor: tint(th.foreground, 0.05) },
    ".cm-activeLineGutter": { backgroundColor: "transparent", color: th.foreground },
    ".cm-gutters": { backgroundColor: th.background, color: dim, border: "none" },
    ".cm-lineNumbers .cm-gutterElement": { padding: "0 10px 0 14px" },
    ".cm-foldPlaceholder": { backgroundColor: "transparent", border: "none", color: dim },
    ".cm-selectionMatch": { backgroundColor: tint(th.selection, 0.5) },
    ".cm-matchingBracket, &.cm-focused .cm-matchingBracket": { backgroundColor: tint(th.foreground, 0.16), outline: "none" },
    ".cm-searchMatch": { backgroundColor: tint(pal(3, "#e5c07b"), 0.3), outline: `1px solid ${mix(th.background, pal(3, "#e5c07b"), 0.6)}` },
    ".cm-searchMatch.cm-searchMatch-selected": { backgroundColor: tint(pal(3, "#e5c07b"), 0.55) },
    ".cm-panels": { backgroundColor: mix(th.background, th.foreground, 0.04), color: th.foreground, fontFamily: "-apple-system, system-ui, sans-serif", fontSize: "12px" },
    ".cm-panels.cm-panels-top": { borderBottom: `1px solid ${mix(th.background, th.foreground, 0.12)}` },
    ".cm-panels.cm-panels-bottom": { borderTop: `1px solid ${mix(th.background, th.foreground, 0.12)}` },
    ".cm-panel input, .cm-panel button, .cm-textfield": { font: "inherit", color: th.foreground, backgroundColor: mix(th.background, th.foreground, 0.08), border: `1px solid ${mix(th.background, th.foreground, 0.18)}`, borderRadius: "5px", padding: "2px 6px" },
    ".cm-button": { backgroundImage: "none" },
    ".cm-tooltip": { backgroundColor: mix(th.background, th.foreground, 0.06), color: th.foreground, border: `1px solid ${mix(th.background, th.foreground, 0.15)}`, borderRadius: "6px" },
    ".cm-tooltip-autocomplete ul li[aria-selected]": { backgroundColor: th.selection, color: th.foreground },
    ".cm-git": { width: "3px", paddingLeft: "2px" },
    ".cm-git-mark": { width: "3px", height: "100%", borderRadius: "1px" },
    ".cm-git-added": { backgroundColor: pal(2, "#98c379") },
    ".cm-git-modified": { backgroundColor: pal(4, "#61afef") },
    ".cm-git-deleted": { height: "0", borderTop: `4px solid transparent`, borderBottom: "4px solid transparent", borderLeft: `5px solid ${pal(1, "#e06c75")}`, width: "0", marginTop: "-4px", borderRadius: "0" },
    ".cm-changedLine": { backgroundColor: tint(pal(2, "#98c379"), 0.1) + " !important" },
    ".cm-deletedChunk": { backgroundColor: tint(pal(1, "#e06c75"), 0.1) },
    ".cm-insertedLine, .cm-changedText": { background: tint(pal(2, "#98c379"), 0.22) + " !important" },
    ".cm-deletedLine, .cm-deletedText, del": { background: tint(pal(1, "#e06c75"), 0.22) + " !important" },
    ".cm-vim-panel": { fontFamily: fam },
    ".cm-fat-cursor": { background: th.cursor + " !important", color: th.background + " !important" },
    "&:not(.cm-focused) .cm-fat-cursor": { background: "none !important", outline: `solid 1px ${th.cursor} !important` },
  }, { dark: th.dark })
  const style = HighlightStyle.define([
    { tag: [t.keyword, t.modifier, t.controlKeyword, t.operatorKeyword, t.definitionKeyword, t.moduleKeyword], color: pal(5, "#c678dd") },
    { tag: [t.string, t.special(t.string), t.regexp, t.inserted], color: pal(2, "#98c379") },
    { tag: [t.number, t.bool, t.null, t.atom, t.constant(t.name)], color: pal(3, "#d19a66") },
    { tag: [t.comment, t.lineComment, t.blockComment, t.docComment], color: dim, fontStyle: "italic" },
    { tag: [t.function(t.variableName), t.function(t.propertyName), t.macroName], color: pal(4, "#61afef") },
    { tag: [t.typeName, t.className, t.namespace, t.tagName, t.standard(t.name)], color: pal(6, "#56b6c2") },
    { tag: [t.propertyName, t.attributeName], color: pal(12, pal(4, "#61afef")) },
    { tag: [t.definition(t.variableName), t.labelName], color: th.foreground },
    { tag: [t.escape, t.character, t.special(t.variableName)], color: pal(11, pal(3, "#e5c07b")) },
    { tag: [t.deleted, t.invalid], color: pal(1, "#e06c75") },
    { tag: [t.heading], color: pal(4, "#61afef"), fontWeight: "bold" },
    { tag: [t.strong], fontWeight: "bold" },
    { tag: [t.emphasis], fontStyle: "italic" },
    { tag: [t.link, t.url], color: pal(6, "#56b6c2"), textDecoration: "underline" },
    { tag: [t.meta, t.processingInstruction, t.annotation], color: pal(13, pal(5, "#c678dd")) },
    { tag: [t.punctuation, t.separator, t.bracket], color: mix(th.foreground, th.background, 0.2) },
  ])
  return [base, syntaxHighlighting(style)]
}

function tabExtension() {
  const width = info.tabWidth || config.tabWidth || 4
  const tabs = (info.indent || config.indent) === "tabs"
  return [EditorState.tabSize.of(width), indentUnit.of(tabs ? "\t" : " ".repeat(width))]
}

const readOnlyExt = () => (config.readOnly || info.readOnly) ? [EditorState.readOnly.of(true), EditorView.editable.of(false)] : []

// ---- git marks ----

const setGit = StateEffect.define()
const gitField = StateField.define({
  create: () => [],
  update(v, tr) {
    for (const e of tr.effects) if (e.is(setGit)) return e.value
    return v
  },
})
class GitMarker extends GutterMarker {
  constructor(kind) { super(); this.kind = kind }
  eq(o) { return o.kind === this.kind }
  toDOM() {
    const d = document.createElement("div")
    d.className = "cm-git-mark cm-git-" + this.kind
    return d
  }
}
const gitMarkers = { added: new GitMarker("added"), modified: new GitMarker("modified"), deleted: new GitMarker("deleted") }
const gitGutter = [gitField, gutter({
  class: "cm-git",
  markers(view) {
    const marks = view.state.field(gitField), doc = view.state.doc, ranges = []
    for (const m of marks) {
      const kind = m.kind
      if (kind === "deleted") {
        const line = Math.min(Math.max(m.from, 1), doc.lines)
        ranges.push(gitMarkers.deleted.range(doc.line(line).from))
        continue
      }
      for (let l = m.from; l <= Math.min(m.to, doc.lines); l++) ranges.push(gitMarkers[kind].range(doc.line(l).from))
    }
    ranges.sort((a, b) => a.from - b.from)
    return RangeSet.of(ranges, true)
  },
  lineMarkerChange: (u) => u.transactions.some((tr) => tr.effects.some((e) => e.is(setGit))),
})]

// ---- the view ----

let view = null
let pushing = false
let pushedAt = -1
let pushedCount = 0
// The daemon's text at our synced version: what our unconfirmed updates
// apply to, so they can be carried over a reload (load) instead of lost.
let syncedDoc = null
let languageName = ""

function keys() {
  return keymap.of([
    { key: "Mod-s", run: () => { post({ type: "save" }); return true }, preventDefault: true },
    { key: "Mod-Alt-Shift-s", run: () => { post({ type: "saveAs" }); return true }, preventDefault: true },
    { key: "Mod-l", run: gotoLine, preventDefault: true },
    ...closeBracketsKeymap, ...defaultKeymap, ...searchKeymap, ...historyKeymap, ...foldKeymap, ...completionKeymap, indentWithTab,
  ])
}

function extensions(version) {
  return [
    vimC.of(config.vim ? vim() : []),
    collab({ startVersion: version, clientID: clientID() }),
    // Lines end at "\n" alone, as the daemon counts them (it keeps "\r\n"
    // files as "\n"): a lone "\r" breaking a line here would number lines
    // differently from the daemon (agents' selections, Go to Line) and show
    // a file that isn't what's saved.
    EditorState.lineSeparator.of("\n"),
    // …so pasted "\r\n" text is made "\n" here, as CodeMirror otherwise would.
    EditorView.clipboardInputFilter.of((text) => text.replace(/\r\n?/g, "\n")),
    numbersC.of(config.lineNumbers ? [lineNumbers(), highlightActiveLineGutter()] : []),
    gitGutter,
    foldGutter({ markerDOM: (open) => { const s = document.createElement("span"); s.textContent = open ? "⌄" : "›"; s.style.opacity = "0.55"; return s } }),
    highlightSpecialChars(), history(), drawSelection(), dropCursor(),
    EditorState.allowMultipleSelections.of(true), indentOnInput(), bracketMatching(), closeBrackets(),
    autocompletion({ activateOnTyping: true }), rectangularSelection(), crosshairCursor(),
    highlightActiveLine(), highlightSelectionMatches(), search({ top: true }),
    keys(),
    themeC.of(themeExtension()), wrapC.of(config.wrap ? EditorView.lineWrapping : []), tabC.of(tabExtension()),
    langC.of([]), readC.of(readOnlyExt()), diffC.of([]),
    EditorView.updateListener.of(onUpdate),
    EditorView.domEventHandlers({ focus: () => { post({ type: "focus" }) } }),
  ]
}

// One id per page: our updates come back carrying it.
let _cid = null
function clientID() {
  if (!_cid) _cid = Math.random().toString(36).slice(2, 12)
  return _cid
}

let selTimer = null
function onUpdate(u) {
  if (u.docChanged) queueMicrotask(maybePush)
  if (u.selectionSet || u.docChanged) {
    clearTimeout(selTimer)
    selTimer = setTimeout(reportSelection, 150)
  }
}

function reportSelection() {
  if (!view) return
  const s = view.state.selection.main, line = view.state.doc.lineAt(s.head)
  post({
    type: "select", version: getSyncedVersion(view.state), anchor: s.anchor, head: s.head,
    line: line.number, column: s.head - line.from + 1, selected: Math.abs(s.head - s.anchor),
    // Only when nothing's unconfirmed are offsets at the synced version.
    synced: sendableUpdates(view.state).length === 0,
  })
}

function maybePush() {
  if (!view || pushing) return
  const updates = sendableUpdates(view.state)
  if (!updates.length) return
  pushing = true
  pushedAt = getSyncedVersion(view.state)
  pushedCount = updates.length
  post({
    type: "push", version: pushedAt,
    updates: updates.map((u) => ({ changes: JSON.stringify(u.changes.toJSON()), clientID: u.clientID })),
  })
}

async function applyLanguage() {
  if (!view) return
  let desc = null
  if (info.language) desc = languages.find((l) => l.name.toLowerCase() === info.language.toLowerCase())
  if (!desc && info.name) desc = LanguageDescription.matchFilename(languages, info.name)
  const name = desc ? desc.name : ""
  if (name === languageName && view.state.facet(EditorState.languageData).length) return
  languageName = name
  post({ type: "language", name })
  if (!desc) { view.dispatch({ effects: langC.reconfigure([]) }); return }
  try {
    const support = await desc.load()
    if (view && languageName === name) view.dispatch({ effects: langC.reconfigure(support) })
  } catch (e) { console.log("language", e) }
}

// ---- what the app calls (window.hammock) ----

window.hammock = {
  /** Settings and theme; anything left out stays. */
  configure(c) {
    const old = config
    config = { ...config, ...c, theme: { ...config.theme, ...(c.theme || {}) }, font: { ...config.font, ...(c.font || {}) } }
    document.documentElement.style.background = config.theme.background
    document.body.style.background = config.theme.background
    if (!view) return
    const effects = [themeC.reconfigure(themeExtension()), wrapC.reconfigure(config.wrap ? EditorView.lineWrapping : []),
      tabC.reconfigure(tabExtension()), readC.reconfigure(readOnlyExt()),
      numbersC.reconfigure(config.lineNumbers ? [lineNumbers(), highlightActiveLineGutter()] : [])]
    if (old.vim !== config.vim) effects.push(vimC.reconfigure(config.vim ? vim() : []))
    view.dispatch({ effects })
  },

  /** The file's name (for its language), a chosen language, indentation, read-only. */
  setInfo(i) {
    const before = JSON.stringify(info)
    info = { ...info, ...i }
    if (!view || JSON.stringify(info) === before) return
    view.dispatch({ effects: [tabC.reconfigure(tabExtension()), readC.reconfigure(readOnlyExt())] })
    applyLanguage()
  },

  /** The buffer, afresh: on attaching, and after reconnecting. */
  load(m) {
    const parent = document.getElementById("editor")
    const keepScroll = view && view.scrollDOM.scrollTop
    const hadFocus = view && view.hasFocus
    const carried = view && !m.discardLocal ? unsentOver(m) : null
    if (view) view.destroy()
    pushing = false
    const sel = m.selection ? EditorSelection.single(clamp(m.selection.anchor, m.text), clamp(m.selection.head, m.text)) : undefined
    const state = EditorState.create({ doc: m.text, selection: sel, extensions: extensions(m.version) })
    view = new EditorView({ state, parent })
    syncedDoc = state.doc
    languageName = "\u0000"
    applyLanguage()
    if (m.git) view.dispatch({ effects: setGit.of(m.git) })
    if (m.selection) view.dispatch({ effects: EditorView.scrollIntoView(sel.main.head, { y: "center" }) })
    else if (keepScroll) view.scrollDOM.scrollTop = keepScroll
    if (hadFocus || m.focus) view.focus()
    // Typed while offline (or as the daemon restarted): ours again, pushed.
    if (carried && !carried.empty && carried.length === view.state.doc.length) {
      view.dispatch({ changes: carried, userEvent: "input", scrollIntoView: false })
    }
  },

  /** Updates the daemon took (others' and ours). */
  receive(m) {
    if (!view) return
    const updates = (m.updates || []).map((u) => ({ changes: ChangeSet.fromJSON(JSON.parse(u.changes)), clientID: u.clientID }))
    if (updates.length) {
      const synced = getSyncedVersion(view.state)
      if (m.version - updates.length !== synced) {
        // Out of step (missed some): ask for the whole buffer again.
        post({ type: "resync" })
        return
      }
      view.dispatch(receiveUpdates(view.state, updates))
      for (const u of updates) syncedDoc = u.changes.apply(syncedDoc)
    }
    if (m.git) view.dispatch({ effects: setGit.of(m.git) })
    if (m.selection) this.select(m.selection)
    if (updates.length && !pushing) maybePush()
  },

  /** Whether the daemon took our push. */
  pushResult(accepted) {
    pushing = false
    if (!view) return
    // Taken: our updates came back first (confirming them); push anything
    // typed since. Refused: what we missed has arrived (we're rebased), or
    // it's on its way and receive pushes.
    if (accepted || getSyncedVersion(view.state) !== pushedAt) maybePush()
  },

  setGit(marks) { if (view) view.dispatch({ effects: setGit.of(marks || []) }) },

  /** Select and show (an agent pointing at something). */
  select(s) {
    if (!view) return
    const len = view.state.doc.length
    const sel = EditorSelection.single(Math.min(s.anchor, len), Math.min(s.head, len))
    view.dispatch({ selection: sel, effects: EditorView.scrollIntoView(sel.main, { y: "center" }) })
  },

  /** The buffer against the file on disk, chunk by chunk (accept or reject each). */
  showDiff(diskText) {
    if (!view) return
    view.dispatch({ effects: diffC.reconfigure(unifiedMergeView({ original: diskText, mergeControls: !(config.readOnly || info.readOnly), highlightChanges: true, gutter: true })) })
  },
  hideDiff() { if (view) view.dispatch({ effects: diffC.reconfigure([]) }) },

  languageNames() { return languages.map((l) => l.name).sort((a, b) => a.localeCompare(b)) },
  languageName() { return languageName === "\u0000" ? "" : languageName },

  command(name) {
    if (!view) return false
    const cmds = {
      undo, redo, selectAll, find: openSearchPanel, closeFind: closeSearchPanel, findNext, findPrevious, gotoLine,
      left: cursorCharLeft, right: cursorCharRight, up: cursorLineUp, down: cursorLineDown,
      wordLeft: cursorGroupLeft, wordRight: cursorGroupRight, lineStart: cursorLineBoundaryBackward, lineEnd: cursorLineBoundaryForward,
      indent: indentMore, outdent: indentLess,
      tab: (v) => v.state.readOnly ? false : (v.dispatch(v.state.replaceSelection((info.indent || config.indent) === "tabs" ? "\t" : " ".repeat(info.tabWidth || config.tabWidth || 4))), true),
      focus: (v) => (v.focus(), true), blur: (v) => (v.contentDOM.blur(), true),
    }
    const c = cmds[name]
    return c ? c(view) : false
  },

  /** What's selected (for the app's context menu, services and Look Up). */
  selection() {
    if (!view) return ""
    return view.state.sliceDoc(view.state.selection.main.from, view.state.selection.main.to)
  },

  /** The CodeMirror view (tests, and debugging in Web Inspector). */
  view() { return view },

  /** Insert text at the selection (iOS's accessory bar, drops). */
  insert(text) {
    if (!view || view.state.readOnly) return
    view.dispatch(view.state.replaceSelection(text))
  },
}

// unsentOver is the edits made here the daemon hasn't confirmed, moved onto
// the text of snapshot m (null: none, or nothing to carry). A push in flight
// that the snapshot's version could include is taken as included.
function unsentOver(m) {
  const pending = sendableUpdates(view.state)
  if (!pending.length || !syncedDoc) return null
  if (m.text === view.state.doc.toString()) return null // all of it arrived
  let base = syncedDoc, rest = pending
  if (pushing && m.version >= pushedAt + pushedCount) {
    for (const u of pending.slice(0, pushedCount)) base = u.changes.apply(base)
    rest = pending.slice(pushedCount)
  }
  if (!rest.length) return null
  let mine = rest[0].changes
  for (const u of rest.slice(1)) mine = mine.compose(u.changes)
  try {
    return mine.map(diffTo(base.toString(), m.text))
  } catch (e) {
    console.log("hammock: couldn't carry edits over", e)
    return null
  }
}

// diffTo is the change from a to b: one replacement between what they start
// and end with in common.
function diffTo(a, b) {
  let p = 0
  while (p < a.length && p < b.length && a.charCodeAt(p) === b.charCodeAt(p)) p++
  let s = 0
  while (s < a.length - p && s < b.length - p && a.charCodeAt(a.length - 1 - s) === b.charCodeAt(b.length - 1 - s)) s++
  return ChangeSet.of(p === a.length && p === b.length ? [] : [{ from: p, to: a.length - s, insert: b.slice(p, b.length - s) }], a.length)
}

function clamp(n, text) { return Math.max(0, Math.min(n || 0, text.length)) }

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => post({ type: "ready" }))
else post({ type: "ready" })
