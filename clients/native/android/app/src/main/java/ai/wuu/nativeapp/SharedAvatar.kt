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
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.foundation.layout.size
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import org.json.JSONObject
import java.io.ByteArrayInputStream

private object AvatarDocument {
    private var html: String? = null
    fun read(context: Context) = html ?: context.assets.open("mascot.html").bufferedReader().use { it.readText() }.also { html = it }
}

/** Local presentation surface for the desktop AgentAvatarMark/ChannelGroupAvatar. */
private class AvatarWebView(context: Context, val recreate: () -> Unit) : WebView(context) {
    private var released = false
    private var loaded = false
    private var payload = "{}"
    private var paused = false
    init {
        setBackgroundColor(Color.TRANSPARENT)
        importantForAccessibility = View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS
        isHorizontalScrollBarEnabled = false; isVerticalScrollBarEnabled = false
        settings.javaScriptEnabled = true
        settings.allowFileAccess = false; settings.allowContentAccess = false
        settings.domStorageEnabled = false; settings.textZoom = 100
        webViewClient = object : WebViewClient() {
            override fun onPageFinished(view: WebView, url: String?) { loaded = true; render() }
            override fun onRenderProcessGone(view: WebView, detail: RenderProcessGoneDetail): Boolean {
                loaded = false; released = true; recreate()
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
    fun release() { released = true; stopLoading(); destroy() }
    fun pause(value: Boolean) { if (released) return; paused = value; if (value) onPause() else onResume(); render() }
    private fun render() {
        if (!loaded || released) return
        val value = JSONObject(payload).put("paused", paused).put("reducedMotion", !ValueAnimator.areAnimatorsEnabled())
        evaluateJavascript("window.renderWuuAvatar($value)", null)
    }
}

@Composable internal fun SharedAvatar(value: JSONObject, size: Dp) {
    val dark = isSystemInDarkTheme()
    val owner = LocalLifecycleOwner.current
    var generation by remember { mutableIntStateOf(0) }
    var view by remember { mutableStateOf<AvatarWebView?>(null) }
    val payload = value.put("dark", dark).put("size", size.value).toString()
    key(generation) {
    AndroidView(factory = { context -> AvatarWebView(context) { generation++ }.also { view = it } },
        modifier = Modifier.size(size), update = { it.update(payload) },
        onRelease = { it.release(); if (view === it) view = null })
    }
    DisposableEffect(owner, view) {
        val current = view
        val observer = LifecycleEventObserver { _, _ -> current?.pause(!owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) }
        owner.lifecycle.addObserver(observer)
        current?.pause(!owner.lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED))
        onDispose { owner.lifecycle.removeObserver(observer); current?.pause(true) }
    }
}

internal fun avatarRecord(agent: JSONObject?): JSONObject = json(
    "id" to agent?.optString("id").orEmpty(), "avatar_key" to agent?.optString("avatar_key", "abstract-1"),
    "avatar_image" to agent?.optString("avatar_image").orEmpty(), "activity_status" to agent?.optString("activity_status").orEmpty(),
)
