package ai.wuu.nativeapp

import java.util.Base64
import java.util.UUID

class InputAttachment(filename: String, val mediaType: String, val data: ByteArray) {
    val id: String = UUID.randomUUID().toString()
    val filename = filename.substringAfterLast('/').substringAfterLast('\\').take(200)
    val isImage get() = mediaType.startsWith("image/")
    init {
        require(data.isNotEmpty() && data.size <= MAX_BYTES) { "附件需介于 1 字节和 3 MB 之间" }
        require(mediaType in listOf("image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf")) { "支持图片和 PDF 附件" }
    }
    fun value() = json("media_type" to mediaType, "data" to Base64.getEncoder().encodeToString(data)).apply {
        if (!isImage) put("filename", filename)
    }
    companion object {
        const val MAX_BYTES = 3 * 1024 * 1024
        fun validate(attachments: List<InputAttachment>, text: String) {
            // JSON and the relay's base64 envelope must fit the phone's 8 MB frame cap.
            require(attachments.size <= 4 && attachments.sumOf { it.data.size.toLong() } <= MAX_BYTES) { "每条消息最多 4 个附件，合计不超过 3 MB" }
            require(text.toByteArray().size <= 128 * 1024) { "文字消息不能超过 128 KB" }
        }
    }
}

class ChatInput(val text: String, val attachments: List<InputAttachment> = emptyList()) {
    init { InputAttachment.validate(attachments, text) }
    fun params(threadID: String, queued: Boolean) = json("thread_id" to threadID, "prompt" to text,
        "images" to org.json.JSONArray(attachments.filter { it.isImage }.map { it.value() }),
        "files" to org.json.JSONArray(attachments.filterNot { it.isImage }.map { it.value() })).apply {
        // turn/start rejects unknown fields; only queued/steered inputs accept client_id.
        if (queued) put("client_id", UUID.randomUUID().toString())
    }
}
