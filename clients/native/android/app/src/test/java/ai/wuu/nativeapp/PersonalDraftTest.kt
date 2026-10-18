package ai.wuu.nativeapp

import kotlinx.coroutines.test.runTest
import org.json.JSONArray
import org.junit.Assert.*
import org.junit.Test
import java.nio.file.Files

class PersonalDraftTest {
    private fun initialized() = json("user_space_id" to "local", "product_domain" to "personal", "extension_inventory" to JSONArray().put(json("id" to "plugin:bundled:personal", "fingerprint" to "one", "runtime_state" to "active")))
    @Test fun coldRecoveryKeepsExactIdentityAndBytesAndCannotCrossScope() = runTest {
        val directory = Files.createTempDirectory("personal-draft").toFile()
        try {
            val client = PersonalClient { _, p -> json("result" to json("agent" to json("agent_id" to p.getJSONObject("input").getString("agent_id"), "user_space_id" to "local"))) }
            client.configure(initialized()); client.selectAgent("main")
            val file = InputAttachment("photo.pdf", "application/pdf", "%PDF-exact".toByteArray())
            val draft = client.prepareMessage("  exact\ntext  ", listOf(file))
            val store = PersonalDraftStore(directory, listOf("account", "login", "host")); store.save(draft); store.invalidate(false)
            assertNull(PersonalDraftStore(directory, listOf("other", "login", "host")).load())
            val reopened = PersonalDraftStore(directory, listOf("account", "login", "host")); val recovered = reopened.load()!!
            assertEquals(draft.requestID, recovered.requestID); assertEquals(draft.prompt, recovered.prompt); assertArrayEquals(file.data, recovered.attachments[0].data)
            val calls = mutableListOf<org.json.JSONObject>()
            val second = PersonalClient { _, p -> calls += p; if (p.getString("method") == "agents.send") json("result" to json("state" to "accepted")) else json("result" to json("agent" to json("agent_id" to p.getJSONObject("input").getString("agent_id"), "user_space_id" to "local"))) }
            second.configure(initialized()); second.selectAgent("other")
            try { second.restoreDraft(recovered); fail("Recovered draft crossed agents") } catch (_: PersonalClientException) {}
            second.selectAgent("main"); val retry = second.restoreDraft(recovered)
            assertEquals(2, calls.size); second.send(retry)
            assertEquals(draft.requestID, calls.last().getJSONObject("input").getString("request_id")); assertEquals(draft.prompt, calls.last().getJSONObject("user_input").getString("prompt"))
            reopened.invalidate(true)
            try { reopened.save(draft); fail("Store resurrected after logout") } catch (_: IllegalStateException) {}
            assertNull(PersonalDraftStore(directory, listOf("account", "login", "host")).load())
        } finally { directory.deleteRecursively() }
    }
    @Test fun personalDraftRejectsOversizedUTF8AndControlFilenames() = runTest {
        val client = PersonalClient { _, p -> json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local"))) }
        client.configure(initialized()); client.selectAgent("main")
        for (name in listOf("文".repeat(100) + ".pdf", "bad\u0085.pdf")) {
            val file = InputAttachment(name, "application/pdf", "%PDF-data".toByteArray())
            try { client.prepareMessage("", listOf(file)); fail("Invalid filename admitted") } catch (_: IllegalArgumentException) {}
        }
    }
}
