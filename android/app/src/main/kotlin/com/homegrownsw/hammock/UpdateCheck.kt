package com.homegrownsw.hammock

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.util.Log
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/** A release newer than this build, from the feed. */
data class AppUpdate(val version: String, val build: Int, val apk: String, val notes: String)

/**
 * Updates for a build installed from its APK (no store updates it): the feed
 * at homegrownsw.com/hammock/android.json (scripts/release writes it,
 * marketing/android.json, and the website publishes it) names the latest
 * release, its build, its APK and its notes. Checked when the app comes to
 * the front, at most once a day; a newer one is offered once (UpdatePrompt)
 * and stays in Settings ▸ About. Download hands the APK to the browser, which
 * downloads it for the person to open and install.
 *
 * Release builds only: a debug build is someone's own, unless --es
 * updateFeed <url> names a feed to try (docs/debug-hooks.md).
 */
object UpdateCheck {
    private const val FEED = "https://homegrownsw.com/hammock/android.json"
    private const val DAY_MS = 24 * 60 * 60 * 1000L
    private const val CHECKED_AT = "updateCheckedAt"
    private const val LAST_FEED = "updateFeed"        // the last feed read, so a known update outlives a restart
    private const val DISMISSED = "updateDismissedBuild"
    private const val TAG = "HammockUpdate"

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val _available = MutableStateFlow<AppUpdate?>(null)
    /** A newer release, once one's been seen. */
    val available: StateFlow<AppUpdate?> = _available.asStateFlow()

    enum class Status { Idle, Checking, UpToDate, Failed }
    private val _status = MutableStateFlow(Status.Idle)
    val status: StateFlow<Status> = _status.asStateFlow()

    /** A feed to check instead (debug builds: --es updateFeed). */
    @Volatile var feedOverride: String? = null

    val enabled: Boolean get() = !BuildConfig.DEBUG || feedOverride != null

    /** On coming to the front: the last feed's news at once, then a fresh look if a day has passed. */
    fun checkIfDue(ctx: Context) {
        if (!enabled) return
        val prefs = Prefs.of(ctx)
        prefs.getString(LAST_FEED, null)?.let { parse(it) }?.let(::offer)
        if (feedOverride == null && System.currentTimeMillis() - prefs.getLong(CHECKED_AT, 0) < DAY_MS) return
        check(ctx)
    }

    /** Looks now (Settings ▸ Check for Updates). */
    fun check(ctx: Context) {
        if (!enabled || _status.value == Status.Checking) return
        val app = ctx.applicationContext
        _status.value = Status.Checking
        scope.launch {
            val text = try {
                fetch(feedOverride ?: FEED)
            } catch (e: Exception) {
                Log.w(TAG, "couldn't read the update feed: $e")
                _status.value = Status.Failed
                return@launch
            }
            val u = parse(text)
            if (u == null) {
                _status.value = Status.Failed
                return@launch
            }
            Prefs.of(app).edit().putLong(CHECKED_AT, System.currentTimeMillis()).putString(LAST_FEED, text).apply()
            offer(u)
            _status.value = if (_available.value == null) Status.UpToDate else Status.Idle
        }
    }

    private fun offer(u: AppUpdate) {
        if (u.build > BuildConfig.VERSION_CODE) _available.value = u
    }

    /** Whether to ask about this one (not once it's been answered). */
    fun shouldPrompt(ctx: Context, u: AppUpdate) = Prefs.of(ctx).getInt(DISMISSED, 0) < u.build

    fun answered(ctx: Context, u: AppUpdate) = Prefs.of(ctx).edit().putInt(DISMISSED, u.build).apply()

    fun download(ctx: Context, u: AppUpdate) = open(ctx, u.apk)

    fun whatsNew(ctx: Context, u: AppUpdate) = open(ctx, u.notes)

    private fun open(ctx: Context, url: String) {
        if (!url.startsWith("https://")) return
        ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(url)).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    }

    private fun fetch(url: String): String {
        val c = URL(url).openConnection() as HttpURLConnection
        c.connectTimeout = 10_000
        c.readTimeout = 10_000
        c.setRequestProperty("Accept", "application/json")
        try {
            if (c.responseCode != 200) throw java.io.IOException("HTTP ${c.responseCode}")
            return c.inputStream.bufferedReader().use { it.readText() }
        } finally {
            c.disconnect()
        }
    }

    /** The feed: {version, build, apk, notes}. Null if it isn't one. */
    internal fun parse(text: String): AppUpdate? = try {
        val o = JSONObject(text)
        AppUpdate(o.getString("version"), o.getInt("build"), o.getString("apk"), o.optString("notes"))
    } catch (_: Exception) {
        null
    }
}
