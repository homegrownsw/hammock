package com.homegrownsw.hammock.media

import com.google.protobuf.ByteString
import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.IOException
import java.nio.ByteBuffer
import java.util.Collections
import java.util.concurrent.Executor
import java.util.concurrent.LinkedBlockingQueue
import java.util.concurrent.TimeUnit

/** VideoStream over QUIC (a fake dialer): what it asks for, where frames and keyframe requests go, and falling back. */
class QuicStreamTest {
    private class FakeSession : QuicSession {
        val records = LinkedBlockingQueue<ByteArray>()
        @Volatile var keyframes = 0
        @Volatile var closed = false
        @Volatile var ended: String? = null
        override fun next(timeoutMs: Int): ByteArray? {
            ended?.let { throw QuicEnded(it) }
            if (closed) throw QuicEnded("closed")
            return records.poll(timeoutMs.toLong(), TimeUnit.MILLISECONDS)
        }
        override fun keyframe() { keyframes++ }
        override fun close() { closed = true }
    }

    private class Dialed(val host: String, val port: Int, val pin: String, val ticket: ByteArray, val datagrams: Boolean)

    private class FakeLink(val fails: Boolean = false) : MediaLink {
        val sent: MutableList<ClientMessage> = Collections.synchronizedList(ArrayList())
        @Volatile var opened: Hammock.OpenMedia? = null
        @Volatile var dialed: Dialed? = null
        @Volatile var failedQuic = false
        val session = FakeSession()
        override suspend fun open(request: Hammock.OpenMedia): Hammock.MediaTicket {
            opened = request
            return Hammock.MediaTicket.newBuilder().setSessionId("ms_1").setQuicPort(7600)
                .setTicket(ByteString.copyFrom(ByteArray(32) { it.toByte() })).build()
        }
        override fun send(build: ClientMessage.Builder.() -> Unit) { sent += ClientMessage.newBuilder().apply(build).build() }
        override fun setHandler(sessionID: String, handler: ((ServerMessage) -> Unit)?) {}
        override val quic get() = QuicTarget("100.64.0.2", "ab".repeat(32))
        override val dialer get() = QuicDialer { host, port, pin, ticket, datagrams ->
            dialed = Dialed(host, port, pin, ticket, datagrams)
            if (fails) throw IOException("UDP blocked")
            session
        }
        override fun quicFailed() { failedQuic = true }
    }

    private class FakeDecoder : VideoDecoder {
        val frames = Collections.synchronizedList(ArrayList<ByteArray>())
        override fun decode(annexB: ByteArray, key: Boolean, ptsUs: Long): Boolean { frames += annexB; return true }
        override fun release() {}
    }

    private val direct = Executor { it.run() }
    private val decoder = FakeDecoder()
    private val factory = DecoderFactory { _, _ -> decoder }

    private fun avcc(vararg units: ByteArray): ByteArray {
        var out = ByteArray(0)
        for (u in units) out += byteArrayOf(0, 0, 0, u.size.toByte()) + u
        return out
    }

    /** A frame record as the transport hands it over. */
    private fun record(seq: Int, key: Boolean, crop: FloatArray? = null): ByteArray {
        val pic = if (key) avcc(byteArrayOf(0x67, 0x42, 0x00, 0x1f), byteArrayOf(0x68, 0xce.toByte(), 0x3c, 0x80.toByte()), byteArrayOf(0x65, 0x11))
        else avcc(byteArrayOf(0x41, 0x33))
        val b = ByteBuffer.allocate(17 + (if (crop != null) 8 else 0) + pic.size)
        b.putInt(seq).put(((if (key) 1 else 0) or (if (crop != null) 8 else 0)).toByte()).putLong(0).putShort(320).putShort(200)
        crop?.forEach { b.putShort((it * 65535).toInt().toShort()) }
        b.put(pic)
        return b.array()
    }

    private fun waitFor(what: String, cond: () -> Boolean) {
        val deadline = System.currentTimeMillis() + 3000
        while (!cond()) {
            if (System.currentTimeMillis() > deadline) throw AssertionError("never: $what")
            Thread.sleep(5)
        }
    }

    @Test fun aPaneOverQuic() {
        val link = FakeLink()
        val v = VideoStream(link, VideoStream.pane(link, "p1").request, direct, log = {})
        v.setOutput(factory)
        v.start()
        waitFor("dialed") { link.dialed != null }
        assertFalse(link.opened!!.inBand)
        assertFalse(link.opened!!.datagrams)
        val d = link.dialed!!
        assertEquals("100.64.0.2", d.host)
        assertEquals(7600, d.port)
        assertEquals("ab".repeat(32), d.pin)
        assertArrayEquals(ByteArray(32) { it.toByte() }, d.ticket)
        assertFalse(d.datagrams)

        link.session.records += record(1, key = true)
        link.session.records += record(2, key = false)
        waitFor("decoded") { decoder.frames.size == 2 }
        assertEquals(VideoStream.State.Playing, v.state.value)
        assertEquals(VideoStream.Size(320, 200), v.size.value)
        // The transport acks: nothing in band.
        assertTrue(link.sent.none { it.hasMediaAck() })

        v.stop()
        waitFor("closed") { link.session.closed }
        assertTrue(link.sent.any { it.hasCloseMedia() })
    }

    @Test fun aScreenTakesDatagramsAndItsCrop() {
        val link = FakeLink()
        val v = VideoStream(link, VideoStream.screen(link, display = 0, computerID = "", maxPixels = 0).request, direct, log = {})
        v.setOutput(factory)
        v.start()
        waitFor("dialed") { link.dialed != null }
        assertTrue(link.opened!!.datagrams && link.dialed!!.datagrams)
        link.session.records += record(1, key = true, crop = floatArrayOf(0.25f, 0.5f, 0.5f, 0.25f))
        waitFor("cropped") { v.crop.value != Crop.WHOLE }
        assertEquals(0.25f, v.crop.value.x, 0.001f)
        assertEquals(0.5f, v.crop.value.w, 0.001f)
        v.stop()
    }

    @Test fun aLinkedComputersScreenStaysInBand() {
        val link = FakeLink()
        val v = VideoStream(link, VideoStream.screen(link, display = 0, computerID = "us_x", maxPixels = 0).request, direct, log = {})
        v.start()
        waitFor("opened") { link.opened != null }
        assertTrue(link.opened!!.inBand)
        Thread.sleep(50)
        assertEquals(null, link.dialed)
        v.stop()
    }

    @Test fun keyframesAreAskedForOverQuic() {
        val link = FakeLink()
        val v = VideoStream(link, VideoStream.pane(link, "p1").request, direct, log = {})
        v.setOutput(factory)
        v.start()
        waitFor("dialed") { link.dialed != null }
        // A frame before any keyframe: one's asked for.
        link.session.records += record(5, key = false)
        waitFor("asked") { link.session.keyframes > 0 }
        assertTrue(link.sent.none { it.hasMediaAck() && it.mediaAck.keyframe })
        v.stop()
    }

    @Test fun theVideoEndingFailsTheStream() {
        val link = FakeLink()
        val v = VideoStream(link, VideoStream.pane(link, "p1").request, direct, log = {})
        v.start()
        waitFor("dialed") { link.dialed != null }
        link.session.ended = "the pane closed"
        waitFor("failed") { v.state.value is VideoStream.State.Failed }
        assertEquals("the pane closed", (v.state.value as VideoStream.State.Failed).reason)
    }

    @Test fun quicFailingTurnsItOffForTheConnection() {
        val link = FakeLink(fails = true)
        val v = VideoStream(link, VideoStream.pane(link, "p1").request, direct, log = {})
        v.start()
        waitFor("failed") { v.state.value is VideoStream.State.Failed }
        assertTrue(link.failedQuic)
    }

    @Test fun hostOfAddresses() {
        assertEquals("100.64.0.2", ConnectionLink.hostOf("100.64.0.2:7443"))
        assertEquals("fd7a::1", ConnectionLink.hostOf("[fd7a::1]:7443"))
        assertEquals("studio.example.ts.net", ConnectionLink.hostOf("studio.example.ts.net:7443"))
        assertEquals("fd7a::1", ConnectionLink.hostOf("fd7a::1"))
    }
}
