package com.homegrownsw.hammock.media

import java.io.IOException

/**
 * One media session's QUIC connection (ADR-0012; datagrams for screens,
 * ADR-0014): whole frame records, already acked.
 */
interface QuicSession {
    /** The next frame record, waiting up to timeoutMs: null if none came. Throws once the video has ended. */
    fun next(timeoutMs: Int): ByteArray?
    fun keyframe()
    fun close()
}

/** Connects a session's QUIC (the native one, or a test's). Throws IOException saying why it couldn't. */
fun interface QuicDialer {
    fun open(host: String, port: Int, pin: String, ticket: ByteArray, datagrams: Boolean): QuicSession
}

/** Thrown by next once the video has ended. */
class QuicEnded(why: String) : IOException(why)

/**
 * The daemon's own QUIC (quic-go), as libhmkquic.so (cmd/hmkquic,
 * internal/mediaview): it connects, checks the certificate against the pin,
 * presents the ticket, puts datagram frames back together and acks each
 * frame record.
 */
object Quic : QuicDialer {
    private val loaded: Throwable? = try {
        System.loadLibrary("hmkquic")
        null
    } catch (t: Throwable) {
        t
    }

    @JvmStatic private external fun nativeOpen(host: String, port: Int, pin: String, ticket: ByteArray, datagrams: Boolean): Long
    @JvmStatic private external fun nativeNext(handle: Long, timeoutMs: Int): ByteArray?
    @JvmStatic private external fun nativeKeyframe(handle: Long)
    @JvmStatic private external fun nativeReason(handle: Long): String
    @JvmStatic private external fun nativeClose(handle: Long)

    override fun open(host: String, port: Int, pin: String, ticket: ByteArray, datagrams: Boolean): QuicSession {
        loaded?.let { throw IOException("QUIC isn't available: ${it.message}") }
        val h = nativeOpen(host, port, pin, ticket, datagrams)
        return object : QuicSession {
            @Volatile private var closed = false
            override fun next(timeoutMs: Int): ByteArray? {
                if (closed) throw QuicEnded("The video stopped.")
                val rec = nativeNext(h, timeoutMs) ?: return null
                if (rec.isEmpty()) throw QuicEnded(nativeReason(h).ifEmpty { "The video stopped." })
                return rec
            }
            override fun keyframe() { if (!closed) nativeKeyframe(h) }
            override fun close() {
                if (closed) return
                closed = true
                nativeClose(h)
            }
        }
    }
}
