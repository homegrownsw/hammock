package com.homegrownsw.hammock.agentview

import androidx.compose.runtime.Immutable
import androidx.compose.runtime.Stable
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.ui.graphics.ImageBitmap
import com.google.protobuf.ByteString
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.AgentBlock
import hammock.v1.Hammock.AgentLink
import hammock.v1.Hammock.AgentPrompt
import hammock.v1.Hammock.AgentShell
import hammock.v1.Hammock.AgentSubagent
import hammock.v1.Hammock.AgentTask
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Deferred
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.atomic.AtomicLong

/** Everything the agent view shows of a session, as one immutable snapshot. */
@Immutable
data class AgentViewState(
    val blocks: List<AgentBlock> = emptyList(),
    /** The blocks as rows (tool calls grouped), kept so a view doesn't regroup them each time it draws. */
    val items: List<AgentItem> = emptyList(),
    val hasMore: Boolean = false,
    val loadingOlder: Boolean = false,
    /** The daemon has sent the conversation (or said there isn't one): no blocks now is an empty chat, not one loading. */
    val heard: Boolean = false,
    val prompt: AgentPrompt = AgentPrompt.getDefaultInstance(),
    val working: Boolean = false,
    /** What the agent says it's doing. */
    val status: String = "",
    /** What's typed in the agent's input box in the terminal. */
    val draft: String = "",
    /** A "!" command's output so far, while it runs (from the screen: the transcript has it once it's done). */
    val bangOutput: String = "",
    val commands: List<String> = emptyList(),
    /** Those of [commands] that only run at the start of a message (built-ins). */
    val startCommands: Set<String> = emptySet(),
    /** The model of the latest reply (e.g. claude-opus-5-5), its effort level, and how full its context is. */
    val model: String = "",
    val effort: String = "",
    val contextTokens: Long = 0,
    val contextWindow: Long = 0,
    val error: String = "",
    val subagents: List<AgentSubagent> = emptyList(),
    val shells: List<AgentShell> = emptyList(),
    /** A shell's view: its output, the last of it. */
    val shellOutput: String = "",
    val tasks: List<AgentTask> = emptyList(),
    val links: List<AgentLink> = emptyList(),
    /** Messages sent from here that the transcript hasn't shown yet. */
    val pendingSends: List<PendingSend> = emptyList(),
    /** An answer being typed, and the error if it didn't match the screen (shown on the prompt card only). */
    val answering: Boolean = false,
    val answerError: String? = null,
    /** Why the last message from the composer didn't go (shown by the composer, with Retry): cleared by sending again, or editing it. */
    val sendError: String? = null,
)

/**
 * One pane's agent view: the conversation the daemon parses from the agent's
 * session file, the prompt on its screen, and the actions that answer it. A
 * subagent's transcript or a background shell's output when [subagentID] or
 * [shellID] is set (read only).
 *
 * [start] watches (again after every reconnect) and [stop] stops; they count,
 * so the app and the view can both hold it. Updates arrive on the model's
 * thread and replace [state] atomically; actions may be called from anywhere.
 */
@Stable
class AgentSession(
    val model: AppModel,
    val paneID: String,
    val subagentID: String = "",
    val shellID: String = "",
    /** How long it keeps watching after the last [stop], so coming back shows it at once. */
    private val lingerMs: Long = 60_000,
) {
    companion object {
        /** Sessions by model, then by pane (and subagent or shell): one watch each, as the daemon keeps one per connection. */
        private val sessions = java.util.WeakHashMap<AppModel, HashMap<String, AgentSession>>()

        private fun key(paneID: String, subagentID: String, shellID: String) = "$paneID/$subagentID/$shellID"

        /**
         * The pane's session: the one still showing it, or lingering, if there
         * is one, so coming back reuses its watch (a second session would
         * watch again for nothing, then its linger unwatch the pane).
         */
        fun of(model: AppModel, paneID: String, subagentID: String = "", shellID: String = ""): AgentSession = synchronized(sessions) {
            sessions.getOrPut(model) { HashMap() }.getOrPut(key(paneID, subagentID, shellID)) { AgentSession(model, paneID, subagentID, shellID) }
        }

        internal fun forget(s: AgentSession) = synchronized(sessions) {
            val m = sessions[s.model] ?: return@synchronized
            if (m[key(s.paneID, s.subagentID, s.shellID)] === s) m.remove(key(s.paneID, s.subagentID, s.shellID))
        }
    }

    private val _state = MutableStateFlow(AgentViewState())
    val state: StateFlow<AgentViewState> = _state.asStateFlow()

    /** When the agent's replies arrived while watched (not loaded with the conversation), by block id: those are revealed as they appear. */
    val arrived: MutableMap<String, Long> = ConcurrentHashMap()

    /** Rows opened (tool calls, groups, thinking, notes), kept here so a row scrolled away and back stays open. */
    val opened = mutableStateMapOf<String, Boolean>()

    fun isOpen(id: String) = opened[id] == true
    fun setOpen(id: String, open: Boolean) { if (open) opened[id] = true else opened.remove(id) }

    /** When the message box's text last changed by typing: a prompt card that takes its place waits a second after it before taking the keys. */
    @Volatile var composerEditedAtMs = 0L

    /** The prompt card on screen had the keyboard (it or one of its fields). */
    @Volatile var promptHadKeys = false

    /** When a prompt last ended (one shown, then none), for the message box to take back the keyboard it had. */
    @Volatile var promptEndedAtMs = 0L

    /** Who's in the pane, as people say it: "Claude", "Codex". */
    val agentName: String get() {
        val h = harness
        return AgentCatalog.shortName(h, model.workspace.value.kinds)
    }

    /** claude, codex…: the pane's agent (claude when it's not known yet). */
    val harness: String get() = model.workspace.value.panes[paneID]?.agent?.harness?.ifEmpty { null } ?: "claude"

    /** A message starting with "!" runs as a shell command in this agent. */
    val hasShellMode: Boolean get() = model.workspace.value.kinds.firstOrNull { it.id == harness }?.shellMode ?: false

    // MARK: Watching

    private val lock = Any()
    private var refs = 0
    private var stopListening: (() -> Unit)? = null
    private var stopRewatch: (() -> Unit)? = null
    private var lingerJob: Job? = null

    private fun watchMsg(watch: Boolean, before: String = "") = Hammock.WatchAgent.newBuilder().setPaneId(paneID)
        .setSubagentId(subagentID).setShellId(shellID).setWatch(watch).setBeforeBlockId(before).build()

    /** Starts watching (if it isn't already), and again after every reconnect. */
    fun start() = synchronized(lock) {
        refs++
        lingerJob?.cancel()
        lingerJob = null
        if (stopListening != null) return@synchronized
        stopListening = model.listen(ServerMessage.MsgCase.AGENT_VIEW) { msg ->
            val v = msg.agentView
            if (v.paneId == paneID && v.subagentId == subagentID && v.shellId == shellID) apply(v)
        }
        stopRewatch = model.onEachConnection { c -> c.send { setWatchAgent(watchMsg(true)) } }
    }

    /** Stops watching once nothing holds it, after a while (coming back meanwhile is instant). */
    fun stop() = synchronized(lock) {
        if (refs == 0) return@synchronized
        refs--
        if (refs > 0) return@synchronized
        lingerJob?.cancel()
        lingerJob = model.scope.launch {
            if (lingerMs > 0) delay(lingerMs)
            synchronized(lock) {
                if (refs > 0) return@launch
                forget(this@AgentSession)
                stopListening?.invoke()
                stopRewatch?.invoke()
                stopListening = null
                stopRewatch = null
                lingerJob = null
            }
            model.send { setWatchAgent(watchMsg(false)) }
        }
    }

    // MARK: Updates

    /** Your messages already in the transcript when the pending ones were sent. */
    private var seenUserIDs = HashSet<String>()
    private val nextPendingID = AtomicLong(1)

    /** Applies an update from the daemon, as the iPhone's AgentSession.apply does. */
    internal fun apply(v: Hammock.AgentView, nowMs: Long = System.currentTimeMillis()) = synchronized(lock) {
        val s = _state.value
        var bs = s.blocks
        var commands = s.commands
        var startCommands = s.startCommands
        var loadingOlder = s.loadingOlder
        // Rows already shown above a new snapshot's are kept (coming back,
        // or reconnecting, sends the latest turns again).
        var keptAbove = false
        if (v.replace) {
            bs = v.blocksList
            val first = v.blocksList.firstOrNull()
            if (first != null) {
                val k = s.blocks.indexOfFirst { it.id == first.id }
                if (k > 0) {
                    bs = s.blocks.subList(0, k) + v.blocksList
                    keptAbove = true
                }
            }
            commands = v.commandsList
            startCommands = v.startCommandsList.toSet()
        } else if (v.older) {
            bs = v.blocksList + bs
            loadingOlder = false
        } else if (v.blocksCount > 0) {
            val list = ArrayList(bs)
            val index = HashMap<String, Int>(list.size * 2)
            list.forEachIndexed { i, b -> index[b.id] = i }
            for (b in v.blocksList) {
                val i = index[b.id]
                if (i != null) list[i] = b
                else {
                    index[b.id] = list.size
                    list.add(b)
                    if (b.kind == AgentBlock.Kind.KIND_TEXT) arrived[b.id] = nowMs
                }
            }
            bs = list
        }
        var items = s.items
        if (bs != s.blocks) {
            val grouped = AgentItem.group(bs)
            if (grouped != items) items = grouped
        } else {
            bs = s.blocks
        }
        var n = s.copy(blocks = bs, items = items, commands = commands, startCommands = startCommands, loadingOlder = loadingOlder)
        if ((v.replace && !keptAbove) || v.older) n = n.copy(hasMore = v.hasMore)
        if (v.replace) n = n.copy(heard = true)
        if (v.replace || v.subagentsCount > 0) n = n.copy(subagents = v.subagentsList)
        if (v.replace || v.shellsCount > 0) n = n.copy(shells = v.shellsList)
        if (v.replace || v.hasTasks()) n = n.copy(tasks = v.tasks.tasksList)
        if (v.replace || v.hasLinks()) n = n.copy(links = v.links.linksList)
        if (shellID.isNotEmpty()) {
            n = n.copy(shellOutput = ShellOutput.append(if (v.replace) "" else s.shellOutput, v.shellOutput))
        }
        if (!v.older) {
            if (s.prompt.kind != AgentPrompt.Kind.KIND_NONE && v.prompt.kind == AgentPrompt.Kind.KIND_NONE) promptEndedAtMs = nowMs
            n = n.copy(
                model = v.model, effort = v.effort, contextTokens = v.contextTokens, contextWindow = v.contextWindow,
                prompt = v.prompt, working = v.working, status = v.status, error = v.error, bangOutput = v.bangOutput,
                // What's typed in its input box in the terminal (the composer
                // offers it; sending replaces it).
                draft = if (subagentID.isEmpty()) v.draft else n.draft,
                // A new prompt (or none): the last one's answer error isn't its.
                answerError = if (v.prompt.promptId != s.prompt.promptId) null else n.answerError,
            )
        }
        // A sent message shows as pending until a new message of yours
        // appears in the transcript. Not by exact text: one queued while the
        // agent works arrives reworded (spacing, image references).
        if (n.pendingSends.isNotEmpty()) {
            if (v.older) {
                n.blocks.filter { it.sentByYou() }.forEach { seenUserIDs.add(it.id) } // history, not new
            } else {
                val pending = n.pendingSends.toMutableList()
                for (b in n.blocks) {
                    if (pending.isEmpty()) break
                    if (!b.sentByYou() || !seenUserIDs.add(b.id)) continue
                    // A "!" command is recorded as the Bash run it is, not a message.
                    val t = if (b.kind == AgentBlock.Kind.KIND_USER) squashed(b.text) else "!" + squashed(b.input)
                    val i = pending.indexOfFirst { val p = squashed(it.text); t.contains(p) || p.contains(t) }
                    if (i >= 0) pending.removeAt(i) else pending.removeAt(0)
                }
                if (pending.size != n.pendingSends.size) n = n.copy(pendingSends = pending)
            }
            if (s.working && !n.working && !v.older) dropUntaken(n.pendingSends.map { it.id }.toSet())
        }
        // Only a real change is a new value (StateFlow drops equal ones too,
        // but equality of a big snapshot isn't free while the status ticks).
        if (n != s) _state.value = n
    }

    /**
     * Done working, with messages it never took (a "!" command stopped in
     * the terminal, messages Esc took back into its input box): they aren't
     * coming. A moment later, in case one's on its way still.
     */
    private fun dropUntaken(ids: Set<Long>) {
        model.scope.launch {
            delay(3_000)
            synchronized(lock) {
                _state.update { s -> if (s.working) s else s.copy(pendingSends = s.pendingSends.filter { it.id !in ids }) }
            }
        }
    }

    private fun squashed(s: String) = s.split(Regex("\\s+")).filter { it.isNotEmpty() }.joinToString(" ")

    // MARK: Actions

    /** Pages in older turns. */
    fun loadEarlier() {
        val first: String
        synchronized(lock) {
            val s = _state.value
            if (!s.hasMore || s.loadingOlder || s.blocks.isEmpty()) return
            first = s.blocks.first().id
            _state.value = s.copy(loadingOlder = true)
        }
        if (model.connection == null) {
            _state.update { it.copy(loadingOlder = false) }
            return
        }
        model.send { setWatchAgent(watchMsg(true, before = first)) }
        // No page back (the link dropped, the daemon refused): Earlier Messages works again.
        model.scope.launch {
            delay(10_000)
            _state.update { if (it.loadingOlder && it.blocks.firstOrNull()?.id == first) it.copy(loadingOlder = false) else it }
        }
    }

    /**
     * Sends a message (with pictures), queued by the agent if it's working.
     * False if it didn't go: the caller gives the text back. [pending] shows
     * it in the conversation until the transcript does (not for /model and
     * the like).
     */
    suspend fun send(text: String, attachments: List<OutgoingAttachment> = emptyList(), pending: Boolean = true): Boolean {
        _state.update { it.copy(sendError = null) }
        if (model.connection == null) {
            _state.update { it.copy(sendError = "Not connected. Your message wasn't sent.") }
            return false
        }
        val p = PendingSend(nextPendingID.getAndIncrement(), text.replace("￼", "").trim(), attachments)
        if (pending && (p.text.isNotEmpty() || p.attachments.isNotEmpty())) addPending(p)
        return try {
            model.call {
                setAgentSend(
                    Hammock.AgentSend.newBuilder().setPaneId(paneID).setText(text)
                        .addAllAttachments(attachments.map { Hammock.AgentAttachment.newBuilder().setName(it.name).setData(ByteString.copyFrom(it.data)).build() })
                        .build(),
                )
            }
            true
        } catch (e: Exception) {
            synchronized(lock) { _state.update { s -> s.copy(pendingSends = s.pendingSends.filter { it.id != p.id }, sendError = e.message ?: "Couldn't send it") } }
            false
        }
    }

    internal fun addPending(p: PendingSend) = synchronized(lock) {
        val s = _state.value
        if (s.pendingSends.isEmpty()) {
            seenUserIDs = s.blocks.filter { it.sentByYou() }.mapTo(HashSet()) { it.id }
        }
        _state.value = s.copy(pendingSends = s.pendingSends + p)
    }

    /** Messages sent while it works can be sent now: Claude Code holds them until its turn ends, and its Ctrl+Enter sends them at once. */
    val canSendNow: Boolean get() = subagentID.isEmpty() && shellID.isEmpty() && harness == "claude"

    /** Sends the messages Claude Code is holding now, not when its turn ends (Ctrl+Enter in its terminal). */
    fun sendNow() {
        model.scope.launch {
            try {
                model.call { setAgentSend(Hammock.AgentSend.newBuilder().setPaneId(paneID).setSendNow(true).build()) }
            } catch (e: Exception) {
                _state.update { it.copy(sendError = e.message ?: "Couldn't send it now") }
            }
        }
    }

    /** Switches the agent's model (Claude's /model, with an alias such as "opus" or "sonnet[1m]"). */
    fun switchModel(alias: String) = runCommand("/model $alias")

    /**
     * Runs one of the agent's commands for a control (the model menu, the
     * context gauge's Compact and Clear), not as a message: if it can't go, a
     * toast says so, and the composer's failed-send notice (whose Retry would
     * send what's typed there) is left alone.
     */
    fun runCommand(command: String) {
        model.scope.launch {
            if (model.connection == null) { model.showToast("Not connected: $command didn't run."); return@launch }
            try {
                model.call { setAgentSend(Hammock.AgentSend.newBuilder().setPaneId(paneID).setText(command).build()) }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                model.showToast("$command didn't run: ${e.message ?: "couldn't reach the agent"}")
            }
        }
    }

    /** Sets Claude's effort level (/effort, "low" to "max"): it shows from the next reply. */
    fun switchEffort(level: String) = runCommand("/effort $level")

    /** Picks option n (1-based); with text, types it into that ("other") option; with notes, sends them with the pick. */
    suspend fun answer(option: Int, text: String = "", notes: String = "") =
        answer { setOption(option).setText(text).setNotes(notes) }

    /** Multi-select: tick exactly these options (1-based), and the "other" option with text typed into it if there is any, then move on. */
    suspend fun answer(ticking: List<Int>, text: String = "") = answer { addAllToggles(ticking); setText(text) }

    private suspend fun answer(build: Hammock.AgentAnswer.Builder.() -> Unit) {
        val seen = _state.value.prompt.optionsList
        val promptID = _state.value.prompt.promptId // only into the prompt shown
        _state.update { it.copy(answering = true, answerError = null) }
        try {
            val a = Hammock.AgentAnswer.newBuilder().setPaneId(paneID).addAllOptionsSeen(seen).setPromptId(promptID).apply(build).build()
            model.call { setAgentAnswer(a) }
        } catch (e: Exception) {
            _state.update { it.copy(answerError = e.message ?: "Couldn't answer") }
        } finally {
            _state.update { it.copy(answering = false) }
        }
    }

    fun clearAnswerError() = _state.update { it.copy(answerError = null) }

    fun clearSendError() = _state.update { it.copy(sendError = null) }

    /** Files for an "@" mention typed in the composer: the pane's Group's, best match first, each named as the agent would. */
    suspend fun mentionFiles(query: String, limit: Int = 8): List<String> = try {
        val l = model.call { setListFiles(Hammock.ListFiles.newBuilder().setPaneId(paneID).setQuery(query).setLimit(limit)) }.fileList
        val cwd = model.workspace.value.panes[paneID]?.cwd ?: ""
        l.pathsList.map { SlashToken.mentionPath(it, l.folder, cwd) }
    } catch (e: kotlinx.coroutines.CancellationException) {
        throw e
    } catch (e: Exception) {
        emptyList()
    }

    /** Stops the agent (Esc). */
    fun interrupt() = model.interrupt(paneID)

    /** Types keys into the pane (the question form's ← and →). */
    fun keys(s: String) = model.input(paneID, s.toByteArray())

    // MARK: Files

    private val files = ConcurrentHashMap<String, ByteArray>()
    private val fetching = ConcurrentHashMap<String, Deferred<ByteArray?>>()

    /** Pictures decoded, so a row made again (scrolled back to) shows its picture at once, at its size. */
    private val decoded = object : LinkedHashMap<String, ImageBitmap>(64, 0.75f, true) {
        override fun removeEldestEntry(eldest: MutableMap.MutableEntry<String, ImageBitmap>?) = size > 120
    }

    fun decodedImage(ref: String): ImageBitmap? = synchronized(decoded) { decoded[ref] }
    internal fun keepDecoded(ref: String, image: ImageBitmap) = synchronized(decoded) { decoded[ref] = image }

    /** An image or a referenced file, fetched once (callers asking meanwhile share the fetch) and cached. */
    suspend fun file(ref: String): ByteArray? {
        files[ref]?.let { return it }
        val mine = CompletableDeferred<ByteArray?>()
        val running = fetching.putIfAbsent(ref, mine)
        if (running != null) return running.await()
        val data = try {
            model.call { setReadAgentFile(Hammock.ReadAgentFile.newBuilder().setPaneId(paneID).setRef(ref).build()) }.agentFile.data.toByteArray()
        } catch (e: Exception) {
            null
        }
        if (data != null) files[ref] = data
        fetching.remove(ref)
        mine.complete(data)
        return data
    }
}
