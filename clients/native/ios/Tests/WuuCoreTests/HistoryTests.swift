import XCTest
@testable import WuuCore

final class HistoryTests: XCTestCase {
    private func page(_ generation: String = "g1", cursor: String, deleted: Bool = false,
                      revision: String = "1", enabled: Bool = true) throws -> HistoryPage {
        let value: JSONValue = ["host": "host", "enabled": .bool(enabled), "generation": .string(generation),
            "cursor": .string(cursor), "more": false, "entries": [["id": "t1", "title": "Conversation",
                "updated_at": "2026-09-11", "revision": .string(revision), "digest": "d", "deleted": .bool(deleted)]]]
        return try JSONDecoder().decode(HistoryPage.self, from: JSONEncoder().encode(value))
    }
    private func body(_ revision: String = "1") throws -> HistoryBody {
        let value: JSONValue = ["revision": .string(revision), "thread": ["id": "t1", "title": "Conversation",
            "updated_at": "2026-09-11", "messages": [["id": "m1", "turn_id": "turn1", "role": "user", "text": "hello"]]]]
        return try JSONDecoder().decode(HistoryBody.self, from: JSONEncoder().encode(value))
    }
    func testTombstoneRemovesOfflineBodyAndRejectsLateFetch() throws {
        var state = HistorySnapshot()
        try state.merge(page(cursor: "1"))
        try state.store(body(), generation: "g1")
        XCTAssertEqual(state.bodies["t1"]?.thread.messages.first?.text, "hello")
        try state.merge(page(cursor: "2", deleted: true))
        XCTAssertTrue(state.entries.isEmpty)
        XCTAssertTrue(state.bodies.isEmpty)
        XCTAssertThrowsError(try state.store(body(), generation: "g1"))
    }
    func testGenerationResetCannotResurrectOldAccountHistory() throws {
        var state = HistorySnapshot()
        try state.merge(page(cursor: "99"))
        try state.store(body(), generation: "g1")
        try state.merge(page("g2", cursor: "1"))
        XCTAssertEqual(state.cursor, "1")
        XCTAssertTrue(state.bodies.isEmpty)
        XCTAssertThrowsError(try state.store(body(), generation: "g1"))
        try state.merge(page("g3", cursor: "0", enabled: false))
        XCTAssertTrue(state.entries.isEmpty)
        XCTAssertFalse(state.enabled)
    }
    func testRevisionAndCursorProtectAgainstStaleReads() throws {
        var state = HistorySnapshot()
        try state.merge(page(cursor: "1"))
        try state.store(body(), generation: "g1")
        try state.merge(page(cursor: "2", revision: "2"))
        XCTAssertTrue(state.bodies.isEmpty)
        XCTAssertThrowsError(try state.store(body(), generation: "g1"))
        XCTAssertThrowsError(try state.merge(page(cursor: "1")))
        try state.store(body("2"), generation: "g1")
        let restored = try JSONDecoder().decode(HistorySnapshot.self, from: JSONEncoder().encode(state))
        XCTAssertEqual(restored.bodies["t1"]?.revision, "2")
        XCTAssertEqual(restored.cursor, "2")
    }
}
