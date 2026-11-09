package ai.wuu.nativeapp

import java.io.File
import org.json.JSONArray
import org.json.JSONObject

/** Bounded display-only data in the app-private cache directory. Scope hashes never grant remote access. */
class PersonalHistoryCache(directory: File, identity: List<String>) {
    private val scope = key(identity)
    private val file = File(directory, "$scope.json")
    private var valid = true
    private fun document(): JSONObject {
        check(valid) { "Cache scope expired" }
        if (!file.exists()) return json("scope" to scope, "agents" to JSONObject())
        check(file.length() <= MAX_BYTES) { "Personal cache exceeds its display budget" }
        return JSONObject(file.readText()).also { check(it.optString("scope") == scope) { "Cache scope changed" } }
    }
    @Synchronized fun load(agentID: String? = null): JSONObject? {
        val value = document(); val id = agentID ?: value.optString("selected").takeIf { it.isNotEmpty() } ?: return null
        val snapshot = value.optJSONObject("agents")?.optJSONObject(id) ?: return null
        if (snapshot.optJSONObject("agent")?.optString("agent_id") != id || snapshot.getJSONObject("agent").optString("user_space_id") != "local") return null
        return snapshot
    }
    @Synchronized fun save(agent: JSONObject, records: List<JSONObject>) {
        require(agent.optString("user_space_id") == "local")
        val id = agent.getString("agent_id"); val current = document()
        var agents = current.optJSONObject("agents") ?: JSONObject()
        fun fields(value: JSONObject, names: List<String>) = JSONObject().also { output -> names.forEach { key -> if (value.has(key)) output.put(key, value.get(key)) } }
        val shown = fields(agent, listOf("agent_id", "user_space_id", "name", "kind", "responsibility", "status"))
        val rows = records.takeLast(100).map { row -> fields(row, listOf("seq", "role", "content")).put("attachments", JSONArray(
            row.optJSONArray("attachments")?.objects()?.map { fields(it, listOf("kind", "index", "name", "mime_type", "size", "sha256")) } ?: emptyList<JSONObject>())) }
        agents.put(id, json("agent" to shown, "records" to JSONArray(rows)))
        if (agents.length() > 8) agents.keys().asSequence().sorted().firstOrNull { it != id }?.let { agents.remove(it) }
        var bytes = json("scope" to scope, "selected" to id, "agents" to agents).toString().toByteArray()
        if (bytes.size > MAX_BYTES) {
            agents = json(id to json("agent" to shown, "records" to JSONArray(rows.takeLast(10))))
            bytes = json("scope" to scope, "selected" to id, "agents" to agents).toString().toByteArray()
        }
        if (bytes.size > MAX_BYTES) return
        check(file.parentFile!!.isDirectory || file.parentFile!!.mkdirs())
        if (file.exists() && file.readBytes().contentEquals(bytes)) return
        val pending = File(file.parentFile, file.name + ".tmp")
        pending.writeBytes(bytes)
        check(pending.renameTo(file)) { "Could not store Personal history" }
    }
    @Synchronized fun invalidate(removeCache: Boolean) {
        valid = false
        if (removeCache && file.exists()) check(file.delete()) { "Could not clear Personal history" }
    }
    companion object {
        private const val MAX_BYTES = 2 * 1024 * 1024
        private fun key(identity: List<String>) = sha256(JSONArray(identity).toString().toByteArray()).joinToString("") { "%02x".format(it) }
        fun prune(directory: File, identities: List<List<String>>) {
            val keep = identities.map { key(it) + ".json" }.toSet()
            directory.listFiles()?.filter { it.name !in keep && !it.name.removeSuffix(".tmp").let(keep::contains) }?.forEach {
                check(it.delete()) { "Could not clear revoked Personal history" }
            }
        }
    }
}
