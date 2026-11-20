// The JNI side of TerminalEngine (TerminalEngine.kt): a client-side terminal
// over libghostty-vt, as apple/Sources/HammockKit/TerminalEngine.swift is on
// the iPhone. It never answers terminal queries (the daemon's mirror does),
// so it's purely a renderer's model.
//
// Not thread-safe: TerminalEngine.kt holds a lock around every call (output
// is written on the model's thread, frames are read on the GL thread).

#include <jni.h>
#include <android/keycodes.h>

#include <cstdint>
#include <cstring>
#include <vector>

#include <ghostty/vt.h>

namespace {

struct Term {
    GhosttyTerminal terminal = nullptr;
    GhosttyRenderState render = nullptr;
    GhosttyRenderStateRowIterator rows = nullptr;
    GhosttyRenderStateRowCells cells = nullptr;
    GhosttyKeyEncoder keyEncoder = nullptr;
    GhosttyKeyEvent keyEvent = nullptr;
    GhosttyMouseEncoder mouseEncoder = nullptr;
    GhosttyMouseEvent mouseEvent = nullptr;
    bool mouseButtonDown = false;
    uint16_t cols = 1, rowsN = 1;
    uint32_t cellW = 0, cellH = 0;
    // The theme, applied again after a reset (which forgets it).
    bool hasTheme = false;
    GhosttyColorRgb bg{}, fg{}, cursor{};
    GhosttyColorRgb palette[256]{};
    // The word a touch and hold selected, in screen coordinates: dragging on
    // from it selects whole words, keeping this one (extendWord).
    bool hasAnchor = false;
    GhosttyPointCoordinate anchorStart{}, anchorEnd{};
};

Term* T(jlong h) { return reinterpret_cast<Term*>(h); }

GhosttyColorRgb rgb(jint v) {
    return GhosttyColorRgb{uint8_t((v >> 16) & 0xFF), uint8_t((v >> 8) & 0xFF), uint8_t(v & 0xFF)};
}

jint pack(GhosttyColorRgb c) { return (jint(c.r) << 16) | (jint(c.g) << 8) | jint(c.b); }

void applyTheme(Term* t) {
    if (!t->hasTheme) return;
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_BACKGROUND, &t->bg);
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_FOREGROUND, &t->fg);
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_CURSOR, &t->cursor);
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_COLOR_PALETTE, t->palette);
}

void configure(Term* t) {
    // Grapheme clustering (mode 2027), as in Ghostty: emoji sequences and
    // flags take one cluster's width. Must match the daemon's mirror.
    GhosttyTerminalModeConfig cluster{ghostty_mode_new(2027, false), true};
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_MODE_DEFAULT, &cluster);
    // As much scrollback as the daemon's mirror keeps (10,000 lines), with
    // no limit in bytes: libghostty's default kept only a few hundred lines
    // of coloured output.
    size_t lines = 10000;
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_LINES, &lines);
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_SCROLLBACK_MAX_BYTES, nullptr);
}

jbyteArray bytes(JNIEnv* env, const void* data, size_t len) {
    jbyteArray out = env->NewByteArray(jsize(len));
    if (out && len > 0) env->SetByteArrayRegion(out, 0, jsize(len), reinterpret_cast<const jbyte*>(data));
    return out;
}

std::vector<char> utf8(JNIEnv* env, jbyteArray a) {
    std::vector<char> v;
    if (!a) return v;
    jsize n = env->GetArrayLength(a);
    v.resize(size_t(n));
    if (n > 0) env->GetByteArrayRegion(a, 0, n, reinterpret_cast<jbyte*>(v.data()));
    return v;
}

bool decMode(Term* t, uint16_t mode, bool ansi) {
    GhosttyTerminalModeConfig cfg{ghostty_mode_new(mode, ansi), false};
    if (ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_MODE, &cfg) != GHOSTTY_SUCCESS) return false;
    return cfg.value;
}

// Android's key codes to libghostty's physical keys (US layout positions).
GhosttyKey keyFor(jint code) {
    switch (code) {
    case AKEYCODE_A: return GHOSTTY_KEY_A;
    case AKEYCODE_B: return GHOSTTY_KEY_B;
    case AKEYCODE_C: return GHOSTTY_KEY_C;
    case AKEYCODE_D: return GHOSTTY_KEY_D;
    case AKEYCODE_E: return GHOSTTY_KEY_E;
    case AKEYCODE_F: return GHOSTTY_KEY_F;
    case AKEYCODE_G: return GHOSTTY_KEY_G;
    case AKEYCODE_H: return GHOSTTY_KEY_H;
    case AKEYCODE_I: return GHOSTTY_KEY_I;
    case AKEYCODE_J: return GHOSTTY_KEY_J;
    case AKEYCODE_K: return GHOSTTY_KEY_K;
    case AKEYCODE_L: return GHOSTTY_KEY_L;
    case AKEYCODE_M: return GHOSTTY_KEY_M;
    case AKEYCODE_N: return GHOSTTY_KEY_N;
    case AKEYCODE_O: return GHOSTTY_KEY_O;
    case AKEYCODE_P: return GHOSTTY_KEY_P;
    case AKEYCODE_Q: return GHOSTTY_KEY_Q;
    case AKEYCODE_R: return GHOSTTY_KEY_R;
    case AKEYCODE_S: return GHOSTTY_KEY_S;
    case AKEYCODE_T: return GHOSTTY_KEY_T;
    case AKEYCODE_U: return GHOSTTY_KEY_U;
    case AKEYCODE_V: return GHOSTTY_KEY_V;
    case AKEYCODE_W: return GHOSTTY_KEY_W;
    case AKEYCODE_X: return GHOSTTY_KEY_X;
    case AKEYCODE_Y: return GHOSTTY_KEY_Y;
    case AKEYCODE_Z: return GHOSTTY_KEY_Z;
    case AKEYCODE_0: return GHOSTTY_KEY_DIGIT_0;
    case AKEYCODE_1: return GHOSTTY_KEY_DIGIT_1;
    case AKEYCODE_2: return GHOSTTY_KEY_DIGIT_2;
    case AKEYCODE_3: return GHOSTTY_KEY_DIGIT_3;
    case AKEYCODE_4: return GHOSTTY_KEY_DIGIT_4;
    case AKEYCODE_5: return GHOSTTY_KEY_DIGIT_5;
    case AKEYCODE_6: return GHOSTTY_KEY_DIGIT_6;
    case AKEYCODE_7: return GHOSTTY_KEY_DIGIT_7;
    case AKEYCODE_8: return GHOSTTY_KEY_DIGIT_8;
    case AKEYCODE_9: return GHOSTTY_KEY_DIGIT_9;
    case AKEYCODE_GRAVE: return GHOSTTY_KEY_BACKQUOTE;
    case AKEYCODE_BACKSLASH: return GHOSTTY_KEY_BACKSLASH;
    case AKEYCODE_LEFT_BRACKET: return GHOSTTY_KEY_BRACKET_LEFT;
    case AKEYCODE_RIGHT_BRACKET: return GHOSTTY_KEY_BRACKET_RIGHT;
    case AKEYCODE_COMMA: return GHOSTTY_KEY_COMMA;
    case AKEYCODE_EQUALS: return GHOSTTY_KEY_EQUAL;
    case AKEYCODE_MINUS: return GHOSTTY_KEY_MINUS;
    case AKEYCODE_PERIOD: return GHOSTTY_KEY_PERIOD;
    case AKEYCODE_APOSTROPHE: return GHOSTTY_KEY_QUOTE;
    case AKEYCODE_SEMICOLON: return GHOSTTY_KEY_SEMICOLON;
    case AKEYCODE_SLASH: return GHOSTTY_KEY_SLASH;
    case AKEYCODE_SPACE: return GHOSTTY_KEY_SPACE;
    case AKEYCODE_DEL: return GHOSTTY_KEY_BACKSPACE;          // Android's DEL is backspace
    case AKEYCODE_FORWARD_DEL: return GHOSTTY_KEY_DELETE;
    case AKEYCODE_ENTER: return GHOSTTY_KEY_ENTER;
    case AKEYCODE_TAB: return GHOSTTY_KEY_TAB;
    case AKEYCODE_ESCAPE: return GHOSTTY_KEY_ESCAPE;
    case AKEYCODE_MOVE_HOME: return GHOSTTY_KEY_HOME;
    case AKEYCODE_MOVE_END: return GHOSTTY_KEY_END;
    case AKEYCODE_PAGE_UP: return GHOSTTY_KEY_PAGE_UP;
    case AKEYCODE_PAGE_DOWN: return GHOSTTY_KEY_PAGE_DOWN;
    case AKEYCODE_INSERT: return GHOSTTY_KEY_INSERT;
    case AKEYCODE_DPAD_UP: return GHOSTTY_KEY_ARROW_UP;
    case AKEYCODE_DPAD_DOWN: return GHOSTTY_KEY_ARROW_DOWN;
    case AKEYCODE_DPAD_LEFT: return GHOSTTY_KEY_ARROW_LEFT;
    case AKEYCODE_DPAD_RIGHT: return GHOSTTY_KEY_ARROW_RIGHT;
    case AKEYCODE_F1: return GHOSTTY_KEY_F1;
    case AKEYCODE_F2: return GHOSTTY_KEY_F2;
    case AKEYCODE_F3: return GHOSTTY_KEY_F3;
    case AKEYCODE_F4: return GHOSTTY_KEY_F4;
    case AKEYCODE_F5: return GHOSTTY_KEY_F5;
    case AKEYCODE_F6: return GHOSTTY_KEY_F6;
    case AKEYCODE_F7: return GHOSTTY_KEY_F7;
    case AKEYCODE_F8: return GHOSTTY_KEY_F8;
    case AKEYCODE_F9: return GHOSTTY_KEY_F9;
    case AKEYCODE_F10: return GHOSTTY_KEY_F10;
    case AKEYCODE_F11: return GHOSTTY_KEY_F11;
    case AKEYCODE_F12: return GHOSTTY_KEY_F12;
    case AKEYCODE_NUMPAD_0: return GHOSTTY_KEY_NUMPAD_0;
    case AKEYCODE_NUMPAD_1: return GHOSTTY_KEY_NUMPAD_1;
    case AKEYCODE_NUMPAD_2: return GHOSTTY_KEY_NUMPAD_2;
    case AKEYCODE_NUMPAD_3: return GHOSTTY_KEY_NUMPAD_3;
    case AKEYCODE_NUMPAD_4: return GHOSTTY_KEY_NUMPAD_4;
    case AKEYCODE_NUMPAD_5: return GHOSTTY_KEY_NUMPAD_5;
    case AKEYCODE_NUMPAD_6: return GHOSTTY_KEY_NUMPAD_6;
    case AKEYCODE_NUMPAD_7: return GHOSTTY_KEY_NUMPAD_7;
    case AKEYCODE_NUMPAD_8: return GHOSTTY_KEY_NUMPAD_8;
    case AKEYCODE_NUMPAD_9: return GHOSTTY_KEY_NUMPAD_9;
    case AKEYCODE_NUMPAD_DOT: return GHOSTTY_KEY_NUMPAD_DECIMAL;
    case AKEYCODE_NUMPAD_MULTIPLY: return GHOSTTY_KEY_NUMPAD_MULTIPLY;
    case AKEYCODE_NUMPAD_ADD: return GHOSTTY_KEY_NUMPAD_ADD;
    case AKEYCODE_NUMPAD_DIVIDE: return GHOSTTY_KEY_NUMPAD_DIVIDE;
    case AKEYCODE_NUMPAD_ENTER: return GHOSTTY_KEY_NUMPAD_ENTER;
    case AKEYCODE_NUMPAD_SUBTRACT: return GHOSTTY_KEY_NUMPAD_SUBTRACT;
    case AKEYCODE_NUMPAD_EQUALS: return GHOSTTY_KEY_NUMPAD_EQUAL;
    case AKEYCODE_NUMPAD_COMMA: return GHOSTTY_KEY_NUMPAD_COMMA;
    default: return GHOSTTY_KEY_UNIDENTIFIED;
    }
}

// Cell flags, as TerminalFrame.kt reads them.
enum : jint {
    F_BOLD = 1 << 0,
    F_ITALIC = 1 << 1,
    F_FAINT = 1 << 2,
    F_UNDERLINE = 1 << 3,
    F_STRIKE = 1 << 4,
    F_INVERSE = 1 << 5,
    F_INVISIBLE = 1 << 6,
    F_WIDE = 1 << 7,
    F_SPACER = 1 << 8,
    F_SELECTED = 1 << 9,
    F_BLINK = 1 << 10,
    F_OVERLINE = 1 << 11,
    // Bits 12–14: the underline's style (GHOSTTY_SGR_UNDERLINE_*).
};

// A colour that's set: the RGB with bit 24 on (0 means the default).
constexpr jint HAS = 1 << 24;

// The Kitty graphics protocol's Unicode placeholder: where an image goes,
// never drawn itself (this client doesn't draw Kitty images yet).
constexpr uint32_t kKittyPlaceholder = 0x10EEEE;

// Header slots, as TerminalFrame.kt reads them.
enum {
    H_COLS, H_ROWS, H_BG, H_FG, H_CURSOR_COLOR, H_CURSOR_VISIBLE, H_CURSOR_X, H_CURSOR_Y,
    H_CURSOR_STYLE, H_CURSOR_BLINK, H_DIRTY, H_EXTRA_USED, H_NEED_CELLS, H_NEED_EXTRA, H_COUNT
};

}  // namespace

#define JNI(ret, name) extern "C" JNIEXPORT ret JNICALL Java_com_homegrownsw_hammock_terminal_NativeVt_##name

JNI(jlong, create)(JNIEnv*, jobject, jint cols, jint rows) {
    auto* t = new Term();
    t->cols = uint16_t(cols < 1 ? 1 : cols);
    t->rowsN = uint16_t(rows < 1 ? 1 : rows);
    if (ghostty_terminal_new(nullptr, &t->terminal, t->cols, t->rowsN) != GHOSTTY_SUCCESS) {
        delete t;
        return 0;
    }
    configure(t);
    ghostty_render_state_new(nullptr, &t->render);
    ghostty_render_state_row_iterator_new(nullptr, &t->rows);
    ghostty_render_state_row_cells_new(nullptr, &t->cells);
    ghostty_key_encoder_new(nullptr, &t->keyEncoder);
    ghostty_key_event_new(nullptr, &t->keyEvent);
    ghostty_mouse_encoder_new(nullptr, &t->mouseEncoder);
    ghostty_mouse_event_new(nullptr, &t->mouseEvent);
    return reinterpret_cast<jlong>(t);
}

JNI(void, free)(JNIEnv*, jobject, jlong h) {
    Term* t = T(h);
    if (!t) return;
    if (t->mouseEvent) ghostty_mouse_event_free(t->mouseEvent);
    if (t->mouseEncoder) ghostty_mouse_encoder_free(t->mouseEncoder);
    if (t->keyEvent) ghostty_key_event_free(t->keyEvent);
    if (t->keyEncoder) ghostty_key_encoder_free(t->keyEncoder);
    if (t->cells) ghostty_render_state_row_cells_free(t->cells);
    if (t->rows) ghostty_render_state_row_iterator_free(t->rows);
    if (t->render) ghostty_render_state_free(t->render);
    if (t->terminal) ghostty_terminal_free(t->terminal);
    delete t;
}

JNI(void, write)(JNIEnv* env, jobject, jlong h, jbyteArray data, jint off, jint len) {
    Term* t = T(h);
    if (len <= 0) return;
    // Large snapshots: copied in pieces rather than pinned, so the GC isn't held up.
    uint8_t buf[16384];
    jint done = 0;
    while (done < len) {
        jint n = len - done < jint(sizeof(buf)) ? len - done : jint(sizeof(buf));
        env->GetByteArrayRegion(data, off + done, n, reinterpret_cast<jbyte*>(buf));
        ghostty_terminal_vt_write(t->terminal, buf, size_t(n));
        done += n;
    }
}

JNI(void, resize)(JNIEnv*, jobject, jlong h, jint cols, jint rows, jint cw, jint ch) {
    Term* t = T(h);
    if (cols <= 0 || rows <= 0) return;
    t->cols = uint16_t(cols);
    t->rowsN = uint16_t(rows);
    if (cw > 0 && ch > 0) {
        t->cellW = uint32_t(cw);
        t->cellH = uint32_t(ch);
    }
    ghostty_terminal_resize(t->terminal, t->cols, t->rowsN, t->cellW, t->cellH);
}

JNI(void, reset)(JNIEnv*, jobject, jlong h, jint cols, jint rows) {
    Term* t = T(h);
    ghostty_terminal_reset(t->terminal);
    applyTheme(t);
    if (cols > 0 && rows > 0) {
        t->cols = uint16_t(cols);
        t->rowsN = uint16_t(rows);
    }
    ghostty_terminal_resize(t->terminal, t->cols, t->rowsN, t->cellW, t->cellH);
}

JNI(void, setTheme)(JNIEnv* env, jobject, jlong h, jint bg, jint fg, jint cursor, jintArray ansi) {
    Term* t = T(h);
    t->bg = rgb(bg);
    t->fg = rgb(fg);
    t->cursor = rgb(cursor);
    // The theme's 16 colours, then the standard cube and greys.
    jint a[16] = {0};
    jsize n = env->GetArrayLength(ansi);
    env->GetIntArrayRegion(ansi, 0, n < 16 ? n : 16, a);
    for (int i = 0; i < 16; i++) t->palette[i] = rgb(a[i]);
    static const uint8_t steps[6] = {0, 95, 135, 175, 215, 255};
    int p = 16;
    for (int r = 0; r < 6; r++)
        for (int g = 0; g < 6; g++)
            for (int b = 0; b < 6; b++) t->palette[p++] = GhosttyColorRgb{steps[r], steps[g], steps[b]};
    for (int i = 0; i < 24; i++) {
        uint8_t v = uint8_t(8 + i * 10);
        t->palette[p++] = GhosttyColorRgb{v, v, v};
    }
    t->hasTheme = true;
    applyTheme(t);
}

JNI(void, setDefaultCursor)(JNIEnv*, jobject, jlong h, jint style, jboolean blink) {
    Term* t = T(h);
    GhosttyTerminalCursorStyle s;
    switch (style) {
    case 1: s = GHOSTTY_TERMINAL_CURSOR_STYLE_BAR; break;
    case 2: s = GHOSTTY_TERMINAL_CURSOR_STYLE_UNDERLINE; break;
    case 3: s = GHOSTTY_TERMINAL_CURSOR_STYLE_BLOCK_HOLLOW; break;
    default: s = GHOSTTY_TERMINAL_CURSOR_STYLE_BLOCK; break;
    }
    bool b = blink;
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_STYLE, &s);
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_DEFAULT_CURSOR_BLINK, &b);
}

// mode: 0 by delta rows (negative: back into scrollback), 1 to the bottom, 2 to the top.
JNI(void, scroll)(JNIEnv*, jobject, jlong h, jint mode, jint delta) {
    Term* t = T(h);
    GhosttyTerminalScrollViewport b{};
    switch (mode) {
    case 1: b.tag = GHOSTTY_SCROLL_VIEWPORT_BOTTOM; break;
    case 2: b.tag = GHOSTTY_SCROLL_VIEWPORT_TOP; break;
    default:
        b.tag = GHOSTTY_SCROLL_VIEWPORT_DELTA;
        b.value.delta = intptr_t(delta);
        break;
    }
    ghostty_terminal_scroll_viewport(t->terminal, b);
}

// Fills out[0..2] with the scrollbar: total rows, the viewport's offset, its length.
JNI(void, scrollbar)(JNIEnv* env, jobject, jlong h, jlongArray out) {
    Term* t = T(h);
    GhosttyTerminalScrollbar s{};
    if (ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_SCROLLBAR, &s) != GHOSTTY_SUCCESS) return;
    jlong v[3] = {jlong(s.total), jlong(s.offset), jlong(s.len)};
    env->SetLongArrayRegion(out, 0, 3, v);
}

JNI(jboolean, mode)(JNIEnv*, jobject, jlong h, jint mode, jboolean ansi) {
    return decMode(T(h), uint16_t(mode), ansi);
}

// Bits: 1 the alternate screen, 2 mouse tracking, 4 bracketed paste,
// 8 the viewport at the bottom (following output).
JNI(jint, state)(JNIEnv*, jobject, jlong h) {
    Term* t = T(h);
    jint s = 0;
    GhosttyTerminalScreen screen = GHOSTTY_TERMINAL_SCREEN_PRIMARY;
    ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_ACTIVE_SCREEN, &screen);
    if (screen == GHOSTTY_TERMINAL_SCREEN_ALTERNATE) s |= 1;
    bool tracking = false;
    ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_MOUSE_TRACKING, &tracking);
    if (tracking) s |= 2;
    if (decMode(t, 2004, false)) s |= 4;
    bool active = true;
    ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_VIEWPORT_ACTIVE, &active);
    if (active) s |= 8;
    return s;
}

JNI(jbyteArray, title)(JNIEnv* env, jobject, jlong h) {
    GhosttyString s{};
    if (ghostty_terminal_get(T(h)->terminal, GHOSTTY_TERMINAL_DATA_TITLE, &s) != GHOSTTY_SUCCESS || !s.ptr) return bytes(env, "", 0);
    return bytes(env, s.ptr, s.len);
}

// Copies the viewport into the arrays TerminalFrame.kt keeps: a header (see
// H_*), four ints a cell (first codepoint, or -(1+offset) into `extra` for a
// cluster of several; fg; bg; flags), and each row's dirty flag. Returns 1,
// with the sizes needed in the header, when an array is too small. `clean`
// marks everything drawn (libghostty's dirty tracking: what changed since).
JNI(jint, frame)(JNIEnv* env, jobject, jlong h, jintArray headerA, jintArray cellsA, jintArray extraA,
                  jintArray rowFlagsA, jboolean clean) {
    Term* t = T(h);
    jint header[H_COUNT] = {0};
    if (ghostty_render_state_update(t->render, t->terminal) != GHOSTTY_SUCCESS) return -1;

    uint16_t cols = 0, rows = 0;
    ghostty_render_state_get(t->render, GHOSTTY_RENDER_STATE_DATA_COLS, &cols);
    ghostty_render_state_get(t->render, GHOSTTY_RENDER_STATE_DATA_ROWS, &rows);
    header[H_COLS] = cols;
    header[H_ROWS] = rows;
    jsize cellCap = env->GetArrayLength(cellsA);
    jsize extraCap = env->GetArrayLength(extraA);
    jsize rowCap = env->GetArrayLength(rowFlagsA);
    if (cellCap < jsize(cols) * rows * 4 || rowCap < rows) {
        header[H_NEED_CELLS] = jint(cols) * rows * 4;
        header[H_NEED_EXTRA] = extraCap;
        env->SetIntArrayRegion(headerA, 0, H_COUNT, header);
        return 1;
    }

    GhosttyRenderStateColors colors = GHOSTTY_INIT_SIZED(GhosttyRenderStateColors);
    if (ghostty_render_state_get(t->render, GHOSTTY_RENDER_STATE_DATA_COLORS, &colors) == GHOSTTY_SUCCESS) {
        header[H_BG] = pack(colors.background);
        header[H_FG] = pack(colors.foreground);
        header[H_CURSOR_COLOR] = colors.cursor_has_value ? (pack(colors.cursor) | HAS) : 0;
    }
    GhosttyRenderStateCursor cursor = GHOSTTY_INIT_SIZED(GhosttyRenderStateCursor);
    if (ghostty_render_state_get(t->render, GHOSTTY_RENDER_STATE_DATA_CURSOR, &cursor) == GHOSTTY_SUCCESS) {
        header[H_CURSOR_VISIBLE] = cursor.visible && cursor.viewport_has_value;
        header[H_CURSOR_X] = cursor.viewport_x;
        header[H_CURSOR_Y] = cursor.viewport_y;
        header[H_CURSOR_BLINK] = cursor.blinking;
        switch (cursor.visual_style) {
        case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BAR: header[H_CURSOR_STYLE] = 1; break;
        case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_UNDERLINE: header[H_CURSOR_STYLE] = 2; break;
        case GHOSTTY_RENDER_STATE_CURSOR_VISUAL_STYLE_BLOCK_HOLLOW: header[H_CURSOR_STYLE] = 3; break;
        default: header[H_CURSOR_STYLE] = 0; break;
        }
    }
    GhosttyRenderStateDirty dirty = GHOSTTY_RENDER_STATE_DIRTY_FULL;
    ghostty_render_state_get(t->render, GHOSTTY_RENDER_STATE_DATA_DIRTY, &dirty);
    header[H_DIRTY] = dirty == GHOSTTY_RENDER_STATE_DIRTY_FALSE ? 0 : dirty == GHOSTTY_RENDER_STATE_DIRTY_PARTIAL ? 1 : 2;

    if (ghostty_render_state_get(t->render, GHOSTTY_RENDER_STATE_DATA_ROW_ITERATOR, &t->rows) != GHOSTTY_SUCCESS) {
        env->SetIntArrayRegion(headerA, 0, H_COUNT, header);
        return -1;
    }

    // Written straight into the Java arrays, released once at the end.
    jint* cells = static_cast<jint*>(env->GetPrimitiveArrayCritical(cellsA, nullptr));
    std::vector<jint> extra;
    std::vector<jint> rowFlags(rows, 0);
    std::memset(cells, 0, sizeof(jint) * size_t(cols) * rows * 4);
    uint32_t cps[16];
    std::vector<uint32_t> big;
    int y = 0;
    while (ghostty_render_state_row_iterator_next(t->rows) && y < rows) {
        bool rowDirty = true;
        ghostty_render_state_row_get(t->rows, GHOSTTY_RENDER_STATE_ROW_DATA_DIRTY, &rowDirty);
        rowFlags[y] = rowDirty ? 1 : 0;
        if (ghostty_render_state_row_get(t->rows, GHOSTTY_RENDER_STATE_ROW_DATA_CELLS, &t->cells) != GHOSTTY_SUCCESS) {
            y++;
            continue;
        }
        int x = 0;
        while (ghostty_render_state_row_cells_next(t->cells) && x < cols) {
            jint* c = cells + (size_t(y) * cols + x) * 4;
            jint flags = 0;
            GhosttyCell raw = 0;
            if (ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_RAW, &raw) == GHOSTTY_SUCCESS) {
                GhosttyCellWide wide = GHOSTTY_CELL_WIDE_NARROW;
                ghostty_cell_get(raw, GHOSTTY_CELL_DATA_WIDE, &wide);
                if (wide == GHOSTTY_CELL_WIDE_WIDE) flags |= F_WIDE;
                if (wide == GHOSTTY_CELL_WIDE_SPACER_TAIL || wide == GHOSTTY_CELL_WIDE_SPACER_HEAD) flags |= F_SPACER;
            }
            uint32_t glen = 0;
            ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_LEN, &glen);
            if (glen > 0) {
                uint32_t* buf = cps;
                if (glen > 16) {
                    big.resize(glen);
                    buf = big.data();
                }
                ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_GRAPHEMES_BUF, buf);
                if (buf[0] != kKittyPlaceholder) {
                    if (glen == 1) {
                        c[0] = jint(buf[0]);
                    } else {
                        c[0] = -(jint(extra.size()) + 1);
                        extra.push_back(jint(glen));
                        for (uint32_t i = 0; i < glen; i++) extra.push_back(jint(buf[i]));
                    }
                }
            }
            bool hasStyle = false;
            ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_HAS_STYLING, &hasStyle);
            if (hasStyle) {
                GhosttyStyle style = GHOSTTY_INIT_SIZED(GhosttyStyle);
                if (ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_STYLE, &style) == GHOSTTY_SUCCESS) {
                    if (style.bold) flags |= F_BOLD;
                    if (style.italic) flags |= F_ITALIC;
                    if (style.faint) flags |= F_FAINT;
                    if (style.blink) flags |= F_BLINK;
                    if (style.inverse) flags |= F_INVERSE;
                    if (style.invisible) flags |= F_INVISIBLE;
                    if (style.strikethrough) flags |= F_STRIKE;
                    if (style.overline) flags |= F_OVERLINE;
                    if (style.underline != GHOSTTY_SGR_UNDERLINE_NONE) flags |= F_UNDERLINE | ((jint(style.underline) & 7) << 12);
                }
                GhosttyColorRgb c3{};
                if (ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_FG_COLOR, &c3) == GHOSTTY_SUCCESS) c[1] = pack(c3) | HAS;
            }
            // Asked of every cell, styled or not: a cell erased in a colour
            // (an erase with a background set) has the colour but no style.
            GhosttyColorRgb bg{};
            if (ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_BG_COLOR, &bg) == GHOSTTY_SUCCESS) c[2] = pack(bg) | HAS;
            bool selected = false;
            ghostty_render_state_row_cells_get(t->cells, GHOSTTY_RENDER_STATE_ROW_CELLS_DATA_SELECTED, &selected);
            if (selected) flags |= F_SELECTED;
            c[3] = flags;
            x++;
        }
        y++;
    }
    env->ReleasePrimitiveArrayCritical(cellsA, cells, 0);

    header[H_EXTRA_USED] = jint(extra.size());
    if (jsize(extra.size()) > extraCap) {
        // Too many clusters for `extra`: the caller grows it and asks again
        // (without cleaning, so the dirty rows are still there to read).
        header[H_NEED_CELLS] = jint(cols) * rows * 4;
        header[H_NEED_EXTRA] = jint(extra.size());
        env->SetIntArrayRegion(headerA, 0, H_COUNT, header);
        return 1;
    }
    if (!extra.empty()) env->SetIntArrayRegion(extraA, 0, jsize(extra.size()), extra.data());
    if (rows > 0) env->SetIntArrayRegion(rowFlagsA, 0, rows, rowFlags.data());
    env->SetIntArrayRegion(headerA, 0, H_COUNT, header);
    if (clean) ghostty_render_state_clean(t->render);
    return 0;
}

// Encodes a key press for the PTY, honouring the program's keyboard modes
// (application cursor keys, the kitty keyboard protocol, …). `code` is an
// Android key code; `text` what it types (UTF-8, may be null).
JNI(jbyteArray, encodeKey)(JNIEnv* env, jobject, jlong h, jint code, jint mods, jbyteArray text, jint unshifted, jint action) {
    Term* t = T(h);
    ghostty_key_encoder_setopt_from_terminal(t->keyEncoder, t->terminal);
    GhosttyKeyAction a = action == 2 ? GHOSTTY_KEY_ACTION_REPEAT : action == 0 ? GHOSTTY_KEY_ACTION_RELEASE : GHOSTTY_KEY_ACTION_PRESS;
    ghostty_key_event_set_action(t->keyEvent, a);
    ghostty_key_event_set_key(t->keyEvent, keyFor(code));
    ghostty_key_event_set_mods(t->keyEvent, GhosttyMods(mods));
    std::vector<char> s = utf8(env, text);
    // Shift that produced the text (`:` from shift-;) is consumed, as
    // Ghostty's own app reports it: otherwise the kitty protocol sends
    // shift-; as ESC[59;2u and neovim never sees a `:`.
    GhosttyMods consumed = s.empty() ? 0 : GhosttyMods(mods & GHOSTTY_MODS_SHIFT);
    ghostty_key_event_set_consumed_mods(t->keyEvent, consumed);
    ghostty_key_event_set_unshifted_codepoint(t->keyEvent, uint32_t(unshifted));
    ghostty_key_event_set_composing(t->keyEvent, false);
    ghostty_key_event_set_utf8(t->keyEvent, s.empty() ? nullptr : s.data(), s.size());
    char out[128];
    size_t len = 0;
    if (ghostty_key_encoder_encode(t->keyEncoder, t->keyEvent, out, sizeof(out), &len) != GHOSTTY_SUCCESS || len == 0) return nullptr;
    return bytes(env, out, len);
}

JNI(jboolean, pasteIsSafe)(JNIEnv* env, jobject, jbyteArray text) {
    std::vector<char> s = utf8(env, text);
    if (s.empty()) return true;
    return ghostty_paste_is_safe(s.data(), s.size());
}

// Pasted text, bracketed if the program asked for it (mode 2004).
JNI(jbyteArray, encodePaste)(JNIEnv* env, jobject, jlong h, jbyteArray text) {
    Term* t = T(h);
    std::vector<char> s = utf8(env, text);
    bool bracketed = decMode(t, 2004, false);
    std::vector<char> out(s.size() + 32);
    size_t written = 0;
    GhosttyResult r = ghostty_paste_encode(s.data(), s.size(), bracketed, out.data(), out.size(), &written);
    if (r == GHOSTTY_OUT_OF_SPACE) {
        out.resize(written);
        r = ghostty_paste_encode(s.data(), s.size(), bracketed, out.data(), out.size(), &written);
    }
    if (r != GHOSTTY_SUCCESS) return bytes(env, s.data(), s.size());
    return bytes(env, out.data(), written);
}

// Encodes a mouse event for a program that enabled mouse reporting; null
// when it isn't tracking this kind of event. action: 0 press, 1 release,
// 2 motion. button: 0 none, 1 left, 2 right, 3 middle, 4/5 the wheel up/down.
// geometry: the view's width and height, the cell's width and height, and
// the padding, all in pixels.
JNI(jbyteArray, encodeMouse)(JNIEnv* env, jobject, jlong h, jint action, jint button, jfloat x, jfloat y, jint mods,
                              jfloatArray geometry) {
    Term* t = T(h);
    float g[5] = {0};
    env->GetFloatArrayRegion(geometry, 0, 5, g);
    ghostty_mouse_encoder_setopt_from_terminal(t->mouseEncoder, t->terminal);
    // The encoder works in whole pixels, but cells are often fractional
    // (19.8 px wide): truncated, every column came out a little narrow and a
    // click far along a line landed cells right of it. Find the cell with
    // the real size, then give the encoder the same spot on a grid of whole
    // pixels.
    float width = g[0], height = g[1], cw = g[2] > 1 ? g[2] : 1, ch = g[3] > 1 ? g[3] : 1, pad = g[4];
    float cwPx = cw < 1 ? 1 : float(int(cw + 0.5f)), chPx = ch < 1 ? 1 : float(int(ch + 0.5f)), padPx = float(int(pad + 0.5f));
    if (cwPx < 1) cwPx = 1;
    if (chPx < 1) chPx = 1;
    float cols = float(int((width - 2 * pad) / cw)), rows = float(int((height - 2 * pad) / ch));
    float px = padPx + (x - pad) / cw * cwPx, py = padPx + (y - pad) / ch * chPx;
    GhosttyMouseEncoderSize size = GHOSTTY_INIT_SIZED(GhosttyMouseEncoderSize);
    float sw = 2 * padPx + cols * cwPx, sh = 2 * padPx + rows * chPx;
    size.screen_width = uint32_t(sw < 1 ? 1 : sw);
    size.screen_height = uint32_t(sh < 1 ? 1 : sh);
    size.cell_width = uint32_t(cwPx);
    size.cell_height = uint32_t(chPx);
    size.padding_top = uint32_t(padPx);
    size.padding_left = uint32_t(padPx);
    ghostty_mouse_encoder_setopt(t->mouseEncoder, GHOSTTY_MOUSE_ENCODER_OPT_SIZE, &size);
    // Button-event tracking (mode 1002) only reports motion while a button
    // is held; setopt_from_terminal leaves that state to us.
    if (button >= 1 && button <= 3) {
        if (action == 0) t->mouseButtonDown = true;
        if (action == 1) t->mouseButtonDown = false;
    }
    bool down = t->mouseButtonDown;
    ghostty_mouse_encoder_setopt(t->mouseEncoder, GHOSTTY_MOUSE_ENCODER_OPT_ANY_BUTTON_PRESSED, &down);
    ghostty_mouse_event_set_action(t->mouseEvent, action == 1 ? GHOSTTY_MOUSE_ACTION_RELEASE
                                                  : action == 2 ? GHOSTTY_MOUSE_ACTION_MOTION
                                                                : GHOSTTY_MOUSE_ACTION_PRESS);
    if (button > 0) {
        ghostty_mouse_event_set_button(t->mouseEvent, GhosttyMouseButton(button));
    } else {
        ghostty_mouse_event_clear_button(t->mouseEvent);
    }
    ghostty_mouse_event_set_mods(t->mouseEvent, GhosttyMods(mods));
    ghostty_mouse_event_set_position(t->mouseEvent, GhosttyMousePosition{px, py});
    char out[64];
    size_t len = 0;
    if (ghostty_mouse_encoder_encode(t->mouseEncoder, t->mouseEvent, out, sizeof(out), &len) != GHOSTTY_SUCCESS || len == 0) return nullptr;
    return bytes(env, out, len);
}

// Touch selection, as TerminalEngine+Selection.swift does it on the iPhone.
// Cells are in the viewport; a selection's ends can be off the screen.

namespace {

bool gridRef(Term* t, GhosttyPointTag tag, uint16_t x, uint32_t y, GhosttyGridRef* out) {
    *out = GHOSTTY_INIT_SIZED(GhosttyGridRef);
    GhosttyPoint pt{};
    pt.tag = tag;
    pt.value.coordinate = GhosttyPointCoordinate{x, y};
    return ghostty_terminal_grid_ref(t->terminal, pt, out) == GHOSTTY_SUCCESS;
}

// A viewport cell, held to the screen.
bool viewportRef(Term* t, jint x, jint y, GhosttyGridRef* out) {
    jint cx = x < 0 ? 0 : (x >= t->cols ? t->cols - 1 : x);
    jint cy = y < 0 ? 0 : (y >= t->rowsN ? t->rowsN - 1 : y);
    return gridRef(t, GHOSTTY_POINT_TAG_VIEWPORT, uint16_t(cx), uint32_t(cy), out);
}

bool screenPoint(Term* t, GhosttyGridRef ref, GhosttyPointCoordinate* out) {
    return ghostty_terminal_point_from_grid_ref(t->terminal, &ref, GHOSTTY_POINT_TAG_SCREEN, out) == GHOSTTY_SUCCESS;
}

bool screenRef(Term* t, GhosttyPointCoordinate p, GhosttyGridRef* out) {
    return gridRef(t, GHOSTTY_POINT_TAG_SCREEN, p.x, p.y, out);
}

bool before(GhosttyPointCoordinate a, GhosttyPointCoordinate b) { return a.y != b.y ? a.y < b.y : a.x < b.x; }

GhosttySelection selection(GhosttyGridRef start, GhosttyGridRef end) {
    GhosttySelection s = GHOSTTY_INIT_SIZED(GhosttySelection);
    s.start = start;
    s.end = end;
    s.rectangle = false;
    return s;
}

void install(Term* t, GhosttySelection s) { ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, &s); }

// The current selection, its ends in reading order.
bool ordered(Term* t, GhosttySelection* out) {
    GhosttySelection s = GHOSTTY_INIT_SIZED(GhosttySelection);
    if (ghostty_terminal_get(t->terminal, GHOSTTY_TERMINAL_DATA_SELECTION, &s) != GHOSTTY_SUCCESS) return false;
    *out = GHOSTTY_INIT_SIZED(GhosttySelection);
    return ghostty_terminal_selection_ordered(t->terminal, &s, GHOSTTY_SELECTION_ORDER_FORWARD, out) == GHOSTTY_SUCCESS;
}

// The word at a cell, first cell to last, in screen coordinates.
bool wordAt(Term* t, GhosttyGridRef ref, GhosttyPointCoordinate* lo, GhosttyPointCoordinate* hi) {
    GhosttyTerminalSelectWordOptions o = GHOSTTY_INIT_SIZED(GhosttyTerminalSelectWordOptions);
    o.ref = ref;
    GhosttySelection s = GHOSTTY_INIT_SIZED(GhosttySelection);
    if (ghostty_terminal_select_word(t->terminal, &o, &s) != GHOSTTY_SUCCESS) return false;
    GhosttyPointCoordinate a{}, b{};
    if (!screenPoint(t, s.start, &a) || !screenPoint(t, s.end, &b)) return false;
    *lo = before(b, a) ? b : a;
    *hi = before(b, a) ? a : b;
    return true;
}

jbyteArray format(JNIEnv* env, Term* t, const GhosttySelection* sel) {
    GhosttyTerminalSelectionFormatOptions o = GHOSTTY_INIT_SIZED(GhosttyTerminalSelectionFormatOptions);
    o.emit = GHOSTTY_FORMATTER_FORMAT_PLAIN;
    o.unwrap = true;  // soft-wrapped lines joined
    o.trim = true;    // trailing spaces trimmed
    o.selection = sel;
    uint8_t* ptr = nullptr;
    size_t len = 0;
    if (ghostty_terminal_selection_format_alloc(t->terminal, nullptr, o, &ptr, &len) != GHOSTTY_SUCCESS || !ptr || len == 0) {
        if (ptr) ghostty_free(nullptr, ptr, len);
        return nullptr;
    }
    jbyteArray out = bytes(env, ptr, len);
    ghostty_free(nullptr, ptr, len);
    return out;
}

}  // namespace

// Selects the word at a cell; false, selecting nothing, on a blank.
JNI(jboolean, selectWord)(JNIEnv*, jobject, jlong h, jint x, jint y) {
    Term* t = T(h);
    t->hasAnchor = false;
    GhosttyGridRef ref;
    GhosttyPointCoordinate lo{}, hi{};
    GhosttyGridRef a, b;
    if (!viewportRef(t, x, y, &ref) || !wordAt(t, ref, &lo, &hi) || !screenRef(t, lo, &a) || !screenRef(t, hi, &b)) {
        ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, nullptr);
        return false;
    }
    t->hasAnchor = true;
    t->anchorStart = lo;
    t->anchorEnd = hi;
    install(t, selection(a, b));
    return true;
}

// From the word selectWord selected to the word at a cell (or the cell, between words).
JNI(void, extendWord)(JNIEnv*, jobject, jlong h, jint x, jint y) {
    Term* t = T(h);
    if (!t->hasAnchor) return;
    GhosttyGridRef a, b, ref;
    GhosttyPointCoordinate here{};
    if (!screenRef(t, t->anchorStart, &a) || !screenRef(t, t->anchorEnd, &b) || !viewportRef(t, x, y, &ref) || !screenPoint(t, ref, &here)) return;
    GhosttyPointCoordinate lo = here, hi = here;
    wordAt(t, ref, &lo, &hi);
    GhosttyGridRef r;
    if (before(lo, t->anchorStart)) {
        install(t, selection(screenRef(t, lo, &r) ? r : ref, b));
    } else if (before(t->anchorEnd, hi)) {
        install(t, selection(a, screenRef(t, hi, &r) ? r : ref));
    } else {
        install(t, selection(a, b));
    }
}

// Selects the screen as it shows (the Mac's Select All).
JNI(void, selectScreen)(JNIEnv*, jobject, jlong h) {
    Term* t = T(h);
    t->hasAnchor = false;
    GhosttyGridRef a, b;
    if (!viewportRef(t, 0, 0, &a) || !viewportRef(t, t->cols - 1, t->rowsN - 1, &b)) return;
    install(t, selection(a, b));
}

JNI(void, clearSelection)(JNIEnv*, jobject, jlong h) {
    Term* t = T(h);
    t->hasAnchor = false;
    ghostty_terminal_set(t->terminal, GHOSTTY_TERMINAL_OPT_SELECTION, nullptr);
}

// Moves one end of the selection (edge 0 the start, 1 the end) to a cell,
// keeping the other. Returns which end the moved one is now.
JNI(jint, moveSelection)(JNIEnv*, jobject, jlong h, jint edge, jint x, jint y) {
    Term* t = T(h);
    t->hasAnchor = false;
    GhosttySelection s;
    GhosttyGridRef ref;
    if (!ordered(t, &s) || !viewportRef(t, x, y, &ref)) return edge;
    GhosttyGridRef fixed = edge == 0 ? s.end : s.start;
    GhosttyPointCoordinate fixedAt{}, here{};
    if (!screenPoint(t, fixed, &fixedAt) || !screenPoint(t, ref, &here)) return edge;
    if (before(here, fixedAt)) {
        install(t, selection(ref, fixed));
        return 0;
    }
    install(t, selection(fixed, ref));
    return 1;
}

// The selection's first and last cells into out[0..3] (x, y, x, y), in the
// viewport: a row above the screen is negative. False when there's none.
JNI(jboolean, selectionBounds)(JNIEnv* env, jobject, jlong h, jintArray out) {
    Term* t = T(h);
    GhosttySelection s;
    GhosttyGridRef top;
    GhosttyPointCoordinate topAt{}, a{}, b{};
    if (!ordered(t, &s) || !viewportRef(t, 0, 0, &top) || !screenPoint(t, top, &topAt) || !screenPoint(t, s.start, &a) ||
        !screenPoint(t, s.end, &b)) return false;
    jint v[4] = {jint(a.x), jint(a.y) - jint(topAt.y), jint(b.x), jint(b.y) - jint(topAt.y)};
    env->SetIntArrayRegion(out, 0, 4, v);
    return true;
}

// The selected text (UTF-8), or null.
JNI(jbyteArray, selectionText)(JNIEnv* env, jobject, jlong h) { return format(env, T(h), nullptr); }

// The screen as it shows, as text the way a selection of it copies (Copy Screen).
JNI(jbyteArray, screenText)(JNIEnv* env, jobject, jlong h) {
    Term* t = T(h);
    GhosttyGridRef a, b;
    if (!viewportRef(t, 0, 0, &a) || !viewportRef(t, t->cols - 1, t->rowsN - 1, &b)) return nullptr;
    GhosttySelection s = selection(a, b);
    return format(env, t, &s);
}
