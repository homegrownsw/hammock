package com.homegrownsw.hammock.client

import hammock.v1.Hammock.Agent
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** The bar over a pane whose program ended: when it shows, and what it says, as on the Mac and the iPhone. */
class RunAgainTest {
    private val kinds = AgentCatalog.resolve(emptyList())

    private fun ended(edit: Pane.Builder.() -> Unit = {}): Pane =
        Pane.newBuilder().setId("p").setKind(PaneKind.PANE_KIND_TERMINAL).setExited(true).setExitStatus(1).apply(edit).build()

    @Test fun anEndedTerminalCanRunAgain() {
        assertTrue(ended().canRunAgain)
        assertFalse(ended { exited = false }.canRunAgain)
        assertFalse(ended { asleep = true }.canRunAgain)
        assertFalse(ended { kind = PaneKind.PANE_KIND_BROWSER }.canRunAgain)
        // A scheduled run's task runs again instead.
        assertFalse(ended { scheduleRunId = "r" }.canRunAgain)
    }

    @Test fun itSaysHowItEnded() {
        assertEquals("Exited 1", ended().exitedStatusLabel)
        assertEquals("Couldn't start", ended { startError = "no such file" }.exitedStatusLabel)
    }

    @Test fun anAgentWithASessionResumes() {
        assertEquals("Run Again", ended().runAgainTitle(kinds))
        assertEquals("Resume", ended { agent = Agent.newBuilder().setHarness("claude").setSessionId("s").build() }.runAgainTitle(kinds))
        // Nothing said in it yet: its own command again.
        assertEquals("Run Again", ended { agent = Agent.newBuilder().setHarness("claude").build() }.runAgainTitle(kinds))
    }
}
