package com.homegrownsw.hammock.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp

// The iPhone's inset grouped lists: each section's rows on one rounded
// surface, set in from the screen's edges, with a divider between rows
// (from where their text starts), and the section's name above it.

/** How far a card is set in from the screen's edges. */
val CardInset = 16.dp
private val CardRadius = 20.dp

/** The surface a section's rows sit on. */
@Composable
fun cardColor(): Color = MaterialTheme.colorScheme.surfaceContainer

/** A section's name above its card, with [trailing] (a See All, a +) at its end. */
fun LazyListScope.section(title: String, key: String = "h:$title", trailing: (@Composable RowScope.() -> Unit)? = null) {
    item(key = key, contentType = "header") { SectionHeader(title, trailing = trailing) }
}

@Composable
fun SectionHeader(title: String, modifier: Modifier = Modifier, trailing: (@Composable RowScope.() -> Unit)? = null) {
    Row(
        modifier.fillMaxWidth().padding(start = CardInset + 16.dp, end = CardInset + 4.dp, top = 22.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(title, style = MaterialTheme.typography.titleSmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = Modifier.weight(1f))
        trailing?.invoke(this)
    }
}

/** The shape of the [i]th of [n] rows in a card: rounded at the card's ends. */
fun cardShape(i: Int, n: Int): RoundedCornerShape {
    val top = if (i == 0) CardRadius else 0.dp
    val bottom = if (i == n - 1) CardRadius else 0.dp
    return RoundedCornerShape(top, top, bottom, bottom)
}

/** A row's place in its card: the card's surface, its corners, and the divider above it. */
@Composable
fun CardRow(i: Int, n: Int, dividerStart: Dp = 56.dp, content: @Composable () -> Unit) {
    Column(Modifier.padding(horizontal = CardInset).clip(cardShape(i, n)).background(cardColor())) {
        if (i > 0) HorizontalDivider(Modifier.padding(start = dividerStart, end = 16.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.7f))
        content()
    }
}

/** A section's rows as one card, lazily. */
fun <T> LazyListScope.cardItems(
    items: List<T>,
    key: (T) -> Any,
    dividerStart: Dp = 56.dp,
    content: @Composable (T) -> Unit,
) {
    val n = items.size
    items.forEachIndexed { i, t ->
        item(key = key(t)) { CardRow(i, n, dividerStart) { content(t) } }
    }
}

/** A card of rows, not in a lazy list (a sheet's, a settings page's). */
@Composable
fun GroupedCard(modifier: Modifier = Modifier, content: @Composable ColumnScope.() -> Unit) {
    Column(modifier.padding(horizontal = CardInset).fillMaxWidth().clip(RoundedCornerShape(CardRadius)).background(cardColor()), content = content)
}

/** The divider between a card's rows. */
@Composable
fun CardDivider(start: Dp = 16.dp) =
    HorizontalDivider(Modifier.padding(start = start, end = 16.dp), color = MaterialTheme.colorScheme.outlineVariant.copy(alpha = 0.7f))

/** A row that opens another screen, as the iPhone's chevron says. */
@Composable
fun Chevron(modifier: Modifier = Modifier) =
    Icon(Icons.AutoMirrored.Filled.KeyboardArrowRight, null, modifier.size(20.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant.copy(alpha = 0.6f))

/** Room at the end of a list. */
fun LazyListScope.endSpace(height: Dp = 24.dp) = item(key = "end") { Spacer(Modifier.size(height)) }

/** A button tinted with the accent (the iPhone's bordered buttons), for the quieter of a pair. */
@Composable
fun tintedButtonColors() = androidx.compose.material3.ButtonDefaults.filledTonalButtonColors(
    containerColor = MaterialTheme.colorScheme.primary.copy(alpha = 0.14f),
    contentColor = MaterialTheme.colorScheme.primary,
)

/** A ListItem on a card's surface (not its own). */
@Composable
fun cardListItemColors() = androidx.compose.material3.ListItemDefaults.colors(containerColor = Color.Transparent)
