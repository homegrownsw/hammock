package com.homegrownsw.hammock.media

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class FrameOrderTest {
    private var now = 0L
    private val order = FrameOrder { now }

    private fun frame(seq: Long, key: Boolean = false, refresh: Boolean = false) = VideoFrame(seq, key, 100, 100, ByteArray(0), refresh = refresh)
    private fun seqs(step: FrameOrder.Step) = step.decode.map { it.seq }

    @Test fun waitsForAKeyframeToStart() {
        val s = order.receive(frame(1))
        assertTrue(s.decode.isEmpty())
        assertNotNull(s.keyframe)
        assertTrue(s.again)
        assertEquals(listOf(2L), seqs(order.receive(frame(2, key = true))))
        assertEquals(listOf(3L), seqs(order.receive(frame(3))))
    }

    @Test fun anEarlyFrameWaitsForTheOneBefore() {
        order.receive(frame(1, key = true))
        assertTrue(order.receive(frame(3)).decode.isEmpty())
        assertTrue(order.receive(frame(4)).decode.isEmpty())
        assertEquals(2, order.waiting)
        assertEquals(listOf(2L, 3L, 4L), seqs(order.receive(frame(2))))
        assertEquals(0, order.waiting)
        assertEquals(4L, order.lastSeq)
    }

    @Test fun oldAndRepeatedFramesAreIgnored() {
        order.receive(frame(5, key = true))
        val s = order.receive(frame(5))
        assertTrue(s.decode.isEmpty())
        assertNull(s.keyframe)
        assertTrue(order.receive(frame(3)).decode.isEmpty())
    }

    @Test fun aGapGivenUpAfterASecondAsksForAKeyframe() {
        order.receive(frame(1, key = true))
        order.receive(frame(3))
        now = 500
        assertNull(order.receive(frame(4)).keyframe)
        now = 1001
        val s = order.receive(frame(5))
        assertNotNull(s.keyframe)
        assertFalse(s.again)
        assertTrue(order.needKey)
        assertEquals(0, order.waiting)
        // Until it comes, only a keyframe will do (a refresh may reference what was thrown away).
        val waiting = order.receive(frame(6, refresh = true))
        assertTrue(waiting.decode.isEmpty())
        assertTrue(waiting.again)
        assertEquals(listOf(7L), seqs(order.receive(frame(7, key = true))))
        assertFalse(order.needKey)
    }

    @Test fun tooManyWaitingAsksForAKeyframe() {
        order.receive(frame(1, key = true))
        var asked: String? = null
        for (i in 3L..250L) asked = order.receive(frame(i)).keyframe ?: asked
        assertNotNull(asked)
        assertTrue(order.needKey)
    }

    @Test fun aKeyframeSkipsTheGapAndDropsWhatWaitedBeforeIt() {
        order.receive(frame(1, key = true))
        order.receive(frame(3))
        order.receive(frame(6))
        assertEquals(listOf(5L, 6L), seqs(order.receive(frame(5, key = true))))
        assertEquals(0, order.waiting)
    }

    @Test fun aRefreshSkipsFramesTheHostGaveUp() {
        order.receive(frame(1, key = true))
        order.receive(frame(2))
        // 3 and 4 given up by the host; 5 references a frame the decoder has.
        assertEquals(listOf(5L), seqs(order.receive(frame(5, refresh = true))))
        assertEquals(listOf(6L), seqs(order.receive(frame(6))))
    }

    @Test fun lostWaitsForAKeyframe() {
        order.receive(frame(1, key = true))
        order.receive(frame(3))
        order.lost()
        assertEquals(0, order.waiting)
        assertTrue(order.receive(frame(2)).decode.isEmpty())
        assertEquals(listOf(4L), seqs(order.receive(frame(4, key = true))))
    }
}
