package ai.wuu.nativeapp

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.*
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.Dialog
import androidx.compose.ui.window.DialogProperties
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.CancellationException

@Composable fun AttachmentPreview(model: AppModel, attachment: LoadedAttachment) {
    val context = LocalContext.current
    var bitmap by remember(attachment) { mutableStateOf<Bitmap?>(null) }
    var error by remember(attachment) { mutableStateOf<String?>(null) }
    val pdf = attachment.mediaType == "application/pdf"
    LaunchedEffect(attachment) {
        if (!pdf) try { bitmap = withContext(Dispatchers.Default) { decodeAttachmentImage(attachment.data) } }
        catch (e: CancellationException) { throw e }
        catch (e: Exception) { error = e.message ?: "无法读取图片" }
    }
    Dialog(onDismissRequest = { model.attachmentPreview = null }, properties = DialogProperties(usePlatformDefaultWidth = false)) {
        Surface(Modifier.fillMaxSize().systemBarsPadding()) {
            Column(Modifier.padding(16.dp), verticalArrangement = Arrangement.spacedBy(16.dp)) {
                Row {
                    Text(attachment.filename, Modifier.weight(1f), style = MaterialTheme.typography.titleMedium)
                    TextButton(onClick = { model.attachmentPreview = null }) { Text("关闭") }
                }
                if (pdf) {
                    Text("使用设备上的 PDF 阅读器查看文件。")
                    Button(onClick = { model.perform { model.exportAttachment(context, attachment, view = true) } }) { Text("打开 PDF") }
                } else {
                    bitmap?.let { Image(it.asImageBitmap(), "附件图片", Modifier.weight(1f).fillMaxWidth()) }
                    if (bitmap == null && error == null) CircularProgressIndicator()
                }
                error?.let { Text(it, color = MaterialTheme.colorScheme.error) }
                Button(onClick = { model.perform { model.exportAttachment(context, attachment, view = false) } }) { Text("分享原文件") }
            }
        }
    }
}
