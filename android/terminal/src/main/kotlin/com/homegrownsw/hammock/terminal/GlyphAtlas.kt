package com.homegrownsw.hammock.terminal

import android.graphics.Bitmap
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.graphics.Typeface
import android.opengl.GLES30
import android.opengl.GLUtils
import kotlin.math.ceil

/**
 * One texture of glyphs for a renderer, rasterised by Android's own text
 * stack (so font fallback, emoji and shaping inside a cluster are the
 * system's) into slots a cell or two wide, plus a margin for what reaches
 * past its cell (italics, icons). Text is drawn white, its coverage as alpha,
 * and tinted when drawn; colour glyphs (emoji) are kept as they are.
 *
 * Belongs to the GL thread. When it fills, [reset] starts afresh and the
 * frame is built again.
 */
internal class GlyphAtlas(fonts: TerminalFonts) {
    /** The typefaces glyphs are drawn in: changing them forgets every glyph. */
    var fonts: TerminalFonts = fonts
        set(value) {
            if (value === field) return
            field = value
            clear()
        }

    class Glyph(
        /** The slot in the texture, 0–1. */
        val u0: Float, val v0: Float, val u1: Float, val v1: Float,
        /** The slot's size in pixels; its cell starts [padX], [padY] in. */
        val width: Int, val height: Int,
        val color: Boolean,
    )

    private val size = 2048
    var texture = 0; private set
    private var metrics: CellMetrics? = null
    var padX = 0; private set
    var padY = 0; private set
    private var slotH = 0
    private var x = 0
    private var y = 0

    private val singles = LongGlyphMap()
    private val clusters = HashMap<String, Glyph>()
    private val scratch = HashMap<Long, Bitmap>()
    private val paint = Paint(Paint.ANTI_ALIAS_FLAG or Paint.SUBPIXEL_TEXT_FLAG).apply { color = Color.WHITE }
    private val path = Path()
    private val bounds = RectF()
    private var pixels = IntArray(0)

    /** Makes the texture (again, after the GL context was lost). */
    fun create() {
        val ids = IntArray(1)
        GLES30.glGenTextures(1, ids, 0)
        texture = ids[0]
        GLES30.glBindTexture(GLES30.GL_TEXTURE_2D, texture)
        GLES30.glTexImage2D(GLES30.GL_TEXTURE_2D, 0, GLES30.GL_RGBA, size, size, 0, GLES30.GL_RGBA, GLES30.GL_UNSIGNED_BYTE, null)
        // Glyphs are drawn a texel to a pixel.
        GLES30.glTexParameteri(GLES30.GL_TEXTURE_2D, GLES30.GL_TEXTURE_MIN_FILTER, GLES30.GL_NEAREST)
        GLES30.glTexParameteri(GLES30.GL_TEXTURE_2D, GLES30.GL_TEXTURE_MAG_FILTER, GLES30.GL_NEAREST)
        GLES30.glTexParameteri(GLES30.GL_TEXTURE_2D, GLES30.GL_TEXTURE_WRAP_S, GLES30.GL_CLAMP_TO_EDGE)
        GLES30.glTexParameteri(GLES30.GL_TEXTURE_2D, GLES30.GL_TEXTURE_WRAP_T, GLES30.GL_CLAMP_TO_EDGE)
        clear()
    }

    /** Uses [m]'s size from now on: every glyph drawn at another goes. */
    fun use(m: CellMetrics) {
        if (metrics === m) return
        metrics = m
        padX = ceil(m.cellWidth / 2).toInt()
        padY = ceil(m.cellHeight / 4f).toInt()
        slotH = m.cellHeight + 2 * padY
        for (b in scratch.values) b.recycle()
        scratch.clear()
        clear()
    }

    /** Forgets every glyph (the texture filled up). */
    fun clear() {
        singles.clear()
        clusters.clear()
        x = 0
        y = 0
    }

    /**
     * The glyph for [text] (codepoint [cp], or 0 for a cluster) in a cell's
     * style ([flags]: bold, italic, wide), with [room] cells for an icon to
     * fill; null if the atlas is full.
     */
    fun glyph(cp: Int, text: String?, flags: Int, room: Int): Glyph? {
        val style = flags and (CellFlags.BOLD or CellFlags.ITALIC or CellFlags.WIDE)
        if (cp > 0) {
            val key = cp.toLong() or (style.toLong() shl 21) or (room.toLong() shl 40)
            singles[key]?.let { return it }
            val g = rasterize(String(Character.toChars(cp)), cp, style, room) ?: return null
            singles[key] = g
            return g
        }
        val s = text ?: return null
        val key = "$style|$room|$s"
        clusters[key]?.let { return it }
        val g = rasterize(s, s.codePointAt(0), style, room) ?: return null
        clusters[key] = g
        return g
    }

    private fun rasterize(text: String, cp: Int, style: Int, room: Int): Glyph? {
        val m = metrics ?: return null
        val cells = if (style and CellFlags.WIDE != 0) 2 else 1
        val w = ceil(m.cellWidth * cells).toInt() + 2 * padX
        val h = slotH
        if (x + w > size) {
            x = 0
            y += h
        }
        if (y + h > size) return null

        val bitmap = scratch.getOrPut((w.toLong() shl 32) or h.toLong()) { Bitmap.createBitmap(w, h, Bitmap.Config.ARGB_8888) }
        bitmap.eraseColor(Color.TRANSPARENT)
        val canvas = Canvas(bitmap)
        // Emoji sequences (ZWJ families, skin tones, keycaps) go straight to
        // the system's emoji font: through the terminal font's fallback chain
        // the sequence is split between fonts (JetBrains Mono has a ZWJ) and
        // comes out as separate pictures or a blank box.
        val emojiSequence = text.length > Character.charCount(cp) &&
            (cp >= 0x1F000 || text.contains('\u200D') || text.contains('\uFE0F') || text.contains('\u20E3'))
        paint.typeface = if (emojiSequence) Typeface.DEFAULT else fonts.forFlags(style)
        paint.textSize = m.fontPx
        draw(canvas, text, cp, m, cells, room)

        // A glyph with colour of its own (emoji) is kept as drawn; the rest
        // is tinted. Text drawn white has every channel equal to its alpha
        // (premultiplied); anything else, grey emoji silhouettes too, has its own.
        if (pixels.size < w * h) pixels = IntArray(w * h)
        bitmap.getPixels(pixels, 0, w, 0, 0, w, h)
        var color = false
        for (i in 0 until w * h) {
            val p = pixels[i]
            val a = p ushr 24
            if (a == 0) continue
            // getPixels un-premultiplies: white text reads back as white.
            val r = (p shr 16) and 0xFF
            val g = (p shr 8) and 0xFF
            val b = p and 0xFF
            if (r < 250 || g < 250 || b < 250) {
                color = true
                break
            }
        }
        GLES30.glBindTexture(GLES30.GL_TEXTURE_2D, texture)
        GLUtils.texSubImage2D(GLES30.GL_TEXTURE_2D, 0, x, y, bitmap)
        val g = Glyph(
            u0 = x / size.toFloat(), v0 = y / size.toFloat(),
            u1 = (x + w) / size.toFloat(), v1 = (y + h) / size.toFloat(),
            width = w, height = h, color = color,
        )
        x += w
        return g
    }

    /** Draws one glyph into its slot, its cell [padX], [padY] in. */
    private fun draw(canvas: Canvas, text: String, cp: Int, m: CellMetrics, cells: Int, room: Int) {
        val cw = m.cellWidth
        val ch = m.cellHeight.toFloat()
        val left = padX.toFloat()
        val top = padY.toFloat()
        canvas.save()
        when {
            // Box drawing and legacy computing symbols: designed to span the
            // font's line height; stretched to the cell so lines join
            // across rows (the cell is taller than the font). Across the row
            // they reach past the cell to join.
            cp in 0x2500..0x257F || cp in 0x1FB00..0x1FBFF -> {
                canvas.clipRect(0f, top, canvas.width.toFloat(), top + ch)
                val sy = ch / (m.descent - m.ascent)
                canvas.translate(left, top)
                canvas.scale(1f, sy)
                canvas.drawText(text, 0f, -m.ascent, paint)
            }
            // Powerline separators: the glyph's bounds mapped onto the cell,
            // so segments join seamlessly.
            cp in 0xE0B0..0xE0D7 -> {
                paint.getTextPath(text, 0, text.length, 0f, 0f, path)
                path.computeBounds(bounds, true)
                if (bounds.width() > 0 && bounds.height() > 0) {
                    canvas.clipRect(left, top, left + cw, top + ch)
                    canvas.translate(left, top)
                    canvas.scale(cw / bounds.width(), ch / bounds.height())
                    canvas.translate(-bounds.left, -bounds.top)
                    canvas.drawPath(path, paint)
                } else {
                    canvas.drawText(text, left, top + m.baseline, paint)
                }
            }
            // Nerd Font icons (Private Use Area) are often wider than a cell:
            // shrunk to the room they have (two cells when followed by a
            // blank, as Ghostty does), about their middle.
            cp in 0xE000..0xF8FF || cp >= 0xF0000 -> {
                paint.getTextPath(text, 0, text.length, 0f, 0f, path)
                path.computeBounds(bounds, true)
                val space = cw * room
                if (bounds.right > space + 0.5f && bounds.right > 0) {
                    val s = space / bounds.right
                    val mid = bounds.centerY()
                    canvas.translate(left, top + m.baseline + mid * (1 - s))
                    canvas.scale(s, s)
                    canvas.drawPath(path, paint)
                } else {
                    canvas.drawText(text, left, top + m.baseline, paint)
                }
            }
            else -> {
                // Wide cells (emoji, CJK) from fallback fonts can be wider
                // than their two cells: shrunk to fit, about the line's middle.
                val advance = paint.measureText(text)
                val space = cw * cells
                if (advance > space + 0.5f && (cells == 2 || cp >= 0x2E80)) {
                    val s = space / advance
                    val mid = (m.ascent + m.descent) / 2
                    canvas.translate(left, top + m.baseline + mid * (1 - s))
                    canvas.scale(s, s)
                    canvas.drawText(text, 0f, 0f, paint)
                } else {
                    canvas.drawText(text, left, top + m.baseline, paint)
                }
            }
        }
        canvas.restore()
    }

    fun release() {
        if (texture != 0) GLES30.glDeleteTextures(1, intArrayOf(texture), 0)
        texture = 0
        for (b in scratch.values) b.recycle()
        scratch.clear()
        clear()
    }
}

/** Glyphs by a packed key, without boxing a Long per lookup. */
internal class LongGlyphMap {
    private var keys = LongArray(1024)
    private var values = arrayOfNulls<GlyphAtlas.Glyph>(1024)
    private var count = 0

    operator fun get(key: Long): GlyphAtlas.Glyph? {
        val mask = keys.size - 1
        var i = mix(key) and mask
        while (true) {
            val v = values[i] ?: return null
            if (keys[i] == key) return v
            i = (i + 1) and mask
        }
    }

    operator fun set(key: Long, value: GlyphAtlas.Glyph) {
        if ((count + 1) * 2 > keys.size) grow()
        val mask = keys.size - 1
        var i = mix(key) and mask
        while (values[i] != null && keys[i] != key) i = (i + 1) and mask
        if (values[i] == null) count++
        keys[i] = key
        values[i] = value
    }

    fun clear() {
        values.fill(null)
        count = 0
    }

    private fun grow() {
        val oldK = keys
        val oldV = values
        keys = LongArray(oldK.size * 2)
        values = arrayOfNulls(oldV.size * 2)
        count = 0
        for (i in oldK.indices) oldV[i]?.let { set(oldK[i], it) }
    }

    private fun mix(k: Long): Int {
        var h = k * -0x61c8864680b583ebL
        h = h xor (h ushr 29)
        return h.toInt()
    }
}
