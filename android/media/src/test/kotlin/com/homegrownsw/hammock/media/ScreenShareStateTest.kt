package com.homegrownsw.hammock.media

import com.homegrownsw.hammock.media.ScreenShareState.Phase
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class ScreenShareStateTest {
    @Test fun opensMinimisesAndComesBack() {
        var s = ScreenShareState()
        assertFalse(s.isShowing)
        s = s.open("us_studio", "Studio")
        assertEquals(Phase.OPEN, s.phase)
        val session = s.session
        s = s.minimise()
        assertTrue(s.isMinimised && s.isShowing)
        // Back from the bar: the same session, so the same streams.
        s = s.expand()
        assertEquals(Phase.OPEN, s.phase)
        assertEquals(session, s.session)
        s = s.minimise().open("us_studio", "Studio")
        assertEquals(Phase.OPEN, s.phase)
        assertEquals(session, s.session)
    }

    @Test fun anotherComputerReplacesIt() {
        val s = ScreenShareState().open("us_studio", "Studio").minimise()
        val t = s.open("us_laptop", "Laptop")
        assertEquals(Phase.OPEN, t.phase)
        assertEquals("us_laptop", t.computerID)
        assertEquals(s.session + 1, t.session)
    }

    @Test fun endingLeavesNothing() {
        var s = ScreenShareState().open("us_studio", "Studio").minimise().end()
        assertEquals(Phase.ENDED, s.phase)
        assertEquals("", s.computerID)
        assertEquals(Phase.ENDED, s.minimise().expand().phase)
        val session = s.session
        s = s.open("us_studio", "Studio")
        assertEquals(session + 1, s.session)
    }

    @Test fun onlyOpenMinimisesAndOnlyMinimisedExpands() {
        assertEquals(Phase.ENDED, ScreenShareState().expand().phase)
        assertEquals(Phase.OPEN, ScreenShareState().open("us_studio", "Studio").expand().phase)
    }

    @Test fun fewFramesWhileMinimised() {
        assertTrue(ScreenShareState.detailFPS(true) <= 2)
        assertTrue(ScreenShareState.baseFPS(true) <= 2)
        assertEquals(60, ScreenShareState.detailFPS(false))
        assertEquals(15, ScreenShareState.baseFPS(false))
    }
}
