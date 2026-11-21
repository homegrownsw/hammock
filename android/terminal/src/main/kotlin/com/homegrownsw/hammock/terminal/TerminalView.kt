package com.homegrownsw.hammock.terminal

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.graphics.Paint
import android.graphics.Rect
import android.opengl.GLSurfaceView
import android.os.Bundle
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
import android.view.accessibility.AccessibilityNodeInfo
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputConnection
import android.view.inputmethod.InputMethodManager
import kotlin.math.abs
import kotlin.math.hypot
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
 * touch and hold to select text (then drag its handles, and copy or paste
 * from its menu).
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
            observation = value?.observe {
                requestRender()
                // Output moves (or clears) a selection: its handles follow.
                if (selectionShown) post(selectionMoved)
            }
            if (selectionShown) post(selectionMoved)
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

    /**
     * Connected to the computer: while it isn't, keys are blocked rather than
     * kept, since what they'd do depends on a screen that may have moved on.
     */
    var linkUp = true

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
        stopAutoscroll()
        selectionMenu?.finish()
        super.onDetachedFromWindow()
    }

    override fun onApplyWindowInsets(insets: WindowInsets): WindowInsets {
        keyboardVisible = insets.isVisible(WindowInsets.Type.ime())
        return super.onApplyWindowInsets(insets)
    }

    // MARK: Output

    private fun send(bytes: ByteArray) {
        if (readOnly || !linkUp || bytes.isEmpty()) return
        onInput?.invoke(bytes)
    }

    /**
     * Types [text] (from the keyboard): newlines become Return, and a
     * latched ctrl turns a letter into its control character. Returns
     * whether the line is over (a Return or a control character).
     */
    internal fun typeText(text: String): Boolean {
        // Offline: dropped, and the keyboard's line forgotten with it.
        if (!linkUp) return true
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
        if (!linkUp) return
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
            if (dragging || !selectable) return
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
        // A selection handle caught, or a touch and hold dragging on: they
        // take the whole gesture (not the scrolling, nor the pager's swipe).
        if (event.actionMasked == MotionEvent.ACTION_DOWN && selectionShown) {
            handleAt(event.x, event.y)?.let { (edge, caretX, caretY) ->
                stopMomentum()
                handleDrag = HandleDrag(edge, event.x - caretX, event.y - caretY)
                parent?.requestDisallowInterceptTouchEvent(true)
                hideSelectionMenu()
                return true
            }
        }
        handleDrag?.let { drag ->
            when (event.actionMasked) {
                MotionEvent.ACTION_MOVE -> {
                    val x = event.x - drag.dx
                    val y = event.y - drag.dy
                    selectPoint = x to y
                    moveHandle(x, y)
                    updateAutoscroll(y)
                }
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    handleDrag = null
                    selectPoint = null
                    stopAutoscroll()
                    parent?.requestDisallowInterceptTouchEvent(false)
                    showSelectionMenu(null)
                }
            }
            return true
        }
        if (wordDragging) {
            when (event.actionMasked) {
                MotionEvent.ACTION_MOVE -> {
                    selectPoint = event.x to event.y
                    extendWord(event.x, event.y)
                    updateAutoscroll(event.y)
                }
                MotionEvent.ACTION_UP, MotionEvent.ACTION_CANCEL -> {
                    wordDragging = false
                    selectPoint = null
                    stopAutoscroll()
                    parent?.requestDisallowInterceptTouchEvent(false)
                    showSelectionMenu(event.x to event.y)
                }
            }
            gestures.onTouchEvent(event)
            return true
        }
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
                if (selectionShown) selectionMoved.run() // the selection scrolls with the text
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
        // A tap puts a selection away, and does nothing else.
        if (selectionShown || session?.engine?.selectionBounds() != null) {
            clearSelection()
            return
        }
        // And a menu without one (Select all, Paste).
        if (selectionMenu != null) {
            hideSelectionMenu(clearing = true)
            return
        }
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

    // MARK: Selection

    /** Touch and hold selects text (a tile's screen doesn't). */
    var selectable = true

    /**
     * Where the selection's handles go, for the Compose overlay to draw
     * (TerminalPane), or null with nothing selected. Main thread.
     */
    var onSelectionHandles: ((SelectionHandles?) -> Unit)? = null

    private class HandleDrag(var edge: SelectionEdge, val dx: Float, val dy: Float)

    private var handleDrag: HandleDrag? = null
    /** A touch and hold is dragging on, a word at a time. */
    private var wordDragging = false
    /** Where the finger (or a dragged handle's caret) is, for scrolling on past the screen's edge. */
    private var selectPoint: Pair<Float, Float>? = null
    private var lastHandleCell: Cell? = null
    private var selectionMenu: ActionMode? = null
    /** Finishing the menu to show it again elsewhere, not to put the selection away. */
    private var menuMoving = false
    private var handles: SelectionHandles? = null

    /** The accessibility action Copy Screen's id. */
    private val copyScreenAction = View.generateViewId()

    /** There's a selection (its handles show): output and scrolling move it. Read on the model's thread too. */
    @Volatile
    private var selectionShown = false

    private val touchGrid: TouchGrid?
        get() {
            val d = drawn ?: return null
            return TouchGrid(d.originX, d.originY, d.cellWidth, d.cellHeight, d.cols, d.rows)
        }

    /** How far from a handle's knob a finger still catches it. */
    private val handleReach = 28 * density

    /** The handle under a point, if any: its end, and its caret's place (x, the row's middle). */
    private fun handleAt(x: Float, y: Float): Triple<SelectionEdge, Float, Float>? {
        val h = handles ?: return null
        return listOfNotNull(h.start, h.end)
            .map { it to hypot(x - it.knobX(density), y - it.knobY(density)) }
            .filter { it.second <= handleReach }
            .minByOrNull { it.second }
            ?.first?.let { Triple(it.edge, it.x, (it.top + it.bottom) / 2) }
    }

    /** Touch and hold: selects the word under the finger, and dragging on selects more, a word at a time. */
    private fun longPressed(x: Float, y: Float) {
        val engine = session?.engine ?: return
        val g = touchGrid ?: return
        stopMomentum()
        hideSelectionMenu()
        val c = g.cell(x, y)
        engine.selectWord(c.x, c.y)
        performHapticFeedback(HapticFeedbackConstants.LONG_PRESS)
        wordDragging = true
        selectPoint = x to y
        parent?.requestDisallowInterceptTouchEvent(true)
        selectionChanged()
    }

    private fun extendWord(x: Float, y: Float) {
        val engine = session?.engine ?: return
        val c = touchGrid?.cell(x, y) ?: return
        engine.extendWordSelection(c.x, c.y)
        selectionChanged()
    }

    /** A handle dragged to (x, y), its caret's place: moves its end, a cell at a time. */
    private fun moveHandle(x: Float, y: Float) {
        val engine = session?.engine ?: return
        val drag = handleDrag ?: return
        val c = touchGrid?.handleCell(x, y, drag.edge) ?: return
        drag.edge = engine.moveSelection(drag.edge, c.x, c.y)
        if (c != lastHandleCell) {
            if (lastHandleCell != null) performHapticFeedback(HapticFeedbackConstants.TEXT_HANDLE_MOVE)
            lastHandleCell = c
        }
        selectionChanged()
    }

    /**
     * Selecting past the screen's top or bottom scrolls back or forward, a
     * row at a time, while the finger stays there (not in full-screen
     * programs, which have no scrollback here).
     */
    private fun updateAutoscroll(y: Float) {
        val engine = session?.engine
        if (engine == null || engine.isAlternateScreen || (touchGrid?.overscroll(y) ?: 0) == 0) return stopAutoscroll()
        if (!autoscrolling) {
            autoscrolling = true
            postDelayed(autoscroll, 70)
        }
    }

    private var autoscrolling = false

    private val autoscroll = object : Runnable {
        override fun run() {
            val engine = session?.engine
            val p = selectPoint
            val over = p?.let { touchGrid?.overscroll(it.second) } ?: 0
            if (engine == null || p == null || over == 0) return stopAutoscroll()
            engine.scroll(if (over < 0) -1 else 1)
            if (handleDrag != null) moveHandle(p.first, p.second) else extendWord(p.first, p.second)
            requestRender()
            postDelayed(this, 70)
        }
    }

    private fun stopAutoscroll() {
        autoscrolling = false
        removeCallbacks(autoscroll)
    }

    /** The selection changed: redraw it, and move its handles and menu. */
    private fun selectionChanged() {
        requestRender()
        selectionMoved.run()
    }

    /** Puts the handles where the selection is now, or puts them away when there's none (output can clear it). */
    private val selectionMoved = Runnable {
        val bounds = session?.engine?.selectionBounds()
        val g = touchGrid
        if (bounds == null || g == null) {
            if (selectionShown) {
                selectionShown = false
                handleDrag = null
                wordDragging = false
                lastHandleCell = null
                stopAutoscroll()
                hideSelectionMenu(clearing = true)
            }
            if (handles != null) {
                handles = null
                onSelectionHandles?.invoke(null)
            }
            return@Runnable
        }
        selectionShown = true
        fun place(c: Cell, edge: SelectionEdge) =
            if (g.shows(c)) SelectionHandle(edge, g.caretX(c, edge), g.rowTop(c.y), g.rowBottom(c.y)) else null
        val h = SelectionHandles(place(bounds.start, SelectionEdge.START), place(bounds.end, SelectionEdge.END))
        if (h != handles) {
            handles = h
            onSelectionHandles?.invoke(h)
            selectionMenu?.invalidateContentRect()
        }
    }

    private fun clearSelection() {
        session?.engine?.clearSelection()
        selectionChanged()
    }

    /** Copies the selection, as plain text (trailing spaces trimmed, wrapped lines joined, as the Mac copies). */
    fun copySelection(): Boolean {
        val text = session?.engine?.selectionText() ?: return false
        context.getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText("Terminal", text))
        return true
    }

    /** Selects the screen as it shows. */
    fun selectScreen() {
        session?.engine?.selectScreen() ?: return
        selectionChanged()
        showSelectionMenu(null)
    }

    private fun hideSelectionMenu(clearing: Boolean = false) {
        val m = selectionMenu ?: return
        menuMoving = !clearing
        selectionMenu = null
        m.finish()
        menuMoving = false
    }

    /** The floating menu (Copy, Select all, Paste), over the selection, or at [at] without one. */
    private fun showSelectionMenu(at: Pair<Float, Float>?) {
        hideSelectionMenu()
        val clipboard = context.getSystemService(ClipboardManager::class.java)
        val canPaste = !readOnly && clipboard?.hasPrimaryClip() == true
        val selected = session?.engine?.selectionBounds() != null
        if (!selected && !canPaste && at == null) return
        selectionMenu = startActionMode(object : ActionMode.Callback2() {
            override fun onCreateActionMode(mode: ActionMode, menu: Menu): Boolean {
                if (selected) menu.add(Menu.NONE, android.R.id.copy, 0, android.R.string.copy)
                menu.add(Menu.NONE, android.R.id.selectAll, 1, android.R.string.selectAll)
                if (canPaste) menu.add(Menu.NONE, android.R.id.paste, 2, android.R.string.paste)
                return true
            }

            override fun onPrepareActionMode(mode: ActionMode, menu: Menu) = false

            override fun onActionItemClicked(mode: ActionMode, item: MenuItem): Boolean {
                when (item.itemId) {
                    android.R.id.copy -> {
                        copySelection()
                        // As a text field does: copied, the selection goes.
                        clearSelection()
                    }
                    android.R.id.selectAll -> selectScreen()
                    android.R.id.paste -> {
                        mode.finish()
                        clearSelection()
                        paste()
                    }
                }
                return true
            }

            override fun onDestroyActionMode(mode: ActionMode) {
                if (selectionMenu === mode) selectionMenu = null
                // Back, or the system closing it: the selection goes with it.
                if (!menuMoving && selectionShown) clearSelection()
            }

            override fun onGetContentRect(mode: ActionMode, view: View, outRect: Rect) {
                val b = session?.engine?.selectionBounds()
                val r = b?.let { touchGrid?.rect(it.start, it.end) }
                if (r != null) {
                    outRect.set(r.left.toInt(), r.top.toInt(), r.right.toInt(), r.bottom.toInt())
                } else {
                    val (x, y) = at ?: (width / 2f to height / 2f)
                    outRect.set(x.toInt(), y.toInt(), x.toInt() + 1, y.toInt() + 1)
                }
            }
        }, ActionMode.TYPE_FLOATING)
    }

    override fun onInitializeAccessibilityNodeInfo(info: AccessibilityNodeInfo) {
        super.onInitializeAccessibilityNodeInfo(info)
        if (selectionShown) info.addAction(AccessibilityNodeInfo.AccessibilityAction(android.R.id.copy, context.getString(android.R.string.copy)))
        info.addAction(AccessibilityNodeInfo.AccessibilityAction(copyScreenAction, "Copy Screen"))
    }

    override fun performAccessibilityAction(action: Int, arguments: Bundle?): Boolean = when (action) {
        android.R.id.copy -> copySelection()
        copyScreenAction -> copyScreen()
        else -> super.performAccessibilityAction(action, arguments)
    }

    /** Copies the screen as it shows (Copy Screen). */
    fun copyScreen(): Boolean {
        val text = session?.engine?.screenText() ?: return false
        context.getSystemService(ClipboardManager::class.java)?.setPrimaryClip(ClipData.newPlainText("Terminal", text))
        return true
    }

}

/** A selection handle's place: its end, the caret's x, and its row's top and bottom, in the view's pixels. */
data class SelectionHandle(val edge: SelectionEdge, val x: Float, val top: Float, val bottom: Float) {
    /** The knob's middle: below the row, to the left of the caret for the start and the right for the end, as Android's. */
    fun knobX(density: Float) = x + (if (edge == SelectionEdge.START) -1 else 1) * KNOB_RADIUS_DP * density
    fun knobY(density: Float) = bottom + KNOB_RADIUS_DP * density

    companion object {
        const val KNOB_RADIUS_DP = 11f
    }
}

/** Where the handles go (an end off the screen has none). */
data class SelectionHandles(val start: SelectionHandle?, val end: SelectionHandle?)
