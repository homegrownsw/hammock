package com.homegrownsw.hammock.media

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class ScreenGeometryTest {
    private fun assertRect(expected: Rect, actual: Rect, eps: Float = 0.001f) {
        assertEquals("left", expected.left, actual.left, eps)
        assertEquals("top", expected.top, actual.top, eps)
        assertEquals("right", expected.right, actual.right, eps)
        assertEquals("bottom", expected.bottom, actual.bottom, eps)
    }

    // A 16:10 display on a phone held sideways: 2000×1000 px, 200 px kept for the bar.
    private val area = Rect(0f, 0f, 2000f, 800f)
    private val fitted = ScreenGeometry.fitIn(Size(1600f, 1000f), area)

    @Test fun fitsTheDisplayCentred() {
        assertRect(Rect(360f, 0f, 1640f, 800f), fitted)
    }

    @Test fun zoomedOutThePageIsCentredWhateverThePan() {
        assertRect(fitted, ScreenGeometry.page(fitted, 1f, Offset(300f, -200f), area))
    }

    @Test fun zoomedInThePageAlwaysCoversWhatsSeen() {
        // Twice as big: 2560×1600, panned far right, stops with its left edge at the area's.
        val r = ScreenGeometry.page(fitted, 2f, Offset(5000f, 0f), area)
        assertEquals(0f, r.left, 0.001f)
        assertEquals(2560f, r.width, 0.001f)
        val l = ScreenGeometry.page(fitted, 2f, Offset(-5000f, -5000f), area)
        assertEquals(area.right, l.right, 0.001f)
        assertEquals(area.bottom, l.bottom, 0.001f)
        // Its pan back gives the same page.
        assertRect(l, ScreenGeometry.page(fitted, 2f, ScreenGeometry.panOf(l, fitted), area))
    }

    @Test fun theKeyboardLetsATallPageSlideUp() {
        // Zoomed out the page fills the height; the keyboard covers the bottom half.
        val seen = Rect(0f, 0f, 2000f, 400f)
        val down = ScreenGeometry.page(fitted, 1f, Offset.Zero, seen)
        assertEquals(0f, down.top, 0.001f)
        val up = ScreenGeometry.page(fitted, 1f, Offset(0f, -10_000f), seen)
        assertEquals(seen.bottom, up.bottom, 0.001f)
        // Sideways it still fits, so it stays centred.
        assertEquals(seen.center.x, up.center.x, 0.001f)
    }

    @Test fun pointsAndFractions() {
        val p = ScreenGeometry.point(Offset(0.25f, 0.5f), fitted)
        assertEquals(Offset(680f, 400f), p)
        assertEquals(Offset(0.25f, 0.5f), ScreenGeometry.fraction(p, fitted))
        // Outside the page: its nearest edge.
        assertEquals(Offset(0f, 1f), ScreenGeometry.fraction(Offset(0f, 5000f), fitted))
    }

    @Test fun inSightIsAllOfItZoomedOut() {
        assertRect(WholeDisplay, ScreenGeometry.inSight(fitted, area, 1f))
        assertRect(WholeDisplay, ScreenGeometry.inSight(fitted, area, 1.01f))
    }

    @Test fun inSightZoomedInIsThePartOnScreen() {
        val r = ScreenGeometry.page(fitted, 4f, Offset.Zero, area)
        val v = ScreenGeometry.inSight(r, area, 4f)
        // 2000 of the page's 5120 wide, 800 of its 3200 high, in the middle.
        assertEquals(2000f / 5120f, v.width, 0.001f)
        assertEquals(0.25f, v.height, 0.001f)
        assertEquals(0.5f, v.center.x, 0.001f)
        assertEquals(0.5f, v.center.y, 0.001f)
    }

    @Test fun followPansJustEnoughToKeepThePointerInsideTheMargin() {
        assertEquals(Offset.Zero, ScreenGeometry.follow(Offset(1000f, 400f), area, 60f))
        assertEquals(Offset(50f, 0f), ScreenGeometry.follow(Offset(10f, 400f), area, 60f))
        assertEquals(Offset(0f, -40f), ScreenGeometry.follow(Offset(1000f, 780f), area, 60f))
        assertEquals(60f, ScreenGeometry.followMargin(area, 60f), 0f)
        assertEquals(25f, ScreenGeometry.followMargin(Rect(0f, 0f, 500f, 100f), 60f), 0f)
    }

    @Test fun aPointerOutOfSightComesInJustInsideTheMargin() {
        assertNull(ScreenGeometry.intoView(Offset(1000f, 400f), area, 60f))
        val to = ScreenGeometry.intoView(Offset(-500f, 2000f), area, 60f)
        assertNotNull(to)
        assertEquals(Offset(61f, 739f), to)
        // Inside follow's margin: the view doesn't move after it.
        assertEquals(Offset.Zero, ScreenGeometry.follow(to!!, area, 60f))
    }

    @Test fun thePointerGrowsWithTheZoomButLess() {
        // About 1:1: the Mac's own size.
        assertEquals(1f, ScreenGeometry.pointerScale(1512f, 1512f), 0.001f)
        // Zoomed right out it's smaller, but not below three quarters.
        assertEquals(0.75f, ScreenGeometry.pointerScale(300f, 1512f), 0.001f)
        // Zoomed right in, not more than three times.
        assertEquals(3f, ScreenGeometry.pointerScale(1512f * 16, 1512f), 0.001f)
        assertTrue(ScreenGeometry.pointerScale(1512f * 2, 1512f) < 2f)
    }

    @Test fun trackpadAccelerationAndScrollScale() {
        assertEquals(1f, ScreenGeometry.acceleration(0f), 0f)
        assertEquals(2f, ScreenGeometry.acceleration(1500f), 0f)
        assertEquals(2.5f, ScreenGeometry.acceleration(100_000f), 0f)
        // A page drawn at half the Mac's width: a finger's pixel scrolls two of the Mac's points.
        assertEquals(2f, ScreenGeometry.wheelScale(756f, 1512f), 0f)
        assertEquals(1f, ScreenGeometry.wheelScale(756f, 0f), 0f)
    }
}

class ViewAskerTest {
    private val screen = 2400f * 1080f

    @Test fun zoomedOutAsksForTheWholeDisplayOnce() {
        val a = ViewAsker()
        val first = a.next(WholeDisplay, 0, touching = false, crop = WholeDisplay, screenPixels = screen, detailPixels = 0f, aspect = 2.2f)
        assertNotNull(first)
        assertTrue(first!!.whole)
        assertEquals(2.2f, first.aspect, 0f)
        // The same again: nothing to send.
        assertNull(a.next(WholeDisplay, 100, false, WholeDisplay, screen, 0f, 2.2f))
        // A new shape (turned): sent again.
        assertNotNull(a.next(WholeDisplay, 200, false, WholeDisplay, screen, 0f, 0.45f))
    }

    @Test fun zoomedInAsksWithATenthToSpareAllRound() {
        val a = ViewAsker()
        val v = Rect(0.4f, 0.4f, 0.6f, 0.6f)
        val ask = a.next(v, 0, false, WholeDisplay, screen, detailPixels = screen, aspect = 2.2f)!!
        assertEquals(0.38f, ask.view.left, 0.0001f)
        assertEquals(0.62f, ask.view.right, 0.0001f)
        assertEquals(0.38f, ask.view.top, 0.0001f)
    }

    @Test fun moreToSpareWhenTheDetailsFramesAreBiggerThanTheScreen() {
        val a = ViewAsker()
        val v = Rect(0.4f, 0.4f, 0.6f, 0.6f)
        // Frames with 1.69 times the pixels: 15% a side (the most).
        val ask = a.next(v, 0, false, WholeDisplay, screen, detailPixels = screen * 1.69f, aspect = 2.2f)!!
        assertEquals(0.4f - 0.2f * 0.15f, ask.view.left, 0.0001f)
    }

    @Test fun whileTouchingItWaitsTillTheViewNearsTheDetailsEdge() {
        val a = ViewAsker()
        val crop = Rect(0.3f, 0.3f, 0.7f, 0.7f)
        // Well inside what the detail has: no new ask while fingers are down.
        assertNull(a.next(Rect(0.38f, 0.38f, 0.62f, 0.62f), 0, true, crop, screen, screen, 2.2f))
        assertNull(a.next(Rect(0.39f, 0.38f, 0.63f, 0.62f), 100, true, crop, screen, screen, 2.2f))
        // Near its edge: asked, ahead along the pan.
        val ask = a.next(Rect(0.5f, 0.38f, 0.74f, 0.62f), 200, true, crop, screen, screen, 2.2f)
        assertNotNull(ask)
        assertTrue("ahead of the pan", ask!!.view.center.x > 0.66f)
        // And not again within a quarter second, however near.
        assertNull(a.next(Rect(0.55f, 0.38f, 0.79f, 0.62f), 300, true, crop, screen, screen, 2.2f))
    }

    @Test fun onceFingersLiftItAsksForExactlyWhatsSeen() {
        val a = ViewAsker()
        val crop = Rect(0.3f, 0.3f, 0.7f, 0.7f)
        val v = Rect(0.38f, 0.38f, 0.62f, 0.62f)
        assertNull(a.next(v, 0, true, crop, screen, screen, 2.2f))
        val ask = a.next(v, 100, false, crop, screen, screen, 2.2f)!!
        assertEquals(0.5f, ask.view.center.x, 0.0001f)
        assertEquals(0.24f * 1.2f, ask.view.width, 0.0001f)
    }

    @Test fun zoomingWellIntoTheDetailAsksEvenMidPinch() {
        val a = ViewAsker()
        // Less than half the detail's width: asked for, for sharpness.
        val ask = a.next(Rect(0.45f, 0.45f, 0.55f, 0.55f), 0, true, WholeDisplay, screen, screen, 2.2f)
        assertNotNull(ask)
    }
}

class TwoFingerJudgeTest {
    @Test fun apartIsAZoomTheSameWayIsAScroll() {
        val zoom = TwoFingerJudge(10f)
        assertEquals(TwoFingers.UNDECIDED, zoom.judge(Offset(100f, 100f), 200f))
        assertEquals(TwoFingers.UNDECIDED, zoom.judge(Offset(101f, 100f), 205f))
        assertEquals(TwoFingers.ZOOM, zoom.judge(Offset(102f, 100f), 215f))
        // Settled: a scroll's worth of travel later doesn't change it.
        assertEquals(TwoFingers.ZOOM, zoom.judge(Offset(300f, 100f), 215f))

        val scroll = TwoFingerJudge(10f)
        scroll.judge(Offset(100f, 100f), 200f)
        assertEquals(TwoFingers.SCROLL, scroll.judge(Offset(100f, 115f), 203f))
        assertEquals(TwoFingers.SCROLL, scroll.judge(Offset(100f, 115f), 400f))
        assertEquals(Offset(100f, 100f), scroll.start!!.first)
    }

    @Test fun aQuickTwoFingerFlickIsAScrollASlowOneATap() {
        assertTrue(isFlick(Offset(0f, 400f)))
        assertTrue(!isFlick(Offset(100f, 100f)))
    }
}

class ClickCounterTest {
    @Test fun tapsCloseTogetherCountUpToThree() {
        val c = ClickCounter()
        assertEquals(1, c.click("left", 1000))
        assertEquals(2, c.click("left", 1300))
        assertEquals(3, c.click("left", 1600))
        assertEquals(3, c.click("left", 1900))
        // Too slow: a single click again.
        assertEquals(1, c.click("left", 2400))
        // Right clicks are always single.
        assertEquals(1, c.click("right", 2500))
        assertEquals(1, c.click("right", 2600))
    }
}
