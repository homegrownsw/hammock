package com.homegrownsw.hammock.media

import android.util.Log
import com.homegrownsw.hammock.client.DaemonConnection
import hammock.v1.Hammock
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.concurrent.CountDownLatch
import java.util.concurrent.Executor
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

/** What a stream needs of the connection: a DaemonConnection's, or a test's. */
interface MediaLink {
    /** Sends OpenMedia and returns the MediaTicket (throws if the daemon refused). */
    suspend fun open(request: Hammock.OpenMedia): Hammock.MediaTicket
    fun send(build: ClientMessage.Builder.() -> Unit)
    /** Routes a session's MediaFrame, MediaEnded, ScreenCursor and ScreenState to `handler` (null stops). */
    fun setHandler(sessionID: String, handler: ((ServerMessage) -> Unit)?)
}

class ConnectionLink(private val conn: DaemonConnection) : MediaLink {
    override suspend fun open(request: Hammock.OpenMedia): Hammock.MediaTicket = conn.call { setOpenMedia(request) }.mediaTicket
    override fun send(build: ClientMessage.Builder.() -> Unit) = conn.send(build)
    override fun setHandler(sessionID: String, handler: ((ServerMessage) -> Unit)?) {
        conn.setMediaHandler(sessionID, handler)
    }
}

/** The video's parameters: a decoder is made for one set, and made again when they change. */
data class VideoConfig(val sets: H264.ParameterSets, val width: Int, val height: Int)

/** Decodes frames to wherever it was made to show them (MediaCodecDecoder: a Surface). */
interface VideoDecoder {
    /** Queues an Annex-B frame. False if it couldn't (the decoder is behind or broken): it has lost its place. */
    fun decode(annexB: ByteArray, key: Boolean, ptsUs: Long): Boolean
    fun release()
}

/** Makes decoders for one output (a surface). onError: the decoder failed later, from its own thread. */
fun interface DecoderFactory {
    fun create(config: VideoConfig, onError: (String) -> Unit): VideoDecoder?
}

/** A screen's pointer: where (fractions of the display), whether it's on this display, and its shape. */
data class ScreenPointer(
    val x: Float,
    val y: Float,
    val visible: Boolean,
    val shapeID: Int,
    /** The shape's PNG, once it's come (it's sent once per shape). */
    val png: ByteArray?,
    val hotX: Float,
    val hotY: Float,
    /** In the Mac's points. */
    val width: Float,
    val height: Float,
)

/**
 * Live video of a browser or simulator pane, or of a whole display: H.264 the
 * host encodes for this viewer alone (ADR-0012), in band on the connection
 * (OpenMedia in_band: MediaFrame messages, each acked with MediaAck so the
 * host's pacing sees the real link).
 *
 * Frames are acked as they arrive, put in order (FrameOrder), and decoded on
 * the stream's own thread into whatever output is set (setOutput: a surface's
 * decoder factory). With no output (the view's off screen) frames are dropped,
 * and a keyframe asked for when one's set again.
 *
 * A stream runs once: when it fails (state Failed) the viewer stops it and
 * makes another.
 */
class VideoStream(
    private val link: MediaLink,
    internal val request: Hammock.OpenMedia,
    executor: Executor? = null,
    private val clock: () -> Long = { System.nanoTime() / 1_000_000 },
    private val log: (String) -> Unit = { Log.i("HammockVideo", it) },
) {
    sealed interface State {
        data object Connecting : State
        data object Playing : State
        data class Failed(val reason: String) : State
    }

    data class Size(val width: Int, val height: Int)

    private val ownExecutor: ExecutorService? = if (executor == null) {
        Executors.newSingleThreadExecutor { r -> Thread(r, "hammock-video").apply { isDaemon = true } }
    } else null
    private val executor: Executor = executor ?: ownExecutor!!
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    private val _state = MutableStateFlow<State>(State.Connecting)
    val state: StateFlow<State> = _state.asStateFlow()
    /** The video's size in pixels, from its frames. */
    private val _size = MutableStateFlow<Size?>(null)
    val size: StateFlow<Size?> = _size.asStateFlow()
    /** The part of the display the latest frame shows (a zoomed screen; the whole otherwise). */
    private val _crop = MutableStateFlow(Crop.WHOLE)
    val crop: StateFlow<Crop> = _crop.asStateFlow()
    /** A screen's pointer (it isn't in the picture). */
    private val _pointer = MutableStateFlow<ScreenPointer?>(null)
    val pointer: StateFlow<ScreenPointer?> = _pointer.asStateFlow()
    /** A screen's Mac is at its lock screen. */
    private val _locked = MutableStateFlow(false)
    val locked: StateFlow<Boolean> = _locked.asStateFlow()

    // All below on the executor.
    @Volatile var sessionID = ""
        private set
    private var started = false
    private var stopped = false
    private val order = FrameOrder(clock)
    private var output: DecoderFactory? = null
    private var decoder: VideoDecoder? = null
    private var config: VideoConfig? = null
    private var lastKeyRequest = Long.MIN_VALUE / 2
    private var view: FloatArray? = null // x, y, w, h, aspect
    private var page: Pair<Float, Float>? = null
    private val shapes = HashMap<Int, ByteArray>()
    /** Counts, for diagnostics and tests. */
    var received = 0
        private set
    var decoded = 0
        private set

    val paneID: String get() = request.paneId

    fun start() {
        executor.execute { started = true }
        scope.launch {
            val t = try {
                link.open(request)
            } catch (e: Exception) {
                fail(e.message ?: "The video couldn't start.")
                return@launch
            }
            executor.execute {
                if (stopped) {
                    link.send { setCloseMedia(Hammock.CloseMedia.newBuilder().setSessionId(t.sessionId)) }
                    return@execute
                }
                sessionID = t.sessionId
                val sid = t.sessionId
                link.setHandler(sid) { msg -> message(sid, msg) }
                sendView()
                sendPage()
            }
        }
    }

    fun stop() {
        executor.execute {
            if (stopped) return@execute
            stopped = true
            decoder?.release()
            decoder = null
            val sid = sessionID
            if (sid.isNotEmpty()) {
                link.setHandler(sid, null)
                link.send { setCloseMedia(Hammock.CloseMedia.newBuilder().setSessionId(sid)) }
            }
        }
        ownExecutor?.shutdown()
        scope.cancel()
    }

    /**
     * Where frames go: a decoder factory for a surface, or null (none: the
     * view's gone). Returns once the old decoder's released, so the caller can
     * release its surface after (or within half a second, if it's busy).
     */
    fun setOutput(factory: DecoderFactory?) = changeOutput { factory }

    /**
     * Stops showing on `factory`'s surface, if that's where it shows: a view
     * going away mustn't take the output from the view that has it now (full
     * screen taking over from the pane).
     */
    fun releaseOutput(factory: DecoderFactory) = changeOutput { if (output === factory) null else output }

    private fun changeOutput(next: () -> DecoderFactory?) {
        val done = CountDownLatch(1)
        try {
            executor.execute {
                val factory = next()
                if (output !== factory) {
                    output = factory
                    decoder?.release()
                    decoder = null
                    config = null
                    if (order.started) {
                        order.lost()
                        if (factory != null) requestKeyframe("new surface", force = true)
                    }
                }
                done.countDown()
            }
        } catch (_: Exception) {
            return // stopped
        }
        done.await(500, TimeUnit.MILLISECONDS)
    }

    /**
     * A screen viewer zoomed in: the part of the display it shows (fractions;
     * w 0 for the whole display), in frames of the viewer's shape (width over
     * height; 0: the display's). Kept, for a session still opening.
     */
    fun setView(x: Float, y: Float, w: Float, h: Float, aspect: Float = 0f) = executor.execute {
        view = floatArrayOf(x, y, w, h, aspect)
        sendView()
    }

    /** A browser pane's viewer: the size (dp) it shows the page at, which the page takes while it's driven from here. */
    fun setPageSize(width: Float, height: Float) = executor.execute {
        if (width <= 0f || height <= 0f || page == width to height) return@execute
        page = width to height
        sendPage()
    }

    private fun sendView() {
        val v = view ?: return
        val sid = sessionID
        if (sid.isEmpty() || stopped) return
        link.send {
            setSetMediaView(Hammock.SetMediaView.newBuilder().setSessionId(sid).setX(v[0]).setY(v[1]).setW(v[2]).setH(v[3]).setAspect(v[4]))
        }
    }

    private fun sendPage() {
        val p = page ?: return
        val sid = sessionID
        if (sid.isEmpty() || stopped) return
        link.send { setSetMediaView(Hammock.SetMediaView.newBuilder().setSessionId(sid).setPageWidth(p.first).setPageHeight(p.second)) }
    }

    /** From the connection's thread. */
    private fun message(sid: String, msg: ServerMessage) {
        when (msg.msgCase) {
            ServerMessage.MsgCase.MEDIA_FRAME -> {
                val f = msg.mediaFrame
                // Acked as it arrives, before ordering: the host paces on how
                // fast frames get here, not how fast they're shown.
                link.send { setMediaAck(Hammock.MediaAck.newBuilder().setSessionId(sid).addSeqs(f.seq)) }
                val crop = if (f.cropW > 0f) Crop(f.cropX, f.cropY, f.cropW, f.cropH) else Crop.WHOLE
                val frame = VideoFrame(
                    seq = f.seq.toLong() and 0xffffffffL, key = f.keyframe, width = f.width, height = f.height,
                    data = f.data.toByteArray(), refresh = f.refresh, hevc = f.hevc, crop = crop,
                )
                executor.execute { received(frame) }
            }
            ServerMessage.MsgCase.MEDIA_ENDED -> fail(msg.mediaEnded.reason.ifEmpty { "The video stopped." })
            ServerMessage.MsgCase.SCREEN_CURSOR -> {
                val c = msg.screenCursor
                val png = if (c.png.isEmpty) null else c.png.toByteArray()
                executor.execute {
                    if (png != null) shapes[c.shapeId] = png
                    _pointer.value = ScreenPointer(c.x, c.y, c.visible, c.shapeId, shapes[c.shapeId], c.hotX, c.hotY, c.width, c.height)
                }
            }
            ServerMessage.MsgCase.SCREEN_STATE -> _locked.value = msg.screenState.locked
            else -> {}
        }
    }

    /** Hands a frame in, as if it came from the connection (tests; on the executor). */
    internal fun receivedForTest(f: VideoFrame) = executor.execute { received(f) }

    private fun received(f: VideoFrame) {
        if (stopped) return
        received++
        val step = order.receive(f)
        for (d in step.decode) decode(d)
        // Off screen there's nothing to show a keyframe on: asked for when there is.
        if (output != null) step.keyframe?.let { requestKeyframe(it, again = step.again) }
    }

    private fun decode(f: VideoFrame) {
        if (_size.value?.let { it.width != f.width || it.height != f.height } != false) _size.value = Size(f.width, f.height)
        if (f.hevc) {
            // Never asked for (OpenMedia's codecs leave hevc444 out); can't be shown.
            lose("an HEVC frame")
            return
        }
        val out = output
        if (out == null) {
            // Off screen: nothing to decode into. A keyframe's asked for when it's back.
            order.lost()
            return
        }
        H264.parameterSets(f.data)?.let { sets ->
            val c = VideoConfig(sets, f.width, f.height)
            if (c != config || decoder == null) {
                decoder?.release()
                decoder = out.create(c) { why -> executor.execute { decoderFailed(why) } }
                config = c
                if (decoder == null) {
                    lose("couldn't make a decoder")
                    return
                }
            }
        }
        val d = decoder
        if (d == null || !H264.hasPicture(f.data)) {
            if (f.key) lose("unreadable keyframe")
            return
        }
        if (!d.decode(H264.annexB(f.data), f.key, f.seq)) {
            d.release()
            decoder = null
            config = null
            lose("decoder behind")
            return
        }
        decoded++
        if (_crop.value != f.crop) _crop.value = f.crop
        if (f.key && _state.value != State.Playing && !stopped) {
            log("$paneID $sessionID: playing")
            _state.value = State.Playing
        }
    }

    private fun decoderFailed(why: String) {
        if (stopped) return
        log("$paneID $sessionID: decoder failed: $why")
        decoder?.release()
        decoder = null
        config = null
        lose(why)
    }

    private fun lose(why: String) {
        order.lost()
        requestKeyframe(why)
    }

    private fun requestKeyframe(why: String, again: Boolean = false, force: Boolean = false) {
        val now = clock()
        val gap = if (again) 1500 else 500
        if (!force && now - lastKeyRequest < gap) return
        val sid = sessionID
        if (sid.isEmpty() || stopped) return
        lastKeyRequest = now
        log("$paneID $sid: keyframe requested ($why)")
        link.send { setMediaAck(Hammock.MediaAck.newBuilder().setSessionId(sid).setKeyframe(true)) }
    }

    private fun fail(reason: String) {
        if (_state.value is State.Failed) return
        log("$paneID $sessionID: $reason")
        _state.value = State.Failed(reason)
    }

    companion object {
        /** A browser or simulator pane's video. maxPixels: at most this many (0: the pane's own size). */
        fun pane(link: MediaLink, paneID: String, maxPixels: Int = 0): VideoStream =
            VideoStream(link, Hammock.OpenMedia.newBuilder().setPaneId(paneID).setInBand(true).setMaxPixels(maxPixels).build())

        /**
         * A display's video (0: the main one) on the computer named ("" for
         * the one connected to). background: a layer under a zoomed view,
         * which takes a small share of the link. No HEVC: H.264 only.
         */
        fun screen(link: MediaLink, display: Int, computerID: String, maxPixels: Int, maxFPS: Int = 0, background: Boolean = false): VideoStream =
            VideoStream(
                link,
                Hammock.OpenMedia.newBuilder().setScreen(true).setDisplayId(display).setComputerId(computerID).setInBand(true)
                    .setMaxPixels(maxPixels).setMaxFps(maxFPS).setBackground(background).build(),
            )
    }
}
