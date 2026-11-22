package com.homegrownsw.hammock

import android.content.Context
import com.homegrownsw.hammock.client.AppModel
import org.json.JSONObject
import java.io.File
import java.security.SecureRandom
import java.util.Base64

/**
 * Notifications from a computer while the app isn't open: each paired
 * computer gets its own AES-256 key, sent to it with this phone's relay
 * registration (RegisterPush), and seals what it pushes with it, so the
 * relay and Google only ever carry ciphertext (internal/daemon/push.go).
 * The keys live in the app's no-backup files.
 */
object Push {
    /** What a computer notifies about, by default all of it (Settings ▸ Notify Me When). */
    val allEvents = listOf("join", "needs_input", "finished", "errored", "program_needs_input", "program_finished", "program_errored")

    private fun keysFile(ctx: Context) = File(ctx.noBackupFilesDir, "push-keys.json")

    @Synchronized
    private fun keys(ctx: Context): JSONObject = runCatching { JSONObject(keysFile(ctx).readText()) }.getOrDefault(JSONObject())

    @Synchronized
    fun key(ctx: Context, hostID: String): ByteArray {
        val all = keys(ctx)
        all.optString(hostID).takeIf { it.isNotEmpty() }?.let { return Base64.getDecoder().decode(it) }
        val k = ByteArray(32).also { SecureRandom().nextBytes(it) }
        all.put(hostID, Base64.getEncoder().encodeToString(k))
        keysFile(ctx).writeText(all.toString())
        return k
    }

    /** Every computer's key, by its id (for opening a push). */
    fun allKeys(ctx: Context): Map<String, ByteArray> {
        val all = keys(ctx)
        return all.keys().asSequence().associateWith { Base64.getDecoder().decode(all.getString(it)) }
    }

    @Synchronized
    fun forget(ctx: Context, hostID: String) {
        val all = keys(ctx)
        all.remove(hostID)
        keysFile(ctx).writeText(all.toString())
    }

    /**
     * Tells a connected computer where to push to this phone: the relay's
     * registration (PushRegistration, once FCM has given a token) and this
     * computer's key. Does nothing until notifications are set up.
     */
    fun register(ctx: Context, model: AppModel, hostID: String) {
        val reg = PushRegistration.current(ctx) ?: return
        // "badge": notifications carry the count (the computer sends it).
        val events = NotifyPrefs.events(ctx) + "badge"
        model.registerPush(reg.relayURL, reg.relayID, key(ctx, hostID), events)
    }
}
