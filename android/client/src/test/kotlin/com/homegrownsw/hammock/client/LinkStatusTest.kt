package com.homegrownsw.hammock.client

import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** What the phone says about its link: nothing for a blip, then connecting or reconnecting, then that it can't reach it. */
class LinkStatusTest {
    private val now = 1_000_000_000L
    private fun shown(connected: Boolean = false, ever: Boolean = true, downFor: Long?) =
        LinkStatus.shown(connected, LinkHistory(everConnected = ever, downSinceMs = downFor?.let { now - it }), now)

    @Test fun quietWhileConnected() {
        assertNull(shown(connected = true, downFor = null))
        assertNull(shown(connected = true, downFor = 60_000))
    }

    @Test fun blipsAreNotShown() {
        assertNull(shown(downFor = 0))
        assertNull(shown(downFor = 900))
        assertNull(shown(downFor = null)) // paused: away, nothing said
    }

    @Test fun aRealDropShowsPromptly() {
        assertEquals(LinkStatus.Reconnecting, shown(downFor = LinkStatus.SHOW_AFTER_MS))
        assertEquals(LinkStatus.Connecting, shown(ever = false, downFor = 2_000))
    }

    @Test fun aWhileDownCantReachIt() {
        assertEquals(LinkStatus.Reconnecting, shown(downFor = LinkStatus.UNREACHABLE_AFTER_MS - 1))
        assertEquals(LinkStatus.Unreachable, shown(downFor = LinkStatus.UNREACHABLE_AFTER_MS))
        assertEquals(LinkStatus.Unreachable, shown(ever = false, downFor = 30_000))
    }

    @Test fun wording() {
        assertEquals("Connecting to Studio…", LinkStatus.Connecting.title("Studio"))
        assertEquals("Reconnecting to Studio…", LinkStatus.Reconnecting.title("Studio"))
        assertEquals("Can't reach Studio", LinkStatus.Unreachable.title("Studio"))
        assertNull(LinkStatus.Reconnecting.detail("It didn't answer.", now, now))
        assertEquals("It may be asleep, or on another network.", LinkStatus.Unreachable.detail(null, null, now))
        assertEquals("It didn't answer. Last updated 3m ago.", LinkStatus.Unreachable.detail("It didn't answer.", now - 180_000, now))
    }

    @Test fun announcements() {
        assertNull(LinkStatus.announcement(null, LinkStatus.Reconnecting, "Studio"))
        assertEquals("Can't reach Studio", LinkStatus.announcement(LinkStatus.Reconnecting, LinkStatus.Unreachable, "Studio"))
        assertNull(LinkStatus.announcement(LinkStatus.Unreachable, LinkStatus.Unreachable, "Studio"))
        assertEquals("Reconnected to Studio", LinkStatus.announcement(LinkStatus.Unreachable, null, "Studio"))
        assertEquals("Connected to Studio", LinkStatus.announcement(LinkStatus.Connecting, null, "Studio"))
        assertNull(LinkStatus.announcement(null, null, "Studio"))
    }

    /** A daemon that answers sign-in, and can be away. */
    private class Daemon : DaemonStandIn {
        @Volatile var away = false
        override val reachable get() = !away
        override fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit) {
            when (msg.msgCase) {
                ClientMessage.MsgCase.HELLO ->
                    reply(ServerMessage.newBuilder().setReplyTo(msg.id).setHelloReply(Hammock.HelloReply.newBuilder().setHostName("Studio")).build())
                ClientMessage.MsgCase.SUBSCRIBE ->
                    reply(ServerMessage.newBuilder().setReplyTo(msg.id).setState(Hammock.StateUpdate.newBuilder().setFull(true)).build())
                else -> {}
            }
        }
    }

    @Test fun dropKeepsHistoryAndTryAgainReconnects(): Unit = runBlocking {
        val d = Daemon()
        val m = AppModel.standIn(d)
        assertTrue(!m.history.value.everConnected && m.history.value.downSinceMs != null)
        m.start()
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        assertTrue(m.history.value.everConnected)
        assertNull(m.history.value.downSinceMs)

        d.away = true
        m.dropLink().join()
        withTimeout(5_000) { m.link.first { it is Link.Failed } }
        assertNotNull(m.history.value.downSinceMs)
        assertNotNull(m.history.value.lastLiveMs)
        m.reconnectNow().join()
        assertEquals(LinkStatus.Reconnecting, LinkStatus.shown(false, m.history.value, System.currentTimeMillis()))

        d.away = false
        m.refresh(5_000)
        assertEquals(Link.Connected, m.link.value)
        assertNull(m.history.value.downSinceMs)
        m.stop().join()
    }
}
