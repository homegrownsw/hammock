package com.homegrownsw.hammock.terminal

import android.os.Bundle
import androidx.activity.ComponentActivity
import androidx.activity.compose.setContent
import androidx.activity.enableEdgeToEdge
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.systemBarsPadding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import java.util.concurrent.CopyOnWriteArrayList

/** A TerminalPane over a session fed by hand, recording what it sends. */
class TerminalTestActivity : ComponentActivity() {
    val session = TerminalSession.standalone("test", 60, 20)
    val sent = CopyOnWriteArrayList<ByteArray>()
    val resizes = CopyOnWriteArrayList<IntArray>()
    @Volatile var view: TerminalView? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        enableEdgeToEdge()
        super.onCreate(savedInstanceState)
        session.engine.write(DEMO.toByteArray())
        setContent {
            MaterialTheme(colorScheme = darkColorScheme()) {
                TerminalPane(
                    session = session,
                    onInput = { sent.add(it) },
                    onResize = { c, r, w, h -> resizes.add(intArrayOf(c, r, w, h)) },
                    readOnly = false,
                    modifier = Modifier.fillMaxSize().background(Color(0xFF1E1E20)).systemBarsPadding(),
                )
            }
        }
    }

    override fun onDestroy() {
        super.onDestroy()
        session.close()
    }

    companion object {
        private const val ESC = "\u001b"
        val DEMO = buildString {
            // A powerline prompt with Nerd Font icons.
            append("$ESC[48;5;31m$ESC[38;5;15m  ~/Developer/app $ESC[0m$ESC[38;5;31m$ESC[0m ")
            append("$ESC[1mbold$ESC[0m $ESC[3mitalic$ESC[0m $ESC[4munder$ESC[0m $ESC[9mstrike$ESC[0m $ESC[2mfaint$ESC[0m\r\n")
            for (i in 0..15) append("$ESC[48;5;${i}m  ")
            append("$ESC[0m\r\n")
            append("$ESC[31mred $ESC[32mgreen $ESC[33myellow $ESC[34mblue $ESC[35mmagenta $ESC[36mcyan$ESC[0m\r\n")
            append("$ESC[38;2;255;128;0mtruecolour orange$ESC[0m $ESC[7m inverse $ESC[0m\r\n")
            append("CJK 日本語 emoji 😀🎉 family 👨‍👩‍👧 flag 🇬🇧\r\n")
            append("┌──────┬──────┐\r\n│ box  │ draw │\r\n└──────┴──────┘\r\n")
            append("blocks ▀▄█▌▐░▒▓ ▖▗▘▙▚▛▜▝▞▟\r\n")
            append("icons     ok\r\n")
            append("$ESC[32m❯$ESC[0m ")
        }
    }
}
