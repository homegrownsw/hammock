package com.homegrownsw.hammock.panes

import com.homegrownsw.hammock.client.AppModel
import hammock.v1.Hammock
import hammock.v1.Hammock.FilesCommand
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import org.json.JSONArray
import org.json.JSONObject

/**
 * One files pane as this app shows it (HammockKit's FilesSession): lists
 * folders for its pages, passes on what's done there, and hands every page
 * showing the pane the folders that changed. Shared by everything showing
 * the pane (acquire and release), so one leaving doesn't stop the daemon
 * watching for another. Main thread, as the pages are.
 */
class FilesSession private constructor(val model: AppModel, val paneID: String) {
    /** An error to show (the view clears it). */
    val error = MutableStateFlow("")

    private var refs = 0
    private val pages = LinkedHashSet<PageBridge>()
    /** Where commands go: the page last focused (or ready). */
    private var current: PageBridge? = null
    private var config: String? = null
    private var info: String? = null
    private var stops: List<() -> Unit> = emptyList()
    private val onMain = mainPoster()

    private fun start() {
        stops = listOf(
            model.listen(ServerMessage.MsgCase.FILES_CHANGED) { msg ->
                val c = msg.filesChanged
                if (c.paneId == paneID) onMain { for (p in pages) p.call("changed", JSONArray(c.pathsList)) }
            },
            // Watched again after a reconnect, and what's shown listed again.
            model.onEachConnection {
                onMain {
                    attach()
                    for (p in pages) p.call("relist")
                }
            },
        )
    }

    private fun stop() {
        stops.forEach { it() }
        stops = emptyList()
        model.send { setDetachPane(Hammock.DetachPane.newBuilder().setPaneId(paneID).build()) }
    }

    /** Asks the daemon to watch what the pane shows while it's showing. */
    private fun attach() = model.send { setAttachPane(Hammock.AttachPane.newBuilder().setPaneId(paneID).build()) }

    internal fun attachPage(p: PageBridge) {
        pages += p
        if (p.ready) pageReady(p)
    }

    internal fun detachPage(p: PageBridge) {
        pages -= p
        if (current === p) current = pages.lastOrNull()
    }

    internal fun configure(c: JSONObject) {
        val s = c.toString()
        if (s == config) return
        config = s
        for (p in pages) p.call("configure", c)
    }

    internal fun setInfo(i: JSONObject) {
        val s = i.toString()
        if (s == info) return
        info = s
        for (p in pages) p.call("setInfo", i)
    }

    private fun pageReady(p: PageBridge) {
        current = p
        config?.let { p.call("configure", JSONObject(it)) }
        info?.let { p.call("setInfo", JSONObject(it)) }
    }

    internal fun onPageMessage(p: PageBridge, type: String, m: JSONObject) {
        when (type) {
            "ready" -> if (p in pages) pageReady(p)
            "focus" -> current = p
            "list" -> list(p, m.optString("path", ""))
            // A name being typed, and dragging, are the page's own on a phone; open, menu and trash are
            // the view's (FilesPane).
            "editing", "drag" -> {}
            else -> FilesJson.command(paneID, type, m)?.let(::send)
        }
    }

    /** A folder's entries, for the page that asked. */
    private fun list(p: PageBridge, path: String) {
        if (model.connection == null) return p.call("listing", FilesJson.listingError(path, "Not connected to Hammock"))
        model.scope.launch {
            val j = try {
                FilesJson.listing(model.call { setListDir(Hammock.ListDir.newBuilder().setPaneId(paneID).setPath(path).build()) }.dirListing)
            } catch (e: Exception) {
                FilesJson.listingError(path, e.message ?: "Couldn't list it")
            }
            onMain { p.call("listing", j) }
        }
    }

    /** One folder's entries, for the app's own picker (Move to…); null if it couldn't. */
    suspend fun listDir(path: String): Hammock.DirListing? =
        runCatching { model.call { setListDir(Hammock.ListDir.newBuilder().setPaneId(paneID).setPath(path).build()) }.dirListing }.getOrNull()

    /** Changes what the pane shows, or its files. */
    fun send(c: FilesCommand) {
        if (model.connection == null) {
            error.value = "Not connected to Hammock"
            return
        }
        model.scope.launch {
            try {
                model.call { setFilesCommand(c) }
            } catch (e: Exception) {
                onMain { error.value = e.message ?: "Couldn't do that" }
            }
        }
    }

    private fun build(action: FilesCommand.Action, b: FilesCommand.Builder.() -> Unit = {}) =
        FilesCommand.newBuilder().setPaneId(paneID).setAction(action).apply(b).build()

    fun setRoot(path: String) = send(build(FilesCommand.Action.ACTION_SET_ROOT) { setPath(path) })
    fun setHidden(on: Boolean) = send(build(FilesCommand.Action.ACTION_SHOW_HIDDEN) { setOn(on) })
    fun trash(paths: List<String>) = send(FilesJson.trash(paneID, paths))
    fun move(paths: List<String>, into: String) = send(FilesJson.move(paneID, paths, into))
    fun duplicate(paths: List<String>) = FilesJson.duplicate(paneID, paths).forEach(::send)

    /** One of the page's commands (files.js `command`): rename, newFile, newFolder, collapseAll… */
    fun command(name: String) { current?.call("command", name) }

    companion object {
        private val sessions = HashMap<Pair<AppModel, String>, FilesSession>()

        /** The pane's session, watched by the daemon from the first acquire to the last release. Main thread. */
        fun acquire(model: AppModel, paneID: String): FilesSession {
            val s = sessions.getOrPut(model to paneID) { FilesSession(model, paneID) }
            s.refs++
            if (s.refs == 1) s.start()
            return s
        }

        fun release(s: FilesSession) {
            s.refs--
            if (s.refs > 0) return
            sessions.remove(s.model to s.paneID)
            s.stop()
        }
    }
}
