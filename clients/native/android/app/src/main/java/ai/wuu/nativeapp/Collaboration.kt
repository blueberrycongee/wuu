package ai.wuu.nativeapp

import androidx.compose.runtime.*
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import org.json.JSONArray
import org.json.JSONObject

/** Host-scoped public room state. Never consumes private harness event bodies. */
class Collaboration(private val call: suspend (String, JSONObject) -> JSONObject) {
    var visible by mutableStateOf(false); private set
    var selectedID by mutableStateOf<String?>(null); private set
    var rooms by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var agents by mutableStateOf<List<JSONObject>>(emptyList()); private set
    var error by mutableStateOf<String?>(null); private set
    var loading by mutableStateOf(false); private set
    val drafts = mutableStateMapOf<String, String>()
    val attachments = mutableStateMapOf<String, List<InputAttachment>>()
    private val timelines = mutableStateMapOf<String, List<JSONObject>>()
    private val previews = mutableStateMapOf<String, List<JSONObject>>()
    private val older = mutableStateMapOf<String, Boolean>()
    private val taskCursor = mutableMapOf<String, Int>()
    private val sendingRooms = mutableStateMapOf<String, String>()
    private var lifetime = 0L
    private var epoch = 0L
    private val refreshLock = Mutex()
    val messages get() = timelines[selectedID].orEmpty()
    val responses get() = previews[selectedID].orEmpty()
    val hasOlder get() = older[selectedID] == true
    val sending get() = sendingRooms.containsKey(selectedID)
    val selectedRoom get() = rooms.firstOrNull { it.optString("id") == selectedID }

    fun mode(value: Boolean) { if (visible != value) { visible = value; invalidate() } }
    fun select(id: String?) { if (selectedID != id) { selectedID = id; invalidate() } }
    fun invalidate() { epoch++; loading = false }
    fun clear() {
        invalidate(); lifetime++; visible = false; selectedID = null; rooms = emptyList(); agents = emptyList()
        drafts.clear(); attachments.clear(); timelines.clear(); previews.clear(); older.clear(); taskCursor.clear(); sendingRooms.clear(); error = null
    }
    private fun check(stamp: Long) { if (stamp != epoch) throw CancellationException() }
    fun agentName(id: String) = agents.firstOrNull { it.optString("id") == id }?.optString("name")?.takeIf { it.isNotBlank() } ?: id
    fun roomName(room: JSONObject): String {
        if (room.optString("kind") == "dm") room.optJSONArray("members")?.objects()
            ?.firstOrNull { it.optString("member_type") == "agent" }?.let { return agentName(it.optString("member_id")) }
        return room.optString("name").ifBlank { "群聊" }
    }

    suspend fun refresh() = refreshLock.withLock {
        val stamp = epoch
        if (!visible) return@withLock
        loading = true
        try {
            val directory = call("channel/bootstrap", json()); check(stamp)
            agents = directory.optJSONArray("agents")?.objects().orEmpty()
            rooms = directory.optJSONArray("rooms")?.objects().orEmpty().sortedByDescending {
                it.optJSONObject("last_message")?.optString("created_at") ?: it.optString("created_at")
            }
            val accessible = rooms.map { it.optString("id") }.toSet()
            for (removed in (timelines.keys + drafts.keys + attachments.keys + sendingRooms.keys).toSet() - accessible) {
                timelines.remove(removed); previews.remove(removed); drafts.remove(removed); attachments.remove(removed); older.remove(removed); taskCursor.remove(removed)
                sendingRooms.remove(removed)
            }
            val id = selectedID
            if (id != null && rooms.none { it.optString("id") == id }) {
                timelines.remove(id); previews.remove(id); drafts.remove(id); attachments.remove(id)
                selectedID = null
            } else if (id != null) {
                // Refresh the visible tail (including mutable tasks) and fill any intervening gap.
                val latest = call("channel/message/list", json("room_id" to id, "latest" to true, "limit" to PAGE, "attachment_metadata_only" to true)); check(stamp)
                val page = latest.optJSONArray("messages")?.objects().orEmpty()
                val previous = timelines[id].orEmpty()
                var tail = previous.lastOrNull()?.optLong("seq") ?: 0
                if (tail > 0 && page.firstOrNull()?.optLong("seq")?.let { it > tail + 1 } == true) {
                    do {
                        val next = call("channel/message/list", json("room_id" to id, "after_seq" to tail, "limit" to PAGE, "attachment_metadata_only" to true)); check(stamp)
                        val gap = next.optJSONArray("messages")?.objects().orEmpty()
                        val end = gap.lastOrNull()?.optLong("seq") ?: tail
                        if (end <= tail) break
                        merge(id, gap); tail = end
                    } while (tail < page.first().optLong("seq") - 1)
                }
                merge(id, page)
                if (previous.isEmpty()) older[id] = page.size == PAGE
                // Old tasks remain mutable. Refresh one per tick rather than re-downloading
                // every historical message when the user has paged far back.
                val olderTasks = timelines[id].orEmpty().filter {
                    it.optString("kind") == "task" && it.optLong("seq") < (page.firstOrNull()?.optLong("seq") ?: 0)
                }
                if (olderTasks.isNotEmpty()) {
                    val index = (taskCursor[id] ?: 0) % olderTasks.size
                    val seq = olderTasks[index].getLong("seq")
                    val updated = call("channel/message/list", json("room_id" to id, "after_seq" to seq - 1,
                        "before_seq" to seq + 1, "limit" to 1, "attachment_metadata_only" to true)); check(stamp)
                    merge(id, updated.optJSONArray("messages")?.objects().orEmpty())
                    taskCursor[id] = (index + 1) % olderTasks.size
                }
                val settled = timelines[id].orEmpty().map { it.optString("source_session_ref") to it.optString("source_turn_id") }.toSet()
                previews[id] = latest.optJSONArray("responses")?.objects().orEmpty().filterNot {
                    (it.optString("session_ref") to it.optString("turn_id")) in settled
                }
                call("channel/room/read", json("room_id" to id)); check(stamp)
            }
            error = null
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { check(stamp); error = e.message ?: "协作读取失败" }
        finally { if (epoch == stamp) loading = false }
    }

    suspend fun loadOlder() = refreshLock.withLock {
        val id = selectedID ?: return@withLock; val stamp = epoch
        val first = messages.firstOrNull()?.optLong("seq") ?: return@withLock
        val result = call("channel/message/list", json("room_id" to id, "latest" to true, "before_seq" to first, "limit" to PAGE, "attachment_metadata_only" to true)); check(stamp)
        val page = result.optJSONArray("messages")?.objects().orEmpty()
        merge(id, page); older[id] = page.size == PAGE
    }
    private fun merge(id: String, page: List<JSONObject>) {
        timelines[id] = mergeRoomMessages(timelines[id].orEmpty(), page)
    }
    suspend fun direct(agentID: String) {
        val stamp = epoch
        val result = call("channel/direct-message/open", json("agent_id" to agentID)); check(stamp)
        val room = result.getJSONObject("room")
        rooms = rooms.filterNot { it.optString("id") == room.getString("id") } + room
        select(room.getString("id"))
    }
    suspend fun create(name: String, members: Set<String>) {
        require(name.isNotBlank() && members.isNotEmpty()) { "填写群名并选择成员" }
        val stamp = epoch
        val result = call("channel/room/create", json("name" to name.trim(), "agent_ids" to JSONArray(members.toList()))); check(stamp)
        val room = result.getJSONObject("room"); rooms = rooms + room; select(room.getString("id"))
    }
    suspend fun send() {
        val id = selectedID ?: return
        if (sendingRooms.containsKey(id)) return
        val text = drafts[id].orEmpty(); val files = attachments[id].orEmpty()
        if (text.isBlank() && files.isEmpty()) return
        InputAttachment.validate(files, text)
        val stamp = lifetime
        val token = java.util.UUID.randomUUID().toString()
        sendingRooms[id] = token
        try {
            val result = call("channel/message/send", json("room_id" to id, "body" to text,
                "images" to JSONArray(files.filter { it.isImage }.map { it.value() }),
                "files" to JSONArray(files.filterNot { it.isImage }.map { it.value() })))
            if (stamp != lifetime) throw CancellationException()
            // Losing room access invalidates this send, even if access is later restored.
            if (sendingRooms[id] != token) return
            merge(id, listOf(result.getJSONObject("message")))
            if (drafts[id].orEmpty() == text) drafts.remove(id)
            if (attachments[id].orEmpty() == files) attachments.remove(id)
        } catch (e: CancellationException) { throw e }
        catch (e: Exception) { throw IllegalStateException("发送未确认，请先检查房间消息再决定是否重发：${e.message}", e) }
        finally { if (sendingRooms[id] == token) sendingRooms.remove(id) }
    }
    companion object { const val PAGE = 30 }
}

internal fun mergeRoomMessages(current: List<JSONObject>, incoming: List<JSONObject>): List<JSONObject> =
    (current + incoming).associateBy { it.getString("id") }.values.sortedBy { it.getLong("seq") }
