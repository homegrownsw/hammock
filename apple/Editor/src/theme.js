// The terminal theme as CodeMirror's: shared by editor panes (main.js) and
// diff panes (diff.js), so both look like the terminal beside them.

import { EditorView } from "@codemirror/view"
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language"
import { tags as t } from "@lezer/highlight"

export function hexToRGB(h) {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})/i.exec(h || "")
  return m ? [parseInt(m[1], 16), parseInt(m[2], 16), parseInt(m[3], 16)] : [128, 128, 128]
}
export function mix(a, b, f) {
  const x = hexToRGB(a), y = hexToRGB(b)
  return "#" + x.map((v, i) => Math.round(v + (y[i] - v) * f).toString(16).padStart(2, "0")).join("")
}
// c at alpha f: over the background it looks like mix(background, c, f), but
// lets the selection (drawn beneath the text) show through.
export function tint(c, f) {
  const x = hexToRGB(c)
  return `rgba(${x[0]}, ${x[1]}, ${x[2]}, ${f})`
}

// The terminal theme's colours, as syntax colours: its palette carries the
// theme's character into code.
export function themeExtension(config) {
  const th = config.theme, p = th.palette || []
  const pal = (i, fallback) => p[i] || fallback
  const dim = mix(th.foreground, th.background, 0.45)
  const fam = `"${(config.font.family || "Menlo").replace(/"/g, "")}", ui-monospace, Menlo, "Symbols Nerd Font Mono", monospace`
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
