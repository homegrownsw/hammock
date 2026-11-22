package com.homegrownsw.hammock

import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.os.Build
import androidx.core.app.NotificationCompat
import androidx.core.app.NotificationManagerCompat
import com.homegrownsw.hammock.client.AppModel
import org.json.JSONObject

/**
 * A Live Activity's content as the computer makes it (internal/daemon/live.go's liveContent, the same
 * JSON the iPhone's Live Activity shows): the agent that most needs you, else the one at work. The
 * words are the computer's; this only reads them.
 */
data class LiveContent(
    val agent: String,
    val harness: String,
    val place: String,
    val state: String,
    val status: String,
    val doing: String,
    val since: Long,
    val ctx: Int?,
    val pane: String,
    val prompt: String,
    val item: String,
    val more: String,
    val others: Int,
    val waiting: Int,
) {
    /** Allow and Deny answer it: it waits on a permission prompt the computer read. */
    val answerable get() = state == "needs_you" && prompt.isNotEmpty() && pane.isNotEmpty()

    /** Over: it finished or failed (shown a little while, then gone). */
    val over get() = state == "finished" || state == "errored"

    /** The line under its name: where it is, and the others. */
    val subText get() = listOf(place, more).filter { it.isNotEmpty() }.joinToString(" · ")

    /** The status bar chip's few words (a promoted notification's). */
    val chip get() = if (state == "needs_you") "Needs you" else if (others > 0) "${agent.take(10)} +$others" else agent.take(12)

    companion object {
        fun from(o: JSONObject?): LiveContent? {
            if (o == null || o.optString("agent").isEmpty() || o.optString("state").isEmpty()) return null
            return LiveContent(
                agent = o.optString("agent"), harness = o.optString("harness"), place = o.optString("place"),
                state = o.optString("state"), status = o.optString("status"), doing = o.optString("doing"),
                since = o.optLong("since"), ctx = if (o.has("ctx")) o.optInt("ctx").coerceIn(0, 100) else null,
                pane = o.optString("pane"), prompt = o.optString("prompt"), item = o.optString("item"),
                more = o.optString("more"), others = o.optInt("others"), waiting = o.optInt("waiting"),
            )
        }
    }
}

/**
 * Live Activities on Android (Settings ▸ Notifications): one ongoing notification per computer while
 * you're away from it and an agent works or needs you, promoted to a Live Update on Android 16. The
 * computer sends it, updates and ends it in its encrypted pushes (a note of kind "live"), so nothing
 * of it passes the relay in plain text.
 */
object LiveNotification {
    const val CH_LIVE = "live"
    private const val ID = 7501

    /** Android 16's promoted ongoing notifications (Notification.EXTRA_REQUEST_PROMOTED_ONGOING, and its status chip's text). */
    private const val EXTRA_PROMOTED = "android.requestPromotedOngoing"
    private const val EXTRA_CHIP = "android.shortCriticalText"
    private const val EXTRA_PROMPT = "com.homegrownsw.hammock.livePrompt"

    fun createChannel(ctx: Context) {
        ctx.getSystemService(NotificationManager::class.java).createNotificationChannel(
            NotificationChannel(CH_LIVE, "Live Activities", NotificationManager.IMPORTANCE_DEFAULT).apply {
                description = "What your agents are doing while you're away from your computer."
                setShowBadge(false)
                setSound(null, null)
                enableVibration(false)
            },
        )
    }

    private fun tag(host: String) = "$host/live"

    /** Whether a note from a computer is its Live Activity. */
    fun isLive(note: JSONObject) = note.optString("k") == "live"

    /** Shows, updates or ends a computer's Live Activity, as its note says. */
    fun show(ctx: Context, note: JSONObject) {
        val host = note.getString("host").lowercase()
        val nm = NotificationManagerCompat.from(ctx)
        val c = LiveContent.from(note.optJSONObject("L"))
        val end = note.optLong("le", 0)
        val now = System.currentTimeMillis() / 1000
        if (!enabled(ctx) || c == null || end != 0L && end <= now) {
            nm.cancel(tag(host), ID)
            return
        }
        if (androidx.core.content.ContextCompat.checkSelfPermission(ctx, android.Manifest.permission.POST_NOTIFICATIONS) !=
            android.content.pm.PackageManager.PERMISSION_GRANTED
        ) return
        nm.notify(tag(host), ID, build(ctx, host, c, if (end != 0L) (end - now) * 1000 else 0))
    }

    private fun build(ctx: Context, host: String, c: LiveContent, lingerMs: Long): android.app.Notification {
        val open = Intent(ctx, MainActivity::class.java).apply {
            flags = Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP
            putExtra(Notifications.EXTRA_HOST, host)
            if (c.pane.isNotEmpty()) putExtra(Notifications.EXTRA_PANE, c.pane) else putExtra(Notifications.EXTRA_NEEDS_YOU, true)
        }
        val text = c.doing.ifEmpty { c.status }
        val n = NotificationCompat.Builder(ctx, CH_LIVE)
            .setSmallIcon(R.drawable.ic_notification)
            .setContentTitle("${c.agent} · ${c.status}")
            .setContentText(text)
            .setSubText(c.subText.ifEmpty { null })
            .setStyle(NotificationCompat.BigTextStyle().bigText(text))
            .setOnlyAlertOnce(true)
            .setSilent(true)
            .setShowWhen(true)
            .setWhen(c.since * 1000)
            .setCategory(if (c.state == "needs_you") NotificationCompat.CATEGORY_REMINDER else NotificationCompat.CATEGORY_PROGRESS)
            .setContentIntent(PendingIntent.getActivity(ctx, tag(host).hashCode(), open, PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT))
        if (c.over) {
            // Done: says so a while, then goes (or swipe it away).
            n.setOngoing(false).setAutoCancel(true)
            if (lingerMs > 0) n.setTimeoutAfter(lingerMs)
        } else {
            n.setOngoing(true).setUsesChronometer(true)
            c.ctx?.let { n.setProgress(100, it, false) }
            n.addExtras(android.os.Bundle().apply {
                putBoolean(EXTRA_PROMOTED, true)
                putString(EXTRA_CHIP, c.chip)
                putString(EXTRA_PROMPT, c.prompt)
            })
        }
        if (c.answerable) {
            // As a notification's Allow and Deny: for that prompt only, and the phone unlocked first.
            n.addAction(Notifications.answerAction(ctx, "Deny", Notifications.ACTION_PROMPT_DENY, host, c.pane, c.item, open, c.prompt))
            n.addAction(Notifications.answerAction(ctx, "Allow", Notifications.ACTION_PROMPT_ALLOW, host, c.pane, c.item, open, c.prompt))
        }
        return n.build()
    }

    /**
     * After Allow or Deny on the computer's Live Activity (or any of its prompt's): if it still offers
     * them for that prompt, it now says what came of it, without them; the computer's next update
     * says what the agent does next.
     */
    fun answered(ctx: Context, host: String, prompt: String, result: AppModel.PromptAnswer, allow: Boolean) {
        if (result == AppModel.PromptAnswer.UNSENT) return // the buttons stay, to try again
        val nm = NotificationManagerCompat.from(ctx)
        for (sbn in ctx.getSystemService(NotificationManager::class.java).activeNotifications) {
            if (sbn.tag != tag(host) || sbn.notification.extras.getString(EXTRA_PROMPT) != prompt) continue
            val says = if (result == AppModel.PromptAnswer.REFUSED) Notifications.COULDNT_ANSWER else if (allow) "Allowed" else "Denied"
            val again = android.app.Notification.Builder.recoverBuilder(ctx, sbn.notification)
                .setContentText(says)
                .setStyle(android.app.Notification.BigTextStyle().bigText(says))
                .setActions()
                .setOnlyAlertOnce(true)
                .build()
            again.extras.putString(EXTRA_PROMPT, "")
            if (Build.VERSION.SDK_INT >= 33 && androidx.core.content.ContextCompat.checkSelfPermission(ctx, android.Manifest.permission.POST_NOTIFICATIONS) ==
                android.content.pm.PackageManager.PERMISSION_GRANTED
            ) nm.notify(sbn.tag, sbn.id, again)
        }
    }

    /** Turned on in Settings ▸ Notifications. */
    fun enabled(ctx: Context) = Prefs.of(ctx).getBoolean(PREF, false)

    fun setEnabled(ctx: Context, on: Boolean) {
        Prefs.of(ctx).edit().putBoolean(PREF, on).apply()
        if (!on) {
            val nm = NotificationManagerCompat.from(ctx)
            for (sbn in ctx.getSystemService(NotificationManager::class.java).activeNotifications) {
                if (sbn.tag?.endsWith("/live") == true) nm.cancel(sbn.tag, sbn.id)
            }
        }
    }

    private const val PREF = "notify.live"
}
