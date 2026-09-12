package ai.wuu.nativeapp

import org.json.JSONArray
import org.junit.Assert.*
import org.junit.Test

class CoreTest {
    @Test fun goFramesAuthenticateAndRejectReplayWithoutPoisoningCounter() {
        val shared = "7m04smrnw6Ze4LRwsN4zMQXTX47eCxLNlBh_xQb3KDo".unb64()
        val transcript = "qE1-R4qo148jV39azZgImQcYgU6oCIMl93ZGQtwip8c".unb64()
        val phone = SecureChannel(shared, transcript)
        val host = SecureChannel(shared, transcript, false)
        val plain = "eyJpZCI6IjEiLCJtZXRob2QiOiJpbml0aWFsaXplIiwicGFyYW1zIjp7fX0K".unb64()
        val frame = phone.seal(plain)
        assertEquals("AAAAAAAAAAAAAAABNPgt9aU7lsVM27S0hKYHSGglgVNHg5pTDRukVqL5hcxpwj20i7G_aSR16KuWSrh4Be1ENDnK5BTTR8Ot7A", frame.b64())
        val corrupt = frame.copyOf().also { it[it.lastIndex] = (it.last().toInt() xor 1).toByte() }
        assertThrows(Exception::class.java) { host.open(corrupt) }
        assertArrayEquals(plain, host.open(frame))
        assertThrows(IllegalArgumentException::class.java) { host.open(frame) }
        assertEquals("AAAAAAAAAAAAAAACMFFBfnrSyNAuNXtJts4oxw", phone.seal(byteArrayOf()).b64())
    }
    @Test fun enrollmentMatchesGoIdentityAndRelayProof() {
        val identity = Identity("4Yxwscu9AXnFoXJkd-VQ3-CESwiDldRP4YXkla7kOXI".unb64())
        assertEquals("-yy5BmoxzRhqVEmIv1bynQUcrRHPo66aRq6vUXfZ6vo", identity.pub.b64())
        assertEquals("D3zcAOFooDUQJEEi3dPZ8bLqw3khpjNy-KaXxsQ8MfUrs866EH3-syKe-GxQYrAGecHiseM39heS64y07hRUAw", identity.proof("7SlatUzvq_mKJiLsN8a0IZztjWIvwg7AgjhRx12kRgY".unb64()))
    }
    @Test fun originCannotLeakCredentialsOrDowngradeTLS() {
        listOf("http://example.com", "https://user:password@example.com", "https://example.com/path", "https://example.com?token=x", "https://example.com#x").forEach {
            assertThrows(it, IllegalArgumentException::class.java) { AccountAPI(it) }
        }
        assertEquals("https://example.com/", AccountAPI("https://example.com").origin.toString())
    }
    private fun entry(revision: String = "1", deleted: Boolean = false) = json("id" to "thread-a", "title" to "hello", "updated_at" to "2026-09-11", "revision" to revision, "deleted" to deleted)
    private fun page(generation: String = "g1", cursor: String = "1", enabled: Boolean = true, deleted: Boolean = false) = json("generation" to generation, "cursor" to cursor, "enabled" to enabled, "entries" to JSONArray().put(entry(cursor, deleted)))
    private fun body(revision: String = "1") = json("revision" to revision, "thread" to json("id" to "thread-a", "messages" to JSONArray().put(json("id" to "m1", "turn_id" to "turn", "role" to "assistant", "text" to "private text"))))
    @Test fun tombstoneRejectsLateBodyAndPersistedCacheDoesNotResurrectText() {
        val snapshot = HistorySnapshot(); snapshot.merge(page()); snapshot.store(body(), "g1")
        assertNotNull(snapshot.cached("thread-a"))
        snapshot.merge(page(cursor = "2", deleted = true))
        assertThrows(IllegalArgumentException::class.java) { snapshot.store(body(), "g1") }
        val restored = HistorySnapshot(org.json.JSONObject(snapshot.encode()))
        assertNull(restored.cached("thread-a")); assertTrue(restored.entries.isEmpty())
    }
    @Test fun optOutAndGenerationChangeWipeBodiesAndFenceStaleDownloads() {
        val snapshot = HistorySnapshot(); snapshot.merge(page()); snapshot.store(body(), "g1")
        snapshot.merge(page(generation = "g2"))
        assertNull(snapshot.cached("thread-a"))
        assertThrows(IllegalArgumentException::class.java) { snapshot.store(body(), "g1") }
        snapshot.store(body(), "g2"); snapshot.merge(page(generation = "g2", enabled = false))
        assertNull(snapshot.cached("thread-a")); assertTrue(snapshot.entries.isEmpty())
    }
    @Test fun revisionChangeInvalidatesBodyAndCursorCannotGoBackwards() {
        val snapshot = HistorySnapshot(); snapshot.merge(page()); snapshot.store(body(), "g1")
        snapshot.merge(page(cursor = "2"))
        assertNull(snapshot.cached("thread-a"))
        assertThrows(IllegalArgumentException::class.java) { snapshot.store(body(), "g1") }
        assertThrows(IllegalArgumentException::class.java) { snapshot.merge(page()) }
    }
}
