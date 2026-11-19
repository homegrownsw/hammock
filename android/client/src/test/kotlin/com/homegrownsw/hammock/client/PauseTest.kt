package com.homegrownsw.hammock.client

import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import org.junit.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertTrue

/**
 * The app hangs up in the background (pause) and signs in again when it's
 * back (resume); a link that stops answering is dialled again (checkConnection);
 * and a computer that's removed the phone is told apart from one that's
 * merely unreachable.
 */
class PauseTest {
    /** A daemon that answers sign-in, counting each one; `mute(n)`: the n-th Hello goes unanswered. */
    private class Daemon(val mute: Int = 0) : DaemonStandIn {
        val hellos = AtomicInteger()
        override fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit) {
            when (msg.msgCase) {
                ClientMessage.MsgCase.HELLO -> {
                    if (hellos.incrementAndGet() == mute) return
                    reply(ServerMessage.newBuilder().setReplyTo(msg.id).setHelloReply(Hammock.HelloReply.newBuilder().setHostName("Studio")).build())
                }
                ClientMessage.MsgCase.SUBSCRIBE ->
                    reply(ServerMessage.newBuilder().setReplyTo(msg.id).setState(Hammock.StateUpdate.newBuilder().setFull(true)).build())
                else -> {}
            }
        }
    }

    @Test fun pausedConnectsOnlyOnceResumed(): Unit = runBlocking {
        val d = Daemon()
        val m = AppModel.standIn(d)
        m.start(paused = true).join()
        Thread.sleep(300)
        assertEquals(0, d.hellos.get(), "no sign-in before the app's on screen")
        m.resume()
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        assertEquals(1, d.hellos.get())

        m.pause().join()
        assertEquals(Link.Connecting, m.link.value)
        assertEquals(null, m.connection)
        m.reconnectNow().join() // (nothing while paused)
        m.checkConnection().join()
        Thread.sleep(300)
        assertEquals(1, d.hellos.get(), "no dialling while paused")

        m.resume()
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        assertEquals(2, d.hellos.get())
        m.stop().join()
    }

    @Test fun deadLinkIsDialledAgain(): Unit = runBlocking {
        val d = Daemon(mute = 2) // the check's Hello gets no answer
        val m = AppModel.standIn(d)
        m.start()
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        m.checkConnection(timeoutMs = 300)
        withTimeout(5_000) { m.link.first { it is Link.Failed } }
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        assertEquals(3, d.hellos.get())
        m.stop().join()
    }

    @Test fun liveLinkIsKept(): Unit = runBlocking {
        val d = Daemon()
        val m = AppModel.standIn(d)
        m.start()
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        val c = m.connection
        m.checkConnection(timeoutMs = 2_000).join()
        assertEquals(Link.Connected, m.link.value)
        assertTrue(c === m.connection, "the same connection")
        m.stop().join()
    }

    @Test fun removedIsToldApart() {
        assertTrue(AppModel.signInRefused(DaemonException("authentication failed")))
        assertFalse(AppModel.signInRefused(DaemonException("Connection closed")))
        assertFalse(AppModel.signInRefused(DaemonException("API authentication failed")))
        assertFalse(AppModel.signInRefused(java.io.IOException("authentication failed")))
    }
}
