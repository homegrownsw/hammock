package com.homegrownsw.hammock.client

/**
 * The link's history, for [LinkStatus]: whether it's connected since the
 * model started, when this try at connecting began (null while connected),
 * when what's shown was last live (null before the first drop), and why the
 * last try failed, in a person's words (null: nothing better to say).
 */
data class LinkHistory(
    val everConnected: Boolean = false,
    val downSinceMs: Long? = System.currentTimeMillis(),
    val lastLiveMs: Long? = null,
    val reason: String? = null,
)

/**
 * What the phone says about its link to the computer, on every screen (the
 * connection banner), as the iPhone's LinkStatus: nothing while connected, or
 * while it's been down only a moment (a blip); then connecting (not connected
 * yet since the app opened) or reconnecting, and after a while, that it can't
 * reach it. It keeps trying throughout, and Try Again tries now.
 */
enum class LinkStatus {
    Connecting, Reconnecting, Unreachable;

    fun title(host: String): String = when (this) {
        Connecting -> "Connecting to $host…"
        Reconnecting -> "Reconnecting to $host…"
        Unreachable -> "Can't reach $host"
    }

    /** The line under the title, once it can't reach the computer: why, and how old what's on screen is. */
    fun detail(reason: String?, lastLiveMs: Long?, nowMs: Long): String? {
        if (this != Unreachable) return null
        val parts = mutableListOf(reason ?: GENERIC_REASON)
        if (lastLiveMs != null) parts += "Last updated ${elapsedString(lastLiveMs, nowMs)} ago."
        return parts.joinToString(" ")
    }

    companion object {
        /** Down for less than this: nothing shown. */
        const val SHOW_AFTER_MS = 1_000L
        /** Down for this long: "Can't reach". */
        const val UNREACHABLE_AFTER_MS = 8_000L
        const val GENERIC_REASON = "It may be asleep, or on another network."

        /** What to show, null for nothing. */
        fun shown(connected: Boolean, history: LinkHistory, nowMs: Long): LinkStatus? {
            val since = history.downSinceMs
            if (connected || since == null) return null
            val down = nowMs - since
            return when {
                down < SHOW_AFTER_MS -> null
                down >= UNREACHABLE_AFTER_MS -> Unreachable
                history.everConnected -> Reconnecting
                else -> Connecting
            }
        }

        /**
         * What TalkBack says as the status changes, if anything: that it can't reach the
         * computer, and that it's back once something was shown. Nothing for a blip, or for
         * connecting and reconnecting (the banner says those, and they pass).
         */
        fun announcement(old: LinkStatus?, new: LinkStatus?, host: String): String? = when {
            new == Unreachable && old != Unreachable -> "Can't reach $host"
            new == null && old == Connecting -> "Connected to $host"
            new == null && old != null -> "Reconnected to $host"
            else -> null
        }
    }
}
