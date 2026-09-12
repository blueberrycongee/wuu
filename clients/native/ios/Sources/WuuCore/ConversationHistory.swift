import Foundation
import CryptoKit

public struct HistoryMessage: Codable, Identifiable, Sendable {
    public let id: String
    public let turn_id: String
    public let role: String
    public let text: String
    public var stableID: String { turn_id + ":" + id }
}
public struct HistoryThread: Codable, Identifiable, Sendable {
    public let id: String
    public let title: String
    public let updated_at: String
    public let status: String?
    public let messages: [HistoryMessage]
}
public struct HistorySettings: Codable, Sendable {
    public let host: String
    public let enabled: Bool
    public let generation: String
}
public struct HistoryEntry: Codable, Identifiable, Sendable {
    public let id: String
    public let title: String
    public let updated_at: String
    public let revision: String
    public let digest: String
    public let deleted: Bool
}
public struct HistoryPage: Codable, Sendable {
    public let host: String
    public let enabled: Bool
    public let generation: String
    public let entries: [HistoryEntry]
    public let cursor: String
    public let more: Bool
}
public struct HistoryBody: Codable, Sendable {
    public let thread: HistoryThread
    public let revision: String
}

public struct HistorySnapshot: Codable, Sendable {
    public private(set) var generation = ""
    public private(set) var cursor = "0"
    public private(set) var enabled = false
    public private(set) var entries: [String: HistoryEntry] = [:]
    public private(set) var bodies: [String: HistoryBody] = [:]
    public init() {}
    public var sortedEntries: [HistoryEntry] { entries.values.sorted { $0.updated_at > $1.updated_at } }

    public mutating func merge(_ page: HistoryPage) throws {
        guard let next = UInt64(page.cursor) else { throw NativeError.invalid("Invalid history cursor") }
        if generation != page.generation || !page.enabled { self = HistorySnapshot() }
        guard next >= (UInt64(cursor) ?? 0) else { throw NativeError.invalid("History cursor moved backwards") }
        generation = page.generation
        enabled = page.enabled
        cursor = page.cursor
        guard enabled else { return }
        for entry in page.entries {
            if entry.deleted {
                entries.removeValue(forKey: entry.id)
                bodies.removeValue(forKey: entry.id)
            } else {
                entries[entry.id] = entry
                if bodies[entry.id]?.revision != entry.revision { bodies.removeValue(forKey: entry.id) }
            }
        }
    }
    public mutating func store(_ body: HistoryBody, generation: String) throws {
        guard enabled, self.generation == generation,
              entries[body.thread.id]?.revision == body.revision else { throw NativeError.invalid("History changed; refresh again") }
        bodies[body.thread.id] = body
        let keep = Set(sortedEntries.filter { bodies[$0.id] != nil }.prefix(20).map(\.id))
        bodies = bodies.filter { keep.contains($0.key) }
        // Bound disk and memory use even when every snapshot is near the server's 4 MiB limit.
        while try JSONEncoder().encode(bodies).count > 16 * 1024 * 1024 {
            guard let oldest = sortedEntries.reversed().first(where: { bodies[$0.id] != nil }) else { break }
            bodies.removeValue(forKey: oldest.id)
        }
    }
}

/// A separate instance per authenticated device and host. Invalidation fences all in-flight responses.
public actor ConversationHistory {
    private let api: AccountAPI
    private let account: AccountSession
    private let host: String
    private let file: URL
    private var epoch: UInt64 = 0
    private var active = true
    private var pending: (id: UUID, task: Task<HistorySnapshot, Error>)?
    private var snapshot = HistorySnapshot()

    public init(account: AccountSession, host: String, directory: URL) throws {
        self.account = account
        self.host = host
        api = try AccountAPI(server: account.server)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        file = try Self.cacheFile(account: account, host: host, directory: directory)
        if let size = try? file.resourceValues(forKeys: [.fileSizeKey]).fileSize, size <= 20 * 1024 * 1024,
           let raw = try? Data(contentsOf: file),
           let cached = try? JSONDecoder().decode(HistorySnapshot.self, from: raw) { snapshot = cached }
    }
    private static func cacheFile(account: AccountSession, host: String, directory: URL) throws -> URL {
        let partition = try JSONEncoder().encode([account.server, account.username, account.pub, host])
        let name = SHA256.hash(data: partition).map { String(format: "%02x", $0) }.joined()
        return directory.appendingPathComponent(name + ".json")
    }
    /// Call after invalidating the selected store if its host was revoked. Also removes cold caches.
    public static func prune(account: AccountSession, hosts: [String], directory: URL) throws {
        let allowed = try Set(hosts.map { try cacheFile(account: account, host: $0, directory: directory).lastPathComponent })
        guard FileManager.default.fileExists(atPath: directory.path) else { return }
        for url in try FileManager.default.contentsOfDirectory(at: directory, includingPropertiesForKeys: nil) where !allowed.contains(url.lastPathComponent) {
            try FileManager.default.removeItem(at: url)
        }
    }
    public func current() -> HistorySnapshot { snapshot }
    public func invalidate(removeCache: Bool) throws {
        active = false
        epoch &+= 1
        pending?.task.cancel()
        snapshot = HistorySnapshot()
        if removeCache, FileManager.default.fileExists(atPath: file.path) { try FileManager.default.removeItem(at: file) }
    }
    private func check(_ value: UInt64) throws {
        try Task.checkCancellation()
        guard active, epoch == value else { throw CancellationError() }
    }
    private func persist() throws {
        let data = try JSONEncoder().encode(snapshot)
        #if os(iOS)
        try data.write(to: file, options: [.atomic, .completeFileProtectionUntilFirstUserAuthentication])
        #else
        try data.write(to: file, options: .atomic)
        #endif
        var url = file
        var values = URLResourceValues()
        values.isExcludedFromBackup = true
        try url.setResourceValues(values)
    }
    public func sync() async throws -> HistorySnapshot {
        try await serialized(.sync)
    }
    private enum Operation { case sync, setting(Bool) }
    // Actor isolation alone does not serialize requests across network awaits.
    // Settings and pages must commit in order; callers must not receive an unfinished page set.
    private func serialized(_ operation: Operation) async throws -> HistorySnapshot {
        let previous = pending?.task, id = UUID()
        let task = Task {
            if let previous { _ = try? await previous.value }
            try check(epoch)
            switch operation {
            case .sync: return try await fetchPages()
            case .setting(let enabled): try await changeSetting(enabled); return snapshot
            }
        }
        pending = (id, task)
        defer { if pending?.id == id { pending = nil } }
        let result = try await task.value
        try Task.checkCancellation()
        return result
    }
    private func fetchPages() async throws -> HistorySnapshot {
        let stamp = epoch
        try check(stamp)
        var conflicts = 0
        while true {
            do {
                let page: HistoryPage = try await api.request("/history", token: account.token,
                    query: ["host": host, "generation": snapshot.generation, "after": snapshot.cursor])
                try check(stamp)
                guard page.host == host else { throw NativeError.invalid("Wrong history host") }
                let previousCursor = snapshot.cursor
                try snapshot.merge(page)
                try persist()
                if !page.more { return snapshot }
                guard page.cursor != previousCursor else { throw NativeError.invalid("History pagination stalled") }
            } catch NativeError.http(409, _) where conflicts == 0 {
                try check(stamp)
                snapshot = HistorySnapshot()
                try persist()
                conflicts += 1
            }
        }
    }
    public func thread(_ id: String) async throws -> HistoryThread {
        let stamp = epoch
        try check(stamp)
        if let body = snapshot.bodies[id] { return body.thread }
        let generation = snapshot.generation
        let body: HistoryBody = try await api.request("/history/thread", token: account.token,
            query: ["host": host, "generation": generation, "id": id])
        try check(stamp)
        guard body.thread.id == id else { throw NativeError.invalid("Wrong history thread") }
        try snapshot.store(body, generation: generation)
        try persist()
        return body.thread
    }
    public func setEnabled(_ enabled: Bool) async throws {
        _ = try await serialized(.setting(enabled))
    }
    private func changeSetting(_ enabled: Bool) async throws {
        let stamp = epoch
        try check(stamp)
        let _: HistorySettings = try await api.request("/history/settings", token: account.token,
                                                      body: ["host": host, "enabled": enabled])
        try check(stamp)
        epoch &+= 1
        snapshot = HistorySnapshot()
        try persist()
    }
}
