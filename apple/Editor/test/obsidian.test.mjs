// Obsidian's Markdown in the preview (src/obsidian.js, src/markdown.js).
// Run: npm test (in apple/Editor), or just editor-test.

import { test } from "node:test"
import assert from "node:assert/strict"
import { createMarkdown, taskKey } from "../src/markdown.js"
import { stripComments, noteSection, parseWiki, CALLOUT_KINDS, calloutIcons, EMBED_DEPTH } from "../src/obsidian.js"

const md = createMarkdown(() => "")
const plain = createMarkdown(() => "", { withObsidian: false })

// A made-up vault: what the daemon would answer.
const vault = {
  "/v/Home.md": "",
  "/v/Projects/Garden Plan.md": "# Garden Plan\n\nIntro.\n\n## Beds\n\nCarrots and [[Seeds]].\n\n### Raised\n\nTwo of them.\n\n## Paths\n\nGravel.\n",
  "/v/Projects/Seeds.md": "---\ntags: [x]\n---\n- [ ] Sow beans ^sow\n- [x] Buy compost\n\nA para. ^para\n\n[relative](sub/other.md) ![pic](img/p.png)\n",
  "/v/Loop.md": "Before\n\n![[Loop]]\n",
  "/v/A.md": "In A\n\n![[B]]\n",
  "/v/B.md": "In B\n\n![[C]]\n",
  "/v/C.md": "In C\n\n![[D]]\n",
  "/v/D.md": "In D\n\n![[E]]\n",
  "/v/E.md": "In E\n",
  "/v/Images/diagram.svg": null,
  "/v/report.pdf": null,
  "/v/song.mp3": null,
}
const byName = (name) => {
  const want = name.includes(".") && !/\.md$/.test(name) ? name : name.replace(/\.md$/, "") + ".md"
  return Object.keys(vault).find((p) => p.endsWith("/" + want)) || null
}
function env(from = "/v/Home.md", { pending = false } = {}) {
  const asked = []
  return {
    asked,
    wiki: {
      from, depth: 0, chain: [], budget: { n: 0 },
      lookup(f, target, wantText) {
        asked.push({ from: f, target, wantText })
        if (pending) return undefined
        const path = byName(target)
        if (!path) return null
        const text = vault[path]
        return { path, text: text ?? "", hasText: wantText && text !== null }
      },
    },
  }
}
const render = (src, e = env()) => md.render(src, e)

test("plain Markdown renders as before", () => {
  const docs = [
    "# Title\n\nSome *emphasis*, **strong**, `code`, ~~gone~~ and a [link](other.md#part).\n",
    "Setext\n===\n\nAnother\n---\n",
    "- one\n- two\n  - nested\n\n1. first\n2. second\n",
    "- [ ] task\n- [x] done\n",
    "> A quote.\n> Still quoting.\n\n> [not a callout] text\n",
    "| a | b |\n|---|---|\n| 1 | 2 |\n",
    "```js\nconst x = [[1]] // %% not a comment %%\n```\n\n    indented [[code]]\n",
    "Inline `[[not a link]]` and `==not marked==` and `#nottag`.\n",
    "Issue #123, C# and a#b, https://example.com/#frag, [frag](#part).\n",
    "a == b, x = y, 2 ^ 3 and caret^up.\n",
    "![image](shot.png) <b>html</b>\n\n<div>block html</div>\n",
    "Line with trailing  \nbreak and a footnote-ish [^1].\n",
    "[ref]: https://example.com\n\nUse [ref].\n",
  ]
  for (const d of docs) assert.equal(md.render(d, env()), plain.render(d, {}), d)
})

test("wikilinks", () => {
  const h = render("See [[Garden Plan]], [[Garden Plan|the plan]] and [[Garden Plan#Beds]].\n")
  assert.match(h, /<a class="wikilink" href="\/v\/Projects\/Garden%20Plan\.md" data-path="\/v\/Projects\/Garden Plan\.md">Garden Plan<\/a>/)
  assert.match(h, />the plan<\/a>/)
  assert.match(h, /data-sub="Beds">Garden Plan &gt; Beds<\/a>/)
  // This note's own heading and block.
  const own = render("[[#My Heading]] and [[#^abc]]\n")
  assert.match(own, /<a class="wikilink" href="#my-heading" data-path="\/v\/Home\.md" data-sub="My Heading">My Heading<\/a>/)
  assert.match(own, /href="#\^abc"[^>]*data-sub="\^abc">\^abc<\/a>/)
  // Paths, extensions, other files.
  assert.match(render("[[Projects/Seeds]]\n"), /data-path="\/v\/Projects\/Seeds\.md">Projects\/Seeds</)
  assert.match(render("[[Seeds.md]]\n"), /data-path="\/v\/Projects\/Seeds\.md"/)
  assert.match(render("[[report.pdf]]\n"), /data-path="\/v\/report\.pdf"/)
  assert.match(render("[[Seeds#^sow]]\n"), /data-sub="\^sow"/)
  // Not found, and not known yet.
  assert.match(render("[[Nowhere]]\n"), /<a class="wikilink unresolved" title="[^"]*">Nowhere<\/a>/)
  assert.match(render("[[Garden Plan]]\n", env("/v/Home.md", { pending: true })), /<a class="wikilink pending">Garden Plan<\/a>/)
  // Untitled: nowhere to look from.
  assert.match(render("[[Garden Plan]]\n", env("")), /unresolved/)
  // In a table, | is written \|.
  assert.match(render("| a |\n|---|\n| [[Garden Plan\\|plan]] |\n"), />plan<\/a>/)
  // In code, and escaped: as written.
  assert.match(render("`[[Garden Plan]]`\n"), /<code>\[\[Garden Plan\]\]<\/code>/)
  assert.doesNotMatch(render("\\[[Garden Plan]]\n"), /wikilink/)
  // Its text escaped.
  assert.match(render("[[Nowhere|<b>x</b>]]\n"), /&lt;b&gt;x&lt;\/b&gt;/)
})

test("parseWiki", () => {
  assert.deepEqual(parseWiki("Note"), { name: "Note", sub: "", alias: "" })
  assert.deepEqual(parseWiki("folder/Note#H1#H2|shown"), { name: "folder/Note", sub: "H1#H2", alias: "shown" })
  assert.deepEqual(parseWiki("#^block"), { name: "", sub: "^block", alias: "" })
  assert.deepEqual(parseWiki("pic.png|300x200"), { name: "pic.png", sub: "", alias: "300x200" })
})

test("embedded images and files", () => {
  assert.match(render("![[diagram.svg]]\n"), /<img src="\/v\/Images\/diagram\.svg" alt="diagram\.svg">/)
  assert.match(render("![[diagram.svg|300]]\n"), /alt="diagram\.svg" width="300">/)
  assert.match(render("![[diagram.svg|300x200]]\n"), /width="300" height="200">/)
  assert.match(render("![[diagram.svg|A diagram]]\n"), /alt="A diagram">/)
  assert.match(render("![[missing.png]]\n"), /wikilink unresolved/)
  assert.match(render("![[diagram.svg]]\n", env("/v/Home.md", { pending: true })), /embed-pending/)
  assert.match(render("![[report.pdf]]\n"), /<a class="wikilink embed-file" href="\/v\/report\.pdf"/)
  assert.match(render("![[song.mp3]]\n"), /embed-file/)
  // A note amid text: a link.
  assert.match(render("See ![[Seeds]] here.\n"), /<p data-line="0" data-line-end="1">See <a class="wikilink embed-link"/)
})

test("embedded notes", () => {
  const e = env()
  const h = render("Intro\n\n![[Seeds]]\n", e)
  assert.ok(e.asked.some((a) => a.target === "Seeds" && a.wantText), "asks for its text")
  assert.match(h, /<div class="embed" data-line="2" data-line-end="3"><div class="embed-title"><a class="wikilink" href="\/v\/Projects\/Seeds\.md"/)
  const body = h.slice(h.indexOf("embed-body"))
  assert.doesNotMatch(body, /data-line|data-anchor|data-task-key/, "no lines, ids or tickable boxes from another file")
  assert.match(body, /<input type="checkbox" class="task" disabled>Sow beans/)
  assert.match(body, /class="task" disabled checked>Buy compost/)
  assert.doesNotMatch(body, /tags: \[x\]|\^sow/, "front matter and block ids hidden")
  assert.match(body, /href="\/v\/Projects\/sub\/other\.md"/, "its links relative to its folder")
  assert.match(body, /src="\/v\/Projects\/img\/p\.png"/)
  // A section, a block.
  const sec = render("![[Garden Plan#Beds]]\n")
  assert.match(sec, /Carrots/)
  assert.match(sec, /Two of them/)
  assert.doesNotMatch(sec, /Intro|Gravel/)
  const deeper = render("![[Garden Plan#Beds#Raised]]\n")
  assert.match(deeper, /Two of them/)
  assert.doesNotMatch(deeper, /Carrots/)
  const block = render("![[Seeds#^sow]]\n")
  assert.match(block, /Sow beans/)
  assert.doesNotMatch(block, /Buy compost|A para/)
  assert.match(render("![[Seeds#^para]]\n"), /<p>A para\.<\/p>/)
  assert.match(render("![[Garden Plan#Nope]]\n"), /No “Nope” in it/)
  // Links in it lead from it.
  const ne = env()
  render("![[Garden Plan]]\n", ne)
  assert.ok(ne.asked.some((a) => a.from === "/v/Projects/Garden Plan.md" && a.target === "Seeds"))
  // Not found, not known yet.
  assert.match(render("![[Nowhere]]\n"), /<div class="embed unresolved"/)
  assert.match(render("![[Seeds]]\n", env("/v/Home.md", { pending: true })), /<div class="embed pending"/)
  // Several on their own lines: each its own.
  assert.equal(render("![[Seeds]]\n![[Garden Plan]]\n").match(/<div class="embed"/g).length, 2)
  // Inside a callout and a list.
  assert.match(render("> [!note]\n> ![[Seeds]]\n"), /callout-content">\s*<div class="embed" data-line="1"/)
})

test("embeds stop: themselves, too deep", () => {
  const loop = render("![[Loop]]\n", env("/v/Loop.md"))
  assert.match(loop, /This note embeds itself/)
  const chain = render("![[A]]\n")
  for (const n of ["In A", "In B", "In C"]) assert.match(chain, new RegExp(n))
  assert.equal(EMBED_DEPTH, 3)
  assert.doesNotMatch(chain, /In D/)
  assert.match(chain, /Embedded too deep/)
})

test("callouts", () => {
  const h = render("> [!tip] Water daily\n> In the *morning*.\n")
  assert.match(h, /^<div class="callout" data-callout="tip" data-line="0" data-line-end="2">\s*<div class="callout-title">\s*<span class="callout-icon"><\/span>Water daily<\/div>\s*<div class="callout-content">\s*<p data-line="1" data-line-end="2">In the <em>morning<\/em>\.<\/p>\s*<\/div>\s*<\/div>/)
  assert.match(render("> [!TLDR]\n> Short.\n"), /data-callout="abstract".*Tldr<\/div>/s)
  assert.match(render("> [!NOTE]\n> GitHub's.\n"), /data-callout="note".*>Note<\/div>/s)
  assert.match(render("> [!whatever] Mine\n"), /data-callout="note".*>Mine<\/div>/s)
  const folded = render("> [!faq]- Why?\n> Because.\n")
  assert.match(folded, /^<details class="callout" data-callout="question"[^>]*>\s*<summary class="callout-title">/)
  assert.doesNotMatch(folded, /open=""/)
  assert.match(render("> [!faq]+ Why?\n> Because.\n"), /^<details class="callout" data-callout="question" open=""/)
  const nested = render("> [!warning] Outer\n> > [!danger] Inner\n> > Careful.\n")
  assert.match(nested, /data-callout="warning"[\s\S]*data-callout="danger"[\s\S]*Careful/)
  for (const [alias, kind] of [["hint", "tip"], ["check", "success"], ["caution", "warning"], ["fail", "failure"], ["error", "danger"], ["cite", "quote"], ["important", "tip"]]) {
    assert.match(render(`> [!${alias}]\n`), new RegExp(`data-callout="${kind}"`), alias)
  }
  for (const k of CALLOUT_KINDS) assert.ok(calloutIcons[k], `${k} has an icon`)
  // Tasks in a callout still tick their line.
  assert.match(render("> [!todo]\n> - [ ] Weed\n"), new RegExp(`data-task-line="1" data-task-key="${taskKey}"`))
})

test("highlights", () => {
  assert.match(render("Some ==marked *text*== here.\n"), /<mark>marked <em>text<\/em><\/mark>/)
  assert.doesNotMatch(render("a == b == c\n"), /<mark>/)
  assert.doesNotMatch(render("`==x==`\n"), /<mark>/)
})

test("tags", () => {
  assert.match(render("A #tag, #nested/tag and #tag-two.\n"), /<span class="tag">#tag<\/span>, <span class="tag">#nested\/tag<\/span> and <span class="tag">#tag-two<\/span>\./)
  assert.match(render("#start of a line\n"), /<p[^>]*><span class="tag">#start<\/span>/)
  for (const s of ["#123", "a#b", "C# rocks", "# Heading", "[x](#frag)", "https://example.com/#frag", "`#code`"]) {
    assert.doesNotMatch(render(s + "\n"), /class="tag"/, s)
  }
  assert.match(render("## Heading #tag\n"), /<h2[^>]*>Heading <span class="tag">#tag<\/span><\/h2>/)
})

test("block ids", () => {
  const h = render("A paragraph. ^para-1\n\n- item ^li\n- other\n\n| a |\n|---|\n| 1 |\n\n^tbl\n")
  assert.match(h, /<p data-anchor="\^para-1" data-line="0" data-line-end="1">A paragraph\.<\/p>/)
  assert.match(h, /<li data-anchor="\^li" data-line="2" data-line-end="3">item<\/li>/)
  assert.match(h, /<table data-anchor="\^tbl" data-line="5" data-line-end="8">/)
  assert.doesNotMatch(h, /\^tbl<\/p>/)
})

test("comments", () => {
  assert.equal(stripComments("a %%hidden%% b"), "a  b")
  assert.equal(stripComments("one\n%%\nhidden\nlines\n%%\ntwo"), "one\n\n\n\n\ntwo")
  assert.equal(stripComments("keep `%%code%%` this"), "keep `%%code%%` this")
  assert.equal(stripComments("```\n%% in a fence %%\n```\n%%x%%"), "```\n%% in a fence %%\n```\n")
  assert.equal(stripComments("no comments"), "no comments")
  assert.equal(stripComments("open %% to the end\nstill"), "open \n")
  const src = "a\n%%\nb\n%%\nc\n"
  assert.equal(stripComments(src).split("\n").length, src.split("\n").length)
})

test("noteSection", () => {
  const text = "# T\n\n## A\n\nin a\n\n### A1\n\nin a1\n\n## B\n\nin b\n"
  assert.equal(noteSection(md, text, "A"), "## A\n\nin a\n\n### A1\n\nin a1\n")
  assert.equal(noteSection(md, text, "a1"), "### A1\n\nin a1\n")
  assert.equal(noteSection(md, text, "B"), "## B\n\nin b\n")
  assert.equal(noteSection(md, text, "Z"), null)
  assert.equal(noteSection(md, "- a\n  - b ^x\n- c\n", "^x"), "- b ^x")
  assert.equal(noteSection(md, "p\n\n^none\n", "^gone"), null)
})
