package com.homegrownsw.hammock.media

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import android.graphics.BitmapFactory
import android.os.SystemClock
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.calculateCentroid
import androidx.compose.foundation.gestures.calculateZoom
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.filled.RotateLeft
import androidx.compose.material.icons.automirrored.filled.RotateRight
import androidx.compose.material.icons.automirrored.filled.VolumeUp
import androidx.compose.material.icons.filled.CameraAlt
import androidx.compose.material.icons.filled.CheckBoxOutlineBlank
import androidx.compose.material.icons.filled.Circle
import androidx.compose.material.icons.filled.Close
import androidx.compose.material.icons.filled.Fullscreen
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.Keyboard
import androidx.compose.material.icons.filled.Lock
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Tv
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.geometry.Size
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import androidx.compose.ui.window.DialogWindowProvider
import androidx.core.content.FileProvider
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import kotlin.math.abs
import kotlin.math.hypot
import kotlin.math.max

/** Each pane's last screenshot and screen size this run: shown at once when the pane comes back, until video or a new one comes. */
private val lastShots = HashMap<String, Pair<ImageBitmap, Size>>()

/**
 * A simulator pane: an iOS simulator, Android emulator or TV as live video,
 * with screenshots a few times a second until the video plays (or while it's
 * down). A short touch taps, a longer one swipes at the speed you moved, two
 * fingers pinch; a TV's picture is its remote's touch surface instead. Its
 * buttons are in the bar under it, with full screen and a screenshot to share.
 * Read-only when you can't drive it.
 */
@Composable
fun SimulatorPane(model: AppModel, pane: Hammock.Pane, canDrive: Boolean, modifier: Modifier = Modifier) {
    val paneID = pane.id
    val live = rememberLiveVideo(model, paneID) { conn -> VideoStream.pane(ConnectionLink(conn), paneID) }
    val stream = live.stream
    val last = remember(paneID) { lastShots[paneID] }
    var image by remember(paneID) { mutableStateOf(last?.first) }
    var shotPoints by remember(paneID) { mutableStateOf(last?.second) }
    var videoPixels by remember(paneID) { mutableStateOf<VideoStream.Size?>(null) }
    var failed by remember(paneID) { mutableStateOf(false) }
    var fullScreen by remember { mutableStateOf(false) }
    var typing by remember { mutableStateOf(false) }
    LaunchedEffect(stream) { stream?.size?.collect { if (it != null) videoPixels = it } }

    // The screen's size in points (taps are measured in them): from the
    // video's pixels and the device's scale, else the last screenshot's.
    val points = remember(videoPixels, shotPoints, pane.deviceScale, pane.devicePlatform) {
        videoPixels?.let { px ->
            var scale = if (pane.isTV) max(px.width, px.height) / 1920f else if (px.width >= 1000) 3f else 2f
            if (pane.deviceScale > 0) scale = pane.deviceScale.toFloat()
            Size(px.width / scale, px.height / scale)
        } ?: shotPoints ?: Size(402f, 874f)
    }

    // Screenshots, one after another, while the video isn't playing.
    LaunchedEffect(paneID) {
        while (true) {
            if (live.playing) {
                delay(500)
                continue
            }
            val r = model.simulatorCall(paneID, "screenshot", mapOf("format" to "jpeg"))
            val bmp = r?.data?.takeIf { it.isNotEmpty() }?.let { withContext(Dispatchers.Default) { BitmapFactory.decodeByteArray(it, 0, it.size) } }
            if (r != null && bmp != null) {
                image = bmp.asImageBitmap()
                failed = false
                val w = r.json.optDouble("width", 0.0)
                val h = r.json.optDouble("height", 0.0)
                if (w > 0 && h > 0) shotPoints = Size(w.toFloat(), h.toFloat())
                lastShots[paneID] = image!! to (shotPoints ?: points)
            } else if (image == null) {
                failed = true
            }
            delay(250)
        }
    }

    val surface: @Composable (Modifier, () -> Unit) -> Unit = { m, onThreeFingers ->
        DeviceSurface(model, pane, canDrive, live, image, failed, points, m, onThreeFingers)
    }

    Column(modifier.fillMaxSize()) {
        // Left empty while full screen has the video: a stream shows in one view at a time.
        if (fullScreen) Spacer(Modifier.weight(1f)) else surface(Modifier.weight(1f).fillMaxWidth().padding(horizontal = 16.dp)) {}
        Row(Modifier.fillMaxWidth().padding(horizontal = 8.dp), horizontalArrangement = Arrangement.SpaceEvenly, verticalAlignment = Alignment.CenterVertically) {
            DeviceButtons(model, pane, canDrive) { typing = true }
            IconButton(onClick = { fullScreen = true }) { Icon(Icons.Filled.Fullscreen, "Full Screen") }
            ShareScreenshotButton(model, paneID)
        }
    }

    if (fullScreen) FullScreen(model, pane, canDrive, onExit = { fullScreen = false }, onType = { typing = true }, surface = surface)
    if (typing) TypeDialog(model, pane, canDrive) { typing = false }
}

/** The device: its video, else its screenshots, fitted, taking touches. */
@Composable
private fun DeviceSurface(
    model: AppModel,
    pane: Hammock.Pane,
    canDrive: Boolean,
    live: LiveVideo,
    image: ImageBitmap?,
    failed: Boolean,
    points: Size,
    modifier: Modifier,
    onThreeFingers: () -> Unit,
) {
    val density = LocalDensity.current
    var box by remember { mutableStateOf(Size.Zero) }
    val fitted = fit(points, box)
    val scope = rememberCoroutineScope()
    val now by rememberUpdatedState(Triple(fitted, points, pane))
    // The screen's corners: about 55 points on an iPhone, square-ish on a TV or an emulator.
    val corner = with(density) {
        val k = fitted.width / max(points.width, 1f)
        when {
            pane.isTV -> 6f * k
            pane.isAndroid -> minOf(points.width * 0.03f, 20f) * k
            else -> minOf(55f, minOf(points.width, points.height) * 0.137f) * k
        }.toDp()
    }
    Box(modifier.onSizeChanged { box = Size(it.width.toFloat(), it.height.toFloat()) }, contentAlignment = Alignment.Center) {
        val shape = RoundedCornerShape(corner)
        val size = with(density) { Modifier.size(fitted.width.toDp(), fitted.height.toDp()) }
        val gestures = Modifier.pointerInput(canDrive, pane.isTV) {
            awaitEachGesture {
                val down = awaitFirstDown(requireUnconsumed = false)
                val startAt = SystemClock.uptimeMillis()
                var end = down.position
                var pinchStart: Offset? = null
                var scale = 1f
                var fingers = 1
                while (true) {
                    val ev = awaitPointerEvent()
                    val pressed = ev.changes.filter { it.pressed }
                    if (pressed.isEmpty()) break
                    fingers = max(fingers, pressed.size)
                    if (pressed.size >= 3) { onThreeFingers(); pinchStart = null }
                    if (pressed.size == 2 && fingers == 2) {
                        if (pinchStart == null) pinchStart = ev.calculateCentroid(useCurrent = true)
                        scale *= ev.calculateZoom()
                    } else if (pressed.size == 1 && fingers == 1) {
                        end = pressed[0].position
                    }
                    ev.changes.forEach { it.consume() }
                }
                if (!canDrive || fingers >= 3) return@awaitEachGesture
                val (fit, pts, p) = now
                val k = pts.width / max(fit.width, 1f)
                val ms = (SystemClock.uptimeMillis() - startAt).toInt()
                if (fingers == 2) {
                    // A pinch on the device, played once you let go (its runner synthesises whole gestures).
                    val c = pinchStart ?: return@awaitEachGesture
                    if (p.isTV || abs(scale - 1f) <= 0.05f) return@awaitEachGesture
                    scope.launch {
                        model.simulatorCall(p.id, "pinch", mapOf("x" to c.x * k, "y" to c.y * k, "scale" to scale, "ms" to ms.coerceIn(250, 800)))
                    }
                    return@awaitEachGesture
                }
                val a = down.position * k
                val b = end * k
                if (p.isTV) {
                    // The remote's touch surface: a tap selects (held, a long select), a swipe moves focus.
                    val dx = end.x - down.position.x
                    val dy = end.y - down.position.y
                    val length = hypot(dx, dy) / density.density
                    if (length < 12) {
                        model.simulatorInput(p.id, "remote", mapOf("name" to "select", "holdms" to if (ms >= 500) ms.coerceAtMost(3000) else 60))
                    } else {
                        val dir = if (abs(dx) > abs(dy)) (if (dx > 0) "right" else "left") else (if (dy > 0) "down" else "up")
                        val share = hypot(dx, dy) / max(minOf(fit.width, fit.height), 1f)
                        model.simulatorInput(p.id, "remote", mapOf("swipe" to dir, "distance" to if (share < 0.25f) "short" else if (share < 0.6f) "medium" else "long"))
                    }
                    return@awaitEachGesture
                }
                scope.launch {
                    if (hypot(b.x - a.x, b.y - a.y) < 8) {
                        model.simulatorCall(p.id, "tap", mapOf("x" to a.x, "y" to a.y))
                    } else {
                        model.simulatorCall(p.id, "swipe", mapOf("x" to a.x, "y" to a.y, "x2" to b.x, "y2" to b.y, "ms" to ms.coerceIn(120, 1500)))
                    }
                }
            }
        }
        val stream = live.stream
        // The surface is there from the start, hidden until the video plays:
        // frames are decoded onto it, so it can't wait for them.
        if (stream != null) VideoSurface(stream, size.clip(shape).graphicsLayer { alpha = if (live.playing) 1f else 0f }.then(gestures))
        when {
            stream != null && live.playing -> {}
            image != null -> Image(image, null, size.clip(shape).then(gestures), contentScale = ContentScale.FillBounds)
            failed -> Unavailable("Can't See This Simulator", "Its computer isn't answering, or it's still booting.")
            else -> CircularProgressIndicator()
        }
        // An emulator that isn't showing (starting, stopped, or in Android
        // Studio on the computer): its old picture, dimmed, under why.
        if (pane.isAndroid && (!pane.deviceBooted || pane.deviceElsewhere) && (stream != null || image != null)) {
            Box(size.clip(shape).background(Color.Black.copy(alpha = 0.6f)))
            Surface(shape = RoundedCornerShape(14.dp), tonalElevation = 3.dp) {
                Column(Modifier.padding(horizontal = 20.dp, vertical = 14.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    if (!pane.deviceElsewhere) CircularProgressIndicator(Modifier.size(24.dp))
                    Text(pane.deviceNote.ifEmpty { "Starting…" }, style = MaterialTheme.typography.titleSmall)
                }
            }
        }
    }
}

/** Home, Lock, rotation and typing; an Android phone's Back, Home and Recents; a TV's remote. */
@Composable
private fun DeviceButtons(model: AppModel, pane: Hammock.Pane, canDrive: Boolean, onType: () -> Unit) {
    val id = pane.id
    var more by remember { mutableStateOf(false) }
    val scope = rememberCoroutineScope()
    fun runner(op: String, value: String) = scope.launch { model.simulatorCall(id, op, mapOf("value" to value)) }
    val open = pane.deviceHinge > 120
    when {
        pane.isTV -> {
            IconButton(onClick = { model.pressRemote(id, "menu") }, enabled = canDrive) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
            IconButton(onClick = { model.pressRemote(id, "home") }, enabled = canDrive) { Icon(Icons.Filled.Tv, "TV") }
            IconButton(onClick = { model.pressRemote(id, "play_pause") }, enabled = canDrive) { Icon(Icons.Filled.PlayArrow, "Play/Pause") }
            Box {
                IconButton(onClick = { more = true }, enabled = canDrive) { Icon(Icons.AutoMirrored.Filled.VolumeUp, "Volume") }
                DropdownMenu(more, { more = false }) {
                    DropdownMenuItem({ Text("Volume Up") }, { more = false; model.pressRemote(id, "volume_up") })
                    DropdownMenuItem({ Text("Volume Down") }, { more = false; model.pressRemote(id, "volume_down") })
                    DropdownMenuItem({ Text("Mute") }, { more = false; model.pressRemote(id, "mute") })
                    if (pane.isAppleTV) DropdownMenuItem({ Text("Siri") }, { more = false; model.pressRemote(id, "siri") })
                }
            }
        }
        pane.isAndroidPhone -> {
            IconButton(onClick = { model.pressSimulatorButton(pane, "back") }, enabled = canDrive) { Icon(Icons.AutoMirrored.Filled.ArrowBack, "Back") }
            IconButton(onClick = { model.pressSimulatorButton(pane, "home") }, enabled = canDrive) { Icon(Icons.Filled.Circle, "Home") }
            IconButton(onClick = { model.pressSimulatorButton(pane, "recents") }, enabled = canDrive) { Icon(Icons.Filled.CheckBoxOutlineBlank, "Recents") }
            Box {
                IconButton(onClick = { more = true }, enabled = canDrive) { Icon(Icons.Filled.MoreVert, "More") }
                DropdownMenu(more, { more = false }) {
                    DropdownMenuItem({ Text("Rotate Left") }, { more = false; runner("rotate", "left") })
                    DropdownMenuItem({ Text("Rotate Right") }, { more = false; runner("rotate", "right") })
                    DropdownMenuItem({ Text("Power") }, { more = false; model.pressSimulatorButton(pane, "power") })
                    DropdownMenuItem({ Text("Volume Up") }, { more = false; model.pressSimulatorButton(pane, "volume_up") })
                    DropdownMenuItem({ Text("Volume Down") }, { more = false; model.pressSimulatorButton(pane, "volume_down") })
                    if (pane.deviceFoldable) {
                        DropdownMenuItem({ Text(if (open) "Close Phone" else "Open Phone") }, { more = false; runner("fold", if (open) "closed" else "open") })
                    }
                }
            }
        }
        else -> {
            IconButton(onClick = { model.pressSimulatorButton(pane, "home") }, enabled = canDrive) { Icon(Icons.Filled.Home, "Home") }
            IconButton(onClick = { model.pressSimulatorButton(pane, "lock") }, enabled = canDrive) { Icon(Icons.Filled.Lock, "Lock") }
            IconButton(onClick = { runner("rotate", "left") }, enabled = canDrive) { Icon(Icons.AutoMirrored.Filled.RotateLeft, "Rotate Left") }
            IconButton(onClick = { runner("rotate", "right") }, enabled = canDrive) { Icon(Icons.AutoMirrored.Filled.RotateRight, "Rotate Right") }
            if (pane.deviceFoldable) {
                Box {
                    IconButton(onClick = { more = true }, enabled = canDrive) { Icon(Icons.Filled.MoreVert, "Fold") }
                    DropdownMenu(more, { more = false }) {
                        DropdownMenuItem({ Text("Open Phone") }, { more = false; runner("fold", "open") })
                        DropdownMenuItem({ Text("Half Open Phone") }, { more = false; runner("fold", "half") })
                        DropdownMenuItem({ Text("Close Phone") }, { more = false; runner("fold", "closed") })
                    }
                }
            }
        }
    }
    IconButton(onClick = onType, enabled = canDrive) { Icon(Icons.Filled.Keyboard, "Type") }
}

/** Typing onto the device, and its clipboard both ways (not an Apple TV's: it has none). */
@Composable
private fun TypeDialog(model: AppModel, pane: Hammock.Pane, canDrive: Boolean, onDone: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var text by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDone,
        title = { Text("Type on ${pane.title.ifEmpty { "the Simulator" }}") },
        text = {
            Column {
                OutlinedTextField(text, { text = it }, label = { Text("Text") }, singleLine = false, modifier = Modifier.fillMaxWidth())
                if (canDrive && !pane.isAppleTV) {
                    Spacer(Modifier.height(8.dp))
                    TextButton(onClick = { onDone(); sendClipboard(context, model, pane, scope) }) { Text("Send Clipboard to Device") }
                    TextButton(onClick = { onDone(); copyClipboard(context, model, pane, scope) }) { Text("Copy Device Clipboard") }
                }
                if (canDrive && !pane.isTV && !pane.isAndroid) {
                    TextButton(onClick = { onDone(); model.pressSimulatorButton(pane, "keyboard") }) { Text("Toggle Software Keyboard") }
                }
            }
        },
        confirmButton = {
            TextButton(onClick = {
                val t = text
                onDone()
                if (t.isNotEmpty()) scope.launch { model.simulatorCall(pane.id, "text", mapOf("text" to t)) }
            }, enabled = canDrive) { Text("Send") }
        },
        dismissButton = { TextButton(onClick = onDone) { Text("Cancel") } },
    )
}

private fun sendClipboard(context: Context, model: AppModel, pane: Hammock.Pane, scope: kotlinx.coroutines.CoroutineScope) {
    val text = context.getSystemService(ClipboardManager::class.java)?.primaryClip?.takeIf { it.itemCount > 0 }?.getItemAt(0)?.coerceToText(context)?.toString()
    if (text.isNullOrEmpty()) {
        model.showToast("Your clipboard has no text to send")
        return
    }
    scope.launch {
        model.lastError = null
        if (model.simulatorCall(pane.id, "set_clipboard", mapOf("text" to text)) != null) model.showToast("Sent to ${pane.title}'s clipboard")
        else model.showToast("Couldn't send the clipboard" + (model.lastError?.let { ": $it" } ?: ""))
    }
}

private fun copyClipboard(context: Context, model: AppModel, pane: Hammock.Pane, scope: kotlinx.coroutines.CoroutineScope) {
    scope.launch {
        model.lastError = null
        val r = model.simulatorCall(pane.id, "get_clipboard")
        if (r == null) {
            model.showToast("Couldn't read the clipboard" + (model.lastError?.let { ": $it" } ?: ""))
            return@launch
        }
        val text = r.json.optString("text", "")
        if (text.isEmpty()) {
            model.showToast("The clipboard on ${pane.title} is empty")
            return@launch
        }
        context.getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText(pane.title, text))
        model.showToast("Copied ${pane.title}'s clipboard")
    }
}

/** A screenshot in the device's pixels, to the share sheet (save, copy, send). */
@Composable
private fun ShareScreenshotButton(model: AppModel, paneID: String) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var shooting by remember { mutableStateOf(false) }
    IconButton(enabled = !shooting, onClick = {
        shooting = true
        scope.launch {
            try {
                val png = model.simulatorCall(paneID, "screenshot", mapOf("pixels" to true))?.data?.takeIf { it.isNotEmpty() } ?: return@launch
                val uri = withContext(Dispatchers.IO) {
                    val dir = File(context.cacheDir, "shared").apply { mkdirs() }
                    val f = File(dir, "Screenshot.png")
                    f.writeBytes(png)
                    FileProvider.getUriForFile(context, context.packageName + ".media.files", f)
                }
                val send = Intent(Intent.ACTION_SEND).setType("image/png").putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
                context.startActivity(Intent.createChooser(send, null).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (e: Exception) {
                model.showToast("Couldn't share the screenshot")
            } finally {
                shooting = false
            }
        }
    }) { Icon(Icons.Filled.CameraAlt, "Share Screenshot") }
}

/**
 * The device filling the phone, the system bars hidden. Its controls show
 * for a few seconds, and again with a three-finger touch.
 */
@Composable
private fun FullScreen(
    model: AppModel,
    pane: Hammock.Pane,
    canDrive: Boolean,
    onExit: () -> Unit,
    onType: () -> Unit,
    surface: @Composable (Modifier, () -> Unit) -> Unit,
) {
    var controls by remember { mutableStateOf(true) }
    var hint by remember { mutableStateOf(true) }
    var used by remember { mutableIntStateOf(0) }
    LaunchedEffect(used, controls) {
        if (!controls) return@LaunchedEffect
        delay(if (hint) 5000 else 4000)
        controls = false
        hint = false
    }
    Dialog(onDismissRequest = onExit, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        val window = (LocalView.current.parent as? DialogWindowProvider)?.window
        val view = LocalView.current
        LaunchedEffect(window) {
            window ?: return@LaunchedEffect
            WindowCompat.getInsetsController(window, view).apply {
                hide(WindowInsetsCompat.Type.systemBars())
                systemBarsBehavior = WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
            }
        }
        Box(Modifier.fillMaxSize().background(Color.Black)) {
            surface(Modifier.fillMaxSize()) {
                controls = !controls
                hint = false
                used++
            }
            if (controls) {
                Column(Modifier.align(Alignment.BottomCenter).padding(bottom = 24.dp, start = 12.dp, end = 12.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                    if (hint) {
                        Text(
                            "Touch with three fingers to show these again.",
                            color = Color.White,
                            style = MaterialTheme.typography.bodySmall,
                            modifier = Modifier.background(Color.Black.copy(alpha = 0.7f), RoundedCornerShape(16.dp)).padding(horizontal = 14.dp, vertical = 7.dp),
                        )
                        Spacer(Modifier.height(10.dp))
                    }
                    Surface(shape = RoundedCornerShape(28.dp), color = Color.Black.copy(alpha = 0.72f), contentColor = Color.White) {
                        Row(Modifier.padding(horizontal = 6.dp), verticalAlignment = Alignment.CenterVertically) {
                            IconButton(onClick = onExit) { Icon(Icons.Filled.Close, "Exit Full Screen") }
                            DeviceButtons(model, pane, canDrive) { used++; onType() }
                            ShareScreenshotButton(model, pane.id)
                        }
                    }
                }
            }
        }
    }
}
