package ai.wuu.nativeapp

import org.json.JSONArray
import org.json.JSONObject
import java.util.Base64
import kotlinx.coroutines.currentCoroutineContext
import kotlinx.coroutines.ensureActive

data class LoadedAttachment(val mediaType: String, val filename: String, val data: ByteArray)

// Explicit large-message reads use bounded chunks and an immutable content digest.
suspend fun Remote.readContent(reference: String, threadID: String): JSONObject {
    require(reference.startsWith("content:")) { "无效的消息引用" }
    val parts = JSONArray(String(reference.removePrefix("content:").unb64(), Charsets.UTF_8))
    require(parts.length() == 4 && parts.getString(0) == threadID) { "消息不属于当前会话" }
    val encoded = readEncoded("thread/content/read", json("thread_id" to parts.getString(0), "turn_id" to parts.getString(1),
        "item_id" to parts.getString(2), "sha256" to parts.getString(3)), "application/json")
    val data = Base64.getDecoder().decode(encoded)
    check(sha256(data).joinToString("") { "%02x".format(it) } == parts.getString(3)) { "消息内容校验失败" }
    return JSONObject(String(data, Charsets.UTF_8)).also { check(it.getString("id") == parts.getString(2)) { "消息引用不匹配" } }
}

// Do not fetch attachment URLs, or references belonging to another message.
suspend fun Remote.readAttachment(attachment: JSONObject, threadID: String, messageID: String): LoadedAttachment {
    val media = attachment.optString("media_type")
    require(media in setOf("image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf")) { "手机暂不支持此附件格式" }
    var encoded = attachment.optString("data")
    val ref = attachment.optString("remote_ref")
    if (ref.isNotEmpty()) {
        require(ref.startsWith("thread:") && ref.length < 8192) { "无效的附件引用" }
        val parts = JSONArray(String(ref.removePrefix("thread:").unb64(), Charsets.UTF_8))
        require(parts.length() == 5 && parts.getString(0) == threadID &&
            parts.getString(1) + ":" + parts.getString(2) == messageID &&
            parts.getDouble(3) >= 0 && parts.getDouble(3) == parts.getInt(3).toDouble() && parts.getString(4).length == 64) { "附件不属于当前消息" }
        encoded = readEncoded("thread/attachment/read", json("thread_id" to threadID, "turn_id" to parts.getString(1),
            "item_id" to parts.getString(2), "index" to parts.getInt(3), "sha256" to parts.getString(4)), media)
        check(sha256((media + "\u0000" + encoded).toByteArray()).joinToString("") { "%02x".format(it) } == parts.getString(4)) { "附件内容校验失败" }
    }
    require(encoded.isNotEmpty() && encoded.length <= 16 * 1024 * 1024) { "附件过大或内容无效，请在电脑上查看" }
    val data = Base64.getDecoder().decode(encoded)
    require(data.isNotEmpty()) { "附件内容为空" }
    if (media == "application/pdf") require(data.take(5).toByteArray().contentEquals("%PDF-".toByteArray())) { "文件不是有效的 PDF" }
    return LoadedAttachment(media, attachment.optString("filename", "图片"), data)
}

private suspend fun Remote.readEncoded(method: String, params: JSONObject, mediaType: String): String {
    val encoded = StringBuilder()
    var total: Int? = null
    do {
        currentCoroutineContext().ensureActive()
        val result = call(method, params.put("offset", encoded.length))
        val count = result.getLong("total")
        val chunk = result.getString("data")
        check(count in 1..16L * 1024 * 1024 && result.getDouble("total") == count.toDouble() && result.getString("content_type") == mediaType &&
            result.getDouble("offset") == encoded.length.toDouble() && chunk.isNotEmpty() && chunk.length <= 128 * 1024 &&
            encoded.length + chunk.length <= count && (total == null || total.toLong() == count)) { "消息过大或读取不完整，请在电脑上查看" }
        total = count.toInt(); encoded.append(chunk)
    } while (encoded.length < checkNotNull(total))
    return encoded.toString()
}
