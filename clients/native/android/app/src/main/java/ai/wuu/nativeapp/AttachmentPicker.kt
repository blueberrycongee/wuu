package ai.wuu.nativeapp

import android.content.Context
import android.graphics.Bitmap
import android.graphics.ImageDecoder
import android.net.Uri
import android.provider.OpenableColumns
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Add
import androidx.compose.material.icons.filled.PhotoLibrary
import androidx.compose.material.icons.filled.Description
import androidx.compose.material.icons.filled.PhotoCamera
import androidx.compose.foundation.layout.*
import androidx.compose.ui.Modifier
import androidx.compose.ui.unit.dp
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.core.content.FileProvider
import java.io.File
import androidx.compose.material3.*
import androidx.compose.runtime.*
import androidx.compose.ui.platform.LocalContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.ByteArrayOutputStream
import java.io.InputStream
import java.nio.ByteBuffer

internal fun InputStream.readBounded(limit: Int): ByteArray {
    val out = ByteArrayOutputStream(); val buffer = ByteArray(16 * 1024)
    while (true) {
        val count = read(buffer, 0, minOf(buffer.size, limit + 1 - out.size()))
        if (count < 0) return out.toByteArray()
        out.write(buffer, 0, count)
        require(out.size() <= limit) { "源文件过大" }
    }
}

internal fun prepareAttachment(context: Context, uri: Uri): InputAttachment {
    val resolver = context.contentResolver
    val mime = resolver.getType(uri).orEmpty()
    val pdf = mime == "application/pdf"
    require(pdf || mime.startsWith("image/")) { "支持图片和 PDF 附件" }
    var name = if (pdf) "document.pdf" else "image.jpg"
    resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { cursor ->
        if (cursor.moveToFirst()) name = cursor.getString(0) ?: name
    }
    val bytes = requireNotNull(resolver.openInputStream(uri)) { "无法打开附件" }.use { it.readBounded(if (pdf) InputAttachment.MAX_BYTES else 32 * 1024 * 1024) }
    if (pdf) {
        require(bytes.take(5).toByteArray().contentEquals("%PDF-".toByteArray())) { "文件不是有效的 PDF" }
        return InputAttachment(name, mime, bytes)
    }
    val bitmap = decodeAttachmentImage(bytes)
    val output = ByteArrayOutputStream()
    try { check(bitmap.compress(Bitmap.CompressFormat.JPEG, 85, output)) } finally { bitmap.recycle() }
    return InputAttachment(name.substringBeforeLast('.') + ".jpg", "image/jpeg", output.toByteArray())
}

// ImageDecoder applies camera orientation and downsizes before allocating the bitmap.
internal fun decodeAttachmentImage(bytes: ByteArray, maxDimension: Int = 1600): Bitmap =
    ImageDecoder.decodeBitmap(ImageDecoder.createSource(ByteBuffer.wrap(bytes))) { decoder, info, _ ->
        val (width, height) = info.size.width to info.size.height
        require(width > 0 && height > 0 && width.toLong() * height <= 40_000_000) { "无法读取图片，或图片超过 4000 万像素" }
        val scale = minOf(1.0, maxDimension.toDouble() / maxOf(width, height))
        decoder.setTargetSize(maxOf(1, (width * scale).toInt()), maxOf(1, (height * scale).toInt()))
        decoder.allocator = ImageDecoder.ALLOCATOR_SOFTWARE
    }

@Composable fun AttachmentPicker(attachments: List<InputAttachment>, update: (List<InputAttachment>) -> Unit, model: AppModel, enabled: Boolean = true) {
    val context = LocalContext.current; val scope = rememberCoroutineScope()
    var menu by remember { mutableStateOf(false) }; var reading by remember { mutableStateOf(false) }
    var cameraPath by rememberSaveable { mutableStateOf<String?>(null) }
    val currentAttachments by rememberUpdatedState(attachments)
    val currentUpdate by rememberUpdatedState(update)
    fun accept(uri: Uri?, cleanup: File? = null) {
        if (uri == null) { cleanup?.delete(); return }
        reading = true
        scope.launch {
            try {
                val attachment = withContext(Dispatchers.IO) { prepareAttachment(context, uri) }
                val next = currentAttachments + attachment
                InputAttachment.validate(next, ""); currentUpdate(next)
            } catch (e: kotlinx.coroutines.CancellationException) { throw e }
            catch (e: Exception) { model.error = e.message }
            finally { reading = false; cleanup?.delete() }
        }
    }
    val photos = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { accept(it) }
    val files = rememberLauncherForActivityResult(ActivityResultContracts.OpenDocument()) { accept(it) }
    val camera = rememberLauncherForActivityResult(ActivityResultContracts.TakePicture()) { captured ->
        val file = cameraPath?.let(::File); cameraPath = null
        accept(if (captured && file != null) FileProvider.getUriForFile(context, context.packageName + ".exports", file) else null, file)
    }
    Box {
        ChromeButton(Icons.Default.Add, if (reading) "正在处理附件" else "添加附件", enabled && !reading && attachments.size < 4) { menu = true }
        DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
            DropdownMenuItem(
                text = { Text("拍照") },
                onClick = {
                    menu = false
                    try {
                        val folder = File(context.cacheDir, "exports").apply { mkdirs() }
                        val file = File.createTempFile("capture-", ".jpg", folder)
                        cameraPath = file.absolutePath
                        camera.launch(FileProvider.getUriForFile(context, context.packageName + ".exports", file))
                    } catch (e: Exception) {
                        cameraPath?.let(::File)?.delete(); cameraPath = null; model.error = "无法打开相机：${e.message}"
                    }
                },
                leadingIcon = { Icon(Icons.Default.PhotoCamera, null) },
            )
            DropdownMenuItem(
                text = { Text("照片") },
                onClick = {
                    menu = false
                    photos.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly))
                },
                leadingIcon = { Icon(Icons.Default.PhotoLibrary, null) },
            )
            DropdownMenuItem(
                text = { Text("文件") },
                onClick = {
                    menu = false
                    files.launch(arrayOf("image/*", "application/pdf"))
                },
                leadingIcon = { Icon(Icons.Default.Description, null) },
            )
            DropdownMenuItem(
                text = {
                    Text(
                        "最多 4 个图片或 PDF，合计 3 MB",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                },
                onClick = {},
                enabled = false,
            )
        }
    }
}
