package com.homegrownsw.hammock.panes

import hammock.v1.Hammock
import hammock.v1.Hammock.DiffContent
import hammock.v1.Hammock.DiffFile
import hammock.v1.Hammock.DiffInfo
import hammock.v1.Hammock.DiffSource
import hammock.v1.Hammock.EditorInfo
import hammock.v1.Hammock.EditorUpdate
import hammock.v1.Hammock.GitMark
import hammock.v1.Hammock.Review
import hammock.v1.Hammock.ReviewCommand
import hammock.v1.Hammock.ReviewComment
import hammock.v1.Hammock.ReviewFinish
import hammock.v1.Hammock.ReviewThread
import org.json.JSONArray
import org.json.JSONObject
import java.net.URI
import java.util.Base64

// What the pages are given and what they send, as the Mac's EditorConfig,
// EditorBridge and DiffSession shape it (EditorWebView.swift,
// DiffWebView.swift): the pages are the same, so the JSON must be too.
// Pure functions, so they're tested on the JVM.

/** The platform the pages are told they're on: the iPhone's layout (touch, one column, the preview's phone padding). */
internal const val PAGE_PLATFORM = "ios"

private fun palette(t: PaneTheme) = JSONArray(t.palette.map(::hex))

private fun themeJSON(t: PaneTheme) = JSONObject()
    .put("background", hex(t.background)).put("foreground", hex(t.foreground)).put("cursor", hex(t.cursor))
    .put("selection", hex(t.selection)).put("palette", palette(t)).put("dark", t.dark)

object EditorJson {
    /** hammock.configure: the theme, font and settings. */
    fun config(theme: PaneTheme, s: EditorSettings, readOnly: Boolean): JSONObject = JSONObject()
        .put("theme", themeJSON(theme))
        .put("font", JSONObject().put("family", fontStack(s.fontFamily)).put("size", s.fontSize))
        .put("wrap", s.wrap).put("lineNumbers", s.lineNumbers).put("tabWidth", s.tabWidth).put("indent", s.indent)
        .put("vim", s.vim).put("readOnly", readOnly).put("platform", PAGE_PLATFORM)

    /** hammock.setInfo: the file's name (for its language) and path, a chosen language, indentation, how to show it. */
    fun info(e: EditorInfo, readOnly: Boolean, view: EditorInfo.View): JSONObject = JSONObject()
        .put("name", e.name).put("language", e.language).put("tabWidth", e.tabWidth).put("indent", e.indent)
        .put("readOnly", readOnly || e.readOnly).put("path", e.path).put("view", viewName(view))

    fun viewName(v: EditorInfo.View): String = when (v) {
        EditorInfo.View.VIEW_PREVIEW -> "preview"
        EditorInfo.View.VIEW_SPLIT -> "split"
        else -> "source"
    }

    fun view(name: String): EditorInfo.View = when (name) {
        "preview" -> EditorInfo.View.VIEW_PREVIEW
        "split" -> EditorInfo.View.VIEW_SPLIT
        else -> EditorInfo.View.VIEW_SOURCE
    }

    /** hammock.load: the whole buffer. */
    fun load(u: EditorUpdate, git: List<GitMark>, discardLocal: Boolean): JSONObject {
        val m = JSONObject().put("text", u.text).put("version", u.version).put("git", marks(git)).put("discardLocal", discardLocal)
        if (u.hasSelection()) m.put("selection", selection(u))
        return m
    }

    /** hammock.receive: updates the daemon took (others' and ours). */
    fun receive(u: EditorUpdate): JSONObject {
        val ups = JSONArray()
        for (c in u.updatesList) ups.put(JSONObject().put("changes", c.changes).put("clientID", c.clientId))
        val m = JSONObject().put("version", u.version).put("updates", ups)
        if (carriesGit(u)) m.put("git", marks(u.gitList))
        if (u.hasSelection()) m.put("selection", selection(u))
        return m
    }

    /** Whether an update says what git's marks are (else they're as they were). */
    fun carriesGit(u: EditorUpdate): Boolean = u.gitKnown || u.gitCount > 0 || (u.updatesCount == 0 && !u.hasSelection())

    private fun selection(u: EditorUpdate) = JSONObject().put("anchor", u.selection.anchor).put("head", u.selection.head)

    fun marks(git: List<GitMark>): JSONArray = JSONArray().also { a ->
        for (m in git) {
            val kind = when (m.kind) {
                GitMark.Kind.KIND_ADDED -> "added"
                GitMark.Kind.KIND_DELETED -> "deleted"
                else -> "modified"
            }
            a.put(JSONObject().put("kind", kind).put("from", m.from).put("to", m.to))
        }
    }

    /** The page's "push": its updates, at the version they were made at. */
    fun pushUpdates(m: JSONObject): List<Hammock.EditorChange> {
        val a = m.optJSONArray("updates") ?: return emptyList()
        return (0 until a.length()).mapNotNull { i ->
            val u = a.optJSONObject(i) ?: return@mapNotNull null
            Hammock.EditorChange.newBuilder().setChanges(u.optString("changes", "[]")).setClientId(u.optString("clientID", "")).build()
        }
    }
}

/**
 * A CodeMirror ChangeSet (its JSON: a number keeps that many UTF-16 units,
 * an array deletes its first element's worth and inserts the lines after
 * it, joined by newlines) applied to text; null if it doesn't fit. Kotlin's
 * strings are UTF-16, as CodeMirror's offsets are.
 */
object ChangeSets {
    fun apply(json: String, text: String): String? {
        val parts = try { JSONArray(json) } catch (e: Exception) { return null }
        val out = StringBuilder(text.length)
        var at = 0
        for (i in 0 until parts.length()) {
            when (val part = parts.get(i)) {
                is Number -> {
                    val keep = part.toInt()
                    if (keep < 0 || at + keep > text.length) return null
                    out.append(text, at, at + keep)
                    at += keep
                }
                is JSONArray -> {
                    val delete = (part.opt(0) as? Number)?.toInt() ?: return null
                    if (delete < 0 || at + delete > text.length) return null
                    at += delete
                    val lines = (1 until part.length()).mapNotNull { part.opt(it) as? String }
                    out.append(lines.joinToString("\n"))
                }
                else -> return null
            }
        }
        if (at != text.length) return null
        return out.toString()
    }
}

/**
 * Images in Markdown's preview: the page asks for hammock-file://local/PATH
 * (PATH absolute), which the WebView may not fetch itself, so a shim in the
 * page points them at the page's own origin under /hammock-file/local/PATH
 * instead. Either way, the path to ask the daemon for, or null if it's not
 * one to ask for (relative, or climbing out with "..").
 */
object PreviewImages {
    const val SCHEME = "hammock-file"
    const val ORIGIN_PREFIX = "/hammock-file/local"

    fun path(url: String): String? {
        val u = try { URI(url) } catch (e: Exception) { return null }
        val raw = when {
            u.scheme == SCHEME && u.host == "local" -> u.path
            u.scheme == "https" && u.host == PageAssets.HOST && (u.path ?: "").startsWith("$ORIGIN_PREFIX/") -> u.path.removePrefix(ORIGIN_PREFIX)
            else -> return null
        } ?: return null
        if (!raw.startsWith("/") || raw.split("/").contains("..")) return null
        return raw
    }
}

object DiffJson {
    /** hammock.configure for the diff page. */
    fun config(
        theme: PaneTheme, s: DiffSettings, view: Hammock.DiffView, canDrive: Boolean, target: DiffAgent?, agents: List<DiffAgent>,
        canSend: Boolean, keyboard: Boolean,
    ): JSONObject {
        val ws = view.whitespace.ifEmpty { s.whitespace }
        val wrap = if (view.wrap.isEmpty()) s.wrap else view.wrap == "on"
        return JSONObject()
            .put("theme", themeJSON(theme))
            .put("font", JSONObject().put("family", fontStack(s.fontFamily)).put("size", s.fontSize))
            // One column on a phone, whatever the pane's choice: split needs width.
            .put("view", JSONObject().put("style", "unified").put("whitespace", if (ws == "ignore") "ignore" else "show").put("wrap", wrap))
            .put("overridden", !(view.style.isEmpty() && view.whitespace.isEmpty() && view.wrap.isEmpty()))
            .put("canDrive", canDrive).put("platform", PAGE_PLATFORM)
            .put("target", target?.json() ?: JSONObject.NULL)
            .put("agents", JSONArray(agents.map { it.json() }))
            .put("canSend", canSend).put("accent", hex(theme.accent)).put("keyboard", keyboard).put("editorName", "")
    }

    fun info(d: DiffInfo): JSONObject = JSONObject()
        .put("title", d.title).put("stale", d.stale).put("error", d.error).put("generation", d.generation)
        .put("files", d.files).put("added", d.added).put("removed", d.removed).put("branch", d.branch).put("repo", d.repo)
        .put("source", source(d.source))

    fun source(s: DiffSource): JSONObject {
        val kind = when (s.kind) {
            DiffSource.Kind.KIND_BRANCH -> "branch"
            DiffSource.Kind.KIND_COMMIT -> "commit"
            DiffSource.Kind.KIND_RANGE -> "range"
            else -> "uncommitted"
        }
        return JSONObject().put("kind", kind).put("base", s.base).put("rev", s.rev)
            .put("withUncommitted", s.withUncommitted).put("mergeBase", s.mergeBase)
    }

    fun source(j: JSONObject): DiffSource = DiffSource.newBuilder()
        .setKind(
            when (j.optString("kind")) {
                "branch" -> DiffSource.Kind.KIND_BRANCH
                "commit" -> DiffSource.Kind.KIND_COMMIT
                "range" -> DiffSource.Kind.KIND_RANGE
                else -> DiffSource.Kind.KIND_UNCOMMITTED
            },
        )
        .setBase(j.optString("base", "")).setRev(j.optString("rev", ""))
        .setWithUncommitted(j.optBoolean("withUncommitted", false)).setMergeBase(j.optBoolean("mergeBase", false))
        .build()

    /** The page's "setView": the pane's own choices (null: back to the settings'). */
    fun view(j: JSONObject?): Hammock.DiffView {
        if (j == null) return Hammock.DiffView.getDefaultInstance()
        val b = Hammock.DiffView.newBuilder().setStyle(j.optString("style", "")).setWhitespace(j.optString("whitespace", ""))
        if (j.has("wrap") && !j.isNull("wrap")) b.wrap = if (j.optBoolean("wrap")) "on" else "off"
        return b.build()
    }

    /** hammock.load: the diff's files, recent commits and branches. */
    fun content(c: DiffContent): JSONObject {
        val files = JSONArray()
        for (f in c.filesList) {
            val status = when (f.status) {
                DiffFile.Status.STATUS_ADDED -> "added"
                DiffFile.Status.STATUS_DELETED -> "deleted"
                DiffFile.Status.STATUS_RENAMED -> "renamed"
                DiffFile.Status.STATUS_COPIED -> "copied"
                DiffFile.Status.STATUS_TYPE_CHANGED -> "typeChanged"
                else -> "modified"
            }
            files.put(
                JSONObject().put("path", f.path).put("oldPath", f.oldPath).put("status", status).put("added", f.added)
                    .put("removed", f.removed).put("binary", f.binary).put("generated", f.generated).put("untracked", f.untracked),
            )
        }
        val commits = JSONArray()
        for (k in c.commitsList) {
            commits.put(JSONObject().put("sha", k.sha).put("subject", k.subject).put("author", k.author).put("unix", k.unix))
        }
        return JSONObject().put("generation", c.generation).put("files", files).put("commits", commits)
            .put("branches", JSONArray(c.branchesList)).put("defaultBranch", c.defaultBranch)
    }

    /** hammock.fileText: a file's two sides (images' bytes as base64). */
    fun fileText(t: Hammock.DiffFileText, generation: Long): JSONObject {
        val j = JSONObject().put("path", t.path).put("generation", generation).put("oldText", t.oldText).put("newText", t.newText)
            .put("mime", t.mime).put("oldSize", t.oldSize).put("newSize", t.newSize).put("tooLarge", t.tooLarge)
        if (!t.oldData.isEmpty) j.put("oldData", Base64.getEncoder().encodeToString(t.oldData.toByteArray()))
        if (!t.newData.isEmpty) j.put("newData", Base64.getEncoder().encodeToString(t.newData.toByteArray()))
        return j
    }

    /** hammock.setReview: the comment threads and the files marked viewed. */
    fun review(r: Review): JSONObject {
        val threads = JSONArray()
        for (t in r.threadsList) {
            val state = when (t.state) {
                ReviewThread.State.STATE_ADDRESSED -> "addressed"
                ReviewThread.State.STATE_RESOLVED -> "resolved"
                else -> "open"
            }
            val comments = JSONArray()
            for (c in t.commentsList) {
                val delivery = when (c.delivery) {
                    ReviewComment.Delivery.DELIVERY_UNSENT -> "unsent"
                    ReviewComment.Delivery.DELIVERY_QUEUED -> "queued"
                    ReviewComment.Delivery.DELIVERY_SENT -> "sent"
                    else -> "none"
                }
                comments.put(
                    JSONObject().put("id", c.id).put("author", c.author).put("agent", c.agent).put("text", c.text)
                        .put("unixMs", c.unixMs).put("delivery", delivery).put("unread", c.unread),
                )
            }
            threads.put(
                JSONObject().put("id", t.id).put("path", t.path).put("side", if (t.side == ReviewThread.Side.SIDE_OLD) "old" else "new")
                    .put("line", t.line).put("endLine", maxOf(t.endLine, t.line)).put("state", state).put("outdated", t.outdated)
                    .put("quote", t.quote).put("comments", comments),
            )
        }
        return JSONObject().put("id", r.id).put("threads", threads).put("viewed", JSONArray(r.viewedList.map { it.path }))
    }

    /** A review command from the page (add a thread, reply, resolve, send…), for the pane. */
    fun reviewCommand(paneID: String, c: JSONObject): ReviewCommand {
        val b = ReviewCommand.newBuilder().setPaneId(paneID)
        b.action = when (c.optString("action")) {
            "addThread" -> ReviewCommand.Action.ACTION_ADD_THREAD
            "reply" -> ReviewCommand.Action.ACTION_REPLY
            "setState" -> ReviewCommand.Action.ACTION_SET_STATE
            "send" -> ReviewCommand.Action.ACTION_SEND
            "deleteComment" -> ReviewCommand.Action.ACTION_DELETE_COMMENT
            "setTarget" -> ReviewCommand.Action.ACTION_SET_TARGET
            "markRead" -> ReviewCommand.Action.ACTION_MARK_READ
            "editComment" -> ReviewCommand.Action.ACTION_EDIT_COMMENT
            "setViewed" -> ReviewCommand.Action.ACTION_SET_VIEWED
            "finish" -> ReviewCommand.Action.ACTION_FINISH
            else -> ReviewCommand.Action.ACTION_UNSPECIFIED
        }
        b.threadId = c.optString("threadId", "")
        b.commentId = c.optString("commentId", "")
        b.path = c.optString("path", "")
        b.side = if (c.optString("side") == "old") ReviewThread.Side.SIDE_OLD else ReviewThread.Side.SIDE_NEW
        b.line = c.optInt("line", 0)
        b.endLine = c.optInt("endLine", 0)
        b.text = c.optString("text", "")
        b.send = c.optBoolean("send", false)
        b.state = when (c.optString("state")) {
            "addressed" -> ReviewThread.State.STATE_ADDRESSED
            "resolved" -> ReviewThread.State.STATE_RESOLVED
            else -> ReviewThread.State.STATE_OPEN
        }
        c.optJSONArray("commentIds")?.let { a -> for (i in 0 until a.length()) a.optString(i)?.let(b::addCommentIds) }
        b.targetPaneId = c.optString("targetPaneId", "")
        b.quote = c.optString("quote", "")
        b.before = c.optString("before", "")
        b.after = c.optString("after", "")
        b.viewed = c.optBoolean("viewed", false)
        b.verdict = when (c.optString("verdict")) {
            "approve" -> ReviewFinish.Verdict.VERDICT_APPROVE
            "requestChanges" -> ReviewFinish.Verdict.VERDICT_REQUEST_CHANGES
            else -> ReviewFinish.Verdict.VERDICT_COMMENT
        }
        return b.build()
    }
}

/** An agent review comments can go to. */
data class DiffAgent(val id: String, val name: String, val detail: String) {
    fun json(): JSONObject = JSONObject().put("id", id).put("name", name).put("detail", detail)
}
