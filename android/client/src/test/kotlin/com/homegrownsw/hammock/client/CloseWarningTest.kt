package com.homegrownsw.hammock.client

import hammock.v1.Hammock.Agent
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.Pane
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Closing a pane or a tab asks first only when it stops an agent at work, and says which. */
class CloseWarningTest {
    private fun pane(state: AgentState, harness: String = "claude", subagents: Int = 0, command: Boolean = false, exited: Boolean = false) =
        Pane.newBuilder().setExited(exited).setAgent(
            Agent.newBuilder().setHarness(harness).setState(state).setSubagents(subagents).setRunningCommand(command),
        ).build()

    @Test fun anAgentAtWorkIsNamed() {
        assertEquals("Claude is still working. Closing stops it.", closeWarning(listOf(pane(AgentState.AGENT_STATE_WORKING))))
        assertEquals("Codex is waiting for you. Closing stops it.", closeWarning(listOf(pane(AgentState.AGENT_STATE_NEEDS_INPUT, "codex"))))
        assertEquals("Claude is running a command. Closing stops it.", closeWarning(listOf(pane(AgentState.AGENT_STATE_IDLE, command = true))))
        assertEquals("Claude has 2 subagents still running. Closing stops it.", closeWarning(listOf(pane(AgentState.AGENT_STATE_IDLE, subagents = 2))))
    }

    @Test fun severalAreCounted() {
        val tab = listOf(pane(AgentState.AGENT_STATE_WORKING), pane(AgentState.AGENT_STATE_STARTING, "codex"), Pane.getDefaultInstance())
        assertEquals("2 agents are still at work. Closing stops them.", closeWarning(tab))
    }

    @Test fun nothingAtWorkClosesWithoutAsking() {
        assertNull(closeWarning(listOf(pane(AgentState.AGENT_STATE_IDLE))))
        assertNull(closeWarning(listOf(pane(AgentState.AGENT_STATE_WORKING, exited = true))))
        assertNull(closeWarning(listOf(Pane.getDefaultInstance())))
        assertNull(closeWarning(emptyList()))
    }
}
