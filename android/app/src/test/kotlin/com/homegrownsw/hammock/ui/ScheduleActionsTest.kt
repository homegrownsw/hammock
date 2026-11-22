package com.homegrownsw.hammock.ui

import com.homegrownsw.hammock.ui.ScheduleAction.DETAILS
import com.homegrownsw.hammock.ui.ScheduleAction.DONT_SKIP
import com.homegrownsw.hammock.ui.ScheduleAction.PAUSE
import com.homegrownsw.hammock.ui.ScheduleAction.RESUME
import com.homegrownsw.hammock.ui.ScheduleAction.RUN_EARLY
import com.homegrownsw.hammock.ui.ScheduleAction.RUN_NOW
import com.homegrownsw.hammock.ui.ScheduleAction.SKIP
import hammock.v1.Hammock.Schedule
import org.junit.Assert.assertEquals
import org.junit.Test

/** A scheduled task's touch-and-hold menu offers what the iPhone's does, in its order. */
class ScheduleActionsTest {
    private val next = 2_000_000_000_000L

    @Test fun anUpcomingTask() {
        val s = Schedule.newBuilder().setNextRunUnixMs(next).build()
        assertEquals(listOf(RUN_NOW, RUN_EARLY, SKIP, PAUSE), scheduleActions(s, details = false))
        assertEquals(listOf(RUN_NOW, RUN_EARLY, SKIP, PAUSE, DETAILS), scheduleActions(s, details = true))
    }

    @Test fun aSkippedRunCantAlsoRunEarly() {
        val s = Schedule.newBuilder().setNextRunUnixMs(next).setSkipUntilUnixMs(next + 1).build()
        assertEquals(listOf(RUN_NOW, DONT_SKIP, PAUSE), scheduleActions(s, details = false))
    }

    @Test fun aPausedTaskOnlyRunsNowOrResumes() {
        val s = Schedule.newBuilder().setNextRunUnixMs(next).setPaused(true).build()
        assertEquals(listOf(RUN_NOW, RESUME), scheduleActions(s, details = false))
    }
}
