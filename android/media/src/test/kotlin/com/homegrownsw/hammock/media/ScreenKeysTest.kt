package com.homegrownsw.hammock.media

import android.view.KeyEvent
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test

class ScreenKeysTest {
    private val sent = ArrayList<Pair<String, Map<String, Any?>>>()
    private var now = 1000L
    private val keys = ScreenKeys({ op, args -> sent += op to args }, clock = { now })

    /** The keys sent, as "vk+mods" for each press (down and up together). */
    private fun presses(): List<String> = sent.filter { it.first == "key" && it.second["type"] == "down" }.map { (_, a) ->
        @Suppress("UNCHECKED_CAST")
        val mods = (a["mods"] as List<String>).sorted()
        "${a["vk"]}" + mods.joinToString("") { "+$it" }
    }

    private fun texts() = sent.filter { it.first == "text" }.map { it.second["text"] }

    @Test fun textGoesAsTextWhateverTheMacsLayout() {
        keys.typed(Typed.Text("héllo"))
        assertEquals(listOf("héllo"), texts())
        assertTrue(presses().isEmpty())
    }

    @Test fun enterAndDeleteAreTheMacsKeys() {
        keys.typed(Typed.Enter)
        keys.typed(Typed.Backspace)
        assertEquals(listOf("36", "51"), presses())
        // Each a down and an up.
        assertEquals(4, sent.size)
        assertEquals("up", sent[1].second["type"])
    }

    @Test fun aModifierHoldsForOneKeyAndIsTheKeyItself() {
        keys.tapMod("meta")
        keys.typed(Typed.Text("c"))
        keys.typed(Typed.Text("c"))
        // ⌘C as C's key, then a plain c as text.
        assertEquals(listOf("8+meta"), presses())
        assertEquals(listOf("c"), texts())
        // A capital with a modifier: shift too.
        keys.tapMod("meta")
        keys.typed(Typed.Text("Z"))
        assertEquals("6+meta+shift", presses().last())
    }

    @Test fun doubleTappedAModifierStaysOnTillTappedAgain() {
        keys.tapMod("ctrl")
        now += 200
        keys.tapMod("ctrl")
        assertEquals(setOf("ctrl"), keys.held)
        keys.typed(Typed.Text("a"))
        keys.typed(Typed.Text("e"))
        assertEquals(listOf("0+ctrl", "14+ctrl"), presses())
        now += 1000
        keys.tapMod("ctrl")
        assertTrue(keys.mods.isEmpty() && keys.held.isEmpty())
    }

    @Test fun twoTapsFarApartTurnItOnThenOff() {
        keys.tapMod("alt")
        now += 1000
        keys.tapMod("alt")
        assertTrue(keys.mods.isEmpty() && keys.held.isEmpty())
    }

    @Test fun atTheLockScreenTypingGoesKeyByKeyWithShift() {
        keys.locked = true
        keys.typed(Typed.Text("aB1!é"))
        // a, shift-b, 1, shift-1 (!), and é (no key for it) as text.
        assertEquals(listOf("0", "11+shift", "18", "18+shift"), presses())
        assertEquals(listOf("é"), texts())
    }

    @Test fun atTheLockScreenAModifierIsLetGoAfter() {
        keys.locked = true
        keys.tapMod("shift")
        keys.typed(Typed.Text("ab"))
        keys.typed(Typed.Text("c"))
        // A modifier with one character is that key; then it's let go.
        assertEquals(listOf("0+shift", "11+shift", "8"), presses())
        assertTrue(keys.mods.isEmpty())
    }

    @Test fun aHardwareKeyWithModifiersIsTheMacsKey() {
        keys.typed(Typed.Key(KeyEvent.KEYCODE_COMMA, KeyEvent.META_META_ON))
        keys.typed(Typed.Key(KeyEvent.KEYCODE_TAB, KeyEvent.META_ALT_ON or KeyEvent.META_SHIFT_ON))
        keys.typed(Typed.Key(KeyEvent.KEYCODE_F5, 0))
        keys.typed(Typed.Key(KeyEvent.KEYCODE_DPAD_UP, 0))
        keys.typed(Typed.Key(KeyEvent.KEYCODE_DEL, KeyEvent.META_META_ON))
        assertEquals(listOf("43+meta", "48+alt+shift", "96", "126", "51+meta"), presses())
    }

    @Test fun pastingTypesTheText() {
        keys.paste("one\ntwo")
        keys.paste("")
        assertEquals(listOf("one\ntwo"), texts())
    }

    @Test fun macKeyCodes() {
        assertEquals(0, MacKeys.codes['a'])
        assertEquals(29, MacKeys.codes['0'])
        assertEquals(MacKeys.codes['/'], MacKeys.shifted['?'])
        assertEquals(122, MacKeys.fromAndroid(KeyEvent.KEYCODE_F1))
        assertEquals(MacKeys.codes['`'], MacKeys.fromAndroid(KeyEvent.KEYCODE_GRAVE))
    }
}
