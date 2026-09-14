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

// Both native surfaces consume the same immutable references as desktop.
suspend fun Remote.readAttachment(attachment: JSONObject, threadID: String, messageID: String, preview: Boolean = false): LoadedAttachment =
    readMessageAttachment({ method, params -> call(method, params) }, attachment, threadID, messageID, preview)

internal suspend fun readMessageAttachment(call: suspend (String, JSONObject) -> JSONObject,
    attachment: JSONObject, scopeID: String, messageID: String, preview: Boolean = false): LoadedAttachment {
    val media = attachment.optString("media_type")
    require(media in setOf("image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf")) { "手机暂不支持此附件格式" }
    var encoded = attachment.optString("data")
    val ref = attachment.optString("remote_ref")
    if (ref.startsWith("markdown:")) {
        require(ref.length < 8192) { "无效的图片引用" }
        val parts = JSONArray(String(ref.substringAfter(':').unb64(), Charsets.UTF_8))
        require(parts.length() == 5 && parts.getString(0) in listOf("thread", "channel") && parts.getString(1) == scopeID &&
            (if (parts.getString(0) == "thread") parts.getString(2) + ":" + parts.getString(3) else parts.getString(3)) == messageID) { "图片不属于当前消息" }
        val params = json("kind" to parts.getString(0), "scope_id" to scopeID, "turn_id" to parts.getString(2),
            "message_id" to parts.getString(3), "source" to parts.getString(4), "preview" to preview)
        if (parts.getString(0) == "channel") params.put("seq", parts.getString(2).toLong())
        encoded = readEncoded(call, "message/image/read", params, if (preview) "image/jpeg" else media,
            if (preview) 128 * 1024 else 16 * 1024 * 1024, verifyDigest = !preview)
    } else if (ref.isNotEmpty()) {
        require(ref.length < 8192 && (ref.startsWith("thread:") || ref.startsWith("channel:"))) { "无效的附件引用" }
        val channel = ref.startsWith("channel:")
        val parts = JSONArray(String(ref.substringAfter(':').unb64(), Charsets.UTF_8))
        val index = parts.optDouble(3, -1.0)
        require(parts.getString(0) == scopeID && index >= 0 && index == parts.getInt(3).toDouble() && parts.getString(4).length == 64) { "附件不属于当前消息" }
        val params = if (channel) {
            require(parts.length() == 6 && parts.getString(1) == messageID && parts.getLong(2) > 0 && parts.getDouble(2) == parts.getLong(2).toDouble() && parts.getString(5) in listOf("images", "files")) { "附件不属于当前消息" }
            json("room_id" to scopeID, "message_id" to messageID, "seq" to parts.getLong(2), "field" to parts.getString(5))
        } else {
            require(parts.length() in 5..6 && parts.getString(1) + ":" + parts.getString(2) == messageID && (parts.length() == 5 || parts.getString(5) == "result")) { "附件不属于当前消息" }
            json("thread_id" to scopeID, "turn_id" to parts.getString(1), "item_id" to parts.getString(2), "kind" to parts.optString(5))
        }
        params.put("index", parts.getInt(3)).put("sha256", parts.getString(4)).put("preview", preview)
        encoded = readEncoded(call, if (channel) "channel/attachment/read" else "thread/attachment/read", params,
            if (preview) "image/jpeg" else media, if (preview) 128 * 1024 else 16 * 1024 * 1024)
        if (!preview) check(sha256((media + "\u0000" + encoded).toByteArray()).joinToString("") { "%02x".format(it) } == parts.getString(4)) { "附件内容校验失败" }
    }
    return decodeInlineAttachment(if (preview && ref.isNotEmpty()) json("media_type" to "image/jpeg", "filename" to attachment.optString("filename", "图片")) else attachment, encoded)
}

internal fun decodeInlineAttachment(attachment: JSONObject, encoded: String = attachment.optString("data")): LoadedAttachment {
    val media = attachment.optString("media_type")
    require(media in setOf("image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf")) { "手机暂不支持此附件格式" }
    require(encoded.isNotEmpty() && encoded.length <= 16 * 1024 * 1024) { "附件过大或内容无效，请在电脑上查看" }
    val data = Base64.getDecoder().decode(encoded)
    require(data.isNotEmpty()) { "附件内容为空" }
    if (media == "application/pdf") require(data.take(5).toByteArray().contentEquals("%PDF-".toByteArray())) { "文件不是有效的 PDF" }
    return LoadedAttachment(media, attachment.optString("filename", "图片"), data)
}

private suspend fun Remote.readEncoded(method: String, params: JSONObject, mediaType: String): String =
    readEncoded({ name, request -> call(name, request) }, method, params, mediaType)

private suspend fun readEncoded(call: suspend (String, JSONObject) -> JSONObject, method: String, params: JSONObject, mediaType: String, limit: Int = 16 * 1024 * 1024, verifyDigest: Boolean = false): String {
    val encoded = StringBuilder()
    var total: Int? = null
    do {
        currentCoroutineContext().ensureActive()
        val result = call(method, params.put("offset", encoded.length))
        val count = result.getLong("total")
        val chunk = result.getString("data")
        check(count in 1..limit.toLong() && result.getDouble("total") == count.toDouble() && result.getString("content_type") == mediaType &&
            result.getDouble("offset") == encoded.length.toDouble() && chunk.isNotEmpty() && chunk.length <= 128 * 1024 &&
            encoded.length + chunk.length <= count && (total == null || total.toLong() == count)) { "消息过大或读取不完整，请在电脑上查看" }
        if (verifyDigest) {
            val digest = result.getString("sha256")
            check(digest.matches(Regex("[a-f0-9]{64}")) && (!params.has("sha256") || params.getString("sha256") == digest)) { "图片读取时发生变化" }
            params.put("sha256", digest)
        }
        total = count.toInt(); encoded.append(chunk)
    } while (encoded.length < checkNotNull(total))
    if (verifyDigest) check(sha256((mediaType + "\u0000" + encoded).toByteArray()).joinToString("") { "%02x".format(it) } == params.getString("sha256")) { "图片内容校验失败" }
    return encoded.toString()
}
