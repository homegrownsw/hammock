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
import androidx.compose.material3.IconButton
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
import androidx.compose.ui.semantics.clearAndSetSemantics
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import kotlinx.coroutines.delay

/** What you've switched on a code block (wrapped, Markdown rendered), by what it holds, so it lasts as the row scrolls away and back. */
private object CodeBlockChoices {
    val wrap = mutableStateMapOf<String, Boolean>()
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
            SmallAction(Icons.AutoMirrored.Outlined.WrapText, if (wraps) "Don't wrap" else "Wrap", tint = if (wraps) c.accent else c.secondary) {
                CodeBlockChoices.wrap[key] = !wraps
            }
            SmallAction(if (copied) Icons.Outlined.Check else Icons.Outlined.ContentCopy, if (copied) "Copied" else "Copy") {
                clipboard.setText(AnnotatedString(code))
                copied = true
            }
        }
        val codeStyle = codeTextStyle()
        when {
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

@Composable
internal fun Gap(h: Dp) = Spacer(Modifier.size(h))
