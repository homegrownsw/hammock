package com.homegrownsw.hammock.ui

import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.CallMerge
import androidx.compose.material.icons.automirrored.filled.CallSplit
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.unit.dp
import com.homegrownsw.hammock.client.summary
import hammock.v1.Hammock.TabGit

/**
 * A tab's git mark: its branch's pull request (open green, merged purple,
 * closed red, as GitHub shows them), or a branch for a worktree. Status
 * colours, not the theme's: they mean the same as on GitHub whatever the theme.
 */
@Composable
fun GitMark(git: TabGit, quiet: Color) {
    val pr = git.hasPr() && git.pr.number > 0
    val tint = when (if (pr) git.pr.state else "") {
        "open" -> Color(0xFF2DA44E)
        "merged" -> Color(0xFF8250DF)
        "closed" -> ErrorColor
        else -> quiet.copy(alpha = 0.6f)
    }
    val icon = if (pr && git.pr.state == "merged") Icons.AutoMirrored.Filled.CallMerge else Icons.AutoMirrored.Filled.CallSplit
    Icon(icon, git.summary, Modifier.size(14.dp), tint = tint)
}
