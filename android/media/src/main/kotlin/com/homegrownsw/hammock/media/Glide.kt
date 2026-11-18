package com.homegrownsw.hammock.media

import androidx.compose.runtime.withFrameNanos
import androidx.compose.ui.geometry.Offset
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlin.math.hypot
import kotlin.math.pow

/**
 * Momentum after a flick: keeps scrolling at `velocity` (px/s), slowing by
 * `decay` a millisecond as a scroll view does, until it's below a third of
 * minSpeed. Nothing for a flick slower than minSpeed. Cancel the job to stop
 * it (a new touch does).
 */
internal fun CoroutineScope.launchGlide(
    velocity: Offset,
    minSpeed: Float,
    decay: Double,
    step: (dx: Float, dy: Float) -> Unit,
): Job? {
    if (hypot(velocity.x, velocity.y) < minSpeed) return null
    return launch {
        var v = velocity
        var last = withFrameNanos { it }
        while (true) {
            val now = withFrameNanos { it }
            val dt = (now - last) / 1e9f
            last = now
            step(v.x * dt, v.y * dt)
            v *= decay.pow(dt * 1000.0).toFloat()
            if (hypot(v.x, v.y) < minSpeed / 3) break
        }
    }
}
