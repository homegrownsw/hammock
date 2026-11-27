package com.homegrownsw.hammock.ui

import hammock.v1.Hammock.CalendarTrigger
import hammock.v1.Hammock.CommandCheckTrigger
import hammock.v1.Hammock.Condition
import hammock.v1.Hammock.EventTrigger
import hammock.v1.Hammock.GitHubTrigger
import hammock.v1.Hammock.MacTrigger
import hammock.v1.Hammock.Schedule
import hammock.v1.Hammock.ShortcutTrigger
import hammock.v1.Hammock.SlackTrigger
import hammock.v1.Hammock.UrlCheckTrigger
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

    @Test fun urlChecksAndPlaces() {
        val u = EventTrigger.newBuilder().setUrl(UrlCheckTrigger.newBuilder().setUrl("https://example.com/status")
            .setOn(UrlCheckTrigger.On.ON_CONTAINS).setText("deployed").setEveryMinutes(15).setLastResult("200 OK")).build()
        assertEquals("https://example.com/status starts containing “deployed”, checked every 15 minutes", ScheduleWords.trigger(u, null))
        assertEquals("200 OK", ScheduleWords.checkResult(u))
        assertEquals("The Mac arrives at “Office”", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_PLACE_ARRIVE).setName("Office")))
        assertEquals("The Mac leaves “Studio”", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_PLACE_LEAVE).setName("Studio")))
        assertEquals("the Mac is at “Office”", ScheduleWords.condition(Condition.newBuilder().setKind(Condition.Kind.KIND_PLACE).setPlace("Office").build()))
    }

    @Test fun shortcuts() {
        assertEquals("Shortcuts runs it", ScheduleWords.trigger(EventTrigger.newBuilder().setShortcut(ShortcutTrigger.getDefaultInstance()).build(), null))
    }

    private fun mac(b: MacTrigger.Builder) = ScheduleWords.trigger(EventTrigger.newBuilder().setMac(b).build(), null)

    @Test fun thisMac() {
        assertEquals("The startup disk has less than 10% free", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_DISK_LOW).setPercent(10)))
        assertEquals("/Volumes/Backup has less than 50 GB free",
            mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_DISK_LOW).setGigabytes(50).setVolume("/Volumes/Backup")))
        assertEquals("The battery falls below 20%", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_BATTERY_LOW).setPercent(20)))
        assertEquals("The Mac wakes after sleeping 30 minutes or more", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_WAKE).setMinutes(30)))
        assertEquals("I’ve been away from the Mac for 15 minutes", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_AWAY).setMinutes(15)))
        assertEquals("I’m back at the Mac after 10 minutes or more away", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_BACK)))
        assertEquals("The Mac joins the Wi-Fi “Office”", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_WIFI_JOINED).setName("Office")))
        assertEquals("“Xcode” opens", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_APP_OPENED).setName("Xcode")))
        assertEquals("A display is connected", mac(MacTrigger.newBuilder().setEvent(MacTrigger.Event.EVENT_DISPLAY)))
    }

    @Test fun calendarAndGitHub() {
        fun cal(b: CalendarTrigger.Builder) = ScheduleWords.trigger(EventTrigger.newBuilder().setCalendar(b).build(), null)
        assertEquals("At the end of events titled “Standup”", cal(CalendarTrigger.newBuilder().setWhen(CalendarTrigger.When.WHEN_END).setTitle("Standup")))
        assertEquals("Free time of 60 minutes or more begins in working hours (Work)",
            cal(CalendarTrigger.newBuilder().setWhen(CalendarTrigger.When.WHEN_FREE).setFreeMinutes(60).setCalendar("Work")))
        val wf = GitHubTrigger.newBuilder().setEvent(GitHubTrigger.Event.EVENT_WORKFLOW).setWorkflow("ci.yml").setRepo("acme/w")
        assertEquals("The “ci.yml” workflow finishes (acme/w)", ScheduleWords.trigger(EventTrigger.newBuilder().setGithub(wf).build(), null))
    }

    @Test fun onlyIf() {
        val s = Schedule.newBuilder()
            .addOnlyIf(Condition.newBuilder().setKind(Condition.Kind.KIND_POWER))
            .addOnlyIf(Condition.newBuilder().setKind(Condition.Kind.KIND_HOURS).setFromMinute(9 * 60).setToMinute(17 * 60 + 30))
            .addOnlyIf(Condition.newBuilder().setKind(Condition.Kind.KIND_DAYS).addAllWeekdays(listOf(1, 3)))
            .build()
        assertEquals("the Mac is plugged in and between 09:00 and 17:30 and on Mon, Wed", ScheduleWords.onlyIf(s))
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

    @Test fun slack() {
        fun t(b: SlackTrigger.Builder) = ScheduleWords.trigger(EventTrigger.newBuilder().setSlack(b).build(), null)
        assertEquals("A Slack message in #deploys containing “failed”",
            t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_MESSAGE).setChannel("deploys").setText("failed")))
        assertEquals("A Slack message in C0123ABCD from “sam”, mine too",
            t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_MESSAGE).setChannel("C0123ABCD").setFrom("sam").setMine(true)))
        assertEquals("I’m mentioned in Slack (#ops) matching /deploy (failed|broke), again/",
            t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_MENTION).setChannel("ops").setText("/deploy (failed|broke), again/")))
        assertEquals("A Slack direct message to me from “Sam Example”",
            t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_DM).setFrom("Sam Example")))
        assertEquals("A reply in a Slack thread I started containing “ship it”",
            t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_THREAD_REPLY).setText("ship it")))
        assertEquals("I react with :eyes: in Slack", t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_REACTION).setEmoji("eyes")))
        assertEquals("Someone reacts to a Slack message (#ops)",
            t(SlackTrigger.newBuilder().setEvent(SlackTrigger.Event.EVENT_REACTION).setChannel("ops").setAnyone(true)))
    }

    @Test fun checkResult() {
        assertEquals("Failed (exit 2)", ScheduleWords.checkResult(check("make test", result = "Failed (exit 2)")))
        assertNull(ScheduleWords.checkResult(check("make test")))
        assertNull(ScheduleWords.checkResult(EventTrigger.newBuilder().setShortcut(ShortcutTrigger.getDefaultInstance()).build()))
    }
}
