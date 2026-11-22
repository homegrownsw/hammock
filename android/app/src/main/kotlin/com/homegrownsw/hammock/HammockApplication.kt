package com.homegrownsw.hammock

import android.app.Application
import android.net.ConnectivityManager
import android.net.Network
import android.os.Handler
import android.os.Looper
import androidx.lifecycle.DefaultLifecycleObserver
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.ProcessLifecycleOwner

/** The app: one Session (the paired computers and the one shown) for its life. */
class HammockApplication : Application() {
    lateinit var session: Session
        private set

    /** Whether the app is on screen: the computer holds pushes for a phone that's showing it (Presence). */
    @Volatile var foreground = false
        private set

    private val main = Handler(Looper.getMainLooper())
    /** Hangs up once the app's been away [AWAY_MS] (onStop), unless it's back first. */
    private val hangUp = Runnable { session.pause() }

    override fun onCreate() {
        super.onCreate()
        Notifications.createChannels(this)
        LiveNotification.createChannel(this)
        // Hooked before the first model is made. It connects only once the
        // app's on screen (onStart), not when a push wakes it to show a
        // notification: signed in, the computer takes you to be on your phone.
        session = Session(this) { m, h -> Notifications.hook(this, m, h) }
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                foreground = true
                main.removeCallbacks(hangUp)
                // Back from the background: connects again (a link that died
                // meanwhile is dialled at once, one still up checked), and the
                // computer hears the phone is showing it (it holds pushes then).
                session.resume()
                session.model.value?.reportPresence(emptyList(), true)
                // An APK's own updates (UpdateCheck), at most once a day.
                UpdateCheck.checkIfDue(this@HammockApplication)
            }

            override fun onStop(owner: LifecycleOwner) {
                foreground = false
                session.model.value?.reportPresence(emptyList(), false)
                // A moment away (another app, then back) keeps the link.
                main.postDelayed(hangUp, AWAY_MS)
            }
        })
        // On another network (Wi‑Fi to mobile data), the old link may look up
        // but carry nothing: checked at once while the app's in use.
        getSystemService(ConnectivityManager::class.java)?.registerDefaultNetworkCallback(object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) = changed()
            override fun onLost(network: Network) = changed()
            private fun changed() {
                main.post { if (foreground) session.model.value?.checkConnection() }
            }
        })
        PushSetup.start(this)
    }

    companion object {
        /** How long the app can be away before it hangs up. */
        const val AWAY_MS = 30_000L
    }
}
