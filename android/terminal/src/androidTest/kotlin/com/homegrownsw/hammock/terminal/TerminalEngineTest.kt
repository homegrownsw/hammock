package com.homegrownsw.hammock.terminal

import android.view.KeyEvent
import androidx.test.ext.junit.runners.AndroidJUnit4
import org.junit.Assert.assertArrayEquals
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

@RunWith(AndroidJUnit4::class)
class TerminalEngineTest {
    private fun engine(cols: Int = 20, rows: Int = 5) = TerminalEngine(cols, rows)

    @Test
    fun writesTextAndColours() = engine().use { e ->
        e.write("hello\u001b[31mred\u001b[0m".toByteArray())
        val f = e.frame()
        assertEquals(20, f.cols)
        assertEquals(5, f.rows)
        assertEquals("hellored", f.rowText(0))
        assertEquals("h", f.text(0, 0))
        assertEquals(-1, f.fg(0, 0))
        // SGR 31 is the theme's red (TerminalTheme.Dark.ansi[1]).
        assertEquals(TerminalTheme.Dark.ansi[1], f.fg(5, 0))
        assertEquals("r", f.text(5, 0))
        assertEquals(TerminalTheme.Dark.background, f.background)
        assertTrue(f.cursorVisible)
        assertEquals(8, f.cursorX)
        assertEquals(0, f.cursorY)
    }

    @Test
    fun stylesAndWideCells() = engine().use { e ->
        e.write("\u001b[1;4mB\u001b[0m\u001b[7mI\u001b[0m界x".toByteArray())
        val f = e.frame()
        assertTrue(f.flags(0, 0) and CellFlags.BOLD != 0)
        assertTrue(f.flags(0, 0) and CellFlags.UNDERLINE != 0)
        assertEquals(1, CellFlags.underlineStyle(f.flags(0, 0)))
        assertTrue(f.flags(1, 0) and CellFlags.INVERSE != 0)
        assertEquals("界", f.text(2, 0))
        assertTrue(f.flags(2, 0) and CellFlags.WIDE != 0)
        assertTrue(f.flags(3, 0) and CellFlags.SPACER != 0)
        assertEquals("x", f.text(4, 0))
    }

    @Test
    fun graphemeClusters() = engine().use { e ->
        // A family emoji (ZWJ sequence) and a flag: one cluster each (mode 2027).
        val family = "👨‍👩‍👧"
        e.write("$family|🇬🇧|".toByteArray())
        val f = e.frame()
        assertEquals(family, f.text(0, 0))
        assertTrue(f.codepoint(0, 0) < 0)
        assertEquals("|", f.text(2, 0))
        assertEquals("🇬🇧", f.text(3, 0))
    }

    @Test
    fun resetAndSnapshot() = engine().use { e ->
        e.write("old".toByteArray())
        e.reset(30, 6)
        e.write("new".toByteArray())
        val f = e.frame()
        assertEquals(30, f.cols)
        assertEquals(6, f.rows)
        assertEquals("new", f.rowText(0))
        assertEquals("new\n\n\n\n\n", f.text())
    }

    @Test
    fun dirtyTracking() = engine().use { e ->
        e.frame() // clean
        assertEquals(TerminalFrame.Dirty.NONE, e.frame().dirty)
        e.write("\u001b[3;1Hx".toByteArray())
        val f = e.frame()
        assertTrue(f.dirty != TerminalFrame.Dirty.NONE)
        assertTrue(f.rowDirty(2))
    }

    @Test
    fun scrollback() = engine(rows = 3).use { e ->
        for (i in 1..10) e.write("line $i\r\n".toByteArray())
        assertTrue(e.isAtBottom)
        e.scroll(-3)
        assertFalse(e.isAtBottom)
        assertEquals("line 6", e.frame().rowText(0))
        val sb = e.scrollbar
        assertTrue(sb.total > sb.length)
        e.scrollToBottom()
        assertTrue(e.isAtBottom)
    }

    @Test
    fun keysFollowCursorKeyMode() = engine().use { e ->
        assertArrayEquals("\u001b[A".toByteArray(), e.encodeKey(KeyEvent.KEYCODE_DPAD_UP))
        e.write("\u001b[?1h".toByteArray()) // DECCKM: application cursor keys
        assertTrue(e.decMode(1))
        assertArrayEquals("\u001bOA".toByteArray(), e.encodeKey(KeyEvent.KEYCODE_DPAD_UP))
        assertArrayEquals(byteArrayOf(0x1b), e.encodeKey(KeyEvent.KEYCODE_ESCAPE))
        assertArrayEquals(byteArrayOf(0x03), e.encodeKey(KeyEvent.KEYCODE_C, KeyMods.CTRL, null, 'c'.code))
        assertArrayEquals("a".toByteArray(), e.encodeKey(KeyEvent.KEYCODE_A, 0, "a", 'a'.code))
        assertArrayEquals(byteArrayOf(0x7f), e.encodeKey(KeyEvent.KEYCODE_DEL))
        assertArrayEquals("\u001b[1;5C".toByteArray(), e.encodeKey(KeyEvent.KEYCODE_DPAD_RIGHT, KeyMods.CTRL))
    }

    @Test
    fun kittyKeyboard() = engine().use { e ->
        e.write("\u001b[>1u".toByteArray()) // kitty: disambiguate
        assertArrayEquals("\u001b[27u".toByteArray(), e.encodeKey(KeyEvent.KEYCODE_ESCAPE))
    }

    @Test
    fun paste() = engine().use { e ->
        assertArrayEquals("hi".toByteArray(), e.encodePaste("hi"))
        e.write("\u001b[?2004h".toByteArray())
        assertTrue(e.bracketedPaste)
        assertArrayEquals("\u001b[200~hi\u001b[201~".toByteArray(), e.encodePaste("hi"))
        assertTrue(TerminalEngine.pasteIsSafe("ls"))
        assertFalse(TerminalEngine.pasteIsSafe("rm -rf x\n"))
    }

    @Test
    fun mouse() = engine().use { e ->
        val g = MouseGeometry(width = 200f, height = 100f, cellWidth = 10f, cellHeight = 20f, padding = 0f)
        assertFalse(e.mouseTracking)
        assertNull(e.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, 15f, 25f, 0, g))
        e.write("\u001b[?1000h\u001b[?1006h".toByteArray())
        assertTrue(e.mouseTracking)
        val press = e.encodeMouse(MouseAction.PRESS, MouseButton.LEFT, 15f, 25f, 0, g)
        assertNotNull(press)
        assertEquals("\u001b[<0;2;2M", String(press!!))
        assertEquals("\u001b[<64;1;1M", String(e.encodeMouse(MouseAction.PRESS, MouseButton.WHEEL_UP, 1f, 1f, 0, g)!!))
    }

    @Test
    fun alternateScreenAndTitle() = engine().use { e ->
        assertFalse(e.isAlternateScreen)
        e.write("\u001b[?1049h\u001b]2;hello ✨\u0007".toByteArray())
        assertTrue(e.isAlternateScreen)
        assertEquals("hello ✨", e.title)
    }

    @Test
    fun themeSurvivesReset() = engine().use { e ->
        e.theme = TerminalTheme.Light
        e.reset(20, 5)
        assertEquals(TerminalTheme.Light.background, e.frame().background)
    }

    @Test
    fun sessionFeedsEngine() = TerminalSession("p1").use { s ->
        var changes = 0
        s.observe { changes++ }
        assertFalse(s.isReady)
        s.snapshot(40, 10, "\u001b[32mok".toByteArray())
        assertTrue(s.isReady)
        s.output(" more".toByteArray())
        s.resized(50, 12)
        assertEquals(3, changes)
        val f = s.engine.frame()
        assertEquals(50, f.cols)
        assertEquals("ok more", f.rowText(0))
    }
}
