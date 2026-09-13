import Foundation

public struct CollaborationAgent: Identifiable, Equatable, Sendable {
    public let value: JSONValue
    public init(_ value: JSONValue) { self.value = value }
    public var id: String { value["id"].string ?? "" }
    public var name: String { value["name"].string ?? id }
    public var role: String { value["role"].string ?? "" }
}

public struct CollaborationRoom: Identifiable, Equatable, Sendable {
    public let value: JSONValue
    public init(_ value: JSONValue) { self.value = value }
    public var id: String { value["id"].string ?? "" }
    public var name: String { value["name"].string ?? id }
    public var members: [JSONValue] { value["members"].array }
    public var unread: Int { Int(value["unread_count"].number ?? 0) }
    public var updated: String { value["last_message"]["created_at"].string ?? value["created_at"].string ?? "" }
    public func title(agents: [CollaborationAgent]) -> String {
        guard value["kind"].string == "dm", let member = members.first(where: { $0["member_type"].string == "agent" }),
              let agent = agents.first(where: { $0.id == member["member_id"].string }) else { return name }
        return agent.name
    }
}

public struct CollaborationMessage: Identifiable, Equatable, Sendable {
    public let value: JSONValue
    public init(_ value: JSONValue) { self.value = value }
    public var id: String { value["id"].string ?? "" }
    public var seq: Int { Int(value["seq"].number ?? 0) }
    public var body: String { value["body"].string ?? "" }
    public var authorID: String { value["author_id"].string ?? "" }
    public var isHuman: Bool { value["author_type"].string == "human" }
    public var taskTitle: String { value["task_title"].string ?? "" }
    public var taskState: String { value["work"]["state"].string ?? value["task_state"].string ?? "" }
    public var replyID: String? { value["reply_to"].string }
}

public struct CollaborationTimeline: Equatable, Sendable {
    public private(set) var messages: [CollaborationMessage] = []
    public private(set) var responses: [JSONValue] = []
    public init() {}
    // Polling must refresh existing tasks as well as append new messages.
    public mutating func merge(_ result: JSONValue) {
        var byID = Dictionary(messages.map { ($0.id, $0) }, uniquingKeysWith: { _, new in new })
        for value in result["messages"].array {
            let message = CollaborationMessage(value)
            if !message.id.isEmpty { byID[message.id] = message }
        }
        messages = byID.values.sorted { $0.seq == $1.seq ? $0.id < $1.id : $0.seq < $1.seq }
        let durableIDs = Set(messages.map(\.id))
        if result["responses"] != .null { responses = result["responses"].array }
        responses = responses.filter { response in
            !durableIDs.contains(response["id"].string ?? "") && !messages.contains { message in
                let turn = message.value["source_turn_id"].string ?? ""
                return !turn.isEmpty && turn == response["turn_id"].string && message.value["source_session_ref"].string == response["session_ref"].string
            }
        }
    }
}

public enum CollaborationInput {
    public static func params(roomID: String, text: String, attachments: [InputAttachment]) throws -> JSONValue {
        try InputAttachment.validate(attachments, text: text)
        guard !roomID.isEmpty, !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || !attachments.isEmpty else {
            throw NativeError.invalid("请选择房间并输入消息")
        }
        return ["room_id": .string(roomID), "body": .string(text),
                "images": .array(attachments.filter(\.isImage).map(\.value)),
                "files": .array(attachments.filter { !$0.isImage }.map(\.value))]
    }
}
