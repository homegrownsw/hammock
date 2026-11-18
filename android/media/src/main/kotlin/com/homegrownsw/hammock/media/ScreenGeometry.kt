package com.homegrownsw.hammock.media

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow
import kotlin.math.sqrt

// The screen viewer's arithmetic, apart from its views so it can be tested:
// where the display sits as it's zoomed and panned, what part of it is in
// sight, when to ask the Mac for that part sharp, and how touches become the
// pointer's moves. As the iPhone's ScreenViewer.swift does it.

/** The whole display, as fractions of it. */
internal val WholeDisplay = Rect(0f, 0f, 1f, 1f)

internal object ScreenGeometry {
    /** `shape` fitted in `area`, centred. */
    fun fitIn(shape: Size, area: Rect): Rect {
        if (shape.width <= 0f || shape.height <= 0f || area.width <= 0f || area.height <= 0f) return area
        val s = min(area.width / shape.width, area.height / shape.height)
        val w = shape.width * s
        val h = shape.height * s
        return Rect(area.center.x - w / 2, area.center.y - h / 2, area.center.x + w / 2, area.center.y + h / 2)
    }

    /**
     * Where the page (the display, fitted, zoomed about its centre and
     * panned from there) is, kept covering what can be seen (or centred in
     * it while smaller), so its every edge can be brought into view.
     */
    fun page(fitted: Rect, zoom: Float, pan: Offset, seen: Rect): Rect {
        val w = fitted.width * zoom
        val h = fitted.height * zoom
        var x = fitted.center.x - w / 2 + pan.x
        var y = fitted.center.y - h / 2 + pan.y
        x = if (w > seen.width) x.coerceIn(seen.right - w, seen.left) else seen.center.x - w / 2
        y = if (h > seen.height) y.coerceIn(seen.bottom - h, seen.top) else seen.center.y - h / 2
        return Rect(x, y, x + w, y + h)
    }

    /** The pan that puts the page at `page`. */
    fun panOf(page: Rect, fitted: Rect): Offset = page.center - fitted.center

    /** A point in the view as fractions of the display. */
    fun fraction(p: Offset, page: Rect): Offset {
        if (page.width <= 0f || page.height <= 0f) return Offset(0.5f, 0.5f)
        return Offset(((p.x - page.left) / page.width).coerceIn(0f, 1f), ((p.y - page.top) / page.height).coerceIn(0f, 1f))
    }

    /** A fraction of the display as a point in the view. */
    fun point(f: Offset, page: Rect): Offset = Offset(page.left + f.x * page.width, page.top + f.y * page.height)

    /** The part of the display in sight (fractions): all of it zoomed out. */
    fun inSight(page: Rect, seen: Rect, zoom: Float): Rect {
        if (page.width <= 0f || page.height <= 0f) return WholeDisplay
        val s = seen.intersect(page)
        if (s.width <= 0f || s.height <= 0f) return WholeDisplay
        val v = Rect((s.left - page.left) / page.width, (s.top - page.top) / page.height, (s.right - page.left) / page.width, (s.bottom - page.top) / page.height)
        return if (zoom <= 1.02f || v.width >= 0.98f) WholeDisplay else v
    }

    /** How near the edge the pointer may come before the view follows it. */
    fun followMargin(seen: Rect, sixtyDp: Float) = min(sixtyDp, seen.height / 4)

    /** How far to pan so the pointer at `tip` stays `margin` inside what's seen. */
    fun follow(tip: Offset, seen: Rect, margin: Float): Offset {
        var dx = 0f
        var dy = 0f
        if (tip.x < seen.left + margin) dx = seen.left + margin - tip.x
        if (tip.x > seen.right - margin) dx = -(tip.x - (seen.right - margin))
        if (tip.y < seen.top + margin) dy = seen.top + margin - tip.y
        if (tip.y > seen.bottom - margin) dy = -(tip.y - (seen.bottom - margin))
        return Offset(dx, dy)
    }

    /**
     * Zoomed in, a pointer outside the part you see comes in to its nearest
     * point there (just inside follow's margin, so the view doesn't pan),
     * rather than the view leaping off after it on the next move. Null: it's in.
     */
    fun intoView(tip: Offset, seen: Rect, margin: Float): Offset? {
        if (seen.width <= 0f || seen.height <= 0f) return null
        val m = margin + 1
        val dx = min(m, seen.width / 2 - 1).coerceAtLeast(0f)
        val dy = min(m, seen.height / 2 - 1).coerceAtLeast(0f)
        val inner = Rect(seen.left + dx, seen.top + dy, seen.right - dx, seen.bottom - dy)
        if (tip.x >= inner.left && tip.x < inner.right && tip.y >= inner.top && tip.y < inner.bottom) return null
        return Offset(tip.x.coerceIn(inner.left, inner.right), tip.y.coerceIn(inner.top, inner.bottom))
    }

    /**
     * How big the pointer's drawn, for the page's width (dp) and the
     * display's (the Mac's points): as big as the Mac's own when shown about
     * 1:1, a little smaller zoomed right out, and not so big zoomed right in
     * that it hides what it points at.
     */
    fun pointerScale(pageWidthDp: Float, macWidth: Float): Float {
        val s = if (macWidth > 0f) pageWidthDp / macWidth else 1f
        return s.pow(0.6f).coerceIn(0.75f, 3f)
    }

    /** Trackpad: the pointer moves a little faster when the finger's quick (dp/s). */
    fun acceleration(speedDp: Float) = 1 + min(speedDp / 1500f, 1.5f)

    /** Scrolling's scale: the Mac's points per pixel of the page, so a finger scrolls the Mac as far as it moved over it. */
    fun wheelScale(pageWidthPx: Float, macWidth: Float) = if (macWidth > 0f && pageWidthPx > 0f) macWidth / pageWidthPx else 1f
}

/**
 * Decides when to tell the Mac which part of its display is in view
 * (SetMediaView), so it sends that part sharp, and with how much to spare.
 * Asked about ten times a second. While fingers are down the detail isn't
 * asked to move unless the view nears the edge of what it has (or zooms well
 * into it), at most four times a second, for where the view's heading a
 * quarter second on: each new part is a frame of all-new pixels, and the
 * whole display underneath fills in meanwhile. Exactly once they lift.
 */
internal class ViewAsker {
    data class Ask(
        /** Fractions of the display; WholeDisplay for all of it (SetMediaView's w 0). */
        val view: Rect,
        /** The frames' shape: this screen's width over its height. */
        val aspect: Float,
    ) {
        val whole: Boolean get() = view.width >= 1f
    }

    private var last: Rect? = null
    private var lastAt = 0L
    /** How fast the view moves, in fractions of the display a second. */
    var velocity = Offset.Zero
        private set
    private var lastAsked = Long.MIN_VALUE / 2
    private var sent: Rect? = null
    private var sentAspect = 0f

    /**
     * v: what's in sight now (ScreenGeometry.inSight). crop: what the
     * detail's frames show now. screenPixels: this view's area in pixels;
     * detailPixels: the detail's frames' (0 before the first). Null: nothing
     * to send now.
     */
    fun next(v: Rect, now: Long, touching: Boolean, crop: Rect, screenPixels: Float, detailPixels: Float, aspect: Float): Ask? {
        var view = v
        last?.let { l ->
            if (now - lastAt > 10) {
                val k = 1000f / (now - lastAt)
                val vel = Offset((view.center.x - l.center.x) * k, (view.center.y - l.center.y) * k)
                velocity = velocity * 0.5f + vel * 0.5f
            }
        }
        last = view
        lastAt = now
        if (touching) {
            val inner = Rect(crop.left + crop.width * 0.15f, crop.top + crop.height * 0.15f, crop.right - crop.width * 0.15f, crop.bottom - crop.height * 0.15f)
            val inside = view.left >= inner.left && view.top >= inner.top && view.right <= inner.right && view.bottom <= inner.bottom
            val near = !inside || view.width < crop.width * 0.5f
            if (!near || now - lastAsked < 250) return null
            lastAsked = now
            view = view.translate(velocity * 0.25f)
            val x = view.left.coerceIn(0f, max(0f, 1 - view.width))
            val y = view.top.coerceIn(0f, max(0f, 1 - view.height))
            view = Rect(x, y, x + view.width, y + view.height)
        }
        if (view.width < 1f) {
            // Some to spare all round, so a pan stays sharp: as much as the
            // detail's frames cover with this screen's pixels one to one, a
            // tenth to 15% a side (a tenth once the link has made them smaller).
            val fits = if (screenPixels > 0f && detailPixels > 0f) (sqrt(detailPixels / screenPixels) - 1) / 2 else 0f
            val spare = min(max(fits, 0.1f), 0.15f)
            view = Rect(view.left - view.width * spare, view.top - view.height * spare, view.right + view.width * spare, view.bottom + view.height * spare)
        }
        val s = sent
        if (s != null && abs(view.left - s.left) < 0.01f && abs(view.top - s.top) < 0.01f && abs(view.width - s.width) < 0.01f &&
            abs(aspect - sentAspect) < 0.01f
        ) return null
        sent = view
        sentAspect = aspect
        return Ask(if (view.width >= 1f) WholeDisplay else view, aspect)
    }
}

/** What two fingers are doing: settled once they've moved enough to tell, then kept till they lift. */
internal enum class TwoFingers { UNDECIDED, ZOOM, SCROLL }

/**
 * Tells, once two fingers have moved `threshold` from where they landed,
 * whether they're zooming (apart or together) or scrolling (the same way),
 * by whichever they've done more of; then keeps to it, so a scroll doesn't
 * turn into a zoom halfway (two fingers never keep quite the same distance
 * apart) or a zoom into a scroll.
 */
internal class TwoFingerJudge(private val threshold: Float) {
    var mode = TwoFingers.UNDECIDED
        private set
    /** Where they landed: their centre and how far apart. */
    var start: Pair<Offset, Float>? = null
        private set

    fun judge(centre: Offset, spread: Float): TwoFingers {
        if (mode != TwoFingers.UNDECIDED) return mode
        val s = start ?: run {
            start = centre to spread
            return mode
        }
        val travel = (centre - s.first).getDistance()
        val change = abs(spread - s.second)
        if (max(travel, change) >= threshold) mode = if (change > travel) TwoFingers.ZOOM else TwoFingers.SCROLL
        return mode
    }
}

/** Taps close together are a double or triple click (left only). */
internal class ClickCounter {
    private var at = Long.MIN_VALUE / 2
    private var count = 0

    fun click(button: String, now: Long): Int {
        val c = if (button == "left" && now - at < 400) min(count + 1, 3) else 1
        at = now
        count = c
        return c
    }
}

/** Two fingers lifting before they could be told apart, quickly: a flick that scrolls (dp/s). */
internal fun isFlick(velocityDp: Offset) = hypot(velocityDp.x, velocityDp.y) > 300f
