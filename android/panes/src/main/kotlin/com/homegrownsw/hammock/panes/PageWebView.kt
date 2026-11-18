package com.homegrownsw.hammock.panes

import android.annotation.SuppressLint
import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.net.Uri
import android.util.Log
import android.view.inputmethod.InputMethodManager
import android.webkit.ConsoleMessage
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.setValue
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import org.json.JSONObject
import java.io.ByteArrayInputStream

/**
 * Where the pages are served from: a made-up https origin the WebView never
 * reaches the network for (WebViewAssetLoader's convention), so the pages
 * have an origin of their own and the message listener can be held to it.
 */
internal object PageAssets {
    const val HOST = "appassets.androidplatform.net"
    const val ORIGIN = "https://$HOST"
    fun url(page: String) = "$ORIGIN/hammock/$page"

    /**
     * What the page is given before its own scripts run: window.webkit's
     * message handler, as WKWebView has it, forwarding to the listener; and
     * the preview's hammock-file:// images pointed at this origin, where
     * shouldInterceptRequest answers them (a custom scheme isn't reliably
     * offered to it).
     */
    val SHIM = """
        <script>
        (function () {
          var bridge = window.HammockAndroid;
          window.webkit = { messageHandlers: { hammock: { postMessage: function (m) { bridge.postMessage(JSON.stringify(m)); } } } };
          var set = Element.prototype.setAttribute;
          Element.prototype.setAttribute = function (name, value) {
            if (name === "src" && typeof value === "string" && value.indexOf("${PreviewImages.SCHEME}://local/") === 0) {
              value = "$ORIGIN${PreviewImages.ORIGIN_PREFIX}" + value.slice("${PreviewImages.SCHEME}://local".length);
            }
            return set.call(this, name, value);
          };
        })();
        </script>
    """.trimIndent()

    /** The page's HTML with the shim first in its head. */
    fun withShim(html: String): String {
        val i = html.indexOf("<head>")
        return if (i < 0) SHIM + html else html.substring(0, i + 6) + "\n" + SHIM + html.substring(i + 6)
    }
}

/**
 * One of the pages (editor.html or diff.html) in a WebView, and its
 * messages both ways. The WebView shows that page and nothing else: links
 * to the web open in the browser, other navigation is refused, and only the
 * page's own origin, in its main frame, can post (its messages can change
 * files). Everything here runs on the main thread.
 */
@SuppressLint("SetJavaScriptEnabled")
internal class PageBridge(
    context: Context,
    private val page: String,
    background: Int,
    /** A message from the page: its "type", and the whole of it. */
    private val onMessage: (type: String, m: JSONObject) -> Unit,
    /** An image the preview shows (an absolute path): its media type and bytes, or null. Called off the main thread. */
    private val readImage: ((String) -> Pair<String, ByteArray>?)? = null,
) {
    // Filling its parent: AndroidView otherwise lays it out wrap_content, and a
    // wrap_content WebView gives the page a zero-height viewport, so the pages
    // (sized at 100% of it) draw nothing.
    val webView = WebView(context).apply {
        layoutParams = android.view.ViewGroup.LayoutParams(android.view.ViewGroup.LayoutParams.MATCH_PARENT, android.view.ViewGroup.LayoutParams.MATCH_PARENT)
    }
    var ready = false
        private set
    // Compose state: a page that stops (its renderer gone) says so where it was.
    var problem: String? by mutableStateOf(null)
        private set
    private val html: ByteArray? = synchronized(pages) {
        pages[page] ?: try {
            PageAssets.withShim(context.assets.open("hammock/$page").use { it.readBytes().decodeToString() }).encodeToByteArray()
                .also { pages[page] = it }
        } catch (e: Exception) {
            null
        }
    }

    init {
        webView.setBackgroundColor(background)
        webView.overScrollMode = WebView.OVER_SCROLL_NEVER
        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = false
            allowFileAccess = false
            allowContentAccess = false
            javaScriptCanOpenWindowsAutomatically = false
            setSupportMultipleWindows(false)
            setSupportZoom(false)
            builtInZoomControls = false
            mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
            // The pages size their text in CSS pixels, as on the iPhone; the system's font scale isn't applied twice.
            textZoom = 100
        }
        if (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0) WebView.setWebContentsDebuggingEnabled(true)
        webView.webViewClient = Client()
        webView.webChromeClient = object : WebChromeClient() {
            override fun onConsoleMessage(m: ConsoleMessage): Boolean {
                Log.i(TAG, "$page:${m.lineNumber()} ${m.message()}")
                return true
            }
        }
        when {
            html == null -> problem = "The ${page.removeSuffix(".html")} page is missing from this build ($page)"
            !WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER) ->
                problem = "This phone's WebView is too old for editor and diff panes: update Android System WebView"
            else -> {
                WebViewCompat.addWebMessageListener(webView, "HammockAndroid", setOf(PageAssets.ORIGIN)) { _, message, origin, isMainFrame, _ ->
                    if (!isMainFrame || origin.toString().trimEnd('/') != PageAssets.ORIGIN) return@addWebMessageListener
                    val m = try { JSONObject(message.data ?: return@addWebMessageListener) } catch (e: Exception) { return@addWebMessageListener }
                    val type = m.optString("type")
                    if (type == "ready") ready = true
                    onMessage(type, m)
                }
                webView.loadUrl(PageAssets.url(page))
            }
        }
    }

    /** Calls fn on window.hammock with arguments as JSON (never text built into the script). */
    fun call(fn: String, vararg args: Any?) {
        if (!ready) return
        val list = args.joinToString(",") { a -> if (a == null) "null" else JSONObject.wrap(a)?.let(::literal) ?: "null" }
        webView.evaluateJavascript("hammock.$fn($list)", null)
    }

    /** Evaluates hammock.fn() and hands back its result, as JSON. */
    fun ask(fn: String, result: (String?) -> Unit) {
        if (!ready) return result(null)
        webView.evaluateJavascript("hammock.$fn()") { result(it) }
    }

    fun hideKeyboard() {
        webView.clearFocus()
        val imm = webView.context.getSystemService(InputMethodManager::class.java)
        imm?.hideSoftInputFromWindow(webView.windowToken, 0)
    }

    fun destroy() {
        ready = false
        webView.stopLoading()
        webView.webViewClient = WebViewClient()
        // After Compose has taken it out of the window: destroying it while attached is refused noisily.
        android.os.Handler(android.os.Looper.getMainLooper()).post {
            (webView.parent as? android.view.ViewGroup)?.removeView(webView)
            webView.destroy()
        }
    }

    private fun literal(v: Any): String = when (v) {
        is String -> JSONObject.quote(v)
        else -> v.toString() // JSONObject, JSONArray, numbers, booleans
    }

    private inner class Client : WebViewClient() {
        override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest): Boolean {
            val url = request.url
            if (url.toString() == PageAssets.url(page) && request.isForMainFrame) return false
            if (request.hasGesture() && url.scheme?.lowercase() in setOf("http", "https", "mailto")) openOutside(view.context, url)
            return true
        }

        override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest): WebResourceResponse? {
            val url = request.url.toString()
            if (url == PageAssets.url(page)) {
                val bytes = html ?: return refused()
                return WebResourceResponse("text/html", "utf-8", ByteArrayInputStream(bytes))
            }
            PreviewImages.path(url)?.let { path ->
                val image = try { readImage?.invoke(path) } catch (e: Exception) { null } ?: return refused(404)
                return WebResourceResponse(image.first, null, ByteArrayInputStream(image.second))
            }
            // data: and blob: URLs never come here; anything else the page has no business fetching.
            return refused()
        }

        // The page's renderer crashed or was killed for memory: without this the app goes with it.
        override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
            ready = false
            problem = "The page stopped. Leave the pane and come back to show it again."
            return true
        }

        private fun refused(code: Int = 403) =
            WebResourceResponse("text/plain", "utf-8", code, if (code == 404) "Not Found" else "Forbidden", emptyMap(), ByteArrayInputStream(ByteArray(0)))
    }

    companion object {
        const val TAG = "HammockPanes"

        /** The pages with their shim, read once (a couple of MB each). */
        private val pages = HashMap<String, ByteArray>()

        /** A link to the web (or mail), outside the app. */
        fun openOutside(context: Context, url: Uri) {
            if (url.scheme?.lowercase() !in setOf("http", "https", "mailto")) return
            try {
                context.startActivity(Intent(Intent.ACTION_VIEW, url).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            } catch (e: ActivityNotFoundException) {
                Log.d(TAG, "no app opens $url")
            }
        }
    }
}
