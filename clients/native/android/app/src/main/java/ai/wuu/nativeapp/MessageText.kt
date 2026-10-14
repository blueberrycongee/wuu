package ai.wuu.nativeapp

import android.content.Context
import android.content.Intent
import android.net.Uri
import android.widget.TextView
import android.text.Spanned
import android.text.SpannedString
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.ui.unit.dp
import io.noties.markwon.MarkwonSpansFactory
import io.noties.markwon.core.CoreProps
import org.commonmark.node.FencedCodeBlock
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
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.conflate

internal fun messageMarkdown(context: Context, open: (Uri) -> Unit): Markwon = Markwon.builder(context)
    .usePlugin(StrikethroughPlugin.create()).usePlugin(TablePlugin.create(context)).usePlugin(TaskListPlugin.create(context))
    .usePlugin(object : AbstractMarkwonPlugin() {
        override fun configureSpansFactory(builder: MarkwonSpansFactory.Builder) {
            builder.appendFactory(FencedCodeBlock::class.java) { _, props ->
                if (CoreProps.CODE_BLOCK_INFO.get(props)?.trim()?.split(Regex("\\s+"))?.firstOrNull()?.lowercase() == "mermaid") MermaidSpan() else null
            }
        }
        override fun configureConfiguration(builder: MarkwonConfiguration.Builder) {
            builder.linkResolver { _, link ->
                val uri = Uri.parse(link)
                if (uri.scheme?.lowercase() in listOf("https", "http") && !uri.host.isNullOrBlank()) open(uri)
            }
        }
    }).build() // No HTML or image loader: merely opening a message never makes external requests.

private class MermaidSpan
private data class MessagePart(val text: Spanned, val diagram: Boolean)
private data class ParsedMessage(val source: String, val content: Spanned)

@Composable fun MessageText(text: String, markdown: Boolean, modifier: Modifier = Modifier, isLive: Boolean = false) {
    val bodyStyle = MaterialTheme.typography.bodyLarge.copy(fontSize = 15.sp, lineHeight = 23.sp)
    if (!markdown || text.toByteArray().size > 128 * 1024) { Text(text, modifier, style = bodyStyle); return }
    val context = LocalContext.current
    val renderer = remember(context) { messageMarkdown(context) { uri ->
        runCatching { context.startActivity(Intent(Intent.ACTION_VIEW, uri).addCategory(Intent.CATEGORY_BROWSABLE)) }
    } }
    val latestText by rememberUpdatedState(text)
    val latestLive by rememberUpdatedState(isLive)
    var rendered by remember { mutableStateOf<ParsedMessage?>(null) }
    LaunchedEffect(renderer) {
        // Conflate instead of cancelling each parse: continuous deltas must not
        // starve the native text or its diagrams, even for long replies.
        snapshotFlow { latestText }.conflate().collect { source ->
            val parsed = withContext(Dispatchers.Default) { renderer.toMarkdown(source) }
            if (latestText.startsWith(source)) rendered = ParsedMessage(source, parsed)
            if (latestLive) delay(100)
        }
    }
    val snapshot = rendered
    val value = snapshot?.content
    if (value == null) {
        Text(text, modifier, style = bodyStyle)
        return
    }
    val parts = remember(value) {
        val diagrams = value.getSpans(0, value.length, MermaidSpan::class.java).sortedBy { value.getSpanStart(it) }
        buildList {
            fun addText(from: Int, to: Int) {
                var start = from
                var end = to
                if (diagrams.isNotEmpty()) {
                    while (start < end && value[start] == '\n') start++
                    while (end > start && value[end - 1] == '\n') end--
                }
                if (start < end) add(MessagePart(SpannedString(value.subSequence(start, end)), false))
            }
            var start = 0
            for (span in diagrams) {
                val from = value.getSpanStart(span)
                val end = value.getSpanEnd(span)
                addText(start, from)
                // Markwon 4.6.2 pads code spans with NBSP + newline and a final NBSP.
                // Remove only that framing and the parser's terminal newline so
                // streaming appends and incomplete-line fallback see the source.
                val code = value.subSequence(from, end).toString()
                    .removePrefix("\u00a0\n").removeSuffix("\u00a0").removeSuffix("\n")
                add(MessagePart(SpannedString(code), true))
                start = end
            }
            addText(start, value.length)
        }
    }
    Column(modifier, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        parts.forEachIndexed { index, part ->
            key(index) {
                if (part.diagram) MermaidDiagram(part.text.toString(), isLive || snapshot.source != text)
                else NativeMarkdownText(part.text, renderer, bodyStyle)
            }
        }
    }
}

@Composable private fun NativeMarkdownText(value: Spanned, renderer: Markwon, bodyStyle: androidx.compose.ui.text.TextStyle) {
    val color = MaterialTheme.colorScheme.onSurface.toArgb()
    val linkColor = MaterialTheme.colorScheme.primary.toArgb()
    val size = bodyStyle.fontSize.value
    val lineHeight = with(LocalDensity.current) { bodyStyle.lineHeight.toPx().roundToInt() }
    AndroidView(factory = { TextView(it).apply {
        // WRAP_CONTENT so short agent bubbles hug text; parents that need full width pass Modifier.fillMaxWidth().
        layoutParams = ViewGroup.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT)
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
