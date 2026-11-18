package com.homegrownsw.hammock.client

import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.CompletableDeferred
import okhttp3.ConnectionSpec
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.TlsVersion
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.net.Socket
import java.security.MessageDigest
import java.security.cert.CertificateException
import java.security.cert.X509Certificate
import java.util.concurrent.Executors
import java.util.concurrent.RejectedExecutionException
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.ScheduledExecutorService
import java.util.concurrent.ScheduledFuture
import java.util.concurrent.TimeUnit
import javax.net.ssl.SSLContext
import javax.net.ssl.SSLEngine
import javax.net.ssl.X509ExtendedTrustManager

class DaemonException(message: String) : Exception(message)

/**
 * Stands in for a daemon that isn't there (the demo): gets everything a
 * connection would send, and answers through `reply`, from any thread. Replies
 * to calls carry the call's id in replyTo; anything else is an event.
 */
interface DaemonStandIn {
    fun receive(msg: ClientMessage, reply: (ServerMessage) -> Unit)
}

/**
 * A connection to a paired computer's daemon: a WebSocket at wss://address/v1
 * over TLS 1.3 that trusts only the certificate pinned at pairing, raced over
 * the computer's addresses happy-eyeballs style (the first starts now, each
 * next one 250 ms later; the first to finish TLS and the upgrade wins).
 * Replies are matched to requests by id; everything else goes to [onEvent].
 * All callbacks run on the connection's own thread.
 */
class DaemonConnection private constructor(
    private val addresses: List<String>,
    private val lanSubnets: List<String>,
    private val lastWorked: String?,
    private val pin: String?,
    private val standIn: DaemonStandIn?,
    private val localIPs: () -> List<String>,
) {
    companion object {
        const val STAGGER_MS = 250L
        const val DEADLINE_MS = 12_000L
        private const val SUBPROTOCOL = "hammock.v1"

        fun remote(
            addresses: List<String>, lanSubnets: List<String>, lastWorked: String?, certSHA256: String,
            localIPs: () -> List<String> = RemoteCandidates::interfaceAddresses,
        ) = DaemonConnection(addresses, lanSubnets, lastWorked, certSHA256.lowercase(), null, localIPs)

        fun standIn(s: DaemonStandIn) = DaemonConnection(emptyList(), emptyList(), null, null, s) { emptyList() }

        private val sharedClient = OkHttpClient()

        fun sha256Hex(b: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(b).hex()
    }

    /** Unsolicited messages: state updates, pane output, agent views… */
    @Volatile var onEvent: ((ServerMessage) -> Unit)? = null
    /** The connection became ready (true) or closed or failed (false, with why). */
    @Volatile var onStateChange: ((Boolean, Throwable?) -> Unit)? = null
    /** In-band video by media session id (MediaFrame, MediaEnded). */
    private val mediaHandlers = HashMap<String, (ServerMessage) -> Unit>()
    /**
     * A session's messages that came before its handler: the daemon starts
     * sending as soon as it answers OpenMedia, before the stream has the
     * ticket. Handed over in order when the handler arrives (unacked, the
     * first frames would hold up the rest). A few sessions, a few seconds' worth.
     */
    private val earlyMedia = object : LinkedHashMap<String, ArrayList<ServerMessage>>() {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ArrayList<ServerMessage>>) = size > 4
    }

    private fun media(id: String, msg: ServerMessage, end: Boolean = false) {
        val h = if (end) mediaHandlers.remove(id) else mediaHandlers[id]
        if (h != null) { h(msg); return }
        val q = earlyMedia.getOrPut(id) { ArrayList() }
        if (q.size < 60) q.add(msg)
    }

    private val queue: ScheduledExecutorService = Executors.newSingleThreadScheduledExecutor { r ->
        Thread(r, "hammock-connection").apply { isDaemon = true }
    }
    /** Work for the connection's thread; dropped once it's shut down (OkHttp calls back after a close). */
    private fun onQueue(block: () -> Unit) {
        try { queue.execute(block) } catch (_: RejectedExecutionException) {}
    }

    private fun later(ms: Long, block: () -> Unit): ScheduledFuture<*>? =
        try { queue.schedule(block, ms, TimeUnit.MILLISECONDS) } catch (_: RejectedExecutionException) { null }

    private val shutDown = AtomicBoolean(false)
    private var nextID = 0L
    private val pending = HashMap<Long, CompletableDeferred<ServerMessage>>()
    private var ws: WebSocket? = null
    private var dial: Dial? = null
    private var standInOpen = false
    private var closed = false
    @Volatile var connectedAddress: String? = null
        private set

    /** The pin of the computer's certificate (hex SHA-256 of its DER); null for a stand-in. */
    val certSHA256: String? get() = pin

    private class Dial {
        val attempts = HashMap<WebSocket, String>()
        var scheduled = 0
        var lastError: Throwable? = null
        var done = false
        val timers = ArrayList<ScheduledFuture<*>?>()
        fun stop() {
            done = true
            timers.forEach { it?.cancel(false) }
            attempts.keys.forEach { it.cancel() }
            attempts.clear()
        }
    }

    private val client: OkHttpClient? by lazy {
        val pin = pin ?: return@lazy null
        val tm = PinnedTrustManager(pin)
        val ctx = SSLContext.getInstance("TLS")
        ctx.init(null, arrayOf(tm), null)
        // One client's dispatcher and pools for every connection, each with its own pin.
        sharedClient.newBuilder()
            .sslSocketFactory(ctx.socketFactory, tm)
            .hostnameVerifier { _, _ -> true } // the pin is the identity; the certificate names nothing reachable
            .connectionSpecs(listOf(ConnectionSpec.Builder(ConnectionSpec.MODERN_TLS).tlsVersions(TlsVersion.TLS_1_3).build()))
            .connectTimeout(DEADLINE_MS, TimeUnit.MILLISECONDS)
            .readTimeout(0, TimeUnit.MILLISECONDS)
            // A dead link (Wi‑Fi gone) looks open for minutes: a ping every
            // 10 s, and no pong by the next one closes it, so it's dialled again.
            .pingInterval(10, TimeUnit.SECONDS)
            .retryOnConnectionFailure(false)
            .build()
    }

    fun connect() {
        onQueue {
            if (closed) return@onQueue
            if (standIn != null) {
                standInOpen = true
                onStateChange?.invoke(true, null)
                return@onQueue
            }
            startDial()
        }
    }

    /** Closes the connection; no callbacks after. */
    fun disconnect() {
        if (!shutDown.compareAndSet(false, true)) return // once: it may be asked again by whoever lost the race
        onQueue {
            closed = true
            dial?.stop()
            dial = null
            ws?.cancel() // not close(): its close handshake would call back for a minute after
            ws = null
            standInOpen = false
            failAll(DaemonException("Connection closed"))
        }
        later(1000) { queue.shutdown() }
    }

    private fun startDial() {
        val d = Dial()
        dial = d
        val candidates = RemoteCandidates.ordered(addresses, lanSubnets, lastWorked, localIPs())
        if (candidates.isEmpty()) {
            dialFailed(d, DaemonException("No address for this computer can be reached from this network."))
            return
        }
        candidates.forEachIndexed { i, address ->
            d.scheduled++
            d.timers += later(STAGGER_MS * i) {
                if (!d.done) {
                    d.scheduled--
                    attempt(address, d)
                }
            }
        }
        d.timers += later(DEADLINE_MS) { if (!d.done) dialFailed(d, DaemonException("The computer didn't answer.")) }
    }

    private fun attempt(address: String, d: Dial) {
        val c = client ?: return
        val req = Request.Builder().url("wss://$address/v1").header("Sec-WebSocket-Protocol", SUBPROTOCOL).build()
        val sock = c.newWebSocket(req, Listener(address, d))
        d.attempts[sock] = address
    }

    private inner class Listener(val address: String, val d: Dial) : WebSocketListener() {
        override fun onOpen(webSocket: WebSocket, response: Response) = onQueue {
            if (d.done || closed) {
                if (webSocket !== ws) webSocket.cancel()
                return@onQueue
            }
            d.attempts.remove(webSocket)
            d.stop()
            if (dial === d) dial = null
            ws = webSocket
            connectedAddress = address
            onStateChange?.invoke(true, null)
        }

        override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
            val data = bytes.toByteArray()
            onQueue {
                if (webSocket !== ws) return@onQueue
                val body = Wire.decode(data) ?: return@onQueue webSocket.cancel()
                val msg = try { ServerMessage.parseFrom(body) } catch (_: Exception) { return@onQueue }
                deliver(msg)
            }
        }

        override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
            webSocket.close(1000, null)
        }

        override fun onClosed(webSocket: WebSocket, code: Int, reason: String) = onQueue {
            lost(webSocket, DaemonException(reason.ifEmpty { "Connection closed" }))
        }

        override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) = onQueue {
            lost(webSocket, t)
        }

        private fun lost(webSocket: WebSocket, t: Throwable) {
            if (webSocket === ws) {
                ws = null
                connectedAddress = null
                failAll(t)
                if (!closed) onStateChange?.invoke(false, t)
                return
            }
            if (d.attempts.remove(webSocket) != null && !d.done) {
                d.lastError = t
                checkExhausted(d)
            }
        }
    }

    private fun checkExhausted(d: Dial) {
        if (d.done || d.attempts.isNotEmpty() || d.scheduled > 0) return
        dialFailed(d, d.lastError ?: DaemonException("Connection closed"))
    }

    private fun dialFailed(d: Dial, t: Throwable) {
        d.stop()
        if (dial === d) dial = null
        failAll(t)
        if (!closed) onStateChange?.invoke(false, t)
    }

    private fun failAll(t: Throwable) {
        val p = pending.values.toList()
        pending.clear()
        p.forEach { it.completeExceptionally(t) }
        val media = mediaHandlers.toMap()
        mediaHandlers.clear()
        earlyMedia.clear()
        media.forEach { (id, h) ->
            h(ServerMessage.newBuilder().setMediaEnded(hammock.v1.Hammock.MediaEnded.newBuilder().setSessionId(id).setReason("The connection to Hammock closed.")).build())
        }
    }

    private fun deliver(msg: ServerMessage) {
        when (msg.msgCase) {
            ServerMessage.MsgCase.MEDIA_FRAME -> { media(msg.mediaFrame.sessionId, msg); return }
            ServerMessage.MsgCase.MEDIA_ENDED -> { media(msg.mediaEnded.sessionId, msg, end = true); return }
            ServerMessage.MsgCase.SCREEN_CURSOR -> { media(msg.screenCursor.sessionId, msg); return }
            ServerMessage.MsgCase.SCREEN_STATE -> { media(msg.screenState.sessionId, msg); return }
            else -> {}
        }
        val cb = if (msg.replyTo != 0L) pending.remove(msg.replyTo) else null
        if (cb != null) {
            if (msg.msgCase == ServerMessage.MsgCase.ERROR) cb.completeExceptionally(DaemonException(msg.error.message))
            else cb.complete(msg)
        } else {
            onEvent?.invoke(msg)
        }
    }

    /** Routes a media session's frames, cursor, state and end to `handler` (null stops). */
    fun setMediaHandler(sessionID: String, handler: ((ServerMessage) -> Unit)?) = onQueue {
        if (handler == null) {
            mediaHandlers.remove(sessionID)
            earlyMedia.remove(sessionID)
        } else {
            mediaHandlers[sessionID] = handler
            earlyMedia.remove(sessionID)?.forEach { msg ->
                // (One that ended meanwhile takes its handler with it.)
                if (mediaHandlers[sessionID] === handler) {
                    if (msg.msgCase == ServerMessage.MsgCase.MEDIA_ENDED) mediaHandlers.remove(sessionID)
                    handler(msg)
                }
            }
        }
    }

    private val isOpen get() = ws != null || standInOpen

    private fun write(msg: ClientMessage) {
        val s = standIn
        if (s != null) {
            s.receive(msg) { reply -> onQueue { if (standInOpen) deliver(reply) } }
            return
        }
        ws?.send(msg.toByteArray().toByteString())
    }

    /** Sends a request and waits for its reply (a daemon error throws [DaemonException]). */
    suspend fun call(build: ClientMessage.Builder.() -> Unit): ServerMessage {
        val b = ClientMessage.newBuilder().apply(build)
        val d = CompletableDeferred<ServerMessage>()
        val work = Runnable {
            if (!isOpen) {
                d.completeExceptionally(DaemonException("Not connected"))
            } else {
                nextID++
                b.id = nextID
                pending[nextID] = d
                write(b.build())
            }
        }
        try { queue.execute(work) } catch (_: RejectedExecutionException) { d.completeExceptionally(DaemonException("Not connected")) }
        return d.await()
    }

    /** Sends a message without waiting for a reply. */
    fun send(build: ClientMessage.Builder.() -> Unit) {
        val m = ClientMessage.newBuilder().apply(build).setId(0).build()
        onQueue { if (isOpen) write(m) }
    }

    /** Runs `block` on the connection's thread. */
    fun post(block: () -> Unit) = onQueue(block)
}

/**
 * Trusts exactly one certificate: the leaf whose DER hashes to the pin from
 * pairing. No CA, no names: the computer's certificate is self-signed.
 */
class PinnedTrustManager(private val pin: String) : X509ExtendedTrustManager() {
    private fun check(chain: Array<out X509Certificate>?) {
        val leaf = chain?.firstOrNull() ?: throw CertificateException("no certificate")
        if (DaemonConnection.sha256Hex(leaf.encoded) != pin) throw CertificateException("not the paired computer's certificate")
    }

    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?) = check(chain)
    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = check(chain)
    override fun checkServerTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = check(chain)
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?) = throw CertificateException("not a server")
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, socket: Socket?) = throw CertificateException("not a server")
    override fun checkClientTrusted(chain: Array<out X509Certificate>?, authType: String?, engine: SSLEngine?) = throw CertificateException("not a server")
    override fun getAcceptedIssuers(): Array<X509Certificate> = emptyArray()
}

/**
 * The remote link's message framing (internal/wirezstd). Only the plain
 * `hammock.v1` subprotocol is offered for now, so messages arrive as is; a
 * leading 0 still means "as is" (video), and 1 (compressed) never comes.
 */
object Wire {
    fun decode(m: ByteArray): ByteArray? {
        if (m.isEmpty()) return m
        return when (m[0].toInt()) {
            0 -> m.copyOfRange(1, m.size)
            1 -> null // compressed: not offered, so out of step
            else -> m
        }
    }
}
