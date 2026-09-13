import Foundation
import Observation

@MainActor public protocol CollaborationConnection: AnyObject {
    var connected: Bool { get }
    func channelCall(_ method: String, _ params: JSONValue) async throws -> JSONValue
}

@MainActor @Observable public final class CollaborationModel {
    public var agents: [CollaborationAgent] = []
    public var rooms: [CollaborationRoom] = []
    public private(set) var roomID: String?
    public var timelines: [String: CollaborationTimeline] = [:]
    public var drafts: [String: String] = [:]
    public var attachments: [String: [InputAttachment]] = [:]
    public var positions: [String: String] = [:]
    public private(set) var hasOlder: [String: Bool] = [:]
    public private(set) var loadingOlder = false
    public var error: String?
    public private(set) var loading = false
    public private(set) var sending = false
    public private(set) var creating = false
    private var revision = UUID()
    private var selection = UUID()
    private var mutation = 0
    private var removedRooms: Set<String> = []
    private var taskRefreshOffset: [String: Int] = [:]
    public var room: CollaborationRoom? { rooms.first { $0.id == roomID } }
    public var timeline: CollaborationTimeline { timelines[roomID ?? ""] ?? CollaborationTimeline() }
    public init() {}

    public func reset() {
        revision = UUID(); selection = UUID(); mutation += 1
        agents = []; rooms = []; roomID = nil; timelines = [:]; drafts = [:]; attachments = [:]; positions = [:]
        hasOlder = [:]; loadingOlder = false
        removedRooms = []; taskRefreshOffset = [:]
        error = nil; loading = false; sending = false; creating = false
    }
    public func select(_ id: String?) { selection = UUID(); roomID = id }

    public func poll(app: any CollaborationConnection) async {
        let stamp = revision
        loading = rooms.isEmpty
        defer { if revision == stamp { loading = false } }
        while !Task.isCancelled, revision == stamp, app.connected {
            do {
                let version = mutation
                let directory = try await app.channelCall("channel/bootstrap", [:])
                try Task.checkCancellation()
                guard revision == stamp else { return }
                if version == mutation {
                    let nextIDs = Set(directory["rooms"].array.compactMap { $0["id"].string })
                    for id in rooms.map(\.id) where !nextIDs.contains(id) {
                        removedRooms.insert(id)
                        timelines[id] = nil; drafts[id] = nil; attachments[id] = nil; positions[id] = nil
                        hasOlder[id] = nil; taskRefreshOffset[id] = nil
                    }
                    removedRooms.subtract(nextIDs)
                    agents = directory["agents"].array.map(CollaborationAgent.init)
                    rooms = directory["rooms"].array.map(CollaborationRoom.init).sorted { $0.updated > $1.updated }
                    if let id = roomID, !rooms.contains(where: { $0.id == id }) { select(nil) }
                }
                loading = false
                if let id = roomID { try await refreshRoom(id, app: app) }
                error = nil
            } catch is CancellationError { return }
            catch { if revision == stamp, !Task.isCancelled { self.error = error.localizedDescription; loading = false } }
            guard app.connected, revision == stamp else { return }
            do { try await Task.sleep(for: .seconds(2)) } catch { return }
        }
    }

    public func refreshRoom(_ id: String, app: any CollaborationConnection) async throws {
        let stamp = revision, version = mutation
        let previous = timelines[id]?.messages.last?.seq
        let result = try await app.channelCall("channel/message/list", pageParams(id))
        try Task.checkCancellation()
        guard revision == stamp else { return }
        let first = Int(result["messages"].array.first?["seq"].number ?? 0)
        var gap: [JSONValue] = []
        // Older loaded tasks remain live without rereading the entire history.
        // Rotate at most two per poll to bound cost for long rooms.
        let tasks = timelines[id]?.messages.filter { !$0.taskTitle.isEmpty && $0.seq < first } ?? []
        if !tasks.isEmpty {
            let start = (taskRefreshOffset[id] ?? 0) % tasks.count
            for index in 0..<min(2, tasks.count) {
                let task = tasks[(start + index) % tasks.count]
                let page = try await app.channelCall("channel/message/list", pageParams(id, after: task.seq - 1, before: task.seq + 1, limit: 1))
                try Task.checkCancellation()
                guard revision == stamp else { return }
                gap.append(contentsOf: page["messages"].array)
            }
            taskRefreshOffset[id] = (start + min(2, tasks.count)) % tasks.count
        }
        if var after = previous, first > after + 1 {
            while after < first - 1 {
                let page = try await app.channelCall("channel/message/list", pageParams(id, after: after, before: first, latest: false))
                try Task.checkCancellation()
                guard revision == stamp else { return }
                let rows = page["messages"].array
                gap.append(contentsOf: rows)
                guard let next = rows.last?["seq"].number, Int(next) > after else { break }
                after = Int(next)
                if rows.count < 30 { break }
            }
        }
        guard version == mutation else { return }
        var timeline = timelines[id] ?? CollaborationTimeline()
        timeline.merge(["messages": .array(gap)])
        timeline.merge(result)
        timelines[id] = timeline
        if hasOlder[id] == nil { hasOlder[id] = result["messages"].array.count == 30 }
        if roomID == id { _ = try await app.channelCall("channel/room/read", ["room_id": .string(id)]) }
    }

    public func loadOlder(app: any CollaborationConnection) async throws {
        guard !loadingOlder, let id = roomID, let before = timelines[id]?.messages.first?.seq else { return }
        let stamp = revision
        loadingOlder = true
        defer { if revision == stamp { loadingOlder = false } }
        let result = try await app.channelCall("channel/message/list", pageParams(id, before: before))
        try Task.checkCancellation()
        guard revision == stamp, !removedRooms.contains(id) else { return }
        var timeline = timelines[id] ?? CollaborationTimeline()
        // Loading an older page must not replace the current reply preview.
        timeline.merge(["messages": result["messages"]])
        timelines[id] = timeline
        hasOlder[id] = result["messages"].array.count == 30
    }

    private func pageParams(_ id: String, after: Int = 0, before: Int = 0, latest: Bool = true, limit: Int = 30) -> JSONValue {
        ["room_id": .string(id), "limit": .number(Double(limit)), "latest": .bool(latest),
         "after_seq": .number(Double(after)), "before_seq": .number(Double(before)), "attachment_metadata_only": true]
    }

    public func openAgent(_ id: String, app: any CollaborationConnection) async throws {
        try await open("channel/direct-message/open", params: ["agent_id": .string(id)], app: app)
    }
    public func createRoom(name: String, agents: Set<String>, app: any CollaborationConnection) async throws {
        try await open("channel/room/create", params: ["name": .string(name), "agent_ids": .array(agents.sorted().map(JSONValue.string))], app: app)
    }
    private func open(_ method: String, params: JSONValue, app: any CollaborationConnection) async throws {
        guard !creating else { return }
        let stamp = revision, target = selection
        mutation += 1
        creating = true
        defer { if revision == stamp { creating = false } }
        let result = try await app.channelCall(method, params)
        guard revision == stamp else { return }
        mutation += 1
        let room = CollaborationRoom(result["room"])
        guard !room.id.isEmpty else { throw NativeError.invalid("电脑没有返回房间") }
        removedRooms.remove(room.id)
        rooms.removeAll { $0.id == room.id }; rooms.insert(room, at: 0)
        if selection == target { select(room.id) }
    }

    public func send(app: any CollaborationConnection) async throws {
        guard !sending, let id = roomID else { return }
        let text = drafts[id] ?? "", files = attachments[id] ?? [], stamp = revision
        let params = try CollaborationInput.params(roomID: id, text: text, attachments: files)
        mutation += 1
        sending = true
        defer { if revision == stamp { sending = false } }
        let result: JSONValue
        do { result = try await app.channelCall("channel/message/send", params) }
        catch is CancellationError { throw CancellationError() }
        catch { throw NativeError.invalid("发送未确认，请先检查房间消息再决定是否重发：" + error.localizedDescription) }
        guard revision == stamp, !removedRooms.contains(id) else { return }
        mutation += 1
        if drafts[id] == text { drafts[id] = "" }
        attachments[id]?.removeAll { attachment in files.contains { $0.id == attachment.id } }
        var timeline = timelines[id] ?? CollaborationTimeline()
        timeline.merge(["messages": .array([result["message"]])])
        timelines[id] = timeline
    }
}
