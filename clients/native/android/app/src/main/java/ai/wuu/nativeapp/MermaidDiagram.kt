package ai.wuu.nativeapp

import android.annotation.SuppressLint
import android.content.Context
import android.graphics.Color
import android.webkit.JavascriptInterface
import android.webkit.RenderProcessGoneDetail
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.luminance
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import org.json.JSONObject
import java.io.ByteArrayInputStream

@Composable internal fun MermaidDiagram(code: String, live: Boolean) {
    var height by remember { mutableIntStateOf(96) }
    var showingSource by remember { mutableStateOf(false) }
    var generation by remember { mutableIntStateOf(0) }
    val clipboard = LocalClipboardManager.current
    val dark = MaterialTheme.colorScheme.surface.luminance() < 0.5f
    val fontSize = 15 * LocalDensity.current.fontScale
    val payload = JSONObject().put("code", code).put("live", live).put("dark", dark).put("fontSize", fontSize).toString()
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            TextButton(onClick = { showingSource = !showingSource }) { Text(if (showingSource) "显示图表" else "查看源码") }
            TextButton(onClick = { clipboard.setText(AnnotatedString(code)) }) { Text("复制代码") }
        }
        if (showingSource) {
            SelectionContainer {
                Text(code, Modifier.horizontalScroll(rememberScrollState()), fontFamily = FontFamily.Monospace, fontSize = 13.sp)
            }
        } else key(generation) {
            AndroidView(
                modifier = Modifier.fillMaxWidth().height(height.dp),
                factory = { DiagramWebView(it, { height = it }, { generation++ }) },
                update = { it.render(payload) },
                onRelease = { it.dispose() },
            )
        }
    }
}

@SuppressLint("SetJavaScriptEnabled")
private class DiagramWebView(context: Context, onHeight: (Int) -> Unit, onTerminated: () -> Unit) : WebView(context) {
    private var ready = false
    private var payload = ""
    private var disposed = false
    init {
        setBackgroundColor(Color.TRANSPARENT)
        settings.javaScriptEnabled = true
        // Compose already supplies the user's font scale to the renderer.
        settings.textZoom = 100
        settings.allowFileAccess = false
        settings.allowContentAccess = false
        settings.blockNetworkLoads = true
        settings.domStorageEnabled = false
        addJavascriptInterface(object {
            @JavascriptInterface fun height(value: Double) {
                if (value.isFinite()) post { if (!disposed) onHeight(value.toInt().coerceIn(64, 600)) }
            }
        }, "WuuDiagram")
        webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String) { ready = true; deliver() }
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest) = true
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest) =
                WebResourceResponse("text/plain", "UTF-8", ByteArrayInputStream(ByteArray(0)))
            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                if (!disposed) onTerminated()
                return true
            }
        }
        loadDataWithBaseURL(null, document(context), "text/html", "UTF-8", null)
    }
    fun render(value: String) {
        if (payload == value) return
        payload = value; deliver()
    }
    private fun deliver() {
        if (ready && !disposed && payload.isNotEmpty()) evaluateJavascript("window.renderWuuMermaid($payload)", null)
    }
    fun dispose() {
        disposed = true; stopLoading(); removeJavascriptInterface("WuuDiagram"); destroy()
    }
    companion object {
        private var html: String? = null
        private fun document(context: Context): String = html ?: context.assets.open("mermaid.html").bufferedReader().use { it.readText() }.also { html = it }
    }
}
