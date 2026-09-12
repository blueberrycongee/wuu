package ai.wuu.nativeapp

import android.util.AtomicFile
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import org.json.JSONArray
import org.json.JSONObject
import java.io.File
import kotlin.coroutines.coroutineContext

internal fun JSONArray.objects() = (0 until length()).map { getJSONObject(it) }

/** Cursor and bodies form one atomic snapshot, including deletion and opt-out state. */
class HistorySnapshot(private var data: JSONObject = JSONObject()) {
    val generation: String get() = data.optString("generation")
    val cursor: String get() = data.optString("cursor", "0")
    val enabled: Boolean get() = data.optBoolean("enabled")
    private val entriesObject get() = data.optJSONObject("entries") ?: JSONObject().also { data.put("entries", it) }
    private val bodies get() = data.optJSONObject("bodies") ?: JSONObject().also { data.put("bodies", it) }
    val entries: List<JSONObject> get() = entriesObject.keys().asSequence().map { entriesObject.getJSONObject(it) }.sortedByDescending { it.optString("updated_at") }.toList()
    fun cached(id: String): JSONObject? = bodies.optJSONObject(id)?.optJSONObject("thread")
    fun merge(page: JSONObject) {
        val next = page.getString("cursor").toLong(); require(next >= 0)
        if (generation != page.getString("generation") || !page.getBoolean("enabled")) data = JSONObject()
        require(next >= cursor.toLong()) { "History cursor moved backwards" }
        data.put("generation", page.getString("generation")).put("cursor", next.toString()).put("enabled", page.getBoolean("enabled"))
        if (!enabled) return
        page.getJSONArray("entries").objects().forEach { entry ->
            val id = entry.getString("id")
            if (entry.getBoolean("deleted")) { entriesObject.remove(id); bodies.remove(id) }
            else {
                entriesObject.put(id, entry)
                if (bodies.optJSONObject(id)?.optString("revision") != entry.getString("revision")) bodies.remove(id)
            }
        }
    }
    fun store(body: JSONObject, expectedGeneration: String) {
        val id = body.getJSONObject("thread").getString("id")
        require(enabled && generation == expectedGeneration && entriesObject.optJSONObject(id)?.optString("revision") == body.getString("revision")) { "History changed; refresh again" }
        bodies.put(id, body)
        val keep = entries.filter { bodies.has(it.getString("id")) }.take(20).map { it.getString("id") }.toSet()
        bodies.keys().asSequence().toList().filter { it !in keep }.forEach { bodies.remove(it) }
        while (bodies.toString().toByteArray().size > 16 * 1024 * 1024) {
            val oldest = entries.lastOrNull { bodies.has(it.getString("id")) } ?: break
            bodies.remove(oldest.getString("id"))
        }
    }
    fun encode() = data.toString()
}

/** Main-confined. Invalidation prevents a late network response from recreating logged-out data. */
class History(private val account: AccountSession, private val host: String, directory: File) {
    private val api = AccountAPI(account.server)
    private val file = AtomicFile(cacheFile(account, host, directory))
    companion object {
        private fun cacheFile(account: AccountSession, host: String, directory: File) =
            File(directory, sha256(JSONArray(listOf(account.server, account.username, account.pub, host)).toString().toByteArray()).b64() + ".json")
        /** Invalidate a revoked selected store first; cold caches are checked too. */
        fun prune(account: AccountSession, hosts: List<String>, directory: File) {
            val allowed = hosts.flatMap { val name = cacheFile(account, it, directory).name; listOf(name, "$name.bak", "$name.new") }.toSet()
            directory.listFiles()?.filter { it.name !in allowed }?.forEach { check(it.delete()) { "无法清除已撤销电脑的缓存" } }
        }
    }
    private val writes = Mutex()
    private val syncLock = Mutex()
    private var epoch = 0L
    private var active = true
    var snapshot = HistorySnapshot()
        private set
    suspend fun restore() {
        val stamp = epoch
        val cached = withContext(Dispatchers.IO) {
            runCatching {
                require(file.baseFile.length() <= 20 * 1024 * 1024)
                HistorySnapshot(JSONObject(String(file.readFully(), Charsets.UTF_8)))
            }.getOrNull()
        }
        check(stamp); if (cached != null) snapshot = cached
    }
    private suspend fun check(stamp: Long) {
        coroutineContext.ensureActive()
        if (!active || epoch != stamp) throw CancellationException()
    }
    private suspend fun persist(stamp: Long) {
        val bytes = snapshot.encode().toByteArray()
        writes.withLock {
            check(stamp)
            withContext(Dispatchers.IO) {
                file.baseFile.parentFile?.mkdirs()
                val stream = file.startWrite()
                try { stream.write(bytes); file.finishWrite(stream) } catch (e: Throwable) { file.failWrite(stream); throw e }
            }
        }
    }
    suspend fun invalidate(removeCache: Boolean = false) {
        active = false; epoch++; snapshot = HistorySnapshot()
        writes.withLock { if (removeCache) withContext(Dispatchers.IO) { file.delete() } }
    }
    suspend fun sync(): HistorySnapshot = syncLock.withLock {
        val stamp = epoch; check(stamp)
        var conflicts = 0
        while (true) {
            try {
                val page = api.request("/history", account.token, query = mapOf("host" to host, "generation" to snapshot.generation, "after" to snapshot.cursor))
                check(stamp); require(page.getString("host") == host)
                val previous = snapshot.cursor
                snapshot.merge(page); persist(stamp)
                if (!page.getBoolean("more")) break
                require(snapshot.cursor != previous) { "History pagination stalled" }
            } catch (e: HttpFailure) {
                if (e.status != 409 || conflicts++ != 0) throw e
                check(stamp); snapshot = HistorySnapshot(); persist(stamp)
            }
        }
        snapshot
    }
    suspend fun thread(id: String): JSONObject {
        val stamp = epoch; check(stamp)
        snapshot.cached(id)?.let { return it }
        val generation = snapshot.generation
        val body = api.request("/history/thread", account.token, query = mapOf("host" to host, "generation" to generation, "id" to id))
        check(stamp); require(body.getJSONObject("thread").getString("id") == id)
        snapshot.store(body, generation); persist(stamp)
        return body.getJSONObject("thread")
    }
    suspend fun setEnabled(enabled: Boolean) = syncLock.withLock {
        val stamp = epoch; check(stamp)
        api.request("/history/settings", account.token, json("host" to host, "enabled" to enabled))
        check(stamp); epoch++; snapshot = HistorySnapshot(); persist(epoch)
    }
}
