package com.homegrownsw.hammock.agentview

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.WrapText
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Code
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Preview
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay

/** An agent's Markdown: prose as selectable text, code, tables and rules as blocks of their own. */
@Composable
fun MarkdownText(text: String, modifier: Modifier = Modifier, color: Color = Color.Unspecified) {
    val c = LocalAgentColors.current
    val runs = remember(text, c.md) { Markdown.runs(text, c.md) }
    Column(modifier.fillMaxWidth(), verticalArrangement = Arrangement.spacedBy(10.dp)) {
        for (r in runs) {
            when (r) {
                is Markdown.Run.Prose -> SelectionContainer {
                    Text(r.text, style = MaterialTheme.typography.bodyLarge, color = color)
                }
                is Markdown.Run.Block -> when (val b = r.block) {
                    is MdBlock.Code -> CodeBlock(b.text, b.language)
                    is MdBlock.Table -> MdTable(b)
                    MdBlock.Rule -> HorizontalDivider()
                    else -> {}
                }
            }
        }
    }
}

@Composable
private fun MdTable(t: MdBlock.Table) {
    val c = LocalAgentColors.current
    val cols = maxOf(t.header.size, t.rows.maxOfOrNull { it.size } ?: 0)
    val style = MaterialTheme.typography.bodyMedium
    Row(
        Modifier.fillMaxWidth().horizontalScroll(rememberScrollState())
            .background(c.fill, RoundedCornerShape(10.dp)).padding(12.dp),
        horizontalArrangement = Arrangement.spacedBy(16.dp),
    ) {
        // A column at a time, so each is as wide as its widest cell.
        for (col in 0 until cols) {
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                Text(Markdown.inline(t.header.getOrElse(col) { "" }, c.md), style = style, fontWeight = FontWeight.SemiBold)
                for (row in t.rows) {
                    SelectionContainer { Text(Markdown.inline(row.getOrElse(col) { "" }, c.md), style = style, maxLines = 4) }
                }
            }
        }
    }
}

/** What you've switched on a code block (wrapped, Markdown rendered), by what it holds, so it lasts as the row scrolls away and back. */
private object CodeBlockChoices {
    val wrap = mutableStateMapOf<String, Boolean>()
    val rendered = mutableStateMapOf<String, Boolean>()
}

private val proseLanguages = setOf("markdown", "md", "text", "txt", "plain", "plaintext")

/**
 * Code, highlighted, with its language, a wrap toggle and Copy. Unlabelled
 * blocks don't wrap by default (they're often trees and diagrams); prose
 * languages and tools' commands and output ([wrapsByDefault]) do.
 */
@Composable
fun CodeBlock(code: String, language: String = "", maxHeight: Dp = 420.dp, wrapsByDefault: Boolean = false) {
    val c = LocalAgentColors.current
    val lang = language.lowercase()
    val key = "$lang|${code.hashCode()}|${code.length}"
    val isMarkdown = lang == "markdown" || lang == "md"
    val rendered = isMarkdown && CodeBlockChoices.rendered[key] == true
    val wrapSetting = LocalAgentFonts.current.codeWrap
    val wraps = CodeBlockChoices.wrap[key] ?: when (wrapSetting) {
        "always" -> true
        "never" -> false
        else -> wrapsByDefault || lang in proseLanguages
    }
    val clipboard = LocalClipboardManager.current
    var copied by remember { mutableStateOf(false) }
    LaunchedEffect(copied) { if (copied) { delay(1200); copied = false } }
    Column(Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(c.fill)) {
        Row(Modifier.fillMaxWidth().padding(start = 12.dp, end = 4.dp, top = 4.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(language.ifEmpty { "code" }, style = MaterialTheme.typography.labelSmall, color = c.secondary, modifier = Modifier.weight(1f))
            if (isMarkdown) {
                SmallAction(if (rendered) Icons.Outlined.Code else Icons.Outlined.Preview, if (rendered) "Source" else "Preview") {
                    if (rendered) CodeBlockChoices.rendered.remove(key) else CodeBlockChoices.rendered[key] = true
                }
            }
            if (!rendered) {
                SmallAction(Icons.AutoMirrored.Outlined.WrapText, if (wraps) "Don't wrap" else "Wrap", tint = if (wraps) c.accent else c.secondary) {
                    CodeBlockChoices.wrap[key] = !wraps
                }
            }
            SmallAction(if (copied) Icons.Outlined.Check else Icons.Outlined.ContentCopy, if (copied) "Copied" else "Copy") {
                clipboard.setText(AnnotatedString(code))
                copied = true
            }
        }
        val codeStyle = codeTextStyle()
        when {
            rendered -> Column(Modifier.heightIn(max = maxHeight).verticalScroll(rememberScrollState()).padding(12.dp)) { MarkdownText(code) }
            wraps -> SelectionContainer {
                Text(
                    CodeHighlight.highlighted(code, language, c.md), style = codeStyle,
                    modifier = Modifier.fillMaxWidth().heightIn(max = maxHeight).verticalScroll(rememberScrollState()).padding(12.dp),
                )
            }
            else -> SelectionContainer {
                Text(
                    CodeHighlight.highlighted(code, language, c.md), style = codeStyle, softWrap = false,
                    modifier = Modifier.fillMaxWidth().heightIn(max = maxHeight).verticalScroll(rememberScrollState())
                        .horizontalScroll(rememberScrollState()).padding(12.dp),
                )
            }
        }
    }
}

/** Code at Settings' code size, times [scale] (the iPhone's fonts.code()). */
@Composable
internal fun codeTextStyle(scale: Float = 1f): TextStyle {
    val size = LocalAgentFonts.current.codeSize * scale
    return MaterialTheme.typography.bodyLarge.copy(fontFamily = FontFamily.Monospace, fontSize = size.sp, lineHeight = (size * 1.35f).sp)
}

@Composable
internal fun SmallAction(icon: androidx.compose.ui.graphics.vector.ImageVector, label: String, tint: Color = LocalAgentColors.current.secondary, onClick: () -> Unit) {
    Row(
        Modifier.clip(RoundedCornerShape(6.dp)).clickable(onClickLabel = label, onClick = onClick).padding(horizontal = 8.dp, vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(4.dp),
    ) {
        Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(14.dp))
        Text(label, style = MaterialTheme.typography.labelSmall, color = tint)
    }
}

/**
 * A unified diff, a line at a time, tinted green and red. [preview]: only
 * that many lines (and how many more there are), unscrolled, a tap away
 * from the rest.
 */
@Composable
fun DiffView(diff: String, preview: Int? = null, onTap: (() -> Unit)? = null) {
    val c = LocalAgentColors.current
    val all = remember(diff) { diff.split('\n').let { if (it.lastOrNull()?.isEmpty() == true) it.dropLast(1) else it } }
    val shown = all.take(preview ?: 600)
    val more = if (preview == null) 0 else all.size - shown.size
    val style = codeTextStyle(0.95f)
    BoxWithConstraints(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(c.fill)
            .then(if (onTap != null) Modifier.clickable(onClickLabel = "Show the whole change", onClick = onTap) else Modifier),
    ) {
        val min = maxWidth
        Column {
            val scroll = Modifier.horizontalScroll(rememberScrollState(), enabled = preview == null)
            val vertical = if (preview == null) Modifier.heightIn(max = 360.dp).verticalScroll(rememberScrollState()) else Modifier
            Column(vertical.then(scroll).padding(vertical = 8.dp).width(IntrinsicSize.Max).widthIn(min = min)) {
                for (line in shown) {
                    val bg = when {
                        line.startsWith("+") -> c.added.copy(alpha = 0.16f)
                        line.startsWith("-") -> c.removed.copy(alpha = 0.16f)
                        else -> Color.Transparent
                    }
                    Text(
                        line.ifEmpty { " " }, style = style, softWrap = false, maxLines = 1,
                        color = if (line.startsWith("@@")) c.secondary else MaterialTheme.colorScheme.onSurface,
                        overflow = TextOverflow.Clip,
                        modifier = Modifier.fillMaxWidth().background(bg).padding(horizontal = 10.dp),
                    )
                }
            }
            if (more > 0) {
                Text(
                    if (more == 1) "1 more line" else "$more more lines",
                    style = MaterialTheme.typography.labelSmall, color = c.secondary,
                    modifier = Modifier.padding(start = 10.dp, bottom = 8.dp),
                )
            }
        }
    }
}

@Composable
internal fun Gap(h: Dp) = Spacer(Modifier.size(h))
