package com.homegrownsw.hammock.media

import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class H264Test {
    private val sps = byteArrayOf(0x67, 0x42, 0x00, 0x1f, 0x11)
    private val pps = byteArrayOf(0x68, 0xce.toByte(), 0x3c, 0x80.toByte())
    private val idr = byteArrayOf(0x65, 0x88.toByte(), 0x84.toByte(), 0x00, 0x33)
    private val slice = byteArrayOf(0x41, 0x9a.toByte(), 0x02)

    private fun avcc(vararg units: ByteArray): ByteArray {
        var out = ByteArray(0)
        for (u in units) {
            val n = u.size
            out += byteArrayOf((n ushr 24).toByte(), (n ushr 16).toByte(), (n ushr 8).toByte(), n.toByte()) + u
        }
        return out
    }

    private val start = byteArrayOf(0, 0, 0, 1)

    @Test fun splitsNalUnitsWithTheirTypes() {
        val nals = H264.nals(avcc(sps, pps, idr))
        assertEquals(listOf(H264.NAL_SPS, H264.NAL_PPS, H264.NAL_IDR), nals.map { it.type })
        assertEquals(4, nals[0].start)
        assertEquals(4 + sps.size, nals[0].end)
    }

    @Test fun annexBReplacesEachLengthWithAStartCode() {
        val out = H264.annexB(avcc(sps, pps, idr))
        assertArrayEquals(start + sps + start + pps + start + idr, out)
    }

    @Test fun annexBOfAPFrame() {
        assertArrayEquals(start + slice, H264.annexB(avcc(slice)))
    }

    @Test fun aLengthPastTheEndStopsThere() {
        val good = avcc(slice)
        val bad = good + byteArrayOf(0, 0, 1, 0) + byteArrayOf(1, 2, 3)
        assertEquals(1, H264.nals(bad).size)
        assertArrayEquals(start + slice, H264.annexB(bad))
    }

    @Test fun aZeroOrHugeLengthStops() {
        assertTrue(H264.nals(byteArrayOf(0, 0, 0, 0, 0x65)).isEmpty())
        assertTrue(H264.nals(byteArrayOf(0xff.toByte(), 0xff.toByte(), 0xff.toByte(), 0xff.toByte(), 0x65)).isEmpty())
        assertTrue(H264.nals(byteArrayOf(0, 0, 1)).isEmpty())
        assertEquals(0, H264.annexB(ByteArray(0)).size)
    }

    @Test fun parameterSetsFromAKeyframe() {
        val sets = H264.parameterSets(avcc(sps, pps, idr))!!
        assertArrayEquals(sps, sets.sps)
        assertArrayEquals(pps, sets.pps)
        assertArrayEquals(start + sps, sets.csd0)
        assertArrayEquals(start + pps, sets.csd1)
        assertEquals(sets, H264.parameterSets(avcc(sps, pps, slice)))
    }

    @Test fun noParameterSetsWithoutBoth() {
        assertNull(H264.parameterSets(avcc(slice)))
        assertNull(H264.parameterSets(avcc(sps, idr)))
    }

    @Test fun differentParameterSetsDiffer() {
        val other = byteArrayOf(0x67, 0x64, 0x00, 0x28)
        assertFalse(H264.parameterSets(avcc(sps, pps, idr)) == H264.parameterSets(avcc(other, pps, idr)))
    }

    @Test fun pictureOrNot() {
        assertTrue(H264.hasPicture(avcc(sps, pps, idr)))
        assertFalse(H264.hasPicture(avcc(sps, pps)))
    }
}
