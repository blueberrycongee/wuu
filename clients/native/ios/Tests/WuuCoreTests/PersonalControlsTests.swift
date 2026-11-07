import XCTest
@testable import WuuCore

private actor ControlsProbe {
    var calls: [JSONValue] = []
    var taskAgent = "main"
    func foreignTask() { taskAgent = "foreign" }
    func request(_ method: String, _ params: JSONValue) throws -> JSONValue {
        calls.append(params)
        let input = params["input"]
        switch params["method"].string {
        case "agents.open", "agents.update", "agents.pause", "agents.resume": return ["result": ["agent": ["agent_id": input["agent_id"], "user_space_id": "local"]]]
        case "tasks.get", "tasks.update", "tasks.stop", "tasks.resume": return ["result": ["task": ["task_id": "task-one", "agent_id": .string(taskAgent), "user_space_id": "local", "revision": 9, "requirements_revision": 3]]]
        default: return ["result": ["status": "uncertain"]]
        }
    }
}
final class PersonalControlsTests: XCTestCase {
    private var initialized: JSONValue { ["user_space_id": "local", "product_domain": "personal", "extension_inventory": [
        ["id": "plugin:bundled:personal", "fingerprint": "personal-one", "graph_generation": "graph-one", "runtime_state": "active"]]] }
    func testAgentSettingsAndSpecialistDecisionKeepExplicitIdentities() async throws {
        let probe = ControlsProbe(), client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized); _ = try await client.selectAgent("main")
        _ = try await client.updateAgent(expectedRevision: 8, name: "Mosaic", communication: "Keep it brief")
        _ = try await client.decideSpecialist("proposal-one", approved: false)
        let calls = await probe.calls
        XCTAssertEqual(calls[1]["input"]["expected_revision"].number, 8)
        XCTAssertEqual(calls[1]["input"]["agent_id"].string, "main")
        XCTAssertEqual(calls[1]["input"]["session_id"], .null)
        XCTAssertEqual(calls[2]["input"], ["proposal_id": "proposal-one", "approved": false])
        await client.invalidate()
        do { _ = try await client.decideSpecialist("proposal-one", approved: true); XCTFail("Offline decision admitted") } catch {}
    }
    func testTaskMutationRetainsDisplayedCASAndRejectsForeignOwner() async throws {
        let probe = ControlsProbe(), client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized); _ = try await client.selectAgent("main")
        _ = try await client.changeTask("task-one", expectedRevision: 7, action: .update, goal: "New exact goal")
        let calls = await probe.calls
        XCTAssertEqual(calls.last?["method"].string, "tasks.update")
        XCTAssertEqual(calls.last?["input"]["expected_revision"].number, 7)
        XCTAssertEqual(calls.last?["input"]["goal"].string, "New exact goal")
        XCTAssertEqual(calls.last?["input"]["requirements_revision"], .null)
        _ = try await client.taskHistory("task-one", requirementsRevision: 2)
        let history = await probe.calls
        XCTAssertEqual(history.last?["input"]["requirements_revision"].number, 2)
        XCTAssertEqual(history.last?["input"]["session_id"], .null)
        await probe.foreignTask()
        do { _ = try await client.changeTask("task-one", expectedRevision: 9, action: .stop); XCTFail("Foreign task mutation admitted") } catch {}
        let end = await probe.calls
        XCTAssertEqual(end.last?["method"].string, "tasks.get")
    }
    func testMemoryEditAndSettingsKeepExplicitRevisionWithoutUserInputProof() async throws {
        let probe = ControlsProbe(), client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized); _ = try await client.selectAgent("main")
        _ = try await client.memory()
        let browse = await probe.calls
        XCTAssertEqual(browse.last?["input"]["include_inactive"], .bool(true))
        _ = try await client.correctMemory("fact-one", expectedRevision: 4, content: "Exact correction", requestID: "edit-one")
        _ = try await client.updateSettings(expectedRevision: 2, notifications: false, quietHours: ["enabled": true, "start": "22:00", "end": "08:00", "timezone": "UTC"], notificationBatchSeconds: 300)
        let calls = await probe.calls
        XCTAssertEqual(calls[2]["input"]["agent_id"].string, "main")
        XCTAssertEqual(calls[2]["input"]["request_id"].string, "edit-one")
        XCTAssertEqual(calls[2]["input"]["expected_revision"].number, 4)
        XCTAssertEqual(calls[2]["user_input"], .null)
        XCTAssertEqual(calls[3]["input"]["expected_revision"].number, 2)
        XCTAssertEqual(calls[3]["input"]["notification_batch_seconds"].number, 300)
        _ = try await client.searchHistory(query: "Ada travel", timeMin: "2026-10-18T00:00:00Z", timeMax: "2026-10-19T00:00:00Z", cursor: "snapshot-cursor")
        let search = await probe.calls
        XCTAssertEqual(search.last?["method"].string, "agents.search")
        XCTAssertEqual(search.last?["input"]["agent_id"].string, "main")
        XCTAssertEqual(search.last?["input"]["time_min"].string, "2026-10-18T00:00:00Z")
        XCTAssertEqual(search.last?["input"]["cursor"].string, "snapshot-cursor")

    }
}
