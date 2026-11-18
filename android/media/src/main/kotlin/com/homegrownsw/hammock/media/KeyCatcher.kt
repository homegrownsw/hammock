package com.homegrownsw.hammock.media

import android.content.Context
import android.text.InputType
import android.view.KeyEvent
import android.view.View
import android.view.inputmethod.BaseInputConnection
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.viewinterop.AndroidView

/** What's typed into a page or a screen: text, or a key with no text of its own. */
sealed interface Typed {
    data class Text(val text: String) : Typed
    data object Enter : Typed
    data object Backspace : Typed
    /** A hardware keyboard's key that isn't text (arrows, Tab, Escape…), with its modifiers. */
    data class Key(val keyCode: Int, val meta: Int) : Typed
}

/**
 * Takes the keyboard while `active` and passes on what's typed, without a
 * text field of its own: the text is on the computer, so there's nothing here
 * to edit. The keyboard's told it's a visible password field so it commits
 * each character as typed (no word composing or suggestions to undo).
 */
@Composable
fun KeyCatcher(active: Boolean, onTyped: (Typed) -> Unit, onDismissed: () -> Unit, modifier: Modifier = Modifier) {
    AndroidView(
        factory = { ctx -> CatcherView(ctx) },
        modifier = modifier,
        update = { v ->
            v.onTyped = onTyped
            v.onDismissed = onDismissed
            v.setActive(active)
        },
    )
}

internal class CatcherView(context: Context) : View(context) {
    var onTyped: (Typed) -> Unit = {}
    var onDismissed: () -> Unit = {}
    private var active = false
    private val imm get() = context.getSystemService(InputMethodManager::class.java)

    init {
        isFocusable = true
        isFocusableInTouchMode = true
    }

    fun setActive(on: Boolean) {
        if (on == active) return
        active = on
        if (on) {
            requestFocus()
            post { imm?.showSoftInput(this, 0) }
        } else {
            imm?.hideSoftInputFromWindow(windowToken, 0)
            clearFocus()
        }
    }

    override fun onCheckIsTextEditor() = true

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection {
        outAttrs.inputType = InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI or EditorInfo.IME_FLAG_NO_FULLSCREEN or EditorInfo.IME_ACTION_NONE
        return Connection()
    }

    /** The keyboard going away (Back while it's up) ends typing. */
    override fun onKeyPreIme(keyCode: Int, event: KeyEvent): Boolean {
        if (keyCode == KeyEvent.KEYCODE_BACK && event.action == KeyEvent.ACTION_UP && active) {
            active = false
            onDismissed()
        }
        return super.onKeyPreIme(keyCode, event)
    }

    /** A hardware keyboard's keys reach the focused view directly. */
    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        if (!active) return super.onKeyDown(keyCode, event)
        when (keyCode) {
            KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER -> onTyped(Typed.Enter)
            KeyEvent.KEYCODE_DEL -> onTyped(Typed.Backspace)
            else -> {
                val ch = event.getUnicodeChar(event.metaState and (KeyEvent.META_SHIFT_MASK or KeyEvent.META_CAPS_LOCK_ON))
                val withMods = event.metaState and (KeyEvent.META_CTRL_MASK or KeyEvent.META_ALT_MASK or KeyEvent.META_META_MASK) != 0
                if (ch != 0 && !withMods) onTyped(Typed.Text(String(Character.toChars(ch))))
                else onTyped(Typed.Key(keyCode, event.metaState))
            }
        }
        return true
    }

    private inner class Connection : BaseInputConnection(this, false) {
        /** Text being composed (a keyboard that composes anyway, or voice): what's been sent of it. */
        private var composing = ""

        override fun commitText(text: CharSequence, newCursorPosition: Int): Boolean {
            replaceComposing(text.toString())
            composing = ""
            return true
        }

        override fun setComposingText(text: CharSequence, newCursorPosition: Int): Boolean {
            replaceComposing(text.toString())
            composing = text.toString()
            return true
        }

        override fun finishComposingText(): Boolean {
            composing = ""
            return true
        }

        /** Sends the difference between what was composed and `text`: backspaces, then what's new. */
        private fun replaceComposing(text: String) {
            val common = composing.commonPrefixWith(text).length
            repeat(composing.length - common) { onTyped(Typed.Backspace) }
            val added = text.substring(common)
            if (added.isEmpty()) return
            // Enter as the key (forms submit on it), the rest as text.
            val parts = added.split('\n')
            parts.forEachIndexed { i, p ->
                if (p.isNotEmpty()) onTyped(Typed.Text(p))
                if (i < parts.size - 1) onTyped(Typed.Enter)
            }
        }

        override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
            // There's no text here, so most keyboards send Delete as a key
            // event (below); this is for the ones that don't.
            repeat(beforeLength.coerceIn(0, 64)) { onTyped(Typed.Backspace) }
            return true
        }

        override fun sendKeyEvent(event: KeyEvent): Boolean {
            if (event.action == KeyEvent.ACTION_DOWN) onKeyDown(event.keyCode, event)
            return true
        }
    }
}
