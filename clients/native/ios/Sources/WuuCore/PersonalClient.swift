import Foundation
import CryptoKit

public enum PersonalTaskAction: String, Sendable { case update, stop, resume }

public struct PersonalCalendarConfirmation: Sendable {
    public let preview: JSONValue
    fileprivate let owner: UUID
    fileprivate let agentID: String
    // Consent belongs to the displayed connection/selection, unlike a recoverable message draft.
    fileprivate let epoch: UInt64
}

public enum PersonalClientError: Error, Equatable, LocalizedError {
    case unavailable, isolationRequired, noAgentSelected, selectionChanged, invalidResponse, emptyMessage
    public var errorDescription: String? {
        switch self {
        case .unavailable: return "Personal is unavailable on this host. Connect to a host with Personal user-space routing enabled."
        case .isolationRequired: return "Personal requires a verified separate host identity before activation. Projects remain available."
        case .noAgentSelected: return "Choose a Personal assistant first."
        case .selectionChanged: return "The Personal connection or assistant changed. Reload before continuing."
        case .invalidResponse: return "The host returned an invalid Personal response."
        case .emptyMessage: return "Write a message first."
        }
    }
}

/// Keep the same draft for an explicit retry after an uncertain send. It is not an acknowledgement.
public struct PersonalMessageDraft: Sendable {
    public let agentID: String
    public let requestID: String
    public let prompt: String
    public let attachments: [InputAttachment]
    fileprivate let owner: UUID
}

/// One instance belongs to one authenticated account and host. Discard it on login/host change.
/// This adapter neither connects nor grants access. The host enforces user-space authority.
public actor PersonalClient {
    public typealias Transport = @Sendable (String, JSONValue) async throws -> JSONValue
    private var transport: Transport
    private let owner = UUID()
    private var epoch: UInt64 = 0
    private var verifiedScope: JSONValue?
    private var binding: (id: String, fingerprint: String, graph: String)?
    private var calendarBinding: (id: String, fingerprint: String, graph: String)?
    public private(set) var selectedAgentID: String?

    public init(transport: @escaping Transport) { self.transport = transport }
    public init(connection: RemoteConnection) {
        transport = { method, params in try await connection.call(method, params: params) }
    }

    /// Rebind only within the same authenticated account and host. The caller clears this adapter on identity changes.
    public func reconnect(_ connection: RemoteConnection, initialized: JSONValue) throws {
        transport = { method, params in try await connection.call(method, params: params) }
        try configure(initialized)
    }

    /// Call with the current connection's initialize result, again after every reconnect/reload.
    /// The admitted graph is captured separately from the plugin's code fingerprint.
    public func configure(_ initialized: JSONValue) throws {
        invalidate()
        guard initialized["user_space_id"].string == "local", initialized["product_domain"].string == "personal" else { throw PersonalClientError.unavailable }
        verifiedScope = ["user_space_id": initialized["user_space_id"], "product_domain": initialized["product_domain"]]
        let candidates = initialized["extension_inventory"].array.filter {
            $0["id"].string == "plugin:bundled:personal" || $0["provenance"]["plugin_id"].string == "personal"
        }
        let bundled = candidates.first { $0["id"].string == "plugin:bundled:personal" }
        guard let record = bundled ?? (candidates.count == 1 ? candidates.first : nil),
              record["runtime_state"].string == "active", record["enabled"] != .bool(false),
              let id = record["id"].string, !id.isEmpty,
              let fingerprint = record["fingerprint"].string, !fingerprint.isEmpty,
              let graph = record["graph_generation"].string, !graph.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else {
            throw PersonalClientError.unavailable
        }
        binding = (id, fingerprint, graph)
        let calendars = initialized["extension_inventory"].array.filter { $0["id"].string == "plugin:bundled:personal-calendar" || $0["provenance"]["plugin_id"].string == "personal-calendar" }
        if let calendar = calendars.first(where: { $0["id"].string == "plugin:bundled:personal-calendar" }) ?? (calendars.count == 1 ? calendars.first : nil), calendar["runtime_state"].string == "active", calendar["enabled"] != .bool(false),
           let id = calendar["id"].string, let fingerprint = calendar["fingerprint"].string, !id.isEmpty, !fingerprint.isEmpty,
           let graph = calendar["graph_generation"].string, !graph.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { calendarBinding = (id, fingerprint, graph) }
    }

    /// Inventory events omit immutable host scope; only the verified live initialization can supply it.
    public func refreshInventory(_ event: JSONValue) throws {
        guard let scope = verifiedScope, case .object(var initialized) = scope else { throw PersonalClientError.unavailable }
        initialized["extension_inventory"] = event["extension_inventory"]
        try configure(.object(initialized))
    }

    /// Call immediately on disconnect, sign-out, revocation, or plugin-generation invalidation.
    public func invalidate() {
        epoch &+= 1
        binding = nil
        calendarBinding = nil
        verifiedScope = nil
        selectedAgentID = nil
    }

    public func listAgents() async throws -> JSONValue { try await request("agents.list") }
    public func ensureDefaultAgent(name: String? = nil) async throws -> JSONValue {
        try await request("agents.ensure_default", input: name.map { ["name": .string($0)] } ?? [:])
    }

    /// Opening restores the named assistant; it never selects or creates a thread.
    public func selectAgent(_ agentID: String) async throws -> JSONValue {
        if selectedAgentID != agentID { epoch &+= 1; selectedAgentID = nil }
        guard !agentID.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { throw PersonalClientError.noAgentSelected }
        let result = try await request("agents.open", input: ["agent_id": .string(agentID)])
        guard result["agent"]["agent_id"].string == agentID,
              result["agent"]["user_space_id"].string == "local" else { throw PersonalClientError.invalidResponse }
        selectedAgentID = agentID
        return result
    }

    public func history(endSequence: Int? = nil) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        var input: [String: JSONValue] = ["agent_id": .string(agentID)]
        if let endSequence { input["end_seq"] = .number(Double(endSequence)) }
        return try await request("agents.history", input: .object(input))
    }

    public func listTasks() async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        let result = try await request("tasks.list", input: ["agent_id": .string(agentID)])
        guard case .array(let tasks) = result["tasks"], tasks.allSatisfy({ $0["agent_id"].string == agentID && $0["user_space_id"].string == "local" }) else { throw PersonalClientError.invalidResponse }
        return result
    }

    public func task(_ taskID: String) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        let result = try await request("tasks.get", input: ["task_id": .string(taskID)])
        guard result["task"]["task_id"].string == taskID, result["task"]["agent_id"].string == agentID,
              result["task"]["user_space_id"].string == "local" else { throw PersonalClientError.invalidResponse }
        return result
    }

    public func taskHistory(_ taskID: String, requirementsRevision: Int? = nil) async throws -> JSONValue {
        let stamp = epoch
        _ = try await task(taskID)
        guard stamp == epoch else { throw PersonalClientError.selectionChanged }
        var input: [String: JSONValue] = ["task_id": .string(taskID)]
        if let requirementsRevision { input["requirements_revision"] = .number(Double(requirementsRevision)) }
        return try await request("tasks.history", input: .object(input))
    }

    public func changeTask(_ taskID: String, expectedRevision: Int, action: PersonalTaskAction, goal: String? = nil, title: String? = nil) async throws -> JSONValue {
        let stamp = epoch
        _ = try await task(taskID)
        var input: [String: JSONValue] = ["task_id": .string(taskID), "expected_revision": .number(Double(expectedRevision))]
        if let goal { input["goal"] = .string(goal) }; if let title { input["title"] = .string(title) }
        let result = try await request("tasks." + action.rawValue, input: .object(input), expectedEpoch: stamp)
        guard result["task"]["task_id"].string == taskID, result["task"]["agent_id"].string == selectedAgentID, result["task"]["user_space_id"].string == "local" else { throw PersonalClientError.invalidResponse }
        return result
    }
    public func searchHistory(query: String, timeMin: String? = nil, timeMax: String? = nil, cursor: String? = nil) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        var input: [String: JSONValue] = ["agent_id": .string(agentID), "query": .string(query), "limit": 25]
        if let timeMin { input["time_min"] = .string(timeMin) }
        if let timeMax { input["time_max"] = .string(timeMax) }
        if let cursor { input["cursor"] = .string(cursor) }
        return try await request("agents.search", input: .object(input))
    }
    public func memory(query: String = "") async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        return try await request("memory.query", input: ["agent_id": .string(agentID), "query": .string(query), "include_inactive": true, "limit": 100])
    }
    public func correctMemory(_ id: String, expectedRevision: Int, content: String, requestID: String) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        return try await request("memory.correct", input: ["agent_id": .string(agentID), "id": .string(id), "expected_revision": .number(Double(expectedRevision)), "content": .string(content), "request_id": .string(requestID)])
    }
    public func forgetMemory(_ id: String, expectedRevision: Int) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        return try await request("memory.forget", input: ["agent_id": .string(agentID), "id": .string(id), "expected_revision": .number(Double(expectedRevision))])
    }
    public func updateAgent(expectedRevision: Int, name: String, communication: String, responsibility: String? = nil) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        var input: [String: JSONValue] = ["agent_id": .string(agentID), "expected_revision": .number(Double(expectedRevision)), "name": .string(name), "communication": .string(communication)]
        if let responsibility { input["responsibility"] = .string(responsibility) }
        return try await request("agents.update", input: .object(input))
    }
    public func setAgentPaused(_ paused: Bool, expectedRevision: Int) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        return try await request(paused ? "agents.pause" : "agents.resume", input: ["agent_id": .string(agentID), "expected_revision": .number(Double(expectedRevision))])
    }
    public func specialistProposals() async throws -> JSONValue { try await request("agents.proposals") }
    public func decideSpecialist(_ id: String, approved: Bool) async throws -> JSONValue {
        try await request("agents.confirm_specialist", input: ["proposal_id": .string(id), "approved": .bool(approved)])
    }
    public func settings() async throws -> JSONValue { try await request("settings.get") }
    public func updateSettings(expectedRevision: Int, notifications: Bool, quietHours: JSONValue, notificationBatchSeconds: Int = 0) async throws -> JSONValue {
        try await request("settings.update", input: ["expected_revision": .number(Double(expectedRevision)), "notifications": .bool(notifications), "quiet_hours": quietHours, "notification_batch_seconds": .number(Double(notificationBatchSeconds))])
    }
    public func routines() async throws -> JSONValue { try await request("routines.list") }
    public func calendarAgenda(calendarID: String, start: String, end: String, timezone: String, refresh: Bool = false) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        return try await request(refresh ? "calendar.refresh" : "calendar.agenda", input: ["agent_id": .string(agentID), "calendar_id": .string(calendarID), "time_min": .string(start), "time_max": .string(end), "timezone": .string(timezone)])
    }
    public func calendarOperations() async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        let result = try await request("operations.list", calendar: true)
        guard case .array(let rows) = result["operations"], rows.allSatisfy({ $0["user_space_id"].string == "local" }) else { throw PersonalClientError.invalidResponse }
        return ["operations": .array(rows.filter { $0["agent_id"].string == agentID })]
    }
    public func calendarOperation(_ id: String, reconcile: Bool = false) async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        let stamp = epoch
        let result = try await request("operations.get", input: ["operation_id": .string(id)], calendar: true)
        guard result["operation_id"].string == id, result["agent_id"].string == agentID, result["user_space_id"].string == "local" else { throw PersonalClientError.invalidResponse }
        return reconcile ? try await request("operations.reconcile", input: ["operation_id": .string(id)], expectedEpoch: stamp, calendar: true) : result
    }
    public func prepareCalendarConfirmation(_ preview: JSONValue) throws -> PersonalCalendarConfirmation {
        guard let agentID = selectedAgentID, preview["agent_id"].string == agentID, preview["user_space_id"].string == "local", preview["status"].string == "proposed",
              let revision = preview["revision"].number, revision >= 1, revision.rounded() == revision,
              let digest = preview["preview_digest"].string, digest.range(of: "^[a-f0-9]{64}$", options: .regularExpression) != nil,
              ["create", "reschedule"].contains(preview["kind"].string ?? ""),
              ["operation_id", "task_id", "account_id", "calendar_id", "notification_notice"].allSatisfy({ !(preview[$0].string ?? "").isEmpty }),
              validCalendarFields(preview["fields"]), (preview["requirements_revision"].number ?? 0) >= 1,
              ["all", "externalOnly", "none"].contains(preview["fields"]["send_updates"].string ?? ""),
              case .array = preview["fields"]["attendees"], preview["availability"]["checked_at"].string != nil,
              case .array = preview["availability"]["conflicting_event_ids"] else { throw PersonalClientError.invalidResponse }
        if preview["kind"].string == "reschedule", !validCalendarFields(preview["original_event"]["fields"]) { throw PersonalClientError.invalidResponse }
        return PersonalCalendarConfirmation(preview: preview, owner: owner, agentID: agentID, epoch: epoch)
    }
    private func validCalendarFields(_ fields: JSONValue) -> Bool {
        guard !(fields["summary"].string ?? "").isEmpty, fields["location"].string != nil, case .array(let attendees) = fields["attendees"] else { return false }
        guard attendees.allSatisfy({ row in
            guard let email = row["email"].string, email.contains("@"), case .bool = row["optional"] else { return false }; return true
        }) else { return false }
        return [fields["start"], fields["end"]].allSatisfy { time in
            guard !(time["timezone"].string ?? "").isEmpty else { return false }
            let date = time["date"].string, instant = time["date_time"].string
            return (date != nil && instant == nil && date?.range(of: "^\\d{4}-\\d{2}-\\d{2}$", options: .regularExpression) != nil) ||
                (date == nil && instant != nil && instant?.range(of: "^\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}(?:\\.\\d+)?(?:Z|[+-]\\d{2}:\\d{2})$", options: .regularExpression) != nil)
        }
    }
    public func confirmCalendar(_ confirmation: PersonalCalendarConfirmation) async throws -> JSONValue {
        guard confirmation.owner == owner, confirmation.agentID == selectedAgentID, confirmation.epoch == epoch else { throw PersonalClientError.selectionChanged }
        let preview = confirmation.preview
        return try await request("operations.confirm", input: ["operation_id": preview["operation_id"], "expected_revision": preview["revision"], "preview_digest": preview["preview_digest"]], calendar: true)
    }

    public func prepareMessage(_ prompt: String, attachments: [InputAttachment] = [], requestID: String? = nil) throws -> PersonalMessageDraft {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        try PersonalStoredDraft.validate(prompt, attachments)
        let identifier = requestID ?? UUID().uuidString
        guard identifier.range(of: "^[A-Za-z0-9_-]{1,128}$", options: .regularExpression) != nil else { throw PersonalClientError.invalidResponse }
        return PersonalMessageDraft(agentID: agentID, requestID: identifier, prompt: prompt, attachments: attachments, owner: owner)
    }

    public func restoreDraft(_ stored: PersonalStoredDraft) throws -> PersonalMessageDraft {
        guard stored.agentID == selectedAgentID else { throw PersonalClientError.selectionChanged }
        try PersonalStoredDraft.validate(stored.prompt, stored.attachments)
        return PersonalMessageDraft(agentID: stored.agentID, requestID: stored.requestID, prompt: stored.prompt, attachments: stored.attachments, owner: owner)
    }

    public func send(_ draft: PersonalMessageDraft) async throws -> JSONValue {
        guard draft.owner == owner, selectedAgentID == draft.agentID else { throw PersonalClientError.selectionChanged }
        var input: [String: JSONValue] = ["agent_id": .string(draft.agentID), "request_id": .string(draft.requestID), "prompt": .string(draft.prompt)]
        var proof: [String: JSONValue] = ["request_id": .string(draft.requestID), "prompt": .string(draft.prompt)]
        if !draft.attachments.isEmpty {
            let files: [JSONValue] = draft.attachments.map { file in
                ["sha256": .string(SHA256.hash(data: file.data).map { String(format: "%02x", $0) }.joined()), "filename": .string(file.filename), "media_type": .string(file.mediaType)]
            }
            input["attachments"] = .array(files)
            proof["attachments"] = .array(zip(files, draft.attachments).map { reference, file in
                guard case .object(var values) = reference else { return .null }
                values["data"] = .string(file.data.base64EncodedString()); return .object(values)
            })
        }
        return try await request("agents.send", input: .object(input), userInput: .object(proof))
    }

    public func attachment(sequence: Int, descriptor: JSONValue, taskID: String? = nil, requirementsRevision: Int? = nil) async throws -> LoadedAttachment {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        let stamp = epoch
        if let taskID { _ = try await task(taskID) }
        guard stamp == epoch, sequence > 0, let digest = descriptor["sha256"].string, digest.count == 64,
              ["image", "file", "artifact"].contains(descriptor["kind"].string ?? ""),
              let index = descriptor["index"].number, index >= 0, index.rounded() == index else { throw PersonalClientError.invalidResponse }
        guard (descriptor["size"].number ?? 0) <= 12 * 1024 * 1024 else { throw NativeError.invalid("手机支持 12 MB 以内的附件，请在宿主查看此文件") }
        let media = descriptor["mime_type"].string ?? ""
        guard ["image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf"].contains(media) else { throw NativeError.invalid("手机暂不支持此附件格式") }
        var input: [String: JSONValue] = ["seq": .number(Double(sequence)), "kind": descriptor["kind"], "index": .number(index), "sha256": .string(digest)]
        if let taskID {
            guard let requirementsRevision else { throw PersonalClientError.invalidResponse }
            input["task_id"] = .string(taskID); input["requirements_revision"] = .number(Double(requirementsRevision))
        } else { input["agent_id"] = .string(agentID) }
        let encoded = try await readAttachmentChunks(taskID == nil ? "agents.attachments.read" : "tasks.attachments.read", params: .object(input), mediaType: media, limit: 16 * 1024 * 1024) { method, input in
            try await self.request(method, input: input, expectedEpoch: stamp)
        }
        guard stamp == epoch, let data = Data(base64Encoded: encoded), SHA256.hash(data: data).map({ String(format: "%02x", $0) }).joined() == digest.lowercased() else { throw PersonalClientError.invalidResponse }
        return try decodeInlineAttachment(["media_type": .string(media), "filename": descriptor["name"]], encoded: encoded)
    }

    public func stopReply() async throws -> JSONValue {
        guard let agentID = selectedAgentID else { throw PersonalClientError.noAgentSelected }
        return try await request("agents.stop_reply", input: ["agent_id": .string(agentID)])
    }

    private func request(_ method: String, input: JSONValue = [:], userInput: JSONValue? = nil, expectedEpoch: UInt64? = nil, calendar: Bool = false) async throws -> JSONValue {
        try Task.checkCancellation()
        if let expectedEpoch, expectedEpoch != epoch { throw PersonalClientError.selectionChanged }
        guard let binding = calendar ? calendarBinding : binding else { throw PersonalClientError.unavailable }
        let stamp = epoch
        do {
            var params: [String: JSONValue] = ["id": .string(binding.id),
                "fingerprint": .string(binding.fingerprint), "graph_generation": .string(binding.graph), "method": .string(method), "input": input,
                "request_id": .string(UUID().uuidString), "user_space_id": "local", "product_domain": "personal"]
            if let userInput { params["user_input"] = userInput }
            let envelope = try await transport("plugin/client/request", .object(params))
            try Task.checkCancellation()
            guard stamp == epoch else { throw PersonalClientError.selectionChanged }
            guard case .object(let value) = envelope, let result = value["result"] else { throw PersonalClientError.invalidResponse }
            return result
        } catch {
            try Task.checkCancellation()
            guard stamp == epoch else { throw PersonalClientError.selectionChanged }
            let message = error.localizedDescription
            if message.contains("user_host_isolation_required:") { throw PersonalClientError.isolationRequired }
            if message.contains("request belongs to another user space or product domain") { throw PersonalClientError.unavailable }
            throw error
        }
    }
}

/// Display task artifacts without projecting tool or system internals as conversation text.
public func personalDisplayRecords(_ records: [JSONValue]) -> [JSONValue] {
    records.compactMap { record in
        let conversational = ["user", "assistant"].contains(record["role"].string ?? "")
        guard conversational || !record["attachments"].array.isEmpty else { return nil }
        return ["seq": record["seq"], "role": conversational ? record["role"] : "assistant",
            "content": conversational ? (record["display_content"].string.map(JSONValue.string) ?? record["content"]) : "",
            "attachments": record["attachments"]]
    }
}
