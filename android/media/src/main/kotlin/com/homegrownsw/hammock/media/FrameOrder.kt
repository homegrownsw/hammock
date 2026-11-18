package com.homegrownsw.hammock.media

import java.util.TreeMap

/** One frame of a stream, as it arrived. Crop: the part of the display it shows (fractions; the whole for a pane). */
class VideoFrame(
    val seq: Long,
    val key: Boolean,
    val width: Int,
    val height: Int,
    val data: ByteArray,
    /** Predicted from a frame the viewer has (a long-term reference): frames before it may be skipped. */
    val refresh: Boolean = false,
    val hevc: Boolean = false,
    val crop: Crop = Crop.WHOLE,
)

/** A part of a display, as fractions of it. */
data class Crop(val x: Float, val y: Float, val w: Float, val h: Float) {
    companion object { val WHOLE = Crop(0f, 0f, 1f, 1f) }
}

/**
 * Puts frames in decoding order, as the iPhone's MediaStream does: each
 * reaches the decoder after the one it references. One that comes early
 * waits; a keyframe (or a refresh, which references a frame the decoder has)
 * starts again from itself; frames whose predecessor never comes are given up
 * after a second, or 240 of them, and a keyframe asked for.
 *
 * Not thread-safe: the stream calls it from its own thread. Times in ms.
 */
class FrameOrder(private val now: () -> Long) {
    /**
     * What to do with a frame: decode these, in order; and ask for a keyframe
     * if `keyframe` says why. A reminder (`again`) is for one asked for
     * already that hasn't come: asked again less often, since a keyframe is
     * big and slow on a poor link.
     */
    data class Step(val decode: List<VideoFrame>, val keyframe: String? = null, val again: Boolean = false)

    var lastSeq = 0L
        private set
    var started = false
        private set
    /**
     * Frames were thrown away here (not given up by the host): only a keyframe
     * will do, since a refresh may reference one of them.
     */
    var needKey = false
        private set
    private val pending = TreeMap<Long, VideoFrame>()
    private var waitingSince: Long? = null

    val waiting: Int get() = pending.size

    fun receive(f: VideoFrame): Step {
        val restart = f.key && (!started || f.seq > lastSeq)
        val refresh = f.refresh && started && !needKey && f.seq > lastSeq
        if (restart || refresh) {
            pending.headMap(f.seq, true).clear()
            started = true
            if (f.key) needKey = false
            lastSeq = f.seq
            return Step(listOf(f) + drain())
        }
        // Nothing to decode from yet: the stream began with a frame that
        // never came (the first frames can beat the handler being set).
        if (!started) return Step(emptyList(), "no keyframe yet", again = true)
        if (f.seq <= lastSeq) return Step(emptyList())
        // Waiting for a keyframe: anything else references frames the
        // decoder no longer has.
        if (needKey) return Step(emptyList(), "waiting for a keyframe", again = true)
        if (f.seq == lastSeq + 1) {
            lastSeq = f.seq
            return Step(listOf(f) + drain())
        }
        pending[f.seq] = f
        val since = waitingSince ?: now().also { waitingSince = it }
        if (pending.size > 240 || now() - since > 1000) {
            val n = pending.size
            pending.clear()
            waitingSince = null
            needKey = true
            return Step(emptyList(), "frame ${lastSeq + 1} missing, $n waiting")
        }
        return Step(emptyList())
    }

    /** The decoder lost its place (an error, a new surface): drop what's waiting, and wait for a keyframe. */
    fun lost() {
        pending.clear()
        waitingSince = null
        needKey = true
    }

    private fun drain(): List<VideoFrame> {
        val out = ArrayList<VideoFrame>()
        while (true) {
            val next = pending.remove(lastSeq + 1) ?: break
            lastSeq = next.seq
            out += next
        }
        waitingSince = if (pending.isEmpty()) null else (waitingSince ?: now())
        return out
    }
}
