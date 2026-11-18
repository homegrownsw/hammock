package com.homegrownsw.hammock

import android.app.Application
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

    override fun onCreate() {
        super.onCreate()
        Notifications.createChannels(this)
        session = Session(this)
        session.onModel = { m, h -> Notifications.hook(this, m, h) }
        ProcessLifecycleOwner.get().lifecycle.addObserver(object : DefaultLifecycleObserver {
            override fun onStart(owner: LifecycleOwner) {
                foreground = true
                // Back from the background: a link that died meanwhile is dialled again at once,
                // and the computer hears the phone is showing it (it holds pushes then).
                session.model.value?.reconnectNow()
                session.model.value?.reportPresence(emptyList(), true)
            }

            override fun onStop(owner: LifecycleOwner) {
                foreground = false
                session.model.value?.reportPresence(emptyList(), false)
            }
        })
        PushSetup.start(this)
    }
}
