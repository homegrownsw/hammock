package com.homegrownsw.hammock.panes

import androidx.compose.runtime.Immutable

/**
 * The colours a pane is drawn in: the terminal theme's (as the app shows
 * terminals in this mode) and the app theme's accent. Colours are ARGB ints
 * (alpha ignored by the pages).
 */
@Immutable
data class PaneTheme(
    val background: Int,
    val foreground: Int,
    val cursor: Int,
    val selection: Int,
    /** The 16 ANSI colours: the pages' syntax highlighting takes from them. */
    val palette: List<Int>,
    val dark: Boolean,
    /** The app theme's accent: buttons, chips, the diff page's own accent. */
    val accent: Int,
)

/**
 * The editor settings the Mac keeps in its config file (editor-wrap,
 * editor-line-numbers…), with the same defaults. The app passes the font
 * from Settings ▸ Editors and Diffs (else the terminal's).
 */
@Immutable
data class EditorSettings(
    /** A family name: the bundled "JetBrains Mono" (PageAssets serves it) or a system family; "" is the platform's monospace. */
    val fontFamily: String = "",
    /** CSS pixels (dp). */
    val fontSize: Double = 13.0,
    val wrap: Boolean = false,
    val lineNumbers: Boolean = true,
    val tabWidth: Int = 4,
    /** "spaces" or "tabs" (a file's .editorconfig, or its own indentation, wins). */
    val indent: String = "spaces",
    val vim: Boolean = false,
)

/** The diff settings (diff-whitespace, diff-wrap); the pane's own choices win over them. */
@Immutable
data class DiffSettings(
    val fontFamily: String = "",
    val fontSize: Double = 12.0,
    /** "show" or "ignore". */
    val whitespace: String = "show",
    val wrap: Boolean = false,
)

internal fun hex(argb: Int): String = String.format("#%06x", argb and 0xFFFFFF)

/** The font stack the pages are given: the setting's, else Android's monospace. */
internal fun fontStack(family: String): String = family.ifBlank { "monospace" }
