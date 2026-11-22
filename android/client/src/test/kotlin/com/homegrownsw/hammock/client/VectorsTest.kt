package com.homegrownsw.hammock.client

import hammock.v1.Hammock
import org.json.JSONObject
import org.junit.Test
import java.io.File
import java.util.Base64
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * The same vectors the daemon's and the iPhone's tests check
 * (testdata/client/vectors.json, internal/daemon/vectors_test.go): what this
 * client builds must be byte for byte what the daemon expects.
 */
class VectorsTest {
    private val v = JSONObject(File("../../testdata/client/vectors.json").readText())

    private fun hex(s: String) = ByteArray(s.length / 2) { s.substring(it * 2, it * 2 + 2).toInt(16).toByte() }

    @Test fun signInMessage() {
        val a = v.getJSONArray("auth")
        for (i in 0 until a.length()) {
            val o = a.getJSONObject(i)
            assertEquals(o.getString("message_hex"), DeviceAuth.message(o.getString("cert"), hex(o.getString("nonce_hex"))).hex())
        }
    }

    @Test fun pairingLinks() {
        val a = v.getJSONArray("pairing")
        for (i in 0 until a.length()) {
            val o = a.getJSONObject(i)
            val p = assertNotNull(PairingCodes.parse(o.getString("link")))
            assertEquals(o.getString("host_name"), p.hostName)
            assertEquals(o.getString("cert_sha256"), p.certSha256)
            assertEquals(o.getString("secret_hex"), p.secret.toByteArray().hex())
            assertEquals(o.getJSONArray("addresses").length(), p.addressesCount)
            assertEquals(o.getBoolean("remote_enabled"), p.remoteEnabled)
            // The link as the phone would write it, read back the same.
            assertEquals(p, PairingCodes.parse(PairingCodes.url(p)))
        }
    }

    @Test fun sealedNotes() {
        val a = v.getJSONArray("push")
        for (i in 0 until a.length()) {
            val o = a.getJSONObject(i)
            val plain = assertNotNull(PushCrypto.open(hex(o.getString("key_hex")), Base64.getDecoder().decode(o.getString("sealed_b64"))))
            val note = JSONObject(String(plain))
            val want = o.getJSONObject("note")
            assertEquals(want.toMap(), note.toMap())
            assertEquals(o.getString("host"), note.getString("host"))
            // An agent's permission prompt names the pane and the prompt its Allow and Deny answer.
            if (note.optString("c") == "agentprompt") assertTrue(note.optString("pane").isNotEmpty() && note.optString("prompt").isNotEmpty())
            // Another key opens nothing.
            assertTrue(PushCrypto.open(ByteArray(32), Base64.getDecoder().decode(o.getString("sealed_b64"))) == null)
        }
    }

    @Test fun approvalMessage() {
        val a = v.getJSONArray("approval")
        for (i in 0 until a.length()) {
            val o = a.getJSONObject(i)
            val got = HammockKeyApproval.message(o.getString("key_id"), o.getString("id"), o.getString("data_hash"), o.getString("pane_id"),
                o.getBoolean("window"), o.getLong("expires_unix_ms"))
            assertEquals(o.getString("message"), String(got))
        }
    }
    /** The number on the Sessions tab and with notifications, as the daemon counts it for pushes. */
    @Test fun badgeCount() {
        val states = mapOf(
            "needs_input" to Hammock.AgentState.AGENT_STATE_NEEDS_INPUT, "idle" to Hammock.AgentState.AGENT_STATE_IDLE,
            "errored" to Hammock.AgentState.AGENT_STATE_ERRORED, "working" to Hammock.AgentState.AGENT_STATE_WORKING,
        )
        val a = v.getJSONArray("badge")
        assertTrue(a.length() > 0)
        for (i in 0 until a.length()) {
            val o = a.getJSONObject(i)
            val list = o.getJSONArray("attention")
            val attention = (0 until list.length()).map { Hammock.AttentionItem.newBuilder().setState(states.getValue(list.getString(it))).build() }
            assertEquals(o.getInt("count"), Workspace.badgeCount(attention, o.getInt("grants"), o.getInt("joins"), o.getInt("keys")))
        }
    }
}
