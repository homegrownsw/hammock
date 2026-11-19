package com.homegrownsw.hammock

import android.content.Intent
import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.ui.HammockMaterialTheme
import com.homegrownsw.hammock.ui.HammockTheme
import com.homegrownsw.hammock.ui.Root

/** Where a notification or a link asked to go, for Root to open once it can. */
data class OpenRequest(
    val hostID: String? = null, val paneID: String? = null, val needsYou: Boolean = false,
    /** A notification's answer, sent once the app's lock is open (Require Unlock). */
    val answer: NotificationAnswer? = null,
)

class MainActivity : androidx.fragment.app.FragmentActivity() {
    private var opening by mutableStateOf<OpenRequest?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val session = (application as HammockApplication).session
        handle(intent)
        if (savedInstanceState == null) debugExtras(intent) // (not again when it's only recreated: a rotation)
        setContent {
            val model by session.model.collectAsStateWithLifecycle()
            val themePref = rememberPref(Prefs.THEME, HammockTheme.SAME_AS_COMPUTER)
            val appearance = rememberPref(Prefs.APPEARANCE, "system")
            val ws = model?.workspace?.collectAsStateWithLifecycle()
            val theme = HammockTheme.resolve(themePref.value, ws?.value?.hostTheme)
            HammockMaterialTheme(theme, appearance.value) {
                AppLock {
                    Root(session, opening) { opening = null }
                }
            }
        }
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        handle(intent)
        debugExtras(intent)
    }

    /**
     * A debug build pairs straight from `adb shell am start … --es pair <link>`
     * (android/scripts/dev-daemon.sh), and opens into the demo with --ez sample
     * true (docs/debug-hooks.md), whether it's starting or already open.
     */
    private fun debugExtras(intent: Intent) {
        if (!BuildConfig.DEBUG) return
        val session = (application as HammockApplication).session
        intent.getStringExtra("pair")?.let { session.pair(it) }
        // A feed for the update check to try (UpdateCheck), as a release build would read its own.
        intent.getStringExtra("updateFeed")?.let { UpdateCheck.feedOverride = it; UpdateCheck.check(this) }
        if (intent.getBooleanExtra("sample", false)) {
            session.startDemo(com.homegrownsw.hammock.demo.DemoDaemon(
                longChat = intent.getBooleanExtra("longChat", false), newChat = intent.getBooleanExtra("newChat", false),
                macTheme = intent.getStringExtra("macTheme"), hammockKey = intent.getStringExtra("hammockKey"),
            ))
        }
    }

    private fun handle(intent: Intent?) {
        intent ?: return
        val session = (application as HammockApplication).session
        val data = intent.data
        if (intent.action == Intent.ACTION_VIEW && data?.scheme == "hammock") {
            // A link could come from anywhere: ask before pairing.
            session.offer(data.toString())
            return
        }
        val pane = intent.getStringExtra(Notifications.EXTRA_PANE)
        val host = intent.getStringExtra(Notifications.EXTRA_HOST)
        if (pane != null || intent.getBooleanExtra(Notifications.EXTRA_NEEDS_YOU, false)) {
            // (Not again when opened from Recents, which hands back the intent that started it.)
            val fresh = intent.flags and Intent.FLAG_ACTIVITY_LAUNCHED_FROM_HISTORY == 0
            opening = OpenRequest(host, pane, needsYou = pane == null, answer = if (fresh) NotificationAnswer.from(intent) else null)
        }
    }
}
