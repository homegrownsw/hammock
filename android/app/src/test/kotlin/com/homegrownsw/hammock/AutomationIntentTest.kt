package com.homegrownsw.hammock

import com.homegrownsw.hammock.AutomationIntent.Entry
import com.homegrownsw.hammock.AutomationIntent.Found
import hammock.v1.Hammock
import hammock.v1.Hammock.ServerMessage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Test

/** Tasker's intent: which automation it names, its secret, what's sent, and what the computer said. */
class AutomationIntentTest {
    private val studio = Entry("aa11", "sc_1", "Deploy", "s1")
    private val studioTests = Entry("aa11", "sc_2", "Run Tests", "s2")
    private val laptop = Entry("bb22", "sc_3", "Deploy", "s3")
    private val all = listOf(studio, studioTests, laptop)
    private val names = mapOf("aa11" to listOf("Studio.local", "Studio"), "bb22" to listOf("Laptop.local", "Laptop"))

    @Test fun findsByIDThenName() {
        assertEquals(Found.One(studioTests), AutomationIntent.find(all, "sc_2", null, names))
        assertEquals(Found.One(studioTests), AutomationIntent.find(all, " run tests ", null, names))
        assertEquals(Found.None, AutomationIntent.find(all, "Backup", null, names))
        assertEquals(Found.None, AutomationIntent.find(all, "", null, names))
    }

    @Test fun aNameOnTwoComputersNeedsTheComputer() {
        assertEquals(Found.Several, AutomationIntent.find(all, "Deploy", null, names))
        assertEquals(Found.One(laptop), AutomationIntent.find(all, "Deploy", "laptop", names))
        assertEquals(Found.One(studio), AutomationIntent.find(all, "Deploy", "aa11", names))
        assertEquals(Found.None, AutomationIntent.find(all, "sc_2", "Laptop", names))
    }

    @Test fun secrets() {
        val a = AutomationIntent.newSecret()
        assertEquals(32, a.length)
        assertTrue(a.all { it.isLetterOrDigit() || it == '-' || it == '_' })
        assertNotEquals(a, AutomationIntent.newSecret())
        assertTrue(AutomationIntent.matches(a, a))
        assertFalse(AutomationIntent.matches(a.dropLast(1), a))
        assertFalse(AutomationIntent.matches(null, a))
        assertFalse(AutomationIntent.matches("", ""))
    }

    @Test fun cutsOnACharacterBoundary() {
        assertEquals("abc", AutomationIntent.cut("abc", 10))
        assertEquals("ab", AutomationIntent.cut("abc", 2))
        // é is 2 bytes, 😀 4: never half of one.
        assertEquals("a", AutomationIntent.cut("aé", 2))
        assertEquals("aé", AutomationIntent.cut("aé", 3))
        assertEquals("x", AutomationIntent.cut("x😀", 4))
        assertEquals("x😀", AutomationIntent.cut("x😀y", 5))
        val big = "é".repeat(40_000)
        assertEquals(32_768, AutomationIntent.cut(big, AutomationIntent.MAX_DATA).length)
    }

    @Test fun theMessage() {
        val m = AutomationIntent.message("sc_1", "hello", " Arrived home ")
        assertEquals("sc_1", m.scheduleId)
        assertTrue(m.trigger && m.statusReply)
        assertEquals("tasker", m.source)
        assertEquals("hello", m.data)
        assertEquals("Arrived home", m.label)
    }

    @Test fun repliesNewAndOld() {
        fun reply(s: Hammock.TriggerReply.Status, reason: String = "") = ServerMessage.newBuilder()
            .setTriggerReply(Hammock.TriggerReply.newBuilder().setStatus(s).setReason(reason)).build()
        assertEquals("Started", AutomationIntent.outcome(reply(Hammock.TriggerReply.Status.STATUS_STARTED)).text)
        assertEquals("Queued: waiting for its cooldown",
            AutomationIntent.outcome(reply(Hammock.TriggerReply.Status.STATUS_QUEUED, "waiting for its cooldown")).text)
        assertEquals("Skipped: Only if: not plugged in",
            AutomationIntent.outcome(reply(Hammock.TriggerReply.Status.STATUS_SKIPPED, "Only if: not plugged in")).text)
        // An older computer: Created when it started, Ok when it waits.
        assertEquals("Started", AutomationIntent.outcome(ServerMessage.newBuilder().setCreated(Hammock.Created.newBuilder().setId("tb_1")).build()).text)
        assertEquals("Queued", AutomationIntent.outcome(ServerMessage.newBuilder().setOk(Hammock.Ok.getDefaultInstance()).build()).text)
    }
}
