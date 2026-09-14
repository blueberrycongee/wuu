import XCTest
import CryptoKit
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
    @MainActor func testResumeUsesCurrentPublicReplyAndRejectsStaleSelection() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        let reply: JSONValue = ["id": "reply", "session_ref": "session", "turn_id": "turn", "state": "failed"]
        var timeline = CollaborationTimeline(); timeline.merge(["responses": .array([reply])])
        model.select("a"); model.timelines["a"] = timeline
        var resumes = 0
        fixture.handle = { method, params in
            if method == "channel/session/resume" {
                XCTAssertEqual(params["sessionRef"].string, "session"); resumes += 1
            }
            return ["messages": [], "responses": []]
        }
        try await model.resume(reply, app: fixture)
        XCTAssertEqual(resumes, 1)
        do { try await model.resume(reply, app: fixture); XCTFail("Finished reply was resumed twice") } catch {}
        model.select("b")
        do { try await model.resume(reply, app: fixture); XCTFail("Old room reply was resumed") } catch {}
        XCTAssertEqual(resumes, 1)
    }

    @MainActor func testAttachmentUsesBoundedProtocolAndRejectsNavigationRace() async throws {
        let model = CollaborationModel(), fixture = ChannelFixture()
        let data = Data("%PDF-test".utf8).base64EncodedString()
        let digest = SHA256.hash(data: Data(("application/pdf\0" + data).utf8)).map { String(format: "%02x", $0) }.joined()
        let reference: JSONValue = .array(["a", "file", 7, 0, .string(digest), "files"])
        let file: JSONValue = ["media_type": "application/pdf", "filename": "brief.pdf", "remote_ref": .string("channel:" + (try JSONEncoder().encode(reference)).base64URL)]
        let value: JSONValue = ["id": "file", "seq": 7, "files": .array([file])]
        var timeline = CollaborationTimeline(); timeline.merge(["messages": .array([value])])
        model.select("a"); model.timelines["a"] = timeline
        var switchRoom = false
        fixture.handle = { method, params in
            XCTAssertEqual(method, "channel/attachment/read")
            XCTAssertEqual(params["room_id"].string, "a"); XCTAssertEqual(params["message_id"].string, "file")
            XCTAssertEqual(params["seq"].number, 7); XCTAssertEqual(params["offset"].number, 0); XCTAssertEqual(params["sha256"].string, digest)
            if switchRoom { model.select("b") }
            return ["data": .string(data), "total": .number(Double(data.count)), "offset": 0, "content_type": "application/pdf"]
        }
        let attachment = try await model.readAttachment(CollaborationMessage(value), field: "files", index: 0, app: fixture)
        XCTAssertEqual(attachment.filename, "brief.pdf")
        switchRoom = true
        do { _ = try await model.readAttachment(CollaborationMessage(value), field: "files", index: 0, app: fixture); XCTFail("Stale attachment opened") }
        catch is CancellationError {} catch { XCTFail("Unexpected error: \(error)") }
    }

    func testInlineAttachmentRejectsUnsupportedContentAndURLs() {
        XCTAssertThrowsError(try decodeInlineAttachment(["media_type": "text/html", "data": "dGVzdA=="]))
        XCTAssertThrowsError(try decodeInlineAttachment(["media_type": "application/pdf", "data": "dGVzdA=="]))
        XCTAssertThrowsError(try decodeInlineAttachment(["media_type": "image/png", "url": "https://example.test/photo.png"]))
    }

    @MainActor func testDeletedRoomClearsAllHostScopedContent() async {
        let model = CollaborationModel(), fixture = ChannelFixture()
        model.rooms = [CollaborationRoom(["id": "deleted"])]
        model.select("deleted"); model.drafts["deleted"] = "private draft"
        model.positions["deleted"] = "message"
        model.followingLatest["deleted"] = false
        model.timelines["deleted"] = CollaborationTimeline()
        fixture.handle = { _, _ in fixture.connected = false; return ["rooms": [], "agents": []] }
        await model.poll(app: fixture)
        XCTAssertNil(model.roomID)
        XCTAssertTrue(model.timelines.isEmpty); XCTAssertTrue(model.drafts.isEmpty); XCTAssertTrue(model.positions.isEmpty); XCTAssertTrue(model.followingLatest.isEmpty)
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
