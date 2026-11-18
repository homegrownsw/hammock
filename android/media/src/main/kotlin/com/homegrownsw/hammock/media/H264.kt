package com.homegrownsw.hammock.media

/**
 * H.264 as the daemon sends it: AVCC, each NAL unit after its length as a
 * 4-byte big-endian number, a keyframe leading with its SPS and PPS. MediaCodec
 * wants Annex-B (each NAL after a 00 00 00 01 start code) and the parameter
 * sets as csd-0 and csd-1.
 */
object H264 {
    const val NAL_IDR = 5
    const val NAL_SPS = 7
    const val NAL_PPS = 8

    private val START = byteArrayOf(0, 0, 0, 1)

    /** A NAL unit inside a frame's bytes: where its payload starts and ends (exclusive), and its type. */
    data class Nal(val start: Int, val end: Int, val type: Int)

    /**
     * The NAL units of an AVCC frame. Stops at the first length that's zero or
     * runs past the end (a truncated frame): what came before is still good.
     */
    fun nals(avcc: ByteArray): List<Nal> {
        val out = ArrayList<Nal>(4)
        var i = 0
        while (i + 4 <= avcc.size) {
            val n = ((avcc[i].toInt() and 0xff) shl 24) or ((avcc[i + 1].toInt() and 0xff) shl 16) or
                ((avcc[i + 2].toInt() and 0xff) shl 8) or (avcc[i + 3].toInt() and 0xff)
            val start = i + 4
            // n is signed here: a length of 2 GB or more is as bad as one past the end.
            if (n <= 0 || n > avcc.size - start) break
            out += Nal(start, start + n, avcc[start].toInt() and 0x1f)
            i = start + n
        }
        return out
    }

    /**
     * The frame as Annex-B. A 4-byte length and a 4-byte start code are the
     * same size, so this is the frame with each length overwritten; anything
     * after a bad length is left off.
     */
    fun annexB(avcc: ByteArray): ByteArray {
        val units = nals(avcc)
        if (units.isEmpty()) return ByteArray(0)
        val out = avcc.copyOf(units.last().end)
        for (u in units) START.copyInto(out, u.start - 4)
        return out
    }

    /** A keyframe's parameter sets, without start codes. */
    data class ParameterSets(val sps: ByteArray, val pps: ByteArray) {
        /** As MediaCodec's csd-0 and csd-1 take them: each after a start code. */
        val csd0: ByteArray get() = START + sps
        val csd1: ByteArray get() = START + pps

        override fun equals(other: Any?) = other is ParameterSets && sps.contentEquals(other.sps) && pps.contentEquals(other.pps)
        override fun hashCode() = 31 * sps.contentHashCode() + pps.contentHashCode()
    }

    /** The SPS and PPS a frame carries (a keyframe's), or null without both. The last of each wins. */
    fun parameterSets(avcc: ByteArray): ParameterSets? {
        var sps: ByteArray? = null
        var pps: ByteArray? = null
        for (u in nals(avcc)) {
            when (u.type) {
                NAL_SPS -> sps = avcc.copyOfRange(u.start, u.end)
                NAL_PPS -> pps = avcc.copyOfRange(u.start, u.end)
            }
        }
        return if (sps != null && pps != null) ParameterSets(sps, pps) else null
    }

    /** Whether a frame has a picture to decode (anything but parameter sets). */
    fun hasPicture(avcc: ByteArray): Boolean = nals(avcc).any { it.type != NAL_SPS && it.type != NAL_PPS }
}
