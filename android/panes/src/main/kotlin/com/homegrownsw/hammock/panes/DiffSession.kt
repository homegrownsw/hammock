package com.homegrownsw.hammock.panes

import android.net.Uri
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Workspace
import hammock.v1.Hammock
import hammock.v1.Hammock.DiffCommand
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import org.json.JSONObject

/**
 * One diff pane as this app shows it (as the iPhone's DiffSession): reads
 * the diff from the daemon for its page, passes on what's asked of it there
 * (another source, the pane's view, a refresh, review comments), and hands
 * the page the review as it changes. Main thread, as the page is.
 */
class DiffSession internal constructor(
    val model: AppModel,
    val paneID: String,
    private val onMain: (() -> Unit) -> Unit,
) {
    constructor(model: AppModel, paneID: String) : this(model, paneID, mainPoster())

    /** An error to show (the view clears it). */
    val error = MutableStateFlow("")
    val loading = MutableStateFlow(false)

    private var page: PageBridge? = null
    private var content: JSONObject? = null
    private var generation = 0L
    private var review: JSONObject? = null
    /** Files' two sides already read, for a page that loads again (small ones). */
    private val texts = HashMap<String, JSONObject>()
    private var config: String? = null
    private var info: String? = null
    private var stops: List<() -> Unit> = emptyList()

    internal var onOpenFile: ((path: String, line: Int) -> Unit)? = null

    fun start() {
        if (stops.isNotEmpty()) return
        stops = listOf(
            model.listen(ServerMessage.MsgCase.REVIEW) { msg ->
                val r = msg.review
                if (r.paneId == paneID) onMain { apply(r) }
            },
            // Watched again after a reconnect, and read again if a page shows it.
            model.onEachConnection {
                onMain {
                    attach()
                    if (page?.ready == true) load(refresh = false)
                }
            },
        )
    }

    fun stop() {
        stops.forEach { it() }
        stops = emptyList()
        model.send { setDetachPane(Hammock.DetachPane.newBuilder().setPaneId(paneID).build()) }
    }

    /** Asks the daemon to watch the repository while the pane's showing. */
    private fun attach() = model.send { setAttachPane(Hammock.AttachPane.newBuilder().setPaneId(paneID).build()) }

    internal fun attachPage(p: PageBridge) {
        page = p
        if (p.ready) pageReady(p)
    }

    internal fun detachPage(p: PageBridge) {
        if (page === p) page = null
    }

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
        val c = content
        if (c != null) {
            p.call("load", c)
            review?.let { p.call("setReview", it) }
        } else {
            load(refresh = false)
        }
    }

    private fun apply(r: Hammock.Review) {
        val j = DiffJson.review(r)
        review = j
        page?.call("setReview", j)
    }

    internal fun onPageMessage(p: PageBridge, type: String, m: JSONObject) {
        when (type) {
            "ready" -> if (page === p) pageReady(p)
            "needFile" -> m.optString("path").takeIf { it.isNotEmpty() }?.let { needFile(p, it, m.optLong("generation")) }
            "refresh" -> load(refresh = true)
            "setSource" -> m.optJSONObject("source")?.let { setSource(DiffJson.source(it)) }
            "setView" -> setView(m.optJSONObject("view"))
            "openFile" -> m.optString("path").takeIf { it.isNotEmpty() }?.let { onOpenFile?.invoke(it, m.optInt("line", 0)) }
            "review" -> m.optJSONObject("command")?.let(::reviewCommand)
            "openURL" -> PageBridge.openOutside(p.webView.context, Uri.parse(m.optString("url")))
        }
    }

    /** The diff's files, afresh from the daemon (refresh: worked out again). */
    fun load(refresh: Boolean) {
        if (model.connection == null) {
            error.value = "Not connected to Hammock"
            return
        }
        loading.value = true
        model.scope.launch {
            try {
                val c = model.call { setReadDiff(Hammock.ReadDiff.newBuilder().setPaneId(paneID).setRefresh(refresh).build()) }.diffContent
                val j = DiffJson.content(c)
                onMain {
                    loading.value = false
                    if (c.generation != generation) texts.clear()
                    generation = c.generation
                    content = j
                    error.value = ""
                    page?.call("load", j)
                    readReview()
                }
            } catch (e: Exception) {
                onMain {
                    loading.value = false
                    error.value = e.message ?: "Couldn't read the diff"
                }
            }
        }
    }

    private fun readReview() {
        model.scope.launch {
            val r = runCatching { model.call { setReadReview(Hammock.ReadReview.newBuilder().setPaneId(paneID).build()) } }.getOrNull()
            if (r != null && r.hasReview()) onMain { apply(r.review) }
        }
    }

    /** A page asking for a file's two sides: they go to that page. */
    private fun needFile(p: PageBridge, path: String, gen: Long) {
        texts[path]?.let { t ->
            if (t.optLong("generation") == gen) return p.call("fileText", t)
        }
        if (model.connection == null) return p.call("fileError", path, gen, "Not connected to Hammock")
        model.scope.launch {
            try {
                val t = model.call {
                    setReadDiffFile(Hammock.ReadDiffFile.newBuilder().setPaneId(paneID).setPath(path).setGeneration(gen).build())
                }.diffFileText
                val j = DiffJson.fileText(t, gen)
                val size = t.oldText.length + t.newText.length + t.oldData.size() + t.newData.size()
                onMain {
                    // Kept for the page loading again (small ones: big ones are read again).
                    if (size < 2_000_000) texts[path] = j
                    p.call("fileText", j)
                }
            } catch (e: Exception) {
                onMain { p.call("fileError", path, gen, e.message ?: "Couldn't read it") }
            }
        }
    }

    private fun reviewCommand(c: JSONObject) {
        if (model.connection == null) {
            error.value = "Not connected to Hammock"
            return
        }
        val msg = DiffJson.reviewCommand(paneID, c)
        // Finishing closes the diff, as quitting revdiff does, once it's done.
        val closeAfter = msg.action == Hammock.ReviewCommand.Action.ACTION_FINISH && c.optBoolean("close")
        model.scope.launch {
            try {
                val reply = model.call { setReviewCommand(msg) }.reviewReply
                onMain {
                    if (reply.hasReview()) apply(reply.review)
                    if (reply.queued > 0) error.value = "Held until the agent has your answer: it's waiting on you"
                }
                if (closeAfter) model.closePane(paneID)
            } catch (e: Exception) {
                onMain { error.value = e.message ?: "Couldn't do that" }
                readReview() // what the daemon has, comments saved but not sent included
            }
        }
    }

    /** Compares something else (the page's source menu). */
    fun setSource(s: Hammock.DiffSource) {
        model.scope.launch {
            try {
                command { setAction(DiffCommand.Action.ACTION_SET_SOURCE).setSource(s) }
                onMain { load(refresh = false) }
            } catch (e: Exception) {
                onMain { error.value = e.message ?: "Couldn't compare that" }
            }
        }
    }

    /** The pane's own view (null: back to the settings'). */
    private fun setView(v: JSONObject?) {
        val view = DiffJson.view(v)
        model.scope.launch {
            try {
                command { setAction(DiffCommand.Action.ACTION_SET_VIEW).setView(view) }
            } catch (e: Exception) {
                onMain { error.value = e.message ?: "Couldn't change the view" }
            }
        }
    }

    private suspend fun command(build: DiffCommand.Builder.() -> Unit) {
        val c = DiffCommand.newBuilder().setPaneId(paneID).apply(build).build()
        model.call { setDiffCommand(c) }
    }

    /** Runs one of the page's commands (diff.js `command`): refresh, send, nextUnread… */
    fun command(name: String) { page?.call("command", name) }
    fun showNextUnread() { page?.call("nextUnread") }
    fun reveal(path: String) { page?.call("reveal", path) }

    companion object {
        /**
         * The agents a diff pane's comments can go to: those running in its
         * Group (the pane's target first, even if it's elsewhere).
         */
        fun agents(w: Workspace, paneID: String): Pair<DiffAgent?, List<DiffAgent>> {
            val pane = w.panes[paneID] ?: return null to emptyList()
            if (pane.kind != Hammock.PaneKind.PANE_KIND_DIFF) return null to emptyList()
            val group = w.groupOfPane(paneID)?.id
            fun agent(p: Hammock.Pane): DiffAgent {
                val tab = w.tabs[p.tabId]?.let { w.title(it) } ?: ""
                return DiffAgent(p.id, AgentCatalog.shortName(p.agent.harness, w.kinds), tab)
            }
            val list = w.panes.values.filter { p ->
                p.hasAgent() && p.agent.state != Hammock.AgentState.AGENT_STATE_EXITED && p.id != paneID && w.groupOfPane(p.id)?.id == group
            }.map(::agent).sortedWith(compareBy({ it.detail }, { it.id }))
            val target = w.panes[pane.diff.targetPaneId]?.let(::agent)
            return target to list
        }
    }
}
