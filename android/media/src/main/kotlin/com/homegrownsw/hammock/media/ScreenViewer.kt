package com.homegrownsw.hammock.media

import android.app.Activity
import android.content.ClipboardManager
import android.content.Context
import android.content.ContextWrapper
import android.graphics.BitmapFactory
import android.os.SystemClock
import android.view.HapticFeedbackConstants
import androidx.activity.compose.BackHandler
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.foundation.Canvas
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.displayCutout
import androidx.compose.foundation.layout.displayCutoutPadding
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.isImeVisible
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.systemGestureExclusion
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.ContentPaste
import androidx.compose.material.icons.filled.DesktopMac
import androidx.compose.material.icons.filled.Keyboard
import androidx.compose.material.icons.filled.KeyboardArrowDown
import androidx.compose.material.icons.filled.KeyboardHide
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.OpenWith
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Rect
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.Path
import androidx.compose.ui.graphics.TransformOrigin
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.PointerEvent
import androidx.compose.ui.input.pointer.PointerEventPass
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.SubcomposeLayout
import androidx.compose.ui.layout.onGloballyPositioned
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.layout.positionInParent
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalLayoutDirection
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.Constraints
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import hammock.v1.Hammock
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/**
 * A computer's whole screen (screen sharing, ADR-0014): its display as live
 * video, in two layers (a small, cheap one of the whole display, and one about
 * this phone's size that, zoomed in, the computer cuts from the display at full
 * resolution: SetMediaView), with its pointer drawn here at once. Driven as a
 * trackpad (drag moves the pointer, tap clicks, two fingers scroll or
 * right-click, hold and drag drags) or by touch (tap clicks where you tap),
 * pinch to zoom, and the keyboard with a bar of the Mac's modifier keys. As
 * the iPhone's ScreenViewer.swift. Shown full screen by the caller, over
 * everything, while `share` is open; minimised (Back, the controls' Minimize,
 * or a swipe down on them) it stays composed, drawing nothing, so its streams
 * go on, slowed to a frame or two a second, for the bar's thumbnail
 * (ScreenShareBar). End closes it. (The app lock, if it's on, guards it as it
 * guards the app.)
 */
@Composable
fun ScreenViewer(model: AppModel, share: ScreenShare) {
    val computerID = share.state.computerID
    val name = share.state.name
    val minimised = share.state.isMinimised
    val onClose = share::end
    val hostView = LocalView.current
    val onMinimise = { share.minimise(hostView) }
    var displays by remember { mutableStateOf<List<Hammock.Display>>(emptyList()) }
    var unavailable by remember { mutableStateOf<String?>(null) }
    var loaded by remember { mutableStateOf(false) }
    var display by remember { mutableIntStateOf(0) }
    var trackpad by remember { mutableStateOf(true) }
    var keyboard by remember { mutableStateOf(false) }
    var session by remember { mutableIntStateOf(0) }
    var hint by remember { mutableStateOf(true) }
    var state by remember { mutableStateOf<VideoStream.State>(VideoStream.State.Connecting) }
    var detail by remember { mutableStateOf<VideoStream?>(null) }
    var base by remember { mutableStateOf<VideoStream?>(null) }
    val context = LocalContext.current

    // Back minimises (End ends).
    BackHandler(!minimised) { if (keyboard) keyboard = false else onMinimise() }
    if (!minimised) HideSystemBars()

    LaunchedEffect(computerID) {
        val r = model.listDisplays(computerID)
        if (r == null) unavailable = "$name didn't answer."
        else {
            displays = r.displaysList
            unavailable = r.unavailable.ifEmpty { null }
        }
        loaded = true
    }

    // Leaving the app for a moment (a code from Messages, a password from
    // another app) keeps the screen: the streams go on a while (as long as
    // Android lets them) before they stop. Away longer, it picks up again on
    // return. (collectAsState, not ...WithLifecycle: that stops watching the
    // lifecycle just as it goes below started.)
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val life by lifecycle.currentStateFlow.collectAsState()
    val started = life.isAtLeast(Lifecycle.State.STARTED)
    var foreground by remember { mutableStateOf(true) }
    var away by remember { mutableStateOf(false) }
    var returnedAt by remember { mutableLongStateOf(Long.MIN_VALUE / 2) }
    var resume by remember { mutableStateOf(false) }
    LaunchedEffect(started) {
        if (started) {
            foreground = true
            if (away) {
                away = false
                returnedAt = SystemClock.uptimeMillis()
                if (state is VideoStream.State.Failed) resume = true
            }
        } else {
            away = true
            delay(BACKGROUND_GRACE_MS)
            foreground = false
        }
    }
    // A stream that ends while the app's away, or just after it's back (it
    // died while away), starts again by itself once the computer's
    // connection is back (it dropped too), rather than "The screen stopped".
    LaunchedEffect(state) {
        if (state is VideoStream.State.Failed &&
            (!lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED) || SystemClock.uptimeMillis() - returnedAt < 15_000)
        ) resume = true
    }
    val link by model.link.collectAsState()
    LaunchedEffect(resume, started, link) {
        if (resume && started && link == Link.Connected) {
            resume = false
            session++
        }
    }

    LaunchedEffect(display, session, foreground, loaded && unavailable == null) {
        if (!foreground || !loaded || unavailable != null) return@LaunchedEffect
        state = VideoStream.State.Connecting
        var conn = model.connection
        while (conn == null) {
            delay(500)
            conn = model.connection
        }
        val link = ConnectionLink(conn)
        val cid = model.screenComputer(computerID)
        // Detail: about this screen's size; zoomed in, just the part shown, sharp.
        val d = VideoStream.screen(link, display, cid, maxPixels = 3072 * 1728)
        // Base: the whole display, small and cheap, always live underneath, so
        // a pan shows what's there at once while the detail catches up. 15
        // frames a second leaves the encoder to the detail.
        val b = VideoStream.screen(link, display, cid, maxPixels = 1280 * 720, maxFPS = 15, background = true)
        detail = d
        base = b
        share.base = b
        try {
            b.start()
            d.start()
            d.state.collect { state = it }
        } finally {
            d.stop()
            b.stop()
            detail = null
            base = null
            if (share.base === b) share.base = null
        }
    }

    // Minimised: a frame or two a second (the bar's thumbnail is the base),
    // and the keyboard put away; open, each layer's full rate again.
    LaunchedEffect(minimised, detail, base) {
        detail?.setMaxFPS(ScreenShareState.detailFPS(minimised))
        base?.setMaxFPS(ScreenShareState.baseFPS(minimised))
        if (minimised) keyboard = false
    }
    // Opened over the app: whatever under it had the keyboard (an agent's
    // message box) lets go, or what's typed would go there, behind the
    // screen, rather than to the Mac.
    val focusManager = LocalFocusManager.current
    LaunchedEffect(minimised) { if (!minimised) focusManager.clearFocus(force = true) }
    LaunchedEffect(state) { share.live = state == VideoStream.State.Playing }
    // Where it was zoomed to, kept while minimised (a new display or session starts afresh).
    val viewState = remember(display, session) { ScreenViewState() }

    val shown = displays.firstOrNull { if (display == 0) it.main else it.id == display } ?: displays.firstOrNull()
    val macWidth = shown?.width?.toFloat() ?: 0f
    val playing = state == VideoStream.State.Playing
    val locked by (detail?.locked ?: notLocked).collectAsState()
    val send: (String, Map<String, Any?>) -> Unit = { op, args -> model.screenInput(computerID, display, op, args) }
    val sendNow by rememberUpdatedState(send)
    val keys = remember { ScreenKeys({ op, args -> sendNow(op, args) }) }
    keys.locked = locked
    // Minimised: nothing drawn, and no input (the streams go on above).
    if (minimised) return

    Box(
        Modifier
            .fillMaxSize()
            .background(Color.Black)
            // Over the app: its touches stop here.
            .pointerInput(Unit) { awaitEachGesture { awaitFirstDown(requireUnconsumed = false) } },
    ) {
        val why = unavailable
        if (why != null) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { Unavailable("Can't Share $name's Screen", why, onDark = true) }
        } else {
            // A new display, or a new session, starts afresh: zoomed out, the pointer from the Mac.
            key(display, session) {
                ScreenSurface(
                    detail = detail,
                    base = base,
                    view = viewState,
                    displayShape = shown?.let { Size(it.width.toFloat(), it.height.toFloat()) },
                    macWidth = macWidth,
                    trackpad = trackpad,
                    keyboard = keyboard,
                    keys = keys,
                    send = send,
                    onKeyboard = { keyboard = it },
                    onEnd = onClose,
                )
            }
        }
        (state as? VideoStream.State.Failed)?.let { f ->
            if (why == null) {
                Surface(shape = RoundedCornerShape(16.dp), tonalElevation = 3.dp, modifier = Modifier.align(Alignment.Center).padding(24.dp)) {
                    Column(Modifier.padding(20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                        Text("The screen stopped", style = MaterialTheme.typography.titleMedium)
                        Text(f.reason, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Spacer(Modifier.size(10.dp))
                        Button(onClick = { state = VideoStream.State.Connecting; session++ }) { Text("Try Again") }
                    }
                }
            }
        }
        // Top: the lock screen's prompt, or how to drive it, for a moment.
        Box(Modifier.align(Alignment.TopCenter).displayCutoutPadding().padding(top = 8.dp, start = 12.dp, end = 12.dp)) {
            if (locked && playing && !keyboard) {
                // At the lock screen: type the password (macOS lets the keys through to its field).
                Row(
                    Modifier.background(Color.Black.copy(alpha = 0.78f), RoundedCornerShape(22.dp)).padding(start = 16.dp, end = 6.dp, top = 6.dp, bottom = 6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Icon(Icons.Filled.Lock, null, tint = Color.White)
                    Text(
                        "$name is locked. Tap the password field and type your password.",
                        color = Color.White,
                        style = MaterialTheme.typography.bodySmall,
                        modifier = Modifier.weight(1f, fill = false).padding(horizontal = 12.dp),
                    )
                    Button(onClick = { keyboard = true }, colors = ButtonDefaults.buttonColors(containerColor = Color.White, contentColor = Color.Black)) {
                        Icon(Icons.Filled.Keyboard, null, Modifier.size(18.dp))
                        Text("Keyboard", Modifier.padding(start = 6.dp))
                    }
                }
            } else {
                AnimatedVisibility(hint && playing && trackpad, enter = fadeIn(), exit = fadeOut()) {
                    Text(
                        "Drag to move · tap to click · two fingers to scroll or right-click · pinch to zoom",
                        color = Color.White,
                        style = MaterialTheme.typography.bodySmall,
                        textAlign = TextAlign.Center,
                        modifier = Modifier.background(Color.Black.copy(alpha = 0.7f), RoundedCornerShape(16.dp)).padding(horizontal = 14.dp, vertical = 7.dp),
                    )
                }
                LaunchedEffect(playing) {
                    if (playing) {
                        delay(5000)
                        hint = false
                    }
                }
            }
        }
        if (!keyboard) {
            val controls = @Composable { tight: Boolean ->
                Controls(
                    tight = tight,
                    trackpad = trackpad,
                    onTrackpad = { trackpad = it },
                    displays = displays,
                    display = display,
                    onDisplay = { display = it },
                    playing = playing,
                    onKeyboard = { keyboard = true },
                    onMinimise = onMinimise,
                    onPaste = {
                        val text = context.getSystemService(ClipboardManager::class.java)?.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
                        if (text.isNullOrEmpty()) model.showToast("Your clipboard has no text") else keys.paste(text)
                    },
                    onEnd = onClose,
                )
            }
            // Tighter when that's all there's room for (a phone held upright).
            FirstThatFits(
                listOf({ controls(false) }, { controls(true) }),
                Modifier.align(Alignment.BottomCenter).displayCutoutPadding().padding(bottom = 8.dp, start = 12.dp, end = 12.dp),
            )
        }
    }
}

/** How long the screen keeps going while the app's away (iOS gives about this). */
private const val BACKGROUND_GRACE_MS = 30_000L

/** The first of `options` that fits the width (the last if none does), as SwiftUI's ViewThatFits. */
@Composable
private fun FirstThatFits(options: List<@Composable () -> Unit>, modifier: Modifier = Modifier) {
    SubcomposeLayout(modifier) { c ->
        val loose = Constraints(maxHeight = c.maxHeight)
        for ((i, option) in options.withIndex()) {
            val placeables = subcompose(i, option).map { it.measure(loose) }
            val w = placeables.maxOfOrNull { it.width } ?: 0
            if (w <= c.maxWidth || i == options.lastIndex) {
                val h = placeables.maxOfOrNull { it.height } ?: 0
                val width = min(w, c.maxWidth).coerceAtLeast(c.minWidth)
                return@SubcomposeLayout layout(width, h) { placeables.forEach { it.place((width - it.width) / 2, 0) } }
            }
        }
        layout(0, 0) {}
    }
}

/** The floating bar: Trackpad or Touch, the keyboard, the display, paste, the state and End. */
@Composable
private fun Controls(
    tight: Boolean,
    trackpad: Boolean,
    onTrackpad: (Boolean) -> Unit,
    displays: List<Hammock.Display>,
    display: Int,
    onDisplay: (Int) -> Unit,
    playing: Boolean,
    onKeyboard: () -> Unit,
    onMinimise: () -> Unit,
    onPaste: () -> Unit,
    onEnd: () -> Unit,
) {
    var picking by remember { mutableStateOf(false) }
    val button: Dp = if (tight) 44.dp else 48.dp
    Row(
        Modifier
            // A swipe down on the controls minimises (on the screen itself, a drag is the computer's).
            .pointerInput(Unit) {
                val far = 40.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    var done = false
                    while (!done) {
                        val ev = awaitPointerEvent(PointerEventPass.Initial)
                        val c = ev.changes.firstOrNull { it.id == down.id } ?: break
                        val d = c.position - down.position
                        if (d.y > far && d.y > abs(d.x)) {
                            ev.changes.forEach { it.consume() }
                            onMinimise()
                            done = true
                        }
                        if (!c.pressed) done = true
                    }
                }
            }
            .background(Color.Black.copy(alpha = 0.72f), CircleShape)
            .border(1.dp, Color.White.copy(alpha = 0.18f), CircleShape)
            .padding(6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(if (tight) 0.dp else 4.dp),
    ) {
        Row(Modifier.background(Color.White.copy(alpha = 0.14f), CircleShape).padding(2.dp), horizontalArrangement = Arrangement.spacedBy(2.dp)) {
            ModeButton("Trackpad", trackpad, tight) { onTrackpad(true) }
            ModeButton("Touch", !trackpad, tight) { onTrackpad(false) }
        }
        IconButton(onClick = onKeyboard, modifier = Modifier.size(button)) { Icon(Icons.Filled.Keyboard, "Keyboard", tint = Color.White) }
        if (displays.size > 1) {
            Box {
                IconButton(onClick = { picking = true }, modifier = Modifier.size(button)) { Icon(Icons.Filled.DesktopMac, "Display", tint = Color.White) }
                DropdownMenu(picking, { picking = false }) {
                    for (d in displays) {
                        val id = if (d.main) 0 else d.id
                        DropdownMenuItem(
                            { Text(d.name) },
                            { picking = false; onDisplay(id) },
                            trailingIcon = if (id == display) ({ Icon(Icons.Filled.Check, "Shown") }) else null,
                        )
                    }
                }
            }
        }
        IconButton(onClick = onPaste, modifier = Modifier.size(button)) { Icon(Icons.Filled.ContentPaste, "Paste to the Mac", tint = Color.White) }
        // The state, in words when there's room (else just its colour).
        Row(
            Modifier.padding(horizontal = if (tight) 6.dp else 8.dp).semantics(mergeDescendants = true) { contentDescription = if (playing) "Live" else "Connecting" },
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Box(Modifier.size(8.dp).background(if (playing) Color(0xFF34C759) else Color(0xFFFF9F0A), CircleShape))
            if (!tight) Text(if (playing) "Live" else "Connecting", color = Color.White, style = MaterialTheme.typography.labelMedium, maxLines = 1, modifier = Modifier.padding(start = 6.dp))
        }
        // Down to a bar that stays connected.
        IconButton(onClick = onMinimise, modifier = Modifier.size(button)) { Icon(Icons.Filled.KeyboardArrowDown, "Minimize", tint = Color.White) }
        // A plain circle: an IconButton keeps a 48dp touch target, wider than the bar has room for.
        Box(
            Modifier.size(36.dp).clip(CircleShape).background(Color(0xFFFF3B30)).clickable(onClickLabel = "End screen sharing", onClick = onEnd),
            contentAlignment = Alignment.Center,
        ) {
            Icon(Icons.Filled.Close, "End screen sharing", Modifier.size(20.dp), tint = Color.White)
        }
    }
}

@Composable
private fun ModeButton(title: String, on: Boolean, tight: Boolean, onClick: () -> Unit) {
    Text(
        title,
        color = if (on) Color.Black else Color.White,
        fontWeight = if (on) FontWeight.SemiBold else FontWeight.Normal,
        style = MaterialTheme.typography.bodyMedium,
        maxLines = 1,
        modifier = Modifier
            .heightIn(min = 40.dp)
            .background(if (on) Color.White else Color.Transparent, CircleShape)
            .clickable(onClick = onClick)
            .semantics { selected = on }
            .padding(horizontal = if (tight) 10.dp else 14.dp, vertical = 10.dp),
    )
}

/** Hides the status and navigation bars while shown (a swipe brings them back for a moment). */
@Composable
private fun HideSystemBars() {
    val view = LocalView.current
    DisposableEffect(view) {
        val window = view.context.activity()?.window ?: return@DisposableEffect onDispose {}
        val c = WindowCompat.getInsetsController(window, view)
        val before = c.systemBarsBehavior
        c.systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        c.hide(WindowInsetsCompat.Type.systemBars())
        onDispose {
            c.show(WindowInsetsCompat.Type.systemBars())
            c.systemBarsBehavior = before
        }
    }
}

private fun Context.activity(): Activity? {
    var c: Context? = this
    while (c is ContextWrapper) {
        if (c is Activity) return c
        c = c.baseContext
    }
    return null
}

// What a stream's flows hold before there's a stream.
private val notLocked = MutableStateFlow(false)
private val noSize = MutableStateFlow<VideoStream.Size?>(null)
private val wholeCrop = MutableStateFlow(Crop.WHOLE)
private val noPointer = MutableStateFlow<ScreenPointer?>(null)

/**
 * The display, zoomed and panned, taking touches; the keyboard and its bar of
 * modifier keys. Positions go out as fractions of the display.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun ScreenSurface(
    detail: VideoStream?,
    base: VideoStream?,
    view: ScreenViewState,
    displayShape: Size?,
    macWidth: Float,
    trackpad: Boolean,
    keyboard: Boolean,
    keys: ScreenKeys,
    send: (String, Map<String, Any?>) -> Unit,
    onKeyboard: (Boolean) -> Unit,
    onEnd: () -> Unit,
) {
    val density = LocalDensity.current
    val hostView = LocalView.current
    val scope = rememberCoroutineScope()
    var box by remember { mutableStateOf(Size.Zero) }
    // Kept by the viewer: the same zoom when it's opened again from the bar.
    var zoom by view::zoom
    var pan by view::pan
    var cursor by view::cursor
    var lastLocal by remember { mutableLongStateOf(Long.MIN_VALUE / 2) }
    var touching by remember { mutableStateOf(false) }
    // The key bar's top (px in this view) while the keyboard's up: what's below it is covered.
    var keyBarTop by remember { mutableFloatStateOf(Float.NaN) }

    val baseSize by (base?.size ?: noSize).collectAsState()
    val detailSize by (detail?.size ?: noSize).collectAsState()
    val crop by (detail?.crop ?: wholeCrop).collectAsState()
    val pointer by (detail?.pointer ?: noPointer).collectAsState()

    // The display's shape: from the base's frames (the detail's come in this
    // screen's shape, whatever they show), else the display's size. Through
    // updated state: the gesture handler outlives this composition.
    val shape by rememberUpdatedState(baseSize?.let { Size(it.width.toFloat(), it.height.toFloat()) } ?: displayShape ?: Size(16f, 10f))
    val mac by rememberUpdatedState(macWidth)
    val input by rememberUpdatedState(send)
    // Clear of a camera cutout; the floating bar's height kept clear at the
    // bottom (so zoomed out everything fits above it); and the keyboard with
    // its key bar while it's up (the screen slides up above it, at the same zoom).
    val cutout = WindowInsets.displayCutout
    val direction = LocalLayoutDirection.current
    val cut by rememberUpdatedState(
        Rect(
            cutout.getLeft(density, direction).toFloat(), cutout.getTop(density).toFloat(),
            cutout.getRight(density, direction).toFloat(), cutout.getBottom(density).toFloat(),
        ),
    )
    val keyboardInset by rememberUpdatedState(if (keyboard && !keyBarTop.isNaN()) max(0f, box.height - keyBarTop) else 0f)
    val bar = with(density) { 76.dp.toPx() }
    val sixty = with(density) { 60.dp.toPx() }

    fun area(bottom: Float) = Rect(cut.left, cut.top, max(cut.left + 1, box.width - cut.right), max(cut.top + 1, box.height - bottom))
    /** Where the display's fitted (above the bar), and where it can be seen now (above the bar, or the keyboard). */
    fun fitted() = ScreenGeometry.fitIn(shape, area(cut.bottom + bar))
    fun seen() = area(max(cut.bottom + bar, keyboardInset))
    fun page(z: Float = zoom, p: Offset = pan) = ScreenGeometry.page(fitted(), z, p, seen())
    fun setPage(r: Rect) {
        pan = ScreenGeometry.panOf(r, fitted())
    }
    fun fraction(p: Offset) = ScreenGeometry.fraction(p, page())

    /** Zoomed in (or the keyboard covering part), the view follows the pointer near its edges. */
    fun follow(force: Boolean = false) {
        val s = seen()
        val r = page()
        if (!force && zoom <= 1.01f && r.height <= s.height + 1) return
        val d = ScreenGeometry.follow(ScreenGeometry.point(cursor, r), s, ScreenGeometry.followMargin(s, sixty))
        setPage(page(p = pan + d))
    }

    fun mouse(type: String, at: Offset, button: String = "left", clicks: Int = 1, pressed: Boolean = false) {
        val a = mutableMapOf<String, Any?>("type" to type, "x" to at.x, "y" to at.y, "button" to button, "clicks" to clicks)
        if (pressed) a["pressed"] = true
        if (keys.mods.isNotEmpty()) a["mods"] = keys.mods.toList()
        input("mouse", a)
    }

    fun moveCursor(to: Offset) {
        cursor = Offset(to.x.coerceIn(0f, 1f), to.y.coerceIn(0f, 1f))
        lastLocal = SystemClock.uptimeMillis()
    }

    /**
     * Trackpad, zoomed in: a pointer outside the part you see (left there as
     * you zoomed, or moved on the Mac) comes in to its nearest point there,
     * rather than the view leaping off after it on the next move.
     */
    fun keepPointerInView() {
        if (!trackpad || zoom <= 1.01f) return
        val s = seen()
        val r = page()
        val to = ScreenGeometry.intoView(ScreenGeometry.point(cursor, r), s, ScreenGeometry.followMargin(s, sixty)) ?: return
        moveCursor(ScreenGeometry.fraction(to, r))
        mouse("move", cursor)
    }

    var glide by remember { mutableStateOf<Job?>(null) }
    val clicks = remember { ClickCounter() }
    fun click(button: String) {
        glide?.cancel()
        val count = clicks.click(button, SystemClock.uptimeMillis())
        mouse("down", cursor, button, count)
        mouse("up", cursor, button, count)
        keys.clearMods()
    }

    var scrollAt by remember { mutableStateOf(Offset.Zero) }
    fun wheel(dx: Float, dy: Float, phase: String) {
        // In the Mac's points: a finger's travel scrolls it as far as it moved over it.
        val r = page()
        if (r.width <= 0f) return
        val k = ScreenGeometry.wheelScale(r.width, mac)
        input("wheel", mapOf("x" to scrollAt.x, "y" to scrollAt.y, "dx" to dx * k, "dy" to dy * k, "phase" to phase))
    }
    fun momentum(v: Offset) {
        glide = scope.launchGlide(v, minSpeed = 80 * density.density, decay = 0.997) { dx, dy -> wheel(dx, dy, "momentum") }
    }

    // Where the Mac says its pointer is, unless this view's input moved it just now (then the Mac's report is behind it).
    LaunchedEffect(pointer) {
        val p = pointer ?: return@LaunchedEffect
        if (SystemClock.uptimeMillis() - lastLocal > 500) cursor = Offset(p.x, p.y)
    }

    // The keyboard coming up: what you tapped (the password field) stays in sight.
    LaunchedEffect(keyboardInset) { follow(force = true) }
    LaunchedEffect(keyboard) { if (!keyboard) keyBarTop = Float.NaN }
    // The keyboard put away by Android (the back gesture, which the keyboard
    // takes itself): the key bar goes with it. One that never came (a
    // hardware keyboard) leaves the bar up.
    val imeUp = WindowInsets.isImeVisible
    var imeCame by remember { mutableStateOf(false) }
    LaunchedEffect(keyboard, imeUp) {
        when {
            !keyboard -> imeCame = false
            imeUp -> imeCame = true
            imeCame -> onKeyboard(false)
        }
    }

    // Tells the computer what part of its display is in view, so it sends
    // that part sharp (ViewAsker: with some to spare, ahead of a pan, once
    // fingers lift). Frames come in this screen's shape.
    LaunchedEffect(detail) {
        val d = detail ?: return@LaunchedEffect
        val asker = ViewAsker()
        while (true) {
            val r = page()
            if (r.width > 0f && box.width > 0f && box.height > 0f) {
                val c = crop
                val px = detailSize?.let { it.width.toFloat() * it.height } ?: 0f
                val ask = asker.next(
                    ScreenGeometry.inSight(r, seen(), zoom), SystemClock.uptimeMillis(), touching,
                    Rect(c.x, c.y, c.x + c.w, c.y + c.h), box.width * box.height, px, box.width / box.height,
                )
                if (ask != null) {
                    if (ask.whole) d.setView(0f, 0f, 0f, 0f, ask.aspect)
                    else d.setView(ask.view.left, ask.view.top, ask.view.width, ask.view.height, ask.aspect)
                }
            }
            delay(100)
        }
    }

    Box(Modifier.fillMaxSize().onSizeChanged { box = Size(it.width.toFloat(), it.height.toFloat()) }) {
        // The touches, apart from the key bar's (else a tap on a key was a click, and let go of the modifiers).
        Box(
            Modifier
                .matchParentSize()
                .clipToBounds()
                // Drags from the screen's edges are the Mac's, not Back (as far as Android allows).
                .systemGestureExclusion()
                .pointerInput(trackpad) {
                    val slop = viewConfiguration.touchSlop
                    val ten = 10.dp.toPx()
                    awaitEachGesture {
                        val down = awaitFirstDown(requireUnconsumed = false)
                        glide?.cancel()
                        touching = true
                        try {
                            var fingers = 1
                            var moved = Offset.Zero
                            var beyondSlop = false
                            var holding = false
                            var scrolling = false
                            val two = TwoFingerJudge(ten)
                            var prevCentre = Offset.Zero
                            var prevSpread = 0f
                            var lastCentre = Offset.Zero
                            var lastPos = down.position
                            val tracker = VelocityTracker()
                            tracker.addPosition(down.uptimeMillis, down.position)
                            val start = down.uptimeMillis
                            while (true) {
                                // One finger held still long enough is a hold.
                                val wait = viewConfiguration.longPressTimeoutMillis - (SystemClock.uptimeMillis() - start)
                                val ev: PointerEvent? = if (fingers == 1 && !beyondSlop && !holding && wait > 0) {
                                    withTimeoutOrNull(wait) { awaitPointerEvent() }
                                } else awaitPointerEvent()
                                if (ev == null) {
                                    holding = true
                                    hostView.performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
                                    if (trackpad) {
                                        // Press and hold, then drag: the button stays down. It starts where you can see it.
                                        keepPointerInView()
                                        mouse("down", cursor)
                                    } else {
                                        // Touch: press and hold right-clicks where you hold.
                                        moveCursor(fraction(lastPos))
                                        click("right")
                                    }
                                    continue
                                }
                                val pressed = ev.changes.filter { it.pressed }
                                if (pressed.isEmpty()) break
                                fingers = max(fingers, pressed.size)
                                if (pressed.size >= 2 && !holding) {
                                    val a = pressed[0].position
                                    val b = pressed[1].position
                                    val centre = (a + b) / 2f
                                    val spread = (a - b).getDistance()
                                    ev.changes.forEach { it.consume() }
                                    if (two.start == null) {
                                        two.judge(centre, spread) // where they landed
                                        // The fingers' centre from here: not the first finger's track before.
                                        tracker.resetTracking()
                                        tracker.addPosition(pressed[0].uptimeMillis, centre)
                                        prevCentre = centre
                                        prevSpread = spread
                                        lastCentre = centre
                                        continue
                                    }
                                    tracker.addPosition(pressed[0].uptimeMillis, centre)
                                    lastCentre = centre
                                    val mode = two.judge(centre, spread)
                                    // Touch mode, zoomed: two fingers move the view (as they do while zooming).
                                    val movesView = !trackpad && zoom > 1.01f
                                    when {
                                        mode == TwoFingers.ZOOM -> {
                                            val before = zoom
                                            val z = (zoom * (if (prevSpread > 0f) spread / prevSpread else 1f)).coerceIn(1f, 16f)
                                            val r = page()
                                            val f = fitted()
                                            val k = z / before
                                            // About the fingers: the point under them stays put, and moves with them.
                                            val nx = centre.x - (prevCentre.x - r.left) * k
                                            val ny = centre.y - (prevCentre.y - r.top) * k
                                            zoom = z
                                            val w = f.width * z
                                            val h = f.height * z
                                            setPage(page(z, Offset(nx + w / 2 - f.center.x, ny + h / 2 - f.center.y)))
                                        }
                                        mode == TwoFingers.SCROLL && movesView -> setPage(page(p = pan + (centre - prevCentre)))
                                        mode == TwoFingers.SCROLL -> {
                                            if (!scrolling) {
                                                // Two fingers only just settled on scrolling: a scroll from here.
                                                scrollAt = if (trackpad) cursor else fraction(centre)
                                                scrolling = true
                                                wheel(0f, 0f, "began")
                                            }
                                            val d = centre - prevCentre
                                            wheel(d.x, d.y, "changed")
                                        }
                                        else -> {}
                                    }
                                    prevCentre = centre
                                    // Undecided, the spread builds up, to be zoomed by at once if it's a zoom.
                                    if (mode != TwoFingers.UNDECIDED) prevSpread = spread
                                    continue
                                }
                                if (fingers > 1 && !holding) {
                                    ev.changes.forEach { it.consume() }
                                    continue // one of two lifted: the rest of a two-finger gesture
                                }
                                val ch = pressed[0]
                                val d = ch.positionChange()
                                lastPos = ch.position
                                tracker.addPosition(ch.uptimeMillis, ch.position)
                                if (!beyondSlop && !holding) {
                                    moved += d
                                    if (moved.getDistance() < slop) continue
                                    beyondSlop = true
                                    if (trackpad) keepPointerInView()
                                }
                                ch.consume()
                                val r = page()
                                if (r.width <= 0f) continue
                                if (holding && trackpad) {
                                    // A held drag: the pointer moves with the finger, the button down.
                                    moveCursor(cursor + Offset(d.x / r.width, d.y / r.height))
                                    follow()
                                    mouse("move", cursor, pressed = true)
                                } else if (trackpad) {
                                    // Relative, as on a trackpad, a little faster when quick.
                                    val v = tracker.calculateVelocity()
                                    val k = ScreenGeometry.acceleration(hypot(v.x, v.y) / density.density)
                                    moveCursor(cursor + Offset(d.x * k / r.width, d.y * k / r.height))
                                    follow()
                                    mouse("move", cursor)
                                } else if (!holding) {
                                    // Touch: one finger scrolls where it is.
                                    if (!scrolling) {
                                        scrollAt = fraction(down.position)
                                        scrolling = true
                                        wheel(0f, 0f, "began")
                                    }
                                    wheel(d.x, d.y, "changed")
                                }
                            }
                            val movesView = !trackpad && zoom > 1.01f
                            when {
                                holding && trackpad -> {
                                    mouse("up", cursor)
                                    keys.clearMods()
                                }
                                scrolling -> {
                                    wheel(0f, 0f, "ended")
                                    momentum(tracker.calculateVelocity().let { Offset(it.x, it.y) })
                                }
                                two.mode == TwoFingers.ZOOM -> keepPointerInView()
                                holding || beyondSlop || two.mode != TwoFingers.UNDECIDED -> {}
                                fingers == 2 -> {
                                    val v = tracker.calculateVelocity().let { Offset(it.x, it.y) }
                                    if (!movesView && isFlick(v / density.density)) {
                                        // A quick flick, over before it could be told: a scroll.
                                        scrollAt = if (trackpad) cursor else fraction(lastCentre)
                                        wheel(0f, 0f, "began")
                                        wheel(0f, 0f, "ended")
                                        momentum(v)
                                    } else {
                                        if (!trackpad) two.start?.let { moveCursor(fraction(it.first)) }
                                        click("right")
                                    }
                                }
                                fingers == 1 -> {
                                    if (!trackpad) moveCursor(fraction(down.position))
                                    click("left")
                                }
                            }
                        } finally {
                            touching = false
                        }
                    }
                },
        ) {
            val f = fitted()
            val r = page()
            // The page: the whole display (the base) filling it, the detail over the part its frames show.
            Box(
                Modifier
                    .offset { IntOffset(f.left.roundToInt(), f.top.roundToInt()) }
                    .size(with(density) { f.width.toDp() }, with(density) { f.height.toDp() })
                    .graphicsLayer {
                        transformOrigin = TransformOrigin(0f, 0f)
                        scaleX = zoom
                        scaleY = zoom
                        translationX = r.left - f.left
                        translationY = r.top - f.top
                    }
                    .background(Color(0xFF141414)),
            ) {
                base?.let { VideoSurface(it, Modifier.fillMaxSize()) }
                detail?.let {
                    with(density) {
                        VideoSurface(
                            it,
                            Modifier
                                .offset { IntOffset((crop.x * f.width).roundToInt(), (crop.y * f.height).roundToInt()) }
                                .size((crop.w * f.width).toDp(), (crop.h * f.height).toDp()),
                        )
                    }
                }
            }
            // The pointer (it isn't in the picture), drawn here at once. Hidden
            // when it's on another display, or (Touch mode) not known yet.
            val p = pointer
            val hidden = p?.visible == false || (!trackpad && p == null)
            if (!hidden && r.width > 0f) {
                PointerView(p, ScreenGeometry.point(cursor, r), ScreenGeometry.pointerScale(r.width / density.density, mac))
            }
        }
        // The keyboard and its bar.
        KeyCatcher(keyboard, onTyped = keys::typed, onDismissed = { onKeyboard(false) }, modifier = Modifier.size(1.dp))
        if (keyboard) {
            ModifierBar(
                keys, onHide = { onKeyboard(false) }, onEnd = onEnd,
                modifier = Modifier.align(Alignment.BottomCenter).imePadding().onGloballyPositioned { keyBarTop = it.positionInParent().y },
            )
        }
    }
}

/** Where the screen's zoomed to and where its pointer is: kept by the viewer while it's minimised. */
@Stable
internal class ScreenViewState {
    var zoom by mutableFloatStateOf(1f)
    var pan by mutableStateOf(Offset.Zero)
    var cursor by mutableStateOf(Offset(0.5f, 0.5f))
}

/** The computer's pointer at `tip` (px): its own shape once it's come, else an arrow. k: its size, from ScreenGeometry.pointerScale. */
@Composable
private fun PointerView(p: ScreenPointer?, tip: Offset, k: Float) {
    val density = LocalDensity.current
    val png = p?.png
    val image: ImageBitmap? = remember(p?.shapeID, png != null) { png?.let { BitmapFactory.decodeByteArray(it, 0, it.size)?.asImageBitmap() } }
    if (p != null && image != null && p.width > 0f) {
        // The Mac's shape, its hot spot on the spot.
        Image(
            image, null,
            Modifier
                .offset { IntOffset((tip.x - p.hotX * k * density.density).roundToInt(), (tip.y - p.hotY * k * density.density).roundToInt()) }
                .size((p.width * k).dp, (p.height * k).dp),
            contentScale = ContentScale.FillBounds,
        )
    } else {
        Canvas(Modifier.offset { IntOffset((tip.x - k * density.density).roundToInt(), (tip.y - k * density.density).roundToInt()) }.size((16 * k).dp, (22 * k).dp)) {
            val w = size.width
            val h = size.height
            val arrow = Path().apply {
                moveTo(w / 16, h / 22)
                lineTo(w / 16, h * 17 / 22)
                lineTo(w * 5.5f / 16, h * 13 / 22)
                lineTo(w * 8.5f / 16, h * 20 / 22)
                lineTo(w * 11 / 16, h * 19 / 22)
                lineTo(w * 8 / 16, h * 12 / 22)
                lineTo(w * 14 / 16, h * 12 / 22)
                close()
            }
            drawPath(arrow, Color.Black)
            drawPath(arrow, Color.White, style = Stroke(width = 1.2f * k * density.density))
        }
    }
}

/** esc, tab, ⌃ ⌥ ⌘ ⇧, arrows, more keys, hide and End, above the keyboard. A modifier holds for the next key; double-tap locks it. */
@Composable
private fun ModifierBar(keys: ScreenKeys, onHide: () -> Unit, onEnd: () -> Unit, modifier: Modifier) {
    var arrows by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    val view = LocalView.current
    Surface(modifier.fillMaxWidth(), tonalElevation = 3.dp) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(5.dp), verticalAlignment = Alignment.CenterVertically) {
            val each = Modifier.weight(1f)
            BarKey("esc", each, label = "Escape") { keys.key(MacKeys.ESCAPE) }
            BarKey("tab", each, label = "Tab") { keys.key(MacKeys.TAB) }
            for ((m, symbol, label) in listOf(Triple("ctrl", "⌃", "Control"), Triple("alt", "⌥", "Option"), Triple("meta", "⌘", "Command"), Triple("shift", "⇧", "Shift"))) {
                BarKey(symbol, each, label = label, on = m in keys.mods, held = m in keys.held) {
                    view.performHapticFeedback(HapticFeedbackConstants.KEYBOARD_TAP)
                    keys.tapMod(m)
                }
            }
            Box(each) {
                BarKey("", Modifier.fillMaxWidth(), label = "Arrow keys", icon = { Icon(Icons.Filled.OpenWith, null) }) { arrows = true }
                DropdownMenu(arrows, { arrows = false }) {
                    for ((t, vk) in listOf("Up" to MacKeys.UP, "Down" to MacKeys.DOWN, "Left" to MacKeys.LEFT, "Right" to MacKeys.RIGHT)) {
                        DropdownMenuItem({ Text(t) }, { arrows = false; keys.key(vk) })
                    }
                }
            }
            Box(each) {
                BarKey("…", Modifier.fillMaxWidth(), label = "More keys") { more = true }
                DropdownMenu(more, { more = false }) {
                    for ((t, vk) in listOf("Delete Forward" to MacKeys.FORWARD_DELETE, "Home" to MacKeys.HOME, "End" to MacKeys.END,
                        "Page Up" to MacKeys.PAGE_UP, "Page Down" to MacKeys.PAGE_DOWN)) {
                        DropdownMenuItem({ Text(t) }, { more = false; keys.key(vk) })
                    }
                    MacKeys.FUNCTION.forEachIndexed { i, vk -> DropdownMenuItem({ Text("F${i + 1}") }, { more = false; keys.key(vk) }) }
                }
            }
            BarKey("", each, label = "Hide keyboard", icon = { Icon(Icons.Filled.KeyboardHide, null) }, onClick = onHide)
            // End without putting the keyboard away first (the bar's End is under it).
            Box(
                each.heightIn(min = 40.dp).background(Color(0xFFFF3B30), RoundedCornerShape(8.dp)).clickable(onClick = onEnd),
                contentAlignment = Alignment.Center,
            ) {
                Icon(Icons.Filled.Close, "End screen sharing", tint = Color.White)
            }
        }
    }
}

@Composable
private fun BarKey(
    title: String,
    modifier: Modifier,
    label: String,
    on: Boolean = false,
    held: Boolean = false,
    icon: (@Composable () -> Unit)? = null,
    onClick: () -> Unit,
) {
    // Keys as the keyboard draws them; a modifier held for the next key in the
    // tint's container, one locked on in the tint.
    val bg = when {
        held -> MaterialTheme.colorScheme.primary
        on -> MaterialTheme.colorScheme.primaryContainer
        else -> MaterialTheme.colorScheme.surfaceVariant
    }
    val fg = when {
        held -> MaterialTheme.colorScheme.onPrimary
        on -> MaterialTheme.colorScheme.onPrimaryContainer
        else -> MaterialTheme.colorScheme.onSurface
    }
    Box(
        modifier
            .heightIn(min = 40.dp)
            .background(bg, RoundedCornerShape(8.dp))
            .clickable(onClick = onClick)
            .semantics(mergeDescendants = true) {
                contentDescription = if (held) "$label, locked" else label
                if (on) selected = true
            },
        contentAlignment = Alignment.Center,
    ) {
        if (icon != null) icon()
        else Column(horizontalAlignment = Alignment.CenterHorizontally) {
            Text(title, style = MaterialTheme.typography.labelLarge, color = fg)
            if (held) Text("locked", style = MaterialTheme.typography.labelSmall, color = fg)
        }
    }
}
