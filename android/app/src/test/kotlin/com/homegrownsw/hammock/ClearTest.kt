package com.homegrownsw.hammock

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

/** A computer's clear takes down what it sent before, not a newer notification about the same thing (a late clear, held back by Doze). */
class ClearTest {
    @Test fun clearsOnlyWhatCameBefore() {
        assertTrue(Notifications.clears(sent = 100, at = 200))
        assertTrue(Notifications.clears(sent = 200, at = 200))
        assertFalse(Notifications.clears(sent = 300, at = 200))
        // Either time unknown: it goes, as before.
        assertTrue(Notifications.clears(sent = 0, at = 200))
        assertTrue(Notifications.clears(sent = 300, at = 0))
    }
}
