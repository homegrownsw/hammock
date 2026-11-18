package com.homegrownsw.hammock

import android.content.Context

/**
 * This phone's registration with the push relay (relay/push): its id and
 * secret there, and the relay's address. Made once FCM gives the app a
 * token (HammockMessagingService), kept in the no-backup files.
 */
data class PushRegistration(val relayURL: String, val relayID: String, val secret: String) {
    companion object {
        private const val FILE = "push"

        fun current(ctx: Context): PushRegistration? {
            val p = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)
            val id = p.getString("relayID", null) ?: return null
            return PushRegistration(p.getString("relayURL", BuildConfig.PUSH_RELAY)!!, id, p.getString("secret", "")!!)
        }

        fun save(ctx: Context, r: PushRegistration?) {
            val e = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE).edit()
            if (r == null) e.clear() else e.putString("relayURL", r.relayURL).putString("relayID", r.relayID).putString("secret", r.secret)
            e.apply()
        }
    }
}
