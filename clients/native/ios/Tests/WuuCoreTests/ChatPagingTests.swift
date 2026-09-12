import XCTest
@testable import WuuCore

final class ChatPagingTests: XCTestCase {
    func testToolCompletionWinsOverStaleExpansionAndHistory() {
        var thread = ChatThread(["id": "t", "history_cursor": "page", "turns": [["id": "turn", "status": "in_progress", "items": []]]])
        let started: JSONValue = ["id": "tool", "type": "tool_call", "name": "read_file", "status": "in_progress", "remote_content_ref": "old"]
        thread.apply("item/started", ["thread_id": "t", "turn_id": "turn", "item": started])
        XCTAssertEqual(thread.messages.first?.tool?.status, "in_progress")
        let completed: JSONValue = ["id": "tool", "type": "tool_call", "name": "read_file", "status": "failed", "error": "File removed", "remote_content_ref": "new"]
        thread.apply("item/completed", ["thread_id": "t", "turn_id": "turn", "item": completed])
        thread.expandContent("old", item: started)
        thread.prependHistory(["thread_id": "t", "cursor": "page", "turns": [["id": "turn", "items": [started]]]])
        XCTAssertEqual(thread.messages.count, 1)
        XCTAssertEqual(thread.messages.first?.tool?.status, "failed")
        XCTAssertEqual(thread.messages.first?.tool?.error, "File removed")
        thread.apply("item/removed", ["thread_id": "t", "turn_id": "turn", "item_id": "tool"])
        XCTAssertTrue(thread.messages.isEmpty)
    }

    func testSplitPagePreservesStreamAndRemovalAndRejectsStalePage() {
        var thread = ChatThread(["id": "t", "status": "in_progress", "history_cursor": "page", "turns": [
            ["id": "turn", "items": [["id": "a", "type": "agent_message", "text": "live"]]]]])
        thread.apply("item/agentMessage/delta", ["thread_id": "t", "turn_id": "turn", "item_id": "a", "delta": " update"])
        thread.apply("item/removed", ["thread_id": "t", "turn_id": "turn", "item_id": "removed"])
        let page: JSONValue = ["thread_id": "t", "cursor": "page", "turns": [["id": "turn", "items": [
            ["id": "u", "type": "user_message", "text": "earlier"],
            ["id": "removed", "type": "agent_message", "text": "deleted"],
            ["id": "a", "type": "agent_message", "text": "stale"]]]]]
        thread.prependHistory(page)
        thread.prependHistory(page)
        XCTAssertEqual(thread.messages.map(\.text), ["earlier", "live update"])
        XCTAssertTrue(thread.running)
        thread.apply("turn/error", ["thread_id": "t", "turn": ["id": "turn", "items": [], "error": ["message": "Provider unavailable"]]])
        XCTAssertFalse(thread.running)
        XCTAssertEqual(thread.messages.first?.role, "error")
        XCTAssertEqual(thread.messages.first?.text, "Provider unavailable")
    }
}
