package com.homegrownsw.hammock.client

import com.google.protobuf.ByteString
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentKind
import hammock.v1.Hammock.AgentState
import hammock.v1.Hammock.AttentionItem
import hammock.v1.Hammock.ClientMessage
import hammock.v1.Hammock.Computer
import hammock.v1.Hammock.GrantRequest
import hammock.v1.Hammock.Group
import hammock.v1.Hammock.HammockKeyRequest
import hammock.v1.Hammock.HammockKeyStatus
import hammock.v1.Hammock.HostTheme
import hammock.v1.Hammock.JoinRequest
import hammock.v1.Hammock.Pane
import hammock.v1.Hammock.PaneKind
import hammock.v1.Hammock.Person
import hammock.v1.Hammock.Schedule
import hammock.v1.Hammock.ServerMessage
import hammock.v1.Hammock.StateUpdate
import hammock.v1.Hammock.Tab
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.asCoroutineDispatcher
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import java.util.concurrent.Executors

/** What a computer's daemon has: one immutable snapshot, replaced on each update. */
data class Workspace(
    val groups: List<Group> = emptyList(),
    val tabs: Map<String, Tab> = emptyMap(),
    val panes: Map<String, Pane> = emptyMap(),
    val attention: List<AttentionItem> = emptyList(),
    val grants: List<GrantRequest> = emptyList(),
    val hammockKey: HammockKeyStatus = HammockKeyStatus.getDefaultInstance(),
    val hammockKeyRequests: List<HammockKeyRequest> = emptyList(),
    val people: Map<String, Person> = emptyMap(),
    val joinRequests: List<JoinRequest> = emptyList(),
    val computers: List<Computer> = emptyList(),
    val activeComputerID: String = "",
    val usage: List<Hammock.AgentUsage> = emptyList(),
    val hostTheme: HostTheme? = null,
    val agentKinds: List<AgentKind> = emptyList(),
    val schedules: Map<String, Schedule> = emptyMap(),
    val hostName: String = "",
    val myUserID: String = "",
) {
    val kinds: List<AgentKind> get() = AgentCatalog.resolve(agentKinds)

    fun tabs(group: Group): List<Tab> = group.tabIdsList.mapNotNull { tabs[it] }
    fun groupOf(tab: Tab): Group? = groups.firstOrNull { it.id == tab.groupId }
    fun groupOfPane(paneID: String): Group? = panes[paneID]?.let { tabs[it.tabId] }?.let { groupOf(it) }
    fun panes(tab: Tab): List<Pane> = tab.allPaneIDs().mapNotNull { panes[it] }

    /** The tab's name, else its work's, else a browser's page, else "Tab N". */
    fun title(tab: Tab): String {
        if (tab.name.isNotEmpty()) return tab.name
        if (tab.autoName.isNotEmpty()) return tab.autoName
        val p = panes[tab.focusedPaneId]
        if (p != null && p.kind == PaneKind.PANE_KIND_BROWSER) {
            if (p.title.isNotEmpty() && p.title != p.url) return p.title
            return runCatching { java.net.URI(p.url).host }.getOrNull()?.takeIf { it.isNotEmpty() } ?: "New Tab"
        }
        if (p != null && p.kind == PaneKind.PANE_KIND_SIMULATOR) return p.title
        val i = groupOf(tab)?.tabIdsList?.indexOf(tab.id) ?: -1
        return if (i >= 0) "Tab ${i + 1}" else "Tab"
    }

    /** The Group's name, else its folder's ("Home" for the home directory), else "Group N". */
    fun title(group: Group): String {
        if (group.name.isNotEmpty()) return group.name
        val cwd = panes[group.lastFocusedPaneId]?.cwd ?: ""
        if (cwd.isNotEmpty()) return folderName(cwd)
        if (group.folder.isNotEmpty()) return folderName(group.folder)
        val i = groups.indexOfFirst { it.id == group.id }
        return if (i >= 0) "Group ${i + 1}" else "Group"
    }

    private fun folderName(path: String): String {
        val trimmed = path.trimEnd('/')
        val parts = trimmed.split('/')
        // /Users/me or /home/me: the home directory.
        if ((parts.size == 3 && (parts[1] == "Users" || parts[1] == "home")) || trimmed == "/root") return "Home"
        return parts.lastOrNull()?.ifEmpty { "/" } ?: "/"
    }

    /** The most urgent agent state among a tab's panes (a finished one you've seen shows none). */
    fun agentState(tab: Tab): AgentState? {
        val states = panes(tab).mapNotNull { p ->
            if (p.hasAgent() && !(p.agent.shownState == AgentState.AGENT_STATE_IDLE && p.agent.seen)) p.agent.shownState else null
        }
        return URGENCY.firstOrNull { it in states }
    }

    /** The agent pane a tab's row stands for. */
    fun shownAgentPane(tab: Tab): Pane? {
        val agents = panes(tab).filter { it.hasAgent() }
        fun focusRank(p: Pane) = if (p.id == tab.focusedPaneId) 0 else 1
        val shown = agents.mapNotNull { p ->
            if (p.agent.shownState == AgentState.AGENT_STATE_IDLE && p.agent.seen) null
            else URGENCY.indexOf(p.agent.shownState).takeIf { it >= 0 }?.let { it to p }
        }
        shown.minWithOrNull(compareBy({ it.first }, { focusRank(it.second) }))?.let { return it.second }
        return agents.filter { !it.exited && it.agent.shownState == AgentState.AGENT_STATE_IDLE && it.agent.seen }.minByOrNull { focusRank(it) }
    }

    /** Every tab, in Group order. */
    val orderedTabs: List<Tab> get() = groups.flatMap { tabs(it) }

    /** The ids of what can still need you: a notification about anything else is stale. */
    val liveNoticeIDs: Set<String> get() =
        (attention.map { it.id } + grants.map { it.id } + joinRequests.map { it.id } + hammockKeyRequests.map { it.id }).toSet()

    companion object {
        val URGENCY = listOf(
            AgentState.AGENT_STATE_NEEDS_INPUT, AgentState.AGENT_STATE_ERRORED, AgentState.AGENT_STATE_WORKING,
            AgentState.AGENT_STATE_IDLE, AgentState.AGENT_STATE_STARTING,
        )
    }
}

/** Where a pane's terminal output goes: a terminal engine the app holds. */
interface PaneSink {
    /** The pane's screen as a VT replay, at its size: reset, then write. */
    fun snapshot(cols: Int, rows: Int, data: ByteArray)
    fun output(data: ByteArray)
    fun resized(cols: Int, rows: Int)
}

sealed class Link {
    data object Connecting : Link()
    data object Connected : Link()
    /** Can't connect; unpaired: the computer removed this phone, and it stopped trying (pair again). */
    data class Failed(val message: String, val unpaired: Boolean = false) : Link()
}

/**
 * The client's view of one computer's daemon: its state, the pane streams
 * shown, and every action. State lives on one thread of its own; screens read
 * [workspace] and call the actions from anywhere.
 */
class AppModel private constructor(
    @Volatile private var identity: RemoteIdentity?,
    private val key: DeviceKey?,
    private val standIn: DaemonStandIn?,
    private val clientKind: String,
) {
    companion object {
        fun remote(identity: RemoteIdentity, key: DeviceKey, clientKind: String = "android") = AppModel(identity, key, null, clientKind)
        fun standIn(s: DaemonStandIn, clientKind: String = "android") = AppModel(null, null, s, clientKind)
        /** A stand-in that signs in (or pairs) as a remote one does, for tests. */
        internal fun standIn(s: DaemonStandIn, identity: RemoteIdentity, key: DeviceKey) = AppModel(identity, key, s, "android")

        /**
         * The computer refused this phone's sign-in for good: it doesn't know
         * the device (removed in Settings ▸ Devices) or its key. The daemon
         * says "authentication failed" for either (internal/daemon/remote.go
         * authenticate), and something else for what may pass (its store, a
         * dropped connection).
         */
        fun signInRefused(e: Throwable): Boolean = e is DaemonException && e.message.equals("authentication failed", ignoreCase = true)
    }

    private val executor = Executors.newSingleThreadExecutor { r -> Thread(r, "hammock-model").apply { isDaemon = true } }
    val dispatcher = executor.asCoroutineDispatcher()
    val scope = CoroutineScope(SupervisorJob() + dispatcher)

    private val _workspace = MutableStateFlow(Workspace())
    val workspace: StateFlow<Workspace> = _workspace.asStateFlow()
    private val _link = MutableStateFlow<Link>(Link.Connecting)
    val link: StateFlow<Link> = _link.asStateFlow()
    private val _toast = MutableStateFlow<String?>(null)
    val toast: StateFlow<String?> = _toast.asStateFlow()

    val isDemo get() = standIn != null
    val certSHA256: String? get() = identity?.certSHA256
    /** Signing in with a pairing code, not paired yet. */
    val isPairing: Boolean get() = identity?.let { it.deviceID == null && it.pairingSecret != null } == true

    // Hooks for the app (all called on the model's thread).
    var onPaired: ((deviceID: String) -> Unit)? = null
    var onIdentityChanged: ((RemoteIdentity) -> Unit)? = null
    var onAttention: ((AttentionItem, Pane?) -> Unit)? = null
    var onResolved: ((List<String>) -> Unit)? = null
    var onFullState: ((Workspace) -> Unit)? = null
    var onConnected: ((AppModel) -> Unit)? = null
    /** Messages the model doesn't handle itself (agent views, logs, editors…), by kind. */
    private val listeners = HashMap<ServerMessage.MsgCase, MutableList<(ServerMessage) -> Unit>>()

    @Volatile var connection: DaemonConnection? = null
        private set
    private var stopped = false
    private var paused = false
    /** Paused while pairing: it waits until the pairing's done (paired, or failed). */
    private var pauseWanted = false
    private var checking = false
    private var retryDelay = 200L
    private var challenge: ByteArray? = null
    private var challengeWaiter: kotlinx.coroutines.CompletableDeferred<ByteArray>? = null
    private val knownAttention = HashSet<String>()
    private val sinks = HashMap<String, MutableList<PaneSink>>()
    private val desiredSize = HashMap<String, Pair<Int, Int>>()
    private val detachJobs = HashMap<String, Job>()
    private var lastColors: Hammock.SetColors? = null
    private var toastJob: Job? = null
    private val rewatch = ArrayList<(DaemonConnection) -> Unit>()

    /** Connects, or (paused) waits for [resume] first: the app not on screen yet. */
    fun start(paused: Boolean = false) = scope.launch {
        this@AppModel.paused = paused
        open()
    }

    /**
     * Hangs up until [resume]: the app in the background. A phone that stays
     * signed in tells its computer you're on it (pushes and Hammock Key
     * requests go straight there), and dials and pings for nothing. A pairing
     * under way finishes first: hung up halfway, it never would.
     */
    fun pause() = scope.launch { pauseNow() }

    private fun pauseNow() {
        if (stopped || paused) return
        if (isPairing) { pauseWanted = true; return }
        pauseWanted = false
        paused = true
        connection?.disconnect()
        connection = null
        challengeWaiter?.cancel()
        challengeWaiter = null
        if (_link.value == Link.Connected) _link.value = Link.Connecting
    }

    /** Connects again after [pause]. */
    fun resume() = scope.launch {
        pauseWanted = false
        if (stopped || !paused) return@launch
        paused = false
        retryDelay = 200
        open()
    }

    /** Disconnects for good, and lets the model's thread go. */
    fun stop() = scope.launch {
        stopped = true
        connection?.disconnect()
        connection = null
        challengeWaiter?.cancel()
        challengeWaiter = null
        kotlinx.coroutines.delay(100) // what the stop just sent leaves first
        scope.coroutineContext[Job]?.cancel()
        executor.shutdown()
    }

    /** Whether c is still this model's connection (a reconnect may have replaced it while it was waiting). */
    private fun current(c: DaemonConnection) = connection === c && !stopped

    private fun open() {
        if (stopped || paused) return
        val id = identity
        val c = if (standIn != null) DaemonConnection.standIn(standIn)
        else DaemonConnection.remote(id!!.addresses, id.lanSubnets, id.address, id.certSHA256)
        connection = c
        challenge = null
        challengeWaiter?.cancel() // the old connection's sign-in, if it was waiting
        challengeWaiter = null
        _link.value = Link.Connecting
        c.onEvent = { msg -> scope.launch { if (connection === c) handle(msg) } }
        c.onStateChange = { ready, err -> scope.launch { if (connection === c) connectionChanged(c, ready, err) } }
        c.connect()
    }

    private suspend fun connectionChanged(c: DaemonConnection, ready: Boolean, err: Throwable?) {
        if (stopped) return
        if (ready) {
            handshake(c)
            return
        }
        c.disconnect()
        connection = null
        _link.value = Link.Failed(friendly(err))
        if (pausedAfterFailing()) return
        val d = retryDelay
        retryDelay = minOf(retryDelay * 2, 5000)
        delay(d)
        if (!stopped && connection == null) open()
    }

    /** A pause that waited on a pairing (or on signing in after it) that's failed: it takes effect, rather than dialling again. */
    private fun pausedAfterFailing(): Boolean {
        if (!pauseWanted || stopped) return false
        pauseWanted = false
        paused = true
        return true
    }

    /** Dials again now (back in the foreground, or the network changed), unless connected. */
    fun reconnectNow() = scope.launch {
        if (stopped || paused) return@launch
        if (_link.value !is Link.Connected) {
            connection?.disconnect()
            connection = null
            retryDelay = 200
            open()
        }
    }

    /**
     * Back in the foreground, or on another network: a link waiting to dial
     * again dials now, and one that says it's connected is asked something
     * (its pings only notice a dead one after 20 s): no answer in [timeoutMs],
     * and it's dialled again, as the iPhone's checkConnection.
     */
    fun checkConnection(timeoutMs: Long = 5_000) = scope.launch {
        if (stopped || paused) return@launch
        when (_link.value) {
            is Link.Failed -> reconnectNow()
            Link.Connecting -> {}
            Link.Connected -> {
                val c = connection ?: return@launch
                if (checking) return@launch
                checking = true
                val alive = try {
                    // Hello: answered by any daemon, and changes nothing.
                    kotlinx.coroutines.withTimeoutOrNull(timeoutMs) {
                        c.call { setHello(Hammock.Hello.newBuilder().setProtocolVersion(1).setClientKind(clientKind).setClientName(clientKind).build()) }
                    } != null
                } catch (_: DaemonException) {
                    true // an answer, even a refusal
                } catch (_: Exception) {
                    false
                } finally {
                    checking = false
                }
                if (!alive && current(c)) connectionChanged(c, false, java.net.SocketTimeoutException())
            }
        }
    }

    private fun friendly(t: Throwable?): String = when {
        t == null -> "The connection closed."
        t is javax.net.ssl.SSLHandshakeException || t.cause is java.security.cert.CertificateException ->
            "This isn't the computer you paired with (its certificate changed)."
        t is java.net.ConnectException || t is java.net.NoRouteToHostException -> "The computer can't be reached."
        t is java.net.SocketTimeoutException -> "The computer didn't answer."
        else -> t.message ?: t.javaClass.simpleName
    }

    private suspend fun nextChallenge(): ByteArray {
        challenge?.let { challenge = null; return it }
        val w = kotlinx.coroutines.CompletableDeferred<ByteArray>()
        challengeWaiter = w
        return kotlinx.coroutines.withTimeout(15_000) { w.await() }
    }

    private suspend fun handshake(c: DaemonConnection) {
        try {
            identity?.let { authenticate(c, it, nextChallenge()) }
            if (!current(c)) return
            val hello = c.call {
                setHello(Hammock.Hello.newBuilder().setProtocolVersion(1).setClientKind(clientKind).setClientName(clientKind).build())
            }.helloReply
            var id = identity
            if (id != null) {
                if (hello.addressesCount > 0) id = id.copy(addresses = hello.addressesList, lanSubnets = hello.lanSubnetsList)
                c.connectedAddress?.let { id = id!!.copy(address = it) }
                identity = id
                onIdentityChanged?.invoke(id!!)
            }
            if (!current(c)) return
            val state = c.call { setSubscribe(Hammock.Subscribe.getDefaultInstance()) }.state
            if (!current(c)) return
            knownAttention.clear()
            _workspace.value = _workspace.value.copy(hostName = hello.hostName, myUserID = hello.userId)
            apply(state)
            _link.value = Link.Connected
            retryDelay = 200
            lastColors?.let { col -> c.send { setColors = col } }
            for ((paneID, list) in sinks) {
                if (list.isEmpty() || _workspace.value.panes[paneID] == null) continue
                desiredSize[paneID]?.let { (cols, rows) -> c.send { resize = resizeMsg(paneID, cols, rows) } }
                c.send { setAttachPane(Hammock.AttachPane.newBuilder().setPaneId(paneID).build()) }
            }
            rewatch.toList().forEach { it(c) }
            onConnected?.invoke(this)
            // Paired while the app was away: it hangs up now, as it would have.
            if (pauseWanted) pauseNow()
        } catch (e: Exception) {
            c.disconnect()
            // Superseded meanwhile (reconnected, stopped): the new connection carries on.
            if (!current(c)) return
            connection = null
            if (identity?.deviceID != null && signInRefused(e)) {
                // Removed on the computer: don't keep knocking; pair again.
                _link.value = Link.Failed("This phone was removed from the computer. Pair it again to connect.", unpaired = true)
                stopped = true
                return
            }
            _link.value = Link.Failed(e.message ?: "Couldn't sign in")
            if (pausedAfterFailing()) return
            if (!stopped) {
                delay(retryDelay)
                retryDelay = minOf(retryDelay * 2, 5000)
                if (!stopped && connection == null) open()
            }
        }
    }

    private suspend fun authenticate(c: DaemonConnection, id: RemoteIdentity, nonce: ByteArray) {
        val k = key ?: throw DaemonException("No key for this computer")
        val deviceID = id.deviceID
        if (deviceID != null) {
            if (nonce.size != 32) throw DaemonException("Bad challenge")
            val sig = k.sign(DeviceAuth.message(id.certSHA256, nonce))
            c.call {
                setAuthenticate(Hammock.Authenticate.newBuilder().setDeviceId(deviceID).setSignature(ByteString.copyFrom(sig))
                    .setDeviceName(id.deviceName).build())
            }
        } else {
            val secret = id.pairingSecret ?: throw DaemonException("This device isn't paired")
            val r = c.call {
                setPair(Hammock.Pair.newBuilder().setSecret(ByteString.copyFrom(secret)).setDeviceName(id.deviceName)
                    .setPlatform(id.platform).setPublicKey(ByteString.copyFrom(k.publicKey)).build())
            }
            if (!current(c)) throw DaemonException("Pairing was cancelled")
            val newID = id.copy(deviceID = r.paired.deviceId, pairingSecret = null)
            identity = newID
            onPaired?.invoke(r.paired.deviceId)
            onIdentityChanged?.invoke(newID)
        }
    }

    // MARK: Messages

    /** Listens for a kind of message the model doesn't handle itself. Returns a function that stops. */
    fun listen(kind: ServerMessage.MsgCase, fn: (ServerMessage) -> Unit): () -> Unit {
        scope.launch { listeners.getOrPut(kind) { ArrayList() }.add(fn) }
        return { scope.launch { listeners[kind]?.remove(fn) } }
    }

    /** Runs `fn` with each new connection once signed in (re-watching what's shown). Returns a function that stops. */
    fun onEachConnection(fn: (DaemonConnection) -> Unit): () -> Unit {
        scope.launch {
            rewatch.add(fn)
            if (_link.value == Link.Connected) connection?.let(fn)
        }
        return { scope.launch { rewatch.remove(fn) } }
    }

    private fun handle(msg: ServerMessage) {
        when (msg.msgCase) {
            ServerMessage.MsgCase.STATE -> apply(msg.state)
            ServerMessage.MsgCase.PANE_SNAPSHOT -> {
                val s = msg.paneSnapshot
                sinks[s.paneId]?.forEach { it.snapshot(s.cols, s.rows, s.data.toByteArray()) }
            }
            ServerMessage.MsgCase.PANE_OUTPUT -> {
                val o = msg.paneOutput
                val list = sinks[o.paneId] ?: return
                val data = o.data.toByteArray()
                list.forEach { it.output(data) }
            }
            ServerMessage.MsgCase.AUTH_CHALLENGE -> {
                val n = msg.authChallenge.nonce.toByteArray()
                val w = challengeWaiter
                if (w != null && !w.isCompleted) { challengeWaiter = null; w.complete(n) } else challenge = n
            }
            else -> listeners[msg.msgCase]?.toList()?.forEach { it(msg) }
        }
    }

    internal fun apply(u: StateUpdate) {
        var w = _workspace.value
        if (u.full) {
            w = w.copy(groups = emptyList(), tabs = emptyMap(), panes = emptyMap(), attention = emptyList(), grants = emptyList(),
                hammockKeyRequests = emptyList(), people = emptyMap(), joinRequests = emptyList())
        }
        if (u.hasComputers()) w = w.copy(computers = u.computers.listList, activeComputerID = u.computers.activeId)
        // Schedules.
        if (u.full) w = w.copy(schedules = u.schedulesList.associateBy { it.id })
        else if (u.schedulesCount > 0 || u.deletedScheduleIdsCount > 0) {
            val m = w.schedules.toMutableMap()
            u.schedulesList.forEach { m[it.id] = it }
            u.deletedScheduleIdsList.forEach { m.remove(it) }
            w = w.copy(schedules = m)
        }
        if (u.peopleCount > 0) w = w.copy(people = w.people + u.peopleList.associateBy { it.userId })
        if (u.hasUsage()) w = w.copy(usage = u.usage.agentsList)
        if (u.hasTheme()) w = w.copy(hostTheme = u.theme)
        if (u.full || u.agentKindsCount > 0) w = w.copy(agentKinds = u.agentKindsList)
        if (u.joinRequestsCount > 0 || u.resolvedJoinIdsCount > 0) {
            val j = w.joinRequests.toMutableList()
            u.joinRequestsList.forEach { r -> if (j.none { it.id == r.id }) j.add(r) }
            j.removeAll { it.id in u.resolvedJoinIdsList }
            w = w.copy(joinRequests = j)
        }
        if (u.groupsCount > 0 || u.deletedGroupIdsCount > 0) {
            val gs = w.groups.toMutableList()
            for (g in u.groupsList) {
                val i = gs.indexOfFirst { it.id == g.id }
                if (i >= 0) gs[i] = g else gs.add(g)
            }
            gs.removeAll { it.id in u.deletedGroupIdsList }
            gs.sortBy { it.order }
            w = w.copy(groups = gs)
        }
        if (u.tabsCount > 0 || u.deletedTabIdsCount > 0) {
            val t = w.tabs.toMutableMap()
            u.tabsList.forEach { t[it.id] = it }
            u.deletedTabIdsList.forEach { t.remove(it) }
            w = w.copy(tabs = t)
        }
        if (u.panesCount > 0 || u.deletedPaneIdsCount > 0) {
            val p = w.panes.toMutableMap()
            for (pane in u.panesList) {
                val old = p[pane.id]
                p[pane.id] = pane
                if (old != null && (old.cols != pane.cols || old.rows != pane.rows)) sinks[pane.id]?.forEach { it.resized(pane.cols, pane.rows) }
            }
            u.deletedPaneIdsList.forEach { p.remove(it) }
            w = w.copy(panes = p)
        }
        if (u.attentionCount > 0 || u.clearedAttentionIdsCount > 0) {
            val a = w.attention.toMutableList()
            for (item in u.attentionList) {
                a.removeAll { it.id == item.id }
                a.add(0, item)
                val k = item.id + item.createdUnixMs
                if (!u.full && k !in knownAttention) onAttention?.invoke(item, w.panes[item.paneId])
                knownAttention.add(k)
            }
            a.removeAll { it.id in u.clearedAttentionIdsList }
            w = w.copy(attention = a)
        }
        if (u.grantsCount > 0 || u.resolvedGrantIdsCount > 0) {
            val g = w.grants.toMutableList()
            u.grantsList.forEach { r -> if (g.none { it.id == r.id }) g.add(0, r) }
            g.removeAll { it.id in u.resolvedGrantIdsList }
            w = w.copy(grants = g)
        }
        if (u.hasHammockKey()) w = w.copy(hammockKey = u.hammockKey)
        if (u.hammockKeyRequestsCount > 0 || u.resolvedHammockKeyIdsCount > 0) {
            val r = w.hammockKeyRequests.toMutableList()
            u.hammockKeyRequestsList.forEach { x -> if (r.none { it.id == x.id }) r.add(x) }
            r.removeAll { it.id in u.resolvedHammockKeyIdsList }
            w = w.copy(hammockKeyRequests = r)
        }
        _workspace.value = w
        val resolved = u.clearedAttentionIdsList + u.resolvedGrantIdsList + u.resolvedJoinIdsList + u.resolvedHammockKeyIdsList
        if (resolved.isNotEmpty()) onResolved?.invoke(resolved)
        if (u.full) onFullState?.invoke(w)
    }

    // MARK: Pane streams

    /**
     * Shows a pane's terminal in `sink`: attaches for each new one (attaching
     * again re-sends the snapshot, which a second view of a pane needs to
     * start from), and again after reconnecting.
     */
    fun attach(paneID: String, sink: PaneSink) = scope.launch {
        detachJobs.remove(paneID)?.cancel()
        val list = sinks.getOrPut(paneID) { ArrayList() }
        if (sink in list) return@launch
        list.add(sink)
        connection?.send { setAttachPane(Hammock.AttachPane.newBuilder().setPaneId(paneID).build()) }
    }

    /** Stops showing it there; the last one off detaches after a while, so coming back is instant. */
    fun detach(paneID: String, sink: PaneSink) = scope.launch {
        val list = sinks[paneID] ?: return@launch
        list.remove(sink)
        if (list.isNotEmpty()) return@launch
        detachJobs[paneID] = scope.launch {
            delay(30_000)
            if (sinks[paneID].isNullOrEmpty()) {
                sinks.remove(paneID)
                desiredSize.remove(paneID)
                connection?.send { setDetachPane(Hammock.DetachPane.newBuilder().setPaneId(paneID).build()) }
            }
        }
    }

    private var cellPixels = 0 to 0

    private fun resizeMsg(paneID: String, cols: Int, rows: Int) = Hammock.Resize.newBuilder().setPaneId(paneID).setCols(cols).setRows(rows)
        .setCellWidthPx(cellPixels.first).setCellHeightPx(cellPixels.second).build()

    /** Asks for the pane's size (this device is driving it). */
    fun resize(paneID: String, cols: Int, rows: Int, cellWidthPx: Int = 0, cellHeightPx: Int = 0) = scope.launch {
        desiredSize[paneID] = cols to rows
        if (cellWidthPx > 0) cellPixels = cellWidthPx to cellHeightPx
        connection?.send { resize = resizeMsg(paneID, cols, rows) }
    }

    fun input(paneID: String, data: ByteArray) {
        if (_workspace.value.groupOfPane(paneID)?.canDrive == false) return
        connection?.send { setInput(Hammock.Input.newBuilder().setPaneId(paneID).setData(ByteString.copyFrom(data)).build()) }
    }

    fun focus(paneID: String) {
        connection?.send { setFocus(Hammock.Focus.newBuilder().setPaneId(paneID).build()) }
    }

    fun reportPresence(visible: List<String>, foreground: Boolean, focused: String = "") {
        connection?.send {
            setPresence(Hammock.Presence.newBuilder().addAllVisiblePaneIds(visible).setForeground(foreground).setFocusedPaneId(focused).build())
        }
    }

    fun setColors(c: Hammock.SetColors) = scope.launch {
        if (c == lastColors) return@launch
        lastColors = c
        connection?.send { setColors = c }
    }

    // MARK: Calls

    /** A request; null (and a toast with why) if it failed. */
    suspend fun request(build: ClientMessage.Builder.() -> Unit): ServerMessage? {
        val c = connection ?: run { showToast("Not connected"); return null }
        return try { c.call(build) } catch (e: Exception) { lastError = e.message; null }
    }

    /** As run, but the error is the caller's to show. */
    suspend fun call(build: ClientMessage.Builder.() -> Unit): ServerMessage {
        val c = connection ?: throw DaemonException("Not connected")
        return c.call(build)
    }

    fun send(build: ClientMessage.Builder.() -> Unit) { connection?.send(build) }

    @Volatile var lastError: String? = null

    fun showToast(text: String) {
        _toast.value = text
        toastJob?.cancel()
        toastJob = scope.launch {
            delay(3000)
            if (_toast.value == text) _toast.value = null
        }
    }

    // MARK: Actions

    fun respond(grant: GrantRequest, decision: Hammock.RespondGrant.Decision) = scope.launch {
        _workspace.value = _workspace.value.let { it.copy(grants = it.grants.filter { g -> g.id != grant.id }) }
        request { setRespondGrant(Hammock.RespondGrant.newBuilder().setId(grant.id).setDecision(decision).build()) }
    }

    fun clearAttention(id: String) = send { setClearAttention(Hammock.ClearAttention.newBuilder().setId(id).build()) }

    /**
     * Needs You's Allow / Deny (or allowSchedule): typed only while the agent still asks, and an Allow only
     * while it asks what the card showed (its prompt id); if it changed, the card comes back as it is now.
     */
    fun decide(item: AttentionItem, decision: Hammock.AgentAnswer.Decision) = scope.launch {
        clearAttention(item.id)
        _workspace.value = _workspace.value.let { it.copy(attention = it.attention.filter { a -> a.id != item.id }) }
        if (_workspace.value.groupOfPane(item.paneId)?.canDrive == false) return@launch
        val name = _workspace.value.panes[item.paneId]?.displayName?.replaceFirstChar { it.uppercase() } ?: "The agent"
        try {
            call {
                setAgentAnswer(Hammock.AgentAnswer.newBuilder().setPaneId(item.paneId).setDecision(decision)
                    .setPromptId(item.promptId).setBound(true).build())
            }
        } catch (e: DaemonException) {
            showToast(if (e.message?.contains("request changed") == true) "The request changed: look again" else "$name isn’t asking any more")
        } catch (e: Exception) {
            showToast(e.message ?: "Couldn't answer")
        }
    }

    fun respondJoin(req: JoinRequest, role: Hammock.Role) = scope.launch {
        _workspace.value = _workspace.value.let { it.copy(joinRequests = it.joinRequests.filter { j -> j.id != req.id }) }
        request { setRespondJoin(Hammock.RespondJoin.newBuilder().setId(req.id).setRole(role).build()) }
    }

    suspend fun createGroup(name: String, computerID: String = ""): String? = request {
        setCreateGroup(Hammock.CreateGroup.newBuilder().setName(name).apply {
            if (computerID.isNotEmpty() && computerID != _workspace.value.myUserID) setComputerId(computerID)
        }.build())
    }?.created?.id

    suspend fun createTab(groupID: String, command: List<String> = emptyList(), cwd: String = ""): String? {
        val id = newTabID()
        val created = request {
            setCreateTab(Hammock.CreateTab.newBuilder().setGroupId(groupID).setTabId(id)
                .setPane(Hammock.PaneSpec.newBuilder().addAllCommand(command).setCwd(cwd)).build())
        }?.created?.id
        if (created == null) showToast("Couldn't open a tab: ${lastError ?: "unknown error"}")
        return created
    }

    suspend fun startAgent(harness: String, cwd: String, groupID: String, prompt: String = "", computerID: String = ""): String? {
        val created = request {
            setStartAgent(Hammock.StartAgent.newBuilder().setHarness(harness).setCwd(cwd).setPrompt(prompt).apply {
                if (groupID.isNotEmpty()) { setGroupId(groupID); setTabId(newTabID()) }
                else if (computerID.isNotEmpty() && computerID != _workspace.value.myUserID) setComputerId(computerID)
            }.build())
        }?.created?.id
        if (created == null) showToast("Couldn't start the agent: ${lastError ?: "unknown error"}")
        return created
    }

    suspend fun listFolders(path: String = "", groupID: String = "", computerID: String = ""): Hammock.FolderList? = request {
        setListFolders(Hammock.ListFolders.newBuilder().setPath(path).setGroupId(groupID).apply {
            if (groupID.isEmpty() && computerID.isNotEmpty() && computerID != _workspace.value.myUserID) setComputerId(computerID)
        }.build())
    }?.folderList

    fun closeTab(id: String) = scope.launch {
        val w = _workspace.value
        val tab = w.tabs[id]
        if (tab != null) {
            _workspace.value = w.copy(
                tabs = w.tabs - id,
                groups = w.groups.map { g -> if (g.id == tab.groupId) g.toBuilder().clearTabIds().addAllTabIds(g.tabIdsList - id).build() else g },
            )
        }
        if (request { setCloseTab(Hammock.CloseTab.newBuilder().setTabId(id).build()) } == null) {
            showToast("Couldn't close the tab: ${lastError ?: "unknown error"}")
            // The next update brings it back; ask for one.
            connection?.let { c -> runCatching { apply(c.call { setSubscribe(Hammock.Subscribe.getDefaultInstance()) }.state) } }
        }
    }

    fun closePane(id: String) = scope.launch { request { setClosePane(Hammock.ClosePane.newBuilder().setPaneId(id).build()) } }
    fun closeGroup(id: String) = scope.launch {
        if (request { setCloseGroup(Hammock.CloseGroup.newBuilder().setGroupId(id).build()) } == null) showToast("Couldn't close the Group: ${lastError ?: "unknown error"}")
    }
    fun renameTab(id: String, name: String) = scope.launch { request { setRenameTab(Hammock.RenameTab.newBuilder().setTabId(id).setName(name).build()) } }
    fun renameGroup(id: String, name: String) = scope.launch { request { setRenameGroup(Hammock.RenameGroup.newBuilder().setGroupId(id).setName(name).build()) } }
    fun setGroupIcon(id: String, icon: String) = scope.launch { request { setSetGroupIcon(Hammock.SetGroupIcon.newBuilder().setGroupId(id).setIcon(icon).build()) } }
    fun moveTab(tabID: String, groupID: String, index: Int) = scope.launch {
        request { setMoveTab(Hammock.MoveTab.newBuilder().setTabId(tabID).setGroupId(groupID).setIndex(index).build()) }
    }
    fun leaveShared(groupID: String) = scope.launch { request { setLeaveShared(Hammock.LeaveShared.newBuilder().setGroupId(groupID).build()) } }

    fun sleepPane(paneID: String, wake: Boolean = false) = scope.launch {
        if (request { setSleepPane(Hammock.SleepPane.newBuilder().setPaneId(paneID).setWake(wake).build()) } == null) {
            val why = lastError ?: "unknown error"
            showToast(if (wake) "Couldn't wake the agent: $why" else why.replaceFirstChar { it.uppercase() })
        }
    }

    fun switchAgent(paneID: String, harness: String) = scope.launch {
        val name = AgentCatalog.shortName(harness, _workspace.value.kinds)
        showToast("Switching to $name…")
        val r = request { setSwitchAgent(Hammock.SwitchAgent.newBuilder().setPaneId(paneID).setTargetHarness(harness).build()) }
        if (r == null) showToast("Couldn't switch to $name: ${lastError ?: "unknown error"}")
        else if (r.agentSwitched.report.isNotEmpty()) showToast(r.agentSwitched.report)
    }

    suspend fun breakOut(paneID: String): String? = request { setBreakOutPane(Hammock.BreakOutPane.newBuilder().setPaneId(paneID).build()) }?.created?.id

    fun interrupt(paneID: String) = input(paneID, byteArrayOf(0x1b))

    fun runSchedule(id: String) = scope.launch {
        if (request { setRunSchedule(Hammock.RunSchedule.newBuilder().setScheduleId(id).build()) } == null) showToast("Couldn't run it: ${lastError ?: "unknown error"}")
    }
    fun setSchedulePaused(id: String, paused: Boolean) = scope.launch {
        request { setSetSchedulePaused(Hammock.SetSchedulePaused.newBuilder().setScheduleId(id).setPaused(paused).build()) }
    }
    fun skipScheduleRun(id: String, skip: Boolean = true) = scope.launch {
        request { setSkipScheduleRun(Hammock.SkipScheduleRun.newBuilder().setScheduleId(id).setSkip(skip).build()) }
    }

    suspend fun listSessions(query: String, limit: Int = 50, computerID: String = ""): Hammock.SessionList? = request {
        setListSessions(Hammock.ListSessions.newBuilder().setQuery(query).setLimit(limit).setComputerId(computerID).build())
    }?.sessionList

    suspend fun resumeSession(s: Hammock.SessionInfo, target: String? = null, groupID: String = "", computerID: String = ""): Hammock.SessionResumed? {
        val r = request {
            setResumeSession(Hammock.ResumeSession.newBuilder().setHarness(s.harness).setId(s.id).setTargetHarness(target ?: "")
                .setGroupId(groupID).setComputerId(computerID).build())
        }
        if (r == null) { showToast("Couldn't resume it: ${lastError ?: "unknown error"}"); return null }
        if (r.sessionResumed.report.isNotEmpty()) showToast(r.sessionResumed.report)
        return r.sessionResumed
    }

    fun registerPush(relayURL: String, relayID: String, key: ByteArray, events: List<String>) = send {
        setRegisterPush(Hammock.RegisterPush.newBuilder().setRelayUrl(relayURL).setRelayId(relayID).setKey(ByteString.copyFrom(key))
            .addAllEvents(events).build())
    }

    private val idAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"
    private fun newTabID() = "tb_" + (1..8).map { idAlphabet.random() }.joinToString("")
}
