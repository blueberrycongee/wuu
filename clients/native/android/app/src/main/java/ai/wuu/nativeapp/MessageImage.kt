package ai.wuu.nativeapp

import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.BrokenImage
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.layout.ContentScale
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Semaphore
import kotlinx.coroutines.sync.withPermit
import kotlinx.coroutines.withContext

internal class ImagePreviewLoader {
    private val slots = Semaphore(2)
    private val cache = LinkedHashMap<String, ImageBitmap>(48, 0.75f, true)
    private var generation = 0
    fun clear() { generation++; cache.clear() }
    suspend fun load(key: String, read: suspend () -> LoadedAttachment): ImageBitmap {
        val stamp = generation
        return slots.withPermit {
            if (stamp != generation) throw CancellationException()
            cache[key]?.let { return@withPermit it }
            val data = read().data
            val image = withContext(Dispatchers.Default) { decodeAttachmentImage(data, 384).asImageBitmap() }
            if (stamp != generation) throw CancellationException()
            cache[key] = image
            if (cache.size > 48) cache.remove(cache.keys.first())
            image
        }
    }
}

@Composable internal fun MessageImage(key: String, connected: Boolean, loader: ImagePreviewLoader, read: suspend () -> LoadedAttachment, open: () -> Unit) {
    var picture by remember(key) { mutableStateOf<ImageBitmap?>(null) }
    var failed by remember(key) { mutableStateOf(false) }
    var attempt by remember(key) { mutableIntStateOf(0) }
    val currentRead by rememberUpdatedState(read)
    LaunchedEffect(key, connected, attempt) {
        if (picture != null || !connected) return@LaunchedEffect
        failed = false
        try { picture = loader.load(key) { currentRead() } }
        catch (e: CancellationException) { throw e }
        catch (_: Exception) { failed = true }
    }
    val image = picture
    val ratio = image?.let { (it.width.toFloat() / it.height).coerceIn(0.65f, 1.8f) } ?: 1.25f
    Surface(onClick = { if (failed) attempt++ else open() }, enabled = connected,
        shape = RoundedCornerShape(12.dp), color = MaterialTheme.colorScheme.surfaceContainerHighest,
        modifier = Modifier.width(232.dp).aspectRatio(ratio)) {
        if (image != null) Image(image, "图片，点击查看原图", Modifier.fillMaxSize(), contentScale = ContentScale.Fit)
        else Box(contentAlignment = Alignment.Center) {
            if (failed) Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Icon(Icons.Default.BrokenImage, null)
                Text("点击重试", style = MaterialTheme.typography.labelMedium)
            } else if (connected) CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
            else Icon(Icons.Default.Refresh, "连接后加载图片")
        }
    }
}

@Composable internal fun DraftImage(attachment: InputAttachment) {
    val picture by produceState<ImageBitmap?>(null, attachment.id) {
        value = withContext(Dispatchers.Default) { runCatching { decodeAttachmentImage(attachment.data, 192).asImageBitmap() }.getOrNull() }
    }
    picture?.let { Image(it, attachment.filename, Modifier.fillMaxSize(), contentScale = ContentScale.Crop) }
}

@Composable internal fun MessageAttachments(model: AppModel, message: ChatMessage) {
    message.attachments.forEachIndexed { index, raw ->
        val attachment = org.json.JSONObject(raw)
        if (attachment.optString("media_type").startsWith("image/")) MessageImage("${model.activeID}:${message.id}:$index:${attachment.optString("remote_ref")}", model.connected, model.imagePreviews,
            read = { model.attachmentThumbnail(message, index) }, open = { model.perform { model.previewAttachment(message, index) } })
        else TextButton(onClick = { model.perform { model.previewAttachment(message, index) } }, enabled = model.connected && !model.loadingAttachment) {
            Text(attachment.optString("filename", "查看文件"))
        }
    }
}
