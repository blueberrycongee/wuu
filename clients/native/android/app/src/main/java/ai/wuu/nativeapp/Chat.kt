package ai.wuu.nativeapp

import org.json.JSONArray
import org.json.JSONObject

data class ChatMessage(val id: String, val role: String, val text: String)
data class ThreadRow(val id: String, val title: String, val updated: String, val pinned: Boolean = false, val saved: Boolean = false) {
    companion object {
        fun from(j: JSONObject, saved: Boolean = false) = ThreadRow(j.getString("id"), j.optString("title", j.optString("preview", "新会话")), j.optString("updated_at"), j.optBoolean("pinned"), saved)
    }
}
class ChatThread(val value: JSONObject, private var pending: List<JSONObject> = emptyList()) {
    val id get() = value.getString("id")
    val title get() = value.optString("title", "新会话")
    val running get() = value.optString("status") == "in_progress"
    val readOnly get() = value.optBoolean("read_only")
    private val turns get() = value.optJSONArray("turns") ?: JSONArray().also { value.put("turns", it) }
    val messages: List<ChatMessage> get() = turns.objects().flatMap { turn ->
        (turn.optJSONArray("items") ?: JSONArray()).objects().mapNotNull { item ->
            val role = when (item.optString("type")) { "user_message" -> "user"; "agent_message" -> "assistant"; "error" -> "error"; else -> return@mapNotNull null }
            ChatMessage(turn.getString("id") + ":" + item.getString("id"), role, item.optString("text", item.optString("error")))
        }
    } + pending.map { ChatMessage("pending:" + it.getString("id"), "pending", it.optString("prompt")) }
    fun apply(method: String, params: JSONObject) {
        if (method == "turn/queued" && params.optJSONObject("message")?.optString("thread_id") == id) {
            val message = params.getJSONObject("message")
            pending = pending.filterNot { it.optString("id") == message.optString("id") } + message
            return
        }
        if (params.optString("thread_id") != id) return
        if (method == "turn/dequeued") { pending = pending.filterNot { it.optString("id") == params.optString("queue_id") }; return }
        params.optJSONObject("turn")?.let { turn ->
            val index = turns.objects().indexOfFirst { it.getString("id") == turn.getString("id") }
            turns.put(if (index < 0) turns.length() else index, turn)
            if (method == "turn/started") value.put("status", "in_progress")
            if (method in listOf("turn/completed", "turn/interrupted", "turn/error")) value.put("status", "idle")
            return
        }
        val turn = turns.objects().firstOrNull { it.optString("id") == params.optString("turn_id") } ?: return
        val items = turn.optJSONArray("items") ?: JSONArray().also { turn.put("items", it) }
        if (method in listOf("item/started", "item/completed")) {
            val item = params.getJSONObject("item")
            val index = items.objects().indexOfFirst { it.getString("id") == item.getString("id") }
            items.put(if (index < 0) items.length() else index, item)
        } else {
            val index = items.objects().indexOfFirst { it.optString("id") == params.optString("item_id") }
            if (index < 0) return
            if (method == "item/removed") items.remove(index)
            if (method == "item/agentMessage/delta") items.getJSONObject(index).let { it.put("text", it.optString("text") + params.optString("delta")) }
        }
    }
}
internal fun savedMessages(thread: JSONObject?): List<ChatMessage> = thread?.optJSONArray("messages")?.objects()?.map {
    ChatMessage(it.optString("turn_id") + ":" + it.getString("id"), it.getString("role"), it.getString("text"))
} ?: emptyList()
