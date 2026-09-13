import XCTest
@testable import WuuCore

@MainActor private final class ChannelFixture: CollaborationConnection {
    var connected = true
    var calls: [(String, JSONValue)] = []
    var handle: (String, JSONValue) async throws -> JSONValue = { _, _ in [:] }
    func channelCall(_ method: String, _ params: JSONValue) async throws -> JSONValue {
        calls.append((method, params))
        return try await handle(method, params)
    }
}

final class CollaborationLifecycleTests: XCTestCase {
    @MainActor func testDeletedRoomClearsAllHostScopedContent() async {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.rooms = [CollaborationRoom(["id": "deleted"])]
        model.select("deleted"); model.drafts["deleted"] = "private draft"
        model.positions["deleted"] = "message"
        model.timelines["deleted"] = CollaborationTimeline()
        fixture.handle = { _, _ in fixture.connected = false; return ["rooms": [], "agents": []] }
        await model.poll(app: fixture)
        XCTAssertNil(model.roomID)
        XCTAssertTrue(model.timelines.isEmpty); XCTAssertTrue(model.drafts.isEmpty); XCTAssertTrue(model.positions.isEmpty)
    }

    @MainActor func testOlderLoadedTaskRefreshesOutsideLatestWindow() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.select("room")
        var timeline = CollaborationTimeline()
        timeline.merge(["messages": [["id": "task", "seq": 1, "task_title": "Build", "task_state": "doing"], ["id": "tail", "seq": 39]]])
        model.timelines["room"] = timeline
        fixture.handle = { method, params in
            if method == "channel/room/read" { return [:] }
            if params["limit"].number == 1 {
                XCTAssertEqual(params["before_seq"].number, 2)
                return ["messages": [["id": "task", "seq": 1, "task_title": "Build", "task_state": "done"]]]
            }
            return ["messages": [["id": "latest", "seq": 40]], "responses": []]
        }
        try await model.refreshRoom("room", app: fixture)
        XCTAssertEqual(model.timeline.messages.first?.taskState, "done")
    }

    @MainActor func testOlderPageCannotRestoreDeletedRoom() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.rooms = [CollaborationRoom(["id": "room"])]; model.select("room")
        var timeline = CollaborationTimeline()
        timeline.merge(["messages": [["id": "tail", "seq": 40]]]); model.timelines["room"] = timeline
        fixture.handle = { method, _ in
            if method == "channel/bootstrap" { fixture.connected = false; return ["rooms": [], "agents": []] }
            await model.poll(app: fixture)
            return ["messages": [["id": "old", "seq": 1]]]
        }
        try await model.loadOlder(app: fixture)
        XCTAssertNil(model.roomID); XCTAssertTrue(model.timelines.isEmpty)
    }

    @MainActor func testLateSendCannotClearNewDraftOrAnotherRoom() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.select("one"); model.drafts["one"] = "original"
        fixture.handle = { method, params in
            XCTAssertEqual(method, "channel/message/send")
            XCTAssertEqual(params["room_id"].string, "one")
            model.drafts["one"] = "new draft"; model.select("two"); model.drafts["two"] = "other"
            return ["message": ["id": "sent", "seq": 1, "body": "original"]]
        }
        try await model.send(app: fixture)
        XCTAssertEqual(model.drafts["one"], "new draft")
        XCTAssertEqual(model.drafts["two"], "other")
        XCTAssertEqual(model.timelines["one"]?.messages.first?.body, "original")
        XCTAssertNil(model.timelines["two"])
    }

    @MainActor func testHostResetRejectsLateSendAndDirectoryResults() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.select("one"); model.drafts["one"] = "old host"
        fixture.handle = { _, _ in
            model.reset()
            return ["message": ["id": "old", "seq": 1]]
        }
        try await model.send(app: fixture)
        XCTAssertTrue(model.timelines.isEmpty); XCTAssertTrue(model.drafts.isEmpty)
        XCTAssertFalse(model.sending)
        fixture.handle = { _, _ in
            model.reset(); fixture.connected = false
            return ["rooms": [["id": "old room"]], "agents": [["id": "old agent"]]]
        }
        await model.poll(app: fixture)
        XCTAssertTrue(model.rooms.isEmpty); XCTAssertTrue(model.agents.isEmpty)
    }

    @MainActor func testLateDMOpenDoesNotOverrideNavigation() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        fixture.handle = { _, _ in
            model.select("different")
            return ["room": ["id": "dm"]]
        }
        try await model.openAgent("agent", app: fixture)
        XCTAssertEqual(model.roomID, "different")
        XCTAssertEqual(model.rooms.first?.id, "dm")
    }

    @MainActor func testWindowRefreshFillsGapAndPreservesEarlierHistory() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.select("room")
        var initial = CollaborationTimeline()
        initial.merge(["messages": [["id": "1", "seq": 1], ["id": "2", "seq": 2]]])
        model.timelines["room"] = initial
        fixture.handle = { method, params in
            if method == "channel/room/read" { return [:] }
            XCTAssertTrue(params["attachment_metadata_only"].bool)
            if params["latest"].bool {
                return ["messages": [["id": "5", "seq": 5], ["id": "6", "seq": 6]], "responses": []]
            }
            XCTAssertEqual(params["after_seq"].number, 2)
            XCTAssertEqual(params["before_seq"].number, 5)
            return ["messages": [["id": "3", "seq": 3], ["id": "4", "seq": 4]]]
        }
        try await model.refreshRoom("room", app: fixture)
        XCTAssertEqual(model.timeline.messages.map(\.seq), Array(1...6))
    }

    @MainActor func testFailedSendKeepsTextAndAttachmentForRetry() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.select("room"); model.drafts["room"] = "retry"
        let file = try InputAttachment(filename: "file.pdf", mediaType: "application/pdf", data: Data([1]))
        model.attachments["room"] = [file]
        fixture.handle = { _, _ in throw NativeError.invalid("offline") }
        do { try await model.send(app: fixture); XCTFail("Expected error") } catch {}
        XCTAssertEqual(model.drafts["room"], "retry")
        XCTAssertEqual(model.attachments["room"], [file])
        XCTAssertFalse(model.sending)
    }

    @MainActor func testCancelledPollDoesNotApplyOutstandingDirectory() async {
        let model = CollaborationModel(), fixture = ChannelFixture()
        let entered = expectation(description: "request entered")
        var resume: CheckedContinuation<JSONValue, Never>?
        fixture.handle = { _, _ in
            await withCheckedContinuation { continuation in resume = continuation; entered.fulfill() }
        }
        let polling = Task { await model.poll(app: fixture) }
        await fulfillment(of: [entered], timeout: 1)
        polling.cancel()
        resume?.resume(returning: ["rooms": [["id": "late"]]])
        await polling.value
        XCTAssertTrue(model.rooms.isEmpty)
        XCTAssertEqual(fixture.calls.count, 1)
    }
}
