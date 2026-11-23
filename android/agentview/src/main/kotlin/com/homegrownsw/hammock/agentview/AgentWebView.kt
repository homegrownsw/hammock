package com.homegrownsw.hammock.agentview

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.media.MediaMetadataRetriever
import android.util.Base64
import android.util.Log
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.Reply
import androidx.compose.material.icons.automirrored.outlined.Assignment
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.automirrored.outlined.Undo
import androidx.compose.material.icons.automirrored.outlined.WrapText
import androidx.compose.material.icons.filled.Cancel
import androidx.compose.material.icons.filled.CheckCircle
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.outlined.ArrowDownward
import androidx.compose.material.icons.outlined.AttachFile
import androidx.compose.material.icons.outlined.AutoAwesome
import androidx.compose.material.icons.outlined.Build
import androidx.compose.material.icons.outlined.ChatBubbleOutline
import androidx.compose.material.icons.outlined.Check
import androidx.compose.material.icons.outlined.Checklist
import androidx.compose.material.icons.outlined.ChevronRight
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.Description
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.ExpandMore
import androidx.compose.material.icons.outlined.Extension
import androidx.compose.material.icons.outlined.Forum
import androidx.compose.material.icons.outlined.Groups
import androidx.compose.material.icons.outlined.Layers
import androidx.compose.material.icons.outlined.Movie
import androidx.compose.material.icons.outlined.Photo
import androidx.compose.material.icons.outlined.PostAdd
import androidx.compose.material.icons.outlined.Psychology
import androidx.compose.material.icons.outlined.Public
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material.icons.outlined.Terminal
import androidx.compose.material.icons.outlined.VerticalSplit
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.graphics.vector.PathNode
import androidx.compose.ui.graphics.vector.VectorGroup
import androidx.compose.ui.graphics.vector.VectorNode
import androidx.compose.ui.graphics.vector.VectorPath
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.platform.LocalFocusManager
import androidx.compose.ui.platform.LocalSoftwareKeyboardController
import androidx.compose.ui.platform.LocalUriHandler
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import com.homegrownsw.hammock.panes.PageBridge
import hammock.v1.Hammock.AgentBlock
import hammock.v1.Hammock.AgentPrompt
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File

/**
 * The conversation drawn by the page the Mac and the iPhone draw it with
 * (apple/Editor/src/agent.js, built into agent.html), in a WebView: the
 * page owns the scrolling, and this sends it the rows that changed, as
 * AgentWebView.swift does, and acts on what it says you did.
 */
@Composable
internal fun WebConversation(session: AgentSession, state: AgentViewState, modifier: Modifier) {
    val context = LocalContext.current
    val colors = LocalAgentColors.current
    val fonts = LocalAgentFonts.current
    val density = LocalDensity.current
    val typography = MaterialTheme.typography
    val cs = MaterialTheme.colorScheme
    val links = rememberUpdatedState(LocalUriHandler.current)
    val openImage = rememberUpdatedState(LocalOpenImage.current)
    val clipboard = LocalClipboardManager.current
    val keyboard = LocalSoftwareKeyboardController.current
    val focus = LocalFocusManager.current
    val scope = rememberCoroutineScope()

    val bridge = remember(session) {
        AgentWebBridge(context, session, scope).apply {
            onLink = { links.value.openUri(it) }
            onCopy = { clipboard.setText(AnnotatedString(it)) }
            onOpenImage = { openImage.value(it.asImageBitmap()) }
            onDismissKeyboard = { keyboard?.hide(); focus.clearFocus() }
        }
    }
    DisposableEffect(bridge) { onDispose { bridge.page.destroy() } }

    val config = remember(colors, fonts, density.fontScale, typography, cs, session.agentName) {
        AgentWebBridge.config(colors, fonts, density.fontScale, typography, cs, session.agentName)
    }
    LaunchedEffect(bridge, config) { bridge.configure(config) }
    LaunchedEffect(bridge, state) { bridge.update(state) }

    Box(modifier) {
        AndroidView(factory = { bridge.page.webView }, modifier = Modifier.matchParentSize())
        bridge.page.problem?.let { Text(it, color = colors.secondary, modifier = Modifier.align(Alignment.Center).padding(24.dp)) }
    }
}

internal class AgentWebBridge(context: Context, private val session: AgentSession, private val scope: CoroutineScope) {
    var onLink: (String) -> Unit = {}
    var onCopy: (String) -> Unit = {}
    var onOpenImage: (Bitmap) -> Unit = {}
    var onDismissKeyboard: () -> Unit = {}

    // Transparent: the conversation's own background shows through, as it does
    // behind the native rows.
    val page = PageBridge(context, "agent.html", android.graphics.Color.TRANSPARENT, ::onMessage)

    private var config: JSONObject? = null
    private var state: AgentViewState? = null
    // What the page has (rows by id, their order, the tail and the rest), so
    // each update sends only what changed.
    private var sentItems = HashMap<String, AgentItem>()
    private var sentOrder: List<String> = emptyList()
    private var sentTail: String? = null
    private var sentMeta: String? = null
    private var sentPending = 0
    /** Replies already shown arriving (revealed once, not again when redrawn). */
    private val revealed = HashSet<String>()

    fun configure(c: JSONObject) {
        config = c
        page.call("configure", c)
    }

    fun update(s: AgentViewState) {
        state = s
        push()
    }

    private fun push(replace: Boolean = false) {
        val s = state ?: return
        if (!page.ready) return
        val u = JSONObject()
        if (replace) {
            u.put("replace", true)
            sentItems.clear(); sentOrder = emptyList(); sentTail = null; sentMeta = null
        }
        val order = s.items.map { it.id }
        val rows = JSONArray()
        for (it in s.items) if (sentItems[it.id] != it) rows.put(row(it))
        if (replace || order != sentOrder || rows.length() > 0) {
            u.put("order", JSONArray(order))
            u.put("rows", rows)
        }
        sentItems = HashMap(s.items.associateBy { it.id })
        sentOrder = order
        val meta = JSONObject()
            .put("hasMore", s.hasMore).put("loadingOlder", s.loadingOlder)
            .put("error", if (s.error.isNotEmpty() && s.blocks.isEmpty()) s.error else "")
            .put("working", s.working)
            .put("prompt", s.prompt.kind != AgentPrompt.Kind.KIND_NONE && s.prompt.kind != AgentPrompt.Kind.UNRECOGNIZED)
        val metaText = meta.toString()
        if (metaText != sentMeta) { u.put("meta", meta); sentMeta = metaText }
        val tail = tail(s)
        val tailText = tail.toString()
        if (tailText != sentTail) { u.put("tail", tail); sentTail = tailText }
        // Sent from here: down to see it go, as the native view does.
        if (s.pendingSends.size > sentPending) u.put("sent", true)
        sentPending = s.pendingSends.size
        if (u.length() > 0) page.call("update", u)
    }

    private fun row(it: AgentItem): JSONObject {
        val r = JSONObject().put("id", it.id)
        val b = it.block
        if (b == null) {
            if (it.tools.size == 1) {
                r.put("kind", "tool").put("b", block(it.tools[0]))
            } else {
                r.put("kind", "group")
                r.put("tools", JSONArray(it.tools.map(::block)))
                r.put("summary", ToolSummary.words(it.tools.map { t -> t.tool to t.input }))
                r.put("failed", ToolSummary.failed(it.tools.count { t -> t.error && !t.pending }) ?: "")
            }
            return r
        }
        when (b.kind) {
            AgentBlock.Kind.KIND_USER -> r.put("kind", "user").put("text", b.text).put("rich", b.richText).put("images", JSONArray(b.imagesList.map { i -> i.ref }))
            AgentBlock.Kind.KIND_TEXT -> {
                r.put("kind", "text").put("text", b.text)
                // Arrived while watched: shown from the top down.
                val at = session.arrived[b.id]
                if (at != null && System.currentTimeMillis() - at < 2000 && revealed.add(b.id)) r.put("live", true)
            }
            AgentBlock.Kind.KIND_THINKING -> {
                r.put("kind", "thinking").put("text", b.text)
                it.thoughtSeconds?.let { secs -> r.put("seconds", secs) }
            }
            AgentBlock.Kind.KIND_TOOL -> r.put("kind", if (b.tool == "SendUserFile") "files" else "tool").put("b", block(b))
            AgentBlock.Kind.KIND_QUESTION -> r.put("kind", "question").put("b", block(b))
            AgentBlock.Kind.KIND_PLAN -> r.put("kind", "plan").put("b", block(b))
            AgentBlock.Kind.KIND_SUBAGENT -> r.put("kind", "subagent").put("b", block(b))
            AgentBlock.Kind.KIND_NOTE -> r.put("kind", "note").put("b", block(b))
            AgentBlock.Kind.KIND_DONE -> r.put("kind", "done").put("words", TurnWords.words(b.durationMs, b.timeUnixMs))
            else -> r.put("kind", "")
        }
        return r
    }

    private fun block(b: AgentBlock): JSONObject {
        val d = JSONObject()
            .put("id", b.id).put("tool", b.tool).put("title", b.title).put("input", b.input).put("output", b.output).put("text", b.text)
            .put("error", b.error).put("pending", b.pending).put("time", b.timeUnixMs)
            .put("added", b.added).put("removed", b.removed).put("images", JSONArray(b.imagesList.map { it.ref }))
            .put("bang", b.isBang())
        if (b.diff.isNotEmpty()) d.put("diff", diff(b))
        if (b.questionsCount > 0) {
            d.put("questions", JSONArray(b.questionsList.map { q -> JSONObject().put("question", q.question).put("answer", q.answer).put("notes", q.notes) }))
        }
        return d
    }

    /** An edit's diff as the page's DiffView shows it: its file, and its lines numbered. */
    private fun diff(b: AgentBlock): JSONObject {
        val lines = DiffLines.numbered(b.diff)
        val path = b.diffPath()
        val files = DiffLines.files(lines)
        var name = "Changes"
        var folder = ""
        if (path.isNotEmpty()) {
            name = File(path).name
            folder = File(path).parent ?: ""
        } else files.firstOrNull()?.let { first ->
            name = File(first).name
            if (files.size == 1) folder = File(first).parent ?: "" else name += " and ${files.size - 1} more"
        }
        val kinds = mapOf(
            DiffLines.Kind.CONTEXT to "c", DiffLines.Kind.ADDED to "a", DiffLines.Kind.REMOVED to "r",
            DiffLines.Kind.HUNK to "h", DiffLines.Kind.FILE to "f", DiffLines.Kind.NOTE to "n",
        )
        return JSONObject().put("name", name).put("folder", folder).put("path", path.ifEmpty { files.firstOrNull() ?: "" }).put("raw", b.diff)
            .put("lines", JSONArray(lines.map { l -> JSONArray(listOf(kinds[l.kind] ?: "c", l.old ?: 0, l.new ?: 0, l.text)) }))
    }

    /** What follows the transcript: messages on their way (a "!" command as its run), and Working…. */
    private fun tail(s: AgentViewState): JSONObject {
        val running = s.pendingSends.firstOrNull { it.isBang }?.id
        val pending = JSONArray()
        for (p in s.pendingSends) {
            val run = p.bangRun(if (p.id == running) s.bangOutput else "")
            pending.put(
                if (run != null) JSONObject().put("id", p.id.toString()).put("kind", "tool").put("b", block(run))
                else JSONObject().put("id", p.id.toString()).put("kind", "user").put("text", p.text),
            )
        }
        return JSONObject().put("pending", pending).put("working", s.working).put("status", s.status)
    }

    // ---- from the page ----

    private fun onMessage(type: String, m: JSONObject) {
        when (type) {
            "ready" -> {
                // Loaded (or loaded again): it has nothing, so everything goes.
                config?.let { page.call("configure", it) }
                push(replace = true)
            }
            "log" -> Log.i(TAG, "page: " + m.optString("text"))
            "dismissKeyboard" -> onDismissKeyboard()
            "loadOlder" -> session.loadEarlier()
            "link" -> m.optString("href").takeIf { it.isNotEmpty() }?.let(onLink)
            "copy" -> onCopy(m.optString("text"))
            "image" -> {
                val ref = m.optString("ref")
                scope.launch {
                    val url = withContext(Dispatchers.IO) { picture(ref) }
                    page.call("image", ref, url)
                }
            }
            "openImage" -> {
                val ref = m.optString("ref")
                scope.launch {
                    val bitmap = withContext(Dispatchers.IO) {
                        val data = session.file(ref) ?: return@withContext null
                        if (isVideo(ref, data)) poster(data) else BitmapFactory.decodeByteArray(data, 0, data.size)
                    }
                    bitmap?.let(onOpenImage)
                }
            }
        }
    }

    /** A picture as a data URL for the page: a video's first frame; "" when it can't be shown. */
    private suspend fun picture(ref: String): String {
        val data = session.file(ref) ?: return ""
        if (!isVideo(ref, data)) {
            val type = mediaType(data).ifEmpty { "application/octet-stream" }
            return "data:$type;base64," + Base64.encodeToString(data, Base64.NO_WRAP)
        }
        val frame = poster(data) ?: return ""
        val out = ByteArrayOutputStream()
        frame.compress(Bitmap.CompressFormat.JPEG, 85, out)
        return "data:image/jpeg;base64," + Base64.encodeToString(out.toByteArray(), Base64.NO_WRAP)
    }

    /** A video's first frame, at most 800 pixels across. */
    private fun poster(data: ByteArray): Bitmap? {
        val r = MediaMetadataRetriever()
        return try {
            r.setDataSource(ByteArrayDataSource(data))
            r.getScaledFrameAtTime(0, MediaMetadataRetriever.OPTION_CLOSEST_SYNC, 800, 800)
        } catch (e: Exception) {
            null
        } finally {
            r.release()
        }
    }

    private class ByteArrayDataSource(private val data: ByteArray) : android.media.MediaDataSource() {
        override fun readAt(position: Long, buffer: ByteArray, offset: Int, size: Int): Int {
            if (position >= data.size) return -1
            val n = minOf(size, data.size - position.toInt())
            System.arraycopy(data, position.toInt(), buffer, offset, n)
            return n
        }
        override fun getSize() = data.size.toLong()
        override fun close() {}
    }

    companion object {
        const val TAG = "HammockAgentPage"

        private fun isVideo(ref: String, data: ByteArray): Boolean =
            ref.substringAfterLast('.', "").lowercase() in setOf("mp4", "mov", "m4v", "webm") || mediaType(data).startsWith("video/")

        /** What a file is, by its first bytes ("" when it can't tell). */
        fun mediaType(d: ByteArray): String {
            fun at(i: Int) = if (i < d.size) d[i].toInt() and 0xFF else -1
            fun starts(vararg b: Int) = b.withIndex().all { (i, v) -> at(i) == v }
            if (starts(0x89, 0x50, 0x4E, 0x47)) return "image/png"
            if (starts(0xFF, 0xD8, 0xFF)) return "image/jpeg"
            if (starts(0x47, 0x49, 0x46)) return "image/gif"
            if (d.size >= 12 && String(d, 8, 4, Charsets.US_ASCII) == "WEBP") return "image/webp"
            if (starts(0x1A, 0x45, 0xDF, 0xA3)) return "video/webm"
            if (d.size >= 12 && String(d, 4, 4, Charsets.US_ASCII) == "ftyp") {
                val brand = String(d, 8, 4, Charsets.US_ASCII)
                if (brand in setOf("heic", "heix", "heim", "heis", "mif1", "msf1")) return "image/heic"
                if (brand == "avif") return "image/avif"
                return if (brand == "qt  ") "video/quicktime" else "video/mp4"
            }
            return ""
        }

        /**
         * What the page is drawn with: this phone's fonts and sizes (Settings ▸
         * Text, scaled by the system's font size, as the native rows are), the
         * theme's colours, and the icons the native rows use.
         */
        fun config(
            c: AgentColors, f: AgentFonts, fontScale: Float, t: androidx.compose.material3.Typography,
            cs: androidx.compose.material3.ColorScheme, agentName: String,
        ): JSONObject {
            val size = f.size * fontScale
            val code = f.codeSize * fontScale
            // Compose's body line (forAgent): 1.25 of the size, then the spacing between lines.
            val bodyLine = size * 1.25f
            val lines = JSONObject()
            for (k in listOf(100, 93, 90, 88, 80)) lines.put("b$k", bodyLine * k / 100)
            for (k in listOf(100, 95, 88)) lines.put("c$k", code * 1.35f * k / 100)
            lines.put("caption", t.bodySmall.lineHeight.value * fontScale)
            lines.put("caption2", t.labelSmall.lineHeight.value * fontScale)
            lines.put("callout", t.labelLarge.lineHeight.value * fontScale)
            val family = if (f.family.isEmpty()) "sans-serif" else "${f.family}, sans-serif"
            val fonts = JSONObject()
                .put("family", family).put("size", size)
                .put("lineHeight", bodyLine + f.lineSpacing).put("gap", bodyLine * 0.45f + f.lineSpacing)
                .put("code", "monospace").put("codeSize", code).put("codeLineHeight", code * 1.35f)
                .put("spacing", f.lineSpacing).put("lines", lines)
            val metrics = JSONObject()
                .put("caption", t.bodySmall.fontSize.value * fontScale)
                .put("caption2", t.labelSmall.fontSize.value * fontScale)
                .put("callout", t.labelLarge.fontSize.value * fontScale)
                .put("button", t.labelLarge.fontSize.value * fontScale)
            // One set for light and dark: the app's theme decides, not the WebView's.
            val palette = JSONObject()
                .put("fg", css(cs.onSurface)).put("fg2", css(c.secondary)).put("fg3", css(c.tertiary))
                .put("fg4", css(cs.onSurface.copy(alpha = 0.16f))).put("sep", css(cs.outlineVariant))
                .put("green", css(c.added)).put("red", css(c.removed)).put("orange", css(c.warning))
                .put("purple", css(c.plan)).put("teal", css(c.subagent))
                .put("accent", css(c.accent)).put("bubble", css(c.bubble)).put("bubbleFill", css(c.bubble))
            return JSONObject()
                // The app's light or dark, which can differ from the system's the WebView follows.
                .put("scheme", if (cs.surface.luminance() < 0.5f) "dark" else "light")
                .put("platform", "android").put("codeWrap", f.codeWrap).put("canOpenDiff", false).put("agentName", agentName)
                .put("fonts", fonts).put("metrics", metrics)
                .put("colors", JSONObject().put("light", palette).put("dark", palette))
                .put("symbols", symbols)
        }

        private fun css(c: Color): String {
            val v = c.toArgb()
            return "rgba(${(v shr 16) and 0xFF}, ${(v shr 8) and 0xFF}, ${v and 0xFF}, ${"%.3f".format(java.util.Locale.ROOT, c.alpha)})"
        }

        /**
         * The page's icons, by the SF Symbol names it asks for, drawn with the
         * Material icons the native rows use: each as an SVG of its paths, the
         * mask the page fills with its text's colour.
         */
        private val symbols: JSONObject by lazy {
            val icons = mapOf(
                "arrow.turn.down.left" to Icons.AutoMirrored.Outlined.WrapText, "doc.on.doc" to Icons.Outlined.ContentCopy,
                "checkmark" to Icons.Outlined.Check, "terminal" to Icons.Outlined.Terminal, "doc.text" to Icons.Outlined.Description,
                "doc.badge.plus" to Icons.Outlined.PostAdd, "pencil" to Icons.Outlined.Edit, "magnifyingglass" to Icons.Outlined.Search,
                "globe" to Icons.Outlined.Public, "checklist" to Icons.Outlined.Checklist, "wand.and.stars" to Icons.Outlined.AutoAwesome,
                "puzzlepiece.extension" to Icons.Outlined.Extension, "wrench.and.screwdriver" to Icons.Outlined.Build,
                "xmark.circle.fill" to Icons.Filled.Cancel, "checkmark.circle.fill" to Icons.Filled.CheckCircle,
                "chevron.down" to Icons.Outlined.ExpandMore, "chevron.right" to Icons.Outlined.ChevronRight,
                "rectangle.split.2x1" to Icons.Outlined.VerticalSplit, "square.stack.3d.up" to Icons.Outlined.Layers,
                "brain" to Icons.Outlined.Psychology, "questionmark.bubble" to Icons.AutoMirrored.Outlined.HelpOutline,
                "arrowshape.turn.up.left.fill" to Icons.AutoMirrored.Filled.Reply, "text.bubble" to Icons.Outlined.ChatBubbleOutline,
                "arrow.uturn.backward" to Icons.AutoMirrored.Outlined.Undo, "list.bullet.clipboard" to Icons.AutoMirrored.Outlined.Assignment,
                "person.2.wave.2" to Icons.Outlined.Groups, "paperclip" to Icons.Outlined.AttachFile, "sparkle" to Icons.Outlined.AutoAwesome,
                "arrow.down" to Icons.Outlined.ArrowDownward, "bubble.left.and.text.bubble.right" to Icons.Outlined.Forum,
                "photo" to Icons.Outlined.Photo, "film" to Icons.Outlined.Movie, "play.fill" to Icons.Filled.PlayArrow,
            )
            val out = JSONObject()
            for ((name, icon) in icons) {
                val svg = svg(icon)
                // The size at a text size of 1: the native rows draw them about 1.15 of the text beside them.
                out.put(name, JSONObject().put("url", "data:image/svg+xml;base64," + Base64.encodeToString(svg.toByteArray(), Base64.NO_WRAP)).put("w", 1.15).put("h", 1.15))
            }
            out
        }

        /** An icon's paths as an SVG, filled black (the page uses it as a mask). */
        fun svg(v: ImageVector): String {
            val sb = StringBuilder()
            sb.append("""<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${v.viewportWidth} ${v.viewportHeight}">""")
            fun walk(n: VectorNode) {
                when (n) {
                    is VectorGroup -> n.forEach(::walk)
                    is VectorPath -> {
                        val rule = if (n.pathFillType == androidx.compose.ui.graphics.PathFillType.EvenOdd) " fill-rule=\"evenodd\"" else ""
                        sb.append("<path d=\"").append(pathData(n.pathData)).append("\"").append(rule).append("/>")
                    }
                }
            }
            walk(v.root)
            sb.append("</svg>")
            return sb.toString()
        }

        private fun pathData(nodes: List<PathNode>): String = nodes.joinToString(" ") { n ->
            fun f(x: Float) = if (x == x.toInt().toFloat()) x.toInt().toString() else x.toString()
            fun b(x: Boolean) = if (x) "1" else "0"
            when (n) {
                is PathNode.Close -> "Z"
                is PathNode.MoveTo -> "M${f(n.x)} ${f(n.y)}"
                is PathNode.RelativeMoveTo -> "m${f(n.dx)} ${f(n.dy)}"
                is PathNode.LineTo -> "L${f(n.x)} ${f(n.y)}"
                is PathNode.RelativeLineTo -> "l${f(n.dx)} ${f(n.dy)}"
                is PathNode.HorizontalTo -> "H${f(n.x)}"
                is PathNode.RelativeHorizontalTo -> "h${f(n.dx)}"
                is PathNode.VerticalTo -> "V${f(n.y)}"
                is PathNode.RelativeVerticalTo -> "v${f(n.dy)}"
                is PathNode.CurveTo -> "C${f(n.x1)} ${f(n.y1)} ${f(n.x2)} ${f(n.y2)} ${f(n.x3)} ${f(n.y3)}"
                is PathNode.RelativeCurveTo -> "c${f(n.dx1)} ${f(n.dy1)} ${f(n.dx2)} ${f(n.dy2)} ${f(n.dx3)} ${f(n.dy3)}"
                is PathNode.ReflectiveCurveTo -> "S${f(n.x1)} ${f(n.y1)} ${f(n.x2)} ${f(n.y2)}"
                is PathNode.RelativeReflectiveCurveTo -> "s${f(n.dx1)} ${f(n.dy1)} ${f(n.dx2)} ${f(n.dy2)}"
                is PathNode.QuadTo -> "Q${f(n.x1)} ${f(n.y1)} ${f(n.x2)} ${f(n.y2)}"
                is PathNode.RelativeQuadTo -> "q${f(n.dx1)} ${f(n.dy1)} ${f(n.dx2)} ${f(n.dy2)}"
                is PathNode.ReflectiveQuadTo -> "T${f(n.x)} ${f(n.y)}"
                is PathNode.RelativeReflectiveQuadTo -> "t${f(n.dx)} ${f(n.dy)}"
                is PathNode.ArcTo -> "A${f(n.horizontalEllipseRadius)} ${f(n.verticalEllipseRadius)} ${f(n.theta)} ${b(n.isMoreThanHalf)} ${b(n.isPositiveArc)} ${f(n.arcStartX)} ${f(n.arcStartY)}"
                is PathNode.RelativeArcTo -> "a${f(n.horizontalEllipseRadius)} ${f(n.verticalEllipseRadius)} ${f(n.theta)} ${b(n.isMoreThanHalf)} ${b(n.isPositiveArc)} ${f(n.arcStartDx)} ${f(n.arcStartDy)}"
            }
        }
    }
}
