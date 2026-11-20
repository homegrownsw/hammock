package com.homegrownsw.hammock.agentview

import hammock.v1.Hammock.AgentShell
import hammock.v1.Hammock.AgentTask
import java.net.URI
import java.text.DateFormat
import java.util.Calendar
import java.util.Date

// The words the agent view shows, kept apart from the views so they can be
// tested on the JVM. Each follows its iPhone counterpart in AgentViews.swift.

/** A turn's end: "Worked for 30m 31s · done 9:47 AM", with the day too when it wasn't today. */
object TurnWords {
    fun words(durationMs: Long, doneUnixMs: Long, nowMs: Long = System.currentTimeMillis()): String {
        var s = "Worked for " + duration(durationMs)
        if (doneUnixMs > 0) {
            val done = Date(doneUnixMs)
            val a = Calendar.getInstance().apply { timeInMillis = doneUnixMs }
            val b = Calendar.getInstance().apply { timeInMillis = nowMs }
            val sameDay = a.get(Calendar.YEAR) == b.get(Calendar.YEAR) && a.get(Calendar.DAY_OF_YEAR) == b.get(Calendar.DAY_OF_YEAR)
            val f = if (sameDay) DateFormat.getTimeInstance(DateFormat.SHORT) else DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT)
            s += " · done " + f.format(done)
        }
        return s
    }

    /** "45s", "30m 31s", "2h 5m". */
    fun duration(ms: Long): String {
        val s = maxOf(ms / 1000, 0)
        return when {
            s < 60 -> "${s}s"
            s < 3600 -> "${s / 60}m ${s % 60}s"
            else -> "${s / 3600}h ${(s % 3600) / 60}m"
        }
    }

    /** A running tool call's time, as the terminal shows it: "12s", "1m 05s", "1h 02m". */
    fun running(seconds: Long): String {
        val s = maxOf(seconds, 0)
        return when {
            s < 60 -> "${s}s"
            s < 3600 -> "%dm %02ds".format(s / 60, s % 60)
            else -> "%dh %02dm".format(s / 3600, (s % 3600) / 60)
        }
    }
}

/** The model and effort names the status bar shows, and Claude's choices. */
object ModelNames {
    /** Claude's /model aliases. */
    val choices = listOf(
        "Opus" to "opus", "Opus (1M context)" to "opus[1m]",
        "Sonnet" to "sonnet", "Sonnet (1M context)" to "sonnet[1m]",
        "Haiku" to "haiku", "Default" to "default",
    )

    /** Claude's /effort levels, lowest first. */
    val efforts = listOf("Low" to "low", "Medium" to "medium", "High" to "high", "Extra high" to "xhigh", "Max" to "max")

    fun effortName(level: String): String =
        efforts.firstOrNull { it.second == level }?.first ?: level.replaceFirstChar { it.uppercase() }

    /** "claude-opus-5-5" as "Opus 5.5" (a date suffix dropped); "gpt-6-luna" as "GPT-6 Luna". */
    fun displayName(id: String): String {
        if (id.contains(' ')) return id // already a name
        if (id.startsWith("gpt-")) {
            val parts = id.removePrefix("gpt-").split('-')
            val version = parts.firstOrNull()?.takeIf { it.isNotEmpty() } ?: return id
            return (listOf("GPT-$version") + parts.drop(1).map { cap(it) }).joinToString(" ")
        }
        val rest = id.replace("[1m]", "").removePrefix("claude-")
        val parts = rest.split('-').filter { it.isNotEmpty() && !(it.length == 8 && it.all(Char::isDigit)) }
        val family = parts.firstOrNull { it.first().isLetter() } ?: return id
        val version = parts.filter { p -> p.all(Char::isDigit) }.joinToString(".")
        return listOf(cap(family)).plus(if (version.isEmpty()) emptyList() else listOf(version)).joinToString(" ")
    }

    private fun cap(s: String) = s.lowercase().replaceFirstChar { it.uppercase() }

    /** What the context gauge's menu offers to make room: Compact, and Clear (Codex's New Chat), when the agent has them. */
    fun contextActions(harness: String, commands: List<String>): List<Pair<String, String>> {
        val has = commands.toSet()
        val out = ArrayList<Pair<String, String>>()
        if (harness == "claude" || harness == "codex" || "/compact" in has) out.add("Compact" to "/compact")
        if (harness == "claude" || (harness != "codex" && "/clear" in has)) out.add("Clear" to "/clear")
        else if (harness == "codex" || "/new" in has) out.add("New Chat" to "/new")
        return out
    }

    /** "~120k tokens left"; under a thousand, the number itself (not "~0k"). */
    fun tokensLeft(n: Long): String {
        val left = maxOf(0, n)
        return if (left < 1000) "$left tokens left" else "~${tokens(left)} tokens left"
    }

    /** "120k", "1M", "1.5M". */
    fun tokens(n: Long): String =
        if (n >= 1_000_000) ("%.1fM".format(n / 1_000_000.0)).replace(".0M", "M") else "${n / 1000}k"
}

/**
 * The "/command" being typed at the cursor, anywhere in a message: from a "/"
 * at the start or after whitespace, up to the cursor, with only whitespace or
 * the end after it.
 */
object SlashToken {
    /** With [marker] '@', the "@file" being typed there instead. */
    fun at(cursor: Int, text: String, marker: Char = '/'): IntRange? {
        if (cursor < 0 || cursor > text.length) return null
        if (cursor < text.length && !text[cursor].isWhitespace()) return null
        var start = cursor
        while (start > 0 && !text[start - 1].isWhitespace()) start--
        if (start >= cursor || text[start] != marker) return null
        return start until cursor
    }

    /** Nothing but whitespace before it: agents' built-in commands only run there. */
    fun startsMessage(token: IntRange, text: String) = text.substring(0, token.first).isBlank()

    /**
     * How well a command matches what's typed after its "/" (lower is better;
     * null: not at all): it starts with it, then a part of its name does (after
     * ':', '-' or '_'), then it has it anywhere, then its letters in order.
     */
    fun rank(query: String, command: String): Int? {
        val q = query.lowercase().trimStart('/')
        val name = command.lowercase().trimStart('/')
        if (q.isEmpty() || name.startsWith(q)) return 0
        if (name.split(':', '-', '_').drop(1).any { it.startsWith(q) }) return 1
        if (name.contains(q)) return 2
        var from = 0
        for (ch in q) {
            val i = name.indexOf(ch, from)
            if (i < 0) return null
            from = i + 1
        }
        return 3
    }

    /** Up to eight commands that match the token, best first, leaving out start-only ones when it isn't at the start. */
    fun suggestions(token: IntRange, text: String, commands: List<String>, startOnly: Set<String>): List<String> {
        val q = text.substring(token.first, token.last + 1)
        val skip = if (startsMessage(token, text)) emptySet() else startOnly
        return commands.withIndex().mapNotNull { (i, c) -> if (c in skip) null else rank(q, c)?.let { Triple(it, i, c) } }
            .sortedWith(compareBy({ it.first }, { it.second })).take(8).map { it.third }
    }

    /** Whether Return takes the highlighted suggestion: only once it's been moved with ↑ or ↓. */
    fun returnTakes(suggesting: Boolean, moved: Boolean) = suggesting && moved

    /** A file as an "@" mention: quoted when it has a space in it. */
    fun mention(path: String) = if (path.any { it.isWhitespace() }) "@\"$path\"" else "@$path"

    /** A file the daemon listed (relative to [folder]) as the agent would name it: relative to its [cwd] when in there, else in full. */
    fun mentionPath(path: String, folder: String, cwd: String): String {
        val full = if (folder.isEmpty() || path.startsWith("/")) path else folder.trimEnd('/') + "/" + path
        val base = if (cwd.endsWith("/")) cwd else "$cwd/"
        return if (cwd.isNotEmpty() && full.startsWith(base)) full.removePrefix(base) else full
    }

    /** The text with the token replaced by the command and a space, and where the cursor goes (after the space). */
    fun complete(token: IntRange, command: String, text: String): Pair<String, Int> {
        val after = text.substring(token.last + 1)
        val spaced = after.firstOrNull()?.let { it == ' ' || it == '\t' } ?: false
        val t = text.substring(0, token.first) + (if (spaced) command else "$command ") + after
        return t to (token.first + command.length + 1)
    }

    private val commandWord = Regex("(?<![^\\s])/[^\\s\uFFFC]+")

    /** The ranges of commands it knows in the text, to colour as Claude Code does. */
    fun highlights(text: String, commands: Set<String>, startOnly: Set<String>): List<IntRange> {
        if (!text.contains('/') || commands.isEmpty()) return emptyList()
        return commandWord.findAll(text).filter { m ->
            val w = m.value
            (w in commands || w.lowercase() in commands) && (w !in startOnly || text.substring(0, m.range.first).isBlank())
        }.map { it.range }.toList()
    }
}

/** Dictation's rules: phrases joined, and "slash review PR" made the command it means. */
object DictationText {
    /** One phrase after another: a space between, unless one's there or the next starts with punctuation. */
    fun join(a: String, b: String): String {
        if (a.isEmpty()) return b.trimStart()
        if (b.isEmpty()) return a
        if (a.last().isWhitespace() || b.first().isWhitespace() || isPunctuation(b.first())) return a + b
        return "$a $b"
    }

    private fun isPunctuation(c: Char) = Character.getType(c).let {
        it == Character.OTHER_PUNCTUATION.toInt() || it == Character.END_PUNCTUATION.toInt() || it == Character.DASH_PUNCTUATION.toInt() ||
            it == Character.FINAL_QUOTE_PUNCTUATION.toInt() || it == Character.CONNECTOR_PUNCTUATION.toInt()
    }

    /** The words for the recogniser to listen for: commands as you'd say them ("/review-pr" is "review pr"). */
    fun vocabulary(commands: List<String>): List<String> {
        val seen = HashSet<String>()
        val out = ArrayList<String>()
        for (c in commands) {
            var name = c.removePrefix("/")
            name.lastIndexOf(':').takeIf { it >= 0 }?.let { name = name.substring(it + 1) }
            val said = name.replace('-', ' ').replace('_', ' ')
            if (said.isNotEmpty() && seen.add(said.lowercase())) out.add(said)
            if (out.size == 100) break
        }
        return out
    }

    private val slashPattern = Regex(
        "(\\bslash\\s+|(?<![/\\w])/|(?<=\\w)/)([A-Za-z0-9][\\w:-]*(?:\\s+[A-Za-z0-9][\\w:-]*){0,3})",
        RegexOption.IGNORE_CASE,
    )
    private val wordPattern = Regex("[A-Za-z0-9][\\w:-]*")

    private fun key(s: String) = s.lowercase().filter { it.isLetterOrDigit() }

    /**
     * "slash review PR" or "/review PR" (recognisers write "slash" as a slash,
     * often joined to the word before) becomes "/review-pr" when that's one of
     * [commands], matched ignoring case, spaces, hyphens and underscores.
     * Anything else is left as heard.
     */
    fun mapSlashCommands(text: String, commands: List<String>): String {
        if (commands.isEmpty() || (!text.contains('/') && !text.contains("slash", ignoreCase = true))) return text
        val byKey = HashMap<String, String>()
        for (c in commands) {
            if (!c.startsWith("/")) continue
            val name = c.substring(1)
            byKey.putIfAbsent(key(name), c)
            name.lastIndexOf(':').takeIf { it >= 0 }?.let { byKey.putIfAbsent(key(name.substring(it + 1)), c) }
        }
        val out = StringBuilder()
        var at = 0
        var searchFrom = 0
        var changed = false
        while (searchFrom < text.length) {
            val m = slashPattern.find(text, searchFrom) ?: break
            val marker = m.groups[1]!!.range
            val wordsRange = m.groups[2]!!.range
            val words = ArrayList<IntRange>()
            for (w in wordPattern.findAll(text.substring(0, wordsRange.last + 1), wordsRange.first)) {
                if (w.value.equals("slash", ignoreCase = true)) break
                words.add(w.range)
            }
            var hit: Pair<String, Int>? = null
            for (n in minOf(4, words.size) downTo 1) {
                val spoken = words.subList(0, n).joinToString("") { text.substring(it) }
                byKey[key(spoken)]?.let { hit = it to (words[n - 1].last + 1) }
                if (hit != null) break
            }
            val h = hit
            if (h == null) {
                searchFrom = marker.last + 1
                continue
            }
            out.append(text, at, marker.first)
            if (out.isNotEmpty() && !out.last().isWhitespace()) out.append(' ')
            out.append(h.first)
            at = h.second
            searchFrom = h.second
            changed = true
        }
        if (!changed) return text
        return out.append(text.substring(at)).toString()
    }
}

/** Background shells' output, kept as the iPhone keeps it. */
object ShellOutput {
    /** Most of a shell's output kept. */
    const val KEPT = 256 shl 10

    /** The output so far, with what's new: a line redrawn (a carriage return, as progress bars do) keeps only its last. */
    fun append(old: String, new: String): String {
        var s = old + new
        if (new.contains('\r') || old.endsWith("\r")) {
            s = s.replace("\r\n", "\n")
            // A line ending in one (still being redrawn) keeps it, so what
            // comes next replaces the line rather than joining it.
            val redrawing = s.endsWith("\r")
            s = s.split('\n').joinToString("\n") { line -> line.split('\r').lastOrNull { it.isNotEmpty() } ?: "" }
            if (redrawing) s += "\r"
        }
        val bytes = s.toByteArray()
        if (bytes.size > KEPT) {
            s = String(bytes, bytes.size - KEPT, KEPT)
            val nl = s.indexOf('\n')
            if (nl >= 0) s = s.substring(nl + 1)
        }
        return s
    }
}

/** What a link in the conversation may do without asking. */
object LinkPolicy {
    sealed class Opening {
        /** http(s): the browser. */
        data object Web : Opening()
        /** mailto: straight away. */
        data object Direct : Opening()
        /** A file on the agent's computer, by its path. */
        data class File(val path: String) : Opening()
        /** Any other scheme (it could open any app), or a file on another host: ask first. */
        data object Ask : Opening()
    }

    fun opening(url: String): Opening {
        val u = runCatching { URI(url) }.getOrNull() ?: return Opening.Ask
        return when (u.scheme?.lowercase() ?: "") {
            "http", "https" -> Opening.Web
            "mailto" -> Opening.Direct
            "file" -> {
                // file://server/share… would reach out to another machine.
                val host = u.host ?: ""
                val path = u.path ?: ""
                if ((host.isEmpty() || host.equals("localhost", true)) && path.isNotEmpty()) Opening.File(path) else Opening.Ask
            }
            else -> Opening.Ask
        }
    }
}

/**
 * The composer's message history, as a shell's (the iPhone's ComposerHistory):
 * ↑ brings back what you sent before, ↓ goes the other way, back to what you
 * were typing.
 */
class ComposerHistory<D> {
    var entries: List<String> = emptyList(); private set
    /** Which entry is shown (null: what you were typing). */
    var index: Int? = null; private set
    var draft: D? = null; private set
    val browsing get() = index != null

    /** One further back; [all] is read as stepping starts. */
    fun up(current: D, all: () -> List<String>): String? {
        val i = index
        if (i != null) {
            if (i == 0) return null
            index = i - 1
            return entries[i - 1]
        }
        val e = all()
        if (e.isEmpty()) return null
        entries = e
        draft = current
        index = e.size - 1
        return e.last()
    }

    /** One forward: the next entry (first), or past the newest, what you were typing (second). */
    fun down(): Pair<String?, D?>? {
        val i = index ?: return null
        if (i + 1 < entries.size) {
            index = i + 1
            return entries[i + 1] to null
        }
        val d = draft
        reset()
        return null to d
    }

    fun reset() { index = null; draft = null; entries = emptyList() }

    /** The text now in the box: once it's not the entry brought back (it's been edited), stepping ends. */
    fun edited(text: String) {
        val i = index ?: return
        if (entries[i] != text) reset()
    }

    companion object {
        const val CAP = 50
        /** Panes whose history is kept: those written to last. */
        const val PANES = 40

        /** The panes kept once [pane] is written to ([order] oldest first), and those to drop. */
        fun panesKept(order: List<String>, pane: String, cap: Int = PANES): Pair<List<String>, List<String>> {
            val all = order.filter { it != pane } + pane
            val keep = all.takeLast(cap)
            return keep to all.dropLast(keep.size)
        }
        /** Longer messages (a pasted log) aren't kept. */
        const val LONGEST = 4000

        /**
         * Whether ↑ ([up]) or ↓ steps through the history rather than moving the
         * cursor: ↑ with only the first line before the cursor, in an empty box,
         * at its start, or while stepping; ↓ while stepping, with only the last
         * line after the cursor. Never with text selected ([cursor] null).
         */
        fun steps(up: Boolean, text: String, cursor: Int?, browsing: Boolean): Boolean {
            val at = (cursor ?: return false).coerceIn(0, text.length)
            if (up) {
                if ('\n' in text.substring(0, at)) return false
                return text.isEmpty() || browsing || at == 0
            }
            return browsing && '\n' !in text.substring(at)
        }

        /**
         * The steps: the conversation's messages in order, with those sent from
         * here it doesn't have: older ones (before a /clear) ahead of it, newer
         * ones (not in the transcript yet) after it. Each once, at its latest.
         */
        fun merged(sent: List<String>, conversation: List<String>, cap: Int = CAP): List<String> {
            fun clean(s: List<String>) = s.map { it.trim() }.filter { it.isNotEmpty() && it.length <= LONGEST }
            val conv = clean(conversation)
            val mine = clean(sent)
            val inConv = conv.toSet()
            val last = mine.indexOfLast { it in inConv }
            val older = if (last >= 0) mine.subList(0, last) else emptyList()
            val newer = if (last >= 0) mine.subList(last + 1, mine.size) else mine
            val seen = HashSet<String>()
            val out = ArrayList<String>()
            for (m in (older.filter { it !in inConv } + conv + newer.filter { it !in inConv }).asReversed()) if (seen.add(m)) out.add(m)
            return out.take(cap).asReversed()
        }

        /** [sent] with [text] added last (once). */
        fun recorded(sent: List<String>, text: String): List<String> {
            val t = text.trim()
            if (t.isEmpty() || t.length > LONGEST) return sent
            return (sent.filter { it != t } + t).takeLast(CAP)
        }
    }
}

/** What a run of tool calls did, in words: "Read 4 files · ran 2 commands · edited 1 file" (the iPhone's ToolSummary). */
object ToolSummary {
    private enum class Kind { READ, EDIT, WRITE, RUN, SEARCH, FETCH, TASKS, SKILL, MCP, OTHER }

    private fun kind(tool: String) = when (tool) {
        "Read" -> Kind.READ
        "Edit", "MultiEdit", "NotebookEdit" -> Kind.EDIT
        "Write" -> Kind.WRITE
        "Bash" -> Kind.RUN
        "Grep", "Glob", "WebSearch" -> Kind.SEARCH
        "WebFetch" -> Kind.FETCH
        "TodoWrite", "TaskCreate", "TaskUpdate" -> Kind.TASKS
        "Skill" -> Kind.SKILL
        else -> if (tool.startsWith("mcp__")) Kind.MCP else Kind.OTHER
    }

    private fun words(k: Kind, n: Int, alone: Boolean): String {
        fun c(one: String, many: String) = "$n " + if (n == 1) one else many
        return when (k) {
            Kind.READ -> "read " + c("file", "files")
            Kind.EDIT -> "edited " + c("file", "files")
            Kind.WRITE -> "wrote " + c("file", "files")
            Kind.RUN -> "ran " + c("command", "commands")
            Kind.SEARCH -> if (n == 1) "searched once" else "searched $n times"
            Kind.FETCH -> "fetched " + c("page", "pages")
            Kind.TASKS -> "updated the tasks"
            Kind.SKILL -> "used " + c("skill", "skills")
            Kind.MCP -> "used " + c("MCP tool", "MCP tools")
            Kind.OTHER -> "used " + if (alone) c("tool", "tools") else c("other tool", "other tools")
        }
    }

    /** The line for tools named [tools] with inputs [inputs], at most [parts] kinds (the rest as "2 more"); files counted once each. */
    fun words(tools: List<Pair<String, String>>, parts: Int = 3): String {
        val order = ArrayList<Kind>()
        val counts = HashMap<Kind, Int>()
        val files = HashMap<Kind, HashSet<String>>()
        for ((tool, input) in tools) {
            val k = kind(tool)
            if (k !in counts) order.add(k)
            val byFile = k == Kind.READ || k == Kind.EDIT || k == Kind.WRITE
            if (byFile && input.isNotEmpty()) {
                if (files.getOrPut(k) { HashSet() }.add(input)) counts[k] = (counts[k] ?: 0) + 1 else counts.putIfAbsent(k, 0)
            } else counts[k] = (counts[k] ?: 0) + 1
        }
        val said = order.take(parts).map { words(it, counts[it] ?: 0, order.size == 1) }.toMutableList()
        val rest = order.drop(parts).sumOf { counts[it] ?: 0 }
        if (rest > 0) said.add("$rest more")
        if (said.isEmpty()) return "Used no tools"
        said[0] = said[0].replaceFirstChar { it.uppercase() }
        return said.joinToString(" · ")
    }

    /** "1 failed", or null when none did. */
    fun failed(errors: Int) = if (errors == 0) null else "$errors failed"
}

/**
 * A diff's lines with their numbers in the old and new file, from its "@@ -12 +14 @@"
 * hunk headers (DiffView's gutter; the iPhone's DiffLines). Lines before any have none.
 */
object DiffLines {
    enum class Kind { CONTEXT, ADDED, REMOVED, HUNK, FILE }

    data class Line(val text: String, val kind: Kind, val old: Int? = null, val new: Int? = null)

    fun numbered(diff: String): List<Line> {
        val lines = diff.split('\n').let { if (it.lastOrNull()?.isEmpty() == true) it.dropLast(1) else it }
        var o: Int? = null
        var n: Int? = null
        return lines.map { s ->
            when {
                s.startsWith("@@") -> {
                    val h = hunk(s)
                    o = h?.first
                    n = h?.second
                    // "@@ path": which file follows (a command's edits, Codex's patches).
                    Line(s, if (h != null) Kind.HUNK else Kind.FILE)
                }
                s.startsWith("+") -> Line(s, Kind.ADDED, new = n).also { n = n?.plus(1) }
                s.startsWith("-") -> Line(s, Kind.REMOVED, old = o).also { o = o?.plus(1) }
                else -> Line(s, Kind.CONTEXT, o, n).also { o = o?.plus(1); n = n?.plus(1) }
            }
        }
    }

    /** "@@ -12,3 +14,4 @@ func x()" as 12 to 14. */
    fun hunk(s: String): Pair<Int, Int>? {
        val parts = s.split(' ')
        if (parts.size < 3 || !parts[1].startsWith("-") || !parts[2].startsWith("+")) return null
        val a = parts[1].drop(1).takeWhile { it.isDigit() }.toIntOrNull() ?: return null
        val b = parts[2].drop(1).takeWhile { it.isDigit() }.toIntOrNull() ?: return null
        return a to b
    }

    /** The files a diff's "@@ path" lines name, in order. */
    fun files(lines: List<Line>) = lines.filter { it.kind == Kind.FILE && !it.text.startsWith("@@ and ") }.map { it.text.drop(3) }
}

/** What TalkBack says for a tool call's row and how it went (the iPhone's ToolWords). */
object ToolWords {
    const val SUCCEEDED = "Succeeded"
    const val FAILED = "Failed"

    /** "Running, 12 seconds" (just "Running" without a start). */
    fun running(seconds: Long?): String = if (seconds == null) "Running" else "Running, " + duration(seconds)

    /** "12 seconds", "1 minute, 5 seconds", "2 hours, 3 minutes". */
    fun duration(seconds: Long): String {
        val s = maxOf(seconds, 0)
        fun unit(n: Long, name: String) = "$n $name" + if (n == 1L) "" else "s"
        return when {
            s < 60 -> unit(s, "second")
            s < 3600 -> unit(s / 60, "minute") + if (s % 60 == 0L) "" else ", " + unit(s % 60, "second")
            else -> unit(s / 3600, "hour") + if ((s % 3600) / 60 == 0L) "" else ", " + unit((s % 3600) / 60, "minute")
        }
    }

    fun toolName(tool: String) = if (tool.startsWith("mcp__")) tool.removePrefix("mcp__").replace("__", " ") else tool

    private val saysInput = setOf("Bash", "Read", "Edit", "MultiEdit", "NotebookEdit", "Write", "Grep", "Glob", "WebFetch", "WebSearch")

    /** A tool row as one: "Bash: npm test, failed" (whether it's open is its state, said apart). */
    fun row(tool: String, title: String, input: String, bang: Boolean, pending: Boolean, error: Boolean, added: Int, removed: Int): String {
        val first = input.substringBefore('\n')
        val what = when {
            bang -> "Shell command: $first"
            tool in saysInput && first.isNotEmpty() -> toolName(tool) + ": " + first
            else -> title.replace("`", "").replace("**", "").let { if (it.isEmpty()) toolName(tool) else toolName(tool) + ": " + it }
        }
        val parts = mutableListOf(what)
        if (added > 0 || removed > 0) parts.add("$added added, $removed removed")
        parts.add(if (pending) "running" else if (error) "failed" else "succeeded")
        return parts.joinToString(", ")
    }
}

/** What a hardware keyboard's key does on the prompt card (the iPhone's PromptKey). */
object PromptKeys {
    enum class Key { ESCAPE, LEFT, RIGHT, OTHER }

    sealed class Action {
        data class Pick(val n: Int) : Action()
        data object Previous : Action()
        data object Next : Action()
        data object Escape : Action()
    }

    /**
     * [digit] 1–9 picks that option (when there is one); ← and → move between
     * questions when there are several; Esc is Esc. Nothing with Ctrl, Meta
     * or Alt held.
     */
    /** How long after a card appears its number keys start to count, in ms. */
    const val SETTLE_MS = 500L

    /** A number key now picks an option: not in the moment after the card appeared (a key meant for what it replaced). */
    fun acceptsPick(shownAtMs: Long, nowMs: Long) = nowMs - shownAtMs >= SETTLE_MS

    /** The card takes the keyboard by itself as it appears only when nothing else is being typed in. */
    fun takesKeysOnAppear(typingElsewhere: Boolean) = !typingElsewhere

    fun action(digit: Int, key: Key, modifiers: Boolean, options: Int, multipleQuestions: Boolean): Action? {
        if (modifiers) return null
        return when (key) {
            Key.ESCAPE -> Action.Escape
            Key.LEFT -> if (multipleQuestions) Action.Previous else null
            Key.RIGHT -> if (multipleQuestions) Action.Next else null
            Key.OTHER -> if (digit in 1..9 && digit <= options) Action.Pick(digit) else null
        }
    }
}

/** A pull request's state as words, and an option's "(Recommended)". */
object CardText {
    fun prState(s: String): String = when (s.uppercase()) {
        "OPEN" -> "Open"; "DRAFT" -> "Draft"; "MERGED" -> "Merged"; "CLOSED" -> "Closed"; else -> ""
    }

    /** A label without Claude's "(Recommended)" at its end, and whether it had it. */
    fun recommended(label: String): Pair<String, Boolean> {
        for (suffix in listOf(" (Recommended)", " (recommended)")) {
            if (label.endsWith(suffix)) return label.dropLast(suffix.length) to true
        }
        return label to false
    }

    /** What a task says while it's under way ("Running the tests"), else its subject. */
    fun saying(t: AgentTask): String =
        if (t.status == AgentTask.Status.STATUS_IN_PROGRESS && t.activeForm.isNotEmpty()) t.activeForm else t.subject

    /** A shell by what it's for, as the agent described it, else its command's first line. */
    fun title(s: AgentShell): String = s.description.ifEmpty { s.command.substringBefore('\n') }
}
