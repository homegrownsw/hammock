package com.homegrownsw.hammock.media

import android.view.HapticFeedbackConstants
import android.view.View
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.CustomAccessibilityAction
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.customActions
import androidx.compose.ui.semantics.onClick
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.stateDescription
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp

/**
 * Screen sharing on a phone: a computer's screen open full screen, minimised
 * to a bar that stays connected while the rest of the app is used, or ended.
 * One screen at a time: opening another computer's replaces it. As the
 * iPhone's ScreenShareState.
 */
data class ScreenShareState(
    val phase: Phase = Phase.ENDED,
    val computerID: String = "",
    val name: String = "",
    /** Bumped each time a screen is opened afresh (not from the bar): a new viewer, new streams. */
    val session: Int = 0,
) {
    enum class Phase { ENDED, OPEN, MINIMISED }

    val isShowing get() = phase != Phase.ENDED
    val isMinimised get() = phase == Phase.MINIMISED
    val isOpen get() = phase == Phase.OPEN

    /** Full screen: the screen already shared comes back as it was (the same streams); another replaces it. */
    fun open(computerID: String, name: String): ScreenShareState =
        if (isShowing && computerID == this.computerID) copy(phase = Phase.OPEN, name = name)
        else copy(phase = Phase.OPEN, computerID = computerID, name = name, session = session + 1)

    /** Down to the bar: only an open screen minimises. */
    fun minimise() = if (isOpen) copy(phase = Phase.MINIMISED) else this

    /** Back to full screen from the bar. */
    fun expand() = if (isMinimised) copy(phase = Phase.OPEN) else this

    fun end() = copy(phase = Phase.ENDED, computerID = "", name = "")

    companion object {
        /**
         * The most frames a second asked of the computer for a screen's two
         * layers. Minimised, the base (the whole display) is the bar's live
         * thumbnail, at two a second, and the detail (not shown) at one; open,
         * each layer's full rate (the detail's is the computer's most, the
         * base's leaves the encoder to the detail).
         */
        fun detailFPS(minimised: Boolean) = if (minimised) 1 else 60
        fun baseFPS(minimised: Boolean) = if (minimised) 2 else 15
    }
}

/** The screen being shared, for the app: kept at its root (the workspace), so it outlives the screens it's opened from. */
@Stable
class ScreenShare {
    var state by mutableStateOf(ScreenShareState())
        private set
    /** The whole display's stream (the base), for the bar's live thumbnail. */
    var base by mutableStateOf<VideoStream?>(null)
        internal set
    var live by mutableStateOf(false)
        internal set

    fun open(computerID: String, name: String) { state = state.open(computerID, name) }

    fun minimise(view: View? = null) {
        if (!state.isOpen) return
        state = state.minimise()
        view?.performHapticFeedback(HapticFeedbackConstants.CONTEXT_CLICK)
    }

    fun expand(view: View? = null) {
        if (!state.isMinimised) return
        state = state.expand()
        view?.performHapticFeedback(HapticFeedbackConstants.CONTEXT_CLICK)
    }

    fun end() {
        state = state.end()
        base = null
        live = false
    }
}

/**
 * The minimised screen: a live thumbnail, the computer's name and whether it's
 * live, and End. Tapped, the screen comes back full screen. One element for
 * TalkBack, with Open and End. `top`: under a screen's top bar (a pane, where
 * the navigation bar's hidden), else above the navigation bar.
 */
@Composable
fun ScreenShareBar(share: ScreenShare, top: Boolean, modifier: Modifier = Modifier) {
    val s = share.state
    if (!s.isMinimised) return
    val view = LocalView.current
    val live = share.live
    Surface(
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        shape = if (top) RoundedCornerShape(0.dp) else RoundedCornerShape(topStart = 16.dp, topEnd = 16.dp),
        modifier = modifier
            .fillMaxWidth()
            .clickable { share.expand(view) }
            .clearAndSetSemantics {
                contentDescription = "Sharing ${s.name}'s screen"
                stateDescription = if (live) "Live" else "Connecting"
                role = Role.Button
                onClick(label = "Open") { share.expand(view); true }
                customActions = listOf(CustomAccessibilityAction("End") { share.end(); true })
            },
    ) {
        Row(
            Modifier.padding(start = 16.dp, end = 8.dp, top = 8.dp, bottom = 8.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(12.dp),
        ) {
            Box(
                Modifier.size(52.dp, 32.dp).clip(RoundedCornerShape(6.dp)).background(Color(0xFF141414))
                    .border(1.dp, MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(6.dp)),
            ) {
                share.base?.let { VideoSurface(it, Modifier.fillMaxSize()) }
            }
            Column(Modifier.weight(1f)) {
                Text(s.name, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Box(Modifier.size(6.dp).background(if (live) Color(0xFF34C759) else Color(0xFFFF9F0A), CircleShape))
                    Text(if (live) "Live" else "Connecting", style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.padding(start = 5.dp))
                }
            }
            Icon(Icons.Filled.KeyboardArrowUp, null, tint = MaterialTheme.colorScheme.primary)
            Box(
                Modifier.size(32.dp).clip(CircleShape).background(Color(0xFFFF3B30)).clickable(onClick = share::end),
                contentAlignment = Alignment.Center,
            ) {
                Icon(Icons.Filled.Close, null, Modifier.size(18.dp), tint = Color.White)
            }
        }
    }
}
