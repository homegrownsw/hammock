package com.homegrownsw.hammock

import android.content.Context
import android.content.SharedPreferences
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.MutableState
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.ui.platform.LocalContext

/**
 * The phone's own settings (Settings): appearance, theme, text sizes,
 * what to be notified about. Plain SharedPreferences, read by views through
 * [rememberPref], which follows changes made elsewhere.
 */
object Prefs {
    const val FILE = "settings"
    fun of(ctx: Context): SharedPreferences = ctx.getSharedPreferences(FILE, Context.MODE_PRIVATE)

    // Keys, and their defaults.
    const val APPEARANCE = "appearance"         // system, light, dark
    const val DEFAULT_TAB = "defaultTab"        // sessions, groups: first in the bar, and where the app opens
    const val THEME = "theme"                   // "" (the computer's) or a theme's name
    const val AGENT_TABS_OPEN_IN = "agentTabsOpenIn" // agent, terminal
    const val TERMINAL_FONT = "terminalFont"    // "" (JetBrains Mono) or a system family
    const val TERMINAL_FONT_SIZE = "terminalFontSize"
    const val TERMINAL_LINE_SPACING = "terminalLineSpacing" // percent of the row's height, -20 to 100; 0 normal
    const val TEXT_FONT = "agentFont"           // "" (the system's) or a family (AgentFonts.families)
    const val TEXT_SIZE = "agentTextSize"
    const val LINE_SPACING = "agentLineSpacing"
    const val CODE_SIZE = "agentCodeSize"
    const val CODE_WRAP = "codeWrap"            // prose, always, never
    const val EDITOR_FONT = "editorFont"        // "" (the terminal's) or a family
    const val ATTACHMENT_SIZE = "attachmentSize" // small, medium, large, original
    const val DICTATION = "dictation"
    const val WARN_CLOSE_BUSY = "warnCloseBusyAgent" // ask before closing an agent at work (the Mac's confirm-close-busy-agent)
    const val APP_LOCK = "appLock"
    const val LOCK_AFTER = "lockAfter"          // seconds; 0 immediately
    const val USAGE_SHOWN = "usageShown"        // comma-separated agent ids; "-" none chosen yet
}

/** A setting as Compose state, kept in step with the preferences file. */
@Composable
fun rememberPref(key: String, default: String): MutableState<String> {
    val prefs = Prefs.of(LocalContext.current)
    val state = remember(key) { mutableStateOf(prefs.getString(key, default) ?: default) }
    DisposableEffect(key) {
        val l = SharedPreferences.OnSharedPreferenceChangeListener { p, k -> if (k == key) state.value = p.getString(key, default) ?: default }
        prefs.registerOnSharedPreferenceChangeListener(l)
        onDispose { prefs.unregisterOnSharedPreferenceChangeListener(l) }
    }
    return remember(key) {
        object : MutableState<String> {
            override var value: String
                get() = state.value
                set(v) { state.value = v; prefs.edit().putString(key, v).apply() }
            override fun component1() = value
            override fun component2(): (String) -> Unit = { value = it }
        }
    }
}

@Composable
fun rememberBoolPref(key: String, default: Boolean): MutableState<Boolean> {
    val s = rememberPref(key, default.toString())
    return remember(key) {
        object : MutableState<Boolean> {
            override var value: Boolean
                get() = s.value.toBooleanStrictOrNull() ?: default
                set(v) { s.value = v.toString() }
            override fun component1() = value
            override fun component2(): (Boolean) -> Unit = { value = it }
        }
    }
}

/** What to be notified about (Settings ▸ Notify Me When), as RegisterPush's events. */
object NotifyPrefs {
    fun events(ctx: Context): List<String> = Push.allEvents.filter { Prefs.of(ctx).getBoolean("notify.$it", true) }
    fun set(ctx: Context, event: String, on: Boolean) = Prefs.of(ctx).edit().putBoolean("notify.$event", on).apply()
}
