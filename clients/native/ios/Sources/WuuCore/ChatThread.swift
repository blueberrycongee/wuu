import Foundation

public struct ChatMessage: Identifiable, Sendable, Equatable {
    public let id: String
    public let role: String
    public var text: String
    public init(id: String, role: String, text: String) { self.id = id; self.role = role; self.text = text }
}

/// The host owns turns. Only user/assistant text is projected into the phone timeline.
public struct ChatThread: Identifiable, Sendable {
    public let id: String
    public var title: String
    public var updatedAt: String
    public var pinned: Bool
    public var running: Bool
    public var readOnly: Bool
    public private(set) var turns: [JSONValue]
    private var pending: [JSONValue]
    public init(_ value: JSONValue, pending: [JSONValue] = []) {
        self.pending = pending
        id = value["id"].string ?? ""
        title = value["title"].string ?? value["preview"].string ?? "新会话"
        updatedAt = value["updated_at"].string ?? ""
        pinned = value["pinned"].bool
        running = value["status"].string == "in_progress"
        readOnly = value["read_only"].bool
        turns = value["turns"].array
    }
    public var messages: [ChatMessage] {
        let completed: [ChatMessage] = turns.flatMap { turn in
            turn["items"].array.compactMap { item in
                guard let type = item["type"].string, ["user_message", "agent_message", "error"].contains(type) else { return nil }
                return ChatMessage(id: (turn["id"].string ?? "") + ":" + (item["id"].string ?? ""),
                    role: type == "user_message" ? "user" : type == "error" ? "error" : "assistant",
                    text: item["text"].string ?? item["error"].string ?? "")
            }
        }
        return completed + pending.map {
            ChatMessage(id: "pending:" + ($0["id"].string ?? ""), role: "pending", text: $0["prompt"].string ?? "")
        }
    }
    public mutating func apply(_ method: String, _ params: JSONValue) {
        if method == "turn/queued", params["message"]["thread_id"].string == id {
            let message = params["message"]
            pending.removeAll { $0["id"] == message["id"] }; pending.append(message)
            return
        }
        guard params["thread_id"].string == id else { return }
        if method == "turn/dequeued" { pending.removeAll { $0["id"] == params["queue_id"] }; return }
        if params["turn"] != .null {
            let turn = params["turn"]
            if let i = turns.firstIndex(where: { $0["id"] == turn["id"] }) { turns[i] = turn }
            else { turns.append(turn) }
            if method == "turn/started" { running = true }
            if ["turn/completed", "turn/interrupted", "turn/error"].contains(method) { running = false }
            return
        }
        guard let t = turns.firstIndex(where: { $0["id"] == params["turn_id"] }),
              case .object(var turn) = turns[t] else { return }
        var items = turns[t]["items"].array
        if ["item/started", "item/completed"].contains(method) {
            let item = params["item"]
            if let i = items.firstIndex(where: { $0["id"] == item["id"] }) { items[i] = item }
            else { items.append(item) }
        } else if method == "item/removed" {
            items.removeAll { $0["id"] == params["item_id"] }
        } else if method == "item/agentMessage/delta",
                  let i = items.firstIndex(where: { $0["id"] == params["item_id"] }),
                  case .object(var item) = items[i] {
            item["text"] = .string((item["text"]?.string ?? "") + (params["delta"].string ?? ""))
            items[i] = .object(item)
        }
        turn["items"] = .array(items)
        turns[t] = .object(turn)
    }
}
