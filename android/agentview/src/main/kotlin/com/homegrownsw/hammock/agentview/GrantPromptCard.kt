package com.homegrownsw.hammock.agentview

import androidx.compose.foundation.background
import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.widthIn
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.key
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.compose.ui.graphics.Color
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.PanTool
import androidx.compose.material.icons.outlined.Verified
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import android.view.HapticFeedbackConstants
import androidx.compose.ui.platform.LocalView
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.client.AppModel
import com.homegrownsw.hammock.client.Link
import com.homegrownsw.hammock.client.agentNote
import com.homegrownsw.hammock.client.canAllowAlways
import com.homegrownsw.hammock.client.headlineText
import com.homegrownsw.hammock.client.originText
import hammock.v1.Hammock.GrantRequest
import hammock.v1.Hammock.RespondGrant

/**
 * Hammock asking you to allow something an agent in this pane asked for (a
 * GrantRequest: adding an automation, searching past sessions, typing into
 * another pane), or that hmk run in its shell asked for: the whole of it, in
 * place of the message box, as a permission prompt of the agent's own shows
 * there; over the bottom of its terminal when that's showing ([GrantPromptOverlay]).
 * The same answers as Needs You's card. [queued]: the pane's other
 * requests waiting behind this one.
 */
@Composable
internal fun GrantPromptCard(model: AppModel, grant: GrantRequest, queued: Int, enabled: Boolean, maxHeight: Dp) {
    val c = LocalAgentColors.current
    val view = LocalView.current
    var answered by remember(grant.id) { mutableStateOf<RespondGrant.Decision?>(null) }
    val options = buildList {
        add("Allow once" to RespondGrant.Decision.DECISION_ALLOW_ONCE)
        if (grant.canAllowAlways) add("Always allow in this Group" to RespondGrant.Decision.DECISION_ALLOW_GROUP)
        add("Deny" to RespondGrant.Decision.DECISION_DENY)
    }
    Surface(
        shape = RoundedCornerShape(24.dp), tonalElevation = 3.dp, shadowElevation = 6.dp,
        modifier = Modifier.fillMaxWidth().heightIn(max = maxHeight),
    ) {
        Column(Modifier.verticalScroll(rememberScrollState()).padding(14.dp), verticalArrangement = Arrangement.spacedBy(12.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
                Icon(Icons.Outlined.PanTool, null, tint = c.warning, modifier = Modifier.size(16.dp))
                Text("Hammock needs your permission", style = MaterialTheme.typography.labelLarge, fontWeight = FontWeight.SemiBold, color = c.secondary)
                if (queued > 0) Text("· $queued more", style = MaterialTheme.typography.labelMedium, color = c.tertiary)
                Spacer(Modifier.weight(1f))
            }
            SelectionContainer { Text(grant.headlineText, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.SemiBold) }
            grant.originText?.let { Text(it, style = MaterialTheme.typography.bodyMedium, color = c.secondary) }
            grant.agentNote?.let { note ->
                // The agent's own words, marked as such (Hammock's are above).
                Column(
                    Modifier.fillMaxWidth().clip(RoundedCornerShape(10.dp)).background(c.fill).padding(10.dp),
                    verticalArrangement = Arrangement.spacedBy(4.dp),
                ) {
                    Text("The agent says", style = MaterialTheme.typography.labelMedium, fontWeight = FontWeight.SemiBold, color = c.secondary)
                    SelectionContainer { Text(note, style = MaterialTheme.typography.bodyMedium) }
                }
            }
            Column(verticalArrangement = Arrangement.spacedBy(6.dp)) {
                options.forEach { (label, decision) ->
                    // An icon, not a number: unlike the agent's own card, it takes no number keys.
                    OptionRow(
                        0, label, "", isOther = false, multi = false, ticked = false, picked = false,
                        spinning = answered == decision, enabled = enabled && answered == null,
                        icon = when (decision) {
                            RespondGrant.Decision.DECISION_DENY -> Icons.Outlined.Close
                            RespondGrant.Decision.DECISION_ALLOW_GROUP -> Icons.Outlined.Verified
                            else -> Icons.Outlined.Check
                        },
                    ) {
                        answered = decision
                        view.performHapticFeedback(if (decision == RespondGrant.Decision.DECISION_DENY) HapticFeedbackConstants.REJECT else HapticFeedbackConstants.CONFIRM)
                        model.respond(grant, decision)
                    }
                }
            }
        }
    }
}

/**
 * The pane's oldest request from Hammock, floating over the bottom of its
 * terminal (on top, not beside it: the terminal keeps its size, so its screen
 * doesn't reflow while you answer). Nothing when there's none, or when you
 * can't drive the pane ([canDrive]). [accent]: the app theme's.
 */
@Composable
fun GrantPromptOverlay(model: AppModel, paneID: String, canDrive: Boolean, accent: Color, modifier: Modifier = Modifier) {
    val workspace by model.workspace.collectAsStateWithLifecycle()
    val link by model.link.collectAsStateWithLifecycle()
    val grants = if (canDrive) workspace.grants.filter { it.requesterPaneId == paneID } else emptyList()
    val colors = rememberAgentColors(accent)
    BoxWithConstraints(modifier) {
        val height = maxHeight
        AnimatedVisibility(
            grants.isNotEmpty(), Modifier.align(Alignment.BottomCenter),
            enter = slideInVertically { it } + fadeIn(), exit = slideOutVertically { it } + fadeOut(),
        ) {
            val grant = grants.lastOrNull() ?: return@AnimatedVisibility
            CompositionLocalProvider(LocalAgentColors provides colors) {
                key(grant.id) {
                    Box(Modifier.widthIn(max = 600.dp).padding(12.dp)) {
                        // The oldest (new ones are added at the front).
                        GrantPromptCard(model, grant, queued = grants.size - 1, enabled = link == Link.Connected, maxHeight = height * 0.7f)
                    }
                }
            }
        }
    }
}
