package ai.wuu.nativeapp

import java.nio.file.Files
import org.junit.Assert.*
import org.junit.Test

class PersonalCacheTest {
    @Test fun cacheIsLoginHostAgentScopedAndCannotResurrectAfterInvalidation() {
        val directory = Files.createTempDirectory("personal-cache").toFile()
        try {
            val identity = listOf("server", "alice", "login-one", "host-one")
            val cache = PersonalHistoryCache(directory, identity)
            val agent = json("agent_id" to "main", "user_space_id" to "local", "name" to "Wuu", "kind" to "main", "status" to "active")
            cache.save(agent, listOf(json("seq" to 1, "role" to "user", "content" to "Private history")))
            assertEquals("Private history", cache.load()!!.getJSONArray("records").getJSONObject(0).getString("content"))
            assertNull(PersonalHistoryCache(directory, listOf("server", "alice", "login-two", "host-one")).load())
            assertNull(cache.load("other"))
            cache.invalidate(true)
            try { cache.save(agent, emptyList()); fail("Stale writer restored revoked cache") } catch (_: IllegalStateException) {}
            assertNull(PersonalHistoryCache(directory, identity).load())
        } finally { directory.deleteRecursively() }
    }
}
