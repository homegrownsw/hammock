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
data class OpenRequest(val hostID: String? = null, val paneID: String? = null, val needsYou: Boolean = false)

class MainActivity : androidx.fragment.app.FragmentActivity() {
    private var opening by mutableStateOf<OpenRequest?>(null)

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        val session = (application as HammockApplication).session
        handle(intent)
        // A debug build pairs straight from `adb shell am start … --es pair <link>` (android/scripts/dev-daemon.sh).
        if (BuildConfig.DEBUG) intent.getStringExtra("pair")?.let { session.pair(it) }
        // …and opens straight into the demo with --ez sample true (docs/debug-hooks.md).
        if (BuildConfig.DEBUG && intent.getBooleanExtra("sample", false)) {
            session.startDemo(com.homegrownsw.hammock.demo.DemoDaemon(
                longChat = intent.getBooleanExtra("longChat", false), newChat = intent.getBooleanExtra("newChat", false),
                macTheme = intent.getStringExtra("macTheme"), hammockKey = intent.getStringExtra("hammockKey"),
            ))
        }
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
            opening = OpenRequest(host, pane, needsYou = pane == null)
        }
    }
}
