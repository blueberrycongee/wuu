import XCTest
import CryptoKit
@testable import WuuCore

private actor PersonalTransportProbe {
    var calls: [JSONValue] = []
    var failure: Error?
    func fail(_ value: Error?) { failure = value }
    func request(_ method: String, _ params: JSONValue) throws -> JSONValue {
        XCTAssertEqual(method, "plugin/client/request")
        calls.append(params)
        if let failure { throw failure }
        if params["method"].string == "agents.open" {
            return ["result": ["agent": ["agent_id": params["input"]["agent_id"], "user_space_id": "local"]]]
        }
        return ["result": ["state": "accepted"]]
    }
}

final class PersonalClientTests: XCTestCase {
    private var initialized: JSONValue {
        ["user_space_id": "local", "product_domain": "personal", "extension_inventory": [["id": "plugin:bundled:personal", "fingerprint": "generation-one", "runtime_state": "active"]]]
    }

    func testDiscoveryFailsClosedAndNeverUsesWorkspaceOrThreadRouting() async throws {
        let probe = PersonalTransportProbe()
        let client = PersonalClient { try await probe.request($0, $1) }
        do { _ = try await client.listAgents(); XCTFail("Undiscovered plugin admitted a request") }
        catch let error as PersonalClientError { XCTAssertEqual(error, .unavailable) }
        try await client.configure(initialized)
        _ = try await client.selectAgent("travel")
        let calls = await probe.calls
        XCTAssertEqual(calls.count, 1)
        XCTAssertEqual(calls[0]["id"].string, "plugin:bundled:personal")
        XCTAssertEqual(calls[0]["fingerprint"].string, "generation-one")
        XCTAssertEqual(calls[0]["user_space_id"].string, "local")
        XCTAssertEqual(calls[0]["product_domain"].string, "personal")
        XCTAssertEqual(calls[0]["input"], ["agent_id": "travel"])
        XCTAssertEqual(calls[0]["workspace_id"], .null)
        XCTAssertEqual(calls[0]["session_id"], .null)
        await client.invalidate()
        do { _ = try await client.history(); XCTFail("Invalidated selection survived") }
        catch let error as PersonalClientError { XCTAssertEqual(error, .noAgentSelected) }
    }

    func testFailedSendRetainsDraftIdentityAndDoesNotReplayAutomatically() async throws {
        let probe = PersonalTransportProbe()
        let client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized)
        _ = try await client.selectAgent("main")
        let draft = try await client.prepareMessage("Remember this")
        await probe.fail(NativeError.invalid("Connection lost"))
        do { _ = try await client.send(draft); XCTFail("Offline send was accepted") } catch {}
        let failedCalls = await probe.calls
        XCTAssertEqual(failedCalls.count, 2)
        await probe.fail(nil)
        _ = try await client.send(draft)
        let calls = await probe.calls
        XCTAssertEqual(calls[1]["input"]["request_id"], calls[2]["input"]["request_id"])
        XCTAssertNotEqual(calls[1]["request_id"], calls[2]["request_id"])
        XCTAssertEqual(calls[1]["user_input"], ["request_id": calls[1]["input"]["request_id"], "prompt": "Remember this"])
        XCTAssertEqual(calls[1]["user_input"], calls[2]["user_input"])
        XCTAssertEqual(calls[0]["user_input"], .null)
        _ = try await client.selectAgent("other")
        do { _ = try await client.send(draft); XCTFail("Draft crossed agents") }
        catch let error as PersonalClientError { XCTAssertEqual(error, .selectionChanged) }
    }

    func testIsolationFailureRemainsDistinctAndDoesNotFallback() async throws {
        let probe = PersonalTransportProbe()
        let client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized)
        await probe.fail(NativeError.invalid("wuu core exited: user_host_isolation_required: configure a verified host"))
        do { _ = try await client.listAgents(); XCTFail("Isolation gate accepted") }
        catch let error as PersonalClientError { XCTAssertEqual(error, .isolationRequired) }
        let calls = await probe.calls
        XCTAssertEqual(calls.count, 1)
    }

    func testTaskOwnershipAndSameAgentRecoveryNeverCreateAThread() async throws {
        let probe = PersonalTransportProbe()
        let client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized)
        _ = try await client.selectAgent("main")
        let draft = try await client.prepareMessage("Keep this request")
        await client.invalidate()
        try await client.configure(initialized)
        _ = try await client.selectAgent("main")
        _ = try await client.send(draft)
        let calls = await probe.calls
        XCTAssertEqual(calls.map { $0["method"].string ?? "" }, ["agents.open", "agents.open", "agents.send"])
        XCTAssertEqual(calls.last?["input"]["request_id"].string, draft.requestID)
        do { _ = try await client.task("foreign"); XCTFail("Invalid task ownership was accepted") }
        catch let error as PersonalClientError { XCTAssertEqual(error, .invalidResponse) }
    }

    func testAttachmentOnlySendCarriesTrustedBytesAndStableReferences() async throws {
        let probe = PersonalTransportProbe()
        let client = PersonalClient { try await probe.request($0, $1) }
        try await client.configure(initialized); _ = try await client.selectAgent("main")
        do { _ = try await client.prepareMessage(String(repeating: "a", count: 64_001)); XCTFail("Oversized Personal proof accepted") } catch {}
        let file = try InputAttachment(filename: "note.pdf", mediaType: "application/pdf", data: Data("%PDF-test".utf8))
        let draft = try await client.prepareMessage("", attachments: [file])
        _ = try await client.send(draft); _ = try await client.send(draft)
        let calls = await probe.calls
        let proof = calls[1]["user_input"]["attachments"].array[0]
        XCTAssertEqual(proof["data"].string, file.data.base64EncodedString())
        XCTAssertEqual(proof["sha256"].string, SHA256.hash(data: file.data).map { String(format: "%02x", $0) }.joined())
        XCTAssertEqual(calls[1]["input"]["attachments"].array[0]["data"], .null)
        XCTAssertEqual(calls[1]["input"], calls[2]["input"])
        XCTAssertEqual(calls[1]["user_input"], calls[2]["user_input"])
    }

    func testAttachmentReadRejectsCorruptDigestAndNeverUsesGlobalPaths() async throws {
        let client = PersonalClient { _, params in
            if params["method"].string == "agents.open" { return ["result": ["agent": ["agent_id": "main", "user_space_id": "local"]]] }
            XCTAssertEqual(params["method"].string, "agents.attachments.read")
            XCTAssertEqual(params["input"]["agent_id"].string, "main")
            XCTAssertEqual(params["input"]["session_id"], .null); XCTAssertEqual(params["input"]["path"], .null)
            return ["result": ["data": .string(Data("%PDF-corrupt".utf8).base64EncodedString()), "offset": 0, "total": 16, "content_type": "application/pdf"]]
        }
        try await client.configure(initialized); _ = try await client.selectAgent("main")
        do {
            _ = try await client.attachment(sequence: 1, descriptor: ["kind": "file", "index": 0, "sha256": .string(String(repeating: "0", count: 64)), "mime_type": "application/pdf", "name": "note.pdf"])
            XCTFail("Corrupt attachment accepted")
        } catch {}
    }

    func testInventoryRefreshUsesOnlyPreviouslyVerifiedLiveScope() async throws {
        let probe = PersonalTransportProbe()
        let client = PersonalClient { try await probe.request($0, $1) }
        let inventory: JSONValue = ["extension_inventory": [["id": "plugin:bundled:personal", "fingerprint": "generation-two", "runtime_state": "active"]]]
        do { try await client.configure(inventory); XCTFail("Missing immutable scope accepted") } catch {}
        try await client.configure(initialized)
        try await client.refreshInventory(inventory)
        _ = try await client.selectAgent("main")
        let calls = await probe.calls
        XCTAssertEqual(calls.last?["fingerprint"].string, "generation-two")
        await client.invalidate()
        do { try await client.refreshInventory(inventory); XCTFail("Inventory notification restored disconnected authority") } catch {}
    }

    func testHeldAttachmentSurvivesSameAgentRefreshButNotAgentSwitch() async throws {
        for changeAgent in [false, true] {
            let gate = PersonalAttachmentGate()
            let client = PersonalClient { try await gate.request($0, $1) }
            try await client.configure(initialized); _ = try await client.selectAgent("main")
            let bytes = Data("%PDF-test".utf8)
            let digest = SHA256.hash(data: bytes).map { String(format: "%02x", $0) }.joined()
            let download = Task { try await client.attachment(sequence: 1, descriptor: ["kind": "file", "index": 0, "sha256": .string(digest), "mime_type": "application/pdf", "name": "note.pdf"]) }
            await gate.waitForRequest()
            _ = try await client.selectAgent(changeAgent ? "other" : "main")
            await gate.release()
            if changeAgent {
                do { _ = try await download.value; XCTFail("Attachment crossed an agent switch") } catch {}
            } else { let file = try await download.value; XCTAssertEqual(file.data, bytes) }
        }
    }

    func testTaskArtifactProjectionKeepsAttachmentWithoutToolInternals() {
        let rows = personalDisplayRecords([["seq": 1, "role": "user", "content": "Draw this"],
            ["seq": 2, "role": "tool", "content": "private tool payload", "tool_result": "private result", "attachments": [["kind": "artifact", "index": 0, "name": "drawing.png"]]],
            ["seq": 3, "role": "system", "content": "private instructions"]])
        XCTAssertEqual(rows.count, 2); XCTAssertEqual(rows[1]["content"].string, "")
        XCTAssertEqual(rows[1]["attachments"].array.first?["name"].string, "drawing.png")
        XCTAssertEqual(rows[1]["tool_result"], .null)
    }

    func testReconnectFencesLateResultsEvenWithSameFingerprint() async throws {
        let gate = PersonalResponseGate()
        let client = PersonalClient { _, _ in await gate.request() }
        try await client.configure(initialized)
        let pending = Task { try await client.listAgents() }
        await gate.waitForRequest()
        try await client.configure(initialized)
        await gate.release()
        do { _ = try await pending.value; XCTFail("Late result crossed connection generation") }
        catch let error as PersonalClientError { XCTAssertEqual(error, .selectionChanged) }
    }
}

private actor PersonalResponseGate {
    private var response: CheckedContinuation<JSONValue, Never>?
    private var started: CheckedContinuation<Void, Never>?
    func request() async -> JSONValue {
        await withCheckedContinuation { continuation in
            response = continuation
            started?.resume(); started = nil
        }
    }
    func waitForRequest() async {
        if response != nil { return }
        await withCheckedContinuation { started = $0 }
    }
    func release() { response?.resume(returning: ["result": ["agents": []]]); response = nil }
}

private actor PersonalAttachmentGate {
    private var response: CheckedContinuation<JSONValue, Never>?
    private var started: CheckedContinuation<Void, Never>?
    func request(_ method: String, _ params: JSONValue) async throws -> JSONValue {
        if params["method"].string == "agents.open" { return ["result": ["agent": ["agent_id": params["input"]["agent_id"], "user_space_id": "local"]]] }
        return await withCheckedContinuation { response = $0; started?.resume(); started = nil }
    }
    func waitForRequest() async { if response == nil { await withCheckedContinuation { started = $0 } } }
    func release() {
        let data = Data("%PDF-test".utf8).base64EncodedString()
        response?.resume(returning: ["result": ["data": .string(data), "offset": 0, "total": .number(Double(data.utf8.count)), "content_type": "application/pdf"]]); response = nil
    }
}
