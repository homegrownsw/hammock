package com.homegrownsw.hammock.agentview

import androidx.compose.runtime.Immutable
import hammock.v1.Hammock.AgentBlock

/**
 * A row of the conversation: one block, or a run of tool calls shown as one.
 * Immutable, so a row whose blocks haven't changed skips recomposing while
 * the status line ticks (the blocks are the same instances from update to
 * update, and a protobuf's equals checks identity first).
 */
@Immutable
data class AgentItem(
    val id: String,
    val block: AgentBlock? = null,
    val tools: List<AgentBlock> = emptyList(),
    val thoughtSeconds: Int? = null,
) {
    /** What kind of row it is, for the list's contentType (rows of a kind reuse each other's layout). */
    val kind: String get() {
        val b = block ?: return if (tools.size == 1) "tool" else "group"
        return when (b.kind) {
            AgentBlock.Kind.KIND_USER -> "user"
            AgentBlock.Kind.KIND_TEXT -> "text"
            AgentBlock.Kind.KIND_THINKING -> if (b.text.isBlank()) "thought" else "text"
            AgentBlock.Kind.KIND_TOOL -> if (b.tool == "SendUserFile") "files" else "tool"
            AgentBlock.Kind.KIND_QUESTION -> "question"
            AgentBlock.Kind.KIND_PLAN -> "plan"
            AgentBlock.Kind.KIND_SUBAGENT -> "subagent"
            AgentBlock.Kind.KIND_NOTE -> "note"
            AgentBlock.Kind.KIND_DONE -> "done"
            else -> "other"
        }
    }

    companion object {
        /** A turn shorter than this ends without a "Worked for" line. */
        const val DONE_SHOWN_FROM_MS = 30_000L

        /** The blocks as rows, as the iPhone groups them (AgentItem.group). */
        fun group(blocks: List<AgentBlock>): List<AgentItem> {
            val out = ArrayList<AgentItem>(blocks.size)
            var run = ArrayList<AgentBlock>()
            // A run of tool calls is one row from its first call on, whatever
            // it grows to: its key doesn't change when a second call joins,
            // so the list doesn't swap one row for another.
            fun flush() {
                if (run.isNotEmpty()) out.add(AgentItem(id = "g:" + run[0].id, tools = run))
                run = ArrayList()
            }
            for ((i, b) in blocks.withIndex()) {
                when {
                    b.kind == AgentBlock.Kind.KIND_TOOL && (b.diff.isNotEmpty() || b.tool == "SendUserFile") -> {
                        // Edits stand on their own, so what changed shows without
                        // opening a group. Keyed as they would be as a run's first
                        // call (an edit has no diff until it's made), so the row
                        // stays the same row when its diff arrives.
                        flush()
                        out.add(AgentItem(id = "g:" + b.id, block = b))
                    }
                    b.kind == AgentBlock.Kind.KIND_TOOL -> run.add(b)
                    b.kind == AgentBlock.Kind.KIND_THINKING && b.text.isNotBlank() -> {
                        // Thinking with words is the agent's narration: it
                        // stands on its own, even between tool calls.
                        flush()
                        out.add(AgentItem(id = b.id, block = b))
                    }
                    // A quick reply doesn't need to say how long it took.
                    b.kind == AgentBlock.Kind.KIND_DONE && b.durationMs < DONE_SHOWN_FROM_MS -> continue
                    b.kind == AgentBlock.Kind.KIND_THINKING -> {
                        // Silent thinking between tool calls folds into the run.
                        if (run.isNotEmpty()) continue
                        var secs: Int? = null
                        if (i + 1 < blocks.size && b.timeUnixMs > 0 && blocks[i + 1].timeUnixMs > b.timeUnixMs) {
                            secs = ((blocks[i + 1].timeUnixMs - b.timeUnixMs) / 1000).toInt()
                        }
                        out.add(AgentItem(id = b.id, block = b, thoughtSeconds = secs))
                    }
                    // A kind from a newer daemon: no row, rather than an empty one.
                    b.kind == AgentBlock.Kind.UNRECOGNIZED || b.kind == AgentBlock.Kind.KIND_UNSPECIFIED -> continue
                    else -> {
                        flush()
                        out.add(AgentItem(id = b.id, block = b))
                    }
                }
            }
            flush()
            // Row ids key the list, which can't take two the same: should the
            // daemon ever send a block twice, the second gets a suffix.
            val seen = HashSet<String>(out.size * 2)
            for (i in out.indices) {
                var id = out[i].id
                var n = 1
                while (!seen.add(id)) id = out[i].id + "#" + n++
                if (id != out[i].id) out[i] = out[i].copy(id = id)
            }
            return out
        }
    }
}

/** A picture or file going with a message: its name and bytes, as they'll be sent. */
@Immutable
class OutgoingAttachment(val name: String, val data: ByteArray)

/** A message sent from here, shown until the transcript has it. */
@Immutable
data class PendingSend(
    val id: Long,
    /** As the transcript will show it, to know it there by. */
    val text: String,
    val attachments: List<OutgoingAttachment> = emptyList(),
) {
    /**
     * A "!" command (an agent's shell mode), as the Bash run it's shown as
     * once the transcript has it, still running, with [output] so far. Null:
     * a message.
     */
    fun bangRun(output: String = ""): AgentBlock? {
        if (attachments.isNotEmpty() || !text.startsWith("!")) return null
        val cmd = text.drop(1).trim()
        if (cmd.isEmpty()) return null
        return AgentBlock.newBuilder().setId(RUN_PREFIX + id).setKind(AgentBlock.Kind.KIND_TOOL).setTool("Bash")
            .setInput(cmd).setTitle("Ran `" + cmd.lineSequence().first() + "`").setOutput(output).setPending(true).build()
    }

    val isBang: Boolean get() = bangRun() != null

    companion object {
        const val RUN_PREFIX = "pending-run-"
    }
}

/**
 * A block that's something you sent: a message, or a "!" command, which is a
 * Bash run that's a turn of its own (the agent's own runs are in the turn of
 * the message they answer).
 */
internal fun AgentBlock.sentByYou(): Boolean =
    kind == AgentBlock.Kind.KIND_USER || (kind == AgentBlock.Kind.KIND_TOOL && tool == "Bash" && turnId == id)

/** A "!" command you ran, from the transcript or still running: shown as you typed it. */
internal fun AgentBlock.isBang(): Boolean =
    tool == "Bash" && (turnId == id || id.startsWith(PendingSend.RUN_PREFIX))
