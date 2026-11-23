package com.homegrownsw.hammock.agentview

import android.graphics.Bitmap
import android.graphics.ImageDecoder
import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.gestures.detectTapGestures
import androidx.compose.foundation.gestures.detectTransformGestures
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.BrokenImage
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableFloatStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import com.homegrownsw.hammock.client.AppCover
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import java.nio.ByteBuffer
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.roundToInt

/** Picture sizes, as the iPhone's AttachmentSize: the long edge's limit in pixels. */
enum class AttachmentSize(val maxEdge: Int?, val label: String) {
    ORIGINAL(null, "Original"),
    LARGE(1568, "Large (1568 px)"),
    MEDIUM(1024, "Medium (1024 px)"),
    SMALL(768, "Small (768 px)"),
}

object ImageCodec {
    /**
     * Decodes a picture at most [maxEdge] pixels on its long edge. ImageDecoder
     * turns it by its EXIF orientation, and reads HEIF as well as the rest.
     */
    fun decode(data: ByteArray, maxEdge: Int? = null): Bitmap? = runCatching {
        ImageDecoder.decodeBitmap(ImageDecoder.createSource(ByteBuffer.wrap(data))) { decoder, info, _ ->
            decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
            val w = info.size.width
            val h = info.size.height
            if (maxEdge != null && max(w, h) > maxEdge) {
                val scale = maxEdge.toFloat() / max(w, h)
                decoder.setTargetSize(max(1, (w * scale).roundToInt()), max(1, (h * scale).roundToInt()))
            }
        }
    }.getOrNull()

    /** Formats Claude reads as they are; anything else (a phone's HEIC photo) is made JPEG. */
    fun claudeReadable(data: ByteArray): Boolean {
        if (data.size < 12) return false
        fun at(i: Int, vararg b: Int) = b.withIndex().all { (k, v) -> data[i + k] == v.toByte() }
        return at(0, 0x89, 0x50, 0x4E, 0x47) || at(0, 0xFF, 0xD8, 0xFF) || at(0, 0x47, 0x49, 0x46) || at(8, 0x57, 0x45, 0x42, 0x50)
    }

    /**
     * The picture with its long edge at most [size]'s: JPEG, or PNG while it
     * has transparency. As it is when it's no bigger and Claude can read it.
     */
    fun resized(name: String, data: ByteArray, size: AttachmentSize): OutgoingAttachment {
        val bounds = runCatching {
            var w = 0; var h = 0
            ImageDecoder.decodeDrawable(ImageDecoder.createSource(ByteBuffer.wrap(data))) { d, info, _ ->
                w = info.size.width; h = info.size.height
                d.setTargetSize(1, 1) // only the header was wanted
            }
            w to h
        }.getOrNull()
        val edge = size.maxEdge
        val fits = edge == null || (bounds != null && max(bounds.first, bounds.second) <= edge)
        if (fits && claudeReadable(data)) return OutgoingAttachment(name, data)
        val bmp = decode(data, edge) ?: return OutgoingAttachment(name, data)
        val alpha = bmp.hasAlpha()
        val out = ByteArrayOutputStream()
        bmp.compress(if (alpha) Bitmap.CompressFormat.PNG else Bitmap.CompressFormat.JPEG, 85, out)
        bmp.recycle()
        val base = name.substringBeforeLast('.', name)
        return OutgoingAttachment(base + if (alpha) ".png" else ".jpg", out.toByteArray())
    }
}

/** A picture full screen: pinch to zoom, drag to pan, double-tap to zoom in or out, drag down (unzoomed) to close. */
@Composable
fun ImageViewer(image: ImageBitmap, onDismiss: () -> Unit) {
    // (A window of its own, over the app's lock: not while it covers the app.)
    val locked by AppCover.locked.collectAsState()
    if (locked) return
    Dialog(onDismissRequest = onDismiss, properties = DialogProperties(usePlatformDefaultWidth = false, decorFitsSystemWindows = false)) {
        var scale by remember { mutableFloatStateOf(1f) }
        var offset by remember { mutableStateOf(Offset.Zero) }
        var dismissDrag by remember { mutableFloatStateOf(0f) }
        val shown by animateFloatAsState(scale, label = "zoom")
        val fade = (1f - abs(dismissDrag) / 900f).coerceIn(0.3f, 1f)
        Box(
            Modifier.fillMaxSize().background(Color.Black.copy(alpha = fade))
                .pointerInput(Unit) {
                    detectTapGestures(onDoubleTap = { at ->
                        if (scale > 1f) { scale = 1f; offset = Offset.Zero } else {
                            scale = 2.5f
                            offset = (Offset(size.width / 2f, size.height / 2f) - at) * 1.5f
                        }
                    })
                }
                .pointerInput(Unit) {
                    detectTransformGestures { _, pan, zoom, _ ->
                        val s = (scale * zoom).coerceIn(1f, 6f)
                        if (s <= 1.01f && zoom == 1f) {
                            // Unzoomed: a drag down (or up) closes it.
                            dismissDrag += pan.y
                            if (abs(dismissDrag) > 220f) onDismiss()
                        } else {
                            scale = s
                            offset = if (s == 1f) Offset.Zero else offset + pan
                        }
                    }
                },
        ) {
            Image(
                image, contentDescription = "Picture", contentScale = ContentScale.Fit,
                modifier = Modifier.fillMaxSize().graphicsLayer {
                    scaleX = shown; scaleY = shown
                    translationX = offset.x; translationY = offset.y + dismissDrag
                },
            )
            IconButton(
                onClick = onDismiss,
                colors = IconButtonDefaults.iconButtonColors(containerColor = Color.Black.copy(alpha = 0.5f), contentColor = Color.White),
                modifier = Modifier.align(Alignment.TopEnd).statusBarsPadding().padding(12.dp),
            ) { Icon(Icons.Outlined.Close, contentDescription = "Close") }
        }
    }
}
