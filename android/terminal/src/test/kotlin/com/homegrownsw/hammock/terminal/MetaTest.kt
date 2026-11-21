package com.homegrownsw.hammock.terminal

import org.junit.Assert.assertArrayEquals
import org.junit.Test

/** The key bar's alt: Esc, then the key, as PhoneTerminalKeysTests.swift checks the iPhone's. */
class MetaTest {
    @Test
    fun metaIsEscFirst() {
        assertArrayEquals("\u001bb".toByteArray(), meta("b".toByteArray()))
        assertArrayEquals(byteArrayOf(0x1B, 0x7F), meta(byteArrayOf(0x7F)))
    }
}
