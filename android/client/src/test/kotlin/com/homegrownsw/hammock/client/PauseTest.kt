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

    /** A daemon that pairs: a challenge on each connection, Pair answered once [answer] says (refused with `refuse`), then sign-in. */
    private class PairingDaemon(val refuse: Boolean = false) : DaemonStandIn {
        val asked = kotlinx.coroutines.CompletableDeferred<Unit>()
        val answer = kotlinx.coroutines.CompletableDeferred<Unit>()
        val hellos = AtomicInteger()
        val pairs = AtomicInteger()
        override fun opened(send: (ServerMessage) -> Unit) {
            send(ServerMessage.newBuilder().setAuthChallenge(Hammock.AuthChallenge.newBuilder()
                .setNonce(com.google.protobuf.ByteString.copyFrom(ByteArray(32)))).build())
        }
        override fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit) {
            val r = ServerMessage.newBuilder().setReplyTo(msg.id)
            when (msg.msgCase) {
                ClientMessage.MsgCase.PAIR -> {
                    pairs.incrementAndGet()
                    asked.complete(Unit)
                    Thread {
                        runBlocking { answer.await() }
                        reply(if (refuse) r.setError(Hammock.Error.newBuilder().setMessage("pairing code expired")).build()
                        else r.setPaired(Hammock.Paired.newBuilder().setDeviceId("phone-1")).build())
                    }.start()
                }
                ClientMessage.MsgCase.AUTHENTICATE -> reply(r.setOk(Hammock.Ok.getDefaultInstance()).build())
                ClientMessage.MsgCase.HELLO -> {
                    hellos.incrementAndGet()
                    reply(r.setHelloReply(Hammock.HelloReply.newBuilder().setHostName("Studio")).build())
                }
                ClientMessage.MsgCase.SUBSCRIBE -> reply(r.setState(Hammock.StateUpdate.newBuilder().setFull(true)).build())
                else -> {}
            }
        }
    }

    private fun pairingModel(d: DaemonStandIn) = AppModel.standIn(
        d,
        RemoteIdentity(address = "", addresses = emptyList(), lanSubnets = emptyList(), certSHA256 = "", deviceID = null,
            pairingSecret = ByteArray(16), deviceName = "Sam's Phone"),
        SoftwareKey(),
    )

    @Test fun pauseWaitsForPairing(): Unit = runBlocking {
        val d = PairingDaemon()
        val m = pairingModel(d)
        var paired: String? = null
        m.onPaired = { paired = it }
        m.start()
        withTimeout(5_000) { d.asked.await() }
        m.pause().join() // the app away while it pairs
        assertTrue(m.isPairing)
        assertTrue(m.connection != null, "still pairing")

        d.answer.complete(Unit)
        // Paired and signed in, then the pause it was asked for takes effect.
        // (Connected only for a moment: the link may never be seen to say so.)
        withTimeout(5_000) { while (d.hellos.get() == 0 || m.connection != null) kotlinx.coroutines.delay(20) }
        assertEquals("phone-1", paired)
        assertFalse(m.isPairing)
        assertEquals(Link.Connecting, m.link.value)
        m.checkConnection().join()
        Thread.sleep(300)
        assertEquals(1, d.hellos.get(), "no dialling while paused")

        m.resume()
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        assertEquals(2, d.hellos.get(), "signs in with its new device")
        assertEquals(1, d.pairs.get())
        m.stop().join()
    }

    @Test fun backBeforePairedKeepsGoing(): Unit = runBlocking {
        val d = PairingDaemon()
        val m = pairingModel(d)
        m.start()
        withTimeout(5_000) { d.asked.await() }
        m.pause().join()
        m.resume().join()
        d.answer.complete(Unit)
        withTimeout(5_000) { m.link.first { it == Link.Connected } }
        Thread.sleep(300)
        assertEquals(Link.Connected, m.link.value, "not paused once paired")
        m.stop().join()
    }

    @Test fun failedPairingPausedDoesNotRetry(): Unit = runBlocking {
        val d = PairingDaemon(refuse = true)
        val m = pairingModel(d)
        m.start()
        withTimeout(5_000) { d.asked.await() }
        m.pause().join()
        d.answer.complete(Unit)
        val failed = withTimeout(5_000) { m.link.first { it is Link.Failed } }
        assertEquals("pairing code expired", (failed as Link.Failed).message)
        Thread.sleep(800)
        assertEquals(1, d.pairs.get(), "paused once it failed: no tries in the background")
        m.stop().join()
    }

    @Test fun removedIsToldApart() {
        assertTrue(AppModel.signInRefused(DaemonException("authentication failed")))
        assertFalse(AppModel.signInRefused(DaemonException("Connection closed")))
        assertFalse(AppModel.signInRefused(DaemonException("API authentication failed")))
        assertFalse(AppModel.signInRefused(java.io.IOException("authentication failed")))
    }
}
