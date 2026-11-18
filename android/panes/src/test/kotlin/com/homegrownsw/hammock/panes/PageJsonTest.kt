package com.homegrownsw.hammock.panes

import com.google.protobuf.ByteString
import hammock.v1.Hammock
import hammock.v1.Hammock.DiffSource
import hammock.v1.Hammock.EditorInfo
import hammock.v1.Hammock.EditorUpdate
import hammock.v1.Hammock.GitMark
import hammock.v1.Hammock.ReviewCommand
import hammock.v1.Hammock.ReviewThread
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class PageJsonTest {
    private val theme = PaneTheme(
        background = 0xFF1E1E1E.toInt(), foreground = 0xFFD4D4D4.toInt(), cursor = 0xFFFFFFFF.toInt(), selection = 0xFF264F78.toInt(),
        palette = List(16) { 0xFF000000.toInt() or (it * 0x111111) }, dark = true, accent = 0xFF61AFEF.toInt(),
    )

    @Test fun editorConfig() {
        val c = EditorJson.config(theme, EditorSettings(), readOnly = true)
        val t = c.getJSONObject("theme")
        assertEquals("#1e1e1e", t.getString("background"))
        assertEquals("#264f78", t.getString("selection"))
        assertEquals(16, t.getJSONArray("palette").length())
        assertEquals("#111111", t.getJSONArray("palette").getString(1))
        assertTrue(t.getBoolean("dark"))
        assertEquals("monospace", c.getJSONObject("font").getString("family"))
        assertEquals(13.0, c.getJSONObject("font").getDouble("size"), 0.0)
        assertTrue(c.getBoolean("readOnly"))
        assertTrue(c.getBoolean("lineNumbers"))
        assertFalse(c.getBoolean("wrap"))
        assertEquals("spaces", c.getString("indent"))
        assertEquals("ios", c.getString("platform"))
    }

    @Test fun editorInfo() {
        val e = EditorInfo.newBuilder().setName("notes.md").setPath("/Users/me/notes.md").setTabWidth(2).setIndent("tabs")
            .setView(EditorInfo.View.VIEW_SPLIT).build()
        val i = EditorJson.info(e, readOnly = false, view = EditorInfo.View.VIEW_PREVIEW)
        assertEquals("notes.md", i.getString("name"))
        assertEquals(2, i.getInt("tabWidth"))
        assertEquals("tabs", i.getString("indent"))
        assertEquals("preview", i.getString("view")) // the view shown here, not the pane's
        assertFalse(i.getBoolean("readOnly"))
        // A file that can't be written is read-only whoever you are.
        assertTrue(EditorJson.info(e.toBuilder().setReadOnly(true).build(), false, e.view).getBoolean("readOnly"))
        assertEquals(EditorInfo.View.VIEW_SPLIT, EditorJson.view("split"))
        assertEquals(EditorInfo.View.VIEW_SOURCE, EditorJson.view("anything"))
    }

    @Test fun editorLoadAndReceive() {
        val git = listOf(GitMark.newBuilder().setKind(GitMark.Kind.KIND_ADDED).setFrom(2).setTo(3).build())
        val snap = EditorUpdate.newBuilder().setPaneId("p").setSnapshot(true).setText("hi\n").setVersion(7)
            .setSelection(Hammock.EditorSelection.newBuilder().setAnchor(1).setHead(2)).build()
        val l = EditorJson.load(snap, git, discardLocal = true)
        assertEquals("hi\n", l.getString("text"))
        assertEquals(7L, l.getLong("version"))
        assertTrue(l.getBoolean("discardLocal"))
        assertEquals(2, l.getJSONObject("selection").getInt("head"))
        val m = l.getJSONArray("git").getJSONObject(0)
        assertEquals("added", m.getString("kind"))
        assertEquals(2, m.getInt("from"))

        val u = EditorUpdate.newBuilder().setVersion(8)
            .addUpdates(Hammock.EditorChange.newBuilder().setChanges("[3,[0,\"!\"]]").setClientId("abc")).build()
        val r = EditorJson.receive(u)
        assertEquals(8L, r.getLong("version"))
        assertEquals("abc", r.getJSONArray("updates").getJSONObject(0).getString("clientID"))
        assertEquals("[3,[0,\"!\"]]", r.getJSONArray("updates").getJSONObject(0).getString("changes"))
        // Updates without git's marks leave the page's as they are.
        assertFalse(r.has("git"))
        assertFalse(r.has("selection"))
        // An update with nothing else is a change to git's marks (to none).
        assertTrue(EditorJson.receive(EditorUpdate.newBuilder().setVersion(8).build()).has("git"))
        assertTrue(EditorJson.receive(u.toBuilder().setGitKnown(true).build()).has("git"))
    }

    @Test fun editorPushFromThePage() {
        val m = JSONObject("""{"type":"push","version":4,"updates":[{"changes":"[1,[0,\"x\"]]","clientID":"c1"},{"clientID":"c1"}]}""")
        val ups = EditorJson.pushUpdates(m)
        assertEquals(2, ups.size)
        assertEquals("[1,[0,\"x\"]]", ups[0].changes)
        assertEquals("[]", ups[1].changes)
        assertEquals("c1", ups[1].clientId)
    }

    @Test fun previewImagePaths() {
        assertEquals("/Users/me/img/a b.png", PreviewImages.path("hammock-file://local/Users/me/img/a%20b.png"))
        assertEquals("/Users/me/x#1.png", PreviewImages.path("https://appassets.androidplatform.net/hammock-file/local/Users/me/x%231.png"))
        assertNull(PreviewImages.path("hammock-file://local/Users/../etc/passwd"))
        assertNull(PreviewImages.path("hammock-file://elsewhere/a.png"))
        assertNull(PreviewImages.path("https://example.com/hammock-file/local/a.png"))
        assertNull(PreviewImages.path("https://appassets.androidplatform.net/hammock/editor.html"))
    }

    @Test fun shimGoesFirstInTheHead() {
        val html = PageAssets.withShim("<!doctype html>\n<html>\n<head>\n<meta charset=\"utf-8\"><script>page()</script></head></html>")
        val shim = html.indexOf("window.webkit")
        assertTrue(shim > html.indexOf("<head>"))
        assertTrue(shim < html.indexOf("page()"))
        assertTrue(html.contains("https://appassets.androidplatform.net/hammock-file/local"))
    }

    @Test fun diffConfigIsOneColumn() {
        val view = Hammock.DiffView.newBuilder().setStyle("split").setWrap("on").build()
        val agent = DiffAgent("p1", "Claude", "fix tests")
        val c = DiffJson.config(theme, DiffSettings(), view, canDrive = true, target = agent, agents = listOf(agent), canSend = false, keyboard = false)
        val v = c.getJSONObject("view")
        assertEquals("unified", v.getString("style"))
        assertEquals("show", v.getString("whitespace"))
        assertTrue(v.getBoolean("wrap"))
        assertTrue(c.getBoolean("overridden"))
        assertEquals("p1", c.getJSONObject("target").getString("id"))
        assertEquals("fix tests", c.getJSONArray("agents").getJSONObject(0).getString("detail"))
        assertFalse(c.getBoolean("canSend"))
        assertEquals("#61afef", c.getString("accent"))
        assertEquals("ios", c.getString("platform"))
        val none = DiffJson.config(theme, DiffSettings(whitespace = "ignore"), Hammock.DiffView.getDefaultInstance(), false, null, emptyList(), true, true)
        assertTrue(none.isNull("target"))
        assertFalse(none.getBoolean("overridden"))
        assertEquals("ignore", none.getJSONObject("view").getString("whitespace"))
    }

    @Test fun diffContentAndFiles() {
        val c = Hammock.DiffContent.newBuilder().setGeneration(3).setDefaultBranch("main").addBranches("main").addBranches("dev")
            .addFiles(Hammock.DiffFile.newBuilder().setPath("b.go").setOldPath("a.go").setStatus(Hammock.DiffFile.Status.STATUS_RENAMED).setAdded(4).setRemoved(1))
            .addFiles(Hammock.DiffFile.newBuilder().setPath("go.sum").setGenerated(true))
            .addCommits(Hammock.DiffCommit.newBuilder().setSha("1a2b").setSubject("Fix").setAuthor("Sam").setUnix(1_700_000_000))
            .build()
        val j = DiffJson.content(c)
        assertEquals(3L, j.getLong("generation"))
        val f = j.getJSONArray("files")
        assertEquals("renamed", f.getJSONObject(0).getString("status"))
        assertEquals("a.go", f.getJSONObject(0).getString("oldPath"))
        assertEquals("modified", f.getJSONObject(1).getString("status"))
        assertTrue(f.getJSONObject(1).getBoolean("generated"))
        assertEquals(1_700_000_000L, j.getJSONArray("commits").getJSONObject(0).getLong("unix"))
        assertEquals(JSONArray(listOf("main", "dev")).toString(), j.getJSONArray("branches").toString())

        val t = Hammock.DiffFileText.newBuilder().setPath("logo.png").setMime("image/png").setNewData(ByteString.copyFrom(byteArrayOf(1, 2, 3))).build()
        val ft = DiffJson.fileText(t, 3)
        assertEquals("AQID", ft.getString("newData"))
        assertFalse(ft.has("oldData"))
        assertEquals(3L, ft.getLong("generation"))
    }

    @Test fun diffSourceRoundTrip() {
        val s = DiffSource.newBuilder().setKind(DiffSource.Kind.KIND_RANGE).setBase("main").setRev("HEAD").setMergeBase(true).build()
        assertEquals(s, DiffJson.source(DiffJson.source(s)))
        assertEquals(DiffSource.Kind.KIND_UNCOMMITTED, DiffJson.source(JSONObject("{}")).kind)
        assertEquals("off", DiffJson.view(JSONObject("""{"style":"split","wrap":false}""")).wrap)
        assertEquals("", DiffJson.view(JSONObject("""{"style":"split"}""")).wrap)
        assertEquals(Hammock.DiffView.getDefaultInstance(), DiffJson.view(null))
    }

    @Test fun review() {
        val r = Hammock.Review.newBuilder().setId("r1")
            .addThreads(
                ReviewThread.newBuilder().setId("t1").setPath("a.go").setSide(ReviewThread.Side.SIDE_OLD).setLine(5)
                    .setState(ReviewThread.State.STATE_ADDRESSED)
                    .addComments(
                        Hammock.ReviewComment.newBuilder().setId("c1").setText("why?").setUnixMs(42)
                            .setDelivery(Hammock.ReviewComment.Delivery.DELIVERY_UNSENT),
                    ),
            )
            .addViewed(Hammock.ReviewViewed.newBuilder().setPath("b.go"))
            .build()
        val j = DiffJson.review(r)
        val t = j.getJSONArray("threads").getJSONObject(0)
        assertEquals("old", t.getString("side"))
        assertEquals(5, t.getInt("endLine")) // endLine is at least line
        assertEquals("addressed", t.getString("state"))
        assertEquals("unsent", t.getJSONArray("comments").getJSONObject(0).getString("delivery"))
        assertEquals("b.go", j.getJSONArray("viewed").getString(0))
    }

    @Test fun reviewCommandFromThePage() {
        val c = DiffJson.reviewCommand(
            "pane1",
            JSONObject("""{"action":"addThread","path":"a.go","side":"old","line":3,"endLine":4,"text":"hmm","send":true,"quote":"x","before":"b","after":"a"}"""),
        )
        assertEquals("pane1", c.paneId)
        assertEquals(ReviewCommand.Action.ACTION_ADD_THREAD, c.action)
        assertEquals(ReviewThread.Side.SIDE_OLD, c.side)
        assertEquals(4, c.endLine)
        assertTrue(c.send)
        assertEquals("b", c.before)
        val read = DiffJson.reviewCommand("p", JSONObject("""{"action":"markRead","commentIds":["c1","c2"]}"""))
        assertEquals(listOf("c1", "c2"), read.commentIdsList)
        val fin = DiffJson.reviewCommand("p", JSONObject("""{"action":"finish","verdict":"requestChanges","text":"no"}"""))
        assertEquals(Hammock.ReviewFinish.Verdict.VERDICT_REQUEST_CHANGES, fin.verdict)
        val state = DiffJson.reviewCommand("p", JSONObject("""{"action":"setState","threadId":"t","state":"resolved"}"""))
        assertEquals(ReviewThread.State.STATE_RESOLVED, state.state)
        assertEquals(ReviewCommand.Action.ACTION_UNSPECIFIED, DiffJson.reviewCommand("p", JSONObject("""{"action":"nope"}""")).action)
    }
}
