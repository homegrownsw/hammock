package com.homegrownsw.hammock.ui

import android.graphics.BitmapFactory
import android.util.LruCache
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ViewAgenda
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.unit.dp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import hammock.v1.Hammock

/** The computer shown, for views deep in a list that need to ask it something (a Group's picture). */
val LocalModel = staticCompositionLocalOf<AppModel?> { null }

/**
 * Groups' pictures (a .hammock/icon.png in the Group's folder), fetched once
 * each from the computer by their hash and kept while the app runs.
 */
object GroupPictures {
    private val cache = LruCache<String, ImageBitmap>(64)

    suspend fun load(model: AppModel, g: Hammock.Group): ImageBitmap? {
        cache.get(g.iconImage)?.let { return it }
        val r = model.request { setReadGroupIcon(Hammock.ReadGroupIcon.newBuilder().setGroupId(g.id).build()) } ?: return null
        val png = r.groupIcon.png.toByteArray()
        if (png.isEmpty()) return null
        val bmp = BitmapFactory.decodeByteArray(png, 0, png.size)?.asImageBitmap() ?: return null
        cache.put(g.iconImage, bmp)
        if (r.groupIcon.hash.isNotEmpty()) cache.put(r.groupIcon.hash, bmp)
        return bmp
    }
}

/** A Group's icon: its emoji, else its picture, else the Group glyph, so rows line up. */
@Composable
fun GroupIcon(g: Hammock.Group, sizeDp: Int) {
    if (g.icon.isNotEmpty()) return Text(g.icon, style = MaterialTheme.typography.bodySmall)
    val model = LocalModel.current
    if (g.iconImage.isNotEmpty() && model != null) {
        // Asked again once connected: a list drawn before the link is up asks too soon.
        val link by model.link.collectAsStateWithLifecycle()
        val picture by produceState<ImageBitmap?>(null, g.iconImage, link == Link.Connected) {
            if (link == Link.Connected) value = GroupPictures.load(model, g)
        }
        picture?.let {
            return Image(it, null, Modifier.size(sizeDp.dp).clip(RoundedCornerShape((sizeDp / 4).dp)))
        }
    }
    Icon(Icons.Default.ViewAgenda, null, Modifier.size(sizeDp.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
}
