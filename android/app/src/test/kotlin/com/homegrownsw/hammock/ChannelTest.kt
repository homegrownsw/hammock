package com.homegrownsw.hammock

import org.junit.Assert.assertEquals
import org.junit.Test

/** A notification's channel comes from what happened, as the computer says; guessed only for a computer from before it said. */
class ChannelTest {
    private fun ch(kind: String, category: String = "", title: String = "Map Judder", body: String = "", live: Boolean = false) =
        Notifications.channel(kind, category, title, body, live)

    @Test fun byWhatHappened() {
        assertEquals(Notifications.CH_NEEDS_YOU, ch("needs_input", body = "Claude needs your input", live = true))
        assertEquals(Notifications.CH_NEEDS_YOU, ch("program_needs_input", live = true))
        assertEquals(Notifications.CH_NEEDS_YOU, ch("grant", category = "grant", live = true))
        // Words about errors don't make it one, nor a finished agent's live item a question.
        assertEquals(Notifications.CH_FINISHED, ch("finished", body = "Claude: fixed the error handling", live = true))
        assertEquals(Notifications.CH_FINISHED, ch("program_finished"))
        assertEquals(Notifications.CH_ERRORS, ch("errored", body = "Claude exited with status 1", live = true))
        assertEquals(Notifications.CH_ERRORS, ch("program_errored"))
        assertEquals(Notifications.CH_KEY, ch("hammockkey", category = "hammockkey"))
        assertEquals(Notifications.CH_JOIN, ch("join", category = "join"))
    }

    @Test fun guessedFromAnOlderComputer() {
        assertEquals(Notifications.CH_KEY, ch("", category = "hammockkey"))
        assertEquals(Notifications.CH_JOIN, ch("", category = "join"))
        assertEquals(Notifications.CH_NEEDS_YOU, ch("", category = "grant"))
        assertEquals(Notifications.CH_ERRORS, ch("", body = "Hit an error", live = true))
        assertEquals(Notifications.CH_NEEDS_YOU, ch("", body = "Needs your input", live = true))
        assertEquals(Notifications.CH_FINISHED, ch("", body = "Ready"))
    }
}
