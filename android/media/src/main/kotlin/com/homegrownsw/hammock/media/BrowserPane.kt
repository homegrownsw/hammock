package com.homegrownsw.hammock.media

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Intent
import android.net.Uri
import android.os.Handler
import android.os.Looper
import android.view.KeyEvent
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.calculateCentroid
import androidx.compose.foundation.gestures.calculatePan
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.ArrowForward
import androidx.compose.material.icons.filled.Check
import androidx.compose.material.icons.filled.Keyboard
import androidx.compose.material.icons.filled.KeyboardHide
import androidx.compose.material.icons.filled.OpenInBrowser
import androidx.compose.material.icons.filled.Public
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clipToBounds
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.input.pointer.positionChange
import androidx.compose.ui.input.pointer.util.VelocityTracker
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.Job

/**
 * A browser pane: the computer's page as live video, fitted to the space.
 * A tap clicks, a long press right-clicks, one finger scrolls the page (with
 * momentum), and a pinch zooms in here, 1–4x (one finger then pans the zoomed
 * view). Type opens the keyboard into the page. The page takes this view's
 * size while it's driven from here, as a terminal takes its driver's.
 * Read-only when you can't drive it.
 */
@Composable
fun BrowserPane(model: AppModel, pane: Hammock.Pane, canDrive: Boolean, modifier: Modifier = Modifier) {
    val context = LocalContext.current
    val density = LocalDensity.current
    val paneID = pane.id
    // No bigger than this phone's screen.
    val maxPixels = remember { context.resources.displayMetrics.let { it.widthPixels * it.heightPixels } }
    val live = rememberLiveVideo(model, paneID) { conn -> VideoStream.pane(ConnectionLink(conn), paneID, maxPixels) }
    val stream = live.stream

    var pixels by remember { mutableStateOf(Size(1600f, 1000f)) } // the video's, for its shape
    LaunchedEffect(stream) { stream?.size?.collect { s -> if (s != null && s.width > 0 && s.height > 0) pixels = Size(s.width.toFloat(), s.height.toFloat()) } }
    var box by remember { mutableStateOf(Size.Zero) } // the page's space, in px
    var zoom by remember { mutableFloatStateOf(1f) }
    var pan by remember { mutableStateOf(Offset.Zero) }
    var typing by remember { mutableStateOf(false) }
    var menu by remember { mutableStateOf<Hammock.BrowserMenu?>(null) }
    val fitted = fit(pixels, box)
    val geometry by rememberUpdatedState(Triple(box, fitted, canDrive))

    // The page's size here, in dp: it takes it while driven from here.
    LaunchedEffect(stream, box, canDrive) {
        if (canDrive && box.width > 0) with(density) { stream?.setPageSize(box.width.toDp().value, box.height.toDp().value) }
    }
    // A new size (rotation, another page shape) starts again from fitted.
    LaunchedEffect(box, pixels) { zoom = 1f; pan = Offset.Zero }

    // Menus the page opens (a <select>, a right-click) and what Copy copies.
    DisposableEffect(model, paneID) {
        val main = Handler(Looper.getMainLooper())
        val stopMenu = model.listen(ServerMessage.MsgCase.BROWSER_MENU) { m -> if (m.browserMenu.paneId == paneID) main.post { menu = m.browserMenu } }
        val stopClip = model.listen(ServerMessage.MsgCase.BROWSER_CLIPBOARD) { m ->
            if (m.browserClipboard.paneId == paneID) main.post {
                context.getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText("Page", m.browserClipboard.text))
            }
        }
        onDispose { stopMenu(); stopClip() }
    }

    fun input(op: String, args: Map<String, Any?>) {
        if (canDrive) model.browserInput(paneID, op, args)
    }

    /** A point in the view as fractions of the page, wherever it's zoomed. */
    fun fraction(p: Offset): Pair<Float, Float> {
        val (b, f, _) = geometry
        if (f.width <= 0f || f.height <= 0f) return 0f to 0f
        val local = (p - Offset(b.width / 2, b.height / 2) - pan) / zoom + Offset(f.width / 2, f.height / 2)
        return (local.x / f.width).coerceIn(0f, 1f) to (local.y / f.height).coerceIn(0f, 1f)
    }

    fun click(p: Offset, button: String) {
        val (x, y) = fraction(p)
        input("mouse", mapOf("type" to "down", "x" to x, "y" to y, "clicks" to 1, "button" to button))
        input("mouse", mapOf("type" to "up", "x" to x, "y" to y, "clicks" to 1, "button" to button))
    }

    fun key(name: String, vk: Int, text: String = "", mods: List<String> = emptyList()) {
        val down = mutableMapOf<String, Any?>("type" to "down", "key" to name, "code" to name, "vk" to vk, "mods" to mods)
        if (text.isNotEmpty()) down["text"] = text
        input("key", down)
        input("key", mapOf("type" to "up", "key" to name, "code" to name, "vk" to vk, "mods" to mods))
    }

    fun typed(t: Typed) {
        when (t) {
            is Typed.Text -> input("text", mapOf("text" to t.text))
            Typed.Enter -> key("Enter", 13, "\r")
            Typed.Backspace -> key("Backspace", 8)
            is Typed.Key -> pageKeys[t.keyCode]?.let { (name, vk) -> key(name, vk, mods = browserMods(t.meta)) }
        }
    }

    val scope = rememberCoroutineScope()
    var glide by remember { mutableStateOf<Job?>(null) }

    Column(modifier.fillMaxSize()) {
        Box(
            Modifier
                .weight(1f)
                .fillMaxWidth()
                .clipToBounds()
                .onSizeChanged { box = Size(it.width.toFloat(), it.height.toFloat()) }
                .pointerInput(canDrive) {
                    detectTapGestures(
                        onPress = { glide?.cancel() }, // a touch stops a glide, as in any scroll view
                        onTap = { click(it, "left") },
                        onLongPress = { click(it, "right") },
                    )
                }
                .pointerInput(canDrive) {
                    awaitEachGesture {
                        val down = awaitFirstDown(requireUnconsumed = false)
                        glide?.cancel()
                        val tracker = VelocityTracker()
                        tracker.addPosition(down.uptimeMillis, down.position)
                        val anchor = fraction(down.position) // the wheel events' position
                        var moved = Offset.Zero
                        var dragging = false
                        var pinched = false
                        while (true) {
                            val ev = awaitPointerEvent()
                            val pressed = ev.changes.filter { it.pressed }
                            if (pressed.isEmpty()) break
                            if (pressed.size >= 2) {
                                // Zoom about the fingers: the point under them stays put.
                                pinched = true
                                val (b, _, _) = geometry
                                val before = zoom
                                val z = (zoom * ev.calculateZoom()).coerceIn(1f, 4f)
                                val c = ev.calculateCentroid(useCurrent = true)
                                val centre = Offset(b.width / 2, b.height / 2)
                                pan = c - centre - (c - centre - pan) * (z / before) + ev.calculatePan()
                                zoom = z
                                pan = clampPan(pan, zoom, geometry.second, b)
                                ev.changes.forEach { it.consume() }
                                continue
                            }
                            val ch = pressed[0]
                            tracker.addPosition(ch.uptimeMillis, ch.position)
                            val d = ch.positionChange()
                            if (!dragging) {
                                moved += d
                                if (moved.getDistance() < viewConfiguration.touchSlop) continue
                                dragging = true
                            }
                            ch.consume()
                            if (zoom > 1.01f) {
                                // Zoomed in, one finger pans the view; fitted, it scrolls the page.
                                pan = clampPan(pan + d, zoom, geometry.second, geometry.first)
                            } else if (!pinched) {
                                wheel(geometry.second, anchor, -d.x, -d.y, ::input)
                            }
                        }
                        if (dragging && !pinched && zoom <= 1.01f && canDrive) {
                            val v = tracker.calculateVelocity()
                            val f = geometry.second
                            glide = scope.launchGlide(Offset(-v.x, -v.y), minSpeed = 60 * density.density, decay = 0.998) { dx, dy ->
                                wheel(f, anchor, dx, dy, ::input)
                            }
                        }
                    }
                },
            contentAlignment = Alignment.Center,
        ) {
            if (stream != null && live.playing) {
                with(density) {
                    VideoSurface(
                        stream,
                        Modifier
                            .size(fitted.width.toDp(), fitted.height.toDp())
                            .graphicsLayer {
                                scaleX = zoom
                                scaleY = zoom
                                translationX = pan.x
                                translationY = pan.y
                            },
                    )
                }
            } else if (live.failure != null) {
                Unavailable("Can't See This Page", "Its computer isn't answering.")
            } else {
                CircularProgressIndicator()
            }
            KeyCatcher(typing && canDrive, onTyped = ::typed, onDismissed = { typing = false }, modifier = Modifier.size(1.dp))
        }
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), horizontalArrangement = Arrangement.SpaceEvenly) {
            IconButton(onClick = { model.navigate(paneID, "back") }, enabled = canDrive && pane.canGoBack) {
                Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back")
            }
            IconButton(onClick = { model.navigate(paneID, "forward") }, enabled = canDrive && pane.canGoForward) {
                Icon(Icons.AutoMirrored.Filled.ArrowForward, "Forward")
            }
            IconButton(onClick = { model.navigate(paneID, "reload") }, enabled = canDrive) {
                Icon(Icons.Filled.Refresh, "Reload")
            }
            IconButton(onClick = { typing = !typing }, enabled = canDrive) {
                Icon(if (typing) Icons.Filled.KeyboardHide else Icons.Filled.Keyboard, if (typing) "Hide Keyboard" else "Type")
            }
            val url = pane.url
            if (url.startsWith("http://") || url.startsWith("https://")) {
                IconButton(onClick = {
                    try { context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url))) } catch (_: Exception) { model.showToast("No browser to open it in") }
                }) { Icon(Icons.Filled.OpenInBrowser, "Open in Browser") }
            }
        }
    }

    menu?.let { m -> PageMenuSheet(m) { i -> menu = null; model.answerBrowserMenu(m, i) } }
}

/** A menu the page opened (a <select>'s options or a right-click), as a list to pick from; dismissed is -1. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PageMenuSheet(menu: Hammock.BrowserMenu, pick: (Int) -> Unit) {
    ModalBottomSheet(onDismissRequest = { pick(-1) }) {
        Text(
            if (menu.kind == "select") "Choose" else "Page",
            style = MaterialTheme.typography.titleMedium,
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp),
        )
        LazyColumn(Modifier.padding(bottom = 24.dp)) {
            itemsIndexed(menu.itemsList) { i, item ->
                if (item.separator) return@itemsIndexed
                val checked = item.checked || (menu.kind == "select" && menu.selected == i)
                Row(
                    Modifier
                        .fillMaxWidth()
                        .clickable(enabled = item.enabled) { pick(i) }
                        .padding(start = (24 + item.indent * 16).dp, end = 24.dp, top = 14.dp, bottom = 14.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        item.title,
                        modifier = Modifier.weight(1f),
                        color = if (item.enabled) MaterialTheme.colorScheme.onSurface else MaterialTheme.colorScheme.onSurface.copy(alpha = 0.38f),
                    )
                    if (checked) Icon(Icons.Filled.Check, null, tint = MaterialTheme.colorScheme.primary)
                }
            }
        }
    }
}

/** Why there's no picture, in the middle of the pane. */
@Composable
internal fun Unavailable(title: String, detail: String, onDark: Boolean = false) {
    Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.padding(24.dp)) {
        Icon(Icons.Filled.Public, null, Modifier.size(40.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
        Text(title, style = MaterialTheme.typography.titleMedium, textAlign = TextAlign.Center, color = if (onDark) androidx.compose.ui.graphics.Color.White else MaterialTheme.colorScheme.onSurface)
        Text(detail, style = MaterialTheme.typography.bodyMedium, textAlign = TextAlign.Center, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** One finger's scroll as wheel events, in fractions of the page, at where the scroll began. */
private fun wheel(fitted: Size, anchor: Pair<Float, Float>, dx: Float, dy: Float, input: (String, Map<String, Any?>) -> Unit) {
    if (fitted.width <= 0f || fitted.height <= 0f || (dx == 0f && dy == 0f)) return
    input("wheel", mapOf("x" to anchor.first, "y" to anchor.second, "dx" to dx / fitted.width, "dy" to dy / fitted.height))
}

/** Keeps the zoomed page covering the view (or centred while it's smaller), so each edge can be brought into view. */
internal fun clampPan(pan: Offset, zoom: Float, fitted: Size, box: Size): Offset {
    val mx = maxOf(0f, (fitted.width * zoom - box.width) / 2)
    val my = maxOf(0f, (fitted.height * zoom - box.height) / 2)
    return Offset(pan.x.coerceIn(-mx, mx), pan.y.coerceIn(-my, my))
}

/** Content of `content`'s shape, as big as fits in `box`. */
internal fun fit(content: Size, box: Size): Size {
    if (content.width <= 0f || content.height <= 0f || box.width <= 0f || box.height <= 0f) return box
    val s = minOf(box.width / content.width, box.height / content.height)
    return Size(content.width * s, content.height * s)
}

/** A hardware keyboard's keys a page understands, as DOM key names and Windows virtual key codes. */
private val pageKeys = mapOf(
    KeyEvent.KEYCODE_DPAD_LEFT to ("ArrowLeft" to 37), KeyEvent.KEYCODE_DPAD_UP to ("ArrowUp" to 38),
    KeyEvent.KEYCODE_DPAD_RIGHT to ("ArrowRight" to 39), KeyEvent.KEYCODE_DPAD_DOWN to ("ArrowDown" to 40),
    KeyEvent.KEYCODE_TAB to ("Tab" to 9), KeyEvent.KEYCODE_ESCAPE to ("Escape" to 27), KeyEvent.KEYCODE_FORWARD_DEL to ("Delete" to 46),
    KeyEvent.KEYCODE_MOVE_HOME to ("Home" to 36), KeyEvent.KEYCODE_MOVE_END to ("End" to 35),
    KeyEvent.KEYCODE_PAGE_UP to ("PageUp" to 33), KeyEvent.KEYCODE_PAGE_DOWN to ("PageDown" to 34),
)

private fun browserMods(meta: Int): List<String> = buildList {
    if (meta and KeyEvent.META_ALT_ON != 0) add("alt")
    if (meta and KeyEvent.META_CTRL_ON != 0) add("ctrl")
    if (meta and KeyEvent.META_META_ON != 0) add("meta")
    if (meta and KeyEvent.META_SHIFT_ON != 0) add("shift")
}
