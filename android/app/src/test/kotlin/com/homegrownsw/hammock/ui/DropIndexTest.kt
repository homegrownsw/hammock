package com.homegrownsw.hammock.ui

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Dragging a tab's handle in Edit mode: where it lands in its Group. */
class DropIndexTest {
    // A Group's header, then its three tabs, 100 pixels each (the second taller), then the next Group's.
    private val rows = listOf(
        RowSpan("g:a", 0, 60), RowSpan("t:1", 60, 100), RowSpan("t:2", 160, 140), RowSpan("t:3", 300, 100), RowSpan("g:b", 400, 60),
    )
    private val keys = listOf("t:1", "t:2", "t:3")

    @Test fun itLandsWhereItsMiddleIs() {
        assertEquals(0, dropIndex(rows, keys, "t:1", 0f))
        assertEquals(1, dropIndex(rows, keys, "t:1", 120f))
        assertEquals(2, dropIndex(rows, keys, "t:1", 200f))
        assertEquals(0, dropIndex(rows, keys, "t:3", -250f))
    }

    @Test fun pastTheGroupsEndsItStaysInIt() {
        assertEquals(2, dropIndex(rows, keys, "t:1", 1000f))
        assertEquals(0, dropIndex(rows, keys, "t:3", -1000f))
    }

    @Test fun offScreenItGoesNowhere() {
        assertNull(dropIndex(rows.drop(2), keys, "t:1", 50f))
    }
}
