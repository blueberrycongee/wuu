import Foundation

public struct ChatMessage: Identifiable, Sendable, Equatable {
    public let id: String
    public let role: String
    public var text: String
    public var contentRef: String
    public var attachments: [JSONValue]
    public var tool: ToolActivity?
    public var sourceSessionID: String
    public var sourceSessionName: String
    public init(id: String, role: String, text: String, contentRef: String = "", attachments: [JSONValue] = [], tool: ToolActivity? = nil, sourceSessionID: String = "", sourceSessionName: String = "") {
        self.id = id; self.role = role; self.text = text; self.contentRef = contentRef; self.attachments = attachments
        self.tool = tool
        self.sourceSessionID = sourceSessionID; self.sourceSessionName = sourceSessionName
    }
}

public struct ToolActivity: Sendable, Equatable {
    public let name: String
    public let status: String
    public let arguments: String
    public let result: String
    public let error: String
    public let presentation: JSONValue
    public init(_ item: JSONValue, turnStatus: String) {
        name = [item["display"]["label"].string, item["name"].string].compactMap { $0 }.first { !$0.isEmpty } ?? "工具"
        let state = item["status"].string ?? ""
        status = state == "in_progress" && turnStatus != "in_progress" ? "ended" : state
        arguments = item["arguments"].string ?? ""
        let text = item["result"].string ?? ""
        result = text.isEmpty ? item["result_detail"]["content"].array.filter { $0["type"].string == "text" }.compactMap { $0["text"].string }.joined(separator: "\n") : text
        error = item["error"].string ?? ""
        presentation = ["id": item["id"], "type": "tool_call", "name": item["name"],
            "status": .string(status), "display": item["display"], "arguments": item["arguments"],
            "result": item["result"], "error": item["error"],
            "result_detail": ["structured_content": item["result_detail"]["structured_content"],
                "content": .array(item["result_detail"]["content"].array.filter { $0["type"].string == "text" })]]
    }
    public var statusLabel: String {
        switch status { case "in_progress": "执行中"; case "completed": "已完成"; case "failed": "失败"; case "ended": "已结束"; default: "状态未知" }
    }
}

public struct PendingMessage: Identifiable, Sendable {
    public let value: JSONValue
    public let held: Bool
    public var id: String { value["id"].string ?? "" }
    public var text: String { value["prompt"].string ?? "" }
    public var origin: String { value["origin"].string ?? "queue" }
    public init(_ value: JSONValue, held: Bool) { self.value = value; self.held = held }
    public var resumeParams: JSONValue {
        ["thread_id": value["thread_id"], "client_id": value["id"], "prompt": value["prompt"],
         "images": .array(value["images"].array), "files": .array(value["files"].array)]
    }
}

/// The host owns turns. The phone projects conversation text and tool activity.
public struct ChatThread: Identifiable, Sendable {
    public let id: String
    public var title: String
    public var updatedAt: String
    public var pinned: Bool
    public var archived: Bool
    public var running: Bool
    public var readOnly: Bool
    public var settings: ThreadSettings
    public var engine: String
    public private(set) var turns: [JSONValue]
    public private(set) var pending: [PendingMessage]
    public private(set) var historyCursor: String
    public private(set) var messages: [ChatMessage] = []
    public private(set) var rows: [ConversationRow] = []
    private var projectedTurns: [String: [ChatMessage]] = [:]
    private var removedItems: Set<String> = []
    public init(_ value: JSONValue, pending: [JSONValue] = [], held: [JSONValue] = []) {
        self.pending = pending.map { PendingMessage($0, held: false) } + held.map { PendingMessage($0, held: true) }
        id = value["id"].string ?? ""
        title = value["title"].string ?? value["preview"].string ?? "新会话"
        updatedAt = value["updated_at"].string ?? ""
        pinned = value["pinned"].bool
        archived = value["archived"].bool
        running = value["status"].string == "in_progress"
        readOnly = value["read_only"].bool
        settings = ThreadSettings(value)
        engine = value["engine_id"].string ?? "wuu"
        turns = value["turns"].array
        historyCursor = value["history_cursor"].string ?? ""
        rebuildProjection()
    }
    private static func project(_ turn: JSONValue) -> [ChatMessage] {
            var messages: [ChatMessage] = turn["items"].array.compactMap { item in
                guard let type = item["type"].string, ["user_message", "agent_message", "error", "tool_call"].contains(type) else { return nil }
                let sessionMessage = item["origin"].string == "plugin" && item["presentation_kind"].string == "session_message"
                // Host-marked model notifications are not user messages; attributed peer messages remain visible.
                if type == "user_message", !sessionMessage,
                   ["wuu_process_notification", "wuu_agent_notification"].contains(item["name"].string ?? "") { return nil }
                return ChatMessage(id: (turn["id"].string ?? "") + ":" + (item["id"].string ?? ""),
                    role: type == "user_message" ? "user" : type == "error" ? "error" : type == "tool_call" ? "tool" : "assistant",
                    text: item["text"].string ?? item["error"].string ?? "", contentRef: item["remote_content_ref"].string ?? "",
                    attachments: item["images"].array + item["files"].array + item["markdown_images"].array +
                        item["result_detail"]["content"].array.filter { $0["type"].string == "image" }.map { part in
                            ["media_type": part["mime_type"], "data": part["data"], "remote_ref": part["remote_ref"]]
                        },
                    tool: type == "tool_call" ? ToolActivity(item, turnStatus: turn["status"].string ?? "") : nil,
                    sourceSessionID: sessionMessage ? item["related_session_id"].string ?? "" : "",
                    sourceSessionName: item["name"].string ?? "")
            }
            let cancelled = turn["status"].string == "interrupted" && turn["error"]["category"].string == "cancelled"
            if let error = turn["error"]["message"].string, !cancelled, !error.isEmpty, !messages.contains(where: { $0.role == "error" }) {
                messages.append(ChatMessage(id: (turn["id"].string ?? "") + ":error", role: "error", text: error))
            }
        return messages
    }
    private mutating func rebuildProjection(changed turn: JSONValue? = nil) {
        if let turn {
            projectedTurns[turn["id"].string ?? ""] = Self.project(turn)
        } else {
            projectedTurns = Dictionary(turns.map { ($0["id"].string ?? "", Self.project($0)) }, uniquingKeysWith: { _, new in new })
        }
        // Reads during layout must be O(1); unchanged turns keep their parsed tool payloads.
        messages = turns.flatMap { projectedTurns[$0["id"].string ?? ""] ?? [] }
        rows = ConversationRow.grouped(messages)
    }
    /// Older pages can split a turn. Retain live updates and deletions received while fetching.
    public mutating func prependHistory(_ page: JSONValue) {
        guard page["thread_id"].string == id, page["cursor"].string == historyCursor, !historyCursor.isEmpty else { return }
        var older: [JSONValue] = []
        for old in page["turns"].array {
            guard case .object(var merged) = old else { continue }
            let current = turns.first { $0["id"] == old["id"] }
            let currentItems = current?["items"].array ?? []
            let items = old["items"].array.filter { item in
                !currentItems.contains { $0["id"] == item["id"] } &&
                    !removedItems.contains((old["id"].string ?? "") + ":" + (item["id"].string ?? ""))
            } + currentItems
            if case .object(let fields) = current { merged.merge(fields) { _, new in new } }
            merged["items"] = .array(items)
            older.append(.object(merged))
        }
        turns = older + turns.filter { current in !older.contains { $0["id"] == current["id"] } }
        historyCursor = page["history_cursor"].string ?? ""
        rebuildProjection()
    }
    public mutating func expandContent(_ ref: String, item: JSONValue) {
        for t in turns.indices {
            guard case .object(var turn) = turns[t] else { continue }
            var items = turn["items"]?.array ?? []
            guard let i = items.firstIndex(where: { $0["remote_content_ref"].string == ref && $0["id"] == item["id"] }) else { continue }
            items[i] = item; turn["items"] = .array(items); turns[t] = .object(turn)
            rebuildProjection(changed: turns[t])
        }
    }
    public mutating func apply(_ method: String, _ params: JSONValue) {
        if method == "thread/updated", params["thread"]["id"].string == id {
            let thread = ChatThread(params["thread"])
            title = thread.title; pinned = thread.pinned; archived = thread.archived; updatedAt = thread.updatedAt
            settings = thread.settings; engine = thread.engine; readOnly = thread.readOnly
            return
        }
        if ["turn/queued", "turn/steered"].contains(method), params["message"]["thread_id"].string == id {
            let message = params["message"]
            pending.removeAll { $0.id == message["id"].string }; pending.append(PendingMessage(message, held: false))
            return
        }
        guard params["thread_id"].string == id else { return }
        if method == "turn/held" {
            let held = params["messages"].array.map { PendingMessage($0, held: true) }
            pending.removeAll { old in old.held || held.contains(where: { $0.id == old.id }) }
            pending += held
            return
        }
        if ["turn/dequeued", "turn/unsteered"].contains(method) {
            let removed = params[method == "turn/dequeued" ? "queue_id" : "steer_id"].string
            pending.removeAll { $0.id == removed }; return
        }
        defer {
            if !pending.isEmpty {
                let materialized = Set(turns.flatMap { $0["items"].array }.compactMap { $0["client_id"].string })
                pending.removeAll { materialized.contains($0.id) }
            }
        }
        if params["turn"] != .null {
            let turn = params["turn"]
            if let i = turns.firstIndex(where: { $0["id"] == turn["id"] }) { turns[i] = turn }
            else { turns.append(turn) }
            rebuildProjection(changed: turn)
            if method == "turn/started" { running = true }
            if ["turn/completed", "turn/interrupted", "turn/error"].contains(method) { running = false }
            return
        }
        if method == "item/removed" {
            removedItems.insert((params["turn_id"].string ?? "") + ":" + (params["item_id"].string ?? ""))
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
        } else if ["item/agentMessage/delta", "item/agentMessage/replace"].contains(method),
                  let i = items.firstIndex(where: { $0["id"] == params["item_id"] }),
                  case .object(var item) = items[i] {
            item["text"] = method == "item/agentMessage/replace" ? params["text"] : .string((item["text"]?.string ?? "") + (params["delta"].string ?? ""))
            items[i] = .object(item)
        } else { return }
        turn["items"] = .array(items)
        turns[t] = .object(turn)
        rebuildProjection(changed: turns[t])
    }
}
