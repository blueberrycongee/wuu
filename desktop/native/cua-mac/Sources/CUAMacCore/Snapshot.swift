import Foundation

struct SnapshotTicket {
    let id = UUID().uuidString
    let processID: Int32
    let launchTime: TimeInterval
    let controlEpoch: String
    private(set) var consumed = false

    mutating func invalidate() { consumed = true }

    func validate(reference: String?, processID: Int32, launchTime: TimeInterval, epoch: String, requiresReference: Bool) throws {
        guard self.processID == processID, self.launchTime == launchTime, controlEpoch == epoch else {
            throw ComputerError.staleSnapshot("app or control ownership changed")
        }
        if requiresReference || reference != nil {
            guard reference == id, !consumed else {
                throw ComputerError.staleSnapshot("snapshot_id is missing, replaced, or already consumed by input")
            }
        }
    }
}

// Pages are slices of the stored snapshot, never a new AX traversal with reused IDs.
struct SnapshotPage {
    let text: String
    let total: Int
    let nextOffset: Int?

    init(text: String, query: String?, offset: Int, limit: Int) {
        let lines = text.components(separatedBy: "\n").filter {
            guard let query, !query.isEmpty else { return true }
            return $0.localizedCaseInsensitiveContains(query)
        }
        total = lines.count
        let start = min(max(0, offset), total)
        let end = min(start + max(1, min(limit, 300)), total)
        self.text = lines[start..<end].joined(separator: "\n")
        nextOffset = end < total ? end : nil
    }
}

public struct AXExpectation: Sendable {
    let role: String?
    let title: String?
    let description: String?
    let value: String?
    let exists: Bool

    init(_ fields: [String: Any]) throws {
        role = fields["role"] as? String
        title = fields["title"] as? String
        description = fields["description"] as? String
        value = fields["value"] as? String
        exists = fields["exists"] as? Bool ?? true
        guard [role, title, description, value].contains(where: { $0 != nil }) else {
            throw ComputerError.invalidArguments("expect requires an exact role, title, description, or value")
        }
    }

    func evaluate(_ elements: [AXElementDescriptor], truncated: Bool) -> String {
        let matches = elements.filter {
            (role == nil || $0.role == role) && (title == nil || $0.title == title) &&
            (description == nil || $0.description == description) && (value == nil || $0.value == value)
        }.count
        if exists {
            if matches > 1 { return "ambiguous" }
            if matches == 1 { return truncated ? "unavailable" : "matched" }
        } else if matches == 0 && !truncated { return "matched" }
        return truncated ? "unavailable" : "pending"
    }
}
