package com.homegrownsw.hammock.panes

import android.net.Uri
import android.os.Handler
import android.os.Looper
import androidx.compose.runtime.Immutable
import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.EditorCommand
import hammock.v1.Hammock.EditorInfo
import hammock.v1.Hammock.EditorUpdate
import hammock.v1.Hammock.GitMark
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONArray
import org.json.JSONObject

/** Runs a block on the main thread (where the WebView and the sessions' state live). */
internal fun mainPoster(): (() -> Unit) -> Unit {
    val h = Handler(Looper.getMainLooper())
    return { block -> if (Looper.myLooper() == Looper.getMainLooper()) block() else h.post(block) }
}

/** What an editor pane shows of its session, besides the page itself. */
@Immutable
data class EditorUiState(
    /** Where the cursor is (1-based), and how much is selected. */
    val line: Int = 1,
    val column: Int = 1,
    val selected: Int = 0,
    /** The language highlighting it ("" plain text), as the page found it. */
    val languageName: String = "",
    val languageNames: List<String> = emptyList(),
    val loaded: Boolean = false,
    /** The buffer against the file on disk is showing. */
    val showingDiff: Boolean = false,
    /** What the page really shows: Split falls back to the preview on a narrow screen, and to the source for a file it can't render. */
    val pageShown: EditorInfo.View = EditorInfo.View.VIEW_SOURCE,
    /** This device's own view, for someone who can't change the pane's (a View member). */
    val localView: EditorInfo.View? = null,
) {
    /** The preview alone: no source to go to a line in, or type into. */
    val previewOnly: Boolean get() = pageShown == EditorInfo.View.VIEW_PREVIEW
    val canPreview: Boolean get() = languageName == "Markdown"
    fun shownView(info: EditorInfo): EditorInfo.View = localView ?: info.view
}

/**
 * One editor pane as this app shows it: the go-between for the daemon, which
 * keeps the buffer, and the pane's CodeMirror page, which does the editing
 * and the collab rebasing (as the iPhone's EditorSession). Its state and the
 * page are the main thread's; daemon messages are moved there.
 */
class EditorSession internal constructor(
    val model: AppModel,
    val paneID: String,
    private val onMain: (() -> Unit) -> Unit,
) {
    constructor(model: AppModel, paneID: String) : this(model, paneID, mainPoster())

    private val _state = MutableStateFlow(EditorUiState())
    val state: StateFlow<EditorUiState> = _state.asStateFlow()

    /** Errors to show (a push the daemon refused, a command that failed); the view clears it. */
    val error = MutableStateFlow("")

    /** The buffer's text as last heard (null until a snapshot, or after an update it couldn't follow). */
    var text: String? = null
        private set

    private var page: PageBridge? = null
    private var snapshot: EditorUpdate? = null
    private val since = ArrayList<EditorUpdate>()
    private var lastGit: List<GitMark> = emptyList()
    private var discardLocal = false
    // Attached and waiting for its snapshot: a page loading meanwhile needn't ask again.
    private var awaitingSnapshot = false
    private var config: String? = null
    private var info: String? = null
    private var stops: List<() -> Unit> = emptyList()

    /** The page asked to save (saveAs: Save As), and a link in the preview to another file (an absolute path). */
    internal var onSave: ((saveAs: Boolean) -> Unit)? = null
    internal var onOpenFile: ((String) -> Unit)? = null

    // MARK: Watching

    fun start() {
        if (stops.isNotEmpty()) return
        stops = listOf(
            model.listen(ServerMessage.MsgCase.EDITOR_UPDATE) { msg ->
                val u = msg.editorUpdate
                if (u.paneId == paneID) onMain { apply(u) }
            },
            // Now (if connected) and after every reconnect: the whole buffer again.
            model.onEachConnection { onMain { resync() } },
        )
    }

    fun stop() {
        stops.forEach { it() }
        stops = emptyList()
        model.send { setDetachPane(Hammock.DetachPane.newBuilder().setPaneId(paneID).build()) }
    }

    private fun attach() {
        awaitingSnapshot = true
        model.send { setAttachPane(Hammock.AttachPane.newBuilder().setPaneId(paneID).build()) }
    }

    /** Out of step with the daemon: its whole buffer again. */
    internal fun resync(discard: Boolean = false) {
        if (discard) discardLocal = true
        snapshot = null
        since.clear()
        attach()
    }

    internal fun apply(u: EditorUpdate) {
        if (u.gitKnown || u.gitCount > 0 || (u.updatesCount == 0 && !u.snapshot && !u.hasSelection())) lastGit = u.gitList
        if (u.snapshot) {
            text = u.text
        } else if (u.updatesCount > 0) {
            text = u.updatesList.fold(text) { t, c -> t?.let { ChangeSets.apply(c.changes, it) } }
        }
        if (u.snapshot) {
            snapshot = u
            awaitingSnapshot = false
            since.clear()
            _state.update { it.copy(loaded = true) }
            page?.call("load", EditorJson.load(u, lastGit, discardLocal))
            discardLocal = false
            return
        }
        if (snapshot != null && u.updatesCount > 0) since.add(u)
        // Not too many kept: past that, a page loading late asks again.
        if (since.size > 2000) {
            since.clear()
            snapshot = null
        }
        page?.call("receive", EditorJson.receive(u))
    }

    // MARK: The page

    internal fun attachPage(p: PageBridge) {
        page = p
        // A page that was showing before (the view came back) loads again.
        if (p.ready) pageReady(p)
    }

    internal fun detachPage(p: PageBridge) {
        if (page === p) page = null
    }

    /** hammock.configure's settings: sent when they change, and to a page as it loads. */
    internal fun configure(c: JSONObject) {
        val s = c.toString()
        if (s == config) return
        config = s
        page?.call("configure", c)
    }

    internal fun setInfo(i: JSONObject) {
        val s = i.toString()
        if (s == info) return
        info = s
        page?.call("setInfo", i)
    }

    private fun pageReady(p: PageBridge) {
        config?.let { p.call("configure", JSONObject(it)) }
        info?.let { p.call("setInfo", JSONObject(it)) }
        val s = snapshot
        if (s == null) {
            if (!awaitingSnapshot) attach() // its snapshot loads it
        } else {
            p.call("load", EditorJson.load(s, lastGit, false))
            for (u in since) p.call("receive", EditorJson.receive(u))
        }
        p.ask("languageNames") { r ->
            val names = try { JSONArray(r ?: "[]").let { a -> (0 until a.length()).map { a.getString(it) } } } catch (e: Exception) { emptyList() }
            _state.update { it.copy(languageNames = names) }
        }
    }

    /** A message from the page (main thread). */
    internal fun onPageMessage(p: PageBridge, type: String, m: JSONObject) {
        when (type) {
            "ready" -> if (page === p) pageReady(p)
            "push" -> push(p, m.optLong("version"), EditorJson.pushUpdates(m))
            "select" -> selected(m)
            "language" -> { val n = m.optString("name"); _state.update { it.copy(languageName = n) } }
            "resync" -> resync()
            "save" -> onSave?.invoke(false)
            "saveAs" -> onSave?.invoke(true)
            "shown" -> { val v = EditorJson.view(m.optString("view")); _state.update { it.copy(pageShown = v) } }
            "openFile" -> m.optString("path").takeIf { it.startsWith("/") }?.let { onOpenFile?.invoke(it) }
            "openURL" -> PageBridge.openOutside(p.webView.context, Uri.parse(m.optString("url")))
        }
    }

    private fun push(p: PageBridge, version: Long, updates: List<Hammock.EditorChange>) {
        if (model.connection == null) {
            p.call("pushResult", false)
            return
        }
        model.scope.launch {
            try {
                val r = model.call {
                    setEditorPush(Hammock.EditorPush.newBuilder().setPaneId(paneID).setVersion(version).addAllUpdates(updates).build())
                }
                onMain { p.call("pushResult", r.editorPushReply.accepted) }
            } catch (e: Exception) {
                // Changes the daemon couldn't apply: start again from its buffer.
                onMain {
                    error.value = e.message ?: "Couldn't save your edit"
                    p.call("pushResult", false)
                    resync(discard = true)
                }
            }
        }
    }

    private fun selected(m: JSONObject) {
        _state.update { it.copy(line = m.optInt("line", it.line), column = m.optInt("column", it.column), selected = m.optInt("selected", 0)) }
        if (!m.optBoolean("synced") || !m.has("version")) return
        val sel = Hammock.EditorSelect.newBuilder().setPaneId(paneID).setVersion(m.optLong("version"))
            .setAnchor(m.optInt("anchor")).setHead(m.optInt("head")).build()
        model.send { setEditorSelect(sel) }
    }

    /** An image Markdown's preview shows, from the daemon: blocks (the WebView asks off the main thread). */
    internal fun readImageBlocking(path: String): Pair<String, ByteArray>? = runBlocking {
        withTimeoutOrNull(20_000) {
            try {
                val r = model.call { setReadEditorImage(Hammock.ReadEditorImage.newBuilder().setPaneId(paneID).setPath(path).build()) }
                r.editorImage.mediaType to r.editorImage.data.toByteArray()
            } catch (e: Exception) {
                null
            }
        }
    }

    // MARK: Commands

    /** Saves (as path, if given). Errors are thrown for the caller to show; "already exists" and "changed on disk" ones can be retried with force. */
    suspend fun save(path: String = "", force: Boolean = false) = command { setAction(EditorCommand.Action.ACTION_SAVE).setPath(path).setForce(force) }
    suspend fun reload() = command { setAction(EditorCommand.Action.ACTION_RELOAD) }
    /** Renames the file (a new name, or a path relative to its folder), unsaved edits and all. */
    suspend fun rename(name: String) = command { setAction(EditorCommand.Action.ACTION_RENAME).setPath(name) }
    suspend fun keepMine() = command { setAction(EditorCommand.Action.ACTION_KEEP_MINE) }

    fun setLanguage(name: String) {
        model.scope.launch { runCatching { command { setAction(EditorCommand.Action.ACTION_SET_LANGUAGE).setLanguage(name) } } }
    }

    /** Shows the file as its source, rendered, or both: for everyone on the pane, or (local) only here. */
    fun setView(view: EditorInfo.View, local: Boolean = false) {
        if (local) {
            _state.update { it.copy(localView = view) }
            return
        }
        _state.update { it.copy(localView = null) }
        model.scope.launch { runCatching { command { setAction(EditorCommand.Action.ACTION_SET_VIEW).setView(view) } } }
    }

    private suspend fun command(build: EditorCommand.Builder.() -> Unit) {
        val c = EditorCommand.newBuilder().setPaneId(paneID).apply(build).build()
        model.call { setEditorCommand(c) }
    }

    /** Shows the buffer against the file on disk (each difference can be taken or left). */
    suspend fun showDiff() {
        val r = model.call { setReadEditor(Hammock.ReadEditor.newBuilder().setPaneId(paneID).setDisk(true).build()) }
        val disk = if (r.editorUpdate.diskMissing) "" else r.editorUpdate.diskText
        onMain {
            page?.call("showDiff", disk)
            _state.update { it.copy(showingDiff = true) }
        }
    }

    fun hideDiff() {
        page?.call("hideDiff")
        _state.update { it.copy(showingDiff = false) }
    }

    /**
     * Runs one of the page's commands: undo, redo, find, findNext,
     * findPrevious, gotoLine, selectAll, left/right/up/down, wordLeft,
     * wordRight, lineStart, lineEnd, tab, indent, outdent, focus, blur.
     */
    fun run(name: String) {
        page?.call("command", name)
        if (name == "blur") page?.hideKeyboard()
    }

    fun insert(text: String) { page?.call("insert", text) }
}
