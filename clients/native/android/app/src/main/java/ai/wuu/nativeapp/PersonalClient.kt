package ai.wuu.nativeapp

import java.io.IOException
import java.util.UUID
import kotlin.coroutines.coroutineContext
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.ensureActive
import org.json.JSONObject

enum class PersonalTaskAction(val method: String) { UPDATE("update"), STOP("stop"), RESUME("resume") }
/** Consent belongs to the displayed connection/selection, unlike a recoverable message draft. */
class PersonalCalendarConfirmation internal constructor(internal val snapshot: String, internal val owner: UUID, internal val agentID: String, internal val epoch: Long) { val preview: JSONObject get() = JSONObject(snapshot) }

enum class PersonalClientFailure { UNAVAILABLE, ISOLATION_REQUIRED, NO_AGENT_SELECTED, SELECTION_CHANGED, INVALID_RESPONSE, EMPTY_MESSAGE }
class PersonalClientException(val reason: PersonalClientFailure) : IOException(when (reason) {
    PersonalClientFailure.UNAVAILABLE -> "Personal is unavailable on this host. Connect to a host with Personal user-space routing enabled."
    PersonalClientFailure.ISOLATION_REQUIRED -> "Personal requires a verified separate host identity before activation. Projects remain available."
    PersonalClientFailure.NO_AGENT_SELECTED -> "Choose a Personal assistant first."
    PersonalClientFailure.SELECTION_CHANGED -> "The Personal connection or assistant changed. Reload before continuing."
    PersonalClientFailure.INVALID_RESPONSE -> "The host returned an invalid Personal response."
    PersonalClientFailure.EMPTY_MESSAGE -> "Write a message first."
})

/** Keep the same draft for an explicit retry; creating it does not acknowledge execution. */
class PersonalMessageDraft internal constructor(val agentID: String, val requestID: String, val prompt: String, val attachments: List<InputAttachment>, internal val owner: UUID)

/** One instance per authenticated account and host; discard on identity change. No new grants or connections. */
class PersonalClient(private var transport: suspend (String, JSONObject) -> JSONObject) {
    constructor(remote: Remote) : this({ method, params -> remote.call(method, params) })
    private data class Binding(val id: String, val fingerprint: String, val graph: String)
    private val owner = UUID.randomUUID()
    private var epoch = 0L
    private var verifiedScope: Pair<String, String>? = null
    private var binding: Binding? = null
    private var calendarBinding: Binding? = null
    private var selected: String? = null
    val selectedAgentID: String? get() = synchronized(this) { selected }

    /** Rebind only within the same authenticated account and host; identity changes require a new adapter. */
    @Synchronized fun reconnect(remote: Remote, initialized: JSONObject) {
        transport = { method, params -> remote.call(method, params) }
        configure(initialized)
    }

    /** Capture the admitted graph separately from the code fingerprint after each reconnect or reload. */
    @Synchronized fun configure(initialized: JSONObject) {
        invalidate()
        if (initialized.optString("user_space_id") != "local" || initialized.optString("product_domain") != "personal") throw PersonalClientException(PersonalClientFailure.UNAVAILABLE)
        verifiedScope = initialized.getString("user_space_id") to initialized.getString("product_domain")
        val inventory = initialized.optJSONArray("extension_inventory")
        val candidates = (0 until (inventory?.length() ?: 0)).mapNotNull { inventory?.optJSONObject(it) }.filter {
            it.optString("id") == "plugin:bundled:personal" || it.optJSONObject("provenance")?.optString("plugin_id") == "personal"
        }
        val record = candidates.firstOrNull { it.optString("id") == "plugin:bundled:personal" } ?: candidates.singleOrNull()
        val graph = record?.opt("graph_generation") as? String
        if (record == null || record.optString("runtime_state") != "active" || record.opt("enabled") == false ||
            record.optString("id").isBlank() || record.optString("fingerprint").isBlank() || graph.isNullOrBlank()) throw PersonalClientException(PersonalClientFailure.UNAVAILABLE)
        binding = Binding(record.getString("id"), record.getString("fingerprint"), graph)
        val calendars = (0 until (inventory?.length() ?: 0)).mapNotNull { inventory?.optJSONObject(it) }.filter { it.optString("id") == "plugin:bundled:personal-calendar" || it.optJSONObject("provenance")?.optString("plugin_id") == "personal-calendar" }
        val calendar = calendars.firstOrNull { it.optString("id") == "plugin:bundled:personal-calendar" } ?: calendars.singleOrNull()
        val calendarGraph = calendar?.opt("graph_generation") as? String
        if (calendar != null && calendar.optString("runtime_state") == "active" && calendar.opt("enabled") != false && calendar.optString("id").isNotEmpty() && calendar.optString("fingerprint").isNotEmpty() && !calendarGraph.isNullOrBlank()) calendarBinding = Binding(calendar.getString("id"), calendar.getString("fingerprint"), calendarGraph)
    }

    /** Inventory events cannot recreate scope after disconnect; only verified live initialization supplies it. */
    @Synchronized fun refreshInventory(event: JSONObject) {
        val scope = verifiedScope ?: throw PersonalClientException(PersonalClientFailure.UNAVAILABLE)
        configure(json("user_space_id" to scope.first, "product_domain" to scope.second, "extension_inventory" to event.optJSONArray("extension_inventory")))
    }

    /** Call immediately on disconnect, sign-out, revocation, or plugin-generation invalidation. */
    @Synchronized fun invalidate() { epoch++; binding = null; calendarBinding = null; selected = null; verifiedScope = null }

    suspend fun listAgents(): JSONObject = request("agents.list")
    suspend fun ensureDefaultAgent(name: String? = null): JSONObject = request("agents.ensure_default", if (name == null) JSONObject() else json("name" to name))

    /** Restore the named assistant; never choose or create an execution thread. */
    suspend fun selectAgent(agentID: String): JSONObject {
        val stamp = synchronized(this) { if (selected != agentID) { epoch++; selected = null }; epoch }
        if (agentID.isBlank()) throw PersonalClientException(PersonalClientFailure.NO_AGENT_SELECTED)
        val result = request("agents.open", json("agent_id" to agentID), stamp)
        synchronized(this) {
            if (epoch != stamp) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED)
            val agent = result.optJSONObject("agent")
            if (agent == null || agent.optString("agent_id") != agentID || agent.optString("user_space_id") != "local") throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
            selected = agentID
        }
        return result
    }

    suspend fun history(endSequence: Long? = null): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val input = json("agent_id" to agentID)
        if (endSequence != null) input.put("end_seq", endSequence)
        return request("agents.history", input, stamp)
    }

    suspend fun listTasks(): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val result = request("tasks.list", json("agent_id" to agentID), stamp)
        val tasks = result.optJSONArray("tasks") ?: throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        if (tasks.objects().any { it.optString("agent_id") != agentID || it.optString("user_space_id") != "local" }) throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        return result
    }
    suspend fun task(taskID: String): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val result = request("tasks.get", json("task_id" to taskID), stamp)
        val task = result.optJSONObject("task") ?: throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        if (task.optString("task_id") != taskID || task.optString("agent_id") != agentID || task.optString("user_space_id") != "local") throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        return result
    }
    suspend fun taskHistory(taskID: String, requirementsRevision: Long? = null): JSONObject {
        val (_, stamp) = selectedSnapshot()
        task(taskID)
        val input = json("task_id" to taskID)
        if (requirementsRevision != null) input.put("requirements_revision", requirementsRevision)
        return request("tasks.history", input, stamp)
    }

    suspend fun changeTask(taskID: String, expectedRevision: Long, action: PersonalTaskAction, goal: String? = null, title: String? = null): JSONObject {
        val (agentID, stamp) = selectedSnapshot(); task(taskID)
        val input = json("task_id" to taskID, "expected_revision" to expectedRevision)
        if (goal != null) input.put("goal", goal); if (title != null) input.put("title", title)
        val result = request("tasks.${action.method}", input, stamp)
        val task = result.optJSONObject("task") ?: throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        if (task.optString("task_id") != taskID || task.optString("agent_id") != agentID || task.optString("user_space_id") != "local") throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        return result
    }
    suspend fun searchHistory(query: String, timeMin: String? = null, timeMax: String? = null, cursor: String? = null): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val input = json("agent_id" to agentID, "query" to query, "limit" to 25)
        timeMin?.let { input.put("time_min", it) }; timeMax?.let { input.put("time_max", it) }; cursor?.let { input.put("cursor", it) }
        return request("agents.search", input, stamp)
    }
    suspend fun memory(query: String = ""): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        return request("memory.query", json("agent_id" to agentID, "query" to query, "include_inactive" to true, "limit" to 100), stamp)
    }
    suspend fun correctMemory(id: String, expectedRevision: Long, content: String, requestID: String): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        return request("memory.correct", json("agent_id" to agentID, "id" to id, "expected_revision" to expectedRevision, "content" to content, "request_id" to requestID), stamp)
    }
    suspend fun forgetMemory(id: String, expectedRevision: Long): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        return request("memory.forget", json("agent_id" to agentID, "id" to id, "expected_revision" to expectedRevision), stamp)
    }
    suspend fun updateAgent(expectedRevision: Long, name: String, communication: String, responsibility: String? = null): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val input = json("agent_id" to agentID, "expected_revision" to expectedRevision, "name" to name, "communication" to communication)
        if (responsibility != null) input.put("responsibility", responsibility)
        return request("agents.update", input, stamp)
    }
    suspend fun setAgentPaused(paused: Boolean, expectedRevision: Long): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        return request(if (paused) "agents.pause" else "agents.resume", json("agent_id" to agentID, "expected_revision" to expectedRevision), stamp)
    }
    suspend fun specialistProposals(): JSONObject = request("agents.proposals")
    suspend fun decideSpecialist(id: String, approved: Boolean): JSONObject = request("agents.confirm_specialist", json("proposal_id" to id, "approved" to approved))
    suspend fun settings(): JSONObject = request("settings.get")
    suspend fun updateSettings(expectedRevision: Long, notifications: Boolean, quietHours: JSONObject, notificationBatchSeconds: Int = 0): JSONObject = request("settings.update", json("expected_revision" to expectedRevision, "notifications" to notifications, "quiet_hours" to quietHours, "notification_batch_seconds" to notificationBatchSeconds))
    suspend fun routines(): JSONObject = request("routines.list")
    suspend fun calendarAgenda(calendarID: String, start: String, end: String, timezone: String, refresh: Boolean = false): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        return request(if (refresh) "calendar.refresh" else "calendar.agenda", json("agent_id" to agentID, "calendar_id" to calendarID, "time_min" to start, "time_max" to end, "timezone" to timezone), stamp)
    }
    suspend fun calendarOperations(): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val result = request("operations.list", expectedEpoch = stamp, calendar = true)
        val rows = result.optJSONArray("operations")?.objects() ?: throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        if (rows.any { it.optString("user_space_id") != "local" }) throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        return json("operations" to org.json.JSONArray(rows.filter { it.optString("agent_id") == agentID }))
    }
    suspend fun calendarOperation(id: String, reconcile: Boolean = false): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        val result = request("operations.get", json("operation_id" to id), stamp, calendar = true)
        if (result.optString("operation_id") != id || result.optString("agent_id") != agentID || result.optString("user_space_id") != "local") throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        return if (reconcile) request("operations.reconcile", json("operation_id" to id), stamp, calendar = true) else result
    }
    @Synchronized fun prepareCalendarConfirmation(preview: JSONObject): PersonalCalendarConfirmation {
        val agentID = requireSelected(); val fields = preview.optJSONObject("fields"); val availability = preview.optJSONObject("availability")
        if (preview.optString("agent_id") != agentID || preview.optString("user_space_id") != "local" || preview.optString("status") != "proposed" || preview.optLong("revision") < 1 || !preview.optString("preview_digest").matches(Regex("[a-f0-9]{64}")) || preview.optString("kind") !in listOf("create", "reschedule") || listOf("operation_id", "task_id", "account_id", "calendar_id", "notification_notice").any { preview.optString(it).isBlank() } || fields == null || !validCalendarFields(fields) || preview.optLong("requirements_revision") < 1 || fields.optString("send_updates") !in listOf("all", "externalOnly", "none") || fields.optJSONArray("attendees") == null || availability?.optString("checked_at").isNullOrEmpty() || availability?.optJSONArray("conflicting_event_ids") == null || (preview.optString("kind") == "reschedule" && !validCalendarFields(preview.optJSONObject("original_event")?.optJSONObject("fields")))) throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        return PersonalCalendarConfirmation(preview.toString(), owner, agentID, epoch)
    }
    private fun validCalendarFields(fields: JSONObject?): Boolean {
        if (fields == null || fields.optString("summary").isBlank() || !fields.has("location")) return false
        val attendees = fields.optJSONArray("attendees") ?: return false
        if ((0 until attendees.length()).any { index -> val row = attendees.optJSONObject(index); row == null || !row.optString("email").contains('@') || row.opt("optional") !is Boolean }) return false
        return listOf("start", "end").all { field ->
            val time = fields.optJSONObject(field)
            time != null && time.optString("timezone").isNotEmpty() &&
                ((time.has("date") && !time.has("date_time") && time.optString("date").matches(Regex("\\d{4}-\\d{2}-\\d{2}"))) ||
                    (!time.has("date") && time.optString("date_time").matches(Regex("\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(?:\\.\\d+)?(?:Z|[+-]\\d{2}:\\d{2})"))))
        }
    }
    suspend fun confirmCalendar(confirmation: PersonalCalendarConfirmation): JSONObject {
        val stamp = synchronized(this) { if (confirmation.owner != owner || confirmation.agentID != selected || confirmation.epoch != epoch) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED); epoch }
        val preview = confirmation.preview
        return request("operations.confirm", json("operation_id" to preview.getString("operation_id"), "expected_revision" to preview.getLong("revision"), "preview_digest" to preview.getString("preview_digest")), stamp, calendar = true)
    }

    @Synchronized fun prepareMessage(prompt: String, attachments: List<InputAttachment> = emptyList()): PersonalMessageDraft {
        val agentID = requireSelected()
        PersonalStoredDraft.validate(prompt, attachments)
        return PersonalMessageDraft(agentID, UUID.randomUUID().toString(), prompt, attachments.map { InputAttachment(it.filename, it.mediaType, it.data.copyOf()) }, owner)
    }

    @Synchronized fun restoreDraft(stored: PersonalStoredDraft): PersonalMessageDraft {
        if (stored.agentID != selected) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED)
        PersonalStoredDraft.validate(stored.prompt, stored.attachments)
        return PersonalMessageDraft(stored.agentID, stored.requestID, stored.prompt, stored.attachments.map { InputAttachment(it.filename, it.mediaType, it.data.copyOf()) }, owner)
    }

    suspend fun send(draft: PersonalMessageDraft): JSONObject {
        val stamp = synchronized(this) {
            if (draft.owner != owner || selected != draft.agentID) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED)
            epoch
        }
        val input = json("agent_id" to draft.agentID, "request_id" to draft.requestID, "prompt" to draft.prompt)
        val proof = json("request_id" to draft.requestID, "prompt" to draft.prompt)
        if (draft.attachments.isNotEmpty()) {
            val refs = draft.attachments.map { file -> json("sha256" to sha256(file.data).joinToString("") { "%02x".format(it) }, "filename" to file.filename, "media_type" to file.mediaType) }
            input.put("attachments", org.json.JSONArray(refs))
            proof.put("attachments", org.json.JSONArray(refs.zip(draft.attachments).map { (ref, file) -> JSONObject(ref.toString()).put("data", java.util.Base64.getEncoder().encodeToString(file.data)) }))
        }
        return request("agents.send", input, stamp, proof)
    }

    suspend fun attachment(sequence: Long, descriptor: JSONObject, taskID: String? = null, requirementsRevision: Long? = null): LoadedAttachment {
        val (agentID, stamp) = selectedSnapshot()
        if (taskID != null) task(taskID)
        val digest = descriptor.optString("sha256"); val media = descriptor.optString("mime_type")
        if (sequence <= 0 || !digest.matches(Regex("[a-fA-F0-9]{64}")) || descriptor.optString("kind") !in listOf("image", "file", "artifact") || descriptor.optInt("index", -1) < 0) throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        require(descriptor.optLong("size") <= 12 * 1024 * 1024) { "手机支持 12 MB 以内的附件，请在宿主查看此文件" }
        require(media in listOf("image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf")) { "手机暂不支持此附件格式" }
        val input = json("seq" to sequence, "kind" to descriptor.getString("kind"), "index" to descriptor.getInt("index"), "sha256" to digest)
        if (taskID != null) { input.put("task_id", taskID); input.put("requirements_revision", requireNotNull(requirementsRevision)) }
        else input.put("agent_id", agentID)
        val encoded = readEncoded({ method, params -> request(method, params, stamp) }, if (taskID == null) "agents.attachments.read" else "tasks.attachments.read", input, media, limit = 16 * 1024 * 1024)
        val data = java.util.Base64.getDecoder().decode(encoded)
        if (sha256(data).joinToString("") { "%02x".format(it) } != digest.lowercase()) throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        synchronized(this) { if (stamp != epoch) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED) }
        return decodeInlineAttachment(json("media_type" to media, "filename" to descriptor.optString("name")), encoded)
    }

    suspend fun stopReply(): JSONObject {
        val (agentID, stamp) = selectedSnapshot()
        return request("agents.stop_reply", json("agent_id" to agentID), stamp)
    }
    @Synchronized private fun requireSelected(): String = selected ?: throw PersonalClientException(PersonalClientFailure.NO_AGENT_SELECTED)
    @Synchronized private fun selectedSnapshot(): Pair<String, Long> = requireSelected() to epoch

    private suspend fun request(method: String, input: JSONObject = JSONObject(), expectedEpoch: Long? = null, userInput: JSONObject? = null, calendar: Boolean = false): JSONObject {
        coroutineContext.ensureActive()
        val (target, stamp, call) = synchronized(this) {
            if (expectedEpoch != null && expectedEpoch != epoch) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED)
            Triple((if (calendar) calendarBinding else binding) ?: throw PersonalClientException(PersonalClientFailure.UNAVAILABLE), epoch, transport)
        }
        try {
            val params = json("id" to target.id, "fingerprint" to target.fingerprint, "graph_generation" to target.graph,
                "method" to method, "input" to input, "request_id" to UUID.randomUUID().toString(),
                "user_space_id" to "local", "product_domain" to "personal")
            if (userInput != null) params.put("user_input", userInput)
            val envelope = call("plugin/client/request", params)
            coroutineContext.ensureActive()
            synchronized(this) { if (stamp != epoch) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED) }
            return envelope.optJSONObject("result") ?: throw PersonalClientException(PersonalClientFailure.INVALID_RESPONSE)
        } catch (error: CancellationException) {
            throw error
        } catch (error: Exception) {
            coroutineContext.ensureActive()
            synchronized(this) { if (stamp != epoch) throw PersonalClientException(PersonalClientFailure.SELECTION_CHANGED) }
            val message = error.message.orEmpty()
            if (message.contains("user_host_isolation_required:")) throw PersonalClientException(PersonalClientFailure.ISOLATION_REQUIRED)
            if (message.contains("request belongs to another user space or product domain")) throw PersonalClientException(PersonalClientFailure.UNAVAILABLE)
            throw error
        }
    }
}

/** Display artifacts without presenting tool or system internals as chat text. */
fun personalDisplayRecords(records: List<JSONObject>): List<JSONObject> = records.mapNotNull { record ->
    val conversational = record.optString("role") in listOf("user", "assistant")
    if (!conversational && (record.optJSONArray("attachments")?.length() ?: 0) == 0) null
    else json("seq" to record.getLong("seq"), "role" to if (conversational) record.getString("role") else "assistant",
        "content" to if (conversational) record.optString("display_content").ifEmpty { record.optString("content") } else "",
        "attachments" to (record.optJSONArray("attachments") ?: org.json.JSONArray()))
}

/** A local calendar day may contain 23 or 25 hours. Provider queries require explicit RFC3339 seconds. */
fun personalCalendarDayWindow(day: java.time.LocalDate, timezone: java.time.ZoneId): Pair<String, String> {
    val start = day.atStartOfDay(timezone).toInstant()
    val end = day.plusDays(1).atStartOfDay(timezone).toInstant()
    return java.time.format.DateTimeFormatter.ISO_INSTANT.format(start) to java.time.format.DateTimeFormatter.ISO_INSTANT.format(end)
}
