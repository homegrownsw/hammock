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
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.ContentPaste
import androidx.compose.material.icons.filled.DesktopMac
import androidx.compose.material.icons.filled.Keyboard
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
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
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
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.IntOffset
import androidx.compose.ui.unit.dp
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.max
import kotlin.math.min
import kotlin.math.pow

/**
 * A computer's whole screen (screen sharing, ADR-0014): its display as live
 * video, in two layers (a small, cheap one of the whole display, and one about
 * this phone's size that, zoomed in, the computer cuts from the display at full
 * resolution: SetMediaView), with its pointer drawn here at once. Driven as a
 * trackpad (drag moves the pointer, tap clicks, two fingers scroll or
 * right-click, hold and drag drags) or by touch (tap clicks where you tap),
 * pinch to zoom, and the keyboard with a bar of the Mac's modifier keys.
 * Shown full screen by the caller; Back or End closes it.
 */
@Composable
fun ScreenViewer(model: AppModel, computerID: String, name: String, onClose: () -> Unit) {
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

    BackHandler { if (keyboard) keyboard = false else onClose() }
    HideSystemBars()

    LaunchedEffect(computerID) {
        val r = model.listDisplays(computerID)
        if (r == null) unavailable = "$name didn't answer."
        else {
            displays = r.displaysList
            unavailable = r.unavailable.ifEmpty { null }
        }
        loaded = true
    }

    // The streams, while the app's in the foreground: back from elsewhere, it picks up again.
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val life by lifecycle.currentStateFlow.collectAsStateWithLifecycle()
    val foreground = life.isAtLeast(Lifecycle.State.STARTED)
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
        // a pan shows what's there at once while the detail catches up.
        val b = VideoStream.screen(link, display, cid, maxPixels = 1280 * 720, maxFPS = 15, background = true)
        detail = d
        base = b
        try {
            b.start()
            d.start()
            d.state.collect { state = it }
        } finally {
            d.stop()
            b.stop()
            detail = null
            base = null
        }
    }

    val shown = displays.firstOrNull { if (display == 0) it.main else it.id == display } ?: displays.firstOrNull()
    val macWidth = shown?.width?.toFloat() ?: 0f
    val playing = state == VideoStream.State.Playing
    val locked by (detail?.locked ?: notLocked).collectAsState()
    val send: (String, Map<String, Any?>) -> Unit = { op, args -> model.screenInput(computerID, display, op, args) }

    Box(Modifier.fillMaxSize().background(Color.Black)) {
        val why = unavailable
        if (why != null) {
            Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { Unavailable("Can't Share $name's Screen", why, onDark = true) }
        } else {
            ScreenSurface(
                detail = detail,
                base = base,
                displayShape = shown?.let { Size(it.width.toFloat(), it.height.toFloat()) },
                macWidth = macWidth,
                trackpad = trackpad,
                keyboard = keyboard,
                locked = locked,
                send = send,
                onKeyboard = { keyboard = it },
                onEnd = onClose,
            )
        }
        (state as? VideoStream.State.Failed)?.let { f ->
            if (why == null) {
                Surface(shape = RoundedCornerShape(16.dp), tonalElevation = 3.dp, modifier = Modifier.align(Alignment.Center).padding(24.dp)) {
                    Column(Modifier.padding(20.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                        Text("The screen stopped", style = MaterialTheme.typography.titleMedium)
                        Text(f.reason, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Spacer(Modifier.size(10.dp))
                        Button(onClick = { session++ }) { Text("Try Again") }
                    }
                }
            }
        }
        // Top: the lock screen's prompt, or how to drive it, for a moment.
        Box(Modifier.align(Alignment.TopCenter).statusBarsPadding().padding(top = 8.dp, start = 12.dp, end = 12.dp)) {
            if (locked && playing && !keyboard) {
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
                        Text("Keyboard")
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
            Controls(
                trackpad = trackpad,
                onTrackpad = { trackpad = it },
                displays = displays,
                display = display,
                onDisplay = { display = it },
                playing = playing,
                onKeyboard = { keyboard = true },
                onPaste = {
                    val text = context.getSystemService(ClipboardManager::class.java)?.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
                    if (text.isNullOrEmpty()) model.showToast("Your clipboard has no text") else send("text", mapOf("text" to text))
                },
                onEnd = onClose,
                modifier = Modifier.align(Alignment.BottomCenter).navigationBarsPadding().padding(bottom = 8.dp, start = 12.dp, end = 12.dp),
            )
        }
    }
}

/** The floating bar: Trackpad or Touch, the keyboard, the display, paste, the state and End. */
@Composable
private fun Controls(
    trackpad: Boolean,
    onTrackpad: (Boolean) -> Unit,
    displays: List<Hammock.Display>,
    display: Int,
    onDisplay: (Int) -> Unit,
    playing: Boolean,
    onKeyboard: () -> Unit,
    onPaste: () -> Unit,
    onEnd: () -> Unit,
    modifier: Modifier,
) {
    var picking by remember { mutableStateOf(false) }
    Row(
        modifier
            .background(Color.Black.copy(alpha = 0.72f), CircleShape)
            .border(1.dp, Color.White.copy(alpha = 0.18f), CircleShape)
            .padding(6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(2.dp),
    ) {
        Row(Modifier.background(Color.White.copy(alpha = 0.14f), CircleShape).padding(2.dp)) {
            ModeButton("Trackpad", trackpad) { onTrackpad(true) }
            ModeButton("Touch", !trackpad) { onTrackpad(false) }
        }
        IconButton(onClick = onKeyboard) { Icon(Icons.Filled.Keyboard, "Keyboard", tint = Color.White) }
        if (displays.size > 1) {
            Box {
                IconButton(onClick = { picking = true }) { Icon(Icons.Filled.DesktopMac, "Display", tint = Color.White) }
                DropdownMenu(picking, { picking = false }) {
                    for (d in displays) {
                        val id = if (d.main) 0 else d.id
                        DropdownMenuItem({ Text(d.name + if (id == display) "  ✓" else "") }, { picking = false; onDisplay(id) })
                    }
                }
            }
        }
        IconButton(onClick = onPaste) { Icon(Icons.Filled.ContentPaste, "Paste to the Mac", tint = Color.White) }
        Row(Modifier.padding(horizontal = 6.dp), verticalAlignment = Alignment.CenterVertically) {
            Box(Modifier.size(8.dp).background(if (playing) Color(0xFF34C759) else Color(0xFFFF9F0A), CircleShape))
            Text(if (playing) "Live" else "Connecting", color = Color.White, style = MaterialTheme.typography.labelMedium, modifier = Modifier.padding(start = 6.dp))
        }
        IconButton(onClick = onEnd, modifier = Modifier.size(36.dp).background(Color(0xFFFF3B30), CircleShape)) {
            Icon(Icons.Filled.Close, "End screen sharing", tint = Color.White)
        }
    }
}

@Composable
private fun ModeButton(title: String, on: Boolean, onClick: () -> Unit) {
    Text(
        title,
        color = if (on) Color.Black else Color.White,
        fontWeight = if (on) FontWeight.SemiBold else FontWeight.Normal,
        style = MaterialTheme.typography.bodyMedium,
        modifier = Modifier
            .heightIn(min = 40.dp)
            .background(if (on) Color.White else Color.Transparent, CircleShape)
            .clickable(onClick = onClick)
            .padding(horizontal = 14.dp, vertical = 10.dp),
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

/** What two fingers are doing, settled once they've moved enough to tell, then kept till they lift. */
private enum class TwoFingers { UNDECIDED, ZOOM, SCROLL }

/**
 * The display, zoomed and panned, taking touches; the keyboard and its bar of
 * modifier keys. Positions go out as fractions of the display.
 */
@Composable
private fun ScreenSurface(
    detail: VideoStream?,
    base: VideoStream?,
    displayShape: Size?,
    macWidth: Float,
    trackpad: Boolean,
    keyboard: Boolean,
    locked: Boolean,
    send: (String, Map<String, Any?>) -> Unit,
    onKeyboard: (Boolean) -> Unit,
    onEnd: () -> Unit,
) {
    val density = LocalDensity.current
    val view = LocalView.current
    val scope = rememberCoroutineScope()
    var box by remember { mutableStateOf(Size.Zero) }
    var zoom by remember { mutableFloatStateOf(1f) }
    var pan by remember { mutableStateOf(Offset.Zero) }
    var cursor by remember { mutableStateOf(Offset(0.5f, 0.5f)) }
    var lastLocal by remember { mutableLongStateOf(0L) }
    val keys = remember { ScreenKeys(send) }
    keys.locked = locked

    val baseSize by (base?.size ?: noSize).collectAsState()
    val crop by (detail?.crop ?: wholeCrop).collectAsState()
    val pointer by (detail?.pointer ?: noPointer).collectAsState()

    // The display's shape: from the base's frames, else the display's size.
    // Through updated state: the gesture handler outlives this composition.
    val shape by rememberUpdatedState(baseSize?.let { Size(it.width.toFloat(), it.height.toFloat()) } ?: displayShape ?: Size(16f, 10f))
    val mac by rememberUpdatedState(macWidth)
    val input by rememberUpdatedState(send)
    // The floating bar's height is kept clear at the bottom (and, with the keyboard, its bar).
    val bar = with(density) { 76.dp.toPx() }
    fun area() = Rect(0f, 0f, box.width, max(0f, box.height - bar))
    fun fitted() = fit(shape, area().size)

    /** Where the page is: fitted, zoomed about its centre, panned, kept covering what can be seen. */
    fun page(z: Float = zoom, p: Offset = pan): Rect {
        val area = area()
        val fitted = fitted()
        val w = fitted.width * z
        val h = fitted.height * z
        val cx = area.center.x + p.x
        val cy = area.center.y + p.y
        var x = cx - w / 2
        var y = cy - h / 2
        x = if (w > area.width) x.coerceIn(area.right - w, area.left) else area.center.x - w / 2
        y = if (h > area.height) y.coerceIn(area.bottom - h, area.top) else area.center.y - h / 2
        return Rect(x, y, x + w, y + h)
    }

    fun setPan(r: Rect) {
        val area = area()
        pan = Offset(r.center.x - area.center.x, r.center.y - area.center.y)
    }

    fun fraction(p: Offset): Offset {
        val r = page()
        if (r.width <= 0f || r.height <= 0f) return Offset(0.5f, 0.5f)
        return Offset(((p.x - r.left) / r.width).coerceIn(0f, 1f), ((p.y - r.top) / r.height).coerceIn(0f, 1f))
    }

    /** Zoomed in, the view follows the pointer near its edges. */
    fun follow() {
        val area = area()
        val r = page()
        if (zoom <= 1.01f) return
        val tip = Offset(r.left + cursor.x * r.width, r.top + cursor.y * r.height)
        val margin = min(with(density) { 60.dp.toPx() }, area.height / 4)
        var p = pan
        if (tip.x < area.left + margin) p += Offset(area.left + margin - tip.x, 0f)
        if (tip.x > area.right - margin) p -= Offset(tip.x - (area.right - margin), 0f)
        if (tip.y < area.top + margin) p += Offset(0f, area.top + margin - tip.y)
        if (tip.y > area.bottom - margin) p -= Offset(0f, tip.y - (area.bottom - margin))
        setPan(page(p = p))
    }

    fun mouse(type: String, at: Offset, button: String = "left", clicks: Int = 1, pressed: Boolean = false) {
        val a = mutableMapOf<String, Any?>("type" to type, "x" to at.x, "y" to at.y, "button" to button, "clicks" to clicks)
        if (pressed) a["pressed"] = true
        if (keys.mods.isNotEmpty()) a["mods"] = keys.mods.toList()
        input("mouse", a)
    }

    // Taps close together are a double (or triple) click.
    val taps = remember { longArrayOf(0L, 0L) } // when, how many
    fun click(button: String) {
        val now = SystemClock.uptimeMillis()
        val count = if (button == "left" && now - taps[0] < 400) min(taps[1] + 1, 3) else 1
        taps[0] = now
        taps[1] = count
        mouse("down", cursor, button, count.toInt())
        mouse("up", cursor, button, count.toInt())
        keys.clearMods()
    }

    fun moveCursor(to: Offset) {
        cursor = Offset(to.x.coerceIn(0f, 1f), to.y.coerceIn(0f, 1f))
        lastLocal = SystemClock.uptimeMillis()
    }

    var scrollAt by remember { mutableStateOf(Offset.Zero) }
    fun wheel(dx: Float, dy: Float, phase: String) {
        // In the Mac's points: a finger's travel scrolls it as far as it moved over it.
        val r = page()
        if (r.width <= 0f) return
        val k = if (mac > 0) mac / r.width else 1f
        input("wheel", mapOf("x" to scrollAt.x, "y" to scrollAt.y, "dx" to dx * k, "dy" to dy * k, "phase" to phase))
    }
    var glide by remember { mutableStateOf<Job?>(null) }
    fun momentum(v: Offset) {
        glide = scope.launchGlide(v, minSpeed = 80 * density.density, decay = 0.997) { dx, dy -> wheel(dx, dy, "momentum") }
    }

    // Tells the computer what part of its display is in view, at most ten times
    // a second, so it sends that part sharp; with some to spare all round, so
    // a pan stays sharp. Frames come in this screen's shape.
    LaunchedEffect(detail) {
        val d = detail ?: return@LaunchedEffect
        var sent: FloatArray? = null
        snapshotFlow { Triple(page(), area(), zoom) }.collectLatest { (r, a, z) ->
            if (r.width <= 0f || r.height <= 0f || a.height <= 0f) return@collectLatest
            val seen = a.intersect(r)
            var x = (seen.left - r.left) / r.width
            var y = (seen.top - r.top) / r.height
            var w = seen.width / r.width
            var h = seen.height / r.height
            if (z <= 1.02f || w >= 0.98f) {
                x = 0f; y = 0f; w = 0f; h = 0f
            } else {
                val sx = w * 0.1f
                val sy = h * 0.1f
                x = max(0f, x - sx); y = max(0f, y - sy)
                w = min(1f - x, w + 2 * sx); h = min(1f - y, h + 2 * sy)
            }
            val aspect = a.width / a.height
            val v = floatArrayOf(x, y, w, h, aspect)
            val prev = sent
            if (prev != null && v.indices.all { abs(v[it] - prev[it]) < 0.01f }) return@collectLatest
            sent = v
            d.setView(x, y, w, h, aspect)
            delay(100)
        }
    }

    Box(
        Modifier
            .fillMaxSize()
            .clipToBounds()
            .onSizeChanged { box = Size(it.width.toFloat(), it.height.toFloat()) }
            .pointerInput(trackpad) {
                val slop = viewConfiguration.touchSlop
                val ten = 10.dp.toPx()
                awaitEachGesture {
                    val down = awaitFirstDown(requireUnconsumed = false)
                    glide?.cancel()
                    var fingers = 1
                    var moved = Offset.Zero
                    var beyondSlop = false
                    var holding = false
                    var scrolling = false
                    var two = TwoFingers.UNDECIDED
                    var twoStart: Pair<Offset, Float>? = null
                    var prevCentre = Offset.Zero
                    var prevSpread = 0f
                    var lastPos = down.position
                    val tracker = VelocityTracker()
                    val start = down.uptimeMillis
                    while (true) {
                        // One finger held still long enough is a hold.
                        val wait = viewConfiguration.longPressTimeoutMillis - (SystemClock.uptimeMillis() - start)
                        val ev: PointerEvent? = if (fingers == 1 && !beyondSlop && !holding && wait > 0) {
                            withTimeoutOrNull(wait) { awaitPointerEvent() }
                        } else awaitPointerEvent()
                        if (ev == null) {
                            holding = true
                            view.performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
                            if (trackpad) {
                                // Press and hold, then drag: the button stays down.
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
                            tracker.addPosition(pressed[0].uptimeMillis, centre)
                            ev.changes.forEach { it.consume() }
                            val s = twoStart
                            if (s == null) {
                                twoStart = centre to spread
                                prevCentre = centre
                                prevSpread = spread
                                continue
                            }
                            if (two == TwoFingers.UNDECIDED) {
                                val travel = (centre - s.first).getDistance()
                                val change = abs(spread - s.second)
                                if (max(travel, change) >= ten) two = if (change > travel) TwoFingers.ZOOM else TwoFingers.SCROLL
                            }
                            // Touch mode, zoomed: two fingers move the view (as they do while zooming).
                            val movesView = !trackpad && zoom > 1.01f
                            when {
                                two == TwoFingers.ZOOM -> {
                                    val before = zoom
                                    val z = (zoom * (if (prevSpread > 0f) spread / prevSpread else 1f)).coerceIn(1f, 16f)
                                    val r = page()
                                    val area = area()
                                    val fitted = fitted()
                                    val k = z / before
                                    // About the fingers: the point under them stays put, and moves with them.
                                    val nx = centre.x - (prevCentre.x - r.left) * k
                                    val ny = centre.y - (prevCentre.y - r.top) * k
                                    zoom = z
                                    val w = fitted.width * z
                                    val h = fitted.height * z
                                    setPan(page(p = Offset(nx + w / 2 - area.center.x, ny + h / 2 - area.center.y)))
                                }
                                two == TwoFingers.SCROLL && movesView -> setPan(page(p = pan + (centre - prevCentre)))
                                two == TwoFingers.SCROLL -> {
                                    if (!scrolling) {
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
                            prevSpread = spread
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
                        if (!beyondSlop) {
                            moved += d
                            if (moved.getDistance() < slop) continue
                            beyondSlop = true
                        }
                        ch.consume()
                        val r = page()
                        if (r.width <= 0f) continue
                        if (holding && trackpad) {
                            moveCursor(cursor + Offset(d.x / r.width, d.y / r.height))
                            follow()
                            mouse("move", cursor, pressed = true)
                        } else if (trackpad) {
                            // Relative, as on a trackpad, a little faster when quick.
                            val v = tracker.calculateVelocity()
                            val k = 1 + min(hypot(v.x, v.y) / density.density / 1500f, 1.5f)
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
                    when {
                        holding && trackpad -> {
                            mouse("up", cursor)
                            keys.clearMods()
                        }
                        scrolling -> {
                            wheel(0f, 0f, "ended")
                            momentum(tracker.calculateVelocity().let { Offset(it.x, it.y) })
                        }
                        holding || beyondSlop || two != TwoFingers.UNDECIDED -> {}
                        fingers == 2 -> {
                            if (!trackpad) twoStart?.let { moveCursor(fraction(it.first)) }
                            click("right")
                        }
                        fingers == 1 -> {
                            if (!trackpad) moveCursor(fraction(down.position))
                            click("left")
                        }
                    }
                }
            },
    ) {
        val r = page()
        val area = area()
        val fitted = fitted()
        // The page: the base filling it, the detail over the part its frames show.
        Box(
            Modifier
                .offset { IntOffset(area.left.toInt() + ((area.width - fitted.width) / 2).toInt(), area.top.toInt() + ((area.height - fitted.height) / 2).toInt()) }
                .size(with(density) { fitted.width.toDp() }, with(density) { fitted.height.toDp() })
                .graphicsLayer {
                    transformOrigin = TransformOrigin(0f, 0f)
                    scaleX = zoom
                    scaleY = zoom
                    translationX = r.left - (area.left + (area.width - fitted.width) / 2)
                    translationY = r.top - (area.top + (area.height - fitted.height) / 2)
                },
        ) {
            base?.let { VideoSurface(it, Modifier.fillMaxSize()) }
            detail?.let {
                with(density) {
                    VideoSurface(
                        it,
                        Modifier
                            .offset { IntOffset((crop.x * fitted.width).toInt(), (crop.y * fitted.height).toInt()) }
                            .size((crop.w * fitted.width).toDp(), (crop.h * fitted.height).toDp()),
                    )
                }
            }
        }
        // The pointer, drawn here at once: where this view's input put it
        // while that's recent, else where the computer says it is.
        val p = pointer
        val at = if (p != null && SystemClock.uptimeMillis() - lastLocal > 500) Offset(p.x, p.y) else cursor
        val hidden = p?.visible == false || (!trackpad && p == null)
        if (!hidden && r.width > 0f) {
            PointerView(p, Offset(r.left + at.x * r.width, r.top + at.y * r.height), if (mac > 0) (r.width / density.density) / mac else 1f)
        }
        // The keyboard and its bar.
        KeyCatcher(keyboard, onTyped = keys::typed, onDismissed = { onKeyboard(false) }, modifier = Modifier.size(1.dp))
        if (keyboard) {
            ModifierBar(keys, onHide = { onKeyboard(false) }, onEnd = onEnd, modifier = Modifier.align(Alignment.BottomCenter).imePadding())
        }
    }
}

/** The computer's pointer at `tip` (px): its own shape once it's come, else an arrow. scale: phone dp per Mac point. */
@Composable
private fun PointerView(p: ScreenPointer?, tip: Offset, scale: Float) {
    val density = LocalDensity.current
    // As big as the Mac's own shown about 1:1, a little smaller zoomed right out, and not huge zoomed right in.
    val k = scale.pow(0.6f).coerceIn(0.75f, 3f)
    val png = p?.png
    val image: ImageBitmap? = remember(p?.shapeID, png != null) { png?.let { BitmapFactory.decodeByteArray(it, 0, it.size)?.asImageBitmap() } }
    if (p != null && image != null && p.width > 0f) {
        with(density) {
            Image(
                image, null,
                Modifier
                    .offset { IntOffset((tip.x - p.hotX * k * density.density).toInt(), (tip.y - p.hotY * k * density.density).toInt()) }
                    .size((p.width * k).dp, (p.height * k).dp),
                contentScale = ContentScale.FillBounds,
            )
        }
    } else {
        Canvas(Modifier.offset { IntOffset(tip.x.toInt(), tip.y.toInt()) }.size((16 * k).dp, (22 * k).dp)) {
            val w = size.width
            val h = size.height
            val arrow = Path().apply {
                moveTo(0f, 0f)
                lineTo(0f, h * 0.78f)
                lineTo(w * 0.3f, h * 0.6f)
                lineTo(w * 0.5f, h)
                lineTo(w * 0.65f, h * 0.93f)
                lineTo(w * 0.45f, h * 0.56f)
                lineTo(w * 0.85f, h * 0.56f)
                close()
            }
            drawPath(arrow, Color.Black)
            drawPath(arrow, Color.White, style = Stroke(width = 1.5f * density.density))
        }
    }
}

/**
 * The keys typed to the screen, and the modifiers for the next one (or
 * click): a modifier holds for one key, double-tapped it stays on.
 */
private class ScreenKeys(private val send: (String, Map<String, Any?>) -> Unit) {
    var mods by mutableStateOf(setOf<String>())
    var held by mutableStateOf(setOf<String>())
    /** The Mac's at its lock screen: typing goes as keys (its password field may take only those). */
    var locked = false
    private var lastModTap = "" to 0L

    fun clearMods() {
        mods = held
    }

    fun tapMod(m: String) {
        val now = SystemClock.uptimeMillis()
        val double = lastModTap.first == m && now - lastModTap.second < 350
        lastModTap = m to now
        when {
            double -> if (m in held) { held = held - m; mods = mods - m } else { held = held + m; mods = mods + m }
            m in mods -> { mods = mods - m; held = held - m }
            else -> mods = mods + m
        }
    }

    fun key(vk: Int, extra: Set<String> = emptySet()) {
        val m = (mods + extra).toList()
        send("key", mapOf("type" to "down", "vk" to vk, "mods" to m))
        send("key", mapOf("type" to "up", "vk" to vk, "mods" to m))
        clearMods()
    }

    fun typed(t: Typed) {
        when (t) {
            Typed.Enter -> key(0x24)
            Typed.Backspace -> key(0x33)
            is Typed.Text -> text(t.text)
            is Typed.Key -> hardware(t.keyCode, t.meta)
        }
    }

    private fun text(text: String) {
        // With a modifier, the key itself (⌘C is C's key); else the text, whatever the Mac's layout.
        if (mods.isNotEmpty() && text.length == 1) {
            val c = text[0]
            MacKeys.codes[c.lowercaseChar()]?.let { vk ->
                key(vk, if (c != c.lowercaseChar()) setOf("shift") else emptySet())
                return
            }
        }
        if (locked) {
            // Key by key, with shift where the character needs it (a US layout), for a password field.
            for (c in text) {
                val lower = c.lowercaseChar()
                val vk = MacKeys.codes[lower]
                val shifted = MacKeys.shifted[c]
                when {
                    vk != null -> key(vk, if (c != lower) setOf("shift") else emptySet())
                    shifted != null -> key(shifted, setOf("shift"))
                    else -> send("text", mapOf("text" to c.toString()))
                }
            }
            return
        }
        send("text", mapOf("text" to text))
        clearMods()
    }

    /** A hardware keyboard's key with no text of its own, as the Mac's. */
    private fun hardware(keyCode: Int, meta: Int) {
        val m = buildSet {
            if (meta and android.view.KeyEvent.META_CTRL_ON != 0) add("ctrl")
            if (meta and android.view.KeyEvent.META_ALT_ON != 0) add("alt")
            if (meta and android.view.KeyEvent.META_META_ON != 0) add("meta")
            if (meta and android.view.KeyEvent.META_SHIFT_ON != 0) add("shift")
        }
        val vk = MacKeys.fromAndroid(keyCode) ?: return
        key(vk, m)
    }
}

/** Mac virtual key codes (kVK_*), as on a US keyboard. */
internal object MacKeys {
    val codes: Map<Char, Int> = buildMap {
        putAll(mapOf(' ' to 49, '-' to 27, '=' to 24, '[' to 33, ']' to 30, '\\' to 42, ';' to 41, '\'' to 39, ',' to 43, '.' to 47, '/' to 44, '`' to 50))
        val letters = "abcdefghijklmnopqrstuvwxyz"
        val letterCodes = intArrayOf(0, 11, 8, 2, 14, 3, 5, 4, 34, 38, 40, 37, 46, 45, 31, 35, 12, 15, 1, 17, 32, 9, 13, 7, 16, 6)
        letters.forEachIndexed { i, c -> put(c, letterCodes[i]) }
        val digitCodes = intArrayOf(29, 18, 19, 20, 21, 23, 22, 26, 28, 25)
        for (i in 0..9) put('0' + i, digitCodes[i])
    }

    /** Characters typed with shift on a US keyboard, to the key they're on. */
    val shifted: Map<Char, Int> = buildMap {
        val pairs = listOf('!' to '1', '@' to '2', '#' to '3', '$' to '4', '%' to '5', '^' to '6', '&' to '7', '*' to '8', '(' to '9', ')' to '0',
            '_' to '-', '+' to '=', '{' to '[', '}' to ']', '|' to '\\', ':' to ';', '"' to '\'', '<' to ',', '>' to '.', '?' to '/', '~' to '`')
        for ((s, b) in pairs) codes[b]?.let { put(s, it) }
    }

    const val ESCAPE = 53
    const val TAB = 48
    const val LEFT = 123
    const val RIGHT = 124
    const val DOWN = 125
    const val UP = 126
    const val FORWARD_DELETE = 117
    const val HOME = 115
    const val END = 119
    const val PAGE_UP = 116
    const val PAGE_DOWN = 121
    val FUNCTION = intArrayOf(122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103, 111)

    fun fromAndroid(keyCode: Int): Int? {
        return when (keyCode) {
            android.view.KeyEvent.KEYCODE_ESCAPE -> ESCAPE
            android.view.KeyEvent.KEYCODE_TAB -> TAB
            android.view.KeyEvent.KEYCODE_DPAD_LEFT -> LEFT
            android.view.KeyEvent.KEYCODE_DPAD_RIGHT -> RIGHT
            android.view.KeyEvent.KEYCODE_DPAD_DOWN -> DOWN
            android.view.KeyEvent.KEYCODE_DPAD_UP -> UP
            android.view.KeyEvent.KEYCODE_FORWARD_DEL -> FORWARD_DELETE
            android.view.KeyEvent.KEYCODE_MOVE_HOME -> HOME
            android.view.KeyEvent.KEYCODE_MOVE_END -> END
            android.view.KeyEvent.KEYCODE_PAGE_UP -> PAGE_UP
            android.view.KeyEvent.KEYCODE_PAGE_DOWN -> PAGE_DOWN
            android.view.KeyEvent.KEYCODE_SPACE -> 49
            in android.view.KeyEvent.KEYCODE_A..android.view.KeyEvent.KEYCODE_Z -> codes['a' + (keyCode - android.view.KeyEvent.KEYCODE_A)]
            in android.view.KeyEvent.KEYCODE_0..android.view.KeyEvent.KEYCODE_9 -> codes['0' + (keyCode - android.view.KeyEvent.KEYCODE_0)]
            in android.view.KeyEvent.KEYCODE_F1..android.view.KeyEvent.KEYCODE_F12 -> FUNCTION[keyCode - android.view.KeyEvent.KEYCODE_F1]
            else -> null
        }
    }
}

/** esc, tab, ⌃ ⌥ ⌘ ⇧, arrows, more keys, hide and End, above the keyboard. */
@Composable
private fun ModifierBar(keys: ScreenKeys, onHide: () -> Unit, onEnd: () -> Unit, modifier: Modifier) {
    var arrows by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    Surface(modifier.fillMaxWidth(), tonalElevation = 3.dp) {
        Row(Modifier.padding(horizontal = 8.dp, vertical = 6.dp), horizontalArrangement = Arrangement.spacedBy(5.dp), verticalAlignment = Alignment.CenterVertically) {
            val each = Modifier.weight(1f)
            BarKey("esc", each) { keys.key(MacKeys.ESCAPE) }
            BarKey("tab", each) { keys.key(MacKeys.TAB) }
            for ((m, symbol) in listOf("ctrl" to "⌃", "alt" to "⌥", "meta" to "⌘", "shift" to "⇧")) {
                BarKey(symbol, each, on = m in keys.mods, held = m in keys.held) { keys.tapMod(m) }
            }
            Box(each) {
                BarKey("", Modifier.fillMaxWidth(), icon = { Icon(Icons.Filled.OpenWith, "Arrow keys") }) { arrows = true }
                DropdownMenu(arrows, { arrows = false }) {
                    for ((t, vk) in listOf("Up" to MacKeys.UP, "Down" to MacKeys.DOWN, "Left" to MacKeys.LEFT, "Right" to MacKeys.RIGHT)) {
                        DropdownMenuItem({ Text(t) }, { arrows = false; keys.key(vk) })
                    }
                }
            }
            Box(each) {
                BarKey("…", Modifier.fillMaxWidth()) { more = true }
                DropdownMenu(more, { more = false }) {
                    for ((t, vk) in listOf("Delete Forward" to MacKeys.FORWARD_DELETE, "Home" to MacKeys.HOME, "End" to MacKeys.END,
                        "Page Up" to MacKeys.PAGE_UP, "Page Down" to MacKeys.PAGE_DOWN)) {
                        DropdownMenuItem({ Text(t) }, { more = false; keys.key(vk) })
                    }
                    MacKeys.FUNCTION.forEachIndexed { i, vk -> DropdownMenuItem({ Text("F${i + 1}") }, { more = false; keys.key(vk) }) }
                }
            }
            BarKey("", each, icon = { Icon(Icons.Filled.KeyboardHide, "Hide keyboard") }, onClick = onHide)
            // End without putting the keyboard away first (the bar's End is under it).
            Box(each.heightIn(min = 40.dp).background(Color(0xFFFF3B30), RoundedCornerShape(8.dp)).clickable(onClick = onEnd), contentAlignment = Alignment.Center) {
                Icon(Icons.Filled.Close, "End screen sharing", tint = Color.White)
            }
        }
    }
}

@Composable
private fun BarKey(title: String, modifier: Modifier, on: Boolean = false, held: Boolean = false, icon: (@Composable () -> Unit)? = null, onClick: () -> Unit) {
    val bg = when {
        held -> MaterialTheme.colorScheme.primary
        on -> MaterialTheme.colorScheme.primaryContainer
        else -> MaterialTheme.colorScheme.surfaceVariant
    }
    Box(
        modifier.heightIn(min = 40.dp).background(bg, RoundedCornerShape(8.dp)).clickable(onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        if (icon != null) icon() else Text(title, style = MaterialTheme.typography.labelLarge, color = if (held) MaterialTheme.colorScheme.onPrimary else MaterialTheme.colorScheme.onSurface)
    }
}
