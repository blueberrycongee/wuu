import Foundation

public struct ConversationRow: Identifiable {
    public var id: String { messages[0].id }
    public var messages: [ChatMessage]
    public var isToolGroup: Bool { messages[0].tool != nil }

    /// Keep tool activity together without moving it across conversation text.
    public static func grouped(_ messages: [ChatMessage]) -> [ConversationRow] {
        var rows: [ConversationRow] = []
        for message in messages {
            if message.tool != nil, rows.last?.isToolGroup == true {
                rows[rows.count - 1].messages.append(message)
            } else {
                rows.append(ConversationRow(messages: [message]))
            }
        }
        return rows
    }
}
