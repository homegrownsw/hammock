package com.homegrownsw.hammock.terminal

import android.content.ClipboardManager
import android.content.Context
import android.graphics.Paint
import android.graphics.Rect
import android.opengl.GLSurfaceView
import android.text.InputType
import android.util.AttributeSet
import android.util.TypedValue
import android.view.ActionMode
import android.view.Choreographer
import android.view.GestureDetector
import android.view.HapticFeedbackConstants
import android.view.KeyCharacterMap
import android.view.KeyEvent
import android.view.Menu
import android.view.MenuItem
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import kotlin.math.abs
import kotlin.math.max
import kotlin.math.pow

/** The keys on the bar above the soft keyboard ([TerminalView.pressKey]). */
enum class TerminalKey(internal val keyCode: Int) {
    ESCAPE(KeyEvent.KEYCODE_ESCAPE),
    TAB(KeyEvent.KEYCODE_TAB),
    UP(KeyEvent.KEYCODE_DPAD_UP),
    DOWN(KeyEvent.KEYCODE_DPAD_DOWN),
    LEFT(KeyEvent.KEYCODE_DPAD_LEFT),
    RIGHT(KeyEvent.KEYCODE_DPAD_RIGHT),
    BACKSPACE(KeyEvent.KEYCODE_DEL),
}

/**
 * Draws a pane from its [TerminalSession] and takes keyboard input, as
 * TerminalUIView does on the iPhone (TerminalScreen.swift). Drag to scroll
 * back through the pane (or to send the wheel to programs that track the
 * mouse, or arrow keys to full-screen programs that don't), tap to click in
 * programs that track the mouse or else to show or hide the keyboard,
 * touch and hold to paste.
 *
 * The pane keeps its computer's size, and the font shrinks so it fits
 * (both ways, so the prompt stays in sight). While [active], the view
 * reports the grid that would fit at the chosen font size through
 * [onResize]: the app sends it to the computer when this phone drives the
 * pane.
 */
class TerminalView @JvmOverloads constructor(context: Context, attrs: AttributeSet? = null) : GLSurfaceView(context, attrs) {
    private var fonts = TerminalFonts.get(context.assets)
    private val density = resources.displayMetrics.density
    private val renderer = TerminalRenderer(fonts) { grid -> post { drew(grid) } }
    private var observation: AutoCloseable? = null

    /** The pane drawn here. Setting it doesn't close the old one. */
    var session: TerminalSession? = null
        set(value) {
            if (value === field) return
            observation?.close()
            field = value
            observation = value?.observe { requestRender() }
            value?.engine?.theme = theme
            lastGrid = null
            pushInputs()
        }

    /** Bytes for the pane's PTY (keys, paste, mouse). */
    var onInput: ((ByteArray) -> Unit)? = null

    /** The grid that fits at the chosen font size, and its cell in pixels (while [active]). */
    var onResize: ((cols: Int, rows: Int, cellWidthPx: Int, cellHeightPx: Int) -> Unit)? = null

    /** The cell's size in pixels as drawn (after shrinking to fit). */
    var onCellSize: ((widthPx: Int, heightPx: Int) -> Unit)? = null

    /** Typing here began (the keyboard came up, or a tap): the app focuses the pane. */
    var onFocus: (() -> Unit)? = null

    /** The sticky ctrl key changed (the accessory bar shows it). */
    var onCtrlChanged: ((Boolean) -> Unit)? = null

    /** Watching only: nothing typed, clicked, pasted or resized. */
    var readOnly = false
        set(value) {
            field = value
            isFocusableInTouchMode = !value
            if (value && hasFocus()) hideKeyboard()
        }

    /**
     * Showing on screen: fit the pane to the view (through [onResize]) now,
     * and whenever the view changes size (the keyboard opening or closing).
     */
    var active = false
        set(value) {
            val was = field
            field = value
            if (value && !was) reportGrid(force = true)
            // Hidden (behind the agent view, paged away): let go of the keyboard.
            if (!value && was && hasFocus()) {
                hideKeyboard()
                clearFocus()
            }
        }

    /** A picture (a tile): the font fits the width only, and the rows down to the cursor show. */
    var preview = false
        set(value) {
            field = value
            pushInputs()
        }

    var theme: TerminalTheme = TerminalTheme.Dark
        set(value) {
            if (value == field) return
            field = value
            session?.engine?.theme = value
            pushInputs()
        }

    /** The font size people chose, in pixels (by default 11 sp). */
    var fontSizePx: Float = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_SP, 11f, resources.displayMetrics)
        set(value) {
            if (value == field || value <= 0) return
            field = value
            pushInputs()
            reportGrid(force = false)
        }

    /** The font family people chose: "" is the bundled JetBrains Mono (TerminalFonts). */
    var fontFamily = ""
        set(value) {
            if (value == field) return
            field = value
            fonts = TerminalFonts.get(context.assets, value)
            pushInputs()
            reportGrid(force = false)
        }

    /** Extra row height, a fraction of the font's (negative: tighter). */
    var lineSpacing = 0f
        set(value) {
            if (value == field) return
            field = value
            pushInputs()
            reportGrid(force = false)
        }

    /**
     * Whether the soft keyboard is up (the Compose wrapper says, from
     * WindowInsets): the cursor is drawn solid while typing here.
     */
    var keyboardVisible = false
        set(value) {
            if (value == field) return
            field = value
            pushInputs()
        }

    /** Asks the keyboard for suggestions, voice typing and gesture typing (off: a plain one, as terminals want). */
    var imeSuggestions = false

    /** Ctrl from the accessory bar: applies to the next key typed. */
    var ctrlLatched = false
        private set

    /** Where the last frame put the grid (main thread). */
    var drawn: DrawnGrid? = null
        private set

    private val padding = 4 * density
    private var lastGrid: Pair<Int, Int>? = null
    private var lastCellSize: Pair<Int, Int>? = null
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG)
    private var connection: TerminalInputConnection? = null
    internal val inputMethodManager get() = context.getSystemService(InputMethodManager::class.java)

    init {
        setEGLContextClientVersion(3)
        preserveEGLContextOnPause = true
        setRenderer(renderer)
        renderMode = RENDERMODE_WHEN_DIRTY
        isFocusable = true
        isFocusableInTouchMode = true
        isHapticFeedbackEnabled = true
        contentDescription = "Terminal"
        pushInputs()
    }

    private fun pushInputs() {
        renderer.inputs = RenderInputs(
            session = session,
            theme = theme,
            fontPx = fontSizePx,
            minFontPx = 5 * density,
            paddingPx = padding,
            onePx = density,
            lineSpacing = lineSpacing,
            fonts = fonts,
            focused = hasFocus() && keyboardVisible && !readOnly,
            preview = preview,
        )
        requestRender()
    }

    private fun drew(grid: DrawnGrid) {
        drawn = grid
        val px = Math.round(grid.cellWidth).coerceAtLeast(1) to grid.cellHeight.toInt()
        if (px != lastCellSize) {
            lastCellSize = px
            onCellSize?.invoke(px.first, px.second)
        }
    }

    // MARK: Geometry

    /** The grid this view could show at the chosen font size (iOS's reportGrid). */
    fun reportGrid(force: Boolean) {
        if (readOnly || preview) return
        val fn = onResize ?: return
        if (!(active || hasFocus() || force) || !isAttachedToWindow || width <= 0 || height <= 0) return
        val m = CellMetrics.measure(paint, fonts, fontSizePx, density, lineSpacing)
        val cols = max(((width - padding * 2) / m.cellWidth).toInt(), 10)
        val rows = max(((height - padding * 2) / m.cellHeight).toInt(), 4)
        if (force || lastGrid != cols to rows) {
            lastGrid = cols to rows
            fn(cols, rows, m.cellWidthPx, m.cellHeight)
        }
    }

    override fun onSizeChanged(w: Int, h: Int, oldw: Int, oldh: Int) {
        super.onSizeChanged(w, h, oldw, oldh)
        post { reportGrid(force = false) }
    }

    override fun onAttachedToWindow() {
        super.onAttachedToWindow()
        if (active) post { reportGrid(force = true) }
    }

    override fun onDetachedFromWindow() {
        stopMomentum()
        super.onDetachedFromWindow()
    }

    override fun onApplyWindowInsets(insets: WindowInsets): WindowInsets {
        keyboardVisible = insets.isVisible(WindowInsets.Type.ime())
        return super.onApplyWindowInsets(insets)
    }

    // MARK: Output

    private fun send(bytes: ByteArray) {
        if (readOnly || bytes.isEmpty()) return
        onInput?.invoke(bytes)
    }

    /**
     * Types [text] (from the keyboard): newlines become Return, and a
     * latched ctrl turns a letter into its control character. Returns
     * whether the line is over (a Return or a control character).
     */
    internal fun typeText(text: String): Boolean {
        var t = text.replace('\n', '\r')
        if (ctrlLatched && t.codePointCount(0, t.length) == 1) {
            val c = t.lowercase()[0]
            if (c in 'a'..'z' || c in "[\\]^_ ") {
                t = (if (c == ' ') 0 else c.code and 0x1F).toChar().toString()
                setCtrlLatched(false)
            }
        }
        session?.engine?.scrollToBottom()
        send(t.toByteArray())
        requestRender()
        return t.any { it == '\r' || it.code < 0x20 }
    }

    internal fun backspaces(n: Int) {
        if (n > 0) send(ByteArray(n) { 0x7F })
    }

    fun setCtrlLatched(on: Boolean) {
        if (on == ctrlLatched) return
        ctrlLatched = on
        onCtrlChanged?.invoke(on)
    }

    /** A key from the accessory bar. */
    fun pressKey(key: TerminalKey) {
        val engine = session?.engine ?: return
        if (key == TerminalKey.BACKSPACE) {
            connection?.dropLast()
            send(byteArrayOf(0x7F))
            return
        }
        connection?.forget()
        engine.encodeKey(key.keyCode)?.let(::send)
    }

    /** Pastes the clipboard's text (bracketed if the program asked for it). */
    fun paste() {
        val clip = context.getSystemService(ClipboardManager::class.java)?.primaryClip ?: return
        if (clip.itemCount == 0) return
        val text = clip.getItemAt(0).coerceToText(context)?.toString().orEmpty()
        val engine = session?.engine ?: return
        if (text.isEmpty() || readOnly) return
        connection?.forget()
        engine.scrollToBottom()
        send(engine.encodePaste(text))
    }

    // MARK: Keyboard

    fun showKeyboard() {
        if (readOnly) return
        requestFocus()
        inputMethodManager?.showSoftInput(this, 0)
        onFocus?.invoke()
        // Typing from the phone makes it the pane's driver: size the PTY to fit.
        reportGrid(force = true)
    }

    fun hideKeyboard() {
        inputMethodManager?.hideSoftInputFromWindow(windowToken, 0)
    }

    private val keyboardShown get() = rootWindowInsets?.isVisible(WindowInsets.Type.ime()) ?: keyboardVisible

    override fun onCheckIsTextEditor() = !readOnly

    override fun onCreateInputConnection(outAttrs: EditorInfo): InputConnection? {
        if (readOnly) return null
        outAttrs.inputType = if (imeSuggestions) {
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        } else {
            // Visible password: no autocorrect, suggestions or capitals, and
            // keyboards commit each key as it's typed.
            InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_VISIBLE_PASSWORD or InputType.TYPE_TEXT_FLAG_NO_SUGGESTIONS
        }
        outAttrs.imeOptions = EditorInfo.IME_FLAG_NO_EXTRACT_UI or EditorInfo.IME_FLAG_NO_FULLSCREEN or
            EditorInfo.IME_ACTION_NONE or EditorInfo.IME_FLAG_NO_ENTER_ACTION
        outAttrs.initialSelStart = 0
        outAttrs.initialSelEnd = 0
        return TerminalInputConnection(this).also { connection = it }
    }

    override fun onFocusChanged(gainFocus: Boolean, direction: Int, previouslyFocusedRect: Rect?) {
        super.onFocusChanged(gainFocus, direction, previouslyFocusedRect)
        pushInputs()
    }

    /** Keys a terminal encodes itself rather than typing their text. */
    private fun special(code: Int) = when (code) {
        KeyEvent.KEYCODE_DPAD_UP, KeyEvent.KEYCODE_DPAD_DOWN, KeyEvent.KEYCODE_DPAD_LEFT, KeyEvent.KEYCODE_DPAD_RIGHT,
        KeyEvent.KEYCODE_ESCAPE, KeyEvent.KEYCODE_TAB, KeyEvent.KEYCODE_ENTER, KeyEvent.KEYCODE_NUMPAD_ENTER,
        KeyEvent.KEYCODE_DEL, KeyEvent.KEYCODE_FORWARD_DEL, KeyEvent.KEYCODE_MOVE_HOME, KeyEvent.KEYCODE_MOVE_END,
        KeyEvent.KEYCODE_PAGE_UP, KeyEvent.KEYCODE_PAGE_DOWN, KeyEvent.KEYCODE_INSERT -> true
        in KeyEvent.KEYCODE_F1..KeyEvent.KEYCODE_F12 -> true
        else -> false
    }

    override fun onKeyDown(keyCode: Int, event: KeyEvent): Boolean {
        val engine = session?.engine
        if (readOnly || engine == null || event.isSystem) return super.onKeyDown(keyCode, event)
        val mods = KeyMods.fromMetaState(event.metaState)
        val action = if (event.repeatCount > 0) KeyAction.REPEAT else KeyAction.PRESS
        if (special(keyCode)) {
            // Backspace takes a character off what the keyboard typed, so
            // its later revisions still line up; anything else moves on.
            if (keyCode == KeyEvent.KEYCODE_DEL && mods == 0) connection?.dropLast() else connection?.forget()
            engine.scrollToBottom()
            engine.encodeKey(keyCode, mods, null, 0, action)?.let(::send)
            return true
        }
        val plain = event.metaState and (KeyEvent.META_CTRL_MASK or KeyEvent.META_ALT_MASK or KeyEvent.META_META_MASK).inv()
        val unicode = event.getUnicodeChar(plain)
        if (unicode and KeyCharacterMap.COMBINING_ACCENT != 0) return super.onKeyDown(keyCode, event)
        val modified = mods and (KeyMods.CTRL or KeyMods.ALT or KeyMods.SUPER) != 0
        if (unicode == 0 && !modified) return super.onKeyDown(keyCode, event)
        val text = if (unicode != 0) String(Character.toChars(unicode)) else null
        connection?.forget()
        engine.scrollToBottom()
        val bytes = engine.encodeKey(keyCode, mods, text, event.getUnicodeChar(0), action)
            ?: text?.takeIf { !modified }?.toByteArray()
            ?: return super.onKeyDown(keyCode, event)
        send(bytes)
        return true
    }

    override fun onKeyMultiple(keyCode: Int, repeatCount: Int, event: KeyEvent): Boolean {
        val chars = event.characters
        if (readOnly || keyCode != KeyEvent.KEYCODE_UNKNOWN || chars.isNullOrEmpty()) return super.onKeyMultiple(keyCode, repeatCount, event)
        connection?.forget()
        typeText(chars)
        return true
    }

    // MARK: Touch

    private var scrollAccumulator = 0f
    private var momentum = 0f
    private var lastGlide = 0L
    /** This drag scrolls (it went vertical first); sideways ones are the pager's. */
    private var dragging = false
    private var decided = false

    private val gestures = GestureDetector(context, object : GestureDetector.SimpleOnGestureListener() {
        override fun onDown(e: MotionEvent): Boolean {
            stopMomentum()
            scrollAccumulator = 0f
            dragging = false
            decided = false
            return true
        }

        override fun onSingleTapUp(e: MotionEvent): Boolean {
            tapped(e.x, e.y)
            return true
        }

        override fun onLongPress(e: MotionEvent) {
            if (readOnly || dragging) return
            longPressed(e.x, e.y)
        }

        override fun onScroll(e1: MotionEvent?, e2: MotionEvent, distanceX: Float, distanceY: Float): Boolean {
            if (!decided) {
                decided = true
                dragging = abs(distanceY) > abs(distanceX)
                if (dragging) parent?.requestDisallowInterceptTouchEvent(true)
            }
            if (!dragging) return false
            // Like the Mac's wheel: a finger moving down shows earlier lines.
            scrollBy(-distanceY)
            return true
        }

        override fun onFling(e1: MotionEvent?, e2: MotionEvent, velocityX: Float, velocityY: Float): Boolean {
            if (!dragging) return false
            momentum = velocityY
            if (abs(momentum) > 200 * density) {
                lastGlide = 0L
                Choreographer.getInstance().postFrameCallback(glide)
            }
            return true
        }
    })

    override fun onTouchEvent(event: MotionEvent): Boolean {
        val handled = gestures.onTouchEvent(event)
        if (event.actionMasked == MotionEvent.ACTION_UP || event.actionMasked == MotionEvent.ACTION_CANCEL) {
            if (dragging) parent?.requestDisallowInterceptTouchEvent(false)
            dragging = false
        }
        return handled || super.onTouchEvent(event)
    }

    private val glide = object : Choreographer.FrameCallback {
        override fun doFrame(frameTimeNanos: Long) {
            if (lastGlide == 0L) {
                lastGlide = frameTimeNanos
                Choreographer.getInstance().postFrameCallback(this)
                return
            }
            val dt = ((frameTimeNanos - lastGlide) / 1e9f).coerceIn(0f, 0.1f)
            lastGlide = frameTimeNanos
            scrollBy(momentum * dt)
            momentum *= 0.12f.pow(dt) // decays like a scroll view's fling
            if (abs(momentum) < 40 * density) stopMomentum() else Choreographer.getInstance().postFrameCallback(this)
        }
    }

    private fun stopMomentum() {
        Choreographer.getInstance().removeFrameCallback(glide)
        momentum = 0f
    }

    /** Scrolls by [dy] pixels of finger travel, a row at a time. */
    private fun scrollBy(dy: Float) {
        val engine = session?.engine ?: return
        val ch = drawn?.cellHeight ?: CellMetrics.measure(paint, fonts, fontSizePx, density, lineSpacing).cellHeight.toFloat()
        scrollAccumulator += dy
        val lines = (scrollAccumulator / ch).toInt()
        if (lines == 0) return
        scrollAccumulator -= lines * ch
        when {
            !readOnly && engine.mouseTracking -> {
                // The program wants wheel events.
                val button = if (lines > 0) MouseButton.WHEEL_UP else MouseButton.WHEEL_DOWN
                repeat(abs(lines)) { sendMouse(MouseAction.PRESS, button, width / 2f, height / 2f) }
            }
            !readOnly && engine.isAlternateScreen -> {
                // Full-screen programs without mouse support: arrow keys.
                val key = if (lines > 0) KeyEvent.KEYCODE_DPAD_UP else KeyEvent.KEYCODE_DPAD_DOWN
                repeat(abs(lines)) { engine.encodeKey(key)?.let(::send) }
            }
            else -> {
                engine.scroll(-lines)
                requestRender()
            }
        }
    }

    private fun sendMouse(action: MouseAction, button: MouseButton, x: Float, y: Float) {
        val engine = session?.engine ?: return
        val d = drawn ?: return
        val ox = d.originX - padding
        val oy = d.originY - padding
        val g = MouseGeometry(width - ox, height - oy, d.cellWidth, d.cellHeight, padding)
        engine.encodeMouse(action, button, x - ox, y - oy, 0, g)?.let(::send)
    }

    private fun tapped(x: Float, y: Float) {
        stopMomentum()
        if (readOnly) return
        // Programs that track the mouse get the tap as a click.
        if (session?.engine?.mouseTracking == true) {
            sendMouse(MouseAction.PRESS, MouseButton.LEFT, x, y)
            sendMouse(MouseAction.RELEASE, MouseButton.LEFT, x, y)
            if (!keyboardShown) showKeyboard() else onFocus?.invoke()
            return
        }
        if (keyboardShown && hasFocus()) hideKeyboard() else showKeyboard()
    }

    private fun longPressed(x: Float, y: Float) {
        val clipboard = context.getSystemService(ClipboardManager::class.java)
        if (clipboard?.hasPrimaryClip() != true) return
        performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
        startActionMode(object : ActionMode.Callback2() {
            override fun onCreateActionMode(mode: ActionMode, menu: Menu): Boolean {
                menu.add(Menu.NONE, android.R.id.paste, Menu.NONE, android.R.string.paste)
                return true
            }

            override fun onPrepareActionMode(mode: ActionMode, menu: Menu) = false

            override fun onActionItemClicked(mode: ActionMode, item: MenuItem): Boolean {
                if (item.itemId == android.R.id.paste) paste()
                mode.finish()
                return true
            }

            override fun onDestroyActionMode(mode: ActionMode) {}

            override fun onGetContentRect(mode: ActionMode, view: View, outRect: Rect) {
                outRect.set(x.toInt(), y.toInt(), x.toInt() + 1, y.toInt() + 1)
            }
        }, ActionMode.TYPE_FLOATING)
    }
}
