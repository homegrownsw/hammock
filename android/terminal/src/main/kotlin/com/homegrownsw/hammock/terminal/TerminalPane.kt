package com.homegrownsw.hammock.terminal

import android.view.HapticFeedbackConstants
import android.view.View
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowLeft
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material.icons.automirrored.outlined.Backspace
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardArrowUp
import androidx.compose.material.icons.filled.KeyboardHide
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.TextUnit
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch

/**
 * A pane's terminal ([TerminalView]) with the key bar above the soft
 * keyboard while it's up: esc, tab, a sticky ctrl, the arrows and delete
 * (which repeat while held), and a key to put the keyboard away.
 *
 * [onResize] is the grid that fits at [fontSize] (in [fontFamily], ""
 * the bundled font, with [lineSpacing] extra row height, a fraction of the
 * font's) and its cell in pixels,
 * reported while [active] (on screen) and when typing starts here: send it
 * to the computer when this phone drives the pane. Pads itself above the
 * keyboard (imePadding), so the activity should let the window resize
 * for it (edge to edge, or adjustResize). Touch and hold selects text,
 * with handles to drag and a menu to copy it, unless not [selectable].
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun TerminalPane(
    session: TerminalSession,
    onInput: (ByteArray) -> Unit,
    onResize: (cols: Int, rows: Int, cellWidthPx: Int, cellHeightPx: Int) -> Unit,
    readOnly: Boolean,
    modifier: Modifier = Modifier,
    active: Boolean = true,
    theme: TerminalTheme = TerminalTheme.Dark,
    fontSize: TextUnit = 11.sp,
    fontFamily: String = "",
    lineSpacing: Float = 0f,
    onFocus: () -> Unit = {},
    onCellSize: (widthPx: Int, heightPx: Int) -> Unit = { _, _ -> },
    selectable: Boolean = true,
) {
    val input by rememberUpdatedState(onInput)
    val resize by rememberUpdatedState(onResize)
    val focus by rememberUpdatedState(onFocus)
    val cellSize by rememberUpdatedState(onCellSize)
    var view by remember { mutableStateOf<TerminalView?>(null) }
    var focused by remember { mutableStateOf(false) }
    var ctrl by remember { mutableStateOf(false) }
    var handles by remember { mutableStateOf<SelectionHandles?>(null) }
    val imeVisible = WindowInsets.isImeVisible
    val fontPx = with(LocalDensity.current) { fontSize.toPx() }

    // GLSurfaceView wants to know when the activity pauses and resumes.
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle, view) {
        val v = view
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_PAUSE -> v?.onPause()
                Lifecycle.Event.ON_RESUME -> v?.onResume()
                else -> {}
            }
        }
        lifecycle.addObserver(observer)
        onDispose { lifecycle.removeObserver(observer) }
    }

    Column(modifier.imePadding()) {
        Box(Modifier.weight(1f).fillMaxWidth()) {
            AndroidView(
                factory = { context ->
                    TerminalView(context).apply {
                        this.onInput = { input(it) }
                        this.onResize = { c, r, w, h -> resize(c, r, w, h) }
                        this.onFocus = { focus() }
                        this.onCellSize = { w, h -> cellSize(w, h) }
                        this.onCtrlChanged = { ctrl = it }
                        this.onSelectionHandles = { handles = it }
                        onFocusChangeListener = View.OnFocusChangeListener { _, has -> focused = has }
                        view = this
                    }
                },
                modifier = Modifier.fillMaxSize(),
                update = { v ->
                    v.selectable = selectable
                    v.session = session
                    v.readOnly = readOnly
                    v.theme = theme
                    v.fontFamily = fontFamily
                    v.lineSpacing = lineSpacing
                    v.fontSizePx = fontPx
                    v.keyboardVisible = imeVisible
                    v.active = active && !readOnly
                },
                onRelease = { v ->
                    v.session = null
                    v.onFocusChangeListener = null
                    v.onSelectionHandles = null
                    handles = null
                    view = null
                },
            )
            handles?.let { SelectionHandlesOverlay(it, MaterialTheme.colorScheme.primary) }
        }
        val v = view
        if (v != null && imeVisible && focused && !readOnly) {
            KeyBar(v, ctrl)
        }
    }
}

/** Esc, tab, ctrl, arrows, delete and hide, above the soft keyboard (KeyAccessoryView on the iPhone). */
@Composable
private fun KeyBar(view: TerminalView, ctrl: Boolean) {
    Surface(color = MaterialTheme.colorScheme.surfaceContainer, modifier = Modifier.fillMaxWidth()) {
        Row(
            Modifier.padding(horizontal = 6.dp, vertical = 5.dp).height(38.dp),
            horizontalArrangement = Arrangement.spacedBy(5.dp),
        ) {
            BarKey("Escape", text = "esc") { view.pressKey(TerminalKey.ESCAPE) }
            BarKey("Tab", text = "tab") { view.pressKey(TerminalKey.TAB) }
            BarKey("Control", text = "ctrl", selected = ctrl) { view.setCtrlLatched(!ctrl) }
            BarKey("Left arrow", icon = Icons.AutoMirrored.Filled.KeyboardArrowLeft, repeats = true) { view.pressKey(TerminalKey.LEFT) }
            BarKey("Down arrow", icon = Icons.Filled.KeyboardArrowDown, repeats = true) { view.pressKey(TerminalKey.DOWN) }
            BarKey("Up arrow", icon = Icons.Filled.KeyboardArrowUp, repeats = true) { view.pressKey(TerminalKey.UP) }
            BarKey("Right arrow", icon = Icons.AutoMirrored.Filled.KeyboardArrowRight, repeats = true) { view.pressKey(TerminalKey.RIGHT) }
            BarKey("Delete", icon = Icons.AutoMirrored.Outlined.Backspace, repeats = true) { view.pressKey(TerminalKey.BACKSPACE) }
            BarKey("Hide keyboard", icon = Icons.Filled.KeyboardHide) { view.hideKeyboard() }
        }
    }
}

/**
 * One key. Repeating keys go at once when touched and again while held,
 * as a keyboard's do, with a light tick each time.
 */
@Composable
private fun RowScope.BarKey(
    label: String,
    text: String? = null,
    icon: ImageVector? = null,
    selected: Boolean = false,
    repeats: Boolean = false,
    onKey: () -> Unit,
) {
    val key by rememberUpdatedState(onKey)
    val hostView = LocalView.current
    val scope = rememberCoroutineScope()
    var pressed by remember { mutableStateOf(false) }
    val colors = MaterialTheme.colorScheme
    val background = when {
        selected -> colors.primary
        pressed -> colors.surfaceVariant
        else -> colors.surfaceContainerHighest
    }
    val content = if (selected) colors.onPrimary else colors.onSurface
    fun tick() = hostView.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
    Surface(
        color = background,
        contentColor = content,
        shape = RoundedCornerShape(8.dp),
        modifier = Modifier
            .weight(1f)
            .semantics {
                contentDescription = label
                role = Role.Button
            }
            .pointerInput(repeats) {
                detectTapGestures(onPress = {
                    pressed = true
                    val job = if (repeats) {
                        scope.launch {
                            key()
                            tick()
                            delay(400)
                            while (isActive) {
                                key()
                                tick()
                                delay(60)
                            }
                        }
                    } else {
                        null
                    }
                    val released = tryAwaitRelease()
                    job?.cancel()
                    pressed = false
                    if (!repeats && released) {
                        tick()
                        key()
                    }
                })
            },
    ) {
        Box(contentAlignment = Alignment.Center) {
            if (icon != null) {
                Icon(icon, contentDescription = null)
            } else if (text != null) {
                Text(text, fontSize = 15.sp, maxLines = 1, softWrap = false)
            }
        }
    }
}

/**
 * The selection's handles over the terminal, Android's teardrops: a knob
 * hanging under the selection's first and last cells. Drawn only; the
 * terminal takes the touches (TerminalView.handleAt).
 */
@Composable
private fun SelectionHandlesOverlay(handles: SelectionHandles, color: Color) {
    val density = LocalDensity.current.density
    Canvas(Modifier.fillMaxSize().clearAndSetSemantics {}) {
        for (h in listOfNotNull(handles.start, handles.end)) {
            val r = SelectionHandle.KNOB_RADIUS_DP * density
            val cx = h.knobX(density)
            val cy = h.knobY(density)
            drawCircle(color, r, Offset(cx, cy))
            // The corner that points at the caret.
            val left = if (h.edge == SelectionEdge.START) cx else cx - r
            drawRect(color, Offset(left, cy - r), Size(r, r))
        }
    }
}
