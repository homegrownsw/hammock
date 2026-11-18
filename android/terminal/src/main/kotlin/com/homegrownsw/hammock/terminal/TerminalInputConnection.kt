package com.homegrownsw.hammock.terminal

import android.text.Editable
import android.text.Selection
import android.view.inputmethod.BaseInputConnection

/**
 * The soft keyboard's way in. Keyboards edit a document; a terminal only
 * takes keystrokes. So, as on the iPhone (TerminalScreen.swift's stand-in
 * document), the keyboard edits a line of its own: what's been typed since
 * the last Return or control key. After each edit, whatever changed at its
 * end goes to the terminal as backspaces and new text, so composing (IMEs,
 * gesture typing), autocorrect and dictation revising what it typed all
 * work, and the text arrives as it's typed rather than when a word is done.
 */
internal class TerminalInputConnection(private val view: TerminalView) : BaseInputConnection(view, true) {
    private val doc: Editable = Editable.Factory.getInstance().newEditable("")
    /** What the terminal has been sent of [doc]. */
    private var sent = ""
    private var batch = 0

    override fun getEditable(): Editable = doc

    override fun beginBatchEdit(): Boolean {
        batch++
        return true
    }

    override fun endBatchEdit(): Boolean {
        if (batch > 0) batch--
        if (batch == 0) sync()
        return batch > 0
    }

    private inline fun edit(fn: () -> Boolean): Boolean {
        val r = fn()
        // The caret stays at the end: the terminal's cursor is where typing goes.
        if (Selection.getSelectionEnd(doc) != doc.length || Selection.getSelectionStart(doc) != doc.length) Selection.setSelection(doc, doc.length)
        if (batch == 0) sync()
        return r
    }

    override fun commitText(text: CharSequence?, newCursorPosition: Int) = edit { super.commitText(text, newCursorPosition) }

    override fun setComposingText(text: CharSequence?, newCursorPosition: Int) = edit { super.setComposingText(text, newCursorPosition) }

    override fun setComposingRegion(start: Int, end: Int) = edit { super.setComposingRegion(start, end) }

    override fun finishComposingText() = edit { super.finishComposingText() }

    override fun setSelection(start: Int, end: Int) = edit { true }

    override fun deleteSurroundingText(beforeLength: Int, afterLength: Int): Boolean {
        // More than the line holds: the rest is the terminal's (a prompt the
        // keyboard can't see), deleted with backspaces.
        val extra = beforeLength - Selection.getSelectionStart(doc).coerceAtLeast(0)
        val r = edit { super.deleteSurroundingText(beforeLength, afterLength) }
        if (extra > 0) view.backspaces(extra)
        return r
    }

    override fun deleteSurroundingTextInCodePoints(beforeLength: Int, afterLength: Int): Boolean {
        val before = doc.substring(0, Selection.getSelectionStart(doc).coerceAtLeast(0))
        val have = before.codePointCount(0, before.length)
        val r = edit { super.deleteSurroundingTextInCodePoints(beforeLength, afterLength) }
        if (beforeLength > have) view.backspaces(beforeLength - have)
        return r
    }

    override fun performEditorAction(actionCode: Int): Boolean {
        view.typeText("\r")
        return true
    }

    override fun getTextAfterCursor(length: Int, flags: Int): CharSequence = ""

    override fun getCursorCapsMode(reqModes: Int): Int = 0

    /** Sends what changed at the end of the line since it was last sent. */
    private fun sync() {
        val now = doc.toString()
        if (now == sent) return
        var p = 0
        val n = min(now.length, sent.length)
        while (p < n && now[p] == sent[p]) p++
        // Never split a surrogate pair.
        if (p > 0 && Character.isHighSurrogate(sent.getOrElse(p - 1) { ' ' }) && p < sent.length) p--
        val removed = sent.codePointCount(p, sent.length)
        val added = now.substring(p)
        sent = now
        if (removed > 0) view.backspaces(removed)
        if (added.isNotEmpty() && view.typeText(added)) {
            forget()
        } else if (doc.length > 400 && getComposingSpanStart(doc) < 0) {
            // Only the end of a long line matters for revising it.
            doc.delete(0, doc.length - 400)
            sent = doc.toString()
            view.inputMethodManager?.updateSelection(view, doc.length, doc.length, -1, -1)
        }
    }

    private fun min(a: Int, b: Int) = if (a < b) a else b

    /** The line is someone else's now (a Return, a control key, the cursor moved): start a new one. */
    fun forget() {
        if (doc.isEmpty() && sent.isEmpty()) return
        removeComposingSpans(doc)
        doc.clear()
        sent = ""
        view.inputMethodManager?.updateSelection(view, 0, 0, -1, -1)
    }

    /** Takes the last character off the line, without sending anything. */
    fun dropLast() {
        if (doc.isEmpty()) return
        val end = doc.length
        val start = doc.toString().offsetByCodePoints(end, -1)
        doc.delete(start, end)
        sent = doc.toString()
    }
}
