package com.homegrownsw.hammock

import android.app.Activity
import android.app.NotificationChannel
import android.app.NotificationManager
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.DaemonRefused
import com.homegrownsw.hammock.client.Link
import hammock.v1.Hammock
import hammock.v1.Hammock.ServerMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.security.MessageDigest
import java.security.SecureRandom
import java.util.Base64

/**
 * Running an automation from Tasker (or any app that can send an intent): a
 * broadcast to [RunAutomationReceiver] naming the automation, with its secret.
 *
 * Each automation's secret is made here, the first time Run from Tasker… is
 * opened, and kept in the app's own storage by computer and automation; it
 * never goes to the computer. A broadcast without it does nothing: it's what
 * stops other apps on the phone from running automations.
 */
object AutomationIntent {
    const val ACTION = "com.homegrownsw.hammock.RUN_AUTOMATION"
    const val EXTRA_AUTOMATION = "automation"
    const val EXTRA_SECRET = "secret"
    const val EXTRA_TEXT = "text"
    const val EXTRA_LABEL = "label"
    const val EXTRA_COMPUTER = "computer"

    /** An ordered broadcast's result codes: started or queued (its data says which), refused, or not sent. */
    const val RESULT_RUN = Activity.RESULT_OK
    const val RESULT_REFUSED = Activity.RESULT_FIRST_USER
    const val RESULT_FAILED = Activity.RESULT_FIRST_USER + 1

    /** The most of `text` sent, in UTF-8 bytes (the computer takes 64 KB). */
    const val MAX_DATA = 64 * 1024
    const val MAX_LABEL = 200

    const val CH_AUTOMATIONS = "automations"

    fun createChannel(ctx: Context) {
        ctx.getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CH_AUTOMATIONS, "Automations", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "An automation another app asked to run couldn't run."
            },
        )
    }

    /** One automation's secret, as kept: its computer, id and (last seen) name. */
    data class Entry(val host: String, val id: String, val name: String, val secret: String)

    private fun prefs(ctx: Context) = ctx.getSharedPreferences("automation_secrets", Context.MODE_PRIVATE)
    private fun key(host: String, id: String) = "$host/$id"

    fun entries(ctx: Context): List<Entry> = prefs(ctx).all.mapNotNull { (k, v) ->
        val host = k.substringBefore('/', "").ifEmpty { return@mapNotNull null }
        val o = runCatching { JSONObject(v as? String ?: return@mapNotNull null) }.getOrNull() ?: return@mapNotNull null
        Entry(host, k.substringAfter('/'), o.optString("name"), o.optString("secret").ifEmpty { return@mapNotNull null })
    }

    /** Its secret, made now if it has none; its name kept up to date (for an intent that names it). */
    @Synchronized
    fun secret(ctx: Context, host: String, id: String, name: String): String {
        val old = prefs(ctx).getString(key(host, id), null)?.let { runCatching { JSONObject(it) }.getOrNull() }
        val secret = old?.optString("secret")?.ifEmpty { null } ?: newSecret()
        if (old == null || old.optString("name") != name) save(ctx, host, id, name, secret)
        return secret
    }

    /** A new secret in place of its old one, which stops working. */
    @Synchronized
    fun renew(ctx: Context, host: String, id: String, name: String): String = newSecret().also { save(ctx, host, id, name, it) }

    /** A computer forgotten: its automations' secrets go with it. */
    fun forget(ctx: Context, host: String) {
        val p = prefs(ctx)
        p.edit().apply { p.all.keys.filter { it.startsWith("$host/") }.forEach(::remove) }.apply()
    }

    private fun save(ctx: Context, host: String, id: String, name: String, secret: String) =
        prefs(ctx).edit().putString(key(host, id), JSONObject().put("secret", secret).put("name", name).toString()).apply()

    /** 24 random bytes, base64url without padding (32 characters). */
    fun newSecret(): String {
        val b = ByteArray(24)
        SecureRandom().nextBytes(b)
        return Base64.getUrlEncoder().withoutPadding().encodeToString(b)
    }

    /** The two secrets match, compared in constant time; an empty one never does. */
    fun matches(given: String?, kept: String): Boolean {
        if (given.isNullOrEmpty() || kept.isEmpty()) return false
        return MessageDigest.isEqual(given.toByteArray(), kept.toByteArray())
    }

    /** What an intent's names came to: its automation, none that's set up, or more than one (on different computers). */
    sealed interface Found {
        data class One(val entry: Entry) : Found
        data object None : Found
        data object Several : Found
    }

    /**
     * The automation an intent names: by its id, else its name (any case); on the computer it names
     * ([computer]: a computer's id or name, [hostNames] each paired one's id to its names), if it does.
     */
    fun find(entries: List<Entry>, automation: String, computer: String?, hostNames: Map<String, List<String>>): Found {
        val a = automation.trim()
        if (a.isEmpty()) return Found.None
        val c = computer?.trim().orEmpty()
        val on = if (c.isEmpty()) entries else entries.filter { e ->
            e.host.equals(c, ignoreCase = true) || hostNames[e.host].orEmpty().any { it.equals(c, ignoreCase = true) }
        }
        val byID = on.filter { it.id == a }
        val found = byID.ifEmpty { on.filter { it.name.trim().equals(a, ignoreCase = true) } }
        return when (found.size) {
            0 -> Found.None
            1 -> Found.One(found[0])
            else -> Found.Several
        }
    }

    /** [s] in at most [maxBytes] of UTF-8, cut on a character's boundary (a surrogate pair kept whole). */
    fun cut(s: String, maxBytes: Int): String {
        if (s.length * 3 <= maxBytes) return s
        var bytes = 0
        var i = 0
        while (i < s.length) {
            val cp = s.codePointAt(i)
            val n = when {
                cp < 0x80 -> 1
                cp < 0x800 -> 2
                cp < 0x10000 -> 3
                else -> 4
            }
            if (bytes + n > maxBytes) return s.substring(0, i)
            bytes += n
            i += Character.charCount(cp)
        }
        return s
    }

    /** The message that runs it, as a trigger from Tasker. */
    fun message(id: String, text: String, label: String): Hammock.RunSchedule = Hammock.RunSchedule.newBuilder()
        .setScheduleId(id).setTrigger(true).setSource("tasker")
        .setData(cut(text, MAX_DATA)).setLabel(cut(label.trim(), MAX_LABEL)).setStatusReply(true).build()

    /** What the computer said: started, queued or skipped (with why, if it says). */
    data class Outcome(val queued: Boolean, val reason: String, val skipped: Boolean = false) {
        /** The result data for an ordered broadcast: "Started", or "Queued" or "Skipped" and why. */
        val text: String get() {
            val word = if (skipped) "Skipped" else if (queued) "Queued" else return "Started"
            return if (reason.isEmpty()) word else "$word: $reason"
        }
    }

    /** Reads its reply: TriggerReply, or (an older computer) Created when it started and Ok when it waits. */
    fun outcome(r: ServerMessage): Outcome = when (r.msgCase) {
        ServerMessage.MsgCase.TRIGGER_REPLY -> when (r.triggerReply.status) {
            Hammock.TriggerReply.Status.STATUS_QUEUED -> Outcome(queued = true, reason = r.triggerReply.reason)
            // (Only If didn't hold, or a run was already waiting: it didn't run.)
            Hammock.TriggerReply.Status.STATUS_SKIPPED -> Outcome(queued = false, reason = r.triggerReply.reason, skipped = true)
            else -> Outcome(queued = false, reason = r.triggerReply.reason)
        }
        ServerMessage.MsgCase.OK -> Outcome(queued = true, reason = "")
        else -> Outcome(queued = false, reason = "")
    }

    /** Says, in a notification, that an automation couldn't run. */
    fun notifyFailure(ctx: Context, title: String, text: String) {
        if (androidx.core.content.ContextCompat.checkSelfPermission(ctx, android.Manifest.permission.POST_NOTIFICATIONS) !=
            android.content.pm.PackageManager.PERMISSION_GRANTED
        ) return
        val open = Intent(ctx, MainActivity::class.java).apply { flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP }
        val n = NotificationCompat.Builder(ctx, CH_AUTOMATIONS)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(title)
            .setContentText(text)
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setAutoCancel(true)
            .setCategory(NotificationCompat.CATEGORY_ERROR)
            .setContentIntent(android.app.PendingIntent.getActivity(ctx, title.hashCode(), open,
                android.app.PendingIntent.FLAG_IMMUTABLE or android.app.PendingIntent.FLAG_UPDATE_CURRENT))
            .build()
        NotificationManagerCompat.from(ctx).notify("automation/$title", 0, n)
    }
}

/**
 * [AutomationIntent.ACTION] from another app: runs the automation it names, if its secret's right,
 * over the computer's connection if the app has it open, else a brief one of its own (the app's
 * process is started for it if it isn't running). An ordered broadcast hears how it went.
 */
class RunAutomationReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        if (intent.action != AutomationIntent.ACTION) return
        val ordered = isOrderedBroadcast
        val app = ctx.applicationContext as HammockApplication
        val session = app.session
        val automation = intent.getStringExtra(AutomationIntent.EXTRA_AUTOMATION).orEmpty()
        val hostNames = session.hosts.value.associate { it.id to listOf(it.hostName, it.shortName) }
        val found = AutomationIntent.find(AutomationIntent.entries(ctx), automation, intent.getStringExtra(AutomationIntent.EXTRA_COMPUTER), hostNames)
        // Refused here, before anything's sent: the same to the caller whatever was wrong.
        val entry = (found as? AutomationIntent.Found.One)?.entry
        if (entry == null || !AutomationIntent.matches(intent.getStringExtra(AutomationIntent.EXTRA_SECRET), entry.secret)) {
            if (ordered) setResult(AutomationIntent.RESULT_REFUSED, "Refused", null)
            return
        }
        val host = session.hosts.value.firstOrNull { it.id == entry.host }
        if (host == null) {
            if (ordered) setResult(AutomationIntent.RESULT_REFUSED, "Refused", null)
            return
        }
        val text = intent.getStringExtra(AutomationIntent.EXTRA_TEXT).orEmpty()
        val label = intent.getStringExtra(AutomationIntent.EXTRA_LABEL).orEmpty()
        val title = "Couldn’t run “${entry.name.ifEmpty { "an automation" }}”"
        val pending = goAsync()
        CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            var brief: AppModel? = null
            var code = AutomationIntent.RESULT_FAILED
            var data = "${host.shortName} can’t be reached"
            try {
                // All within the broadcast's time (Android allows 10 s).
                withTimeoutOrNull(9_000) {
                    val shown = session.model.value?.takeIf { session.host.value?.id == host.id && it.link.value == Link.Connected && !it.isDemo }
                    val m = shown ?: session.briefConnection(host.id)?.also { brief = it } ?: return@withTimeoutOrNull
                    withTimeoutOrNull(7_000) { m.link.first { it == Link.Connected } }
                    if (m.link.value != Link.Connected) return@withTimeoutOrNull
                    try {
                        val r = m.call { setRunSchedule(AutomationIntent.message(entry.id, text, label)) }
                        val o = AutomationIntent.outcome(r)
                        code = AutomationIntent.RESULT_RUN
                        data = o.text
                    } catch (e: DaemonRefused) {
                        // Automations off, it's paused, it was deleted…
                        code = AutomationIntent.RESULT_REFUSED
                        data = e.message ?: "Refused"
                    }
                }
            } catch (e: Exception) {
                data = e.message ?: data
            } finally {
                // Stopped however it ends (the time running out too), or it would keep dialling.
                brief?.stop()
                if (code != AutomationIntent.RESULT_RUN) AutomationIntent.notifyFailure(ctx, title, data)
                if (ordered) pending.setResult(code, data, null)
                pending.finish()
            }
        }
    }
}
