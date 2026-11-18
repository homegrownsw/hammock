package com.homegrownsw.hammock.terminal

import android.graphics.Bitmap
import android.view.View
import android.view.ViewGroup
import android.view.inputmethod.EditorInfo
import androidx.test.core.app.ActivityScenario
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import java.io.File
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertTrue
import org.junit.Test
import org.junit.runner.RunWith

/**
 * The view in an activity: input through the keyboard's connection, the
 * grid it reports, and screenshots (saved to the app's external files, for
 * looking at: `adb pull /sdcard/Android/data/<package>/files/`).
 */
@RunWith(AndroidJUnit4::class)
class TerminalViewTest {
    private val instrumentation = InstrumentationRegistry.getInstrumentation()

    private fun <T> onMain(fn: () -> T): T {
        var r: T? = null
        instrumentation.runOnMainSync { r = fn() }
        @Suppress("UNCHECKED_CAST")
        return r as T
    }

    private fun waitFor(timeoutMs: Long = 5000, cond: () -> Boolean) {
        val end = System.currentTimeMillis() + timeoutMs
        while (!cond() && System.currentTimeMillis() < end) Thread.sleep(50)
    }

    private fun findView(a: TerminalTestActivity): TerminalView? {
        fun find(v: View): TerminalView? = when (v) {
            is TerminalView -> v
            is ViewGroup -> (0 until v.childCount).firstNotNullOfOrNull { find(v.getChildAt(it)) }
            else -> null
        }
        return find(a.window.decorView)
    }

    private fun screenshot(name: String) {
        instrumentation.waitForIdleSync()
        Thread.sleep(500)
        val shot = instrumentation.uiAutomation.takeScreenshot() ?: return
        val dir = instrumentation.targetContext.getExternalFilesDir(null) ?: return
        File(dir, "$name.png").outputStream().use { shot.compress(Bitmap.CompressFormat.PNG, 100, it) }
    }

    @Test
    fun drawsAndTakesInput() {
        ActivityScenario.launch(TerminalTestActivity::class.java).use { scenario ->
            lateinit var activity: TerminalTestActivity
            scenario.onActivity { activity = it }
            waitFor { onMain { findView(activity)?.drawn } != null }
            val view = onMain { findView(activity) }
            assertNotNull(view)
            view!!
            // The pane is 60 columns: the font shrinks to fit them across.
            val drawn = view.drawn!!
            assertEquals(60, drawn.cols)
            assertTrue(drawn.originX + 60 * drawn.cellWidth <= view.width + 1)
            // Active: it reported the grid that fits at the chosen size.
            waitFor { activity.resizes.isNotEmpty() }
            val r = activity.resizes.last()
            assertTrue("cols ${r[0]}", r[0] >= 10)
            assertTrue("rows ${r[1]}", r[1] >= 4)
            screenshot("terminal")

            fun typed(): String = activity.sent.joinToString("") { String(it, Charsets.UTF_8) }
            onMain {
                view.requestFocus()
                val ic = view.onCreateInputConnection(EditorInfo())!!
                ic.commitText("ls", 1)
                // Composing sends as it goes, then revises.
                ic.setComposingText("he", 1)
                ic.setComposingText("hel", 1)
                ic.setComposingText("help", 1)
                ic.commitText("hello", 1)
                ic.deleteSurroundingText(1, 0)
                ic.commitText("\n", 1)
                // A delete past the line: the rest is the terminal's.
                ic.deleteSurroundingText(2, 0)
                // A sticky ctrl from the bar.
                view.setCtrlLatched(true)
                ic.commitText("c", 1)
                view.pressKey(TerminalKey.UP)
                view.pressKey(TerminalKey.ESCAPE)
            }
            val del = "\u007f"
            assertEquals("lshe" + "l" + "p" + del + "lo" + del + "\r" + del + del + "\u0003" + "\u001b[A" + "\u001b", typed())
            assertTrue(!view.ctrlLatched)

            // The keyboard and the key bar above it.
            onMain { view.showKeyboard() }
            waitFor { onMain { view.keyboardVisible } }
            Thread.sleep(800)
            screenshot("terminal-keyboard")
        }
    }

    @Test
    fun dragScrollsBackAndTapClicks() {
        ActivityScenario.launch(TerminalTestActivity::class.java).use { scenario ->
            lateinit var activity: TerminalTestActivity
            scenario.onActivity { activity = it }
            waitFor { onMain { findView(activity)?.drawn } != null }
            val view = onMain { findView(activity) }!!
            for (i in 1..200) activity.session.output("line $i\r\n".toByteArray())
            assertTrue(activity.session.engine.isAtBottom)
            // Sent to the view itself: injected input lands on whatever window
            // is in front, and other apps share the emulator.
            val x = view.width / 2f
            val y0 = view.height * 0.3f
            fun touch(e: android.view.MotionEvent) = onMain { view.dispatchTouchEvent(e); e.recycle() }
            // A finger moving down shows earlier lines.
            val t = android.os.SystemClock.uptimeMillis()
            fun ev(action: Int, y: Float, at: Long) = android.view.MotionEvent.obtain(t, at, action, x, y, 0)
            touch(ev(android.view.MotionEvent.ACTION_DOWN, y0, t))
            for (k in 1..20) touch(ev(android.view.MotionEvent.ACTION_MOVE, y0 + k * 30f, t + k * 16L))
            touch(ev(android.view.MotionEvent.ACTION_UP, y0 + 600f, t + 340L))
            Thread.sleep(300)
            assertTrue(!activity.session.engine.isAtBottom)
            assertTrue(activity.sent.isEmpty())

            // A program tracking the mouse gets a tap as a click (once the fling has stopped).
            Thread.sleep(1500)
            activity.sent.clear()
            activity.session.output("\u001b[?1000h\u001b[?1006h".toByteArray())
            val t2 = android.os.SystemClock.uptimeMillis()
            touch(android.view.MotionEvent.obtain(t2, t2, android.view.MotionEvent.ACTION_DOWN, x, y0, 0))
            touch(android.view.MotionEvent.obtain(t2, t2 + 50, android.view.MotionEvent.ACTION_UP, x, y0, 0))
            waitFor { activity.sent.size >= 2 }
            val clicks = activity.sent.joinToString("") { String(it) }
            assertTrue(clicks, clicks.startsWith("\u001b[<0;") && clicks.contains("m"))
        }
    }
}
