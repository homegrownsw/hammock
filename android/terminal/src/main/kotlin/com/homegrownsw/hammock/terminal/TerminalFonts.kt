package com.homegrownsw.hammock.terminal

import android.content.res.AssetManager
import android.graphics.Paint
import android.graphics.Rect
import android.graphics.Typeface
import android.graphics.fonts.Font
import android.graphics.fonts.FontFamily
import android.graphics.fonts.FontStyle
import android.util.Log
import kotlin.math.ceil
import kotlin.math.floor
import kotlin.math.max
import kotlin.math.roundToInt

/**
 * The terminal's typefaces: JetBrains Mono (bundled, as on the iPhone, with
 * its licence in assets/fonts) or a system family chosen in Settings, then
 * Symbols Nerd Font Mono for prompt icons whatever the font, then the
 * system's fonts (emoji, CJK, …). Monospace if the bundled files can't be
 * read.
 */
class TerminalFonts private constructor(
    val regular: Typeface,
    val bold: Typeface,
    val italic: Typeface,
    val boldItalic: Typeface,
    /** What cells are measured with: the chosen font's own face (a system family's typefaces start with the symbols). */
    val metrics: Typeface = regular,
) {
    /** The typeface for a cell's bold and italic flags. */
    fun forFlags(flags: Int): Typeface {
        val b = flags and CellFlags.BOLD != 0
        val i = flags and CellFlags.ITALIC != 0
        return when {
            b && i -> boldItalic
            b -> bold
            i -> italic
            else -> regular
        }
    }

    companion object {
        private const val REGULAR = "fonts/JetBrainsMono-VF.ttf"
        private const val ITALIC = "fonts/JetBrainsMono-Italic-VF.ttf"
        private const val SYMBOLS = "fonts/SymbolsNerdFontMono-Regular.ttf"

        /** The bundled font's name, which Settings can also choose by name (editor panes). */
        const val BUNDLED = "JetBrains Mono"

        /**
         * The system's monospaced families Settings offers besides the
         * bundled font: Android's generic names, which every phone has
         * (fonts.xml), as name and label.
         */
        val systemFamilies = listOf("monospace" to "Monospace", "serif-monospace" to "Serif Monospace")

        private val shared = HashMap<String, TerminalFonts>()

        /** The fonts for [family] ("" or [BUNDLED]: JetBrains Mono; else a system family), loaded once each for the process. */
        fun get(assets: AssetManager, family: String = ""): TerminalFonts {
            val key = family.takeUnless { it == BUNDLED || it.isBlank() } ?: ""
            return synchronized(shared) { shared.getOrPut(key) { if (key.isEmpty()) load(assets) else loadSystem(assets, key) } }
        }

        /**
         * A system family, with the icons: the symbols font goes first (it
         * has only icons, so everything else falls through to the family).
         */
        private fun loadSystem(assets: AssetManager, family: String): TerminalFonts = try {
            val symbols = FontFamily.Builder(Font.Builder(assets, SYMBOLS).build()).build()
            fun typeface(weight: Int, italic: Boolean) = Typeface.CustomFallbackBuilder(symbols)
                .setSystemFallback(family)
                .setStyle(FontStyle(weight, if (italic) FontStyle.FONT_SLANT_ITALIC else FontStyle.FONT_SLANT_UPRIGHT))
                .build()
            TerminalFonts(typeface(400, false), typeface(700, false), typeface(400, true), typeface(700, true), Typeface.create(family, Typeface.NORMAL))
        } catch (e: Exception) {
            Log.w("TerminalFonts", "symbols unavailable, using $family alone", e)
            val base = Typeface.create(family, Typeface.NORMAL)
            TerminalFonts(base, Typeface.create(base, Typeface.BOLD), Typeface.create(base, Typeface.ITALIC), Typeface.create(base, Typeface.BOLD_ITALIC))
        }

        private fun load(assets: AssetManager): TerminalFonts = try {
            // Variable fonts: each weight is the same file at another 'wght'.
            fun face(path: String, weight: Int, italic: Boolean) = Font.Builder(assets, path)
                .setWeight(weight)
                .setSlant(if (italic) FontStyle.FONT_SLANT_ITALIC else FontStyle.FONT_SLANT_UPRIGHT)
                .setFontVariationSettings("'wght' $weight")
                .build()
            val family = FontFamily.Builder(face(REGULAR, 400, false))
                .addFont(face(REGULAR, 700, false))
                .addFont(face(ITALIC, 400, true))
                .addFont(face(ITALIC, 700, true))
                .build()
            val symbols = FontFamily.Builder(Font.Builder(assets, SYMBOLS).build()).build()
            fun typeface(weight: Int, italic: Boolean) = Typeface.CustomFallbackBuilder(family)
                .addCustomFallback(symbols)
                .setSystemFallback("monospace")
                .setStyle(FontStyle(weight, if (italic) FontStyle.FONT_SLANT_ITALIC else FontStyle.FONT_SLANT_UPRIGHT))
                .build()
            TerminalFonts(typeface(400, false), typeface(700, false), typeface(400, true), typeface(700, true))
        } catch (e: Exception) {
            Log.w("TerminalFonts", "bundled fonts unavailable, using monospace", e)
            TerminalFonts(
                Typeface.MONOSPACE,
                Typeface.create(Typeface.MONOSPACE, Typeface.BOLD),
                Typeface.create(Typeface.MONOSPACE, Typeface.ITALIC),
                Typeface.create(Typeface.MONOSPACE, Typeface.BOLD_ITALIC),
            )
        }
    }
}

/**
 * A cell's size and where text sits in it, at one font size, in pixels.
 * Rows are a whole number of pixels tall so they never blur into each
 * other; columns are fractional (the font's advance), snapped when drawn.
 */
class CellMetrics private constructor(
    val fontPx: Float,
    val cellWidth: Float,
    val cellHeight: Int,
    /** From the row's top to the baseline. */
    val baseline: Float,
    /** The font's ascent and descent (ascent negative, as Paint has it). */
    val ascent: Float,
    val descent: Float,
    /** Below the baseline, and how thick. */
    val underlinePosition: Float,
    val underlineThickness: Float,
    /** Half the x-height above the baseline: where a strikethrough goes. */
    val strikePosition: Float,
) {
    /** The cell's width in whole pixels, as Kitty graphics tools are told. */
    val cellWidthPx get() = cellWidth.roundToInt().coerceAtLeast(1)

    companion object {
        /**
         * [onePx] is a point's worth of pixels (the density): rows get one
         * more, as on the iPhone. [lineSpacing] is extra row height, a
         * fraction of the font's (negative: tighter).
         */
        fun measure(paint: Paint, fonts: TerminalFonts, fontPx: Float, onePx: Float, lineSpacing: Float = 0f): CellMetrics {
            paint.typeface = fonts.metrics
            paint.textSize = fontPx
            val cw = paint.measureText("M")
            val fm = paint.fontMetrics
            val natural = ceil(fm.descent - fm.ascent + fm.leading) + onePx
            val ch = max((natural * (1 + lineSpacing)).roundToInt(), ceil(fontPx * 0.5f).toInt()).coerceAtLeast(1)
            val inset = floor((ch - natural) / 2)
            val x = Rect()
            paint.getTextBounds("x", 0, 1, x)
            val thickness = max(1f, paint.underlineThickness.takeIf { it > 0 } ?: (fontPx / 14))
            val position = paint.underlinePosition.takeIf { it > 0 } ?: (fm.descent / 2)
            return CellMetrics(
                fontPx = fontPx,
                cellWidth = cw,
                cellHeight = ch,
                baseline = inset - fm.ascent,
                ascent = fm.ascent,
                descent = fm.descent,
                underlinePosition = position,
                underlineThickness = thickness,
                strikePosition = -x.height() / 2f,
            )
        }
    }
}
