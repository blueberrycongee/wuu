package ai.wuu.nativeapp

import java.io.IOException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.test.runTest
import org.json.JSONArray
import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class PersonalClientTest {
    private fun initialized() = json("user_space_id" to "local", "product_domain" to "personal", "extension_inventory" to JSONArray().put(json(
        "id" to "plugin:bundled:personal", "fingerprint" to "generation-one", "runtime_state" to "active")))

    @Test fun discoveryAndSelectionNeverFallBackToWorkspaceOrThread() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { method, params ->
            assertEquals("plugin/client/request", method); calls += params
            json("result" to json("agent" to json("agent_id" to params.getJSONObject("input").getString("agent_id"), "user_space_id" to "local")))
        }
        try { client.listAgents(); fail("Undiscovered plugin admitted a request") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.UNAVAILABLE, error.reason) }
        client.configure(initialized()); client.selectAgent("travel")
        assertEquals(1, calls.size)
        assertEquals("generation-one", calls.single().getString("fingerprint"))
        assertEquals("local", calls.single().getString("user_space_id"))
        assertEquals("personal", calls.single().getString("product_domain"))
        assertEquals("travel", calls.single().getJSONObject("input").getString("agent_id"))
        assertFalse(calls.single().has("workspace_id")); assertFalse(calls.single().has("session_id"))
        client.invalidate()
        try { client.history(); fail("Invalidated selection survived") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.NO_AGENT_SELECTED, error.reason) }
    }

    @Test fun failedSendRetainsDraftIdentityWithoutAutomaticReplay() = runTest {
        val calls = mutableListOf<JSONObject>(); var offline = false
        val client = PersonalClient { _, params ->
            calls += params
            if (offline) throw IOException("Connection lost")
            if (params.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to params.getJSONObject("input").getString("agent_id"), "user_space_id" to "local")))
            else json("result" to json("state" to "accepted"))
        }
        client.configure(initialized()); client.selectAgent("main")
        val draft = client.prepareMessage("Remember this")
        offline = true
        try { client.send(draft); fail("Offline send was accepted") } catch (_: IOException) {}
        assertEquals(2, calls.size)
        offline = false; client.send(draft)
        assertEquals(calls[1].getJSONObject("input").getString("request_id"), calls[2].getJSONObject("input").getString("request_id"))
        assertNotEquals(calls[1].getString("request_id"), calls[2].getString("request_id"))
        assertEquals(calls[1].getJSONObject("input").getString("request_id"), calls[1].getJSONObject("user_input").getString("request_id"))
        assertEquals("Remember this", calls[1].getJSONObject("user_input").getString("prompt"))
        assertEquals(calls[1].getJSONObject("user_input").toString(), calls[2].getJSONObject("user_input").toString())
        assertFalse(calls[0].has("user_input"))
        client.selectAgent("other")
        try { client.send(draft); fail("Draft crossed agents") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.SELECTION_CHANGED, error.reason) }
    }

    @Test fun isolationFailureHasSpecificReasonAndNoFallback() = runTest {
        var calls = 0
        val client = PersonalClient { _, _ -> calls++; throw IOException("user_host_isolation_required: configure a verified host") }
        client.configure(initialized())
        try { client.listAgents(); fail("Isolation gate accepted") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.ISOLATION_REQUIRED, error.reason) }
        assertEquals(1, calls)
    }

    @Test fun recoveryKeepsBusinessIdentityAndRejectsForeignTasks() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, params ->
            calls += params
            if (params.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local")))
            else json("result" to json("state" to "accepted"))
        }
        client.configure(initialized()); client.selectAgent("main")
        val draft = client.prepareMessage("Keep this request")
        client.invalidate(); client.configure(initialized()); client.selectAgent("main"); client.send(draft)
        assertEquals(listOf("agents.open", "agents.open", "agents.send"), calls.map { it.getString("method") })
        assertEquals(draft.requestID, calls.last().getJSONObject("input").getString("request_id"))
        try { client.task("foreign"); fail("Invalid task ownership was accepted") }
        catch (error: PersonalClientException) { assertEquals(PersonalClientFailure.INVALID_RESPONSE, error.reason) }
    }

    @Test fun attachmentOnlySendCarriesTrustedBytesAndStableReferences() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, params ->
            calls += params
            if (params.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local")))
            else json("result" to json("state" to "accepted"))
        }
        client.configure(initialized()); client.selectAgent("main")
        try { client.prepareMessage("a".repeat(64_001)); fail("Oversized Personal proof accepted") } catch (_: IllegalArgumentException) {}
        val file = InputAttachment("note.pdf", "application/pdf", "%PDF-test".toByteArray())
        val draft = client.prepareMessage("", listOf(file))
        client.send(draft); client.send(draft)
        val proof = calls[1].getJSONObject("user_input").getJSONArray("attachments").getJSONObject(0)
        assertEquals(java.util.Base64.getEncoder().encodeToString(file.data), proof.getString("data"))
        assertEquals(sha256(file.data).joinToString("") { "%02x".format(it) }, proof.getString("sha256"))
        assertFalse(calls[1].getJSONObject("input").getJSONArray("attachments").getJSONObject(0).has("data"))
        assertEquals(calls[1].getJSONObject("input").toString(), calls[2].getJSONObject("input").toString())
        assertEquals(calls[1].getJSONObject("user_input").toString(), calls[2].getJSONObject("user_input").toString())
    }

    @Test fun attachmentReadRejectsCorruptDigestAndNeverUsesGlobalPaths() = runTest {
        val client = PersonalClient { _, params ->
            if (params.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local")))
            else {
                assertEquals("agents.attachments.read", params.getString("method"))
                assertEquals("main", params.getJSONObject("input").getString("agent_id"))
                assertFalse(params.getJSONObject("input").has("session_id")); assertFalse(params.getJSONObject("input").has("path"))
                json("result" to json("data" to java.util.Base64.getEncoder().encodeToString("%PDF-corrupt".toByteArray()), "offset" to 0, "total" to 16, "content_type" to "application/pdf"))
            }
        }
        client.configure(initialized()); client.selectAgent("main")
        try {
            client.attachment(1, json("kind" to "file", "index" to 0, "sha256" to "0".repeat(64), "mime_type" to "application/pdf", "name" to "note.pdf"))
            fail("Corrupt attachment accepted")
        } catch (_: Exception) {}
    }

    @Test fun inventoryRefreshUsesOnlyPreviouslyVerifiedLiveScope() = runTest {
        val calls = mutableListOf<JSONObject>()
        val client = PersonalClient { _, params -> calls += params; json("result" to json("agent" to json("agent_id" to "main", "user_space_id" to "local"))) }
        val inventory = json("extension_inventory" to JSONArray().put(json("id" to "plugin:bundled:personal", "fingerprint" to "generation-two", "runtime_state" to "active")))
        try { client.configure(inventory); fail("Missing immutable scope accepted") } catch (_: PersonalClientException) {}
        client.configure(initialized()); client.refreshInventory(inventory); client.selectAgent("main")
        assertEquals("generation-two", calls.last().getString("fingerprint"))
        client.invalidate()
        try { client.refreshInventory(inventory); fail("Disconnected authority restored") } catch (_: PersonalClientException) {}
    }

    @Test fun heldAttachmentSurvivesSameAgentRefreshButNotAgentSwitch() = runTest {
        for (changeAgent in listOf(false, true)) {
            val started = CompletableDeferred<Unit>(); val release = CompletableDeferred<Unit>()
            val bytes = "%PDF-test".toByteArray(); val encoded = java.util.Base64.getEncoder().encodeToString(bytes)
            val client = PersonalClient { _, params ->
                if (params.getString("method") == "agents.open") json("result" to json("agent" to json("agent_id" to params.getJSONObject("input").getString("agent_id"), "user_space_id" to "local")))
                else { started.complete(Unit); release.await(); json("result" to json("data" to encoded, "offset" to 0, "total" to encoded.length, "content_type" to "application/pdf")) }
            }
            client.configure(initialized()); client.selectAgent("main")
            val download = async { runCatching { client.attachment(1, json("kind" to "file", "index" to 0, "sha256" to sha256(bytes).joinToString("") { "%02x".format(it) }, "mime_type" to "application/pdf", "name" to "note.pdf")) } }
            started.await(); client.selectAgent(if (changeAgent) "other" else "main"); release.complete(Unit)
            if (changeAgent) assertEquals(PersonalClientFailure.SELECTION_CHANGED, (download.await().exceptionOrNull() as PersonalClientException).reason)
            else assertArrayEquals(bytes, download.await().getOrThrow().data)
        }
    }

    @Test fun taskArtifactProjectionKeepsAttachmentWithoutToolInternals() {
        val rows = personalDisplayRecords(listOf(json("seq" to 1, "role" to "user", "content" to "Draw this"),
            json("seq" to 2, "role" to "tool", "content" to "private tool payload", "tool_result" to "private result", "attachments" to JSONArray().put(json("kind" to "artifact", "index" to 0, "name" to "drawing.png"))),
            json("seq" to 3, "role" to "system", "content" to "private instructions")))
        assertEquals(2, rows.size); assertEquals("", rows[1].getString("content"))
        assertEquals("drawing.png", rows[1].getJSONArray("attachments").getJSONObject(0).getString("name"))
        assertFalse(rows[1].has("tool_result"))
    }

    @Test fun reconnectFencesLateResultsEvenWithSameFingerprint() = runTest {
        val started = CompletableDeferred<Unit>(); val release = CompletableDeferred<Unit>()
        val client = PersonalClient { _, _ -> started.complete(Unit); release.await(); json("result" to json("agents" to JSONArray())) }
        client.configure(initialized())
        val pending = async { runCatching { client.listAgents() } }
        started.await(); client.configure(initialized()); release.complete(Unit)
        assertEquals(PersonalClientFailure.SELECTION_CHANGED, (pending.await().exceptionOrNull() as PersonalClientException).reason)
    }
}
