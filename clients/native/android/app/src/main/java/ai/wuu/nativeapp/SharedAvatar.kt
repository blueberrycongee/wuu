package ai.wuu.nativeapp

import android.animation.ValueAnimator
import android.content.Context
import android.graphics.Color
import android.view.MotionEvent
import android.view.View
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebView
import android.webkit.WebViewClient
import android.webkit.RenderProcessGoneDetail
import androidx.compose.foundation.background
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.graphicsLayer
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import org.json.JSONArray
import org.json.JSONObject
import java.io.ByteArrayInputStream
import kotlin.math.absoluteValue


private fun webAvatarSupported(): Boolean = try {
    val major = WebView.getCurrentWebViewPackage()?.versionName?.substringBefore('.')?.toIntOrNull() ?: 0
    major >= 80
} catch (_: Exception) { false }

private object AvatarDocument {
    private var html: String? = null
    fun read(context: Context) = html ?: context.assets.open("mascot.html").bufferedReader().use { it.readText() }.also { html = it }
}

/** Desktop AVATAR_HUES — keep Compose fallback hues aligned with blobatar seeds. */
private val AVATAR_HUES = intArrayOf(14, 33, 52, 96, 150, 182, 202, 222, 250, 288, 322, 350)

private fun fnv1a(seed: String): Int {
    var h = 0x811c9dc5.toInt()
    for (ch in seed) {
        h = h xor ch.code
        h *= 0x01000193
    }
    return h
}

private fun avatarHue(seed: String): Float {
    val index = fnv1a(seed).absoluteValue % AVATAR_HUES.size
    return AVATAR_HUES[index].toFloat()
}

private fun avatarLetter(label: String, seed: String): String {
    val source = label.trim().ifBlank { seed.trim() }.ifBlank { "?" }
    val first = source.firstOrNull { !it.isWhitespace() } ?: '?'
    return first.uppercaseChar().toString()
}

private data class AvatarFallbackModel(val seed: String, val label: String, val group: Boolean = false)

private fun fallbackModel(value: JSONObject): AvatarFallbackModel {
    val agent = value.optJSONObject("agent")
    if (agent != null) {
        val seed = agent.optString("id").ifBlank { "wuu" }
        val label = agent.optString("name").ifBlank { seed }
        return AvatarFallbackModel(seed, label)
    }
    val room = value.optJSONObject("room")
    val agents = value.optJSONArray("agents") ?: JSONArray()
    if (room != null && room.optString("kind") != "dm" && agents.length() > 0) {
        val first = agents.optJSONObject(0)
        val seed = first?.optString("id").orEmpty().ifBlank { room.optString("id").ifBlank { "group" } }
        val label = first?.optString("name").orEmpty().ifBlank { room.optString("name").ifBlank { "群" } }
        return AvatarFallbackModel(seed, label, group = true)
    }
    if (agents.length() > 0) {
        val first = agents.optJSONObject(0)
        val seed = first?.optString("id").orEmpty().ifBlank { "wuu" }
        return AvatarFallbackModel(seed, first?.optString("name").orEmpty().ifBlank { seed })
    }
    val seed = room?.optString("id").orEmpty().ifBlank { "wuu" }
    return AvatarFallbackModel(seed, room?.optString("name").orEmpty().ifBlank { seed }, group = room != null)
}

@Composable private fun AvatarFallbackMark(model: AvatarFallbackModel, size: Dp) {
    val hue = avatarHue(model.seed)
    val fill = androidx.compose.ui.graphics.Color.hsv(hue, 0.42f, if (isSystemInDarkTheme()) 0.62f else 0.78f)
    val onFill = androidx.compose.ui.graphics.Color.hsv(hue, 0.15f, if (isSystemInDarkTheme()) 0.95f else 0.18f)
    val letterSize = (size.value * 0.42f).coerceIn(9f, 18f).sp
    Box(
        Modifier.size(size).clip(CircleShape).background(fill),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = avatarLetter(model.label, model.seed),
            color = onFill,
            style = MaterialTheme.typography.labelLarge.copy(fontSize = letterSize, lineHeight = letterSize),
            maxLines = 1,
        )
    }
}

/** Local presentation surface for the desktop AgentAvatarMark/ChannelGroupAvatar. */
private class AvatarWebView(
    context: Context,
    val recreate: () -> Unit,
    val onRendered: (Boolean) -> Unit,
) : WebView(context) {
    private var released = false
    private var loaded = false
    private var payload = "{}"
    private var paused = false
    init {
        setBackgroundColor(Color.TRANSPARENT)
        setLayerType(LAYER_TYPE_HARDWARE, null)
        isClickable = false; isFocusable = false; isFocusableInTouchMode = false
        importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
        isHorizontalScrollBarEnabled = false; isVerticalScrollBarEnabled = false
        settings.javaScriptEnabled = true
        settings.allowFileAccess = false; settings.allowContentAccess = false
        settings.domStorageEnabled = false; settings.textZoom = 100
        webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String?) { loaded = true; render() }
            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                loaded = false; released = true; onRendered(false); recreate()
                return true
            }
            override fun shouldOverrideUrlLoading(view: WebView, request: WebResourceRequest) = true
            override fun shouldInterceptRequest(view: WebView, request: WebResourceRequest) =
                WebResourceResponse("text/plain", "UTF-8", ByteArrayInputStream(ByteArray(0)))
        }
        loadDataWithBaseURL(null, AvatarDocument.read(context), "text/html", "UTF-8", null)
    }
    override fun onTouchEvent(event: MotionEvent?) = false
    fun update(value: String) { if (payload != value) { payload = value; render() } }
    fun release() { released = true; onRendered(false); stopLoading(); destroy() }
    fun pause(value: Boolean) { if (released) return; paused = value; if (value) onPause() else onResume(); render() }
    private fun render() {
        if (!loaded || released) return
        val value = JSONObject(payload).put("paused", paused).put("reducedMotion", !ValueAnimator.areAnimatorsEnabled())
        // Confirm the renderer exists before covering the Compose fallback.
        evaluateJavascript(
            "(function(p){try{if(typeof window.renderWuuAvatar!=='function')return false;window.renderWuuAvatar(p);return true}catch(e){return false}})($value)"
        ) { result -> onRendered(result == "true") }
    }
}

@Composable internal fun SharedAvatar(value: JSONObject, size: Dp) {
    val dark = isSystemInDarkTheme()
    val owner = LocalLifecycleOwner.current
    var generation by remember { mutableIntStateOf(0) }
    var view by remember { mutableStateOf<AvatarWebView?>(null) }
    var webReady by remember { mutableStateOf(false) }
    val fallback = remember(value.toString()) { fallbackModel(value) }
    val payload = remember(value, dark, size) { value.put("dark", dark).put("size", size.value).toString() }
    // Timeline marks are tiny; Compose letter marks are reliable on API 28 WebView 69.
    val useWeb = size >= 28.dp && webAvatarSupported()
    Box(Modifier.size(size), contentAlignment = Alignment.Center) {
        AvatarFallbackMark(fallback, size)
        if (useWeb) {
            key(generation) {
                AndroidView(
                    factory = { context ->
                        AvatarWebView(context, recreate = { generation++ }, onRendered = { webReady = it }).also { view = it }
                    },
                    modifier = Modifier.fillMaxSize().graphicsLayer { alpha = if (webReady) 1f else 0f },
                    update = { it.update(payload) },
                    onRelease = {
                        it.release()
                        if (view === it) view = null
                        webReady = false
                    },
                )
            }
            DisposableEffect(owner, view) {
                val current = view
                val observer = LifecycleEventObserver { _, _ ->
                    current?.pause(!owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
                }
                owner.lifecycle.addObserver(observer)
                current?.pause(!owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
                onDispose { owner.lifecycle.removeObserver(observer); current?.pause(true) }
            }
        }
    }
}

internal fun avatarRecord(agent: JSONObject?): JSONObject = json(
    "id" to agent?.optString("id").orEmpty(),
    "name" to agent?.optString("name").orEmpty(),
    "avatar_key" to agent?.optString("avatar_key", "abstract-1"),
    "avatar_image" to agent?.optString("avatar_image").orEmpty(),
    "activity_status" to agent?.optString("activity_status").orEmpty(),
)
