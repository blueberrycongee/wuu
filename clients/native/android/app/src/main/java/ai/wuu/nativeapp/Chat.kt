package ai.wuu.nativeapp

import org.json.JSONArray
import org.json.JSONObject

data class ChatMessage(val id: String, val role: String, val text: String, val contentRef: String = "", val attachments: List<String> = emptyList(), val tool: ToolActivity? = null, val turnId: String = "")
data class ToolActivity(val name: String, val status: String, val arguments: String, val result: String, val error: String) {
    val statusLabel get() = when (status) { "in_progress" -> "执行中"; "completed" -> "已完成"; "failed" -> "失败"; "ended" -> "已结束"; else -> "状态未知" }
    companion object {
        fun from(item: JSONObject, turnStatus: String): ToolActivity {
            val status = item.optString("status")
            return ToolActivity(item.optJSONObject("display")?.optString("label").orEmpty().ifEmpty { item.optString("name").ifEmpty { "工具" } },
                if (status == "in_progress" && turnStatus != "in_progress") "ended" else status, item.optString("arguments"),
                item.optString("result").ifEmpty { item.optJSONObject("result_detail")?.optJSONArray("content")?.objects()
                    ?.filter { it.optString("type") == "text" }?.joinToString("\n") { it.optString("text") }.orEmpty() }, item.optString("error"))
        }
    }
}
data class PendingMessage(val value: JSONObject, val held: Boolean) {
    val id get() = value.getString("id")
    val text get() = value.optString("prompt")
    val origin get() = value.optString("origin", "queue")
    fun resumeParams() = json("thread_id" to value.getString("thread_id"), "client_id" to id, "prompt" to text,
        "images" to (value.optJSONArray("images") ?: JSONArray()), "files" to (value.optJSONArray("files") ?: JSONArray()))
}
data class ThreadRow(val id: String, val title: String, val updated: String, val pinned: Boolean = false, val saved: Boolean = false, val archived: Boolean = false) {
    companion object {
        fun from(j: JSONObject, saved: Boolean = false) = ThreadRow(j.getString("id"), j.optString("title", j.optString("preview", "新会话")), j.optString("updated_at"), j.optBoolean("pinned"), saved, j.optBoolean("archived"))
    }
}
class ChatThread(val value: JSONObject, pending: List<JSONObject> = emptyList(), held: List<JSONObject> = emptyList()) {
    var pending = pending.map { PendingMessage(it, false) } + held.map { PendingMessage(it, true) }; private set
    val id get() = value.getString("id")
    val title get() = value.optString("title", "新会话")
    val running get() = value.optString("status") == "in_progress"
    val readOnly get() = value.optBoolean("read_only")
    val settings get() = ThreadSettings.from(value)
    val engine get() = value.optString("engine_id", "wuu")
    val historyCursor get() = value.optString("history_cursor")
    private val removedItems = mutableSetOf<String>()
    private val turns get() = value.optJSONArray("turns") ?: JSONArray().also { value.put("turns", it) }
    val messages: List<ChatMessage> get() = turns.objects().flatMap { turn ->
        val messages = (turn.optJSONArray("items") ?: JSONArray()).objects().mapNotNull { item ->
            val role = when (item.optString("type")) { "user_message" -> "user"; "agent_message" -> "assistant"; "error" -> "error"; "tool_call" -> "tool"; else -> return@mapNotNull null }
            ChatMessage(turn.getString("id") + ":" + item.getString("id"), role, item.optString("text", item.optString("error")), item.optString("remote_content_ref"),
                ((item.optJSONArray("images") ?: JSONArray()).objects() + (item.optJSONArray("files") ?: JSONArray()).objects()).map { it.toString() },
                if (role == "tool") ToolActivity.from(item, turn.optString("status")) else null, turnId = turn.getString("id"))
        }
        val error = turn.optJSONObject("error")?.optString("message").orEmpty()
        if (error.isNotEmpty() && messages.none { it.role == "error" }) messages + ChatMessage(turn.getString("id") + ":error", "error", error) else messages
    }
    // Pages may split a turn. Live fields and item deletions take precedence over older history.
    fun prependHistory(page: JSONObject) {
        if (page.optString("thread_id") != id || page.optString("cursor") != historyCursor || historyCursor.isEmpty()) return
        val current = turns.objects()
        val older = page.getJSONArray("turns").objects().map { old ->
            val live = current.firstOrNull { it.optString("id") == old.optString("id") }
            val liveItems = live?.optJSONArray("items")?.objects() ?: emptyList()
            val items = (old.optJSONArray("items") ?: JSONArray()).objects().filter { item ->
                liveItems.none { it.optString("id") == item.optString("id") } && "${old.optString("id")}:${item.optString("id")}" !in removedItems
            } + liveItems
            JSONObject(old.toString()).apply {
                live?.keys()?.forEach { put(it, live.get(it)) }
                put("items", JSONArray(items))
            }
        }
        value.put("turns", JSONArray(older + current.filter { turn -> older.none { it.optString("id") == turn.optString("id") } }))
        value.put("history_cursor", page.optString("history_cursor"))
    }
    fun expandContent(ref: String, item: JSONObject) {
        turns.objects().forEach { turn ->
            val items = turn.optJSONArray("items") ?: return@forEach
            val index = items.objects().indexOfFirst { it.optString("remote_content_ref") == ref && it.optString("id") == item.optString("id") }
            if (index >= 0) items.put(index, item)
        }
    }
    fun apply(method: String, params: JSONObject) {
        if (method == "thread/updated" && params.optJSONObject("thread")?.optString("id") == id) {
            val thread = params.getJSONObject("thread")
            listOf("title", "pinned", "archived", "updated_at", "model_provider", "model", "model_variant",
                "model_effort", "permission_mode", "engine_id", "read_only").forEach { value.put(it, thread.opt(it)) }
            return
        }
        if (method in listOf("turn/queued", "turn/steered") && params.optJSONObject("message")?.optString("thread_id") == id) {
            val message = params.getJSONObject("message")
            pending = pending.filterNot { it.id == message.optString("id") } + PendingMessage(message, false)
            return
        }
        if (params.optString("thread_id") != id) return
        if (method == "turn/held") {
            val held = params.getJSONArray("messages").objects().map { PendingMessage(it, true) }
            pending = pending.filterNot { old -> old.held || held.any { it.id == old.id } } + held
            return
        }
        if (method in listOf("turn/dequeued", "turn/unsteered")) {
            val removed = params.optString(if (method == "turn/dequeued") "queue_id" else "steer_id")
            pending = pending.filterNot { it.id == removed }; return
        }
        try {
        params.optJSONObject("turn")?.let { turn ->
            val index = turns.objects().indexOfFirst { it.getString("id") == turn.getString("id") }
            turns.put(if (index < 0) turns.length() else index, turn)
            if (method == "turn/started") value.put("status", "in_progress")
            if (method in listOf("turn/completed", "turn/interrupted", "turn/error")) value.put("status", "idle")
            return
        }
        if (method == "item/removed") removedItems.add("${params.optString("turn_id")}:${params.optString("item_id")}")
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
            if (method == "item/agentMessage/replace") items.getJSONObject(index).put("text", params.getString("text"))
        }
        } finally {
            val materialized = turns.objects().flatMap { (it.optJSONArray("items") ?: JSONArray()).objects() }.map { it.optString("client_id") }.toSet()
            pending = pending.filterNot { it.id in materialized }
        }
    }
}
internal fun savedMessages(thread: JSONObject?): List<ChatMessage> = thread?.optJSONArray("messages")?.objects()?.map {
    ChatMessage(it.optString("turn_id") + ":" + it.getString("id"), it.getString("role"), it.getString("text"))
} ?: emptyList()
