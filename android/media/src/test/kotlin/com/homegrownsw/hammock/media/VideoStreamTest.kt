package com.homegrownsw.hammock.media

import com.google.protobuf.ByteString
import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.util.Collections
import java.util.concurrent.Executor

class VideoStreamTest {
    private class FakeLink : MediaLink {
        val sent: MutableList<ClientMessage> = Collections.synchronizedList(ArrayList())
        val handlers = HashMap<String, (ServerMessage) -> Unit>()
        var opened: Hammock.OpenMedia? = null
        override suspend fun open(request: Hammock.OpenMedia): Hammock.MediaTicket {
            opened = request
            return Hammock.MediaTicket.newBuilder().setSessionId("ms_1").build()
        }
        override fun send(build: ClientMessage.Builder.() -> Unit) {
            sent += ClientMessage.newBuilder().apply(build).build()
        }
        override fun setHandler(sessionID: String, handler: ((ServerMessage) -> Unit)?) {
            synchronized(handlers) { if (handler == null) handlers.remove(sessionID) else handlers[sessionID] = handler }
        }
        fun deliver(msg: ServerMessage) = synchronized(handlers) { handlers["ms_1"] }!!.invoke(msg)
        fun acks() = sent.filter { it.hasMediaAck() && !it.mediaAck.keyframe }.flatMap { it.mediaAck.seqsList }
        fun keyframeRequests() = sent.count { it.hasMediaAck() && it.mediaAck.keyframe }
    }

    private class FakeDecoder(val config: VideoConfig, val fail: () -> Boolean) : VideoDecoder {
        val frames = ArrayList<ByteArray>()
        var released = false
        override fun decode(annexB: ByteArray, key: Boolean, ptsUs: Long): Boolean {
            if (fail()) return false
            frames += annexB
            return true
        }
        override fun release() { released = true }
    }

    private val direct = Executor { it.run() }
    private var now = 10_000L
    private val link = FakeLink()
    private val decoders = ArrayList<FakeDecoder>()
    private var failing = false
    private val factory = DecoderFactory { c, _ -> FakeDecoder(c) { failing }.also { decoders += it } }

    private val sps = byteArrayOf(0x67, 0x42, 0x00, 0x1f)
    private val pps = byteArrayOf(0x68, 0xce.toByte(), 0x3c, 0x80.toByte())
    private val idr = byteArrayOf(0x65, 0x11, 0x22)
    private val slice = byteArrayOf(0x41, 0x33)

    private fun avcc(vararg units: ByteArray): ByteArray {
        var out = ByteArray(0)
        for (u in units) out += byteArrayOf(0, 0, 0, u.size.toByte()) + u
        return out
    }

    private fun frame(seq: Int, key: Boolean = false, width: Int = 100, height: Int = 50, refresh: Boolean = false): ServerMessage {
        val data = if (key) avcc(sps, pps, idr) else avcc(slice)
        return ServerMessage.newBuilder().setMediaFrame(
            Hammock.MediaFrame.newBuilder().setSessionId("ms_1").setSeq(seq).setKeyframe(key).setRefresh(refresh)
                .setWidth(width).setHeight(height).setData(ByteString.copyFrom(data)),
        ).build()
    }

    private fun started(request: Hammock.OpenMedia = Hammock.OpenMedia.newBuilder().setPaneId("p1").setInBand(true).build()): VideoStream {
        val s = VideoStream(link, request, direct, clock = { now }, log = {})
        s.setOutput(factory)
        s.start()
        val deadline = System.currentTimeMillis() + 3000
        while (s.sessionID.isEmpty() && System.currentTimeMillis() < deadline) Thread.sleep(5)
        assertEquals("ms_1", s.sessionID)
        return s
    }

    @Test fun opensInBandAndAcksEveryFrameAsItComes() {
        val v = started(VideoStream.pane(link, "p1", maxPixels = 1000).request)
        assertTrue(link.opened!!.inBand)
        assertEquals("p1", link.opened!!.paneId)
        assertEquals(1000, link.opened!!.maxPixels)
        link.deliver(frame(1, key = true))
        link.deliver(frame(3))
        link.deliver(frame(2))
        // Acked as they arrive, whatever their order.
        assertEquals(listOf(1, 3, 2), link.acks())
        // Decoded in order, as Annex-B, the keyframe with its parameter sets.
        val start = byteArrayOf(0, 0, 0, 1)
        assertEquals(3, decoders.single().frames.size)
        assertArrayEquals(start + sps + start + pps + start + idr, decoders.single().frames[0])
        assertArrayEquals(start + slice, decoders.single().frames[2])
        assertArrayEquals(sps, decoders.single().config.sets.sps)
        assertEquals(VideoStream.State.Playing, v.state.value)
        assertEquals(VideoStream.Size(100, 50), v.size.value)
    }

    @Test fun aScreenAsksForH264Only() {
        val s = VideoStream.screen(link, display = 2, computerID = "us_x", maxPixels = 500, maxFPS = 15, background = true)
        s.start()
        val deadline = System.currentTimeMillis() + 3000
        while (link.opened == null && System.currentTimeMillis() < deadline) Thread.sleep(5)
        val o = link.opened!!
        assertTrue(o.screen && o.inBand && o.background)
        assertEquals(2, o.displayId)
        assertEquals("us_x", o.computerId)
        assertEquals(15, o.maxFps)
        assertEquals(0, o.codecsCount) // no hevc444
        s.stop()
    }

    @Test fun aMissingFrameAsksForAKeyframeOnceAndNotTooOften() {
        started()
        link.deliver(frame(1, key = true))
        link.deliver(frame(3))
        now += 1001
        link.deliver(frame(4))
        assertEquals(1, link.keyframeRequests())
        // Waiting for it: reminders no more than every 1.5 s.
        now += 1000
        link.deliver(frame(5))
        assertEquals(1, link.keyframeRequests())
        now += 600
        link.deliver(frame(6))
        assertEquals(2, link.keyframeRequests())
        link.deliver(frame(7, key = true))
        assertEquals(2, decoders.single().frames.size)
    }

    @Test fun aNewSizeMakesANewDecoder() {
        started()
        link.deliver(frame(1, key = true, width = 100, height = 50))
        link.deliver(frame(2))
        link.deliver(frame(3, key = true, width = 200, height = 100))
        assertEquals(2, decoders.size)
        assertTrue(decoders[0].released)
        assertEquals(200, decoders[1].config.width)
        // The same parameter sets and size again: the same decoder.
        link.deliver(frame(4, key = true, width = 200, height = 100))
        assertEquals(2, decoders.size)
    }

    @Test fun aDecoderThatFailsIsReplacedFromTheNextKeyframe() {
        started()
        link.deliver(frame(1, key = true))
        failing = true
        link.deliver(frame(2))
        assertTrue(decoders[0].released)
        assertEquals(1, link.keyframeRequests())
        failing = false
        link.deliver(frame(3))
        assertEquals(1, decoders.size)
        link.deliver(frame(4, key = true))
        assertEquals(2, decoders.size)
        assertEquals(1, decoders[1].frames.size)
    }

    @Test fun offScreenFramesAreDroppedAndAKeyframeAskedForOnReturn() {
        val v = started()
        link.deliver(frame(1, key = true))
        v.setOutput(null)
        assertTrue(decoders[0].released)
        link.deliver(frame(2))
        now += 5000
        link.deliver(frame(3))
        assertEquals(0, link.keyframeRequests())
        v.setOutput(factory)
        assertEquals(1, link.keyframeRequests())
        link.deliver(frame(4, key = true))
        assertEquals(1, decoders[1].frames.size)
    }

    @Test fun releasingAnotherViewsOutputLeavesThisOne() {
        val v = started()
        val other = DecoderFactory { _, _ -> null }
        v.releaseOutput(other)
        link.deliver(frame(1, key = true))
        assertEquals(1, decoders.single().frames.size)
    }

    @Test fun theViewIsSentOnceTheSessionOpens() {
        val v = VideoStream(link, Hammock.OpenMedia.newBuilder().setScreen(true).setInBand(true).build(), direct, clock = { now }, log = {})
        v.setView(0.25f, 0.25f, 0.5f, 0.5f, 0.5f)
        assertTrue(link.sent.isEmpty())
        v.start()
        val deadline = System.currentTimeMillis() + 3000
        while (link.sent.none { it.hasSetMediaView() } && System.currentTimeMillis() < deadline) Thread.sleep(5)
        val view = link.sent.single { it.hasSetMediaView() }.setMediaView
        assertEquals("ms_1", view.sessionId)
        assertEquals(0.5f, view.w)
        assertEquals(0.5f, view.aspect)
    }

    @Test fun mediaEndedFailsAndStopCloses() {
        val v = started()
        link.deliver(ServerMessage.newBuilder().setMediaEnded(Hammock.MediaEnded.newBuilder().setSessionId("ms_1").setReason("The pane closed.")).build())
        assertEquals(VideoStream.State.Failed("The pane closed."), v.state.value)
        v.stop()
        assertTrue(link.sent.any { it.hasCloseMedia() && it.closeMedia.sessionId == "ms_1" })
        assertFalse(link.handlers.containsKey("ms_1"))
    }

    @Test fun aScreensPointerAndLock() {
        val v = started()
        val png = ByteString.copyFrom(byteArrayOf(1, 2, 3))
        link.deliver(ServerMessage.newBuilder().setScreenCursor(Hammock.ScreenCursor.newBuilder().setSessionId("ms_1").setX(0.5f).setVisible(true).setShapeId(7).setPng(png)).build())
        link.deliver(ServerMessage.newBuilder().setScreenCursor(Hammock.ScreenCursor.newBuilder().setSessionId("ms_1").setX(0.6f).setVisible(true).setShapeId(7)).build())
        // The shape's image comes once, and stays with it.
        assertArrayEquals(byteArrayOf(1, 2, 3), v.pointer.value!!.png)
        assertEquals(0.6f, v.pointer.value!!.x)
        link.deliver(ServerMessage.newBuilder().setScreenState(Hammock.ScreenState.newBuilder().setSessionId("ms_1").setLocked(true)).build())
        assertTrue(v.locked.value)
    }

    @Test fun aCroppedFrameSaysWhatPartItShows() {
        val v = started()
        val f = frame(1, key = true).mediaFrame.toBuilder().setCropX(0.25f).setCropY(0.5f).setCropW(0.5f).setCropH(0.25f)
        link.deliver(ServerMessage.newBuilder().setMediaFrame(f).build())
        assertEquals(Crop(0.25f, 0.5f, 0.5f, 0.25f), v.crop.value)
        link.deliver(frame(2))
        assertEquals(Crop.WHOLE, v.crop.value)
        assertNull(v.pointer.value)
    }

    /** A decoder that says when it shows each picture, as MediaCodec's does. */
    private class ShowingDecoder : VideoDecoder {
        val queued = ArrayList<Long>()
        var shown: ((Long) -> Unit)? = null
        override fun decode(annexB: ByteArray, key: Boolean, ptsUs: Long): Boolean {
            queued += ptsUs
            return true
        }
        override fun release() {}
        override fun onShown(shown: (ptsUs: Long) -> Unit): Boolean {
            this.shown = shown
            return true
        }
    }

    @Test fun aCroppedFrameTakesItsPlaceAsItsShownNotBefore() {
        val d = ShowingDecoder()
        val v = VideoStream(link, Hammock.OpenMedia.newBuilder().setScreen(true).setInBand(true).build(), direct, clock = { now }, log = {})
        v.setOutput { _, _ -> d }
        v.start()
        val deadline = System.currentTimeMillis() + 3000
        while (v.sessionID.isEmpty() && System.currentTimeMillis() < deadline) Thread.sleep(5)
        val f = frame(1, key = true).mediaFrame.toBuilder().setCropX(0.25f).setCropY(0.5f).setCropW(0.5f).setCropH(0.25f)
        link.deliver(ServerMessage.newBuilder().setMediaFrame(f).build())
        link.deliver(frame(2))
        assertEquals(listOf(1L, 2L), d.queued)
        // Queued, not shown yet: the last picture is still where it was.
        assertEquals(Crop.WHOLE, v.crop.value)
        d.shown!!(1)
        assertEquals(Crop(0.25f, 0.5f, 0.5f, 0.25f), v.crop.value)
        d.shown!!(2)
        assertEquals(Crop.WHOLE, v.crop.value)
        // A late report of one already passed changes nothing.
        d.shown!!(1)
        assertEquals(Crop.WHOLE, v.crop.value)
    }
}
