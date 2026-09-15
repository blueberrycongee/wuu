import XCTest
@testable import WuuCore

final class ChatPagingTests: XCTestCase {
    func testCachedRowsFollowTextToolStatusRemovalAndHistory() {
        var thread = ChatThread(["id": "t", "history_cursor": "older", "turns": [["id": "turn", "status": "in_progress", "items": [
            ["id": "answer", "type": "agent_message", "text": "start"]]]]])
        thread.apply("item/agentMessage/delta", ["thread_id": "t", "turn_id": "turn", "item_id": "answer", "delta": " streamed"])
        thread.apply("item/started", ["thread_id": "t", "turn_id": "turn", "item": ["id": "tool", "type": "tool_call", "name": "read_file", "status": "in_progress"]])
        XCTAssertEqual(thread.rows.flatMap(\.messages), thread.messages)
        XCTAssertEqual(thread.rows.first?.messages.first?.text, "start streamed")
        XCTAssertEqual(thread.rows.last?.messages.first?.tool?.status, "in_progress")
        thread.apply("item/removed", ["thread_id": "t", "turn_id": "turn", "item_id": "tool"])
        thread.prependHistory(["thread_id": "t", "cursor": "older", "turns": [["id": "old", "items": [["id": "user", "type": "user_message", "text": "older"]]]]])
        XCTAssertEqual(thread.rows.flatMap(\.messages), thread.messages)
        XCTAssertEqual(thread.messages.map(\.text), ["older", "start streamed"])
    }

    func testSessionMessageSourceSurvivesLiveDeliveryAndReload() {
        let item: JSONValue = ["id": "message", "type": "user_message", "text": "Coordination update",
            "input_text": "Internal delivery context", "origin": "plugin", "presentation_kind": "session_message",
            "name": "Source task", "related_session_id": "source"]
        let turn: JSONValue = ["id": "turn", "items": [item]]
        var live = ChatThread(["id": "target", "turns": [["id": "turn", "items": []]]])
        live.apply("item/completed", ["thread_id": "target", "turn_id": "turn", "item": item])
        let restored = ChatThread(["id": "target", "turns": [turn]])
        XCTAssertEqual(live.messages, restored.messages)
        XCTAssertEqual(live.messages.first?.sourceSessionID, "source")
        XCTAssertEqual(live.messages.first?.sourceSessionName, "Source task")
        XCTAssertEqual(live.messages.first?.text, "Coordination update")
    }

    func testUserStopPreservesPartialAnswerWithoutSynthesizingFailure() {
        for (status, category, isFailure) in [("interrupted", "cancelled", false), ("failed", "provider", true), ("interrupted", "provider", true)] {
            let turn: JSONValue = ["id": "turn", "status": .string(status),
                "error": ["message": "diagnostic", "category": .string(category)],
                "items": [["id": "answer", "type": "agent_message", "text": "partial answer"]]]
            var live = ChatThread(["id": "t", "status": "in_progress", "turns": []])
            live.apply("turn/error", ["thread_id": "t", "turn": turn])
            let restored = ChatThread(["id": "t", "status": "idle", "turns": [turn]])
            XCTAssertFalse(live.running)
            XCTAssertEqual(live.messages, restored.messages)
            XCTAssertEqual(live.messages.first?.role, "assistant")
            XCTAssertEqual(live.messages.contains { $0.role == "error" }, isFailure)
        }
    }

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
