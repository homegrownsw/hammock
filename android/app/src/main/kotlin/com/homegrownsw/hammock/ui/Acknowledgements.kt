package com.homegrownsw.hammock.ui

import android.content.Context
import android.content.Intent
import android.net.Uri
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.ListItem
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.json.JSONObject

/**
 * One piece of third-party software the app ships, with its licence: a row of
 * Acknowledgements.json (internal/acknowledgements, made by `just
 * acknowledgements`), which the build copies into the app's assets.
 */
data class Acknowledgement(
    val name: String,
    val version: String,
    val url: String,
    /** SPDX ids, joined with " AND " when it has several. */
    val license: String,
    /** A family's members (AndroidX's artifacts, say): name to version. */
    val packages: List<Pair<String, String>>,
    val note: String,
    /** Its licence files' full text. */
    val text: String,
) {
    /** Its version, or how many packages it is. */
    val detail: String?
        get() = when {
            // A commit: its first 12 characters.
            version.length == 40 && version.all { it in "0123456789abcdefABCDEF" } -> version.take(12)
            version.isNotEmpty() -> version
            packages.size > 1 -> "${packages.size} packages"
            else -> null
        }

    /** The licence, readably: "MIT, Apache 2.0". */
    val licenseName: String
        get() = license.split(" AND ").joinToString(", ") {
            when (it) {
                "Apache-2.0" -> "Apache 2.0"
                "BSD-3-Clause" -> "BSD 3-Clause"
                "BSD-2-Clause" -> "BSD 2-Clause"
                "CC0-1.0" -> "CC0 1.0"
                "MPL-2.0" -> "MPL 2.0"
                "OFL-1.1" -> "OFL 1.1"
                "Unicode-3.0" -> "Unicode License v3"
                "Public-Domain" -> "Public domain"
                "LicenseRef-ML-Kit-Terms" -> "ML Kit Terms of Service"
                "LicenseRef-Android-SDK-License" -> "Android SDK License"
                else -> it
            }
        }

    /**
     * The text with its paragraphs' hard line breaks taken out, for a screen
     * narrower than the 80 columns it was written for, in paragraphs. Short
     * lines (titles, copyright lines) and indented or listed ones keep theirs.
     */
    fun reflowedParagraphs(): List<String> {
        val out = mutableListOf<String>()
        var paragraph = StringBuilder()
        fun flush() {
            if (paragraph.isNotEmpty()) out += paragraph.toString()
            paragraph = StringBuilder()
        }
        for (line in text.split("\n")) {
            val trimmed = line.trim()
            if (trimmed.isEmpty()) { flush(); continue }
            val listed = line.first().isWhitespace() || trimmed.startsWith("*") || trimmed.startsWith("-") || trimmed.startsWith("•") ||
                trimmed.first().isDigit() && trimmed.take(4).any { it == '.' || it == ')' }
            if (paragraph.isEmpty() || listed) { flush(); paragraph.append(if (listed) line.trimEnd() else trimmed) }
            else paragraph.append(' ').append(trimmed)
            // A short line ends its paragraph: a title, a name, an address.
            if (trimmed.length < 50) flush()
        }
        flush()
        return out
    }

    companion object {
        /** This app's name in Acknowledgements.json. */
        const val THIS_APP = "android"

        /**
         * The software in this app, by name: Acknowledgements.json's (every app's,
         * those in this one) and Acknowledgements-android.json's (only this one's).
         * Empty if they're missing. Reads large files: not on the main thread.
         */
        fun load(context: Context): List<Acknowledgement> =
            (read(context, "Acknowledgements.json") + read(context, "Acknowledgements-android.json"))
                // As the generator sorts them: by name, ignoring case and a leading @.
                .sortedBy { it.name.lowercase().removePrefix("@") }

        private fun read(context: Context, file: String): List<Acknowledgement> {
            val json = runCatching { context.assets.open(file).use { it.readBytes().decodeToString() } }.getOrNull()
                ?: return emptyList()
            val components = runCatching { JSONObject(json).getJSONArray("components") }.getOrNull() ?: return emptyList()
            return (0 until components.length()).map { components.getJSONObject(it) }
                .filter { c -> c.optJSONArray("apps")?.let { a -> (0 until a.length()).any { a.optString(it) == THIS_APP } } == true }
                .map { c ->
                    val pkgs = c.optJSONArray("packages")
                    Acknowledgement(
                        name = c.optString("name"),
                        version = c.optString("version"),
                        url = c.optString("url"),
                        license = c.optString("license"),
                        packages = pkgs?.let { p -> (0 until p.length()).map { p.getJSONObject(it).let { o -> o.optString("name") to o.optString("version") } } }
                            ?: emptyList(),
                        note = c.optString("note"),
                        text = c.optString("text"),
                    )
                }
        }
    }
}

/**
 * Settings ▸ Acknowledgements: the third-party software in the app, each
 * opening its licence.
 */
@Composable
fun AcknowledgementsScreen(onBack: () -> Unit) {
    val ctx = LocalContext.current
    val items by produceState<List<Acknowledgement>?>(null) {
        value = withContext(Dispatchers.IO) { Acknowledgement.load(ctx.applicationContext) }
    }
    var openName by rememberSaveable { mutableStateOf<String?>(null) }
    val open = items?.firstOrNull { it.name == openName }

    if (open != null) {
        BackHandler { openName = null }
        AcknowledgementText(open) { openName = null }
        return
    }
    BackHandler(onBack = onBack)
    DetailScaffold("Acknowledgements", onBack = onBack) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = padding) {
            items(items.orEmpty(), key = { it.name }) { item ->
                ListItem(
                    modifier = Modifier.clickable { openName = item.name },
                    headlineContent = { Text(item.name) },
                    supportingContent = { Text(listOfNotNull(item.licenseName, item.detail).joinToString(" · ")) },
                )
            }
            if (items != null) item {
                Footer("Hammock is open source, under the MIT licence, and is built with, or inspired by, this software. Each one's licence is above.")
            }
        }
    }
}

/** One component's licence: what it is, where it's from, and the text. */
@Composable
private fun AcknowledgementText(item: Acknowledgement, onBack: () -> Unit) {
    val ctx = LocalContext.current
    val paragraphs = remember(item) { item.reflowedParagraphs() }
    DetailScaffold(item.name, onBack = onBack) { padding ->
        LazyColumn(Modifier.fillMaxSize(), contentPadding = padding) {
            item {
                Column(Modifier.padding(horizontal = 20.dp, vertical = 12.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                    Text(item.name, style = MaterialTheme.typography.titleLarge, fontWeight = FontWeight.Bold)
                    SelectionContainer {
                        Text(listOfNotNull(item.licenseName, item.version.takeIf { it.isNotEmpty() }?.let { item.detail }).joinToString(" · "),
                            style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                    }
                    if (item.url.isNotEmpty()) {
                        Text(item.url.removePrefix("https://"), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.clickable { runCatching { ctx.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(item.url))) } })
                    }
                    if (item.note.isNotEmpty()) Text(item.note, style = MaterialTheme.typography.bodyMedium)
                    if (item.packages.size > 1) {
                        SelectionContainer {
                            Text(item.packages.joinToString("\n") { (n, v) -> "$n $v" }, style = MaterialTheme.typography.bodySmall,
                                fontFamily = FontFamily.Monospace, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                    HorizontalDivider(Modifier.padding(top = 4.dp))
                }
            }
            // Paragraph by paragraph: some run to hundreds of pages.
            items(paragraphs) { p ->
                SelectionContainer {
                    Text(p, Modifier.padding(horizontal = 20.dp, vertical = 4.dp), style = MaterialTheme.typography.bodySmall)
                }
            }
        }
    }
}
