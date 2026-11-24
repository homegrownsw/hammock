package com.homegrownsw.hammock.demo

import com.google.protobuf.ByteString
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

class DemoDaemonTest {
    /** A demo at 1/100 its pace, and everything it sends. */
    private class Harness(longChat: Boolean = false, newChat: Boolean = false) {
        val demo = DemoDaemon(longChat = longChat, newChat = newChat, pace = 0.01)
        val got = LinkedBlockingQueue<ServerMessage>()
        private var id = 0L

        fun send(build: ClientMessage.Builder.() -> Unit) = demo.receive(ClientMessage.newBuilder().apply(build).build()) { got.add(it) }
        fun call(fill: ClientMessage.Builder.() -> Unit): ServerMessage {
            val n = ++id
            send { this.id = n; fill() }
            return next { it.replyTo == n }
        }
        fun next(timeoutMs: Long = 2000, where: (ServerMessage) -> Boolean): ServerMessage {
            val end = System.currentTimeMillis() + timeoutMs
            while (true) {
                val left = end - System.currentTimeMillis()
                val m = got.poll(left.coerceAtLeast(0), TimeUnit.MILLISECONDS) ?: throw AssertionError("nothing matching in time")
                if (where(m)) return m
            }
        }
    }

    @Test fun helloAndSubscribeGiveTheMadeUpMac() {
        val h = Harness()
        val hello = h.call { hello = Hammock.Hello.getDefaultInstance() }
        assertEquals("MacBook", hello.helloReply.hostName)
        val s = h.call { subscribe = Hammock.Subscribe.getDefaultInstance() }.state
        assertTrue(s.full)
        assertEquals(listOf("g_notes", "g_hammock", "g_release", "g_units"), s.groupsList.map { it.id })
        assertEquals(DemoDaemon.STUDIO, s.groupsList.first { it.id == "g_release" }.hostUserId)
        assertEquals(2, s.computers.listCount)
        // Every tab's panes are there.
        val panes = s.panesList.map { it.id }.toSet()
        for (t in s.tabsList) assertTrue(t.id, t.focusedPaneId in panes)
        assertEquals(AgentState.AGENT_STATE_NEEDS_INPUT, s.panesList.first { it.id == "p_triage" }.agent.state)
        assertEquals(6, s.schedulesCount)
        assertEquals(listOf("a_triage"), s.attentionList.map { it.id })
    }

    @Test fun attachingATerminalSendsItsScreen() {
        val h = Harness()
        h.send { attachPane = Hammock.AttachPane.newBuilder().setPaneId("p_shell").build() }
        val snap = h.next { it.hasPaneSnapshot() }.paneSnapshot
        assertEquals("p_shell", snap.paneId)
        assertEquals(80, snap.cols)
        assertTrue(snap.data.toStringUtf8().contains("git log --oneline -4\r\n"))
        h.send { attachPane = Hammock.AttachPane.newBuilder().setPaneId("p_page").build() }
        assertTrue(h.next { it.hasEditorUpdate() }.editorUpdate.text.startsWith("---\ntitle: iPhone"))
    }

    @Test fun theShellAnswersCannedCommands() {
        val h = Harness()
        h.send { input = Hammock.Input.newBuilder().setPaneId("p_shell").setData(ByteString.copyFromUtf8("git status\r")).build() }
        val out = h.next { it.hasPaneOutput() }.paneOutput.data.toStringUtf8()
        assertTrue(out, out.startsWith("git status\r\nOn branch main"))
    }

    @Test fun allowingThePromptFinishesTheAgent() {
        val h = Harness()
        h.send { watchAgent = Hammock.WatchAgent.newBuilder().setPaneId("p_triage").setWatch(true).build() }
        val first = h.next { it.hasAgentView() }.agentView
        assertTrue(first.replace)
        assertEquals(Hammock.AgentPrompt.Kind.KIND_PERMISSION, first.prompt.kind)
        h.call { agentAnswer = Hammock.AgentAnswer.newBuilder().setPaneId("p_triage").setOption(1).build() }
        val done = h.next { it.hasAgentView() && it.agentView.blocksList.any { b -> b.id == "b_triage_done" } }.agentView
        assertFalse(done.working)
        val idle = h.next { it.hasState() && it.state.panesList.any { p -> p.id == "p_triage" && p.agent.state == AgentState.AGENT_STATE_IDLE } }
        assertNotNull(idle)
        // Its run is ready, in the next update.
        val run = h.next { it.hasState() && it.state.schedulesCount > 0 }.state.schedulesList.first().runsList.first { it.id == "r_triage_now" }
        assertEquals(Hammock.ScheduleRun.Status.STATUS_READY, run.status)
    }

    @Test fun aMessageGetsTheDemosAnswer() {
        val h = Harness()
        h.send { watchAgent = Hammock.WatchAgent.newBuilder().setPaneId("p_inbox").setWatch(true).build() }
        h.call { agentSend = Hammock.AgentSend.newBuilder().setPaneId("p_inbox").setText("Hello").build() }
        val reply = h.next { it.hasAgentView() && it.agentView.blocksList.any { b -> b.id.startsWith("b_demo_") } }
        assertTrue(reply.agentView.getBlocks(0).text.contains("Hammock's demo"))
    }

    @Test fun changingTheMacNeedsOne() {
        val h = Harness()
        for (build in listOf<ClientMessage.Builder.() -> Unit>(
            { createTab = Hammock.CreateTab.newBuilder().setGroupId("g_hammock").build() },
            { createGroup = Hammock.CreateGroup.getDefaultInstance() },
            { closeTab = Hammock.CloseTab.newBuilder().setTabId("t_shell").build() },
        )) {
            val r = h.call(build)
            assertEquals(DemoDaemon.NEEDS_MAC, r.error.message)
        }
    }

    @Test fun movingATabAndSleepingAnAgent() {
        val h = Harness()
        h.call { moveTab = Hammock.MoveTab.newBuilder().setTabId("t_shell").setGroupId("g_notes").setIndex(0).build() }
        val u = h.next { it.hasState() }.state
        assertEquals("t_shell", u.groupsList.first { it.id == "g_notes" }.getTabIds(0))
        assertFalse(u.groupsList.first { it.id == "g_hammock" }.tabIdsList.contains("t_shell"))
        // A working agent can't sleep; a finished one can.
        assertTrue(h.call { sleepPane = Hammock.SleepPane.newBuilder().setPaneId("p_polish").build() }.hasError())
        assertFalse(h.call { sleepPane = Hammock.SleepPane.newBuilder().setPaneId("p_inbox").build() }.hasError())
        assertTrue(h.next { it.hasState() }.state.getPanes(0).asleep)
    }

    @Test fun longChatPagesOlderBlocks() {
        val h = Harness(longChat = true)
        h.send { watchAgent = Hammock.WatchAgent.newBuilder().setPaneId("p_polish").setWatch(true).build() }
        val v = h.next { it.hasAgentView() }.agentView
        assertTrue(v.hasMore)
        assertTrue(v.blocksCount >= 150)
        h.send { watchAgent = Hammock.WatchAgent.newBuilder().setPaneId("p_polish").setWatch(true).setBeforeBlockId(v.getBlocks(0).id).build() }
        val older = h.next { it.hasAgentView() }.agentView
        assertTrue(older.older)
        assertTrue(older.blocksCount > 0)
    }

    @Test fun newChatStartsEmpty() {
        val h = Harness(newChat = true)
        h.send { watchAgent = Hammock.WatchAgent.newBuilder().setPaneId("p_polish").setWatch(true).build() }
        assertEquals(0, h.next { it.hasAgentView() }.agentView.blocksCount)
    }
}
