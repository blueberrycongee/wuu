import XCTest
@testable import CUAMacCore

private final class GatedBackend: ComputerBackend, @unchecked Sendable {
    let started = DispatchSemaphore(value: 0)
    let proceed = DispatchSemaphore(value: 0)
    func perform(_ command: ComputerCommand) throws -> ComputerResult {
        if command.action == .typeText {
            try ComputerExecution.input()
            started.signal()
            guard proceed.wait(timeout: .now() + 2) == .success else { throw ComputerError.operationFailed("test gate timeout") }
            try ComputerExecution.input()
        }
        return ComputerResult(text: "ok")
    }
}

final class ExecutionTests: XCTestCase {
    func testCancellationInterruptsRunningRequestAndQueueRecovers() {
        let backend = GatedBackend()
        let queue = MCPRequestQueue(backend: backend, queue: DispatchQueue(label: "test.cua"))
        let cancelled = expectation(description: "cancelled response")
        queue.submit(["id": 1, "method": "tools/call", "params": ["name": "computer", "arguments": ["action": "type_text", "text": "hello"]]]) { response in
            let result = response["result"] as? [String: Any]
            let evidence = result?["structuredContent"] as? [String: Any]
            XCTAssertEqual(result?["isError"] as? Bool, true)
            XCTAssertEqual(evidence?["error_code"] as? String, "cancelled")
            XCTAssertEqual(evidence?["delivery"] as? String, "unknown")
            XCTAssertEqual(evidence?["input_units_attempted"] as? Int, 1)
            cancelled.fulfill()
        }
        XCTAssertEqual(backend.started.wait(timeout: .now() + 2), .success)
        queue.submit(["method": "notifications/cancelled", "params": ["requestId": 1]]) { _ in XCTFail("notification replied") }
        backend.proceed.signal()
        let recovered = expectation(description: "next request")
        queue.submit(["id": 2, "method": "tools/call", "params": ["name": "computer", "arguments": ["action": "permission_status"]]]) { response in
            XCTAssertEqual((response["result"] as? [String: Any])?["isError"] as? Bool, false)
            recovered.fulfill()
        }
        wait(for: [cancelled, recovered], timeout: 3)
    }

    func testCancelledBeforeDispatchDoesNotDeliverInput() throws {
        let execution = ComputerExecution()
        execution.cancel()
        let server = MCPServer(backend: GatedBackend())
        let response = try server.handle(["id": 1, "method": "tools/call", "params": ["name": "computer", "arguments": ["action": "type_text"]]], execution: execution)
        let result = response?["result"] as? [String: Any]
        XCTAssertEqual((result?["structuredContent"] as? [String: Any])?["delivery"] as? String, "not_delivered")
    }

    func testDeadlineAndUnicodeChunkBoundaries() throws {
        let execution = ComputerExecution(timeout: -1)
        XCTAssertThrowsError(try execution.beginInput())
        let text = String(repeating: "a🫐", count: 30)
        let chunks = unicodeChunks(text)
        XCTAssertTrue(chunks.allSatisfy { $0.count <= 16 })
        XCTAssertEqual(chunks.map { String(decoding: $0, as: UTF16.self) }.joined(), text)
    }
}
