package com.homegrownsw.hammock.media

import android.media.MediaCodec
import android.media.MediaCodecInfo
import android.media.MediaCodecList
import android.media.MediaFormat
import android.os.Handler
import android.os.HandlerThread
import android.util.Log
import android.view.Surface
import java.nio.ByteBuffer

/**
 * H.264 decoded in hardware straight onto a Surface (a TextureView's), in
 * MediaCodec's asynchronous mode: frames wait here for an input buffer, and
 * each picture is shown as soon as it's decoded (there's no reordering to wait
 * for: the host's encoder never reorders).
 */
class MediaCodecDecoder private constructor(
    private val codec: MediaCodec,
    private val thread: HandlerThread,
) : VideoDecoder {
    private class Input(val data: ByteArray, val key: Boolean, val ptsUs: Long)

    private val lock = Any()
    private val free = ArrayDeque<Int>()
    private val queued = ArrayDeque<Input>()
    private var released = false
    @Volatile private var onError: (String) -> Unit = {}

    override fun decode(annexB: ByteArray, key: Boolean, ptsUs: Long): Boolean = synchronized(lock) {
        if (released) return false
        // Far behind (the decoder's stalled): better to start again from a keyframe than show old frames late.
        if (queued.size >= MAX_QUEUED) return false
        queued.addLast(Input(annexB, key, ptsUs))
        feed()
    }

    /** Fills free input buffers with waiting frames. Under lock. */
    private fun feed(): Boolean {
        while (free.isNotEmpty() && queued.isNotEmpty()) {
            val index = free.removeFirst()
            val f = queued.removeFirst()
            try {
                val buf: ByteBuffer = codec.getInputBuffer(index) ?: return false
                if (buf.capacity() < f.data.size) {
                    Log.w(TAG, "frame of ${f.data.size} bytes won't fit an input buffer of ${buf.capacity()}")
                    return false
                }
                buf.clear()
                buf.put(f.data)
                codec.queueInputBuffer(index, 0, f.data.size, f.ptsUs, if (f.key) MediaCodec.BUFFER_FLAG_KEY_FRAME else 0)
            } catch (e: Exception) {
                Log.w(TAG, "queueing a frame failed", e)
                return false
            }
        }
        return true
    }

    private val callback = object : MediaCodec.Callback() {
        override fun onInputBufferAvailable(codec: MediaCodec, index: Int) {
            synchronized(lock) {
                if (released) return
                free.addLast(index)
                if (!feed()) onError("couldn't queue a frame")
            }
        }

        override fun onOutputBufferAvailable(codec: MediaCodec, index: Int, info: MediaCodec.BufferInfo) {
            synchronized(lock) {
                if (released) return
                try {
                    // Shown now: it's live video, not a clip to keep time with.
                    codec.releaseOutputBuffer(index, info.size > 0)
                } catch (e: Exception) {
                    Log.w(TAG, "showing a frame failed", e)
                }
            }
        }

        override fun onError(codec: MediaCodec, e: MediaCodec.CodecException) {
            Log.w(TAG, "decoder error", e)
            onError(e.diagnosticInfo ?: "decoder error")
        }

        override fun onOutputFormatChanged(codec: MediaCodec, format: MediaFormat) {}
    }

    override fun release() {
        synchronized(lock) {
            if (released) return
            released = true
            free.clear()
            queued.clear()
        }
        try { codec.stop() } catch (_: Exception) {}
        try { codec.release() } catch (_: Exception) {}
        thread.quitSafely()
    }

    companion object {
        private const val TAG = "HammockVideo"
        private const val MIME = MediaFormat.MIMETYPE_VIDEO_AVC
        private const val MAX_QUEUED = 8

        /** Decoders onto this surface. */
        fun factory(surface: Surface): DecoderFactory = DecoderFactory { config, onError -> create(surface, config, onError) }

        /** The platform's default H.264 decoder (hardware first), and whether it does low latency. */
        private val lowLatency: Boolean by lazy {
            try {
                val name = MediaCodecList(MediaCodecList.REGULAR_CODECS).findDecoderForFormat(MediaFormat.createVideoFormat(MIME, 1280, 720))
                    ?: return@lazy false
                val info = MediaCodecList(MediaCodecList.REGULAR_CODECS).codecInfos.firstOrNull { it.name == name } ?: return@lazy false
                info.getCapabilitiesForType(MIME).isFeatureSupported(MediaCodecInfo.CodecCapabilities.FEATURE_LowLatency)
            } catch (_: Exception) {
                false
            }
        }

        fun create(surface: Surface, config: VideoConfig, onError: (String) -> Unit): VideoDecoder? {
            if (!surface.isValid) return null
            val thread = HandlerThread("hammock-decoder").apply { start() }
            var codec: MediaCodec? = null
            try {
                codec = MediaCodec.createDecoderByType(MIME)
                val d = MediaCodecDecoder(codec, thread)
                d.onError = onError
                codec.setCallback(d.callback, Handler(thread.looper))
                codec.configure(format(config), surface, null, 0)
                codec.start()
                return d
            } catch (e: Exception) {
                Log.w(TAG, "couldn't start a decoder for ${config.width}x${config.height}", e)
                try { codec?.release() } catch (_: Exception) {}
                thread.quitSafely()
                return null
            }
        }

        private fun format(c: VideoConfig): MediaFormat = MediaFormat.createVideoFormat(MIME, c.width, c.height).apply {
            setByteBuffer("csd-0", ByteBuffer.wrap(c.sets.csd0))
            setByteBuffer("csd-1", ByteBuffer.wrap(c.sets.csd1))
            // A keyframe of a busy screen can be big: room for an uncompressed picture.
            setInteger(MediaFormat.KEY_MAX_INPUT_SIZE, maxOf(c.width * c.height * 3 / 2, 1 shl 20))
            // Real time: decoded as soon as it comes, each picture out at once.
            setInteger(MediaFormat.KEY_PRIORITY, 0)
            if (lowLatency) setInteger(MediaFormat.KEY_LOW_LATENCY, 1)
        }
    }
}
