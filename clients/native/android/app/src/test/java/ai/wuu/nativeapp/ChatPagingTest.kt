package ai.wuu.nativeapp

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test

class ChatPagingTest {
    @Test fun toolCompletionWinsOverStaleExpansionAndHistory() {
        val thread = ChatThread(JSONObject("""{"id":"t","history_cursor":"page","turns":[{"id":"turn","status":"in_progress","items":[]}]}"""))
        val started = json("id" to "tool", "type" to "tool_call", "name" to "read_file", "status" to "in_progress", "remote_content_ref" to "old")
        thread.apply("item/started", json("thread_id" to "t", "turn_id" to "turn", "item" to started))
        assertEquals("in_progress", thread.messages.single().tool!!.status)
        val completed = json("id" to "tool", "type" to "tool_call", "name" to "read_file", "status" to "failed", "error" to "File removed", "remote_content_ref" to "new")
        thread.apply("item/completed", json("thread_id" to "t", "turn_id" to "turn", "item" to completed))
        thread.expandContent("old", started)
        thread.prependHistory(JSONObject("""{"thread_id":"t","cursor":"page","turns":[{"id":"turn","items":[$started]}]}"""))
        assertEquals("failed", thread.messages.single().tool!!.status)
        assertEquals("File removed", thread.messages.single().tool!!.error)
        thread.apply("item/removed", json("thread_id" to "t", "turn_id" to "turn", "item_id" to "tool"))
        assertTrue(thread.messages.isEmpty())
    }

    @Test fun splitPagePreservesStreamAndRemovalAndRejectsStalePage() {
        val thread = ChatThread(JSONObject("""{"id":"t","status":"in_progress","history_cursor":"page","turns":[{"id":"turn","items":[{"id":"a","type":"agent_message","text":"live"}]}]}"""))
        thread.apply("item/agentMessage/delta", json("thread_id" to "t", "turn_id" to "turn", "item_id" to "a", "delta" to " update"))
        thread.apply("item/removed", json("thread_id" to "t", "turn_id" to "turn", "item_id" to "removed"))
        val page = JSONObject("""{"thread_id":"t","cursor":"page","turns":[{"id":"turn","items":[
            {"id":"u","type":"user_message","text":"earlier"},
            {"id":"removed","type":"agent_message","text":"deleted"},
            {"id":"a","type":"agent_message","text":"stale"}]}]}""")
        thread.prependHistory(page); thread.prependHistory(page)
        assertEquals(listOf("earlier", "live update"), thread.messages.map { it.text })
        assertTrue(thread.running)
        thread.apply("turn/error", JSONObject("""{"thread_id":"t","turn":{"id":"turn","items":[],"error":{"message":"Provider unavailable"}}}"""))
        assertFalse(thread.running)
        assertEquals("error", thread.messages.single().role)
        assertEquals("Provider unavailable", thread.messages.single().text)
    }
}
