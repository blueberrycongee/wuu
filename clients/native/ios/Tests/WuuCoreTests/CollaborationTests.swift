import XCTest
@testable import WuuCore

final class CollaborationTests: XCTestCase {
    func testPollingUpdatesExistingTaskAndSettledReplyReplacesPreview() {
        var timeline = CollaborationTimeline()
        timeline.merge(["messages": [["id": "task", "seq": 1, "task_title": "Build", "task_state": "doing"]],
                        "responses": [["id": "reply", "body": "Working"]]])
        XCTAssertEqual(timeline.responses.count, 1)
        timeline.merge(["messages": [["id": "reply", "seq": 2, "body": "Done"],
                                      ["id": "task", "seq": 1, "task_title": "Build", "task_state": "done"]],
                        "responses": [["id": "reply", "body": "Working"]]])
        XCTAssertEqual(timeline.messages.map(\.id), ["task", "reply"])
        XCTAssertEqual(timeline.messages.first?.taskState, "done")
        XCTAssertTrue(timeline.responses.isEmpty)
    }

    func testOverlappingPagesKeepMessagesAndStructuredContent() {
        var timeline = CollaborationTimeline()
        timeline.merge(["messages": [["id": "a", "seq": 1, "images": [["media_type": "image/png"]]],
                                      ["id": "b", "seq": 2, "body": "hello"]]])
        timeline.merge(["messages": [["id": "b", "seq": 2, "body": "updated"],
                                      ["id": "c", "seq": 3, "reply_to": "b", "work": ["state": "needs_human"]]]])
        XCTAssertEqual(timeline.messages.count, 3)
        XCTAssertEqual(timeline.messages[0].value["images"].array.count, 1)
        XCTAssertEqual(timeline.messages[1].body, "updated")
        XCTAssertEqual(timeline.messages[2].replyID, "b")
        XCTAssertEqual(timeline.messages[2].taskState, "needs_human")
    }

    func testChannelInputUsesRoomContractAndValidatesAttachments() throws {
        let file = try InputAttachment(filename: "spec.pdf", mediaType: "application/pdf", data: Data([1]))
        let params = try CollaborationInput.params(roomID: "room", text: "", attachments: [file])
        XCTAssertEqual(params["room_id"].string, "room")
        XCTAssertEqual(params["files"].array.first?["filename"].string, "spec.pdf")
        XCTAssertEqual(params["thread_id"], .null)
        XCTAssertEqual(params["prompt"], .null)
        XCTAssertThrowsError(try CollaborationInput.params(roomID: "room", text: " \n", attachments: []))
        XCTAssertThrowsError(try CollaborationInput.params(roomID: "", text: "hello", attachments: []))
        XCTAssertThrowsError(try CollaborationInput.params(roomID: "room", text: "", attachments: Array(repeating: file, count: 5)))
    }
}
