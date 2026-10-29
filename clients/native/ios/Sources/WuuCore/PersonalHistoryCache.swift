import Foundation
import CryptoKit

/// Bounded display-only cache. Its login/host hash grants no authority to read or execute remotely.
public actor PersonalHistoryCache {
    private let file: URL
    private let scope: String
    private var valid = true
    private static let maxBytes = 2 * 1024 * 1024
    public init(directory: URL, identity: [String]) {
        let key = Self.key(identity)
        scope = key; file = directory.appendingPathComponent(key + ".json")
    }
    private static func key(_ identity: [String]) -> String {
        SHA256.hash(data: (try? JSONEncoder().encode(identity)) ?? Data()).map { String(format: "%02x", $0) }.joined()
    }
    public static func prune(directory: URL, identities: [[String]]) throws {
        guard FileManager.default.fileExists(atPath: directory.path) else { return }
        let keep = Set(identities.map { key($0) + ".json" })
        for entry in try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil)
            where !keep.contains(entry.lastPathComponent) { try FileManager.default.removeItem(at: entry) }
    }
    private func document() throws -> JSONValue {
        guard valid else { throw CancellationError() }
        guard FileManager.default.fileExists(atPath: file.path) else { return ["scope": .string(scope), "agents": [:]] }
        let size = try file.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
        guard size <= Self.maxBytes else { throw PersonalClientError.invalidResponse }
        let value = try JSONDecoder().decode(JSONValue.self, from: Data(contentsOf: file))
        guard value["scope"].string == scope else { throw PersonalClientError.invalidResponse }
        return value
    }
    public func load(agentID: String? = nil) throws -> JSONValue? {
        let value = try document()
        guard let id = agentID ?? value["selected"].string, value["agents"][id]["agent"]["agent_id"].string == id,
              value["agents"][id]["agent"]["user_space_id"].string == "local" else { return nil }
        return value["agents"][id]
    }
    public func save(agent: JSONValue, records: [JSONValue]) throws {
        guard let id = agent["agent_id"].string, agent["user_space_id"].string == "local" else { throw PersonalClientError.invalidResponse }
        let current = try document()
        var agents: [String: JSONValue] = [:]
        if case .object(let items) = current["agents"] { agents = items }
        // Store only presentation fields; no bytes, paths, credentials or session identifiers.
        let shown = JSONValue.object(Dictionary(uniqueKeysWithValues: ["agent_id", "user_space_id", "name", "kind", "responsibility", "status"].map { ($0, agent[$0]) }))
        let rows = records.suffix(100).map { record in
            JSONValue.object(["seq": record["seq"], "role": record["role"], "content": record["content"],
                "attachments": .array(record["attachments"].array.map { attachment in
                    .object(Dictionary(uniqueKeysWithValues: ["kind", "index", "name", "mime_type", "size", "sha256"].map { ($0, attachment[$0]) }))
                })])
        }
        agents[id] = ["agent": shown, "records": .array(rows)]
        if agents.count > 8, let oldest = agents.keys.sorted().first(where: { $0 != id }) { agents.removeValue(forKey: oldest) }
        var document: JSONValue = ["scope": .string(scope), "selected": .string(id), "agents": .object(agents)]
        let encoder = JSONEncoder(); encoder.outputFormatting = [.sortedKeys]
        var bytes = try encoder.encode(document)
        if bytes.count > Self.maxBytes {
            agents = [id: ["agent": shown, "records": .array(Array(rows.suffix(10)))]]
            document = ["scope": .string(scope), "selected": .string(id), "agents": .object(agents)]
            bytes = try encoder.encode(document)
        }
        guard bytes.count <= Self.maxBytes else { return }
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        if (try? Data(contentsOf: file)) == bytes { return }
        #if os(iOS)
        try bytes.write(to: file, options: [.atomic, .completeFileProtection])
        #else
        try bytes.write(to: file, options: [.atomic])
        #endif
    }
    public func invalidate(removeCache: Bool) throws {
        valid = false
        if removeCache, FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
    }
}
