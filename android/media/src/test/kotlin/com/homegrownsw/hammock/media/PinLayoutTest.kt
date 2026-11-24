package com.homegrownsw.hammock.media

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import hammock.v1.Hammock
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

class PinLayoutTest {
    private val eps = 0.001f

    @Test fun fittedPageIsCentredInTheBox() {
        val r = PinLayout.pageRect(Size(1000f, 800f), Size(1000f, 600f), 1f, Offset.Zero)
        assertEquals(Rect(0f, 100f, 1000f, 700f), r)
    }

    @Test fun zoomedPageGrowsAboutItsCentreAndPans() {
        val r = PinLayout.pageRect(Size(1000f, 800f), Size(1000f, 600f), 2f, Offset(100f, -50f))
        assertEquals(Rect(-400f, -250f, 1600f, 950f), r)
    }

    @Test fun fractionsRoundTrip() {
        val page = Rect(100f, 50f, 500f, 250f)
        val p = PinLayout.point(0.25, 0.5, page)
        assertEquals(200f, p.x, eps)
        assertEquals(150f, p.y, eps)
        val (x, y) = PinLayout.fraction(p, page)!!
        assertEquals(0.25, x, 1e-6)
        assertEquals(0.5, y, 1e-6)
    }

    @Test fun touchOutsideThePageIsNoPin() {
        val page = Rect(100f, 50f, 500f, 250f)
        assertNull(PinLayout.fraction(Offset(50f, 100f), page))
        assertNull(PinLayout.fraction(Offset(200f, 300f), page))
        assertNull(PinLayout.fraction(Offset(10f, 10f), Rect.Zero))
    }

    @Test fun aPinOnTheFarEdgeShows() {
        val bounds = Rect(0f, 0f, 100f, 100f)
        assertEquals(true, PinLayout.shows(Offset(100f, 100f), bounds))
        assertEquals(false, PinLayout.shows(Offset(100.5f, 50f), bounds))
    }

    @Test fun outlineFollowsThePage() {
        val page = Rect(0f, 0f, 200f, 100f)
        val pin = Hammock.Pin.newBuilder().setRect(Hammock.PinRect.newBuilder().setX(0.5).setY(0.5).setW(0.25).setH(0.5)).build()
        assertEquals(Rect(100f, 50f, 150f, 100f), PinLayout.outline(pin, page))
        assertNull(PinLayout.outline(Hammock.Pin.getDefaultInstance(), page))
    }

    @Test fun badgeSitsUpAndRightOfItsPoint() {
        val c = PinLayout.badgeCentre(Offset(100f, 100f), Offset(20f, -20f), 12f, Rect(0f, 0f, 400f, 400f))
        assertEquals(Offset(120f, 80f), c)
    }

    @Test fun badgeAtAnEdgeStaysInside() {
        val bounds = Rect(0f, 0f, 400f, 400f)
        assertEquals(Offset(388f, 12f), PinLayout.badgeCentre(Offset(395f, 2f), Offset(20f, -20f), 12f, bounds))
    }

    @Test fun badgeInBoundsNarrowerThanItIsCentred() {
        val c = PinLayout.badgeCentre(Offset(5f, 5f), Offset(20f, -20f), 12f, Rect(0f, 0f, 10f, 10f))
        assertEquals(Offset(5f, 5f), c)
    }

    private fun pane(id: String, harness: String? = null, state: Hammock.AgentState = Hammock.AgentState.AGENT_STATE_IDLE, exited: Boolean = false, tab: String = "") =
        Hammock.Pane.newBuilder().setId(id).setExited(exited).setTabId(tab).apply {
            if (harness != null) setAgent(Hammock.Agent.newBuilder().setHarness(harness).setState(state))
        }.build()

    @Test fun targetsAreTheGroupsOtherRunningAgents() {
        val panes = listOf(
            pane("self", "claude"),
            pane("a", "claude"),
            pane("shell"),
            pane("gone", "codex", state = Hammock.AgentState.AGENT_STATE_EXITED),
            pane("ended", "codex", exited = true),
            pane("b", "codex", state = Hammock.AgentState.AGENT_STATE_WORKING),
        )
        assertEquals(listOf("a", "b"), PinLayout.targets(panes, "self").map { it.id })
    }

    @Test fun twoOfTheSameAgentAreToldApartByTab() {
        val ts = listOf(pane("a", "claude", tab = "t1"), pane("b", "claude", tab = "t2"), pane("c", "codex", tab = "t3"))
        val labels = PinLayout.targetLabels(ts, { if (it.agent.harness == "claude") "Claude" else "Codex" }, { mapOf("t1" to "web", "t2" to "api", "t3" to "x")[it.tabId]!! })
        assertEquals(listOf("Claude · web", "Claude · api", "Codex"), labels)
    }

    @Test fun statusSaysHowMany() {
        assertEquals("Touch and hold to pin a note", PinLayout.status(0, false))
        assertEquals("1 note", PinLayout.status(1, false))
        assertEquals("3 notes", PinLayout.status(3, false))
        assertEquals("Held: the agent's waiting on you", PinLayout.status(3, true))
    }
}
