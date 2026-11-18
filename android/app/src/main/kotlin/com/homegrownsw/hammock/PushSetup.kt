package com.homegrownsw.hammock

import android.content.Context
import android.util.Log
import com.google.firebase.FirebaseApp
import com.google.firebase.FirebaseOptions
import com.google.firebase.messaging.FirebaseMessaging
import com.google.firebase.messaging.FirebaseMessagingService
import com.google.firebase.messaging.RemoteMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/**
 * Firebase Cloud Messaging, set up from the build's own values (FCM_* in
 * BuildConfig, from the environment at build time: android/README.md) rather
 * than a google-services.json, and only Messaging: nothing else of Firebase.
 * A build without them has no notifications while the app is closed, and
 * Settings says so.
 */
object PushSetup {
    private const val TAG = "HammockPush"
    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)

    val configured: Boolean get() = BuildConfig.FCM_PROJECT_ID.isNotEmpty() && BuildConfig.FCM_APP_ID.isNotEmpty()

    fun start(ctx: Context) {
        if (!configured) return
        if (FirebaseApp.getApps(ctx).isEmpty()) {
            FirebaseApp.initializeApp(ctx, FirebaseOptions.Builder()
                .setProjectId(BuildConfig.FCM_PROJECT_ID)
                .setApplicationId(BuildConfig.FCM_APP_ID)
                .setApiKey(BuildConfig.FCM_API_KEY)
                .setGcmSenderId(BuildConfig.FCM_SENDER_ID)
                .build())
        }
        // Off until the app starts: no token is made before then (manifest).
        FirebaseMessaging.getInstance().isAutoInitEnabled = true
        FirebaseMessaging.getInstance().token.addOnSuccessListener { token -> scope.launch { registerToken(ctx, token) } }
    }

    /**
     * Registers this phone's token with the relay (keeping its id and secret
     * there across new tokens), then tells the computer shown where to push.
     * Re-registering weekly keeps the relay from forgetting an unused phone.
     */
    fun registerToken(ctx: Context, token: String) {
        val prev = PushRegistration.current(ctx)
        val relay = prev?.relayURL ?: BuildConfig.PUSH_RELAY
        val body = JSONObject().put("token", token).put("provider", "fcm")
        if (prev != null) body.put("id", prev.relayID).put("secret", prev.secret)
        try {
            val c = URL("$relay/register").openConnection() as HttpURLConnection
            c.requestMethod = "POST"
            c.doOutput = true
            c.connectTimeout = 15_000
            c.readTimeout = 15_000
            c.setRequestProperty("Content-Type", "application/json")
            c.outputStream.use { it.write(body.toString().toByteArray()) }
            if (c.responseCode !in 200..299) {
                Log.w(TAG, "relay register: ${c.responseCode}")
                return
            }
            val r = JSONObject(c.inputStream.bufferedReader().readText())
            PushRegistration.save(ctx, PushRegistration(relay, r.getString("id"), r.getString("secret")))
        } catch (e: Exception) {
            Log.w(TAG, "relay register: $e")
            return
        }
        val app = ctx.applicationContext as HammockApplication
        val m = app.session.model.value ?: return
        val h = app.session.host.value ?: return
        if (!m.isDemo) Push.register(ctx, m, h.id)
    }
}

/** Pushes from the relay: data-only, so each is decrypted and shown here (Notifications.show). */
class HammockMessagingService : FirebaseMessagingService() {
    override fun onMessageReceived(message: RemoteMessage) {
        val e = message.data["e"] ?: return
        Notifications.show(this, e)
    }

    override fun onNewToken(token: String) {
        PushSetup.registerToken(this, token)
    }
}
