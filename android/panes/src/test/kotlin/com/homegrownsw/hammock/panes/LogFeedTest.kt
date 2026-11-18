package com.homegrownsw.hammock.panes

import hammock.v1.Hammock.LogEntry
import hammock.v1.Hammock.LogFacet
import hammock.v1.Hammock.LogLevel
import hammock.v1.Hammock.LogView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class LogFeedTest {
    private fun entry(seq: Long, msg: String = "m$seq") = LogEntry.newBuilder().setSeq(seq).setMessage(msg).build()
    private fun entries(r: LongRange) = r.map { entry(it) }

    @Test fun replaceThenAppend() {
        val f = LogFeed()
        val filter = f.watching()
        f.apply(LogView.newBuilder().setReplace(true).setFilter(filter).addAllEntries(entries(1L..3L)).setTotal(3).setHasMore(false).build())
        assertTrue(f.state.value.loaded)
        assertEquals(listOf(1L, 2L, 3L), f.state.value.entries.map { it.seq })
        val before = f.state.value.appended
        f.apply(LogView.newBuilder().addAllEntries(entries(4L..5L)).setTotal(5).build())
        assertEquals(listOf(1L, 2L, 3L, 4L, 5L), f.state.value.entries.map { it.seq })
        assertEquals(5L, f.state.value.total)
        assertEquals(before + 1, f.state.value.appended)
    }

    @Test fun dropsPagesForAReplacedFilter() {
        val f = LogFeed()
        val old = f.watching()
        f.setText("error")
        val wanted = f.watching()
        // The old filter's page arrives late: not shown, and still waiting.
        f.apply(LogView.newBuilder().setReplace(true).setFilter(old).addAllEntries(entries(1L..9L)).build())
        assertFalse(f.state.value.loaded)
        // Appends meanwhile are for the old filter too.
        f.apply(LogView.newBuilder().addEntries(entry(10)).build())
        assertTrue(f.state.value.entries.isEmpty())
        f.apply(LogView.newBuilder().setReplace(true).setFilter(wanted).addEntries(entry(7, "an error")).build())
        assertEquals(listOf(7L), f.state.value.entries.map { it.seq })
        f.apply(LogView.newBuilder().addEntries(entry(11)).build())
        assertEquals(listOf(7L, 11L), f.state.value.entries.map { it.seq })
    }

    @Test fun pagesOlderInFront() {
        val f = LogFeed()
        val filter = f.watching()
        f.apply(LogView.newBuilder().setReplace(true).setFilter(filter).addAllEntries(entries(50L..52L)).setHasMore(true).build())
        assertEquals(50L, f.olderFrom())
        assertTrue(f.state.value.loadingOlder)
        assertNull(f.olderFrom()) // one at a time
        f.apply(LogView.newBuilder().setOlder(true).setFilter(filter).addAllEntries(entries(48L..49L)).setHasMore(false).build())
        assertEquals(listOf(48L, 49L, 50L, 51L, 52L), f.state.value.entries.map { it.seq })
        assertFalse(f.state.value.loadingOlder)
        assertNull(f.olderFrom()) // nothing older
    }

    @Test fun keepsTheNewestPastTheCap() {
        val f = LogFeed()
        f.apply(LogView.newBuilder().setReplace(true).setFilter(f.watching()).addAllEntries(entries(1L..LogFeed.CAP.toLong())).build())
        f.apply(LogView.newBuilder().addAllEntries(entries(LogFeed.CAP + 1L..LogFeed.CAP + 1001L)).build())
        val s = f.state.value
        assertEquals(LogFeed.CAP, s.entries.size)
        assertEquals(LogFeed.CAP + 1001L, s.entries.last().seq)
        assertTrue(s.hasMore)
    }

    @Test fun filters() {
        val f = LogFeed()
        assertFalse(f.state.value.isFiltered)
        f.toggleLevel(LogLevel.LOG_LEVEL_ERROR)
        f.toggleLevel(LogLevel.LOG_LEVEL_DEBUG)
        // Levels go to the daemon in order.
        assertEquals(listOf(LogLevel.LOG_LEVEL_DEBUG, LogLevel.LOG_LEVEL_ERROR), f.state.value.filter.levelsList)
        f.toggleOnly("process:Safari")
        f.toggleHide("process:Safari") // hiding it stops showing only it
        assertEquals(emptyList<String>(), f.state.value.only)
        assertEquals(listOf("process:Safari"), f.state.value.hide)
        f.toggleOnly("process:Safari")
        assertEquals(listOf("process:Safari"), f.state.value.only)
        assertEquals(emptyList<String>(), f.state.value.hide)
        assertTrue(f.state.value.isFiltered)
        assertFalse(f.setText("")) // unchanged
        assertTrue(f.setText("boom"))
        assertEquals("boom", f.state.value.filter.text)
        f.clearFilters()
        assertFalse(f.state.value.isFiltered)
        assertEquals(0, f.state.value.filter.levelsCount)
    }

    @Test fun facets() {
        val f = LogFeed()
        f.apply(
            LogView.newBuilder().setReplace(true).setFilter(f.watching()).addAllFacets(
                listOf(
                    LogFacet.newBuilder().setKey("process:Safari").setCount(10).build(),
                    LogFacet.newBuilder().setKey("tag:net:http").setCount(4).build(),
                    LogFacet.newBuilder().setKey("level:error").setCount(2).build(),
                    LogFacet.newBuilder().setKey("level:debug").setCount(3).build(),
                    LogFacet.newBuilder().setKey("level:bogus").setCount(1).build(),
                ),
            ).build(),
        )
        val s = f.state.value
        assertEquals(listOf(Triple("Safari", "process:Safari", 10L)), s.facets("process"))
        // Only the first colon splits: a tag may have its own.
        assertEquals(listOf(Triple("net:http", "tag:net:http", 4L)), s.facets("tag"))
        assertEquals(listOf(LogLevel.LOG_LEVEL_DEBUG, LogLevel.LOG_LEVEL_ERROR), s.presentLevels)
    }

    @Test fun highlightsPlainTextNotRegex() {
        val a = highlighted("Error: an ERROR here", "error", dark = true)
        assertEquals(2, a.spanStyles.size)
        assertEquals(0, a.spanStyles[0].start)
        assertEquals(10, a.spanStyles[1].start)
        assertEquals(0, highlighted("error", "/err.r/", dark = true).spanStyles.size)
    }
}
