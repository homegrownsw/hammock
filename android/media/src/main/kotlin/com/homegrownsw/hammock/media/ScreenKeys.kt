package com.homegrownsw.hammock.media

import android.os.SystemClock
import android.view.KeyEvent
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue

/**
 * The keys typed to a shared screen, and the modifiers for the next one (or
 * click): a modifier holds for one key, double-tapped it stays on (locked).
 * Sends ScreenInput's key and text ops.
 */
internal class ScreenKeys(
    private val send: (String, Map<String, Any?>) -> Unit,
    private val clock: () -> Long = { SystemClock.uptimeMillis() },
) {
    var mods by mutableStateOf(setOf<String>())
        private set
    var held by mutableStateOf(setOf<String>())
        private set
    /** The Mac's at its lock screen: typing goes as keys (its password field may take only those), not as text. */
    var locked = false
    private var lastModTap = "" to Long.MIN_VALUE / 2

    fun clearMods() {
        mods = held
    }

    fun tapMod(m: String) {
        val now = clock()
        val double = lastModTap.first == m && now - lastModTap.second < 350
        lastModTap = m to now
        when {
            double -> if (m in held) { held = held - m; mods = mods - m } else { held = held + m; mods = mods + m }
            m in mods -> { mods = mods - m; held = held - m }
            else -> mods = mods + m
        }
    }

    /** A key pressed and let go, with the modifiers held for it (and `extra`). */
    fun key(vk: Int, extra: Set<String> = emptySet()) {
        press(vk, mods + extra)
        clearMods()
    }

    private fun press(vk: Int, m: Set<String>) {
        val list = m.toList()
        send("key", mapOf("type" to "down", "vk" to vk, "mods" to list))
        send("key", mapOf("type" to "up", "vk" to vk, "mods" to list))
    }

    fun typed(t: Typed) {
        when (t) {
            Typed.Enter -> key(MacKeys.RETURN)
            Typed.Backspace -> key(MacKeys.DELETE)
            is Typed.Text -> text(t.text)
            is Typed.Key -> hardware(t.keyCode, t.meta)
        }
    }

    /** Text: the phone's clipboard, say, typed as is. */
    fun paste(text: String) {
        if (text.isNotEmpty()) send("text", mapOf("text" to text))
    }

    private fun text(text: String) {
        // With a modifier, the key itself (⌘C is C's key); else the text, whatever the Mac's layout.
        if (mods.isNotEmpty() && text.length == 1) {
            val c = text[0]
            MacKeys.codes[c.lowercaseChar()]?.let { vk ->
                key(vk, if (c != c.lowercaseChar()) setOf("shift") else emptySet())
                return
            }
        }
        if (locked) {
            // Key by key, with shift where the character needs it (a US layout), for a password field.
            for (c in text) {
                val lower = c.lowercaseChar()
                val vk = MacKeys.codes[lower]
                val shifted = MacKeys.shifted[c]
                when {
                    vk != null -> press(vk, if (c != lower) mods + "shift" else mods)
                    shifted != null -> press(shifted, mods + "shift")
                    else -> send("text", mapOf("text" to c.toString()))
                }
            }
            clearMods()
            return
        }
        send("text", mapOf("text" to text))
        clearMods()
    }

    /** A hardware keyboard's key with no text of its own (or with ⌃ ⌥ ⌘), as the Mac's. */
    private fun hardware(keyCode: Int, meta: Int) {
        val m = buildSet {
            if (meta and KeyEvent.META_CTRL_ON != 0) add("ctrl")
            if (meta and KeyEvent.META_ALT_ON != 0) add("alt")
            if (meta and KeyEvent.META_META_ON != 0) add("meta")
            if (meta and KeyEvent.META_SHIFT_ON != 0) add("shift")
        }
        val vk = MacKeys.fromAndroid(keyCode) ?: return
        key(vk, m)
    }
}

/** Mac virtual key codes (kVK_*), as on a US keyboard. */
internal object MacKeys {
    val codes: Map<Char, Int> = buildMap {
        putAll(mapOf(' ' to 49, '-' to 27, '=' to 24, '[' to 33, ']' to 30, '\\' to 42, ';' to 41, '\'' to 39, ',' to 43, '.' to 47, '/' to 44, '`' to 50))
        val letters = "abcdefghijklmnopqrstuvwxyz"
        val letterCodes = intArrayOf(0, 11, 8, 2, 14, 3, 5, 4, 34, 38, 40, 37, 46, 45, 31, 35, 12, 15, 1, 17, 32, 9, 13, 7, 16, 6)
        letters.forEachIndexed { i, c -> put(c, letterCodes[i]) }
        val digitCodes = intArrayOf(29, 18, 19, 20, 21, 23, 22, 26, 28, 25)
        for (i in 0..9) put('0' + i, digitCodes[i])
    }

    /** Characters typed with shift on a US keyboard, to the key they're on. */
    val shifted: Map<Char, Int> = buildMap {
        val pairs = listOf('!' to '1', '@' to '2', '#' to '3', '$' to '4', '%' to '5', '^' to '6', '&' to '7', '*' to '8', '(' to '9', ')' to '0',
            '_' to '-', '+' to '=', '{' to '[', '}' to ']', '|' to '\\', ':' to ';', '"' to '\'', '<' to ',', '>' to '.', '?' to '/', '~' to '`')
        for ((s, b) in pairs) codes[b]?.let { put(s, it) }
    }

    const val RETURN = 0x24
    const val DELETE = 0x33
    const val ESCAPE = 53
    const val TAB = 48
    const val LEFT = 123
    const val RIGHT = 124
    const val DOWN = 125
    const val UP = 126
    const val FORWARD_DELETE = 117
    const val HOME = 115
    const val END = 119
    const val PAGE_UP = 116
    const val PAGE_DOWN = 121
    val FUNCTION = intArrayOf(122, 120, 99, 118, 96, 97, 98, 100, 101, 109, 103, 111)

    fun fromAndroid(keyCode: Int): Int? = when (keyCode) {
        KeyEvent.KEYCODE_ESCAPE -> ESCAPE
        KeyEvent.KEYCODE_TAB -> TAB
        KeyEvent.KEYCODE_DPAD_LEFT -> LEFT
        KeyEvent.KEYCODE_DPAD_RIGHT -> RIGHT
        KeyEvent.KEYCODE_DPAD_DOWN -> DOWN
        KeyEvent.KEYCODE_DPAD_UP -> UP
        KeyEvent.KEYCODE_FORWARD_DEL -> FORWARD_DELETE
        KeyEvent.KEYCODE_MOVE_HOME -> HOME
        KeyEvent.KEYCODE_MOVE_END -> END
        KeyEvent.KEYCODE_PAGE_UP -> PAGE_UP
        KeyEvent.KEYCODE_PAGE_DOWN -> PAGE_DOWN
        KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> RETURN
        KeyEvent.KEYCODE_DEL -> DELETE
        KeyEvent.KEYCODE_SPACE -> codes[' ']
        KeyEvent.KEYCODE_MINUS -> codes['-']
        KeyEvent.KEYCODE_EQUALS -> codes['=']
        KeyEvent.KEYCODE_LEFT_BRACKET -> codes['[']
        KeyEvent.KEYCODE_RIGHT_BRACKET -> codes[']']
        KeyEvent.KEYCODE_BACKSLASH -> codes['\\']
        KeyEvent.KEYCODE_SEMICOLON -> codes[';']
        KeyEvent.KEYCODE_APOSTROPHE -> codes['\'']
        KeyEvent.KEYCODE_COMMA -> codes[',']
        KeyEvent.KEYCODE_PERIOD -> codes['.']
        KeyEvent.KEYCODE_SLASH -> codes['/']
        KeyEvent.KEYCODE_GRAVE -> codes['`']
        in KeyEvent.KEYCODE_A..KeyEvent.KEYCODE_Z -> codes['a' + (keyCode - KeyEvent.KEYCODE_A)]
        in KeyEvent.KEYCODE_0..KeyEvent.KEYCODE_9 -> codes['0' + (keyCode - KeyEvent.KEYCODE_0)]
        in KeyEvent.KEYCODE_F1..KeyEvent.KEYCODE_F12 -> FUNCTION[keyCode - KeyEvent.KEYCODE_F1]
        else -> null
    }
}
