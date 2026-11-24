package com.homegrownsw.hammock.client

import hammock.v1.Hammock.Agent
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.LimitResume
import hammock.v1.Hammock.Pane
import java.time.ZoneId
import java.time.ZonedDateTime
import java.util.Locale
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull

class LimitResumeTest {
    // A Tuesday, at noon.
    private val now = ZonedDateTime.of(2026, 11, 24, 12, 0, 0, 0, ZoneId.of("Europe/London"))

    private fun resume(minutes: Long, known: Boolean = true) =
        LimitResume.newBuilder().setAtUnixMs(now.plusMinutes(minutes).toInstant().toEpochMilli()).setResetKnown(known).build()

    @Test fun saysWhen() {
        assertEquals("Continues at 15:01", resume(181).label(now, Locale.UK))
        assertEquals("Tries again at 13:00", resume(60, known = false).label(now, Locale.UK))
        assertEquals("Continues tomorrow at 09:00", resume(21 * 60).label(now, Locale.UK))
        assertEquals("Continues Thursday at 10:00", resume(46 * 60).label(now, Locale.UK))
        assertEquals("Continues 3 Dec at 12:00", resume(9 * 24 * 60).label(now, Locale.UK))
    }

    @Test fun onlyWhileErrored() {
        val waiting = Pane.newBuilder().setAgent(Agent.newBuilder().setState(AgentState.AGENT_STATE_ERRORED).setLimitResume(resume(60))).build()
        assertEquals("Continues at 13:00", waiting.limitResumeLabel(now, Locale.UK))
        val working = waiting.toBuilder().setAgent(waiting.agent.toBuilder().setState(AgentState.AGENT_STATE_WORKING)).build()
        assertNull(working.limitResumeLabel(now, Locale.UK))
        assertNull(Pane.getDefaultInstance().limitResumeLabel(now, Locale.UK))
    }

    @Test fun seenIsACalmWait() {
        val unseen = Agent.newBuilder().setState(AgentState.AGENT_STATE_ERRORED).setLimitResume(resume(60)).build()
        assertEquals(false, unseen.waitingOnLimit)
        assertEquals(AgentState.AGENT_STATE_ERRORED, unseen.shownState)
        val seen = unseen.toBuilder().setSeen(true).build()
        assertEquals(true, seen.waitingOnLimit)
        assertEquals(AgentState.AGENT_STATE_IDLE, seen.shownState)
    }
}
