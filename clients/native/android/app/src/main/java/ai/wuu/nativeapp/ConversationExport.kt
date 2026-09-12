package ai.wuu.nativeapp

import android.content.ClipData
import android.content.Context
import android.content.Intent
import android.content.ActivityNotFoundException
import androidx.core.content.FileProvider
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.util.UUID

suspend fun shareAttachment(context: Context, attachment: LoadedAttachment, view: Boolean, isCurrent: () -> Boolean) {
    val extension = when (attachment.mediaType) {
        "application/pdf" -> "pdf"; "image/jpeg" -> "jpg"; "image/png" -> "png"; "image/gif" -> "gif"; "image/webp" -> "webp"
        else -> error("不支持的附件类型")
    }
    val file = withContext(Dispatchers.IO) {
        val directory = exportDirectory(context)
        // Never use a remote filename as a filesystem path.
        File(directory, "attachment-${UUID.randomUUID()}.$extension").apply { writeBytes(attachment.data) }
    }
    if (!isCurrent()) { withContext(Dispatchers.IO) { file.delete() }; return }
    val uri = FileProvider.getUriForFile(context, "${context.packageName}.exports", file)
    val intent = if (view) Intent(Intent.ACTION_VIEW).setDataAndType(uri, attachment.mediaType)
        else Intent(Intent.ACTION_SEND).setType(attachment.mediaType).putExtra(Intent.EXTRA_STREAM, uri)
    intent.addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    intent.clipData = ClipData.newRawUri("attachment", uri)
    try { context.startActivity(if (view) intent else Intent.createChooser(intent, "分享附件")) }
    catch (_: ActivityNotFoundException) { file.delete(); error("设备上没有可用的阅读器，请使用分享原文件保存或发送附件") }
}

private fun exportDirectory(context: Context): File = File(context.cacheDir, "exports").apply {
    mkdirs()
    val cutoff = System.currentTimeMillis() - 24 * 60 * 60 * 1000
    listFiles()?.filter { it.lastModified() < cutoff }?.forEach { it.delete() }
}

suspend fun exportConversation(context: Context, messages: List<ChatMessage>, isCurrent: () -> Boolean) {
    // A transcript can exceed Binder's transaction limit; grant access to one file.
    val file = withContext(Dispatchers.IO) {
        val directory = exportDirectory(context)
        File(directory, "conversation-${UUID.randomUUID()}.txt").apply {
            bufferedWriter().use { out -> messages.filter { it.tool == null }.forEach { out.append("## ${it.role}\n\n${it.text}\n\n") } }
        }
    }
    if (!isCurrent()) { withContext(Dispatchers.IO) { file.delete() }; return }
    val uri = FileProvider.getUriForFile(context, "${context.packageName}.exports", file)
    val intent = Intent(Intent.ACTION_SEND).setType("text/plain")
        .putExtra(Intent.EXTRA_STREAM, uri).addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
    intent.clipData = ClipData.newRawUri("conversation", uri)
    context.startActivity(Intent.createChooser(intent, "导出对话"))
}
