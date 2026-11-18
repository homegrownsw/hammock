package com.homegrownsw.hammock.terminal

import android.graphics.Paint
import android.opengl.GLES30
import android.opengl.GLSurfaceView
import android.util.Log
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.nio.FloatBuffer
import javax.microedition.khronos.egl.EGLConfig
import javax.microedition.khronos.opengles.GL10
import kotlin.math.ceil
import kotlin.math.max
import kotlin.math.min
import kotlin.math.roundToInt

/** What the view tells the renderer, swapped whole (the renderer reads it on the GL thread). */
internal data class RenderInputs(
    val session: TerminalSession? = null,
    val theme: TerminalTheme = TerminalTheme.Dark,
    /** The font size people chose, and the smallest it may shrink to, in pixels. */
    val fontPx: Float = 33f,
    val minFontPx: Float = 15f,
    val paddingPx: Float = 12f,
    /** A point's worth of pixels (the density). */
    val onePx: Float = 3f,
    val lineSpacing: Float = 0f,
    /** Typing here: the cursor is drawn solid (else as a faint underline). */
    val focused: Boolean = false,
    /** A picture (a tile): the font fits the width only, showing the rows down to the cursor. */
    val preview: Boolean = false,
)

/** Where the last frame put the grid, in pixels: for turning touches into cells. */
data class DrawnGrid(
    val originX: Float, val originY: Float,
    val cellWidth: Float, val cellHeight: Float,
    val cols: Int, val rows: Int,
    val fontPx: Float,
)

/**
 * Draws a pane with OpenGL ES 3 (as TerminalMetalRenderer does on the
 * iPhone): each frame, the screen copied out of the engine becomes one
 * instanced draw of quads (backgrounds merged into runs, glyphs from the
 * atlas, underlines, block elements, the cursor), in that order. The whole
 * pane is drawn every frame, which costs the GPU little; frames are only
 * drawn when something changed (RENDERMODE_WHEN_DIRTY).
 */
internal class TerminalRenderer(fonts: TerminalFonts, private val onDrawn: (DrawnGrid) -> Unit) : GLSurfaceView.Renderer {
    @Volatile var inputs = RenderInputs()

    private val frame = TerminalFrame()
    private val atlas = GlyphAtlas(fonts)
    private val fontsRef = fonts
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG)
    private val metricsCache = HashMap<Float, CellMetrics>()
    private var width = 0
    private var height = 0

    private var program = 0
    private var vao = 0
    private var quadVbo = 0
    private var instanceVbo = 0
    private var uViewport = 0
    private var uAtlas = 0

    private var data = FloatArray(STRIDE * 4096)
    private var count = 0
    private var buffer: FloatBuffer = direct(data.size)

    private var fitKey: List<Any>? = null
    private var fit: CellMetrics? = null
    private var lastDrawn: DrawnGrid? = null

    override fun onSurfaceCreated(gl: GL10?, config: EGLConfig?) {
        program = link(VERTEX, FRAGMENT)
        uViewport = GLES30.glGetUniformLocation(program, "uViewport")
        uAtlas = GLES30.glGetUniformLocation(program, "uAtlas")
        val ids = IntArray(2)
        GLES30.glGenVertexArrays(1, ids, 0)
        vao = ids[0]
        GLES30.glGenBuffers(2, ids, 0)
        quadVbo = ids[0]
        instanceVbo = ids[1]
        GLES30.glBindVertexArray(vao)
        GLES30.glBindBuffer(GLES30.GL_ARRAY_BUFFER, quadVbo)
        val quad = floatArrayOf(0f, 0f, 1f, 0f, 0f, 1f, 1f, 1f)
        GLES30.glBufferData(GLES30.GL_ARRAY_BUFFER, quad.size * 4, direct(quad.size).put(quad).position(0), GLES30.GL_STATIC_DRAW)
        GLES30.glEnableVertexAttribArray(0)
        GLES30.glVertexAttribPointer(0, 2, GLES30.GL_FLOAT, false, 8, 0)
        GLES30.glBindBuffer(GLES30.GL_ARRAY_BUFFER, instanceVbo)
        val stride = STRIDE * 4
        for ((loc, size, offset) in listOf(Triple(1, 4, 0), Triple(2, 4, 16), Triple(3, 4, 32), Triple(4, 1, 48))) {
            GLES30.glEnableVertexAttribArray(loc)
            GLES30.glVertexAttribPointer(loc, size, GLES30.GL_FLOAT, false, stride, offset)
            GLES30.glVertexAttribDivisor(loc, 1)
        }
        GLES30.glBindVertexArray(0)
        // A new context has none of the old one's textures.
        atlas.create()
        GLES30.glDisable(GLES30.GL_DEPTH_TEST)
        GLES30.glEnable(GLES30.GL_BLEND)
        GLES30.glBlendFunc(GLES30.GL_ONE, GLES30.GL_ONE_MINUS_SRC_ALPHA)
    }

    override fun onSurfaceChanged(gl: GL10?, width: Int, height: Int) {
        this.width = width
        this.height = height
        GLES30.glViewport(0, 0, width, height)
    }

    override fun onDrawFrame(gl: GL10?) {
        val inp = inputs
        val session = inp.session
        if (session == null || !session.isReady || width == 0 || height == 0) {
            clear(inp.theme.background)
            return
        }
        session.engine.frame(frame)
        if (frame.cols == 0 || frame.rows == 0) {
            clear(frame.background)
            return
        }
        val m = fitted(frame.cols, frame.rows, inp)
        atlas.use(m)
        // Kitty graphics tools and XTWINOPS are told the cell's size as drawn.
        session.engine.setCellPixels(m.cellWidthPx, m.cellHeight)

        val pad = inp.paddingPx.roundToInt().toFloat()
        var originY = pad
        if (inp.preview) {
            // A picture whose rows down to the cursor don't fit shows them ending at its bottom.
            val shown = min(if (frame.cursorVisible) frame.cursorY + 1 else frame.rows, frame.rows) * m.cellHeight
            if (shown + pad * 2 > height) originY = height - pad - shown
        }
        val drawn = DrawnGrid(pad, originY, m.cellWidth, m.cellHeight.toFloat(), frame.cols, frame.rows, m.fontPx)
        if (drawn != lastDrawn) {
            lastDrawn = drawn
            onDrawn(drawn)
        }

        if (!build(m, inp, pad, originY)) {
            // The atlas filled: start it afresh, and build the frame again.
            atlas.clear()
            build(m, inp, pad, originY)
        }
        clear(frame.background)
        if (count == 0) return
        if (buffer.capacity() < count * STRIDE) buffer = direct(data.size)
        buffer.clear()
        buffer.put(data, 0, count * STRIDE)
        buffer.flip()
        GLES30.glUseProgram(program)
        GLES30.glUniform2f(uViewport, width.toFloat(), height.toFloat())
        GLES30.glActiveTexture(GLES30.GL_TEXTURE0)
        GLES30.glBindTexture(GLES30.GL_TEXTURE_2D, atlas.texture)
        GLES30.glUniform1i(uAtlas, 0)
        GLES30.glBindVertexArray(vao)
        GLES30.glBindBuffer(GLES30.GL_ARRAY_BUFFER, instanceVbo)
        GLES30.glBufferData(GLES30.GL_ARRAY_BUFFER, count * STRIDE * 4, buffer, GLES30.GL_STREAM_DRAW)
        GLES30.glDrawArraysInstanced(GLES30.GL_TRIANGLE_STRIP, 0, 4, count)
        GLES30.glBindVertexArray(0)
    }

    private fun clear(rgb: Int) {
        GLES30.glClearColor(((rgb shr 16) and 0xFF) / 255f, ((rgb shr 8) and 0xFF) / 255f, (rgb and 0xFF) / 255f, 1f)
        GLES30.glClear(GLES30.GL_COLOR_BUFFER_BIT)
    }

    private fun metrics(px: Float, inp: RenderInputs): CellMetrics {
        if (metricsCache.size > 64) metricsCache.clear()
        return metricsCache.getOrPut(px) { CellMetrics.measure(paint, fontsRef, px, inp.onePx, inp.lineSpacing) }
    }

    /**
     * The font shrunk so a pane wider (or taller) than the view, sized by
     * the Mac, still fits; as on the iPhone, both ways, so the prompt
     * (usually the bottom row) stays in sight.
     */
    private fun fitted(cols: Int, rows: Int, inp: RenderInputs): CellMetrics {
        val key = listOf(cols, rows, width, height, inp.fontPx, inp.minFontPx, inp.paddingPx, inp.lineSpacing, inp.onePx, inp.preview)
        fit?.let { if (key == fitKey) return it }
        if (fitKey != null && fitKey!![4] != inp.fontPx) metricsCache.clear()
        val pad = inp.paddingPx.roundToInt() * 2f
        fun fits(m: CellMetrics) = cols * m.cellWidth + pad <= width + 0.5f && (inp.preview || rows * m.cellHeight + pad <= height)
        var px = inp.fontPx
        var m = metrics(px, inp)
        if (!fits(m)) {
            val shrink = min(width / (cols * m.cellWidth + pad), if (inp.preview) 1f else height / (rows * m.cellHeight + pad))
            px = max(inp.minFontPx, px * min(1f, shrink))
            m = metrics(px, inp)
            // Rows are whole pixels and advances don't scale exactly: step down until it fits.
            while (!fits(m) && px > inp.minFontPx) {
                px = max(inp.minFontPx, px * 0.97f)
                m = metrics(px, inp)
            }
        }
        fitKey = key
        fit = m
        return m
    }

    // MARK: Building a frame

    private fun add(x: Float, y: Float, w: Float, h: Float, u0: Float, v0: Float, u1: Float, v1: Float, rgb: Int, a: Float, mode: Float) {
        if (data.size < (count + 1) * STRIDE) data = data.copyOf(data.size * 2)
        var i = count * STRIDE
        val d = data
        d[i++] = x; d[i++] = y; d[i++] = w; d[i++] = h
        d[i++] = u0; d[i++] = v0; d[i++] = u1; d[i++] = v1
        d[i++] = ((rgb shr 16) and 0xFF) / 255f; d[i++] = ((rgb shr 8) and 0xFF) / 255f; d[i++] = (rgb and 0xFF) / 255f; d[i++] = a
        d[i] = mode
        count++
    }

    private fun rect(x: Float, y: Float, w: Float, h: Float, rgb: Int, a: Float = 1f) {
        if (w > 0 && h > 0) add(x, y, w, h, 0f, 0f, 0f, 0f, rgb, a, 0f)
    }

    /** The cell's background, or -1 to leave the default (TerminalPalette in TerminalLineLayout.swift). */
    private fun background(x: Int, y: Int, theme: TerminalTheme): Int {
        val flags = frame.flags(x, y)
        if (flags and CellFlags.SELECTED != 0) return theme.selection
        if (flags and CellFlags.INVERSE != 0) return frame.fg(x, y).takeIf { it >= 0 } ?: frame.foreground
        return frame.bg(x, y)
    }

    private fun foreground(x: Int, y: Int, theme: TerminalTheme): Int {
        val flags = frame.flags(x, y)
        val fg = frame.fg(x, y)
        var c = if (flags and CellFlags.INVERSE != 0) frame.bg(x, y).takeIf { it >= 0 } ?: frame.background
        else if (fg >= 0) fg else frame.foreground
        if (flags and CellFlags.SELECTED != 0) c = theme.selectionForeground ?: (if (fg < 0) frame.foreground else c)
        return c
    }

    private fun isBlank(x: Int, y: Int): Boolean {
        val cp = frame.codepoint(x, y)
        return cp == 0 || cp == ' '.code
    }

    /** Builds the frame's quads; false if the atlas filled up on the way. */
    private fun build(m: CellMetrics, inp: RenderInputs, ox: Float, oy: Float): Boolean {
        count = 0
        val theme = inp.theme
        val cw = m.cellWidth
        val ch = m.cellHeight.toFloat()
        val cols = frame.cols
        fun sx(col: Int) = (ox + col * cw).roundToInt().toFloat()
        val cursorColor = frame.cursorColor ?: theme.cursor
        val cursorOn = frame.cursorVisible && frame.cursorY in 0 until frame.rows && frame.cursorX in 0 until cols
        val cursorWide = cursorOn && frame.flags(frame.cursorX, frame.cursorY) and CellFlags.WIDE != 0
        // A solid block cursor goes under the text; the character on it is
        // drawn in the background's colour.
        val solidBlock = cursorOn && inp.focused && frame.cursorStyle == CursorStyle.BLOCK

        for (y in 0 until frame.rows) {
            val top = oy + y * ch
            if (top > height) break
            if (top + ch < 0) continue
            // Backgrounds, merged into runs of one colour.
            var runStart = 0
            var runColor = -1
            for (x in 0..cols) {
                val c = if (x < cols) background(x, y, theme) else -2
                if (c != runColor) {
                    if (runColor >= 0) rect(sx(runStart), top, sx(x) - sx(runStart), ch, runColor)
                    runStart = x
                    runColor = c
                }
            }
            if (solidBlock && y == frame.cursorY) {
                val x0 = sx(frame.cursorX)
                rect(x0, top, sx(frame.cursorX + if (cursorWide) 2 else 1) - x0, ch, cursorColor)
            }
            // Text, block elements and decorations.
            for (x in 0 until cols) {
                val flags = frame.flags(x, y)
                if (flags and (CellFlags.SPACER or CellFlags.INVISIBLE) != 0) continue
                var fg = foreground(x, y, theme)
                if (solidBlock && y == frame.cursorY && x == frame.cursorX) fg = background(x, y, theme).takeIf { it >= 0 } ?: frame.background
                val alpha = if (flags and CellFlags.FAINT != 0) 0.6f else 1f
                val wide = flags and CellFlags.WIDE != 0
                val x0 = sx(x)
                val x1 = sx(x + if (wide) 2 else 1)
                decorations(flags, x0, x1, top, m, fg, alpha)
                val cp = frame.codepoint(x, y)
                if (cp == 0 || cp == ' '.code) continue
                if (cp in 0x2580..0x259F) {
                    block(cp, x, top, cw, ch, ox, fg, alpha)
                    continue
                }
                val room = if (wide || (x + 1 < cols && isBlank(x + 1, y))) 2 else 1
                val g = atlas.glyph(cp, if (cp < 0) frame.text(x, y) else null, flags, room) ?: return false
                add(x0 - atlas.padX, top - atlas.padY, g.width.toFloat(), g.height.toFloat(), g.u0, g.v0, g.u1, g.v1,
                    fg, alpha, if (g.color) 2f else 1f)
            }
        }
        if (cursorOn && !solidBlock) {
            val x0 = sx(frame.cursorX)
            val w = sx(frame.cursorX + if (cursorWide) 2 else 1) - x0
            val top = oy + frame.cursorY * ch
            val line = max(1f, (inp.onePx * 2).roundToInt().toFloat())
            if (!inp.focused) {
                // Not typing here: a faint underline, as on the iPhone.
                rect(x0, top + ch - line, w, line, cursorColor, 0.35f)
            } else when (frame.cursorStyle) {
                CursorStyle.BAR -> rect(x0, top, line, ch, cursorColor)
                CursorStyle.UNDERLINE -> rect(x0, top + ch - line, w, line, cursorColor)
                else -> {
                    val t = max(1f, inp.onePx.roundToInt().toFloat())
                    rect(x0, top, w, t, cursorColor)
                    rect(x0, top + ch - t, w, t, cursorColor)
                    rect(x0, top + t, t, ch - 2 * t, cursorColor)
                    rect(x0 + w - t, top + t, t, ch - 2 * t, cursorColor)
                }
            }
        }
        return true
    }

    private fun decorations(flags: Int, x0: Float, x1: Float, top: Float, m: CellMetrics, fg: Int, alpha: Float) {
        if (flags and (CellFlags.UNDERLINE or CellFlags.STRIKETHROUGH or CellFlags.OVERLINE) == 0) return
        val t = ceil(m.underlineThickness)
        if (flags and CellFlags.UNDERLINE != 0) {
            val y = (top + m.baseline + m.underlinePosition).roundToInt().toFloat()
            rect(x0, y, x1 - x0, t, fg, alpha)
            // Double underlines get a second line; curly, dotted and dashed are drawn single.
            if (CellFlags.underlineStyle(flags) == 2) rect(x0, y + 2 * t, x1 - x0, t, fg, alpha)
        }
        if (flags and CellFlags.STRIKETHROUGH != 0) rect(x0, (top + m.baseline + m.strikePosition).roundToInt().toFloat(), x1 - x0, t, fg, alpha)
        if (flags and CellFlags.OVERLINE != 0) rect(x0, top, x1 - x0, t, fg, alpha)
    }

    /**
     * Block elements (U+2580–259F) as rectangles snapped to pixels, as
     * Ghostty draws them: glyphs leave anti-aliased seams where cells meet
     * (the cell width isn't a whole number of pixels), striping block art.
     */
    private fun block(v: Int, col: Int, top: Float, cw: Float, ch: Float, ox: Float, fg: Int, alpha: Float) {
        val x0 = ox + col * cw
        fun fill(fx: Float, fy: Float, fw: Float, fh: Float, a: Float = 1f) {
            val l = (x0 + fx * cw).roundToInt().toFloat()
            val r = (x0 + (fx + fw) * cw).roundToInt().toFloat()
            val t = top + (fy * ch).roundToInt()
            val b = top + ((fy + fh) * ch).roundToInt()
            rect(l, t, r - l, b - t, fg, alpha * a)
        }
        fun quadrants(q: Int) { // 1 upper left, 2 upper right, 4 lower left, 8 lower right
            if (q and 1 != 0) fill(0f, 0f, 0.5f, 0.5f)
            if (q and 2 != 0) fill(0.5f, 0f, 0.5f, 0.5f)
            if (q and 4 != 0) fill(0f, 0.5f, 0.5f, 0.5f)
            if (q and 8 != 0) fill(0.5f, 0.5f, 0.5f, 0.5f)
        }
        when (v) {
            0x2580 -> fill(0f, 0f, 1f, 0.5f)
            in 0x2581..0x2587 -> { val n = (v - 0x2580) / 8f; fill(0f, 1 - n, 1f, n) } // lower eighths
            0x2588 -> fill(0f, 0f, 1f, 1f)
            in 0x2589..0x258F -> fill(0f, 0f, (0x2590 - v) / 8f, 1f) // left eighths
            0x2590 -> fill(0.5f, 0f, 0.5f, 1f)
            0x2591 -> fill(0f, 0f, 1f, 1f, 0.25f)
            0x2592 -> fill(0f, 0f, 1f, 1f, 0.5f)
            0x2593 -> fill(0f, 0f, 1f, 1f, 0.75f)
            0x2594 -> fill(0f, 0f, 1f, 1f / 8)
            0x2595 -> fill(7f / 8, 0f, 1f / 8, 1f)
            else -> quadrants(when (v) {
                0x2596 -> 4; 0x2597 -> 8; 0x2598 -> 1; 0x2599 -> 13; 0x259A -> 9
                0x259B -> 7; 0x259C -> 11; 0x259D -> 2; 0x259E -> 6; 0x259F -> 14
                else -> 0
            })
        }
    }

    /** Frees the GL objects (on the GL thread, while the context is current). */
    fun release() {
        atlas.release()
        if (program != 0) GLES30.glDeleteProgram(program)
        GLES30.glDeleteBuffers(2, intArrayOf(quadVbo, instanceVbo), 0)
        GLES30.glDeleteVertexArrays(1, intArrayOf(vao), 0)
        program = 0
    }

    private companion object {
        /** Floats an instance: x, y, w, h; u0, v0, u1, v1; r, g, b, a; mode. */
        const val STRIDE = 13

        // Rectangles in pixels from the top left. Mode 0 fills with the
        // colour, 1 tints the atlas's coverage with it, 2 draws the atlas's
        // own colours (emoji). Premultiplied alpha throughout.
        const val VERTEX = """#version 300 es
layout(location = 0) in vec2 aCorner;
layout(location = 1) in vec4 aRect;
layout(location = 2) in vec4 aUV;
layout(location = 3) in vec4 aColor;
layout(location = 4) in float aMode;
uniform vec2 uViewport;
out vec2 vUV;
out vec4 vColor;
flat out int vMode;
void main() {
    vec2 p = aRect.xy + aCorner * aRect.zw;
    gl_Position = vec4(p.x / uViewport.x * 2.0 - 1.0, 1.0 - p.y / uViewport.y * 2.0, 0.0, 1.0);
    vUV = mix(aUV.xy, aUV.zw, aCorner);
    vColor = aColor;
    vMode = int(aMode + 0.5);
}"""

        const val FRAGMENT = """#version 300 es
precision mediump float;
in vec2 vUV;
in vec4 vColor;
flat in int vMode;
uniform sampler2D uAtlas;
out vec4 outColor;
void main() {
    if (vMode == 0) {
        outColor = vec4(vColor.rgb * vColor.a, vColor.a);
    } else if (vMode == 1) {
        float a = texture(uAtlas, vUV).a * vColor.a;
        outColor = vec4(vColor.rgb * a, a);
    } else {
        outColor = texture(uAtlas, vUV) * vColor.a;
    }
}"""

        fun direct(floats: Int): FloatBuffer = ByteBuffer.allocateDirect(floats * 4).order(ByteOrder.nativeOrder()).asFloatBuffer()

        fun compile(type: Int, src: String): Int {
            val s = GLES30.glCreateShader(type)
            GLES30.glShaderSource(s, src)
            GLES30.glCompileShader(s)
            val ok = IntArray(1)
            GLES30.glGetShaderiv(s, GLES30.GL_COMPILE_STATUS, ok, 0)
            if (ok[0] == 0) Log.e("TerminalRenderer", "shader: " + GLES30.glGetShaderInfoLog(s))
            return s
        }

        fun link(vs: String, fs: String): Int {
            val p = GLES30.glCreateProgram()
            val v = compile(GLES30.GL_VERTEX_SHADER, vs)
            val f = compile(GLES30.GL_FRAGMENT_SHADER, fs)
            GLES30.glAttachShader(p, v)
            GLES30.glAttachShader(p, f)
            GLES30.glLinkProgram(p)
            val ok = IntArray(1)
            GLES30.glGetProgramiv(p, GLES30.GL_LINK_STATUS, ok, 0)
            if (ok[0] == 0) Log.e("TerminalRenderer", "link: " + GLES30.glGetProgramInfoLog(p))
            GLES30.glDeleteShader(v)
            GLES30.glDeleteShader(f)
            return p
        }
    }
}
