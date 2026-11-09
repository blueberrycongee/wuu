import Foundation

/// A location, never an authorization grant. Revalidate the current account's host directory before opening.
/// Message sequences belong to the named agent's immutable main conversation.
public struct PersonalLocation: Equatable, Sendable {
    public let host: String
    public let agentID: String
    public let taskID: String?
    public let messageSequence: Int?

    private init?(_ values: [String: String]) {
        guard values["version"] == "1", values["product_domain"] == "personal", values["user_space_id"] == "local",
              values["session_id"] == nil, values["workspace_id"] == nil,
              let host = values["host"], (try? Data(base64URL: host).count) == 32,
              let agent = values["agent_id"], Self.validID(agent) else { return nil }
        if let task = values["task_id"], !Self.validID(task) { return nil }
        var sequence: Int?
        if let value = values["message_seq"] {
            guard let number = Int(value), number > 0, String(number) == value else { return nil }
            sequence = number
        }
        self.host = host; agentID = agent; taskID = values["task_id"]; messageSequence = sequence
    }
    private static func validID(_ value: String) -> Bool {
        !value.isEmpty && value.utf8.count <= 256 && value.unicodeScalars.allSatisfy { !CharacterSet.controlCharacters.contains($0) } && value == value.trimmingCharacters(in: .whitespacesAndNewlines)
    }
    public init?(url: URL) {
        guard let parts = URLComponents(url: url, resolvingAgainstBaseURL: false), parts.scheme == "wuu", parts.host == "personal",
              parts.path.isEmpty, parts.user == nil, parts.password == nil, parts.port == nil, parts.fragment == nil else { return nil }
        var values: [String: String] = [:]
        for item in parts.queryItems ?? [] {
            guard let value = item.value, values[item.name] == nil else { return nil }
            values[item.name] = value
        }
        self.init(values)
    }
    public static func notification(_ values: [String: String], recipient: String) -> PersonalLocation? {
        guard !recipient.isEmpty, values["recipient"] == recipient else { return nil }
        return PersonalLocation(values)
    }
}
