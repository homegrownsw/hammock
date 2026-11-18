package com.homegrownsw.hammock.agentview

import androidx.compose.runtime.Immutable
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.LinkAnnotation
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.TextLinkStyles
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextDecoration
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.sp

/** A block of Markdown, for the agent view. */
sealed class MdBlock {
    data class Heading(val level: Int, val text: String) : MdBlock()
    data class Paragraph(val text: String) : MdBlock()
    data class Code(val language: String, val text: String) : MdBlock()
    data class ListBlock(val items: List<MdListItem>) : MdBlock()
    data class Quote(val text: String) : MdBlock()
    data class Table(val header: List<String>, val rows: List<List<String>>) : MdBlock()
    data object Rule : MdBlock()
}

/** A list item: marker "•", "1." or "" for a task box (then [checked] says which). */
data class MdListItem(val marker: String, val checked: Boolean?, val indent: Int, val text: String)

/** The colours and size Markdown is drawn with (from the theme). */
@Immutable
data class MdStyle(
    val body: TextUnit = 16.sp,
    val secondary: Color = Color.Gray,
    val link: Color = Color(0xFF3A7BD5),
    val codeBackground: Color = Color(0x1F808080),
    val string: Color = Color(0xFFC4543D),
    val number: Color = Color(0xFF1C85AD),
    val keyword: Color = Color(0xFF9E40B3),
)

/**
 * The agent view's Markdown, hand-rolled as the iPhone's is (AgentMarkdown.swift):
 * block structure here, inline formatting (emphasis, code spans, links, bare
 * paths) as an AnnotatedString.
 */
object Markdown {
    fun parse(src: String): List<MdBlock> {
        val out = ArrayList<MdBlock>()
        val para = ArrayList<String>()
        val items = ArrayList<MdListItem>()
        val quote = ArrayList<String>()
        val lines = src.replace("\r\n", "\n").split('\n')
        fun flushPara() { if (para.isNotEmpty()) { out.add(MdBlock.Paragraph(para.joinToString("\n"))); para.clear() } }
        fun flushList() { if (items.isNotEmpty()) { out.add(MdBlock.ListBlock(items.toList())); items.clear() } }
        fun flushQuote() { if (quote.isNotEmpty()) { out.add(MdBlock.Quote(quote.joinToString("\n"))); quote.clear() } }
        fun flushAll() { flushPara(); flushList(); flushQuote() }

        var i = 0
        while (i < lines.size) {
            val line = lines[i]
            val t = line.trim()
            val fence = openingFence(t)
            if (fence != null) {
                flushAll()
                val lang = t.substring(fence.length).trim()
                val code = ArrayList<String>()
                i++
                while (i < lines.size && !closes(fence, lines[i])) { code.add(lines[i]); i++ }
                out.add(MdBlock.Code(lang, code.joinToString("\n")))
                i++
                continue
            }
            if (t.isEmpty()) { flushAll(); i++; continue }
            val h = heading(t)
            if (h != null) { flushAll(); out.add(h); i++; continue }
            if (t == "---" || t == "***" || t == "___") { flushAll(); out.add(MdBlock.Rule); i++; continue }
            // Tables: a header row, a |---| row, then rows.
            if (t.startsWith("|") && i + 1 < lines.size && isTableRule(lines[i + 1])) {
                flushAll()
                val header = cells(t)
                val rows = ArrayList<List<String>>()
                i += 2
                while (i < lines.size && lines[i].trim().startsWith("|")) { rows.add(cells(lines[i].trim())); i++ }
                out.add(MdBlock.Table(header, rows))
                continue
            }
            if (t.startsWith(">")) {
                flushPara(); flushList()
                quote.add(t.substring(1).trim())
                i++
                continue
            }
            val item = listItem(line)
            if (item != null) {
                flushPara(); flushQuote()
                items.add(item)
                i++
                continue
            }
            // A continuation of a list item.
            if (items.isNotEmpty() && line.startsWith("  ")) {
                items[items.size - 1] = items.last().copy(text = items.last().text + " " + t)
                i++
                continue
            }
            flushList(); flushQuote()
            para.add(line)
            i++
        }
        flushAll()
        return out
    }

    /** A fence opening a code block: three or more backticks or tildes (a backtick fence can't have one in its language). */
    private fun openingFence(t: String): String? {
        val c = t.firstOrNull() ?: return null
        if (c != '`' && c != '~') return null
        val fence = t.takeWhile { it == c }
        if (fence.length < 3) return null
        if (c == '`' && t.substring(fence.length).contains('`')) return null
        return fence
    }

    /** A line closes the block [fence] opened: the same character, at least as many, nothing after. */
    private fun closes(fence: String, line: String): Boolean {
        val t = line.trim()
        val c = fence.first()
        val run = t.takeWhile { it == c }
        return run.length >= fence.length && run.length == t.length
    }

    private fun heading(t: String): MdBlock.Heading? {
        val level = t.takeWhile { it == '#' }.length
        if (level !in 1..6 || t.getOrNull(level) != ' ') return null
        return MdBlock.Heading(level, t.substring(level + 1))
    }

    private fun isTableRule(l: String): Boolean {
        val t = l.trim()
        return t.startsWith("|") && t.all { it in "|-: " } && t.contains('-')
    }

    private fun cells(row: String): List<String> {
        var r = row
        if (r.startsWith("|")) r = r.substring(1)
        if (r.endsWith("|")) r = r.dropLast(1)
        return r.split('|').map { it.trim() }
    }

    private val bullet = Regex("^(\\s*)([-*+]|\\d+[.)])\\s+(\\[[ xX~]\\]\\s+)?(.*)$")

    private fun listItem(line: String): MdListItem? {
        val m = bullet.find(line) ?: return null
        val indent = m.groupValues[1].length / 2
        var marker = m.groupValues[2]
        if (marker in listOf("-", "*", "+")) marker = "•"
        var checked: Boolean? = null
        val box = m.groups[3]?.value
        if (box != null) {
            checked = box.contains('x') || box.contains('X')
            marker = ""
        }
        return MdListItem(marker, checked, indent, m.groupValues[4])
    }

    // MARK: Inline

    private val barePath = Regex("(?<![\\w`(\\[/])(/(?:Users|tmp|private|var|opt|Volumes|home|root|etc|srv|mnt)/[^\\s`)\\]\"'<>]+[^\\s`)\\]\"'<>.,:;])")

    /** Inline Markdown, with bare absolute paths made file links. */
    fun inline(s: String, style: MdStyle = MdStyle(), links: Boolean = true): AnnotatedString =
        RenderCache.get("in|$links|${style.hashCode()}|$s") {
            buildAnnotatedString { InlineParser(s, style, links, this).run() }
        }

    /** Text with its links' URLs marked, where bare paths become file:// links (not inside code spans). */
    internal fun linkPaths(s: String): String {
        if (!s.contains('/')) return s
        val sb = StringBuilder()
        var last = 0
        for (m in barePath.findAll(s)) {
            sb.append(s, last, m.range.first)
            val inCode = s.substring(0, m.range.first).count { it == '`' } % 2 == 1
            val path = m.groupValues[1]
            if (inCode) sb.append(path) else sb.append("[").append(path).append("](file://").append(path.replace(" ", "%20")).append(")")
            last = m.range.last + 1
        }
        sb.append(s, last, s.length)
        return sb.toString()
    }

    /**
     * Headings, paragraphs, lists and quotes in a row as one AnnotatedString,
     * so a selection runs across them, as the iPhone's prose run is.
     */
    fun prose(blocks: List<MdBlock>, style: MdStyle): AnnotatedString = buildAnnotatedString {
        for ((n, b) in blocks.withIndex()) {
            if (n > 0) {
                // A short blank line between blocks.
                append("\n")
                pushStyle(SpanStyle(fontSize = style.body * 0.45f))
                append("\n")
                pop()
            }
            when (b) {
                is MdBlock.Heading -> {
                    val scale = listOf(1.45f, 1.25f, 1.1f, 1f, 1f, 1f)[minOf(b.level - 1, 5)]
                    pushStyle(SpanStyle(fontSize = style.body * scale, fontWeight = FontWeight.SemiBold))
                    append(inline(b.text, style))
                    pop()
                }
                is MdBlock.Paragraph -> append(inline(b.text, style))
                is MdBlock.ListBlock -> b.items.forEachIndexed { i, it ->
                    if (i > 0) append("\n")
                    append("    ".repeat(it.indent))
                    if (it.checked != null) {
                        withStyle(SpanStyle(color = if (it.checked) Color.Unspecified else style.secondary)) { append(if (it.checked) "☑ " else "☐ ") }
                    } else {
                        withStyle(SpanStyle(color = style.secondary)) { append(it.marker + " ") }
                    }
                    append(inline(it.text, style))
                }
                is MdBlock.Quote -> b.text.split('\n').forEachIndexed { i, line ->
                    if (i > 0) append("\n")
                    withStyle(SpanStyle(color = style.secondary.copy(alpha = 0.5f))) { append("▍ ") }
                    withStyle(SpanStyle(color = style.secondary)) { append(inline(line, style)) }
                }
                else -> {}
            }
        }
    }

    private inline fun AnnotatedString.Builder.withStyle(s: SpanStyle, block: AnnotatedString.Builder.() -> Unit) {
        pushStyle(s); block(); pop()
    }

    /** A message as runs: prose (headings, paragraphs, lists, quotes together) and blocks drawn on their own. */
    sealed class Run {
        data class Prose(val text: AnnotatedString) : Run()
        data class Block(val block: MdBlock) : Run()
    }

    fun runs(text: String, style: MdStyle): List<Run> = RenderCache.get("md|${style.hashCode()}|$text") {
        val out = ArrayList<Run>()
        val run = ArrayList<MdBlock>()
        fun flush() { if (run.isNotEmpty()) { out.add(Run.Prose(prose(run.toList(), style))); run.clear() } }
        for (b in parse(text)) {
            when (b) {
                is MdBlock.Heading, is MdBlock.Paragraph, is MdBlock.ListBlock, is MdBlock.Quote -> run.add(b)
                else -> { flush(); out.add(Run.Block(b)) }
            }
        }
        flush()
        out
    }
}

/**
 * Inline Markdown: **bold**, __bold__, *italic*, _italic_, ~~struck~~, `code`,
 * [text](url), <https://…> and bare http(s) URLs, with backslash escapes.
 * Unclosed markers are left as they were typed.
 */
private class InlineParser(src: String, val style: MdStyle, val links: Boolean, val out: AnnotatedString.Builder) {
    val s = Markdown.linkPaths(src)
    /** Inside a link's text: a URL there isn't a link of its own. */
    var inLink = false

    fun run() = parse(0, s.length)

    private fun parse(from: Int, to: Int) {
        var i = from
        val text = StringBuilder()
        fun flush() { if (text.isNotEmpty()) { out.append(text.toString()); text.clear() } }
        while (i < to) {
            val c = s[i]
            when {
                c == '\\' && i + 1 < to && !s[i + 1].isLetterOrDigit() && !s[i + 1].isWhitespace() -> { text.append(s[i + 1]); i += 2 }
                c == '`' -> {
                    val n = run(i, '`', to)
                    val close = findRun(i + n, '`', n, to)
                    if (close < 0) { text.append(s, i, i + n); i += n; continue }
                    flush()
                    var code = s.substring(i + n, close).replace('\n', ' ')
                    if (code.length > 2 && code.startsWith(" ") && code.endsWith(" ")) code = code.substring(1, code.length - 1)
                    out.pushStyle(SpanStyle(fontFamily = FontFamily.Monospace, background = style.codeBackground, fontSize = style.body * 0.92f))
                    out.append(code)
                    out.pop()
                    i = close + n
                }
                (c == '*' || c == '_') && i + 1 < to && s[i + 1] == c && opens(i, 2, to) -> {
                    val close = findDelim(i + 2, "$c$c", to)
                    if (close < 0) { text.append(c).append(c); i += 2; continue }
                    flush()
                    out.pushStyle(SpanStyle(fontWeight = FontWeight.SemiBold))
                    parse(i + 2, close)
                    out.pop()
                    i = close + 2
                }
                c == '~' && i + 1 < to && s[i + 1] == '~' && opens(i, 2, to) -> {
                    val close = findDelim(i + 2, "~~", to)
                    if (close < 0) { text.append("~~"); i += 2; continue }
                    flush()
                    out.pushStyle(SpanStyle(textDecoration = TextDecoration.LineThrough))
                    parse(i + 2, close)
                    out.pop()
                    i = close + 2
                }
                (c == '*' || c == '_') && opens(i, 1, to) && (c == '*' || i == 0 || !s[i - 1].isLetterOrDigit()) -> {
                    val close = findDelim(i + 1, c.toString(), to)
                    if (close < 0 || (c == '_' && close + 1 < to && s[close + 1].isLetterOrDigit())) { text.append(c); i++; continue }
                    flush()
                    out.pushStyle(SpanStyle(fontStyle = FontStyle.Italic))
                    parse(i + 1, close)
                    out.pop()
                    i = close + 1
                }
                c == '[' -> {
                    val end = matchingBracket(i, to)
                    if (end < 0 || end + 1 >= to || s[end + 1] != '(') { text.append(c); i++; continue }
                    val close = s.indexOf(')', end + 2).takeIf { it in 0 until to }
                    if (close == null) { text.append(c); i++; continue }
                    val url = s.substring(end + 2, close).trim().substringBefore(' ')
                    flush()
                    link(url) { parse(i + 1, end) }
                    i = close + 1
                }
                c == '<' && (s.startsWith("<http://", i) || s.startsWith("<https://", i) || s.startsWith("<mailto:", i)) -> {
                    val close = s.indexOf('>', i).takeIf { it in 0 until to }
                    if (close == null) { text.append(c); i++; continue }
                    flush()
                    val url = s.substring(i + 1, close)
                    link(url) { out.append(url) }
                    i = close + 1
                }
                c == 'h' && !inLink && (i == 0 || !s[i - 1].isLetterOrDigit()) && (s.startsWith("https://", i) || s.startsWith("http://", i)) -> {
                    var end = i
                    while (end < to && !s[end].isWhitespace() && s[end] != '<' && s[end] != '>' && s[end] != '`') end++
                    // Punctuation after a URL ends the sentence, not the URL.
                    while (end > i && s[end - 1] in ".,:;!?'\")]") {
                        if (s[end - 1] == ')' && s.substring(i, end).count { it == '(' } >= s.substring(i, end).count { it == ')' }) break
                        end--
                    }
                    flush()
                    val url = s.substring(i, end)
                    link(url) { out.append(url) }
                    i = end
                }
                else -> { text.append(c); i++ }
            }
        }
        flush()
    }

    private fun link(url: String, content: () -> Unit) {
        if (!links || url.isEmpty()) { content(); return }
        out.pushLink(LinkAnnotation.Url(url, TextLinkStyles(SpanStyle(color = style.link))))
        inLink = true
        content()
        inLink = false
        out.pop()
    }

    /** How many of c start at i. */
    private fun run(i: Int, c: Char, to: Int): Int { var n = 0; while (i + n < to && s[i + n] == c) n++; return n }

    /** A run of exactly n of c from `from`, or -1. */
    private fun findRun(from: Int, c: Char, n: Int, to: Int): Int {
        var i = from
        while (i < to) {
            if (s[i] == c) {
                val k = run(i, c, to)
                if (k == n) return i
                i += k
            } else i++
        }
        return -1
    }

    /** An opening delimiter of length n at i: followed by something other than whitespace. */
    private fun opens(i: Int, n: Int, to: Int) = i + n < to && !s[i + n].isWhitespace()

    /** A closing delimiter from `from`: preceded by something other than whitespace, skipping code spans. */
    private fun findDelim(from: Int, d: String, to: Int): Int {
        var i = from
        while (i < to) {
            val c = s[i]
            if (c == '\\') { i += 2; continue }
            if (c == '`') {
                val n = run(i, '`', to)
                val close = findRun(i + n, '`', n, to)
                i = if (close < 0) i + n else close + n
                continue
            }
            if (s.startsWith(d, i) && i + d.length <= to && i > from && !s[i - 1].isWhitespace()) {
                // "**" closing a single "*": not it (it's the start of bold inside).
                if (d.length == 1 && i + 1 < to && s[i + 1] == d[0]) { i += 2; continue }
                return i
            }
            i++
        }
        return -1
    }

    private fun matchingBracket(i: Int, to: Int): Int {
        var depth = 0
        var k = i
        while (k < to) {
            when (s[k]) {
                '\\' -> k++
                '[' -> depth++
                ']' -> { depth--; if (depth == 0) return k }
            }
            k++
        }
        return -1
    }
}

/** A tiny syntax highlighter: comments, strings, numbers and common keywords. */
object CodeHighlight {
    private val keywords = setOf(
        "func", "let", "var", "if", "else", "for", "while", "return", "import", "package", "type", "struct", "class", "enum", "case", "switch",
        "const", "def", "fn", "pub", "use", "go", "defer", "interface", "public", "private", "static", "final", "async", "await", "try", "catch",
        "throw", "throws", "guard", "in", "of", "new", "this", "self", "nil", "null", "true", "false", "none", "None", "True", "False", "export",
        "from", "default", "break", "continue", "extension", "protocol", "range", "map", "select", "chan", "echo", "then", "fi", "do", "done",
        "fun", "val", "object", "when", "is", "override",
    )
    private val token = Regex(
        "(//[^\\n]*|#[^\\n]*|--[^\\n]*)|(\"(?:[^\"\\\\\\n]|\\\\.)*\"|'(?:[^'\\\\\\n]|\\\\.)*'|`[^`]*`)|\\b(\\d[\\d_.xXa-fA-F]*)\\b|\\b([A-Za-z_][A-Za-z0-9_]*)\\b",
    )
    private val hashLanguages = setOf("sh", "bash", "zsh", "shell", "python", "py", "ruby", "rb", "yaml", "yml", "toml", "make", "makefile", "just", "conf", "dockerfile")

    fun highlighted(code: String, language: String, style: MdStyle): AnnotatedString =
        RenderCache.get("hl|$language|${style.hashCode()}|$code") { build(code, language, style) }

    private fun build(code: String, language: String, style: MdStyle): AnnotatedString {
        val lang = language.lowercase()
        if (lang in setOf("", "text", "txt", "plain", "output", "log") || code.length >= 40_000) return AnnotatedString(code)
        val hash = lang in hashLanguages
        val b = AnnotatedString.Builder(code)
        for (m in token.findAll(code)) {
            val r = m.range
            val color = when {
                m.groups[1] != null -> {
                    val t = m.groups[1]!!.value
                    if (t.startsWith("#") && !hash) continue
                    if (t.startsWith("--") && lang !in setOf("sql", "lua", "haskell", "hs")) continue
                    style.secondary
                }
                m.groups[2] != null -> style.string
                m.groups[3] != null -> style.number
                m.groups[4] != null && m.groups[4]!!.value in keywords -> style.keyword
                else -> continue
            }
            b.addStyle(SpanStyle(color = color), r.first, r.last + 1)
        }
        return b.toAnnotatedString()
    }
}

/** Rendered text (parsed Markdown, highlighted code) by what it was made from, so a row coming back into view skips the work. */
object RenderCache {
    private val map = object : LinkedHashMap<String, Any>(256, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, Any>?) = size > 600
    }

    @Suppress("UNCHECKED_CAST")
    fun <T : Any> get(key: String, make: () -> T): T {
        synchronized(map) { map[key]?.let { return it as T } }
        val v = make()
        synchronized(map) { map[key] = v }
        return v
    }
}
