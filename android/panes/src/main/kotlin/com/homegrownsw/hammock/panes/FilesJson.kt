package com.homegrownsw.hammock.panes

import com.homegrownsw.hammock.client.Workspace
import com.homegrownsw.hammock.client.allPaneIDs
import hammock.v1.Hammock
import hammock.v1.Hammock.DirListing
import hammock.v1.Hammock.FilesCommand
import hammock.v1.Hammock.FilesInfo
import hammock.v1.Hammock.PaneKind
import org.json.JSONArray
import org.json.JSONObject
import java.net.URI

// Files panes' JSON, as HammockKit's FilesConfig and FilesSession shape it
// (FilesWebView.swift): the page is the same, so the JSON must be too. Pure
// functions, so they're tested on the JVM.

object FilesJson {
    /** hammock.configure. The page sizes its rows for touch on "android" and ignores the font size there. */
    fun config(theme: PaneTheme, canDrive: Boolean, fontSize: Double = 15.0): JSONObject = JSONObject()
        .put(
            "theme",
            JSONObject().put("background", hex(theme.background)).put("foreground", hex(theme.foreground))
                .put("palette", JSONArray(theme.palette.map(::hex))).put("dark", theme.dark),
        )
        .put("font", JSONObject().put("size", fontSize))
        .put("canDrive", canDrive).put("ops", true).put("platform", "android").put("accent", hex(theme.accent)).put("home", "")

    /** hammock.setInfo: what the daemon keeps of the pane. */
    fun info(f: FilesInfo): JSONObject = JSONObject()
        .put("root", f.root).put("followGroup", f.followGroup).put("expanded", JSONArray(f.expandedList))
        .put("showHidden", f.showHidden).put("selected", JSONArray(f.selectedList)).put("error", f.error).put("repo", f.repo)
        .put("reveal", f.reveal).put("revealSeq", f.revealSeq)

    /** hammock.listing: one folder's entries. */
    fun listing(l: DirListing): JSONObject {
        val entries = JSONArray()
        for (e in l.entriesList) {
            entries.put(
                JSONObject().put("name", e.name).put("dir", e.dir).put("symlink", e.symlink).put("size", e.size)
                    .put("mtime", e.mtimeUnixMs).put("git", e.git).put("ignored", e.ignored),
            )
        }
        return JSONObject().put("path", l.path).put("error", l.error).put("truncated", l.truncated).put("entries", entries)
    }

    /** A listing that failed: the page shows why in the folder's place. */
    fun listingError(path: String, error: String): JSONObject =
        JSONObject().put("path", path).put("error", error).put("truncated", false).put("entries", JSONArray())

    fun strings(a: JSONArray?): List<String> = if (a == null) emptyList() else (0 until a.length()).mapNotNull { a.optString(it).takeIf(String::isNotEmpty) }
    fun bools(a: JSONArray?): List<Boolean> = if (a == null) emptyList() else (0 until a.length()).map { a.optBoolean(it) }

    /**
     * The FilesCommand a page message asks for, or null for one the app
     * handles itself (open, menu, trash: a phone asks first) or doesn't know.
     */
    fun command(paneID: String, type: String, m: JSONObject): FilesCommand? {
        val b = FilesCommand.newBuilder().setPaneId(paneID)
        val paths = strings(m.optJSONArray("paths"))
        when (type) {
            "expand" -> b.setAction(FilesCommand.Action.ACTION_EXPAND).addAllPaths(paths)
            "collapse" -> b.setAction(FilesCommand.Action.ACTION_COLLAPSE).addAllPaths(paths)
            "select" -> b.setAction(FilesCommand.Action.ACTION_SELECT).addAllPaths(paths)
            "setRoot" -> b.setAction(FilesCommand.Action.ACTION_SET_ROOT).setPath(m.optString("path", ""))
            "hidden" -> b.setAction(FilesCommand.Action.ACTION_SHOW_HIDDEN).setOn(m.optBoolean("on", false))
            "rename" -> {
                val path = m.optString("path", "")
                val name = m.optString("name", "")
                if (path.isEmpty() || name.isEmpty()) return null
                b.setAction(FilesCommand.Action.ACTION_RENAME).setPath(path).setName(name)
            }
            "create" -> {
                val dir = m.optString("dir", "")
                val name = m.optString("name", "")
                if (dir.isEmpty() || name.isEmpty()) return null
                b.setAction(if (m.optBoolean("folder")) FilesCommand.Action.ACTION_CREATE_FOLDER else FilesCommand.Action.ACTION_CREATE_FILE)
                    .setPath(dir).setName(name)
            }
            "move" -> {
                val dest = m.optString("dest", "")
                if (dest.isEmpty() || paths.isEmpty()) return null
                b.setAction(if (m.optBoolean("copy")) FilesCommand.Action.ACTION_COPY else FilesCommand.Action.ACTION_MOVE).setPath(dest).addAllPaths(paths)
            }
            "undo" -> b.setAction(FilesCommand.Action.ACTION_UNDO)
            else -> return null
        }
        return b.build()
    }

    fun trash(paneID: String, paths: List<String>): FilesCommand =
        FilesCommand.newBuilder().setPaneId(paneID).setAction(FilesCommand.Action.ACTION_TRASH).addAllPaths(paths).build()

    fun move(paneID: String, paths: List<String>, into: String, copy: Boolean = false): FilesCommand = FilesCommand.newBuilder().setPaneId(paneID)
        .setAction(if (copy) FilesCommand.Action.ACTION_COPY else FilesCommand.Action.ACTION_MOVE).setPath(into).addAllPaths(paths).build()

    /** Duplicate: each item copied into its own folder (one COPY per folder, in the order they come). */
    fun duplicate(paneID: String, paths: List<String>): List<FilesCommand> =
        paths.groupBy(::parent).map { (folder, ps) -> move(paneID, ps, folder, copy = true) }
}

internal fun base(path: String): String = path.trimEnd('/').substringAfterLast('/').ifEmpty { path }
internal fun parent(path: String): String {
    val i = path.trimEnd('/').lastIndexOf('/')
    return if (i <= 0) "/" else path.substring(0, i)
}

/** What a file opens in on a phone. */
enum class FileOpener { EDITOR, BROWSER, ELSEWHERE }

object FilesOpening {
    /** Shown rendered by a browser pane. */
    val browserKinds = setOf("html", "htm", "xhtml", "svg", "png", "jpg", "jpeg", "gif", "webp", "avif", "bmp", "ico", "pdf")

    /** Not text: an editor can't show them, and a phone has nothing else to. */
    val binaryKinds = setOf(
        "heic", "tif", "tiff", "icns", "psd", "sketch", "mov", "mp4", "m4v", "avi", "mkv", "webm", "mp3", "m4a", "wav", "aiff", "flac",
        "zip", "gz", "tgz", "xz", "bz2", "7z", "rar", "dmg", "pkg", "ipa", "apk", "aab", "o", "a", "dylib", "so", "exe", "dll", "class",
        "jar", "wasm", "woff", "woff2", "ttf", "otf", "sqlite", "db", "key", "pages", "numbers", "docx", "xlsx", "pptx",
    )

    fun extension(path: String): String {
        val name = base(path)
        val i = name.lastIndexOf('.')
        return if (i <= 0) "" else name.substring(i + 1).lowercase()
    }

    fun opener(path: String): FileOpener {
        val ext = extension(path)
        return when (ext) {
            in browserKinds -> FileOpener.BROWSER
            in binaryKinds -> FileOpener.ELSEWHERE
            else -> FileOpener.EDITOR
        }
    }

    /** A path as a browser pane's file:// URL (each part percent-encoded, as Foundation's file URLs are). */
    fun fileURL(path: String): String = "file://" + URI(null, null, path, null).toASCIIString().replace("?", "%3F").replace("#", "%23")

    /** Where a file opened in an editor goes: a pane to just show, one to put it in, or a new one beside. */
    sealed interface EditorTarget {
        data class Show(val paneID: String) : EditorTarget
        data class Replace(val paneID: String) : EditorTarget
        data object Split : EditorTarget
    }

    /**
     * The tab's editor already on path; else the editor this files pane
     * opened last, while it's still in the tab with nothing unsaved; else a
     * new one.
     */
    fun editorTarget(w: Workspace, filesPane: String, path: String, linked: String?): EditorTarget {
        val tab = w.panes[filesPane]?.let { w.tabs[it.tabId] } ?: return EditorTarget.Split
        val inTab = tab.allPaneIDs().mapNotNull { w.panes[it] }
        inTab.firstOrNull { it.kind == PaneKind.PANE_KIND_EDITOR && it.editor.path == path }?.let { return EditorTarget.Show(it.id) }
        val ed = linked?.let { id -> inTab.firstOrNull { it.id == id } }
        if (ed != null && ed.kind == PaneKind.PANE_KIND_EDITOR && !ed.editor.dirty) return EditorTarget.Replace(ed.id)
        return EditorTarget.Split
    }
}

/** What touch and hold offers. */
enum class FilesAction(val label: String) {
    OPEN("Open"),
    OPEN_IN_BROWSER("Open in Browser"),
    SHOW_ONLY_FOLDER("Show Only This Folder"),
    RENAME("Rename"),
    NEW_FILE("New File"),
    NEW_FOLDER("New Folder"),
    DUPLICATE("Duplicate"),
    MOVE_TO("Move to…"),
    COPY_PATH("Copy Path"),
    COPY_PATHS("Copy Paths"),
    TRASH("Move to Trash"),
    SHOW_HIDDEN("Show Hidden Files"),
    HIDE_HIDDEN("Hide Hidden Files"),
    COLLAPSE_ALL("Collapse Folders"),
    GROUP_FOLDER("Back to the Group's Folder"),
}

object FilesMenu {
    /**
     * The actions for what was held: paths (none: the background) and which
     * of them are folders. Those that change files only for someone who can
     * drive the Group.
     */
    fun actions(paths: List<String>, dirs: List<Boolean>, info: FilesInfo, canDrive: Boolean): List<FilesAction> {
        val out = ArrayList<FilesAction>()
        if (paths.isEmpty()) {
            if (canDrive) out += listOf(FilesAction.NEW_FILE, FilesAction.NEW_FOLDER)
            out += if (info.showHidden) FilesAction.HIDE_HIDDEN else FilesAction.SHOW_HIDDEN
            out += FilesAction.COLLAPSE_ALL
            if (!info.followGroup) out += FilesAction.GROUP_FOLDER
            return out
        }
        val isDir = { i: Int -> dirs.getOrElse(i) { false } }
        val files = paths.filterIndexed { i, _ -> !isDir(i) }
        if (files.size == 1 && paths.size == 1) {
            out += FilesAction.OPEN
            // Pages, images and PDFs open in a browser anyway.
            if (FilesOpening.opener(files[0]) != FileOpener.BROWSER) out += FilesAction.OPEN_IN_BROWSER
        }
        if (paths.size == 1 && isDir(0)) out += FilesAction.SHOW_ONLY_FOLDER
        if (canDrive) {
            if (paths.size == 1) out += FilesAction.RENAME
            out += listOf(FilesAction.NEW_FILE, FilesAction.NEW_FOLDER, FilesAction.DUPLICATE, FilesAction.MOVE_TO)
        }
        out += if (paths.size == 1) FilesAction.COPY_PATH else FilesAction.COPY_PATHS
        if (canDrive) out += FilesAction.TRASH
        return out
    }

    /** The menu's title: the item's name, or how many. */
    fun title(paths: List<String>): String = when (paths.size) {
        0 -> ""
        1 -> base(paths[0])
        else -> "${paths.size} items"
    }

    /** Trashing asks first. */
    fun trashQuestion(paths: List<String>): String =
        if (paths.size == 1) "Move ${base(paths[0])} to the Trash?" else "Move ${paths.size} items to the Trash?"
}
