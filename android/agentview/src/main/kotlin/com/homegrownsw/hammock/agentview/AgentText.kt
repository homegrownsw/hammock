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
    fun at(cursor: Int, text: String): IntRange? {
        if (cursor < 0 || cursor > text.length) return null
        if (cursor < text.length && !text[cursor].isWhitespace()) return null
        var start = cursor
        while (start > 0 && !text[start - 1].isWhitespace()) start--
        if (start >= cursor || text[start] != '/') return null
        return start until cursor
    }

    /** Nothing but whitespace before it: agents' built-in commands only run there. */
    fun startsMessage(token: IntRange, text: String) = text.substring(0, token.first).isBlank()

    /** Up to eight commands the token begins, leaving out start-only ones when it isn't at the start. */
    fun suggestions(token: IntRange, text: String, commands: List<String>, startOnly: Set<String>): List<String> {
        val q = text.substring(token.first, token.last + 1).lowercase()
        val skip = if (startsMessage(token, text)) emptySet() else startOnly
        return commands.filter { it !in skip && it.lowercase().startsWith(q) }.take(8)
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
