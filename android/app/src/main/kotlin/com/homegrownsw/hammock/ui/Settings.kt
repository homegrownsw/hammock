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
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.Remove
import androidx.compose.material.icons.filled.UnfoldMore
import androidx.compose.material3.IconButton
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.VerticalDivider
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.widthIn
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
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
import androidx.compose.ui.text.font.Font
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
import com.homegrownsw.hammock.agentview.AgentFonts
import com.homegrownsw.hammock.terminal.TerminalFonts

/** The phone's own settings (not the computer's), in the iPhone's sections and words. */
@Composable
fun SettingsScreen(session: Session, model: AppModel) {
    val ctx = LocalContext.current
    val w by model.workspace.collectAsStateWithLifecycle()
    var themePref by rememberPref(Prefs.THEME, HammockTheme.SAME_AS_COMPUTER)
    var appearance by rememberPref(Prefs.APPEARANCE, "system")
    var opensIn by rememberPref(Prefs.AGENT_TABS_OPEN_IN, "agent")
    var codeWrap by rememberPref(Prefs.CODE_WRAP, "prose")
    var textFont by rememberPref(Prefs.TEXT_FONT, "")
    var textSize by rememberPref(Prefs.TEXT_SIZE, DefaultSizes.TEXT.toString())
    var lineSpacing by rememberPref(Prefs.LINE_SPACING, DefaultSizes.LINE_SPACING.toString())
    var codeSize by rememberPref(Prefs.CODE_SIZE, DefaultSizes.CODE.toString())
    var termFont by rememberPref(Prefs.TERMINAL_FONT, "")
    var termSize by rememberPref(Prefs.TERMINAL_FONT_SIZE, DefaultSizes.TERMINAL.toString())
    var termSpacing by rememberPref(Prefs.TERMINAL_LINE_SPACING, "0")
    var editorFont by rememberPref(Prefs.EDITOR_FONT, "")
    val fonts = rememberAgentFonts()
    val terminalFont = rememberTerminalFont()
    val bundledMono = remember { runCatching { FontFamily(Font("fonts/JetBrainsMono-VF.ttf", ctx.assets)) }.getOrDefault(FontFamily.Monospace) }
    val monoFamily = { name: String -> monoFamily(name, bundledMono) }
    var attachSize by rememberPref(Prefs.ATTACHMENT_SIZE, "medium")
    var dictation by rememberBoolPref(Prefs.DICTATION, true)
    var lock by rememberBoolPref(Prefs.APP_LOCK, false)
    var lockAfter by rememberPref(Prefs.LOCK_AFTER, "0")
    val computerTheme = HammockTheme.resolve("", w.hostTheme)
    var acknowledgements by remember { mutableStateOf(false) }
    var pickingTheme by remember { mutableStateOf(false) }
    if (acknowledgements) {
        androidx.activity.compose.BackHandler { acknowledgements = false }
        AcknowledgementsScreen(onBack = { acknowledgements = false })
        return
    }
    if (pickingTheme) {
        androidx.activity.compose.BackHandler { pickingTheme = false }
        ThemePicker(themePref, computerTheme, HammockTheme.wallpaper(ctx), onBack = { pickingTheme = false }) { themePref = it }
        return
    }

    fun reRegister() {
        val h = session.host.value ?: return
        if (!model.isDemo) Push.register(ctx, model, h.id)
    }
    val dark = MaterialTheme.colorScheme.surface.luminance() < 0.5f
    val wallpaper = HammockTheme.wallpaper(ctx)
    val shownTheme = HammockTheme.resolve(themePref, null, wallpaper).takeIf { themePref.isNotEmpty() } ?: computerTheme

    DetailScaffold("Settings", onBack = null, large = true) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = padding.calculateTopPadding(), bottom = 24.dp)) {
            item {
                SectionTitle("Appearance")
                GroupedCard {
                    ValueRow("Theme", onClick = { pickingTheme = true }) {
                        Text(if (themePref.isEmpty()) "Same as Computer" else shownTheme.name, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        Swatch(shownTheme)
                        Chevron()
                    }
                    CardDivider()
                    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 12.dp)) {
                        val options = listOf("system" to "System", "light" to "Light", "dark" to "Dark")
                        options.forEachIndexed { i, (k, v) ->
                            SegmentedButton(appearance == k, { appearance = k }, SegmentedButtonDefaults.itemShape(i, options.size), icon = {}) { Text(v) }
                        }
                    }
                }
                Footer("The app's colour, your messages' and the terminal's. By default, your computer's theme.")
            }
            item {
                SectionTitle("Agent View")
                GroupedCard { Choice("Agent tabs open in", opensIn, listOf("agent" to "Agent View", "terminal" to "Terminal")) { opensIn = it } }
                Footer("Each session remembers your choice once you switch it.")
            }
            item {
                SectionTitle("Text")
                GroupedCard {
                    Choice("Font", textFont, AgentFonts.families, optionFont = { AgentFonts.fontFamily(it) }) { textFont = it }
                    CardDivider()
                    StepperRow("Size", fonts.size.toInt(), 12..28) { textSize = it.toString() }
                    CardDivider()
                    StepperRow("Line spacing", fonts.lineSpacing.toInt(), 0..12) { lineSpacing = it.toString() }
                    CardDivider()
                    StepperRow("Code size", fonts.codeSize.toInt(), 10..26) { codeSize = it.toString() }
                    CardDivider()
                    Choice("Wrap code", codeWrap, listOf("prose" to "Markdown, Text and Tools", "always" to "Every Block", "never" to "Never")) { codeWrap = it }
                }
            }
            item {
                SectionTitle("Preview")
                GroupedCard {
                    Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                        val bubble = shownTheme.bubble(dark)
                        Box(Modifier.fillMaxWidth(), contentAlignment = Alignment.CenterEnd) {
                            Text("Your messages", color = if (bubble.luminance() < 0.6f) Color.White else Color.Black,
                                modifier = Modifier.clip(RoundedCornerShape(16.dp)).background(bubble).padding(horizontal = 12.dp, vertical = 8.dp))
                        }
                        Text(buildAnnotatedString {
                            append("Here's the plan: "); pushStyle(SpanStyle(fontWeight = FontWeight.Bold)); append("two"); pop(); append(" small changes, then a test run.")
                        }, fontSize = fonts.size.sp, fontFamily = fonts.fontFamily, lineHeight = (fonts.size * 1.25f + fonts.lineSpacing).sp)
                        Text("go test ./internal/daemon/", fontFamily = FontFamily.Monospace, fontSize = fonts.codeSize.sp,
                            color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                }
            }
            item {
                SectionTitle("Attachments")
                GroupedCard { Choice("Picture size", attachSize, listOf("small" to "Small", "medium" to "Medium", "large" to "Large", "original" to "Actual Size")) { attachSize = it } }
                SectionTitle("Dictation")
                GroupedCard { SwitchRow("Dictation Button", dictation) { dictation = it } }
            }
            item {
                SectionTitle("Terminal")
                val term = rememberTerminalTheme(model)
                GroupedCard {
                    Choice("Font", termFont, listOf("" to TerminalFonts.BUNDLED) + TerminalFonts.systemFamilies, optionFont = monoFamily) { termFont = it }
                    CardDivider()
                    StepperRow("Size", terminalFont.size.toInt(), 7..18) { termSize = it.toString() }
                    CardDivider()
                    val pct = termSpacing.toIntOrNull() ?: 0
                    StepperRow("Line spacing", pct, -20..100, step = 5,
                        shown = if (pct == 0) "Normal" else "${if (pct > 0) "+" else ""}$pct%") { termSpacing = it.toString() }
                    Text("~/Developer/app \$ just test", Modifier.fillMaxWidth().background(Color(term.background or 0xFF000000.toInt())).padding(16.dp),
                        fontFamily = monoFamily(termFont), fontSize = terminalFont.size.sp, maxLines = 1,
                        color = Color(term.foreground or 0xFF000000.toInt()))
                }
                Footer("Smaller text fits more columns. Panes wider than your screen shrink to fit.")
            }
            item {
                SectionTitle("Editors and Diffs")
                GroupedCard {
                    Choice("Font", editorFont, listOf("" to "Same as Terminal", TerminalFonts.BUNDLED to TerminalFonts.BUNDLED) + TerminalFonts.systemFamilies,
                        optionFont = { if (it.isEmpty()) null else monoFamily(it) }) { editorFont = it }
                }
            }
            item {
                SectionTitle("Privacy")
                GroupedCard {
                    SwitchRow("Require Unlock", lock) { lock = it }
                    if (lock) {
                        CardDivider()
                        Choice("Lock after", lockAfter, listOf("0" to "Immediately", "60" to "1 Minute", "300" to "5 Minutes", "900" to "15 Minutes", "3600" to "1 Hour")) { lockAfter = it }
                    }
                }
                Footer("Ask for your fingerprint, face or screen lock when you come back to Hammock after the time above, and hide it in Recents.")
            }
            item {
                val agents = AgentCatalog.resolve(w.agentKinds).filter { it.usage && it.installed }
                if (agents.isNotEmpty()) {
                    SectionTitle("Usage Limits")
                    val prefs = Prefs.of(ctx)
                    GroupedCard {
                        agents.forEachIndexed { i, a ->
                            if (i > 0) CardDivider()
                            var on by remember(a.id) { mutableStateOf(prefs.getBoolean("usage.${a.id}", false)) }
                            SwitchRow(AgentCatalog.shortName(a.id, w.kinds), on) { on = it; prefs.edit().putBoolean("usage.${a.id}", it).apply() }
                        }
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
                GroupedCard {
                    for ((event, label) in labels) {
                        var on by remember(event) { mutableStateOf(prefs.getBoolean("notify.$event", true)) }
                        SwitchRow(label, on) { on = it; NotifyPrefs.set(ctx, event, it); reRegister() }
                        CardDivider()
                    }
                    ValueRow("Notification Settings", onClick = {
                        ctx.startActivity(Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS).putExtra(Settings.EXTRA_APP_PACKAGE, ctx.packageName))
                    }) { Chevron() }
                }
                val status = when {
                    !PushSetup.configured -> "not set up in this build"
                    PushRegistration.current(ctx) == null -> "registering"
                    else -> "on"
                }
                Footer("Sent while you're away from your computer. Push: $status. Encrypted on your computer; only this phone can read them.")
            }
            item {
                SectionTitle("About")
                GroupedCard {
                    ValueRow("Version") { Text(BuildConfig.VERSION_NAME, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    CardDivider()
                    if (com.homegrownsw.hammock.UpdateCheck.enabled) {
                        val update by com.homegrownsw.hammock.UpdateCheck.available.collectAsStateWithLifecycle()
                        val checking by com.homegrownsw.hammock.UpdateCheck.status.collectAsStateWithLifecycle()
                        val u = update
                        if (u != null) {
                            ValueRow("Update to ${u.version}", accent = true, onClick = { com.homegrownsw.hammock.UpdateCheck.download(ctx, u) }) {}
                        } else {
                            ValueRow("Check for Updates", accent = true, onClick = { com.homegrownsw.hammock.UpdateCheck.check(ctx) }) {
                                val s = when (checking) {
                                    com.homegrownsw.hammock.UpdateCheck.Status.Checking -> "Checking…"
                                    com.homegrownsw.hammock.UpdateCheck.Status.UpToDate -> "Up to date"
                                    com.homegrownsw.hammock.UpdateCheck.Status.Failed -> "Couldn't check"
                                    com.homegrownsw.hammock.UpdateCheck.Status.Idle -> ""
                                }
                                if (s.isNotEmpty()) Text(s, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                        }
                        CardDivider()
                    }
                    ValueRow("Hammock Documentation", accent = true, onClick = {
                        ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse("https://homegrownsw.com/hammock/docs/")))
                    }) {}
                    CardDivider()
                    ValueRow("Acknowledgements", onClick = { acknowledgements = true }) { Chevron() }
                }
            }
        }
    }
}

/** Settings ▸ Theme: Same as Computer, the wallpaper's colours (Android 12 and later), then every theme, each with its swatch. */
@Composable
private fun ThemePicker(pref: String, computer: HammockTheme, wallpaper: HammockTheme?, onBack: () -> Unit, set: (String) -> Unit) {
    DetailScaffold("Theme", onBack = onBack) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(top = padding.calculateTopPadding() + 12.dp, bottom = 24.dp)) {
            item {
                GroupedCard {
                    ThemeChoice("Same as Computer", computer.name, computer, pref.isEmpty()) { set(""); onBack() }
                }
                Footer("Your computer's theme, from Settings ▸ Appearance there.")
                Spacer(Modifier.size(12.dp))
                if (wallpaper != null) {
                    GroupedCard {
                        ThemeChoice(wallpaper.name, null, wallpaper, pref == HammockTheme.WALLPAPER) { set(HammockTheme.WALLPAPER); onBack() }
                    }
                    Spacer(Modifier.size(12.dp))
                }
            }
            cardItems(HammockTheme.all, key = { it.name }, dividerStart = 72.dp) { t ->
                ThemeChoice(t.name, null, t, pref == t.name) { set(t.name); onBack() }
            }
        }
    }
}

@Composable
private fun ThemeChoice(name: String, detail: String?, t: HammockTheme, on: Boolean, pick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(onClick = pick).padding(horizontal = 16.dp, vertical = 14.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(16.dp)) {
        Swatch(t)
        Column(Modifier.weight(1f)) {
            Text(name, style = MaterialTheme.typography.bodyLarge)
            if (detail != null) Text(detail, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        if (on) Icon(Icons.Default.Check, "Chosen", tint = MaterialTheme.colorScheme.primary)
    }
}

/** A theme as the iPhone draws it: its sidebar's colour, with its accent as a dot. */
@Composable
private fun Swatch(t: HammockTheme) {
    val dark = MaterialTheme.colorScheme.surface.luminance() < 0.5f
    Box(
        Modifier.size(width = 34.dp, height = 26.dp).clip(RoundedCornerShape(8.dp)).background(t.sidebar(dark))
            .border(1.dp, MaterialTheme.colorScheme.outlineVariant, RoundedCornerShape(8.dp)).padding(end = 7.dp),
        contentAlignment = Alignment.CenterEnd,
    ) { Box(Modifier.size(12.dp).clip(CircleShape).background(t.accent(dark))) }
}

private fun Color.luminance(): Float = 0.2126f * red + 0.7152f * green + 0.0722f * blue

/** A row with its label at the start and [value] at the end. */
@Composable
fun ValueRow(label: String, accent: Boolean = false, onClick: (() -> Unit)? = null, value: @Composable RowScope.() -> Unit) {
    Row(
        Modifier.fillMaxWidth().heightIn(min = 56.dp).let { if (onClick != null) it.clickable(onClick = onClick) else it }.padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge, color = if (accent) MaterialTheme.colorScheme.primary else Color.Unspecified)
        value()
    }
}

/**
 * A choice: its label, and what's chosen in the accent, opening a menu of the options (the iPhone's menu picker).
 * [optionFont] draws each option in a font of its own (a font setting's families).
 */
@Composable
fun Choice(label: String, value: String, options: List<Pair<String, String>>, optionFont: ((String) -> FontFamily?)? = null, set: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    ValueRow(label, onClick = { open = true }) {
        Box {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(options.firstOrNull { it.first == value }?.second ?: value, color = MaterialTheme.colorScheme.primary, maxLines = 1,
                    overflow = TextOverflow.Ellipsis, modifier = Modifier.widthIn(max = 220.dp))
                Icon(Icons.Default.UnfoldMore, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
            }
            DropdownMenu(open, { open = false }) {
                for ((k, v) in options) DropdownMenuItem(text = { Text(v, fontFamily = optionFont?.invoke(k)) }, trailingIcon = { if (k == value) Icon(Icons.Default.Check, null) },
                    onClick = { open = false; set(k) })
            }
        }
    }
}

@Composable
fun SwitchRow(label: String, on: Boolean, set: (Boolean) -> Unit) {
    Row(Modifier.fillMaxWidth().clickable { set(!on) }.padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(label, Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
        Switch(on, set)
    }
}

/** "Size: 16" (or [shown]), with − and + (the iPhone's stepper), [step] at a time. */
@Composable
private fun StepperRow(label: String, value: Int, range: IntRange, step: Int = 1, shown: String = "$value", set: (Int) -> Unit) {
    ValueRow("$label: $shown") {
        Row(Modifier.clip(CircleShape).background(MaterialTheme.colorScheme.surfaceContainerHighest), verticalAlignment = Alignment.CenterVertically) {
            IconButton(onClick = { set((value - step).coerceIn(range)) }, enabled = value > range.first) { Icon(Icons.Default.Remove, "Less") }
            VerticalDivider(Modifier.height(20.dp))
            IconButton(onClick = { set((value + step).coerceIn(range)) }, enabled = value < range.last) { Icon(Icons.Default.Add, "More") }
        }
    }
}

/** A terminal font's family for Compose: "" or its name is the bundled JetBrains Mono, from the terminal's assets. */
private fun monoFamily(name: String, bundled: FontFamily): FontFamily =
    if (name.isEmpty() || name == TerminalFonts.BUNDLED) bundled else AgentFonts.fontFamily(name) ?: FontFamily.Monospace
