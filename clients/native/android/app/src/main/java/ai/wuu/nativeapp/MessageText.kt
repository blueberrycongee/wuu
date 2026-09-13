package ai.wuu.nativeapp

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.widget.TextView
import android.view.ViewGroup
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.unit.sp
import kotlin.math.roundToInt
import androidx.compose.ui.viewinterop.AndroidView
import io.noties.markwon.AbstractMarkwonPlugin
import io.noties.markwon.Markwon
import io.noties.markwon.MarkwonConfiguration
import io.noties.markwon.ext.strikethrough.StrikethroughPlugin
import io.noties.markwon.ext.tables.TablePlugin
import io.noties.markwon.ext.tasklist.TaskListPlugin
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

internal fun messageMarkdown(context: Context, open: (Uri) -> Unit): Markwon = Markwon.builder(context)
    .usePlugin(StrikethroughPlugin.create()).usePlugin(TablePlugin.create(context)).usePlugin(TaskListPlugin.create(context))
    .usePlugin(object : AbstractMarkwonPlugin() {
        override fun configureConfiguration(builder: MarkwonConfiguration.Builder) {
            builder.linkResolver { _, link ->
                val uri = Uri.parse(link)
                if (uri.scheme?.lowercase() in listOf("https", "http") && !uri.host.isNullOrBlank()) open(uri)
            }
        }
    }).build() // No HTML or image loader: merely opening a message never makes external requests.

@Composable fun MessageText(text: String, markdown: Boolean, modifier: Modifier = Modifier) {
    val bodyStyle = MaterialTheme.typography.bodyLarge.copy(fontSize = 15.sp, lineHeight = 23.sp)
    if (!markdown || text.toByteArray().size > 128 * 1024) { Text(text, modifier, style = bodyStyle); return }
    val context = LocalContext.current
    val renderer = remember(context) { messageMarkdown(context) { uri ->
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE)) }
    } }
    var rendered by remember { mutableStateOf<android.text.Spanned?>(null) }
    LaunchedEffect(text) { rendered = withContext(Dispatchers.Default) { renderer.toMarkdown(text) } }
    val value = rendered
    if (value == null) {
        Text(text, modifier, style = bodyStyle)
        return
    }
    val color = MaterialTheme.colorScheme.onSurface.toArgb()
    val linkColor = MaterialTheme.colorScheme.primary.toArgb()
    val size = bodyStyle.fontSize.value
    val lineHeight = with(LocalDensity.current) { bodyStyle.lineHeight.toPx().roundToInt() }
    AndroidView(modifier = modifier, factory = { TextView(it).apply {
        // Let the lazy list measure the full reply, including asynchronously rendered text.
        layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
        setTextIsSelectable(true)
        // Populate before the first measure; lazy prefetch can otherwise cache an empty layout.
        renderer.setParsedMarkdown(this, value)
        tag = value
    } }, update = { view ->
        view.setTextColor(color); view.setLinkTextColor(linkColor); view.textSize = size
        view.setLineHeight(lineHeight)
        if (view.tag !== value) { renderer.setParsedMarkdown(view, value); view.tag = value }
    })
}
