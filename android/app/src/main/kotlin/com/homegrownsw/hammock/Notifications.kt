package com.homegrownsw.hammock

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.PushCrypto
import hammock.v1.Hammock
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeoutOrNull
import org.json.JSONObject
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/**
 * Notifications from your computers, built here from what a push decrypts
 * to (Android has no notification extension: HammockMessagingService hands
 * each push to [show]). Channels per kind, so each can be turned off or made
 * quieter in the system's settings; actions as the iPhone has them.
 */
object Notifications {
    const val EXTRA_PANE = "pane"
    const val EXTRA_HOST = "host"
    const val EXTRA_NEEDS_YOU = "needsYou"
    /** When the computer sent what a notification shows (its note's "at", by its clock). */
    private const val EXTRA_SENT = "com.homegrownsw.hammock.sent"

    const val CH_NEEDS_YOU = "needs_you"
    const val CH_FINISHED = "finished"
    const val CH_ERRORS = "errors"
    const val CH_KEY = "hammock_key"
    const val CH_JOIN = "join"

    fun createChannels(ctx: Context) {
        val nm = ctx.getSystemService(NotificationManager::class.java)
        nm.createNotificationChannels(listOf(
            NotificationChannel(CH_NEEDS_YOU, "Needs You", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "An agent is waiting for you: a permission, a question or a plan."
                setShowBadge(true)
            },
            NotificationChannel(CH_FINISHED, "Finished", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "An agent finished its turn."
                setShowBadge(true)
            },
            NotificationChannel(CH_ERRORS, "Errors", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "An agent stopped on an error."
                setShowBadge(true)
            },
            NotificationChannel(CH_KEY, "Hammock Key", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Something asks to sign with your computer's Hammock Key."
                setShowBadge(true)
            },
            NotificationChannel(CH_JOIN, "Join Requests", NotificationManager.IMPORTANCE_HIGH).apply {
                description = "Someone asks to join a Group you share."
                setShowBadge(true)
            },
        ))
    }

    /** A computer's note, decrypted: only if it names the computer whose key opened it. */
    fun open(ctx: Context, e: String): JSONObject? {
        val sealed = runCatching { Base64.getDecoder().decode(e) }.getOrNull() ?: return null
        if (sealed.size < 12 + 16) return null
        for ((host, key) in Push.allKeys(ctx)) {
            val plain = PushCrypto.open(key, sealed) ?: continue
            val note = runCatching { JSONObject(String(plain)) }.getOrNull() ?: continue
            if (note.optString("host").equals(host, ignoreCase = true)) return note
        }
        return null
    }

    /** Sent within the last hour (by the computer's clock): an older one is a relay replaying it. */
    fun fresh(note: JSONObject): Boolean {
        val at = note.optLong("at", 0)
        return at == 0L || kotlin.math.abs(System.currentTimeMillis() / 1000 - at) < 3600
    }

    /** What a notification is about: its pane, join, grant or key request, else its collapse id. */
    private fun subject(note: JSONObject): String =
        listOf("id", "pane", "join", "grant", "key").map { note.optString(it) }.firstOrNull { it.isNotEmpty() } ?: ""

    private fun tag(host: String, subject: String) = "$host/$subject"

    /** Shows (or, for a clear, takes down) what a push says. */
    fun show(ctx: Context, e: String) {
        val note = open(ctx, e) ?: return // not from one of your computers: nothing shown
        if (!fresh(note)) return
        val host = note.getString("host").lowercase()
        val subject = subject(note)
        val nm = NotificationManagerCompat.from(ctx)
        if (note.optBoolean("clear")) {
            if (subject.isNotEmpty()) clear(ctx, tag(host, subject), note.optLong("at", 0))
            return
        }
        // The app is showing this computer: the computer doesn't push then, but a late one is dropped too.
        val app = ctx.applicationContext as HammockApplication
        if (app.foreground && app.session.host.value?.id == host) return
        val category = note.optString("c")
        val pane = note.optString("pane")
        val channel = channel(note.optString("k"), category, note.optString("t"), note.optString("b"), note.optBoolean("live"))
        val open = Intent(ctx, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP
            putExtra(EXTRA_HOST, host)
            if (pane.isNotEmpty() && category.isEmpty()) putExtra(EXTRA_PANE, pane) else putExtra(EXTRA_NEEDS_YOU, true)
        }
        val n = NotificationCompat.Builder(ctx, channel)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle(note.optString("t").ifEmpty { "Hammock" })
            .setSubText(note.optString("s").ifEmpty { null })
            .setContentText(note.optString("b"))
            .setStyle(NotificationCompat.BigTextStyle().bigText(note.optString("b")))
            .setAutoCancel(true)
            .setGroup(host + "/" + (pane.ifEmpty { subject }))
            .setCategory(if (category == "hammockkey") NotificationCompat.CATEGORY_REMINDER else NotificationCompat.CATEGORY_MESSAGE)
            .setContentIntent(PendingIntent.getActivity(ctx, (host + subject).hashCode(), open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
            .addExtras(android.os.Bundle().apply { putLong(EXTRA_SENT, note.optLong("at", 0)) })
        // The count for the launcher's badge, as the computer counts what needs you.
        if (note.has("n")) n.setNumber(note.optInt("n"))
        val id = note.optString("grant").ifEmpty { note.optString("join").ifEmpty { note.optString("key") } }
        when (category) {
            "grant" -> {
                n.addAction(action(ctx, "Deny", ACTION_GRANT_DENY, host, id, subject, open))
                n.addAction(action(ctx, "Allow", ACTION_GRANT_ALLOW, host, id, subject, open))
            }
            "join" -> {
                n.addAction(action(ctx, "Decline", ACTION_JOIN_DECLINE, host, id, subject, open))
                n.addAction(action(ctx, "Let View", ACTION_JOIN_VIEW, host, id, subject, open))
                // Letting someone drive means typing on your computer: in the app, where you see who.
                n.addAction(NotificationCompat.Action(0, "Let Drive…", PendingIntent.getActivity(ctx, (host + id + "drive").hashCode(), open,
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)))
            }
            "hammockkey" -> {
                n.addAction(action(ctx, "Don't Allow", ACTION_KEY_DENY, host, id, subject, open))
                // Approving needs your fingerprint or face: in the app.
                n.addAction(NotificationCompat.Action(0, "Approve…", PendingIntent.getActivity(ctx, (host + id + "key").hashCode(), open,
                    PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)))
            }
        }
        // Without permission to notify (turned off in Android's settings): nothing to show.
        if (androidx.core.content.ContextCompat.checkSelfPermission(ctx, android.Manifest.permission.POST_NOTIFICATIONS) ==
            android.content.pm.PackageManager.PERMISSION_GRANTED
        ) nm.notify(tag(host, subject), 0, n.build())
    }

    /**
     * The channel for a notification: by what happened, as the computer says
     * (its note's "k"); from a computer from before that, guessed from its
     * category and words.
     */
    fun channel(kind: String, category: String, title: String, body: String, live: Boolean): String = when (kind) {
        "needs_input", "program_needs_input", "grant" -> CH_NEEDS_YOU
        "finished", "program_finished" -> CH_FINISHED
        "errored", "program_errored" -> CH_ERRORS
        "hammockkey" -> CH_KEY
        "join" -> CH_JOIN
        else -> when {
            category == "hammockkey" -> CH_KEY
            category == "join" -> CH_JOIN
            category == "grant" -> CH_NEEDS_YOU
            title.contains("error", true) || body.contains("error", true) -> CH_ERRORS
            live -> CH_NEEDS_YOU
            else -> CH_FINISHED
        }
    }

    const val ACTION_GRANT_ALLOW = "com.homegrownsw.hammock.GRANT_ALLOW"
    const val ACTION_GRANT_DENY = "com.homegrownsw.hammock.GRANT_DENY"
    const val ACTION_JOIN_VIEW = "com.homegrownsw.hammock.JOIN_VIEW"
    const val ACTION_JOIN_DECLINE = "com.homegrownsw.hammock.JOIN_DECLINE"
    const val ACTION_KEY_DENY = "com.homegrownsw.hammock.KEY_DENY"

    private fun action(ctx: Context, title: String, action: String, host: String, id: String, subject: String, open: Intent): NotificationCompat.Action {
        val answer = NotificationAnswer(action, host, id, subject)
        val flags = PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT
        // With the app's own lock on (Require Unlock), every answer opens the
        // app, whose lock asks first, and is sent once it's open, as on the
        // iPhone; else it's answered from the shade.
        val pi = if (Prefs.of(ctx).getBoolean(Prefs.APP_LOCK, false)) {
            PendingIntent.getActivity(ctx, (action + host + id).hashCode(), answer.into(Intent(open)), flags)
        } else {
            PendingIntent.getBroadcast(ctx, (action + host + id).hashCode(), answer.into(Intent(ctx, NotificationActionReceiver::class.java)), flags)
        }
        // Answering for you needs the phone unlocked, as on the iPhone.
        return NotificationCompat.Action.Builder(0, title, pi).setAuthenticationRequired(true).build()
    }

    /**
     * A computer's clear: its notification goes, unless it was sent after the
     * clear (a late one, held back by Doze): the same thing can need you
     * again since, as on the iPhone (DeliveredNotes.clear).
     */
    private fun clear(ctx: Context, tag: String, at: Long) {
        val nm = NotificationManagerCompat.from(ctx)
        for (sbn in ctx.getSystemService(NotificationManager::class.java).activeNotifications) {
            if (sbn.tag != tag) continue
            val sent = sbn.notification.extras.getLong(EXTRA_SENT, 0)
            if (clears(sent, at)) nm.cancel(tag, sbn.id)
        }
    }

    /** Whether a clear sent at `at` takes down a notification sent at `sent` (either unknown: it does). */
    fun clears(sent: Long, at: Long) = at == 0L || sent == 0L || sent <= at

    /** Takes down what a computer showed about things now resolved (answered here, on the computer, anywhere). */
    fun withdraw(ctx: Context, host: String, ids: Collection<String>) {
        val nm = NotificationManagerCompat.from(ctx)
        val active = ctx.getSystemService(NotificationManager::class.java).activeNotifications
        for (sbn in active) {
            val t = sbn.tag ?: continue
            if (!t.startsWith("$host/")) continue
            if (t.removePrefix("$host/") in ids) nm.cancel(t, sbn.id)
        }
    }

    /** After a full state: anything shown that's no longer live (and was about something live) goes. */
    fun reconcile(ctx: Context, host: String, live: Set<String>, paneIDs: Set<String>) {
        val nm = NotificationManagerCompat.from(ctx)
        for (sbn in ctx.getSystemService(NotificationManager::class.java).activeNotifications) {
            val t = sbn.tag ?: continue
            if (!t.startsWith("$host/")) continue
            val subject = t.removePrefix("$host/")
            val stillThere = subject in live || subject in paneIDs
            if (!stillThere && sbn.notification.channelId in setOf(CH_NEEDS_YOU, CH_KEY, CH_JOIN)) nm.cancel(t, sbn.id)
        }
    }

    /** Hooks a model's events to notifications: withdrawing, and telling it where to push. */
    fun hook(ctx: Context, m: AppModel, h: StoredHost) {
        m.onResolved = { ids -> withdraw(ctx, h.id, ids) }
        m.onFullState = { w -> reconcile(ctx, h.id, w.liveNoticeIDs, w.panes.keys) }
        val prev = m.onConnected
        m.onConnected = { connected ->
            prev?.invoke(connected)
            if (!connected.isDemo) Push.register(ctx, connected, h.id)
        }
    }
}

/** An answer chosen on a notification (Allow, Deny, Let View…), from the shade or once the app opens. */
data class NotificationAnswer(val action: String, val host: String, val id: String, val subject: String) {
    /** Puts it in an intent: the receiver's, or the app's with the lock on. */
    fun into(i: Intent): Intent = i.putExtra(EXTRA_ACTION, action).putExtra(EXTRA_HOST, host).putExtra(EXTRA_ID, id).putExtra(EXTRA_SUBJECT, subject)

    /** Tells the computer, over a model signed in to it. */
    suspend fun send(m: AppModel) {
        val about = id // (in the builders below, `id` is the message's own)
        when (action) {
            Notifications.ACTION_GRANT_ALLOW, Notifications.ACTION_GRANT_DENY -> m.request {
                setRespondGrant(Hammock.RespondGrant.newBuilder().setId(about).setDecision(
                    if (action == Notifications.ACTION_GRANT_ALLOW) Hammock.RespondGrant.Decision.DECISION_ALLOW_ONCE
                    else Hammock.RespondGrant.Decision.DECISION_DENY,
                ).build())
            }
            Notifications.ACTION_JOIN_VIEW, Notifications.ACTION_JOIN_DECLINE -> m.request {
                setRespondJoin(Hammock.RespondJoin.newBuilder().setId(about).setRole(
                    if (action == Notifications.ACTION_JOIN_VIEW) Hammock.Role.ROLE_VIEWER else Hammock.Role.ROLE_UNSPECIFIED,
                ).build())
            }
            Notifications.ACTION_KEY_DENY -> m.request {
                setRespondHammockKey(Hammock.RespondHammockKey.newBuilder().setId(about).setAllow(false).build())
            }
        }
    }

    /** Takes its notification down. */
    fun answered(ctx: Context) = NotificationManagerCompat.from(ctx).cancel("$host/$subject", 0)

    companion object {
        private const val EXTRA_ACTION = "answer"
        private const val EXTRA_HOST = "answerHost"
        private const val EXTRA_ID = "answerID"
        private const val EXTRA_SUBJECT = "answerSubject"

        fun from(i: Intent): NotificationAnswer? {
            val action = i.getStringExtra(EXTRA_ACTION) ?: return null
            val host = i.getStringExtra(EXTRA_HOST) ?: return null
            val id = i.getStringExtra(EXTRA_ID) ?: return null
            return NotificationAnswer(action, host, id, i.getStringExtra(EXTRA_SUBJECT) ?: "")
        }
    }
}

/**
 * A notification's action answered from the shade: over the computer's
 * connection if the app has it open, else a brief one of its own.
 */
class NotificationActionReceiver : BroadcastReceiver() {
    override fun onReceive(ctx: Context, intent: Intent) {
        val answer = NotificationAnswer.from(intent) ?: return
        // The app's lock turned on since it was shown: answered in the app,
        // once unlocked (tapping the notification opens it), not from here.
        if (Prefs.of(ctx).getBoolean(Prefs.APP_LOCK, false)) return
        val pending = goAsync()
        val session = (ctx.applicationContext as HammockApplication).session
        CoroutineScope(SupervisorJob() + Dispatchers.Default).launch {
            var brief: AppModel? = null
            try {
                // All within the broadcast's time (Android allows 10 s).
                withTimeoutOrNull(8_500) {
                    // The app's own connection only while it's up (it hangs up in the background).
                    val shown = session.model.value?.takeIf { session.host.value?.id == answer.host && it.link.value == Link.Connected }
                    val m = shown ?: session.briefConnection(answer.host)?.also { brief = it } ?: return@withTimeoutOrNull
                    withTimeoutOrNull(6_000) { m.link.first { it == Link.Connected } }
                    if (m.link.value == Link.Connected) {
                        answer.send(m)
                        answer.answered(ctx)
                    }
                }
            } finally {
                // Stopped however it ends (the time running out too), or it would keep dialling.
                brief?.stop()
                pending.finish()
            }
        }
    }
}
