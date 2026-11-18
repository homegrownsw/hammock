package com.homegrownsw.hammock.ui

import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Cloud
import androidx.compose.material.icons.filled.Computer
import androidx.compose.material.icons.filled.DesktopMac
import androidx.compose.material.icons.filled.DesktopWindows
import androidx.compose.material.icons.filled.Dns
import androidx.compose.material.icons.filled.Home
import androidx.compose.material.icons.filled.LaptopMac
import androidx.compose.material.icons.filled.Memory
import androidx.compose.material.icons.filled.Storage
import androidx.compose.material.icons.filled.Work
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.foundation.layout.size
import hammock.v1.Hammock.Computer

/** A kind of computer's icon (the iPhone's Computer.symbol(kind:os:), in Material's). */
fun computerIcon(kind: String, os: String = ""): ImageVector = when (kind) {
    "laptop" -> Icons.Default.LaptopMac
    "studio", "mini" -> MacMini
    "tower" -> Icons.Default.Computer
    "server" -> Icons.Default.Dns
    else -> if (os == "linux") Icons.Default.Dns else Icons.Default.DesktopMac
}

/** The SF Symbol a computer's icon names, as a Material icon, else its kind's. */
private fun symbolIcon(c: Computer): ImageVector = when (c.icon) {
    "laptopcomputer", "macbook" -> Icons.Default.LaptopMac
    "desktopcomputer", "display", "display.2" -> Icons.Default.DesktopMac
    "macmini", "macstudio" -> MacMini
    "macpro.gen3", "macpro.gen2", "macpro.gen1", "pc" -> Icons.Default.Computer
    "server.rack", "macpro.gen3.server", "xserve" -> Icons.Default.Dns
    "externaldrive", "internaldrive" -> Icons.Default.Storage
    "cpu", "memorychip" -> Icons.Default.Memory
    "terminal", "apple.terminal" -> Icons.Outlined.Terminal
    "house" -> Icons.Default.Home
    "building.2", "briefcase" -> Icons.Default.Work
    "cloud" -> Icons.Default.Cloud
    else -> if (c.kind == "desktop" && c.os != "macos") Icons.Default.DesktopWindows else computerIcon(c.kind, c.os)
}

/** A computer's badge colour ("#RRGGBB"), else null: drawn in the text's. */
fun Computer.tintColor(): Color? =
    tint.takeIf { it.length == 7 && it.startsWith("#") }?.substring(1)?.toLongOrNull(16)?.let { Color(0xFF000000 or it) }

/** A computer's icon: its emoji, or its symbol in its colour (the iPhone's ComputerIcon). */
@Composable
fun ComputerIcon(c: Computer, size: Dp = 14.dp, color: Color? = null, modifier: Modifier = Modifier) {
    val emoji = c.icon.takeIf { s -> s.isNotEmpty() && s.any { it.code > 127 } }
    if (emoji != null) {
        Text(emoji, fontSize = (size.value * 0.92f).sp, lineHeight = (size.value).sp, modifier = modifier)
    } else {
        Icon(symbolIcon(c), null, modifier.size(size + 2.dp), tint = color ?: c.tintColor() ?: LocalContentColor.current)
    }
}

/** A Mac mini or Studio: a low box (Material has none). */
val MacMini: ImageVector by lazy {
    ImageVector.Builder("MacMini", 24.dp, 24.dp, 24f, 24f).apply {
        addPath(
            androidx.compose.ui.graphics.vector.PathParser()
                .parsePathString("M5 9h14a2 2 0 0 1 2 2v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4a2 2 0 0 1 2-2zm0 2v4h14v-4zm2 1.25h3v1.5H7z").toNodes(),
            fill = androidx.compose.ui.graphics.SolidColor(Color.Black),
            pathFillType = androidx.compose.ui.graphics.PathFillType.EvenOdd,
        )
    }.build()
}
