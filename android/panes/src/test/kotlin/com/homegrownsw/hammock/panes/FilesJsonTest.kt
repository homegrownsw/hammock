package com.homegrownsw.hammock.panes

import com.homegrownsw.hammock.client.Workspace
import hammock.v1.Hammock
import hammock.v1.Hammock.DirEntry
import hammock.v1.Hammock.DirListing
import hammock.v1.Hammock.FilesCommand.Action
import hammock.v1.Hammock.FilesInfo
import hammock.v1.Hammock.PaneKind
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class FilesJsonTest {
    private val theme = PaneTheme(
        background = 0xFF1E1E1E.toInt(), foreground = 0xFFD4D4D4.toInt(), cursor = 0xFFFFFFFF.toInt(), selection = 0xFF264F78.toInt(),
        palette = List(16) { 0xFF000000.toInt() or (it * 0x111111) }, dark = true, accent = 0xFF61AFEF.toInt(),
    )

    @Test fun config() {
        val c = FilesJson.config(theme, canDrive = false)
        val t = c.getJSONObject("theme")
        assertEquals("#1e1e1e", t.getString("background"))
        assertEquals("#d4d4d4", t.getString("foreground"))
        assertEquals(16, t.getJSONArray("palette").length())
        assertTrue(t.getBoolean("dark"))
        assertEquals(15.0, c.getJSONObject("font").getDouble("size"), 0.0)
        assertFalse(c.getBoolean("canDrive"))
        assertTrue(c.getBoolean("ops"))
        // Touch sizes, and a tap opens: files.js's touch().
        assertEquals("android", c.getString("platform"))
        assertEquals("#61afef", c.getString("accent"))
        assertEquals("", c.getString("home"))
    }

    @Test fun info() {
        val f = FilesInfo.newBuilder().setRoot("/Users/me/app").setFollowGroup(true).addExpanded("/Users/me/app/src").setShowHidden(true)
            .addSelected("/Users/me/app/README.md").setRepo("/Users/me/app").setReveal("/Users/me/app/src").setRevealSeq(3).build()
        val i = FilesJson.info(f)
        assertEquals("/Users/me/app", i.getString("root"))
        assertTrue(i.getBoolean("followGroup"))
        assertEquals("/Users/me/app/src", i.getJSONArray("expanded").getString(0))
        assertTrue(i.getBoolean("showHidden"))
        assertEquals("/Users/me/app/README.md", i.getJSONArray("selected").getString(0))
        assertEquals("", i.getString("error"))
        assertEquals("/Users/me/app", i.getString("repo"))
        assertEquals(3L, i.getLong("revealSeq"))
    }

    @Test fun listing() {
        val l = DirListing.newBuilder().setPath("/Users/me/app").setTruncated(true)
            .addEntries(DirEntry.newBuilder().setName("src").setDir(true).setGit("M"))
            .addEntries(DirEntry.newBuilder().setName("link").setSymlink(true).setSize(12).setMtimeUnixMs(1_700_000_000_000).setIgnored(true))
            .build()
        val j = FilesJson.listing(l)
        assertEquals("/Users/me/app", j.getString("path"))
        assertTrue(j.getBoolean("truncated"))
        val e = j.getJSONArray("entries")
        assertEquals(2, e.length())
        assertEquals("src", e.getJSONObject(0).getString("name"))
        assertTrue(e.getJSONObject(0).getBoolean("dir"))
        assertEquals("M", e.getJSONObject(0).getString("git"))
        assertTrue(e.getJSONObject(1).getBoolean("symlink"))
        assertEquals(12L, e.getJSONObject(1).getLong("size"))
        assertEquals(1_700_000_000_000L, e.getJSONObject(1).getLong("mtime"))
        assertTrue(e.getJSONObject(1).getBoolean("ignored"))

        val bad = FilesJson.listingError("/gone", "Not connected to Hammock")
        assertEquals("/gone", bad.getString("path"))
        assertEquals(0, bad.getJSONArray("entries").length())
        assertEquals("Not connected to Hammock", bad.getString("error"))
    }

    private fun msg(vararg kv: Pair<String, Any>) = JSONObject().apply { for ((k, v) in kv) put(k, v) }

    @Test fun pageMessages() {
        val p = JSONArray(listOf("/a/b", "/a/c"))
        FilesJson.command("p1", "expand", msg("paths" to p))!!.let {
            assertEquals("p1", it.paneId)
            assertEquals(Action.ACTION_EXPAND, it.action)
            assertEquals(listOf("/a/b", "/a/c"), it.pathsList)
        }
        assertEquals(Action.ACTION_COLLAPSE, FilesJson.command("p1", "collapse", msg("paths" to p))!!.action)
        assertEquals(Action.ACTION_SELECT, FilesJson.command("p1", "select", msg("paths" to p))!!.action)
        // "" is the Group's folder.
        FilesJson.command("p1", "setRoot", msg("path" to ""))!!.let { assertEquals(Action.ACTION_SET_ROOT, it.action); assertEquals("", it.path) }
        FilesJson.command("p1", "hidden", msg("on" to true))!!.let { assertEquals(Action.ACTION_SHOW_HIDDEN, it.action); assertTrue(it.on) }
        FilesJson.command("p1", "rename", msg("path" to "/a/b", "name" to "c"))!!.let {
            assertEquals(Action.ACTION_RENAME, it.action); assertEquals("/a/b", it.path); assertEquals("c", it.name)
        }
        FilesJson.command("p1", "create", msg("dir" to "/a", "name" to "new", "folder" to true))!!.let {
            assertEquals(Action.ACTION_CREATE_FOLDER, it.action); assertEquals("/a", it.path); assertEquals("new", it.name)
        }
        assertEquals(Action.ACTION_CREATE_FILE, FilesJson.command("p1", "create", msg("dir" to "/a", "name" to "x.txt"))!!.action)
        FilesJson.command("p1", "move", msg("paths" to p, "dest" to "/d", "copy" to true))!!.let {
            assertEquals(Action.ACTION_COPY, it.action); assertEquals("/d", it.path); assertEquals(2, it.pathsCount)
        }
        assertEquals(Action.ACTION_MOVE, FilesJson.command("p1", "move", msg("paths" to p, "dest" to "/d"))!!.action)
        assertEquals(Action.ACTION_UNDO, FilesJson.command("p1", "undo", msg())!!.action)
        // The view's own (a phone asks before trashing), and ones missing what they need.
        assertNull(FilesJson.command("p1", "trash", msg("paths" to p)))
        assertNull(FilesJson.command("p1", "open", msg("path" to "/a/b")))
        assertNull(FilesJson.command("p1", "rename", msg("path" to "/a/b", "name" to "")))
        assertNull(FilesJson.command("p1", "move", msg("paths" to JSONArray(), "dest" to "/d")))
    }

    @Test fun duplicateCopiesIntoEachFolder() {
        val cs = FilesJson.duplicate("p1", listOf("/a/x", "/b/y", "/a/z", "/top"))
        assertEquals(listOf("/a", "/b", "/"), cs.map { it.path })
        assertTrue(cs.all { it.action == Action.ACTION_COPY })
        assertEquals(listOf("/a/x", "/a/z"), cs[0].pathsList)
    }

    @Test fun whatAFileOpensIn() {
        assertEquals(FileOpener.EDITOR, FilesOpening.opener("/a/main.go"))
        assertEquals(FileOpener.EDITOR, FilesOpening.opener("/a/Makefile"))
        assertEquals(FileOpener.EDITOR, FilesOpening.opener("/a/.gitignore"))
        assertEquals(FileOpener.BROWSER, FilesOpening.opener("/a/index.HTML"))
        assertEquals(FileOpener.BROWSER, FilesOpening.opener("/a/shot.png"))
        assertEquals(FileOpener.BROWSER, FilesOpening.opener("/a/spec.pdf"))
        assertEquals(FileOpener.ELSEWHERE, FilesOpening.opener("/a/app.zip"))
        assertEquals(FileOpener.ELSEWHERE, FilesOpening.opener("/a/clip.mov"))
    }

    @Test fun fileURLs() {
        assertEquals("file:///Users/me/app/index.html", FilesOpening.fileURL("/Users/me/app/index.html"))
        assertEquals("file:///Users/me/my%20site/a%23b%3F.html", FilesOpening.fileURL("/Users/me/my site/a#b?.html"))
        assertEquals("file:///tmp/caf%C3%A9.svg", FilesOpening.fileURL("/tmp/café.svg"))
    }

    private fun workspace(vararg panes: Hammock.Pane): Workspace {
        val split = Hammock.Split.newBuilder().addAllChildren(panes.map { Hammock.Layout.newBuilder().setPaneId(it.id).build() })
        val tab = Hammock.Tab.newBuilder().setId("t").setGroupId("g").setLayout(Hammock.Layout.newBuilder().setSplit(split)).build()
        return Workspace(tabs = mapOf("t" to tab), panes = panes.associateBy { it.id })
    }

    private fun editor(id: String, path: String, dirty: Boolean = false) = Hammock.Pane.newBuilder().setId(id).setTabId("t")
        .setKind(PaneKind.PANE_KIND_EDITOR).setEditor(Hammock.EditorInfo.newBuilder().setPath(path).setDirty(dirty)).build()

    @Test fun whichEditorAFileOpensIn() {
        val files = Hammock.Pane.newBuilder().setId("f").setTabId("t").setKind(PaneKind.PANE_KIND_FILES).build()
        val w = workspace(files, editor("e1", "/a/one.go"), editor("e2", "/a/two.go", dirty = true))
        // Already open in the tab: just shown.
        assertEquals(FilesOpening.EditorTarget.Show("e1"), FilesOpening.editorTarget(w, "f", "/a/one.go", linked = null))
        // The last one it opened, unchanged: reused.
        assertEquals(FilesOpening.EditorTarget.Replace("e1"), FilesOpening.editorTarget(w, "f", "/a/three.go", linked = "e1"))
        // With unsaved edits, gone, or none: a new one.
        assertEquals(FilesOpening.EditorTarget.Split, FilesOpening.editorTarget(w, "f", "/a/three.go", linked = "e2"))
        assertEquals(FilesOpening.EditorTarget.Split, FilesOpening.editorTarget(w, "f", "/a/three.go", linked = "gone"))
        assertEquals(FilesOpening.EditorTarget.Split, FilesOpening.editorTarget(w, "f", "/a/three.go", linked = null))
    }

    @Test fun menuForItems() {
        val info = FilesInfo.newBuilder().setRoot("/a").setFollowGroup(true).build()
        val file = FilesMenu.actions(listOf("/a/main.go"), listOf(false), info, canDrive = true)
        assertEquals(
            listOf(
                FilesAction.OPEN, FilesAction.OPEN_IN_BROWSER, FilesAction.RENAME, FilesAction.NEW_FILE, FilesAction.NEW_FOLDER,
                FilesAction.DUPLICATE, FilesAction.MOVE_TO, FilesAction.COPY_PATH, FilesAction.TRASH,
            ),
            file,
        )
        // A page already opens in a browser.
        assertFalse(FilesAction.OPEN_IN_BROWSER in FilesMenu.actions(listOf("/a/i.html"), listOf(false), info, true))
        val folder = FilesMenu.actions(listOf("/a/src"), listOf(true), info, canDrive = true)
        assertTrue(FilesAction.SHOW_ONLY_FOLDER in folder)
        assertFalse(FilesAction.OPEN in folder)
        val two = FilesMenu.actions(listOf("/a/x", "/a/y"), listOf(false, false), info, canDrive = true)
        assertFalse(FilesAction.RENAME in two)
        assertFalse(FilesAction.OPEN in two)
        assertTrue(FilesAction.COPY_PATHS in two)
        // Viewers change nothing.
        assertEquals(listOf(FilesAction.OPEN, FilesAction.OPEN_IN_BROWSER, FilesAction.COPY_PATH),
            FilesMenu.actions(listOf("/a/main.go"), listOf(false), info, canDrive = false))
    }

    @Test fun menuForTheBackground() {
        val info = FilesInfo.newBuilder().setRoot("/a").setFollowGroup(false).setShowHidden(true).build()
        assertEquals(
            listOf(FilesAction.NEW_FILE, FilesAction.NEW_FOLDER, FilesAction.HIDE_HIDDEN, FilesAction.COLLAPSE_ALL, FilesAction.GROUP_FOLDER),
            FilesMenu.actions(emptyList(), emptyList(), info, canDrive = true),
        )
        assertEquals(
            listOf(FilesAction.SHOW_HIDDEN, FilesAction.COLLAPSE_ALL),
            FilesMenu.actions(emptyList(), emptyList(), FilesInfo.newBuilder().setFollowGroup(true).build(), canDrive = false),
        )
    }

    @Test fun titles() {
        assertEquals("main.go", FilesMenu.title(listOf("/a/main.go")))
        assertEquals("3 items", FilesMenu.title(listOf("/a", "/b", "/c")))
        assertEquals("Move main.go to the Trash?", FilesMenu.trashQuestion(listOf("/a/main.go")))
        assertEquals("Move 2 items to the Trash?", FilesMenu.trashQuestion(listOf("/a/x", "/a/y")))
    }
}
