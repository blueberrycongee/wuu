import XCTest
@testable import WuuCore

final class PersonalCacheTests: XCTestCase {
    func testCacheIsLoginHostAgentScopedAndCannotResurrectAfterInvalidation() async throws {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: directory) }
        let cache = PersonalHistoryCache(directory: directory, identity: ["server", "alice", "login-one", "host-one"])
        let agent: JSONValue = ["agent_id": "main", "user_space_id": "local", "name": "Wuu", "kind": "main", "status": "active"]
        try await cache.save(agent: agent, records: [["seq": 1, "role": "user", "content": "Private history"]])
        let saved = try await cache.load()
        XCTAssertEqual(saved?["records"].array.first?["content"].string, "Private history")
        let foreign = PersonalHistoryCache(directory: directory, identity: ["server", "alice", "login-two", "host-one"])
        let missing = try await foreign.load()
        XCTAssertNil(missing)
        let otherAgent = try await cache.load(agentID: "other")
        XCTAssertNil(otherAgent)
        try await cache.invalidate(removeCache: true)
        do { try await cache.save(agent: agent, records: []); XCTFail("Stale writer restored a revoked cache") } catch {}
        let reopened = PersonalHistoryCache(directory: directory, identity: ["server", "alice", "login-one", "host-one"])
        let deleted = try await reopened.load()
        XCTAssertNil(deleted)
    }
}
