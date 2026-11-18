package com.homegrownsw.hammock

import android.Manifest
import android.app.NotificationManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.rule.GrantPermissionRule
import org.json.JSONObject
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.security.SecureRandom
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * A computer's sealed note, as the daemon seals it (push.go seal), shown as a
 * notification with its actions, taken down by a clear, and ignored when it's
 * from no computer this phone knows or too old.
 */
@RunWith(AndroidJUnit4::class)
class NotificationsTest {
    @get:Rule val notify: GrantPermissionRule = GrantPermissionRule.grant(Manifest.permission.POST_NOTIFICATIONS)

    private val ctx = InstrumentationRegistry.getInstrumentation().targetContext
    private val host = "0f".repeat(32)
    private val nm get() = ctx.getSystemService(NotificationManager::class.java)

    private fun seal(key: ByteArray, note: JSONObject): String {
        val nonce = ByteArray(12).also { SecureRandom().nextBytes(it) }
        val c = Cipher.getInstance("AES/GCM/NoPadding")
        c.init(Cipher.ENCRYPT_MODE, SecretKeySpec(key, "AES"), GCMParameterSpec(128, nonce))
        return Base64.getEncoder().encodeToString(nonce + c.doFinal(note.toString().toByteArray()))
    }

    private fun note(extra: JSONObject.() -> Unit = {}) = JSONObject().put("t", "Claude needs you").put("s", "Web ▸ api")
        .put("b", "Allow Bash: npm test?").put("c", "grant").put("grant", "gr_1").put("host", host).put("id", "c_1")
        .put("at", System.currentTimeMillis() / 1000).put("live", true).apply(extra)

    @After fun tearDown() {
        nm.cancelAll()
        Push.forget(ctx, host)
    }

    @Test fun showsTakesDownAndRefuses() {
        val key = Push.key(ctx, host)
        Notifications.show(ctx, seal(key, note()))
        val shown = nm.activeNotifications.filter { it.tag == "$host/c_1" }
        assertEquals(1, shown.size)
        val n = shown[0].notification
        assertEquals(Notifications.CH_NEEDS_YOU, n.channelId)
        assertEquals(listOf("Deny", "Allow"), n.actions.map { it.title.toString() })

        // Dealt with elsewhere: the computer's silent clear takes it down.
        Notifications.show(ctx, seal(key, JSONObject().put("host", host).put("id", "c_1").put("clear", true)))
        assertTrue(nm.activeNotifications.none { it.tag == "$host/c_1" })

        // Sealed with another key, or sent long ago: nothing.
        Notifications.show(ctx, seal(ByteArray(32), note()))
        Notifications.show(ctx, seal(key, note { put("at", System.currentTimeMillis() / 1000 - 7200) }))
        assertTrue(nm.activeNotifications.none { it.tag?.startsWith(host) == true })
    }
}
