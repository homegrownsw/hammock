package com.homegrownsw.hammock.media

import android.graphics.BitmapFactory
import android.text.format.DateUtils
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.aspectRatio
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.OpenInFull
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Brush
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ColorMatrix
import androidx.compose.ui.graphics.ColorFilter
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.layout.onSizeChanged
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import hammock.v1.Hammock
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext

/** Computers whose screens might be shown: this one, and linked Macs that are online and trusted. */
private fun screenComputers(all: List<Hammock.Computer>) = all.filter { c ->
    c.thisComputer || (c.online && !c.refused && !c.suspect && c.os != "linux")
}

/**
 * The Devices screen's Show Screen section: a card for each Mac whose screen
 * can be shared now (asked of each: one with Screen Sharing off, or too old to
 * say, isn't listed), each a live-ish picture of its display. Nothing at all
 * when there are none. A card opens the viewer through onOpen.
 */
@Composable
fun ScreenCards(model: AppModel, onOpen: (computerID: String, name: String) -> Unit) {
    val workspace by model.workspace.collectAsState()
    val link by model.link.collectAsState()
    val connected = link == Link.Connected
    val candidates = if (connected) screenComputers(workspace.computers) else emptyList()
    var shareable by remember { mutableStateOf(setOf<String>()) }
    // The cards look again each time the app comes back (the viewer having been open, say).
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val life by lifecycle.currentStateFlow.collectAsStateWithLifecycle()
    val resumed = life.isAtLeast(Lifecycle.State.RESUMED)
    var visits by remember { mutableIntStateOf(0) }
    LaunchedEffect(resumed) { if (resumed) visits++ }
    LaunchedEffect(candidates.map { it.userId }, visits) {
        val ok = HashSet<String>()
        for (c in candidates) {
            val r = model.listDisplays(c.userId)
            if (r != null && r.enabled && r.displaysCount > 0) ok += c.userId
        }
        shareable = ok
    }
    val screens = candidates.filter { it.userId in shareable }
    if (screens.isEmpty()) return
    Column(Modifier.fillMaxWidth().padding(horizontal = 16.dp), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        Text("Show Screen", style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(top = 8.dp))
        for (c in screens) {
            ScreenCard(model, c, visits) { onOpen(c.userId, c.name) }
        }
        Text(
            "See and control a Mac's whole screen, to approve a prompt or do something only its windows do. Turn it on in Hammock ▸ Settings ▸ Devices on that Mac.",
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ScreenCard(model: AppModel, computer: Hammock.Computer, visits: Int, onClick: () -> Unit) {
    var picture by remember { mutableStateOf<ImageBitmap?>(null) }
    var aspect by remember { mutableFloatStateOf(16f / 9f) }
    var asleep by remember { mutableStateOf(false) }
    var taken by remember { mutableLongStateOf(0L) }
    var width by remember { mutableIntStateOf(0) }
    // Every few seconds while it's shown, at the card's own width in pixels.
    LaunchedEffect(computer.userId, visits) {
        while (true) {
            val r = model.screenSnapshot(computer.userId, maxWidth = maxOf(width, 800))
            if (r != null) {
                asleep = r.asleep
                taken = r.takenUnixMs
                val bytes = r.jpeg.toByteArray()
                val bmp = withContext(Dispatchers.Default) { if (bytes.isEmpty()) null else BitmapFactory.decodeByteArray(bytes, 0, bytes.size) }
                if (bmp != null) {
                    picture = bmp.asImageBitmap()
                    if (r.height > 0) aspect = r.width.toFloat() / r.height
                }
            }
            delay(if (asleep) 10_000 else 5_000)
        }
    }
    val caption = when {
        asleep && taken > 0 -> "Display Asleep · " + DateUtils.getRelativeTimeSpanString(taken, System.currentTimeMillis(), DateUtils.MINUTE_IN_MILLIS)
        asleep -> "Display Asleep"
        computer.thisComputer -> "The computer this phone is connected to"
        else -> (computer.model.ifEmpty { "Mac" }) + " · online"
    }
    Box(
        Modifier
            .fillMaxWidth()
            .aspectRatio(aspect)
            .clip(RoundedCornerShape(20.dp))
            .onSizeChanged { width = it.width }
            .clickable(onClick = onClick)
            .semantics { contentDescription = "Show ${computer.name}'s screen" },
    ) {
        val p = picture
        if (p != null) {
            Image(
                p, null, Modifier.fillMaxSize(), contentScale = ContentScale.Crop,
                // Asleep: faded, as a picture of what it last showed.
                colorFilter = if (asleep) ColorFilter.colorMatrix(ColorMatrix().apply { setToSaturation(0.3f) }) else null,
                alpha = if (asleep) 0.8f else 1f,
            )
        } else {
            Box(Modifier.fillMaxSize().background(MaterialTheme.colorScheme.surfaceVariant), contentAlignment = Alignment.Center) {
                Icon(Icons.Filled.Computer, null, Modifier.size(44.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))
            }
        }
        // The name over a fade, so it reads on any picture.
        Box(Modifier.fillMaxSize().background(Brush.verticalGradient(0.5f to Color.Transparent, 1f to Color.Black.copy(alpha = 0.75f))))
        Row(Modifier.align(Alignment.BottomStart).fillMaxWidth().padding(14.dp), verticalAlignment = Alignment.Bottom) {
            Column(Modifier.weight(1f)) {
                Text(computer.name, style = MaterialTheme.typography.titleMedium, color = Color.White)
                Text(caption, style = MaterialTheme.typography.bodyMedium, color = Color.White.copy(alpha = 0.8f))
            }
            Box(Modifier.size(40.dp).background(Color.White.copy(alpha = 0.2f), CircleShape), contentAlignment = Alignment.Center) {
                Icon(Icons.Filled.OpenInFull, null, tint = Color.White)
            }
        }
    }
}
