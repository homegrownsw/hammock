package com.homegrownsw.hammock.media

import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import hammock.v1.Hammock

// Design notes' arithmetic (Pins.kt draws them): pins are fractions of the
// page or screen as it shows, drawn over wherever that is in the view.

internal object PinLayout {
    /** The page as drawn: fitted in the box, centred, then zoomed about its centre and panned (BrowserPane's graphicsLayer). */
    fun pageRect(box: Size, fitted: Size, zoom: Float, pan: Offset): Rect {
        val w = fitted.width * zoom
        val h = fitted.height * zoom
        return Rect(Offset(box.width / 2 + pan.x - w / 2, box.height / 2 + pan.y - h / 2), Size(w, h))
    }

    /** Where fractions (0...1) of [page] are in the view. */
    fun point(x: Double, y: Double, page: Rect): Offset =
        Offset(page.left + x.toFloat() * page.width, page.top + y.toFloat() * page.height)

    /** A touch as fractions of [page]; null outside it (the space around a fitted page). */
    fun fraction(p: Offset, page: Rect): Pair<Double, Double>? {
        if (page.width <= 0f || page.height <= 0f) return null
        if (p.x < page.left || p.x > page.right || p.y < page.top || p.y > page.bottom) return null
        return ((p.x - page.left) / page.width).toDouble() to ((p.y - page.top) / page.height).toDouble()
    }

    /** Whether a point is in the part of the page showing (its far edges included: a pin can be at 1). */
    fun shows(p: Offset, bounds: Rect): Boolean = p.x >= bounds.left && p.x <= bounds.right && p.y >= bounds.top && p.y <= bounds.bottom

    /** A pin's element outline in the view (null: none found). */
    fun outline(pin: Hammock.Pin, page: Rect): Rect? {
        if (!pin.hasRect() || pin.rect.w <= 0 || pin.rect.h <= 0) return null
        val r = pin.rect
        return Rect(point(r.x, r.y, page), Size((r.w * page.width).toFloat(), (r.h * page.height).toFloat()))
    }

    /**
     * The badge's centre: [offset] from its point (up and to the right), kept
     * wholly inside [bounds] so one at an edge stays readable and tappable.
     */
    fun badgeCentre(point: Offset, offset: Offset, radius: Float, bounds: Rect): Offset {
        val c = point + offset
        return Offset(clamp(c.x, bounds.left + radius, bounds.right - radius), clamp(c.y, bounds.top + radius, bounds.bottom - radius))
    }

    // Narrower than the badge: its middle.
    private fun clamp(v: Float, lo: Float, hi: Float) = if (lo > hi) (lo + hi) / 2 else v.coerceIn(lo, hi)

    /** The agents a board can go to: the Group's other panes with an agent still running. */
    fun targets(groupPanes: List<Hammock.Pane>, selfID: String): List<Hammock.Pane> = groupPanes.filter {
        it.id != selfID && it.hasAgent() && !it.exited && it.agent.state != Hammock.AgentState.AGENT_STATE_EXITED
    }

    /** Each target's name in the menu: its agent's, with its tab's when two share a name. */
    fun targetLabels(targets: List<Hammock.Pane>, name: (Hammock.Pane) -> String, place: (Hammock.Pane) -> String): List<String> {
        val names = targets.map(name)
        return targets.mapIndexed { i, p ->
            val n = names[i]
            val where = place(p)
            if (names.count { it == n } > 1 && where.isNotEmpty()) "$n · $where" else n
        }
    }

    /** What the bar says. */
    fun status(count: Int, queued: Boolean): String = when {
        queued -> "Held: the agent's waiting on you"
        count == 0 -> "Touch and hold to pin a note"
        count == 1 -> "1 note"
        else -> "$count notes"
    }
}
