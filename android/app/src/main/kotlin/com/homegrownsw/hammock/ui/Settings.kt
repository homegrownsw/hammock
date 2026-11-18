package com.homegrownsw.hammock.ui

import android.content.Intent
import android.net.Uri
import android.provider.Settings
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Check
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Slider
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.homegrownsw.hammock.BuildConfig
import com.homegrownsw.hammock.NotifyPrefs
import com.homegrownsw.hammock.Prefs
import com.homegrownsw.hammock.Push
import com.homegrownsw.hammock.PushRegistration
import com.homegrownsw.hammock.PushSetup
import com.homegrownsw.hammock.Session
import com.homegrownsw.hammock.client.AgentCatalog
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.rememberBoolPref
import com.homegrownsw.hammock.rememberPref

/** The phone's own settings (not the computer's). */
@Composable
fun SettingsScreen(session: Session, model: AppModel) {
    val ctx = LocalContext.current
    val w by model.workspace.collectAsStateWithLifecycle()
    var themePref by rememberPref(Prefs.THEME, HammockTheme.SAME_AS_COMPUTER)
    var appearance by rememberPref(Prefs.APPEARANCE, "system")
    var opensIn by rememberPref(Prefs.AGENT_TABS_OPEN_IN, "agent")
    var codeWrap by rememberPref(Prefs.CODE_WRAP, "prose")
    var textSize by rememberPref(Prefs.TEXT_SIZE, "16")
    var termSize by rememberPref(Prefs.TERMINAL_FONT_SIZE, "12")
    var attachSize by rememberPref(Prefs.ATTACHMENT_SIZE, "medium")
    var dictation by rememberBoolPref(Prefs.DICTATION, true)
    var lock by rememberBoolPref(Prefs.APP_LOCK, false)
    var lockAfter by rememberPref(Prefs.LOCK_AFTER, "0")
    val computerTheme = HammockTheme.resolve("", w.hostTheme)

    fun reRegister() {
        val h = session.host.value ?: return
        if (!model.isDemo) Push.register(ctx, model, h.id)
    }

    DetailScaffold("Settings", onBack = null) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = padding) {
            item {
                SectionTitle("Appearance")
                ThemeRow(themePref, computerTheme) { themePref = it }
                Choice("Appearance", appearance, listOf("system" to "System", "light" to "Light", "dark" to "Dark")) { appearance = it }
                Footer("The app's colour, your messages' and the terminal's. By default, your computer's theme.")
            }
            item {
                SectionTitle("Agent View")
                Choice("Agent tabs open in", opensIn, listOf("agent" to "Agent View", "terminal" to "Terminal")) { opensIn = it }
                Footer("Each session remembers your choice once you switch it.")
            }
            item {
                SectionTitle("Text")
                SliderRow("Size", textSize.toFloatOrNull() ?: 16f, 12f..28f) { textSize = it.toInt().toString() }
                Choice("Wrap code", codeWrap, listOf("prose" to "Markdown, Text and Tools", "always" to "Every Block", "never" to "Never")) { codeWrap = it }
            }
            item {
                SectionTitle("Attachments")
                Choice("Picture size", attachSize, listOf("small" to "Small", "medium" to "Medium", "large" to "Large", "original" to "Actual Size")) { attachSize = it }
                SectionTitle("Dictation")
                SwitchRow("Dictation Button", dictation) { dictation = it }
            }
            item {
                SectionTitle("Terminal")
                SliderRow("Font size", termSize.toFloatOrNull() ?: 12f, 7f..18f) { termSize = it.toInt().toString() }
                Text("~/Developer/app \$ just test", Modifier.padding(horizontal = 20.dp), fontFamily = FontFamily.Monospace,
                    fontSize = (termSize.toFloatOrNull() ?: 12f).sp)
                Footer("Smaller text fits more columns. Panes wider than your screen shrink to fit.")
            }
            item {
                SectionTitle("Privacy")
                SwitchRow("Require Unlock", lock) { lock = it }
                if (lock) Choice("Lock after", lockAfter, listOf("0" to "Immediately", "60" to "1 Minute", "300" to "5 Minutes", "900" to "15 Minutes", "3600" to "1 Hour")) { lockAfter = it }
                Footer("Ask for your fingerprint, face or screen lock when you come back to Hammock after the time above, and hide it in Recents.")
            }
            item {
                val agents = AgentCatalog.resolve(w.agentKinds).filter { it.usage && it.installed }
                if (agents.isNotEmpty()) {
                    SectionTitle("Usage Limits")
                    val prefs = Prefs.of(ctx)
                    for (a in agents) {
                        var on by remember(a.id) { mutableStateOf(prefs.getBoolean("usage.${a.id}", false)) }
                        SwitchRow(AgentCatalog.shortName(a.id, w.kinds), on) { on = it; prefs.edit().putBoolean("usage.${a.id}", it).apply() }
                    }
                    Footer("At the bottom of Sessions: how much of each limit window (5 hours, a week) is used, and when it resets, as your computer's agents last reported it.")
                }
            }
            item {
                SectionTitle("Notify Me When")
                val labels = listOf(
                    "join" to "Someone asks to join", "needs_input" to "An agent needs you", "finished" to "An agent finishes",
                    "errored" to "An agent hits an error", "program_needs_input" to "Another program needs you",
                    "program_finished" to "Another program finishes", "program_errored" to "Another program fails",
                )
                val prefs = Prefs.of(ctx)
                for ((event, label) in labels) {
                    var on by remember(event) { mutableStateOf(prefs.getBoolean("notify.$event", true)) }
                    SwitchRow(label, on) { on = it; NotifyPrefs.set(ctx, event, it); reRegister() }
                }
                ListItem(
                    modifier = Modifier.clickable {
                        ctx.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, ctx.packageName))
                    },
                    headlineContent = { Text("Notification Settings") },
                    supportingContent = { Text("Sounds, and which kinds show, in Android's settings") },
                )
                val status = when {
                    !PushSetup.configured -> "not set up in this build"
                    PushRegistration.current(ctx) == null -> "registering"
                    else -> "on"
                }
                Footer("Sent while you're away from your computer. Push: $status. Encrypted on your computer; only this phone can read them.")
            }
            item {
                SectionTitle("About")
                ListItem(headlineContent = { Text("Version") }, trailingContent = { Text(BuildConfig.VERSION_NAME) })
                ListItem(
                    modifier = Modifier.clickable { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://homegrownsw.com/hammock/docs/"))) },
                    headlineContent = { Text("Hammock Documentation") },
                )
            }
        }
    }
}

@Composable
private fun ThemeRow(pref: String, computer: HammockTheme, set: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val shown = if (pref.isEmpty()) "Same as Computer (${computer.name})" else pref
    Box {
        ListItem(
            modifier = Modifier.clickable { open = true },
            headlineContent = { Text("Theme") },
            supportingContent = { Text(shown) },
            trailingContent = { Swatch(HammockTheme.resolve(pref, null).takeIf { pref.isNotEmpty() } ?: computer) },
        )
        DropdownMenu(open, { open = false }) {
            DropdownMenuItem(text = { Text("Same as Computer") }, leadingIcon = { Swatch(computer) },
                trailingIcon = { if (pref.isEmpty()) Icon(Icons.Default.Check, null) }, onClick = { open = false; set("") })
            for (t in HammockTheme.all) {
                DropdownMenuItem(text = { Text(t.name) }, leadingIcon = { Swatch(t) },
                    trailingIcon = { if (pref == t.name) Icon(Icons.Default.Check, null) }, onClick = { open = false; set(t.name) })
            }
        }
    }
}

@Composable
private fun Swatch(t: HammockTheme) {
    val dark = MaterialTheme.colorScheme.surface.luminance() < 0.5f
    Box(Modifier.size(22.dp).clip(CircleShape).background(t.sidebar(dark)), contentAlignment = Alignment.Center) {
        Box(Modifier.size(11.dp).clip(CircleShape).background(t.accent(dark)))
    }
}

private fun Color.luminance(): Float = 0.2126f * red + 0.7152f * green + 0.0722f * blue

@Composable
fun Choice(label: String, value: String, options: List<Pair<String, String>>, set: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Box {
        ListItem(
            modifier = Modifier.clickable { open = true },
            headlineContent = { Text(label) },
            supportingContent = { Text(options.firstOrNull { it.first == value }?.second ?: value) },
        )
        DropdownMenu(open, { open = false }) {
            for ((k, v) in options) DropdownMenuItem(text = { Text(v) }, trailingIcon = { if (k == value) Icon(Icons.Default.Check, null) },
                onClick = { open = false; set(k) })
        }
    }
}

@Composable
fun SwitchRow(label: String, on: Boolean, set: (Boolean) -> Unit) {
    ListItem(
        modifier = Modifier.clickable { set(!on) },
        headlineContent = { Text(label) },
        trailingContent = { Switch(on, set) },
    )
}

@Composable
private fun SliderRow(label: String, value: Float, range: ClosedFloatingPointRange<Float>, set: (Float) -> Unit) {
    Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 6.dp)) {
        Row {
            Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
            Text("${value.toInt()} pt", color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Slider(value, set, valueRange = range, steps = (range.endInclusive - range.start).toInt() - 1)
    }
}
