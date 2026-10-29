package ai.wuu.nativeapp

import java.io.File
import java.io.FileOutputStream
import java.util.Base64
import java.util.UUID
import org.json.JSONArray
import org.json.JSONObject

/** Local retry data only, not authority. An explicit retry adopts it after identity and agent validation. */
class PersonalStoredDraft internal constructor(val agentID: String, val requestID: String, val prompt: String, val attachments: List<InputAttachment>) {
    companion object {
        internal fun validate(prompt: String, attachments: List<InputAttachment>) {
            InputAttachment.validate(attachments, prompt)
            require(prompt.toByteArray(Charsets.UTF_8).size <= 64_000) { "Personal 消息不能超过 64,000 字节" }
            if (prompt.isBlank() && attachments.isEmpty()) throw PersonalClientException(PersonalClientFailure.EMPTY_MESSAGE)
            attachments.forEach { file -> require(file.filename.isNotBlank() && file.filename.toByteArray(Charsets.UTF_8).size <= 255 && !file.filename.contains('/') && !file.filename.contains('\\') && file.filename.none { Character.isISOControl(it) }) { "Personal 附件名称不能包含控制字符或超过 255 字节" } }
        }
    }
}
/** Durable no-backup app-private storage, separate from the OS-purgeable display cache. */
class PersonalDraftStore(directory: File, identity: List<String>) {
    private val scope = sha256(JSONArray(identity).toString().toByteArray()).joinToString("") { "%02x".format(it) }
    private val file = File(directory, "$scope.json")
    private var valid = true
    @Synchronized fun save(draft: PersonalMessageDraft) {
        check(valid) { "Retry scope expired" }; PersonalStoredDraft.validate(draft.prompt, draft.attachments)
        val value = json("version" to 1, "scope" to scope, "agent_id" to draft.agentID, "request_id" to draft.requestID, "prompt" to draft.prompt, "attachments" to JSONArray(draft.attachments.map { data -> json("filename" to data.filename, "media_type" to data.mediaType, "data" to Base64.getEncoder().encodeToString(data.data), "sha256" to sha256(data.data).joinToString("") { "%02x".format(it) }) }))
        val bytes = value.toString().toByteArray(); check(bytes.size <= MAX_BYTES)
        check(file.parentFile!!.isDirectory || file.parentFile!!.mkdirs())
        val pending = File(file.parentFile, file.name + ".tmp")
        FileOutputStream(pending).use { it.write(bytes); it.fd.sync() }
        check(pending.renameTo(file)) { "无法保存尚未确认的消息，未发起新的发送" }
    }
    @Synchronized fun load(): PersonalStoredDraft? {
        check(valid) { "Retry scope expired" }; if (!file.exists()) return null
        check(file.length() <= MAX_BYTES) { "Invalid retry storage size" }
        val value = JSONObject(file.readText()); check(value.getInt("version") == 1 && value.getString("scope") == scope)
        val agentID = value.getString("agent_id"); check(agentID.matches(Regex("[A-Za-z0-9_.:-]{1,256}")))
        val requestID = value.getString("request_id"); UUID.fromString(requestID)
        val files = value.getJSONArray("attachments"); check(files.length() <= 4)
        val attachments = files.objects().map { data ->
            val bytes = Base64.getDecoder().decode(data.getString("data")); check(sha256(bytes).joinToString("") { "%02x".format(it) } == data.getString("sha256"))
            InputAttachment(data.getString("filename"), data.getString("media_type"), bytes).also { check(it.filename == data.getString("filename")) }
        }
        val prompt = value.getString("prompt"); PersonalStoredDraft.validate(prompt, attachments)
        return PersonalStoredDraft(agentID, requestID, prompt, attachments)
    }
    @Synchronized fun clear() { check(valid) { "Retry scope expired" }; if (file.exists()) check(file.delete()) { "无法清除尚未确认的消息" } }
    @Synchronized fun invalidate(remove: Boolean) { valid = false; if (remove && file.exists()) check(file.delete()) { "无法清除尚未确认的消息" } }
    companion object { private const val MAX_BYTES = 5 * 1024 * 1024 }
}
