package com.homegrownsw.hammock

import org.json.JSONObject
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** A Live Activity's content as the computer sends it (testdata/client/vectors.json's "live", made by internal/daemon/live.go). */
class LiveContentTest {
    private fun vectors(): org.json.JSONArray {
        var dir = File("").absoluteFile
        while (!File(dir, "testdata/client/vectors.json").exists()) dir = dir.parentFile
        return JSONObject(File(dir, "testdata/client/vectors.json").readText()).getJSONArray("live")
    }

    @Test fun readsTheComputersContent() {
        val list = vectors()
        assertTrue(list.length() > 0)
        for (i in 0 until list.length()) {
            val o = list.getJSONObject(i)
            val c = assertNotNull(LiveContent.from(o)).let { LiveContent.from(o)!! }
            assertEquals(o.getString("agent"), c.agent)
            assertEquals(o.getString("state"), c.state)
            assertEquals(o.getString("status"), c.status)
            assertEquals(o.optString("doing"), c.doing)
            assertEquals(o.optString("place"), c.place)
            assertEquals(o.optString("more"), c.more)
            assertEquals(o.getLong("since"), c.since)
            assertEquals(if (o.has("ctx")) o.getInt("ctx") else null, c.ctx)
            assertEquals(o.optString("prompt"), c.prompt)
            assertEquals(o.optString("item"), c.item)
            assertEquals(o.getInt("others"), c.others)
            assertEquals(o.getInt("waiting"), c.waiting)
            assertEquals(o.getString("state") == "needs_you" && o.has("prompt"), c.answerable)
            assertEquals(o.getString("state") == "finished", c.over)
        }
    }

    @Test fun whatItSays() {
        val working = LiveContent.from(vectors().getJSONObject(0))!!
        assertEquals("🗺 Atlas · Map Judder · +2 more working", working.subText)
        assertEquals("Claude +2", working.chip)
        val waiting = LiveContent.from(vectors().getJSONObject(1))!!
        assertEquals("Needs you", waiting.chip)
        assertTrue(waiting.answerable)
        assertFalse(LiveContent.from(vectors().getJSONObject(2))!!.answerable)
        assertNull(LiveContent.from(JSONObject("{}")))
        assertNull(LiveContent.from(null))
    }
}
