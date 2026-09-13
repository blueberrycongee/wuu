package ai.wuu.nativeapp

import org.junit.Assert.*
import org.junit.Test

class ConversationTimelineTest {
    private fun tool(id: String, turn: String = "turn", status: String = "completed") =
        ChatMessage(id, "tool", "", tool = ToolActivity("read_file", status, "{}", "result", ""), turnId = turn)

    @Test fun toolBurstOccupiesOneEntryWithoutReorderingRepliesOrTurns() {
        val tools = (1..40).map { tool("call-$it") }
        val reply = ChatMessage("reply", "assistant", "Here is the result.", turnId = "turn")
        val entries = conversationTimeline(tools + reply + tool("later") + tool("next", "next-turn"))
        assertEquals(4, entries.size)
        assertEquals(tools, (entries[0] as ConversationEntry.Tools).messages)
        assertEquals(reply, (entries[1] as ConversationEntry.Message).message)
        assertEquals(listOf("later"), (entries[2] as ConversationEntry.Tools).messages.map { it.id })
        assertEquals(listOf("next"), (entries[3] as ConversationEntry.Tools).messages.map { it.id })
    }

    @Test fun streamingAppendKeepsIdentityAndExposesFailuresWhileOtherToolsRun() {
        val first = tool("first", status = "in_progress")
        val initial = conversationTimeline(listOf(first)).single() as ConversationEntry.Tools
        val failed = first.copy(tool = first.tool!!.copy(status = "failed", error = "File unavailable"))
        val running = tool("second", status = "in_progress")
        val updated = conversationTimeline(listOf(failed, running)).single() as ConversationEntry.Tools
        assertEquals(initial.key, updated.key)
        assertEquals(running, updated.running)
        assertEquals(1, updated.failedCount)
        assertFalse(updated.completed)
        assertEquals("File unavailable", updated.messages.first().tool!!.error)
        val ended = conversationTimeline(listOf(tool("first", status = "ended"))).single() as ConversationEntry.Tools
        assertNull(ended.running)
        assertFalse(ended.completed)
    }

    @Test fun prependedHistoryRetainsEveryToolAndPreservesBodyBoundaries() {
        val user = ChatMessage("user", "user", "Inspect the project.", turnId = "turn")
        val current = listOf(tool("recent"), ChatMessage("answer", "assistant", "Done", turnId = "turn"))
        val merged = conversationTimeline(listOf(user, tool("older")) + current)
        assertEquals(3, merged.size)
        assertEquals(listOf("older", "recent"), (merged[1] as ConversationEntry.Tools).messages.map { it.id })
        assertEquals("Done", (merged.last() as ConversationEntry.Message).message.text)
    }
}
