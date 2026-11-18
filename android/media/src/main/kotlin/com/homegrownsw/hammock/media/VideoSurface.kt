package com.homegrownsw.hammock.media

import android.content.Context
import android.graphics.SurfaceTexture
import android.view.Surface
import android.view.TextureView
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.Stable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.DaemonConnection
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first

/**
 * A stream's pictures, in a TextureView (not a SurfaceView: it scales,
 * moves and clips with the views around it, which zooming needs). The view
 * stretches the video to its bounds: size it to the video's shape.
 */
@Composable
fun VideoSurface(stream: VideoStream, modifier: Modifier = Modifier) {
    AndroidView(
        factory = { ctx -> VideoTextureView(ctx) },
        modifier = modifier,
        update = { it.stream = stream },
        onRelease = { it.stream = null },
    )
}

private class VideoTextureView(context: Context) : TextureView(context), TextureView.SurfaceTextureListener {
    private var surface: Surface? = null
    private var factory: DecoderFactory? = null

    var stream: VideoStream? = null
        set(value) {
            if (field === value) return
            factory?.let { field?.releaseOutput(it) }
            field = value
            factory?.let { value?.setOutput(it) }
        }

    init {
        surfaceTextureListener = this
        isOpaque = false // clear until the first picture
    }

    override fun onSurfaceTextureAvailable(st: SurfaceTexture, width: Int, height: Int) {
        val s = Surface(st)
        surface = s
        val f = MediaCodecDecoder.factory(s)
        factory = f
        stream?.setOutput(f)
    }

    override fun onSurfaceTextureDestroyed(st: SurfaceTexture): Boolean {
        // The decoder's let go before the surface it draws on.
        factory?.let { stream?.releaseOutput(it) }
        factory = null
        surface?.release()
        surface = null
        return true
    }

    override fun onSurfaceTextureSizeChanged(st: SurfaceTexture, width: Int, height: Int) {}
    override fun onSurfaceTextureUpdated(st: SurfaceTexture) {}
}

/** A stream kept going while it's shown: the current one (null between them), and whether it plays or failed. */
@Stable
class LiveVideo {
    var stream: VideoStream? by mutableStateOf(null)
        internal set
    var playing by mutableStateOf(false)
        internal set
    /** The last one failed before it played: why. */
    var failure: String? by mutableStateOf(null)
        internal set
}

/**
 * Keeps a stream going while `active` and the app's in the foreground: one
 * that fails is replaced two seconds later (the connection dropping ends it,
 * and the next one opens on the new connection). Keyed: a new key starts again.
 */
@Composable
fun rememberLiveVideo(model: AppModel, vararg keys: Any?, active: Boolean = true, open: (DaemonConnection) -> VideoStream): LiveVideo {
    val live = remember(*keys) { LiveVideo() }
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    val lifeState by lifecycle.currentStateFlow.collectAsStateWithLifecycle()
    val foreground = lifeState.isAtLeast(Lifecycle.State.STARTED)
    LaunchedEffect(live, active && foreground) {
        if (!active || !foreground) return@LaunchedEffect
        try {
            while (true) {
                val conn = model.connection
                if (conn == null) {
                    delay(1000)
                    continue
                }
                val s = open(conn)
                live.stream = s
                s.start()
                val end = try {
                    s.state.first { it is VideoStream.State.Playing || it is VideoStream.State.Failed }.let { first ->
                        if (first is VideoStream.State.Playing) {
                            live.playing = true
                            live.failure = null
                            s.state.first { it is VideoStream.State.Failed }
                        } else first
                    }
                } finally {
                    s.stop()
                }
                if (!live.playing) live.failure = (end as? VideoStream.State.Failed)?.reason
                live.playing = false
                live.stream = null
                delay(2000)
            }
        } finally {
            live.stream?.stop()
            live.stream = null
            live.playing = false
        }
    }
    DisposableEffect(live) { onDispose { live.stream?.stop() } }
    return live
}
