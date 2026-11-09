import XCTest
@testable import WuuCore

final class ConversationRowTests: XCTestCase {
    private func tool(_ id: String, status: String = "completed") -> ChatMessage {
        ChatMessage(id: id, role: "tool", text: "", contentRef: "detail-" + id,
            tool: ToolActivity(["name": "bash", "status": .string(status)], turnStatus: "in_progress"))
    }

    func testGroupingPreservesTextBoundariesAndToolDetails() {
        let messages = [ChatMessage(id: "user", role: "user", text: "Run checks"), tool("a"), tool("b"),
            ChatMessage(id: "reply", role: "assistant", text: "Retrying"), tool("c", status: "failed")]
        let rows = ConversationRow.grouped(messages)
        XCTAssertEqual(rows.map { $0.messages.map(\.id) }, [["user"], ["a", "b"], ["reply"], ["c"]])
        XCTAssertEqual(rows.flatMap(\.messages), messages)
        XCTAssertTrue(ConversationRow.grouped([]).isEmpty)
    }

    func testStreamingToolKeepsGroupIdentity() {
        let initial = ConversationRow.grouped([tool("a", status: "in_progress")])
        let updated = ConversationRow.grouped([tool("a"), tool("b", status: "in_progress")])
        XCTAssertEqual(initial.first?.id, updated.first?.id)
        XCTAssertEqual(updated.first?.messages.last?.tool?.status, "in_progress")
    }

}
