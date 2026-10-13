import Foundation

public struct ConversationRow: Identifiable, Sendable, Equatable {
    public var id: String { messages[0].id }
    public var messages: [ChatMessage]
    public var isProcessGroup: Bool { messages[0].isProcess }
    public private(set) var processActive = false
    public var isToolGroup: Bool { !isProcessGroup && messages[0].tool != nil }

    /// Preserve turn boundaries and image publication order when folding completed work.
    public static func grouped(_ messages: [ChatMessage]) -> [ConversationRow] {
        var rows: [ConversationRow] = []
        for message in messages {
            let previous = rows.last
            let sameTurn = previous?.messages.last?.turnID == message.turnID
            let joinsProcess = message.isProcess && previous?.isProcessGroup == true &&
                (previous?.messages.last?.hasInlineAttachments == false || (message.tool != nil && previous?.messages.last?.tool != nil))
            let joinsTools = !message.isProcess && message.tool != nil && previous?.isToolGroup == true
            if sameTurn && (joinsProcess || joinsTools) {
                rows[rows.count - 1].messages.append(message)
            } else {
                rows.append(ConversationRow(messages: [message]))
            }
        }
        // A turn can contain several replies separated by resumed work. Only the
        // trailing process is current; earlier groups already led to an answer.
        if let last = rows.indices.last {
            rows[last].processActive = rows[last].isProcessGroup && rows[last].messages.contains { $0.turnActive }
        }
        return rows
    }
}
