package com.homegrownsw.hammock.client

import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.AttentionItem

/**
 * What a Needs You card shows of the prompt an agent is waiting on (its whole text as the pane shows it:
 * the command, every line), cut to a few lines. Allow goes from the card only when nothing was cut:
 * otherwise the card offers Review, which opens the pane, so what's allowed is always seen.
 */
data class PromptPreview(val text: String, val hiddenLines: Int, val complete: Boolean) {
    /** "+3 more lines", when any are left out. */
    val moreLabel: String?
        get() = when (hiddenLines) {
            0 -> if (complete) null else "More not shown"
            1 -> "+1 more line"
            else -> "+$hiddenLines more lines"
        }

    companion object {
        /** The most lines a card shows, and characters in all (as the iPhone's). */
        const val MAX_LINES = 10
        const val MAX_CHARACTERS = 1200

        fun of(detail: String): PromptPreview {
            var lines = detail.split("\n")
            var hidden = 0
            if (lines.size > MAX_LINES) {
                hidden = lines.size - MAX_LINES
                lines = lines.take(MAX_LINES)
            }
            var shown = lines.joinToString("\n")
            // "…" first: the prompt ran off the top of its pane's screen.
            var cut = detail.startsWith("…\n")
            if (shown.length > MAX_CHARACTERS) {
                shown = shown.take(MAX_CHARACTERS) + "…"
                cut = true
            }
            return PromptPreview(shown, hidden, hidden == 0 && !cut)
        }
    }
}

/** The prompt it's waiting on, as its card shows it: null without one (an older daemon, a question, or not shown yet). */
val AttentionItem.promptPreview: PromptPreview?
    get() = if (state == AgentState.AGENT_STATE_NEEDS_INPUT && !question && detail.isNotEmpty()) PromptPreview.of(detail) else null

/** Allow can go straight from its card: it shows the whole of what's asked, or has nothing more to show. */
val AttentionItem.allowsFromCard: Boolean get() = promptPreview?.complete ?: true
