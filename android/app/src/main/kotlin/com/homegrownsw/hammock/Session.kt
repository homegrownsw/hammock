package com.homegrownsw.hammock

import android.content.Context
import android.os.Build
import android.provider.Settings
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.DaemonStandIn
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.PairingCodes
import com.homegrownsw.hammock.client.RemoteCandidates
import com.homegrownsw.hammock.client.RemoteIdentity
import hammock.v1.Hammock.Pairing
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import java.net.InetSocketAddress
import java.net.Socket
import java.util.UUID

/** A pairing link opened from outside the app, waiting for a yes. */
data class PairOffer(val code: String, val pairing: Pairing)

/**
 * Owns the paired computers (this phone's key for each) and the live model
 * of the one shown, as the iPhone's Session does. One per app.
 */
class Session(
    private val context: Context,
    /** Called with each model as it's made, to hook notifications to it (set before the first is made). */
    var onModel: ((AppModel, StoredHost) -> Unit)? = null,
) {
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)
    private val store = IdentityStore(context)
    private val prefs = context.getSharedPreferences("session", Context.MODE_PRIVATE)

    private val _hosts = MutableStateFlow<List<StoredHost>>(emptyList())
    val hosts: StateFlow<List<StoredHost>> = _hosts.asStateFlow()
    private val _host = MutableStateFlow<StoredHost?>(null)
    /** The computer shown. */
    val host: StateFlow<StoredHost?> = _host.asStateFlow()
    private val _model = MutableStateFlow<AppModel?>(null)
    /** Its live model (nil with nothing paired). */
    val model: StateFlow<AppModel?> = _model.asStateFlow()
    private val _pairingError = MutableStateFlow<String?>(null)
    val pairingError: StateFlow<String?> = _pairingError.asStateFlow()
    private val _pairing = MutableStateFlow(false)
    /** A pairing is under way (finding the computer, signing in). */
    val pairing: StateFlow<Boolean> = _pairing.asStateFlow()
    private val _offered = MutableStateFlow<PairOffer?>(null)
    val offered: StateFlow<PairOffer?> = _offered.asStateFlow()

    /** A computer being paired while another is shown: it takes over once paired. */
    private var pending: AppModel? = null
    private var probe: Job? = null


    val isDemo: Boolean get() = _model.value?.isDemo == true

    /**
     * Whether the app is in use (HammockApplication): the computer shown is
     * only connected then, not when a push wakes the app in the background.
     */
    private var active = false

    init {
        val stored = store.load()
        _hosts.value = stored.hosts
        val h = stored.hosts.firstOrNull { it.id == stored.active } ?: stored.hosts.firstOrNull()
        if (h != null) {
            _host.value = h
            _model.value = connect(h, null)
        }
    }

    /** The app's in use: the computer shown connects (or checks its link still carries). */
    fun resume() {
        active = true
        _model.value?.let { it.resume(); it.checkConnection() }
    }

    /** The app's been away a while: the computer shown hangs up until [resume]. */
    fun pause() {
        active = false
        _model.value?.takeIf { !it.isDemo }?.pause()
    }

    /** What this phone calls itself to its computers (Devices ▸ This Phone). */
    var deviceName: String
        get() = prefs.getString("deviceName", null)?.takeIf { it.isNotBlank() }
            ?: Settings.Global.getString(context.contentResolver, Settings.Global.DEVICE_NAME)
            ?: Build.MODEL
        set(v) { prefs.edit().putString("deviceName", v.trim()).apply() }

    /** A link opened from elsewhere: asks first (PairConfirmDialog). */
    fun offer(code: String) {
        val p = PairingCodes.parse(code)
        if (p == null) {
            _pairingError.value = "That doesn't look like a Hammock pairing code."
            return
        }
        _offered.value = PairOffer(code, p)
    }

    fun dismissOffer() { _offered.value = null }

    /** Pairs with a `hammock://pair/…` link or code from the computer. */
    fun pair(code: String, switching: Boolean = true) {
        _offered.value = null
        val p = PairingCodes.parse(code)
        if (p == null) {
            _pairingError.value = "That doesn't look like a Hammock pairing code."
            return
        }
        if (p.addressesCount == 0) {
            _pairingError.value = "The computer didn't include an address. Is remote access on there, with a network chosen (Hammock ▸ Settings ▸ Devices on a Mac, hmk remote networks on a server)?"
            return
        }
        _pairingError.value = if (p.remoteEnabled) null
        else "Turn on “Allow paired devices to connect” in Hammock ▸ Settings ▸ Devices on your Mac (on a server: hmk remote on), then try again."
        val candidates = RemoteCandidates.ordered(p.addressesList, p.lanSubnetsList, null, RemoteCandidates.interfaceAddresses())
        probe?.cancel()
        _pairing.value = true
        probe = scope.launch {
            val address = firstReachable(candidates)
            if (address == null) {
                _pairing.value = false
                _pairingError.value = "Can't reach the computer. Is remote access on there, and is this phone on one of its networks (the same Wi‑Fi, Tailscale or your VPN)?"
                return@launch
            }
            pair(p, address, switching)
        }
    }

    private fun pair(p: Pairing, address: String, switching: Boolean) {
        val alias = "host-" + UUID.randomUUID()
        // Ed25519 in the Keystore needs Android 13's KeyMint: a phone upgraded
        // from an older one may not have it.
        try {
            KeystoreKey.get(alias)
        } catch (e: Exception) {
            _pairing.value = false
            _pairingError.value = "This phone can't keep a key for Hammock: its Keystore has no Ed25519 (${e.javaClass.simpleName})."
            return
        }
        val h = StoredHost(
            hostName = p.hostName, address = address, addresses = p.addressesList, lanSubnets = p.lanSubnetsList,
            certSHA256 = p.certSha256.lowercase(), deviceID = null, keyAlias = alias,
        )
        pending?.stop()
        val m = connect(h, p.secret.toByteArray(), switching)
        if (_model.value == null) {
            _host.value = h
            _model.value = m
        } else {
            pending = m
        }
        // Signing in failing (a used or expired code) shows here.
        scope.launch {
            val r = withTimeoutOrNull(20_000) { m.link.first { it is Link.Failed || it == Link.Connected } }
            // Only this attempt's: another may have started since. Failing
            // once paired (signing in after) isn't the pairing's: it retries.
            val ours = m === pending || m === _model.value
            if (ours && r is Link.Failed && m.isPairing) {
                _pairingError.value = "Couldn't pair: ${r.message}"
                cancelPairing()
            }
            if (ours) _pairing.value = false
        }
    }

    /** The first address to take a connection, as soon as one does (a probe that hangs isn't waited for). */
    private suspend fun firstReachable(addresses: List<String>): String? {
        if (addresses.isEmpty()) return null
        val result = MutableStateFlow<String?>(null)
        val left = java.util.concurrent.atomic.AtomicInteger(addresses.size)
        val probes = CoroutineScope(SupervisorJob() + Dispatchers.IO)
        addresses.forEachIndexed { i, a ->
            probes.launch {
                try {
                    delay(250L * i)
                    if (result.value != null) return@launch
                    val host = RemoteCandidates.host(a)
                    val port = RemoteCandidates.port(a) ?: return@launch
                    val ok = runCatching { Socket().use { it.connect(InetSocketAddress(host, port), 4000) } }.isSuccess
                    if (ok) result.compareAndSet(null, a)
                } finally {
                    if (left.decrementAndGet() == 0) result.compareAndSet(null, "") // all failed
                }
            }
        }
        val a = withTimeoutOrNull(12_000) { result.first { it != null } }
        probes.coroutineContext[Job]?.cancel()
        return a?.ifEmpty { null }
    }

    /** Stops a pairing that hasn't finished. */
    fun cancelPairing() {
        probe?.cancel()
        _pairing.value = false
        pending?.let { it.stop(); pending = null; return }
        val h = _host.value
        if (h != null && h.deviceID == null && _hosts.value.none { it.id == h.id }) {
            _model.value?.stop()
            _model.value = null
            _host.value = null
            KeystoreKey.delete(h.keyAlias)
        }
    }

    fun clearPairingError() { _pairingError.value = null }

    /** Shows another paired computer. */
    fun show(h: StoredHost) {
        if (h.id == _host.value?.id && !isDemo) return
        _model.value?.stop()
        _host.value = h
        _model.value = connect(h, null)
        persist()
    }

    /** Forgets a computer (it can be paired again with a new code). */
    fun forget(h: StoredHost) {
        val shown = if (h.id == _host.value?.id) _model.value else null
        _hosts.value = _hosts.value.filter { it.id != h.id }
        if (shown != null) {
            _model.value = null
            _host.value = null
            _hosts.value.firstOrNull()?.let { show(it) }
        }
        persist()
        Push.forget(context, h.id)
        // Ask it to stop pushing here first, if it answers soon.
        scope.launch {
            val m = shown ?: if (h.deviceID != null) model(h, null).also { it.start() } else null
            if (m != null && !m.isDemo) {
                withTimeoutOrNull(10_000) { m.link.first { it == Link.Connected } }
                if (m.link.value == Link.Connected) {
                    m.registerPush("", "", ByteArray(0), emptyList())
                    delay(500)
                }
            }
            m?.stop()
            KeystoreKey.delete(h.keyAlias)
        }
    }

    /** Renamed this phone: the computer shown hears it as the phone signs in again. */
    fun renamed() {
        val h = _host.value ?: return
        if (h.deviceID == null || pending != null || isDemo) return
        _model.value?.stop()
        _model.value = connect(h, null)
    }

    /** The demo (a made-up Mac), in place of any computer shown. */
    fun startDemo(standIn: DaemonStandIn) {
        _model.value?.stop()
        _host.value = StoredHost("MacBook", "", emptyList(), emptyList(), "demo", "sample", "")
        val m = AppModel.standIn(standIn)
        m.start()
        _model.value = m
    }

    fun endDemo() {
        if (!isDemo) return
        _model.value?.stop()
        _model.value = null
        _host.value = null
        _hosts.value.firstOrNull()?.let { show(it) }
    }

    /** A connection of its own to a paired computer, not shown (to answer a notification). The caller stops it. */
    fun briefConnection(hostID: String): AppModel? {
        val h = _hosts.value.firstOrNull { it.id == hostID && it.deviceID != null } ?: return null
        return model(h, null).also { it.start() }
    }

    fun noteKind(kind: String) {
        val h = _host.value ?: return
        if (kind.isEmpty() || h.kind == kind) return
        update(h.id) { it.copy(kind = kind) }
    }

    private fun persist() = store.save(IdentityStore.Stored(_hosts.value, _host.value?.id))

    private fun update(id: String, change: (StoredHost) -> StoredHost) {
        _hosts.value = _hosts.value.map { if (it.id == id) change(it) else it }
        _host.value?.let { if (it.id == id) _host.value = change(it) }
        persist()
    }

    private fun identity(h: StoredHost, secret: ByteArray?) = RemoteIdentity(
        address = h.address, addresses = h.addresses.ifEmpty { listOf(h.address) }, lanSubnets = h.lanSubnets,
        certSHA256 = h.certSHA256, deviceID = h.deviceID, pairingSecret = secret, deviceName = deviceName,
    )

    private fun model(h: StoredHost, secret: ByteArray?) = AppModel.remote(identity(h, secret), KeystoreKey.get(h.keyAlias))

    private fun connect(h: StoredHost, secret: ByteArray?, switching: Boolean = true): AppModel {
        val m = model(h, secret)
        m.onPaired = { deviceID ->
            scope.launch {
                // A pairing cancelled, or overtaken by another, doesn't take over.
                if (m !== pending && m !== _model.value) return@launch
                val paired = h.copy(deviceID = deviceID)
                // Paired again with a computer already here: its old key goes.
                _hosts.value.firstOrNull { it.id == paired.id && it.keyAlias != paired.keyAlias }?.let { KeystoreKey.delete(it.keyAlias) }
                _hosts.value = _hosts.value.filter { it.id != paired.id } + paired
                _pairingError.value = null
                _pairing.value = false
                if (pending === m) {
                    pending = null
                    if (switching) {
                        _model.value?.stop()
                        _model.value = m
                        _host.value = paired
                        if (!active) m.pause()
                    } else {
                        persist()
                        Push.register(context, m, paired.id)
                        delay(1000)
                        m.stop()
                        return@launch
                    }
                } else {
                    _host.value = paired
                }
                persist()
            }
        }
        m.onIdentityChanged = { id ->
            scope.launch {
                if (_hosts.value.none { it.id == h.id }) return@launch
                update(h.id) { it.copy(address = id.address, addresses = id.addresses, lanSubnets = id.lanSubnets) }
            }
        }
        m.onConnected = { connected ->
            scope.launch {
                val name = connected.workspace.value.hostName
                if (name.isNotEmpty() && _hosts.value.any { it.id == h.id && it.hostName != name }) update(h.id) { it.copy(hostName = name) }
            }
        }
        onModel?.invoke(m, h)
        // (A pairing goes ahead regardless: it's the app's own doing. Paused
        // while it's pairing, it hangs up once paired: AppModel.pause.)
        m.start(paused = !active && secret == null)
        return m
    }
}
