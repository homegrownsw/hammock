package com.homegrownsw.hammock.ui

import hammock.v1.Hammock.CommandCheckTrigger
import hammock.v1.Hammock.EventTrigger
import hammock.v1.Hammock.ShortcutTrigger
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import java.time.ZoneId
import java.time.ZonedDateTime

/** Triggers read as hmk, the Mac and the iPhone say them (schedtrig.Describe, EveryWords and Status). */
class TriggerWordsTest {
    private val zone = ZoneId.of("UTC")
    private fun ms(y: Int, mo: Int, d: Int, h: Int, mi: Int) = ZonedDateTime.of(y, mo, d, h, mi, 0, 0, zone).toInstant().toEpochMilli()
    private val now = ms(2026, 11, 27, 15, 0) // a Friday

    private fun check(cmd: String, on: CommandCheckTrigger.On = CommandCheckTrigger.On.ON_FAIL, every: Int = 0, result: String = "") =
        EventTrigger.newBuilder().setCheck(CommandCheckTrigger.newBuilder().setCommand(cmd).setOn(on).setEveryMinutes(every).setLastResult(result)).build()

    @Test fun commandChecks() {
        assertEquals("“make test” starts failing, checked every 5 minutes", ScheduleWords.trigger(check("make test"), null))
        assertEquals("“curl -sf localhost:8080”’s output changes, checked every hour",
            ScheduleWords.trigger(check("curl -sf localhost:8080", CommandCheckTrigger.On.ON_CHANGE, 60), null))
        assertEquals("“./ping.sh” passes again, checked every minute", ScheduleWords.trigger(check("./ping.sh", CommandCheckTrigger.On.ON_RECOVER, 1), null))
    }

    @Test fun shortcuts() {
        assertEquals("Shortcuts runs it", ScheduleWords.trigger(EventTrigger.newBuilder().setShortcut(ShortcutTrigger.getDefaultInstance()).build(), null))
    }

    @Test fun every() {
        assertEquals("every minute", ScheduleWords.every(0))
        assertEquals("every 5 minutes", ScheduleWords.every(5))
        assertEquals("every 90 minutes", ScheduleWords.every(90))
        assertEquals("every hour", ScheduleWords.every(60))
        assertEquals("every 2 hours", ScheduleWords.every(120))
        assertEquals("every day", ScheduleWords.every(1440))
        assertEquals("every 7 days", ScheduleWords.every(10080))
    }

    @Test fun status() {
        val t = EventTrigger.newBuilder()
        assertEquals("", ScheduleWords.triggerStatus(t.build(), now, zone))
        t.checkedUnixMs = now - 2 * 60_000
        assertEquals("Last checked 2 min ago · never fired", ScheduleWords.triggerStatus(t.build(), now, zone))
        t.firedUnixMs = ms(2026, 11, 26, 14, 2)
        assertEquals("Last checked 2 min ago · last fired yesterday 14:02", ScheduleWords.triggerStatus(t.build(), now, zone))
        t.held = "waiting for its cooldown, until 15:05"
        assertEquals("Last checked 2 min ago · last fired yesterday 14:02 · waiting for its cooldown, until 15:05",
            ScheduleWords.triggerStatus(t.build(), now, zone))
        // A trigger that doesn't look (a webhook): only when it fired.
        val fired = EventTrigger.newBuilder().setFiredUnixMs(now - 5 * 60_000).build()
        assertEquals("Last fired 14:55", ScheduleWords.triggerStatus(fired, now, zone))
    }

    @Test fun agoAndWhen() {
        assertEquals("just now", ScheduleWords.ago(now - 30_000, now))
        assertEquals("just now", ScheduleWords.ago(now + 30_000, now))
        assertEquals("3 h ago", ScheduleWords.ago(now - 3 * 3_600_000 - 1, now))
        assertEquals("1 day ago", ScheduleWords.ago(now - 86_400_000, now))
        assertEquals("2 days ago", ScheduleWords.ago(now - 2 * 86_400_000, now))
        assertEquals("Mon 09:30", ScheduleWords.whenText(ms(2026, 11, 23, 9, 30), now, zone))
        assertEquals("3 Nov", ScheduleWords.whenText(ms(2026, 11, 3, 9, 30), now, zone))
        assertEquals("3 Nov 2025", ScheduleWords.whenText(ms(2025, 11, 3, 9, 30), now, zone))
    }

    @Test fun checkResult() {
        assertEquals("Failed (exit 2)", ScheduleWords.checkResult(check("make test", result = "Failed (exit 2)")))
        assertNull(ScheduleWords.checkResult(check("make test")))
        assertNull(ScheduleWords.checkResult(EventTrigger.newBuilder().setShortcut(ShortcutTrigger.getDefaultInstance()).build()))
    }
}
