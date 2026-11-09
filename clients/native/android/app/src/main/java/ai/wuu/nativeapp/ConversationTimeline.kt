package ai.wuu.nativeapp

internal sealed interface ConversationEntry {
    val key: String
    data class Message(val message: ChatMessage) : ConversationEntry {
        override val key get() = message.id
    }
    data class Tools(val messages: List<ChatMessage>) : ConversationEntry {
        override val key get() = "tools:${messages.first().id}"
        val running get() = messages.lastOrNull { it.tool?.status == "in_progress" }
        val failedCount get() = messages.count { it.tool?.status == "failed" }
        val completed get() = messages.all { it.tool?.status == "completed" }
    }
}

internal fun conversationTimeline(messages: List<ChatMessage>): List<ConversationEntry> {
    val entries = mutableListOf<ConversationEntry>()
    var index = 0
    while (index < messages.size) {
        val first = messages[index]
        if (first.tool == null) {
            entries += ConversationEntry.Message(first)
            index++
        } else {
            val start = index++
            // Text and turn boundaries preserve the order of the conversation.
            while (index < messages.size && messages[index].tool != null && messages[index].turnId == first.turnId) index++
            entries += ConversationEntry.Tools(messages.subList(start, index).toList())
        }
    }
    return entries
}
